package playground

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/playground"
)

// The fixtures are inline and carry no BPMN-DI: nothing here renders a diagram,
// these models exist to be executed by the handler under test.
const userTaskXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" id="d1" targetNamespace="http://atlas/test">
  <process id="approval" name="Approval" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="approve" name="Approve"/>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="approve"/>
    <sequenceFlow id="f2" sourceRef="approve" targetRef="end"/>
  </process>
</definitions>`

const waitXML = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" id="d2" targetNamespace="http://atlas/test">
  <process id="waiting" name="Waiting" isExecutable="true">
    <startEvent id="start"/>
    <intermediateCatchEvent id="wait">
      <timerEventDefinition><timeDuration>PT1H</timeDuration></timerEventDefinition>
    </intermediateCatchEvent>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="wait"/>
    <sequenceFlow id="f2" sourceRef="wait" targetRef="end"/>
  </process>
</definitions>`

// call drives one handler directly with the path values the mux would have
// extracted, the way the per-area services are meant to be testable (ADR-0147).
func call(t *testing.T, h http.HandlerFunc, method, body string, vals map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, "/", strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, "/", nil)
	}
	for k, v := range vals {
		r.SetPathValue(k, v)
	}
	rec := httptest.NewRecorder()
	h(rec, r)
	return rec
}

// newService builds a service whose model source serves one draft and refuses
// everything else, so the authorization seam can be exercised without a server.
func newService(t *testing.T) *Service {
	t.Helper()
	reg := playground.NewRegistry(time.Hour, 4)
	t.Cleanup(reg.CloseAll)
	source := func(_ *http.Request, kind, ref string) ([]byte, int, string) {
		if kind == "draft" && ref == "approval" {
			return []byte(userTaskXML), 0, ""
		}
		return nil, http.StatusForbidden, "not yours"
	}
	return New(reg, source, vars)
}

// vars is the conversion the server injects; a small stand-in that covers the
// kinds these tests use.
func vars(in map[string]any) ([]model.VariableValue, error) {
	out := make([]model.VariableValue, 0, len(in))
	for name, raw := range in {
		vv := model.VariableValue{Name: name, Kind: model.VarString}
		switch x := raw.(type) {
		case string:
			vv.Text = x
		case bool:
			vv.Kind, vv.Bool = model.VarBool, x
		case json.Number:
			vv.Kind, vv.Text = model.VarNumber, x.String()
		}
		out = append(out, vv)
	}
	return out, nil
}

func decodeInto(t *testing.T, rec *httptest.ResponseRecorder, into any) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
}

// openSession opens a session on an inline model and returns it.
func openSession(t *testing.T, svc *Service, xml string) sessionResp {
	t.Helper()
	body := `{"source":"xml","xml":` + jsonString(xml) + `,"seed":4711,"startTime":"2026-03-05T08:00:00Z"}`
	rec := call(t, svc.HandleOpen, http.MethodPost, body, nil)
	var out sessionResp
	decodeInto(t, rec, &out)
	return out
}

// jsonString quotes a model for embedding in a request body.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// The whole interactive loop through the HTTP surface: open a session, start a
// case, find the human task, complete it, and read the finished case.
func TestPlayThroughAHumanTask(t *testing.T) {
	svc := newService(t)
	sess := openSession(t, svc, userTaskXML)
	if sess.ProcessID != "approval" {
		t.Errorf("processId = %q, want %q", sess.ProcessID, "approval")
	}
	if sess.Seed != 4711 {
		t.Errorf("seed = %d, want the one asked for", sess.Seed)
	}

	var started caseResp
	decodeInto(t, call(t, svc.HandleStartCase, http.MethodPost,
		`{"variables":{"applicant":"Vreni"}}`, map[string]string{"id": sess.ID}), &started)
	if started.Variables["applicant"] != "Vreni" {
		t.Errorf("start variables did not reach the case: %+v", started.Variables)
	}

	var tasks []taskResp
	decodeInto(t, call(t, svc.HandleTasks, http.MethodGet, "", map[string]string{"id": sess.ID}), &tasks)
	if len(tasks) != 1 || tasks[0].Element != "approve" || !tasks[0].Human {
		t.Fatalf("tasks = %+v, want one human task on \"approve\"", tasks)
	}

	rec := call(t, svc.HandleCompleteTask, http.MethodPost, `{"variables":{"decision":"yes"}}`,
		map[string]string{"id": sess.ID, "jobKey": tasks[0].JobKey})
	if rec.Code != http.StatusOK {
		t.Fatalf("complete = %d, body %s", rec.Code, rec.Body)
	}

	var done caseResp
	decodeInto(t, call(t, svc.HandleCase, http.MethodGet, "",
		map[string]string{"id": sess.ID, "caseKey": started.InstanceKey}), &done)
	if done.State != "completed" {
		t.Errorf("state = %q, want completed", done.State)
	}
	if done.Variables["decision"] != "yes" {
		t.Errorf("variables = %+v, want the decision the person entered", done.Variables)
	}

	var visits map[string]int64
	decodeInto(t, call(t, svc.HandleOverlay, http.MethodGet, "", map[string]string{"id": sess.ID}), &visits)
	if visits["approve"] != 1 {
		t.Errorf("overlay = %+v, want one visit on \"approve\"", visits)
	}
}

