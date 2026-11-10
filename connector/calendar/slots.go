package calendar

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/calendar/catalog"
)

// Window is one opening interval of a weekday, in local wall time ("08:00" to
// "12:00"). To may be "24:00".
type Window struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Slot is one free slot find-free-slots offers.
type Slot struct {
	Start time.Time
	End   time.Time
	// Calendars are the calendars the slot is free in: every free one under
	// [catalog.RequireAny], all of them under [catalog.RequireAll].
	Calendars []string
}

// slotRequest is find-free-slots' input, checked and parsed.
type slotRequest struct {
	calendars []string
	from, to  time.Time
	duration  time.Duration
	grid      time.Duration
	buffer    time.Duration
	earliest  time.Time
	loc       *time.Location
	opening   map[time.Weekday][]minuteWindow
	closed    map[string]bool
	require   string
	max       int
}

// minuteWindow is a [Window] in minutes after local midnight.
type minuteWindow struct{ from, to int }

var weekdayOf = map[string]time.Weekday{
	"mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday, "thu": time.Thursday,
	"fri": time.Friday, "sat": time.Saturday, "sun": time.Sunday,
}

// parseClock reads "HH:MM" as minutes after midnight; "24:00" is the end of the day.
func parseClock(s string) (int, error) {
	h, m, ok := strings.Cut(s, ":")
	if !ok || len(h) != 2 || len(m) != 2 {
		return 0, fmt.Errorf("%q is not a time of day in the form HH:MM", s)
	}
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if err1 != nil || err2 != nil || hh < 0 || mm < 0 || mm > 59 || hh > 24 || (hh == 24 && mm != 0) {
		return 0, fmt.Errorf("%q is not a time of day in the form HH:MM", s)
	}
	return hh*60 + mm, nil
}

// parseOpening checks an opening-hours object: known weekday keys, intervals in the
// form HH:MM with from before to, at most [catalog.MaxWindowsPerDay] a day, none
// overlapping another of the same day.
func parseOpening(in map[string][]Window) (map[time.Weekday][]minuteWindow, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("the opening hours name no weekday; a calendar knows busy and free, not when you are open")
	}
	out := map[time.Weekday][]minuteWindow{}
	for key, windows := range in {
		day, ok := weekdayOf[key]
		if !ok {
			return nil, fmt.Errorf("the opening hours name %q, which is not one of %s", key, strings.Join(catalog.Weekdays, ", "))
		}
		if len(windows) > catalog.MaxWindowsPerDay {
			return nil, fmt.Errorf("the opening hours of %s have %d intervals; at most %d are allowed", key, len(windows), catalog.MaxWindowsPerDay)
		}
		ws := make([]minuteWindow, 0, len(windows))
		for _, w := range windows {
			from, err := parseClock(w.From)
			if err != nil {
				return nil, fmt.Errorf("the opening hours of %s: %w", key, err)
			}
			to, err := parseClock(w.To)
			if err != nil {
				return nil, fmt.Errorf("the opening hours of %s: %w", key, err)
			}
			if from >= to {
				return nil, fmt.Errorf("the opening hours of %s: %s is not before %s", key, w.From, w.To)
			}
			ws = append(ws, minuteWindow{from, to})
		}
		sort.Slice(ws, func(i, j int) bool { return ws[i].from < ws[j].from })
		for i := 1; i < len(ws); i++ {
			if ws[i].from < ws[i-1].to {
				return nil, fmt.Errorf("the opening hours of %s overlap", key)
			}
		}
		out[day] = ws
	}
	return out, nil
}

// candidates are the slots of the grid inside the opening hours, in time order, before
// any calendar is asked. The grid is laid in local wall time from the start of each
// opening interval, so a slot is "09:00" on every day of the year. A wall time a
// daylight-saving change skips does not exist and is not offered; one it repeats is
// offered once.
func (r *slotRequest) candidates(fn func(start, end time.Time) bool) {
	first := r.from.In(r.loc)
	last := r.to.In(r.loc)
	day := time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, r.loc)
	for !day.After(last) {
		y, m, d := day.Date()
		date := day.Format(time.DateOnly)
		if !r.closed[date] {
			for _, w := range r.opening[day.Weekday()] {
				step := int(r.grid / time.Minute)
				dur := int(r.duration / time.Minute)
				for at := w.from; at+dur <= w.to; at += step {
					start := time.Date(y, m, d, at/60, at%60, 0, 0, r.loc)
					if start.Hour() != at/60 || start.Minute() != at%60 || start.Day() != d {
						continue // skipped by a daylight-saving change
					}
					end := start.Add(r.duration)
					if start.Before(r.from) || end.After(r.to) || start.Before(r.earliest) {
						continue
					}
					if !fn(start, end) {
						return
					}
				}
			}
		}
		day = time.Date(y, m, d+1, 0, 0, 0, 0, r.loc)
	}
}

// freeSlots matches the grid against the busy times of each calendar. A calendar is
// free for a slot when none of its busy events takes any time of the slot widened by
// the buffer on both sides. It answers at most r.max slots, the earliest, and whether
// it stopped there.
func (r *slotRequest) freeSlots(busy map[string][]Event) (slots []Slot, truncated bool) {
	r.candidates(func(start, end time.Time) bool {
		lo, hi := start.Add(-r.buffer), end.Add(r.buffer)
		var free []string
		for _, cal := range r.calendars {
			if isFree(busy[cal], lo, hi) {
				free = append(free, cal)
			} else if r.require == catalog.RequireAll {
				return true
			}
		}
		if len(free) == 0 {
			return true
		}
		if len(slots) == r.max {
			truncated = true
			return false
		}
		slots = append(slots, Slot{Start: start, End: end, Calendars: free})
		return true
	})
	return slots, truncated
}

func isFree(events []Event, start, end time.Time) bool {
	for _, e := range events {
		if e.Busy && e.overlaps(start, end) {
			return false
		}
	}
	return true
}
