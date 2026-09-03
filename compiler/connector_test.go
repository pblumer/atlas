package compiler

import (
	"strings"
	"testing"
)

// A service task bearing an <atlas:clioConnector> extension is a clio worker
// task (ADR-0036): it delegates to a server-registered worker via the job
// path rather than to an external service-task worker.
const clioConnectorBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:clioConnector connector="orders-clio" subject="orders/new" eventType="OrderPlaced"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseClioConnectorTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(clioConnectorBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	node := cp.Node(task)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	d := cp.ConnectorTask(node.Detail)
	if got := cp.Intern(d.Connector); got != "orders-clio" {
		t.Errorf("worker = %q, want orders-clio", got)
	}
	if got := cp.Intern(d.Subject); got != "orders/new" {
		t.Errorf("subject = %q, want orders/new", got)
	}
	if got := cp.Intern(d.EventType); got != "OrderPlaced" {
		t.Errorf("eventType = %q, want OrderPlaced", got)
	}
	if got := cp.Intern(d.JobType); got != ClioWriteJobType {
		t.Errorf("jobType = %q, want %q", got, ClioWriteJobType)
	}
}

func TestParseClioConnectorErrors(t *testing.T) {
	// A clio task missing a required attribute fails to compile.
	const missingSubject = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:clioConnector connector="c" eventType="E"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	if _, err := Parse(1, 1, strings.NewReader(missingSubject)); err == nil {
		t.Fatal("Parse: want an error for a clio task missing subject, got nil")
	}
}

// clioTaskBPMN wraps a single <atlas:clioConnector> element (its attributes given
// as a raw string) in a Start → service task → End process.
func clioTaskBPMN(attrs string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
	                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:clioConnector ` + attrs + `/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

func clioDetail(t *testing.T, attrs string) (*CompiledProcess, *ConnectorTaskDetail) {
	t.Helper()
	cp, err := Parse(1, 1, strings.NewReader(clioTaskBPMN(attrs)))
	if err != nil {
		t.Fatalf("Parse(%q): %v", attrs, err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	return cp, cp.ConnectorTask(cp.Node(task).Detail)
}

// TestParseClioWriteDefaultOperation proves the operation attribute defaults to
// "write", so the original write-only element keeps compiling unchanged.
func TestParseClioWriteDefaultOperation(t *testing.T) {
	cp, d := clioDetail(t, `connector="c" subject="orders/new" eventType="OrderPlaced"`)
	if got := cp.Intern(d.JobType); got != ClioWriteJobType {
		t.Errorf("jobType = %q, want %q", got, ClioWriteJobType)
	}
}

func TestParseClioQueryTask(t *testing.T) {
	// run_query variant: a query string, no subject.
	cp, d := clioDetail(t, `connector="c" operation="query" query="select 1" resultVariable="rows"`)
	if got := cp.Intern(d.JobType); got != ClioQueryJobType {
		t.Errorf("jobType = %q, want %q", got, ClioQueryJobType)
	}
	if got := cp.Intern(d.ClioQuery); got != "select 1" {
		t.Errorf("query = %q, want 'select 1'", got)
	}
	if got := cp.Intern(d.ResultVar); got != "rows" {
		t.Errorf("resultVar = %q, want rows", got)
	}
	// get_state variant: a subject and a reduce spec, no query.
	cp, d = clioDetail(t, `connector="c" operation="query" subject="orders/42" reduceSpec="orderTotals" resultVariable="state"`)
	if got := cp.Intern(d.Subject); got != "orders/42" {
		t.Errorf("subject = %q, want orders/42", got)
	}
	if got := cp.Intern(d.ReduceSpec); got != "orderTotals" {
		t.Errorf("reduceSpec = %q, want orderTotals", got)
	}
}

func TestParseClioReadTask(t *testing.T) {
	cp, d := clioDetail(t, `connector="c" operation="read" subject="orders/new" limit="50" resultVariable="events"`)
	if got := cp.Intern(d.JobType); got != ClioReadJobType {
		t.Errorf("jobType = %q, want %q", got, ClioReadJobType)
	}
	if d.Limit != 50 {
		t.Errorf("limit = %d, want 50", d.Limit)
	}
	if got := cp.Intern(d.ResultVar); got != "events" {
		t.Errorf("resultVar = %q, want events", got)
	}
}

func TestParseClioOperationErrors(t *testing.T) {
	cases := []struct{ name, attrs string }{
		{"no worker", `subject="s" eventType="E"`},
		{"unknown op", `connector="c" operation="delete"`},
		{"query without query or subject", `connector="c" operation="query" resultVariable="r"`},
		{"query without resultVariable", `connector="c" operation="query" query="select 1"`},
		{"read without subject", `connector="c" operation="read" resultVariable="r"`},
		{"read without resultVariable", `connector="c" operation="read" subject="s"`},
		{"read invalid limit", `connector="c" operation="read" subject="s" resultVariable="r" limit="-2"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Parse(1, 1, strings.NewReader(clioTaskBPMN(c.attrs))); err == nil {
				t.Fatalf("Parse(%q): want an error, got nil", c.attrs)
			}
		})
	}
}
