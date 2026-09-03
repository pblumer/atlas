package googlesheets

import (
	"fmt"
	"regexp"
	"strings"
)

// Rows turns one resolved FEEL value into the rows Sheets writes. A value reaching
// here has already been evaluated against the variables the task saw, so what arrives
// is plain data — a list, a context, or a scalar.
//
// Three shapes are accepted, because three shapes are what a process holds
// (ADR-draft-google-sheets-worker):
//
//   - a list of lists — the rows verbatim, which is Sheets' own shape;
//   - a flat list of scalars — one row;
//   - a list of contexts — projected through columns, which name the fields AND their
//     order.
//
// A scalar is one cell, so writing a status into a one-cell range does not have to be
// authored as a list of one list of one.
//
// A list of contexts with no columns is refused rather than guessed at. A context's
// key order is not a column order; picking one would make the failure a silently
// transposed table, which is a defect nobody finds twice — where the refusal names the
// fix and costs one attribute.
func Rows(v any, columns []string) ([][]any, error) {
	list, ok := v.([]any)
	if !ok {
		// A scalar is one cell. A nil or an empty string is not: a write with nothing
		// in it changes nothing, and saying so here beats a Sheets 400.
		if v == nil || v == "" {
			return nil, fmt.Errorf("googlesheets: the task's values are empty; there is nothing to write")
		}
		return [][]any{{v}}, nil
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("googlesheets: the task's values are an empty list; there is nothing to write")
	}
	switch shapeOf(list) {
	case shapeRows:
		out := make([][]any, len(list))
		for i, r := range list {
			cells, _ := r.([]any)
			out[i] = cells
		}
		return out, nil
	case shapeContexts:
		if len(columns) == 0 {
			return nil, fmt.Errorf("googlesheets: the task's values are a list of objects, so it needs columns " +
				"naming the fields to write and their order (e.g. columns=\"name,amount\") — " +
				"an object's key order is not a column order, so it is not guessed")
		}
		out := make([][]any, len(list))
		for i, r := range list {
			obj, _ := r.(map[string]any)
			cells := make([]any, len(columns))
			for j, col := range columns {
				cells[j] = obj[col] // an absent field leaves the cell empty
			}
			out[i] = cells
		}
		return out, nil
	case shapeCells:
		return [][]any{list}, nil
	default:
		return nil, fmt.Errorf("googlesheets: the task's values mix rows, objects and plain cells in one list; " +
			"write a list of rows, a list of objects (with columns), or one row of cells")
	}
}

// The three list shapes Rows accepts, plus the mixture it refuses.
type shape int

const (
	shapeMixed shape = iota
	shapeRows
	shapeContexts
	shapeCells
)

// shapeOf classifies a list by what its elements are. A list is one shape or it is
// nothing: a half-and-half list has no reading that is not a guess about what the
// author meant.
func shapeOf(list []any) shape {
	var lists, contexts, cells int
	for _, el := range list {
		switch el.(type) {
		case []any:
			lists++
		case map[string]any:
			contexts++
		default:
			cells++
		}
	}
	switch {
	case lists == len(list):
		return shapeRows
	case contexts == len(list):
		return shapeContexts
	case cells == len(list):
		return shapeCells
	default:
		return shapeMixed
	}
}

// WithHeader turns the raw rows of a read into a list of contexts keyed by the first
// row's cells — the shape a multi-instance subprocess or a gateway can use, where a
// list of positional lists is one the model has to index into.
//
// A short row (Sheets omits trailing empty cells) yields empty strings rather than
// missing keys, so every record has the same fields and a FEEL path never hits a null
// it has to defend against. The result is always a list, never nil: a sheet holding
// only its header answers with no records, and `count(rows) = 0` is the reading a
// model expects.
func WithHeader(rows [][]any) []any {
	if len(rows) == 0 {
		return []any{}
	}
	names := make([]string, len(rows[0]))
	for i, cell := range rows[0] {
		names[i] = cellText(cell)
	}
	out := make([]any, 0, len(rows)-1)
	for _, row := range rows[1:] {
		rec := make(map[string]any, len(names))
		for i, name := range names {
			if name == "" {
				continue // an unnamed column cannot be addressed; its cells stay in no field
			}
			if i < len(row) {
				rec[name] = row[i]
				continue
			}
			rec[name] = ""
		}
		out = append(out, rec)
	}
	return out
}

// cellText renders a header cell as the field name it becomes. Sheets returns header
// cells as strings, but a numeric or boolean header is legal and has to name a field
// too.
func cellText(cell any) string {
	if cell == nil {
		return ""
	}
	if s, ok := cell.(string); ok {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(fmt.Sprint(cell))
}

// spreadsheetURL and folderURL match what a person copies out of the browser instead
// of hunting for the bare id. Accepting both is not indulgence: the id is only ever
// obtained *from* one of these URLs, so refusing the URL means every author performs
// the same manual extraction, and one of them gets it wrong.
var (
	spreadsheetURL = regexp.MustCompile(`/spreadsheets/d/([A-Za-z0-9_-]+)`)
	folderURL      = regexp.MustCompile(`/folders/([A-Za-z0-9_-]+)`)
)

// SpreadsheetID reads a spreadsheet id out of an id or a Google Sheets URL.
func SpreadsheetID(raw string) string { return matchID(raw, spreadsheetURL) }

// FolderID reads a Drive folder id out of an id or a Drive folder URL.
func FolderID(raw string) string { return matchID(raw, folderURL) }

func matchID(raw string, re *regexp.Regexp) string {
	raw = strings.TrimSpace(raw)
	if m := re.FindStringSubmatch(raw); m != nil {
		return m[1]
	}
	return raw
}
