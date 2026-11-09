package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The start-grant administration routes of ADR-0516, end to end: a real server
// with authentication, real accounts and groups, an application that published a
// process, and a restart over the same data directory.

// grantWorld is a server with an administrator, a field worker in a group, a
// modeler who owns an application of their own, and two applications with one
// published process each.
type grantWorld struct {
	ts                     *httptest.Server
	admin, worker, modeler *http.Client
	adminID, workerID      string
	modelerID, groupID     string
	fieldApp, otherApp     string
	modelersApp            string
	close                  func()
}

func publishProcessInto(t *testing.T, c *http.Client, ts *httptest.Server, app, processID string) {
	t.Helper()
	if code, body := cReqTyped(t, c, ts, "POST", "/api/v1/drafts?projectId="+app, "application/xml", releasableBPMN(processID)); code != http.StatusOK {
		t.Fatalf("save draft %s = %d %s", processID, code, body)
	}
	if code, body := cReq(t, c, ts, "POST", "/api/v1/applications/"+app+"/publish", `{}`); code != http.StatusOK {
		t.Fatalf("publish %s = %d %s", app, code, body)
	}
}

func appNamed(t *testing.T, c *http.Client, ts *httptest.Server, name string) string {
	t.Helper()
	code, body := cReq(t, c, ts, "POST", "/api/v1/applications", `{"name":"`+name+`"}`)
	if code != http.StatusOK {
		t.Fatalf("create application = %d %s", code, body)
	}
	return idOf(t, body)
}

func newGrantWorld(t *testing.T, dir string) *grantWorld {
	t.Helper()
	ts, closeFn := authServerOn(t, dir, "root", "rootpassword")
	w := &grantWorld{ts: ts, close: closeFn, admin: newClient(t)}
	if code := login(t, w.admin, ts, "root", "rootpassword"); code != http.StatusOK {
		t.Fatalf("admin login = %d", code)
	}
	_, me := cReq(t, w.admin, ts, "GET", "/api/v1/auth/me", "")
	var m struct {
		User struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	_ = json.Unmarshal(me, &m)
	if w.adminID = m.User.ID; w.adminID == "" {
		t.Fatalf("no administrator id in %s", me)
	}
	w.workerID = createUserWithRoles(t, w.admin, ts.URL, "fieldworker", `["user"]`)
	w.modelerID = createUserWithRoles(t, w.admin, ts.URL, "appowner", `["modeler","operator","user"]`)
	w.groupID = createGroup(t, w.admin, ts.URL, "field-inspectors")
	if code, body := cReq(t, w.admin, ts, "PUT", "/api/v1/groups/"+w.groupID+"/members/"+w.workerID, ""); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("add member = %d %s", code, body)
	}
	w.fieldApp = appNamed(t, w.admin, ts, "Field Operations")
	publishProcessInto(t, w.admin, ts, w.fieldApp, "safety-inspection")
	w.otherApp = appNamed(t, w.admin, ts, "Back Office")
	publishProcessInto(t, w.admin, ts, w.otherApp, "invoice-check")
	w.worker = signInAs(t, ts.URL, "fieldworker", "a-password-that-is-long")
	w.modeler = signInAs(t, ts.URL, "appowner", "a-password-that-is-long")
	w.modelersApp = appNamed(t, w.modeler, ts, "Owned by a modeler")
	publishProcessInto(t, w.modeler, ts, w.modelersApp, "owned-process")
	return w
}

func grantPath(app, process string) string {
	return "/api/v1/applications/" + app + "/start-grants/" + process
}

func decodeMap(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return m
}

