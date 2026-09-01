package panorama

import (
	"bytes"
	"strings"
	"testing"
)

// layoutModel is a document with everything a byte-level writer could damage: a
// comment, a processing instruction, an unusual namespace prefix, attributes in an
// order no serialiser would choose, single quotes, extra whitespace inside a tag,
// a nested node, and an element Panorama has no reason to understand.
const layoutModel = `<?xml version="1.0" encoding="UTF-8"?>
<?atlas-pipeline stage="review"?>
<!-- Authored by hand. Do not reformat. -->
<am:model xmlns:am="http://www.opengroup.org/xsd/archimate/3.0/"
          xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m1">
  <am:name xml:lang="en">Hand written</am:name>
  <am:elements>
    <am:element identifier="app-1" xsi:type="ApplicationComponent">
      <am:name xml:lang="en">Order Service</am:name>
    </am:element>
    <am:element identifier="node-1" xsi:type="Node"><am:name>Host</am:name></am:element>
  </am:elements>
  <am:someFutureThing weight="3">kept exactly</am:someFutureThing>
  <am:views><am:diagrams>
    <am:view identifier="v1" xsi:type="Diagram">
      <am:name>Application cooperation</am:name>
      <am:node   w="190"  identifier="n-app"   x='80' y="100"    h="80" elementRef="app-1">
        <am:node identifier="n-inner" x="10" y="12" w="40" h="20" elementRef="node-1"/>
      </am:node>
    </am:view>
  </am:diagrams></am:views>
</am:model>`

