package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// publish deploys the start-form process, creates its form, and mints a public
// link, returning the link token. Shared by the public-endpoint tests.
func publish(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	// The start form the process binds must exist in the form store.
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/forms",
		`{"id":"onboarding-form","name":"Onboarding","schema":{"type":"default","components":[{"type":"textfield","key":"customer","label":"Customer"}]}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("save form: %d %s", code, body)
	}
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/deployments", startFormBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: %d %s", code, body)
	}
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/public-links", `{"processId":"onboard"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("create link: %d %s", code, body)
	}
	var link struct {
		Token, URL, FormID string
	}
	if err := json.Unmarshal(body, &link); err != nil {
		t.Fatalf("decode link: %v (%s)", err, body)
	}
	if link.Token == "" || link.URL != "/public/forms/"+link.Token || link.FormID != "onboarding-form" {
		t.Fatalf("unexpected link: %+v", link)
	}
	return link.Token
}

func TestPublicLinkFlow(t *testing.T) {
	ts := newTestServer(t)
	token := publish(t, ts)

	// The public schema endpoint serves the bound form, unauthenticated.
	code, body := doReq(t, ts, http.MethodGet, "/public/forms/"+token+"/schema", "", "")
	if code != http.StatusOK {
		t.Fatalf("public schema: %d %s", code, body)
	}
	var sch struct {
		ProcessName string `json:"processName"`
		Schema      struct {
			Components []struct {
				Key string `json:"key"`
			} `json:"components"`
		} `json:"schema"`
	}
	if err := json.Unmarshal(body, &sch); err != nil {
		t.Fatalf("decode schema: %v (%s)", err, body)
	}
	if len(sch.Schema.Components) != 1 || sch.Schema.Components[0].Key != "customer" {
		t.Fatalf("schema not served: %s", body)
	}

	// A public start creates an instance seeded with the submitted variables.
	code, body = doReq(t, ts, http.MethodPost, "/public/forms/"+token+"/start", `{"variables":{"customer":"Acme"}}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("public start: %d %s", code, body)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: %d %s", code, body)
	}
	if !bytes.Contains(body, []byte(`"customer"`)) || !bytes.Contains(body, []byte("Acme")) {
		t.Fatalf("started instance lacks the submitted variable: %s", body)
	}

	// The public page renders as HTML.
	code, body = doReq(t, ts, http.MethodGet, "/public/forms/"+token, "", "")
	if code != http.StatusOK || !bytes.Contains(body, []byte("form-viewer.js")) {
		t.Fatalf("public page: %d %s", code, body)
	}

	// The trusted list endpoint returns the link.
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/public-links", "", "")
	if code != http.StatusOK {
		t.Fatalf("list links: %d %s", code, body)
	}
	var links []struct{ Token string }
	if err := json.Unmarshal(body, &links); err != nil {
		t.Fatalf("decode list: %v (%s)", err, body)
	}
	if len(links) != 1 || links[0].Token != token {
		t.Fatalf("list = %+v, want the one link %s", links, token)
	}

	// Publishing again is idempotent — same token.
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/public-links", `{"processId":"onboard"}`, "application/json")
	var again struct{ Token string }
	_ = json.Unmarshal(body, &again)
	if code != http.StatusOK || again.Token != token {
		t.Fatalf("publish not idempotent: %d token=%s want %s", code, again.Token, token)
	}

	// Revoking kills the link: schema and start now 404.
	code, _ = doReq(t, ts, http.MethodDelete, "/api/v1/public-links/"+token, "", "")
	if code != http.StatusOK {
		t.Fatalf("revoke: %d", code)
	}
	if code, _ := doReq(t, ts, http.MethodGet, "/public/forms/"+token+"/schema", "", ""); code != http.StatusNotFound {
		t.Errorf("schema after revoke: %d, want 404", code)
	}
	if code, _ := doReq(t, ts, http.MethodPost, "/public/forms/"+token+"/start", `{}`, "application/json"); code != http.StatusNotFound {
		t.Errorf("start after revoke: %d, want 404", code)
	}
}

func TestCreatePublicLinkErrors(t *testing.T) {
	ts := newTestServer(t)
	// Unknown process → 404.
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/public-links", `{"processId":"nope"}`, "application/json"); code != http.StatusNotFound {
		t.Errorf("unknown process: %d, want 404", code)
	}
	// Missing processId → 400.
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/public-links", `{}`, "application/json"); code != http.StatusBadRequest {
		t.Errorf("missing processId: %d, want 400", code)
	}
	// A process with no start form → 400.
	noForm := `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"><process id="plain" isExecutable="true"><startEvent id="s"/><endEvent id="e"/><sequenceFlow id="f" sourceRef="s" targetRef="e"/></process></definitions>`
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", noForm, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy plain: %d %s", code, body)
	}
	if code, _ := doReq(t, ts, http.MethodPost, "/api/v1/public-links", `{"processId":"plain"}`, "application/json"); code != http.StatusBadRequest {
		t.Errorf("no start form: %d, want 400", code)
	}
}

