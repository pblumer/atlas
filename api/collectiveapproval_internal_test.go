package api

import (
	"strings"
	"testing"
)

// The approver's page decides a request, not a line (ADR-0362).
//
// The server half is held end to end in approvaldecide_http_test.go. These hold
// the half that lives in the browser, and they read the region they guard rather
// than the file — a search for a bare name across genehmigung.js also finds the
// declaration of the thing it is meant to prove is used, and then stays green when
// the use is removed.

// TestThePageStillDecidesOnePositionByItself.
//
// The collective route is an addition and not a replacement. A page that sent
// every decision through it would have moved the single approval — the common
// case, the one a mail link opens — onto a route with a different gate, and
// would have done it silently.
func TestThePageStillDecidesOnePositionByItself(t *testing.T) {
	body := webRegion(t, readWeb(t, "genehmigung.js"), "async function decide(", "\n}")
	if !strings.Contains(body, "/api/v1/tasks/") {
		t.Error("the page no longer completes a single approval through the task route, " +
			"so one decision now takes the collective gate")
	}
	if !strings.Contains(body, "/api/v1/approvals/decide") {
		t.Error("the page never reaches the collective route, so the checkbox decides " +
			"one position and says it decided all of them")
	}
	// Which of the two is a question about the batch and not about the checkbox: a
	// request with one position must take the single route even with the box ticked,
	// or the count on the button and the call would disagree.
	if !strings.Contains(body, "batch.length > 1") {
		t.Error("the page does not choose its route by how many positions it is " +
			"deciding")
	}
}

// TestAPartialResultIsNotRoundedOff.
//
// There is no transaction across a request's process instances, so "two of three"
// happens. The page has to read what the server said about the third, or an
// approver is told "decided" and finds out from the orderer.
func TestAPartialResultIsNotRoundedOff(t *testing.T) {
	src := readWeb(t, "genehmigung.js")
	decide := webRegion(t, src, "async function decide(", "\n}")
	if !strings.Contains(decide, "skipped") {
		t.Error("the page throws away what the server said did not go through")
	}
	// The screen that says "decided", not the function around it: the list of what
	// did not go through can sit in the source behind a condition that is never
	// true, and a guard looking only for the name would not notice.
	done := webRegion(t, src, "  if (state.decided) {", "\n  }")
	if !strings.Contains(done, "state.partial.length") {
		t.Error("the screen that says a request was decided does not ask whether " +
			"anything was left open")
	}
	if !strings.Contains(done, "state.partial.map") {
		t.Error("what did not go through is counted and never named, so the approver " +
			"is told a number and has to go looking")
	}
}

// TestATickDoesNotSurviveTheNextRequest.
//
// "Decide all of this request" is a statement about the request that is open. An
// approver who ticked it for a twelve-line workplace and then opened somebody
// else's single laptop has said nothing about that one — and the box would be
// sitting there ticked.
func TestATickDoesNotSurviveTheNextRequest(t *testing.T) {
	body := webRegion(t, readWeb(t, "genehmigung.js"), "function select(", "\n}")
	if !strings.Contains(body, "state.together = false") {
		t.Error("selecting another approval keeps the collective tick, so a decision " +
			"about one request carries into the next")
	}
	if !strings.Contains(body, "state.partial = []") {
		t.Error("the previous decision's unfinished positions are still on screen " +
			"beside a different request")
	}
}

// TestTheRequestIsTheOrderAndNothingWider.
//
// The server refuses keys from two orders, and the page must not offer them: a
// checkbox that gathered every approval in the list would put one reason on
// several people's requests and then be refused, which is a worse surface than
// not offering it.
func TestTheRequestIsTheOrderAndNothingWider(t *testing.T) {
	body := webRegion(t, readWeb(t, "genehmigung.js"), "function siblings(", "\n}")
	if !strings.Contains(body, "orderId") {
		t.Error("the collective decision is not bounded by the order, so it gathers " +
			"approvals the server will refuse in one call")
	}
}
