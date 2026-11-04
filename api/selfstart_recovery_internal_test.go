package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/startgrant"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// The acceptance scenario of MFE1 (ADR-0516 slice 3), end to end over the deployable
// safety-inspection example: an administrator grants the process to a group, a
// member starts it from their own session, the instance records them as its
// initiator, and all of it survives a restart that replays the log. Revoking stops
// the next start and leaves the started instance running.

const (
	safetyBPMN = "../docs/programs/mfe/safety-inspection/safety-inspection.bpmn"
	safetyForm = "../docs/programs/mfe/safety-inspection/safety-inspection.form.json"
	safetyPID  = "proc_mfe1_safety_inspection"
)

type recoveryWorld struct {
	srv   *Server
	ts    *httptest.Server
	close func()
}

func openRecoveryWorld(t *testing.T, dir string) *recoveryWorld {
	t.Helper()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	srv, err := New(proc, store, dir, WithAuth())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	return &recoveryWorld{srv: srv, ts: ts, close: func() {
		ts.Close()
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	}}
}

func (w *recoveryWorld) client(t *testing.T, user, pass string) *http.Client {
	t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	if code, body := w.call(t, c, "POST", "/api/v1/auth/login", "application/json", `{"username":"`+user+`","password":"`+pass+`"}`); code != http.StatusOK {
		t.Fatalf("login %s = %d %s", user, code, body)
	}
	return c
}

func (w *recoveryWorld) call(t *testing.T, c *http.Client, method, path, contentType, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, w.ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func (w *recoveryWorld) json(t *testing.T, c *http.Client, method, path, body string, want int) map[string]any {
	t.Helper()
	code, out := w.call(t, c, method, path, "application/json", body)
	if code != want {
		t.Fatalf("%s %s = %d %s, want %d", method, path, code, out, want)
	}
	var m map[string]any
	_ = json.Unmarshal(out, &m)
	return m
}

func (w *recoveryWorld) startedBy(t *testing.T, instance uint64) string {
	t.Helper()
	var (
		who string
		ok  bool
		err error
	)
	w.srv.do(func() {
		pi, found, e := w.srv.store.ProcessInstance(instance)
		ok, err = found, e
		if found {
			who = pi.StartedBy
		}
	})
	if err != nil || !ok {
		t.Fatalf("process instance %d: found=%v err=%v", instance, ok, err)
	}
	return who
}

// snapshot is what an operator reads about one instance: its timeline and its
// variables. Built live and rebuilt by replay, the two must be byte-identical.
func (w *recoveryWorld) snapshot(t *testing.T, admin *http.Client, instance uint64) string {
	t.Helper()
	k := strconv.FormatUint(instance, 10)
	var b bytes.Buffer
	for _, path := range []string{"/api/v1/instances/" + k + "/timeline", "/api/v1/instances/" + k + "/variables", "/api/v1/tasks?processInstance=" + k} {
		code, out := w.call(t, admin, "GET", path, "", "")
		if code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, code, out)
		}
		b.Write(out)
	}
	return b.String()
}

