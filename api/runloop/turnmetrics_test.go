package runloop

import (
	"context"
	"sync"
	"testing"
	"time"
)

// recorder collects the turns a loop reports. It is safe for the loop goroutine to
// write while the test goroutine reads.
type recorder struct {
	mu    sync.Mutex
	turns []turn
}

type turn struct{ waited, held time.Duration }

func (r *recorder) TurnTaken(waited, held time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.turns = append(r.turns, turn{waited, held})
}

func (r *recorder) snapshot() []turn {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]turn(nil), r.turns...)
}

// runLoop starts a loop and returns it with a stop func.
func runLoop(t *testing.T, m Metrics) (*Loop, func()) {
	t.Helper()
	quit := make(chan struct{})
	l := New(quit)
	if m != nil {
		l.SetMetrics(m)
	}
	done := make(chan struct{})
	go func() { defer close(done); l.Run() }()
	return l, func() {
		close(quit)
		<-done
	}
}

// TestEveryTurnIsReported: one dispatched closure is one reported turn. Without this
// the metric silently under-counts, which is worse than not having it — a histogram
// missing its slowest turns says the writer is healthy.
func TestEveryTurnIsReported(t *testing.T) {
	r := &recorder{}
	l, stop := runLoop(t, r)
	defer stop()

	for range 3 {
		l.Do(func() {})
	}
	if got := len(r.snapshot()); got != 3 {
		t.Errorf("reported %d turns for 3 dispatches, want 3", got)
	}
}

// TestHeldCoversTheWorkTheClosureDid: the held duration is the time the closure
// occupied the writer, which is the number the metric exists to expose. Asserted by
// making the closure wait for the test rather than for a clock, so the ordering is
// deterministic — the turn cannot be shorter than the wait the test imposed.
func TestHeldCoversTheWorkTheClosureDid(t *testing.T) {
	r := &recorder{}
	l, stop := runLoop(t, r)
	defer stop()

	release := make(chan struct{})
	entered := make(chan struct{})
	go func() {
		l.Do(func() {
			close(entered)
			<-release // the writer is held until the test lets go
		})
	}()
	<-entered
	// The closure is now inside the loop. Nothing is reported until it returns.
	if got := len(r.snapshot()); got != 0 {
		t.Fatalf("reported %d turns while the closure was still running, want 0", got)
	}
	close(release)

	deadline := time.After(5 * time.Second)
	for len(r.snapshot()) == 0 {
		select {
		case <-deadline:
			t.Fatal("the finished turn was never reported")
		default:
		}
	}
	if turns := r.snapshot(); turns[0].held <= 0 {
		t.Errorf("held = %v, want a positive duration covering the closure", turns[0].held)
	}
}

// TestWaitedCoversTheQueueingBehindABusyWriter: waited is what a caller spent before
// its closure even started — the half a request actually feels. A second caller
// arriving behind a held writer must see it.
func TestWaitedCoversTheQueueingBehindABusyWriter(t *testing.T) {
	r := &recorder{}
	l, stop := runLoop(t, r)
	defer stop()

	release := make(chan struct{})
	entered := make(chan struct{})
	first := make(chan struct{})
	go func() {
		defer close(first)
		l.Do(func() {
			close(entered)
			<-release
		})
	}()
	<-entered

	second := make(chan struct{})
	go func() {
		defer close(second)
		l.Do(func() {}) // queues behind the held writer
	}()
	// Let the second caller reach the send before releasing the first, so its wait is
	// genuinely the first closure's hold and not a scheduling accident.
	time.Sleep(20 * time.Millisecond)
	close(release)
	<-first
	<-second

	turns := r.snapshot()
	if len(turns) != 2 {
		t.Fatalf("reported %d turns, want 2", len(turns))
	}
	if turns[1].waited <= 0 {
		t.Errorf("the queued caller's waited = %v, want a positive duration", turns[1].waited)
	}
}

// TestAnUninstrumentedLoopReportsNothing: metrics are opt-in, and a loop without them
// must neither panic nor pay for a clock it has nobody to report to.
func TestAnUninstrumentedLoopReportsNothing(t *testing.T) {
	l, stop := runLoop(t, nil)
	defer stop()

	ran := false
	l.Do(func() { ran = true })
	if !ran {
		t.Error("the closure did not run on an uninstrumented loop")
	}
}

// TestPingIsNotATurn: Ping hands over an empty closure to see whether the loop
// answers. Counting it would fill the histogram with zero-length turns nobody
// performed and drag the picture of the writer's real work down.
func TestPingIsNotATurn(t *testing.T) {
	r := &recorder{}
	l, stop := runLoop(t, r)
	defer stop()

	if !l.Ping(context.Background(), 5*time.Second) {
		t.Fatal("Ping did not reach the loop")
	}
	if got := len(r.snapshot()); got != 0 {
		t.Errorf("Ping reported %d turns, want 0 — it is a probe, not work", got)
	}
}

// TestATurnIsReportedEvenWhenTheClosurePanics: a closure that panics still held the
// writer for however long it ran, and that is exactly the turn an operator most wants
// to see. The panic itself is left to propagate, unchanged.
func TestATurnIsReportedEvenWhenTheClosurePanics(t *testing.T) {
	r := &recorder{}
	quit := make(chan struct{})
	l := New(quit)
	l.SetMetrics(r)
	panicked := make(chan struct{})
	go func() {
		defer func() {
			_ = recover()
			close(panicked)
		}()
		l.Run()
	}()
	go func() { l.Do(func() { panic("boom") }) }()
	<-panicked
	close(quit)

	if got := len(r.snapshot()); got != 1 {
		t.Errorf("reported %d turns for a panicking closure, want 1", got)
	}
}
