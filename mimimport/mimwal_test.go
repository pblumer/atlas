package mimimport

import (
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"strings"
	"testing"
)

// mimSources returns the payload of every <atlas:mimSource> element in a
// generated document, unescaped by the XML decoder exactly as a consumer of the
// extension element would read it.
func mimSources(t *testing.T, bpmn []byte) []string {
	t.Helper()
	var out []string
	dec := xml.NewDecoder(bytes.NewReader(bpmn))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return out
		}
		if err != nil {
			t.Fatalf("generated BPMN did not parse: %v", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "mimSource" {
			continue
		}
		var payload string
		if err := dec.DecodeElement(&payload, &se); err != nil {
			t.Fatalf("mimSource did not decode: %v", err)
		}
		out = append(out, payload)
	}
}

// TestConvertMIMWALWorkflow covers a workflow as the MIMWAL activity library
// really serialises one: xmlns declarations written without quotes, the author's
// label in ActivityDisplayName rather than DisplayName, and MIM expressions that
// contain quotation marks.
func TestConvertMIMWALWorkflow(t *testing.T) {
	src, err := os.ReadFile("testdata/mimwal-workflow.xoml")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Convert(bytes.NewReader(src), "")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
	bpmn := string(res.BPMN)

	// The document only parses after its unquoted xmlns values are repaired, and
	// that repair must be reported rather than assumed.
	if len(res.Report.Warnings) != 1 || !strings.Contains(res.Report.Warnings[0], "unquoted attribute values") {
		t.Errorf("expected a warning about the repaired input, got %q", res.Report.Warnings)
	}
	if !strings.Contains(res.Report.String(), "warning") {
		t.Error("report should render its warnings")
	}
	if !strings.Contains(bpmn, "Hinweis: Die Eingabe war nicht wohlgeformt") {
		t.Errorf("process documentation should carry the warning:\n%s", bpmn)
	}

	// Names come from ActivityDisplayName, not from the WF designer id in x:Name.
	for _, want := range []string{
		`name="Abhängige Objekte abfragen"`,
		`name="Je Namensmuster ausführen"`,
		`name="Eindeutigen Kontonamen erzeugen"`,
	} {
		if !strings.Contains(bpmn, want) {
			t.Errorf("generated BPMN is missing %s\n%s", want, bpmn)
		}
	}
	// x:Name survives inside the preserved source, but must never become a node
	// name.
	if strings.Contains(bpmn, `name="actionActivity`) {
		t.Errorf("no node should be named after its x:Name designer id:\n%s", bpmn)
	}

	// Re-parsing the preserved source must yield the activity's original
	// attribute values, quotation marks included.
	sources := mimSources(t, res.BPMN)
	if len(sources) != 3 {
		t.Fatalf("want a preserved source per activity, got %d", len(sources))
	}
	update, _, err := decodeNode([]byte(sources[0]))
	if err != nil {
		t.Fatalf("preserved UpdateResources did not re-parse: %v\n%s", err, sources[0])
	}
	const wantCond = `Not(ParametersContain([//Request/RequestParameter], "ReApplyBusinessObject"))`
	if got, _ := update.attr("ActivityExecutionCondition"); got != wantCond {
		t.Errorf("ActivityExecutionCondition = %q, want %q", got, wantCond)
	}
	if !strings.Contains(update.Inner, `/Group[DependsOn='[//Target/ObjectID]']`) {
		t.Errorf("the MIMWAL queries table was not preserved:\n%s", update.Inner)
	}

	unique, _, err := decodeNode([]byte(sources[2]))
	if err != nil {
		t.Fatalf("preserved GenerateUniqueValue did not re-parse: %v\n%s", err, sources[1])
	}
	const wantFilter = `/*[(AccountName = '[//Value]')]`
	if got, _ := unique.attr("ConflictFilter"); got != wantFilter {
		t.Errorf("ConflictFilter = %q, want %q", got, wantFilter)
	}
}

