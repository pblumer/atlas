package entra

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pblumer/atlas/connector/lookup"
)

// A form lookup is the interactive twin of list-users (ADR-0473): a
// person types a few letters into a task form and sees the first matching people,
// without the directory ever becoming a process variable.
//
// It deliberately shares nothing of list-users' contract. A listing is the whole answer
// and fails above its cap, because a process decides from it; a lookup is one page and
// its continuation, because a person reads it and types another letter. And where a
// listing takes the OData a model authored, a lookup takes none: the server sends a
// structure — which properties to search, the term, the equality filters — and this
// file writes the filter from its own allowlist, so neither a form nor a person typing
// into one decides what this credential reads.

// LookupResource is one Entra collection as a form may search it.
type LookupResource struct {
	// Path is the collection under the tenant's Graph root.
	Path string
	// ValueProperty identifies a record: what a form submits and what a value lookup
	// reads by.
	ValueProperty string
	// Searchable are the properties a term may be a prefix of. Each supports
	// startsWith without advanced query support, so a lookup stays strongly consistent.
	Searchable []string
	// Filterable are the properties an equality filter may name, under the same rule.
	Filterable []string
	// Readable are the properties a lookup may return. Nothing outside it is selected
	// from Graph, and nothing outside what a query names is passed on.
	Readable []string
	// ActiveProperty is the boolean an ActiveOnly query requires to be true.
	ActiveProperty string
}

// LookupResources are the collections a form may search. One today: users. A group
// lookup is a row here and a value check, when somebody needs one.
//
// Every operator these rows lead to — startsWith on the searchable properties, eq on
// the filterable ones and on accountEnabled — is one Graph's property tables mark as
// supported by default, so the request needs neither ConsistencyLevel: eventual nor
// $count, and what a person is shown is the directory as it is, not as an index last
// saw it.
var LookupResources = map[string]LookupResource{
	"users": {
		Path:           "/users",
		ValueProperty:  "id",
		Searchable:     []string{"displayName", "givenName", "surname", "mail", "userPrincipalName"},
		Filterable:     []string{"department", "jobTitle"},
		Readable:       []string{"id", "displayName", "givenName", "surname", "mail", "userPrincipalName", "department", "jobTitle"},
		ActiveProperty: "accountEnabled",
	},
}

// objectID is the shape of an Entra object id: a GUID.
var objectID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// ValidLookupValue reports whether v has the shape of a record's value in resource —
// an object id for users. It is what a submission is checked against before anything
// is asked of Graph, and what a value lookup refuses to send otherwise.
func ValidLookupValue(resource, v string) bool {
	res, ok := LookupResources[resource]
	if !ok {
		return false
	}
	if res.ValueProperty == "id" {
		return objectID.MatchString(v)
	}
	return v != "" && utf8.RuneCountInString(v) <= lookup.MaxValueLength && lookup.CleanText(v)
}

// CheckLookup holds a query to its resource's allowlist and to the bounds every lookup
// keeps. The server runs the same check before a query is sent, from this same table;
// running it again here is what makes the table the Worker's rule rather than the
// server's courtesy — a query built by hand against the worker API meets it too.
func CheckLookup(q lookup.Query) (LookupResource, error) {
	res, ok := LookupResources[q.Resource]
	if !ok {
		return res, lookup.Fail(lookup.CodeInvalid)
	}
	if len(q.Properties) == 0 || !slices.Contains(q.Properties, res.ValueProperty) {
		return res, lookup.Fail(lookup.CodeInvalid)
	}
	for _, p := range q.Properties {
		if !slices.Contains(res.Readable, p) {
			return res, lookup.Fail(lookup.CodeInvalid)
		}
	}
	if q.PageSize < 0 || q.PageSize > lookup.MaxPageSize {
		return res, lookup.Fail(lookup.CodeInvalid)
	}
	seen := map[string]bool{}
	for _, f := range q.Filters {
		if !slices.Contains(res.Filterable, f.Property) || seen[f.Property] {
			return res, lookup.Fail(lookup.CodeInvalid)
		}
		seen[f.Property] = true
		if f.Value == "" || utf8.RuneCountInString(f.Value) > lookup.MaxValueLength || !lookup.CleanText(f.Value) {
			return res, lookup.Fail(lookup.CodeInvalid)
		}
	}
	if q.Value != "" {
		if !ValidLookupValue(q.Resource, q.Value) {
			return res, lookup.Fail(lookup.CodeInvalid)
		}
		return res, nil
	}
	if utf8.RuneCountInString(q.Search) > lookup.MaxSearchLength || !lookup.CleanText(q.Search) {
		return res, lookup.Fail(lookup.CodeInvalid)
	}
	if strings.TrimSpace(q.Search) != "" && len(q.SearchFields) == 0 {
		return res, lookup.Fail(lookup.CodeInvalid)
	}
	for _, p := range q.SearchFields {
		if !slices.Contains(res.Searchable, p) {
			return res, lookup.Fail(lookup.CodeInvalid)
		}
	}
	return res, nil
}

