package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// brokenResolver is a DMN resolver that always fails with a non-not-found error,
// to drive the resolve-error branch of bundle-deploy.
type brokenResolver struct{}

func (brokenResolver) Resolve(context.Context, string) ([]byte, error) {
	return nil, errors.New("resolver down")
}

// TestProcessLookup covers the DMN worker's process resolver: a deployed key
// resolves to its process, an unknown key to nil.
func TestProcessLookup(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	pid := x.mkProject("P")
	x.saveDraft(pid, deployableBPMN)
	code, b := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", "")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, b)
	}
	var rep projectDeployResp
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	key := rep.Definitions[0].Key
	if srv.processLookup(key) == nil {
		t.Fatal("processLookup(deployed key) = nil, want the process")
	}
	if srv.processLookup(999999) != nil {
		t.Fatal("processLookup(unknown key) != nil, want nil")
	}
}

// TestDeployModelBadDMN covers deployModel's DMN-registration error branch: an
// uncompilable DMN snapshot is a server error (the caller validates first, so
// this is a defensive path reached here directly).
func TestDeployModelBadDMN(t *testing.T) {
	srv, _ := newValidateServer(t)
	var persistErr error
	srv.do(func() {
		_, _, persistErr = srv.deployModel([]byte(deployableBPMN), []byte("<not-dmn"), 123)
	})
	if persistErr == nil {
		t.Fatal("deployModel with an uncompilable DMN snapshot: want an error")
	}
}

// TestBundleDeployResolveError covers bundle-deploy's resolve-error branch: the
// preflight validates a reference (its validator keeps a working resolver) but
// resolving the model for deployment fails, so the deploy is a 500.
func TestBundleDeployResolveError(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	pid := x.mkProject("P")
	x.saveDraft(pid, deployableBPMN)
	x.addRef(pid, "Dish", "dish")

	srv.dmnResolver = brokenResolver{} // validator still resolves; explicit resolve fails
	if code, _ := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", ""); code != http.StatusInternalServerError {
		t.Fatalf("deploy with broken resolver = %d, want 500", code)
	}
}

// dinnerBPMN is a Start → BusinessRuleTask(decision "Dish") → End process. Its
// task feeds the decision a static Season input; the decision lives in the
// project's DMN reference (dish.dmn, decision "Dish").
const dinnerBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="dinner" isExecutable="true">
    <startEvent id="s"/>
    <businessRuleTask id="decide">
      <extensionElements>
        <calledDecision decisionId="Dish" retries="3"/>
        <decisionInput name="Season" value="Winter"/>
      </extensionElements>
    </businessRuleTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="decide"/>
    <sequenceFlow id="f2" sourceRef="decide" targetRef="e"/>
  </process>
</definitions>`

// TestBundleDeployExecutesDMN is the whole point of wiring DMN into the engine:
// a business rule task in a bundle-deployed project evaluates its decision
// in-process and the instance runs to completion, instead of parking forever.
func TestBundleDeployExecutesDMN(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	pid := x.mkProject("Dinner")
	x.saveDraft(pid, dinnerBPMN)
	x.addRef(pid, "Dish decision", "dish") // dish.dmn provides decision "Dish"

	code, b := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", "")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, b)
	}
	var rep projectDeployResp
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !rep.Deployed || len(rep.Definitions) != 1 {
		t.Fatalf("deploy = %+v, want one definition deployed", rep)
	}
	key := rep.Definitions[0].Key

	// Creating an instance drives the DMN job to completion in-process, so no token
	// parks on the business rule task — the instance finishes.
	code, b = x.do(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", key), "{}")
	if code != http.StatusOK {
		t.Fatalf("create instance status=%d body=%s", code, b)
	}
	var ci struct {
		Stats struct {
			ActiveProcessInstances int `json:"activeProcessInstances"`
			ActiveElementInstances int `json:"activeElementInstances"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(b, &ci); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if ci.Stats.ActiveProcessInstances != 0 || ci.Stats.ActiveElementInstances != 0 {
		t.Fatalf("stats = %+v, want 0/0 (DMN evaluated, instance completed — not parked)", ci.Stats)
	}
	if _, list := x.do(http.MethodGet, "/api/v1/instances", ""); !strings.Contains(string(list), `"state":"completed"`) {
		t.Fatalf("instance did not complete: %s", list)
	}
}

