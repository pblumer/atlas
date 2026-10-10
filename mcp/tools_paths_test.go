package mcp_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// apiCall is one request a tool made of the Atlas API, as the backend saw it.
type apiCall struct {
	Method, Path, RawQuery, ContentType string
	Body                                []byte
}

// apiRecorder stands in for an Atlas server: it answers every request with the
// status and body it was given and keeps what arrived, so a test can say which
// endpoint a tool's arguments turned into — the whole of what a tool handler does.
type apiRecorder struct {
	srv *httptest.Server

	mu     sync.Mutex
	calls  []apiCall
	status int
	reply  string
}

func newAPIRecorder(t *testing.T, status int, reply string) *apiRecorder {
	t.Helper()
	rec := &apiRecorder{status: status, reply: reply}
	rec.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		rec.mu.Lock()
		rec.calls = append(rec.calls, apiCall{
			Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery,
			ContentType: r.Header.Get("Content-Type"), Body: body,
		})
		status, reply := rec.status, rec.reply
		rec.mu.Unlock()
		if reply != "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(rec.srv.Close)
	return rec
}

// Calls returns what arrived so far.
func (r *apiRecorder) Calls() []apiCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]apiCall(nil), r.calls...)
}

// call runs one tool through the stdio dispatcher against the recorder and returns
// the tool's text, whether it was a tool error, and the single API request it made
// (nil when it made none — a refused argument must not reach the API).
func (r *apiRecorder) call(t *testing.T, tool string, args map[string]any) (string, bool, *apiCall) {
	t.Helper()
	before := len(r.Calls())
	text, isErr := toolText(t, result(t, callToolAgainstBackend(t, r.srv, tool, args)))
	calls := r.Calls()[before:]
	switch len(calls) {
	case 0:
		return text, isErr, nil
	case 1:
		return text, isErr, &calls[0]
	default:
		t.Fatalf("%s made %d API calls, want at most 1: %+v", tool, len(calls), calls)
		return "", false, nil
	}
}

// bodyObject decodes a JSON request body.
func bodyObject(t *testing.T, c *apiCall) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(c.Body, &m); err != nil {
		t.Fatalf("request body %q is not a JSON object: %v", c.Body, err)
	}
	return m
}

// wantCall fails unless c is the request described.
func wantCall(t *testing.T, tool string, c *apiCall, method, path, rawQuery string) {
	t.Helper()
	if c == nil {
		t.Fatalf("%s made no API call, want %s %s", tool, method, path)
	}
	if c.Method != method || c.Path != path || c.RawQuery != rawQuery {
		t.Fatalf("%s called %s %s?%s, want %s %s?%s", tool, c.Method, c.Path, c.RawQuery, method, path, rawQuery)
	}
}

// wantRefusal fails unless the tool answered with a tool error naming want and made
// no API call: an argument the tool cannot use must stop it before the server.
func wantRefusal(t *testing.T, tool, text string, isErr bool, c *apiCall, want string) {
	t.Helper()
	if !isErr || !strings.Contains(text, want) {
		t.Fatalf("%s = (%q, isErr=%v), want a tool error containing %q", tool, text, isErr, want)
	}
	if c != nil {
		t.Fatalf("%s reached the API (%s %s) despite refusing its arguments", tool, c.Method, c.Path)
	}
}

// TestProductUsageReadsTheItemsUsage pins the endpoint the reverse-usage tool reads,
// with the product id path-escaped: an id is free text from a catalogue.
func TestProductUsageReadsTheItemsUsage(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `{"itemId":"laptop pro"}`)
	text, isErr, c := rec.call(t, "atlas_product_usage", map[string]any{"itemId": "laptop pro"})
	if isErr || text != `{"itemId":"laptop pro"}` {
		t.Fatalf("atlas_product_usage = (%q, isErr=%v), want the API body", text, isErr)
	}
	wantCall(t, "atlas_product_usage", c, http.MethodGet, "/api/v1/catalog-products/laptop pro/usage", "")
}

// TestSaveProcessDiagramPutsTheDocument: the diagram tool sends the whole document
// as XML to the definition's diagram endpoint, and refuses without one.
func TestSaveProcessDiagramPutsTheDocument(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `{"key":7}`)

	text, isErr, c := rec.call(t, "atlas_save_process_diagram", map[string]any{"key": 7})
	wantRefusal(t, "atlas_save_process_diagram", text, isErr, c, "argument: xml")

	text, isErr, c = rec.call(t, "atlas_save_process_diagram", map[string]any{"key": 7, "xml": "<definitions/>"})
	if isErr || text != `{"key":7}` {
		t.Fatalf("atlas_save_process_diagram = (%q, isErr=%v), want the API body", text, isErr)
	}
	wantCall(t, "atlas_save_process_diagram", c, http.MethodPut, "/api/v1/processes/7/diagram", "")
	if c.ContentType != "application/xml" || string(c.Body) != "<definitions/>" {
		t.Fatalf("diagram request = (%q, %q), want the XML document verbatim", c.ContentType, c.Body)
	}
}

