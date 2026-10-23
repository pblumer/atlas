package googlecontacts

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pblumer/atlas/connector/lookup"
)

// A form lookup is a person choosing a contact in a task form — "which customer is this
// order for" — by typing a few letters, without the address book ever becoming a
// process variable (ADR-0473, ADR-0476).
//
// It shares find-contacts' address book and nothing of its contract. A find is the
// process deciding whether somebody is known, so it reads the whole book and compares
// exactly; a lookup is a person reading a short list and typing another letter, so it
// asks Google's own prefix search, which is fast and may lag a few minutes behind a
// change — a contact created a minute ago is found by typing on, or a minute later,
// and nobody acts on its absence.
//
// The server sends a structure, never a request: which resource, which properties the
// term may match, the label or organization a result must have, and the properties to
// return. This file holds every one of them to its own table.

// LookupResource is what a form may search in a Google address book.
type LookupResource struct {
	// ValueProperty identifies a record: what a form submits and what a value lookup
	// reads by.
	ValueProperty string
	// Searchable are the properties a term may be a prefix of — of the value, or of any
	// word in it. Google searches names, e-mail addresses, phone numbers and
	// organizations itself; a field that names fewer of them has the others' matches
	// taken out again here.
	Searchable []string
	// Filterable are the properties an equality filter may name: groups (a label the
	// contact has, by its name) and organization.
	Filterable []string
	// Readable are the properties a lookup may return, and nothing else is passed on.
	Readable []string
}

// LookupResources are what a form may search. One: the contacts of the Worker's
// address book. The organization's directory is a second row when somebody needs it.
var LookupResources = map[string]LookupResource{
	"contacts": {
		ValueProperty: "id",
		Searchable:    []string{"displayName", "givenName", "familyName", "email", "phone", "organization"},
		Filterable:    []string{"groups", "organization"},
		Readable:      []string{"id", "displayName", "givenName", "familyName", "email", "phone", "organization", "jobTitle", "department", "city"},
	},
}

// The lookup's own bounds.
const (
	// searchWindow is how many matches a search asks Google for: its ceiling, so a field
	// narrowed to a label or to fewer properties still has matches to narrow.
	searchWindow = 30
	// warmAge is how long a warmed search cache counts as warm. A person types a name
	// in a few seconds; one warm-up per minute serves the whole burst.
	warmAge = time.Minute
	// labelAge is how long the address book's labels are reused between searches.
	labelAge = time.Minute
	// maxContinuation bounds a continuation's length; Google's page tokens are far
	// shorter.
	maxContinuation = 2048
)

// contactValue is the shape of a contact's value: the resource name Google gave it.
var contactValue = regexp.MustCompile(`^people/[A-Za-z0-9_-]{1,128}$`)

// ValidLookupValue reports whether v has the shape of a record's value in resource.
func ValidLookupValue(resource, v string) bool {
	if _, ok := LookupResources[resource]; !ok {
		return false
	}
	return contactValue.MatchString(v)
}

// The continuations a lookup hands out. A term search has none — Google's search has
// no pages — so only a listing without a term continues: through the address book by
// Google's page token, or through one label's members by position.
const (
	nextConnections = "connections:"
	nextMembers     = "members:"
)

// CheckLookup holds a query to its resource's table and to the bounds every lookup
// keeps. The server runs the same check before it sends one; running it here makes the
// table this Worker's rule rather than the server's courtesy.
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
		if strings.TrimSpace(f.Value) == "" || utf8.RuneCountInString(f.Value) > lookup.MaxValueLength || !lookup.CleanText(f.Value) {
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
	term := lookup.NormalizeSearch(q.Search)
	if term != "" && len(q.SearchFields) == 0 {
		return res, lookup.Fail(lookup.CodeInvalid)
	}
	for _, p := range q.SearchFields {
		if !slices.Contains(res.Searchable, p) {
			return res, lookup.Fail(lookup.CodeInvalid)
		}
	}
	if q.Next != "" {
		// A continuation belongs to a listing, and to the one it was issued for: by
		// label when the query names a label, through the book when it does not.
		_, byLabel := filterValue(q.Filters, "groups")
		switch {
		case term != "", len(q.Next) > maxContinuation, !lookup.CleanText(q.Next):
			return res, lookup.Fail(lookup.CodeInvalid)
		case byLabel && !validMembersNext(q.Next):
			return res, lookup.Fail(lookup.CodeInvalid)
		case !byLabel && (!strings.HasPrefix(q.Next, nextConnections) || len(q.Next) == len(nextConnections)):
			return res, lookup.Fail(lookup.CodeInvalid)
		}
	}
	return res, nil
}

func validMembersNext(next string) bool {
	n, err := strconv.Atoi(strings.TrimPrefix(next, nextMembers))
	return strings.HasPrefix(next, nextMembers) && err == nil && n > 0 && n <= scanLimit
}

