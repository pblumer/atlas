package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// TestSingleDeployBundlesDecision covers the Modeler-style single deploy resolving
// and bundling a referenced decision's model, so a business rule task deployed this
// way evaluates instead of creating a job that can never find a model (the poison
// job that used to fail every future deploy). The seeded dish.dmn provides "Dish".
func TestSingleDeployBundlesDecision(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	if code, b := x.do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"Dish","modelRef":"dish"}`); code != http.StatusOK {
		t.Fatalf("add ref: %d %s", code, b)
	}
	code, b := x.do(http.MethodPost, "/api/v1/deployments", dinnerBPMN)
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, b)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	_ = json.Unmarshal(b, &dep)
	// The instance evaluates the decision (Winter → Roastbeef) and runs to
	// completion, rather than erroring "no model deployed for def N".
	code, ib := x.do(http.MethodPost, "/api/v1/processes/"+strconv.FormatUint(dep.Key, 10)+"/instances", "{}")
	if code != http.StatusOK {
		t.Fatalf("start instance: %d %s", code, ib)
	}
	if !strings.Contains(string(ib), `"activeElementInstances":0`) {
		t.Fatalf("instance did not complete (decision not bundled?): %s", ib)
	}
}

// TestSingleDeploySkipsUnresolvedRef covers the skip branch: a dangling reference
// (its model is missing) is passed over, and a valid reference still provides the
// decision, so the deploy bundles the working model.
func TestSingleDeploySkipsUnresolvedRef(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	if code, b := x.do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"Missing","modelRef":"nope"}`); code != http.StatusOK {
		t.Fatalf("add dangling ref: %d %s", code, b)
	}
	if code, b := x.do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"Dish","modelRef":"dish"}`); code != http.StatusOK {
		t.Fatalf("add dish ref: %d %s", code, b)
	}
	if code, b := x.do(http.MethodPost, "/api/v1/deployments", dinnerBPMN); code != http.StatusOK {
		t.Fatalf("deploy = %d %s, want 200 (valid ref provides the decision)", code, b)
	}
}

// TestSingleDeployRefusesUnprovidedDecision covers the refusal: a business rule
// task whose decision no stored model provides is a clear 409, not a silent
// model-less deploy that would poison the run loop.
func TestSingleDeployRefusesUnprovidedDecision(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	code, b := x.do(http.MethodPost, "/api/v1/deployments", dinnerBPMN)
	if code != http.StatusConflict {
		t.Fatalf("deploy without a model = %d, want 409; body %s", code, b)
	}
	if !strings.Contains(string(b), "no DMN model provides") {
		t.Fatalf("refusal message = %s", b)
	}
}

// TestSingleDeployNoDecisionUnaffected covers the common path: a process with no
// business rule task needs no DMN and deploys unchanged.
func TestSingleDeployNoDecisionUnaffected(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	if code, b := x.do(http.MethodPost, "/api/v1/deployments", deployableBPMN); code != http.StatusOK {
		t.Fatalf("plain deploy = %d %s, want 200", code, b)
	}
}

// TestSingleDeployInvalidBpmn covers the compile-error path: dmnForDeployBody can't
// parse the body, so the deploy itself surfaces the 400 (client error).
func TestSingleDeployInvalidBpmn(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	if code, _ := x.do(http.MethodPost, "/api/v1/deployments", incompleteBPMN); code != http.StatusBadRequest {
		t.Fatalf("invalid bpmn deploy, want 400")
	}
}

// TestSingleDeployRefStoreError covers the ref-load failure branch: an unusable
// dmnref store makes the deploy a 500 before anything is registered.
func TestSingleDeployRefStoreError(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.dmnrefs = brokenStore(newDmnRefStore(filepath.Join(t.TempDir(), "gone")))
	x := deployTestHarness{t, srv.Handler()}
	if code, _ := x.do(http.MethodPost, "/api/v1/deployments", dinnerBPMN); code != http.StatusInternalServerError {
		t.Fatalf("deploy with a broken ref store, want 500")
	}
}

// TestSingleDeployResolveError covers the model-resolution failure branch: a
// reference exists but its source errors (a remote resolver that can't connect), a
// 500 rather than a refusal.
func TestSingleDeployResolveError(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	if code, b := x.do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"Dish","modelRef":"dish"}`); code != http.StatusOK {
		t.Fatalf("add ref: %d %s", code, b)
	}
	srv.dmnResolver = dmn.ServiceResolver{BaseURL: "http://127.0.0.1:0"} // connect fails
	x = deployTestHarness{t, srv.Handler()}
	if code, _ := x.do(http.MethodPost, "/api/v1/deployments", dinnerBPMN); code != http.StatusInternalServerError {
		t.Fatalf("deploy with a failing resolver, want 500")
	}
}
