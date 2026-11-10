package ad

import (
	"context"
	"fmt"
	"strings"

	goldap "github.com/go-ldap/ldap/v3"

	"github.com/pblumer/atlas/connector/csvdoc"
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
// stores to that instance (ADR-0543). The LDAP search and the SQL query write theirs
// through the same writer (connector/csvdoc), so the file means one thing whichever
// source wrote it.
type DocumentStore = csvdoc.Store

// pagedSearcher is a connection that can hand a search over a page at a time, so a
// large read holds one page rather than the whole result. A connection without it is
// read whole, which is right for a test double and for small directories.
type pagedSearcher interface {
	SearchPages(req SearchRequest, fn func([]Entry) error) error
}

// runSearchDocument reads what is under the base into a CSV document and completes
// with {found, count, dn, document}: found, count and the first DN keep the meaning
// they have in the variable form, so a model's gateways read the same; entries are in
// the document instead. The columns are dn, then select in its order; a multi-valued
// attribute is one cell, joined and escaped as csvdoc.JoinValues says.
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
	w := csvdoc.NewWriter(append([]string{csvdoc.DNColumn}, j.Select...), csvdoc.Bounds{
		What: "ad: the search under " + j.BaseDN, Unit: "entry", Units: "entries",
		Limit: csvdoc.Limit(j.DocumentLimit, docs), Max: j.MaxEntries,
	})
	first := ""
	var row []string
	each := func(entries []Entry) error {
		for _, e := range entries {
			if first == "" || e.DN < first {
				first = e.DN
			}
			row = csvdoc.EntryRow(e.DN, e.Attributes, j.Select, row)
			if err := w.Write(row); err != nil {
				return err
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
	name := j.DocumentName
	if name == "" {
		name = "ad-search.csv"
	}
	ref, err := w.Store(ctx, docs, name)
	if err != nil {
		return nil, fmt.Errorf("ad: %w", err)
	}
	out := make(map[string]any, 2)
	if j.ResultVariable != "" {
		res := csvdoc.Summary(w.Rows(), ref)
		res["dn"] = first
		out[j.ResultVariable] = res
	}
	if j.FoundVariable != "" {
		out[j.FoundVariable] = w.Rows() > 0
	}
	return out, nil
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
