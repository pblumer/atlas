package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A straight-through executable process (compiles + deploys) and a lone start
// event (the compiler rejects it) for the bundle-deploy tests.
const deployableBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="approve" isExecutable="true">
    <startEvent id="s"/>
    <endEvent id="e"/>
    <sequenceFlow id="f" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

// incompleteBPMN has no start event, which the compiler rejects.
const incompleteBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="wip"><endEvent id="e"/></process>
</definitions>`

// deployTestHarness drives the server's HTTP handlers and returns (code, body).
type deployTestHarness struct {
	t *testing.T
	h http.Handler
}

func (x deployTestHarness) do(method, path, body string) (int, []byte) {
	x.t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	rec := httptest.NewRecorder()
	x.h.ServeHTTP(rec, req)
	return rec.Code, rec.Body.Bytes()
}

func (x deployTestHarness) mkProject(name string) string {
	x.t.Helper()
	code, b := x.do(http.MethodPost, "/api/v1/projects", `{"name":"`+name+`"}`)
	if code != http.StatusOK {
		x.t.Fatalf("create project: %d", code)
	}
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		x.t.Fatalf("decode project: %v", err)
	}
	return p.ID
}

func (x deployTestHarness) saveDraft(projectID, xml string) {
	x.t.Helper()
	if code, b := x.do(http.MethodPost, "/api/v1/drafts?projectId="+projectID, xml); code != http.StatusOK {
		x.t.Fatalf("save draft: status=%d body=%s", code, b)
	}
}

func (x deployTestHarness) addRef(projectID, name, modelRef string) {
	x.t.Helper()
	if code, b := x.do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"`+name+`","modelRef":"`+modelRef+`","projectId":"`+projectID+`"}`); code != http.StatusOK {
		x.t.Fatalf("add ref: status=%d body=%s", code, b)
	}
}

// TestBundleDeploySucceeds deploys a project whose BPMN compiles and whose DMN
// reference is valid: the definition is registered and the reference is reported
// as validated.
func TestBundleDeploySucceeds(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	pid := x.mkProject("Bundle")
	x.saveDraft(pid, deployableBPMN)
	x.addRef(pid, "Dish", "dish") // dish.dmn is valid (seeded by newValidateServer)

	code, b := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", "")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, b)
	}
	var rep projectDeployResp
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !rep.Deployed || len(rep.Definitions) != 1 || rep.Definitions[0].ProcessID != "approve" {
		t.Fatalf("deploy result = %+v, want one deployed 'approve' definition", rep)
	}
	if len(rep.References) != 1 || !rep.References[0].Valid {
		t.Fatalf("references = %+v, want one valid reference", rep.References)
	}
	// The definition is now live.
	if _, list := x.do(http.MethodGet, "/api/v1/processes", ""); !strings.Contains(string(list), `"processId":"approve"`) {
		t.Fatalf("deployed process not listed: %s", list)
	}
}

// TestBundleDeployRefusedOnInvalidDMN refuses the whole bundle when a DMN
// reference does not resolve, and deploys nothing.
func TestBundleDeployRefusedOnInvalidDMN(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	pid := x.mkProject("Blocked")
	x.saveDraft(pid, deployableBPMN)
	x.addRef(pid, "Missing", "ghost") // no such model → unresolved

	code, b := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", "")
	if code != http.StatusConflict {
		t.Fatalf("deploy status=%d body=%s, want 409", code, b)
	}
	var rep projectDeployResp
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rep.Deployed || len(rep.Definitions) != 0 || rep.Reason == "" {
		t.Fatalf("result = %+v, want refused with a reason and nothing deployed", rep)
	}
	// Nothing was deployed.
	if _, list := x.do(http.MethodGet, "/api/v1/processes", ""); strings.Contains(string(list), `"processId":"approve"`) {
		t.Fatalf("a definition was deployed despite the invalid DMN: %s", list)
	}
}

// TestBundleDeployRefusedOnBadBPMN refuses the bundle when a draft does not
// compile, before deploying any definition.
func TestBundleDeployRefusedOnBadBPMN(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	pid := x.mkProject("Broken BPMN")
	x.saveDraft(pid, deployableBPMN)
	x.saveDraft(pid, incompleteBPMN) // won't compile

	code, b := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", "")
	if code != http.StatusConflict {
		t.Fatalf("deploy status=%d body=%s, want 409", code, b)
	}
	var rep projectDeployResp
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rep.Deployed || !strings.Contains(rep.Reason, "wip") {
		t.Fatalf("result = %+v, want refused naming the non-compiling draft", rep)
	}
	// The compilable draft must NOT have been deployed (validate-all-first).
	if _, list := x.do(http.MethodGet, "/api/v1/processes", ""); strings.Contains(string(list), `"processId":"approve"`) {
		t.Fatalf("a definition slipped through despite a bad sibling draft: %s", list)
	}
}

