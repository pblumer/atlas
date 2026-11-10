package compiler

import (
	"strings"
	"testing"
)

const applyBase = `operation="apply-corrections" source="datei" corrections="korrekturen" resultVariable="korrigiert"`

// apply-corrections carries its corrections, the drop list and the file's name
// (ADR-0548, S2).
func TestCsvApplyCorrections(t *testing.T) {
	cp, d := csvDetailOf(t, applyBase+` drop="weg" documentName="neu.csv" delimiter=";"`)
	if cp.Intern(d.CsvOperation) != "apply-corrections" || cp.Intern(d.CsvCorrections) != "korrekturen" || cp.Intern(d.CsvDrop) != "weg" || cp.Intern(d.CsvDocumentName) != "neu.csv" {
		t.Fatalf("detail = op %q corrections %q drop %q name %q", cp.Intern(d.CsvOperation), cp.Intern(d.CsvCorrections), cp.Intern(d.CsvDrop), cp.Intern(d.CsvDocumentName))
	}
	_, d = csvDetailOf(t, applyBase)
	if cp.Intern(d.CsvDrop) != "" || cp.Intern(d.CsvDocumentName) != "" {
		t.Fatalf("defaults = drop %q name %q", cp.Intern(d.CsvDrop), cp.Intern(d.CsvDocumentName))
	}
}

// What apply-corrections could not do faithfully, and what only it reads, is refused at
// deploy.
func TestCsvApplyCorrectionsRefused(t *testing.T) {
	for _, tc := range []struct{ attrs, want string }{
		{`operation="apply-corrections" corrections="k" resultVariable="r"`, "needs a source"},
		{`operation="apply-corrections" source="d" resultVariable="r"`, "needs corrections"},
		{`operation="apply-corrections" source="d" corrections="k"`, "needs a resultVariable"},
		{applyBase + ` hasHeader="false"`, "without a header row"},
		{applyBase + ` format="avp"`, "avp file"},
		{applyBase + ` columns="a,b"`, "lists columns"},
		{applyBase + ` documentName="../x.csv"`, "is a path"},
		{`operation="apply-corrections" source="datei.x" corrections="k" resultVariable="r"`, "not a variable name"},
		{`source="csvText" corrections="k"`, "sets corrections, which only apply-corrections reads"},
		{`source="csvText" operation="write" documentName="x.csv"`, "sets documentName"},
		{`source="csvText" drop="weg"`, "sets drop"},
		{`operation="fix"`, "apply-corrections, read, or write"},
	} {
		_, err := Parse(1, 1, strings.NewReader(csvTaskBPMN(tc.attrs)))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", tc.attrs, err, tc.want)
		}
	}
}
