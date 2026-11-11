package runloop

import (
	"context"
	"time"
)

// Probe hands work to the loop as a reference turn and reports how long it queued before
// the loop started it (ADR-draft-runloop-reference-probe).
//
// It exists because the turn histograms (see [Metrics]) measure the traffic the server
// happens to be carrying. When they rise, the cause may be more load, a different mix
// of work, or a loop that has become slower at the same work, and those numbers cannot
// tell the three apart. They also stay silent while nobody dispatches. A probe dispatched
// on a fixed cadence, doing the same fixed work every time, provides a measurement that
// does not depend on the traffic:
//
//   - waited is how long the probe queued before the loop took it: the delay any request
//     would have experienced at that moment, sampled at a steady rate.
//   - work runs on the loop goroutine and measures itself. It must be fixed work, so that
//     a change in its duration is a change in the conditions it runs under rather than
//     in the work.
//
// ok is false when the probe did not finish within d, or the caller hung up first. A
// closure the loop has already taken still runs to completion with nobody waiting, so
// work must be safe to abandon. It must also be read-only: it holds the single writer
// while it runs (invariant I3), so it has to be small, and it must not touch state the
// loop owns in a way a turn dispatched with [Loop.Do] would not.
//
// A probe is not reported as a turn through [Metrics]. A steady stream of small synthetic
// turns would pull the turn histograms' quantiles towards the probe's own cost, and those
// histograms are meant to describe the work callers dispatched.
//
// A nil work is an empty closure, which is what [Loop.Ping] sends.
func (l *Loop) Probe(ctx context.Context, d time.Duration, work func()) (waited time.Duration, ok bool) {
	done := make(chan struct{})
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	// started is written on the loop goroutine before done is closed and read here only
	// after done is closed, so the channel orders the two. A probe that timed out after
	// the hand-over never reads it.
	var started time.Time
	// Read before the send, as in Do: the send is the wait.
	queued := time.Now()
	select {
	case l.tasks <- func() {
		defer close(done)
		started = time.Now()
		if work != nil {
			work()
		}
	}:
	case <-ctx.Done(): // the caller hung up; nothing was queued
		return 0, false
	case <-deadline.C:
		return 0, false
	}
	select {
	case <-done:
		return started.Sub(queued), true
	case <-ctx.Done():
		return 0, false
	case <-deadline.C:
		return 0, false
	}
}
