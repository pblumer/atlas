package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

type groupJSON struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

func decodeGroup(t *testing.T, b []byte) groupJSON {
	t.Helper()
	var g groupJSON
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatalf("decode group: %v (%s)", err, b)
	}
	return g
}

// TestGroupsCRUD covers the admin-gated group management surface
// (ADR-draft-groups-as-members): non-admins are refused, names are unique, and
// members must be real users.
func TestGroupsCRUD(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	_, ab := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"alice","password":"password1"}`)
	aliceID := idOf(t, ab)

	// A non-admin cannot manage groups.
	alice := newClient(t)
	login(t, alice, ts, "alice", "password1")
	if code, _ := cReq(t, alice, ts, "GET", "/api/v1/groups", ""); code != http.StatusForbidden {
		t.Fatalf("non-admin list groups = %d, want 403", code)
	}
	if code, _ := cReq(t, alice, ts, "POST", "/api/v1/groups", `{"name":"x"}`); code != http.StatusForbidden {
		t.Fatalf("non-admin create group = %d, want 403", code)
	}

	// Admin creates a group; a duplicate name (case-insensitive) is refused.
	code, gb := cReq(t, admin, ts, "POST", "/api/v1/groups", `{"name":"Engineering"}`)
	if code != http.StatusCreated {
		t.Fatalf("create group = %d %s", code, gb)
	}
	gid := decodeGroup(t, gb).ID
	if code, _ := cReq(t, admin, ts, "POST", "/api/v1/groups", `{"name":"engineering"}`); code != http.StatusConflict {
		t.Fatalf("duplicate name = %d, want 409", code)
	}
	if code, _ := cReq(t, admin, ts, "POST", "/api/v1/groups", `{"name":"  "}`); code != http.StatusBadRequest {
		t.Fatalf("blank name = %d, want 400", code)
	}

	// Rename.
	if code, _ := cReq(t, admin, ts, "PATCH", "/api/v1/groups/"+gid, `{"name":"Eng"}`); code != http.StatusOK {
		t.Fatalf("rename = %d, want 200", code)
	}

	// Members: an unknown user is refused; a real user is added and removed.
	if code, _ := cReq(t, admin, ts, "PUT", "/api/v1/groups/"+gid+"/members/usr_ghost", ""); code != http.StatusBadRequest {
		t.Fatalf("add unknown user = %d, want 400", code)
	}
	code, mb := cReq(t, admin, ts, "PUT", "/api/v1/groups/"+gid+"/members/"+aliceID, "")
	if code != http.StatusOK {
		t.Fatalf("add member = %d %s", code, mb)
	}
	if m := decodeGroup(t, mb).Members; len(m) != 1 || m[0] != aliceID {
		t.Fatalf("members after add = %v, want [%s]", m, aliceID)
	}
	code, rb := cReq(t, admin, ts, "DELETE", "/api/v1/groups/"+gid+"/members/"+aliceID, "")
	if code != http.StatusOK || len(decodeGroup(t, rb).Members) != 0 {
		t.Fatalf("remove member = %d %s, want 200 empty", code, rb)
	}

	// List shows the group; delete is idempotent.
	if _, lb := cReq(t, admin, ts, "GET", "/api/v1/groups", ""); true {
		var list []groupJSON
		_ = json.Unmarshal(lb, &list)
		if len(list) != 1 || list[0].ID != gid {
			t.Fatalf("list groups = %s, want the one group", lb)
		}
	}
	if code, _ := cReq(t, admin, ts, "DELETE", "/api/v1/groups/"+gid, ""); code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", code)
	}
	if code, _ := cReq(t, admin, ts, "DELETE", "/api/v1/groups/"+gid, ""); code != http.StatusNoContent {
		t.Fatalf("delete again = %d, want 204 (idempotent)", code)
	}
}

// TestGroupsCRUDBranches covers the not-found, forbidden, conflict, and
// idempotent branches of the group handlers.
func TestGroupsCRUDBranches(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	_, ab := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"alice","password":"password1"}`)
	aliceID := idOf(t, ab)
	alice := newClient(t)
	login(t, alice, ts, "alice", "password1")

	_, gb := cReq(t, admin, ts, "POST", "/api/v1/groups", `{"name":"Eng"}`)
	gid := decodeGroup(t, gb).ID

	// 404 on a missing group across the by-id handlers.
	for _, op := range []struct{ method, path, body string }{
		{"PATCH", "/api/v1/groups/grp_missing", `{"name":"z"}`},
		{"PUT", "/api/v1/groups/grp_missing/members/" + aliceID, ""},
		{"DELETE", "/api/v1/groups/grp_missing/members/" + aliceID, ""},
	} {
		if code, _ := cReq(t, admin, ts, op.method, op.path, op.body); code != http.StatusNotFound {
			t.Fatalf("%s %s = %d, want 404", op.method, op.path, code)
		}
	}
	// A blank rename is refused.
	if code, _ := cReq(t, admin, ts, "PATCH", "/api/v1/groups/"+gid, `{"name":"  "}`); code != http.StatusBadRequest {
		t.Fatalf("blank rename = %d, want 400", code)
	}
	// Non-admins are refused on every mutating op.
	for _, op := range []struct{ method, path, body string }{
		{"PATCH", "/api/v1/groups/" + gid, `{"name":"z"}`},
		{"DELETE", "/api/v1/groups/" + gid, ""},
		{"PUT", "/api/v1/groups/" + gid + "/members/" + aliceID, ""},
		{"DELETE", "/api/v1/groups/" + gid + "/members/" + aliceID, ""},
	} {
		if code, _ := cReq(t, alice, ts, op.method, op.path, op.body); code != http.StatusForbidden {
			t.Fatalf("non-admin %s %s = %d, want 403", op.method, op.path, code)
		}
	}
	// Rename conflict: a second group cannot take the first's name.
	_, ob := cReq(t, admin, ts, "POST", "/api/v1/groups", `{"name":"Ops"}`)
	oid := decodeGroup(t, ob).ID
	if code, _ := cReq(t, admin, ts, "PATCH", "/api/v1/groups/"+oid, `{"name":"eng"}`); code != http.StatusConflict {
		t.Fatalf("rename to taken name = %d, want 409", code)
	}
	// Renaming a group to its own name is allowed (excludeID).
	if code, _ := cReq(t, admin, ts, "PATCH", "/api/v1/groups/"+oid, `{"name":"Ops"}`); code != http.StatusOK {
		t.Fatalf("rename to own name = %d, want 200", code)
	}
	// Adding an already-present member is idempotent.
	cReq(t, admin, ts, "PUT", "/api/v1/groups/"+gid+"/members/"+aliceID, "")
	if code, _ := cReq(t, admin, ts, "PUT", "/api/v1/groups/"+gid+"/members/"+aliceID, ""); code != http.StatusOK {
		t.Fatalf("idempotent add = %d, want 200", code)
	}
	// Removing a non-member is idempotent.
	if code, _ := cReq(t, admin, ts, "DELETE", "/api/v1/groups/"+gid+"/members/usr_nobody", ""); code != http.StatusOK {
		t.Fatalf("remove non-member = %d, want 200", code)
	}
}

