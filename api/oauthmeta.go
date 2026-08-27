package api

import (
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
)

// Atlas as an OAuth protected resource (RFC 9728, ADR-0200).
//
// This is the resource-server half of that record: it says what this server is,
// how a bearer reaches it, and where to go and get one. The authorization server
// it points at is oauthserver.go.
//
// What it buys is that a refusal explains itself. A hosted MCP client — a
// connector on somebody else's infrastructure, driven by a person in a browser —
// has nowhere to put an API token, so it goes looking for an authorization flow
// when it is refused. Without a pointer it has to guess, and what it guesses is
// /authorize: before this existed Atlas served no such route, and the operator saw
// a 404 with no reason in it.
//
// The two halves shipped separately on purpose, and this one is the durable one.
// If Atlas ever delegates to an external identity provider, the authorization
// server here is deleted and authorization_servers names somebody else; nothing
// else in this file moves.

// protectedResourceMetadataPath is the well-known location of the metadata
// document. RFC 9728 also defines a path-suffixed form for a resource served
// under a path, which is the one an MCP client at /mcp looks for; both are served.
const protectedResourceMetadataPath = "/.well-known/oauth-protected-resource"

// WithExternalURL states the origin under which this server is reachable from
// outside — "https://atlas.example.com" — for the absolute URLs the discovery
// documents and the WWW-Authenticate challenge have to carry.
//
// It exists because Atlas terminates no TLS. Every deployment with a certificate
// has a proxy in front, so the scheme a request arrives with is http and the
// origin derived from it would name a URL no client can use. Setting this once is
// the reliable answer; leaving it unset falls back to what the request says, which
// is right for direct access and for tests.
func WithExternalURL(origin string) Option {
	return func(s *Server) { s.externalURL = strings.TrimRight(strings.TrimSpace(origin), "/") }
}

// externalBase returns the origin to build self-referential URLs from.
//
// A configured origin wins. Otherwise the host is what the request addressed and
// the scheme is how it arrived — honouring X-Forwarded-Proto, which is the one
// forwarded header consulted anywhere in this package.
//
// That is a deliberate difference from httpapi.ClientIP, which refuses
// X-Forwarded-For. The question there feeds a security decision: which bucket a
// login attempt is charged to, where a client-supplied value would let an attacker
// spread a password guess across as many buckets as it likes. The question here
// shapes a URL in the caller's own response and reaches nothing: a caller who lies
// about the scheme is told about a document at a scheme of their choosing, on a
// host they already addressed, and no other caller is affected — the document
// carries Vary so a shared cache keeps the variants apart.
//
// An empty result means no absolute URL can be built (a request that carried no
// Host at all), and every caller treats that as "say nothing" rather than emitting
// a URL that is wrong.
func (s *Server) externalBase(r *http.Request) string {
	if s.externalURL != "" {
		return s.externalURL
	}
	if r.Host == "" {
		return ""
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	// A proxy chain sends a comma-separated list; the first entry is the client-facing
	// hop, which is the one whose scheme a client would use.
	if fwd := r.Header.Get("X-Forwarded-Proto"); fwd != "" {
		if first := strings.TrimSpace(strings.Split(fwd, ",")[0]); first == "http" || first == "https" {
			scheme = first
		}
	}
	return scheme + "://" + r.Host
}

// resourceMetadataURL is the document to point a refused request at.
//
// RFC 9728 derives a resource's metadata URL from its path, so the honest answer
// for a 401 from /mcp is the /mcp document — that is the resource a token would
// have to be issued for. Every other route is covered by the document describing
// the server, because Atlas publishes one document per *resource*, not one per
// route, and /api/v1 is not a separate resource from the server that serves it.
func (s *Server) resourceMetadataURL(r *http.Request) string {
	base := s.externalBase(r)
	if base == "" {
		return ""
	}
	if s.mcpHandler != nil && (r.URL.Path == "/mcp" || strings.HasPrefix(r.URL.Path, "/mcp/")) {
		return base + protectedResourceMetadataPath + "/mcp"
	}
	return base + protectedResourceMetadataPath
}

// handleProtectedResourceMetadata serves the RFC 9728 document. One handler serves
// both mount points: the resource it describes is the origin plus whatever path
// follows the well-known prefix, which is "" for the server itself and "/mcp" for
// the transport.
func (s *Server) handleProtectedResourceMetadata(w http.ResponseWriter, r *http.Request) {
	base := s.externalBase(r)
	suffix := strings.TrimPrefix(r.URL.Path, protectedResourceMetadataPath)

	doc := map[string]any{
		// REQUIRED. A client checks its token's audience against this, so it names the
		// resource exactly as a token would have to name it.
		"resource": base + suffix,
		// Human-readable, for a client that shows the person what they are connecting to.
		"resource_name": "Atlas",
		// Atlas reads Authorization: Bearer and nothing else — never a query parameter,
		// which would put the credential in access logs and browser history.
		"bearer_methods_supported": []string{"header"},
	}
	// Where to go and get a token. This field was deliberately absent while Atlas
	// issued none — naming an authorization server it could not honour would have
	// walked a client through an entire flow to refuse it at the end. It issues them
	// now (oauthserver.go), so the pointer is the truth and the flow completes.
	if base != "" {
		doc["authorization_servers"] = []string{base}
	}

	// The document varies by the header externalBase consults, so a shared cache in
	// front of Atlas cannot serve one caller's scheme to another.
	w.Header().Set("Vary", "X-Forwarded-Proto")
	httpapi.JSON(w, http.StatusOK, doc)
}