func TestSetLayoutWritesOnlyTheGeometry(t *testing.T) {
	out, err := SetLayout([]byte(layoutModel), []LayoutChange{
		{NodeID: "n-app", X: 300, Y: 240, W: 220, H: 96},
	})
	if err != nil {
		t.Fatalf("SetLayout: %v", err)
	}
	got := string(out)

	// The geometry moved, and the attributes kept the order and the quoting the
	// author used — the single-quoted x is still single-quoted, and the odd spacing
	// inside the tag survives.
	want := `<am:node   w="220"  identifier="n-app"   x='300' y="240"    h="96" elementRef="app-1">`
	if !strings.Contains(got, want) {
		t.Errorf("the start tag was rewritten rather than spliced:\n%s", tagLine(got, "n-app"))
	}

	// Everything a decode/encode cycle would have normalised or dropped is still
	// there, byte for byte. This is ADR-0189 §2's requirement, and it is the whole
	// reason this writer splices instead of re-serialising.
	for _, keep := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<?atlas-pipeline stage="review"?>`,
		`<!-- Authored by hand. Do not reformat. -->`,
		`<am:someFutureThing weight="3">kept exactly</am:someFutureThing>`,
		`<am:element identifier="node-1" xsi:type="Node"><am:name>Host</am:name></am:element>`,
		`<am:node identifier="n-inner" x="10" y="12" w="40" h="20" elementRef="node-1"/>`,
	} {
		if !strings.Contains(got, keep) {
			t.Errorf("the writer did not preserve %q", keep)
		}
	}

	// And nothing else moved. Blanking every geometry value in both documents leaves
	// two byte-identical remainders, which says the splice touched those values and
	// nothing else — without a diff heuristic standing between the claim and the
	// check.
	if before, after := blankGeometry(t, []byte(layoutModel)), blankGeometry(t, out); !bytes.Equal(before, after) {
		t.Errorf("bytes outside the geometry changed:\nbefore: %s\nafter:  %s", before, after)
	}
}

// TestSetLayoutMovesEachPlacementOnItsOwn. A view may place the same element more
// than once, so the writer is keyed by the *view node*, not by the element it
// references — otherwise moving one box would move its twin somewhere else on the
// canvas.
func TestSetLayoutMovesEachPlacementOnItsOwn(t *testing.T) {
	out, err := SetLayout([]byte(layoutModel), []LayoutChange{
		{NodeID: "n-inner", X: 1, Y: 2, W: 3, H: 4},
	})
	if err != nil {
		t.Fatalf("SetLayout: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `<am:node identifier="n-inner" x="1" y="2" w="3" h="4" elementRef="node-1"/>`) {
		t.Errorf("the nested node did not move: %s", tagLine(got, "n-inner"))
	}
	// Its parent, which references a different element, is untouched.
	if !strings.Contains(got, `w="190"`) || !strings.Contains(got, `x='80'`) {
		t.Errorf("moving a nested node moved its parent: %s", tagLine(got, "n-app"))
	}
}

// TestSetLayoutRefusesWhatItCannotWrite. The canvas can only move what it drew, so
// an unknown id means the document and the canvas have disagreed about what is in
// the view. Dropping it quietly would save a layout that is not the one on screen.
func TestSetLayoutRefusesWhatItCannotWrite(t *testing.T) {
	for name, change := range map[string]LayoutChange{
		"unknown node":  {NodeID: "n-ghost", X: 1, Y: 1, W: 10, H: 10},
		"zero width":    {NodeID: "n-app", X: 1, Y: 1, W: 0, H: 10},
		"zero height":   {NodeID: "n-app", X: 1, Y: 1, W: 10, H: 0},
		"negative size": {NodeID: "n-app", X: 1, Y: 1, W: -5, H: 10},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := SetLayout([]byte(layoutModel), []LayoutChange{change}); err == nil {
				t.Error("the writer accepted a change it cannot honestly make")
			}
		})
	}

	// A refusal writes nothing at all: one bad change in a batch must not leave the
	// document half-moved.
	_, err := SetLayout([]byte(layoutModel), []LayoutChange{
		{NodeID: "n-app", X: 9, Y: 9, W: 9, H: 9},
		{NodeID: "n-ghost", X: 1, Y: 1, W: 1, H: 1},
	})
	if err == nil {
		t.Fatal("a batch with an unknown node was accepted")
	}
}

// TestSetLayoutIsBounded. A person dragging boxes produces a handful of changes; a
// request claiming thousands is not a canvas.
func TestSetLayoutIsBounded(t *testing.T) {
	many := make([]LayoutChange, maxLayoutChanges+1)
	for i := range many {
		many[i] = LayoutChange{NodeID: "n-app", X: 1, Y: 1, W: 1, H: 1}
	}
	if _, err := SetLayout([]byte(layoutModel), many); err == nil {
		t.Error("an unbounded batch was accepted")
	}
}

// TestSetLayoutOfNothingChangesNothing. Saving a view nobody moved must produce the
// same bytes, or every open-and-close would rewrite the document.
func TestSetLayoutOfNothingChangesNothing(t *testing.T) {
	out, err := SetLayout([]byte(layoutModel), nil)
	if err != nil {
		t.Fatalf("SetLayout: %v", err)
	}
	if !bytes.Equal(out, []byte(layoutModel)) {
		t.Error("saving no changes rewrote the document")
	}
}

// TestSetLayoutRejectsADocumentItCannotRead, rather than returning the input
// unchanged and reporting success — which would tell somebody their move was saved.
func TestSetLayoutRejectsADocumentItCannotRead(t *testing.T) {
	if _, err := SetLayout([]byte("<model"), []LayoutChange{{NodeID: "n", W: 1, H: 1}}); err == nil {
		t.Error("a malformed document was accepted")
	}
}

// TestSetLayoutIgnoresNodesOutsideAView. `node` is an ArchiMate element type as
// well as a diagram shape. Only the diagram shape carries geometry this writer
// owns, and confusing the two would put x/y on a semantic element.
func TestSetLayoutIgnoresNodesOutsideAView(t *testing.T) {
	spans, err := indexViewNodes([]byte(layoutModel))
	if err != nil {
		t.Fatalf("indexViewNodes: %v", err)
	}
	if _, found := spans["node-1"]; found {
		t.Error("a semantic element was indexed as a diagram node")
	}
	for _, want := range []string{"n-app", "n-inner"} {
		if _, found := spans[want]; !found {
			t.Errorf("the diagram node %q was not indexed", want)
		}
	}
}

// tagLine returns the line carrying an identifier, for a readable failure.
func tagLine(document, id string) string {
	for _, line := range strings.Split(document, "\n") {
		if strings.Contains(line, `"`+id+`"`) {
			return strings.TrimSpace(line)
		}
	}
	return "(not found)"
}

// blankGeometry replaces every view node's x/y/w/h value with a fixed placeholder,
// so two documents can be compared for everything *except* the geometry.
func blankGeometry(t *testing.T, data []byte) []byte {
	t.Helper()
	spans, err := indexViewNodes(data)
	if err != nil {
		t.Fatalf("indexViewNodes: %v", err)
	}
	var edits []edit
	for _, span := range spans {
		for _, at := range span.attrs {
			edits = append(edits, edit{from: at.from, to: at.to, text: "#"})
		}
	}
	return applyEdits(data, edits)
}

// TestFindAttrIsNotFooledByLookalikes. This scanner is the part of the writer most
// able to be subtly wrong: it works on raw tag bytes, and a match in the wrong
// place would splice a number into the middle of somebody's document.
func TestFindAttrIsNotFooledByLookalikes(t *testing.T) {
	value := func(tag, name string) string {
		at := findAttr([]byte(tag), name)
		if at.to <= at.from {
			return ""
		}
		return tag[at.from:at.to]
	}

	// A name that is the tail of another attribute must not match: `sw` ends in `w`,
	// and `width` starts with it.
	if got := value(`<node sw="2" width="9" w="42"/>`, "w"); got != "42" {
		t.Errorf("w = %q, want the real attribute rather than a lookalike", got)
	}
	// Whitespace around the equals sign is legal XML.
	if got := value(`<node  x   =   '17' />`, "x"); got != "17" {
		t.Errorf("x = %q, want the value across the spacing", got)
	}
	// An attribute that is not there is reported as absent, not as empty.
	if at := findAttr([]byte(`<node x="1"/>`), "h"); at.to > at.from {
		t.Error("a missing attribute was reported as present")
	}
	// A name with no equals sign after it is not an attribute.
	if at := findAttr([]byte(`<node w />`), "w"); at.to > at.from {
		t.Error("a bare name was read as an attribute")
	}
	// An unterminated value is not spliceable, and guessing where it ends would
	// corrupt the rest of the document.
	if at := findAttr([]byte(`<node w="42 />`), "w"); at.to > at.from {
		t.Error("an unterminated value was accepted")
	}
	// The first character of a tag is never the start of an attribute name.
	if at := findAttr([]byte(`w="1"`), "w"); at.to > at.from {
		t.Error("a name with no preceding whitespace was matched")
	}
	// An empty value is present and empty, which the writer then overwrites.
	if at := findAttr([]byte(`<node x="" y="3"/>`), "x"); at.to != at.from {
		t.Errorf("an empty value spans %d bytes, want zero", at.to-at.from)
	}
	// An unquoted value is not XML. The document would already have been refused,
	// but this must not go looking for a closing quote that is not there.
	if at := findAttr([]byte(`<node w=42 />`), "w"); at.to > at.from {
		t.Error("an unquoted value was matched")
	}
}

