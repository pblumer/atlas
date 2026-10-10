package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Carrying a person through a wizard (ADR-0449, its second step): the completion of a
// screen, and a start that runs into a wizard, answer with where the sitting stands —
// the next screen, claimed for the person, or that it waits, is done, or has passed to
// somebody else.

// wizardBPMN is a sitting of two screens with a branch between them, bounded by a
// timer, followed by a task outside it. inner replaces the screens when a case needs a
// different inside; empty keeps the branch.
func wizardBPMN(inner string) string {
	if inner == "" {
		inner = `
      <userTask id="eins"/>
      <exclusiveGateway id="gw" default="toB"/>
      <userTask id="zwei_a"/>
      <userTask id="zwei_b"/>
      <endEvent id="we_a"/>
      <endEvent id="we_b"/>
      <sequenceFlow id="w1" sourceRef="ws" targetRef="eins"/>
      <sequenceFlow id="w2" sourceRef="eins" targetRef="gw"/>
      <sequenceFlow id="toA" sourceRef="gw" targetRef="zwei_a"><conditionExpression>=weiter = "a"</conditionExpression></sequenceFlow>
      <sequenceFlow id="toB" sourceRef="gw" targetRef="zwei_b"/>
      <sequenceFlow id="w3" sourceRef="zwei_a" targetRef="we_a"/>
      <sequenceFlow id="w4" sourceRef="zwei_b" targetRef="we_b"/>`
	}
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:atlas="http://atlas/schema/1.0"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="antrag" isExecutable="true">
    <startEvent id="s"/>
    <subProcess id="sitting" atlas:wizard="internal">
      <startEvent id="ws"/>` + inner + `
    </subProcess>
    <boundaryEvent id="abandoned" attachedToRef="sitting"><timerEventDefinition><timeDuration>PT30M</timeDuration></timerEventDefinition></boundaryEvent>
    <endEvent id="ea"/>
    <userTask id="nachlauf"/>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="sitting"/>
    <sequenceFlow id="f2" sourceRef="sitting" targetRef="nachlauf"/>
    <sequenceFlow id="f3" sourceRef="nachlauf" targetRef="e"/>
    <sequenceFlow id="fa" sourceRef="abandoned" targetRef="ea"/>
  </process>
</definitions>`
}

// wizardAnswer is what a start or a completion answers; Wizard is nil when the call
// touched no wizard.
type wizardAnswer struct {
	TaskKey     uint64 `json:"taskKey"`
	InstanceKey uint64 `json:"instanceKey"`
	Wizard      *struct {
		State              string `json:"state"`
		ScopeKey           uint64 `json:"scopeKey"`
		ProcessInstanceKey uint64 `json:"processInstanceKey"`
		Task               *struct {
			Key            uint64 `json:"key"`
			ElementID      string `json:"elementId"`
			Assignee       string `json:"assignee"`
			WizardScopeKey uint64 `json:"wizardScopeKey"`
			Wizard         string `json:"wizard"`
		} `json:"task"`
	} `json:"wizard"`
}

func decodeWizardAnswer(t *testing.T, code int, body []byte) wizardAnswer {
	t.Helper()
	if code != http.StatusOK {
		t.Fatalf("status %d (%s)", code, body)
	}
	var a wizardAnswer
	if err := json.Unmarshal(body, &a); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	return a
}

// wantStep asserts the answer carries the next screen, elementID, of the sitting.
func wantStep(t *testing.T, a wizardAnswer, elementID string) {
	t.Helper()
	if a.Wizard == nil || a.Wizard.State != "step" || a.Wizard.Task == nil {
		t.Fatalf("want the step %s, got %+v", elementID, a.Wizard)
	}
	if got := a.Wizard.Task.ElementID; got != elementID {
		t.Fatalf("next screen = %s, want %s", got, elementID)
	}
	if a.Wizard.Task.WizardScopeKey != a.Wizard.ScopeKey || a.Wizard.Task.Wizard != "internal" {
		t.Errorf("the screen does not name its sitting: task scope %d (%q), sitting %d",
			a.Wizard.Task.WizardScopeKey, a.Wizard.Task.Wizard, a.Wizard.ScopeKey)
	}
}

func deployKey(t *testing.T, ts *httptest.Server, c *http.Client, bpmn string) uint64 {
	t.Helper()
	code, body := cReq(t, c, ts, "POST", "/api/v1/deployments", bpmn)
	if code != http.StatusOK {
		t.Fatalf("deploy: %d (%s)", code, body)
	}
	var d struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &d); err != nil {
		t.Fatalf("decode deploy: %v (%s)", err, body)
	}
	return d.Key
}

func completeTask(t *testing.T, ts *httptest.Server, c *http.Client, key uint64, vars string) wizardAnswer {
	t.Helper()
	code, body := cReq(t, c, ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/complete", key), `{"variables":`+vars+`}`)
	return decodeWizardAnswer(t, code, body)
}

// TestTheStartAndEachScreenCarryThePersonOn walks a sitting end to end: the start lands
// on its first screen, the answer on it decides the second, and the second ends the
// sitting. The task after it is ordinary work and says nothing about a wizard.
func TestTheStartAndEachScreenCarryThePersonOn(t *testing.T) {
	ts := newTestServer(t)
	c := ts.Client()
	key := deployKey(t, ts, c, wizardBPMN(""))

	code, body := cReq(t, c, ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", key), "{}")
	start := decodeWizardAnswer(t, code, body)
	wantStep(t, start, "eins")
	if start.Wizard.ProcessInstanceKey != start.InstanceKey {
		t.Errorf("sitting of instance %d, started %d", start.Wizard.ProcessInstanceKey, start.InstanceKey)
	}
	sitting := start.Wizard.ScopeKey

	second := completeTask(t, ts, c, start.Wizard.Task.Key, `{"weiter":"a"}`)
	wantStep(t, second, "zwei_a")
	if second.Wizard.ScopeKey != sitting {
		t.Errorf("the second screen is in sitting %d, the first was in %d", second.Wizard.ScopeKey, sitting)
	}

	last := completeTask(t, ts, c, second.Wizard.Task.Key, `{}`)
	if last.Wizard == nil || last.Wizard.State != "done" || last.Wizard.Task != nil {
		t.Fatalf("after the last screen: %+v, want done", last.Wizard)
	}

	// The task after the sitting is a plain task, and completing it says so by
	// saying nothing about a wizard.
	code, body = cReq(t, c, ts, "GET", "/api/v1/tasks?processInstance="+fmt.Sprint(start.InstanceKey), "")
	if code != http.StatusOK || !strings.Contains(string(body), `"nachlauf"`) || strings.Contains(string(body), "wizardScopeKey") {
		t.Fatalf("the task after the sitting: %d %s", code, body)
	}
	var page struct {
		Items []struct {
			Key uint64 `json:"key"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &page); err != nil || len(page.Items) != 1 {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if after := completeTask(t, ts, c, page.Items[0].Key, `{}`); after.Wizard != nil {
		t.Errorf("a task in no wizard answered with one: %+v", after.Wizard)
	}
}

