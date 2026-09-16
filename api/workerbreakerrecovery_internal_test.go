package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// The half of ADR-0340 an HTTP-level test cannot reach: recovery. A breaker admits its
// probe one cooldown after it trips, and a cooldown is real seconds — so the only honest
// way to drive the transition is to own the clock, which means an internal test.
//
// It is worth driving end to end rather than only on the state machine, because what the
// decision promises is not "the breaker closes". It is that a held backlog *drains by
// itself*, with no operator resolving anything, and that promise runs through the
// dispatch path, the runner and the handlers — none of which the unit tests touch.

// floodedServer deploys the mail model, starts n instances against a Worker nobody has
// configured, and returns the server with its breaker's clock under the caller's control.
func floodedServer(t *testing.T, n int) (*Server, func(d time.Duration)) {
	t.Helper()
	srv, cleanup := newOffLoopServer(t)
	t.Cleanup(cleanup)

	now := time.Now().UnixNano()
	srv.breakers.now = func() int64 { return now }
	advance := func(d time.Duration) { now += int64(d) }

	const mailBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:atlas="http://atlas/schema/1.0">
  <process id="notify" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="send">
      <extensionElements><atlas:mailConnector connector="Patrick Blumer" to="a@b.ch" subject="hi" body="hi"/></extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="send"/>
    <sequenceFlow id="f2" sourceRef="send" targetRef="end"/>
  </process>
</definitions>`
	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", mailBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	for i := 0; i < n; i++ {
		path := fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key)
		if code, body := serveInternal(t, srv, http.MethodPost, path, "{}", "application/json"); code != http.StatusOK {
			t.Fatalf("create instance %d: status=%d body=%s", i, code, body)
		}
	}
	return srv, advance
}

// incidentCount reads how many tokens are parked, through the surface an operator reads.
func incidentCount(t *testing.T, srv *Server) int {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/incidents/summary", "", "")
	if code != http.StatusOK {
		t.Fatalf("summary: status=%d body=%s", code, raw)
	}
	var s incidentSummaryResp
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("decode summary: %v (%s)", err, raw)
	}
	return s.Total
}

// activatable counts the jobs of a type still waiting to be handed out.
func activatable(t *testing.T, srv *Server, jobType int32) int {
	t.Helper()
	n := 0
	srv.do(func() {
		if err := srv.store.ActivatableJobs(jobType, func(uint64) error { n++; return nil }); err != nil {
			t.Errorf("ActivatableJobs: %v", err)
		}
	})
	return n
}

// mailJobType is the reserved index the mail task above compiles to.
func mailJobType(t *testing.T, srv *Server) int32 {
	t.Helper()
	for _, e := range srv.jobTypes.All() {
		if e.Name == "io.atlas.mail.send" {
			return e.Index
		}
	}
	t.Fatal("the mail job type was never interned; the model did not deploy as expected")
	return 0
}

// TestAHeldBacklogDrainsItselfWhenTheTargetReturns is the decision's promise end to end.
// Nothing in the second half of this test resolves an incident or touches a token: the
// operator's only action is to fix the Worker, and everything after that is the engine
// noticing.
func TestAHeldBacklogDrainsItselfWhenTheTargetReturns(t *testing.T) {
	srv, advance := floodedServer(t, 20)
	jobType := mailJobType(t, srv)

	parked := incidentCount(t, srv)
	held := activatable(t, srv, jobType)
	if parked == 0 || parked >= 20 {
		t.Fatalf("setup: %d incidents from 20 instances, want a breaker that tripped early", parked)
	}
	if held == 0 {
		t.Fatal("setup: nothing is being held, so there is no backlog to drain")
	}

	// The fix: a Worker under the name the model states, delivering to the preview
	// outbox rather than an SMTP host.
	conn := `{"name":"Patrick Blumer","kind":"mail","provider":"preview","endpoint":"mx.example.ch:587","sender":"a@b.ch"}`
	if code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/connectors", conn, "application/json"); code != http.StatusOK {
		t.Fatalf("configure the worker: status=%d body=%s", code, body)
	}

	// Before the cooldown elapses nothing may go out — the fix is invisible to the
	// engine until it probes, and probing early is what hammers a recovering host.
	if err := srv.drive(); err != nil {
		t.Fatalf("drive: %v", err)
	}
	if now := activatable(t, srv, jobType); now != held {
		t.Errorf("%d jobs went out before the cooldown elapsed (was %d)", held-now, held)
	}

	// One cooldown later the probe is admitted, succeeds, and the breaker closes; from
	// there the ordinary rounds drain the rest.
	advance(breakerCooldown)
	for i := 0; i < 10 && activatable(t, srv, jobType) > 0; i++ {
		if err := srv.drive(); err != nil {
			t.Fatalf("drive: %v", err)
		}
	}
	if left := activatable(t, srv, jobType); left != 0 {
		t.Errorf("%d jobs still held after the target returned, want the backlog drained", left)
	}
	if after := incidentCount(t, srv); after != parked {
		t.Errorf("incidents went from %d to %d while the work was waiting, want no token to have paid for the outage",
			parked, after)
	}
}

// TestNoProbeGoesOutBeforeItsCooldown is the other side of the same mechanism, and the
// one that protects a target that is still down: the probe is not "try again soon", it
// is exactly one call per cooldown, and the cooldown grows while the target keeps
// refusing.
func TestNoProbeGoesOutBeforeItsCooldown(t *testing.T) {
	srv, advance := floodedServer(t, 20)
	jobType := mailJobType(t, srv)

	held := activatable(t, srv, jobType)
	if held == 0 {
		t.Fatal("setup: nothing held")
	}
	// The target is still broken, so each probe fails and the next wait doubles. What
	// must not happen is a second job going out inside one cooldown.
	for cycle, wait := 0, breakerCooldown; cycle < 3; cycle, wait = cycle+1, wait*2 {
		before := activatable(t, srv, jobType)
		advance(wait - time.Second)
		if err := srv.drive(); err != nil {
			t.Fatalf("cycle %d: drive: %v", cycle, err)
		}
		if now := activatable(t, srv, jobType); now != before {
			t.Errorf("cycle %d: %d jobs went out one second before the %s cooldown", cycle, before-now, wait)
		}
		advance(time.Second)
		if err := srv.drive(); err != nil {
			t.Fatalf("cycle %d: drive: %v", cycle, err)
		}
	}
	// Three probes over three cooldowns, against a target that answered none of them:
	// the flood is still held, and the engine spent three calls rather than twenty.
	if left := activatable(t, srv, jobType); left == 0 {
		t.Error("the whole backlog went out against a target that is still down")
	}
}
