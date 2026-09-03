package openapimock_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/atlas/connector/rest/openapimock"
)

// serve builds a mock over the petstore fixture and returns it with its handler.
func serve(t *testing.T, opts ...openapimock.Option) (*openapimock.Server, http.Handler) {
	t.Helper()
	srv := openapimock.New(loadPetstore(t), opts...)
	return srv, srv.Handler()
}

// do issues one request against a handler and returns the recorder.
func do(h http.Handler, method, path string, body string, headers map[string]string) *httptest.ResponseRecorder {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestServesTheSpecsExample(t *testing.T) {
	_, h := serve(t)
	w := do(h, "GET", "/v1/pets/7", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.Contains(w.Body.String(), "Fido") {
		t.Errorf("body = %s, want the first named example", w.Body)
	}
}

func TestALiteralPathBeatsAParameter(t *testing.T) {
	_, h := serve(t)
	if w := do(h, "GET", "/v1/pets/mine", "", nil); w.Code != http.StatusOK ||
		!strings.Contains(w.Body.String(), "[") {
		t.Errorf("GET /v1/pets/mine = %d %s, want the myPets list", w.Code, w.Body)
	}
}

func TestPicksTheLowestSuccessStatusAndItsHeaders(t *testing.T) {
	_, h := serve(t)
	w := do(h, "POST", "/v1/pets", `{"name":"Fido"}`, nil)
	// createPet declares 201 and 400; a mock answers the success it was asked for.
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/v1/pets/1" {
		t.Errorf("Location = %q, want the header example", loc)
	}
}

func TestAResponseWithNoContentHasNoBody(t *testing.T) {
	_, h := serve(t)
	w := do(h, "DELETE", "/v1/pets/7", "", nil)
	if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Errorf("DELETE = %d %q, want 204 and nothing", w.Code, w.Body)
	}
}

func TestTheDefaultResponseAnswers200(t *testing.T) {
	spec, err := openapimock.LoadFile(filepath.Join("testdata", "tickets.json"))
	if err != nil {
		t.Fatal(err)
	}
	h := openapimock.New(spec).Handler()
	w := do(h, "POST", "/tickets", "{}", nil)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for a `default` response", w.Code)
	}
}

func TestPreferChoosesTheStatusAndTheExample(t *testing.T) {
	_, h := serve(t)
	w := do(h, "GET", "/v1/pets/7", "", map[string]string{"Prefer": "code=404"})
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "no such pet") {
		t.Errorf("Prefer code=404 = %d %s", w.Code, w.Body)
	}
	w = do(h, "GET", "/v1/pets/7", "", map[string]string{"Prefer": "example=rex"})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Rex") {
		t.Errorf("Prefer example=rex = %d %s", w.Code, w.Body)
	}
	// Both at once, and with the whitespace RFC 7240 allows.
	w = do(h, "GET", "/v1/pets/7", "", map[string]string{"Prefer": " code=200 , example=rex "})
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Rex") {
		t.Errorf("Prefer code=200, example=rex = %d %s", w.Code, w.Body)
	}
}

func TestPreferRefusesWhatTheSpecDoesNotOffer(t *testing.T) {
	_, h := serve(t)
	// Ignoring an unserveable Prefer (what RFC 7240 allows) would answer 200 to a test
	// written to exercise the 418 path, and the model would be judged on it.
	w := do(h, "GET", "/v1/pets/7", "", map[string]string{"Prefer": "code=418"})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "404") {
		t.Errorf("Prefer code=418 = %d %s, want a refusal naming the statuses on offer", w.Code, w.Body)
	}
	w = do(h, "GET", "/v1/pets/7", "", map[string]string{"Prefer": "example=nope"})
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "rex") {
		t.Errorf("Prefer example=nope = %d %s, want a refusal naming the examples", w.Code, w.Body)
	}
	w = do(h, "GET", "/v1/pets/7", "", map[string]string{"Prefer": "code=abc"})
	if w.Code != http.StatusBadRequest {
		t.Errorf("Prefer code=abc = %d, want 400", w.Code)
	}
	// A preference this mock knows nothing about is ignored, as the RFC says.
	if w := do(h, "GET", "/v1/pets/7", "", map[string]string{"Prefer": "wait=10"}); w.Code != http.StatusOK {
		t.Errorf("Prefer wait=10 = %d, want it ignored", w.Code)
	}
}

func TestAnUnknownMethodOnAKnownPathIs405(t *testing.T) {
	_, h := serve(t)
	w := do(h, "PUT", "/v1/pets/7", "{}", nil)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
	if allow := w.Header().Get("Allow"); allow != "DELETE, GET" {
		t.Errorf("Allow = %q, want DELETE, GET", allow)
	}
}

func TestAnUnknownPathIs404WithAReason(t *testing.T) {
	_, h := serve(t)
	w := do(h, "GET", "/v1/nope", "", nil)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "no operation") {
		t.Errorf("= %d %s", w.Code, w.Body)
	}
	// The base path is the mistake people actually make, so the 404 names it.
	w = do(h, "GET", "/pets/7", "", nil)
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "/v1") {
		t.Errorf("= %d %s, want the base path in the message", w.Code, w.Body)
	}
}

