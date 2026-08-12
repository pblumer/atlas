package compiler

import (
	"strings"
	"testing"
)

// A service task bearing an <atlas:webscrapeConnector> extension is a web-scraping
// connector task (ADR-0117): it fetches the model-authored URL and extracts the
// elements matching a CSS selector via the job path rather than delegating to an
// external service-task worker.
const webscrapeConnectorBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:webscrapeConnector url="https://example.com/news" selector=".headline a" attribute="href" resultVariable="links"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseWebScrapeConnectorTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(webscrapeConnectorBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	node := cp.Node(task)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	d := cp.ConnectorTask(node.Detail)
	if d.Url.Expr != nil || d.Url.Literal != "https://example.com/news" {
		t.Errorf("url = %+v, want the literal model URL", d.Url)
	}
	if d.ScrapeSelector.Expr != nil || d.ScrapeSelector.Literal != ".headline a" {
		t.Errorf("selector = %+v, want the literal selector", d.ScrapeSelector)
	}
	if got := cp.Intern(d.ScrapeAttribute); got != "href" {
		t.Errorf("attribute = %q, want href", got)
	}
	if got := cp.Intern(d.ResultVar); got != "links" {
		t.Errorf("resultVar = %q, want links", got)
	}
	if got := cp.Intern(d.JobType); got != WebScrapeJobType {
		t.Errorf("jobType = %q, want %q", got, WebScrapeJobType)
	}
	if d.JobType != WebScrapeJobTypeIndex {
		t.Errorf("jobType index = %d, want the reserved WebScrapeJobTypeIndex %d", d.JobType, WebScrapeJobTypeIndex)
	}
	// A scrape task leaves the clio/REST-only fields unset (-1 → "").
	if cp.Intern(d.Connector) != "" || cp.Intern(d.Method) != "" || cp.Intern(d.Subject) != "" {
		t.Errorf("unrelated fields not empty for a scrape task: connector=%q method=%q subject=%q",
			cp.Intern(d.Connector), cp.Intern(d.Method), cp.Intern(d.Subject))
	}
}

// A scrape task's url and selector honor the fx toggle: a leading '=' marks a FEEL
// expression compiled at deploy time; attribute is optional (empty → text content).
func TestParseWebScrapeConnectorFeel(t *testing.T) {
	const bpmn = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:webscrapeConnector url="=&quot;https://example.com/&quot; + slug" selector="=sel" resultVariable="rows"/>
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
		t.Errorf("url = %+v, want a compiled FEEL expression", d.Url)
	}
	if d.ScrapeSelector.Expr == nil {
		t.Errorf("selector = %+v, want a compiled FEEL expression", d.ScrapeSelector)
	}
	// An omitted attribute is unset (-1 → ""), meaning "extract text content".
	if got := cp.Intern(d.ScrapeAttribute); got != "" {
		t.Errorf("attribute = %q, want empty (text content)", got)
	}
}

func TestParseWebScrapeConnectorErrors(t *testing.T) {
	cases := []struct {
		name    string
		ext     string
		wantSub string
	}{
		{"no url", `<atlas:webscrapeConnector selector=".x" resultVariable="r"/>`, "needs a url"},
		{"no selector", `<atlas:webscrapeConnector url="https://x" resultVariable="r"/>`, "needs a selector"},
		{"no resultVariable", `<atlas:webscrapeConnector url="https://x" selector=".y"/>`, "needs a resultVariable"},
		{"bad feel url", `<atlas:webscrapeConnector url="=" selector=".y" resultVariable="r"/>`, "empty FEEL expression"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bpmn := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>` + tc.ext + `</bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
			_, err := Parse(1, 1, strings.NewReader(bpmn))
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("Parse error = %v, want one containing %q", err, tc.wantSub)
			}
		})
	}
}
