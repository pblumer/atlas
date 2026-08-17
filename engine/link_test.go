package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// linkProcess builds Start → linkThrow ⇢ linkCatch → End, wired by the compile-time synthetic
// goto (ADR-0133). It uses the raw builder API: b.AddLinkThrowEvent / b.AddLinkCatchEvent are
// bare pass-through nodes, and b.Connect(throw, catch) is the synthetic edge connectScope emits
// from the XML link names.
func linkProcess(t testing.TB, key uint64) (cp *compiler.CompiledProcess, throw, catch, end int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "linkgoto", 1)
	start := b.AddStartEvent()
	thr := b.AddLinkThrowEvent()
	cat := b.AddLinkCatchEvent()
	e := b.AddEndEvent()
	b.Connect(start, thr)
	b.Connect(thr, cat) // the synthetic goto
	b.Connect(cat, e)
	built, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return built, thr, cat, e
}

// TestLinkGotoRunsThroughCatch runs Start → linkThrow ⇢ linkCatch → End: the token flows
// through the throw, jumps to the catch, and reaches the end, visiting both link events (so the
// Operations overlay lights them up) and completing the instance (ADR-0133).
func TestLinkGotoRunsThroughCatch(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, throw, catch, end := linkProcess(t, 90)

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
		t.Fatalf("after link goto: process=%d element=%d, want 0 and 0 (ran to completion)", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[throw] != 1 {
		t.Errorf("link throw visits = %d, want 1 (the token passes through it)", v[throw])
	}
	if v[catch] != 1 {
		t.Errorf("link catch visits = %d, want 1 (the goto lands on it)", v[catch])
	}
	if v[end] != 1 {
		t.Errorf("end visits = %d, want 1 (the catch flows on to the end)", v[end])
	}
}

// TestLinkGotoAcrossJobRecovers parks on a job before the link throw, replays the log into a
// fresh store, then completes the job — the token then flows through the link throw ⇢ catch to
// the end. Proves the compile-time goto is present after recovery (the synthetic edge lives in
// the CompiledProcess, re-derived on Deploy) — ADR-0133, invariant I6.
func TestLinkGotoAcrossJobRecovers(t *testing.T) {
	dir := t.TempDir()

	b := compiler.NewBuilder(91, "linkgoto-rec", 1)
	start := b.AddStartEvent()
	work := b.AddServiceTask("work", 3)
	thr := b.AddLinkThrowEvent()
	cat := b.AddLinkCatchEvent()
	end := b.AddEndEvent()
	b.Connect(start, work)
	b.Connect(work, thr)
	b.Connect(thr, cat) // the synthetic goto
	b.Connect(cat, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	jobType := cp.ServiceTask(cp.Node(work).Detail).JobType

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
	// Parked on the job, before the link.
	if pi, ei := counts(t, h1.store); pi != 1 || ei != 1 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 1 (on the job)", pi, ei)
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
	// Completing the job drives the token through the link goto to the end.
	p2.CompleteJob(singleActivatableJob(t, store2, jobType))
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (after recovery): %v", err)
	}
	if pi, ei := counts(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after link goto post-recovery: process=%d element=%d, want 0 and 0", pi, ei)
	}
	v := elementVisits(t, store2, cp.Key)
	if v[cat] != 1 || v[end] != 1 {
		t.Errorf("post-recovery visits catch=%d end=%d, want 1 and 1 (goto worked after replay)", v[cat], v[end])
	}
}
