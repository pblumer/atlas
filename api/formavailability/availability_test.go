package formavailability

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/workerquery"
	"github.com/pblumer/atlas/connector/lookup"
)

const siteForm = `{"components":[
  {"type":"textfield","key":"titel","label":"Titel"},
  {"type":"group","components":[
    {"type":"textfield","key":"url","label":"URL","atlasAvailability":{"version":1,"checks":[
      {"workerType":"entra","worker":"ucc_entra","resource":"groups"},
      {"workerType":"entra","worker":"ucc_entra","resource":"deletedGroups"},
      {"workerType":"sharepoint","worker":"ucc_sharepoint","resource":"sites","host":"contoso.sharepoint.com"}
    ]}}
  ]}
]}`

// A block nested in a group is found, every source it names is resolved against the
// provider table, and a form without any block parses to nothing.
func TestParse(t *testing.T) {
	form, err := Parse([]byte(siteForm))
	if err != nil {
		t.Fatal(err)
	}
	f, ok := form.Field("url")
	if !ok || len(f.Providers) != 3 || f.MinLength() != 3 {
		t.Fatalf("field = %#v, %v", f, ok)
	}
	if f.Providers[2].JobType == "" || !f.Providers[2].Located {
		t.Errorf("the SharePoint source = %#v, want a located source with a job type", f.Providers[2])
	}
	if _, ok := form.Field("titel"); ok {
		t.Error("a plain textfield became an availability field")
	}
	if form, err := Parse([]byte(`{"components":[{"type":"textfield","key":"a"}]}`)); err != nil || len(form.Fields) != 0 {
		t.Errorf("plain form = %#v, %v", form, err)
	}
}

// Everything a block could say wrongly is refused at save, with the field named.
func TestCheckSchemaRefusals(t *testing.T) {
	block := func(field string) string {
		return `{"components":[{"type":"textfield","key":"url",` + field + `}]}`
	}
	for name, schema := range map[string]string{
		"a newer version":      block(`"atlasAvailability":{"version":2,"checks":[{"workerType":"entra","worker":"w","resource":"groups"}]}`),
		"no checks":            block(`"atlasAvailability":{"version":1,"checks":[]}`),
		"an unknown source":    block(`"atlasAvailability":{"version":1,"checks":[{"workerType":"entra","worker":"w","resource":"users"}]}`),
		"no worker":            block(`"atlasAvailability":{"version":1,"checks":[{"workerType":"entra","resource":"groups"}]}`),
		"a site without host":  block(`"atlasAvailability":{"version":1,"checks":[{"workerType":"sharepoint","worker":"w","resource":"sites"}]}`),
		"a bad managed path":   block(`"atlasAvailability":{"version":1,"checks":[{"workerType":"sharepoint","worker":"w","resource":"sites","host":"contoso.sharepoint.com","managedPath":"personal"}]}`),
		"a host on a group":    block(`"atlasAvailability":{"version":1,"checks":[{"workerType":"entra","worker":"w","resource":"groups","host":"contoso.sharepoint.com"}]}`),
		"a twice-named source": block(`"atlasAvailability":{"version":1,"checks":[{"workerType":"entra","worker":"w","resource":"groups"},{"workerType":"entra","worker":"w","resource":"groups"}]}`),
		"an unknown member":    block(`"atlasAvailability":{"version":1,"url":"https://evil","checks":[{"workerType":"entra","worker":"w","resource":"groups"}]}`),
		"a zero minLength":     block(`"atlasAvailability":{"version":1,"minLength":0,"checks":[{"workerType":"entra","worker":"w","resource":"groups"}]}`),
		"a select":             `{"components":[{"type":"select","key":"url","atlasAvailability":{"version":1,"checks":[{"workerType":"entra","worker":"w","resource":"groups"}]}}]}`,
		"no key":               `{"components":[{"type":"textfield","atlasAvailability":{"version":1,"checks":[{"workerType":"entra","worker":"w","resource":"groups"}]}}]}`,
	} {
		if err := CheckSchema([]byte(schema)); err == nil {
			t.Errorf("%s: saved", name)
		}
	}
	if err := CheckSchema([]byte(siteForm)); err != nil {
		t.Errorf("a good form refused: %v", err)
	}
}

