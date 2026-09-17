package api

import (
	"strings"
	"testing"
)

// The inbox decides a request, not a line (ADR-0362).
//
// The server half is held end to end in approvaldecide_http_test.go, and what the
// buttons send is held end to end in e2e/tasks-approval.spec.mjs. These hold the
// three properties that are cheaper to read than to drive, and they read the region
// they guard rather than the file — a search for a bare name across app.js finds
// the declaration of the thing it is meant to prove is used, and then stays green
// when the use is removed.
//
// They read app.js because that is where the decision is now: the approver's page
// was a second place to take it, and it is gone
// (ADR-draft-approval-in-the-inbox).

// TestTheInboxStillDecidesOnePositionByItself.
//
// The collective route is an addition and not a replacement. A screen that sent
// every decision through it would have moved the single approval — the common case,
// the one a mail link opens — onto a route with a different gate, and would have
// done it silently.
func TestTheInboxStillDecidesOnePositionByItself(t *testing.T) {
	body := webRegion(t, readWeb(t, "app.js"), "async function decideApproval(", "\n  }")
	if !strings.Contains(body, `"/api/v1/tasks/"`) {
		t.Error("a single approval is no longer completed through the task route, so " +
			"one decision now takes the collective gate")
	}
	if !strings.Contains(body, "/api/v1/approvals/decide") {
		t.Error("the collective route is never reached, so the checkbox decides one " +
			"position and says it decided all of them")
	}
	// Which of the two is a question about the batch and not about the checkbox: a
	// request with one position must take the single route even with the box ticked,
	// or the count in the message and the call would disagree.
	if !strings.Contains(body, "batch.length > 1") {
		t.Error("the route is not chosen by how many positions are being decided")
	}
}

// TestAPartialResultIsNotRoundedOff.
//
// There is no transaction across a request's process instances, so "two of three"
// happens. The screen has to read what the server said about the third, or an
// approver is told "decided" and finds out from the orderer.
//
// What the page this replaced did with the rest — list it — the inbox does by being
// an inbox: a position that was not decided was not completed, so its row is still
// there after the reload. The message says how many, and the list says which.
func TestAPartialResultIsNotRoundedOff(t *testing.T) {
	body := webRegion(t, readWeb(t, "app.js"), "async function decideApproval(", "\n  }")
	if !strings.Contains(body, "skipped") {
		t.Error("the answer's list of what did not go through is thrown away, so an " +
			"approver is told every position was decided")
	}
	if !strings.Contains(body, "skipped.length") {
		t.Error("what did not go through is never counted, so the message says " +
			"\"decided\" whatever came back")
	}
	if !strings.Contains(body, "await load()") {
		t.Error("the list is not re-read after a decision, so the positions that were " +
			"not decided are not the ones left on screen")
	}
}

// TestATickDoesNotSurviveTheNextRequest.
//
// "Decide all of this request" is a statement about the request that is open. An
// approver who ticked it for a twelve-line workplace and then opened somebody else's
// single laptop has said nothing about that one — and the box would be sitting there
// ticked.
//
// True by construction here rather than by a reset: the block is rebuilt from the
// selected task on every render, and it renders the box unticked. A `checked`
// attribute derived from anything held in state is what would end that.
func TestATickDoesNotSurviveTheNextRequest(t *testing.T) {
	body := webRegion(t, readWeb(t, "app.js"), "function approvalBlock(", "\n  }")
	if !strings.Contains(body, `<input type="checkbox" id="appr-together">`) {
		t.Error("the collective tick is not rendered fresh and unticked, so a decision " +
			"about one request can carry into the next")
	}
	// And the reason it may not be kept in state: nothing reads it back.
	if strings.Contains(body, "state.together") {
		t.Error("the tick is held in state, which is what lets it outlive the request " +
			"it was about")
	}
}

// TestTheRequestIsTheOrderAndNothingWider.
//
// The server refuses keys from two orders, and the screen must not offer them: a
// checkbox that gathered every approval in the list would put one reason on several
// people's requests and then be refused, which is a worse surface than not offering
// it.
func TestTheRequestIsTheOrderAndNothingWider(t *testing.T) {
	body := webRegion(t, readWeb(t, "app.js"), "function approvalSiblings(", "\n  }")
	if !strings.Contains(body, "orderId") {
		t.Error("the collective decision is not bounded by the order, so it gathers " +
			"approvals the server will refuse in one call")
	}
}
