package compiler

import (
	"strings"
	"testing"
	"time"
)

func mcpTask(attrs string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/><endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
	    <sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
	    <serviceTask id="t"><extensionElements><atlas:mcpConnector ` + attrs + `/></extensionElements></serviceTask>
	  </process>
	</definitions>`
}

// An MCP task compiles to a job of its own type carrying the Worker, the one tool, the
// arguments as an expression and the bound on the call (ADR-draft-mcp-worker).
func TestAnMcpTaskCompiles(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(mcpTask(
		`connector="monitoring" tool="search_issues" input='{"query":"=meldung","limit":5}' resultVariable="treffer" timeout="PT30S"`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	d := cp.ConnectorTask(nodeByBpmnId(t, cp, "t").Detail)
	if d.JobType != MCPJobTypeIndex || cp.Intern(d.Connector) != "monitoring" || cp.Intern(d.McpTool) != "search_issues" ||
		cp.Intern(d.ResultVar) != "treffer" || d.McpTimeoutNanos != int64(30*time.Second) || d.McpInput.Expr == nil {
		t.Errorf("detail = %+v", d)
	}
	if got := d.McpInput.Expr.Inputs(); len(got) != 1 || got[0] != "meldung" {
		t.Errorf("the arguments read %v, want the one variable their =expression names", got)
	}

	// A tool that takes nothing, and a Worker named by an expression.
	cp, err = Parse(1, 1, strings.NewReader(mcpTask(`connector="=server" tool="ping"`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	d = cp.ConnectorTask(nodeByBpmnId(t, cp, "t").Detail)
	if d.McpInput.Expr != nil || d.McpConnector.Expr == nil || d.McpTimeoutNanos != 0 || d.ResultVar != -1 {
		t.Errorf("detail = %+v", d)
	}
}

func TestAnMcpTaskThatCannotRunIsRefused(t *testing.T) {
	for name, tc := range map[string]struct{ attrs, want string }{
		"no worker":        {`tool="x"`, "needs a connector attribute"},
		"no tool":          {`connector="m"`, "needs a tool"},
		"input not JSON":   {`connector="m" tool="x" input="nonsense"`, "not JSON"},
		"input not object": {`connector="m" tool="x" input="[1]"`, "must be a JSON object"},
		"bad expression":   {`connector="m" tool="x" input='{"a":"=1 +"}'`, "did not compile"},
		"timeout no ISO":   {`connector="m" tool="x" timeout="30"`, `invalid timeout "30"`},
		"timeout too long": {`connector="m" tool="x" timeout="P3D"`, "outside"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(1, 1, strings.NewReader(mcpTask(tc.attrs))); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Parse error = %v, want one naming %q", err, tc.want)
			}
		})
	}
}
