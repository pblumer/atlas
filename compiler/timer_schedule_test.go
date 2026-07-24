package compiler

import (
	"testing"
	"time"
)

func TestParseTimerSchedule(t *testing.T) {
	const now = int64(1_000_000_000_000) // arbitrary fixed reference instant

	tests := []struct {
		name       string
		def        xmlTimerEventDefinition
		wantKind   TimerScheduleKind
		wantReps   int32
		wantFirst  int64 // 0 = don't check (cron)
		wantRecurs bool
		checkFirst bool
	}{
		{
			name:       "duration fires once after the interval",
			def:        xmlTimerEventDefinition{TimeDuration: "PT1H"},
			wantKind:   TimerDuration,
			wantReps:   0,
			wantFirst:  now + int64(time.Hour),
			checkFirst: true,
			wantRecurs: false,
		},
		{
			name:       "duration tolerates a FEEL '=' prefix",
			def:        xmlTimerEventDefinition{TimeDuration: "=PT30S"},
			wantKind:   TimerDuration,
			wantFirst:  now + int64(30*time.Second),
			checkFirst: true,
		},
		{
			name:       "date fires once at the absolute instant",
			def:        xmlTimerEventDefinition{TimeDate: "2026-08-01T09:00:00Z"},
			wantKind:   TimerDate,
			wantReps:   0,
			wantFirst:  mustInstant(t, "2026-08-01T09:00:00Z"),
			checkFirst: true,
			wantRecurs: false,
		},
		{
			name:       "finite interval cycle recurs with reps-after-first",
			def:        xmlTimerEventDefinition{TimeCycle: "R3/PT1H"},
			wantKind:   TimerCycleInterval,
			wantReps:   2, // R3 = 3 total, so 2 more after the first
			wantFirst:  now + int64(time.Hour),
			checkFirst: true,
			wantRecurs: true,
		},
		{
			name:       "infinite interval cycle",
			def:        xmlTimerEventDefinition{TimeCycle: "R/PT15M"},
			wantKind:   TimerCycleInterval,
			wantReps:   -1,
			wantFirst:  now + int64(15*time.Minute),
			checkFirst: true,
			wantRecurs: true,
		},
		{
			name:       "cron cycle is infinite and recurs",
			def:        xmlTimerEventDefinition{TimeCycle: "0 * * * *"},
			wantKind:   TimerCycleCron,
			wantReps:   -1,
			wantRecurs: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := parseTimerSchedule(&tc.def)
			if err != nil {
				t.Fatalf("parseTimerSchedule: %v", err)
			}
			if s.Kind != tc.wantKind {
				t.Errorf("Kind = %d, want %d", s.Kind, tc.wantKind)
			}
			if s.Repetitions != tc.wantReps {
				t.Errorf("Repetitions = %d, want %d", s.Repetitions, tc.wantReps)
			}
			if tc.checkFirst {
				if got := s.FirstDue(now); got != tc.wantFirst {
					t.Errorf("FirstDue = %d, want %d", got, tc.wantFirst)
				}
			}
			if _, ok := s.NextDue(now); ok != tc.wantRecurs {
				t.Errorf("NextDue recurs = %v, want %v", ok, tc.wantRecurs)
			}
		})
	}
}

func TestParseTimerScheduleErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		def  xmlTimerEventDefinition
	}{
		{"empty", xmlTimerEventDefinition{}},
		{"two fields set", xmlTimerEventDefinition{TimeDuration: "PT1H", TimeDate: "2026-08-01T09:00:00Z"}},
		{"bad duration", xmlTimerEventDefinition{TimeDuration: "1 hour"}},
		{"bad date", xmlTimerEventDefinition{TimeDate: "next tuesday"}},
		{"bad interval repetition", xmlTimerEventDefinition{TimeCycle: "R0/PT1H"}},
		{"interval missing slash", xmlTimerEventDefinition{TimeCycle: "R3"}},
		{"bad interval duration", xmlTimerEventDefinition{TimeCycle: "R3/1h"}},
		{"bad cron field count", xmlTimerEventDefinition{TimeCycle: "0 * * *"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseTimerSchedule(&tc.def); err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func TestCronNext(t *testing.T) {
	tests := []struct {
		name string
		expr string
		from string
		want string
	}{
		{"every full hour lands on the next top of hour", "0 * * * *", "2026-07-24T10:17:00Z", "2026-07-24T11:00:00Z"},
		{"exact minute already passed rolls to next hour", "0 * * * *", "2026-07-24T11:00:00Z", "2026-07-24T12:00:00Z"},
		{"daily at 09:00", "0 9 * * *", "2026-07-24T10:00:00Z", "2026-07-25T09:00:00Z"},
		{"step minutes", "*/15 * * * *", "2026-07-24T10:07:00Z", "2026-07-24T10:15:00Z"},
		{"day-of-week monday", "30 8 * * 1", "2026-07-24T00:00:00Z", "2026-07-27T08:30:00Z"}, // 2026-07-27 is a Monday
		{"list of hours", "0 8,12,18 * * *", "2026-07-24T13:00:00Z", "2026-07-24T18:00:00Z"},
		{"day-of-month first", "0 0 1 * *", "2026-07-24T00:00:00Z", "2026-08-01T00:00:00Z"},
		{"dom or dow both restricted", "0 0 15 * 1", "2026-07-24T00:00:00Z", "2026-07-27T00:00:00Z"}, // Monday 07-27 comes before the 15th
		{"month restricted", "0 0 1 1 *", "2026-07-24T00:00:00Z", "2027-01-01T00:00:00Z"},
		{"stepped range", "0-30/15 * * * *", "2026-07-24T10:05:00Z", "2026-07-24T10:15:00Z"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := parseTimeCycle(tc.expr)
			if err != nil {
				t.Fatalf("parseTimeCycle(%q): %v", tc.expr, err)
			}
			from := mustInstant(t, tc.from)
			got := s.Cron.next(from)
			if want := mustInstant(t, tc.want); got != want {
				t.Errorf("next = %s, want %s", time.Unix(0, got).UTC().Format(time.RFC3339), tc.want)
			}
		})
	}
}

func TestParseCronErrors(t *testing.T) {
	for _, expr := range []string{
		"",
		"* * * *",     // too few
		"* * * * * *", // too many
		"99 * * * *",  // minute out of range
		"0 24 * * *",  // hour out of range
		"0 * */0 * *", // zero step
		"0 * 5-1 * *", // inverted range
		"0 * a * *",   // non-numeric
		"0 0 1 13 *",  // month out of range
		"0 0 1 1 9",   // day-of-week out of range
		"0 a-5 * * *", // non-numeric range lower bound
		"0 5-a * * *", // non-numeric range upper bound
		"0 */x * * *", // non-numeric step
	} {
		if _, err := parseCron(expr); err == nil {
			t.Errorf("parseCron(%q): expected error", expr)
		}
	}
}

// TestCronFirstDueAndUnsatisfiable covers a cron schedule's FirstDue (the cron
// branch) and next()'s fallback when no minute within the scan window matches.
func TestCronFirstDueAndUnsatisfiable(t *testing.T) {
	s, err := parseTimeCycle("0 * * * *")
	if err != nil {
		t.Fatalf("parseTimeCycle: %v", err)
	}
	from := mustInstant(t, "2026-07-24T10:17:00Z")
	if got, want := s.FirstDue(from), mustInstant(t, "2026-07-24T11:00:00Z"); got != want {
		t.Errorf("FirstDue = %d, want %d", got, want)
	}

	// February 30 never occurs: next() exhausts its scan window and returns the
	// reference instant unchanged, so the caller simply never fires it.
	impossible, err := parseCron("0 0 30 2 *")
	if err != nil {
		t.Fatalf("parseCron: %v", err)
	}
	if got := impossible.next(from); got != from {
		t.Errorf("unsatisfiable next = %d, want the reference %d (no match)", got, from)
	}
}

func mustInstant(t *testing.T, s string) int64 {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("bad test instant %q: %v", s, err)
	}
	return tm.UnixNano()
}
