package mimimport

import (
	"bytes"
	"encoding/xml"
	"os"
	"strings"
	"testing"
)

// The tests in this file cover the two halves of the migration worksheet: the
// decoded collections as machine-readable extension elements on the node they
// came from, and the per-row entries in the Report. Both state the *structure*
// of a MIMWAL collection — which is mechanical — and neither states what a
// column means, which is not (see tables.go).

// collectionsOf re-parses the generated BPMN and returns every emitted
// <atlas:mimCollection>, so a test asserts against decoded values rather than
// against a substring of the markup.
type xmlCollection struct {
	Property string `xml:"property,attr"`
	Kind     string `xml:"kind,attr"`
	Count    int    `xml:"count,attr"`
	Rows     []struct {
		Index int `xml:"index,attr"`
		Cells []struct {
			Column int    `xml:"column,attr"`
			Text   string `xml:",chardata"`
		} `xml:"mimCell"`
	} `xml:"mimRow"`
	Notes []string `xml:"mimNote"`
}

func collectionsOf(t *testing.T, bpmn []byte) []xmlCollection {
	t.Helper()
	var out []xmlCollection
	dec := xml.NewDecoder(bytes.NewReader(bpmn))
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "mimCollection" {
			continue
		}
		var c xmlCollection
		if err := dec.DecodeElement(&c, &se); err != nil {
			t.Fatalf("mimCollection did not decode: %v", err)
		}
		out = append(out, c)
	}
	return out
}

// TestCollectionsAreEmittedAsExtensionElements is the point of the worksheet:
// the rows a reviewer has to work through are addressable by a tool, not only
// readable in a documentation string.
func TestCollectionsAreEmittedAsExtensionElements(t *testing.T) {
	src, err := os.ReadFile("testdata/mimwal-workflow.xoml")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Convert(bytes.NewReader(src), "")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)

	cols := collectionsOf(t, res.BPMN)
	if len(cols) != 3 {
		t.Fatalf("want the three collections of the workflow, got %d: %+v", len(cols), cols)
	}

	byProp := map[string]xmlCollection{}
	for _, c := range cols {
		byProp[c.Property] = c
	}

	queries, ok := byProp["QueriesTable"]
	if !ok {
		t.Fatalf("QueriesTable was not emitted: %+v", cols)
	}
	if queries.Kind != "table" || queries.Count != 1 || len(queries.Rows) != 1 {
		t.Errorf("QueriesTable = %+v, want one table row", queries)
	}
	if got := queries.Rows[0].Cells[1].Text; got != `/Group[DependsOn='[//Target/ObjectID]']` {
		t.Errorf("query cell 1 = %q", got)
	}

	updates := byProp["UpdatesTable"]
	if updates.Kind != "table" || len(updates.Rows) != 2 {
		t.Fatalf("UpdatesTable = %+v, want two rows", updates)
	}
	// Cells come back in column order however the source listed them, and the
	// row index is MIM's own, not a position in the emitted document.
	if updates.Rows[0].Index != 0 || len(updates.Rows[0].Cells) != 3 {
		t.Errorf("row 0 = %+v, want three cells", updates.Rows[0])
	}
	for i, want := range []string{"[//Queries/AllGroups]", "[//WorkflowData/AllGroups]", "false"} {
		if got := updates.Rows[0].Cells[i].Text; got != want {
			t.Errorf("row 0 cell %d = %q, want %q", i, got, want)
		}
		if got := updates.Rows[0].Cells[i].Column; got != i {
			t.Errorf("row 0 cell %d carries column %d", i, got)
		}
	}

	// An ArrayList is a list, not a table: one row per entry, one cell each.
	values := byProp["ValueExpressions"]
	if values.Kind != "list" || values.Count != 2 || len(values.Rows) != 2 {
		t.Fatalf("ValueExpressions = %+v, want a two-entry list", values)
	}
	if got := values.Rows[1].Cells[0].Text; got != "Left(Trim([//WorkflowData/AccountNameBase]),18)+[//UniquenessKey]" {
		t.Errorf("list entry 1 = %q", got)
	}

	// The structured form is an addition, never a replacement: the verbatim
	// source and the readable table both have to survive it.
	if len(mimSources(t, res.BPMN)) != 3 {
		t.Error("every activity must still carry its original markup")
	}
	if !strings.Contains(string(res.BPMN), "UpdatesTable (2 rows)") {
		t.Errorf("the readable table must still be on the documentation:\n%s", res.BPMN)
	}
}

