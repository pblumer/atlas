package mimimport

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// validate compiles the generated BPMN through the real Atlas validator and
// fails the test on any error-severity problem — the primary guarantee that a
// conversion is deployable.
func validate(t *testing.T, bpmn []byte) {
	t.Helper()
	ps, err := compiler.ValidateModel(bytes.NewReader(bpmn))
	if err != nil {
		t.Fatalf("ValidateModel returned error: %v", err)
	}
	if compiler.HasErrors(ps) {
		for _, p := range ps {
			if p.Severity == compiler.SeverityError {
				t.Errorf("validation error [%s] %s: %s", p.Rule, p.Element, p.Message)
			}
		}
		t.Fatalf("generated BPMN did not validate:\n%s", bpmn)
	}
}

func TestConvertApprovalWorkflow(t *testing.T) {
	src, err := os.ReadFile("testdata/approval-workflow.xoml")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Convert(bytes.NewReader(src), "")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)

	bpmn := string(res.BPMN)
	wants := []string{
		`<userTask`,               // ApprovalActivity → user task
		`type="mim-notification"`, // NotificationActivity → notification worker
		`type="mim-powershell"`,   // PowerShellActivity preserved
		`type="mim-resource"`,     // UpdateResourceActivity preserved
		`type="mim-function"`,     // FunctionEvaluatorActivity preserved
		`<exclusiveGateway`,       // IfElseActivity → gateway
		`<atlas:mimSource`,        // original markup preserved
		`New-Mailbox`,             // PowerShell body survived verbatim
		`<bpmndi:BPMNDiagram`,     // diagram interchange so the Modeler can render it
		`<bpmndi:BPMNShape`,       // a shape per node
		`<di:waypoint`,            // an edge per flow
	}
	for _, w := range wants {
		if !strings.Contains(bpmn, w) {
			t.Errorf("generated BPMN is missing %q\n---\n%s", w, bpmn)
		}
	}

	if res.Report.Count(StatusNative) == 0 {
		t.Error("expected some native nodes")
	}
	if res.Report.Count(StatusPreserved) == 0 {
		t.Error("expected some preserved nodes")
	}
	// The Finance condition on the approval branch cannot be translated to FEEL,
	// so it must be surfaced for manual review rather than dropped.
	if res.Report.Count(StatusManualReview) == 0 {
		t.Error("expected the untranslatable branch condition to be flagged")
	}
	if !strings.Contains(res.Report.String(), "manual-review") {
		t.Error("report should mention manual-review")
	}
}

// TestConvertMIMWALSequential guards the extraction of the three information
// classes a raw MIMWAL SequentialWorkflow carries beyond its bare activity list:
// the workflow-level identity metadata, the ActivityDisplayName labels, and the
// per-activity ActivityExecutionCondition gates — now translated to FEEL and
// wired as exclusive skip gateways (as attribute and as property element).
func TestConvertMIMWALSequential(t *testing.T) {
	src, err := os.ReadFile("testdata/mimwal-sequential.xoml")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Convert(bytes.NewReader(src), "")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)

	bpmn := string(res.BPMN)
	wants := []string{
		`<atlas:mimWorkflow`,          // workflow-level metadata preserved
		`name="WorkflowDefinitionId"`, // the MIM identity GUIDs are kept
		`name="ActorId"`,
		`Version=2.20.723.0`, // the activity-library assembly/version survived
		// ActivityDisplayName drives the node label instead of the x:Name handle.
		`name="Global - Get CoreMIMConfiguration Parameter"`,
		// Each execution condition becomes a skip gateway carrying translated FEEL.
		`<exclusiveGateway`,
		`ausführen?`,
		`<conditionExpression>= (HasStructureResponsibilities) or (HasDisableInheritanceResponsibilities)</conditionExpression>`,
		`<conditionExpression>= (HasResponsibilities) and (PersonTransitionInGracePeriod)</conditionExpression>`,
		// The property-element condition and the //Target/… path both translate.
		`<conditionExpression>= (Target.PersonType.UseSuffixAccountName)</conditionExpression>`,
	}
	for _, w := range wants {
		if !strings.Contains(bpmn, w) {
			t.Errorf("generated BPMN is missing %q\n---\n%s", w, bpmn)
		}
	}

	// The raw WAL syntax must not leak into an executable conditionExpression.
	if strings.Contains(bpmn, "<conditionExpression>= ConvertToBoolean") {
		t.Errorf("WAL ConvertToBoolean leaked untranslated into a condition:\n%s", bpmn)
	}
	// The generated node must not be labelled by the meaningless x:Name handle.
	if strings.Contains(bpmn, `name="actionActivity6"`) {
		t.Errorf("node was labelled by x:Name handle instead of ActivityDisplayName:\n%s", bpmn)
	}

	// All three execution conditions translate, so each is a native skip gateway
	// and none is left for manual review.
	translated := 0
	for _, n := range res.Report.Notes {
		if strings.HasPrefix(n.Detail, "ActivityExecutionCondition translated to FEEL") {
			translated++
		}
	}
	if translated != 3 {
		t.Errorf("expected 3 translated execution conditions, got %d\n%s", translated, res.Report.String())
	}
	if res.Report.Count(StatusManualReview) != 0 {
		t.Errorf("no condition should be left for manual review, got %d\n%s",
			res.Report.Count(StatusManualReview), res.Report.String())
	}
}

