package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestArtifactScopeInheritance verifies ADR-0071 membership inheritance on
// artifacts: a private project's draft is hidden from non-members, viewers can
// read but not write it, editors can write it, ungrouped stays open, and the
// runtime form-fetch is not gated.
func TestArtifactScopeInheritance(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	if login(t, admin, ts, "admin", "password1") != http.StatusOK {
		t.Fatal("admin login")
	}
	cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"alice","password":"password1"}`)
	_, bb := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"bob","password":"password1"}`)
	bobID := idOf(t, bb)

	alice := newClient(t)
	if login(t, alice, ts, "alice", "password1") != http.StatusOK {
		t.Fatal("alice login")
	}
	bob := newClient(t)
	if login(t, bob, ts, "bob", "password1") != http.StatusOK {
		t.Fatal("bob login")
	}

	// listProcessIDs of a client's /drafts.
	drafts := func(c *http.Client) map[string]bool {
		_, body := cReq(t, c, ts, "GET", "/api/v1/drafts", "")
		var list []struct {
			ProcessID string `json:"processId"`
		}
		if err := json.Unmarshal(body, &list); err != nil {
			t.Fatalf("decode drafts: %v (%s)", err, body)
		}
		out := map[string]bool{}
		for _, d := range list {
			out[d.ProcessID] = true
		}
		return out
	}

	// Alice: private project + a draft filed into it, plus an ungrouped draft.
	_, pbody := cReq(t, alice, ts, "POST", "/api/v1/projects", `{"name":"Secret"}`)
	pid := decodeProject(t, pbody).ID
	if code, b := cReq(t, alice, ts, "POST", "/api/v1/drafts?projectId="+pid, projectDraftXML("wip", "WIP")); code != http.StatusOK {
		t.Fatalf("alice file draft: %d %s", code, b)
	}
	if code, b := cReq(t, alice, ts, "POST", "/api/v1/drafts", projectDraftXML("loose", "Loose")); code != http.StatusOK {
		t.Fatalf("alice ungrouped draft: %d %s", code, b)
	}

	// Bob (non-member) cannot see the scoped draft, but the ungrouped one is open.
	bd := drafts(bob)
	if bd["wip"] {
		t.Fatal("bob must not see a draft in alice's private project")
	}
	if !bd["loose"] {
		t.Fatal("ungrouped draft should be visible to everyone (open)")
	}
	// Content read + writes on the hidden draft are refused (404 hides existence).
	if code, _ := cReq(t, bob, ts, "GET", "/api/v1/drafts/wip/xml", ""); code != http.StatusNotFound {
		t.Fatalf("bob draft xml = %d, want 404", code)
	}
	if code, _ := cReq(t, bob, ts, "DELETE", "/api/v1/drafts/wip", ""); code != http.StatusNotFound {
		t.Fatalf("bob delete hidden draft = %d, want 404", code)
	}
	if code, _ := cReq(t, bob, ts, "PATCH", "/api/v1/drafts/wip", `{"projectId":""}`); code != http.StatusNotFound {
		t.Fatalf("bob move hidden draft = %d, want 404", code)
	}
	// Bob cannot file a draft into Alice's project either.
	if code, _ := cReq(t, bob, ts, "POST", "/api/v1/drafts?projectId="+pid, projectDraftXML("intrude", "X")); code == http.StatusOK {
		t.Fatal("bob must not file a draft into a project he cannot edit")
	}

	// Share as viewer: Bob can now read but not write.
	if code, _ := cReq(t, alice, ts, "PUT", "/api/v1/projects/"+pid+"/members/"+bobID, `{"role":"viewer"}`); code != http.StatusOK {
		t.Fatalf("share viewer: %d", code)
	}
	if !drafts(bob)["wip"] {
		t.Fatal("viewer bob should see the shared draft")
	}
	if code, _ := cReq(t, bob, ts, "GET", "/api/v1/drafts/wip/xml", ""); code != http.StatusOK {
		t.Fatalf("viewer xml = %d, want 200", code)
	}
	if code, _ := cReq(t, bob, ts, "DELETE", "/api/v1/drafts/wip", ""); code != http.StatusForbidden {
		t.Fatalf("viewer delete = %d, want 403", code)
	}

	// Promote to editor: Bob can write.
	if code, _ := cReq(t, alice, ts, "PUT", "/api/v1/projects/"+pid+"/members/"+bobID, `{"role":"editor"}`); code != http.StatusOK {
		t.Fatalf("promote editor: %d", code)
	}
	if code, _ := cReq(t, bob, ts, "DELETE", "/api/v1/drafts/wip", ""); code != http.StatusNoContent {
		t.Fatalf("editor delete = %d, want 204", code)
	}

	// Admin sees every draft regardless of membership.
	if !drafts(admin)["loose"] {
		t.Fatal("admin should see all drafts")
	}

	// Alice moves her ungrouped draft into the project and back — the success
	// paths of move (source + target authorization, then the save round-trip).
	if code, _ := cReq(t, alice, ts, "PATCH", "/api/v1/drafts/loose", `{"projectId":"`+pid+`"}`); code != http.StatusOK {
		t.Fatalf("alice move into project = %d, want 200", code)
	}
	if code, _ := cReq(t, alice, ts, "PATCH", "/api/v1/drafts/loose", `{"projectId":""}`); code != http.StatusOK {
		t.Fatalf("alice move to ungrouped = %d, want 200", code)
	}
	// Overwriting her own ungrouped draft in place still works.
	if code, _ := cReq(t, alice, ts, "POST", "/api/v1/drafts", projectDraftXML("loose", "Loose v2")); code != http.StatusOK {
		t.Fatalf("alice overwrite ungrouped = %d, want 200", code)
	}
}

