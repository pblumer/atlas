package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/identity"
	"github.com/pblumer/atlas/api/vault"
)

// Setting up account linking in the Console (ADR-0475, amended): an administrator
// switches it on, changes it and removes it while the server runs, the start flags
// still win, and the client secret is a reserved vault entry nothing else reaches.

const (
	setupTenant = "8f5c2a1e-3b4d-4c6e-9a7b-1d2e3f4a5b6c"
	setupClient = "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	otherTenant = "11111111-2222-4333-8444-555555555555"
)

// setupServer is a server with no linking flags, whose Console setups reach f.
func setupServer(t *testing.T, f *fakeTenant, opts ...Option) (*httptest.Server, *Server) {
	t.Helper()
	t.Setenv("ATLAS_ADMIN_USERNAME", "root")
	t.Setenv("ATLAS_ADMIN_PASSWORD", "rootpassword12")
	authority := "https://login.invalid"
	if f != nil {
		authority = f.URL
	}
	srv := newServerWithOptions(t, append([]Option{WithAuth(), WithMicrosoftLinking(MicrosoftLinkConfig{Authority: authority})}, opts...)...)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, srv
}

// call sends a JSON request and returns the status and the body.
func call(t *testing.T, c *http.Client, method, url, body string) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func setupView(t *testing.T, body string) linkSetupView {
	t.Helper()
	var v linkSetupView
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("linking setup %q: %v", body, err)
	}
	return v
}

func linkable(t *testing.T, ts *httptest.Server, c *http.Client) int {
	t.Helper()
	code, body := call(t, c, http.MethodGet, ts.URL+"/api/v1/me/connections", "")
	if code != http.StatusOK {
		t.Fatalf("me/connections = %d %s", code, body)
	}
	var v struct {
		Linkable []identity.LinkOption `json:"linkable"`
	}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	return len(v.Linkable)
}

const goodSetup = `{"tenantId":"` + setupTenant + `","clientId":"` + setupClient + `",` +
	`"scopes":"openid https://graph.microsoft.com/Sites.ReadWrite.All offline_access","clientSecret":"shh"}`

func TestLinkingIsSetUpInTheConsoleAndWorksAtOnce(t *testing.T) {
	f := newFakeTenantFor(t, setupTenant, setupClient)
	ts, srv := setupServer(t, f)
	admin := signInClient(t, ts, "root", "rootpassword12")
	setup := ts.URL + linkMicrosoftSetupPath

	code, body := call(t, admin, http.MethodGet, setup, "")
	v := setupView(t, body)
	if code != http.StatusOK || !v.Available || v.Active || v.Source != linkSourceNone {
		t.Fatalf("before = %d %+v", code, v)
	}
	if v.RedirectURI != ts.URL+linkMicrosoftCallbackPath || !v.RedirectURIDerived {
		t.Fatalf("redirect URI = %q (derived %v)", v.RedirectURI, v.RedirectURIDerived)
	}
	if linkable(t, ts, admin) != 0 {
		t.Fatal("linking offered before it was set up")
	}
	if resp, err := admin.Get(ts.URL + linkMicrosoftStartPath); err != nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("start before setup: %v %v", err, resp)
	}

	// Ids as Entra shows them may come in either case; they are kept in the case
	// the token's tid claim uses.
	code, body = call(t, admin, http.MethodPut, setup, strings.Replace(goodSetup, setupTenant, strings.ToUpper(setupTenant), 1))
	if code != http.StatusOK {
		t.Fatalf("setup = %d %s", code, body)
	}
	if strings.Contains(body, "shh") {
		t.Fatal("the client secret came back")
	}
	v = setupView(t, body)
	if !v.Active || v.Source != linkSourceConsole || !v.ClientSecretSet || v.TenantID != setupTenant ||
		v.Scopes != "https://graph.microsoft.com/Sites.ReadWrite.All" {
		t.Fatalf("after = %+v", v)
	}
	if linkable(t, ts, admin) != 1 {
		t.Fatal("linking is set up but not offered")
	}

	// It works from the next request on, through the secret the Console stored.
	if where := link(t, ts, f, admin, admin); !strings.HasPrefix(where, linkDonePage+"?linked=con_") {
		t.Fatalf("link = %q", where)
	}
	_, body = call(t, admin, http.MethodGet, setup, "")
	if v = setupView(t, body); v.Connections != 1 || v.Stranded != 0 {
		t.Fatalf("connections = %+v", v)
	}

	// The secret is a reserved vault entry: listed nowhere, and the secrets API
	// neither writes nor deletes it.
	if got, ok, _ := srv.vault.Get(vault.LinkClientSecretName(identity.ProviderMicrosoft)); !ok || got != "shh" {
		t.Fatalf("vault holds %q %v", got, ok)
	}
	metas, _ := srv.vault.List()
	for _, m := range metas {
		if vault.IsLinkClientSecretName(m.Name) {
			t.Fatal("the vault listing shows the linking client secret")
		}
	}
	secretURL := ts.URL + "/api/v1/secrets/" + vault.LinkClientSecretName(identity.ProviderMicrosoft)
	if code, _ := call(t, admin, http.MethodPut, secretURL, `{"value":"mine"}`); code != http.StatusForbidden {
		t.Fatalf("secrets API wrote the linking secret: %d", code)
	}
	if code, _ := call(t, admin, http.MethodDelete, secretURL, ""); code != http.StatusForbidden {
		t.Fatalf("secrets API deleted the linking secret: %d", code)
	}

	// Saved again without a secret, the held one stays.
	noSecret := strings.Replace(goodSetup, `,"clientSecret":"shh"`, "", 1)
	if code, body = call(t, admin, http.MethodPut, setup, noSecret); code != http.StatusOK || !setupView(t, body).ClientSecretSet {
		t.Fatalf("resave = %d %s", code, body)
	}
	if where := link(t, ts, f, admin, admin); !strings.HasPrefix(where, linkDonePage+"?linked=con_") {
		t.Fatalf("link after resave = %q", where)
	}

	// A restart reads it back.
	srv.msLink.Store(nil)
	srv.startMicrosoftLinking()
	if l := srv.msLink.Load(); l == nil || l.cfg.TenantID != setupTenant || l.cfg.ClientSecret != "shh" {
		t.Fatalf("after a restart linking is %+v", l)
	}
}

