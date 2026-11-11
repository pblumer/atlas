package api

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pblumer/atlas/compiler"
)

// The REST Worker Type under the breaker (ADR-0340, amended 2026-11-11).
//
// Found live: a model's REST task POSTed to Atlas's own API with an unencoded '#' in the
// path, hit the wrong route and drew HTTP 400 for every instance. Three distinct
// instances failing in a row is the trip condition, so the breaker judged the target
// down — and the target was the whole REST job type, so the engine held back every REST
// task on the server, the shop's fulfilment among them, until a probe got through.
//
// Two things were wrong, and these tests pin both. A 400 is the target answering that
// the request was wrong, not the target being down. And a REST task names no Worker,
// so its target is the endpoint it calls — one host's trouble is not another's.

// restBreakerBPMN is one REST task whose URL is built from a variable, the way the
// fulfilment model builds its own: the endpoint is only known per instance.
const restBreakerBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
  <process id="call" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="post">
      <extensionElements><atlas:restConnector method="post" url="=base + &#34;/api/v1/orders/1/lines/a#1/start&#34;"/></extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="post"/>
    <sequenceFlow id="f2" sourceRef="post" targetRef="end"/>
  </process>
</definitions>`

// restBreakerServer deploys the REST model on a server whose breaker clock the test
// owns, with the timer scheduler held still for the reason floodedServer gives.
func restBreakerServer(t *testing.T) (*Server, uint64, func(time.Duration)) {
	t.Helper()
	srv, cleanup := newOffLoopServer(t, withTimerTrigger(make(chan time.Time)))
	t.Cleanup(cleanup)
	var now atomic.Int64
	now.Store(time.Now().UnixNano())
	srv.breakers.now = now.Load
	if !srv.jobRunner.Handles(compiler.RestJobTypeIndex) {
		t.Fatal("setup: this server does not run REST tasks in process")
	}
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", restBreakerBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	return srv, deploy.Key, func(d time.Duration) { now.Add(int64(d)) }
}

// startCalls starts n instances calling base and drives them as far as they go.
func startCalls(t *testing.T, srv *Server, defKey uint64, base string, n int) []uint64 {
	t.Helper()
	var keys []uint64
	for i := 0; i < n; i++ {
		code, body := serveInternal(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", defKey),
			fmt.Sprintf(`{"variables":{"base":%q}}`, base), "application/json")
		if code != http.StatusOK {
			t.Fatalf("start instance %d: status=%d body=%s", i, code, body)
		}
		var inst struct {
			InstanceKey uint64 `json:"instanceKey"`
		}
		if err := json.Unmarshal(body, &inst); err != nil || inst.InstanceKey == 0 {
			t.Fatalf("decode instance: %v (%s)", err, body)
		}
		keys = append(keys, inst.InstanceKey)
	}
	if err := srv.drive(); err != nil {
		t.Fatalf("drive: %v", err)
	}
	return keys
}

// answering is a target that answers every call with status, counting the calls.
func answering(t *testing.T, status int) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(status)
		if status >= 400 {
			_, _ = w.Write([]byte(`{"error":"order: a line cannot be set to here"}`))
		}
	}))
	t.Cleanup(ts.Close)
	return ts, &calls
}

func instanceState(t *testing.T, srv *Server, key uint64) string {
	t.Helper()
	code, body := serveInternal(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/instances/%d/timeline", key), "", "")
	if code != http.StatusOK {
		t.Fatalf("get instance %d: status=%d body=%s", key, code, body)
	}
	var inst struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(body, &inst); err != nil {
		t.Fatalf("decode instance: %v", err)
	}
	return inst.State
}

// TestRepeatedBadRequestsDoNotOpenTheBreaker is the incident. Five instances send a
// request the target refuses as wrong; every one of them still fails its whole retry
// budget and parks on an incident, as before — but nothing is held back, because a
// target that answers 400 is answering.
func TestRepeatedBadRequestsDoNotOpenTheBreaker(t *testing.T) {
	srv, defKey, _ := restBreakerServer(t)
	target, _ := answering(t, http.StatusBadRequest)

	startCalls(t, srv, defKey, target.URL, 5)

	if srv.breakers.holdingFor(compiler.RestJobTypeIndex) {
		t.Fatalf("five 400s opened the breaker: %+v", srv.breakers.openBreakers(srv.jobTypes.Name))
	}
	if got := incidentCount(t, srv); got != 5 {
		t.Errorf("incidents = %d, want all 5: a rejected request must still fail its job", got)
	}
}

// TestAnUnavailableTargetStillOpensTheBreaker is the other half: what the breaker is for
// keeps working. A 503 and a host that refuses the connection are a target in trouble.
func TestAnUnavailableTargetStillOpensTheBreaker(t *testing.T) {
	refused := func(t *testing.T) string {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		addr := l.Addr().String()
		_ = l.Close() // nothing listens there now
		return "http://" + addr
	}
	for name, base := range map[string]func(t *testing.T) string{
		"503":                func(t *testing.T) string { ts, _ := answering(t, http.StatusServiceUnavailable); return ts.URL },
		"429":                func(t *testing.T) string { ts, _ := answering(t, http.StatusTooManyRequests); return ts.URL },
		"connection refused": refused,
	} {
		t.Run(name, func(t *testing.T) {
			srv, defKey, _ := restBreakerServer(t)
			startCalls(t, srv, defKey, base(t), 10)
			if !srv.breakers.holdingFor(compiler.RestJobTypeIndex) {
				t.Fatal("ten instances against an unavailable target did not open the breaker")
			}
			if got := incidentCount(t, srv); got >= 10 {
				t.Errorf("incidents = %d of 10: the flood was not stopped at its source", got)
			}
		})
	}
}

// TestOneHostsOutageDoesNotHoldBackAnother. A REST task names no Worker, so before the
// amendment its target was the REST job type itself — and one dead host held back every
// REST task on the server. Its target is the endpoint's origin now.
func TestOneHostsOutageDoesNotHoldBackAnother(t *testing.T) {
	srv, defKey, _ := restBreakerServer(t)
	down, downCalls := answering(t, http.StatusServiceUnavailable)
	up, upCalls := answering(t, http.StatusOK)

	startCalls(t, srv, defKey, down.URL, 10)
	held := srv.breakers.openBreakers(srv.jobTypes.Name)
	if len(held) != 1 {
		t.Fatalf("held = %+v, want the one host that is down", held)
	}
	if want := strings.ToLower(down.URL); held[0].Endpoint != want || held[0].Connector != "" {
		t.Errorf("held row names endpoint %q and connector %q, want endpoint %q and no Worker",
			held[0].Endpoint, held[0].Connector, want)
	}
	callsWhileHeld := downCalls.Load()

	healthy := startCalls(t, srv, defKey, up.URL, 3)
	for _, k := range healthy {
		if got := instanceState(t, srv, k); got != "completed" {
			t.Errorf("instance %d calling a healthy host is %q, want completed: one host's outage held back another's", k, got)
		}
	}
	if got := upCalls.Load(); got != 3 {
		t.Errorf("healthy host saw %d calls, want 3", got)
	}
	if got := downCalls.Load(); got != callsWhileHeld {
		t.Errorf("the host that is down saw %d more calls while held", got-callsWhileHeld)
	}
	if !srv.breakers.holdingFor(compiler.RestJobTypeIndex) {
		t.Error("the healthy host's completions closed the breaker of the host that is down")
	}
}

// TestARejectedProbeClosesTheBreaker. A probe that draws a 400 has learned that the
// target answers, which is the probe's question. Reading it as no verdict would leave
// the target half open until the probe is written off, minutes later.
func TestARejectedProbeClosesTheBreaker(t *testing.T) {
	srv, defKey, advance := restBreakerServer(t)
	var status atomic.Int64
	status.Store(http.StatusServiceUnavailable)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(int(status.Load()))
	}))
	t.Cleanup(ts.Close)

	startCalls(t, srv, defKey, ts.URL, 10)
	if !srv.breakers.holdingFor(compiler.RestJobTypeIndex) {
		t.Fatal("setup: the breaker did not open")
	}
	status.Store(http.StatusBadRequest)
	advance(breakerCooldown)
	if err := srv.drive(); err != nil {
		t.Fatalf("drive: %v", err)
	}
	if srv.breakers.holdingFor(compiler.RestJobTypeIndex) {
		t.Errorf("a probe the target answered with 400 left the breaker %+v", srv.breakers.openBreakers(srv.jobTypes.Name))
	}
}

// TestAWorkersRejectionDoesNotOpenTheBreaker is the same rule on the external path: a
// worker that says its failure was a rejected request ("rejected": true) is not reporting
// an outage. A worker that does not say so — any worker older than the amendment — is
// counted exactly as before, which tripPullBreaker already proves.
func TestAWorkersRejectionDoesNotOpenTheBreaker(t *testing.T) {
	srv := jobPullSrv(t, "send-email", `{}`)
	idx := waitTypeIndex(t, srv, "send-email")
	for i := 1; i < 5; i++ {
		if code, body := serveInternal(t, srv, http.MethodPost,
			"/api/v1/processes/1/instances", `{"variables":{}}`, "application/json"); code != http.StatusOK {
			t.Fatalf("start instance %d: status=%d body=%s", i, code, body)
		}
	}
	code, got := pull(t, srv, `{"type":"send-email","worker":"w1","maxJobs":5}`)
	if code != http.StatusOK || len(got.Jobs) != 5 {
		t.Fatalf("lease: status=%d jobs=%d, want 5", code, len(got.Jobs))
	}
	for _, j := range got.Jobs {
		body := fmt.Sprintf(`{"worker":"w1","leaseToken":%d,"retries":1,"message":"HTTP 400","rejected":true}`, j.LeaseToken)
		if code, raw := serveInternal(t, srv, http.MethodPost,
			fmt.Sprintf("/api/v1/jobs/%d/fail", j.JobKey), body, "application/json"); code != http.StatusOK {
			t.Fatalf("fail job %d: status=%d body=%s", j.JobKey, code, raw)
		}
	}
	if srv.breakers.holdingFor(idx) {
		t.Error("five rejected requests from five instances opened the breaker")
	}
	// Still failed: the attempt was spent and the job is offered again.
	if code, again := pull(t, srv, `{"type":"send-email","worker":"w1","maxJobs":5}`); code != http.StatusOK || len(again.Jobs) != 5 {
		t.Errorf("re-lease after rejection: status=%d jobs=%d, want the 5 failed jobs back", code, len(again.Jobs))
	}
}
