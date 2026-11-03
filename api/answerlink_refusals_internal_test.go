package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// The refusals of an answer link (ADR-0523): every way a
// request can fail to name an open task answers before anything is read or written, and
// every one of them answers the same 404 a wrong signature does, so trying tokens tells a
// visitor nothing.

// answerRefusalModel is a task answered by link whose form holds a personal value, in a
// subprocess, so a link's scope can be checked against a chain of scopes.
const answerRefusalModel = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
    xmlns:atlas="http://atlas/schema/1.0"
    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="refusals" isExecutable="true" atlas:personal="bemerkung" atlas:dataSubject="kunde">
    <startEvent id="s"/>
    <subProcess id="sub">
      <startEvent id="ss"/>
      <userTask id="ask" atlas:answeredBy="link">
        <extensionElements><zeebe:formDefinition formId="al-refusals"/></extensionElements>
      </userTask>
      <endEvent id="se"/>
      <sequenceFlow id="sf1" sourceRef="ss" targetRef="ask"/>
      <sequenceFlow id="sf2" sourceRef="ask" targetRef="se"/>
    </subProcess>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="sub"/>
    <sequenceFlow id="f2" sourceRef="sub" targetRef="e"/>
  </process>
</definitions>`

const answerRefusalForm = `{"id":"al-refusals","name":"Antwort","schema":{"type":"default","components":[
  {"type":"textfield","key":"bemerkung","label":"Bemerkung"}]}}`

type answerRefusalFixture struct {
	srv          *Server
	pi, sub, ask uint64 // the instance, the subprocess's scope, the user task's element instance
	taskKey      uint64
	token        string
}

func (f answerRefusalFixture) do(t *testing.T, method, path, auth, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, req)
	return rec
}

func newAnswerRefusalFixture(t *testing.T) answerRefusalFixture {
	t.Helper()
	srv, _ := newValidateServer(t)
	f := answerRefusalFixture{srv: srv}
	if rec := f.do(t, "POST", "/api/v1/forms", "", answerRefusalForm); rec.Code != http.StatusOK {
		t.Fatalf("save form: %d %s", rec.Code, rec.Body)
	}
	if rec := f.do(t, "POST", "/api/v1/deployments", "", answerRefusalModel); rec.Code != http.StatusOK {
		t.Fatalf("deploy: %d %s", rec.Code, rec.Body)
	}
	// Started without its data subject, so a personal answer cannot be sealed.
	rec := f.do(t, "POST", "/api/v1/instances", "", `{"processId":"refusals"}`)
	var inst struct {
		Key uint64 `json:"instanceKey"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &inst) != nil || inst.Key == 0 {
		t.Fatalf("start: %d %s", rec.Code, rec.Body)
	}
	f.pi = inst.Key
	srv.do(func() {
		_ = srv.store.ElementInstancesOfProcess(f.pi, func(k uint64) error {
			ei, ok, err := srv.store.GetElementInstance(k)
			if err != nil || !ok {
				return err
			}
			switch srv.deployments[ei.ProcessDefKey].cp.ElementBpmnId(ei.ElementId) {
			case "sub":
				f.sub = k
			case "ask":
				f.ask = k
			}
			return nil
		})
	})
	if f.sub == 0 || f.ask == 0 {
		t.Fatalf("subprocess %d, task %d: the instance did not reach the task", f.sub, f.ask)
	}
	var err error
	if f.token, err = srv.answerToken(f.pi, f.sub, "ask"); err != nil {
		t.Fatalf("token: %v", err)
	}
	rec = f.do(t, "GET", "/public/answer/task", f.token, "")
	var open struct {
		Step struct {
			TaskKey uint64 `json:"taskKey"`
		} `json:"step"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &open) != nil || open.Step.TaskKey == 0 {
		t.Fatalf("open the link: %d %s", rec.Code, rec.Body)
	}
	f.taskKey = open.Step.TaskKey
	return f
}

// TestAMalformedAnswerTokenOpensNothing walks each part of the token: it is refused on
// its shape, and a correctly signed one with a broken part is refused too.
func TestAMalformedAnswerTokenOpensNothing(t *testing.T) {
	f := newAnswerRefusalFixture(t)
	parts := strings.Split(f.token, ".")
	mac := parts[3]
	task := parts[2]
	for name, tok := range map[string]string{
		"an instance that is no number": "x." + parts[1] + "." + task + "." + mac,
		"instance zero":                 "0." + parts[1] + "." + task + "." + mac,
		"a scope that is no number":     parts[0] + ".x." + task + "." + mac,
		"scope zero":                    parts[0] + ".0." + task + "." + mac,
		"a task id that is no base64":   parts[0] + "." + parts[1] + ".!!!!." + mac,
		"a signature that is no hex":    parts[0] + "." + parts[1] + "." + task + "." + strings.Repeat("z", len(mac)),
		"a task id past the bound": parts[0] + "." + parts[1] + "." +
			base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("a", maxAnswerTaskID+1))) + "." + mac,
	} {
		if rec := f.do(t, "GET", "/public/answer/task", tok, ""); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d %s, want 404", name, rec.Code, rec.Body)
		}
	}
	// Not a bearer token at all.
	req := httptest.NewRequest("GET", "/public/answer/task", nil)
	req.Header.Set("Authorization", "Basic "+f.token)
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("a Basic header: %d, want 404", rec.Code)
	}
}

// TestALinkOpensItsTaskOnlyFromAnEnclosingScope: the link names the scope the mail was
// sent from. The subprocess and the instance enclose the task; a scope elsewhere does not,
// even signed.
func TestALinkOpensItsTaskOnlyFromAnEnclosingScope(t *testing.T) {
	f := newAnswerRefusalFixture(t)
	var ei *model.ElementInstanceValue
	f.srv.do(func() { ei, _, _ = f.srv.store.GetElementInstance(f.ask) })
	if ei == nil {
		t.Fatal("the task's element instance is gone")
	}
	for _, tc := range []struct {
		name  string
		scope uint64
		want  bool
	}{
		{"the instance", f.pi, true},
		{"the subprocess", f.sub, true},
		{"a key no scope has", f.ask + 1_000_000, false},
		{"the task itself, which encloses nothing", f.ask, false},
	} {
		var (
			in  bool
			err error
		)
		f.srv.do(func() { in, err = f.srv.withinScope(ei, tc.scope) })
		if err != nil || in != tc.want {
			t.Errorf("%s: within = %v, %v; want %v", tc.name, in, err, tc.want)
		}
	}
	// A scope chain that leads to a key the store does not hold ends there.
	lost := *ei
	lost.FlowScopeKey = f.ask + 2_000_000
	f.srv.do(func() {
		in, err := f.srv.withinScope(&lost, f.sub)
		if in || err != nil {
			t.Errorf("a broken chain: within = %v, %v; want false", in, err)
		}
	})
	// An element with no flow scope lies in nothing but its instance.
	root := *ei
	root.FlowScopeKey = 0
	f.srv.do(func() {
		if in, _ := f.srv.withinScope(&root, f.sub); in {
			t.Error("an element without a flow scope lies in the subprocess")
		}
	})

	tok, err := f.srv.answerToken(f.pi, f.ask+1_000_000, "ask")
	if err != nil {
		t.Fatal(err)
	}
	if rec := f.do(t, "GET", "/public/answer/task", tok, ""); rec.Code != http.StatusNotFound {
		t.Errorf("a signed link from a scope that does not enclose the task: %d, want 404", rec.Code)
	}
}

// TestAnAnswerIsRefusedBeforeItIsRead: a task key that is no number, a body that cannot
// be read or parsed, and a personal value with no data subject to seal it under are all
// refused, and the task stays open.
func TestAnAnswerIsRefusedBeforeItIsRead(t *testing.T) {
	f := newAnswerRefusalFixture(t)
	path := "/public/answer/task/" + strconv.FormatUint(f.taskKey, 10) + "/complete"

	if rec := f.do(t, "POST", "/public/answer/task/abc/complete", f.token, `{}`); rec.Code != http.StatusNotFound {
		t.Errorf("a task key that is no number: %d, want 404", rec.Code)
	}
	if rec := f.do(t, "POST", path, f.token, `{"variables":`); rec.Code != http.StatusBadRequest {
		t.Errorf("a body that is no JSON: %d, want 400", rec.Code)
	}
	req := httptest.NewRequest("POST", path, errReader{})
	req.Header.Set("Authorization", "Bearer "+f.token)
	rec := httptest.NewRecorder()
	f.srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a body that cannot be read: %d, want 400", rec.Code)
	}
	if rec := f.do(t, "POST", path, "garbage", `{}`); rec.Code != http.StatusNotFound {
		t.Errorf("an answer under a malformed token: %d, want 404", rec.Code)
	}
	rec = f.do(t, "POST", path, f.token, `{"variables":{"bemerkung":"bitte zurückrufen"}}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "data subject") {
		t.Errorf("a personal answer with no data subject: %d %s, want 400 naming the subject", rec.Code, rec.Body)
	}
	var open bool
	f.srv.do(func() {
		_, jv, err := f.srv.openAnswerTask(answerLink{piKey: f.pi, scopeKey: f.sub, task: "ask"})
		open = err == nil && jv != nil && jv.JobType == compiler.UserTaskJobTypeIndex
	})
	if !open {
		t.Error("a refused answer completed the task")
	}
}

