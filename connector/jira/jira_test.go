package jira_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/connector/jira"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/model"
)

// call is one request the fake Jira recorded.
type call struct {
	method string
	path   string
	query  string
	auth   string
	body   map[string]any
}

// fakeJira stands in for a Jira instance: it records every request and answers from
// a per-path table, so a test states the API shape it expects rather than a live
// server's behaviour.
type fakeJira struct {
	t       *testing.T
	calls   []call
	answers map[string]any // "METHOD /path" → response body (nil → 204 No Content)
	status  map[string]int
}

func newFakeJira(t *testing.T) (*fakeJira, *httptest.Server) {
	f := &fakeJira{t: t, answers: map[string]any{}, status: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := call{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, auth: r.Header.Get("Authorization")}
		if r.Body != nil {
			var body map[string]any
			dec := json.NewDecoder(r.Body)
			dec.UseNumber()
			_ = dec.Decode(&body)
			c.body = body
		}
		f.calls = append(f.calls, c)
		key := r.Method + " " + r.URL.Path
		if code, ok := f.status[key]; ok {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"errorMessages":["Field 'summary' is required."],"errors":{}}`))
			return
		}
		ans, ok := f.answers[key]
		if !ok || ans == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ans)
	}))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeJira) only(t *testing.T) call {
	t.Helper()
	if len(f.calls) != 1 {
		t.Fatalf("calls = %d, want exactly 1: %+v", len(f.calls), f.calls)
	}
	return f.calls[0]
}

func cloudClient(base string) *jira.HTTPClient {
	return jira.NewHTTPClient(jira.Connector{BaseURL: base, Email: "bot@acme.example", APIToken: "t0ken"})
}

// dcClient is a Data Center connector: a bearer personal access token rather than a
// Cloud {email, apiToken} bundle. The distinction decides how an assignee is addressed
// and — since the Cloud search deprecation — which search endpoint is used.
func dcClient(base string) *jira.HTTPClient {
	return jira.NewHTTPClient(jira.Connector{BaseURL: base, Token: "pat"})
}

// Creating an issue POSTs Jira's nested field shape: the project and the issue type
// wrapped in an object each, the summary and description plain, and the extra fields
// merged in beside them with the JSON shape their FEEL value had.
func TestCreateIssue(t *testing.T) {
	f, srv := newFakeJira(t)
	f.answers["POST /rest/api/2/issue"] = map[string]any{"id": "10001", "key": "OPS-42"}
	got, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{
		Operation: "create-issue", Project: "OPS", IssueType: "Task",
		Summary: "Neuer Zugang", Description: "Bitte anlegen",
		Fields:    map[string]any{"labels": []any{"atlas"}, "priority": map[string]any{"name": "High"}},
		RequestID: "77",
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	c := f.only(t)
	if c.method != http.MethodPost || c.path != "/rest/api/2/issue" {
		t.Errorf("request = %s %s, want POST /rest/api/2/issue", c.method, c.path)
	}
	fields, _ := c.body["fields"].(map[string]any)
	if fields == nil {
		t.Fatalf("body = %+v, want a fields object", c.body)
	}
	if p, _ := fields["project"].(map[string]any); p == nil || p["key"] != "OPS" {
		t.Errorf("project = %+v, want {\"key\":\"OPS\"}", fields["project"])
	}
	if it, _ := fields["issuetype"].(map[string]any); it == nil || it["name"] != "Task" {
		t.Errorf("issuetype = %+v, want {\"name\":\"Task\"}", fields["issuetype"])
	}
	if fields["summary"] != "Neuer Zugang" || fields["description"] != "Bitte anlegen" {
		t.Errorf("summary/description = %v / %v", fields["summary"], fields["description"])
	}
	if labels, _ := fields["labels"].([]any); len(labels) != 1 || labels[0] != "atlas" {
		t.Errorf("labels = %+v, want the JSON list the FEEL value produced", fields["labels"])
	}
	if pr, _ := fields["priority"].(map[string]any); pr == nil || pr["name"] != "High" {
		t.Errorf("priority = %+v, want the JSON object the FEEL value produced", fields["priority"])
	}
	if m, _ := got.(map[string]any); m == nil || m["key"] != "OPS-42" {
		t.Errorf("result = %+v, want the created issue Jira returned", got)
	}
}

// An issue type given as a number is an issue type *id*, which Jira addresses under a
// different member than a name. The same holds for a project: a numeric value is an id.
func TestCreateIssueByIDs(t *testing.T) {
	f, srv := newFakeJira(t)
	f.answers["POST /rest/api/2/issue"] = map[string]any{"key": "OPS-1"}
	if _, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{
		Operation: "create-issue", Project: "10000", IssueType: "10002", Summary: "s",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	fields, _ := f.only(t).body["fields"].(map[string]any)
	if p, _ := fields["project"].(map[string]any); p == nil || p["id"] != "10000" {
		t.Errorf("project = %+v, want it addressed by id", fields["project"])
	}
	if it, _ := fields["issuetype"].(map[string]any); it == nil || it["id"] != "10002" {
		t.Errorf("issuetype = %+v, want it addressed by id", fields["issuetype"])
	}
}

func TestGetIssue(t *testing.T) {
	f, srv := newFakeJira(t)
	f.answers["GET /rest/api/2/issue/OPS-42"] = map[string]any{"key": "OPS-42", "fields": map[string]any{"summary": "Zugang"}}
	got, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{Operation: "get-issue", Issue: "OPS-42"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if c := f.only(t); c.method != http.MethodGet {
		t.Errorf("method = %s, want GET", c.method)
	}
	if m, _ := got.(map[string]any); m == nil || m["key"] != "OPS-42" {
		t.Errorf("result = %+v, want the issue", got)
	}
}

// Updating an issue PUTs only what the model changed, and Jira answers 204 — so the
// operation returns nothing rather than inventing a result.
func TestUpdateIssue(t *testing.T) {
	f, srv := newFakeJira(t)
	got, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{
		Operation: "update-issue", Issue: "OPS-42", Summary: "Anders",
		Fields: map[string]any{"customfield_10010": "x"},
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got != nil {
		t.Errorf("result = %+v, want nothing for a 204", got)
	}
	c := f.only(t)
	if c.method != http.MethodPut || c.path != "/rest/api/2/issue/OPS-42" {
		t.Errorf("request = %s %s, want PUT /rest/api/2/issue/OPS-42", c.method, c.path)
	}
	fields, _ := c.body["fields"].(map[string]any)
	if fields["summary"] != "Anders" || fields["customfield_10010"] != "x" {
		t.Errorf("fields = %+v, want the changed summary and custom field", fields)
	}
	if _, ok := fields["description"]; ok {
		t.Errorf("fields = %+v, want no description: the model did not change one", fields)
	}
}

// A transition authored as a number is a transition id and goes straight through. A
// comment alongside it rides in Jira's update block, which is how one call both moves
// the issue and says why.
func TestTransitionByID(t *testing.T) {
	f, srv := newFakeJira(t)
	if _, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{
		Operation: "transition-issue", Issue: "OPS-42", Transition: "31", Comment: "erledigt",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	c := f.only(t)
	if c.method != http.MethodPost || c.path != "/rest/api/2/issue/OPS-42/transitions" {
		t.Errorf("request = %s %s, want the transitions sub-resource", c.method, c.path)
	}
	tr, _ := c.body["transition"].(map[string]any)
	if tr == nil || tr["id"] != "31" {
		t.Errorf("transition = %+v, want {\"id\":\"31\"}", c.body["transition"])
	}
	upd, _ := c.body["update"].(map[string]any)
	adds, _ := upd["comment"].([]any)
	if len(adds) != 1 {
		t.Fatalf("update = %+v, want one comment add", upd)
	}
	add, _ := adds[0].(map[string]any)
	body, _ := add["add"].(map[string]any)
	if body == nil || body["body"] != "erledigt" {
		t.Errorf("comment add = %+v, want the authored body", adds[0])
	}
}

// A transition authored by the name a person reads in Jira is looked up first: Jira's
// API only moves an issue by transition id, and making a model carry the id would tie
// it to one workflow configuration.
func TestTransitionByName(t *testing.T) {
	f, srv := newFakeJira(t)
	f.answers["GET /rest/api/2/issue/OPS-42/transitions"] = map[string]any{
		"transitions": []any{
			map[string]any{"id": "11", "name": "In Arbeit"},
			map[string]any{"id": "31", "name": "Fertig"},
		},
	}
	if _, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{
		Operation: "transition-issue", Issue: "OPS-42", Transition: "fertig",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if len(f.calls) != 2 {
		t.Fatalf("calls = %+v, want a lookup then the transition", f.calls)
	}
	tr, _ := f.calls[1].body["transition"].(map[string]any)
	if tr == nil || tr["id"] != "31" {
		t.Errorf("transition = %+v, want the id the name resolved to", f.calls[1].body["transition"])
	}
}

// A name no transition carries is an error naming what the issue *can* do, which is
// the one thing an operator needs and cannot see from "HTTP 400".
func TestTransitionByUnknownName(t *testing.T) {
	f, srv := newFakeJira(t)
	f.answers["GET /rest/api/2/issue/OPS-42/transitions"] = map[string]any{
		"transitions": []any{map[string]any{"id": "11", "name": "In Arbeit"}},
	}
	_, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{
		Operation: "transition-issue", Issue: "OPS-42", Transition: "Fertig",
	})
	if err == nil {
		t.Fatal("Do accepted a transition the issue does not offer")
	}
	if !strings.Contains(err.Error(), "In Arbeit") {
		t.Errorf("error = %v, want it to list the available transitions", err)
	}
}

func TestAddComment(t *testing.T) {
	f, srv := newFakeJira(t)
	f.answers["POST /rest/api/2/issue/OPS-42/comment"] = map[string]any{"id": "10100", "body": "hallo"}
	got, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{
		Operation: "add-comment", Issue: "OPS-42", Comment: "hallo",
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if c := f.only(t); c.body["body"] != "hallo" {
		t.Errorf("body = %+v, want the comment", c.body)
	}
	if m, _ := got.(map[string]any); m == nil || m["id"] != "10100" {
		t.Errorf("result = %+v, want the created comment", got)
	}
}

// Cloud addresses an account by accountId; Data Center by username. Which one is not
// guessed from the value — it follows from the credential the connector holds, which
// is the same thing that decides the authentication scheme.
func TestAssignIssue(t *testing.T) {
	f, srv := newFakeJira(t)
	if _, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{
		Operation: "assign-issue", Issue: "OPS-42", Assignee: "5b10ac",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	c := f.only(t)
	if c.method != http.MethodPut || c.path != "/rest/api/2/issue/OPS-42/assignee" {
		t.Errorf("request = %s %s, want the assignee sub-resource", c.method, c.path)
	}
	if c.body["accountId"] != "5b10ac" {
		t.Errorf("body = %+v, want an accountId on Cloud", c.body)
	}

	f2, srv2 := newFakeJira(t)
	dc := jira.NewHTTPClient(jira.Connector{BaseURL: srv2.URL, Token: "pat"})
	if _, err := dc.Do(context.Background(), jira.Request{Operation: "assign-issue", Issue: "OPS-42", Assignee: "arno"}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if c := f2.only(t); c.body["name"] != "arno" {
		t.Errorf("body = %+v, want a username on Data Center", c.body)
	}
}

// A search returns the issues themselves, not Jira's paging envelope: the envelope is
// this connector's business, and a model that had to unwrap it would be modelling the
// API rather than the work. Data Center keeps the offset-paged endpoint, so this is
// what it looks like there.
func TestSearchOnDataCenterPagesToTheCap(t *testing.T) {
	var seen []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		dec := json.NewDecoder(r.Body)
		dec.UseNumber()
		_ = dec.Decode(&body)
		seen = append(seen, body)
		startAt := 0
		if n, ok := body["startAt"].(json.Number); ok {
			v, _ := n.Int64()
			startAt = int(v)
		}
		issues := []any{}
		for i := startAt; i < startAt+2 && i < 5; i++ {
			issues = append(issues, map[string]any{"key": fmt.Sprintf("OPS-%d", i)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"startAt": startAt, "total": 5, "issues": issues})
	}))
	t.Cleanup(srv.Close)

	got, err := dcClient(srv.URL).Do(context.Background(), jira.Request{
		Operation: "search", JQL: "project = OPS", MaxResults: 3,
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	issues, _ := got.([]any)
	if len(issues) != 3 {
		t.Fatalf("issues = %d, want the cap of 3: %+v", len(issues), got)
	}
	if len(seen) != 2 {
		t.Fatalf("requests = %d, want two pages", len(seen))
	}
	if seen[0]["jql"] != "project = OPS" {
		t.Errorf("jql = %v, want the authored query", seen[0]["jql"])
	}
}

// An uncapped search follows Jira's paging to the end rather than stopping at one page.
func TestSearchOnDataCenterUncappedReadsEveryPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		dec := json.NewDecoder(r.Body)
		dec.UseNumber()
		_ = dec.Decode(&body)
		startAt := 0
		if n, ok := body["startAt"].(json.Number); ok {
			v, _ := n.Int64()
			startAt = int(v)
		}
		issues := []any{}
		for i := startAt; i < startAt+2 && i < 5; i++ {
			issues = append(issues, map[string]any{"key": fmt.Sprintf("OPS-%d", i)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"startAt": startAt, "total": 5, "issues": issues})
	}))
	t.Cleanup(srv.Close)
	got, err := dcClient(srv.URL).Do(context.Background(), jira.Request{Operation: "search", JQL: "project = OPS"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if issues, _ := got.([]any); len(issues) != 5 {
		t.Fatalf("issues = %d, want all five", len(issues))
	}
}

// Cloud authenticates with HTTP Basic over email:apiToken; Data Center with a bearer
// personal access token. Both are the same connector kind and differ only in the
// bundle an operator stored.
func TestAuthenticationSchemes(t *testing.T) {
	f, srv := newFakeJira(t)
	f.answers["GET /rest/api/2/issue/OPS-1"] = map[string]any{"key": "OPS-1"}
	if _, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{Operation: "get-issue", Issue: "OPS-1"}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got := f.only(t).auth; !strings.HasPrefix(got, "Basic ") {
		t.Errorf("Authorization = %q, want HTTP Basic for a Cloud API token", got)
	}

	f2, srv2 := newFakeJira(t)
	f2.answers["GET /rest/api/2/issue/OPS-1"] = map[string]any{"key": "OPS-1"}
	dc := jira.NewHTTPClient(jira.Connector{BaseURL: srv2.URL, Token: "pat"})
	if _, err := dc.Do(context.Background(), jira.Request{Operation: "get-issue", Issue: "OPS-1"}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if got := f2.only(t).auth; got != "Bearer pat" {
		t.Errorf("Authorization = %q, want a bearer PAT", got)
	}
}

// Jira's own error envelope is what an operator needs to see; "HTTP 400" is not.
func TestErrorSurfacesJirasMessage(t *testing.T) {
	f, srv := newFakeJira(t)
	f.status["POST /rest/api/2/issue"] = http.StatusBadRequest
	_, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{
		Operation: "create-issue", Project: "OPS", IssueType: "Task", Summary: "s",
	})
	if err == nil {
		t.Fatal("Do accepted a rejected create")
	}
	if !strings.Contains(err.Error(), "summary") {
		t.Errorf("error = %v, want Jira's own message in it", err)
	}
}

// An operation nothing implements is refused rather than sent somewhere plausible.
func TestUnknownOperation(t *testing.T) {
	_, srv := newFakeJira(t)
	if _, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{Operation: "explode", Issue: "OPS-1"}); err == nil {
		t.Fatal("Do accepted an unknown operation")
	}
}

// A connector with neither credential shape is refused at build time, so its tasks
// park with a reason instead of calling Jira unauthenticated.
func TestConnectorNeedsACredential(t *testing.T) {
	if _, err := jira.NewProviderClient(jira.ProviderConfig{Endpoint: "https://acme.atlassian.net", Secret: ""}); err == nil {
		t.Fatal("a connector with no credential was accepted")
	}
	if _, err := jira.NewProviderClient(jira.ProviderConfig{Endpoint: "https://acme.atlassian.net", Secret: "{"}); err == nil {
		t.Fatal("a malformed credential bundle was accepted")
	}
	if _, err := jira.NewProviderClient(jira.ProviderConfig{Endpoint: "https://acme.atlassian.net", Secret: `{"email":"a@b.c"}`}); err == nil {
		t.Fatal("a bundle with an email and no token was accepted")
	}
	if _, err := jira.NewProviderClient(jira.ProviderConfig{Endpoint: "", Secret: `{"token":"pat"}`}); err == nil {
		t.Fatal("a connector with no base URL was accepted")
	}
	if _, err := jira.NewProviderClient(jira.ProviderConfig{Endpoint: "https://acme.atlassian.net", Secret: `{"token":"pat"}`}); err != nil {
		t.Fatalf("a Data Center PAT bundle was rejected: %v", err)
	}
}

// ---------- the worker ----------

// fakeReader is the slice of the state store the handler reads: one element instance
// and the variables its scope sees.
type fakeReader struct {
	ei   *model.ElementInstanceValue
	vars []model.VariableValue
}

func (f *fakeReader) GetElementInstance(uint64) (*model.ElementInstanceValue, bool, error) {
	return f.ei, f.ei != nil, nil
}

func (f *fakeReader) VariablesOfScope(scope uint64, fn func(*model.VariableValue) error) error {
	if scope != f.ei.ProcessInstanceKey {
		return nil
	}
	for i := range f.vars {
		if err := fn(&f.vars[i]); err != nil {
			return err
		}
	}
	return nil
}

// recordingClient captures what the worker resolved and answers with a canned result.
type recordingClient struct {
	reqs   []jira.Request
	result any
	err    error
}

func (r *recordingClient) Do(_ context.Context, req jira.Request) (any, error) {
	r.reqs = append(r.reqs, req)
	return r.result, r.err
}

// workerFixture compiles a one-task process and returns the pieces a handler needs.
func workerFixture(t *testing.T, inner string, vars ...model.VariableValue) (*fakeReader, func(uint64) *compiler.CompiledProcess) {
	t.Helper()
	bpmn := `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>` + inner + `</bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
	cp, err := compiler.Parse(7, 1, strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	task := cp.Flow(cp.Outgoing(cp.StartEvents()[0])[0]).Target
	rd := &fakeReader{
		// FlowScopeKey is the process-instance scope: the chain the handler walks to
		// read the variables the task sees (ADR-0068) ends there.
		ei:   &model.ElementInstanceValue{ProcessInstanceKey: 500, ProcessDefKey: 7, ElementId: task, FlowScopeKey: 500},
		vars: vars,
	}
	return rd, func(uint64) *compiler.CompiledProcess { return cp }
}

// The worker evaluates the task's FEEL values over the variables it sees, sends what
// they resolved to, and writes what Jira returned into the result variable.
func TestHandlerResolvesAndWritesBack(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:jiraConnector connector="acme" operation="create-issue" project="OPS" issueType="Task"
		    summary="=betreff" resultVariable="ticket"><atlas:jiraField name="labels" value="=marken"/></atlas:jiraConnector>`,
		model.VariableValue{Name: "betreff", Kind: model.VarString, Text: "Neuer Zugang"},
		model.VariableValue{Name: "marken", Kind: model.VarJSON, Text: `["atlas","zugang"]`},
	)
	client := &recordingClient{result: map[string]any{"key": "OPS-42"}}
	reg := jira.NewRegistry()
	reg.Register("acme", client)

	out, err := jira.Handler(rd, lookup, reg)(job.Job{Key: 9, ElementInstanceKey: 42})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(client.reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(client.reqs))
	}
	req := client.reqs[0]
	if req.Operation != "create-issue" || req.Summary != "Neuer Zugang" {
		t.Errorf("request = %+v, want the resolved summary", req)
	}
	labels, _ := req.Fields["labels"].([]any)
	if len(labels) != 2 || labels[0] != "atlas" {
		t.Errorf("labels = %#v, want the FEEL list sent as a JSON list", req.Fields["labels"])
	}
	if req.RequestID != "9" {
		t.Errorf("requestID = %q, want the job key", req.RequestID)
	}
	if len(out) != 1 || out[0].Name != "ticket" || out[0].Kind != model.VarJSON {
		t.Fatalf("outputs = %+v, want the created issue in \"ticket\"", out)
	}
}

// An operation Jira answers with nothing writes no variable, even where the model
// authored a result variable for another task on the same process.
func TestHandlerWritesNothingWithoutAResultVariable(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:jiraConnector connector="acme" operation="assign-issue" issueKey="OPS-42" assignee="5b10"/>`)
	reg := jira.NewRegistry()
	reg.Register("acme", &recordingClient{})
	out, err := jira.Handler(rd, lookup, reg)(job.Job{Key: 1, ElementInstanceKey: 42})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("outputs = %+v, want none", out)
	}
}

