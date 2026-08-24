package api

import (
	"bufio"
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/opensearch"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// These tests exercise the OpenSearch event exporter loop (ADR-0114)
// deterministically: the loop's real ticker is replaced by an explicit trigger, so a
// test runs an export pass and awaits it via a channel handshake rather than firing a
// short real ticker and polling stub.count() under a deadline. That removes the
// wall-clock dependency the previous timing-based test carried (a 15ms export interval
// raced by a 3s deadline/time.Sleep poll loop), honoring the repository rule that tests
// must not depend on wall-clock time or goroutine scheduling (AGENTS.md). Production is
// unchanged — a real ticker still drives the loop in the running server.

// exporterBPMN parks at a service task, so creating one instance produces a handful of
// durable engine events (instance created, element activations, job created) for the
// exporter to mirror.
const exporterBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="order" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="task">
      <extensionElements><zeebe:taskDefinition type="payment" retries="5"/></extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="task"/>
    <sequenceFlow id="f2" sourceRef="task" targetRef="end"/>
  </process>
</definitions>`

// osStub is a minimal OpenSearch _bulk endpoint that records the document ids it was
// asked to index, so a test can assert the exporter delivered the event log.
type osStub struct {
	srv *httptest.Server
	mu  sync.Mutex
	ids []string
}

func newOSStub(t *testing.T) *osStub {
	t.Helper()
	s := &osStub{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_bulk" {
			http.NotFound(w, r)
			return
		}
		// The body is NDJSON: alternating action / document lines. Count the action
		// lines, which name the target index and the document _id.
		sc := bufio.NewScanner(r.Body)
		s.mu.Lock()
		for sc.Scan() {
			line := bytes.TrimSpace(sc.Bytes())
			if bytes.HasPrefix(line, []byte(`{"index"`)) {
				s.ids = append(s.ids, string(line))
			}
		}
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errors":false,"items":[]}`))
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *osStub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.ids)
}

// TestOpenSearchExporterMirrorsEvents wires the exporter into a live server and
// confirms that running an instance results in event documents being pushed to
// OpenSearch off the run loop (ADR-0114). The export pass is triggered explicitly and
// awaited, so the assertion runs against a completed pass — no deadline, no polling.
func TestOpenSearchExporterMirrorsEvents(t *testing.T) {
	stub := newOSStub(t)
	cfg := opensearch.Config{URL: stub.srv.URL, Index: "atlas-events-test"}

	ticks := make(chan time.Time)
	ticked := make(chan struct{})
	_, x := newSystemServer(t,
		WithOpenSearchExporter(cfg),
		withExporterTrigger(ticks, ticked),
	)

	// Produce events: deploy a definition and start an instance that runs to a parked
	// service task. Both complete durably before the export pass is triggered.
	if code, body := x.do(http.MethodPost, "/api/v1/deployments", exporterBPMN); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	if code, body := x.do(http.MethodPost, "/api/v1/processes/1/instances", "{}"); code != http.StatusOK {
		t.Fatalf("create instance status=%d body=%s", code, body)
	}

	// Trigger one export pass and wait for it to finish. The Bulk POST to the stub runs
	// synchronously inside the pass, so by the time ticked fires the stub has recorded
	// every document. The timeouts are failsafes against a deadlock, not a timing
	// dependency of the passing path.
	triggerExport(t, ticks, ticked)

	if got := stub.count(); got == 0 {
		t.Fatal("exporter indexed no documents; expected the instance events to be mirrored")
	}

	// Every action line targets the configured index — proof the config flowed through
	// and these are our exporter's writes.
	stub.mu.Lock()
	defer stub.mu.Unlock()
	for _, line := range stub.ids {
		if !strings.Contains(line, `"_index":"atlas-events-test"`) {
			t.Fatalf("bulk action did not target the configured index: %s", line)
		}
	}
}

// triggerExport fires one export pass and blocks until it has completed.
func triggerExport(t *testing.T, ticks chan<- time.Time, ticked <-chan struct{}) {
	t.Helper()
	select {
	case ticks <- time.Time{}:
	case <-time.After(2 * time.Second):
		t.Fatal("exporter loop did not accept a tick")
	}
	select {
	case <-ticked:
	case <-time.After(2 * time.Second):
		t.Fatal("export pass did not complete")
	}
}

// TestExporterLoopLogsTickErrorAndRetries drives an export pass against a _bulk
// endpoint that always fails: the loop must log the error and leave the high-water
// mark unadvanced, so the same records are retried on the next pass — at-least-once
// delivery (ADR-0114). Exercising the error branch deterministically (via the injected
// trigger) rather than by racing a real ticker against a paused stub.
func TestExporterLoopLogsTickErrorAndRetries(t *testing.T) {
	fail := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(fail.Close)

	ticks := make(chan time.Time)
	ticked := make(chan struct{})
	srv, x := newSystemServer(t,
		WithOpenSearchExporter(opensearch.Config{URL: fail.URL, Index: "atlas-fail-test"}),
		withExporterTrigger(ticks, ticked),
	)

	// Produce events so the pass has records to index — and thus a Bulk call to fail on.
	if code, body := x.do(http.MethodPost, "/api/v1/deployments", exporterBPMN); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	if code, body := x.do(http.MethodPost, "/api/v1/processes/1/instances", "{}"); code != http.StatusOK {
		t.Fatalf("create instance status=%d body=%s", code, body)
	}

	triggerExport(t, ticks, ticked)

	// The failed pass must not advance the mark: a later pass retries the same records.
	if hwm := srv.exporter.HighWaterMark(); hwm != 0 {
		t.Fatalf("high-water mark advanced to %d despite a failed export; want 0 (retry preserved)", hwm)
	}
}

// TestExporterLoopRealTickerShutsDown covers the production wiring the deterministic
// test bypasses: with no injected trigger the loop builds a real ticker (ADR-0114) and
// must exit promptly on Close rather than leak its goroutine. A hung Close (the failure
// this guards) would block the server's WaitGroup forever.
func TestExporterLoopRealTickerShutsDown(t *testing.T) {
	stub := newOSStub(t)
	dir := t.TempDir()
	log, err := wal.Open(wal.Options{Dir: filepath.Join(dir, "wal")})
	if err != nil {
		t.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	defer func() { _ = store.Close(); _ = log.Close() }()
	proc := engine.New(1, log, store, nil)
	if err := proc.Recover(); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	// Exporter on, a long interval, and no injected trigger → the real-ticker path.
	srv, err := New(proc, store, dir,
		WithOpenSearchExporter(opensearch.Config{URL: stub.srv.URL, Index: "atlas-shutdown-test"}),
		WithOpenSearchExportInterval(time.Hour),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	done := make(chan struct{})
	go func() { srv.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("exporter loop on the real ticker did not shut down on Close")
	}
}
