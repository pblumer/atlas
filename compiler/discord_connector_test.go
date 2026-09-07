package compiler

import (
	"strings"
	"testing"
)

// A service task bearing an <atlas:discordConnector> extension is a Discord task
// (ADR-0258): it performs one chat operation against a configured
// Discord Worker via the job path. The bot token lives server-side, like Jira's
// credential and Google's (ADR-0201/0235); only what the task is *about* — the
// operation and its values — is authored in the model.
const discordConnectorBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:discordConnector connector="team" operation="send-message"
                                channel="123456789012345678" content="=meldung"
                                resultVariable="nachricht">
          <atlas:discordField name="embeds" value="=[{&quot;title&quot;: &quot;Antrag&quot;}]"/>
          <atlas:discordField name="tts" value="false"/>
        </atlas:discordConnector>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseDiscordConnectorTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(discordConnectorBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	node := cp.Node(task)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	d := cp.ConnectorTask(node.Detail)
	if got := cp.Intern(d.JobType); got != DiscordJobType {
		t.Errorf("jobType = %q, want %q", got, DiscordJobType)
	}
	if d.JobType != DiscordJobTypeIndex {
		t.Errorf("jobType index = %d, want the reserved DiscordJobTypeIndex %d", d.JobType, DiscordJobTypeIndex)
	}
	if got := cp.Intern(d.Connector); got != "team" {
		t.Errorf("worker = %q, want team", got)
	}
	if got := cp.Intern(d.DiscordOp); got != "send-message" {
		t.Errorf("operation = %q, want send-message", got)
	}
	if d.DiscordChannel.Expr != nil || d.DiscordChannel.Literal != "123456789012345678" {
		t.Errorf("channel = %+v, want the literal channel id", d.DiscordChannel)
	}
	if d.DiscordContent.Expr == nil {
		t.Errorf("content = %+v, want a compiled FEEL expression", d.DiscordContent)
	}
	if got := cp.Intern(d.ResultVar); got != "nachricht" {
		t.Errorf("resultVariable = %q, want nachricht", got)
	}
	if len(d.DiscordFields) != 2 {
		t.Fatalf("fields = %+v, want two extra body properties", d.DiscordFields)
	}
	if d.DiscordFields[0].Name != "embeds" || d.DiscordFields[0].Val.Expr == nil {
		t.Errorf("field[0] = %+v, want a FEEL embeds field", d.DiscordFields[0])
	}
	if d.DiscordFields[1].Name != "tts" || d.DiscordFields[1].Val.Literal != "false" {
		t.Errorf("field[1] = %+v, want a literal tts field", d.DiscordFields[1])
	}
	// send-message takes no list bounds, so the cap stays zero rather than picking up
	// the list default — a value the worker would then send on a call that has no limit.
	if d.DiscordMaxResults != 0 {
		t.Errorf("maxResults = %d, want 0 for an operation that does not page", d.DiscordMaxResults)
	}
	// A Discord task leaves the REST-only URL and the clio-only coordinates unset.
	if d.Url.Expr != nil || d.Url.Literal != "" {
		t.Errorf("url = %+v, want empty for a Discord task", d.Url)
	}
	if cp.Intern(d.EventType) != "" || cp.Intern(d.Method) != "" {
		t.Errorf("clio/REST-only fields not empty for a Discord task: eventType=%q method=%q",
			cp.Intern(d.EventType), cp.Intern(d.Method))
	}
}

