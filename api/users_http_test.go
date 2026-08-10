package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// newAuthServer wires a real engine behind an auth-enforcing API, seeding the
// bootstrap admin with a known password so tests can log in.
func newAuthServer(t *testing.T, adminUser, adminPass string) (*httptest.Server, string) {
	t.Helper()
	t.Setenv("ATLAS_ADMIN_USERNAME", adminUser)
	t.Setenv("ATLAS_ADMIN_PASSWORD", adminPass)
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := api.New(proc, store, dir, api.WithAuth())
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	})
	return ts, dir
}

func newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{Jar: jar}
}

func cReq(t *testing.T, c *http.Client, ts *httptest.Server, method, path, body string) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, r)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, data
}

func login(t *testing.T, c *http.Client, ts *httptest.Server, user, pass string) int {
	t.Helper()
	code, _ := cReq(t, c, ts, "POST", "/api/v1/auth/login", `{"username":"`+user+`","password":"`+pass+`"}`)
	return code
}

// idOf extracts the "id" field from a user JSON response.
func idOf(t *testing.T, body []byte) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	id, _ := m["id"].(string)
	if id == "" {
		t.Fatalf("no id in %s", body)
	}
	return id
}

func TestAuthDisabledIsOpen(t *testing.T) {
	ts := newTestServer(t) // no WithAuth
	c := newClient(t)

	// /auth/me reports enforcement off and no user.
	code, body := cReq(t, c, ts, "GET", "/api/v1/auth/me", "")
	if code != http.StatusOK {
		t.Fatalf("me: got %d, want 200", code)
	}
	var me map[string]any
	_ = json.Unmarshal(body, &me)
	if me["authEnabled"] != false || me["user"] != nil {
		t.Fatalf("expected authEnabled=false, user=null; got %s", body)
	}

	// User management is reachable without logging in (single-user mode).
	code, cu := cReq(t, c, ts, "POST", "/api/v1/users",
		`{"username":"alice","password":"password1","roles":["user"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create user (auth off): got %d, want 201 (%s)", code, cu)
	}
	code, _ = cReq(t, c, ts, "GET", "/api/v1/users", "")
	if code != http.StatusOK {
		t.Fatalf("list users (auth off): got %d, want 200", code)
	}
}

func TestAuthLoginFlow(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	anon := newClient(t)

	// Gated endpoints require a session.
	if code, _ := cReq(t, anon, ts, "GET", "/api/v1/auth/me", ""); code != http.StatusUnauthorized {
		t.Fatalf("me without session: got %d, want 401", code)
	}
	if code, _ := cReq(t, anon, ts, "GET", "/api/v1/users", ""); code != http.StatusUnauthorized {
		t.Fatalf("users without session: got %d, want 401", code)
	}
	// But the static UI and info are public so the login screen can load.
	if code, _ := cReq(t, anon, ts, "GET", "/api/v1/info", ""); code != http.StatusOK {
		t.Fatalf("info should be public: got %d", code)
	}

	// Bad credentials are rejected uniformly.
	if login(t, anon, ts, "root", "wrong") != http.StatusUnauthorized {
		t.Fatalf("bad password should be 401")
	}
	if login(t, anon, ts, "ghost", "whatever") != http.StatusUnauthorized {
		t.Fatalf("unknown user should be 401")
	}
	if code, _ := cReq(t, anon, ts, "POST", "/api/v1/auth/login", `{"username":"","password":""}`); code != http.StatusBadRequest {
		t.Fatalf("empty creds should be 400, got %d", code)
	}

	// Log in as the seeded admin.
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatalf("admin login failed")
	}
	code, body := cReq(t, admin, ts, "GET", "/api/v1/auth/me", "")
	if code != http.StatusOK {
		t.Fatalf("me after login: %d", code)
	}
	var me struct {
		AuthEnabled bool `json:"authEnabled"`
		User        struct {
			Username string   `json:"username"`
			Roles    []string `json:"roles"`
		} `json:"user"`
	}
	_ = json.Unmarshal(body, &me)
	if !me.AuthEnabled || me.User.Username != "root" || len(me.User.Roles) != 1 || me.User.Roles[0] != "admin" {
		t.Fatalf("unexpected me: %s", body)
	}

	// Logout ends the session.
	if code, _ := cReq(t, admin, ts, "POST", "/api/v1/auth/logout", ""); code != http.StatusOK {
		t.Fatalf("logout: %d", code)
	}
	if code, _ := cReq(t, admin, ts, "GET", "/api/v1/auth/me", ""); code != http.StatusUnauthorized {
		t.Fatalf("me after logout: got %d, want 401", code)
	}
}

func TestUserCRUDAndAuthorization(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatalf("admin login failed")
	}

	// Validation.
	if code, _ := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"","password":"password1"}`); code != http.StatusBadRequest {
		t.Fatalf("missing username: want 400, got %d", code)
	}
	if code, _ := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"x","password":"short"}`); code != http.StatusBadRequest {
		t.Fatalf("short password: want 400, got %d", code)
	}
	if code, _ := cReq(t, admin, ts, "POST", "/api/v1/users", `not json`); code != http.StatusBadRequest {
		t.Fatalf("bad json: want 400, got %d", code)
	}

	// Create a plain user.
	code, body := cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"alice","email":"alice@example.com","displayName":"Alice","password":"password1","roles":["user"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create alice: got %d (%s)", code, body)
	}
	if strings.Contains(string(body), "passwordHash") || strings.Contains(string(body), "PasswordHash") {
		t.Fatalf("password hash leaked in response: %s", body)
	}
	aliceID := idOf(t, body)

	// Duplicate username and email are conflicts.
	if code, _ := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"Alice","password":"password1"}`); code != http.StatusConflict {
		t.Fatalf("dup username: want 409, got %d", code)
	}
	if code, _ := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"al2","email":"alice@example.com","password":"password1"}`); code != http.StatusConflict {
		t.Fatalf("dup email: want 409, got %d", code)
	}

	// Get / not found.
	if code, _ := cReq(t, admin, ts, "GET", "/api/v1/users/"+aliceID, ""); code != http.StatusOK {
		t.Fatalf("get alice: %d", code)
	}
	if code, _ := cReq(t, admin, ts, "GET", "/api/v1/users/nope", ""); code != http.StatusNotFound {
		t.Fatalf("get missing: want 404, got %d", code)
	}

	// Non-admins cannot manage users.
	alice := newClient(t)
	if login(t, alice, ts, "alice", "password1") != http.StatusOK {
		t.Fatalf("alice login failed")
	}
	if code, _ := cReq(t, alice, ts, "GET", "/api/v1/users", ""); code != http.StatusForbidden {
		t.Fatalf("alice list users: want 403, got %d", code)
	}
	if code, _ := cReq(t, alice, ts, "POST", "/api/v1/users", `{"username":"x","password":"password1"}`); code != http.StatusForbidden {
		t.Fatalf("alice create user: want 403, got %d", code)
	}
	// Every user-management route rejects a non-admin, not just list/create.
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/v1/users/" + aliceID, ""},
		{"PATCH", "/api/v1/users/" + aliceID, `{"displayName":"x"}`},
		{"POST", "/api/v1/users/" + aliceID + "/password", `{"password":"password1"}`},
		{"DELETE", "/api/v1/users/" + aliceID, ""},
	} {
		if code, _ := cReq(t, alice, ts, c.method, c.path, c.body); code != http.StatusForbidden {
			t.Fatalf("alice %s %s: want 403, got %d", c.method, c.path, code)
		}
	}
	// A malformed set-password body is a 400.
	if code, _ := cReq(t, admin, ts, "POST", "/api/v1/users/"+aliceID+"/password", `bad json`); code != http.StatusBadRequest {
		t.Fatalf("set password bad json: want 400, got %d", code)
	}

	// A second user, to test email-conflict on patch.
	code, cb := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"carol","email":"carol@example.com","password":"password1"}`)
	if code != http.StatusCreated {
		t.Fatalf("create carol: %d (%s)", code, cb)
	}
	// Patching alice to carol's email conflicts.
	if code, _ := cReq(t, admin, ts, "PATCH", "/api/v1/users/"+aliceID, `{"email":"carol@example.com"}`); code != http.StatusConflict {
		t.Fatalf("patch email conflict: want 409, got %d", code)
	}
	// Patching to the user's own (unchanged) email is fine, as is clearing it.
	if code, _ := cReq(t, admin, ts, "PATCH", "/api/v1/users/"+aliceID, `{"email":"alice@example.com"}`); code != http.StatusOK {
		t.Fatalf("patch same email: %d", code)
	}
	if code, _ := cReq(t, admin, ts, "PATCH", "/api/v1/users/"+aliceID, `{"email":""}`); code != http.StatusOK {
		t.Fatalf("patch clear email: %d", code)
	}
	if code, _ := cReq(t, admin, ts, "PATCH", "/api/v1/users/"+aliceID, `bad json`); code != http.StatusBadRequest {
		t.Fatalf("patch bad json: want 400, got %d", code)
	}

	// Patch: rename + promote to admin.
	if code, body := cReq(t, admin, ts, "PATCH", "/api/v1/users/"+aliceID,
		`{"displayName":"Alice A.","roles":["admin","user"]}`); code != http.StatusOK {
		t.Fatalf("patch alice: got %d (%s)", code, body)
	}

	// Set password, then log in with it.
	if code, _ := cReq(t, admin, ts, "POST", "/api/v1/users/"+aliceID+"/password", `{"password":"newpassword"}`); code != http.StatusOK {
		t.Fatalf("set password: %d", code)
	}
	if code, _ := cReq(t, admin, ts, "POST", "/api/v1/users/"+aliceID+"/password", `{"password":"short"}`); code != http.StatusBadRequest {
		t.Fatalf("short new password: want 400, got %d", code)
	}
	fresh := newClient(t)
	if login(t, fresh, ts, "alice", "newpassword") != http.StatusOK {
		t.Fatalf("login with new password failed")
	}
	if login(t, fresh, ts, "alice", "password1") != http.StatusUnauthorized {
		t.Fatalf("old password should no longer work")
	}

	// Delete alice (root is still admin, so no lockout).
	if code, _ := cReq(t, admin, ts, "DELETE", "/api/v1/users/"+aliceID, ""); code != http.StatusNoContent {
		t.Fatalf("delete alice: want 204, got %d", code)
	}
	if code, _ := cReq(t, admin, ts, "DELETE", "/api/v1/users/nope", ""); code != http.StatusNotFound {
		t.Fatalf("delete missing: want 404, got %d", code)
	}
}

