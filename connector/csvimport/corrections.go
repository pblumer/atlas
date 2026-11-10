package csvimport

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/pblumer/atlas/connector/csvdoc"
)

// Writing corrections back (ADR-0548, S2).
//
// A check of a file ends with corrected *records*: the rows a person fixed in a task
// each, or the file of failures they downloaded, fixed and uploaded again. What they
// need is a corrected *file*, the one they load back into the system the records came
// from. apply-corrections writes it: it streams the source, replaces each record a
// correction names by its line, drops the ones marked to drop, and stores the result
// as a new document. The source is never changed — a document is immutable
// (ADR-0543), and keeping both is what makes the correction reviewable.
//
// It refuses what would change the file unnoticed: a correction for a line the source
// does not have, a line corrected twice, a field the source has no column for. Fields
// whose names start with an underscore are the check's own (_valid, a reason, _error)
// and are ignored, except _row, which names the line, and _drop, which drops it.

// OperationApplyCorrections writes a corrected copy of an uploaded file.
const OperationApplyCorrections = "apply-corrections"

// DocumentStore is the document store as apply-corrections reaches it: reads only
// what the job's instance was given, and stores the corrected file as a new document
// the job's completion binds (ADR-0543).
type DocumentStore interface {
	ReadDocument(ctx context.Context, id string) ([]byte, string, string, error)
	StoreDocument(ctx context.Context, name, contentType string, data []byte) (map[string]any, error)
}

// The fields of a correction that are not columns of the file.
const (
	correctionRow  = "_row"
	correctionDrop = "_drop"
)

// correction is one record's replacement: the fields to set, or drop.
type correction struct {
	line   int
	fields map[string]string
	drop   bool
	used   bool
}

// ApplyCorrections runs an apply-corrections job: the source and, where they are a
// document, the corrections are read through docs, and the corrected file is stored
// through it. It completes with {document, source, total, replaced, dropped, records}.
func ApplyCorrections(ctx context.Context, j Job, docs DocumentStore) (map[string]any, error) {
	if docs == nil {
		return nil, fmt.Errorf("csv-import: apply-corrections writes a new document, and where it runs there is no document store to write it to")
	}
	if j.SourceDocument == "" {
		return nil, fmt.Errorf("csv-import: apply-corrections corrects an uploaded file, and the source holds none")
	}
	result := strings.TrimSpace(j.Result)
	if result == "" {
		return nil, fmt.Errorf("csv-import: apply-corrections needs a result variable for the corrected file")
	}
	src, srcName, _, err := docs.ReadDocument(ctx, j.SourceDocument)
	if err != nil {
		return nil, fmt.Errorf("csv-import: read the source %s: %w", j.SourceDocument, err)
	}
	records := j.Corrections
	if j.CorrectionsDocument != "" {
		data, _, _, err := docs.ReadDocument(ctx, j.CorrectionsDocument)
		if err != nil {
			return nil, fmt.Errorf("csv-import: read the corrections %s: %w", j.CorrectionsDocument, err)
		}
		if records, err = correctionsFromFile(data, j.Delimiter); err != nil {
			return nil, err
		}
	}
	byLine, err := correctionsByLine(records, j.Drop)
	if err != nil {
		return nil, err
	}
	data, stats, err := correctFile(src, j.Delimiter, byLine)
	if err != nil {
		return nil, err
	}
	limit := csvdoc.Limit(j.DocumentLimit, docs)
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("csv-import: the corrected file is larger than the %d bytes a document may be (ATLAS_LIMIT_DOCUMENT)", limit)
	}
	name := j.DocumentName
	if name == "" {
		name = correctedName(srcName)
	}
	ref, err := docs.StoreDocument(ctx, name, "text/csv; charset=utf-8", data)
	if err != nil {
		return nil, fmt.Errorf("csv-import: store the corrected file: %w", err)
	}
	return map[string]any{result: map[string]any{
		"document": ref,
		"source":   map[string]any{"$document": j.SourceDocument, "name": srcName},
		"total":    stats.total,
		"replaced": stats.replaced,
		"dropped":  stats.dropped,
		"records":  stats.total - stats.dropped,
	}}, nil
}

// correctionsByLine indexes the corrections by the line they replace, adding the lines
// to drop, and refuses a correction that does not say which line it is, or a line
// named twice.
func correctionsByLine(records []map[string]any, drop []int) (map[int]*correction, error) {
	out := make(map[int]*correction, len(records)+len(drop))
	for i, r := range records {
		line, err := lineOf(r[correctionRow])
		if err != nil {
			return nil, fmt.Errorf("csv-import: correction %d: %v", i+1, err)
		}
		if out[line] != nil {
			return nil, fmt.Errorf("csv-import: line %d is corrected twice", line)
		}
		c := &correction{line: line, fields: map[string]string{}, drop: truthy(r[correctionDrop])}
		for k, v := range r {
			if strings.HasPrefix(k, "_") {
				continue // the check's own fields: _valid, a reason, _error
			}
			c.fields[k] = fieldText(v)
		}
		out[line] = c
	}
	for _, line := range drop {
		if c := out[line]; c != nil && !c.drop {
			return nil, fmt.Errorf("csv-import: line %d is both corrected and dropped", line)
		}
		out[line] = &correction{line: line, drop: true}
	}
	return out, nil
}

