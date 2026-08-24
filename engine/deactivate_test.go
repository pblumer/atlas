package engine_test

import (
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/engine"
)

// TestTimerStartInactiveDoesNotInstantiate proves a deactivated definition's start
// timer keeps ticking and re-arming but creates no instance while inactive, and
// resumes instantiating once reactivated (ADR-0119).
func TestTimerStartInactiveDoesNotInstantiate(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	const interval = int64(10e9)
	// An infinite cycle (R without a count) so the timer re-arms on every fire.
	cp := timerStartProcess(t, 800, "cron", compiler.TimerSchedule{Kind: compiler.TimerCycleInterval, BaseNanos: interval, Repetitions: -1})
	clk := &fixedClock{t: 1_000}

	p := engine.New(1, h.log, h.store, clk)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	armDeployed(t, p, cp)

	// Deactivate before the timer comes due.
	p.SetProcessActive(cp.Key, false)
	if p.ProcessActive(cp.Key) {
		t.Fatal("ProcessActive after deactivate = true, want false")
	}

	// Two fires while inactive: the timer stays armed (re-anchored each fire) but no
	// instance is ever created.
	for fire := 1; fire <= 2; fire++ {
		clk.t += interval + 1
		if err := p.TickTimers(); err != nil {
			t.Fatalf("TickTimers (inactive fire %d): %v", fire, err)
		}
		if got := activeProcs(t, h.store); got != 0 {
			t.Fatalf("inactive fire %d: active=%d, want 0 (deactivated, must not start)", fire, got)
		}
		if got := countStartTimers(t, h.store); got != 1 {
			t.Fatalf("inactive fire %d: armed timers=%d, want 1 (timer keeps re-arming)", fire, got)
		}
	}

	// Reactivate: the next fire instantiates as usual.
	p.SetProcessActive(cp.Key, true)
	if !p.ProcessActive(cp.Key) {
		t.Fatal("ProcessActive after reactivate = false, want true")
	}
	clk.t += interval + 1
	if err := p.TickTimers(); err != nil {
		t.Fatalf("TickTimers (reactivated): %v", err)
	}
	if got := len(completedInstances(t, h.store)); got != 1 {
		t.Fatalf("after reactivate: completed=%d, want 1 (start resumes)", got)
	}
}

// TestMessageStartInactiveDoesNotInstantiate proves a deactivated definition does
// not start on a correlating message, and starts again once reactivated (ADR-0119).
func TestMessageStartInactiveDoesNotInstantiate(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	responder := parkingResponder(t, 801)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(responder)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	p.SetProcessActive(responder.Key, false)
	p.PublishMessage("request", "", numVar("orderId", "42"))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (inactive): %v", err)
	}
	if got := activeProcs(t, h.store); got != 0 {
		t.Fatalf("after message to inactive responder: active=%d, want 0", got)
	}

	// Reactivate: a message now starts an instance.
	p.SetProcessActive(responder.Key, true)
	p.PublishMessage("request", "", numVar("orderId", "43"))
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (reactivated): %v", err)
	}
	if got := activeProcs(t, h.store); got != 1 {
		t.Fatalf("after message to reactivated responder: active=%d, want 1", got)
	}
}

// TestSignalStartInactiveDoesNotInstantiate proves a deactivated signal-start
// definition is skipped by a broadcast, while other listeners are unaffected
// (ADR-0119).
func TestSignalStartInactiveDoesNotInstantiate(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	onSignal, startEnd := signalStartProc(t, 802)
	thrower := signalThrower(t, 803)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(onSignal)
	p.Deploy(thrower)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	p.SetProcessActive(onSignal.Key, false)
	p.CreateInstance(thrower.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (inactive broadcast): %v", err)
	}
	if v := elementVisits(t, h.store, onSignal.Key); v[startEnd] != 0 {
		t.Fatalf("onSignal end visits = %d, want 0 (deactivated, must not start)", v[startEnd])
	}

	// Reactivate: a fresh broadcast now instantiates it.
	p.SetProcessActive(onSignal.Key, true)
	p.CreateInstance(thrower.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle (reactivated broadcast): %v", err)
	}
	if v := elementVisits(t, h.store, onSignal.Key); v[startEnd] != 1 {
		t.Fatalf("onSignal end visits = %d, want 1 (reactivated, broadcast starts it)", v[startEnd])
	}
}

// TestManualStartUnaffectedByDeactivation proves deactivation gates only the
// automatic start triggers: an explicit CreateInstance (the API/operator start
// path) still runs while the definition is inactive (ADR-0119).
func TestManualStartUnaffectedByDeactivation(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	responder := parkingResponder(t, 804)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(responder)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	p.SetProcessActive(responder.Key, false)
	p.CreateInstance(responder.Key)
	if err := p.RunUntilIdle(); err != nil {
		t.Fatalf("RunUntilIdle: %v", err)
	}
	if got := activeProcs(t, h.store); got != 1 {
		t.Fatalf("explicit start of inactive definition: active=%d, want 1 (manual start is not gated)", got)
	}
}

// TestUndeployClearsDeactivation proves undeploying a definition forgets its
// deactivation, so ProcessActive reports the default (active) for the key again
// (ADR-0119).
func TestUndeployClearsDeactivation(t *testing.T) {
	h := openHarness(t, t.TempDir())
	defer h.close(t)
	responder := parkingResponder(t, 805)

	p := engine.New(1, h.log, h.store, &manualClock{})
	p.Deploy(responder)
	if err := p.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}

	p.SetProcessActive(responder.Key, false)
	if p.ProcessActive(responder.Key) {
		t.Fatal("ProcessActive after deactivate = true, want false")
	}
	p.Undeploy(responder.Key)
	if !p.ProcessActive(responder.Key) {
		t.Fatal("ProcessActive after undeploy = false, want true (deactivation cleared)")
	}
}
