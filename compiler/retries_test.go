package compiler

import (
	"strings"
	"testing"
)

// Retries is a property of every task that creates a job (ADR-0134): the number of
// attempts the engine grants the job before its failure parks the token behind an
// incident (ADR-0061). It is authored where the task's implementation is configured
// — <zeebe:taskDefinition retries> on a job-worker service/send task, the connector
// extension's own retries attribute on a connector task, <atlas:jobScript retries>
// on a polyglot script task, <zeebe:calledDecision retries> on a business rule task
// — and every one of them defaults to defaultRetries when omitted.

// retriesBPMN wraps a single task's XML in a minimal executable process, so a test
// case only has to state the task it is about.
func retriesBPMN(task string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    ` + task + `
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

// compiledRetries compiles a one-task process and returns the retry budget the task
// node carries, whichever detail shape its kind uses.
func compiledRetries(t *testing.T, cp *CompiledProcess) int32 {
	t.Helper()
	node := cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target)
	switch node.Type {
	case TypeServiceTask:
		return cp.ServiceTask(node.Detail).Retries
	case TypeSendTask:
		return cp.SendTask(node.Detail).Retries
	case TypeConnectorTask:
		return cp.ConnectorTask(node.Detail).Retries
	case TypeScriptJobTask:
		return cp.ScriptJobTask(node.Detail).Retries
	case TypeBusinessRuleTask:
		return cp.BusinessRuleTask(node.Detail).Retries
	case TypeUserTask:
		return cp.UserTask(node.Detail).Retries
	}
	t.Fatalf("task node type %v carries no retry budget", node.Type)
	return 0
}

