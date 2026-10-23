package entra

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/connector/lookup"
)

// lookupClient answers one scripted response per call and keeps every request, so a
// test can read back exactly what would have gone to Graph.
type lookupClient struct {
	reqs []Request
	resp []any
	errs []error
}

func (c *lookupClient) BaseURL() string { return "https://graph.microsoft.com/v1.0" }

func (c *lookupClient) Call(_ context.Context, r Request) (any, error) {
	c.reqs = append(c.reqs, r)
	i := len(c.reqs) - 1
	if i < len(c.errs) && c.errs[i] != nil {
		return nil, c.errs[i]
	}
	if i < len(c.resp) {
		return c.resp[i], nil
	}
	return map[string]any{"value": []any{}}, nil
}

func lookupRegistry(c Client) *Registry {
	reg := NewRegistry()
	reg.Register("ucc_entra", c)
	return reg
}

// userQuery is the query the example form sends: a prefix over three properties, one
// department, the properties the option shows.
func userQuery(search string) lookup.Query {
	return lookup.Query{
		Resource:     "users",
		Search:       search,
		SearchFields: []string{"displayName", "mail", "userPrincipalName"},
		Filters:      []lookup.Filter{{Property: "department", Value: "IT"}},
		ActiveOnly:   true,
		Properties:   []string{"id", "displayName", "mail", "userPrincipalName", "department"},
		PageSize:     25,
	}
}

// decoded splits a request path into its collection and its decoded query parameters,
// so assertions read the OData Graph would read rather than its percent-encoding.
func decoded(t *testing.T, path string) (string, url.Values) {
	t.Helper()
	p, q, _ := strings.Cut(path, "?")
	vals, err := url.ParseQuery(q)
	if err != nil {
		t.Fatalf("path %q does not parse: %v", path, err)
	}
	return p, vals
}

func TestLookupBuildsOnePrefixSearchUnderItsFilters(t *testing.T) {
	c := &lookupClient{}
	if _, err := RunLookup(context.Background(), "ucc_entra", userQuery("Ann"), lookupRegistry(c)); err != nil {
		t.Fatal(err)
	}
	if len(c.reqs) != 1 {
		t.Fatalf("requests = %d, want exactly one", len(c.reqs))
	}
	r := c.reqs[0]
	if r.Method != http.MethodGet || r.Eventual {
		t.Errorf("method %s eventual %v, want a plain GET: every operator used is supported by default", r.Method, r.Eventual)
	}
	path, q := decoded(t, r.Path)
	if path != "/users" {
		t.Errorf("collection = %q, want /users", path)
	}
	wantFilter := "(startswith(displayName,'Ann') or startswith(mail,'Ann') or startswith(userPrincipalName,'Ann'))" +
		" and department eq 'IT' and accountEnabled eq true"
	if got := q.Get("$filter"); got != wantFilter {
		t.Errorf("$filter =\n  %s\nwant\n  %s", got, wantFilter)
	}
	if got := q.Get("$select"); got != "id,displayName,mail,userPrincipalName,department" {
		t.Errorf("$select = %q", got)
	}
	if got := q.Get("$top"); got != "25" {
		t.Errorf("$top = %q, want 25", got)
	}
	if q.Has("$count") || q.Has("$search") || q.Has("$orderby") {
		t.Errorf("query %v carries an advanced-query parameter it does not need", q)
	}
}

// A quote is the only character that can end an OData string literal, and doubling it
// is how OData writes one inside a literal. Everything a person types therefore stays
// inside the literal it was put in — including text built to close it and append a
// clause of its own.
func TestLookupKeepsEveryTermInsideItsLiteral(t *testing.T) {
	for term, literal := range map[string]string{
		"O'Brien":                "'O''Brien'",
		"a') or startswith(id,'": "'a'') or startswith(id,'''",
		"x' or accountEnabled eq false or displayName eq '": "'x'' or accountEnabled eq false or displayName eq '''",
		"Müller":            "'Müller'",
		"李":                 "'李'",
		"50% & more #1 + ?": "'50% & more #1 + ?'",
	} {
		c := &lookupClient{}
		q := userQuery(term)
		q.SearchFields = []string{"displayName"}
		q.Filters = nil
		q.ActiveOnly = false
		if _, err := RunLookup(context.Background(), "ucc_entra", q, lookupRegistry(c)); err != nil {
			t.Fatalf("%q: %v", term, err)
		}
		_, vals := decoded(t, c.reqs[0].Path)
		if got, want := vals.Get("$filter"), "(startswith(displayName,"+literal+"))"; got != want {
			t.Errorf("%q: $filter = %s, want %s", term, got, want)
		}
		// Nothing past the filter parameter: an & in the term must not start a new one.
		for k := range vals {
			if k != "$filter" && k != "$select" && k != "$top" {
				t.Errorf("%q: the term produced a query parameter %q", term, k)
			}
		}
	}
}