// TestQuoteAttrValues checks the repair pass in isolation: it must quote bare
// values and leave everything that only looks like one untouched.
func TestQuoteAttrValues(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
		changed        bool
	}{
		{
			name:    "bare xmlns value",
			in:      `<a xmlns=http://x/y b="1"/>`,
			want:    `<a xmlns="http://x/y" b="1"/>`,
			changed: true,
		},
		{
			name: "value ending at a self-closing tag",
			in:   `<a b=1/>`, want: `<a b="1"/>`, changed: true,
		},
		{
			name: "equals inside a quoted value is left alone",
			in:   `<a b="x=y" c='p=q'/>`, want: `<a b="x=y" c='p=q'/>`,
		},
		{
			name: "markup inside text and comments is left alone",
			in:   `<a><!-- b=c --><![CDATA[d=e]]>f=g</a>`,
			want: `<a><!-- b=c --><![CDATA[d=e]]>f=g</a>`,
		},
		{
			name: "bare value containing a quote falls back to apostrophes",
			in:   `<a b=x"y/>`, want: `<a b='x"y'/>`, changed: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := quoteAttrValues([]byte(tc.in))
			if string(got) != tc.want {
				t.Errorf("quoteAttrValues() = %q, want %q", got, tc.want)
			}
			if changed != tc.changed {
				t.Errorf("changed = %v, want %v", changed, tc.changed)
			}
		})
	}
}

// TestDecodeNodeKeepsOriginalError checks that input which stays broken after a
// repair still fails, and that well-formed input is never rewritten.
func TestDecodeNodeKeepsOriginalError(t *testing.T) {
	if _, _, err := decodeNode([]byte(`<a b=1`)); err == nil {
		t.Error("truncated input should still be an error")
	}
	if _, repaired, err := decodeNode([]byte(`<a b="1"/>`)); err != nil || repaired {
		t.Errorf("well-formed input needs no repair: repaired=%v err=%v", repaired, err)
	}
}

// forwardEdges reports every DI edge drawn right-to-left. The layout is a
// left-to-right layering, so an edge pointing backwards means a node landed in
// the wrong column, not that the model is wrong. Only for acyclic models: a
// while loop's return edge points backwards by design.
func forwardEdges(t *testing.T, bpmn []byte) {
	t.Helper()
	type wp struct {
		X int `xml:"x,attr"`
	}
	var doc struct {
		Edges []struct {
			ID        string `xml:"id,attr"`
			Waypoints []wp   `xml:"waypoint"`
		} `xml:"BPMNDiagram>BPMNPlane>BPMNEdge"`
	}
	if err := xml.Unmarshal(bpmn, &doc); err != nil {
		t.Fatalf("DI did not parse: %v", err)
	}
	if len(doc.Edges) == 0 {
		t.Fatal("no DI edges at all")
	}
	for _, e := range doc.Edges {
		if len(e.Waypoints) == 2 && e.Waypoints[1].X < e.Waypoints[0].X {
			t.Errorf("edge %s is drawn backwards: x %d → %d", e.ID, e.Waypoints[0].X, e.Waypoints[1].X)
		}
	}
}

// TestGuardBecomesConditionalPath covers the MIMWAL guard: an activity carrying
// an ActivityExecutionCondition is entered through a conditional split and
// bypassed through the gateway default, and an activity without one is left in
// the chain untouched.
func TestGuardBecomesConditionalPath(t *testing.T) {
	src, err := os.ReadFile("testdata/mimwal-workflow.xoml")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Convert(bytes.NewReader(src), "")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
	forwardEdges(t, res.BPMN)
	bpmn := string(res.BPMN)

	// One guarded activity of three: one split/merge pair, no more.
	if got := strings.Count(bpmn, "<exclusiveGateway"); got != 2 {
		t.Errorf("want a split and a merge for the one guarded activity, got %d gateways:\n%s", got, bpmn)
	}
	// The activity is entered on a condition and bypassed by the default, so the
	// generated process still runs it while showing that it is conditional.
	if !strings.Contains(bpmn, `name="ausführen"`) || !strings.Contains(bpmn, "<conditionExpression>= true</conditionExpression>") {
		t.Errorf("the guarded activity should be entered on a placeholder condition:\n%s", bpmn)
	}
	if !strings.Contains(bpmn, `name="überspringen"`) {
		t.Errorf("the guard needs a bypass:\n%s", bpmn)
	}
	if !strings.Contains(bpmn, "MIM ActivityExecutionCondition: Not(ParametersContain(") {
		t.Errorf("the split should document the original guard:\n%s", bpmn)
	}

	// The bypass is the gateway default: once the placeholder is replaced by the
	// real condition, an activity whose guard does not hold is skipped.
	var doc struct {
		Gateways []struct {
			ID      string `xml:"id,attr"`
			Default string `xml:"default,attr"`
		} `xml:"process>exclusiveGateway"`
		Flows []struct {
			ID   string `xml:"id,attr"`
			To   string `xml:"targetRef,attr"`
			Name string `xml:"name,attr"`
		} `xml:"process>sequenceFlow"`
	}
	if err := xml.Unmarshal(res.BPMN, &doc); err != nil {
		t.Fatal(err)
	}
	var defaultFlow string
	for _, g := range doc.Gateways {
		if g.Default != "" {
			defaultFlow = g.Default
		}
	}
	if defaultFlow == "" {
		t.Fatal("the guard split needs a default flow")
	}
	for _, f := range doc.Flows {
		if f.ID == defaultFlow && f.Name != "überspringen" {
			t.Errorf("the default flow should be the bypass, got %q", f.Name)
		}
	}

	// The untranslated guard is flagged, with its original expression.
	var flagged int
	for _, n := range res.Report.Notes {
		if n.Kind == "conditionExpression" && n.Status == StatusManualReview &&
			strings.Contains(n.Detail, "ParametersContain") {
			flagged++
		}
	}
	if flagged != 1 {
		t.Errorf("want the one guard flagged for review with its expression, got %d", flagged)
	}
}

