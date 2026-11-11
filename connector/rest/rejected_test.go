package rest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pblumer/atlas/job"
)

// A REST task's failure says whose fault it was. A 4xx other than 408 and 429 is the
// far side answering that the request was wrong — the unencoded '#' that sent a task to
// the wrong route and drew a 400 — and is marked rejected, so the circuit breaker does
// not read it as the target being down (ADR-0340, amended 2026-11-11). A 5xx, a timeout,
// a rate limit and an unreachable host are the target's trouble and stay plain failures.
func TestHTTPClientMarksWhatTheTargetRefusedAsRejected(t *testing.T) {
	for status, want := range map[int]bool{
		http.StatusBadRequest: true, http.StatusUnauthorized: true, http.StatusNotFound: true,
		http.StatusUnprocessableEntity: true,
		http.StatusRequestTimeout:      false, http.StatusTooManyRequests: false,
		http.StatusInternalServerError: false, http.StatusServiceUnavailable: false, http.StatusBadGateway: false,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		}))
		_, err := NewHTTPClient().Do(context.Background(), Request{Method: "POST", URL: srv.URL + "/x"})
		srv.Close()
		if err == nil {
			t.Fatalf("HTTP %d: err = nil, want the call to fail", status)
		}
		if got := job.IsRejected(err); got != want {
			t.Errorf("HTTP %d: rejected = %v, want %v (%v)", status, got, want, err)
		}
	}
	_, err := NewHTTPClient().Do(context.Background(), Request{Method: "GET", URL: "http://127.0.0.1:1/x"})
	if err == nil || job.IsRejected(err) {
		t.Errorf("unreachable host: err = %v, want a plain failure", err)
	}
}
