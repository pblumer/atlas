package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// The Microsoft 365 example asks the line manager of the person a position is for.
//
// Atlas's own approval kind "superior" asks the manager of the person who ordered,
// and finds that person's manager only through a directory worker. A licence, a
// team or a mailbox is ordered for somebody named in the order form, and that
// answer reaches only an approval model of the installation's own (ADR-0441). So
// the example binds its products to proc_m365_genehmigung_vorgesetzter, which reads
// the manager from Entra ID with the Entra Worker's get-user-manager and hands the
// task to the manager's Atlas account, or to a fallback group.
//
// The answers name people and are declared personal data (ADR-0314), so the engine
// holds them enciphered and no engine expression may read them: the task's userId
// picks the person when the job is handed over, and it is the worker that sees the
// plaintext. These tests run the example's model on a real server, play the Entra
// worker by leasing its job, and follow every way a decision is reached.

const m365ApprovalProcess = "proc_m365_genehmigung_vorgesetzter"

// The stand-ins declare the answers that name somebody, as the example's own
// processes do, so a publish warning can only be the approval model's.
const m365StubProvisionBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0" id="defs_m365_stub_prov" targetNamespace="http://atlas/test">
  <process id="m365_stub_prov" isExecutable="true" atlas:personal="besitzer,postfach,person,anlass" atlas:dataSubject="recipient">
    <startEvent id="s"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <userTask id="t" name="Bereitstellen"/>
    <sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
    <endEvent id="e"/>
  </process>
</definitions>`

const m365StubDeprovisionBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0" id="defs_m365_stub_deprov" targetNamespace="http://atlas/test">
  <process id="m365_stub_deprov" isExecutable="true" atlas:personal="besitzer,postfach,person,anlass" atlas:dataSubject="recipient">
    <startEvent id="s"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="e"/>
    <endEvent id="e"/>
  </process>
</definitions>`

// TestTheM365ApprovalGoesToTheManagerOfTheTeamOwner: the manager Entra ID names has
// an Atlas account, the task is assigned to it, and the manager's yes starts the
// provisioning with the manager recorded as the approver.
func TestTheM365ApprovalGoesToTheManagerOfTheTeamOwner(t *testing.T) {
	ts, admin := aServerThatCanCallItself(t)
	setUpM365Approval(t, ts, admin)
	chefin := createM365User(t, ts, admin, "chefin.muster", "Chefin Muster")

	ord := orderM365(t, ts, admin, "m365-team", "m365-team-bestellung", map[string]string{
		"bereich": "IT", "bezeichnung": "Projekt Atlas", "zweck": "Zusammenarbeit",
		"sichtbarkeit": "Private", "besitzer": "anna.beispiel@contoso.example",
	})

	// The task's userId picks the person from the answers, so the job carries the
	// plaintext the worker sends to Graph.
	job := leaseM365Graph(t, ts, admin)
	if got := job.Connector.Fields["userId"]; got != "anna.beispiel@contoso.example" {
		t.Fatalf("the Entra job got userId=%v, want the plaintext besitzer; fields: %v",
			got, job.Connector.Fields)
	}
	completeM365Graph(t, ts, admin, job, `{"id":"m-1","userPrincipalName":"Chefin.Muster@contoso.example",`+
		`"mail":"chefin.muster@contoso.example","displayName":"Chefin Muster"}`)

	// The account is named after the part before the "@", as Atlas names an account
	// from Entra ID, and its display name agrees: that is the manager.
	task := awaitM365Task(t, ts, admin, "GenehmigenPerson")
	if task.Assignee != "chefin.muster" {
		t.Fatalf("the approval is assigned to %q, want chefin.muster", task.Assignee)
	}
	if task.FormID != "genehmigung" {
		t.Fatalf("the approval carries form %q, want genehmigung (the inbox's Genehmigen/Ablehnen)", task.FormID)
	}

	if code, b := cReq(t, chefin.client, ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/complete", task.Key),
		`{"variables":{"genehmigt":true,"begruendung":""}}`); code != http.StatusOK {
		t.Fatalf("approve: %d (%s)", code, b)
	}

	// approvedBy is who completed the task (completedBy, the account's id): the
	// order records the approval only with somebody named.
	line := awaitM365Line(t, ts, admin, ord, func(l m365Line) bool { return l.ApprovedBy != "" })
	if line.ApprovedBy != chefin.id {
		t.Fatalf("the line was approved by %q, want chefin.muster (%s)", line.ApprovedBy, chefin.id)
	}
	awaitM365Instance(t, ts, admin, "m365_stub_prov")
}

