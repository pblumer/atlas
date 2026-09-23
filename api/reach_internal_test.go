package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// ADR-0410: a peer credential carries the reach a membership cannot give it.
//
// The escalation ADR-0402 §1 describes has an address, and it is `effectiveRole`: a machine
// principal is Viewer on everything, because — in the words of the comment that has stood
// there since ADR-0129 — *"Sharing scopes cannot express it: there is no account to own or be
// a member of anything"*. That was tolerable while the route allowlist kept it to one
// read-only endpoint. A derived landscape is not that narrow.
//
// So a credential may state which projects it reaches, and that statement narrows the role it
// would otherwise hold everywhere. The narrowing is one-way on purpose: it can only take
// away, which is what makes it safe to consult from the same function every route already
// uses.

// TestAReachNarrowsAMachineCredentialToWhatItNames is the mechanism.
func TestAReachNarrowsAMachineCredentialToWhatItNames(t *testing.T) {
	agent := &httpapi.Principal{Roles: []string{RoleDeployAgent}, Reach: []string{"proj-a"}}
	named := project{ID: "proj-a", OwnerID: "someone"}
	other := project{ID: "proj-b", OwnerID: "someone"}

	if got := named.effectiveRole(agent, true); got != ScopeRoleViewer {
		t.Errorf("role in a named project = %q, want %q", got, ScopeRoleViewer)
	}
	if got := other.effectiveRole(agent, true); got != "" {
		t.Errorf("role outside the reach = %q, want none — a stated reach is a maximum", got)
	}
}

// TestAReachlessMachineCredentialKeepsWhatItShippedWith is the compatibility half, and it is
// not a courtesy. Every deploy token an operator already holds has no reach, and ADR-0129's
// allowlisted read depends on the wholesale Viewer. Making an unstated reach mean *nothing*
// everywhere would revoke credentials in the field on upgrade; ADR-0410 scopes the
// fail-closed rule to the landscape credential, which is refused at minting instead.
func TestAReachlessMachineCredentialKeepsWhatItShippedWith(t *testing.T) {
	agent := &httpapi.Principal{Roles: []string{RoleDeployAgent}}
	for _, p := range []project{{ID: "proj-a", OwnerID: "someone"}, {ID: "proj-b", OwnerID: "x"}} {
		if got := p.effectiveRole(agent, true); got != ScopeRoleViewer {
			t.Errorf("reachless agent in %s = %q, want %q", p.ID, got, ScopeRoleViewer)
		}
	}
}

// TestAReachNarrowsAPersonToo keeps the rule from having two meanings. A reach is a property
// of the credential, not of machines: a session carries none, so nothing changes for people —
// but a credential that carries one narrows whoever presents it, which is the only reading
// under which "the reach is a maximum" is true of the code and not only of the record.
func TestAReachNarrowsAPersonToo(t *testing.T) {
	owner := &httpapi.Principal{UserID: "u-1", Reach: []string{"proj-a"}}
	mine := project{ID: "proj-b", OwnerID: "u-1"}
	if got := mine.effectiveRole(owner, true); got != "" {
		t.Errorf("role = %q in an owned project outside the credential's reach, want none", got)
	}
	inReach := project{ID: "proj-a", OwnerID: "u-1"}
	if got := inReach.effectiveRole(owner, true); got != ScopeRoleOwner {
		t.Errorf("role = %q inside the reach, want the ownership the reach does not touch", got)
	}
}

// TestAnAdminIsNarrowedByACredentialsReach is the case that would otherwise be the hole. An
// admin is Owner everywhere by the branch above the reach check, so a landscape credential
// minted by an admin would reach the whole estate whatever it stated — which is exactly the
// escalation this record exists to close.
func TestAnAdminIsNarrowedByACredentialsReach(t *testing.T) {
	admin := &httpapi.Principal{UserID: "u-admin", Roles: []string{RoleAdmin}, Reach: []string{"proj-a"}}
	if got := (project{ID: "proj-b", OwnerID: "x"}).effectiveRole(admin, true); got != "" {
		t.Errorf("admin role outside the credential's reach = %q, want none", got)
	}
	if got := (project{ID: "proj-a", OwnerID: "x"}).effectiveRole(admin, true); got != ScopeRoleOwner {
		t.Errorf("admin role inside the reach = %q, want %q", got, ScopeRoleOwner)
	}
}

// TestAProtectedProjectIsStillInsideTheReachRule pins the interaction with ADR-0122: a
// protected system project is visible to every authenticated principal, and a reach that does
// not name it must still exclude it. Otherwise the platform's own processes would be the one
// thing every landscape credential could always see.
func TestAProtectedProjectIsStillInsideTheReachRule(t *testing.T) {
	cred := &httpapi.Principal{UserID: "u-1", Reach: []string{"proj-a"}}
	system := project{ID: "proj-system", Protected: true}
	if got := system.effectiveRole(cred, true); got != "" {
		t.Errorf("protected project outside the reach = %q, want none", got)
	}
}

// TestTheLandscapeScopeReachesTheLandscapeAndNothingElse is ADR-0402 §1's least-privilege
// scope: *"whose whole reach is the derived landscape, and which can neither deploy, read an
// instance, nor list a person"*.
func TestTheLandscapeScopeReachesTheLandscapeAndNothingElse(t *testing.T) {
	routes, ok := apiScopeAllowed[apiScopeLandscape]
	if !ok {
		t.Fatalf("scope %q has no route list", apiScopeLandscape)
	}
	want := map[string]bool{
		"GET /api/v1/panorama/mesh":           true,
		"GET /api/v1/panorama/mesh/archimate": true,
	}
	for _, r := range routes {
		if !want[r] {
			t.Errorf("the landscape scope reaches %q, which is not the derived landscape", r)
		}
		delete(want, r)
	}
	for r := range want {
		t.Errorf("the landscape scope does not reach %q", r)
	}
	if !validAPIScope(apiScopeLandscape) {
		t.Error("the landscape scope cannot be minted, so nobody can hold one")
	}
}

// TestTheReachLandsInTheSameVisibilityBooleanIsTheLink closes the chain ADR-0410 claims, at
// the one place it could quietly not hold. The record's whole argument is that a credential's
// reach and a person's membership end up in the *same* `CanView` boolean, so there is one
// filter and not two — and `canViewArtifact` is where that is either true or false.
func TestTheReachLandsInTheSameVisibilityBooleanIsTheLink(t *testing.T) {
	s := &Server{authEnabled: true}
	projs := map[string]project{
		"proj-a": {ID: "proj-a", OwnerID: "u-1"},
		"proj-b": {ID: "proj-b", OwnerID: "u-1"},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/panorama/mesh", nil)
	req = req.WithContext(httpapi.WithPrincipal(req.Context(),
		&httpapi.Principal{UserID: "u-1", Scope: apiScopeLandscape, Reach: []string{"proj-a"}}))

	if !s.canViewArtifact(req, "proj-a", "u-1", projs) {
		t.Error("an artifact inside the credential's reach is not visible")
	}
	if s.canViewArtifact(req, "proj-b", "u-1", projs) {
		t.Error("an artifact outside the credential's reach is visible, so the reach is not the filter")
	}
}
