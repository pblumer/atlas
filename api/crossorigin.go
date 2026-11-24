package api

import (
	"log/slog"
	"net/http"
	"net/url"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/logging"
)

// crossOriginRefusal is what a refused request is told. It names both readings of
// the refusal, because the person who reads it is either looking at an attack that
// did not work or at their own proxy.
const crossOriginRefusal = "cross-origin request refused: a browser sent this from another origin than the one it is addressed to, and a request that changes something is accepted from Atlas's own pages only. If this is Atlas's own Console behind a proxy, have the proxy pass the Host header on unchanged or start the server with --external-url"

// withCrossOriginProtection refuses a browser's state-changing request that does not
// come from this server's own origin (draft ADR "cross-origin protection").
//
// A browser attaches a person's session to a request whichever page caused it.
// SameSite=Lax on the cookie keeps the session off requests from another site — and
// a site is wider than an origin. A service under a sibling host name, or on another
// port of this host, is the same site: its pages' requests arrive signed in, and
// cookies are not scoped to a port at all. A server run open (--auth=false) has no
// cookie to withhold in the first place: any page a person on that machine opens
// could post to it.
//
// What a browser also says, in headers no page can set or forge, is where a request
// comes from: Sec-Fetch-Site, and Origin for the browsers and the plain-HTTP origins
// that do not send it. net/http's CrossOriginProtection reads the two. GET, HEAD and
// OPTIONS always pass; any other method passes when the browser says same-origin (or
// "none": the person did it themselves), when the Origin it names is the host the
// request arrived for, or when neither header is there — which is every client that
// is not a browser, so no API client, worker or command line is affected.
//
// Two route families are called from other origins on purpose and act on no session,
// so they are left as they were:
//
//   - a public start form and the wizard behind it, submitted from the page that
//     embeds them (ADR-0186), with nothing but the link's own token;
//   - the OAuth token, revocation and registration endpoints, which authenticate a
//     client from the body of the request (ADR-0200, ADR-0510).
//
// The origin an operator configured as the server's own (--external-url) is trusted:
// behind a proxy that rewrites Host, it is the answer to "is this origin me" that the
// request itself can no longer give.
//
// It sits in front of the access layer, so a refused request is not authenticated,
// spends nobody's login budget and reaches no handler.
func (s *Server) withCrossOriginProtection(next http.Handler) http.Handler {
	p := http.NewCrossOriginProtection()
	for _, pattern := range []string{
		publicFormsPrefix,
		"/public/wizard/",
		"POST " + oauthTokenPath,
		"POST " + oauthRevokePath,
		"POST " + oauthRegisterPath,
	} {
		p.AddInsecureBypassPattern(pattern)
	}
	if u, err := url.Parse(s.externalURL); err == nil && u.Scheme != "" && u.Host != "" {
		// The setting may carry a trailing slash or a path; an origin has neither.
		_ = p.AddTrustedOrigin(u.Scheme + "://" + u.Host)
	}
	p.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auditRefusal(r, logging.AuthCrossOriginRefused, "refused a state-changing request a browser sent from another origin",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("origin", r.Header.Get("Origin")),
			slog.String("fetch_site", r.Header.Get("Sec-Fetch-Site")),
			slog.String("host", r.Host))
		httpapi.Error(w, http.StatusForbidden, crossOriginRefusal)
	}))
	return p.Handler(next)
}
