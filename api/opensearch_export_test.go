package api_test

import (
	"bufio"
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/opensearch"
)

// osStub is a minimal OpenSearch _bulk endpoint that records the document ids it
// was asked to index, so a test can assert the exporter delivered the event log.
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
// confirms that deploying a process and running an instance results in event
// documents being pushed to OpenSearch off the run loop (ADR-0114).
func TestOpenSearchExporterMirrorsEvents(t *testing.T) {
	stub := newOSStub(t)
	cfg := opensearch.Config{URL: stub.srv.URL, Index: "atlas-events-test"}
	ts := newTestServerWith(t,
		api.WithOpenSearchExporter(cfg),
		api.WithOpenSearchExportInterval(15*time.Millisecond),
	)

	// Produce events: deploy a definition and start an instance that runs to a
	// parked service task.
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/deployments", sampleBPMN, "application/xml"); code != http.StatusOK {
		t.Fatalf("deploy status=%d body=%s", code, body)
	}
	if code, body := doReq(t, ts, http.MethodPost, "/api/v1/processes/1/instances", "{}", "application/json"); code != http.StatusOK {
		t.Fatalf("create instance status=%d body=%s", code, body)
	}

	// The exporter polls off the run loop; wait for it to mirror the events.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if stub.count() > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := stub.count(); got == 0 {
		t.Fatalf("exporter indexed no documents; expected the deploy/instance events to be mirrored")
	}

	// Every action line targets the configured index — proof the config flowed
	// through and these are our exporter's writes.
	stub.mu.Lock()
	defer stub.mu.Unlock()
	for _, line := range stub.ids {
		if !strings.Contains(line, `"_index":"atlas-events-test"`) {
			t.Fatalf("bulk action did not target the configured index: %s", line)
		}
	}
}
