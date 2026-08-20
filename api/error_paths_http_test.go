package api_test

import (
	"net/http"
	"testing"
)

// These tests pin the /api/v1 error contract: a non-numeric key is a 400, a
// malformed JSON body is a 400, and an unknown entity is a 404 where the handler
// checks existence. They assert the status an operator (or the Scalar explorer)
// relies on — meaningful behavior, not just line execution — and in doing so cover
// the parse/lookup error branches that the happy-path tests skip.

// TestInvalidKeyRejected: every keyed route rejects a non-numeric key with 400.
func TestInvalidKeyRejected(t *testing.T) {
	ts := newTestServer(t)
	cases := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/processes/nope/xml"},
		{http.MethodDelete, "/api/v1/processes/nope"},
		{http.MethodGet, "/api/v1/processes/nope/runtime"},
		{http.MethodPost, "/api/v1/processes/nope/instances"},
		{http.MethodPost, "/api/v1/processes/nope/cancel-instances"},
		{http.MethodGet, "/api/v1/collaborations/nope/runtime"},
		{http.MethodGet, "/api/v1/instances/nope/variables"},
		{http.MethodGet, "/api/v1/instances/nope/data-objects"},
		{http.MethodGet, "/api/v1/instances/nope/timeline"},
		{http.MethodGet, "/api/v1/instances/nope/decisions"},
		{http.MethodGet, "/api/v1/instances/nope/jobs"},
		{http.MethodDelete, "/api/v1/instances/nope"},
		{http.MethodPost, "/api/v1/jobs/nope/complete"},
		{http.MethodPost, "/api/v1/jobs/nope/fail"},
		{http.MethodPost, "/api/v1/incidents/nope/resolve"},
		{http.MethodGet, "/api/v1/tasks/nope"},
		{http.MethodPost, "/api/v1/tasks/nope/complete"},
		{http.MethodPost, "/api/v1/tasks/nope/claim"},
		{http.MethodPost, "/api/v1/tasks/nope/unclaim"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			body := ""
			if c.method == http.MethodPost {
				body = "{}"
			}
			if code, b := doReq(t, ts, c.method, c.path, body, "application/json"); code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", code, b)
			}
		})
	}
}

// TestMalformedBodyRejected: a POST with a non-JSON body is a 400.
func TestMalformedBodyRejected(t *testing.T) {
	ts := newTestServer(t)
	cases := []string{
		"/api/v1/feel/validate",
		"/api/v1/feel/evaluate",
		"/api/v1/scripts/run",
		"/api/v1/messages",
		"/api/v1/processes/1/instances",
		"/api/v1/jobs/1/complete",
		"/api/v1/jobs/1/fail",
		"/api/v1/incidents/1/resolve",
		"/api/v1/tasks/1/complete",
		"/api/v1/forms",
	}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			if code, b := doReq(t, ts, http.MethodPost, path, "{not json", "application/json"); code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", code, b)
			}
		})
	}
}

// TestUnknownEntityNotFound: routes that check existence 404 an unknown key.
func TestUnknownEntityNotFound(t *testing.T) {
	ts := newTestServer(t)
	cases := []struct {
		method, path, body string
	}{
		{http.MethodDelete, "/api/v1/processes/999999", ""},
		{http.MethodGet, "/api/v1/processes/999999/xml", ""},
		{http.MethodGet, "/api/v1/processes/999999/runtime", ""},
		{http.MethodPost, "/api/v1/processes/999999/instances", "{}"},
		{http.MethodDelete, "/api/v1/instances/999999", ""},
		{http.MethodPost, "/api/v1/jobs/999999/complete", `{"reason":"test: operator completed the parked job by hand"}`},
		{http.MethodPost, "/api/v1/jobs/999999/fail", "{}"},
		{http.MethodPost, "/api/v1/incidents/999999/resolve", "{}"},
		{http.MethodGet, "/api/v1/tasks/999999", ""},
		{http.MethodPost, "/api/v1/tasks/999999/complete", "{}"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			if code, b := doReq(t, ts, c.method, c.path, c.body, "application/json"); code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (body=%s)", code, b)
			}
		})
	}
}
