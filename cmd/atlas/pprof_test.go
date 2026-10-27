package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestTheStandardPprofHandlersAreNotLinked: importing net/http/pprof registers its
// handlers on http.DefaultServeMux as a side effect — without a login, a role or a bound
// on a recording. Atlas serves profiles behind the admin role instead
// (ADR-draft-admin-profiling), and nothing serves that mux today; this keeps the day
// something does from being the day the profiler goes public. It sits here because this
// test binary links everything the server does, dependencies included.
func TestTheStandardPprofHandlersAreNotLinked(t *testing.T) {
	_, pattern := http.DefaultServeMux.Handler(httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil))
	if pattern != "" {
		t.Errorf("http.DefaultServeMux serves %q: something links net/http/pprof", pattern)
	}
}
