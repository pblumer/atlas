package compiler

import (
	"strings"
	"testing"
)

// A service task bearing an <atlas:sharepointConnector> extension is a SharePoint
// task (ADR-0141): it creates a list item in a model-authored site/list
// through a server-registered SharePoint provider (Microsoft Graph) via the job
// path, mirroring the mail worker (the Graph base and OAuth credential live
// server-side, never in the model) while the target (site, list, item fields) is
// model-authored like REST's endpoint.
const sharePointConnectorBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:sharepointConnector connector="contoso" site="contoso.sharepoint.com,/sites/ops"
                                   list="Incidents" resultVariable="created">
          <atlas:itemField name="Title" value="Order shipped"/>
          <atlas:itemField name="Customer" value="=customer.name"/>
        </atlas:sharepointConnector>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseSharePointConnectorTask(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(sharePointConnectorBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	node := cp.Node(task)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	d := cp.ConnectorTask(node.Detail)
	if got := cp.Intern(d.JobType); got != SharePointJobType {
		t.Errorf("jobType = %q, want %q", got, SharePointJobType)
	}
	if d.JobType != SharePointJobTypeIndex {
		t.Errorf("jobType index = %d, want the reserved SharePointJobTypeIndex %d", d.JobType, SharePointJobTypeIndex)
	}
	if got := cp.Intern(d.Connector); got != "contoso" {
		t.Errorf("worker = %q, want contoso", got)
	}
	if d.Site.Expr != nil || d.Site.Literal != "contoso.sharepoint.com,/sites/ops" {
		t.Errorf("site = %+v, want the literal site", d.Site)
	}
	if d.List.Expr != nil || d.List.Literal != "Incidents" {
		t.Errorf("list = %+v, want the literal list", d.List)
	}
	if got := cp.Intern(d.ResultVar); got != "created" {
		t.Errorf("resultVariable = %q, want created", got)
	}
	if len(d.Fields) != 2 {
		t.Fatalf("fields = %+v, want two item fields", d.Fields)
	}
	if d.Fields[0].Name != "Title" || d.Fields[0].Val.Literal != "Order shipped" {
		t.Errorf("field[0] = %+v, want literal Title", d.Fields[0])
	}
	if d.Fields[1].Name != "Customer" || d.Fields[1].Val.Expr == nil {
		t.Errorf("field[1] = %+v, want a FEEL Customer field", d.Fields[1])
	}
	// A SharePoint task leaves the REST-only URL and the clio-only coordinates unset.
	if d.Url.Expr != nil || d.Url.Literal != "" {
		t.Errorf("url = %+v, want empty for a SharePoint task", d.Url)
	}
	if cp.Intern(d.EventType) != "" || cp.Intern(d.Method) != "" {
		t.Errorf("clio/REST-only fields not empty for a SharePoint task: eventType=%q method=%q",
			cp.Intern(d.EventType), cp.Intern(d.Method))
	}
}

// The site and list fields may be FEEL expressions (a leading '='), evaluated over
// the instance's variables at call time; a literal stays literal.
func TestParseSharePointConnectorFeelFields(t *testing.T) {
	const bpmn = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:sharepointConnector connector="contoso" site="=siteRef" list="=listName">
          <atlas:itemField name="Title" value="=subject"/>
        </atlas:sharepointConnector>
      </bpmn:extensionElements>
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
	if d.Site.Expr == nil {
		t.Errorf("site should be a FEEL expression, got literal %q", d.Site.Literal)
	}
	if d.List.Expr == nil {
		t.Errorf("list should be a FEEL expression, got literal %q", d.List.Literal)
	}
	if len(d.Fields) != 1 || d.Fields[0].Val.Expr == nil {
		t.Errorf("item field should be a FEEL expression, got %+v", d.Fields)
	}
}

func TestParseSharePointConnectorErrors(t *testing.T) {
	wrap := func(inner string) string {
		return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>` + inner + `</bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	}
	// Each required-field and malformed-FEEL case exercises one validation branch.
	cases := map[string]string{
		"missing worker":      `<atlas:sharepointConnector site="s" list="l"/>`,
		"missing site":        `<atlas:sharepointConnector connector="c" list="l"/>`,
		"missing list":        `<atlas:sharepointConnector connector="c" site="s"/>`,
		"malformed FEEL site": `<atlas:sharepointConnector connector="c" site="=(" list="l"/>`,
		"malformed FEEL list": `<atlas:sharepointConnector connector="c" site="s" list="=("/>`,
		"malformed FEEL field": `<atlas:sharepointConnector connector="c" site="s" list="l">
			<atlas:itemField name="Title" value="=("/></atlas:sharepointConnector>`,
		"duplicate field": `<atlas:sharepointConnector connector="c" site="s" list="l">
			<atlas:itemField name="Title" value="a"/><atlas:itemField name="Title" value="b"/></atlas:sharepointConnector>`,
	}
	for name, inner := range cases {
		if _, err := Parse(1, 1, strings.NewReader(wrap(inner))); err == nil {
			t.Errorf("%s: want a compile error, got nil", name)
		}
	}
}

// sharePointCamelBPMN is the same task under the tag the Modeler writes. bpmn-js
// derives an element's tag from its moddle type by lowercasing only the first
// letter, so the type SharePointConnector serializes as <atlas:sharePointConnector>
// — with a capital P — while hand-authored models use the all-lowercase spelling
// above.
const sharePointCamelBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:sharePointConnector connector="contoso" site="contoso.sharepoint.com,/sites/ops"
                                   list="Incidents" resultVariable="created">
          <atlas:itemField name="Title" value="Order shipped"/>
        </atlas:sharePointConnector>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`

// TestParseSharePointConnectorCamelTag: a SharePoint task authored in the Modeler
// must compile exactly like the hand-authored one. Go's XML matching is
// case-sensitive, so before both spellings were read this task parsed as an
// ordinary, unconfigured service task — its configuration sitting in the XML,
// silently ignored, with no error to notice.
func TestParseSharePointConnectorCamelTag(t *testing.T) {
	cp, err := Parse(1, 1, strings.NewReader(sharePointCamelBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	node := cp.Node(task)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask — the Modeler's tag was ignored", node.Type)
	}
	d := cp.ConnectorTask(node.Detail)
	if d.JobType != SharePointJobTypeIndex {
		t.Errorf("jobType index = %d, want SharePointJobTypeIndex %d", d.JobType, SharePointJobTypeIndex)
	}
	if got := cp.Intern(d.Connector); got != "contoso" {
		t.Errorf("worker = %q, want contoso", got)
	}
	if d.List.Literal != "Incidents" {
		t.Errorf("list = %+v, want Incidents", d.List)
	}
	if len(d.Fields) != 1 || d.Fields[0].Name != "Title" {
		t.Errorf("fields = %+v, want the Title item field", d.Fields)
	}
}

// sharePointTask compiles one SharePoint task carrying attrs and the given children,
// and returns its detail, or the compile error.
func sharePointTask(t *testing.T, attrs, children string) (*CompiledProcess, *ConnectorTaskDetail, error) {
	t.Helper()
	bpmn := `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:sharepointConnector ` + attrs + `>` + children + `</atlas:sharepointConnector></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		return nil, nil, err
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	return cp, cp.ConnectorTask(cp.Node(task).Detail), nil
}