// RunLookup answers one lookup against the address book connector names, through a
// registry the caller owns.
func RunLookup(ctx context.Context, connector string, q lookup.Query, reg *Registry) (lookup.Page, error) {
	c, ok := reg.Client(connector)
	if !ok {
		return lookup.Page{}, lookup.Fail(lookup.CodeUnconfigured)
	}
	if _, err := CheckLookup(q); err != nil {
		return lookup.Page{}, err
	}
	page, err := (&lookupRun{c: c, q: q}).answer(ctx)
	if err != nil {
		var le *lookup.Error
		if errors.As(err, &le) {
			return lookup.Page{}, le
		}
		return lookup.Page{}, lookupFailure(err)
	}
	return page, nil
}

// lookupRun is one lookup: the client, the query, and the label it resolved.
type lookupRun struct {
	c *Client
	q lookup.Query
	// label is the resource name of the label the query is narrowed to, when it is; a
	// label filter naming no label of this book leaves it empty and matches nothing.
	label string
}

func (r *lookupRun) answer(ctx context.Context) (lookup.Page, error) {
	if err := r.loadLabels(ctx); err != nil {
		return lookup.Page{}, err
	}
	if _, narrowed := filterValue(r.q.Filters, "groups"); narrowed && r.label == "" {
		// A label this address book does not have: no contact carries it.
		return lookup.Page{Items: []map[string]any{}}, nil
	}
	size := r.q.PageSize
	if size == 0 {
		size = lookup.DefaultPageSize
	}
	switch {
	case r.q.Value != "":
		return r.byValue(ctx)
	case lookup.NormalizeSearch(r.q.Search) != "":
		people, err := r.c.search(ctx, lookup.NormalizeSearch(r.q.Search), searchWindow, readFields, warmAge)
		if err != nil {
			return lookup.Page{}, err
		}
		return lookup.Page{Items: r.keep(people, size)}, nil
	case r.label != "":
		return r.listLabel(ctx, size)
	default:
		token := strings.TrimPrefix(r.q.Next, nextConnections)
		people, next, err := r.c.connectionsPage(ctx, readFields, size, token)
		if err != nil {
			return lookup.Page{}, err
		}
		page := lookup.Page{Items: r.keep(people, size)}
		if next != "" {
			page.Next = nextConnections + next
		}
		return page, nil
	}
}

// loadLabels resolves the label a query is narrowed to, when it is narrowed to one.
func (r *lookupRun) loadLabels(ctx context.Context) error {
	name, ok := filterValue(r.q.Filters, "groups")
	if !ok {
		return nil
	}
	all, err := r.c.cachedGroups(ctx, labelAge)
	if err != nil {
		return err
	}
	r.label = matchLabel(all, name)
	return nil
}

// matchLabel finds the label a filter names: by its resource name, by the name a person
// gave it, or — when that is unambiguous — ignoring case. A label the book does not have,
// or a name two labels share, matches none: a person cannot be asked which they meant.
func matchLabel(all []group, name string) string {
	name = strings.TrimSpace(name)
	var exact, folded []string
	for _, g := range all {
		switch {
		case g.ResourceName == name, g.Name == name:
			exact = append(exact, g.ResourceName)
		case strings.EqualFold(g.Name, name):
			folded = append(folded, g.ResourceName)
		}
	}
	if len(exact) == 1 {
		return exact[0]
	}
	if len(exact) == 0 && len(folded) == 1 {
		return folded[0]
	}
	return ""
}

// listLabel lists one label's members, page by page, in the order Google keeps them.
func (r *lookupRun) listLabel(ctx context.Context, size int) (lookup.Page, error) {
	from := 0
	if r.q.Next != "" {
		from, _ = strconv.Atoi(strings.TrimPrefix(r.q.Next, nextMembers))
	}
	names, count, err := r.c.members(ctx, r.label, from+size)
	if err != nil {
		return lookup.Page{}, err
	}
	if from >= len(names) {
		return lookup.Page{Items: []map[string]any{}}, nil
	}
	names = names[from:]
	people, err := r.c.batchGet(ctx, names, readFields)
	if err != nil {
		return lookup.Page{}, err
	}
	page := lookup.Page{Items: r.keep(people, size)}
	if end := from + len(names); end < count && end < scanLimit {
		page.Next = nextMembers + strconv.Itoa(end)
	}
	return page, nil
}

// byValue reads the one contact a value names and holds it to the query's filters. A
// contact that no longer exists is no record, not a failure: the server then says the
// choice is gone.
func (r *lookupRun) byValue(ctx context.Context) (lookup.Page, error) {
	p, err := r.c.get(ctx, r.q.Value, readFields)
	if isGoogle(err, http.StatusNotFound, "NOT_FOUND") {
		return lookup.Page{Items: []map[string]any{}}, nil
	}
	if err != nil {
		return lookup.Page{}, err
	}
	return lookup.Page{Items: r.keep([]person{p}, 1)}, nil
}

