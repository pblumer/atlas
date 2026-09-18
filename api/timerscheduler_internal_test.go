package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// The timer scheduler's ticker, as a seam (withTimerTrigger).
//
// The scheduler is the one goroutine on an otherwise idle server that acts by itself:
// every real second it fires due timers and then *drives jobs*. That is right in
// production and wrong inside a test that advances a clock of its own and counts what
// the engine handed out — the two are asking one question with two clocks, and the
// answer depends on whether a test cycle happened to straddle a second.
//
// So the ticker can be held, exactly as the retention sweep's and the checkpoint
// loop's can. This proves that it is actually held rather than merely slower, which
// is the part a test relying on it needs: the wait below is longer than the real
// ticker's own period, so a scheduler still running on wall time would have fired
// inside it.

// timerBPMN parks an instance on a one-second timer — the shortest the compiler takes
// — so that what the instance is waiting for is not the duration but somebody to tick.
const timerBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <process id="waits" isExecutable="true">
    <startEvent id="start"/>
    <intermediateCatchEvent id="pause">
      <timerEventDefinition>
        <timeDuration xsi:type="tFormalExpression">PT1S</timeDuration>
      </timerEventDefinition>
    </intermediateCatchEvent>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="pause"/>
    <sequenceFlow id="f2" sourceRef="pause" targetRef="end"/>
  </process>
</definitions>`

// stillWaiting reports whether the instance is still parked on its timer, read
// through the counters the operations overview reads rather than through the engine.
func stillWaiting(t *testing.T, srv *Server) bool {
	t.Helper()
	code, raw := serveInternal(t, srv, http.MethodGet, "/api/v1/instances/summary", "", "")
	if code != http.StatusOK {
		t.Fatalf("summary: status=%d body=%s", code, raw)
	}
	var rows []instanceSummaryRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("decode summary: %v (%s)", err, raw)
	}
	for _, r := range rows {
		if r.ProcessID == "waits" {
			return r.Active == 1 && r.Completed == 0
		}
	}
	t.Fatalf("the deployed process is not in the summary: %s", raw)
	return false
}

func TestAHeldTimerSchedulerFiresNothingUntilItIsTicked(t *testing.T) {
	ticks := make(chan time.Time)
	srv, _ := newOffLoopServer(t, withTimerTrigger(ticks))

	code, body := serveInternal(t, srv, http.MethodPost, "/api/v1/deployments", timerBPMN, "application/xml")
	if code != http.StatusOK {
		t.Fatalf("deploy: status=%d body=%s", code, body)
	}
	var deploy struct {
		Key uint64 `json:"key"`
	}
	if err := json.Unmarshal(body, &deploy); err != nil {
		t.Fatalf("decode deploy: %v", err)
	}
	if code, body := serveInternal(t, srv,
		http.MethodPost, fmt.Sprintf("/api/v1/processes/%d/instances", deploy.Key), "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance: status=%d body=%s", code, body)
	}

	// Longer than the timer's own second *and* than the scheduler's, which is the whole
	// point: a ticker still running on wall time fires at least once inside this, with
	// the timer long due. It is the one assertion here that cannot be made without
	// waiting for a real duration, and the duration is the thing being disproved.
	time.Sleep(2500 * time.Millisecond)
	if !stillWaiting(t, srv) {
		t.Fatal("the instance moved on with nobody ticking: the scheduler is still running on its own clock")
	}

	// And it is held, not broken: one tick does what the second would have done.
	ticks <- time.Now()
	deadline := time.Now().Add(5 * time.Second)
	for stillWaiting(t, srv) {
		if time.Now().After(deadline) {
			t.Fatal("the instance is still waiting after a tick; the seam does not drive the scheduler at all")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
