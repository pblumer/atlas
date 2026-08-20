// Package rest integrates an external HTTP-REST API as a service-task connector:
// a BPMN REST connector task calls a model-authored endpoint through the job path
// (ADR-0036/0067), mirroring how the dmn package delegates a decision to temis
// (ADR-0014). The integration inherits the job protocol's durability and
// non-blocking properties (ADR-0007):
//
//   - A connector task creates a job carrying the reserved [compiler.RestJobType].
//     The processor never performs the outbound call itself, so it stays
//     allocation-free (invariant I1) and free of any HTTP dependency.
//   - The in-process [Handler] — a job worker — pulls those jobs, calls the REST
//     API off the processor goroutine and after fsync (invariant I2, never inside
//     applyToState / I4), writes the JSON response into the task's result variable,
//     and completes the job, which drives the token onward.
//
// Unlike the clio connector (ADR-0036), a REST task authors its full URL, method,
// headers, and query parameters in the model; credentials are never authored there
// — authentication (basic/bearer/apiKey) names a server-side secret the worker
// resolves at runtime (ADR-0041/0067), so a token never appears in a BPMN file.
//
// Delivery is at-least-once (a crash between "the API accepted the call" and "job
// completed" replays the request); every request carries the job key as an
// Idempotency-Key header so a well-behaved API de-duplicates a replayed
// non-idempotent request rather than performing it twice.
package rest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/pblumer/atlas/connector/nettimeout"
)

// Request is one HTTP call a REST connector task makes. URL is the full,
// model-authored endpoint (ADR-0067). Headers are set on the request (including
// any Authorization/api-key header the worker resolved from a secret); Query is
// appended to the URL. Body, when non-nil, is sent as a JSON request body (the
// worker attaches it only for methods that carry one). IdempotencyKey is
// deterministic (the job key), so an at-least-once retry can be de-duplicated by
// the target API.
type Request struct {
	Method         string
	URL            string
	Headers        map[string]string
	Query          map[string]string
	Body           map[string]any
	IdempotencyKey string
}

// Response is a REST call's outcome. Status is the HTTP status code; Body is the
// decoded JSON response (an object, array, number, string, bool or nil), or the
// raw response text when it is not valid JSON.
type Response struct {
	Status int
	Body   any
}

// Client calls a REST API. It is an interface so the worker is testable without a
// live server.
type Client interface {
	Do(ctx context.Context, r Request) (Response, error)
}

// HTTPClient calls a real REST API over HTTP. It sends Request.Body as a JSON
// body (when present) to Request.URL with an Idempotency-Key header, and decodes a
// JSON response body. A non-2xx status is returned as an error so the job stays
// pending and is retried (at-least-once).
type HTTPClient struct {
	http *http.Client
}

// NewHTTPClient builds a REST HTTP client bounded by the shared connector call
// budget (nettimeout.Default). The worker runs on the run-loop goroutine, so an
// unbounded call would let a hung host stall the whole engine; see the
// nettimeout package doc. A per-connector configurable timeout is a follow-up
// (ADR-0067).
func NewHTTPClient() *HTTPClient {
	return &HTTPClient{http: nettimeout.HTTPClient()}
}

func (c *HTTPClient) Do(ctx context.Context, r Request) (Response, error) {
	var reqBody io.Reader
	if r.Body != nil {
		raw, err := json.Marshal(r.Body)
		if err != nil {
			return Response{}, fmt.Errorf("rest: encode request body: %w", err)
		}
		reqBody = bytes.NewReader(raw)
	}
	reqURL, err := withQuery(r.URL, r.Query)
	if err != nil {
		return Response{}, err
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, reqURL, reqBody)
	if err != nil {
		return Response{}, fmt.Errorf("rest: build request: %w", err)
	}
	// Model-authored headers first, so a task can override the defaults below
	// (e.g. a different Accept or Content-Type) — but never the idempotency key.
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	if reqBody != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	if r.IdempotencyKey != "" {
		req.Header.Set("Idempotency-Key", r.IdempotencyKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("rest: call %s %s: %w", r.Method, r.URL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("rest: read response from %s %s: %w", r.Method, r.URL, err)
	}
	if resp.StatusCode/100 != 2 {
		return Response{}, fmt.Errorf("rest: %s %s returned HTTP %d", r.Method, r.URL, resp.StatusCode)
	}
	return Response{Status: resp.StatusCode, Body: decodeBody(raw)}, nil
}

// withQuery appends the connector's query parameters to the endpoint URL,
// preserving any already present in the model URL. Encoding sorts keys, so the
// request URL is deterministic. An unparseable URL is an error (the job then
// retries/incidents like any other failure).
func withQuery(raw string, q map[string]string) (string, error) {
	if len(q) == 0 {
		return raw, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("rest: parse url %q: %w", raw, err)
	}
	values := u.Query()
	for k, v := range q {
		values.Set(k, v)
	}
	u.RawQuery = values.Encode()
	return u.String(), nil
}

// decodeBody parses a response body as JSON (numbers preserved as json.Number),
// falling back to the raw text when it is not valid JSON and to nil when empty —
// so a caller sees a real nested value for a JSON API and the literal text
// otherwise, never a decode error.
func decodeBody(raw []byte) any {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return string(raw)
	}
	return out
}
