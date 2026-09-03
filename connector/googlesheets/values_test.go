package googlesheets_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	gs "github.com/pblumer/atlas/connector/googlesheets"
)

// TestRowsFromListOfLists: the canonical shape. A list of lists is what Sheets writes,
// so it passes through untouched — including a short row, which Sheets treats as
// leaving the remaining cells alone rather than as an error.
func TestRowsFromListOfLists(t *testing.T) {
	in := []any{
		[]any{"Anna", json.Number("42"), true},
		[]any{"Bo"},
	}
	got, err := gs.Rows(in, nil)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	want := [][]any{{"Anna", json.Number("42"), true}, {"Bo"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Rows = %#v; want %#v", got, want)
	}
}

// TestRowsFromFlatList: a list of scalars is one row, which is what "append this
// record" means when the record is already a list of cells.
func TestRowsFromFlatList(t *testing.T) {
	got, err := gs.Rows([]any{"Anna", json.Number("42")}, nil)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	want := [][]any{{"Anna", json.Number("42")}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Rows = %#v; want %#v", got, want)
	}
}

// TestRowsFromScalar: writing one value into a one-cell range is a real thing a
// process does (a status, a timestamp), and making it author [["done"]] would be
// ceremony for nothing.
func TestRowsFromScalar(t *testing.T) {
	got, err := gs.Rows("done", nil)
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	if !reflect.DeepEqual(got, [][]any{{"done"}}) {
		t.Errorf("Rows = %#v; want [[done]]", got)
	}
}

// TestRowsFromContextsUsesColumns: the shape a process actually holds. The columns
// name the fields AND their order, which is the half that has to match the sheet.
func TestRowsFromContextsUsesColumns(t *testing.T) {
	in := []any{
		map[string]any{"name": "Anna", "amount": json.Number("42"), "extra": "ignored"},
		map[string]any{"name": "Bo"},
	}
	got, err := gs.Rows(in, []string{"amount", "name"})
	if err != nil {
		t.Fatalf("Rows: %v", err)
	}
	want := [][]any{{json.Number("42"), "Anna"}, {nil, "Bo"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Rows = %#v; want %#v", got, want)
	}
}

// TestRowsRefusesContextsWithoutColumns is the case this design exists to refuse
// rather than guess: a context's key order is not a column order, and picking one
// would turn a wrong answer into a silently transposed table.
func TestRowsRefusesContextsWithoutColumns(t *testing.T) {
	_, err := gs.Rows([]any{map[string]any{"name": "Anna"}}, nil)
	if err == nil {
		t.Fatal("Rows on contexts with no columns: want an error, got nil")
	}
	if !strings.Contains(err.Error(), "columns") {
		t.Errorf("error %q should name the fix (columns)", err)
	}
}

// TestRowsRefusesNoValues: an empty write is a call that changes nothing, and the
// failure is far cheaper here than as a Sheets 400.
func TestRowsRefusesNoValues(t *testing.T) {
	for name, in := range map[string]any{"nil": nil, "empty list": []any{}, "empty string": ""} {
		if _, err := gs.Rows(in, nil); err == nil {
			t.Errorf("Rows(%s): want an error, got nil", name)
		}
	}
}

// TestRowsRefusesMixedShapes: half rows and half scalars has no reading that is not a
// guess about what the author meant.
func TestRowsRefusesMixedShapes(t *testing.T) {
	_, err := gs.Rows([]any{[]any{"a"}, "b"}, nil)
	if err == nil {
		t.Fatal("Rows on a mixed list: want an error, got nil")
	}
}

// TestWithHeaderKeysRowsByTheFirstRow: what makes a read usable by a multi-instance
// subprocess or a gateway, instead of a list of positional lists.
func TestWithHeaderKeysRowsByTheFirstRow(t *testing.T) {
	rows := [][]any{
		{"name", "amount"},
		{"Anna", json.Number("42")},
		{"Bo"}, // short row: the missing cell is empty, not absent
	}
	got := gs.WithHeader(rows)
	want := []any{
		map[string]any{"name": "Anna", "amount": json.Number("42")},
		map[string]any{"name": "Bo", "amount": ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("WithHeader = %#v; want %#v", got, want)
	}
}

// TestWithHeaderOnEmptyRangeIsEmpty: a sheet with only a header row, or none at all,
// yields no records — an empty list rather than a null, so `count(rows)` is 0 rather
// than an error in the model that reads it.
func TestWithHeaderOnEmptyRangeIsEmpty(t *testing.T) {
	for _, rows := range [][][]any{nil, {{"name", "amount"}}} {
		got := gs.WithHeader(rows)
		if got == nil || len(got) != 0 {
			t.Errorf("WithHeader(%v) = %#v; want an empty list", rows, got)
		}
	}
}

// TestSpreadsheetID accepts what a person actually copies: the id, or the whole URL
// out of the browser's address bar.
func TestSpreadsheetID(t *testing.T) {
	const id = "1BxiMVs0XRA5nFMdKvBdBZjgmUUqptlbs74OgvE2upms"
	for _, in := range []string{
		id,
		"https://docs.google.com/spreadsheets/d/" + id + "/edit#gid=0",
		"https://docs.google.com/spreadsheets/d/" + id,
		"  " + id + "  ",
	} {
		if got := gs.SpreadsheetID(in); got != id {
			t.Errorf("SpreadsheetID(%q) = %q; want %q", in, got, id)
		}
	}
}

// TestFolderID accepts a Drive folder id or the URL of the folder page.
func TestFolderID(t *testing.T) {
	const id = "1AbCdEfGhIjKlMnOpQrStUvWxYz"
	for _, in := range []string{id, "https://drive.google.com/drive/folders/" + id + "?usp=sharing"} {
		if got := gs.FolderID(in); got != id {
			t.Errorf("FolderID(%q) = %q; want %q", in, got, id)
		}
	}
}
