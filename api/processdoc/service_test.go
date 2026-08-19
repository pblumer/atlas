package processdoc

import (
	"os"
	"sync"
	"testing"

	"github.com/pblumer/atlas/api/runloop"
	"github.com/pblumer/atlas/api/token"
)

// newService builds a service over a temp store with a running loop, and returns
// it alongside the store. That it can be built at all without an api.Server is the
// point of ADR-0147: the area's dependencies are the five arguments below and
// nothing else.
func newService(t *testing.T) (*Service, *Store) {
	t.Helper()
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	quit := make(chan struct{})
	loop := runloop.New(quit)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); loop.Run() }()
	t.Cleanup(func() { close(quit); wg.Wait() })

	svc := New(loop, store,
		func(string) bool { return true },
		func(string) (Deployment, bool) { return Deployment{}, false },
		token.New)
	return svc, store
}

// TestLoadVersionsRebuildsCounter proves the per-process counter is taken from the
// highest stored version of each process. Without it a restart would restart
// numbering at v1 and the next export would overwrite history.
func TestLoadVersionsRebuildsCounter(t *testing.T) {
	svc, store := newService(t)
	for _, rec := range []Doc{
		{ID: "01", ProcessID: "a", Version: 2},
		{ID: "02", ProcessID: "a", Version: 5},
		{ID: "03", ProcessID: "b", Version: 1},
	} {
		if err := store.Save(rec, []byte("%PDF-")); err != nil {
			t.Fatalf("Save %s: %v", rec.ID, err)
		}
	}
	if err := svc.LoadVersions(); err != nil {
		t.Fatalf("LoadVersions: %v", err)
	}
	if svc.versions["a"] != 5 || svc.versions["b"] != 1 {
		t.Fatalf("versions = %v, want a=5 b=1", svc.versions)
	}
}

// TestLoadVersionsReportsAnUnreadableStore proves a store that cannot be read is a
// startup error rather than a silent reset to zero.
func TestLoadVersionsReportsAnUnreadableStore(t *testing.T) {
	svc, store := newService(t)
	removeDir(t, store)
	if err := svc.LoadVersions(); err == nil {
		t.Fatal("LoadVersions over an unreadable store: want an error")
	}
}

// TestFilenameForSanitizes proves the download filename is built from the process
// id with everything outside a conservative allowlist replaced, so it cannot carry
// a quote, a path separator, or a header-splitting byte into the
// Content-Disposition header.
func TestFilenameForSanitizes(t *testing.T) {
	for _, tc := range []struct {
		processID string
		version   int32
		want      string
	}{
		{"reisebuchung", 3, "reisebuchung-v3.pdf"},
		{"order_to-cash2", 1, "order_to-cash2-v1.pdf"},
		{`../../etc/pa"sswd`, 2, `------etc-pa-sswd-v2.pdf`},
		{"Ünïcödé", 1, "-n-c-d--v1.pdf"},
		{"", 1, "process-v1.pdf"},
	} {
		if got := filenameFor(Doc{ProcessID: tc.processID, Version: tc.version}); got != tc.want {
			t.Errorf("filenameFor(%q, v%d) = %q, want %q", tc.processID, tc.version, got, tc.want)
		}
	}
}

// TestNewIDIsStoreSafe pins what the create path relies on when it skips
// validating its own mint: NewID produces exactly the shape the store demands, so
// a freshly minted id can never be refused as unsafe.
func TestNewIDIsStoreSafe(t *testing.T) {
	id, err := NewID()
	if err != nil {
		t.Fatalf("NewID: %v", err)
	}
	if !token.IsHex(id) || len(id) != 32 {
		t.Fatalf("NewID = %q, want 32 hex characters", id)
	}
}

// removeDir deletes a store's directory so every call into it fails — the seam
// the error branches are reached through.
func removeDir(t *testing.T, store *Store) {
	t.Helper()
	if err := os.RemoveAll(store.Dir()); err != nil {
		t.Fatalf("remove store dir: %v", err)
	}
}