func TestStartGrantAdministrationIsAnAdministratorsAlone(t *testing.T) {
	w := newGrantWorld(t, t.TempDir())
	defer w.close()
	path := grantPath(w.fieldApp, "safety-inspection")
	body := `{"groupIds":["` + w.groupID + `"],"expectedRevision":0}`

	// Nobody signed in.
	anon := newClient(t)
	if code, _ := cReq(t, anon, w.ts, "GET", path, ""); code != http.StatusUnauthorized {
		t.Fatalf("anonymous read = %d, want 401", code)
	}
	if code, _ := cReq(t, anon, w.ts, "PUT", path, body); code != http.StatusUnauthorized {
		t.Fatalf("anonymous grant = %d, want 401", code)
	}
	// A task user, the very person a grant is for, cannot grant it to themselves.
	for _, m := range []string{"GET", "PUT", "DELETE"} {
		target, b := path, body
		if m == "DELETE" {
			target, b = path+"?expectedRevision=0", ""
		}
		if code, _ := cReq(t, w.worker, w.ts, m, target, b); code != http.StatusForbidden {
			t.Errorf("task user %s = %d, want 403", m, code)
		}
	}
	// Owning an application is not the right to say who starts its processes.
	ownPath := grantPath(w.modelersApp, "owned-process")
	if code, _ := cReq(t, w.modeler, w.ts, "PUT", ownPath, `{"userIds":["`+w.workerID+`"],"expectedRevision":0}`); code != http.StatusForbidden {
		t.Fatalf("application owner granting = %d, want 403", code)
	}
	// A machine credential never carries admin, whoever minted it.
	code, minted := cReq(t, w.admin, w.ts, "POST", "/api/v1/api-tokens", `{"name":"ci","scope":"full","expiresInDays":1}`)
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("mint token = %d %s", code, minted)
	}
	tok, _ := decodeMap(t, minted)["token"].(string)
	if code, _ := bearerReq(t, w.ts, "PUT", path, body, tok); code != http.StatusForbidden {
		t.Fatalf("machine token granting = %d, want 403", code)
	}

	// The administrator, from their own session.
	code, out := cReq(t, w.admin, w.ts, "PUT", path, body)
	if code != http.StatusOK {
		t.Fatalf("admin grant = %d %s", code, out)
	}
	got := decodeMap(t, out)
	if got["granted"] != true || got["revision"] != float64(1) || got["changedBy"] == "" || got["changedAt"] == float64(0) {
		t.Fatalf("grant answer = %s", out)
	}
	if got["changedBy"] != w.adminID {
		t.Fatalf("changedBy = %v, want the signed-in administrator %s", got["changedBy"], w.adminID)
	}
	// Every refusal on the way left nothing behind but this one change.
	_, hist := cReq(t, w.admin, w.ts, "GET", path+"/history", "")
	if h := decodeMap(t, hist); h["total"] != float64(1) {
		t.Fatalf("history = %s, want exactly the administrator's change", hist)
	}
}

func TestStartGrantsAreCheckedAgainstTheDirectoryAndTheApplication(t *testing.T) {
	w := newGrantWorld(t, t.TempDir())
	defer w.close()
	cases := map[string]struct {
		path, body string
		code       int
	}{
		"unknown application":            {grantPath("prj_doesnotexist", "safety-inspection"), `{"userIds":["` + w.workerID + `"],"expectedRevision":0}`, http.StatusNotFound},
		"process of another application": {grantPath(w.fieldApp, "invoice-check"), `{"userIds":["` + w.workerID + `"],"expectedRevision":0}`, http.StatusNotFound},
		"process never deployed":         {grantPath(w.fieldApp, "nothing-here"), `{"userIds":["` + w.workerID + `"],"expectedRevision":0}`, http.StatusNotFound},
		"unknown user":                   {grantPath(w.fieldApp, "safety-inspection"), `{"userIds":["usr_000000000000000000000000"],"expectedRevision":0}`, http.StatusBadRequest},
		"a username, not an id":          {grantPath(w.fieldApp, "safety-inspection"), `{"userIds":["fieldworker"],"expectedRevision":0}`, http.StatusBadRequest},
		"a group name, not an id":        {grantPath(w.fieldApp, "safety-inspection"), `{"groupIds":["field-inspectors"],"expectedRevision":0}`, http.StatusBadRequest},
		"duplicate audience":             {grantPath(w.fieldApp, "safety-inspection"), `{"userIds":["` + w.workerID + `","` + w.workerID + `"],"expectedRevision":0}`, http.StatusBadRequest},
		"forged actor":                   {grantPath(w.fieldApp, "safety-inspection"), `{"userIds":["` + w.workerID + `"],"expectedRevision":0,"actorId":"usr_x"}`, http.StatusBadRequest},
		"body names another application": {grantPath(w.fieldApp, "safety-inspection"), `{"applicationId":"` + w.otherApp + `","userIds":["` + w.workerID + `"],"expectedRevision":0}`, http.StatusBadRequest},
	}
	for name, c := range cases {
		if code, body := cReq(t, w.admin, w.ts, "PUT", c.path, c.body); code != c.code {
			t.Errorf("%s = %d %s, want %d", name, code, body, c.code)
		}
	}
	_, list := cReq(t, w.admin, w.ts, "GET", "/api/v1/applications/"+w.fieldApp+"/start-grants", "")
	if items := decodeMap(t, list)["items"].([]any); len(items) != 0 {
		t.Fatalf("refused grants were stored: %s", list)
	}
}

