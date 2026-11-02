package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// GET /api/v1/me/startable-processes (ADR-0516): the processes the signed-in person
// may start now — explicitly granted to them or to a group they are in, the newest
// deployed version of the process, filed under the grant's application, and
// startable. Nothing else, and nothing about anything else.

type startableItem struct {
	Key             uint64 `json:"key"`
	ProcessID       string `json:"processId"`
	Name            string `json:"name"`
	Version         int32  `json:"version"`
	ApplicationID   string `json:"applicationId"`
	ApplicationName string `json:"applicationName"`
	StartForm       struct {
		Available bool   `json:"available"`
		FormID    string `json:"formId"`
	} `json:"startForm"`
}

type startablePage struct {
	Items     []startableItem `json:"items"`
	Total     int             `json:"total"`
	Truncated bool            `json:"truncated"`
}

func myStartable(t *testing.T, w *grantWorld, c *http.Client) (startablePage, string) {
	t.Helper()
	code, body := cReq(t, c, w.ts, "GET", "/api/v1/me/startable-processes", "")
	if code != http.StatusOK {
		t.Fatalf("startable processes = %d %s", code, body)
	}
	var p startablePage
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return p, string(body)
}

// grant issues a start grant as the administrator, on whatever revision is current.
func (w *grantWorld) grant(t *testing.T, app, process, body string) {
	t.Helper()
	path := grantPath(app, process)
	_, cur := cReq(t, w.admin, w.ts, "GET", path, "")
	var c struct {
		Revision uint64 `json:"revision"`
	}
	_ = json.Unmarshal(cur, &c)
	body = strings.TrimSuffix(strings.TrimSpace(body), "}") + `,"expectedRevision":` + jsonNum(c.Revision) + "}"
	if code, out := cReq(t, w.admin, w.ts, "PUT", path, body); code != http.StatusOK {
		t.Fatalf("grant %s/%s = %d %s", app, process, code, out)
	}
}

func (w *grantWorld) revoke(t *testing.T, app, process string) {
	t.Helper()
	path := grantPath(app, process)
	_, cur := cReq(t, w.admin, w.ts, "GET", path, "")
	var c struct {
		Revision uint64 `json:"revision"`
	}
	_ = json.Unmarshal(cur, &c)
	if code, out := cReq(t, w.admin, w.ts, "DELETE", path+"?expectedRevision="+jsonNum(c.Revision), ""); code != http.StatusOK {
		t.Fatalf("revoke %s/%s = %d %s", app, process, code, out)
	}
}

