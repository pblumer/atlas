package benchmarks

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pblumer/atlas/api"
	"github.com/pblumer/atlas/engine"
	"github.com/pblumer/atlas/state"
	"github.com/pblumer/atlas/wal"
)

// These benchmarks measure the end-to-end HTTP/API path: the same durable engine as
// the steady-state suite, but driven through api.Server's HTTP handlers instead of
// the processor directly. The gap between a workload here and its engine-level twin
// is the server overhead — request decode, routing, the run-loop handoff (do), the
// stats read the create handler does, and response encode.
//
// Requests are served in-process through the handler (httptest.NewRequest +
// ServeHTTP), so the figure isolates the API layer over the engine and excludes
// TCP/client cost, keeping it reproducible. It is an API-layer benchmark, not a
// network benchmark; a loopback-socket variant is a later slice.

// linearBPMN is the HTTP twin of the engine-level workload #1: Start → Script → End,
// self-completing (the script writes one variable via in-engine FEEL). Its shape
// matches BenchmarkLinearSelfCompleting so the ns/op difference is API-layer overhead.
const linearBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="bench-http-linear" isExecutable="true">
    <startEvent id="s"/>
    <scriptTask id="t">
      <extensionElements><zeebe:script expression="= &quot;done&quot;" resultVariable="status"/></extensionElements>
    </scriptTask>
    <endEvent id="e"/>
    <sequenceFlow id="f1" sourceRef="s" targetRef="t"/>
    <sequenceFlow id="f2" sourceRef="t" targetRef="e"/>
  </process>
</definitions>`

// routerBPMN is the HTTP twin of the engine-level workload #3: Start → XOR(amount >
// 100) → Script → End. Creating it with amount=150 takes the "high" branch and
// self-completes within the create request. Its shape matches
// BenchmarkVariableGatewayRouting.
const routerBPMN = `<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
                    xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="bench-http-router" isExecutable="true">
    <startEvent id="s"/>
    <exclusiveGateway id="gw" default="toLow"/>
    <scriptTask id="high">
      <extensionElements><zeebe:script expression="= &quot;high&quot;" resultVariable="path"/></extensionElements>
    </scriptTask>
    <scriptTask id="low">
      <extensionElements><zeebe:script expression="= &quot;low&quot;" resultVariable="path"/></extensionElements>
    </scriptTask>
    <endEvent id="endHigh"/>
    <endEvent id="endLow"/>
    <sequenceFlow id="s2gw" sourceRef="s" targetRef="gw"/>
    <sequenceFlow id="toHigh" sourceRef="gw" targetRef="high">
      <conditionExpression>= amount &gt; 100</conditionExpression>
    </sequenceFlow>
    <sequenceFlow id="toLow" sourceRef="gw" targetRef="low"/>
    <sequenceFlow id="high2e" sourceRef="high" targetRef="endHigh"/>
    <sequenceFlow id="low2e" sourceRef="low" targetRef="endLow"/>
  </process>
</definitions>`

// durableServer builds an api.Server over the same durable engine profile as the
// engine-level harness (real WAL fsync per batch, real Pebble store) and returns its
// HTTP handler plus the store and WAL dir for metric reporting. The server owns
// background goroutines; b.Cleanup closes it (and then the store and log, which the
// server does not own).
func durableServer(b *testing.B) (http.Handler, *state.Store, string) {
	b.Helper()
	dir := b.TempDir()
	walDir := filepath.Join(dir, "wal")
	log, err := wal.Open(wal.Options{Dir: walDir})
	if err != nil {
		b.Fatalf("wal.Open: %v", err)
	}
	store, err := state.Open(filepath.Join(dir, "state"))
	if err != nil {
		b.Fatalf("state.Open: %v", err)
	}
	proc := engine.New(1, log, store, &benchClock{})
	if err := proc.Recover(); err != nil {
		b.Fatalf("Recover: %v", err)
	}
	srv, err := api.New(proc, store, dir)
	if err != nil {
		b.Fatalf("api.New: %v", err)
	}
	b.Cleanup(func() { srv.Close(); _ = store.Close(); _ = log.Close() })
	return srv.Handler(), store, walDir
}

// serveOK issues one in-process request and fails the benchmark on a non-200.
func serveOK(b *testing.B, h http.Handler, method, path, body, contentType string) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		b.Fatalf("%s %s = %d: %s", method, path, rec.Code, rec.Body.String())
	}
}

// BenchmarkHTTPLinearCreate is workload #1 over the HTTP path: deploy once, then
// POST-create a self-completing instance per iteration. Compare its ns/op to
// BenchmarkLinearSelfCompleting to read off the API-layer overhead.
func BenchmarkHTTPLinearCreate(b *testing.B) {
	h, store, walDir := durableServer(b)
	serveOK(b, h, http.MethodPost, "/api/v1/deployments", linearBPMN, "application/xml")
	startPos := appliedPosition(b, store)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		serveOK(b, h, http.MethodPost, "/api/v1/processes/1/instances", "{}", "application/json")
	}
	b.StopTimer()
	reportEngineMetrics(b, store, walDir, startPos)
}

// BenchmarkHTTPVariableGatewayCreate is workload #3 over the HTTP path: each request
// carries a start variable that routes the instance through an exclusive gateway to
// completion.
func BenchmarkHTTPVariableGatewayCreate(b *testing.B) {
	h, store, walDir := durableServer(b)
	serveOK(b, h, http.MethodPost, "/api/v1/deployments", routerBPMN, "application/xml")
	startPos := appliedPosition(b, store)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		serveOK(b, h, http.MethodPost, "/api/v1/processes/1/instances", `{"variables":{"amount":150}}`, "application/json")
	}
	b.StopTimer()
	reportEngineMetrics(b, store, walDir, startPos)
}
