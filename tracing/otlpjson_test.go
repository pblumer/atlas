package tracing

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// The wire format, unit by unit. These are the values a collector parses, so they are
// constants of the protocol rather than of this package — getting one wrong produces a
// payload that is accepted and then displayed as something else, which is worse than a
// rejection.

func TestSpanKindsMapToTheOTLPEnum(t *testing.T) {
	for _, tc := range []struct {
		kind trace.SpanKind
		want int
	}{
		{trace.SpanKindUnspecified, 0},
		{trace.SpanKindInternal, 1},
		{trace.SpanKindServer, 2},
		{trace.SpanKindClient, 3},
		{trace.SpanKindProducer, 4},
		{trace.SpanKindConsumer, 5},
	} {
		if got := spanKind(tc.kind); got != tc.want {
			t.Errorf("spanKind(%v) = %d, want %d", tc.kind, got, tc.want)
		}
	}
}

func TestStatusCodesMapToTheOTLPEnum(t *testing.T) {
	for _, tc := range []struct {
		code codes.Code
		want int
	}{
		{codes.Unset, 0},
		{codes.Ok, 1},
		{codes.Error, 2},
	} {
		if got := statusCode(tc.code); got != tc.want {
			t.Errorf("statusCode(%v) = %d, want %d", tc.code, got, tc.want)
		}
	}
}

// TestAttributeValuesCarryTheirOTLPType: AnyValue is a union with one field set, and the
// field says how to read the value. An int sent as a string field is a string in every
// query written against it.
func TestAttributeValuesCarryTheirOTLPType(t *testing.T) {
	got := attrsToOTLP([]attribute.KeyValue{
		attribute.String("s", "text"),
		attribute.Int("i", 42),
		attribute.Bool("b", true),
		attribute.Float64("f", 1.5),
		attribute.StringSlice("sl", []string{"a", "b"}),
	})
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back []map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(back) != 5 {
		t.Fatalf("got %d attributes, want 5", len(back))
	}
	value := func(i int) map[string]any {
		m, _ := back[i]["value"].(map[string]any)
		return m
	}
	if v := value(0)["stringValue"]; v != "text" {
		t.Errorf("string attribute = %#v", value(0))
	}
	// The one that is easy to get wrong: int64 is a JSON *string* in proto3 mapping.
	if v, ok := value(1)["intValue"].(string); !ok || v != "42" {
		t.Errorf("int attribute = %#v, want intValue as the string \"42\"", value(1))
	}
	if v := value(2)["boolValue"]; v != true {
		t.Errorf("bool attribute = %#v", value(2))
	}
	if v := value(3)["doubleValue"]; v != 1.5 {
		t.Errorf("float attribute = %#v", value(3))
	}
	// A slice has no scalar field in this encoder; it degrades to its string form rather
	// than being dropped, so nothing silently vanishes.
	if v, ok := value(4)["stringValue"].(string); !ok || !strings.Contains(v, "a") {
		t.Errorf("slice attribute = %#v, want its string form", value(4))
	}
	// Exactly one field is set per value, or a receiver reading the union sees the wrong
	// one first.
	for i, a := range back {
		if n := len(value(i)); n != 1 {
			t.Errorf("attribute %v has %d value fields set, want exactly 1", a["key"], n)
		}
	}
}

func TestAttrsToOTLPOnNoAttributes(t *testing.T) {
	if got := attrsToOTLP(nil); got != nil {
		t.Errorf("attrsToOTLP(nil) = %#v, want nil so the field is omitted", got)
	}
}

// TestTheEndpointBecomesTheTracesPath: OTLP/HTTP puts traces at /v1/traces under the
// configured base, and an operator writes the base. Getting this wrong means every
// export 404s against a collector that is running perfectly.
func TestTheEndpointBecomesTheTracesPath(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"http://collector:4318", "http://collector:4318/v1/traces"},
		{"http://collector:4318/", "http://collector:4318/v1/traces"},
		{"  https://otel.example.com  ", "https://otel.example.com/v1/traces"},
	} {
		got, err := endpointURL(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("endpointURL(%q) = %q, %v; want %q, nil", tc.in, got, err, tc.want)
		}
	}
}

// TestAnEndpointWithoutASchemeFailsTheBoot. "collector:4318" is the natural thing to
// write and it is not a URL; failing at startup is how an operator finds out, instead of
// discovering weeks later that no trace ever arrived.
func TestAnEndpointWithoutASchemeFailsTheBoot(t *testing.T) {
	if _, err := endpointURL("collector:4318"); err == nil {
		t.Fatal("an endpoint with no scheme was accepted")
	}
	p, err := Setup(Config{Endpoint: "collector:4318", ServiceName: "atlas"})
	if err == nil {
		t.Fatal("Setup accepted an endpoint with no scheme")
	}
	if p != nil {
		t.Error("Setup returned a provider alongside an error")
	}
	if !strings.Contains(err.Error(), "http://") {
		t.Errorf("error %q should say what a valid endpoint looks like", err)
	}
}