func TestLookupFilterValuesAreLiteralsToo(t *testing.T) {
	c := &lookupClient{}
	q := userQuery("")
	q.Filters = []lookup.Filter{{Property: "department", Value: "R&D' or department eq 'HR"}}
	if _, err := RunLookup(context.Background(), "ucc_entra", q, lookupRegistry(c)); err != nil {
		t.Fatal(err)
	}
	_, vals := decoded(t, c.reqs[0].Path)
	if got, want := vals.Get("$filter"), "department eq 'R&D'' or department eq ''HR' and accountEnabled eq true"; got != want {
		t.Errorf("$filter = %s, want %s", got, want)
	}
}

// Without a term the lookup lists the first page of what its filters admit — the
// department's people — and without a filter either, the first page of active users.
func TestLookupWithoutATermListsWhatTheFiltersAdmit(t *testing.T) {
	for _, tc := range []struct {
		q    lookup.Query
		want string
	}{
		{userQuery(""), "department eq 'IT' and accountEnabled eq true"},
		{lookup.Query{Resource: "users", ActiveOnly: true, Properties: []string{"id"}, PageSize: 5}, "accountEnabled eq true"},
	} {
		c := &lookupClient{}
		if _, err := RunLookup(context.Background(), "ucc_entra", tc.q, lookupRegistry(c)); err != nil {
			t.Fatal(err)
		}
		_, vals := decoded(t, c.reqs[0].Path)
		if got := vals.Get("$filter"); got != tc.want {
			t.Errorf("$filter = %q, want %q", got, tc.want)
		}
	}
	// No term, no filter, disabled accounts included: there is nothing to filter on.
	c := &lookupClient{}
	if _, err := RunLookup(context.Background(), "ucc_entra",
		lookup.Query{Resource: "users", Properties: []string{"id"}, PageSize: 5}, lookupRegistry(c)); err != nil {
		t.Fatal(err)
	}
	if _, vals := decoded(t, c.reqs[0].Path); vals.Has("$filter") {
		t.Errorf("a lookup with nothing to filter on sent $filter=%q", vals.Get("$filter"))
	}
}

// The allowlist is the Worker's own, checked again here although the server checked it
// already: a form, or a request built by hand against the worker API, must not be able
// to make this credential read a property nobody decided a form may read.
func TestLookupRefusesWhatTheResourceDoesNotAllow(t *testing.T) {
	for name, mutate := range map[string]func(*lookup.Query){
		"unknown resource":      func(q *lookup.Query) { q.Resource = "groups" },
		"search on a secret":    func(q *lookup.Query) { q.SearchFields = []string{"passwordProfile"} },
		"search on id":          func(q *lookup.Query) { q.SearchFields = []string{"id"} },
		"filter on a property":  func(q *lookup.Query) { q.Filters = []lookup.Filter{{Property: "employeeId", Value: "1"}} },
		"filter is an OData":    func(q *lookup.Query) { q.Filters = []lookup.Filter{{Property: "department eq 'x' or id", Value: "1"}} },
		"property not readable": func(q *lookup.Query) { q.Properties = []string{"id", "signInActivity"} },
		"no properties":         func(q *lookup.Query) { q.Properties = nil },
		"page too large":        func(q *lookup.Query) { q.PageSize = lookup.MaxPageSize + 1 },
		"term too long":         func(q *lookup.Query) { q.Search = strings.Repeat("a", lookup.MaxSearchLength+1) },
		"control in term":       func(q *lookup.Query) { q.Search = "a\nb" },
		"value too long":        func(q *lookup.Query) { q.Filters[0].Value = strings.Repeat("x", lookup.MaxValueLength+1) },
		"search with no fields": func(q *lookup.Query) { q.SearchFields = nil },
		"value is not an id":    func(q *lookup.Query) { q.Value = "anna@example.com" },
		"duplicate filter": func(q *lookup.Query) {
			q.Filters = append(q.Filters, lookup.Filter{Property: "department", Value: "HR"})
		},
		"next on another path":   func(q *lookup.Query) { q.Next = "https://graph.microsoft.com/v1.0/groups?$skiptoken=x" },
		"next on another host":   func(q *lookup.Query) { q.Next = "https://attacker.example/v1.0/users?$skiptoken=x" },
		"next that is not a URL": func(q *lookup.Query) { q.Next = "/users?$filter=id ne null" },
	} {
		c := &lookupClient{}
		q := userQuery("Ann")
		mutate(&q)
		_, err := RunLookup(context.Background(), "ucc_entra", q, lookupRegistry(c))
		var le *lookup.Error
		if !errors.As(err, &le) || le.Code != lookup.CodeInvalid {
			t.Errorf("%s: err = %v, want %s", name, err, lookup.CodeInvalid)
		}
		if len(c.reqs) != 0 {
			t.Errorf("%s: %d request(s) reached Graph", name, len(c.reqs))
		}
	}
}