// TestEmptyGuardIsNotAGateway guards the empty-attribute case: MIMWAL writes
// ActivityExecutionCondition="" on an activity that always runs.
func TestEmptyGuardIsNotAGateway(t *testing.T) {
	res, err := Convert(strings.NewReader(
		`<SequentialWorkflow><UpdateResources ActivityExecutionCondition="" ActivityDisplayName="A"/></SequentialWorkflow>`), "E")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
	if strings.Contains(string(res.BPMN), "<exclusiveGateway") {
		t.Errorf("an empty guard must not produce a gateway:\n%s", res.BPMN)
	}
}

// TestLayoutBypassIsForward covers the layering fix on the shape that exposed
// it: a split that both enters a branch and bypasses it reaches the merge in one
// hop and through the branch in two.
func TestLayoutBypassIsForward(t *testing.T) {
	src := `<SequentialWorkflow>
	  <ParallelActivity Description="Fan out">
	    <SequenceActivity><NotificationActivity Description="A"/></SequenceActivity>
	    <SequenceActivity/>
	  </ParallelActivity>
	  <UpdateResources ActivityExecutionCondition="Eq([//Target/X],True)" ActivityDisplayName="Guarded"/>
	</SequentialWorkflow>`
	res, err := Convert(strings.NewReader(src), "L")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
	forwardEdges(t, res.BPMN)
}

// TestIterationBecomesMultiInstance covers the MIMWAL Iteration: an activity MIM
// runs once per value of a delimited attribute is a multi-instance activity, not
// the single step the element alone suggests.
func TestIterationBecomesMultiInstance(t *testing.T) {
	src, err := os.ReadFile("testdata/mimwal-workflow.xoml")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Convert(bytes.NewReader(src), "")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
	bpmn := string(res.BPMN)

	// One iterated activity of three, walked in order.
	if got := strings.Count(bpmn, "<multiInstanceLoopCharacteristics"); got != 1 {
		t.Errorf("want one multi-instance activity, got %d:\n%s", got, bpmn)
	}
	for _, want := range []string{
		`<multiInstanceLoopCharacteristics isSequential="true">`,
		`<zeebe:loopCharacteristics inputCollection="=[1]" inputElement="mimValue"/>`,
	} {
		if !strings.Contains(bpmn, want) {
			t.Errorf("generated BPMN is missing %s\n%s", want, bpmn)
		}
	}
	// The untranslated expression stays readable on the activity and flagged.
	if !strings.Contains(bpmn, "MIM Iteration: SplitString([//Target/AssetType/PatternAccountName]") {
		t.Errorf("the activity should document its MIM Iteration:\n%s", bpmn)
	}
	var flagged int
	for _, n := range res.Report.Notes {
		if n.Kind == "multiInstanceLoopCharacteristics" && n.Status == StatusManualReview &&
			strings.Contains(n.Detail, "SplitString") {
			flagged++
		}
	}
	if flagged != 1 {
		t.Errorf("want the one iteration flagged with its expression, got %d", flagged)
	}
}

// TestGuardAndIterationCombine covers an activity carrying both, which is how the
// MIMWAL workflows that iterate a pattern are written: the guard wraps the
// activity, the loop marker sits on it.
func TestGuardAndIterationCombine(t *testing.T) {
	res, err := Convert(strings.NewReader(
		`<SequentialWorkflow><ns1:UpdateResources ActivityDisplayName="Beides"`+
			` ActivityExecutionCondition="Eq([//Target/X],True)"`+
			` Iteration="SplitString([//Target/P],&quot;;&quot;)"/></SequentialWorkflow>`), "GI")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
	forwardEdges(t, res.BPMN)
	bpmn := string(res.BPMN)
	if got := strings.Count(bpmn, "<exclusiveGateway"); got != 2 {
		t.Errorf("the guard still needs its split and merge, got %d gateways:\n%s", got, bpmn)
	}
	if !strings.Contains(bpmn, "<multiInstanceLoopCharacteristics") {
		t.Errorf("the loop marker must survive the guard wrapper:\n%s", bpmn)
	}
}

