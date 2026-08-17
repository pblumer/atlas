package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Store, index, and handler branches of the ADR-0129 deploy-token layer that a
// successful mint never reaches. The end-to-end credential behaviour lives in
// deploytokens_test.go; these are the failure and edge paths.

func TestDeployTokenIndexMatching(t *testing.T) {
	idx := newDeployTokenIndex()
	secret := deployTokenPrefix + "abcdef"
	rec := deployToken{ID: "t1", Name: "Peer", Hash: hashDeployToken(secret)}
	idx.replaceAll([]deployToken{rec})

	got, ok := idx.match(secret)
	if !ok || got.ID != "t1" {
		t.Fatalf("match = %+v ok=%v, want t1", got, ok)
	}
	// A bearer that is not a deploy token is not ours to claim: the session and
	// internal-token schemes must still get their turn.
	if _, ok := idx.match("some-other-bearer"); ok {
		t.Error("a non-prefixed bearer matched a deploy token")
	}
	if _, ok := idx.match(deployTokenPrefix + "wrong"); ok {
		t.Error("a wrong secret matched")
	}

	// add and remove keep the index in step with the durable records.
	second := deployTokenPrefix + "222222"
	idx.add(deployToken{ID: "t2", Hash: hashDeployToken(second)})
	if _, ok := idx.match(second); !ok {
		t.Error("added token does not match")
	}
	idx.remove("t2")
	if _, ok := idx.match(second); ok {
		t.Error("removed token still matches")
	}
	// Removing an id that is not there is a no-op, not a panic.
	idx.remove("ghost")
	if _, ok := idx.match(secret); !ok {
		t.Error("removing an absent id disturbed the remaining tokens")
	}
}

func TestDeployTokenStoreLoadAllSkipsForeignFiles(t *testing.T) {
	dir := t.TempDir()
	s, err := newDeployTokenStore(dir)
	if err != nil {
		t.Fatalf("newDeployTokenStore: %v", err)
	}
	if err := s.save(deployToken{ID: "a", Name: "A", Hash: "h", CreatedAt: 1}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write non-hex json: %v", err)
	}
	all, err := s.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(all) != 1 || all[0].ID != "a" {
		t.Fatalf("loadAll = %+v, want just the real record", all)
	}
}

func TestDeployTokenStoreOrdersOldestFirst(t *testing.T) {
	s, err := newDeployTokenStore(t.TempDir())
	if err != nil {
		t.Fatalf("newDeployTokenStore: %v", err)
	}
	for _, rec := range []deployToken{
		{ID: "c", CreatedAt: 30}, {ID: "a", CreatedAt: 10},
		{ID: "b1", CreatedAt: 20}, {ID: "b0", CreatedAt: 20},
	} {
		if err := s.save(rec); err != nil {
			t.Fatalf("save %s: %v", rec.ID, err)
		}
	}
	all, err := s.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	got := make([]string, 0, len(all))
	for _, rec := range all {
		got = append(got, rec.ID)
	}
	if strings.Join(got, ",") != "a,b0,b1,c" {
		t.Fatalf("order = %v, want a,b0,b1,c (oldest first, ties by id)", got)
	}
}

