package api

import (
	"strings"
	"testing"
)

// An approval belongs under Tasks, and it was always there.
//
// The finding that shaped this: an approval *is* an ordinary engine user task,
// `handleListTasks` does not filter those out, and the inbox never knew the word.
// So the same approval sat in two places and neither said it was the same thing —
// in the inbox as an unlabelled row that decides somebody's order, and in a menu
// entry of its own advertising a page.
//
// First the entry moved under Tasks, where Access review sits for the same reason:
// a second kind of thing addressed to a person, not a second application. Then the
// page it led to went, because the inbox reads and decides the approval itself
// (ADR-0394) — an entry
// beside the inbox leading to a second way of answering was the drift this removed,
// and the two had already begun to differ over whether a rejection needs a reason.

// appsList and topnav are the two navigation tables, read as regions because both
// are long and both mention routes the other one owns.
func appsList(t *testing.T) string {
	t.Helper()
	return webRegion(t, readWeb(t, "app.js"), "const APPS = [", "\n];")
}

func topnavList(t *testing.T) string {
	t.Helper()
	return webRegion(t, readWeb(t, "app.js"), "const TOPNAV = {", "\n};")
}

// TestApprovalsIsNotAnApplicationOfItsOwn.
func TestApprovalsIsNotAnApplicationOfItsOwn(t *testing.T) {
	if strings.Contains(appsList(t), `id: "approvals"`) {
		t.Error("approvals is still advertised as an application beside Modeler and " +
			"Operations, and it is a kind of task")
	}
	// And no entry under Tasks either, which is the half that changed: the page that
	// entry led to is gone, because the decision is taken in the inbox the entry sits
	// under (ADR-0394). An
	// entry beside the inbox, leading to a redirect into the inbox, is the second
	// place for one decision that this removed.
	tasks := webRegion(t, topnavList(t), "  tasks: [", "\n  ],")
	if strings.Contains(tasks, "genehmigung.html") {
		t.Error("an entry under Tasks still leads to the approvals page, which now only " +
			"forwards back into the inbox it sits under")
	}
}

// There is no separate page under Tasks any more, so the guard that insisted one
// opened in its own window has no subject. What it protected — following an entry
// must not unload the console somebody is in the middle of using — is still held
// for the drawer's own separate entry by TestThePortalSurfacesOpenInTheirOwnWindow,
// which is where the rule started.

// TestTheInboxSaysWhichRowsDecideAnOrder.
//
// The row was unlabelled: a task that decides what somebody gets, rendered like any
// other, with no product, no price and nothing to say where it is decided.
func TestTheInboxSaysWhichRowsDecideAnOrder(t *testing.T) {
	src := readWeb(t, "app.js")
	body := webRegion(t, src, "async function viewTasks(", "\nasync function viewStartProcess(")
	// The call, not the word: the paragraph explaining why this lookup exists names
	// the route too, and a guard that only looked for the name would be satisfied by
	// the comment after the call had gone.
	if !strings.Contains(body, `api("GET", "/api/v1/approvals")`) {
		t.Error("the inbox never asks which of its rows are approvals, so it cannot " +
			"say so")
	}
	// And the row says what it decides rather than only that it is one. Every
	// approval task is called "Genehmigen", so a queue of them was a column of
	// identical lines that had to be opened one at a time.
	if !strings.Contains(body, "tasks-item-appr") {
		t.Error("an approval row is marked as one and says nothing about what it " +
			"decides, so a list of them cannot be read")
	}
	// The decision is here too, which is what makes this the one place: a row that
	// said what it decided and sent somebody elsewhere to decide it would be the
	// second surface again, one screen further in.
	if !strings.Contains(body, `id="appr-approve"`) || !strings.Contains(body, `id="appr-reject"`) {
		t.Error("the inbox names an approval and offers no way to decide it")
	}
}

// TestTheInboxSurvivesAnApprovalListItCannotRead.
//
// The list is an addition to a screen that worked without it. A reader who holds no
// approvals, a route that answers 403 in a deployment with enforcement off, a
// server mid-restart — none of that is the inbox failing, and none of it may take
// the inbox down.
func TestTheInboxSurvivesAnApprovalListItCannotRead(t *testing.T) {
	body := webRegion(t, readWeb(t, "app.js"), "async function loadApprovalKeys(", "\n  }")
	if !strings.Contains(body, "catch") {
		t.Error("the approval lookup is unguarded, so a screen that worked without it " +
			"now fails with it")
	}
}
