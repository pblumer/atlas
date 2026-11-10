package conformance

import (
	"testing"

	"github.com/pblumer/atlas/model"
)

// TestCON2InclusiveJoinWhoseUpstreamTokenEndsElsewhere characterises a known gap in
// registry row FLOW-05. OMG BPMN 2.0.2 Table 13.3 states the inclusive join's
// activation as a condition on the tokens in the diagram, not as something an
// arrival triggers: once no token can reach an empty incoming flow, the join fires.
//
// Here the split opens both branches. Branch a reaches the join and parks while
// branch b is still at its task. Completing b routes it to an end event, not to the
// join, so no token can reach the join any more and, by the specification, the join
// must fire and the instance reach "end". Atlas re-checks a join only when a token
// arrives at it, so the parked token stays there and the instance stays active.
//
// ADR-0551 depends on fixing this, because the complex
// gateway's reset uses the same rule. When the engine re-checks a parked join after
// a token leaves its upstream, this test fails by design: turn it into an execution
// oracle that expects completion and update FLOW-05.
func TestCON2InclusiveJoinWhoseUpstreamTokenEndsElsewhere(t *testing.T) {
	S, F, W := con2Script, con2Flow, con2Wait
	xml := con2Defs("", `<startEvent id="s"/><inclusiveGateway id="split"/>`+S("a")+W("b")+
		`<exclusiveGateway id="route" default="toEnd"/><endEvent id="elsewhere"/>`+
		`<inclusiveGateway id="join"/>`+S("after")+`<endEvent id="end"/>`+
		F("f0", "s", "split")+F("f1", "split", "a")+F("f2", "split", "b")+
		F("f3", "a", "join")+F("f4", "b", "route")+F("toEnd", "route", "elsewhere")+
		`<sequenceFlow id="toJoin" sourceRef="route" targetRef="join"><conditionExpression>= false</conditionExpression></sequenceFlow>`+
		F("f5", "join", "after")+F("f6", "after", "end"))
	got := con2Run(t, xml, Complete("b"))
	if got.State == model.PICompleted {
		t.Fatalf("the join now fires once its upstream token ends elsewhere: turn this into an execution oracle and update FLOW-05; path=%v", got.Path)
	}
	if got.State != model.PIActive {
		t.Fatalf("state %v, want active (the known gap) or completed (fixed)", got.State)
	}
	con2Expect(t, "FLOW-05 §13.4", got, map[string]int{"join": 1, "elsewhere": 1, "after": 0, "end": 0})
}
