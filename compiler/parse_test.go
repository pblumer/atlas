package compiler

import (
	"encoding/json"
	"strings"
	"testing"
)

const linearBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="order" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:serviceTask id="task" name="Charge">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="payment" retries="5"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="task"/>
    <bpmn:sequenceFlow id="f2" sourceRef="task" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseLinearProcess(t *testing.T) {
	cp, err := Parse(99, 1, strings.NewReader(linearBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cp.Key != 99 || cp.Version != 1 {
		t.Errorf("key/version = %d/%d, want 99/1", cp.Key, cp.Version)
	}
	if got := cp.Intern(cp.BpmnProcessId); got != "order" {
		t.Errorf("BpmnProcessId = %q, want \"order\"", got)
	}

	starts := cp.StartEvents()
	if len(starts) != 1 {
		t.Fatalf("start events = %d, want 1", len(starts))
	}
	start := starts[0]
	if cp.Node(start).Type != TypeStartEvent {
		t.Fatalf("start node type = %v", cp.Node(start).Type)
	}

	// start → task → end
	out := cp.Outgoing(start)
	if len(out) != 1 {
		t.Fatalf("start outgoing = %d, want 1", len(out))
	}
	task := cp.Flow(out[0]).Target
	if cp.Node(task).Type != TypeServiceTask {
		t.Fatalf("expected service task after start, got %v", cp.Node(task).Type)
	}
	detail := cp.ServiceTask(cp.Node(task).Detail)
	if cp.Intern(detail.JobType) != "payment" || detail.Retries != 5 {
		t.Errorf("task detail jobType=%q retries=%d, want payment/5", cp.Intern(detail.JobType), detail.Retries)
	}

	out = cp.Outgoing(task)
	if len(out) != 1 || cp.Node(cp.Flow(out[0]).Target).Type != TypeEndEvent {
		t.Errorf("expected end event after task")
	}
}

func TestParseDefaultRetries(t *testing.T) {
	const noRetries = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p">
    <startEvent id="s"/>
    <serviceTask id="t">
      <extensionElements><taskDefinition type="work"/></extensionElements>
    </serviceTask>
    <sequenceFlow id="f" sourceRef="s" targetRef="t"/>
  </process>
</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(noRetries))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// the service task is element 1 (after the start event)
	var task int32 = -1
	for _, fid := range cp.Outgoing(cp.StartEvents()[0]) {
		task = cp.Flow(fid).Target
	}
	if detail := cp.ServiceTask(cp.Node(task).Detail); detail.Retries != defaultRetries {
		t.Errorf("retries = %d, want default %d", detail.Retries, defaultRetries)
	}
}

const businessRuleBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="dinner" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:businessRuleTask id="decide" name="Pick dish">
      <bpmn:extensionElements>
        <zeebe:calledDecision decisionId="Dish" retries="5"/>
        <atlas:decisionInput name="Season" value="Winter"/>
        <atlas:decisionInput name="Guests" value="8"/>
      </bpmn:extensionElements>
    </bpmn:businessRuleTask>
    <bpmn:endEvent id="end"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="decide"/>
    <bpmn:sequenceFlow id="f2" sourceRef="decide" targetRef="end"/>
  </bpmn:process>
</bpmn:definitions>`

func TestParseBusinessRuleTask(t *testing.T) {
	cp, err := Parse(7, 1, strings.NewReader(businessRuleBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	if cp.Node(task).Type != TypeBusinessRuleTask {
		t.Fatalf("expected business rule task after start, got %v", cp.Node(task).Type)
	}
	detail := cp.BusinessRuleTask(cp.Node(task).Detail)
	if got := cp.Intern(detail.DecisionId); got != "Dish" {
		t.Errorf("decisionId = %q, want Dish", got)
	}
	if cp.Intern(detail.JobType) != DMNJobType {
		t.Errorf("jobType = %q, want %q", cp.Intern(detail.JobType), DMNJobType)
	}
	if detail.Retries != 5 {
		t.Errorf("retries = %d, want 5", detail.Retries)
	}
	// Inputs are stored as a JSON object; the string value stays a string and the
	// numeric value keeps its JSON number type.
	var inputs map[string]any
	if err := json.Unmarshal([]byte(cp.Intern(detail.Inputs)), &inputs); err != nil {
		t.Fatalf("inputs not valid JSON: %v", err)
	}
	if inputs["Season"] != "Winter" {
		t.Errorf("Season = %#v, want \"Winter\"", inputs["Season"])
	}
	if inputs["Guests"] != float64(8) {
		t.Errorf("Guests = %#v, want 8", inputs["Guests"])
	}
	if out := cp.Outgoing(task); len(out) != 1 || cp.Node(cp.Flow(out[0]).Target).Type != TypeEndEvent {
		t.Errorf("expected end event after business rule task")
	}
}

func TestParseBusinessRuleTaskDefaults(t *testing.T) {
	const noRetries = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p">
    <startEvent id="s"/>
    <businessRuleTask id="t"><extensionElements><calledDecision decisionId="D"/></extensionElements></businessRuleTask>
    <sequenceFlow id="f" sourceRef="s" targetRef="t"/>
  </process>
</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(noRetries))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	detail := cp.BusinessRuleTask(cp.Node(task).Detail)
	if detail.Retries != defaultRetries {
		t.Errorf("retries = %d, want default %d", detail.Retries, defaultRetries)
	}
	if detail.Inputs != -1 {
		t.Errorf("Inputs index = %d, want -1 (no inputs)", detail.Inputs)
	}
}

func TestParseBusinessRuleTaskWithoutDecision(t *testing.T) {
	const missing = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="p"><startEvent id="s"/><businessRuleTask id="t"/></process></definitions>`
	if _, err := Parse(1, 1, strings.NewReader(missing)); err == nil {
		t.Fatal("Parse of business rule task without decisionId = nil error, want error")
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name string
		xml  string
	}{
		{
			name: "no process",
			xml:  `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"></definitions>`,
		},
		{
			name: "no start event",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
				<endEvent id="e"/></process></definitions>`,
		},
		{
			name: "service task without type",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
				<startEvent id="s"/><serviceTask id="t"/></process></definitions>`,
		},
		{
			name: "dangling flow",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
				<startEvent id="s"/><sequenceFlow id="f" sourceRef="s" targetRef="missing"/></process></definitions>`,
		},
		{
			name: "duplicate id",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
				<startEvent id="s"/><endEvent id="s"/></process></definitions>`,
		},
		{
			name: "unsupported user task",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
				<startEvent id="s"/><userTask id="t"/><endEvent id="e"/>
				<sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
				<sequenceFlow id="f2" sourceRef="t" targetRef="e"/></process></definitions>`,
		},
		{
			name: "unsupported receive task",
			xml: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
				<startEvent id="s"/><receiveTask id="g"/></process></definitions>`,
		},
		{
			name: "malformed xml",
			xml:  `<definitions><process`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Parse(1, 1, strings.NewReader(tt.xml)); err == nil {
				t.Errorf("Parse(%s) = nil error, want error", tt.name)
			}
		})
	}
}

