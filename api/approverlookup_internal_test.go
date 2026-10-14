package api

import (
	"testing"
)

// The half of the approver report that knows about people.
//
// The catalogue asks two questions and this answers them from the user and group
// stores. What makes it correct is not that it is reasonable but that it matches
// [Server.holdsTask], which is what actually decides — at the moment an approval
// lands — whether the person reading it is allowed to. A report stricter than that
// names working rules as broken; a report looser than it calls a dead rule live.

func TestAnApproverResolvesTheWayATaskAssigneeDoes(t *testing.T) {
	srv := newServerForErrors(t)
	if err := srv.users.Save(User{ID: "u1", Username: "Ada", Source: SourceLocal, CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := srv.users.Save(User{ID: "u2", Username: "gone", Source: SourceLocal, Disabled: true, CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatalf("seed disabled user: %v", err)
	}
	if err := srv.groups.Save(group{ID: "grp_ops", Name: "Operations", CreatedAt: 1, UpdatedAt: 1}); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	look := approverLookup{s: srv}

	for _, c := range []struct {
		name string
		got  bool
		want bool
	}{
		// holdsTask compares an assignee with strings.EqualFold, so the report has to
		// as well — otherwise every rule whose author typed a capital is reported.
		{"a username, as written", look.Account("Ada"), true},
		{"a username, in another case", look.Account("ada"), true},
		{"a username with the space a hand-written rule carries", look.Account(" Ada "), true},
		{"a username nobody answers to", look.Account("adaa"), false},
		// A disabled account cannot sign in, so an approval addressed to it is one
		// nobody can decide. That is the report's whole subject.
		{"a disabled account", look.Account("gone"), false},
		{"nothing at all", look.Account(""), false},

		// Candidate groups are matched against ids first and names after, both
		// ignoring case. Accepting only ids would report every rule an author wrote
		// by name — and those work.
		{"a group by id", look.Group("grp_ops"), true},
		{"a group by id, in another case", look.Group("GRP_OPS"), true},
		{"a group by name", look.Group("Operations"), true},
		{"a group by name, in another case", look.Group("operations"), true},
		{"a group that is neither", look.Group("grp_gone"), false},
		{"no group at all", look.Group("  "), false},
	} {
		if c.got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
}
