package runloop

import (
	"context"
	"testing"
	"time"
)

// TestProbeRunsTheWorkOnTheLoop is the base case: the work runs, and the probe reports
// a wait that is a duration rather than garbage from a zero time.
func TestProbeRunsTheWorkOnTheLoop(t *testing.T) {
	l, stop := start(t)
	defer stop()

	ran := false
	waited, ok := l.Probe(context.Background(), 2*time.Second, func() { ran = true })
	if !ok {
		t.Fatal("Probe on a running loop = not ok, want ok")
	}
	if !ran {
		t.Error("the probe's work did not run")
	}
	if waited < 0 || waited > 2*time.Second {
		t.Errorf("waited = %s, want a duration within the deadline", waited)
	}
}

// TestProbeMeasuresTheQueueAheadOfIt is what the wait half is for: a probe dispatched
// while a long turn holds the writer reports roughly the rest of that turn.
func TestProbeMeasuresTheQueueAheadOfIt(t *testing.T) {
	l, stop := start(t)
	defer stop()

	const hold = 80 * time.Millisecond
	occupied := make(chan struct{})
	go l.Do(func() { close(occupied); time.Sleep(hold) })
	<-occupied

	waited, ok := l.Probe(context.Background(), 5*time.Second, nil)
	if !ok {
		t.Fatal("Probe behind a finite turn = not ok, want ok")
	}
	// The probe was dispatched after the turn began, so it cannot have waited longer
	// than the whole turn; it waited for most of it, but a lower bound tight enough to
	// be interesting would fail on a slow machine, so it is half.
	if waited < hold/2 || waited > hold+time.Second {
		t.Errorf("waited = %s behind a %s turn, want most of that turn", waited, hold)
	}
}

// TestProbeDoesNotCountAsATurn keeps the turn histograms about the work callers
// dispatched: a steady stream of synthetic probes must not appear in them.
func TestProbeDoesNotCountAsATurn(t *testing.T) {
	l, stop := start(t)
	defer stop()
	rec := &recorder{}
	l.SetMetrics(rec)

	for range 3 {
		if _, ok := l.Probe(context.Background(), 2*time.Second, func() {}); !ok {
			t.Fatal("Probe = not ok")
		}
	}
	if n := len(rec.snapshot()); n != 0 {
		t.Errorf("probes reported %d turns, want 0", n)
	}
}

// TestProbeGivesUpOnAWedgedLoop: a probe behind a turn that does not end reports a
// failure at its deadline and never runs its work.
func TestProbeGivesUpOnAWedgedLoop(t *testing.T) {
	l, stop := start(t)
	defer stop()

	release := make(chan struct{})
	occupied := make(chan struct{})
	go l.Do(func() { close(occupied); <-release })
	<-occupied

	ran := make(chan struct{}, 1)
	if _, ok := l.Probe(context.Background(), 50*time.Millisecond, func() { ran <- struct{}{} }); ok {
		t.Error("Probe on a wedged loop = ok, want not ok")
	}
	close(release)
	// The probe was never handed over, so nothing is queued behind the released turn.
	if !l.Ping(context.Background(), 2*time.Second) {
		t.Fatal("loop did not recover after the wedge was released")
	}
	select {
	case <-ran:
		t.Error("work of a probe that was never handed over ran anyway")
	default:
	}
}

// TestProbeGivesUpOnWorkThatDoesNotFinish: the deadline also covers the work itself,
// so a read hung on a stalled volume is reported rather than waited out.
func TestProbeGivesUpOnWorkThatDoesNotFinish(t *testing.T) {
	l, stop := start(t)
	defer stop()

	release := make(chan struct{})
	if _, ok := l.Probe(context.Background(), 50*time.Millisecond, func() { <-release }); ok {
		t.Error("Probe whose work hangs = ok, want not ok")
	}
	close(release)
}

// TestProbeHonorsAHungUpCaller: a cancelled caller is answered at once.
func TestProbeHonorsAHungUpCaller(t *testing.T) {
	l := New(make(chan struct{})) // never run, so the send blocks
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := l.Probe(ctx, time.Hour, nil); ok {
		t.Error("Probe with a cancelled context = ok, want not ok")
	}
}
