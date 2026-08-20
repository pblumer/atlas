package api

import "sync"

// Long-poll for the type-keyed pull (ADR-0157 step 2b, on the post-fsync
// notification ADR-0005 already fires).
//
// Without it a worker busy-polls: every idle queue costs a request, and the
// latency between a job appearing and a worker seeing it is the poll interval.
// With it the request sleeps until the engine says a job of that type exists.
//
// The one rule that shapes this file: **the waiting happens off the run loop.** A
// request holding `Loop.Do` while it waits would freeze the single writer for the
// whole poll — the exact stall ADR-0155 was written about, reintroduced by the
// feature meant to remove it. So this registry guards itself with a mutex rather
// than being run-loop owned, and it is touched from two goroutines: the handler
// registers and cancels, and the processor's post-fsync notifier signals.
//
// Signalling must never block: the notifier runs on the goroutine that just
// committed a batch, so it closes channels (which never blocks) rather than
// sending on them.

// jobWaiters tracks who is waiting for a job of which type.
type jobWaiters struct {
	mu sync.Mutex
	// byType holds one entry per waiting request. The value is closed to wake it.
	byType map[int32]map[*waiter]struct{}
}

// waiter is one waiting request. The channel is closed exactly once, either by a
// notification or by the waiter cancelling.
type waiter struct {
	ch     chan struct{}
	closed bool
}

func newJobWaiters() *jobWaiters {
	return &jobWaiters{byType: map[int32]map[*waiter]struct{}{}}
}

// wait registers interest in a job type and returns the channel that closes when
// one appears, plus a cancel that deregisters. Cancel is idempotent and must always
// be called — a handler that wakes and one that times out both leave through it, or
// the entry is walked by every later notification.
//
// Register BEFORE looking for work: a job created between a failed look and the
// registration would otherwise be missed, and the request would sleep through the
// very thing it is waiting for.
func (w *jobWaiters) wait(jobType int32) (<-chan struct{}, func()) {
	entry := &waiter{ch: make(chan struct{})}
	w.mu.Lock()
	if w.byType[jobType] == nil {
		w.byType[jobType] = map[*waiter]struct{}{}
	}
	w.byType[jobType][entry] = struct{}{}
	w.mu.Unlock()

	return entry.ch, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		if set := w.byType[jobType]; set != nil {
			delete(set, entry)
			if len(set) == 0 {
				delete(w.byType, jobType)
			}
		}
		entry.close()
	}
}

// notify wakes every request waiting for this job type. It runs on the processor's
// goroutine right after a batch is durable, so it does only bounded, non-blocking
// work: closing a channel, never sending on one.
//
// Every waiter wakes, not just one. Several workers may poll the same queue, and
// the ones that lose the race simply find nothing and poll again — which is correct
// and much simpler than trying to hand the job to a particular waiter.
func (w *jobWaiters) notify(jobType int32) {
	w.mu.Lock()
	defer w.mu.Unlock()
	set := w.byType[jobType]
	for entry := range set {
		entry.close()
	}
	delete(w.byType, jobType)
}

// close wakes the waiter, at most once — a waiter can be woken by a notification
// and then cancelled by its own handler's defer.
func (e *waiter) close() {
	if !e.closed {
		e.closed = true
		close(e.ch)
	}
}

// count is how many requests are waiting on a job type. It exists for the tests,
// which must know a waiter is registered before signalling it.
func (w *jobWaiters) count(jobType int32) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.byType[jobType])
}
