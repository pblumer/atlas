package mcp

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a thin HTTP client for the Atlas server API (see the api package).
// The MCP server owns no engine state of its own; every tool call is translated
// into an HTTP request against a running Atlas server, which remains the single
// writer of its partition (invariant I3). That keeps the MCP surface a pure
// adapter — it can never violate an engine invariant because it never touches
// the engine directly.
type Client struct {
	baseURL string
	token   string // optional bearer token attached to every request
	caller  caller // the credential of whoever made this request, when there is one
	http    *http.Client
}

// caller is the credential an MCP request arrived with, forwarded to the Atlas
// API so a tool call acts as whoever made it rather than as the adapter.
//
// It is carried verbatim, by header name, and never parsed. The adapter then does
// not need to know what an Atlas session cookie is called or which bearer schemes
// the server accepts — and cannot get either wrong as those change. Whatever
// authenticated the caller at /mcp is exactly what authenticates the calls their
// tool makes.
type caller struct {
	authorization string // the Authorization header, as received
	cookie        string // the Cookie header, as received
	via           string // the TransportHeader marker, as received
}

// empty reports whether the caller presented no credential at all — the ordinary
// case on a server running without --auth.
func (c caller) empty() bool { return c.authorization == "" && c.cookie == "" && c.via == "" }

// TransportHeader marks an API request as one a tool call made, rather than one a
// client made directly against /api/v1 with the same credential.
//
// Atlas stamps it on every request entering /mcp and this adapter forwards it, the
// same way it forwards the caller's own credential and for the same reason: what
// arrives at the API has to be recognisable as what it is. A token a person
// approved for the transport alone is otherwise confined away from the very API
// calls its tools are made of.
//
// It is not a credential this adapter holds — it holds none (ADR-0196). It arrives
// on the request, is carried verbatim like the other two, and means nothing except
// to the server that wrote it.
const TransportHeader = "X-Atlas-Via-MCP"

// forCaller returns a Client that authenticates as the given caller instead of
// with the token this one was built with. It shallow-copies, so the derived
// client shares the underlying http.Client and its connection pool: one struct
// copy per request, no new transport.
//
// A caller with no credential yields the receiver unchanged, which is what makes
// this safe to call unconditionally on an unauthenticated server.
func (c *Client) forCaller(cl caller) *Client {
	if cl.empty() {
		return c
	}
	dup := *c
	dup.caller = cl
	return &dup
}

// ClientOption configures a Client at construction.
type ClientOption func(*Client)

// WithBearer attaches an Authorization: Bearer <token> header to every request
// that carries no caller credential of its own. That is the stdio adapter's case:
// it is a per-agent process with one identity for its whole life, given on the
// command line (atlas mcp --token).
//
// The HTTP transport does not use it. There, each request brings its own caller
// and forCaller takes precedence — see ADR-0196 for
// why the adapter no longer holds a credential of its own on that path.
//
// An empty token is a no-op, so callers can pass it unconditionally.
func WithBearer(token string) ClientOption {
	return func(c *Client) { c.token = token }
}

// WithTLSRoots verifies the server's certificate against pool in addition to the
// host's roots, for the stdio adapter pointed at an https:// Atlas whose
// certificate an internal CA issued (atlas mcp --tls-ca).
//
// It is a trust anchor and not a way around verification: there is no
// skip-verify switch here, in api/targetstore.go, or anywhere else in Atlas
// (ADR-0191). A nil pool leaves the client exactly as it was — verifying against
// the host's roots — so callers can pass it unconditionally.
func WithTLSRoots(pool *x509.CertPool) ClientOption {
	return func(c *Client) {
		if pool == nil {
			return
		}
		c.http = &http.Client{
			Timeout:   c.http.Timeout,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
		}
	}
}

// NewClient builds a Client for the Atlas server at baseURL (e.g.
// "http://localhost:8080"). A trailing slash is tolerated.
func NewClient(baseURL string, opts ...ClientOption) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// apiError carries the {"error": "..."} body Atlas returns on a 4xx/5xx so tool
// handlers can surface the server's own message rather than a bare status code.
type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("atlas server returned status %d", e.Status)
	}
	return fmt.Sprintf("atlas server error (%d): %s", e.Status, e.Message)
}

type clientResponse struct {
	body   []byte
	header http.Header
}

// get issues a GET and returns the raw response body on 2xx, or an *apiError.
func (c *Client) get(path string) ([]byte, error) {
	return c.do(http.MethodGet, path, "", nil)
}

// getWithHeaders issues a GET and also returns a copy of the response headers.
// Listing tools use it to preserve API pagination metadata in their MCP result.
func (c *Client) getWithHeaders(path string) ([]byte, http.Header, error) {
	resp, err := c.doResponse(http.MethodGet, path, "", nil)
	if err != nil {
		return nil, nil, err
	}
	return resp.body, resp.header, nil
}

// post issues a POST with the given content type and body.
func (c *Client) post(path, contentType string, body []byte) ([]byte, error) {
	return c.do(http.MethodPost, path, contentType, body)
}

// put issues a PUT with the given content type and body — a whole-document
// replace, which is how the information model is written.
func (c *Client) put(path, contentType string, body []byte) ([]byte, error) {
	return c.do(http.MethodPut, path, contentType, body)
}

// del issues a DELETE and returns the raw response body on 2xx, or an *apiError.
// A 204 No Content yields an empty body, which handlers turn into a confirmation.
func (c *Client) del(path string) ([]byte, error) {
	return c.do(http.MethodDelete, path, "", nil)
}

func (c *Client) do(method, path, contentType string, body []byte) ([]byte, error) {
	resp, err := c.doResponse(method, path, contentType, body)
	if err != nil {
		return nil, err
	}
	return resp.body, nil
}

func (c *Client) doResponse(method, path, contentType string, body []byte) (clientResponse, error) {
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
	}
	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		return clientResponse{}, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	// The caller's credential wins over the adapter's own. An adapter that lent its
	// token to a request that arrived with a weaker one would re-open, one level
	// down, exactly the privilege the transport used to hand out for free.
	switch {
	case c.caller.authorization != "":
		req.Header.Set("Authorization", c.caller.authorization)
	case c.token != "":
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.caller.cookie != "" {
		req.Header.Set("Cookie", c.caller.cookie)
	}
	if c.caller.via != "" {
		req.Header.Set(TransportHeader, c.caller.via)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return clientResponse{}, fmt.Errorf("reach atlas server at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return clientResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return clientResponse{}, &apiError{Status: resp.StatusCode, Message: extractError(data)}
	}
	return clientResponse{body: data, header: resp.Header.Clone()}, nil
}

// extractError pulls the "error" field out of an Atlas JSON error body, falling
// back to the raw (trimmed) body when it isn't the expected shape.
func extractError(body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && e.Error != "" {
		return e.Error
	}
	return strings.TrimSpace(string(body))
}
