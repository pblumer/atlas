package exchange

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/pblumer/atlas/connector/exchange/catalog"
	"github.com/pblumer/atlas/limits"
)

// defaultOnPremTimeout bounds one cmdlet against Exchange Server: starting pwsh, opening
// the Remote PowerShell session, importing the one command and running it. Opening the
// session alone takes seconds, so this is longer than a REST call's bound; it is still
// a bound, because a session that never answers must fail the job visibly rather than
// hold the worker.
const defaultOnPremTimeout = 2 * time.Minute

// defaultPowerShell is the interpreter the worker starts. PowerShell 7 runs on Windows
// and Linux; Windows PowerShell 5.1 (powershell.exe) works too and is what a domain
// member without PowerShell 7 has.
const defaultPowerShell = "pwsh"

// The authentication mechanisms a Remote PowerShell session to Exchange Server may use.
// Kerberos is the default because it is what Exchange's /PowerShell endpoint is
// configured for out of the box, and because it can run as the worker's own service
// account with no password in any configuration.
var onPremAuth = map[string]string{
	"kerberos": "Kerberos", "negotiate": "Negotiate", "basic": "Basic", "default": "Default",
}

// OnPremConfig is what an [OnPremClient] is built from.
type OnPremConfig struct {
	// ConnectionURI is the Exchange server's Remote PowerShell endpoint,
	// http://exch01.contoso.local/PowerShell/.
	ConnectionURI string
	// Authentication is Kerberos (the default), Negotiate, Basic or Default.
	Authentication string
	// Username and Password are an explicit account. Both empty runs the session as
	// the worker's own identity.
	Username string
	Password string
	// PowerShell is the interpreter; empty is pwsh.
	PowerShell string
	// Timeout bounds one call; zero is two minutes.
	Timeout time.Duration
}

// Runner starts a program and returns what it wrote. It is an interface so the backend
// is testable without PowerShell or an Exchange server.
type Runner interface {
	Run(ctx context.Context, bin string, args, env []string) (stdout, stderr []byte, err error)
}

// OnPremClient runs cmdlets on Exchange Server through Remote PowerShell
// (ADR-0462). Exchange Server has no REST surface for its management
// cmdlets — Remote PowerShell to its Microsoft.Exchange endpoint is the supported way
// in — so this backend starts PowerShell on the worker host for each call.
//
// What it starts is a fixed program, [onPremBootstrap]. The cmdlet name and every
// parameter travel to it as JSON in the environment and are bound by splatting, so no
// authored value is ever parsed as PowerShell; and the program runs only a command the
// Exchange endpoint exported, so a name like Get-Content cannot reach the worker's own
// file system.
type OnPremClient struct {
	uri        string
	auth       string
	username   string
	password   string
	powershell string
	timeout    time.Duration
	run        Runner
	environ    func() []string
}

// NewOnPremClient checks a configuration and builds a client from it.
func NewOnPremClient(c OnPremConfig) (*OnPremClient, error) {
	uri := strings.TrimSpace(c.ConnectionURI)
	if uri == "" {
		return nil, fmt.Errorf("exchange: an on-premises Worker needs the connection URI of the Exchange server's Remote PowerShell endpoint (http://exch01.contoso.local/PowerShell/)")
	}
	u, err := url.Parse(uri)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("exchange: the connection URI %q must be an http or https URL naming the Exchange server", uri)
	}
	authName := strings.ToLower(strings.TrimSpace(c.Authentication))
	if authName == "" {
		authName = "kerberos"
	}
	auth, ok := onPremAuth[authName]
	if !ok {
		return nil, fmt.Errorf("exchange: unknown authentication %q (want Kerberos, Negotiate, Basic or Default)", c.Authentication)
	}
	if (c.Username == "") != (c.Password == "") {
		if c.Username == "" {
			return nil, fmt.Errorf("exchange: a password is configured without a username")
		}
		return nil, fmt.Errorf("exchange: username %q is configured without a password", c.Username)
	}
	if auth == "Basic" {
		if c.Username == "" {
			return nil, fmt.Errorf("exchange: Basic authentication needs a username and a password")
		}
		// Basic sends the password itself; over http that is the password on the wire.
		if u.Scheme != "https" {
			return nil, fmt.Errorf("exchange: Basic authentication sends the password as it is, so the connection URI must be https")
		}
	}
	ps := strings.TrimSpace(c.PowerShell)
	if ps == "" {
		ps = defaultPowerShell
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = defaultOnPremTimeout
	}
	return &OnPremClient{
		uri: uri, auth: auth, username: c.Username, password: c.Password,
		powershell: ps, timeout: timeout, run: execRunner{}, environ: os.Environ,
	}, nil
}