func TestStartGrantPreviewRevocationAndRestart(t *testing.T) {
	dir := t.TempDir()
	w := newGrantWorld(t, dir)
	path := grantPath(w.fieldApp, "safety-inspection")
	if code, body := cReq(t, w.admin, w.ts, "PUT", path, `{"groupIds":["`+w.groupID+`"],"expectedRevision":0}`); code != http.StatusOK {
		t.Fatalf("grant = %d %s", code, body)
	}
	_, pv := cReq(t, w.admin, w.ts, "GET", path+"/preview?userId="+w.workerID, "")
	p := decodeMap(t, pv)
	if p["wouldBeOffered"] != true || p["startable"] != true {
		t.Fatalf("preview for a member = %s", pv)
	}
	if matched, _ := p["matchedBy"].([]any); len(matched) != 1 || matched[0] != "group:"+w.groupID {
		t.Fatalf("matchedBy = %v", p["matchedBy"])
	}
	// The process of another application is not this grant's, whatever its id.
	_, other := cReq(t, w.admin, w.ts, "GET", grantPath(w.fieldApp, "invoice-check")+"/preview?userId="+w.workerID, "")
	if o := decodeMap(t, other); o["granted"] != false || o["wouldBeOffered"] != false {
		t.Fatalf("preview of another application's process = %s", other)
	}
	// Leaving the group takes the right away at once.
	if code, body := cReq(t, w.admin, w.ts, "DELETE", "/api/v1/groups/"+w.groupID+"/members/"+w.workerID, ""); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("remove member = %d %s", code, body)
	}
	_, pv = cReq(t, w.admin, w.ts, "GET", path+"/preview?userId="+w.workerID, "")
	if p := decodeMap(t, pv); p["granted"] != false {
		t.Fatalf("preview after leaving the group = %s", pv)
	}
	// A competing administrator's stale change is refused.
	if code, body := cReq(t, w.admin, w.ts, "PUT", path, `{"userIds":["`+w.workerID+`"],"expectedRevision":0}`); code != http.StatusConflict ||
		!strings.Contains(string(body), `"currentRevision":1`) {
		t.Fatalf("stale grant = %d %s, want 409 naming revision 1", code, body)
	}
	if code, body := cReq(t, w.admin, w.ts, "PUT", path, `{"userIds":["`+w.workerID+`"],"expectedRevision":1}`); code != http.StatusOK {
		t.Fatalf("replace = %d %s", code, body)
	}
	code, body := cReq(t, w.admin, w.ts, "DELETE", path+"?expectedRevision=2", "")
	if code != http.StatusOK || decodeMap(t, body)["changed"] != true {
		t.Fatalf("revoke = %d %s", code, body)
	}
	code, body = cReq(t, w.admin, w.ts, "DELETE", path+"?expectedRevision=2", "")
	if code != http.StatusOK || decodeMap(t, body)["changed"] != false {
		t.Fatalf("repeated revoke = %d %s, want 200 unchanged", code, body)
	}
	w.close()

	// A restart reads the grant document back: the revocation stands, the audit is whole.
	ts, closeFn := authServerOn(t, dir, "root", "rootpassword")
	defer closeFn()
	admin := newClient(t)
	if code := login(t, admin, ts, "root", "rootpassword"); code != http.StatusOK {
		t.Fatalf("admin login after restart = %d", code)
	}
	_, cur := cReq(t, admin, ts, "GET", path, "")
	if c := decodeMap(t, cur); c["revision"] != float64(3) || c["granted"] != false {
		t.Fatalf("grant after restart = %s", cur)
	}
	_, hist := cReq(t, admin, ts, "GET", path+"/history", "")
	h := decodeMap(t, hist)
	items, _ := h["items"].([]any)
	if h["total"] != float64(3) || len(items) != 3 {
		t.Fatalf("history after restart = %s", hist)
	}
	actions := []string{}
	for _, it := range items {
		actions = append(actions, it.(map[string]any)["action"].(string))
	}
	if strings.Join(actions, ",") != "revoke,grant,grant" {
		t.Fatalf("history actions = %v, want newest first", actions)
	}
	_, pv = cReq(t, admin, ts, "GET", path+"/preview?userId="+w.workerID, "")
	if p := decodeMap(t, pv); p["granted"] != false || p["wouldBeOffered"] != false {
		t.Fatalf("revoked grant resurrected after restart: %s", pv)
	}
}

