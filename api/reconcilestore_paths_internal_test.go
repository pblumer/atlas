package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReconcileStoreRefusesADataDirectoryItCannotUse. A journal that cannot be
// opened is a startup error, not a journal that opens empty and reads every
// standing finding as gone.
func TestReconcileStoreRefusesADataDirectoryItCannotUse(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "discrepancies")
	if err := os.WriteFile(blocked, []byte("a file, not a directory"), 0o600); err != nil {
		t.Fatalf("plant file: %v", err)
	}
	if _, err := newDiscrepancyStore(blocked); err == nil || !strings.Contains(err.Error(), "discrepancystore") {
		t.Errorf("err = %v, want the journal named", err)
	}
}

// TestReconcileStoreOpenFailsRatherThanAnsweringEmpty. A journal that cannot be
// listed reports the failure; an empty answer would say nothing is wrong.
func TestReconcileStoreOpenFailsRatherThanAnsweringEmpty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "discrepancies")
	s, err := newDiscrepancyStore(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	recertifyHTTPPathsBreakDir(t, dir)
	if out, err := s.open(); err == nil {
		t.Errorf("open() = %+v, nil; want an error for an unreadable journal", out)
	}
}
