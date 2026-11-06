package workercmd

import "testing"

// The supervisor hands a launched artifact its identity through the environment; a
// worker run by hand without it reports none (ADR-0535 §3).
func TestWorkerIdentityFromEnv(t *testing.T) {
	env := map[string]string{
		workerTypeIDEnv:      " com.example.sap-s4 ",
		workerTypeVersionEnv: "2.1.3",
		workerTypeDigestEnv:  "sha256:abc",
	}
	got := workerIdentityFromEnv(func(k string) string { return env[k] })
	if got == nil || got.ID != "com.example.sap-s4" || got.Version != "2.1.3" || got.Digest != "sha256:abc" {
		t.Errorf("identity = %+v, want the trimmed values from the environment", got)
	}
	if got := workerIdentityFromEnv(func(string) string { return "" }); got != nil {
		t.Errorf("identity with nothing set = %+v, want none", got)
	}
}
