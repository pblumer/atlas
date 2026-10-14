package api

import (
	"testing"
	"time"
)

// TestSessionRolesLive is the role counterpart to TestSessionGroupMembershipLive.
// Group ids have been pushed into live sessions since ADR-0185; roles were left
// as a login-time snapshot, so an administrative demotion did not take effect
// until the account logged out or the session expired — twelve hours by default.
// Roles decide far more than group membership does, so they cannot be the slower
// of the two.
func TestSessionRolesLive(t *testing.T) {
	ss := newSessionStore(time.Hour)
	// Alice is signed in on two devices; Bob on one. Both start as admins.
	tokA1, err := ss.create(User{ID: "usr_a", Username: "alice", Roles: []string{RoleAdmin}}, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tokA2, _ := ss.create(User{ID: "usr_a", Username: "alice", Roles: []string{RoleAdmin}}, nil)
	tokB, _ := ss.create(User{ID: "usr_b", Username: "bob", Roles: []string{RoleAdmin}}, nil)

	roles := func(tok string) []string {
		s, ok := ss.lookup(tok)
		if !ok {
			t.Fatalf("session %s gone", tok)
		}
		return s.roles
	}
	hasRole := func(tok, want string) bool {
		for _, r := range roles(tok) {
			if r == want {
				return true
			}
		}
		return false
	}

	// Demote alice: both her sessions lose admin at once; bob keeps his.
	ss.setUserRoles("usr_a", []string{RoleUser})
	if hasRole(tokA1, RoleAdmin) || hasRole(tokA2, RoleAdmin) {
		t.Fatalf("alice sessions kept admin: %v %v", roles(tokA1), roles(tokA2))
	}
	if !hasRole(tokA1, RoleUser) || !hasRole(tokA2, RoleUser) {
		t.Fatalf("alice sessions missing the new role: %v %v", roles(tokA1), roles(tokA2))
	}
	if !hasRole(tokB, RoleAdmin) {
		t.Fatalf("bob should be unaffected: %v", roles(tokB))
	}

	// Promotion travels the same path, so a grant is live too.
	ss.setUserRoles("usr_a", []string{RoleUser, RoleOperator})
	if !hasRole(tokA1, RoleOperator) {
		t.Fatalf("alice did not gain operator: %v", roles(tokA1))
	}

	// The snapshot is copied, never aliased: a caller that keeps mutating the slice
	// it passed must not be able to rewrite a live session's rights.
	mine := []string{RoleUser}
	ss.setUserRoles("usr_a", mine)
	mine[0] = RoleAdmin
	if hasRole(tokA1, RoleAdmin) {
		t.Fatalf("session aliased the caller's slice: %v", roles(tokA1))
	}

	// A user with no live session, and an unknown user, are both no-ops.
	ss.setUserRoles("usr_ghost", []string{RoleAdmin})
	if hasRole(tokA1, RoleAdmin) || hasRole(tokB, RoleUser) {
		t.Fatal("targeting an unknown user touched another session")
	}
}
