package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRecertifyStoreRefusesADataDirectoryItCannotUse. Either half of the store
// failing to open is a startup error naming that half, rather than a store that
// opens and then loses every campaign or every row written to it.
func TestRecertifyStoreRefusesADataDirectoryItCannotUse(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("a file, not a directory"), 0o600); err != nil {
		t.Fatalf("plant file: %v", err)
	}

	if _, err := newRecertifyStore(blocked, filepath.Join(dir, "rows")); err == nil ||
		!strings.Contains(err.Error(), "recertifycampaignstore") {
		t.Errorf("campaign dir blocked: err = %v, want the campaign store named", err)
	}
	if _, err := newRecertifyStore(filepath.Join(dir, "campaigns"), blocked); err == nil ||
		!strings.Contains(err.Error(), "recertifyrowstore") {
		t.Errorf("row dir blocked: err = %v, want the row store named", err)
	}
}
