package api

import (
	"strings"
	"testing"
)

// The screen where somebody answers one question at a time
// (ADR-draft-access-recertification).
//
// The record's whole argument is that an attestation is worth the reading behind
// it. A screen is where that is won or lost: everything the server refuses to make
// cheap, an interface can make cheap again with one button.

func TestTheAccessReviewIsReachableFromTasks(t *testing.T) {
	src := readWeb(t, "app.js")

	if !strings.Contains(src, `{ name: "Access review", route: "#/tasks/recertification", role: "user" }`) {
		t.Error("no navigation entry leads to the access review. A campaign that reaches " +
			"nobody is a campaign nobody answers, and the rows then close as unanswered " +
			"through no fault of the reviewer")
	}
	if !strings.Contains(src, `if (path === "#/tasks/recertification")`) {
		t.Error("the route is offered in the navigation and resolves to nothing; following it " +
			"would land on a blank screen")
	}
	// Tasks and not Operations. Reconciliation is repair and the operator's; this
	// asks a line manager about their own team, and the server gates the two
	// decisions at `user` precisely because a line manager is one.
	if strings.Contains(src, `route: "#/operations/recertification"`) {
		t.Error("the access review sits in Operations. The person who answers it is an " +
			"ordinary user who has never opened Operations")
	}
}

// TestTheScreenOffersNoWayToAnswerInBulk.
//
// This is the record in one test. The API has no bulk route, and a screen that
// added one — a select-all, a "certify the rest", a walk-the-list shortcut — would
// hand back exactly the signature-without-a-reading the whole slice exists to
// prevent. It is worth guarding here because it is the single most tempting feature
// request this page will ever receive.
func TestTheScreenOffersNoWayToAnswerInBulk(t *testing.T) {
	src := readWeb(t, "recertification.js")
	code := withoutLineComments(src)

	for _, tempting := range []string{
		"selectAll", "select-all", "certify all", "Certify all", "keepAll", "approveAll",
		"data-act=\"keep-all\"", "rows.forEach(async",
	} {
		if strings.Contains(code, tempting) {
			t.Errorf("the screen contains %q. An attestation is worth the reading behind it, "+
				"and a campaign answered in bulk is a signature with none", tempting)
		}
	}
	// And it says so, because a reviewer who cannot find the bulk button should
	// learn that there isn't one rather than conclude the page is broken.
	if !strings.Contains(src, "no way to answer several at once") {
		t.Error("the screen does not tell the reviewer that answering one at a time is " +
			"deliberate; an absent feature with no explanation reads as a missing one")
	}
}

// TestAnUnansweredRowIsNamedAsUnanswered.
//
// Not "pending", not "open", and never implied by a progress bar. What the campaign
// will record for that row is *nothing*, and the word has to say so — a reviewer
// reading "83% complete" treats the remainder as work in hand, and whoever closes
// the campaign treats it as almost finished.
func TestAnUnansweredRowIsNamedAsUnanswered(t *testing.T) {
	src := readWeb(t, "recertification.js")

	if !strings.Contains(src, `>unanswered</span>`) {
		t.Error("an undecided row is not labelled as unanswered")
	}
	if !strings.Contains(src, "closing the campaign will not make them so") {
		t.Error("the summary does not say that unanswered rows stay uncertified, which is " +
			"the one thing a person closing a campaign has to know")
	}
	if code := withoutLineComments(src); strings.Contains(code, "progress") ||
		strings.Contains(code, "% complete") {
		t.Error("the screen shows progress. The unanswered rows are a finding, not a " +
			"remainder to be worked off")
	}
}

