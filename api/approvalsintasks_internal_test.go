package api

import (
	"strings"
	"testing"
)

// An approval belongs under Tasks, and it was already there.
//
// The finding that shaped this: an approval *is* an ordinary engine user task,
// `handleListTasks` does not filter those out, and the inbox never knew the word.
// So the same approval sat in two places and neither said it was the same thing —
// in the inbox as an unlabelled row that decides somebody's order, and in a menu
// entry of its own advertising a page.
//
// The entry moves under Tasks, where Access review already sits for the same
// reason: a second kind of thing addressed to a person, not a second application.
// And the inbox row says what it is and where it is decided, which is the half that
// was actually missing.

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
	tasks := webRegion(t, topnavList(t), "  tasks: [", "\n  ],")
	if !strings.Contains(tasks, `route: "genehmigung.html"`) {
		t.Error("no entry under Tasks leads to the approvals page. Its only other way " +
			"in is the link in a notification mail, and a decision nobody can reach is " +
			"an order that waits forever")
	}
}

// TestASeparatePageUnderTasksStillOpensInItsOwnWindow.
//
// The reason this is not a one-line move. The drawer opens a separate page in its
// own window on purpose — replacing the console in the same tab puts whoever
// followed the entry on a page whose only way back is one small link, and asks
// somebody in the middle of something to lose it. The sub-navigation renders a
// plain anchor, so moving the entry down there without teaching it the same rule
// would restore exactly the behaviour that comment argues against.
func TestASeparatePageUnderTasksStillOpensInItsOwnWindow(t *testing.T) {
	// The entry says it is a page of its own. This half moved here from
	// TestThePortalSurfacesOpenInTheirOwnWindow, which made the same promise while
	// the entry was in the drawer.
	tasks := webRegion(t, topnavList(t), "  tasks: [", "\n  ],")
	for _, line := range strings.Split(tasks, "\n") {
		if strings.Contains(line, `route: "genehmigung.html"`) &&
			!strings.Contains(line, "separate: true") {
			t.Error("the approvals entry is not marked separate, so following it replaces " +
				"the console in the same tab. It is a page of its own — its own brand, " +
				"its own message catalogue — and unloading Atlas to reach it costs " +
				"whoever clicked whatever they had open")
		}
	}

	body := webRegion(t, readWeb(t, "app.js"), "topnav.innerHTML = ", "syncIncidentBadge(")
	if !strings.Contains(body, "separate") {
		t.Error("the sub-navigation renders every entry as a plain link, so a page of " +
			"its own replaces the console in the same tab")
	}
	if !strings.Contains(body, "_blank") {
		t.Error("a separate page under Tasks does not open in its own window")
	}
}

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
	// Linked by the order line, which is what the approvals page takes and what it
	// takes for a reason its own comment gives: a task key does not exist until the
	// task activates, and the order and the product do.
	if !strings.Contains(body, "genehmigung.html?order=") {
		t.Error("an approval row does not lead to where it is decided, or leads there " +
			"without naming which decision")
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
