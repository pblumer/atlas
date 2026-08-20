package tracing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ADR-0142 slice 8b: distributed traces.
//
// Metrics say *that* something is slow; a trace says *where*. The two rules this slice
// is under are the ones the ADR set before any of it was written: the single writer's
// hot path stays untouched (there is a test in drift_test.go for that), and a span's
// name is bounded by the code — a route, never a URL, because a trace backend indexes
// what it is sent and a per-instance span name is a cardinality bomb with a slow fuse.

// collector is a stand-in for an OTLP/HTTP receiver: it captures what Atlas posts.
type collector struct {
	*httptest.Server
	mu       sync.Mutex
	requests []capturedRequest
}

type capturedRequest struct {
	path        string
	contentType string
	body        map[string]any
}

func newCollector(t *testing.T) *collector {
	t.Helper()
	c := &collector{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("collector got a body that is not JSON: %v", err)
		}
		c.mu.Lock()
		c.requests = append(c.requests, capturedRequest{
			path: r.URL.Path, contentType: r.Header.Get("Content-Type"), body: body,
		})
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(c.Close)
	return c
}

func (c *collector) captured() []capturedRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]capturedRequest(nil), c.requests...)
}

// spans digs the span objects out of every captured payload.
func (c *collector) spans(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, req := range c.captured() {
		for _, rs := range asSlice(req.body["resourceSpans"]) {
			for _, ss := range asSlice(asMap(rs)["scopeSpans"]) {
				for _, s := range asSlice(asMap(ss)["spans"]) {
					out = append(out, asMap(s))
				}
			}
		}
	}
	return out
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// record runs one request through a traced handler and flushes, returning the collector.
func record(t *testing.T, cfg Config, route string, h http.Handler, req *http.Request) *collector {
	t.Helper()
	c := newCollector(t)
	cfg.Endpoint = c.URL
	if cfg.ServiceName == "" {
		cfg.ServiceName = "atlas"
	}
	if cfg.SampleRatio == 0 {
		cfg.SampleRatio = 1 // record everything, so a test never depends on a coin flip
	}
	p, err := Setup(cfg)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	Handler(route, h).ServeHTTP(httptest.NewRecorder(), req)
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	return c
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
}

// TestASpanReachesTheCollectorAsOTLPJSON is the wire contract. Atlas writes this
// encoding itself rather than pulling in protobuf and gRPC, so the shape of the payload
// is our responsibility and gets a test rather than a hope.
func TestASpanReachesTheCollectorAsOTLPJSON(t *testing.T) {
	c := record(t, Config{}, "GET /api/v1/processes", okHandler(),
		httptest.NewRequest(http.MethodGet, "/api/v1/processes", nil))

	reqs := c.captured()
	if len(reqs) == 0 {
		t.Fatal("nothing was exported")
	}
	if got := reqs[0].path; got != "/v1/traces" {
		t.Errorf("posted to %q, want /v1/traces", got)
	}
	if got := reqs[0].contentType; got != "application/json" {
		t.Errorf("content-type = %q, want application/json", got)
	}

	spans := c.spans(t)
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	s := spans[0]
	if got := s["name"]; got != "GET /api/v1/processes" {
		t.Errorf("span name = %v, want the route", got)
	}
	// Hex, not base64: OTLP/JSON departs from proto3's default bytes encoding here, and a
	// collector rejects the wrong one.
	traceID, _ := s["traceId"].(string)
	if len(traceID) != 32 || strings.Trim(traceID, "0123456789abcdef") != "" {
		t.Errorf("traceId = %q, want 32 lowercase hex characters", traceID)
	}
	spanID, _ := s["spanId"].(string)
	if len(spanID) != 16 || strings.Trim(spanID, "0123456789abcdef") != "" {
		t.Errorf("spanId = %q, want 16 lowercase hex characters", spanID)
	}
	if got := s["kind"]; got != float64(2) { // SPAN_KIND_SERVER
		t.Errorf("kind = %v, want 2 (server)", got)
	}
	// 64-bit integers are strings in proto3 JSON. A number here silently loses precision
	// in any consumer that parses JSON numbers as float64 — which is most of them.
	start, ok := s["startTimeUnixNano"].(string)
	if !ok {
		t.Fatalf("startTimeUnixNano = %#v, want a string", s["startTimeUnixNano"])
	}
	if n, err := strconv.ParseUint(start, 10, 64); err != nil || n == 0 {
		t.Errorf("startTimeUnixNano = %q, want unix nanoseconds", start)
	}
	if _, ok := s["endTimeUnixNano"].(string); !ok {
		t.Fatalf("endTimeUnixNano = %#v, want a string", s["endTimeUnixNano"])
	}
}

// TestTheServiceIsNamedOnTheResource: a trace backend groups by service, so a span with
// no service name arrives as "unknown_service" and is useless next to anything else.
func TestTheServiceIsNamedOnTheResource(t *testing.T) {
	c := record(t, Config{ServiceName: "atlas", Version: "9.9.9"}, "GET /x", okHandler(),
		httptest.NewRequest(http.MethodGet, "/x", nil))

	reqs := c.captured()
	if len(reqs) == 0 {
		t.Fatal("nothing was exported")
	}
	attrs := map[string]string{}
	for _, rs := range asSlice(reqs[0].body["resourceSpans"]) {
		for _, a := range asSlice(asMap(asMap(rs)["resource"])["attributes"]) {
			am := asMap(a)
			key, _ := am["key"].(string)
			val, _ := asMap(am["value"])["stringValue"].(string)
			attrs[key] = val
		}
	}
	if attrs["service.name"] != "atlas" {
		t.Errorf("service.name = %q, want atlas (resource attrs: %v)", attrs["service.name"], attrs)
	}
	if attrs["service.version"] != "9.9.9" {
		t.Errorf("service.version = %q, want 9.9.9", attrs["service.version"])
	}
}

// TestTheRouteIsTheSpanNameNotTheURL is the cardinality guarantee. A span named for the
// URL it was requested with mints a new name per process instance; a backend indexes
// what it is sent, so that bill arrives weeks later as a slow query and a storage
// invoice. The route pattern is fixed by the code, so the name set is bounded by it.
func TestTheRouteIsTheSpanNameNotTheURL(t *testing.T) {
	c := record(t, Config{}, "GET /api/v1/instances/{key}", okHandler(),
		httptest.NewRequest(http.MethodGet, "/api/v1/instances/8891234567890?verbose=true", nil))

	spans := c.spans(t)
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	name, _ := spans[0]["name"].(string)
	if strings.Contains(name, "8891234567890") {
		t.Errorf("span name %q carries the instance key — that is one span name per instance", name)
	}
	if name != "GET /api/v1/instances/{key}" {
		t.Errorf("span name = %q, want the route pattern", name)
	}
	// The same rule applies to the attributes: the route is recorded, the raw target is
	// not, so a key or a query string cannot ride along into the index.
	for _, a := range asSlice(spans[0]["attributes"]) {
		am := asMap(a)
		val, _ := asMap(am["value"])["stringValue"].(string)
		if strings.Contains(val, "8891234567890") || strings.Contains(val, "verbose=true") {
			t.Errorf("attribute %v carries the raw target", am)
		}
	}
}

// TestAnIncomingTraceIsContinued: the point of W3C trace context. A request arriving
// from another service must land in *that* trace, not start a new one — otherwise every
// hop is its own island and the word "distributed" means nothing.
func TestAnIncomingTraceIsContinued(t *testing.T) {
	const (
		parentTrace = "4bf92f3577b34da6a3ce929d0e0e4736"
		parentSpan  = "00f067aa0ba902b7"
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/processes", nil)
	req.Header.Set("traceparent", "00-"+parentTrace+"-"+parentSpan+"-01")

	c := record(t, Config{}, "GET /api/v1/processes", okHandler(), req)

	spans := c.spans(t)
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	if got := spans[0]["traceId"]; got != parentTrace {
		t.Errorf("traceId = %v, want the caller's %s", got, parentTrace)
	}
	if got := spans[0]["parentSpanId"]; got != parentSpan {
		t.Errorf("parentSpanId = %v, want the caller's %s", got, parentSpan)
	}
}

// TestAFailedRequestIsMarkedAsAnError: an unset status on a 500 makes the one span an
// operator is hunting for look exactly like the thousands that succeeded.
func TestAFailedRequestIsMarkedAsAnError(t *testing.T) {
	boom := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	c := record(t, Config{}, "GET /api/v1/processes", boom,
		httptest.NewRequest(http.MethodGet, "/api/v1/processes", nil))

	spans := c.spans(t)
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	if got := asMap(spans[0]["status"])["code"]; got != float64(2) { // STATUS_CODE_ERROR
		t.Errorf("status code = %v, want 2 (error)", got)
	}
	var status float64
	for _, a := range asSlice(spans[0]["attributes"]) {
		am := asMap(a)
		if am["key"] == "http.response.status_code" {
			// int64 is a string in proto3 JSON, here as everywhere.
			raw, ok := asMap(am["value"])["intValue"].(string)
			if !ok {
				t.Fatalf("intValue = %#v, want a string", asMap(am["value"])["intValue"])
			}
			n, _ := strconv.ParseFloat(raw, 64)
			status = n
		}
	}
	if status != 500 {
		t.Errorf("http.response.status_code = %v, want 500", status)
	}
}

// TestA4xxIsNotAnError: a rejected request is the client's problem, not the server's.
// Marking it an error makes the error rate of a healthy server look terrible and trains
// everyone to ignore it.
func TestA4xxIsNotAnError(t *testing.T) {
	notFound := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	c := record(t, Config{}, "GET /api/v1/processes/{key}", notFound,
		httptest.NewRequest(http.MethodGet, "/api/v1/processes/7", nil))

	spans := c.spans(t)
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	if code := asMap(spans[0]["status"])["code"]; code == float64(2) {
		t.Errorf("a 404 was recorded as a server error")
	}
}

// TestTracingIsOffWithoutAnEndpoint. Nothing is exported, nothing is sampled, and the
// handler is a pass-through — an operator who has not asked for tracing pays for none of
// it, and gets no background goroutine dialling a collector that does not exist.
func TestTracingIsOffWithoutAnEndpoint(t *testing.T) {
	cfg := Config{}
	if cfg.Enabled() {
		t.Fatal("tracing is enabled with no endpoint configured")
	}
	p, err := Setup(cfg)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if p.Enabled() {
		t.Error("Setup with no endpoint produced an enabled provider")
	}

	reached := false
	h := Handler("GET /x", http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	if !reached {
		t.Error("the traced handler did not reach the wrapped handler with tracing off")
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown with tracing off: %v", err)
	}
}

// TestShutdownIsSafeOnANilProvider: cmd defers Shutdown unconditionally, so this is the
// difference between a clean exit and a nil dereference on a server that never traced.
func TestShutdownIsSafeOnANilProvider(t *testing.T) {
	var p *Provider
	if p.Enabled() {
		t.Error("a nil provider reports enabled")
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("Shutdown on a nil provider: %v", err)
	}
}

// TestNothingIsSampledAtRatioZero: the ratio is what makes tracing affordable on a busy
// server, so it has to actually gate export. Zero means zero — the config is read
// literally, and the 10% default lives on the flag rather than being inferred here,
// where it would turn an operator's explicit "trace nothing" into "trace some".
func TestNothingIsSampledAtRatioZero(t *testing.T) {
	c := record(t, Config{SampleRatio: -0.0001}, "GET /x", okHandler(),
		httptest.NewRequest(http.MethodGet, "/x", nil))
	if spans := c.spans(t); len(spans) != 0 {
		t.Errorf("got %d spans at sample ratio 0, want none", len(spans))
	}
}

// TestAnUnreachableCollectorDoesNotBreakTheRequest. Export happens on the SDK's own
// goroutine, after the response; a collector that is down, slow, or returning 500 is an
// observability outage, and it must not become an availability one.
func TestAnUnreachableCollectorDoesNotBreakTheRequest(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	dead.Close() // nothing is listening at this address at all

	p, err := Setup(Config{Endpoint: dead.URL, ServiceName: "atlas", Timeout: 100 * time.Millisecond})
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	defer func() { _ = p.Shutdown(context.Background()) }()

	rec := httptest.NewRecorder()
	Handler("GET /x", okHandler()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("request returned %d with the collector down, want 200", rec.Code)
	}
}