func TestLookupNamesAWorkerItDoesNotHoldAsUnconfigured(t *testing.T) {
	_, err := RunLookup(context.Background(), "elsewhere", userQuery("Ann"), lookupRegistry(&lookupClient{}))
	var le *lookup.Error
	if !errors.As(err, &le) || le.Code != lookup.CodeUnconfigured {
		t.Fatalf("err = %v, want %s", err, lookup.CodeUnconfigured)
	}
}

// One page is the answer, never the listing: an autocomplete that followed every
// @odata.nextLink would read the directory a keystroke at a time.
func TestLookupAnswersOnePageAndItsContinuation(t *testing.T) {
	next := "https://graph.microsoft.com/v1.0/users?$filter=x&$top=2&$skiptoken=RFNwdAIAAQAAAD8"
	c := &lookupClient{resp: []any{map[string]any{
		"@odata.context": "https://graph.microsoft.com/v1.0/$metadata#users",
		"value": []any{
			map[string]any{"id": "00000000-0000-0000-0000-000000000001", "displayName": "Anna Müller",
				"mail": "anna.mueller@example.com", "userPrincipalName": "anna@example.com", "department": "IT",
				"passwordProfile": map[string]any{"password": "never"}, "@odata.type": "#microsoft.graph.user"},
			map[string]any{"id": "00000000-0000-0000-0000-000000000002", "displayName": "Ann Ohne Mail",
				"mail": nil, "userPrincipalName": "ann@example.com", "department": "IT"},
		},
		"@odata.nextLink": next,
	}}}
	page, err := RunLookup(context.Background(), "ucc_entra", userQuery("Ann"), lookupRegistry(c))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.reqs) != 1 {
		t.Fatalf("requests = %d, want one: the continuation is the caller's to ask for", len(c.reqs))
	}
	if page.Next != next {
		t.Errorf("next = %q, want Graph's own link verbatim", page.Next)
	}
	if len(page.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(page.Items))
	}
	first := page.Items[0]
	for _, k := range []string{"passwordProfile", "@odata.type"} {
		if _, ok := first[k]; ok {
			t.Errorf("item carries %q, which the query did not ask for", k)
		}
	}
	if first["displayName"] != "Anna Müller" || first["department"] != "IT" {
		t.Errorf("item = %v", first)
	}
	if v, ok := page.Items[1]["mail"]; !ok || v != nil {
		t.Errorf("a user without mail reads mail = %#v (present %v), want an explicit null", v, ok)
	}
}

func TestLookupFollowsTheContinuationItWasGiven(t *testing.T) {
	next := "https://graph.microsoft.com/v1.0/users?$filter=x&$top=25&$skiptoken=abc"
	c := &lookupClient{}
	q := userQuery("Ann")
	q.Next = next
	if _, err := RunLookup(context.Background(), "ucc_entra", q, lookupRegistry(c)); err != nil {
		t.Fatal(err)
	}
	if len(c.reqs) != 1 || c.reqs[0].Path != next {
		t.Fatalf("requests = %+v, want the continuation verbatim", c.reqs)
	}
}

func TestLookupAnswersAnEmptyDirectoryWithAnEmptyPage(t *testing.T) {
	page, err := RunLookup(context.Background(), "ucc_entra", userQuery("Zzz"), lookupRegistry(&lookupClient{}))
	if err != nil {
		t.Fatal(err)
	}
	if page.Items == nil || len(page.Items) != 0 || page.Next != "" {
		t.Errorf("page = %+v, want an empty, non-nil list and no continuation", page)
	}
}