// A connector name the registry does not hold fails the job with the registry's own
// reason, so a parked token can say whether it was never configured or is broken.
func TestHandlerUnresolvedConnector(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:jiraConnector connector="acme" operation="get-issue" issueKey="OPS-1" resultVariable="t"/>`)
	_, err := jira.Handler(rd, lookup, jira.NewRegistry())(job.Job{Key: 1, ElementInstanceKey: 42})
	if err == nil || !strings.Contains(err.Error(), "acme") {
		t.Fatalf("error = %v, want it to name the unresolved connector", err)
	}
}

// A vanished element instance is not an error: the job outlived what it belonged to.
func TestHandlerMissingElementInstance(t *testing.T) {
	rd, lookup := workerFixture(t,
		`<atlas:jiraConnector connector="acme" operation="get-issue" issueKey="OPS-1" resultVariable="t"/>`)
	rd.ei = nil
	out, err := jira.Handler(rd, lookup, jira.NewRegistry())(job.Job{Key: 1, ElementInstanceKey: 42})
	if err != nil || out != nil {
		t.Fatalf("handler = %+v, %v; want nothing to do", out, err)
	}
}

// TestJiraOpsMatchTheConnector is the drift guard between this package's [Ops] table
// and the compiler's own copy of the operation rules.
//
// The compiler cannot import this package — connector/jira imports compiler, so the
// dependency only runs one way — which is why the rules exist twice. The check is
// therefore behavioural: for every operation, a model supplying exactly what Ops says
// is required must compile, and a model missing any one of those values must not.
func TestJiraOpsMatchTheConnector(t *testing.T) {
	attrsFor := func(op string, spec jira.Op, omit string) string {
		parts := []string{`connector="acme"`, `operation="` + op + `"`}
		add := func(name, attr, value string) {
			if omit != name {
				parts = append(parts, attr+`="`+value+`"`)
			}
		}
		if spec.NeedsIssue {
			add("issue", "issueKey", "OPS-42")
		}
		if spec.NeedsProject {
			add("project", "project", "OPS")
			add("issueType", "issueType", "Task")
		}
		if spec.NeedsSummary {
			add("summary", "summary", "Zugang")
		}
		if spec.NeedsTransition {
			add("transition", "transition", "Fertig")
		}
		if spec.NeedsComment {
			add("comment", "comment", "hallo")
		}
		if spec.NeedsAssignee {
			add("assignee", "assignee", "5b10")
		}
		if spec.NeedsJQL {
			add("jql", "jql", "project = OPS")
		}
		if spec.NeedsQuery {
			add("query", "query", "patrick")
		}
		if spec.NeedsResult {
			add("result", "resultVariable", "ergebnis")
		}
		// update-issue changes nothing without one of summary, description or a field.
		if spec.NeedsChange {
			add("change", "summary", "Anders")
		}
		return strings.Join(parts, " ")
	}
	compile := func(attrs string) error {
		bpmn := `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0">
  <bpmn:process id="p">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements><atlas:jiraConnector ` + attrs + `/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
		_, err := compiler.Parse(1, 1, strings.NewReader(bpmn))
		return err
	}
	for op, spec := range jira.Ops {
		t.Run(op, func(t *testing.T) {
			if err := compile(attrsFor(op, spec, "")); err != nil {
				t.Fatalf("the compiler rejects a model that satisfies Ops[%q]: %v", op, err)
			}
			for _, omit := range []string{"issue", "project", "summary", "transition", "comment", "assignee", "jql", "query", "result", "change"} {
				required := map[string]bool{
					"issue": spec.NeedsIssue, "project": spec.NeedsProject, "summary": spec.NeedsSummary,
					"transition": spec.NeedsTransition, "comment": spec.NeedsComment, "assignee": spec.NeedsAssignee,
					"jql": spec.NeedsJQL, "query": spec.NeedsQuery, "result": spec.NeedsResult,
					"change": spec.NeedsChange,
				}[omit]
				if !required {
					continue
				}
				if err := compile(attrsFor(op, spec, omit)); err == nil {
					t.Errorf("the compiler accepts %q without its %s, which Ops says it needs", op, omit)
				}
			}
		})
	}
}

