package formlookup

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/workerquery"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/lookup"
)

const (
	anna   = "00000000-0000-0000-0000-000000000001"
	beat   = "00000000-0000-0000-0000-000000000002"
	taskID = 42
)

type fakeTask struct {
	schema string
	holder string
}

type fakes struct {
	tasks   map[uint64]fakeTask
	workers map[string]Worker
	asked   []lookup.Query
	jobType string
	conn    string
	page    lookup.Page
	err     error
	allowed int
	// timeout is what the last Ask was given; the *Err fields stand in for a store
	// that cannot be read.
	timeout    time.Duration
	taskErr    error
	workerErr  error
	workersErr error
}

func newFakes() *fakes {
	return &fakes{
		tasks:   map[uint64]fakeTask{taskID: {schema: exampleForm, holder: "anna"}},
		workers: map[string]Worker{"ucc_entra": {Name: "ucc_entra", WorkerType: "entra", Enabled: true, FormLookups: true}},
		allowed: 1000,
	}
}

func (f *fakes) service() *Service { return New(f.config()) }

func (f *fakes) config() Config {
	return Config{
		Task: func(r *http.Request, key uint64) (Task, error) {
			if f.taskErr != nil {
				return Task{}, f.taskErr
			}
			t, ok := f.tasks[key]
			if !ok {
				return Task{}, ErrNoTask
			}
			if t.holder != "" && r.Header.Get("X-User") != t.holder {
				return Task{}, ErrForbidden
			}
			return Task{Schema: []byte(t.schema)}, nil
		},
		Worker: func(name string) (Worker, bool, error) {
			w, ok := f.workers[name]
			return w, ok, f.workerErr
		},
		Workers: func(*http.Request) ([]Worker, error) {
			if f.workersErr != nil {
				return nil, f.workersErr
			}
			var out []Worker
			for _, w := range f.workers {
				out = append(out, w)
			}
			return out, nil
		},
		Ask: func(ctx context.Context, jobType, connector string, q lookup.Query, timeout time.Duration) (lookup.Page, error) {
			f.asked = append(f.asked, q)
			f.jobType, f.conn, f.timeout = jobType, connector, timeout
			return f.page, f.err
		},
		Allow: func(string) bool {
			f.allowed--
			return f.allowed >= 0
		},
		Principal: func(r *http.Request) string { return r.Header.Get("X-User") },
		Timeout:   time.Second,
		MaxBody:   4096,
	}
}

type reply struct {
	status int
	body   map[string]any
	header http.Header
}

func (f *fakes) query(t *testing.T, s *Service, user string, key uint64, field, body string) reply {
	t.Helper()
	return f.queryAt(t, s, user, "/api/v1/tasks/"+strconv.FormatUint(key, 10)+"/form-data/"+field+"/query", body)
}

func (f *fakes) queryAt(t *testing.T, s *Service, user, path, body string) reply {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/tasks/{key}/form-data/{field}/query", s.HandleQuery)
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req.Header.Set("X-User", user)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return reply{rec.Code, out, rec.Header()}
}

func code(r reply) string { s, _ := r.body["code"].(string); return s }

