package api

import (
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
)

// The shop over MCP (ADR-0550).
//
// An agent connected as a person — over the /mcp transport with that person's
// OAuth grant or session (ADR-0200), or through a stdio adapter holding that
// person's API token — may browse the shop, order, follow and change orders, decide
// the approvals it holds and ask a held position for a customer's action, always as
// that person and with exactly their rights: each tool is the HTTP operation the
// portal calls (ADR-0016), and the handler decides as it decides for the portal.
//
// It is an opt-out. --mcp-shop=false (ATLAS_MCP_SHOP=false, [WithoutMCPShop]) keeps
// the portal working and refuses these routes to a request that came through MCP.
// The line is held here, by the server, and not only by the adapter.

// mcpAdapterHeader is how an MCP adapter declares that a request is one of its tool
// calls. Every request the mcp package's Client makes carries it: the in-process
// adapter behind /mcp and a stdio adapter (atlas mcp) alike.
//
// It is a declaration and not a proof, unlike mcpTransportHeader: a stdio adapter is
// a separate process holding an ordinary API token, and nothing it sends can be
// verified by this server. That is enough for what it decides. The switch is about
// whether an agent drives the shop, not a boundary against the holder of the
// credential — who may use the portal with the same rights anyway — and every
// adapter Atlas ships sends it. What the declaration cannot reach is a client that
// does not send it: a third-party MCP server wrapping this HTTP API, or an older
// atlas mcp binary. The decision record says so.
//
// The name is duplicated in the mcp package rather than imported, as
// mcpTransportHeader is; TestMCPAdapterHeaderMatchesTheAdapter holds the two together.
const mcpAdapterHeader = "X-Atlas-MCP-Adapter"

// WithoutMCPShop refuses the shop's customer side to requests that arrive through
// MCP (--mcp-shop=false). The portal is unaffected: the same routes answer a request
// that did not come through an adapter exactly as before.
func WithoutMCPShop() Option { return func(s *Server) { s.mcpShopOff = true } }

// mcpShopRoutes are the routes --mcp-shop=false refuses over MCP: what a person does
// in the shop, and nothing an operator or the system does. The action act is not
// here because only its customer half is the shop's; [Server.refuseCustomerActionOverMCP]
// holds that line inside its handler.
//
// TestMCPShopRoutesAreRoutes holds every entry to the route table, so a renamed route
// cannot fall out of the switch silently.
var mcpShopRoutes = map[string]bool{
	"GET /api/v1/shop/catalogs": true,
	"GET /api/v1/shop/catalog":  true,
	"POST /api/v1/shop/releases/{release}/items/{item}/form-data/{field}/query": true,
	"POST /api/v1/orders":                                    true,
	"GET /api/v1/orders":                                     true,
	"GET /api/v1/orders/{id}":                                true,
	"GET /api/v1/orders/{id}/next":                           true,
	"GET /api/v1/shop/tasks":                                 true,
	"GET /api/v1/shop/orders/{id}/lines/{position}/progress": true,
	"POST /api/v1/orders/{id}/cancel":                        true,
	"POST /api/v1/orders/{id}/lines/{item}/cancel":           true,
	"POST /api/v1/orders/{id}/lines/{item}/details":          true,
	"POST /api/v1/orders/{id}/lines/{item}/return":           true,
	"POST /api/v1/orders/{id}/lines/{item}/change":           true,
	"GET /api/v1/approvals":                                  true,
	"POST /api/v1/approvals/decide":                          true,
}

// mcpShopRefusal is the sentence a refused request is answered with. It names the
// setting in both spellings, so an agent can tell the person exactly what the
// operator decided and the operator can find it.
const mcpShopRefusal = "the shop is not offered over MCP on this server: it was started with " +
	"--mcp-shop=false (ATLAS_MCP_SHOP=false). The person can still use the shop in a browser"

// viaMCP reports whether a request came through an MCP adapter: one the /mcp
// transport stamped (verified), or one that declares itself with mcpAdapterHeader.
func (s *Server) viaMCP(r *http.Request) bool {
	return s.viaMCPTransport(r) || r.Header.Get(mcpAdapterHeader) != ""
}

// guardMCPShop wraps a shop route's handler so a server started with
// --mcp-shop=false refuses it to a request that came through MCP. A server that
// offers the shop over MCP — the default — gets the handler back unwrapped.
func (s *Server) guardMCPShop(route string, h http.HandlerFunc) http.HandlerFunc {
	if !s.mcpShopOff || !mcpShopRoutes[route] {
		return h
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if s.viaMCP(r) {
			httpapi.Error(w, http.StatusForbidden, mcpShopRefusal)
			return
		}
		h(w, r)
	}
}

// refuseCustomerActionOverMCP answers 403 and reports true when a server started
// with --mcp-shop=false is asked over MCP for an action as a customer — the trigger
// named "customer", or none, which asks as whoever the caller is. An operator's or
// the system's action stays available to agents either way (ADR-0429 §2).
func (s *Server) refuseCustomerActionOverMCP(w http.ResponseWriter, r *http.Request, trigger string) bool {
	if !s.mcpShopOff || !s.viaMCP(r) {
		return false
	}
	// Spelled out rather than read from api/catalog, which this file must not import
	// (ADR-0515: the server reaches into the shop from ever fewer files);
	// TestMCPShopSwitchRefusesOnlyTheCustomersActions pins the two names.
	switch strings.TrimSpace(trigger) {
	case "operator", "system":
		return false
	}
	httpapi.Error(w, http.StatusForbidden, mcpShopRefusal+"; an operator's or the system's action "+
		"may still be asked with trigger operator or system")
	return true
}
