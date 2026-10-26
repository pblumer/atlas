package api

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/model"
	"github.com/pblumer/atlas/state"
)

// Deleting a catalogue or a product for good (ADR-0469).
//
// ADR-0312 gave the catalogue no delete: an order placed years ago and a right
// still held resolve through the product, and a store that can delete one can
// orphan that evidence. On a production installation that holds. On a development
// installation it is the wrong rule — somebody imports a catalogue, orders from it
// to see the fulfilment run, and then cannot start over, because every test leaves
// a catalogue, its products, its orders and its holds behind for good.
//
// So the purge exists. It is on unless the server is started with
// --catalogue-purge=false — an opt-out, so a development installation needs no
// configuration, and a production one has to say that it must not delete. Within
// that, it is the administrator's, the route answers what it would remove before
// it removes anything, and the delete itself has to repeat the id it deletes. What
// it removes is the evidence ADR-0312 protects — that is the point of it, and the
// preview says so rather than hiding it.
//
// # What goes, what changes, what stays
//
// A catalogue takes with it its releases, its brand mark and every product whose
// home it is. A product taken out of a catalogue that does not go with it is
// stripped from that catalogue's offer and edges. An order goes when it was placed
// against a release of the catalogue, or when every line of it is a product that
// goes; an order with other lines stays, and its lines on a deleted product are
// withdrawn where they had not started. Every running instance working an order
// that goes, or a line that goes, is cancelled, and every hold of a deleted
// product, or granted by a deleted order, leaves the inventory.
//
// What stays is said in the answer: the processes the products were bound to (a
// process is deployed, not catalogued, and something else may use it), what a
// cancelled process already did in a target system, the inventory's history row
// for every hold it removed, and another catalogue's published release, which
// keeps offering the product until that catalogue is published again.
//
// # Order of the work
//
// The engine first, then the stores, the catalogue last. The purge can fail
// halfway — a store that cannot be written, a server stopping — and the one state
// it must never leave is a catalogue gone with its orders still running, because
// then nothing names them any more and running the purge again finds nothing to
// do. With the catalogue deleted last, a purge that stopped is simply run again:
// every step is idempotent, and what is already gone is not found twice.

// WithoutCataloguePurge refuses an administrator's delete of a catalogue or a
// product for good (--catalogue-purge=false). The preview is still answered, and
// says the purge is switched off. A production installation, whose orders and held
// rights are evidence, is started with it.
func WithoutCataloguePurge() Option { return func(s *Server) { s.cataloguePurgeOff = true } }

// purgeKind is what a purge was asked to delete.
type purgeKind string

const (
	purgeCatalog purgeKind = "catalog"
	purgeProduct purgeKind = "product"
)

// purgeReason is recorded on every order line the purge withdraws, so the line
// says why it stopped rather than looking like somebody's change of mind.
const purgeReason = "the product was deleted for good by an administrator"

// purgeStripped is a catalogue that stays and loses products.
type purgeStripped struct {
	Catalog  string   `json:"catalog"`
	Products []string `json:"products"`
}

// purgeStillOffered is a catalogue whose published release still carries a product
// the purge deletes. A release is frozen, which is the snapshot rule; the shop
// keeps offering it until that catalogue is published again.
type purgeStillOffered struct {
	Catalog  string   `json:"catalog"`
	Release  string   `json:"release"`
	Products []string `json:"products"`
}

// purgeKeptOrder is an order that stays because it carries other products, with
// the lines it loses.
type purgeKeptOrder struct {
	Order    string   `json:"order"`
	Products []string `json:"products"`
}

