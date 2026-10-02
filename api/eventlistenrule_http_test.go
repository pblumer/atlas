package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Who may listen to atlas's own events (ADR-0435 §6).
//
// atlas.user.requested carries a requester's name and address to every model with a
// signal start on it. Deploying such a listener needs an administrator; an event
// without personal data, a signal atlas does not catalogue, and a throw stay open.
// Every door a caller deploys through runs the same check, and the Problems panel
// reports it before anybody tries.

// signalBPMN is a process whose first element does something with a signal: a start
// on it ("start") or a throw of it ("throw").
func signalBPMN(processID, signalName, how string) string {
	first := `<bpmn:startEvent id="Start_sig"><bpmn:signalEventDefinition signalRef="Sig"/></bpmn:startEvent>`
	if how == "throw" {
		first = `<bpmn:startEvent id="Start_sig"/><bpmn:intermediateThrowEvent id="Throw_sig"><bpmn:signalEventDefinition signalRef="Sig"/></bpmn:intermediateThrowEvent>`
	}
	flows := `<bpmn:sequenceFlow id="F1" sourceRef="Start_sig" targetRef="End_1"/>`
	if how == "throw" {
		flows = `<bpmn:sequenceFlow id="F0" sourceRef="Start_sig" targetRef="Throw_sig"/>` +
			`<bpmn:sequenceFlow id="F1" sourceRef="Throw_sig" targetRef="End_1"/>`
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" targetNamespace="http://atlas.test">
  <bpmn:signal id="Sig" name="` + signalName + `"/>
  <bpmn:process id="` + processID + `" isExecutable="true">
    ` + first + `
    <bpmn:endEvent id="End_1"/>
    ` + flows + `
  </bpmn:process>
</bpmn:definitions>`
}

const requested = "atlas.user.requested"

func TestAModelerCannotDeployAListenerOnPersonalData(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "bert")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")

	status, body := deployAs(t, bert, ts.URL, signalBPMN("bertHoert", requested, "start"))
	if status != http.StatusForbidden {
		t.Fatalf("= %d, want 403: a modeler deployed a listener on a requester's data\n%s", status, body)
	}
	var refusal struct {
		Error     string `json:"error"`
		Needs     string `json:"needs"`
		Roles     []string
		Listeners []struct {
			ElementID string   `json:"elementId"`
			Event     string   `json:"event"`
			Role      string   `json:"role"`
			Personal  []string `json:"personal"`
		}
	}
	if err := json.Unmarshal([]byte(body), &refusal); err != nil {
		t.Fatalf("decode refusal: %v\n%s", err, body)
	}
	// The error is the whole answer, because an MCP client keeps nothing else: the
	// element, the event, the data, the role it needs and the role the caller has.
	for _, want := range []string{"Start_sig", requested, "vorname", "email", "needs the admin role", "you have modeler, operator, user"} {
		if !strings.Contains(refusal.Error, want) {
			t.Errorf("the refusal does not say %q: %s", want, refusal.Error)
		}
	}
	if refusal.Needs != "admin" || len(refusal.Listeners) != 1 || refusal.Listeners[0].ElementID != "Start_sig" ||
		refusal.Listeners[0].Role != "start" || len(refusal.Listeners[0].Personal) == 0 {
		t.Errorf("the refusal's fields: %+v", refusal)
	}
	if deployedProcessIDs(t, admin, ts.URL)["bertHoert"] {
		t.Fatal("the refused definition exists anyway")
	}

	t.Run("an administrator may", func(t *testing.T) {
		if status, body := deployAs(t, admin, ts.URL, signalBPMN("rootHoert", requested, "start")); status != http.StatusOK {
			t.Errorf("= %d, want 200\n%s", status, body)
		}
	})
	t.Run("a signal atlas does not catalogue is outside the rule", func(t *testing.T) {
		if status, body := deployAs(t, bert, ts.URL, signalBPMN("bertWartet", "atlas.user.vanished", "start")); status != http.StatusOK {
			t.Errorf("= %d, want 200: nothing declares what such a signal carries\n%s", status, body)
		}
	})
	t.Run("a throw does not listen", func(t *testing.T) {
		if status, body := deployAs(t, bert, ts.URL, signalBPMN("bertWirft", requested, "throw")); status != http.StatusOK {
			t.Errorf("= %d, want 200: throwing a name receives nothing\n%s", status, body)
		}
	})
}

// A bundle is "validate all, then deploy all": one draft that listens to personal data
// deploys none of the application.
func TestTheProjectDeployAppliesTheListenerRuleToEveryDraft(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "bert")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")

	pid := createProjectAs(t, bert, ts.URL, "Berts Anwendung")
	saveDraftAs(t, bert, ts.URL, pid, signalBPMN("bertHarmlos", "kunde.angelegt", "start"))
	saveDraftAs(t, bert, ts.URL, pid, signalBPMN("bertImProjekt", requested, "start"))
	status, body := postAs(t, bert, ts.URL+"/api/v1/projects/"+pid+"/deploy", "")
	if status != http.StatusForbidden || !strings.Contains(body, "bertImProjekt") || !strings.Contains(body, "needs the admin role") {
		t.Fatalf("project deploy = %d, want 403 naming the draft and the role\n%s", status, body)
	}
	deployed := deployedProcessIDs(t, admin, ts.URL)
	if deployed["bertImProjekt"] || deployed["bertHarmlos"] {
		t.Fatalf("a refused bundle deployed part of itself: %v", deployed)
	}
}

// An import is a deploy, and its credential — a deploy token, an API token — is never
// an administrator's: a listener on personal data is deployed on a server by its
// administrator, not shipped to it.
func TestTheApplicationImportAppliesTheListenerRule(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "bert")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")

	raw, _ := json.Marshal(map[string]any{
		"application": "Importiert",
		"release":     map[string]any{"version": 1},
		"artifacts":   []any{map[string]any{"kind": "process", "processId": "importHoert", "xml": signalBPMN("importHoert", requested, "start")}},
	})
	status, body := postAs(t, bert, ts.URL+"/api/v1/applications/import", string(raw))
	if status != http.StatusForbidden || !strings.Contains(body, requested) {
		t.Fatalf("import = %d, want 403\n%s", status, body)
	}
	if deployedProcessIDs(t, admin, ts.URL)["importHoert"] {
		t.Fatal("the refused import was deployed anyway")
	}
}

// validateAs posts a model to the Problems panel's validation as one caller.
func validateAs(t *testing.T, c *http.Client, base, xml string) []map[string]string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/validate", strings.NewReader(xml))
	if err != nil {
		t.Fatalf("build validate: %v", err)
	}
	req.Header.Set("Content-Type", "application/xml")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("validate = %d: %s", resp.StatusCode, raw)
	}
	var out struct {
		Problems []map[string]string `json:"problems"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode validate: %v\n%s", err, raw)
	}
	return out.Problems
}

