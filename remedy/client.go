// Package remedy integrates BMC Remedy (BMC Helix ITSM / the AR System) as a
// server-registered Atlas connector: a BPMN Remedy connector task creates an entry
// (e.g. an incident) in a model-authored Remedy form through the AR System REST API
// via the job path (ADR-0106), mirroring how the mail package delegates a send to a
// registry-managed provider (ADR-0079) and clio an append to a registry-managed
// endpoint (ADR-0036). The integration inherits the job protocol's durability and
// non-blocking properties (ADR-0007):
//
//   - A connector task creates a job carrying the reserved [compiler.RemedyJobType].
//     The processor never performs the outbound call itself, so it stays
//     allocation-free (invariant I1) and free of any HTTP dependency.
//   - The in-process [Handler] — a job worker — pulls those jobs, calls the AR System
//     REST API off the processor goroutine and after fsync (invariant I2, never inside
//     applyToState / I4), writes the created entry's id into the task's result
//     variable, and completes the job, which drives the token onward.
//   - The Remedy base URL and credentials live in a server-side [Registry] keyed by
//     connector name, so a model refers to a Remedy instance by name only and never
//     carries a URL or a secret (ADR-0036/0041). Only the form and its field values
//     are authored in the model, like a mail task's message (ADR-0079).
//
// The AR System REST flow is: obtain a JWT from POST /api/jwt/login (form-encoded
// username/password), then POST /api/arsys/v1/entry/{form} with an AR-JWT bearer
// header and a {"values": …} body; the created entry's id is read from the response's
// Location header, and the token is released with a best-effort POST /api/jwt/logout.
//
// Delivery is at-least-once (a crash between "Remedy created the entry" and "job
// completed" replays the create). The AR System has no idempotency-key header, so a
// replay can create a duplicate entry; the job key rides along as an X-Request-ID for
// a downstream de-duplicator, and a Remedy-side dedup field is a follow-up (ADR-0106).
package remedy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
)

// Entry is one Remedy entry a connector task creates. Form is the Remedy form the
// entry is created in (e.g. "HPD:IncidentInterface_Create"); Values are the entry's
// field values, keyed by Remedy field name. RequestID is deterministic (the job
// key), sent as an X-Request-ID header so an at-least-once retry can be recognized by
// a downstream de-duplicator.
type Entry struct {
	Form      string
	Values    map[string]any
	RequestID string
}

// Result is a create-entry call's outcome: the id (request id) of the created entry,
// read from the AR System response's Location header.
type Result struct {
	EntryID string
}

// Client creates an entry in a Remedy instance. It is an interface so the worker is
// testable without a live AR System and so a connector name binds to exactly one
// Remedy instance.
type Client interface {
	CreateEntry(ctx context.Context, e Entry) (Result, error)
}

// Registry resolves a connector name to the [Client] for its Remedy instance.
// Connectors are registered at the server from managed configuration (base URL plus
// credentials), so a model refers to a connector by name only (ADR-0036/0041). A
// Registry is read-only once populated and safe for concurrent use by workers.
type Registry struct {
	clients map[string]Client
}

// NewRegistry creates an empty connector registry.
func NewRegistry() *Registry { return &Registry{clients: map[string]Client{}} }

// Register binds a connector name to its client. Registering the same name again
// replaces the earlier binding (last write wins), so reconfiguration is simple.
func (r *Registry) Register(name string, c Client) { r.clients[name] = c }

// Client returns the client bound to name, or nil and false if none is registered.
func (r *Registry) Client(name string) (Client, bool) {
	c, ok := r.clients[name]
	return c, ok
}

// Replace swaps the whole set of registered connectors at once, so a server can
// rebuild the registry from managed configuration after a change (ADR-0041). The
// caller must serialize Replace with the workers that read the registry — the Atlas
// server does both on its run-loop goroutine — so no lock is needed. A nil map
// clears the registry.
func (r *Registry) Replace(clients map[string]Client) {
	if clients == nil {
		clients = map[string]Client{}
	}
	r.clients = clients
}

