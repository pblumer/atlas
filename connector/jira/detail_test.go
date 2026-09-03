package jira_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/jira"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
)

// A field's JSON shape follows its FEEL value's kind, not the look of its text. Each
// kind is asserted here because each is a different member of the request body: a
// number sent as "3" and a number sent as 3 are different fields to Jira, and a
// summary that begins with "{" must stay a string.
func TestFieldsKeepTheirFeelShape(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:jiraConnector connector="acme" operation="create-issue" project="OPS" issueType="Task" summary="s">
			<atlas:jiraField name="story_points" value="=punkte"/>
			<atlas:jiraField name="flagged" value="=dringend"/>
			<atlas:jiraField name="components" value="=teile"/>
			<atlas:jiraField name="environment" value="=umgebung"/>
			<atlas:jiraField name="fehlt" value="=gibtEsNicht"/>
			<atlas:jiraField name="literal" value="{nicht json}"/>
			<atlas:jiraField name="trace" value="=processInstanceKey"/>
		 </atlas:jiraConnector>`,
		model.VariableValue{Name: "punkte", Kind: model.VarNumber, Text: "3"},
		model.VariableValue{Name: "dringend", Kind: model.VarBool, Bool: true},
		model.VariableValue{Name: "teile", Kind: model.VarJSON, Text: `[{"name":"api"}]`},
		model.VariableValue{Name: "umgebung", Kind: model.VarString, Text: "prod"},
	)
	client := &recordingClient{}
	reg := jira.NewRegistry()
	reg.Register("acme", client)
	if _, err := jira.Handler(rd, lookup, reg)(job.Job{Key: 4, ElementInstanceKey: 42}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	fields := client.reqs[0].Fields
	if got, ok := fields["story_points"].(json.Number); !ok || got.String() != "3" {
		t.Errorf("story_points = %#v, want the JSON number 3", fields["story_points"])
	}
	if fields["flagged"] != true {
		t.Errorf("flagged = %#v, want the boolean true", fields["flagged"])
	}
	if list, _ := fields["components"].([]any); len(list) != 1 {
		t.Errorf("components = %#v, want a one-element JSON list", fields["components"])
	}
	if fields["environment"] != "prod" {
		t.Errorf("environment = %#v, want the string prod", fields["environment"])
	}
	// An unbound name is FEEL null, which is a field explicitly cleared rather than a
	// field left out — Jira reads a null as "unset this".
	if fields["fehlt"] != nil {
		t.Errorf("fehlt = %#v, want null for a variable that is not set", fields["fehlt"])
	}
	if fields["literal"] != "{nicht json}" {
		t.Errorf("literal = %#v, want the literal text: a literal is never parsed", fields["literal"])
	}
	// The instance's own key is the back-reference that makes an issue traceable to
	// the process that opened it.
	if fields["trace"] != "500" {
		t.Errorf("trace = %#v, want the process instance key", fields["trace"])
	}
}

// A FEEL expression that cannot be evaluated resolves to empty rather than failing the
// job, matching the engine's null-propagating contract — the same rule the REST and
// SharePoint workers follow.
func TestUnevaluableExpressionResolvesEmpty(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:jiraConnector connector="acme" operation="create-issue" project="OPS" issueType="Task" summary="=zahl + 1">
			<atlas:jiraField name="labels" value="=zahl + 1"/>
		 </atlas:jiraConnector>`,
		model.VariableValue{Name: "zahl", Kind: model.VarString, Text: "nicht zahl"},
	)
	client := &recordingClient{}
	reg := jira.NewRegistry()
	reg.Register("acme", client)
	if _, err := jira.Handler(rd, lookup, reg)(job.Job{Key: 4, ElementInstanceKey: 42}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if got := client.reqs[0].Summary; got != "" {
		t.Errorf("summary = %q, want empty", got)
	}
	// A field resolves to null rather than to empty text: FEEL propagates null through
	// arithmetic it cannot perform, and for an issue field null is the value that
	// clears it — the same thing an unset variable means (see the shapes test above).
	if got := client.reqs[0].Fields["labels"]; got != nil {
		t.Errorf("labels = %#v, want null", got)
	}
}

