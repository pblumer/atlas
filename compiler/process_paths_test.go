package compiler_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// TestBundleBoundDecisionsNameEachDeploymentBoundDecisionOnce: these are the
// references a deploy must bundle a model for (ADR-0327). A decision two tasks share
// is one model to bundle, and a latest-bound or central decision needs none.
func TestBundleBoundDecisionsNameEachDeploymentBoundDecisionOnce(t *testing.T) {
	b := compiler.NewBuilder(502, "orders", 1)
	start := b.AddStartEvent()
	var prev = start
	for _, task := range []struct {
		decision string
		binding  compiler.DecisionBinding
	}{
		{"discount", compiler.BindingDeployment},
		{"eligibility", compiler.BindingLatest},
		{"shipping", compiler.BindingDeployment},
		{"discount", compiler.BindingDeployment},
	} {
		id, err := b.AddBusinessRuleTaskMapped(task.decision, "", nil, nil, 3, task.binding)
		if err != nil {
			t.Fatalf("task %s: %v", task.decision, err)
		}
		b.Connect(prev, id)
		prev = id
	}
	central, err := b.AddTemisDecisionTask("central", "risk", "r", nil, nil, 3)
	if err != nil {
		t.Fatalf("central task: %v", err)
	}
	end := b.AddEndEvent()
	b.Connect(prev, central)
	b.Connect(central, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if got, want := cp.BundleBoundDecisions(), []string{"discount", "shipping"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("BundleBoundDecisions = %v, want %v", got, want)
	}
}

// versionBoundProcess compiles one process whose business rule tasks carry the given
// calledDecision attributes, in order.
func versionBoundProcess(t *testing.T, tasks ...string) *compiler.CompiledProcess {
	t.Helper()
	var body strings.Builder
	prev := "s"
	for i, attrs := range tasks {
		id := fmt.Sprintf("d%d", i)
		fmt.Fprintf(&body, `<businessRuleTask id="%s"><extensionElements><calledDecision %s/></extensionElements></businessRuleTask>`, id, attrs)
		fmt.Fprintf(&body, `<sequenceFlow id="f%d" sourceRef="%s" targetRef="%s"/>`, i, prev, id)
		prev = id
	}
	bpmn := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
	  <process id="p" isExecutable="true"><startEvent id="s"/>` + body.String() +
		`<endEvent id="e"/><sequenceFlow id="fe" sourceRef="` + prev + `" targetRef="e"/></process></definitions>`
	cp, err := compiler.Parse(1, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return cp
}

// TestVersionBoundDecisionsAreDistinctPairs: a deploy resolves each (decision,
// version) once, however many tasks name it, and nothing it was not asked to pin.
func TestVersionBoundDecisionsAreDistinctPairs(t *testing.T) {
	cp := versionBoundProcess(t,
		`decisionId="Dish" atlas:version="3"`,
		`decisionId="Dish"`,
		`decisionId="Dish" atlas:version="3"`,
		`decisionId="Dish" atlas:version="2"`,
	)
	want := []compiler.DecisionVersionRef{{DecisionID: "Dish", Version: 3}, {DecisionID: "Dish", Version: 2}}
	if got := cp.VersionBoundDecisions(); !reflect.DeepEqual(got, want) {
		t.Fatalf("VersionBoundDecisions = %+v, want %+v", got, want)
	}
}

// TestTheRuntimePolicyAnswersOnlyForWhatWasPinned: a version-bound task under the
// runtime policy evaluates exactly the deployment its version resolved to, and a
// version the deploy recorded nothing for answers "none" — never a neighbour
// (ADR-0423).
func TestTheRuntimePolicyAnswersOnlyForWhatWasPinned(t *testing.T) {
	cp := versionBoundProcess(t, `decisionId="Dish" atlas:version="3"`)
	if cp.LatestAtRuntime() {
		t.Fatal("LatestAtRuntime = true before the deploy chose the runtime policy")
	}
	cp.ResolveLatestAtRuntime(map[compiler.DecisionVersionRef]uint64{{DecisionID: "Dish", Version: 3}: 7003})
	if !cp.LatestAtRuntime() {
		t.Fatal("LatestAtRuntime = false after ResolveLatestAtRuntime")
	}
	if key, ok := cp.VersionPinnedKey("Dish", 3); !ok || key != 7003 {
		t.Fatalf("VersionPinnedKey(Dish, 3) = %d, %v; want 7003, true", key, ok)
	}
	if key, ok := cp.VersionPinnedKey("Dish", 2); ok {
		t.Fatalf("VersionPinnedKey(Dish, 2) = %d, true; want nothing for a version never pinned", key)
	}
}

// TestEveryBindingHasItsWireToken: the token is what the Modeler writes and reads
// back, and an unknown value is shown as itself rather than mapped to latest.
func TestEveryBindingHasItsWireToken(t *testing.T) {
	for b, want := range map[compiler.DecisionBinding]string{
		compiler.BindingLatest:       "latest",
		compiler.BindingDeployment:   "deployment",
		compiler.BindingVersion:      "version",
		compiler.BindingVersionTag:   "versionTag",
		compiler.DecisionBinding(42): "DecisionBinding(42)",
	} {
		if got := b.String(); got != want {
			t.Errorf("DecisionBinding(%d).String() = %q, want %q", int32(b), got, want)
		}
	}
}

// TestEveryElementTypeHasItsOwnName walks the whole defined range: a type that
// printed as "Unspecified" would make a validation finding or a timeline row
// unreadable, and two types sharing a name would make two kinds of element look the
// same. Only the zero value is unspecified.
func TestEveryElementTypeHasItsOwnName(t *testing.T) {
	seen := map[string]compiler.BpmnType{}
	for ty := compiler.TypeUnspecified + 1; ty < compiler.NumBpmnTypes; ty++ {
		name := ty.String()
		if name == "" || name == "Unspecified" {
			t.Errorf("BpmnType(%d) has no name: %q", ty, name)
		} else if other, dup := seen[name]; dup {
			t.Errorf("BpmnType(%d) and BpmnType(%d) are both %q", other, ty, name)
		}
		seen[name] = ty
	}
}
