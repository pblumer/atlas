package engine_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// limitedAgent builds start → adhoc{zinsen_holen} → end with an agent-driven container
// carrying the given bounds, deploys it and runs one instance up to its first round job.
func limitedAgent(t *testing.T, h *harness, key uint64, id string, limits compiler.AgentLimits) (*engine.Processor, *compiler.CompiledProcess, int32) {
	t.Helper()
	bl := compiler.NewBuilder(key, id, 1)
	start := bl.AddStartEvent()
	adhoc := bl.AddAdHocSubProcess(compiler.AdHocDetail{
		CancelRemaining: true, AgentDriven: true, AgentWorker: -1, ResultCollection: -1, AgentLimits: limits,
	})
	end := bl.AddEndEvent()
	bl.Connect(start, adhoc)
	bl.Connect(adhoc, end)
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
	idle(t, p)
	return p, cp, tool
}

func idle(t *testing.T, p *engine.Processor) {
	t.Helper()
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
}

// agentRound plays one whole round: the model reports usage and calls the tool, the tool
// finishes, and the scope drains — which is where the engine decides what comes next.
func agentRound(t *testing.T, h *harness, p *engine.Processor, cp *compiler.CompiledProcess, tool int32, in, out uint64) {
	t.Helper()
	round := singleActivatableJob(t, h.store, compiler.AgentJobTypeIndex)
	p.CompleteJobWithAgentRun(round, &model.AgentRunValue{InputTokens: in, OutputTokens: out, Model: "m"},
		[]model.ToolCall{{Tool: "zinsen_holen", CallId: "c"}})
	idle(t, p)
	p.CompleteJob(singleActivatableJob(t, h.store, jobTypeOf(t, cp, tool)))
	idle(t, p)
}

func agentHistory(t *testing.T, s *state.Store) []model.AgentRunValue {
	t.Helper()
	var out []model.AgentRunValue
	if err := s.EachAgentRun(func(_ int64, _ uint64, v *model.AgentRunValue) error {
		out = append(out, *v)
		return nil
	}); err != nil {
		t.Fatalf("history: %v", err)
	}
	return out
}

func agentIncident(t *testing.T, s *state.Store) (uint64, model.IncidentValue, bool) {
	t.Helper()
	for k, inc := range incidents(t, s) {
		if inc.Reason == model.IncidentAgentLimit {
			return k, inc, true
		}
	}
	return 0, model.IncidentValue{}, false
}

// Every model call is on the record, numbered and summed by the engine and attached to
// the element the job was leased for — whatever the worker's account says about any of
// that, and also when the worker gives no account at all.
func TestEveryAgentRoundIsRecordedByTheEngineNotByTheWorker(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p, cp, tool := limitedAgent(t, h, 700, "agent-record", compiler.AgentLimits{})

	// Round 1: an account that lies about everything the engine states itself.
	round := singleActivatableJob(t, h.store, compiler.AgentJobTypeIndex)
	p.CompleteJobWithAgentRun(round, &model.AgentRunValue{
		ProcessInstanceKey: 1, ElementInstanceKey: 2, JobKey: 3, Round: 99, Kind: model.AgentRunToolResult,
		SumInputTokens: 1 << 40, Limit: model.AgentRunLimitTokens, ToolCalls: "forged", CallId: "forged",
		Model: "claude-opus-5-5", StopReason: "tool_use", InputTokens: 100, OutputTokens: 20, CacheReadTokens: 60,
		Prompt: "Goal: prüfe", Answer: "",
	}, []model.ToolCall{{Tool: "zinsen_holen", CallId: "call-1"}})
	idle(t, p)
	p.CompleteJob(singleActivatableJob(t, h.store, jobTypeOf(t, cp, tool)))
	idle(t, p)
	// Round 2: a worker that reports nothing has still asked a model.
	p.CompleteJobWithToolCalls(singleActivatableJob(t, h.store, compiler.AgentJobTypeIndex),
		[]model.ToolCall{{Tool: "zinsen_holen", CallId: "call-2"}})
	idle(t, p)
	p.CompleteJob(singleActivatableJob(t, h.store, jobTypeOf(t, cp, tool)))
	idle(t, p)
	// Round 3 answers, which ends the run.
	p.CompleteJobWithAgentRun(singleActivatableJob(t, h.store, compiler.AgentJobTypeIndex),
		&model.AgentRunValue{InputTokens: 300, OutputTokens: 40, Answer: "tragbar"}, nil)
	idle(t, p)

	all := agentHistory(t, h.store)
	var got, results []model.AgentRunValue
	for _, f := range all {
		if f.Kind == model.AgentRunToolResult {
			results = append(results, f)
		} else {
			got = append(got, f)
		}
	}
	if len(got) != 3 {
		t.Fatalf("history has %d model calls, want 3: %+v", len(got), all)
	}
	// What each tool returned is on the record too, paired with its call and its round,
	// under the container and naming the tool — and it moves no total.
	if len(results) != 2 || results[0].CallId != "call-1" || results[0].Round != 1 || results[1].CallId != "call-2" || results[1].Round != 2 ||
		results[0].ElementInstanceKey != got[0].ElementInstanceKey || results[0].ElementId != tool || results[0].JobKey != 0 {
		t.Errorf("tool results = %+v, want one per call, under the container", results)
	}
	first := got[0]
	if first.Round != 1 || first.Kind != model.AgentRunModelCall || first.JobKey != round ||
		first.ProcessInstanceKey == 1 || first.ElementInstanceKey == 2 || first.CallId != "" ||
		first.SumInputTokens != 100 || first.SumOutputTokens != 20 || first.Limit != model.AgentRunLimitNone {
		t.Errorf("round 1 = %+v: the engine's own fields must not come from the account", first)
	}
	if first.ToolCalls != `[{"tool":"zinsen_holen","callId":"call-1"}]` {
		t.Errorf("round 1 tool calls = %s", first.ToolCalls)
	}
	if first.Model != "claude-opus-5-5" || first.CacheReadTokens != 60 || first.Prompt != "Goal: prüfe" {
		t.Errorf("round 1 lost what only the worker knows: %+v", first)
	}
	if got[1].Round != 2 || got[1].SumInputTokens != 100 || got[1].InputTokens != 0 {
		t.Errorf("round 2 (unreported) = %+v, want it counted with no tokens of its own", got[1])
	}
	if got[2].Round != 3 || got[2].SumInputTokens != 400 || got[2].SumOutputTokens != 60 || got[2].ToolCalls != "" || got[2].Answer != "tragbar" {
		t.Errorf("round 3 = %+v, want the sums of the run and the answer", got[2])
	}
	if pi, _ := counts(t, h.store); pi != 0 {
		t.Error("the instance did not finish after the agent answered")
	}
}

