package script

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// These tests run the real PowerShell on the production bootstrap, because what
// they pin down — how a param() block binds, which stream becomes the result — is
// PowerShell's behaviour, which a fake runner cannot have. They skip where pwsh is
// not installed; GitHub's ubuntu-latest and windows-latest images carry it, so CI
// runs them on both.

// runPowerShell runs src with vars through the real interpreter and the bootstrap.
func runPowerShell(t *testing.T, src string, vars map[string]any) (any, error) {
	t.Helper()
	if _, err := exec.LookPath("pwsh"); err != nil {
		t.Skip("pwsh not available")
	}
	return New(PowerShell).Run(context.Background(), src, vars)
}

// resultJSON renders a script result as compact JSON so a test can state the shape
// it wants in the notation the result is stored in.
func resultJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal result %#v: %v", v, err)
	}
	return string(b)
}

// TestPowerShellResultShapes pins how the output stream becomes the result, so a
// change to the way the bootstrap collects it cannot reshape a result a deployed
// script already returns: one object is that object, several are a list, an array
// written as one object unrolls, and no output (or only $null) is a null result.
func TestPowerShellResultShapes(t *testing.T) {
	tests := []struct{ src, want string }{
		{`"a"`, `"a"`},
		{`1, 2`, `[1,2]`},
		{`,@(1, 2)`, `[1,2]`},
		{`,@(5)`, `5`},
		{`@{ a = 1 }`, `{"a":1}`},
		{`$null; "a"`, `[null,"a"]`},
		{`$null`, `null`},
		{`@()`, `null`},
		{`$x = 1`, `null`},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			t.Parallel()
			out, err := runPowerShell(t, tt.src, nil)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := resultJSON(t, out); got != tt.want {
				t.Errorf("result = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestPowerShellBindsTheParamBlock: a script that declares its inputs in a param()
// block, as a PowerShell script run on its own does, receives them from the instance
// variables of the same names — so the one file runs unchanged in Atlas and from a
// prompt. A namespace such as application is one variable holding a hashtable and
// binds to a [hashtable] parameter. Names match as PowerShell matches them, without
// regard to case; a variable overrides a parameter's default; a parameter with no
// variable keeps its default; a variable the block does not declare binds nothing.
func TestPowerShellBindsTheParamBlock(t *testing.T) {
	src := `param(
    [Parameter(Mandatory = $true)][string]$Name,
    [Parameter(Mandatory = $true)][hashtable]$application,
    [string]$LanguageId = 'de-CH',
    [string]$TimeZone = 'W. Europe Standard Time'
)
[ordered]@{ Name = $Name; ClientId = $application.CLIENT_ID; LanguageId = $LanguageId; TimeZone = $TimeZone }`
	out, err := runPowerShell(t, src, map[string]any{
		"name":        "Test",
		"application": map[string]any{"CLIENT_ID": "8fc8a83c", "TENANT_ID": "df9aeca7"},
		"LanguageId":  "de-DE",
		"Other":       "not a parameter",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	// json.Marshal writes a map's keys sorted, whatever order the script built.
	want := `{"ClientId":"8fc8a83c","LanguageId":"de-DE","Name":"Test","TimeZone":"W. Europe Standard Time"}`
	if got := resultJSON(t, out); got != want {
		t.Errorf("result = %s, want %s", got, want)
	}
}

// TestPowerShellReportsAMissingMandatoryParameter: a mandatory parameter no variable
// supplies fails the run and is named — the parameters that were supplied are not,
// which is what tells a bound parameter from one the bootstrap left alone.
func TestPowerShellReportsAMissingMandatoryParameter(t *testing.T) {
	src := `param(
    [Parameter(Mandatory = $true)][string]$Name,
    [Parameter(Mandatory = $true)][string]$DomainSuffix
)
"$Name@$DomainSuffix"`
	_, err := runPowerShell(t, src, map[string]any{"Name": "Test"})
	if err == nil {
		t.Fatal("Run without a mandatory parameter succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "missing mandatory parameters: DomainSuffix.") {
		t.Errorf("error = %v, want it to name DomainSuffix and only DomainSuffix", err)
	}
}

// TestPowerShellScriptWithoutParamBlockSeesEveryVariable: the contract scripts were
// written against stays — every instance variable is a variable of the script.
func TestPowerShellScriptWithoutParamBlockSeesEveryVariable(t *testing.T) {
	out, err := runPowerShell(t, `"$Name / $($application.CLIENT_ID)"`, map[string]any{
		"Name":        "Test",
		"application": map[string]any{"CLIENT_ID": "8fc8a83c"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "Test / 8fc8a83c" {
		t.Errorf("result = %#v, want %q", out, "Test / 8fc8a83c")
	}
}

// TestPowerShellLogsDoNotBecomeTheResult: PowerShell writes Write-Host, warnings,
// verbose and information messages to stdout, which is where the result is read
// from — so a script that logs, as most scripts run by hand do, failed with "decode
// output". Only the output stream is the result.
func TestPowerShellLogsDoNotBecomeTheResult(t *testing.T) {
	src := `Write-Host "step 1"
Write-Warning "careful"
Write-Verbose "detail" -Verbose
Write-Information "note"
"ok"`
	out, err := runPowerShell(t, src, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "ok" {
		t.Errorf("result = %#v, want %q", out, "ok")
	}
}

// TestPowerShellExitKeepsTheOutput: a script run on its own commonly ends with an
// exit code, and what it wrote before is its output. exit ends the interpreter, not
// only the script, so the result is written on the way out: exit 0 keeps it, and a
// non-zero exit is a failure that says which code it was.
func TestPowerShellExitKeepsTheOutput(t *testing.T) {
	out, err := runPowerShell(t, "\"done\"\nexit 0\n\"never\"", nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "done" {
		t.Errorf("result = %#v, want %q", out, "done")
	}
	if _, err := runPowerShell(t, "\"partial\"\nexit 3", nil); err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Errorf("error = %v, want the run to fail with exit status 3", err)
	}
}

// TestPowerShellLogsAccompanyAFailure: what a script logged before it failed is the
// first thing a person debugging it wants, so it reaches the error, in order and
// marked the way PowerShell marks it at a prompt.
func TestPowerShellLogsAccompanyAFailure(t *testing.T) {
	_, err := runPowerShell(t, "Write-Host \"step 1\"\nWrite-Warning \"careful\"\nthrow \"boom\"", nil)
	if err == nil {
		t.Fatal("Run of a throwing script succeeded, want an error")
	}
	msg := err.Error()
	step, warn, boom := strings.Index(msg, "step 1"), strings.Index(msg, "WARNING: careful"), strings.Index(msg, "boom")
	if step < 0 || warn < 0 || boom < 0 || !(step < warn && warn < boom) {
		t.Errorf("error = %q, want the log line, then the warning, then the failure", msg)
	}
}
