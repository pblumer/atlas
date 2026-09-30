//go:build !linux

package script

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Strict has no profile to apply anywhere but Linux (ADR-0303), so there it must
// refuse to run a script rather than run it unconfined. serve and worker already
// refuse to start with it; this is the same contract one layer down, where a caller
// that skipped the startup check would land. The strict tests that exercise the
// profile itself are in sandbox_policy_linux_test.go.
func TestStrictSandboxRefusesToRunAScriptOutsideLinux(t *testing.T) {
	if err := CheckSandbox(SandboxStrict); err == nil {
		t.Fatal("strict was reported as available")
	}
	ran := false
	e := &CmdExec{Lang: Python, Bin: os.Args[0], Sandbox: SandboxStrict,
		run: func(context.Context, string, []string, []string) ([]byte, error) {
			ran = true
			return nil, nil
		}}
	_, err := e.Run(context.Background(), `result = 1`, nil)
	if err == nil || !strings.Contains(err.Error(), "outside the sandbox runtime") {
		t.Fatalf("Run under strict = %v, want the interpreter refused", err)
	}
	if ran {
		t.Error("the interpreter was started without the profile")
	}
}
