package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/pblumer/atlas/compiler"
)

type workersResp struct {
	Workers []workerStat `json:"workers"`
	Types   []struct {
		Type            string     `json:"type"`
		Index           int32      `json:"index"`
		ServedInProcess bool       `json:"servedInProcess"`
		BuiltIn         bool       `json:"builtIn"`
		Leasable        bool       `json:"leasable"`
		Parked          int64      `json:"parked"`
		InFlight        int64      `json:"inFlight"`
		Incidents       int64      `json:"incidents"`
		Processes       []typeUser `json:"processes"`
	} `json:"types"`
}

func workers(t *testing.T, srv *Server) workersResp {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/workers", "", "")
	if code != http.StatusOK {
		t.Fatalf("GET /workers: status=%d body=%s", code, raw)
	}
	var out workersResp
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode workers: %v (%s)", err, raw)
	}
	return out
}

func typeRow(t *testing.T, resp workersResp, name string) (int64, int64, bool) {
	t.Helper()
	for _, row := range resp.Types {
		if row.Type == name {
			return row.Parked, row.InFlight, row.ServedInProcess
		}
	}
	t.Fatalf("no row for job type %q in %+v", name, resp.Types)
	return 0, 0, false
}

// TestWorkersViewShowsQueueDepthBeforeAnyWorkerExists is the state an operator
// most needs to recognize and today cannot: work is piling up and *nobody is
// serving it*. The type is listed with its queue depth and no worker against it.
func TestWorkersViewShowsQueueDepthBeforeAnyWorkerExists(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	for range 2 {
		if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/processes/1/instances", "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("start instance: status=%d body=%s", code, body)
		}
	}

	got := workers(t, srv)
	if len(got.Workers) != 0 {
		t.Errorf("workers = %+v, want none before any pull", got.Workers)
	}
	parked, inFlight, served := typeRow(t, got, "send-email")
	if parked != 3 {
		t.Errorf("parked = %d, want 3", parked)
	}
	if inFlight != 0 {
		t.Errorf("inFlight = %d, want 0", inFlight)
	}
	if served {
		t.Error("servedInProcess = true for a model-authored type, want false")
	}
}

// Once a worker pulls, the view attributes the work: who has it, what they took,
// and how the queue moved.
func TestWorkersViewAttributesLeasedWork(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	if code, _ := pull(t, srv, `{"type":"send-email","worker":"mailer-1"}`); code != http.StatusOK {
		t.Fatalf("pull: status=%d", code)
	}

	got := workers(t, srv)
	if len(got.Workers) != 1 {
		t.Fatalf("workers = %+v, want 1", got.Workers)
	}
	w := got.Workers[0]
	if w.Worker != "mailer-1" {
		t.Errorf("worker = %q, want mailer-1", w.Worker)
	}
	if w.Pulled != 1 || w.Leased != 1 || w.Completed != 0 {
		t.Errorf("pulled/leased/completed = %d/%d/%d, want 1/1/0", w.Pulled, w.Leased, w.Completed)
	}
	if w.Types["send-email"] != 1 {
		t.Errorf("types = %v, want send-email=1", w.Types)
	}
	parked, inFlight, _ := typeRow(t, got, "send-email")
	if parked != 0 || inFlight != 1 {
		t.Errorf("parked/inFlight = %d/%d, want 0/1", parked, inFlight)
	}
}

// A completion moves the job out of flight and into the processed count — the
// "wie viele wurden verarbeitet" half of the view.
func TestWorkersViewCountsCompletions(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	_, pulled := pull(t, srv, `{"type":"send-email","worker":"mailer-1"}`)
	if len(pulled.Jobs) != 1 {
		t.Fatalf("pulled %d jobs, want 1", len(pulled.Jobs))
	}
	path := fmt.Sprintf("/api/v1/jobs/%d/complete", pulled.Jobs[0].JobKey)
	done := fmt.Sprintf(`{"worker":"mailer-1","leaseToken":%d}`, pulled.Jobs[0].LeaseToken)
	if code, body := serveInternal(t, srv, http.MethodPost, path, done, "application/json"); code != http.StatusOK {
		t.Fatalf("complete: status=%d body=%s", code, body)
	}

	got := workers(t, srv)
	w := got.Workers[0]
	if w.Completed != 1 || w.Leased != 0 {
		t.Errorf("completed/leased = %d/%d, want 1/0", w.Completed, w.Leased)
	}
	if _, inFlight, _ := typeRow(t, got, "send-email"); inFlight != 0 {
		t.Errorf("inFlight = %d after the completion, want 0", inFlight)
	}
}

