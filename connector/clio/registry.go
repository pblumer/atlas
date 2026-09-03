// Package clio integrates a clio event store as a server-registered Atlas
// worker: a BPMN clio "write-events" task appends an event to a
// configured clio instance through the job path (ADR-0036), mirroring how the
// dmn package delegates a decision to temis (ADR-0014). The integration inherits
// the job protocol's durability and non-blocking properties (ADR-0007):
//
//   - A task creates a job carrying the reserved
//     [compiler.ClioWriteJobType]. The processor never performs the outbound call
//     itself, so it stays allocation-free (invariant I1) and free of any HTTP
//     dependency.
//   - The in-process [Handler] — a job worker — pulls those jobs, appends the
//     event to clio off the processor goroutine and after fsync (invariant I2,
//     never inside applyToState / I4), and completes the job, which drives the
//     token onward.
//   - The clio endpoint and credentials live in a server-side [Registry] keyed by
//     worker name, so a model refers to a worker by name only and never
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
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/pblumer/atlas/connector/nettimeout"

	"github.com/pblumer/atlas/connector/clientreg"
)

// Event is one event a task appends to clio. IdempotencyKey is
// deterministic (the job key), so an at-least-once retry is de-duplicated by
// clio rather than appended twice.
type Event struct {
	Source         string // CloudEvents source (clio requires it); defaults to DefaultEventSource when empty
	Subject        string
	Type           string
	Data           map[string]any
	IdempotencyKey string
}

// InboundEvent is one event read back from clio (a read/query result, or an event
// the inbound bridge consumes, ADR-0075). ID is clio's event id — a per-partition
// monotonic sequence rendered as a decimal string (clio's `strconv.FormatUint`),
// which is both the resume cursor and, parsed as a uint64, the inbound bridge's
// dedup sequence. clio events carry no separate `seq` field. Partition is a
// server-derived view attribute set only on reads (0 for a single-partition clio,
// the recommended production configuration).
type InboundEvent struct {
	ID        string         `json:"id"`
	Subject   string         `json:"subject"`
	Type      string         `json:"type"`
	Data      map[string]any `json:"data"`
	Partition int            `json:"partition,omitempty"`
}

// ReadEventsRequest selects the events a read returns: a Subject, an optional
// exclusive AfterID cursor ("" reads from the start), whether to include the
// subject's subtree, an optional type filter, and a Limit (0 = the worker's
// default).
type ReadEventsRequest struct {
	Subject   string
	AfterID   string
	Recursive bool
	Types     []string
	Limit     int
}

// Client talks to one clio instance. It is an interface so the worker and the
// inbound bridge are testable without a live clio and so a worker name binds to
// exactly one endpoint. WriteEvent appends a domain event; GetState reads a
// projection; Query runs a stored query; ReadEvents reads a subject's events.
type Client interface {
	WriteEvent(ctx context.Context, e Event) error
	GetState(ctx context.Context, subject, reduceSpec string) (map[string]any, error)
	Query(ctx context.Context, subject, where string) (any, error)
	ReadEvents(ctx context.Context, req ReadEventsRequest) ([]InboundEvent, error)
}

// Registry resolves a worker name to the [Client] for this kind. Workers are
// registered at the server from managed configuration (endpoint plus credentials), so
// a model refers to a worker by name only (ADR-0036/0041).
//
// It is the shared [clientreg.Registry], which also carries *why* a configured
// worker is missing from it — the difference between "never configured" and
// "configured and broken", which is what a parked token has to be able to say
// (ADR-0158).
type Registry = clientreg.Registry[Client]

// NewRegistry creates an empty worker registry.
func NewRegistry() *Registry { return clientreg.New[Client]() }

// Connector is the server-side configuration of one clio worker: the base
// endpoint of the clio instance and an optional bearer token for it.
type Connector struct {
	Endpoint string
	Token    string
}

// DefaultEventSource is the CloudEvents `source` an outbound write carries when a
// task does not set one. clio rejects a write with an empty source, so this keeps
// a model that only names a subject and type working.
const DefaultEventSource = "atlas"

// KeyRequest describes a clio API key to mint (clio's POST /api/v1/keys). Scopes are
// clio scope strings — e.g. "read:/employees/*" grants read on the /employees
// subtree (the recursive grant a subtree watch needs), "read:/employees" only the
// exact subject. ExpiresAt is an optional RFC3339 timestamp ("" = no expiry).
type KeyRequest struct {
	Name      string
	Scopes    []string
	ExpiresAt string
}

