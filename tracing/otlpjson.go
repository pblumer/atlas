package tracing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// The OTLP/HTTP exporter, in its JSON encoding.
//
// OTLP defines two encodings over HTTP — binary protobuf and JSON — and every collector
// that accepts one accepts the other. Taking the official exporter would mean taking
// protobuf and gRPC with it (66 gRPC packages, ~13MB of binary) for a service that
// speaks no gRPC anywhere else, so this writes the JSON encoding directly. What is
// avoided is a serializer; what is kept is the SDK, which owns everything actually
// subtle here: span lifecycle, sampling, batching, and W3C context propagation.
//
// Two details in that encoding are easy to get wrong and are what the tests pin:
//
//   - **IDs are hex, not base64.** proto3 JSON encodes `bytes` as base64, and OTLP/JSON
//     explicitly departs from that for trace and span ids. A collector rejects base64.
//   - **64-bit integers are strings.** proto3 JSON encodes int64/uint64/fixed64 as
//     strings, because a JSON number is a float64 in most parsers and nanosecond
//     timestamps do not survive that. This applies to timestamps and to int attributes.

// newOTLPJSONExporter builds the exporter for a config. It validates the endpoint here,
// at startup, so a typo fails the boot rather than becoming a silent export failure
// discovered when someone goes looking for a trace.
func newOTLPJSONExporter(c Config) (*otlpJSONExporter, error) {
	url, err := endpointURL(c.Endpoint)
	if err != nil {
		return nil, err
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &otlpJSONExporter{url: url, client: &http.Client{Timeout: timeout}}, nil
}

// otlpJSONExporter posts batches of finished spans to an OTLP/HTTP receiver.
type otlpJSONExporter struct {
	url    string
	client *http.Client
}

// ExportSpans is called by the SDK's batch processor, on its own goroutine and after the
// requests that produced the spans have already been answered. An error here is returned
// for the SDK to log and retry; it never reaches a request.
func (e *otlpJSONExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}
	payload, err := json.Marshal(otlpPayload(spans))
	if err != nil {
		return fmt.Errorf("tracing: encode spans: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("tracing: build export request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return fmt.Errorf("tracing: export to %s: %w", e.url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	// The body is drained so the connection can be reused, and read with a bound so a
	// misconfigured endpoint answering with something enormous cannot be a memory
	// problem on a path nobody is watching.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("tracing: export to %s: %s: %s", e.url, resp.Status, bytes.TrimSpace(body))
	}
	return nil
}

// Shutdown releases the exporter's idle connections. The SDK flushes before calling it.
func (e *otlpJSONExporter) Shutdown(context.Context) error {
	e.client.CloseIdleConnections()
	return nil
}

// resourceFor describes this process. service.name is the one attribute a backend
// genuinely needs; without it spans arrive as "unknown_service".
func resourceFor(c Config) *resource.Resource {
	attrs := []attribute.KeyValue{semconv.ServiceName(c.ServiceName)}
	if c.Version != "" {
		attrs = append(attrs, semconv.ServiceVersion(c.Version))
	}
	return resource.NewWithAttributes(semconv.SchemaURL, attrs...)
}

// The OTLP/JSON wire types. They are structs rather than map literals so the field names
// — which are the schema — are declared once and checked by the compiler at every use.

type otlpTraces struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

type otlpResourceSpans struct {
	Resource   otlpResource     `json:"resource"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}

type otlpResource struct {
	Attributes []otlpKeyValue `json:"attributes,omitempty"`
}

type otlpScopeSpans struct {
	Scope otlpScope  `json:"scope"`
	Spans []otlpSpan `json:"spans"`
}

type otlpScope struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type otlpSpan struct {
	TraceID           string         `json:"traceId"`
	SpanID            string         `json:"spanId"`
	ParentSpanID      string         `json:"parentSpanId,omitempty"`
	Name              string         `json:"name"`
	Kind              int            `json:"kind"`
	StartTimeUnixNano string         `json:"startTimeUnixNano"`
	EndTimeUnixNano   string         `json:"endTimeUnixNano"`
	Attributes        []otlpKeyValue `json:"attributes,omitempty"`
	Status            *otlpStatus    `json:"status,omitempty"`
}

type otlpStatus struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
}

type otlpKeyValue struct {
	Key   string    `json:"key"`
	Value otlpValue `json:"value"`
}

// otlpValue is OTLP's AnyValue: exactly one field is set.
type otlpValue struct {
	StringValue *string  `json:"stringValue,omitempty"`
	BoolValue   *bool    `json:"boolValue,omitempty"`
	IntValue    *string  `json:"intValue,omitempty"` // int64 is a string in proto3 JSON
	DoubleValue *float64 `json:"doubleValue,omitempty"`
}

// otlpPayload groups finished spans by the resource they came from. Every span in one
// export shares this process's resource, but the structure is per-resource because that
// is what the schema says, and a receiver reads it that way.
func otlpPayload(spans []sdktrace.ReadOnlySpan) otlpTraces {
	out := otlpTraces{ResourceSpans: []otlpResourceSpans{{
		Resource:   otlpResource{Attributes: attrsToOTLP(spans[0].Resource().Attributes())},
		ScopeSpans: []otlpScopeSpans{{Scope: otlpScope{Name: scopeName}}},
	}}}
	for _, s := range spans {
		out.ResourceSpans[0].ScopeSpans[0].Spans = append(out.ResourceSpans[0].ScopeSpans[0].Spans, spanToOTLP(s))
	}
	return out
}

func spanToOTLP(s sdktrace.ReadOnlySpan) otlpSpan {
	sc := s.SpanContext()
	out := otlpSpan{
		TraceID:           sc.TraceID().String(), // hex, per OTLP/JSON
		SpanID:            sc.SpanID().String(),
		Name:              s.Name(),
		Kind:              spanKind(s.SpanKind()),
		StartTimeUnixNano: unixNano(s.StartTime()),
		EndTimeUnixNano:   unixNano(s.EndTime()),
		Attributes:        attrsToOTLP(s.Attributes()),
	}
	if parent := s.Parent(); parent.HasSpanID() {
		out.ParentSpanID = parent.SpanID().String()
	}
	if st := s.Status(); st.Code != codes.Unset {
		out.Status = &otlpStatus{Code: statusCode(st.Code), Message: st.Description}
	}
	return out
}

// spanKind maps the SDK's kind onto OTLP's enum. The values are the wire format, so they
// are written out rather than derived from the SDK's own numbering, which is a different
// enum that merely happens to line up today.
func spanKind(k trace.SpanKind) int {
	switch k {
	case trace.SpanKindInternal:
		return 1
	case trace.SpanKindServer:
		return 2
	case trace.SpanKindClient:
		return 3
	case trace.SpanKindProducer:
		return 4
	case trace.SpanKindConsumer:
		return 5
	default:
		return 0 // SPAN_KIND_UNSPECIFIED
	}
}

func statusCode(c codes.Code) int {
	switch c {
	case codes.Ok:
		return 1
	case codes.Error:
		return 2
	default:
		return 0
	}
}

func unixNano(t time.Time) string { return strconv.FormatInt(t.UnixNano(), 10) }

func attrsToOTLP(attrs []attribute.KeyValue) []otlpKeyValue {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]otlpKeyValue, 0, len(attrs))
	for _, a := range attrs {
		out = append(out, otlpKeyValue{Key: string(a.Key), Value: valueToOTLP(a.Value)})
	}
	return out
}

func valueToOTLP(v attribute.Value) otlpValue {
	switch v.Type() {
	case attribute.BOOL:
		b := v.AsBool()
		return otlpValue{BoolValue: &b}
	case attribute.INT64:
		s := strconv.FormatInt(v.AsInt64(), 10)
		return otlpValue{IntValue: &s}
	case attribute.FLOAT64:
		f := v.AsFloat64()
		return otlpValue{DoubleValue: &f}
	default:
		// Everything else — strings and the slice types — is emitted as its string
		// form. Atlas records only scalars today, so this is a safety net rather than a
		// lossy path anything actually takes.
		s := v.Emit()
		return otlpValue{StringValue: &s}
	}
}
