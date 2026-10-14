package conformance

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/model"
)

// parallelApprovals is a parallel multi-instance user task over two items: two
// tokens, and so two jobs, on one element.
const parallelApprovals = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
             id="defs_parallel_approvals" targetNamespace="http://atlas/conformance">
  <process id="parallel-approvals" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="approve">
      <multiInstanceLoopCharacteristics>
        <extensionElements>
          <zeebe:loopCharacteristics inputCollection="=[1, 2]" inputElement="item"/>
        </extensionElements>
      </multiInstanceLoopCharacteristics>
    </userTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="approve"/>
    <sequenceFlow id="f2" sourceRef="approve" targetRef="end"/>
  </process>
</definitions>`

// TestACompleteStepOnASharedElementIsAmbiguous: with two jobs on one element a
// Complete step cannot say which token it means, so the driver refuses rather than
// completing whichever it happened to find first — a scenario that passed that way
// would pass by accident.
func TestACompleteStepOnASharedElementIsAmbiguous(t *testing.T) {
	_, err := Run(t.TempDir(), []byte(parallelApprovals), "", Start{}, []Step{Complete("approve")})
	if err == nil || !strings.Contains(err.Error(), `2 activatable jobs for element "approve"; ambiguous`) {
		t.Fatalf("Run = %v, want the ambiguity named", err)
	}
}

// TestEffectCarriesDataObjects: two runs whose variables agree but whose data objects
// differ did not have the same effect, so the projection must keep the objects.
func TestEffectCarriesDataObjects(t *testing.T) {
	a := RunResult{State: model.PICompleted, Variables: map[string]string{"v": "1"}, DataObjects: []string{"order[approved]=100"}}
	b := RunResult{State: model.PICompleted, Variables: map[string]string{"v": "1"}, DataObjects: []string{"order[rejected]=100"}}
	if got := a.Effect(); got != "state="+model.PICompleted.String()+" v=1 do:order[approved]=100" {
		t.Errorf("Effect = %q, want the data object after the variables", got)
	}
	if a.Effect() == b.Effect() {
		t.Errorf("Effect did not tell two different data objects apart: %q", a.Effect())
	}
}
