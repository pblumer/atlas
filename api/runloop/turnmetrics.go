package runloop

import (
	"sync/atomic"
	"time"
)

// Turn instrumentation (ADR-0142's shape, applied to the single-writer boundary).
//
// The run loop is the one place in Atlas where "how long did that take" is a question
// about the *whole server* rather than about one request: it is the single writer
// (invariant I3) **and** the gate every request passes through, including read-only
// ones, which take a turn to open their view (ADR-0239). A turn that runs long does
// not slow one caller down, it stops everything.
//
// Nothing on disk records that. The engine's batch counters do not either: a batch is
// work the processor did, while a turn is work *somebody dispatched*, and the two
// that hurt most — publishing a checkpoint, resolving a compaction cut — are not
// batches at all. So it has to be pushed from here.
//
// Both halves are reported because they answer different questions and only together
// tell the story:
//
//   - **held** is the cause. It is how long the closure occupied the writer, which is
//     the number a change like "take the whole-store read off the loop" moves.
//   - **waited** is the effect. It is what a caller spent queueing before its closure
//     even started, which is what a person experiences as the interface hanging. A
//     server can have one long turn and no complaints, or many medium ones and a queue
//     nobody can get through; held alone cannot tell those apart.

// Metrics observes the loop's turns.
//
// TurnTaken is called on the loop goroutine, so an implementation must not block, must
// not dispatch back onto the loop, and must be cheap — it is inside the very duration
// it is reporting.
//
// A loop with no Metrics — the default — reports nothing and does not even read the
// clock, so an uninstrumented loop pays literally nothing.
type Metrics interface {
	// TurnTaken reports one closure dispatched with [Loop.Do]: waited is how long the
	// caller spent before the loop picked it up, held is how long the loop then spent
	// running it. It is reported after the closure returns, including when the closure
	// panicked — a turn that ended badly still held the writer, and is the one most
	// worth seeing.
	TurnTaken(waited, held time.Duration)
}

// SetMetrics attaches turn instrumentation, or detaches it with nil.
//
// It is safe to call while the loop is running, which is not a luxury: the server
// starts its loop before it builds its metrics registry, and dispatches onto the loop
// in between — a supervised worker's environment is read from a store the loop owns.
// Storing the value atomically is what keeps that from being a data race, and it costs
// one atomic load per turn against a channel rendezvous that costs far more.
func (l *Loop) SetMetrics(m Metrics) {
	if m == nil {
		l.metrics.Store(nil)
		return
	}
	l.metrics.Store(&m)
}

// metricsRef is the Loop field SetMetrics writes. It is a pointer to the interface
// rather than an atomic.Value holding it, because atomic.Value panics when handed two
// different concrete types and a Metrics is an interface that could be either.
type metricsRef = atomic.Pointer[Metrics]

// currentMetrics is what is attached right now, or nil.
func (l *Loop) currentMetrics() Metrics {
	if p := l.metrics.Load(); p != nil {
		return *p
	}
	return nil
}

// dispatchedAt reads the clock, but only when there is somebody to report to. An
// uninstrumented loop gets a zero time and never calls time.Now at all.
func (l *Loop) dispatchedAt() time.Time {
	if l.currentMetrics() == nil {
		return time.Time{}
	}
	return time.Now()
}

// observeTurn reports a finished turn. queued is when the caller dispatched and
// started is when the loop began running the closure; the end is now, which is why
// this is deferred rather than called.
//
// A zero started means the loop was uninstrumented when the closure was handed over,
// so there is no turn to report — the alternative would be a bogus duration measured
// from the zero time.
func (l *Loop) observeTurn(queued, started time.Time) {
	m := l.currentMetrics()
	if m == nil || started.IsZero() {
		return
	}
	end := time.Now()
	m.TurnTaken(started.Sub(queued), end.Sub(started))
}
