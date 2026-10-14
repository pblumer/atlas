package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// boundBPMN reads TENANT_ID from its application with no fallback, and LANGUAGE with the
// fallback de-CH, then waits at a user task so the started instance can be read
// (ADR-0453, ADR-0456).
const boundBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:atlas="http://atlas/schema/1.0">
  <process id="ucc-bound" isExecutable="true" name="Sammelanschluss">
    <extensionElements>
      <atlas:constants>
        <atlas:constant name="TENANT_ID" type="string" source="application"/>
        <atlas:constant name="LANGUAGE" type="string" source="application" value="de-CH"/>
      </atlas:constants>
    </extensionElements>
    <startEvent id="s"/>
    <userTask id="u" name="Prüfen"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="u"/>
    <sequenceFlow id="f2" sourceRef="u" targetRef="e"/>
  </process>
</definitions>`

// serverBPMN reads TENANT_ID from the installation with no fallback, and the facts
// server.environment and application.name.
const serverBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:atlas="http://atlas/schema/1.0">
  <process id="facts" isExecutable="true">
    <extensionElements>
      <atlas:constants>
        <atlas:constant name="TENANT_ID" source="server"/>
        <atlas:constant name="environment" source="server"/>
        <atlas:constant name="name" source="application"/>
      </atlas:constants>
    </extensionElements>
    <startEvent id="s"/>
    <userTask id="u"/>
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

type constantsResp struct {
	Constants []map[string]string `json:"constants"`
	Required  []struct {
		Name      string   `json:"name"`
		Processes []string `json:"processes"`
		Fallback  bool     `json:"fallback"`
		State     string   `json:"state"`
	} `json:"required"`
}

func getConstants(t *testing.T, ts *httptest.Server, path string) constantsResp {
	t.Helper()
	code, body := doReq(t, ts, http.MethodGet, path, "", "")
	if code != http.StatusOK {
		t.Fatalf("get %s: status=%d body=%s", path, code, body)
	}
	var out constantsResp
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode constants: %v (%s)", err, body)
	}
	return out
}

func putConstants(t *testing.T, ts *httptest.Server, path, body string) {
	t.Helper()
	if code, resp := doReq(t, ts, http.MethodPut, path, body, "application/json"); code != http.StatusOK {
		t.Fatalf("put %s: status=%d body=%s", path, code, resp)
	}
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

func wantNamespace(t *testing.T, vars map[string]any, name string, want map[string]any) {
	t.Helper()
	if got, _ := vars[name].(map[string]any); !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", name, vars[name], want)
	}
}

// The whole round for an application's constants: a deploy warns about one this
// installation does not hold, the Constants tab says which is missing and which a fallback
// covers, a start is refused until it is set, an instance started afterwards reads it as
// application.TENANT_ID — and the constants survive a restart.
func TestApplicationConstantsReachInstancesAndSurviveARestart(t *testing.T) {
	dir := t.TempDir()
	ts, stop := openServerAt(t, dir)
	appID := newApplication(t, ts, "UCC")
	path := "/api/v1/applications/" + appID + "/constants"

	dep := deployInto(t, ts, appID, boundBPMN)
	if !strings.Contains(strings.Join(dep.Warnings, "\n"), `application.TENANT_ID is not set on this installation`) {
		t.Errorf("deploy warnings = %q, want one naming the missing application.TENANT_ID", dep.Warnings)
	}

	v := getConstants(t, ts, path)
	if len(v.Required) != 2 || v.Required[0].Name != "LANGUAGE" || v.Required[0].State != "fallback" ||
		v.Required[1].Name != "TENANT_ID" || v.Required[1].State != "missing" ||
		len(v.Required[1].Processes) != 1 || v.Required[1].Processes[0] != "ucc-bound" {
		t.Fatalf("required = %+v, want LANGUAGE covered by its fallback and TENANT_ID missing", v.Required)
	}

	if code, _, msg := startVars(t, ts, dep.Key); code != http.StatusConflict || !strings.Contains(msg, `TENANT_ID\" is not set on this installation`) {
		t.Fatalf("start without the constant: status=%d body=%s, want 409 naming TENANT_ID", code, msg)
	}

	putConstants(t, ts, path, `{"constants":[{"name":"TENANT_ID","type":"string","value":"tenant-int"}]}`)
	if v := getConstants(t, ts, path); v.Required[1].State != "set" {
		t.Errorf("TENANT_ID state after the put = %q, want set", v.Required[1].State)
	}

	code, vars, msg := startVars(t, ts, dep.Key)
	if code != http.StatusOK {
		t.Fatalf("start with the constant: status=%d body=%s", code, msg)
	}
	wantNamespace(t, vars, "application", map[string]any{"TENANT_ID": "tenant-int", "LANGUAGE": "de-CH"})
	if _, ok := vars["TENANT_ID"]; ok {
		t.Errorf("TENANT_ID is a root variable; an application constant is read as application.TENANT_ID")
	}

	// Restart over the same data: the constants come back from their sidecar and reach the
	// next instance without anybody setting them again.
	stop()
	ts2, _ := openServerAt(t, dir)
	code, vars, msg = startVars(t, ts2, dep.Key)
	if code != http.StatusOK {
		t.Fatalf("start after restart: status=%d body=%s", code, msg)
	}
	wantNamespace(t, vars, "application", map[string]any{"TENANT_ID": "tenant-int", "LANGUAGE": "de-CH"})
}

