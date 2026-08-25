package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

type auditEntry struct {
	ID            string `json:"id"`
	ApplicationID string `json:"applicationId"`
	At            int64  `json:"at"`
	ActorID       string `json:"actorId"`
	ActorName     string `json:"actorName"`
	Action        string `json:"action"`
	SubjectType   string `json:"subjectType"`
	SubjectID     string `json:"subjectId"`
	Role          string `json:"role"`
	From          string `json:"from"`
	To            string `json:"to"`
}

func decodeAudit(t *testing.T, b []byte) []auditEntry {
	t.Helper()
	var out []auditEntry
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode audit: %v (%s)", err, b)
	}
	return out
}

// TestGrantAuditLog drives the full ADR-draft-grant-audit-log flow: every grant
// mutation on a project is recorded, no-ops leave no trail, and the history is
// owner-only.
func TestGrantAuditLog(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	_, ab := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"alice","password":"password1"}`)
	aliceID := idOf(t, ab)
	_, bb := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"bob","password":"password1"}`)
	bobID := idOf(t, bb)
	_, gb := cReq(t, admin, ts, "POST", "/api/v1/groups", `{"name":"Eng"}`)
	gid := decodeGroup(t, gb).ID

	_, pb := cReq(t, admin, ts, "POST", "/api/v1/projects", `{"name":"Audited"}`)
	pid := decodeProject(t, pb).ID
	adminID := decodeProject(t, pb).OwnerID

	// A member but not the owner cannot read the history (owner-only, 403 not 404).
	if code, _ := cReq(t, admin, ts, "PUT", "/api/v1/projects/"+pid+"/members/"+aliceID, `{"role":"editor"}`); code != http.StatusOK {
		t.Fatalf("share alice editor: %d", code)
	}
	alice := newClient(t)
	login(t, alice, ts, "alice", "password1")
	if code, _ := cReq(t, alice, ts, "GET", "/api/v1/projects/"+pid+"/audit", ""); code != http.StatusForbidden {
		t.Fatalf("editor reads audit = %d, want 403", code)
	}
	// A stranger with no access is refused with 404 (existence hidden).
	cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"carol","password":"password1"}`)
	carol := newClient(t)
	login(t, carol, ts, "carol", "password1")
	if code, _ := cReq(t, carol, ts, "GET", "/api/v1/projects/"+pid+"/audit", ""); code != http.StatusNotFound {
		t.Fatalf("stranger reads audit = %d, want 404", code)
	}

	// The rest of the grant mutations.
	cReq(t, admin, ts, "PUT", "/api/v1/projects/"+pid+"/members/"+aliceID, `{"role":"viewer"}`) // role change
	cReq(t, admin, ts, "PUT", "/api/v1/projects/"+pid+"/members/"+gid, `{"role":"editor","type":"group"}`)
	cReq(t, admin, ts, "DELETE", "/api/v1/projects/"+pid+"/members/"+aliceID, "")    // unshare
	cReq(t, admin, ts, "DELETE", "/api/v1/projects/"+pid+"/members/usr_nobody", "")  // no-op: not a member
	cReq(t, admin, ts, "PATCH", "/api/v1/projects/"+pid, `{"visibility":"private"}`) // shared->private
	cReq(t, admin, ts, "PATCH", "/api/v1/projects/"+pid, `{"visibility":"private"}`) // no-op: already private
	cReq(t, admin, ts, "PATCH", "/api/v1/projects/"+pid, `{"visibility":"shared"}`)  // private->shared
	cReq(t, admin, ts, "PATCH", "/api/v1/projects/"+pid, `{"name":"Renamed"}`)       // no-op: not a grant
	cReq(t, admin, ts, "PATCH", "/api/v1/projects/"+pid, `{"ownerId":"`+bobID+`"}`)  // transfer
	cReq(t, admin, ts, "PATCH", "/api/v1/projects/"+pid, `{"ownerId":"`+bobID+`"}`)  // no-op: already owner

	// Admin (owner-equivalent) reads the history.
	_, lb := cReq(t, admin, ts, "GET", "/api/v1/applications/"+pid+"/audit", "")
	entries := decodeAudit(t, lb)

	byAction := map[string]int{}
	var share, unshare, vis, transfer *auditEntry
	for i := range entries {
		e := &entries[i]
		byAction[e.Action]++
		if e.ActorName != "admin" {
			t.Fatalf("actor name = %q, want admin: %+v", e.ActorName, e)
		}
		switch e.Action {
		case "share":
			if e.SubjectID == gid {
				share = e // remember the group share for its fields
			}
		case "unshare":
			unshare = e
		case "visibility":
			vis = e
		case "transfer":
			transfer = e
		}
	}
	// 3 shares (editor, viewer role-change, group), 1 unshare, 2 visibility, 1 transfer.
	if byAction["share"] != 3 || byAction["unshare"] != 1 || byAction["visibility"] != 2 || byAction["transfer"] != 1 {
		t.Fatalf("action counts = %v, want share:3 unshare:1 visibility:2 transfer:1 (%s)", byAction, lb)
	}
	if len(entries) != 7 {
		t.Fatalf("total entries = %d, want 7 (%s)", len(entries), lb)
	}
	if share == nil || share.SubjectType != "group" || share.Role != "editor" {
		t.Fatalf("group share entry wrong: %+v", share)
	}
	if unshare == nil || unshare.SubjectType != "user" || unshare.SubjectID != aliceID {
		t.Fatalf("unshare entry wrong: %+v", unshare)
	}
	if vis == nil || vis.From == "" || vis.To == "" {
		t.Fatalf("visibility entry missing from/to: %+v", vis)
	}
	if transfer == nil || transfer.From != adminID || transfer.To != bobID {
		t.Fatalf("transfer entry wrong: %+v (admin=%s bob=%s)", transfer, adminID, bobID)
	}
	// Newest-first ordering: timestamps are non-increasing down the list.
	for i := 1; i < len(entries); i++ {
		if entries[i-1].At < entries[i].At {
			t.Fatalf("entries not newest-first at %d: %d < %d", i, entries[i-1].At, entries[i].At)
		}
	}

	// Deleting the application clears its history with it.
	if code, _ := cReq(t, admin, ts, "DELETE", "/api/v1/projects/"+pid, ""); code != http.StatusNoContent {
		t.Fatalf("delete project: %d", code)
	}
	_, pb2 := cReq(t, admin, ts, "POST", "/api/v1/projects", `{"name":"Fresh"}`)
	pid2 := decodeProject(t, pb2).ID
	if _, lb2 := cReq(t, admin, ts, "GET", "/api/v1/applications/"+pid2+"/audit", ""); len(decodeAudit(t, lb2)) != 0 {
		t.Fatalf("fresh project should have empty history: %s", lb2)
	}
}
