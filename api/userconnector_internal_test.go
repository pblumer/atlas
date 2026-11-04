package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// userConnBPMN wraps a <atlas:userConnector> extension in a minimal Start → task →
// End process with the given process id.
func userConnBPMN(procID, inner string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs">
  <bpmn:process id="` + procID + `" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t"><bpmn:extensionElements>` + inner + `</bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
}

func deployBPMN(t *testing.T, h http.Handler, xml string) uint64 {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/deployments", strings.NewReader(xml))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	return resp.Key
}

// deploySystemBPMN deploys xml as atlas deploys its own processes: into the protected
// system project, the only place a system process id may be deployed and the only
// deployment the provisioning gate accepts (systemprocessguard.go).
func deploySystemBPMN(t *testing.T, srv *Server, xml string) uint64 {
	t.Helper()
	var (
		dps           []deployedProcess
		compErr, pErr error
	)
	srv.do(func() { dps, compErr, pErr = srv.deployModel([]byte(xml), nil, time.Now().Unix(), systemProjectID, "") })
	if compErr != nil || pErr != nil || len(dps) != 1 {
		t.Fatalf("deploy into the system project: %v / %v (%d definitions)", compErr, pErr, len(dps))
	}
	return dps[0].Key
}

func startInstance(t *testing.T, h http.Handler, key uint64, varsJSON string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/processes/"+strconv.FormatUint(key, 10)+"/instances",
		strings.NewReader(`{"variables":`+varsJSON+`}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

const createInner = `<atlas:userConnector operation="create" username="=username" email="=email" displayName="=displayName" roles="user"/>`

// TestUserConnectorCreatesUser is the ADR-0123 happy path: a system process with a
// create userConnector task, run with start variables, provisions exactly the
// account an admin would — right username and roles — and no password: it awaits
// activation by its owner (ADR-0526).
func TestUserConnectorCreatesUser(t *testing.T) {
	srv, _ := newSystemServer(t, WithUserProvisioning())
	srv.systemPIDs = map[string]bool{"sysproc": true} // gate: treat this as a system process
	h := srv.Handler()

	key := deploySystemBPMN(t, srv, userConnBPMN("sysproc", createInner))
	startInstance(t, h, key, `{"username":"erika.muster","email":"erika@example.org","displayName":"Erika Muster"}`)

	u, ok, err := srv.users.byUsername("erika.muster")
	if err != nil || !ok {
		t.Fatalf("user not provisioned (ok=%v err=%v)", ok, err)
	}
	if u.Email != "erika@example.org" || u.DisplayName != "Erika Muster" || u.Disabled {
		t.Fatalf("provisioned user = %+v", u.toPublic())
	}
	if !u.hasRole(RoleUser) {
		t.Fatalf("roles = %v, want [user]", u.Roles)
	}
	if u.PasswordHash != "" || !u.toPublic().AwaitingActivation {
		t.Fatal("a provisioned account carries a password; it must await activation by its owner")
	}
}

// TestUserConnectorIdempotent: running create twice for the same username yields one
// account (an at-least-once retry must not double-create).
func TestUserConnectorIdempotent(t *testing.T) {
	srv, _ := newSystemServer(t, WithUserProvisioning())
	srv.systemPIDs = map[string]bool{"sysproc": true}
	h := srv.Handler()
	key := deploySystemBPMN(t, srv, userConnBPMN("sysproc", createInner))

	vars := `{"username":"dupe","email":"dupe@example.org","displayName":"Dupe"}`
	startInstance(t, h, key, vars)
	startInstance(t, h, key, vars)

	all, err := srv.users.LoadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	n := 0
	for _, u := range all {
		if u.Username == "dupe" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("dupe provisioned %d times, want 1 (idempotent)", n)
	}
}

// TestUserConnectorGatedToSystemProject: a process that is NOT a system process
// cannot provision — the worker refuses and the account is never created.
func TestUserConnectorGatedToSystemProject(t *testing.T) {
	srv, _ := newSystemServer(t, WithUserProvisioning())
	srv.systemPIDs = map[string]bool{"sysproc": true} // "tenant" is NOT in the set
	h := srv.Handler()

	key := deployBPMN(t, h, userConnBPMN("tenant", createInner))
	startInstance(t, h, key, `{"username":"sneaky","email":"x@example.org","displayName":"X"}`)

	if _, ok, _ := srv.users.byUsername("sneaky"); ok {
		t.Fatal("a non-system process provisioned a user; gating failed")
	}
}

// TestUserConnectorRequiresOptIn: without WithUserProvisioning there is no worker,
// so the job parks and no account is created — the opt-in default is safe.
func TestUserConnectorRequiresOptIn(t *testing.T) {
	srv, _ := newSystemServer(t) // provisioning NOT enabled
	srv.systemPIDs = map[string]bool{"sysproc": true}
	h := srv.Handler()

	key := deploySystemBPMN(t, srv, userConnBPMN("sysproc", createInner))
	startInstance(t, h, key, `{"username":"noworker","email":"x@example.org","displayName":"X"}`)

	if _, ok, _ := srv.users.byUsername("noworker"); ok {
		t.Fatal("provisioning happened without the opt-in")
	}
}

// TestProvisionOpsRails drives the provisioning operations' guards directly: the
// reused admin-API rails (email uniqueness, last-admin lockout)
// and the not-found / idempotent-disable branches.
func TestProvisionOpsRails(t *testing.T) {
	srv := newServerForErrors(t)
	const now = 1000

	// create u1 (as an admin, for the lockout test), then a second create is idempotent.
	if _, created, err := srv.provisionCreateUser("u1", "u1@example.org", "U1", []string{RoleAdmin}, now); err != nil || !created {
		t.Fatalf("create u1: created=%v err=%v", created, err)
	}
	if _, created, err := srv.provisionCreateUser("u1", "", "", nil, now); err != nil || created {
		t.Fatalf("re-create u1: want created=false nil err, got created=%v err=%v", created, err)
	}
	// create with an already-taken email is refused.
	if _, _, err := srv.provisionCreateUser("u2", "u1@example.org", "", nil, now); err == nil {
		t.Fatal("duplicate email: want error")
	}
	// disable: unknown user, last-admin guard, then idempotent re-disable of a
	// non-admin. u1 is the only enabled admin, so disabling it is refused.
	if err := srv.provisionDisableUser("ghost", now); err == nil {
		t.Fatal("disable unknown user: want error")
	}
	if err := srv.provisionDisableUser("u1", now); err == nil {
		t.Fatal("disable last admin: want error")
	}
	// A plain user disables, and disabling again is an idempotent no-op.
	if _, _, err := srv.provisionCreateUser("plain", "", "", nil, now); err != nil {
		t.Fatalf("create plain: %v", err)
	}
	if err := srv.provisionDisableUser("plain", now); err != nil {
		t.Fatalf("disable plain: %v", err)
	}
	if err := srv.provisionDisableUser("plain", now); err != nil {
		t.Fatalf("re-disable plain (idempotent): %v", err)
	}
}

// TestUserConnectorEmptyUsername covers the handler's empty-username guard: when
// the username field resolves to nothing (an unbound FEEL variable), no account
// is created.
func TestUserConnectorEmptyUsername(t *testing.T) {
	srv, _ := newSystemServer(t, WithUserProvisioning())
	srv.systemPIDs = map[string]bool{"sysproc": true}
	h := srv.Handler()

	key := deploySystemBPMN(t, srv, userConnBPMN("sysproc",
		`<atlas:userConnector operation="create" username="=fehlt" email="x@example.org" displayName="X" roles="user"/>`))
	startInstance(t, h, key, `{}`)

	all, err := srv.users.LoadAll()
	if err != nil {
		t.Fatalf("loadAll: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("empty username provisioned %d users, want 0", len(all))
	}
}

// TestUserConnectorHandlerOpErrors drives the handler's operation-error branches end
// to end: a create that fails its rails (an address already taken) and a disable of a
// user that does not exist.
func TestUserConnectorHandlerOpErrors(t *testing.T) {
	srv, _ := newSystemServer(t, WithUserProvisioning())
	srv.systemPIDs = map[string]bool{"sysproc": true}
	h := srv.Handler()

	if _, _, err := srv.provisionCreateUser("owner", "x@example.org", "", nil, 1); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	badCreate := deploySystemBPMN(t, srv, userConnBPMN("sysproc",
		`<atlas:userConnector operation="create" username="x" email="x@example.org"/>`))
	startInstance(t, h, badCreate, `{}`)

	missingDisable := deploySystemBPMN(t, srv, userConnBPMN("sysproc",
		`<atlas:userConnector operation="disable" username="ghost"/>`))
	startInstance(t, h, missingDisable, `{}`)

	// None of these created an account; the point is the handler ran each op's
	// error branch (the jobs fail and raise incidents, which is fine here).
	if all, _ := srv.users.LoadAll(); len(all) != 1 {
		t.Fatalf("op-error runs provisioned %d users, want only the seeded one", len(all))
	}
}

// TestUserConnectorRefusesPasswords holds a process to the rule that it never carries
// a password (ADR-0526): a create that names one and a
// set-password are refused at deploy, as RuleUserPassword problems — and disabling an
// account still works.
func TestUserConnectorRefusesPasswords(t *testing.T) {
	srv, _ := newSystemServer(t, WithUserProvisioning())
	srv.systemPIDs = map[string]bool{"sysproc": true}
	h := srv.Handler()

	for name, inner := range map[string]string{
		"create with a password": `<atlas:userConnector operation="create" username="bob" email="bob@example.org" password="=pw"/>`,
		"set-password":           `<atlas:userConnector operation="set-password" username="bob" password="=newpw"/>`,
	} {
		// Under an id of its own: the rule holds for every model, and the id of a
		// system process would be refused before it, for being reserved.
		req := httptest.NewRequest(http.MethodPost, "/api/v1/deployments", strings.NewReader(userConnBPMN("pwproc", inner)))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code == http.StatusOK || !strings.Contains(rec.Body.String(), "user.password") {
			t.Errorf("%s: deploy = %d %s, want it refused naming user.password", name, rec.Code, rec.Body.String())
		}
	}

	createKey := deploySystemBPMN(t, srv, userConnBPMN("sysproc", createInner))
	startInstance(t, h, createKey, `{"username":"bob","email":"bob@example.org","displayName":"Bob"}`)
	disableKey := deploySystemBPMN(t, srv, userConnBPMN("sysproc", `<atlas:userConnector operation="disable" username="bob"/>`))
	startInstance(t, h, disableKey, `{}`)
	if u, ok, _ := srv.users.byUsername("bob"); !ok || !u.Disabled {
		t.Fatalf("bob not disabled: ok=%v", ok)
	}
}
