package exchange

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/pblumer/atlas/connector/exchange/catalog"
)

// fakeRunner stands in for pwsh: it records how it was started and answers what the
// test told it to.
type fakeRunner struct {
	bin    string
	args   []string
	env    []string
	stdout string
	stderr string
	err    error
	block  bool
}

func (f *fakeRunner) Run(ctx context.Context, bin string, args, env []string) ([]byte, []byte, error) {
	f.bin, f.args, f.env = bin, args, env
	if f.block {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}
	return []byte(f.stdout), []byte(f.stderr), f.err
}

func (f *fakeRunner) envValue(name string) (string, bool) {
	for _, kv := range f.env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == name {
			return v, true
		}
	}
	return "", false
}

func onprem(t *testing.T, r *fakeRunner, cfg OnPremConfig) *OnPremClient {
	t.Helper()
	if cfg.ConnectionURI == "" {
		cfg.ConnectionURI = "http://exch01.contoso.local/PowerShell/"
	}
	c, err := NewOnPremClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c.run = r
	c.environ = func() []string {
		return []string{"PATH=/usr/bin", "KRB5CCNAME=FILE:/tmp/krb5cc_1000", "ATLAS_EXCHANGE_CONNECTORS=onprem", "ATLAS_EXCHANGE_ONPREM_CREDENTIALS={\"password\":\"x\"}", "ATLAS_API_TOKEN=secret"}
	}
	return c
}

func decodeEncodedCommand(t *testing.T, b64 string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatal(err)
	}
	u := make([]uint16, len(raw)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(raw[2*i:])
	}
	return string(utf16.Decode(u))
}