// A worker that fails a job until its retries are gone leaves an incident, and the
// view has to say so against the type — an operator looking at a stalled queue
// needs to see that the work is failing, not merely waiting.
func TestWorkersViewCountsFailuresAndIncidents(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	_, pulled := pull(t, srv, `{"type":"send-email","worker":"mailer-1"}`)
	if len(pulled.Jobs) != 1 {
		t.Fatalf("pulled %d jobs, want 1", len(pulled.Jobs))
	}
	// Retries to zero raises the incident (ADR-0061).
	path := fmt.Sprintf("/api/v1/jobs/%d/fail", pulled.Jobs[0].JobKey)
	body := fmt.Sprintf(`{"retries":0,"message":"smtp unreachable","worker":"mailer-1","leaseToken":%d}`, pulled.Jobs[0].LeaseToken)
	if code, b := serveInternal(t, srv, http.MethodPost, path, body, "application/json"); code != http.StatusOK {
		t.Fatalf("fail: status=%d body=%s", code, b)
	}

	got := workers(t, srv)
	w := got.Workers[0]
	if w.Failed != 1 || w.Leased != 0 {
		t.Errorf("failed/leased = %d/%d, want 1/0", w.Failed, w.Leased)
	}
	var incidents int64
	for _, row := range got.Types {
		if row.Type == "send-email" {
			incidents = row.Incidents
		}
	}
	if incidents != 1 {
		t.Errorf("incidents against send-email = %d, want 1", incidents)
	}
}

// A type an in-process worker serves is marked as such: it is the answer to "why
// is nothing external picking this up", and it is the flag that says relocating
// the kind means unregistering that handler (ADR-0157).
func TestWorkersViewMarksInProcessTypes(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	got := workers(t, srv)
	var found bool
	for _, row := range got.Types {
		if row.Type == compiler.MailJobType {
			found = true
			if !row.ServedInProcess {
				t.Error("the mail worker type is not marked as served in-process")
			}
		}
	}
	if !found {
		t.Errorf("no row for the reserved mail job type in %+v", got.Types)
	}
}

// The view has to tell a type nothing is *meant* to serve apart from one nobody
// *is* serving. An in-process type and a user task are both refused by the pull, so
// neither may be drawn as an unserved queue; a model-authored type is the only kind
// an external worker can take.
func TestWorkersViewMarksWhichTypesAWorkerMayLease(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	byType := map[string]struct{ builtIn, leasable, inProcess bool }{}
	for _, row := range workers(t, srv).Types {
		byType[row.Type] = struct{ builtIn, leasable, inProcess bool }{row.BuiltIn, row.Leasable, row.ServedInProcess}
	}
	for _, tc := range []struct {
		name                         string
		builtIn, leasable, inProcess bool
	}{
		{"send-email", false, true, false},
		{compiler.MailJobType, true, false, true},
		{compiler.UserTaskJobType, true, false, false},
	} {
		got, ok := byType[tc.name]
		if !ok {
			t.Errorf("no row for %q", tc.name)
			continue
		}
		if got.builtIn != tc.builtIn || got.leasable != tc.leasable || got.inProcess != tc.inProcess {
			t.Errorf("%q: builtIn/leasable/inProcess = %v/%v/%v, want %v/%v/%v",
				tc.name, got.builtIn, got.leasable, got.inProcess, tc.builtIn, tc.leasable, tc.inProcess)
		}
	}
}

// TestWorkersViewNamesTheProcessesThatUseAType is what makes a row actionable on a
// real installation: an engine with fifty job types has fifty "nobody" rows, and
// none of them says anything until you know which process is waiting on it.
func TestWorkersViewNamesTheProcessesThatUseAType(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	orders := deployBPMN(t, h, jobTypeBPMN("orders", "send-email"))
	newsletter := deployBPMN(t, h, jobTypeBPMN("newsletter", "send-email"))
	deployBPMN(t, h, jobTypeBPMN("billing", "charge-card"))

	byType := map[string][]typeUser{}
	for _, row := range workers(t, srv).Types {
		byType[row.Type] = row.Processes
	}

	// A type two definitions use names both of them, with the key to link to.
	sendEmail := byType["send-email"]
	if len(sendEmail) != 2 {
		t.Fatalf("send-email is used by %d processes, want 2 (%+v)", len(sendEmail), sendEmail)
	}
	got := map[string]uint64{}
	for _, u := range sendEmail {
		got[u.ProcessID] = u.ProcessDefKey
	}
	if got["orders"] != orders || got["newsletter"] != newsletter {
		t.Errorf("send-email users = %+v, want orders=%d newsletter=%d", got, orders, newsletter)
	}
	if len(byType["charge-card"]) != 1 {
		t.Errorf("charge-card is used by %d processes, want 1", len(byType["charge-card"]))
	}
	// A built-in nothing deployed uses names nobody, rather than every process.
	if users := byType[compiler.ScimJobType]; len(users) != 0 {
		t.Errorf("an unused built-in lists %d processes, want none (%+v)", len(users), users)
	}
}

