package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// discordTaskIn is one Discord service task with the atlas prefix bound to whichever
// namespace the caller names — the whole model, so the only difference between the
// two deploys below is the namespace.
func discordTaskIn(ns string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="` + ns + `"
                  id="defs_ns" targetNamespace="http://atlas/examples">
  <bpmn:process id="proc_namespace_probe" isExecutable="true">
    <bpmn:startEvent id="Start"><bpmn:outgoing>F1</bpmn:outgoing></bpmn:startEvent>
    <bpmn:serviceTask id="Send" name="Melden">
      <bpmn:extensionElements>
        <atlas:discordConnector connector="chat" operation="send-message"
                                channel="=channelId" content="=text"/>
      </bpmn:extensionElements>
      <bpmn:incoming>F1</bpmn:incoming>
      <bpmn:outgoing>F2</bpmn:outgoing>
    </bpmn:serviceTask>
    <bpmn:endEvent id="End"><bpmn:incoming>F2</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="F1" sourceRef="Start" targetRef="Send"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Send" targetRef="End"/>
  </bpmn:process>
</bpmn:definitions>`
}

// TestDeployWarnsAboutAForeignAtlasNamespace is the end of the trail the Discord
// worker exposed: a model whose atlas prefix was bound to http://atlas.dev/schema/1.0
// deployed, compiled and ran for hours, and only failed when its author pressed Save
// in the Modeler ("no namespace uri given for prefix <ns0>"). The deploy is where
// somebody is looking, so the deploy says it — and still deploys, because it runs.
func TestDeployWarnsAboutAForeignAtlasNamespace(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments",
		discordTaskIn("http://atlas.dev/schema/1.0"), "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var resp struct {
		Key      uint64   `json:"key"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if resp.Key == 0 {
		t.Fatal("the deploy was refused; a stray namespace must never block one — the process runs")
	}
	joined := strings.Join(resp.Warnings, "\n")
	if !strings.Contains(joined, "discordConnector") {
		t.Errorf("warnings do not name the element: %v", resp.Warnings)
	}
	if !strings.Contains(joined, "http://atlas.dev/schema/1.0") {
		t.Errorf("warnings do not name the namespace found: %v", resp.Warnings)
	}
	if !strings.Contains(joined, "xmlns:atlas") {
		t.Errorf("warnings do not say what to edit: %v", resp.Warnings)
	}
}

// The same model in Atlas' own namespace — the one the Modeler writes — must deploy
// silently, or every model authored in the Modeler would carry a warning.
func TestDeployIsSilentAboutTheCanonicalAtlasNamespace(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments",
		discordTaskIn("http://atlas/schema/1.0"), "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var resp struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	for _, w := range resp.Warnings {
		if strings.Contains(w, "namespace") {
			t.Errorf("the canonical namespace produced a namespace warning: %q", w)
		}
	}
}
