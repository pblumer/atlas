package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// systemPIDsWanted are the process ids the embedded bundle must bootstrap-deploy.
var systemPIDsWanted = []string{
	"proc_benutzer_aufnahme",
	"proc_benutzer_review",
	"proc_benutzer_offboarding",
}

// systemFormsWanted are the form ids the bundle must seed.
var systemFormsWanted = []string{
	"ba-antrag", "ba-konto", "rev-start", "rev-pruefen", "off-antrag", "off-sperren",
}

type procRow struct {
	Key       uint64 `json:"key"`
	ProcessID string `json:"processId"`
	Version   int32  `json:"version"`
}

func listProcesses(t *testing.T, h http.Handler) []procRow {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/processes", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list processes: status=%d", rec.Code)
	}
	var rows []procRow
	if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode processes: %v", err)
	}
	return rows
}

// TestBootstrapDeploysSystemProcesses asserts ADR-0122's bootstrap-deploy: New()
// deploys the embedded platform processes, seeds their forms, files their drafts
// under the protected system project, and does so idempotently — a second run
// adds no new versions.
func TestBootstrapDeploysSystemProcesses(t *testing.T) {
	srv, _ := newSystemServer(t, WithSystemProcesses())
	h := srv.Handler()

	rows := listProcesses(t, h)
	for _, pid := range systemPIDsWanted {
		n := 0
		for _, r := range rows {
			if r.ProcessID == pid {
				n++
				if r.Version != 1 {
					t.Fatalf("%s deployed at version %d, want 1", pid, r.Version)
				}
			}
		}
		if n != 1 {
			t.Fatalf("%s deployed %d times, want exactly 1", pid, n)
		}
	}

	// Forms seeded, filed under the system project.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/forms?projectId="+systemProjectID, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	fbody := rec.Body.String()
	for _, fid := range systemFormsWanted {
		if !strings.Contains(fbody, `"id":"`+fid+`"`) {
			t.Fatalf("system form %q not seeded under system project: %s", fid, fbody)
		}
	}

	// Drafts filed under the system project.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/drafts?projectId="+systemProjectID, nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	dbody := rec.Body.String()
	for _, pid := range systemPIDsWanted {
		if !strings.Contains(dbody, `"processId":"`+pid+`"`) {
			t.Fatalf("system draft %q not filed under system project: %s", pid, dbody)
		}
	}

	// Idempotent: a second ensure deploys nothing new.
	if err := srv.ensureSystemProcesses(999); err != nil {
		t.Fatalf("second ensureSystemProcesses: %v", err)
	}
	rows2 := listProcesses(t, h)
	if len(rows2) != len(rows) {
		t.Fatalf("re-ensure changed deployment count: %d -> %d", len(rows), len(rows2))
	}
}

