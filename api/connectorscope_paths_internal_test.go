package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// connectorScopePathsSeed files worker records directly, on the loop.
func connectorScopePathsSeed(t *testing.T, srv *Server, recs ...connector) {
	t.Helper()
	var err error
	srv.do(func() {
		for _, c := range recs {
			if err = srv.connectors.Save(c); err != nil {
				return
			}
		}
	})
	if err != nil {
		t.Fatalf("seed workers: %v", err)
	}
}

// connectorScopePathsGet reads one worker back, on the loop.
func connectorScopePathsGet(t *testing.T, srv *Server, id string) connector {
	t.Helper()
	var (
		c   connector
		ok  bool
		err error
	)
	srv.do(func() { c, ok, err = srv.connectors.Get(id) })
	if err != nil || !ok {
		t.Fatalf("read worker %s: ok=%v err=%v", id, ok, err)
	}
	return c
}

// connectorScopePathsBlockWrites leaves the record readable but makes its next
// write fail: a directory sits where the store writes its temporary file. That is
// the shape of a write failing after a good read, on every platform and as any user.
func connectorScopePathsBlockWrites(t *testing.T, srv *Server, id string) {
	t.Helper()
	if err := os.MkdirAll(srv.connectors.FileFor(id)+".tmp", 0o755); err != nil {
		t.Fatal(err)
	}
}

// connectorScopePathsCall drives one sharing route through the mux.
func connectorScopePathsCall(t *testing.T, srv *Server, method, path, body string) (int, string) {
	t.Helper()
	code, b := serveInternal(t, srv, method, path, body, "application/json")
	return code, string(b)
}

// TestConnectorScopeRefusesABodyThatCannotBeRead: a share or a visibility change
// whose body broke off names no role and no visibility; neither is guessed.
func TestConnectorScopeRefusesABodyThatCannotBeRead(t *testing.T) {
	srv := newServerForErrors(t)
	connectorScopePathsSeed(t, srv, connector{ID: "w-1", Name: "db", Kind: connectorKindPostgres})
	before := connectorScopePathsGet(t, srv, "w-1")

	for _, path := range []string{"/api/v1/connectors/w-1/members/usr_bob", "/api/v1/connectors/w-1/visibility"} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPut, path, errReader{}))
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "read body") {
			t.Errorf("PUT %s with a broken body: %d (%s), want 400", path, rec.Code, rec.Body)
		}
	}
	if after := connectorScopePathsGet(t, srv, "w-1"); !reflect.DeepEqual(before, after) {
		t.Errorf("a refused change altered the worker: %+v -> %+v", before, after)
	}
}

// TestConnectorScopeReportsAChangeItCouldNotStore: every sharing change is a write,
// and one that did not land must not answer with the record as if it had — the
// owner would believe a colleague's access was withdrawn, or a worker sealed, while
// it was not.
func TestConnectorScopeReportsAChangeItCouldNotStore(t *testing.T) {
	srv := newServerForErrors(t)
	connectorScopePathsSeed(t, srv, connector{ID: "w-1", Name: "db", Kind: connectorKindPostgres,
		OwnerID: "usr_ann", Visibility: VisibilityShared,
		Members: []projectMember{{Ref: principalRef{Type: PrincipalTypeUser, ID: "usr_bob"}, Role: ScopeRoleEditor}}})
	before := connectorScopePathsGet(t, srv, "w-1")
	connectorScopePathsBlockWrites(t, srv, "w-1")

	for _, tc := range []struct{ method, path, body, says string }{
		{http.MethodPut, "/api/v1/connectors/w-1/members/usr_cleo", `{"role":"viewer"}`, "share worker"},
		{http.MethodDelete, "/api/v1/connectors/w-1/members/usr_bob", "", "unshare worker"},
		{http.MethodPut, "/api/v1/connectors/w-1/visibility", `{"visibility":"private"}`, "set worker visibility"},
		{http.MethodPut, "/api/v1/connectors/w-1/owner/usr_cleo", "", "transfer worker"},
	} {
		code, body := connectorScopePathsCall(t, srv, tc.method, tc.path, tc.body)
		if code != http.StatusInternalServerError || !strings.Contains(body, tc.says) {
			t.Errorf("%s %s: %d (%s), want 500 %q", tc.method, tc.path, code, body, tc.says)
		}
	}
	if after := connectorScopePathsGet(t, srv, "w-1"); !reflect.DeepEqual(before, after) {
		t.Errorf("a failed write still changed the worker: %+v -> %+v", before, after)
	}
}

