package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// cReqCT issues an authenticated request through a cookie-jar client with an
// explicit content type, so a test can deploy XML (or any media type) as a
// logged-in user — cReq forces application/json.
func cReqCT(t *testing.T, c *http.Client, ts *httptest.Server, method, path, body, contentType string) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, r)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, data
}

// deployAndStartWaiting deploys sampleBPMN (which parks at a service task with no
// worker) and starts one instance, returning the running instance's key. Shared by
// the set-variables tests, which need a live instance to correct.
func deployAndStartWaiting(t *testing.T, ts *httptest.Server, startBody string) uint64 {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), startBody, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}
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
	for _, in := range instances {
		if in.State == "active" {
			return in.Key
		}
	}
	t.Fatal("no active instance found")
	return 0
}

func readVarsHTTP(t *testing.T, ts *httptest.Server, key uint64) map[string]any {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/variables", key), "", "")
	if code != http.StatusOK {
		t.Fatalf("get variables: status=%d body=%s", code, body)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var vars map[string]any
	if err := dec.Decode(&vars); err != nil {
		t.Fatalf("decode variables: %v (%s)", err, body)
	}
	return vars
}

// TestSetInstanceVariablesOverwritesAndCreates is the happy path (ADR-0095): an
// operator overwrites an existing variable and adds a new one on a running instance,
// then reads them back typed. Auth is off (single-user), so the admin gate is a
// pass-through.
func TestSetInstanceVariablesOverwritesAndCreates(t *testing.T) {
	ts := newTestServer(t)
	key := deployAndStartWaiting(t, ts, `{"variables":{"amount":100,"status":"new"}}`)

	code, body := doReq(t, ts, http.MethodPost,
		fmt.Sprintf("/api/v1/instances/%d/variables", key),
		`{"variables":{"amount":200,"reviewer":"patrick"}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("set variables: status=%d body=%s", code, body)
	}
	var res struct {
		InstanceKey  uint64 `json:"instanceKey"`
		VariablesSet int    `json:"variablesSet"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode set result: %v (%s)", err, body)
	}
	if res.InstanceKey != key || res.VariablesSet != 2 {
		t.Fatalf("set result = %+v, want instanceKey=%d variablesSet=2", res, key)
	}

	vars := readVarsHTTP(t, ts, key)
	if got, ok := vars["amount"].(json.Number); !ok || got.String() != "200" {
		t.Errorf("amount = %v (%T), want number 200 (overwritten)", vars["amount"], vars["amount"])
	}
	if got, ok := vars["reviewer"].(string); !ok || got != "patrick" {
		t.Errorf("reviewer = %v (%T), want string patrick (created)", vars["reviewer"], vars["reviewer"])
	}
	if got, ok := vars["status"].(string); !ok || got != "new" {
		t.Errorf("status = %v (%T), want string new (untouched)", vars["status"], vars["status"])
	}
}

// TestSetInstanceVariablesUnknownInstance returns 404: only a running instance can
// be corrected; an unknown (or finished) key is not found.
func TestSetInstanceVariablesUnknownInstance(t *testing.T) {
	ts := newTestServer(t)
	code, _ := doReq(t, ts, http.MethodPost, "/api/v1/instances/424242/variables",
		`{"variables":{"x":1}}`, "application/json")
	if code != http.StatusNotFound {
		t.Errorf("set on unknown instance: %d, want 404", code)
	}
}

// TestSetInstanceVariablesNoVariables rejects a body with no variables to set — a
// no-op POST is almost always a mistake, so it is a 400 rather than a silent 200.
func TestSetInstanceVariablesNoVariables(t *testing.T) {
	ts := newTestServer(t)
	key := deployAndStartWaiting(t, ts, "")
	code, body := doReq(t, ts, http.MethodPost,
		fmt.Sprintf("/api/v1/instances/%d/variables", key),
		`{"variables":{}}`, "application/json")
	if code != http.StatusBadRequest {
		t.Errorf("set with empty variables: %d, want 400 (%s)", code, body)
	}
}

// TestSetInstanceVariablesInvalidScope rejects a scopeKey that is not an element
// instance of the target instance, so a stray key cannot orphan variables under a
// scope no FEEL lookup reaches.
func TestSetInstanceVariablesInvalidScope(t *testing.T) {
	ts := newTestServer(t)
	key := deployAndStartWaiting(t, ts, "")
	code, body := doReq(t, ts, http.MethodPost,
		fmt.Sprintf("/api/v1/instances/%d/variables", key),
		`{"variables":{"x":1},"scopeKey":424242}`, "application/json")
	if code != http.StatusBadRequest {
		t.Errorf("set with foreign scope: %d, want 400 (%s)", code, body)
	}
}

// TestSetInstanceVariablesInvalidBody rejects a malformed JSON body with 400.
func TestSetInstanceVariablesInvalidBody(t *testing.T) {
	ts := newTestServer(t)
	key := deployAndStartWaiting(t, ts, "")
	code, _ := doReq(t, ts, http.MethodPost,
		fmt.Sprintf("/api/v1/instances/%d/variables", key),
		`{not json`, "application/json")
	if code != http.StatusBadRequest {
		t.Errorf("malformed JSON body: %d, want 400", code)
	}
}

func TestSetInstanceVariablesInvalidKey(t *testing.T) {
	ts := newTestServer(t)
	code, _ := doReq(t, ts, http.MethodPost, "/api/v1/instances/not-a-number/variables",
		`{"variables":{"x":1}}`, "application/json")
	if code != http.StatusBadRequest {
		t.Errorf("non-numeric key: %d, want 400", code)
	}
}

// TestSetInstanceVariablesRequiresAdmin proves the authorization boundary when auth
// is on: an unauthenticated caller is rejected by the auth middleware (401), a
// logged-in non-admin is rejected by the admin gate (403), and an admin is allowed
// through to correct a live instance (200). Editing live process state is a
// deliberately narrower right than starting an instance.
func TestSetInstanceVariablesRequiresAdmin(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "supersecret")

	// Unauthenticated: gated before the handler runs.
	code, _ := doReq(t, ts, http.MethodPost, "/api/v1/instances/1/variables",
		`{"variables":{"x":1}}`, "application/json")
	if code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated set: %d, want 401", code)
	}

	admin := newClient(t)
	if code := login(t, admin, ts, "root", "supersecret"); code != http.StatusOK {
		t.Fatalf("admin login: %d, want 200", code)
	}

	// A non-admin user, created by the admin, is rejected by the admin gate even
	// before the instance is looked up (so an unknown key still yields 403).
	code, cu := cReq(t, admin, ts, http.MethodPost, "/api/v1/users",
		`{"username":"clerk","password":"password1","roles":["user"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create non-admin: %d, want 201 (%s)", code, cu)
	}
	clerk := newClient(t)
	if code := login(t, clerk, ts, "clerk", "password1"); code != http.StatusOK {
		t.Fatalf("clerk login: %d, want 200", code)
	}
	code, _ = cReq(t, clerk, ts, http.MethodPost, "/api/v1/instances/1/variables",
		`{"variables":{"x":1}}`)
	if code != http.StatusForbidden {
		t.Fatalf("non-admin set: %d, want 403", code)
	}

	// The admin deploys, starts a waiting instance, and corrects it — the gate lets
	// the admin through.
	code, dep := cReqCT(t, admin, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("admin deploy: %d (%s)", code, dep)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(dep, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	code, _ = cReq(t, admin, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "")
	if code != http.StatusOK {
		t.Fatalf("admin start: %d", code)
	}
	code, list := cReq(t, admin, ts, http.MethodGet, "/api/v1/instances", "")
	if code != http.StatusOK {
		t.Fatalf("admin list: %d", code)
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
	code, sv := cReq(t, admin, ts, http.MethodPost, fmt.Sprintf("/api/v1/instances/%d/variables", key),
		`{"variables":{"x":1}}`)
	if code != http.StatusOK {
		t.Fatalf("admin set: %d, want 200 (%s)", code, sv)
	}
}
