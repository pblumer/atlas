package api_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

// The two credentials a connected system works user tasks with
// (ADR-0510 §2 and §3): a token of scope `tasklist`
// that lists the open tasks and can do nothing else, and a person's grant for the
// resource `…/tasks` that works that person's tasks as them — Atlas deciding, as it
// does for the person in the Tasks app.

// TestTasklistScopeListsTasksAndNothingElse: the token a copy of the open tasks is
// compared with reads the list and one task, and nothing it could change or that lies
// behind a task — not its form, not the instance's variables, not the feed.
func TestTasklistScopeListsTasksAndNothingElse(t *testing.T) {
	ts, admin := apiTokenServer(t)
	key := taskFrom(t, ts, admin, openTaskBPMN)
	secret, _ := mint(t, admin, ts, `{"name":"wiki tasks","scope":"tasklist","expiresInDays":365}`)

	code, body := bearerReq(t, ts, http.MethodGet, "/api/v1/tasks", "", secret)
	if code != http.StatusOK {
		t.Fatalf("tasklist token on GET /api/v1/tasks = %d, want 200; body = %.200s", code, body)
	}
	var tasks []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(listRows(t, body), &tasks); err != nil || len(tasks) != 1 || tasks[0].Key != key {
		t.Fatalf("an administrator's tasklist token lists %s, want the one open task %d", body, key)
	}
	if code, body := bearerReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/tasks/%d", key), "", secret); code != http.StatusOK {
		t.Errorf("tasklist token on GET /api/v1/tasks/{key} = %d, want 200; body = %.160s", code, body)
	}

	var instance uint64
	_, one := cReq(t, admin, ts, "GET", fmt.Sprintf("/api/v1/tasks/%d", key), "")
	var tr struct {
		ProcessInstanceKey uint64 `json:"processInstanceKey"`
	}
	_ = json.Unmarshal(one, &tr)
	instance = tr.ProcessInstanceKey

	for _, path := range []string{"/api/v1/processes", "/api/v1/instances", "/api/v1/events", "/api/v1/forms/any",
		"/api/v1/task-folders", "/api/v1/auth/me", fmt.Sprintf("/api/v1/instances/%d/variables", instance)} {
		if code, _ := bearerReq(t, ts, http.MethodGet, path, "", secret); code != http.StatusForbidden {
			t.Errorf("tasklist token on GET %s = %d, want 403", path, code)
		}
	}
	// It lists; it never takes a task on, gives one back or completes one.
	for _, w := range []struct{ method, path, body string }{
		{http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/claim", key), `{}`},
		{http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/unclaim", key), ``},
		{http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/complete", key), `{"variables":{}}`},
		{http.MethodPost, "/api/v1/deployments", idBPMN("smuggled")},
	} {
		if code, _ := bearerReq(t, ts, w.method, w.path, w.body, secret); code != http.StatusForbidden {
			t.Errorf("tasklist token on %s %s = %d, want 403", w.method, w.path, code)
		}
	}
	if code, _ := cReq(t, admin, ts, "GET", fmt.Sprintf("/api/v1/tasks/%d", key), ""); code != http.StatusOK {
		t.Errorf("the task is gone after the refused writes (%d)", code)
	}
}

// TestTheTasksScopeIsNotMintable: `tasks` follows from the resource a person
// approved, as `mcp` does; no token request can ask for it.
func TestTheTasksScopeIsNotMintable(t *testing.T) {
	ts, admin := apiTokenServer(t)
	if code, body := cReq(t, admin, ts, "POST", "/api/v1/api-tokens", `{"name":"x","scope":"tasks","expiresInDays":30}`); code != http.StatusBadRequest {
		t.Errorf("minting a tasks token = %d %s, want 400", code, body)
	}
}

// personClient signs a person in and returns a cookie-carrying client.
func personClient(t *testing.T, base, username, password string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	c := &http.Client{Jar: jar}
	resp, err := c.Post(base+"/api/v1/auth/login", "application/json",
		strings.NewReader(`{"username":"`+username+`","password":"`+password+`"}`))
	if err != nil {
		t.Fatalf("login %s: %v", username, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %s = %d", username, resp.StatusCode)
	}
	return c
}

// tasksGrant walks the authorization for the resource …/tasks as the person behind
// c and returns the access token.
func tasksGrant(t *testing.T, c *http.Client, base, clientID, clientSecret string) string {
	t.Helper()
	code := codeFrom(t, approve(t, c, base, clientID, base+"/tasks", true))
	if code == "" {
		t.Fatal("approval for …/tasks produced no code")
	}
	status, tok := postToken(t, base, url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {testRedirect}, "code_verifier": {testVerifier},
		"client_id": {clientID}, "client_secret": {clientSecret},
		"resource": {base + "/tasks"},
	})
	if status != http.StatusOK {
		t.Fatalf("token exchange for …/tasks = %d: %v", status, tok)
	}
	access, _ := tok["access_token"].(string)
	if access == "" {
		t.Fatalf("no access token: %v", tok)
	}
	return access
}

// bearerDo issues a request with an access token and returns its status and body.
func bearerDo(t *testing.T, base, method, path, body, token string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, base+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

// TestATasksGrantWorksThePersonsTasksAsThemAndNothingElse: a person approves a
// connected system for …/tasks; its token answers who the person is with their groups,
// lists and works the tasks Atlas lets that person work — and Atlas refuses the rest
// exactly as it refuses the person: a task addressed to somebody else is not theirs
// to see or complete. Nothing beyond the task routes is reached, whatever the person's
// roles: root is an administrator, and its tasks grant still cannot deploy.
func TestATasksGrantWorksThePersonsTasksAsThemAndNothingElse(t *testing.T) {
	ts := newMCPServer(t)
	root := signedInClient(t, ts.URL)
	clientID, clientSecret := registerClient(t, root, ts.URL)
	for _, u := range []string{"alice", "carol"} {
		resp, err := root.Post(ts.URL+"/api/v1/users", "application/json",
			strings.NewReader(`{"username":"`+u+`","password":"password1","roles":["user"]}`))
		if err != nil || resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s: %v %v", u, err, resp)
		}
		resp.Body.Close()
	}
	key := taskOn(t, root, ts.URL, addressedTaskBPMN) // addressed to alice

	// The resource is published, so a client can find what to ask for.
	resp, err := http.Get(ts.URL + "/.well-known/oauth-protected-resource/tasks")
	if err != nil {
		t.Fatalf("metadata: %v", err)
	}
	var meta map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&meta)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || meta["resource"] != ts.URL+"/tasks" {
		t.Fatalf("…/tasks metadata = %d %v, want 200 naming %s/tasks", resp.StatusCode, meta, ts.URL)
	}

	carol := tasksGrant(t, personClient(t, ts.URL, "carol", "password1"), ts.URL, clientID, clientSecret)
	alice := tasksGrant(t, personClient(t, ts.URL, "alice", "password1"), ts.URL, clientID, clientSecret)

	// Who the person is, with their groups.
	code, body := bearerDo(t, ts.URL, http.MethodGet, "/api/v1/auth/me", "", alice)
	var me struct {
		User struct {
			Username string            `json:"username"`
			Groups   []json.RawMessage `json:"groups"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &me); code != http.StatusOK || err != nil || me.User.Username != "alice" || me.User.Groups == nil {
		t.Fatalf("alice's grant on auth/me = %d %s, want alice with her groups", code, body)
	}

	// Atlas decides: carol does not see alice's task and cannot complete it.
	if code, _ := bearerDo(t, ts.URL, http.MethodGet, fmt.Sprintf("/api/v1/tasks/%d", key), "", carol); code != http.StatusNotFound {
		t.Errorf("carol's grant on alice's task = %d, want 404", code)
	}
	if code, _ := bearerDo(t, ts.URL, http.MethodPost, fmt.Sprintf("/api/v1/tasks/%d/complete", key), `{"variables":{}}`, carol); code == http.StatusOK {
		t.Errorf("carol's grant completed alice's task")
	}

	// alice sees it, gives it back, takes it on and completes it.
	if code, b := bearerDo(t, ts.URL, http.MethodGet, "/api/v1/tasks", "", alice); code != http.StatusOK || !strings.Contains(string(b), fmt.Sprint(key)) {
		t.Errorf("alice's grant lists %d %s, want her task %d", code, b, key)
	}
	for _, step := range []struct{ path, body string }{
		{fmt.Sprintf("/api/v1/tasks/%d/unclaim", key), ""},
		{fmt.Sprintf("/api/v1/tasks/%d/claim", key), "{}"},
		{fmt.Sprintf("/api/v1/tasks/%d/complete", key), `{"variables":{"ok":true}}`},
	} {
		if code, b := bearerDo(t, ts.URL, http.MethodPost, step.path, step.body, alice); code != http.StatusOK {
			t.Fatalf("alice's grant on POST %s = %d %s, want 200", step.path, code, b)
		}
	}

	// Nothing beyond the task routes, even for an administrator's grant.
	rootTasks := tasksGrant(t, root, ts.URL, clientID, clientSecret)
	for _, w := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/processes", ""},
		{http.MethodGet, "/api/v1/instances", ""},
		{http.MethodGet, "/api/v1/users", ""},
		{http.MethodGet, "/api/v1/events", ""},
		{http.MethodPost, "/api/v1/deployments", openTaskBPMN},
		{http.MethodGet, "/mcp", ""},
	} {
		if code, _ := bearerDo(t, ts.URL, w.method, w.path, w.body, rootTasks); code != http.StatusForbidden {
			t.Errorf("a tasks grant on %s %s = %d, want 403", w.method, w.path, code)
		}
	}
}

// taskOn deploys a model as c, starts it, and returns the key of its one task.
func taskOn(t *testing.T, c *http.Client, base, bpmn string) uint64 {
	t.Helper()
	resp, err := c.Post(base+"/api/v1/deployments", "application/xml", strings.NewReader(bpmn))
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&dep)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || dep.Key == 0 {
		t.Fatalf("deploy = %d", resp.StatusCode)
	}
	resp, err = c.Post(fmt.Sprintf("%s/api/v1/processes/%d/instances", base, dep.Key), "application/json", strings.NewReader("{}"))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("start: %v %v", err, resp)
	}
	resp.Body.Close()
	resp, err = c.Get(base + "/api/v1/tasks")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer resp.Body.Close()
	var page struct {
		Items []struct {
			Key uint64 `json:"key"`
		} `json:"items"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&page)
	if len(page.Items) != 1 {
		t.Fatalf("tasks = %+v, want one", page.Items)
	}
	return page.Items[0].Key
}
