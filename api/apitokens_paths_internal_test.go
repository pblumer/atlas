package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
)

// apiTokensPathsStored reads the stored tokens on the loop, which owns the store.
func apiTokensPathsStored(t *testing.T, srv *Server) []apiToken {
	t.Helper()
	var (
		recs []apiToken
		err  error
	)
	srv.do(func() { recs, err = srv.apiTokenStore.LoadAll() })
	if err != nil {
		t.Fatalf("read tokens: %v", err)
	}
	return recs
}

// TestAPITokensRefusesABodyThatCannotBeRead: a mint request that broke off names no
// scope anybody chose, and no credential may come of it.
func TestAPITokensRefusesABodyThatCannotBeRead(t *testing.T) {
	srv := newServerForErrors(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/api-tokens", errReader{}))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "read body") {
		t.Fatalf("broken body: %d (%s), want 400 'read body'", rec.Code, rec.Body)
	}
	if recs := apiTokensPathsStored(t, srv); len(recs) != 0 {
		t.Errorf("tokens after a refused mint = %v, want none", recs)
	}
}

// TestAPITokensFailsWhenTheStoreCannotBeWritten: a credential that was never stored
// must not be handed out, and must not authenticate either — it would work until the
// next restart and then silently stop, on a machine nobody is watching.
func TestAPITokensFailsWhenTheStoreCannotBeWritten(t *testing.T) {
	srv := newServerForErrors(t)
	approvalsPathsDirAsFile(t, srv.apiTokenStore.Dir())

	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/api-tokens",
		`{"name":"ci","scope":"full"}`, "application/json")
	if code != http.StatusInternalServerError || !strings.Contains(string(body), "create api token") {
		t.Fatalf("unwritable store: %d (%s), want 500", code, body)
	}
	if strings.Contains(string(body), apiTokenPrefix) {
		t.Errorf("a secret was returned for a token that was never stored: %s", body)
	}
	srv.apiTokens.mu.RLock()
	n := len(srv.apiTokens.byHash)
	srv.apiTokens.mu.RUnlock()
	if n != 0 {
		t.Errorf("%d token(s) authenticate although none was stored", n)
	}

	// Listing and revoking read and write the same store, and say so rather than
	// answering "no tokens" or "revoked".
	if code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/api-tokens", "", ""); code != http.StatusInternalServerError ||
		!strings.Contains(string(body), "list api tokens") {
		t.Errorf("list: %d (%s), want 500", code, body)
	}
	if code, body := serveInternal(t, srv, http.MethodDelete, "/api/v1/api-tokens/tok-1", "", ""); code != http.StatusInternalServerError ||
		!strings.Contains(string(body), "revoke api token") {
		t.Errorf("revoke: %d (%s), want 500", code, body)
	}
}

