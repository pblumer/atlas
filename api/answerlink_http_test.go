package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
)

// A user task answered through a link a mail carries
// (ADR-draft-user-task-answered-by-link). The garage mails its customer the estimate
// with a link; the customer, who has no account, opens it, sees the task's form and
// answers; the instance carries on with the answer.

const answerLinkBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:atlas="http://atlas/schema/1.0"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="kostenfreigabe" isExecutable="true">
    <startEvent id="s"/>
    <serviceTask id="send" name="Kostenvoranschlag senden">
      <extensionElements>
        <atlas:mailConnector connector="mail" to="=kundeEmail" answerTask="approve"
          subject="Ihr Kostenvoranschlag" body="=&quot;Bitte freigeben: &quot; + answerLink"/>
      </extensionElements>
    </serviceTask>
    <userTask id="approve" name="Kostenvoranschlag freigeben" atlas:answeredBy="link">
      <documentation>Bitte prüfen Sie den Kostenvoranschlag.</documentation>
      <extensionElements><zeebe:formDefinition formId="al-approve"/></extensionElements>
    </userTask>
    <exclusiveGateway id="gw" default="no"/>
    <userTask id="repair" name="Reparieren"/>
    <endEvent id="declined"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="send"/>
    <sequenceFlow id="f2" sourceRef="send" targetRef="approve"/>
    <sequenceFlow id="f3" sourceRef="approve" targetRef="gw"/>
    <sequenceFlow id="yes" sourceRef="gw" targetRef="repair"><conditionExpression>=freigegeben = true</conditionExpression></sequenceFlow>
    <sequenceFlow id="no" sourceRef="gw" targetRef="declined"/>
    <sequenceFlow id="f4" sourceRef="repair" targetRef="e"/>
  </process>
