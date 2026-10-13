package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// constantsBPMN declares two process constants and waits at a user task, so the started
// instance stays alive to be read (ADR-0454).
const constantsBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:atlas="http://atlas/schema/1.0">
  <process id="ucc" isExecutable="true" name="Sammelanschluss">
    <extensionElements>
      <atlas:constants>
        <atlas:constant name="DomainSuffix" type="string" value="contoso.ch"/>
        <atlas:constant name="MaxAgents" type="number" value="20"/>
      </atlas:constants>
    </extensionElements>
    <startEvent id="s"/>
    <userTask id="u" name="Prüfen"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="u"/>
    <sequenceFlow id="f2" sourceRef="u" targetRef="e"/>
  </process>
</definitions>`

// The listing names a definition's constants, and an instance started through the API
// carries them — with a start variable naming one dropped, because the constant is
// written first, and the caller's other start variables kept.
func TestConstantsAreListedAndSeededThroughTheAPI(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", constantsBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/processes", "", "")
	if code != http.StatusOK {
		t.Fatalf("list processes: status=%d body=%s", code, body)
	}
	var procs []map[string]json.RawMessage
	if err := json.Unmarshal(body, &procs); err != nil {
		t.Fatalf("decode processes: %v (%s)", err, body)
	}
	const wantConstants = `[{"name":"DomainSuffix","type":"string","value":"contoso.ch"},{"name":"MaxAgents","type":"number","value":"20"}]`
	if len(procs) != 1 || string(procs[0]["constants"]) != wantConstants {
		t.Fatalf("listed constants = %s, want %s", procs[0]["constants"], wantConstants)
	}

	start := `{"variables":{"DomainSuffix":"from-the-caller","Other":"x"}}`
	code, body = doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), start, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}
	var created struct {
		InstanceKey uint64 `json:"instanceKey"`
	}
	if err := json.Unmarshal(body, &created); err != nil || created.InstanceKey == 0 {
		t.Fatalf("decode create: %v (%s)", err, body)
	}

	code, body = doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/variables", created.InstanceKey), "", "")
	if code != http.StatusOK {
		t.Fatalf("get variables: status=%d body=%s", code, body)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var vars map[string]any
	if err := dec.Decode(&vars); err != nil {
		t.Fatalf("decode variables: %v (%s)", err, body)
	}
	if got := vars["DomainSuffix"]; got != "contoso.ch" {
		t.Errorf("DomainSuffix = %v, want the constant contoso.ch", got)
	}
	if got, ok := vars["MaxAgents"].(json.Number); !ok || got.String() != "20" {
		t.Errorf("MaxAgents = %v (%T), want number 20", vars["MaxAgents"], vars["MaxAgents"])
	}
	if got := vars["Other"]; got != "x" {
		t.Errorf("Other = %v, want x", got)
	}
}

// A model that writes one of its constants does not deploy, and the refusal names it.
func TestADeploymentThatWritesAConstantIsRefused(t *testing.T) {
	ts := newTestServer(t)
	model := strings.Replace(constantsBPMN, `<userTask id="u" name="Prüfen"/>`,
		`<scriptTask id="u"><extensionElements xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
        <zeebe:script expression="=&quot;other.ch&quot;" resultVariable="DomainSuffix"/></extensionElements></scriptTask>`, 1)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", model, "application/xml")
	if code == http.StatusOK || !strings.Contains(string(body), `writes \"DomainSuffix\"`) {
		t.Fatalf("deploy: status=%d body=%s, want a refusal naming DomainSuffix", code, body)
	}
}
