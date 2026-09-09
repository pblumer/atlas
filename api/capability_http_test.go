package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The business architecture end to end, against a real server: the registry, the
// landscape it is compared against, and the two reads that make the registry worth
// keeping (ADR-draft-business-capabilities-and-value-streams).
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