// TestSystemIntakeProvisionsUser drives the rewired intake process (ADR-0123) end
// to end: start the bootstrap-deployed proc_benutzer_aufnahme, approve the "Antrag
// freigeben" task, and confirm the userConnector created exactly that Atlas login.
// (The downstream mail task parks — no provider is configured — but the account is
// created before it, which is what this asserts.)
func TestSystemIntakeProvisionsUser(t *testing.T) {
	srv, _ := newSystemServer(t, WithSystemProcesses(), WithUserProvisioning())
	h := srv.Handler()

	var key uint64
	for _, r := range listProcesses(t, h) {
		if r.ProcessID == "proc_benutzer_aufnahme" {
			key = r.Key
		}
	}
	if key == 0 {
		t.Fatal("intake process not deployed")
	}
	startInstance(t, h, key, `{"vorname":"Neuer","nachname":"Kollege","email":"neuer@example.org","rolle":"user"}`)

	// Find the waiting "Antrag freigeben" user task.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var tasks []struct {
		Key       uint64 `json:"key"`
		ProcessID string `json:"processId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tasks); err != nil {
		t.Fatalf("decode tasks: %v (%s)", err, rec.Body.String())
	}
	var taskKey uint64
	for _, it := range tasks {
		if it.ProcessID == "proc_benutzer_aufnahme" {
			taskKey = it.Key
		}
	}
	if taskKey == 0 {
		t.Fatal("freigabe task not found")
	}

	// Approve: set the username and initial password, decide "anlegen".
	creq := httptest.NewRequest(http.MethodPost, "/api/v1/tasks/"+strconv.FormatUint(taskKey, 10)+"/complete",
		strings.NewReader(`{"variables":{"benutzername":"neuer.kollege","entscheidung":"anlegen","initialpasswort":"willkommen1"}}`))
	crec := httptest.NewRecorder()
	h.ServeHTTP(crec, creq)
	if crec.Code != http.StatusOK {
		t.Fatalf("complete freigabe task: %d %s", crec.Code, crec.Body.String())
	}

	u, ok, err := srv.users.byUsername("neuer.kollege")
	if err != nil || !ok {
		t.Fatalf("intake did not provision the user (ok=%v err=%v)", ok, err)
	}
	if !u.hasRole(RoleUser) || u.Disabled {
		t.Fatalf("provisioned user = %+v", u.toPublic())
	}
	if !checkPassword(u.PasswordHash, "willkommen1") {
		t.Fatal("provisioned password does not verify")
	}
}

// TestBootstrapDeployIdempotentAcrossRestart proves the checksum guard holds
// across a real restart: reconstructing the server on the same data dir
// re-recovers the deployments and deploys no new versions.
func TestBootstrapDeployIdempotentAcrossRestart(t *testing.T) {
	dir := t.TempDir()

	build := func() (*Server, func()) {
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
		srv, err := New(proc, store, dir, WithSystemProcesses())
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return srv, func() {
			srv.Close()
			store.Close()
			log.Close()
		}
	}

	srv1, close1 := build()
	v1 := listProcesses(t, srv1.Handler())
	close1()

	srv2, close2 := build()
	defer close2()
	v2 := listProcesses(t, srv2.Handler())

	if len(v2) != len(v1) {
		t.Fatalf("restart changed deployment count: %d -> %d (want stable — checksum should skip)", len(v1), len(v2))
	}
	for _, pid := range systemPIDsWanted {
		n := 0
		for _, r := range v2 {
			if r.ProcessID == pid {
				n++
				if r.Version != 1 {
					t.Fatalf("%s at version %d after restart, want 1", pid, r.Version)
				}
			}
		}
		if n != 1 {
			t.Fatalf("%s deployed %d times after restart, want 1", pid, n)
		}
	}
}

// TestDeleteSystemProcessRefused asserts a bootstrap-deployed system process
// cannot be deleted through the API (ADR-0122), even by the implicit owner
// (auth off).
func TestDeleteSystemProcessRefused(t *testing.T) {
	srv, _ := newSystemServer(t, WithSystemProcesses())
	h := srv.Handler()

	var key uint64
	for _, r := range listProcesses(t, h) {
		if r.ProcessID == "proc_benutzer_aufnahme" {
			key = r.Key
		}
	}
	if key == 0 {
		t.Fatal("system process not deployed")
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/processes/"+strconv.FormatUint(key, 10), nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("delete system process = %d, want 403", rec.Code)
	}
}

// TestSystemFormAndProjectDeployGuards covers the remaining protected surfaces:
// a system form cannot be overwritten or deleted, and the protected project
// cannot be deployed through the API — while an ordinary form still saves.
func TestSystemFormAndProjectDeployGuards(t *testing.T) {
	srv, _ := newSystemServer(t, WithSystemProcesses())
	h := srv.Handler()

	do := func(method, path, body string) int {
		var req *http.Request
		if body != "" {
			req = httptest.NewRequest(method, path, strings.NewReader(body))
		} else {
			req = httptest.NewRequest(method, path, nil)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := do(http.MethodPost, "/api/v1/forms", `{"id":"ba-antrag","schema":{"type":"default"}}`); got != http.StatusForbidden {
		t.Fatalf("overwrite system form = %d, want 403", got)
	}
	if got := do(http.MethodDelete, "/api/v1/forms/ba-antrag", ""); got != http.StatusForbidden {
		t.Fatalf("delete system form = %d, want 403", got)
	}
	if got := do(http.MethodPost, "/api/v1/projects/"+systemProjectID+"/deploy", ""); got != http.StatusForbidden {
		t.Fatalf("deploy protected project = %d, want 403", got)
	}
	// An ordinary form still saves.
	if got := do(http.MethodPost, "/api/v1/forms", `{"id":"my-form","schema":{"type":"default"}}`); got != http.StatusOK {
		t.Fatalf("save ordinary form = %d, want 200", got)
	}
}
