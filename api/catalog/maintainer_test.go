package catalog

import (
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// The maintenance sight, apart from the portal one (MayMaintain).
//
// A catalogue is read by two different people asking two different questions.
// A customer reaches it to order from it, and mayRead answers them. Somebody
// maintaining it — or a derived surface drawing the estate behind it, like the
// starmap — is asking about the catalogue as a thing that is kept, and being the
// audience for it is no part of that answer. The two are one function apart, which
// is exactly why the difference is worth a test of its own: a surface that reached
// for mayRead would show every customer of a catalogue its products, their
// provisioning processes, and the engine around them.

// shared builds a catalogue owned by one person and shared with others.
func shared(owner string, members ...Member) Catalog {
	return Catalog{ID: "cat_1", OwnerID: owner, Members: members}
}

func member(kind, id string, role MemberRole) Member {
	return Member{Ref: PrincipalRef{Type: kind, ID: id}, Role: role}
}

func TestWhoMaintainsACatalogue(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := shared("usr_owner",
		member("user", "usr_editor", RoleEditor),
		member("user", "usr_viewer", RoleViewer),
		member("group", "grp_architects", RoleViewer),
	)
	// The audience: reached through a group the catalogue names as its own, which is
	// what makes somebody a customer of it.
	cat.Groups = []string{"grp_staff"}

	for _, tc := range []struct {
		what string
		who  *httpapi.Principal
		want bool
	}{
		{"its owner", &httpapi.Principal{UserID: "usr_owner"}, true},
		{"an editor", &httpapi.Principal{UserID: "usr_editor"}, true},
		{"a viewer it was shared with", &httpapi.Principal{UserID: "usr_viewer"}, true},
		{"somebody in a group it was shared with",
			&httpapi.Principal{UserID: "usr_x", GroupIDs: []string{"grp_architects"}}, true},
		{"an administrator", &httpapi.Principal{UserID: "usr_root", Roles: []string{"admin"}}, true},
		// The two that must answer no, and the first of them is the whole point.
		{"a customer it is offered to",
			&httpapi.Principal{UserID: "usr_staff", GroupIDs: []string{"grp_staff"}}, false},
		{"a stranger", &httpapi.Principal{UserID: "usr_nobody"}, false},
		{"nobody at all", nil, false},
	} {
		if got := s.MayMaintain(cat, tc.who); got != tc.want {
			t.Errorf("%s: MayMaintain = %v, want %v", tc.what, got, tc.want)
		}
	}

	// And the sight it is *not*: the customer who may not maintain it may still read
	// it, which is the difference this function exists to draw.
	audience := &httpapi.Principal{UserID: "usr_staff", GroupIDs: []string{"grp_staff"}}
	if !s.mayRead(cat, audience) {
		t.Errorf("the audience cannot read the catalogue it is offered — mayRead and MayMaintain have converged")
	}
}
