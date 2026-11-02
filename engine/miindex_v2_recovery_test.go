package engine_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// The state package names the loopCounter variable itself because it cannot
// import the engine; the two must be the same variable.
func TestMILoopCounterVariableMatchesEngine(t *testing.T) {
	if state.MILoopCounterVariable != engine.LoopCounterVariable {
		t.Fatalf("state reads %q, engine writes %q", state.MILoopCounterVariable, engine.LoopCounterVariable)
	}
}

// miSnapshot is everything the MI history API can read for one instance.
type miSnapshot struct {
	Bodies     []state.MIHistoryEntry
	Iterations map[uint64][]state.MIHistoryEntry
	ByState    map[uint64]map[string][]uint64
	WithInc    map[uint64][]uint64
	Incidents  map[uint64][]state.MIIncidentRecord
}

func miReadSnapshot(t *testing.T, s *state.Store, pi uint64) miSnapshot {
	t.Helper()
	snap := miSnapshot{
		Iterations: map[uint64][]state.MIHistoryEntry{},
		ByState:    map[uint64]map[string][]uint64{},
		WithInc:    map[uint64][]uint64{},
		Incidents:  map[uint64][]state.MIIncidentRecord{},
	}
	incidents := func(owner uint64) {
		if _, err := s.MIIncidents(pi, owner, state.MIMaxPageLimit, func(r state.MIIncidentRecord) error {
			snap.Incidents[owner] = append(snap.Incidents[owner], r)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	var walk func(parent uint64)
	walk = func(parent uint64) {
		var bodies []state.MIHistoryEntry
		if _, err := s.MIBodyPage(pi, state.MIBodyPageQuery{ParentIteration: parent, Limit: state.MIMaxPageLimit}, func(b state.MIHistoryEntry) error {
			bodies = append(bodies, b)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		for _, b := range bodies {
			snap.Bodies = append(snap.Bodies, b)
			incidents(b.ElementInstanceKey)
			page := func(q state.MIIterationQuery, fn func(state.MIHistoryEntry)) {
				q.Limit = state.MIMaxPageLimit
				if _, err := s.MIIterationPage(pi, b.ElementInstanceKey, b.ActivationPosition, q, func(e state.MIHistoryEntry) error {
					fn(e)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			page(state.MIIterationQuery{}, func(e state.MIHistoryEntry) {
				snap.Iterations[b.ElementInstanceKey] = append(snap.Iterations[b.ElementInstanceKey], e)
			})
			snap.ByState[b.ElementInstanceKey] = map[string][]uint64{}
			for _, st := range []state.MIHistoryState{state.MIHistoryActive, state.MIHistoryCompleted, state.MIHistoryTerminated} {
				page(state.MIIterationQuery{State: st}, func(e state.MIHistoryEntry) {
					snap.ByState[b.ElementInstanceKey][st.String()] = append(snap.ByState[b.ElementInstanceKey][st.String()], e.LoopCounter)
				})
			}
			page(state.MIIterationQuery{IncidentsOnly: true}, func(e state.MIHistoryEntry) {
				snap.WithInc[b.ElementInstanceKey] = append(snap.WithInc[b.ElementInstanceKey], e.LoopCounter)
			})
			for _, r := range snap.Iterations[b.ElementInstanceKey] {
				incidents(r.ElementInstanceKey)
				walk(r.ElementInstanceKey)
			}
		}
	}
	walk(0)
	return snap
}

// miReplayInto recovers the WAL in dir into a brand-new state store and returns it.
func miReplayInto(t *testing.T, dir string, cps ...*compiler.CompiledProcess) *state.Store {
	t.Helper()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() })
	fresh, err := state.Open(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fresh.Close() })
	replay := engine.New(1, log, fresh, &manualClock{})
	for _, cp := range cps {
		replay.Deploy(cp)
	}
	if err := replay.Recover(); err != nil {
		t.Fatal(err)
	}
	if ready, err := fresh.MIHistoryIndexReady(); err != nil || !ready {
		t.Fatalf("replayed index readiness: ready=%v err=%v", ready, err)
	}
	return fresh
}

func miRun(t *testing.T, p *engine.Processor) {
	t.Helper()
	if err := p.RunUntilIdle(); err != nil {
		t.Fatal(err)
	}
}

// TestMIIndexV2IncidentsTerminationReplayEqualsLive drives a parallel MI service
// task through an incident that is resolved, an incident left open, a completion
// condition that terminates the remaining iterations (silently clearing the open
// incident), and compares every MI history read after an independent WAL replay.
func TestMIIndexV2IncidentsTerminationReplayEqualsLive(t *testing.T) {
	dir := t.TempDir()
	h := openHarness(t, dir)
	b := compiler.NewBuilder(1, "mi-v2", 1)
	start := b.AddStartEvent()
	setup := b.AddScriptTask(mustCompile(t, "[1, 2, 3, 4, 5, 6]"), "items")
	work := b.AddServiceTask("v2work", 3)
	b.SetMultiInstance(work, false, "item", "", mustCompile(t, "items"), nil, nil, mustCompile(t, "loopCounter >= 5"))
	end := b.AddEndEvent()
	b.Connect(start, setup)
	b.Connect(setup, work)
	b.Connect(work, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	jobType := cp.ServiceTask(cp.Node(work).Detail).JobType
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatal(err)
	}
	p.CreateInstance(cp.Key)
	miRun(t, p)

	job2 := jobByLoopCounter(t, h, jobType, 2)
	j2, _, _ := h.store.GetJob(job2)
	pi := j2.ProcessInstanceKey
	p.FailJob(job2, 0, "boom", 0)
	miRun(t, p)
	p.ResolveIncident(j2.ElementInstanceKey, 1)
	miRun(t, p)
	for _, n := range []int{1, 2, 3} {
		p.CompleteJob(jobByLoopCounter(t, h, jobType, n))
		miRun(t, p)
	}
	job4 := jobByLoopCounter(t, h, jobType, 4)
	j4, _, _ := h.store.GetJob(job4)
	p.FailJob(job4, 0, "still failing", 0)
	miRun(t, p)

	open := miReadSnapshot(t, h.store, pi)
	if got := open.WithInc[open.Bodies[0].ElementInstanceKey]; !reflect.DeepEqual(got, []uint64{2, 4}) {
		t.Fatalf("iterations with incidents while running: %v", got)
	}
	if isOpen, err := h.store.IncidentOpen(j4.ElementInstanceKey); err != nil || !isOpen {
		t.Fatalf("iteration 4 incident not open: %v %v", isOpen, err)
	}

	p.CompleteJob(jobByLoopCounter(t, h, jobType, 5))
	miRun(t, p)

	live := miReadSnapshot(t, h.store, pi)
	if len(live.Bodies) != 1 {
		t.Fatalf("bodies: %+v", live.Bodies)
	}
	body := live.Bodies[0]
	if body.State != state.MIHistoryCompleted || body.Iterations != 6 || body.Completed != 4 || body.Terminated != 2 ||
		body.IncidentsRaised != 2 || body.IterationsWithIncidents != 2 || body.ParentIterationKey != 0 {
		t.Fatalf("body summary: %+v", body)
	}
	byState := live.ByState[body.ElementInstanceKey]
	if !reflect.DeepEqual(byState["completed"], []uint64{1, 2, 3, 5}) || !reflect.DeepEqual(byState["terminated"], []uint64{4, 6}) || len(byState["active"]) != 0 {
		t.Fatalf("state index: %+v", byState)
	}
	recs := live.Incidents[j4.ElementInstanceKey]
	if len(recs) != 1 || recs[0].Outcome != state.MIIncidentUnresolved {
		t.Fatalf("iteration 4 incident record: %+v", recs)
	}
	if isOpen, err := h.store.IncidentOpen(j4.ElementInstanceKey); err != nil || isOpen {
		t.Fatalf("terminated iteration still has an open incident: %v %v", isOpen, err)
	}
	if recs := live.Incidents[j2.ElementInstanceKey]; len(recs) != 1 || recs[0].Outcome != state.MIIncidentResolved {
		t.Fatalf("iteration 2 incident record: %+v", recs)
	}
	h.close(t)

	got := miReadSnapshot(t, miReplayInto(t, dir, cp), pi)
	if !reflect.DeepEqual(got, live) {
		t.Fatalf("MI history differs after WAL replay\nlive:   %+v\nreplay: %+v", live, got)
	}
}

// TestMIIndexV2NestedReplayEqualsLive runs a sequential MI subprocess whose
// iterations each contain a parallel MI service task, beside a second instance
// of the same process, and compares live and replayed reads: each nested body
// hangs under its own outer iteration, the two executions of the inner BPMN
// element stay apart, and the other instance never shows through.
func TestMIIndexV2NestedReplayEqualsLive(t *testing.T) {
	dir := t.TempDir()
	h := openHarness(t, dir)
	b := compiler.NewBuilder(1, "mi-nested", 1)
	start := b.AddStartEvent()
	setup := b.AddScriptTask(mustCompile(t, "[1, 2]"), "items")
	sub := b.AddSubProcess()
	b.SetMultiInstance(sub, true /*sequential*/, "outer", "", mustCompile(t, "items"), nil, nil, nil)
	b.PushScope(sub)
	istart := b.AddStartEvent()
	inner := b.AddServiceTask("nestedwork", 3)
	b.SetMultiInstance(inner, false, "x", "", mustCompile(t, "[10, 20, 30]"), nil, nil, nil)
	iend := b.AddEndEvent()
	b.Connect(istart, inner)
	b.Connect(inner, iend)
	b.PopScope()
	end := b.AddEndEvent()
	b.Connect(start, setup)
	b.Connect(setup, sub)
	b.Connect(sub, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	jobType := cp.ServiceTask(cp.Node(inner).Detail).JobType
	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatal(err)
	}
	p.CreateInstance(cp.Key)
	p.CreateInstance(cp.Key) // a second instance must stay invisible to the first
	miRun(t, p)
	var pi uint64
	for {
		jobs := activatableJobs(t, h.store, jobType)
		if len(jobs) == 0 {
			break
		}
		if pi == 0 {
			j, _, _ := h.store.GetJob(jobs[0])
			pi = j.ProcessInstanceKey
		}
		p.CompleteJob(jobs[0])
		miRun(t, p)
	}
	live := miReadSnapshot(t, h.store, pi)
	// One outer body, two outer iterations, each enclosing its own inner body
	// of three iterations.
	if len(live.Bodies) != 3 {
		t.Fatalf("bodies: %+v", live.Bodies)
	}
	outer := live.Bodies[0]
	if outer.ParentIterationKey != 0 || outer.Iterations != 2 || outer.Completed != 2 {
		t.Fatalf("outer body: %+v", outer)
	}
	rounds := live.Iterations[outer.ElementInstanceKey]
	if len(rounds) != 2 || rounds[0].LoopCounter != 1 || rounds[1].LoopCounter != 2 {
		t.Fatalf("outer iterations: %+v", rounds)
	}
	for i, nested := range live.Bodies[1:] {
		if nested.ParentIterationKey != rounds[i].ElementInstanceKey || nested.Iterations != 3 || nested.Completed != 3 {
			t.Fatalf("nested body %d: %+v (outer iteration %d)", i, nested, rounds[i].ElementInstanceKey)
		}
		var counters []uint64
		for _, r := range live.Iterations[nested.ElementInstanceKey] {
			counters = append(counters, r.LoopCounter)
		}
		if !reflect.DeepEqual(counters, []uint64{1, 2, 3}) {
			t.Fatalf("nested iterations %d: %v", i, counters)
		}
	}
	h.close(t)

	got := miReadSnapshot(t, miReplayInto(t, dir, cp), pi)
	if !reflect.DeepEqual(got, live) {
		t.Fatalf("MI history differs after WAL replay\nlive:   %+v\nreplay: %+v", live, got)
	}
}