// TestTaskRetriesAuthored proves every job-backed task kind honours an authored
// retries count, and falls back to the default when it is omitted.
func TestTaskRetriesAuthored(t *testing.T) {
	tests := []struct {
		name string
		task string
		want int32
	}{
		{
			name: "service task",
			task: `<bpmn:serviceTask id="t"><bpmn:extensionElements><zeebe:taskDefinition type="pay" retries="5"/></bpmn:extensionElements></bpmn:serviceTask>`,
			want: 5,
		},
		{
			name: "service task default",
			task: `<bpmn:serviceTask id="t"><bpmn:extensionElements><zeebe:taskDefinition type="pay"/></bpmn:extensionElements></bpmn:serviceTask>`,
			want: defaultRetries,
		},
		{
			name: "send task",
			task: `<bpmn:sendTask id="t"><bpmn:extensionElements><zeebe:taskDefinition type="notify" retries="7"/></bpmn:extensionElements></bpmn:sendTask>`,
			want: 7,
		},
		{
			name: "script task (polyglot)",
			task: `<bpmn:scriptTask id="t"><bpmn:extensionElements><atlas:jobScript language="powershell" resultVariable="Greeting" retries="4">Write-Output "hi"</atlas:jobScript></bpmn:extensionElements></bpmn:scriptTask>`,
			want: 4,
		},
		{
			name: "script task (polyglot) default",
			task: `<bpmn:scriptTask id="t"><bpmn:extensionElements><atlas:jobScript language="powershell" resultVariable="Greeting">Write-Output "hi"</atlas:jobScript></bpmn:extensionElements></bpmn:scriptTask>`,
			want: defaultRetries,
		},
		{
			name: "business rule task",
			task: `<bpmn:businessRuleTask id="t"><bpmn:extensionElements><zeebe:calledDecision decisionId="d" resultVariable="r" retries="2"/></bpmn:extensionElements></bpmn:businessRuleTask>`,
			want: 2,
		},
		{
			name: "rest connector task",
			task: `<bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:restConnector method="GET" url="https://example.com" retries="6"/></bpmn:extensionElements></bpmn:serviceTask>`,
			want: 6,
		},
		{
			name: "rest connector task default",
			task: `<bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:restConnector method="GET" url="https://example.com"/></bpmn:extensionElements></bpmn:serviceTask>`,
			want: defaultRetries,
		},
		{
			name: "mail connector task",
			task: `<bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:mailConnector connector="office365" to="ops@example.com" retries="9"/></bpmn:extensionElements></bpmn:serviceTask>`,
			want: 9,
		},
		{
			name: "clio connector task",
			task: `<bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:clioConnector connector="c" subject="orders/new" eventType="Placed" retries="8"/></bpmn:extensionElements></bpmn:serviceTask>`,
			want: 8,
		},
		{
			name: "sharepoint connector task",
			task: `<bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:sharepointConnector connector="contoso" site="s" list="l" retries="2"/></bpmn:extensionElements></bpmn:serviceTask>`,
			want: 2,
		},
		{
			name: "remedy connector task",
			task: `<bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:remedyConnector connector="helix" form="HPD:Create" retries="4"/></bpmn:extensionElements></bpmn:serviceTask>`,
			want: 4,
		},
		{
			name: "webscrape connector task",
			task: `<bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:webscrapeConnector url="https://example.com" selector=".x" resultVariable="hits" retries="5"/></bpmn:extensionElements></bpmn:serviceTask>`,
			want: 5,
		},
		{
			name: "csv connector task",
			task: `<bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:csvConnector source="csvText" resultVariable="rows" retries="1"/></bpmn:extensionElements></bpmn:serviceTask>`,
			want: 1,
		},
		{
			name: "user connector task",
			task: `<bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:userConnector operation="disable" username="anna" retries="2"/></bpmn:extensionElements></bpmn:serviceTask>`,
			want: 2,
		},
		{
			// A connector task may still carry a <zeebe:taskDefinition> (hand-authored
			// models predate the connector attribute); the connector's own retries wins
			// when both are set, and the task definition's is honoured when it is not.
			name: "connector retries override the task definition",
			task: `<bpmn:serviceTask id="t"><bpmn:extensionElements><zeebe:taskDefinition type="ignored" retries="3"/><atlas:restConnector method="GET" url="https://example.com" retries="11"/></bpmn:extensionElements></bpmn:serviceTask>`,
			want: 11,
		},
		{
			name: "connector inherits the task definition's retries",
			task: `<bpmn:serviceTask id="t"><bpmn:extensionElements><zeebe:taskDefinition type="ignored" retries="12"/><atlas:restConnector method="GET" url="https://example.com"/></bpmn:extensionElements></bpmn:serviceTask>`,
			want: 12,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cp, err := Parse(1, 1, strings.NewReader(retriesBPMN(tc.task)))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if got := compiledRetries(t, cp); got != tc.want {
				t.Errorf("retries = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestTaskRetriesInvalid proves a retry budget that cannot produce a running job is
// refused at deploy time (invariant I5) rather than deploying a task whose job is
// created and never handed to a worker: a job is activatable only while it has
// retries left (state.PutJob), so retries below 1 would park a token silently — with
// no incident to resolve.
func TestTaskRetriesInvalid(t *testing.T) {
	tests := []struct {
		name string
		task string
		want string // substring of the expected compile error
	}{
		{
			name: "service task, not a number",
			task: `<bpmn:serviceTask id="t"><bpmn:extensionElements><zeebe:taskDefinition type="pay" retries="lots"/></bpmn:extensionElements></bpmn:serviceTask>`,
			want: `service task "t" has invalid retries "lots"`,
		},
		{
			name: "service task, zero",
			task: `<bpmn:serviceTask id="t"><bpmn:extensionElements><zeebe:taskDefinition type="pay" retries="0"/></bpmn:extensionElements></bpmn:serviceTask>`,
			want: `service task "t" has retries 0`,
		},
		{
			name: "service task, negative",
			task: `<bpmn:serviceTask id="t"><bpmn:extensionElements><zeebe:taskDefinition type="pay" retries="-1"/></bpmn:extensionElements></bpmn:serviceTask>`,
			want: `service task "t" has retries -1`,
		},
		{
			name: "script task, not a number",
			task: `<bpmn:scriptTask id="t"><bpmn:extensionElements><atlas:jobScript language="powershell" resultVariable="g" retries="soon">x</atlas:jobScript></bpmn:extensionElements></bpmn:scriptTask>`,
			want: `script task "t" has invalid retries "soon"`,
		},
		{
			name: "script task, zero",
			task: `<bpmn:scriptTask id="t"><bpmn:extensionElements><atlas:jobScript language="powershell" resultVariable="g" retries="0">x</atlas:jobScript></bpmn:extensionElements></bpmn:scriptTask>`,
			want: `script task "t" has retries 0`,
		},
		{
			name: "business rule task, zero",
			task: `<bpmn:businessRuleTask id="t"><bpmn:extensionElements><zeebe:calledDecision decisionId="d" retries="0"/></bpmn:extensionElements></bpmn:businessRuleTask>`,
			want: `business rule task "t" has retries 0`,
		},
		{
			name: "rest connector task, not a number",
			task: `<bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:restConnector method="GET" url="https://example.com" retries="many"/></bpmn:extensionElements></bpmn:serviceTask>`,
			want: `service task "t" has invalid retries "many"`,
		},
		{
			name: "csv connector task, zero",
			task: `<bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:csvConnector source="csvText" resultVariable="rows" retries="0"/></bpmn:extensionElements></bpmn:serviceTask>`,
			want: `service task "t" has retries 0`,
		},
		{
			name: "send task, zero",
			task: `<bpmn:sendTask id="t"><bpmn:extensionElements><zeebe:taskDefinition type="notify" retries="0"/></bpmn:extensionElements></bpmn:sendTask>`,
			want: `send task "t" has retries 0`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(retriesBPMN(tc.task)))
			if err == nil {
				t.Fatalf("Parse succeeded, want an error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}
