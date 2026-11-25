package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
)

// recheckProcess builds start → inclusive split → service tasks feeding an inclusive
// join, plus service task "B" whose exclusive "route" sends it to an end event by
// default; its conditional flow to the join is false. arrivals names the service tasks
// that reach the join directly. noRoute makes the join's only outgoing flow a false
// condition, so a firing cannot route. extra names service tasks that end at their own
// end event. Returns the process and the job type of the task after the join.
func recheckProcess(t *testing.T, arrivals []string, noRoute bool, extra ...string) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(defKey, "join-recheck", 1)
	start := b.AddStartEvent()
	split := b.AddInclusiveGateway()
	join := b.AddInclusiveGateway()
	b.Connect(start, split)
	for _, name := range arrivals {
		task := b.AddServiceTask(name, 3)
		b.Connect(split, task)
		b.Connect(task, join)
	}
	tb := b.AddServiceTask("B", 3)
	route := b.AddExclusiveGateway()
	elsewhere := b.AddEndEvent()
	b.Connect(split, tb)
	b.Connect(tb, route)
	b.SetFlowDefault(b.Connect(route, elsewhere))
	b.SetFlowCondition(b.Connect(route, join), mustCompile(t, "false"))
	for _, name := range extra {
		task := b.AddServiceTask(name, 3)
		b.Connect(split, task)
		b.Connect(task, b.AddEndEvent())
	}
	after := b.AddServiceTask("after", 3)
	out := b.Connect(join, after)
	if noRoute {
		b.SetFlowCondition(out, mustCompile(t, "false"))
	}
	b.Connect(after, b.AddEndEvent())
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, cp.ServiceTask(cp.Node(after).Detail).JobType
}

func startRecheck(t *testing.T, cp *compiler.CompiledProcess) (*harness, *engine.Processor) {
	t.Helper()
	h := openHarness(t, t.TempDir())
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	return h, p
}

func completeTask(t *testing.T, h *harness, p *engine.Processor, cp *compiler.CompiledProcess, name string) {
	t.Helper()
	jobType := cp.ServiceTask(cp.Node(branchNode(t, cp, name)).Detail).JobType
	jobs := activatableJobs(t, h.store, jobType)
	if len(jobs) != 1 {
		t.Fatalf("%s: %d activatable jobs, want 1", name, len(jobs))
	}
	p.CompleteJob(jobs[0])
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (%s): %v", name, err)
	}
}

// TestInclusiveJoinFiresWhenItsOtherBranchEndsElsewhere: A parks at the join while B can
// still reach it through route. B then ends at another end event; nothing can reach the
// join any more, so it fires once (ADR-0551 part 1).
func TestInclusiveJoinFiresWhenItsOtherBranchEndsElsewhere(t *testing.T) {
	cp, afterJob := recheckProcess(t, []string{"A"}, false)
	h, p := startRecheck(t, cp)
	defer h.close(t)
	completeTask(t, h, p, cp, "A")
	if n := len(activatableJobs(t, h.store, afterJob)); n != 0 {
		t.Fatalf("after A: %d after jobs, want 0 (B can still reach the join)", n)
	}
	completeTask(t, h, p, cp, "B")
	if n := len(activatableJobs(t, h.store, afterJob)); n != 1 {
		t.Fatalf("after B ended elsewhere: %d after jobs, want exactly 1", n)
	}
}

// TestInclusiveJoinRecheckFiresOnceForSeveralParkedArrivals: two arrivals wait on the
// join; the re-check fires it once and consumes both.
func TestInclusiveJoinRecheckFiresOnceForSeveralParkedArrivals(t *testing.T) {
	cp, afterJob := recheckProcess(t, []string{"A1", "A2"}, false)
	h, p := startRecheck(t, cp)
	defer h.close(t)
	completeTask(t, h, p, cp, "A1")
	completeTask(t, h, p, cp, "A2")
	if n := len(activatableJobs(t, h.store, afterJob)); n != 0 {
		t.Fatalf("after A1 and A2: %d after jobs, want 0", n)
	}
	completeTask(t, h, p, cp, "B")
	if n := len(activatableJobs(t, h.store, afterJob)); n != 1 {
		t.Fatalf("%d after jobs, want exactly 1: one firing for both parked arrivals", n)
	}
}

// TestInclusiveJoinRecheckLeavesARoutingIncidentAlone: the join fires when B ends
// elsewhere but cannot route, so it parks on an incident (ADR-0273). C ending later
// re-checks the join; the incident must stay the one raised for B, not be raised again.
func TestInclusiveJoinRecheckLeavesARoutingIncidentAlone(t *testing.T) {
	cp, _ := recheckProcess(t, []string{"A"}, true, "C")
	h, p := startRecheck(t, cp)
	defer h.close(t)
	completeTask(t, h, p, cp, "A")
	completeTask(t, h, p, cp, "B")
	first := incidents(t, h.store)
	if len(first) != 1 {
		t.Fatalf("after B: %d incidents, want 1 (the join cannot route)", len(first))
	}
	completeTask(t, h, p, cp, "C")
	again := incidents(t, h.store)
	if len(again) != 1 {
		t.Fatalf("after C: %d incidents, want still 1", len(again))
	}
	for k, inc := range first {
		if again[k].RaisedAt != inc.RaisedAt {
			t.Fatalf("the re-check raised the incident again: RaisedAt %d, then %d", inc.RaisedAt, again[k].RaisedAt)
		}
	}
}