// TestTryingAnswerLinksSpendsTheVisitorsBudget: the rate limit stands before the token
// is read, on the page, the task and the answer alike.
func TestTryingAnswerLinksSpendsTheVisitorsBudget(t *testing.T) {
	f := newAnswerRefusalFixture(t)
	f.srv.publicRate = newRateLimiter(0, 0)
	for _, r := range []struct{ method, path string }{
		{"GET", "/public/answer"},
		{"GET", "/public/answer/task"},
		{"POST", "/public/answer/task/" + strconv.FormatUint(f.taskKey, 10) + "/complete"},
	} {
		if rec := f.do(t, r.method, r.path, f.token, `{}`); rec.Code != http.StatusTooManyRequests {
			t.Errorf("%s %s past the budget: %d, want 429", r.method, r.path, rec.Code)
		}
	}
}

// TestAnAnswerLinkNeedsTheVaultAndAnOrigin: the signing key is derived from the vault's
// master key and the link is built on the operator's origin, and without either no link
// is minted rather than a weaker one.
func TestAnAnswerLinkNeedsTheVaultAndAnOrigin(t *testing.T) {
	bare := &Server{externalURL: "https://atlas.example.ch"}
	if _, err := bare.answerToken(1, 1, "ask"); err == nil || !strings.Contains(err.Error(), "vault") {
		t.Errorf("a token without a vault: %v, want an error naming the vault", err)
	}
	if _, err := (mailDirectory{s: bare}).AnswerLink(1, 1, "ask"); err == nil || !strings.Contains(err.Error(), "vault") {
		t.Errorf("a link without a vault: %v, want an error naming the vault", err)
	}
	if _, err := (mailDirectory{s: &Server{}}).AnswerLink(1, 1, "ask"); err == nil || !strings.Contains(err.Error(), "external URL") {
		t.Errorf("a link without an origin: %v, want an error naming the external URL", err)
	}
}

// TestAnAnswerLinkRefusesAFormWithADirectoryField: a directory field can only be answered
// by a signed-in person (ADR-0473), and the holder of a link is nobody the directory
// knows, so the answer fails closed — as through every public link.
func TestAnAnswerLinkRefusesAFormWithADirectoryField(t *testing.T) {
	f := newAnswerRefusalFixture(t)
	if rec := f.do(t, "POST", "/api/v1/forms", "", `{"id":"al-refusals","name":"Antwort","schema":{"type":"default","components":[
	  {"type":"textfield","key":"bemerkung","label":"Bemerkung"},
	  {"type":"textfield","key":"kontakt","label":"Kontakt",
	   "atlasDataSource":{"version":1,"workerType":"entra","worker":"ucc_entra","resource":"users"}}]}}`); rec.Code != http.StatusOK {
		t.Fatalf("save the form with a directory field: %d %s", rec.Code, rec.Body)
	}
	rec := f.do(t, "POST", "/public/answer/task/"+strconv.FormatUint(f.taskKey, 10)+"/complete", f.token,
		`{"variables":{"kontakt":"someone"}}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "directory field") {
		t.Errorf("an answer to a form with a directory field: %d %s, want 400 naming the directory field", rec.Code, rec.Body)
	}
}
