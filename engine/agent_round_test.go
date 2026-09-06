package engine_test

import (
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// agentAdHoc builds: start → adhoc{zinsen_holen, historie_lesen} → end, where the ad-hoc is
// agent-driven (ADR-0253). Both contained tasks are unconnected, so both are entry activities
// and therefore both are tools — but unlike a plain ad-hoc, entering the container activates
// neither. The BPMN ids are set explicitly because a tool's name is its element id: that is
// what the model is told and what it calls back with.
func agentAdHoc(t *testing.T, key uint64, id string) (cp *compiler.CompiledProcess, adhoc, a, b, end int32) {
	t.Helper()
	bl := compiler.NewBuilder(key, id, 1)
	start := bl.AddStartEvent()
	adhoc = bl.AddAdHocSubProcess(compiler.AdHocDetail{
		CancelRemaining:  true,
		AgentDriven:      true,
		AgentWorker:      -1,
		ResultCollection: -1,
	})
	end = bl.AddEndEvent()
	bl.Connect(start, adhoc)
	bl.Connect(adhoc, end)
	bl.PushScope(adhoc)
	a = bl.AddServiceTask("ta", 3)
	b = bl.AddServiceTask("tb", 3)
	bl.PopScope()
	bl.SetElementBpmnId(adhoc, "adhoc")
	bl.SetElementBpmnId(a, "zinsen_holen")
	bl.SetElementBpmnId(b, "historie_lesen")
	cp, err := bl.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, adhoc, a, b, end
}

// startAgentAdHoc runs an instance up to the point where the container parks on its first
// round job, and returns the harness pieces every test below needs.
func startAgentAdHoc(t *testing.T, h *harness, key uint64, id string) (*engine.Processor, *compiler.CompiledProcess, int32, int32, int32) {
	t.Helper()
	cp, _, a, b, end := agentAdHoc(t, key, id)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	return p, cp, a, b, end
}

func jobTypeOf(t *testing.T, cp *compiler.CompiledProcess, node int32) int32 {
	t.Helper()
	return cp.ServiceTask(cp.Node(node).Detail).JobType
}

// toolScopeVariables collects what an activated tool can see from its own scope, by name —
// the same read the tool's own expressions and I/O mappings do.
func toolScopeVariables(t *testing.T, s *state.Store, elementInstanceKey uint64) map[string]model.VariableValue {
	t.Helper()
	out := map[string]model.VariableValue{}
	if err := state.VisibleVariables(s, elementInstanceKey, func(v *model.VariableValue) error {
		out[v.Name] = *v
		return nil
	}); err != nil {
		t.Fatalf("VisibleVariables: %v", err)
	}
	return out
}

// TestAgentAdHocParksOnItsRoundJob is the inversion of ADR-0138's entry rule: entering an
// agent-driven container activates *nothing* and creates one job on the container itself.
// Which contained activity runs is the model's choice, and it has not been asked yet.
func TestAgentAdHocParksOnItsRoundJob(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	_, cp, a, b, _ := startAgentAdHoc(t, h, 300, "agent-parks")

	// One element instance: the container. Neither tool was activated.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("after entry: process=%d element=%d, want 1 and 1 (the container alone)", pi, ei)
	}
	if !jobGone(t, h.store, jobTypeOf(t, cp, a)) || !jobGone(t, h.store, jobTypeOf(t, cp, b)) {
		t.Error("a tool has a job, but entry must activate nothing on an agent-driven ad-hoc")
	}
	// And the container itself parks on exactly one round job.
	if key := singleActivatableJob(t, h.store, compiler.AgentJobTypeIndex); key == 0 {
		t.Fatal("no round job on the container")
	}
}

// TestAgentRoundActivatesOnlyTheChosenTool: the round job comes back naming one tool, and
// exactly that contained activity is activated. The container stays Activated — the round is
// not the container's completion, it is its next step.
func TestAgentRoundActivatesOnlyTheChosenTool(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	p, cp, a, b, end := startAgentAdHoc(t, h, 301, "agent-one-tool")
	round := singleActivatableJob(t, h.store, compiler.AgentJobTypeIndex)

	p.CompleteJobWithToolCalls(round, []engine.ToolCall{{Tool: "zinsen_holen", CallId: "call-1"}})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	if jobGone(t, h.store, jobTypeOf(t, cp, a)) {
		t.Error("the chosen tool has no job, but it should have been activated")
	}
	if !jobGone(t, h.store, jobTypeOf(t, cp, b)) {
		t.Error("the tool the agent did not choose was activated anyway")
	}
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("after one call: process=%d element=%d, want 1 and 2 (container + one tool)", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key)[end]; v != 0 {
		t.Error("the container completed, but a round with tool calls must leave it running")
	}
}

