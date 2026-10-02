package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// Who may listen to a catalogued event (ADR-0435 §6).
//
// A signal hands its listener every variable of the throwing instance. Before this
// rule, anybody who could deploy could receive an intake requester's name, mail
// address and reason by drawing one signal start. These tests are the doors: every
// deploy path refuses a modeler's listener on an event that carries personal data,
// an administrator deploys it, a signal outside the catalogue stays open, and the
// Problems panel reports the same finding before the deploy.

// listenerBPMN waits for signal name in the given way: "start", "catch" or "boundary".
func listenerBPMN(processID, name, how string) string {
	head := `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" targetNamespace="http://atlas.test">
  <bpmn:signal id="Sig" name="` + name + `"/>
  <bpmn:process id="` + processID + `" isExecutable="true">
`
	var body string
	switch how {
	case "start":
		body = `    <bpmn:startEvent id="Hoeren"><bpmn:signalEventDefinition signalRef="Sig"/></bpmn:startEvent>
    <bpmn:endEvent id="Ende"/>
    <bpmn:sequenceFlow id="F1" sourceRef="Hoeren" targetRef="Ende"/>
`
	case "catch":
		body = `    <bpmn:startEvent id="Start"/>
    <bpmn:intermediateCatchEvent id="Hoeren"><bpmn:signalEventDefinition signalRef="Sig"/></bpmn:intermediateCatchEvent>
    <bpmn:endEvent id="Ende"/>
    <bpmn:sequenceFlow id="F1" sourceRef="Start" targetRef="Hoeren"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Hoeren" targetRef="Ende"/>
`
	case "boundary":
		body = `    <bpmn:startEvent id="Start"/>
    <bpmn:userTask id="Warten"/>
    <bpmn:boundaryEvent id="Hoeren" attachedToRef="Warten" cancelActivity="false"><bpmn:signalEventDefinition signalRef="Sig"/></bpmn:boundaryEvent>
    <bpmn:endEvent id="Ende"/>
    <bpmn:endEvent id="Gehoert"/>
    <bpmn:sequenceFlow id="F1" sourceRef="Start" targetRef="Warten"/>
    <bpmn:sequenceFlow id="F2" sourceRef="Warten" targetRef="Ende"/>
    <bpmn:sequenceFlow id="F3" sourceRef="Hoeren" targetRef="Gehoert"/>
`
	}
	return head + body + `  </bpmn:process>
</bpmn:definitions>`
}

func TestAModelerCannotDeployAListenerOnPersonalData(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "bert")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")

	for _, how := range []string{"start", "catch", "boundary"} {
		t.Run(how, func(t *testing.T) {
			pid := "bertHoert_" + how
			status, body := deployAs(t, bert, ts.URL, listenerBPMN(pid, "atlas.user.requested", how))
			if status != http.StatusForbidden {
				t.Fatalf("= %d, want 403: a modeler deployed a listener on a requester's data\n%s", status, body)
			}
			var refusal map[string]any
			_ = json.Unmarshal([]byte(body), &refusal)
			if refusal["elementId"] != "Hoeren" || refusal["event"] != "atlas.user.requested" || refusal["requiredRole"] != "admin" {
				t.Errorf("the refusal must name the element, the event and the role it needs: %s", body)
			}
			if fields, _ := refusal["personalFields"].([]any); !containsAny(fields, "email") {
				t.Errorf("the refusal must name the personal data the listener would receive: %s", body)
			}
			if roles, _ := refusal["callerRoles"].(string); !strings.Contains(roles, "modeler") {
				t.Errorf("the refusal must name the caller's roles: %s", body)
			}
			if deployedProcessIDs(t, admin, ts.URL)[pid] {
				t.Fatal("the refused definition exists anyway")
			}
		})
	}

	t.Run("an administrator deploys it", func(t *testing.T) {
		if status, body := deployAs(t, admin, ts.URL, listenerBPMN("adminHoert", "atlas.user.requested", "start")); status != http.StatusOK {
			t.Fatalf("= %d, want 200\n%s", status, body)
		}
	})
	t.Run("a signal outside the catalogue stays open", func(t *testing.T) {
		if status, body := deployAs(t, bert, ts.URL, listenerBPMN("bertEigenes", "bert.eigenes-signal", "start")); status != http.StatusOK {
			t.Fatalf("= %d, want 200: the rule reads the catalogue and nothing else\n%s", status, body)
		}
	})
	t.Run("throwing a catalogued name is not listening", func(t *testing.T) {
		thrower := strings.Replace(listenerBPMN("bertWirft", "atlas.user.requested", "catch"),
			"bpmn:intermediateCatchEvent", "bpmn:intermediateThrowEvent", 2)
		if status, body := deployAs(t, bert, ts.URL, thrower); status != http.StatusOK {
			t.Fatalf("= %d, want 200\n%s", status, body)
		}
	})
}

