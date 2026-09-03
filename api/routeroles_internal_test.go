package api

import (
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
)

// The role inventory. These tests are to routeroles.go what the access inventory
// is to access.go: they turn "not everybody may deploy" from a claim into
// something a reviewer checks by reading one list and re-running one command.

// wantAdminRoutes is the complete set of routes only an administrator reaches. It
// is written out here rather than derived, for the reason wantPublicRoutes is:
// narrowing a route to admin, or — the dangerous direction — widening one out of
// admin, is then a diff to this list that somebody has to read.
//
// It is also the upgrade contract. These 51 were admin-gated inside their handlers
// before roles existed, so an account that keeps modeler, operator and user on
// upgrade can still do exactly what it could yesterday: everything except these.
var wantAdminRoutes = []string{
	// The instance itself: its log, its data, its recovery, and the identity it
	// presents to other servers (ADR-0189 §6). Reading that identity is open to any
	// signed-in caller — correlation needs it — but naming this node is an operator
	// act, because the name is what an architect binds a model element to.
	"PUT /api/v1/node",
	"GET /api/v1/logs",
	"GET /api/v1/backup",
	"POST /api/v1/restore",
	"GET /api/v1/backup/full",
	"POST /api/v1/restore/full",
	"GET /api/v1/checkpoints",
	"POST /api/v1/checkpoints",

	// Code execution outside a deployment, and the overrides that decide which
	// process a call activity reaches on this server.
	"POST /api/v1/scripts/run",
	"PUT /api/v1/call-activities/overrides/{processId}",
	"DELETE /api/v1/call-activities/overrides/{processId}",

	// Reaching into a running instance's data, or moving instances between
	// definitions. An operator may cancel and repair; rewriting a variable or
	// migrating a population is a heavier act and stays where it was.
	"POST /api/v1/instances/{key}/variables",
	"POST /api/v1/instances/{key}/migrate/plan",
	"POST /api/v1/instances/{key}/migrate",
	"POST /api/v1/processes/{key}/migrate-instances",

	// What a worker did, in full: job payloads carry whatever the process carries.
	"GET /api/v1/workers/{id}/history",
	"GET /api/v1/workers/{id}/jobs",
	// And what a mock AD worker holds. Invented entries, but shaped like a staff
	// list — no more public than the seed file they started from, which is admin-only
	// for the same reason (ADR-0202).
	"GET /api/v1/ad/mock-directory",
	// And what a mock SQL worker was asked. Invented answers, but the *values a
	// process bound* travel with them, and nothing can tell a password on its way into
	// a table from an id — a stronger reason than the directory's, for the same gate.
	"GET /api/v1/sql/mock-journal",

	// Credentials, in every shape Atlas has one.
	"POST /api/v1/targets",
	"DELETE /api/v1/targets/{id}",
	"POST /api/v1/deploy-tokens",
	"GET /api/v1/deploy-tokens",
	"DELETE /api/v1/deploy-tokens/{id}",
	"POST /api/v1/api-tokens",
	"GET /api/v1/api-tokens",
	"DELETE /api/v1/api-tokens/{id}",
	"POST /api/v1/oauth-clients",
	"GET /api/v1/oauth-clients",
	"DELETE /api/v1/oauth-clients/{id}",
	"POST /api/v1/connectors/{id}/provision-clio-key",
	"GET /api/v1/secrets",
	"PUT /api/v1/secrets/{name}",
	"DELETE /api/v1/secrets/{name}",

	// Org-wide settings. Every one of them is read by somebody who is not an
	// administrator — the login screen reads three before anyone is anybody — so it
	// is the write that is listed here and the read that is not.
	"PUT /api/v1/settings/theme",
	"DELETE /api/v1/settings/theme",
	"PUT /api/v1/settings/logo",
	"DELETE /api/v1/settings/logo",
	"PUT /api/v1/settings/ad-mock",
	// Turning the database mockup on makes every SQL task stop reaching a database, and
	// turning it off makes them start again — the same authority the AD switch above
	// carries, over the workers whose credential is the most valuable one Atlas holds.
	"PUT /api/v1/settings/sql-mock",
	"PUT /api/v1/settings/registration",
	"DELETE /api/v1/settings/registration",

	// The claim mapping is admin-only in both directions, unlike the three above:
	// its rules name the provider's group identifiers, and nothing on the login
	// screen needs them.
	"GET /api/v1/settings/oidc-mapping",
	"PUT /api/v1/settings/oidc-mapping",

	// Who exists, what they may do, and the record of what they did.
	"GET /api/v1/users",
	// Who is signed in this minute. Same list, same reach: presence says where a
	// person is, and only the people who administer accounts see it
	// (ADR-0228).
	"GET /api/v1/users/presence",
	"POST /api/v1/users",
	"GET /api/v1/users/{id}",
	"PATCH /api/v1/users/{id}",
	"POST /api/v1/users/{id}/password",
	"DELETE /api/v1/users/{id}",
	"GET /api/v1/groups",
	"POST /api/v1/groups",
	"PATCH /api/v1/groups/{id}",
	"DELETE /api/v1/groups/{id}",
	"PUT /api/v1/groups/{id}/members/{userId}",
	"DELETE /api/v1/groups/{id}/members/{userId}",
	"GET /api/v1/audit",
}

