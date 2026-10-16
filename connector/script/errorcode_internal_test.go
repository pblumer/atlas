package script

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/pblumer/atlas/job"
)

// TestParseErrorReport covers reading the code a failing bootstrap reports on stdout.
// It is lenient about what precedes the report — a script may have written to stdout
// itself before it failed — and strict about the report: no code, no report.
func TestParseErrorReport(t *testing.T) {
	tests := []struct {
		name, out, code, msg string
		ok                   bool
	}{
		{"a report", `{"atlasError":{"code":"UPN_EXISTS","message":"taken"}}`, "UPN_EXISTS", "taken", true},
		{"after the script's own output", "partial\n" + `{"atlasError":{"code":"UPN_EXISTS","message":"taken"}}`, "UPN_EXISTS", "taken", true},
		{"a code is trimmed", `{"atlasError":{"code":"  UPN_EXISTS ","message":""}}`, "UPN_EXISTS", "", true},
		{"no code", `{"atlasError":{"message":"taken"}}`, "", "", false},
		{"an empty code", `{"atlasError":{"code":" ","message":"taken"}}`, "", "", false},
		{"not JSON", `{"atlasError":{"code":`, "", "", false},
		{"nothing", "", "", "", false},
		{"a result, not a report", `{"code":"UPN_EXISTS"}`, "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, msg, ok := parseErrorReport([]byte(tt.out))
			if code != tt.code || msg != tt.msg || ok != tt.ok {
				t.Errorf("parseErrorReport(%q) = %q, %q, %v; want %q, %q, %v", tt.out, code, msg, ok, tt.code, tt.msg, tt.ok)
			}
		})
	}
}

// TestRunReportsTheScriptsErrorCode: a failing run whose stdout carries a report
// returns a [CodedError] that still reads as the interpreter's own failure, so an
// incident shows what it showed before; one without a report fails as before.
func TestRunReportsTheScriptsErrorCode(t *testing.T) {
	failed := errors.New("exit status 1: boom")
	e := &CmdExec{Lang: Python, run: func(context.Context, string, []string, []string) ([]byte, error) {
		return []byte(`{"atlasError":{"code":"UPN_EXISTS","message":"taken"}}`), failed
	}}
	_, err := e.Run(context.Background(), "raise", nil)
	var ce *CodedError
	if !errors.As(err, &ce) || ce.Code != "UPN_EXISTS" || ce.Message != "taken" {
		t.Fatalf("Run error = %#v, want a CodedError UPN_EXISTS", err)
	}
	if err.Error() != "script: run python: exit status 1: boom" || !errors.Is(err, failed) {
		t.Errorf("Run error reads %q, want the interpreter's failure unchanged", err)
	}

	e.run = func(context.Context, string, []string, []string) ([]byte, error) { return nil, failed }
	if _, err := e.Run(context.Background(), "raise", nil); errors.As(err, &ce) {
		t.Errorf("Run error = %v, want no code without a report", err)
	}
}

// runCoded runs src through the real interpreter for lang, with the given codes as the
// ones the task's process catches, and returns what [Run] made of it.
func runCoded(t *testing.T, lang Lang, src string, caught ...string) error {
	t.Helper()
	if _, err := exec.LookPath(lang.Bin); err != nil {
		t.Skipf("%s not available", lang.Bin)
	}
	_, err := Run(context.Background(), Job{Source: src, ErrorCodes: caught}, realExec(lang))
	if err == nil {
		t.Fatal("Run of a failing script succeeded, want an error")
	}
	return err
}

func wantBPMNError(t *testing.T, err error, code, msg string) {
	t.Helper()
	var be *job.BPMNError
	if !errors.As(err, &be) {
		t.Fatalf("error = %v, want BPMN error %s", err, code)
	}
	if be.Code != code || be.Message != msg {
		t.Errorf("BPMN error = %q: %q, want %q: %q", be.Code, be.Message, code, msg)
	}
}

func wantFailure(t *testing.T, err error, contains string) {
	t.Helper()
	var be *job.BPMNError
	if errors.As(err, &be) {
		t.Fatalf("error = %v, want a plain failure", err)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Errorf("error = %q, want it to contain %q", err, contains)
	}
}

const upnTaken = "An object with the same userPrincipalName already exists."

