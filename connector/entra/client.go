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
// or 21Vianet in China — is a different host, which is why a worker can override
// it rather than having the address baked in.
const DefaultBaseURL = "https://graph.microsoft.com/v1.0"

// DefaultScope requests the app registration's configured application permissions.
const DefaultScope = "https://graph.microsoft.com/.default"

// Client performs one Graph request against a configured tenant. It is an interface
// so the worker is testable without a live directory, and so a worker name binds
// to exactly one tenant.
//
// The shape is a single Call rather than a method per operation because this
// worker is a typed façade over Graph REST: the value it adds is at the *model*
// level — naming the lifecycle operations and building their URLs and bodies — not
// in wrapping nine HTTP calls in nine Go signatures.
type Client interface {
	// Call performs one request described by a [Request].
	Call(ctx context.Context, req Request) (any, error)
	// BaseURL is the tenant's Graph root. Adding a group member is the one operation
	// whose *body* carries a URL — the @odata.id of the member being added — so the
	// caller has to know which cloud this worker talks to, not just its path.
	BaseURL() string
}

// Registry resolves a worker name to the tenant behind it. It is the worker's own
// map: the engine never holds one for this kind (ADR-0172).
type Registry = clientreg.Registry[Client]

// NewRegistry creates an empty registry.
func NewRegistry() *Registry { return clientreg.New[Client]() }

// Request is one Graph call. It is a struct rather than a parameter list because
// the fourth thing a call needs — whether it asks for advanced query support — is a
// property of the *query*, and a bare bool at the end of a signature says nothing at
// the call site about which one it is.
type Request struct {
	Method string
	// Path is normally a path under [Client.BaseURL], but a paged listing passes back
	// the absolute @odata.nextLink Graph handed it — verbatim, because a continuation
	// token is not something to take apart and reassemble. An implementation must
	// confine such a URL to its own endpoint (see [GraphClient.Call]).
	Path string
	Body any
	// Eventual asks for Graph's advanced query support by sending
	// ConsistencyLevel: eventual. It is what makes endsWith, ne, not and $search
	// usable on a directory collection — and it must be set on *every* request of a
	// listing, continuations included, because Graph rejects a page fetched without
	// it. The matching $count=true belongs in Path; Graph requires the two together.
	//
	// It is deliberately not derived from Path carrying $count=true. Graph does pair
	// them, so deriving it would work today — but a behavioural header inferred by
	// sniffing a URL is the kind of coupling that breaks quietly, and a fake client
	// in a test could then only observe the string rather than the intent.
	Eventual bool

	// Binary asks for the response *bytes* rather than a decoded object, and it is
	// how a photo is read (ADR-draft-directory-photo). Graph serves one as
	// image/jpeg, and every other operation this worker performs returns JSON.
	//
	// A field on the request rather than a second method on [Client], because what
	// differs is a property of this request and not of the client — and a method
	// would have broken every fake standing in for Graph in every test, for a
	// distinction none of them make.
	//
	// It changes two things beyond the decoding. The result is a [Binary] rather
	// than a decoded object; and **404 is an answer rather than a failure**: it
	// returns (nil, nil), meaning there is nothing there. Graph answers 404 both for
	// a person who has no photo and for an id that is not anybody's, and the error
	// code distinguishing them is not something to hang a directory run on. The
	// trade is stated rather than hidden: a mistyped id reads as "no photo", where
	// the other way round every person without one would fail a job — and in a
	// tenant where most have none, that is an incident queue nobody can read.
	Binary bool

	// MaxBytes caps a binary read. Zero means [DefaultMaxBinaryBytes].
	//
	// It refuses rather than truncates. An unbounded body into a process variable is
	// the failure the listing cap exists for, and half a JPEG is not a smaller JPEG:
	// the magic is at the front, so a truncated one passes every format check there
	// is and lands as a broken image nobody can explain.
	MaxBytes int64
}

// Binary is what a [Request] with Binary set returns: the bytes, and what Graph
// said they are. It is a type rather than a bare []byte because the content type
// is half the answer — nothing downstream can store an image it cannot name.
type Binary struct {
	ContentType string
	Data        []byte
}

// DefaultMaxBinaryBytes bounds a binary read when the request names no cap.
//
// A megabyte is far above any photo Graph serves from /photo/$value — those run to
// tens of kilobytes — so reaching it means something is wrong, and saying so is the
// useful outcome.
const DefaultMaxBinaryBytes = 1 << 20

// consistencyLevelHeader is the header Graph reads for advanced query support, and
// eventualConsistency its only value this worker sends.
const (
	consistencyLevelHeader = "ConsistencyLevel"
	eventualConsistency    = "eventual"
)

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
func (c *GraphClient) Call(ctx context.Context, r Request) (any, error) {
	target, err := c.resolve(r.Path)
	if err != nil {
		return nil, err
	}
	var rdr io.Reader
	if r.Body != nil {
		raw, err := json.Marshal(r.Body)
		if err != nil {
			return nil, fmt.Errorf("entra: encode request body: %w", err)
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, r.Method, target, rdr)
	if err != nil {
		return nil, fmt.Errorf("entra: build %s %s: %w", r.Method, r.Path, err)
	}
	tok, err := c.tokens.Token(ctx)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	if r.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.Eventual {
		req.Header.Set(consistencyLevelHeader, eventualConsistency)
	}
	if r.Binary {
		// Ask for anything: the JSON Accept above is a lie for a photo, and Graph
		// answers an error envelope as JSON regardless.
		req.Header.Set("Accept", "*/*")
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("entra: %s %s: %w", r.Method, r.Path, err)
	}
	defer resp.Body.Close()
	if r.Binary {
		return c.readBinary(r, resp)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("entra: read %s %s response: %w", r.Method, r.Path, err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, graphFailure(r.Method, r.Path, resp.StatusCode, raw)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("entra: %s %s returned a body that is not JSON: %w", r.Method, r.Path, err)
	}
	return out, nil
}

// readBinary finishes a binary request: the bytes, the type Graph named them, and
// the two answers that are not bytes.
//
// 404 is absence, not failure — see [Request.Binary] for why that way round. Every
// other non-2xx is a failure and carries Graph's own error envelope, which is
// JSON even here.
func (c *GraphClient) readBinary(r Request, resp *http.Response) (any, error) {
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	max := r.MaxBytes
	if max <= 0 {
		max = DefaultMaxBinaryBytes
	}
	// One byte past the cap, so an over-large body is refused rather than truncated
	// into a smaller one that still passes every format check.
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, fmt.Errorf("entra: read %s %s response: %w", r.Method, r.Path, err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, graphFailure(r.Method, r.Path, resp.StatusCode, data)
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("entra: %s %s returned more than the %d byte limit; it is refused rather than cut short, because a truncated image passes every format check and is still broken",
			r.Method, r.Path, max)
	}
	if len(data) == 0 {
		// A 2xx with no body is the same statement as a 404 here: there is nothing to
		// carry. Reporting it as an empty image would put a zero-byte file on an
		// account.
		return nil, nil
	}
	ct := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	return Binary{ContentType: strings.ToLower(strings.TrimSpace(ct)), Data: data}, nil
}

// resolve turns what a caller asked for into the URL to request: a path is taken
// under this worker's own base, and an absolute URL — which is what a paged
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
		return "", fmt.Errorf("entra: cannot follow the paged result: this worker's own base URL %q is not a URL", c.baseURL)
	}
	if u.Scheme != base.Scheme || u.Host != base.Host {
		return "", fmt.Errorf("entra: refusing to follow a paged result to %s://%s: a continuation may only stay on this worker's own endpoint (%s://%s)",
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
