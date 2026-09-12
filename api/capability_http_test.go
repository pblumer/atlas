package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// The business architecture end to end, against a real server: the registry, the
// landscape it is compared against, and the two reads that make the registry worth
// keeping (ADR-0305).
//
// The half these tests exist for is the landscape collector. Everything else in the
// area is unit-tested against a landscape written by hand; this is the only place that
// checks the one Atlas actually builds — that a realization's portable application key
// resolves to a real deployment, that a call activity between two capabilities' own
// processes is seen, and that a process nobody claims is reported.

func capabilityBPMN(processID string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="` + processID + `" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="task">
      <extensionElements><zeebe:taskDefinition type="payment" retries="5"/></extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="task"/>
    <sequenceFlow id="f2" sourceRef="task" targetRef="end"/>
  </process>
</definitions>`
}

// callerBPMN is a process whose call activity reaches another process — the input to
// the one gap finding that compares two things Atlas already holds.
func capabilityCallerBPMN(processID, calls string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="` + processID + `" isExecutable="true">
    <startEvent id="start"/>
    <callActivity id="call-it">
      <extensionElements><zeebe:calledElement processId="` + calls + `"/></extensionElements>
    </callActivity>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="call-it"/>
    <sequenceFlow id="f2" sourceRef="call-it" targetRef="end"/>
  </process>
</definitions>`
}

