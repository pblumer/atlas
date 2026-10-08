package trustedproxy

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type ctxKey int

const (
	connKey ctxKey = iota
	addrKey
)

// vouched is what the handler concluded: the client, and the trusted hop that named it.
type vouched struct{ client, via string }

// ConnContext is the http.Server hook that lets the handler see through a PROXY
// header to the balancer that wrote it. It only records the connection — reading the
// header can wait for a client, and this runs on the server's accept loop, which must
// not.
func ConnContext(ctx context.Context, c net.Conn) context.Context {
	if tc, ok := c.(*tls.Conn); ok {
		c = tc.NetConn()
	}
	if pc, ok := c.(*conn); ok {
		return context.WithValue(ctx, connKey, pc)
	}
	return ctx
}

// Handler resolves each request's client address before next sees it.
//
// The request's own RemoteAddr is the starting point — after the PROXY listener that is
// already the client a balancer declared. Where that address is itself a trusted proxy,
// X-Forwarded-For is walked from the right: each entry a trusted proxy appended is
// passed over, and the first one that is not a trusted proxy is the client. Everything
// to its left is what the client wrote about itself and is never read. An entry that
// cannot be read ends the walk where it stands, because past it there is nothing left
// that a trusted hop vouched for.
//
// Only X-Forwarded-For is read. RFC 7239's Forwarded carries the same fact in a grammar
// no balancer in front of Atlas has needed yet; it is an amendment when one does.
func (s Set) Handler(next http.Handler) http.Handler {
	if s.Empty() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v, ok := s.resolve(r); ok {
			r = r.WithContext(context.WithValue(r.Context(), addrKey, v))
		}
		next.ServeHTTP(w, r)
	})
}

func (s Set) resolve(r *http.Request) (vouched, bool) {
	client, ok := parseHop(r.RemoteAddr)
	if !ok {
		return vouched{}, false
	}
	var via netip.Addr
	if pc, _ := r.Context().Value(connKey).(*conn); pc != nil {
		if balancer, declared := pc.proxiedBy(); declared {
			via = balancer
		}
	}
	if s.Contains(client) {
		hops := forwardedFor(r.Header)
		for i := len(hops) - 1; i >= 0; i-- {
			hop, ok := parseHop(hops[i])
			if !ok {
				break
			}
			via, client = client, hop
			if !s.Contains(hop) {
				break
			}
		}
	}
	if !via.IsValid() {
		return vouched{}, false
	}
	return vouched{client: client.String(), via: via.String()}, true
}

// forwardedFor is every X-Forwarded-For entry in order, however many header lines
// carried them.
func forwardedFor(h http.Header) []string {
	var hops []string
	for _, v := range h.Values("X-Forwarded-For") {
		for _, hop := range strings.Split(v, ",") {
			hops = append(hops, strings.TrimSpace(hop))
		}
	}
	return hops
}

// parseHop reads an address the way proxies write one: bare, with a port, or bracketed.
// A zone is kept, so Set.Contains can refuse it.
func parseHop(s string) (netip.Addr, bool) {
	if a, err := netip.ParseAddr(s); err == nil {
		return a.Unmap(), true
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		return ap.Addr().Unmap(), true
	}
	if inner, ok := strings.CutPrefix(s, "["); ok {
		if inner, ok = strings.CutSuffix(inner, "]"); ok {
			if a, err := netip.ParseAddr(inner); err == nil {
				return a.Unmap(), true
			}
		}
	}
	return netip.Addr{}, false
}

// Client is the client address a trusted proxy vouched for, and whether one did. Where
// none did, the request's own connection is the answer, and the caller reads it there.
func Client(r *http.Request) (string, bool) {
	v, ok := r.Context().Value(addrKey).(vouched)
	return v.client, ok
}

// Via is the trusted proxy that named the client, or empty where none did — the
// balancer's own address, which an audit line keeps beside the client's so a request
// that came round the balancer can be told from one that came through it.
func Via(r *http.Request) string {
	v, _ := r.Context().Value(addrKey).(vouched)
	return v.via
}
