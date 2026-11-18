package api

import (
	"net/http"
	"strconv"
)

// WithHSTS has the server send a transport policy (Strict-Transport-Security,
// RFC 6797) of maxAgeSeconds with every answer to a request that arrived over HTTPS:
// a browser that received it asks this host over HTTPS by itself for that long, so a
// later http:// request never leaves it in the clear (--hsts-max-age,
// ADR-draft-https-redirect-and-hsts).
//
// It is an option and not the default because the policy reaches beyond this server.
// It binds the whole host name in each browser, on every port — another service
// under the same name that answers plain HTTP stops working there — and it lasts
// until its time runs out whatever this server does afterwards. Zero is valid and is
// the way back: it tells a browser to forget the policy. It never names subdomains
// and never asks to be preloaded; both bind hosts this server is not.
func WithHSTS(maxAgeSeconds uint64) Option {
	return func(s *Server) { s.hsts = "max-age=" + strconv.FormatUint(maxAgeSeconds, 10) }
}

// withHSTS adds the policy to the answers of next. "Over HTTPS" is what a cookie's
// Secure attribute asks too (arrivedOverHTTPS): this server's own TLS, or a proxy
// that ended it and says so. Over plain HTTP the header is left out — a browser
// ignores it there — so the loopback listener this process's children use
// (ADR-0191) never carries it.
func (s *Server) withHSTS(next http.Handler) http.Handler {
	if s.hsts == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if arrivedOverHTTPS(r) {
			w.Header().Set("Strict-Transport-Security", s.hsts)
		}
		next.ServeHTTP(w, r)
	})
}
