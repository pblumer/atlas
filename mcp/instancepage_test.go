package mcp_test

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// listedInstance is the part of an instance row the tests here care about.
type listedInstance struct {
	Key   uint64 `json:"key"`
	State string `json:"state"`
}

// listedInstancePage is the envelope atlas_list_instances answers with — the same
// {items, truncated, nextCursor} shape as atlas_list_tasks.
type listedInstancePage struct {
	Items      []listedInstance `json:"items"`
	Truncated  bool             `json:"truncated"`
	NextCursor string           `json:"nextCursor"`
}

// listInstancesPage calls atlas_list_instances and decodes the page it answered
// with. It is the one place the tests decode that envelope: a dozen sites used to
// carry their own inline struct, which is a dozen edits every time the shape moves —
// and the reason the last shape change was worth doing only once.
func listInstancesPage(t *testing.T, atlas *httptest.Server, id int, args map[string]any) listedInstancePage {
	t.Helper()
	text, isErr := toolText(t, result(t, run(t, atlas, callTool(id, "atlas_list_instances", args))[0]))
	if isErr {
		t.Fatalf("atlas_list_instances(%v) errored: %s", args, text)
	}
	var page listedInstancePage
	if err := json.Unmarshal([]byte(text), &page); err != nil {
		t.Fatalf("decode instance page %q: %v", text, err)
	}
	return page
}

// listedInstances returns just the rows, for the callers that never look at the
// paging metadata.
func listedInstances(t *testing.T, atlas *httptest.Server, id int, args map[string]any) []listedInstance {
	t.Helper()
	return listInstancesPage(t, atlas, id, args).Items
}

// firstInstanceKey returns the key of the first listed instance, failing the test
// when the listing is empty — the "start something, then find it" step almost every
// tool test opens with.
func firstInstanceKey(t *testing.T, atlas *httptest.Server, id int, args map[string]any) uint64 {
	t.Helper()
	rows := listedInstances(t, atlas, id, args)
	if len(rows) == 0 {
		t.Fatalf("atlas_list_instances(%v) listed no instance", args)
	}
	return rows[0].Key
}
