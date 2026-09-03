package api

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/connector/mail"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// The rule behind worker ownership, tested where it is decided (ADR-0205), and
// the property that no test above it can prove: the runtime does not consult it.

// TestConnectorRoleIsTheProjectRuleWithOneDeparture walks every branch of the
// access rule, including the one place it deliberately differs from ADR-0071.
func TestConnectorRoleIsTheProjectRuleWithOneDeparture(t *testing.T) {
	anna := &httpapi.Principal{UserID: "usr_anna", Username: "anna", Roles: []string{"user"}}
	bert := &httpapi.Principal{UserID: "usr_bert", Username: "bert", Roles: []string{"user"}}
	admin := &httpapi.Principal{UserID: "usr_root", Username: "root", Roles: []string{RoleAdmin}}
	inHaushalt := &httpapi.Principal{UserID: "usr_bert", Username: "bert", Roles: []string{"user"}, GroupIDs: []string{"grp_haushalt"}}

	annas := connector{ID: "c1", OwnerID: "usr_anna", Visibility: VisibilityPrivate}
	shared := connector{ID: "c2", OwnerID: "usr_anna", Visibility: VisibilityShared, Members: []projectMember{
		{Ref: principalRef{Type: PrincipalTypeUser, ID: "usr_bert"}, Role: ScopeRoleViewer},
	}}
	toGroup := connector{ID: "c3", OwnerID: "usr_anna", Visibility: VisibilityShared, Members: []projectMember{
		{Ref: principalRef{Type: PrincipalTypeGroup, ID: "grp_haushalt"}, Role: ScopeRoleEditor},
	}}
	// A direct grant and a group grant on the same person: the higher one wins, so a
	// team-wide viewer grant cannot quietly demote somebody's own editor grant.
	both := connector{ID: "c4", OwnerID: "usr_anna", Visibility: VisibilityShared, Members: []projectMember{
		{Ref: principalRef{Type: PrincipalTypeUser, ID: "usr_bert"}, Role: ScopeRoleEditor},
		{Ref: principalRef{Type: PrincipalTypeGroup, ID: "grp_haushalt"}, Role: ScopeRoleViewer},
	}}
	legacy := connector{ID: "c5"} // written before ownership existed
	// Members on a private worker are inert rather than deleted: sealing a
	// worker and opening it again must not cost the owner the list.
	sealed := connector{ID: "c6", OwnerID: "usr_anna", Visibility: VisibilityPrivate, Members: []projectMember{
		{Ref: principalRef{Type: PrincipalTypeUser, ID: "usr_bert"}, Role: ScopeRoleEditor},
	}}

	for _, tc := range []struct {
		name string
		c    connector
		pr   *httpapi.Principal
		auth bool
		want string
	}{
		{"auth off makes everyone an owner", annas, nil, false, ScopeRoleOwner},
		{"auth off, even over somebody else's", annas, bert, false, ScopeRoleOwner},
		{"no principal reaches nothing", annas, nil, true, ""},
		{"the owner owns it", annas, anna, true, ScopeRoleOwner},
		{"a stranger reaches nothing", annas, bert, true, ""},
		{"an administrator is owner-equivalent", annas, admin, true, ScopeRoleOwner},
		{"a shared worker grants its member", shared, bert, true, ScopeRoleViewer},
		{"a group grant reaches its members", toGroup, inHaushalt, true, ScopeRoleEditor},
		{"a group grant reaches nobody else", toGroup, bert, true, ""},
		{"the highest grant wins", both, inHaushalt, true, ScopeRoleEditor},
		{"private ignores the member list", sealed, bert, true, ""},
		{"a legacy worker is nobody's", legacy, anna, true, ""},
		{"a legacy worker is still an administrator's", legacy, admin, true, ScopeRoleOwner},
		{"a legacy worker is open when auth is off", legacy, nil, false, ScopeRoleOwner},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := connectorRole(tc.c, tc.pr, tc.auth); got != tc.want {
				t.Errorf("= %q, want %q", got, tc.want)
			}
		})
	}
}

// TestCheckConnectorRoleHidesRatherThanForbids: a caller with no access is told the
// worker is not there, and one with too little is told they may not. The
// difference matters — 403 on a worker somebody cannot see would confirm that it
// exists, which is exactly what a private worker must not do.
func TestCheckConnectorRoleHidesRatherThanForbids(t *testing.T) {
	s := &Server{authEnabled: true}
	c := connector{ID: "c1", OwnerID: "usr_anna", Visibility: VisibilityShared, Members: []projectMember{
		{Ref: principalRef{Type: PrincipalTypeUser, ID: "usr_bert"}, Role: ScopeRoleViewer},
	}}

	req := httptest.NewRequest("GET", "/", nil)
	if code, _ := s.checkConnectorRole(req, c, ScopeRoleViewer); code != 404 {
		t.Errorf("a caller with no access got %d, want 404 — 403 would confirm the worker exists", code)
	}

	bert := httptest.NewRequest("GET", "/", nil)
	bert = bert.WithContext(httpapi.WithPrincipal(bert.Context(),
		&httpapi.Principal{UserID: "usr_bert", Roles: []string{"user"}}))
	if code, _ := s.checkConnectorRole(bert, c, ScopeRoleViewer); code != 0 {
		t.Errorf("a viewer was refused at viewer: %d", code)
	}
	if code, _ := s.checkConnectorRole(bert, c, ScopeRoleEditor); code != 403 {
		t.Errorf("a viewer got %d at editor, want 403 — they can see it, so hiding it now would be a lie", code)
	}
}

// TestTheRuntimeResolvesAConnectorNobodyIsSignedInFor is the non-regression this
// whole measure hangs on.
//
// Execution is not authoring (ADR-0071). A deployed process resolves a worker by
// name, on the run loop, with no request and no principal anywhere near it — so a
// worker owned by one person and private to them must still be *in the
// registry*, or every model referencing it would park the moment ownership landed.
//
// The test reaches for the registry rather than for an HTTP handler on purpose:
// that is the path the engine takes, and routing it through a scoped read is
// precisely the tidy-up this guards against.
func TestTheRuntimeResolvesAConnectorNobodyIsSignedInFor(t *testing.T) {
	dir := t.TempDir()
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
	// Auth on: with it off there would be no scope to ignore and the test would pass
	// vacuously.
	srv, err := New(proc, store, dir, WithAuth())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	})

	// Anna's private mail worker, on the preview provider so building its client
	// dials nothing.
	rec := connector{
		ID: "c-annas", Name: "annas-mail", Kind: connectorKindMail,
		Provider: mail.ProviderPreview, Sender: "anna@example.com", Enabled: true,
		OwnerID: "usr_anna", Visibility: VisibilityPrivate,
	}
	var saveErr error
	srv.do(func() {
		if saveErr = srv.connectors.Save(rec); saveErr != nil {
			return
		}
		saveErr = srv.rebuildConnectorRegistries()
	})
	if saveErr != nil {
		t.Fatalf("save and rebuild: %v", saveErr)
	}

	var problem string
	srv.do(func() { problem = srv.connectorProblem(connectorKindMail, rec.Name) })
	if problem != "" {
		t.Errorf("the runtime cannot use a private worker: %q — ownership reached execution, "+
			"which parks every model referencing it", problem)
	}
}
