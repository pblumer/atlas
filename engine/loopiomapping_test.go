package engine_test

import (
	"strconv"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
	"github.com/pblumer/atlas/model"
)

// A looping activity that also carries a zeebe:ioMapping keeps an activity-local
// scope (ADR-0068) — and that is the very scope the engine binds the loop's own
// loopCounter and item into (ADR-0077/ADR-0133). These tests pin that the two
// coexist: the mapped locals are there for the run, and the loop's bookkeeping
// survives long enough for the loop to know which run just finished. They are driven
// one job at a time under a hard round limit, so a loop that has lost count fails the
// test instead of hanging it.

// stdLoopIOProcess builds start → work → end, where work is a service task carrying a
// standard loop (repeat while cond, at most max — either may be empty/zero) and one
// input mapping, task_counter = loopCounter.
func stdLoopIOProcess(t *testing.T, cond string, max int32) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(1, "loop-io", 1)
	start := b.AddStartEvent()
	work := b.AddServiceTask("loopiowork", 3)
	var compiled *expr.Compiled
	if cond != "" {
		compiled = mustCompile(t, cond)
	}
	b.SetStandardLoop(work, false, max, compiled)
	b.AddInputMapping(work, "task_counter", mustCompile(t, "loopCounter"))
	end := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ServiceTask(cp.Node(work).Detail).JobType
}

// seqMIIOProcess builds start → setup(items = [10, 20, 30]) → work → end, where work is
// a *sequential* multi-instance service task over items with one input mapping,
// task_item = item.
func seqMIIOProcess(t *testing.T) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(1, "mi-io", 1)
	start := b.AddStartEvent()
	setup := b.AddScriptTask(mustCompile(t, "[10, 20, 30]"), "items")
	work := b.AddServiceTask("miiowork", 3)
	b.SetMultiInstance(work, true /*sequential*/, "item", "", mustCompile(t, "items"), nil, nil, nil)
	b.AddInputMapping(work, "task_item", mustCompile(t, "item"))
	end := b.AddEndEvent()
	b.Connect(start, setup)
	b.Connect(setup, work)
	b.Connect(work, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ServiceTask(cp.Node(work).Detail).JobType
}

// runLoopRounds starts one instance of cp and completes the loop's job one round at a
// time — calling each with the round's job key first, and completing the job with the
// outputs it returns — until no job is left or limit rounds have run, and reports how
// many rounds happened. The limit is the point: a loop that cannot tell one run from the
// next never ends, and that must surface as a failed assertion rather than a hung test.
func runLoopRounds(t *testing.T, h *harness, cp *compiler.CompiledProcess, jobType int32, limit int, each func(round int, jobKey uint64) []model.VariableValue) int {
	t.Helper()
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	rounds := 0
	for rounds < limit {
		jobs := activatableJobs(t, h.store, jobType)
		if len(jobs) == 0 {
			break
		}
		if len(jobs) != 1 {
			t.Fatalf("round %d: live jobs = %d, want 1 (a loop runs one at a time)", rounds+1, len(jobs))
		}
		rounds++
		p.CompleteJob(jobs[0], each(rounds, jobs[0])...)
		if err := p.RunUntilIdle(); err != nil {
			t.Fatalf("RunUntilIdle: %v", err)
		}
	}
	return rounds
}

// iterationVar reads one variable of the element instance a job belongs to — the
// iteration's own scope, which holds both the loop's bindings and the input-mapped
// locals while the run is live.
func iterationVar(t *testing.T, h *harness, jobKey uint64, name string) int {
	t.Helper()
	job, ok, err := h.store.GetJob(jobKey)
	if err != nil || !ok {
		t.Fatalf("GetJob(%d): ok=%v err=%v", jobKey, ok, err)
	}
	v := readVar(t, h.store, job.ElementInstanceKey, name)
	if v == nil {
		t.Fatalf("iteration %d has no %s", job.ElementInstanceKey, name)
	}
	return atoi(t, v.Text)
}

