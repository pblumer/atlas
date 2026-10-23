package api_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
)

// An anonymous visitor walking a public wizard (ADR-0449, its fourth step). The start
// hands the visitor a pass; the pass, in an Authorization header, opens the open screens
// of that instance's public sittings and nothing else.

// publicWizardFlowBPMN starts from a start form into a public wizard of two screens with
// a branch between them, followed by an internal task. The second screen depends on the
// answer to the first.
const publicWizardFlowBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:atlas="http://atlas/schema/1.0"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="antrag" isExecutable="true">
    <startEvent id="s">
      <extensionElements><zeebe:formDefinition formId="pw-start"/></extensionElements>
    </startEvent>
    <subProcess id="sitting" atlas:wizard="public">
      <startEvent id="ws"/>
      <userTask id="eins" name="Ihre Angaben">
        <extensionElements><zeebe:formDefinition formId="pw-eins"/></extensionElements>
      </userTask>
      <exclusiveGateway id="gw" default="toB"/>
      <userTask id="zwei_a" name="Reiseziel">
        <extensionElements><zeebe:formDefinition formId="pw-zwei"/></extensionElements>
      </userTask>
      <userTask id="zwei_b" name="Alternative"/>
      <endEvent id="we_a"/>
      <endEvent id="we_b"/>
      <sequenceFlow id="w1" sourceRef="ws" targetRef="eins"/>
      <sequenceFlow id="w2" sourceRef="eins" targetRef="gw"/>
      <sequenceFlow id="toA" sourceRef="gw" targetRef="zwei_a"><conditionExpression>=weiter = "a"</conditionExpression></sequenceFlow>
      <sequenceFlow id="toB" sourceRef="gw" targetRef="zwei_b"/>
      <sequenceFlow id="w3" sourceRef="zwei_a" targetRef="we_a"/>
      <sequenceFlow id="w4" sourceRef="zwei_b" targetRef="we_b"/>
    </subProcess>
    <boundaryEvent id="abandoned" attachedToRef="sitting"><timerEventDefinition><timeDuration>PT30M</timeDuration></timerEventDefinition></boundaryEvent>
    <endEvent id="ea"/>
    <userTask id="intern" name="Prüfen"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="sitting"/>
    <sequenceFlow id="f2" sourceRef="sitting" targetRef="intern"/>
    <sequenceFlow id="f3" sourceRef="intern" targetRef="e"/>
    <sequenceFlow id="fa" sourceRef="abandoned" targetRef="ea"/>
  </process>