// TestBundleDeployRefusedWhenDecisionMissing refuses a bundle whose BPMN
// references a decision no project DMN model provides.
func TestBundleDeployRefusedWhenDecisionMissing(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	pid := x.mkProject("Dinner")
	x.saveDraft(pid, dinnerBPMN) // references decision "Dish"
	// No DMN reference added → nothing provides "Dish".

	code, b := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", "")
	if code != http.StatusConflict {
		t.Fatalf("deploy status=%d body=%s, want 409", code, b)
	}
	var rep projectDeployResp
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rep.Deployed || !strings.Contains(rep.Reason, "Dish") {
		t.Fatalf("result = %+v, want refused naming the missing decision", rep)
	}
	if _, list := x.do(http.MethodGet, "/api/v1/processes", ""); strings.Contains(string(list), `"processId":"dinner"`) {
		t.Fatalf("a definition was deployed despite the missing decision: %s", list)
	}
}

// bootAPIWithModels opens the whole stack over dir (seeding the DMN model source
// once) and returns the server plus a close func, so a test can restart over the
// same dir to prove recovery.
func bootAPIWithModels(t *testing.T, dir string, seed bool) (*Server, func()) {
	t.Helper()
	if seed {
		models := filepath.Join(dir, "dmn-models")
		if err := os.MkdirAll(models, 0o755); err != nil {
			t.Fatalf("mkdir models: %v", err)
		}
		if err := os.WriteFile(filepath.Join(models, "dish.dmn"), []byte(validDMNModel), 0o644); err != nil {
			t.Fatalf("write dish: %v", err)
		}
	}
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
	srv, err := New(proc, store, dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv, func() { srv.Close(); _ = store.Close(); _ = log.Close() }
}

// TestMatchModelPicksCoveringModel checks the model-matching helper directly:
// it picks the model that provides every needed decision, and reports no match
// when the decisions span models or are absent.
func TestMatchModelPicksCoveringModel(t *testing.T) {
	models := []resolvedModel{
		{decisions: []string{"A", "B"}, xml: []byte("<first/>")},
		{decisions: []string{"Dish", "C"}, xml: []byte("<second/>")},
	}
	if xml, ok := matchModel(models, []string{"Dish"}); !ok || string(xml) != "<second/>" {
		t.Fatalf("matchModel = (%q, %v), want the second model", xml, ok)
	}
	if _, ok := matchModel(models, []string{"A", "Dish"}); ok {
		t.Fatal("matchModel spanning two models: want no match")
	}
	if _, ok := matchModel(models, []string{"Zzz"}); ok {
		t.Fatal("matchModel unknown decision: want no match")
	}
}

// TestReloadFailsOnBadDMNSnapshot covers loadDeployments' DMN re-registration
// error branch: a persisted deployment whose DMN snapshot no longer compiles is a
// hard startup error, not a silently dropped decision.
func TestReloadFailsOnBadDMNSnapshot(t *testing.T) {
	dir := t.TempDir()
	ds, err := newDeployStore(filepath.Join(dir, "deployments"))
	if err != nil {
		t.Fatalf("newDeployStore: %v", err)
	}
	// Valid BPMN, but an uncompilable DMN snapshot.
	if err := ds.save(persistedDeployment{Key: 1, ProcessID: "dinner", Version: 1, XML: dinnerBPMN, DMNXML: "<not-dmn"}); err != nil {
		t.Fatalf("save: %v", err)
	}
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
	if srv, err := New(proc, store, dir); err == nil {
		srv.Close()
		t.Fatal("New with a bad DMN snapshot: want error")
	}
	_ = store.Close()
	_ = log.Close()
}

// TestDeployedDMNSurvivesRestart proves the snapshotted DMN model re-registers on
// restart: an instance created after a restart still evaluates its decision and
// completes.
func TestDeployedDMNSurvivesRestart(t *testing.T) {
	dir := t.TempDir()

	srv1, close1 := bootAPIWithModels(t, dir, true)
	x1 := deployTestHarness{t, srv1.Handler()}
	pid := x1.mkProject("Dinner")
	x1.saveDraft(pid, dinnerBPMN)
	x1.addRef(pid, "Dish", "dish")
	code, b := x1.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", "")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, b)
	}
	var rep projectDeployResp
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("decode: %v", err)
	}
	key := rep.Definitions[0].Key
	close1()

	// Restart over the same dir: the deployment reloads and its DMN model
	// re-registers from the snapshot (no temis reference re-resolved).
	srv2, close2 := bootAPIWithModels(t, dir, false)
	defer close2()
	x2 := deployTestHarness{t, srv2.Handler()}

	code, b = x2.do(http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", key), "{}")
	if code != http.StatusOK {
		t.Fatalf("create instance after restart status=%d body=%s", code, b)
	}
	var ci struct {
		Stats struct {
			ActiveElementInstances int `json:"activeElementInstances"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(b, &ci); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ci.Stats.ActiveElementInstances != 0 {
		t.Fatalf("after restart: active elements = %d, want 0 (DMN re-registered and evaluated)", ci.Stats.ActiveElementInstances)
	}
}