// TestBundleDeployEmptyProject deploys a project with no artifacts as a valid
// no-op.
func TestBundleDeployEmptyProject(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	pid := x.mkProject("Empty")
	code, b := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", "")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, b)
	}
	var rep projectDeployResp
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !rep.Deployed || len(rep.Definitions) != 0 || len(rep.References) != 0 {
		t.Fatalf("empty deploy = %+v, want deployed with nothing", rep)
	}
}

func TestBundleDeployUnknownProject(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	if code, _ := x.do(http.MethodPost, "/api/v1/projects/nope/deploy", ""); code != http.StatusNotFound {
		t.Fatalf("deploy unknown project: want 404")
	}
}

// assertProcessProject fails unless the deployed process is listed with the given
// project id (ADR-0034 grouping).
func assertProcessProject(t *testing.T, x deployTestHarness, processID, want string) {
	t.Helper()
	code, b := x.do(http.MethodGet, "/api/v1/processes", "")
	if code != http.StatusOK {
		t.Fatalf("list processes: %d", code)
	}
	var list []processResp
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatalf("decode processes: %v", err)
	}
	for _, p := range list {
		if p.ProcessID == processID {
			if p.ProjectID != want {
				t.Fatalf("process %q projectId = %q, want %q", processID, p.ProjectID, want)
			}
			return
		}
	}
	t.Fatalf("process %q not found in %s", processID, b)
}

// TestDeployStampsProjectID checks a deployment records the project its draft
// belonged to and surfaces it on /api/v1/processes, across every deploy route: a
// project bundle deploy, a direct deploy that names the project, and a direct
// deploy that inherits the project from the matching draft. An unknown project on
// a direct deploy is rejected, and a plain deploy stays ungrouped.
func TestDeployStampsProjectID(t *testing.T) {
	t.Run("project bundle deploy", func(t *testing.T) {
		srv, _ := newValidateServer(t)
		x := deployTestHarness{t, srv.Handler()}
		pid := x.mkProject("Grouped")
		x.saveDraft(pid, deployableBPMN)
		if code, b := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", ""); code != http.StatusOK {
			t.Fatalf("deploy: %d %s", code, b)
		}
		assertProcessProject(t, x, "approve", pid)
	})

	t.Run("direct deploy names the project", func(t *testing.T) {
		srv, _ := newValidateServer(t)
		x := deployTestHarness{t, srv.Handler()}
		pid := x.mkProject("Named")
		if code, b := x.do(http.MethodPost, "/api/v1/deployments?projectId="+pid, deployableBPMN); code != http.StatusOK {
			t.Fatalf("deploy: %d %s", code, b)
		}
		assertProcessProject(t, x, "approve", pid)
	})

	t.Run("direct deploy inherits from the draft", func(t *testing.T) {
		srv, _ := newValidateServer(t)
		x := deployTestHarness{t, srv.Handler()}
		pid := x.mkProject("Inherited")
		x.saveDraft(pid, deployableBPMN) // draft filed under the project
		if code, b := x.do(http.MethodPost, "/api/v1/deployments", deployableBPMN); code != http.StatusOK {
			t.Fatalf("deploy: %d %s", code, b)
		}
		assertProcessProject(t, x, "approve", pid)
	})

	t.Run("direct deploy rejects an unknown project", func(t *testing.T) {
		srv, _ := newValidateServer(t)
		x := deployTestHarness{t, srv.Handler()}
		if code, _ := x.do(http.MethodPost, "/api/v1/deployments?projectId=nope", deployableBPMN); code != http.StatusBadRequest {
			t.Fatalf("deploy unknown project: want 400")
		}
	})

	t.Run("plain direct deploy is ungrouped", func(t *testing.T) {
		srv, _ := newValidateServer(t)
		x := deployTestHarness{t, srv.Handler()}
		if code, b := x.do(http.MethodPost, "/api/v1/deployments", deployableBPMN); code != http.StatusOK {
			t.Fatalf("deploy: %d %s", code, b)
		}
		assertProcessProject(t, x, "approve", "")
	})
}

