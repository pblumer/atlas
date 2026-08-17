package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// Deploy tokens (ADR-0129) are the credential a peer Atlas presents to publish a
// bundle here. They are the first durable machine credential in Atlas, so these
// tests pin the properties that make one safe: the secret is shown once and never
// again, the stored form is not the secret, revocation is immediate, and — the
// load-bearing one — a valid deploy token reaches *only* the import endpoint.

// bearerReq issues a request authenticated with an Authorization: Bearer token,
// the way a peer Atlas publishes here (ADR-0129).
func bearerReq(t *testing.T, ts *httptest.Server, method, path, body, token string) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, r)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, data
}

// bootAuth boots the whole persistence stack with authentication enabled over a
// fixed data dir, so a test can restart it and check a credential still works.
func bootAuth(t *testing.T, dir string) *stack {
	t.Helper()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := api.New(proc, store, dir, api.WithAuth())
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	return &stack{ts: httptest.NewServer(srv.Handler()), srv: srv, store: store, log: log}
}

// mintToken creates a deploy token and returns (id, secret).
func mintToken(t *testing.T, c *http.Client, ts *httptest.Server, name string) (string, string) {
	t.Helper()
	code, body := cReq(t, c, ts, http.MethodPost, "/api/v1/deploy-tokens", `{"name":"`+name+`"}`)
	if code != http.StatusOK {
		t.Fatalf("mint deploy token = %d body=%s", code, body)
	}
	var out struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode token: %v (%s)", err, body)
	}
	if out.ID == "" || out.Token == "" {
		t.Fatalf("minted token = %+v, want an id and a secret", out)
	}
	if !strings.HasPrefix(out.Token, "atlasdt_") {
		t.Errorf("token %q lacks the atlasdt_ prefix that makes it recognizable to secret scanners", out.Token)
	}
	return out.ID, out.Token
}

// TestDeployTokenLifecycle covers mint → list → revoke, and the secret-handling
// rules: the token value is returned exactly once and never stored in a form the
// listing can leak.
func TestDeployTokenLifecycle(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	c := newClient(t)
	if code := login(t, c, ts, "admin", "password1"); code != http.StatusOK {
		t.Fatalf("login = %d", code)
	}

	id, secret := mintToken(t, c, ts, "Test-Server")

	// The listing shows the token's identity but never its secret.
	code, body := cReq(t, c, ts, http.MethodGet, "/api/v1/deploy-tokens", "")
	if code != http.StatusOK {
		t.Fatalf("list = %d body=%s", code, body)
	}
	if !strings.Contains(string(body), `"name":"Test-Server"`) || !strings.Contains(string(body), id) {
		t.Fatalf("listing does not show the token: %s", body)
	}
	if strings.Contains(string(body), secret) {
		t.Fatal("the listing leaked the token secret")
	}
	if strings.Contains(string(body), `"hash"`) || strings.Contains(string(body), `"token"`) {
		t.Fatalf("listing exposes a secret-bearing field: %s", body)
	}

	// Revoking removes it.
	if code, _ := cReq(t, c, ts, http.MethodDelete, "/api/v1/deploy-tokens/"+id, ""); code != http.StatusNoContent {
		t.Fatalf("revoke = %d", code)
	}
	if _, body := cReq(t, c, ts, http.MethodGet, "/api/v1/deploy-tokens", ""); strings.Contains(string(body), id) {
		t.Fatalf("revoked token still listed: %s", body)
	}
}

// TestDeployTokenManagementIsAdminOnly pins that minting and listing credentials is
// an administrator's act, not something any signed-in user can do.
func TestDeployTokenManagementIsAdminOnly(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	if code := login(t, admin, ts, "admin", "password1"); code != http.StatusOK {
		t.Fatalf("admin login = %d", code)
	}
	// A plain (non-admin) user.
	if code, body := cReq(t, admin, ts, http.MethodPost, "/api/v1/users",
		`{"username":"bob","password":"password2","roles":[]}`); code != http.StatusCreated {
		t.Fatalf("create user = %d body=%s", code, body)
	}
	bob := newClient(t)
	if code := login(t, bob, ts, "bob", "password2"); code != http.StatusOK {
		t.Fatalf("bob login = %d", code)
	}

	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/deploy-tokens", `{"name":"sneaky"}`},
		{http.MethodGet, "/api/v1/deploy-tokens", ""},
		{http.MethodDelete, "/api/v1/deploy-tokens/whatever", ""},
	} {
		if code, _ := cReq(t, bob, ts, tc.method, tc.path, tc.body); code != http.StatusForbidden {
			t.Errorf("%s %s as a non-admin = %d, want 403", tc.method, tc.path, code)
		}
	}
}