// RunLookup answers one lookup against the tenant connector names, through a registry
// the caller owns. It makes exactly one request to Graph.
func RunLookup(ctx context.Context, connector string, q lookup.Query, reg *Registry) (lookup.Page, error) {
	client, ok := reg.Client(connector)
	if !ok {
		return lookup.Page{}, lookup.Fail(lookup.CodeUnconfigured)
	}
	res, err := CheckLookup(q)
	if err != nil {
		return lookup.Page{}, err
	}
	if q.Value != "" {
		return lookupByValue(ctx, client, res, q)
	}
	path := lookupPath(res, q)
	if q.Next != "" {
		if !continues(client.BaseURL(), res.Path, q.Next) {
			return lookup.Page{}, lookup.Fail(lookup.CodeInvalid)
		}
		path = q.Next
	}
	answer, err := client.Call(ctx, Request{Method: http.MethodGet, Path: path})
	if err != nil {
		return lookup.Page{}, lookupFailure(err)
	}
	batch, next, err := collectionPage(answer, "lookup")
	if err != nil {
		return lookup.Page{}, lookup.Fail(lookup.CodeUnavailable)
	}
	items := make([]map[string]any, 0, len(batch))
	for _, raw := range batch {
		rec, ok := raw.(map[string]any)
		if !ok {
			return lookup.Page{}, lookup.Fail(lookup.CodeUnavailable)
		}
		if v, _ := rec[res.ValueProperty].(string); v == "" {
			// A record without its value cannot be chosen, so it is not offered.
			continue
		}
		items = append(items, project(rec, q.Properties))
	}
	return lookup.Page{Items: items, Next: next}, nil
}

// lookupPath builds a search's first request. The filter is written here and nowhere
// else: a prefix over each search property joined by or, and-ed with each equality
// filter and with the active flag. Every literal goes through odataString, which is
// the whole defence against a term that tries to close its literal — and it is enough,
// because OData has exactly one way to end a string.
func lookupPath(res LookupResource, q lookup.Query) string {
	var clauses []string
	if term := lookup.NormalizeSearch(q.Search); term != "" {
		prefixes := make([]string, 0, len(q.SearchFields))
		for _, p := range q.SearchFields {
			prefixes = append(prefixes, "startswith("+p+","+odataString(term)+")")
		}
		clauses = append(clauses, "("+strings.Join(prefixes, " or ")+")")
	}
	for _, f := range q.Filters {
		clauses = append(clauses, f.Property+" eq "+odataString(f.Value))
	}
	if q.ActiveOnly {
		clauses = append(clauses, res.ActiveProperty+" eq true")
	}
	var params []string
	if len(clauses) > 0 {
		params = append(params, "$filter="+url.QueryEscape(strings.Join(clauses, " and ")))
	}
	params = append(params, "$select="+url.QueryEscape(strings.Join(q.Properties, ",")))
	size := q.PageSize
	if size == 0 {
		size = lookup.DefaultPageSize
	}
	params = append(params, "$top="+strconv.Itoa(size))
	return res.Path + "?" + strings.Join(params, "&")
}

