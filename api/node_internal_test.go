package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/iotest"

	"github.com/pblumer/atlas/api/runloop"
)

// nodeServer builds a server whose only wiring is a live run loop and a settings
// store, which is everything the node routes touch.
func nodeServer(t *testing.T, settings *settingsStore) *Server {
	t.Helper()
	quit := make(chan struct{})
	s := &Server{quit: quit, runLoop: runloop.New(quit), settings: settings}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); s.runLoop.Run() }()
	t.Cleanup(func() { close(quit); wg.Wait() })
	return s
}

// brokenSettings is a settings store whose directory is gone, so every read and
// write on it fails — the faithful stand-in for a data directory that has become
// unwritable underneath a running server.
func brokenSettings(t *testing.T) *settingsStore {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "settings")
	store, err := newSettingsStore(dir)
	if err != nil {
		t.Fatalf("newSettingsStore: %v", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove settings dir: %v", err)
	}
	return store
}

// TestNodeIdentityRefusesRatherThanInventsAnIdentity is the shape every failure on
// this route has to take. A descriptor is a claim about *which server answered*,
// so a partial or empty one is not a lesser answer than a full one — it is a wrong
// one, and a correlator that believed it would attribute this node's facts to
// nothing.
func TestNodeIdentityRefusesRatherThanInventsAnIdentity(t *testing.T) {
	s := nodeServer(t, brokenSettings(t))

	rec := httptest.NewRecorder()
	s.handleNode(rec, httptest.NewRequest(http.MethodGet, "/api/v1/node", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("unreadable identity: code = %d, want 500; body = %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), `"id"`) {
		t.Errorf("a failed read still produced a descriptor: %s", rec.Body)
	}

	// The write half fails the same way, and the read that would have followed it
	// never reports a success the store did not accept.
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/node", strings.NewReader(`{"name":"n"}`))
	s.handleUpdateNode(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("unwritable identity: code = %d, want 500; body = %s", rec.Code, rec.Body)
	}
}

// TestNodeRoutesRefuseAClosingServer: with the loop closing, s.do never runs the
// closure, which would otherwise leave a zero descriptor looking like a real one.
func TestNodeRoutesRefuseAClosingServer(t *testing.T) {
	quit := make(chan struct{})
	close(quit)
	s := &Server{quit: quit, runLoop: runloop.New(quit)}

	for name, call := range map[string]func(*httptest.ResponseRecorder){
		"read": func(rec *httptest.ResponseRecorder) {
			s.handleNode(rec, httptest.NewRequest(http.MethodGet, "/api/v1/node", nil))
		},
		"write": func(rec *httptest.ResponseRecorder) {
			s.handleUpdateNode(rec, httptest.NewRequest(http.MethodPut, "/api/v1/node",
				strings.NewReader(`{"name":"n"}`)))
		},
	} {
		rec := httptest.NewRecorder()
		call(rec)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s during shutdown: code = %d, want 503; body = %s", name, rec.Code, rec.Body)
		}
	}
}

// TestNodeIdentityIsNotSalvagedFromACorruptFile. A half-written node.json is a
// file whose id nobody can trust, and reading past it — minting a fresh id, or
// treating the record as absent — would silently re-identify a server that other
// systems have already correlated against. Refusing is the only answer that
// preserves the property the id exists for.
func TestNodeIdentityIsNotSalvagedFromACorruptFile(t *testing.T) {
	dir := t.TempDir()
	settings, err := newSettingsStore(filepath.Join(dir, "settings"))
	if err != nil {
		t.Fatalf("newSettingsStore: %v", err)
	}
	if err := os.WriteFile(settings.nodeFile, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt node file: %v", err)
	}

	if _, _, err := settings.getNode(); err == nil {
		t.Fatal("a corrupt node file read as a valid identity")
	}
	if _, err := ensureNodeIdentity(settings); err == nil {
		t.Fatal("startup minted a new identity over a corrupt one")
	}

	// Both routes refuse it. The write especially: saving a name over an
	// unreadable record would persist a node with a name and no id, and every read
	// after that would fail on a file an operator now has to reconstruct.
	s := nodeServer(t, settings)
	for name, call := range map[string]func(*httptest.ResponseRecorder){
		"read": func(rec *httptest.ResponseRecorder) {
			s.handleNode(rec, httptest.NewRequest(http.MethodGet, "/api/v1/node", nil))
		},
		"write": func(rec *httptest.ResponseRecorder) {
			s.handleUpdateNode(rec, httptest.NewRequest(http.MethodPut, "/api/v1/node",
				strings.NewReader(`{"name":"n"}`)))
		},
	} {
		rec := httptest.NewRecorder()
		call(rec)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s over a corrupt identity: code = %d, want 500; body = %s", name, rec.Code, rec.Body)
		}
	}
	// And the file is untouched: a refused write must not be a half-written one.
	raw, err := os.ReadFile(settings.nodeFile)
	if err != nil || string(raw) != "{not json" {
		t.Errorf("the corrupt file changed under a refused write: %q, %v", raw, err)
	}
}

// TestNodeUpdateReportsABodyItCannotRead: a request whose body fails mid-read is a
// 400, not a silent partial update. The alternative — treating what arrived as the
// whole request — would let a truncated write clear fields nobody asked to clear.
func TestNodeUpdateReportsABodyItCannotRead(t *testing.T) {
	s := nodeServer(t, brokenSettings(t))
	req := httptest.NewRequest(http.MethodPut, "/api/v1/node",
		iotest.ErrReader(errors.New("connection reset")))
	rec := httptest.NewRecorder()
	s.handleUpdateNode(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unreadable body: code = %d, want 400; body = %s", rec.Code, rec.Body)
	}
}

