package adr

import (
	"strings"
	"testing"
)

// A record has two states, and for 295 records one field was asked to carry both.
// `Status` is meant to say whether a decision holds; readers used it to ask whether
// the thing was built, because that is the more useful question and no other field
// answered it. So the two drifted apart in the only way they could: 115 records whose
// code had shipped, with tests, still read `Proposed`, because nothing in the process
// ever went back to change the line. An audit said so in August and named 28 of them;
// two weeks later not one had moved, which is the evidence that this is a missing
// mechanism rather than a missing afternoon.
//
// The fix is two fields and a rule between them: code merged against a decision *is*
// the decision being taken, so `Implementation: Landed` and `Status: Proposed` cannot
// both be true. The tests below hold the vocabularies and that rule. They are what
// makes the index answerable without reading the source first.

const stateHead = "# ADR-0001: T\n"

// record builds a minimal record body with the given front-matter lines.
func record(lines ...string) string {
	return stateHead + "\n" + strings.Join(lines, "\n") + "\n- **Date:** 2026-01-01\n"
}

// TestStateVocabularies pins both fields to their word lists. The status list has
// been in template.md since the directory existed and was enforced by nothing, which
// is how two numbered, shipped records came to read `Draft` — a word this package
// uses for "not numbered yet", said of records that are numbered.
func TestStateVocabularies(t *testing.T) {
	tests := []struct {
		name    string
		status  string
		impl    string
		wantErr string
	}{
		{name: "proposed and not started", status: "Proposed", impl: "Not started"},
		{name: "proposed and partial", status: "Proposed", impl: "Partial"},
		{name: "accepted and landed", status: "Accepted", impl: "Landed"},
		{name: "accepted and not started is legitimate",
			// A decision can be taken before anything is built. It is only the
			// reverse — built but never taken — that cannot be true.
			status: "Accepted", impl: "Not started"},
		{name: "superseded names its successor", status: "Superseded by ADR-0164", impl: "Superseded"},
		{name: "deprecated", status: "Deprecated", impl: "Partial"},
		{name: "a parenthetical after the status word is fine",
			status: "Accepted (amended 2026-08-17: the marker runs on every activity kind)", impl: "Landed"},
		{
			name: "Draft is not a status", status: "Draft", impl: "Landed",
			wantErr: `status "Draft" is not one of`,
		},
		{
			name: "an invented implementation word", status: "Accepted", impl: "Shipped",
			wantErr: `implementation "Shipped" is not one of`,
		},
		{
			// The field is read by machine to render the index, so it stays one of
			// four exact words. Nuance goes in the record, where a reader is.
			name: "implementation carries no parenthetical", status: "Accepted", impl: "Landed (mostly)",
			wantErr: `implementation "Landed (mostly)" is not one of`,
		},
		{
			name: "superseded without a successor", status: "Superseded", impl: "Superseded",
			wantErr: `status "Superseded" is not one of`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := record("- **Status:** "+tt.status, "- **Implementation:** "+tt.impl)
			r, err := parseRecord("0001-t.md", body)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("parseRecord: %v", err)
			case tt.wantErr != "":
				if err == nil {
					t.Fatalf("parseRecord accepted status %q with implementation %q", tt.status, tt.impl)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
				}
				return
			}
			if r.Status != tt.status {
				t.Errorf("Status = %q, want %q", r.Status, tt.status)
			}
			if r.Implementation != tt.impl {
				t.Errorf("Implementation = %q, want %q", r.Implementation, tt.impl)
			}
		})
	}
}

// TestLandedRecordsAreAccepted is the rule the whole split exists for. Nobody merges
// code against a decision that has not been made, so a record with landed code is a
// record whose decision was taken — whatever the front matter still says. Without
// this check the two fields drift exactly as the one field did, only twice as slowly.
func TestLandedRecordsAreAccepted(t *testing.T) {
	tests := []struct {
		name    string
		status  string
		wantErr string
	}{
		{name: "accepted", status: "Accepted"},
		{name: "accepted with a parenthetical", status: "Accepted (amended 2026-08-18)"},
		{
			name: "proposed", status: "Proposed",
			wantErr: `implementation is Landed but status is "Proposed"`,
		},
		{
			// The state the directory was actually in: shipped, and filed as a
			// suggestion.
			name: "proposed with an amendment note", status: "Proposed (amended 2026-08-21)",
			wantErr: `implementation is Landed but status is "Proposed"`,
		},
		{
			name: "deprecated", status: "Deprecated",
			wantErr: `implementation is Landed but status is "Deprecated"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := record("- **Status:** "+tt.status, "- **Implementation:** Landed")
			_, err := parseRecord("0001-t.md", body)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("parseRecord: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("parseRecord accepted Landed with status %q", tt.status)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

// TestSupersededImplementationNamesTheSuccessor keeps the one status that carries a
// parameter from being written as a bare word. "Superseded" alone tells a reader the
// decision is gone without telling them where it went, which is the one thing they
// need. ADR-0156 sat as a plain `Proposed` peer of the record that revised it for
// three weeks after an audit said so.
func TestSupersededImplementationNamesTheSuccessor(t *testing.T) {
	body := record("- **Status:** Proposed", "- **Implementation:** Superseded")
	_, err := parseRecord("0001-t.md", body)
	if err == nil {
		t.Fatal("parseRecord accepted a superseded implementation under a Proposed status")
	}
	if want := "say which record replaced it"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v, want it to mention %q", err, want)
	}
}

// TestMissingImplementationIsNamed makes the field mandatory rather than optional.
// An optional field would be absent from exactly the records nobody reconciled — the
// ones this exists to surface — and the index would carry an empty cell that reads as
// "nothing to say" instead of "nobody looked".
func TestMissingImplementationIsNamed(t *testing.T) {
	body := record("- **Status:** Accepted")
	_, err := parseRecord("0001-t.md", body)
	if err == nil {
		t.Fatal("parseRecord accepted a record with no implementation line")
	}
	if want := "no `- **Implementation:** ...` line"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v, want it to mention %q", err, want)
	}
}
