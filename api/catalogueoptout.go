package api

import (
	"net/http"
)

// Switching the catalogue off (ADR-0434).
//
// An installation that runs Atlas as a workflow engine and has no use for a shop says
// so once, at start, with --catalogue=false. What that removes is the area's
// *surface*: every route of the shop, the catalogue, the orders and the inventory
// (the two route tags below), the shop page, the area's menu entries in the Console,
// its tools in the MCP adapter, its picture on the starmap, and the fulfilment and
// approval processes it would otherwise file into the system project.
//
// What it does not touch is anything the engine does with what is already in the
// log. Entitlements, action outcomes and the event feed are engine state, applied
// by the one applyToState live and on recovery (I4); a switch that changed what is
// applied would make replay depend on a flag, and the state a restart rebuilt would
// differ from the state the server had. The shop send tasks keep their handlers for
// the same reason: a model that is already running behaves the same under either
// setting, and a task without a handler does not fail, it parks (ADR-0411). And the
// stores stay where they are, read by nobody: switching back on is a restart, and
// everything that was there is there.

// catalogueRouteTags are the route tags of the area. The tag is the boundary
// because every route already has to carry one; TestTheCatalogueSwitchCoversTheWholeArea
// states the same boundary twice more — by path and by the package of the handler —
// and fails when a route of the area arrives under some other tag.
var catalogueRouteTags = map[string]bool{"Catalogue": true, "Order": true}

// catalogueRoute reports whether r belongs to the area the switch removes.
func catalogueRoute(r apiRoute) bool { return catalogueRouteTags[r.op.tag] }

// WithoutCatalogue switches off the shop, the catalogue, the orders and the
// inventory: their routes are not mounted, so each answers like an endpoint that
// never existed, and /api/v1/info says catalogue:false so the Console leaves them out
// of its menus. Nothing stored is removed, and the engine runs every deployed model
// exactly as it would with the area on.
func WithoutCatalogue() Option { return func(s *Server) { s.catalogueOff = true } }

// offeredRoutes is the route table less what the operator switched off. It is the
// one place the decision is applied, because the table is the one place the mux, the
// OpenAPI document and the node descriptor read the surface from.
func (s *Server) offeredRoutes(all []apiRoute) []apiRoute {
	if !s.catalogueOff {
		return all
	}
	out := make([]apiRoute, 0, len(all))
	for _, r := range all {
		if !catalogueRoute(r) {
			out = append(out, r)
		}
	}
	return out
}

// catalogueSystemProcesses are the embedded platform processes that exist only to
// run the shop: the fulfilment orchestration and the three approval kinds. With the
// area off they are not filed into the system project. They stay in systemPIDs
// regardless, so one deployed by an earlier start with the area on is still a
// platform process the delete guard protects.
//
// Which processes these are is read off what they call:
// TestTheCatalogueSystemProcessesAreTheOnesThatCallIt fails when a process that calls
// a route of the area is missing here, or one that does not is held back.
var catalogueSystemProcesses = map[string]bool{
	"atlas-auftrag-erfuellung":       true,
	"atlas-genehmigung-fix":          true,
	"atlas-genehmigung-rolle":        true,
	"atlas-genehmigung-vorgesetzter": true,
}

// catalogueSystemForms are the embedded forms only those processes use.
var catalogueSystemForms = map[string]bool{"genehmigung": true}

// handleSwitchedOffPage answers the shop page, and its old address, on a server that
// does not offer a shop. A 404 is the truth here — the service is switched off —
// and a plain one is better than serving a page that renders and then fails every
// call it makes, which reads as an outage to whoever followed a bookmark.
func (s *Server) handleSwitchedOffPage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("Der Shop ist auf diesem Server ausgeschaltet.\n" +
		"The shop is switched off on this server.\n"))
}
