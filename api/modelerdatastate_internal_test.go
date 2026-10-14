package api

import (
	"regexp"
	"strings"
	"testing"
)

// The data-state caption on a data object.
//
// A `<dataObjectReference>` carries a data state — the `[ARCHIVIERT]` BPMN writes under
// the box (ADR-0053, ADR-0259) — and bpmn-js parses it into the business object without
// drawing anything with it. editor.js draws it: drawDataStateLabels writes the state
// under the object's name on every surface that renders a diagram.
//
// "Every surface" is the part that rots. There are five bpmn-js instances in editor.js —
// the Modeler and four read-only views — and each one wires its own decorations. A view
// added or reworked without this call renders a diagram whose data objects are silently
// unlabelled, which looks exactly like a model that carries no states at all. That is
// invisible in review and invisible in the browser, so it is checked here instead.
//
// The type badges (drawImplBadges) are the proxy: they are the other decoration every
// view draws, they are drawn at the same point in each view's setup, and any new view
// will draw them too. So every drawImplBadges call site must be joined by a
// drawDataStateLabels one — including inside the runtime view's poll, which clears its
// overlays and redraws both.

// implBadgeCallRe matches a drawImplBadges call and captures the bpmn-js instance it is
// handed (`viewer`, `v`), so the pairing can be checked per instance rather than only
// per count.
var implBadgeCallRe = regexp.MustCompile(`drawImplBadges\((\w+)\)`)

// TestEveryViewDrawingTypeBadgesAlsoDrawsDataStates keeps the four read-only views in
// step with the Modeler. A data state is model content — what the object *is* at that
// point in the process — so a view that draws the diagram at all owes the reader that
// caption; unlike the implementation badges, it is not a level-of-detail choice.
func TestEveryViewDrawingTypeBadgesAlsoDrawsDataStates(t *testing.T) {
	src := modelerSource(t)
	lines := strings.Split(src, "\n")

	var badgeSites int
	var missing []string
	for i, line := range lines {
		m := implBadgeCallRe.FindStringSubmatch(line)
		if m == nil || strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue // a mention in a comment is not a call site
		}
		// `modeler` is the editable instance, and it is not checked here: the function's
		// own declaration reads that way, and so does the tab-gated refresh inside
		// makeImplementBadges. The Modeler draws its captions live instead of once —
		// TestModelerKeepsDataStateCaptionsLive is what holds that end up. Every
		// read-only view names its instance something else (`viewer`, `v`).
		if m[1] == "modeler" || strings.Contains(line, "function drawImplBadges") {
			continue
		}
		badgeSites++
		// The state caption is drawn beside the badges, so it is looked for in the
		// window right after them — close enough that the two read as one step, wide
		// enough to allow a comment line between.
		window := strings.Join(lines[i:min(i+4, len(lines))], "\n")
		if !strings.Contains(window, "drawDataStateLabels("+m[1]+")") {
			missing = append(missing, "editor.js:"+itoa(i+1)+": "+strings.TrimSpace(line))
		}
	}

	if badgeSites == 0 {
		t.Fatal("no drawImplBadges call found in editor.js; the per-view decoration wiring must have been renamed")
	}
	if len(missing) > 0 {
		t.Errorf("%d view(s) draw the type badges but not the data-state captions:\n  %s\n\n"+
			"Every bpmn-js instance in editor.js draws the same diagram, and a data object's\n"+
			"[state] is part of what that diagram says (ADR-0053/0259). A view without the call\n"+
			"renders states as if the model had none. Add drawDataStateLabels(<instance>) beside\n"+
			"drawImplBadges(<instance>) — or makeDataStateLabels for a view that can be edited.",
			len(missing), strings.Join(missing, "\n  "))
	}
}

// TestModelerKeepsDataStateCaptionsLive guards the Modeler's own wiring, which differs
// from the read-only views: there the diagram changes under the caption — a state typed
// in the panel, an association drawn (which is what decides whether a state executes at
// all), a label dragged elsewhere — so the Modeler subscribes rather than drawing once.
func TestModelerKeepsDataStateCaptionsLive(t *testing.T) {
	src := modelerSource(t)
	if !strings.Contains(src, "makeDataStateLabels(modeler)") {
		t.Error("the Modeler does not call makeDataStateLabels(modeler); a data state typed in the\n" +
			"properties panel would be stored but never drawn until the diagram is re-opened")
	}
	// The subscriptions are what "live" means. Without them the factory is a one-shot
	// draw with extra steps, and the failure is silent in exactly the same way.
	fn := between(src, "function makeDataStateLabels(", "\n}\n")
	for _, ev := range []string{"element.changed", "elements.changed", "import.done"} {
		if !strings.Contains(fn, ev) {
			t.Errorf("makeDataStateLabels does not refresh on %q, so the caption goes stale on that event", ev)
		}
	}
}

// between returns the slice of src from the first occurrence of start to the first
// occurrence of end after it, empty if either is missing.
func between(src, start, end string) string {
	i := strings.Index(src, start)
	if i < 0 {
		return ""
	}
	rest := src[i:]
	j := strings.Index(rest, end)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// itoa keeps the error message readable without pulling strconv in for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