func TestASearchReachesTheFormsWorkerAsAStructuredQuery(t *testing.T) {
	f := newFakes()
	f.page = lookup.Page{Items: []map[string]any{
		{"id": anna, "displayName": "Anna Müller", "mail": "anna.mueller@example.com", "userPrincipalName": "anna@example.com", "department": "IT"},
		{"id": beat, "displayName": "Beat Ohne Mail", "mail": nil, "userPrincipalName": "beat@example.com", "department": "IT",
			"passwordProfile": "leak"},
	}, Next: "https://graph.microsoft.com/v1.0/users?$skiptoken=1"}
	s := f.service()
	r := f.query(t, s, "anna", taskID, "user", `{"search":"  Ann ","filters":{"department":"IT"}}`)
	if r.status != http.StatusOK {
		t.Fatalf("status %d %v", r.status, r.body)
	}
	if f.jobType != compiler.EntraJobType || f.conn != "ucc_entra" {
		t.Errorf("routed to %s/%s", f.jobType, f.conn)
	}
	q := f.asked[0]
	want := lookup.Query{Resource: "users", Search: "Ann", SearchFields: []string{"displayName", "mail", "userPrincipalName"},
		Filters: []lookup.Filter{{Property: "department", Value: "IT"}}, ActiveOnly: true,
		Properties: []string{"id", "displayName", "mail", "userPrincipalName", "department"}, PageSize: 25}
	if gj, wj := mustJSON(q), mustJSON(want); gj != wj {
		t.Errorf("query =\n  %s\nwant\n  %s", gj, wj)
	}
	items := r.body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %v", items)
	}
	first := items[0].(map[string]any)
	if first["value"] != anna || first["label"] != "Anna Müller" || first["description"] != "anna.mueller@example.com" {
		t.Errorf("first = %v", first)
	}
	if d := first["details"].(map[string]any); d["department"] != "IT" {
		t.Errorf("details = %v", d)
	}
	second := items[1].(map[string]any)
	if second["description"] != "beat@example.com" {
		t.Errorf("a user without mail reads %v, want the user principal name", second["description"])
	}
	if raw := mustJSON(r.body); strings.Contains(raw, "passwordProfile") || strings.Contains(raw, "leak") ||
		strings.Contains(raw, "skiptoken") || strings.Contains(raw, "graph.microsoft.com") {
		t.Errorf("the answer carries what it must not: %s", raw)
	}
	if c, _ := r.body["nextCursor"].(string); c == "" {
		t.Errorf("no cursor for a page with a continuation: %v", r.body)
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func TestACursorContinuesTheSameSearchAndNothingElse(t *testing.T) {
	f := newFakes()
	f.page = lookup.Page{Items: []map[string]any{{"id": anna, "displayName": "Anna"}}, Next: "https://graph.microsoft.com/v1.0/users?$skiptoken=1"}
	s := f.service()
	first := f.query(t, s, "anna", taskID, "user", `{"search":"Ann","filters":{"department":"IT"}}`)
	cursor := first.body["nextCursor"].(string)
	f.page = lookup.Page{Items: []map[string]any{{"id": beat, "displayName": "Annette"}}}
	next := f.query(t, s, "anna", taskID, "user", `{"search":"Ann","filters":{"department":"IT"},"cursor":"`+cursor+`"}`)
	if next.status != http.StatusOK || f.asked[1].Next != "https://graph.microsoft.com/v1.0/users?$skiptoken=1" {
		t.Fatalf("status %d, next %q", next.status, f.asked[1].Next)
	}
	if next.body["nextCursor"] != nil {
		t.Errorf("the last page has a cursor: %v", next.body["nextCursor"])
	}
	for name, body := range map[string]string{
		"another term":       `{"search":"Bea","filters":{"department":"IT"},"cursor":"` + cursor + `"}`,
		"another department": `{"search":"Ann","filters":{"department":"Finance"},"cursor":"` + cursor + `"}`,
		"a forged cursor":    `{"search":"Ann","filters":{"department":"IT"},"cursor":"` + cursor[:len(cursor)-3] + `abc"}`,
	} {
		if r := f.query(t, s, "anna", taskID, "user", body); r.status != http.StatusBadRequest || code(r) != "invalid_cursor" {
			t.Errorf("%s: %d %v", name, r.status, r.body)
		}
	}
	f.tasks[taskID] = fakeTask{schema: exampleForm} // open work: anybody may ask
	if r := f.query(t, s, "beat", taskID, "user", `{"search":"Ann","filters":{"department":"IT"},"cursor":"`+cursor+`"}`); code(r) != "invalid_cursor" {
		t.Errorf("another person's cursor: %d %v", r.status, r.body)
	}
}

// The task is the authority: a person who may not work on it may not search through it,
// whatever the form says.
func TestALookupIsAnsweredOnlyForTheTasksHolder(t *testing.T) {
	f := newFakes()
	s := f.service()
	if r := f.query(t, s, "mallory", taskID, "user", `{"search":"Ann"}`); r.status != http.StatusForbidden || code(r) != "forbidden" {
		t.Errorf("another person's task: %d %v", r.status, r.body)
	}
	if r := f.query(t, s, "anna", 99, "user", `{"search":"Ann"}`); r.status != http.StatusNotFound || code(r) != "not_found" {
		t.Errorf("no such task: %d %v", r.status, r.body)
	}
	if r := f.query(t, s, "anna", taskID, "abteilung", `{"search":"Ann"}`); r.status != http.StatusNotFound {
		t.Errorf("a field that is no directory field: %d %v", r.status, r.body)
	}
	if r := f.query(t, s, "anna", taskID, "nobody", `{"search":"Ann"}`); r.status != http.StatusNotFound {
		t.Errorf("no such field: %d %v", r.status, r.body)
	}
	if len(f.asked) != 0 {
		t.Errorf("%d queries reached a worker", len(f.asked))
	}
}

// The Worker is the operator's: a form can name it, but only a Worker its editors opened
// for forms answers one.
func TestALookupNeedsAWorkerThatAllowsIt(t *testing.T) {
	for name, tc := range map[string]struct {
		worker *Worker
		status int
		code   string
	}{
		"missing":           {nil, http.StatusUnprocessableEntity, "invalid_configuration"},
		"another type":      {&Worker{Name: "ucc_entra", WorkerType: "sharepoint", Enabled: true, FormLookups: true}, http.StatusUnprocessableEntity, "invalid_configuration"},
		"disabled":          {&Worker{Name: "ucc_entra", WorkerType: "entra", Enabled: false, FormLookups: true}, http.StatusServiceUnavailable, "worker_unavailable"},
		"not open to forms": {&Worker{Name: "ucc_entra", WorkerType: "entra", Enabled: true}, http.StatusForbidden, "forbidden"},
	} {
		f := newFakes()
		delete(f.workers, "ucc_entra")
		if tc.worker != nil {
			f.workers["ucc_entra"] = *tc.worker
		}
		r := f.query(t, f.service(), "anna", taskID, "user", `{"search":"Ann"}`)
		if r.status != tc.status || code(r) != tc.code {
			t.Errorf("%s: %d %v, want %d %s", name, r.status, r.body, tc.status, tc.code)
		}
		if len(f.asked) != 0 {
			t.Errorf("%s: a query reached a worker", name)
		}
	}
}

// The request says what the person typed and what they picked elsewhere on the form —
// nothing about which Worker, which tenant, which resource or which properties.
func TestALookupRequestCannotChooseWhatTheServerDecides(t *testing.T) {
	f := newFakes()
	s := f.service()
	long := strings.Repeat("a", lookup.MaxSearchLength+1)
	for name, body := range map[string]string{
		"another worker":                  `{"search":"Ann","worker":"contoso"}`,
		"another tenant":                  `{"search":"Ann","tenant":"contoso.onmicrosoft.com"}`,
		"another resource":                `{"search":"Ann","resource":"groups"}`,
		"a Graph URL":                     `{"search":"Ann","url":"https://graph.microsoft.com/v1.0/users?$filter=id ne null"}`,
		"a raw filter":                    `{"search":"Ann","$filter":"id ne null"}`,
		"properties":                      `{"search":"Ann","properties":["passwordProfile"]}`,
		"an unbound filter":               `{"search":"Ann","filters":{"jobTitle":"CEO"}}`,
		"an OData filter key":             `{"search":"Ann","filters":{"department eq 'IT' or id":"x"}}`,
		"a department off list":           `{"search":"Ann","filters":{"department":"HR"}}`,
		"too short":                       `{"search":"A"}`,
		"empty":                           `{"search":""}`,
		"too long":                        `{"search":"` + long + `"}`,
		"control characters":              `{"search":"An\u0000n"}`,
		"a page too large":                `{"search":"Ann","limit":26}`,
		"a value that is no id":           `{"value":"anna@example.com"}`,
		"a search and a value":            `{"search":"Ann","value":"` + anna + `"}`,
		"a value with a cursor":           `{"value":"` + anna + `","cursor":"x"}`,
		"a control character in a filter": `{"search":"Ann","filters":{"department":"I\u0001T"}}`,
		"not JSON":                        `{"search":`,
	} {
		r := f.query(t, s, "anna", taskID, "user", body)
		if r.status != http.StatusBadRequest || code(r) != "invalid_query" {
			t.Errorf("%s: %d %v, want 400 invalid_query", name, r.status, r.body)
		}
	}
	if len(f.asked) != 0 {
		t.Errorf("%d refused requests reached a worker", len(f.asked))
	}
	if r := f.query(t, s, "anna", taskID, "user", `{"search":"`+strings.Repeat("x", 5000)+`"}`); r.status != http.StatusRequestEntityTooLarge {
		t.Errorf("an oversized body: %d", r.status)
	}
}

// A fixed filter is the form's, applied whatever the request says; a request cannot
// lift it, and cannot even name it.
func TestAFixedFilterHoldsWhateverTheRequestSays(t *testing.T) {
	f := newFakes()
	f.tasks[taskID] = fakeTask{schema: strings.Replace(exampleForm, `"field": "abteilung"`, `"value": "IT"`, 1)}
	s := f.service()
	if r := f.query(t, s, "anna", taskID, "user", `{"search":"Ann","filters":{"department":"Finance"}}`); r.status != http.StatusBadRequest {
		t.Errorf("overriding the fixed filter: %d %v", r.status, r.body)
	}
	if r := f.query(t, s, "anna", taskID, "user", `{"search":"Ann"}`); r.status != http.StatusOK {
		t.Fatalf("%d %v", r.status, r.body)
	}
	if got := f.asked[0].Filters; len(got) != 1 || got[0] != (lookup.Filter{Property: "department", Value: "IT"}) {
		t.Errorf("filters = %v", got)
	}
}

func TestARequiredBoundFilterMustBeChosenFirst(t *testing.T) {
	f := newFakes()
	f.tasks[taskID] = fakeTask{schema: strings.Replace(exampleForm, `"field": "abteilung"`, `"field": "abteilung", "required": true`, 1)}
	s := f.service()
	r := f.query(t, s, "anna", taskID, "user", `{"search":"Ann"}`)
	if r.status != http.StatusBadRequest || !strings.Contains(r.body["error"].(string), "Abteilung") {
		t.Errorf("%d %v, want a refusal naming the field to choose first", r.status, r.body)
	}
	// Unbound, an empty department simply does not narrow the search.
	f.tasks[taskID] = fakeTask{schema: exampleForm}
	if r := f.query(t, s, "anna", taskID, "user", `{"search":"Ann","filters":{"department":""}}`); r.status != http.StatusOK {
		t.Fatalf("%d %v", r.status, r.body)
	}
	if len(f.asked[0].Filters) != 0 {
		t.Errorf("filters = %v, want none", f.asked[0].Filters)
	}
}

func TestALookupByValueAsksForThatOneRecord(t *testing.T) {
	f := newFakes()
	f.page = lookup.Page{Items: []map[string]any{{"id": anna, "displayName": "Anna Müller", "mail": "a@example.com"}}}
	r := f.query(t, f.service(), "anna", taskID, "user", `{"value":"`+anna+`","filters":{"department":"IT"}}`)
	if r.status != http.StatusOK || len(r.body["items"].([]any)) != 1 {
		t.Fatalf("%d %v", r.status, r.body)
	}
	if q := f.asked[0]; q.Value != anna || q.Search != "" || len(q.Filters) != 1 {
		t.Errorf("query = %+v", q)
	}
}

// A misbehaving worker cannot put a record in front of a person that the form could not
// submit.
func TestARecordWithoutAValidValueIsNotOffered(t *testing.T) {
	f := newFakes()
	f.page = lookup.Page{Items: []map[string]any{{"id": "not-a-guid", "displayName": "X"}, {"displayName": "no id"}, {"id": anna, "displayName": "Anna"}}}
	r := f.query(t, f.service(), "anna", taskID, "user", `{"search":"Ann"}`)
	if items := r.body["items"].([]any); len(items) != 1 {
		t.Fatalf("items = %v, want only the valid one", items)
	}
}

func TestALookupFailureIsAnsweredWithItsCode(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
		retry  string
	}{
		{workerquery.ErrUnavailable, 503, "worker_unavailable", ""},
		{workerquery.ErrBusy, 503, "worker_busy", "2"},
		{workerquery.ErrTimeout, 504, "timeout", ""},
		{&lookup.Error{Code: lookup.CodeThrottled, RetryAfter: 9}, 503, "provider_throttled", "9"},
		{lookup.Fail(lookup.CodeUnavailable), 502, "provider_unavailable", ""},
		{lookup.Fail(lookup.CodeRefused), 502, "provider_refused", ""},
		{lookup.Fail(lookup.CodeUnconfigured), 503, "worker_unavailable", ""},
		{lookup.Fail(lookup.CodeInvalid), 422, "invalid_configuration", ""},
		{errors.New("something odd"), 502, "provider_unavailable", ""},
	} {
		f := newFakes()
		f.err = tc.err
		r := f.query(t, f.service(), "anna", taskID, "user", `{"search":"Ann"}`)
		if r.status != tc.status || code(r) != tc.code || r.header.Get("Retry-After") != tc.retry {
			t.Errorf("%v: %d %v Retry-After %q, want %d %s %q", tc.err, r.status, r.body, r.header.Get("Retry-After"), tc.status, tc.code, tc.retry)
		}
		if msg, _ := r.body["error"].(string); msg == "" || strings.Contains(msg, "Ann") {
			t.Errorf("%v: message %q", tc.err, msg)
		}
	}
}