// TestParseScriptTask compiles a Zeebe script task and checks its detail: a
// compiled FEEL expression and a result variable.
func TestParseScriptTask(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <scriptTask id="calc">
	      <extensionElements><zeebe:script expression="= 6 * 7" resultVariable="answer"/></extensionElements>
	    </scriptTask>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="calc"/>
	    <sequenceFlow id="f2" sourceRef="calc" targetRef="e"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// Registration order: start(0), scriptTask(1), end(2).
	node := cp.Node(1)
	if node.Type != TypeScriptTask {
		t.Fatalf("node 1 type = %v, want ScriptTask", node.Type)
	}
	detail := cp.ScriptTask(node.Detail)
	if detail.ResultVar != "answer" {
		t.Errorf("result var = %q, want answer", detail.ResultVar)
	}
	if detail.Expr == nil {
		t.Error("script task has no compiled expression")
	}
}

// TestParseScriptTaskRejectsBadExpression fails deploy when the FEEL is invalid.
func TestParseScriptTaskRejectsBadExpression(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
	  <process id="p"><startEvent id="s"/>
	    <scriptTask id="calc"><extensionElements>
	      <zeebe:script expression="= 6 * " resultVariable="answer"/></extensionElements>
	    </scriptTask></process></definitions>`
	if _, err := Parse(1, 1, strings.NewReader(xml)); err == nil {
		t.Fatal("want a compile error for a malformed FEEL expression")
	}
}

// TestParseExclusiveGateway checks a gateway parses with a conditional flow and a
// marked default flow.
func TestParseExclusiveGateway(t *testing.T) {
	const gwBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="router" isExecutable="true">
	    <startEvent id="s"/>
	    <exclusiveGateway id="gw" default="toLow"/>
	    <endEvent id="high"/>
	    <endEvent id="low"/>
	    <sequenceFlow id="s2gw" sourceRef="s" targetRef="gw"/>
	    <sequenceFlow id="toHigh" sourceRef="gw" targetRef="high">
	      <conditionExpression>= amount &gt; 100</conditionExpression>
	    </sequenceFlow>
	    <sequenceFlow id="toLow" sourceRef="gw" targetRef="low"/>
	  </process>
	</definitions>`
	cp, err := Parse(1, 1, strings.NewReader(gwBPMN))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	// start(0) → gateway(1)
	gw := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	if cp.Node(gw).Type != TypeExclusiveGateway {
		t.Fatalf("expected exclusive gateway, got %v", cp.Node(gw).Type)
	}
	var conditional, deflt int
	for _, fid := range cp.Outgoing(gw) {
		f := cp.Flow(fid)
		if f.Condition != nil {
			conditional++
		}
		if f.Default {
			deflt++
		}
	}
	if conditional != 1 {
		t.Errorf("conditional flows = %d, want 1", conditional)
	}
	if deflt != 1 {
		t.Errorf("default flows = %d, want 1", deflt)
	}
}