// purgePlan is what a purge removes, changes and leaves, answered before it acts
// (Done false) and after it has (Done true). Lists are never null, so a reader can
// count every one of them without a guard.
type purgePlan struct {
	Kind purgeKind `json:"kind"`
	ID   string    `json:"id"`
	// Enabled says whether this server allows the purge at all. The preview is
	// answered either way, because knowing what a purge would remove is how an
	// operator decides whether to allow one.
	Enabled bool `json:"enabled"`
	// Confirm is what the delete has to repeat as ?confirm=.
	Confirm string `json:"confirm"`
	Done    bool   `json:"done"`

	Catalogs     []string            `json:"catalogs"`
	Releases     []string            `json:"releases"`
	Products     []string            `json:"products"`
	StrippedFrom []purgeStripped     `json:"strippedFrom"`
	StillOffered []purgeStillOffered `json:"stillOffered"`
	Orders       []string            `json:"orders"`
	KeptOrders   []purgeKeptOrder    `json:"keptOrders"`
	// Instances counts the running process instances cancelled; Entitlements the
	// holds taken out of the inventory, and Holders the people they belonged to.
	// Counts and not lists: a list of who held what is somebody's access, and the
	// inventory answers it to an administrator only one person at a time.
	Instances    int `json:"instances"`
	Entitlements int `json:"entitlements"`
	Holders      int `json:"holders"`
	// Processes are the processes the deleted products were bound to. They stay
	// deployed.
	Processes []string `json:"processes"`
	// Warnings say in sentences what the lists mean for whoever confirms.
	Warnings []string `json:"warnings"`
}

// purgeScope is the plan together with what carrying it out needs.
type purgeScope struct {
	plan purgePlan

	products map[string]bool
	orders   map[string]bool
	// deleted are the records of the orders that go, for the instances they name;
	// kept are the orders that stay with lines on a deleted product.
	deleted []order.Order
	kept    []order.Order
}

// scopePurge decides what a purge of one catalogue or one product removes, from
// the stores' contents alone. It is pure, so the preview and the purge cannot
// disagree about it, and it answers false when there is nothing of that id.
func scopePurge(kind purgeKind, id string, cats []catalog.Catalog, items []catalog.Item,
	rels []catalog.Release, orders []order.Order) (purgeScope, bool) {

	sc := purgeScope{
		plan: purgePlan{Kind: kind, ID: id, Confirm: id,
			Catalogs: []string{}, Releases: []string{}, Products: []string{},
			StrippedFrom: []purgeStripped{}, StillOffered: []purgeStillOffered{},
			Orders: []string{}, KeptOrders: []purgeKeptOrder{},
			Processes: []string{}, Warnings: []string{}},
		products: map[string]bool{},
		orders:   map[string]bool{},
	}
	gone := map[string]bool{}    // catalogues
	fromRel := map[string]bool{} // releases of a catalogue that goes

	switch kind {
	case purgeCatalog:
		found := false
		for _, c := range cats {
			if c.ID == id {
				found = true
				break
			}
		}
		if !found {
			return sc, false
		}
		gone[id] = true
		sc.plan.Catalogs = append(sc.plan.Catalogs, id)
		for _, r := range rels {
			if r.CatalogID == id {
				fromRel[r.ID] = true
				sc.plan.Releases = append(sc.plan.Releases, r.ID)
			}
		}
		for _, it := range items {
			if it.HomeCatalog == id {
				sc.products[it.ID] = true
			}
		}
	case purgeProduct:
		for _, it := range items {
			if it.ID == id {
				sc.products[id] = true
			}
		}
		if !sc.products[id] {
			return sc, false
		}
	default:
		return sc, false
	}

	procs := map[string]bool{}
	for _, it := range items {
		if !sc.products[it.ID] {
			continue
		}
		sc.plan.Products = append(sc.plan.Products, it.ID)
		for _, p := range []string{it.ProvisionProcess, it.DeprovisionProcess, it.LifecycleProcess} {
			if p != "" && p != catalog.FulfilmentProcess {
				procs[p] = true
			}
		}
	}
	for p := range procs {
		sc.plan.Processes = append(sc.plan.Processes, p)
	}

	// The catalogues that stay: what they lose now, and what their published
	// release goes on offering until they are published again.
	newest := map[string]catalog.Release{}
	for _, r := range rels {
		if cur, ok := newest[r.CatalogID]; !ok || r.CreatedAt > cur.CreatedAt ||
			(r.CreatedAt == cur.CreatedAt && r.ID > cur.ID) {
			newest[r.CatalogID] = r
		}
	}
	for _, c := range cats {
		if gone[c.ID] {
			continue
		}
		if _, lost := catalog.StripProducts(c, sc.products); len(lost) > 0 {
			sc.plan.StrippedFrom = append(sc.plan.StrippedFrom, purgeStripped{Catalog: c.ID, Products: lost})
		}
		r, ok := newest[c.ID]
		if !ok {
			continue
		}
		var carried []string
		for _, it := range r.Items {
			if sc.products[it.ID] {
				carried = append(carried, it.ID)
			}
		}
		if len(carried) > 0 {
			sort.Strings(carried)
			sc.plan.StillOffered = append(sc.plan.StillOffered,
				purgeStillOffered{Catalog: c.ID, Release: r.ID, Products: carried})
		}
	}

	for _, o := range orders {
		var on []string
		for _, l := range o.Lines {
			if sc.products[l.ItemID] {
				on = append(on, l.ItemID)
			}
		}
		switch {
		case fromRel[o.ReleaseID] || (len(o.Lines) > 0 && len(on) == len(o.Lines)):
			sc.orders[o.ID] = true
			sc.deleted = append(sc.deleted, o)
			sc.plan.Orders = append(sc.plan.Orders, o.ID)
		case len(on) > 0:
			sc.kept = append(sc.kept, o)
			sc.plan.KeptOrders = append(sc.plan.KeptOrders, purgeKeptOrder{Order: o.ID, Products: purgeDistinct(on)})
		}
	}

	sort.Strings(sc.plan.Releases)
	sort.Strings(sc.plan.Products)
	sort.Strings(sc.plan.Processes)
	sort.Strings(sc.plan.Orders)
	sort.Slice(sc.plan.StrippedFrom, func(a, b int) bool {
		return sc.plan.StrippedFrom[a].Catalog < sc.plan.StrippedFrom[b].Catalog
	})
	sort.Slice(sc.plan.StillOffered, func(a, b int) bool {
		return sc.plan.StillOffered[a].Catalog < sc.plan.StillOffered[b].Catalog
	})
	sort.Slice(sc.plan.KeptOrders, func(a, b int) bool {
		return sc.plan.KeptOrders[a].Order < sc.plan.KeptOrders[b].Order
	})
	return sc, true
}

