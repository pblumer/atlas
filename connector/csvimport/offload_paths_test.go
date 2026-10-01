package csvimport

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
)

// The write direction (a file rendered from rows), end to end through the handler a
// compiled atlas:csvConnector task reaches, and the refusals on the way.

// csvTaskProcess compiles start → CSV task → end and returns the process and the
// task's element id.
func csvTaskProcess(t *testing.T, cfg compiler.CsvConfig) (*compiler.CompiledProcess, int32) {
	t.Helper()
	b := compiler.NewBuilder(7, "export", 1)
	start := b.AddStartEvent()
	task := b.AddCsvConnectorTask(cfg)
	end := b.AddEndEvent()
	b.Connect(start, task)
	b.Connect(task, end)
	cp, err := b.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return cp, task
}

// TestTheHandlerWritesAFixedWidthFileFromRows: the compiled layout — column order,
// widths, a blank column skipped — reaches the renderer, and the result is the file
// as text with the number of records written.
func TestTheHandlerWritesAFixedWidthFileFromRows(t *testing.T) {
	cp, task := csvTaskProcess(t, compiler.CsvConfig{
		Source: "people", Result: "file", Format: FormatFixedWidth, Operation: OperationWrite,
		Columns: []string{"name", "", "amount"}, Widths: []int32{5, 9, 3},
	})
	store := fakeVarStore{
		ei: map[uint64]model.ElementInstanceValue{1: {ProcessDefKey: 7, ElementId: task}},
		vars: map[uint64][]model.VariableValue{1: {
			{Name: "people", Kind: model.VarJSON, Text: `[{"name":"Ada","amount":12},{"name":"Grace Hopper","amount":7}]`},
		}},
	}
	lookup := func(defKey uint64) *compiler.CompiledProcess {
		if defKey == 7 {
			return cp
		}
		return nil
	}

	out, err := Handler(store, lookup)(job.Job{ElementInstanceKey: 1})
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("outputs = %+v, want the file and the row count", out)
	}
	// The blank column is skipped, and widths stay with the columns they were authored
	// beside: "amount" keeps its own 3, not the skipped column's 9. "Grace Hopper" is
	// cut to the 5 characters its column holds.
	want := "Ada  12 \n" + "Grace7  \n"
	if out[0] != (model.VariableValue{Name: "file", Kind: model.VarString, Text: want}) {
		t.Fatalf("file = %+v, want %q", out[0], want)
	}
	if out[1].Name != csvRowCountVar || out[1].Text != "2" {
		t.Fatalf("rowCount = %+v, want 2", out[1])
	}
}

// TestWritingNeedsRowsNotText: the source of a write is the structured rows, so text
// in that variable is refused with the variable's name, before anything is rendered.
func TestWritingNeedsRowsNotText(t *testing.T) {
	cp, task := csvTaskProcess(t, compiler.CsvConfig{Source: "people", Operation: OperationWrite})
	store := fakeVarStore{vars: map[uint64][]model.VariableValue{1: {
		{Name: "people", Kind: model.VarString, Text: "name,amount\nAda,12\n"},
	}}}
	_, err := Resolve(store, cp, cp.ConnectorTask(cp.Node(task).Detail), 1)
	if err == nil || !strings.Contains(err.Error(), `source variable "people" must hold the rows to write`) {
		t.Fatalf("Resolve = %v, want the write refused for text", err)
	}
}

// TestRunRefusesWhatItCannotWrite: each way a write job can be unusable is named.
func TestRunRefusesWhatItCannotWrite(t *testing.T) {
	for _, tc := range []struct {
		name string
		job  Job
		want string
	}{
		{"rows that are not an array of objects",
			Job{Operation: OperationWrite, Source: `{"name":"Ada"}`},
			"the rows to write are not a JSON array of objects"},
		{"a fixed-width column with no width",
			Job{Operation: OperationWrite, Format: FormatFixedWidth, Source: `[{"name":"Ada"}]`,
				Columns: []Column{{Name: "name"}}},
			`fixed-width column "name" needs a positive width`},
		{"a delimiter that is not one character",
			Job{Operation: OperationWrite, Delimiter: "||", Source: `[{"name":"Ada"}]`},
			"delimiter must be a single character"},
		{"a format nobody reads",
			Job{Operation: OperationWrite, Format: "xlsx", Source: `[]`},
			`unknown format "xlsx"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Run(tc.job); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Run = %v, want an error containing %q", err, tc.want)
			}
		})
	}
}

// TestUnconfiguredRowsWriteEveryFieldOnce: with no layout, the columns are every
// field any row carries, once, sorted — a field shared by several rows is not
// repeated, and a row without it writes an empty cell.
func TestUnconfiguredRowsWriteEveryFieldOnce(t *testing.T) {
	res, err := Run(Job{Operation: OperationWrite, HasHeader: true,
		Source: `[{"b":2,"a":1},{"a":3},{"c":true,"a":4}]`})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := "a,b,c\n1,2,\n3,,\n4,,true\n"; res.Text != want || !res.IsText || res.RowCount != 3 {
		t.Fatalf("Run = %+v, want text %q with 3 rows", res, want)
	}
	vars, err := res.Variables()
	if err != nil {
		t.Fatalf("Variables: %v", err)
	}
	if b, _ := json.Marshal(vars); string(b) != `{"rowCount":3,"rows":"a,b,c\n1,2,\n3,,\n4,,true\n"}` {
		t.Fatalf("Variables = %s, want the file under the default name", b)
	}
}

// TestAnIntegerColumnKeepsWhatIsNotAnInteger: a cell that will not coerce stays the
// text it was rather than becoming zero or vanishing.
func TestAnIntegerColumnKeepsWhatIsNotAnInteger(t *testing.T) {
	if got := coerceCell(" 42 ", "integer"); got != json.Number("42") {
		t.Errorf("coerceCell(42) = %#v, want json.Number 42", got)
	}
	if got := coerceCell("n/a", "integer"); got != "n/a" {
		t.Errorf("coerceCell(n/a) = %#v, want the raw text", got)
	}
}