// The result variable takes the kind of what Jira returned, so a scalar answer stays a
// scalar in the variable store rather than becoming a JSON string.
func TestResultVariableKinds(t *testing.T) {
	cases := []struct {
		name   string
		result any
		want   model.VarKind
	}{
		{"object", map[string]any{"key": "OPS-1"}, model.VarJSON},
		{"string", "OPS-1", model.VarString},
		{"number", json.Number("42"), model.VarNumber},
		{"bool", true, model.VarBool},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rd, lookup := workerFixture(t,
				`<atlas:jiraConnector connector="acme" operation="get-issue" issueKey="OPS-1" resultVariable="ticket"/>`)
			reg := jira.NewRegistry()
			reg.Register("acme", &recordingClient{result: tc.result})
			out, err := jira.Handler(rd, lookup, reg)(job.Job{Key: 1, ElementInstanceKey: 42})
			if err != nil {
				t.Fatalf("handler: %v", err)
			}
			if len(out) != 1 || out[0].Kind != tc.want {
				t.Fatalf("outputs = %+v, want one %v variable", out, tc.want)
			}
		})
	}
}

// A client error fails the job so the token stays put and the retry budget applies —
// completing it would drive the process on work that did not happen.
func TestHandlerPropagatesTheClientError(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:jiraConnector connector="acme" operation="get-issue" issueKey="OPS-1" resultVariable="t"/>`)
	reg := jira.NewRegistry()
	reg.Register("acme", &recordingClient{err: context.DeadlineExceeded})
	if _, err := jira.Handler(rd, lookup, reg)(job.Job{Key: 1, ElementInstanceKey: 42}); err == nil {
		t.Fatal("handler completed a job whose call failed")
	}
}

// A job whose process definition is gone cannot be resolved into a call, and saying so
// is better than calling Jira with whatever the zero value would produce.
func TestHandlerWithoutACompiledProcess(t *testing.T) {
	rd, _ := workerFixture(t,
		`<atlas:jiraConnector connector="acme" operation="get-issue" issueKey="OPS-1" resultVariable="t"/>`)
	h := jira.Handler(rd, func(uint64) *compiler.CompiledProcess { return nil }, jira.NewRegistry())
	if _, err := h(job.Job{Key: 1, ElementInstanceKey: 42}); err == nil {
		t.Fatal("handler accepted a job with no compiled process")
	}
}

// The element the job points at must be a worker task. A job type index and an
// element that disagree is a corrupted store, not something to call Jira about.
func TestHandlerOnANonConnectorElement(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:jiraConnector connector="acme" operation="get-issue" issueKey="OPS-1" resultVariable="t"/>`)
	rd.ei.ElementId = 0 // the start event
	if _, err := jira.Handler(rd, lookup, jira.NewRegistry())(job.Job{Key: 1, ElementInstanceKey: 42}); err == nil {
		t.Fatal("handler accepted a job on an element that is not a task")
	}
}

// A store that cannot be read fails the job rather than calling Jira with an empty
// scope, which would silently send a request with every FEEL value resolved to nothing.
func TestHandlerWhenTheStoreFails(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:jiraConnector connector="acme" operation="get-issue" issueKey="=schluessel" resultVariable="t"/>`)
	failing := &erroringReader{fakeReader: rd}
	reg := jira.NewRegistry()
	reg.Register("acme", &recordingClient{})
	if _, err := jira.Handler(failing, lookup, reg)(job.Job{Key: 1, ElementInstanceKey: 42}); err == nil {
		t.Fatal("handler accepted a job whose variables could not be read")
	}
}

// erroringReader answers the element instance and then fails the variable read.
type erroringReader struct{ *fakeReader }

func (e *erroringReader) VariablesOfScope(uint64, func(*model.VariableValue) error) error {
	return context.DeadlineExceeded
}

// A worker with no base URL is refused before a request is built, so the failure
// names the configuration rather than arriving as a malformed URL.
func TestCallWithoutABaseURL(t *testing.T) {
	c := jira.NewHTTPClient(jira.Connector{Token: "pat"})
	if _, err := c.Do(context.Background(), jira.Request{Operation: "get-issue", Issue: "OPS-1"}); err == nil {
		t.Fatal("a client with no base URL performed a call")
	}
}

// A host that is not there fails the job; the message names the operation, because
// "connection refused" alone does not say which step of the process stopped.
func TestTransportErrorNamesTheOperation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	base := srv.URL
	srv.Close() // nothing is listening now
	_, err := jira.NewHTTPClient(jira.Connector{BaseURL: base, Token: "pat"}).
		Do(context.Background(), jira.Request{Operation: "get-issue", Issue: "OPS-1"})
	if err == nil || !strings.Contains(err.Error(), "get-issue") {
		t.Fatalf("error = %v, want it to name the operation", err)
	}
}

// A rejection that is not Jira's error envelope — a proxy's HTML page, most often —
// is surfaced as it arrived rather than swallowed: it is itself the answer to why the
// call failed.
func TestErrorFallsBackToTheRawBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>502 Bad Gateway</html>"))
	}))
	t.Cleanup(srv.Close)
	_, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{Operation: "get-issue", Issue: "OPS-1"})
	if err == nil || !strings.Contains(err.Error(), "Bad Gateway") {
		t.Fatalf("error = %v, want the raw body in it", err)
	}
}

// Jira's per-field errors are the useful half of a rejected create, and they arrive in
// a map — so they are rendered in a stable order, or two incidents on the same fault
// would not compare.
func TestErrorRendersFieldErrorsInAStableOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errorMessages":[],"errors":{"summary":"is required","project":"is invalid","issuetype":"is invalid"}}`))
	}))
	t.Cleanup(srv.Close)
	var first string
	for i := 0; i < 5; i++ {
		_, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{Operation: "create-issue", Project: "X", IssueType: "T", Summary: "s"})
		if err == nil {
			t.Fatal("a rejected create was accepted")
		}
		if i == 0 {
			first = err.Error()
			continue
		}
		if err.Error() != first {
			t.Fatalf("error text differs between runs:\n%s\n%s", first, err.Error())
		}
	}
	if !strings.Contains(first, "issuetype: is invalid; project: is invalid; summary: is required") {
		t.Errorf("error = %q, want the field errors sorted", first)
	}
}

