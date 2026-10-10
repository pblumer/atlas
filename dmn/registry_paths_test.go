package dmn

import "testing"

// TestADeploymentProvidesOnlyItsOwnDecisions: whether a process carries its own copy
// of a decision is a question about that deployment's model, and no other.
func TestADeploymentProvidesOnlyItsOwnDecisions(t *testing.T) {
	r := NewRegistry()
	if err := r.Deploy(41, []byte(serviceXML)); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	for _, tc := range []struct {
		key      uint64
		decision string
		want     bool
	}{
		{41, "praemie", true},
		{41, "Praemienrechnung", true},
		{41, "kredit", false},
		{42, "praemie", false},
	} {
		if got := r.Provides(tc.key, tc.decision); got != tc.want {
			t.Errorf("Provides(%d, %q) = %v, want %v", tc.key, tc.decision, got, tc.want)
		}
	}
}
