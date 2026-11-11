package atlasreport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/pblumer/atlas/connector/nettimeout"
)

// Bundle is what a Worker's credential holds: an API token of a technical principal
// (ADR-0194) whose roles admit the read routes the operations call — operator for the
// instance, statistics and incident summaries, and the product manager's role for the
// delivery report. A token of the `worker` scope a supervised worker runs with is not
// one: that scope reaches the job protocol and nothing else.
type Bundle struct {
	Token string `json:"token"`
}

// maxAnswer bounds what one read route's answer may weigh before it is decoded. The
// summaries are small; a statistics answer lists every element of a model, which a
// large model makes a few hundred kilobytes.
const maxAnswer = 8 << 20

// CheckBundle reads an endpoint and a credential bundle as the worker will, so the
// server's supervision and the worker cannot disagree about what they say.
func CheckBundle(endpoint, bundle string) error {
	_, err := NewClient(endpoint, bundle)
	return err
}

// Client reads Atlas's read routes with one token.
type Client struct {
	base  *url.URL
	token string
	http  *http.Client
}

// NewClient builds a client from a Worker's endpoint — the Atlas server's base URL, such
// as https://atlas.example.ch — and its credential bundle.
func NewClient(endpoint, bundle string) (*Client, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, fmt.Errorf("atlasreport: no endpoint; set the Atlas server's address, e.g. https://atlas.example.ch")
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("atlasreport: the endpoint %q is not an http(s) address of an Atlas server", endpoint)
	}
	if u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return nil, fmt.Errorf("atlasreport: the endpoint %q carries a query, a fragment or a user; give the server's address alone", endpoint)
	}
	// The credential is a bundle {"token": "…"}, or the bare token: what the API-token
	// page seals into the vault when it is told a vaultSecret, so the token never has to
	// cross a clipboard (ADR-0194, amendment 2026-10-21).
	var b Bundle
	if raw := strings.TrimSpace(bundle); strings.HasPrefix(raw, "{") {
		if err := json.Unmarshal([]byte(raw), &b); err != nil {
			return nil, fmt.Errorf("atlasreport: the credential is not a bundle of the form {\"token\": \"…\"}")
		}
	} else if strings.ContainsAny(raw, " \t\r\n") {
		return nil, fmt.Errorf("atlasreport: the credential is neither a bundle {\"token\": \"…\"} nor a token alone")
	} else {
		b.Token = raw
	}
	if strings.TrimSpace(b.Token) == "" {
		return nil, fmt.Errorf("atlasreport: the credential bundle has no token; create an API token for a technical account with the operator role")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	return &Client{base: u, token: strings.TrimSpace(b.Token), http: nettimeout.HTTPClient()}, nil
}

// get reads one route and decodes its answer into out.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	u := *c.base
	u.Path = c.base.Path + path
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return &callError{kind: kindUnreachable, err: fmt.Errorf("atlasreport: %s: %w", path, err)}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer+1))
	if err != nil {
		return &callError{kind: kindUnreachable, err: fmt.Errorf("atlasreport: %s: %w", path, err)}
	}
	if len(body) > maxAnswer {
		return fmt.Errorf("atlasreport: %s answered more than %d bytes", path, maxAnswer)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return &callError{kind: kindRefused, err: fmt.Errorf("atlasreport: %s was refused (%d): the Worker's token lacks the role this report reads with (operator, and the product manager's role for the delivery report)", path, resp.StatusCode)}
	case resp.StatusCode == http.StatusNotFound:
		return &callError{kind: kindNotFound, err: fmt.Errorf("atlasreport: %s answered 404 (%s)", path, clip(string(body)))}
	case resp.StatusCode/100 != 2:
		return &callError{kind: kindUnreachable, err: fmt.Errorf("atlasreport: %s answered %d (%s)", path, resp.StatusCode, clip(string(body)))}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("atlasreport: %s answered something that is not the expected JSON: %w", path, err)
	}
	return nil
}

func clip(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// Registry is the Atlas servers a worker reads, by Worker name.
type Registry struct {
	clients map[string]*Client
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{clients: map[string]*Client{}} }

// Register adds a client under a Worker's name.
func (r *Registry) Register(name string, c *Client) { r.clients[name] = c }

// Get returns the client of that Worker.
func (r *Registry) Get(name string) (*Client, bool) {
	if r == nil {
		return nil, false
	}
	c, ok := r.clients[name]
	return c, ok
}
