package api

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime/pprof"
	"runtime/trace"
	"strings"
	"testing"
)

// Profiling a running server (ADR-draft-admin-profiling): an administrator can take what
// `go tool pprof` reads from the server that is slow, without a restart, a second port or
// a build with a debug flag — and nobody else can, and nothing that would carry a secret
// is offered.

// servePprof runs one request against the real route table, without the boundary: the
// role is the boundary's question and TestProfilingIsForAdministrators asks it.
func servePprof(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux, _ := accessTestServer(t).mountRoutes()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// gzipped reports whether a body is the gzip-compressed protobuf `go tool pprof` reads.
func gzipped(b []byte) bool { return len(b) > 2 && b[0] == 0x1f && b[1] == 0x8b }

// TestProfilingIsForAdministrators: a profile shows where the server spends its time and
// what it holds, and taking a CPU profile or a trace costs the server while it runs. Both
// are an administrator's, decided at the boundary like every role.
func TestProfilingIsForAdministrators(t *testing.T) {
	const letThrough = http.StatusTeapot
	for _, name := range []string{"heap", "goroutine", "profile", "trace"} {
		path := "/api/v1/debug/pprof/" + name
		if got := boundaryStatus(t, []string{RoleModeler, RoleOperator, RoleUser, RoleFeedReader, RoleProductManager}, http.MethodGet, path); got != http.StatusForbidden {
			t.Errorf("%s without an admin principal: status=%d, want 403", path, got)
		}
		if got := boundaryStatus(t, []string{RoleAdmin}, http.MethodGet, path); got != letThrough {
			t.Errorf("%s as an administrator: status=%d, want the request to reach the handler", path, got)
		}
	}
}

// TestSnapshotProfilesAreWhatPprofReads: the profiles that describe the server as it is —
// its memory, its goroutines, its threads — come back at once, as the binary `go tool
// pprof` opens, or as text where the runtime offers one.
func TestSnapshotProfilesAreWhatPprofReads(t *testing.T) {
	for _, name := range []string{"heap", "allocs", "goroutine", "threadcreate"} {
		rec := servePprof(t, "/api/v1/debug/pprof/"+name)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", name, rec.Code, rec.Body.Bytes())
		}
		if !gzipped(rec.Body.Bytes()) {
			t.Errorf("%s: the body is not a gzip-compressed profile", name)
		}
		if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, name) {
			t.Errorf("%s: Content-Disposition %q, want an attachment named for the profile", name, cd)
		}
	}

	// debug=2 is the goroutine dump an incident is read from: every stack, as text.
	rec := servePprof(t, "/api/v1/debug/pprof/goroutine?debug=2")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("goroutine?debug=2: status=%d type=%q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), "goroutine ") {
		t.Errorf("goroutine?debug=2 is not a goroutine dump: %.200s", rec.Body.String())
	}
	// gc=1 asks for a collection first, so the heap shows what is live rather than what
	// is merely not yet collected.
	if rec := servePprof(t, "/api/v1/debug/pprof/heap?gc=1"); rec.Code != http.StatusOK || !gzipped(rec.Body.Bytes()) {
		t.Errorf("heap?gc=1: status=%d", rec.Code)
	}
}

// TestCPUProfileAndTraceRecordAWindow: the two that record what the server does over a
// time come back after it, in the formats `go tool pprof` and `go tool trace` open.
func TestCPUProfileAndTraceRecordAWindow(t *testing.T) {
	rec := servePprof(t, "/api/v1/debug/pprof/profile?seconds=1")
	if rec.Code != http.StatusOK || !gzipped(rec.Body.Bytes()) {
		t.Fatalf("profile?seconds=1: status=%d body=%.200s", rec.Code, rec.Body.Bytes())
	}
	rec = servePprof(t, "/api/v1/debug/pprof/trace?seconds=1")
	if rec.Code != http.StatusOK || !bytes.HasPrefix(rec.Body.Bytes(), []byte("go 1.")) {
		t.Fatalf("trace?seconds=1: status=%d body=%.40q", rec.Code, rec.Body.Bytes())
	}
}

// TestOneRecordingAtATime: the runtime keeps one CPU profile and one trace. A second
// request while one runs is told so, rather than handed an empty file.
func TestOneRecordingAtATime(t *testing.T) {
	if err := pprof.StartCPUProfile(io.Discard); err != nil {
		t.Fatalf("start a CPU profile to collide with: %v", err)
	}
	rec := servePprof(t, "/api/v1/debug/pprof/profile?seconds=1")
	pprof.StopCPUProfile()
	if rec.Code != http.StatusConflict {
		t.Errorf("a CPU profile while one runs: status=%d, want 409", rec.Code)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != "" {
		t.Errorf("the refusal still names a download: %q", cd)
	}

	if err := trace.Start(io.Discard); err != nil {
		t.Fatalf("start a trace to collide with: %v", err)
	}
	rec = servePprof(t, "/api/v1/debug/pprof/trace?seconds=1")
	trace.Stop()
	if rec.Code != http.StatusConflict {
		t.Errorf("a trace while one runs: status=%d, want 409", rec.Code)
	}
}

// TestProfilingRefusesWhatItDoesNotOffer: the command line is not offered — a flag can
// carry a secret, and the API is not where one is read back — and neither is a profile
// that would come back empty. A window is bounded, so one request cannot keep the
// profiler for an afternoon.
func TestProfilingRefusesWhatItDoesNotOffer(t *testing.T) {
	for _, name := range []string{"cmdline", "symbol", "block", "mutex", "nonsense"} {
		if rec := servePprof(t, "/api/v1/debug/pprof/"+name); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status=%d, want 404", name, rec.Code)
		}
	}
	for _, q := range []string{
		"profile?seconds=0", "profile?seconds=-5", "profile?seconds=121", "profile?seconds=abc",
		"trace?seconds=0", "trace?seconds=31",
		"heap?debug=2", "goroutine?debug=3", "heap?debug=x", "heap?gc=yes",
	} {
		if rec := servePprof(t, "/api/v1/debug/pprof/"+q); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d, want 400", q, rec.Code)
		}
	}
}

// TestProfilesLeaveAnAuditLine: who looked inside the server, and what they recorded.
func TestProfilesLeaveAnAuditLine(t *testing.T) {
	sink := captureAuditLog(t)
	if rec := servePprof(t, "/api/v1/debug/pprof/heap"); rec.Code != http.StatusOK {
		t.Fatalf("heap: status=%d", rec.Code)
	}
	if rec := servePprof(t, "/api/v1/debug/pprof/profile?seconds=1"); rec.Code != http.StatusOK {
		t.Fatalf("profile: status=%d", rec.Code)
	}
	line := sink.String()
	for _, want := range []string{"profile.downloaded", `"profile":"heap"`, `"profile":"profile"`, `"seconds":1`} {
		if !strings.Contains(line, want) {
			t.Errorf("the audit trail does not record %s: %s", want, line)
		}
	}
}
