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
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
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

// InboundEvent is one event read back from clio (a read/query result, or an event
// the inbound bridge consumes, ADR-0074). ID is clio's globally-unique event id
// (the cursor/dedup key); Seq is the monotonic per-subject sequence.
type InboundEvent struct {
	ID      string         `json:"id"`
	Seq     uint64         `json:"seq"`
	Subject string         `json:"subject"`
	Type    string         `json:"type"`
	Data    map[string]any `json:"data"`
}

// ReadEventsRequest selects the events a read returns: a Subject, an optional
// exclusive AfterID cursor ("" reads from the start), whether to include the
// subject's subtree, an optional type filter, and a Limit (0 = the connector's
// default).
type ReadEventsRequest struct {
	Subject   string
	AfterID   string
	Recursive bool
	Types     []string
	Limit     int
}

// Client talks to one clio instance. It is an interface so the worker and the
// inbound bridge are testable without a live clio and so a connector name binds to
// exactly one endpoint. WriteEvent appends a domain event; GetState reads a
// projection; Query runs a stored query; ReadEvents reads a subject's events.
type Client interface {
	WriteEvent(ctx context.Context, e Event) error
	GetState(ctx context.Context, subject, reduceSpec string) (map[string]any, error)
	Query(ctx context.Context, query string) (any, error)
	ReadEvents(ctx context.Context, req ReadEventsRequest) ([]InboundEvent, error)
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

// Replace swaps the whole set of registered connectors at once, so a server can
// rebuild the registry from managed configuration after a change (ADR-0041). The
// caller must serialize Replace with the workers that read the registry — the
// Atlas server does both on its run-loop goroutine — so no lock is needed. A nil
// map clears the registry.
func (r *Registry) Replace(clients map[string]Client) {
	if clients == nil {
		clients = map[string]Client{}
	}
	r.clients = clients
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

// GetState reads a projection (reduceSpec) for a subject and returns its state
// object. Provisional wire format: GET {Endpoint}/api/state?subject=…&reduceSpec=….
func (c *HTTPClient) GetState(ctx context.Context, subject, reduceSpec string) (map[string]any, error) {
	q := url.Values{"subject": {subject}}
	if reduceSpec != "" {
		q.Set("reduceSpec", reduceSpec)
	}
	var out map[string]any
	if err := c.getJSON(ctx, "/api/state?"+q.Encode(), &out); err != nil {
		return nil, fmt.Errorf("clio: get-state for %q: %w", subject, err)
	}
	return out, nil
}

// Query runs a stored query and returns its result (rows/object). Provisional wire
// format: POST {Endpoint}/api/query with {"query":…}.
func (c *HTTPClient) Query(ctx context.Context, query string) (any, error) {
	body, err := json.Marshal(map[string]any{"query": query})
	if err != nil {
		return nil, fmt.Errorf("clio: encode query: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.conn.Endpoint+"/api/query", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("clio: build query request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("clio: run-query: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("clio: run-query returned HTTP %d", resp.StatusCode)
	}
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("clio: decode query result: %w", err)
	}
	return out, nil
}

// ReadEvents reads a subject's events oldest-first. Provisional wire format: GET
// {Endpoint}/api/events/read?subject=…&lowerBound=…&recursive=…&limit=… returning
// NDJSON (one event JSON per line). clio's lowerBound is inclusive but AfterID is
// the last-consumed id, so the first line equal to AfterID is dropped, making
// AfterID an exclusive cursor. This translation is the only place that depends on
// the wire shape; swap it here when the contract firms up.
func (c *HTTPClient) ReadEvents(ctx context.Context, r ReadEventsRequest) ([]InboundEvent, error) {
	q := url.Values{"subject": {r.Subject}}
	if r.AfterID != "" {
		q.Set("lowerBound", r.AfterID)
	}
	if r.Recursive {
		q.Set("recursive", "true")
	}
	if r.Limit > 0 {
		q.Set("limit", strconv.Itoa(r.Limit))
	}
	for _, t := range r.Types {
		q.Add("types", t)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.conn.Endpoint+"/api/events/read?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("clio: build read request: %w", err)
	}
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("clio: read-events for %q: %w", r.Subject, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("clio: read-events for %q returned HTTP %d", r.Subject, resp.StatusCode)
	}
	var out []InboundEvent
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e InboundEvent
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.UseNumber()
		if err := dec.Decode(&e); err != nil {
			return nil, fmt.Errorf("clio: decode read-events line: %w", err)
		}
		if e.ID == r.AfterID { // exclusive cursor: drop the boundary event
			continue
		}
		out = append(out, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("clio: read-events stream for %q: %w", r.Subject, err)
	}
	return out, nil
}

// authorize adds the connector's bearer token to a request when one is configured.
func (c *HTTPClient) authorize(req *http.Request) {
	if c.conn.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.conn.Token)
	}
}

// getJSON performs an authorized GET against a path (relative to the endpoint) and
// decodes the JSON response into out, keeping exact decimals (UseNumber).
func (c *HTTPClient) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.conn.Endpoint+path, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("returned HTTP %d", resp.StatusCode)
	}
	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
