package api

import (
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// TestEffectiveRoleGroups checks that a shared project's group grant applies to a
// user in that group, and that when a user matches more than one grant the highest
// role wins (ADR-0180).
func TestEffectiveRoleGroups(t *testing.T) {
	inGroup := &httpapi.Principal{UserID: "usr_a", Roles: []string{RoleUser}, GroupIDs: []string{"grp_1"}}
	notInGroup := &httpapi.Principal{UserID: "usr_b", Roles: []string{RoleUser}, GroupIDs: []string{"grp_9"}}

	sharedWith := func(members ...projectMember) project {
		return project{ID: "p", OwnerID: "usr_owner", Visibility: VisibilityShared, Members: members}
	}
	grp := func(role string) projectMember {
		return projectMember{Ref: principalRef{Type: PrincipalTypeGroup, ID: "grp_1"}, Role: role}
	}
	usr := func(id, role string) projectMember {
		return projectMember{Ref: principalRef{Type: PrincipalTypeUser, ID: id}, Role: role}
	}

	cases := []struct {
		name string
		proj project
		pr   *httpapi.Principal
		want string
	}{
		{"group editor", sharedWith(grp(ScopeRoleEditor)), inGroup, ScopeRoleEditor},
		{"group viewer", sharedWith(grp(ScopeRoleViewer)), inGroup, ScopeRoleViewer},
		{"not in group gets nothing", sharedWith(grp(ScopeRoleEditor)), notInGroup, ""},
		{"highest of direct+group wins", sharedWith(usr("usr_a", ScopeRoleViewer), grp(ScopeRoleEditor)), inGroup, ScopeRoleEditor},
		{"direct wins when higher", sharedWith(usr("usr_a", ScopeRoleEditor), grp(ScopeRoleViewer)), inGroup, ScopeRoleEditor},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.proj.effectiveRole(tc.pr, true); got != tc.want {
				t.Fatalf("effectiveRole = %q, want %q", got, tc.want)
			}
		})
	}
}
