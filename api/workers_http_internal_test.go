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
		Type            string `json:"type"`
		Index           int32  `json:"index"`
		ServedInProcess bool   `json:"servedInProcess"`
		BuiltIn         bool   `json:"builtIn"`
		Leasable        bool   `json:"leasable"`
		Parked          int64  `json:"parked"`
		InFlight        int64  `json:"inFlight"`
		Incidents       int64  `json:"incidents"`
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
				t.Error("the mail connector type is not marked as served in-process")
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
