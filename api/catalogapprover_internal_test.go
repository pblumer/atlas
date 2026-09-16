package api

import (
	"strings"
	"testing"
)

// The approver, picked per kind.
//
// This was the last typed identifier on the catalogue screen, and the most
// expensive one, because nothing reports a wrong value. genehmigung-fix.bpmn puts
// the ref in `assignee`, and holdsTask compares an assignee against the caller's
// *username*; genehmigung-rolle.bpmn puts it in `candidateGroups`, matched against
// group *ids* first. A misspelt username or a group name where an id belongs
// creates an approval that reaches no inbox — the order does not fail, it waits.
//
// So the control is not one picker but two, chosen by the kind, and the value each
// sends is decided by what matches it at the other end rather than by taste.

// TestTheApproverIsPickedForTheKindThatAsksForOne.
func TestTheApproverIsPickedForTheKindThatAsksForOne(t *testing.T) {
	body := webRegion(t, readWeb(t, "catalog-admin.js"), "function approverField(ap, dir, people)", "\n}")

	// A named person is a username, from the assignable-accounts list — the one list
	// that carries one. The principals directory carries display names and ids.
	if !strings.Contains(body, `<select name="aref-fixed">`) {
		t.Error("a named approver is still typed rather than picked")
	}
	if !strings.Contains(body, "esc(u.username)") {
		t.Error("the named approver is not sent as a username, so the approval lands in " +
			"nobody's inbox and the order waits")
	}
	// A group is an id, because candidateGroups is matched against ids first and a
	// rename then costs nothing.
	if !strings.Contains(body, `<select name="aref-role">`) {
		t.Error("a group approver is still typed rather than picked")
	}
	if !strings.Contains(body, "groupChoices(dir)") {
		t.Error("the group approver is not offered from the directory")
	}
	if !strings.Contains(body, "esc(g.id)") {
		t.Error("the group approver is not sent as an id, so renaming the group loses the approver")
	}
}

// TestOnlyTheApproverTheKindAsksForIsSaved.
//
// Switching from "a named person" to "the orderer's superior" has to clear the
// username. Left behind it rides along in a rule with no use for it and sits in the
// catalogue looking like an answer to a question nobody asked — and the next reader
// cannot tell it from a rule that means it.
func TestOnlyTheApproverTheKindAsksForIsSaved(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	body := webRegion(t, src, "function approvalFrom(f)", "\n}")
	if !strings.Contains(body, `{ fixed: "aref-fixed", role: "aref-role" }`) {
		t.Fatal("the save no longer maps a kind to the field it asks for")
	}
	if !strings.Contains(body, `ref: field ? String(f.get(field) || "").trim() : ""`) {
		t.Error("a kind that asks for no approver does not clear the one already there")
	}
	// And the form is what feeds it: the old single field would make the map useless.
	if strings.Contains(src, `<input name="aref" `) {
		t.Error("the single free-text approver field is still rendered")
	}
}

// TestTheApproverControlFollowsTheKindWithoutAReload.
//
// Three controls exist and one is shown. The choice is made by a select above them,
// so the page has to react — a form that shows the wrong control until it is
// reopened teaches somebody that the field does not apply to them.
func TestTheApproverControlFollowsTheKindWithoutAReload(t *testing.T) {
	src := readWeb(t, "catalog-admin.js")
	// On the view and not on the select: the product form is re-rendered from
	// scratch every time a product is opened, and a listener bound to the select
	// inside it would be thrown away with it.
	handler := webRegion(t, src, `view.addEventListener("change"`, "\n  });")
	if !strings.Contains(handler, `select[name="akind"]`) {
		t.Fatal("nothing listens for the approval kind changing")
	}
	if !strings.Contains(handler, "box.dataset.kind !== sel.value") {
		t.Error("the control shown does not follow the kind chosen")
	}
}
