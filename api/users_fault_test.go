package api_test

import (
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// corruptUserFile overwrites a user's on-disk record with garbage so any handler
// that reads it surfaces a 500 — the internal-error branches that are otherwise
// unreachable over HTTP.
func corruptUserFile(t *testing.T, dataDir, id string) {
	t.Helper()
	p := filepath.Join(dataDir, "users", hex.EncodeToString([]byte(id))+".json")
	if err := os.WriteFile(p, []byte("{ not json"), 0o644); err != nil {
		t.Fatalf("corrupt user file: %v", err)
	}
}

func TestUserHandlerStoreErrors(t *testing.T) {
	ts, dir := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatalf("admin login failed")
	}

	// A malformed login body is a 400 (unmarshal branch).
	if code, _ := cReq(t, admin, ts, "POST", "/api/v1/auth/login", `not json`); code != http.StatusBadRequest {
		t.Fatalf("malformed login: want 400, got %d", code)
	}

	// Create a user, log in as them, then corrupt their record on disk.
	_, cu := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"alice","password":"password1"}`)
	aliceID := idOf(t, cu)
	alice := newClient(t)
	if login(t, alice, ts, "alice", "password1") != http.StatusOK {
		t.Fatalf("alice login failed")
	}
	corruptUserFile(t, dir, aliceID)

	// Every read path over the corrupted record now 500s.
	checks := []struct {
		name, method, path, body string
	}{
		{"list", "GET", "/api/v1/users", ""},
		{"get", "GET", "/api/v1/users/" + aliceID, ""},
		{"patch", "PATCH", "/api/v1/users/" + aliceID, `{"displayName":"x"}`},
		{"password", "POST", "/api/v1/users/" + aliceID + "/password", `{"password":"password1"}`},
		{"delete", "DELETE", "/api/v1/users/" + aliceID, ""},
		{"create", "POST", "/api/v1/users", `{"username":"bob","password":"password1"}`},
	}
	for _, c := range checks {
		if code, _ := cReq(t, admin, ts, c.method, c.path, c.body); code != http.StatusInternalServerError {
			t.Fatalf("%s over corrupt store: want 500, got %d", c.name, code)
		}
	}

	// Login and /me over the corrupted record also 500 (username lookup / self read).
	if login(t, newClient(t), ts, "alice", "password1") != http.StatusInternalServerError {
		t.Fatalf("login over corrupt store: want 500")
	}
	if code, _ := cReq(t, alice, ts, "GET", "/api/v1/auth/me", ""); code != http.StatusInternalServerError {
		t.Fatalf("me over corrupt store: want 500, got %d", code)
	}
}

// TestMeAfterUserRemoved covers the branch where a session outlives its user
// record: /me then reports authenticated-but-no-user rather than 500.
func TestMeAfterUserRemoved(t *testing.T) {
	ts, dir := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	login(t, admin, ts, "root", "rootpassword")
	_, cu := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"carol","password":"password1"}`)
	carolID := idOf(t, cu)
	carol := newClient(t)
	if login(t, carol, ts, "carol", "password1") != http.StatusOK {
		t.Fatalf("carol login failed")
	}
	// Remove the record out from under the live session.
	p := filepath.Join(dir, "users", hex.EncodeToString([]byte(carolID))+".json")
	if err := os.Remove(p); err != nil {
		t.Fatalf("remove: %v", err)
	}
	code, body := cReq(t, carol, ts, "GET", "/api/v1/auth/me", "")
	if code != http.StatusOK {
		t.Fatalf("me after removal: want 200, got %d", code)
	}
	if string(body) == "" || !containsNullUser(body) {
		t.Fatalf("me after removal should report user null: %s", body)
	}
}

func containsNullUser(body []byte) bool {
	return string(body) != "" && (jsonUserIsNull(body))
}

func jsonUserIsNull(body []byte) bool {
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return false
	}
	v, ok := m["user"]
	return ok && v == nil
}

// TestLockoutGuardStoreErrors covers the store-error branch inside the last-admin
// lockout guard, reached when the target reads fine but the roster scan hits a
// corrupt record.
func TestLockoutGuardStoreErrors(t *testing.T) {
	ts, dir := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	login(t, admin, ts, "root", "rootpassword")
	// A second admin so the guard actually runs the roster scan.
	_, cb := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"bob","password":"password1","roles":["admin"]}`)
	bobID := idOf(t, cb)
	// A third record, corrupted, that the roster scan will trip over.
	_, cc := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"carol","password":"password1"}`)
	corruptUserFile(t, dir, idOf(t, cc))

	// De-admining bob makes the guard scan the roster -> corrupt -> 500.
	if code, _ := cReq(t, admin, ts, "PATCH", "/api/v1/users/"+bobID, `{"roles":["user"]}`); code != http.StatusInternalServerError {
		t.Fatalf("patch lockout scan error: want 500, got %d", code)
	}
	// Deleting bob (an enabled admin) does the same.
	if code, _ := cReq(t, admin, ts, "DELETE", "/api/v1/users/"+bobID, ""); code != http.StatusInternalServerError {
		t.Fatalf("delete lockout scan error: want 500, got %d", code)
	}
}

// TestPatchEmailLookupError covers the patch path where the target reads fine but
// the uniqueness scan hits a corrupt record elsewhere in the store.
func TestPatchEmailLookupError(t *testing.T) {
	ts, dir := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	login(t, admin, ts, "root", "rootpassword")
	_, ca := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"alice","password":"password1"}`)
	aliceID := idOf(t, ca)
	_, cb := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"bob","password":"password1"}`)
	bobID := idOf(t, cb)
	// Corrupt bob so the email scan fails while alice's own record still reads.
	corruptUserFile(t, dir, bobID)
	if code, _ := cReq(t, admin, ts, "PATCH", "/api/v1/users/"+aliceID, `{"email":"new@example.com"}`); code != http.StatusInternalServerError {
		t.Fatalf("patch with email scan error: want 500, got %d", code)
	}
}
