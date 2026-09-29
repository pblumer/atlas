package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"
)

// Every task row says when the task was opened, and a list asked for it carries
// what the task is about — the values its form is filled from — so the inbox can
// find "every task for this recipient" by more than a task name that is the same
// on every row.
func TestTaskRowsSayWhenTheyOpenedAndCarryTheirContentOnRequest(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", userTaskBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	before := time.Now().Add(-time.Minute).UnixMilli()
	vars := `{"variables":{"recipient":"usr_patrick","anzahl":3,"eilig":true,"leer":""}}`
	if c, b := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), vars, "application/json"); c != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", c, b)
	}

	type row struct {
		Key       uint64   `json:"key"`
		CreatedAt int64    `json:"createdAt"`
		Content   []string `json:"content"`
	}
	read := func(path string) []row {
		t.Helper()
		code, body := doReq(t, ts, http.MethodGet, path, "", "")
		if code != http.StatusOK {
			t.Fatalf("GET %s: status=%d body=%s", path, code, body)
		}
		var rows []row
		if err := json.Unmarshal(listRows(t, body), &rows); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if len(rows) != 1 {
			t.Fatalf("GET %s = %d rows, want 1", path, len(rows))
		}
		return rows
	}

	plain := read("/api/v1/tasks")[0]
	if plain.CreatedAt < before || plain.CreatedAt > time.Now().Add(time.Minute).UnixMilli() {
		t.Errorf("createdAt = %d, want the moment the task opened (Unix ms, now-ish)", plain.CreatedAt)
	}
	if plain.Content != nil {
		t.Errorf("content = %v on a list that did not ask for it; the default row stays the size it was", plain.Content)
	}

	full := read("/api/v1/tasks?content=1")[0]
	if full.CreatedAt != plain.CreatedAt {
		t.Errorf("createdAt = %d with content, %d without; one task, one instant", full.CreatedAt, plain.CreatedAt)
	}
	for _, want := range []string{"usr_patrick", "3"} {
		if !slices.Contains(full.Content, want) {
			t.Errorf("content = %v, missing %q", full.Content, want)
		}
	}
	for _, not := range []string{"true", ""} {
		if slices.Contains(full.Content, not) {
			t.Errorf("content = %v carries %q; only non-empty text and numbers find a task", full.Content, not)
		}
	}

	// The by-key read is the same row.
	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/tasks/%d", plain.Key), "", "")
	var one row
	if code != http.StatusOK || json.Unmarshal(body, &one) != nil || one.CreatedAt != plain.CreatedAt {
		t.Errorf("GET task by key: status=%d createdAt=%d, want %d", code, one.CreatedAt, plain.CreatedAt)
	}
}