// TestTheReviewerIsToldWhereTheRightCameFromAndWhetherItIsDisputed.
//
// Two facts decide the answer, and a row without them is a question nobody can
// answer honestly: an `ordered` right carries an approval behind it, a `legacy` one
// carries nobody's decision at all — and a disputed right is one the target system
// currently denies, so certifying it attests to something contested.
func TestTheReviewerIsToldWhereTheRightCameFromAndWhetherItIsDisputed(t *testing.T) {
	src := readWeb(t, "recertification.js")

	for _, origin := range []string{"ordered", "adopted", "legacy"} {
		if !strings.Contains(src, origin+": {") {
			t.Errorf("the screen explains no %q origin; the three mean completely different "+
				"things to somebody deciding", origin)
		}
	}
	// Short fragments rather than whole sentences: the source wraps, and a test that
	// pinned a sentence across a line break would fail on a reflow and say nothing
	// about what it was guarding.
	for _, frag := range []struct{ text, why string }{
		{"Atlas did not grant it", "what an adopted right is"},
		{"Nobody here decided it", "what a legacy right is"},
		{"attests to something the target system denies", "why a disputed row matters"},
	} {
		if !strings.Contains(src, frag.text) {
			t.Errorf("the screen does not say %q — %s", frag.text, frag.why)
		}
	}
	if !strings.Contains(src, `disputePill(r)`) {
		t.Error("the dispute is explained and never rendered, so a reviewer certifies a " +
			"contested right without being told")
	}
}

// TestOnlyTheAnswerThatTakesAccessAwayAsks.
//
// The same rule as the reconciliation screen, for the same reason: a confirmation
// on every answer is one nobody reads by the third row. Confirming a right is
// recoverable — the next campaign asks again — and withdrawing it is not.
func TestOnlyTheAnswerThatTakesAccessAwayAsks(t *testing.T) {
	src := readWeb(t, "recertification.js")

	if !strings.Contains(src, `act === "revoke" && !window.confirm(`) {
		t.Error("withdrawing a right does not ask first. It is the answer whose effect is " +
			"outside Atlas and that Atlas cannot undo")
	}
	if strings.Contains(src, `act === "keep" && !window.confirm(`) {
		t.Error("confirming a right asks too. It changes nothing but the record that " +
			"somebody said so, and confirming everything is how a confirmation stops " +
			"being read")
	}
	if !strings.Contains(src, "Atlas cannot undo it") {
		t.Error("the confirmation does not say that it cannot be undone, which is the whole " +
			"reason it asks")
	}
	if !strings.Contains(src, `data-act="revoke"`) || !strings.Contains(src, `btn sm danger`) {
		t.Error("the destructive answer is not styled as one, so the row looks like two " +
			"equivalent choices")
	}
}

// TestEveryAnswerTheScreenOffersIsAServerRoute: a button that posts where nothing
// listens fails on click, and the failure reads as the campaign being broken rather
// than the page.
func TestEveryAnswerTheScreenOffersIsAServerRoute(t *testing.T) {
	src := readWeb(t, "recertification.js")
	if !strings.Contains(src, "/api/v1/recertification/${encodeURIComponent(id)}/rows/${encodeURIComponent(row)}/${act}") {
		t.Fatal("the page no longer builds its decision path from the act; this test cannot " +
			"check what it posts to")
	}

	offered := map[string]bool{}
	for _, act := range []string{"keep", "revoke"} {
		if strings.Contains(src, `data-act="`+act+`"`) {
			offered[act] = true
		}
	}
	if len(offered) != 2 {
		t.Errorf("the page offers %v; a campaign with only one answer available is a "+
			"campaign whose answer is already known", offered)
	}

	routes := routeTableFor(t)
	for act := range offered {
		pattern := "POST /api/v1/recertification/{id}/rows/{row}/" + act
		if !routes[pattern] {
			t.Errorf("the page posts to %s, which the route table does not serve", pattern)
		}
	}
	// And the reviewer's own view, which is how somebody finds their four rows in a
	// campaign of five thousand.
	if !strings.Contains(src, `?mine=true`) {
		t.Error("the screen never asks for the caller's own rows, so a reviewer reads the " +
			"whole estate to find the questions addressed to them")
	}
}

// withoutLineComments drops the source's own prose before a test asks what the
// screen does.
//
// These guards forbid things — a bulk answer, a progress bar — and the file that
// implements them also explains at length why they are absent. Matching the whole
// source would make a record of the reasoning fail the test that the reasoning was
// followed, which is the most annoying possible way to be wrong.
func withoutLineComments(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
