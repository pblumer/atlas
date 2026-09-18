package api

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The approver's page is retired, and its address is not
// (ADR-0394).
//
// The page was a second place to take one decision, and the two had begun to differ
// — one of them refused a rejection with no reason and the other did not. The
// decision moved into the inbox that already held the task, and the page went.
//
// What stays is the address. Every approval notification ever sent carries a link to
// it with the order line in the query, and a mail cannot be recalled: an approver
// opening one next week must land on their approval rather than on a 404.

// TestTheOldAddressStillLeadsSomewhere.
func TestTheOldAddressStillLeadsSomewhere(t *testing.T) {
	stub := readWeb(t, "genehmigung.html")
	if !strings.Contains(stub, "/index.html#/tasks") {
		t.Fatal("the retired address does not forward into the inbox, so every " +
			"notification already sent points at nothing")
	}
	// Carrying what it was given. A forward that dropped the query would land the
	// approver in an inbox of fourteen identical rows, which is the thing the link
	// exists to skip.
	if !strings.Contains(stub, `"/index.html#/tasks" + (q`) {
		t.Error("the forward drops the order line it was linked with, so the approver " +
			"arrives in the inbox without the approval they were sent to")
	}
	if !strings.Contains(stub, "location.replace(") {
		t.Error("the forward is a navigation rather than a replacement, so the back " +
			"button returns to a page that only forwards again")
	}
	// And it is a stub: the application that was here is gone, not hidden.
	if strings.Contains(stub, "genehmigung.js") || strings.Contains(stub, "type=\"module\"") {
		t.Error("the retired page still loads an application; what is left should be " +
			"the address and nothing else")
	}
	if _, err := os.Stat(filepath.Join("web", "genehmigung.js")); err == nil {
		t.Error("web/genehmigung.js is still served, so the page it belonged to is " +
			"retired in name only")
	}
}

// TestTheShippedApprovalsLinkIntoTheInbox.
//
// Three models send the notification and each sends it twice — once when the
// approval opens and once when the reminder fires. A link that survived in one of
// the six is a mail that sends an approver through a redirect, and the redirect is
// there for mails that were already sent rather than for mails still being written.
func TestTheShippedApprovalsLinkIntoTheInbox(t *testing.T) {
	for _, model := range []string{
		"genehmigung-fix.bpmn", "genehmigung-rolle.bpmn", "genehmigung-vorgesetzter.bpmn",
	} {
		raw, err := os.ReadFile(filepath.Join("systemprocesses", model))
		if err != nil {
			t.Fatalf("read %s: %v", model, err)
		}
		src := string(raw)
		if strings.Contains(src, "genehmigung.html") {
			t.Errorf("%s still links its notification at the retired page", model)
		}
		if n := strings.Count(src, "/index.html#/tasks?order="); n != 2 {
			t.Errorf("%s carries %d links into the inbox, want 2 — the notification and "+
				"its reminder", model, n)
		}
	}
}
