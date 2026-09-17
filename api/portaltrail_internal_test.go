package api

import (
	"strings"
	"testing"
)

// The icons at the end of a catalogue row stay on one line.
//
// Every one of them refuses to shrink — `.sq` is `flex:0 0 auto` — but they sat
// inside a plain `<span>` that carried no rule at all. That span is a flex item
// with the default `flex: 0 1 auto`, so it may shrink, and its contents are inline
// boxes, so they wrap inside it. The cascade is four columns across; in a narrow
// one the star, the ± and the "i" broke onto a second line and the row read as two.
//
// Fixed in `cell` rather than at the seven call sites, which is the part worth
// guarding: a rule applied per caller is a rule the next caller forgets. The cell
// wraps whatever it is handed, so a trail cannot be built without it.

// TestTheCellWrapsWhateverTrailsIt.
func TestTheCellWrapsWhateverTrailsIt(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "function cell(", "\n}")
	if !strings.Contains(body, "class: 'trail'") {
		t.Error("the cell hands a trail straight through, so whether the icons wrap " +
			"depends on what each of seven callers happened to pass")
	}
}

// TestNoTrailBuildsItsOwnBareSpan.
//
// The failure this prevents is subtle and would look fixed: a caller passing its
// own `<span>` puts that span inside the wrapper as a single flex item, and the
// icons inside *it* are inline again — so they wrap exactly as before, under a
// class that says they do not.
func TestNoTrailBuildsItsOwnBareSpan(t *testing.T) {
	src := readWeb(t, "portal.js")
	if strings.Contains(src, "trail: el('span', {},") {
		t.Error("a trail builds its own bare span, which becomes one flex item inside " +
			"the wrapper and lets its icons wrap again")
	}
}

// TestTheTrailIsAFlexItemThatDoesNotShrink.
func TestTheTrailIsAFlexItemThatDoesNotShrink(t *testing.T) {
	css := readWeb(t, "portal.html")
	rule := ""
	if at := strings.Index(css, ".cell .trail"); at >= 0 {
		if end := strings.Index(css[at:], "}"); end >= 0 {
			rule = css[at : at+end]
		}
	}
	if rule == "" {
		t.Fatal("portal.html declares no .cell .trail rule; the wrapper exists and " +
			"nothing styles it, which is the same wrapping with an extra element")
	}
	for _, want := range []struct{ decl, why string }{
		{"flex:0 0 auto", "the trail may shrink, and a shrunk trail wraps its icons"},
		{"display:flex", "the icons are inline boxes, which is what wraps them"},
	} {
		if !strings.Contains(strings.ReplaceAll(rule, " ", ""),
			strings.ReplaceAll(want.decl, " ", "")) {
			t.Errorf("the trail rule does not say %q: %s", want.decl, want.why)
		}
	}
}