// TestGroupSharingGrantsAccess is the end-to-end flow: a project shared with a
// group grants its role to a group member, resolved from the login snapshot, and
// removing the user from the group revokes it on the member's next login
// (ADR-draft-groups-as-members).
func TestGroupSharingGrantsAccess(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	_, ab := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"alice","password":"password1"}`)
	aliceID := idOf(t, ab)

	// A group with alice in it.
	_, gb := cReq(t, admin, ts, "POST", "/api/v1/groups", `{"name":"Eng"}`)
	gid := decodeGroup(t, gb).ID
	if code, _ := cReq(t, admin, ts, "PUT", "/api/v1/groups/"+gid+"/members/"+aliceID, ""); code != http.StatusOK {
		t.Fatalf("add alice to group: %d", code)
	}

	// Admin owns a private project and shares it with the group as editor.
	_, pb := cReq(t, admin, ts, "POST", "/api/v1/projects", `{"name":"Secret"}`)
	pid := decodeProject(t, pb).ID
	if code, _ := cReq(t, admin, ts, "PUT", "/api/v1/projects/"+pid+"/members/nogroup", `{"role":"editor","type":"group"}`); code != http.StatusBadRequest {
		t.Fatalf("share with unknown group = %d, want 400", code)
	}
	if code, _ := cReq(t, admin, ts, "PUT", "/api/v1/projects/"+pid+"/members/"+gid, `{"role":"weird","type":"group"}`); code != http.StatusBadRequest {
		t.Fatalf("bad role = %d, want 400", code)
	}
	if code, b := cReq(t, admin, ts, "PUT", "/api/v1/projects/"+pid+"/members/"+gid, `{"role":"editor","type":"group"}`); code != http.StatusOK {
		t.Fatalf("share with group = %d %s", code, b)
	}

	// Alice logs in fresh — her group is snapshotted into the session — and now
	// sees the private project and can act as an editor (file a draft into it).
	alice := newClient(t)
	login(t, alice, ts, "alice", "password1")
	if !containsID(t, alice, ts, pid) {
		t.Fatal("group member should see the shared project")
	}
	if code, b := cReq(t, alice, ts, "POST", "/api/v1/drafts?projectId="+pid, projectDraftXML("g1", "G1")); code != http.StatusOK {
		t.Fatalf("group-editor file draft = %d %s, want 200", code, b)
	}
	// The principals directory lists the group so an owner could pick it.
	if _, prb := cReq(t, alice, ts, "GET", "/api/v1/principals", ""); true {
		var dir []struct{ Type, ID, Name string }
		_ = json.Unmarshal(prb, &dir)
		found := false
		for _, e := range dir {
			if e.Type == "group" && e.ID == gid {
				found = true
			}
		}
		if !found {
			t.Fatalf("principals should include the group: %s", prb)
		}
	}

	// Remove alice from the group; her current session keeps the snapshot, but a
	// fresh login re-snapshots without the group, so access is gone.
	if code, _ := cReq(t, admin, ts, "DELETE", "/api/v1/groups/"+gid+"/members/"+aliceID, ""); code != http.StatusOK {
		t.Fatalf("remove alice from group: %d", code)
	}
	alice2 := newClient(t)
	login(t, alice2, ts, "alice", "password1")
	if containsID(t, alice2, ts, pid) {
		t.Fatal("access should be gone after re-login without the group")
	}
}