func TestLookupRefusesAnAnswerThatIsNotACollection(t *testing.T) {
	for _, resp := range []any{"nonsense", map[string]any{"value": "x"}, map[string]any{}} {
		_, err := RunLookup(context.Background(), "ucc_entra", userQuery("Ann"),
			lookupRegistry(&lookupClient{resp: []any{resp}}))
		var le *lookup.Error
		if !errors.As(err, &le) || le.Code != lookup.CodeUnavailable {
			t.Errorf("resp %#v: err = %v, want %s", resp, err, lookup.CodeUnavailable)
		}
	}
}

const annaID = "00000000-0000-0000-0000-000000000001"

// A value lookup reads the one user by id and holds it to the query's filters, so a
// submission cannot carry somebody the form would never have offered.
func TestLookupByValueReadsTheUserAndHoldsItToTheFilters(t *testing.T) {
	user := func(dept string, enabled bool) map[string]any {
		return map[string]any{"id": annaID, "displayName": "Anna Müller", "mail": "anna@example.com",
			"userPrincipalName": "anna@example.com", "department": dept, "accountEnabled": enabled}
	}
	for _, tc := range []struct {
		name  string
		resp  map[string]any
		found bool
	}{
		{"in the department", user("IT", true), true},
		{"case differs", user("it", true), true},
		{"another department", user("Finance", true), false},
		{"disabled", user("IT", false), false},
	} {
		c := &lookupClient{resp: []any{tc.resp}}
		q := userQuery("")
		q.Value = annaID
		page, err := RunLookup(context.Background(), "ucc_entra", q, lookupRegistry(c))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		path, vals := decoded(t, c.reqs[0].Path)
		if path != "/users/"+annaID {
			t.Errorf("%s: path = %q, want the user by id", tc.name, path)
		}
		if got := vals.Get("$select"); got != "id,displayName,mail,userPrincipalName,department,accountEnabled" {
			t.Errorf("%s: $select = %q, want the properties plus what the filters need", tc.name, got)
		}
		if got := len(page.Items) == 1; got != tc.found {
			t.Errorf("%s: found = %v, want %v", tc.name, got, tc.found)
		}
		if tc.found {
			if _, ok := page.Items[0]["accountEnabled"]; ok {
				t.Errorf("%s: the item carries accountEnabled, which was read only to check it", tc.name)
			}
		}
	}
}

func TestLookupByValueOfNobodyIsAnEmptyPage(t *testing.T) {
	c := &lookupClient{errs: []error{&GraphError{Method: "GET", Path: "/users/x", Status: 404, Code: "Request_ResourceNotFound"}}}
	q := userQuery("")
	q.Value = annaID
	page, err := RunLookup(context.Background(), "ucc_entra", q, lookupRegistry(c))
	if err != nil {
		t.Fatalf("err = %v, want an empty page: an id that is nobody's is an answer", err)
	}
	if len(page.Items) != 0 {
		t.Errorf("items = %v", page.Items)
	}
}

// A provider's failure becomes a code, and nothing of the provider's text — Graph's
// error carries the request path, and the path carries the person's term.
func TestLookupTurnsGraphFailuresIntoCodesWithoutTheTerm(t *testing.T) {
	const term = "Geheimname"
	for _, tc := range []struct {
		err   error
		code  string
		retry int
	}{
		{&GraphError{Status: 429, Path: "/users?$filter=" + term, RetryAfter: 7 * time.Second}, lookup.CodeThrottled, 7},
		{&GraphError{Status: 429, Path: "/users?$filter=" + term}, lookup.CodeThrottled, 0},
		{&GraphError{Status: 403, Code: "Authorization_RequestDenied", Path: term}, lookup.CodeRefused, 0},
		{&GraphError{Status: 401, Path: term}, lookup.CodeRefused, 0},
		{&GraphError{Status: 400, Code: "Request_UnsupportedQuery", Path: term}, lookup.CodeRefused, 0},
		{&GraphError{Status: 503, Path: term}, lookup.CodeUnavailable, 0},
		{&GraphError{Status: 404, Path: term}, lookup.CodeUnavailable, 0},
		{errors.New("dial tcp: connection refused " + term), lookup.CodeUnavailable, 0},
	} {
		_, err := RunLookup(context.Background(), "ucc_entra", userQuery(term),
			lookupRegistry(&lookupClient{errs: []error{tc.err}}))
		var le *lookup.Error
		if !errors.As(err, &le) || le.Code != tc.code || le.RetryAfter != tc.retry {
			t.Errorf("%v: err = %#v, want %s retry %d", tc.err, err, tc.code, tc.retry)
			continue
		}
		if strings.Contains(err.Error(), term) {
			t.Errorf("%v: the error %q repeats the term", tc.err, err)
		}
	}
}

