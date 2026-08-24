package compiler

import (
	"strings"
	"testing"
)

// entraTaskBPMN builds a one-task model from a raw <atlas:entraConnector .../>.
func entraTaskBPMN(attrs string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:entraConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

func entraDetail(t *testing.T, attrs string) (*CompiledProcess, *ConnectorTaskDetail) {
	t.Helper()
	cp, err := Parse(1, 1, strings.NewReader(entraTaskBPMN(attrs)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	return cp, cp.ConnectorTask(node.Detail)
}

// A service task bearing <atlas:entraConnector> is an Entra ID connector task
// (ADR-0172): it names a tenant connector and a lifecycle operation, never an
// address or a credential.
func TestParseEntraConnectorTask(t *testing.T) {
	cp, d := entraDetail(t, `connector="contoso" operation="disable" userId="=person.upn" resultVariable="konto"`)
	if got := cp.Intern(d.JobType); got != EntraJobType {
		t.Errorf("jobType = %q, want %q", got, EntraJobType)
	}
	if d.JobType != EntraJobTypeIndex {
		t.Errorf("jobType index = %d, want %d", d.JobType, EntraJobTypeIndex)
	}
	if got := cp.Intern(d.Connector); got != "contoso" {
		t.Errorf("connector = %q, want contoso", got)
	}
	if got := cp.Intern(d.EntraOp); got != "disable" {
		t.Errorf("operation = %q, want disable", got)
	}
	if d.EntraUserID.Expr == nil {
		t.Errorf("userId should be a compiled FEEL expression, got literal %q", d.EntraUserID.Literal)
	}
	if got := cp.Intern(d.ResultVar); got != "konto" {
		t.Errorf("resultVariable = %q", got)
	}
	// An Entra task authors no HTTP method and no inline auth.
	if cp.Intern(d.Method) != "" || d.Auth != -1 {
		t.Errorf("method/auth = %q/%d, want empty/-1", cp.Intern(d.Method), d.Auth)
	}
}

func TestEntraConnectorGroupMembership(t *testing.T) {
	cp, d := entraDetail(t, `connector="contoso" operation="add-group-member" userId="arno@contoso.com" groupId="=gruppe.id"`)
	if got := cp.Intern(d.EntraOp); got != "add-group-member" {
		t.Errorf("operation = %q", got)
	}
	if d.EntraUserID.Literal != "arno@contoso.com" {
		t.Errorf("userId = %+v", d.EntraUserID)
	}
	if d.EntraGroupID.Expr == nil {
		t.Error("groupId should be a compiled FEEL expression")
	}
}

func TestEntraConnectorCreateUser(t *testing.T) {
	cp, d := entraDetail(t, `connector="contoso" operation="create-user" attributesVariable="neuerBenutzer" resultVariable="angelegt"`)
	if got := cp.Intern(d.EntraAttributesVar); got != "neuerBenutzer" {
		t.Errorf("attributesVariable = %q", got)
	}
	// create-user addresses no existing object.
	if d.EntraUserID.Literal != "" || d.EntraUserID.Expr != nil {
		t.Errorf("userId = %+v, want unset for create-user", d.EntraUserID)
	}
}

func TestEntraConnectorValidation(t *testing.T) {
	for _, tc := range []struct{ name, attrs, want string }{
		{"no connector", `operation="disable" userId="u"`, "connector"},
		{"no operation", `connector="c" userId="u"`, "operation"},
		{"unknown operation", `connector="c" operation="rename" userId="u"`, "unknown operation"},
		{"disable without user", `connector="c" operation="disable"`, "userId"},
		{"group op without group", `connector="c" operation="add-group-member" userId="u"`, "groupId"},
		{"group op without user", `connector="c" operation="remove-group-member" groupId="g"`, "userId"},
		{"create without attributes", `connector="c" operation="create-user"`, "attributesVariable"},
		{"update without attributes", `connector="c" operation="update-user" userId="u"`, "attributesVariable"},
		{"bad FEEL userId", `connector="c" operation="disable" userId="="`, "userId"},
		{"bad FEEL groupId", `connector="c" operation="add-group-member" userId="u" groupId="="`, "groupId"},
		{"list without a result variable", `connector="c" operation="list-users"`, "resultVariable"},
		{"bad FEEL filter", `connector="c" operation="list-users" resultVariable="r" filter="="`, "filter"},
		{"non-numeric pageSize", `connector="c" operation="list-users" resultVariable="r" pageSize="viele"`, "non-numeric pageSize"},
		{"negative maxUsers", `connector="c" operation="list-users" resultVariable="r" maxUsers="-1"`, "negative maxUsers"},
		{"pageSize above Graph's ceiling", `connector="c" operation="list-users" resultVariable="r" pageSize="1000"`, "above the 999"},
		{"filter on a single-object read", `connector="c" operation="get-user" userId="u" filter="x eq 1"`, "applies to list-users"},
		{"select on a single-object read", `connector="c" operation="get-user" userId="u" select="id"`, "applies to list-users"},
		{"pageSize on a single-object read", `connector="c" operation="disable" userId="u" pageSize="10"`, "applies to list-users"},
		{"maxUsers on a single-object read", `connector="c" operation="disable" userId="u" maxUsers="10"`, "applies to list-users"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(entraTaskBPMN(tc.attrs)))
			if err == nil {
				t.Fatalf("want an error mentioning %q, got none", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}

// A listing compiles its query: the filter is a literal-or-FEEL value like every
// other authored connector value, and the bounds are settled at deploy so the
// runtime interprets nothing (I5).
func TestEntraConnectorListUsers(t *testing.T) {
	cp, d := entraDetail(t, `connector="contoso" operation="list-users" resultVariable="leute"
		filter="=&#34;department eq '&#34; + abteilung + &#34;'&#34;" select="id,displayName" pageSize="200" maxUsers="500"`)
	if got := cp.Intern(d.EntraOp); got != "list-users" {
		t.Errorf("operation = %q, want list-users", got)
	}
	if d.EntraFilter.Expr == nil {
		t.Errorf("filter should be a compiled FEEL expression, got literal %q", d.EntraFilter.Literal)
	}
	if got := cp.Intern(d.EntraSelect); got != "id,displayName" {
		t.Errorf("select = %q", got)
	}
	if d.EntraPageSize != 200 || d.EntraMaxUsers != 500 {
		t.Errorf("bounds = %d/%d, want 200/500", d.EntraPageSize, d.EntraMaxUsers)
	}
	// A literal filter stays a literal.
	_, lit := entraDetail(t, `connector="c" operation="list-users" resultVariable="r" filter="accountEnabled eq true"`)
	if lit.EntraFilter.Literal != "accountEnabled eq true" || lit.EntraFilter.Expr != nil {
		t.Errorf("filter = %+v, want the literal", lit.EntraFilter)
	}
}

// An unauthored listing inherits the bounds, and 0 is the authored way to say
// unbounded — so a model that means "all of them" can say it, and one that says
// nothing does not accidentally mean it.
func TestEntraConnectorListDefaults(t *testing.T) {
	_, d := entraDetail(t, `connector="c" operation="list-users" resultVariable="leute"`)
	if d.EntraPageSize != defaultEntraPageSize {
		t.Errorf("pageSize = %d, want the default %d (no $top, Graph's own page size)", d.EntraPageSize, defaultEntraPageSize)
	}
	if d.EntraMaxUsers != defaultEntraMaxUsers {
		t.Errorf("maxUsers = %d, want the default %d", d.EntraMaxUsers, defaultEntraMaxUsers)
	}
	_, unbounded := entraDetail(t, `connector="c" operation="list-users" resultVariable="leute" maxUsers="0"`)
	if unbounded.EntraMaxUsers != 0 {
		t.Errorf("maxUsers = %d, want 0 to survive as unbounded", unbounded.EntraMaxUsers)
	}
	// And an operation that returns one object or none carries no listing state at all.
	_, single := entraDetail(t, `connector="c" operation="disable" userId="u"`)
	if single.EntraPageSize != 0 || single.EntraMaxUsers != 0 || single.EntraFilter.Literal != "" || single.EntraSelect != -1 {
		t.Errorf("a non-listing task carries listing state: %+v", single)
	}
}

// The reserved index is baked into jobs already on disk, so it may never move.
func TestEntraJobTypeIndexIsReserved(t *testing.T) {
	if EntraJobTypeIndex != 23 {
		t.Errorf("EntraJobTypeIndex = %d, want 23", EntraJobTypeIndex)
	}
	if got := reservedJobTypes[EntraJobTypeIndex]; got != EntraJobType {
		t.Errorf("reservedJobTypes[%d] = %q, want %q", EntraJobTypeIndex, got, EntraJobType)
	}
}
