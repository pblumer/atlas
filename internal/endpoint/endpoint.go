// Package endpoint reduces a configured endpoint to the three parts an architecture
// sketch shows of it — scheme, host and port — and to nothing else (ADR-0471).
//
// It is one package because two processes do the same reduction for two different
// reasons. A worker reduces the endpoints it holds before it reports them, so a
// credential or a path in its configuration never leaves it; the server reduces what
// it records and what it is told, because a report is input and not a promise. One
// function on both sides is what keeps "scheme, host and port" from meaning two
// slightly different things.
//
// The result is rebuilt from the parsed parts rather than trimmed from the input, so
// user information, a path, a query or a fragment cannot survive it by an oversight in
// what was cut.
package endpoint

import (
	"net"
	"net/url"
	"strings"
)

// Address is a reduced endpoint. Port is empty when the configured value named none,
// which means the scheme's or the Worker Type's default; the reduction does not guess
// which.
type Address struct {
	Scheme string
	Host   string
	Port   string
}

// Reduce reads a configured endpoint in any of the shapes the Worker Types store — a
// URL, "host:port", or a bare host — and returns its scheme, host and port. ok is
// false when the value is empty or cannot be an address.
func Reduce(raw string) (Address, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Address{}, false
	}
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || !validHost(u.Hostname()) {
			return Address{}, false
		}
		return Address{Scheme: strings.ToLower(u.Scheme), Host: strings.ToLower(u.Hostname()), Port: u.Port()}, true
	}
	if i := strings.IndexAny(raw, "/?#"); i >= 0 {
		raw = raw[:i]
	}
	if i := strings.LastIndex(raw, "@"); i >= 0 {
		raw = raw[i+1:]
	}
	host, port := raw, ""
	if h, p, err := net.SplitHostPort(raw); err == nil {
		host, port = h, p
	}
	if !validHost(host) {
		return Address{}, false
	}
	return Address{Host: strings.ToLower(host), Port: port}, true
}

// validHost refuses what cannot be a host name or an address, so a value that only
// looks parsed — a sentence, a credential pasted into the wrong field — is reported as
// unreadable instead of being drawn as a machine.
func validHost(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for _, r := range h {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == ':', r == '_':
		default:
			return false
		}
	}
	return true
}

// String writes the address the way a reader writes one: scheme://host:port, with an
// IPv6 host in brackets and the parts that are empty left out.
func (a Address) String() string {
	host := a.Host
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if a.Port != "" {
		host += ":" + a.Port
	}
	if a.Scheme != "" {
		return a.Scheme + "://" + host
	}
	return host
}
