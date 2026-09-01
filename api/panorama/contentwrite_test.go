package panorama

import (
	"strconv"
	"strings"
	"testing"
)

// prefixedModel writes its ArchiMate in a namespace prefix and indents two spaces
// per level — an ordinary document, and one that catches a writer that assumes the
// default namespace or invents its own formatting.
const prefixedModel = `<?xml version="1.0" encoding="UTF-8"?>
<am:model xmlns:am="http://www.opengroup.org/xsd/archimate/3.0/"
          xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m1">
  <am:name xml:lang="en">Landscape</am:name>
  <am:elements>
    <am:element identifier="app-1" xsi:type="ApplicationComponent">
      <am:name xml:lang="en">Orders</am:name>
    </am:element>
    <am:element identifier="svc-1" xsi:type="ApplicationService">
      <am:name xml:lang="en">Order API</am:name>
    </am:element>
  </am:elements>
  <am:relationships>
    <am:relationship identifier="rel-existing" source="app-1" target="svc-1" xsi:type="Realization"/>
  </am:relationships>
  <am:views><am:diagrams>
    <am:view identifier="v1" xsi:type="Diagram">
      <am:name>Cooperation</am:name>
      <am:node identifier="n-app" elementRef="app-1" x="10" y="20" w="160" h="70"/>
      <am:node identifier="n-svc" elementRef="svc-1" x="300" y="20" w="160" h="70"/>
    </am:view>
  </am:diagrams></am:views>
</am:model>`

