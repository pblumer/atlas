package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/workersdk"
	"github.com/pblumer/atlas/workertype/install"
)

// An installed Worker Type's instance holds a credential of its own, confined to the
// job types its manifest declares (ADR-0545).
// Before it, every supervised child held this server's internal token, whose worker
// scope reaches every job type: an artifact atlas did not build could lease and settle
// the jobs of every other Worker Type.

const (
	echoInstance   = "workertype:com.example.echo@1.0.0"
	echoJobType    = "com.example.echo"
	foreignJobType = "com.example.foreign"
)

// foreignBPMN is a process whose one task is of a job type the echo manifest does not
// declare: the work an installed echo instance must not reach.
const foreignBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <bpmn:process id="foreign" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:serviceTask id="call">
      <bpmn:extensionElements><zeebe:taskDefinition type="com.example.foreign"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="done"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="call"/>
    <bpmn:sequenceFlow id="f2" sourceRef="call" targetRef="done"/>
  </bpmn:process>
</bpmn:definitions>`

// adminRequest runs one request as a signed-in administrator.
func adminRequest(t *testing.T, srv *Server, method, path, body, contentType string) (int, []byte) {
	t.Helper()
	tok, err := srv.sessions.create(User{ID: "usr_admin", Username: "admin", Roles: []string{RoleAdmin}}, nil)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: tok})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	return rec.Code, rec.Body.Bytes()
}

// bearerRequest runs one request carrying token as its bearer.
func bearerRequest(t *testing.T, srv *Server, token, method, path, body string) (int, []byte) {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, r)
	return rec.Code, rec.Body.Bytes()
}

// deployAndStart deploys model and starts one instance of processID, as an
// administrator, and returns the instance's key.
func deployAndStart(t *testing.T, srv *Server, model, processID string) uint64 {
	t.Helper()
	if code, raw := adminRequest(t, srv, http.MethodPost, "/api/v1/deployments", model, "application/xml"); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy %s: status=%d body=%s", processID, code, raw)
	}
	code, raw := adminRequest(t, srv, http.MethodPost, "/api/v1/instances", `{"processId":"`+processID+`","variables":{"greeting":"hello"}}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("start %s: status=%d body=%s", processID, code, raw)
	}
	var started struct {
		Key uint64 `json:"instanceKey"`
	}
	if err := json.Unmarshal(raw, &started); err != nil || started.Key == 0 {
		t.Fatalf("start %s answered %s", processID, raw)
	}
	return started.Key
}

type instanceLease struct {
	JobKey     uint64 `json:"jobKey"`
	LeaseToken uint64 `json:"leaseToken"`
}