func TestDeployTokenStoreErrors(t *testing.T) {
	broken := &deployTokenStore{dir: filepath.Join(t.TempDir(), "gone")}
	if _, err := broken.loadAll(); err == nil {
		t.Error("loadAll on a missing dir: want an error")
	}
	if err := broken.delete("x"); err == nil {
		t.Error("delete on a missing dir: want an error")
	}

	// A record that is not a token fails the load loudly rather than being dropped.
	dir := t.TempDir()
	s, err := newDeployTokenStore(dir)
	if err != nil {
		t.Fatalf("newDeployTokenStore: %v", err)
	}
	if err := os.WriteFile(s.fileFor("bad"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := s.loadAll(); err == nil {
		t.Error("loadAll with a malformed record: want an error")
	}

	// A path whose parent is a regular file cannot become a directory.
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := newDeployTokenStore(filepath.Join(file, "tokens")); err == nil {
		t.Error("newDeployTokenStore under a file path: want an error")
	}
}

func TestLoadDeployTokensRebuildsIndex(t *testing.T) {
	store, err := newDeployTokenStore(t.TempDir())
	if err != nil {
		t.Fatalf("newDeployTokenStore: %v", err)
	}
	secret := deployTokenPrefix + "seeded"
	if err := store.save(deployToken{ID: "t", Name: "Seeded", Hash: hashDeployToken(secret)}); err != nil {
		t.Fatalf("save: %v", err)
	}
	s := &Server{deployTokenStore: store, deployTokens: newDeployTokenIndex()}
	if err := s.loadDeployTokens(); err != nil {
		t.Fatalf("loadDeployTokens: %v", err)
	}
	if _, ok := s.deployTokens.match(secret); !ok {
		t.Error("index does not carry the seeded token after load")
	}

	brokenSrv := &Server{
		deployTokenStore: &deployTokenStore{dir: filepath.Join(t.TempDir(), "gone")},
		deployTokens:     newDeployTokenIndex(),
	}
	if err := brokenSrv.loadDeployTokens(); err == nil {
		t.Error("loadDeployTokens with a broken store: want an error")
	}
}

func TestDeployTokenHandlerBadInput(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	do := func(method, path, body string) int {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// An unreadable body, malformed JSON, and a blank name are each a 400.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/deploy-tokens", errReader{}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unreadable body = %d, want 400", rec.Code)
	}
	if got := do(http.MethodPost, "/api/v1/deploy-tokens", `{`); got != http.StatusBadRequest {
		t.Errorf("malformed JSON = %d, want 400", got)
	}
	if got := do(http.MethodPost, "/api/v1/deploy-tokens", `{"name":"   "}`); got != http.StatusBadRequest {
		t.Errorf("blank name = %d, want 400", got)
	}
}

func TestDeployTokenHandlerStoreErrors(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	do := func(method, path, body string) int {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	real := srv.deployTokenStore
	srv.deployTokenStore = &deployTokenStore{dir: filepath.Join(t.TempDir(), "gone")}
	if got := do(http.MethodPost, "/api/v1/deploy-tokens", `{"name":"x"}`); got != http.StatusInternalServerError {
		t.Errorf("mint with a broken store = %d, want 500", got)
	}
	if got := do(http.MethodGet, "/api/v1/deploy-tokens", ""); got != http.StatusInternalServerError {
		t.Errorf("list with a broken store = %d, want 500", got)
	}
	if got := do(http.MethodDelete, "/api/v1/deploy-tokens/x", ""); got != http.StatusInternalServerError {
		t.Errorf("revoke with a broken store = %d, want 500", got)
	}
	srv.deployTokenStore = real
}

func TestImportBundleStoreErrors(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	body := `{"application":"Imported","release":{"version":1},"artifacts":[{"kind":"process","processId":"imp","xml":"<definitions xmlns=\"http://www.omg.org/spec/BPMN/20100524/MODEL\"><process id=\"imp\" isExecutable=\"true\"><startEvent id=\"s\"/><endEvent id=\"e\"/><sequenceFlow id=\"f\" sourceRef=\"s\" targetRef=\"e\"/></process></definitions>"}]}`

	do := func() int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/applications/import", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// An unreadable body is a 400, before any store is touched.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/applications/import", errReader{}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("import with an unreadable body = %d, want 400", rec.Code)
	}

	// Resolving (or creating) the application reads the project store first.
	realProjects := srv.projects
	srv.projects = &projectStore{dir: filepath.Join(t.TempDir(), "gone")}
	if got := do(); got != http.StatusInternalServerError {
		t.Errorf("import with a broken project store = %d, want 500", got)
	}
	srv.projects = realProjects

	// Then the release history, to refuse a version already held.
	realReleases := srv.releases
	srv.releases = &releaseStore{dir: filepath.Join(t.TempDir(), "gone")}
	if got := do(); got != http.StatusInternalServerError {
		t.Errorf("import with a broken release store = %d, want 500", got)
	}
	srv.releases = realReleases
}