// The operation names are the vocabulary three places share; a name here that the
// compiler does not know would deploy and then fail at call time.
func TestOpNames(t *testing.T) {
	names := jira.OpNames()
	if len(names) != len(jira.Ops) {
		t.Fatalf("OpNames() = %v, want one per operation", names)
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("OpNames() = %v, want them sorted", names)
		}
	}
}

// On Jira Cloud a search goes to the token-paged /search/jql endpoint, not the
// offset-paged /search Atlassian removed. It asks for navigable fields explicitly,
// because the replacement returns none unless told to — a model reading
// issue.fields.summary would otherwise get issues with nothing in them.
func TestSearchOnCloudUsesTheJQLEndpoint(t *testing.T) {
	var seen []call
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := call{method: r.Method, path: r.URL.Path}
		dec := json.NewDecoder(r.Body)
		dec.UseNumber()
		_ = dec.Decode(&c.body)
		seen = append(seen, c)
		_, _ = w.Write([]byte(`{"issues":[{"key":"OPS-1"}]}`))
	}))
	t.Cleanup(srv.Close)

	got, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{Operation: "search", JQL: "project = OPS"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if issues, _ := got.([]any); len(issues) != 1 {
		t.Fatalf("issues = %+v, want the one match", got)
	}
	if len(seen) != 1 {
		t.Fatalf("requests = %d, want one", len(seen))
	}
	if seen[0].method != http.MethodPost || seen[0].path != "/rest/api/3/search/jql" {
		t.Errorf("request = %s %s, want POST /rest/api/3/search/jql", seen[0].method, seen[0].path)
	}
	if seen[0].body["jql"] != "project = OPS" {
		t.Errorf("jql = %v, want the authored query", seen[0].body["jql"])
	}
	if _, ok := seen[0].body["startAt"]; ok {
		t.Errorf("body = %+v, want no startAt: the replacement endpoint pages by token", seen[0].body)
	}
	fields, _ := seen[0].body["fields"].([]any)
	if len(fields) != 1 || fields[0] != "*navigable" {
		t.Errorf("fields = %+v, want the navigable set asked for explicitly", seen[0].body["fields"])
	}
}