// leaseAs pulls one job of jobType as worker with token, and wants exactly one.
func leaseAs(t *testing.T, srv *Server, token, worker, jobType string) instanceLease {
	t.Helper()
	code, raw := bearerRequest(t, srv, token, http.MethodPost, "/api/v1/jobs/activate",
		`{"type":"`+jobType+`","worker":"`+worker+`","maxJobs":1}`)
	if code != http.StatusOK {
		t.Fatalf("lease %s as %s: status=%d body=%s", jobType, worker, code, raw)
	}
	var out struct {
		Jobs []instanceLease `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || len(out.Jobs) != 1 {
		t.Fatalf("lease %s as %s answered %s", jobType, worker, raw)
	}
	return out.Jobs[0]
}

// confinedServer is an authenticated server with one job of the echo type and one of
// a foreign type waiting, and a credential minted for the echo release's instance.
func confinedServer(t *testing.T) (*Server, string) {
	t.Helper()
	srv, _ := newValidateServer(t, WithAuth())
	deployAndStart(t, srv, echoBPMN, "echo")
	deployAndStart(t, srv, foreignBPMN, "foreign")
	tok := srv.instanceCreds.mint(echoInstance, []string{echoJobType})
	if tok == "" {
		t.Fatal("no credential was minted for the installed instance")
	}
	return srv, tok
}

func TestAnInstalledInstanceLeasesOnlyItsOwnJobTypes(t *testing.T) {
	srv, tok := confinedServer(t)

	code, raw := bearerRequest(t, srv, tok, http.MethodPost, "/api/v1/jobs/activate",
		`{"type":"`+foreignJobType+`","worker":"`+echoInstance+`"}`)
	if code != http.StatusForbidden || !strings.Contains(string(raw), echoJobType) || !strings.Contains(string(raw), foreignJobType) {
		t.Errorf("lease of a foreign type = %d %s, want 403 naming what it serves and what it asked for", code, raw)
	}
	code, raw = bearerRequest(t, srv, tok, http.MethodPost, "/api/v1/jobs/activate",
		`{"type":"`+echoJobType+`","worker":"somebody-else"}`)
	if code != http.StatusForbidden || !strings.Contains(string(raw), echoInstance) {
		t.Errorf("lease under another worker id = %d %s, want 403 naming its own", code, raw)
	}

	// Its own type, as itself, works.
	own := leaseAs(t, srv, tok, echoInstance, echoJobType)
	if code, raw := bearerRequest(t, srv, tok, http.MethodPost, "/api/v1/jobs/"+strconv.FormatUint(own.JobKey, 10)+"/complete",
		`{"worker":"`+echoInstance+`","leaseToken":`+strconv.FormatUint(own.LeaseToken, 10)+`}`); code != http.StatusOK {
		t.Errorf("complete its own job = %d %s, want 200", code, raw)
	}
}

func TestAnInstalledInstanceSettlesNoJobOfAnotherType(t *testing.T) {
	srv, tok := confinedServer(t)
	// A built-in supervised worker, holding the internal token, leases the foreign job.
	foreign := leaseAs(t, srv, srv.internalToken, "builtin", foreignJobType)
	key := strconv.FormatUint(foreign.JobKey, 10)
	fence := strconv.FormatUint(foreign.LeaseToken, 10)

	for _, tc := range []struct{ name, path, body string }{
		{"complete as itself", "/complete", `{"worker":"` + echoInstance + `","leaseToken":` + fence + `}`},
		{"complete as the holder", "/complete", `{"worker":"builtin","leaseToken":` + fence + `}`},
		{"complete through the operator door", "/complete", `{"reason":"looks done"}`},
		{"fail as itself", "/fail", `{"worker":"` + echoInstance + `","leaseToken":` + fence + `,"retries":0,"message":"no"}`},
		{"fail through the operator door", "/fail", `{"retries":0,"message":"no"}`},
		{"throw as itself", "/error", `{"errorCode":"E","worker":"` + echoInstance + `","leaseToken":` + fence + `}`},
		{"throw through the operator door", "/error", `{"errorCode":"E","reason":"forced"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, raw := bearerRequest(t, srv, tok, http.MethodPost, "/api/v1/jobs/"+key+tc.path, tc.body)
			if code != http.StatusForbidden {
				t.Errorf("= %d %s, want 403", code, raw)
			}
		})
	}
	// The job is untouched: its holder still settles it.
	if code, raw := bearerRequest(t, srv, srv.internalToken, http.MethodPost, "/api/v1/jobs/"+key+"/complete",
		`{"worker":"builtin","leaseToken":`+fence+`}`); code != http.StatusOK {
		t.Errorf("the holder's completion = %d %s, want 200", code, raw)
	}
	// An unknown key is still not found, before any binding is consulted.
	if code, _ := bearerRequest(t, srv, tok, http.MethodPost, "/api/v1/jobs/999999/complete",
		`{"worker":"`+echoInstance+`","leaseToken":1}`); code != http.StatusNotFound {
		t.Errorf("unknown key = %d, want 404", code)
	}
}