</definitions>`

// publicWizardAnswer is what the start, the step lookup and a completion answer.
type publicWizardAnswer struct {
	Started bool `json:"started"`
	Wizard  *struct {
		Pass  string `json:"pass"`
		State string `json:"state"`
		Step  *struct {
			TaskKey uint64          `json:"taskKey"`
			Title   string          `json:"title"`
			Schema  json.RawMessage `json:"schema"`
			Data    map[string]any  `json:"data"`
		} `json:"step"`
	} `json:"wizard"`
	State string `json:"state"`
	Step  *struct {
		TaskKey uint64         `json:"taskKey"`
		Title   string         `json:"title"`
		Data    map[string]any `json:"data"`
	} `json:"step"`
}

// publishPublicWizard saves the three forms, deploys the model and mints its start link.
func publishPublicWizard(t *testing.T, ts *httptest.Server, c *http.Client) string {
	t.Helper()
	for _, f := range []string{
		`{"id":"pw-start","name":"Start","schema":{"type":"default","components":[{"type":"textfield","key":"customer"},{"type":"textfield","key":"notiz"}]}}`,
		`{"id":"pw-eins","name":"Eins","schema":{"type":"default","components":[{"type":"textfield","key":"weiter"},{"type":"textfield","key":"customer"}]}}`,
		`{"id":"pw-zwei","name":"Zwei","schema":{"type":"default","components":[{"type":"textfield","key":"ziel"}]}}`,
	} {
		if code, body := cReq(t, c, ts, "POST", "/api/v1/forms", f); code != http.StatusOK {
			t.Fatalf("save form: %d (%s)", code, body)
		}
	}
	deployKey(t, ts, c, publicWizardFlowBPMN)
	code, body := cReq(t, c, ts, "POST", "/api/v1/public-links", `{"processId":"antrag"}`)
	if code != http.StatusOK {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	var link struct{ Token string }
	if err := json.Unmarshal(body, &link); err != nil || link.Token == "" {
		t.Fatalf("decode link: %v (%s)", err, body)
	}
	return link.Token
}

// anon sends a request with no session at all, carrying pass in the Authorization header
// when it is set.
func anon(t *testing.T, ts *httptest.Server, method, path, pass, body string) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, r)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if pass != "" {
		req.Header.Set("Authorization", "Bearer "+pass)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	return res.StatusCode, out
}

func decodePublic(t *testing.T, code int, body []byte) publicWizardAnswer {
	t.Helper()
	if code != http.StatusOK {
		t.Fatalf("status %d (%s)", code, body)
	}
	var a publicWizardAnswer
	if err := json.Unmarshal(body, &a); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	return a
}

// anonAnswer is anon for a call that must succeed, decoded.
func anonAnswer(t *testing.T, ts *httptest.Server, method, path, pass, body string) publicWizardAnswer {
	t.Helper()
	code, out := anon(t, ts, method, path, pass, body)
	return decodePublic(t, code, out)
}

var passShape = regexp.MustCompile(`^[0-9]+\.[0-9a-f]{64}$`)

// startPublic starts the published process anonymously and returns the visitor's pass and
// the first screen's task key.
func startPublic(t *testing.T, ts *httptest.Server, token string) (string, uint64) {
	t.Helper()
	code, body := anon(t, ts, "POST", "/public/forms/"+token+"/start", "", `{"variables":{"customer":"Acme","notiz":"intern"}}`)
	a := decodePublic(t, code, body)
	if !a.Started || a.Wizard == nil || !passShape.MatchString(a.Wizard.Pass) {
		t.Fatalf("the start handed no pass: %s", body)
	}
	if a.Wizard.State != "step" || a.Wizard.Step == nil || a.Wizard.Step.Title != "Ihre Angaben" || len(a.Wizard.Step.Schema) == 0 {
		t.Fatalf("the start did not land on the first screen: %s", body)
	}
	// The screen is prefilled with what the instance holds under the fields its form
	// binds — the customer the start form asked for — and nothing it does not bind.
	if a.Wizard.Step.Data["customer"] != "Acme" || a.Wizard.Step.Data["notiz"] != nil {
		t.Errorf("prefill = %v, want the form's own field and nothing else", a.Wizard.Step.Data)
	}
	return a.Wizard.Pass, a.Wizard.Step.TaskKey
}

// TestAVisitorWalksAPublicWizardWithItsPass walks the whole sitting anonymously: the start
// hands a pass and lands on the first screen, the answer on it decides the second, the
// second ends the sitting, and the internal task after it is not the visitor's.
func TestAVisitorWalksAPublicWizardWithItsPass(t *testing.T) {
	ts := newTestServer(t)
	token := publishPublicWizard(t, ts, ts.Client())
	pass, first := startPublic(t, ts, token)

	// The pass reads back the same screen.
	got := anonAnswer(t, ts, "GET", "/public/wizard/step", pass, "")
	if got.State != "step" || got.Step == nil || got.Step.TaskKey != first {
		t.Fatalf("the step lookup: %+v, want screen %d", got, first)
	}

	// A field the screen does not have is refused, and nothing is completed.
	code, body := anon(t, ts, "POST", fmt.Sprintf("/public/wizard/steps/%d/complete", first), pass, `{"variables":{"weiter":"a","approved":true}}`)
	if code != http.StatusBadRequest || !strings.Contains(string(body), "approved") {
		t.Fatalf("a field the screen does not have: %d (%s), want 400 naming it", code, body)
	}

	second := anonAnswer(t, ts, "POST", fmt.Sprintf("/public/wizard/steps/%d/complete", first), pass, `{"variables":{"weiter":"a"}}`)
	if second.State != "step" || second.Step == nil || second.Step.Title != "Reiseziel" {
		t.Fatalf("after the first screen: %+v, want Reiseziel", second)
	}

	last := anonAnswer(t, ts, "POST", fmt.Sprintf("/public/wizard/steps/%d/complete", second.Step.TaskKey), pass, `{"variables":{"ziel":"Rom"}}`)
	if last.State != "done" || last.Step != nil {
		t.Fatalf("after the last screen: %+v, want done", last)
	}
	if again := anonAnswer(t, ts, "GET", "/public/wizard/step", pass, ""); again.State != "done" {
		t.Errorf("the step lookup after the sitting: %+v, want done", again)
	}

	// The internal task after the sitting is not the visitor's, though the pass opens
	// its instance.
	code, body = cReq(t, ts.Client(), ts, "GET", "/api/v1/tasks", "")
	var page struct {
		Items []struct {
			Key       uint64 `json:"key"`
			ElementID string `json:"elementId"`
		} `json:"items"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &page) != nil || len(page.Items) != 1 || page.Items[0].ElementID != "intern" {
		t.Fatalf("the task after the sitting: %d (%s)", code, body)
	}
	if code, body := anon(t, ts, "POST", fmt.Sprintf("/public/wizard/steps/%d/complete", page.Items[0].Key), pass, `{}`); code != http.StatusNotFound {
		t.Errorf("the pass completed an internal task: %d (%s)", code, body)
	}
}