func purgeDistinct(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// purgeHold is one hold the purge takes out of the inventory.
type purgeHold struct{ principal, item string }

// purgeWork is the engine half of a purge: the running instances it cancels and
// the holds it removes.
type purgeWork struct {
	instances []uint64
	holds     []purgeHold
}

// workOf finds the engine state a purge reaches, off the loop: both questions walk
// a population that grows with the installation — every running instance, every
// hold — and the single writer does not do scans (ADR-0239).
//
// An instance is found two ways. The order records the instances it started on a
// line (ADR-0416), which is the certain answer for provisioning, lifecycle and
// approval processes. The fulfilment orchestration is not recorded on a line, and
// orders placed before instances were recorded name none, so every running
// instance whose orderId names an order that goes is cancelled too.
func (s *Server) workOf(sc purgeScope) (purgeWork, error) {
	recorded := map[uint64]bool{}
	for _, o := range sc.deleted {
		for _, l := range o.Lines {
			for _, in := range l.Instances {
				recorded[in.Key] = true
			}
		}
	}
	for _, o := range sc.kept {
		for _, l := range o.Lines {
			if !sc.products[l.ItemID] {
				continue
			}
			for _, in := range l.Instances {
				recorded[in.Key] = true
			}
		}
	}

	var w purgeWork
	err := s.readOffLoop(func(rv *state.ReadView, _ defIndex) error {
		if err := rv.ActiveProcessInstances(func(key uint64, _ *model.ProcessInstanceValue) error {
			if recorded[key] {
				w.instances = append(w.instances, key)
				return nil
			}
			if len(sc.orders) == 0 {
				return nil
			}
			hit := false
			if err := rv.VariablesOfScope(key, func(v *model.VariableValue) error {
				if v.Name == progressOrderVar {
					if id, ok := nativeVar(v).(string); ok && sc.orders[id] {
						hit = true
					}
				}
				return nil
			}); err != nil {
				return err
			}
			if hit {
				w.instances = append(w.instances, key)
			}
			return nil
		}); err != nil {
			return err
		}
		return rv.Entitlements(func(v *model.EntitlementValue) error {
			if sc.products[v.ItemID] || (v.OrderID != "" && sc.orders[v.OrderID]) {
				w.holds = append(w.holds, purgeHold{principal: v.Principal, item: v.ItemID})
			}
			return nil
		})
	})
	return w, err
}

// explain fills in the counts and the warnings once the engine half is known.
func (p *purgePlan) explain(w purgeWork) {
	holders := map[string]bool{}
	for _, h := range w.holds {
		holders[h.principal] = true
	}
	p.Instances, p.Entitlements, p.Holders = len(w.instances), len(w.holds), len(holders)

	warn := func(format string, args ...any) { p.Warnings = append(p.Warnings, fmt.Sprintf(format, args...)) }
	if !p.Enabled {
		warn("This server was started with --catalogue-purge=false (ATLAS_CATALOGUE_PURGE=false), " +
			"so it does not delete catalogues and products for good.")
	}
	warn("Deleting is permanent: there is no undo and no recycle bin. Take a backup first " +
		"(GET /api/v1/backup/full) if any of it may be needed again. On a production " +
		"installation, withdraw a product instead and start the server with --catalogue-purge=false.")
	if n := len(p.Orders); n > 0 {
		warn("%d order(s) are deleted with their approvals and decisions; nobody can look them up again.", n)
	}
	if p.Instances > 0 {
		warn("%d running process instance(s) are cancelled. What they already did in a target "+
			"system is not undone.", p.Instances)
	}
	if p.Entitlements > 0 {
		warn("%d hold(s) of %d person(s) are taken out of the inventory. The access itself is "+
			"not removed from any target system: no deprovisioning process runs.", p.Entitlements, p.Holders)
		warn("The inventory's history keeps a row marked corrected for every hold removed, and " +
			"the event feed announces each removal to its subscribers.")
	}
	if n := len(p.KeptOrders); n > 0 {
		warn("%d order(s) stay because they carry other products; their lines on a deleted "+
			"product are withdrawn where they had not started.", n)
	}
	for _, so := range p.StillOffered {
		warn("Catalogue %s still offers %s in its published release %s until it is published again.",
			so.Catalog, strings.Join(so.Products, ", "), so.Release)
	}
	if len(p.Processes) > 0 {
		warn("The processes the products are bound to stay deployed: %s. Delete them separately "+
			"if nothing else uses them.", strings.Join(p.Processes, ", "))
	}
}

// loadPurgeScope reads the stores and scopes a purge. The order store is read
// whole, as the fulfilment repair reads it: it is the only way to find the orders
// placed against a release, and an administrator's purge is not the hot path.
func (s *Server) loadPurgeScope(kind purgeKind, id string) (purgeScope, bool, error) {
	var (
		sc    purgeScope
		found bool
		err   error
	)
	s.do(func() {
		var (
			cats   []catalog.Catalog
			items  []catalog.Item
			rels   []catalog.Release
			orders []order.Order
		)
		if cats, err = s.catalogStore.Catalogs(); err != nil {
			return
		}
		if items, err = s.catalogStore.Items(); err != nil {
			return
		}
		if rels, err = s.catalogStore.Releases(); err != nil {
			return
		}
		if orders, err = s.orderStore.All(); err != nil {
			return
		}
		sc, found = scopePurge(kind, id, cats, items, rels, orders)
	})
	sc.plan.Enabled = !s.cataloguePurgeOff
	return sc, found, err
}

// handleCatalogDeletion answers what deleting a catalogue for good would remove.
func (s *Server) handleCatalogDeletion(w http.ResponseWriter, r *http.Request) {
	s.previewPurge(w, r, purgeCatalog)
}

// handleProductDeletion answers what deleting a product for good would remove.
func (s *Server) handleProductDeletion(w http.ResponseWriter, r *http.Request) {
	s.previewPurge(w, r, purgeProduct)
}

// handleDeleteCatalog deletes a catalogue for good.
func (s *Server) handleDeleteCatalog(w http.ResponseWriter, r *http.Request) {
	s.purge(w, r, purgeCatalog)
}

// handleDeleteProduct deletes a product for good.
func (s *Server) handleDeleteProduct(w http.ResponseWriter, r *http.Request) {
	s.purge(w, r, purgeProduct)
}

func (s *Server) previewPurge(w http.ResponseWriter, r *http.Request, kind purgeKind) {
	id := r.PathValue("id")
	sc, found, err := s.loadPurgeScope(kind, id)
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read the catalogue: "+err.Error())
		return
	case !found:
		httpapi.Error(w, http.StatusNotFound, fmt.Sprintf("no %s %s", kind, id))
		return
	}
	work, err := s.workOf(sc)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read what is running and held: "+err.Error())
		return
	}
	sc.plan.explain(work)
	httpapi.JSON(w, http.StatusOK, sc.plan)
}

