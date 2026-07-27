package pwsh

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// TestOutputVariableTypes proves a script result of each JSON shape is classified
// into the right stored variable kind, so a downstream gateway routes on it and it
// round-trips on replay.
func TestOutputVariableTypes(t *testing.T) {
	tests := []struct {
		name     string
		result   any
		wantKind model.VarKind
		wantText string
		wantBool bool
	}{
		{"string", "Hallo", model.VarString, "Hallo", false},
		{"number", json.Number("42"), model.VarNumber, "42", false},
		{"bool", true, model.VarBool, "", true},
		{"null", nil, model.VarNull, "", false},
		{"object", map[string]any{"a": json.Number("1")}, model.VarJSON, `{"a":1}`, false},
		{"array", []any{json.Number("1"), json.Number("2")}, model.VarJSON, "[1,2]", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := outputVariable("r", tt.result)
			if v.Name != "r" {
				t.Errorf("name = %q, want r", v.Name)
			}
			if v.Kind != tt.wantKind {
				t.Errorf("kind = %v, want %v", v.Kind, tt.wantKind)
			}
			if v.Text != tt.wantText {
				t.Errorf("text = %q, want %q", v.Text, tt.wantText)
			}
			if v.Bool != tt.wantBool {
				t.Errorf("bool = %v, want %v", v.Bool, tt.wantBool)
			}
		})
	}
}

// TestVarToAny proves a stored variable of each kind becomes the JSON-ready Go
// value a script sees as input.
func TestVarToAny(t *testing.T) {
	tests := []struct {
		name string
		in   model.VariableValue
		want any
	}{
		{"string", model.VariableValue{Kind: model.VarString, Text: "hi"}, "hi"},
		{"bool", model.VariableValue{Kind: model.VarBool, Bool: true}, true},
		{"number", model.VariableValue{Kind: model.VarNumber, Text: "3.5"}, json.Number("3.5")},
		{"null", model.VariableValue{Kind: model.VarNull}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := varToAny(&tt.in); got != tt.want {
				t.Errorf("varToAny = %#v, want %#v", got, tt.want)
			}
		})
	}
	// A structured value re-parses into a real object rather than a JSON string.
	obj := varToAny(&model.VariableValue{Kind: model.VarJSON, Text: `{"n":7}`})
	m, ok := obj.(map[string]any)
	if !ok || m["n"] != json.Number("7") {
		t.Errorf("VarJSON varToAny = %#v, want map with n=7", obj)
	}
	// Malformed structured text degrades to nil rather than panicking.
	if got := varToAny(&model.VariableValue{Kind: model.VarJSON, Text: "{"}); got != nil {
		t.Errorf("malformed VarJSON = %#v, want nil", got)
	}
}

// TestParseOutput covers decoding an interpreter's stdout into the script result.
func TestParseOutput(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		v, err := parseOutput([]byte(`"Hallo"` + "\n"))
		if err != nil || v != "Hallo" {
			t.Fatalf("parseOutput = %#v, %v; want Hallo", v, err)
		}
	})
	t.Run("number keeps precision", func(t *testing.T) {
		v, err := parseOutput([]byte("10000000000000001"))
		if err != nil || v != json.Number("10000000000000001") {
			t.Fatalf("parseOutput = %#v, %v; want exact json.Number", v, err)
		}
	})
	t.Run("empty is null", func(t *testing.T) {
		v, err := parseOutput([]byte("   \n"))
		if err != nil || v != nil {
			t.Fatalf("parseOutput = %#v, %v; want nil", v, err)
		}
	})
	t.Run("invalid errors", func(t *testing.T) {
		if _, err := parseOutput([]byte("not json")); err == nil {
			t.Fatal("parseOutput of non-JSON succeeded, want an error")
		}
	})
}

// TestWrapSource proves the wrapper embeds the authored source verbatim and keeps
// the safety flags/contract: stop-on-error and reading variables from varsEnv.
func TestWrapSource(t *testing.T) {
	src := `"Hallo " + $Vorname`
	w := wrapSource(src)
	if !strings.Contains(w, src) {
		t.Error("wrapper does not embed the authored source")
	}
	if !strings.Contains(w, "$ErrorActionPreference = 'Stop'") {
		t.Error("wrapper does not stop on error")
	}
	if !strings.Contains(w, varsEnv) {
		t.Errorf("wrapper does not read variables from %s", varsEnv)
	}
	if !strings.Contains(w, "ConvertTo-Json") {
		t.Error("wrapper does not emit its result as JSON")
	}
}