// TestDeployTokenReachesOnlyTheImportEndpoint is the security-critical assertion.
// A deploy token authenticates a peer that publishes here; it must not be usable to
// read or change anything else. The gate is a fail-closed allowlist, so this checks
// a broad sample of the surface — a leaked token that could list instances, read
// drafts, or manage users would be a far worse credential than the one ADR-0129
// argued for.
func TestDeployTokenReachesOnlyTheImportEndpoint(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	c := newClient(t)
	if code := login(t, c, ts, "admin", "password1"); code != http.StatusOK {
		t.Fatalf("login = %d", code)
	}
	_, secret := mintToken(t, c, ts, "Peer")

	// Every one of these is refused, even though the token is valid.
	forbidden := []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/applications", ""},
		{http.MethodPost, "/api/v1/applications", `{"name":"x"}`},
		{http.MethodGet, "/api/v1/processes", ""},
		{http.MethodGet, "/api/v1/instances", ""},
		{http.MethodGet, "/api/v1/drafts", ""},
		{http.MethodPost, "/api/v1/deployments", "<definitions/>"},
		{http.MethodGet, "/api/v1/users", ""},
		{http.MethodGet, "/api/v1/secrets", ""},
		{http.MethodGet, "/api/v1/deploy-tokens", ""},
		{http.MethodGet, "/api/v1/backup", ""},
		{http.MethodGet, "/api/v1/tasks", ""},
	}
	for _, tc := range forbidden {
		code, _ := bearerReq(t, ts, tc.method, tc.path, tc.body, secret)
		if code != http.StatusForbidden {
			t.Errorf("deploy token on %s %s = %d, want 403 (a deploy token must reach only the import endpoint)", tc.method, tc.path, code)
		}
	}

	// The import endpoint accepts it. An empty bundle is a bad request, not a 401
	// or 403 — which proves the token authenticated and authorized.
	if code, body := bearerReq(t, ts, http.MethodPost, "/api/v1/applications/import", `{}`, secret); code == http.StatusUnauthorized || code == http.StatusForbidden {
		t.Fatalf("deploy token rejected on the import endpoint: %d %s", code, body)
	}
}

// TestRevokedDeployTokenIsRejected proves revocation takes effect immediately.
func TestRevokedDeployTokenIsRejected(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	c := newClient(t)
	if code := login(t, c, ts, "admin", "password1"); code != http.StatusOK {
		t.Fatalf("login = %d", code)
	}
	id, secret := mintToken(t, c, ts, "Doomed")

	if code, _ := bearerReq(t, ts, http.MethodPost, "/api/v1/applications/import", `{}`, secret); code == http.StatusUnauthorized {
		t.Fatal("token did not authenticate before revocation")
	}
	if code, _ := cReq(t, c, ts, http.MethodDelete, "/api/v1/deploy-tokens/"+id, ""); code != http.StatusNoContent {
		t.Fatal("revoke failed")
	}
	if code, _ := bearerReq(t, ts, http.MethodPost, "/api/v1/applications/import", `{}`, secret); code != http.StatusUnauthorized {
		t.Errorf("revoked token still authenticates: %d", code)
	}
}

// TestUnknownDeployTokenIsRejected covers the plain wrong-credential path.
func TestUnknownDeployTokenIsRejected(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	if code, _ := bearerReq(t, ts, http.MethodPost, "/api/v1/applications/import", `{}`, "atlasdt_deadbeef"); code != http.StatusUnauthorized {
		t.Errorf("unknown token = %d, want 401", code)
	}
}

// TestPeerPublishesThroughADeployToken is the whole ADR-0129 receiving flow end to
// end: an operator mints a credential for a peer, the peer presents it as a bearer
// and posts a release bundle, and the bundle lands as a deployed application whose
// release records *which peer* published it.
func TestPeerPublishesThroughADeployToken(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	c := newClient(t)
	if code := login(t, c, ts, "admin", "password1"); code != http.StatusOK {
		t.Fatalf("login = %d", code)
	}
	tokenID, secret := mintToken(t, c, ts, "Produktion")

	bundle := importBody("Onboarding", 7, "shipped from Test", "peerproc")
	code, body := bearerReq(t, ts, http.MethodPost, "/api/v1/applications/import", bundle, secret)
	if code != http.StatusOK {
		t.Fatalf("peer import = %d body=%s", code, body)
	}
	var res importResult
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode import: %v (%s)", err, body)
	}
	if !res.Imported || res.Release == nil || res.Release.Version != 7 {
		t.Fatalf("import = %+v, want the publisher's v7", res)
	}

	// The admin can see what the peer published, attributed to the token that did
	// it — so revoking the right credential later is a decision, not a guess.
	code, raw := cReq(t, c, ts, http.MethodGet, "/api/v1/applications/"+res.ApplicationID+"/releases", "")
	if code != http.StatusOK {
		t.Fatalf("releases = %d body=%s", code, raw)
	}
	var hist []struct {
		Version     int32  `json:"version"`
		PublishedBy string `json:"publishedBy"`
	}
	if err := json.Unmarshal(raw, &hist); err != nil {
		t.Fatalf("decode releases: %v (%s)", err, raw)
	}
	if len(hist) != 1 || hist[0].Version != 7 {
		t.Fatalf("release history = %+v, want a single v7", hist)
	}
	if !strings.Contains(hist[0].PublishedBy, tokenID) {
		t.Errorf("publishedBy = %q, want it to name the deploy token %s that published", hist[0].PublishedBy, tokenID)
	}
}

// TestDeployTokensSurviveRestart is the recovery test: tokens are durable design-time
// state, so a peer's credential keeps working across a restart.
func TestDeployTokensSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ATLAS_ADMIN_USERNAME", "admin")
	t.Setenv("ATLAS_ADMIN_PASSWORD", "password1")

	first := bootAuth(t, dir)
	c := newClient(t)
	if code := login(t, c, first.ts, "admin", "password1"); code != http.StatusOK {
		t.Fatalf("login = %d", code)
	}
	_, secret := mintToken(t, c, first.ts, "Persistent")
	first.shutdown()

	second := bootAuth(t, dir)
	defer second.shutdown()

	if code, body := bearerReq(t, second.ts, http.MethodPost, "/api/v1/applications/import", `{}`, secret); code == http.StatusUnauthorized {
		t.Fatalf("token stopped authenticating after restart: %d %s", code, body)
	}
}