func TestBusinessArchitectureEndToEnd(t *testing.T) {
	ts := newTestServer(t)

	// An application, and the portable key a realization will name.
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"Consumer Loans"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create application: %d %s", code, body)
	}
	var app struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode application: %v", err)
	}

	for _, pid := range []string{"identity-verification", "onboarding-support"} {
		if code, b := doReq(t, ts, http.MethodPost,
			"/api/v1/deployments?projectId="+app.ID, capabilityBPMN(pid), "application/xml"); code != http.StatusOK {
			t.Fatalf("deploy %s: %d %s", pid, code, b)
		}
	}

	// The application key is derived on demand, so read it back after the deploy
	// rather than assuming the create returned one.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/business-architecture/gaps", "", "")
	if code != http.StatusOK {
		t.Fatalf("gaps: %d %s", code, body)
	}
	var firstReport struct {
		Counts   map[string]int `json:"counts"`
		Findings []struct {
			Kind           string `json:"kind"`
			ApplicationKey string `json:"applicationKey"`
			ProcessID      string `json:"processId"`
		} `json:"findings"`
		Checked struct {
			Processes int `json:"processes"`
		} `json:"checked"`
	}
	if err := json.Unmarshal(body, &firstReport); err != nil {
		t.Fatalf("decode gaps: %v", err)
	}
	// With an empty map, every deployed process is unclaimed. That is the honest
	// starting state and it is what makes the report usable as a worklist.
	if firstReport.Counts["process.unclaimed"] != 2 {
		t.Fatalf("counts = %v, want both deployed processes unclaimed", firstReport.Counts)
	}
	if firstReport.Checked.Processes != 2 {
		t.Errorf("checked.processes = %d", firstReport.Checked.Processes)
	}
	var appKey string
	for _, f := range firstReport.Findings {
		if f.ProcessID == "identity-verification" {
			appKey = f.ApplicationKey
		}
	}
	if appKey == "" {
		t.Fatalf("the report does not name the application key a realization would use: %s", body)
	}

	// Two capabilities: one realized by the deployed process, one still done by hand.
	identity := map[string]any{
		"key": "identity-verification", "name": "Identity Verification", "state": "active",
		"scope": "Establishing that an applicant is who they say. Not the credit decision.",
		"owner": map[string]any{"name": "Head of Customer Operations"},
		"slas": []map[string]any{{"name": "Decision", "metric": "cycleTime",
			"threshold": "10 min", "scope": "internal"}},
		"realizations": []map[string]any{
			{"kind": "process", "applicationKey": appKey, "processId": "identity-verification"},
		},
	}
	underwriting := map[string]any{
		"key": "loan-underwriting", "name": "Loan Underwriting", "state": "active",
		"owner":    map[string]any{"name": "Head of Credit Risk"},
		"requires": []string{"identity-verification"},
		"tags":     []string{"area:lending"},
	}
	for _, c := range []map[string]any{identity, underwriting} {
		payload, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if code, b := doReq(t, ts, http.MethodPost, "/api/v1/capabilities", string(payload), "application/json"); code != http.StatusCreated {
			t.Fatalf("create capability: %d %s", code, b)
		}
	}

	stream, err := json.Marshal(map[string]any{
		"key": "consumer-loan", "name": "Consumer Loan",
		"owner": map[string]any{"name": "SVP Consumer Loans", "role": "SVP"},
		"stages": []map[string]any{
			{"key": "apply", "name": "Application submission", "capabilities": []string{"identity-verification"}},
			{"key": "underwrite", "name": "Credit evaluation", "capabilities": []string{"loan-underwriting"}},
			{"key": "disburse", "name": "Disbursement"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/value-streams", string(stream), "application/json"); code != http.StatusCreated {
		t.Fatalf("create value stream: %d %s", code, b)
	}

	// Coverage resolves the realization against the real deployment registry.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/capabilities/identity-verification/coverage", "", "")
	if code != http.StatusOK {
		t.Fatalf("coverage: %d %s", code, body)
	}
	var cov struct {
		Realized     bool `json:"realized"`
		Realizations []struct {
			Resolved        bool   `json:"resolved"`
			Name            string `json:"name"`
			ApplicationName string `json:"applicationName"`
			Version         int32  `json:"version"`
		} `json:"realizations"`
		RequiredBy []struct {
			Key string `json:"key"`
		} `json:"requiredBy"`
		ValueStreams []struct {
			Key    string `json:"key"`
			Stages []struct {
				Key string `json:"key"`
			} `json:"stages"`
		} `json:"valueStreams"`
		Measurement string `json:"measurement"`
	}
	if err := json.Unmarshal(body, &cov); err != nil {
		t.Fatalf("decode coverage: %v", err)
	}
	if !cov.Realized || len(cov.Realizations) != 1 || !cov.Realizations[0].Resolved {
		t.Fatalf("coverage did not resolve the realization: %s", body)
	}
	if cov.Realizations[0].Version != 1 || cov.Realizations[0].ApplicationName != "Consumer Loans" {
		t.Errorf("realization = %+v, want the live facts resolved from the registry", cov.Realizations[0])
	}
	if len(cov.RequiredBy) != 1 || cov.RequiredBy[0].Key != "loan-underwriting" {
		t.Errorf("requiredBy = %+v", cov.RequiredBy)
	}
	if len(cov.ValueStreams) != 1 || len(cov.ValueStreams[0].Stages) != 1 {
		t.Errorf("valueStreams = %+v", cov.ValueStreams)
	}
	if cov.Measurement == "" {
		t.Error("coverage carries an SLA threshold but does not say that nothing here is measured")
	}

	// The gap report, now that there is a map to compare.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/business-architecture/gaps", "", "")
	if code != http.StatusOK {
		t.Fatalf("gaps: %d %s", code, body)
	}
	var rep struct {
		Counts     map[string]int `json:"counts"`
		Restricted int            `json:"restricted"`
		Findings   []struct {
			Kind          string `json:"kind"`
			CapabilityKey string `json:"capabilityKey"`
			ProcessID     string `json:"processId"`
			StageKey      string `json:"stageKey"`
			Detail        string `json:"detail"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(body, &rep); err != nil {
		t.Fatalf("decode gaps: %v", err)
	}
	want := map[string]int{
		"capability.unrealized": 1, // underwriting is still done by hand
		"process.unclaimed":     1, // onboarding-support belongs to no capability
		"stage.empty":           1, // disbursement has no capability
		"realization.missing":   0,
		"requires.unknown":      0,
		"stage.unknown":         0,
		"call.undeclared":       0,
	}
	for kind, n := range want {
		if rep.Counts[kind] != n {
			t.Errorf("counts[%s] = %d, want %d (whole report: %s)", kind, rep.Counts[kind], n, body)
		}
	}
	if rep.Restricted != 0 {
		t.Errorf("restricted = %d with auth off", rep.Restricted)
	}
	for _, f := range rep.Findings {
		if f.Detail == "" {
			t.Errorf("finding %+v has no detail", f)
		}
	}

	// The adoption backlog is one query.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/capabilities?realized=false", "", "")
	if code != http.StatusOK {
		t.Fatalf("list: %d %s", code, body)
	}
	var backlog []struct {
		Key      string `json:"key"`
		Realized bool   `json:"realized"`
	}
	if err := json.Unmarshal(body, &backlog); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(backlog) != 1 || backlog[0].Key != "loan-underwriting" || backlog[0].Realized {
		t.Errorf("backlog = %+v, want only the capability nothing realizes", backlog)
	}
}

// TestGapsSeesAnUndeclaredCallAcrossCapabilities drives the finding that compares the
// declared dependency graph against the call activities Atlas resolves — through the
// real deployment registry and the real call resolution, which is the half a
// hand-written landscape cannot check.
func TestGapsSeesAnUndeclaredCallAcrossCapabilities(t *testing.T) {
	ts := newTestServer(t)

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications", `{"name":"CRM"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create application: %d %s", code, body)
	}
	var app struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatal(err)
	}
	if code, b := doReq(t, ts, http.MethodPost,
		"/api/v1/deployments?projectId="+app.ID, capabilityBPMN("identity"), "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy callee: %d %s", code, b)
	}
	if code, b := doReq(t, ts, http.MethodPost,
		"/api/v1/deployments?projectId="+app.ID, capabilityCallerBPMN("onboarding", "identity"), "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy caller: %d %s", code, b)
	}

	_, body = doReq(t, ts, http.MethodGet, "/api/v1/business-architecture/gaps", "", "")
	var probe struct {
		Findings []struct {
			ApplicationKey string `json:"applicationKey"`
			ProcessID      string `json:"processId"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		t.Fatal(err)
	}
	var appKey string
	for _, f := range probe.Findings {
		if f.ProcessID == "identity" {
			appKey = f.ApplicationKey
		}
	}
	if appKey == "" {
		t.Fatalf("no application key in %s", body)
	}

	for _, c := range []map[string]any{
		{"key": "onboarding", "name": "Onboarding", "state": "active", "realizations": []map[string]any{
			{"kind": "process", "applicationKey": appKey, "processId": "onboarding"}}},
		{"key": "identity", "name": "Identity", "state": "active", "realizations": []map[string]any{
			{"kind": "process", "applicationKey": appKey, "processId": "identity"}}},
	} {
		payload, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if code, b := doReq(t, ts, http.MethodPost, "/api/v1/capabilities", string(payload), "application/json"); code != http.StatusCreated {
			t.Fatalf("create capability: %d %s", code, b)
		}
	}

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/business-architecture/gaps", "", "")
	if code != http.StatusOK {
		t.Fatalf("gaps: %d %s", code, body)
	}
	var rep struct {
		Counts   map[string]int `json:"counts"`
		Findings []struct {
			Kind          string `json:"kind"`
			CapabilityKey string `json:"capabilityKey"`
			RequiredKey   string `json:"requiredKey"`
			ElementID     string `json:"elementId"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(body, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Counts["call.undeclared"] != 1 {
		t.Fatalf("counts = %v, want the undeclared call reported once (%s)", rep.Counts, body)
	}
	for _, f := range rep.Findings {
		if f.Kind != "call.undeclared" {
			continue
		}
		if f.CapabilityKey != "onboarding" || f.RequiredKey != "identity" || f.ElementID != "call-it" {
			t.Errorf("finding = %+v, want caller, callee and the element that makes the call", f)
		}
	}

	// Declaring the dependency clears it, and nothing else changes.
	declared, err := json.Marshal(map[string]any{
		"key": "onboarding", "name": "Onboarding", "state": "active",
		"requires": []string{"identity"},
		"realizations": []map[string]any{
			{"kind": "process", "applicationKey": appKey, "processId": "onboarding"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if code, b := doReq(t, ts, http.MethodPut, "/api/v1/capabilities/onboarding", string(declared), "application/json"); code != http.StatusOK {
		t.Fatalf("declare the dependency: %d %s", code, b)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/business-architecture/gaps", "", "")
	if code != http.StatusOK {
		t.Fatalf("gaps: %d %s", code, body)
	}
	if err := json.Unmarshal(body, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Counts["call.undeclared"] != 0 {
		t.Errorf("declaring the dependency did not clear the finding: %s", body)
	}
	if len(rep.Findings) != 0 {
		t.Errorf("a complete map still reports: %s", body)
	}
}

func TestBusinessArchitectureSubsetIsServed(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/business-architecture/subset", "", "")
	if code != http.StatusOK {
		t.Fatalf("subset: %d %s", code, body)
	}
	if !strings.Contains(string(body), "keyPattern") || !strings.Contains(string(body), "hierarchy") {
		t.Errorf("subset = %s", body)
	}
}

// A Worker realization resolves against the worker store, which is the other half of
// the landscape.
func TestCoverageResolvesAWorkerRealizationAgainstTheServer(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/connectors",
		`{"name":"mail-service-desk","kind":"mail","provider":"preview","sender":"desk@example.test"}`,
		"application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create worker: %d %s", code, body)
	}
	payload, err := json.Marshal(map[string]any{
		"key": "notify", "name": "Notify the customer", "state": "active",
		"realizations": []map[string]any{{"kind": "worker", "workerRef": "mail-service-desk"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/capabilities", string(payload), "application/json"); code != http.StatusCreated {
		t.Fatalf("create capability: %d %s", code, b)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/capabilities/notify/coverage", "", "")
	if code != http.StatusOK {
		t.Fatalf("coverage: %d %s", code, body)
	}
	var cov struct {
		Realizations []struct {
			Resolved   bool   `json:"resolved"`
			WorkerType string `json:"workerType"`
		} `json:"realizations"`
	}
	if err := json.Unmarshal(body, &cov); err != nil {
		t.Fatal(err)
	}
	if len(cov.Realizations) != 1 || !cov.Realizations[0].Resolved || cov.Realizations[0].WorkerType != "mail" {
		t.Errorf("realizations = %+v", cov.Realizations)
	}
}

// TestGapsReportsWhatTheCallerMayNotSee is the sharing-scope half of the landscape
// collector. A process outside the caller's scope must not become a "missing
// realization" and must not become an "unclaimed process" either: both would be Atlas
// reporting the caller's own access as a defect in somebody's architecture. It travels
// as a placeholder instead, and the report says how many it met.
func TestGapsReportsWhatTheCallerMayNotSee(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	if login(t, admin, ts, "admin", "password1") != http.StatusOK {
		t.Fatal("admin login")
	}
	createUserWithRoles(t, admin, ts.URL, "outsider", `["modeler"]`)
	outsider := signInAs(t, ts.URL, "outsider", "a-password-that-is-long")

	code, body := cReq(t, admin, ts, "POST", "/api/v1/projects", `{"name":"Private application"}`)
	if code != http.StatusOK {
		t.Fatalf("create project: %d %s", code, body)
	}
	appID := decodeProject(t, body).ID
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/deployments?projectId="+appID,
		deployableBPMN("private-process")); code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, b)
	}

	// The admin sees the deployed process, so with an empty map it is unclaimed.
	code, body = cReq(t, admin, ts, "GET", "/api/v1/business-architecture/gaps", "")
	if code != http.StatusOK {
		t.Fatalf("gaps as admin: %d %s", code, body)
	}
	var asAdmin struct {
		Counts     map[string]int `json:"counts"`
		Restricted int            `json:"restricted"`
	}
	if err := json.Unmarshal(body, &asAdmin); err != nil {
		t.Fatal(err)
	}
	if asAdmin.Counts["process.unclaimed"] != 1 || asAdmin.Restricted != 0 {
		t.Fatalf("as admin: counts %v restricted %d", asAdmin.Counts, asAdmin.Restricted)
	}

	// The outsider sees neither, and is told so rather than shown a clean report.
	code, body = cReq(t, outsider, ts, "GET", "/api/v1/business-architecture/gaps", "")
	if code != http.StatusOK {
		t.Fatalf("gaps as outsider: %d %s", code, body)
	}
	var asOutsider struct {
		Counts     map[string]int `json:"counts"`
		Restricted int            `json:"restricted"`
	}
	if err := json.Unmarshal(body, &asOutsider); err != nil {
		t.Fatal(err)
	}
	if asOutsider.Counts["process.unclaimed"] != 0 {
		t.Errorf("a process the caller cannot see was reported as unclaimed: %s", body)
	}
	if asOutsider.Restricted != 1 {
		t.Errorf("restricted = %d, want 1: a report that hides what it could not see is worse than none",
			asOutsider.Restricted)
	}

	// And a realization pointing at it reads as restricted, not as missing.
	appKey := ""
	code, body = cReq(t, admin, ts, "GET", "/api/v1/business-architecture/gaps", "")
	if code != http.StatusOK {
		t.Fatal(body)
	}
	var probe struct {
		Findings []struct {
			ApplicationKey string `json:"applicationKey"`
			ProcessID      string `json:"processId"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		t.Fatal(err)
	}
	for _, f := range probe.Findings {
		if f.ProcessID == "private-process" {
			appKey = f.ApplicationKey
		}
	}
	if appKey == "" {
		t.Fatalf("no application key in %s", body)
	}
	payload, err := json.Marshal(map[string]any{
		"key": "private-thing", "name": "Private thing", "state": "active",
		"realizations": []map[string]any{
			{"kind": "process", "applicationKey": appKey, "processId": "private-process"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/capabilities", string(payload)); code != http.StatusCreated {
		t.Fatalf("create capability: %d %s", code, b)
	}

	code, body = cReq(t, outsider, ts, "GET", "/api/v1/capabilities/private-thing/coverage", "")
	if code != http.StatusOK {
		t.Fatalf("coverage as outsider: %d %s", code, body)
	}
	var cov struct {
		Realized     bool `json:"realized"`
		Realizations []struct {
			Resolved   bool   `json:"resolved"`
			Restricted bool   `json:"restricted"`
			Name       string `json:"name"`
		} `json:"realizations"`
	}
	if err := json.Unmarshal(body, &cov); err != nil {
		t.Fatal(err)
	}
	if len(cov.Realizations) != 1 || !cov.Realizations[0].Restricted || cov.Realizations[0].Resolved {
		t.Fatalf("realizations = %+v, want restricted and not resolved", cov.Realizations)
	}
	if cov.Realizations[0].Name != "" {
		t.Errorf("a restricted realization disclosed the process name %q", cov.Realizations[0].Name)
	}

	code, body = cReq(t, outsider, ts, "GET", "/api/v1/business-architecture/gaps", "")
	if code != http.StatusOK {
		t.Fatal(body)
	}
	var after struct {
		Counts map[string]int `json:"counts"`
	}
	if err := json.Unmarshal(body, &after); err != nil {
		t.Fatal(err)
	}
	if after.Counts["realization.missing"] != 0 {
		t.Errorf("a realization the caller cannot see was reported as missing: %s", body)
	}
}

// TestGapsFollowsACallOverride is the claim the landscape collector makes in a comment
// and nothing checked: a call activity's dependency is the one the engine would
// actually take, redirects and pins included. An edge that read the model's own
// `calledElement` would report a dependency this server never takes, which is a finding
// about a process that is not running.
func TestGapsFollowsACallOverride(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	if login(t, admin, ts, "admin", "password1") != http.StatusOK {
		t.Fatal("admin login")
	}
	code, body := cReq(t, admin, ts, "POST", "/api/v1/projects", `{"name":"CRM"}`)
	if code != http.StatusOK {
		t.Fatalf("create application: %d %s", code, body)
	}
	appID := decodeProject(t, body).ID

	for _, xml := range []string{
		capabilityBPMN("identity"),
		capabilityBPMN("identity-v2"),
		capabilityCallerBPMN("onboarding", "identity"),
	} {
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/deployments?projectId="+appID, xml); code != http.StatusOK {
			t.Fatalf("deploy: %d %s", code, b)
		}
	}

	code, body = cReq(t, admin, ts, "GET", "/api/v1/business-architecture/gaps", "")
	if code != http.StatusOK {
		t.Fatalf("gaps: %d %s", code, body)
	}
	var probe struct {
		Findings []struct {
			ApplicationKey string `json:"applicationKey"`
			ProcessID      string `json:"processId"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		t.Fatal(err)
	}
	appKey := ""
	for _, f := range probe.Findings {
		if f.ProcessID == "identity" {
			appKey = f.ApplicationKey
		}
	}
	if appKey == "" {
		t.Fatalf("no application key in %s", body)
	}

	claim := func(key, processID string, requires []string) {
		t.Helper()
		payload, err := json.Marshal(map[string]any{
			"key": key, "name": key, "state": "active",
			"requires": requires,
			"realizations": []map[string]any{
				{"kind": "process", "applicationKey": appKey, "processId": processID}},
		})
		if err != nil {
			t.Fatal(err)
		}
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/capabilities", string(payload)); code != http.StatusCreated {
			t.Fatalf("create capability %s: %d %s", key, code, b)
		}
	}
	// Onboarding declares the dependency its model states, so nothing is reported.
	claim("onboarding", "onboarding", []string{"identity"})
	claim("identity", "identity", nil)
	claim("identity-next", "identity-v2", nil)

	code, body = cReq(t, admin, ts, "GET", "/api/v1/business-architecture/gaps", "")
	if code != http.StatusOK {
		t.Fatalf("gaps: %d %s", code, body)
	}
	var before struct {
		Counts map[string]int `json:"counts"`
	}
	if err := json.Unmarshal(body, &before); err != nil {
		t.Fatal(err)
	}
	if before.Counts["call.undeclared"] != 0 {
		t.Fatalf("fixture: the declared dependency was reported anyway: %s", body)
	}

	// Redirect the call to the other capability's process. The declared dependency is
	// now the wrong one, and the report has to say so.
	if code, b := cReq(t, admin, ts, "PUT", "/api/v1/call-activities/overrides/identity",
		`{"action":"redirect","targetProcessId":"identity-v2"}`); code != http.StatusOK {
		t.Fatalf("set override: %d %s", code, b)
	}
	code, body = cReq(t, admin, ts, "GET", "/api/v1/business-architecture/gaps", "")
	if code != http.StatusOK {
		t.Fatalf("gaps: %d %s", code, body)
	}
	var after struct {
		Counts   map[string]int `json:"counts"`
		Findings []struct {
			Kind          string `json:"kind"`
			CapabilityKey string `json:"capabilityKey"`
			RequiredKey   string `json:"requiredKey"`
			ProcessID     string `json:"processId"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(body, &after); err != nil {
		t.Fatal(err)
	}
	if after.Counts["call.undeclared"] != 1 {
		t.Fatalf("the redirect was not followed: %s", body)
	}
	for _, f := range after.Findings {
		if f.Kind != "call.undeclared" {
			continue
		}
		if f.RequiredKey != "identity-next" || f.ProcessID != "identity-v2" {
			t.Errorf("finding = %+v, want the dependency the engine would actually take", f)
		}
	}
}

// A deployment filed under no application still belongs on the landscape. It carries no
// application key, so no realization can name it — and the report says it is unclaimed
// rather than leaving it out, which would make an ungrouped process invisible to the
// one view meant to find work nobody has mapped.
func TestGapsSeesAnUngroupedDeployment(t *testing.T) {
	ts := newTestServer(t)
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/deployments",
		capabilityBPMN("ungrouped"), "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, b)
	}
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/business-architecture/gaps", "", "")
	if code != http.StatusOK {
		t.Fatalf("gaps: %d %s", code, body)
	}
	var rep struct {
		Counts   map[string]int `json:"counts"`
		Findings []struct {
			Kind           string `json:"kind"`
			ApplicationKey string `json:"applicationKey"`
			ProcessID      string `json:"processId"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(body, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Counts["process.unclaimed"] != 1 {
		t.Fatalf("counts = %v, want the ungrouped process reported (%s)", rep.Counts, body)
	}
	for _, f := range rep.Findings {
		if f.ProcessID != "ungrouped" {
			continue
		}
		if f.ApplicationKey != "" {
			t.Errorf("an ungrouped deployment reported application key %q", f.ApplicationKey)
		}
	}
}

// The confirmation date, end to end against a real server: the field somebody sets, the
// horizon an operator sets, and the finding that connects them.
func TestConfirmationEndToEnd(t *testing.T) {
	ts := newTestServer(t)

	// The horizon is readable by anyone and says whether it is a decision or the default.
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/settings/confirmation", "", "")
	if code != http.StatusOK {
		t.Fatalf("read horizon: %d %s", code, body)
	}
	var horizon struct {
		HorizonMonths int  `json:"horizonMonths"`
		Configured    bool `json:"configured"`
		Default       int  `json:"default"`
	}
	if err := json.Unmarshal(body, &horizon); err != nil {
		t.Fatal(err)
	}
	if horizon.HorizonMonths != 12 || horizon.Configured || horizon.Default != 12 {
		t.Fatalf("horizon = %+v, want the built-in default and no decision recorded", horizon)
	}

	payload, err := json.Marshal(map[string]any{
		"key": "loan-underwriting", "name": "Loan Underwriting", "state": "active",
		"owner": map[string]any{"name": "Head of Credit Risk"},
	})
	if err != nil {
		t.Fatal(err)
	}
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/capabilities", string(payload), "application/json")
	if code != http.StatusCreated {
		t.Fatalf("create: %d %s", code, body)
	}
	var created struct {
		Confirmation struct {
			At int64  `json:"at"`
			By string `json:"by"`
		} `json:"confirmation"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	if created.Confirmation.At == 0 {
		t.Fatalf("a freshly written record is unconfirmed: %s", body)
	}

	// Nothing is stale yet, and the report says against which interval.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/business-architecture/gaps", "", "")
	if code != http.StatusOK {
		t.Fatalf("gaps: %d %s", code, body)
	}
	var rep struct {
		Counts        map[string]int `json:"counts"`
		HorizonMonths int            `json:"horizonMonths"`
	}
	if err := json.Unmarshal(body, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Counts["capability.unconfirmed"] != 0 || rep.HorizonMonths != 12 {
		t.Fatalf("counts %v horizon %d on a map written moments ago", rep.Counts, rep.HorizonMonths)
	}

	// An operator narrows the horizon to something nothing can satisfy. The record was
	// confirmed seconds ago and is now past a negative interval, so the finding appears
	// without the test having to wait a year.
	code, body = doReq(t, ts, http.MethodPut, "/api/v1/settings/confirmation",
		`{"horizonMonths":-1}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("set horizon: %d %s", code, body)
	}
	// A negative horizon switches the check off rather than making everything stale,
	// which is the safer reading of a number nobody can satisfy.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/business-architecture/gaps", "", "")
	if err := json.Unmarshal(body, &rep); err != nil {
		t.Fatal(err)
	}
	if code != http.StatusOK || rep.Counts["capability.unconfirmed"] != 0 {
		t.Fatalf("a negative horizon reported findings: %s", body)
	}

	// Confirming through the API records who was asked, and leaves the revision alone.
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/capabilities/loan-underwriting/confirmation",
		`{"with":"Head of Credit Risk","note":"SLA renegotiated to 3 days"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("confirm: %d %s", code, body)
	}
	var view struct {
		With          string `json:"with"`
		Note          string `json:"note"`
		Stale         bool   `json:"stale"`
		Ever          bool   `json:"ever"`
		SelfConfirmed bool   `json:"selfConfirmed"`
		HorizonMonths int    `json:"horizonMonths"`
	}
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatal(err)
	}
	if view.With != "Head of Credit Risk" || view.Note == "" || !view.Ever || view.Stale {
		t.Errorf("view = %+v", view)
	}

	code, body = doReq(t, ts, http.MethodGet, "/api/v1/capabilities/loan-underwriting/coverage", "", "")
	if code != http.StatusOK {
		t.Fatalf("coverage: %d %s", code, body)
	}
	if !strings.Contains(string(body), "Head of Credit Risk") || !strings.Contains(string(body), "horizonMonths") {
		t.Errorf("coverage does not carry the confirmation: %s", body)
	}
}

// The horizon is an admin's to set and everybody's to read: every report already
// carries the interval it applied, so hiding the number would hide nothing.
func TestTheHorizonIsAdminOnlyToSet(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	if login(t, admin, ts, "admin", "password1") != http.StatusOK {
		t.Fatal("admin login")
	}
	createUserWithRoles(t, admin, ts.URL, "architect", `["modeler"]`)
	architect := signInAs(t, ts.URL, "architect", "a-password-that-is-long")

	if code, body := cReq(t, architect, ts, "GET", "/api/v1/settings/confirmation", ""); code != http.StatusOK {
		t.Errorf("a modeler cannot read the horizon: %d %s", code, body)
	}
	if code, _ := cReq(t, architect, ts, "PUT", "/api/v1/settings/confirmation", `{"horizonMonths":1200}`); code != http.StatusForbidden {
		t.Errorf("a modeler set the horizon: %d", code)
	}
	if code, body := cReq(t, admin, ts, "PUT", "/api/v1/settings/confirmation", `{"horizonMonths":3}`); code != http.StatusOK {
		t.Errorf("an admin could not set the horizon: %d %s", code, body)
	}
	// The bound is a sanity check, not a policy: past a century the number is not an
	// interval anybody meant, and a negative value is the honest way to say "off".
	if code, _ := cReq(t, admin, ts, "PUT", "/api/v1/settings/confirmation", `{"horizonMonths":99999}`); code != http.StatusBadRequest {
		t.Errorf("an absurd horizon was accepted: %d", code)
	}
	if code, _ := cReq(t, admin, ts, "PUT", "/api/v1/settings/confirmation", `{}`); code != http.StatusBadRequest {
		t.Errorf("an empty body was accepted: %d", code)
	}
	if code, _ := cReq(t, admin, ts, "PUT", "/api/v1/settings/confirmation", `{nope`); code != http.StatusBadRequest {
		t.Errorf("a malformed body was accepted: %d", code)
	}
}

// Zero restores the default rather than meaning "no horizon at all". Zero is what a
// form sends when somebody clears the field, and reading that as "never stale" would
// switch the freshness check off by accident — which a negative value says on purpose.
func TestZeroHorizonRestoresTheDefault(t *testing.T) {
	ts, _ := newAuthServer(t, "admin", "password1")
	admin := newClient(t)
	if login(t, admin, ts, "admin", "password1") != http.StatusOK {
		t.Fatal("admin login")
	}
	if code, body := cReq(t, admin, ts, "PUT", "/api/v1/settings/confirmation", `{"horizonMonths":3}`); code != http.StatusOK {
		t.Fatalf("set: %d %s", code, body)
	}
	code, body := cReq(t, admin, ts, "PUT", "/api/v1/settings/confirmation", `{"horizonMonths":0}`)
	if code != http.StatusOK {
		t.Fatalf("clear: %d %s", code, body)
	}
	var view struct {
		HorizonMonths int  `json:"horizonMonths"`
		Configured    bool `json:"configured"`
	}
	if err := json.Unmarshal(body, &view); err != nil {
		t.Fatal(err)
	}
	if view.HorizonMonths != 12 || view.Configured {
		t.Errorf("after clearing = %+v, want the default and no decision recorded", view)
	}
	// And the gap report applies it.
	code, body = cReq(t, admin, ts, "GET", "/api/v1/business-architecture/gaps", "")
	if code != http.StatusOK {
		t.Fatalf("gaps: %d %s", code, body)
	}
	var rep struct {
		HorizonMonths int `json:"horizonMonths"`
	}
	if err := json.Unmarshal(body, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.HorizonMonths != 12 {
		t.Errorf("the report applied %d months after the setting was cleared", rep.HorizonMonths)
	}
}

// Measuring a capability is the one read in this area that walks instances, so it is
// the one that had to be proved affordable before it was built
// (benchmarks/results/measurement-381825f.md). These tests are about the contract that
// measurement produced rather than about the arithmetic, which is unit-tested against
// values in api/capability.

// The window is required, and the refusal says why rather than only that. A caller who
// omits it is asking for a reading over all history, which is the thing the
// measurement ruled out.
func TestMeasurementRefusesAnUnboundedReading(t *testing.T) {
	ts := newTestServer(t)
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/capabilities",
		`{"key":"onboarding","name":"Customer onboarding"}`, "application/json"); code != http.StatusCreated {
		t.Fatalf("create capability: %d", code)
	}
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/capabilities/onboarding/measurement", "", "")
	if code != http.StatusBadRequest {
		t.Fatalf("missing windowDays = %d %s, want 400", code, body)
	}
	if !strings.Contains(string(body), "windowDays") {
		t.Errorf("refusal = %s, want it to name the parameter", body)
	}
	// And a window nobody could mean is refused with the ceiling named, so the caller
	// learns the bound rather than guessing at it.
	code, body = doReq(t, ts, http.MethodGet,
		"/api/v1/capabilities/onboarding/measurement?windowDays=100000", "", "")
	if code != http.StatusBadRequest || !strings.Contains(string(body), "400") {
		t.Errorf("oversized window = %d %s, want 400 naming the ceiling", code, body)
	}
}

// TestMeasurementSaysWhatEachFigureRestsOn is the property the whole response exists
// to protect. Outcome counts are all-time and cycle times are windowed; both are
// integers on a screen, and a client that mixed them would be wrong invisibly.
func TestMeasurementSaysWhatEachFigureRestsOn(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications",
		`{"name":"Onboarding"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create application: %d %s", code, body)
	}
	var app struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode application: %v", err)
	}
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/deployments?projectId="+app.ID,
		capabilityBPMN("identity-verification"), "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, b)
	}
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/capabilities",
		`{"key":"identity","name":"Verify an identity",
		  "realizations":[{"kind":"process","applicationKey":"`+app.Key+`","processId":"identity-verification"}],
		  "slas":[{"name":"Ten minutes","metric":"cycle time","threshold":"within 10 minutes",
		           "thresholdSeconds":600,"scope":"internal"},
		          {"name":"Five business days","metric":"cycle time",
		           "threshold":"within five business days","scope":"internal"}],
		  "kpis":[{"name":"Verify same day","metric":"cycle time"}]}`,
		"application/json"); code != http.StatusCreated {
		t.Fatalf("create capability: %d %s", code, b)
	}

	code, body = doReq(t, ts, http.MethodGet,
		"/api/v1/capabilities/identity/measurement?windowDays=30", "", "")
	if code != http.StatusOK {
		t.Fatalf("measurement: %d %s", code, body)
	}
	var got struct {
		WindowDays   int    `json:"windowDays"`
		CountedBasis string `json:"countedBasis"`
		WalkedBasis  string `json:"walkedBasis"`
		Processes    []struct {
			ProcessID string `json:"processId"`
			Deployed  bool   `json:"deployed"`
		} `json:"processes"`
		SLAs []struct {
			Name             string `json:"name"`
			ThresholdSeconds int64  `json:"thresholdSeconds"`
		} `json:"slas"`
		NotMeasured []struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"notMeasured"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode measurement: %v — %s", err, body)
	}
	if got.WindowDays != 30 {
		t.Errorf("windowDays = %d, want the 30 that was measured", got.WindowDays)
	}
	if got.CountedBasis == "" || got.WalkedBasis == "" || got.CountedBasis == got.WalkedBasis {
		t.Errorf("bases = %q / %q, want two distinct sentences", got.CountedBasis, got.WalkedBasis)
	}
	if len(got.Processes) != 1 || got.Processes[0].ProcessID != "identity-verification" || !got.Processes[0].Deployed {
		t.Errorf("processes = %+v, want the deployed realization", got.Processes)
	}
	// The SLA with a number is measured; the one written as prose and the KPI are both
	// reported as not measured rather than silently absent.
	if len(got.SLAs) != 1 || got.SLAs[0].ThresholdSeconds != 600 {
		t.Errorf("slas = %+v, want only the one carrying thresholdSeconds", got.SLAs)
	}
	kinds := map[string]bool{}
	for _, n := range got.NotMeasured {
		kinds[n.Kind] = true
	}
	if !kinds["sla"] || !kinds["kpi"] {
		t.Errorf("notMeasured = %+v, want both the prose SLA and the KPI named", got.NotMeasured)
	}
}

// A capability realised only by a person or a purchased system has nothing this server
// can measure, and that is a fact about the map rather than a failure of the reading.
func TestMeasurementOfAnUnautomatedCapabilitySaysSo(t *testing.T) {
	ts := newTestServer(t)
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/capabilities",
		`{"key":"underwriting","name":"Underwrite a loan",
		  "realizations":[{"kind":"manual","note":"a clerk with a scoring tool"}]}`,
		"application/json"); code != http.StatusCreated {
		t.Fatalf("create capability: %d %s", code, b)
	}
	code, body := doReq(t, ts, http.MethodGet,
		"/api/v1/capabilities/underwriting/measurement?windowDays=7", "", "")
	if code != http.StatusOK {
		t.Fatalf("measurement: %d %s", code, body)
	}
	var got struct {
		Unrealizable bool       `json:"unrealizable"`
		Processes    []struct{} `json:"processes"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Unrealizable {
		t.Error("unrealizable = false; a manual realization is nothing this server records")
	}
	if len(got.Processes) != 0 {
		t.Errorf("processes = %d, want none: a manual realization is not a process that failed to deploy", len(got.Processes))
	}
}

// measurableBPMN is a process that runs to completion on its own: no task parks it,
// so creating an instance produces a finished case with a cycle time and a visit on
// its end event. The end event is distinctly named because the method asks for that,
// even though the compiler drops the name today.
func measurableBPMN(processID string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="` + processID + `" isExecutable="true">
    <startEvent id="start"/>
    <endEvent id="verified" name="Verified"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="verified"/>
  </process>
</definitions>`
}

// TestMeasurementCountsWhatActuallyRan is the test the contract tests above do not
// replace: it runs real instances through a real engine and checks the numbers, not
// the shape. Everything between the counters and the response — which elements count
// as an ending, how the window is applied, how a cycle time is derived — is only
// exercised by a case that actually completed.
func TestMeasurementCountsWhatActuallyRan(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications",
		`{"name":"Verification"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create application: %d %s", code, body)
	}
	var app struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode application: %v", err)
	}
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/deployments?projectId="+app.ID,
		measurableBPMN("verify"), "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	const cases = 3
	for range cases {
		if code, b := doReq(t, ts, http.MethodPost,
			fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key),
			`{}`, "application/json"); code != http.StatusOK && code != http.StatusCreated {
			t.Fatalf("create instance: %d %s", code, b)
		}
	}
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/capabilities",
		`{"key":"verify","name":"Verify an identity",
		  "realizations":[{"kind":"process","applicationKey":"`+app.Key+`","processId":"verify"}],
		  "slas":[{"name":"Instant","metric":"cycle time","threshold":"within an hour",
		           "thresholdSeconds":3600,"scope":"internal"}]}`,
		"application/json"); code != http.StatusCreated {
		t.Fatalf("create capability: %d %s", code, b)
	}

	code, body = doReq(t, ts, http.MethodGet,
		"/api/v1/capabilities/verify/measurement?windowDays=1", "", "")
	if code != http.StatusOK {
		t.Fatalf("measurement: %d %s", code, body)
	}
	var got struct {
		Processes []struct {
			Outcomes []struct {
				ElementID string `json:"elementId"`
				Count     int64  `json:"count"`
			} `json:"outcomes"`
			Cases struct {
				Cases       int64   `json:"cases"`
				MeanSeconds float64 `json:"meanSeconds"`
			} `json:"cases"`
		} `json:"processes"`
		SLAs []struct {
			Within int64   `json:"within"`
			Cases  int64   `json:"cases"`
			Share  float64 `json:"share"`
		} `json:"slas"`
		Unrealizable bool `json:"unrealizable"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode measurement: %v — %s", err, body)
	}
	if got.Unrealizable {
		t.Fatal("unrealizable = true, but a deployed process ran three cases")
	}
	if len(got.Processes) != 1 {
		t.Fatalf("processes = %d, want the one realization", len(got.Processes))
	}
	p := got.Processes[0]
	// The end event is the only element counted as an outcome: the start event was
	// visited just as often and is not an ending.
	if len(p.Outcomes) != 1 || p.Outcomes[0].ElementID != "verified" {
		t.Fatalf("outcomes = %+v, want only the end event", p.Outcomes)
	}
	if p.Outcomes[0].Count != cases {
		t.Errorf("outcome count = %d, want %d", p.Outcomes[0].Count, cases)
	}
	// And the window held all three, each of which completed instantly.
	if p.Cases.Cases != cases {
		t.Errorf("cases in window = %d, want %d", p.Cases.Cases, cases)
	}
	if p.Cases.MeanSeconds < 0 {
		t.Errorf("mean cycle time = %v, want a non-negative duration", p.Cases.MeanSeconds)
	}
	// All three came in under an hour, so the SLA is fully attained — and the share is
	// reported beside the counts rather than instead of them.
	if len(got.SLAs) != 1 || got.SLAs[0].Within != cases || got.SLAs[0].Cases != cases {
		t.Fatalf("attainment = %+v, want %d/%d", got.SLAs, cases, cases)
	}
	if got.SLAs[0].Share != 1 {
		t.Errorf("share = %v, want 1", got.SLAs[0].Share)
	}
}

// A window that ended before anything ran holds no case. The counters still report
// every ending, because they are all-time — which is the mixing the response's two
// basis sentences exist to keep legible, checked here against real numbers.
func TestMeasurementWindowExcludesWhatItShould(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications",
		`{"name":"Verification"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create application: %d %s", code, body)
	}
	var app struct{ ID, Key string }
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode: %v", err)
	}
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/deployments?projectId="+app.ID,
		measurableBPMN("verify2"), "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	if code, b := doReq(t, ts, http.MethodPost,
		fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key),
		`{}`, "application/json"); code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create instance: %d %s", code, b)
	}
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/capabilities",
		`{"key":"verify2","name":"Verify",
		  "realizations":[{"kind":"process","applicationKey":"`+app.Key+`","processId":"verify2"}]}`,
		"application/json"); code != http.StatusCreated {
		t.Fatalf("create capability: %d %s", code, b)
	}
	code, body = doReq(t, ts, http.MethodGet,
		"/api/v1/capabilities/verify2/measurement?windowDays=400", "", "")
	if code != http.StatusOK {
		t.Fatalf("measurement: %d %s", code, body)
	}
	var got struct {
		Processes []struct {
			Outcomes []struct{ Count int64 } `json:"outcomes"`
			Cases    struct{ Cases int64 }   `json:"cases"`
		} `json:"processes"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Processes[0].Cases.Cases != 1 || got.Processes[0].Outcomes[0].Count != 1 {
		t.Errorf("a 400-day window should hold the one case that just ran: %+v", got.Processes[0])
	}
}

// A cancelled case is reported, and this is the only test that produces one. The
// measurement reports cancellations beside outcomes because a token that left an
// element cancelled did not complete it — and a capability whose cases are being
// abandoned looks healthy in an outcome distribution that only counts endings.
func TestMeasurementCountsACancelledCase(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/applications",
		`{"name":"Onboarding"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create application: %d %s", code, body)
	}
	var app struct{ ID, Key string }
	if err := json.Unmarshal(body, &app); err != nil {
		t.Fatalf("decode application: %v", err)
	}
	// capabilityBPMN parks on a service task, which is what makes it cancellable: a
	// self-completing process leaves nothing to cancel.
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/deployments?projectId="+app.ID,
		capabilityBPMN("parks"), "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	code, body = doReq(t, ts, http.MethodPost,
		fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), `{}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("create instance: %d %s", code, body)
	}
	// The create does not return the instance key, so it is read back from the list —
	// the same way every other cancellation test in this package finds one.
	_, body = doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	var insts []struct {
		Key   uint64 `json:"key"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(body, &insts); err != nil {
		t.Fatalf("decode instances: %v (%s)", err, body)
	}
	if len(insts) != 1 || insts[0].State != "active" {
		t.Fatalf("instances = %+v, want one parked on the service task", insts)
	}
	if code, b := doReq(t, ts, http.MethodDelete,
		fmt.Sprintf("/api/v1/instances/%d", insts[0].Key), "", ""); code != http.StatusOK {
		t.Fatalf("cancel instance: %d %s", code, b)
	}
	if code, b := doReq(t, ts, http.MethodPost, "/api/v1/capabilities",
		`{"key":"parks","name":"Parks",
		  "realizations":[{"kind":"process","applicationKey":"`+app.Key+`","processId":"parks"}]}`,
		"application/json"); code != http.StatusCreated {
		t.Fatalf("create capability: %d %s", code, b)
	}

	code, body = doReq(t, ts, http.MethodGet,
		"/api/v1/capabilities/parks/measurement?windowDays=1", "", "")
	if code != http.StatusOK {
		t.Fatalf("measurement: %d %s", code, body)
	}
	var got struct {
		Processes []struct {
			Cancellations []struct {
				ElementID string `json:"elementId"`
				Count     int64  `json:"count"`
			} `json:"cancellations"`
			Outcomes []struct{} `json:"outcomes"`
		} `json:"processes"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v — %s", err, body)
	}
	p := got.Processes[0]
	if len(p.Cancellations) == 0 {
		t.Fatalf("no cancellation reported, but the parked instance was cancelled: %+v", p)
	}
	var onTask int64
	for _, c := range p.Cancellations {
		if c.ElementID == "task" {
			onTask = c.Count
		}
	}
	if onTask != 1 {
		t.Errorf("cancellations on the parked task = %d, want 1 (got %+v)", onTask, p.Cancellations)
	}
	// And no ending was recorded, which is the point: a cancelled case is invisible to
	// an outcome distribution, so the two are reported side by side.
	if len(p.Outcomes) != 0 {
		t.Errorf("outcomes = %+v, want none: the case never reached an end event", p.Outcomes)
	}
}