// TestCmdExecRun drives CmdExec through an injected runner (no interpreter), so the
// env wiring, argument construction, and output decoding are all exercised.
func TestCmdExecRun(t *testing.T) {
	var gotName string
	var gotArgs, gotEnv []string
	e := &CmdExec{
		Bin: "pwsh-test",
		run: func(_ context.Context, name string, args, env []string) ([]byte, error) {
			gotName, gotArgs, gotEnv = name, args, env
			return []byte(`"ok"`), nil
		},
	}
	out, err := e.Run(context.Background(), `"ok"`, map[string]any{"Vorname": "Anna"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out != "ok" {
		t.Errorf("result = %#v, want ok", out)
	}
	if gotName != "pwsh-test" {
		t.Errorf("bin = %q, want pwsh-test", gotName)
	}
	if len(gotArgs) < 3 || gotArgs[0] != "-NoProfile" || gotArgs[1] != "-NonInteractive" || gotArgs[2] != "-Command" {
		t.Errorf("args = %v, want the non-interactive PowerShell flags", gotArgs)
	}
	// The instance's variables ride in the varsEnv environment entry as JSON.
	var found bool
	for _, kv := range gotEnv {
		if strings.HasPrefix(kv, varsEnv+"=") {
			found = true
			if !strings.Contains(kv, `"Vorname":"Anna"`) {
				t.Errorf("%s = %q, want the variables JSON", varsEnv, kv)
			}
		}
	}
	if !found {
		t.Errorf("no %s entry in env %v", varsEnv, gotEnv)
	}
}

// TestHandlerElementGone: a job whose element instance is already gone (e.g. the
// instance completed) is a no-op — no error, no output — so a stale job can't
// disturb a finished instance.
func TestHandlerElementGone(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	h := Handler(store, func(uint64) *compiler.CompiledProcess { return nil }, &countingExec{})
	out, err := h(job.Job{ElementInstanceKey: 999})
	if err != nil || out != nil {
		t.Fatalf("handler for missing element = %v, %v; want nil, nil", out, err)
	}
}

// countingExec is a minimal Exec that should never be called in the tests that use
// it (the handler returns before reaching the interpreter).
type countingExec struct{ calls int }

func (c *countingExec) Run(context.Context, string, map[string]any) (any, error) {
	c.calls++
	return nil, nil
}

// TestRunMarshalError: input that cannot be JSON-encoded fails before the
// interpreter is invoked.
func TestRunMarshalError(t *testing.T) {
	e := &CmdExec{run: func(context.Context, string, []string, []string) ([]byte, error) {
		t.Fatal("runner must not be reached when input encoding fails")
		return nil, nil
	}}
	if _, err := e.Run(context.Background(), "x", map[string]any{"bad": make(chan int)}); err == nil {
		t.Fatal("Run with unencodable input succeeded, want an error")
	}
}

// TestRunnerError: a runner (interpreter) error surfaces from Run, so the job is
// left pending.
func TestRunnerError(t *testing.T) {
	e := &CmdExec{run: func(context.Context, string, []string, []string) ([]byte, error) {
		return nil, errors.New("exit status 1")
	}}
	if _, err := e.Run(context.Background(), "x", nil); err == nil {
		t.Fatal("Run with a failing runner succeeded, want an error")
	}
}

// TestCheck reports whether the configured interpreter is resolvable on PATH, so
// the server can warn at startup instead of letting script tasks silently park.
func TestCheck(t *testing.T) {
	// A missing interpreter is reported as an error.
	if err := (&CmdExec{Bin: "atlas-no-such-pwsh-xyz"}).Check(); err == nil {
		t.Error("Check with a missing binary = nil, want an error")
	}
	// A binary that exists on PATH resolves cleanly (sh is present on the test host).
	if err := (&CmdExec{Bin: "sh"}).Check(); err != nil {
		t.Errorf("Check for sh = %v, want nil", err)
	}
}

// TestTimeoutDefault: the zero value bounds a script with the default timeout, and
// an explicit Timeout overrides it.
func TestTimeoutDefault(t *testing.T) {
	if got := (&CmdExec{}).timeout(); got != defaultTimeout {
		t.Errorf("default timeout = %s, want %s", got, defaultTimeout)
	}
	if got := (&CmdExec{Timeout: 5 * time.Second}).timeout(); got != 5*time.Second {
		t.Errorf("timeout = %s, want 5s", got)
	}
}

// TestRunTimesOut: a script that outruns the timeout is killed and surfaces a
// clear timeout error (which leaves the job pending), rather than blocking the
// worker forever. The fake runner honours ctx just as os/exec's CommandContext
// does — it blocks until the deadline fires.
func TestRunTimesOut(t *testing.T) {
	e := &CmdExec{
		Timeout: 20 * time.Millisecond,
		run: func(ctx context.Context, _ string, _, _ []string) ([]byte, error) {
			<-ctx.Done() // CommandContext kills the process and returns when ctx is done
			return nil, ctx.Err()
		},
	}
	_, err := e.Run(context.Background(), "Start-Sleep 60", nil)
	if err == nil {
		t.Fatal("Run of a slow script succeeded, want a timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %v, want it to mention the timeout", err)
	}
}

// TestExecCommand covers the real os/exec seam without an interpreter, using a
// POSIX utility that is always present: it returns the command's stdout, and a
// missing binary surfaces an error (which leaves a job pending).
func TestExecCommand(t *testing.T) {
	out, err := execCommand(context.Background(), "printf", []string{"ok"}, nil)
	if err != nil {
		t.Fatalf("execCommand: %v", err)
	}
	if string(out) != "ok" {
		t.Errorf("stdout = %q, want ok", out)
	}
	if _, err := execCommand(context.Background(), "atlas-no-such-binary-xyz", nil, nil); err == nil {
		t.Error("execCommand of a missing binary succeeded, want an error")
	}
}

// TestCmdExecDefaults proves the zero value targets pwsh and NewCmdExec matches it,
// without invoking an interpreter.
func TestCmdExecDefaults(t *testing.T) {
	if got := (&CmdExec{}).bin(); got != "pwsh" {
		t.Errorf("default bin = %q, want pwsh", got)
	}
	if NewCmdExec().bin() != "pwsh" {
		t.Error("NewCmdExec should default to pwsh")
	}
	// The default runner is the real os/exec seam (non-nil); we do not invoke it.
	if (&CmdExec{}).runner() == nil {
		t.Error("default runner is nil")
	}
}