func (s *Server) purge(w http.ResponseWriter, r *http.Request, kind purgeKind) {
	id := r.PathValue("id")
	if s.cataloguePurgeOff {
		// Refused before anything is read: on a server that switched it off, the
		// answer must not depend on what exists.
		httpapi.Error(w, http.StatusForbidden, "deleting a "+string(kind)+" for good is switched "+
			"off on this server: it was started with --catalogue-purge=false (ATLAS_CATALOGUE_PURGE=false)")
		return
	}
	if r.URL.Query().Get("confirm") != id {
		httpapi.Error(w, http.StatusBadRequest, "nothing was deleted: repeat the "+string(kind)+
			" id as ?confirm="+id+" to delete it for good. GET …/deletion answers what would go")
		return
	}
	by := ""
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		by = p.UserID
	}
	if by == "" && s.authEnabled {
		httpapi.Error(w, http.StatusBadRequest,
			"a purge records who did it on every hold it removes, and this request has no identity")
		return
	}
	if by == "" {
		by = "single-user"
	}

	sc, found, err := s.loadPurgeScope(kind, id)
	switch {
	case err != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read the catalogue: "+err.Error())
		return
	case !found:
		httpapi.Error(w, http.StatusNotFound, fmt.Sprintf("no %s %s", kind, id))
		return
	}
	work, err := s.workOf(sc)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read what is running and held: "+err.Error())
		return
	}
	sc.plan.explain(work)

	at := s.now()
	if err := s.stopPurgedWork(work, by, at); err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "cancel the instances and remove the holds: "+err.Error())
		return
	}
	withdrawn, err := s.withdrawPurgedLines(sc, by, at)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "withdraw the lines of the orders that stay: "+err.Error())
		return
	}
	// The same two steps a cancelled order takes (ordercancel.go): nobody is left
	// with an approval for a line that no longer exists, and an orchestration that
	// waits for those lines is told they will not start.
	for _, o := range sc.kept {
		if items := withdrawn[o.ID]; len(items) > 0 {
			s.closeApprovalsOf(o.ID, items)
			s.wakeFulfilment(o.ID)
		}
	}
	if err := s.deletePurged(sc, at); err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "delete the records: "+err.Error()+
			". Run the purge again: what is already gone is not found twice")
		return
	}
	sc.plan.Done = true
	httpapi.JSON(w, http.StatusOK, sc.plan)
}

