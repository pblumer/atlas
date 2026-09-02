package compiler

import (
	"strings"
	"testing"
)

// A service task bearing an <atlas:jiraConnector> extension is a Jira connector task
// (ADR-0201): it performs one issue-tracker operation against a
// server-registered Jira instance via the job path. The base URL and credential live
// server-side, like Remedy's and SharePoint's (ADR-0106/0141); only what the task is
// *about* — the operation and its values — is authored in the model.
const jiraConnectorBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:jiraConnector connector="acme" operation="create-issue" project="OPS"
                             issueType="Task" summary="Neuer Zugang" description="=begruendung"
                             resultVariable="ticket">
          <atlas:jiraField name="labels" value="=[&quot;atlas&quot;]"/>
          <atlas:jiraField name="priority" value="High"/>
        </atlas:jiraConnector>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseJiraConnectorTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(jiraConnectorBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	node := cp.Node(task)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	d := cp.ConnectorTask(node.Detail)
	if got := cp.Intern(d.JobType); got != JiraJobType {
		t.Errorf("jobType = %q, want %q", got, JiraJobType)
	}
	if d.JobType != JiraJobTypeIndex {
		t.Errorf("jobType index = %d, want the reserved JiraJobTypeIndex %d", d.JobType, JiraJobTypeIndex)
	}
	if got := cp.Intern(d.Connector); got != "acme" {
		t.Errorf("connector = %q, want acme", got)
	}
	if got := cp.Intern(d.JiraOp); got != "create-issue" {
		t.Errorf("operation = %q, want create-issue", got)
	}
	if d.JiraProject.Expr != nil || d.JiraProject.Literal != "OPS" {
		t.Errorf("project = %+v, want the literal OPS", d.JiraProject)
	}
	if d.JiraIssueType.Expr != nil || d.JiraIssueType.Literal != "Task" {
		t.Errorf("issueType = %+v, want the literal Task", d.JiraIssueType)
	}
	if d.JiraSummary.Expr != nil || d.JiraSummary.Literal != "Neuer Zugang" {
		t.Errorf("summary = %+v, want the literal summary", d.JiraSummary)
	}
	if d.JiraDescription.Expr == nil {
		t.Errorf("description = %+v, want a compiled FEEL expression", d.JiraDescription)
	}
	if got := cp.Intern(d.ResultVar); got != "ticket" {
		t.Errorf("resultVariable = %q, want ticket", got)
	}
	if len(d.JiraFields) != 2 {
		t.Fatalf("fields = %+v, want two extra issue fields", d.JiraFields)
	}
	if d.JiraFields[0].Name != "labels" || d.JiraFields[0].Val.Expr == nil {
		t.Errorf("field[0] = %+v, want a FEEL labels field", d.JiraFields[0])
	}
	if d.JiraFields[1].Name != "priority" || d.JiraFields[1].Val.Literal != "High" {
		t.Errorf("field[1] = %+v, want a literal priority field", d.JiraFields[1])
	}
	// A Jira task leaves the REST-only URL and the clio-only coordinates unset.
	if d.Url.Expr != nil || d.Url.Literal != "" {
		t.Errorf("url = %+v, want empty for a Jira task", d.Url)
	}
	if cp.Intern(d.EventType) != "" || cp.Intern(d.Method) != "" {
		t.Errorf("clio/REST-only fields not empty for a Jira task: eventType=%q method=%q",
			cp.Intern(d.EventType), cp.Intern(d.Method))
	}
}

