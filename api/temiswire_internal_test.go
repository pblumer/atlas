package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestConnectorEnvKey covers the name → env-fragment normalization.
func TestConnectorEnvKey(t *testing.T) {
	cases := map[string]string{
		"risk":         "RISK",
		"risk-service": "RISK_SERVICE",
		"a.b/c":        "A_B_C",
		"  spaced  ":   "SPACED",
		"--edges--":    "EDGES",
		"under_score":  "UNDER_SCORE",
	}
	for in, want := range cases {
		if got := connectorEnvKey(in); got != want {
			t.Errorf("connectorEnvKey(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestTemisRegistryFromEnv builds the connector registry from the environment: a
// listed connector with a URL is registered (under its exact name), one without a
// URL is skipped, and an unlisted name is absent.
func TestTemisRegistryFromEnv(t *testing.T) {
	t.Setenv("ATLAS_TEMIS_CONNECTORS", "risk-service, nourl ,")
	t.Setenv("ATLAS_TEMIS_RISK_SERVICE_URL", "https://temis.example")
	t.Setenv("ATLAS_TEMIS_RISK_SERVICE_TOKEN", "tok")
	// nourl has no URL configured, so it is skipped.

	reg := temisRegistryFromEnv()
	if _, ok := reg.Client("risk-service"); !ok {
		t.Error("risk-service not registered, want it")
	}
	if _, ok := reg.Client("nourl"); ok {
		t.Error("nourl registered, want it skipped (no URL)")
	}
	if _, ok := reg.Client("other"); ok {
		t.Error("unlisted connector registered, want absent")
	}
}

// centralDecisionBPMN is Start → BusinessRuleTask(central, connector "risk") → End.
// It needs no local DMN model — the decision is evaluated by the remote connector.
const centralDecisionBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:atlas="http://atlas/schema/1.0">
  <process id="central" isExecutable="true">
    <startEvent id="s"/>
    <businessRuleTask id="decide">
      <extensionElements>
        <zeebe:calledDecision decisionId="Dish" resultVariable="dish"/>
        <atlas:temisConnector connector="risk"/>
      </extensionElements>
    </businessRuleTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="decide"/>
    <sequenceFlow id="f2" sourceRef="decide" targetRef="e"/>
  </process>
</definitions>`

// TestServerExecutesCentralDecision is the end-to-end proof of the server wiring: a
// deployed central business rule task, when a token reaches it, is evaluated by the
// configured remote temis connector (a fake here) and the instance runs to
// completion instead of parking — the temis connector worker is driven on the run
// loop exactly like the local DMN worker.
func TestServerExecutesCentralDecision(t *testing.T) {
	var calls int32
	var gotDecision string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		b, _ := io.ReadAll(r.Body)
		var req struct {
			DecisionId string `json:"decisionId"`
		}
		_ = json.Unmarshal(b, &req)
		gotDecision = req.DecisionId
		_, _ = w.Write([]byte(`{"outputs":{"Dish":"Roastbeef"}}`))
	}))
	defer ts.Close()

	// Configure the "risk" connector to point at the fake temis service. The env is
	// read when the server is constructed, so it must be set first.
	t.Setenv("ATLAS_TEMIS_CONNECTORS", "risk")
	t.Setenv("ATLAS_TEMIS_RISK_URL", ts.URL)

	srv, _ := newValidateServer(t)
	x := deployTestHarness{t, srv.Handler()}

	pid := x.mkProject("Central")
	x.saveDraft(pid, centralDecisionBPMN) // no DMN reference needed — evaluated remotely

	code, b := x.do(http.MethodPost, "/api/v1/projects/"+pid+"/deploy", "")
	if code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, b)
	}
	var rep projectDeployResp
	if err := json.Unmarshal(b, &rep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if !rep.Deployed || len(rep.Definitions) != 1 {
		t.Fatalf("deploy = %+v, want one definition deployed", rep)
	}
	key := rep.Definitions[0].Key

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
		t.Fatalf("stats = %+v, want 0/0 (central decision evaluated, instance completed)", ci.Stats)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("temis service calls = %d, want 1", calls)
	}
	if gotDecision != "Dish" {
		t.Errorf("temis got decisionId %q, want Dish", gotDecision)
	}
	if _, list := x.do(http.MethodGet, "/api/v1/instances", ""); !strings.Contains(string(list), `"state":"completed"`) {
		t.Fatalf("instance did not complete: %s", list)
	}
}
