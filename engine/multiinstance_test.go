package engine_test

import (
	"sort"
	"strconv"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/expr"
)

// miServiceProcess builds start → setup(items = collection) → work → end, where work
// is a parallel multi-instance service task over items, binding item + loopCounter.
func miServiceProcess(t *testing.T, collection string) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(1, "mi", 1)
	start := b.AddStartEvent()
	setup := b.AddScriptTask(mustCompile(t, collection), "items")
	work := b.AddServiceTask("miwork", 3)
	b.SetMultiInstance(work, false /*parallel*/, "item", "", mustCompile(t, "items"), nil, nil, nil)
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

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("atoi(%q): %v", s, err)
	}
	return n
}

// TestMultiInstanceParallelFansOutAndJoins runs a parallel multi-instance service task
// over a three-element collection: it seeds one iteration per element (each bound its
// item and loopCounter), and the body completes only once every iteration's job is
// done, taking its single outgoing flow (ADR-0077 Phase 2).
func TestMultiInstanceParallelFansOutAndJoins(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := miServiceProcess(t, "[10, 20, 30]")

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	jobs := activatableJobs(t, h.store, jobType)
	if len(jobs) != 3 {
		t.Fatalf("multi-instance jobs = %d, want 3 (one per collection element)", len(jobs))
	}
	// The body plus its three parked iterations are the four active element instances.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 4 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 4 (body + 3 iterations)", pi, ei)
	}
	// Each iteration bound loopCounter (1..3) and its item (the collection value).
	var counters, items []int
	for _, jk := range jobs {
		job, ok, err := h.store.GetJob(jk)
		if err != nil || !ok {
			t.Fatalf("GetJob(%d): ok=%v err=%v", jk, ok, err)
		}
		inner := job.ElementInstanceKey
		lc := readVar(t, h.store, inner, "loopCounter")
		it := readVar(t, h.store, inner, "item")
		if lc == nil || it == nil {
			t.Fatalf("iteration %d missing loopCounter/item (%v/%v)", inner, lc, it)
		}
		counters = append(counters, atoi(t, lc.Text))
		items = append(items, atoi(t, it.Text))
	}
	sort.Ints(counters)
	sort.Ints(items)
	if len(counters) != 3 || counters[0] != 1 || counters[1] != 2 || counters[2] != 3 {
		t.Errorf("loopCounters = %v, want [1 2 3]", counters)
	}
	if len(items) != 3 || items[0] != 10 || items[1] != 20 || items[2] != 30 {
		t.Errorf("items = %v, want [10 20 30]", items)
	}

	// Complete every iteration's job; the body joins and the instance finishes.
	for _, jk := range jobs {
		p.CompleteJob(jk)
	}
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after completion: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if left := activatableJobs(t, h.store, jobType); len(left) != 0 {
		t.Errorf("leftover jobs = %d, want 0", len(left))
	}
}

// TestMultiInstanceEmptyCollection completes a multi-instance activity in one batch
// when its collection is empty: no iteration is seeded, the body takes its outgoing
// flow, and the instance finishes (ADR-0077).
func TestMultiInstanceEmptyCollection(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, jobType := miServiceProcess(t, "[]")

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if left := activatableJobs(t, h.store, jobType); len(left) != 0 {
		t.Fatalf("jobs = %d, want 0 (empty collection seeds no iteration)", len(left))
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after run: process=%d element=%d, want 0 and 0 (completed immediately)", pi, ei)
	}
}