func jsonNum(n uint64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// newestKey is the definition key of the newest deployed version of a process.
func newestKey(t *testing.T, w *grantWorld, processID string) (uint64, int32) {
	t.Helper()
	_, body := cReq(t, w.admin, w.ts, "GET", "/api/v1/processes", "")
	var all []struct {
		Key       uint64 `json:"key"`
		ProcessID string `json:"processId"`
		Version   int32  `json:"version"`
	}
	if err := json.Unmarshal(body, &all); err != nil {
		t.Fatalf("decode processes: %v", err)
	}
	var key uint64
	var version int32
	for _, p := range all {
		if p.ProcessID == processID && p.Version >= version {
			key, version = p.Key, p.Version
		}
	}
	if key == 0 {
		t.Fatalf("no deployed %s", processID)
	}
	return key, version
}

func TestStartableProcessesListOnlyWhatIsGranted(t *testing.T) {
	w := newGrantWorld(t, t.TempDir())
	defer w.close()

	if p, raw := myStartable(t, w, w.worker); len(p.Items) != 0 || p.Total != 0 {
		t.Fatalf("no grants yet, but listed %s", raw)
	}

	// A group grant.
	w.grant(t, w.fieldApp, "safety-inspection", `{"groupIds":["`+w.groupID+`"]}`)
	key, version := newestKey(t, w, "safety-inspection")
	p, raw := myStartable(t, w, w.worker)
	if len(p.Items) != 1 {
		t.Fatalf("group grant = %s, want one process", raw)
	}
	it := p.Items[0]
	if it.Key != key || it.Version != version || it.ProcessID != "safety-inspection" ||
		it.ApplicationID != w.fieldApp || it.ApplicationName != "Field Operations" || it.StartForm.Available {
		t.Fatalf("listed item = %+v", it)
	}
	if it.Name == "" {
		t.Fatalf("listed item has no display name: %+v", it)
	}

	// A new version replaces the old one: never two versions, never the superseded one.
	publishProcessInto(t, w.admin, w.ts, w.fieldApp, "safety-inspection")
	key2, version2 := newestKey(t, w, "safety-inspection")
	p, raw = myStartable(t, w, w.worker)
	if len(p.Items) != 1 || p.Items[0].Key != key2 || p.Items[0].Version != version2 || key2 == key {
		t.Fatalf("after a redeploy = %s, want only version %d (key %d)", raw, version2, key2)
	}

	// A direct user grant in another application; the list is sorted by application.
	w.grant(t, w.otherApp, "invoice-check", `{"userIds":["`+w.workerID+`"]}`)
	p, raw = myStartable(t, w, w.worker)
	if len(p.Items) != 2 || p.Items[0].ApplicationName != "Back Office" || p.Items[1].ApplicationName != "Field Operations" {
		t.Fatalf("two grants = %s, want Back Office before Field Operations", raw)
	}
	// Somebody else sees none of it.
	if p, raw := myStartable(t, w, w.modeler); len(p.Items) != 0 {
		t.Fatalf("an ungranted person sees %s", raw)
	}

	// Leaving the group takes the group's grant away from the next request.
	if code, body := cReq(t, w.admin, w.ts, "DELETE", "/api/v1/groups/"+w.groupID+"/members/"+w.workerID, ""); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("remove member = %d %s", code, body)
	}
	p, raw = myStartable(t, w, w.worker)
	if len(p.Items) != 1 || p.Items[0].ProcessID != "invoice-check" {
		t.Fatalf("after leaving the group = %s", raw)
	}

	// Revoking takes the direct grant away.
	w.revoke(t, w.otherApp, "invoice-check")
	p, raw = myStartable(t, w, w.worker)
	if len(p.Items) != 0 || strings.Contains(raw, "invoice-check") || strings.Contains(raw, "safety-inspection") || strings.Contains(raw, "owned-process") {
		t.Fatalf("after revoking = %s, want nothing at all", raw)
	}
}

func TestStartableProcessesHideWhatCannotBeStarted(t *testing.T) {
	w := newGrantWorld(t, t.TempDir())
	defer w.close()
	w.grant(t, w.fieldApp, "safety-inspection", `{"userIds":["`+w.workerID+`"]}`)
	if p, raw := myStartable(t, w, w.worker); len(p.Items) != 1 {
		t.Fatalf("granted = %s", raw)
	}

	// Deactivated (ADR-0119): not offered; reactivated: offered again.
	key, _ := newestKey(t, w, "safety-inspection")
	active := "/api/v1/processes/" + jsonNum(key) + "/active"
	if code, body := cReq(t, w.admin, w.ts, "PUT", active, `{"active":false}`); code != http.StatusOK {
		t.Fatalf("deactivate = %d %s", code, body)
	}
	if p, raw := myStartable(t, w, w.worker); len(p.Items) != 0 {
		t.Fatalf("deactivated process listed: %s", raw)
	}
	if code, body := cReq(t, w.admin, w.ts, "PUT", active, `{"active":true}`); code != http.StatusOK {
		t.Fatalf("reactivate = %d %s", code, body)
	}
	if p, raw := myStartable(t, w, w.worker); len(p.Items) != 1 {
		t.Fatalf("reactivated process not listed: %s", raw)
	}

	// The newest version is not executable: nothing is offered, not the older one either.
	nonExec := strings.Replace(releasableBPMN("safety-inspection"), `isExecutable="true"`, `isExecutable="false"`, 1)
	if code, body := cReqTyped(t, w.admin, w.ts, "POST", "/api/v1/deployments?projectId="+w.fieldApp, "application/xml", nonExec); code != http.StatusOK {
		t.Fatalf("deploy non-executable = %d %s", code, body)
	}
	if p, raw := myStartable(t, w, w.worker); len(p.Items) != 0 {
		t.Fatalf("a superseded version or a non-executable one is listed: %s", raw)
	}

	// The process id's newest version is filed under another application now: the
	// grant on this application does not follow it there.
	if code, body := cReqTyped(t, w.admin, w.ts, "POST", "/api/v1/deployments?projectId="+w.otherApp, "application/xml", releasableBPMN("safety-inspection")); code != http.StatusOK {
		t.Fatalf("deploy into another application = %d %s", code, body)
	}
	if p, raw := myStartable(t, w, w.worker); len(p.Items) != 0 {
		t.Fatalf("a grant followed its process id into another application: %s", raw)
	}
	if code, body := cReqTyped(t, w.admin, w.ts, "POST", "/api/v1/deployments?projectId="+w.fieldApp, "application/xml", releasableBPMN("safety-inspection")); code != http.StatusOK {
		t.Fatalf("redeploy into its application = %d %s", code, body)
	}
	if p, raw := myStartable(t, w, w.worker); len(p.Items) != 1 {
		t.Fatalf("back in its application, not listed: %s", raw)
	}

	// The application is deleted: its grant offers nothing.
	if code, body := cReq(t, w.admin, w.ts, "DELETE", "/api/v1/applications/"+w.fieldApp, ""); code != http.StatusNoContent && code != http.StatusOK {
		t.Fatalf("delete application = %d %s", code, body)
	}
	if p, raw := myStartable(t, w, w.worker); len(p.Items) != 0 {
		t.Fatalf("a deleted application's process is listed: %s", raw)
	}
}