// Connector is the server-side configuration of one Remedy instance: the AR System
// REST BaseURL (e.g. "https://helix.example.com:8008"), and the Username and Password
// used to obtain a JWT. The Password is the resolved secret, held only at call time,
// never persisted (I6).
type Connector struct {
	BaseURL  string
	Username string
	Password string
}

// HTTPClient calls a real Remedy AR System over its REST API. It authenticates per
// call (login → create → logout), which keeps the client stateless and safe for
// concurrent use; token caching is a follow-up (ADR-0106).
type HTTPClient struct {
	conn Connector
	http *http.Client
}

// NewHTTPClient builds a Remedy REST client for a configured connector, backed by
// http.DefaultClient. A configurable timeout is a follow-up (ADR-0106).
func NewHTTPClient(conn Connector) *HTTPClient {
	return &HTTPClient{conn: conn, http: http.DefaultClient}
}

// CreateEntry obtains a JWT, POSTs the entry to its form, reads the created entry's
// id from the Location header, and releases the token (best effort). A login or
// create failure (transport error or non-2xx status) is returned so the job stays
// pending and is retried (at-least-once).
func (c *HTTPClient) CreateEntry(ctx context.Context, e Entry) (Result, error) {
	token, err := c.login(ctx)
	if err != nil {
		return Result{}, err
	}
	defer c.logout(ctx, token) // release the JWT; failure here must not fail the job

	raw, err := json.Marshal(struct {
		Values map[string]any `json:"values"`
	}{Values: e.Values})
	if err != nil {
		return Result{}, fmt.Errorf("remedy: encode entry values: %w", err)
	}
	endpoint := c.url("/api/arsys/v1/entry/" + e.Form)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if err != nil {
		return Result{}, fmt.Errorf("remedy: build create request: %w", err)
	}
	req.Header.Set("Authorization", "AR-JWT "+token)
	req.Header.Set("Content-Type", "application/json")
	if e.RequestID != "" {
		req.Header.Set("X-Request-ID", e.RequestID)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("remedy: create entry in %s: %w", e.Form, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return Result{}, fmt.Errorf("remedy: create entry in %s returned HTTP %d: %s", e.Form, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return Result{EntryID: entryIDFromLocation(resp.Header.Get("Location"))}, nil
}

// login exchanges the connector's username/password for a JWT via the AR System JWT
// login endpoint. The token is the plain-text response body. An empty token (a 2xx
// with no body — a misconfigured server) is an error so the job retries rather than
// calling create unauthenticated.
func (c *HTTPClient) login(ctx context.Context) (string, error) {
	form := url.Values{"username": {c.conn.Username}, "password": {c.conn.Password}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/api/jwt/login"), strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("remedy: build login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("remedy: login to %s: %w", c.conn.BaseURL, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("remedy: login to %s returned HTTP %d: %s", c.conn.BaseURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	token := strings.TrimSpace(string(body))
	if token == "" {
		return "", fmt.Errorf("remedy: login to %s returned an empty token", c.conn.BaseURL)
	}
	return token, nil
}

// logout releases a JWT. It is best effort — a failure to release a token must never
// fail (or duplicate) the job — so its error is intentionally ignored.
func (c *HTTPClient) logout(ctx context.Context, token string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url("/api/jwt/logout"), nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "AR-JWT "+token)
	if resp, err := c.http.Do(req); err == nil {
		resp.Body.Close()
	}
}

// url joins the connector's base URL with an API path, tolerating a trailing slash on
// the base.
func (c *HTTPClient) url(p string) string {
	return strings.TrimRight(c.conn.BaseURL, "/") + p
}

// entryIDFromLocation extracts the created entry's id from an AR System Location
// header (…/entry/{form}/{id}) — the last path segment, URL-decoded. An empty or
// unparseable Location yields "" (the create still succeeded; only the id is absent).
func entryIDFromLocation(loc string) string {
	loc = strings.TrimSpace(loc)
	if loc == "" {
		return ""
	}
	if u, err := url.Parse(loc); err == nil && u.Path != "" {
		return path.Base(u.Path)
	}
	return path.Base(loc)
}
