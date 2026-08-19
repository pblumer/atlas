package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// ADR-0142 slice 7: readiness, distinct from liveness.
//
// /healthz answers "is this process alive" and always has. That is the right answer for
// a liveness probe — a probe whose only remedy is a restart must not fail for anything a
// restart would not fix — but it is the wrong answer for a readiness probe, and until
// this slice the Helm chart pointed all three probes at it. A pod whose store cannot be
// read, or whose single writer is wedged, answered "ok" and kept receiving traffic.
//
// The tests below are the ways readiness can be false, plus the one thing that must not
// change: /healthz stays liveness-only.

// readyFixture is a server over a store and log the test owns, so a test can break one
// of them out from under the server. Unlike newTestServer it does not insist the
// processor has recovered.
type readyFixture struct {
	srv         *Server
	x           deployTestHarness
	store       *state.Store
	log         *wal.Log
	storeClosed bool
	srvClosed   bool
}

func newReadyFixture(t *testing.T, recovered bool, opts ...Option) *readyFixture {
	t.Helper()
	dir := t.TempDir()
	lg, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, lg, store, nil)
	if recovered {
		if err := proc.Recover(); err != nil {
			t.Fatalf("Recover: %v", err)
		}
	}
	srv, err := New(proc, store, dir, opts...)
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}
	f := &readyFixture{srv: srv, x: deployTestHarness{t, srv.Handler()}, store: store, log: lg}
	t.Cleanup(func() {
		f.closeServer()
		f.closeStore(t)
		if err := lg.Close(); err != nil {
			t.Errorf("log.Close: %v", err)
		}
	})
	return f
}

func (f *readyFixture) closeServer() {
	if f.srvClosed {
		return
	}
	f.srvClosed = true
	f.srv.Close()
}

func (f *readyFixture) closeStore(t *testing.T) {
	t.Helper()
	if f.storeClosed {
		return
	}
	f.storeClosed = true
	if err := f.store.Close(); err != nil {
		t.Errorf("store.Close: %v", err)
	}
}

// ready fetches /readyz and returns the status and the body as text.
func (f *readyFixture) ready(t *testing.T) (int, string) {
	t.Helper()
	code, body := f.x.do(http.MethodGet, "/readyz", "")
	return code, string(body)
}

// TestReadyzIsReadyOnARecoveredServer: the happy path — recovery done, store readable,
// run loop answering.
func TestReadyzIsReadyOnARecoveredServer(t *testing.T) {
	f := newReadyFixture(t, true)
	code, body := f.ready(t)
	if code != http.StatusOK || !strings.Contains(body, "ok") {
		t.Fatalf("GET /readyz = %d %q, want 200 ok", code, body)
	}
}

// TestReadyzFailsWhileRecoveryIsIncomplete is the ordering contract: a server whose
// processor has not replayed the log holds state that does not yet describe reality, so
// it must not be routed traffic.
func TestReadyzFailsWhileRecoveryIsIncomplete(t *testing.T) {
	f := newReadyFixture(t, false)
	code, body := f.ready(t)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz = %d %q, want 503 before recovery", code, body)
	}
	if !strings.Contains(body, "recover") {
		t.Errorf("body = %q, want it to name recovery as the reason", body)
	}
}

// TestReadyzFailsWhenTheStateStoreCannotBeRead: a store that cannot answer a read is a
// store the engine cannot serve from. The reason reaches the operator as a 503 with a
// message rather than as a dropped connection.
func TestReadyzFailsWhenTheStateStoreCannotBeRead(t *testing.T) {
	f := newReadyFixture(t, true)
	f.closeStore(t) // the store is gone; the server does not know yet

	code, body := f.ready(t)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz = %d %q, want 503 with an unreadable store", code, body)
	}
	if !strings.Contains(body, "state store") {
		t.Errorf("body = %q, want it to name the state store as the reason", body)
	}
}

// TestReadyzFailsWhenTheWriterIsWedged is the failure /healthz structurally cannot see:
// the process is alive and answering HTTP, but the goroutine that owns the processor is
// stuck — a blocked fsync on a hung volume looks exactly like this. Readiness bounds its
// wait and reports not-ready rather than hanging with it.
func TestReadyzFailsWhenTheWriterIsWedged(t *testing.T) {
	f := newReadyFixture(t, true, withReadyTimeout(50*time.Millisecond))

	wedge := make(chan struct{})
	defer close(wedge) // let the run loop finish, or Close would wait for it forever
	f.srv.tasks <- func() { <-wedge }

	start := time.Now()
	code, body := f.ready(t)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz = %d %q, want 503 with a wedged run loop", code, body)
	}
	if !strings.Contains(body, "not responding") {
		t.Errorf("body = %q, want it to say the writer is not responding", body)
	}
	// The point of the deadline: the probe fails, it does not hang alongside the writer.
	// The bound is far below the production default, so a probe that ignored its
	// deadline — or waited out the default one — is caught rather than merely slow.
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("probe took %v with a 50ms deadline, want it to give up at that deadline", elapsed)
	}
}

// TestReadyzGivesUpWhenTheProberDoes: a kubelet that hits its own timeoutSeconds hangs
// up, and the handler should stop waiting on the writer rather than hold a goroutine
// until its deadline for a connection nobody is reading.
func TestReadyzGivesUpWhenTheProberDoes(t *testing.T) {
	f := newReadyFixture(t, true, withReadyTimeout(30*time.Second))

	wedge := make(chan struct{})
	defer close(wedge)
	f.srv.tasks <- func() { <-wedge }

	// Cancelled before the request is served, with the run loop wedged: the only way out
	// of the handler is the caller's context.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	start := time.Now()
	f.srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz = %d %q, want 503", rec.Code, rec.Body.String())
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("probe took %v after the caller gave up, want it to return at once", elapsed)
	}
}

// TestReadyzFailsOnceTheServerIsClosing: after Close the run loop is gone and do() is a
// silent no-op, so every write would be dropped. Failing readiness is what takes the
// endpoint out of rotation instead of accepting work that goes nowhere.
func TestReadyzFailsOnceTheServerIsClosing(t *testing.T) {
	f := newReadyFixture(t, true)
	f.closeServer()

	code, body := f.ready(t)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("GET /readyz = %d %q, want 503 after Close", code, body)
	}
	if !strings.Contains(body, "shutting down") {
		t.Errorf("body = %q, want it to say the server is shutting down", body)
	}
}

// TestHealthzStaysLivenessOnly guards the split from the other side. If /healthz ever
// grew the readiness checks, a Kubernetes liveness probe would restart a pod in the
// middle of a long recovery — repeatedly, and forever, since every restart makes the
// replay start over.
func TestHealthzStaysLivenessOnly(t *testing.T) {
	f := newReadyFixture(t, false) // never recovered: not ready, but alive
	code, body := f.x.do(http.MethodGet, "/healthz", "")
	if code != http.StatusOK || !strings.Contains(string(body), "ok") {
		t.Fatalf("GET /healthz = %d %q, want 200 ok on an unrecovered server", code, body)
	}
}
