package compiler

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/carddav/catalog"
)

// cardDAVTaskBPMN builds a one-task model from a raw <atlas:carddavConnector .../>.
// Attributes holding JSON are written in single quotes so the JSON keeps its own.
func cardDAVTaskBPMN(attrs string) string {
	return `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:carddavConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

func cardDAVDetail(t *testing.T, attrs string) (*CompiledProcess, *ConnectorTaskDetail) {
	t.Helper()
	cp, err := Parse(1, 1, strings.NewReader(cardDAVTaskBPMN(attrs)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target)
	if node.Type != TypeConnectorTask {
		t.Fatalf("task node type = %v, want ConnectorTask", node.Type)
	}
	return cp, cp.ConnectorTask(node.Detail)
}

// A service task bearing <atlas:carddavConnector> is a CardDAV task: it names a Worker,
// an operation and what the operation acts on — never a server or a credential.
func TestParseCardDAVConnectorTask(t *testing.T) {
	cp, d := cardDAVDetail(t, `connector="verkauf" operation="find-contacts" query="=anfrage.absender" field="Kundennummer" group="Kunden" retries="2" resultVariable="treffer"`)
	if d.JobType != CardDAVJobTypeIndex || cp.Intern(d.JobType) != CardDAVJobType {
		t.Errorf("jobType = %d %q", d.JobType, cp.Intern(d.JobType))
	}
	if got := cp.Intern(d.Connector); got != "verkauf" || d.CardDAVConnector.Expr != nil {
		t.Errorf("worker = %q (expr %v)", got, d.CardDAVConnector.Expr)
	}
	if got := cp.Intern(d.CardDAVOp); got != "find-contacts" {
		t.Errorf("operation = %q", got)
	}
	if d.CardDAVQuery.Expr == nil || d.CardDAVField.Literal != "Kundennummer" || d.CardDAVGroup.Literal != "Kunden" {
		t.Errorf("values = %+v %+v %+v", d.CardDAVQuery, d.CardDAVField, d.CardDAVGroup)
	}
	if d.CardDAVMaxResults != 100 {
		t.Errorf("maxResults = %d", d.CardDAVMaxResults)
	}
	if got := cp.Intern(d.ResultVar); got != "treffer" || d.Retries != 2 {
		t.Errorf("resultVariable = %q retries %d", got, d.Retries)
	}
	if d.Method != -1 || d.Auth != -1 {
		t.Errorf("an address-book task carries REST settings: method %d auth %d", d.Method, d.Auth)
	}
	// It is not a Google Contacts task under another name.
	if d.GoogleContactsOp != 0 || d.GoogleContactsQuery.Expr != nil {
		t.Error("a CardDAV task filled the Google Contacts fields")
	}
}

func TestCardDAVContactCompilesToOneContext(t *testing.T) {
	cp, d := cardDAVDetail(t, `connector="=buch" operation="update-contact" contactId="=kunde.id" contact='{"email":"=formular.mail","fields":{"Quelle":"Web"}}' resultVariable="kunde"`)
	if d.CardDAVConnector.Expr == nil {
		t.Error("a FEEL worker compiled no expression")
	}
	if d.CardDAVContact.Expr == nil {
		t.Fatal("the contact was not compiled to a FEEL context")
	}
	if inputs := d.CardDAVContact.Expr.Inputs(); len(inputs) != 1 || inputs[0] != "formular" {
		t.Errorf("inputs = %v", inputs)
	}
	if d.CardDAVMaxResults != 0 || cp.Intern(d.CardDAVContactVar) != "" {
		t.Errorf("maxResults %d, contactVariable %q", d.CardDAVMaxResults, cp.Intern(d.CardDAVContactVar))
	}
	_, d = cardDAVDetail(t, `connector="v" operation="create-contact" contactVariable="neukunde"`)
	if d.CardDAVContact.Expr != nil {
		t.Error("a contact variable compiled an inline contact")
	}
}

func TestCardDAVMaxResults(t *testing.T) {
	for _, c := range []struct {
		attrs string
		want  int32
	}{
		{`operation="list-contacts" resultVariable="r"`, 500},
		{`operation="list-contacts" maxResults="25" resultVariable="r"`, 25},
		{`operation="search-contacts" query="Ann" resultVariable="r"`, 10},
		{`operation="search-contacts" query="Ann" maxResults="100" resultVariable="r"`, 100},
	} {
		_, d := cardDAVDetail(t, `connector="v" `+c.attrs)
		if d.CardDAVMaxResults != c.want {
			t.Errorf("%s: maxResults = %d, want %d", c.attrs, d.CardDAVMaxResults, c.want)
		}
	}
}

// TestEveryCardDAVOperationCompiles: each row of the catalog deploys with the values it
// needs, so a row the compiler cannot express is found here and not by a model.
func TestEveryCardDAVOperationCompiles(t *testing.T) {
	for _, name := range catalog.Names() {
		op, _ := catalog.Lookup(name)
		attrs := `connector="v" operation="` + name + `"`
		if op.NeedsContactID {
			attrs += ` contactId="5F3A.vcf"`
		}
		if op.NeedsQuery {
			attrs += ` query="a@b.ch"`
		}
		if op.NeedsGroup {
			attrs += ` group="Kunden"`
		}
		if op.NeedsContact {
			attrs += ` contact='{"familyName":"Muster"}'`
		}
		if op.NeedsResult {
			attrs += ` resultVariable="r"`
		}
		if _, err := Parse(1, 1, strings.NewReader(cardDAVTaskBPMN(attrs))); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCardDAVRefusals(t *testing.T) {
	for _, c := range []struct {
		name, attrs, want string
	}{
		{"no worker", `operation="get-contact" contactId="c1" resultVariable="k"`, "needs a connector"},
		{"bad worker FEEL", `connector="=" operation="get-contact" contactId="c1" resultVariable="k"`, "empty FEEL"},
		{"no operation", `connector="v"`, "needs an operation"},
		{"unknown operation", `connector="v" operation="search-directory" query="a" resultVariable="r"`, "unknown operation"},
		{"missing contactId", `connector="v" operation="get-contact" resultVariable="k"`, "needs contactId"},
		{"contactId not used", `connector="v" operation="list-contacts" contactId="c1" resultVariable="k"`, "does not use contactId"},
		{"literal contactId a path", `connector="v" operation="get-contact" contactId="../x.vcf" resultVariable="k"`, "not a contact id"},
		{"missing query", `connector="v" operation="find-contacts" resultVariable="k"`, "needs query"},
		{"field outside find", `connector="v" operation="search-contacts" query="a" field="Kundennummer" resultVariable="k"`, "does not use field"},
		{"field no property name", `connector="v" operation="find-contacts" query="a" field="Kunden Nr" resultVariable="k"`, "letters, digits and hyphens"},
		{"missing group", `connector="v" operation="add-to-group" contactId="c1"`, "needs group"},
		{"group on a search", `connector="v" operation="search-contacts" query="a" group="Kunden" resultVariable="k"`, "does not use group"},
		{"missing contact", `connector="v" operation="create-contact"`, "needs contact"},
		{"contact on a read", `connector="v" operation="get-contact" contactId="c1" contact='{"familyName":"X"}' resultVariable="k"`, "does not use contact"},
		{"both contacts", `connector="v" operation="create-contact" contact='{"familyName":"X"}' contactVariable="k"`, "both an inline contact and a contactVariable"},
		{"contact not JSON", `connector="v" operation="create-contact" contact='{familyName:X}'`, "invalid contact JSON"},
		{"contact not an object", `connector="v" operation="create-contact" contact='["X"]'`, "must be a JSON object"},
		{"contact unknown key", `connector="v" operation="create-contact" contact='{"famillyName":"X"}'`, `no key "famillyName"`},
		{"address key vCard cannot hold", `connector="v" operation="create-contact" contact='{"address":{"countryCode":"CH"}}'`, `no key "countryCode"`},
		{"contact bad FEEL", `connector="v" operation="create-contact" contact='{"familyName":"=)"}'`, "did not compile"},
		{"contact empty FEEL", `connector="v" operation="create-contact" contact='{"familyName":"= "}'`, "empty =expression"},
		{"groups on update", `connector="v" operation="update-contact" contactId="c1" contact='{"groups":["Kunden"]}'`, "add-to-group"},
		{"read without result", `connector="v" operation="find-contacts" query="a"`, "needs a resultVariable"},
		{"result on a delete", `connector="v" operation="delete-contact" contactId="c1" resultVariable="r"`, "returns nothing"},
		{"maxResults on a read of one", `connector="v" operation="get-contact" contactId="c1" maxResults="5" resultVariable="r"`, "does not use maxResults"},
		{"maxResults not a number", `connector="v" operation="list-contacts" maxResults="viele" resultVariable="r"`, "non-numeric"},
		{"maxResults zero", `connector="v" operation="list-contacts" maxResults="0" resultVariable="r"`, "at least 1"},
		{"search above its ceiling", `connector="v" operation="search-contacts" query="a" maxResults="101" resultVariable="r"`, "ceiling of 100"},
		{"bad query FEEL", `connector="v" operation="find-contacts" query="=)" resultVariable="r"`, "query"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(1, 1, strings.NewReader(cardDAVTaskBPMN(c.attrs)))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want mention of %q", err, c.want)
			}
		})
	}
}
