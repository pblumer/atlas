package api

import (
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
)

// TestCollectFormFieldKeysWalksTheWholeSchema pins the allowlist's source. A
// form-js schema nests — a group holds components of its own — so a walk that only
// looked at the top level would hand a task holder an allowlist missing every field
// inside a group, and the form would render blank. A key that addresses a nested
// value by path names a variable at its root, which is what an instance scope
// holds.
func TestCollectFormFieldKeysWalksTheWholeSchema(t *testing.T) {
	out := map[string]bool{}
	collectFormFieldKeys([]byte(`{"type":"default","components":[
		{"type":"textfield","key":"top"},
		{"type":"group","key":"grp","components":[
			{"type":"textfield","key":"customer.name"},
			{"type":"group","components":[{"type":"checkbox","key":"deep"}]}
		]},
		{"type":"text"},
		"not an object"
	]}`), out)
	for _, want := range []string{"top", "grp", "customer", "deep"} {
		if !out[want] {
			t.Errorf("field %q was not collected: %v", want, out)
		}
	}
	if out["customer.name"] {
		t.Errorf("a path was collected whole: %v", out)
	}
}

// TestAFormNobodyCanParseGrantsNothing: the allowlist decides what a task holder is
// handed, so a schema the walk cannot read must contribute no fields rather than
// fall back to something. Failing closed here costs a blank form; failing open
// costs the instance.
func TestAFormNobodyCanParseGrantsNothing(t *testing.T) {
	out := map[string]bool{}
	collectFormFieldKeys([]byte(`{"components": [`), out)
	collectFormFieldKeys(nil, out)
	if len(out) != 0 {
		t.Errorf("fields = %v, want none from a schema that will not parse", out)
	}
}

// TestInstanceAccessAllows pins how the two answers differ. Full access asks no
// questions; a field allowlist answers for the names on it and for the root of a
// dotted path, and for nothing else.
func TestInstanceAccessAllows(t *testing.T) {
	full := instanceAccess{full: true}
	if !full.allows("anything") || !full.any() {
		t.Error("full access refused something")
	}
	scoped := instanceAccess{fields: map[string]bool{"comment": true}}
	if !scoped.allows("comment") {
		t.Error("an allowlisted field was refused")
	}
	if !scoped.allows("comment.body") {
		t.Error("a path rooted in an allowlisted field was refused")
	}
	if scoped.allows("secret") || scoped.allows("secret.part") {
		t.Error("a field nobody's form asks for was allowed")
	}
	if !scoped.any() {
		t.Error("an allowlist with a field in it reads as no access")
	}
	if (instanceAccess{}).any() {
		t.Error("an empty allowlist reads as access")
	}
}

// TestHoldsTask pins the rule this change had to invent: a claimed task belongs to
// its assignee alone, and an unclaimed one is readable by the identity groups its
// candidate list names — matched by group name or by id, because a BPMN candidate
// group is free text and a modeller may have written either.
func TestHoldsTask(t *testing.T) {
	groups, err := newGroupStore(t.TempDir())
	if err != nil {
		t.Fatalf("newGroupStore: %v", err)
	}
	if err := groups.Save(group{ID: "grp_reviewers", Name: "Reviewers"}); err != nil {
		t.Fatalf("save group: %v", err)
	}
	s := &Server{groups: groups}
	alice := &httpapi.Principal{UserID: "usr_a", Username: "alice", GroupIDs: []string{"grp_reviewers"}}

	if !s.holdsTask(alice, "Alice", "") {
		t.Error("the assignee was not recognised (the comparison is case-insensitive)")
	}
	if s.holdsTask(alice, "bob", "grp_reviewers") {
		t.Error("a claimed task was readable by the candidate group it names")
	}
	if !s.holdsTask(alice, "", "grp_reviewers") {
		t.Error("an unclaimed task naming the caller's group id was not matched")
	}
	if !s.holdsTask(alice, "", " other , GRP_REVIEWERS ") {
		t.Error("the candidate list is comma-separated and matched case-insensitively")
	}
	if !s.holdsTask(alice, "", "reviewers") {
		t.Error("a candidate group named by the identity group's *name* was not matched")
	}
	if s.holdsTask(alice, "", "grp_others") {
		t.Error("a group the caller is not in was matched")
	}
	if s.holdsTask(alice, "", ",, ,") {
		t.Error("an empty candidate entry matched something")
	}
	anonymous := &httpapi.Principal{}
	if anonymous.Username == "" && s.holdsTask(anonymous, "someone", "") {
		t.Error("a principal with no username matched an assignee")
	}
}