// The installation's constants and the facts the namespaces carry
// (ADR-0456): a missing server constant is warned about and refuses a
// start like an application's, a fact not set here is warned about, and naming the node or
// renaming the application reaches the next instance.
func TestServerConstantsAndFactsReachInstances(t *testing.T) {
	ts := newTestServer(t)
	appID := newApplication(t, ts, "UCC")
	dep := deployInto(t, ts, appID, serverBPMN)
	warnings := strings.Join(dep.Warnings, "\n")
	for _, want := range []string{"server.TENANT_ID is not set on this installation", "server.environment is not set on this installation"} {
		if !strings.Contains(warnings, want) {
			t.Errorf("deploy warnings = %q, want one saying %q", dep.Warnings, want)
		}
	}
	if v := getConstants(t, ts, "/api/v1/server/constants"); len(v.Required) != 1 || v.Required[0].Name != "TENANT_ID" || v.Required[0].State != "missing" {
		t.Fatalf("server required = %+v, want TENANT_ID missing and no fact listed", v.Required)
	}
	if code, _, msg := startVars(t, ts, dep.Key); code != http.StatusConflict || !strings.Contains(msg, "server constant") {
		t.Fatalf("start without the server constant: status=%d body=%s, want 409", code, msg)
	}

	putConstants(t, ts, "/api/v1/server/constants", `{"constants":[{"name":"TENANT_ID","value":"contoso-int"}]}`)
	if code, body := doReq(t, ts, http.MethodPut, "/api/v1/node", `{"environment":"INT"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("name the node: status=%d body=%s", code, body)
	}
	code, vars, msg := startVars(t, ts, dep.Key)
	if code != http.StatusOK {
		t.Fatalf("start: status=%d body=%s", code, msg)
	}
	wantNamespace(t, vars, "server", map[string]any{"TENANT_ID": "contoso-int", "environment": "INT"})
	wantNamespace(t, vars, "application", map[string]any{"name": "UCC"})

	if code, body := doReq(t, ts, http.MethodPatch, "/api/v1/applications/"+appID, `{"name":"UCC Sammelanschlüsse"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("rename: status=%d body=%s", code, body)
	}
	if _, vars, _ = startVars(t, ts, dep.Key); vars != nil {
		wantNamespace(t, vars, "application", map[string]any{"name": "UCC Sammelanschlüsse"})
	}
}

// A constant is checked when it is saved: a name not written in capitals, a reserved one,
// a malformed value, a repeated name or an unknown type refuses the whole set, and nothing
// is written — on both pages.
func TestMalformedHeldConstantsAreRefused(t *testing.T) {
	ts := newTestServer(t)
	appID := newApplication(t, ts, "UCC")
	for _, path := range []string{"/api/v1/applications/" + appID + "/constants", "/api/v1/server/constants"} {
		for name, body := range map[string]string{
			"lower case":   `{"constants":[{"name":"tenantId","value":"x"}]}`,
			"reserved":     `{"constants":[{"name":"SERVER","value":"x"}]}`,
			"not a number": `{"constants":[{"name":"MAX_AGENTS","type":"number","value":"many"}]}`,
			"no name":      `{"constants":[{"name":" ","type":"string","value":"x"}]}`,
			"listed twice": `{"constants":[{"name":"A","value":"1"},{"name":"A","value":"2"}]}`,
			"unknown type": `{"constants":[{"name":"A","type":"date","value":"2026-01-01"}]}`,
		} {
			code, resp := doReq(t, ts, http.MethodPut, path, body, "application/json")
			if code != http.StatusBadRequest {
				t.Errorf("%s %s: status=%d body=%s, want 400", path, name, code, resp)
			}
		}
		if v := getConstants(t, ts, path); len(v.Constants) != 0 {
			t.Errorf("%s after refused puts = %+v, want none", path, v.Constants)
		}
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