// odataString writes v as an OData string literal: in single quotes, with every single
// quote inside doubled.
func odataString(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

// continues reports whether next is a continuation of the collection at path on this
// tenant's own endpoint. The server seals the continuation it hands out, so a person
// cannot have altered it; the Worker still refuses one that leads anywhere else,
// because this is the request that carries a directory-wide bearer.
func continues(baseURL, path, next string) bool {
	u, err := url.Parse(next)
	if err != nil || !u.IsAbs() {
		return false
	}
	base, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return false
	}
	return u.Scheme == base.Scheme && u.Host == base.Host && u.Path == base.Path+path
}

// lookupByValue reads the one record by its value and holds it to the query's filters.
// Graph has no $filter on a user's id, so the record is read by its path and the
// filters are compared here; what was read only to compare it is not passed on.
func lookupByValue(ctx context.Context, client Client, res LookupResource, q lookup.Query) (lookup.Page, error) {
	sel := slices.Clone(q.Properties)
	for _, f := range q.Filters {
		if !slices.Contains(sel, f.Property) {
			sel = append(sel, f.Property)
		}
	}
	if q.ActiveOnly && !slices.Contains(sel, res.ActiveProperty) {
		sel = append(sel, res.ActiveProperty)
	}
	path := res.Path + "/" + url.PathEscape(q.Value) + "?$select=" + url.QueryEscape(strings.Join(sel, ","))
	answer, err := client.Call(ctx, Request{Method: http.MethodGet, Path: path})
	if err != nil {
		var ge *GraphError
		if errors.As(err, &ge) && ge.Status == http.StatusNotFound && ge.Code == "Request_ResourceNotFound" {
			return lookup.Page{Items: []map[string]any{}}, nil
		}
		return lookup.Page{}, lookupFailure(err)
	}
	rec, ok := answer.(map[string]any)
	if !ok {
		return lookup.Page{}, lookup.Fail(lookup.CodeUnavailable)
	}
	for _, f := range q.Filters {
		if !strings.EqualFold(fmt.Sprint(rec[f.Property]), f.Value) {
			return lookup.Page{Items: []map[string]any{}}, nil
		}
	}
	if q.ActiveOnly {
		if active, _ := rec[res.ActiveProperty].(bool); !active {
			return lookup.Page{Items: []map[string]any{}}, nil
		}
	}
	return lookup.Page{Items: []map[string]any{project(rec, q.Properties)}}, nil
}

// project keeps exactly the properties a query named. One Graph did not send reads as
// null rather than being absent, so every record of a page has the same shape — a
// user without a mailbox has mail: null, which is what the form falls back on.
func project(rec map[string]any, props []string) map[string]any {
	out := make(map[string]any, len(props))
	for _, p := range props {
		out[p] = rec[p]
	}
	return out
}

// lookupFailure turns whatever Graph or the network answered into a lookup code. The
// code is all that leaves this function: a GraphError carries its request path, and a
// search's path carries the person's term.
func lookupFailure(err error) error {
	var ge *GraphError
	if !errors.As(err, &ge) {
		return lookup.Fail(lookup.CodeUnavailable)
	}
	switch {
	case ge.Status == http.StatusTooManyRequests:
		return &lookup.Error{Code: lookup.CodeThrottled, RetryAfter: seconds(ge.RetryAfter)}
	case ge.Status == http.StatusBadRequest || ge.Status == http.StatusUnauthorized || ge.Status == http.StatusForbidden:
		return lookup.Fail(lookup.CodeRefused)
	default:
		return &lookup.Error{Code: lookup.CodeUnavailable, RetryAfter: seconds(ge.RetryAfter)}
	}
}

// seconds rounds a wait up to whole seconds, so advice of 1.2s is not reported as 1s.
func seconds(d time.Duration) int {
	if d <= 0 {
		return 0
	}
	return int(math.Ceil(d.Seconds()))
}
