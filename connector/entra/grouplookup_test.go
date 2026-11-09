package entra

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/workersdk/lookup"
)

// aliasQuery is the value lookup that answers "does a group with this alias exist".
func aliasQuery(resource, alias string) lookup.Query {
	return lookup.Query{
		Resource:   resource,
		Value:      alias,
		Properties: []string{"id", "displayName", "mailNickname"},
	}
}

// A group is identified by its alias, which Graph cannot read by path, so the value
// lookup is one filtered page of at most one record — on the live directory and on the
// recycle bin alike (ADR-0172, amended 2026-11-03).
func TestGroupValueLookupFiltersByAlias(t *testing.T) {
	for resource, collection := range map[string]string{
		"groups":        "/groups",
		"deletedGroups": "/directory/deletedItems/microsoft.graph.group",
	} {
		t.Run(resource, func(t *testing.T) {
			c := &lookupClient{resp: []any{map[string]any{"value": []any{
				map[string]any{"id": "g1", "displayName": "Projekt Apollo", "mailNickname": "projekt-apollo", "visibility": "Private"},
			}}}}
			got, err := RunLookup(context.Background(), "ucc_entra", aliasQuery(resource, "projekt-apollo"), lookupRegistry(c))
			if err != nil {
				t.Fatal(err)
			}
			if len(c.reqs) != 1 {
				t.Fatalf("requests = %d, want exactly one", len(c.reqs))
			}
			r := c.reqs[0]
			if r.Method != http.MethodGet || r.Eventual {
				t.Errorf("method %s eventual %v, want a plain GET", r.Method, r.Eventual)
			}
			path, q := decoded(t, r.Path)
			if path != collection {
				t.Errorf("collection = %q, want %q", path, collection)
			}
			if f := q.Get("$filter"); f != "mailNickname eq 'projekt-apollo'" {
				t.Errorf("$filter = %q", f)
			}
			if s := q.Get("$select"); s != "id,displayName,mailNickname" {
				t.Errorf("$select = %q", s)
			}
			if top := q.Get("$top"); top != "1" {
				t.Errorf("$top = %q, want 1", top)
			}
			if len(got.Items) != 1 || got.Items[0]["id"] != "g1" || got.Items[0]["mailNickname"] != "projekt-apollo" {
				t.Fatalf("items = %v, want the one group", got.Items)
			}
			if _, leaked := got.Items[0]["visibility"]; leaked {
				t.Error("a property the query did not name was passed on")
			}

			// No such alias is an answer, not a failure.
			empty := &lookupClient{resp: []any{map[string]any{"value": []any{}}}}
			got, err = RunLookup(context.Background(), "ucc_entra", aliasQuery(resource, "frei"), lookupRegistry(empty))
			if err != nil {
				t.Fatalf("err = %v, want an empty page", err)
			}
			if got.Items == nil || len(got.Items) != 0 {
				t.Errorf("items = %#v, want an empty, non-nil list", got.Items)
			}
		})
	}
}

// Only an alias's shape reaches Graph: a quote, a space or OData punctuation is refused
// before any request is made.
func TestGroupValueLookupRefusesWhatIsNoAlias(t *testing.T) {
	for _, alias := range []string{
		"o'brien", "zwei worte", "-führend", ".punkt", "a) or (id ne null", "ä", "a/b",
		strings.Repeat("x", 65), // one past the 64 an alias may have
	} {
		c := &lookupClient{}
		_, err := RunLookup(context.Background(), "ucc_entra", aliasQuery("groups", alias), lookupRegistry(c))
		var le *lookup.Error
		if !errors.As(err, &le) || le.Code != lookup.CodeInvalid {
			t.Errorf("%q: err = %v, want %s", alias, err, lookup.CodeInvalid)
		}
		if len(c.reqs) != 0 {
			t.Errorf("%q: a request was sent for a value that is no alias", alias)
		}
	}
	for _, alias := range []string{"a", "Projekt_Apollo-2026.v1", strings.Repeat("x", 64)} {
		if !ValidLookupValue("groups", alias) || !ValidLookupValue("deletedGroups", alias) {
			t.Errorf("%q was refused as an alias", alias)
		}
	}
	// A group's value is its alias, not an object id, and a user's is still its id.
	if ValidLookupValue("users", "projekt-apollo") {
		t.Error("an alias was accepted as a user's object id")
	}
}

