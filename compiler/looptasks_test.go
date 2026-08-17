package compiler

import (
	"strings"
	"testing"
)

// loopTaskXML wraps activity markup in a minimal executable process whose single
// activity is "work".
func loopTaskXML(activity string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	        xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    ` + activity + `
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="work"/>
	    <sequenceFlow id="f2" sourceRef="work" targetRef="e"/>
	  </process>
	</definitions>`
}

// TestParseLoopOnRemainingActivities checks that the two loop markers are honoured on
// the activity kinds that used to ignore them — business rule tasks, manual tasks, and
// undefined tasks (ADR-0133, amended). Before this they were the last place where a
// marker drawn on the diagram was silently dropped and the activity ran once.
func TestParseLoopOnRemainingActivities(t *testing.T) {
	loop := `<standardLoopCharacteristics><loopCondition>=again</loopCondition></standardLoopCharacteristics>`
	mi := `<multiInstanceLoopCharacteristics isSequential="true"><extensionElements>
	         <zeebe:loopCharacteristics inputCollection="=items" inputElement="item"/>
	       </extensionElements></multiInstanceLoopCharacteristics>`
	brt := func(marker string) string {
		return `<businessRuleTask id="work"><extensionElements>
		          <zeebe:calledDecision decisionId="d" resultVariable="r"/>
		        </extensionElements>` + marker + `</businessRuleTask>`
	}
	cases := []struct {
		name     string
		activity string
		standard bool // the marker is a standard loop rather than a multi-instance
	}{
		{"business rule task, standard loop", brt(loop), true},
		{"business rule task, multi-instance", brt(mi), false},
		{"manual task, standard loop", `<manualTask id="work">` + loop + `</manualTask>`, true},
		{"manual task, multi-instance", `<manualTask id="work">` + mi + `</manualTask>`, false},
		{"undefined task, standard loop", `<task id="work">` + loop + `</task>`, true},
		{"undefined task, multi-instance", `<task id="work">` + mi + `</task>`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cp, err := Parse(1, 1, strings.NewReader(loopTaskXML(tc.activity)))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			node := nodeByBpmnId(t, cp, "work")
			if node.MultiInstance < 0 {
				t.Fatalf("MultiInstance = %d, want a loop detail index (>= 0) — the marker must not be dropped", node.MultiInstance)
			}
			d := cp.MultiInstance(node.MultiInstance)
			if d.Standard != tc.standard {
				t.Errorf("Standard = %v, want %v", d.Standard, tc.standard)
			}
			if tc.standard {
				if d.LoopCondition == nil {
					t.Errorf("LoopCondition = nil, want a compiled expression")
				}
				return
			}
			if !d.Sequential || d.InputCollection == nil {
				t.Errorf("Sequential/InputCollection = %v/%v, want true and a compiled collection", d.Sequential, d.InputCollection)
			}
		})
	}
}

// TestParseLoopOnRemainingActivitiesRefusals checks that these activities go through
// the same deploy-time gate as every other looping activity: no unbounded standard
// loop, and never both markers at once.
func TestParseLoopOnRemainingActivitiesRefusals(t *testing.T) {
	cases := map[string]string{
		"manual task, unbounded standard loop": `<manualTask id="work"><standardLoopCharacteristics/></manualTask>`,
		"undefined task, both markers": `<task id="work">
			<standardLoopCharacteristics><loopCondition>=again</loopCondition></standardLoopCharacteristics>
			<multiInstanceLoopCharacteristics><loopCardinality>2</loopCardinality></multiInstanceLoopCharacteristics></task>`,
		"business rule task, multi-instance with no source": `<businessRuleTask id="work"><extensionElements>
			<zeebe:calledDecision decisionId="d" resultVariable="r"/></extensionElements>
			<multiInstanceLoopCharacteristics/></businessRuleTask>`,
	}
	for name, activity := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(1, 1, strings.NewReader(loopTaskXML(activity))); err == nil {
				t.Fatalf("Parse: err = nil, want a refusal for %s", name)
			}
		})
	}
}

// TestParseLoopOnGatewayIsIgnored pins the boundary of the change: the loop fields ride
// on the task structs, not on the shared node shape, so a (nonsensical) loop marker on a
// gateway stays unparsed and can never turn a gateway into a loop body.
func TestParseLoopOnGatewayIsIgnored(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <parallelGateway id="gw">
	      <standardLoopCharacteristics><loopCondition>=again</loopCondition></standardLoopCharacteristics>
	    </parallelGateway>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="gw"/>
	    <sequenceFlow id="f2" sourceRef="gw" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := nodeByBpmnId(t, cp, "gw").MultiInstance; got != -1 {
		t.Errorf("gateway MultiInstance = %d, want -1 (a gateway is not an activity and cannot loop)", got)
	}
}
