package wikiwikibangbang

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

// GetElementInstance knows the task's element instance 10, in process instance 99.
func (f fakeVarStore) GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error) {
	if key != 10 {
		return nil, false, nil
	}
	return &model.ElementInstanceValue{ProcessInstanceKey: 99}, true, nil
}

func compileTask(t *testing.T, attrs string) (*compiler.CompiledProcess, *compiler.ConnectorTaskDetail) {
	t.Helper()
	bpmn := `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0" id="d">
  <bpmn:process id="p" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:wikiwikibangbangConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
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

func vars(vs ...model.VariableValue) fakeVarStore {
	return fakeVarStore{vars: map[uint64][]model.VariableValue{10: vs}}
}

func TestResolveACreate(t *testing.T) {
	cp, d := compileTask(t, `connector="=welches" operation="create-item" space="OPS" itemType="task" title="=antrag.titel" parent="=antrag.seite" properties='{"assignee":"=antrag.besitzer","points":3,"due":"=antrag.faellig","source-id":"=string(processInstanceKey)"}' matchField="source-id" text="=antrag.text" resultVariable="aufgabe"`)
	store := vars(
		model.VariableValue{Name: "welches", Kind: model.VarString, Text: "wiki"},
		model.VariableValue{Name: "antrag", Kind: model.VarJSON, Text: `{"titel":"Laptop","seite":"OPS-1","besitzer":"anna","text":"Bitte bestellen."}`},
	)
	j, err := Resolve(store, cp, d, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := Job{Connector: "wiki", Operation: "create-item", Space: "OPS", ItemType: "task", Title: "Laptop", Parent: "OPS-1",
		Properties: map[string]any{"assignee": "anna", "points": json.Number("3"), "due": nil, "source-id": "99"},
		MatchField: "source-id", Text: "Bitte bestellen.", ResultVariable: "aufgabe", ErrorCodes: []string{}}
	if !reflect.DeepEqual(j, want) {
		t.Errorf("job = %#v\nwant  %#v", j, want)
	}
}

func TestResolveAQuery(t *testing.T) {
	cp, d := compileTask(t, `connector="wiki" operation="query-items" query="type = task AND state != done" maxResults="20" resultVariable="offen"`)
	j, err := Resolve(vars(), cp, d, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := Job{Connector: "wiki", Operation: "query-items", Query: "type = task AND state != done", MaxResults: 20, ResultVariable: "offen", ErrorCodes: []string{}}
	if !reflect.DeepEqual(j, want) {
		t.Errorf("job = %#v\nwant  %#v", j, want)
	}
}

func TestResolveLabels(t *testing.T) {
	for attrs, want := range map[string]*[]string{
		`labels="onboarding, it"`: {"onboarding", "it"},
		`labels="=liste"`:         {"a", "2"},
		`labels="=text"`:          {"x", "y"},
		`labels="=leer"`:          {},
		`labels="=fehlt"`:         nil,
	} {
		cp, d := compileTask(t, `connector="wiki" operation="update-item" item="OPS-1" `+attrs)
		j, err := Resolve(vars(
			model.VariableValue{Name: "liste", Kind: model.VarJSON, Text: `["a",2]`},
			model.VariableValue{Name: "text", Kind: model.VarString, Text: "x, y"},
			model.VariableValue{Name: "leer", Kind: model.VarJSON, Text: `[]`},
		), cp, d, 10)
		if err != nil {
			t.Fatalf("%s: %v", attrs, err)
		}
		if !reflect.DeepEqual(j.Labels, want) {
			t.Errorf("%s: labels = %v, want %v", attrs, deref(j.Labels), deref(want))
		}
	}
}

func deref(p *[]string) any {
	if p == nil {
		return nil
	}
	return *p
}

func TestResolveBlocks(t *testing.T) {
	cp, d := compileTask(t, `connector="wiki" operation="append-to-page" item="OPS-1" blocks='[{"kind":"heading","level":2,"text":"Log"}]'`)
	j, err := Resolve(vars(), cp, d, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []any{map[string]any{"kind": "heading", "level": json.Number("2"), "text": "Log"}}
	if !reflect.DeepEqual(j.Blocks, want) {
		t.Errorf("blocks = %#v", j.Blocks)
	}
	cp, d = compileTask(t, `connector="wiki" operation="append-to-page" item="OPS-1" blocks="=[{kind: &quot;paragraph&quot;, text: schritt}]"`)
	j, err = Resolve(vars(model.VariableValue{Name: "schritt", Kind: model.VarString, Text: "Fertig."}), cp, d, 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []any{map[string]any{"kind": "paragraph", "text": "Fertig."}}; !reflect.DeepEqual(j.Blocks, want) {
		t.Errorf("blocks = %#v", j.Blocks)
	}
	// FEEL that is no list is refused when the job is resolved, naming what it was.
	cp, d = compileTask(t, `connector="wiki" operation="append-to-page" item="OPS-1" blocks="=schritt"`)
	if _, err := Resolve(vars(model.VariableValue{Name: "schritt", Kind: model.VarString, Text: "x"}), cp, d, 10); err == nil || !strings.Contains(err.Error(), "list") {
		t.Errorf("err = %v", err)
	}
	// FEEL that is null is no blocks, which the worker then reports.
	if j, err := Resolve(vars(), cp, d, 10); err != nil || j.Blocks != nil {
		t.Errorf("blocks = %v, err = %v", j.Blocks, err)
	}
}

func TestResolvePropertiesFromAVariable(t *testing.T) {
	cp, d := compileTask(t, `connector="wiki" operation="update-item" item="OPS-1" propertiesVariable="felder"`)
	j, err := Resolve(vars(model.VariableValue{Name: "felder", Kind: model.VarJSON, Text: `{"due":"2026-11-01","points":5}`}), cp, d, 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"due": "2026-11-01", "points": json.Number("5")}; !reflect.DeepEqual(j.Properties, want) {
		t.Errorf("properties = %#v", j.Properties)
	}
	for _, v := range []model.VariableValue{
		{Name: "felder", Kind: model.VarString, Text: "x"},
		{Name: "other", Kind: model.VarString, Text: "x"},
	} {
		if _, err := Resolve(vars(v), cp, d, 10); err == nil {
			t.Errorf("properties from %+v were accepted", v)
		}
	}
}

func TestResolveRefusals(t *testing.T) {
	cp, d := compileTask(t, `connector="=welches" operation="get-item" item="OPS-1" resultVariable="x"`)
	if _, err := Resolve(vars(), cp, d, 10); err == nil || !strings.Contains(err.Error(), "empty name") {
		t.Errorf("err = %v", err)
	}
	if _, err := Resolve(vars(), nil, nil, 10); err == nil {
		t.Error("a task without detail resolved")
	}
	cp, d = compileTask(t, `connector="wiki" operation="update-item" item="OPS-1" properties='{"a":"=b"}'`)
	if _, err := Resolve(vars(model.VariableValue{Name: "b", Kind: model.VarString, Text: "x"}), cp, d, 10); err != nil {
		t.Errorf("err = %v", err)
	}
}