func TestPublicUnknownToken(t *testing.T) {
	ts := newTestServer(t)
	for _, path := range []string{"/public/forms/deadbeef/schema", "/public/forms/deadbeef"} {
		if code, _ := doReq(t, ts, http.MethodGet, path, "", ""); code == http.StatusOK && path != "/public/forms/deadbeef" {
			t.Errorf("%s: unexpectedly ok", path)
		}
	}
	if code, _ := doReq(t, ts, http.MethodGet, "/public/forms/deadbeef/schema", "", ""); code != http.StatusNotFound {
		t.Errorf("unknown token schema: %d, want 404", code)
	}
	if code, _ := doReq(t, ts, http.MethodPost, "/public/forms/deadbeef/start", `{}`, "application/json"); code != http.StatusNotFound {
		t.Errorf("unknown token start: %d, want 404", code)
	}
}

// TestPublicStartRefusesAFieldTheFormLacks is ADR-0029's promise, end to end: the
// anonymous start accepts only what the link's form has fields for, and a refused
// submission starts nothing.
func TestPublicStartRefusesAFieldTheFormLacks(t *testing.T) {
	ts := newTestServer(t)
	token := publish(t, ts)

	code, body := doReq(t, ts, http.MethodPost, "/public/forms/"+token+"/start",
		`{"variables":{"customer":"Acme","approved":true}}`, "application/json")
	if code != http.StatusBadRequest {
		t.Fatalf("a variable the form has no field for: %d %s, want 400", code, body)
	}
	if !bytes.Contains(body, []byte("approved")) || bytes.Contains(body, []byte("customer")) {
		t.Errorf("the refusal should name the extra field and only it: %s", body)
	}
	code, body = doReq(t, ts, http.MethodGet, "/api/v1/instances", "", "")
	if code != http.StatusOK {
		t.Fatalf("list instances: %d %s", code, body)
	}
	if bytes.Contains(body, []byte("Acme")) {
		t.Fatalf("a refused submission started an instance: %s", body)
	}
}

// publicWizardBPMN is a start form followed by a public wizard (ADR-0449). boundary is
// what is attached to the wizard: the interrupting timer that ends an abandoned sitting,
// or nothing.
func publicWizardBPMN(boundary string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:atlas="http://atlas/schema/1.0"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="intake" isExecutable="true">
    <startEvent id="s">
      <extensionElements><zeebe:formDefinition formId="onboarding-form"/></extensionElements>
    </startEvent>
    <subProcess id="sitting" atlas:wizard="public">
      <startEvent id="ws"/>
      <userTask id="details"/>
      <endEvent id="we"/>
      <sequenceFlow id="w1" sourceRef="ws" targetRef="details"/>
      <sequenceFlow id="w2" sourceRef="details" targetRef="we"/>
    </subProcess>
    ` + boundary + `
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="sitting"/>
    <sequenceFlow id="f2" sourceRef="sitting" targetRef="e"/>
  </process>
</definitions>`
}

const wizardTimeout = `<boundaryEvent id="abandoned" attachedToRef="sitting"><timerEventDefinition><timeDuration>PT30M</timeDuration></timerEventDefinition></boundaryEvent>
    <endEvent id="ea"/>
    <sequenceFlow id="fa" sourceRef="abandoned" targetRef="ea"/>`

// TestAPublicWizardNeedsATimeoutToBePublished: a start link is the door anonymous
// visitors come through, so a public wizard nothing bounds would keep every sitting they
// abandon as a live instance. Publishing refuses it, and so does a link minted before a
// redeploy introduced it (ADR-0449).
func TestAPublicWizardNeedsATimeoutToBePublished(t *testing.T) {
	ts := newTestServer(t)
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/forms",
		`{"id":"onboarding-form","name":"Onboarding","schema":{"type":"default","components":[{"type":"textfield","key":"customer","label":"Customer"}]}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("save form: %d %s", code, body)
	}
	deploy := func(boundary string) {
		t.Helper()
		if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", publicWizardBPMN(boundary), "application/xml"); code != http.StatusOK {
			t.Fatalf("deploy: %d %s", code, body)
		}
	}

	deploy("")
	code, body := doReq(t, ts, http.MethodPost, "/api/v1/public-links", `{"processId":"intake"}`, "application/json")
	if code != http.StatusConflict || !bytes.Contains(body, []byte("sitting")) {
		t.Fatalf("publishing an untimed public wizard: %d %s, want 409 naming the wizard", code, body)
	}

	deploy(wizardTimeout)
	code, body = doReq(t, ts, http.MethodPost, "/api/v1/public-links", `{"processId":"intake"}`, "application/json")
	if code != http.StatusOK {
		t.Fatalf("publishing a timed public wizard: %d %s", code, body)
	}
	var link struct{ Token string }
	if err := json.Unmarshal(body, &link); err != nil || link.Token == "" {
		t.Fatalf("decode link: %v (%s)", err, body)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/public/forms/"+link.Token+"/start", `{"variables":{"customer":"Acme"}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("start through the link: %d %s", code, body)
	}

	deploy("")
	if code, body := doReq(t, ts, http.MethodPost, "/public/forms/"+link.Token+"/start", `{"variables":{"customer":"Acme"}}`, "application/json"); code != http.StatusConflict {
		t.Errorf("start after a redeploy dropped the timeout: %d %s, want 409", code, body)
	}
}