// discordTaskBPMN wraps one <atlas:discordConnector …/> attribute list in a runnable
// process.
func discordTaskBPMN(inner string) string {
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
func TestParseDiscordConnectorOperations(t *testing.T) {
	cases := []struct {
		op    string
		attrs string
		check func(t *testing.T, cp *CompiledProcess, d *ConnectorTaskDetail)
	}{
		{
			op:    "edit-message",
			attrs: `channel="42" messageId="=nachricht.id" content="Erledigt"`,
			check: func(t *testing.T, cp *CompiledProcess, d *ConnectorTaskDetail) {
				if d.DiscordMessage.Expr == nil {
					t.Errorf("messageId = %+v, want a FEEL expression", d.DiscordMessage)
				}
				if d.DiscordContent.Literal != "Erledigt" {
					t.Errorf("content = %+v, want the literal", d.DiscordContent)
				}
			},
		},
		{
			op:    "delete-message",
			attrs: `channel="42" messageId="7"`,
			check: func(t *testing.T, cp *CompiledProcess, d *ConnectorTaskDetail) {
				if cp.Intern(d.ResultVar) != "" {
					t.Errorf("resultVariable = %q, want none: Discord answers a delete with no content", cp.Intern(d.ResultVar))
				}
			},
		},
		{
			op:    "get-message",
			attrs: `channel="42" messageId="7" resultVariable="nachricht"`,
			check: func(t *testing.T, cp *CompiledProcess, d *ConnectorTaskDetail) {
				if d.DiscordMessage.Literal != "7" {
					t.Errorf("messageId = %+v, want 7", d.DiscordMessage)
				}
			},
		},
		{
			op:    "list-messages",
			attrs: `channel="42" after="=letzteGelesen" maxResults="25" resultVariable="nachrichten"`,
			check: func(t *testing.T, cp *CompiledProcess, d *ConnectorTaskDetail) {
				if d.DiscordAfter.Expr == nil {
					t.Errorf("after = %+v, want a FEEL expression", d.DiscordAfter)
				}
				if d.DiscordMaxResults != 25 {
					t.Errorf("maxResults = %d, want 25", d.DiscordMaxResults)
				}
			},
		},
		{
			op: "create-thread",
			// The one operation that takes a message optionally: naming one hangs the
			// thread under it rather than starting a standalone one.
			attrs: `channel="42" messageId="7" name="=&quot;Antrag &quot; + vorgang" resultVariable="faden"`,
			check: func(t *testing.T, cp *CompiledProcess, d *ConnectorTaskDetail) {
				if d.DiscordMessage.Literal != "7" {
					t.Errorf("messageId = %+v, want the parent message", d.DiscordMessage)
				}
				if d.DiscordName.Expr == nil {
					t.Errorf("name = %+v, want a FEEL expression", d.DiscordName)
				}
			},
		},
		{
			op: "create-thread",
			// No result variable: opening a thread under a notice so humans have
			// somewhere to discuss it is a complete act, and whether the process then
			// posts into the thread is the model's business — the same rule
			// create-issue follows.
			attrs: `channel="42" messageId="7" name="Fall 9"`,
			check: func(t *testing.T, cp *CompiledProcess, d *ConnectorTaskDetail) {
				if cp.Intern(d.ResultVar) != "" {
					t.Errorf("resultVariable = %q, want none", cp.Intern(d.ResultVar))
				}
			},
		},
		{
			op:    "create-thread",
			attrs: `channel="42" name="Fall 9" resultVariable="faden"`,
			check: func(t *testing.T, cp *CompiledProcess, d *ConnectorTaskDetail) {
				// No message: a standalone thread in the channel, which the worker
				// sends to Discord's other endpoint.
				if d.DiscordMessage.Expr != nil || d.DiscordMessage.Literal != "" {
					t.Errorf("messageId = %+v, want none for a standalone thread", d.DiscordMessage)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.op+" "+tc.attrs, func(t *testing.T) {
			cp, err := Parse(1, 1, strings.NewReader(discordTaskBPMN(
				`<atlas:discordConnector connector="team" operation="`+tc.op+`" `+tc.attrs+`/>`)))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
			d := cp.ConnectorTask(cp.Node(task).Detail)
			if got := cp.Intern(d.DiscordOp); got != tc.op {
				t.Fatalf("operation = %q, want %q", got, tc.op)
			}
			tc.check(t, cp, d)
		})
	}
}

// A list that authors no cap gets the default at deploy, so the runtime interprets
// nothing (I5).
func TestDiscordListDefaultsItsCap(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(discordTaskBPMN(
		`<atlas:discordConnector connector="team" operation="list-messages" channel="42" resultVariable="n"/>`)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	d := cp.ConnectorTask(cp.Node(task).Detail)
	if d.DiscordMaxResults != discordDefaultMaxResults {
		t.Errorf("maxResults = %d, want the default %d", d.DiscordMaxResults, discordDefaultMaxResults)
	}
}

// What the compiler refuses, and why each refusal is worth having: a value on an
// operation that does not use it would be dropped silently at call time, and a cap the
// endpoint will not accept would come back as a 400 naming a field the author never
// typed.
func TestDiscordConnectorRefusals(t *testing.T) {
	cases := []struct {
		name  string
		attrs string
		want  string
	}{
		{"no worker", `operation="send-message" channel="42" content="x"`, "needs a worker"},
		{"no operation", `connector="team" channel="42"`, "needs an operation"},
		{"unknown operation", `connector="team" operation="explodieren" channel="42"`, "unknown operation"},
		{"missing channel", `connector="team" operation="send-message" content="x"`, "needs a channel"},
		{"missing content", `connector="team" operation="send-message" channel="42"`, "needs a content"},
		{"missing thread name", `connector="team" operation="create-thread" channel="42" resultVariable="f"`, "needs a name"},
		{"missing result", `connector="team" operation="get-message" channel="42" messageId="7"`, "needs a resultVariable"},
		{
			"content on a delete",
			`connector="team" operation="delete-message" channel="42" messageId="7" content="x"`,
			"does not use content",
		},
		{
			"result on a delete",
			`connector="team" operation="delete-message" channel="42" messageId="7" resultVariable="n"`,
			"does not use resultVariable",
		},
		{
			"after on a send",
			`connector="team" operation="send-message" channel="42" content="x" after="7"`,
			"does not use after",
		},
		{
			"message id on a send",
			`connector="team" operation="send-message" channel="42" content="x" messageId="7"`,
			"does not use messageId",
		},
		{
			"thread name on a send",
			`connector="team" operation="send-message" channel="42" content="x" name="Fall 9"`,
			"does not use name",
		},
		{"non-numeric cap", `connector="team" operation="list-messages" channel="42" maxResults="viele" resultVariable="n"`, "non-numeric maxResults"},
		{"cap of zero", `connector="team" operation="list-messages" channel="42" maxResults="0" resultVariable="n"`, "reads at least one message"},
		{"cap past the ceiling", `connector="team" operation="list-messages" channel="42" maxResults="500" resultVariable="n"`, "at most 100"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(discordTaskBPMN(`<atlas:discordConnector `+tc.attrs+`/>`)))
			if err == nil {
				t.Fatalf("Parse accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// A body property on an operation with no request body is refused rather than dropped:
// a GET and a DELETE have nothing to merge it into.
func TestDiscordFieldsNeedARequestBody(t *testing.T) {
	_, err := Parse(1, 1, strings.NewReader(discordTaskBPMN(
		`<atlas:discordConnector connector="team" operation="get-message" channel="42" messageId="7" resultVariable="n">
			<atlas:discordField name="embeds" value="x"/>
		 </atlas:discordConnector>`)))
	if err == nil {
		t.Fatal("Parse accepted a discordField on an operation with no request body")
	}
	if !strings.Contains(err.Error(), "no request body") {
		t.Errorf("error = %v, want it to say the operation has no body", err)
	}
}

// The operation names are sorted, so the message that lists them reads the same way
// twice — it is what an author sees after a typo.
func TestDiscordOpNamesAreSorted(t *testing.T) {
	names := discordOpNames()
	if len(names) != len(discordOps) {
		t.Fatalf("discordOpNames = %d entries, discordOps = %d", len(names), len(discordOps))
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("discordOpNames is not sorted: %q before %q", names[i-1], names[i])
		}
	}
}
