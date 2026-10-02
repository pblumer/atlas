package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/opensearch"
)

// Confidential projects (ADR-draft-confidential-projects).
//
// Every test here is the same sentence from a different side: on a shared server,
// the operator of one team does not see — and cannot act on — the instances of a
// project another team marked confidential, while the team, its task holders, an
// admin and the worker protocol carry on as before.

// lohnPruefungBPMN waits at a user task offered to the group Reviewers, whose form
// asks for `comment` only. The instance also carries `secret`.
const lohnPruefungBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="lohnpruefung" isExecutable="true">
    <startEvent id="start"/>
    <userTask id="pruefen">
      <extensionElements>
        <zeebe:formDefinition formId="review-form"/>
        <zeebe:assignmentDefinition candidateGroups="Reviewers"/>
      </extensionElements>
    </userTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="pruefen"/>
    <sequenceFlow id="f2" sourceRef="pruefen" targetRef="end"/>
  </process>
</definitions>`

// serviceBPMN waits at a job of the given type, which no worker serves.
func serviceBPMN(processID, jobType string) string {
	return `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="` + processID + `" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="buchen">
      <extensionElements><zeebe:taskDefinition type="` + jobType + `"/></extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="buchen"/>
    <sequenceFlow id="f2" sourceRef="buchen" targetRef="end"/>
  </process>
</definitions>`
}

// shortBPMN finishes as soon as it starts.
const shortBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL">
  <process id="kurz" isExecutable="true">
    <startEvent id="start"/>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="end"/>
  </process>
</definitions>`

// confidentialWorld is one shared server: anna owns the application "Lohn", bert is
// an operator of another team, rita reviews payroll as a member of the group the
// task is offered to and of nothing else.
type confidentialWorld struct {
	ts                *httptest.Server
	admin, anna, bert *http.Client
	rita              *http.Client
	annaID, bertID    string
	project           string
	pruefungKey       uint64 // definition: the user task
	buchungKey        uint64 // definition: the job
	pruefung          uint64 // instance at the user task
	parked, waiting   uint64 // instances at the job: one with an incident, one not
	parkedElement     uint64 // the element instance holding the incident
	waitingJob        uint64
	bertsKey          uint64 // bert's own definition, ungrouped
	bertsInstance     uint64
}

func mustDo(t *testing.T, c *http.Client, ts *httptest.Server, method, path, body string, want int) []byte {
	t.Helper()
	code, out := cReq(t, c, ts, method, path, body)
	if code != want {
		t.Fatalf("%s %s = %d, want %d: %s", method, path, code, want, out)
	}
	return out
}

