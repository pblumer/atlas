package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/runloop"
	"github.com/pblumer/atlas/api/vault"
	"github.com/pblumer/atlas/connector/clio"
)

func testVaultKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return k
}

func newTestVault(t *testing.T) *vault.Vault {
	t.Helper()
	v, err := vault.New(filepath.Join(t.TempDir(), "vault"), testVaultKey(t))
	if err != nil {
		t.Fatalf("vault.New: %v", err)
	}
	return v
}

// newVaultServer builds an api server with the vault enabled via the environment
// (the production enablement path), so handler tests exercise the real wiring.
func newVaultServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv(vault.KeyEnv, hex.EncodeToString(testVaultKey(t)))
	srv, _ := newValidateServer(t)
	if srv.vault == nil {
		t.Fatal("vault not enabled")
	}
	return srv
}

// TestSecretHandlersCRUD drives the HTTP surface end to end and asserts no response
// ever carries the secret value, and that a stored secret resolves as a connector
// credential.
func TestSecretHandlersCRUD(t *testing.T) {
	srv := newVaultServer(t)
	h := srv.Handler()

	put := func(name, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/secrets/"+name, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := put("gmail_ops", `{"value":"s3cr3t"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: code=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "s3cr3t") {
		t.Errorf("PUT response leaked the secret value: %s", rec.Body.String())
	}

	greq := httptest.NewRequest(http.MethodGet, "/api/v1/secrets", nil)
	grec := httptest.NewRecorder()
	h.ServeHTTP(grec, greq)
	if grec.Code != http.StatusOK {
		t.Fatalf("GET: code=%d", grec.Code)
	}
	if !strings.Contains(grec.Body.String(), "gmail_ops") {
		t.Errorf("GET list missing the secret name: %s", grec.Body.String())
	}
	if strings.Contains(grec.Body.String(), "s3cr3t") {
		t.Errorf("GET list leaked the secret value: %s", grec.Body.String())
	}

	if got := srv.resolveConnectorSecret("gmail_ops"); got != "s3cr3t" {
		t.Errorf("resolveConnectorSecret = %q, want s3cr3t (vault-resolved)", got)
	}

	dreq := httptest.NewRequest(http.MethodDelete, "/api/v1/secrets/gmail_ops", nil)
	drec := httptest.NewRecorder()
	h.ServeHTTP(drec, dreq)
	if drec.Code != http.StatusNoContent {
		t.Fatalf("DELETE: code=%d", drec.Code)
	}
	if got := srv.resolveConnectorSecret("gmail_ops"); got != "" {
		t.Errorf("after delete resolveConnectorSecret = %q, want empty", got)
	}
}