func TestSampleRatioIsClamped(t *testing.T) {
	for _, tc := range []struct{ in, want float64 }{
		{-1, 0}, {0, 0}, {0.25, 0.25}, {1, 1}, {5, 1},
	} {
		if got := ratio(tc.in); got != tc.want {
			t.Errorf("ratio(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestTheStatusRecorderStaysOutOfTheWay. Wrapping the ResponseWriter to learn the status
// code must not change what a handler can do with it: an implicit 200, a flush on a
// streaming response (ADR-0140 serves SSE through this), and the real writer underneath
// for anything reaching for it via ResponseController.
func TestTheStatusRecorderStaysOutOfTheWay(t *testing.T) {
	t.Run("implicit 200", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s := &statusRecorder{ResponseWriter: rec, status: http.StatusOK}
		if _, err := s.Write([]byte("body")); err != nil {
			t.Fatalf("Write: %v", err)
		}
		if s.status != http.StatusOK {
			t.Errorf("status = %d after a bare Write, want 200", s.status)
		}
		if rec.Body.String() != "body" {
			t.Errorf("body = %q, want it passed through", rec.Body.String())
		}
	})

	t.Run("first WriteHeader wins", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s := &statusRecorder{ResponseWriter: rec, status: http.StatusOK}
		s.WriteHeader(http.StatusTeapot)
		s.WriteHeader(http.StatusInternalServerError) // net/http ignores a second one
		if s.status != http.StatusTeapot {
			t.Errorf("status = %d, want the first one written (418)", s.status)
		}
	})

	t.Run("flush reaches the real writer", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s := &statusRecorder{ResponseWriter: rec, status: http.StatusOK}
		s.Flush()
		if !rec.Flushed {
			t.Error("Flush did not reach the wrapped writer — an SSE stream would stall")
		}
	})

	t.Run("flush on a writer that cannot", func(t *testing.T) {
		s := &statusRecorder{ResponseWriter: nonFlusher{httptest.NewRecorder()}, status: http.StatusOK}
		s.Flush() // must not panic
	})

	t.Run("unwrap", func(t *testing.T) {
		rec := httptest.NewRecorder()
		s := &statusRecorder{ResponseWriter: rec, status: http.StatusOK}
		if s.Unwrap() != http.ResponseWriter(rec) {
			t.Error("Unwrap did not return the wrapped writer")
		}
	})
}

// nonFlusher hides the recorder's Flush method.
type nonFlusher struct{ inner http.ResponseWriter }

func (n nonFlusher) Header() http.Header         { return n.inner.Header() }
func (n nonFlusher) Write(b []byte) (int, error) { return n.inner.Write(b) }
func (n nonFlusher) WriteHeader(code int)        { n.inner.WriteHeader(code) }

// TestARejectedExportIsReportedWithWhatTheCollectorSaid. A collector that refuses the
// payload — a bad path, a partial-success rejection, an auth failure — is the most
// likely way this exporter is wrong in the field, so the error names the endpoint, the
// status and the body rather than saying "export failed".
func TestARejectedExportIsReportedWithWhatTheCollectorSaid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("unknown field\n"))
	}))
	defer srv.Close()

	exp, err := newOTLPJSONExporter(Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("newOTLPJSONExporter: %v", err)
	}
	defer func() { _ = exp.Shutdown(context.Background()) }()

	err = exp.ExportSpans(context.Background(), finishedSpans(t))
	if err == nil {
		t.Fatal("a 400 from the collector was reported as a successful export")
	}
	for _, want := range []string{srv.URL, "400", "unknown field"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

// TestExportingNothingCostsNothing: the batch processor can call in with an empty batch,
// and posting an empty payload would be a request per tick against the collector.
func TestExportingNothingCostsNothing(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	defer srv.Close()

	exp, err := newOTLPJSONExporter(Config{Endpoint: srv.URL})
	if err != nil {
		t.Fatalf("newOTLPJSONExporter: %v", err)
	}
	if err := exp.ExportSpans(context.Background(), nil); err != nil {
		t.Errorf("ExportSpans(nil) = %v, want nil", err)
	}
	if called {
		t.Error("an empty batch was posted to the collector")
	}
}

// finishedSpans produces one real, finished span through the SDK, so the exporter is
// tested against what it is actually handed rather than a hand-built stub.
func finishedSpans(t *testing.T) []sdktrace.ReadOnlySpan {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec), sdktrace.WithResource(resourceFor(Config{ServiceName: "atlas"})))
	_, span := tp.Tracer(scopeName).Start(context.Background(), "GET /x", trace.WithSpanKind(trace.SpanKindServer))
	span.End()
	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want 1", len(spans))
	}
	out := make([]sdktrace.ReadOnlySpan, len(spans))
	for i, s := range spans {
		out[i] = s
	}
	return out
}
