package googlecontacts

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/pblumer/atlas/connector/lookup"
)

// lookupBook is an address book with three customers labelled Kunden, one supplier and
// a contact without a name.
func lookupBook(t *testing.T) (*fakePeople, map[string]string) {
	t.Helper()
	f := newFakePeople(t)
	kunden := f.addGroup("kunden1", "Kunden")
	ids := map[string]string{}
	ids["anna"] = f.add(anna(), kunden)
	ids["annabelle"] = f.add(person{
		"names":          []any{map[string]any{"givenName": "Annabelle", "familyName": "Zweifel", "displayName": "Annabelle Zweifel"}},
		"emailAddresses": []any{map[string]any{"value": "a.zweifel@beispiel.ch"}},
		"organizations":  []any{map[string]any{"name": "Beispiel AG"}},
		"addresses":      []any{map[string]any{"city": "Bern"}},
	}, kunden)
	ids["bruno"] = f.add(person{
		"names":          []any{map[string]any{"givenName": "Bruno", "familyName": "Anderegg", "displayName": "Bruno Anderegg"}},
		"emailAddresses": []any{map[string]any{"value": "bruno@lieferant.ch"}},
		"phoneNumbers":   []any{map[string]any{"value": "+41 44 555 66 77"}},
	})
	ids["carla"] = f.add(person{
		"names":          []any{map[string]any{"givenName": "Carla", "familyName": "Annen", "displayName": "Carla Annen"}},
		"emailAddresses": []any{map[string]any{"value": "carla@beispiel.ch"}},
		"organizations":  []any{map[string]any{"name": "Beispiel AG"}},
	}, kunden)
	return f, ids
}

func contactsQuery(search string, filters ...lookup.Filter) lookup.Query {
	return lookup.Query{
		Resource: "contacts", Search: search,
		SearchFields: []string{"displayName", "email", "organization"},
		Filters:      filters,
		Properties:   []string{"id", "displayName", "email", "organization"},
		PageSize:     25,
	}
}