func TestAnInstalledInstanceSettlesItsOwnJobOnlyAsItself(t *testing.T) {
	srv, tok := confinedServer(t)
	own := leaseAs(t, srv, tok, echoInstance, echoJobType)
	key := strconv.FormatUint(own.JobKey, 10)
	for _, body := range []string{
		`{"reason":"by hand"}`,
		`{"worker":"somebody-else","leaseToken":` + strconv.FormatUint(own.LeaseToken, 10) + `}`,
	} {
		if code, raw := bearerRequest(t, srv, tok, http.MethodPost, "/api/v1/jobs/"+key+"/complete", body); code != http.StatusForbidden {
			t.Errorf("complete %s = %d %s, want 403", body, code, raw)
		}
	}
	if code, raw := bearerRequest(t, srv, tok, http.MethodPost, "/api/v1/jobs/"+key+"/fail",
		`{"worker":"`+echoInstance+`","leaseToken":`+strconv.FormatUint(own.LeaseToken, 10)+`,"retries":2,"message":"try again"}`); code != http.StatusOK {
		t.Errorf("fail its own job = %d %s, want 200", code, raw)
	}
}

func TestAnInstalledInstanceReadsNoQueryOfAnotherType(t *testing.T) {
	srv, tok := confinedServer(t)
	code, raw := bearerRequest(t, srv, tok, http.MethodPost, "/api/v1/worker-queries/activate",
		`{"type":"`+foreignJobType+`","worker":"`+echoInstance+`","connectors":["x"]}`)
	if code != http.StatusForbidden || !strings.Contains(string(raw), foreignJobType) {
		t.Errorf("query poll of a foreign type = %d %s, want 403", code, raw)
	}
	if code, raw := bearerRequest(t, srv, tok, http.MethodPost, "/api/v1/worker-queries/activate",
		`{"type":"`+echoJobType+`","worker":"somebody-else","connectors":["x"]}`); code != http.StatusForbidden {
		t.Errorf("query poll as another worker = %d %s, want 403", code, raw)
	}
	if code, raw := bearerRequest(t, srv, tok, http.MethodPost, "/api/v1/worker-queries/activate",
		`{"type":"`+echoJobType+`","worker":"`+echoInstance+`","connectors":["x"]}`); code != http.StatusOK {
		t.Errorf("query poll of its own type = %d %s, want 200", code, raw)
	}
	if code, raw := bearerRequest(t, srv, tok, http.MethodPost, "/api/v1/worker-queries/q1/answer",
		`{"worker":"somebody-else","leaseToken":"t","error":{"code":"provider_unavailable"}}`); code != http.StatusForbidden {
		t.Errorf("answer as another worker = %d %s, want 403", code, raw)
	}
	if code, raw := bearerRequest(t, srv, tok, http.MethodPost, "/api/v1/worker-queries/q1/answer",
		`{"worker":"`+echoInstance+`","leaseToken":"t","error":{"code":"provider_unavailable"}}`); code != http.StatusNotFound {
		t.Errorf("answer to a query nobody asked = %d %s, want 404", code, raw)
	}
}

// The scope reaches the job and query protocol and nothing else: not the routes the
// built-in Worker Types report to, not the authorization a brokered credential comes
// from, not the product API.
func TestAnInstalledInstanceReachesOnlyTheProtocol(t *testing.T) {
	srv, tok := confinedServer(t)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/jobs/1/authorize", `{}`},
		{http.MethodPost, "/api/v1/mail/outbox", `{}`},
		{http.MethodPost, "/api/v1/ad/mock-directory", `{}`},
		{http.MethodPost, "/api/v1/sql/mock-journal", `{}`},
		{http.MethodPost, "/api/v1/sharepoint/mock-sites/w", `{}`},
		{http.MethodGet, "/api/v1/processes", ""},
		{http.MethodGet, "/api/v1/workers", ""},
	} {
		code, raw := bearerRequest(t, srv, tok, tc.method, tc.path, tc.body)
		if code != http.StatusForbidden || !strings.Contains(string(raw), apiScopeInstalledWorker) {
			t.Errorf("%s %s = %d %s, want 403 naming the scope", tc.method, tc.path, code, raw)
		}
	}
	if validAPIScope(apiScopeInstalledWorker) {
		t.Error("the installed-instance scope must not be mintable as an API token")
	}
}