func startOne(t *testing.T, c *http.Client, ts *httptest.Server, defKey uint64, vars string) uint64 {
	t.Helper()
	before := activeKeys(t, c, ts, defKey)
	mustDo(t, c, ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", defKey), `{"variables":`+vars+`}`, http.StatusOK)
	for k := range activeKeys(t, c, ts, defKey) {
		if !before[k] {
			return k
		}
	}
	t.Fatalf("no new instance of %d", defKey)
	return 0
}

func activeKeys(t *testing.T, c *http.Client, ts *httptest.Server, defKey uint64) map[uint64]bool {
	t.Helper()
	out := mustDo(t, c, ts, "GET", fmt.Sprintf("/api/v1/instances?process=%d&state=active", defKey), "", http.StatusOK)
	var rows []struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(listRows(t, out), &rows); err != nil {
		t.Fatalf("decode instances: %v", err)
	}
	keys := map[uint64]bool{}
	for _, r := range rows {
		keys[r.Key] = true
	}
	return keys
}

func newConfidentialWorld(t *testing.T, opts ...api.Option) *confidentialWorld {
	t.Helper()
	ts, _ := newAuthServerWith(t, "admin", "password1", opts...)
	w := &confidentialWorld{ts: ts, admin: newClient(t)}
	login(t, w.admin, ts, "admin", "password1")
	w.annaID = createUser(t, w.admin, ts.URL, "anna")
	w.bertID = createUser(t, w.admin, ts.URL, "bert")
	ritaID := createUserWithRoles(t, w.admin, ts.URL, "rita", `["user"]`)
	grp := mustDo(t, w.admin, ts, "POST", "/api/v1/groups", `{"name":"Reviewers"}`, http.StatusCreated)
	var g struct{ ID string }
	_ = json.Unmarshal(grp, &g)
	if code, body := cReq(t, w.admin, ts, "PUT", "/api/v1/groups/"+g.ID+"/members/"+ritaID, ""); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("add rita to Reviewers = %d: %s", code, body)
	}
	w.anna = signInAs(t, ts.URL, "anna", "a-password-that-is-long")
	w.bert = signInAs(t, ts.URL, "bert", "a-password-that-is-long")
	w.rita = signInAs(t, ts.URL, "rita", "a-password-that-is-long")

	mustDo(t, w.admin, ts, "POST", "/api/v1/forms", reviewForm, http.StatusOK)
	w.project = createProjectAs(t, w.anna, ts.URL, "Lohn")
	saveDraftAs(t, w.anna, ts.URL, w.project, lohnPruefungBPMN)
	saveDraftAs(t, w.anna, ts.URL, w.project, serviceBPMN("lohnbuchung", "lohn-buchen"))
	out := mustDo(t, w.anna, ts, "POST", "/api/v1/projects/"+w.project+"/deploy", "", http.StatusOK)
	var dep struct {
		Definitions []struct {
			Key       uint64 `json:"key"`
			ProcessID string `json:"processId"`
		} `json:"definitions"`
	}
	if err := json.Unmarshal(out, &dep); err != nil {
		t.Fatalf("decode project deploy: %v (%s)", err, out)
	}
	for _, d := range dep.Definitions {
		switch d.ProcessID {
		case "lohnpruefung":
			w.pruefungKey = d.Key
		case "lohnbuchung":
			w.buchungKey = d.Key
		}
	}
	if w.pruefungKey == 0 || w.buchungKey == 0 {
		t.Fatalf("project deploy did not report both definitions: %s", out)
	}

	secret := `{"comment":"","secret":"lohn-4711"}`
	w.pruefung = startOne(t, w.anna, ts, w.pruefungKey, secret)
	w.parked = startOne(t, w.anna, ts, w.buchungKey, secret)
	w.waiting = startOne(t, w.anna, ts, w.buchungKey, secret)

	// One of the two jobs fails for good, which parks its token behind an incident.
	jobs := mustDo(t, w.admin, ts, "POST", "/api/v1/jobs/activate", `{"type":"lohn-buchen","worker":"w","maxJobs":2}`, http.StatusOK)
	var leased struct {
		Jobs []struct {
			JobKey             uint64 `json:"jobKey"`
			ProcessInstanceKey uint64 `json:"processInstanceKey"`
			ElementInstanceKey uint64 `json:"elementInstanceKey"`
			LeaseToken         uint64 `json:"leaseToken"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(jobs, &leased); err != nil || len(leased.Jobs) != 2 {
		t.Fatalf("lease both jobs: %v %s", err, jobs)
	}
	for _, j := range leased.Jobs {
		switch j.ProcessInstanceKey {
		case w.parked:
			w.parkedElement = j.ElementInstanceKey
			mustDo(t, w.admin, ts, "POST", fmt.Sprintf("/api/v1/jobs/%d/fail", j.JobKey),
				fmt.Sprintf(`{"retries":0,"message":"Konto gesperrt","worker":"w","leaseToken":%d}`, j.LeaseToken), http.StatusOK)
		case w.waiting:
			w.waitingJob = j.JobKey
			// Handed back, so it is activatable again: the job a worker would lease next.
			mustDo(t, w.admin, ts, "POST", fmt.Sprintf("/api/v1/jobs/%d/fail", j.JobKey),
				fmt.Sprintf(`{"retries":3,"message":"später","worker":"w","leaseToken":%d}`, j.LeaseToken), http.StatusOK)
		}
	}

	// Bert's own work, deployed ungrouped: the operator role still governs it.
	code, body := deployAs(t, w.bert, ts.URL, serviceBPMN("bertsarbeit", "bert-arbeit"))
	if code != http.StatusOK {
		t.Fatalf("bert deploys = %d: %s", code, body)
	}
	var bd struct{ Key uint64 }
	_ = json.Unmarshal([]byte(body), &bd)
	w.bertsKey = bd.Key
	w.bertsInstance = startOne(t, w.bert, ts, w.bertsKey, `{"x":1}`)
	return w
}

func (w *confidentialWorld) mark(t *testing.T, on bool) {
	t.Helper()
	mustDo(t, w.anna, w.ts, "PATCH", "/api/v1/projects/"+w.project, fmt.Sprintf(`{"confidential":%v}`, on), http.StatusOK)
}

// page decodes a listing's rows and total.
func page(t *testing.T, body []byte) (keys map[uint64]bool, total int) {
	t.Helper()
	var p struct {
		Items []struct {
			Key           uint64 `json:"key"`
			ProcessDefKey uint64 `json:"processDefKey"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("decode page: %v (%s)", err, body)
	}
	keys = map[uint64]bool{}
	for _, it := range p.Items {
		keys[it.Key] = true
	}
	return keys, p.Total
}

func TestAConfidentialProjectsInstancesAreHiddenFromAnotherTeamsOperator(t *testing.T) {
	w := newConfidentialWorld(t)
	ts := w.ts

	// Before the mark, the operator role means what it always meant.
	if keys, _ := page(t, mustDo(t, w.bert, ts, "GET", "/api/v1/instances?state=active", "", http.StatusOK)); !keys[w.pruefung] {
		t.Fatal("before the mark bert should see every instance, as an operator always has")
	}
	w.mark(t, true)

	t.Run("lists and totals", func(t *testing.T) {
		keys, total := page(t, mustDo(t, w.bert, ts, "GET", "/api/v1/instances?state=active", "", http.StatusOK))
		if keys[w.pruefung] || keys[w.parked] || keys[w.waiting] {
			t.Errorf("bert lists a confidential instance: %v", keys)
		}
		if !keys[w.bertsInstance] {
			t.Errorf("bert no longer sees his own instance: %v", keys)
		}
		if total != 1 {
			t.Errorf("total = %d, want 1: an exact total that counts what it hides is the hidden list by another name", total)
		}
		keys, total = page(t, mustDo(t, w.bert, ts, "GET", "/api/v1/instances", "", http.StatusOK))
		if len(keys) != 1 || total != 1 {
			t.Errorf("both halves = %v (total %d), want bert's one instance", keys, total)
		}
		keys, total = page(t, mustDo(t, w.bert, ts, "GET", fmt.Sprintf("/api/v1/instances?process=%d&state=active", w.buchungKey), "", http.StatusOK))
		if len(keys) != 0 || total != 0 {
			t.Errorf("scoped to a confidential definition = %v (total %d), want nothing", keys, total)
		}
		var sum []struct {
			ProcessDefKey uint64 `json:"processDefKey"`
		}
		_ = json.Unmarshal(mustDo(t, w.bert, ts, "GET", "/api/v1/instances/summary", "", http.StatusOK), &sum)
		for _, r := range sum {
			if r.ProcessDefKey == w.pruefungKey || r.ProcessDefKey == w.buchungKey {
				t.Errorf("the summary counts a confidential definition for bert: %+v", r)
			}
		}
		for _, q := range []string{"secret=lohn-4711", fmt.Sprint(w.pruefung)} {
			if keys, _ := page(t, mustDo(t, w.bert, ts, "GET", "/api/v1/instances/search?q="+q, "", http.StatusOK)); len(keys) != 0 {
				t.Errorf("search %q finds %v for bert", q, keys)
			}
		}
	})

	t.Run("one instance answers as missing", func(t *testing.T) {
		for _, path := range []string{"timeline", "jobs", "lifecycle", "variable-audit", "data-objects", "decisions", "variables"} {
			if code, body := cReq(t, w.bert, ts, "GET", fmt.Sprintf("/api/v1/instances/%d/%s", w.pruefung, path), ""); code != http.StatusNotFound {
				t.Errorf("%s = %d, want 404: %s", path, code, body)
			}
		}
		if code, _ := cReq(t, w.bert, ts, "GET", fmt.Sprintf("/api/v1/processes/%d/runtime", w.buchungKey), ""); code != http.StatusNotFound {
			t.Errorf("runtime of a confidential definition = %d, want 404", code)
		}
	})

	t.Run("no operator action reaches it", func(t *testing.T) {
		if code, body := cReq(t, w.bert, ts, "DELETE", fmt.Sprintf("/api/v1/instances/%d", w.pruefung), ""); code != http.StatusNotFound {
			t.Errorf("cancel = %d, want 404: %s", code, body)
		}
		out := mustDo(t, w.bert, ts, "POST", "/api/v1/instances/terminate", fmt.Sprintf(`{"keys":[%d]}`, w.waiting), http.StatusOK)
		if !strings.Contains(string(out), `"terminated":0`) {
			t.Errorf("terminate by key: %s", out)
		}
		if code, _ := cReq(t, w.bert, ts, "POST", "/api/v1/instances/terminate", fmt.Sprintf(`{"processDefKey":%d}`, w.buchungKey)); code != http.StatusNotFound {
			t.Errorf("terminate by definition = %d, want 404", code)
		}
		if code, _ := cReq(t, w.bert, ts, "POST", fmt.Sprintf("/api/v1/processes/%d/cancel-instances", w.buchungKey), ""); code != http.StatusNotFound {
			t.Errorf("cancel-instances = %d, want 404", code)
		}
		if code, _ := cReq(t, w.bert, ts, "POST", fmt.Sprintf("/api/v1/incidents/%d/resolve", w.parkedElement), `{}`); code != http.StatusNotFound {
			t.Errorf("resolve one incident = %d, want 404", code)
		}
		out = mustDo(t, w.bert, ts, "POST", "/api/v1/incidents/resolve", `{"type":"job"}`, http.StatusOK)
		if !strings.Contains(string(out), `"resolved":0`) {
			t.Errorf("bulk resolve reached a confidential incident: %s", out)
		}
		out = mustDo(t, w.bert, ts, "POST", "/api/v1/incidents/resolve", fmt.Sprintf(`{"keys":[%d]}`, w.parkedElement), http.StatusOK)
		if !strings.Contains(string(out), `"resolved":0`) || !strings.Contains(string(out), `"notFound":1`) {
			t.Errorf("resolve by key: %s", out)
		}
		if code, _ := cReq(t, w.bert, ts, "POST", fmt.Sprintf("/api/v1/jobs/%d/complete", w.waitingJob), `{"reason":"erledigt"}`); code != http.StatusNotFound {
			t.Errorf("complete a confidential job by hand = %d, want 404", code)
		}
		if code, _ := cReq(t, w.bert, ts, "POST", fmt.Sprintf("/api/v1/jobs/%d/fail", w.waitingJob), `{"retries":0}`); code != http.StatusNotFound {
			t.Errorf("fail a confidential job by hand = %d, want 404", code)
		}
		out = mustDo(t, w.bert, ts, "POST", "/api/v1/jobs/activate", `{"type":"lohn-buchen","maxJobs":5}`, http.StatusOK)
		if strings.Contains(string(out), "lohn-4711") || !strings.Contains(string(out), `"jobs":[]`) {
			t.Errorf("bert leased a confidential job, variables and all: %s", out)
		}
		// And all of it is still there.
		if keys := activeKeys(t, w.admin, ts, w.buchungKey); !keys[w.parked] || !keys[w.waiting] {
			t.Errorf("a refused action changed something: %v", keys)
		}
	})

	t.Run("incidents", func(t *testing.T) {
		out := mustDo(t, w.bert, ts, "GET", "/api/v1/incidents", "", http.StatusOK)
		if strings.Contains(string(out), "Konto gesperrt") {
			t.Errorf("bert lists a confidential incident: %s", out)
		}
		out = mustDo(t, w.bert, ts, "GET", "/api/v1/incidents/summary", "", http.StatusOK)
		if strings.Contains(string(out), "Konto gesperrt") || !strings.Contains(string(out), `"total":0`) {
			t.Errorf("the incident summary counts a confidential incident for bert: %s", out)
		}
		if out := mustDo(t, w.anna, ts, "GET", "/api/v1/incidents", "", http.StatusOK); !strings.Contains(string(out), "Konto gesperrt") {
			t.Errorf("the owner no longer sees her own incident: %s", out)
		}
	})

	t.Run("tasks", func(t *testing.T) {
		out := mustDo(t, w.bert, ts, "GET", "/api/v1/tasks", "", http.StatusOK)
		if strings.Contains(string(out), "lohnpruefung") {
			t.Errorf("bert lists a confidential task he was not offered: %s", out)
		}
		var tasks struct {
			Items []struct {
				Key uint64 `json:"key"`
			} `json:"items"`
		}
		_ = json.Unmarshal(mustDo(t, w.rita, ts, "GET", "/api/v1/tasks", "", http.StatusOK), &tasks)
		if len(tasks.Items) != 1 {
			t.Fatalf("rita, offered the task by group, sees %d tasks, want 1", len(tasks.Items))
		}
		task := tasks.Items[0].Key
		if code, _ := cReq(t, w.bert, ts, "GET", fmt.Sprintf("/api/v1/tasks/%d", task), ""); code != http.StatusNotFound {
			t.Errorf("bert reads the task by key = %d, want 404", code)
		}
		if code, _ := cReq(t, w.bert, ts, "POST", fmt.Sprintf("/api/v1/tasks/%d/claim", task), `{"assignee":"bert"}`); code != http.StatusNotFound {
			t.Errorf("bert claims it = %d, want 404", code)
		}
		code, vars := instanceVarsAs(t, w.rita, ts.URL, w.pruefung)
		if code != http.StatusOK || vars["secret"] != nil {
			t.Errorf("rita's form fields = %d %v, want comment and not secret", code, vars)
		}
		if _, ok := vars["comment"]; !ok {
			t.Errorf("rita lost the field her form asks for: %v", vars)
		}
	})

	t.Run("members, admins and workers carry on", func(t *testing.T) {
		for name, c := range map[string]*http.Client{"anna": w.anna, "admin": w.admin} {
			keys, total := page(t, mustDo(t, c, ts, "GET", "/api/v1/instances?state=active", "", http.StatusOK))
			if !keys[w.pruefung] || !keys[w.bertsInstance] || total != 4 {
				t.Errorf("%s sees %v (total %d), want all four", name, keys, total)
			}
			if code, vars := instanceVarsAs(t, c, ts.URL, w.pruefung); code != http.StatusOK || vars["secret"] != "lohn-4711" {
				t.Errorf("%s reads %d %v, want the whole instance", name, code, vars)
			}
		}
		tok := mustDo(t, w.admin, ts, "POST", "/api/v1/api-tokens", `{"name":"lohn-worker","scope":"worker"}`, http.StatusOK)
		var minted struct {
			Token string `json:"token"`
		}
		_ = json.Unmarshal(tok, &minted)
		code, out := bearerReq(t, ts, "POST", "/api/v1/jobs/activate", `{"type":"lohn-buchen","worker":"lohn"}`, minted.Token)
		if code != http.StatusOK || !strings.Contains(string(out), "lohn-4711") {
			t.Errorf("the worker protocol stopped serving a confidential process: %d %s", code, out)
		}
	})

	t.Run("removing the mark restores the operator's view", func(t *testing.T) {
		w.mark(t, false)
		if keys, _ := page(t, mustDo(t, w.bert, ts, "GET", "/api/v1/instances?state=active", "", http.StatusOK)); !keys[w.pruefung] {
			t.Errorf("bert still does not see the instance after the mark was removed: %v", keys)
		}
		audit := mustDo(t, w.anna, ts, "GET", "/api/v1/projects/"+w.project+"/audit", "", http.StatusOK)
		if strings.Count(string(audit), `"action":"confidential"`) != 2 {
			t.Errorf("marking and unmarking are access changes and must both be audited: %s", audit)
		}
	})
}

func TestOnlyTheOwnerMarksAProjectConfidential(t *testing.T) {
	w := newConfidentialWorld(t)
	shareBody := `{"role":"editor","type":"user"}`
	if code, body := cReq(t, w.anna, w.ts, "PUT", "/api/v1/projects/"+w.project+"/members/"+w.bertID, shareBody); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("share as editor = %d: %s", code, body)
	}
	if code, body := cReq(t, w.bert, w.ts, "PATCH", "/api/v1/projects/"+w.project, `{"confidential":true}`); code != http.StatusForbidden {
		t.Fatalf("an editor marked the project = %d, want 403: %s", code, body)
	}
	w.mark(t, true)
	var view struct {
		Confidential bool     `json:"confidential"`
		Warnings     []string `json:"warnings"`
	}
	_ = json.Unmarshal(mustDo(t, w.anna, w.ts, "PATCH", "/api/v1/projects/"+w.project, `{"confidential":true}`, http.StatusOK), &view)
	if !view.Confidential || len(view.Warnings) != 0 {
		t.Errorf("view = %+v: marked, and no export warning on a server that exports nothing", view)
	}
	// A member sees the instances of the project they belong to, whatever the mark.
	if keys, _ := page(t, mustDo(t, w.bert, w.ts, "GET", "/api/v1/instances?state=active", "", http.StatusOK)); !keys[w.pruefung] {
		t.Errorf("an editor of the project lost its instances: %v", keys)
	}
}