// TestAPassOpensItsOwnSittingAndNothingElse: no pass, a forged pass, and a pass of
// another instance all answer 404 alike; one visitor's pass does not reach another
// visitor's screen.
func TestAPassOpensItsOwnSittingAndNothingElse(t *testing.T) {
	ts := newTestServer(t)
	token := publishPublicWizard(t, ts, ts.Client())
	passA, _ := startPublic(t, ts, token)
	passB, stepB := startPublic(t, ts, token)
	if passA == passB {
		t.Fatal("two starts were handed the same pass")
	}

	keyA, _, _ := strings.Cut(passA, ".")
	_, secretB, _ := strings.Cut(passB, ".")
	for name, pass := range map[string]string{
		"no pass":                      "",
		"not a pass":                   "hello",
		"a forged secret":              keyA + "." + strings.Repeat("0", 64),
		"another instance's secret":    keyA + "." + secretB,
		"a secret of the wrong length": keyA + ".abcd",
	} {
		if code, body := anon(t, ts, "GET", "/public/wizard/step", pass, ""); code != http.StatusNotFound {
			t.Errorf("%s: %d (%s), want 404", name, code, body)
		}
	}
	if code, body := anon(t, ts, "POST", fmt.Sprintf("/public/wizard/steps/%d/complete", stepB), passA, `{"variables":{"weiter":"a"}}`); code != http.StatusNotFound {
		t.Errorf("visitor A completed visitor B's screen: %d (%s), want 404", code, body)
	}
	if b := anonAnswer(t, ts, "GET", "/public/wizard/step", passB, ""); b.Step == nil || b.Step.TaskKey != stepB {
		t.Errorf("visitor B's screen is no longer open: %+v", b)
	}
}

// TestAPublicWizardNeedsNoAccount: on a server that requires sign-in, the public routes
// still answer the pass and nothing else, and the screens stay out of signed-in inboxes.
func TestAPublicWizardNeedsNoAccount(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	alice := twoUsers(t, ts, admin, "alice", "mallory")[0]
	token := publishPublicWizard(t, ts, admin)
	pass, first := startPublic(t, ts, token)

	if code, body := cReq(t, alice, ts, "GET", "/api/v1/tasks", ""); code != http.StatusOK || strings.Contains(string(body), fmt.Sprint(first)) {
		t.Errorf("the visitor's screen is in alice's inbox: %d (%s)", code, body)
	}
	next := anonAnswer(t, ts, "POST", fmt.Sprintf("/public/wizard/steps/%d/complete", first), pass, `{"variables":{"weiter":"b"}}`)
	if next.State != "step" || next.Step == nil || next.Step.Title != "Alternative" {
		t.Fatalf("after the first screen: %+v, want Alternative", next)
	}
}

// TestAnEmbeddedPageMaySendThePass: a cross-origin page on the allow-list is told it may
// send the Authorization header the pass travels in, and still never credentials.
func TestAnEmbeddedPageMaySendThePass(t *testing.T) {
	const origin = "https://embed.example"
	ts := newTestServerWith(t, api.WithPublicFormsCORS([]string{origin}))
	code, h := reqOrigin(t, ts.URL+"/public/wizard/step", http.MethodOptions, origin, "", "")
	if code != http.StatusNoContent {
		t.Fatalf("preflight: %d", code)
	}
	if got := h.Get("Access-Control-Allow-Headers"); !strings.Contains(got, "Authorization") {
		t.Errorf("Access-Control-Allow-Headers = %q, want Authorization in it", got)
	}
	if h.Get("Access-Control-Allow-Credentials") != "" {
		t.Error("credentials were allowed")
	}
}

// TestAPublicWizardStepRefusesADirectoryField: forms are not versioned, so a screen's
// form can gain a directory field while a visitor sits on it. The step then refuses the
// answer as the public start link does — nobody there is signed in to search, and a
// check would tell an anonymous visitor who is in the directory — and the screen stays
// open.
func TestAPublicWizardStepRefusesADirectoryField(t *testing.T) {
	ts := newTestServer(t)
	token := publishPublicWizard(t, ts, ts.Client())
	pass, first := startPublic(t, ts, token)
	if code, body := cReq(t, ts.Client(), ts, "POST", "/api/v1/forms",
		`{"id":"pw-eins","name":"Eins","schema":{"type":"default","components":[
		  {"type":"textfield","key":"weiter",
		   "atlasDataSource":{"version":1,"workerType":"entra","worker":"ucc_entra","resource":"users"}},
		  {"type":"textfield","key":"customer"}]}}`); code != http.StatusOK {
		t.Fatalf("save form: %d (%s)", code, body)
	}
	code, body := anon(t, ts, "POST", fmt.Sprintf("/public/wizard/steps/%d/complete", first), pass,
		`{"variables":{"weiter":"11111111-0000-0000-0000-000000000001"}}`)
	if code != http.StatusBadRequest || !strings.Contains(string(body), "directory field") {
		t.Fatalf("a screen with a directory field: %d (%s), want 400", code, body)
	}
	if got := anonAnswer(t, ts, "GET", "/public/wizard/step", pass, ""); got.Step == nil || got.Step.TaskKey != first {
		t.Errorf("the screen is no longer open: %+v", got)
	}
}