func TestChangeOwnPassword(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatalf("admin login failed")
	}
	// Admin creates a regular user whose password that user will change itself.
	code, cu := cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"alice","password":"password1","roles":["user"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create alice: %d (%s)", code, cu)
	}

	const path = "/api/v1/auth/password"

	// Unauthenticated callers cannot change a password.
	if code, _ := cReq(t, newClient(t), ts, "POST", path, `{"currentPassword":"password1","newPassword":"password2"}`); code != http.StatusUnauthorized {
		t.Fatalf("change password without session: want 401, got %d", code)
	}

	alice := newClient(t)
	if login(t, alice, ts, "alice", "password1") != http.StatusOK {
		t.Fatalf("alice login failed")
	}

	// Wrong current password is refused (an open session alone must not suffice).
	if code, _ := cReq(t, alice, ts, "POST", path, `{"currentPassword":"nope","newPassword":"password2"}`); code != http.StatusForbidden {
		t.Fatalf("wrong current password: want 403, got %d", code)
	}
	// New password must meet the minimum length.
	if code, _ := cReq(t, alice, ts, "POST", path, `{"currentPassword":"password1","newPassword":"short"}`); code != http.StatusBadRequest {
		t.Fatalf("short new password: want 400, got %d", code)
	}
	// Malformed body is a 400.
	if code, _ := cReq(t, alice, ts, "POST", path, `not json`); code != http.StatusBadRequest {
		t.Fatalf("bad json: want 400, got %d", code)
	}

	// A password over bcrypt's 72-byte input limit can't be hashed → 500, and the
	// hashing happens before the current-password check so it surfaces regardless.
	if code, _ := cReq(t, alice, ts, "POST", path,
		`{"currentPassword":"password1","newPassword":"`+strings.Repeat("a", 73)+`"}`); code != http.StatusInternalServerError {
		t.Fatalf("over-long password: want 500, got %d", code)
	}

	// A valid change succeeds and keeps the current session alive.
	if code, _ := cReq(t, alice, ts, "POST", path, `{"currentPassword":"password1","newPassword":"password2"}`); code != http.StatusOK {
		t.Fatalf("valid change: want 200, got %d", code)
	}
	if code, _ := cReq(t, alice, ts, "GET", "/api/v1/auth/me", ""); code != http.StatusOK {
		t.Fatalf("session should survive a password change: got %d", code)
	}

	// The old password no longer works; the new one does.
	if login(t, newClient(t), ts, "alice", "password1") != http.StatusUnauthorized {
		t.Fatalf("old password should no longer log in")
	}
	if login(t, newClient(t), ts, "alice", "password2") != http.StatusOK {
		t.Fatalf("new password should log in")
	}
}

