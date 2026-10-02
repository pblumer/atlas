package compiler

import (
	"strings"
	"testing"
)

// mailTaskBPMN wraps one <atlas:mailConnector> with the given attributes in a process.
func mailTaskBPMN(attrs string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:mailConnector ` + attrs + `/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

func mailDetail(t *testing.T, attrs string) (*CompiledProcess, ConnectorTaskDetail) {
	t.Helper()
	cp, err := Parse(1, 1, strings.NewReader(mailTaskBPMN(attrs)))
	if err != nil {
		t.Fatalf("Parse(%s): %v", attrs, err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	return cp, *cp.ConnectorTask(cp.Node(task).Detail)
}

func TestMailSendIsWhatItWasWithOrWithoutTheOperation(t *testing.T) {
	_, plain := mailDetail(t, `connector="m" to="a@b.ch" subject="S" body="B"`)
	_, named := mailDetail(t, `connector="m" operation="send" to="a@b.ch" subject="S" body="B"`)
	if plain.MailOp != "" || named.MailOp != "" {
		t.Errorf("a send's operation must stay empty, so a recompiled model means what it meant: %q / %q", plain.MailOp, named.MailOp)
	}
	if plain.ResultVar != -1 || named.ResultVar != -1 {
		t.Errorf("a send writes no result variable")
	}
	if _, err := Parse(1, 1, strings.NewReader(mailTaskBPMN(`connector="m" subject="S"`))); err == nil ||
		!strings.Contains(err.Error(), "needs a to recipient") {
		t.Errorf("a send without a recipient = %v", err)
	}
}

func TestMailListCompilesWithDefaults(t *testing.T) {
	cp, d := mailDetail(t, `connector="m" operation="list" folder="=box" unreadOnly="true" includeBody="true" resultVariable="mails"`)
	if d.MailOp != "list" || d.MailMaxResults != mailDefaultMaxResults || !d.MailUnreadOnly || !d.MailIncludeBody {
		t.Errorf("list detail = op %q max %d unread %v body %v", d.MailOp, d.MailMaxResults, d.MailUnreadOnly, d.MailIncludeBody)
	}
	if d.MailFolder.Expr == nil || cp.Intern(d.ResultVar) != "mails" {
		t.Errorf("folder %+v result %q", d.MailFolder, cp.Intern(d.ResultVar))
	}
	_, d = mailDetail(t, `connector="m" operation="list" maxResults="100" resultVariable="mails"`)
	if d.MailMaxResults != 100 || d.MailFolder != (RestExpr{}) {
		t.Errorf("list detail = max %d folder %+v", d.MailMaxResults, d.MailFolder)
	}
}

func TestMailOperationsCompile(t *testing.T) {
	for _, attrs := range []string{
		`operation="get" messageId="=mail.messageId" resultVariable="m"`,
		`operation="get" messageId="x" includeBody="true" resultVariable="m"`,
		`operation="move" messageId="x" destination="Archiv"`,
		`operation="move" messageId="x" destination="=target" resultVariable="newId"`,
		`operation="mark-read" messageId="x"`,
		`operation="MARK-UNREAD" messageId="x"`,
		`operation="delete" messageId="x"`,
		`operation="reply" messageId="x" body="Danke"`,
		`operation="reply" messageId="x" bodyHtml="&lt;p&gt;Danke&lt;/p&gt;"`,
	} {
		cp, d := mailDetail(t, `connector="m" `+attrs)
		uses := cp.MailboxUses()
		if len(uses) != 1 || uses[0].Worker != "m" || uses[0].Operation != d.MailOp || uses[0].ElementID != "t" {
			t.Errorf("%s: MailboxUses = %+v", attrs, uses)
		}
	}
	cp, _ := mailDetail(t, `connector="m" to="a@b.ch"`)
	if uses := cp.MailboxUses(); len(uses) != 0 {
		t.Errorf("a send is not a mailbox use: %+v", uses)
	}
}

func TestMailOperationsRefuseWhatTheyDoNotUse(t *testing.T) {
	for attrs, want := range map[string]string{
		`operation="peek"`:                                                 "unknown operation",
		`operation="list"`:                                                 "needs a resultVariable",
		`operation="get" resultVariable="m"`:                               "needs a messageId",
		`operation="move" messageId="x"`:                                   "needs a destination",
		`operation="reply" messageId="x"`:                                  "needs a body or a bodyHtml",
		`operation="delete" messageId="x" resultVariable="r"`:              "does not use resultVariable",
		`operation="get" messageId="x" resultVariable="m" to="a@b.ch"`:     "does not use to",
		`operation="reply" messageId="x" body="b" subject="Re: x"`:         "does not use subject",
		`operation="mark-read" messageId="x" includeBody="true"`:           "does not use includeBody",
		`operation="get" messageId="x" resultVariable="m" folder="F"`:      "does not use folder",
		`operation="list" resultVariable="m" maxResults="0"`:               "between 1 and 100",
		`operation="list" resultVariable="m" maxResults="101"`:             "between 1 and 100",
		`operation="list" resultVariable="m" maxResults="many"`:            "non-numeric maxResults",
		`operation="list" resultVariable="m" includeBody="yes"`:            "true or false",
		`operation="list" resultVariable="m" unreadOnly="=x"`:              "true or false",
		`to="a@b.ch" messageId="x"`:                                        "does not use messageId",
		`to="a@b.ch" resultVariable="r"`:                                   "does not use resultVariable",
		`operation="get" messageId="x" resultVariable="m" destination="d"`: "does not use destination",
	} {
		_, err := Parse(1, 1, strings.NewReader(mailTaskBPMN(`connector="m" `+attrs)))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s = %v, want an error containing %q", attrs, err, want)
		}
	}
}

func TestMailOperationsListIsSorted(t *testing.T) {
	ops := MailOperations()
	if strings.Join(ops, ",") != "delete,get,list,mark-read,mark-unread,move,reply,send" {
		t.Errorf("MailOperations = %v", ops)
	}
}