// TestAPITokensRefusesAReachTheMinterCannotSee: a credential is never more
// privileged than the person who mints it, and a reach naming a project the minter
// cannot view would be exactly that — mint the token, then read through it.
func TestAPITokensRefusesAReachTheMinterCannotSee(t *testing.T) {
	srv := newServerWithOptions(t, WithAuth())
	var err error
	srv.do(func() {
		err = srv.projects.Save(project{ID: "anns-project", Name: "Ann's", OwnerID: "usr_ann", Visibility: VisibilityPrivate})
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/api-tokens",
		strings.NewReader(`{"name":"bob's reader","scope":"landscape","reach":["anns-project"]}`))
	req = req.WithContext(httpapi.WithPrincipal(req.Context(),
		&httpapi.Principal{UserID: "usr_bob", Username: "bob", Roles: []string{RoleModeler}}))
	rec := httptest.NewRecorder()
	srv.handleCreateAPIToken(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "a project you cannot see") {
		t.Fatalf("reach over an invisible project: %d (%s), want 400", rec.Code, rec.Body)
	}
	if recs := apiTokensPathsStored(t, srv); len(recs) != 0 {
		t.Errorf("tokens after a refused mint = %v, want none", recs)
	}
}

// TestAPITokensSurviveARestartInAStableOrder: a machine's credential must keep
// working when the server restarts, which means the index is rebuilt from disk at
// startup; and the listing an operator audits is oldest first, ties broken by id, so
// two reads of the same tokens agree.
func TestAPITokensSurviveARestartInAStableOrder(t *testing.T) {
	dir := t.TempDir()
	store, err := newAPITokenStore(filepath.Join(dir, "api-tokens"))
	if err != nil {
		t.Fatal(err)
	}
	secret := apiTokenPrefix + "survives-a-restart"
	for _, rec := range []apiToken{
		{ID: "tok-b", Name: "b", Hash: hashAPIToken(secret), Scope: apiScopeFull, CreatedAt: 200},
		{ID: "tok-a", Name: "a", Hash: hashAPIToken(apiTokenPrefix + "other"), Scope: apiScopeFull, CreatedAt: 200},
		{ID: "tok-c", Name: "c", Hash: hashAPIToken(apiTokenPrefix + "oldest"), Scope: apiScopeFull, CreatedAt: 100},
	} {
		if err := store.Save(rec); err != nil {
			t.Fatal(err)
		}
	}

	srv, err := newServerAtDir(t, dir)
	if err != nil {
		t.Fatalf("New over stored tokens: %v", err)
	}
	t.Cleanup(srv.Close)

	if got, ok := srv.apiTokens.match(secret, time.Now().Unix()); !ok || got.ID != "tok-b" {
		t.Errorf("after a restart the stored token matches %+v (%v), want tok-b", got, ok)
	}
	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/api-tokens", "", "")
	if code != http.StatusOK {
		t.Fatalf("list: %d (%s)", code, body)
	}
	var rows []apiTokenView
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	if strings.Join(ids, ",") != "tok-c,tok-a,tok-b" {
		t.Errorf("order = %v, want oldest first and ties by id", ids)
	}
}

// TestAPITokensStopTheServerWhenTheyCannotBeLoaded: a server that came up without
// the credentials it issued would refuse every machine that holds one, with nothing
// in the log to say why. An unreadable token record stops the start instead.
func TestAPITokensStopTheServerWhenTheyCannotBeLoaded(t *testing.T) {
	dir := t.TempDir()
	tokens := filepath.Join(dir, "api-tokens")
	if err := os.MkdirAll(tokens, 0o755); err != nil {
		t.Fatal(err)
	}
	corrupt(t, tokens, "tok-broken")
	srv, err := newServerAtDir(t, dir)
	if err == nil {
		srv.Close()
		t.Fatal("the server started over an unreadable token store")
	}
	if !strings.Contains(err.Error(), "apitokenstore") {
		t.Errorf("err = %v, want it to name the token store", err)
	}
}

// TestAPITokensFailWhenTheReachCannotBeChecked: whether a reach names projects the
// minter may see is read from the projects. A store that cannot be read is the
// server's fault, not something wrong with the request — a 400 would send the caller
// off to fix a body that is fine — and no credential is minted over an unchecked reach.
func TestAPITokensFailWhenTheReachCannotBeChecked(t *testing.T) {
	srv := newServerForErrors(t)
	approvalsPathsDirAsFile(t, srv.projects.Dir())

	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/api-tokens",
		`{"name":"reader","scope":"landscape","reach":["p1"]}`, "application/json")
	if code != http.StatusInternalServerError || !strings.Contains(string(body), "read projects") {
		t.Fatalf("reach over unreadable projects: %d (%s), want 500 'read projects'", code, body)
	}
	if recs := apiTokensPathsStored(t, srv); len(recs) != 0 {
		t.Errorf("tokens after a failed mint = %v, want none", recs)
	}
	srv.apiTokens.mu.RLock()
	n := len(srv.apiTokens.byHash)
	srv.apiTokens.mu.RUnlock()
	if n != 0 {
		t.Errorf("%d token(s) authenticate although none was minted", n)
	}
}