// TestIndexViewNodesSkipsAnUnidentifiedNode. A view node with no identifier is one
// nothing can name, so nothing can ask to move it — and indexing it under the empty
// string would make every such node the same node.
func TestIndexViewNodesSkipsAnUnidentifiedNode(t *testing.T) {
	anonymous := `<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/" identifier="m">
  <views><diagrams><view identifier="v">
    <node x="1" y="2" w="3" h="4" elementRef="e1"/>
    <node identifier="n-real" x="5" y="6" w="7" h="8" elementRef="e2"/>
  </view></diagrams></views>
</model>`
	spans, err := indexViewNodes([]byte(anonymous))
	if err != nil {
		t.Fatalf("indexViewNodes: %v", err)
	}
	if _, found := spans[""]; found {
		t.Error("a node with no identifier was indexed under the empty string")
	}
	if len(spans) != 1 {
		t.Errorf("indexed %d nodes, want only the one that can be named", len(spans))
	}
}

// TestSetLayoutRefusesANodeWithNoGeometry. A view node the schema allows to carry
// no bounds is not one this writer can move, and inventing the attributes would be
// authoring geometry the document never had.
func TestSetLayoutRefusesANodeWithNoGeometry(t *testing.T) {
	bare := `<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/" identifier="m">
  <views><diagrams><view identifier="v">
    <node identifier="n-bare" elementRef="e1"/>
  </view></diagrams></views>
</model>`
	_, err := SetLayout([]byte(bare), []LayoutChange{{NodeID: "n-bare", X: 1, Y: 1, W: 10, H: 10}})
	if err == nil {
		t.Fatal("a node with no geometry attributes was accepted")
	}
	if !strings.Contains(err.Error(), "n-bare") {
		t.Errorf("err = %v, want it to name the node", err)
	}
}