// TestEnsureNodeIdentityIsIdempotent: the id is minted once. Running startup twice
// over the same data directory must not produce a second identity, or a restart
// would re-identify the server — the exact failure the persistence exists to
// prevent.
func TestEnsureNodeIdentityIsIdempotent(t *testing.T) {
	settings, err := newSettingsStore(filepath.Join(t.TempDir(), "settings"))
	if err != nil {
		t.Fatalf("newSettingsStore: %v", err)
	}
	first, err := ensureNodeIdentity(settings)
	if err != nil || first.ID == "" {
		t.Fatalf("first start: %+v, %v", first, err)
	}
	second, err := ensureNodeIdentity(settings)
	if err != nil {
		t.Fatalf("second start: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("second start minted %q over %q", second.ID, first.ID)
	}

	// A data directory that cannot be written to is an operator's problem to see at
	// startup, where it is one line in a log, rather than on a request weeks later.
	if _, err := ensureNodeIdentity(brokenSettings(t)); err == nil {
		t.Error("an unwritable data directory started clean")
	}
}

// TestNodeUpdateRefusesTooManyLabels bounds the one field an operator can grow
// without limit. The descriptor is fetched by other servers, so it stays small and
// predictable rather than becoming wherever free-form text accumulates.
func TestNodeUpdateRefusesTooManyLabels(t *testing.T) {
	labels := map[string]string{}
	for i := range maxNodeLabels + 1 {
		labels[string(rune('a'+i%26))+strings.Repeat("x", i)] = "v"
	}
	if msg := validateNodeUpdate(updateNodeReq{Labels: labels}); msg == "" {
		t.Fatalf("%d labels were accepted; the bound is %d", len(labels), maxNodeLabels)
	}
	if msg := validateNodeUpdate(updateNodeReq{Labels: map[string]string{"region": "ch-zh"}}); msg != "" {
		t.Errorf("one ordinary label was refused: %s", msg)
	}
}

// TestBindingCatalogReportsAnUnreadableNodeIdentity: the runtime catalog reads the
// same identity, and a failure there is an error rather than a catalog that
// quietly reports every runtime binding as missing — which would read as drift
// against an architecture model that is perfectly correct.
func TestBindingCatalogReportsAnUnreadableNodeIdentity(t *testing.T) {
	s := storesFor(t)
	dir := t.TempDir()
	releases, err := newReleaseStore(filepath.Join(dir, "releases"))
	if err != nil {
		t.Fatalf("newReleaseStore: %v", err)
	}
	s.releases, s.settings = releases, brokenSettings(t)
	s.versions = map[string]int32{}

	if _, err := s.collectBindingCatalog(httptest.NewRequest(http.MethodGet, "/", nil)); err == nil {
		t.Fatal("an unreadable node identity produced a catalog anyway")
	}
}

// TestNodeIdentityReportsAReadThatIsNotAMissingFile separates the two ways a file
// can be unreadable. A missing node.json is the state of a server that has never
// started, and ensureNodeIdentity turns it into an id; anything else is a data
// directory in trouble, and reading past it would mint a second identity for a
// server that already has one.
func TestNodeIdentityReportsAReadThatIsNotAMissingFile(t *testing.T) {
	settings, err := newSettingsStore(filepath.Join(t.TempDir(), "settings"))
	if err != nil {
		t.Fatalf("newSettingsStore: %v", err)
	}
	// Not a file at all: the read fails with something that is not "does not exist".
	if err := os.Mkdir(settings.nodeFile, 0o755); err != nil {
		t.Fatalf("mkdir over node file: %v", err)
	}

	if _, ok, err := settings.getNode(); err == nil || ok {
		t.Fatalf("an unreadable node file read as (ok=%v, err=%v)", ok, err)
	}
	if _, err := ensureNodeIdentity(settings); err == nil {
		t.Fatal("startup minted a second identity over an unreadable one")
	}
}

// TestNodeUpdateReportsAnIdentityItCannotWrite is the failure between the read and
// the write — a data volume that goes away mid-request. The read still answers
// from the page cache and the write has nowhere to land, and the operator has to
// be told, because the alternative is a 200 for a name that was never stored.
func TestNodeUpdateReportsAnIdentityItCannotWrite(t *testing.T) {
	backing, err := newSettingsStore(filepath.Join(t.TempDir(), "settings"))
	if err != nil {
		t.Fatalf("newSettingsStore: %v", err)
	}
	if _, err := ensureNodeIdentity(backing); err != nil {
		t.Fatalf("ensureNodeIdentity: %v", err)
	}
	// Reads land on the real file; writes go to a directory that is not there.
	s := nodeServer(t, &settingsStore{
		dir:      filepath.Join(t.TempDir(), "gone"),
		nodeFile: backing.nodeFile,
	})

	rec := httptest.NewRecorder()
	s.handleUpdateNode(rec, httptest.NewRequest(http.MethodPut, "/api/v1/node",
		strings.NewReader(`{"name":"Zurich primary"}`)))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("unwritable identity: code = %d, want 500; body = %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "save node identity") {
		t.Errorf("the failure does not say which half failed: %s", rec.Body)
	}
	// Note what the 500 does and does not claim. sidecar.WriteJSON renames the
	// record into place before it fsyncs the directory, so an error from the last
	// step means "not proven durable", not "not written" — the bytes may well be in
	// the page cache. Reporting success would tell the operator the name is stored
	// when a power loss could still take it; reporting the failure lets them retry,
	// which is idempotent. The id is what must not move, and it does not.
	stored, _, err := backing.getNode()
	if err != nil || stored.ID == "" {
		t.Errorf("the identity lost its id under a failed write: %+v, %v", stored, err)
	}
}
