package csvimport_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/csvimport"
)

// A resolved CSV job is what a worker receives: plain values, no engine concepts.
// This is the mechanism ADR-0167 rests on — the engine finds the task detail and
// evaluates it, and only the result travels — proved first on a kind where no
// credential is involved, so the security decision does not ride on it.
func TestRunParsesAResolvedJob(t *testing.T) {
	got, err := csvimport.Run(csvimport.Job{
		Source:    "name,amount\nAda,12\nGrace,7\n",
		HasHeader: true,
		Result:    "orders",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.ResultVariable != "orders" {
		t.Errorf("result variable = %q, want orders", got.ResultVariable)
	}
	if got.RowCount != 2 {
		t.Errorf("row count = %d, want 2", got.RowCount)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(got.RowsJSON), &rows); err != nil {
		t.Fatalf("rows are not JSON: %v (%s)", err, got.RowsJSON)
	}
	if len(rows) != 2 || rows[0]["name"] != "Ada" || rows[1]["name"] != "Grace" {
		t.Errorf("rows = %v, want the two named rows", rows)
	}
}

// The defaults a model may leave unset are applied by Run, not by whoever calls it,
// so the engine-side and worker-side paths cannot disagree about them.
func TestRunAppliesTheDefaults(t *testing.T) {
	got, err := csvimport.Run(csvimport.Job{Source: "a,b\n1,2\n", HasHeader: true})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.ResultVariable != "rows" {
		t.Errorf("result variable = %q, want the default rows", got.ResultVariable)
	}
}

// A headerless file must name its columns; without them there is nothing to key the
// JSON objects by, and silently producing an empty batch is the failure mode the
// connector exists to avoid.
func TestRunRejectsAHeaderlessFileWithNoColumns(t *testing.T) {
	if _, err := csvimport.Run(csvimport.Job{Source: "1,2\n"}); err == nil {
		t.Error("a headerless file with no columns parsed, want an error")
	}
}

// An empty source is an error rather than an empty batch, for the same reason.
func TestRunRejectsAnEmptySource(t *testing.T) {
	if _, err := csvimport.Run(csvimport.Job{Source: "  ", HasHeader: true}); err == nil {
		t.Error("an empty source parsed, want an error")
	}
}

// Columns and a delimiter travel as plain values and are honoured.
func TestRunHonoursColumnsAndDelimiter(t *testing.T) {
	got, err := csvimport.Run(csvimport.Job{
		Source: "Ada;12\nGrace;7\n", Delimiter: ";", Columns: []string{"name", "amount"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(got.RowsJSON), &rows); err != nil {
		t.Fatalf("rows are not JSON: %v", err)
	}
	if len(rows) != 2 || rows[0]["name"] != "Ada" {
		t.Errorf("rows = %v, want the semicolon-separated rows keyed by the given columns", rows)
	}
}

// TestHeaderlessColumnsAreParsedByPosition is a regression: a headerless CSV
// connector task could not run at all. The compiler requires such a task to list its
// columns, the parser requires an index for each when there is no header row, and
// nothing was supplying one — so a model the compiler accepted failed at runtime
// with "column needs an index". Without a header the listed order *is* the file's
// order, which is the only thing the modeler's column list can mean.
func TestHeaderlessColumnsAreParsedByPosition(t *testing.T) {
	got, err := csvimport.Run(csvimport.Job{
		Source:  "ada@x.io,users\ngrace@x.io,admins\n",
		Columns: []string{"email", "group"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(got.RowsJSON), &rows); err != nil {
		t.Fatalf("rows are not JSON: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("%d rows, want 2", len(rows))
	}
	if rows[0]["email"] != "ada@x.io" || rows[0]["group"] != "users" {
		t.Errorf("first row = %v, want the columns keyed in the order they were listed", rows[0])
	}
}

// A file the parser refuses is an error, not an empty batch. This is the whole
// reason Run validates rather than returning whatever it managed: an import that
// silently produced zero rows would look like a successful run of an empty file.
// A multi-character delimiter is the reachable case — the reader itself is
// deliberately tolerant of ragged rows and stray quotes (dirty data is the norm),
// so what fails here is a model that asked for something the format cannot express.
func TestRunRejectsAFileItCannotParse(t *testing.T) {
	_, err := csvimport.Run(csvimport.Job{
		Source:    "name;;amount\nAda;;12\n",
		Delimiter: ";;",
		HasHeader: true,
	})
	if err == nil {
		t.Fatal("an unusable delimiter returned no error")
	}
	if !strings.Contains(err.Error(), "csv-import") {
		t.Errorf("error = %v, want it to name the connector", err)
	}
}

// A blank entry in the column list is skipped rather than becoming a nameless column.
// It matters most for a headerless file, where the listed order *is* the file's
// order: counting a blank would shift every column after it by one.
func TestRunSkipsBlankColumnNames(t *testing.T) {
	got, err := csvimport.Run(csvimport.Job{
		Source:  "Ada,12\nGrace,7\n",
		Columns: []string{"name", "   ", "amount"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(got.RowsJSON), &rows); err != nil {
		t.Fatalf("rows are not JSON: %v (%s)", err, got.RowsJSON)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %v, want 2", rows)
	}
	// Neither column declares a type, so both cells stay the file's raw text.
	if rows[0]["name"] != "Ada" || rows[0]["amount"] != "12" {
		t.Errorf("row = %v, want amount to have kept the second position despite the blank", rows[0])
	}
}
