package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// principalEntry mirrors the { type, id, name } shape of the principals
// directory (ADR-0073).
type principalEntry struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

// TestPrincipalsDirectory checks the id-referenced directory: a non-admin may
// read it, it returns enabled users as { type, id, name }, excludes disabled
// users, and leaks none of the management-only fields (ADR-0073).
func TestPrincipalsDirectory(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")

	admin := newClient(t)
	if login(t, admin, ts, "admin", "password1") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	_, ab := cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"alice","password":"password1","displayName":"Alice Ng","email":"alice@example.com"}`)
	aliceID := idOf(t, ab)
	_, bb := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"bob","password":"password1"}`)
	bobID := idOf(t, bb)
	// Disable bob: he must drop out of the directory.
	if code, _ := cReq(t, admin, ts, "PATCH", "/api/v1/users/"+bobID, `{"disabled":true}`); code != http.StatusOK {
		t.Fatalf("disable bob: %d", code)
	}

	// A non-admin can read the directory (it is not admin-gated).
	alice := newClient(t)
	if login(t, alice, ts, "alice", "password1") != http.StatusOK {
		t.Fatal("alice login failed")
	}
	code, body := cReq(t, alice, ts, "GET", "/api/v1/principals", "")
	if code != http.StatusOK {
		t.Fatalf("principals as non-admin: %d %s", code, body)
	}
	var list []principalEntry
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}

	byID := map[string]principalEntry{}
	for _, e := range list {
		byID[e.ID] = e
	}
	if e, ok := byID[aliceID]; !ok || e.Type != "user" || e.Name != "Alice Ng" {
		t.Fatalf("alice entry = %+v (ok=%v), want type=user name=Alice Ng", e, ok)
	}
	if _, ok := byID["admin-missing"]; ok { /* placeholder */
	}
	if _, ok := byID[bobID]; ok {
		t.Fatalf("disabled bob must not appear: %s", body)
	}
	// admin (enabled) is present; the directory lists every enabled user.
	if len(list) != 2 {
		t.Fatalf("directory size = %d, want 2 (admin + alice); body=%s", len(list), body)
	}
	// No management-only field leaks through the minimal projection.
	for _, bad := range []string{"passwordHash", "roles", "email", "disabled", "source", "username"} {
		if strings.Contains(string(body), bad) {
			t.Fatalf("principals response leaks %q: %s", bad, body)
		}
	}
}

// TestPrincipalsRequiresSession checks that under --auth an unauthenticated
// caller is rejected by the standard middleware (the directory is not on the
// pre-login exemption list).
func TestPrincipalsRequiresSession(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	anon := newClient(t)
	if code, _ := cReq(t, anon, ts, "GET", "/api/v1/principals", ""); code != http.StatusUnauthorized {
		t.Fatalf("anonymous principals: %d, want 401", code)
	}
}

// TestPrincipalsOpenWhenAuthOff checks that with auth disabled the endpoint is
// open like every other read and returns an (empty) directory.
func TestPrincipalsOpenWhenAuthOff(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/principals", "", "")
	if code != http.StatusOK || strings.TrimSpace(string(body)) != "[]" {
		t.Fatalf("principals (auth off) = %d %s, want 200 []", code, body)
	}
}
