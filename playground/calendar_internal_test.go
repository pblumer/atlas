package playground

import (
	"testing"
	"time"
)

// mon is a Monday at 09:00 UTC, so a test can say "Tuesday" and mean it.
var mon = time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC)

func at(d time.Duration) int64 { return mon.Add(d).UnixNano() }

// The zero calendar is always open, which is what lets a caller who does not care
// about hours leave it out.
func TestTheZeroCalendarIsAlwaysOpen(t *testing.T) {
	var c Calendar
	if !c.openAt(at(0)) || !c.openAt(at(72*time.Hour)) {
		t.Error("the zero calendar should be open at any instant")
	}
	when, ok := c.opensAfter(at(5 * time.Hour))
	if !ok || when != at(5*time.Hour) {
		t.Errorf("opensAfter = %d/%v, want the instant itself", when, ok)
	}
	done, ok := c.finishAt(at(0), 3*time.Hour)
	if !ok || done != at(3*time.Hour) {
		t.Errorf("finishAt = %d/%v, want three hours later", done, ok)
	}
}

// Hours without days: open every day between the windows.
func TestHourWindows(t *testing.T) {
	c := Calendar{Open: []Window{{From: 8 * time.Hour, To: 12 * time.Hour}, {From: 13 * time.Hour, To: 17 * time.Hour}}}

	if !c.openAt(at(0)) { // 09:00
		t.Error("09:00 is inside the morning window")
	}
	if c.openAt(at(3 * time.Hour)) { // 12:00, the lunch gap
		t.Error("12:00 is in the gap between the windows")
	}
	// From inside the gap, the next opening is the afternoon window.
	when, ok := c.opensAfter(at(3 * time.Hour))
	if !ok || when != at(4*time.Hour) {
		t.Errorf("opensAfter noon = %s, want 13:00", time.Unix(0, when).UTC())
	}
	// From after closing, the next opening is tomorrow morning.
	when, ok = c.opensAfter(at(9 * time.Hour)) // 18:00
	if !ok || when != at(23*time.Hour) {       // next day 08:00
		t.Errorf("opensAfter 18:00 = %s, want tomorrow 08:00", time.Unix(0, when).UTC())
	}
	// Work spans the lunch gap: two hours from 11:00 ends at 14:00.
	done, ok := c.finishAt(at(2*time.Hour), 2*time.Hour)
	if !ok || done != at(5*time.Hour) {
		t.Errorf("finishAt = %s, want 14:00", time.Unix(0, done).UTC())
	}
}

// Days without hours: a whole working day, and the weekend skipped.
func TestWeekdaysWithoutHours(t *testing.T) {
	var c Calendar
	for _, d := range []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday} {
		c.Days[int(d)] = true
	}
	if !c.openAt(at(0)) {
		t.Error("Monday 09:00 should be a working moment")
	}
	saturday := at(5 * 24 * time.Hour)
	if c.openAt(saturday) {
		t.Error("Saturday should not be a working day")
	}
	// From Saturday, the next working moment is Monday midnight.
	when, ok := c.opensAfter(saturday)
	if !ok {
		t.Fatal("a weekday calendar should open again")
	}
	if got := time.Unix(0, when).UTC(); got.Weekday() != time.Monday || got.Hour() != 0 {
		t.Errorf("opensAfter Saturday = %s, want Monday midnight", got)
	}
	// Inside a working day with no hour windows, "now" is already open.
	if when, ok := c.opensAfter(at(0)); !ok || when != at(0) {
		t.Errorf("opensAfter a working moment = %d, want the instant itself", when)
	}
	// Work that outlasts Friday resumes on Monday.
	friday := at(4 * 24 * time.Hour) // Friday 09:00
	done, ok := c.finishAt(friday, 20*time.Hour)
	if !ok {
		t.Fatal("work should finish eventually")
	}
	if got := time.Unix(0, done).UTC(); got.Weekday() != time.Monday {
		t.Errorf("20 hours of work from Friday 09:00 ends %s, want a Monday", got)
	}
}