func TestAnInstancesCredentialEndsWithItsReleaseAndItsServer(t *testing.T) {
	srv, tok := confinedServer(t)
	// Stable while the release stays registered, so a restart of the process does not
	// read as a changed environment.
	if again := srv.instanceCreds.mint(echoInstance, []string{echoJobType}); again != tok {
		t.Error("minting again for the same instance gave a different secret")
	}
	srv.instanceCreds.revoke(echoInstance)
	if code, _ := bearerRequest(t, srv, tok, http.MethodPost, "/api/v1/jobs/activate",
		`{"type":"`+echoJobType+`","worker":"`+echoInstance+`"}`); code != http.StatusUnauthorized {
		t.Errorf("after revocation = %d, want 401", code)
	}
	if renewed := srv.instanceCreds.mint(echoInstance, []string{echoJobType}); renewed == tok || renewed == "" {
		t.Error("a release registered again must get a new secret")
	}
	// A new server knows none of the old one's secrets.
	other, _ := newValidateServer(t, WithAuth())
	if code, _ := bearerRequest(t, other, tok, http.MethodPost, "/api/v1/jobs/activate",
		`{"type":"`+echoJobType+`","worker":"`+echoInstance+`"}`); code != http.StatusUnauthorized {
		t.Errorf("on another server = %d, want 401", code)
	}
}

// Built-in supervised workers and API tokens are not confined by any of this.
func TestBuiltInAndTokenWorkersStillServeAnyJobType(t *testing.T) {
	srv, _ := confinedServer(t)
	leaseAs(t, srv, srv.internalToken, "builtin", foreignJobType)
	apiTok := apiTokenPrefix + "test-worker-token"
	srv.apiTokens.add(apiToken{ID: "w1", Name: "worker", Hash: hashAPIToken(apiTok), Scope: apiScopeWorker, Roles: legacyRoles()})
	leaseAs(t, srv, apiTok, "external", echoJobType)
}

// The launch environment carries the instance's own secret, never this server's
// internal token nor an operator's ATLAS_TOKEN; with authentication off it carries
// none.
func TestAnInstalledInstanceIsLaunchedWithItsOwnCredential(t *testing.T) {
	t.Setenv("ATLAS_TOKEN", "operator-chosen")
	inst := install.Installed{ID: "com.example.echo", Version: "1.0.0", Digest: "sha256:00", JobTypes: []string{echoJobType}}

	srv, _ := newValidateServer(t, WithAuth(), WithInstalledWorkerTypes(t.TempDir(), "http://127.0.0.1:1", false))
	env := srv.installedLaunchEnv(inst)
	got := envValue(env, workersdk.EnvToken)
	if got == "" || got == srv.internalToken || got == "operator-chosen" {
		t.Fatalf("ATLAS_TOKEN = %q, want the instance's own secret", got)
	}
	if again := envValue(srv.installedLaunchEnv(inst), workersdk.EnvToken); again != got {
		t.Error("the environment changes between two spawns, so refresh would restart it forever")
	}
	if strings.Contains(strings.Join(env, "\n"), brokerKeyEnv) {
		t.Error("an installed instance was handed the broker key")
	}

	open, _ := newValidateServer(t, WithInstalledWorkerTypes(t.TempDir(), "http://127.0.0.1:1", false))
	if v := envValue(open.installedLaunchEnv(inst), workersdk.EnvToken); v != "" {
		t.Errorf("without authentication ATLAS_TOKEN = %q, want none", v)
	}
}

