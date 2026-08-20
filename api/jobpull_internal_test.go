package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// pullResp is the shape POST /api/v1/jobs/activate answers with.
type pullResp struct {
	Jobs []struct {
		JobKey             uint64         `json:"jobKey"`
		Type               string         `json:"type"`
		ProcessInstanceKey uint64         `json:"processInstanceKey"`
		ElementInstanceKey uint64         `json:"elementInstanceKey"`
		ProcessDefKey      uint64         `json:"processDefKey"`
		ElementID          string         `json:"elementId"`
		Retries            int32          `json:"retries"`
		LeaseExpiresAt     int64          `json:"leaseExpiresAt"`
		Variables          map[string]any `json:"variables"`
	} `json:"jobs"`
}

func pull(t *testing.T, srv *Server, body string) (int, pullResp) {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodPost, "/api/v1/jobs/activate", body, "application/json")
	var out pullResp
	if code == http.StatusOK {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("decode pull: %v (%s)", err, raw)
		}
	}
	return code, out
}

// jobPullSrv deploys one service-task process with the given model-authored job
// type, starts an instance carrying variables, and returns the ready server.
func jobPullSrv(t *testing.T, jobType string, vars string) *Server {
	t.Helper()
	srv := newServerForErrors(t)
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", jobTypeBPMN("orders", jobType), "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var dep struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &dep); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if code, body := serveInternal(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/processes/%d/instances", dep.Key), `{"variables":`+vars+`}`, "application/json"); code != http.StatusOK {
		t.Fatalf("start instance: status=%d body=%s", code, body)
	}
	return srv
}

// TestPullByTypeLeasesTheJob is the endpoint ADR-0007 owed: a worker asks for the
// next job of a *named* type and gets it, leased, with everything it needs to do
// the work — including the variables visible at the task, so it need not make a
// second call and race a concurrent write.
func TestPullByTypeLeasesTheJob(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{"to":"a@example.com","count":2}`)

	code, got := pull(t, srv, `{"type":"send-email","worker":"w1","maxJobs":5}`)
	if code != http.StatusOK {
		t.Fatalf("pull: status=%d", code)
	}
	if len(got.Jobs) != 1 {
		t.Fatalf("pulled %d jobs, want 1", len(got.Jobs))
	}
	j := got.Jobs[0]
	if j.Type != "send-email" {
		t.Errorf("type = %q, want send-email", j.Type)
	}
	if j.JobKey == 0 || j.ProcessInstanceKey == 0 || j.ElementInstanceKey == 0 {
		t.Errorf("job identity incomplete: %+v", j)
	}
	if j.ElementID != "t" {
		t.Errorf("elementId = %q, want t", j.ElementID)
	}
	if j.Retries != 3 {
		t.Errorf("retries = %d, want 3", j.Retries)
	}
	if j.LeaseExpiresAt == 0 {
		t.Error("leaseExpiresAt = 0, want the lease the engine froze")
	}
	if j.Variables["to"] != "a@example.com" {
		t.Errorf("variables = %v, want the instance's to=a@example.com", j.Variables)
	}
}

// A leased job is off the offer: the second worker to ask must not be handed work
// the first is already doing.
func TestPullByTypeDoesNotHandOutALeasedJob(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)

	if code, first := pull(t, srv, `{"type":"send-email","worker":"w1"}`); code != http.StatusOK || len(first.Jobs) != 1 {
		t.Fatalf("first pull: status=%d jobs=%d, want 200 and 1", code, len(first.Jobs))
	}
	code, second := pull(t, srv, `{"type":"send-email","worker":"w2"}`)
	if code != http.StatusOK {
		t.Fatalf("second pull: status=%d", code)
	}
	if len(second.Jobs) != 0 {
		t.Errorf("second worker was handed %d job(s) already leased by the first", len(second.Jobs))
	}
}

// maxJobs bounds a batch, and defaults to one job so a naive worker cannot
// accidentally lease an entire backlog it has no capacity for.
func TestPullByTypeRespectsMaxJobs(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	// A second and third instance of the same definition, so three jobs are parked.
	for range 2 {
		if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/processes/1/instances", "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("start extra instance: status=%d body=%s", code, body)
		}
	}

	code, one := pull(t, srv, `{"type":"send-email","worker":"w1"}`)
	if code != http.StatusOK || len(one.Jobs) != 1 {
		t.Fatalf("default pull: status=%d jobs=%d, want 200 and 1", code, len(one.Jobs))
	}
	code, rest := pull(t, srv, `{"type":"send-email","worker":"w2","maxJobs":10}`)
	if code != http.StatusOK {
		t.Fatalf("batch pull: status=%d", code)
	}
	if len(rest.Jobs) != 2 {
		t.Errorf("batch pull returned %d jobs, want the 2 still unleased", len(rest.Jobs))
	}
}

// A job type nobody ever deployed is a typo far more often than it is a worker
// that started early, and answering an empty list would leave it polling forever
// with nothing to debug.
func TestPullByTypeRejectsAnUnknownType(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	if code, _ := pull(t, srv, `{"type":"no-such-type","worker":"w1"}`); code != http.StatusNotFound {
		t.Errorf("pull of an unknown type: status=%d, want 404", code)
	}
}

// A type an in-process worker is already draining must not be leasable: the runner
// does not lease, it dispatches whatever is activatable, so handing the same job to
// an external worker is how it gets done twice (ADR-0157).
func TestPullByTypeRefusesTypesServedInProcess(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	for _, name := range []string{compiler.MailJobType, compiler.RestJobType, compiler.DMNJobType} {
		body := fmt.Sprintf(`{"type":%q,"worker":"w1"}`, name)
		if code, _ := pull(t, srv, body); code != http.StatusConflict {
			t.Errorf("pull of in-process type %q: status=%d, want 409", name, code)
		}
	}
}

// A user task is not worker work: it waits for a person and is claimed through the
// Tasks app, so the worker protocol must not hand it out.
func TestPullByTypeRefusesUserTasks(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	body := fmt.Sprintf(`{"type":%q,"worker":"w1"}`, compiler.UserTaskJobType)
	if code, _ := pull(t, srv, body); code != http.StatusConflict {
		t.Errorf("pull of the user-task type: status=%d, want 409", code)
	}
}

// The request has to name a type; an empty one would otherwise resolve to nothing
// and look like an empty queue.
func TestPullByTypeRequiresAType(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	if code, _ := pull(t, srv, `{"worker":"w1"}`); code != http.StatusBadRequest {
		t.Errorf("pull with no type: status=%d, want 400", code)
	}
}

// A lease longer than the maximum is refused for the same reason the by-key
// activation refuses it: the lease is what returns a job from a dead worker.
func TestPullByTypeRefusesAnOverlongLease(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	body := fmt.Sprintf(`{"type":"send-email","worker":"w1","leaseMs":%d}`, (maxJobLease * 2).Milliseconds())
	if code, _ := pull(t, srv, body); code != http.StatusBadRequest {
		t.Errorf("pull with an overlong lease: status=%d, want 400", code)
	}
}

// TestPulledJobIsCompletedByItsWorkerWithoutAReason is the other half of the
// protocol, and it is where completing a job by hand and completing one as its
// worker part company. Completing by key is an *operator intervention* and must
// say why (ADR-0159) — but a worker reporting the outcome of work it holds a
// lease on is the ordinary path ADR-0007 describes, not an override of the model,
// and demanding a reason from it would both block the protocol and file every
// worker completion in the operator audit trail.
func TestPulledJobIsCompletedByItsWorkerWithoutAReason(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	_, got := pull(t, srv, `{"type":"send-email","worker":"w1"}`)
	if len(got.Jobs) != 1 {
		t.Fatalf("pulled %d jobs, want 1", len(got.Jobs))
	}
	job := got.Jobs[0]

	path := fmt.Sprintf("/api/v1/jobs/%d/complete", job.JobKey)
	if code, body := serveInternal(t, srv, http.MethodPost, path,
		`{"worker":"w1","variables":{"sent":true}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("worker completion: status=%d body=%s", code, body)
	}

	// Nothing is parked any more…
	if code, after := pull(t, srv, `{"type":"send-email","worker":"w2"}`); code != http.StatusOK || len(after.Jobs) != 0 {
		t.Errorf("after completing: status=%d jobs=%d, want 200 and 0", code, len(after.Jobs))
	}
	// …and the completion was not filed as an operator intervention.
	actions := 0
	srv.do(func() {
		_ = srv.store.OperatorActionHistory(job.ProcessInstanceKey, func(int64, uint64, *model.OperatorActionValue) error {
			actions++
			return nil
		})
	})
	if actions != 0 {
		t.Errorf("%d operator action(s) recorded for a worker completion, want 0", actions)
	}
}