// TestPowerShellErrorIdIsTheCode: PowerShell's own way to give an error an
// identifier — Write-Error -ErrorId, or the ErrorRecord's ErrorId however it was
// built — is the code. It is a normal terminating error at a prompt, so the script
// runs unchanged there. Written in a function the identifier gains the function's name
// after a comma; the code is what precedes it.
func TestPowerShellErrorIdIsTheCode(t *testing.T) {
	inFunction := `function New-Account { Write-Error -Message '` + upnTaken + `' -ErrorId 'UPN_EXISTS' -Category ResourceExists }
New-Account`
	wantBPMNError(t, runCoded(t, PowerShell, inFunction, "UPN_EXISTS"), "UPN_EXISTS", upnTaken)

	// The pattern a script that already catches its failures uses: catch, then say
	// which one it was.
	rethrown := `try { throw '` + upnTaken + `' }
catch { Write-Error -Message $_.Exception.Message -ErrorId 'UPN_EXISTS' -Category ResourceExists }`
	wantBPMNError(t, runCoded(t, PowerShell, rethrown, "UPN_EXISTS"), "UPN_EXISTS", upnTaken)

	// A bare throw of a string makes the string the identifier, so throwing the code
	// itself is the shortest form.
	wantBPMNError(t, runCoded(t, PowerShell, `throw 'UPN_EXISTS'`, "UPN_EXISTS"), "UPN_EXISTS", "UPN_EXISTS")

	// Not a code the process catches: the failure PowerShell reported, as before.
	wantFailure(t, runCoded(t, PowerShell, inFunction, "SOMETHING_ELSE"), upnTaken)
	wantFailure(t, runCoded(t, PowerShell, `Get-Item -Path '/atlas/does/not/exist'`, "UPN_EXISTS"), "does not exist")
}

// TestPowerShellCodedFailureKeepsItsLog: a script that logs and then throws a code its
// process does not catch still fails with its log in the message — the report on stdout
// takes nothing from stderr.
func TestPowerShellCodedFailureKeepsItsLog(t *testing.T) {
	err := runCoded(t, PowerShell, "Write-Host 'step 1'\nWrite-Error -ErrorId 'UPN_EXISTS' -Message 'taken'")
	wantFailure(t, err, "step 1")
}

// TestPythonCodeAttributeIsTheCode: an exception's code attribute is the code — a
// string, or an integer such as an HTTP status. SystemExit carries a code too, the exit
// status, and is not one: sys.exit is how a script ends, not an error it raises.
func TestPythonCodeAttributeIsTheCode(t *testing.T) {
	adHoc := "e = RuntimeError('" + upnTaken + "')\ne.code = 'UPN_EXISTS'\nraise e"
	wantBPMNError(t, runCoded(t, Python, adHoc, "UPN_EXISTS"), "UPN_EXISTS", upnTaken)

	declared := "class UpnExists(Exception):\n    code = 'UPN_EXISTS'\nraise UpnExists('" + upnTaken + "')"
	wantBPMNError(t, runCoded(t, Python, declared, "UPN_EXISTS"), "UPN_EXISTS", upnTaken)

	status := "e = RuntimeError('conflict')\ne.code = 409\nraise e"
	wantBPMNError(t, runCoded(t, Python, status, "409"), "409", "conflict")

	wantFailure(t, runCoded(t, Python, "import sys\nsys.exit(3)", "3"), "exit status 3")
	wantFailure(t, runCoded(t, Python, "raise ValueError('plain')", "UPN_EXISTS"), "plain")
	wantFailure(t, runCoded(t, Python, adHoc, "SOMETHING_ELSE"), upnTaken)
}

// TestJavaScriptCodePropertyIsTheCode: an error's code property is the code, the
// convention Node's own errors follow (ENOENT and the like).
func TestJavaScriptCodePropertyIsTheCode(t *testing.T) {
	src := "throw Object.assign(new Error('" + upnTaken + "'), { code: 'UPN_EXISTS' })"
	wantBPMNError(t, runCoded(t, JavaScript, src, "UPN_EXISTS"), "UPN_EXISTS", upnTaken)

	status := "throw Object.assign(new Error('conflict'), { code: 409 })"
	wantBPMNError(t, runCoded(t, JavaScript, status, "409"), "409", "conflict")

	wantFailure(t, runCoded(t, JavaScript, "throw new Error('plain')", "UPN_EXISTS"), "plain")
	wantFailure(t, runCoded(t, JavaScript, src, "SOMETHING_ELSE"), upnTaken)
}
