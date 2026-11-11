package workersdk_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/pblumer/atlas/workersdk"
)

// A rejection — the target answered that the request was wrong — still fails the job,
// with its message and its spent attempt, but says so on the wire, so the server's
// circuit breaker does not read it as the target being down (ADR-0340, amended
// 2026-11-11). Any other failure says nothing extra, which is what an older worker
// sends too.
func TestARejectionFailsTheJobAndSaysItWasTheRequest(t *testing.T) {
	stub, ts := newProtoStub(t, map[string]http.HandlerFunc{
		"/api/v1/jobs/activate": oneJob(`{"jobKey":5,"type":"call","retries":3,"leaseToken":1},{"jobKey":6,"type":"call","retries":3,"leaseToken":2}`),
	})
	err := runOnce(t, ts, workersdk.Options{MaxJobs: 2, NoRetryBackoff: true, Handlers: handling("call", workersdk.ExecFunc(func(_ context.Context, j workersdk.Job) (map[string]any, error) {
		if j.JobKey == 5 {
			// Wrapped, as a handler naturally returns it.
			return nil, fmt.Errorf("rest: %w", workersdk.Rejected(errors.New("POST /x returned HTTP 400")))
		}
		return nil, errors.New("dial tcp: connection refused")
	}))})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	rejected := stub.to("/api/v1/jobs/5/fail")
	if len(rejected) != 1 {
		t.Fatalf("a rejected job was failed %d times, want 1", len(rejected))
	}
	if b := rejected[0].body; b["rejected"] != true || b["retries"] != float64(2) || b["message"] != "rest: POST /x returned HTTP 400" {
		t.Errorf("rejected fail body = %v, want rejected:true, retries 2 and the handler's message", b)
	}
	failed := stub.to("/api/v1/jobs/6/fail")
	if len(failed) != 1 {
		t.Fatalf("a failed job was reported %d times, want 1", len(failed))
	}
	if _, ok := failed[0].body["rejected"]; ok {
		t.Errorf("an ordinary failure carried rejected: %v", failed[0].body)
	}
}

// Rejected leaves nil alone, and IsRejected finds the mark through wrapping.
func TestRejectedWrapsAndUnwraps(t *testing.T) {
	if workersdk.Rejected(nil) != nil {
		t.Error("Rejected(nil) is not nil")
	}
	cause := errors.New("HTTP 404")
	err := fmt.Errorf("outer: %w", workersdk.Rejected(cause))
	if !workersdk.IsRejected(err) {
		t.Error("IsRejected does not see a wrapped rejection")
	}
	if !errors.Is(err, cause) {
		t.Error("a rejection hides the error it marks")
	}
	if workersdk.IsRejected(cause) || workersdk.IsRejected(nil) {
		t.Error("IsRejected is true for an error that was never marked")
	}
	if got := workersdk.Rejected(cause).Error(); got != "HTTP 404" {
		t.Errorf("Error() = %q, want the cause's text unchanged", got)
	}
}

// Which HTTP statuses say the request was wrong rather than that the target is in
// trouble. 408 and 429 are the two 4xx answers about the target's own state — it timed
// out waiting, it is shedding load — and are failures like a 503.
func TestRejectsRequest(t *testing.T) {
	for status, want := range map[int]bool{
		200: false, 302: false,
		400: true, 401: true, 403: true, 404: true, 405: true, 409: true, 410: true, 422: true, 499: true,
		408: false, 429: false,
		500: false, 502: false, 503: false, 504: false,
	} {
		if got := workersdk.RejectsRequest(status); got != want {
			t.Errorf("RejectsRequest(%d) = %v, want %v", status, got, want)
		}
	}
}
