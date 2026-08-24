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
