package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

// What a worker is handed when the engine cannot resolve the task it leases.
//
// resolveConnectorTask has one arm per Worker Type, and every arm ends the same way
// when its kind's Resolve fails: no payload. That is only safe because of what
// pulledJob does next — it reads the job's variables through the same opening reader,
// and a job whose values cannot be opened is withheld rather than handed out. ADR-0314
// names the case that produces exactly this: a declared personal value whose data
// subject has been erased. Neither half had a test per kind, so a kind whose Resolve
// stopped reading through the opening reader — or one that started returning a partial
// payload on error — would have handed a worker an envelope, or nothing, with no test
// failing.
//
// The tests below lease two instances of the same task side by side: one whose subject
// was erased, one whose subject was not. The erased one must not be handed out; the
// other must arrive with the payload its kind resolves to, which is what proves the
// erased one was withheld for its values and not because the kind cannot be leased.

// handlersPersonalTaskModel wraps one task element (id "t") in a process that declares
// vorname personal and personalnummer its data subject (ADR-0314).
func handlersPersonalTaskModel(procID, task string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
                  xmlns:atlas="http://atlas/schema/1.0" id="defs-%s">
  <bpmn:process id="%s" isExecutable="true" atlas:personal="vorname" atlas:dataSubject="personalnummer">
    <bpmn:startEvent id="s"/>
    %s
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`, procID, procID, task)
}

// handlersServiceTask is the common case: a service task carrying one Worker
// Type's element.
func handlersServiceTask(element string) string {
	return `<bpmn:serviceTask id="t"><bpmn:extensionElements>` + element + `</bpmn:extensionElements></bpmn:serviceTask>`
}

// handlersLeasedJob is the part of a leased job these tests read.
type handlersLeasedJob struct {
	ProcessInstanceKey uint64            `json:"processInstanceKey"`
	Connector          *connectorPayload `json:"connector"`
}

// handlersStartPersonal starts one instance for one data subject and returns its key.
func handlersStartPersonal(t *testing.T, srv *Server, defKey uint64, subject string, extra map[string]any) uint64 {
	t.Helper()
	vars := map[string]any{"personalnummer": subject, "vorname": "Ida-" + subject}
	for k, v := range extra {
		vars[k] = v
	}
	body, err := json.Marshal(map[string]any{"variables": vars})
	if err != nil {
		t.Fatalf("encode variables: %v", err)
	}
	code, raw := serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", defKey), string(body), "application/json")
	if code != http.StatusOK {
		t.Fatalf("start instance for %s: status=%d body=%s", subject, code, raw)
	}
	var out struct {
		InstanceKey uint64 `json:"instanceKey"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || out.InstanceKey == 0 {
		t.Fatalf("decode instance: %v (%s)", err, raw)
	}
	return out.InstanceKey
}

// handlersAgentRoundTask is an agent-driven ad-hoc subprocess with one tool: its round
// job is the one job here that is not a service task's (ADR-0253/0254).
const handlersAgentRoundTask = `<bpmn:adHocSubProcess id="t">
      <bpmn:documentation>Beantworte die Frage.</bpmn:documentation>
      <bpmn:extensionElements>
        <atlas:agentConnector connector="anthropic_pb" resultCollection="toolCallResults" resultElement="=toolCallResult"/>
      </bpmn:extensionElements>
      <bpmn:serviceTask id="nachschlagen">
        <bpmn:documentation>Schlagt nach.</bpmn:documentation>
        <bpmn:extensionElements><zeebe:taskDefinition type="lookup"/></bpmn:extensionElements>
      </bpmn:serviceTask>
    </bpmn:adHocSubProcess>`

