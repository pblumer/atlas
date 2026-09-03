package compiler

import (
	"strings"
	"testing"
)

// A service task bearing an <atlas:csvConnector> extension is a CSV-to-JSON
// task (ADR-0139): the in-process CSV worker parses the named source
// variable's text against the model-authored layout into a rows collection via the
// job path, the whole layout living in the model rather than a columnConfig variable.
const csvConnectorBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:csvConnector source="upload" delimiter=";" hasHeader="false"
                            columns="email, group ,license" resultVariable="records"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseCsvConnectorTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(csvConnectorBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	node := cp.Node(task)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	d := cp.ConnectorTask(node.Detail)
	if got := cp.Intern(d.JobType); got != CsvImportJobType {
		t.Errorf("jobType = %q, want %q", got, CsvImportJobType)
	}
	if d.JobType != CsvImportJobTypeIndex {
		t.Errorf("jobType index = %d, want the reserved CsvImportJobTypeIndex %d", d.JobType, CsvImportJobTypeIndex)
	}
	if got := cp.Intern(d.CsvSource); got != "upload" {
		t.Errorf("source = %q, want upload", got)
	}
	if got := cp.Intern(d.CsvResult); got != "records" {
		t.Errorf("result = %q, want records", got)
	}
	if got := cp.Intern(d.CsvDelimiter); got != ";" {
		t.Errorf("delimiter = %q, want ;", got)
	}
	if d.CsvHasHeader {
		t.Errorf("hasHeader = true, want false (authored hasHeader=\"false\")")
	}
	var cols []string
	for _, ci := range d.CsvColumns {
		cols = append(cols, cp.Intern(ci))
	}
	if strings.Join(cols, ",") != "email,group,license" {
		t.Errorf("columns = %v, want the three trimmed field names", cols)
	}
	// A CSV task leaves the REST/mail/clio-only fields unset.
	if cp.Intern(d.Connector) != "" || cp.Intern(d.Method) != "" || d.Url.Expr != nil || d.Url.Literal != "" {
		t.Errorf("non-CSV fields set on a CSV task: connector=%q method=%q url=%+v",
			cp.Intern(d.Connector), cp.Intern(d.Method), d.Url)
	}
}

// A minimal csvConnector — no attributes — defaults to a header row and an empty
// column list (the worker derives the columns and defaults source/result/delimiter).
func TestParseCsvConnectorDefaults(t *testing.T) {
	const bpmn = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements><atlas:csvConnector/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	d := cp.ConnectorTask(cp.Node(task).Detail)
	if !d.CsvHasHeader {
		t.Errorf("hasHeader = false, want the default true")
	}
	if len(d.CsvColumns) != 0 {
		t.Errorf("columns = %v, want empty (derive from header)", d.CsvColumns)
	}
	if cp.Intern(d.CsvSource) != "" || cp.Intern(d.CsvResult) != "" || cp.Intern(d.CsvDelimiter) != "" {
		t.Errorf("unset source/result/delimiter should intern to empty; got %q/%q/%q",
			cp.Intern(d.CsvSource), cp.Intern(d.CsvResult), cp.Intern(d.CsvDelimiter))
	}
}

// A headerless CSV worker must name its columns (they map by position), so one
// without a column list is a compile error. Bad retries also fail the compile.
func TestParseCsvConnectorErrors(t *testing.T) {
	wrap := func(inner string) string {
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
	cases := map[string]string{
		"headerless without columns": `<atlas:csvConnector hasHeader="false"/>`,
		"invalid retries":            `<atlas:csvConnector retries="soon"/>`,
	}
	for name, inner := range cases {
		if _, err := Parse(1, 1, strings.NewReader(wrap(inner))); err == nil {
			t.Errorf("%s: want a compile error, got nil", name)
		}
	}
}

// csvTaskBPMN builds a one-task model from raw <atlas:csvConnector> attributes.
func csvTaskBPMN(attrs string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:csvConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

func csvDetailOf(t *testing.T, attrs string) (*CompiledProcess, *ConnectorTaskDetail) {
	t.Helper()
	cp, err := Parse(1, 1, strings.NewReader(csvTaskBPMN(attrs)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target)
	return cp, cp.ConnectorTask(node.Detail)
}

// A model written before formats existed keeps meaning exactly what it meant: a
// delimited file, read.
func TestCsvFormatDefaults(t *testing.T) {
	cp, d := csvDetailOf(t, `source="csvText" resultVariable="rows"`)
	if got := cp.Intern(d.CsvFormat); got != "csv" {
		t.Errorf("format = %q, want csv", got)
	}
	if got := cp.Intern(d.CsvOperation); got != "read" {
		t.Errorf("operation = %q, want read", got)
	}
	if len(d.CsvWidths) != 0 {
		t.Errorf("widths = %v, want none for a delimited file", d.CsvWidths)
	}
}

// A fixed-width file finds its fields by position, so each column carries a width.
func TestCsvFixedWidthColumns(t *testing.T) {
	cp, d := csvDetailOf(t, `source="datei" format="fixed-width" columns="personalnr:8,name:30" resultVariable="zeilen"`)
	if got := cp.Intern(d.CsvFormat); got != "fixed-width" {
		t.Errorf("format = %q", got)
	}
	if len(d.CsvColumns) != 2 || cp.Intern(d.CsvColumns[0]) != "personalnr" {
		t.Fatalf("columns = %v", d.CsvColumns)
	}
	if len(d.CsvWidths) != 2 || d.CsvWidths[0] != 8 || d.CsvWidths[1] != 30 {
		t.Errorf("widths = %v, want [8 30]", d.CsvWidths)
	}
}

// An attribute-value file names its own fields, so it needs no layout — and a
// headerless delimited file still does.
func TestCsvAVPNeedsNoColumns(t *testing.T) {
	cp, d := csvDetailOf(t, `source="datei" format="avp" hasHeader="false" resultVariable="zeilen"`)
	if got := cp.Intern(d.CsvFormat); got != "avp" {
		t.Errorf("format = %q", got)
	}
	if len(d.CsvColumns) != 0 {
		t.Errorf("columns = %v, want none", d.CsvColumns)
	}
}

func TestCsvWriteOperation(t *testing.T) {
	cp, d := csvDetailOf(t, `source="zeilen" operation="write" resultVariable="datei"`)
	if got := cp.Intern(d.CsvOperation); got != "write" {
		t.Errorf("operation = %q, want write", got)
	}
	// A write needs no column layout: the rows carry their own field names.
	if len(d.CsvColumns) != 0 {
		t.Errorf("columns = %v", d.CsvColumns)
	}
}

func TestCsvFormatValidation(t *testing.T) {
	for _, tc := range []struct{ name, attrs, want string }{
		{"unknown format", `source="f" format="yaml"`, "unknown format"},
		{"unknown operation", `source="f" operation="append"`, "unknown operation"},
		{"fixed-width without columns", `source="f" format="fixed-width"`, "name:width"},
		{"fixed-width column without width", `source="f" format="fixed-width" columns="a,b"`, "needs a width"},
		{"width on a delimited file", `source="f" columns="a:5"`, "only a fixed-width file uses"},
		{"non-numeric width", `source="f" format="fixed-width" columns="a:breit"`, "non-numeric width"},
		{"zero width", `source="f" format="fixed-width" columns="a:0"`, "at least one character"},
		{"headerless delimited read without columns", `source="f" hasHeader="false"`, "must list its columns"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(csvTaskBPMN(tc.attrs)))
			if err == nil {
				t.Fatalf("want an error mentioning %q, got none", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}
