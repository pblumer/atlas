package dmn

import (
	"errors"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// boundProcess is a definition with one business rule task per binding, and a user
// task that is not one, for TaskModelOf.
const boundProcess = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="s"/>
    <businessRuleTask id="latest"><extensionElements><calledDecision decisionId="eligibility" resultVariable="a"/></extensionElements></businessRuleTask>
    <businessRuleTask id="pinned"><extensionElements><calledDecision decisionId="eligibility" resultVariable="b" atlas:version="2"/></extensionElements></businessRuleTask>
    <businessRuleTask id="bundled"><extensionElements><calledDecision decisionId="eligibility" resultVariable="c" bindingType="deployment"/></extensionElements></businessRuleTask>
    <businessRuleTask id="remote"><extensionElements><calledDecision decisionId="eligibility" resultVariable="d"/><atlas:temisConnector connector="risk"/></extensionElements></businessRuleTask>
    <userTask id="wait"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="latest"/>
    <sequenceFlow id="f2" sourceRef="latest" targetRef="pinned"/>
    <sequenceFlow id="f3" sourceRef="pinned" targetRef="bundled"/>
    <sequenceFlow id="f4" sourceRef="bundled" targetRef="remote"/>
    <sequenceFlow id="f6" sourceRef="remote" targetRef="wait"/>
    <sequenceFlow id="f5" sourceRef="wait" targetRef="e"/>
  </process>
</definitions>`

func parseBound(t *testing.T) *compiler.CompiledProcess {
	t.Helper()
	cp, err := compiler.Parse(9001, 1, strings.NewReader(boundProcess))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return cp
}

// TestTaskModelOf pins the decision peek's reading of a deployed task to the worker's:
// each binding resolves as decisionModelKey resolves it, and what is not a local
// decision task is said rather than guessed at (ADR-draft-decision-peek).
func TestTaskModelOf(t *testing.T) {
	reg := NewRegistry()
	if err := reg.DeployDecision(3001, []byte(eligibilityModel)); err != nil {
		t.Fatal(err)
	}

	t.Run("runtime policy", func(t *testing.T) {
		cp := parseBound(t)
		cp.ResolveLatestAtRuntime(map[compiler.DecisionVersionRef]uint64{{DecisionID: "eligibility", Version: 2}: 3002})
		for _, c := range []struct {
			element, binding string
			version          int32
			key              uint64
		}{
			{"latest", "latest", 0, 3001},
			{"pinned", "version", 2, 3002},
			{"bundled", "deployment", 0, cp.Key},
		} {
			tm, err := TaskModelOf(reg, cp, c.element)
			if err != nil || !tm.Resolved || tm.DecisionID != "eligibility" || tm.Binding != c.binding || tm.Version != c.version || tm.Key != c.key {
				t.Errorf("%s = %+v, %v; want binding %s v%d key %d", c.element, tm, err, c.binding, c.version, c.key)
			}
		}
	})

	t.Run("a version the definition did not resolve is the worker's error", func(t *testing.T) {
		cp := parseBound(t)
		cp.ResolveLatestAtRuntime(nil)
		tm, err := TaskModelOf(reg, cp, "pinned")
		if err == nil || tm.Binding != "version" {
			t.Fatalf("unresolved version = %+v, %v; want the worker's error", tm, err)
		}
	})

	t.Run("a definition deployed before pinning resolves when activated", func(t *testing.T) {
		tm, err := TaskModelOf(reg, parseBound(t), "latest")
		if err != nil || tm.Resolved || tm.Key != 0 {
			t.Fatalf("legacy latest = %+v, %v; want unresolved", tm, err)
		}
	})

	t.Run("what is not a local decision task", func(t *testing.T) {
		cp := parseBound(t)
		// A task evaluated by a temis Worker has no local model to name (ADR-0050).
		for _, id := range []string{"remote", "wait", "nope", ""} {
			if _, err := TaskModelOf(reg, cp, id); !errors.Is(err, ErrNotADecisionTask) {
				t.Errorf("%q = %v, want ErrNotADecisionTask", id, err)
			}
		}
		if _, err := TaskModelOf(reg, nil, "latest"); !errors.Is(err, ErrNotADecisionTask) {
			t.Errorf("nil definition = %v, want ErrNotADecisionTask", err)
		}
	})
}

// TestRegistryLogic is Registry.Graph's counterpart: the logic of the model a key
// evaluates a decision from, falling back to the newest model providing it.
func TestRegistryLogic(t *testing.T) {
	reg := NewRegistry()
	if err := reg.DeployDecision(3001, []byte(eligibilityModel)); err != nil {
		t.Fatal(err)
	}
	for _, key := range []uint64{3001, 0, 4242} {
		l, ok := reg.Logic(key, "eligibility")
		if !ok || len(l.Elements) != 1 || l.Elements[0].Kind != "literal" {
			t.Errorf("Logic(%d) = %+v, %v; want the literal decision", key, l, ok)
		}
	}
	if l, ok := reg.Logic(0, "unknown"); ok || l.Elements == nil || len(l.Elements) != 0 {
		t.Errorf("Logic of an unknown decision = %+v, %v; want false and an empty list", l, ok)
	}
}