// TestEveryRouteDeclaresAKnownRole is the fail-closed half stated as a build
// failure rather than as a 403 somebody meets in production. A new route that
// names no role reaches nobody — correct, and useless — so it has to be caught
// here, where the author is still looking at it.
func TestEveryRouteDeclaresAKnownRole(t *testing.T) {
	for _, r := range accessTestServer(t).apiRoutes() {
		route := r.method + " " + r.pattern
		switch {
		case r.op.role == "":
			t.Errorf("%s declares no role — it would reach nobody; add one to its apiOp", route)
		case !isRouteRole(r.op.role):
			t.Errorf("%s declares role %q, which is not one of %v", route, r.op.role, routeRoles)
		}
	}
}

// TestEveryMountedPatternDeclaresARole covers what the route table cannot: the
// routes mounted beside it — the MCP transport, the metrics exposition, the share
// links, the UI. They are declared at their own mount sites, so they need their
// own inventory or they are exactly where the next hole opens.
func TestEveryMountedPatternDeclaresARole(t *testing.T) {
	_, policy := accessTestServer(t).mountRoutes()
	for pattern, role := range policy.declaredRoles() {
		switch {
		case role == "":
			t.Errorf("mounted pattern %q declares no role", pattern)
		case !isRouteRole(role):
			t.Errorf("mounted pattern %q declares role %q, which is not one of %v", pattern, role, routeRoles)
		}
	}
}

// TestAdminRoutesAreExactlyTheAllowlist is the inventory guard. A route that
// becomes admin-only, or stops being, shows up here as a failing test naming the
// pattern.
func TestAdminRoutesAreExactlyTheAllowlist(t *testing.T) {
	got := []string{}
	for _, r := range accessTestServer(t).apiRoutes() {
		if r.op.role == RoleAdmin {
			got = append(got, r.method+" "+r.pattern)
		}
	}
	sort.Strings(got)
	want := append([]string(nil), wantAdminRoutes...)
	sort.Strings(want)
	if strings.Join(got, "\n") == strings.Join(want, "\n") {
		return
	}
	inWant := map[string]bool{}
	for _, p := range want {
		inWant[p] = true
	}
	inGot := map[string]bool{}
	for _, p := range got {
		inGot[p] = true
	}
	for _, p := range got {
		if !inWant[p] {
			t.Errorf("route %q is admin-only and not on the allowlist — if that is intended, add it to wantAdminRoutes", p)
		}
	}
	for _, p := range want {
		if !inGot[p] {
			t.Errorf("allowlisted admin route %q no longer requires admin — a widening; say why in the record before removing it here", p)
		}
	}
}

// TestPublicRoutesNameNoRole holds the two layers to the same story. A route
// served without a principal cannot sensibly demand one hold a role: an anonymous
// caller would sail through while a signed-in one was refused, which is a rule
// backwards.
func TestPublicRoutesNameNoRole(t *testing.T) {
	_, policy := accessTestServer(t).mountRoutes()
	roles := policy.declaredRoles()
	for _, pattern := range policy.publicPatterns() {
		if roles[pattern] != roleAny {
			t.Errorf("public route %q requires role %q; a public route must name roleAny", pattern, roles[pattern])
		}
	}
}

