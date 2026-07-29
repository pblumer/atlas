package engine_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// TestRuntimeCountersAgreeAndRecover proves the ADR-0080 runtime aggregate
// counters are correct two ways: the maintained per-definition instance counter
// and per-element live-token counter agree with an authoritative full scan of the
// state, and they rebuild identically after a crash and recovery (the applyToState
// fold runs the same live and on replay — invariant I4).
func TestRuntimeCountersAgreeAndRecover(t *testing.T) {
	dir := t.TempDir()
	cp, _ := businessRuleProcess(t) // each instance parks at a business-rule job
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	for i := 0; i < 3; i++ {
		p1.CreateInstance(cp.Key)
	}
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	assertCountersAgree(t, h1.store, cp.Key, 3)
	liveBefore := defTokenMap(t, h1.store, cp.Key)
	if len(liveBefore) == 0 {
		t.Fatal("expected live tokens on the parked element")
	}
	h1.close(t)

	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	assertCountersAgree(t, h2.store, cp.Key, 3)
	if got := defTokenMap(t, h2.store, cp.Key); !reflect.DeepEqual(got, liveBefore) {
		t.Fatalf("live tokens after recovery = %v, want %v", got, liveBefore)
	}
}

// assertCountersAgree checks the maintained counters equal an authoritative scan
// of the state for one definition, and that the instance count is wantInstances.
func assertCountersAgree(t *testing.T, store *state.Store, def uint64, wantInstances int) {
	t.Helper()
	scanned := 0
	if err := store.ActiveProcessInstances(func(_ uint64, v *model.ProcessInstanceValue) error {
		if v.ProcessDefKey == def {
			scanned++
		}
		return nil
	}); err != nil {
		t.Fatalf("scan instances: %v", err)
	}
	counter, err := store.DefInstanceCount(def)
	if err != nil {
		t.Fatalf("DefInstanceCount: %v", err)
	}
	if counter != scanned || counter != wantInstances {
		t.Fatalf("def instance count: counter=%d scan=%d want=%d", counter, scanned, wantInstances)
	}

	scanTokens := map[int32]int64{}
	if err := store.ActiveElementInstances(func(_ uint64, v *model.ElementInstanceValue) error {
		if v.ProcessDefKey == def {
			scanTokens[v.ElementId]++
		}
		return nil
	}); err != nil {
		t.Fatalf("scan element instances: %v", err)
	}
	if got := defTokenMap(t, store, def); !reflect.DeepEqual(got, scanTokens) {
		t.Fatalf("live-token counter=%v disagrees with scan=%v", got, scanTokens)
	}
}

func defTokenMap(t *testing.T, store *state.Store, def uint64) map[int32]int64 {
	t.Helper()
	m := map[int32]int64{}
	if err := store.ElementLiveTokens(def, func(el int32, c int64) error {
		if c != 0 {
			m[el] = c
		}
		return nil
	}); err != nil {
		t.Fatalf("ElementLiveTokens: %v", err)
	}
	return m
}

// TestRuntimeCountersReturnToZeroOnCompletion covers the counters' decrement path
// (ADR-0080): a process that runs to completion leaves no live instances or tokens,
// while the cumulative visit heatmap is retained.
func TestRuntimeCountersReturnToZeroOnCompletion(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp := scriptProcess(t, "1 < 2", "flag") // start → script → end, runs to completion

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	if n, err := h.store.DefInstanceCount(cp.Key); err != nil || n != 0 {
		t.Fatalf("def instance count after completion = %d (err %v), want 0", n, err)
	}
	if got := defTokenMap(t, h.store, cp.Key); len(got) != 0 {
		t.Fatalf("live tokens after completion = %v, want none", got)
	}
	visits := 0
	if err := h.store.ElementVisitTotals(cp.Key, func(_ int32, c int64) error {
		if c > 0 {
			visits++
		}
		return nil
	}); err != nil {
		t.Fatalf("ElementVisitTotals: %v", err)
	}
	if visits == 0 {
		t.Fatal("visit heatmap should persist after completion")
	}
}

// TestSummaryCountersCompletionAndRecovery covers the ADR-0083 summary counters: a
// completing instance bumps its definition's finished count and last-activity, and
// both rebuild identically when the log is replayed into a fresh store (I4/I6).
func TestSummaryCountersCompletionAndRecovery(t *testing.T) {
	dir := t.TempDir()
	cp := scriptProcess(t, "1 < 2", "flag") // start → script → end, runs to completion
	clock := &manualClock{}

	h1 := openHarness(t, dir)
	p1 := engine.New(1, h1.log, h1.store, clock)
	p1.Deploy(cp)
	if err := p1.Recover(); err != nil {
		t.Fatalf("Recover 1: %v", err)
	}
	p1.CreateInstance(cp.Key)
	if err := p1.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	if n, err := h1.store.DefCompletedCount(cp.Key); err != nil || n != 1 {
		t.Fatalf("finished count after completion = %d (err %v), want 1", n, err)
	}
	act, err := h1.store.DefLastActivity(cp.Key)
	if err != nil || act == 0 {
		t.Fatalf("last activity = %d (err %v), want > 0", act, err)
	}
	h1.close(t)

	// Replay the log into a fresh, empty store: the counters rebuild from the events.
	log2, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open 2: %v", err)
	}
	store2, err := state.Open(filepath.Join(dir, "state2"))
	if err != nil {
		t.Fatalf("state.Open 2: %v", err)
	}
	defer func() { _ = store2.Close(); _ = log2.Close() }()
	p2 := engine.New(1, log2, store2, clock)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2 (replay): %v", err)
	}
	if n, _ := store2.DefCompletedCount(cp.Key); n != 1 {
		t.Fatalf("replayed finished count = %d, want 1", n)
	}
	if a, _ := store2.DefLastActivity(cp.Key); a != act {
		t.Fatalf("replayed last activity = %d, want %d", a, act)
	}
}