func TestMarkingConfidentialOnAnExportingServerSaysTheIndexIsNotCovered(t *testing.T) {
	w := newConfidentialWorld(t, api.WithOpenSearchExporter(opensearch.Config{URL: "http://127.0.0.1:1", Index: "atlas-test"}))
	var view struct {
		Warnings []string `json:"warnings"`
	}
	_ = json.Unmarshal(mustDo(t, w.anna, w.ts, "PATCH", "/api/v1/projects/"+w.project, `{"confidential":true}`, http.StatusOK), &view)
	if len(view.Warnings) != 1 || !strings.Contains(view.Warnings[0], "OpenSearch") {
		t.Errorf("warnings = %v, want the one about the exporter", view.Warnings)
	}
}

func TestAConfidentialProjectIsNotDeletedWhileMarked(t *testing.T) {
	w := newConfidentialWorld(t)
	w.mark(t, true)
	if code, body := cReq(t, w.anna, w.ts, "DELETE", "/api/v1/projects/"+w.project, ""); code != http.StatusConflict {
		t.Fatalf("delete while marked = %d, want 409: %s", code, body)
	}
	w.mark(t, false)
	if code, body := cReq(t, w.anna, w.ts, "DELETE", "/api/v1/projects/"+w.project, ""); code != http.StatusNoContent {
		t.Fatalf("delete after unmarking = %d, want 204: %s", code, body)
	}
}

