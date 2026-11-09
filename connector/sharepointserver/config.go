package sharepointserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/pblumer/atlas/connector/clientreg"
)

// Bundle is the optional JSON a Worker carries beside its endpoint. There is no
// credential field by construction: the cmdlets run as the account the worker runs as.
//
//	{"managedPath": "sites", "timeoutSeconds": 300}
type Bundle struct {
	// ManagedPath is the wildcard managed path sites live under; empty is "sites".
	ManagedPath string `json:"managedPath,omitempty"`
	// TimeoutSeconds bounds one call; zero is [defaultTimeout].
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`
}

// Options are the parts of a client that belong to the host rather than to the Worker.
type Options struct {
	// PowerShell is the interpreter; empty is [defaultPowerShell].
	PowerShell string
}

var managedPath = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// Registry resolves a Worker name to the web application behind it.
type Registry = clientreg.Registry[*Client]

// NewRegistry creates an empty registry.
func NewRegistry() *Registry { return clientreg.New[*Client]() }

// parseBundle reads a bundle; empty is all defaults. An unknown field is refused, for
// the usual reason and one more: a password here is an operator who has misunderstood
// where this Worker's identity comes from.
func parseBundle(raw string) (Bundle, error) {
	var b Bundle
	if strings.TrimSpace(raw) == "" {
		return b, nil
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		// A syntax error is reported without its text, since it could quote a secret an
		// operator pasted here by mistake.
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			return Bundle{}, fmt.Errorf("sharepointserver: the settings are not a JSON object (syntax error at byte %d)", syn.Offset)
		}
		if strings.Contains(err.Error(), "unknown field") {
			return Bundle{}, fmt.Errorf("sharepointserver: the settings have an %s; this Worker holds no credential, it runs as the worker's own account", strings.TrimPrefix(err.Error(), "json: "))
		}
		return Bundle{}, fmt.Errorf("sharepointserver: the settings are not a JSON object of the expected shape")
	}
	return b, nil
}

// NewClient builds the client an endpoint and bundle describe. endpoint is the web
// application's URL. Nothing here reaches the farm.
func NewClient(endpoint, raw string, opts Options) (*Client, error) {
	b, err := parseBundle(raw)
	if err != nil {
		return nil, err
	}
	web := strings.TrimRight(strings.TrimSpace(endpoint), "/")
	u, err := url.Parse(web)
	if web == "" || err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" {
		return nil, fmt.Errorf("sharepointserver: the Worker's endpoint %q must be the web application's URL (http://sp01.contoso.local)", endpoint)
	}
	path := strings.Trim(strings.TrimSpace(b.ManagedPath), "/")
	if path == "" {
		path = defaultManagedPath
	}
	if !managedPath.MatchString(path) {
		return nil, fmt.Errorf("sharepointserver: managedPath %q must be one path segment such as sites", b.ManagedPath)
	}
	if b.TimeoutSeconds < 0 {
		return nil, fmt.Errorf("sharepointserver: timeoutSeconds must not be negative")
	}
	timeout := time.Duration(b.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = defaultTimeout
	}
	ps := strings.TrimSpace(opts.PowerShell)
	if ps == "" {
		ps = defaultPowerShell
	}
	return newClient(web, path, ps, timeout), nil
}

// CheckBundle reports whether an endpoint and settings build a client at all.
func CheckBundle(endpoint, raw string) error {
	_, err := NewClient(endpoint, raw, Options{})
	return err
}