// TestAgentRoundRepeatsATool: two calls naming the same activity are two independent
// instances of it. That is the ad-hoc's zero-or-more semantics doing its job — and the
// reason the toolbox is an ad-hoc rather than a fan-out of modelled branches.
func TestAgentRoundRepeatsATool(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	p, _, _, _, _ := startAgentAdHoc(t, h, 302, "agent-repeat")
	round := singleActivatableJob(t, h.store, compiler.AgentJobTypeIndex)

	p.CompleteJobWithToolCalls(round, []engine.ToolCall{
		{Tool: "zinsen_holen", CallId: "call-1"},
		{Tool: "zinsen_holen", CallId: "call-2"},
	})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	if pi, ei := counts(t, h.store); pi != 1 || ei != 3 {
		t.Fatalf("after two calls to one tool: process=%d element=%d, want 1 and 3 (container + two instances)", pi, ei)
	}
}

// TestAgentRoundBindsArgumentsInTheToolsScope: a call's arguments — and the id of the call
// itself, so a result can be paired back to it — are written into the activated activity's
// own scope, where its I/O mappings and expressions read them.
func TestAgentRoundBindsArgumentsInTheToolsScope(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	p, cp, a, _, _ := startAgentAdHoc(t, h, 303, "agent-args")
	round := singleActivatableJob(t, h.store, compiler.AgentJobTypeIndex)

	p.CompleteJobWithToolCalls(round, []engine.ToolCall{{
		Tool:   "zinsen_holen",
		CallId: "call-1",
		Arguments: []model.VariableValue{
			{Name: "url", Kind: model.VarString, Text: "https://www.migrosbank.ch/zinsen"},
		},
	}})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	toolJob := singleActivatableJob(t, h.store, jobTypeOf(t, cp, a))
	job, ok, err := h.store.GetJob(toolJob)
	if err != nil || !ok {
		t.Fatalf("GetJob(%d): ok=%v err=%v", toolJob, ok, err)
	}
	vars := toolScopeVariables(t, h.store, job.ElementInstanceKey)
	if got := vars["url"].Text; got != "https://www.migrosbank.ch/zinsen" {
		t.Errorf("url in the tool's scope = %q, want the argument the agent supplied", got)
	}
	if got := vars[engine.AgentCallIdVariable].Text; got != "call-1" {
		t.Errorf("%s = %q, want call-1 so the result can be paired back", engine.AgentCallIdVariable, got)
	}
}

// TestAgentRoundWithoutToolCallsCompletesTheContainer: a completion naming no tool is the
// agent saying it is done. The container then completes through the ordinary path and takes
// its outgoing flow — an agent-driven ad-hoc is an ordinary activity to the flow around it.
func TestAgentRoundWithoutToolCallsCompletesTheContainer(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	p, cp, _, _, end := startAgentAdHoc(t, h, 304, "agent-done")
	round := singleActivatableJob(t, h.store, compiler.AgentJobTypeIndex)

	p.CompleteJob(round)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	if got := elementVisits(t, h.store, cp.Key)[end]; got != 1 {
		t.Fatalf("end visits = %d, want 1 — no tool calls ends the loop", got)
	}
	if len(incidents(t, h.store)) != 0 {
		t.Error("finishing an agent run raised an incident")
	}
}

// TestAgentAsksForAnUnknownToolRaisesAnIncident: the model named something the deploy-time
// tool index does not have, which means the worker offered it something it should not have.
// Stopping visibly beats silently skipping a step the agent believes it took.
func TestAgentAsksForAnUnknownToolRaisesAnIncident(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	p, cp, _, _, end := startAgentAdHoc(t, h, 305, "agent-unknown-tool")
	round := singleActivatableJob(t, h.store, compiler.AgentJobTypeIndex)

	p.CompleteJobWithToolCalls(round, []engine.ToolCall{{Tool: "sap_buchen", CallId: "call-1"}})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	inc := incidents(t, h.store)
	if len(inc) != 1 {
		t.Fatalf("incidents = %d, want 1", len(inc))
	}
	for _, v := range inc {
		if !strings.Contains(v.Message, "sap_buchen") || !strings.Contains(v.Message, "zinsen_holen") {
			t.Errorf("incident message = %q, want it to name the tool asked for and the ones on offer", v.Message)
		}
	}
	if v := elementVisits(t, h.store, cp.Key)[end]; v != 0 {
		t.Error("the container completed despite the incident")
	}
}
