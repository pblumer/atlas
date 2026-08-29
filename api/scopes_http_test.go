package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// scopeProjectView is the subset of a project's JSON the scope tests assert on.
type scopeProjectView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	OwnerID    string `json:"ownerId"`
	Visibility string `json:"visibility"`
	MyRole     string `json:"myRole"`
	Members    []struct {
		Ref struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"ref"`
		Role string `json:"role"`
	} `json:"members"`
}

func decodeProject(t *testing.T, body []byte) scopeProjectView {
	t.Helper()
	var p scopeProjectView
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode project: %v (%s)", err, body)
	}
	return p
}

// TestProjectSharingLifecycle drives the full ADR-0071 Phase 1 flow under auth:
// a private project is invisible to a stranger, sharing grants exactly the
// granted role, revoking takes it away, admin sees everything, and ownership
// transfers.
func TestProjectSharingLifecycle(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")

	admin := newClient(t)
	if login(t, admin, ts, "admin", "password1") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	_, ab := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"alice","password":"password1","roles":["modeler","operator","user"]}`)
	aliceID := idOf(t, ab)
	_, bb := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"bob","password":"password1","roles":["modeler","operator","user"]}`)
	bobID := idOf(t, bb)

	alice := newClient(t)
	if login(t, alice, ts, "alice", "password1") != http.StatusOK {
		t.Fatal("alice login failed")
	}
	bob := newClient(t)
	if login(t, bob, ts, "bob", "password1") != http.StatusOK {
		t.Fatal("bob login failed")
	}

	// Alice creates a project: private, owned by her, she is owner.
	code, body := cReq(t, alice, ts, "POST", "/api/v1/projects", `{"name":"Secret"}`)
	if code != http.StatusOK {
		t.Fatalf("create: %d %s", code, body)
	}
	p := decodeProject(t, body)
	if p.OwnerID != aliceID || p.Visibility != "private" || p.MyRole != "owner" {
		t.Fatalf("new project = %+v, want owner=%s private owner-role", p, aliceID)
	}
	pid := p.ID

	// Bob cannot see it, and every project op 404s (hidden, existence not leaked).
	if code, _ := cReq(t, bob, ts, "GET", "/api/v1/projects", ""); code != http.StatusOK {
		t.Fatalf("bob list: %d", code)
	} else if containsID(t, bob, ts, pid) {
		t.Fatal("bob should not see alice's private project")
	}
	for _, op := range []struct{ method, path, body string }{
		{"PATCH", "/api/v1/projects/" + pid, `{"name":"Hacked"}`},
		{"DELETE", "/api/v1/projects/" + pid, ""},
		{"POST", "/api/v1/projects/" + pid + "/deploy", ""},
		{"POST", "/api/v1/projects/" + pid + "/validate", ""},
	} {
		if code, _ := cReq(t, bob, ts, op.method, op.path, op.body); code != http.StatusNotFound {
			t.Fatalf("bob %s %s = %d, want 404", op.method, op.path, code)
		}
	}

	// Input validation on sharing (alice is owner).
	if code, _ := cReq(t, alice, ts, "PUT", "/api/v1/projects/"+pid+"/members/"+bobID, `{"role":"boss"}`); code != http.StatusBadRequest {
		t.Fatalf("bad role = %d, want 400", code)
	}
	if code, _ := cReq(t, alice, ts, "PUT", "/api/v1/projects/"+pid+"/members/"+bobID, `{"role":"owner"}`); code != http.StatusBadRequest {
		t.Fatalf("owner not grantable = %d, want 400", code)
	}
	if code, _ := cReq(t, alice, ts, "PUT", "/api/v1/projects/"+pid+"/members/usr_ghost", `{"role":"viewer"}`); code != http.StatusBadRequest {
		t.Fatalf("share with unknown user = %d, want 400", code)
	}
	if code, _ := cReq(t, alice, ts, "PUT", "/api/v1/projects/"+pid+"/members/"+aliceID, `{"role":"viewer"}`); code != http.StatusBadRequest {
		t.Fatalf("owner as member = %d, want 400", code)
	}
	if code, _ := cReq(t, alice, ts, "PATCH", "/api/v1/projects/"+pid, `{"visibility":"open"}`); code != http.StatusBadRequest {
		t.Fatalf("bad visibility = %d, want 400", code)
	}
	if code, _ := cReq(t, alice, ts, "PATCH", "/api/v1/projects/"+pid, `{}`); code != http.StatusBadRequest {
		t.Fatalf("empty update = %d, want 400", code)
	}

	// Share with Bob as viewer: project flips to shared, Bob gets read access.
	code, body = cReq(t, alice, ts, "PUT", "/api/v1/projects/"+pid+"/members/"+bobID, `{"role":"viewer"}`)
	if code != http.StatusOK {
		t.Fatalf("share viewer: %d %s", code, body)
	}
	p = decodeProject(t, body)
	if p.Visibility != "shared" || len(p.Members) != 1 || p.Members[0].Ref.ID != bobID || p.Members[0].Role != "viewer" {
		t.Fatalf("after share = %+v, want shared with bob=viewer", p)
	}

	// Bob now sees it as a viewer, can validate (read), but not deploy or rename.
	if !containsID(t, bob, ts, pid) {
		t.Fatal("bob should see the shared project")
	}
	if code, _ := cReq(t, bob, ts, "POST", "/api/v1/projects/"+pid+"/validate", ""); code != http.StatusOK {
		t.Fatalf("viewer validate = %d, want 200", code)
	}
	if code, _ := cReq(t, bob, ts, "POST", "/api/v1/projects/"+pid+"/deploy", ""); code != http.StatusForbidden {
		t.Fatalf("viewer deploy = %d, want 403", code)
	}
	if code, _ := cReq(t, bob, ts, "PATCH", "/api/v1/projects/"+pid, `{"name":"Nope"}`); code != http.StatusForbidden {
		t.Fatalf("viewer rename = %d, want 403", code)
	}
	// A viewer cannot re-share the project (needs owner).
	if code, _ := cReq(t, bob, ts, "PUT", "/api/v1/projects/"+pid+"/members/"+aliceID, `{"role":"editor"}`); code != http.StatusForbidden {
		t.Fatalf("viewer share = %d, want 403", code)
	}

	// Promote Bob to editor: he can now deploy.
	if code, _ := cReq(t, alice, ts, "PUT", "/api/v1/projects/"+pid+"/members/"+bobID, `{"role":"editor"}`); code != http.StatusOK {
		t.Fatalf("promote editor = %d", code)
	}
	if code, _ := cReq(t, bob, ts, "POST", "/api/v1/projects/"+pid+"/deploy", ""); code != http.StatusOK {
		t.Fatalf("editor deploy = %d, want 200", code)
	}

	// Revoke Bob: access disappears again.
	code, body = cReq(t, alice, ts, "DELETE", "/api/v1/projects/"+pid+"/members/"+bobID, "")
	if code != http.StatusOK {
		t.Fatalf("revoke: %d %s", code, body)
	}
	if len(decodeProject(t, body).Members) != 0 {
		t.Fatalf("members after revoke = %s, want empty", body)
	}
	if containsID(t, bob, ts, pid) {
		t.Fatal("bob should not see the project after revoke")
	}
	if code, _ := cReq(t, bob, ts, "PATCH", "/api/v1/projects/"+pid, `{"name":"x"}`); code != http.StatusNotFound {
		t.Fatalf("revoked bob rename = %d, want 404", code)
	}

	// Admin sees everything and can manage it.
	if !containsID(t, admin, ts, pid) {
		t.Fatal("admin should see every project")
	}
	if code, _ := cReq(t, admin, ts, "PATCH", "/api/v1/projects/"+pid, `{"name":"AdminTouched"}`); code != http.StatusOK {
		t.Fatalf("admin rename = %d, want 200", code)
	}

	// Transfer ownership to Bob: Alice loses access, Bob gains owner rights.
	if code, _ := cReq(t, alice, ts, "PATCH", "/api/v1/projects/"+pid, `{"ownerId":"usr_ghost"}`); code != http.StatusBadRequest {
		t.Fatalf("transfer to unknown = %d, want 400", code)
	}
	code, body = cReq(t, alice, ts, "PATCH", "/api/v1/projects/"+pid, `{"ownerId":"`+bobID+`"}`)
	if code != http.StatusOK {
		t.Fatalf("transfer: %d %s", code, body)
	}
	if decodeProject(t, body).OwnerID != bobID {
		t.Fatalf("owner after transfer = %s", body)
	}
	if code, _ := cReq(t, alice, ts, "PATCH", "/api/v1/projects/"+pid, `{"name":"x"}`); code != http.StatusNotFound {
		t.Fatalf("ex-owner alice rename = %d, want 404", code)
	}
	if code, _ := cReq(t, bob, ts, "PATCH", "/api/v1/projects/"+pid, `{"name":"BobsNow"}`); code != http.StatusOK {
		t.Fatalf("new owner bob rename = %d, want 200", code)
	}
}

// TestProjectTransferDropsNewOwnerMembership checks the invariant that the owner
// is never also a listed member (ADR-0071): transferring a shared project to a
// user who is already a member drops that stale grant, so they appear only as the
// owner and not a second time in the member list.
func TestProjectTransferDropsNewOwnerMembership(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"alice","password":"password1","roles":["modeler","operator","user"]}`)
	_, bb := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"bob","password":"password1","roles":["modeler","operator","user"]}`)
	bobID := idOf(t, bb)

	alice := newClient(t)
	login(t, alice, ts, "alice", "password1")

	// Alice owns a project and shares it with Bob as editor.
	_, pb := cReq(t, alice, ts, "POST", "/api/v1/projects", `{"name":"Handoff"}`)
	pid := decodeProject(t, pb).ID
	if code, mb := cReq(t, alice, ts, "PUT", "/api/v1/projects/"+pid+"/members/"+bobID, `{"role":"editor"}`); code != http.StatusOK {
		t.Fatalf("share with bob = %d %s", code, mb)
	}

	// Transfer to Bob: he becomes owner and his now-redundant member grant is gone.
	code, tb := cReq(t, alice, ts, "PATCH", "/api/v1/projects/"+pid, `{"ownerId":"`+bobID+`"}`)
	if code != http.StatusOK {
		t.Fatalf("transfer: %d %s", code, tb)
	}
	after := decodeProject(t, tb)
	if after.OwnerID != bobID {
		t.Fatalf("owner after transfer = %s, want %s", after.OwnerID, bobID)
	}
	for _, m := range after.Members {
		if m.Ref.ID == bobID {
			t.Fatalf("new owner still listed as a member: %+v", after.Members)
		}
	}
	// Bob, now owner, sees it as owner; Alice keeps a viewer grant only if she were
	// added — she was not, so the clean handoff leaves her without access.
	bob := newClient(t)
	login(t, bob, ts, "bob", "password1")
	if _, gb := cReq(t, bob, ts, "GET", "/api/v1/projects", ""); !containsID(t, bob, ts, pid) {
		t.Fatalf("bob should own the project after transfer: %s", gb)
	}
	if containsID(t, admin, ts, pid) {
		_, lb := cReq(t, admin, ts, "GET", "/api/v1/projects", "")
		var list []scopeProjectView
		_ = json.Unmarshal(lb, &list)
		for _, pv := range list {
			if pv.ID == pid && pv.OwnerID != bobID {
				t.Fatalf("admin view owner = %s, want %s", pv.OwnerID, bobID)
			}
		}
	}
}

// containsID reports whether a client's project listing includes the given id.
func containsID(t *testing.T, c *http.Client, ts *httptest.Server, id string) bool {
	t.Helper()
	_, body := cReq(t, c, ts, "GET", "/api/v1/projects", "")
	var list []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode list: %v (%s)", err, body)
	}
	for _, p := range list {
		if p.ID == id {
			return true
		}
	}
	return false
}
