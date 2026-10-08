// Package trustedproxy takes a client's address from the proxy in front of Atlas, and
// from nobody else (ADR-draft-trusted-proxies).
//
// Behind a load balancer every connection comes from the balancer. The login throttle
// then charges every person to one bucket (ADR-0197), and every audit line names the
// balancer as the client — the one fact about a request an audit trail exists to keep.
// The balancer knows the real address and can say so, in one of two ways depending on
// what it is:
//
//   - A balancer that speaks HTTP appends it to X-Forwarded-For.
//   - A balancer that forwards TCP — and so cannot add a header to a TLS stream it does
//     not decrypt — writes a PROXY protocol header in front of the stream.
//
// Both are things a client can also write. What makes either believable is the
// connection it arrived on, so both are read only on a connection from an address the
// operator listed, and from the side the nearest proxy controls: the PROXY header must
// be the very first bytes, and X-Forwarded-For is walked from the right. A connection
// from anywhere else is left exactly as it was — its first bytes are not even peeked at.
//
// An empty Set, which is what a server started without --trusted-proxies has, changes
// nothing at all: Listener returns the listener and Handler the handler.
package trustedproxy

import (
	"fmt"
	"net/netip"
	"strings"
	"unicode"
)

// Set is the proxies whose word about a client's address is taken.
type Set struct{ prefixes []netip.Prefix }

// Parse reads a list of addresses and CIDR prefixes, separated by commas or spaces. An
// address is the prefix of its own full length.
//
// A prefix that covers every address is refused: trusting everybody to name the client
// is trusting the client to name itself, which is the one thing the list exists to rule
// out. Anything that does not parse is refused too, rather than dropped — a list that
// silently lost an entry trusts less than its operator believes, and one that silently
// grew would trust more.
func Parse(spec string) (Set, error) {
	var s Set
	for _, f := range strings.FieldsFunc(spec, func(r rune) bool { return r == ',' || unicode.IsSpace(r) }) {
		p, err := parsePrefix(f)
		if err != nil {
			return Set{}, err
		}
		if p.Bits() == 0 {
			return Set{}, fmt.Errorf("%s trusts every address to name the client, which lets any client name itself; list the proxy's own addresses or subnet instead", f)
		}
		s.prefixes = append(s.prefixes, p)
	}
	return s, nil
}

func parsePrefix(f string) (netip.Prefix, error) {
	if strings.Contains(f, "/") {
		p, err := netip.ParsePrefix(f)
		if err != nil {
			return netip.Prefix{}, fmt.Errorf("%q is not an address or a CIDR prefix", f)
		}
		p = p.Masked()
		// ::ffff:10.0.0.0/104 is 10.0.0.0/8 spelled for a dual-stack socket. Stored as
		// what it means, it matches an IPv4 peer however the socket reports it.
		if a := p.Addr(); a.Is4In6() && p.Bits() >= 96 {
			p = netip.PrefixFrom(a.Unmap(), p.Bits()-96)
		}
		return p, nil
	}
	a, err := netip.ParseAddr(f)
	if err != nil || a.Zone() != "" {
		return netip.Prefix{}, fmt.Errorf("%q is not an address or a CIDR prefix", f)
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// Empty reports whether nothing is trusted.
func (s Set) Empty() bool { return len(s.prefixes) == 0 }

// Contains reports whether a is one of the trusted proxies. An IPv4 address carried in
// IPv6 is the IPv4 address. One with a zone is never trusted: fe80::1 on one interface
// is not fe80::1 on another, and the list cannot say which interface it meant.
func (s Set) Contains(a netip.Addr) bool {
	if a.Zone() != "" {
		return false
	}
	a = a.Unmap()
	for _, p := range s.prefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// String is the list as the server understood it, for the startup log.
func (s Set) String() string {
	parts := make([]string, len(s.prefixes))
	for i, p := range s.prefixes {
		parts[i] = p.String()
	}
	return strings.Join(parts, ", ")
}
