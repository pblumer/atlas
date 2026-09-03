package compiler

import (
	"strings"
	"testing"
)

// A service task bearing an <atlas:googleSheetsConnector> extension is a Google Sheets
// task (ADR-0235): it performs one spreadsheet operation against
// a Worker an operator configured, via the job path. The credential lives server-side,
// like Jira's and SharePoint's (ADR-0141/0201); only what the task is *about* — the
// operation, the spreadsheet, the range and the values — is authored in the model.
const googleSheetsConnectorBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:googleSheetsConnector connector="acme" operation="append-row"
                                     spreadsheet="1Bxi" range="Anträge!A:C"
                                     values="=zeilen" columns="name, betrag ,status"
                                     valueInput="raw" resultVariable="ergebnis"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseGoogleSheetsConnectorTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(googleSheetsConnectorBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	node := cp.Node(task)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	d := cp.ConnectorTask(node.Detail)
	if got := cp.Intern(d.JobType); got != GoogleSheetsJobType {
		t.Errorf("jobType = %q, want %q", got, GoogleSheetsJobType)
	}
	if d.JobType != GoogleSheetsJobTypeIndex {
		t.Errorf("jobType index = %d, want the reserved GoogleSheetsJobTypeIndex %d", d.JobType, GoogleSheetsJobTypeIndex)
	}
	if got := cp.Intern(d.Connector); got != "acme" {
		t.Errorf("connector = %q, want acme", got)
	}
	if got := cp.Intern(d.SheetsOp); got != "append-row" {
		t.Errorf("operation = %q, want append-row", got)
	}
	if d.SheetsID.Expr != nil || d.SheetsID.Literal != "1Bxi" {
		t.Errorf("spreadsheet = %+v, want the literal id", d.SheetsID)
	}
	if d.SheetsRange.Expr != nil || d.SheetsRange.Literal != "Anträge!A:C" {
		t.Errorf("range = %+v, want the literal range", d.SheetsRange)
	}
	if d.SheetsValues.Expr == nil {
		t.Errorf("values = %+v, want a compiled FEEL expression", d.SheetsValues)
	}
	// The columns are the sheet's column order, so they are compiled structure, not a
	// string the runtime has to split (I5) — and the spacing an author leaves is theirs.
	want := []string{"name", "betrag", "status"}
	if len(d.SheetsColumns) != len(want) {
		t.Fatalf("columns = %#v, want %#v", d.SheetsColumns, want)
	}
	for i, col := range want {
		if d.SheetsColumns[i] != col {
			t.Errorf("column %d = %q, want %q", i, d.SheetsColumns[i], col)
		}
	}
	if got := cp.Intern(d.SheetsInput); got != "raw" {
		t.Errorf("valueInput = %q, want raw", got)
	}
	if got := cp.Intern(d.ResultVar); got != "ergebnis" {
		t.Errorf("resultVariable = %q, want ergebnis", got)
	}
}

// TestGoogleSheetsDefaultsAreCompiledIn: the runtime must interpret nothing (I5), so
// an unauthored value-input mode is the default *in the compiled process*, not a blank
// the worker fills in later.
func TestGoogleSheetsDefaultsAreCompiledIn(t *testing.T) {
	d := compileSheetsTask(t, `connector="acme" operation="write-range" spreadsheet="1B" range="A1" values="=v"`)
	if got := cp0(t, d).Intern(d.SheetsInput); got != "user" {
		t.Errorf("valueInput = %q, want the compiled-in user default", got)
	}
}

// TestGoogleSheetsHeaderIsStructural: header decides the *shape* of what lands in the
// result variable, so it is a compiled bool rather than a literal-or-FEEL value that
// could be a different shape on every token.
func TestGoogleSheetsHeaderIsStructural(t *testing.T) {
	d := compileSheetsTask(t, `connector="acme" operation="read-range" spreadsheet="1B" range="A:C" header="true" resultVariable="r"`)
	if !d.SheetsHeader {
		t.Error("header=\"true\" did not compile to a set header flag")
	}
}

