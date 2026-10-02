package api

import (
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
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
	reporting, err := s.processesReportingToOrders()
	if err == nil {
		s.catalogueReporting = reporting
	}
	var byProcess map[string]int
	if err == nil {
		byProcess, err = s.catalogueWorkInFlight()
	}
	if err != nil {
		logging.Warn(logging.ServerCatalogueInFlight,
			"the catalogue is switched off, and whether processes are still working orders could not be read",
			slog.String("error", err.Error()))
		return
	}
	st := strandedBy(byProcess)
	if st.ShopProcessInstances+st.ProductProcessInstances == 0 {
		return
	}
	parts := make([]string, len(st.Processes))
	for i, p := range st.Processes {
		parts[i] = fmt.Sprintf("%s=%d", p.ProcessID, p.Instances)
	}
	shop, product := st.ShopProcessInstances, st.ProductProcessInstances
	logging.Warn(logging.ServerCatalogueInFlight,
		"the catalogue is switched off while processes are still working orders: each fails at its next call to the "+
			"order routes and, its retries spent, raises an incident. Switch the catalogue back on and retry those "+
			"incidents to finish the orders, or end the instances deliberately",
		slog.Int("shop_process_instances", shop),
		slog.Int("product_process_instances", product),
		slog.String("processes", strings.Join(parts, ",")))
}

// processesReportingToOrders is every BPMN process id that calls the order routes:
// the shop's system processes and the two-process bindings of the catalogue's
// products. It reads the product store whole, so it is read once, at start: with the
// catalogue off nothing can change a product — every route that writes one is
// switched off with it — so the set cannot go stale while it is used.
func (s *Server) processesReportingToOrders() (map[string]bool, error) {
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
	return reports, nil
}

// catalogueWorkInFlight counts the live instances, by BPMN process id, of every
// process in catalogueReporting. Ids with none running are left out. It reads the
// deployments and one counter per deployed version of those processes — design-time
// size — so it may run on the loop; after start it must, since the deployments are
// the loop's.
func (s *Server) catalogueWorkInFlight() (map[string]int, error) {
	out := map[string]int{}
	for key, d := range s.deployments {
		if !s.catalogueReporting[d.ProcessID] {
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

// strandedProcess is one process still working orders, and how many of its instances.
type strandedProcess struct {
	ProcessID string `json:"processId"`
	Instances int    `json:"instances"`
}

// catalogueSwitchResp is GET /api/v1/catalogue-switch: whether the catalogue is
// served, and — when it is not — what the switch strands. The counts are zero with
// the catalogue on, because then nothing is stranded.
type catalogueSwitchResp struct {
	Catalogue               bool              `json:"catalogue"`
	ShopProcessInstances    int               `json:"shopProcessInstances"`
	ProductProcessInstances int               `json:"productProcessInstances"`
	Processes               []strandedProcess `json:"processes"`
}

// strandedBy sums a per-process count into the shop's own processes and the
// products', and lists the processes by id so a reader sees them in a stable order.
func strandedBy(byProcess map[string]int) catalogueSwitchResp {
	out := catalogueSwitchResp{Processes: []strandedProcess{}}
	for id, n := range byProcess {
		if catalogueSystemProcesses[id] {
			out.ShopProcessInstances += n
		} else {
			out.ProductProcessInstances += n
		}
		out.Processes = append(out.Processes, strandedProcess{ProcessID: id, Instances: n})
	}
	sort.Slice(out.Processes, func(i, j int) bool { return out.Processes[i].ProcessID < out.Processes[j].ProcessID })
	return out
}

// handleCatalogueSwitch answers the Console's dashboard, which shows an administrator
// what the start's warning said — read live, so it goes away once the stranded
// instances are finished or ended, rather than repeating what was true at boot. A log
// line at start is lost wherever nobody reads the start, and in a container that is
// most places.
//
// It is not a route of the catalogue: it is the one that has something to say
// precisely when the catalogue is off, so its tag is System and the switch leaves it
// served (ADR-0434).
func (s *Server) handleCatalogueSwitch(w http.ResponseWriter, _ *http.Request) {
	if !s.catalogueOff {
		httpapi.JSON(w, http.StatusOK, catalogueSwitchResp{Catalogue: true, Processes: []strandedProcess{}})
		return
	}
	if s.catalogueReporting == nil {
		// The product store could not be read at start, and the start said so; a
		// count without it would read as "nothing stranded", which is not known.
		httpapi.Error(w, http.StatusServiceUnavailable,
			"what the switched-off catalogue strands could not be read at start; the start log says why")
		return
	}
	var (
		byProcess map[string]int
		err       error
	)
	s.do(func() { byProcess, err = s.catalogueWorkInFlight() })
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read what the switched-off catalogue strands: "+err.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, strandedBy(byProcess))
}
