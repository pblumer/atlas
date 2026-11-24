package api

import (
	"net/http"
	"strings"
)

// publicFormsPrefix is the route family of a public start form (ADR-0029): the page,
// its schema and its submission.
const publicFormsPrefix = "/public/forms/"

// withSecurityHeaders says three things about every answer of next, before next has
// run:
//
//   - X-Content-Type-Options: nosniff — the declared content type is the type, so an
//     answer that is a document is never run as a script because it looked like one;
//   - Referrer-Policy: same-origin — the address of a page inside Atlas, which names
//     instances, orders and people, is sent as a referrer to Atlas and to nobody else;
//   - Content-Security-Policy: frame-ancestors 'self' and X-Frame-Options: SAMEORIGIN —
//     only a page of this origin may put an answer in a frame, so another site cannot
//     lay its own controls over the Console (the second header for browsers that do
//     not read the first).
//
// They used to be set by the handlers that had thought of them. Here they are the
// default of a route nobody thought about, which is the state every new route starts
// in. It is a default and no more: the headers are written before the handler runs,
// so a handler that sets one of them — the pages a link opens keep out of every
// frame (setNoFraming), a stored document is sandboxed — writes over it and is the
// one that is sent.
//
// One route family is left frameable on purpose. A public start form may be shown
// in a frame on somebody else's page (ADR-0186 names that as a way of embedding it),
// carries no session and can do only what its link allows; the framing rule would
// break the sites that do it and protect nothing there.
//
// Strict-Transport-Security is not here: it binds the whole host name and is the
// operator's to switch on (withHSTS, ADR-0560).
func withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		if !strings.HasPrefix(r.URL.Path, publicFormsPrefix) {
			h.Set("Content-Security-Policy", "frame-ancestors 'self'")
			h.Set("X-Frame-Options", "SAMEORIGIN")
		}
		next.ServeHTTP(w, r)
	})
}