// Stepping and the clock, which is how an author walks a waiting case forward.
func TestStepAndClock(t *testing.T) {
	svc := newService(t)
	sess := openSession(t, svc, waitXML)
	if rec := call(t, svc.HandleStartCase, http.MethodPost, "", map[string]string{"id": sess.ID}); rec.Code != http.StatusOK {
		t.Fatalf("start case = %d, body %s", rec.Code, rec.Body)
	}

	var occ occurrenceResp
	decodeInto(t, call(t, svc.HandleStep, http.MethodPost, "", map[string]string{"id": sess.ID}), &occ)
	if !occ.Happened || occ.Kind != "timer" {
		t.Errorf("occurrence = %+v, want a timer to have fired", occ)
	}
	if occ.SimTime != "2026-03-05T09:00:00Z" {
		t.Errorf("simTime = %q, want the hour jumped", occ.SimTime)
	}

	decodeInto(t, call(t, svc.HandleStep, http.MethodPost, "", map[string]string{"id": sess.ID}), &occ)
	if occ.Happened {
		t.Errorf("occurrence = %+v, want nothing left to do", occ)
	}

	if rec := call(t, svc.HandleAdvanceClock, http.MethodPost, `{"millis":-1}`, map[string]string{"id": sess.ID}); rec.Code != http.StatusBadRequest {
		t.Errorf("advancing by a negative duration = %d, want 400", rec.Code)
	}
	var clock struct {
		SimTime string `json:"simTime"`
	}
	decodeInto(t, call(t, svc.HandleAdvanceClock, http.MethodPost, `{"millis":3600000}`,
		map[string]string{"id": sess.ID}), &clock)
	if clock.SimTime != "2026-03-05T10:00:00Z" {
		t.Errorf("simTime = %q, want another hour", clock.SimTime)
	}
}

// Pause holds a run; resume lets it finish.
func TestPauseAndResumeARun(t *testing.T) {
	svc := newService(t)
	sess := openSession(t, svc, waitXML)
	call(t, svc.HandleStartCase, http.MethodPost, "", map[string]string{"id": sess.ID})

	if rec := call(t, svc.HandlePause, http.MethodPost, "", map[string]string{"id": sess.ID}); rec.Code != http.StatusOK {
		t.Fatalf("pause = %d", rec.Code)
	}
	var prog progressResp
	decodeInto(t, call(t, svc.HandleRun, http.MethodPost, "", map[string]string{"id": sess.ID}), &prog)
	if prog.Occurrences != 0 || prog.Quiescent || !prog.Paused {
		t.Errorf("progress = %+v while paused", prog)
	}

	call(t, svc.HandleResume, http.MethodPost, "", map[string]string{"id": sess.ID})
	decodeInto(t, call(t, svc.HandleRun, http.MethodPost, "", map[string]string{"id": sess.ID}), &prog)
	if !prog.Quiescent || prog.Paused {
		t.Errorf("progress = %+v after resume, want the run to finish", prog)
	}
}

