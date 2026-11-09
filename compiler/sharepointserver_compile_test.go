package compiler

import (
	"strings"
	"testing"
)

func spServerBPMN(attrs string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true"><bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:sharepointServerConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/><bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process></bpmn:definitions>`
}

func TestParseSharePointServerTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(spServerBPMN(`connector="farm" operation="create-site" parameters='{"Site":"=alias","Name":"=titel","OwnerAlias":"=besitzer"}' resultVariable="site"`)))
	if err != nil {
		t.Fatal(err)
	}
	node := cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target)
	d := cp.ConnectorTask(node.Detail)
	if d.JobType != SharePointServerJobTypeIndex || cp.Intern(d.Connector) != "farm" || cp.Intern(d.SharePointServerOp) != "create-site" || d.SharePointServerParameters.Expr == nil || cp.Intern(d.ResultVar) != "site" {
		t.Errorf("detail = %+v", d)
	}
}

func TestSharePointServerDeployRefusals(t *testing.T) {
	for name, tc := range map[string]struct{ attrs, want string }{
		"no worker":      {`operation="get-site" parameters='{"Site":"a"}' resultVariable="s"`, "connector attribute"},
		"FEEL worker":    {`connector="=w" operation="get-site" parameters='{"Site":"a"}' resultVariable="s"`, "FEEL"},
		"no operation":   {`connector="w"`, "needs an operation"},
		"unknown op":     {`connector="w" operation="x"`, "unknown operation"},
		"bad JSON":       {`connector="w" operation="get-site" parameters="{" resultVariable="s"`, "invalid parameters"},
		"null params":    {`connector="w" operation="get-site" parameters="null" resultVariable="s"`, "JSON object"},
		"missing param":  {`connector="w" operation="get-site" resultVariable="s"`, "needs the parameter"},
		"bad FEEL":       {`connector="w" operation="get-site" parameters='{"Site":"=("}' resultVariable="s"`, "did not compile"},
		"read no result": {`connector="w" operation="get-site" parameters='{"Site":"a"}'`, "needs a resultVariable"},
		"change result":  {`connector="w" operation="delete-site" parameters='{"Site":"a"}' resultVariable="s"`, "returns nothing"},
		"a URL":          {`connector="w" operation="get-site" parameters='{"Site":"http://x/sites/a"}' resultVariable="s"`, "no URL"},
	} {
		if _, err := Parse(1, 1, strings.NewReader(spServerBPMN(tc.attrs))); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
}
