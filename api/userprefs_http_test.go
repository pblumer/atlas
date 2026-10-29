package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The display preferences over the real server (ADR-0459):
// the route is wired, the identity comes from the session, and an instance with no
// logins answers that there is no such resource rather than inventing a shared one.

// prefsOf reads the signed-in client's preferences.
func prefsOf(t *testing.T, c *http.Client, ts *httptest.Server) map[string]any {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", "/api/v1/me/preferences", "")
	if code != http.StatusOK {
		t.Fatalf("GET preferences = %d: %s", code, body)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return got
}

// signIn logs a client in as an account the admin has just created.
func signIn(t *testing.T, ts *httptest.Server, user, pass string) *http.Client {
	t.Helper()
	c := newClient(t)
	code, body := cReq(t, c, ts, "POST", "/api/v1/auth/login",
		fmt.Sprintf(`{"username":%q,"password":%q}`, user, pass))
	if code != http.StatusOK {
		t.Fatalf("login as %s = %d: %s", user, code, body)
	}
	return c
}

// TestDisplayPreferencesBelongToThePersonSignedIn is the whole reason this moved
// out of the browser: the setting follows the account, and two accounts on one
// instance do not share one.
func TestDisplayPreferencesBelongToThePersonSignedIn(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := signIn(t, ts, "root", "rootpassword")

	// A second account, so "per user" is asserted against somebody.
	if code, body := cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"anna","password":"annapassword123","roles":["user"]}`); code != http.StatusCreated {
		t.Fatalf("create user = %d: %s", code, body)
	}
	anna := signIn(t, ts, "anna", "annapassword123")

	// Nothing stored yet is the unset record, not a 404: that is the state every
	// account starts in and it means "the defaults".
	if got := prefsOf(t, anna, ts)["region"]; got != "" {
		t.Fatalf("a fresh account reads region %v, want it unset", got)
	}

	code, body := cReq(t, anna, ts, "PUT", "/api/v1/me/preferences",
		`{"region":"ch","timeZone":"Europe/Zurich","language":"de","appearance":"dark"}`)
	if code != http.StatusOK {
		t.Fatalf("PUT preferences = %d: %s", code, body)
	}

	got := prefsOf(t, anna, ts)
	if got["region"] != "ch" || got["timeZone"] != "Europe/Zurich" || got["language"] != "de" || got["appearance"] != "dark" {
		t.Fatalf("anna reads %v, want what she saved", got)
	}
	if got := prefsOf(t, admin, ts); got["region"] != "" || got["appearance"] != "" {
		t.Fatalf("root reads %v, want anna's choices to be hers alone", got)
	}

	// A region nobody offers is refused at the boundary, because the browser is
	// what formats with it: stored, it would come back as a broken screen.
	if code, _ := cReq(t, anna, ts, "PUT", "/api/v1/me/preferences", `{"region":"fr"}`); code != http.StatusBadRequest {
		t.Fatalf("PUT an unknown region = %d, want 400", code)
	}
	// So is an appearance the stylesheet does not draw (ADR-0507).
	if code, _ := cReq(t, anna, ts, "PUT", "/api/v1/me/preferences", `{"appearance":"sepia"}`); code != http.StatusBadRequest {
		t.Fatalf("PUT an unknown appearance = %d, want 400", code)
	}
}

// TestDisplayPreferencesHaveNoOwnerWithoutAuthentication: with no logins there is
// no "me", so the resource does not exist. The console reads that as "keep these
// in this browser" — which is why it must be a plain 404 rather than a 401 that
// would send somebody looking for a sign-in page this server does not have.
func TestDisplayPreferencesHaveNoOwnerWithoutAuthentication(t *testing.T) {
	ts := newTestServer(t)
	for _, c := range []struct{ method, body string }{
		{"GET", ""},
		{"PUT", `{"region":"ch"}`},
	} {
		if code, body := doReq(t, ts, c.method, "/api/v1/me/preferences", c.body, "application/json"); code != http.StatusNotFound {
			t.Errorf("%s preferences without auth = %d (%s), want 404", c.method, code, body)
		}
	}
}