func TestAcceptIsHonoredAndRefusedWhenImpossible(t *testing.T) {
	_, h := serve(t)
	if w := do(h, "GET", "/v1/pets/7", "", map[string]string{"Accept": "application/*"}); w.Code != http.StatusOK {
		t.Errorf("Accept application/* = %d", w.Code)
	}
	w := do(h, "GET", "/v1/pets/7", "", map[string]string{"Accept": "text/plain"})
	if w.Code != http.StatusNotAcceptable || !strings.Contains(w.Body.String(), "application/json") {
		t.Errorf("Accept text/plain = %d %s, want 406 naming what it has", w.Code, w.Body)
	}
	// A media type that is not JSON is served as written.
	w = do(h, "GET", "/v1/reports/2026-09.csv", "", map[string]string{"Accept": "text/csv"})
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Body.String(), "pets,adoptions") {
		t.Errorf("csv report = %d %q", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "text/csv" {
		t.Errorf("content-type = %q", ct)
	}
}

func TestTheJournalRecordsWhatTheCallerSent(t *testing.T) {
	srv, h := serve(t)
	do(h, "POST", "/v1/pets", `{"name":"Fido"}`, map[string]string{"X-Request-ID": "job-42"})
	do(h, "GET", "/v1/nope", "", nil)
	do(h, "GET", "/v1/pets?limit=2", "", nil)
	calls := srv.Calls()
	if len(calls) != 3 {
		t.Fatalf("got %d calls, want 3", len(calls))
	}
	first := calls[0]
	if first.Seq != 1 || first.Method != "POST" || first.Path != "/v1/pets" ||
		first.Operation != "createPet" || first.Status != 201 || first.RequestID != "job-42" {
		t.Errorf("first call = %+v", first)
	}
	if string(first.Body) != `{"name":"Fido"}` {
		t.Errorf("recorded body = %s", first.Body)
	}
	// An unmatched call is recorded too — "nothing happened" is the report an operator
	// most needs when a task's URL is one character off.
	if unmatched := calls[1]; unmatched.Status != 404 || unmatched.Operation != "" {
		t.Errorf("unmatched call = %+v", unmatched)
	}
	// A query string is part of what the caller sent, so it is part of the record.
	if got := calls[2].Path; got != "/v1/pets?limit=2" {
		t.Errorf("recorded path = %q, want the query kept", got)
	}
}

func TestTheJournalIsBounded(t *testing.T) {
	srv, h := serve(t)
	for i := 0; i < 205; i++ {
		do(h, "GET", "/v1/pets/7", "", nil)
	}
	calls := srv.Calls()
	if len(calls) != 200 {
		t.Fatalf("got %d calls, want the journal bounded at 200", len(calls))
	}
	// The newest are kept, and the sequence numbers say what was dropped.
	if calls[0].Seq != 6 || calls[199].Seq != 205 {
		t.Errorf("seq range = %d..%d, want 6..205", calls[0].Seq, calls[199].Seq)
	}
}

func TestTheInspectionEndpointsServeTheJournalAndTheReport(t *testing.T) {
	_, h := serve(t, openapimock.WithID("mock-1"))
	do(h, "GET", "/v1/pets/7", "", nil)

	w := do(h, "GET", "/__mock/calls", "", nil)
	var calls []openapimock.Call
	if err := json.Unmarshal(w.Body.Bytes(), &calls); err != nil {
		t.Fatalf("calls: %v (%s)", err, w.Body)
	}
	if len(calls) != 1 || calls[0].Operation != "getPet" {
		t.Errorf("calls = %+v", calls)
	}

	w = do(h, "GET", "/__mock/report", "", nil)
	var report openapimock.Report
	if err := json.Unmarshal(w.Body.Bytes(), &report); err != nil {
		t.Fatalf("report: %v (%s)", err, w.Body)
	}
	// The envelope is the one the Console's Mockups view takes: the server understands
	// kind/source/target/summary and nothing about the payload.
	if report.Kind != "openapi" || report.Source != "mock-1" || report.Target != "Petstore 1.4.2" {
		t.Errorf("report envelope = %+v", report)
	}
	if !strings.Contains(report.Summary, "6 operations") || !strings.Contains(report.Summary, "1 call") {
		t.Errorf("summary = %q", report.Summary)
	}
	var data struct {
		Title      string `json:"title"`
		BasePath   string `json:"basePath"`
		Operations []struct {
			Method, Path, OperationID string
			Calls                     int
		} `json:"operations"`
		Calls []openapimock.Call `json:"calls"`
	}
	if err := json.Unmarshal(report.Data, &data); err != nil {
		t.Fatalf("report data: %v", err)
	}
	if data.Title != "Petstore" || data.BasePath != "/v1" || len(data.Operations) != 6 || len(data.Calls) != 1 {
		t.Errorf("report data = %+v", data)
	}
	var served int
	for _, op := range data.Operations {
		served += op.Calls
	}
	if served != 1 {
		t.Errorf("operation call counts sum to %d, want 1", served)
	}
	// The inspection endpoints are not part of what is mocked, so they stay out of it.
	if len(calls) != 1 {
		t.Errorf("the journal recorded its own inspection endpoint: %+v", calls)
	}
	if w := do(h, "GET", "/__mock/nope", "", nil); w.Code != http.StatusNotFound {
		t.Errorf("unknown inspection endpoint = %d", w.Code)
	}
}

func TestTheBasePathCanBeOverridden(t *testing.T) {
	_, h := serve(t, openapimock.WithBasePath("/"))
	if w := do(h, "GET", "/pets/7", "", nil); w.Code != http.StatusOK {
		t.Errorf("with the base path stripped, GET /pets/7 = %d", w.Code)
	}

	srv, h := serve(t, openapimock.WithBasePath("/api/"))
	if w := do(h, "GET", "/api/pets/7", "", nil); w.Code != http.StatusOK {
		t.Errorf("with an overridden base path, GET /api/pets/7 = %d", w.Code)
	}
	// The prefix is readable back, because a banner has to print the URLs a caller
	// actually reaches rather than the ones the document states.
	if got := srv.BasePath(); got != "/api" {
		t.Errorf("BasePath() = %q, want /api", got)
	}
}

func TestATrailingSlashIsTheSamePath(t *testing.T) {
	_, h := serve(t)
	if w := do(h, "GET", "/v1/pets/", "", nil); w.Code != http.StatusOK {
		t.Errorf("GET /v1/pets/ = %d, want the same as /v1/pets", w.Code)
	}
}

func TestTheLogNamesEveryCall(t *testing.T) {
	var buf bytes.Buffer
	_, h := serve(t, openapimock.WithLog(&buf))
	do(h, "GET", "/v1/pets/7", "", nil)
	do(h, "GET", "/v1/nope", "", nil)
	out := buf.String()
	if !strings.Contains(out, "GET /v1/pets/7 → 200 getPet") {
		t.Errorf("log = %q, want the served operation", out)
	}
	if !strings.Contains(out, "GET /v1/nope → 404") {
		t.Errorf("log = %q, want the unmatched call", out)
	}
}

func TestConcurrentCallsShareOneJournal(t *testing.T) {
	srv, h := serve(t)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			do(h, "GET", "/v1/pets/7", "", nil)
			srv.Report()
		}()
	}
	wg.Wait()
	if got := len(srv.Calls()); got != 16 {
		t.Errorf("got %d calls, want 16", got)
	}
}