// jiraTaskBPMN wraps one <atlas:jiraConnector …/> attribute list in a runnable process.
func jiraTaskBPMN(inner string) string {
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

// Every operation compiles from the values its row in the table requires, and each
// carries only the fields that operation is about.
func TestParseJiraConnectorOperations(t *testing.T) {
	cases := []struct {
		op    string
		attrs string
		check func(t *testing.T, cp *CompiledProcess, d *ConnectorTaskDetail)
	}{
		{
			op:    "get-issue",
			attrs: `issueKey="OPS-42" resultVariable="ticket"`,
			check: func(t *testing.T, cp *CompiledProcess, d *ConnectorTaskDetail) {
				if d.JiraIssue.Literal != "OPS-42" {
					t.Errorf("issueKey = %+v, want OPS-42", d.JiraIssue)
				}
			},
		},
		{
			op:    "update-issue",
			attrs: `issueKey="=ticket.key" summary="Neu"`,
			check: func(t *testing.T, cp *CompiledProcess, d *ConnectorTaskDetail) {
				if d.JiraIssue.Expr == nil {
					t.Errorf("issueKey = %+v, want a FEEL expression", d.JiraIssue)
				}
			},
		},
		{
			op:    "transition-issue",
			attrs: `issueKey="OPS-42" transition="Done" comment="erledigt"`,
			check: func(t *testing.T, cp *CompiledProcess, d *ConnectorTaskDetail) {
				if d.JiraTransition.Literal != "Done" || d.JiraComment.Literal != "erledigt" {
					t.Errorf("transition/comment = %+v / %+v", d.JiraTransition, d.JiraComment)
				}
			},
		},
		{
			op:    "add-comment",
			attrs: `issueKey="OPS-42" comment="=antwort" resultVariable="kommentar"`,
			check: func(t *testing.T, cp *CompiledProcess, d *ConnectorTaskDetail) {
				if d.JiraComment.Expr == nil {
					t.Errorf("comment = %+v, want a FEEL expression", d.JiraComment)
				}
			},
		},
		{
			op:    "assign-issue",
			attrs: `issueKey="OPS-42" assignee="=konto.accountId"`,
			check: func(t *testing.T, cp *CompiledProcess, d *ConnectorTaskDetail) {
				if d.JiraAssignee.Expr == nil {
					t.Errorf("assignee = %+v, want a FEEL expression", d.JiraAssignee)
				}
			},
		},
		{
			op:    "search-users",
			attrs: `query="=antragsteller.mail" project="OPS" maxResults="10" resultVariable="konten"`,
			check: func(t *testing.T, cp *CompiledProcess, d *ConnectorTaskDetail) {
				if d.JiraQuery.Expr == nil {
					t.Errorf("query = %+v, want a FEEL expression", d.JiraQuery)
				}
				// The one operation that takes a project without an issue type: it
				// restricts the search to the accounts that project can assign.
				if d.JiraProject.Literal != "OPS" {
					t.Errorf("project = %+v, want the literal OPS", d.JiraProject)
				}
				if d.JiraMaxResults != 10 {
					t.Errorf("maxResults = %d, want 10", d.JiraMaxResults)
				}
			},
		},
		{
			op:    "search",
			attrs: `jql="project = OPS AND status = Open" maxResults="25" resultVariable="offen"`,
			check: func(t *testing.T, cp *CompiledProcess, d *ConnectorTaskDetail) {
				if d.JiraJQL.Literal != "project = OPS AND status = Open" {
					t.Errorf("jql = %+v", d.JiraJQL)
				}
				if d.JiraMaxResults != 25 {
					t.Errorf("maxResults = %d, want 25", d.JiraMaxResults)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			bpmn := jiraTaskBPMN(`<atlas:jiraConnector connector="acme" operation="` + tc.op + `" ` + tc.attrs + `/>`)
			cp, err := Parse(1, 1, strings.NewReader(bpmn))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
			d := cp.ConnectorTask(cp.Node(task).Detail)
			if got := cp.Intern(d.JiraOp); got != tc.op {
				t.Fatalf("operation = %q, want %q", got, tc.op)
			}
			tc.check(t, cp, d)
		})
	}
}

// A search that authors no maxResults compiles the connector's default rather than
// leaving the runtime to interpret a zero (invariant I5).
func TestParseJiraConnectorSearchDefaultsItsPageSize(t *testing.T) {
	bpmn := jiraTaskBPMN(`<atlas:jiraConnector connector="acme" operation="search" jql="project = OPS" resultVariable="offen"/>`)
	cp, err := Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	d := cp.ConnectorTask(cp.Node(task).Detail)
	if d.JiraMaxResults != jiraDefaultMaxResults {
		t.Errorf("maxResults = %d, want the compiled default %d", d.JiraMaxResults, jiraDefaultMaxResults)
	}
}

func TestParseJiraConnectorErrors(t *testing.T) {
	cases := map[string]string{
		"missing connector":       `<atlas:jiraConnector operation="get-issue" issueKey="OPS-1"/>`,
		"missing operation":       `<atlas:jiraConnector connector="acme" issueKey="OPS-1"/>`,
		"unknown operation":       `<atlas:jiraConnector connector="acme" operation="explode" issueKey="OPS-1"/>`,
		"create without project":  `<atlas:jiraConnector connector="acme" operation="create-issue" issueType="Task" summary="s"/>`,
		"create without type":     `<atlas:jiraConnector connector="acme" operation="create-issue" project="OPS" summary="s"/>`,
		"create without summary":  `<atlas:jiraConnector connector="acme" operation="create-issue" project="OPS" issueType="Task"/>`,
		"get without issue":       `<atlas:jiraConnector connector="acme" operation="get-issue" resultVariable="t"/>`,
		"get without result":      `<atlas:jiraConnector connector="acme" operation="get-issue" issueKey="OPS-1"/>`,
		"update without change":   `<atlas:jiraConnector connector="acme" operation="update-issue" issueKey="OPS-1"/>`,
		"transition without id":   `<atlas:jiraConnector connector="acme" operation="transition-issue" issueKey="OPS-1"/>`,
		"comment without body":    `<atlas:jiraConnector connector="acme" operation="add-comment" issueKey="OPS-1" resultVariable="k"/>`,
		"assign without account":  `<atlas:jiraConnector connector="acme" operation="assign-issue" issueKey="OPS-1"/>`,
		"search without jql":      `<atlas:jiraConnector connector="acme" operation="search" resultVariable="offen"/>`,
		"users without query":     `<atlas:jiraConnector connector="acme" operation="search-users" resultVariable="konten"/>`,
		"users without result":    `<atlas:jiraConnector connector="acme" operation="search-users" query="patrick"/>`,
		"users with an issueType": `<atlas:jiraConnector connector="acme" operation="search-users" query="patrick" issueType="Task" resultVariable="konten"/>`,
		"users with a jql":        `<atlas:jiraConnector connector="acme" operation="search-users" query="patrick" jql="project = OPS" resultVariable="konten"/>`,
		"query on a search":       `<atlas:jiraConnector connector="acme" operation="search" jql="project = OPS" query="patrick" resultVariable="offen"/>`,
		"query on a create":       `<atlas:jiraConnector connector="acme" operation="create-issue" project="OPS" issueType="Task" summary="s" query="patrick"/>`,
		"search without result":   `<atlas:jiraConnector connector="acme" operation="search" jql="project = OPS"/>`,
		"result on an assign":     `<atlas:jiraConnector connector="acme" operation="assign-issue" issueKey="OPS-1" assignee="a" resultVariable="x"/>`,
		"search with bad max":     `<atlas:jiraConnector connector="acme" operation="search" jql="project = OPS" maxResults="viele" resultVariable="offen"/>`,
		"negative max":            `<atlas:jiraConnector connector="acme" operation="search" jql="project = OPS" maxResults="-1" resultVariable="offen"/>`,
		"project on a search":     `<atlas:jiraConnector connector="acme" operation="search" jql="project = OPS" project="OPS" resultVariable="offen"/>`,
		"comment on a search":     `<atlas:jiraConnector connector="acme" operation="search" jql="project = OPS" comment="hallo" resultVariable="offen"/>`,
		"fields on a search":      `<atlas:jiraConnector connector="acme" operation="search" jql="project = OPS" resultVariable="offen"><atlas:jiraField name="labels" value="x"/></atlas:jiraConnector>`,
		"issue key on a search":   `<atlas:jiraConnector connector="acme" operation="search" jql="project = OPS" issueKey="OPS-1" resultVariable="offen"/>`,
		"jql on a create":         `<atlas:jiraConnector connector="acme" operation="create-issue" project="OPS" issueType="Task" summary="s" jql="project = OPS"/>`,
		"malformed FEEL summary":  `<atlas:jiraConnector connector="acme" operation="create-issue" project="OPS" issueType="Task" summary="=("/>`,
		"malformed FEEL field":    `<atlas:jiraConnector connector="acme" operation="create-issue" project="OPS" issueType="Task" summary="s"><atlas:jiraField name="labels" value="=("/></atlas:jiraConnector>`,
		"duplicate field":         `<atlas:jiraConnector connector="acme" operation="create-issue" project="OPS" issueType="Task" summary="s"><atlas:jiraField name="labels" value="a"/><atlas:jiraField name="labels" value="b"/></atlas:jiraConnector>`,
	}
	for name, inner := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(1, 1, strings.NewReader(jiraTaskBPMN(inner))); err == nil {
				t.Fatalf("Parse accepted %s", name)
			}
		})
	}
}

// The Modeler serializes the moddle type JiraConnector as <atlas:jiraConnector>, which
// is the spelling the compiler reads — the guard TestCompilerReadsWhatTheModelerWrites
// states repo-wide, asserted here on the connector's own round trip.
func TestParseJiraConnectorRetries(t *testing.T) {
	bpmn := jiraTaskBPMN(`<atlas:jiraConnector connector="acme" operation="get-issue" issueKey="OPS-1" resultVariable="t" retries="7"/>`)
	cp, err := Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	if d := cp.ConnectorTask(cp.Node(task).Detail); d.Retries != 7 {
		t.Errorf("retries = %d, want 7", d.Retries)
	}
}
