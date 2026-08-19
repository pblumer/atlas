package api

import (
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// TestScopeRankOrders checks the role ladder owner > editor > viewer > none, so
// "at least editor" comparisons in the handlers are correct (ADR-0071).
func TestScopeRankOrders(t *testing.T) {
	if !(scopeRank(ScopeRoleOwner) > scopeRank(ScopeRoleEditor) &&
		scopeRank(ScopeRoleEditor) > scopeRank(ScopeRoleViewer) &&
		scopeRank(ScopeRoleViewer) > scopeRank("")) {
		t.Fatalf("role ranks not strictly ordered: owner=%d editor=%d viewer=%d none=%d",
			scopeRank(ScopeRoleOwner), scopeRank(ScopeRoleEditor),
			scopeRank(ScopeRoleViewer), scopeRank(""))
	}
	if scopeRank("bogus") != 0 {
		t.Fatalf("unknown role should rank 0, got %d", scopeRank("bogus"))
	}
}

// TestEffectiveRole exercises every branch of the ADR-0071 access rule.
func TestEffectiveRole(t *testing.T) {
	owner := &httpapi.Principal{UserID: "usr_owner", Roles: []string{RoleUser}}
	member := &httpapi.Principal{UserID: "usr_member", Roles: []string{RoleUser}}
	stranger := &httpapi.Principal{UserID: "usr_stranger", Roles: []string{RoleUser}}
	admin := &httpapi.Principal{UserID: "usr_admin", Roles: []string{RoleAdmin}}

	memberOf := func(role string) project {
		return project{
			ID: "p1", OwnerID: "usr_owner", Visibility: VisibilityShared,
			Members: []projectMember{{Ref: principalRef{Type: PrincipalTypeUser, ID: "usr_member"}, Role: role}},
		}
	}
	privateProj := project{ID: "p1", OwnerID: "usr_owner", Visibility: VisibilityPrivate}
	// A member listed on a project that is currently private gets no access:
	// flipping visibility back to private revokes without clearing the list.
	privateWithMember := project{
		ID: "p1", OwnerID: "usr_owner", Visibility: VisibilityPrivate,
		Members: []projectMember{{Ref: principalRef{Type: PrincipalTypeUser, ID: "usr_member"}, Role: ScopeRoleEditor}},
	}
	legacy := project{ID: "p1"} // ownerless, predates scopes

	cases := []struct {
		name        string
		proj        project
		pr          *httpapi.Principal
		authEnabled bool
		want        string
	}{
		{"auth off is fully open", privateProj, nil, false, ScopeRoleOwner},
		{"auth off open even for stranger", privateProj, stranger, false, ScopeRoleOwner},
		{"admin sees everything", privateProj, admin, true, ScopeRoleOwner},
		{"owner is owner", privateProj, owner, true, ScopeRoleOwner},
		{"stranger on private gets nothing", privateProj, stranger, true, ""},
		{"nil principal under auth gets nothing", privateProj, nil, true, ""},
		{"shared viewer", memberOf(ScopeRoleViewer), member, true, ScopeRoleViewer},
		{"shared editor", memberOf(ScopeRoleEditor), member, true, ScopeRoleEditor},
		{"shared but non-member", memberOf(ScopeRoleViewer), stranger, true, ""},
		{"member ignored while private", privateWithMember, member, true, ""},
		{"legacy ownerless, non-admin", legacy, stranger, true, ""},
		{"legacy ownerless, admin", legacy, admin, true, ScopeRoleOwner},
		{"legacy ownerless, auth off", legacy, nil, false, ScopeRoleOwner},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.proj.effectiveRole(tc.pr, tc.authEnabled); got != tc.want {
				t.Fatalf("effectiveRole = %q, want %q", got, tc.want)
			}
		})
	}
}