// TestTheM365ApprovalFallsBackToTheGroupAndItsReasonReachesTheOrder: Entra ID names
// no manager, so the fallback group M365-Administration decides. Its refusal
// reaches the order with the approver's reason — not with the order form's own
// justification, which is why that answer is called anlass and not begruendung.
func TestTheM365ApprovalFallsBackToTheGroupAndItsReasonReachesTheOrder(t *testing.T) {
	ts, admin := aServerThatCanCallItself(t)
	setUpM365Approval(t, ts, admin)
	mia := createM365User(t, ts, admin, "mia", "Mia Admin")
	code, b := cReq(t, admin, ts, "POST", "/api/v1/groups", `{"name":"M365-Administration"}`)
	if code != http.StatusCreated {
		t.Fatalf("create group: %d (%s)", code, b)
	}
	grp := idOf(t, b)
	if code, b := cReq(t, admin, ts, "PUT", "/api/v1/groups/"+grp+"/members/"+mia.id, ""); code != http.StatusNoContent && code != http.StatusOK {
		t.Fatalf("add mia: %d (%s)", code, b)
	}

	ord := orderM365(t, ts, admin, "m365-postfachzugriff", "m365-postfachzugriff-bestellung", map[string]string{
		"postfach": "info@contoso.example", "postfachtyp": "funktion", "zugriffsart": "vollzugriff",
		"person": "anna.beispiel@contoso.example", "anlass": "Vertretung während der Ferien",
	})

	job := leaseM365Graph(t, ts, admin)
	if got := job.Connector.Fields["userId"]; got != "anna.beispiel@contoso.example" {
		t.Fatalf("the Entra job got userId=%v, want the plaintext person; fields: %v",
			got, job.Connector.Fields)
	}
	// Entra ID names no manager: get-user-manager answers null.
	completeM365Graph(t, ts, admin, job, `null`)

	task := awaitM365Task(t, ts, admin, "GenehmigenGruppe")
	if task.CandidateGroups != "M365-Administration" {
		t.Fatalf("the approval is offered to %q, want M365-Administration", task.CandidateGroups)
	}
	if got := m365Variables(t, ts, admin, task.ProcessInstanceKey)["entscheidungsweg"]; got != "Ersatzgruppe: für die betroffene Person ist kein Vorgesetzter gepflegt" {
		t.Fatalf("entscheidungsweg = %v, want the missing manager named", got)
	}
	if code, b := cReq(t, mia.client, ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/claim", task.Key), `{}`); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("claim: %d (%s)", code, b)
	}
	if code, b := cReq(t, mia.client, ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/complete", task.Key),
		`{"variables":{"genehmigt":false,"begruendung":"Kein Bedarf"}}`); code != http.StatusOK {
		t.Fatalf("reject: %d (%s)", code, b)
	}

	line := awaitM365Line(t, ts, admin, ord, func(l m365Line) bool { return l.Status == "rejected" })
	if line.Reason != "Kein Bedarf" {
		t.Fatalf("the line was rejected with reason %q, want the approver's %q", line.Reason, "Kein Bedarf")
	}
	if line.DecidedBy != mia.id {
		t.Fatalf("the line was decided by %q, want mia (%s)", line.DecidedBy, mia.id)
	}
}

