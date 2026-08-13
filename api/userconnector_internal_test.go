package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// userConnBPMN wraps a <atlas:userConnector> extension in a minimal Start → task →
// End process with the given process id.
func userConnBPMN(procID, inner string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
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

const createInner = `<atlas:userConnector operation="create" username="=username" email="=email" displayName="=displayName" roles="user" password="=password"/>`

// TestUserConnectorCreatesUser is the ADR-0123 happy path: a system process with a
// create userConnector task, run with start variables, provisions exactly the
// account an admin would — right username, roles, and a verifiable password.
func TestUserConnectorCreatesUser(t *testing.T) {
	srv, _ := newSystemServer(t, WithUserProvisioning())
	srv.systemPIDs = map[string]bool{"sysproc": true} // gate: treat this as a system process
	h := srv.Handler()

	key := deployBPMN(t, h, userConnBPMN("sysproc", createInner))
	startInstance(t, h, key, `{"username":"erika.muster","email":"erika@example.org","displayName":"Erika Muster","password":"initialpass1"}`)

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
	if !checkPassword(u.PasswordHash, "initialpass1") {
		t.Fatal("provisioned password does not verify")
	}
}

// TestUserConnectorIdempotent: running create twice for the same username yields one
// account (an at-least-once retry must not double-create).
func TestUserConnectorIdempotent(t *testing.T) {
	srv, _ := newSystemServer(t, WithUserProvisioning())
	srv.systemPIDs = map[string]bool{"sysproc": true}
	h := srv.Handler()
	key := deployBPMN(t, h, userConnBPMN("sysproc", createInner))

	vars := `{"username":"dupe","email":"dupe@example.org","displayName":"Dupe","password":"initialpass1"}`
	startInstance(t, h, key, vars)
	startInstance(t, h, key, vars)

	all, err := srv.users.loadAll()
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
	startInstance(t, h, key, `{"username":"sneaky","email":"x@example.org","displayName":"X","password":"initialpass1"}`)

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

	key := deployBPMN(t, h, userConnBPMN("sysproc", createInner))
	startInstance(t, h, key, `{"username":"noworker","email":"x@example.org","displayName":"X","password":"initialpass1"}`)

	if _, ok, _ := srv.users.byUsername("noworker"); ok {
		t.Fatal("provisioning happened without the opt-in")
	}
}

// TestProvisionOpsRails drives the provisioning operations' guards directly: the
// reused admin-API rails (password length, email uniqueness, last-admin lockout)
// and the not-found / idempotent-disable branches.
func TestProvisionOpsRails(t *testing.T) {
	srv := newServerForErrors(t)
	const now = 1000

	// create: password too short is refused.
	if _, _, err := srv.provisionCreateUser("a", "", "", nil, "short", now); err == nil {
		t.Fatal("short password: want error")
	}
	// create u1 (as an admin, for the lockout test), then a second create is idempotent.
	if _, created, err := srv.provisionCreateUser("u1", "u1@example.org", "U1", []string{RoleAdmin}, "longenough1", now); err != nil || !created {
		t.Fatalf("create u1: created=%v err=%v", created, err)
	}
	if _, created, err := srv.provisionCreateUser("u1", "", "", nil, "longenough1", now); err != nil || created {
		t.Fatalf("re-create u1: want created=false nil err, got created=%v err=%v", created, err)
	}
	// create with an already-taken email is refused.
	if _, _, err := srv.provisionCreateUser("u2", "u1@example.org", "", nil, "longenough1", now); err == nil {
		t.Fatal("duplicate email: want error")
	}
	// set-password: no such user, and too-short.
	if err := srv.provisionSetPassword("ghost", "longenough1", now); err == nil {
		t.Fatal("set-password unknown user: want error")
	}
	if err := srv.provisionSetPassword("u1", "short", now); err == nil {
		t.Fatal("set-password too short: want error")
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
	if _, _, err := srv.provisionCreateUser("plain", "", "", nil, "longenough1", now); err != nil {
		t.Fatalf("create plain: %v", err)
	}
	if err := srv.provisionDisableUser("plain", now); err != nil {
		t.Fatalf("disable plain: %v", err)
	}
	if err := srv.provisionDisableUser("plain", now); err != nil {
		t.Fatalf("re-disable plain (idempotent): %v", err)
	}
}

// TestUserConnectorSetPasswordAndDisable exercises the other two operations against
// an already-provisioned account.
func TestUserConnectorSetPasswordAndDisable(t *testing.T) {
	srv, _ := newSystemServer(t, WithUserProvisioning())
	srv.systemPIDs = map[string]bool{"sysproc": true}
	h := srv.Handler()

	// Provision, then change the password.
	createKey := deployBPMN(t, h, userConnBPMN("sysproc", createInner))
	startInstance(t, h, createKey, `{"username":"bob","email":"bob@example.org","displayName":"Bob","password":"initialpass1"}`)

	setPwKey := deployBPMN(t, h, userConnBPMN("sysproc", `<atlas:userConnector operation="set-password" username="bob" password="=newpw"/>`))
	startInstance(t, h, setPwKey, `{"newpw":"changedpass2"}`)

	u, ok, _ := srv.users.byUsername("bob")
	if !ok {
		t.Fatal("bob missing")
	}
	if checkPassword(u.PasswordHash, "initialpass1") || !checkPassword(u.PasswordHash, "changedpass2") {
		t.Fatal("set-password did not replace the password")
	}

	// Disable.
	disableKey := deployBPMN(t, h, userConnBPMN("sysproc", `<atlas:userConnector operation="disable" username="bob"/>`))
	startInstance(t, h, disableKey, `{}`)
	if u, ok, _ := srv.users.byUsername("bob"); !ok || !u.Disabled {
		t.Fatalf("bob not disabled: ok=%v", ok)
	}
}
