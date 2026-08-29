package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// What a signed-in identity may do (ADR-0209, measure
// M9). Each test here is a line from the concept's acceptance list.
//
// The one worth reading twice is the upgrade: an installation that is running work
// must be able to take this version without an administrator standing by, so an
// account written before roles keeps exactly what it had — and a narrowing made
// afterwards has to survive the next restart, or the upgrade rule quietly undoes
// every decision an operator makes.

// authServerOn is newAuthServer against a directory the caller owns, so a test can
// close the server and open a second one over the same data — the only way to
// state what a restart does.
func authServerOn(t *testing.T, dir, adminUser, adminPass string) (*httptest.Server, func()) {
	t.Helper()
	t.Setenv("ATLAS_ADMIN_USERNAME", adminUser)
	t.Setenv("ATLAS_ADMIN_PASSWORD", adminPass)
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
	srv, err := api.New(proc, store, dir, api.WithAuth())
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	return ts, func() {
		ts.Close()
		srv.Close()
		_ = store.Close()
		_ = log.Close()
	}
}

// rolesOf reads an account's roles straight off the store, which is where the
// upgrade writes them — the API projection would answer the same, but through the
// admin route this is asserting about.
func rolesOf(t *testing.T, dir, username string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "users"))
	if err != nil {
		t.Fatalf("read users dir: %v", err)
	}
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, "users", e.Name()))
		if err != nil {
			continue
		}
		var u struct {
			Username string   `json:"username"`
			Roles    []string `json:"roles"`
		}
		if json.Unmarshal(data, &u) == nil && u.Username == username {
			return u.Roles
		}
	}
	t.Fatalf("no stored account named %q", username)
	return nil
}

// TestOnlyAModelerMayDeploy is the measure in one sentence. Deploying a model is
// code execution (risk R-09), and before this every account with a login could do
// it.
func TestOnlyAModelerMayDeploy(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if code := login(t, admin, ts, "root", "rootpassword"); code != http.StatusOK {
		t.Fatalf("admin login = %d", code)
	}
	createUserWithRoles(t, admin, ts.URL, "tina", `["user"]`)
	createUserWithRoles(t, admin, ts.URL, "mona", `["modeler"]`)

	tina := signInAs(t, ts.URL, "tina", "a-password-that-is-long")
	code, body := cReq(t, tina, ts, "POST", "/api/v1/deployments", sampleBPMN)
	if code != http.StatusForbidden {
		t.Errorf("a task worker deploying = %d, want 403", code)
	}
	if !strings.Contains(string(body), "modeler") {
		t.Errorf("refusal = %s, want it to name the role that is missing", body)
	}

	mona := signInAs(t, ts.URL, "mona", "a-password-that-is-long")
	if code, body := cReq(t, mona, ts, "POST", "/api/v1/deployments", sampleBPMN); code != http.StatusOK {
		t.Errorf("a modeller deploying = %d (%s), want 200", code, body)
	}
}

// TestARoleIsNotARank. The roles are a list, not a ladder: a modeller does not get
// the operator's reach by being further up, and an administrator gets both by
// being the one role that is a superset.
func TestARoleIsNotARank(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if code := login(t, admin, ts, "root", "rootpassword"); code != http.StatusOK {
		t.Fatalf("admin login = %d", code)
	}
	createUserWithRoles(t, admin, ts.URL, "mona", `["modeler"]`)
	createUserWithRoles(t, admin, ts.URL, "tina", `["user"]`)

	mona := signInAs(t, ts.URL, "mona", "a-password-that-is-long")
	if code, _ := cReq(t, mona, ts, "GET", "/api/v1/instances", ""); code != http.StatusForbidden {
		t.Errorf("a modeller reading the instance list = %d, want 403", code)
	}
	tina := signInAs(t, ts.URL, "tina", "a-password-that-is-long")
	if code, _ := cReq(t, tina, ts, "GET", "/api/v1/tasks", ""); code != http.StatusOK {
		t.Errorf("a task worker reading their tasks = %d, want 200", code)
	}
	if code, _ := cReq(t, admin, ts, "GET", "/api/v1/instances", ""); code != http.StatusOK {
		t.Errorf("an administrator reading the instance list = %d, want 200", code)
	}
	// And what nobody's role changes: a route every signed-in identity reaches.
	if code, _ := cReq(t, tina, ts, "GET", "/api/v1/processes", ""); code != http.StatusOK {
		t.Errorf("a task worker listing processes = %d, want 200", code)
	}
	// Reading one instance's variables is deliberately among those, because a task
	// form is prefilled from them: an operator-only rule here would hand the person
	// the task is for an empty form. The instance does not exist, so the answer is a
	// 404 — what matters is that it is not a refusal.
	if code, _ := cReq(t, tina, ts, "GET", "/api/v1/instances/1/variables", ""); code == http.StatusForbidden {
		t.Error("a task worker was refused the variables their own task's form is filled from")
	}
}