// TestMayReach pins the rule itself, including the two edges that decide whether
// this is a gate or a decoration.
func TestMayReach(t *testing.T) {
	user := &httpapi.Principal{UserID: "usr_1", Roles: []string{RoleUser}}
	modeler := &httpapi.Principal{UserID: "usr_2", Roles: []string{RoleModeler, RoleUser}}
	admin := &httpapi.Principal{UserID: "usr_3", Roles: []string{RoleAdmin}}

	cases := []struct {
		name     string
		p        *httpapi.Principal
		required string
		want     bool
	}{
		{"an undeclared route reaches nobody", user, "", false},
		{"not even an administrator reaches an undeclared route", admin, "", false},
		{"no principal is nobody to ask", nil, RoleAdmin, true},
		{"any signed-in identity", user, roleAny, true},
		{"the role itself", modeler, RoleModeler, true},
		{"a role not held", user, RoleModeler, false},
		{"an administrator reaches what modellers reach", admin, RoleModeler, true},
		{"a modeller is not an operator", modeler, RoleOperator, false},
		{"holding several", modeler, RoleUser, true},
	}
	for _, tc := range cases {
		if got := mayReach(tc.p, tc.required); got != tc.want {
			t.Errorf("%s: mayReach = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestKnownRoles keeps the two vocabularies apart: roleAny describes a route and
// is never granted to anybody, and a role somebody invented is neither.
func TestKnownRoles(t *testing.T) {
	if !isRouteRole(roleAny) || !isRouteRole(RoleModeler) {
		t.Error("roleAny and modeler must both be roles a route may name")
	}
	if isRouteRole("moduler") {
		t.Error("a typo passed as a route role")
	}
	if isGrantableRole(roleAny) {
		t.Error("roleAny is grantable — it describes a route, not a person")
	}
	if !isGrantableRole(RoleOperator) || isGrantableRole("wizard") {
		t.Error("grantable roles are exactly the four")
	}
}

// TestUnenforcedRolesAreNamed. A role Atlas does not enforce is allowed — the field
// is free-form (ADR-0044) — but it reaches nothing, and a typo looks exactly like a
// deliberate one. The write says so where somebody is already looking.
func TestUnenforcedRolesAreNamed(t *testing.T) {
	if got := unenforcedRoles([]string{RoleAdmin, "modeller", RoleUser, "reporting"}); strings.Join(got, " ") != "modeller reporting" {
		t.Errorf("unenforcedRoles = %v, want the two Atlas does not enforce", got)
	}
	if got := unenforcedAttr([]string{RoleModeler, RoleUser}); got != nil {
		t.Errorf("unenforcedAttr on ordinary roles = %v, want no attribute at all", got)
	}
	if got := unenforcedAttr([]string{"wizard"}); len(got) != 1 || got[0].Key != "unenforced_roles" {
		t.Errorf("unenforcedAttr = %v, want one attribute naming the roles", got)
	}
}

// TestRoleRefusalSaysWhatIsMissing. A 403 that names nothing sends the person to
// an administrator with nothing to ask for.
func TestRoleRefusalSaysWhatIsMissing(t *testing.T) {
	rec := httptest.NewRecorder()
	refuseRole(rec, httptest.NewRequest("POST", "/api/v1/deployments", nil), RoleModeler)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "modeler") ||
		!strings.Contains(body, "/api/v1/deployments") {
		t.Errorf("refusal = %s, want it to name the role and the route", body)
	}

	// And the case that must never ship: a route nobody annotated. It reaches
	// nobody, and says so rather than naming a role that is not there.
	rec = httptest.NewRecorder()
	refuseRole(rec, httptest.NewRequest("GET", "/api/v1/nowhere", nil), "")
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if body := rec.Body.String(); !strings.Contains(body, "no role is declared") {
		t.Errorf("refusal = %s, want it to say the route declares no role", body)
	}
}

// TestTokenRolesNeverIncludeAdmin is the property that keeps a leaked machine
// credential from being the worst kind of leak.
func TestTokenRolesNeverIncludeAdmin(t *testing.T) {
	cases := []struct {
		name string
		p    *httpapi.Principal
		want string
	}{
		{"an administrator's token gets the whole non-admin set",
			&httpapi.Principal{UserID: "usr_1", Roles: []string{RoleAdmin}}, "modeler operator user"},
		{"auth off has nobody to be narrower than", nil, "modeler operator user"},
		{"a modeller's token models and nothing else",
			&httpapi.Principal{UserID: "usr_2", Roles: []string{RoleModeler, RoleUser}}, "modeler user"},
		{"an account with a role nobody enforces mints a token that reaches nothing",
			&httpapi.Principal{UserID: "usr_3", Roles: []string{"wizard"}}, ""},
	}
	for _, tc := range cases {
		if got := strings.Join(tokenRoles(tc.p), " "); got != tc.want {
			t.Errorf("%s: tokenRoles = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestATokenWrittenBeforeRolesKeepsItsReach. The record's empty Roles reads as the
// legacy set, exactly as its empty Scope reads as apiScopeFull: a worker minted
// last year must not go inert on upgrade.
func TestATokenWrittenBeforeRolesKeepsItsReach(t *testing.T) {
	if got := strings.Join(apiToken{ID: "tok_1"}.roles(), " "); got != "modeler operator user" {
		t.Errorf("a token with no recorded roles = %q, want the legacy set", got)
	}
	explicit := apiToken{ID: "tok_2", Roles: []string{RoleUser}}
	if got := strings.Join(explicit.roles(), " "); got != RoleUser {
		t.Errorf("a token with recorded roles = %q, want them kept", got)
	}
}

// TestWithLegacyRoles: the upgrade adds, and takes nothing away — not the admin
// role, and not a role somebody invented for their own reporting.
func TestWithLegacyRoles(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, "modeler operator user"},
		{[]string{RoleUser}, "user modeler operator"},
		{[]string{RoleAdmin}, "admin modeler operator user"},
		{[]string{"wizard", RoleOperator}, "wizard operator modeler user"},
		{[]string{RoleModeler, RoleOperator, RoleUser}, "modeler operator user"},
	}
	for _, tc := range cases {
		if got := strings.Join(withLegacyRoles(tc.in), " "); got != tc.want {
			t.Errorf("withLegacyRoles(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// upgradeStores builds the two stores upgradeLegacyRoles touches, over a directory
// the test owns. It is a partial Server on purpose: the upgrade runs on the
// constructing goroutine before there is a run loop, so it needs nothing else.
func upgradeStores(t *testing.T, dir string) *Server {
	t.Helper()
	users, err := newUserStore(filepath.Join(dir, "users"))
	if err != nil {
		t.Fatalf("newUserStore: %v", err)
	}
	grants, err := newOAuthGrantStore(filepath.Join(dir, "oauth-grants"))
	if err != nil {
		t.Fatalf("newOAuthGrantStore: %v", err)
	}
	return &Server{users: users, oauthGrantStore: grants, oauthGrants: newOAuthGrantIndex()}
}

// corrupt writes a file the store will try to read and fail on: a valid record
// name, holding something that is not a record.
func corrupt(t *testing.T, dir, id string) {
	t.Helper()
	name := hex.EncodeToString([]byte(id)) + ".json"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("{"), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestUpgradeLegacyRolesStopsOnAnUnreadableStore. It runs before the server serves
// anything, so a failure has to stop the start rather than be swallowed: an
// instance that came up having silently skipped the upgrade would be one where
// every account had quietly lost what it could do.
func TestUpgradeLegacyRolesStopsOnAnUnreadableStore(t *testing.T) {
	t.Run("accounts", func(t *testing.T) {
		dir := t.TempDir()
		s := upgradeStores(t, dir)
		corrupt(t, filepath.Join(dir, "users"), "usr_broken")
		if err := s.upgradeLegacyRoles(100); err == nil {
			t.Error("an unreadable user store did not stop the upgrade")
		}
	})

	t.Run("grants", func(t *testing.T) {
		dir := t.TempDir()
		s := upgradeStores(t, dir)
		if err := s.users.Save(User{ID: "usr_1", Username: "olli", Roles: []string{RoleUser}}); err != nil {
			t.Fatalf("save user: %v", err)
		}
		corrupt(t, filepath.Join(dir, "oauth-grants"), "grant_broken")
		if err := s.upgradeLegacyRoles(100); err == nil {
			t.Error("an unreadable grant store did not stop the upgrade")
		}
	})
}

// TestUpgradeLegacyRolesRunsOnce is the marker doing its job, stated at the level
// the marker lives at: a second pass over an already-upgraded account changes
// nothing, whatever an operator did to it in between.
func TestUpgradeLegacyRolesRunsOnce(t *testing.T) {
	dir := t.TempDir()
	s := upgradeStores(t, dir)
	if err := s.users.Save(User{ID: "usr_1", Username: "olli", Roles: []string{RoleUser}}); err != nil {
		t.Fatalf("save user: %v", err)
	}
	if err := s.upgradeLegacyRoles(100); err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	u, _, err := s.users.Get("usr_1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got := strings.Join(u.Roles, " "); got != "user modeler operator" {
		t.Fatalf("after the upgrade = %q", got)
	}
	if u.RolesUpgradedAt != 100 {
		t.Errorf("marker = %d, want the time of the upgrade", u.RolesUpgradedAt)
	}

	u.Roles = []string{RoleUser}
	if err := s.users.Save(u); err != nil {
		t.Fatalf("save narrowed: %v", err)
	}
	if err := s.upgradeLegacyRoles(200); err != nil {
		t.Fatalf("second upgrade: %v", err)
	}
	again, _, err := s.users.Get("usr_1")
	if err != nil {
		t.Fatalf("get again: %v", err)
	}
	if got := strings.Join(again.Roles, " "); got != RoleUser {
		t.Errorf("a second pass widened a narrowed account back to %q", got)
	}
}

// TestTheUpgradeReachesAStandingApproval. A grant carries its own snapshot of what
// the person could do (ADR-0200), so an upgrade that touched only the account would
// leave somebody's connector able to do less than they can, for a change they never
// made.
func TestTheUpgradeReachesAStandingApproval(t *testing.T) {
	dir := t.TempDir()
	s := upgradeStores(t, dir)
	if err := s.users.Save(User{ID: "usr_1", Username: "olli", Roles: []string{RoleUser}}); err != nil {
		t.Fatalf("save user: %v", err)
	}
	mine := oauthGrant{ID: "grant_1", UserID: "usr_1", Username: "olli", Roles: []string{RoleUser}}
	somebodyElses := oauthGrant{ID: "grant_2", UserID: "usr_2", Username: "vera", Roles: []string{RoleUser}}
	for _, g := range []oauthGrant{mine, somebodyElses} {
		if err := s.oauthGrantStore.Save(g); err != nil {
			t.Fatalf("save grant: %v", err)
		}
	}

	if err := s.upgradeLegacyRoles(100); err != nil {
		t.Fatalf("upgrade: %v", err)
	}

	got, _, err := s.oauthGrantStore.Get("grant_1")
	if err != nil {
		t.Fatalf("get grant: %v", err)
	}
	if want := strings.Join(withLegacyRoles([]string{RoleUser}), " "); strings.Join(got.Roles, " ") != want {
		t.Errorf("the grant carries %v, want the account's upgraded roles %q", got.Roles, want)
	}
	// And only that person's. A grant belonging to an account this pass did not touch
	// is left exactly as it was.
	other, _, err := s.oauthGrantStore.Get("grant_2")
	if err != nil {
		t.Fatalf("get other grant: %v", err)
	}
	if strings.Join(other.Roles, " ") != RoleUser {
		t.Errorf("somebody else's grant was rewritten to %v", other.Roles)
	}
}

// boundaryStatus runs one request through the real route policy and the real
// middleware as somebody holding these roles, and answers with what the boundary
// did: 403 when it refused, and 418 when it let the request through to what would
// have been the handler.
//
// Nothing behind it runs, which is the point — a role refusal is the boundary's
// answer, and a test that reached a handler to find out would be testing the wrong
// layer. It is what tests elsewhere in this package use to say "this route needs
// that role" without standing up an engine.
func boundaryStatus(t *testing.T, roles []string, method, path string) int {
	t.Helper()
	s := accessTestServer(t)
	s.authEnabled = true
	s.sessions = newSessionStore(time.Hour)
	tok, err := s.sessions.create(User{ID: "usr_1", Username: "somebody", Roles: roles}, nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, policy := s.mountRoutes()
	h := s.withAuth(policy, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	req := httptest.NewRequest(method, path, nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code
}

// TestTheBoundaryIsWhereARoleIsDecided. The check runs before anything else this
// package does: no handler is reached, so no handler needs its own copy of the
// rule — which is why the 51 requireAdmin calls that used to open these handlers
// are gone.
func TestTheBoundaryIsWhereARoleIsDecided(t *testing.T) {
	const letThrough = http.StatusTeapot
	if got := boundaryStatus(t, []string{RoleModeler, RoleOperator, RoleUser}, "GET", "/api/v1/backup"); got != http.StatusForbidden {
		t.Errorf("a non-admin reading the backup = %d, want 403 before the handler runs", got)
	}
	if got := boundaryStatus(t, []string{RoleAdmin}, "GET", "/api/v1/backup"); got != letThrough {
		t.Errorf("an administrator reading the backup = %d, want the request to reach the handler", got)
	}
}