// Target is [catalog.OnPrem].
func (c *OnPremClient) Target() catalog.Target { return catalog.OnPrem }

// onPremRequest is what the bootstrap reads from ATLAS_EXCHANGE_REQUEST.
type onPremRequest struct {
	Cmdlet     string         `json:"cmdlet"`
	Parameters map[string]any `json:"parameters"`
	Limit      int            `json:"limit"`
}

// onPremAnswer is the one line the bootstrap writes.
type onPremAnswer struct {
	OK    bool   `json:"ok"`
	Value *[]any `json:"value"`
	Error *struct {
		Message string `json:"message"`
		Reason  string `json:"reason"`
		ErrorID string `json:"errorId"`
	} `json:"error"`
}

// Invoke runs one cmdlet in a fresh Remote PowerShell session.
//
// A session per call is the price of this backend, and it is paid knowingly: a
// long-lived session would save seconds per job and cost a process to supervise, a
// session to expire and re-open, and calls that are no longer independent of one
// another. A worker process that is slow and plain is easier to trust with an
// organization's mailboxes than one that is fast and stateful.
func (c *OnPremClient) Invoke(ctx context.Context, inv catalog.Invocation, limit int) ([]any, error) {
	req, err := json.Marshal(onPremRequest{Cmdlet: inv.Cmdlet, Parameters: inv.Parameters, Limit: limit})
	if err != nil {
		return nil, fmt.Errorf("exchange: encode %s: %w", inv.Cmdlet, err)
	}
	env := childEnv(c.environ())
	env = append(env,
		"ATLAS_EXCHANGE_REQUEST="+string(req),
		"ATLAS_EXCHANGE_URI="+c.uri,
		"ATLAS_EXCHANGE_AUTHENTICATION="+c.auth,
	)
	if c.username != "" {
		env = append(env, "ATLAS_EXCHANGE_USERNAME="+c.username, "ATLAS_EXCHANGE_PASSWORD="+c.password)
	}
	args := []string{"-NoProfile", "-NonInteractive", "-NoLogo", "-EncodedCommand", encodeCommand(onPremBootstrap)}
	runCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	stdout, stderr, runErr := c.run.Run(runCtx, c.powershell, args, env)
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
		return nil, fmt.Errorf("exchange: %s did not answer within %s (is %s reachable from this worker, and does the session open?)", inv.Cmdlet, c.timeout, c.uri)
	}
	ans, ok := lastAnswer(stdout)
	if !ok {
		quote := strings.TrimSpace(string(stderr))
		if quote == "" {
			quote = strings.TrimSpace(string(stdout))
		}
		if max := int(limits.Default().ErrorBody); len(quote) > max {
			quote = quote[:max] + "…"
		}
		if runErr != nil {
			return nil, fmt.Errorf("exchange: %s: %s could not run the call (%v): %s", inv.Cmdlet, c.powershell, runErr, quote)
		}
		return nil, fmt.Errorf("exchange: %s: %s wrote something that is not the bootstrap's answer: %s", inv.Cmdlet, c.powershell, quote)
	}
	if !ans.OK {
		if ans.Error == nil {
			return nil, fmt.Errorf("exchange: %s failed without saying why", inv.Cmdlet)
		}
		msg := strings.TrimSpace(ans.Error.Message)
		if r := strings.TrimSpace(ans.Error.Reason); r != "" {
			msg += " (" + r + ")"
		}
		return nil, fmt.Errorf("exchange: %s failed: %s", inv.Cmdlet, msg)
	}
	if ans.Value == nil {
		return nil, fmt.Errorf("exchange: %s answered without a %q list", inv.Cmdlet, "value")
	}
	return *ans.Value, nil
}

