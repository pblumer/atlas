// Package tracing gives Atlas distributed traces (ADR-0142).
//
// Metrics say *that* a request was slow; a trace says *where* the time went, and — once
// a caller propagates its context — across which services. This package owns the tracer
// provider, the export path, and the one HTTP wrapper that produces spans.
//
// # What is deliberately not traced
//
// The engine. The batch loop is the single writer (invariant I3) and must not allocate
// (invariant I1), and a span is an allocation, a clock read, and a lock. Nothing in
// engine/ imports this package and a test enforces that. Traces here cover the HTTP
// surface, which is where a request's latency is actually attributable and where the
// work is already per-request rather than per-batch.
//
// Probes and scrapes are not traced either: /healthz, /readyz and /metrics run every few
// seconds forever and would drown a backend in spans nobody will ever read.
//
// # Why the exporter is written here
//
// The official OTLP exporter pulls in protobuf and — even in its HTTP form — gRPC: 66
// gRPC packages and about 13MB of binary, for a service that speaks no gRPC anywhere
// else. ADR-0010 asks for few dependencies and ADR-0142 already declined the OTel
// *metrics* SDK on the same grounds. OTLP over HTTP has a documented JSON encoding, so
// this package takes the API and SDK — the parts that are hard and spec-bound: span
// model, sampling, batching, W3C context propagation — and writes the serializer, which
// is a schema, by hand. See otlpjson.go.
package tracing

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// scopeName identifies the instrumentation, not the service; it is what a backend shows
// as the source of the spans.
const scopeName = "github.com/pblumer/atlas"

// DefaultSampleRatio is what fraction of traces are recorded when tracing is turned on
// without a ratio. One in ten keeps a busy server's export affordable while still
// catching enough of the ordinary traffic to be worth reading; a caller that already
// decided to sample is always honored (see the sampler below).
const DefaultSampleRatio = 0.1

// DefaultTimeout bounds one export attempt.
const DefaultTimeout = 10 * time.Second

// Config is how tracing is set up. The zero value is tracing turned off.
type Config struct {
	// Endpoint is the base URL of an OTLP/HTTP receiver, e.g. http://collector:4318.
	// Empty means tracing is off — nothing is sampled and no goroutine is started.
	Endpoint string
	// ServiceName and Version name this process on every exported resource. A backend
	// groups by them; without a name spans arrive as "unknown_service".
	ServiceName string
	Version     string
	// SampleRatio is the fraction of new traces recorded, clamped to [0,1]. It is read
	// literally: zero records nothing. DefaultSampleRatio is what the *flag* defaults
	// to, so an operator who passes 0 gets the nothing they asked for rather than a
	// default inferred behind their back.
	SampleRatio float64
	// Timeout bounds a single export attempt.
	Timeout time.Duration
}

// Enabled reports whether an endpoint is configured.
func (c Config) Enabled() bool { return strings.TrimSpace(c.Endpoint) != "" }

// Provider owns the tracer provider installed by Setup. A nil or disabled Provider is
// safe to use and does nothing, so a caller can defer Shutdown unconditionally.
type Provider struct {
	tp *sdktrace.TracerProvider
}

// Enabled reports whether this provider exports anything.
func (p *Provider) Enabled() bool { return p != nil && p.tp != nil }

// Shutdown flushes whatever is buffered and stops the exporter. It is safe on a nil or
// disabled Provider.
func (p *Provider) Shutdown(ctx context.Context) error {
	if !p.Enabled() {
		return nil
	}
	return p.tp.Shutdown(ctx)
}

// Setup installs the global tracer provider and the W3C context propagator.
//
// With no endpoint it installs nothing: the global provider stays the API's no-op, so
// Handler is a pass-through and an operator who has not asked for tracing pays for
// none of it.
func Setup(c Config) (*Provider, error) {
	// The propagator is installed either way. Reading a caller's traceparent costs
	// nothing when nothing is sampled, and it means turning tracing on does not also
	// require re-deploying whatever calls Atlas.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	if !c.Enabled() {
		return &Provider{}, nil
	}
	exp, err := newOTLPJSONExporter(c)
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(resourceFor(c)),
		// ParentBased means a caller that already decided to sample this trace is
		// honored: sampling half a distributed trace produces a picture with holes in
		// it, which is worse than not having it.
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio(c.SampleRatio)))),
	)
	otel.SetTracerProvider(tp)
	return &Provider{tp: tp}, nil
}

// ratio clamps a configured sample ratio into [0,1].
func ratio(r float64) float64 {
	switch {
	case r < 0:
		return 0
	case r > 1:
		return 1
	default:
		return r
	}
}

// Handler wraps h in a server span named for route.
//
// route is the *pattern* — "GET /api/v1/instances/{key}" — not the URL that matched it.
// That is the whole cardinality discipline in one parameter: the set of span names is
// bounded by the route table, so it cannot grow with traffic. Callers pass the same
// string they registered with the mux, so there is nothing to derive and nothing to get
// wrong at runtime.
//
// With tracing off this is the wrapped handler and one no-op span, which the API's
// no-op provider compiles down to a few nil checks.
func Handler(route string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := otel.Tracer(scopeName).Start(ctx, route,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.String("http.route", route),
			),
		)
		defer span.End()

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h.ServeHTTP(rec, r.WithContext(ctx))

		span.SetAttributes(attribute.Int("http.response.status_code", rec.status))
		// Only 5xx is the server's failure. A 404 or a 400 is the caller being told no,
		// and recording it as an error makes a healthy server's error rate meaningless.
		if rec.status >= http.StatusInternalServerError {
			span.SetStatus(codes.Error, http.StatusText(rec.status))
		}
	})
}

// statusRecorder remembers the status code so the span can carry it. It deliberately
// implements only WriteHeader and Write: anything else a handler needs from the
// ResponseWriter (Flush, Hijack) is reached through the embedded interface by the
// optional-interface assertions net/http already does.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.written {
		s.status, s.written = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.written = true // an implicit 200
	return s.ResponseWriter.Write(b)
}

// Flush forwards to the wrapped writer when it supports flushing, so wrapping a handler
// in a span does not break an SSE stream (ADR-0140 collaboration streams are served
// through this).
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets net/http's ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// endpointURL is where OTLP/HTTP receivers accept trace payloads.
func endpointURL(base string) (string, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return "", fmt.Errorf("tracing: endpoint %q must start with http:// or https://", base)
	}
	return base + "/v1/traces", nil
}