// TestAnAccountFromBeforeRolesKeepsWhatItCouldDo is the upgrade rule. The record is
// written the way the previous version wrote it — roles ["user"], no marker — and
// the account must be able to do on the next start exactly what it could on the
// last one.
func TestAnAccountFromBeforeRolesKeepsWhatItCouldDo(t *testing.T) {
	dir := t.TempDir()
	ts, closeFirst := authServerOn(t, dir, "root", "rootpassword")
	admin := newClient(t)
	if code := login(t, admin, ts, "root", "rootpassword"); code != http.StatusOK {
		t.Fatalf("admin login = %d", code)
	}
	createUserWithRoles(t, admin, ts.URL, "olli", `["user"]`)
	closeFirst()

	// Age the record: strip the marker the way a file written by the previous
	// version has it. Nothing else about it changes.
	unmarkRoles(t, dir, "olli")

	ts, closeSecond := authServerOn(t, dir, "root", "rootpassword")
	defer closeSecond()

	if got := strings.Join(rolesOf(t, dir, "olli"), " "); got != "user modeler operator" {
		t.Errorf("after the upgrade olli holds %q, want the three roles that describe what they could already do", got)
	}
	olli := signInAs(t, ts.URL, "olli", "a-password-that-is-long")
	if code, body := cReq(t, olli, ts, "POST", "/api/v1/deployments", sampleBPMN); code != http.StatusOK {
		t.Errorf("an account from before roles deploying = %d (%s), want 200 — the upgrade took something away", code, body)
	}
	// And still not an administrator, which is the one thing it could not do before.
	if code, _ := cReq(t, olli, ts, "GET", "/api/v1/users", ""); code != http.StatusForbidden {
		t.Errorf("the upgrade made an ordinary account an administrator (%d)", code)
	}
}

// TestNarrowingAnAccountSurvivesARestart is the other half, and the one that makes
// the upgrade a one-off rather than a policy. An operator who narrows an account
// must not find it widened again by the next restart.
func TestNarrowingAnAccountSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	ts, closeFirst := authServerOn(t, dir, "root", "rootpassword")
	admin := newClient(t)
	if code := login(t, admin, ts, "root", "rootpassword"); code != http.StatusOK {
		t.Fatalf("admin login = %d", code)
	}
	id := createUserWithRoles(t, admin, ts.URL, "olli", `["user"]`)
	unmarkRoles(t, dir, "olli")
	// The operator narrows the account back to tasks only, after the upgrade widened
	// it — which is exactly the sequence the record says an installation will follow.
	patchUser(t, admin, ts.URL, id, `{"roles":["user"]}`)
	closeFirst()

	ts, closeSecond := authServerOn(t, dir, "root", "rootpassword")
	defer closeSecond()
	if got := strings.Join(rolesOf(t, dir, "olli"), " "); got != "user" {
		t.Errorf("after a restart olli holds %q, want the narrowing to have held", got)
	}
	olli := signInAs(t, ts.URL, "olli", "a-password-that-is-long")
	if code, _ := cReq(t, olli, ts, "POST", "/api/v1/deployments", sampleBPMN); code != http.StatusForbidden {
		t.Errorf("a narrowed account deploying = %d, want 403", code)
	}
}

// unmarkRoles rewrites a stored account the way the version before roles wrote it:
// the same roles, without the marker that says they were written under the model.
func unmarkRoles(t *testing.T, dir, username string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, "users"))
	if err != nil {
		t.Fatalf("read users dir: %v", err)
	}
	for _, e := range entries {
		path := filepath.Join(dir, "users", e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var rec map[string]any
		if json.Unmarshal(data, &rec) != nil || rec["username"] != username {
			continue
		}
		delete(rec, "rolesUpgradedAt")
		out, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("encode %s: %v", username, err)
		}
		if err := os.WriteFile(path, out, 0o600); err != nil {
			t.Fatalf("write %s: %v", username, err)
		}
		return
	}
	t.Fatalf("no stored account named %q", username)
}

// TestAnAPITokenIsNeverAnAdministrator. Only an administrator can mint one, so
// snapshotting the minter verbatim would hand every machine the whole instance.
func TestAnAPITokenIsNeverAnAdministrator(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if code := login(t, admin, ts, "root", "rootpassword"); code != http.StatusOK {
		t.Fatalf("admin login = %d", code)
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/api-tokens",
		`{"name":"ci","scope":"full","expiresInDays":30}`)
	if code != http.StatusOK {
		t.Fatalf("mint = %d (%s)", code, body)
	}
	var minted struct{ Token string }
	if err := json.Unmarshal(body, &minted); err != nil || minted.Token == "" {
		t.Fatalf("no token in %s", body)
	}

	withToken := func(method, path, payload string) int {
		t.Helper()
		var r *strings.Reader
		if payload != "" {
			r = strings.NewReader(payload)
		}
		var req *http.Request
		var err error
		if r != nil {
			req, err = http.NewRequest(method, ts.URL+path, r)
		} else {
			req, err = http.NewRequest(method, ts.URL+path, nil)
		}
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+minted.Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	// It does what a machine is minted for.
	if got := withToken("POST", "/api/v1/deployments", sampleBPMN); got != http.StatusOK {
		t.Errorf("token deploying = %d, want 200", got)
	}
	// And not what its minter can do.
	if got := withToken("GET", "/api/v1/users", ""); got != http.StatusForbidden {
		t.Errorf("token listing accounts = %d, want 403", got)
	}
}

// TestAuthOffEnforcesNoRole. With --auth=false there is no principal to hold a
// role, and a rule enforced against nobody is a rule that breaks single-user mode
// for nothing.
func TestAuthOffEnforcesNoRole(t *testing.T) {
	ts := newTestServer(t)
	c := newClient(t)
	if code, body := cReq(t, c, ts, "POST", "/api/v1/deployments", sampleBPMN); code != http.StatusOK {
		t.Errorf("deploy with auth off = %d (%s), want 200", code, body)
	}
	if code, _ := cReq(t, c, ts, "GET", "/api/v1/users", ""); code != http.StatusOK {
		t.Errorf("admin route with auth off = %d, want 200", code)
	}
}