// The alias check keeps every quote out, but the literal is still written by
// odataString — the one defence the file's filters share — so a value that reached it
// any other way would stay inside its literal.
func TestGroupValueLookupKeepsTheValueInsideItsLiteral(t *testing.T) {
	c := &lookupClient{}
	res := LookupResources["groups"]
	q := aliasQuery("groups", "x' or displayName eq 'y")
	if _, err := lookupByFilter(context.Background(), c, res, q); err != nil {
		t.Fatal(err)
	}
	_, vals := decoded(t, c.reqs[0].Path)
	if f := vals.Get("$filter"); f != "mailNickname eq 'x'' or displayName eq ''y'" {
		t.Errorf("$filter = %q, want the quotes doubled inside one literal", f)
	}
}

// A group has no active flag, so a query asking for active records only is refused
// rather than answered with every record; the group rows search and filter nothing.
func TestGroupLookupRefusesWhatTheRowDoesNotOffer(t *testing.T) {
	for name, mutate := range map[string]func(*lookup.Query){
		"active only":         func(q *lookup.Query) { q.ActiveOnly = true },
		"a search":            func(q *lookup.Query) { q.Value = ""; q.Search = "Pro"; q.SearchFields = []string{"displayName"} },
		"a filter":            func(q *lookup.Query) { q.Filters = []lookup.Filter{{Property: "displayName", Value: "x"}} },
		"an unread property":  func(q *lookup.Query) { q.Properties = append(q.Properties, "visibility") },
		"without the alias":   func(q *lookup.Query) { q.Properties = []string{"id", "displayName"} },
		"an object id as key": func(q *lookup.Query) { q.Value = annaID + "/x" },
	} {
		for _, resource := range []string{"groups", "deletedGroups"} {
			q := aliasQuery(resource, "projekt-apollo")
			mutate(&q)
			c := &lookupClient{}
			_, err := RunLookup(context.Background(), "ucc_entra", q, lookupRegistry(c))
			var le *lookup.Error
			if !errors.As(err, &le) || le.Code != lookup.CodeInvalid {
				t.Errorf("%s on %s: err = %v, want %s", name, resource, err, lookup.CodeInvalid)
			}
			if len(c.reqs) != 0 {
				t.Errorf("%s on %s: a request was sent", name, resource)
			}
		}
	}
}

// Graph refusing the filter — which is how an unsupported $filter on the recycle bin
// would show — is a code, not an empty page that reads as "the alias is free".
func TestGroupValueLookupFailureIsNotAnEmptyAnswer(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{&GraphError{Status: 400, Code: "Request_UnsupportedQuery"}, lookup.CodeRefused},
		{&GraphError{Status: 404, Code: "Request_ResourceNotFound"}, lookup.CodeUnavailable},
		{&GraphError{Status: 503}, lookup.CodeUnavailable},
	} {
		c := &lookupClient{errs: []error{tc.err}}
		_, err := RunLookup(context.Background(), "ucc_entra", aliasQuery("deletedGroups", "projekt-apollo"), lookupRegistry(c))
		var le *lookup.Error
		if !errors.As(err, &le) || le.Code != tc.code {
			t.Errorf("%v: err = %v, want %s", tc.err, err, tc.code)
		}
	}
	// An answer that is not a collection is no answer either.
	c := &lookupClient{resp: []any{map[string]any{"id": "g1"}}}
	if _, err := RunLookup(context.Background(), "ucc_entra", aliasQuery("groups", "projekt-apollo"), lookupRegistry(c)); err == nil {
		t.Error("a non-collection answer was taken for one")
	}
	// A record without its alias cannot be the one asked for.
	c = &lookupClient{resp: []any{map[string]any{"value": []any{map[string]any{"id": "g1"}}}}}
	got, err := RunLookup(context.Background(), "ucc_entra", aliasQuery("groups", "projekt-apollo"), lookupRegistry(c))
	if err != nil || len(got.Items) != 0 {
		t.Errorf("items = %v, err = %v; want an empty page", got.Items, err)
	}
	c = &lookupClient{resp: []any{map[string]any{"value": []any{"not a record"}}}}
	if _, err := RunLookup(context.Background(), "ucc_entra", aliasQuery("groups", "projekt-apollo"), lookupRegistry(c)); err == nil {
		t.Error("a record that is not an object was taken for one")
	}
}
