// Package scenarios defines the process catalogue the performance suite runs.
//
// Each scenario targets one BPMN feature (service task, exclusive gateway,
// parallel gateway, script, and a realistic mix) so a number can be traced back
// to the feature it measures — a single linear "hello world" would say nothing
// about an engine whose value is in gateways, expressions, and recovery.
//
// A scenario carries two structurally-equivalent representations of the same
// process so both measurement levels exercise the same work:
//
//   - Build builds a *compiler.CompiledProcess programmatically. The in-process
//     benchmarks (level 1) use it and the returned job types to drive instances
//     to completion deterministically, with no network in the measurement.
//   - BPMN is the equivalent BPMN 2.0 XML. The end-to-end load test (level 2)
//     deploys it over the real HTTP API, so the client-visible path (transport,
//     serialization) is included.
//
// scenarios_test.go pins the two representations to the same terminal behavior,
// so they cannot silently drift apart.
package scenarios

import (
	"fmt"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// Scenario is one process in the catalogue, in both its programmatic and its
// BPMN-XML form, plus the inputs needed to run one instance to completion.
type Scenario struct {
	Name        string // stable id, used in benchmark names and the baseline
	Feature     string // the BPMN feature this scenario isolates
	Description string // one line explaining what it measures

	// BPMN is the BPMN 2.0 XML deployed by the end-to-end load test.
	BPMN string

	// StartVars are the process variables set when an instance is created. They
	// drive data-based routing (e.g. an exclusive gateway condition).
	StartVars []model.VariableValue

	// build constructs the compiled process for the in-process benchmarks and
	// returns the interned job types a driver must complete to run an instance to
	// the end. An empty slice means the instance runs start-to-end with no
	// external worker (script/gateway-only paths).
	build func(key uint64) (*compiler.CompiledProcess, []int32)
}

// Compile builds the compiled process for in-process (level 1) benchmarks and
// returns the job types that must be completed to finish an instance.
func (s Scenario) Compile(key uint64) (*compiler.CompiledProcess, []int32) {
	return s.build(key)
}

// All returns the catalogue in a stable order. Benchmarks and the baseline are
// keyed by scenario name, so appending here is safe; reordering is harmless.
func All() []Scenario {
	return []Scenario{Linear(), Exclusive(), Parallel(), Script(), Mixed()}
}

// mustExpr compiles a FEEL expression at construction time. The catalogue is a
// fixed set of authored fixtures, so a compile failure here is a programming
// error in this file, not a runtime condition.
func mustExpr(src string) *expr.Compiled {
	c, err := expr.CompileAuto(src)
	if err != nil {
		panic(fmt.Sprintf("scenarios: compile %q: %v", src, err))
	}
	return c
}

// mustBuild finalizes a builder, panicking on error for the same reason as
// mustExpr: the catalogue is authored, not user input.
func mustBuild(b *compiler.Builder) *compiler.CompiledProcess {
	cp, err := b.Build()
	if err != nil {
		panic(fmt.Sprintf("scenarios: build: %v", err))
	}
	return cp
}

// Linear is the baseline: Start → ServiceTask → End, one external job per
// instance. It mirrors engine.BenchmarkInstanceLifecycle so the two stay
// comparable, and isolates the cost of the create → job → complete → finish
// cycle that every service task pays.
func Linear() Scenario {
	return Scenario{
		Name:        "linear",
		Feature:     "service-task",
		Description: "Start → ServiceTask → End; one external job — the baseline instance lifecycle.",
		BPMN: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="linear" name="Linear" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="t"><extensionElements><zeebe:taskDefinition type="work" retries="3"/></extensionElements></serviceTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </process>
</definitions>`,
		build: func(key uint64) (*compiler.CompiledProcess, []int32) {
			b := compiler.NewBuilder(key, "linear", 1)
			s := b.AddStartEvent()
			t := b.AddServiceTask("work", 3)
			e := b.AddEndEvent()
			b.Connect(s, t)
			b.Connect(t, e)
			cp := mustBuild(b)
			return cp, []int32{cp.ServiceTask(cp.Node(t).Detail).JobType}
		},
	}
}

// Exclusive isolates data-based XOR routing: the gateway evaluates a FEEL
// condition over an instance variable and takes one branch. Both branches are
// script tasks, so the instance runs to completion with no external worker —
// the number is the gateway + expression cost, not job plumbing.
func Exclusive() Scenario {
	return Scenario{
		Name:        "exclusive",
		Feature:     "exclusive-gateway",
		Description: "Start → XOR(amount>100) → script → End; data-based routing and FEEL evaluation.",
		StartVars:   []model.VariableValue{{Name: "amount", Kind: model.VarNumber, Text: "150"}},
		BPMN: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="exclusive" name="Exclusive" isExecutable="true">
    <startEvent id="s"/>
    <exclusiveGateway id="gw" default="fLow"/>
    <scriptTask id="high"><extensionElements><zeebe:script expression="= &quot;high&quot;" resultVariable="path"/></extensionElements></scriptTask>
    <scriptTask id="low"><extensionElements><zeebe:script expression="= &quot;low&quot;" resultVariable="path"/></extensionElements></scriptTask>
    <endEvent id="eHigh"/>
    <endEvent id="eLow"/>
    <sequenceFlow id="f0" sourceRef="s" targetRef="gw"/>
    <sequenceFlow id="fHigh" sourceRef="gw" targetRef="high"><conditionExpression>= amount &gt; 100</conditionExpression></sequenceFlow>
    <sequenceFlow id="fLow" sourceRef="gw" targetRef="low"/>
    <sequenceFlow id="f3" sourceRef="high" targetRef="eHigh"/>
    <sequenceFlow id="f4" sourceRef="low" targetRef="eLow"/>
  </process>
</definitions>`,
		build: func(key uint64) (*compiler.CompiledProcess, []int32) {
			b := compiler.NewBuilder(key, "exclusive", 1)
			s := b.AddStartEvent()
			gw := b.AddExclusiveGateway()
			high := b.AddScriptTask(mustExpr(`"high"`), "path")
			low := b.AddScriptTask(mustExpr(`"low"`), "path")
			eHigh := b.AddEndEvent()
			eLow := b.AddEndEvent()
			b.Connect(s, gw)
			fHigh := b.Connect(gw, high)
			b.SetFlowCondition(fHigh, mustExpr("amount > 100"))
			fLow := b.Connect(gw, low)
			b.SetFlowDefault(fLow)
			b.Connect(high, eHigh)
			b.Connect(low, eLow)
			return mustBuild(b), nil
		},
	}
}

// Parallel isolates the AND gateway: a fork multiplies one token onto two
// branches and a join synchronizes them before the end. It measures token
// fan-out/fan-in and the join's waiting state, with pass-through tasks so no
// worker is involved.
func Parallel() Scenario {
	return Scenario{
		Name:        "parallel",
		Feature:     "parallel-gateway",
		Description: "Start → AND fork → 2 branches → AND join → End; token fan-out and synchronizing join.",
		BPMN: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="parallel" name="Parallel" isExecutable="true">
    <startEvent id="s"/>
    <parallelGateway id="fork"/>
    <task id="a" name="A"/>
    <task id="b" name="B"/>
    <parallelGateway id="join"/>
    <endEvent id="e"/>
    <sequenceFlow id="f0" sourceRef="s" targetRef="fork"/>
    <sequenceFlow id="f1" sourceRef="fork" targetRef="a"/>
    <sequenceFlow id="f2" sourceRef="fork" targetRef="b"/>
    <sequenceFlow id="f3" sourceRef="a" targetRef="join"/>
    <sequenceFlow id="f4" sourceRef="b" targetRef="join"/>
    <sequenceFlow id="f5" sourceRef="join" targetRef="e"/>
  </process>
</definitions>`,
		build: func(key uint64) (*compiler.CompiledProcess, []int32) {
			b := compiler.NewBuilder(key, "parallel", 1)
			s := b.AddStartEvent()
			fork := b.AddParallelGateway()
			ta := b.AddTask()
			tb := b.AddTask()
			join := b.AddParallelGateway()
			e := b.AddEndEvent()
			b.Connect(s, fork)
			b.Connect(fork, ta)
			b.Connect(fork, tb)
			b.Connect(ta, join)
			b.Connect(tb, join)
			b.Connect(join, e)
			return mustBuild(b), nil
		},
	}
}

// Script isolates in-engine FEEL evaluation: a script task computes an
// expression and writes the result to a variable. It is the cost of the
// compile-once/eval-many path with no gateway or worker around it.
func Script() Scenario {
	return Scenario{
		Name:        "script",
		Feature:     "script-task",
		Description: "Start → ScriptTask(FEEL) → End; in-engine expression evaluation and a variable write.",
		BPMN: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="script" name="Script" isExecutable="true">
    <startEvent id="s"/>
    <scriptTask id="x"><extensionElements><zeebe:script expression="= 1 + 2 * 3" resultVariable="r"/></extensionElements></scriptTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="x"/>
    <sequenceFlow id="f2" sourceRef="x" targetRef="e"/>
  </process>
</definitions>`,
		build: func(key uint64) (*compiler.CompiledProcess, []int32) {
			b := compiler.NewBuilder(key, "script", 1)
			s := b.AddStartEvent()
			x := b.AddScriptTask(mustExpr("1 + 2 * 3"), "r")
			e := b.AddEndEvent()
			b.Connect(s, x)
			b.Connect(x, e)
			return mustBuild(b), nil
		},
	}
}

// Mixed is the honest single number: one process that combines data-based
// routing, a parallel fork/join, and script evaluation. With amount=150 it takes
// the heavier branch (fork → two scripts → join), so the measurement reflects a
// process doing several kinds of work rather than one primitive.
func Mixed() Scenario {
	return Scenario{
		Name:        "mixed",
		Feature:     "combined",
		Description: "XOR → (AND fork → 2 scripts → join) with a script fallback; a realistic multi-feature path.",
		StartVars:   []model.VariableValue{{Name: "amount", Kind: model.VarNumber, Text: "150"}},
		BPMN: `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="mixed" name="Mixed" isExecutable="true">
    <startEvent id="s"/>
    <exclusiveGateway id="gw" default="fLow"/>
    <parallelGateway id="fork"/>
    <scriptTask id="a"><extensionElements><zeebe:script expression="= &quot;a&quot;" resultVariable="a"/></extensionElements></scriptTask>
    <scriptTask id="b"><extensionElements><zeebe:script expression="= &quot;b&quot;" resultVariable="b"/></extensionElements></scriptTask>
    <parallelGateway id="join"/>
    <endEvent id="eHigh"/>
    <scriptTask id="low"><extensionElements><zeebe:script expression="= &quot;low&quot;" resultVariable="path"/></extensionElements></scriptTask>
    <endEvent id="eLow"/>
    <sequenceFlow id="f0" sourceRef="s" targetRef="gw"/>
    <sequenceFlow id="fHigh" sourceRef="gw" targetRef="fork"><conditionExpression>= amount &gt; 100</conditionExpression></sequenceFlow>
    <sequenceFlow id="fLow" sourceRef="gw" targetRef="low"/>
    <sequenceFlow id="f1" sourceRef="fork" targetRef="a"/>
    <sequenceFlow id="f2" sourceRef="fork" targetRef="b"/>
    <sequenceFlow id="f3" sourceRef="a" targetRef="join"/>
    <sequenceFlow id="f4" sourceRef="b" targetRef="join"/>
    <sequenceFlow id="f5" sourceRef="join" targetRef="eHigh"/>
    <sequenceFlow id="f6" sourceRef="low" targetRef="eLow"/>
  </process>
</definitions>`,
		build: func(key uint64) (*compiler.CompiledProcess, []int32) {
			b := compiler.NewBuilder(key, "mixed", 1)
			s := b.AddStartEvent()
			gw := b.AddExclusiveGateway()
			fork := b.AddParallelGateway()
			ta := b.AddScriptTask(mustExpr(`"a"`), "a")
			tb := b.AddScriptTask(mustExpr(`"b"`), "b")
			join := b.AddParallelGateway()
			eHigh := b.AddEndEvent()
			low := b.AddScriptTask(mustExpr(`"low"`), "path")
			eLow := b.AddEndEvent()
			b.Connect(s, gw)
			fHigh := b.Connect(gw, fork)
			b.SetFlowCondition(fHigh, mustExpr("amount > 100"))
			fLow := b.Connect(gw, low)
			b.SetFlowDefault(fLow)
			b.Connect(fork, ta)
			b.Connect(fork, tb)
			b.Connect(ta, join)
			b.Connect(tb, join)
			b.Connect(join, eHigh)
			b.Connect(low, eLow)
			return mustBuild(b), nil
		},
	}
}
