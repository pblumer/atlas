package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/pblumer/atlas/tracing"
)

// ADR-0142 slice 8b, from the server's side: the wiring, not the exporter.
//
// The tracing package's own tests prove a span reaches a collector in the right shape.
// What matters here is which requests produce one — that the /api/v1 surface is wrapped
// with its route pattern, and that the endpoints a monitoring system hits forever on a
// timer are not.

// traceCollector captures span names posted to an OTLP receiver.
type traceCollector struct {
	*httptest.Server
	mu    sync.Mutex
	names []string
}

func newTraceCollector(t *testing.T) *traceCollector {
	t.Helper()
	c := &traceCollector{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ResourceSpans []struct {
				ScopeSpans []struct {
					Spans []struct {
						Name string `json:"name"`
					} `json:"spans"`
				} `json:"scopeSpans"`
			} `json:"resourceSpans"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("collector: %v", err)
		}
		c.mu.Lock()
		for _, rs := range body.ResourceSpans {
			for _, ss := range rs.ScopeSpans {
				for _, s := range ss.Spans {
					c.names = append(c.names, s.Name)
				}
			}
		}
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *traceCollector) spanNames() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.names...)
}

// TestTracedRoutesAreNamedByTheirPattern is the wiring assertion: a request to a route
// with a key in the path produces one span named for the *pattern*. If the wrapper ever
// took the URL instead, the span names would grow with the number of instances.
func TestTracedRoutesAreNamedByTheirPattern(t *testing.T) {
	c := newTraceCollector(t)
	p, err := tracing.Setup(tracing.Config{Endpoint: c.URL, ServiceName: "atlas", SampleRatio: 1})
	if err != nil {
		t.Fatalf("tracing.Setup: %v", err)
	}
	f := newReadyFixture(t, true, WithTracing())

	if code, body := f.x.do(http.MethodGet, "/api/v1/info", ""); code != http.StatusOK {
		t.Fatalf("GET /api/v1/info status = %d body = %s", code, body)
	}
	// A route with a path parameter, requested with a real key.
	f.x.do(http.MethodGet, "/api/v1/processes/999", "")

	if err := p.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	names := c.spanNames()
	if len(names) == 0 {
		t.Fatal("no spans were exported for traced routes")
	}
	joined := strings.Join(names, ", ")
	if !strings.Contains(joined, "GET /api/v1/info") {
		t.Errorf("span names = [%s], want one for GET /api/v1/info", joined)
	}
	if strings.Contains(joined, "999") {
		t.Errorf("span names = [%s], want the route pattern rather than the requested key", joined)
	}
}

// TestProbesAndScrapesAreNotTraced. /healthz, /readyz and /metrics are hit every few
// seconds for the life of the process. Tracing them buys nothing and buries the spans
// someone is actually looking for, so they are registered outside the traced table —
// which is a property of the wiring, and therefore worth a test rather than a comment.
func TestProbesAndScrapesAreNotTraced(t *testing.T) {
	c := newTraceCollector(t)
	p, err := tracing.Setup(tracing.Config{Endpoint: c.URL, ServiceName: "atlas", SampleRatio: 1})
	if err != nil {
		t.Fatalf("tracing.Setup: %v", err)
	}
	f := newReadyFixture(t, true, WithTracing())

	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		if code, body := f.x.do(http.MethodGet, path, ""); code != http.StatusOK {
			t.Fatalf("GET %s status = %d body = %s", path, code, body)
		}
	}
	if err := p.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if names := c.spanNames(); len(names) != 0 {
		t.Errorf("probes and scrapes produced spans: %v", names)
	}
}

// TestRoutesAreNotWrappedWithoutTheOption: the default server has no tracing wrapper at
// all, so an operator who never configured a collector pays nothing per request.
func TestRoutesAreNotWrappedWithoutTheOption(t *testing.T) {
	c := newTraceCollector(t)
	p, err := tracing.Setup(tracing.Config{Endpoint: c.URL, ServiceName: "atlas", SampleRatio: 1})
	if err != nil {
		t.Fatalf("tracing.Setup: %v", err)
	}
	f := newReadyFixture(t, true) // no WithTracing

	if code, body := f.x.do(http.MethodGet, "/api/v1/info", ""); code != http.StatusOK {
		t.Fatalf("GET /api/v1/info status = %d body = %s", code, body)
	}
	if err := p.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	if names := c.spanNames(); len(names) != 0 {
		t.Errorf("a server without WithTracing exported spans: %v", names)
	}
}