// TestGoogleSheetsCreateSpreadsheet covers the one operation that addresses no
// existing spreadsheet, and the two attributes only it takes.
func TestGoogleSheetsCreateSpreadsheet(t *testing.T) {
	d := compileSheetsTask(t, `connector="acme" operation="create-spreadsheet" title="Anträge" sheet="Eingang" folder="=ordner" resultVariable="datei"`)
	if d.SheetsTitle.Literal != "Anträge" {
		t.Errorf("title = %+v, want the literal title", d.SheetsTitle)
	}
	if d.SheetsTab.Literal != "Eingang" {
		t.Errorf("sheet = %+v, want the literal tab title", d.SheetsTab)
	}
	if d.SheetsFolder.Expr == nil {
		t.Errorf("folder = %+v, want a compiled FEEL expression", d.SheetsFolder)
	}
}

// TestGoogleSheetsRefusesBadModels is the deploy-time half of the operation table: a
// value an operation does not use is refused rather than silently dropped at call
// time, which from the author's side is indistinguishable from a connector that
// ignored it.
func TestGoogleSheetsRefusesBadModels(t *testing.T) {
	for name, tc := range map[string]struct{ attrs, want string }{
		"no worker":          {`operation="read-range" spreadsheet="1B" range="A1" resultVariable="r"`, "naming the Worker"},
		"no operation":       {`connector="acme" spreadsheet="1B"`, "needs an operation"},
		"unknown operation":  {`connector="acme" operation="pivot"`, "unknown operation"},
		"no spreadsheet":     {`connector="acme" operation="read-range" range="A1" resultVariable="r"`, "needs a spreadsheet"},
		"no range":           {`connector="acme" operation="read-range" spreadsheet="1B" resultVariable="r"`, "needs a range"},
		"no values":          {`connector="acme" operation="write-range" spreadsheet="1B" range="A1"`, "needs values"},
		"no result on read":  {`connector="acme" operation="read-range" spreadsheet="1B" range="A1"`, "needs a resultVariable"},
		"no title on create": {`connector="acme" operation="create-spreadsheet"`, "needs a title"},
		"no sheet on delete": {`connector="acme" operation="delete-sheet" spreadsheet="1B"`, "needs a sheet"},
		"range on create":    {`connector="acme" operation="create-spreadsheet" title="T" range="A1"`, "does not use range"},
		"values on read":     {`connector="acme" operation="read-range" spreadsheet="1B" range="A1" resultVariable="r" values="=v"`, "does not use values"},
		"header on write":    {`connector="acme" operation="write-range" spreadsheet="1B" range="A1" values="=v" header="true"`, "does not use header"},
		"result on delete":   {`connector="acme" operation="delete-spreadsheet" spreadsheet="1B" resultVariable="r"`, "does not use resultVariable"},
		"folder elsewhere":   {`connector="acme" operation="add-sheet" spreadsheet="1B" sheet="Neu" folder="f"`, "does not use folder"},
		"columns on read":    {`connector="acme" operation="read-range" spreadsheet="1B" range="A1" resultVariable="r" columns="a,b"`, "does not use columns"},
		"bad valueInput":     {`connector="acme" operation="write-range" spreadsheet="1B" range="A1" values="=v" valueInput="magic"`, "valueInput"},
		"bad header":         {`connector="acme" operation="read-range" spreadsheet="1B" range="A1" resultVariable="r" header="vielleicht"`, "header"},
	} {
		_, err := Parse(1, 1, strings.NewReader(sheetsBPMN(tc.attrs)))
		if err == nil {
			t.Errorf("%s: want a compile error, got nil", name)
			continue
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: error %q should mention %q", name, err, tc.want)
		}
	}
}

// sheetsBPMN wraps one set of connector attributes in the smallest process that
// carries a service task.
func sheetsBPMN(attrs string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements><atlas:googleSheetsConnector ` + attrs + `/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

// compileSheetsTask compiles one connector task and returns its detail, failing the
// test if the model does not compile.
func compileSheetsTask(t *testing.T, attrs string) *ConnectorTaskDetail {
	t.Helper()
	cp, err := Parse(1, 1, strings.NewReader(sheetsBPMN(attrs)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	sheetsCP = cp
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	return cp.ConnectorTask(cp.Node(task).Detail)
}

// sheetsCP holds the process the last compileSheetsTask produced, so an assertion can
// resolve an interned index back to its string.
var sheetsCP *CompiledProcess

func cp0(t *testing.T, _ *ConnectorTaskDetail) *CompiledProcess {
	t.Helper()
	return sheetsCP
}