// TestAddElementWritesInTheDocumentsOwnDialect. A model written as `<am:element>`
// must gain an `<am:element>` — an unprefixed one would be in no namespace and
// would not be the same document — and the new line must be indented like the ones
// around it rather than like a diff somebody will reformat.
func TestAddElementWritesInTheDocumentsOwnDialect(t *testing.T) {
	out, nodeID, err := AddElement([]byte(prefixedModel), NewElement{
		Type: "Node", Name: "Production host", ViewID: "v1", X: 40, Y: 200, W: 180, H: 80,
	})
	if err != nil {
		t.Fatalf("AddElement: %v", err)
	}
	got := string(out)

	if !strings.Contains(got, `<am:element identifier="element-1" xsi:type="Node"><am:name>Production host</am:name></am:element>`) {
		t.Errorf("the element was not written in the document's prefix:\n%s", got)
	}
	if !strings.Contains(got, `<am:node identifier="`+nodeID+`" elementRef="element-1" x="40" y="200" w="180" h="80"/>`) {
		t.Errorf("the shape was not placed on the view:\n%s", got)
	}
	// Indented like its neighbours: four spaces inside <am:elements>, six inside the
	// view. A writer that inserted at column zero would produce a document nobody
	// wants to read the diff of.
	if !strings.Contains(got, "\n    <am:element identifier=\"element-1\"") {
		t.Error("the new element does not match the indentation around it")
	}
	if !strings.Contains(got, "\n      <am:node identifier=\""+nodeID) {
		t.Error("the new shape does not match the indentation around it")
	}
	// And everything that was there is still there.
	for _, keep := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<am:element identifier="app-1" xsi:type="ApplicationComponent">`,
		`<am:node identifier="n-svc" elementRef="svc-1" x="300" y="20" w="160" h="70"/>`,
		`<am:relationship identifier="rel-existing" source="app-1" target="svc-1" xsi:type="Realization"/>`,
	} {
		if !strings.Contains(got, keep) {
			t.Errorf("the writer did not preserve %q", keep)
		}
	}
	// The document still parses, and now holds one more element and one more shape.
	set, err := ExtractBindings(out)
	if err != nil {
		t.Fatalf("the result does not parse: %v", err)
	}
	_ = set
	if validation := Validate(out); !validation.Valid {
		t.Errorf("the result does not validate: %+v", validation.Problems)
	}
}

// TestAddElementMintsIdsNothingElseUses. Two adds in a row must not produce the
// same id, and neither may collide with an identifier the document already has —
// a duplicate identifier is a document that no longer says what it meant.
func TestAddElementMintsIdsNothingElseUses(t *testing.T) {
	document := []byte(prefixedModel)
	seen := map[string]bool{"app-1": true, "svc-1": true, "n-app": true, "n-svc": true, "v1": true}
	for i := range 5 {
		out, nodeID, err := AddElement(document, NewElement{
			Type: "Device", Name: "Host", ViewID: "v1", X: i * 10, Y: 0, W: 100, H: 50,
		})
		if err != nil {
			t.Fatalf("AddElement %d: %v", i, err)
		}
		if seen[nodeID] {
			t.Fatalf("add %d reused the identifier %q", i, nodeID)
		}
		seen[nodeID] = true
		document = out
	}
	// Five adds, five elements, five shapes — and the document still validates.
	if got := strings.Count(string(document), `xsi:type="Device"`); got != 5 {
		t.Errorf("the document holds %d new elements, want 5", got)
	}
	if validation := Validate(document); !validation.Valid {
		t.Errorf("five adds produced an invalid document: %+v", validation.Problems)
	}
}

// TestAddElementRefusesWhatItCannotAuthor. The writer will not create what the
// canvas has no rules for: an element with no relationship rules is one nothing can
// legitimately be connected to.
func TestAddElementRefusesWhatItCannotAuthor(t *testing.T) {
	for name, add := range map[string]NewElement{
		"outside the subset": {Type: "Goal", Name: "Grow", ViewID: "v1", W: 10, H: 10},
		"no name":            {Type: "Node", Name: "  ", ViewID: "v1", W: 10, H: 10},
		"no size":            {Type: "Node", Name: "Host", ViewID: "v1", W: 0, H: 10},
		"no such view":       {Type: "Node", Name: "Host", ViewID: "v-gone", W: 10, H: 10},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := AddElement([]byte(prefixedModel), add); err == nil {
				t.Error("the writer accepted something it cannot honestly create")
			}
		})
	}

	// The refusal for an unauthored type names what *can* be created, so somebody is
	// told what to do rather than only what they may not.
	_, _, err := AddElement([]byte(prefixedModel), NewElement{
		Type: "Goal", Name: "Grow", ViewID: "v1", W: 10, H: 10,
	})
	if !strings.Contains(err.Error(), "ApplicationComponent") {
		t.Errorf("err = %v, want it to list the subset", err)
	}
}

// TestAddRelationshipAsksTheSameTableTheCanvasAsked. The subset decides whether a
// relationship may exist, through the same function the canvas called before it let
// the arrow be drawn — so a connection the canvas offered is one this accepts, and
// one it refused never reaches here.
func TestAddRelationshipAsksTheSameTableTheCanvasAsked(t *testing.T) {
	out, id, err := AddRelationship([]byte(prefixedModel), NewRelationship{
		Type: "Serving", Source: "svc-1", Target: "app-1", ViewID: "v1",
	})
	if err != nil {
		t.Fatalf("AddRelationship: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, `<am:relationship identifier="`+id+`" source="svc-1" target="app-1" xsi:type="Serving"/>`) {
		t.Errorf("the relationship was not written:\n%s", got)
	}
	// Drawn on the view too, between the *nodes* that stand for those elements —
	// a connection references shapes, not elements.
	if !strings.Contains(got, `relationshipRef="`+id+`" source="n-svc" target="n-app"`) {
		t.Errorf("the connection does not join the two shapes:\n%s", got)
	}
	if validation := Validate(out); !validation.Valid {
		t.Errorf("the result does not validate: %+v", validation.Problems)
	}

	// And a relationship the notation forbids is refused with the notation's reason,
	// not a generic failure.
	_, _, err = AddRelationship([]byte(prefixedModel), NewRelationship{
		Type: "Access", Source: "app-1", Target: "svc-1", ViewID: "v1",
	})
	if err == nil {
		t.Fatal("a relationship outside the matrix was written")
	}
	if !strings.Contains(err.Error(), "access runs from behaviour") {
		t.Errorf("err = %v, want the matrix's own explanation", err)
	}
}

// TestAddRelationshipNeedsBothEndsOnTheView. A connection between shapes that are
// not drawn here is a line from nowhere to nowhere: the elements exist, but this
// view does not show them.
func TestAddRelationshipNeedsBothEndsOnTheView(t *testing.T) {
	// A second view that draws only one of the two elements.
	document := strings.Replace(prefixedModel,
		`  </am:diagrams></am:views>`,
		`    <am:view identifier="v2" xsi:type="Diagram">
      <am:name>Partial</am:name>
      <am:node identifier="n2-app" elementRef="app-1" x="10" y="10" w="100" h="50"/>
    </am:view>
  </am:diagrams></am:views>`, 1)
	document = strings.Replace(document, "</am:diagrams></am:views>", "</am:diagrams></am:views>", 1)

	_, _, err := AddRelationship([]byte(document), NewRelationship{
		Type: "Serving", Source: "svc-1", Target: "app-1", ViewID: "v2",
	})
	if err == nil {
		t.Fatal("a connection was drawn to a shape that is not on the view")
	}
	if !strings.Contains(err.Error(), "not on this view") {
		t.Errorf("err = %v, want it to say which side is missing", err)
	}
}

// TestAddRelationshipRefusesUnknownEnds, rather than writing a relationship that
// points at nothing — which the parser would then report as a broken document
// somebody else has to diagnose.
func TestAddRelationshipRefusesUnknownEnds(t *testing.T) {
	for name, add := range map[string]NewRelationship{
		"unknown source": {Type: "Serving", Source: "gone", Target: "app-1", ViewID: "v1"},
		"unknown target": {Type: "Serving", Source: "svc-1", Target: "gone", ViewID: "v1"},
		"unknown view":   {Type: "Serving", Source: "svc-1", Target: "app-1", ViewID: "v-gone"},
		"to itself":      {Type: "Serving", Source: "app-1", Target: "app-1", ViewID: "v1"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := AddRelationship([]byte(prefixedModel), add); err == nil {
				t.Error("the writer accepted an end it cannot resolve")
			}
		})
	}
}

// TestAddingToADocumentWithNoPrefix. The default-namespace case is the common one,
// and a writer that only handled prefixes would write `<:element>`.
func TestAddingToADocumentWithNoPrefix(t *testing.T) {
	out, _, err := AddElement([]byte(viewModelXML), NewElement{
		Type: "ApplicationService", Name: "Billing API", ViewID: "v1", X: 5, Y: 5, W: 120, H: 60,
	})
	if err != nil {
		t.Fatalf("AddElement: %v", err)
	}
	got := string(out)
	if strings.Contains(got, "<:") {
		t.Errorf("the writer invented a prefix:\n%s", got)
	}
	if !strings.Contains(got, `<element identifier="element-1" xsi:type="ApplicationService">`) {
		t.Errorf("the element was not written:\n%s", got)
	}
	if validation := Validate(out); !validation.Valid {
		t.Errorf("the result does not validate: %+v", validation.Problems)
	}
}

// TestAddingRefusesADocumentItCannotRead, rather than returning it unchanged and
// reporting success — which would tell somebody their element was created.
func TestAddingRefusesADocumentItCannotRead(t *testing.T) {
	if _, _, err := AddElement([]byte("<model"), NewElement{
		Type: "Node", Name: "Host", ViewID: "v1", W: 10, H: 10,
	}); err == nil {
		t.Error("a malformed document was accepted")
	}
	if _, _, err := AddRelationship([]byte("<model"), NewRelationship{
		Type: "Serving", Source: "a", Target: "b", ViewID: "v1",
	}); err == nil {
		t.Error("a malformed document was accepted")
	}
}

// TestAddingRefusesADocumentWithNowhereToPutIt. A model with no <elements> or
// <relationships> block is one this writer cannot extend without authoring a block
// somebody did not have — so it says so instead.
func TestAddingRefusesADocumentWithNowhereToPutIt(t *testing.T) {
	bare := `<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/" identifier="m">
  <views><diagrams><view identifier="v1"><name>Empty</name></view></diagrams></views>
</model>`
	if _, _, err := AddElement([]byte(bare), NewElement{
		Type: "Node", Name: "Host", ViewID: "v1", W: 10, H: 10,
	}); err == nil {
		t.Error("an element was added to a document with no elements block")
	}
}

// TestAddingIntoEmptyAndUnusualBlocks. A document whose blocks are empty, or
// written inline, or in a prefix — the writer has to place a child where an author
// would have, without a previous sibling to copy the formatting from.
func TestAddingIntoEmptyAndUnusualBlocks(t *testing.T) {
	empty := `<?xml version="1.0"?>
<am:model xmlns:am="http://www.opengroup.org/xsd/archimate/3.0/"
          xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m">
  <am:name>Fresh</am:name>
  <am:elements>
  </am:elements>
  <am:relationships>
  </am:relationships>
  <am:views><am:diagrams>
    <am:view identifier="v1" xsi:type="Diagram"><am:name>Blank</am:name>
    </am:view>
  </am:diagrams></am:views>
</am:model>`

	out, _, err := AddElement([]byte(empty), NewElement{
		Type: "ApplicationComponent", Name: "First", ViewID: "v1", X: 0, Y: 0, W: 100, H: 50,
	})
	if err != nil {
		t.Fatalf("AddElement into empty blocks: %v", err)
	}
	got := string(out)
	// The prefix comes off the root, which is the one tag always there — reading it
	// off <elements> would leave an empty block with no prefix to copy.
	if !strings.Contains(got, `<am:element identifier="element-1"`) {
		t.Errorf("the prefix was lost on an empty block:\n%s", got)
	}
	if validation := Validate(out); !validation.Valid {
		t.Errorf("adding into empty blocks produced an invalid document: %+v", validation.Problems)
	}

	// A second element, then a relationship between them, into the same empty
	// document — the whole first-use path.
	out, _, err = AddElement(out, NewElement{
		Type: "ApplicationService", Name: "Second", ViewID: "v1", X: 200, Y: 0, W: 100, H: 50,
	})
	if err != nil {
		t.Fatalf("second AddElement: %v", err)
	}
	out, _, err = AddRelationship(out, NewRelationship{
		Type: "Realization", Source: "element-1", Target: "element-2", ViewID: "v1",
	})
	if err != nil {
		t.Fatalf("AddRelationship into an empty block: %v", err)
	}
	if !strings.Contains(string(out), `<am:relationship identifier="rel-1"`) {
		t.Errorf("the relationship was not written:\n%s", out)
	}
	if validation := Validate(out); !validation.Valid {
		t.Errorf("the finished document does not validate: %+v", validation.Problems)
	}
}

// TestAddRelationshipNeedsSomewhereToPutIt and needs *both* ends drawn. The target
// side is checked separately from the source: a rule that only looked at one would
// let a line be drawn to a shape that is not there.
func TestAddRelationshipNeedsSomewhereToPutIt(t *testing.T) {
	// Two views, the second drawing only the source.
	partial := strings.Replace(prefixedModel, `    </am:view>
  </am:diagrams></am:views>`, `    </am:view>
    <am:view identifier="v2" xsi:type="Diagram">
      <am:name>Partial</am:name>
      <am:node identifier="n2-svc" elementRef="svc-1" x="10" y="10" w="100" h="50"/>
    </am:view>
  </am:diagrams></am:views>`, 1)
	if !strings.Contains(partial, "v2") {
		t.Fatal("the fixture did not gain a second view")
	}
	_, _, err := AddRelationship([]byte(partial), NewRelationship{
		Type: "Serving", Source: "svc-1", Target: "app-1", ViewID: "v2",
	})
	if err == nil || !strings.Contains(err.Error(), `"app-1" is not on this view`) {
		t.Errorf("err = %v, want the missing target named", err)
	}

	// And a document with no relationships block at all.
	noBlock := `<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/"
       xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m">
  <elements>
    <element identifier="a" xsi:type="ApplicationComponent"><name>A</name></element>
    <element identifier="b" xsi:type="ApplicationService"><name>B</name></element>
  </elements>
  <views><diagrams><view identifier="v1" xsi:type="Diagram"><name>V</name>
    <node identifier="na" elementRef="a" x="0" y="0" w="10" h="10"/>
    <node identifier="nb" elementRef="b" x="20" y="0" w="10" h="10"/>
  </view></diagrams></views>
</model>`
	_, _, err = AddRelationship([]byte(noBlock), NewRelationship{
		Type: "Realization", Source: "a", Target: "b", ViewID: "v1",
	})
	if err == nil || !strings.Contains(err.Error(), "relationships") {
		t.Errorf("err = %v, want it to say there is nowhere to put it", err)
	}
}

// TestPrefixAtReadsOnlyATag. It works on raw bytes, so it has to say "no prefix"
// for everything that is not a tag rather than returning whatever it found.
func TestPrefixAtReadsOnlyATag(t *testing.T) {
	for input, want := range map[string]string{
		`<am:elements>`:    "am:",
		`</am:elements>`:   "am:",
		`<elements>`:       "",
		`</elements>`:      "",
		`<am:node x="1"/>`: "am:",
		`<`:                "",
		``:                 "",
		`text`:             "",
		`<unterminated`:    "",
	} {
		if got := prefixAt([]byte(input), 0); got != want {
			t.Errorf("prefixAt(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestMintIDFailsRatherThanGuessing. Past its bound it must not return an id it
// cannot prove is free. An earlier version derived one and called it collision-free
// by construction, which was untrue — and a duplicate identifier is a document that
// no longer says what it meant, discovered long after the save that caused it.
func TestMintIDFailsRatherThanGuessing(t *testing.T) {
	taken := map[string]bool{}
	for i := 1; i <= maxMintAttempts; i++ {
		taken["x-"+strconv.Itoa(i)] = true
	}
	got, err := mintID(taken, "x")
	if err == nil {
		t.Fatalf("past the bound mintID returned %q rather than failing", got)
	}
	if !strings.Contains(err.Error(), "no free one") {
		t.Errorf("err = %v, want it to say why", err)
	}

	// Below the bound it hands out the first free number and remembers it, so two
	// inserts in one call cannot collide with each other.
	fresh := map[string]bool{"y-1": true}
	first, err := mintID(fresh, "y")
	if err != nil || first != "y-2" {
		t.Fatalf("mintID = %q, %v; want the first free number", first, err)
	}
	second, _ := mintID(fresh, "y")
	if second == first {
		t.Errorf("two mints in one call both returned %q", first)
	}
}

// TestAddingFailsWhenNoIdentifierCanBeMinted, rather than writing a duplicate. A
// document this crowded is pathological, but the failure it would otherwise cause
// — two elements sharing an identifier — is silent, permanent, and discovered
// somewhere else entirely.
func TestAddingFailsWhenNoIdentifierCanBeMinted(t *testing.T) {
	// A document whose element identifiers occupy every name the minter would try.
	var elements, nodes strings.Builder
	for i := 1; i <= maxMintAttempts; i++ {
		n := strconv.Itoa(i)
		elements.WriteString(`<element identifier="element-` + n + `" xsi:type="Node"><name>N` + n + `</name></element>`)
	}
	nodes.WriteString(`<node identifier="n-a" elementRef="element-1" x="0" y="0" w="10" h="10"/>`)
	crowded := `<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/"
       xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m">
  <elements>` + elements.String() + `</elements>
  <relationships></relationships>
  <views><diagrams><view identifier="v1" xsi:type="Diagram"><name>V</name>` +
		nodes.String() + `</view></diagrams></views>
</model>`

	_, _, err := AddElement([]byte(crowded), NewElement{
		Type: "Node", Name: "One more", ViewID: "v1", X: 0, Y: 0, W: 10, H: 10,
	})
	if err == nil {
		t.Fatal("an element was added with no free identifier to give it")
	}
	if !strings.Contains(err.Error(), "no free one") {
		t.Errorf("err = %v, want it to say why", err)
	}

	// The same guard on the relationship path, where the crowding is on "rel".
	var rels strings.Builder
	for i := 1; i <= maxMintAttempts; i++ {
		n := strconv.Itoa(i)
		rels.WriteString(`<relationship identifier="rel-` + n + `" source="a" target="b" xsi:type="Association"/>`)
	}
	crowdedRels := `<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/"
       xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="m">
  <elements>
    <element identifier="a" xsi:type="ApplicationComponent"><name>A</name></element>
    <element identifier="b" xsi:type="ApplicationService"><name>B</name></element>
  </elements>
  <relationships>` + rels.String() + `</relationships>
  <views><diagrams><view identifier="v1" xsi:type="Diagram"><name>V</name>
    <node identifier="na" elementRef="a" x="0" y="0" w="10" h="10"/>
    <node identifier="nb" elementRef="b" x="20" y="0" w="10" h="10"/>
  </view></diagrams></views>
</model>`
	if _, _, err := AddRelationship([]byte(crowdedRels), NewRelationship{
		Type: "Realization", Source: "a", Target: "b", ViewID: "v1",
	}); err == nil {
		t.Fatal("a relationship was added with no free identifier to give it")
	}
}
