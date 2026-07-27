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
	if d.Url.Expr != nil || d.Url.Literal != "https://api.example.com/customers" {
		t.Errorf("url = %+v, want the literal model URL", d.Url)
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
	// Headers keep model order; the empty-named row is dropped as incomplete.
	if len(d.Headers) != 2 || d.Headers[0].Name != "Accept" || d.Headers[0].Val.Literal != "application/json" ||
		d.Headers[1].Name != "X-Trace" || d.Headers[1].Val.Literal != "on" {
		t.Errorf("headers = %+v, want Accept + X-Trace literals only", d.Headers)
	}
	if len(d.Query) != 1 || d.Query[0].Name != "page" || d.Query[0].Val.Literal != "1" {
		t.Errorf("query = %+v, want page=1", d.Query)
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
	if len(d.Headers) != 0 || len(d.Query) != 0 || d.Auth != -1 {
		t.Errorf("headers/query/auth = %+v/%+v/%d, want none/none/-1", d.Headers, d.Query, d.Auth)
	}
	if d.Url.Expr != nil {
		t.Errorf("url expr = %v, want a literal url", d.Url.Expr)
	}
}

// A url, header, or query value with a leading '=' compiles to a FEEL expression
// evaluated over the instance's variables at call time (ADR-0067). A literal value
// stays literal.
func TestParseRestConnectorFeelFields(t *testing.T) {
	const bpmn = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:restConnector method="GET" url="=&quot;https://api/&quot; + string(customerId)">
          <atlas:httpHeader name="X-Trace" value="=traceId"/>
          <atlas:httpHeader name="Accept" value="application/json"/>
          <atlas:queryParam name="since" value="=order.since"/>
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
	if d.Url.Expr == nil {
		t.Errorf("url should be a compiled FEEL expression, got literal %q", d.Url.Literal)
	}
	// X-Trace is FEEL, Accept is literal.
	if d.Headers[0].Name != "X-Trace" || d.Headers[0].Val.Expr == nil {
		t.Errorf("X-Trace should be a FEEL header, got %+v", d.Headers[0])
	}
	if d.Headers[1].Name != "Accept" || d.Headers[1].Val.Expr != nil || d.Headers[1].Val.Literal != "application/json" {
		t.Errorf("Accept should be a literal header, got %+v", d.Headers[1])
	}
	if d.Query[0].Val.Expr == nil {
		t.Errorf("since query param should be a FEEL expression, got %+v", d.Query[0])
	}
}

// A malformed FEEL expression in a field fails the compile.
func TestParseRestConnectorFeelError(t *testing.T) {
	const bpmn = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:restConnector method="GET" url="=("/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	if _, err := Parse(1, 1, strings.NewReader(bpmn)); err == nil {
		t.Fatal("Parse: want an error for a malformed FEEL url expression, got nil")
	}
}