// A message is what carries a waiting case on when the outside world would have
// sent one.
func TestPublishMessageNeedsAName(t *testing.T) {
	svc := newService(t)
	sess := openSession(t, svc, waitXML)
	if rec := call(t, svc.HandlePublishMessage, http.MethodPost, `{"correlationKey":"K"}`,
		map[string]string{"id": sess.ID}); rec.Code != http.StatusBadRequest {
		t.Errorf("nameless message = %d, want 400", rec.Code)
	}
	var out struct {
		Published bool `json:"published"`
	}
	decodeInto(t, call(t, svc.HandlePublishMessage, http.MethodPost, `{"name":"nobody-waits","correlationKey":"K"}`,
		map[string]string{"id": sess.ID}), &out)
	if !out.Published {
		t.Error("a message nobody waits for should still be accepted; it simply correlates nothing")
	}
}

// The model source is where authorization lives, and its refusal is the answer
// the caller gets.
func TestModelSourceDecidesWhoMayOpenADraft(t *testing.T) {
	svc := newService(t)
	var out sessionResp
	decodeInto(t, call(t, svc.HandleOpen, http.MethodPost, `{"source":"draft","ref":"approval"}`, nil), &out)
	if out.ProcessID != "approval" {
		t.Errorf("processId = %q, want the draft's process", out.ProcessID)
	}

	rec := call(t, svc.HandleOpen, http.MethodPost, `{"source":"draft","ref":"someone-elses"}`, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("opening a draft the source refuses = %d, want 403", rec.Code)
	}
}

// What Open refuses, and why.
func TestOpenRefusals(t *testing.T) {
	svc := newService(t)
	cases := []struct {
		name, body string
		want       int
	}{
		{"unknown source", `{"source":"guess"}`, http.StatusBadRequest},
		{"xml without a model", `{"source":"xml"}`, http.StatusBadRequest},
		{"draft without a ref", `{"source":"draft"}`, http.StatusBadRequest},
		{"unparseable start time", `{"source":"xml","xml":"<x/>","startTime":"yesterday"}`, http.StatusBadRequest},
		{"negative stub duration", `{"source":"xml","xml":"<x/>","stubs":{"default":{"minMillis":-5}}}`, http.StatusBadRequest},
		{"impossible failure rate", `{"source":"xml","xml":"<x/>","stubs":{"default":{"failPerMillion":2000000}}}`, http.StatusBadRequest},
		{"model that does not compile", `{"source":"xml","xml":"<definitions/>"}`, http.StatusConflict},
		{"body that is not JSON", `{`, http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rec := call(t, svc.HandleOpen, http.MethodPost, tc.body, nil); rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body)
			}
		})
	}
}

// A session that is gone answers 404 everywhere, rather than a zero value that
// looks like a successful call.
func TestGoneSessionIsNotFound(t *testing.T) {
	svc := newService(t)
	sess := openSession(t, svc, userTaskXML)

	if rec := call(t, svc.HandleClose, http.MethodDelete, "", map[string]string{"id": sess.ID}); rec.Code != http.StatusOK {
		t.Fatalf("close = %d", rec.Code)
	}
	for name, h := range map[string]http.HandlerFunc{
		"status":   svc.HandleStatus,
		"run":      svc.HandleRun,
		"step":     svc.HandleStep,
		"tasks":    svc.HandleTasks,
		"overlay":  svc.HandleOverlay,
		"pause":    svc.HandlePause,
		"close":    svc.HandleClose,
		"newCase":  svc.HandleStartCase,
		"messages": svc.HandlePublishMessage,
	} {
		t.Run(name, func(t *testing.T) {
			rec := call(t, h, http.MethodPost, `{"name":"x"}`, map[string]string{"id": sess.ID})
			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404 for a closed session", rec.Code)
			}
		})
	}
}

// Bad path values are refused before any session work happens.
func TestBadPathValues(t *testing.T) {
	svc := newService(t)
	sess := openSession(t, svc, userTaskXML)

	if rec := call(t, svc.HandleCompleteTask, http.MethodPost, "",
		map[string]string{"id": sess.ID, "jobKey": "not-a-number"}); rec.Code != http.StatusBadRequest {
		t.Errorf("bad job key = %d, want 400", rec.Code)
	}
	if rec := call(t, svc.HandleCase, http.MethodGet, "",
		map[string]string{"id": sess.ID, "caseKey": "nope"}); rec.Code != http.StatusBadRequest {
		t.Errorf("bad case key = %d, want 400", rec.Code)
	}
	if rec := call(t, svc.HandleCase, http.MethodGet, "",
		map[string]string{"id": sess.ID, "caseKey": "999"}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown case = %d, want 404", rec.Code)
	}
	if rec := call(t, svc.HandleCompleteTask, http.MethodPost, "",
		map[string]string{"id": sess.ID, "jobKey": "999"}); rec.Code != http.StatusConflict {
		t.Errorf("completing a job that is not waiting = %d, want 409", rec.Code)
	}
}

