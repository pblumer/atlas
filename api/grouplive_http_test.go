package api_test

import (
	"net/http"
	"testing"
)

// TestGroupMembershipLive proves ADR-0185 end to end: a
// membership change reaches an already-logged-in user on their next request, with no
// re-login. The same alice client (one session cookie) is used throughout — never a
// fresh login — so any access change observed is the live push, not a re-snapshot.
func TestGroupMembershipLive(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	_, ab := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"alice","password":"password1","roles":["modeler","operator","user"]}`)
	aliceID := idOf(t, ab)
	_, gb := cReq(t, admin, ts, "POST", "/api/v1/groups", `{"name":"Eng"}`)
	gid := decodeGroup(t, gb).ID

	// Admin owns a private project and shares it with the group as editor.
	_, pb := cReq(t, admin, ts, "POST", "/api/v1/projects", `{"name":"Secret"}`)
	pid := decodeProject(t, pb).ID
	if code, _ := cReq(t, admin, ts, "PUT", "/api/v1/projects/"+pid+"/members/"+gid, `{"role":"editor","type":"group"}`); code != http.StatusOK {
		t.Fatalf("share with group: %d", code)
	}

	// Alice logs in ONCE, before she is in the group: no access.
	alice := newClient(t)
	login(t, alice, ts, "alice", "password1")
	if containsID(t, alice, ts, pid) {
		t.Fatal("alice should not see the project before joining the group")
	}

	// Admin adds alice to the group. Without alice re-logging in, her live session
	// gains the group and she can now see and edit the project.
	if code, _ := cReq(t, admin, ts, "PUT", "/api/v1/groups/"+gid+"/members/"+aliceID, ""); code != http.StatusOK {
		t.Fatalf("add member: %d", code)
	}
	if !containsID(t, alice, ts, pid) {
		t.Fatal("alice should see the project live after being added, without re-login")
	}
	if code, b := cReq(t, alice, ts, "POST", "/api/v1/drafts?projectId="+pid, projectDraftXML("g1", "G1")); code != http.StatusOK {
		t.Fatalf("live group-editor file draft = %d %s, want 200", code, b)
	}

	// Admin removes alice from the group: access is gone on her next request, still
	// no re-login.
	if code, _ := cReq(t, admin, ts, "DELETE", "/api/v1/groups/"+gid+"/members/"+aliceID, ""); code != http.StatusOK {
		t.Fatalf("remove member: %d", code)
	}
	if containsID(t, alice, ts, pid) {
		t.Fatal("alice should lose access live after being removed, without re-login")
	}

	// Re-add her, confirm access returns live, then delete the whole group: the
	// group's grant stops applying for her immediately.
	cReq(t, admin, ts, "PUT", "/api/v1/groups/"+gid+"/members/"+aliceID, "")
	if !containsID(t, alice, ts, pid) {
		t.Fatal("alice should regain access live after being re-added")
	}
	if code, _ := cReq(t, admin, ts, "DELETE", "/api/v1/groups/"+gid, ""); code != http.StatusNoContent {
		t.Fatalf("delete group: %d", code)
	}
	if containsID(t, alice, ts, pid) {
		t.Fatal("deleting the group should revoke its grant live")
	}
}
