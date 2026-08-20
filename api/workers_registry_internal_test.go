package api

import "testing"

// A tick clock so the counters can be asserted without depending on wall time.
func tickClock(start int64) func() int64 {
	now := start
	return func() int64 { now++; return now }
}

// The registry is what turns anonymous HTTP traffic into an operator-visible
// picture: who is pulling, what they are working on, and how much of it.
func TestWorkerRegistryTracksAWorkersWork(t *testing.T) {
	r := newWorkerRegistry(tickClock(100))

	r.leased("w1", "send-email", 3)
	r.leased("w1", "charge-card", 1)
	r.completed("w1", "send-email")
	r.completed("w1", "send-email")
	r.failed("w1", "charge-card")

	list := r.list()
	if len(list) != 1 {
		t.Fatalf("registry lists %d workers, want 1", len(list))
	}
	got := list[0]
	if got.Worker != "w1" {
		t.Errorf("worker = %q, want w1", got.Worker)
	}
	if got.Pulled != 4 {
		t.Errorf("pulled = %d, want 4", got.Pulled)
	}
	if got.Completed != 2 || got.Failed != 1 {
		t.Errorf("completed/failed = %d/%d, want 2/1", got.Completed, got.Failed)
	}
	// Four leased, three of them reported: one is still in flight.
	if got.Leased != 1 {
		t.Errorf("leased (in flight) = %d, want 1", got.Leased)
	}
	if got.Types["send-email"] != 3 || got.Types["charge-card"] != 1 {
		t.Errorf("types = %v, want send-email=3 charge-card=1", got.Types)
	}
	if got.FirstSeen == 0 || got.LastSeen <= got.FirstSeen {
		t.Errorf("firstSeen/lastSeen = %d/%d, want both set and lastSeen later", got.FirstSeen, got.LastSeen)
	}
}

// In-flight can never read negative: an outcome reported for a job the registry
// did not see leased (a restart lost the count, an operator completed it) must
// not push the gauge below zero and make the console nonsense.
func TestWorkerRegistryNeverReportsNegativeInFlight(t *testing.T) {
	r := newWorkerRegistry(tickClock(1))
	r.completed("ghost", "send-email")
	r.failed("ghost", "send-email")

	list := r.list()
	if len(list) != 1 {
		t.Fatalf("registry lists %d workers, want 1", len(list))
	}
	if list[0].Leased != 0 {
		t.Errorf("leased = %d after outcomes with no lease, want 0", list[0].Leased)
	}
}

// A worker that gave no id is still doing work, so it is counted rather than
// dropped; the console labels it. Hiding it would make the totals lie.
func TestWorkerRegistryCountsTheUnnamedWorker(t *testing.T) {
	r := newWorkerRegistry(tickClock(1))
	r.leased("", "send-email", 2)

	list := r.list()
	if len(list) != 1 || list[0].Worker != "" {
		t.Fatalf("registry lists %+v, want one entry for the unnamed worker", list)
	}
	if list[0].Pulled != 2 {
		t.Errorf("pulled = %d, want 2", list[0].Pulled)
	}
}

// The listing is stable so the console does not reshuffle rows between polls.
func TestWorkerRegistryListsInAStableOrder(t *testing.T) {
	r := newWorkerRegistry(tickClock(1))
	for _, w := range []string{"zeta", "alpha", "mike"} {
		r.leased(w, "send-email", 1)
	}
	first := r.list()
	second := r.list()
	if len(first) != 3 {
		t.Fatalf("listed %d workers, want 3", len(first))
	}
	for i := range first {
		if first[i].Worker != second[i].Worker {
			t.Fatalf("listing reordered between calls: %v then %v", first, second)
		}
	}
	if first[0].Worker != "alpha" || first[2].Worker != "zeta" {
		t.Errorf("listing = %v, want it sorted by worker id", first)
	}
}

// Per-type in-flight is what the console's type table shows: a queue that is
// draining looks different from one nobody is serving, and only the outcome tells
// them apart. The outcome handlers know the job's type, so this is exact rather
// than inferred.
func TestWorkerRegistryTracksInFlightPerType(t *testing.T) {
	r := newWorkerRegistry(tickClock(1))
	r.leased("w1", "send-email", 3)
	r.leased("w2", "charge-card", 2)
	r.completed("w1", "send-email")
	r.failed("w2", "charge-card")

	for _, tc := range []struct {
		jobType string
		want    int64
	}{{"send-email", 2}, {"charge-card", 1}, {"never-seen", 0}} {
		if got := r.inFlightOf(tc.jobType); got != tc.want {
			t.Errorf("inFlightOf(%q) = %d, want %d", tc.jobType, got, tc.want)
		}
	}
}

// The same floor as the per-worker gauge: an outcome for a type this run never
// leased must not drive the type's in-flight count negative.
func TestWorkerRegistryInFlightPerTypeNeverGoesNegative(t *testing.T) {
	r := newWorkerRegistry(tickClock(1))
	r.completed("w1", "send-email")
	if got := r.inFlightOf("send-email"); got != 0 {
		t.Errorf("inFlightOf = %d after an unmatched outcome, want 0", got)
	}
}