func TestALookupIsRateLimitedPerPerson(t *testing.T) {
	f := newFakes()
	f.allowed = 1
	s := f.service()
	if r := f.query(t, s, "anna", taskID, "user", `{"search":"Ann"}`); r.status != http.StatusOK {
		t.Fatalf("%d", r.status)
	}
	r := f.query(t, s, "anna", taskID, "user", `{"search":"Anna"}`)
	if r.status != http.StatusTooManyRequests || code(r) != "rate_limited" || r.header.Get("Retry-After") == "" {
		t.Errorf("%d %v", r.status, r.body)
	}
	if len(f.asked) != 1 {
		t.Errorf("a throttled request reached a worker")
	}
}

func TestTheSourcesListingSaysWhatCanBeConfigured(t *testing.T) {
	f := newFakes()
	f.workers["closed"] = Worker{Name: "closed", WorkerType: "entra", Enabled: true}
	s := f.service()
	rec := httptest.NewRecorder()
	s.HandleSources(rec, httptest.NewRequest("GET", "/api/v1/form-data/sources", nil))
	var out struct {
		Providers []map[string]any `json:"providers"`
		Workers   []map[string]any `json:"workers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if len(out.Providers) != 1 || out.Providers[0]["workerType"] != "entra" || out.Providers[0]["resource"] != "users" {
		t.Errorf("providers = %v", out.Providers)
	}
	if len(out.Workers) != 2 || out.Workers[0]["name"] != "closed" || out.Workers[0]["formLookups"] != false {
		t.Errorf("workers = %v, want both, sorted, each saying whether forms may use it", out.Workers)
	}
}

// A service built without a deadline or a body budget gets the defaults, not a lookup
// that never ends or a request that is never read.
func TestALookupRunsUnderTheDefaultsItWasNotGiven(t *testing.T) {
	f := newFakes()
	cfg := f.config()
	cfg.Timeout, cfg.MaxBody = 0, 0
	s := New(cfg)
	if r := f.query(t, s, "anna", taskID, "user", `{"search":"Ann","filters":{"department":"IT"}}`); r.status != http.StatusOK {
		t.Fatalf("%d %v", r.status, r.body)
	}
	if f.timeout != DefaultTimeout {
		t.Errorf("asked with %v, want %v", f.timeout, DefaultTimeout)
	}
	huge := `{"search":"` + strings.Repeat("x", 1<<16) + `"}`
	if r := f.query(t, s, "anna", taskID, "user", huge); r.status != http.StatusRequestEntityTooLarge {
		t.Errorf("a body past the default budget: %d", r.status)
	}
}

// What the lookup cannot read is said as such: no task under a key that is no number,
// and a server error — not a forbidden or an empty result — when a store fails.
func TestALookupSaysWhenItCannotReadWhatItNeeds(t *testing.T) {
	f := newFakes()
	s := f.service()
	if r := f.queryAt(t, s, "anna", "/api/v1/tasks/abc/form-data/user/query", `{"search":"Ann"}`); r.status != http.StatusNotFound || code(r) != "not_found" {
		t.Errorf("a key that is no number: %d %v", r.status, r.body)
	}
	f.taskErr = errors.New("store closed")
	if r := f.query(t, s, "anna", taskID, "user", `{"search":"Ann"}`); r.status != http.StatusInternalServerError {
		t.Errorf("an unreadable task: %d %v", r.status, r.body)
	}
	f.taskErr = nil
	f.workerErr = errors.New("store closed")
	if r := f.query(t, s, "anna", taskID, "user", `{"search":"Ann"}`); r.status != http.StatusInternalServerError || code(r) != "internal" {
		t.Errorf("an unreadable Worker: %d %v", r.status, r.body)
	}
	f.workerErr = nil
	// A form stored before this server's rules, or by a newer server, is not searched.
	f.tasks[taskID] = fakeTask{schema: strings.Replace(exampleForm, `"version": 1`, `"version": 2`, 1)}
	if r := f.query(t, s, "", taskID, "user", `{"search":"Ann"}`); r.status != http.StatusUnprocessableEntity || code(r) != "invalid_configuration" {
		t.Errorf("a form this server cannot read: %d %v", r.status, r.body)
	}
	if len(f.asked) != 0 {
		t.Errorf("%d of these reached a worker", len(f.asked))
	}
	f.workersErr = errors.New("store closed")
	rec := httptest.NewRecorder()
	s.HandleSources(rec, httptest.NewRequest("GET", "/api/v1/form-data/sources", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("an unreadable Worker list: %d %s", rec.Code, rec.Body)
	}
}

func TestASmallerPageMayBeAskedFor(t *testing.T) {
	f := newFakes()
	if r := f.query(t, f.service(), "anna", taskID, "user", `{"search":"Ann","filters":{"department":"IT"},"limit":10}`); r.status != http.StatusOK {
		t.Fatalf("%d %v", r.status, r.body)
	}
	if got := f.asked[0].PageSize; got != 10 {
		t.Errorf("page size %d, want 10", got)
	}
}

// The person typed on and the browser withdrew the request: nothing is written for it,
// since nobody reads the answer.
func TestAWithdrawnSearchIsNotAnswered(t *testing.T) {
	f := newFakes()
	f.err = context.Canceled
	s := f.service()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest("POST", "/api/v1/tasks/42/form-data/user/query",
		strings.NewReader(`{"search":"Ann","filters":{"department":"IT"}}`)).WithContext(ctx)
	req.Header.Set("X-User", "anna")
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/tasks/{key}/form-data/{field}/query", s.HandleQuery)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Body.Len() != 0 {
		t.Errorf("a withdrawn search was answered: %d %s", rec.Code, rec.Body)
	}
}

// An option shows each thing once and as text: a mail that is the display name is not
// repeated beneath it, and a detail Graph sends as a number reads as one.
func TestAnOptionShowsEachThingOnceAndAsText(t *testing.T) {
	f := newFakes()
	f.page = lookup.Page{Items: []map[string]any{
		{"id": anna, "displayName": "service@example.com", "mail": "service@example.com", "department": float64(42)},
	}}
	r := f.query(t, f.service(), "anna", taskID, "user", `{"search":"ser","filters":{"department":"IT"}}`)
	if r.status != http.StatusOK {
		t.Fatalf("%d %v", r.status, r.body)
	}
	it := r.body["items"].([]any)[0].(map[string]any)
	if it["label"] != "service@example.com" || it["description"] != nil && it["description"] != "" {
		t.Errorf("item = %v, want the label once", it)
	}
	if d := it["details"].(map[string]any); d["department"] != "42" {
		t.Errorf("details = %v", d)
	}
}

// A required filter whose field has no label is named by its key, so the person is
// still told which field to fill in first.
func TestARequiredFilterIsNamedByItsKeyWhenItsFieldHasNoLabel(t *testing.T) {
	f := newFakes()
	schema := strings.Replace(exampleForm, `"key": "abteilung", "label": "Abteilung",`, `"key": "abteilung",`, 1)
	schema = strings.Replace(schema, `"field": "abteilung"`, `"field": "abteilung", "required": true`, 1)
	f.tasks[taskID] = fakeTask{schema: schema}
	r := f.query(t, f.service(), "", taskID, "user", `{"search":"Ann"}`)
	if msg, _ := r.body["error"].(string); r.status != http.StatusBadRequest || !strings.Contains(msg, "abteilung") {
		t.Errorf("%d %v, want a refusal naming abteilung", r.status, r.body)
	}
}

// A filter bound to a select whose options come from a variable has no fixed options
// the server could hold it to, so the value is searched as given.
func TestAFilterFromAVariableFedSelectIsSearchedAsGiven(t *testing.T) {
	f := newFakes()
	f.tasks[taskID] = fakeTask{schema: strings.Replace(exampleForm,
		`"values": [{"label": "IT", "value": "IT"}, {"label": "Finanzen", "value": "Finance"}]`, `"valuesKey": "abteilungen"`, 1)}
	r := f.query(t, f.service(), "", taskID, "user", `{"search":"Ann","filters":{"department":"HR"}}`)
	if r.status != http.StatusOK {
		t.Fatalf("%d %v", r.status, r.body)
	}
	if got := f.asked[0].Filters; len(got) != 1 || got[0].Value != "HR" {
		t.Errorf("filters = %v", got)
	}
}

// A provider without its Worker Type's rules refuses every query and every value; it
// never lets one through unchecked.
func TestAProviderWithoutItsRulesRefusesEverything(t *testing.T) {
	var p Provider
	var le *lookup.Error
	if err := p.Check(lookup.Query{Resource: "users", Search: "Ann"}); !errors.As(err, &le) || le.Code != lookup.CodeInvalid {
		t.Errorf("Check = %v", err)
	}
	if p.ValidValue(anna) {
		t.Error("ValidValue accepted a value without a rule")
	}
}
