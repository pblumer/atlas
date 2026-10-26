package teamsphone

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// operationsScript is the PowerShell every job runs. It is embedded rather than read
// from disk so the worker binary and the operations it performs cannot be out of step.
//
//go:embed operations.ps1
var operationsScript string

// bootstrap is the fixed program pwsh is started with. It reads the job — the script and
// the request — from stdin as one JSON document, so neither the certificate nor the
// script text appears in the command line any process on the host can list. It reads
// stdin as UTF-8 explicitly: [Console]::In decodes with the console's code page, which on
// a Windows server is rarely UTF-8.
const bootstrap = `$ErrorActionPreference='Stop';` +
	`$r=[IO.StreamReader]::new([Console]::OpenStandardInput(),[Text.UTF8Encoding]::new($false)).ReadToEnd()|ConvertFrom-Json -AsHashtable;` +
	`& ([scriptblock]::Create($r.script)) -Request $r.request`

// DefaultTimeout bounds one job: importing the module, signing in and the cmdlets. It
// stays under the default job lease, so a run that hangs is killed by the worker before
// the engine gives the job to somebody else.
const DefaultTimeout = 4 * time.Minute

// maxStdout and maxStderr bound what a run may write. The answer is a handful of ids;
// stderr is quoted into an error message.
const (
	maxStdout int64 = 1 << 20
	maxStderr int64 = 32 << 10
)

// childEnvNames is the part of the worker's environment pwsh is given: what it needs to
// start, find its modules and reach the internet through the host's proxy. It is an
// allowlist, so a credential the worker holds under a name added later never reaches
// the child; the certificate travels on stdin, never here.
var childEnvNames = []string{
	"PATH", "HOME", "USERPROFILE", "USERNAME", "USERDOMAIN", "COMPUTERNAME",
	"APPDATA", "LOCALAPPDATA", "ProgramData", "ProgramFiles", "ProgramFiles(x86)",
	"TMPDIR", "TMP", "TEMP",
	"LANG", "LANGUAGE", "LC_ALL", "LC_CTYPE", "TZ",
	"SYSTEMROOT", "SystemDrive", "WINDIR", "COMSPEC", "PATHEXT",
	"PSModulePath",
	"HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY", "https_proxy", "http_proxy", "no_proxy",
}

// Credential is how the worker signs in to one tenant: the app's client id, and its
// certificate either as PEM (certificate and unencrypted key) or by the thumbprint of a
// certificate already in the worker host's store. Exactly one of the two is set.
type Credential struct {
	TenantID              string
	ClientID              string
	Certificate           string
	CertificateThumbprint string
}

// Pwsh is the production [Client]: it runs the shipped operations in a fresh pwsh
// process per job.
type Pwsh struct {
	Credential Credential
	// Bin is the PowerShell 7 executable; empty means "pwsh" on the PATH.
	Bin string
	// Timeout bounds one run; zero means [DefaultTimeout].
	Timeout time.Duration

	// AccountWait bounds how long an ensure of a voice app waits for Teams to show a
	// resource account created moments before; zero means [DefaultAccountWait]. The
	// built-in worker retries a failed job at once, so waiting here is what spans
	// Teams' synchronization delay instead of spending the retries in seconds.
	AccountWait time.Duration

	// graphRoot and loginRoot replace Microsoft's endpoints. Only this package's tests
	// set them, to run the shipped script against a local stand-in.
	graphRoot, loginRoot string
}

// DefaultAccountWait is how long an ensure waits for Teams to show a new resource
// account. It and the sign-in stay well inside [DefaultTimeout].
const DefaultAccountWait = 2 * time.Minute

