package infomodel

import "testing"

// The rules a lifecycle is held to are served, not assumed: the canvas enforces them
// while drawing. A state machine belongs to a business object only.
func TestTheLifecycleRulesAreServed(t *testing.T) {
	r := Lifecycles()
	if len(r.Stereotypes) == 0 || r.Meaning == "" {
		t.Fatalf("Lifecycles() = %+v, want the stereotypes and what a lifecycle means", r)
	}
	for _, s := range r.Stereotypes {
		if s == "" {
			t.Fatalf("an empty stereotype in %v", r.Stereotypes)
		}
	}
}
