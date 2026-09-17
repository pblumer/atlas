package catalog

import (
	"net/http"
	"strings"
	"testing"
)

// The report of approvers that reach nobody.
//
// The rule it mirrors lives in the task surface: an assignee is matched against a
// username, and candidate groups against a group id first and a group name after,
// both ignoring case. A report stricter than the thing it reports on names working
// rules as broken, which is worse than no report — somebody corrects a rule that
// was right, and stops believing the next line.

// lookup is an ApproverLookup over two fixed sets.
type lookup struct{ accounts, groups map[string]bool }

func (l lookup) Account(u string) bool { return l.accounts[strings.ToLower(u)] }
func (l lookup) Group(g string) bool   { return l.groups[strings.ToLower(g)] }

func known() lookup {
	return lookup{
		accounts: map[string]bool{"ada": true},
		groups:   map[string]bool{"grp_ops": true, "operations": true},
	}
}

func withApproval(id string, kind ApprovalKind, ref string) Item {
	it := item(id)
	it.Approval = Approval{Kind: kind, Ref: ref}
	return it
}

func onlyProblem(t *testing.T, items []Item) ApproverProblem {
	t.Helper()
	got := approverProblems(items, known())
	if len(got) != 1 {
		t.Fatalf("want exactly one problem, got %d: %+v", len(got), got)
	}
	return got[0]
}

// TestAnApproverThatNamesNobodyIsReported.
func TestAnApproverThatNamesNobodyIsReported(t *testing.T) {
	for _, c := range []struct {
		name, want string
		it         Item
	}{
		{"a named person who does not exist", "no enabled account answers to",
			withApproval("laptop", KindFixed, "adaa")},
		{"a named person, and nobody named", "needs a named approver and names none",
			withApproval("laptop", KindFixed, "")},
		{"a group that does not exist", "is no group here",
			withApproval("laptop", KindRole, "grp_gone")},
		{"a group, and none named", "needs a group and names none",
			withApproval("laptop", KindRole, "  ")},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := onlyProblem(t, []Item{c.it})
			if !strings.Contains(p.Why, c.want) {
				t.Errorf("why = %q, want it to mention %q", p.Why, c.want)
			}
			if p.ItemID != "laptop" {
				t.Errorf("itemId = %q", p.ItemID)
			}
		})
	}
}

// TestAnApproverThatReachesSomebodyIsNotReported.
//
// The half that matters more, because a report that names working rules is a
// report nobody reads twice. Each of these resolves the way the task surface
// resolves it, and a stricter reading would flag every one.
func TestAnApproverThatReachesSomebodyIsNotReported(t *testing.T) {
	items := []Item{
		// Matched by username, ignoring case — which is how an assignee is matched.
		withApproval("a", KindFixed, "Ada"),
		// A group by its id, and a group by its name: candidate groups accept both.
		withApproval("b", KindRole, "grp_ops"),
		withApproval("c", KindRole, "Operations"),
		// A list with one live entry still reaches that group.
		withApproval("d", KindRole, "grp_gone, grp_ops"),
		// Surrounding space is what a hand-written rule carries.
		withApproval("e", KindFixed, "  ada  "),
		// The two kinds that resolve without a reference at all.
		withApproval("f", KindNone, ""),
		withApproval("g", KindSuperior, ""),
		// A leftover reference beside a kind that needs none is untidy, not broken.
		withApproval("h", KindSuperior, "somebody-who-left"),
		// A kind that names a process directly: what it does with the reference is
		// the process's business, and there is nothing here to check it against.
		withApproval("i", "my-own-approval", "whatever this means"),
	}
	if got := approverProblems(items, known()); len(got) != 0 {
		t.Errorf("reported %d working rules: %+v", len(got), got)
	}
}

// TestTheReportIsOrderedAndCounted.
//
// An empty problem list has to be readable as "none of the 240 I looked at"
// rather than as "nothing was looked at", and a list that reorders itself between
// two reads of an unchanged estate cannot be diffed by whoever is working through
// it.
func TestTheReportIsOrderedAndCounted(t *testing.T) {
	items := []Item{
		withApproval("vpn", KindFixed, "nobody"),
		withApproval("account", KindRole, "grp_gone"),
		withApproval("mailbox", KindNone, ""),
	}
	got := approverProblems(items, known())
	if len(got) != 2 {
		t.Fatalf("want two problems, got %d: %+v", len(got), got)
	}
	if got[0].ItemID != "account" || got[1].ItemID != "vpn" {
		t.Errorf("not ordered by product: %s, %s", got[0].ItemID, got[1].ItemID)
	}
	// The stored string, echoed rather than tidied: a reference that differs from
	// what its author believes they typed is the ordinary case here.
	if got[1].Ref != "nobody" || got[1].Kind != "fixed" {
		t.Errorf("the problem does not carry what the rule says: %+v", got[1])
	}
}

// TestTheApproverReportFollowsTheHomeCatalogue.
//
// The report names products and the people who approve them, which is maintenance
// information — so it reaches exactly as far as the product listing does, and no
// further. Cited by name from the gate inventory, which is where the claim that
// this handler needs no object gate is written down.
func TestTheApproverReportFollowsTheHomeCatalogue(t *testing.T) {
	s := serviceWithAdmin(t)
	s.Approvers = known()
	mine := makeCatalog(t, s, user("usr_a"))
	theirs := makeCatalog(t, s, user("usr_b"))
	as(t, s.HandleSaveItem, user("usr_a"), "POST", brokenApproverBody("mine", mine.ID))
	as(t, s.HandleSaveItem, user("usr_b"), "POST", brokenApproverBody("theirs", theirs.ID))

	got := decode[ApproverReport](t, as(t, s.HandleApproverReport, user("usr_a"), "GET", ""))
	if got.Checked != 1 {
		t.Errorf("checked = %d, want only the one product I maintain", got.Checked)
	}
	if len(got.Problems) != 1 || got.Problems[0].ItemID != "mine" {
		t.Fatalf("problems = %+v, want only mine — a report that names somebody else's "+
			"products names their approvers too", got.Problems)
	}
}

// TestWithoutAResolverTheReportRefusesRatherThanGuesses.
//
// Both guesses are available and both are worse than a refusal: reading every
// reference as unresolvable reports the whole catalogue as broken, and reading
// every one as fine reports a clean estate that nobody checked. The second is the
// dangerous one, because it is the answer somebody wants to see.
func TestWithoutAResolverTheReportRefusesRatherThanGuesses(t *testing.T) {
	s := serviceWithAdmin(t)
	cat := makeCatalog(t, s, user("usr_a"))
	as(t, s.HandleSaveItem, user("usr_a"), "POST", brokenApproverBody("laptop", cat.ID))

	rec := as(t, s.HandleApproverReport, user("usr_a"), "GET", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when no resolver is set; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "cannot say") {
		t.Errorf("the refusal does not say what it cannot do: %s", rec.Body.String())
	}
}

// brokenApproverBody is a product whose approval names an account nobody answers to.
func brokenApproverBody(id, home string) string {
	return `{"id":"` + id + `","homeCatalog":"` + home + `","state":"active",` +
		`"texts":{"de":"X"},"approval":{"kind":"fixed","ref":"nobody"},` +
		`"provisionProcess":"p","deprovisionProcess":"d"}`
}
