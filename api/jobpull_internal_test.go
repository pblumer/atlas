package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/model"
)

// pullResp is the shape POST /api/v1/jobs/activate answers with.
type pullResp struct {
	Jobs []struct {
		JobKey             uint64 `json:"jobKey"`
		Type               string `json:"type"`
		ProcessInstanceKey uint64 `json:"processInstanceKey"`
		ElementInstanceKey uint64 `json:"elementInstanceKey"`
		ProcessDefKey      uint64 `json:"processDefKey"`
		ElementID          string `json:"elementId"`
		Retries            int32  `json:"retries"`
		LeaseToken         uint64 `json:"leaseToken"`
		Connector          *struct {
			Kind   string         `json:"kind"`
			Fields map[string]any `json:"fields"`
		} `json:"connector"`
		LeaseExpiresAt int64          `json:"leaseExpiresAt"`
		Variables      map[string]any `json:"variables"`
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

// testClock is a settable clock, so a test can elapse a worker's lease deliberately
// instead of depending on wall time.
type testClock struct{ t int64 }

func (c *testClock) Now() int64 { return c.t }

// jobPullSrv deploys one service-task process with the given model-authored job
// type, starts an instance carrying variables, and returns the ready server.
func jobPullSrv(t *testing.T, jobType string, vars string) *Server {
	t.Helper()
	srv, _ := jobPullSrvClocked(t, jobType, vars)
	return srv
}

// jobPullSrvClocked is jobPullSrv with the clock handed back, for the tests that
// need a lease to elapse.
func jobPullSrvClocked(t *testing.T, jobType string, vars string) (*Server, *testClock) {
	t.Helper()
	clk := &testClock{t: time.Now().UnixNano()}
	srv := newServerWithClock(t, clk)
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
	return srv, clk
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
	body := fmt.Sprintf(`{"worker":"w1","leaseToken":%d,"variables":{"sent":true}}`, job.LeaseToken)
	if code, b := serveInternal(t, srv, http.MethodPost, path, body, "application/json"); code != http.StatusOK {
		t.Fatalf("worker completion: status=%d body=%s", code, b)
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
// claims — even holding a valid token for it. Both halves have to match, or a
// worker that saw another's token could report on its work.
func TestCompletingAsTheWrongWorkerIsRefused(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	_, got := pull(t, srv, `{"type":"send-email","worker":"w1"}`)
	if len(got.Jobs) != 1 {
		t.Fatalf("pulled %d jobs, want 1", len(got.Jobs))
	}
	path := fmt.Sprintf("/api/v1/jobs/%d/complete", got.Jobs[0].JobKey)
	body := fmt.Sprintf(`{"worker":"w2","leaseToken":%d}`, got.Jobs[0].LeaseToken)
	if code, _ := serveInternal(t, srv, http.MethodPost, path, body, "application/json"); code != http.StatusConflict {
		t.Errorf("completion by a non-holder: status=%d, want 409", code)
	}
}

// An operator completing an unleased job still has to say why: the worker path
// must not become a way around the audit trail by naming any worker at all.
func TestCompletingAnUnleasedJobAsAWorkerIsRefused(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	jobKey, _ := parkedServiceJob(t, srv, 1)
	path := fmt.Sprintf("/api/v1/jobs/%d/complete", jobKey)
	if code, _ := serveInternal(t, srv, http.MethodPost, path, `{"worker":"w1","leaseToken":1}`, "application/json"); code != http.StatusConflict {
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

// TestAStaleWorkerCannotCompleteAJobItNoLongerHolds is the fencing case, and the
// one Assignee alone cannot catch: two instances of one worker deployment share a
// name, so when the first one's lease elapses and the second takes the job, a late
// report from the first presents the right *name* for a lease it no longer holds.
// The token it presents is a lease the job has moved past, so the report is refused
// and the work is not completed twice.
func TestAStaleWorkerCannotCompleteAJobItNoLongerHolds(t *testing.T) {
	srv, clk := jobPullSrvClocked(t, "send-email", `{}`)

	// Both instances of the deployment call themselves "mailer".
	_, first := pull(t, srv, `{"type":"send-email","worker":"mailer","leaseMs":60000}`)
	if len(first.Jobs) != 1 {
		t.Fatalf("first pull returned %d jobs, want 1", len(first.Jobs))
	}
	stale := first.Jobs[0]
	if stale.LeaseToken == 0 {
		t.Fatal("the pull returned no lease token")
	}
	expireLease(t, srv, clk)

	_, second := pull(t, srv, `{"type":"send-email","worker":"mailer","leaseMs":60000}`)
	if len(second.Jobs) != 1 {
		t.Fatalf("after the lease elapsed the job was not offered again (%d jobs)", len(second.Jobs))
	}
	if second.Jobs[0].LeaseToken == stale.LeaseToken {
		t.Fatalf("the second lease reused token %d — it cannot fence the first", stale.LeaseToken)
	}

	// The first instance reports late, with the name it still has and the token it
	// was given.
	path := fmt.Sprintf("/api/v1/jobs/%d/complete", stale.JobKey)
	body := fmt.Sprintf(`{"worker":"mailer","leaseToken":%d}`, stale.LeaseToken)
	if code, _ := serveInternal(t, srv, http.MethodPost, path, body, "application/json"); code != http.StatusConflict {
		t.Errorf("stale completion: status=%d, want 409", code)
	}
	// The holder's own report still works.
	body = fmt.Sprintf(`{"worker":"mailer","leaseToken":%d}`, second.Jobs[0].LeaseToken)
	if code, b := serveInternal(t, srv, http.MethodPost, path, body, "application/json"); code != http.StatusOK {
		t.Errorf("holder completion: status=%d body=%s", code, b)
	}
}

// The same fence guards a failure report: a stale worker must not be able to burn a
// retry (or raise an incident) on work another worker is doing.
func TestAStaleWorkerCannotFailAJobItNoLongerHolds(t *testing.T) {
	srv, clk := jobPullSrvClocked(t, "send-email", `{}`)
	_, first := pull(t, srv, `{"type":"send-email","worker":"mailer","leaseMs":60000}`)
	stale := first.Jobs[0]
	expireLease(t, srv, clk)
	if _, second := pull(t, srv, `{"type":"send-email","worker":"mailer","leaseMs":60000}`); len(second.Jobs) != 1 {
		t.Fatalf("job not re-offered")
	}

	path := fmt.Sprintf("/api/v1/jobs/%d/fail", stale.JobKey)
	body := fmt.Sprintf(`{"retries":0,"message":"late","worker":"mailer","leaseToken":%d}`, stale.LeaseToken)
	if code, _ := serveInternal(t, srv, http.MethodPost, path, body, "application/json"); code != http.StatusConflict {
		t.Errorf("stale failure: status=%d, want 409", code)
	}
}

// Naming a worker without its token is not a completion the protocol can trust, so
// it is refused rather than accepted on the strength of the name alone.
func TestWorkerCompletionRequiresItsLeaseToken(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	_, got := pull(t, srv, `{"type":"send-email","worker":"mailer"}`)
	if len(got.Jobs) != 1 {
		t.Fatalf("pulled %d jobs, want 1", len(got.Jobs))
	}
	path := fmt.Sprintf("/api/v1/jobs/%d/complete", got.Jobs[0].JobKey)
	if code, _ := serveInternal(t, srv, http.MethodPost, path, `{"worker":"mailer"}`, "application/json"); code != http.StatusBadRequest {
		t.Errorf("completion with no token: status=%d, want 400", code)
	}
}

// expireLease pushes the clock past every outstanding lease and ticks the timers,
// which is the engine's own path for "the worker never reported" — no test-only
// engine entry point, and no dependence on wall time.
func expireLease(t *testing.T, srv *Server, clk *testClock) {
	t.Helper()
	clk.t += int64(2 * maxJobLease)
	srv.do(func() {
		if err := srv.proc.TickTimers(); err != nil {
			t.Fatalf("TickTimers: %v", err)
		}
		if err := srv.jobRunner.Drive(); err != nil {
			t.Fatalf("Drive: %v", err)
		}
	})
}

// TestLongPollReturnsWhenAJobArrives is the point of long-polling: the request is
// made against an empty queue and answers when work appears, instead of returning
// nothing and being asked again.
func TestLongPollReturnsWhenAJobArrives(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	// Drain the one job the fixture parked, so the queue starts empty.
	if _, drained := pull(t, srv, `{"type":"send-email","worker":"drain"}`); len(drained.Jobs) != 1 {
		t.Fatalf("could not drain the initial job")
	}

	done := make(chan pullResp, 1)
	go func() {
		_, got := pull(t, srv, `{"type":"send-email","worker":"w1","waitMs":5000}`)
		done <- got
	}()
	// Wait until the poll is actually parked, then create the work it is waiting for.
	deadline := time.Now().Add(3 * time.Second)
	for srv.jobWaiters.count(waitTypeIndex(t, srv, "send-email")) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the long poll never registered a waiter")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/processes/1/instances", "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("start instance: status=%d body=%s", code, body)
	}

	select {
	case got := <-done:
		if len(got.Jobs) != 1 {
			t.Errorf("the long poll returned %d jobs, want the one that arrived", len(got.Jobs))
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the long poll did not return after a job of its type was created")
	}
}

// TestLongPollDoesNotHoldTheRunLoop is the constraint the whole sequence exists for.
// A request that waited inside Loop.Do would freeze the single writer for the length
// of the poll — every other request, every timer tick, the whole engine. So while a
// long poll is parked, ordinary work must still be served.
func TestLongPollDoesNotHoldTheRunLoop(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	if _, drained := pull(t, srv, `{"type":"send-email","worker":"drain"}`); len(drained.Jobs) != 1 {
		t.Fatalf("could not drain the initial job")
	}

	parked := make(chan struct{})
	go func() {
		defer close(parked)
		pull(t, srv, `{"type":"send-email","worker":"w1","waitMs":3000}`)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for srv.jobWaiters.count(waitTypeIndex(t, srv, "send-email")) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the long poll never registered a waiter")
		}
		time.Sleep(2 * time.Millisecond)
	}

	// The engine is still answering while that poll sleeps.
	served := make(chan int, 1)
	go func() {
		code, _ := serveInternal(t, srv, http.MethodGet, "/api/v1/stats", "", "")
		served <- code
	}()
	select {
	case code := <-served:
		if code != http.StatusOK {
			t.Errorf("a request served during a long poll got status %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the run loop was blocked while a long poll waited — the stall this feature must not reintroduce")
	}
	<-parked
}

// A poll that waits and finds nothing answers with an empty list, not an error: an
// idle queue is the steady state a worker polls through.
func TestLongPollTimesOutEmpty(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	if _, drained := pull(t, srv, `{"type":"send-email","worker":"drain"}`); len(drained.Jobs) != 1 {
		t.Fatalf("could not drain the initial job")
	}
	start := time.Now()
	code, got := pull(t, srv, `{"type":"send-email","worker":"w1","waitMs":120}`)
	if code != http.StatusOK {
		t.Fatalf("status=%d, want 200", code)
	}
	if len(got.Jobs) != 0 {
		t.Errorf("returned %d jobs from an empty queue", len(got.Jobs))
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Errorf("returned after %v, want it to have waited about 120ms", elapsed)
	}
}

// A poll that finds work immediately must not wait for it.
func TestLongPollReturnsImmediatelyWhenWorkIsWaiting(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	start := time.Now()
	code, got := pull(t, srv, `{"type":"send-email","worker":"w1","waitMs":5000}`)
	if code != http.StatusOK || len(got.Jobs) != 1 {
		t.Fatalf("status=%d jobs=%d, want 200 and 1", code, len(got.Jobs))
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("took %v to return work that was already parked", elapsed)
	}
}

// waitTypeIndex is the engine-wide index of a job type, which is what the waiter
// registry is keyed by.
func waitTypeIndex(t *testing.T, srv *Server, name string) int32 {
	t.Helper()
	idx, ok := srv.jobTypes.Index(name)
	if !ok {
		t.Fatalf("job type %q is not registered", name)
	}
	return idx
}

// TestALeasedConnectorJobCarriesItsResolvedDetail is the mechanism ADR-0168 rests
// on. A worker has neither the compiled process nor the instance's scope chain, so
// it cannot find a worker task's configuration or evaluate it. The engine does
// that — it is the only one who can — and only the *result* travels: plain values a
// worker can act on with no engine concepts in them.
func TestALeasedConnectorJobCarriesItsResolvedDetail(t *testing.T) {
	srv := newServerWithOptions(t, WithOffloadedConnectorKinds([]string{"csv"}))
	h := srv.Handler()
	defKey := deployBPMN(t, h, csvConnectorBPMN)
	startInstance(t, h, defKey, `{"upload":"name,amount\nAda,12\n"}`)

	code, got := pull(t, srv, fmt.Sprintf(`{"type":%q,"worker":"csv-1"}`, compiler.CsvImportJobType))
	if code != http.StatusOK {
		t.Fatalf("pull: status=%d", code)
	}
	if len(got.Jobs) != 1 {
		t.Fatalf("pulled %d jobs, want 1", len(got.Jobs))
	}
	conn := got.Jobs[0].Connector
	if conn == nil {
		t.Fatal("the leased worker job carried no resolved detail, so a worker could not act on it")
	}
	if conn.Kind != "csv" {
		t.Errorf("kind = %q, want %q", conn.Kind, "csv")
	}
	// The source text was read up the scope chain by the engine and travels as a value.
	if src, _ := conn.Fields["source"].(string); !strings.Contains(src, "Ada,12") {
		t.Errorf("fields = %v, want the source text the engine resolved", conn.Fields)
	}
	if conn.Fields["resultVariable"] != "orders" {
		t.Errorf("resultVariable = %v, want orders", conn.Fields["resultVariable"])
	}
}

// A plain job-worker task carries no worker detail: there is nothing to resolve,
// and an empty object would invite a worker to look for one.
func TestALeasedPlainJobCarriesNoConnectorDetail(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	_, got := pull(t, srv, `{"type":"send-email","worker":"w1"}`)
	if len(got.Jobs) != 1 {
		t.Fatalf("pulled %d jobs, want 1", len(got.Jobs))
	}
	if got.Jobs[0].Connector != nil {
		t.Errorf("a plain job-worker task carried a worker detail: %+v", got.Jobs[0].Connector)
	}
}

// csvConnectorBPMN is a process whose service task is a CSV-to-JSON worker.
const csvConnectorBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
                  xmlns:atlas="http://atlas.dev/schema/1.0" id="defs">
  <bpmn:process id="import" isExecutable="true">
    <bpmn:startEvent id="s"/>
    <bpmn:serviceTask id="t">
      <bpmn:extensionElements>
        <atlas:csvConnector source="upload" resultVariable="orders" hasHeader="true"/>
      </bpmn:extensionElements>
    </bpmn:serviceTask>
    <bpmn:endEvent id="e"/>
    <bpmn:sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <bpmn:sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </bpmn:process>
</bpmn:definitions>`