// A worker id that does not hold the lease is not the lease holder, whatever it
// claims: say so plainly rather than silently treating it as an operator.
func TestCompletingAsTheWrongWorkerIsRefused(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	_, got := pull(t, srv, `{"type":"send-email","worker":"w1"}`)
	if len(got.Jobs) != 1 {
		t.Fatalf("pulled %d jobs, want 1", len(got.Jobs))
	}
	path := fmt.Sprintf("/api/v1/jobs/%d/complete", got.Jobs[0].JobKey)
	if code, _ := serveInternal(t, srv, http.MethodPost, path, `{"worker":"w2"}`, "application/json"); code != http.StatusConflict {
		t.Errorf("completion by a non-holder: status=%d, want 409", code)
	}
}

// An operator completing an unleased job still has to say why: the worker path
// must not become a way around the audit trail by naming any worker at all.
func TestCompletingAnUnleasedJobAsAWorkerIsRefused(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	jobKey, _ := parkedServiceJob(t, srv, 1)
	path := fmt.Sprintf("/api/v1/jobs/%d/complete", jobKey)
	if code, _ := serveInternal(t, srv, http.MethodPost, path, `{"worker":"w1"}`, "application/json"); code != http.StatusConflict {
		t.Errorf("completion of an unleased job as a worker: status=%d, want 409", code)
	}
}

// A malformed body is the client's error, not the engine's.
func TestPullByTypeRejectsAMalformedBody(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	if code, _ := pull(t, srv, `{"type":`); code != http.StatusBadRequest {
		t.Errorf("pull with a truncated body: status=%d, want 400", code)
	}
}

// A pull that names a type nobody has parked work for is an ordinary empty answer,
// not an error: the queue being idle is the normal steady state a worker polls
// through.
func TestPullByTypeAnswersAnIdleQueueWithAnEmptyList(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	if code, first := pull(t, srv, `{"type":"send-email","worker":"w1"}`); code != http.StatusOK || len(first.Jobs) != 1 {
		t.Fatalf("first pull: status=%d jobs=%d", code, len(first.Jobs))
	}
	code, idle := pull(t, srv, `{"type":"send-email","worker":"w1"}`)
	if code != http.StatusOK {
		t.Fatalf("idle pull: status=%d, want 200", code)
	}
	if idle.Jobs == nil {
		t.Error("idle pull returned a null jobs list, want an empty array a worker can range over")
	}
}
