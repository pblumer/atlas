package ldap

import (
	"context"
	"fmt"
	"strings"

	goldap "github.com/go-ldap/ldap/v3"

	"github.com/pblumer/atlas/connector/csvdoc"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// A search written as a document (docs/planning/ad-search-as-document.md, S3).
//
// The same form as Active Directory's, through the same writer (connector/csvdoc): one
// CSV row per entry, the DN, then the attributes in select in their order, a
// multi-valued attribute in one cell. The result variable holds {found, count, dn,
// document}, so a business rule task over a file reads the directory directly.

// DocumentStore is where a search written as a document stores its file.
type DocumentStore = csvdoc.Store

// pagedSearcher is a connection that can hand a search over a page at a time, so a
// large read holds one page rather than the whole result.
type pagedSearcher interface {
	SearchPages(req SearchRequest, fn func([]Entry) error) error
}

// defaultDocumentPageSize is the page a document search asks for when the model set
// none: the same 500 the AD search uses, inside every common server's size limit.
const defaultDocumentPageSize = 500

// RunWithDocuments is [Run] with a document store, for a search written as a
// document. Every other operation, and a search into a variable, is exactly [Run].
func RunWithDocuments(ctx context.Context, j Job, dialer Dialer, secret SecretResolver, docs DocumentStore) (Result, error) {
	if !j.AsDocument {
		return Run(ctx, j, dialer, secret)
	}
	if j.Operation != "search" {
		return Result{}, fmt.Errorf("ldap: only a search writes its result as a document, not %s", j.Operation)
	}
	if docs == nil {
		return Result{}, fmt.Errorf("ldap: this search writes its result as a document, and where it runs there is no document store to write it to")
	}
	if strings.TrimSpace(j.BaseDN) == "" {
		return Result{}, fmt.Errorf("ldap: search resolved an empty baseDN; a search has to start somewhere in the tree (is the variable behind it set?)")
	}
	if len(j.Select) == 0 {
		return Result{}, fmt.Errorf("ldap: a search written as a document needs the attributes it writes as columns (select)")
	}
	conn, err := connect(j, dialer, secret)
	if err != nil {
		return Result{}, err
	}
	defer conn.Close()
	return searchDocument(ctx, j, conn, docs)
}

// searchDocument reads what is under the base into a CSV document, page by page, and
// completes with {found, count, dn, document}. Rows are in the order the directory sent
// them; dn is the smallest DN, tracked as the pages arrive, as in the variable form.
func searchDocument(ctx context.Context, j Job, conn Conn, docs DocumentStore) (Result, error) {
	w := csvdoc.NewWriter(append([]string{csvdoc.DNColumn}, j.Select...), csvdoc.Bounds{
		What: "ldap: the search under " + j.BaseDN, Unit: "entry", Units: "entries",
		Limit: csvdoc.Limit(j.DocumentLimit, docs), Max: int(j.MaxEntries),
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
	pageSize := j.PageSize
	if pageSize <= 0 {
		pageSize = defaultDocumentPageSize
	}
	req := SearchRequest{BaseDN: j.BaseDN, Scope: j.Scope, Filter: j.Filter, Attributes: j.Select, PageSize: pageSize}
	var err error
	if ps, ok := conn.(pagedSearcher); ok {
		err = ps.SearchPages(req, each)
	} else {
		var entries []Entry
		req.MaxEntries = j.MaxEntries
		if entries, err = conn.Search(req); err == nil {
			err = each(entries)
		}
	}
	if err != nil {
		return Result{}, err
	}
	name := j.DocumentName
	if name == "" {
		name = "ldap-search.csv"
	}
	ref, err := w.Store(ctx, docs, name)
	if err != nil {
		return Result{}, fmt.Errorf("ldap: %w", err)
	}
	sum := csvdoc.Summary(w.Rows(), ref)
	sum["dn"] = first
	return Result{ResultVariable: j.ResultVariable, Value: sum, HasValue: true}, nil
}

// SearchPages reads a search a page at a time with the simple paged-results control
// (RFC 2696), handing each page to fn before asking for the next, so a large read holds
// one page. An error from fn stops the read and is returned as it is.
func (c *goConn) SearchPages(req SearchRequest, fn func([]Entry) error) error {
	sr := buildSearchRequest(req)
	size := req.PageSize
	if size <= 0 {
		size = defaultDocumentPageSize
	}
	paging := goldap.NewControlPaging(uint32(size))
	sr.Controls = []goldap.Control{paging}
	for {
		res, err := c.conn.Search(sr)
		if err != nil {
			return fmt.Errorf("ldap: search %s: %w", req.BaseDN, err)
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

// SearchPages delegates to the borrowed connection, reading it whole when it cannot
// page. A failure of the directory poisons the connection, as every other operation's
// does; a refusal from fn (the cap, the document limit) is not the connection's fault.
func (c *pooledConn) SearchPages(req SearchRequest, fn func([]Entry) error) error {
	var fnErr error
	guard := func(entries []Entry) error {
		fnErr = fn(entries)
		return fnErr
	}
	var err error
	if ps, ok := c.Conn.(pagedSearcher); ok {
		err = ps.SearchPages(req, guard)
	} else {
		var entries []Entry
		if entries, err = c.Conn.Search(req); err == nil {
			err = guard(entries)
		}
	}
	if err != nil && err != fnErr {
		c.poisoned = true
	}
	return err
}

// HandlerWithDocuments is [Handler] with a document store, for a search written as a
// document when the engine runs the LDAP kind in process.
func HandlerWithDocuments(store state.Reader, lookup ProcessLookup, dialer Dialer, secret SecretResolver, docs DocumentStore) job.OutputHandler {
	return func(j job.Job) ([]model.VariableValue, error) {
		ei, ok, err := store.GetElementInstance(j.ElementInstanceKey)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, nil
		}
		cp := lookup(ei.ProcessDefKey)
		if cp == nil {
			return nil, fmt.Errorf("ldap: no compiled process for def %d", ei.ProcessDefKey)
		}
		detail, err := cp.ConnectorTaskOf(ei.ElementId)
		if err != nil {
			return nil, fmt.Errorf("ldap: %w", err)
		}
		task, err := Resolve(store, cp, detail, ei, j.ElementInstanceKey)
		if err != nil {
			return nil, err
		}
		res, err := RunWithDocuments(context.Background(), task, dialer, secret, docs)
		if err != nil {
			return nil, err
		}
		return res.Variables(), nil
	}
}
