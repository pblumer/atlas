package ad

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"strings"

	goldap "github.com/go-ldap/ldap/v3"

	"github.com/pblumer/atlas/limits"
)

// A search written as a document (docs/planning/ad-search-as-document.md).
//
// A search's entries normally go into the result variable, which is what a
// provisioning process needs — "is this group there, and what is its DN?" — and which a
// variable's budget bounds at a few thousand entries. Reading a whole directory to check
// it is a different question with a different size: tens of thousands of users. Then the
// entries go into a CSV document, one row per entry, and the result variable names it,
// so a business rule task over a file can check every user (ADR-0548). The engine sees
// one completion with a reference; the directory never enters the log.

// DocumentStore is where a search written as a document stores its file: the
// document store, reached for the job's own instance. The completion binds what it
// stores to that instance (ADR-0543).
type DocumentStore interface {
	StoreDocument(ctx context.Context, name, contentType string, data []byte) (map[string]any, error)
}

// documentLimiter is a store that says how large a document it accepts. A store that
// does not is held to the default document budget.
type documentLimiter interface{ DocumentLimit() int64 }

// pagedSearcher is a connection that can hand a search over a page at a time, so a
// large read holds one page rather than the whole result. A connection without it is
// read whole, which is right for a test double and for small directories.
type pagedSearcher interface {
	SearchPages(req SearchRequest, fn func([]Entry) error) error
}

// The columns a search writes, and how a cell holds several values.
const (
	dnColumn = "dn"
	// valueSeparator joins the values of a multi-valued attribute in one cell. A
	// separator or backslash inside a value is escaped with a backslash, so a value
	// can be split back exactly: a rule tests membership with
	// list contains(split(row.memberOf, "\|"), …).
	valueSeparator = "|"
)

// runSearchDocument reads what is under the base into a CSV document and completes
// with {found, count, dn, document}: found, count and the first DN keep the meaning
// they have in the variable form, so a model's gateways read the same; entries are in
// the document instead.
//
// The rows are in the order the directory sent them, not sorted: sorting would mean
// holding the whole result, which is what this form exists to avoid. dn is still the
// smallest DN of the result, as in the variable form, because it is tracked as the
// pages arrive.
func runSearchDocument(ctx context.Context, j Job, conn Conn, docs DocumentStore) (map[string]any, error) {
	if docs == nil {
		return nil, fmt.Errorf("ad: this search writes its result as a document, and where it runs there is no document store to write it to")
	}
	if strings.TrimSpace(j.BaseDN) == "" {
		return nil, fmt.Errorf("ad: search resolved an empty baseDN; a search has to start somewhere in the tree (is the variable behind it set?)")
	}
	if len(j.Select) == 0 {
		return nil, fmt.Errorf("ad: a search written as a document needs the attributes it writes as columns (select)")
	}
	limit := documentLimit(j, docs)
	w := newEntryWriter(j.Select)
	count := 0
	first := ""
	each := func(entries []Entry) error {
		for _, e := range entries {
			count++
			if j.MaxEntries > 0 && count > j.MaxEntries {
				return fmt.Errorf("ad: the search under %s returned more than the %d-entry cap; narrow the filter or raise maxEntries (truncating would be a wrong answer, not a partial one)", j.BaseDN, j.MaxEntries)
			}
			if first == "" || e.DN < first {
				first = e.DN
			}
			if err := w.write(e); err != nil {
				return err
			}
			if int64(w.size()) > limit {
				return fmt.Errorf("ad: the search under %s is larger than the %d bytes a document may be (ATLAS_LIMIT_DOCUMENT) after %d entries; narrow the filter or select fewer attributes", j.BaseDN, limit, count)
			}
		}
		return ctx.Err()
	}
	req := SearchRequest{
		BaseDN:     j.BaseDN,
		Scope:      j.Scope,
		Filter:     j.Filter,
		Attributes: j.Select,
		PageSize:   adSearchPageSize,
	}
	var err error
	if ps, ok := conn.(pagedSearcher); ok {
		err = ps.SearchPages(req, each)
	} else {
		var entries []Entry
		req.MaxEntries = int32(j.MaxEntries)
		if entries, err = conn.Search(req); err == nil {
			err = each(entries)
		}
	}
	if err != nil {
		return nil, err
	}
	data, err := w.bytes()
	if err != nil {
		return nil, err
	}
	name := j.DocumentName
	if name == "" {
		name = "ad-search.csv"
	}
	ref, err := docs.StoreDocument(ctx, name, "text/csv; charset=utf-8", data)
	if err != nil {
		return nil, fmt.Errorf("ad: store the search result: %w", err)
	}
	out := make(map[string]any, 2)
	if j.ResultVariable != "" {
		out[j.ResultVariable] = map[string]any{
			"found":    count > 0,
			"count":    count,
			"dn":       first,
			"document": ref,
		}
	}
	if j.FoundVariable != "" {
		out[j.FoundVariable] = count > 0
	}
	return out, nil
}