// TestMultiInstanceCardinality runs a multi-instance activity by a fixed cardinality
// (no input collection): it seeds N iterations, each with a loopCounter but no input
// element, and joins when all complete (ADR-0077).
func TestMultiInstanceCardinality(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	b := compiler.NewBuilder(1, "mi-card", 1)
	start := b.AddStartEvent()
	work := b.AddServiceTask("cardwork", 3)
	b.SetMultiInstance(work, false, "", "", nil, mustCompile(t, "3"), nil, nil)
	end := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ServiceTask(cp.Node(work).Detail).JobType

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	jobs := activatableJobs(t, h.store, jobType)
	if len(jobs) != 3 {
		t.Fatalf("cardinality jobs = %d, want 3", len(jobs))
	}
	// Each iteration has a loopCounter but no input element.
	for _, jk := range jobs {
		job, ok, err := h.store.GetJob(jk)
		if err != nil || !ok {
			t.Fatalf("GetJob(%d): ok=%v err=%v", jk, ok, err)
		}
		if lc := readVar(t, h.store, job.ElementInstanceKey, "loopCounter"); lc == nil {
			t.Errorf("iteration %d missing loopCounter", job.ElementInstanceKey)
		}
	}
	for _, jk := range jobs {
		p.CompleteJob(jk)
	}
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after completion: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestMultiInstanceDegenerateCollections completes in one batch (no iteration) when
// the loop's collection is not a list or its cardinality is not a positive integer —
// the body seeds nothing and takes its outgoing flow rather than raising an error
// (ADR-0077).
func TestMultiInstanceDegenerateCollections(t *testing.T) {
	build := func(seq bool, coll, card *expr.Compiled) *compiler.CompiledProcess {
		b := compiler.NewBuilder(1, "mi-degen", 1)
		start := b.AddStartEvent()
		work := b.AddServiceTask("degenwork", 3)
		b.SetMultiInstance(work, seq, "item", "", coll, card, nil, nil)
		end := b.AddEndEvent()
		b.Connect(start, work)
		b.Connect(work, end)
		cp, err := b.Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		return cp
	}
	cases := map[string]*compiler.CompiledProcess{
		"non-list collection":  build(false, mustCompile(t, "42"), nil),    // a number, not a list
		"zero cardinality":     build(false, nil, mustCompile(t, "0")),     // no iterations
		"negative cardinality": build(false, nil, mustCompile(t, "0 - 5")), // guarded to none
	}
	for name, cp := range cases {
		t.Run(name, func(t *testing.T) {
			h := openHarness(t, t.TempDir())
			defer h.close(t)
			p := engine.New(1, h.log, h.store, &manualClock{})
			p.Deploy(cp)
			if err := p.Recover(); err != nil {
				t.Fatalf("Recover: %v", err)
			}
			p.CreateInstance(cp.Key)
			if err := p.RunUntilIdle(); err != nil {
				t.Fatalf("RunUntilIdle: %v", err)
			}
			if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
				t.Fatalf("%s: process=%d element=%d, want 0 and 0 (no iteration, completed)", name, pi, ei)
			}
		})
	}
}

// TestMultiInstanceRecovers parks a parallel multi-instance activity mid-flight (every
// iteration waiting on its job), crashes, and recovers: the body, its iterations, and
// the scope counter must rebuild from the log so completing the jobs still joins the
// body and finishes the instance (ADR-0077).
func TestMultiInstanceRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, jobType := miServiceProcess(t, "[1, 2, 3]")
	clk := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clk)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 1: %v", err)
	}
	if jobs := activatableJobs(t, h1.store, jobType); len(jobs) != 3 {
		t.Fatalf("before crash: jobs = %d, want 3", len(jobs))
	}
	h1.close(t)

	// Recover, then complete the recovered iterations.
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clk)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	jobs := activatableJobs(t, h2.store, jobType)
	if len(jobs) != 3 {
		t.Fatalf("after recovery: jobs = %d, want 3 (iterations rebuilt from the log)", len(jobs))
	}
	if pi, ei := counts(t, h2.store); pi != 1 || ei != 4 {
		t.Fatalf("after recovery: process=%d element=%d, want 1 and 4 (body + 3 iterations)", pi, ei)
	}
	for _, jk := range jobs {
		p2.CompleteJob(jk)
	}
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2: %v", err)
	}
	if pi, ei := counts(t, h2.store); pi != 0 || ei != 0 {
		t.Fatalf("after completion: process=%d element=%d, want 0 and 0", pi, ei)
	}
}