// TestTheOtherBranchIsTheOtherScreen: the gateway between two screens routes on the
// first one's answer, and the continuation follows the route.
func TestTheOtherBranchIsTheOtherScreen(t *testing.T) {
	ts := newTestServer(t)
	c := ts.Client()
	key := deployKey(t, ts, c, wizardBPMN(""))
	code, body := cReq(t, c, ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", key), "{}")
	start := decodeWizardAnswer(t, code, body)
	wantStep(t, completeTask(t, ts, c, start.Wizard.Task.Key, `{"weiter":"b"}`), "zwei_b")
}

// TestAWorkerBetweenScreensIsPending: a step the server does not serve itself stands
// between two screens, so the sitting is live with nothing open — pending, not done.
func TestAWorkerBetweenScreensIsPending(t *testing.T) {
	ts := newTestServer(t)
	c := ts.Client()
	key := deployKey(t, ts, c, wizardBPMN(`
      <userTask id="eins"/>
      <serviceTask id="pruefen"><extensionElements><zeebe:taskDefinition type="extern"/></extensionElements></serviceTask>
      <userTask id="zwei"/>
      <endEvent id="we"/>
      <sequenceFlow id="w1" sourceRef="ws" targetRef="eins"/>
      <sequenceFlow id="w2" sourceRef="eins" targetRef="pruefen"/>
      <sequenceFlow id="w3" sourceRef="pruefen" targetRef="zwei"/>
      <sequenceFlow id="w4" sourceRef="zwei" targetRef="we"/>`))
	code, body := cReq(t, c, ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", key), "{}")
	start := decodeWizardAnswer(t, code, body)
	wantStep(t, start, "eins")

	next := completeTask(t, ts, c, start.Wizard.Task.Key, `{}`)
	if next.Wizard == nil || next.Wizard.State != "pending" || next.Wizard.Task != nil {
		t.Fatalf("with a worker between the screens: %+v, want pending", next.Wizard)
	}
	if next.Wizard.ScopeKey != start.Wizard.ScopeKey {
		t.Errorf("pending names sitting %d, the sitting is %d", next.Wizard.ScopeKey, start.Wizard.ScopeKey)
	}
}

// TestAStartIntoNoWizardSaysNothingAboutOne: a process with no wizard answers its start
// exactly as before.
func TestAStartIntoNoWizardSaysNothingAboutOne(t *testing.T) {
	ts := newTestServer(t)
	c := ts.Client()
	key := deployKey(t, ts, c, `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="plain" isExecutable="true">
    <startEvent id="s"/><userTask id="t"/><endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/><sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </process>
</definitions>`)
	code, body := cReq(t, c, ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", key), "{}")
	if a := decodeWizardAnswer(t, code, body); a.Wizard != nil || a.InstanceKey == 0 {
		t.Errorf("start of a process with no wizard: %s", body)
	}
}

// signedInWizardBPMN puts an ordinary task before the sitting, so the person who
// fills the sitting is not the one who started the instance, and addresses the
// sitting's last screen to a group that person is not in.
const signedInWizardBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:atlas="http://atlas/schema/1.0"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="antrag" isExecutable="true">
    <startEvent id="s"/>
    <userTask id="vorab"/>
    <subProcess id="sitting" atlas:wizard="internal">
      <startEvent id="ws"/>
      <userTask id="eins"/>
      <userTask id="zwei"/>
      <userTask id="pruefung"><extensionElements><zeebe:assignmentDefinition candidateGroups="backoffice"/></extensionElements></userTask>
      <endEvent id="we"/>
      <sequenceFlow id="w1" sourceRef="ws" targetRef="eins"/>
      <sequenceFlow id="w2" sourceRef="eins" targetRef="zwei"/>
      <sequenceFlow id="w3" sourceRef="zwei" targetRef="pruefung"/>
      <sequenceFlow id="w4" sourceRef="pruefung" targetRef="we"/>
    </subProcess>
    <boundaryEvent id="abandoned" attachedToRef="sitting"><timerEventDefinition><timeDuration>PT30M</timeDuration></timerEventDefinition></boundaryEvent>
    <endEvent id="ea"/>
    <endEvent id="e"/>
    <sequenceFlow id="f0" sourceRef="s" targetRef="vorab"/>
    <sequenceFlow id="f1" sourceRef="vorab" targetRef="sitting"/>
    <sequenceFlow id="f2" sourceRef="sitting" targetRef="e"/>
    <sequenceFlow id="fa" sourceRef="abandoned" targetRef="ea"/>
  </process>
</definitions>`

// TestTheNextScreenIsClaimedForThePersonAndPassesOnWhenItIsNotTheirs: signed in, the
// next screen is claimed for whoever submitted the last one, so it leaves every other
// inbox; a screen addressed to a group they are not in is handed over, and the answer
// does not show it.
func TestTheNextScreenIsClaimedForThePersonAndPassesOnWhenItIsNotTheirs(t *testing.T) {
	ts, _ := newAuthServer(t, "root", "rootpassword")
	admin := newClient(t)
	if login(t, admin, ts, "root", "rootpassword") != http.StatusOK {
		t.Fatal("admin login failed")
	}
	clients := twoUsers(t, ts, admin, "alice", "mallory")
	alice, mallory := clients[0], clients[1]

	key := deployKey(t, ts, admin, signedInWizardBPMN)
	code, body := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", key), "{}")
	if a := decodeWizardAnswer(t, code, body); a.Wizard != nil {
		t.Fatalf("the start waits outside the sitting, yet answered with one: %+v", a.Wizard)
	}

	// openTask finds the one open task called elementID in alice's inbox.
	openTask := func(c *http.Client, elementID string) (uint64, bool) {
		t.Helper()
		code, body := cReq(t, c, ts, "GET", "/api/v1/tasks", "")
		if code != http.StatusOK {
			t.Fatalf("list tasks: %d (%s)", code, body)
		}
		var page struct {
			Items []struct {
				Key       uint64 `json:"key"`
				ElementID string `json:"elementId"`
			} `json:"items"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			t.Fatalf("decode tasks: %v (%s)", err, body)
		}
		for _, it := range page.Items {
			if it.ElementID == elementID {
				return it.Key, true
			}
		}
		return 0, false
	}

	vorab, ok := openTask(alice, "vorab")
	if !ok {
		t.Fatal("the task before the sitting is not in alice's inbox")
	}
	if a := completeTask(t, ts, alice, vorab, `{}`); a.Wizard != nil {
		t.Fatalf("a task outside the sitting answered with one: %+v", a.Wizard)
	}

	// The first screen was reached by a task outside the sitting, so nobody was
	// carried to it: it is open work, and alice picks it up from the inbox.
	eins, ok := openTask(alice, "eins")
	if !ok {
		t.Fatal("the sitting's first screen is not in alice's inbox")
	}
	next := completeTask(t, ts, alice, eins, `{}`)
	wantStep(t, next, "zwei")
	if next.Wizard.Task.Assignee != "alice" {
		t.Errorf("the next screen is held by %q, want alice", next.Wizard.Task.Assignee)
	}
	if _, seen := openTask(mallory, "zwei"); seen {
		t.Error("alice's next screen is in mallory's inbox")
	}

	// The last screen is the backoffice's: handed over, and not shown.
	last := completeTask(t, ts, alice, next.Wizard.Task.Key, `{}`)
	if last.Wizard == nil || last.Wizard.State != "handedOver" || last.Wizard.Task != nil {
		t.Fatalf("a screen addressed to another group: %+v, want handedOver without the task", last.Wizard)
	}
}