func TestTheConsoleRefusesASetupThatCouldNotWork(t *testing.T) {
	ts, _ := setupServer(t, nil)
	admin := signInClient(t, ts, "root", "rootpassword12")
	setup := ts.URL + linkMicrosoftSetupPath
	for _, c := range []struct{ name, body, says string }{
		{"no secret on first setup", strings.Replace(goodSetup, `"shh"`, `""`, 1), "clientSecret is required"},
		{"a domain for a tenant", strings.Replace(goodSetup, setupTenant, "contoso.onmicrosoft.com", 1), "tenantId"},
		{"organizations for a tenant", strings.Replace(goodSetup, setupTenant, "organizations", 1), "tenantId"},
		{"a name for a client", strings.Replace(goodSetup, setupClient, "atlas", 1), "clientId"},
		{"only the scopes always added", strings.Replace(goodSetup, "https://graph.microsoft.com/Sites.ReadWrite.All", "profile", 1), "at least one"},
		{"a quote in a scope", strings.Replace(goodSetup, "Sites.ReadWrite.All", `Sites\"All`, 1), "is not a scope"},
		{"a line break in the secret", strings.Replace(goodSetup, `"shh"`, `"sh\nh"`, 1), "control character"},
		{"not JSON", "{", "invalid JSON"},
	} {
		code, body := call(t, admin, http.MethodPut, setup, c.body)
		if code != http.StatusBadRequest || !strings.Contains(body, c.says) {
			t.Errorf("%s: %d %s", c.name, code, body)
		}
	}
	_, body := call(t, admin, http.MethodGet, setup, "")
	if v := setupView(t, body); v.Source != linkSourceNone || v.Active {
		t.Fatalf("a refused setup was kept: %+v", v)
	}
}

func TestTheStartFlagsWin(t *testing.T) {
	f := newFakeTenant(t)
	ts, _ := linkServer(t, f)
	admin := signInClient(t, ts, "root", "rootpassword12")
	setup := ts.URL + linkMicrosoftSetupPath
	code, body := call(t, admin, http.MethodGet, setup, "")
	v := setupView(t, body)
	if code != http.StatusOK || v.Source != linkSourceFlags || !v.Active || v.TenantID != "tid-1" || !v.ClientSecretSet {
		t.Fatalf("flags = %d %+v", code, v)
	}
	if strings.Contains(body, "shh") {
		t.Fatal("the flag's client secret came back")
	}
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		if code, body := call(t, admin, method, setup, goodSetup); code != http.StatusConflict || !strings.Contains(body, "--link-microsoft") {
			t.Errorf("%s under flags = %d %s", method, code, body)
		}
	}
	// One flag is enough to decide: an incomplete set leaves linking off, and the
	// Console still cannot switch it on.
	_, half := setupServer(t, nil, WithMicrosoftLinking(MicrosoftLinkConfig{TenantID: setupTenant}))
	if half.msLink.Load() != nil || !half.msLinkStart.given() {
		t.Fatal("an incomplete set of flags turned linking on, or did not count as given")
	}
}

