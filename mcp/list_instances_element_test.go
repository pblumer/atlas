package mcp_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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
		"before":  "1757248000000000000.281474976710660",
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
	// The cursor is passed through as the server wrote it. It is a string and not a
	// number because the finished half's cursor is a (completion time, key) pair —
	// the tool must not try to interpret it.
	if got := gotQuery.Get("before"); got != "1757248000000000000.281474976710660" {
		t.Errorf("before query = %q, want the cursor verbatim", got)
	}
}

// TestListInstancesReturnsAPage covers the response shape: the same
// {items, truncated, nextCursor} envelope atlas_list_tasks returns, so an agent
// paging one list does not have to learn a second protocol for the other — and so a
// capped page says it is capped. A bare array could not: it would report the first
// page of three hundred thousand instances as though it were all of them.
func TestListInstancesReturnsAPage(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Instances-Truncated", "true")
		w.Header().Set("X-Instances-Next-Cursor", "1757248000000000000.281474976710658")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"key":88},{"key":77}]`))
	}))
	defer backend.Close()

	text, isErr := toolText(t, result(t, callToolAgainstBackend(t, backend, "atlas_list_instances", map[string]any{
		"process": 7, "state": "active", "limit": 2,
	})))
	if isErr {
		t.Fatalf("atlas_list_instances returned tool error: %s", text)
	}
	var page struct {
		Items []struct {
			Key uint64 `json:"key"`
		} `json:"items"`
		Truncated  bool   `json:"truncated"`
		NextCursor string `json:"nextCursor"`
	}
	if err := json.Unmarshal([]byte(text), &page); err != nil {
		t.Fatalf("decode page: %v (%s)", err, text)
	}
	if len(page.Items) != 2 || page.Items[0].Key != 88 {
		t.Errorf("items = %+v, want the two rows the server returned", page.Items)
	}
	if !page.Truncated {
		t.Error("a capped page did not report itself as capped")
	}
	if page.NextCursor != "1757248000000000000.281474976710658" {
		t.Errorf("nextCursor = %q, want the header verbatim", page.NextCursor)
	}
}

// TestListInstancesUncappedPageOmitsCursor keeps the envelope honest the other way:
// a page that is everything says so, and carries no cursor to page with.
func TestListInstancesUncappedPageOmitsCursor(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"key":88}]`))
	}))
	defer backend.Close()

	text, isErr := toolText(t, result(t, callToolAgainstBackend(t, backend, "atlas_list_instances", map[string]any{})))
	if isErr {
		t.Fatalf("atlas_list_instances returned tool error: %s", text)
	}
	if strings.Contains(text, "nextCursor") {
		t.Errorf("an uncapped page carries a cursor: %s", text)
	}
	if !strings.Contains(text, `"truncated":false`) {
		t.Errorf("an uncapped page does not say it is complete: %s", text)
	}
}

// TestListInstancesWithoutArgumentsIsUnscoped pins what "no arguments" means: every
// instance in the engine, asked for with a bare path and no narrowing.
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

// TestListInstancesEmptyPageIsAnEmptyList pins the one shape an agent iterating
// items must never meet: a JSON null. An engine holding no instances answers with an
// empty array, and so does the page around it.
func TestListInstancesEmptyPageIsAnEmptyList(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`null`)) // the shape a server could answer with, but should not
	}))
	defer backend.Close()

	text, isErr := toolText(t, result(t, callToolAgainstBackend(t, backend, "atlas_list_instances", map[string]any{})))
	if isErr {
		t.Fatalf("atlas_list_instances returned tool error: %s", text)
	}
	if !strings.Contains(text, `"items":[]`) {
		t.Errorf("empty page = %s, want an empty items array", text)
	}
}