// TestOnPremRunsTheFixedBootstrap: pwsh is started with the worker's own program, never
// with anything a model authored — the cmdlet and its parameters travel as data in the
// environment, so no value is ever parsed as PowerShell.
func TestOnPremRunsTheFixedBootstrap(t *testing.T) {
	r := &fakeRunner{stdout: `{"ok":true,"value":[{"Name":"Anna"}]}`}
	c := onprem(t, r, OnPremConfig{Username: `CONTOSO\svc-atlas`, Password: "pa$$", Authentication: "Kerberos"})
	inv := catalog.Invocation{Cmdlet: "Get-Mailbox", Parameters: map[string]any{"Identity": "anna; Remove-Mailbox x"}}
	out, err := c.Invoke(context.Background(), inv, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].(map[string]any)["Name"] != "Anna" {
		t.Errorf("out = %v", out)
	}
	if r.bin != "pwsh" {
		t.Errorf("bin = %q", r.bin)
	}
	if len(r.args) != 5 || r.args[0] != "-NoProfile" || r.args[1] != "-NonInteractive" || r.args[3] != "-EncodedCommand" {
		t.Fatalf("args = %v", r.args)
	}
	if got := decodeEncodedCommand(t, r.args[4]); got != onPremBootstrap {
		t.Error("pwsh was not started with the bootstrap")
	}
	for _, a := range r.args {
		if strings.Contains(a, "Remove-Mailbox") || strings.Contains(a, "pa$$") {
			t.Errorf("an authored value or the password reached the command line: %q", a)
		}
	}
	reqRaw, ok := r.envValue("ATLAS_EXCHANGE_REQUEST")
	if !ok {
		t.Fatal("no request in the environment")
	}
	var req struct {
		Cmdlet     string         `json:"cmdlet"`
		Parameters map[string]any `json:"parameters"`
		Limit      int            `json:"limit"`
	}
	if err := json.Unmarshal([]byte(reqRaw), &req); err != nil {
		t.Fatal(err)
	}
	if req.Cmdlet != "Get-Mailbox" || req.Parameters["Identity"] != "anna; Remove-Mailbox x" || req.Limit != 5 {
		t.Errorf("request = %+v", req)
	}
	for name, want := range map[string]string{
		"ATLAS_EXCHANGE_URI":            "http://exch01.contoso.local/PowerShell/",
		"ATLAS_EXCHANGE_AUTHENTICATION": "Kerberos",
		"ATLAS_EXCHANGE_USERNAME":       `CONTOSO\svc-atlas`,
		"ATLAS_EXCHANGE_PASSWORD":       "pa$$",
		"KRB5CCNAME":                    "FILE:/tmp/krb5cc_1000",
		"PATH":                          "/usr/bin",
	} {
		if got, _ := r.envValue(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	// The worker's own credentials — its Atlas token, the other Workers' bundles — are
	// nothing this child needs.
	for _, name := range []string{"ATLAS_API_TOKEN", "ATLAS_EXCHANGE_CONNECTORS", "ATLAS_EXCHANGE_ONPREM_CREDENTIALS"} {
		if _, ok := r.envValue(name); ok {
			t.Errorf("%s was handed to pwsh", name)
		}
	}
}

// TestOnPremWithoutACredentialUsesTheWorkersIdentity: Kerberos as the worker's own
// service account is the arrangement that keeps a password out of every configuration.
func TestOnPremWithoutACredentialUsesTheWorkersIdentity(t *testing.T) {
	r := &fakeRunner{stdout: `{"ok":true,"value":[]}`}
	c := onprem(t, r, OnPremConfig{})
	if _, err := c.Invoke(context.Background(), catalog.Invocation{Cmdlet: "Get-Mailbox"}, 0); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.envValue("ATLAS_EXCHANGE_USERNAME"); ok {
		t.Error("a username was sent without one being configured")
	}
	if got, _ := r.envValue("ATLAS_EXCHANGE_AUTHENTICATION"); got != "Kerberos" {
		t.Errorf("authentication = %q", got)
	}
	if c.Target() != catalog.OnPrem {
		t.Errorf("target = %v", c.Target())
	}
}

func TestOnPremReportsExchangesError(t *testing.T) {
	cases := []struct {
		name string
		r    fakeRunner
		want []string
	}{
		{"cmdlet error", fakeRunner{stdout: `{"ok":false,"error":{"message":"The operation couldn't be performed because object 'x' couldn't be found.","reason":"ManagementObjectNotFoundException","errorId":"[Server=EXCH01],RecipientTaskException"}}`},
			[]string{"Get-Mailbox", "couldn't be found", "ManagementObjectNotFoundException"}},
		{"error with exit status", fakeRunner{stdout: `{"ok":false,"error":{"message":"Connecting to remote server failed: Access is denied."}}`, err: errors.New("exit status 1")},
			[]string{"Access is denied"}},
		{"no JSON", fakeRunner{stdout: "", stderr: "pwsh: command not found", err: errors.New("exit status 127")},
			[]string{"exit status 127", "command not found"}},
		{"garbage", fakeRunner{stdout: "Welcome to PowerShell"}, []string{"not the bootstrap's answer"}},
		{"ok without value", fakeRunner{stdout: `{"ok":true}`}, []string{"value"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.r
			c := onprem(t, &r, OnPremConfig{})
			_, err := c.Invoke(context.Background(), catalog.Invocation{Cmdlet: "Get-Mailbox"}, 0)
			if err == nil {
				t.Fatal("no error")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not mention %q", err, w)
				}
			}
			if !strings.HasPrefix(err.Error(), "exchange:") {
				t.Errorf("error %q does not name the worker type", err)
			}
		})
	}
}

// TestOnPremTimesOut: a Remote PowerShell session that never answers fails the job
// visibly instead of holding the worker.
func TestOnPremTimesOut(t *testing.T) {
	r := &fakeRunner{block: true}
	c := onprem(t, r, OnPremConfig{Timeout: 20 * time.Millisecond})
	_, err := c.Invoke(context.Background(), catalog.Invocation{Cmdlet: "Get-Mailbox"}, 0)
	if err == nil || !strings.Contains(err.Error(), "did not answer within") {
		t.Errorf("err = %v", err)
	}
}

// TestOnPremAnswerMayFollowNoise: a profile or a module can write to stdout before the
// bootstrap does; the answer is the last line, and only that line is read.
func TestOnPremAnswerMayFollowNoise(t *testing.T) {
	r := &fakeRunner{stdout: "WARNING: something\r\n{\"ok\":true,\"value\":[1,2]}\r\n"}
	c := onprem(t, r, OnPremConfig{})
	out, err := c.Invoke(context.Background(), catalog.Invocation{Cmdlet: "Get-Mailbox"}, 0)
	if err != nil || len(out) != 2 {
		t.Errorf("out %v err %v", out, err)
	}
}

func TestOnPremConfigIsChecked(t *testing.T) {
	cases := []struct {
		name string
		cfg  OnPremConfig
		want string
	}{
		{"no uri", OnPremConfig{}, "connection URI"},
		{"not a url", OnPremConfig{ConnectionURI: "exch01"}, "http or https"},
		{"basic over http", OnPremConfig{ConnectionURI: "http://exch01/PowerShell/", Authentication: "Basic", Username: "u", Password: "p"}, "https"},
		{"basic without credential", OnPremConfig{ConnectionURI: "https://exch01/PowerShell/", Authentication: "Basic"}, "username"},
		{"unknown mechanism", OnPremConfig{ConnectionURI: "https://exch01/PowerShell/", Authentication: "Digest"}, "authentication"},
		{"user without password", OnPremConfig{ConnectionURI: "https://exch01/PowerShell/", Username: "u"}, "password"},
		{"password without user", OnPremConfig{ConnectionURI: "https://exch01/PowerShell/", Password: "p"}, "username"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewOnPremClient(c.cfg)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want mention of %q", err, c.want)
			}
		})
	}
	c, err := NewOnPremClient(OnPremConfig{ConnectionURI: "https://exch01/PowerShell/", Authentication: "negotiate", PowerShell: "powershell.exe"})
	if err != nil {
		t.Fatal(err)
	}
	if c.auth != "Negotiate" || c.powershell != "powershell.exe" || c.timeout != defaultOnPremTimeout {
		t.Errorf("client = %+v", c)
	}
}

// TestBootstrapKeepsItsPromises reads the program itself for the properties nothing
// else can check without an Exchange server: what it runs is the endpoint's command and
// never a local one of the same name, a change is never left waiting for a
// confirmation, and the answer is written as UTF-8 whatever the console's code page.
func TestBootstrapKeepsItsPromises(t *testing.T) {
	for _, want := range []string{
		"ConfigurationName = 'Microsoft.Exchange'",
		"Import-PSSession",
		"ExportedCommands",
		"$params['Confirm'] = $false",
		"$params['ErrorAction'] = 'Stop'",
		"Select-Object -First",
		"System.Text.UTF8Encoding",
		"Remove-PSSession",
		"$env:ATLAS_EXCHANGE_REQUEST",
	} {
		if !strings.Contains(onPremBootstrap, want) {
			t.Errorf("the bootstrap no longer contains %q", want)
		}
	}
	if strings.Contains(onPremBootstrap, "Invoke-Expression") || strings.Contains(onPremBootstrap, "iex ") {
		t.Error("the bootstrap evaluates text as code")
	}
}
