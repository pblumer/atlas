package sharepointserver

import (
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

func TestResolveRefusesATaskWithNoDetail(t *testing.T) {
	if _, err := Resolve(nil, nil, nil, 1); err == nil {
		t.Error("no detail must fail")
	}
}

func TestBindVarsAndKinds(t *testing.T) {
	vars := map[string]model.VariableValue{
		"a": {Kind: model.VarString, Text: "x"},
		"b": {Kind: model.VarBool, Bool: true},
		"n": {Kind: model.VarNumber, Text: "3"},
		"j": {Kind: model.VarJSON, Text: "{}"},
		"z": {Kind: model.VarNull},
	}
	if bindVars(1, vars, nil) != nil {
		t.Error("no names binds nothing")
	}
	got := bindVars(7, vars, []string{"a", "b", "n", "j", "z", "missing", "processInstanceKey"})
	if len(got) != 6 {
		t.Errorf("bound = %d", len(got))
	}
	_ = compiler.SharePointServerJobTypeIndex
}

type fakeStore struct {
	vars map[string]model.VariableValue
	err  error
}

func (s fakeStore) VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error {
	if s.err != nil {
		return s.err
	}
	for name, v := range s.vars {
		v := v
		v.Name = name
		if err := fn(&v); err != nil {
			return err
		}
	}
	return nil
}

func (s fakeStore) GetElementInstance(uint64) (*model.ElementInstanceValue, bool, error) {
	return nil, false, nil
}

func compiled(t *testing.T, attrs string) (*compiler.CompiledProcess, *compiler.ConnectorTaskDetail) {
	t.Helper()
	src := `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0" id="d">
  <bpmn:process id="p" isExecutable="true"><bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:sharepointServerConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/><bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process></bpmn:definitions>`
	cp, err := compiler.Parse(1, 1, strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	node := cp.Node(cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target)
	return cp, cp.ConnectorTask(node.Detail)
}

func TestResolveEvaluatesTheParameters(t *testing.T) {
	cp, d := compiled(t, `connector="farm" operation="get-site" parameters='{"Site":"=alias"}' resultVariable="s"`)
	j, err := Resolve(fakeStore{vars: map[string]model.VariableValue{"alias": {Kind: model.VarString, Text: "alpha"}}}, cp, d, 1)
	if err != nil {
		t.Fatal(err)
	}
	if j.Connector != "farm" || j.Operation != "get-site" || j.Parameters["Site"] != "alpha" || j.ResultVariable != "s" {
		t.Errorf("job = %+v", j)
	}
	if _, err := Resolve(fakeStore{err: errors.New("nope")}, cp, d, 1); err == nil {
		t.Error("a store failure is reported")
	}
	cp2, d2 := compiled(t, `connector="farm" operation="list-sites" resultVariable="l"`)
	if j, err := Resolve(fakeStore{}, cp2, d2, 1); err != nil || j.Parameters != nil {
		t.Errorf("no parameters: %+v %v", j, err)
	}
}

func TestRefusalReadsAsItsMessage(t *testing.T) {
	if (&Refusal{Code: "c", Message: "m"}).Error() != "m" {
		t.Error("message")
	}
}