// TestSecretUpdateRebuildsConnectorClients proves a rotated secret reaches the live
// connector clients immediately: setting (or deleting) the secret a clio connector
// references rebuilds the registry, so the bridge/worker picks up the new token
// without the operator re-saving the connector. The Authorization header the clio
// client sends is the observable proof of which token is live.
func TestSecretUpdateRebuildsConnectorClients(t *testing.T) {
	var gotAuth string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	srv := newVaultServer(t)
	h := srv.Handler()
	put := func(name, val string) int {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/secrets/"+name, strings.NewReader(`{"value":"`+val+`"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// Seed the token, then a clio connector referencing it (create builds the client).
	if put("clio-token", "tokenA") != http.StatusOK {
		t.Fatal("seed secret")
	}
	x := deployTestHarness{t, h}
	if code, cb := x.do(http.MethodPost, "/api/v1/connectors",
		`{"name":"events","kind":"clio","endpoint":"`+ts.URL+`","credentialsRef":"clio-token"}`); code != http.StatusOK {
		t.Fatalf("create connector: %d %s", code, cb)
	}

	// Fire the live client at the test server and read back the token it carried.
	fire := func() {
		srv.do(func() {
			c, ok := srv.clioRegistry.Client("events")
			if !ok {
				t.Fatal("clio client not registered")
			}
			_ = c.WriteEvent(context.Background(), clio.Event{Subject: "/s", Type: "T"})
		})
	}
	fire()
	if gotAuth != "Bearer tokenA" {
		t.Fatalf("before rotation: Authorization=%q, want Bearer tokenA", gotAuth)
	}

	// Rotate the secret WITHOUT touching the connector: the rebuild must swap the client.
	if put("clio-token", "tokenB") != http.StatusOK {
		t.Fatal("rotate secret")
	}
	fire()
	if gotAuth != "Bearer tokenB" {
		t.Fatalf("after rotation: Authorization=%q, want Bearer tokenB (secret update did not rebuild the client)", gotAuth)
	}

	// Deleting the secret also rebuilds: the client resolves to no token.
	dreq := httptest.NewRequest(http.MethodDelete, "/api/v1/secrets/clio-token", nil)
	drec := httptest.NewRecorder()
	h.ServeHTTP(drec, dreq)
	if drec.Code != http.StatusNoContent {
		t.Fatalf("DELETE: code=%d", drec.Code)
	}
	fire()
	if gotAuth != "" {
		t.Fatalf("after delete: Authorization=%q, want empty (no token)", gotAuth)
	}
}

// TestProvisionClioKey drives one-click provisioning end to end: an admin token
// mints a scoped read key on a fake clio, the key is sealed as the connector's
// credential, and the live clio client carries the minted token at once — no
// copy-paste, and the connector needed no credentialsRef beforehand.
func TestProvisionClioKey(t *testing.T) {
	var mintAuth, mintPath, writeAuth string
	var mintBody map[string]any
	clioSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/keys":
			mintAuth, mintPath = r.Header.Get("Authorization"), r.URL.Path
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &mintBody)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"kid":"kid_new","secret":"kid_new.minted"}`))
		case "/api/v1/write-events":
			writeAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer clioSrv.Close()

	srv := newVaultServer(t)
	x := deployTestHarness{t, srv.Handler()}
	code, cb := x.do(http.MethodPost, "/api/v1/connectors",
		`{"name":"events","kind":"clio","endpoint":"`+clioSrv.URL+`"}`) // no credentialsRef yet
	if code != http.StatusOK {
		t.Fatalf("create connector: %d %s", code, cb)
	}
	var conn connector
	_ = json.Unmarshal(cb, &conn)

	code, pb := x.do(http.MethodPost, "/api/v1/connectors/"+conn.ID+"/provision-clio-key",
		`{"adminToken":"ADMIN","subject":"/employees","recursive":true}`)
	if code != http.StatusOK {
		t.Fatalf("provision: %d %s", code, pb)
	}
	if mintPath != "/api/v1/keys" || mintAuth != "Bearer ADMIN" {
		t.Errorf("mint path/auth = %q / %q", mintPath, mintAuth)
	}
	scopes, _ := mintBody["scopes"].([]any)
	if len(scopes) != 1 || scopes[0] != "read:/employees/*" {
		t.Errorf("mint scopes = %#v, want [read:/employees/*]", mintBody["scopes"])
	}
	var resp struct {
		CredentialsRef string `json:"credentialsRef"`
	}
	_ = json.Unmarshal(pb, &resp)
	if resp.CredentialsRef == "" || strings.Contains(string(pb), "minted") {
		t.Fatalf("provision response = %s (must carry a ref, never the token)", pb)
	}

	// The credential resolves to the minted token, and the live client uses it.
	var resolved string
	srv.do(func() {
		resolved = srv.resolveConnectorSecret(resp.CredentialsRef)
		c, ok := srv.clioRegistry.Client("events")
		if !ok {
			t.Fatal("clio client not registered after provisioning")
		}
		_ = c.WriteEvent(context.Background(), clio.Event{Subject: "/s", Type: "T"})
	})
	if resolved != "kid_new.minted" {
		t.Errorf("resolved credential = %q, want kid_new.minted", resolved)
	}
	if writeAuth != "Bearer kid_new.minted" {
		t.Errorf("live client Authorization = %q, want Bearer kid_new.minted (rebuild did not happen)", writeAuth)
	}

	// A second provision on the now-referenced connector exercises the path where the
	// credentialsRef already exists (no connector re-save, just re-seal + rebuild).
	if code, pb := x.do(http.MethodPost, "/api/v1/connectors/"+conn.ID+"/provision-clio-key",
		`{"adminToken":"ADMIN","subject":"/employees","recursive":false}`); code != http.StatusOK {
		t.Fatalf("re-provision: %d %s", code, pb)
	}
	scopes, _ = mintBody["scopes"].([]any)
	if len(scopes) != 1 || scopes[0] != "read:/employees" {
		t.Errorf("re-provision scopes = %#v, want [read:/employees] (exact, non-recursive)", mintBody["scopes"])
	}
}

// TestProvisionClioKeyRequireAdmin proves the provisioning endpoint refuses a
// non-admin when auth is enabled.
func TestProvisionClioKeyRequireAdmin(t *testing.T) {
	srv := newVaultServer(t)
	srv.authEnabled = true
	rec := httptest.NewRecorder()
	srv.handleProvisionClioKey(rec, httptest.NewRequest(http.MethodPost, "/api/v1/connectors/x/provision-clio-key", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("provision without admin: code=%d, want 403", rec.Code)
	}
}

// TestProvisionClioKeyErrors covers the provisioning handler's rejection paths:
// missing fields, bad JSON, a non-clio/unknown connector, a clio that refuses the
// mint (502), and the vault-disabled 503.
func TestProvisionClioKeyErrors(t *testing.T) {
	forbid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden) // clio refuses the mint (bad admin token)
	}))
	defer forbid.Close()

	srv := newVaultServer(t)
	x := deployTestHarness{t, srv.Handler()}
	_, cb := x.do(http.MethodPost, "/api/v1/connectors", `{"name":"ev","kind":"clio","endpoint":"`+forbid.URL+`"}`)
	var clioConn connector
	_ = json.Unmarshal(cb, &clioConn)
	_, tb := x.do(http.MethodPost, "/api/v1/connectors", `{"name":"dec","kind":"temis","endpoint":"http://y"}`)
	var temisConn connector
	_ = json.Unmarshal(tb, &temisConn)

	post := func(path, body string) int { code, _ := x.do(http.MethodPost, path, body); return code }
	base := "/api/v1/connectors/"

	// A body that errors mid-read → 400.
	h := srv.Handler()
	rreq := httptest.NewRequest(http.MethodPost, base+clioConn.ID+"/provision-clio-key", errReader{})
	rreq.Header.Set("Content-Type", "application/json")
	rrec := httptest.NewRecorder()
	h.ServeHTTP(rrec, rreq)
	if rrec.Code != http.StatusBadRequest {
		t.Errorf("read-body error: want 400, got %d", rrec.Code)
	}

	// A clio connector saved without an endpoint → 400.
	srv.do(func() {
		_ = srv.connectors.Save(connector{ID: "noep", Name: "noep", Kind: connectorKindClio, Enabled: true, CreatedAt: 9})
	})
	if post(base+"noep/provision-clio-key", `{"adminToken":"a","subject":"/e"}`) != http.StatusBadRequest {
		t.Error("connector with no endpoint: want 400")
	}

	if post(base+clioConn.ID+"/provision-clio-key", `{"subject":"/e"}`) != http.StatusBadRequest {
		t.Error("missing adminToken: want 400")
	}
	if post(base+clioConn.ID+"/provision-clio-key", `{"adminToken":"a"}`) != http.StatusBadRequest {
		t.Error("missing subject: want 400")
	}
	if post(base+clioConn.ID+"/provision-clio-key", `{bad`) != http.StatusBadRequest {
		t.Error("invalid JSON: want 400")
	}
	if post(base+temisConn.ID+"/provision-clio-key", `{"adminToken":"a","subject":"/e"}`) != http.StatusBadRequest {
		t.Error("temis connector: want 400")
	}
	if post(base+"missing/provision-clio-key", `{"adminToken":"a","subject":"/e"}`) != http.StatusBadRequest {
		t.Error("unknown connector: want 400")
	}
	// A corrupt connector record makes the load error → 500.
	srv.do(func() {
		_ = os.WriteFile(srv.connectors.FileFor("corrupt"), []byte("{not json"), 0o644)
	})
	if post(base+"corrupt/provision-clio-key", `{"adminToken":"a","subject":"/e"}`) != http.StatusInternalServerError {
		t.Error("corrupt connector record: want 500")
	}
	if post(base+clioConn.ID+"/provision-clio-key", `{"adminToken":"a","subject":"/e"}`) != http.StatusBadGateway {
		t.Error("clio refuses the mint: want 502")
	}

	// Vault disabled → 503.
	noVault, _ := newValidateServer(t, WithoutVault())
	nx := deployTestHarness{t, noVault.Handler()}
	_, ncb := nx.do(http.MethodPost, "/api/v1/connectors", `{"name":"c","kind":"clio","endpoint":"http://x"}`)
	var nconn connector
	_ = json.Unmarshal(ncb, &nconn)
	if c, _ := nx.do(http.MethodPost, base+nconn.ID+"/provision-clio-key", `{"adminToken":"a","subject":"/e"}`); c != http.StatusServiceUnavailable {
		t.Errorf("vault disabled: want 503, got %d", c)
	}
}

// TestVaultOnByDefault proves the opt-out default (ADR-0070): with no operator key
// the server still builds a vault and generates its key file at 0600.
func TestVaultOnByDefault(t *testing.T) {
	t.Setenv(vault.KeyEnv, "")
	t.Setenv(vault.KeyFileEnv, "")
	srv, dir := newValidateServer(t)
	if srv.vault == nil {
		t.Fatal("vault should be on by default (opt-out, ADR-0070)")
	}
	info, err := os.Stat(filepath.Join(dir, "vault.key"))
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key file mode = %o, want 600", perm)
	}
	if _, err := srv.vault.Set("k", "v"); err != nil {
		t.Errorf("generated key should work: Set: %v", err)
	}
}

// TestResolveConnectorSecretVaultOverEnv proves the vault takes precedence over an
// environment reference, and that a vault miss falls back to the environment.
func TestResolveConnectorSecretVaultOverEnv(t *testing.T) {
	srv := newVaultServer(t)
	t.Setenv("ATLAS_CONNECTOR_SHARED_TOKEN", "from-env")
	if _, err := srv.vault.Set("shared", "from-vault"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := srv.resolveConnectorSecret("shared"); got != "from-vault" {
		t.Errorf("resolveConnectorSecret = %q, want from-vault (vault wins)", got)
	}
	if got := srv.resolveConnectorSecret("shared_env_only"); got != "" {
		t.Errorf("unset ref = %q, want empty", got)
	}
	t.Setenv("ATLAS_CONNECTOR_ONLYENV_TOKEN", "env-value")
	if got := srv.resolveConnectorSecret("onlyenv"); got != "env-value" {
		t.Errorf("vault miss should fall back to env, got %q", got)
	}
}

// TestSecretHandlersVaultDisabled proves that with the vault disabled (WithoutVault)
// the CRUD endpoints report 503 rather than doing anything.
func TestSecretHandlersVaultDisabled(t *testing.T) {
	t.Setenv(vault.KeyEnv, "")
	t.Setenv(vault.KeyFileEnv, "")
	srv, _ := newValidateServer(t, WithoutVault())
	if srv.vault != nil {
		t.Fatal("vault should be nil when disabled with WithoutVault")
	}
	h := srv.Handler()
	check := func(method, path, body string) int {
		var req *http.Request
		if body == "" {
			req = httptest.NewRequest(method, path, nil)
		} else {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := check(http.MethodGet, "/api/v1/secrets", ""); code != http.StatusServiceUnavailable {
		t.Errorf("GET disabled: code=%d, want 503", code)
	}
	if code := check(http.MethodPut, "/api/v1/secrets/k", `{"value":"v"}`); code != http.StatusServiceUnavailable {
		t.Errorf("PUT disabled: code=%d, want 503", code)
	}
	if code := check(http.MethodDelete, "/api/v1/secrets/k", ""); code != http.StatusServiceUnavailable {
		t.Errorf("DELETE disabled: code=%d, want 503", code)
	}
}

// TestSetSecretValidation covers the input guards on the set handler.
func TestSetSecretValidation(t *testing.T) {
	srv := newVaultServer(t)
	h := srv.Handler()
	put := func(name, body string) int {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/secrets/"+name, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := put("k", "not json"); code != http.StatusBadRequest {
		t.Errorf("invalid JSON: code=%d, want 400", code)
	}
	if code := put("k", `{"value":""}`); code != http.StatusBadRequest {
		t.Errorf("empty value: code=%d, want 400", code)
	}

	// Empty {name} is unreachable through the mux, so exercise the guard directly.
	rec := httptest.NewRecorder()
	srv.handleSetSecret(rec, httptest.NewRequest(http.MethodPut, "/api/v1/secrets/x", strings.NewReader(`{"value":"v"}`)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing path name (set): code=%d, want 400", rec.Code)
	}
	drec := httptest.NewRecorder()
	srv.handleDeleteSecret(drec, httptest.NewRequest(http.MethodDelete, "/api/v1/secrets/x", nil))
	if drec.Code != http.StatusBadRequest {
		t.Errorf("missing path name (delete): code=%d, want 400", drec.Code)
	}
}

// TestSecretHandlersRequireAdmin proves each handler refuses a non-admin when auth is
// enforced. Called directly so the per-handler guard is what returns 403 (the global
// middleware would return 401 first through the mux).
func TestSecretHandlersRequireAdmin(t *testing.T) {
	srv := newVaultServer(t)
	srv.authEnabled = true
	forbidden := func(fn func(http.ResponseWriter, *http.Request), method, path string) int {
		rec := httptest.NewRecorder()
		fn(rec, httptest.NewRequest(method, path, nil))
		return rec.Code
	}
	if code := forbidden(srv.handleListSecrets, http.MethodGet, "/api/v1/secrets"); code != http.StatusForbidden {
		t.Errorf("list without admin: code=%d, want 403", code)
	}
	if code := forbidden(srv.handleSetSecret, http.MethodPut, "/api/v1/secrets/k"); code != http.StatusForbidden {
		t.Errorf("set without admin: code=%d, want 403", code)
	}
	if code := forbidden(srv.handleDeleteSecret, http.MethodDelete, "/api/v1/secrets/k"); code != http.StatusForbidden {
		t.Errorf("delete without admin: code=%d, want 403", code)
	}
}

// TestSecretHandlersStoreErrors points the vault at a missing directory so the store
// operations fail, covering the handlers' 500 paths.
func TestSecretHandlersStoreErrors(t *testing.T) {
	srv := newVaultServer(t)
	// Point the vault at a directory that no longer exists, so every store
	// operation fails and the handlers take their 500 paths.
	gone := filepath.Join(t.TempDir(), "gone")
	v, err := vault.New(gone, testVaultKey(t))
	if err != nil {
		t.Fatalf("vault.New: %v", err)
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatalf("remove: %v", err)
	}
	srv.vault = v
	h := srv.Handler()
	do := func(method, path, body string) int {
		var req *http.Request
		if body == "" {
			req = httptest.NewRequest(method, path, nil)
		} else {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if code := do(http.MethodGet, "/api/v1/secrets", ""); code != http.StatusInternalServerError {
		t.Errorf("list store error: code=%d, want 500", code)
	}
	if code := do(http.MethodPut, "/api/v1/secrets/k", `{"value":"v"}`); code != http.StatusInternalServerError {
		t.Errorf("set store error: code=%d, want 500", code)
	}
	if code := do(http.MethodDelete, "/api/v1/secrets/k", ""); code != http.StatusInternalServerError {
		t.Errorf("delete store error: code=%d, want 500", code)
	}
}

// TestSetSecretBodyReadError covers the set handler's request-body read-error branch.
func TestSetSecretBodyReadError(t *testing.T) {
	srv := newVaultServer(t)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/secrets/k", errReader{})
	req.SetPathValue("name", "k")
	rec := httptest.NewRecorder()
	srv.handleSetSecret(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("body read error: code=%d, want 400", rec.Code)
	}
}

// TestSetSecretServerClosing covers the guard for a set that races server shutdown:
// with quit already closed, s.do skips the closure, so no metadata is produced and
// the handler reports 503 rather than a misleading success.
func TestSetSecretServerClosing(t *testing.T) {
	q := make(chan struct{})
	close(q)
	// A closing server: the run loop is built but never run, and quit is already
	// closed, so do() takes its quit arm and the closure never produces metadata.
	srv := &Server{quit: q, runLoop: runloop.New(q), vault: newTestVault(t)}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/secrets/k", strings.NewReader(`{"value":"v"}`))
	req.SetPathValue("name", "k")
	rec := httptest.NewRecorder()
	srv.handleSetSecret(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("set during shutdown: code=%d, want 503", rec.Code)
	}
}

// TestListSecretsEmpty covers the empty-list normalization (nil slice -> []).
func TestListSecretsEmpty(t *testing.T) {
	srv := newVaultServer(t)
	h := srv.Handler()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/secrets", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET empty: code=%d", rec.Code)
	}
	if strings.TrimSpace(rec.Body.String()) != "[]" {
		t.Errorf("empty list body = %q, want []", rec.Body.String())
	}
}

// TestClioReadScope covers the scope-string builder for exact and recursive grants.
func TestClioReadScope(t *testing.T) {
	cases := []struct {
		subject   string
		recursive bool
		want      string
	}{
		{"/employees", true, "read:/employees/*"},
		{"/employees", false, "read:/employees"},
		{"employees", true, "read:/employees/*"}, // made absolute
		{"/employees/", true, "read:/employees/*"},
		{"/", true, "read:/*"},
	}
	for _, c := range cases {
		if got := clioReadScope(c.subject, c.recursive); got != c.want {
			t.Errorf("clioReadScope(%q, %v) = %q, want %q", c.subject, c.recursive, got, c.want)
		}
	}
}