// An uncapped Cloud search follows nextPageToken to the end, and the page without a
// token is the last one.
func TestSearchOnCloudPagesByToken(t *testing.T) {
	var tokens []any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		dec := json.NewDecoder(r.Body)
		dec.UseNumber()
		_ = dec.Decode(&body)
		tokens = append(tokens, body["nextPageToken"])
		switch body["nextPageToken"] {
		case nil:
			_, _ = w.Write([]byte(`{"issues":[{"key":"OPS-1"},{"key":"OPS-2"}],"nextPageToken":"p2"}`))
		case "p2":
			_, _ = w.Write([]byte(`{"issues":[{"key":"OPS-3"}]}`))
		default:
			t.Errorf("unexpected page token %v", body["nextPageToken"])
		}
	}))
	t.Cleanup(srv.Close)

	got, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{Operation: "search", JQL: "project = OPS"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if issues, _ := got.([]any); len(issues) != 3 {
		t.Fatalf("issues = %+v, want all three across the two pages", got)
	}
	if len(tokens) != 2 || tokens[0] != nil || tokens[1] != "p2" {
		t.Errorf("tokens = %+v, want the first page unkeyed and the second carrying p2", tokens)
	}
}

// The cap is the model's statement about what may reach its result variable, so it is
// applied to what arrived rather than trusted to the server.
func TestSearchOnCloudPagesToTheCap(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]any
		dec := json.NewDecoder(r.Body)
		dec.UseNumber()
		_ = dec.Decode(&body)
		_, _ = w.Write([]byte(fmt.Sprintf(
			`{"issues":[{"key":"OPS-%da"},{"key":"OPS-%db"}],"nextPageToken":"p%d"}`, calls, calls, calls+1)))
	}))
	t.Cleanup(srv.Close)

	got, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{
		Operation: "search", JQL: "project = OPS", MaxResults: 3,
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if issues, _ := got.([]any); len(issues) != 3 {
		t.Fatalf("issues = %+v, want the cap of 3", got)
	}
	if calls != 2 {
		t.Errorf("requests = %d, want two pages to reach the cap", calls)
	}
}