// TestTheM365ApprovalAsksTheGroupWhenEntraDoesNotKnowThePerson: the order names
// somebody Entra ID does not know — a mistyped address, typically. The worker ends the
// job with ENTRA_USER_NOT_FOUND because the model catches it, and the fallback group
// decides with that reason in front of it, instead of the order waiting on an incident.
func TestTheM365ApprovalAsksTheGroupWhenEntraDoesNotKnowThePerson(t *testing.T) {
	ts, admin := aServerThatCanCallItself(t)
	setUpM365Approval(t, ts, admin)

	orderM365(t, ts, admin, "m365-team", "m365-team-bestellung", map[string]string{
		"bereich": "IT", "bezeichnung": "Projekt Atlas", "zweck": "Zusammenarbeit",
		"sichtbarkeit": "Private", "besitzer": "anna.beisple@contoso.example",
	})

	job := leaseM365Graph(t, ts, admin)
	// The worker throws the code only when the process catches it, and it can only know
	// that from the job.
	codes, _ := job.Connector.Fields["errorCodes"].([]any)
	if !slices.Contains(codes, any("ENTRA_USER_NOT_FOUND")) {
		t.Fatalf("the Entra job carries errorCodes %v, want ENTRA_USER_NOT_FOUND", job.Connector.Fields["errorCodes"])
	}
	body := fmt.Sprintf(`{"worker":"entra-test","leaseToken":%d,"errorCode":"ENTRA_USER_NOT_FOUND","errorMessage":"entra: get-user-manager: the user does not exist in Entra ID"}`, job.LeaseToken)
	if code, b := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/jobs/%d/error", job.JobKey), body); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("throw the error: %d (%s)", code, b)
	}

	task := awaitM365Task(t, ts, admin, "GenehmigenGruppe")
	if task.CandidateGroups != "M365-Administration" {
		t.Fatalf("the approval is offered to %q, want M365-Administration", task.CandidateGroups)
	}
	if got := m365Variables(t, ts, admin, task.ProcessInstanceKey)["entscheidungsweg"]; got != "Ersatzgruppe: die betroffene Person ist in Entra ID unbekannt" {
		t.Fatalf("entscheidungsweg = %v, want the unknown person named as the reason", got)
	}
	// The notification may raise its own incident here — this server has no mail worker
	// and no such group — but the lookup must not.
	code, inc := cReq(t, admin, ts, "GET", "/api/v1/incidents", "")
	if code != http.StatusOK {
		t.Fatalf("incidents: %d (%s)", code, inc)
	}
	var incidents []struct {
		ElementID string `json:"elementId"`
	}
	if err := json.Unmarshal(listRows(t, inc), &incidents); err != nil {
		t.Fatalf("decode incidents: %v (%s)", err, inc)
	}
	for _, i := range incidents {
		if i.ElementID == "Graph" {
			t.Fatalf("an unknown person raised an incident on the lookup: %s", inc)
		}
	}
}

// m365Variables reads what the approval instance holds.
func m365Variables(t *testing.T, ts *httptest.Server, admin *http.Client, instance uint64) map[string]any {
	t.Helper()
	code, out := cReq(t, admin, ts, "GET", fmt.Sprintf("/api/v1/instances/%d/variables", instance), "")
	if code != http.StatusOK {
		t.Fatalf("variables: %d (%s)", code, out)
	}
	var vars map[string]any
	if err := json.Unmarshal(out, &vars); err != nil {
		t.Fatalf("decode variables: %v (%s)", err, out)
	}
	return vars
}

// setUpM365Approval puts the example's approval model and order forms on the
// server, with stand-in processes to provision.
func setUpM365Approval(t *testing.T, ts *httptest.Server, admin *http.Client) {
	t.Helper()
	dir := filepath.Join("..", "examples", "m365-dienste")
	read := func(name string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(raw)
	}
	for _, id := range []string{"m365-team-bestellung", "m365-postfachzugriff-bestellung"} {
		body := fmt.Sprintf(`{"id":%q,"name":%q,"schema":%s}`, id, id, read(id+".form.json"))
		if code, out := cReq(t, admin, ts, "POST", "/api/v1/forms", body); code != http.StatusOK {
			t.Fatalf("save form %s: %d (%s)", id, code, out)
		}
	}
	for _, xml := range []string{read("genehmigung-vorgesetzter.bpmn"), m365StubProvisionBPMN, m365StubDeprovisionBPMN} {
		if code, b := cReq(t, admin, ts, "POST", "/api/v1/deployments", xml); code != http.StatusOK {
			t.Fatalf("deploy: %d (%s)", code, b)
		}
	}
}

type m365User struct {
	id     string
	client *http.Client
}