// A truncated body still says something: an error page long enough to bury the message
// is cut rather than dropped.
func TestLongErrorBodyIsTruncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(strings.Repeat("x", 900)))
	}))
	t.Cleanup(srv.Close)
	_, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{Operation: "get-issue", Issue: "OPS-1"})
	if err == nil || !strings.Contains(err.Error(), "…") {
		t.Fatalf("error = %v, want a truncated body", err)
	}
}

// A response body that is not JSON at all fails the job rather than writing something
// unusable into a process variable.
func TestUndecodableResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	t.Cleanup(srv.Close)
	if _, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{Operation: "get-issue", Issue: "OPS-1"}); err == nil {
		t.Fatal("an undecodable response was accepted")
	}
}

// A transition carrying extra field values sends them alongside the move — a Jira
// workflow screen can require a field on the transition itself.
func TestTransitionCarriesFields(t *testing.T) {
	f, srv := newFakeJira(t)
	if _, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{
		Operation: "transition-issue", Issue: "OPS-42", Transition: "31",
		Fields: map[string]any{"resolution": map[string]any{"name": "Done"}},
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	fields, _ := f.only(t).body["fields"].(map[string]any)
	if r, _ := fields["resolution"].(map[string]any); r == nil || r["name"] != "Done" {
		t.Errorf("fields = %+v, want the transition-screen field", f.only(t).body["fields"])
	}
}

// An update that changes only the description sends only that.
func TestUpdateDescriptionOnly(t *testing.T) {
	f, srv := newFakeJira(t)
	if _, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{
		Operation: "update-issue", Issue: "OPS-42", Description: "mehr Kontext",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	fields, _ := f.only(t).body["fields"].(map[string]any)
	if len(fields) != 1 || fields["description"] != "mehr Kontext" {
		t.Errorf("fields = %+v, want the description alone", fields)
	}
}

// A search whose first page comes back empty stops there. Trusting the reported total
// instead is how a query whose matches shrink mid-read loops forever.
func TestSearchStopsOnAnEmptyPage(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"startAt":0,"total":99,"issues":[]}`))
	}))
	t.Cleanup(srv.Close)
	got, err := dcClient(srv.URL).Do(context.Background(), jira.Request{Operation: "search", JQL: "project = OPS"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if issues, _ := got.([]any); len(issues) != 0 {
		t.Errorf("issues = %+v, want none", got)
	}
	if calls != 1 {
		t.Errorf("requests = %d, want one", calls)
	}
}

// A response without a total is one page and no way to ask for another, so the search
// ends there rather than paging blind.
func TestSearchStopsWithoutATotal(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"issues":[{"key":"OPS-1"}]}`))
	}))
	t.Cleanup(srv.Close)
	got, err := dcClient(srv.URL).Do(context.Background(), jira.Request{Operation: "search", JQL: "project = OPS"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if issues, _ := got.([]any); len(issues) != 1 || calls != 1 {
		t.Errorf("issues = %+v after %d requests, want one of each", got, calls)
	}
}

// A failing page fails the whole search: half a result set is a wrong answer, not a
// partial one.
func TestSearchFailsOnARejectedPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errorMessages":["Field 'nope' does not exist."]}`))
	}))
	t.Cleanup(srv.Close)
	if _, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{Operation: "search", JQL: "nope = 1"}); err == nil {
		t.Fatal("a rejected search was accepted")
	}
}

