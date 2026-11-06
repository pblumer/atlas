package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/workertype"
	"github.com/pblumer/atlas/workertype/install"
)

// An installed Worker Type artifact, end to end (ADR-0535, migration step 3): the
// example Worker Type is built as its own executable against workersdk alone,
// installed with its manifest, verified, launched by the supervisor, and completes a
// job of a deployed process — and it shows in the Workers view under the identity
// the supervisor verified.

const echoBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <bpmn:process id="echo" isExecutable="true">
    <bpmn:startEvent id="start"/>
    <bpmn:serviceTask id="call">
      <bpmn:extensionElements><zeebe:taskDefinition type="com.example.echo"/></bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="done"/>
    <bpmn:sequenceFlow id="f1" sourceRef="start" targetRef="call"/>
    <bpmn:sequenceFlow id="f2" sourceRef="call" targetRef="done"/>
  </bpmn:process>
</bpmn:definitions>`

// buildEcho builds examples/workertypes/echo and returns its path and bytes.
func buildEcho(t *testing.T) (string, []byte) {
	t.Helper()
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain to build the example Worker Type")
	}
	out := filepath.Join(t.TempDir(), "echo")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command(gobin, "build", "-o", out, "../examples/workertypes/echo")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build the example Worker Type: %v\n%s", err, msg)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return out, raw
}

func echoManifest(version string, bin []byte) []byte {
	sum := sha256.Sum256(bin)
	return []byte(fmt.Sprintf(`{"apiVersion":"atlas.io/v1alpha1","kind":"WorkerType",
"metadata":{"id":"com.example.echo","version":%q,"name":"Echo"},
"spec":{"atlasCompatibility":">=0.1.0","workerProtocol":"v1","runtime":{"mode":"external"},
"jobTypes":["com.example.echo"],"operations":[{"id":"echo","jobType":"com.example.echo"}],
"configurationSchema":"config.schema.json","modelerTemplate":"template.json",
"configuration":{"type":"object","required":["greeting"],"additionalProperties":false,
 "properties":{"greeting":{"type":"string","minLength":1},"token":{"type":"string","writeOnly":true}}},
"artifacts":[{"platform":%q,"mediaType":%q,"digest":"sha256:%s"}]}}`,
		version, install.Platform(), workertype.MediaTypeExecutable, hex.EncodeToString(sum[:])))
}

func installUpload(t *testing.T, url string, manifest, artifact []byte) (int, []byte) {
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
	resp, err := http.Post(url+"/api/v1/worker-types/installed", mw.FormDataContentType(), &body)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out bytes.Buffer
	_, _ = out.ReadFrom(resp.Body)
	return resp.StatusCode, out.Bytes()
}

// installServer serves a server keeping installed Worker Types, on a listener whose
// address is known before the server is built, so a launched instance is told it.
func installServer(t *testing.T, allowUnsigned bool) (*Server, *httptest.Server) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	url := "http://" + ln.Addr().String()
	srv, _ := newValidateServer(t, WithInstalledWorkerTypes(t.TempDir(), url, allowUnsigned))
	ts := httptest.NewUnstartedServer(srv.Handler())
	_ = ts.Listener.Close()
	ts.Listener = ln
	ts.Start()
	t.Cleanup(ts.Close)
	return srv, ts
}

func TestAnInstalledWorkerTypeIsLaunchedAndWorksAJob(t *testing.T) {
	_, bin := buildEcho(t)
	srv, ts := installServer(t, true)

	code, raw := installUpload(t, ts.URL, echoManifest("1.0.0", bin), bin)
	if code != http.StatusCreated {
		t.Fatalf("install: status=%d body=%s", code, raw)
	}
	var inst installedView
	if err := json.Unmarshal(raw, &inst); err != nil {
		t.Fatal(err)
	}
	if inst.ID != "com.example.echo" || inst.Signed || inst.Origin != install.OriginFile {
		t.Errorf("installed = %+v", inst)
	}

	if code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", echoBPMN, "application/xml"); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy: status=%d body=%s", code, raw)
	}
	code, raw = serveInternal(t, srv, http.MethodPost, "/api/v1/processes/1/instances", `{"variables":{"greeting":"hello"}}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("start: status=%d body=%s", code, raw)
	}
	var started struct {
		Key uint64 `json:"instanceKey"`
	}
	if err := json.Unmarshal(raw, &started); err != nil || started.Key == 0 {
		t.Fatalf("start answered %s", raw)
	}

	// The launched executable leases the job and completes it with what it echoed.
	var vars map[string]any
	deadline := time.Now().Add(60 * time.Second)
	for {
		_, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/instances/"+strconv.FormatUint(started.Key, 10)+"/variables", "", "")
		_ = json.Unmarshal(raw, &vars)
		if vars["echoedBy"] != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the installed Worker Type never completed the job; started=%+v variables=%s", started, raw)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if vars["echoedBy"] != installedSupervisorID("com.example.echo", "1.0.0") || vars["greeting"] != "hello" {
		t.Errorf("variables = %v, want the greeting echoed by the installed instance", vars)
	}

	// The Workers view shows the instance under the identity the supervisor verified.
	view := workersView(t, srv)
	found := false
	for _, w := range view.Workers {
		if w.WorkerType != nil && w.WorkerType.ID == "com.example.echo" && w.WorkerType.Digest == inst.Digest {
			found = true
		}
	}
	if !found {
		t.Errorf("workers = %+v, want the echo instance with its verified digest", view.Workers)
	}

	// Removing it stops the instance and deletes the release.
	if code, raw := serveInternal(t, srv, http.MethodDelete, "/api/v1/worker-types/installed/com.example.echo/1.0.0", "", ""); code != http.StatusOK {
		t.Fatalf("remove: status=%d body=%s", code, raw)
	}
	if srv.supervisor.has(installedSupervisorID("com.example.echo", "1.0.0")) {
		t.Error("the removed release is still supervised")
	}
	_, raw = serveInternal(t, srv, http.MethodGet, "/api/v1/worker-types/installed", "", "")
	if !strings.Contains(string(raw), `"installed":[]`) {
		t.Errorf("installed after removal = %s, want none", raw)
	}
}

