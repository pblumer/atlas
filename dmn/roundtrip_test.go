package dmn_test

import (
	"bytes"
	"encoding/xml"
	"os"
	"sort"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// A DMN model that goes through Atlas has to come back out as the model that went in.
//
// Atlas rewrites a stored model on the way to the editor: a model with no diagram, or
// with a partial one, has a diagram generated for it (ADR-0325). That rewrite is a
// text splice — the diagram is cut out and a new one spliced in before the closing
// tag — and everything else in the document is carried across untouched, including
// the parts Atlas knows nothing about.
//
// That property is worth holding rather than assuming, because the obvious way to
// write this code is the one that loses data: parse the document into structs,
// change the diagram, marshal it back. Anything the structs do not declare is gone,
// silently, in a file somebody wrote by hand. The tests below say that Atlas does not
// do that, and they say it over a model deliberately carrying more than Atlas
// understands.
//
// e2e/dmn-fidelity.spec.mjs is the other half: what happens to the same model when the
// shipped editor opens it and saves it back.
func kitchenSink(t *testing.T) []byte {
	t.Helper()
	src, err := os.ReadFile("testdata/roundtrip-kitchen-sink.dmn")
	if err != nil {
		t.Fatalf("read the fixture: %v", err)
	}
	return src
}

// factsOf reduces a document to what it says: one fact per element, one per attribute
// with its value. Comparing facts rather than bytes lets the diagram be rewritten
// freely while still catching anything dropped from the rest — and naming it, which a
// byte comparison cannot do.
//
// The subtree named by skip is left out, which is how "everything but the diagram"
// is expressed.
func factsOf(t *testing.T, src []byte, skip string) map[string]int {
	t.Helper()
	facts := map[string]int{}
	dec := xml.NewDecoder(bytes.NewReader(src))
	depth, skipAt := 0, -1
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch e := tok.(type) {
		case xml.StartElement:
			depth++
			if skipAt >= 0 {
				continue
			}
			if e.Name.Local == skip {
				skipAt = depth
				continue
			}
			facts["<"+e.Name.Local+">"]++
			for _, a := range e.Attr {
				// Namespace declarations are how a document is written, not what it
				// says; a splice may legitimately move them.
				if a.Name.Space == "xmlns" || a.Name.Local == "xmlns" {
					continue
				}
				facts[e.Name.Local+"@"+a.Name.Local+"="+a.Value]++
			}
		case xml.EndElement:
			if skipAt == depth {
				skipAt = -1
			}
			depth--
		}
	}
	if len(facts) == 0 {
		t.Fatal("no facts read from the document; this guard would pass vacuously")
	}
	return facts
}

// missing names what the input said and the output does not, so a failure reads as
// the loss it is rather than as a number.
func missing(before, after map[string]int) []string {
	var lost []string
	for fact, n := range before {
		if after[fact] < n {
			lost = append(lost, fact)
		}
	}
	sort.Strings(lost)
	return lost
}

// TestEnsureDiagramLeavesADrawnModelAlone: a model that already carries a complete
// diagram is somebody's arrangement, and Atlas returns it byte for byte. Not "the
// same model" — the same bytes, because the way to be sure nothing was touched is
// that nothing was touched.
func TestEnsureDiagramLeavesADrawnModelAlone(t *testing.T) {
	src := kitchenSink(t)

	got := dmn.EnsureDiagram(src)

	if !bytes.Equal(got, src) {
		t.Errorf("EnsureDiagram rewrote a model that was already drawn (%d bytes in, %d out).\n"+
			"A complete diagram is an arrangement somebody made; completing it again moves it.\n"+
			"lost: %v", len(src), len(got), missing(factsOf(t, src, ""), factsOf(t, got, "")))
	}
}

// TestEnsureDiagramKeepsEverythingButTheDiagram: the model with its diagram taken
// away is the common case — a model written by an agent, by temis or by hand carries
// logic and no picture. Atlas draws one. Everything else it was given has to still be
// there afterwards, down to the extension element in a namespace Atlas has never
// heard of.
func TestEnsureDiagramKeepsEverythingButTheDiagram(t *testing.T) {
	src := withoutDiagram(t, kitchenSink(t))

	got, drew := dmn.EnsureDiagramReport(src)

	if !drew {
		t.Fatal("EnsureDiagramReport drew nothing for a model with no diagram; the rest of this test would prove nothing")
	}
	if !bytes.Contains(got, []byte("DMNDI")) {
		t.Fatal("a diagram was reported drawn but the result carries none")
	}
	if lost := missing(factsOf(t, src, "DMNDI"), factsOf(t, got, "DMNDI")); len(lost) > 0 {
		t.Errorf("drawing a diagram dropped %d thing(s) from the rest of the model:\n  %v\n"+
			"Completing a diagram is a splice: nothing outside it may change.", len(lost), lost)
	}
}

// TestRegenerateDiagramKeepsEverythingButTheDiagram: Auto-layout is the one action
// that discards an arrangement somebody made, because the author asked for it. It
// discards the arrangement, and nothing else.
func TestRegenerateDiagramKeepsEverythingButTheDiagram(t *testing.T) {
	src := kitchenSink(t)

	got := dmn.RegenerateDiagram(src)

	if bytes.Equal(got, src) {
		t.Fatal("RegenerateDiagram returned the model unchanged; it is meant to lay the graph out afresh")
	}
	if lost := missing(factsOf(t, src, "DMNDI"), factsOf(t, got, "DMNDI")); len(lost) > 0 {
		t.Errorf("Auto-layout dropped %d thing(s) from outside the diagram:\n  %v\n"+
			"It may move every shape; it may not touch the model.", len(lost), lost)
	}
}

// withoutDiagram is the fixture as a tool that writes logic and no picture would have
// left it.
func withoutDiagram(t *testing.T, src []byte) []byte {
	t.Helper()
	open := bytes.Index(src, []byte("<dmndi:DMNDI>"))
	close := bytes.Index(src, []byte("</dmndi:DMNDI>"))
	if open < 0 || close < 0 {
		t.Fatal("the fixture carries no diagram to remove")
	}
	out := make([]byte, 0, len(src))
	out = append(out, src[:open]...)
	out = append(out, src[close+len("</dmndi:DMNDI>"):]...)
	return out
}