// TestArtifactScopeFormsAndRefs covers the list filtering for DMN references and
// forms, and the deliberate runtime exception: fetching a form to render a task
// (GET /forms/{id}) is NOT scope-gated even though the listing is.
func TestArtifactScopeFormsAndRefs(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"alice","password":"password1"}`)
	cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"bob","password":"password1"}`)
	alice := newClient(t)
	login(t, alice, ts, "alice", "password1")
	bob := newClient(t)
	login(t, bob, ts, "bob", "password1")

	_, pbody := cReq(t, alice, ts, "POST", "/api/v1/projects", `{"name":"Secret"}`)
	pid := decodeProject(t, pbody).ID

	// A DMN reference and a form filed into the private project.
	_, rb := cReq(t, alice, ts, "POST", "/api/v1/dmnrefs", `{"name":"Rating","modelRef":"m1","projectId":"`+pid+`"}`)
	refID := idOf(t, rb)
	cReq(t, alice, ts, "POST", "/api/v1/forms", `{"id":"f1","name":"Intake","projectId":"`+pid+`","schema":{"type":"default"}}`)

	// Owner success paths: rename the reference, move it out and back, and list the
	// project's decisions (the reference is viewable so it contributes to the
	// catalog; its unresolved model just yields no decisions).
	if code, _ := cReq(t, alice, ts, "PATCH", "/api/v1/dmnrefs/"+refID, `{"name":"Rating v2"}`); code != http.StatusOK {
		t.Fatalf("alice rename ref = %d, want 200", code)
	}
	if code, _ := cReq(t, alice, ts, "PATCH", "/api/v1/dmnrefs/"+refID, `{"projectId":""}`); code != http.StatusOK {
		t.Fatalf("alice move ref to ungrouped = %d, want 200", code)
	}
	if code, _ := cReq(t, alice, ts, "PATCH", "/api/v1/dmnrefs/"+refID, `{"projectId":"`+pid+`"}`); code != http.StatusOK {
		t.Fatalf("alice move ref back = %d, want 200", code)
	}
	if code, _ := cReq(t, alice, ts, "GET", "/api/v1/decisions?projectId="+pid, ""); code != http.StatusOK {
		t.Fatalf("alice decisions = %d, want 200", code)
	}
	// Bob (non-member) sees no decisions from the private project's references.
	if _, b := cReq(t, bob, ts, "GET", "/api/v1/decisions", ""); true {
		var out []map[string]any
		_ = json.Unmarshal(b, &out)
		for _, d := range out {
			if d["modelRef"] == "m1" {
				t.Fatalf("bob must not see decisions from a private reference: %s", b)
			}
		}
	}

	// Bob (non-member): the reference and form are hidden from the design-time lists.
	if _, b := cReq(t, bob, ts, "GET", "/api/v1/dmnrefs", ""); string(b) != "[]\n" && string(b) != "[]" {
		// bob has no refs of his own, so his filtered list must be empty
		var list []map[string]any
		_ = json.Unmarshal(b, &list)
		if len(list) != 0 {
			t.Fatalf("bob dmnrefs must be empty, got %s", b)
		}
	}
	var forms []map[string]any
	if _, b := cReq(t, bob, ts, "GET", "/api/v1/forms", ""); true {
		_ = json.Unmarshal(b, &forms)
		if len(forms) != 0 {
			t.Fatalf("bob forms must be empty, got %s", b)
		}
	}

	// Content reads on the reference: the owner may fetch its graph/validate, a
	// non-member is refused (404 hides it).
	if code, _ := cReq(t, alice, ts, "GET", "/api/v1/dmnrefs/"+refID+"/graph", ""); code != http.StatusOK {
		t.Fatalf("owner ref graph = %d, want 200", code)
	}
	if code, _ := cReq(t, bob, ts, "GET", "/api/v1/dmnrefs/"+refID+"/graph", ""); code != http.StatusNotFound {
		t.Fatalf("non-member ref graph = %d, want 404", code)
	}
	if code, _ := cReq(t, bob, ts, "POST", "/api/v1/dmnrefs/"+refID+"/validate", ""); code != http.StatusNotFound {
		t.Fatalf("non-member ref validate = %d, want 404", code)
	}

	// But the runtime form fetch is open: a task worker who isn't a project member
	// can still render the bound form (ADR-0071 keeps runtime out of scope).
	if code, b := cReq(t, bob, ts, "GET", "/api/v1/forms/f1", ""); code != http.StatusOK {
		t.Fatalf("runtime form fetch = %d, want 200 (%s)", code, b)
	}

	// Bob cannot delete the scoped form.
	if code, _ := cReq(t, bob, ts, "DELETE", "/api/v1/forms/f1", ""); code != http.StatusNotFound {
		t.Fatalf("bob delete scoped form = %d, want 404", code)
	}
}
