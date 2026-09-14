package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/dmn"
)

// Trying a decision before anything is deployed
// (ADR-0326). The claim under test is that the
// answer comes from the bytes in the request and from nothing else: no key, no
// record, no registry, nothing left behind.

// tryDecision posts a model to the try route and decodes the answer.
func tryDecision(t *testing.T, x deployTestHarness, payload map[string]any) dmn.Trial {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal try payload: %v", err)
	}
	code, b := x.do(http.MethodPost, "/api/v1/decisions/evaluate", string(body))
	if code != http.StatusOK {
		t.Fatalf("try decision: %d %s", code, b)
	}
	var out dmn.Trial
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode trial: %v (%s)", err, b)
	}
	return out
}

// The headline: an author types a table, presses Test, and is told what it
// returned and which rule returned it — with the model stored nowhere.
func TestADecisionCanBeTriedWithoutBeingStoredAnywhere(t *testing.T) {
	srv, dir := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}
	before := modelFiles(t, dir)

	got := tryDecision(t, x, map[string]any{
		"xml": eligibilityDMN("approve"), "decisionId": "eligibility",
		"inputs": map[string]any{"amount": 250},
	})

	if !got.OK || got.Message != "" {
		t.Fatalf("trial = %+v, want it to have run", got)
	}
	if got.Outputs["eligibility"] != "approve" {
		t.Fatalf("outputs = %v, want eligibility=approve", got.Outputs)
	}
	if len(got.Trace) == 0 {
		t.Fatal("no trace came back; which rule fired is the whole point of the panel")
	}
	// The trace is temis's own, so the matrix the Console already draws renders it.
	var trace struct {
		Tables []struct {
			Rules []struct {
				Index   int  `json:"index"`
				Matched bool `json:"matched"`
			} `json:"rules"`
		} `json:"tables"`
	}
	if err := json.Unmarshal(got.Trace, &trace); err != nil {
		t.Fatalf("decode trace: %v (%s)", err, got.Trace)
	}
	if len(trace.Tables) != 1 {
		t.Fatalf("trace tables = %d, want the one decision table", len(trace.Tables))
	}
	matched := 0
	for _, rule := range trace.Tables[0].Rules {
		if rule.Matched {
			matched++
		}
	}
	if matched != 1 {
		t.Fatalf("matched rules = %d, want exactly the one that fired", matched)
	}

	// And nothing was created by asking: no model, no deployment, no key spent.
	if after := modelFiles(t, dir); len(after) != len(before) {
		t.Fatalf("model folder = %v, want it untouched (%v)", after, before)
	}
	if rows := listDecisionDeployments(t, x, ""); len(rows) != 0 {
		t.Fatalf("decision deployments = %+v, want none — trying is not deploying", rows)
	}
	srv.do(func() {
		if key, ok := srv.dmnRegistry.LatestDecisionKey("eligibility"); ok {
			t.Errorf("the registry holds decision deployment %d after a try", key)
		}
	})
}

// With no decision named the model is only described — which is how the panel
// learns what it may run and what each decision wants, out of the same compile.
func TestTryingWithNoDecisionNamedDescribesTheModel(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	got := tryDecision(t, x, map[string]any{"xml": eligibilityDMN("approve")})

	if !got.OK || got.ModelName != "Eligibility" {
		t.Fatalf("trial = %+v, want the model described", got)
	}
	if len(got.Decisions) != 1 || got.Decisions[0].ID != "eligibility" {
		t.Fatalf("decisions = %+v, want the one this model provides", got.Decisions)
	}
	if len(got.Decisions[0].Inputs) != 1 || got.Decisions[0].Inputs[0].Name != "amount" {
		t.Fatalf("inputs = %+v, want the input data the decision consumes", got.Decisions[0].Inputs)
	}
	if got.Outputs != nil || len(got.Trace) != 0 {
		t.Fatalf("trial = %+v, want nothing evaluated when no decision was named", got)
	}
}

// A model that does not compile, a decision that is not in it, and an evaluation
// that fails are all normal states of a decision being written. They come back as
// results to render, not as HTTP errors to handle.
func TestWhatDoesNotWorkYetComesBackAsAResult(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	for _, tc := range []struct {
		name    string
		payload map[string]any
		wantIn  string
	}{
		{
			"a table that does not compile",
			map[string]any{"xml": brokenDMNModel, "decisionId": "bad"},
			"",
		},
		{
			"a decision the model does not provide",
			map[string]any{"xml": eligibilityDMN("approve"), "decisionId": "nope"},
			"provides no decision called nope",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tryDecision(t, x, tc.payload)
			if got.OK {
				t.Fatalf("trial = %+v, want ok:false", got)
			}
			if got.Message == "" {
				t.Fatal("ok:false with no message — the panel has nothing to show")
			}
			if tc.wantIn != "" && !strings.Contains(got.Message, tc.wantIn) {
				t.Fatalf("message = %q, want it to contain %q", got.Message, tc.wantIn)
			}
		})
	}
}

// A caller's mistake is still a 400: the distinction is whether the request is
// malformed or the model is unfinished.
func TestAMalformedTryRequestIsRefused(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	for _, tc := range []struct{ name, body string }{
		{"not json at all", "<definitions/>"},
		{"no model to try", `{"decisionId":"eligibility"}`},
		{"an empty model", `{"xml":"","decisionId":"eligibility"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code, b := x.do(http.MethodPost, "/api/v1/decisions/evaluate", tc.body); code != http.StatusBadRequest {
				t.Fatalf("try = %d %s, want 400", code, b)
			}
		})
	}
}

// The inputs decide the answer, which is the point of trying one: the same table
// run twice over different values must take different rules.
func TestTheInputsDecideWhatComesBack(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	high := tryDecision(t, x, map[string]any{
		"xml": eligibilityDMN("approve"), "decisionId": "eligibility",
		"inputs": map[string]any{"amount": 250},
	})
	low := tryDecision(t, x, map[string]any{
		"xml": eligibilityDMN("approve"), "decisionId": "eligibility",
		"inputs": map[string]any{"amount": 12},
	})
	if high.Outputs["eligibility"] != "approve" || low.Outputs["eligibility"] != "reject" {
		t.Fatalf("high = %v, low = %v — want the rules to divide at 100", high.Outputs, low.Outputs)
	}
}

// Trying the edit on screen is the whole point, so what is stored under the same
// decision id must not answer instead of it — including a version that is deployed.
func TestTryingAnswersFromTheModelInTheRequestNotTheDeployedOne(t *testing.T) {
	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	// Deploy one version, then try a different edit of the same decision id.
	if code, b := x.do(http.MethodPost, "/api/v1/decision-deployments", eligibilityDMN("approve")); code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, b)
	}
	got := tryDecision(t, x, map[string]any{
		"xml": eligibilityDMN("vip"), "decisionId": "eligibility",
		"inputs": map[string]any{"amount": 250},
	})
	if got.Outputs["eligibility"] != "vip" {
		t.Fatalf("outputs = %v, want vip — the model in the request, not the deployed one", got.Outputs)
	}
	// And trying did not disturb what is deployed.
	rows := listDecisionDeployments(t, x, "?decisionId=eligibility")
	if len(rows) != 1 || rows[0].Version != 1 {
		t.Fatalf("deployments = %+v, want the one deploy, still at v1", rows)
	}
}
