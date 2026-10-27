package compiler

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/wikiwikibangbang/catalog"
)

// wikiTaskBPMN builds a one-task model from a raw <atlas:wikiwikibangbangConnector .../>.
// Attributes holding JSON are written in single quotes so the JSON keeps its own.
func wikiTaskBPMN(attrs string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:wikiwikibangbangConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

func wikiDetail(t *testing.T, attrs string) (*CompiledProcess, *ConnectorTaskDetail) {
	t.Helper()
	cp, err := Parse(1, 1, strings.NewReader(wikiTaskBPMN(attrs)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	return cp, cp.ConnectorTask(node.Detail)
}

// A service task bearing <atlas:wikiwikibangbangConnector> is a wiki task: it names a
// Worker, an operation and what the operation acts on — never a wiki's address or a
// token.
func TestParseWikiConnectorTask(t *testing.T) {
	cp, d := wikiDetail(t, `connector="wiki" operation="create-item" space="OPS" itemType="task" title="=antrag.titel" parent="OPS-1" properties='{"assignee":"=antrag.besitzer","source-id":"=antrag.id","points":3}' matchField="source-id" text="=antrag.beschreibung" retries="2" resultVariable="aufgabe"`)
	if d.JobType != WikiwikibangbangJobTypeIndex || cp.Intern(d.JobType) != WikiwikibangbangJobType {
		t.Errorf("jobType = %d %q", d.JobType, cp.Intern(d.JobType))
	}
	if got := cp.Intern(d.Connector); got != "wiki" || d.WikiConnector.Expr != nil {
		t.Errorf("worker = %q (expr %v)", got, d.WikiConnector.Expr)
	}
	if got := cp.Intern(d.WikiOp); got != "create-item" {
		t.Errorf("operation = %q", got)
	}
	if d.WikiSpace.Literal != "OPS" || d.WikiItemType.Literal != "task" || d.WikiParent.Literal != "OPS-1" || d.WikiMatchField.Literal != "source-id" {
		t.Errorf("values = %+v %+v %+v %+v", d.WikiSpace, d.WikiItemType, d.WikiParent, d.WikiMatchField)
	}
	if d.WikiTitle.Expr == nil || d.WikiText.Expr == nil {
		t.Error("a FEEL title or text compiled no expression")
	}
	if d.WikiProperties.Expr == nil {
		t.Fatal("the properties were not compiled to a FEEL context")
	}
	if inputs := d.WikiProperties.Expr.Inputs(); len(inputs) != 1 || inputs[0] != "antrag" {
		t.Errorf("inputs = %v", inputs)
	}
	if d.WikiMaxResults != 0 {
		t.Errorf("maxResults = %d on a create", d.WikiMaxResults)
	}
	if got := cp.Intern(d.ResultVar); got != "aufgabe" || d.Retries != 2 {
		t.Errorf("resultVariable = %q retries %d", got, d.Retries)
	}
	if d.Method != -1 || d.Auth != -1 {
		t.Errorf("a wiki task carries REST settings: method %d auth %d", d.Method, d.Auth)
	}
	// It is not a CardDAV task under another name.
	if d.CardDAVOp != 0 || d.CardDAVQuery.Expr != nil {
		t.Error("a wiki task filled the CardDAV fields")
	}
}

func TestWikiQueryAndBounds(t *testing.T) {
	_, d := wikiDetail(t, `connector="=welches.wiki" operation="query-items" query="type = task AND assignee = currentUser()" resultVariable="offen"`)
	if d.WikiConnector.Expr == nil {
		t.Error("a FEEL worker compiled no expression")
	}
	if d.WikiMaxResults != 100 {
		t.Errorf("maxResults = %d, want the default 100", d.WikiMaxResults)
	}
	// A literal query is not FEEL: the wiki's query language has its own = and quotes.
	if d.WikiQuery.Expr != nil || !strings.Contains(d.WikiQuery.Literal, "currentUser()") {
		t.Errorf("query = %+v", d.WikiQuery)
	}
	_, d = wikiDetail(t, `connector="wiki" operation="query-items" query="type = task" maxResults="1000" resultVariable="x"`)
	if d.WikiMaxResults != 1000 {
		t.Errorf("maxResults = %d", d.WikiMaxResults)
	}
}

func TestWikiBodyAndLabels(t *testing.T) {
	_, d := wikiDetail(t, `connector="wiki" operation="append-to-page" item="=seite.id" blocks='[{"kind":"heading","level":2,"text":"Schritt 1"}]'`)
	if d.WikiBlocks.Expr != nil || !strings.HasPrefix(d.WikiBlocks.Literal, "[") || d.WikiItem.Expr == nil {
		t.Errorf("blocks = %+v item = %+v", d.WikiBlocks, d.WikiItem)
	}
	_, d = wikiDetail(t, `connector="wiki" operation="append-to-page" item="OPS-3" blocks="=protokoll.bloecke"`)
	if d.WikiBlocks.Expr == nil {
		t.Error("FEEL blocks compiled no expression")
	}
	_, d = wikiDetail(t, `connector="wiki" operation="update-item" item="OPS-3" labels="onboarding, it" propertiesVariable="felder"`)
	if d.WikiLabels.Literal != "onboarding, it" {
		t.Errorf("labels = %+v", d.WikiLabels)
	}
	if d.WikiProperties.Expr != nil {
		t.Error("properties compiled from a variable")
	}
}

func TestWikiRefusals(t *testing.T) {
	for attrs, want := range map[string]string{
		`operation="get-item" item="OPS-1" resultVariable="x"`:                                                                       "needs a connector",
		`connector="wiki" item="OPS-1"`:                                                                                              "needs an operation",
		`connector="wiki" operation="assign-item" item="OPS-1"`:                                                                      "unknown operation",
		`connector="wiki" operation="get-item" resultVariable="x"`:                                                                   "needs item",
		`connector="wiki" operation="get-item" item="OPS-1"`:                                                                         "needs a resultVariable",
		`connector="wiki" operation="get-item" item="OPS-1" title="x" resultVariable="x"`:                                            "does not use title",
		`connector="wiki" operation="create-item" itemType="task" title="x"`:                                                         "needs space",
		`connector="wiki" operation="create-item" space="OPS" title="x"`:                                                             "needs itemType",
		`connector="wiki" operation="create-item" space="OPS" itemType="task"`:                                                       "needs title",
		`connector="wiki" operation="create-item" space="OPS" itemType="task" title="x" labels="a"`:                                  "does not use labels",
		`connector="wiki" operation="update-item" item="OPS-1"`:                                                                      "changes nothing",
		`connector="wiki" operation="append-to-page" item="OPS-1"`:                                                                   "needs text or blocks",
		`connector="wiki" operation="append-to-page" item="OPS-1" text="a" blocks='[{"kind":"divider"}]'`:                            "both text and blocks",
		`connector="wiki" operation="append-to-page" item="OPS-1" blocks='{"kind":"divider"}'`:                                       "list of blocks",
		`connector="wiki" operation="append-to-page" item="OPS-1" blocks='[{"text":"x"}]'`:                                           "has no kind",
		`connector="wiki" operation="append-to-page" item="OPS-1" blocks='[nope'`:                                                    "invalid blocks JSON",
		`connector="wiki" operation="get-item" item="OPS/1" resultVariable="x"`:                                                      "not an item id or key",
		`connector="wiki" operation="create-item" space="OPS" itemType="task" title="x" parent="../x"`:                               "not an item id or key",
		`connector="wiki" operation="create-item" space="OPS" itemType="task" title="x" properties='[1]'`:                            "JSON object",
		`connector="wiki" operation="create-item" space="OPS" itemType="task" title="x" properties='{"a=b":1}'`:                      "cannot be addressed",
		`connector="wiki" operation="create-item" space="OPS" itemType="task" title="x" properties='{"a":1}' propertiesVariable="v"`: "both",
		`connector="wiki" operation="create-item" space="OPS" itemType="task" title="x" matchField="source-id"`:                      "matchField",
		`connector="wiki" operation="create-item" space="OPS" itemType="task" title="x" properties='{"a":1}' matchField="source-id"`: "names a property the task does not set",
		`connector="wiki" operation="query-items" query="x" maxResults="1001" resultVariable="x"`:                                    "ceiling",
		`connector="wiki" operation="query-items" query="x" maxResults="0" resultVariable="x"`:                                       "at least 1",
		`connector="wiki" operation="query-items" query="x" maxResults="many" resultVariable="x"`:                                    "non-numeric",
		`connector="wiki" operation="transition-item" item="OPS-1"`:                                                                  "needs transition",
		`connector="wiki" operation="add-comment" item="OPS-1"`:                                                                      "needs comment",
	} {
		_, err := Parse(1, 1, strings.NewReader(wikiTaskBPMN(attrs)))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want %q", attrs, err, want)
		}
	}
}

// With propertiesVariable, matchField cannot be checked at deploy; the worker checks it.
func TestWikiMatchFieldWithAVariableIsCheckedLater(t *testing.T) {
	_, d := wikiDetail(t, `connector="wiki" operation="create-item" space="OPS" itemType="task" title="x" propertiesVariable="felder" matchField="source-id"`)
	if d.WikiMatchField.Literal != "source-id" {
		t.Errorf("matchField = %+v", d.WikiMatchField)
	}
}

// Every operation in the catalog compiles from its needed values alone, so the table
// and the compiler cannot disagree about what an operation needs.
func TestEveryWikiOperationCompilesFromItsNeeds(t *testing.T) {
	value := map[string]string{
		catalog.AttrItem: `item="OPS-1"`, catalog.AttrSpace: `space="OPS"`, catalog.AttrItemType: `itemType="task"`,
		catalog.AttrTitle: `title="x"`, catalog.AttrBody: `text="x"`, catalog.AttrTransition: `transition="finish"`,
		catalog.AttrComment: `comment="x"`, catalog.AttrQuery: `query="type = task"`,
	}
	for _, name := range catalog.Names() {
		op, _ := catalog.Lookup(name)
		attrs := []string{`connector="wiki"`, `operation="` + name + `"`}
		for _, a := range op.Needs {
			attrs = append(attrs, value[a])
		}
		if op.NeedsOneTaken {
			attrs = append(attrs, `title="x"`)
		}
		if op.NeedsResult {
			attrs = append(attrs, `resultVariable="out"`)
		}
		if _, err := Parse(1, 1, strings.NewReader(wikiTaskBPMN(strings.Join(attrs, " ")))); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