// A transition lookup that fails does not fall through to a move with an empty id.
func TestTransitionLookupFailure(t *testing.T) {
	f, srv := newFakeJira(t)
	f.status["GET /rest/api/2/issue/OPS-42/transitions"] = http.StatusNotFound
	_, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{
		Operation: "transition-issue", Issue: "OPS-42", Transition: "Fertig",
	})
	if err == nil {
		t.Fatal("a failed transition lookup was accepted")
	}
	if len(f.calls) != 1 {
		t.Errorf("calls = %+v, want the lookup alone", f.calls)
	}
}

// Jira returns a transition id as a string, but a Data Center response can carry it as
// a JSON number — both address the same transition.
func TestTransitionIDAsANumber(t *testing.T) {
	f, srv := newFakeJira(t)
	f.answers["GET /rest/api/2/issue/OPS-42/transitions"] = map[string]any{
		"transitions": []any{
			map[string]any{"name": ""}, // an entry with no name is skipped, not matched
			map[string]any{"id": 31, "name": "Fertig"},
		},
	}
	if _, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{
		Operation: "transition-issue", Issue: "OPS-42", Transition: "Fertig",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	tr, _ := f.calls[1].body["transition"].(map[string]any)
	if tr == nil || tr["id"] != "31" {
		t.Errorf("transition = %+v, want the numeric id as a string", f.calls[1].body["transition"])
	}
}

// A base URL that cannot form a request fails before anything is sent, naming the
// operation like every other failure of that call.
func TestUnbuildableRequest(t *testing.T) {
	c := jira.NewHTTPClient(jira.Connector{BaseURL: "http://a\x7fb", Token: "pat"})
	if _, err := c.Do(context.Background(), jira.Request{Operation: "get-issue", Issue: "OPS-1"}); err == nil {
		t.Fatal("a request that cannot be built was sent")
	}
}

// A variable holding nothing binds as FEEL null, and a result Jira did not return
// writes nothing — the two null-shaped ends of the same round trip.
func TestNullVariableBindsAsNull(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:jiraConnector connector="acme" operation="create-issue" project="OPS" issueType="Task" summary="=leer" resultVariable="ticket"/>`,
		model.VariableValue{Name: "leer", Kind: model.VarNull},
	)
	client := &recordingClient{} // returns no result: Jira answered with nothing
	reg := jira.NewRegistry()
	reg.Register("acme", client)
	out, err := jira.Handler(rd, lookup, reg)(job.Job{Key: 1, ElementInstanceKey: 42})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if client.reqs[0].Summary != "" {
		t.Errorf("summary = %q, want empty for a null variable", client.reqs[0].Summary)
	}
	if len(out) != 0 {
		t.Errorf("outputs = %+v, want none: there was nothing to write", out)
	}
}

// The job key travels as X-Request-ID so an at-least-once replay is recognizable
// downstream.
func TestRequestIDHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("X-Request-ID")
		_, _ = w.Write([]byte(`{"key":"OPS-1"}`))
	}))
	t.Cleanup(srv.Close)
	if _, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{
		Operation: "get-issue", Issue: "OPS-1", RequestID: "4711",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got != "4711" {
		t.Errorf("X-Request-ID = %q, want the job key", got)
	}
}

// A page token that does not advance cannot describe a *next* page, so the read ends
// there. Without the guard a server answering with the same token forever would page
// forever — the failure the offset path used the reported total to avoid, and which
// the token-paged endpoint has no total to protect against.
func TestSearchOnCloudStopsOnARepeatedToken(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls > 10 {
			t.Fatal("the search paged past a token that never advanced")
		}
		_, _ = w.Write([]byte(`{"issues":[{"key":"OPS-1"}],"nextPageToken":"stuck"}`))
	}))
	t.Cleanup(srv.Close)

	got, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{Operation: "search", JQL: "project = OPS"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if issues, _ := got.([]any); len(issues) != 2 {
		t.Errorf("issues = %+v, want the two pages read before the token repeated", got)
	}
	if calls != 2 {
		t.Errorf("requests = %d, want the read to stop once the token repeated", calls)
	}
}

// Resolve refuses a task with no detail rather than producing a Job with an empty
// operation, which the client would then reject with a message about the operation
// instead of about the model.
func TestResolveRefusesATaskWithNoDetail(t *testing.T) {
	if _, err := jira.Resolve(nil, nil, nil, nil, 1, 2); err == nil {
		t.Fatal("a task with no detail was resolved")
	}
}

// Whitespace is never part of a name in Jira, and Jira compares names exactly: a
// project key that arrives as "OPS " is refused with a message about the project not
// existing, which sends an operator looking in Jira for a fault that is a stray space
// in a form field. Every value that *names* something is trimmed on the way out.
func TestIdentifiersAreTrimmedBeforeTheyReachJira(t *testing.T) {
	f, srv := newFakeJira(t)
	f.answers["POST /rest/api/2/issue"] = map[string]any{"id": "10001", "key": "OPS-42"}
	if _, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{
		Operation: "create-issue", Project: "  OPS\t", IssueType: " Task ",
		Summary: "  ein Titel mit Rand  ",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	fields, _ := f.only(t).body["fields"].(map[string]any)
	project, _ := fields["project"].(map[string]any)
	issuetype, _ := fields["issuetype"].(map[string]any)
	if project["key"] != "OPS" {
		t.Errorf("project = %+v, want the key trimmed to OPS", fields["project"])
	}
	if issuetype["name"] != "Task" {
		t.Errorf("issuetype = %+v, want the name trimmed to Task", fields["issuetype"])
	}
	// Content is not identifiers: what a model composed is sent as composed.
	if fields["summary"] != "  ein Titel mit Rand  " {
		t.Errorf("summary = %q, want it sent exactly as authored", fields["summary"])
	}
}

// A padded project key that is all digits is still read as an id rather than a key —
// the trim happens before the digits test, so " 10000 " addresses project 10000 and not
// a project whose key is a number with spaces around it.
func TestATrimmedNumericProjectIsStillAnID(t *testing.T) {
	f, srv := newFakeJira(t)
	f.answers["POST /rest/api/2/issue"] = map[string]any{"id": "1", "key": "OPS-1"}
	if _, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{
		Operation: "create-issue", Project: " 10000 ", IssueType: "Task", Summary: "x",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	fields, _ := f.only(t).body["fields"].(map[string]any)
	project, _ := fields["project"].(map[string]any)
	if project["id"] != "10000" {
		t.Errorf("project = %+v, want it addressed by id 10000", fields["project"])
	}
}

// An issue key goes into the request path, where a stray space would be percent-encoded
// into a URL naming an issue that cannot exist.
func TestAPaddedIssueKeyDoesNotReachTheURL(t *testing.T) {
	f, srv := newFakeJira(t)
	f.answers["GET /rest/api/2/issue/OPS-42"] = map[string]any{"key": "OPS-42"}
	if _, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{
		Operation: "get-issue", Issue: " OPS-42 ",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got := f.only(t).path; got != "/rest/api/2/issue/OPS-42" {
		t.Errorf("path = %q, want the key trimmed before it is escaped into the URL", got)
	}
}

// An account search carries its term through the whole resolve: the model's FEEL
// expression is evaluated against the variables the task sees, and what reaches the
// client is the address the process actually holds. The resolved job is also what an
// offloaded task sends over the wire, so a term that did not survive here would leave a
// worker searching for the empty string — which Jira answers with accounts, not with an
// error.
func TestSearchUsersResolvesItsTerm(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:jiraConnector connector="acme" operation="search-users" query="=antragsteller.mail"
		                     project="OPS" resultVariable="konten"/>`,
		model.VariableValue{Name: "antragsteller", Kind: model.VarJSON, Text: `{"mail":"patrick@blumer.net"}`},
	)
	client := &recordingClient{}
	reg := jira.NewRegistry()
	reg.Register("acme", client)
	if _, err := jira.Handler(rd, lookup, reg)(job.Job{Key: 4, ElementInstanceKey: 42}); err != nil {
		t.Fatalf("handler: %v", err)
	}
	req := client.reqs[0]
	if req.Operation != "search-users" {
		t.Fatalf("operation = %q, want search-users", req.Operation)
	}
	if req.Query != "patrick@blumer.net" {
		t.Errorf("query = %q, want the evaluated address", req.Query)
	}
	if req.Project != "OPS" {
		t.Errorf("project = %q, want the project the search is restricted to", req.Project)
	}
}
