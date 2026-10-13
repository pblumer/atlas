package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// boundBPMN binds TenantId to its application with no fallback, and Language with the
// fallback de-CH, then waits at a user task so the started instance can be read
// (ADR-draft-application-values-per-installation).
const boundBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:atlas="http://atlas/schema/1.0">
  <process id="ucc-bound" isExecutable="true" name="Sammelanschluss">
    <extensionElements>
      <atlas:constants>
        <atlas:constant name="TenantId" type="string" source="application"/>
        <atlas:constant name="Language" type="string" source="application" value="de-CH"/>
      </atlas:constants>
    </extensionElements>
    <startEvent id="s"/>
    <userTask id="u" name="Prüfen"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="u"/>
    <sequenceFlow id="f2" sourceRef="u" targetRef="e"/>
  </process>
</definitions>`

func newApplication(t *testing.T, ts *httptest.Server, name string) string {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"`+name+`"}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create application: status=%d body=%s", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil || app.ID == "" {
		t.Fatalf("decode application: %v (%s)", err, body)
	}
	return app.ID
}

type deployResult struct {
	Key      uint64   `json:"key"`
	Warnings []string `json:"warnings"`
}

func deployInto(t *testing.T, ts *httptest.Server, appID, model string) deployResult {
	t.Helper()
	path := "/api/v1/deployments"
	if appID != "" {
		path += "?projectId=" + appID
	}
	code, body := doReq(t, ts, http.MethodPost, path, model, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var out deployResult
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	return out
}

type valuesResp struct {
	Values   []map[string]string `json:"values"`
	Required []struct {
		Name      string   `json:"name"`
		Processes []string `json:"processes"`
		Fallback  bool     `json:"fallback"`
		State     string   `json:"state"`
	} `json:"required"`
}

func getValues(t *testing.T, ts *httptest.Server, appID string) valuesResp {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/applications/"+appID+"/values", "", "")
	if code != http.StatusOK {
		t.Fatalf("get values: status=%d body=%s", code, body)
	}
	var out valuesResp
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode values: %v (%s)", err, body)
	}
	return out
}

func startVars(t *testing.T, ts *httptest.Server, defKey uint64) (int, map[string]any, string) {
	t.Helper()
	code, body := doReq(t, ts, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", defKey), `{}`, "application/json")
	if code != http.StatusOK {
		return code, nil, string(body)
	}
	var created struct {
		InstanceKey uint64 `json:"instanceKey"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
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
	return http.StatusOK, vars, ""
}

// The whole round: a deploy warns about a value this installation does not hold, the
// Values tab says which is missing and which a fallback covers, a start is refused until
// the value is set, an instance started afterwards carries it — and the values survive a
// restart.
func TestApplicationValuesReachInstancesAndSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	ts, stop := openServerAt(t, dir)
	appID := newApplication(t, ts, "UCC")

	dep := deployInto(t, ts, appID, boundBPMN)
	if !strings.Contains(strings.Join(dep.Warnings, "\n"), `the application value "TenantId" is not set on this installation`) {
		t.Errorf("deploy warnings = %q, want one naming the missing TenantId", dep.Warnings)
	}

	v := getValues(t, ts, appID)
	if len(v.Required) != 2 || v.Required[0].Name != "Language" || v.Required[0].State != "fallback" ||
		v.Required[1].Name != "TenantId" || v.Required[1].State != "missing" ||
		len(v.Required[1].Processes) != 1 || v.Required[1].Processes[0] != "ucc-bound" {
		t.Fatalf("required = %+v, want Language covered by its fallback and TenantId missing", v.Required)
	}

	if code, _, msg := startVars(t, ts, dep.Key); code != http.StatusConflict || !strings.Contains(msg, `TenantId\" is not set on this installation`) {
		t.Fatalf("start without the value: status=%d body=%s, want 409 naming TenantId", code, msg)
	}

	code, body := doReq(t, ts, http.MethodPut, "/api/v1/applications/"+appID+"/values",
		`{"values":[{"name":"TenantId","type":"string","value":"tenant-int"}]}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("put values: status=%d body=%s", code, body)
	}
	if v := getValues(t, ts, appID); v.Required[1].State != "set" {
		t.Errorf("TenantId state after the put = %q, want set", v.Required[1].State)
	}

	code, vars, msg := startVars(t, ts, dep.Key)
	if code != http.StatusOK {
		t.Fatalf("start with the value: status=%d body=%s", code, msg)
	}
	if vars["TenantId"] != "tenant-int" || vars["Language"] != "de-CH" {
		t.Errorf("instance variables = %v, want TenantId tenant-int and the Language fallback de-CH", vars)
	}

	// Restart over the same data: the values come back from their sidecar and reach the
	// next instance without anybody setting them again.
	stop()
	ts2, _ := openServerAt(t, dir)
	code, vars, msg = startVars(t, ts2, dep.Key)
	if code != http.StatusOK || vars["TenantId"] != "tenant-int" {
		t.Fatalf("start after restart: status=%d vars=%v body=%s, want TenantId tenant-int", code, vars, msg)
	}
}

// A value is parsed when it is saved: a malformed one, a nameless one or a repeated name
// refuses the whole set, and nothing is written.
func TestMalformedApplicationValuesAreRefused(t *testing.T) {
	ts := newTestServer(t)
	appID := newApplication(t, ts, "UCC")
	for name, body := range map[string]string{
		"not a number": `{"values":[{"name":"MaxAgents","type":"number","value":"many"}]}`,
		"no name":      `{"values":[{"name":" ","type":"string","value":"x"}]}`,
		"listed twice": `{"values":[{"name":"a","value":"1"},{"name":"a","value":"2"}]}`,
		"unknown type": `{"values":[{"name":"a","type":"date","value":"2026-01-01"}]}`,
	} {
		code, resp := doReq(t, ts, http.MethodPut, "/api/v1/applications/"+appID+"/values", body, "application/json")
		if code != http.StatusBadRequest {
			t.Errorf("%s: status=%d body=%s, want 400", name, code, resp)
		}
	}
	if v := getValues(t, ts, appID); len(v.Values) != 0 {
		t.Errorf("values after refused puts = %+v, want none", v.Values)
	}
}

// A bound constant in a process filed under no application can never get a value: the
// deploy says so, and a start is refused because there is no fallback.
func TestABoundConstantOutsideAnApplication(t *testing.T) {
	ts := newTestServer(t)
	dep := deployInto(t, ts, "", boundBPMN)
	if !strings.Contains(strings.Join(dep.Warnings, "\n"), "deployed outside any application") {
		t.Errorf("deploy warnings = %q, want one saying the process is outside any application", dep.Warnings)
	}
	if code, _, msg := startVars(t, ts, dep.Key); code != http.StatusConflict || !strings.Contains(msg, "not filed under one") {
		t.Fatalf("start: status=%d body=%s, want 409", code, msg)
	}
}