// TestConnectorScopeFailsWhenTheDirectoryCannotBeRead: sharing with, or handing a
// worker to, somebody who does not exist must be refused — and "cannot tell" is not
// "exists". With the account store unreadable both stop before writing.
func TestConnectorScopeFailsWhenTheDirectoryCannotBeRead(t *testing.T) {
	srv := newServerWithOptions(t, WithAuth())
	connectorScopePathsSeed(t, srv, connector{ID: "w-1", Name: "db", Kind: connectorKindPostgres, OwnerID: "usr_ann"})
	before := connectorScopePathsGet(t, srv, "w-1")
	approvalsPathsDirAsFile(t, srv.users.Dir())
	admin := &httpapi.Principal{UserID: "usr_root", Username: "root", Roles: []string{RoleAdmin}}

	for name, tc := range map[string]struct {
		call func(http.ResponseWriter, *http.Request)
		body string
		says string
	}{
		"share":    {srv.handleSetConnectorMember, `{"role":"viewer"}`, "look up member"},
		"transfer": {srv.handleTransferConnector, "", "look up user"},
	} {
		req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(tc.body))
		req.SetPathValue("id", "w-1")
		req.SetPathValue("principalId", "usr_bob")
		req.SetPathValue("userId", "usr_bob")
		req = req.WithContext(httpapi.WithPrincipal(req.Context(), admin))
		rec := httptest.NewRecorder()
		tc.call(rec, req)
		if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), tc.says) {
			t.Errorf("%s: %d (%s), want 500 %q", name, rec.Code, rec.Body, tc.says)
		}
	}
	if after := connectorScopePathsGet(t, srv, "w-1"); !reflect.DeepEqual(before, after) {
		t.Errorf("the worker changed although the recipient could not be checked: %+v -> %+v", before, after)
	}
}

// connectorScopePathsCheck posts a SQL worker check naming a stored credential.
func connectorScopePathsCheck(srv *Server, p *httpapi.Principal, ref string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/connectors/test",
		strings.NewReader(`{"kind":"postgres","credentialsRef":"`+ref+`"}`))
	req = req.WithContext(httpapi.WithPrincipal(req.Context(), p))
	rec := httptest.NewRecorder()
	srv.handleTestConnector(rec, req)
	return rec
}

// TestConnectorScopeLetsACheckBorrowOnlyACredentialTheCallerMayEdit: the check dials
// whatever the named credential holds, so naming one is allowed only to somebody who
// may already edit a worker that uses it — or to an administrator. Anybody else is
// refused rather than checked without it, which would report a failure about a
// permission as a failure about the database.
func TestConnectorScopeLetsACheckBorrowOnlyACredentialTheCallerMayEdit(t *testing.T) {
	srv := newServerWithOptions(t, WithAuth())
	connectorScopePathsSeed(t, srv,
		connector{ID: "w-ann", Name: "anns-db", Kind: connectorKindPostgres, CredentialsRef: "dsn-ann",
			OwnerID: "usr_ann", Visibility: VisibilityPrivate, CreatedAt: 1},
		connector{ID: "w-bob", Name: "bobs-db", Kind: connectorKindPostgres, CredentialsRef: "dsn-bob",
			OwnerID: "usr_bob", Visibility: VisibilityPrivate, CreatedAt: 2})
	bob := &httpapi.Principal{UserID: "usr_bob", Username: "bob", Roles: []string{RoleModeler}}
	admin := &httpapi.Principal{UserID: "usr_root", Username: "root", Roles: []string{RoleAdmin}}

	// Bob's own credential, and an administrator naming Ann's: both are let through
	// to the vault, which holds nothing under either, and the answer says that.
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"bob, his own":    connectorScopePathsCheck(srv, bob, "dsn-bob"),
		"admin, anyone's": connectorScopePathsCheck(srv, admin, "dsn-ann"),
	} {
		var got struct {
			OK     bool   `json:"ok"`
			Detail string `json:"detail"`
		}
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil ||
			got.OK || !strings.Contains(got.Detail, "holds no connection string") {
			t.Errorf("%s: %d (%s), want 200 saying the vault holds nothing", name, rec.Code, rec.Body)
		}
	}

	// Bob naming Ann's credential is refused outright.
	if rec := connectorScopePathsCheck(srv, bob, "dsn-ann"); rec.Code != http.StatusForbidden ||
		!strings.Contains(rec.Body.String(), "a worker you may not edit") {
		t.Errorf("bob, ann's: %d (%s), want 403", rec.Code, rec.Body)
	}

	// And with the worker store unreadable nobody can say whose a credential is.
	approvalsPathsDirAsFile(t, srv.connectors.Dir())
	if rec := connectorScopePathsCheck(srv, bob, "dsn-bob"); rec.Code != http.StatusInternalServerError ||
		!strings.Contains(rec.Body.String(), "read workers") {
		t.Errorf("unreadable workers: %d (%s), want 500", rec.Code, rec.Body)
	}
}
