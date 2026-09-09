package adr

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"
)

// A record sometimes rests on a question its author could not answer. ADR-0292 is
// the case that prompted this: it decides to render a MIMWAL table's cells by
// position *because* the meaning of the columns is not established from any
// reference — the decision is right while that holds and would be re-argued if it
// stopped holding. Written once and left alone, such a record reads a year later
// as settled, because nothing in it distinguishes "we decided this" from "we
// decided this for now, on a gap in what we know".
//
// So the record says both: what the open question is, and when somebody last
// looked at it. The tests below hold that pair together — parseRecord refuses one
// without the other, and the freshness check below fails once a statement of
// "last looked at" has stood for a year.

// TestOpenQuestionPairIsParsed pins the parse: both lines, or neither, and a date
// that reads as a month.
func TestOpenQuestionPairIsParsed(t *testing.T) {
	const head = "# ADR-0001: T\n\n- **Status:** Accepted\n- **Date:** 2026-01-01\n"

	tests := []struct {
		name        string
		body        string
		wantQ       string
		wantChecked string
		wantErr     string
	}{
		{
			name:        "both lines",
			body:        head + "- **Open question:** whether MIMWAL documents its grid\n- **Question checked:** 2026-09\n",
			wantQ:       "whether MIMWAL documents its grid",
			wantChecked: "2026-09",
		},
		{
			// The records here wrap at prose width, so a question longer than a
			// line continues on the next one, indented. Reading only the first
			// line would truncate the question silently — in the very message
			// that is supposed to tell a reader what is unresolved.
			name: "question wrapped over lines",
			body: head + "- **Open question:** whether MIMWAL documents its grid\n" +
				"  columns anywhere in its own source\n- **Question checked:** 2026-09\n",
			wantQ:       "whether MIMWAL documents its grid columns anywhere in its own source",
			wantChecked: "2026-09",
		},
		{
			// A continuation stops at the next field, so the pair does not
			// swallow the line that dates it.
			name:        "continuation stops at the next field",
			body:        head + "- **Open question:** q\n- **Question checked:** 2026-09\n\n## Context\n",
			wantQ:       "q",
			wantChecked: "2026-09",
		},
		{
			// Neither line is the ordinary record: a decision that rests on
			// nothing unresolved says nothing about open questions.
			name: "neither line",
			body: head,
		},
		{
			// A question with no date cannot go stale, so it would quietly
			// become settled truth — the failure this pair exists to prevent.
			name:    "question without a date",
			body:    head + "- **Open question:** whether MIMWAL documents its grid\n",
			wantQ:   "whether MIMWAL documents its grid",
			wantErr: "no `- **Question checked:** YYYY-MM` line",
		},
		{
			// A date with no question says a thing was checked without saying
			// what, which no reader can act on.
			name:        "date without a question",
			body:        head + "- **Question checked:** 2026-09\n",
			wantChecked: "2026-09",
			wantErr:     "no `- **Open question:** ...` line",
		},
		{
			name:        "unreadable date",
			body:        head + "- **Open question:** q\n- **Question checked:** September 2026\n",
			wantQ:       "q",
			wantChecked: "September 2026",
			wantErr:     "is not a YYYY-MM month",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := parseRecord("0001-t.md", tt.body)
			if r.OpenQuestion != tt.wantQ {
				t.Errorf("OpenQuestion = %q, want %q", r.OpenQuestion, tt.wantQ)
			}
			if r.QuestionChecked != tt.wantChecked {
				t.Errorf("QuestionChecked = %q, want %q", r.QuestionChecked, tt.wantChecked)
			}
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("unexpected error: %v", err)
			case tt.wantErr != "" && err == nil:
				t.Errorf("want an error containing %q, got none", tt.wantErr)
			case tt.wantErr != "" && !strings.Contains(err.Error(), tt.wantErr):
				t.Errorf("error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestQuestionAge pins the arithmetic the freshness guard rests on, without the
// wall clock: how old a stated check is, and the two answers that are not an age
// at all — no date to read, and a date that reads as nothing.
func TestQuestionAge(t *testing.T) {
	now := time.Date(2027, 3, 15, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		checked string
		want    time.Duration
		wantOK  bool
	}{
		{name: "six months ago", checked: "2026-09", want: now.Sub(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)), wantOK: true},
		// The month it was checked is age zero-ish, not stale.
		{name: "this month", checked: "2027-03", want: now.Sub(time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)), wantOK: true},
		// A future date is a negative age, which is how the guard tells a typo
		// from a genuine check rather than waiting the typo out.
		{name: "next year", checked: "2028-01", want: now.Sub(time.Date(2028, 1, 1, 0, 0, 0, 0, time.UTC)), wantOK: true},
		{name: "no date", checked: "", wantOK: false},
		{name: "unreadable", checked: "September 2026", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Record{QuestionChecked: tt.checked}.QuestionAge(now)
			if ok != tt.wantOK {
				t.Fatalf("QuestionAge(%q) ok = %v, want %v", tt.checked, ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("QuestionAge(%q) = %v, want %v", tt.checked, got, tt.want)
			}
		})
	}
}

// TestOpenQuestionGuardFires is the test for the guard itself: a check that never
// fails is the failure mode this whole mechanism exists to prevent, so the two
// ways it must fire are exercised against fixed dates rather than against
// whatever the directory happens to hold today.
func TestOpenQuestionGuardFires(t *testing.T) {
	now := time.Date(2027, 3, 15, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name      string
		checked   string
		wantStale bool
		wantAhead bool
	}{
		{name: "checked this year", checked: "2026-12"},
		{name: "a year and a half ago", checked: "2025-09", wantStale: true},
		{name: "dated in the future", checked: "2028-01", wantAhead: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			age, ok := Record{QuestionChecked: tc.checked}.QuestionAge(now)
			if !ok {
				t.Fatalf("QuestionAge(%q) did not read", tc.checked)
			}
			if stale := age > openQuestionMaxAge; stale != tc.wantStale {
				t.Errorf("stale = %v, want %v (age %v, limit %v)", stale, tc.wantStale, age, openQuestionMaxAge)
			}
			if ahead := age < 0; ahead != tc.wantAhead {
				t.Errorf("ahead = %v, want %v (age %v)", ahead, tc.wantAhead, age)
			}
		})
	}
}

// openQuestionMaxAge is how long a "last looked at" may stand before the check
// below fails. Twelve months is the interval ADR-0289 set for the Worker Type
// setup steps, and the reasoning carries: long enough that re-reading is not
// busywork, short enough that a question cannot outlive everyone who knew it was
// open.
//
// **This test reads the wall clock, which the testing conventions otherwise
// forbid, and that is the point** — the same argument ADR-0289 makes. A check that
// can only fail when somebody edits the file would never fire, because the file
// not being edited is exactly the condition it exists to catch. It will one day
// turn CI red on a change that has nothing to do with the record. The fix is never
// to bump the date: it is to go and look at the question, write down what is known
// now, and date what was actually read — and, if the answer arrived, to remove
// both lines and revisit the decision that rested on the gap.
const openQuestionMaxAge = 12 * 30 * 24 * time.Hour // twelve months, in the units time understands

func TestOpenQuestionsAreRecentlyChecked(t *testing.T) {
	now := time.Now()
	var stale, ahead []string
	for _, r := range loadADRs(t) {
		// No date means no open question, or a half-written pair parseRecord has
		// already reported through loadADRs — either way, not this test's finding.
		age, ok := r.QuestionAge(now)
		if !ok {
			continue
		}
		// A negative age is a date in the future: a typo, and the one typo that
		// makes this check quieter rather than louder, so it fails on its own
		// terms rather than waiting out the year somebody added by accident.
		if age < 0 {
			ahead = append(ahead, fmt.Sprintf("%s (%s)", r.Name, r.QuestionChecked))
			continue
		}
		if age > openQuestionMaxAge {
			stale = append(stale, fmt.Sprintf("%s (last checked %s: %s)", r.Name, r.QuestionChecked, r.OpenQuestion))
		}
	}
	if len(ahead) > 0 {
		sort.Strings(ahead)
		t.Errorf("%d record(s) date their open question in the future: %s\n\n"+
			"That silences this check for as long as the typo lasts.", len(ahead), strings.Join(ahead, ", "))
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		t.Errorf("%d record(s) rest on a question nobody has looked at in over a year:\n  %s\n\n"+
			"Go and look at the question. If it now has an answer, remove both lines and check whether "+
			"the decision that rested on the gap still holds — that is the case this exists for. If it is "+
			"still open, say in the record what you learned and date the month you looked. Bumping the "+
			"date without looking is worse than the stale date, because it tells the next reader the "+
			"question was reviewed when it was not.",
			len(stale), strings.Join(stale, "\n  "))
	}
}