// TestHandlersErasedSubjectsJobIsWithheldForEveryWorkerType walks every arm of
// resolveConnectorTask. A data subject's erasure must stop each kind's job from
// leaving the engine, while the same task for another subject still arrives with the
// payload its kind resolves to.
func TestHandlersErasedSubjectsJobIsWithheldForEveryWorkerType(t *testing.T) {
	cases := []struct {
		name    string
		task    string
		jobType string
		kind    string // the payload kind the surviving job must arrive with
		extra   map[string]any
	}{
		{"script", `<bpmn:scriptTask id="t"><bpmn:extensionElements><atlas:jobScript language="javascript" resultVariable="total">result = 1 + 1;</atlas:jobScript></bpmn:extensionElements></bpmn:scriptTask>`,
			compiler.JsJobType, "script", nil},
		{"agent-round", handlersAgentRoundTask, compiler.AgentJobType, "agent", nil},
		{"temis", `<bpmn:businessRuleTask id="t"><bpmn:extensionElements><zeebe:calledDecision decisionId="Zins" resultVariable="zins"/><atlas:temisConnector connector="rules"/><zeebe:ioMapping><zeebe:input source="=personalnummer" target="kunde"/></zeebe:ioMapping></bpmn:extensionElements></bpmn:businessRuleTask>`,
			compiler.TemisDecisionJobType, "temis", nil},
		{"csv", handlersServiceTask(`<atlas:csvConnector source="csvText" resultVariable="records"/>`),
			compiler.CsvImportJobType, "csv", map[string]any{"csvText": "a,b\n1,2"}},
		{"ldif", handlersServiceTask(`<atlas:ldifConnector format="ldif" source="ldifText" resultVariable="entries"/>`),
			compiler.LdifJobType, "ldif", map[string]any{"ldifText": "dn: cn=ada\nobjectClass: person"}},
		{"mail", handlersServiceTask(`<atlas:mailConnector connector="office365" to="a@b.ch" subject="Hi" body="There"/>`),
			compiler.MailJobType, "mail", nil},
		{"remedy", handlersServiceTask(`<atlas:remedyConnector connector="helix-itsm" form="HPD:IncidentInterface_Create" resultVariable="nr"><atlas:remedyField name="Description" value="Disk full"/></atlas:remedyConnector>`),
			compiler.RemedyJobType, "remedy", nil},
		{"jira", handlersServiceTask(`<atlas:jiraConnector connector="acme" operation="get-issue" issueKey="OPS-1" resultVariable="ticket"/>`),
			compiler.JiraJobType, "jira", nil},
		{"aitask", handlersServiceTask(`<atlas:agentConnector connector="anthropic_pb" model="claude-haiku-4-5" prompt="=&quot;Klassifiziere&quot;" resultVariable="kategorie"/>`),
			compiler.AiTaskJobType, "agent", nil},
		{"googlesheets", handlersServiceTask(`<atlas:googleSheetsConnector connector="acme" operation="append-row" spreadsheet="1AbCdEfGh" range="Eingang!A:B" values="=zeilen" resultVariable="antwort"/>`),
			compiler.GoogleSheetsJobType, "googlesheets", map[string]any{"zeilen": []any{[]any{"Ada", "Lovelace"}}}},
		{"discord", handlersServiceTask(`<atlas:discordConnector connector="team" operation="send-message" channel="42" content="hello"/>`),
			compiler.DiscordJobType, "discord", nil},
		{"postgres", handlersServiceTask(`<atlas:postgresConnector connector="hr-db" operation="query" statement="SELECT $1" parametersVariable="sqlParams" resultVariable="rows"/>`),
			compiler.PostgresJobType, "postgres", map[string]any{"sqlParams": []any{"P-2"}}},
		{"ad", handlersServiceTask(`<atlas:adConnector url="ldaps://dc.example.com" operation="disable" dn="cn=Arno,dc=example,dc=com"/>`),
			compiler.AdJobType, "ad", nil},
		{"ldap", handlersServiceTask(`<atlas:ldapConnector url="ldaps://dir.example.com" bindDN="cn=svc,dc=example,dc=com" bindSecret="ldap-bind" operation="search" baseDN="ou=people,dc=example,dc=com" scope="sub" filter="(uid=ada)" resultVariable="treffer"/>`),
			compiler.LdapJobType, "ldap", nil},
		{"soap", handlersServiceTask(`<atlas:soapConnector endpoint="https://example.com/svc" operation="GetRate" soapVersion="1.2" body="&lt;GetRate/&gt;" resultVariable="kurs"/>`),
			compiler.SoapJobType, "soap", nil},
		{"sharepoint", handlersServiceTask(`<atlas:sharePointConnector connector="contoso" site="contoso.sharepoint.com,/sites/ops" list="Onboarding" resultVariable="angelegt"><atlas:itemField name="Title" value="Neu"/></atlas:sharePointConnector>`),
			compiler.SharePointJobType, "sharepoint", nil},
		{"scim", handlersServiceTask(`<atlas:scimConnector baseUrl="https://idp.example.com/scim/v2" resource="Users" operation="search" filter="userName eq &quot;ada&quot;" resultVariable="konten"/>`),
			compiler.ScimJobType, "scim", nil},
		{"entra", handlersServiceTask(`<atlas:entraConnector connector="contoso" operation="list-users" resultVariable="users"/>`),
			compiler.EntraJobType, "entra", nil},
		{"clio", handlersServiceTask(`<atlas:clioConnector connector="events" operation="write" subject="/kunden/42" eventType="kunde.angelegt"/>`),
			compiler.ClioWriteJobType, "clio", nil},
		{"webscrape", handlersServiceTask(`<atlas:webscrapeConnector url="https://example.com" selector=".price" resultVariable="hits"/>`),
			compiler.WebScrapeJobType, "webscrape", nil},
		{"rest", handlersServiceTask(`<atlas:restConnector method="get" url="https://api.example.com/x" resultVariable="r"/>`),
			compiler.RestJobType, "rest", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newValidateServer(t, WithOffloadedConnectorKinds(offloadableKindNames()))
			code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments",
				handlersPersonalTaskModel(tc.name+"-proc", tc.task), "application/xml")
			if code != http.StatusOK {
				t.Fatalf("deploy: status=%d body=%s", code, raw)
			}
			var dep struct {
				Key uint64 `json:"key"`
			}
			if err := json.Unmarshal(raw, &dep); err != nil {
				t.Fatalf("decode deploy: %v", err)
			}
			erased := handlersStartPersonal(t, srv, dep.Key, "P-1", tc.extra)
			kept := handlersStartPersonal(t, srv, dep.Key, "P-2", tc.extra)

			if code, raw := serveInternal(t, srv, http.MethodDelete, "/api/v1/personal-data/P-1", "", ""); code != http.StatusOK {
				t.Fatalf("erase: status=%d body=%s", code, raw)
			}
			code, raw = serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/activate",
				fmt.Sprintf(`{"type":%q,"worker":"w1","maxJobs":5}`, tc.jobType), "application/json")
			if code != http.StatusOK {
				t.Fatalf("lease: status=%d body=%s", code, raw)
			}
			var out struct {
				Jobs []handlersLeasedJob `json:"jobs"`
			}
			if err := json.Unmarshal(raw, &out); err != nil {
				t.Fatalf("decode lease: %v (%s)", err, raw)
			}
			if len(out.Jobs) != 1 {
				t.Fatalf("leased %d jobs, want only the unerased subject's; body=%s", len(out.Jobs), raw)
			}
			got := out.Jobs[0]
			if got.ProcessInstanceKey == erased {
				t.Fatalf("the erased subject's job (instance %d) was handed to a worker", erased)
			}
			if got.ProcessInstanceKey != kept {
				t.Fatalf("leased instance %d, want %d", got.ProcessInstanceKey, kept)
			}
			if got.Connector == nil || got.Connector.Kind != tc.kind {
				t.Fatalf("the unerased job arrived with payload %+v, want kind %q", got.Connector, tc.kind)
			}
		})
	}
}