// TestCellTextIsVerbatim pins the one difference between the two renderings: the
// documentation collapses a cell onto one line so the table stays readable, and
// the machine-readable form must not, because a MIM expression can hold a string
// literal whose spacing is part of its value.
func TestCellTextIsVerbatim(t *testing.T) {
	src := `<SequentialWorkflow><UpdateResources ActivityDisplayName="A">
	  <UpdateResources.UpdatesTable><Hashtable>
	    <String>IIF(X,"In  Grace
	Period","")<x:Key xmlns:x="urn:x"><String>0:0</String></x:Key></String>
	    <String>[//Target/Status]<x:Key xmlns:x="urn:x"><String>0:1</String></x:Key></String>
	  </Hashtable></UpdateResources.UpdatesTable>
	</UpdateResources></SequentialWorkflow>`
	res, err := Convert(strings.NewReader(src), "V")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)

	cols := collectionsOf(t, res.BPMN)
	if len(cols) != 1 || len(cols[0].Rows) != 1 {
		t.Fatalf("want one row in one collection, got %+v", cols)
	}
	const want = "IIF(X,\"In  Grace\n\tPeriod\",\"\")"
	if got := cols[0].Rows[0].Cells[0].Text; got != want {
		t.Errorf("cell text = %q, want %q — the machine-readable form must keep the cell as MIM wrote it", got, want)
	}
	// The documentation keeps its one-line rendering.
	if !strings.Contains(string(res.BPMN), `IIF(X,"In Grace Period","")`) {
		t.Errorf("the documentation table should still collapse the cell onto one line:\n%s", res.BPMN)
	}
}

// TestWorksheetListsEveryRow is the honest-count half: an activity carrying six
// reads and writes is six items to work through, not one preserved node, and the
// report is what a migration is worked off.
func TestWorksheetListsEveryRow(t *testing.T) {
	src, err := os.ReadFile("testdata/mimwal-workflow.xoml")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Convert(bytes.NewReader(src), "")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}

	var rows []Note
	for _, n := range res.Report.Notes {
		if strings.Contains(n.Detail, "row 0 of") || strings.Contains(n.Detail, "row 1 of") ||
			strings.Contains(n.Detail, "entry 0 of") || strings.Contains(n.Detail, "entry 1 of") {
			rows = append(rows, n)
		}
	}
	// One query row, two update rows, two value expressions.
	if len(rows) != 5 {
		t.Fatalf("want an item per decoded row, got %d:\n%s", len(rows), res.Report.String())
	}
	for _, n := range rows {
		if n.Status != StatusManualReview {
			t.Errorf("a decoded row is something a human has to re-express, got status %q: %+v", n.Status, n)
		}
		if n.NodeID == "" {
			t.Errorf("an item must name the node it belongs to: %+v", n)
		}
	}
	if !strings.Contains(res.Report.String(), "UpdatesTable row 0 of 2") {
		t.Errorf("an item must name its collection, its index and the row count:\n%s", res.Report.String())
	}
	if !strings.Contains(res.Report.String(), "[//Queries/AllGroups] | [//WorkflowData/AllGroups] | false") {
		t.Errorf("an item must carry the cells themselves:\n%s", res.Report.String())
	}
	if !strings.Contains(res.Report.String(), "ValueExpressions entry 1 of 2") {
		t.Errorf("a list entry is an item too:\n%s", res.Report.String())
	}
}

// TestWorksheetReportsCountMismatch keeps the one decoded fact that is a finding
// rather than content out of the documentation alone: a row went missing, and a
// migration worked off the report must see it there.
func TestWorksheetReportsCountMismatch(t *testing.T) {
	src := `<SequentialWorkflow><UpdateResources ActivityDisplayName="A">
	  <UpdateResources.UpdatesTable><Hashtable>
	    <String>x<x:Key xmlns:x="urn:x"><String>0:0</String></x:Key></String>
	    <Int32>4<x:Key xmlns:x="urn:x"><String>Count</String></x:Key></Int32>
	  </Hashtable></UpdateResources.UpdatesTable>
	</UpdateResources></SequentialWorkflow>`
	res, err := Convert(strings.NewReader(src), "C")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)

	var found bool
	for _, n := range res.Report.Notes {
		if strings.Contains(n.Detail, "Count = 4, but 1 row decoded") {
			found = true
			if n.Status != StatusManualReview {
				t.Errorf("a missing row is a manual-review item, got %q", n.Status)
			}
		}
	}
	if !found {
		t.Errorf("the count mismatch must be an item of the worksheet:\n%s", res.Report.String())
	}
	cols := collectionsOf(t, res.BPMN)
	if len(cols) != 1 || len(cols[0].Notes) != 1 {
		t.Fatalf("the mismatch must also travel with the collection: %+v", cols)
	}
}