// TestParseExclusiveGatewayBadCondition fails deploy on invalid FEEL in a guard.
func TestParseExclusiveGatewayBadCondition(t *testing.T) {
	const bad = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p"><startEvent id="s"/><exclusiveGateway id="gw"/><endEvent id="e"/>
	    <sequenceFlow id="a" sourceRef="s" targetRef="gw"/>
	    <sequenceFlow id="b" sourceRef="gw" targetRef="e"><conditionExpression>= amount &gt;</conditionExpression></sequenceFlow>
	  </process></definitions>`
	if _, err := Parse(1, 1, strings.NewReader(bad)); err == nil {
		t.Fatal("want a compile error for a malformed flow condition")
	}
}

// TestParseTimerCatchEvent parses an intermediate timer catch event.
func TestParseTimerCatchEvent(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
	  <process id="p" isExecutable="true">
	    <startEvent id="s"/>
	    <intermediateCatchEvent id="wait">
	      <timerEventDefinition><timeDuration>PT30S</timeDuration></timerEventDefinition>
	    </intermediateCatchEvent>
	    <endEvent id="e"/>
	    <sequenceFlow id="f1" sourceRef="s" targetRef="wait"/>
	    <sequenceFlow id="f2" sourceRef="wait" targetRef="e"/>
	  </process></definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	if cp.Node(node).Type != TypeTimerCatchEvent {
		t.Fatalf("node type = %v, want TimerCatchEvent", cp.Node(node).Type)
	}
	if d := cp.TimerCatch(cp.Node(node).Detail); d.DurationNanos != 30e9 {
		t.Errorf("duration = %d, want %d", d.DurationNanos, int64(30e9))
	}
}

// TestParseTimerCatchEventErrors rejects a non-timer catch event and a bad duration.
func TestParseTimerCatchEventErrors(t *testing.T) {
	for _, xml := range []string{
		`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
			<startEvent id="s"/><intermediateCatchEvent id="w"/></process></definitions>`,
		`<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
			<startEvent id="s"/><intermediateCatchEvent id="w"><timerEventDefinition><timeDuration>soon</timeDuration></timerEventDefinition></intermediateCatchEvent></process></definitions>`,
	} {
		if _, err := Parse(1, 1, strings.NewReader(xml)); err == nil {
			t.Errorf("want error for %.60s", xml)
		}
	}
}

// TestParseUnsupportedElementMessage locks in the actionable error text for an
// unsupported element (a user task) rather than a confusing "unknown targetRef".
func TestParseUnsupportedElementMessage(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
		<startEvent id="s"/><userTask id="Activity_1"/><endEvent id="e"/>
		<sequenceFlow id="f1" sourceRef="s" targetRef="Activity_1"/>
		<sequenceFlow id="f2" sourceRef="Activity_1" targetRef="e"/></process></definitions>`
	_, err := Parse(1, 1, strings.NewReader(xml))
	if err == nil {
		t.Fatal("want error for a <userTask>")
	}
	for _, want := range []string{"Activity_1", "userTask", "service"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err.Error(), want)
		}
	}
}

// TestParseParallelGateway parses a fork/join and checks the join's incoming
// count (what a parallel join waits on) is compiled correctly.
func TestParseParallelGateway(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
		<startEvent id="s"/>
		<parallelGateway id="fork"/>
		<task id="a"/><task id="b"/>
		<parallelGateway id="join"/>
		<endEvent id="e"/>
		<sequenceFlow id="f0" sourceRef="s" targetRef="fork"/>
		<sequenceFlow id="f1" sourceRef="fork" targetRef="a"/>
		<sequenceFlow id="f2" sourceRef="fork" targetRef="b"/>
		<sequenceFlow id="f3" sourceRef="a" targetRef="join"/>
		<sequenceFlow id="f4" sourceRef="b" targetRef="join"/>
		<sequenceFlow id="f5" sourceRef="join" targetRef="e"/></process></definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	fork := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	if cp.Node(fork).Type != TypeParallelGateway {
		t.Fatalf("node after start = %v, want ParallelGateway", cp.Node(fork).Type)
	}
	if got := cp.Node(fork).OutgoingCount; got != 2 {
		t.Errorf("fork OutgoingCount = %d, want 2", got)
	}
	// The join is the fork's outgoing tasks' shared target; it has two incoming.
	join := cp.Flow(cp.Outgoing(cp.Flow(cp.Outgoing(fork)[0]).Target)[0]).Target
	if cp.Node(join).Type != TypeParallelGateway || cp.Node(join).IncomingCount != 2 {
		t.Errorf("join type=%v incoming=%d, want ParallelGateway and 2", cp.Node(join).Type, cp.Node(join).IncomingCount)
	}
}

