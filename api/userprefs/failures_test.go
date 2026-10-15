package userprefs

import (
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/pblumer/atlas/api/runloop"
)

// newServiceOn builds a service over a directory the test keeps, so a record can
// be broken underneath it.
func newServiceOn(t *testing.T, dir string) *Service {
	t.Helper()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); loop.Run() }()
	t.Cleanup(func() { close(quit); wg.Wait() })
	return New(loop, store)
}

// file is where the store keeps one person's record: the user id hex-encoded,
// which is the store's own naming and is spelt out here so these tests reach the
// same file the service writes.
func file(dir, userID string) string {
	return filepath.Join(dir, hex.EncodeToString([]byte(userID))+".json")
}

// A record that cannot be decoded — truncated by a full disk, or hand-edited —
// is reported rather than answered as "nothing set". Silently handing back the
// defaults would reformat somebody's console without saying why, and the next
// save would overwrite whatever was in there.
func TestAnUnreadableRecordIsReportedRatherThanIgnored(t *testing.T) {
	dir := t.TempDir()
	s := newServiceOn(t, dir)
	if err := os.WriteFile(file(dir, "usr_1"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	s.HandleGet(rec, as("usr_1", "GET", ""))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d (%s), want 500", rec.Code, rec.Body)
	}
}

// And a store that cannot be written says so, instead of answering 200 with a
// record that only exists in the response.
func TestAnUnwritableStoreIsReported(t *testing.T) {
	dir := t.TempDir()
	s := newServiceOn(t, dir)
	// A directory where the record file belongs: the store's atomic rename cannot
	// replace it, which is the same failure a full disk or a permission produces,
	// and the only one a test can stage while running as root.
	if err := os.Mkdir(file(dir, "usr_1"), 0o755); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	s.HandleSave(rec, as("usr_1", "PUT", `{"region":"ch"}`))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d (%s), want 500", rec.Code, rec.Body)
	}
}
