package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The decision peek shows the model a business rule task's binding resolves to
// (ADR-draft-decision-peek): GET /api/v1/decisions/{id}/binding. These pin each way
// a binding resolves, for a draft and for a deployed definition, against what the
// deploy and the worker actually do.

// boundView is the part of the answer the tests read.
type boundView struct {
	Perspective     string          `json:"perspective"`
	Binding         string          `json:"binding"`
	Source          string          `json:"source"`
	Key             uint64          `json:"key"`
	DeployedVersion int32           `json:"deployedVersion"`
	DeployedAt      int64           `json:"deployedAt"`
	ModelName       string          `json:"modelName"`
	Fallback        bool            `json:"fallback"`
	Reason          string          `json:"reason"`
	Versions        []int32         `json:"versions"`
	Ref             *boundRef       `json:"ref"`
	Graph           json.RawMessage `json:"graph"`
	Logic           json.RawMessage `json:"logic"`
}

func readBound(t *testing.T, x deployTestHarness, query string) boundView {
	t.Helper()
	code, b := x.do(http.MethodGet, "/api/v1/decisions/eligibility/binding"+query, "")
	if code != http.StatusOK {
		t.Fatalf("binding%s: %d %s", query, code, b)
	}
	var v boundView
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return v
}

// storeEligibility files the eligibility model as a stored model with a reference,
// as an author's "Save to model" does, answering verdict for an amount over 100.
func storeEligibility(t *testing.T, x deployTestHarness, dir, verdict string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "dmn-models", "eligibility.dmn"), []byte(eligibilityDMN(verdict)), 0o644); err != nil {
		t.Fatalf("write model: %v", err)
	}
	code, b := x.do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"Eligibility","modelRef":"eligibility"}`)
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create ref: %d %s", code, b)
	}
	var ref struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &ref); err != nil || ref.ID == "" {
		t.Fatalf("decode ref: %v (%s)", err, b)
	}
	return ref.ID
}

// A draft's task is resolved as a deploy now would resolve it: latest to the newest
// decision deployment, a version to the deployment holding it, and — without a
// decision deployment, or bound to deployment — to the stored model a deploy bundles.
func TestADraftsBindingResolvesAsADeployNowWould(t *testing.T) {
	srv, dir := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	// Nothing anywhere: nothing would answer, and the deploy would be refused.
	if v := readBound(t, x, ""); v.Source != "none" || v.Reason != "noModel" {
		t.Fatalf("no model at all = %+v, want none/noModel", v)
	}

	// A stored model only: latest falls back to the model the deploy would bundle.
	refID := storeEligibility(t, x, dir, "stored")
	v := readBound(t, x, "?binding=latest")
	if v.Source != "storedModel" || !v.Fallback || v.Ref == nil || v.Ref.ID != refID {
		t.Fatalf("latest with only a stored model = %+v, want the stored model %s as a fallback", v, refID)
	}
	if !strings.Contains(string(v.Logic), `stored`) || !strings.Contains(string(v.Graph), `"eligibility"`) {
		t.Errorf("stored model's graph/logic = %s / %s, want the stored model's", v.Graph, v.Logic)
	}

	// Two versions deployed: latest is the newest, a version is that version.
	deployOneDecision(t, x, "", eligibilityDMN("approve"))
	v2 := deployOneDecision(t, x, "", eligibilityDMN("vip"))
	v = readBound(t, x, "?binding=latest")
	if v.Source != "decisionDeployment" || v.Key != v2.Key || v.DeployedVersion != 2 || v.Fallback || v.DeployedAt == 0 || v.ModelName != "Eligibility" {
		t.Fatalf("latest = %+v, want decision deployment v2 (key %d)", v, v2.Key)
	}
	if !strings.Contains(string(v.Logic), `vip`) {
		t.Errorf("latest logic = %s, want v2's rules", v.Logic)
	}
	v = readBound(t, x, "?binding=version&version=1")
	if v.Source != "decisionDeployment" || v.DeployedVersion != 1 || !strings.Contains(string(v.Logic), `approve`) {
		t.Fatalf("version 1 = %+v %s, want v1's model", v, v.Logic)
	}

	// A version that is not deployed: said, with what is.
	v = readBound(t, x, "?binding=version&version=5")
	if v.Source != "none" || v.Reason != "versionNotDeployed" || fmt.Sprint(v.Versions) != "[1 2]" {
		t.Fatalf("version 5 = %+v, want versionNotDeployed listing [1 2]", v)
	}

	// Deployment-bound: the bundle, whatever is deployed on its own.
	v = readBound(t, x, "?binding=deployment")
	if v.Source != "storedModel" || v.Fallback || !strings.Contains(string(v.Logic), `stored`) {
		t.Fatalf("deployment = %+v, want the stored model, not as a fallback", v)
	}
}

// A deployed definition's task is resolved by the worker's own function, against
// what it was deployed with.
func TestADeployedDefinitionsBindingResolvesAsTheWorkerWould(t *testing.T) {
	srv, dir := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	storeEligibility(t, x, dir, "stored")
	deployOneDecision(t, x, "", eligibilityDMN("approve"))

	latestDef := deployProcess(t, x, eligibilityProcess("orders", "latest"))
	pinnedDef := deployProcess(t, x, eligibilityProcessAt("pinned", 1))
	bundleDef := deployProcess(t, x, eligibilityProcess("bundled", "deployment"))
	v2 := deployOneDecision(t, x, "", eligibilityDMN("vip"))

	// Latest follows the newest version when the task runs: v2, deployed after it.
	v := readBound(t, x, fmt.Sprintf("?processKey=%d&elementId=decide", latestDef))
	if v.Perspective != "definition" || v.Binding != "latest" || v.Source != "decisionDeployment" || v.Key != v2.Key || v.DeployedVersion != 2 {
		t.Fatalf("latest-bound definition = %+v, want v2 (key %d)", v, v2.Key)
	}
	// The draft's binding parameters are ignored: the compiled task is the authority.
	v = readBound(t, x, fmt.Sprintf("?binding=deployment&processKey=%d&elementId=decide", pinnedDef))
	if v.Binding != "version" || v.DeployedVersion != 1 || !strings.Contains(string(v.Logic), `approve`) {
		t.Fatalf("version-bound definition = %+v %s, want v1", v, v.Logic)
	}
	// Deployment-bound: the model bundled with the definition, under its own key —
	// the stored model as it was deployed, even after the stored model changes.
	if err := os.WriteFile(filepath.Join(dir, "dmn-models", "eligibility.dmn"), []byte(eligibilityDMN("changed")), 0o644); err != nil {
		t.Fatalf("rewrite model: %v", err)
	}
	v = readBound(t, x, fmt.Sprintf("?processKey=%d&elementId=decide", bundleDef))
	if v.Source != "bundle" || v.Key != bundleDef || !strings.Contains(string(v.Logic), `stored`) || strings.Contains(string(v.Logic), `changed`) {
		t.Fatalf("deployment-bound definition = %+v %s, want its own bundle as deployed", v, v.Logic)
	}

	// An element that is not a decision task is said, not guessed at.
	if v = readBound(t, x, fmt.Sprintf("?processKey=%d&elementId=wait", latestDef)); v.Source != "none" || v.Reason != "notADecisionTask" {
		t.Fatalf("user task = %+v, want notADecisionTask", v)
	}
	if code, _ := x.do(http.MethodGet, "/api/v1/decisions/eligibility/binding?processKey=999999&elementId=decide", ""); code != http.StatusNotFound {
		t.Fatalf("unknown definition = %d, want 404", code)
	}
}

// What the route refuses to guess at.
func TestDecisionBindingRefusesWhatItCannotRead(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	for _, q := range []string{"?binding=versionTag", "?binding=version", "?binding=version&version=0", "?version=x", "?processKey=abc"} {
		if code, b := x.do(http.MethodGet, "/api/v1/decisions/eligibility/binding"+q, ""); code != http.StatusBadRequest {
			t.Errorf("binding%s = %d %s, want 400", q, code, b)
		}
	}
}
