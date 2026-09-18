package adr

import (
	"strings"
	"testing"
)

// ADR-0387 is the case that prompted this file. The catalogue, order and inventory
// complex — the better part of thirty records — was built without anyone asking
// whether a standard or an existing implementation already covered the ground.
// `grep -ci standard` on ADR-0312 returns zero across its 767 lines, and no record
// in the complex cites ADR-0176, the record written to place exactly that question.
// The answers turned out to be largely right, which is not the point: a record that
// does not name prior art cannot be told apart from one that examined it and refused
// it, and the refusal is the half a reviewer can check.
//
// ADR-0387 wrote the comparison down and named this line as a follow-up. Eight
// records followed it, several of them catalogue records, and not one named prior
// art — which is the evidence that a document is not a mechanism. The same lesson
// the two state fields learned in state_test.go: 115 records read `Proposed` with
// their code shipped, an audit named 28 of them, and two weeks later not one had
// moved.
//
// So the line is required, and the tests below hold it. What they cannot do is
// judge the answer: a guard can see that the question was put, never that it was
// put seriously. `none` is a legitimate answer for the many records that decide an
// internal detail — it just has to say why, so that a reader sees a decision rather
// than a blank.

// TestPriorArtIsParsed pins the parse, including the continuation lines these
// records wrap onto: a list of standards truncated at the margin would be
// truncated in the very line meant to enumerate them.
func TestPriorArtIsParsed(t *testing.T) {
	// ADR-0001 rather than a number above the cutoff: every `ADR-NNNN` in the tree
	// has to resolve to a real record (adr_test.go), fixtures included, and the
	// parse under test does not care what the number is.
	const head = "# ADR-0001: T\n\n- **Status:** Accepted\n- **Implementation:** Landed\n- **Date:** 2026-09-18\n"

	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "one line",
			body: head + "- **Prior art:** TMF620, examined in docs/comparisons/catalogue-standards.md\n",
			want: "TMF620, examined in docs/comparisons/catalogue-standards.md",
		},
		{
			name: "wrapped over lines",
			body: head + "- **Prior art:** TMF620, TMF622 and TMF637 carry the same three\n" +
				"  models; the mapping is in docs/comparisons/catalogue-standards.md\n",
			want: "TMF620, TMF622 and TMF637 carry the same three models; the mapping is in docs/comparisons/catalogue-standards.md",
		},
		{
			name: "a continuation stops at the next field",
			body: head + "- **Prior art:** none — an internal detail\n- **Deciders:** somebody\n",
			want: "none — an internal detail",
		},
		{
			name: "absent",
			body: head,
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := parseRecord("0001-thing.md", tc.body)
			if err != nil {
				t.Fatalf("parseRecord: %v", err)
			}
			if r.PriorArt != tc.want {
				t.Errorf("PriorArt = %q, want %q", r.PriorArt, tc.want)
			}
		})
	}
}

// TestPriorArtIsRequiredOfNewRecordsOnly holds the cutoff. The 397 records written
// before the rule are not retrofitted: going back to answer the question for each
// would be answering it from memory, which is the thing the line exists to stop.
func TestPriorArtIsRequiredOfNewRecordsOnly(t *testing.T) {
	tests := []struct {
		name string
		rec  Record
		want bool
	}{
		{name: "a record from before the rule", rec: Record{Num: 100}, want: false},
		{name: "the last record before the rule", rec: Record{Num: lastRecordWithoutPriorArt}, want: false},
		{name: "the first record under it", rec: Record{Num: lastRecordWithoutPriorArt + 1}, want: true},
		{name: "a draft, which will land above the cutoff", rec: Record{Num: 0, Name: "draft-a-thing.md"}, want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.rec.NeedsPriorArt(); got != tc.want {
				t.Errorf("NeedsPriorArt() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPriorArtDefectNamesWhat holds the two shapes a guard can actually judge: that
// the line is there at all, and that a `none` says why. It cannot judge more, and
// the record says so rather than pretending otherwise.
func TestPriorArtDefectNamesWhat(t *testing.T) {
	tests := []struct {
		name    string
		rec     Record
		wantBad bool
		wantSay string
	}{
		{
			name: "a named standard", wantBad: false,
			rec: Record{Num: 400, PriorArt: "TMF620, mapped in docs/comparisons/catalogue-standards.md"},
		},
		{
			name: "none with a reason", wantBad: false,
			rec: Record{Num: 400, PriorArt: "none — this decides where a button sits in the console"},
		},
		{
			name: "none with a reason after a colon", wantBad: false,
			rec: Record{Num: 400, PriorArt: "none: an internal storage detail"},
		},
		{
			name: "no line at all", wantBad: true, wantSay: "no `- **Prior art:**",
			rec: Record{Num: 400},
		},
		{
			name: "a bare none", wantBad: true, wantSay: "says `none` without saying why",
			rec: Record{Num: 400, PriorArt: "none"},
		},
		{
			name: "a none with punctuation and nothing else", wantBad: true, wantSay: "says `none` without saying why",
			rec: Record{Num: 400, PriorArt: "None —"},
		},
		{
			name: "a record from before the rule, with no line", wantBad: false,
			rec: Record{Num: 12},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			say, bad := tc.rec.PriorArtDefect()
			if bad != tc.wantBad {
				t.Fatalf("PriorArtDefect() bad = %v (%q), want %v", bad, say, tc.wantBad)
			}
			if bad && !strings.Contains(say, tc.wantSay) {
				t.Errorf("PriorArtDefect() = %q, want it to contain %q", say, tc.wantSay)
			}
		})
	}
}

// TestEveryNewRecordNamesPriorArt is the guard itself, over the real directory.
// It is what makes the line a mechanism rather than a note in a record nobody
// re-reads.
func TestEveryNewRecordNamesPriorArt(t *testing.T) {
	for _, r := range loadADRs(t) {
		if say, bad := r.PriorArtDefect(); bad {
			t.Errorf("%s: %s", r.Name, say)
		}
	}
}
