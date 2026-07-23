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
	srv.drafts = &draftStore{dir: filepath.Join(t.TempDir(), "gone")}
	if code, _ := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", ""); code != http.StatusInternalServerError {
		t.Fatalf("deploy with broken drafts = %d, want 500", code)
	}
	srv.drafts = realDrafts

	// Broken project read (dir at the record path) → 500.
	ps, err := newProjectStore(filepath.Join(t.TempDir(), "projects"))
	if err != nil {
		t.Fatalf("newProjectStore: %v", err)
	}
	if err := os.MkdirAll(ps.fileFor(pid), 0o755); err != nil {
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
	srv.do(func() { refs, _ = srv.dmnrefs.loadAll() })
	for _, rr := range refs {
		if rr.ModelRef == "busy" {
			srv.do(func() { _ = srv.dmnrefs.delete(rr.ID) })
		}
	}

	// Broken deployment store → persist fails during phase-3 deploy → 500.
	srv.deploys = &deployStore{dir: filepath.Join(t.TempDir(), "gone")}
	if code, _ := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", ""); code != http.StatusInternalServerError {
		t.Fatalf("deploy with broken deploy store = %d, want 500", code)
	}
	srv.deploys = realDeploys
}