// The registry's cap surfaces as a refusal, not a server fault.
func TestFullRegistryRefusesToOpen(t *testing.T) {
	reg := playground.NewRegistry(time.Hour, 1)
	t.Cleanup(reg.CloseAll)
	svc := New(reg, func(*http.Request, string, string) ([]byte, int, string) {
		return nil, http.StatusNotFound, "no"
	}, vars)

	body := `{"source":"xml","xml":` + jsonString(userTaskXML) + `}`
	if rec := call(t, svc.HandleOpen, http.MethodPost, body, nil); rec.Code != http.StatusOK {
		t.Fatalf("first open = %d, body %s", rec.Code, rec.Body)
	}
	if rec := call(t, svc.HandleOpen, http.MethodPost, body, nil); rec.Code != http.StatusConflict {
		t.Errorf("second open over a cap of one = %d, want 409", rec.Code)
	}
}

// A stubbed run needs no person at all: this is the shape a batch will use.
func TestStubbedHumanTaskNeedsNobody(t *testing.T) {
	svc := newService(t)
	body := `{"source":"xml","xml":` + jsonString(userTaskXML) +
		`,"stubs":{"human":{"minMillis":60000,"maxMillis":60000,"outputs":{"decision":"auto"}}}}`
	var sess sessionResp
	decodeInto(t, call(t, svc.HandleOpen, http.MethodPost, body, nil), &sess)

	var started caseResp
	decodeInto(t, call(t, svc.HandleStartCase, http.MethodPost, "", map[string]string{"id": sess.ID}), &started)
	var prog progressResp
	decodeInto(t, call(t, svc.HandleRun, http.MethodPost, "", map[string]string{"id": sess.ID}), &prog)
	if !prog.Quiescent || prog.Occurrences != 1 {
		t.Errorf("progress = %+v, want one occurrence and quiescence", prog)
	}

	var done caseResp
	decodeInto(t, call(t, svc.HandleCase, http.MethodGet, "",
		map[string]string{"id": sess.ID, "caseKey": started.InstanceKey}), &done)
	if done.State != "completed" || done.Variables["decision"] != "auto" {
		t.Errorf("case = %+v, want it completed by the stub", done)
	}
}

// A session is not a shared resource: it can hold the variables of a draft only
// its owner may read, so somebody else's id reads as "not found".
func TestASessionBelongsToWhoeverOpenedIt(t *testing.T) {
	svc := newService(t)
	body := `{"source":"xml","xml":` + jsonString(userTaskXML) + `}`

	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	r = r.WithContext(httpapi.WithPrincipal(r.Context(), &httpapi.Principal{Username: "vreni"}))
	rec := httptest.NewRecorder()
	svc.HandleOpen(rec, r)
	var sess sessionResp
	decodeInto(t, rec, &sess)

	as := func(who string) int {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.SetPathValue("id", sess.ID)
		if who != "" {
			req = req.WithContext(httpapi.WithPrincipal(req.Context(), &httpapi.Principal{Username: who}))
		}
		w := httptest.NewRecorder()
		svc.HandleStatus(w, req)
		return w.Code
	}
	if got := as("vreni"); got != http.StatusOK {
		t.Errorf("the owner got %d, want 200", got)
	}
	if got := as("kurt"); got != http.StatusNotFound {
		t.Errorf("another modeler got %d, want 404 — an existing id must not be an oracle", got)
	}

	// Closing it is as much the owner's to decide as reading it.
	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req.SetPathValue("id", sess.ID)
	req = req.WithContext(httpapi.WithPrincipal(req.Context(), &httpapi.Principal{Username: "kurt"}))
	w := httptest.NewRecorder()
	svc.HandleClose(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("another modeler closed the session: %d", w.Code)
	}
}
