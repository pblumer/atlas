package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/pblumer/atlas/model"
)

// What a worker did, job by job — and who may read it.
//
// The counters answer "how much"; these answer "which one, with what, and why did it
// fail". The last of those is the reason the ring exists at all: a failure that still
// has retries left raises no incident, so before this its message reached nobody.

// runsOf drives a lease and an outcome through the registry and reads back the ring.
func runsOf(t *testing.T, fn func(r *workerRegistry)) []jobRun {
	t.Helper()
	tick := int64(0)
	r := newWorkerRegistry(func() int64 { tick += 1_000_000; return tick })
	fn(r)
	return r.jobsOf("w1")
}

// The payoff: a failed job says what it was handed, what the worker reported, and how
// many attempts are left — none of which is anywhere else until the retries run out.
func TestAFailedJobSaysWhatItWasHandedAndWhyItFailed(t *testing.T) {
	runs := runsOf(t, func(r *workerRegistry) {
		r.recordLease("w1", []pulledJob{{
			JobKey: 7, Type: "io.atlas.mail.send", ProcessInstanceKey: 42, ElementID: "send",
			Variables: map[string]any{"to": "a@example.ch"},
		}})
		r.recordOutcome("w1", 7, jobRunFailed, "dial tcp: connection refused", 2, nil)
	})

	if len(runs) != 1 {
		t.Fatalf("runs = %d, want the one job", len(runs))
	}
	j := runs[0]
	if j.Outcome != jobRunFailed || j.Error != "dial tcp: connection refused" || j.Retries != 2 {
		t.Errorf("run = %+v, want the worker's own failure on it", j)
	}
	if !strings.Contains(j.In, "a@example.ch") {
		t.Errorf("in = %q, want the variables the engine handed over", j.In)
	}
	if j.SettledAt <= j.LeasedAt {
		t.Errorf("settledAt=%d leasedAt=%d, want a duration the view can show", j.SettledAt, j.LeasedAt)
	}
}

// A completed job carries what the worker sent back, which is what turns "it worked"
// into "it wrote this".
func TestACompletedJobCarriesWhatTheWorkerReturned(t *testing.T) {
	runs := runsOf(t, func(r *workerRegistry) {
		r.recordLease("w1", []pulledJob{{JobKey: 8, Type: "io.atlas.http.rest"}})
		r.recordOutcome("w1", 8, jobRunCompleted, "", 3, []model.VariableValue{
			{Name: "response", Kind: model.VarString, Text: "ok"},
		})
	})

	if len(runs) != 1 || runs[0].Outcome != jobRunCompleted {
		t.Fatalf("runs = %+v, want one completed job", runs)
	}
	if !strings.Contains(runs[0].Out, "response") || !strings.Contains(runs[0].Out, "ok") {
		t.Errorf("out = %q, want the returned variables", runs[0].Out)
	}
}

// A leased job appears immediately, before any outcome. A worker that took work and
// went quiet is exactly the case an operator opens this for, and a row that only
// appeared on completion would show nothing at all.
func TestAJobStillOutWithItsWorkerIsVisible(t *testing.T) {
	runs := runsOf(t, func(r *workerRegistry) {
		r.recordLease("w1", []pulledJob{{JobKey: 9, Type: "io.atlas.script.python"}})
	})

	if len(runs) != 1 || runs[0].Outcome != jobRunInFlight {
		t.Fatalf("runs = %+v, want one job still in flight", runs)
	}
	if runs[0].SettledAt != 0 {
		t.Errorf("settledAt = %d, want unset while it is still out", runs[0].SettledAt)
	}
}

