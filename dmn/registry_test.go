package dmn_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// eligibilityModel is one decision whose answer is templated, so two deployments of
// the same decision id can be told apart by what they evaluate to.
func eligibilityModel(verdict string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="defs_elig" name="Eligibility" namespace="http://atlas/dmn">
  <inputData id="id_amount" name="amount"/>
  <decision id="eligibility" name="eligibility">
    <informationRequirement><requiredInput href="#id_amount"/></informationRequirement>
    <decisionTable id="dt" hitPolicy="UNIQUE">
      <input id="in1"><inputExpression id="ie1" typeRef="number"><text>amount</text></inputExpression></input>
      <output id="out1" name="eligibility" typeRef="string"/>
      <rule id="r1"><inputEntry id="e1"><text>&gt;= 100</text></inputEntry><outputEntry id="o1"><text>"` + verdict + `"</text></outputEntry></rule>
    </decisionTable>
  </decision>
</definitions>`
}

// TestUndeployDecisionLeavesTheRegistryAsARestartWould is the claim the whole
// delete rests on (ADR-0336): after removing a
// decision deployment, the registry must answer exactly what the surviving records
// would produce on a fresh boot. Both pointers are last-write-wins with no history,
// so this is the case that is easy to get wrong.
func TestUndeployDecisionLeavesTheRegistryAsARestartWould(t *testing.T) {
	// Three deployments of one decision, oldest first, exactly as keys are minted.
	live := dmn.NewRegistry()
	for _, k := range []uint64{10, 20, 30} {
		if err := live.DeployDecision(k, []byte(eligibilityModel(fmt.Sprintf("v%d", k)))); err != nil {
			t.Fatalf("DeployDecision %d: %v", k, err)
		}
	}
	if key, ok := live.LatestDecisionKey("eligibility"); !ok || key != 30 {
		t.Fatalf("latest = (%d, %v), want the newest deployment", key, ok)
	}

	live.UndeployDecision(30)

	// A fresh registry holding only the survivors, replayed in key order — which is
	// what recovery does.
	rebooted := dmn.NewRegistry()
	for _, k := range []uint64{10, 20} {
		if err := rebooted.DeployDecision(k, []byte(eligibilityModel(fmt.Sprintf("v%d", k)))); err != nil {
			t.Fatalf("DeployDecision %d: %v", k, err)
		}
	}

	liveKey, liveOK := live.LatestDecisionKey("eligibility")
	bootKey, bootOK := rebooted.LatestDecisionKey("eligibility")
	if liveKey != bootKey || liveOK != bootOK {
		t.Fatalf("after delete latest = (%d, %v), after a restart it would be (%d, %v)", liveKey, liveOK, bootKey, bootOK)
	}
	if liveKey != 20 {
		t.Fatalf("latest = %d, want it to fall back to the previous version, not to nothing", liveKey)
	}
	// The removed key evaluates nothing, and the survivor still does.
	if _, err := live.Evaluate(context.Background(), 30, "eligibility", map[string]any{"amount": 250}); err == nil {
		t.Error("the deleted deployment still evaluates")
	}
	out, err := live.Evaluate(context.Background(), 20, "eligibility", map[string]any{"amount": 250})
	if err != nil {
		t.Fatalf("surviving deployment: %v", err)
	}
	if out["eligibility"] != "v20" {
		t.Errorf("surviving deployment answered %v, want the model it was deployed with", out)
	}
}

// TestUndeployingTheWholeLineageLeavesNoPointer: with every deployment of a
// decision gone, a latest-bound reference resolves to nothing again — which is the
// state before it was ever deployed, and what the deploy preflight reads to decide
// whether a model has to be bundled (ADR-0327).
func TestUndeployingTheWholeLineageLeavesNoPointer(t *testing.T) {
	r := dmn.NewRegistry()
	for _, k := range []uint64{5, 6} {
		if err := r.DeployDecision(k, []byte(eligibilityModel("x"))); err != nil {
			t.Fatalf("DeployDecision %d: %v", k, err)
		}
	}
	r.UndeployDecision(6)
	r.UndeployDecision(5)
	if key, ok := r.LatestDecisionKey("eligibility"); ok {
		t.Fatalf("latest = (%d, true), want nothing after the whole lineage is gone", key)
	}
	if ids := r.LatestDecisionIDs(); len(ids) != 0 {
		t.Fatalf("LatestDecisionIDs = %v, want empty", ids)
	}
}

// TestUndeployDecisionLeavesABundledModelAlone: a model a *process* bundled is
// registered under that process's key and is not a decision deployment. Removing
// it here would undeploy part of a process, so the key is not one this touches.
func TestUndeployDecisionLeavesABundledModelAlone(t *testing.T) {
	r := dmn.NewRegistry()
	if err := r.Deploy(7, []byte(eligibilityModel("bundled"))); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	r.UndeployDecision(7)
	out, err := r.Evaluate(context.Background(), 7, "eligibility", map[string]any{"amount": 250})
	if err != nil {
		t.Fatalf("the process's bundled model was removed: %v", err)
	}
	if out["eligibility"] != "bundled" {
		t.Errorf("bundled model answered %v", out)
	}
}

// labelledModel is one decision whose label differs from its FEEL identifier, the
// shape a business rule task may address by either name (ADR-0385).
func labelledModel(label string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" id="defs_lab" name="Labelled" namespace="http://atlas/dmn">
  <decision id="eligibility" name="` + label + `">
    <variable id="v_elig" name="eligibility"/>
    <literalExpression><text>"approve"</text></literalExpression>
  </decision>
</definitions>`
}

// TestProvidedByDecisionOtherThanAnswersForEitherName: the question a delete asks
// before removing a key is answered by every name a task may carry, the label and
// the identifier alike, counts decision deployments only, and leaves the key being
// removed out.
func TestProvidedByDecisionOtherThanAnswersForEitherName(t *testing.T) {
	r := dmn.NewRegistry()
	if err := r.DeployDecision(1, []byte(labelledModel("Eligibility (label)"))); err != nil {
		t.Fatalf("DeployDecision: %v", err)
	}
	// A process's bundled copy is not a decision deployment and must not count.
	if err := r.Deploy(2, []byte(labelledModel("Eligibility (label)"))); err != nil {
		t.Fatalf("Deploy: %v", err)
	}
	for _, name := range []string{"eligibility", "Eligibility (label)"} {
		if r.ProvidedByDecisionOtherThan(name, 1) {
			t.Errorf("%q: provided by another decision deployment, want only key 1 (key 2 is a bundled copy)", name)
		}
	}
	if err := r.DeployDecision(3, []byte(labelledModel("Eligibility (renamed)"))); err != nil {
		t.Fatalf("DeployDecision: %v", err)
	}
	if !r.ProvidedByDecisionOtherThan("eligibility", 1) {
		t.Error("identifier: want key 3 to provide it under a different label")
	}
	if r.ProvidedByDecisionOtherThan("Eligibility (label)", 1) {
		t.Error("old label: key 3 does not answer to it")
	}
}
