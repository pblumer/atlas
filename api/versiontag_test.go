package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// A process's atlas:versionTag surfaces on the process list and the instance
// timeline so Operations can show it beside the deploy version.
const versionTagBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:atlas="http://atlas/schema/1.0">
  <process id="tagged" isExecutable="true" name="Tagged" atlas:versionTag="1.4.0">
    <startEvent id="s"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="e"/>
  </process>
</definitions>`

func TestVersionTagSurfaced(t *testing.T) {
	ts := newTestServer(t)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", versionTagBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}

	// Process list carries the tag.
	code, body := doReq(t, ts, http.MethodGet, "/api/v1/processes", "", "")
	if code != http.StatusOK {
		t.Fatalf("list: status=%d body=%s", code, body)
	}
	var procs []struct {
		Key        uint64 `json:"key"`
		VersionTag string `json:"versionTag"`
	}
	if err := json.Unmarshal(body, &procs); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if len(procs) != 1 || procs[0].VersionTag != "1.4.0" {
		t.Fatalf("procs=%+v, want one with versionTag 1.4.0", procs)
	}

	// Start an instance and confirm the timeline reports the tag too.
	url := fmt.Sprintf("/api/v1/processes/%d/instances", procs[0].Key)
	if code, body := doReq(t, ts, http.MethodPost, url, "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("start: status=%d body=%s", code, body)
	}
	var insts []struct {
		Key uint64 `json:"key"`
	}
	_, ibody := doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if err := json.Unmarshal(ibody, &insts); err != nil || len(insts) == 0 {
		t.Fatalf("instances: %v (%s)", err, ibody)
	}
	_, tbody := doReq(t, ts, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", insts[0].Key), "", "")
	var tl struct {
		VersionTag string `json:"versionTag"`
	}
	if err := json.Unmarshal(tbody, &tl); err != nil {
		t.Fatalf("timeline decode: %v (%s)", err, tbody)
	}
	if tl.VersionTag != "1.4.0" {
		t.Errorf("timeline versionTag = %q, want \"1.4.0\"", tl.VersionTag)
	}
}
