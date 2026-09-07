package mcp_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestListInstancesForwardsScopeAndElement covers the MCP side of the Operations
// filter (ADR-0261): an agent asked "which instances are
// stuck on this task?" must be able to put that question to the engine directly,
// instead of listing everything and sieving the page it happened to get — which at
// a few hundred thousand instances answers with a subset of a page and no way to
// tell that from the truth.
func TestListInstancesForwardsScopeAndElement(t *testing.T) {
	var gotMethod, gotPath string
	var gotQuery url.Values
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"key":88}]`))
	}))
	defer backend.Close()

	response := callToolAgainstBackend(t, backend, "atlas_list_instances", map[string]any{
		"process": 7,
		"element": "Eintritt_verbuchen",
		"state":   "active",
		"limit":   25,
	})
	text, isErr := toolText(t, result(t, response))
	if isErr {
		t.Fatalf("atlas_list_instances returned tool error: %s", text)
	}

	if gotMethod != http.MethodGet || gotPath != "/api/v1/instances" {
		t.Fatalf("request = %s %s, want GET /api/v1/instances", gotMethod, gotPath)
	}
	if got := gotQuery.Get("process"); got != "7" {
		t.Errorf("process query = %q, want 7", got)
	}
	if got := gotQuery.Get("element"); got != "Eintritt_verbuchen" {
		t.Errorf("element query = %q, want the BPMN element id", got)
	}
	if got := gotQuery.Get("state"); got != "active" {
		t.Errorf("state query = %q, want active", got)
	}
	if got := gotQuery.Get("limit"); got != "25" {
		t.Errorf("limit query = %q, want 25", got)
	}
	// There is no cursor argument: the page cursor rides in a response header this
	// tool does not carry, so offering one would be a dead parameter.
	if _, ok := gotQuery["before"]; ok {
		t.Errorf("the tool sent a before cursor it has no way to obtain: %v", gotQuery)
	}
}

// TestListInstancesWithoutArgumentsIsUnscoped keeps the tool's original shape: no
// arguments still means "every instance", so an existing caller sees no change.
func TestListInstancesWithoutArgumentsIsUnscoped(t *testing.T) {
	var gotPath, gotRawQuery string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotRawQuery = r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer backend.Close()

	response := callToolAgainstBackend(t, backend, "atlas_list_instances", map[string]any{})
	if text, isErr := toolText(t, result(t, response)); isErr {
		t.Fatalf("atlas_list_instances returned tool error: %s", text)
	}
	if gotPath != "/api/v1/instances" || gotRawQuery != "" {
		t.Errorf("request = %s?%s, want /api/v1/instances with no query", gotPath, gotRawQuery)
	}
}

// TestListInstancesRefusesElementWithoutProcess reports the one combination the
// engine cannot answer, at the tool boundary rather than as an HTTP 400 an agent
// has to interpret: a BPMN element id means nothing without the version defining
// it, and the index it reads is keyed by that pair.
func TestListInstancesRefusesElementWithoutProcess(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the tool called the server for a request it should have refused: %s", r.URL)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer backend.Close()

	response := callToolAgainstBackend(t, backend, "atlas_list_instances", map[string]any{
		"element": "Eintritt_verbuchen",
	})
	text, isErr := toolText(t, result(t, response))
	if !isErr {
		t.Fatalf("element without process = %q, want a tool error", text)
	}
	if text == "" {
		t.Error("the refusal carries no explanation")
	}
}

// TestListInstancesRejectsMalformedNumbers keeps the numeric arguments honest at the
// tool boundary. A "process" or "limit" that is not a positive integer is a mistake
// in the call, and saying so beats forwarding a query the server would answer with a
// 400 — or, worse, silently dropping the narrowing and returning everything.
func TestListInstancesRejectsMalformedNumbers(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the tool called the server for a request it should have refused: %s", r.URL)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer backend.Close()

	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"process is not a number", map[string]any{"process": "seven"}},
		{"process is zero", map[string]any{"process": 0}},
		{"limit is negative", map[string]any{"limit": -3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text, isErr := toolText(t, result(t, callToolAgainstBackend(t, backend, "atlas_list_instances", tc.args)))
			if !isErr {
				t.Fatalf("atlas_list_instances(%v) = %q, want a tool error", tc.args, text)
			}
		})
	}
}