// TestDeleteToolsPassAnAnswerThrough: the delete tools invent a confirmation only for
// an empty 204. When the server does say something, that is what the model reads.
func TestDeleteToolsPassAnAnswerThrough(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `{"deleted":true,"note":"from the server"}`)
	for _, tc := range []struct {
		tool string
		args map[string]any
		path string
	}{
		{"atlas_delete_process", map[string]any{"key": 5}, "/api/v1/processes/5"},
		{"atlas_delete_decision_deployment", map[string]any{"key": 6}, "/api/v1/decision-deployments/6"},
		{"atlas_clear_mail_outbox", map[string]any{}, "/api/v1/mail/outbox"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			text, isErr, c := rec.call(t, tc.tool, tc.args)
			if isErr || text != `{"deleted":true,"note":"from the server"}` {
				t.Fatalf("%s = (%q, isErr=%v), want the server's own answer", tc.tool, text, isErr)
			}
			wantCall(t, tc.tool, c, http.MethodDelete, tc.path, "")
		})
	}
}

// TestDeleteDecisionDeploymentConfirmsAnEmptyAnswer: on a 204 the tool states what it
// deleted, so the model is not handed an empty string to interpret.
func TestDeleteDecisionDeploymentConfirmsAnEmptyAnswer(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusNoContent, "")
	text, isErr, c := rec.call(t, "atlas_delete_decision_deployment", map[string]any{"key": 42})
	if isErr || text != `{"deleted":true,"key":42}` {
		t.Fatalf("atlas_delete_decision_deployment = (%q, isErr=%v), want a confirmation naming key 42", text, isErr)
	}
	wantCall(t, "atlas_delete_decision_deployment", c, http.MethodDelete, "/api/v1/decision-deployments/42", "")
}

// TestDeleteToolsSurfaceARefusal: a refused delete (a pinned decision, an outbox the
// server cannot clear) must come back as a tool error carrying the server's reason,
// never as a confirmation.
func TestDeleteToolsSurfaceARefusal(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusConflict, `{"error":"pinned by process 9"}`)
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"atlas_delete_decision_deployment", map[string]any{"key": 6}},
		{"atlas_clear_mail_outbox", map[string]any{}},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			text, isErr, _ := rec.call(t, tc.tool, tc.args)
			if !isErr || !strings.Contains(text, "pinned by process 9") || strings.Contains(text, "deleted") {
				t.Fatalf("%s = (%q, isErr=%v), want the server's refusal as a tool error", tc.tool, text, isErr)
			}
		})
	}
}

// TestMailOutboxForwardsItsLimit: the newest-n limit rides as a query parameter, and
// a limit that is not a positive integer is refused before the server is asked.
func TestMailOutboxForwardsItsLimit(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `{"messages":[],"truncated":false}`)

	_, isErr, c := rec.call(t, "atlas_mail_outbox", map[string]any{"limit": 3})
	if isErr {
		t.Fatal("atlas_mail_outbox with limit 3 reported an error")
	}
	wantCall(t, "atlas_mail_outbox", c, http.MethodGet, "/api/v1/mail/outbox", "limit=3")

	text, isErr, c := rec.call(t, "atlas_mail_outbox", map[string]any{"limit": "lots"})
	wantRefusal(t, "atlas_mail_outbox", text, isErr, c, "limit")
}

// TestCompleteJobRequiresAReason: completing a job by hand is an operator
// intervention recorded with why it was done (ADR-0159), so it must not reach the API
// without one.
func TestCompleteJobRequiresAReason(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `{}`)
	text, isErr, c := rec.call(t, "atlas_complete_job", map[string]any{"key": 3})
	wantRefusal(t, "atlas_complete_job", text, isErr, c, "argument: reason")
}

// TestListIncidentsElementIndexZeroIsAFilter: index 0 is a real compiled element, so
// the tool must forward it rather than treat it as "not given" — and refuse an index
// that is not a number at all.
func TestListIncidentsElementIndexZeroIsAFilter(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `{"items":[]}`)

	_, isErr, c := rec.call(t, "atlas_list_incidents", map[string]any{"elementIndex": 0})
	if isErr {
		t.Fatal("atlas_list_incidents with elementIndex 0 reported an error")
	}
	wantCall(t, "atlas_list_incidents", c, http.MethodGet, "/api/v1/incidents", "elementIndex=0")

	_, _, c = rec.call(t, "atlas_list_incidents", map[string]any{"process": 4, "elementIndex": 2, "type": "job"})
	wantCall(t, "atlas_list_incidents", c, http.MethodGet, "/api/v1/incidents", "process=4&elementIndex=2&type=job")

	text, isErr, c := rec.call(t, "atlas_list_incidents", map[string]any{"elementIndex": "first"})
	wantRefusal(t, "atlas_list_incidents", text, isErr, c, "elementIndex")
}

// TestIncidentSummaryRefusesABadScope: a scope that is not a positive key would read
// as "everything", which is the opposite of what the caller asked for.
func TestIncidentSummaryRefusesABadScope(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `{"groups":[]}`)
	text, isErr, c := rec.call(t, "atlas_incident_summary", map[string]any{"process": -1})
	wantRefusal(t, "atlas_incident_summary", text, isErr, c, "process")
}