// An operator forcing a round through asked no model anything, so it is not a round.
func TestAForcedAgentJobIsNotAModelCall(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p, _, _ := limitedAgent(t, h, 701, "agent-forced", compiler.AgentLimits{})
	p.CompleteJobManually(singleActivatableJob(t, h.store, compiler.AgentJobTypeIndex), "ops", "stuck")
	idle(t, p)
	if got := agentHistory(t, h.store); len(got) != 0 {
		t.Errorf("a forced completion left %d agent-run facts, want none", len(got))
	}
}

// maxIterations parks the container on an incident instead of asking again, resolving
// grants the same number once more, and an agent that answers on its last allowed round
// simply finishes.
func TestAnAgentParksAtItsRoundBoundAndResolvingGrantsItAgain(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p, cp, tool := limitedAgent(t, h, 702, "agent-rounds", compiler.AgentLimits{MaxRounds: 2})

	agentRound(t, h, p, cp, tool, 10, 1)
	if _, _, parked := agentIncident(t, h.store); parked {
		t.Fatal("parked after one round of two")
	}
	agentRound(t, h, p, cp, tool, 10, 1)
	key, inc, parked := agentIncident(t, h.store)
	if !parked || !strings.Contains(inc.Message, "ran 2 rounds") || !strings.Contains(inc.Message, "resolve to run 2 more") {
		t.Fatalf("after two rounds: incident %+v (parked=%v), want one naming the bound and the grant", inc, parked)
	}
	if !jobGone(t, h.store, compiler.AgentJobTypeIndex) {
		t.Fatal("a third round was asked for past maxIterations")
	}
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("parked: process=%d element=%d, want the container alone, still active", pi, ei)
	}

	p.ResolveIncident(key, 1)
	idle(t, p)
	if _, _, still := agentIncident(t, h.store); still {
		t.Fatal("the incident survived its resolution")
	}
	agentRound(t, h, p, cp, tool, 10, 1) // round 3 of the granted two
	if _, _, parked := agentIncident(t, h.store); parked {
		t.Fatal("parked one round into the second allowance")
	}
	// Round 4 is the last allowed one, and the agent uses it to answer.
	p.CompleteJobWithAgentRun(singleActivatableJob(t, h.store, compiler.AgentJobTypeIndex), nil, nil)
	idle(t, p)
	if _, _, parked := agentIncident(t, h.store); parked {
		t.Error("an agent that answered on its last round was parked anyway")
	}
	if pi, _ := counts(t, h.store); pi != 0 {
		t.Error("the instance did not finish")
	}
}

// A container that states no bound still has one.
func TestAnAgentThatStatesNoBoundParksAtTheSafeCeiling(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p, cp, tool := limitedAgent(t, h, 703, "agent-ceiling", compiler.AgentLimits{})
	for i := 1; i < compiler.SafeAgentRounds; i++ {
		agentRound(t, h, p, cp, tool, 1, 1)
	}
	if _, _, parked := agentIncident(t, h.store); parked {
		t.Fatalf("parked before round %d", compiler.SafeAgentRounds)
	}
	agentRound(t, h, p, cp, tool, 1, 1)
	_, inc, parked := agentIncident(t, h.store)
	if !parked || !strings.Contains(inc.Message, "states no maxIterations") {
		t.Fatalf("after %d rounds: %+v (parked=%v), want the ceiling's incident", compiler.SafeAgentRounds, inc, parked)
	}
}

