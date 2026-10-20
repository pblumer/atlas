package startfavourites

import (
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// file is where the store keeps one person's record: the user id hex-encoded, which
// is the store's own naming and is spelt out here so these tests reach the same file.
func file(dir, userID string) string {
	return filepath.Join(dir, hex.EncodeToString([]byte(userID))+".json")
}

// A record that cannot be decoded is reported rather than answered as "nothing
// marked": an empty list would read as somebody's favourites having vanished, and
// the next mark would overwrite whatever was in there.
func TestAnUnreadableRecordIsReportedRatherThanIgnored(t *testing.T) {
	dir := t.TempDir()
	s := newServiceOn(t, dir, 100)
	if err := os.WriteFile(file(dir, "usr_1"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _ := call(t, s.HandleGet, as("usr_1", "GET", "")); code != http.StatusInternalServerError {
		t.Fatalf("GET = %d, want 500", code)
	}
	if code, _ := call(t, s.HandleMark, as("usr_1", "PUT", "travel")); code != http.StatusInternalServerError {
		t.Fatalf("PUT = %d, want 500", code)
	}
}

// And a store that cannot be written says so, instead of answering 200 with a list
// that only exists in the response.
func TestAnUnwritableStoreIsReported(t *testing.T) {
	dir := t.TempDir()
	s := newServiceOn(t, dir, 100)
	// A directory where the record file belongs: the store's atomic rename cannot
	// replace it — the one write failure a test can stage while running as root.
	if err := os.Mkdir(file(dir, "usr_1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _ := call(t, s.HandleMark, as("usr_1", "PUT", "travel")); code != http.StatusInternalServerError {
		t.Fatalf("PUT = %d, want 500", code)
	}
}