func createM365User(t *testing.T, ts *httptest.Server, admin *http.Client, username, displayName string) m365User {
	t.Helper()
	code, b := cReq(t, admin, ts, "POST", "/api/v1/users",
		fmt.Sprintf(`{"username":%q,"displayName":%q,"password":"password1"}`, username, displayName))
	if code != http.StatusCreated {
		t.Fatalf("create %s: %d (%s)", username, code, b)
	}
	c := newClient(t)
	if login(t, c, ts, username, "password1") != http.StatusOK {
		t.Fatalf("%s login failed", username)
	}
	return m365User{id: idOf(t, b), client: c}
}

// orderM365 offers one product bound to the example's approval and orders it with
// the given answers. The publish must not warn: every answer that names somebody is
// declared personal data in the approval model, which is what keeps it out of that
// model's history in the clear.
func orderM365(t *testing.T, ts *httptest.Server, admin *http.Client, item, form string, answers map[string]string) string {
	t.Helper()
	code, body := cReq(t, admin, ts, "POST", "/api/v1/catalogs",
		`{"rank":1,"languages":["de"],"texts":{"de":"Microsoft 365"}}`)
	if code != http.StatusCreated {
		t.Fatalf("create catalogue: %d (%s)", code, body)
	}
	cat := idOf(t, body)
	if code, b := cReq(t, admin, ts, "POST", "/api/v1/catalog-products",
		`{"id":"`+item+`","homeCatalog":"`+cat+`","state":"active","texts":{"de":"`+item+`"},`+
			`"approval":{"kind":"`+m365ApprovalProcess+`"},"configForm":"`+form+`",`+
			`"provisionProcess":"m365_stub_prov","deprovisionProcess":"m365_stub_deprov"}`,
	); code != http.StatusOK {
		t.Fatalf("save the product: %d (%s)", code, b)
	}
	if code, b := cReq(t, admin, ts, "PATCH", "/api/v1/catalogs/"+cat,
		`{"items":["`+item+`"]}`); code != http.StatusOK {
		t.Fatalf("offer it: %d (%s)", code, b)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/catalogs/"+cat+"/releases", "")
	if code != http.StatusCreated {
		t.Fatalf("publish: %d (%s)", code, body)
	}
	var rel struct {
		ID       string            `json:"id"`
		Warnings []json.RawMessage `json:"warnings"`
	}
	if err := json.Unmarshal(body, &rel); err != nil {
		t.Fatalf("decode the release: %v (%s)", err, body)
	}
	if len(rel.Warnings) != 0 {
		t.Fatalf("publishing warns about answers in the clear: %s", body)
	}
	config, err := json.Marshal(map[string]map[string]string{item: answers})
	if err != nil {
		t.Fatal(err)
	}
	code, body = cReq(t, admin, ts, "POST", "/api/v1/orders",
		`{"releaseId":"`+rel.ID+`","items":["`+item+`"],"config":`+string(config)+`}`)
	if code != http.StatusCreated {
		t.Fatalf("place the order: %d (%s)", code, body)
	}
	return idOf(t, body)
}

type m365GraphJob struct {
	JobKey     uint64 `json:"jobKey"`
	ElementID  string `json:"elementId"`
	LeaseToken uint64 `json:"leaseToken"`
	Connector  struct {
		Fields map[string]any `json:"fields"`
	} `json:"connector"`
}

// leaseM365Graph takes the "Vorgesetzten lesen" job the way an Entra worker does.
// Polled, because the approval starts only after the order's orchestration has made
// its own calls to this server.
func leaseM365Graph(t *testing.T, ts *httptest.Server, admin *http.Client) m365GraphJob {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		code, raw := cReq(t, admin, ts, "POST", "/api/v1/jobs/activate",
			`{"type":"io.atlas.entra","worker":"entra-test","leaseMs":60000,"maxJobs":1}`)
		if code != http.StatusOK {
			t.Fatalf("lease: %d (%s)", code, raw)
		}
		var out struct {
			Jobs []m365GraphJob `json:"jobs"`
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode lease: %v (%s)", err, raw)
		}
		if len(out.Jobs) == 1 {
			if out.Jobs[0].ElementID != "Graph" {
				t.Fatalf("leased a job of %q, want Graph", out.Jobs[0].ElementID)
			}
			return out.Jobs[0]
		}
		if time.Now().After(deadline) {
			_, inc := cReq(t, admin, ts, "GET", "/api/v1/incidents", "")
			t.Fatalf("no Entra job for the approval.\nincidents: %s", inc)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// completeM365Graph answers the job as the Entra worker does: get-user-manager's
// result variable holds the manager, or null when Entra ID names none.
func completeM365Graph(t *testing.T, ts *httptest.Server, admin *http.Client, job m365GraphJob, chef string) {
	t.Helper()
	if got := job.Connector.Fields["operation"]; got != "get-user-manager" {
		t.Fatalf("the Entra job asks for %v, want get-user-manager", got)
	}
	body := fmt.Sprintf(`{"worker":"entra-test","leaseToken":%d,"variables":{"chef":%s}}`, job.LeaseToken, chef)
	if code, b := cReq(t, admin, ts, "POST", fmt.Sprintf("/api/v1/jobs/%d/complete", job.JobKey), body); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("complete the Entra job: %d (%s)", code, b)
	}
}

type m365Task struct {
	Key                uint64 `json:"key"`
	ProcessInstanceKey uint64 `json:"processInstanceKey"`
	ProcessID          string `json:"processId"`
	ElementID          string `json:"elementId"`
	Assignee           string `json:"assignee"`
	CandidateGroups    string `json:"candidateGroups"`
	FormID             string `json:"formId"`
}

func awaitM365Task(t *testing.T, ts *httptest.Server, admin *http.Client, element string) m365Task {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		code, body := cReq(t, admin, ts, "GET", "/api/v1/tasks", "")
		if code == http.StatusOK {
			var tasks []m365Task
			if err := json.Unmarshal(listRows(t, body), &tasks); err != nil {
				t.Fatalf("decode tasks: %v (%s)", err, body)
			}
			for _, k := range tasks {
				if k.ProcessID == m365ApprovalProcess && k.ElementID == element {
					return k
				}
			}
		}
		if time.Now().After(deadline) {
			_, inc := cReq(t, admin, ts, "GET", "/api/v1/incidents", "")
			t.Fatalf("no %s task.\ntasks: %s\nincidents: %s", element, body, inc)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

type m365Line struct {
	Status     string `json:"status"`
	ApprovedBy string `json:"approvedBy"`
	DecidedBy  string `json:"decidedBy"`
	Reason     string `json:"reason"`
}

// awaitM365Line waits until the order's only line satisfies done: the approval's last
// step is a call to this server's own order API.
func awaitM365Line(t *testing.T, ts *httptest.Server, admin *http.Client, ord string, done func(m365Line) bool) m365Line {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		code, body := cReq(t, admin, ts, "GET", "/api/v1/orders/"+ord, "")
		if code == http.StatusOK {
			var o struct {
				Lines []m365Line `json:"lines"`
			}
			if err := json.Unmarshal(body, &o); err != nil {
				t.Fatalf("decode the order: %v (%s)", err, body)
			}
			if len(o.Lines) == 1 && done(o.Lines[0]) {
				return o.Lines[0]
			}
		}
		if time.Now().After(deadline) {
			_, inc := cReq(t, admin, ts, "GET", "/api/v1/incidents", "")
			t.Fatalf("the order line never got there.\norder: %s\nincidents: %s", body, inc)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func awaitM365Instance(t *testing.T, ts *httptest.Server, admin *http.Client, process string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		code, body := cReq(t, admin, ts, "GET", "/api/v1/instances", "")
		if code == http.StatusOK {
			var page struct {
				Items []struct {
					ProcessID string `json:"processId"`
				} `json:"items"`
			}
			if err := json.Unmarshal(body, &page); err != nil {
				t.Fatalf("decode instances: %v (%s)", err, body)
			}
			for _, i := range page.Items {
				if i.ProcessID == process {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			_, inc := cReq(t, admin, ts, "GET", "/api/v1/incidents", "")
			t.Fatalf("%s was never started.\nincidents: %s", process, inc)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