// TestExecutionConditionFallback covers a condition the first-pass translator
// cannot render: the activity stays unguarded (no gateway) and the original is
// surfaced for manual review rather than emitted as broken FEEL.
func TestExecutionConditionFallback(t *testing.T) {
	src := `<SequentialWorkflow>
	  <UpdateResources ActivityDisplayName="Suffix" ActivityExecutionCondition="IIF(//WorkflowData/X, 1, 0) = 1" />
	</SequentialWorkflow>`
	res, err := Convert(strings.NewReader(src), "F")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
	bpmn := string(res.BPMN)
	if strings.Contains(bpmn, "<exclusiveGateway") {
		t.Errorf("an untranslatable condition must not produce a gateway:\n%s", bpmn)
	}
	if !strings.Contains(bpmn, "IIF(//WorkflowData/X, 1, 0) = 1") {
		t.Errorf("the original condition must be surfaced verbatim:\n%s", bpmn)
	}
	if res.Report.Count(StatusManualReview) != 1 {
		t.Errorf("untranslatable condition should be flagged once, got %d\n%s",
			res.Report.Count(StatusManualReview), res.Report.String())
	}
}

func TestConvertExportWrapper(t *testing.T) {
	// A miniature Export-FIMConfig shape: the workflow XOML lives, escaped, inside
	// an <AttributeType> named XOML. The converter must locate and unescape it.
	inner := `<SequentialWorkflow><ApprovalActivity Description="OK"/></SequentialWorkflow>`
	wrapper := `<ExportObject><ResourceManagementObject><ResourceManagementAttributes>` +
		`<AttributeType AttributeName="XOML"><Value>` + escapeXML(inner) + `</Value></AttributeType>` +
		`</ResourceManagementAttributes></ResourceManagementObject></ExportObject>`

	res, err := Convert(strings.NewReader(wrapper), "Wrapped")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
	if !strings.Contains(string(res.BPMN), "<userTask") {
		t.Errorf("expected the embedded approval to become a user task:\n%s", res.BPMN)
	}
}

func TestConvertEmptyWorkflow(t *testing.T) {
	res, err := Convert(strings.NewReader(`<SequentialWorkflow/>`), "Leer")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN) // start → end only, but still a valid process
	if !strings.Contains(string(res.BPMN), "<startEvent") || !strings.Contains(string(res.BPMN), "<endEvent") {
		t.Error("empty workflow should still yield a start and an end event")
	}
}

func TestConvertWhileLoop(t *testing.T) {
	src := `<SequentialWorkflow>
        <WhileActivity Description="Solange offen">
          <Condition>//WorkflowData["Open"] = true</Condition>
          <UpdateResourceActivity Description="Fortschritt"/>
        </WhileActivity>
      </SequentialWorkflow>`
	res, err := Convert(strings.NewReader(src), "")
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	validate(t, res.BPMN)
	if c := strings.Count(string(res.BPMN), "<exclusiveGateway"); c < 2 {
		t.Errorf("while loop should produce a decide/exit gateway pair, got %d gateways", c)
	}
}

// TestPreservedRawIsWellFormed guards the losslessness claim: an activity kept
// verbatim in <atlas:mimSource> must be re-parseable XML, including attribute
// values that contain quotes.
func TestPreservedRawIsWellFormed(t *testing.T) {
	n, err := decodeNode([]byte(`<PowerShellActivity Description='say "hi"' ScriptText="a &amp; b"/>`))
	if err != nil {
		t.Fatal(err)
	}
	raw := n.raw()
	if _, err := decodeNode([]byte(raw)); err != nil {
		t.Fatalf("preserved markup is not well-formed XML: %v\n%s", err, raw)
	}
}

func escapeXML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}
