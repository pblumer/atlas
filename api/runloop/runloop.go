// Package runloop carries Atlas's single-writer boundary for design-time and
// API state.
//
// HTTP handlers are concurrent; the processor, the state store and the connector
// registries have exactly one owner (invariant 3, ADR-0002/0006). A [Loop] is
// that owner: one goroutine draining a queue of closures, and [Loop.Do] is the
// only way to put work on it. A caller holding a *Loop can reach shared state
// correctly and has no other way to reach it at all.
//
// The type exists so that guarantee is a thing rather than a habit. It was
// previously a private method on the api server object, which worked only
// because that object was the single receiver every handler hung off; as the API
// splits into per-area services (ADR-0147) each one is given a *Loop, so the
// boundary travels with the dependency instead of being inherited from a shared
// receiver.
package runloop

import (
	"context"
	"time"
)

// Loop is the single-writer goroutine. Build one with [New], run it with
// [Loop.Run], and dispatch onto it with [Loop.Do].
type Loop struct {
	// tasks carries closures to the loop goroutine. Unbuffered: a dispatching
	// caller and the loop rendezvous, which is what makes Do's completion signal
	// meaningful rather than merely queued.
	tasks chan func()
	// quit is owned by the caller that built the loop — typically the server,
	// which closes it once for the loop and every background sweeper alike.
	quit <-chan struct{}
}

// New builds a loop that runs until quit is closed. It does not start the
// goroutine; the caller runs [Loop.Run], usually under its own WaitGroup so
// shutdown can wait for it.
func New(quit <-chan struct{}) *Loop {
	return &Loop{tasks: make(chan func()), quit: quit}
}

// Run drains the queue on the calling goroutine until quit is closed. Everything
// dispatched with [Loop.Do] executes here, one closure at a time, which is what
// makes the state these closures touch single-writer.
func (l *Loop) Run() {
	for {
		select {
		case <-l.quit:
			return
		case fn := <-l.tasks:
			fn()
		}
	}
}

// Do runs fn on the loop goroutine and blocks until it completes, so a caller
// can read results out of fn by assignment.
//
// If the loop is closing, fn does not run and Do returns immediately: callers
// must treat their result variables' zero values as "not produced" rather than
// as an answer. The quit arm is also what keeps a caller from blocking forever
// when it dispatches at the moment the loop stops draining.
func (l *Loop) Do(fn func()) {
	done := make(chan struct{})
	select {
	case l.tasks <- func() { defer close(done); fn() }:
		<-done
	case <-l.quit:
	}
}

// Ping reports whether the loop is reachable: it hands over an empty closure and
// waits for it to run, giving up after d. The closure is empty on purpose — the
// check is that the loop is *reachable*, and anything it did would have to be
// safe to abandon, since a timed-out closure still runs later with nobody
// listening.
//
// Handing the closure over is what actually blocks — the queue is unbuffered, so
// a successful send means the loop has already taken it — and the deadline covers
// a stopped loop as well as a wedged one, which is why neither select watches
// quit: a caller that cares about shutdown has already reported it, and a
// shutdown that races this resolves at the deadline like any other unresponsive
// writer.
//
// It is deliberately not [Do] with a timeout: Do promises the closure ran, and a
// readiness probe must be able to give up on one that did not.
func (l *Loop) Ping(ctx context.Context, d time.Duration) bool {
	done := make(chan struct{})
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	select {
	case l.tasks <- func() { close(done) }:
	case <-ctx.Done(): // the caller hung up; nothing was queued
		return false
	case <-deadline.C:
		return false
	}
	select {
	case <-done:
		return true
	case <-deadline.C:
		return false
	}
}