// The panel reports the deploy's own finding, from the same check, before anybody
// deploys; it depends on who asks, and says so.
func TestTheProblemsPanelReportsTheListenerRuleForTheCaller(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "bert")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")
	model := signalBPMN("bertEntwurf", requested, "start")

	var found map[string]string
	for _, p := range validateAs(t, bert, ts.URL, model) {
		if p["rule"] == "event.personal-listener" {
			found = p
		}
	}
	if found == nil {
		t.Fatal("a modeler's panel does not report the listener rule")
	}
	if found["element"] != "Start_sig" || found["severity"] != "error" ||
		!strings.Contains(found["message"], "needs the admin role; you have modeler, operator, user") {
		t.Errorf("the finding: %v", found)
	}
	// The deploy refuses with the same sentence the panel shows.
	_, body := deployAs(t, bert, ts.URL, model)
	if !strings.Contains(body, found["message"]) {
		t.Errorf("the deploy and the panel disagree:\npanel:  %s\ndeploy: %s", found["message"], body)
	}
	for _, p := range validateAs(t, admin, ts.URL, model) {
		if p["rule"] == "event.personal-listener" {
			t.Errorf("an administrator's panel reports the rule: %v", p)
		}
	}
}

func TestAnOpenServerAppliesNoListenerRule(t *testing.T) {
	ts := newOpenConnectorServer(t)
	if status, body := deployAs(t, &http.Client{}, ts.URL, signalBPMN("offen", requested, "start")); status != http.StatusOK {
		t.Errorf("auth off = %d, want 200: an open server is open by declaration\n%s", status, body)
	}
}