// Looking an account up is what makes assign-issue usable from a process: Jira hands an
// issue to an accountId, a process knows a person by their address. On Cloud the term
// travels as query, which Jira matches against a display name and an address — username
// is refused there since its GDPR changes.
func TestSearchUsersOnCloud(t *testing.T) {
	f, srv := newFakeJira(t)
	f.answers["GET /rest/api/2/user/search"] = []any{
		map[string]any{"accountId": "5bbb", "displayName": "Patrick Blumer"},
	}
	got, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{
		Operation: "search-users", Query: "  patrick@blumer.net  ",
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	users, _ := got.([]any)
	if len(users) != 1 {
		t.Fatalf("users = %+v, want the one account", got)
	}
	first, _ := users[0].(map[string]any)
	if first["accountId"] != "5bbb" {
		t.Errorf("account = %+v, want the accountId a later assign-issue takes", first)
	}
	c := f.only(t)
	if c.method != http.MethodGet || c.path != "/rest/api/2/user/search" {
		t.Errorf("call = %s %s, want a GET of the user search", c.method, c.path)
	}
	q, err := url.ParseQuery(c.query)
	if err != nil {
		t.Fatalf("parse query %q: %v", c.query, err)
	}
	// Trimmed: Jira matches the term as a substring, so a stray space is not a
	// difference in formatting but a term nothing can match.
	if q.Get("query") != "patrick@blumer.net" {
		t.Errorf("query = %q, want the trimmed term", q.Get("query"))
	}
	if q.Has("username") {
		t.Errorf("query %q carries username, which Cloud refuses", c.query)
	}
}

// Data Center matches a username fragment instead, and takes it under that name. The
// choice follows from the credential, so a model never says which product it is talking
// to — the same fact that decides how an assignee is addressed.
func TestSearchUsersOnDataCenterMatchesUsername(t *testing.T) {
	f, srv := newFakeJira(t)
	f.answers["GET /rest/api/2/user/search"] = []any{map[string]any{"name": "pblumer"}}
	if _, err := dcClient(srv.URL).Do(context.Background(), jira.Request{
		Operation: "search-users", Query: "pblumer",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	q, err := url.ParseQuery(f.only(t).query)
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	if q.Get("username") != "pblumer" {
		t.Errorf("username = %q, want the term Data Center matches on", q.Get("username"))
	}
	if q.Has("query") {
		t.Errorf("query %q carries the Cloud parameter", f.only(t).query)
	}
}

// A project restricts the search to the accounts that project can actually assign,
// through Jira's own assignable-user endpoint. Filtering afterwards would not do: an
// account that exists but cannot be assigned is exactly the value that makes a later
// assign-issue fail, one task after the one that chose it.
func TestSearchUsersInAProjectAsksForAssignableAccounts(t *testing.T) {
	f, srv := newFakeJira(t)
	f.answers["GET /rest/api/2/user/assignable/search"] = []any{map[string]any{"accountId": "5bbb"}}
	if _, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{
		Operation: "search-users", Query: "patrick", Project: " OPS ",
	}); err != nil {
		t.Fatalf("Do: %v", err)
	}
	c := f.only(t)
	if c.path != "/rest/api/2/user/assignable/search" {
		t.Errorf("path = %q, want the assignable-user endpoint", c.path)
	}
	q, err := url.ParseQuery(c.query)
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	if q.Get("project") != "OPS" {
		t.Errorf("project = %q, want the trimmed project key", q.Get("project"))
	}
}

// The cap is the model's statement about what may reach its result variable, so an
// account search pages to it and applies it to what arrived. This endpoint answers with
// a bare array — no envelope, so no total and no token — which is why a short page is
// what ends the read.
func TestSearchUsersPagesToTheCap(t *testing.T) {
	var seen []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		seen = append(seen, q)
		startAt, _ := strconv.Atoi(q.Get("startAt"))
		page, _ := strconv.Atoi(q.Get("maxResults"))
		users := []any{}
		for i := startAt; i < startAt+page && i < 5; i++ {
			users = append(users, map[string]any{"accountId": fmt.Sprintf("id-%d", i)})
		}
		_ = json.NewEncoder(w).Encode(users)
	}))
	t.Cleanup(srv.Close)

	got, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{
		Operation: "search-users", Query: "a", MaxResults: 3,
	})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	users, _ := got.([]any)
	if len(users) != 3 {
		t.Fatalf("users = %d, want the cap of 3: %+v", len(users), got)
	}
	if len(seen) != 1 {
		t.Fatalf("requests = %d, want one page asked for exactly the cap", len(seen))
	}
	if seen[0].Get("maxResults") != "3" {
		t.Errorf("maxResults = %q, want the cap rather than a full page", seen[0].Get("maxResults"))
	}
}

