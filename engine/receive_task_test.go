package engine_test

import (
	"path/filepath"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// receiveTaskProcess builds Start → receiveTask("reply", key "k") → End. The receive task
// parks on a message subscription until a correlating publish wakes it (ADR-0102).
func receiveTaskProcess(t testing.TB, key uint64) (cp *compiler.CompiledProcess, endEvent int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "recv", 1)
	start := b.AddStartEvent()
	recv := b.AddReceiveTask("reply", mustCompile(t, `"k"`))
	end := b.AddEndEvent()
	b.Connect(start, recv)
	b.Connect(recv, end)
	built, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return built, end
}

// TestReceiveTaskWaitsForMessage parks a token on a receive task and wakes it with a
// correlating publish, carrying the message payload into the instance's scope (ADR-0102).
func TestReceiveTaskWaitsForMessage(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, endEvent := receiveTaskProcess(t, 80)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Parked on the receive task, waiting for the message.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 1 (waiting receive task)", pi, ei)
	}
	instKey := activeInstanceOf(t, h.store, cp.Key)

	// A correlating publish wakes it and carries the payload into the instance scope.
	p.PublishMessage("reply", "k", model.VariableValue{Name: "answer", Kind: model.VarString, Text: "42"})
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (publish): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after publish: process=%d element=%d, want 0 and 0 (receive task continued to end)", pi, ei)
	}
	if v := elementVisits(t, h.store, cp.Key)[endEvent]; v != 1 {
		t.Errorf("end visits = %d, want 1 (the receive task continued on correlate)", v)
	}
	if got := readVar(t, h.store, instKey, "answer"); got == nil || got.Text != "42" {
		t.Errorf("payload answer = %+v, want \"42\" (message payload lands in the instance scope)", got)
	}
}

// TestReceiveTaskNonMatchingMessageIgnored proves a publish whose correlation key does not
// match leaves the receive task waiting (ADR-0102/ADR-0020).
func TestReceiveTaskNonMatchingMessageIgnored(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, _ := receiveTaskProcess(t, 81)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// A publish on the right name but the wrong key does not correlate.
	p.PublishMessage("reply", "other")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (publish): %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 1 || ei != 1 {
		t.Fatalf("after non-matching publish: process=%d element=%d, want 1 and 1 (still waiting)", pi, ei)
	}
}

// TestReceiveTaskBoundaryTimeout fires an interrupting timer boundary on a waiting receive
// task: the task is cancelled and the escalation flow runs — the "wait for a reply, else
// time out" pattern (ADR-0102, boundary events on an activity).
func TestReceiveTaskBoundaryTimeout(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const dur = int64(30e9)

	b := compiler.NewBuilder(82, "recv-timeout", 1)
	start := b.AddStartEvent()
	recv := b.AddReceiveTask("reply", mustCompile(t, `"k"`))
	timeout := b.AddBoundaryTimerEvent(recv, true, dur)
	done := b.AddEndEvent()
	escalated := b.AddEndEvent()
	b.Connect(start, recv)
	b.Connect(recv, done)
	b.Connect(timeout, escalated)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	clk := &fixedClock{t: 1_000}

	p := engine.New(1, h.log, h.store, clk)
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	// Parked: the receive task and its armed boundary timer.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("parked: process=%d element=%d, want 1 and 2 (receive task + armed boundary)", pi, ei)
	}

	// The timer fires before any message arrives: the receive task is interrupted.
	clk.t = 1_000 + dur + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after timeout: process=%d element=%d, want 0 and 0 (receive task interrupted, escalated)", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[escalated] != 1 || v[done] != 0 {
		t.Errorf("visits escalated=%d done=%d, want 1 and 0", v[escalated], v[done])
	}
}

// TestReceiveTaskRecovers proves a parked receive task's subscription survives a crash:
// after replaying the log into a fresh store the task is still waiting, and a publish after
// recovery correlates it and completes the instance (ADR-0102, invariant I4).
func TestReceiveTaskRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, endEvent := receiveTaskProcess(t, 83)

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
	h1.close(t)

	// Replay into a fresh, empty store: the subscription must rebuild from the log.
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
	if pi, ei := counts(t, store2); pi != 1 || ei != 1 {
		t.Fatalf("after replay: process=%d element=%d, want 1 and 1 (subscription rebuilt)", pi, ei)
	}
	// A publish now correlates the recovered subscription and finishes the instance.
	p2.PublishMessage("reply", "k")
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (publish after recovery): %v", err)
	}
	if pi, ei := counts(t, store2); pi != 0 || ei != 0 {
		t.Fatalf("after publish: process=%d element=%d, want 0 and 0", pi, ei)
	}
	if v := elementVisits(t, store2, cp.Key)[endEvent]; v != 1 {
		t.Errorf("end visits = %d, want 1 (recovered receive task correlated)", v)
	}
}
