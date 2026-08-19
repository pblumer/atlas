// Package scim integrates a SCIM 2.0 service provider as a service-task connector
// (RFC 7643/7644): a BPMN SCIM connector task performs a resource operation —
// create, get, replace, patch, delete, or search a User/Group — against a
// model-authored provider endpoint through the job path (ADR-0152), the same seam
// the rest package uses for a generic HTTP call (ADR-0067). It inherits the job
// protocol's durability and non-blocking properties (ADR-0007):
//
//   - A SCIM connector task creates a job carrying the reserved
//     [compiler.ScimJobType]. The processor never performs the outbound call itself,
//     so it stays allocation-free (invariant I1) and free of any HTTP dependency.
//   - The in-process [Handler] — a job worker — pulls those jobs, calls the SCIM
//     provider off the processor goroutine and after fsync (invariant I2, never
//     inside applyToState / I4), writes the JSON response into the task's result
//     variable, and completes the job, which drives the token onward.
//
// Like REST, a SCIM task authors its base URL, resource type, operation, resource
// id, and filter in the model; credentials are never authored there —
// authentication (basic/bearer/apiKey) names a server-side secret the worker
// resolves at runtime (ADR-0041/0067), so a token never appears in a BPMN file.
// Unlike the generic REST connector it speaks SCIM: it sends and accepts
// application/scim+json, addresses resources by path (…/Users/{id}), and turns a
// SCIM error response (a urn:…:Error object with a detail/scimType) into the job
// failure message rather than an opaque status.
//
// Delivery is at-least-once (a crash between "the provider accepted the call" and
// "job completed" replays the request); every request carries the job key as an
// Idempotency-Key header so a well-behaved provider de-duplicates a replayed
// create rather than provisioning the account twice.
package scim

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/pblumer/atlas/connector/nettimeout"
)

// Request is one SCIM call a connector task makes. URL is the resolved resource
// endpoint (base + resource type, plus /{id} for a single-resource operation).
// Headers are set on the request (including any Authorization/api-key header the
// worker resolved from a secret); Query is appended to the URL (a search's filter).
// Body, when non-nil, is sent as an application/scim+json request body (the worker
// attaches it only for create/replace/patch). IdempotencyKey is deterministic (the
// job key), so an at-least-once retry can be de-duplicated by the provider.
type Request struct {
	Method         string
	URL            string
	Headers        map[string]string
	Query          map[string]string
	Body           map[string]any
	IdempotencyKey string
}

// Response is a SCIM call's outcome. Status is the HTTP status code; Body is the
// decoded JSON response (the resource, a ListResponse, or nil for a 204 delete),
// or the raw response text when it is not valid JSON.
type Response struct {
	Status int
	Body   any
}

// Client calls a SCIM provider. It is an interface so the worker is testable
// without a live server.
type Client interface {
	Do(ctx context.Context, r Request) (Response, error)
}

// HTTPClient calls a real SCIM provider over HTTP. It sends Request.Body as an
// application/scim+json body (when present) with an Idempotency-Key header, and
// decodes a JSON response body. A non-2xx status is returned as an error carrying
// the SCIM error detail (RFC 7644 §3.12) so the job stays pending and is retried
// (at-least-once).
type HTTPClient struct {
	http *http.Client
}

// NewHTTPClient builds a SCIM HTTP client bounded by the shared connector call
// budget (nettimeout.HTTPClient), like the REST connector: the worker runs on the
// run-loop goroutine, so an unbounded call would stall the whole engine (ADR-0149).
func NewHTTPClient() *HTTPClient {
	return &HTTPClient{http: nettimeout.HTTPClient()}
}

// scimContentType is the media type SCIM 2.0 mandates for request and response
// bodies (RFC 7644 §3.1).
const scimContentType = "application/scim+json"

func (c *HTTPClient) Do(ctx context.Context, r Request) (Response, error) {
	var reqBody io.Reader
	if r.Body != nil {
		raw, err := json.Marshal(r.Body)
		if err != nil {
			return Response{}, fmt.Errorf("scim: encode request body: %w", err)
		}
		reqBody = bytes.NewReader(raw)
	}
	reqURL, err := withQuery(r.URL, r.Query)
	if err != nil {
		return Response{}, err
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, reqURL, reqBody)
	if err != nil {
		return Response{}, fmt.Errorf("scim: build request: %w", err)
	}
	// Model-authored headers first, so a task can override the defaults below — but
	// never the idempotency key.
	for k, v := range r.Headers {
		req.Header.Set(k, v)
	}
	if reqBody != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", scimContentType)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", scimContentType)
	}
	if r.IdempotencyKey != "" {
		req.Header.Set("Idempotency-Key", r.IdempotencyKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("scim: call %s %s: %w", r.Method, r.URL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("scim: read response from %s %s: %w", r.Method, r.URL, err)
	}
	if resp.StatusCode/100 != 2 {
		return Response{}, fmt.Errorf("scim: %s %s returned HTTP %d%s", r.Method, r.URL, resp.StatusCode, scimErrorDetail(raw))
	}
	return Response{Status: resp.StatusCode, Body: decodeBody(raw)}, nil
}

// withQuery appends the connector's query parameters (a search's filter) to the
// endpoint URL, preserving any already present. Encoding sorts keys, so the request
// URL is deterministic. An unparseable URL is an error (the job then
// retries/incidents like any other failure).
func withQuery(raw string, q map[string]string) (string, error) {
	if len(q) == 0 {
		return raw, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("scim: parse url %q: %w", raw, err)
	}
	values := u.Query()
	for k, v := range q {
		values.Set(k, v)
	}
	u.RawQuery = values.Encode()
	return u.String(), nil
}

// scimErrorDetail extracts the human-readable detail from a SCIM error response
// (RFC 7644 §3.12: a JSON object with a "detail" and optional "scimType"), so a
// failed job's incident names the provider's own reason (e.g. "uniqueness: userName
// already exists") rather than only an HTTP status. It returns "" when the body is
// not a recognizable SCIM error, and always begins with a separator so it can be
// concatenated onto the status line.
func scimErrorDetail(raw []byte) string {
	var e struct {
		Detail   string `json:"detail"`
		ScimType string `json:"scimType"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return ""
	}
	detail := strings.TrimSpace(e.Detail)
	if detail == "" {
		return ""
	}
	if t := strings.TrimSpace(e.ScimType); t != "" {
		return ": " + t + ": " + detail
	}
	return ": " + detail
}

// decodeBody parses a response body as JSON (numbers preserved as json.Number),
// falling back to the raw text when it is not valid JSON and to nil when empty — so
// a caller sees a real nested value for a SCIM resource and the literal text
// otherwise, never a decode error. A 204 (delete) has an empty body and decodes to
// nil.
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
