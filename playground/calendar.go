package playground

import (
	"fmt"
	"time"
)

// Calendar says when something works: which weekdays, and which stretches of
// those days. It is shared by the pools that do the work and by the arrival plan
// that feeds them, because "business hours" means the same thing to both.
//
// The zero value is always open, so a caller who does not care about hours does
// not have to say so.
type Calendar struct {
	// Open are the windows in a day. Empty means the whole day.
	Open []Window
	// Days selects the weekdays, indexed by time.Weekday. The zero value — no day
	// selected — means every day.
	Days [7]bool
}

// Window is a stretch of a day a calendar is open in, as offsets from midnight UTC.
// 08:00–17:00 is {From: 8 * time.Hour, To: 17 * time.Hour}.
type Window struct{ From, To time.Duration }

// validate reports what is wrong with a calendar. A window that ends before it
// starts — or at the same instant — is the mistake worth catching: an empty
// window is reported as an opening by opensAfter and as closed by openAt, and the
// two disagreeing is worse than either answer.
func (c Calendar) validate(what string) error {
	for _, w := range c.Open {
		if w.To <= w.From {
			return fmt.Errorf("playground: %s has a window that ends at or before it starts (%s–%s)", what, w.From, w.To)
		}
	}
	return nil
}

// alwaysOpen reports whether the pool has no calendar at all.
func (c Calendar) alwaysOpen() bool { return len(c.Open) == 0 && c.Days == [7]bool{} }

// worksOn reports whether the pool works on a weekday.
func (c Calendar) worksOn(d time.Weekday) bool {
	if c.Days == [7]bool{} {
		return true
	}
	return c.Days[int(d)]
}

// openAt reports whether the pool is working at instant t (unix nanoseconds).
func (c Calendar) openAt(t int64) bool {
	if c.alwaysOpen() {
		return true
	}
	ts := time.Unix(0, t).UTC()
	if !c.worksOn(ts.Weekday()) {
		return false
	}
	if len(c.Open) == 0 {
		return true // a day filter with no hours: the whole of a working day
	}
	off := time.Duration(ts.Hour())*time.Hour + time.Duration(ts.Minute())*time.Minute +
		time.Duration(ts.Second())*time.Second + time.Duration(ts.Nanosecond())
	for _, w := range c.Open {
		if off >= w.From && off < w.To {
			return true
		}
	}
	return false
}

// opensAfter is the next instant at or after t the pool is working, and false if
// it never works again within the search horizon.
//
// It steps day by day rather than solving for the answer: a calendar is at most a
// handful of windows, the horizon is bounded, and a loop that anyone can read is
// worth more here than arithmetic nobody will check.
func (c Calendar) opensAfter(t int64) (int64, bool) {
	if c.alwaysOpen() {
		return t, true
	}
	ts := time.Unix(0, t).UTC()
	for day := 0; day <= calendarSearchDays; day++ {
		d := ts.AddDate(0, 0, day)
		if !c.worksOn(d.Weekday()) {
			continue
		}
		midnight := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, time.UTC)
		if len(c.Open) == 0 {
			if start := midnight.UnixNano(); start >= t {
				return start, true
			}
			return t, true // already inside a working day with no hour windows
		}
		best, found := int64(0), false
		for _, w := range c.Open {
			start := midnight.Add(w.From).UnixNano()
			end := midnight.Add(w.To).UnixNano()
			if end <= t {
				continue // this window is behind us
			}
			if start < t {
				start = t // we are inside it already
			}
			if !found || start < best {
				best, found = start, true
			}
		}
		if found {
			return best, true
		}
	}
	return 0, false
}

// calendarSearchDays bounds how far opensAfter looks for the next working moment.
// A pool that does not work within a fortnight is a mistake in the policy, not a
// schedule, and the run says so rather than searching for ever.
const calendarSearchDays = 14

// finishAt is when work of length d, started at t, is done — counting only the
// time the pool is actually working, so a case started before closing time carries
// on where it left off when the pool opens again.
func (c Calendar) finishAt(t int64, d time.Duration) (int64, bool) {
	if c.alwaysOpen() {
		return t + int64(d), true
	}
	remaining := int64(d)
	at := t
	// No special case for work of no duration: the loop below already answers it,
	// and it answers it *better* — a task that takes no time still happens when the
	// calendar is open, not the moment it was handed over at midnight.
	for guard := 0; guard <= calendarSearchDays*len(c.Open)+calendarSearchDays+1; guard++ {
		open, ok := c.opensAfter(at)
		if !ok {
			return 0, false
		}
		at = open
		closes, ok := c.closesAfter(at)
		if !ok {
			return 0, false
		}
		if avail := closes - at; avail >= remaining {
			return at + remaining, true
		} else {
			remaining -= avail
			at = closes
		}
	}
	return 0, false
}

// workingTimeBetween is how much working time the calendar offers between two
// instants. It is what a utilisation is a fraction *of*: dividing seat time by
// the wall-clock span instead counts the nights and the weekend as idle capacity,
// which reads as "we have room" on a pool with three hundred cases queued.
func (c Calendar) workingTimeBetween(from, to int64) time.Duration {
	if to <= from {
		return 0
	}
	if c.alwaysOpen() {
		return time.Duration(to - from)
	}
	var total time.Duration
	for at := from; at < to; {
		open, ok := c.opensAfter(at)
		if !ok || open >= to {
			break
		}
		closes, ok := c.closesAfter(open)
		if !ok {
			break
		}
		end := closes
		if end > to {
			end = to
		}
		total += time.Duration(end - open)
		// The loop is bounded by the span, not by calendarSearchDays: that horizon
		// is how far ahead a *single* question about the next opening will look, and
		// borrowing it as an iteration count here would silently stop summing after a
		// fortnight of windows and report a quarter-year run as a fortnight of
		// capacity. What bounds the loop instead is that closesAfter always answers
		// past the opening it was given, so at strictly increases towards to — and if
		// that ever stopped being true we would rather stop than spin.
		if closes <= at {
			break
		}
		at = closes
	}
	return total
}

// closesAfter is the end of the working stretch that contains t.
func (c Calendar) closesAfter(t int64) (int64, bool) {
	ts := time.Unix(0, t).UTC()
	// A day the calendar does not work on contains no working stretch at all, so
	// there is nothing for it to end. Without this the hour windows would answer
	// for a Sunday as readily as for a Tuesday.
	if !c.worksOn(ts.Weekday()) {
		return 0, false
	}
	midnight := time.Date(ts.Year(), ts.Month(), ts.Day(), 0, 0, 0, 0, time.UTC)
	if len(c.Open) == 0 {
		return midnight.AddDate(0, 0, 1).UnixNano(), true // the whole working day
	}
	off := time.Duration(ts.Sub(midnight))
	for _, w := range c.Open {
		if off >= w.From && off < w.To {
			return midnight.Add(w.To).UnixNano(), true
		}
	}
	return 0, false
}