// A deleted definition leaves its finished instances behind. While the project is
// marked they stay where they were: out of sight.
func TestADeletedDefinitionsHistoryStaysConfidential(t *testing.T) {
	w := newConfidentialWorld(t)
	saveDraftAs(t, w.anna, w.ts.URL, w.project, shortBPMN)
	out := mustDo(t, w.anna, w.ts, "POST", "/api/v1/projects/"+w.project+"/deploy", "", http.StatusOK)
	var dep struct {
		Definitions []struct {
			Key       uint64 `json:"key"`
			ProcessID string `json:"processId"`
		} `json:"definitions"`
	}
	_ = json.Unmarshal(out, &dep)
	var kurz uint64
	for _, d := range dep.Definitions {
		if d.ProcessID == "kurz" {
			kurz = d.Key
		}
	}
	if kurz == 0 {
		t.Fatalf("kurz not deployed: %s", out)
	}
	mustDo(t, w.anna, w.ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", kurz), `{"variables":{"secret":"lohn-4711"}}`, http.StatusOK)
	w.mark(t, true)
	mustDo(t, w.anna, w.ts, "DELETE", fmt.Sprintf("/api/v1/processes/%d", kurz), "", http.StatusNoContent)

	finished := func(c *http.Client) bool {
		var p struct {
			Items []struct {
				ProcessDefKey uint64 `json:"processDefKey"`
			} `json:"items"`
		}
		_ = json.Unmarshal(mustDo(t, c, w.ts, "GET", "/api/v1/instances?state=finished", "", http.StatusOK), &p)
		for _, it := range p.Items {
			if it.ProcessDefKey == kurz {
				return true
			}
		}
		return false
	}
	if finished(w.bert) {
		t.Error("deleting the definition published its history to every operator")
	}
	if !finished(w.admin) {
		t.Error("the admin lost the finished instance")
	}
}

func TestOnlyTheOwnerMovesADefinitionOutOfAConfidentialProject(t *testing.T) {
	w := newConfidentialWorld(t)
	if code, body := cReq(t, w.anna, w.ts, "PUT", "/api/v1/projects/"+w.project+"/members/"+w.bertID, `{"role":"editor","type":"user"}`); code != http.StatusOK && code != http.StatusNoContent {
		t.Fatalf("share = %d: %s", code, body)
	}
	w.mark(t, true)
	if code, body := cReq(t, w.bert, w.ts, "PATCH", fmt.Sprintf("/api/v1/processes/%d", w.buchungKey), `{"projectId":""}`); code != http.StatusForbidden {
		t.Fatalf("an editor moved a definition out of the confidential project = %d, want 403: %s", code, body)
	}
	mustDo(t, w.anna, w.ts, "PATCH", fmt.Sprintf("/api/v1/processes/%d", w.buchungKey), `{"projectId":""}`, http.StatusOK)
}

// The index is rebuilt from the records at startup. A server that came back with it
// empty would answer its first requests with every confidential project open.
func TestTheMarkSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	first := newServerOn(t, dir)
	admin := signedInClient(t, first.URL)
	createUser(t, admin, first.URL, "anna")
	createUser(t, admin, first.URL, "bert")
	anna := signInAs(t, first.URL, "anna", "a-password-that-is-long")
	pid := createProjectAs(t, anna, first.URL, "Lohn")
	saveDraftAs(t, anna, first.URL, pid, serviceBPMN("lohnbuchung", "lohn-buchen"))
	if code, body := postAs(t, anna, first.URL+"/api/v1/projects/"+pid+"/deploy", ""); code != http.StatusOK {
		t.Fatalf("deploy = %d: %s", code, body)
	}
	var defs []struct{ Key uint64 }
	resp, err := anna.Get(first.URL + "/api/v1/processes")
	if err != nil {
		t.Fatal(err)
	}
	_ = json.NewDecoder(resp.Body).Decode(&defs)
	resp.Body.Close()
	if len(defs) != 1 {
		t.Fatalf("processes = %+v", defs)
	}
	if code, body := postAs(t, anna, fmt.Sprintf("%s/api/v1/processes/%d/instances", first.URL, defs[0].Key), `{"variables":{"secret":"lohn-4711"}}`); code != http.StatusOK {
		t.Fatalf("start = %d: %s", code, body)
	}
	req, _ := http.NewRequest(http.MethodPatch, first.URL+"/api/v1/projects/"+pid, strings.NewReader(`{"confidential":true}`))
	req.Header.Set("Content-Type", "application/json")
	if resp, err := anna.Do(req); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("mark: %v %v", err, resp)
	}
	first.stop()

	ts := newServerOn(t, dir)
	bert := signInAs(t, ts.URL, "bert", "a-password-that-is-long")
	resp, err = bert.Get(ts.URL + "/api/v1/instances/search?q=secret=lohn-4711")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var found struct {
		Items []json.RawMessage `json:"items"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&found)
	if resp.StatusCode != http.StatusOK || len(found.Items) != 0 {
		t.Errorf("after a restart bert finds %d confidential instances (HTTP %d)", len(found.Items), resp.StatusCode)
	}
}

// The Data view's cross-instance listing reads every instance's data objects; a
// confidential project's are not among them for somebody outside it.
func TestTheDataViewLeavesOutAConfidentialProjectsObjects(t *testing.T) {
	w := newConfidentialWorld(t)
	saveDraftAs(t, w.anna, w.ts.URL, w.project, dataObjectBPMN)
	mustDo(t, w.anna, w.ts, "POST", "/api/v1/projects/"+w.project+"/deploy", "", http.StatusOK)
	var defs []struct {
		Key       uint64 `json:"key"`
		ProcessID string `json:"processId"`
	}
	_ = json.Unmarshal(mustDo(t, w.anna, w.ts, "GET", "/api/v1/processes", "", http.StatusOK), &defs)
	var withData uint64
	for _, d := range defs {
		if d.ProcessID == "withdata" {
			withData = d.Key
		}
	}
	if withData == 0 {
		t.Fatal("withdata not deployed")
	}
	mustDo(t, w.anna, w.ts, "POST", fmt.Sprintf("/api/v1/processes/%d/instances", withData), `{"variables":{}}`, http.StatusOK)
	w.mark(t, true)

	objects := func(c *http.Client) int {
		var out struct {
			Objects []struct {
				ProcessDefKey uint64 `json:"processDefKey"`
			} `json:"objects"`
		}
		_ = json.Unmarshal(mustDo(t, c, w.ts, "GET", "/api/v1/data-objects", "", http.StatusOK), &out)
		n := 0
		for _, o := range out.Objects {
			if o.ProcessDefKey == withData {
				n++
			}
		}
		return n
	}
	if n := objects(w.bert); n != 0 {
		t.Errorf("bert reads %d data objects of a confidential instance", n)
	}
	if n := objects(w.anna); n == 0 {
		t.Error("the owner lost her own data objects")
	}
}
