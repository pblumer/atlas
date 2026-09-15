package api

import (
	"strings"
	"testing"
)

// The screen where somebody reads a finding before acting on it
// (ADR-draft-reconciliation).
//
// The comparison shipped before this page, and for a few days the findings were
// reachable only by constructing an HTTP POST with an id read out of a JSON array.
// The record named that as the gap. These hold the properties that make the page
// the thing the slice's posture requires rather than a table of rows.

func TestTheFindingsAreReachableFromOperations(t *testing.T) {
	src := readWeb(t, "app.js")

	if !strings.Contains(src, `{ name: "Reconciliation", route: "#/operations/reconciliation", role: "operator" }`) {
		t.Error("no navigation entry leads to the findings. The comparison writes them, the " +
			"three actions need a person to have read them, and a person who cannot reach " +
			"them is a person who cannot decide")
	}
	if !strings.Contains(src, `if (path === "#/operations/reconciliation")`) {
		t.Error("the route is offered in the navigation and resolves to nothing; following it " +
			"would land on a blank screen")
	}
	// Operations and not Catalogue. Maintaining a catalogue is authoring and is the
	// product manager's; acting on a finding is repair and is the operator's — which
	// is what the routes themselves say on the server.
	if strings.Contains(src, `route: "#/catalog/reconciliation"`) {
		t.Error("the findings sit under the catalogue. Acting on one is repair, not authoring, " +
			"and the server gates all three actions at the operator role")
	}
}

// TestBothDirectionsAreExplainedOnTheScreen.
//
// The two kinds are not symmetrical and a table that showed only their names would
// make them look it. One is somebody holding what nobody granted; the other is
// Atlas asserting something untrue — and the second is the one nobody expects.
func TestBothDirectionsAreExplainedOnTheScreen(t *testing.T) {
	src := readWeb(t, "reconciliation.js")

	for _, kind := range []string{"unmanaged", "missing"} {
		if !strings.Contains(src, kind+": {") {
			t.Fatalf("the screen has no entry for the %q direction; this test now checks "+
				"nothing and says so instead", kind)
		}
	}
	// Matched as short fragments rather than whole sentences: the source wraps, and
	// a test that pinned a sentence across a line break would fail on a reflow and
	// say nothing about what it was guarding.
	for _, phrase := range []struct{ frag, why string }{
		{"nothing in Atlas decided to give it to them", "what unmanaged means, in words"},
		{"does not agree with", "what missing means, in words"},
		{"keep asserting it", "that a missing finding does not go away by itself"},
	} {
		if !strings.Contains(src, phrase.frag) {
			t.Errorf("the screen does not say %q — %s", phrase.frag, phrase.why)
		}
	}
}

// TestOnlyTheActionThatReachesOutsideAtlasAsks.
//
// A confirmation on every action is a confirmation nobody reads by the third one,
// so they are not styled or guarded alike. Adopt and revoke are recoverable: the
// next comparison finds the truth again either way. Deprovisioning runs a process
// that takes access away in a real system, and Atlas cannot undo it — getting it
// back means ordering the product again, with its approval.
func TestOnlyTheActionThatReachesOutsideAtlasAsks(t *testing.T) {
	src := readWeb(t, "reconciliation.js")

	if !strings.Contains(src, `act === "deprovision" && !window.confirm(`) {
		t.Error("deprovisioning does not ask first. It is the one action whose effect is " +
			"outside Atlas and that Atlas cannot undo")
	}
	if !strings.Contains(src, "Atlas cannot undo it") {
		t.Error("the confirmation does not say that it cannot be undone, which is the whole " +
			"reason it asks")
	}
	// And the other two do not ask: a guard on everything is a guard on nothing.
	for _, act := range []string{"adopt", "revoke"} {
		if strings.Contains(src, `act === "`+act+`" && !window.confirm(`) {
			t.Errorf("%q asks for confirmation too. It is recoverable — the next comparison "+
				"finds the truth again — and confirming everything is how a confirmation "+
				"stops being read", act)
		}
	}
	// The destructive one is marked as such before the click, not only in the dialog.
	if !strings.Contains(src, `cls: "btn sm danger"`) {
		t.Error("deprovisioning is not styled as destructive, so the row looks like three " +
			"equivalent choices")
	}
}

// TestEveryOfferedActionIsAServerRoute: a button that posts to a route the server
// does not serve is a button that fails on click, and the failure would look like
// the finding being wrong rather than the page.
func TestEveryOfferedActionIsAServerRoute(t *testing.T) {
	src := readWeb(t, "reconciliation.js")
	if !strings.Contains(src, "/api/v1/reconciliation/${encodeURIComponent(id)}/${act}") {
		t.Fatal("the page no longer builds its action path from the act; this test cannot " +
			"check what it posts to")
	}

	offered := map[string]bool{}
	for _, act := range []string{"adopt", "deprovision", "revoke"} {
		if strings.Contains(src, `act: "`+act+`"`) {
			offered[act] = true
		}
	}
	if len(offered) != 3 {
		t.Errorf("the page offers %v; the server serves three actions and a direction with "+
			"no offered remedy is a finding nobody can close", offered)
	}

	routes := routeTableFor(t)
	for act := range offered {
		pattern := "POST /api/v1/reconciliation/{id}/" + act
		if !routes[pattern] {
			t.Errorf("the page posts to %s, which the route table does not serve", pattern)
		}
	}
}

// routeTableFor collects the declared routes as "METHOD PATTERN", so a screen can
// be held against what the server actually mounts rather than against a list
// somebody keeps in step by hand.
func routeTableFor(t *testing.T) map[string]bool {
	t.Helper()
	s := &Server{}
	out := map[string]bool{}
	for _, r := range s.apiRoutes() {
		out[r.method+" "+r.pattern] = true
	}
	if len(out) == 0 {
		t.Fatal("the route table is empty; this test now checks nothing")
	}
	return out
}
