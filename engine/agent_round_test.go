package engine_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/expr"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
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

	p.CompleteJobWithToolCalls(round, []model.ToolCall{{Tool: "zinsen_holen", CallId: "call-1"}})
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

	p.CompleteJobWithToolCalls(round, []model.ToolCall{
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

	p.CompleteJobWithToolCalls(round, []model.ToolCall{{
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

	p.CompleteJobWithToolCalls(round, []model.ToolCall{{Tool: "sap_buchen", CallId: "call-1"}})
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

// agentAdHocWithResults is agentAdHoc plus a result collection: each finished tool appends
// the value of resultElement to toolCallResults on the container's scope, which is where the
// next round's job — and the agent behind it — reads what earlier calls returned.
func agentAdHocWithResults(t *testing.T, key uint64, id string, cond *expr.Compiled) (cp *compiler.CompiledProcess, adhoc, a, end int32) {
	t.Helper()
	bl := compiler.NewBuilder(key, id, 1)
	start := bl.AddStartEvent()
	adhoc = bl.AddAdHocSubProcess(compiler.AdHocDetail{
		CancelRemaining:     true,
		AgentDriven:         true,
		AgentWorker:         -1,
		CompletionCondition: cond,
		ResultCollection:    -1,
	})
	bl.SetAdHocResultCollection(adhoc, "toolCallResults", mustCompile(t, "toolCallId"))
	end = bl.AddEndEvent()
	bl.Connect(start, adhoc)
	bl.Connect(adhoc, end)
	bl.PushScope(adhoc)
	a = bl.AddServiceTask("ta", 3)
	bl.PopScope()
	bl.SetElementBpmnId(adhoc, "adhoc")
	bl.SetElementBpmnId(a, "zinsen_holen")
	cp, err := bl.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, adhoc, a, end
}

// TestAgentRoundEndsWhereTheScopeDrains is the loop: the tools of a round finish, the
// container's scope drains, and that is the moment the agent is asked again — a *new* round
// job, not the container's completion. Round two ends the run by naming no tool.
func TestAgentRoundEndsWhereTheScopeDrains(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, _, a, end := agentAdHocWithResults(t, 306, "agent-rounds", nil)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// Round one: the agent calls one tool.
	p.CompleteJobWithToolCalls(singleActivatableJob(t, h.store, compiler.AgentJobTypeIndex),
		[]model.ToolCall{{Tool: "zinsen_holen", CallId: "call-1"}})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (round 1): %v", err)
	}
	// Working that tool drains the round — and must produce the *next* round's job rather
	// than completing the container.
	p.CompleteJob(singleActivatableJob(t, h.store, jobTypeOf(t, cp, a)))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (tool done): %v", err)
	}
	if v := elementVisits(t, h.store, cp.Key)[end]; v != 0 {
		t.Fatal("the container completed when its round drained, instead of asking for the next")
	}
	round2 := singleActivatableJob(t, h.store, compiler.AgentJobTypeIndex)

	// Round two: no tool calls ends the loop.
	p.CompleteJob(round2)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (round 2): %v", err)
	}
	if v := elementVisits(t, h.store, cp.Key)[end]; v != 1 {
		t.Errorf("end visits = %d, want 1 once the agent stops calling tools", v)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Errorf("after the run: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestAgentToolResultsAccumulate: each finished tool appends to the container's collection,
// in the order the tools finished, and the list survives into the next round — which is what
// makes a round able to build on the one before it.
func TestAgentToolResultsAccumulate(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, adhoc, a, _ := agentAdHocWithResults(t, 307, "agent-results", nil)
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	_ = adhoc

	p.CompleteJobWithToolCalls(singleActivatableJob(t, h.store, compiler.AgentJobTypeIndex),
		[]model.ToolCall{{Tool: "zinsen_holen", CallId: "call-1"}, {Tool: "zinsen_holen", CallId: "call-2"}})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (round 1): %v", err)
	}
	// Work both tools; each appends its toolCallId to the collection.
	for i := 0; i < 2; i++ {
		var keys []uint64
		if err := h.store.ActivatableJobs(jobTypeOf(t, cp, a), func(k uint64) error {
			keys = append(keys, k)
			return nil
		}); err != nil {
			t.Fatalf("ActivatableJobs: %v", err)
		}
		if len(keys) == 0 {
			t.Fatalf("tool job %d of 2 is missing", i+1)
		}
		p.CompleteJob(keys[0])
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle (tool %d): %v", i+1, err)
		}
	}

	// The container is parked on the next round; its scope carries what the calls returned.
	job, ok, err := h.store.GetJob(singleActivatableJob(t, h.store, compiler.AgentJobTypeIndex))
	if err != nil || !ok {
		t.Fatalf("GetJob: ok=%v err=%v", ok, err)
	}
	vars := toolScopeVariables(t, h.store, job.ElementInstanceKey)
	got := vars["toolCallResults"].Text
	if !strings.Contains(got, "call-1") || !strings.Contains(got, "call-2") {
		t.Errorf("toolCallResults = %q, want both calls' results", got)
	}
}

// TestAgentLoopIsBoundedByTheCompletionCondition: an agent that never stops calling tools is
// stopped by the model instead. The completion condition is evaluated where the round would
// otherwise begin again, so it is the model's own ceiling on a run — no engine-side counter.
func TestAgentLoopIsBoundedByTheCompletionCondition(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	cp, _, a, end := agentAdHocWithResults(t, 308, "agent-bounded", mustCompile(t, "count(toolCallResults) >= 1"))
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	p.CompleteJobWithToolCalls(singleActivatableJob(t, h.store, compiler.AgentJobTypeIndex),
		[]model.ToolCall{{Tool: "zinsen_holen", CallId: "call-1"}})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (round 1): %v", err)
	}
	p.CompleteJob(singleActivatableJob(t, h.store, jobTypeOf(t, cp, a)))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (tool done): %v", err)
	}

	if v := elementVisits(t, h.store, cp.Key)[end]; v != 1 {
		t.Errorf("end visits = %d, want 1 — the completion condition ends the loop", v)
	}
	if !jobGone(t, h.store, compiler.AgentJobTypeIndex) {
		t.Error("a further round was asked for even though the completion condition held")
	}
}

// TestAgentRoundRecoversMidRound is the determinism contract of ADR-0253 in one test: an
// instance parked mid-round — the agent's choice made, its tool still working — is replayed
// into an empty store and continues from the tool's completion. Nothing re-asks the model:
// what the agent chose is frozen in the activation events, so recovery re-applies the same
// choice rather than making a new one (I4/I6).
func TestAgentRoundRecoversMidRound(t *testing.T) {
	dir := t.TempDir()
	cp, _, a, end := agentAdHocWithResults(t, 309, "agent-recovery", nil)
	toolJobType := jobTypeOf(t, cp, a)

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, &manualClock{})
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// The agent chooses a tool, and we stop the world while that tool is still working.
	p1.CompleteJobWithToolCalls(singleActivatableJob(t, h1.store, compiler.AgentJobTypeIndex),
		[]model.ToolCall{{Tool: "zinsen_holen", CallId: "call-1"}})
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (round 1): %v", err)
	}
	if pi, ei := counts(t, h1.store); pi != 1 || ei != 2 {
		t.Fatalf("parked mid-round: process=%d element=%d, want 1 and 2 (container + its tool)", pi, ei)
	}
	h1.close(t)

	// Replay into a fresh, empty store.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() { _ = store2.Close(); _ = log2.Close() }()
	p2 := engine.New(1, log2, store2, &manualClock{})
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}

	if pi, ei := counts(t, store2); pi != 1 || ei != 2 {
		t.Fatalf("after replay: process=%d element=%d, want 1 and 2 (the agent's choice rebuilt)", pi, ei)
	}
	// The round's own job is done: replay must not manufacture a second one, or the model
	// would be asked twice for a choice it already made.
	if !jobGone(t, store2, compiler.AgentJobTypeIndex) {
		t.Error("replay left a round job outstanding — the agent would be asked again")
	}
	if jobGone(t, store2, toolJobType) {
		t.Fatal("the chosen tool's job did not survive replay")
	}

	// And the recovered instance carries on: the tool finishes, the round drains, the next
	// round is asked for, and an empty answer ends the run.
	p2.CompleteJob(singleActivatableJob(t, store2, toolJobType))
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (tool done): %v", err)
	}
	p2.CompleteJob(singleActivatableJob(t, store2, compiler.AgentJobTypeIndex))
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (round 2): %v", err)
	}
	if v := elementVisits(t, store2, cp.Key)[end]; v != 1 {
		t.Errorf("end visits after recovery = %d, want 1", v)
	}
}