// keep projects the people a page may show: those that match the term on the field's
// own properties and hold every filter, at most size of them, each with exactly the
// properties the query named.
func (r *lookupRun) keep(people []person, size int) []map[string]any {
	term := strings.ToLower(lookup.NormalizeSearch(r.q.Search))
	out := make([]map[string]any, 0, min(len(people), size))
	for _, p := range people {
		if len(out) == size {
			break
		}
		rec := lookupRecord(p)
		if v, _ := rec["id"].(string); !contactValue.MatchString(v) {
			continue // a record without its value cannot be chosen
		}
		if r.q.Value == "" && term != "" && !r.matchesTerm(rec, term) {
			continue
		}
		if !r.holds(p, rec) {
			continue
		}
		item := make(map[string]any, len(r.q.Properties))
		for _, prop := range r.q.Properties {
			item[prop] = rec[prop]
		}
		out = append(out, item)
	}
	return out
}

// lookupRecord is a contact as a lookup reads it: the flat contact's scalar keys, and
// the primary address's city. Its displayName is what Google Contacts shows as the
// contact's title: the name, else the organization, else the e-mail address — so a
// company kept without a person's name is offered by the company's.
func lookupRecord(p person) map[string]any {
	flat := project(p, nil)
	rec := map[string]any{}
	for _, k := range []string{"id", "displayName", "givenName", "familyName", "email", "phone", "organization", "jobTitle", "department"} {
		rec[k] = flat[k]
	}
	if rec["displayName"] == nil {
		if rec["organization"] != nil {
			rec["displayName"] = rec["organization"]
		} else {
			rec["displayName"] = rec["email"]
		}
	}
	rec["city"] = nil
	if a, ok := flat["address"].(map[string]any); ok {
		if city, ok := a["city"].(string); ok && city != "" {
			rec["city"] = city
		}
	}
	return rec
}

// matchesTerm reports whether term is a prefix of one of the field's search properties,
// or of a word in one: "mül" finds "Anna Müller", as Google's own search does. A phone
// number is compared by its digits as well, so "079 12" finds "+41 79 123 45 67" in
// its national form and "0791" finds "079 123 45 67".
func (r *lookupRun) matchesTerm(rec map[string]any, term string) bool {
	for _, prop := range r.q.SearchFields {
		v, _ := rec[prop].(string)
		if v == "" {
			continue
		}
		low := strings.ToLower(v)
		if strings.HasPrefix(low, term) {
			return true
		}
		for _, w := range strings.Fields(low) {
			if strings.HasPrefix(w, term) {
				return true
			}
		}
		if prop == "phone" {
			if d, t := digitsOf(v), digitsOf(term); t != "" && strings.HasPrefix(d, t) {
				return true
			}
		}
	}
	return false
}

func digitsOf(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// holds reports whether a contact satisfies every filter of the query: the label it
// must have, and the organization it must be at, compared ignoring case.
func (r *lookupRun) holds(p person, rec map[string]any) bool {
	for _, f := range r.q.Filters {
		switch f.Property {
		case "groups":
			if !hasMembership(p, r.label) {
				return false
			}
		case "organization":
			org, _ := rec["organization"].(string)
			if !strings.EqualFold(strings.TrimSpace(org), strings.TrimSpace(f.Value)) {
				return false
			}
		}
	}
	return true
}

func hasMembership(p person, label string) bool {
	if label == "" {
		return false
	}
	for _, m := range entries(p, "memberships") {
		cg, _ := m["contactGroupMembership"].(map[string]any)
		if str(cg, "contactGroupResourceName") == label {
			return true
		}
	}
	return false
}

// filterValue returns the value of the filter on property, and whether there is one.
func filterValue(filters []lookup.Filter, property string) (string, bool) {
	for _, f := range filters {
		if f.Property == property {
			return f.Value, true
		}
	}
	return "", false
}

// lookupFailure turns what Google or the network answered into a lookup code. Nothing
// else crosses: Google's message can quote the request, and the request carries the
// person's term.
func lookupFailure(err error) error {
	var ae *APIError
	if !errors.As(err, &ae) {
		return lookup.Fail(lookup.CodeUnavailable)
	}
	switch {
	case ae.HTTP == http.StatusTooManyRequests:
		return lookup.Fail(lookup.CodeThrottled)
	case ae.HTTP == http.StatusBadRequest, ae.HTTP == http.StatusUnauthorized, ae.HTTP == http.StatusForbidden:
		return lookup.Fail(lookup.CodeRefused)
	}
	return lookup.Fail(lookup.CodeUnavailable)
}