// fakeBroker answers each query by the resource it names.
type fakeBroker struct {
	mu     sync.Mutex
	asked  []lookup.Query
	types  []string
	taken  map[string]bool
	failOn string
	err    error
}

func (b *fakeBroker) Ask(_ context.Context, jobType, _ string, q lookup.Query, _ time.Duration) (lookup.Page, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.asked = append(b.asked, q)
	b.types = append(b.types, jobType)
	if q.Resource == b.failOn {
		return lookup.Page{}, b.err
	}
	if b.taken[q.Resource] {
		return lookup.Page{Items: []map[string]any{{"x": "y"}}}, nil
	}
	return lookup.Page{Items: []map[string]any{}}, nil
}

func newService(b *fakeBroker, workers map[string]Worker) *Service {
	return New(Config{
		Worker: func(name string) (Worker, bool, error) {
			w, ok := workers[name]
			return w, ok, nil
		},
		Ask: b.Ask,
	})
}

var openWorkers = map[string]Worker{
	"ucc_entra":      {WorkerType: "entra", Enabled: true, FormLookups: true},
	"ucc_sharepoint": {WorkerType: "sharepoint", Enabled: true, FormLookups: true},
}

func check(t *testing.T, s *Service, field, body string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	s.WriteCheck(rec, req, []byte(siteForm), field)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

// A name is free only when every source finds nothing; it is checked lower-cased, the
// SharePoint source is asked at the form's host under "sites", and the answer carries
// nothing but the value and the bit.
func TestWriteCheck(t *testing.T) {
	b := &fakeBroker{taken: map[string]bool{}}
	s := newService(b, openWorkers)
	code, out := check(t, s, "url", `{"value":"  Dev-HuDa "}`)
	if code != http.StatusOK || out["available"] != true || out["value"] != "dev-huda" || len(out) != 2 {
		t.Fatalf("free name: %d %v", code, out)
	}
	if len(b.asked) != 3 {
		t.Fatalf("asked %d sources, want 3", len(b.asked))
	}
	for _, q := range b.asked {
		if q.Value != "dev-huda" {
			t.Errorf("asked %q, want the lower-cased name", q.Value)
		}
		if q.Resource == "sites" {
			if len(q.Filters) != 2 || q.Filters[0].Value != "contoso.sharepoint.com" || q.Filters[1].Value != "sites" {
				t.Errorf("site query filters = %#v", q.Filters)
			}
		}
	}
	for _, resource := range []string{"groups", "deletedGroups", "sites"} {
		b := &fakeBroker{taken: map[string]bool{resource: true}}
		if code, out := check(t, newService(b, openWorkers), "url", `{"value":"dev-huda"}`); code != http.StatusOK || out["available"] != false {
			t.Errorf("taken in %s: %d %v", resource, code, out)
		}
	}
}

// What a request may not say, and what a field cannot check, is refused before any
// Worker is asked.
func TestWriteCheckRefusals(t *testing.T) {
	cases := []struct {
		name, field, body string
		workers           map[string]Worker
		status            int
		code              string
	}{
		{"too short", "url", `{"value":"ab"}`, openWorkers, http.StatusBadRequest, "invalid_value"},
		{"a slash", "url", `{"value":"dev/huda"}`, openWorkers, http.StatusBadRequest, "invalid_value"},
		{"a dot SharePoint refuses", "url", `{"value":"dev.huda"}`, openWorkers, http.StatusBadRequest, "invalid_value"},
		{"another member", "url", `{"value":"dev-huda","worker":"x"}`, openWorkers, http.StatusBadRequest, "invalid_query"},
		{"no such field", "titel", `{"value":"dev-huda"}`, openWorkers, http.StatusNotFound, "not_found"},
		{"a missing Worker", "url", `{"value":"dev-huda"}`, map[string]Worker{"ucc_entra": openWorkers["ucc_entra"]}, http.StatusUnprocessableEntity, "invalid_configuration"},
		{"a Worker of another type", "url", `{"value":"dev-huda"}`, map[string]Worker{"ucc_entra": openWorkers["ucc_entra"], "ucc_sharepoint": {WorkerType: "entra", Enabled: true, FormLookups: true}}, http.StatusUnprocessableEntity, "invalid_configuration"},
		{"a closed Worker", "url", `{"value":"dev-huda"}`, map[string]Worker{"ucc_entra": {WorkerType: "entra", Enabled: true}, "ucc_sharepoint": openWorkers["ucc_sharepoint"]}, http.StatusForbidden, "forbidden"},
		{"a disabled Worker", "url", `{"value":"dev-huda"}`, map[string]Worker{"ucc_entra": {WorkerType: "entra", FormLookups: true}, "ucc_sharepoint": openWorkers["ucc_sharepoint"]}, http.StatusServiceUnavailable, "worker_unavailable"},
	}
	for _, c := range cases {
		b := &fakeBroker{}
		code, out := check(t, newService(b, c.workers), c.field, c.body)
		if code != c.status || out["code"] != c.code {
			t.Errorf("%s: %d %v, want %d %s", c.name, code, out, c.status, c.code)
		}
		if len(b.asked) != 0 {
			t.Errorf("%s: a Worker was asked", c.name)
		}
	}
}

// One source that cannot answer makes the name unknown rather than free.
func TestWriteCheckFailures(t *testing.T) {
	for err, want := range map[error]string{
		workerquery.ErrUnavailable:                               "worker_unavailable",
		workerquery.ErrTimeout:                                   "timeout",
		lookup.Fail(lookup.CodeRefused):                          "provider_refused",
		&lookup.Error{Code: lookup.CodeThrottled, RetryAfter: 3}: "provider_throttled",
		errors.New("boom"):                                       "provider_unavailable",
	} {
		b := &fakeBroker{failOn: "deletedGroups", err: err}
		code, out := check(t, newService(b, openWorkers), "url", `{"value":"dev-huda"}`)
		if code < 400 || out["code"] != want || out["available"] != nil {
			t.Errorf("%v: %d %v, want %s and no answer", err, code, out, want)
		}
	}
}

// The rate limit is asked before anything else, under the caller's name.
func TestWriteCheckRateLimit(t *testing.T) {
	var keys []string
	s := New(Config{Ask: (&fakeBroker{}).Ask, Allow: func(k string) bool { keys = append(keys, k); return false },
		Principal: func(*http.Request) string { return "usr_1" }})
	code, out := check(t, s, "url", `{"value":"dev-huda"}`)
	if code != http.StatusTooManyRequests || out["code"] != "rate_limited" || len(keys) != 1 || keys[0] != "availability:usr_1" {
		t.Errorf("rate limited: %d %v %v", code, out, keys)
	}
}

// The provider table is what the Console reads to offer the formLookups switch: every
// Worker Type an availability check can ask, each source with a job type to route by.
func TestProviders(t *testing.T) {
	kinds := map[string]bool{}
	for _, p := range Providers() {
		if p.JobType == "" || p.Label == "" || p.ValueFormat == "" {
			t.Errorf("%s/%s is missing a job type, a label or a value format", p.WorkerType, p.Resource)
		}
		kinds[p.WorkerType] = true
	}
	for _, k := range []string{"entra", "exchange", "sharepoint"} {
		if !kinds[k] {
			t.Errorf("no source of Worker Type %s", k)
		}
	}
	ps := Providers()
	ps[0].WorkerType = "changed"
	if Providers()[0].WorkerType == "changed" {
		t.Error("Providers hands out the table itself")
	}
}

// A field states its own threshold; a value shorter than it is not asked.
func TestMinLengthIsTheFieldsOwn(t *testing.T) {
	schema := `{"components":[{"type":"textfield","key":"alias","atlasAvailability":{"version":1,"minLength":5,
	  "checks":[{"workerType":"exchange","worker":"ucc_exchange","resource":"recipients"}]}}]}`
	form, err := Parse([]byte(schema))
	if err != nil {
		t.Fatal(err)
	}
	f, _ := form.Field("alias")
	if f.MinLength() != 5 {
		t.Fatalf("MinLength = %d, want 5", f.MinLength())
	}
	b := &fakeBroker{}
	s := New(Config{Worker: func(string) (Worker, bool, error) {
		return Worker{WorkerType: "exchange", Enabled: true, FormLookups: true}, true, nil
	}, Ask: b.Ask})
	rec := httptest.NewRecorder()
	s.WriteCheck(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"value":"abcd"}`)), []byte(schema), "alias")
	if rec.Code != http.StatusBadRequest || len(b.asked) != 0 {
		t.Errorf("a value under the field's threshold: %d, asked %d", rec.Code, len(b.asked))
	}
	rec = httptest.NewRecorder()
	s.WriteCheck(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"value":"abcde"}`)), []byte(schema), "alias")
	if rec.Code != http.StatusOK || len(b.asked) != 1 || b.types[0] == "" {
		t.Errorf("a value at the threshold: %d, asked %d", rec.Code, len(b.asked))
	}
}

