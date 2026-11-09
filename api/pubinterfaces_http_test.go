package api_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// The published process interfaces of ADR-0373, end to end: a real server with
// authentication, an application that deployed a process with message entry points,
// a reader in another domain, and a restart over the same data directory.

const interfaceBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:message id="Msg_antrag" name="kredit.antrag"/>
  <bpmn:message id="Msg_unterlagen" name="kredit.unterlagen">
    <bpmn:extensionElements><zeebe:subscription correlationKey="= antragId"/></bpmn:extensionElements>
  </bpmn:message>
  <bpmn:error id="Err_rej" errorCode="REJECTED"/>
  <bpmn:process id="kreditpruefung" isExecutable="true">
    <bpmn:startEvent id="antrag" name="Antrag eingegangen"><bpmn:messageEventDefinition messageRef="Msg_antrag"/></bpmn:startEvent>
    <bpmn:receiveTask id="unterlagen" messageRef="Msg_unterlagen"/>
    <bpmn:exclusiveGateway id="gw" default="nein"/>
    <bpmn:endEvent id="bewilligt"/>
    <bpmn:endEvent id="abgelehnt"><bpmn:errorEventDefinition errorRef="Err_rej"/></bpmn:endEvent>
    <bpmn:sequenceFlow id="f1" sourceRef="antrag" targetRef="unterlagen"/>
    <bpmn:sequenceFlow id="f2" sourceRef="unterlagen" targetRef="gw"/>
    <bpmn:sequenceFlow id="ja" sourceRef="gw" targetRef="bewilligt"><bpmn:conditionExpression>= ok</bpmn:conditionExpression></bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="nein" sourceRef="gw" targetRef="abgelehnt"/>
  </bpmn:process>
</bpmn:definitions>`

func definitionKeyOf(t *testing.T, c *http.Client, base string, processID string) uint64 {
	t.Helper()
	resp, err := c.Get(base + "/api/v1/processes")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var list []struct {
		Key       uint64 `json:"key"`
		ProcessID string `json:"processId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	for _, p := range list {
		if p.ProcessID == processID {
			return p.Key
		}
	}
	t.Fatalf("no deployed %s", processID)
	return 0
}

func TestAPublishedInterfaceIsReadAsAContractAcrossARestart(t *testing.T) {
	dir := t.TempDir()
	ts, closeFn := authServerOn(t, dir, "root", "rootpassword")
	admin := newClient(t)
	if code := login(t, admin, ts, "root", "rootpassword"); code != http.StatusOK {
		t.Fatalf("admin login = %d", code)
	}
	app := appNamed(t, admin, ts, "Kredit")
	if code, body := cReqTyped(t, admin, ts, "POST", "/api/v1/drafts?projectId="+app, "application/xml", interfaceBPMN); code != http.StatusOK {
		t.Fatalf("save draft = %d %s", code, body)
	}
	if code, body := cReq(t, admin, ts, "POST", "/api/v1/applications/"+app+"/publish", `{}`); code != http.StatusOK {
		t.Fatalf("publish application = %d %s", code, body)
	}
	key := definitionKeyOf(t, admin, ts.URL, "kreditpruefung")
	readerID := createUserWithRoles(t, admin, ts.URL, "nachbardomaene", `["modeler"]`)
	reader := signInAs(t, ts.URL, "nachbardomaene", "a-password-that-is-long")

	code, body := cReq(t, admin, ts, "GET", "/api/v1/processes/"+strconv.FormatUint(key, 10)+"/interface-candidates", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"elementName":"Antrag eingegangen"`) ||
		!strings.Contains(string(body), `"errors":["REJECTED"]`) {
		t.Fatalf("candidates = %d %s", code, body)
	}
	if code, _ := cReq(t, reader, ts, "GET", "/api/v1/processes/"+strconv.FormatUint(key, 10)+"/interface-candidates", ""); code != http.StatusNotFound {
		t.Fatalf("another domain reading the candidates = %d, want 404", code)
	}

	publish := `{"definitionKey":` + strconv.FormatUint(key, 10) + `,"expectedRevision":0,
	  "entryPoints":[{"name":"antrag","elementId":"antrag"},{"name":"unterlagen","elementId":"unterlagen"}],
	  "outcomes":[{"name":"bewilligt","elementId":"bewilligt"},{"name":"abgelehnt","elementId":"abgelehnt"}]}`
	if code, body := cReq(t, reader, ts, "POST", "/api/v1/published-interfaces/kreditpruefung/versions", publish); code != http.StatusNotFound {
		t.Fatalf("another domain publishing = %d %s, want 404", code, body)
	}
	if code, body := cReq(t, admin, ts, "POST", "/api/v1/published-interfaces/kreditpruefung/versions", publish); code != http.StatusOK {
		t.Fatalf("publish = %d %s", code, body)
	}
	if code, body := cReq(t, reader, ts, "GET", "/api/v1/published-interfaces", ""); code != http.StatusOK || !strings.Contains(string(body), `"total":0`) {
		t.Fatalf("an ungranted reader lists = %d %s", code, body)
	}
	grants := `{"public":false,"view":{"userIds":["` + readerID + `"]},"send":{},"expectedRevision":1}`
	if code, body := cReq(t, admin, ts, "PUT", "/api/v1/published-interfaces/kreditpruefung/grants", grants); code != http.StatusOK {
		t.Fatalf("grants = %d %s", code, body)
	}
	closeFn()

	// The contract, its grant and its audit survive a restart.
	ts, closeFn = authServerOn(t, dir, "root", "rootpassword")
	defer closeFn()
	reader = signInAs(t, ts.URL, "nachbardomaene", "a-password-that-is-long")
	code, body = cReq(t, reader, ts, "GET", "/api/v1/published-interfaces/kreditpruefung", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"name":"unterlagen"`) || !strings.Contains(string(body), `"correlationKey":"antragId"`) ||
		!strings.Contains(string(body), `"available":true`) {
		t.Fatalf("the reader after a restart = %d %s", code, body)
	}
	for _, internal := range []string{"kredit.unterlagen", "elementIds", `"processId"`} {
		if strings.Contains(string(body), internal) {
			t.Errorf("the reader sees %s: %s", internal, body)
		}
	}

	// Deactivating the definition makes the interface unavailable, not unreadable.
	admin = newClient(t)
	if code := login(t, admin, ts, "root", "rootpassword"); code != http.StatusOK {
		t.Fatalf("admin login = %d", code)
	}
	if code, body := cReq(t, admin, ts, "PUT", "/api/v1/processes/"+strconv.FormatUint(key, 10)+"/active", `{"active":false}`); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("deactivate = %d %s", code, body)
	}
	code, body = cReq(t, reader, ts, "GET", "/api/v1/published-interfaces/kreditpruefung/versions/1", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"available":false`) || !strings.Contains(string(body), "definition-inactive") {
		t.Fatalf("after deactivation = %d %s", code, body)
	}
	code, body = cReq(t, admin, ts, "GET", "/api/v1/published-interfaces/kreditpruefung/history", "")
	if code != http.StatusOK || !strings.Contains(string(body), `"action":"grants"`) || !strings.Contains(string(body), `"action":"publish"`) {
		t.Fatalf("history = %d %s", code, body)
	}
}
