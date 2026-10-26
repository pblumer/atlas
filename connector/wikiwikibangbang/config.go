package wikiwikibangbang

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/clientreg"
	"github.com/pblumer/atlas/connector/nettimeout"
	"github.com/pblumer/atlas/limits"
)

// Bundle is what a wikiwikibangbang Worker's vault entry holds: the API token of the
// wiki account the Worker writes as.
//
//	{"token": "wwbb_…"}
//
// The account is a technical one of its own — not a person's, and not the read-only
// account the catalogue reads the same wiki with (ADR-0491), whose standing that record
// checks before every read (ADR-0498).
type Bundle struct {
	Token string `json:"token"`
}

// ParseBundle reads a bundle. An unknown field is refused, so a bundle meant for another
// Worker Type is not taken for this one; no message quotes a fragment of the token.
func ParseBundle(raw string) (Bundle, error) {
	if strings.TrimSpace(raw) == "" {
		return Bundle{}, fmt.Errorf("wikiwikibangbang: the credential bundle is empty")
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	var b Bundle
	if err := dec.Decode(&b); err != nil {
		var syn *json.SyntaxError
		switch {
		case errors.As(err, &syn):
			return Bundle{}, fmt.Errorf("wikiwikibangbang: the credential bundle is not a JSON object (syntax error at byte %d)", syn.Offset)
		case strings.Contains(err.Error(), "unknown field"):
			// The decoder's message names the field and nothing else.
			return Bundle{}, fmt.Errorf("wikiwikibangbang: the credential bundle has an %s (want token)", strings.TrimPrefix(err.Error(), "json: "))
		case errors.Is(err, io.ErrUnexpectedEOF):
			return Bundle{}, fmt.Errorf("wikiwikibangbang: the credential bundle is not a complete JSON object")
		}
		return Bundle{}, fmt.Errorf("wikiwikibangbang: the credential bundle is not a JSON object of the expected shape")
	}
	if dec.More() {
		return Bundle{}, fmt.Errorf("wikiwikibangbang: the credential bundle holds more than one JSON value")
	}
	if strings.TrimSpace(b.Token) == "" {
		return Bundle{}, fmt.Errorf("wikiwikibangbang: the bundle needs a token: an API token of the wiki account this Worker writes as")
	}
	b.Token = strings.TrimSpace(b.Token)
	return b, nil
}

// Options are the parts of a client that belong to the host or the test rather than to
// the Worker.
type Options struct {
	// HTTPClient reaches the wiki; nil is the shared bounded one. It is copied, not
	// changed: the client decides itself which redirect it follows.
	HTTPClient *http.Client
	// Now decides when the account's standing is read again; nil is time.Now.
	Now func() time.Time
	// MaxAnswer bounds one answer in bytes; zero is the installation's payload budget
	// (limits.Payload).
	MaxAnswer int64
}

// NewClient builds the client a Worker's endpoint and bundle describe.
//
// The endpoint is the wiki's base URL — where its web UI is — and must be https, since
// the token goes with every request; http is accepted for a loopback host, where there
// is no network to cross. A path is kept, for a wiki behind a reverse proxy under one.
//
// Nothing here reaches the network: the account's standing is read on the first call,
// so a Worker that builds is one whose configuration is complete, not one known to work.
func NewClient(endpoint, raw string, opts Options) (*Client, error) {
	b, err := ParseBundle(raw)
	if err != nil {
		return nil, err
	}
	u, err := parseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	base := opts.HTTPClient
	if base == nil {
		base = nettimeout.HTTPClient()
	}
	httpc := *base
	httpc.CheckRedirect = sameHostReads(u)
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	maxAnswer := opts.MaxAnswer
	if maxAnswer <= 0 {
		maxAnswer = limits.Default().Payload
	}
	return &Client{base: u, token: b.Token, http: &httpc, now: now, maxAnswer: maxAnswer}, nil
}

// sameHostReads is the redirect rule: a read is followed to the same host, over https
// unless the endpoint itself is a loopback http one, at most five hops; a write is never
// followed, because net/http turns a redirected POST into a GET and a write that arrives
// as a read has not been made. Another host is refused outright — the token is for this
// wiki — rather than left to net/http's dropping of the header.
func sameHostReads(base *url.URL) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 && via[0].Method != http.MethodGet {
			return fmt.Errorf("the wiki redirected a write (%s); a write is not followed", via[0].Method)
		}
		if !strings.EqualFold(req.URL.Host, base.Host) || (req.URL.Scheme != "https" && req.URL.Scheme != base.Scheme) {
			return fmt.Errorf("the wiki redirected to %s, which is not %s", req.URL.Host, base.Host)
		}
		if len(via) >= 5 {
			return errors.New("the wiki redirected too often")
		}
		return nil
	}
}

// parseEndpoint checks a Worker's endpoint.
func parseEndpoint(endpoint string) (*url.URL, error) {
	raw := strings.TrimSpace(endpoint)
	if raw == "" {
		return nil, fmt.Errorf("wikiwikibangbang: the Worker needs an endpoint: the wiki's base URL, e.g. https://wiki.example.ch")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("wikiwikibangbang: the Worker's endpoint %q is not an https URL", clip(raw))
	}
	if u.User != nil {
		return nil, fmt.Errorf("wikiwikibangbang: the Worker's endpoint carries a user name or password; put the token in the credential bundle instead")
	}
	if u.Scheme == "http" && !loopback(u.Hostname()) {
		return nil, fmt.Errorf("wikiwikibangbang: the Worker's endpoint must be https: the token is sent with every request, and over http it would cross the network in the clear")
	}
	u.RawQuery, u.Fragment = "", ""
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u, nil
}

// loopback reports whether a host is this machine.
func loopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// CheckBundle reports whether a Worker's endpoint and bundle build a client at all —
// the question supervision asks before it hands a bundle to a worker, answered by the
// same code the worker then builds with.
func CheckBundle(endpoint, raw string) error {
	_, err := NewClient(endpoint, raw, Options{})
	return err
}

// Registry resolves a Worker name to the wiki behind it. It is the worker's own map:
// the engine never builds one for this kind.
type Registry = clientreg.Registry[*Client]

// NewRegistry creates an empty registry.
func NewRegistry() *Registry { return clientreg.New[*Client]() }

// clip shortens a value quoted in an error message.
func clip(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}