func TestStartableProcessesNeedAPersonsOwnSession(t *testing.T) {
	w := newGrantWorld(t, t.TempDir())
	defer w.close()
	w.grant(t, w.fieldApp, "safety-inspection", `{"userIds":["`+w.workerID+`"]}`)

	if code, _ := cReq(t, newClient(t), w.ts, "GET", "/api/v1/me/startable-processes", ""); code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d, want 401", code)
	}
	// A machine credential carries the user role, but is not a person.
	code, minted := cReq(t, w.admin, w.ts, "POST", "/api/v1/api-tokens", `{"name":"ci","scope":"full","expiresInDays":1}`)
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("mint token = %d %s", code, minted)
	}
	tok, _ := decodeMap(t, minted)["token"].(string)
	if code, body := bearerReq(t, w.ts, "GET", "/api/v1/me/startable-processes", "", tok); code != http.StatusForbidden {
		t.Fatalf("machine token = %d %s, want 403", code, body)
	}
	// An account without the user role does not reach the route, grant or not.
	opsID := createUserWithRoles(t, w.admin, w.ts.URL, "opsonly", `["operator"]`)
	w.grant(t, w.fieldApp, "safety-inspection", `{"userIds":["`+w.workerID+`","`+opsID+`"]}`)
	ops := signInAs(t, w.ts.URL, "opsonly", "a-password-that-is-long")
	if code, _ := cReq(t, ops, w.ts, "GET", "/api/v1/me/startable-processes", ""); code != http.StatusForbidden {
		t.Fatalf("account without the user role = %d, want 403", code)
	}
	// A session that ended is no session.
	if code, _ := cReq(t, w.worker, w.ts, "POST", "/api/v1/auth/logout", ""); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("logout = %d", code)
	}
	if code, _ := cReq(t, w.worker, w.ts, "GET", "/api/v1/me/startable-processes", ""); code != http.StatusUnauthorized {
		t.Fatalf("after logout = %d, want 401", code)
	}
	// A disabled account's sessions end with it.
	worker2 := signInAs(t, w.ts.URL, "fieldworker", "a-password-that-is-long")
	if code, body := cReq(t, w.admin, w.ts, "PATCH", "/api/v1/users/"+w.workerID, `{"disabled":true}`); code != http.StatusOK {
		t.Fatalf("disable = %d %s", code, body)
	}
	if code, _ := cReq(t, worker2, w.ts, "GET", "/api/v1/me/startable-processes", ""); code != http.StatusUnauthorized {
		t.Fatalf("disabled account = %d, want 401", code)
	}
}

// TestStartableProcessesAreNotOfferedWithoutAuthentication: an open server keeps its
// operator Start surface and offers no self-service route (ADR-0516).
func TestStartableProcessesAreNotOfferedWithoutAuthentication(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/me/startable-processes", "", "")
	if code != http.StatusNotFound || !strings.Contains(string(body), "needs authentication") {
		t.Fatalf("open server = %d %s, want 404 saying it needs authentication", code, body)
	}
}
