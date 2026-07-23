// Package clio integrates a clio event store as a server-registered Atlas
// connector: a BPMN clio "write-events" connector task appends an event to a
// configured clio instance through the job path (ADR-0036), mirroring how the
// dmn package delegates a decision to temis (ADR-0014). The integration inherits
// the job protocol's durability and non-blocking properties (ADR-0007):
//
//   - A connector task creates a job carrying the reserved
//     [compiler.ClioWriteJobType]. The processor never performs the outbound call
//     itself, so it stays allocation-free (invariant I1) and free of any HTTP
//     dependency.
//   - The in-process [Handler] — a job worker — pulls those jobs, appends the
//     event to clio off the processor goroutine and after fsync (invariant I2,
//     never inside applyToState / I4), and completes the job, which drives the
//     token onward.
//   - The clio endpoint and credentials live in a server-side [Registry] keyed by
//     connector name, so a model refers to a connector by name only and never
//     carries a URL or secret (ADR-0036).
//
// Delivery is at-least-once (a crash between "clio accepted" and "job completed"
// replays the write); every event carries the job key as an idempotency key so
// clio de-duplicates a replayed write rather than doubling the event.
package clio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// Event is one event a connector task appends to clio. IdempotencyKey is
// deterministic (the job key), so an at-least-once retry is de-duplicated by
// clio rather than appended twice.
type Event struct {
	Subject        string
	Type           string
	Data           map[string]any
	IdempotencyKey string
}

// Client appends events to one clio instance. It is an interface so the worker
// is testable without a live clio and so a connector name binds to exactly one
// endpoint.
type Client interface {
	WriteEvent(ctx context.Context, e Event) error
}

// Registry resolves a connector name to the [Client] for its clio instance.
// Connectors are registered at the server from configuration (endpoint plus
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

// Connector is the server-side configuration of one clio connector: the base
// endpoint of the clio instance and an optional bearer token for it.
type Connector struct {
	Endpoint string
	Token    string
}

// HTTPClient talks to a real clio instance over HTTP.
//
// The wire format is provisional pending the clio API contract: it POSTs the
// event as JSON to {Endpoint}/api/events with an Idempotency-Key header, so an
// at-least-once retry is de-duplicated by clio. Swap the path/shape here when the
// contract is fixed; nothing outside this method depends on it.
type HTTPClient struct {
	conn Connector
	http *http.Client
}

// NewHTTPClient builds a clio HTTP client for a configured connector.
func NewHTTPClient(conn Connector) *HTTPClient {
	return &HTTPClient{conn: conn, http: http.DefaultClient}
}

func (c *HTTPClient) WriteEvent(ctx context.Context, e Event) error {
	body, err := json.Marshal(map[string]any{
		"subject": e.Subject,
		"type":    e.Type,
		"data":    e.Data,
	})
	if err != nil {
		return fmt.Errorf("clio: encode event: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.conn.Endpoint+"/api/events", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("clio: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if e.IdempotencyKey != "" {
		req.Header.Set("Idempotency-Key", e.IdempotencyKey)
	}
	if c.conn.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.conn.Token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("clio: post event: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("clio: write-events to %q returned HTTP %d", e.Subject, resp.StatusCode)
	}
	return nil
}
