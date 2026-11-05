package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
)

// The rights of a system process belong to atlas's own deployment of it, in the
// system project, and not to whatever model takes its process id
// (systemprocessguard.go, ADR-0122).

// sessionReq serves one request through the whole handler as the signed-in session tok.
func sessionReq(t *testing.T, srv *Server, method, path, body, contentType, tok string) (int, string) {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	return rec.Code, rec.Body.String()
}

// hijackModel takes the id of the process atlas starts for a forgotten password, and
// would make an administrator whose address the author holds.
const hijackModel = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0" id="d" targetNamespace="x">
  <process id="proc_passwort_link" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="u"><extensionElements>
      <atlas:userConnector operation="create" username="=&quot;eviladmin&quot;" email="=&quot;attacker@evil.example&quot;" roles="=&quot;admin&quot;"/>
    </extensionElements></serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="u"/>
    <sequenceFlow id="f2" sourceRef="u" targetRef="end"/>
  </process>
</definitions>`

// guardServer is a server with auth, the system processes, user provisioning, a mail
// worker and three accounts: an administrator, a modeler and an ordinary local account
// somebody may ask a password link for. It returns the administrator's and the
// modeler's sessions.
func guardServer(t *testing.T) (srv *Server, atok, mtok string) {
	t.Helper()
	srv, _ = newSystemServer(t, WithAuth(), WithSystemProcesses(), WithUserProvisioning(), WithExternalURL(passwordTestOrigin))
	admin := User{ID: "usr_g00000000000000000000000", Username: "root", Email: "root@example.org", Source: SourceLocal, Roles: []string{RoleAdmin}, PasswordHash: mustHash(t, "rootPasswort123")}
	mod := User{ID: "usr_g00000000000000000000001", Username: "mo", Source: SourceLocal, Roles: []string{RoleModeler}, PasswordHash: mustHash(t, "moPasswort1234")}
	bait := User{ID: "usr_g00000000000000000000002", Username: "bait", Email: "bait@example.org", Source: SourceLocal, PasswordHash: mustHash(t, "baitPasswort12")}
	for _, u := range []User{admin, mod, bait} {
		if err := srv.users.Save(u); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	if atok, err = srv.sessions.create(admin, nil); err != nil {
		t.Fatal(err)
	}
	if mtok, err = srv.sessions.create(mod, nil); err != nil {
		t.Fatal(err)
	}
	if code, raw := sessionReq(t, srv, http.MethodPost, "/api/v1/configured-workers",
		`{"name":"mail","kind":"mail","provider":"preview","sender":"atlas@example.ch"}`, "application/json", atok); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create the mail worker: %d %s", code, raw)
	}
	return srv, atok, mtok
}

// noEvilAdmin fails the test when the hijack model ran with a system process's rights.
func noEvilAdmin(t *testing.T, srv *Server) {
	t.Helper()
	if u, ok, _ := srv.users.byUsername("eviladmin"); ok {
		t.Fatalf("a copy of a system process provisioned an account: %+v", u.Roles)
	}
}

// TestASystemProcessIDIsAtlassAlone: a modeler cannot deploy a model under the id of a
// system process — into their own project, ungrouped, or as an application import —
// so a forgotten password still starts atlas's own process, and nobody becomes an
// administrator by asking for a link.
func TestASystemProcessIDIsAtlassAlone(t *testing.T) {
	srv, _, mtok := guardServer(t)

	code, raw := sessionReq(t, srv, http.MethodPost, "/api/v1/projects", `{"name":"mine"}`, "application/json", mtok)
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create a project: %d %s", code, raw)
	}
	var proj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(raw), &proj); err != nil || proj.ID == "" {
		t.Fatalf("project id from %s: %v", raw, err)
	}
	for _, path := range []string{"/api/v1/deployments?projectId=" + proj.ID, "/api/v1/deployments?projectId="} {
		code, raw := sessionReq(t, srv, http.MethodPost, path, hijackModel, "application/xml", mtok)
		if code != http.StatusForbidden || !strings.Contains(raw, "belongs to an atlas system process") {
			t.Errorf("%s: %d %s, want 403 naming the system process", path, code, raw)
		}
	}

	// deployModel refuses it for every caller that reaches it, whatever they checked.
	var compErr error
	srv.do(func() { _, compErr, _ = srv.deployModel([]byte(hijackModel), nil, time.Now().Unix(), proj.ID, "") })
	if compErr == nil || !strings.Contains(compErr.Error(), "belongs to an atlas system process") {
		t.Errorf("deployModel into a tenant project: %v, want the refusal", compErr)
	}

	if code, raw := bearer(t, srv, http.MethodPost, passwordPagePath+"/forgot", "", `{"login":"bait"}`); code != http.StatusAccepted {
		t.Fatalf("forgot: %d %s", code, raw)
	}
	noEvilAdmin(t, srv)
	if got := len(mailsTo(srv, "bait@example.org")); got != 1 {
		t.Errorf("bait was mailed %d times, want the one link atlas's own process sends", got)
	}
}

// TestACopyOfASystemProcessIsNobodySpecial: a copy that reached the registry before ids
// were reserved — simulated by lifting the reservation while it deploys — is tenant
// content. "The" password process is still atlas's own, the copy provisions nobody
// when it runs, it emits no atlas.* event as atlas, and it may be deleted.
func TestACopyOfASystemProcessIsNobodySpecial(t *testing.T) {
	srv, atok, _ := guardServer(t)

	var (
		copyKey, sysKey uint64
		compErr, pErr   error
	)
	srv.do(func() {
		srv.systemPIDs[passwordLinkProcessID] = false
		var dps []deployedProcess
		dps, compErr, pErr = srv.deployModel([]byte(hijackModel), nil, time.Now().Unix(), "", "")
		srv.systemPIDs[passwordLinkProcessID] = true
		if len(dps) == 1 {
			copyKey = dps[0].Key
		}
		if d := srv.latestDeploymentOf(passwordLinkProcessID); d != nil {
			sysKey = d.Key
		}
	})
	if compErr != nil || pErr != nil || copyKey == 0 {
		t.Fatalf("deploy the copy: %v / %v (key %d)", compErr, pErr, copyKey)
	}

	var copyIsSystem, copySpeaksAsAtlas bool
	srv.do(func() {
		copyIsSystem = srv.isSystemDefKey(copyKey)
		copySpeaksAsAtlas = srv.isSystemDefinition(srv.deployments[copyKey])
	})
	if sysKey == 0 || sysKey == copyKey {
		t.Fatalf("the newest proc_passwort_link is %d, want atlas's own, not the copy %d", sysKey, copyKey)
	}
	if copyIsSystem || copySpeaksAsAtlas {
		t.Errorf("the copy counts as a system process (rights %v, atlas.* events %v)", copyIsSystem, copySpeaksAsAtlas)
	}

	// A forgotten password starts atlas's own process, not the newer copy.
	if code, raw := bearer(t, srv, http.MethodPost, passwordPagePath+"/forgot", "", `{"login":"bait"}`); code != http.StatusAccepted {
		t.Fatalf("forgot: %d %s", code, raw)
	}
	noEvilAdmin(t, srv)

	// Started by its own key, the copy runs as nobody special: the provisioning gate
	// refuses its user task, which runs as the instance starts.
	key := strconv.FormatUint(copyKey, 10)
	if code, raw := sessionReq(t, srv, http.MethodPost, "/api/v1/processes/"+key+"/instances", `{}`, "application/json", atok); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("start the copy: %d %s", code, raw)
	}
	noEvilAdmin(t, srv)

	// It is not protected from deletion the way atlas's own processes are.
	code, raw := sessionReq(t, srv, http.MethodDelete, "/api/v1/processes/"+key+"?force=true", "", "", atok)
	if strings.Contains(raw, "protected system process") {
		t.Errorf("the copy is protected like a system process: %d %s", code, raw)
	}
}

// TestARevokeKillsTheLinksBeforeIt: an activation link the owner already used opens
// nothing after an administrator revokes the password, although the revoke returns the
// account to the empty hash that link was signed over (ADR-0526, amendment of
// 2026-11-04). A link minted after the revoke works.
func TestARevokeKillsTheLinksBeforeIt(t *testing.T) {
	srv := passwordServer(t)
	admin := &httpapi.Principal{UserID: "usr_k00000000000000000000000", Username: "root", Roles: []string{RoleAdmin}, Session: true}
	root := User{ID: admin.UserID, Username: "root", Source: SourceLocal, Roles: []string{RoleAdmin}, PasswordHash: mustHash(t, "rootPasswort123")}
	owner := User{ID: "usr_k00000000000000000000001", Username: "sven", Email: "sven@example.org", Source: SourceLocal, Roles: []string{RoleUser}}
	for _, u := range []User{root, owner} {
		if err := srv.users.Save(u); err != nil {
			t.Fatal(err)
		}
	}
	old, _, err := srv.passwordToken(owner, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if code, raw := bearer(t, srv, http.MethodPost, passwordPagePath+"/link", old, `{"password":"ersterPasswort1"}`); code != http.StatusOK {
		t.Fatalf("activate: %d %s", code, raw)
	}
	if code, raw := revokeAs(t, srv, admin, owner.ID, `{"revoke":true}`); code != http.StatusOK {
		t.Fatalf("revoke: %d %s", code, raw)
	}
	if code, _ := bearer(t, srv, http.MethodPost, passwordPagePath+"/link", old, `{"password":"angreiferPasswort1"}`); code != http.StatusNotFound {
		t.Errorf("the activation link from before the revoke answered %d, want 404", code)
	}
	cur, _, _ := srv.users.Get(owner.ID)
	if checkPassword(cur.PasswordHash, "angreiferPasswort1") {
		t.Fatal("a link from before the revoke set the password")
	}
	if cur.CredentialEpoch != 1 {
		t.Errorf("credential epoch = %d after one revoke, want 1", cur.CredentialEpoch)
	}
	fresh, _, err := srv.passwordToken(cur, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if code, raw := bearer(t, srv, http.MethodPost, passwordPagePath+"/link", fresh, `{"password":"neuesPasswort12"}`); code != http.StatusOK {
		t.Errorf("a link minted after the revoke: %d %s", code, raw)
	}
}

// TestAnAccountNeverRevokedKeepsItsLinks: an account whose epoch is zero is signed as
// before the epoch existed, so the links of every account that was never revoked —
// those mailed before the upgrade among them — keep holding.
func TestAnAccountNeverRevokedKeepsItsLinks(t *testing.T) {
	srv := passwordServer(t)
	u := User{ID: "usr_k00000000000000000000002", Username: "nina", Email: "nina@example.org", Source: SourceLocal}
	expiry := time.Now().Add(time.Hour).Unix()
	withZero, err := srv.passwordMAC(u, expiry)
	if err != nil {
		t.Fatal(err)
	}
	u.CredentialEpoch = 1
	withOne, err := srv.passwordMAC(u, expiry)
	if err != nil {
		t.Fatal(err)
	}
	if string(withZero) == string(withOne) {
		t.Fatal("the epoch does not change the signature")
	}
}

// TestTheLinkPagesCannotBeFramed: the pages a link opens refuse every frame.
func TestTheLinkPagesCannotBeFramed(t *testing.T) {
	srv := passwordServer(t)
	for _, path := range []string{passwordPagePath, answerPagePath} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: %d", path, rec.Code)
			continue
		}
		if got := rec.Header().Get("Content-Security-Policy"); got != "frame-ancestors 'none'" {
			t.Errorf("%s: Content-Security-Policy %q", path, got)
		}
		if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
			t.Errorf("%s: X-Frame-Options %q", path, got)
		}
	}
}
