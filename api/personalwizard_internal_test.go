package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/vault"
)

// A wizard's personal answers, end to end over the real HTTP surface and the real vault
// (ADR-0314, amended; ADR-0449). The screen's answer is sealed at the task door and lands
// in the wizard's scope; the wizard hands it out under its own name, which the engine does
// by copying the sealed record; the step after the wizard reads it as plaintext at the edge;
// and one erasure makes the copy as unreadable as the original.

// personalWizardBPMN is an internal wizard of one screen collecting iban, declared personal
// under the subject personalnummer and handed out unchanged, followed by a service task a
// worker serves.
const personalWizardBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:atlas="http://atlas/schema/1.0"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="personalbogen" isExecutable="true" atlas:personal="iban" atlas:dataSubject="personalnummer">
    <startEvent id="start"/>
    <subProcess id="sitting" atlas:wizard="internal">
      <extensionElements><zeebe:ioMapping><zeebe:output source="=iban" target="iban"/></zeebe:ioMapping></extensionElements>
      <startEvent id="ws"/>
      <userTask id="bank"/>
      <endEvent id="we"/>
      <sequenceFlow id="w1" sourceRef="ws" targetRef="bank"/>
      <sequenceFlow id="w2" sourceRef="bank" targetRef="we"/>
    </subProcess>
    <boundaryEvent id="abandoned" attachedToRef="sitting"><timerEventDefinition><timeDuration>PT30M</timeDuration></timerEventDefinition></boundaryEvent>
    <endEvent id="ea"/>
    <serviceTask id="provision">
      <extensionElements><zeebe:taskDefinition type="provision" retries="5"/></extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="sitting"/>
    <sequenceFlow id="f2" sourceRef="sitting" targetRef="provision"/>
    <sequenceFlow id="f3" sourceRef="provision" targetRef="end"/>
    <sequenceFlow id="fa" sourceRef="abandoned" targetRef="ea"/>
  </process>
</definitions>`

const testIBAN = "CH93-0076-2011-6238-5295-7"

func TestAWizardHandsItsSealedAnswerOutAndAnErasureCoversTheCopy(t *testing.T) {
	srv := newServerForErrors(t)
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", personalWizardBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("a wizard handing its personal answer out under its own name was refused: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	start := fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key)
	if code, body = serveInternal(t, srv, http.MethodPost, start, `{"variables":{"personalnummer":"P-4711"}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}

	// The screen's answer is sealed at the door.
	taskKey, instKey := openUserTask(t, srv)
	complete := fmt.Sprintf("/api/v1/tasks/%d/complete", taskKey)
	if code, body = serveInternal(t, srv, http.MethodPost, complete, `{"variables":{"iban":"`+testIBAN+`"}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("complete the screen: status=%d body=%s", code, body)
	}

	// The wizard handed it out: at the root, sealed, for the instance's subject, and with
	// nothing readable in the stored bytes.
	stored := rootVar(t, srv, instKey, "iban")
	if !vault.IsEnciphered(stored.Text) {
		t.Fatalf("the handed-out iban is stored as kind=%d %q, not an envelope", stored.Kind, stored.Text)
	}
	if strings.Contains(stored.Text, testIBAN) {
		t.Errorf("the stored copy carries the plaintext: %s", stored.Text)
	}
	if env, _ := vault.ParseEnvelope(stored.Text); env.Subject != "P-4711" {
		t.Errorf("the copy is sealed for %q, want P-4711", env.Subject)
	}

	// The step after the wizard reads it in the clear at the edge, as any personal value.
	jobs := activateProvisionJobs(t, srv)
	if len(jobs) != 1 {
		t.Fatalf("activated %d provision jobs, want 1", len(jobs))
	}
	if got := jobs[0].Variables["iban"]; got != testIBAN {
		t.Fatalf("the worker after the wizard got iban = %#v, want the plaintext", got)
	}
	jobKey := jobs[0].JobKey
	if code, body = serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/fail", jobKey),
		`{"retries":4,"errorMessage":"retry after the erasure"}`, "application/json"); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("hand the job back: status=%d body=%s", code, body)
	}

	// One erasure covers the copy: the same ciphertext under the same subject's key.
	if code, body = serveInternal(t, srv, http.MethodDelete, "/api/v1/personal-data/P-4711", "", ""); code != http.StatusOK {
		t.Fatalf("erase: status=%d body=%s", code, body)
	}
	if after := rootVar(t, srv, instKey, "iban"); after.Text != stored.Text {
		t.Errorf("the erasure rewrote the stored copy: %q → %q", stored.Text, after.Text)
	}
	for _, j := range activateProvisionJobs(t, srv) {
		if j.JobKey == jobKey {
			t.Errorf("after the erasure the job was still handed out with %#v", j.Variables)
		}
	}
	code, body = serveInternal(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/variables", instKey), "", "")
	if code != http.StatusConflict || !strings.Contains(string(body), "erased") {
		t.Errorf("reading the erased copy: status=%d body=%s, want 409 saying erased", code, body)
	}
}
