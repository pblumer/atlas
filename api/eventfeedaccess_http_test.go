package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The event feed's own role and token scope
// (ADR-draft-the-event-feed-has-its-own-role-and-token-scope).
//
// A system that follows the feed — a CMDB, a billing system — holds a credential on
// another host for as long as the integration runs. The credential it is given must
// read the feed and nothing else: not deploy, not start an instance, not read what a
// person is working on. The scope says which route; the role says which kind of act;
// both name the feed.

// TestAnEventsTokenReadsTheFeedAndNothingElse: a token minted with the events scope
// reads the feed and is refused everywhere else, by its scope; a full token, which
// carries the minter's non-admin roles and not feedreader, is refused the feed by
// the role; and an events token cannot be narrowed by a reach the route never reads.
func TestAnEventsTokenReadsTheFeedAndNothingElse(t *testing.T) {
	ts, admin := apiTokenServer(t)
	secret, _ := mint(t, admin, ts, `{"name":"cmdb","scope":"events","expiresInDays":365}`)

	code, body := bearerReq(t, ts, http.MethodGet, "/api/v1/events", "", secret)
	if code != http.StatusOK || !strings.Contains(string(body), `"events"`) {
		t.Fatalf("an events token reading the feed = %d (%s), want 200 and a page", code, body)
	}
	for _, path := range []string{"/api/v1/processes", "/api/v1/instances", "/api/v1/orders", "/api/v1/node", "/api/v1/inventory"} {
		if code, _ := bearerReq(t, ts, http.MethodGet, path, "", secret); code != http.StatusForbidden {
			t.Errorf("events token on GET %s = %d, want 403", path, code)
		}
	}
	if code, _ := bearerReq(t, ts, http.MethodPost, "/api/v1/instances", `{"processId":"x"}`, secret); code != http.StatusForbidden {
		t.Errorf("events token starting an instance = %d, want 403", code)
	}

	full, _ := mint(t, admin, ts, `{"name":"ci","scope":"full","expiresInDays":30}`)
	code, body = bearerReq(t, ts, http.MethodGet, "/api/v1/events", "", full)
	if code != http.StatusForbidden || !strings.Contains(string(body), "feedreader") {
		t.Errorf("a full token reading the feed = %d (%s), want 403 naming feedreader", code, body)
	}

	if code, body := cReq(t, admin, ts, "POST", "/api/v1/api-tokens",
		`{"name":"cmdb","scope":"events","reach":["anything"]}`); code != http.StatusBadRequest ||
		!strings.Contains(string(body), "reach does not narrow it") {
		t.Errorf("an events token with a reach = %d (%s), want 400", code, body)
	}

	// The token's record says what it carries, so an administrator reading the list
	// sees a credential that holds feedreader and nothing more.
	code, body = cReq(t, admin, ts, "GET", "/api/v1/api-tokens", "")
	var list []struct {
		Name  string   `json:"name"`
		Scope string   `json:"scope"`
		Roles []string `json:"roles"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &list) != nil {
		t.Fatalf("list tokens: %d (%s)", code, body)
	}
	for _, tok := range list {
		if tok.Scope == "events" && (len(tok.Roles) != 1 || tok.Roles[0] != "feedreader") {
			t.Errorf("the events token carries %v, want [feedreader]", tok.Roles)
		}
	}
}

// TestTheFeedIsReadByTheFeedReaderRole: an operator who starts and repairs instances
// is not thereby a reader of who holds what across every catalogue; an account given
// feedreader is, and an administrator reaches it as it reaches everything.
func TestTheFeedIsReadByTheFeedReaderRole(t *testing.T) {
	ts, admin := apiTokenServer(t)
	createUserWithRoles(t, admin, ts.URL, "otto", `["operator","user"]`)
	createUserWithRoles(t, admin, ts.URL, "fred", `["feedreader"]`)
	otto := signInAs(t, ts.URL, "otto", "a-password-that-is-long")
	fred := signInAs(t, ts.URL, "fred", "a-password-that-is-long")

	if code, body := cReq(t, otto, ts, "GET", "/api/v1/events", ""); code != http.StatusForbidden ||
		!strings.Contains(string(body), "feedreader") {
		t.Errorf("an operator reading the feed = %d (%s), want 403 naming feedreader", code, body)
	}
	if code, _ := cReq(t, fred, ts, "GET", "/api/v1/events", ""); code != http.StatusOK {
		t.Errorf("a feed reader reading the feed = %d, want 200", code)
	}
	if code, _ := cReq(t, fred, ts, "GET", "/api/v1/incidents", ""); code != http.StatusForbidden {
		t.Errorf("a feed reader listing incidents = %d, want 403", code)
	}
	if code, _ := cReq(t, admin, ts, "GET", "/api/v1/events", ""); code != http.StatusOK {
		t.Errorf("an administrator reading the feed = %d, want 200", code)
	}
}
