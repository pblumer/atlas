package script

import "testing"

// Off launches no interpreter at startup; a profile that has to be proved refuses a
// language it does not know before it probes anything.
func TestCheckSandboxLanguages(t *testing.T) {
	if err := CheckSandboxLanguages(SandboxOff, []string{"cobol"}); err != nil {
		t.Errorf("off = %v; off proves nothing and so refuses nothing", err)
	}
	if err := CheckSandboxLanguages(SandboxStrict, []string{"cobol"}); err == nil {
		t.Error("strict with an unknown language = nil, want a refusal")
	}
}