// TestBundleDeployStoreErrors covers the 500 branches: a broken artifact load, a
// broken project read, a resolver failure, and a persist failure during deploy.
func TestBundleDeployStoreErrors(t *testing.T) {
	srv, dir := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	pid := x.mkProject("P")
	x.saveDraft(pid, deployableBPMN)

	realDrafts := srv.drafts
	realProjects := srv.projects
	realDeploys := srv.deploys

	// Broken drafts store → artifact load fails.
	srv.drafts = brokenStore(newDraftStore(filepath.Join(t.TempDir(), "gone")))
	if code, _ := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", ""); code != http.StatusInternalServerError {
		t.Fatalf("deploy with broken drafts = %d, want 500", code)
	}
	srv.drafts = realDrafts

	// Broken project read (dir at the record path) → 500.
	ps, err := newProjectStore(filepath.Join(t.TempDir(), "projects"))
	if err != nil {
		t.Fatalf("newProjectStore: %v", err)
	}
	if err := os.MkdirAll(ps.FileFor(pid), 0o755); err != nil {
		t.Fatalf("mkdir record: %v", err)
	}
	srv.projects = ps
	if code, _ := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", ""); code != http.StatusInternalServerError {
		t.Fatalf("deploy with broken project read = %d, want 500", code)
	}
	srv.projects = realProjects

	// A resolver failure during the DMN preflight → 500.
	x.addRef(pid, "Busy", "busy")
	if err := os.MkdirAll(filepath.Join(dir, "dmn-models", "busy.dmn"), 0o755); err != nil {
		t.Fatalf("mkdir busy model: %v", err)
	}
	if code, _ := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", ""); code != http.StatusInternalServerError {
		t.Fatalf("deploy with broken resolver = %d, want 500", code)
	}
	// Remove the broken reference so the persist-failure case can reach phase 3.
	// (Delete it via the store directly, then continue.)
	var refs []dmnRef
	srv.do(func() { refs, _ = srv.dmnrefs.LoadAll() })
	for _, rr := range refs {
		if rr.ModelRef == "busy" {
			srv.do(func() { _ = srv.dmnrefs.Delete(rr.ID) })
		}
	}

	// Broken deployment store → persist fails during phase-3 deploy → 500.
	srv.deploys = brokenStore(newDeployStore(filepath.Join(t.TempDir(), "gone")))
	if code, _ := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", ""); code != http.StatusInternalServerError {
		t.Fatalf("deploy with broken deploy store = %d, want 500", code)
	}
	srv.deploys = realDeploys
}

// deployWarnProjectBPMN is deployWarnBPMN under a second process id, so a bundle can
// carry it alongside the plain deployable draft without the two colliding.
const deployWarnProjectBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
  <process id="warnbundle" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="notify">
      <extensionElements><atlas:mailConnector connector="Patrick Blumer" to="a@b.ch" subject="hi" body="hi"/></extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="notify"/>
    <sequenceFlow id="f2" sourceRef="notify" targetRef="end"/>
  </process>
</definitions>`

// TestBundleDeployWarnsAboutUnconfiguredWorker is the regression for the preflight
// that ran on one deploy path only. Publishing an application deploys the same models
// as POST /api/v1/deployments, so it owes the operator the same warning: a model
// naming a worker nobody configured deploys fine and then parks its first token, and
// "Publish" is the route most applications reach production through (ADR-0128). The
// deploy still succeeds — deploying before the workers exist is legitimate (ADR-0158).
func TestBundleDeployWarnsAboutUnconfiguredWorker(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	pid := x.mkProject("Preflight")
	x.saveDraft(pid, deployWarnProjectBPMN)

	code, b := x.do(http.MethodPost, "/api/v1/applications/"+pid+"/deploy", "")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, b)
	}
	var rep projectDeployResp
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !rep.Deployed || len(rep.Definitions) != 1 {
		t.Fatalf("deploy result = %+v, want the one definition registered", rep)
	}
	if len(rep.Warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly the unconfigured worker", rep.Warnings)
	}
	if !strings.Contains(rep.Warnings[0], "not configured on this server") ||
		!strings.Contains(rep.Warnings[0], "Patrick Blumer") {
		t.Errorf("warning = %q, want it to name the worker and say it is not configured", rep.Warnings[0])
	}
}

// TestBundleDeployIsSilentWhenTheWorkerIsThere pins the other half: the preflight
// says nothing when every reference resolves, so a publish does not learn to cry wolf.
func TestBundleDeployIsSilentWhenTheWorkerIsThere(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	if err := srv.connectors.Save(connector{
		ID: "1", Name: "Patrick Blumer", Kind: "mail", Provider: "smtp",
		Endpoint: "mx.example.ch:587", Sender: "a@x", Enabled: true, CreatedAt: 1,
	}); err != nil {
		t.Fatalf("save worker: %v", err)
	}
	if err := srv.rebuildConnectorRegistries(); err != nil {
		t.Fatalf("rebuild: %v", err)
	}

	pid := x.mkProject("Preflight quiet")
	x.saveDraft(pid, deployWarnProjectBPMN)

	code, b := x.do(http.MethodPost, "/api/v1/applications/"+pid+"/deploy", "")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, b)
	}
	var rep projectDeployResp
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rep.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none", rep.Warnings)
	}
}

// TestDedupeWarningsKeepsOrderAndDropsRepeats pins the one thing collapsing a
// bundle's warnings must not do: reorder them, or swallow two findings that merely
// look alike. Only an exact repeat goes.
func TestDedupeWarningsKeepsOrderAndDropsRepeats(t *testing.T) {
	if got := dedupeWarnings(nil); got != nil {
		t.Errorf("dedupeWarnings(nil) = %v, want nil", got)
	}
	one := []string{"a"}
	if got := dedupeWarnings(one); len(got) != 1 || got[0] != "a" {
		t.Errorf("dedupeWarnings(one) = %v, want it untouched", got)
	}
	got := dedupeWarnings([]string{"b", "a", "b", "c", "a"})
	want := []string{"b", "a", "c"}
	if len(got) != len(want) {
		t.Fatalf("dedupeWarnings = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("dedupeWarnings = %v, want %v", got, want)
		}
	}
}