// stopPurgedWork cancels the running instances and takes the holds out of the
// inventory, and returns once the engine has made both durable (I2): the stores
// are written only after the log says the work has stopped.
//
// A hold is ended as corrected rather than returned: returned is evidence that
// somebody had the access until that moment, and a purge knows nothing of the
// sort. What it ends is Atlas's claim, which is exactly what corrected says.
func (s *Server) stopPurgedWork(w purgeWork, by string, at int64) error {
	if len(w.instances) == 0 && len(w.holds) == 0 {
		return nil
	}
	s.do(func() {
		for _, k := range w.instances {
			// An instance cancelled with its parent a moment ago is finished, and
			// there is nothing left to stop.
			if _, active, err := s.store.ActiveProcessInstance(k); err == nil && active {
				s.proc.CancelInstance(k)
			}
		}
		for _, h := range w.holds {
			s.proc.RevokeEntitlement(h.principal, h.item, at, model.EndCorrected, by)
		}
	})
	return s.drive()
}

// withdrawPurgedLines withdraws, in every order that stays, the lines on a deleted
// product that had not started, and answers which items each order lost. A line
// already provisioned keeps its outcome: it happened, and withdrawing is not how a
// grant is undone.
func (s *Server) withdrawPurgedLines(sc purgeScope, by string, at int64) (map[string][]string, error) {
	withdrawn := map[string][]string{}
	if len(sc.kept) == 0 {
		return withdrawn, nil
	}
	var err error
	s.do(func() {
		for _, k := range sc.kept {
			// Read again: the scope was taken a moment ago, and an order is written
			// by its fulfilment as it moves.
			o, ok, getErr := s.orderStore.Get(k.ID)
			if getErr != nil {
				err = getErr
				return
			}
			if !ok {
				continue
			}
			lines := make([]order.Line, len(o.Lines))
			copy(lines, o.Lines)
			var items []string
			for i, l := range lines {
				if !sc.products[l.ItemID] || !l.Status.Cancellable() {
					continue
				}
				next, cancelErr := order.Cancel(l, by, at, purgeReason)
				if cancelErr != nil {
					err = cancelErr
					return
				}
				lines[i] = next
				items = append(items, l.ItemID)
			}
			if len(items) == 0 {
				continue
			}
			o.Lines = order.Propagate(lines, o.Requires)
			o.UpdatedAt = at
			if err = s.orderStore.Save(o); err != nil {
				return
			}
			withdrawn[o.ID] = items
		}
	})
	return withdrawn, err
}

