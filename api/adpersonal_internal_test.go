package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api/vault"
	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// The AD worker writes attributes a process keeps sealed (ADR-0166, amended 2026-11-03;
// ADR-0314), end to end over the HTTP surface: the values are ciphertext in the engine,
// plaintext in the entry the worker is handed, and a search's result goes back in sealed
// while the gateway branches on the plain flag beside it.
const adPersonalBPMN = `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas/schema/1.0"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="defs">
  <bpmn:process id="adpersonal" isExecutable="true" atlas:personal="vorname,nachname,konto,treffer" atlas:dataSubject="personalnummer">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="anlegen"><bpmn:extensionElements>
      <atlas:adConnector connector="forest" operation="create-user"
          dn="=&quot;CN=&quot; + vorname + &quot; &quot; + nachname + &quot;,OU=users,DC=example,DC=com&quot;"
          attributes="{&quot;sAMAccountName&quot;: &quot;=konto&quot;, &quot;givenName&quot;: &quot;=vorname&quot;, &quot;sn&quot;: &quot;=nachname&quot;, &quot;description&quot;: null}"/>
    </bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:serviceTask id="suchen"><bpmn:extensionElements>
      <atlas:adConnector connector="forest" operation="search" baseDN="OU=users,DC=example,DC=com"
          filter="=&quot;(sAMAccountName=&quot; + konto + &quot;)&quot;" select="cn,mail"
          resultVariable="treffer" foundVariable="gefunden"/>
    </bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:exclusiveGateway id="gw" default="nein"/>
    <bpmn:serviceTask id="warten"><bpmn:extensionElements><zeebe:taskDefinition type="warten"/></bpmn:extensionElements></bpmn:serviceTask>
    <bpmn:endEvent id="da"/>
    <bpmn:endEvent id="weg"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="anlegen"/>
    <bpmn:sequenceFlow id="f2" sourceRef="anlegen" targetRef="suchen"/>
    <bpmn:sequenceFlow id="f3" sourceRef="suchen" targetRef="gw"/>
    <bpmn:sequenceFlow id="ja" sourceRef="gw" targetRef="warten"><bpmn:conditionExpression>=gefunden</bpmn:conditionExpression></bpmn:sequenceFlow>
    <bpmn:sequenceFlow id="f4" sourceRef="warten" targetRef="da"/>
    <bpmn:sequenceFlow id="nein" sourceRef="gw" targetRef="weg"/>
  </bpmn:process>
</bpmn:definitions>`

type adLease struct {
	JobKey    uint64            `json:"jobKey"`
	Connector *connectorPayload `json:"connector"`
}

func leaseAdJob(t *testing.T, srv *Server) adLease {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/activate",
		fmt.Sprintf(`{"type":%q,"worker":"w1"}`, compiler.AdJobType), "application/json")
	if code != http.StatusOK {
		t.Fatalf("lease: status=%d body=%s", code, raw)
	}
	var out struct {
		Jobs []adLease `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode lease: %v (%s)", err, raw)
	}
	if len(out.Jobs) != 1 || out.Jobs[0].Connector == nil {
		t.Fatalf("leased %s, want one AD job with its payload", raw)
	}
	return out.Jobs[0]
}

func TestTheADWorkerWritesAttributesAProcessKeepsSealed(t *testing.T) {
	srv, _ := newValidateServer(t, WithOffloadedConnectorKinds(offloadableKindNames()))
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", adPersonalBPMN, "application/xml")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("deploy: status=%d body=%s", code, raw)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	_ = json.Unmarshal(raw, &dep)
	code, raw = serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key),
		`{"variables":{"personalnummer":"P-4711","vorname":"Ida-Luise","nachname":"Mustermann-Gross","konto":"imustermann"}}`, "application/json")
	if code != http.StatusOK && code != http.StatusCreated {
		t.Fatalf("start: status=%d body=%s", code, raw)
	}
	var inst struct {
		Key uint64 `json:"key"`
	}
	_ = json.Unmarshal(raw, &inst)
	instKey := inst.Key
	if instKey == 0 {
		instKey = mustInstanceOf(t, srv)
	}

	// Ciphertext where the engine can see it.
	for _, name := range []string{"vorname", "nachname", "konto"} {
		if v := rootVar(t, srv, instKey, name); !vault.IsEnciphered(v.Text) {
			t.Fatalf("%s is stored in the clear: %q", name, v.Text)
		}
	}

	// Plaintext in the entry the worker is handed.
	create := leaseAdJob(t, srv)
	f := create.Connector.Fields
	if f["operation"] != "create-user" || f["dn"] != "CN=Ida-Luise Mustermann-Gross,OU=users,DC=example,DC=com" {
		t.Fatalf("create payload = %v, want the dn built from the opened values", f)
	}
	attrs, _ := f["attributes"].(map[string]any)
	want := map[string]string{"sAMAccountName": "imustermann", "givenName": "Ida-Luise", "sn": "Mustermann-Gross"}
	for name, val := range want {
		vals, _ := attrs[name].([]any)
		if len(vals) != 1 || vals[0] != val {
			t.Errorf("attribute %s = %v, want [%s]", name, attrs[name], val)
		}
	}
	if _, has := attrs["description"]; has {
		t.Errorf("a null attribute travelled on a create: %v", attrs)
	}
	if code, raw := serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/complete", create.JobKey), `{"reason":"test: the worker added the entry"}`, "application/json"); code != http.StatusOK {
		t.Fatalf("complete create: status=%d body=%s", code, raw)
	}

	// The search: its filter opened, its selection and flag on the wire.
	search := leaseAdJob(t, srv)
	f = search.Connector.Fields
	if f["filter"] != "(sAMAccountName=imustermann)" || f["foundVariable"] != "gefunden" || f["resultVariable"] != "treffer" {
		t.Fatalf("search payload = %v", f)
	}
	if sel, _ := f["select"].([]any); len(sel) != 2 || sel[0] != "cn" || sel[1] != "mail" {
		t.Errorf("select = %v, want [cn mail]", f["select"])
	}
	// What the worker reports back: the result is sealed on the way in, the flag is not.
	body := `{"reason":"test: the worker searched","variables":{"treffer":{"found":true,"count":1,"dn":"CN=Ida-Luise Mustermann-Gross,OU=users,DC=example,DC=com","entries":[]},"gefunden":true}}`
	if code, raw := serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/jobs/%d/complete", search.JobKey), body, "application/json"); code != http.StatusOK {
		t.Fatalf("complete search: status=%d body=%s", code, raw)
	}
	// The gateway read the plain flag and took the found branch: the instance waits at
	// the task behind it, with the result sealed and the flag in the clear.
	if k, _ := parkedServiceJob(t, srv, dep.Key); k == 0 {
		t.Fatal("the instance did not take the found branch")
	}
	if v := rootVar(t, srv, instKey, "treffer"); !vault.IsEnciphered(v.Text) || strings.Contains(v.Text, "Mustermann") {
		t.Errorf("the search result landed in the clear: %q", v.Text)
	}
	if v := rootVar(t, srv, instKey, "gefunden"); v.Kind != model.VarBool || !v.Bool {
		t.Errorf("gefunden = kind %d %v, want a plain true", v.Kind, v.Bool)
	}
}
