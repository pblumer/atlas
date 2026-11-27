package agent

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// oneScope is a store holding one element instance under one process instance.
type oneScope struct{ vars []model.VariableValue }

func (s oneScope) VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error {
	if scope != 1 {
		return nil
	}
	for i := range s.vars {
		if err := fn(&s.vars[i]); err != nil {
			return err
		}
	}
	return nil
}

func (s oneScope) GetElementInstance(key uint64) (*model.ElementInstanceValue, bool, error) {
	if key != 10 {
		return nil, false, nil
	}
	return &model.ElementInstanceValue{ProcessInstanceKey: 1, FlowScopeKey: 1}, true, nil
}

const answerFieldsXML = `<atlas:agentOutput name="entscheid" type="string" required="true" values="ja, nein" description="Ob es tragbar ist"/>
        <atlas:agentOutput name="quote" type="number"/>`

func compiledHost(t *testing.T, body string) (*compiler.CompiledProcess, int32) {
	t.Helper()
	cp, err := compiler.Parse(1, 1, strings.NewReader(`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:atlas="http://atlas/schema/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/><endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="host"/><sequenceFlow id="f2" sourceRef="host" targetRef="e"/>
	    `+body+`
	  </process>
	</definitions>`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for id := int32(0); id < int32(cp.NodeCount()); id++ {
		if cp.ElementBpmnId(id) == "host" {
			return cp, id
		}
	}
	t.Fatal("the host element is not in the compiled process")
	return nil, 0
}

func checkVerdictFields(t *testing.T, outs []Output) {
	t.Helper()
	if len(outs) != 2 || outs[0].Name != "entscheid" || outs[0].Type != "string" || !outs[0].Required ||
		outs[0].Description != "Ob es tragbar ist" || len(outs[0].Values) != 2 || outs[0].Values[1] != "nein" ||
		outs[1].Name != "quote" || outs[1].Type != "number" || outs[1].Required || outs[1].Description != "" || outs[1].Values != nil {
		t.Errorf("outputs = %+v", outs)
	}
}

// The fields a container's answer must carry, its bound on one call and what it may read
// are resolved from the compiled process into the round a worker is handed
// (ADR-draft-agent-transcript-and-answer, ADR-draft-agent-run-record).
func TestAContainersAnswerFieldsAndBoundsAreResolved(t *testing.T) {
	cp, id := compiledHost(t, `<adHocSubProcess id="host">
	  <documentation>Prüfe die Tragbarkeit.</documentation>
	  <extensionElements>
	    <atlas:agentConnector connector="anthropic_pb" model="claude-opus-5-5" context="dossier" timeout="PT90S"/>
	    `+answerFieldsXML+`
	  </extensionElements>
	  <serviceTask id="rechnen"><documentation>Rechnet.</documentation>
	    <extensionElements><zeebe:taskDefinition type="calc"/></extensionElements></serviceTask>
	</adHocSubProcess>`)
	store := oneScope{vars: []model.VariableValue{{Name: "dossier", Kind: model.VarString, Text: "D-7"}}}
	r, err := Resolve(store, cp, &model.ElementInstanceValue{ProcessInstanceKey: 1, ElementId: id}, 10)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	checkVerdictFields(t, r.Outputs)
	if r.Timeout.Seconds() != 90 || r.Connector != "anthropic_pb" || r.Model != "claude-opus-5-5" || r.Context["dossier"] != "D-7" ||
		len(r.Tools) != 1 || r.Goal != "Prüfe die Tragbarkeit." {
		t.Errorf("round = %+v", r)
	}
	// What travels states them as the model is told them.
	back, err := RoundFromPayload(ResolveJobPayload(r))
	if err != nil {
		t.Fatalf("RoundFromPayload: %v", err)
	}
	checkVerdictFields(t, back.Outputs)
}

// And an ai task's, with the bound on its one call.
func TestAnAiTasksAnswerFieldsAndTimeoutAreResolved(t *testing.T) {
	cp, id := compiledHost(t, `<serviceTask id="host">
	  <extensionElements>
	    <atlas:agentConnector connector="anthropic_pb" prompt="Ist das tragbar?" resultVariable="urteil" timeout="PT20S"/>
	    `+answerFieldsXML+`
	  </extensionElements>
	</serviceTask>`)
	task, err := ResolveTask(oneScope{}, cp, cp.ConnectorTask(cp.Node(id).Detail),
		&model.ElementInstanceValue{ProcessInstanceKey: 1, ElementId: id}, 10, 77)
	if err != nil {
		t.Fatalf("ResolveTask: %v", err)
	}
	checkVerdictFields(t, task.Outputs)
	if task.TimeoutMs != 20000 || task.ResultVariable != "urteil" || task.RequestID != "77" {
		t.Errorf("task = %+v", task)
	}
	req := TaskRequest(task)
	if len(req.Outputs) != 2 || req.Timeout.Seconds() != 20 {
		t.Errorf("request = %+v", req)
	}
	if _, err := ResolveTask(oneScope{}, cp, nil, &model.ElementInstanceValue{}, 10, 1); err == nil {
		t.Error("a task with no detail was resolved")
	}
}