// TestProcessDocumentationCountsItems keeps the model's own summary honest: the
// three numbers now count worksheet items, and the documentation says so.
func TestProcessDocumentationCountsItems(t *testing.T) {
	src, err := os.ReadFile("testdata/mimwal-workflow.xoml")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Convert(bytes.NewReader(src), "")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if !strings.Contains(string(res.BPMN), "atlas:mimCollection") {
		t.Errorf("the documentation must point at where the decoded rows are:\n%s", res.BPMN)
	}
	if got := res.Report.Count(StatusManualReview); got < 5 {
		t.Errorf("manual-review count = %d, want every decoded row counted", got)
	}
}

// TestEmptyCollections pins the difference between a collection that states it
// holds nothing and a property holding nothing this package can read. MIMWAL
// writes Count="0" on a table an author cleared, and that is a fact about the
// activity — it says the step makes no assignments — so it survives as a
// collection with no rows. An ArrayList with no entries says nothing at all and
// is left to atlas:mimSource.
func TestEmptyCollections(t *testing.T) {
	src := `<SequentialWorkflow><UpdateResources ActivityDisplayName="A">
	  <UpdateResources.UpdatesTable><Hashtable>
	    <Int32>0<x:Key xmlns:x="urn:x"><String>Count</String></x:Key></Int32>
	  </Hashtable></UpdateResources.UpdatesTable>
	  <UpdateResources.ValueExpressions><ArrayList/></UpdateResources.ValueExpressions>
	</UpdateResources></SequentialWorkflow>`
	res, err := Convert(strings.NewReader(src), "E")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)

	cols := collectionsOf(t, res.BPMN)
	if len(cols) != 1 {
		t.Fatalf("want only the counted table, got %+v", cols)
	}
	if cols[0].Property != "UpdatesTable" || cols[0].Count != 0 || len(cols[0].Rows) != 0 {
		t.Errorf("a cleared table must survive as an empty one: %+v", cols[0])
	}
	if !strings.Contains(string(res.BPMN), "UpdatesTable (0 rows)") {
		t.Errorf("the documentation must say the table is empty:\n%s", res.BPMN)
	}
	// No rows means no worksheet items beyond the node itself.
	if got := res.Report.Count(StatusManualReview); got != 0 {
		t.Errorf("an empty table is no work: manual-review = %d\n%s", got, res.Report.String())
	}
}

// TestNothingDecodedIsDropped covers the two ways a collection can hold
// something this package did not expect. An entry whose key is neither a cell
// reference nor the Count becomes a finding of its own rather than disappearing,
// and an element that is not a WF property at all is not mistaken for one.
func TestNothingDecodedIsDropped(t *testing.T) {
	src := `<SequentialWorkflow><UpdateResources ActivityDisplayName="A">
	  <SomeChild Foo="1"/>
	  <UpdateResources.UpdatesTable><Hashtable>
	    <String>x<x:Key xmlns:x="urn:x"><String>0:0</String></x:Key></String>
	    <String>on<x:Key xmlns:x="urn:x"><String>Advanced</String></x:Key></String>
	    <Int32>1<x:Key xmlns:x="urn:x"><String>Count</String></x:Key></Int32>
	  </Hashtable></UpdateResources.UpdatesTable>
	</UpdateResources></SequentialWorkflow>`
	res, err := Convert(strings.NewReader(src), "N")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)

	cols := collectionsOf(t, res.BPMN)
	if len(cols) != 1 {
		t.Fatalf("a child element that is not a property must not become a collection: %+v", cols)
	}
	if len(cols[0].Notes) != 1 || cols[0].Notes[0] != "Advanced = on" {
		t.Errorf("an unexpected key must survive as a finding: %+v", cols[0].Notes)
	}
	var found bool
	for _, n := range res.Report.Notes {
		if n.Detail == "UpdatesTable: Advanced = on" {
			found = true
		}
	}
	if !found {
		t.Errorf("it must be an item of the worksheet too:\n%s", res.Report.String())
	}
}
