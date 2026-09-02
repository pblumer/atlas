package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Presence over the wire (ADR-0228): what the Organization page
// reads, who may read it, and what it says about somebody who has not logged in.

// presenceOf decodes the presence list and returns the entry for a user id.
func presenceOf(t *testing.T, body []byte, userID string) map[string]any {
	t.Helper()
	var list []map[string]any
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode presence: %v (%s)", err, body)
	}
	for _, p := range list {
		if p["userId"] == userID {
			return p
		}
	}
	return nil
}

// meID reads the signed-in caller's own user id from /auth/me.
func meID(t *testing.T, c *http.Client, ts *httptest.Server) string {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/auth/me", "")
	if code != http.StatusOK {
		t.Fatalf("me: got %d (%s)", code, body)
	}
	var m struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode me: %v (%s)", err, body)
	}
	if m.User.ID == "" {
		t.Fatalf("no signed-in user in %s", body)
	}
	return m.User.ID
}

func TestPresenceShowsWhoIsSignedIn(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}

	// A second account that never signs in: it exists, and nobody is behind it.
	code, created := cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"alice","password":"password1","roles":["user"]}`)
	if code != http.StatusCreated {
		t.Fatalf("create alice: got %d (%s)", code, created)
	}
	alice := idOf(t, created)

	code, body := cReq(t, admin, ts, "GET", "/api/v1/users/presence", "")
	if code != http.StatusOK {
		t.Fatalf("presence: got %d (%s)", code, body)
	}
	if p := presenceOf(t, body, alice); p != nil {
		t.Errorf("alice never logged in and has presence: %v", p)
	}
	me := presenceOf(t, body, meID(t, admin, ts))
	if me == nil {
		t.Fatalf("the admin reading this is signed in and absent from %s", body)
	}
	if me["state"] != "online" {
		t.Errorf("state right after login = %v, want online", me["state"])
	}
	if me["sessions"] != float64(1) {
		t.Errorf("sessions = %v, want 1", me["sessions"])
	}

	// Alice signs in from her own browser: two accounts present, two sessions.
	aliceClient := newClient(t)
	if login(t, aliceClient, ts, "alice", "password1") != http.StatusOK {
		t.Fatal("alice login failed")
	}
	_, body = cReq(t, admin, ts, "GET", "/api/v1/users/presence", "")
	if p := presenceOf(t, body, alice); p == nil || p["state"] != "online" {
		t.Errorf("alice is signed in; presence = %v", p)
	}

	// And the roster carries the same answer, so the page's first paint is right.
	code, roster := cReq(t, admin, ts, "GET", "/api/v1/users", "")
	if code != http.StatusOK {
		t.Fatalf("roster: got %d (%s)", code, roster)
	}
	var users []struct {
		ID       string `json:"id"`
		Presence struct {
			State    string `json:"state"`
			Sessions int    `json:"sessions"`
		} `json:"presence"`
	}
	if err := json.Unmarshal(roster, &users); err != nil {
		t.Fatalf("decode roster: %v (%s)", err, roster)
	}
	seen := 0
	for _, u := range users {
		if u.Presence.State == "" {
			t.Errorf("user %s carries no presence state", u.ID)
		}
		if u.ID == alice && u.Presence.State != "online" {
			t.Errorf("alice in the roster = %q, want online", u.Presence.State)
		}
		seen++
	}
	if seen < 2 {
		t.Fatalf("roster has %d users, want at least 2", seen)
	}

	// Logging out is the one thing that makes somebody offline at once, rather
	// than after a window of silence.
	if code, _ := cReq(t, aliceClient, ts, "POST", "/api/v1/auth/logout", ""); code != http.StatusOK {
		t.Fatal("alice logout failed")
	}
	_, body = cReq(t, admin, ts, "GET", "/api/v1/users/presence", "")
	if p := presenceOf(t, body, alice); p != nil {
		t.Errorf("alice logged out and is still present: %v", p)
	}
}

func TestPresenceBeaconIsForEverybodyAndTheListIsNot(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	if code, body := cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"alice","password":"password1","roles":["user"]}`); code != http.StatusCreated {
		t.Fatalf("create alice: got %d (%s)", code, body)
	}
	alice := newClient(t)
	if login(t, alice, ts, "alice", "password1") != http.StatusOK {
		t.Fatal("alice login failed")
	}

	// Anybody signed in may say they are here — the beacon reports on nobody but
	// its own caller.
	for _, body := range []string{`{"active":true}`, `{"active":false}`, ""} {
		if code, resp := cReq(t, alice, ts, "POST", "/api/v1/auth/presence", body); code != http.StatusOK {
			t.Fatalf("beacon %q: got %d (%s)", body, code, resp)
		}
	}
	if code, _ := cReq(t, alice, ts, "POST", "/api/v1/auth/presence", `{"active":`); code != http.StatusBadRequest {
		t.Error("a malformed beacon body should be a 400")
	}

	// Reading who else is here is administration.
	if code, _ := cReq(t, alice, ts, "GET", "/api/v1/users/presence", ""); code != http.StatusForbidden {
		t.Error("a non-admin should not see who is signed in")
	}
	if code, _ := cReq(t, newClient(t), ts, "GET", "/api/v1/users/presence", ""); code != http.StatusUnauthorized {
		t.Error("presence without a session should be a 401")
	}
	// And the beacon is not a way in either.
	if code, _ := cReq(t, newClient(t), ts, "POST", "/api/v1/auth/presence", `{"active":true}`); code != http.StatusUnauthorized {
		t.Error("beacon without a session should be a 401")
	}
}