// Only the newest version of a definition is listed. An engine keeps every version
// it ever deployed, and listing all of them turns one useful link into a column of
// near-identical ones.
func TestWorkersViewListsOnlyTheNewestVersionOfAProcess(t *testing.T) {
	srv := newServerForErrors(t)
	h := srv.Handler()
	deployBPMN(t, h, jobTypeBPMN("orders", "send-email"))
	newest := deployBPMN(t, h, jobTypeBPMN("orders", "send-email"))

	for _, row := range workers(t, srv).Types {
		if row.Type != "send-email" {
			continue
		}
		if len(row.Processes) != 1 {
			t.Fatalf("two versions of one process listed %d entries, want 1 (%+v)", len(row.Processes), row.Processes)
		}
		if row.Processes[0].ProcessDefKey != newest {
			t.Errorf("listed definition key %d, want the newest %d", row.Processes[0].ProcessDefKey, newest)
		}
		if row.Processes[0].Version != 2 {
			t.Errorf("listed version %d, want 2", row.Processes[0].Version)
		}
		return
	}
	t.Fatal("no row for send-email")
}

// TestRestartWorkerEndpoint covers the three answers the restart button can get. A
// server that supervises nothing says so with 409 (the worker runs elsewhere, so it
// is restarted elsewhere); a server that supervises the named worker restarts it and
// reports 200; and a name it does not supervise is a 404. The restart itself goes
// through the same supervisor path a crash would, so the handler only has to route
// the outcome.
func TestRestartWorkerEndpoint(t *testing.T) {
	// No supervisor at all: the honest 409.
	srv, _ := newValidateServer(t)
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/workers/mailer-1/restart", "", ""); code != http.StatusConflict {
		t.Fatalf("restart with no supervisor = %d %s, want 409", code, body)
	}

	// A server that supervises one worker. The supervisor is wired up the way the
	// server's own constructor does it, but nothing is started — restart only cycles
	// the stop channel, so no child process is spawned.
	quit := make(chan struct{})
	defer close(quit)
	sup := newSupervisor(quit)
	sup.add(SuperviseSpec{ID: "mailer-1", Kinds: []string{"send-email"}}, []string{"worker"}, nil)
	srv.supervisor = sup
	if n := sup.count(); n != 1 {
		t.Fatalf("supervised worker count = %d, want 1", n)
	}

	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/workers/mailer-1/restart", "", "")
	if code != http.StatusOK {
		t.Fatalf("restart a supervised worker = %d %s, want 200", code, body)
	}
	var resp struct {
		ID         string `json:"id"`
		Restarting bool   `json:"restarting"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode restart response: %v (%s)", err, body)
	}
	if resp.ID != "mailer-1" || !resp.Restarting {
		t.Errorf("restart response = %+v, want mailer-1 restarting", resp)
	}

	// An unknown worker on a server that does supervise: 404, not 409.
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/workers/nope/restart", "", ""); code != http.StatusNotFound {
		t.Fatalf("restart an unknown worker = %d %s, want 404", code, body)
	}
}

// The Processes column is what makes a row actionable — an engine with fifty job types
// has fifty rows saying "nobody", and none of them means anything until you can see
// which process is waiting on it. It used to be filled from service and send tasks
// alone, on the reasoning that those are the only elements carrying a job type the model
// authored. That is true of the string and false of the job: a task creates a
// job under a reserved type, and so do script, business-rule and user tasks. So every
// Jira, clio, AD, script and DMN row said nothing about who uses it.
//
// This deploys a process whose only job-creating element is a Jira task and
// asks the view who uses io.atlas.jira.
func TestWorkersViewNamesTheProcessesBehindAConnectorJobType(t *testing.T) {
	srv := newServerWithOptions(t)
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", jiraUsersBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}

	got := workers(t, srv)
	for _, row := range got.Types {
		if row.Type != compiler.JiraJobType {
			continue
		}
		if len(row.Processes) != 1 {
			t.Fatalf("processes for %s = %+v, want the one definition that uses it", row.Type, row.Processes)
		}
		if row.Processes[0].ProcessID != "jira-users" {
			t.Errorf("process = %+v, want the deployed definition named", row.Processes[0])
		}
		return
	}
	t.Fatalf("no row for %s in %+v", compiler.JiraJobType, got.Types)
}

// A user task creates a job too, under the reserved user-task type, and the same gap
// left its row unattributed — which matters more than most, because a user-task queue
// is the one an operator reads as "people have work waiting".
func TestWorkersViewNamesTheProcessesBehindTheUserTaskType(t *testing.T) {
	srv := newServerWithOptions(t)
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", jiraUsersBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	got := workers(t, srv)
	for _, row := range got.Types {
		if row.Type != compiler.UserTaskJobType {
			continue
		}
		if len(row.Processes) != 1 || row.Processes[0].ProcessID != "jira-users" {
			t.Errorf("processes for %s = %+v, want the deployed definition named", row.Type, row.Processes)
		}
		return
	}
	t.Fatalf("no row for %s in %+v", compiler.UserTaskJobType, got.Types)
}

// jiraUsersBPMN has one task and one user task, so a single deploy exercises
// both of the node types the users map used to walk past.
const jiraUsersBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="jira-users" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:jiraConnector connector="acme" operation="create-issue" project="OPS"
                             issueType="Task" summary="ein Titel" resultVariable="ticket"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:userTask id="u" name="freigeben"/>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="u"/>
    <bpmn:sequenceFlow id="f3" sourceRef="u" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
