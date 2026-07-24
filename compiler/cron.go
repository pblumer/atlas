package compiler

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// cronSpec is a compiled 5-field cron expression (minute hour day-of-month month
// day-of-week), each field a bitmask of the values it matches. It is evaluated in
// UTC so a due date is a deterministic function of the reference instant
// (invariant I4) — no local time zone or DST ambiguity reaches the engine.
//
// Supported per field: '*', a single value, comma lists (a,b,c), ranges (a-b),
// and steps (*/n or a-b/n). Day-of-month and day-of-week follow standard cron OR
// semantics: when both are restricted (neither is '*'), a day matches if *either*
// matches; when only one is restricted, only that one constrains the day.
type cronSpec struct {
	minute uint64 // bit i set = minute i matches (0-59)
	hour   uint64 // 0-23
	dom    uint64 // 1-31
	month  uint64 // 1-12
	dow    uint64 // 0-6, Sunday = 0

	domRestricted bool // false when the day-of-month field was '*'
	dowRestricted bool // false when the day-of-week field was '*'
}

// cronMaxScan bounds next()'s minute-by-minute search so an unsatisfiable
// expression (e.g. Feb 30) fails fast instead of looping forever. Five years of
// minutes comfortably covers every satisfiable schedule.
const cronMaxScan = 5 * 366 * 24 * 60

// parseCron compiles a 5-field cron expression.
func parseCron(s string) (cronSpec, error) {
	fields := strings.Fields(s)
	if len(fields) != 5 {
		return cronSpec{}, fmt.Errorf("cron expression needs 5 fields (min hour dom mon dow): %q", s)
	}
	var c cronSpec
	var err error
	if c.minute, _, err = parseCronField(fields[0], 0, 59); err != nil {
		return cronSpec{}, fmt.Errorf("cron minute: %w", err)
	}
	if c.hour, _, err = parseCronField(fields[1], 0, 23); err != nil {
		return cronSpec{}, fmt.Errorf("cron hour: %w", err)
	}
	if c.dom, c.domRestricted, err = parseCronField(fields[2], 1, 31); err != nil {
		return cronSpec{}, fmt.Errorf("cron day-of-month: %w", err)
	}
	if c.month, _, err = parseCronField(fields[3], 1, 12); err != nil {
		return cronSpec{}, fmt.Errorf("cron month: %w", err)
	}
	if c.dow, c.dowRestricted, err = parseCronField(fields[4], 0, 6); err != nil {
		return cronSpec{}, fmt.Errorf("cron day-of-week: %w", err)
	}
	return c, nil
}

// parseCronField compiles one field into a bitmask over [lo, hi]. It also reports
// whether the field is restricted (anything other than a bare '*'), which the
// day-of-month/day-of-week OR semantics need.
func parseCronField(field string, lo, hi int) (mask uint64, restricted bool, err error) {
	if field == "*" {
		return fullMask(lo, hi), false, nil
	}
	for _, part := range strings.Split(field, ",") {
		step := 1
		rng := part
		if slash := strings.IndexByte(part, '/'); slash >= 0 {
			rng = part[:slash]
			step, err = strconv.Atoi(part[slash+1:])
			if err != nil || step < 1 {
				return 0, false, fmt.Errorf("invalid step %q", part)
			}
		}
		var from, to int
		switch {
		case rng == "*":
			from, to = lo, hi
		case strings.IndexByte(rng, '-') >= 0:
			dash := strings.IndexByte(rng, '-')
			if from, err = strconv.Atoi(rng[:dash]); err != nil {
				return 0, false, fmt.Errorf("invalid range %q", part)
			}
			if to, err = strconv.Atoi(rng[dash+1:]); err != nil {
				return 0, false, fmt.Errorf("invalid range %q", part)
			}
		default:
			if from, err = strconv.Atoi(rng); err != nil {
				return 0, false, fmt.Errorf("invalid value %q", part)
			}
			to = from
		}
		if from < lo || to > hi || from > to {
			return 0, false, fmt.Errorf("value %q out of range %d-%d", part, lo, hi)
		}
		for v := from; v <= to; v += step {
			mask |= 1 << uint(v)
		}
	}
	return mask, true, nil
}

func fullMask(lo, hi int) uint64 {
	var m uint64
	for v := lo; v <= hi; v++ {
		m |= 1 << uint(v)
	}
	return m
}

// next returns the unix-ns instant of the first minute strictly after afterNanos
// that matches the spec, or afterNanos itself unchanged if none is found within
// cronMaxScan (an unsatisfiable expression — the caller then never fires it).
func (c cronSpec) next(afterNanos int64) int64 {
	t := time.Unix(0, afterNanos).UTC().Truncate(time.Minute).Add(time.Minute)
	for i := 0; i < cronMaxScan; i++ {
		if c.matches(t) {
			return t.UnixNano()
		}
		t = t.Add(time.Minute)
	}
	return afterNanos
}

func (c cronSpec) matches(t time.Time) bool {
	if c.minute&(1<<uint(t.Minute())) == 0 {
		return false
	}
	if c.hour&(1<<uint(t.Hour())) == 0 {
		return false
	}
	if c.month&(1<<uint(int(t.Month()))) == 0 {
		return false
	}
	domOK := c.dom&(1<<uint(t.Day())) != 0
	dowOK := c.dow&(1<<uint(int(t.Weekday()))) != 0
	switch {
	case c.domRestricted && c.dowRestricted:
		return domOK || dowOK
	case c.domRestricted:
		return domOK
	case c.dowRestricted:
		return dowOK
	default:
		return true
	}
}