// An uncapped account search reads every page. A server that ignored startAt would
// otherwise be read forever: the short page that ends the read is the condition an
// envelope's total carries on the issue search.
func TestSearchUsersUncappedReadsEveryPage(t *testing.T) {
	var pages int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages++
		q := r.URL.Query()
		startAt, _ := strconv.Atoi(q.Get("startAt"))
		page, _ := strconv.Atoi(q.Get("maxResults"))
		users := []any{}
		for i := startAt; i < startAt+page && i < 150; i++ {
			users = append(users, map[string]any{"accountId": fmt.Sprintf("id-%d", i)})
		}
		_ = json.NewEncoder(w).Encode(users)
	}))
	t.Cleanup(srv.Close)

	got, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{Operation: "search-users", Query: "a"})
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if users, _ := got.([]any); len(users) != 150 {
		t.Fatalf("users = %d, want all 150 across pages", len(users))
	}
	if pages != 2 {
		t.Errorf("requests = %d, want two pages of a hundred", pages)
	}
}

// A refused account search fails the job rather than answering with no accounts. The
// common cause is a permission rather than a defect — the worker's account needs Jira's
// global "Browse users and groups" — and an empty result would send an operator looking
// for a person who is there, one task later and in the wrong place.
func TestSearchUsersReportsARefusal(t *testing.T) {
	f, srv := newFakeJira(t)
	f.status["GET /rest/api/2/user/search"] = http.StatusForbidden
	_, err := cloudClient(srv.URL).Do(context.Background(), jira.Request{Operation: "search-users", Query: "patrick"})
	if err == nil {
		t.Fatal("Do accepted a 403, which would complete the token on a search that never ran")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("err = %v, want the status Jira answered with", err)
	}
}
