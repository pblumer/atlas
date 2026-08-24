package entra

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/pblumer/atlas/connector/clientreg"
	"github.com/pblumer/atlas/connector/nettimeout"
	"github.com/pblumer/atlas/connector/oauth2"
)

// DefaultBaseURL is the worldwide Graph endpoint. A national cloud — US Government,
// or 21Vianet in China — is a different host, which is why a connector can override
// it rather than having the address baked in.
const DefaultBaseURL = "https://graph.microsoft.com/v1.0"

// DefaultScope requests the app registration's configured application permissions.
const DefaultScope = "https://graph.microsoft.com/.default"

// Client performs one Graph request against a configured tenant. It is an interface
// so the worker is testable without a live directory, and so a connector name binds
// to exactly one tenant.
//
// The shape is a single Call rather than a method per operation because this
// connector is a typed façade over Graph REST: the value it adds is at the *model*
// level — naming the lifecycle operations and building their URLs and bodies — not
// in wrapping nine HTTP calls in nine Go signatures.
type Client interface {
	// Call performs one request. path is normally a path under [Client.BaseURL], but
	// a paged listing passes back the absolute @odata.nextLink Graph handed it —
	// verbatim, because a continuation token is not something to take apart and
	// reassemble. An implementation must confine such a URL to its own endpoint
	// (see [GraphClient.Call]).
	Call(ctx context.Context, method, path string, body any) (any, error)
	// BaseURL is the tenant's Graph root. Adding a group member is the one operation
	// whose *body* carries a URL — the @odata.id of the member being added — so the
	// caller has to know which cloud this connector talks to, not just its path.
	BaseURL() string
}

// Registry resolves a connector name to the tenant behind it. It is the worker's own
// map: the engine never holds one for this kind (ADR-0172).
type Registry = clientreg.Registry[Client]

// NewRegistry creates an empty registry.
func NewRegistry() *Registry { return clientreg.New[Client]() }

// GraphClient calls Microsoft Graph with an OAuth2 bearer token.
type GraphClient struct {
	tokens  oauth2.TokenSource
	baseURL string
	httpc   *http.Client
}

// NewGraphClient builds a client for one tenant. An empty baseURL uses
// [DefaultBaseURL]; httpc defaults to the shared bounded client (ADR-0149), so a
// hung Graph endpoint fails the job into a visible incident instead of parking a
// token in silence.
func NewGraphClient(tokens oauth2.TokenSource, baseURL string, httpc *http.Client) *GraphClient {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	if httpc == nil {
		httpc = nettimeout.HTTPClient()
	}
	return &GraphClient{tokens: tokens, baseURL: strings.TrimRight(baseURL, "/"), httpc: httpc}
}

// BaseURL returns the Graph root this client calls.
func (c *GraphClient) BaseURL() string { return c.baseURL }

// graphError is the error envelope Graph returns. Surfacing its code and message is
// the difference between "HTTP 400" and "Request_BadRequest: Property netId is
// invalid", which is the whole of what an operator needs from a failed provisioning
// step.
type graphError struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Call performs one Graph request and decodes the response.
//
// A 2xx with no body — which is what Graph answers a DELETE or a successful PATCH
// with — returns a nil result rather than an error: the operation succeeded, and
// there is simply nothing to write into a result variable.
func (c *GraphClient) Call(ctx context.Context, method, path string, body any) (any, error) {
	target, err := c.resolve(path)
	if err != nil {
		return nil, err
	}
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("entra: encode request body: %w", err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rdr)
	if err != nil {
		return nil, fmt.Errorf("entra: build %s %s: %w", method, path, err)
	}
	tok, err := c.tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("entra: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("entra: read %s %s response: %w", method, path, err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, graphFailure(method, path, resp.StatusCode, raw)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("entra: %s %s returned a body that is not JSON: %w", method, path, err)
	}
	return out, nil
}

// resolve turns what a caller asked for into the URL to request: a path is taken
// under this connector's own base, and an absolute URL — which is what a paged
// listing passes back from @odata.nextLink — only if it stays on that same endpoint.
//
// The confinement is the point. This client carries a bearer that can read, create
// and disable accounts across an entire directory, and a continuation is the one
// place where a *response* decides the next URL. Following one to another host would
// hand that token to whoever wrote the response, so a foreign continuation is
// refused rather than followed and reported.
func (c *GraphClient) resolve(path string) (string, error) {
	if !strings.HasPrefix(path, "http://") && !strings.HasPrefix(path, "https://") {
		return c.baseURL + path, nil
	}
	u, err := url.Parse(path)
	if err != nil {
		return "", fmt.Errorf("entra: cannot follow the paged result: %q is not a URL", path)
	}
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("entra: cannot follow the paged result: this connector's own base URL %q is not a URL", c.baseURL)
	}
	if u.Scheme != base.Scheme || u.Host != base.Host {
		return "", fmt.Errorf("entra: refusing to follow a paged result to %s://%s: a continuation may only stay on this connector's own endpoint (%s://%s)",
			u.Scheme, u.Host, base.Scheme, base.Host)
	}
	return path, nil
}

// graphFailure turns a non-2xx response into the most specific error the body allows.
func graphFailure(method, path string, status int, raw []byte) error {
	var ge graphError
	if err := json.Unmarshal(raw, &ge); err == nil && ge.Error.Code != "" {
		return fmt.Errorf("entra: %s %s returned HTTP %d: %s: %s", method, path, status, ge.Error.Code, ge.Error.Message)
	}
	return fmt.Errorf("entra: %s %s returned HTTP %d: %s", method, path, status, strings.TrimSpace(string(raw)))
}
