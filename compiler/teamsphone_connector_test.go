package compiler

import (
	"reflect"
	"strings"
	"testing"
)

// teamsPhoneTaskBPMN builds a one-task model from a raw <atlas:teamsPhoneConnector .../>.
func teamsPhoneTaskBPMN(attrs string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:teamsPhoneConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

func teamsPhoneDetail(t *testing.T, attrs string) (*CompiledProcess, *ConnectorTaskDetail) {
	t.Helper()
	cp, err := Parse(1, 1, strings.NewReader(teamsPhoneTaskBPMN(attrs)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	return cp, cp.ConnectorTask(node.Detail)
}

func teamsPhoneArg(d *ConnectorTaskDetail, name string) (RestExpr, bool) {
	for _, kv := range d.TeamsPhoneArgs {
		if kv.Name == name {
			return kv.Val, true
		}
	}
	return RestExpr{}, false
}

// A Teams Phone task names a tenant and an operation and carries the operation's
// values; the worker job type and the interned operation are what the worker reads.
func TestTeamsPhoneTaskCompiles(t *testing.T) {
	cp, d := teamsPhoneDetail(t, `connector="contoso" operation="ensure-call-queue" name="=&quot;CQ &quot; + line" resourceAccountId="=accounts.queue" agents="=agentIds" resultVariable="queue"`)
	if got := cp.Intern(d.JobType); got != TeamsPhoneJobType {
		t.Errorf("job type = %q, want %q", got, TeamsPhoneJobType)
	}
	if got := cp.Intern(d.Connector); got != "contoso" {
		t.Errorf("connector = %q, want contoso", got)
	}
	if got := cp.Intern(d.TeamsPhoneOp); got != "ensure-call-queue" {
		t.Errorf("operation = %q, want ensure-call-queue", got)
	}
	if got := cp.Intern(d.ResultVar); got != "queue" {
		t.Errorf("result variable = %q, want queue", got)
	}
	for _, name := range []string{"name", "resourceAccountId", "agents"} {
		v, ok := teamsPhoneArg(d, name)
		if !ok || v.Expr == nil {
			t.Errorf("%s should compile to a FEEL expression, got %+v (present %v)", name, v, ok)
		}
	}
	if _, ok := teamsPhoneArg(d, "authorizedUsers"); ok {
		t.Error("an attribute the model did not author must not travel")
	}
}

// A static setting the model authored is compiled to its canonical word; one it left
// out does not travel at all. Whether the model named a setting is what tells an
// existing queue's setting to change from one to leave alone, so the compiler must not
// invent the default the worker applies to a new queue (ADR-0463, amendment 2026-10-20).
func TestTeamsPhoneSettingsTravelOnlyWhenAuthored(t *testing.T) {
	_, d := teamsPhoneDetail(t, `connector="c" operation="ensure-call-queue" name="CQ Support" resourceAccountId="id-1" routingMethod="Round-Robin" resultVariable="q"`)
	if v, ok := teamsPhoneArg(d, "routingMethod"); !ok || v.Literal != "round-robin" || v.Expr != nil {
		t.Errorf("routingMethod = %+v (present %v), want the literal round-robin", v, ok)
	}
	for _, name := range []string{"presenceBasedRouting", "conferenceMode", "allowOptOut"} {
		if v, ok := teamsPhoneArg(d, name); ok {
			t.Errorf("%s travels as %+v, though the model did not author it", name, v)
		}
	}
}

// Every refusal is at deploy, naming the task and the attribute, so a misspelled value
// is not found by a token parking on an incident.
func TestTeamsPhoneRefusals(t *testing.T) {
	cases := map[string]struct{ attrs, want string }{
		"no connector":         {`operation="resolve-users" users="a" resultVariable="r"`, "needs a connector"},
		"no operation":         {`connector="c" users="a" resultVariable="r"`, "needs an operation"},
		"unknown operation":    {`connector="c" operation="create-phone" resultVariable="r"`, "unknown operation"},
		"missing required":     {`connector="c" operation="ensure-call-queue" name="CQ" resultVariable="r"`, "needs resourceAccountId"},
		"value not used":       {`connector="c" operation="resolve-users" users="a" timeZone="UTC" resultVariable="r"`, "does not use timeZone"},
		"setting not used":     {`connector="c" operation="resolve-users" users="a" routingMethod="serial" resultVariable="r"`, "does not use routingMethod"},
		"no account type":      {`connector="c" operation="ensure-resource-account" userPrincipalName="u@x" displayName="d" resultVariable="r"`, "needs accountType"},
		"unknown account type": {`connector="c" operation="ensure-resource-account" userPrincipalName="u@x" displayName="d" accountType="queue" resultVariable="r"`, `accountType "queue"`},
		"setting as FEEL":      {`connector="c" operation="ensure-call-queue" name="n" resourceAccountId="i" conferenceMode="=flag" resultVariable="r"`, "cannot be a FEEL expression"},
		"answer discarded":     {`connector="c" operation="ensure-call-queue" name="n" resourceAccountId="i"`, "needs a resultVariable"},
		"attendant language":   {`connector="c" operation="ensure-auto-attendant" name="n" resourceAccountId="i" transferTarget="q" timeZone="UTC" resultVariable="r"`, "needs languageId"},
		"bad FEEL":             {`connector="c" operation="resolve-users" users="=(" resultVariable="r"`, "users"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(teamsPhoneTaskBPMN(c.attrs)))
			if err == nil {
				t.Fatalf("want an error containing %q, got none", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not contain %q", err, c.want)
			}
		})
	}
}

// A remove answers with whether something was removed, which a leaver path may keep or
// discard; it is the one family where the result variable is optional.
func TestTeamsPhoneRemoveMayDiscardItsAnswer(t *testing.T) {
	cp, d := teamsPhoneDetail(t, `connector="c" operation="remove-call-queue" resourceAccountId="=accounts.queue"`)
	if got := cp.Intern(d.ResultVar); got != "" {
		t.Errorf("result variable = %q, want none", got)
	}
}

// values() is how the compiler reads the element; an attribute on the struct that it
// leaves out would be parsed and then silently dropped.
func TestTeamsPhoneValuesCoverEveryAttribute(t *testing.T) {
	listed := map[string]bool{}
	for _, v := range (&xmlTeamsPhoneConnector{}).values() {
		listed[v.attr] = true
	}
	structural := map[string]bool{"connector": true, "operation": true, "resultVariable": true, "retries": true}
	typ := reflect.TypeOf(xmlTeamsPhoneConnector{})
	for i := 0; i < typ.NumField(); i++ {
		attr := strings.TrimSuffix(typ.Field(i).Tag.Get("xml"), ",attr")
		if structural[attr] {
			continue
		}
		if !listed[attr] {
			t.Errorf("attribute %q is parsed but not in values(), so the compiler never sees it", attr)
		}
		if teamsPhoneSettings[attr].values == nil && !teamsPhoneAuthored(attr) {
			t.Errorf("attribute %q is in no operation of teamsPhoneOps", attr)
		}
	}
}

func teamsPhoneAuthored(attr string) bool {
	for _, op := range teamsPhoneOps {
		for _, list := range [][]string{op.required, op.optional} {
			for _, a := range list {
				if a == attr {
					return true
				}
			}
		}
	}
	return false
}