// TestEmptyIterationIsNotALoop guards the empty-attribute case: MIMWAL writes
// Iteration="" on an activity that runs once.
func TestEmptyIterationIsNotALoop(t *testing.T) {
	res, err := Convert(strings.NewReader(
		`<SequentialWorkflow><UpdateResources Iteration="" ActivityDisplayName="A"/></SequentialWorkflow>`), "I")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
	if strings.Contains(string(res.BPMN), "multiInstanceLoopCharacteristics") {
		t.Errorf("an empty Iteration must not produce a loop marker:\n%s", res.BPMN)
	}
}

// TestGenerateUniqueValueIsClassified covers MIMWAL's other core activity, which
// used to fall through to an unrecognised plain-task placeholder.
func TestGenerateUniqueValueIsClassified(t *testing.T) {
	res, err := Convert(strings.NewReader(
		`<SequentialWorkflow><ns1:GenerateUniqueValue ActivityDisplayName="Kontoname"`+
			` PublicationTarget="[//WorkflowData/AccountName]"/></SequentialWorkflow>`), "U")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
	if !strings.Contains(string(res.BPMN), `type="mim-uniquevalue"`) {
		t.Errorf("GenerateUniqueValue should map to a mim-uniquevalue service task:\n%s", res.BPMN)
	}
	if res.Report.Count(StatusManualReview) != 0 || res.Report.Count(StatusPreserved) != 1 {
		t.Errorf("it is a recognised activity, not a placeholder: %s", res.Report.String())
	}
}

// TestMIMWALTablesAreDecoded covers the serialised .NET collections that hold a
// MIMWAL activity's actual work: they are rendered as a readable table on the
// activity's documentation, by position, without naming what a column means.
func TestMIMWALTablesAreDecoded(t *testing.T) {
	src, err := os.ReadFile("testdata/mimwal-workflow.xoml")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Convert(bytes.NewReader(src), "")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
	bpmn := string(res.BPMN)

	for _, want := range []string{
		"QueriesTable (1 row)",
		"[0] AllGroups | /Group[DependsOn='[//Target/ObjectID]']",
		"UpdatesTable (2 rows)",
		// Cells come back in column order however the source listed them.
		"[0] [//Queries/AllGroups] | [//WorkflowData/AllGroups] | false",
		"[1] [//Target/DisplayName] | [//WorkflowData/Name]",
		"ValueExpressions (2 entries)",
		"[1] Left(Trim([//WorkflowData/AccountNameBase]),18)+[//UniquenessKey]",
	} {
		if !strings.Contains(bpmn, want) {
			t.Errorf("generated BPMN is missing %q\n%s", want, bpmn)
		}
	}
	// Count agreed with the decoded rows, so it is a check that passed, not content.
	if strings.Contains(bpmn, "Count = ") {
		t.Errorf("a Count that matches must not be rendered:\n%s", bpmn)
	}
	// Rendering must not cost the verbatim source.
	if len(mimSources(t, res.BPMN)) != 3 {
		t.Error("every activity must still carry its original markup")
	}
}

// TestHashtableCountMismatchIsReported covers the case the Count exists for: it
// disagrees with what was decoded, so a row went missing.
func TestHashtableCountMismatchIsReported(t *testing.T) {
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
	if !strings.Contains(string(res.BPMN), "Count = 4, but 1 row decoded") {
		t.Errorf("a Count that disagrees must be reported:\n%s", res.BPMN)
	}
}

// TestUnknownPropertyIsNotRendered keeps the renderer quiet about property
// elements it does not understand: they stay in atlas:mimSource, unsummarised,
// rather than being reported as an empty table.
func TestUnknownPropertyIsNotRendered(t *testing.T) {
	src := `<SequentialWorkflow><ApprovalActivity ActivityDisplayName="A">
	  <ApprovalActivity.ApprovalObject><SomeType Foo="1"/></ApprovalActivity.ApprovalObject>
	</ApprovalActivity></SequentialWorkflow>`
	res, err := Convert(strings.NewReader(src), "P")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
	bpmn := string(res.BPMN)
	if strings.Contains(bpmn, "ApprovalObject (") {
		t.Errorf("an unrecognised property must not be rendered as a table:\n%s", bpmn)
	}
	if !strings.Contains(bpmn, "ApprovalActivity.ApprovalObject") {
		t.Errorf("it must still be preserved verbatim:\n%s", bpmn)
	}
}
