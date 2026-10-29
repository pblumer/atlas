package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// meGroups is GET /api/v1/auth/me's user with the groups it is a member of.
type meGroups struct {
	User *struct {
		ID     string `json:"id"`
		Groups []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"groups"`
	} `json:"user"`
}

func decodeMeGroups(t *testing.T, b []byte) meGroups {
	t.Helper()
	var m meGroups
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("decode me: %v (%s)", err, b)
	}
	if m.User == nil {
		t.Fatalf("me answered no user: %s", b)
	}
	return m
}

// TestAuthMeAnswersOnesOwnGroups: who am I answers the groups the caller is a member
// of, by id and name, as the group store holds them at the moment of the request — a
// connected system that shows a person the tasks offered to their groups learns them
// from the person's own credential (ADR-0510 §4). Only
// the caller's own: a group the caller is not in is not named, and no group's other
// members are.
func TestAuthMeAnswersOnesOwnGroups(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	_, ab := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"alice","password":"password1","roles":["user"]}`)
	aliceID := idOf(t, ab)
	_, bb := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"bob","password":"password1","roles":["user"]}`)
	bobID := idOf(t, bb)

	alice := newClient(t)
	login(t, alice, ts, "alice", "password1")

	// No group yet: an empty list, never null, so a client can rely on the field.
	code, body := cReq(t, alice, ts, "GET", "/api/v1/auth/me", "")
	if code != http.StatusOK {
		t.Fatalf("me = %d %s", code, body)
	}
	var raw struct {
		User map[string]json.RawMessage `json:"user"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(raw.User["groups"]) != "[]" {
		t.Fatalf("groups of a member of none = %s, want []", raw.User["groups"])
	}

	_, hr := cReq(t, admin, ts, "POST", "/api/v1/groups", `{"name":"HR Approvers"}`)
	hrID := decodeGroup(t, hr).ID
	_, fin := cReq(t, admin, ts, "POST", "/api/v1/groups", `{"name":"Finance"}`)
	finID := decodeGroup(t, fin).ID
	for _, add := range []string{"/api/v1/groups/" + hrID + "/members/" + aliceID,
		"/api/v1/groups/" + hrID + "/members/" + bobID,
		"/api/v1/groups/" + finID + "/members/" + bobID} {
		if code, b := cReq(t, admin, ts, "PUT", add, ""); code != http.StatusOK {
			t.Fatalf("PUT %s = %d %s", add, code, b)
		}
	}

	// The membership added after alice signed in is answered: the store at the
	// moment of the request, not a snapshot of the login.
	_, body = cReq(t, alice, ts, "GET", "/api/v1/auth/me", "")
	me := decodeMeGroups(t, body)
	if me.User.ID != aliceID {
		t.Fatalf("me is %s, want alice %s", me.User.ID, aliceID)
	}
	if len(me.User.Groups) != 1 || me.User.Groups[0].ID != hrID || me.User.Groups[0].Name != "HR Approvers" {
		t.Fatalf("alice's groups = %+v, want only HR Approvers (%s)", me.User.Groups, hrID)
	}
	var whole map[string]map[string]any
	_ = json.Unmarshal(body, &whole)
	if g, _ := whole["user"]["groups"].([]any); len(g) == 1 {
		if _, leaked := g[0].(map[string]any)["members"]; leaked {
			t.Fatalf("a group in me carries its members: %s", body)
		}
	}

	// A group renamed is answered by its new name.
	if code, _ := cReq(t, admin, ts, "PATCH", "/api/v1/groups/"+hrID, `{"name":"HR"}`); code != http.StatusOK {
		t.Fatalf("rename = %d", code)
	}
	_, body = cReq(t, alice, ts, "GET", "/api/v1/auth/me", "")
	if g := decodeMeGroups(t, body).User.Groups; len(g) != 1 || g[0].Name != "HR" {
		t.Fatalf("after the rename alice's groups = %+v, want HR", g)
	}

	// The roster of users does not grow the field: it is who am I's alone.
	_, list := cReq(t, admin, ts, "GET", "/api/v1/users", "")
	var users []map[string]any
	if err := json.Unmarshal(list, &users); err != nil {
		t.Fatalf("decode users: %v (%s)", err, list)
	}
	for _, u := range users {
		if _, there := u["groups"]; there {
			t.Fatalf("the user roster carries groups: %v", u)
		}
	}
}