// Create-item keeps the job type it always had, whether a model names it or leaves the
// operation out, and every other operation carries the operations job type. The split is
// what keeps an older worker — one that reads every job as "create an item" — from ever
// being handed a read or a delete.
func TestSharePointOperationsChooseTheirJobType(t *testing.T) {
	for _, tc := range []struct {
		name, attrs, children string
		wantJob               int32
		wantOp                string
	}{
		{"no operation", `connector="c" site="s" list="l"`, "", SharePointJobTypeIndex, "create-item"},
		{"create-item", `connector="c" operation="create-item" site="s" list="l"`, "", SharePointJobTypeIndex, "create-item"},
		{"get-item", `connector="c" operation="get-item" site="s" list="l" itemId="7" resultVariable="r"`, "", SharePointOpsJobTypeIndex, "get-item"},
		{"upload-file", `connector="c" operation="Upload-File" site="s" path="a/b.txt" content="hi"`, "", SharePointOpsJobTypeIndex, "upload-file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cp, d, err := sharePointTask(t, tc.attrs, tc.children)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if d.JobType != tc.wantJob {
				t.Errorf("job type = %q, want %q", cp.Intern(d.JobType), cp.Intern(tc.wantJob))
			}
			if got := cp.Intern(d.SharePointOp); got != tc.wantOp {
				t.Errorf("operation = %q, want %q", got, tc.wantOp)
			}
		})
	}
}

