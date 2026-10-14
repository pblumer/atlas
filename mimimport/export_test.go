package mimimport

import (
	"strings"
	"testing"
)

// fimExport builds an Export-FIMConfig resource graph in the shape FIMAutomation
// writes: the attribute's name is a child element, not an attribute of the entry.
func fimExport(resources ...map[string]string) string {
	var b strings.Builder
	b.WriteString("<Results>")
	for _, attrs := range resources {
		b.WriteString(`<ExportObject><ResourceManagementObject>` +
			`<ObjectType>WorkflowDefinition</ObjectType><ResourceManagementAttributes>`)
		for _, name := range []string{"DisplayName", "Description", "RequestPhase", "RunOnPolicyUpdate", "ObjectID", "XOML"} {
			v, ok := attrs[name]
			if !ok {
				continue
			}
			b.WriteString(`<ResourceManagementAttribute><AttributeName>` + name +
				`</AttributeName><Value>` + escapeXML(v) + `</Value></ResourceManagementAttribute>`)
		}
		b.WriteString(`</ResourceManagementAttributes></ResourceManagementObject></ExportObject>`)
	}
	b.WriteString("</Results>")
	return b.String()
}

// TestExportFIMConfigElementForm covers the shape a real Export-FIMConfig run
// produces. The importer only recognised the attribute's name written as an XML
// attribute; written as a child element — the form FIMAutomation actually uses —
// nothing was found, and the whole export converted into a process of
// <ExportObject> placeholder tasks.
func TestExportFIMConfigElementForm(t *testing.T) {
	export := fimExport(map[string]string{
		"DisplayName":       "Joiner Prozess",
		"Description":       "Onboarding neuer Mitarbeitender",
		"RequestPhase":      "Action",
		"RunOnPolicyUpdate": "true",
		"ObjectID":          "urn:uuid:1111",
		"XOML":              `<SequentialWorkflow><ApprovalActivity ActivityDisplayName="Freigabe"/></SequentialWorkflow>`,
	})
	res, err := Convert(strings.NewReader(export), "")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
	bpmn := string(res.BPMN)

	if !strings.Contains(bpmn, "<userTask") {
		t.Errorf("the embedded workflow did not convert:\n%s", bpmn)
	}
	if strings.Contains(bpmn, "ExportObject") {
		t.Errorf("the wrapper must not become part of the process:\n%s", bpmn)
	}
	// The resource's own name is the workflow's only human name.
	if !strings.Contains(bpmn, `name="Joiner Prozess"`) {
		t.Errorf("the process should take the resource's DisplayName:\n%s", bpmn)
	}
	if got := res.Report.Source; got.RequestPhase != "Action" || got.ObjectID != "urn:uuid:1111" ||
		got.Description != "Onboarding neuer Mitarbeitender" || got.RunOnPolicyUpdate != "true" {
		t.Errorf("resource fields were not read: %+v", got)
	}
	// They belong on the process too, not only in the import response.
	for _, want := range []string{"Anforderungsphase: Action", "MIM-ObjectID: urn:uuid:1111"} {
		if !strings.Contains(bpmn, want) {
			t.Errorf("process documentation is missing %q:\n%s", want, bpmn)
		}
	}
}

