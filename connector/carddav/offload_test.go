package carddav

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

type fakeVarStore struct {
	vars map[uint64][]model.VariableValue
}

func (f fakeVarStore) VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error {
	for i := range f.vars[scope] {
		v := f.vars[scope][i]
		if err := fn(&v); err != nil {
			return err
		}
	}
	return nil
}

func (f fakeVarStore) GetElementInstance(uint64) (*model.ElementInstanceValue, bool, error) {
	return nil, false, nil
}

func compileTask(t *testing.T, attrs string) (*compiler.CompiledProcess, *compiler.ConnectorTaskDetail) {
	t.Helper()
	bpmn := `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0" id="d">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:carddavConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := compiler.Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target)
	return cp, cp.ConnectorTask(node.Detail)
}

func TestResolveEvaluatesEveryValue(t *testing.T) {
	cp, d := compileTask(t, `connector="=buch" operation="find-contacts" query="=anfrage.absender" field="Kundennummer" group="=segment" maxResults="5" resultVariable="treffer"`)
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{10: {
		{Name: "buch", Kind: model.VarString, Text: "verkauf"},
		{Name: "anfrage", Kind: model.VarJSON, Text: `{"absender":"K-4711"}`},
		{Name: "segment", Kind: model.VarString, Text: "Kunden"},
	}}}
	j, err := Resolve(store, cp, d, 10, 77, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	// A find creates nothing, so it carries no create key.
	want := Job{Connector: "verkauf", Operation: "find-contacts", Query: "K-4711", Field: "Kundennummer",
		Group: "Kunden", MaxResults: 5, ResultVariable: "treffer", ErrorCodes: []string{}}
	if !reflect.DeepEqual(j, want) {
		t.Errorf("job = %#v\nwant  %#v", j, want)
	}
}

// TestTheCreateKeyIsTheJobsOwn: the same job on the same installation derives the same
// key on every delivery; another job, or the same job key on another installation, a
// different one — which is what keeps a test server and a production server writing
// into one address book from taking each other's contacts for their own.
func TestTheCreateKeyIsTheJobsOwn(t *testing.T) {
	cp, d := compileTask(t, `connector="verkauf" operation="create-contact" contact='{"familyName":"Muster"}'`)
	resolve := func(jobKey uint64, node string) string {
		j, err := Resolve(fakeVarStore{}, cp, d, 10, jobKey, node)
		if err != nil {
			t.Fatal(err)
		}
		return j.CreateKey
	}
	first := resolve(77, "node-a")
	if first == "" || first != resolve(77, "node-a") {
		t.Fatalf("the create key is not stable across deliveries: %q", first)
	}
	if first == resolve(78, "node-a") || first == resolve(77, "node-b") {
		t.Error("two jobs share a create key")
	}
	uid := CreateUID(first)
	if len(uid) != 36 || uid != strings.ToUpper(uid) || uid[14] != '8' || !strings.ContainsAny(uid[19:20], "89AB") {
		t.Errorf("CreateUID = %q, want an upper-case version 8 UUID", uid)
	}
	if CreateUID(first) != uid {
		t.Error("CreateUID is not a function of the key")
	}
	// A hand-built job without a key still gets a UID, a random one.
	if a, b := CreateUID(""), CreateUID("zz"); len(a) != 36 || a == b {
		t.Errorf("random UIDs %q %q", a, b)
	}
}

// TestResolveEvaluatesTheContactTemplate: the template's =leaves read the instance's
// variables, its literals stay what they are, and an unset variable is a null — which
// the worker reads as "leave it as it is".
func TestResolveEvaluatesTheContactTemplate(t *testing.T) {
	cp, d := compileTask(t, `connector="verkauf" operation="update-contact" contactId="=kunde.id" contact='{"email":"=formular.mail","fields":{"Kundennummer":"=nummer","Quelle":"Webformular"},"notes":"=fehlt"}' resultVariable="kunde"`)
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{10: {
		{Name: "kunde", Kind: model.VarJSON, Text: `{"id":"5F3A.vcf"}`},
		{Name: "formular", Kind: model.VarJSON, Text: `{"mail":"neu@kunde.ch"}`},
		{Name: "nummer", Kind: model.VarNumber, Text: "4711"},
	}}}
	j, err := Resolve(store, cp, d, 10, 1, "n")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"email": "neu@kunde.ch", "fields": map[string]any{"Kundennummer": json.Number("4711"), "Quelle": "Webformular"}, "notes": nil}
	if j.ContactID != "5F3A.vcf" || !reflect.DeepEqual(j.Contact, want) || j.CreateKey != "" {
		t.Errorf("job = %#v", j)
	}
	raw, _ := json.Marshal(j)
	if strings.Contains(strings.ToLower(string(raw)), "password") {
		t.Errorf("the resolved job carries a password field: %s", raw)
	}
}

func TestResolveReadsTheContactVariable(t *testing.T) {
	cp, d := compileTask(t, `connector="verkauf" operation="create-contact" contactVariable="neukunde"`)
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{10: {
		{Name: "neukunde", Kind: model.VarJSON, Text: `{"familyName":"Muster","groups":["Kunden"]}`},
	}}}
	j, err := Resolve(store, cp, d, 10, 1, "n")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(j.Contact, map[string]any{"familyName": "Muster", "groups": []any{"Kunden"}}) {
		t.Errorf("contact = %#v", j.Contact)
	}
}

func TestResolveRefusals(t *testing.T) {
	for _, c := range []struct {
		name, attrs string
		vars        []model.VariableValue
		want        string
	}{
		{"variable not set", `connector="c" operation="create-contact" contactVariable="k"`, nil, `"k" is not set`},
		{"variable not an object", `connector="c" operation="create-contact" contactVariable="k"`,
			[]model.VariableValue{{Name: "k", Kind: model.VarString, Text: "Muster"}}, "must be an object"},
		{"variable is a list", `connector="c" operation="create-contact" contactVariable="k"`,
			[]model.VariableValue{{Name: "k", Kind: model.VarJSON, Text: `["Muster"]`}}, "not a JSON object"},
		{"worker expression empty", `connector="=buch" operation="delete-contact" contactId="c1"`, nil, "empty"},
	} {
		t.Run(c.name, func(t *testing.T) {
			cp, d := compileTask(t, c.attrs)
			_, err := Resolve(fakeVarStore{vars: map[uint64][]model.VariableValue{10: c.vars}}, cp, d, 10, 1, "n")
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want mention of %q", err, c.want)
			}
		})
	}
	if _, err := Resolve(fakeVarStore{}, nil, nil, 10, 1, "n"); err == nil {
		t.Error("a task without detail resolved")
	}
}

// TestResolveBindsEveryVariableKind: a value reads booleans, numbers, strings and
// objects, and the instance's own key under processInstanceKey.
func TestResolveBindsEveryVariableKind(t *testing.T) {
	cp, d := compileTask(t, `connector="c" operation="create-contact" contact='{"notes":"=string(processInstanceKey)","fields":{"Aktiv":"=aktiv","Stufe":"=stufe","Ort":"=adresse.ort"}}'`)
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{42: {
		{Name: "aktiv", Kind: model.VarBool, Bool: true},
		{Name: "stufe", Kind: model.VarNumber, Text: "3"},
		{Name: "adresse", Kind: model.VarJSON, Text: `{"ort":"Bern"}`},
		{Name: "leer", Kind: model.VarNull},
	}}}
	j, err := Resolve(store, cp, d, 42, 1, "n")
	if err != nil {
		t.Fatal(err)
	}
	if j.Contact["notes"] != "42" || !reflect.DeepEqual(j.Contact["fields"], map[string]any{"Aktiv": true, "Stufe": json.Number("3"), "Ort": "Bern"}) {
		t.Errorf("contact = %#v", j.Contact)
	}
	cp, d = compileTask(t, `connector="c" operation="get-contact" contactId="=leer" resultVariable="k"`)
	j, err = Resolve(store, cp, d, 42, 1, "n")
	if err != nil || j.ContactID != "" {
		t.Errorf("unset id = %q, err %v", j.ContactID, err)
	}
	if toExprKind(model.VarKind(99)) != toExprKind(model.VarNull) {
		t.Error("an unknown variable kind binds as something other than null")
	}
}

// TestResolveCarriesTheCaughtErrorCodes: a worker has no compiled process to ask which
// errors the process catches, so the job says.
func TestResolveCarriesTheCaughtErrorCodes(t *testing.T) {
	bpmn := `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0" id="d">
  <bpmn:error id="err" errorCode="CARDDAV_CONTACT_NOT_FOUND"/>
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:carddavConnector connector="c" operation="get-contact" contactId="c1" resultVariable="k"/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:boundaryEvent id="b" attachedToRef="t"><bpmn:errorEventDefinition errorRef="err"/></bpmn:boundaryEvent>
    <bpmn:endEvent id="e"/>
    <bpmn:endEvent id="e2"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
    <bpmn:sequenceFlow id="f3" sourceRef="b" targetRef="e2"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := compiler.Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatal(err)
	}
	var detail *compiler.ConnectorTaskDetail
	for i := int32(0); i < int32(cp.NodeCount()); i++ {
		if n := cp.Node(i); n.Type == compiler.TypeConnectorTask {
			detail = cp.ConnectorTask(n.Detail)
		}
	}
	j, err := Resolve(fakeVarStore{}, cp, detail, 1, 1, "n")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(j.ErrorCodes, []string{CodeContactNotFound}) {
		t.Errorf("errorCodes = %v", j.ErrorCodes)
	}
}