</definitions>`

const answerLinkForm = `{"id":"al-approve","name":"Freigabe","schema":{"type":"default","components":[
  {"type":"number","key":"kostenvoranschlag","label":"Kostenvoranschlag (CHF)"},
  {"type":"checkbox","key":"freigegeben","label":"Ich gebe die Reparatur frei"},
  {"type":"textfield","key":"bemerkung","label":"Bemerkung"}]}}`

// answerLinkServer is a server with an external URL, a preview mail Worker named "mail",
// the form and the model deployed, and one instance started for kunde@example.ch.
func answerLinkServer(t *testing.T, opts ...api.Option) (*httptest.Server, *http.Client, uint64) {
	t.Helper()
	ts, _ := newAuthServerWith(t, "root", "rootpassword", opts...)
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/configured-workers",
		`{"name":"mail","kind":"mail","provider":"preview","sender":"werkstatt@example.ch"}`); code != http.StatusOK {
		t.Fatalf("create the mail worker: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/forms", answerLinkForm); code != http.StatusOK {
		t.Fatalf("save form: %d (%s)", code, b)
	}
	deployKey(t, ts, admin, answerLinkBPMN)
	code, b := cReq(t, admin, ts, "POST", "/api/v1/instances",
		`{"processId":"kostenfreigabe","variables":{"kundeEmail":"kunde@example.ch","kostenvoranschlag":1200}}`)
	if code != http.StatusOK {
		t.Fatalf("start: %d (%s)", code, b)
	}
	var inst struct {
		Key uint64 `json:"instanceKey"`
	}
	if err := json.Unmarshal(b, &inst); err != nil || inst.Key == 0 {
		t.Fatalf("decode start: %v (%s)", err, b)
	}
	return ts, admin, inst.Key
}

// answerTokenFrom finds the link in the one mail the outbox holds and returns its token.
func answerTokenFrom(t *testing.T, ts *httptest.Server, c *http.Client) string {
	t.Helper()
	msgs := outbox(t, ts, c)
	if len(msgs) != 1 {
		t.Fatalf("the outbox holds %d message(s), want the one estimate", len(msgs))
	}
	const prefix = "https://atlas.example.ch/public/answer#"
	i := strings.Index(msgs[0].Body, prefix)
	if i < 0 {
		t.Fatalf("the mail carries no answer link: %q", msgs[0].Body)
	}
	if len(msgs[0].To) != 1 || msgs[0].To[0] != "kunde@example.ch" {
		t.Errorf("to = %v, want the customer", msgs[0].To)
	}
	return strings.Fields(msgs[0].Body[i+len(prefix):])[0]
}

type answerStep struct {
	State string `json:"state"`
	Step  *struct {
		TaskKey       uint64          `json:"taskKey"`
		Title         string          `json:"title"`
		Documentation string          `json:"documentation"`
		Schema        json.RawMessage `json:"schema"`
		Data          map[string]any  `json:"data"`
	} `json:"step"`
}

func openTaskIDs(t *testing.T, ts *httptest.Server, c *http.Client, pi uint64) []string {
	t.Helper()
	code, body := cReq(t, c, ts, "GET", fmt.Sprintf("/api/v1/tasks?processInstance=%d", pi), "")
	if code != http.StatusOK {
		t.Fatalf("list tasks: %d (%s)", code, body)
	}
	var rows []struct {
		ElementID string `json:"elementId"`
	}
	if err := json.Unmarshal(listRows(t, body), &rows); err != nil {
		t.Fatalf("decode tasks: %v (%s)", err, body)
	}
	var ids []string
	for _, r := range rows {
		ids = append(ids, r.ElementID)
	}
	return ids
}

func TestACustomerAnswersATaskThroughTheLinkInTheirMail(t *testing.T) {
	ts, admin, pi := answerLinkServer(t, api.WithExternalURL("https://atlas.example.ch"))
	token := answerTokenFrom(t, ts, admin)

	// The link is the mail's alone: it is in no variable of the instance.
	code, vars := cReq(t, admin, ts, "GET", fmt.Sprintf("/api/v1/instances/%d/variables", pi), "")
	if code != http.StatusOK || strings.Contains(string(vars), token) || strings.Contains(string(vars), "answerLink") {
		t.Errorf("the instance's variables carry the link: %d %s", code, vars)
	}

	// The page itself is static and asks for nothing.
	if code, body := anon(t, ts, "GET", "/public/answer", "", ""); code != http.StatusOK || !strings.Contains(string(body), "/public/answer/task") {
		t.Fatalf("answer page: %d", code)
	}

	// Opening the link shows the task: its name, its documentation, its form, and the
	// values the instance holds under the form's fields.
	code, body := anon(t, ts, "GET", "/public/answer/task", token, "")
	var got answerStep
	if code != http.StatusOK || json.Unmarshal(body, &got) != nil || got.State != "open" || got.Step == nil {
		t.Fatalf("open the link: %d (%s)", code, body)
	}
	if got.Step.Title != "Kostenvoranschlag freigeben" || got.Step.Documentation == "" || len(got.Step.Schema) == 0 {
		t.Errorf("step = %+v, want the task's name, documentation and form", got.Step)
	}
	if v, _ := got.Step.Data["kostenvoranschlag"].(float64); v != 1200 {
		t.Errorf("prefill = %v, want the estimate the instance holds", got.Step.Data)
	}
	if _, leaked := got.Step.Data["kundeEmail"]; leaked {
		t.Errorf("the page is shown a variable its form does not bind: %v", got.Step.Data)
	}

	// A link scanner opens every link, repeatedly. Opening answers nothing.
	for range 3 {
		anon(t, ts, "GET", "/public/answer/task", token, "")
	}
	if ids := openTaskIDs(t, ts, admin, pi); len(ids) != 1 || ids[0] != "approve" {
		t.Fatalf("after the link was opened, open tasks = %v, want approve still waiting", ids)
	}

	// Nothing but the link opens it.
	parts := strings.Split(token, ".")
	if len(parts) != 4 {
		t.Fatalf("token %q has %d parts, want instance.scope.task.mac", token, len(parts))
	}
	for name, bad := range map[string]string{
		"no token":          "",
		"a wrong signature": strings.Join([]string{parts[0], parts[1], parts[2], strings.Repeat("0", len(parts[3]))}, "."),
		"another task":      strings.Join([]string{parts[0], parts[1], "cmVwYWly", parts[3]}, "."), // "repair", signed for "approve"
		"another instance":  strings.Join([]string{"1", parts[1], parts[2], parts[3]}, "."),
		"another scope":     strings.Join([]string{parts[0], "1", parts[2], parts[3]}, "."),
		"a malformed token": "garbage",
	} {
		if code, _ := anon(t, ts, "GET", "/public/answer/task", bad, ""); code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", name, code)
		}
	}

	// The link answers its own task and no other key.
	if code, _ := anon(t, ts, "POST", fmt.Sprintf("/public/answer/task/%d/complete", got.Step.TaskKey+1), token,
		`{"variables":{"freigegeben":true}}`); code != http.StatusNotFound {
		t.Errorf("another task key under this link: %d, want 404", code)
	}

	// Only the form's fields are accepted.
	path := fmt.Sprintf("/public/answer/task/%d/complete", got.Step.TaskKey)
	if code, body := anon(t, ts, "POST", path, token, `{"variables":{"freigegeben":true,"kundenLimit":99999}}`); code != http.StatusBadRequest {
		t.Errorf("a field not on the form: %d (%s), want 400", code, body)
	}

	// The answer completes the task, and the instance carries on with it.
	code, body = anon(t, ts, "POST", path, token, `{"variables":{"freigegeben":true,"bemerkung":"Bitte bis Freitag"}}`)
	var done answerStep
	if code != http.StatusOK || json.Unmarshal(body, &done) != nil || done.State != "done" {
		t.Fatalf("answer: %d (%s)", code, body)
	}
	if ids := openTaskIDs(t, ts, admin, pi); len(ids) != 1 || ids[0] != "repair" {
		t.Fatalf("after the answer, open tasks = %v, want repair", ids)
	}

	// Answered once: the link opens nothing any more.
	if code, _ := anon(t, ts, "GET", "/public/answer/task", token, ""); code != http.StatusNotFound {
		t.Errorf("the link after the answer: %d, want 404", code)
	}
	if code, _ := anon(t, ts, "POST", path, token, `{"variables":{"freigegeben":false}}`); code != http.StatusNotFound {
		t.Errorf("a second answer: %d, want 404", code)
	}
}

// TestALinkIsBuiltOnTheOperatorsOrigin: without an external URL there is no origin a
// customer can be sent to, and the mail fails saying so rather than going out with a
// link to wherever the server happened to be reached.
func TestALinkIsBuiltOnTheOperatorsOrigin(t *testing.T) {
	ts, admin, _ := answerLinkServer(t)
	if msgs := outbox(t, ts, admin); len(msgs) != 0 {
		t.Fatalf("a mail went out without an origin to link to: %+v", msgs)
	}
	code, body := cReq(t, admin, ts, "GET", "/api/v1/incidents", "")
	if code != http.StatusOK || !strings.Contains(string(body), "external URL") {
		t.Errorf("incidents: %d (%s), want one saying the external URL is missing", code, body)
	}
}

// TestALinkSurvivesARestart: nothing per link is stored, and the key it is signed with is
// derived from the vault's master key, so a link mailed before a restart opens the same
// task after it.
func TestALinkSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	ts, stop := openServerAt(t, dir, api.WithExternalURL("https://atlas.example.ch"))
	c := &http.Client{}
	if code, b := cReq(t, c, ts, "POST", "/api/v1/configured-workers",
		`{"name":"mail","kind":"mail","provider":"preview","sender":"werkstatt@example.ch"}`); code != http.StatusOK {
		t.Fatalf("create the mail worker: %d (%s)", code, b)
	}
	if code, b := cReq(t, c, ts, "POST", "/api/v1/forms", answerLinkForm); code != http.StatusOK {
		t.Fatalf("save form: %d (%s)", code, b)
	}
	deployKey(t, ts, c, answerLinkBPMN)
	if code, b := cReq(t, c, ts, "POST", "/api/v1/instances",
		`{"processId":"kostenfreigabe","variables":{"kundeEmail":"kunde@example.ch","kostenvoranschlag":1200}}`); code != http.StatusOK {
		t.Fatalf("start: %d (%s)", code, b)
	}
	token := answerTokenFrom(t, ts, c)
	stop()

	ts, stop = openServerAt(t, dir, api.WithExternalURL("https://atlas.example.ch"))
	defer stop()
	code, body := anon(t, ts, "GET", "/public/answer/task", token, "")
	var got answerStep
	if code != http.StatusOK || json.Unmarshal(body, &got) != nil || got.Step == nil {
		t.Fatalf("the link after a restart: %d (%s)", code, body)
	}
	code, body = anon(t, ts, "POST", fmt.Sprintf("/public/answer/task/%d/complete", got.Step.TaskKey), token,
		`{"variables":{"freigegeben":false}}`)
	if code != http.StatusOK {
		t.Fatalf("answer after a restart: %d (%s)", code, body)
	}
}

// TestATaskParkedBehindAnIncidentIsNotOpenToItsLink: a task an operator has to repair
// first is not open, to its link either.
func TestATaskParkedBehindAnIncidentIsNotOpenToItsLink(t *testing.T) {
	ts, admin, _ := answerLinkServer(t, api.WithExternalURL("https://atlas.example.ch"))
	token := answerTokenFrom(t, ts, admin)
	code, body := anon(t, ts, "GET", "/public/answer/task", token, "")
	var got answerStep
	if code != http.StatusOK || json.Unmarshal(body, &got) != nil || got.Step == nil {
		t.Fatalf("open the link: %d (%s)", code, body)
	}
	if code, b := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/jobs/%d/fail", got.Step.TaskKey),
		`{"retries":0,"errorMessage":"parked for the test"}`); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("park the task: %d (%s)", code, b)
	}
	if code, _ := anon(t, ts, "GET", "/public/answer/task", token, ""); code != http.StatusNotFound {
		t.Errorf("a parked task through its link: %d, want 404", code)
	}
}

// TestEachRunOfAnEventSubprocessMailsTheLinkOfItsOwnTask: two findings reported back to
// back start two runs of a non-interrupting event subprocess, each mailing the customer
// the link of its own approval. The link is bound to the run it was sent from, so each
// opens the task of its own finding — the case a link naming the task alone would get
// wrong.
func TestEachRunOfAnEventSubprocessMailsTheLinkOfItsOwnTask(t *testing.T) {
	const model = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:atlas="http://atlas/schema/1.0"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <message id="mF" name="finding"><extensionElements><zeebe:subscription correlationKey="=orderId"/></extensionElements></message>
  <process id="werkstatt" isExecutable="true">
    <startEvent id="s"/>
    <userTask id="work" name="Arbeiten"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="work"/>
    <sequenceFlow id="f2" sourceRef="work" targetRef="e"/>
    <subProcess id="on_finding" triggeredByEvent="true">
      <extensionElements><zeebe:ioMapping>
        <zeebe:input source="=findingId" target="findingId"/>
        <zeebe:input source="=null" target="extraApproved"/>
      </zeebe:ioMapping></extensionElements>
      <startEvent id="fs" isInterrupting="false"><messageEventDefinition messageRef="mF"/></startEvent>
      <serviceTask id="offer">
        <extensionElements>
          <atlas:mailConnector connector="mail" to="=kundeEmail" answerTask="approve_extra"
            subject="=&quot;Zusatzbefund &quot; + findingId" body="=findingId + &quot; &quot; + answerLink"/>
        </extensionElements>
      </serviceTask>
      <userTask id="approve_extra" name="Zusatzarbeit freigeben" atlas:answeredBy="link">
        <extensionElements><zeebe:formDefinition formId="al-extra"/></extensionElements>
      </userTask>
      <endEvent id="fe"/>
      <sequenceFlow id="r1" sourceRef="fs" targetRef="offer"/>
      <sequenceFlow id="r2" sourceRef="offer" targetRef="approve_extra"/>
      <sequenceFlow id="r3" sourceRef="approve_extra" targetRef="fe"/>
    </subProcess>
  </process>
</definitions>`
	ts, _ := newAuthServerWith(t, "root", "rootpassword", api.WithExternalURL("https://atlas.example.ch"))
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	for _, req := range [][2]string{
		{"/api/v1/configured-workers", `{"name":"mail","kind":"mail","provider":"preview","sender":"werkstatt@example.ch"}`},
		{"/api/v1/forms", `{"id":"al-extra","name":"Zusatz","schema":{"type":"default","components":[
			{"type":"textfield","key":"findingId","label":"Befund"},{"type":"checkbox","key":"extraApproved","label":"Freigeben"}]}}`},
		{"/api/v1/deployments", model},
		{"/api/v1/instances", `{"processId":"werkstatt","variables":{"orderId":"A-1","kundeEmail":"kunde@example.ch"}}`},
		{"/api/v1/messages", `{"name":"finding","correlationKey":"A-1","variables":{"findingId":"BF-1"}}`},
		{"/api/v1/messages", `{"name":"finding","correlationKey":"A-1","variables":{"findingId":"BF-2"}}`},
	} {
		if code, b := cReq(t, admin, ts, "POST", req[0], req[1]); code != http.StatusOK {
			t.Fatalf("POST %s: %d (%s)", req[0], code, b)
		}
	}
	links := map[string]string{} // finding → token
	for _, m := range outbox(t, ts, admin) {
		f := strings.Fields(m.Body)
		if len(f) == 2 {
			links[f[0]] = f[1][strings.Index(f[1], "#")+1:]
		}
	}
	if len(links) != 2 || links["BF-1"] == links["BF-2"] {
		t.Fatalf("links = %v, want one per finding, each its own", links)
	}
	stepOf := func(finding string) answerStep {
		t.Helper()
		code, body := anon(t, ts, "GET", "/public/answer/task", links[finding], "")
		var got answerStep
		if code != http.StatusOK || json.Unmarshal(body, &got) != nil || got.Step == nil {
			t.Fatalf("open the link of %s: %d (%s)", finding, code, body)
		}
		return got
	}
	for _, f := range []string{"BF-1", "BF-2"} {
		if got := stepOf(f).Step.Data["findingId"]; got != f {
			t.Errorf("the link of %s opens the task of %v", f, got)
		}
	}
	second := stepOf("BF-2")
	if code, body := anon(t, ts, "POST", fmt.Sprintf("/public/answer/task/%d/complete", second.Step.TaskKey), links["BF-2"],
		`{"variables":{"extraApproved":true}}`); code != http.StatusOK {
		t.Fatalf("answer BF-2: %d (%s)", code, body)
	}
	if got := stepOf("BF-1").Step.Data["findingId"]; got != "BF-1" {
		t.Errorf("after BF-2 was answered, the link of BF-1 opens %v", got)
	}
	if code, _ := anon(t, ts, "GET", "/public/answer/task", links["BF-2"], ""); code != http.StatusNotFound {
		t.Errorf("the link of the answered BF-2: %d, want 404", code)
	}
}