// Without the development switch an unsigned artifact is refused before anything is
// read, and a malformed or mismatched upload is refused with the reason.
func TestInstallingRefusesWhatItCannotTrust(t *testing.T) {
	bin := []byte("not really an executable")
	_, strict := installServer(t, false)
	if code, raw := installUpload(t, strict.URL, echoManifest("1.0.0", bin), bin); code != http.StatusForbidden || !strings.Contains(string(raw), "allow-unsigned-worker-types") {
		t.Errorf("unsigned on a strict server: status=%d body=%s, want 403 naming the switch", code, raw)
	}

	srv, ts := installServer(t, true)
	if code, raw := installUpload(t, ts.URL, echoManifest("1.0.0", []byte("other bytes")), bin); code != http.StatusBadRequest || !strings.Contains(string(raw), "digest") {
		t.Errorf("digest mismatch: status=%d body=%s, want 400 naming the digest", code, raw)
	}
	resp, err := http.Post(ts.URL+"/api/v1/worker-types/installed", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("not multipart: status=%d, want 400", resp.StatusCode)
	}
	if code, _ := serveInternal(t, srv, http.MethodDelete, "/api/v1/worker-types/installed/com.example.echo/9.9.9", "", ""); code != http.StatusNotFound {
		t.Errorf("remove of nothing: status=%d, want 404", code)
	}
}

