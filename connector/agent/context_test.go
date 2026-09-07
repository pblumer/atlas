package agent_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/agent"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// What an agent is given to read (ADR-0257).
//
// The bug this closes was not subtle. Request.Context was declared, serialized into the
// job payload, rebuilt on the worker and rendered into the prompt under "What the process
// knows:" — and nothing ever filled it. An agent-driven container knew its goal, its
// tools, and what its own calls had returned, and not one process variable. Run against a
// real model on a container documented "prüfe, ob die Finanzierung dieses Kunden tragbar
// ist", it answered by asking for the purchase price, the equity and the income — all of
// which the process was holding in a variable it had no way to hand over.

// contextProcess is start → adhoc{tool} → end, where the container names two of the
// instance's three variables. The third is the point: an agent is given what the element
// names and not what happens to be in scope.
const contextProcess = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
	 xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
	 xmlns:atlas="http://atlas/schema/1.0">
  <process id="p" isExecutable="true">
    <startEvent id="s"/><endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="adhoc"/>
    <sequenceFlow id="f2" sourceRef="adhoc" targetRef="e"/>
    <adHocSubProcess id="adhoc">
      <documentation>Pruefe, ob die Finanzierung tragbar ist.</documentation>
      <extensionElements>
        <atlas:agentConnector connector="anthropic_pb" context="dossier,offen,fehlt"/>
      </extensionElements>
      <serviceTask id="rechnen">
        <documentation>Rechnet die Tragbarkeit.</documentation>
        <extensionElements><zeebe:taskDefinition type="rates"/></extensionElements>
      </serviceTask>
    </adHocSubProcess>
  </process>
</definitions>`

func resolveWithContext(t *testing.T, vars ...model.VariableValue) agent.Round {
	t.Helper()
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close(); _ = log.Close() })

	cp, err := compiler.Parse(500, 1, strings.NewReader(contextProcess))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	p := engine.New(1, log, store, &clock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key, vars...)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	var round uint64
	if err := store.ActivatableJobs(compiler.AgentJobTypeIndex, func(k uint64) error {
		round = k
		return nil
	}); err != nil {
		t.Fatalf("ActivatableJobs: %v", err)
	}
	jv, ok, err := store.GetJob(round)
	if err != nil || !ok {
		t.Fatalf("GetJob: ok=%v err=%v", ok, err)
	}
	ei, ok, err := store.GetElementInstance(jv.ElementInstanceKey)
	if err != nil || !ok {
		t.Fatalf("GetElementInstance: ok=%v err=%v", ok, err)
	}
	r, err := agent.Resolve(store, cp, ei, jv.ElementInstanceKey)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return r
}

// The named variables reach the round, in the form a model reads them.
func TestAnAgentIsGivenWhatTheElementNames(t *testing.T) {
	r := resolveWithContext(t,
		model.VariableValue{Name: "dossier", Kind: model.VarString, Text: "Kaufpreis CHF 1'150'000"},
		model.VariableValue{Name: "offen", Kind: model.VarBool, Bool: true},
		model.VariableValue{Name: "geheim", Kind: model.VarString, Text: "nicht fuer das Modell"},
	)
	if got := r.Context["dossier"]; got != "Kaufpreis CHF 1'150'000" {
		t.Errorf("dossier = %q, want the variable's value", got)
	}
	if got := r.Context["offen"]; got != "true" {
		t.Errorf("offen = %q, want a boolean in its string form", got)
	}
	// The one the element does not name never travels. "Everything in scope" is a set
	// nobody chose, and this is the line that keeps it from being sent to a third party.
	if _, sent := r.Context["geheim"]; sent {
		t.Errorf("context = %v, carries a variable the element does not name", r.Context)
	}
}

// A named variable that is not there travels as "(not set)" rather than being dropped: an
// agent that is told the process had nothing can say so, where one told nothing at all
// invents a value.
func TestANamedVariableThatIsNotThereSaysSo(t *testing.T) {
	r := resolveWithContext(t, model.VariableValue{Name: "dossier", Kind: model.VarString, Text: "da"})
	got, sent := r.Context["fehlt"]
	if !sent {
		t.Fatalf("context = %v, drops a name the element authored", r.Context)
	}
	if got != "(not set)" {
		t.Errorf("fehlt = %q, want it marked as not set", got)
	}
}

// The context survives the payload, like everything else a round carries: the engine
// resolves it and a worker rebuilds it, and the two cannot disagree without this failing.
func TestTheContextSurvivesTheJobPayload(t *testing.T) {
	r := resolveWithContext(t, model.VariableValue{Name: "dossier", Kind: model.VarString, Text: "Kaufpreis CHF 1'150'000"})
	back, err := agent.RoundFromPayload(agent.ResolveJobPayload(r))
	if err != nil {
		t.Fatalf("RoundFromPayload: %v", err)
	}
	if back.Context["dossier"] != "Kaufpreis CHF 1'150'000" {
		t.Errorf("context over the wire = %v, want the dossier", back.Context)
	}
}