// The ring is a tail, not a history: it holds the newest jobs and lets go of the rest,
// which is what keeps a busy worker from turning a debugging window into an
// unadministered copy of the instance data.
func TestTheRingKeepsTheNewestJobsAndLetsTheRestGo(t *testing.T) {
	runs := runsOf(t, func(r *workerRegistry) {
		for i := range jobRunsPerWorker + 10 {
			r.recordLease("w1", []pulledJob{{JobKey: uint64(i), Type: "t"}})
		}
	})

	if len(runs) != jobRunsPerWorker {
		t.Fatalf("runs = %d, want the ring's bound of %d", len(runs), jobRunsPerWorker)
	}
	// Newest first, and the oldest ten are gone.
	if runs[0].JobKey != uint64(jobRunsPerWorker+9) {
		t.Errorf("first run = %d, want the newest job", runs[0].JobKey)
	}
	if last := runs[len(runs)-1].JobKey; last != 10 {
		t.Errorf("last run = %d, want job 10 — the oldest still inside the bound", last)
	}
}

// An outcome for a job the ring has already let go of is dropped rather than opening a
// row with no variables and no history. Reporting it would put a row in the list that
// says less than the ones around it and claim a lease time it never saw.
func TestAnOutcomeForAnEvictedJobIsDropped(t *testing.T) {
	runs := runsOf(t, func(r *workerRegistry) {
		r.recordLease("w1", []pulledJob{{JobKey: 1, Type: "t"}})
		for i := range jobRunsPerWorker {
			r.recordLease("w1", []pulledJob{{JobKey: uint64(100 + i), Type: "t"}})
		}
		r.recordOutcome("w1", 1, jobRunCompleted, "", 0, nil)
	})

	for _, j := range runs {
		if j.JobKey == 1 {
			t.Fatalf("the evicted job came back as %+v", j)
		}
	}
}

// A worker nobody has seen has an empty list rather than a null: the console iterates
// it, and a worker that pulled but has not settled anything is a real case.
func TestAWorkerWithNoJobsListsNothing(t *testing.T) {
	r := newWorkerRegistry(nil)
	if got := r.jobsOf("never-seen"); got == nil || len(got) != 0 {
		t.Errorf("jobsOf = %#v, want an empty list", got)
	}
}

// Variables are clipped, and the clip says so. A row that showed a shortened value as
// if it were whole would be worse than one that showed nothing.
func TestOversizeVariablesAreClippedVisibly(t *testing.T) {
	runs := runsOf(t, func(r *workerRegistry) {
		r.recordLease("w1", []pulledJob{{
			JobKey: 1, Type: "io.atlas.csv-import",
			Variables: map[string]any{"rows": strings.Repeat("x", maxJobRunVars*2)},
		}})
	})

	if len(runs[0].In) > maxJobRunVars+128 {
		t.Errorf("in is %d bytes, want it bounded near %d", len(runs[0].In), maxJobRunVars)
	}
	if !strings.Contains(runs[0].In, "truncated") {
		t.Errorf("in = %q…, want the truncation marked in the text", runs[0].In[:80])
	}
}

// The gate: a job's variables are the process's own data, so the list is admin-only
// even though the Workers view around it is open to anyone who needs to see whether
// work is moving.
func TestWorkerJobsAreAdminOnly(t *testing.T) {
	srv, _ := newValidateServer(t, WithAuth())

	code, body := serveInternal(t, srv, http.MethodGet, "/api/v1/workers/w1/jobs", "", "")
	if code == http.StatusOK {
		t.Fatalf("an unauthenticated caller read a worker's job variables: %s", body)
	}
}

// And with no authentication configured at all it answers, like the rest of the
// console on such a server: the gate is a role check, not a second product.
func TestWorkerJobsAnswerOnAServerWithoutAuth(t *testing.T) {
	srv, _ := newValidateServer(t)
	srv.do(func() {
		srv.workers.recordLease("w1", []pulledJob{{JobKey: 1, Type: "io.atlas.mail.send"}})
	})

	code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/workers/w1/jobs", "", "")
	if code != http.StatusOK {
		t.Fatalf("GET worker jobs: status=%d body=%s", code, raw)
	}
	var out struct {
		Worker string   `json:"worker"`
		Jobs   []jobRun `json:"jobs"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode: %v (%s)", err, raw)
	}
	if out.Worker != "w1" || len(out.Jobs) != 1 || out.Jobs[0].Type != "io.atlas.mail.send" {
		t.Errorf("response = %+v", out)
	}
}