// The supervisor refuses to start an installed executable that fails verification,
// and says so in the worker's state instead of running it.
func TestTheSupervisorRefusesAnArtifactThatFailsVerification(t *testing.T) {
	quit := make(chan struct{})
	sup := newSupervisor(quit)
	sup.backoff = time.Hour
	refused := errors.New("installed artifact no longer matches its digest")
	sup.addLive(SuperviseSpec{ID: "workertype:x@1", Executable: "/nonexistent", Verify: func() error { return refused }}, nil, nil)
	sup.start()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st := sup.list()
		if len(st) == 1 && st[0].State == "refused" && strings.Contains(st[0].LastExit, "no longer matches") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("supervisor = %+v, want the worker refused", st)
		}
		time.Sleep(5 * time.Millisecond)
	}
	sup.remove("workertype:x@1")
	if sup.has("workertype:x@1") {
		t.Error("a removed worker is still supervised")
	}
	close(quit)
	sup.wait()
}

// A server keeping no installed Worker Types answers for none.
func TestAServerWithoutAStoreInstallsNothing(t *testing.T) {
	srv, _ := newValidateServer(t)
	if code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/worker-types/installed", "", ""); code != http.StatusOK || !strings.Contains(string(raw), `"installed":[]`) {
		t.Errorf("list: status=%d body=%s", code, raw)
	}
	if code, _ := serveInternal(t, srv, http.MethodPost, "/api/v1/worker-types/installed", "", "multipart/form-data"); code != http.StatusConflict {
		t.Errorf("install: status=%d, want 409", code)
	}
	if code, _ := serveInternal(t, srv, http.MethodDelete, "/api/v1/worker-types/installed/a.b/1.0.0", "", ""); code != http.StatusNotFound {
		t.Errorf("remove: status=%d, want 404", code)
	}
}