// Every value an operation takes reaches the detail, as literal or FEEL, and the two
// compiled-structure values carry their defaults when a model authors none.
func TestSharePointOperationValuesReachTheDetail(t *testing.T) {
	cp, d, err := sharePointTask(t,
		`connector="c" operation="list-items" site="=siteUrl" list="Auftraege" filter="=&quot;fields/Status eq '&quot; + status + &quot;'&quot;" select="Title,Status" pageToken="=seite.nextPageToken" resultVariable="seite"`, "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d.Site.Expr == nil || d.SharePointFilter.Expr == nil || d.SharePointPageToken.Expr == nil {
		t.Errorf("site/filter/pageToken = %+v / %+v / %+v, want FEEL", d.Site, d.SharePointFilter, d.SharePointPageToken)
	}
	if d.List.Literal != "Auftraege" || d.SharePointSelect.Literal != "Title,Status" {
		t.Errorf("list/select = %q / %q, want the literals", d.List.Literal, d.SharePointSelect.Literal)
	}
	if d.SharePointMaxResults != sharePointDefaultMaxResults {
		t.Errorf("maxResults = %d, want the default %d", d.SharePointMaxResults, sharePointDefaultMaxResults)
	}
	if got := cp.Intern(d.ResultVar); got != "seite" {
		t.Errorf("resultVariable = %q, want seite", got)
	}

	cp, d, err = sharePointTask(t, `connector="c" operation="upload-file" site="s" list="Vertraege" path="=pfad" content="=pdf" encoding="base64"`,
		`<atlas:itemField name="Kunde" value="=kunde.name"/>`)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d.SharePointPath.Expr == nil || d.SharePointContent.Expr == nil {
		t.Errorf("path/content = %+v / %+v, want FEEL", d.SharePointPath, d.SharePointContent)
	}
	if got := cp.Intern(d.SharePointEncoding); got != "base64" {
		t.Errorf("encoding = %q, want base64", got)
	}
	if len(d.Fields) != 1 || d.Fields[0].Name != "Kunde" {
		t.Errorf("fields = %+v, want the Kunde property", d.Fields)
	}

	cp, d, err = sharePointTask(t, `connector="c" operation="download-file" site="s" path="a.txt" resultVariable="datei"`, "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cp.Intern(d.SharePointEncoding); got != sharePointEncodingText {
		t.Errorf("encoding = %q, want the text default", got)
	}

	_, d, err = sharePointTask(t, `connector="c" operation="list-folder" site="s" maxResults="999" resultVariable="r"`, "")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if d.SharePointMaxResults != 999 {
		t.Errorf("maxResults = %d, want 999", d.SharePointMaxResults)
	}
}

// Each refusal is a value that would otherwise be missing at call time or silently
// dropped there.
func TestSharePointOperationRules(t *testing.T) {
	field := `<atlas:itemField name="Title" value="x"/>`
	for name, tc := range map[string]struct{ attrs, children, want string }{
		"unknown operation":         {`connector="c" operation="move-item" site="s" list="l"`, "", "unknown operation"},
		"get-item without id":       {`connector="c" operation="get-item" site="s" list="l" resultVariable="r"`, "", "needs an itemId"},
		"get-item without result":   {`connector="c" operation="get-item" site="s" list="l" itemId="1"`, "", "needs a resultVariable"},
		"get-item with a filter":    {`connector="c" operation="get-item" site="s" list="l" itemId="1" filter="x" resultVariable="r"`, "", "does not use filter"},
		"update-item without field": {`connector="c" operation="update-item" site="s" list="l" itemId="1"`, "", "needs at least one itemField"},
		"delete-item with fields":   {`connector="c" operation="delete-item" site="s" list="l" itemId="1"`, field, "does not use itemField"},
		"list-items without list":   {`connector="c" operation="list-items" site="s" resultVariable="r"`, "", "needs a list"},
		"item op with a path":       {`connector="c" operation="create-item" site="s" list="l" path="a"`, "", "does not use path"},
		"file op with an item id":   {`connector="c" operation="get-file" site="s" path="a" itemId="1" resultVariable="r"`, "", "does not use itemId"},
		"upload without content":    {`connector="c" operation="upload-file" site="s" path="a"`, "", "needs content"},
		"download with content":     {`connector="c" operation="download-file" site="s" path="a" content="x" resultVariable="r"`, "", "does not use content"},
		"folder listing filtered":   {`connector="c" operation="list-folder" site="s" filter="x" resultVariable="r"`, "", "does not use filter"},
		"unknown encoding":          {`connector="c" operation="download-file" site="s" path="a" encoding="hex" resultVariable="r"`, "", "unknown encoding"},
		"encoding on an item":       {`connector="c" operation="get-item" site="s" list="l" itemId="1" encoding="base64" resultVariable="r"`, "", "does not use encoding"},
		"maxResults past a page":    {`connector="c" operation="list-items" site="s" list="l" maxResults="1000" resultVariable="r"`, "", "at most 999"},
		"maxResults not a number":   {`connector="c" operation="list-items" site="s" list="l" maxResults="viele" resultVariable="r"`, "", "non-numeric"},
		"maxResults zero":           {`connector="c" operation="list-folder" site="s" maxResults="0" resultVariable="r"`, "", "must be positive"},
		"malformed FEEL item id":    {`connector="c" operation="delete-item" site="s" list="l" itemId="=("`, "", "itemId"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := sharePointTask(t, tc.attrs, tc.children)
			if err == nil {
				t.Fatalf("compiled; want a refusal mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want it to mention %q", err, tc.want)
			}
		})
	}
}
