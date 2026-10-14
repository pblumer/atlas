package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestRoleRevocationTakesEffectOnLiveSession: an administrative role change must
// be enforced from the next request, not from the next login. A session
// snapshots its roles at login (ADR-0044); until this was pushed, a PATCH that
// answered 200 and showed a demoted account in the console left that account
// running as an admin for the rest of its twelve-hour session.
func TestRoleRevocationTakesEffectOnLiveSession(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	if login(t, admin, ts, "admin", "password1") != http.StatusOK {
		t.Fatal("admin login")
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"reviewer","password":"password1","roles":["admin"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create user: %d %s", code, body)
	}
	var user struct{ ID string }
	if err := json.Unmarshal(body, &user); err != nil {
		t.Fatalf("decode user: %v (%s)", err, body)
	}

	other := newClient(t)
	if login(t, other, ts, "reviewer", "password1") != http.StatusOK {
		t.Fatal("reviewer login")
	}
	// Fixture: the session really does hold admin right now.
	if code, _ := cReq(t, other, ts, "GET", "/api/v1/users", ""); code != http.StatusOK {
		t.Fatalf("fixture: reviewer is not admin to begin with: %d", code)
	}

	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/users/"+user.ID, `{"roles":["user"]}`); code != http.StatusOK {
		t.Fatalf("downgrade: %d %s", code, b)
	}
	if code, b := cReq(t, other, ts, "GET", "/api/v1/users", ""); code != http.StatusForbidden && code != http.StatusUnauthorized {
		t.Fatalf("revoked admin session still reaches an admin endpoint: %d %s", code, b)
	}
	// Demotion narrows the session; it does not end it. The account keeps working
	// at its new level, which is the difference between a revocation and a logout.
	if code, b := cReq(t, other, ts, "GET", "/api/v1/auth/me", ""); code != http.StatusOK {
		t.Fatalf("demotion logged the account out: %d %s", code, b)
	}

	// A grant is live in the same way, so the two directions cannot drift apart.
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/users/"+user.ID, `{"roles":["admin"]}`); code != http.StatusOK {
		t.Fatalf("re-promote: %d %s", code, b)
	}
	if code, b := cReq(t, other, ts, "GET", "/api/v1/users", ""); code != http.StatusOK {
		t.Fatalf("restored admin not effective on the live session: %d %s", code, b)
	}
}