// adminInstallUpload installs a release as an administrator.
func adminInstallUpload(t *testing.T, srv *Server, manifest, artifact []byte) (int, []byte) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, p := range []struct {
		name string
		data []byte
	}{{"manifest", manifest}, {"artifact", artifact}} {
		w, err := mw.CreateFormFile(p.name, p.name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(p.data)
	}
	_ = mw.Close()
	return adminRequest(t, srv, http.MethodPost, "/api/v1/worker-types/installed", body.String(), mw.FormDataContentType())
}

func envValue(env []string, name string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, name+"="); ok {
			return v
		}
	}
	return ""
}

// End to end with authentication on: the echo artifact is installed, launched with its
// own credential, completes its job, and loses the credential when it is removed.
func TestAnInstalledWorkerTypeWorksItsJobWithItsOwnCredential(t *testing.T) {
	_, bin := buildEcho(t)
	srv, _ := installServer(t, true, WithAuth())

	code, raw := adminInstallUpload(t, srv, echoManifest("1.0.0", bin), bin)
	if code != http.StatusCreated {
		t.Fatalf("install: status=%d body=%s", code, raw)
	}
	key := deployAndStart(t, srv, echoBPMN, "echo")

	deadline := time.Now().Add(60 * time.Second)
	for {
		_, raw := adminRequest(t, srv, http.MethodGet, "/api/v1/instances/"+strconv.FormatUint(key, 10)+"/variables", "", "")
		var vars map[string]any
		_ = json.Unmarshal(raw, &vars)
		if vars["echoedBy"] == echoInstance {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the installed Worker Type never completed its job under authentication; variables=%s", raw)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, ok := srv.instanceCreds.tokenOf(echoInstance); !ok {
		t.Fatal("the running instance holds no credential of its own")
	}
	if code, raw := adminRequest(t, srv, http.MethodDelete, "/api/v1/worker-types/installed/com.example.echo/1.0.0", "", ""); code != http.StatusOK {
		t.Fatalf("remove: status=%d body=%s", code, raw)
	}
	if _, ok := srv.instanceCreds.tokenOf(echoInstance); ok {
		t.Error("the removed release's credential is still valid")
	}
}

// A secret that could not be drawn is no secret: the instance is launched without one
// and refused at every poll, never let in with something guessable.
func TestAnInstanceWhoseSecretCannotBeDrawnGetsNone(t *testing.T) {
	c := newInstanceCredentials()
	c.random = func(int) (string, error) { return "", errors.New("no entropy") }
	if tok := c.mint(echoInstance, []string{echoJobType}); tok != "" {
		t.Errorf("mint without randomness = %q, want none", tok)
	}
	if _, ok := c.tokenOf(echoInstance); ok {
		t.Error("a failed mint left a credential behind")
	}
}

// A job whose type has no name cannot be one an instance serves.
func TestAJobOfAnUnnamedTypeIsNoInstancesWork(t *testing.T) {
	srv, _ := newValidateServer(t)
	p := installedPrincipal()
	if why := srv.instanceRefusesJob(p, echoInstance, &model.JobValue{JobType: 9999}); !strings.Contains(why, "#9999") {
		t.Errorf("refusal = %q, want one naming the unnamed type", why)
	}
	if why := srv.instanceRefusesJob(nil, "", &model.JobValue{JobType: 9999}); why != "" {
		t.Errorf("an unbound request is refused: %q", why)
	}
}

func installedPrincipal() *httpapi.Principal {
	return instanceCredential{worker: echoInstance, jobTypes: []string{echoJobType}}.principal()
}

// A Server built without New has no index: it resolves no instance and mints nothing.
func TestANilIndexHoldsNoCredential(t *testing.T) {
	var c *instanceCredentials
	if tok := c.mint(echoInstance, nil); tok != "" {
		t.Errorf("mint = %q, want none", tok)
	}
	c.revoke(echoInstance)
	if _, ok := c.match("x"); ok {
		t.Error("a nil index matched a bearer")
	}
	if _, ok := c.tokenOf(echoInstance); ok {
		t.Error("a nil index holds a token")
	}
}
