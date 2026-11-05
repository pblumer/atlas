package device

import (
	"sort"
	"sync"
	"testing"
	"time"
)

// fakeClock runs scheduled calls only when a test advances it, so no test here waits
// on the wall clock or depends on how goroutines are scheduled.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Duration
	timers []*fakeTimer
	armed  chan struct{}
}

type fakeTimer struct {
	c       *fakeClock
	at      time.Duration
	f       func()
	stopped bool
	fired   bool
}

func newFakeClock() *fakeClock { return &fakeClock{armed: make(chan struct{}, 64)} }

func (c *fakeClock) AfterFunc(d time.Duration, f func()) Timer {
	c.mu.Lock()
	t := &fakeTimer{c: c, at: c.now + d, f: f}
	c.timers = append(c.timers, t)
	c.mu.Unlock()
	c.armed <- struct{}{}
	return t
}

func (t *fakeTimer) Stop() bool {
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	was := !t.fired && !t.stopped
	t.stopped = true
	return was
}

// Advance moves the clock and runs, in time order, every call that has come due.
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now += d
	var due []*fakeTimer
	for _, t := range c.timers {
		if !t.fired && !t.stopped && t.at <= c.now {
			t.fired = true
			due = append(due, t)
		}
	}
	c.mu.Unlock()
	sort.SliceStable(due, func(i, j int) bool { return due[i].at < due[j].at })
	for _, t := range due {
		t.f()
	}
}

// awaitArmed waits until n more calls have been scheduled.
func (c *fakeClock) awaitArmed(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		select {
		case <-c.armed:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d timers were scheduled", i, n)
		}
	}
}

func TestSystemClockRunsTheCall(t *testing.T) {
	done := make(chan struct{})
	timer := SystemClock{}.AfterFunc(time.Millisecond, func() { close(done) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the call never ran")
	}
	if timer.Stop() {
		t.Fatal("a timer that fired reports it was stopped")
	}
}