func ask(t *testing.T, f *fakePeople, q lookup.Query) lookup.Page {
	t.Helper()
	page, err := RunLookup(context.Background(), "verkauf", q, f.registry("verkauf"))
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func names(page lookup.Page) []string {
	var out []string
	for _, it := range page.Items {
		out = append(out, it["displayName"].(string))
	}
	return out
}

func TestALookupSearchesByPrefixAndShowsWhatTheFieldNames(t *testing.T) {
	f, ids := lookupBook(t)
	page := ask(t, f, contactsQuery("  Ann  "))
	if got := names(page); !reflect.DeepEqual(got, []string{"Anna Muster", "Annabelle Zweifel", "Carla Annen"}) {
		t.Errorf("found %v", got)
	}
	// Exactly the properties the query named, nothing else of the contact.
	want := map[string]any{"id": ids["anna"], "displayName": "Anna Muster", "email": "anna.muster@firma.ch", "organization": "Firma AG"}
	if !reflect.DeepEqual(page.Items[0], want) {
		t.Errorf("item = %v", page.Items[0])
	}
	if page.Next != "" {
		t.Errorf("a search has no pages, got next %q", page.Next)
	}
	q := f.requestsTo("search")[1].URL.Query()
	if q.Get("query") != "Ann" || q.Get("pageSize") != "30" {
		t.Errorf("search = %v", q)
	}
	// A word of the name matches too ("mus" → Anna Muster); a field searching e-mail
	// addresses alone has Google's name matches taken out again.
	if got := names(ask(t, f, contactsQuery("mus"))); !reflect.DeepEqual(got, []string{"Anna Muster"}) {
		t.Errorf("word prefix found %v", got)
	}
	q2 := contactsQuery("ann")
	q2.SearchFields = []string{"email"}
	if got := names(ask(t, f, q2)); !reflect.DeepEqual(got, []string{"Anna Muster"}) {
		t.Errorf("e-mail only found %v", got)
	}
}

// TestALookupWarmsGooglesSearchOncePerBurst: a person types a name in a few seconds;
// warming Google's cache before each letter would double every search.
func TestALookupWarmsGooglesSearchOncePerBurst(t *testing.T) {
	f, _ := lookupBook(t)
	for _, term := range []string{"an", "ann", "anna"} {
		ask(t, f, contactsQuery(term))
	}
	warmups := 0
	for _, r := range f.requestsTo("search") {
		if r.URL.Query().Get("query") == "" {
			warmups++
		}
	}
	if warmups != 1 || len(f.requestsTo("search")) != 4 {
		t.Errorf("warm-ups %d of %d search calls, want 1 of 4", warmups, len(f.requestsTo("search")))
	}
}

func TestALookupNarrowedToALabel(t *testing.T) {
	f, _ := lookupBook(t)
	f.add(person{"names": []any{map[string]any{"givenName": "Annette", "familyName": "Lieferantin", "displayName": "Annette Lieferantin"}}})
	q := contactsQuery("ann", lookup.Filter{Property: "groups", Value: "kunden"})
	if got := names(ask(t, f, q)); !reflect.DeepEqual(got, []string{"Anna Muster", "Annabelle Zweifel", "Carla Annen"}) {
		t.Errorf("found %v", got)
	}
	// The labels are read once for a burst of searches, not per letter.
	ask(t, f, q)
	if n := len(f.requestsTo("groups")); n != 1 {
		t.Errorf("contactGroups.list calls = %d, want 1", n)
	}
	// A label this book does not have matches nobody, and nothing is searched for it.
	before := len(f.requestsTo("search"))
	if page := ask(t, f, contactsQuery("ann", lookup.Filter{Property: "groups", Value: "Lieferanten"})); len(page.Items) != 0 {
		t.Errorf("unknown label found %v", names(page))
	}
	if len(f.requestsTo("search")) != before {
		t.Error("a search ran for a label nobody has")
	}
	// The organization is compared ignoring case.
	org := contactsQuery("", lookup.Filter{Property: "organization", Value: "beispiel ag"})
	org.SearchFields = nil
	if got := names(ask(t, f, org)); !reflect.DeepEqual(got, []string{"Carla Annen", "Annabelle Zweifel"}) {
		t.Errorf("organization found %v", got)
	}
}

func TestMatchLabelRefusesToGuess(t *testing.T) {
	all := []group{
		{ResourceName: "contactGroups/a", Name: "Kunden"},
		{ResourceName: "contactGroups/b", Name: "kunden"},
		{ResourceName: "contactGroups/c", Name: "VIP"},
	}
	for name, want := range map[string]string{
		"Kunden": "contactGroups/a", "kunden": "contactGroups/b", "KUNDEN": "",
		"vip": "contactGroups/c", "contactGroups/c": "contactGroups/c", "Lieferanten": "",
	} {
		if got := matchLabel(all, name); got != want {
			t.Errorf("matchLabel(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestAListingWithoutATermPagesThroughTheBook: a field that searches from zero
// characters lists what its filters admit, a page at a time.
func TestAListingWithoutATermPagesThroughTheBook(t *testing.T) {
	f, _ := lookupBook(t)
	q := contactsQuery("")
	q.SearchFields, q.PageSize = nil, 2
	first := ask(t, f, q)
	if got := names(first); !reflect.DeepEqual(got, []string{"Bruno Anderegg", "Carla Annen"}) || !strings.HasPrefix(first.Next, nextConnections) {
		t.Fatalf("first page %v next %q", got, first.Next)
	}
	q.Next = first.Next
	second := ask(t, f, q)
	if got := names(second); !reflect.DeepEqual(got, []string{"Anna Muster", "Annabelle Zweifel"}) || second.Next != "" {
		t.Errorf("second page %v next %q", got, second.Next)
	}

	label := contactsQuery("", lookup.Filter{Property: "groups", Value: "Kunden"})
	label.SearchFields, label.PageSize = nil, 2
	first = ask(t, f, label)
	if len(first.Items) != 2 || first.Next != nextMembers+"2" {
		t.Fatalf("label page %v next %q", names(first), first.Next)
	}
	label.Next = first.Next
	second = ask(t, f, label)
	if len(second.Items) != 1 || second.Next != "" {
		t.Errorf("label second page %v next %q", names(second), second.Next)
	}
	label.Next = nextMembers + "9"
	if page := ask(t, f, label); len(page.Items) != 0 {
		t.Errorf("past the label's end: %v", names(page))
	}
}

func TestALookupByValueHoldsTheChoiceToTheFilters(t *testing.T) {
	f, ids := lookupBook(t)
	q := contactsQuery("", lookup.Filter{Property: "groups", Value: "Kunden"})
	q.Value = ids["anna"]
	if page := ask(t, f, q); len(page.Items) != 1 || page.Items[0]["id"] != ids["anna"] {
		t.Errorf("anna = %v", page.Items)
	}
	q.Value = ids["bruno"] // no customer
	if page := ask(t, f, q); len(page.Items) != 0 {
		t.Errorf("bruno outside the label = %v", page.Items)
	}
	q.Value = "people/c404"
	if page := ask(t, f, q); len(page.Items) != 0 {
		t.Errorf("a deleted contact = %v", page.Items)
	}
	// A value lookup is not a search: it does not touch Google's search cache.
	if n := len(f.requestsTo("search")); n != 0 {
		t.Errorf("search calls = %d", n)
	}
}

func TestAPhoneNumberIsFoundByItsDigits(t *testing.T) {
	f, ids := lookupBook(t)
	q := contactsQuery("+41 44 5")
	q.SearchFields = []string{"phone"}
	q.Properties = []string{"id", "phone"}
	page := ask(t, f, q)
	if len(page.Items) != 1 || page.Items[0]["id"] != ids["bruno"] {
		t.Errorf("found %v", page.Items)
	}
	if !(&lookupRun{q: lookup.Query{SearchFields: []string{"phone"}}}).matchesTerm(map[string]any{"phone": "+41 44 555 66 77"}, "4144") {
		t.Error("the international form's digits did not match")
	}
}

func TestCheckLookup(t *testing.T) {
	ok := contactsQuery("ann")
	if _, err := CheckLookup(ok); err != nil {
		t.Fatalf("a valid query was refused: %v", err)
	}
	mutate := func(fn func(*lookup.Query)) lookup.Query {
		q := contactsQuery("ann")
		fn(&q)
		return q
	}
	for name, q := range map[string]lookup.Query{
		"resource":            mutate(func(q *lookup.Query) { q.Resource = "directory" }),
		"no value property":   mutate(func(q *lookup.Query) { q.Properties = []string{"displayName"} }),
		"unreadable property": mutate(func(q *lookup.Query) { q.Properties = []string{"id", "notes"} }),
		"page too large":      mutate(func(q *lookup.Query) { q.PageSize = 51 }),
		"filter":              mutate(func(q *lookup.Query) { q.Filters = []lookup.Filter{{Property: "email", Value: "x"}} }),
		"filter twice": mutate(func(q *lookup.Query) {
			q.Filters = []lookup.Filter{{Property: "groups", Value: "a"}, {Property: "groups", Value: "b"}}
		}),
		"empty filter":     mutate(func(q *lookup.Query) { q.Filters = []lookup.Filter{{Property: "groups", Value: " "}} }),
		"bad value":        mutate(func(q *lookup.Query) { q.Search, q.Value = "", "people/../x" }),
		"bare value":       mutate(func(q *lookup.Query) { q.Search, q.Value = "", "c123" }),
		"long term":        mutate(func(q *lookup.Query) { q.Search = strings.Repeat("a", lookup.MaxSearchLength+1) }),
		"control":          mutate(func(q *lookup.Query) { q.Search = "an\x00" }),
		"no search fields": mutate(func(q *lookup.Query) { q.SearchFields = nil }),
		"search field":     mutate(func(q *lookup.Query) { q.SearchFields = []string{"notes"} }),
		"next with a term": mutate(func(q *lookup.Query) { q.Next = nextConnections + "x" }),
		"next empty token": mutate(func(q *lookup.Query) { q.Search, q.Next = "", nextConnections }),
		"next of a label":  mutate(func(q *lookup.Query) { q.Search, q.Next = "", nextMembers+"2" }),
		"next of the book": mutate(func(q *lookup.Query) {
			q.Search, q.Next, q.Filters = "", nextConnections+"x", []lookup.Filter{{Property: "groups", Value: "Kunden"}}
		}),
		"next out of range": mutate(func(q *lookup.Query) {
			q.Search, q.Next, q.Filters = "", nextMembers+"0", []lookup.Filter{{Property: "groups", Value: "Kunden"}}
		}),
		"next too long": mutate(func(q *lookup.Query) { q.Search, q.Next = "", nextConnections+strings.Repeat("x", maxContinuation) }),
	} {
		var le *lookup.Error
		if _, err := CheckLookup(q); !errors.As(err, &le) || le.Code != lookup.CodeInvalid {
			t.Errorf("%s: err = %v, want invalid_query", name, err)
		}
	}
	if !ValidLookupValue("contacts", "people/c1") || ValidLookupValue("contacts", "c1") || ValidLookupValue("directory", "people/c1") {
		t.Error("ValidLookupValue")
	}
}

func TestALookupFailsWithACodeAndNothingElse(t *testing.T) {
	f, _ := lookupBook(t)
	for status, want := range map[int]string{429: lookup.CodeThrottled, 403: lookup.CodeRefused, 400: lookup.CodeRefused, 500: lookup.CodeUnavailable} {
		f.fail["search"] = status
		_, err := RunLookup(context.Background(), "verkauf", contactsQuery("geheim"), f.registry("verkauf"))
		var le *lookup.Error
		if !errors.As(err, &le) || le.Code != want || strings.Contains(err.Error(), "geheim") {
			t.Errorf("HTTP %d: err = %v, want %s", status, err, want)
		}
	}
	delete(f.fail, "search")
	f.fail["groups"] = 503
	if _, err := RunLookup(context.Background(), "verkauf", contactsQuery("an", lookup.Filter{Property: "groups", Value: "Kunden"}), f.registry("verkauf")); err == nil {
		t.Error("a failed label read answered a page")
	}
	if _, err := RunLookup(context.Background(), "andere", contactsQuery("an"), f.registry("verkauf")); err == nil || err.(*lookup.Error).Code != lookup.CodeUnconfigured {
		t.Errorf("unknown worker: %v", err)
	}
	if _, err := RunLookup(context.Background(), "verkauf", lookup.Query{Resource: "x"}, f.registry("verkauf")); err == nil {
		t.Error("an invalid query reached Google")
	}
	if err := lookupFailure(errors.New("network down")); err.(*lookup.Error).Code != lookup.CodeUnavailable {
		t.Errorf("network: %v", err)
	}
}

// TestEveryCallOfALookupCanFail: whichever People API call a lookup makes, its failure
// is a code, never a page.
func TestEveryCallOfALookupCanFail(t *testing.T) {
	label := lookup.Filter{Property: "groups", Value: "Kunden"}
	for method, q := range map[string]lookup.Query{
		"connections": func() lookup.Query { q := contactsQuery(""); q.SearchFields = nil; return q }(),
		"group":       func() lookup.Query { q := contactsQuery("", label); q.SearchFields = nil; return q }(),
		"batchGet":    func() lookup.Query { q := contactsQuery("", label); q.SearchFields = nil; return q }(),
		"get":         func() lookup.Query { q := contactsQuery(""); q.Search, q.Value = "", "people/c1"; return q }(),
	} {
		f, _ := lookupBook(t)
		f.fail[method] = 500
		_, err := RunLookup(context.Background(), "verkauf", q, f.registry("verkauf"))
		var le *lookup.Error
		if !errors.As(err, &le) || le.Code != lookup.CodeUnavailable {
			t.Errorf("%s: err = %v", method, err)
		}
	}
}
