package compiler

import (
	"html"
	"strings"
	"testing"
)

func atlasReportTaskBPMN(attrs string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:atlasReportConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

func reportInput(json string) string { return `input="` + html.EscapeString(json) + `"` }

// A service task bearing <atlas:atlasReportConnector> is an Atlas report task: it names a
// Worker, an operation and an input object — never a token (ADR-0555 §3).
func TestParseAtlasReportConnectorTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(atlasReportTaskBPMN(`connector="atlas" operation="Process-Statistics" retries="2" resultVariable="bericht" `+
		reportInput(`{"process":"eintritt","buckets":8,"bucket":"week","to":"=stichtag","language":"de","title":"Eintritte"}`))))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	d := cp.ConnectorTask(cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target).Detail)
	if d.JobType != AtlasReportJobTypeIndex || cp.Intern(d.JobType) != AtlasReportJobType {
		t.Errorf("jobType = %d %q", d.JobType, cp.Intern(d.JobType))
	}
	if cp.Intern(d.AtlasReportOp) != "process-statistics" || d.AtlasReportInput.Expr == nil || cp.Intern(d.Connector) != "atlas" {
		t.Errorf("detail %+v", d)
	}
	if cp.Intern(d.ResultVar) != "bericht" || d.Retries != 2 || d.Method != -1 || d.Auth != -1 {
		t.Errorf("result %q retries %d method %d auth %d", cp.Intern(d.ResultVar), d.Retries, d.Method, d.Auth)
	}
	// An operation that needs nothing may leave the input out.
	cp, err = Parse(1, 1, strings.NewReader(atlasReportTaskBPMN(`connector="atlas" operation="incident-summary" resultVariable="r"`)))
	if err != nil {
		t.Fatalf("Parse without input: %v", err)
	}
	if d := cp.ConnectorTask(cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target).Detail); d.AtlasReportInput.Expr != nil {
		t.Errorf("an absent input compiled to %v", d.AtlasReportInput)
	}
}

func TestAtlasReportTaskRefusals(t *testing.T) {
	for name, tc := range map[string]struct{ attrs, want string }{
		"no connector":      {`operation="incident-summary" resultVariable="r"`, "needs a connector"},
		"no operation":      {`connector="a" resultVariable="r"`, "needs an operation"},
		"unknown operation": {`connector="a" operation="everything" resultVariable="r"`, "unknown operation"},
		"no result":         {`connector="a" operation="incident-summary"`, "needs a resultVariable"},
		"no process":        {`connector="a" operation="process-statistics" resultVariable="r"`, `needs "process"`},
		"input not JSON":    {`connector="a" operation="incident-summary" resultVariable="r" ` + reportInput(`{`), "not JSON"},
		"input not object":  {`connector="a" operation="incident-summary" resultVariable="r" ` + reportInput(`[1]`), "must be a JSON object"},
		"unknown key":       {`connector="a" operation="incident-summary" resultVariable="r" ` + reportInput(`{"element":"x"}`), `does not take "element"`},
		"bad date":          {`connector="a" operation="delivery-report" resultVariable="r" ` + reportInput(`{"from":"gestern"}`), "neither a date"},
		"bad bucket":        {`connector="a" operation="process-statistics" resultVariable="r" ` + reportInput(`{"process":"p","buckets":3,"bucket":"year"}`), "bucket"},
		"too many buckets":  {`connector="a" operation="process-statistics" resultVariable="r" ` + reportInput(`{"process":"p","buckets":27,"bucket":"week"}`), "between 1 and 26"},
		"buckets alone":     {`connector="a" operation="process-statistics" resultVariable="r" ` + reportInput(`{"process":"p","buckets":3}`), "go together"},
		"from with buckets": {`connector="a" operation="process-statistics" resultVariable="r" ` + reportInput(`{"process":"p","buckets":3,"bucket":"day","from":"2026-01-01"}`), "counts back"},
		"unknown language":  {`connector="a" operation="incident-summary" resultVariable="r" ` + reportInput(`{"language":"fr"}`), "language"},
		"malformed feel":    {`connector="a" operation="incident-summary" resultVariable="r" ` + reportInput(`{"process":"=)("}`), "did not compile"},
	} {
		_, err := Parse(1, 1, strings.NewReader(atlasReportTaskBPMN(tc.attrs)))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
}