// A form that is not a form, and a block nobody can serve, say so rather than check.
func TestMalformedForms(t *testing.T) {
	for name, schema := range map[string]string{
		"not JSON":              `{"atlasAvailability":`,
		"components not a list": `{"components":{"type":"textfield"},"atlasAvailability":1}`,
		"nested not a list":     `{"components":[{"type":"group","components":7,"key":"atlasAvailability"}]}`,
		"two fields one key": `{"components":[
		  {"type":"textfield","key":"a","atlasAvailability":{"version":1,"checks":[{"workerType":"entra","worker":"w","resource":"groups"}]}},
		  {"type":"textfield","key":"a","atlasAvailability":{"version":1,"checks":[{"workerType":"entra","worker":"w","resource":"groups"}]}}]}`,
		"five checks": `{"components":[{"type":"textfield","key":"a","atlasAvailability":{"version":1,"checks":[
		  {"workerType":"entra","worker":"a","resource":"groups"},{"workerType":"entra","worker":"b","resource":"groups"},
		  {"workerType":"entra","worker":"c","resource":"groups"},{"workerType":"entra","worker":"d","resource":"groups"},
		  {"workerType":"entra","worker":"e","resource":"groups"}]}}]}`,
	} {
		if _, err := Parse([]byte(schema)); err == nil {
			t.Errorf("%s: parsed", name)
		}
		rec := httptest.NewRecorder()
		New(Config{}).WriteCheck(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"value":"abc"}`)), []byte(schema), "a")
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: checking answered %d, want 422", name, rec.Code)
		}
	}
}

// What the Worker store and the broker can answer besides a page, and a body too large
// to be a check.
func TestWriteCheckEdges(t *testing.T) {
	ask := func(s *Service, body string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		s.WriteCheck(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), []byte(siteForm), "url")
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	if code, out := ask(New(Config{Ask: (&fakeBroker{}).Ask}), `{"value":"dev-huda"}`); code != http.StatusServiceUnavailable || out["code"] != "worker_unavailable" {
		t.Errorf("no Worker store: %d %v", code, out)
	}
	broken := New(Config{Ask: (&fakeBroker{}).Ask, Worker: func(string) (Worker, bool, error) { return Worker{}, false, errors.New("disk") }})
	if code, _ := ask(broken, `{"value":"dev-huda"}`); code != http.StatusInternalServerError {
		t.Errorf("an unreadable Worker store: %d", code)
	}
	small := New(Config{Ask: (&fakeBroker{}).Ask, Worker: newService(nil, openWorkers).cfg.Worker, MaxBody: 8})
	if code, out := ask(small, `{"value":"dev-huda-and-much-more"}`); code != http.StatusRequestEntityTooLarge || out["code"] != "invalid_query" {
		t.Errorf("a body over the budget: %d %v", code, out)
	}
	for err, want := range map[error]string{
		workerquery.ErrBusy:                  "worker_busy",
		lookup.Fail(lookup.CodeUnconfigured): "worker_unavailable",
		lookup.Fail(lookup.CodeInvalid):      "invalid_configuration",
		context.DeadlineExceeded:             "timeout",
	} {
		b := &fakeBroker{failOn: "groups", err: err}
		if code, out := ask(newService(b, openWorkers), `{"value":"dev-huda"}`); code < 400 || out["code"] != want {
			t.Errorf("%v: %d %v, want %s", err, code, out, want)
		}
	}
	// A person who typed on: the request is withdrawn and nothing is written.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := &fakeBroker{failOn: "groups", err: context.Canceled}
	rec := httptest.NewRecorder()
	newService(b, openWorkers).WriteCheck(rec, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"value":"dev-huda"}`)).WithContext(ctx), []byte(siteForm), "url")
	if rec.Body.Len() != 0 {
		t.Errorf("a withdrawn check answered %q", rec.Body.String())
	}
}
