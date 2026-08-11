package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
)

// eventGatewayProcess builds the request/timeout pattern: Start → event gateway "wait" that
// races a message catch ("reply", no correlation key) against a timer catch (30s). Each
// branch runs to its own end event so a test can tell which won by element visits (ADR-0110).
func eventGatewayProcess(t testing.TB, key uint64) (cp *compiler.CompiledProcess, replyEnd, timeoutEnd int32) {
	t.Helper()
	b := compiler.NewBuilder(key, "req", 1)
	start := b.AddStartEvent()
	wait := b.AddEventBasedGateway()
	reply := b.AddMessageCatchEvent("reply", nil)
	timeout := b.AddTimerCatchEvent(30e9)
	replyEnd = b.AddEndEvent()
	timeoutEnd = b.AddEndEvent()
	b.Connect(start, wait)
	b.Connect(wait, reply)
	b.Connect(wait, timeout)
	b.Connect(reply, replyEnd)
	b.Connect(timeout, timeoutEnd)
	built, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return built, replyEnd, timeoutEnd
}

// TestEventGatewayMessageWins: the message arrives before the timer — the message branch
// continues and the timer branch is cancelled; a later timer tick is a no-op (ADR-0110).
func TestEventGatewayMessageWins(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, replyEnd, timeoutEnd := eventGatewayProcess(t, 90)
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

	// The gateway armed both catches: two live element instances waiting in one instance.
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("armed race: process=%d element=%d, want 1 and 2 (message + timer catch)", pi, ei)
	}

	// The message wins.
	p.PublishMessage("reply", "")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after message won: process=%d element=%d, want 0 and 0 (loser cancelled)", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[replyEnd] != 1 {
		t.Errorf("message-branch end visits = %d, want 1 (message won)", v[replyEnd])
	}
	if v[timeoutEnd] != 0 {
		t.Errorf("timer-branch end visits = %d, want 0 (timer branch cancelled)", v[timeoutEnd])
	}

	// The cancelled timer self-retired: firing it now resurrects nothing.
	clk.t = 1_000 + 30e9 + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after stale timer tick: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestEventGatewayTimerWins: the timer elapses before any message — the timer branch
// continues and the message branch is cancelled; a later publish is a no-op (ADR-0110).
func TestEventGatewayTimerWins(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	cp, replyEnd, timeoutEnd := eventGatewayProcess(t, 91)
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
	if pi, ei := counts(t, h.store); pi != 1 || ei != 2 {
		t.Fatalf("armed race: process=%d element=%d, want 1 and 2", pi, ei)
	}

	// The timer wins.
	clk.t = 1_000 + 30e9 + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers: %v", err)
	}
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after timer won: process=%d element=%d, want 0 and 0 (loser cancelled)", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[timeoutEnd] != 1 {
		t.Errorf("timer-branch end visits = %d, want 1 (timer won)", v[timeoutEnd])
	}
	if v[replyEnd] != 0 {
		t.Errorf("message-branch end visits = %d, want 0 (message branch cancelled)", v[replyEnd])
	}

	// The cancelled message subscription self-retired: a matching publish now does nothing.
	p.PublishMessage("reply", "")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after stale publish: process=%d element=%d, want 0 and 0", pi, ei)
	}
}

// TestEventGatewayDoubleFireGuard: two branches waiting on the SAME message both correlate on
// one publish, in one batch — exactly one wins and the other, cancelled before its queued
// Completing runs, drops out cleanly (no double-fire, no counter corruption) (ADR-0110).
func TestEventGatewayDoubleFireGuard(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)

	b := compiler.NewBuilder(93, "dup", 1)
	start := b.AddStartEvent()
	wait := b.AddEventBasedGateway()
	replyA := b.AddMessageCatchEvent("reply", nil)
	replyB := b.AddMessageCatchEvent("reply", nil)
	endA := b.AddEndEvent()
	endB := b.AddEndEvent()
	b.Connect(start, wait)
	b.Connect(wait, replyA)
	b.Connect(wait, replyB)
	b.Connect(replyA, endA)
	b.Connect(replyB, endB)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	p := engine.New(1, h.log, h.store, &fixedClock{t: 1_000})
	p.Deploy(cp)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	p.CreateInstance(cp.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}

	// One publish matches both subscriptions in the same batch.
	p.PublishMessage("reply", "")
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if pi, ei := counts(t, h.store); pi != 0 || ei != 0 {
		t.Fatalf("after double correlate: process=%d element=%d, want 0 and 0", pi, ei)
	}
	v := elementVisits(t, h.store, cp.Key)
	if v[endA]+v[endB] != 1 {
		t.Errorf("branch-end visits endA=%d endB=%d, want exactly one (no double-fire)", v[endA], v[endB])
	}
}

// TestEventGatewayRecovers: the armed race survives a crash — the two catches, their
// subscription and timer, and their race-group label all rebuild from the log, so the first
// event to fire after restart still wins and cancels the loser (ADR-0110, invariant I4/I6).
func TestEventGatewayRecovers(t *testing.T) {
	dir := t.TempDir()
	cp, replyEnd, timeoutEnd := eventGatewayProcess(t, 92)
	clk := &fixedClock{t: 1_000}

	// First run: park with both branches armed.
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
	if pi, ei := counts(t, h1.store); pi != 1 || ei != 2 {
		t.Fatalf("before crash: process=%d element=%d, want 1 and 2", pi, ei)
	}

	// Crash and recover: the race must rebuild from the log.
	h1.close(t)
	h2 := openHarness(t, dir)
	defer h2.close(t)
	p2 := engine.New(1, h2.log, h2.store, clk)
	p2.Deploy(cp)
	if err := p2.Recover(); err != nil {
		t.Fatalf("Recover 2: %v", err)
	}
	if pi, ei := counts(t, h2.store); pi != 1 || ei != 2 {
		t.Fatalf("after replay: process=%d element=%d, want 1 and 2 (race rebuilt)", pi, ei)
	}

	// The message wins after restart → the timer branch is cancelled.
	p2.PublishMessage("reply", "")
	if err := p2.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle 2: %v", err)
	}
	if pi, ei := counts(t, h2.store); pi != 0 || ei != 0 {
		t.Fatalf("after recovered message won: process=%d element=%d, want 0 and 0", pi, ei)
	}
	v := elementVisits(t, h2.store, cp.Key)
	if v[replyEnd] != 1 || v[timeoutEnd] != 0 {
		t.Errorf("visits reply=%d timeout=%d, want 1 and 0 (message won after recovery)", v[replyEnd], v[timeoutEnd])
	}
}