// documentLimit is how large the document may grow: the limit the activating server
// sent with the job, else the one the store reports, else the default budget. The job's
// comes first because on a Worker Instance it is the only one that is the server's own;
// in process the store reports the same configured value.
func documentLimit(j Job, docs DocumentStore) int64 {
	if j.DocumentLimit > 0 {
		return j.DocumentLimit
	}
	if l, ok := docs.(documentLimiter); ok && l.DocumentLimit() > 0 {
		return l.DocumentLimit()
	}
	return limits.Default().Document
}

// entryWriter writes entries as CSV rows: the DN, then one column per selected
// attribute in the order selected. Attribute names are matched without regard to case,
// as LDAP matches them, because a server answers in its own spelling.
type entryWriter struct {
	attrs []string
	buf   bytes.Buffer
	cw    *csv.Writer
	row   []string
	err   error
}

func newEntryWriter(attrs []string) *entryWriter {
	w := &entryWriter{attrs: attrs}
	w.cw = csv.NewWriter(&w.buf)
	w.err = w.cw.Write(append([]string{dnColumn}, attrs...))
	return w
}

func (w *entryWriter) write(e Entry) error {
	if w.err != nil {
		return w.err
	}
	byName := make(map[string][]string, len(e.Attributes))
	for name, vals := range e.Attributes {
		byName[strings.ToLower(name)] = vals
	}
	w.row = append(w.row[:0], e.DN)
	for _, a := range w.attrs {
		w.row = append(w.row, joinValues(byName[strings.ToLower(a)]))
	}
	return w.cw.Write(w.row)
}

// size is how many bytes the document holds so far, including what the CSV writer
// still buffers.
func (w *entryWriter) size() int {
	w.cw.Flush()
	return w.buf.Len()
}

func (w *entryWriter) bytes() ([]byte, error) {
	w.cw.Flush()
	if err := w.cw.Error(); err != nil {
		return nil, err
	}
	return w.buf.Bytes(), w.err
}

// joinValues puts an attribute's values in one cell, escaping the separator and the
// escape character inside a value so the cell splits back into exactly those values.
// An attribute with no values is an empty cell.
func joinValues(vals []string) string {
	if len(vals) == 1 && !strings.ContainsAny(vals[0], `|\`) {
		return vals[0]
	}
	esc := make([]string, len(vals))
	for i, v := range vals {
		v = strings.ReplaceAll(v, `\`, `\\`)
		esc[i] = strings.ReplaceAll(v, valueSeparator, `\`+valueSeparator)
	}
	return strings.Join(esc, valueSeparator)
}

// SearchPages reads a search a page at a time with LDAP's simple paged-results
// control, handing each page to fn before asking for the next, so a large read holds
// one page. An error from fn stops the read and is returned as it is.
func (c *goConn) SearchPages(req SearchRequest, fn func([]Entry) error) error {
	filter := req.Filter
	if filter == "" {
		filter = "(objectClass=*)"
	}
	size := req.PageSize
	if size <= 0 {
		size = adSearchPageSize
	}
	paging := goldap.NewControlPaging(uint32(size))
	sr := goldap.NewSearchRequest(
		req.BaseDN, searchScope(req.Scope), goldap.NeverDerefAliases,
		0, 0, false, filter, req.Attributes, []goldap.Control{paging},
	)
	for {
		res, err := c.conn.Search(sr)
		if err != nil {
			return fmt.Errorf("ad: search %s: %w", req.BaseDN, err)
		}
		if err := fn(entriesFrom(res)); err != nil {
			return err
		}
		next, ok := goldap.FindControl(res.Controls, goldap.ControlTypePaging).(*goldap.ControlPaging)
		if !ok || len(next.Cookie) == 0 {
			return nil
		}
		paging.SetCookie(next.Cookie)
	}
}

// SearchPages hands the mock's search over in pages of the requested size, so a test
// of a large read goes through the same paging the real directory does.
func (c *mockConn) SearchPages(req SearchRequest, fn func([]Entry) error) error {
	req.MaxEntries = 0 // the caller counts against its cap as the pages arrive
	entries, err := c.Search(req)
	if err != nil {
		return err
	}
	size := int(req.PageSize)
	if size <= 0 {
		size = adSearchPageSize
	}
	for len(entries) > 0 {
		n := min(size, len(entries))
		if err := fn(entries[:n]); err != nil {
			return err
		}
		entries = entries[n:]
	}
	return nil
}