// lineOf reads a correction's _row: a whole number, as the check wrote it.
func lineOf(v any) (int, error) {
	var n float64
	switch x := v.(type) {
	case nil:
		return 0, fmt.Errorf("it names no _row, the line it replaces")
	case float64:
		n = x
	case int:
		n = float64(x)
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return 0, fmt.Errorf("_row %q is not a line number", x)
		}
		n = f
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		if err != nil {
			return 0, fmt.Errorf("_row %q is not a line number", x)
		}
		n = f
	default:
		return 0, fmt.Errorf("_row %v is not a line number", v)
	}
	if n < 1 || n != float64(int(n)) {
		return 0, fmt.Errorf("_row %v is not a line number", v)
	}
	return int(n), nil
}

// truthy reads _drop: true, or a cell a person would write for yes.
func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "true", "1", "x", "ja", "yes", "j", "y":
			return true
		}
	}
	return false
}

// fieldText is a corrected value as a cell: text as it is, a number as it reads, a
// boolean as true or false, nothing as an empty cell.
func fieldText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return x.String()
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// correctionsFromFile reads a corrected file: a header row, then one record per
// correction, every cell text. It is the failures document the check wrote, after a
// person changed it.
func correctionsFromFile(data []byte, delimiter string) ([]map[string]any, error) {
	var out []map[string]any
	// The corrected file has been through a person's spreadsheet, which saves CSV with
	// the separator of its locale — a semicolon in German and Swiss Excel — whatever
	// the file had. Its own header row says which it is.
	cfg := Config{Delimiter: sniffDelimiter(data, delimiter)}
	_, err := EachRow(cfg, bytes.NewReader(data), func(rec Record) error {
		r := make(map[string]any, len(rec.Header))
		for i, name := range rec.Header {
			if i < len(rec.Cells) {
				r[name] = rec.Cells[i]
			}
		}
		out = append(out, r)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("csv-import: read the corrections: %v", err)
	}
	return out, nil
}

// delimiterCandidates are the separators a spreadsheet writes CSV with.
var delimiterCandidates = []byte{',', ';', '\t', '|'}

// sniffDelimiter reads a file's separator off its first line: the candidate that
// occurs there most often outside quotes. A line holding none of them — a file of one
// column — keeps the delimiter the task names.
func sniffDelimiter(data []byte, fallback string) string {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		data = data[:i]
	}
	counts := map[byte]int{}
	quoted := false
	for _, c := range data {
		if c == '"' {
			quoted = !quoted
			continue
		}
		if !quoted && bytes.IndexByte(delimiterCandidates, c) >= 0 {
			counts[c]++
		}
	}
	best, most := byte(0), 0
	for _, c := range delimiterCandidates {
		if counts[c] > most {
			best, most = c, counts[c]
		}
	}
	if most == 0 {
		return fallback
	}
	return string(best)
}

type correctionStats struct{ total, replaced, dropped int }

// correctFile streams the source and writes its corrected copy: the same header, the
// same delimiter, a byte-order mark if the source had one (Excel needs it to read
// UTF-8), every record not named as it was.
func correctFile(src []byte, delimiter string, byLine map[int]*correction) ([]byte, correctionStats, error) {
	var st correctionStats
	comma, err := delimiterRune(delimiter)
	if err != nil {
		return nil, st, err
	}
	var buf bytes.Buffer
	if bytes.HasPrefix(src, []byte{0xEF, 0xBB, 0xBF}) {
		buf.Write([]byte{0xEF, 0xBB, 0xBF})
	}
	w := csv.NewWriter(&buf)
	w.Comma = comma
	var index map[string]int
	var cells []string
	header, err := EachRow(Config{Delimiter: delimiter}, bytes.NewReader(src), func(rec Record) error {
		if index == nil {
			index = make(map[string]int, len(rec.Header))
			for i, name := range rec.Header {
				index[name] = i
			}
			if err := checkFields(byLine, index); err != nil {
				return err
			}
			if err := w.Write(rec.Header); err != nil {
				return err
			}
		}
		st.total++
		c := byLine[rec.Line]
		if c != nil {
			c.used = true
			if c.drop {
				st.dropped++
				return nil
			}
			st.replaced++
		}
		cells = append(cells[:0], rec.Cells...)
		for len(cells) < len(rec.Header) {
			cells = append(cells, "")
		}
		if c != nil {
			for name, v := range c.fields {
				cells[index[name]] = v
			}
		}
		return w.Write(cells)
	})
	if err != nil {
		return nil, st, fmt.Errorf("csv-import: %v", err)
	}
	if header == nil {
		return nil, st, fmt.Errorf("csv-import: apply-corrections needs a file with a header row")
	}
	var missing []int
	for line, c := range byLine {
		if !c.used {
			missing = append(missing, line)
		}
	}
	if len(missing) > 0 {
		sort.Ints(missing)
		if len(missing) > 5 {
			missing = missing[:5]
		}
		return nil, st, fmt.Errorf("csv-import: the source has no record on line %v; a correction replaces a record, it does not add one", missing)
	}
	w.Flush()
	return buf.Bytes(), st, w.Error()
}

// checkFields refuses a correction that names a field the file has no column for: it
// would reshape the record rather than correct it.
func checkFields(byLine map[int]*correction, index map[string]int) error {
	lines := make([]int, 0, len(byLine))
	for line := range byLine {
		lines = append(lines, line)
	}
	sort.Ints(lines)
	for _, line := range lines {
		for name := range byLine[line].fields {
			if _, ok := index[name]; !ok {
				return fmt.Errorf("the correction of line %d sets %q, which is not a column of the file", line, name)
			}
		}
	}
	return nil
}

// correctedName is the corrected file's name beside the source's: data.csv gives
// data.corrected.csv.
func correctedName(src string) string {
	name := path.Base(strings.TrimSpace(src))
	if name == "" || name == "." || name == "/" {
		name = "records.csv"
	}
	return strings.TrimSuffix(name, path.Ext(name)) + ".corrected.csv"
}