func TestRemovingTheSetupSwitchesLinkingOff(t *testing.T) {
	f := newFakeTenantFor(t, setupTenant, setupClient)
	ts, srv := setupServer(t, f)
	admin := signInClient(t, ts, "root", "rootpassword12")
	setup := ts.URL + linkMicrosoftSetupPath
	if code, body := call(t, admin, http.MethodPut, setup, goodSetup); code != http.StatusOK {
		t.Fatalf("setup = %d %s", code, body)
	}
	where := link(t, ts, f, admin, admin)
	conn := strings.TrimPrefix(where, linkDonePage+"?linked=")
	// The broker holds an access token for the connection, as it does after a Permit.
	if tok, _, err := srv.identity.AccessToken(context.Background(), conn); err != nil || tok == "" {
		t.Fatalf("access token under the setup: %q %v", tok, err)
	}

	// Another tenant strands the connection made in this one; it is said, not hidden.
	other := strings.Replace(goodSetup, setupTenant, otherTenant, 1)
	if code, body := call(t, admin, http.MethodPut, setup, other); code != http.StatusOK || setupView(t, body).Stranded != 1 {
		t.Fatalf("tenant change = %d %s", code, body)
	}

	code, body := call(t, admin, http.MethodDelete, setup, "")
	v := setupView(t, body)
	if code != http.StatusOK || v.Active || v.Source != linkSourceNone || v.ClientSecretSet || v.Connections != 1 || v.Stranded != 1 {
		t.Fatalf("removed = %d %+v", code, v)
	}
	if linkable(t, ts, admin) != 0 {
		t.Fatal("linking is still offered")
	}
	if resp, err := admin.Get(ts.URL + linkMicrosoftStartPath); err != nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("start after removal: %v %v", err, resp)
	}
	if _, ok, _ := srv.vault.Get(vault.LinkClientSecretName(identity.ProviderMicrosoft)); ok {
		t.Fatal("the client secret outlived the setup")
	}
	if _, err := os.Stat(srv.linkSetupFile()); !os.IsNotExist(err) {
		t.Fatalf("the setup file outlived the setup: %v", err)
	}
	if _, err := srv.refreshDelegatedToken(context.Background(), identity.Connection{ID: "con_1",
		Provider: identity.ProviderMicrosoft, TenantID: setupTenant}, "rt"); err == nil {
		t.Fatal("a connection refreshed after linking was removed")
	}
	// Nor is the token cached under the setup handed out after it: without this, a
	// removed setup would go on releasing it for up to an hour.
	if tok, _, err := srv.identity.AccessToken(context.Background(), conn); err == nil || tok != "" {
		t.Fatalf("a cached access token outlived the setup: %q %v", tok, err)
	}
	// Removing what is not there is not an error.
	if code, _ := call(t, admin, http.MethodDelete, setup, ""); code != http.StatusOK {
		t.Fatalf("second removal = %d", code)
	}
}

func TestLinkingCannotBeSetUpWithoutTheVault(t *testing.T) {
	ts, _ := setupServer(t, nil, WithoutVault())
	admin := signInClient(t, ts, "root", "rootpassword12")
	setup := ts.URL + linkMicrosoftSetupPath
	code, body := call(t, admin, http.MethodGet, setup, "")
	if v := setupView(t, body); code != http.StatusOK || v.Available || v.Active {
		t.Fatalf("without the vault = %d %+v", code, v)
	}
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		if code, _ := call(t, admin, method, setup, goodSetup); code != http.StatusServiceUnavailable {
			t.Errorf("%s without the vault = %d", method, code)
		}
	}
}

// A setup the server cannot read at start leaves linking off, and the server up.
func TestAnUnreadableSetupLeavesLinkingOff(t *testing.T) {
	ts, srv := setupServer(t, nil)
	admin := signInClient(t, ts, "root", "rootpassword12")
	if code, body := call(t, admin, http.MethodPut, ts.URL+linkMicrosoftSetupPath, goodSetup); code != http.StatusOK {
		t.Fatalf("setup = %d %s", code, body)
	}
	if err := os.WriteFile(srv.linkSetupFile(), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.msLink.Store(nil)
	srv.startMicrosoftLinking()
	if srv.msLink.Load() != nil {
		t.Fatal("an unreadable setup turned linking on")
	}
	if code, _ := call(t, admin, http.MethodGet, ts.URL+linkMicrosoftSetupPath, ""); code != http.StatusInternalServerError {
		t.Fatalf("reading an unreadable setup = %d", code)
	}
}
