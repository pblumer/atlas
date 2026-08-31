package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api"
)

// playgroundBPMN is a model with one human task, so a session has something that
// waits for the person driving it.
const playgroundBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             id="defs_pg" targetNamespace="http://atlas/test">
  <process id="approval" name="Approval" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="approve"/>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="approve"/>
    <sequenceFlow id="f2" sourceRef="approve" targetRef="end"/>
  </process>
</definitions>`

// The Playground's model source is the one place it touches design-time state, so
// it is exercised through the real server rather than a stand-in: a draft, a
// deployed definition, and every way of naming neither.
func TestPlaygroundOpensOnADraftAndOnADeployment(t *testing.T) {
	ts := newTestServer(t)

	// A draft, by the process id the server derives from the model.
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/drafts", playgroundBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("save draft = %d, body %s", code, body)
	}
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/playground/sessions",
		`{"source":"draft","ref":"approval"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("open on draft = %d, body %s", code, body)
	}
	var sess struct {
		ID        string `json:"id"`
		ProcessID string `json:"processId"`
		Seed      int64  `json:"seed"`
	}
	if err := json.Unmarshal(body, &sess); err != nil {
		t.Fatalf("decode session: %v (%s)", err, body)
	}
	if sess.ProcessID != "approval" || sess.ID == "" {
		t.Fatalf("session = %+v, want the draft's process", sess)
	}
	if sess.Seed == 0 {
		t.Error("a session opened without a seed should report the one it got, so the run can be repeated")
	}

	// A deployed definition is the other source: "why did version 1 behave like that?"
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/deployments", playgroundBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy = %d, body %s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deployment: %v (%s)", err, body)
	}
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/playground/sessions",
		`{"source":"process","ref":"`+itoa(dep.Key)+`"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("open on deployment = %d, body %s", code, body)
	}
	if !strings.Contains(string(body), `"processId":"approval"`) {
		t.Errorf("session on a deployment = %s", body)
	}
}

// What the model source refuses, and with which status. These are the answers a
// Modeler shows the author, so the difference between them matters.
func TestPlaygroundModelSourceRefusals(t *testing.T) {
	ts := newTestServer(t)
	cases := []struct {
		name, body string
		want       int
	}{
		{"no such draft", `{"source":"draft","ref":"nope"}`, http.StatusNotFound},
		{"no such deployment", `{"source":"process","ref":"999"}`, http.StatusNotFound},
		{"deployment key that is not a number", `{"source":"process","ref":"abc"}`, http.StatusBadRequest},
		{"a source nobody serves", `{"source":"telepathy","ref":"x"}`, http.StatusBadRequest},
		{"a draft with no ref", `{"source":"draft"}`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doReq(t, ts, http.MethodPost, "/api/v1/playground/sessions", tc.body, "application/json")
			if code != tc.want {
				t.Errorf("status = %d, want %d (body %s)", code, tc.want, body)
			}
		})
	}
}

// The whole interactive loop over the real routes: open, start a case, answer the
// task the person is meant to answer, read the finished case, close.
func TestPlaygroundDrivesACaseOverHTTP(t *testing.T) {
	ts := newTestServer(t)
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/playground/sessions",
		`{"source":"xml","xml":`+jsonQuote(playgroundBPMN)+`,"seed":4711,"startTime":"2026-03-05T08:00:00Z"}`,
		"application/json")
	if code != http.StatusOK {
		t.Fatalf("open = %d, body %s", code, body)
	}
	var sess struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &sess); err != nil {
		t.Fatalf("decode session: %v (%s)", err, body)
	}
	base := "/api/v1/playground/sessions/" + sess.ID

	code, body = doReq(t, ts, http.MethodPost, base+"/cases", `{"variables":{"amount":12400}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("start case = %d, body %s", code, body)
	}
	var started struct {
		InstanceKey string `json:"instanceKey"`
		State       string `json:"state"`
	}
	if err := json.Unmarshal(body, &started); err != nil {
		t.Fatalf("decode case: %v (%s)", err, body)
	}
	if started.State != "active" {
		t.Errorf("state = %q, want active — the user task should park", started.State)
	}

	code, body = doReq(t, ts, http.MethodGet, base+"/tasks", "", "")
	if code != http.StatusOK {
		t.Fatalf("tasks = %d, body %s", code, body)
	}
	var tasks []struct {
		JobKey  string `json:"jobKey"`
		Element string `json:"element"`
		Human   bool   `json:"human"`
	}
	if err := json.Unmarshal(body, &tasks); err != nil {
		t.Fatalf("decode tasks: %v (%s)", err, body)
	}
	if len(tasks) != 1 || tasks[0].Element != "approve" || !tasks[0].Human {
		t.Fatalf("tasks = %+v, want one human task on approve", tasks)
	}

	code, body = doReq(t, ts, http.MethodPost, base+"/tasks/"+tasks[0].JobKey+"/complete",
		`{"variables":{"decision":"yes"}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("complete = %d, body %s", code, body)
	}

	code, body = doReq(t, ts, http.MethodGet, base+"/cases/"+started.InstanceKey, "", "")
	if code != http.StatusOK {
		t.Fatalf("read case = %d, body %s", code, body)
	}
	if !strings.Contains(string(body), `"state":"completed"`) || !strings.Contains(string(body), `"decision":"yes"`) {
		t.Errorf("finished case = %s", body)
	}

	// The overlay is what the Modeler paints onto the diagram.
	code, body = doReq(t, ts, http.MethodGet, base+"/overlay", "", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"approve":1`) {
		t.Fatalf("overlay = %d, body %s", code, body)
	}

	// Run, step, pause, resume and the clock are all reachable on a finished case;
	// they simply find nothing to do.
	for _, p := range []struct{ path, body string }{
		{"/run", ""}, {"/step", ""}, {"/pause", ""}, {"/resume", ""},
		{"/clock", `{"millis":3600000}`}, {"/messages", `{"name":"nobody-waits"}`},
	} {
		if code, body := doReq(t, ts, http.MethodPost, base+p.path, p.body, "application/json"); code != http.StatusOK {
			t.Errorf("POST %s = %d, body %s", p.path, code, body)
		}
	}

	if code, body := doReq(t, ts, http.MethodDelete, base, "", ""); code != http.StatusOK {
		t.Fatalf("close = %d, body %s", code, body)
	}
	// Closed means gone: the id is no longer an answer.
	if code, _ := doReq(t, ts, http.MethodGet, base, "", ""); code != http.StatusNotFound {
		t.Errorf("status after close = %d, want 404", code)
	}
}

func itoa(v uint64) string {
	b := [20]byte{}
	i := len(b)
	if v == 0 {
		return "0"
	}
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// An abandoned sandbox is reclaimed: it is a live engine and a directory on
// disk, and a closed browser tab never says goodbye. The sweep is what makes the
// session limit a limit rather than a high-water mark.
func TestAnIdlePlaygroundSessionIsReaped(t *testing.T) {
	ts := newTestServerWith(t, api.WithPlaygroundSessions(time.Millisecond, 5*time.Millisecond))

	code, body := doReq(t, ts, http.MethodPost, "/api/v1/playground/sessions",
		`{"source":"xml","xml":`+jsonQuote(playgroundBPMN)+`}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("open = %d, body %s", code, body)
	}
	var sess struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &sess); err != nil {
		t.Fatalf("decode: %v", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if code, _ := doReq(t, ts, http.MethodGet, "/api/v1/playground/sessions/"+sess.ID, "", ""); code == http.StatusNotFound {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Error("a session nobody touched was still there after its TTL")
}