// Do runs one prepared operation.
func (p *Pwsh) Do(ctx context.Context, req Request) (map[string]any, error) {
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	wait := p.AccountWait
	if wait <= 0 {
		wait = DefaultAccountWait
	}
	request := map[string]any{
		"operation":          req.Operation,
		"args":               req.Args,
		"reconcile":          req.Reconcile,
		"tenantId":           p.Credential.TenantID,
		"clientId":           p.Credential.ClientID,
		"accountWaitSeconds": int(wait / time.Second),
	}
	if p.graphRoot != "" {
		request["graphRoot"], request["loginRoot"] = p.graphRoot, p.loginRoot
	}
	if p.Credential.Certificate != "" {
		request["certificate"] = p.Credential.Certificate
	} else {
		request["certificateThumbprint"] = p.Credential.CertificateThumbprint
	}
	stdin, err := asciiJSON(map[string]any{"script": operationsScript, "request": request})
	if err != nil {
		return nil, fmt.Errorf("teamsphone: encode the request: %w", err)
	}

	bin := p.Bin
	if bin == "" {
		bin = "pwsh"
	}
	cmd := exec.CommandContext(ctx, bin, "-NoProfile", "-NonInteractive", "-NoLogo", "-Command", bootstrap)
	cmd.Env = childEnvironment()
	cmd.Stdin = bytes.NewReader(stdin)
	// Bounded by the run's own deadline: a process that inherited stdout and outlived
	// pwsh must not hold the job after it.
	cmd.WaitDelay = 5 * time.Second
	stdout, stderr := &capped{max: maxStdout}, &capped{max: maxStderr}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	runErr := cmd.Run()

	if errors.Is(runErr, exec.ErrNotFound) || errors.Is(runErr, os.ErrNotExist) {
		return nil, fmt.Errorf("teamsphone: cannot start %s: install PowerShell 7 and the MicrosoftTeams module on the worker host, or set ATLAS_TEAMSPHONE_PWSH to the pwsh executable", bin)
	}
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("teamsphone: %s did not finish within %s and was stopped%s", req.Operation, timeout, quote(stderr))
	}
	if stdout.dropped {
		return nil, fmt.Errorf("teamsphone: %s wrote more than %d bytes of output", req.Operation, maxStdout)
	}
	env, ok := lastEnvelope(stdout.buf.Bytes())
	if !ok {
		if runErr != nil {
			return nil, fmt.Errorf("teamsphone: %s failed: %v%s", req.Operation, runErr, quote(stderr))
		}
		return nil, fmt.Errorf("teamsphone: %s answered with no result%s", req.Operation, quote(stderr))
	}
	if !env.OK {
		msg := strings.TrimSpace(env.Error)
		// A code the script names is one of the shipped refusals; one outside [Codes]
		// is no promise a model can catch, so it fails like any other error.
		if slices.Contains(Codes, env.Code) {
			return nil, refused(env.Code, req.Operation, msg)
		}
		return nil, fmt.Errorf("teamsphone: %s: %s", req.Operation, msg)
	}
	if env.Result == nil {
		return map[string]any{}, nil
	}
	return env.Result, nil
}

type envelope struct {
	OK     bool           `json:"ok"`
	Result map[string]any `json:"result"`
	Error  string         `json:"error"`
	// Code names the refusal the error is, when it is one of [Codes].
	Code string `json:"code,omitempty"`
}

// lastEnvelope finds the answer: the last line of stdout that decodes as one. A module
// that writes to the output stream despite the script's care then costs nothing.
func lastEnvelope(out []byte) (envelope, bool) {
	lines := strings.Split(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, `{"ok":`) {
			continue
		}
		dec := json.NewDecoder(strings.NewReader(line))
		dec.UseNumber()
		var env envelope
		if err := dec.Decode(&env); err == nil {
			return env, true
		}
	}
	return envelope{}, false
}

// asciiJSON encodes v with every non-ASCII character escaped, so a display name with an
// umlaut arrives intact whatever encoding the reading side assumes.
func asciiJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.Grow(len(raw))
	for len(raw) > 0 {
		r, size := utf8.DecodeRune(raw)
		switch {
		case r < utf8.RuneSelf:
			b.WriteByte(raw[0])
		case r > 0xFFFF:
			r1, r2 := utf16.EncodeRune(r)
			fmt.Fprintf(&b, `\u%04x\u%04x`, r1, r2)
		default:
			fmt.Fprintf(&b, `\u%04x`, r)
		}
		raw = raw[size:]
	}
	return b.Bytes(), nil
}

func childEnvironment() []string {
	env := make([]string, 0, len(childEnvNames))
	for _, name := range childEnvNames {
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}
	return env
}

func quote(stderr *capped) string {
	msg := strings.TrimSpace(stderr.buf.String())
	if msg == "" {
		return ""
	}
	return ": " + msg
}

// capped is a writer that stops growing at max and remembers that it did. The buffer is
// a field rather than embedded: an embedded bytes.Buffer would promote ReadFrom, which
// io.Copy prefers, and the ceiling would silently do nothing.
type capped struct {
	buf     bytes.Buffer
	max     int64
	dropped bool
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - int64(c.buf.Len()); room > 0 {
		if int64(len(p)) <= room {
			return c.buf.Write(p)
		}
		c.buf.Write(p[:room])
	}
	c.dropped = true
	return len(p), nil
}
