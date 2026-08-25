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

// The connector may be a static name (the common case) or a FEEL expression, so one
// process can serve several tenants and resolve the name from its own variables at
// call time (ADR-0172). A literal leaves EntraConnector's Expr nil and Connector
// holds the name; an expression compiles into EntraConnector and Connector keeps the
// authored "=..." text for introspection.
func TestEntraConnectorIsLiteralOrExpression(t *testing.T) {
	// Literal: the static path is unchanged — no connector expression is compiled.
	_, lit := entraDetail(t, `connector="contoso" operation="disable" userId="=person.upn" resultVariable="k"`)
	if lit.EntraConnector.Expr != nil {
		t.Errorf("a literal connector should compile no expression, got %+v", lit.EntraConnector)
	}

	// Expression: the '=' compiles into EntraConnector; Connector keeps the text so an
	// incident or a placement badge can still name the (dynamic) reference.
	cp, dyn := entraDetail(t, `connector="=tenant" operation="disable" userId="=person.upn" resultVariable="k"`)
	if dyn.EntraConnector.Expr == nil {
		t.Errorf("a FEEL connector should compile an expression, got literal %q", dyn.EntraConnector.Literal)
	}
	if got := cp.Intern(dyn.Connector); got != "=tenant" {
		t.Errorf("Connector introspection text = %q, want =tenant", got)
	}
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

// reset-password authors one newPassword value (literal-or-FEEL) the worker wraps in a
// passwordProfile; create-team and add-team-member both address the group by its id,
// which is the Team's id too.
func TestEntraConnectorGroupsAndTeams(t *testing.T) {
	_, pw := entraDetail(t, `connector="contoso" operation="reset-password" userId="u1" newPassword="=tempPassword"`)
	if pw.EntraNewPassword.Expr == nil {
		t.Errorf("newPassword should be a compiled FEEL expression, got literal %q", pw.EntraNewPassword.Literal)
	}
	cpG, grp := entraDetail(t, `connector="contoso" operation="create-group" attributesVariable="neueGruppe"`)
	if got := cpG.Intern(grp.EntraAttributesVar); got != "neueGruppe" {
		t.Errorf("attributesVariable = %q", got)
	}
	// Inline attributes compile to a FEEL context expression, and the variable name is
	// then left unset — the two are mutually exclusive.
	_, inline := entraDetail(t, `connector="contoso" operation="create-user" attributes="{&#34;displayName&#34;:&#34;=name&#34;}"`)
	if inline.EntraAttributes.Expr == nil {
		t.Error("inline attributes should compile to a FEEL context expression")
	}
	if inline.EntraAttributesVar != -1 {
		t.Errorf("attributesVariable = %d, want unset when inline attributes are authored", inline.EntraAttributesVar)
	}
	cp, team := entraDetail(t, `connector="contoso" operation="create-team" groupId="=gruppe.id"`)
	if got := cp.Intern(team.EntraOp); got != "create-team" {
		t.Errorf("operation = %q, want create-team", got)
	}
	if team.EntraGroupID.Expr == nil {
		t.Error("groupId should be a compiled FEEL expression for create-team")
	}
	// A non-password operation carries no password state at all.
	_, single := entraDetail(t, `connector="c" operation="disable" userId="u"`)
	if single.EntraNewPassword.Literal != "" || single.EntraNewPassword.Expr != nil {
		t.Errorf("a non-password task carries a newPassword: %+v", single.EntraNewPassword)
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
		{"create-group without attributes", `connector="c" operation="create-group"`, "attributesVariable"},
		{"delete-group without a group", `connector="c" operation="delete-group"`, "groupId"},
		{"reset-password without a password", `connector="c" operation="reset-password" userId="u"`, "newPassword"},
		{"newPassword on the wrong operation", `connector="c" operation="disable" userId="u" newPassword="x"`, "applies to reset-password"},
		{"create-team without a group", `connector="c" operation="create-team"`, "groupId"},
		{"team member without a user", `connector="c" operation="add-team-member" groupId="g"`, "userId"},
		{"both inline and variable attributes", `connector="c" operation="create-user" attributes="{}" attributesVariable="v"`, "use one, not both"},
		{"inline attributes on a wrong op", `connector="c" operation="disable" userId="u" attributes="{}"`, "attributes apply to"},
		{"malformed inline attributes", `connector="c" operation="create-user" attributes="{oops}"`, "invalid attributes JSON"},
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
		{"search on a single-object read", `connector="c" operation="get-user" userId="u" search="&#34;x:y&#34;"`, "applies to list-users"},
		{"advancedQuery on a single-object read", `connector="c" operation="disable" userId="u" advancedQuery="true"`, "applies to list-users"},
		{"non-boolean advancedQuery", `connector="c" operation="list-users" resultVariable="r" advancedQuery="vielleicht"`, "non-boolean advancedQuery"},
		{"search with advancedQuery false", `connector="c" operation="list-users" resultVariable="r" search="&#34;x:y&#34;" advancedQuery="false"`, "only as an advanced query"},
		{"bad FEEL search", `connector="c" operation="list-users" resultVariable="r" search="="`, "search"},
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

// Graph runs a $search only as an advanced query, so authoring a search is the whole
// of asking for one — the compiler sets the flag rather than leaving an author to
// discover the requirement as a 400.
func TestEntraConnectorSearchImpliesAdvancedQuery(t *testing.T) {
	_, d := entraDetail(t, `connector="c" operation="list-users" resultVariable="r" search="&#34;displayName:Arno&#34;"`)
	if !d.EntraAdvanced {
		t.Error("a search must compile to an advanced query")
	}
	if d.EntraSearch.Literal != `"displayName:Arno"` {
		t.Errorf("search = %+v, want the authored term with its quotes intact", d.EntraSearch)
	}
	// Without a search it is opt-in, and never inferred from the filter text — the
	// filter may be FEEL, with nothing to read at deploy.
	_, plain := entraDetail(t, `connector="c" operation="list-users" resultVariable="r" filter="endsWith(mail,'@blumer.net')"`)
	if plain.EntraAdvanced {
		t.Error("an advanced query must be asked for, not guessed from the filter")
	}
	_, opted := entraDetail(t, `connector="c" operation="list-users" resultVariable="r" filter="endsWith(mail,'@x.de')" advancedQuery="true"`)
	if !opted.EntraAdvanced {
		t.Error("advancedQuery=\"true\" must compile to an advanced query")
	}
	// And it can be turned off in as many words, which is not the same as leaving it
	// out: an author who writes it has decided, and the model records the decision.
	_, off := entraDetail(t, `connector="c" operation="list-users" resultVariable="r" advancedQuery="false"`)
	if off.EntraAdvanced {
		t.Error(`advancedQuery="false" must compile to a plain query`)
	}
	// A FEEL search compiles like every other authored value.
	_, feel := entraDetail(t, `connector="c" operation="list-users" resultVariable="r" search="=suchbegriff"`)
	if feel.EntraSearch.Expr == nil {
		t.Errorf("search should be a compiled FEEL expression, got literal %q", feel.EntraSearch.Literal)
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