// deletePurged removes the records, the catalogue last (see the top of this file).
func (s *Server) deletePurged(sc purgeScope, at int64) error {
	var err error
	s.do(func() {
		for _, id := range sc.plan.Orders {
			if err = s.orderStore.Delete(id); err != nil {
				return
			}
		}
		// The catalogues that stay are stripped from their record as it stands
		// now, not from the one the scope read: a maintainer may have changed one
		// since, and writing the old record back would erase that change.
		for _, st := range sc.plan.StrippedFrom {
			c, ok, getErr := s.catalogStore.Catalog(st.Catalog)
			if getErr != nil {
				err = getErr
				return
			}
			if !ok {
				continue
			}
			next, lost := catalog.StripProducts(c, sc.products)
			if len(lost) == 0 {
				continue
			}
			next.Revision++
			next.UpdatedAt = at
			if err = s.catalogStore.SaveCatalog(next); err != nil {
				return
			}
		}
		refs := []string{}
		for _, id := range sc.plan.Products {
			if err = s.catalogStore.ClearPicture(id); err != nil {
				return
			}
			if err = s.catalogStore.DeleteItem(id); err != nil {
				return
			}
			refs = append(refs, "product:"+id)
		}
		for _, id := range sc.plan.Releases {
			if err = s.catalogStore.DeleteRelease(id); err != nil {
				return
			}
		}
		for _, id := range sc.plan.Catalogs {
			refs = append(refs, "catalog:"+id)
		}
		// The import's memory of these records goes before the catalogue, for the
		// same reason everything does: a failure here leaves the catalogue to run
		// the purge against again.
		if err = s.forgetImportBaselines(catalogueBaselineScope, refs); err != nil {
			return
		}
		for _, id := range sc.plan.Catalogs {
			if err = s.catalogStore.ClearLogo(id); err != nil {
				return
			}
			if err = s.catalogStore.DeleteCatalog(id); err != nil {
				return
			}
		}
	})
	return err
}