// TestHandlersLocalDecisionIsLeasedWithoutAPayload is the other side of
// TestLocalDMNMustNotGainAPayloadArm: that test reads the switch, this one leases the
// job. A local business rule task passes the payload gate — it is the same node type
// as a central decision — and must come out of the switch with nothing, because the
// worker that takes it evaluates with its own library and needs no resolved detail.
// With dmn offloaded the job is still handed out; only the payload is absent.
func TestHandlersLocalDecisionIsLeasedWithoutAPayload(t *testing.T) {
	srv, _ := newValidateServer(t, WithOffloadedConnectorKinds(offloadableKindNames()))
	x := deployTestHarness{t, srv.Handler()}
	// The deploy bundles the model that provides the decision, so the reference to the
	// seeded dish.dmn has to exist first; without it the deploy is refused (409).
	if code, b := x.do(http.MethodPost, "/api/v1/dmnrefs", `{"name":"Dish","modelRef":"dish"}`); code != http.StatusOK {
		t.Fatalf("add reference: status=%d body=%s", code, b)
	}
	model := strings.Replace(localDecisionPayloadBPMN, `decisionId="Rate"`, `decisionId="Dish"`, 1)
	if code, b := x.do(http.MethodPost, "/api/v1/deployments", model); code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, b)
	}
	if code, b := x.do(http.MethodPost, "/api/v1/processes/1/instances", `{"variables":{"Season":"Winter"}}`); code != http.StatusOK {
		t.Fatalf("start: status=%d body=%s", code, b)
	}
	code, raw := x.do(http.MethodPost, "/api/v1/jobs/activate", fmt.Sprintf(`{"type":%q,"worker":"w1"}`, compiler.DMNJobType))
	if code != http.StatusOK {
		t.Fatalf("lease: status=%d body=%s", code, raw)
	}
	var out struct {
		Jobs []handlersLeasedJob `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode lease: %v (%s)", err, raw)
	}
	if len(out.Jobs) != 1 {
		t.Fatalf("leased %d local-decision jobs, want 1: offloading dmn must still hand the job out; body=%s", len(out.Jobs), raw)
	}
	if out.Jobs[0].Connector != nil {
		t.Fatalf("a local decision's job was leased with payload %+v, want none", *out.Jobs[0].Connector)
	}
}

// TestHandlersAiTaskPromptIsResolvedOverPlaintext is the regression test for the ai
// task's arm, the one arm that resolved over the raw store rather than the opening
// reader. An ai task's prompt is a worker-evaluated expression, which compiler/personal.go
// lets read a declared personal value on the condition that the worker sees the
// plaintext (ADR-0314). Resolved over the envelope instead, "Hallo " + vorname is FEEL
// over a context and the prompt travels empty — while a mail subject built the same way
// in the same instance arrives correct.
func TestHandlersAiTaskPromptIsResolvedOverPlaintext(t *testing.T) {
	srv, _ := newValidateServer(t, WithOffloadedConnectorKinds(offloadableKindNames()))
	task := handlersServiceTask(`<atlas:agentConnector connector="anthropic_pb" model="claude-haiku-4-5" prompt="=&quot;Hallo &quot; + vorname" resultVariable="kategorie"/>`)
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", handlersPersonalTaskModel("ai-personal", task), "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, raw)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(raw, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	handlersStartPersonal(t, srv, dep.Key, "P-1", nil)

	code, raw = serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/activate",
		fmt.Sprintf(`{"type":%q,"worker":"w1"}`, compiler.AiTaskJobType), "application/json")
	if code != http.StatusOK {
		t.Fatalf("lease: status=%d body=%s", code, raw)
	}
	var out struct {
		Jobs []handlersLeasedJob `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode lease: %v (%s)", err, raw)
	}
	if len(out.Jobs) != 1 || out.Jobs[0].Connector == nil || out.Jobs[0].Connector.Kind != "agent" {
		t.Fatalf("leased %s, want one ai task with an agent payload", raw)
	}
	if got := out.Jobs[0].Connector.Fields["prompt"]; got != "Hallo Ida-P-1" {
		t.Errorf("prompt = %#v, want %q: the prompt was resolved over the stored envelope, not the plaintext", got, "Hallo Ida-P-1")
	}
}
