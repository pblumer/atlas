package engine

import "testing"

// TestOldestPerFlowKeepsOneAndOnlyOnePerFlow pins the selection a join firing
// consumes. It is the whole of BPMN 2.0.2 §13.4's "one token from each incoming
// sequence flow", and two properties of it matter beyond the count:
//
//   - which token, when a flow has several — the oldest, so a join is
//     first-in-first-out and its choice is a pure function of state rather than of
//     scan order;
//   - what is left out — everything else, which is the *next* firing's and must
//     survive this one.
func TestOldestPerFlowKeepsOneAndOnlyOnePerFlow(t *testing.T) {
	c := &ProcessingContext{p: &Processor{}}

	// Keys ascend with minting, so the scan hands them over oldest first.
	got := c.OldestPerFlow([]Arrival{
		{Key: 10, Flow: 1},
		{Key: 11, Flow: 2},
		{Key: 12, Flow: 1}, // surplus on flow 1
		{Key: 13, Flow: 3},
		{Key: 14, Flow: 2}, // surplus on flow 2
	})
	want := []Arrival{{Key: 10, Flow: 1}, {Key: 11, Flow: 2}, {Key: 13, Flow: 3}}
	if len(got) != len(want) {
		t.Fatalf("selected %v, want one per flow: %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("selected[%d] = %+v, want %+v (the oldest on its flow)", i, got[i], want[i])
		}
	}

	// Nothing to choose from selects nothing, which is what ends an inclusive join's
	// repeat rather than looping on an empty node.
	if n := len(c.OldestPerFlow(nil)); n != 0 {
		t.Errorf("selected %d from no arrivals, want 0", n)
	}

	// Several tokens on one flow are one firing's worth, not several.
	if n := len(c.OldestPerFlow([]Arrival{{Key: 1, Flow: 7}, {Key: 2, Flow: 7}, {Key: 3, Flow: 7}})); n != 1 {
		t.Errorf("selected %d from three tokens on one flow, want 1", n)
	}
}

// TestOldestPerFlowDoesNotAllocate pins invariant I1 for the join path. Choosing
// the set runs on every arrival at every join, and the buffer it fills is
// processor-owned and reused for exactly that reason.
func TestOldestPerFlowDoesNotAllocate(t *testing.T) {
	c := &ProcessingContext{p: &Processor{}}
	arrivals := []Arrival{{Key: 10, Flow: 1}, {Key: 11, Flow: 2}, {Key: 12, Flow: 1}}
	c.OldestPerFlow(arrivals) // warm the buffer, as a running engine's would be

	allocs := testing.AllocsPerRun(1000, func() { c.OldestPerFlow(arrivals) })
	if allocs != 0 {
		t.Errorf("choosing a join's set allocated %v times per run, want 0", allocs)
	}
}