// TestStartGrantsNeedAuthentication: with authentication off there is nobody to
// record as the actor, and no self-service route that would read a grant, so the
// area refuses rather than letting an open server hand out start rights.
func TestStartGrantsNeedAuthentication(t *testing.T) {
	ts := newTestServer(t)
	app := mkApp(t, ts, "Open")
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts?projectId="+app, releasableBPMN("open-proc"), "application/xml"); code != http.StatusOK {
		t.Fatalf("save draft = %d %s", code, body)
	}
	if code, _ := publishApp(t, ts, app, ""); code != http.StatusOK {
		t.Fatalf("publish = %d", code)
	}
	for _, c := range []struct{ method, path, body string }{
		{"GET", grantPath(app, "open-proc"), ""},
		{"PUT", grantPath(app, "open-proc"), `{"userIds":["usr_x"],"expectedRevision":0}`},
		{"DELETE", grantPath(app, "open-proc") + "?expectedRevision=0", ""},
		{"GET", "/api/v1/applications/" + app + "/start-grants", ""},
	} {
		if code, body := doReq(t, ts, c.method, c.path, c.body, "application/json"); code != http.StatusForbidden {
			t.Errorf("%s %s without authentication = %d %s, want 403", c.method, c.path, code, body)
		}
	}
}

// TestStartGrantReadsCarryTheSharedBlockers: the list and the single read say, for
// the process's newest version, what the preview and the self-service list would
// say — offered, or why not — with the server's own codes (MFE1A).
func TestStartGrantReadsCarryTheSharedBlockers(t *testing.T) {
	w := newGrantWorld(t, t.TempDir())
	defer w.close()
	path := grantPath(w.fieldApp, "safety-inspection")
	if code, body := cReq(t, w.admin, w.ts, "PUT", path, `{"groupIds":["`+w.groupID+`"],"expectedRevision":0}`); code != http.StatusOK {
		t.Fatalf("grant = %d %s", code, body)
	}
	key, version := newestKey(t, w, "safety-inspection")
	read := func() map[string]any {
		t.Helper()
		_, body := cReq(t, w.admin, w.ts, "GET", path, "")
		return decodeMap(t, body)
	}
	got := read()
	if b, ok := got["blockers"].([]any); !ok || len(b) != 0 || got["definitionKey"] != float64(key) || got["version"] != float64(version) {
		t.Fatalf("eligible grant = %v, want blockers [] and key %d version %d", got, key, version)
	}
	active := "/api/v1/processes/" + jsonNum(key) + "/active"
	if code, body := cReq(t, w.admin, w.ts, "PUT", active, `{"active":false}`); code != http.StatusOK {
		t.Fatalf("deactivate = %d %s", code, body)
	}
	if b, _ := read()["blockers"].([]any); len(b) != 1 || b[0] != "inactive" {
		t.Fatalf("deactivated grant blockers = %v, want [inactive]", b)
	}
	_, list := cReq(t, w.admin, w.ts, "GET", "/api/v1/applications/"+w.fieldApp+"/start-grants", "")
	items, _ := decodeMap(t, list)["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("list = %s", list)
	}
	if b, _ := items[0].(map[string]any)["blockers"].([]any); len(b) != 1 || b[0] != "inactive" {
		t.Fatalf("list blockers = %v, want [inactive]", b)
	}
	// The preview agrees, for a member of the group.
	_, pv := cReq(t, w.admin, w.ts, "GET", path+"/preview?userId="+w.workerID, "")
	if b, _ := decodeMap(t, pv)["blockers"].([]any); len(b) != 1 || b[0] != "inactive" {
		t.Fatalf("preview blockers = %v, want the same as the read", b)
	}
}
