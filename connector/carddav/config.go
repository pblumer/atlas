package carddav

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/carddav/catalog"
	"github.com/pblumer/atlas/connector/nettimeout"
)

// Bundle is what a CardDAV Worker's vault entry holds: as whom, and which group form
// the address book uses.
//
//	{"username": "verkauf@firma.ch", "password": "app-specific password", "groups": "categories"}
//
// The password is usually not the account's own: iCloud, Fastmail, Nextcloud with two
// factors and mailbox.org issue an app-specific one, which is also what can be revoked
// without changing the account's. Groups is not secret, but it belongs here for
// Exchange's reason — the bundle is the whole of a Worker's configuration beside its
// endpoint — and an operator sets it once with the account.
type Bundle struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Groups   string `json:"groups,omitempty"`
}

// ParseBundle reads a bundle. An unknown field is refused: a misspelt "group" would
// otherwise leave an iCloud address book in the categories form, where every label
// written is invisible in Apple Contacts.
func ParseBundle(raw string) (Bundle, error) {
	if strings.TrimSpace(raw) == "" {
		return Bundle{}, fmt.Errorf("carddav: the credential bundle is empty")
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	var b Bundle
	if err := dec.Decode(&b); err != nil {
		// The decoder's message for an unknown field names the field and nothing else,
		// so it is passed on; any other failure could quote a fragment of the password.
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			return Bundle{}, fmt.Errorf("carddav: the credential bundle is not a JSON object (syntax error at byte %d)", syn.Offset)
		}
		if strings.Contains(err.Error(), "unknown field") {
			return Bundle{}, fmt.Errorf("carddav: the credential bundle has an %s (want username, password, groups)", strings.TrimPrefix(err.Error(), "json: "))
		}
		return Bundle{}, fmt.Errorf("carddav: the credential bundle is not a JSON object of the expected shape")
	}
	return b, nil
}

// Options are the parts of a client that belong to the host or the test rather than to
// the Worker.
type Options struct {
	// HTTPClient reaches the server; nil is the shared bounded one. It is copied, not
	// changed: the client follows redirects itself.
	HTTPClient *http.Client
	// Now stamps REV on a card that carries one; nil is time.Now.
	Now func() time.Time
}

// NewClient builds the client a Worker's endpoint and bundle describe.
//
// The endpoint is required: unlike Google, CardDAV has no one address for everyone. It
// is the address book's own URL or any URL the server can be asked from — its root, a
// principal, /.well-known/carddav — and must be https, since the password goes with
// every request; http is accepted for a loopback host, where there is no network to
// cross.
//
// Nothing here reaches the network — the address book is found on the first call — so
// a Worker that builds is one whose configuration is complete, not one known to work.
func NewClient(endpoint, raw string, opts Options) (*Client, error) {
	b, err := ParseBundle(raw)
	if err != nil {
		return nil, err
	}
	u, err := parseEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(b.Username) == "" || b.Password == "" {
		return nil, fmt.Errorf("carddav: the bundle needs a username and a password (usually an app-specific password from the provider)")
	}
	style, err := catalog.ParseGroupStyle(b.Groups)
	if err != nil {
		return nil, err
	}
	base := opts.HTTPClient
	if base == nil {
		base = nettimeout.HTTPClient()
	}
	httpc := *base
	// Every redirect is followed by the client itself: Go's would turn a PROPFIND into
	// a GET, and would drop the credential for another host.
	httpc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Client{
		endpoint: u, username: strings.TrimSpace(b.Username), password: b.Password,
		groups: style, http: &httpc, now: now,
	}, nil
}

// parseEndpoint checks a Worker's endpoint.
func parseEndpoint(endpoint string) (*url.URL, error) {
	raw := strings.TrimSpace(endpoint)
	if raw == "" {
		return nil, fmt.Errorf("carddav: the Worker needs an endpoint: the address book's URL, or the server's (https://cloud.example.ch/remote.php/dav, https://contacts.icloud.com)")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, fmt.Errorf("carddav: the Worker's endpoint %q is not an https URL", clip(raw))
	}
	if u.User != nil {
		return nil, fmt.Errorf("carddav: the Worker's endpoint carries a user name or password; put them in the credential bundle instead")
	}
	if u.Scheme == "http" && !loopback(u.Hostname()) {
		return nil, fmt.Errorf("carddav: the Worker's endpoint must be https: the password is sent with every request, and over http it would cross the network in the clear")
	}
	u.RawQuery, u.Fragment = "", ""
	if u.Path == "" {
		u.Path = "/"
	}
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

// clip shortens a value quoted in an error message.
func clip(s string) string {
	if len(s) > 60 {
		return s[:60] + "…"
	}
	return s
}
