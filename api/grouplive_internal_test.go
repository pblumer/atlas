package api

import (
	"testing"
	"time"
)

func hasGroup(ids []string, g string) bool {
	n := 0
	for _, id := range ids {
		if id == g {
			n++
		}
	}
	return n == 1
}

// TestSessionGroupMembershipLive covers the in-memory session pushes that back
// ADR-draft-live-group-membership: adding and removing a group id across a user's
// live sessions, idempotency, isolation between users, and dropping a group from
// everyone on delete.
func TestSessionGroupMembershipLive(t *testing.T) {
	ss := newSessionStore(time.Hour)
	// Alice is signed in on two devices; Bob on one. All start in grp_x.
	tokA1, err := ss.create(User{ID: "usr_a", Username: "alice"}, []string{"grp_x"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	tokA2, _ := ss.create(User{ID: "usr_a", Username: "alice"}, []string{"grp_x"})
	tokB, _ := ss.create(User{ID: "usr_b", Username: "bob"}, []string{"grp_x"})

	groups := func(tok string) []string {
		s, ok := ss.lookup(tok)
		if !ok {
			t.Fatalf("session %s gone", tok)
		}
		return s.groupIDs
	}

	// Add alice to grp_y: both her sessions gain it; bob is untouched.
	ss.setUserGroupMembership("usr_a", "grp_y", true)
	if !hasGroup(groups(tokA1), "grp_y") || !hasGroup(groups(tokA2), "grp_y") {
		t.Fatalf("alice sessions should gain grp_y: %v %v", groups(tokA1), groups(tokA2))
	}
	if hasGroup(groups(tokB), "grp_y") {
		t.Fatalf("bob should be unaffected: %v", groups(tokB))
	}
	// Idempotent add: still exactly one grp_y.
	ss.setUserGroupMembership("usr_a", "grp_y", true)
	if !hasGroup(groups(tokA1), "grp_y") {
		t.Fatalf("idempotent add duplicated or dropped grp_y: %v", groups(tokA1))
	}

	// Remove alice from grp_x: gone from her sessions, still present for bob.
	ss.setUserGroupMembership("usr_a", "grp_x", false)
	if hasGroup(groups(tokA1), "grp_x") || hasGroup(groups(tokA2), "grp_x") {
		t.Fatalf("alice should lose grp_x: %v %v", groups(tokA1), groups(tokA2))
	}
	if !hasGroup(groups(tokB), "grp_x") {
		t.Fatalf("bob should still have grp_x: %v", groups(tokB))
	}
	// Removing a group the user isn't in is a no-op.
	before := len(groups(tokA1))
	ss.setUserGroupMembership("usr_a", "grp_absent", false)
	if len(groups(tokA1)) != before {
		t.Fatalf("no-op remove changed the snapshot: %v", groups(tokA1))
	}
	// Targeting an unknown user touches nothing.
	ss.setUserGroupMembership("usr_ghost", "grp_y", true)

	// Delete grp_x: dropped from everyone, so bob loses it too.
	ss.dropGroupFromSessions("grp_x")
	if hasGroup(groups(tokB), "grp_x") {
		t.Fatalf("bob should lose grp_x after group delete: %v", groups(tokB))
	}
	// Dropping an absent group is a no-op.
	before = len(groups(tokA1))
	ss.dropGroupFromSessions("grp_absent")
	if len(groups(tokA1)) != before {
		t.Fatalf("no-op drop changed the snapshot: %v", groups(tokA1))
	}
}
