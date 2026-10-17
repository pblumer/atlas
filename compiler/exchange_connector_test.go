package compiler

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/exchange/catalog"
)

// exchangeTaskBPMN builds a one-task model from a raw <atlas:exchangeConnector .../>.
// Attributes holding JSON are written in single quotes so the JSON keeps its own.
func exchangeTaskBPMN(attrs string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:exchangeConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

func exchangeDetail(t *testing.T, attrs string) (*CompiledProcess, *ConnectorTaskDetail) {
	t.Helper()
	cp, err := Parse(1, 1, strings.NewReader(exchangeTaskBPMN(attrs)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	return cp, cp.ConnectorTask(node.Detail)
}

// A service task bearing <atlas:exchangeConnector> is an Exchange task: it names a
// Worker, an operation and the cmdlet's parameters — never an address or a credential.
func TestParseExchangeConnectorTask(t *testing.T) {
	cp, d := exchangeDetail(t, `connector="contoso" operation="add-full-access" parameters='{"Identity":"=postfach","User":"anna@contoso.com","AutoMapping":false}' resultVariable="ace"`)
	if d.JobType != ExchangeJobTypeIndex || cp.Intern(d.JobType) != ExchangeJobType {
		t.Errorf("jobType = %d %q", d.JobType, cp.Intern(d.JobType))
	}
	if got := cp.Intern(d.Connector); got != "contoso" {
		t.Errorf("worker = %q", got)
	}
	if d.ExchangeConnector.Expr != nil {
		t.Error("a literal worker compiled an expression")
	}
	if got := cp.Intern(d.ExchangeOp); got != "add-full-access" {
		t.Errorf("operation = %q", got)
	}
	if d.ExchangeParameters.Expr == nil {
		t.Fatal("the parameters were not compiled to a FEEL context")
	}
	if got := cp.Intern(d.ResultVar); got != "ace" {
		t.Errorf("resultVariable = %q", got)
	}
	if d.ExchangeMaxResults != 0 {
		t.Errorf("a non-listing carries maxResults %d", d.ExchangeMaxResults)
	}
	if got := cp.Intern(d.ExchangeCmdlet); got != "" {
		t.Errorf("cmdlet = %q on a named operation", got)
	}
}

// TestExchangeParametersEvaluateToTheTemplate: the template compiles to one FEEL context
// whose literals keep their JSON types — a boolean switch must arrive as a boolean,
// because PowerShell refuses the string "false" for one.
func TestExchangeParametersEvaluateToTheTemplate(t *testing.T) {
	_, d := exchangeDetail(t, `connector="c" operation="add-full-access" parameters='{"Identity":"=postfach","User":"anna","AutoMapping":false}'`)
	inputs := d.ExchangeParameters.Expr.Inputs()
	if len(inputs) != 1 || inputs[0] != "postfach" {
		t.Errorf("inputs = %v", inputs)
	}
}

func TestExchangeListingTakesItsBound(t *testing.T) {
	_, d := exchangeDetail(t, `connector="c" operation="list-mailboxes" resultVariable="r"`)
	if d.ExchangeMaxResults != defaultExchangeMaxResults {
		t.Errorf("default maxResults = %d", d.ExchangeMaxResults)
	}
	_, d = exchangeDetail(t, `connector="c" operation="list-distribution-group-members" parameters='{"Identity":"team"}' maxResults="25" resultVariable="r"`)
	if d.ExchangeMaxResults != 25 {
		t.Errorf("maxResults = %d", d.ExchangeMaxResults)
	}
}

func TestExchangeGenericReadCarriesItsCmdlet(t *testing.T) {
	cp, d := exchangeDetail(t, `connector="c" operation="run-get-cmdlet" cmdlet="Get-ADPermission" parameters='{"Identity":"info"}' resultVariable="r"`)
	if got := cp.Intern(d.ExchangeCmdlet); got != "Get-ADPermission" {
		t.Errorf("cmdlet = %q", got)
	}
}

func TestExchangeWorkerMayBeAnExpression(t *testing.T) {
	cp, d := exchangeDetail(t, `connector="=organisation" operation="get-mailbox" parameters='{"Identity":"anna"}' resultVariable="r"`)
	if d.ExchangeConnector.Expr == nil {
		t.Error("a FEEL worker compiled no expression")
	}
	if got := cp.Intern(d.Connector); got != "=organisation" {
		t.Errorf("worker introspection text = %q", got)
	}
}

// TestExchangeParametersMayComeFromAVariable: a process that assembled the parameters
// itself names the variable; what it holds is checked when the task runs.
func TestExchangeParametersMayComeFromAVariable(t *testing.T) {
	cp, d := exchangeDetail(t, `connector="c" operation="update-mailbox" parametersVariable="aenderung"`)
	if got := cp.Intern(d.ExchangeParametersVar); got != "aenderung" {
		t.Errorf("parametersVariable = %q", got)
	}
	if d.ExchangeParameters.Expr != nil {
		t.Error("a variable task compiled an inline template too")
	}
}

func TestExchangeCompileRefusals(t *testing.T) {
	cases := []struct {
		name, attrs, want string
	}{
		{"no worker", `operation="get-mailbox" parameters='{"Identity":"a"}' resultVariable="r"`, "needs a connector"},
		{"no operation", `connector="c"`, "needs an operation"},
		{"unknown operation", `connector="c" operation="new-mailbox"`, "unknown operation"},
		{"missing required", `connector="c" operation="add-full-access" parameters='{"Identity":"a"}'`, "needs User"},
		{"blank required", `connector="c" operation="add-full-access" parameters='{"Identity":"a","User":" "}'`, "needs User"},
		{"no parameters at all", `connector="c" operation="remove-mailbox"`, "needs Identity"},
		{"undeclared on closed op", `connector="c" operation="add-full-access" parameters='{"Identity":"a","User":"b","Deny":true}'`, `does not take "Deny"`},
		{"fixed by the operation", `connector="c" operation="add-full-access" parameters='{"Identity":"a","User":"b","AccessRights":"ReadPermission"}'`, "sets AccessRights itself"},
		{"common parameter", `connector="c" operation="update-mailbox" parameters='{"Identity":"a","WhatIf":true}'`, "common parameter"},
		{"not json", `connector="c" operation="get-mailbox" parameters='{' resultVariable="r"`, "invalid parameters JSON"},
		{"not an object", `connector="c" operation="get-mailbox" parameters='["a"]' resultVariable="r"`, "JSON object"},
		{"empty expression", `connector="c" operation="get-mailbox" parameters='{"Identity":"="}' resultVariable="r"`, "empty =expression"},
		{"both sources", `connector="c" operation="update-mailbox" parameters='{"Identity":"a"}' parametersVariable="p"`, "not both"},
		{"read without result", `connector="c" operation="get-mailbox" parameters='{"Identity":"a"}'`, "needs a resultVariable"},
		{"result on a set", `connector="c" operation="remove-mailbox" parameters='{"Identity":"a"}' resultVariable="r"`, "returns nothing"},
		{"maxResults on a single read", `connector="c" operation="get-mailbox" parameters='{"Identity":"a"}' maxResults="5" resultVariable="r"`, "not a listing"},
		{"maxResults not a number", `connector="c" operation="list-mailboxes" maxResults="viele" resultVariable="r"`, "non-numeric"},
		{"maxResults zero", `connector="c" operation="list-mailboxes" maxResults="0" resultVariable="r"`, "at least 1"},
		{"maxResults too large", `connector="c" operation="list-mailboxes" maxResults="1000000" resultVariable="r"`, "above"},
		{"ResultSize on a sized listing", `connector="c" operation="list-mailboxes" parameters='{"ResultSize":"Unlimited"}' resultVariable="r"`, "maxResults"},
		{"cmdlet on a named op", `connector="c" operation="get-mailbox" cmdlet="Get-User" parameters='{"Identity":"a"}' resultVariable="r"`, "takes no cmdlet"},
		{"generic without cmdlet", `connector="c" operation="run-get-cmdlet" resultVariable="r"`, "needs a cmdlet"},
		{"generic write", `connector="c" operation="run-get-cmdlet" cmdlet="Remove-Mailbox" resultVariable="r"`, "Get- cmdlets only"},
		{"generic expression", `connector="c" operation="run-get-cmdlet" cmdlet="=welches" resultVariable="r"`, "Get- cmdlets only"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(exchangeTaskBPMN(c.attrs)))
			if err == nil {
				t.Fatal("the model compiled")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q", err, c.want)
			}
			if !strings.Contains(err.Error(), `"t"`) {
				t.Errorf("error %q does not name the task", err)
			}
		})
	}
}

// TestEveryCatalogOperationCompiles: the compiler reads the same table the worker
// does, so a model authoring exactly what an operation requires must deploy — for
// every row, which is what catches a row the compiler cannot express.
func TestEveryCatalogOperationCompiles(t *testing.T) {
	for _, name := range catalog.Names() {
		op, _ := catalog.Lookup(name)
		t.Run(name, func(t *testing.T) {
			params := map[string]any{}
			for _, r := range op.Required {
				params[r] = "x"
			}
			if op.AnyOf {
				params[op.Optional[0]] = "1 GB"
			}
			raw, _ := json.Marshal(params)
			attrs := `connector="c" operation="` + name + `" parameters='` + string(raw) + `'`
			if op.Generic {
				attrs += ` cmdlet="Get-Mailbox"`
			}
			if op.Result != catalog.ResultNone {
				attrs += ` resultVariable="r"`
			}
			if _, err := Parse(1, 1, strings.NewReader(exchangeTaskBPMN(attrs))); err != nil {
				t.Fatalf("a complete model does not compile: %v", err)
			}
		})
	}
}

// The reserved index is baked into jobs already on disk, so it may never move.
func TestExchangeJobTypeIndexIsReserved(t *testing.T) {
	if ExchangeJobTypeIndex != 33 {
		t.Errorf("ExchangeJobTypeIndex = %d, want 33", ExchangeJobTypeIndex)
	}
	if got := reservedJobTypes[ExchangeJobTypeIndex]; got != ExchangeJobType {
		t.Errorf("reservedJobTypes[%d] = %q, want %q", ExchangeJobTypeIndex, got, ExchangeJobType)
	}
}
