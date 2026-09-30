package script

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestAScriptsOutputIsBounded is F16's missing budget. A script's stdout is written
// by code the author controls, and it was read with cmd.Output() — a bytes.Buffer
// with no ceiling. `while true: print(x)` was an unbounded allocation on the host,
// held back only by the 30-second timeout, which at a gigabyte a second is not a
// bound at all.
//
// The three cases are the acceptance criterion for a budget: below, exactly at, and
// past. The limit itself is allowed, or it would be a budget of one byte less.
func TestAScriptsOutputIsBounded(t *testing.T) {
	const limit = 64
	// A JSON string of exactly `limit` bytes: two quotes and limit-2 characters.
	at := `"` + strings.Repeat("x", limit-2) + `"`
	under := `"` + strings.Repeat("x", limit-3) + `"`
	over := `"` + strings.Repeat("x", limit-1) + `"`

	for _, tc := range []struct {
		name    string
		out     string
		wantErr bool
	}{
		{"one under the limit", under, false},
		{"exactly the limit", at, false},
		{"one over the limit", over, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name, args := printCommand(t, tc.out)
			got, err := execCommand(context.Background(), name, args, nil, limit)
			switch {
			case tc.wantErr && err == nil:
				t.Fatalf("output of %d bytes was accepted, want the budget to refuse it", len(tc.out))
			case tc.wantErr:
				if !strings.Contains(err.Error(), "exceeded") {
					t.Errorf("error = %v, want it to say the output exceeded the budget", err)
				}
				if got != nil {
					// Nothing partial: half a JSON document is worse than none, because it
					// decodes to a plausible wrong answer or to a confusing parse error.
					t.Errorf("got %q back with the error, want nothing", got)
				}
			case err != nil:
				t.Fatalf("output of %d bytes was refused at a budget of %d: %v", len(tc.out), limit, err)
			case string(got) != tc.out:
				t.Errorf("stdout = %q, want %q", got, tc.out)
			}
		})
	}
}

// TestTheBudgetSurvivesManySmallWrites: the ceiling is on the total, not on one
// write. An interpreter flushes a line at a time, so a budget enforced per-call
// would not be a budget at all.
func TestTheBudgetSurvivesManySmallWrites(t *testing.T) {
	c := &capped{max: 10}
	for range 20 {
		if n, err := c.Write([]byte("ab")); n != 2 || err != nil {
			t.Fatalf("Write = %d, %v; want 2, nil — a writer past the ceiling must not see a broken pipe", n, err)
		}
	}
	if !c.dropped {
		t.Error("forty bytes through a ten-byte ceiling went unnoticed")
	}
	if c.Len() != 10 {
		t.Errorf("kept %d bytes, want exactly the ceiling of 10", c.Len())
	}
}

// TestMaxOutputDefaults pins the fallback: an executor nobody configured still has a
// ceiling. "Unset" must not mean "unbounded" — that is the state this replaced.
func TestMaxOutputDefaults(t *testing.T) {
	e := New(Lang{Name: "python"})
	if got := e.maxOutput(); got != defaultMaxOutput {
		t.Errorf("unset MaxOutput = %d, want the default %d", got, defaultMaxOutput)
	}
	e.MaxOutput = 4096
	if got := e.maxOutput(); got != 4096 {
		t.Errorf("MaxOutput = %d, want the configured 4096", got)
	}
	e.MaxOutput = -1
	if got := e.maxOutput(); got != defaultMaxOutput {
		t.Errorf("negative MaxOutput = %d, want the default %d", got, defaultMaxOutput)
	}
}

// TestTheDefaultRunnerCarriesTheBudget covers the path production takes: no fake
// runner, so the executor builds the closure that calls the real os/exec and hands
// it MaxOutput. Every other test substitutes e.run, which means the one wiring that
// ships was the one nothing exercised — and it is the wiring that decides whether
// the ceiling reaches the process at all.
func TestTheDefaultRunnerCarriesTheBudget(t *testing.T) {
	e := New(Lang{Name: "helper"})
	e.MaxOutput = 8
	name, args := printCommand(t, `"0123456789"`)
	if _, err := e.runner()(context.Background(), name, args, nil); err == nil {
		t.Error("twelve bytes through an eight-byte budget were accepted")
	}
	e.MaxOutput = 64
	name, args = printCommand(t, `"ok"`)
	out, err := e.runner()(context.Background(), name, args, nil)
	if err != nil {
		t.Fatalf("a result inside the budget was refused: %v", err)
	}
	if string(out) != `"ok"` {
		t.Errorf("stdout = %q, want %q", out, `"ok"`)
	}
}

// printHelperEnv makes this test binary the child process of the budget tests: run
// with it set, the binary writes its last argument to stdout and exits, and does
// nothing else. An argument reaches a Go program exactly as it was passed, on every
// platform. printf did not: on Windows it is the MSYS one, which splits a command
// line by rules of its own, and `"ok"` arrived as `\ok"`.
const printHelperEnv = "ATLAS_SCRIPT_PRINT_HELPER"

func TestPrintHelper(t *testing.T) {
	if os.Getenv(printHelperEnv) != "1" {
		return
	}
	_, _ = os.Stdout.WriteString(os.Args[len(os.Args)-1])
	os.Exit(0)
}

// printCommand is the command that makes this test binary print out, and nothing
// else, on stdout. The runner is given no environment of its own, so the child
// inherits the variable that makes it the helper.
func printCommand(t *testing.T, out string) (string, []string) {
	t.Helper()
	t.Setenv(printHelperEnv, "1")
	return os.Args[0], []string{"-test.run=^TestPrintHelper$", "--", out}
}
