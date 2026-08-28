package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

// The claim on a message name (ADR-0205, measure M11 step two).
//
// Step one gave a connector an owner, and stopped there: a stranger could no
// longer *configure* somebody's inbound connector, but could still deploy a
// process whose message-start event named the same message and receive its events.
// The message name was the whole key, and the name is not a secret.
//
// So an inbound subscription claims its message name, and the claim is checked at
// the two design-time doors where a definition and a subscription meet. These
// tests are those two doors, plus the two things the claim must not do: reach into
// a running instance, or stop somebody working inside their own scope.

// messageStartBPMN is a one-node process started by a message. The claim is about which
// names a definition can be *delivered* on, so a message start event is the
// smallest thing that carries one.
func messageStartBPMN(processID, messageName string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
                  xmlns:dc="http://www.omg.org/spec/DD/20100524/DC"
                  targetNamespace="http://atlas.test">
  <bpmn:message id="Msg_1" name="` + messageName + `" />
  <bpmn:process id="` + processID + `" isExecutable="true">
    <bpmn:startEvent id="Start_1">
      <bpmn:messageEventDefinition id="MsgDef_1" messageRef="Msg_1" />
      <bpmn:outgoing>Flow_1</bpmn:outgoing>
    </bpmn:startEvent>
    <bpmn:endEvent id="End_1"><bpmn:incoming>Flow_1</bpmn:incoming></bpmn:endEvent>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="Start_1" targetRef="End_1" />
  </bpmn:process>
  <bpmndi:BPMNDiagram id="Diagram_1">
    <bpmndi:BPMNPlane id="Plane_1" bpmnElement="` + processID + `">
      <bpmndi:BPMNShape id="Shape_Start" bpmnElement="Start_1">
        <dc:Bounds x="100" y="100" width="36" height="36" />
      </bpmndi:BPMNShape>
      <bpmndi:BPMNShape id="Shape_End" bpmnElement="End_1">
        <dc:Bounds x="200" y="100" width="36" height="36" />
      </bpmndi:BPMNShape>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`
}

// deployAs posts a BPMN definition and reports the status and body.
func deployAs(t *testing.T, c *http.Client, base, xml string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/deployments", strings.NewReader(xml))
	if err != nil {
		t.Fatalf("build deploy: %v", err)
	}
	req.Header.Set("Content-Type", "application/xml")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// subscribeAs creates an inbound subscription on a clio connector.
func subscribeAs(t *testing.T, c *http.Client, base, connID, messageName string) (int, string) {
	t.Helper()
	body := `{"watchedSubject":"mail/inbox","messageName":"` + messageName + `","enabled":true}`
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/connectors/"+connID+"/inbound-subscriptions",
		strings.NewReader(body))
	if err != nil {
		t.Fatalf("build subscribe: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// TestAStrangerCannotDeployIntoSomebodyElsesMessage is the door this measure was
// asked for: Anna's mailbox publishes under a name, and Bert cannot point a
// process of his own at it.
func TestAStrangerCannotDeployIntoSomebodyElsesMessage(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "anna")
	createUser(t, admin, ts.URL, "bert")
	anna := signInAs(t, ts.URL, "anna", "a-password-that-is-long")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")

	connID := createConnector(t, anna, ts.URL, "annas-posteingang")
	if status, body := subscribeAs(t, anna, ts.URL, connID, "post-eingegangen"); status != http.StatusOK {
		t.Fatalf("anna's own subscription = %d: %s", status, body)
	}

	status, body := deployAs(t, bert, ts.URL, messageStartBPMN("bertsProzess", "post-eingegangen"))
	if status != http.StatusConflict {
		t.Fatalf("= %d, want 409: a stranger deployed into somebody else's message\n%s", status, body)
	}
	t.Run("the refusal names the message and not the other party", func(t *testing.T) {
		if !strings.Contains(body, "post-eingegangen") {
			t.Errorf("the refusal does not name the message, so nobody can act on it: %s", body)
		}
		for _, leak := range []string{"anna", "annas-posteingang", connID} {
			if strings.Contains(body, leak) {
				t.Errorf("the refusal leaks %q — it is meant to stop a delivery, not disclose whose: %s", leak, body)
			}
		}
	})

	t.Run("the owner may deploy against her own claim", func(t *testing.T) {
		if status, body := deployAs(t, anna, ts.URL, messageStartBPMN("annasProzess", "post-eingegangen")); status != http.StatusOK {
			t.Errorf("= %d, want 200: the claimant cannot use her own message\n%s", status, body)
		}
	})

	t.Run("an unclaimed message is nobody's business", func(t *testing.T) {
		if status, body := deployAs(t, bert, ts.URL, messageStartBPMN("bertsAndererProzess", "irgendwas-anderes")); status != http.StatusOK {
			t.Errorf("= %d, want 200: a message nobody claimed was refused\n%s", status, body)
		}
	})
}

// TestAClaimIsRefusedWhenSomebodyElseIsAlreadyListening is the other door, and the
// reason one door is not enough: without it, deploying first would be all it takes.
func TestAClaimIsRefusedWhenSomebodyElseIsAlreadyListening(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "anna")
	createUser(t, admin, ts.URL, "bert")
	anna := signInAs(t, ts.URL, "anna", "a-password-that-is-long")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")

	// Bert gets there first with a definition Anna cannot reach.
	if status, body := deployAs(t, bert, ts.URL, messageStartBPMN("bertsLauscher", "geteilter-name")); status != http.StatusOK {
		t.Fatalf("bert's deploy = %d: %s", status, body)
	}

	connID := createConnector(t, anna, ts.URL, "annas-posteingang")
	status, body := subscribeAs(t, anna, ts.URL, connID, "geteilter-name")
	if status != http.StatusConflict {
		t.Fatalf("= %d, want 409: the claim landed on a name somebody else already catches\n%s", status, body)
	}
	if !strings.Contains(body, "geteilter-name") {
		t.Errorf("the refusal does not name the message: %s", body)
	}
	for _, leak := range []string{"bert", "bertsLauscher"} {
		if strings.Contains(body, leak) {
			t.Errorf("the refusal leaks %q: %s", leak, body)
		}
	}

	t.Run("and is allowed once nothing is in the way", func(t *testing.T) {
		if status, body := subscribeAs(t, anna, ts.URL, connID, "freier-name"); status != http.StatusOK {
			t.Errorf("= %d, want 200: an unclaimed name was refused\n%s", status, body)
		}
	})
}

// TestAClaimDoesNotStopSomebodyWhoMayReachIt: the check is about reach, not about
// identity. A person who may read the definition — because it is theirs, or shared
// with them — is not a stranger to it.
func TestAClaimDoesNotStopSomebodyWhoMayReachIt(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "anna")
	annaClient := signInAs(t, ts.URL, "anna", "a-password-that-is-long")

	connID := createConnector(t, annaClient, ts.URL, "gemeinsam")
	if status, body := subscribeAs(t, annaClient, ts.URL, connID, "gemeinsame-post"); status != http.StatusOK {
		t.Fatalf("subscribe = %d: %s", status, body)
	}

	// An administrator reaches every connector, so the claim is not a stranger's to
	// them and the deploy stands.
	if status, body := deployAs(t, admin, ts.URL, messageStartBPMN("adminProzess", "gemeinsame-post")); status != http.StatusOK {
		t.Errorf("= %d, want 200: an administrator was refused by a claim they can read\n%s", status, body)
	}
}

// TestAnOpenServerClaimsNothing: with --auth=false there is nobody to be somebody
// else, so no claim can stand between two people who are the same person.
func TestAnOpenServerClaimsNothing(t *testing.T) {
	ts := newOpenConnectorServer(t)
	c := &http.Client{}

	connID := createConnector(t, c, ts.URL, "offen")
	if status, body := subscribeAs(t, c, ts.URL, connID, "offene-post"); status != http.StatusOK {
		t.Fatalf("subscribe = %d: %s", status, body)
	}
	if status, body := deployAs(t, c, ts.URL, messageStartBPMN("offenerProzess", "offene-post")); status != http.StatusOK {
		t.Errorf("= %d, want 200: an open server refused a deploy over a claim\n%s", status, body)
	}
}

// TestTheUpdateEndpointIsNotTheWayAround: renaming a subscription's message, or
// switching a disabled one back on, is a fresh claim. Without checking it, the
// create endpoint's door would guard a room with a second entrance.
func TestTheUpdateEndpointIsNotTheWayAround(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "anna")
	createUser(t, admin, ts.URL, "bert")
	anna := signInAs(t, ts.URL, "anna", "a-password-that-is-long")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")

	if status, body := deployAs(t, bert, ts.URL, messageStartBPMN("bertsLauscher", "bewachter-name")); status != http.StatusOK {
		t.Fatalf("bert's deploy = %d: %s", status, body)
	}
	connID := createConnector(t, anna, ts.URL, "annas-posteingang")
	status, body := subscribeAs(t, anna, ts.URL, connID, "harmloser-name")
	if status != http.StatusOK {
		t.Fatalf("subscribe = %d: %s", status, body)
	}
	subID := decodeField(t, body, "id")

	at := ts.URL + "/api/v1/inbound-subscriptions/" + subID
	t.Run("renaming into a guarded name is refused", func(t *testing.T) {
		if got := statusOf(t, anna, http.MethodPatch, at, `{"messageName":"bewachter-name"}`); got != http.StatusConflict {
			t.Errorf("= %d, want 409: the rename walked around the create door", got)
		}
	})
	t.Run("renaming into a free name still works", func(t *testing.T) {
		if got := statusOf(t, anna, http.MethodPatch, at, `{"messageName":"anderer-name"}`); got != http.StatusOK {
			t.Errorf("= %d, want 200: an unclaimed rename was refused", got)
		}
	})
	t.Run("leaving the name alone is not a fresh claim", func(t *testing.T) {
		// A subscription already holds its own name. Touching another field must not
		// make it re-argue for it.
		if got := statusOf(t, anna, http.MethodPatch, at, `{"recursive":true}`); got != http.StatusOK {
			t.Errorf("= %d, want 200: an unrelated edit was refused over the name it already holds", got)
		}
	})
}

// TestDeployingAProjectStopsBeforeItStarts: a bundle is validate-all-then-deploy-all
// (ADR-0034), so a claim refusal on the third draft must not leave the first two
// registered.
func TestDeployingAProjectStopsBeforeItStarts(t *testing.T) {
	ts := newServerOn(t, t.TempDir())
	admin := signedInClient(t, ts.URL)
	createUser(t, admin, ts.URL, "anna")
	createUser(t, admin, ts.URL, "bert")
	anna := signInAs(t, ts.URL, "anna", "a-password-that-is-long")
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")

	connID := createConnector(t, anna, ts.URL, "annas-posteingang")
	if status, body := subscribeAs(t, anna, ts.URL, connID, "projekt-post"); status != http.StatusOK {
		t.Fatalf("subscribe = %d: %s", status, body)
	}

	projID := createProjectAs(t, bert, ts.URL, "berts-projekt")
	saveDraftAs(t, bert, ts.URL, projID, messageStartBPMN("harmlos", "eigener-name"))
	saveDraftAs(t, bert, ts.URL, projID, messageStartBPMN("greift-zu", "projekt-post"))

	status, body := postAs(t, bert, ts.URL+"/api/v1/projects/"+projID+"/deploy", "")
	if status != http.StatusConflict {
		t.Fatalf("= %d, want 409: a project bundle deployed into somebody else's message\n%s", status, body)
	}
	if !strings.Contains(body, "projekt-post") {
		t.Errorf("the refusal does not name the message: %s", body)
	}
	// Nothing was registered — not even the innocent draft that came first.
	if strings.Contains(body, `"deployed":true`) {
		t.Errorf("the bundle reports itself deployed: %s", body)
	}
	if deployedProcessIDs(t, bert, ts.URL)["harmlos"] {
		t.Error("the first draft registered before the refusal — the bundle is not all-or-nothing")
	}
}

// decodeField pulls one top-level string field out of a JSON body.
func decodeField(t *testing.T, body, field string) string {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode %s: %v (%s)", field, err, body)
	}
	v, _ := out[field].(string)
	if v == "" {
		t.Fatalf("no %s in %s", field, body)
	}
	return v
}

// postAs performs a POST and returns its status and body.
func postAs(t *testing.T, c *http.Client, url, body string) (int, string) {
	t.Helper()
	resp, err := c.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

// createProjectAs creates a project owned by one person and returns its id.
func createProjectAs(t *testing.T, c *http.Client, base, name string) string {
	t.Helper()
	status, body := postAs(t, c, base+"/api/v1/projects", `{"name":"`+name+`"}`)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("create project = %d: %s", status, body)
	}
	return decodeField(t, body, "id")
}

// saveDraftAs files a BPMN draft into a project.
func saveDraftAs(t *testing.T, c *http.Client, base, projectID, xml string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/api/v1/drafts?projectId="+projectID, strings.NewReader(xml))
	if err != nil {
		t.Fatalf("build draft: %v", err)
	}
	req.Header.Set("Content-Type", "application/xml")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("save draft: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		out, _ := io.ReadAll(resp.Body)
		t.Fatalf("save draft = %d: %s", resp.StatusCode, out)
	}
}

// deployedProcessIDs is the set of process ids this caller sees deployed.
func deployedProcessIDs(t *testing.T, c *http.Client, base string) map[string]bool {
	t.Helper()
	resp, err := c.Get(base + "/api/v1/processes")
	if err != nil {
		t.Fatalf("list processes: %v", err)
	}
	defer resp.Body.Close()
	var out []struct {
		ProcessID string `json:"processId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode processes: %v", err)
	}
	seen := map[string]bool{}
	for _, p := range out {
		seen[p.ProcessID] = true
	}
	return seen
}
