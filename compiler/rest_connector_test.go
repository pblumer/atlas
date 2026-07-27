package compiler

import (
	"strings"
	"testing"
)

// A service task bearing an <atlas:restConnector> extension is an HTTP-REST
// connector task (ADR-0067): it calls the model-authored URL via the job path
// rather than delegating to an external service-task worker.
const restConnectorBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:restConnector method="post" url="https://api.example.com/customers" resultVariable="created"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseRestConnectorTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(restConnectorBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	node := cp.Node(task)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	d := cp.ConnectorTask(node.Detail)
	if got := cp.Intern(d.Method); got != "POST" { // upper-cased at deploy time
		t.Errorf("method = %q, want POST", got)
	}
	if got := cp.Intern(d.Url); got != "https://api.example.com/customers" {
		t.Errorf("url = %q, want the model URL", got)
	}
	if got := cp.Intern(d.ResultVar); got != "created" {
		t.Errorf("resultVar = %q, want created", got)
	}
	if got := cp.Intern(d.JobType); got != RestJobType {
		t.Errorf("jobType = %q, want %q", got, RestJobType)
	}
	if d.JobType != RestJobTypeIndex {
		t.Errorf("jobType index = %d, want the reserved RestJobTypeIndex %d", d.JobType, RestJobTypeIndex)
	}
	// A REST task leaves the clio-only coordinates unset (-1 → "").
	if cp.Intern(d.Subject) != "" || cp.Intern(d.EventType) != "" || cp.Intern(d.Connector) != "" {
		t.Errorf("clio fields not empty for a REST task: connector=%q subject=%q eventType=%q",
			cp.Intern(d.Connector), cp.Intern(d.Subject), cp.Intern(d.EventType))
	}
}

// A REST connector task with no method defaults to GET, and no result variable is
// allowed (the response is discarded).
func TestParseRestConnectorDefaults(t *testing.T) {
	const noMethod = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:restConnector url="https://api.example.com/customers/1"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := Parse(1, 1, strings.NewReader(noMethod))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	d := cp.ConnectorTask(cp.Node(task).Detail)
	if got := cp.Intern(d.Method); got != "GET" {
		t.Errorf("method = %q, want GET (the default)", got)
	}
	if got := cp.Intern(d.ResultVar); got != "" {
		t.Errorf("resultVar = %q, want empty (discarded)", got)
	}
}

func TestParseRestConnectorErrors(t *testing.T) {
	// A REST connector task missing its url fails to compile.
	const missingURL = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:restConnector method="POST"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	if _, err := Parse(1, 1, strings.NewReader(missingURL)); err == nil {
		t.Fatal("Parse: want an error for a rest connector task missing url, got nil")
	}

	// An unsupported HTTP method fails to compile.
	const badMethod = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:restConnector method="TRACE" url="https://x"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	if _, err := Parse(1, 1, strings.NewReader(badMethod)); err == nil {
		t.Fatal("Parse: want an error for an unsupported HTTP method, got nil")
	}
}

// A REST connector task carries request headers, query parameters, and
// authentication (ADR-0067). Headers/query compile to canonical JSON objects; auth
// compiles to a JSON object that references a server-side secret, never a value.
func TestParseRestConnectorHeadersQueryAuth(t *testing.T) {
	const bpmn = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:restConnector method="GET" url="https://api.example.com/todos"
                             authType="bearer" authSecret="TODO_TOKEN">
          <atlas:httpHeader name="Accept" value="application/json"/>
          <atlas:httpHeader name="X-Trace" value="on"/>
          <atlas:httpHeader name="" value="dropped"/>
          <atlas:queryParam name="page" value="1"/>
        </atlas:restConnector>
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
	if got := cp.Intern(d.Headers); got != `{"Accept":"application/json","X-Trace":"on"}` {
		t.Errorf("headers = %q, want the canonical JSON object", got)
	}
	if got := cp.Intern(d.Query); got != `{"page":"1"}` {
		t.Errorf("query = %q, want the canonical JSON object", got)
	}
	// A header with an empty name is an incomplete row and is dropped, not stored.
	if strings.Contains(cp.Intern(d.Headers), `""`) {
		t.Errorf("headers %q should not contain an empty-named entry", cp.Intern(d.Headers))
	}
	if got := cp.Intern(d.Auth); got != `{"type":"bearer","secretRef":"TODO_TOKEN"}` {
		t.Errorf("auth = %q, want a bearer auth referencing the secret", got)
	}
}

// Auth needs a secret reference; a duplicate header and an unknown scheme are
// modeling errors that fail the compile.
func TestParseRestConnectorAuthAndHeaderErrors(t *testing.T) {
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
		"bearer without secret": `<atlas:restConnector method="GET" url="https://x" authType="bearer"/>`,
		"apiKey without header": `<atlas:restConnector method="GET" url="https://x" authType="apiKey" authSecret="K"/>`,
		"unknown scheme":        `<atlas:restConnector method="GET" url="https://x" authType="oauth2" authSecret="K"/>`,
		"duplicate header": `<atlas:restConnector method="GET" url="https://x">
			<atlas:httpHeader name="Accept" value="a"/><atlas:httpHeader name="Accept" value="b"/></atlas:restConnector>`,
	}
	for name, inner := range cases {
		if _, err := Parse(1, 1, strings.NewReader(wrap(inner))); err == nil {
			t.Errorf("%s: want a compile error, got nil", name)
		}
	}
}

// Auth defaults: no authType compiles to no auth (-1), and headers/query default
// to none, so an unauthenticated plain GET stays lean.
func TestParseRestConnectorNoAuthNoMaps(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(restConnectorBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	d := cp.ConnectorTask(cp.Node(task).Detail)
	if d.Headers != -1 || d.Query != -1 || d.Auth != -1 {
		t.Errorf("headers/query/auth = %d/%d/%d, want -1/-1/-1 (none)", d.Headers, d.Query, d.Auth)
	}
}
