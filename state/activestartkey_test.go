package state_test

import (
	"testing"

	"github.com/pblumer/atlas/state"
)

// TestActiveStartKeyCounter covers the per-(definition, correlation key) live-instance
// counter behind singleton message start (ADR-0094): an unset key reads 0, increments
// accumulate, a decrement reduces, and distinct keys and distinct definitions are
// independent.
func TestActiveStartKeyCounter(t *testing.T) {
	s := openStore(t)

	if got := readStartKey(t, s, 7, "E-1"); got != 0 {
		t.Fatalf("unset count = %d, want 0", got)
	}

	commit(t, s, func(tx *state.Tx) error {
		if err := tx.IncrementActiveStartKey(7, "E-1"); err != nil {
			return err
		}
		return tx.IncrementActiveStartKey(7, "E-1")
	})
	if got := readStartKey(t, s, 7, "E-1"); got != 2 {
		t.Fatalf("count after two increments = %d, want 2", got)
	}

	commit(t, s, func(tx *state.Tx) error { return tx.DecrementActiveStartKey(7, "E-1") })
	if got := readStartKey(t, s, 7, "E-1"); got != 1 {
		t.Fatalf("count after decrement = %d, want 1", got)
	}

	// A different key and a different definition are independent.
	commit(t, s, func(tx *state.Tx) error {
		if err := tx.IncrementActiveStartKey(7, "E-2"); err != nil {
			return err
		}
		return tx.IncrementActiveStartKey(9, "E-1")
	})
	if got := readStartKey(t, s, 7, "E-1"); got != 1 {
		t.Fatalf("original key changed to %d, want 1 (independent)", got)
	}
	if got := readStartKey(t, s, 7, "E-2"); got != 1 {
		t.Fatalf("other key count = %d, want 1", got)
	}
	if got := readStartKey(t, s, 9, "E-1"); got != 1 {
		t.Fatalf("other definition count = %d, want 1", got)
	}
}

func readStartKey(t *testing.T, s *state.Store, defKey uint64, key string) int32 {
	t.Helper()
	tx := s.NewTransaction()
	defer tx.Close()
	n, err := tx.ActiveStartKeyCount(defKey, key)
	if err != nil {
		t.Fatalf("ActiveStartKeyCount: %v", err)
	}
	return n
}
