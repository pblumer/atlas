package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/pblumer/atlas/logging"
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

// warnCatalogueWorkInFlight says, once at start, what switching the catalogue off
// is about to break (ADR-0434, its open question answered). The switch is never
// refused — an operator must always be able to take the area away — but a process
// still running that calls the order routes will fail at that call and, its retries
// spent, raise an incident. One line at start names how many and which, so the
// operator meets the consequence before the incidents do.
//
// It counts processes, not orders. An order's status lives in a sidecar read whole,
// which grows with every order ever placed; what fails is a running process, and the
// engine keeps a live-instance counter per definition (ADR-0080). So the cost is one
// read per deployed version of the processes concerned — design-time size — and the
// count is of exactly what will fail: the shop's own fulfilment and approval
// processes, and the processes products bind in the two-process form, whose last step
// reports to the order by convention (ADR-0312). A lifecycle process talks to its
// order through shop tasks, which keep their handlers, so it is not counted.
//
// Runs in New before the loop serves, like loadDeployments, so it reads the stores
// directly.
func (s *Server) warnCatalogueWorkInFlight() {
	byProcess, err := s.catalogueWorkInFlight()
	if err != nil {
		logging.Warn(logging.ServerCatalogueInFlight,
			"the catalogue is switched off, and whether processes are still working orders could not be read",
			slog.String("error", err.Error()))
		return
	}
	shop, product := 0, 0
	ids := make([]string, 0, len(byProcess))
	for id, n := range byProcess {
		ids = append(ids, id)
		if catalogueSystemProcesses[id] {
			shop += n
		} else {
			product += n
		}
	}
	if shop+product == 0 {
		return
	}
	sort.Strings(ids)
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf("%s=%d", id, byProcess[id])
	}
	logging.Warn(logging.ServerCatalogueInFlight,
		"the catalogue is switched off while processes are still working orders: each fails at its next call to the "+
			"order routes and, its retries spent, raises an incident. Switch the catalogue back on and retry those "+
			"incidents to finish the orders, or end the instances deliberately",
		slog.Int("shop_process_instances", shop),
		slog.Int("product_process_instances", product),
		slog.String("processes", strings.Join(parts, ",")))
}

// catalogueWorkInFlight counts the live instances, by BPMN process id, of every
// process that calls the order routes: the shop's system processes and the
// two-process bindings of the catalogue's products. Ids with none running are left
// out.
func (s *Server) catalogueWorkInFlight() (map[string]int, error) {
	items, err := s.catalogStore.Items()
	if err != nil {
		return nil, err
	}
	reports := map[string]bool{}
	for id := range catalogueSystemProcesses {
		reports[id] = true
	}
	for _, it := range items {
		for _, p := range []string{it.ProvisionProcess, it.DeprovisionProcess} {
			if p = strings.TrimSpace(p); p != "" {
				reports[p] = true
			}
		}
	}
	out := map[string]int{}
	for key, d := range s.deployments {
		if !reports[d.ProcessID] {
			continue
		}
		n, err := s.store.DefInstanceCount(key)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			out[d.ProcessID] += n
		}
	}
	return out, nil
}