// tokenBudget parks the run when its sum crosses the budget, and resolving lets it run
// to the next multiple rather than forever.
func TestAnAgentParksWhenItCrossesItsTokenBudget(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	p, cp, tool := limitedAgent(t, h, 704, "agent-budget", compiler.AgentLimits{MaxRounds: 100, TokenBudget: 1000})

	agentRound(t, h, p, cp, tool, 500, 100) // 600
	if _, _, parked := agentIncident(t, h.store); parked {
		t.Fatal("parked at 600 of 1000 tokens")
	}
	agentRound(t, h, p, cp, tool, 500, 100) // 1200
	key, inc, parked := agentIncident(t, h.store)
	if !parked || !strings.Contains(inc.Message, "used 1200 tokens") || !strings.Contains(inc.Message, "up to 2000") {
		t.Fatalf("at 1200 tokens: %+v (parked=%v), want the budget's incident", inc, parked)
	}
	p.ResolveIncident(key, 1)
	idle(t, p)
	agentRound(t, h, p, cp, tool, 500, 100) // 1800: inside the granted thousand
	if _, _, parked := agentIncident(t, h.store); parked {
		t.Fatal("parked at 1800, inside the granted budget")
	}
	agentRound(t, h, p, cp, tool, 500, 100) // 2400
	if _, _, parked := agentIncident(t, h.store); !parked {
		t.Fatal("not parked after crossing 2000")
	}
}

// State after replay equals state built live: the facts, the totals a bound is weighed
// against, and the parked container with its incident — and no model is asked again.
func TestAgentRunsAndAParkedAgentSurviveReplay(t *testing.T) {
	dir := t.TempDir()
	h := openHarness(t, dir)
	p, cp, tool := limitedAgent(t, h, 705, "agent-replay", compiler.AgentLimits{MaxRounds: 2})
	agentRound(t, h, p, cp, tool, 100, 10)
	agentRound(t, h, p, cp, tool, 200, 20)
	live := agentHistory(t, h.store)
	_, liveInc, parked := agentIncident(t, h.store)
	if !parked || len(live) != 4 { // two model calls, two tool results
		t.Fatalf("live: parked=%v, %d facts", parked, len(live))
	}
	h.close(t)

	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer func() { _ = store2.Close(); _ = log2.Close() }()
	p2 := engine.New(1, log2, store2, &manualClock{})
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover (replay): %v", err)
	}

	replayed := agentHistory(t, store2)
	if len(replayed) != len(live) {
		t.Fatalf("replayed history = %+v, want %+v", replayed, live)
	}
	for i := range live {
		if replayed[i] != live[i] {
			t.Errorf("replayed fact %d = %+v, want %+v", i, replayed[i], live[i])
		}
	}
	key, inc, parked := agentIncident(t, store2)
	if !parked || inc.Message != liveInc.Message {
		t.Fatalf("replayed incident = %+v (parked=%v), want %+v", inc, parked, liveInc)
	}
	if !jobGone(t, store2, compiler.AgentJobTypeIndex) {
		t.Error("replay asked the model again")
	}
	total, err := store2.AgentRunTotal(replayed[0].ProcessInstanceKey, replayed[0].ElementInstanceKey)
	if err != nil || total.Rounds != 2 || total.InputTokens != 300 || total.OutputTokens != 30 || total.Limit != model.AgentRunLimitRounds {
		t.Errorf("replayed total = %+v, %v", total, err)
	}
	// And the replayed engine carries on from there exactly as the live one would.
	p2.ResolveIncident(key, 1)
	idle(t, p2)
	if jobGone(t, store2, compiler.AgentJobTypeIndex) {
		t.Error("resolving the replayed incident asked for no round")
	}
}

// An ai task's one call is on the record too: round one, and no bound to reach.
func TestAnAiTaskRecordsItsOneCall(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	bl := compiler.NewBuilder(706, "ai-task", 1)
	start := bl.AddStartEvent()
	task := bl.AddAgentConnectorTask(compiler.AgentTaskConfig{Worker: "w", ResultVar: "antwort", Retries: 3})
	end := bl.AddEndEvent()
	bl.Connect(start, task)
	bl.Connect(task, end)
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
	idle(t, p)
	job := singleActivatableJob(t, h.store, compiler.AiTaskJobTypeIndex)
	p.CompleteJobWithAgentRun(job, &model.AgentRunValue{Model: "claude-haiku-5-5", InputTokens: 50, OutputTokens: 5, Answer: "ja"}, nil,
		model.VariableValue{Name: "antwort", Kind: model.VarString, Text: "ja"})
	idle(t, p)

	got := agentHistory(t, h.store)
	if len(got) != 1 || got[0].Round != 1 || got[0].JobKey != job || got[0].ElementId != task ||
		got[0].Model != "claude-haiku-5-5" || got[0].SumInputTokens != 50 || got[0].Limit != model.AgentRunLimitNone {
		t.Fatalf("history = %+v, want the task's one call", got)
	}
}
