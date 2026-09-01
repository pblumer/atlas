package playground

import "time"

// vclock is a sandbox's virtual clock: an [engine.Clock] whose reading is set by
// the scheduler rather than by the host.
//
// Unlike the conformance driver's clock it does not tick on read. Simulated time
// is a modelled quantity here — a report says "this case took four hours" — so a
// bare read must not move it. Nothing in the engine requires strictly increasing
// timestamps: events carry a position alongside their timestamp, so several facts
// at the same instant stay ordered.
type vclock struct{ nanos int64 }

// Now reports the current simulated instant in unix nanoseconds.
func (c *vclock) Now() int64 { return c.nanos }

// advanceTo moves simulated time forward to t. It never moves backwards: a
// scheduler that computed a due date in the past (a timer already overdue when it
// was armed) still leaves the clock where it is, so the run's timeline is
// monotonic.
func (c *vclock) advanceTo(t int64) {
	if t > c.nanos {
		c.nanos = t
	}
}

func (c *vclock) time() time.Time { return time.Unix(0, c.nanos).UTC() }
