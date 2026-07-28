package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// A non-executable process (bpmn:isExecutable="false") still deploys and lists —
// so it can be inspected — but is flagged non-executable and cannot be started.
const nonExecutableBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="descriptive" isExecutable="false" name="Descriptive only">
    <startEvent id="s"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

func TestNonExecutableProcessNotStartable(t *testing.T) {
	ts := newTestServer(t)

	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", nonExecutableBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}

	code, body := doReq(t, ts, http.MethodGet, "/api/v1/processes", "", "")
	if code != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", code, body)
	}
	var procs []struct {
		Key        uint64 `json:"key"`
		Executable bool   `json:"executable"`
	}
	if err := json.Unmarshal(body, &procs); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(procs) != 1 {
		t.Fatalf("processes = %d, want 1 (a non-executable process still lists)", len(procs))
	}
	if procs[0].Executable {
		t.Errorf("executable = true, want false for isExecutable=\"false\"")
	}

	url := fmt.Sprintf("/api/v1/processes/%d/instances", procs[0].Key)
	code, body = doReq(t, ts, http.MethodPost, url, "{}", "application/json")
	if code != http.StatusConflict {
		t.Fatalf("start non-executable: status=%d body=%s, want 409", code, body)
	}
	if !strings.Contains(string(body), "not executable") {
		t.Errorf("error body = %s, want mention of \"not executable\"", body)
	}
}

// An ordinary executable process lists executable=true and starts normally — the
// default path must not regress.
func TestExecutableProcessStartable(t *testing.T) {
	ts := newTestServer(t)

	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/processes", "", "")
	if code != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", code, body)
	}
	var procs []struct {
		Key        uint64 `json:"key"`
		Executable bool   `json:"executable"`
	}
	if err := json.Unmarshal(body, &procs); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(procs) != 1 || !procs[0].Executable {
		t.Fatalf("procs=%+v, want one executable process", procs)
	}
	url := fmt.Sprintf("/api/v1/processes/%d/instances", procs[0].Key)
	if code, body := doReq(t, ts, http.MethodPost, url, "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("start executable: status=%d body=%s, want 200", code, body)
	}
}

// Publishing a non-executable process as a public start link is refused.
func TestPublishNonExecutableRejected(t *testing.T) {
	ts := newTestServer(t)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", nonExecutableBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/public-links", `{"processId":"descriptive"}`, "application/json")
	if code != http.StatusConflict {
		t.Fatalf("publish non-executable: status=%d body=%s, want 409", code, body)
	}
	if !strings.Contains(string(body), "not executable") {
		t.Errorf("body=%s, want mention of \"not executable\"", body)
	}
}

// A public link minted while a process was executable stops working once a later
// version of that process id is redeployed non-executable.
func TestPublicFormStartRefusedAfterNonExecutableRedeploy(t *testing.T) {
	ts := newTestServer(t)
	// v1: executable, with a start form → publishable.
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", startFormBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy v1: status=%d body=%s", code, body)
	}
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/public-links", `{"processId":"onboard"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("publish: status=%d body=%s", code, body)
	}
	var link struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &link); err != nil || link.Token == "" {
		t.Fatalf("decode link: %v (%s)", err, body)
	}
	// v2: same process id, now non-executable.
	const v2 = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="onboard" isExecutable="false" name="Onboarding">
    <startEvent id="s"><extensionElements><zeebe:formDefinition formId="onboarding-form"/></extensionElements></startEvent>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", v2, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy v2: status=%d body=%s", code, body)
	}
	code, body = doReq(t, ts, http.MethodPost, "/public/forms/"+link.Token+"/start", "{}", "application/json")
	if code != http.StatusConflict {
		t.Fatalf("public start after non-executable redeploy: status=%d body=%s, want 409", code, body)
	}
}