// lastAnswer reads the bootstrap's answer: the last non-empty line of its output, which
// is the only line it writes — anything above it came from somewhere else.
func lastAnswer(stdout []byte) (onPremAnswer, bool) {
	lines := strings.Split(strings.TrimSpace(string(stdout)), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if !strings.HasPrefix(last, "{") {
		return onPremAnswer{}, false
	}
	var ans onPremAnswer
	dec := json.NewDecoder(strings.NewReader(last))
	dec.UseNumber()
	if err := dec.Decode(&ans); err != nil {
		return onPremAnswer{}, false
	}
	return ans, true
}

// childEnv is the environment pwsh inherits: the worker's own, which Kerberos, the
// module path and the interpreter itself depend on, without any ATLAS_ variable. Those
// carry the worker's Atlas token and every other Worker's credentials, none of which
// this child needs.
func childEnv(parent []string) []string {
	out := make([]string, 0, len(parent))
	for _, kv := range parent {
		if strings.HasPrefix(strings.ToUpper(kv), "ATLAS_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// encodeCommand is the form -EncodedCommand takes: base64 of UTF-16LE. It is used
// instead of -Command so the program reaches PowerShell byte for byte on every
// platform, with no command-line quoting in between.
func encodeCommand(script string) string {
	u := utf16.Encode([]rune(script))
	b := make([]byte, 2*len(u))
	for i, r := range u {
		binary.LittleEndian.PutUint16(b[2*i:], r)
	}
	return base64.StdEncoding.EncodeToString(b)
}

// execRunner is the production [Runner].
type execRunner struct{}

func (execRunner) Run(ctx context.Context, bin string, args, env []string) ([]byte, []byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = env
	stdout := &capped{max: limits.Default().Payload}
	stderr := &capped{max: limits.Default().ErrorBody}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	// Once the context ends the process is killed; this bounds how long a pipe held
	// open by anything it started can keep the call waiting after that.
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	if stdout.over {
		return nil, stderr.Bytes(), fmt.Errorf("the answer was larger than the %d byte payload budget", stdout.max)
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

// capped keeps the first max bytes written to it and discards the rest, so a child that
// writes without end neither grows this process nor blocks on a full pipe.
type capped struct {
	bytes.Buffer
	max  int64
	over bool
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - int64(c.Len()); room > 0 {
		if int64(len(p)) > room {
			c.Buffer.Write(p[:room])
			c.over = true
		} else {
			c.Buffer.Write(p)
		}
	} else if len(p) > 0 {
		c.over = true
	}
	return len(p), nil
}

// onPremBootstrap is the program pwsh runs for every on-premises call. It is fixed
// text: nothing authored is ever spliced into it.
//
// It must run on Windows PowerShell 5.1 as well as on PowerShell 7, which is why it
// converts JSON objects to hashtables by hand (5.1 has no ConvertFrom-Json
// -AsHashtable) and writes its answer as UTF-8 bytes to the raw output stream (5.1
// would otherwise encode it in the console's code page, and a display name with an
// umlaut would arrive mangled).
//
// The steps, each for a reason:
//   - parameters are rebuilt from JSON, objects becoming the hashtables Exchange takes
//     for multi-valued changes (@{Add=…}), and splatted — a value is data, never code;
//   - only the one command is imported from the session, and it is invoked through the
//     imported module, so a name the Exchange endpoint does not export fails instead of
//     resolving to a local command of the same name;
//   - a change is made with -Confirm:$false, because a prompt in a non-interactive
//     session is a failure, and with -ErrorAction Stop, so an error is an error and not
//     a warning the job completes over;
//   - a limit stops the pipeline after limit objects, so an oversized listing is not
//     read in full to then be refused;
//   - every object is flattened to its properties, complex values to their text, which
//     is the form the REST endpoint returns them in too.
const onPremBootstrap = `$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$WarningPreference = 'SilentlyContinue'

function ConvertTo-AtlasParameter($v) {
    if ($null -eq $v) { return $null }
    if ($v -is [System.Management.Automation.PSCustomObject]) {
        $h = @{}
        foreach ($p in $v.PSObject.Properties) {
            if ($p.Name -ne '@odata.type') { $h[$p.Name] = ConvertTo-AtlasParameter $p.Value }
        }
        return $h
    }
    if ($v -is [System.Array]) { return ,@(foreach ($e in $v) { ConvertTo-AtlasParameter $e }) }
    return $v
}

function ConvertTo-AtlasValue($v) {
    if ($null -eq $v) { return $null }
    if ($v -is [string] -or $v -is [bool]) { return $v }
    if ($v -is [int] -or $v -is [long] -or $v -is [double] -or $v -is [decimal] -or $v -is [single] -or $v -is [int16] -or $v -is [byte] -or $v -is [uint16] -or $v -is [uint32] -or $v -is [uint64]) { return $v }
    if ($v -is [datetime]) { return $v.ToUniversalTime().ToString('o') }
    if ($v -is [System.Collections.IEnumerable]) {
        return ,@(foreach ($e in $v) { if ($null -eq $e) { $null } else { [string]$e } })
    }
    return [string]$v
}

function ConvertTo-AtlasObject($o) {
    if ($null -eq $o) { return $null }
    if ($o -is [string] -or $o -is [ValueType]) { return ConvertTo-AtlasValue $o }
    $h = [ordered]@{}
    foreach ($p in $o.PSObject.Properties) {
        if ($p.Name -in @('PSComputerName', 'RunspaceId', 'PSShowComputerName')) { continue }
        try { $val = $p.Value } catch { $val = $null }
        $h[$p.Name] = ConvertTo-AtlasValue $val
    }
    return $h
}

$result = @{ ok = $false }
$session = $null
try {
    $req = $env:ATLAS_EXCHANGE_REQUEST | ConvertFrom-Json
    $name = [string]$req.cmdlet
    $params = @{}
    if ($null -ne $req.parameters) {
        foreach ($p in $req.parameters.PSObject.Properties) { $params[$p.Name] = ConvertTo-AtlasParameter $p.Value }
    }
    $open = @{
        ConfigurationName = 'Microsoft.Exchange'
        ConnectionUri     = $env:ATLAS_EXCHANGE_URI
        Authentication    = $env:ATLAS_EXCHANGE_AUTHENTICATION
        ErrorAction       = 'Stop'
    }
    if ($env:ATLAS_EXCHANGE_USERNAME) {
        $secret = ConvertTo-SecureString -String $env:ATLAS_EXCHANGE_PASSWORD -AsPlainText -Force
        $open.Credential = New-Object System.Management.Automation.PSCredential($env:ATLAS_EXCHANGE_USERNAME, $secret)
    }
    $session = New-PSSession @open
    $module = Import-PSSession -Session $session -CommandName $name -DisableNameChecking -AllowClobber -ErrorAction Stop
    $command = $null
    foreach ($c in $module.ExportedCommands.Values) { if ($c.Name -eq $name) { $command = $c } }
    if ($null -eq $command) { throw "The Exchange endpoint offers no command $name to this account." }
    if (-not $name.StartsWith('Get-', [System.StringComparison]::OrdinalIgnoreCase) -and $command.Parameters.ContainsKey('Confirm')) {
        $params['Confirm'] = $false
    }
    $params['ErrorAction'] = 'Stop'
    $limit = 0
    if ($null -ne $req.limit) { $limit = [int]$req.limit }
    $items = New-Object System.Collections.Generic.List[object]
    if ($limit -gt 0) {
        & $command @params | Select-Object -First $limit | ForEach-Object { $items.Add((ConvertTo-AtlasObject $_)) }
    } else {
        & $command @params | ForEach-Object { $items.Add((ConvertTo-AtlasObject $_)) }
    }
    $result = @{ ok = $true; value = $items.ToArray() }
} catch {
    $e = $_
    $result = @{ ok = $false; error = @{
        message = [string]$e.Exception.Message
        reason  = [string]$e.CategoryInfo.Reason
        errorId = [string]$e.FullyQualifiedErrorId
    } }
} finally {
    if ($null -ne $session) { Remove-PSSession -Session $session -ErrorAction SilentlyContinue }
}
$json = ConvertTo-Json -InputObject $result -Depth 6 -Compress
$bytes = (New-Object System.Text.UTF8Encoding($false)).GetBytes($json + [Environment]::NewLine)
$out = [Console]::OpenStandardOutput()
$out.Write($bytes, 0, $bytes.Length)
$out.Flush()
`