// Retry-After is Graph's own advice on how long to back off, and the only useful thing
// a throttled lookup can tell the person, so the client keeps it.
func TestGraphClientKeepsRetryAfterOnAThrottledAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "9")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"TooManyRequests","message":"slow down"}}`))
	}))
	defer srv.Close()
	c := NewGraphClient(staticToken{tok: "t"}, srv.URL, srv.Client())
	_, err := c.Call(context.Background(), Request{Method: "GET", Path: "/users"})
	var ge *GraphError
	if !errors.As(err, &ge) || ge.Status != 429 || ge.RetryAfter != 9*time.Second {
		t.Fatalf("err = %#v, want a 429 with RetryAfter 9s", err)
	}
}

// Retry-After is advice in seconds, as Graph sends it, or an HTTP date. Anything else —
// a date already past, a negative number, a word — is no advice, and the person is not
// told to wait for it.
func TestRetryAfterIsReadAsSecondsOrADate(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	for h, want := range map[string]time.Duration{
		"":                              0,
		"10":                            10 * time.Second,
		" 3 ":                           3 * time.Second,
		"0":                             0,
		"-5":                            0,
		"soon":                          0,
		"Fri, 23 Oct 2026 12:00:30 GMT": 30 * time.Second,
		"Fri, 23 Oct 2026 11:59:00 GMT": 0,
	} {
		if got := retryAfter(h, now); got != want {
			t.Errorf("retryAfter(%q) = %v, want %v", h, got, want)
		}
	}
}

// A record Graph returns without its id cannot be chosen, so it is not offered; an
// answer whose records are not objects is a provider failure, not a short page.
func TestLookupOffersOnlyRecordsThatCanBeChosen(t *testing.T) {
	c := &lookupClient{resp: []any{map[string]any{"value": []any{
		map[string]any{"id": annaID, "displayName": "Anna"},
		map[string]any{"displayName": "Without an id"},
	}}}}
	page, err := RunLookup(context.Background(), "ucc_entra", userQuery("An"), lookupRegistry(c))
	if err != nil || len(page.Items) != 1 || page.Items[0]["id"] != annaID {
		t.Fatalf("page = %+v (%v)", page, err)
	}
	c = &lookupClient{resp: []any{map[string]any{"value": []any{"not a record"}}}}
	_, err = RunLookup(context.Background(), "ucc_entra", userQuery("An"), lookupRegistry(c))
	var le *lookup.Error
	if !errors.As(err, &le) || le.Code != lookup.CodeUnavailable {
		t.Fatalf("err = %v, want %s", err, lookup.CodeUnavailable)
	}
	// The same for a value lookup whose answer is not one record.
	c = &lookupClient{resp: []any{[]any{}}}
	q := userQuery("")
	q.Value = annaID
	if _, err := RunLookup(context.Background(), "ucc_entra", q, lookupRegistry(c)); !errors.As(err, &le) || le.Code != lookup.CodeUnavailable {
		t.Fatalf("value lookup: err = %v, want %s", err, lookup.CodeUnavailable)
	}
}

// A query that names no page size gets the default page, never Graph's own default.
func TestLookupAsksForTheDefaultPageWhenNoneIsNamed(t *testing.T) {
	c := &lookupClient{}
	q := userQuery("An")
	q.PageSize = 0
	if _, err := RunLookup(context.Background(), "ucc_entra", q, lookupRegistry(c)); err != nil {
		t.Fatal(err)
	}
	if _, vals := decoded(t, c.reqs[0].Path); vals.Get("$top") != "25" {
		t.Errorf("$top = %q, want 25", vals.Get("$top"))
	}
}

// Only an absolute link on the tenant's own endpoint continues a listing, and only a
// known resource has a value shape.
func TestAContinuationAndAValueAreHeldToTheResource(t *testing.T) {
	base := "https://graph.microsoft.com/v1.0"
	for _, next := range []string{"/v1.0/users?$skiptoken=1", "://bad", "https://graph.microsoft.com/v1.0/groups?$skiptoken=1"} {
		if continues(base, "/users", next) {
			t.Errorf("%q was taken for a continuation of /users", next)
		}
	}
	if !continues(base, "/users", base+"/users?$skiptoken=1") {
		t.Error("the tenant's own next link was refused")
	}
	if ValidLookupValue("devices", annaID) {
		t.Error("a value was accepted for a resource that has none")
	}
}
