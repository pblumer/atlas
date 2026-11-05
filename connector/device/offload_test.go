package device

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

type fakeVarStore struct {
	vars map[uint64][]model.VariableValue
	fail error
}

func (f fakeVarStore) VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error {
	if f.fail != nil {
		return f.fail
	}
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
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:deviceConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
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

func scope(vs ...model.VariableValue) fakeVarStore {
	return fakeVarStore{vars: map[uint64][]model.VariableValue{10: vs}}
}

func TestResolveKeepsEachValuesType(t *testing.T) {
	cp, d := compileTask(t, `connector="pi" operation="digital.write" output="relais" on="true"`)
	j, err := Resolve(scope(), cp, d, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := Job{Connector: "pi", Operation: "digital.write", Fields: map[string]any{"output": "relais", "on": true}}
	if !reflect.DeepEqual(j, want) {
		t.Fatalf("job = %#v\nwant  %#v", j, want)
	}

	cp, d = compileTask(t, `connector="pi" operation="pulse" output="tuer" durationMs="1500"`)
	j, _ = Resolve(scope(), cp, d, 10)
	if j.Fields["durationMs"] != json.Number("1500") {
		t.Fatalf("a literal duration is a number: %#v", j.Fields["durationMs"])
	}
}

func TestResolveEvaluatesFEEL(t *testing.T) {
	cp, d := compileTask(t, `connector="=standort.geraet" operation="pulse" output="=standort.ausgang" on="=dauer > 100" durationMs="=dauer * 2"`)
	store := scope(
		model.VariableValue{Name: "standort", Kind: model.VarJSON, Text: `{"geraet":"pi-nord","ausgang":"tuer"}`},
		model.VariableValue{Name: "dauer", Kind: model.VarNumber, Text: "400"},
	)
	j, err := Resolve(store, cp, d, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := Job{Connector: "pi-nord", Operation: "pulse", Fields: map[string]any{"output": "tuer", "on": true, "durationMs": json.Number("800")}}
	if !reflect.DeepEqual(j, want) {
		t.Fatalf("job = %#v\nwant  %#v", j, want)
	}
}

func TestResolveLeavesANullOut(t *testing.T) {
	cp, d := compileTask(t, `connector="pi" operation="digital.read" line="=welche" resultVariable="zustand"`)
	j, err := Resolve(scope(), cp, d, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, set := j.Fields["line"]; set || j.ResultVariable != "zustand" {
		t.Fatalf("an unset variable is no value: %#v", j)
	}
	cp, d = compileTask(t, `connector="pi" operation="digital.read" line="=string(processInstanceKey)" resultVariable="zustand"`)
	j, _ = Resolve(scope(), cp, d, 10)
	if j.Fields["line"] != "99" {
		t.Fatalf("processInstanceKey binds to the instance: %#v", j.Fields["line"])
	}
}

func TestResolveRefusals(t *testing.T) {
	cp, d := compileTask(t, `connector="=geraet" operation="digital.write" output="relais" on="true"`)
	if _, err := Resolve(scope(), cp, d, 10); err == nil || !strings.Contains(err.Error(), "no name") {
		t.Fatalf("a worker expression with no value: %v", err)
	}
	if _, err := Resolve(scope(), nil, d, 10); err == nil {
		t.Fatal("a task without its process resolved")
	}
	broken := fakeVarStore{fail: errors.New("disk")}
	if _, err := Resolve(broken, cp, d, 10); err == nil || !strings.Contains(err.Error(), "read variables") {
		t.Fatalf("a store that fails: %v", err)
	}
}

func TestRunExecutesOnTheNamedDevice(t *testing.T) {
	d, chip, _ := openTest(t)
	devices := map[string]*Device{"pi": d}
	out, err := Run(context.Background(), Job{Connector: "pi", Operation: "digital.write", Fields: map[string]any{"output": "lampe", "on": true}}, devices, 1)
	if err != nil || out != nil || !chip.Level(23) {
		t.Fatalf("write: %v, %v", out, err)
	}
	out, err = Run(context.Background(), Job{Connector: "pi", Operation: "digital.read", Fields: map[string]any{"line": "lampe"}, ResultVariable: "zustand"}, devices, 2)
	if err != nil || !reflect.DeepEqual(out, map[string]any{"zustand": map[string]any{"on": true}}) {
		t.Fatalf("read: %v, %v", out, err)
	}
	if _, err := Run(context.Background(), Job{Connector: "pi-sued", Operation: "digital.write"}, devices, 3); !IsRefused(err) {
		t.Fatalf("a device this worker does not hold: %v", err)
	}
	if _, err := Run(context.Background(), Job{Connector: "pi", Operation: "digital.toggle"}, devices, 4); !IsRefused(err) {
		t.Fatalf("an operation the device does not know: %v", err)
	}
}
