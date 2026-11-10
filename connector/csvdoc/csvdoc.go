// Package csvdoc writes a source's records into a CSV document as they arrive, for the
// Worker Types whose result can be written as a document instead of into a variable:
// the Active Directory and LDAP searches and the SQL query
// (docs/planning/ad-search-as-document.md).
//
// Each of them answers a different question than its variable form does. The variable
// form answers "is it there, and what is it?" about a few records, within a variable's
// budget. The document form reads a whole source to check it: tens of thousands of
// records, written to a file a business rule task over a file then checks record by
// record (ADR-0548). The three sources share this package so the file means one thing
// whichever of them wrote it: the same CSV dialect, the same escaping of a cell with
// several values, the same refusal to store a cut file.
package csvdoc

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"strings"

	"github.com/pblumer/atlas/limits"
)

// ContentType is what every document this package writes is stored as.
const ContentType = "text/csv; charset=utf-8"

// Separator joins the values of a multi-valued cell. A separator or backslash inside a
// value is escaped with a backslash, so a cell splits back into exactly its values. A
// rule tests membership with list contains(split(row.memberOf, "[|]"), …): a bracket
// expression rather than "\|", because it needs no escape in a FEEL string.
const Separator = "|"

// Store is where a source written as a document stores its file: the document store,
// reached for the job's own instance (ADR-0543). In process that is the server's store;
// on a Worker Instance it is the upload under the job's lease.
type Store interface {
	StoreDocument(ctx context.Context, name, contentType string, data []byte) (map[string]any, error)
}

// limiter is a store that says how large a document it accepts.
type limiter interface{ DocumentLimit() int64 }

// Limit is how large the document may grow: the limit the activating server sent with
// the job, else the one the store reports, else the default budget. The job's comes
// first because on a Worker Instance it is the only one that is the server's own; in
// process the store reports the same configured value.
func Limit(jobLimit int64, store Store) int64 {
	if jobLimit > 0 {
		return jobLimit
	}
	if l, ok := store.(limiter); ok && l.DocumentLimit() > 0 {
		return l.DocumentLimit()
	}
	return limits.Default().Document
}

// Writer writes rows into a CSV document held in memory, and refuses a row that
// would take the document past its limit, or past the cap on the number of rows.
// Neither limit truncates: the caller fails the job, because a cut file that looks
// complete is a wrong answer, not a partial one.
type Writer struct {
	b    Bounds
	rows int
	buf  bytes.Buffer
	cw   *csv.Writer
	err  error
}

// Bounds are what a document may hold, and how a refusal names what it holds.
type Bounds struct {
	// What names the source in a refusal: "ad: the search under ou=x", "sqldb: the query".
	What string
	// Unit and Units name one record and several: "entry"/"entries", "row"/"rows".
	Unit, Units string
	// Limit bounds the document's size in bytes (see [Limit]).
	Limit int64
	// Max bounds the number of records; 0 is no cap.
	Max int
}

// NewWriter starts a document with header as its first row.
func NewWriter(header []string, b Bounds) *Writer {
	if b.Unit == "" {
		b.Unit, b.Units = "row", "rows"
	}
	w := &Writer{b: b}
	w.cw = csv.NewWriter(&w.buf)
	w.err = w.cw.Write(header)
	return w
}

// Write adds one row, or says why it cannot.
func (w *Writer) Write(row []string) error {
	if w.err != nil {
		return w.err
	}
	w.rows++
	if w.b.Max > 0 && w.rows > w.b.Max {
		return fmt.Errorf("%s returned more than the %d-%s cap; narrow it or raise the cap (truncating would be a wrong answer, not a partial one)", w.b.What, w.b.Max, w.b.Unit)
	}
	if err := w.cw.Write(row); err != nil {
		return err
	}
	if int64(w.Size()) > w.b.Limit {
		return fmt.Errorf("%s is larger than the %d bytes a document may be (ATLAS_LIMIT_DOCUMENT) after %d %s; narrow it or select fewer columns", w.b.What, w.b.Limit, w.rows, w.b.Units)
	}
	return nil
}

// Rows is how many rows have been written, the header not counted.
func (w *Writer) Rows() int { return w.rows }

// Size is how many bytes the document holds so far, including what the CSV writer
// still buffers.
func (w *Writer) Size() int {
	w.cw.Flush()
	return w.buf.Len()
}

// Bytes is the finished document.
func (w *Writer) Bytes() ([]byte, error) {
	w.cw.Flush()
	if err := w.cw.Error(); err != nil {
		return nil, err
	}
	return w.buf.Bytes(), w.err
}

// Store stores the finished document under name and answers its reference.
func (w *Writer) Store(ctx context.Context, store Store, name string) (map[string]any, error) {
	data, err := w.Bytes()
	if err != nil {
		return nil, err
	}
	ref, err := store.StoreDocument(ctx, name, ContentType, data)
	if err != nil {
		return nil, fmt.Errorf("store the result as a document: %w", err)
	}
	return ref, nil
}

// JoinValues puts a cell's values in one string, escaping the separator and the escape
// character inside a value so the cell splits back into exactly those values. No
// values is an empty cell.
func JoinValues(vals []string) string {
	if len(vals) == 1 && !strings.ContainsAny(vals[0], `|\`) {
		return vals[0]
	}
	esc := make([]string, len(vals))
	for i, v := range vals {
		v = strings.ReplaceAll(v, `\`, `\\`)
		esc[i] = strings.ReplaceAll(v, Separator, `\`+Separator)
	}
	return strings.Join(esc, Separator)
}

// DNColumn is the first column of a directory's document: the one value that names an
// entry, and what a later correction needs.
const DNColumn = "dn"

// EntryRow is a directory entry as a row: its DN, then one cell per attribute in attrs,
// in that order. Attribute names are matched without regard to case, as LDAP matches
// them, because a server answers in its own spelling; an attribute the entry lacks is
// an empty cell.
func EntryRow(dn string, entry map[string][]string, attrs []string, into []string) []string {
	byName := make(map[string][]string, len(entry))
	for name, vals := range entry {
		byName[strings.ToLower(name)] = vals
	}
	into = append(into[:0], dn)
	for _, a := range attrs {
		into = append(into, JoinValues(byName[strings.ToLower(a)]))
	}
	return into
}

// Summary is what a source written as a document completes with: found and count keep
// the meaning they have in the variable form, so a model's gateways read the same, and
// document is the reference to the records. A directory adds the smallest DN as dn.
func Summary(count int, ref map[string]any) map[string]any {
	return map[string]any{
		"found":    count > 0,
		"count":    count,
		"document": ref,
	}
}