// Hours and days together: the window applies only on the selected days.
func TestWeekdaysWithHours(t *testing.T) {
	c := Calendar{Open: []Window{{From: 8 * time.Hour, To: 17 * time.Hour}}}
	c.Days[int(time.Wednesday)] = true

	if c.openAt(at(0)) {
		t.Error("Monday is not a Wednesday")
	}
	when, ok := c.opensAfter(at(0))
	if !ok {
		t.Fatal("the calendar should open on Wednesday")
	}
	got := time.Unix(0, when).UTC()
	if got.Weekday() != time.Wednesday || got.Hour() != 8 {
		t.Errorf("opensAfter Monday = %s, want Wednesday 08:00", got)
	}
	if _, ok := c.closesAfter(when); !ok {
		t.Error("the stretch containing an opening should have an end")
	}
	// An instant that is inside no window has no closing time of its own.
	if _, ok := c.closesAfter(at(0)); ok {
		t.Error("Monday 09:00 is inside no working stretch and should have no close")
	}
}

// Work of no duration finishes the moment it starts — but still only once the
// calendar is open, so a task that takes no time on a closed pool waits for the
// morning like any other.
func TestWorkOfNoDuration(t *testing.T) {
	c := Calendar{Open: []Window{{From: 8 * time.Hour, To: 17 * time.Hour}}}
	if done, ok := c.finishAt(at(0), 0); !ok || done != at(0) {
		t.Errorf("finishAt with no work = %d/%v, want the instant itself", done, ok)
	}
	// 18:00 is closed: the work lands at tomorrow's opening.
	if done, ok := c.finishAt(at(9*time.Hour), 0); !ok || done != at(23*time.Hour) {
		t.Errorf("finishAt after hours = %s, want tomorrow 08:00", time.Unix(0, done).UTC())
	}
}

// A calendar that cannot fit the work in a fortnight says so, rather than looping
// or reporting a finish that never comes.
func TestWorkThatDoesNotFitTheCalendar(t *testing.T) {
	c := Calendar{Open: []Window{{From: 9 * time.Hour, To: 9*time.Hour + time.Minute}}} // one minute a day
	if _, ok := c.finishAt(at(0), 2*time.Hour); ok {
		t.Error("two hours of work at one minute a day should not report a finish")
	}
}

// workingTimeBetween is the denominator of every utilisation the report prints,
// and it is arithmetic nobody will check by eye: a span of ten days over a
// nine-hour weekday calendar offers about ninety hours, not two hundred and
// forty, and getting that wrong is the difference between "saturated" and
// "quiet" on the same run.
func TestWorkingTimeBetween(t *testing.T) {
	weekdays := Calendar{
		Open: []Window{{From: 8 * time.Hour, To: 12 * time.Hour}, {From: 13 * time.Hour, To: 17 * time.Hour}},
		Days: [7]bool{time.Monday: true, time.Tuesday: true, time.Wednesday: true, time.Thursday: true, time.Friday: true},
	}
	cases := []struct {
		name     string
		c        Calendar
		from, to int64
		want     time.Duration
	}{
		{"an empty span offers nothing", weekdays, at(time.Hour), at(time.Hour), 0},
		{"a backwards span offers nothing", weekdays, at(time.Hour), at(0), 0},
		{"always open is the whole span", Calendar{}, at(0), at(30 * time.Hour), 30 * time.Hour},
		// Monday 09:00 to Monday 14:00: the rest of the morning plus an hour after lunch.
		{"one day, clipped by both ends", weekdays, at(0), at(5 * time.Hour), 4 * time.Hour},
		// Monday 09:00 through Wednesday 09:00: 7h left of Monday, 8h on Tuesday, and
		// 1h of Wednesday morning.
		{"across two nights", weekdays, at(0), at(48 * time.Hour), 16 * time.Hour},
		// Friday 09:00 through Monday 09:00: Friday's remaining 7h and nothing else,
		// because the weekend is not capacity.
		{"across a weekend", weekdays, at(96 * time.Hour), at(168 * time.Hour), 8 * time.Hour},
		// A minute a day, over sixty days. calendarSearchDays is how far ahead one
		// question about the next opening looks; it is not a ceiling on how many
		// openings a span contains, and a run measured in months must not come back
		// reporting a fortnight of capacity.
		{"a long span of small windows", Calendar{Open: []Window{{From: 9 * time.Hour, To: 9*time.Hour + time.Minute}}},
			at(0), at(60 * 24 * time.Hour), 60 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.workingTimeBetween(tc.from, tc.to); got != tc.want {
				t.Errorf("workingTimeBetween = %s, want %s", got, tc.want)
			}
		})
	}
}