// The upload needs both parts, the manifest first; a second artifact under an
// installed version is a conflict.
func TestTheUploadIsHeldToItsShape(t *testing.T) {
	_, ts := installServer(t, true)
	bin := []byte("#!/bin/sh\nsleep 60\n")
	post := func(parts ...[2]string) (int, string) {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		for _, p := range parts {
			w, _ := mw.CreateFormFile(p[0], p[0])
			_, _ = w.Write([]byte(p[1]))
		}
		_ = mw.Close()
		resp, err := http.Post(ts.URL+"/api/v1/worker-types/installed", mw.FormDataContentType(), &body)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var out bytes.Buffer
		_, _ = out.ReadFrom(resp.Body)
		return resp.StatusCode, out.String()
	}
	if code, raw := post([2]string{"artifact", string(bin)}, [2]string{"manifest", string(echoManifest("1.0.0", bin))}); code != http.StatusBadRequest || !strings.Contains(raw, "before the artifact") {
		t.Errorf("artifact first: %d %s", code, raw)
	}
	if code, raw := post([2]string{"manifest", string(echoManifest("1.0.0", bin))}); code != http.StatusBadRequest || !strings.Contains(raw, "want the parts") {
		t.Errorf("no artifact: %d %s", code, raw)
	}
	if code, raw := installUpload(t, ts.URL, echoManifest("1.0.0", bin), bin); code != http.StatusCreated {
		t.Fatalf("install: %d %s", code, raw)
	}
	other := []byte("#!/bin/sh\nsleep 61\n")
	if code, raw := installUpload(t, ts.URL, echoManifest("1.0.0", other), other); code != http.StatusConflict {
		t.Errorf("another artifact under 1.0.0: %d %s, want 409", code, raw)
	}
	resp, err := http.Get(ts.URL + "/api/v1/worker-types/installed")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var list struct {
		Installed     []installedView `json:"installed"`
		Platform      string          `json:"platform"`
		AllowUnsigned bool            `json:"allowUnsigned"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	if len(list.Installed) != 1 || list.Platform != install.Platform() || !list.AllowUnsigned {
		t.Errorf("list = %+v", list)
	}
}

// A release installed before the server starts is launched when it starts: the store
// is the record, so a restart brings every installed Worker Type back.
func TestAnInstalledReleaseIsLaunchedWhenTheServerStarts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in executable is a POSIX shell script")
	}
	dir := t.TempDir()
	store, err := install.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	bin := []byte("#!/bin/sh\nsleep 60\n")
	if _, err := store.Install(echoManifest("1.0.0", bin), bytes.NewReader(bin), install.Platform(), time.Now()); err != nil {
		t.Fatal(err)
	}
	srv, _ := newValidateServer(t, WithInstalledWorkerTypes(dir, "http://127.0.0.1:1", false))
	id := installedSupervisorID("com.example.echo", "1.0.0")
	deadline := time.Now().Add(20 * time.Second)
	for {
		for _, c := range srv.supervisor.list() {
			if c.ID == id && c.State == "running" {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("supervisor = %+v, want %s running", srv.supervisor.list(), id)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A configured Worker reaches its instance (ADR-0535 §4): its settings are checked
// against the manifest's schema, its secret is named by a vault entry and resolved only
// for the launched instance, and the instance restarts with it.
func TestAConfiguredWorkerReachesItsInstance(t *testing.T) {
	_, bin := buildEcho(t)
	srv, ts := installServer(t, true)
	if code, raw := installUpload(t, ts.URL, echoManifest("1.0.0", bin), bin); code != http.StatusCreated {
		t.Fatalf("install: status=%d body=%s", code, raw)
	}
	if _, err := srv.vault.Set("echo-token", "s3cr3t-value"); err != nil {
		t.Fatal(err)
	}
	put := func(name, body string) (int, string) {
		code, raw := serveInternal(t, srv, http.MethodPut, "/api/v1/worker-types/installed/com.example.echo/workers/"+name, body, "application/json")
		return code, string(raw)
	}
	for _, tc := range []struct{ name, body, want string }{
		{"main", `{"config":{"greeting":"Grüezi","token":"inline"}}`, "is a secret"},
		{"main", `{"config":{},"secrets":{}}`, "greeting"},
		{"main", `{"config":{"greeting":"x"},"secrets":{"greeting":"echo-token"}}`, "not a secret"},
		{"main", `{"config":{"greeting":"x"},"secrets":{"token":"no-such-entry"}}`, "does not exist"},
		{"Bad_Name", `{"config":{"greeting":"x"}}`, "lower-case"},
	} {
		if code, raw := put(tc.name, tc.body); code != http.StatusBadRequest || !strings.Contains(raw, tc.want) {
			t.Errorf("PUT %s %s: %d %s, want 400 naming %q", tc.name, tc.body, code, raw, tc.want)
		}
	}
	if code, raw := put("main", `{"config":{"greeting":"Grüezi"},"secrets":{"token":"echo-token"}}`); code != http.StatusOK {
		t.Fatalf("PUT main: %d %s", code, raw)
	}
	_, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/worker-types/installed/com.example.echo/workers", "", "")
	if !strings.Contains(string(raw), `"echo-token"`) || strings.Contains(string(raw), "s3cr3t-value") {
		t.Errorf("list = %s, want the vault entry's name and never its value", raw)
	}

	if code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", echoBPMN, "application/xml"); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy: %d %s", code, raw)
	}
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/processes/1/instances", `{"variables":{}}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("start: %d %s", code, raw)
	}
	var started struct {
		Key uint64 `json:"instanceKey"`
	}
	_ = json.Unmarshal(raw, &started)
	var vars map[string]any
	deadline := time.Now().Add(60 * time.Second)
	for {
		_, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/instances/"+strconv.FormatUint(started.Key, 10)+"/variables", "", "")
		_ = json.Unmarshal(raw, &vars)
		if vars["echoedBy"] != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the job was never completed; variables=%s", raw)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if vars["salutation"] != "Grüezi" || vars["authenticated"] != true || vars["echoWorker"] != "main" {
		t.Errorf("variables = %v, want the configured greeting and the token acknowledged", vars)
	}
	for _, v := range vars {
		if v == "s3cr3t-value" {
			t.Error("the secret reached the process variables")
		}
	}
	if code, _ := serveInternal(t, srv, http.MethodDelete, "/api/v1/worker-types/installed/com.example.echo/workers/main", "", ""); code != http.StatusOK {
		t.Errorf("delete: %d", code)
	}
	if code, _ := serveInternal(t, srv, http.MethodDelete, "/api/v1/worker-types/installed/com.example.echo/workers/main", "", ""); code != http.StatusNotFound {
		t.Errorf("delete again: %d, want 404", code)
	}
	if code, _ := put("other", `{"config":{"greeting":"x"}}`); code != http.StatusOK {
		t.Errorf("a Worker without secrets: %d", code)
	}
	if code, raw := serveInternal(t, srv, http.MethodPut, "/api/v1/worker-types/installed/com.example.none/workers/x", `{"config":{}}`, "application/json"); code != http.StatusBadRequest || !strings.Contains(string(raw), "no installed") {
		t.Errorf("a Worker of a type not installed: %d %s", code, raw)
	}
}

