package state_test

import (
	"testing"

	"github.com/pblumer/atlas/state"
)

// TestCancelingMarker covers the transaction canceling marker (ADR-0105): an unset scope
// reads false, setting it reads true (through the committed batch), and deleting it reads
// false again — the pure-presence marker that routes a drained transaction out its cancel
// boundary. Delete is idempotent.
func TestCancelingMarker(t *testing.T) {
	s := openStore(t)
	const tx1, tx2 = uint64(500), uint64(501)

	isCanceling := func(scope uint64) bool {
		tx := s.NewTransaction()
		defer tx.Close()
		ok, err := tx.IsCanceling(scope)
		if err != nil {
			t.Fatalf("IsCanceling: %v", err)
		}
		return ok
	}

	// Unset: not cancelling.
	if isCanceling(tx1) {
		t.Fatal("an unmarked scope reads cancelling")
	}

	// Set tx1 only.
	commit(t, s, func(tx *state.Tx) error { return tx.SetCanceling(tx1) })
	if !isCanceling(tx1) {
		t.Error("tx1 not cancelling after SetCanceling")
	}
	if isCanceling(tx2) {
		t.Error("tx2 wrongly cancelling — the marker must be per scope")
	}

	// A mark is visible through the in-flight batch before commit.
	tx := s.NewTransaction()
	if err := tx.SetCanceling(tx2); err != nil {
		t.Fatalf("SetCanceling: %v", err)
	}
	if ok, err := tx.IsCanceling(tx2); err != nil || !ok {
		t.Errorf("in-batch IsCanceling(tx2) = %v,%v, want true,nil", ok, err)
	}
	tx.Close()

	// Delete tx1; idempotent.
	commit(t, s, func(tx *state.Tx) error {
		if err := tx.DeleteCanceling(tx1); err != nil {
			return err
		}
		return tx.DeleteCanceling(tx1) // second delete is a no-op
	})
	if isCanceling(tx1) {
		t.Error("tx1 still cancelling after DeleteCanceling")
	}
}
