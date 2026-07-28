package state_test

import (
	"testing"

	"github.com/pblumer/atlas/state"
)

// TestInboundHighWater covers the per-source high-water accessors (ADR-0075): an
// unset source reads 0, a put is read back, a re-put advances it, and distinct
// sources are independent.
func TestInboundHighWater(t *testing.T) {
	s := openStore(t)

	// An unset source has no mark → 0.
	got, err := readHighWater(t, s, "clio:orders")
	if err != nil {
		t.Fatalf("InboundHighWater (unset): %v", err)
	}
	if got != 0 {
		t.Fatalf("unset high-water = %d, want 0", got)
	}

	commit(t, s, func(tx *state.Tx) error { return tx.PutInboundHighWater("clio:orders", 7) })
	if got, _ := readHighWater(t, s, "clio:orders"); got != 7 {
		t.Fatalf("high-water after put = %d, want 7", got)
	}

	// A re-put advances (absolute, not additive); a different source is independent.
	commit(t, s, func(tx *state.Tx) error {
		if err := tx.PutInboundHighWater("clio:orders", 9); err != nil {
			return err
		}
		return tx.PutInboundHighWater("clio:shipments", 3)
	})
	if got, _ := readHighWater(t, s, "clio:orders"); got != 9 {
		t.Fatalf("high-water after re-put = %d, want 9", got)
	}
	if got, _ := readHighWater(t, s, "clio:shipments"); got != 3 {
		t.Fatalf("other source high-water = %d, want 3", got)
	}
}

func readHighWater(t *testing.T, s *state.Store, sourceID string) (uint64, error) {
	t.Helper()
	tx := s.NewTransaction()
	defer tx.Close()
	return tx.InboundHighWater(sourceID)
}
