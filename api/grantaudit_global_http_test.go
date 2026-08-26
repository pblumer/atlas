package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

type globalAuditEntry struct {
	ApplicationID   string `json:"applicationId"`
	ApplicationName string `json:"applicationName"`
	At              int64  `json:"at"`
	ActorName       string `json:"actorName"`
	Action          string `json:"action"`
	SubjectID       string `json:"subjectId"`
	Role            string `json:"role"`
	From            string `json:"from"`
	To              string `json:"to"`
}

func decodeGlobalAudit(t *testing.T, b []byte) []globalAuditEntry {
	t.Helper()
	var out []globalAuditEntry
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode global audit: %v (%s)", err, b)
	}
	return out
}

// TestGlobalAuditView covers the cross-application admin audit view (ADR-0184):
// events from every application in one newest-first list, enriched with the
// application name, filterable by application and action, capped by limit, and
// admin-only.
func TestGlobalAuditView(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	login(t, admin, ts, "admin", "password1")
	_, ab := cReq(t, admin, ts, "POST", "/api/v1/users", `{"username":"alice","password":"password1"}`)
	aliceID := idOf(t, ab)

	// App Alpha: one share event.
	_, pa := cReq(t, admin, ts, "POST", "/api/v1/projects", `{"name":"Alpha"}`)
	pidA := decodeProject(t, pa).ID
	if code, _ := cReq(t, admin, ts, "PUT", "/api/v1/projects/"+pidA+"/members/"+aliceID, `{"role":"editor"}`); code != http.StatusOK {
		t.Fatalf("share Alpha: %d", code)
	}
	// App Beta: a visibility change and an ownership transfer.
	_, pbb := cReq(t, admin, ts, "POST", "/api/v1/projects", `{"name":"Beta"}`)
	pidB := decodeProject(t, pbb).ID
	cReq(t, admin, ts, "PATCH", "/api/v1/projects/"+pidB, `{"visibility":"shared"}`)
	cReq(t, admin, ts, "PATCH", "/api/v1/projects/"+pidB, `{"ownerId":"`+aliceID+`"}`)

	// Global list: all three events, newest-first, application names resolved.
	_, lb := cReq(t, admin, ts, "GET", "/api/v1/audit", "")
	all := decodeGlobalAudit(t, lb)
	if len(all) != 3 {
		t.Fatalf("global audit = %d events, want 3 (%s)", len(all), lb)
	}
	names := map[string]string{}
	for i := 1; i < len(all); i++ {
		if all[i-1].At < all[i].At {
			t.Fatalf("not newest-first at %d", i)
		}
	}
	for _, e := range all {
		names[e.ApplicationID] = e.ApplicationName
	}
	if names[pidA] != "Alpha" || names[pidB] != "Beta" {
		t.Fatalf("application names not resolved: %v", names)
	}

	// Filter by action.
	_, sb := cReq(t, admin, ts, "GET", "/api/v1/audit?action=share", "")
	shares := decodeGlobalAudit(t, sb)
	if len(shares) != 1 || shares[0].Action != "share" || shares[0].ApplicationID != pidA {
		t.Fatalf("action=share filter = %s", sb)
	}
	// Filter by application.
	_, bb := cReq(t, admin, ts, "GET", "/api/v1/audit?applicationId="+pidB, "")
	betas := decodeGlobalAudit(t, bb)
	if len(betas) != 2 {
		t.Fatalf("applicationId=Beta filter = %d, want 2 (%s)", len(betas), bb)
	}
	for _, e := range betas {
		if e.ApplicationID != pidB {
			t.Fatalf("filter leaked another app: %+v", e)
		}
	}
	// limit caps the window.
	if _, l1 := cReq(t, admin, ts, "GET", "/api/v1/audit?limit=1", ""); len(decodeGlobalAudit(t, l1)) != 1 {
		t.Fatalf("limit=1 did not cap: %s", l1)
	}
	// A bad limit is refused.
	for _, bad := range []string{"abc", "0", "-3"} {
		if code, _ := cReq(t, admin, ts, "GET", "/api/v1/audit?limit="+bad, ""); code != http.StatusBadRequest {
			t.Fatalf("limit=%s = %d, want 400", bad, code)
		}
	}

	// Admin-only: a signed-in non-admin is refused even for an application they now
	// own (alice owns Beta after the transfer).
	alice := newClient(t)
	login(t, alice, ts, "alice", "password1")
	if code, _ := cReq(t, alice, ts, "GET", "/api/v1/audit", ""); code != http.StatusForbidden {
		t.Fatalf("non-admin global audit = %d, want 403", code)
	}
}
