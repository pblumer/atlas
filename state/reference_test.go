package state_test

import (
	"testing"

	"github.com/pblumer/atlas/state"
)

// TestReferenceReadSucceedsOnAFreshStore: a store with nothing applied has neither
// key, and two absent keys are still a fixed read, not a failure.
func TestReferenceReadSucceedsOnAFreshStore(t *testing.T) {
	s := openStore(t)
	if err := s.ReferenceRead(); err != nil {
		t.Fatalf("ReferenceRead on a fresh store: %v", err)
	}
}

// TestReferenceReadChangesNothing: the probe runs on the single writer every few
// seconds for as long as the server runs, so it must not leave anything behind.
func TestReferenceReadChangesNothing(t *testing.T) {
	s := openStore(t)
	commit(t, s, func(tx *state.Tx) error { return tx.SetLastAppliedPosition(42) })

	for range 3 {
		if err := s.ReferenceRead(); err != nil {
			t.Fatalf("ReferenceRead: %v", err)
		}
	}
	pos, err := s.LastAppliedPosition()
	if err != nil {
		t.Fatalf("LastAppliedPosition: %v", err)
	}
	if pos != 42 {
		t.Errorf("applied position = %d after reference reads, want 42", pos)
	}
}

// TestReferenceReadDoesNotAllocatePerKey: the read is called on a cadence for as long as
// the server runs, so the keys it reads must not be rebuilt on every call.
func TestReferenceReadDoesNotAllocatePerKey(t *testing.T) {
	s := openStore(t)
	commit(t, s, func(tx *state.Tx) error { return tx.SetLastAppliedPosition(7) })
	// The keys are built once, so the read's own allocations are Pebble's. The bound is
	// loose on purpose: this guards against a key or a buffer built per call, not
	// against Pebble's internals.
	allocs := testing.AllocsPerRun(50, func() { _ = s.ReferenceRead() })
	if allocs > 16 {
		t.Errorf("ReferenceRead allocates %.0f times per call, want at most 16", allocs)
	}
}