func containsAny(list []any, want string) bool {
	for _, v := range list {
		if s, _ := v.(string); s == want {
			return true
		}
	}
	return false
}

func TestTheProjectDeployChecksTheListenerToo(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "bert")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")

	pid := createProjectAs(t, bert, ts.URL, "Berts Anwendung")
	saveDraftAs(t, bert, ts.URL, pid, listenerBPMN("bertImProjekt", "atlas.approval.requested", "start"))
	status, body := postAs(t, bert, ts.URL+"/api/v1/projects/"+pid+"/deploy", "")
	if status != http.StatusForbidden || !strings.Contains(body, "atlas.approval.requested") {
		t.Fatalf("project deploy = %d, want 403 naming the event\n%s", status, body)
	}
	if deployedProcessIDs(t, admin, ts.URL)["bertImProjekt"] {
		t.Fatal("the refused draft was deployed anyway")
	}
}

func TestTheApplicationImportChecksTheListenerToo(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "bert")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")

	raw, _ := json.Marshal(map[string]any{
		"application": "Importierter Zuhoerer",
		"release":     map[string]any{"version": 1},
		"artifacts": []any{map[string]any{"kind": "process", "processId": "importHoert",
			"xml": listenerBPMN("importHoert", "atlas.user.requested", "start")}},
	})
	status, body := postAs(t, bert, ts.URL+"/api/v1/applications/import", string(raw))
	if status != http.StatusForbidden || !strings.Contains(body, "atlas.user.requested") {
		t.Fatalf("import of a listener = %d, want 403\n%s", status, body)
	}
	if deployedProcessIDs(t, admin, ts.URL)["importHoert"] {
		t.Fatal("the refused import was deployed anyway")
	}
}

// The Problems panel reports what the deploy would refuse, and for whom: the same
// draft carries an error for a modeler and none for an administrator.
func TestValidationReportsTheListenerRuleForTheCaller(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "bert")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")
	xml := listenerBPMN("entwurf", "atlas.user.requested", "start")

	validate := func(c *http.Client) []map[string]any {
		t.Helper()
		resp, err := c.Post(ts.URL+"/api/v1/validate", "application/xml", strings.NewReader(xml))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("validate = %d: %s", resp.StatusCode, raw)
		}
		var out struct {
			Problems []map[string]any `json:"problems"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode: %v (%s)", err, raw)
		}
		var found []map[string]any
		for _, p := range out.Problems {
			if p["rule"] == "signal.listener-access" {
				found = append(found, p)
			}
		}
		return found
	}

	got := validate(bert)
	if len(got) != 1 || got[0]["element"] != "Hoeren" || got[0]["severity"] != "error" {
		t.Fatalf("a modeler's draft = %v, want one error on Hoeren", got)
	}
	msg, _ := got[0]["message"].(string)
	for _, want := range []string{"atlas.user.requested", "email", "admin", "modeler"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the finding must name %q: %s", want, msg)
		}
	}
	if got := validate(admin); len(got) != 0 {
		t.Errorf("an administrator's draft = %v, want no finding", got)
	}
}

func TestAnOpenServerChecksNoListener(t *testing.T) {
	ts := newOpenConnectorServer(t)
	if status, body := deployAs(t, &http.Client{}, ts.URL, listenerBPMN("offen", "atlas.user.requested", "start")); status != http.StatusOK {
		t.Errorf("auth off = %d, want 200: an open server is open by declaration\n%s", status, body)
	}
}