// TestExportWithSeveralWorkflows covers what an export of more than one workflow
// used to do: convert the first and drop the rest without a word.
func TestExportWithSeveralWorkflows(t *testing.T) {
	export := fimExport(
		map[string]string{"DisplayName": "Joiner", "XOML": `<SequentialWorkflow><ApprovalActivity/></SequentialWorkflow>`},
		map[string]string{"DisplayName": "Mover", "XOML": `<SequentialWorkflow><NotificationActivity/></SequentialWorkflow>`},
		map[string]string{"DisplayName": "Leaver", "XOML": `<SequentialWorkflow><PowerShellActivity ScriptText="x"/></SequentialWorkflow>`},
	)
	all, err := ConvertAll(strings.NewReader(export), "")
	if err != nil {
		t.Fatalf("ConvertAll: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("want a result per workflow, got %d", len(all))
	}
	for i, want := range []string{"Joiner", "Mover", "Leaver"} {
		validate(t, all[i].BPMN)
		if all[i].Report.Source.DisplayName != want {
			t.Errorf("result %d is %q, want %q", i, all[i].Report.Source.DisplayName, want)
		}
		if all[i].Report.ProcessID != want {
			t.Errorf("result %d has process id %q, want %q", i, all[i].Report.ProcessID, want)
		}
	}

	// Convert still answers with one process, but no longer pretends it is the
	// whole input.
	one, err := Convert(strings.NewReader(export), "")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	var warned bool
	for _, w := range one.Report.Warnings {
		if strings.Contains(w, "carries 3 workflows") && strings.Contains(w, "Mover") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("Convert must say what it left behind, got %q", one.Report.Warnings)
	}
}

// TestNameOverrideOnlyForASingleWorkflow: one name cannot stand for several
// workflows, so it applies only when the input carries one.
func TestNameOverrideOnlyForASingleWorkflow(t *testing.T) {
	one := fimExport(map[string]string{"DisplayName": "Joiner", "XOML": `<SequentialWorkflow><ApprovalActivity/></SequentialWorkflow>`})
	res, err := Convert(strings.NewReader(one), "Wunschname")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if res.Report.ProcessID != "Wunschname" {
		t.Errorf("a single workflow takes the caller's name, got %q", res.Report.ProcessID)
	}

	two := fimExport(
		map[string]string{"DisplayName": "Joiner", "XOML": `<SequentialWorkflow><ApprovalActivity/></SequentialWorkflow>`},
		map[string]string{"DisplayName": "Mover", "XOML": `<SequentialWorkflow><ApprovalActivity/></SequentialWorkflow>`},
	)
	all, err := ConvertAll(strings.NewReader(two), "Wunschname")
	if err != nil {
		t.Fatalf("ConvertAll: %v", err)
	}
	for i, want := range []string{"Joiner", "Mover"} {
		if all[i].Report.ProcessID != want {
			t.Errorf("result %d is %q, want its own name %q", i, all[i].Report.ProcessID, want)
		}
	}
}

// TestUnparseableWorkflowIsSkippedNotFatal: one broken definition in an export
// must not cost the others.
func TestUnparseableWorkflowIsSkippedNotFatal(t *testing.T) {
	export := fimExport(
		map[string]string{"DisplayName": "Kaputt", "XOML": `<SequentialWorkflow`},
		map[string]string{"DisplayName": "Heil", "XOML": `<SequentialWorkflow><ApprovalActivity/></SequentialWorkflow>`},
	)
	all, err := ConvertAll(strings.NewReader(export), "")
	if err != nil {
		t.Fatalf("ConvertAll: %v", err)
	}
	if len(all) != 1 || all[0].Report.Source.DisplayName != "Heil" {
		t.Fatalf("the sound workflow should still convert, got %d results", len(all))
	}
	var warned bool
	for _, w := range all[0].Report.Warnings {
		if strings.Contains(w, "did not parse") && strings.Contains(w, "Kaputt") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("the skipped workflow must be named, got %q", all[0].Report.Warnings)
	}
}

// TestPropertyElementIsNotAnActivity covers the WF property element, which is how
// a real IfElseBranchActivity carries its condition. Reading it as an activity
// put a task the workflow does not have into the branch, named after the property
// and doing nothing, while the condition it holds went unread.
func TestPropertyElementIsNotAnActivity(t *testing.T) {
	src := `<SequentialWorkflow>
	  <IfElseActivity ActivityDisplayName="Weiche">
	    <IfElseBranchActivity ActivityDisplayName="Finance">
	      <IfElseBranchActivity.Condition>
	        <DeclarativeRuleConditionReference ConditionName="FinanceRule"/>
	      </IfElseBranchActivity.Condition>
	      <ApprovalActivity ActivityDisplayName="Freigabe"/>
	    </IfElseBranchActivity>
	    <IfElseBranchActivity ActivityDisplayName="Sonst">
	      <NotificationActivity ActivityDisplayName="Melden"/>
	    </IfElseBranchActivity>
	  </IfElseActivity>
	</SequentialWorkflow>`
	res, err := Convert(strings.NewReader(src), "P")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
	bpmn := string(res.BPMN)

	if strings.Contains(bpmn, "IfElseBranchActivity.Condition") && strings.Contains(bpmn, `<task id=`) {
		t.Errorf("a property element must not become a task:\n%s", bpmn)
	}
	// Two activities, no phantom third.
	if got := strings.Count(bpmn, "<userTask") + strings.Count(bpmn, "<serviceTask") + strings.Count(bpmn, "<task "); got != 2 {
		t.Errorf("want exactly the two real activities, got %d:\n%s", got, bpmn)
	}
	// The condition it holds is read, and named for review.
	var flagged bool
	for _, n := range res.Report.Notes {
		if n.Kind == "conditionExpression" && strings.Contains(n.Detail, "rule FinanceRule") {
			flagged = true
		}
	}
	if !flagged {
		t.Errorf("the branch condition must be reported, got %s", res.Report.String())
	}
}

// TestSingleConditionalBranchKeepsItsCondition covers the commonest MIM branch —
// "if X then do Y" — which used to become an unconditional path: the last branch
// was made the gateway default whatever it carried, so the condition was dropped
// without a note and the node was reported native.
func TestSingleConditionalBranchKeepsItsCondition(t *testing.T) {
	src := `<SequentialWorkflow>
	  <IfElseActivity ActivityDisplayName="Weiche">
	    <IfElseBranchActivity ActivityDisplayName="Nur wenn" Condition="a = b">
	      <ApprovalActivity ActivityDisplayName="Freigabe"/>
	    </IfElseBranchActivity>
	  </IfElseActivity>
	</SequentialWorkflow>`
	res, err := Convert(strings.NewReader(src), "S")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
	bpmn := string(res.BPMN)

	// The branch is conditional, and there is a way past it — which is what WF
	// does when the condition does not hold.
	if !strings.Contains(bpmn, "<conditionExpression>= false</conditionExpression>") {
		t.Errorf("the conditional branch needs a condition:\n%s", bpmn)
	}
	if !strings.Contains(bpmn, `name="keine Bedingung trifft zu"`) {
		t.Errorf("an all-conditional if/else needs a bypass default:\n%s", bpmn)
	}
	var flagged bool
	for _, n := range res.Report.Notes {
		if n.Kind == "conditionExpression" && strings.Contains(n.Detail, "a = b") {
			flagged = true
		}
	}
	if !flagged {
		t.Errorf("the condition must be reported, got %s", res.Report.String())
	}
}

// TestUnconditionalBranchIsTheDefault: an else branch is the default wherever it
// sits, not merely when it happens to be last.
func TestUnconditionalBranchIsTheDefault(t *testing.T) {
	src := `<SequentialWorkflow>
	  <IfElseActivity ActivityDisplayName="Weiche">
	    <IfElseBranchActivity ActivityDisplayName="Sonst"><NotificationActivity ActivityDisplayName="N"/></IfElseBranchActivity>
	    <IfElseBranchActivity ActivityDisplayName="Wenn" Condition="x"><ApprovalActivity ActivityDisplayName="A"/></IfElseBranchActivity>
	  </IfElseActivity>
	</SequentialWorkflow>`
	res, err := Convert(strings.NewReader(src), "U")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
	bpmn := string(res.BPMN)
	if strings.Contains(bpmn, `name="keine Bedingung trifft zu"`) {
		t.Errorf("an unconditional branch is already the default, no bypass needed:\n%s", bpmn)
	}
	if !strings.Contains(bpmn, `name="sonst"`) {
		t.Errorf("the unconditional branch should be labelled as the default:\n%s", bpmn)
	}
}

// TestConditionedActivityGroupIsALoop covers the CAG, which used to be flattened
// into a plain sequence: the group element, its markup, its UntilCondition and
// every child's WhenCondition disappeared without a single note.
func TestConditionedActivityGroupIsALoop(t *testing.T) {
	src := `<SequentialWorkflow>
	  <ConditionedActivityGroup ActivityDisplayName="Bis fertig" UntilCondition="alleErledigt">
	    <UpdateResourceActivity ActivityDisplayName="Schritt" WhenCondition="offen"/>
	  </ConditionedActivityGroup>
	</SequentialWorkflow>`
	res, err := Convert(strings.NewReader(src), "C")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
	bpmn := string(res.BPMN)

	// The group repeats: a decision gateway with a way back into the body.
	if !strings.Contains(bpmn, `name="nochmal"`) || !strings.Contains(bpmn, `name="fertig"`) {
		t.Errorf("the group should become a repeat-until loop:\n%s", bpmn)
	}
	// The group itself is a node now, with its markup on it.
	if !strings.Contains(bpmn, `activity="ConditionedActivityGroup"`) {
		t.Errorf("the group's own markup must be preserved:\n%s", bpmn)
	}
	// Its child's WhenCondition guards the child, like any other MIM guard.
	if !strings.Contains(bpmn, "MIM ActivityExecutionCondition: offen") {
		t.Errorf("the child's WhenCondition should guard it:\n%s", bpmn)
	}
	var untilFlagged bool
	for _, n := range res.Report.Notes {
		if strings.Contains(n.Detail, "UntilCondition") && strings.Contains(n.Detail, "alleErledigt") {
			untilFlagged = true
		}
	}
	if !untilFlagged {
		t.Errorf("the UntilCondition must be reported, got %s", res.Report.String())
	}
}

// TestSourceLabelNamesAResource covers the fallbacks a skipped WorkflowDefinition
// is named by: a resource without a DisplayName is still worth naming.
func TestSourceLabelNamesAResource(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   SourceInfo
		want string
	}{
		{"display name wins", SourceInfo{DisplayName: "Joiner", ObjectID: "urn:1"}, "Joiner"},
		{"object id when unnamed", SourceInfo{ObjectID: "urn:1"}, "urn:1"},
		{"nothing to go on", SourceInfo{}, "unnamed"},
	} {
		if got := sourceLabel(tc.in); got != tc.want {
			t.Errorf("%s: sourceLabel = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestSourceDescribeSaysNothingAboutNothing: raw XOML has no resource around it,
// and the process documentation must not gain an empty sentence for it.
func TestSourceDescribeSaysNothingAboutNothing(t *testing.T) {
	if got := (SourceInfo{}).describe(); got != "" {
		t.Errorf("describe() = %q, want empty", got)
	}
	if got := (SourceInfo{DisplayName: "Joiner"}).describe(); got != "" {
		t.Errorf("a name alone is the process name, not a fact to restate: %q", got)
	}
	if got := (SourceInfo{RequestPhase: "Action"}).describe(); got != "Anforderungsphase: Action." {
		t.Errorf("describe() = %q", got)
	}
}

// TestConditionTextReadsEveryShape covers how WF writes a condition: a rule it
// refers to, an inline expression on an attribute, and the element's own text.
func TestConditionTextReadsEveryShape(t *testing.T) {
	for _, tc := range []struct {
		name, xml, want string
		ok              bool
	}{
		{"declarative rule reference", `<Branch.Condition><RuleConditionReference ConditionName="R1"/></Branch.Condition>`, "rule R1", true},
		{"code condition expression", `<Branch.Condition><CodeCondition Expression="a &gt; b"/></Branch.Condition>`, "a > b", true},
		{"inline text", `<Branch.Condition>x = 1</Branch.Condition>`, "x = 1", true},
		{"markup with no name or text", `<Branch.Condition><Odd><Deeper/></Odd></Branch.Condition>`, "<Odd><Deeper/></Odd>", true},
		{"empty", `<Branch.Condition></Branch.Condition>`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, _, err := decodeNode([]byte(tc.xml))
			if err != nil {
				t.Fatal(err)
			}
			got, ok := conditionText(n)
			if ok != tc.ok || got != tc.want {
				t.Errorf("conditionText = (%q, %v), want (%q, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestAttributeEntryReadsEveryShape covers the three ways FIMAutomation writes an
// attribute, and the element that is none of them.
func TestAttributeEntryReadsEveryShape(t *testing.T) {
	for _, tc := range []struct {
		name, xml, wantName, wantValue string
		ok                             bool
	}{
		{"name as an attribute", `<AttributeType AttributeName="XOML"><Value>x</Value></AttributeType>`, "XOML", "x", true},
		{"name as a child element", `<Attr><AttributeName>XOML</AttributeName><Value>x</Value></Attr>`, "XOML", "x", true},
		{"name is the element", `<XOML>x</XOML>`, "XOML", "x", true},
		{"nothing to read", `<Empty></Empty>`, "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, _, err := decodeNode([]byte(tc.xml))
			if err != nil {
				t.Fatal(err)
			}
			name, value, ok := attributeEntry(n)
			if ok != tc.ok || name != tc.wantName || value != tc.wantValue {
				t.Errorf("attributeEntry = (%q, %q, %v), want (%q, %q, %v)",
					name, value, ok, tc.wantName, tc.wantValue, tc.ok)
			}
		})
	}
}

// TestExportWithNoWorkflowFallsBack: a wrapper carrying no XOML at all is still
// converted as best it can be, rather than failing the upload.
func TestExportWithNoWorkflowFallsBack(t *testing.T) {
	res, err := Convert(strings.NewReader(`<Results><ExportObject><Nothing>here</Nothing></ExportObject></Results>`), "F")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
}

// TestEveryWorkflowUnparseableIsAnError: when nothing in an export could be read,
// the caller gets the parser's reason rather than an empty success.
func TestEveryWorkflowUnparseableIsAnError(t *testing.T) {
	export := fimExport(map[string]string{"DisplayName": "Kaputt", "XOML": `<SequentialWorkflow`})
	if _, err := ConvertAll(strings.NewReader(export), ""); err == nil {
		t.Error("an export whose every workflow is broken must fail")
	}
}

// TestEmptyLoopsStayWellFormed covers the bodies that have nothing in them: a
// group or a while with no child activities still has to be a loop a compiler
// accepts, and says so rather than producing a dangling gateway.
func TestEmptyLoopsStayWellFormed(t *testing.T) {
	for _, tc := range []struct{ name, src, want string }{
		{"conditioned activity group", `<SequentialWorkflow><ConditionedActivityGroup ActivityDisplayName="Leer"/></SequentialWorkflow>`,
			"ConditionedActivityGroup has no child activities"},
		{"while", `<SequentialWorkflow><WhileActivity ActivityDisplayName="Leer"/></SequentialWorkflow>`,
			"empty while body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Convert(strings.NewReader(tc.src), "E")
			if err != nil {
				t.Fatalf("Convert: %v", err)
			}
			validate(t, res.BPMN)
			var flagged bool
			for _, n := range res.Report.Notes {
				if strings.Contains(n.Detail, tc.want) {
					flagged = true
				}
			}
			if !flagged {
				t.Errorf("an empty body must be reported, got %s", res.Report.String())
			}
		})
	}
}

// TestQuoteValueEscapesWhenBothQuotesAppear covers the last resort of the repair
// pass: a bare value holding both a quote and an apostrophe.
func TestQuoteValueEscapesWhenBothQuotesAppear(t *testing.T) {
	if got := string(quoteValue([]byte(`a"b'c`))); got != `"a&quot;b'c"` {
		t.Errorf("quoteValue = %s", got)
	}
}

// TestEmptyCollectionRendersNothing: a table element with no entries must not
// leave an empty heading on the activity's documentation, nor an empty
// <atlas:mimCollection> on the element.
func TestEmptyCollectionRendersNothing(t *testing.T) {
	n, _, err := decodeNode([]byte(`<UpdateResources><UpdateResources.UpdatesTable><ArrayList/></UpdateResources.UpdatesTable></UpdateResources>`))
	if err != nil {
		t.Fatal(err)
	}
	cols := mimCollections(n)
	if len(cols) != 0 {
		t.Errorf("mimCollections = %+v, want none", cols)
	}
	if got := renderCollections(cols); got != "" {
		t.Errorf("renderCollections = %q, want empty", got)
	}
}