// MintKey creates a new clio API key and returns its full, once-shown secret
// (clio's "kid.secret"). It is a standalone call — not a [Client] method — because
// it authenticates with a clio **admin** token, distinct from a worker's read
// token, and exists only to provision a worker's credential (ADR-0092) without an operator
// copy-pasting one. The caller must never persist adminToken; only the returned
// scoped key is stored (sealed in the vault). Delivery is not idempotent, so a
// caller should mint once per provisioning action.
func MintKey(ctx context.Context, endpoint, adminToken string, req KeyRequest) (string, error) {
	if strings.TrimSpace(adminToken) == "" {
		return "", fmt.Errorf("clio: mint key: admin token is required")
	}
	payload := map[string]any{"name": req.Name, "scopes": req.Scopes}
	if strings.TrimSpace(req.ExpiresAt) != "" {
		payload["expiresAt"] = req.ExpiresAt
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("clio: mint key: encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/api/v1/keys", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("clio: mint key: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+adminToken)
	resp, err := nettimeout.HTTPClient().Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("clio: mint key: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("clio: mint key returned HTTP %d", resp.StatusCode)
	}
	var out struct {
		Secret string `json:"secret"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("clio: mint key: decode response: %w", err)
	}
	if out.Secret == "" {
		return "", fmt.Errorf("clio: mint key: response carried no secret")
	}
	return out.Secret, nil
}

// HTTPClient talks to a real clio instance over its HTTP API (clio v1). Each
// method targets clio's documented route: write-events, read-events, run-query
// (all POST with a JSON body) and state (GET with the subject in the path). clio
// subjects are absolute ("/orders/42"); a leading slash is added if a task omits
// it. Reads stream NDJSON, one event JSON per line, oldest first.
type HTTPClient struct {
	conn Connector
	http *http.Client
}

// NewHTTPClient builds a clio HTTP client for a configured worker.
func NewHTTPClient(conn Connector) *HTTPClient {
	return &HTTPClient{conn: conn, http: nettimeout.HTTPClient()}
}

func (c *HTTPClient) WriteEvent(ctx context.Context, e Event) error {
	source := e.Source
	if source == "" {
		source = DefaultEventSource
	}
	// clio's write-events takes a batch of CloudEvents candidates; each needs a
	// source, an absolute subject, and a type (clio 400s otherwise). We send one.
	body, err := json.Marshal(map[string]any{
		"events": []map[string]any{{
			"source":  source,
			"subject": withLeadingSlash(e.Subject),
			"type":    e.Type,
			"data":    e.Data,
		}},
	})
	if err != nil {
		return fmt.Errorf("clio: encode event: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.conn.Endpoint+"/api/v1/write-events", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("clio: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// clio de-duplicates writes with optimistic-concurrency preconditions, not a
	// header, so the idempotency key rides along only as a hint clio may ignore.
	if e.IdempotencyKey != "" {
		req.Header.Set("Idempotency-Key", e.IdempotencyKey)
	}
	c.authorize(req)
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

// GetState reads a subject's folded state and returns its state object. clio's
// state route is GET {Endpoint}/api/v1/state/<subject> with the subject in the
// path; the effective reduce spec is chosen server-side by registered prefix
// (ADR-0041), so reduceSpec is accepted for interface symmetry but not sent. The
// response is a state envelope; we return its `state` object.
func (c *HTTPClient) GetState(ctx context.Context, subject, reduceSpec string) (map[string]any, error) {
	_ = reduceSpec // clio resolves the reduce spec by subject prefix, not per request
	var env struct {
		State map[string]any `json:"state"`
	}
	if err := c.getJSON(ctx, "/api/v1/state/"+escapeSubjectPath(subject), &env); err != nil {
		return nil, fmt.Errorf("clio: get-state for %q: %w", subject, err)
	}
	return env.State, nil
}

// Query runs a clio filter query over a subject and returns the matching events.
// Wire format: POST {Endpoint}/api/v1/run-query with {subject, where}, where
// `where` is clio's CEL predicate ("" = every event in the scope). The response
// streams NDJSON (one event per line); we collect the events into a slice so the
// result canonicalizes into a process variable like any other JSON array.
func (c *HTTPClient) Query(ctx context.Context, subject, where string) (any, error) {
	body, err := json.Marshal(map[string]any{
		"subject": withLeadingSlash(subject),
		"where":   where,
	})
	if err != nil {
		return nil, fmt.Errorf("clio: encode query: %w", err)
	}
	events, err := c.postNDJSON(ctx, "/api/v1/run-query", body)
	if err != nil {
		return nil, fmt.Errorf("clio: run-query for %q: %w", subject, err)
	}
	rows := make([]any, len(events))
	for i := range events {
		rows[i] = events[i]
	}
	return rows, nil
}

// ReadEvents reads a subject's events oldest-first. Wire format: POST
// {Endpoint}/api/v1/read-events with a JSON body {subject, recursive, lowerBound,
// types, limit}, returning NDJSON (one event JSON per line). clio's lowerBound is
// inclusive but AfterID is the last-consumed id, so the first line equal to
// AfterID is dropped, making AfterID an exclusive cursor. `recursive` includes the
// subject's whole subtree — the setting that lets a watch on /employees catch an
// event written to /employees/E-123456.
func (c *HTTPClient) ReadEvents(ctx context.Context, r ReadEventsRequest) ([]InboundEvent, error) {
	reqBody := map[string]any{
		"subject":   withLeadingSlash(r.Subject),
		"recursive": r.Recursive,
	}
	if r.AfterID != "" {
		reqBody["lowerBound"] = r.AfterID
	}
	if r.Limit > 0 {
		reqBody["limit"] = r.Limit
	}
	if len(r.Types) > 0 {
		reqBody["types"] = r.Types
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("clio: encode read request: %w", err)
	}
	rc, err := c.postStream(ctx, "/api/v1/read-events", body)
	if err != nil {
		return nil, fmt.Errorf("clio: read-events for %q: %w", r.Subject, err)
	}
	defer rc.Close()
	var out []InboundEvent
	err = scanNDJSON(rc, func(line []byte) error {
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.UseNumber()
		var e InboundEvent
		if err := dec.Decode(&e); err != nil {
			return fmt.Errorf("decode read-events line: %w", err)
		}
		if e.ID == r.AfterID { // exclusive cursor: drop the inclusive lowerBound boundary
			return nil
		}
		out = append(out, e)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("clio: read-events stream for %q: %w", r.Subject, err)
	}
	return out, nil
}

// authorize adds the worker's bearer token to a request when one is configured.
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

// postStream performs an authorized JSON POST and returns the response body for
// the caller to stream (NDJSON). The caller must close it. A non-2xx status is an
// error (the body is closed first).
func (c *HTTPClient) postStream(ctx context.Context, path string, body []byte) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.conn.Endpoint+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		resp.Body.Close()
		return nil, fmt.Errorf("returned HTTP %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// postNDJSON POSTs a JSON body and decodes the NDJSON response into a slice of
// generic event objects (exact decimals preserved via UseNumber).
func (c *HTTPClient) postNDJSON(ctx context.Context, path string, body []byte) ([]map[string]any, error) {
	rc, err := c.postStream(ctx, path, body)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var out []map[string]any
	err = scanNDJSON(rc, func(line []byte) error {
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.UseNumber()
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			return fmt.Errorf("decode line: %w", err)
		}
		out = append(out, m)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// scanNDJSON reads an NDJSON stream and invokes fn for each non-empty line. It
// grows its buffer to 8 MiB so a single large event line does not truncate.
func scanNDJSON(r io.Reader, fn func(line []byte) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if err := fn(line); err != nil {
			return err
		}
	}
	return sc.Err()
}

// withLeadingSlash returns an absolute clio subject: clio subjects begin with "/"
// (the root is "/"), so a task that names "orders/new" still targets "/orders/new".
func withLeadingSlash(s string) string {
	if s == "" {
		return "/"
	}
	if s[0] == '/' {
		return s
	}
	return "/" + s
}

// escapeSubjectPath renders an absolute subject as the path segment of clio's
// state route (GET /api/v1/state/<subject>): the leading slash is dropped (clio's
// {subject...} route re-adds it) and each segment is path-escaped while the slashes
// between segments are preserved.
func escapeSubjectPath(subject string) string {
	trimmed := strings.TrimPrefix(withLeadingSlash(subject), "/")
	if trimmed == "" {
		return ""
	}
	parts := strings.Split(trimmed, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}