func TestChangePasswordAuthOff(t *testing.T) {
	// In single-user mode (auth off) the route is reachable but there is no
	// principal, so changing a password is meaningless: expect 401.
	ts := newTestServer(t)
	if code, _ := cReq(t, newClient(t), ts, "POST", "/api/v1/auth/password",
		`{"currentPassword":"x","newPassword":"password2"}`); code != http.StatusUnauthorized {
		t.Fatalf("change password with auth off: want 401, got %d", code)
	}
}

func TestLastAdminGuards(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	login(t, admin, ts, "root", "rootpassword")

	// root is the only admin. Find its id.
	_, body := cReq(t, admin, ts, "GET", "/api/v1/users", "")
	var users []map[string]any
	_ = json.Unmarshal(body, &users)
	rootID, _ := users[0]["id"].(string)

	// Cannot delete the last admin.
	if code, _ := cReq(t, admin, ts, "DELETE", "/api/v1/users/"+rootID, ""); code != http.StatusConflict {
		t.Fatalf("delete last admin: want 409, got %d", code)
	}
	// Cannot strip admin from the last admin.
	if code, _ := cReq(t, admin, ts, "PATCH", "/api/v1/users/"+rootID, `{"roles":["user"]}`); code != http.StatusConflict {
		t.Fatalf("de-admin last admin: want 409, got %d", code)
	}
	// Cannot disable the last admin.
	if code, _ := cReq(t, admin, ts, "PATCH", "/api/v1/users/"+rootID, `{"disabled":true}`); code != http.StatusConflict {
		t.Fatalf("disable last admin: want 409, got %d", code)
	}
	// After a second admin exists, disabling one is fine and ends its sessions.
	code, cu := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"bob","password":"password1","roles":["admin"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create bob: %d (%s)", code, cu)
	}
	bobID := idOf(t, cu)
	bob := newClient(t)
	if login(t, bob, ts, "bob", "password1") != http.StatusOK {
		t.Fatalf("bob login failed")
	}
	if code, _ := cReq(t, admin, ts, "PATCH", "/api/v1/users/"+bobID, `{"disabled":true}`); code != http.StatusOK {
		t.Fatalf("disable bob: %d", code)
	}
	// Bob's live session is now invalid.
	if code, _ := cReq(t, bob, ts, "GET", "/api/v1/auth/me", ""); code != http.StatusUnauthorized {
		t.Fatalf("disabled user session should be dead: got %d", code)
	}
	// A disabled user cannot log in.
	if login(t, newClient(t), ts, "bob", "password1") != http.StatusUnauthorized {
		t.Fatalf("disabled user login should be 401")
	}
	// Patch of a missing user is 404.
	if code, _ := cReq(t, admin, ts, "PATCH", "/api/v1/users/nope", `{"displayName":"x"}`); code != http.StatusNotFound {
		t.Fatalf("patch missing: want 404, got %d", code)
	}
	// Set password of a missing user is 404.
	if code, _ := cReq(t, admin, ts, "POST", "/api/v1/users/nope/password", `{"password":"password1"}`); code != http.StatusNotFound {
		t.Fatalf("set password missing: want 404, got %d", code)
	}
}
