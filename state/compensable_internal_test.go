package state

import (
	"testing"

	"github.com/cockroachdb/pebble"

	"github.com/pblumer/atlas/model"
)

// TestCompensableDecodeErrors covers the decode-error propagation in both
// compensable scans: a value too short to decode surfaces an error rather than
// being silently skipped, on the committed forward scan (Store.CompensablesOfScope)
// and the in-batch reverse scan (Tx.CompensablesOfScopeDesc) alike (ADR-0103).
func TestCompensableDecodeErrors(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	const scope = uint64(5)
	// A single byte is shorter than a CompensableValue, so decode must fail.
	if err := s.db.Set(keyCompensable(scope, 1), []byte{0x01}, pebble.NoSync); err != nil {
		t.Fatalf("seed corrupt value: %v", err)
	}

	err = s.CompensablesOfScope(scope, func(*model.CompensableValue) error { return nil })
	if err == nil {
		t.Error("CompensablesOfScope over corrupt value: err = nil, want decode error")
	}

	tx := s.NewTransaction()
	defer tx.Close()
	err = tx.CompensablesOfScopeDesc(scope, func(uint64, *model.CompensableValue) error { return nil })
	if err == nil {
		t.Error("CompensablesOfScopeDesc over corrupt value: err = nil, want decode error")
	}
}