// A Worker whose configuration no longer holds at spawn — its vault entry gone, or a
// release that declares no configuration — is left out of what the instance receives
// rather than handed over half resolved.
func TestAConfiguredWorkerThatNoLongerResolvesIsLeftOut(t *testing.T) {
	dir := t.TempDir()
	srv, _ := newValidateServer(t, WithInstalledWorkerTypes(dir, "http://127.0.0.1:1", false))
	bin := []byte("not run in this test")
	inst, err := srv.installedTypes.Install(echoManifest("1.0.0", bin), bytes.NewReader(bin), install.Platform(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.vault.Set("echo-token", "v"); err != nil {
		t.Fatal(err)
	}
	srv.do(func() {
		err = srv.installedWorkers.Save(installedWorker{WorkerType: inst.ID, Name: "main",
			Config: map[string]any{"greeting": "Hallo"}, Secrets: map[string]string{"token": "echo-token"}})
	})
	if err != nil {
		t.Fatal(err)
	}
	env := srv.installedWorkerConfigEnv(inst)
	if len(env) != 1 || !strings.Contains(env[0], `"token":"v"`) || !strings.HasPrefix(env[0], "ATLAS_WORKER_CONFIG=") {
		t.Fatalf("env = %v, want the Worker with its secret resolved", env)
	}
	if err := srv.vault.Delete("echo-token"); err != nil {
		t.Fatal(err)
	}
	if env := srv.installedWorkerConfigEnv(inst); env != nil {
		t.Errorf("env with the vault entry gone = %v, want nothing handed over", env)
	}
	// A release whose manifest declares no configuration takes none.
	bare := inst
	bare.Manifest = json.RawMessage(strings.Replace(string(inst.Manifest), `"configuration":`, `"x":`, 1))
	if env := srv.installedWorkerConfigEnv(bare); env != nil {
		t.Errorf("env for a release that cannot be read = %v, want nothing", env)
	}
	if _, err := srv.resolvedConfig(installedWorker{}, nil, nil); err == nil {
		t.Error("resolvedConfig without a schema = nil error")
	}
}

// Without a store there are no configured Workers; a body that is not JSON is refused.
func TestConfiguredWorkerRoutesWithoutAStoreAndWithBadInput(t *testing.T) {
	plain, _ := newValidateServer(t)
	for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete} {
		path := "/api/v1/worker-types/installed/a.b/workers"
		if method != http.MethodGet {
			path += "/main"
		}
		if code, _ := serveInternal(t, plain, method, path, `{}`, "application/json"); code != http.StatusNotFound {
			t.Errorf("%s without a store: %d, want 404", method, code)
		}
	}
	srv, _ := newValidateServer(t, WithInstalledWorkerTypes(t.TempDir(), "http://127.0.0.1:1", true))
	if code, _ := serveInternal(t, srv, http.MethodPut, "/api/v1/worker-types/installed/a.b/workers/main", `{`, "application/json"); code != http.StatusBadRequest {
		t.Errorf("a body that is not JSON: %d, want 400", code)
	}
	if code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/worker-types/installed/a.b/workers", "", ""); code != http.StatusOK || !strings.Contains(string(raw), `"workers":[]`) {
		t.Errorf("list of a type with no Workers: %d %s", code, raw)
	}
}