// TestStandardLoopWithInputMappingHonoursMaximum checks that loopMaximum still stops a
// loop on an activity that also has an input mapping. The mapping gives the activity a
// local scope, dropped when it completes (ADR-0068); dropping it before the loop reads
// the finished iteration's loopCounter would make every run look like the first, so the
// cap would never be reached and the loop would run forever (ADR-0133).
func TestStandardLoopWithInputMappingHonoursMaximum(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := stdLoopIOProcess(t, "", 3)

	var counters, mapped []int
	rounds := runLoopRounds(t, h, cp, jobType, 10, func(round int, jobKey uint64) []model.VariableValue {
		counters = append(counters, iterationVar(t, h, jobKey, "loopCounter"))
		mapped = append(mapped, iterationVar(t, h, jobKey, "task_counter"))
		return nil
	})
	if rounds != 3 {
		t.Fatalf("rounds = %d, want 3 (capped by loopMaximum)", rounds)
	}
	if want := []int{1, 2, 3}; !equalInts(counters, want) {
		t.Errorf("loopCounters = %v, want %v (each run counts up)", counters, want)
	}
	if want := []int{1, 2, 3}; !equalInts(mapped, want) {
		t.Errorf("input-mapped task_counter = %v, want %v (the mapping sees its own run's counter)", mapped, want)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Errorf("after the loop: process=%d element=%d, want 0 and 0 (the loop ended and the instance finished)", pi, ei)
	}
}

// TestStandardLoopWithInputMappingCountsUp is the same coexistence seen from the
// condition side: the loop's FEEL is evaluated over the finished iteration's scope, so
// it reads that run's loopCounter even when an input mapping shares the scope.
func TestStandardLoopWithInputMappingCountsUp(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := stdLoopIOProcess(t, "loopCounter < 3", 0)

	rounds := runLoopRounds(t, h, cp, jobType, 10, func(round int, jobKey uint64) []model.VariableValue {
		if got := iterationVar(t, h, jobKey, "loopCounter"); got != round {
			t.Errorf("round %d: loopCounter = %d, want %d", round, got, round)
		}
		return nil
	})
	if rounds != 3 {
		t.Fatalf("rounds = %d, want 3 (repeat while loopCounter < 3)", rounds)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Errorf("after the loop: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestSequentialMultiInstanceWithInputMappingAdvancesItems checks the same scope rule on
// the other loop marker: a sequential multi-instance whose activity has an input mapping
// still walks its collection one item per iteration, rather than repeating one item
// forever because the loopCounter that says where it is was dropped (ADR-0077).
func TestSequentialMultiInstanceWithInputMappingAdvancesItems(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := seqMIIOProcess(t)

	var items, mapped []int
	rounds := runLoopRounds(t, h, cp, jobType, 10, func(round int, jobKey uint64) []model.VariableValue {
		items = append(items, iterationVar(t, h, jobKey, "item"))
		mapped = append(mapped, iterationVar(t, h, jobKey, "task_item"))
		return nil
	})
	if rounds != 3 {
		t.Fatalf("rounds = %d, want 3 (one per collection element)", rounds)
	}
	if want := []int{10, 20, 30}; !equalInts(items, want) {
		t.Errorf("items = %v, want %v (each iteration binds the next element)", items, want)
	}
	if want := []int{10, 20, 30}; !equalInts(mapped, want) {
		t.Errorf("input-mapped task_item = %v, want %v", mapped, want)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Errorf("after the loop: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// equalInts reports whether two int slices hold the same values in the same order.
func equalInts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestStandardLoopWithInputMappingPromotesWhatItWrote checks the other half of the
// scope rule, on the loop's body: what the runs produced is held on the body scope so
// the next run can read it, and promoted to the enclosing scope when the loop ends
// (ADR-0133). An input mapping on the same activity must not cost the loop that result
// — the body scope would otherwise be dropped before it is promoted.
func TestStandardLoopWithInputMappingPromotesWhatItWrote(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := stdLoopIOProcess(t, "", 3)

	rounds := runLoopRounds(t, h, cp, jobType, 10, func(round int, jobKey uint64) []model.VariableValue {
		// What the worker reports: the run's own tally, which lands on the body scope
		// because the activity has no output mapping of its own.
		return []model.VariableValue{{Name: "total", Kind: model.VarNumber, Text: strconv.Itoa(round)}}
	})
	if rounds != 3 {
		t.Fatalf("rounds = %d, want 3 (capped by loopMaximum)", rounds)
	}
	snaps := variableSnapshots(t, h.store, instanceKey(t, h))
	if len(snaps) == 0 || snaps[len(snaps)-1] != "total=3" {
		t.Errorf("instance-scope variable history = %v, want it to end at total=3 (the last run's result escapes the loop)", snaps)
	}
}
