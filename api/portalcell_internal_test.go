package api

import (
	"strings"
	"testing"
)

// A row's name is not squeezed out by what the row says about itself.
//
// The basket's optional column drew rows as "Schutzhü / lle / transpare / nt" — one
// fragment per line, a few characters wide, beside a price and a level name that
// had the width. Two decisions produced it together, and each is enough on its own:
//
//   - the price and the level were built into the row's *trail*, which carries the
//     controls and therefore promises never to give width back;
//   - the name was set to `overflow-wrap:anywhere`, which lets a box shrink below
//     its longest word — so there was no floor under it to stop at.
//
// The rule these hold is one sentence: lead and trail carry controls, meta carries
// text, and text shrinks.

// TestTheNameHasAFloorUnderIt.
func TestTheNameHasAFloorUnderIt(t *testing.T) {
	css := webRegion(t, readWeb(t, "portal.html"), ".cell .label {", "}")
	if strings.Contains(css, "anywhere") {
		t.Error("the row's name may shrink below its longest word, so any sibling that " +
			"refuses to shrink takes the row and the name wraps one letter per line")
	}
	if !strings.Contains(css, "break-word") {
		t.Error("the row's name has no wrapping rule at all, so a long product name " +
			"pushes its own column wider than the cascade")
	}
}

// TestWhatARowSaysAboutItselfIsNotAControl.
//
// Read over every trail in the file rather than over the three that carried text,
// because the next caller is the one that does it again — and the failure is not an
// error anybody sees in a test run, it is a column somebody reads.
func TestWhatARowSaysAboutItselfIsNotAControl(t *testing.T) {
	src := readWeb(t, "portal.js")
	trails := trailArguments(src)
	if len(trails) < 5 {
		t.Fatalf("found %d trails in portal.js; this guard has lost its subject", len(trails))
	}
	for _, trail := range trails {
		// A muted span is this page's way of writing "this is a note, not a
		// control", which is exactly what must not ride in the trail.
		if strings.Contains(trail, "'muted'") {
			t.Errorf("a trail carries descriptive text: %s\n"+
				"The trail is the row's controls, and a control does not give width "+
				"back — text beside one starves the name. It belongs in meta.",
				strings.TrimSpace(trail))
		}
	}
}

// TestTheRowHasSomewhereToPutIt.
//
// The other half: moving text out of the trail is only an improvement if there is a
// slot that renders it. Without this, "not in the trail" is satisfied by deleting
// the price.
func TestTheRowHasSomewhereToPutIt(t *testing.T) {
	body := webRegion(t, readWeb(t, "portal.js"), "function cell(", "\n}")
	if !strings.Contains(body, "o.meta") {
		t.Fatal("a row has no slot for what it says about itself, so a price or a " +
			"level has nowhere to go but the controls")
	}
	if !strings.Contains(readWeb(t, "portal.html"), ".cell .meta {") {
		t.Error("the meta slot is rendered and unstyled, so it inherits the row's own " +
			"size and reads as a second name")
	}
	// And the two rows that carry text actually use it, each named by the region it
	// is drawn in rather than by a count: a count says nothing about *which* caller
	// stopped, and a column that loses its text legitimately — as the first one did
	// when it stopped holding products — would fail a count for being correct.
	src := readWeb(t, "portal.js")
	for _, view := range []struct{ name, from, to string }{
		{"a search result, which says where it found the product", "function renderSearch(", "\n}"},
		{"the basket's optional column, which says a price and a level", "function renderBasket(", "\n}"},
	} {
		if !strings.Contains(webRegion(t, src, view.from, view.to), "meta:") {
			t.Errorf("%s puts its text somewhere other than meta, which leaves only the "+
				"controls to put it in", view.name)
		}
	}
}

// trailArguments returns the source of every `trail:` argument in the file, with
// brackets balanced — a trail is as often a list of four controls as a single one,
// and reading to the end of the line would see neither.
func trailArguments(src string) []string {
	var out []string
	for i := 0; ; {
		at := strings.Index(src[i:], "trail:")
		if at < 0 {
			return out
		}
		start := i + at + len("trail:")
		depth, end := 0, -1
		for j := start; j < len(src); j++ {
			switch src[j] {
			case '[', '(', '{':
				depth++
			case ']', ')', '}':
				if depth == 0 {
					end = j // the object's own closing brace: the trail was the last field
					break
				}
				depth--
			case ',':
				if depth == 0 {
					end = j
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			return append(out, src[start:])
		}
		out = append(out, src[start:end])
		i = end
	}
}