func TestABareBasePathReachesTheRootOperation(t *testing.T) {
	spec, err := openapimock.Load([]byte("openapi: 3.0.0\nservers: [{url: 'https://api.example.com/v1'}]\npaths:\n  /:\n    get:\n      responses:\n        '200': {description: ok}\n"))
	if err != nil {
		t.Fatal(err)
	}
	h := openapimock.New(spec).Handler()
	if w := do(h, "GET", "/v1", "", nil); w.Code != http.StatusOK {
		t.Errorf("GET /v1 = %d, want the root operation", w.Code)
	}
}

func TestAnExplicitWildcardAcceptTakesThePreferredType(t *testing.T) {
	_, h := serve(t)
	w := do(h, "GET", "/v1/pets/7", "", map[string]string{"Accept": "*/*"})
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("= %d %q", w.Code, w.Header().Get("Content-Type"))
	}
}

func TestAnOperationWithoutAnIDIsNamedByItsRoute(t *testing.T) {
	spec, err := openapimock.Load([]byte("openapi: 3.0.0\npaths:\n  /pets/{id}:\n    get:\n      responses:\n        '200': {description: ok}\n"))
	if err != nil {
		t.Fatal(err)
	}
	srv := openapimock.New(spec)
	do(srv.Handler(), "GET", "/pets/7", "", nil)
	if got := srv.Calls()[0].Operation; got != "GET /pets/{id}" {
		t.Errorf("operation = %q, want the route", got)
	}
}

func TestAnUnserveableMediaTypeIsRefusedRatherThanMislabelled(t *testing.T) {
	// Found against Swagger's own Petstore: the mock answered Accept: application/xml
	// with JSON bytes under an XML content type.
	spec, err := openapimock.Load([]byte(`
openapi: 3.0.0
info: {title: Petstore, version: '1'}
paths:
  /pet/{id}:
    get:
      responses:
        '200':
          description: a pet
          content:
            application/json: {schema: {type: object, properties: {id: {type: integer}}}}
            application/xml: {schema: {type: object, properties: {id: {type: integer}}}}
`))
	if err != nil {
		t.Fatal(err)
	}
	h := openapimock.New(spec).Handler()
	w := do(h, "GET", "/pet/7", "", map[string]string{"Accept": "application/xml"})
	if w.Code != http.StatusNotAcceptable {
		t.Errorf("Accept: application/xml = %d %s, want 406", w.Code, w.Body)
	}
	if w := do(h, "GET", "/pet/7", "", nil); w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("content-type = %q", w.Header().Get("Content-Type"))
	}
}
