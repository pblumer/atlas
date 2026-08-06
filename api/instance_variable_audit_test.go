package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// auditRowJSON mirrors one row of the variable-audit endpoint.
type auditRowJSON struct {
	At    int64  `json:"at"`
	Actor string `json:"actor"`
	Scope uint64 `json:"scope"`
	Name  string `json:"name"`
	Value any    `json:"value"`
	Kind  string `json:"kind"`
}

// TestInstanceVariableAuditEmpty covers an instance (or unknown key) with no
// overrides: the endpoint returns an empty array, not a 404 — it is a convenience
// read like the variables and decisions endpoints.
func TestInstanceVariableAuditEmpty(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/instances/999/variable-audit", "", "")
	if code != http.StatusOK {
		t.Fatalf("audit for unknown instance: status=%d body=%s", code, body)
	}
	var rows []auditRowJSON
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(rows) != 0 {
		t.Fatalf("rows = %v, want empty", rows)
	}
}

// TestInstanceVariableAuditRecordsChanges covers the audit read (ADR-0098): after an
// operator overrides variables on a running instance, the endpoint returns one row
// per variable with its typed new value, in change order. With auth off the actor is
// empty (single-user) — the "who" is meaningful only under auth, exercised separately.
func TestInstanceVariableAuditRecordsChanges(t *testing.T) {
	ts := newTestServer(t)
	key := deployAndStartWaiting(t, ts, `{"variables":{"amount":100}}`)

	code, body := doReq(t, ts, http.MethodPost,
		fmt.Sprintf("/api/v1/instances/%d/variables", key),
		`{"variables":{"amount":200,"reviewer":"sam"}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("set variables: status=%d body=%s", code, body)
	}

	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/variable-audit", key), "", "")
	if code != http.StatusOK {
		t.Fatalf("audit: status=%d body=%s", code, body)
	}
	var rows []auditRowJSON
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (%s)", len(rows), body)
	}
	byName := map[string]auditRowJSON{}
	for _, r := range rows {
		byName[r.Name] = r
		if r.Actor != "" {
			t.Errorf("actor = %q, want empty with auth off", r.Actor)
		}
		if r.Scope != key {
			t.Errorf("scope = %d, want %d (instance root)", r.Scope, key)
		}
	}
	if got, ok := byName["amount"]; !ok || got.Kind != "number" || fmt.Sprint(got.Value) != "200" {
		t.Errorf("amount row = %+v, want number 200", byName["amount"])
	}
	if got, ok := byName["reviewer"]; !ok || got.Kind != "string" || got.Value != "sam" {
		t.Errorf("reviewer row = %+v, want string sam", byName["reviewer"])
	}
}

// TestInstanceVariableAuditInvalidKey rejects a non-numeric instance key with 400.
func TestInstanceVariableAuditInvalidKey(t *testing.T) {
	ts := newTestServer(t)
	code, _ := doReq(t, ts, http.MethodGet, "/api/v1/instances/not-a-number/variable-audit", "", "")
	if code != http.StatusBadRequest {
		t.Errorf("non-numeric key: %d, want 400", code)
	}
}

// TestInstanceVariableAuditKinds covers the typed rendering of every variable kind
// in the audit view (boolean, number, string, json, null).
func TestInstanceVariableAuditKinds(t *testing.T) {
	ts := newTestServer(t)
	key := deployAndStartWaiting(t, ts, "")
	code, body := doReq(t, ts, http.MethodPost,
		fmt.Sprintf("/api/v1/instances/%d/variables", key),
		`{"variables":{"flag":true,"count":5,"note":"hi","payload":{"a":1},"empty":null}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("set variables: status=%d body=%s", code, body)
	}
	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/variable-audit", key), "", "")
	if code != http.StatusOK {
		t.Fatalf("audit: status=%d body=%s", code, body)
	}
	var rows []auditRowJSON
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	kinds := map[string]string{}
	for _, r := range rows {
		kinds[r.Name] = r.Kind
	}
	for name, want := range map[string]string{"flag": "boolean", "count": "number", "note": "string", "payload": "json", "empty": "null"} {
		if kinds[name] != want {
			t.Errorf("%s kind = %q, want %q", name, kinds[name], want)
		}
	}
}

// TestInstanceVariableAuditRecordsActor is the point of the feature: under auth, the
// audit trail attributes each override to the authenticated user who made it.
func TestInstanceVariableAuditRecordsActor(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "supersecret")
	admin := newClient(t)
	if code := login(t, admin, ts, "root", "supersecret"); code != http.StatusOK {
		t.Fatalf("admin login: %d, want 200", code)
	}

	// Deploy and start a waiting instance as the admin.
	code, dep := cReqCT(t, admin, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, dep)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(dep, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if code, _ := cReq(t, admin, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), ""); code != http.StatusOK {
		t.Fatalf("start: %d", code)
	}
	code, list := cReq(t, admin, ts, http.MethodGet, "/api/v1/instances", "")
	if code != http.StatusOK {
		t.Fatalf("list: %d", code)
	}
	var instances []struct {
		Key   uint64 `json:"key"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(list, &instances); err != nil {
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

	// The admin overrides a variable, then reads the audit trail back.
	if code, sv := cReq(t, admin, ts, http.MethodPost, fmt.Sprintf("/api/v1/instances/%d/variables", key), `{"variables":{"amount":200}}`); code != http.StatusOK {
		t.Fatalf("set: %d (%s)", code, sv)
	}
	code, body := cReq(t, admin, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/variable-audit", key), "")
	if code != http.StatusOK {
		t.Fatalf("audit: %d (%s)", code, body)
	}
	var rows []auditRowJSON
	if err := json.Unmarshal(body, &rows); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 (%s)", len(rows), body)
	}
	if rows[0].Actor != "root" {
		t.Errorf("actor = %q, want %q", rows[0].Actor, "root")
	}
	if rows[0].Name != "amount" || fmt.Sprint(rows[0].Value) != "200" {
		t.Errorf("row = %+v, want amount=200", rows[0])
	}
}