func readExample(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func activeCount(t *testing.T, w *recoveryWorld, admin *http.Client) float64 {
	t.Helper()
	n, _ := w.json(t, admin, "GET", "/api/v1/stats", "", http.StatusOK)["activeProcessInstances"].(float64)
	return n
}

func TestSafetyInspectionSelfServiceStartSurvivesRecovery(t *testing.T) {
	t.Setenv("ATLAS_ADMIN_USERNAME", "root")
	t.Setenv("ATLAS_ADMIN_PASSWORD", "rootpassword")
	dir := t.TempDir()
	w := openRecoveryWorld(t, dir)
	admin := w.client(t, "root", "rootpassword")

	// The application, its form and its process, published as the example's README says.
	app, _ := w.json(t, admin, "POST", "/api/v1/applications", `{"name":"Field Operations"}`, http.StatusOK)["id"].(string)
	var schema map[string]any
	if err := json.Unmarshal([]byte(readExample(t, safetyForm)), &schema); err != nil {
		t.Fatalf("form: %v", err)
	}
	formBody, _ := json.Marshal(map[string]any{"id": "mfe1-safety-inspection", "name": "Sicherheitskontrolle", "projectId": app, "schema": schema})
	w.json(t, admin, "POST", "/api/v1/forms", string(formBody), http.StatusOK)
	if code, out := w.call(t, admin, "POST", "/api/v1/drafts?projectId="+app, "application/xml", readExample(t, safetyBPMN)); code != http.StatusOK {
		t.Fatalf("save draft = %d %s", code, out)
	}
	w.json(t, admin, "POST", "/api/v1/applications/"+app+"/publish", `{}`, http.StatusOK)

	// A field inspector and the group the grant names.
	created := w.json(t, admin, "POST", "/api/v1/users", `{"username":"inspector","password":"a-password-that-is-long","roles":["user"]}`, http.StatusCreated)
	inspectorID, _ := created["id"].(string)
	group, _ := w.json(t, admin, "POST", "/api/v1/groups", `{"name":"field-inspectors"}`, http.StatusCreated)["id"].(string)
	if code, out := w.call(t, admin, "PUT", "/api/v1/groups/"+group+"/members/"+inspectorID, "", ""); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("add member = %d %s", code, out)
	}
	inspector := w.client(t, "inspector", "a-password-that-is-long")

	// Scenario A: nothing granted — nothing listed, a direct start refused, nothing created.
	list := w.json(t, inspector, "GET", "/api/v1/me/startable-processes", "", http.StatusOK)
	if items, _ := list["items"].([]any); len(items) != 0 {
		t.Fatalf("listed before any grant: %v", list)
	}
	var key uint64
	w.srv.do(func() { key = w.srv.latestDeploymentByProcessID(safetyPID).Key })
	path := "/api/v1/me/processes/" + strconv.FormatUint(key, 10) + "/instances"
	w.json(t, inspector, "POST", path, `{}`, http.StatusForbidden)
	if n := activeCount(t, w, admin); n != 0 {
		t.Fatalf("a refused start created %v instances", n)
	}

	// Scenario B: the administrator grants the group.
	w.json(t, admin, "PUT", "/api/v1/applications/"+app+"/start-grants/"+safetyPID, `{"groupIds":["`+group+`"],"expectedRevision":0}`, http.StatusOK)
	list = w.json(t, inspector, "GET", "/api/v1/me/startable-processes", "", http.StatusOK)
	items, _ := list["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["key"] != float64(key) || items[0].(map[string]any)["name"] != "Sicherheitskontrolle vor Ort" {
		t.Fatalf("listed after the grant: %v", list)
	}

	// Scenario C: the inspector starts it; the answer comes after the start is durable
	// and names the instance, and the instance records who started it.
	started := w.json(t, inspector, "POST", path, `{}`, http.StatusOK)
	ik, _ := started["instanceKey"].(float64)
	instance := uint64(ik)
	if instance == 0 {
		t.Fatalf("no instance reference: %v", started)
	}
	if who := w.startedBy(t, instance); who != inspectorID {
		t.Fatalf("startedBy = %q, want the inspector %q", who, inspectorID)
	}
	live := w.snapshot(t, admin, instance)
	if !strings.Contains(live, "inspect") {
		t.Fatalf("the instance is not waiting at its inspection task: %s", live)
	}

	// Scenario D: restart. The log is replayed into a fresh server; the instance, its
	// state and its initiator are what they were live.
	w.close()
	w = openRecoveryWorld(t, dir)
	defer w.close()
	admin = w.client(t, "root", "rootpassword")
	if who := w.startedBy(t, instance); who != inspectorID {
		t.Fatalf("startedBy after recovery = %q, want %q", who, inspectorID)
	}
	if replayed := w.snapshot(t, admin, instance); replayed != live {
		t.Fatalf("state after replay differs from the state built live:\nlive:   %s\nreplay: %s", live, replayed)
	}

	// Scenario E: revoke. The next start is refused and creates nothing; the instance
	// already started stays and can be completed.
	w.json(t, admin, "DELETE", "/api/v1/applications/"+app+"/start-grants/"+safetyPID+"?expectedRevision=1", "", http.StatusOK)
	inspector = w.client(t, "inspector", "a-password-that-is-long")
	before := activeCount(t, w, admin)
	w.json(t, inspector, "POST", path, `{}`, http.StatusForbidden)
	if after := activeCount(t, w, admin); after != before || before != 1 {
		t.Fatalf("active instances %v -> %v after a refused start, want 1 and unchanged", before, after)
	}
	tasks := w.json(t, inspector, "GET", "/api/v1/tasks?processInstance="+strconv.FormatUint(instance, 10), "", http.StatusOK)
	taskItems, _ := tasks["items"].([]any)
	if len(taskItems) != 1 {
		t.Fatalf("tasks of the started instance = %v", tasks)
	}
	taskKey := strconv.FormatUint(uint64(taskItems[0].(map[string]any)["key"].(float64)), 10)
	w.json(t, inspector, "POST", "/api/v1/tasks/"+taskKey+"/claim", `{}`, http.StatusOK)
	w.json(t, inspector, "POST", "/api/v1/tasks/"+taskKey+"/complete",
		`{"variables":{"site":"Werk 2","asset":"P-17","inspector":"Inspector","safetyCheckResult":"ok"}}`, http.StatusOK)
	if n := activeCount(t, w, admin); n != 0 {
		t.Fatalf("the started instance did not run to its end after the revocation: %v active", n)
	}
}

