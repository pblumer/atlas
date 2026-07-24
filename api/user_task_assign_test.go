package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// oneAuthTask deploys the user-task process, starts an instance, and returns the
// single open task's job key, driving every request through the given
// (authenticated) client.
func oneAuthTask(t *testing.T, ts *httptest.Server, c *http.Client) uint64 {
	t.Helper()
	code, body := cReq(t, c, ts, "POST", "/api/v1/deployments", userTaskBPMN)
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	code, body = cReq(t, c, ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}")
	if code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}
	code, body = cReq(t, c, ts, "GET", "/api/v1/tasks", "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: status=%d body=%s", code, body)
	}
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil {
		t.Fatalf("decode tasks: %v (%s)", err, body)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %d, want 1", len(tasks))
	}
	return tasks[0].Key
}

// assigneeOf reads the single open task's current assignee via the given client.
func assigneeOf(t *testing.T, ts *httptest.Server, c *http.Client, key uint64) string {
	t.Helper()
	_, body := cReq(t, c, ts, "GET", "/api/v1/tasks", "")
	var tasks []struct {
		Key      uint64 `json:"key"`
		Assignee string `json:"assignee"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil {
		t.Fatalf("decode tasks: %v (%s)", err, body)
	}
	for _, tk := range tasks {
		if tk.Key == key {
			return tk.Assignee
		}
	}
	t.Fatalf("task %d not found in %s", key, body)
	return ""
}

func TestClaimBindsToIdentity(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatalf("admin login failed")
	}
	// A real user to assign to, plus a disabled one that must be rejected.
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"alice","password":"password1"}`); code != http.StatusCreated {
		t.Fatalf("create alice: %d (%s)", code, b)
	}
	code, cb := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"carol","password":"password1"}`)
	if code != http.StatusCreated {
		t.Fatalf("create carol: %d (%s)", code, cb)
	}
	var carol struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(cb, &carol)
	if code, _ := cReq(t, admin, ts, "PATCH", "/api/v1/users/"+carol.ID, `{"disabled":true}`); code != http.StatusOK {
		t.Fatalf("disable carol failed")
	}

	key := oneAuthTask(t, ts, admin)

	// Empty body → claim for the signed-in user (authoritative self-claim).
	if code, b := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/claim", key), ""); code != http.StatusOK {
		t.Fatalf("self-claim: %d (%s)", code, b)
	}
	if a := assigneeOf(t, ts, admin, key); a != "root" {
		t.Fatalf("self-claim assignee = %q, want \"root\"", a)
	}

	// A named, enabled user is assigned; case is normalized to the stored username.
	if code, b := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/claim", key), `{"assignee":"ALICE"}`); code != http.StatusOK {
		t.Fatalf("assign alice: %d (%s)", code, b)
	}
	if a := assigneeOf(t, ts, admin, key); a != "alice" {
		t.Fatalf("assign assignee = %q, want \"alice\"", a)
	}

	// Unknown and disabled users are refused.
	if code, _ := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/claim", key), `{"assignee":"ghost"}`); code != http.StatusBadRequest {
		t.Fatalf("assign unknown: want 400, got %d", code)
	}
	if code, _ := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/claim", key), `{"assignee":"carol"}`); code != http.StatusBadRequest {
		t.Fatalf("assign disabled: want 400, got %d", code)
	}
	// A malformed body is still a 400.
	if code, _ := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/claim", key), `{bad`); code != http.StatusBadRequest {
		t.Fatalf("assign bad json: want 400, got %d", code)
	}

	// Unclaim clears it.
	if code, _ := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/unclaim", key), ""); code != http.StatusOK {
		t.Fatalf("unclaim failed")
	}
	if a := assigneeOf(t, ts, admin, key); a != "" {
		t.Fatalf("after unclaim assignee = %q, want empty", a)
	}
}

func TestClaimAuthOffRequiresAssignee(t *testing.T) {
	ts := newTestServer(t) // auth off
	task := listOneTask(t, ts)
	// With no identity, an empty claim body has nobody to claim for.
	code, _ := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/claim", task.Key), "", "application/json")
	if code != http.StatusBadRequest {
		t.Fatalf("empty claim (auth off): want 400, got %d", code)
	}
}

func TestClaimAndAssignableStoreErrors(t *testing.T) {
	ts, dir := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	login(t, admin, ts, "root", "rootpassword")
	_, cu := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"alice","password":"password1"}`)
	aliceID := idOf(t, cu)
	key := oneAuthTask(t, ts, admin)

	// Corrupt a user record so the username lookup (and the assignable scan) fail.
	corruptUserFile(t, dir, aliceID)
	if code, _ := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/claim", key), `{"assignee":"alice"}`); code != http.StatusInternalServerError {
		t.Fatalf("claim over corrupt store: want 500, got %d", code)
	}
	if code, _ := cReq(t, admin, ts, "GET", "/api/v1/users/assignable", ""); code != http.StatusInternalServerError {
		t.Fatalf("assignable over corrupt store: want 500, got %d", code)
	}
}

func TestAssignableUsersEndpoint(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	login(t, admin, ts, "root", "rootpassword")
	cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"alice","password":"password1"}`)
	code, db := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"dave","password":"password1"}`)
	if code != http.StatusCreated {
		t.Fatalf("create dave: %d (%s)", code, db)
	}
	var dave struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(db, &dave)
	cReq(t, admin, ts, "PATCH", "/api/v1/users/"+dave.ID, `{"disabled":true}`)

	// A non-admin can list assignable users (it is not admin-gated), and the
	// literal /assignable path wins over /users/{id}.
	alice := newClient(t)
	if login(t, alice, ts, "alice", "password1") != http.StatusOK {
		t.Fatalf("alice login failed")
	}
	code, body := cReq(t, alice, ts, "GET", "/api/v1/users/assignable", "")
	if code != http.StatusOK {
		t.Fatalf("assignable as non-admin: want 200, got %d (%s)", code, body)
	}
	var list []struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode assignable: %v (%s)", err, body)
	}
	names := map[string]bool{}
	for _, u := range list {
		names[u.Username] = true
	}
	if !names["root"] || !names["alice"] {
		t.Fatalf("assignable missing enabled users: %s", body)
	}
	if names["dave"] {
		t.Fatalf("assignable must exclude disabled users: %s", body)
	}
	// The admin-gated management list is still forbidden for a non-admin.
	if code, _ := cReq(t, alice, ts, "GET", "/api/v1/users", ""); code != http.StatusForbidden {
		t.Fatalf("non-admin management list: want 403, got %d", code)
	}
}