// TestAgentFinalAnswerOutlivesTheContainer settles the question the loop raises: the result
// collection lives on the container's scope and is dropped with it, so does the agent's
// *final* answer vanish too? It does not. A round job is a job like any other, so its
// completion outputs follow ioResultScope (ADR-0068) — with no output mapping on the
// container they land in the process scope, where the step after the ad-hoc reads them.
func TestAgentFinalAnswerOutlivesTheContainer(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	// start → adhoc{zinsen_holen} → weiterverarbeiten → end. The task after the container
	// is what makes the question answerable: it is still live when the agent is gone.
	bl := compiler.NewBuilder(311, "agent-answer", 1)
	begin := bl.AddStartEvent()
	adhoc := bl.AddAdHocSubProcess(compiler.AdHocDetail{
		CancelRemaining: true, AgentDriven: true, AgentWorker: -1, ResultCollection: -1,
	})
	after := bl.AddServiceTask("weiter", 3)
	end := bl.AddEndEvent()
	bl.Connect(begin, adhoc)
	bl.Connect(adhoc, after)
	bl.Connect(after, end)
	bl.PushScope(adhoc)
	tool := bl.AddServiceTask("ta", 3)
	bl.PopScope()
	bl.SetElementBpmnId(adhoc, "adhoc")
	bl.SetElementBpmnId(tool, "zinsen_holen")
	cp, err := bl.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// The agent answers straight away: no tool calls, one result variable.
	p.CompleteJobWithToolCalls(singleActivatableJob(t, h.store, compiler.AgentJobTypeIndex), nil,
		model.VariableValue{Name: "befund", Kind: model.VarString, Text: "1.33 %"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// The container is gone and the step after it is running — with the answer in reach.
	nextJob := singleActivatableJob(t, h.store, cp.ServiceTask(cp.Node(after).Detail).JobType)
	job, ok, err := h.store.GetJob(nextJob)
	if err != nil || !ok {
		t.Fatalf("GetJob: ok=%v err=%v", ok, err)
	}
	vars := toolScopeVariables(t, h.store, job.ElementInstanceKey)
	if got := vars["befund"].Text; got != "1.33 %" {
		t.Errorf("befund seen by the step after the ad-hoc = %q, want the agent's answer", got)
	}
	_ = end
}
