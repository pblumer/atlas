package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Store, URL-validation, and handler branches of the ADR-0129 sending half that a
// successful promotion never reaches. The end-to-end behaviour lives in
// promote_test.go.

func TestValidateTargetURL(t *testing.T) {
	ok := []struct{ in, want string }{
		{"https://atlas.example", "https://atlas.example"},
		{"https://atlas.example/", "https://atlas.example"},
		{"https://atlas.example/base/", "https://atlas.example/base"},
		{"http://localhost:8080", "http://localhost:8080"},
		{"http://127.0.0.1:9000", "http://127.0.0.1:9000"},
		{"http://[::1]:9000", "http://[::1]:9000"},
	}
	for _, tc := range ok {
		got, err := validateTargetURL(tc.in)
		if err != nil {
			t.Errorf("validateTargetURL(%q) = error %v, want ok", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("validateTargetURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// Plaintext to anywhere but this machine is refused: a deploy token over http
	// is a credential handed to everyone on the path.
	bad := []string{"", "   ", "http://elsewhere.example", "ftp://x", "https://", "://nope"}
	for _, in := range bad {
		if _, err := validateTargetURL(in); err == nil {
			t.Errorf("validateTargetURL(%q) accepted, want an error", in)
		}
	}
}

func TestIsLoopbackHost(t *testing.T) {
	for _, h := range []string{"localhost", "127.0.0.1", "127.5.5.5", "::1"} {
		if !isLoopbackHost(h) {
			t.Errorf("isLoopbackHost(%q) = false, want true", h)
		}
	}
	for _, h := range []string{"example.com", "10.0.0.1", "", "notanip"} {
		if isLoopbackHost(h) {
			t.Errorf("isLoopbackHost(%q) = true, want false", h)
		}
	}
}

func TestTargetStoreErrorsAndSkips(t *testing.T) {
	dir := t.TempDir()
	s, err := newTargetStore(dir)
	if err != nil {
		t.Fatalf("newTargetStore: %v", err)
	}
	if err := s.save(deploymentTarget{ID: "a", Name: "A", CreatedAt: 1}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Foreign entries are skipped.
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "n.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	all, err := s.loadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(all) != 1 || all[0].ID != "a" {
		t.Fatalf("loadAll = %+v, want just the real record", all)
	}

	// get on an absent id is a clean miss, not an error.
	if _, ok, err := s.get("ghost"); err != nil || ok {
		t.Errorf("get(ghost) = ok=%v err=%v, want a clean miss", ok, err)
	}
	// A malformed record fails loudly on both paths.
	if err := os.WriteFile(s.fileFor("bad"), []byte("{nope"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := s.get("bad"); err == nil {
		t.Error("get on a malformed record: want an error")
	}
	if _, err := s.loadAll(); err == nil {
		t.Error("loadAll with a malformed record: want an error")
	}

	broken := &targetStore{dir: filepath.Join(t.TempDir(), "gone")}
	if _, err := broken.loadAll(); err == nil {
		t.Error("loadAll on a missing dir: want an error")
	}
	if err := broken.delete("x"); err == nil {
		t.Error("delete on a missing dir: want an error")
	}
	// get on a record path that is a directory is an error, not a miss.
	dirRec := filepath.Join(t.TempDir(), "targets")
	ds, err := newTargetStore(dirRec)
	if err != nil {
		t.Fatalf("newTargetStore: %v", err)
	}
	if err := os.MkdirAll(ds.fileFor("d"), 0o755); err != nil {
		t.Fatalf("mkdir record: %v", err)
	}
	if _, _, err := ds.get("d"); err == nil {
		t.Error("get on a directory record: want an error")
	}

	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := newTargetStore(filepath.Join(file, "targets")); err == nil {
		t.Error("newTargetStore under a file path: want an error")
	}
}

func TestTargetHandlerBadInputAndStoreErrors(t *testing.T) {
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

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/targets", errReader{}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unreadable body = %d, want 400", rec.Code)
	}
	if got := do(http.MethodPost, "/api/v1/targets", `{`); got != http.StatusBadRequest {
		t.Errorf("malformed JSON = %d, want 400", got)
	}
	if got := do(http.MethodPost, "/api/v1/targets", `{"name":"  ","baseUrl":"https://x.example"}`); got != http.StatusBadRequest {
		t.Errorf("blank name = %d, want 400", got)
	}

	real := srv.targets
	srv.targets = &targetStore{dir: filepath.Join(t.TempDir(), "gone")}
	if got := do(http.MethodPost, "/api/v1/targets", `{"name":"X","baseUrl":"https://x.example"}`); got != http.StatusInternalServerError {
		t.Errorf("create with a broken store = %d, want 500", got)
	}
	if got := do(http.MethodGet, "/api/v1/targets", ""); got != http.StatusInternalServerError {
		t.Errorf("list with a broken store = %d, want 500", got)
	}
	if got := do(http.MethodDelete, "/api/v1/targets/x", ""); got != http.StatusInternalServerError {
		t.Errorf("delete with a broken store = %d, want 500", got)
	}
	srv.targets = real
}

func TestPromoteHandlerBadInput(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	do := func(path, body string) int {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := do("/api/v1/applications/x/releases/abc/promote", `{"targetIds":["t"]}`); got != http.StatusBadRequest {
		t.Errorf("non-numeric version = %d, want 400", got)
	}
	if got := do("/api/v1/applications/x/releases/0/promote", `{"targetIds":["t"]}`); got != http.StatusBadRequest {
		t.Errorf("zero version = %d, want 400", got)
	}
	if got := do("/api/v1/applications/x/releases/1/promote", `{`); got != http.StatusBadRequest {
		t.Errorf("malformed JSON = %d, want 400", got)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/applications/x/releases/1/promote", errReader{}))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unreadable body = %d, want 400", rec.Code)
	}
}

// TestPromoteResolvePhaseStoreErrors covers the two store reads the resolve phase
// makes before any network call: a failure in either fails the whole promotion
// rather than shipping a partial bundle.
func TestPromoteResolvePhaseStoreErrors(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	// Seed a real application so authorization passes and the resolve phase runs.
	seed := httptest.NewRecorder()
	h.ServeHTTP(seed, httptest.NewRequest(http.MethodPost, "/api/v1/applications", strings.NewReader(`{"name":"App"}`)))
	if seed.Code != http.StatusOK {
		t.Fatalf("seed application = %d", seed.Code)
	}
	// Address it by its real id.
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(seed.Body.Bytes(), &app); err != nil {
		t.Fatalf("decode: %v", err)
	}
	doFor := func(appID string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/applications/"+appID+"/releases/1/promote",
			strings.NewReader(`{"targetIds":["t"]}`))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	realReleases := srv.releases
	srv.releases = &releaseStore{dir: filepath.Join(t.TempDir(), "gone")}
	if got := doFor(app.ID); got != http.StatusInternalServerError {
		t.Errorf("promote with a broken release store = %d, want 500", got)
	}
	srv.releases = realReleases

	// With the release readable but the target store broken, the resolve phase
	// fails on the target lookup instead.
	seedRelease(t, srv, app.ID)
	realTargets := srv.targets
	srv.targets = &targetStore{dir: filepath.Join(t.TempDir(), "gone")}
	if got := doFor(app.ID); got != http.StatusInternalServerError {
		t.Errorf("promote with a broken target store = %d, want 500", got)
	}
	srv.targets = realTargets

	// And with both readable but the deployment records gone, the artifacts cannot
	// be reassembled — which must fail rather than ship an incomplete bundle.
	realDeploys := srv.deploys
	srv.deploys = &deployStore{dir: filepath.Join(t.TempDir(), "gone")}
	if got := doFor(app.ID); got != http.StatusInternalServerError {
		t.Errorf("promote with a broken deploy store = %d, want 500", got)
	}
	srv.deploys = realDeploys
}

// seedRelease records a release referencing one deployed definition, so the resolve
// phase has something to assemble.
func seedRelease(t *testing.T, srv *Server, appID string) {
	t.Helper()
	var err error
	srv.do(func() {
		err = srv.releases.save(applicationRelease{
			ID: "rel-" + appID, ApplicationID: appID, Version: 1,
			Members: []releaseMember{{Kind: "process", Ref: "p", ArtifactVer: 1, Key: 12345}},
		})
	})
	if err != nil {
		t.Fatalf("seed release: %v", err)
	}
}

// TestPromoteSkipsNonProcessMembersAndCarriesLegacyDMN covers two assembly details
// of the resolve phase: a release member that is not a process is skipped (forms
// and decisions do not travel yet), and a deployment stored with the legacy
// single-DMN field still ships its model.
func TestPromoteSkipsNonProcessMembersAndCarriesLegacyDMN(t *testing.T) {
	srv := newServerForErrors(t)

	var (
		bundle importBundleReq
		err    error
	)
	srv.do(func() {
		// A deployment recorded the old way: one DMN in the singular field.
		err = srv.deploys.save(persistedDeployment{
			Key: 4242, ProcessID: "legacy", Name: "Legacy", Version: 1,
			XML: "<definitions/>", DMNXML: "<dmn-legacy/>",
		})
	})
	if err != nil {
		t.Fatalf("seed deployment: %v", err)
	}

	rel := applicationRelease{
		ID: "r", ApplicationID: "app", Version: 1,
		Members: []releaseMember{
			{Kind: "form", Ref: "a-form"},                               // skipped
			{Kind: "process", Ref: "legacy", ArtifactVer: 1, Key: 4242}, // shipped
		},
	}
	srv.do(func() { bundle = srv.bundleForRelease("App", rel, &err) })
	if err != nil {
		t.Fatalf("assemble bundle: %v", err)
	}
	if len(bundle.Artifacts) != 1 || bundle.Artifacts[0].ProcessID != "legacy" {
		t.Fatalf("artifacts = %+v, want only the process member", bundle.Artifacts)
	}
	if len(bundle.Artifacts[0].DMNXMLs) != 1 || bundle.Artifacts[0].DMNXMLs[0] != "<dmn-legacy/>" {
		t.Errorf("dmn models = %v, want the legacy single model carried across", bundle.Artifacts[0].DMNXMLs)
	}
}

// TestTargetStoreOrdersOldestFirst pins the listing order, including the id
// tie-break that keeps it stable for targets registered in the same second.
func TestTargetStoreOrdersOldestFirst(t *testing.T) {
	s, err := newTargetStore(t.TempDir())
	if err != nil {
		t.Fatalf("newTargetStore: %v", err)
	}
	for _, rec := range []deploymentTarget{
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

// TestPushBundleFailureModes drives the outbound half directly against stand-in
// peers, covering the replies a real one can give.
func TestPushBundleFailureModes(t *testing.T) {
	srv := newServerForErrors(t)

	cases := []struct {
		name      string
		status    int
		body      string
		wantOK    bool
		wantError string
	}{
		{"accepted", http.StatusOK, `{"applicationId":"r1","imported":true}`, true, ""},
		{"refused with a reason", http.StatusConflict, `{"imported":false,"reason":"already imported"}`, false, "already imported"},
		{"error payload", http.StatusBadRequest, `{"error":"bundle carries no artifacts"}`, false, "bundle carries no artifacts"},
		{"unauthorized, no body", http.StatusUnauthorized, ``, false, "HTTP 401"},
		{"non-JSON reply", http.StatusBadGateway, `<html>proxy error</html>`, false, "HTTP 502"},
		{"200 but not imported", http.StatusOK, `{"imported":false}`, false, "HTTP 200"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer peer.Close()

			res := srv.pushBundle(context.Background(),
				deploymentTarget{ID: "t", Name: tc.name, BaseURL: peer.URL}, "tok", []byte(`{}`))
			if res.OK != tc.wantOK {
				t.Fatalf("ok = %v, want %v (error %q)", res.OK, tc.wantOK, res.Error)
			}
			if tc.wantError != "" && !strings.Contains(res.Error, tc.wantError) {
				t.Errorf("error = %q, want it to mention %q", res.Error, tc.wantError)
			}
			if res.RemoteStatus != tc.status {
				t.Errorf("remote status = %d, want %d", res.RemoteStatus, tc.status)
			}
		})
	}

	// A malformed URL fails before any request is made.
	res := srv.pushBundle(context.Background(),
		deploymentTarget{ID: "t", Name: "Bad", BaseURL: "://nope"}, "", []byte(`{}`))
	if res.OK || res.Error == "" {
		t.Errorf("unbuildable request = %+v, want a reported failure", res)
	}
}

// TestFillRemoteStatusFailureModes drives the status read against stand-in peers.
// Every failure must land on the status row rather than propagating: one peer
// being unhappy says nothing about the others, and the view has to render anyway.
func TestFillRemoteStatusFailureModes(t *testing.T) {
	srv := newServerForErrors(t)

	cases := []struct {
		name          string
		status        int
		body          string
		wantReachable bool
		wantError     string
	}{
		{"answers", http.StatusOK, `{"version":3,"running":7,"finished":2,"definitions":[{"processId":"p"}]}`, true, ""},
		{"forbidden", http.StatusForbidden, ``, false, "HTTP 403"},
		{"not found", http.StatusNotFound, ``, false, "HTTP 404"},
		{"unreadable payload", http.StatusOK, `<html>nope</html>`, false, "unreadable reply"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer peer.Close()

			st := targetStatus{Bound: true}
			srv.fillRemoteStatus(context.Background(), &st,
				deploymentTarget{ID: "t", Name: tc.name, BaseURL: peer.URL}, "remote-1", "tok")

			if st.Reachable != tc.wantReachable {
				t.Fatalf("reachable = %v, want %v (error %q)", st.Reachable, tc.wantReachable, st.Error)
			}
			if tc.wantError != "" && !strings.Contains(st.Error, tc.wantError) {
				t.Errorf("error = %q, want it to mention %q", st.Error, tc.wantError)
			}
			if tc.wantReachable {
				if st.Version != 3 || st.Running != 7 || st.Finished != 2 || st.Definitions != 1 {
					t.Errorf("status = %+v, want the peer's numbers folded in", st)
				}
			}
		})
	}

	// A malformed URL fails before any request; an unreachable host after it.
	st := targetStatus{Bound: true}
	srv.fillRemoteStatus(context.Background(), &st, deploymentTarget{BaseURL: "://nope"}, "r", "")
	if st.Reachable || st.Error == "" {
		t.Errorf("unbuildable request = %+v, want a reported failure", st)
	}
}

// TestApplicationTargetsStoreError covers the 500 branch of the status view.
func TestApplicationTargetsStoreError(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()

	seed := httptest.NewRecorder()
	h.ServeHTTP(seed, httptest.NewRequest(http.MethodPost, "/api/v1/applications", strings.NewReader(`{"name":"S"}`)))
	if seed.Code != http.StatusOK {
		t.Fatalf("seed = %d", seed.Code)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(seed.Body.Bytes(), &app); err != nil {
		t.Fatalf("decode: %v", err)
	}

	srv.targets = &targetStore{dir: filepath.Join(t.TempDir(), "gone")}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/applications/"+app.ID+"/targets", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("target status with a broken store = %d, want 500", rec.Code)
	}
}