// TestNodesReaching checks the reverse-reachability an inclusive join relies on:
// a node's ancestors (nodes from which it is reachable) — and nothing downstream.
func TestNodesReaching(t *testing.T) {
	b := NewBuilder(1, "reach", 1)
	s := b.AddStartEvent()
	g := b.AddInclusiveGateway()
	a := b.AddTask()
	bb := b.AddTask()
	j := b.AddInclusiveGateway()
	e := b.AddEndEvent()
	b.Connect(s, g)
	b.Connect(g, a)
	b.Connect(g, bb)
	b.Connect(a, j)
	b.Connect(bb, j)
	b.Connect(j, e)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	reach := cp.NodesReaching(j)
	for _, anc := range []int32{s, g, a, bb} {
		if !reach[anc] {
			t.Errorf("NodesReaching(join) missing ancestor %d", anc)
		}
	}
	if reach[e] {
		t.Errorf("NodesReaching(join) includes the downstream end event %d", e)
	}
	if reach[j] {
		t.Errorf("NodesReaching(join) includes the join itself (no cycle)")
	}
	// A start event has no ancestors.
	if len(cp.NodesReaching(s)) != 0 {
		t.Errorf("NodesReaching(start) = %v, want empty", cp.NodesReaching(s))
	}
}

// TestParseInclusiveGateway parses an inclusive split with a default flow and
// checks the node type, incoming count on the join, and default marking.
func TestParseInclusiveGateway(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
		<startEvent id="s"/>
		<inclusiveGateway id="split" default="fdef"/>
		<task id="a"/><task id="b"/>
		<inclusiveGateway id="join"/>
		<endEvent id="e"/>
		<sequenceFlow id="f0" sourceRef="s" targetRef="split"/>
		<sequenceFlow id="fa" sourceRef="split" targetRef="a"><conditionExpression>= x &gt; 0</conditionExpression></sequenceFlow>
		<sequenceFlow id="fdef" sourceRef="split" targetRef="b"/>
		<sequenceFlow id="f3" sourceRef="a" targetRef="join"/>
		<sequenceFlow id="f4" sourceRef="b" targetRef="join"/>
		<sequenceFlow id="f5" sourceRef="join" targetRef="e"/></process></definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	split := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	if cp.Node(split).Type != TypeInclusiveGateway {
		t.Fatalf("node after start = %v, want InclusiveGateway", cp.Node(split).Type)
	}
	// The default flow (fdef) is marked; the conditional one (fa) is not.
	var sawDefault, sawCond bool
	for _, fid := range cp.Outgoing(split) {
		f := cp.Flow(fid)
		if f.Default {
			sawDefault = true
		}
		if f.Condition != nil {
			sawCond = true
		}
	}
	if !sawDefault || !sawCond {
		t.Errorf("split flows: default=%v cond=%v, want both true", sawDefault, sawCond)
	}
	join := cp.Flow(cp.Outgoing(cp.Flow(cp.Outgoing(split)[0]).Target)[0]).Target
	if cp.Node(join).Type != TypeInclusiveGateway || cp.Node(join).IncomingCount != 2 {
		t.Errorf("join type=%v incoming=%d, want InclusiveGateway and 2", cp.Node(join).Type, cp.Node(join).IncomingCount)
	}
}

// TestParsePlainTaskPassThrough confirms an undefined <task> and a <manualTask>
// now compile (as pass-through nodes) rather than being rejected.
func TestParsePlainTaskPassThrough(t *testing.T) {
	const xml = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p">
		<startEvent id="s"/><task id="t1"/><manualTask id="t2"/><endEvent id="e"/>
		<sequenceFlow id="f1" sourceRef="s" targetRef="t1"/>
		<sequenceFlow id="f2" sourceRef="t1" targetRef="t2"/>
		<sequenceFlow id="f3" sourceRef="t2" targetRef="e"/></process></definitions>`
	cp, err := Parse(1, 1, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	if cp.Node(task).Type != TypeTask {
		t.Fatalf("node after start = %v, want Task (pass-through)", cp.Node(task).Type)
	}
}
