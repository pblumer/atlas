package compiler

import (
	"strings"
	"testing"
)

// A service task bearing an <atlas:remedyConnector> extension is a BMC Remedy
// connector task (ADR-0106): it creates an entry (e.g. an incident) in a Remedy form
// through the AR System REST API via the job path, mirroring clio's and mail's
// registry-managed endpoint (the Remedy base URL and credentials live server-side,
// never in the model) while the form and its field values are model-authored like
// REST's.
const remedyConnectorBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:remedyConnector connector="helix-itsm" form="HPD:IncidentInterface_Create" resultVariable="incidentNumber">
          <atlas:remedyField name="Description" value="Disk full on server"/>
          <atlas:remedyField name="Impact" value="=impact"/>
        </atlas:remedyConnector>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseRemedyConnectorTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(remedyConnectorBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	node := cp.Node(task)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	d := cp.ConnectorTask(node.Detail)
	if got := cp.Intern(d.JobType); got != RemedyJobType {
		t.Errorf("jobType = %q, want %q", got, RemedyJobType)
	}
	if d.JobType != RemedyJobTypeIndex {
		t.Errorf("jobType index = %d, want the reserved RemedyJobTypeIndex %d", d.JobType, RemedyJobTypeIndex)
	}
	if got := cp.Intern(d.Connector); got != "helix-itsm" {
		t.Errorf("connector = %q, want helix-itsm", got)
	}
	if d.RemedyForm.Expr != nil || d.RemedyForm.Literal != "HPD:IncidentInterface_Create" {
		t.Errorf("form = %+v, want the literal form name", d.RemedyForm)
	}
	if got := cp.Intern(d.ResultVar); got != "incidentNumber" {
		t.Errorf("resultVariable = %q, want incidentNumber", got)
	}
	if len(d.RemedyFields) != 2 {
		t.Fatalf("fields = %d, want 2", len(d.RemedyFields))
	}
	if d.RemedyFields[0].Name != "Description" || d.RemedyFields[0].Val.Expr != nil || d.RemedyFields[0].Val.Literal != "Disk full on server" {
		t.Errorf("field[0] = %+v, want a literal Description", d.RemedyFields[0])
	}
	// A field value with a leading '=' compiles to a FEEL expression.
	if d.RemedyFields[1].Name != "Impact" || d.RemedyFields[1].Val.Expr == nil {
		t.Errorf("field[1] = %+v, want a FEEL Impact", d.RemedyFields[1])
	}
	// A remedy task leaves the REST-only URL and the clio-only coordinates unset.
	if d.Url.Expr != nil || d.Url.Literal != "" {
		t.Errorf("url = %+v, want empty for a remedy task", d.Url)
	}
	if cp.Intern(d.EventType) != "" || cp.Intern(d.Method) != "" {
		t.Errorf("clio/REST-only fields not empty for a remedy task: eventType=%q method=%q",
			cp.Intern(d.EventType), cp.Intern(d.Method))
	}
}

// The Remedy form and field values may be FEEL expressions (a leading '='), evaluated
// over the instance's variables at call time; a literal stays literal.
func TestParseRemedyConnectorFeelFields(t *testing.T) {
	const bpmn = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:remedyConnector connector="helix" form="=formName">
          <atlas:remedyField name="Summary" value="=&quot;Ticket for &quot; + customer.name"/>
        </atlas:remedyConnector>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	d := cp.ConnectorTask(cp.Node(task).Detail)
	if d.RemedyForm.Expr == nil {
		t.Errorf("form should be a FEEL expression, got literal %q", d.RemedyForm.Literal)
	}
	if len(d.RemedyFields) != 1 || d.RemedyFields[0].Val.Expr == nil {
		t.Errorf("field should be a FEEL expression, got %+v", d.RemedyFields)
	}
}

func TestParseRemedyConnectorErrors(t *testing.T) {
	wrap := func(inner string) string {
		return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>` + inner + `</bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	}
	cases := map[string]string{
		"missing connector":    `<atlas:remedyConnector form="HPD:Help Desk"><atlas:remedyField name="A" value="x"/></atlas:remedyConnector>`,
		"missing form":         `<atlas:remedyConnector connector="r"><atlas:remedyField name="A" value="x"/></atlas:remedyConnector>`,
		"malformed FEEL form":  `<atlas:remedyConnector connector="r" form="=("/>`,
		"malformed FEEL field": `<atlas:remedyConnector connector="r" form="F"><atlas:remedyField name="A" value="=("/></atlas:remedyConnector>`,
		"duplicate field":      `<atlas:remedyConnector connector="r" form="F"><atlas:remedyField name="A" value="x"/><atlas:remedyField name="A" value="y"/></atlas:remedyConnector>`,
	}
	for name, inner := range cases {
		if _, err := Parse(1, 1, strings.NewReader(wrap(inner))); err == nil {
			t.Errorf("%s: want a compile error, got nil", name)
		}
	}
}
