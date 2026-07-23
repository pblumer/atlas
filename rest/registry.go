// Package rest integrates an external HTTP-REST API as a server-registered Atlas
// connector: a BPMN REST connector task calls a configured REST endpoint through
// the job path (ADR-0036), mirroring how the clio package appends an event and
// the dmn package delegates a decision to temis (ADR-0014). The integration
// inherits the job protocol's durability and non-blocking properties (ADR-0007):
//
//   - A connector task creates a job carrying the reserved [compiler.RestJobType].
//     The processor never performs the outbound call itself, so it stays
//     allocation-free (invariant I1) and free of any HTTP dependency.
//   - The in-process [Handler] — a job worker — pulls those jobs, calls the REST
//     API off the processor goroutine and after fsync (invariant I2, never inside
//     applyToState / I4), and completes the job, which drives the token onward.
//   - The base endpoint and credentials live in a server-side [Registry] keyed by
//     connector name, so a model refers to a connector by name only and never
//     carries a URL or secret (ADR-0036).
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
)

// Request is one HTTP call a connector task makes. Path is appended to the
// connector's configured base endpoint to form the request URL. Body, when
// non-nil, is sent as a JSON request body (the worker attaches it only for
// methods that carry one). IdempotencyKey is deterministic (the job key), so an
// at-least-once retry can be de-duplicated by the target API.
type Request struct {
	Method         string
	Path           string
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

// Client calls one REST API. It is an interface so the worker is testable without
// a live server and so a connector name binds to exactly one endpoint.
type Client interface {
	Do(ctx context.Context, r Request) (Response, error)
}

// Registry resolves a connector name to the [Client] for its REST API.
// Connectors are registered at the server from configuration (base endpoint plus
// credentials), so a model refers to a connector by name only (ADR-0036). A
// Registry is read-only once populated and safe for concurrent use by workers.
type Registry struct {
	clients map[string]Client
}

// NewRegistry creates an empty connector registry.
func NewRegistry() *Registry { return &Registry{clients: map[string]Client{}} }

// Register binds a connector name to its client. Registering the same name again
// replaces the earlier binding (last write wins), so reconfiguration is simple.
// Populate the registry before the processes that use it start running.
func (r *Registry) Register(name string, c Client) { r.clients[name] = c }

// Client returns the client bound to name, or nil and false if none is
// registered.
func (r *Registry) Client(name string) (Client, bool) {
	c, ok := r.clients[name]
	return c, ok
}

// Connector is the server-side configuration of one REST connector: the base
// endpoint of the API and an optional bearer token for it.
type Connector struct {
	Endpoint string
	Token    string
}

// HTTPClient calls a real REST API over HTTP.
//
// It sends Request.Body as a JSON body (when present) to {Endpoint}{Path} with an
// Idempotency-Key header, and decodes a JSON response body. A non-2xx status is
// returned as an error so the job stays pending and is retried (at-least-once).
type HTTPClient struct {
	conn Connector
	http *http.Client
}

// NewHTTPClient builds a REST HTTP client for a configured connector.
func NewHTTPClient(conn Connector) *HTTPClient {
	return &HTTPClient{conn: conn, http: http.DefaultClient}
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
	req, err := http.NewRequestWithContext(ctx, r.Method, c.conn.Endpoint+r.Path, reqBody)
	if err != nil {
		return Response{}, fmt.Errorf("rest: build request: %w", err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if r.IdempotencyKey != "" {
		req.Header.Set("Idempotency-Key", r.IdempotencyKey)
	}
	if c.conn.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.conn.Token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("rest: call %s %s: %w", r.Method, r.Path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return Response{}, fmt.Errorf("rest: read response from %s %s: %w", r.Method, r.Path, err)
	}
	if resp.StatusCode/100 != 2 {
		return Response{}, fmt.Errorf("rest: %s %s returned HTTP %d", r.Method, r.Path, resp.StatusCode)
	}
	return Response{Status: resp.StatusCode, Body: decodeBody(raw)}, nil
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
