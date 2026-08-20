package api

import (
	"sync"
	"testing"
	"time"
)

// The waiter registry is what lets a long-poll sleep instead of spin. It is the one
// piece of the pull that runs OFF the run loop — a request holding the run loop
// while it waits is the exact stall this whole sequence exists to remove — so it
// guards itself, and notifying must never block the goroutine that commits batches.
func TestWaiterWakesOnItsOwnJobType(t *testing.T) {
	w := newJobWaiters()
	wake, cancel := w.wait(18)
	defer cancel()
	other, cancelOther := w.wait(19)
	defer cancelOther()

	w.notify(18)

	select {
	case <-wake:
	case <-time.After(2 * time.Second):
		t.Fatal("a waiter on job type 18 was not woken by a job of type 18")
	}
	select {
	case <-other:
		t.Error("a waiter on job type 19 woke on a job of type 18")
	default:
	}
}

// A notification with nobody waiting must not block or panic — it runs on the
// goroutine that just committed a batch.
func TestNotifyWithNoWaitersIsHarmless(t *testing.T) {
	w := newJobWaiters()
	done := make(chan struct{})
	go func() { defer close(done); w.notify(42) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("notify blocked with no waiter registered")
	}
}

// Every waiter on a type wakes, not just the first: several workers may be polling
// the same queue, and the one that loses the race simply finds nothing and polls on.
func TestEveryWaiterOnATypeWakes(t *testing.T) {
	w := newJobWaiters()
	var wg sync.WaitGroup
	for range 5 {
		wake, cancel := w.wait(7)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer cancel()
			select {
			case <-wake:
			case <-time.After(2 * time.Second):
				t.Error("a waiter was not woken")
			}
		}()
	}
	// Give the waiters a moment to be registered before the notification.
	for w.count(7) < 5 {
		time.Sleep(time.Millisecond)
	}
	w.notify(7)
	wg.Wait()
}

// Cancelling deregisters, so a request that gave up or timed out does not leave its
// channel behind for every later notification to walk.
func TestCancelDeregistersTheWaiter(t *testing.T) {
	w := newJobWaiters()
	_, cancel := w.wait(3)
	if got := w.count(3); got != 1 {
		t.Fatalf("registered waiters = %d, want 1", got)
	}
	cancel()
	if got := w.count(3); got != 0 {
		t.Errorf("registered waiters = %d after cancel, want 0", got)
	}
	cancel() // idempotent: a handler may cancel on both the wake and the defer
	if got := w.count(3); got != 0 {
		t.Errorf("registered waiters = %d after a second cancel, want 0", got)
	}
}
