package api

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// The external half of the dispatch gate (ADR-0340): a worker pulling by job type over
// HTTP is gated exactly as the in-process runner is, and the long poll behind it does
// not turn a held queue into a spin.

// tripPullBreaker starts n instances of the fixture's type and fails the first three
// from distinct instances, as a worker would — which is what a target that is down
// looks like from here. It returns the interned job type.
func tripPullBreaker(t *testing.T, srv *Server, jobType string, n int) int32 {
	t.Helper()
	idx := waitTypeIndex(t, srv, jobType)
	for i := 1; i < n; i++ { // the fixture already started one
		if code, body := serveInternal(t, srv, http.MethodPost,
			"/api/v1/processes/1/instances", `{"variables":{}}`, "application/json"); code != http.StatusOK {
			t.Fatalf("start instance %d: status=%d body=%s", i, code, body)
		}
	}
	code, got := pull(t, srv, fmt.Sprintf(`{"type":%q,"worker":"w1","maxJobs":%d}`, jobType, breakerThreshold))
	if code != http.StatusOK || len(got.Jobs) != breakerThreshold {
		t.Fatalf("lease for the trip: status=%d jobs=%d, want %d", code, len(got.Jobs), breakerThreshold)
	}
	for _, j := range got.Jobs {
		body := fmt.Sprintf(`{"worker":"w1","leaseToken":%d,"retries":1,"message":"dial tcp: connection refused"}`, j.LeaseToken)
		if code, raw := serveInternal(t, srv, http.MethodPost,
			fmt.Sprintf("/api/v1/jobs/%d/fail", j.JobKey), body, "application/json"); code != http.StatusOK {
			t.Fatalf("fail job %d: status=%d body=%s", j.JobKey, code, raw)
		}
	}
	if !srv.breakers.holdingFor(idx) {
		t.Fatalf("three failures from three instances did not trip the breaker")
	}
	return idx
}

// TestAWorkerPullingByTypeIsGatedToo. The two dispatch paths are different code and
// the same decision: a worker asking for work of a held type is handed none, and the
// jobs it did not get are still activatable, unleased and unspent.
func TestAWorkerPullingByTypeIsGatedToo(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	idx := tripPullBreaker(t, srv, "send-email", 8)

	code, got := pull(t, srv, `{"type":"send-email","worker":"w2","maxJobs":10}`)
	if code != http.StatusOK {
		t.Fatalf("pull: status=%d", code)
	}
	if len(got.Jobs) != 0 {
		t.Errorf("a worker was handed %d jobs of a held type, want none", len(got.Jobs))
	}
	var waiting int
	srv.do(func() {
		if err := srv.store.ActivatableJobs(idx, func(uint64) error { waiting++; return nil }); err != nil {
			t.Errorf("ActivatableJobs: %v", err)
		}
	})
	if waiting == 0 {
		t.Error("nothing is waiting, so nothing was held — the gate did not run")
	}
}

// TestAHeldTypeDoesNotSpinTheLongPoll. A worker long-polls a type whose breaker is
// open. Every job created under that type while it waits is, almost by definition,
// another one for the target that is down — so registering for those wake-ups would
// have this request scan the index and answer empty at the rate the flood is being
// created. It waits its own wait out instead, and registers no waiter at all.
func TestAHeldTypeDoesNotSpinTheLongPoll(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	idx := tripPullBreaker(t, srv, "send-email", 8)

	done := make(chan int, 1)
	go func() {
		_, got := pull(t, srv, `{"type":"send-email","worker":"w2","waitMs":300}`)
		done <- len(got.Jobs)
	}()

	// While it is parked, nothing may be subscribed to this type's notifications, and
	// creating more work of the type must not wake it.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if srv.jobWaiters.count(idx) != 0 {
			t.Fatal("a held type registered a long-poll waiter; every new job of it would wake the request")
		}
		if code, body := serveInternal(t, srv, http.MethodPost,
			"/api/v1/processes/1/instances", `{"variables":{}}`, "application/json"); code != http.StatusOK {
			t.Fatalf("start instance: status=%d body=%s", code, body)
		}
		select {
		case n := <-done:
			if n != 0 {
				t.Errorf("the poll came back with %d jobs of a held type, want none", n)
			}
			return
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("the long poll never returned")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestAnUnheldTypeStillWakesOnItsJob keeps the damping above from becoming a
// regression: the long poll is the reason a worker need not busy-poll, and it has to
// go on working on every type that is not held — which is every type, almost always.
func TestAnUnheldTypeStillWakesOnItsJob(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	idx := waitTypeIndex(t, srv, "send-email")
	if _, drained := pull(t, srv, `{"type":"send-email","worker":"drain"}`); len(drained.Jobs) != 1 {
		t.Fatal("could not drain the initial job")
	}

	done := make(chan pullResp, 1)
	go func() {
		_, got := pull(t, srv, `{"type":"send-email","worker":"w1","waitMs":5000}`)
		done <- got
	}()
	deadline := time.Now().Add(3 * time.Second)
	for srv.jobWaiters.count(idx) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("an unheld type registered no waiter — the long poll stopped working")
		}
		time.Sleep(2 * time.Millisecond)
	}
	if code, body := serveInternal(t, srv, http.MethodPost,
		"/api/v1/processes/1/instances", `{"variables":{}}`, "application/json"); code != http.StatusOK {
		t.Fatalf("start instance: status=%d body=%s", code, body)
	}
	select {
	case got := <-done:
		if len(got.Jobs) != 1 {
			t.Errorf("the woken poll returned %d jobs, want the one that arrived", len(got.Jobs))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the poll did not wake on a job of its type")
	}
}

// TestAWorkersCompletionClosesTheBreakerItTripped is the external half of recovery:
// the same HTTP endpoints that reported the failures report the success, and the
// breaker they tripped lets the backlog go.
func TestAWorkersCompletionClosesTheBreakerItTripped(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	idx := tripPullBreaker(t, srv, "send-email", 8)

	// The probe: one job, one cooldown later, exactly as ADR-0340 allows.
	srv.breakers.now = func() int64 { return time.Now().UnixNano() + int64(breakerCooldown) }
	code, probe := pull(t, srv, `{"type":"send-email","worker":"w2","maxJobs":10}`)
	if code != http.StatusOK || len(probe.Jobs) != 1 {
		t.Fatalf("probe: status=%d jobs=%d, want exactly one", code, len(probe.Jobs))
	}

	body := fmt.Sprintf(`{"worker":"w2","leaseToken":%d,"variables":{}}`, probe.Jobs[0].LeaseToken)
	if code, raw := serveInternal(t, srv, http.MethodPost,
		fmt.Sprintf("/api/v1/jobs/%d/complete", probe.Jobs[0].JobKey), body, "application/json"); code != http.StatusOK {
		t.Fatalf("complete the probe: status=%d body=%s", code, raw)
	}
	if srv.breakers.holdingFor(idx) {
		t.Fatal("the probe succeeded and the breaker is still holding work back")
	}
	// And the backlog goes out on the next pull, all of it.
	code, rest := pull(t, srv, `{"type":"send-email","worker":"w2","maxJobs":10}`)
	if code != http.StatusOK || len(rest.Jobs) == 0 {
		t.Errorf("after recovery: status=%d jobs=%d, want the held backlog handed out", code, len(rest.Jobs))
	}
}