// TestSelfServiceGuardRefusesARevocationDurableBeforeTheCommand pins the ordering
// ADR-0516 settles on, deterministically. The handler's first check passed; then the
// revocation became durable; the guard in the turn that submits the command must
// refuse, and nothing may be created.
func TestSelfServiceGuardRefusesARevocationDurableBeforeTheCommand(t *testing.T) {
	t.Setenv("ATLAS_ADMIN_USERNAME", "root")
	t.Setenv("ATLAS_ADMIN_PASSWORD", "rootpassword")
	w := openRecoveryWorld(t, t.TempDir())
	defer w.close()
	admin := w.client(t, "root", "rootpassword")
	app, _ := w.json(t, admin, "POST", "/api/v1/applications", `{"name":"Field Operations"}`, http.StatusOK)["id"].(string)
	bpmn := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="p" isExecutable="true"><startEvent id="s"/><userTask id="t"/><endEvent id="e"/><sequenceFlow id="f1" sourceRef="s" targetRef="t"/><sequenceFlow id="f2" sourceRef="t" targetRef="e"/></process></definitions>`
	if code, out := w.call(t, admin, "POST", "/api/v1/deployments?projectId="+app, "application/xml", bpmn); code != http.StatusOK {
		t.Fatalf("deploy = %d %s", code, out)
	}
	userID, _ := w.json(t, admin, "POST", "/api/v1/users", `{"username":"u","password":"a-password-that-is-long","roles":["user"]}`, http.StatusCreated)["id"].(string)
	w.json(t, admin, "PUT", "/api/v1/applications/"+app+"/start-grants/p", `{"userIds":["`+userID+`"],"expectedRevision":0}`, http.StatusOK)

	var key uint64
	w.srv.do(func() { key = w.srv.latestDeploymentByProcessID("p").Key })
	principal := startgrant.Principal{UserID: userID}
	var first int
	w.srv.do(func() { first, _ = w.srv.selfStartDecisionOnLoop(w.srv.deployments[key], principal) })
	if first != 0 {
		t.Fatalf("authorized before the revocation = %d", first)
	}
	w.json(t, admin, "DELETE", "/api/v1/applications/"+app+"/start-grants/p?expectedRevision=1", "", http.StatusOK)

	r := httptest.NewRequest(http.MethodPost, "/api/v1/me/processes/"+strconv.FormatUint(key, 10)+"/instances", nil)
	r = r.WithContext(httpapi.WithPrincipal(r.Context(), &httpapi.Principal{UserID: userID, Roles: []string{RoleUser}, Session: true}))
	rec := httptest.NewRecorder()
	w.srv.startInstanceGuarded(rec, r, key, nil, func(d *deployment) (int, string) {
		return w.srv.selfStartDecisionOnLoop(d, principal)
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("command turn after a durable revocation = %d %s, want 403", rec.Code, rec.Body.String())
	}
	if n := activeCount(t, w, admin); n != 0 {
		t.Fatalf("the refused command created %v instances", n)
	}
}
