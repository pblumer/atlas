package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestFolderSharedWithAGroup drives the sharing half through the real routes with
// authentication on: the group a folder can be shared with is the caller's own
// membership, a member of that group sees the folder but cannot change it, and
// somebody outside the group does not see it at all.
func TestFolderSharedWithAGroup(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")

	_, ab := cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"alice","password":"password1","roles":["user"]}`)
	aliceID := idOf(t, ab)
	_, bb := cReq(t, admin, ts, "POST", "/api/v1/users",
		`{"username":"bert","password":"password1","roles":["user"]}`)
	if idOf(t, bb) == "" {
		t.Fatal("bert has no id")
	}
	_, gb := cReq(t, admin, ts, "POST", "/api/v1/groups", `{"name":"Service Desk"}`)
	gid := decodeGroup(t, gb).ID
	// A second group alice is also in, so the offered list is one somebody has to
	// choose from — and so its order is a thing this test can state.
	_, gb2 := cReq(t, admin, ts, "POST", "/api/v1/groups", `{"name":"Backoffice"}`)
	gid2 := decodeGroup(t, gb2).ID
	for _, g := range []string{gid, gid2} {
		if code, _ := cReq(t, admin, ts, "PUT", "/api/v1/groups/"+g+"/members/"+aliceID, ""); code != http.StatusOK {
			t.Fatalf("add alice to group %s: %d", g, code)
		}
	}

	// The editor offers the groups the caller is in, by name — which is the only
	// reason the console can show "Gruppe: Service Desk" without a route that lets
	// any signed-in account read the whole group roster.
	alice := newClient(t)
	login(t, alice, ts, "alice", "password1")
	code, fb := cReq(t, alice, ts, "GET", "/api/v1/task-folders/fields", "")
	if code != http.StatusOK {
		t.Fatalf("fields = %d %s", code, fb)
	}
	var fields struct {
		Options struct {
			MyGroups []struct{ Value, Label string } `json:"myGroups"`
		} `json:"options"`
	}
	if err := json.Unmarshal(fb, &fields); err != nil {
		t.Fatalf("decode fields: %v (%s)", err, fb)
	}
	if len(fields.Options.MyGroups) != 2 {
		t.Fatalf("myGroups = %+v, want both groups alice is in", fields.Options.MyGroups)
	}
	// By name, so the control reads as a list somebody scans rather than as the
	// order the memberships happened to be written in.
	if fields.Options.MyGroups[0].Label != "Backoffice" || fields.Options.MyGroups[1].Label != "Service Desk" {
		t.Errorf("myGroups = %+v, want them ordered by name", fields.Options.MyGroups)
	}
	if fields.Options.MyGroups[1].Value != gid {
		t.Errorf("the Service Desk entry carries %q, want the group id", fields.Options.MyGroups[1].Value)
	}

	// Bert is in no group, so he is offered none to share with.
	bert := newClient(t)
	login(t, bert, ts, "bert", "password1")
	_, bf := cReq(t, bert, ts, "GET", "/api/v1/task-folders/fields", "")
	if err := json.Unmarshal(bf, &fields); err != nil {
		t.Fatalf("decode bert's fields: %v", err)
	}
	if len(fields.Options.MyGroups) != 0 {
		t.Errorf("bert is offered %+v to share with; he is in no group", fields.Options.MyGroups)
	}

	// Alice shares a folder with the group.
	body := `{"name":"Service Desk offen","visibility":"group","groupId":"` + gid + `",` +
		`"rule":{"match":"all","conditions":[{"field":"assignee","op":"isEmpty"}]}}`
	code, cb := cReq(t, alice, ts, "POST", "/api/v1/task-folders", body)
	if code != http.StatusOK {
		t.Fatalf("create shared folder = %d %s", code, cb)
	}
	folderID := idOf(t, cb)

	// Bert, outside the group, does not see it.
	_, lb := cReq(t, bert, ts, "GET", "/api/v1/task-folders", "")
	if strings.TrimSpace(string(lb)) != "[]" {
		t.Errorf("bert sees %s; he is not in the group it is shared with", lb)
	}
	// And cannot change or delete it — a folder he cannot see is a folder that is
	// not there, which is the same answer as an unknown id.
	if code, _ := cReq(t, bert, ts, "PUT", "/api/v1/task-folders/"+folderID, body); code != http.StatusForbidden {
		t.Errorf("bert updating the folder = %d, want 403", code)
	}
	if code, _ := cReq(t, bert, ts, "DELETE", "/api/v1/task-folders/"+folderID, ""); code != http.StatusForbidden {
		t.Errorf("bert deleting the folder = %d, want 403", code)
	}

	// Admin is not in the group either, but is in no special position here: a
	// folder is a personal view, and admin is not its owner.
	_, adminList := cReq(t, admin, ts, "GET", "/api/v1/task-folders", "")
	if strings.TrimSpace(string(adminList)) != "[]" {
		t.Errorf("admin sees %s; a folder is not administrative data", adminList)
	}

	// Add bert to the group: the folder appears, read-only, on his next request.
	bertID := ""
	_, users := cReq(t, admin, ts, "GET", "/api/v1/users", "")
	var accounts []struct{ ID, Username string }
	if err := json.Unmarshal(users, &accounts); err != nil {
		t.Fatalf("decode users: %v", err)
	}
	for _, u := range accounts {
		if u.Username == "bert" {
			bertID = u.ID
		}
	}
	if bertID == "" {
		t.Fatal("bert is not in the account list")
	}
	if code, _ := cReq(t, admin, ts, "PUT", "/api/v1/groups/"+gid+"/members/"+bertID, ""); code != http.StatusOK {
		t.Fatalf("add bert to the group: %d", code)
	}
	_, lb = cReq(t, bert, ts, "GET", "/api/v1/task-folders", "")
	var seen []struct {
		ID       string `json:"id"`
		Editable bool   `json:"editable"`
		FEEL     string `json:"feel"`
	}
	if err := json.Unmarshal(lb, &seen); err != nil {
		t.Fatalf("decode bert's folders: %v (%s)", err, lb)
	}
	if len(seen) != 1 || seen[0].ID != folderID {
		t.Fatalf("bert sees %+v after joining the group", seen)
	}
	if seen[0].Editable {
		t.Error("a folder shared with bert's group came back editable by him")
	}
	if seen[0].FEEL != "assignee = null" {
		t.Errorf("shared folder's FEEL = %q", seen[0].FEEL)
	}

	// The owner can still change it, and the change is what the group then reads.
	upd := `{"name":"Service Desk offen","visibility":"group","groupId":"` + gid + `",` +
		`"rule":{"match":"all","conditions":[{"field":"assignee","op":"isMe"}]}}`
	if code, ub := cReq(t, alice, ts, "PUT", "/api/v1/task-folders/"+folderID, upd); code != http.StatusOK {
		t.Fatalf("owner update = %d %s", code, ub)
	}
	_, lb = cReq(t, bert, ts, "GET", "/api/v1/task-folders", "")
	if err := json.Unmarshal(lb, &seen); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// "bin ich" resolves against whoever is reading, which is what makes one shared
	// folder work for a whole team rather than for the person who made it.
	if len(seen) != 1 || seen[0].FEEL != "assignee = user.name" {
		t.Errorf("after the owner's edit bert reads %+v", seen)
	}

	// A disabled account is not offered to assign work to, so it is not offered as
	// a value for an "assigned to" condition either — a folder built around somebody
	// who cannot receive work is a folder that will never fill.
	if code, _ := cReq(t, admin, ts, "PATCH", "/api/v1/users/"+bertID, `{"disabled":true}`); code != http.StatusOK {
		t.Fatalf("disable bert: %d", code)
	}
	_, af := cReq(t, alice, ts, "GET", "/api/v1/task-folders/fields", "")
	var withUsers struct {
		Options struct {
			Users []struct{ Value string } `json:"users"`
		} `json:"options"`
	}
	if err := json.Unmarshal(af, &withUsers); err != nil {
		t.Fatalf("decode fields: %v", err)
	}
	for _, u := range withUsers.Options.Users {
		if u.Value == "bert" {
			t.Error("a disabled account is still offered as an assignee to filter on")
		}
	}
	if len(withUsers.Options.Users) == 0 {
		t.Error("no users offered at all; the enabled ones should still be there")
	}
}
