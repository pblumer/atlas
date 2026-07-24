package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// TestInstanceVariablesTyped seeds an instance with variables of every kind and
// reads them back as a typed JSON object — the shape the Tasks app feeds a form
// so a field whose key matches a variable is prefilled (ADR-0028).
func TestInstanceVariablesTyped(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", twoUserTaskBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	start := `{"variables":{"Name":"Patrick","score":7,"approved":true,"tags":["a","b"],"nothing":null}}`
	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), start, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}

	// Find the running instance's key.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: status=%d body=%s", code, body)
	}
	var instances []struct {
		Key   uint64 `json:"key"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(body, &instances); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	var key uint64
	for _, in := range instances {
		if in.State == "active" {
			key = in.Key
		}
	}
	if key == 0 {
		t.Fatal("no active instance found")
	}

	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/variables", key), "", "")
	if code != http.StatusOK {
		t.Fatalf("get variables: status=%d body=%s", code, body)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var vars map[string]any
	if err := dec.Decode(&vars); err != nil {
		t.Fatalf("decode variables: %v (%s)", err, body)
	}
	if got, ok := vars["Name"].(string); !ok || got != "Patrick" {
		t.Errorf("Name = %v (%T), want string \"Patrick\"", vars["Name"], vars["Name"])
	}
	if got, ok := vars["score"].(json.Number); !ok || got.String() != "7" {
		t.Errorf("score = %v (%T), want number 7", vars["score"], vars["score"])
	}
	if got, ok := vars["approved"].(bool); !ok || got != true {
		t.Errorf("approved = %v (%T), want bool true", vars["approved"], vars["approved"])
	}
	if tags, ok := vars["tags"].([]any); !ok || len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Errorf("tags = %v (%T), want [\"a\",\"b\"]", vars["tags"], vars["tags"])
	}
	if v, present := vars["nothing"]; !present || v != nil {
		t.Errorf("nothing = %v (present=%v), want JSON null", v, present)
	}
}

// TestInstanceVariablesEmpty covers an instance (or unknown key) with no
// variables: the endpoint returns an empty object, not an error.
func TestInstanceVariablesEmpty(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances/999/variables", "", "")
	if code != http.StatusOK {
		t.Fatalf("get variables for unknown instance: status=%d body=%s", code, body)
	}
	if got := string(bytes.TrimSpace(body)); got != "{}" {
		t.Errorf("body = %s, want {}", got)
	}
}

func TestInstanceVariablesInvalidKey(t *testing.T) {
	ts := newTestServer(t)
	code, _ := doReq(t, ts, http.MethodGet, "/api/v1/instances/not-a-number/variables", "", "")
	if code != http.StatusBadRequest {
		t.Errorf("non-numeric key: %d, want 400", code)
	}
}