// TestResolveIncidentsRefusesANonIntegerKey names the offending position, so the
// caller can find it in a long key list.
func TestResolveIncidentsRefusesANonIntegerKey(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `{}`)
	text, isErr, c := rec.call(t, "atlas_resolve_incidents", map[string]any{"keys": []any{1, "two"}})
	wantRefusal(t, "atlas_resolve_incidents", text, isErr, c, "keys[1]")
}

// TestForkInstanceSendsReasonAndResumePoints: a fork is an operator action recorded on
// both instances, so the reason is required; the resume points and an explicit
// mapping travel in the body as given.
func TestForkInstanceSendsReasonAndResumePoints(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `{"successorInstanceKey":99}`)

	text, isErr, c := rec.call(t, "atlas_fork_instance", map[string]any{"key": 11, "targetProcessDefKey": 12})
	wantRefusal(t, "atlas_fork_instance", text, isErr, c, "argument: reason")

	text, isErr, c = rec.call(t, "atlas_fork_instance", map[string]any{"key": 11, "reason": "stuck"})
	wantRefusal(t, "atlas_fork_instance", text, isErr, c, "targetProcessDefKey")

	text, isErr, c = rec.call(t, "atlas_fork_instance", map[string]any{
		"key": 11, "targetProcessDefKey": 12, "reason": "stuck",
		"resume": []any{"review"},
	})
	if isErr || text != `{"successorInstanceKey":99}` {
		t.Fatalf("atlas_fork_instance = (%q, isErr=%v), want the API body", text, isErr)
	}
	wantCall(t, "atlas_fork_instance", c, http.MethodPost, "/api/v1/instances/11/migrate/fork", "")
	body := bodyObject(t, c)
	if body["targetProcessDefKey"] != float64(12) || body["reason"] != "stuck" {
		t.Fatalf("fork body = %v, want target 12 and the reason", body)
	}
	if resume, _ := body["resume"].([]any); len(resume) != 1 || resume[0] != "review" {
		t.Fatalf("fork body resume = %v, want [review]", body["resume"])
	}
	if _, has := body["mapping"]; has {
		t.Fatalf("fork body = %v, want no mapping when none was given", body)
	}
}

// TestMigrationPlanCarriesTheMapping: element-id overrides reach the plan endpoint
// untouched, and a plan needs no reason because it writes nothing — but it does need
// the version it is a plan for.
func TestMigrationPlanCarriesTheMapping(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `{"migratable":true}`)
	text, isErr, c := rec.call(t, "atlas_migration_plan", map[string]any{"key": 1})
	wantRefusal(t, "atlas_migration_plan", text, isErr, c, "targetProcessDefKey")

	mapping := []any{map[string]any{"from": "old", "to": "new"}}
	_, isErr, c = rec.call(t, "atlas_migration_plan",
		map[string]any{"key": 1, "targetProcessDefKey": 2, "mapping": mapping})
	if isErr {
		t.Fatal("atlas_migration_plan reported an error")
	}
	wantCall(t, "atlas_migration_plan", c, http.MethodPost, "/api/v1/instances/1/migrate/plan", "")
	body := bodyObject(t, c)
	if _, has := body["reason"]; has {
		t.Fatalf("plan body = %v, want no reason", body)
	}
	got, _ := body["mapping"].([]any)
	if len(got) != 1 || got[0].(map[string]any)["from"] != "old" || got[0].(map[string]any)["to"] != "new" {
		t.Fatalf("plan body mapping = %v, want the override as given", body["mapping"])
	}
}

// TestBatchToolsRefuseABadPageSize: the batch tools share one paging reader, and a
// limit that is not a positive integer stops them before anything is migrated.
func TestBatchToolsRefuseABadPageSize(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `{}`)
	text, isErr, c := rec.call(t, "atlas_reindex_instances", map[string]any{"key": 3, "limit": 0})
	wantRefusal(t, "atlas_reindex_instances", text, isErr, c, "limit")

	text, isErr, c = rec.call(t, "atlas_migrate_instances",
		map[string]any{"key": 3, "targetProcessDefKey": 4, "reason": "move", "limit": "all"})
	wantRefusal(t, "atlas_migrate_instances", text, isErr, c, "limit")

	text, isErr, c = rec.call(t, "atlas_migrate_instances", map[string]any{"key": 3, "targetProcessDefKey": 4})
	wantRefusal(t, "atlas_migrate_instances", text, isErr, c, "argument: reason")
}

// TestBatchToolsWithoutPagingSendNoQuery: an empty cursor is "start from the front",
// which is the server's default, so nothing is appended to the path.
func TestBatchToolsWithoutPagingSendNoQuery(t *testing.T) {
	rec := newAPIRecorder(t, http.StatusOK, `{"remaining":false}`)
	_, isErr, c := rec.call(t, "atlas_reindex_instances", map[string]any{"key": 3, "after": "  "})
	if isErr {
		t.Fatal("atlas_reindex_instances reported an error")
	}
	wantCall(t, "atlas_reindex_instances", c, http.MethodPost, "/api/v1/processes/3/reindex-instances", "")
}
