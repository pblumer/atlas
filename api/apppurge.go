package api

import (
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"sort"

	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/logging"
)

// Purging an application: deleting it together with everything it owns on this
// server (ADR-0468).
//
// A plain delete keeps the application's artifacts and lets them fall back to Not
// assigned (ADR-0034). A purge is the explicit other choice, and it is preceded by a
// preview that says what goes and what outside the application will stop working,
// because what it removes includes running instances and cannot be undone.

// deletionPreview is what GET /applications/{id}/deletion-preview answers.
type deletionPreview struct {
	ApplicationID string `json:"applicationId"`
	Name          string `json:"name"`

	// What the purge removes.
	Processes           []previewProcess  `json:"processes"`
	RunningInstances    int               `json:"runningInstances"`
	DecisionDeployments []previewDecision `json:"decisionDeployments"`
	Drafts              []previewArtifact `json:"drafts"`
	DecisionDrafts      []previewArtifact `json:"decisionDrafts"`
	DecisionReferences  []previewArtifact `json:"decisionReferences"`
	Forms               []previewArtifact `json:"forms"`
	Scenarios           []previewArtifact `json:"scenarios"`
	PanoramaModels      []previewArtifact `json:"panoramaModels"`
	InformationModels   []previewArtifact `json:"informationModels"`

	// What outside the application depends on what it removes: catalogue products
	// whose current release binds one of its processes, order lines that could still
	// start one, deployed processes of other applications that evaluate one of its
	// decisions. None of it stops the purge; all of it stops working.
	CatalogueProducts  []previewBinding `json:"catalogueProducts"`
	OrderLines         []previewOrders  `json:"orderLines"`
	DependentProcesses []decisionPinRef `json:"dependentProcesses"`

	// Refusal is why the purge would be refused, "" when it would go through.
	Refusal string `json:"refusal,omitempty"`
	// NeedsOperator says there are instances to cancel and the caller may not
	// cancel them.
	NeedsOperator bool `json:"needsOperator"`
}

type previewProcess struct {
	ProcessID string           `json:"processId"`
	Name      string           `json:"name,omitempty"`
	Versions  []previewVersion `json:"versions"`
}

type previewVersion struct {
	Key     uint64 `json:"key"`
	Version int32  `json:"version"`
	Running int    `json:"running"`
}

type previewDecision struct {
	Key       uint64   `json:"key"`
	Name      string   `json:"name,omitempty"`
	Decisions []string `json:"decisions"`
}

type previewArtifact struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type previewBinding struct {
	ProcessID string   `json:"processId"`
	Products  []string `json:"products"`
}

type previewOrders struct {
	ProcessID string                `json:"processId"`
	Lines     int                   `json:"lines"`
	ByProduct []order.FrozenBinding `json:"byProduct"`
}

// applicationInventory is everything one application owns on this server, read in
// one run-loop turn. Every list is design-time size, bounded by what the application
// holds, never by how many instances run.
type applicationInventory struct {
	app        project
	versions   []*deployment
	running    map[uint64]int
	decisions  []persistedDecision
	allDecided []persistedDecision
	drafts     []draft
	dmnDrafts  []dmnDraft
	refs       []dmnRef
	forms      []form
	scenarios  []playgroundScenario
	panorama   []previewArtifact
	infomodels []previewArtifact
}

// inventoryOnLoop reads what app owns. Must be called on the run loop.
func (s *Server) inventoryOnLoop(app project) (applicationInventory, error) {
	inv := applicationInventory{app: app, running: map[uint64]int{}}
	for _, key := range s.order {
		d := s.deployments[key]
		if d == nil || d.ProjectID != app.ID {
			continue
		}
		n, err := s.store.DefInstanceCount(key)
		if err != nil {
			return inv, err
		}
		inv.versions = append(inv.versions, d)
		inv.running[key] = n
	}
	var err error
	if inv.allDecided, err = s.decisionDeploys.LoadAll(); err != nil {
		return inv, err
	}
	for _, rec := range inv.allDecided {
		if rec.ApplicationID == app.ID {
			inv.decisions = append(inv.decisions, rec)
		}
	}
	drafts, err := s.drafts.LoadAll()
	if err != nil {
		return inv, err
	}
	for _, d := range drafts {
		if d.ProjectID == app.ID {
			inv.drafts = append(inv.drafts, d)
		}
	}
	dmnDrafts, err := s.dmnDrafts.LoadAll()
	if err != nil {
		return inv, err
	}
	for _, d := range dmnDrafts {
		if d.ProjectID == app.ID {
			inv.dmnDrafts = append(inv.dmnDrafts, d)
		}
	}
	refs, err := s.dmnrefs.LoadAll()
	if err != nil {
		return inv, err
	}
	for _, rec := range refs {
		if rec.ProjectID == app.ID {
			inv.refs = append(inv.refs, rec)
		}
	}
	forms, err := s.forms.LoadAll()
	if err != nil {
		return inv, err
	}
	for _, f := range forms {
		if f.ProjectID == app.ID {
			inv.forms = append(inv.forms, f)
		}
	}
	scenarios, err := s.playgroundScenarios.LoadAll()
	if err != nil {
		return inv, err
	}
	for _, sc := range scenarios {
		if sc.ProjectID == app.ID {
			inv.scenarios = append(inv.scenarios, sc)
		}
	}
	pano, err := s.panorama.ModelsForApplicationOnLoop(app.ID)
	if err != nil {
		return inv, err
	}
	for _, m := range pano {
		inv.panorama = append(inv.panorama, previewArtifact{ID: m.ID, Name: m.Name})
	}
	info, err := s.infomodel.ModelsForApplicationOnLoop(app.ID)
	if err != nil {
		return inv, err
	}
	for _, m := range info {
		inv.infomodels = append(inv.infomodels, previewArtifact{ID: m.ID, Name: m.Name})
	}
	return inv, nil
}

func (inv applicationInventory) versionKeys() []uint64 {
	keys := make([]uint64, 0, len(inv.versions))
	for _, d := range inv.versions {
		keys = append(keys, d.Key)
	}
	return keys
}

func (inv applicationInventory) totalRunning() int {
	n := 0
	for _, r := range inv.running {
		n += r
	}
	return n
}

func (inv applicationInventory) decisionKeys() map[uint64]bool {
	keys := make(map[uint64]bool, len(inv.decisions))
	for _, rec := range inv.decisions {
		keys[rec.Key] = true
	}
	return keys
}

// lastVersionsOnLoop names the process ids whose every deployed version belongs to
// the application: only for those does the purge end what an order or a catalogue
// starts, because an order starts the newest version of whatever survives. Must be
// called on the run loop.
func (s *Server) lastVersionsOnLoop(inv applicationInventory) map[string]bool {
	going := map[uint64]bool{}
	ids := map[string]bool{}
	for _, d := range inv.versions {
		going[d.Key] = true
		ids[d.ProcessID] = true
	}
	for key, d := range s.deployments {
		if !going[key] && ids[d.ProcessID] {
			delete(ids, d.ProcessID)
		}
	}
	return ids
}

// decisionVersionConflicts names the decisions whose current version the purge
// would remove while an older version survives it, as "id vN", sorted. Removing
// one would lower the per-decision version counter, which is derived from the
// surviving records, so the next deploy would mint a version number a survivor's
// release manifest and the editor already use for different content (ADR-0336).
// That failure is silent, which is why a purge does not override this guard.
func decisionVersionConflicts(going map[uint64]bool, all []persistedDecision) []string {
	goingMax, stayingMax := map[string]int32{}, map[string]int32{}
	for _, rec := range all {
		m := stayingMax
		if going[rec.Key] {
			m = goingMax
		}
		for _, d := range rec.Decisions {
			if d.Version > m[d.ID] {
				m[d.ID] = d.Version
			}
		}
	}
	var out []string
	for id, v := range goingMax {
		if staying := stayingMax[id]; staying > 0 && v > staying {
			out = append(out, fmt.Sprintf("%s v%d", id, v))
		}
	}
	sort.Strings(out)
	return out
}

func versionConflictRefusal(conflicts []string) string {
	return fmt.Sprintf("cannot purge: the application published the current version of %v, and older "+
		"versions survive in other applications. Removing it would make the next deploy reuse a "+
		"version number that is already spoken for — remove the newer versions elsewhere first, "+
		"or delete the application without purging (ADR-0336)", conflicts)
}

// decisionDependentsOnLoop names the deployed processes outside the application that
// would be left without a decision to evaluate: pinned to one of its decision
// deployments, or bound to latest of a decision whose every deployment goes and
// carrying no copy of it — the two refusals of a single decision delete, asked of
// the whole set at once (ADR-0336). Must be called on the run loop.
func (s *Server) decisionDependentsOnLoop(inv applicationInventory) []decisionPinRef {
	goingDecisions := inv.decisionKeys()
	if len(goingDecisions) == 0 {
		return nil
	}
	goingDefs := map[uint64]bool{}
	for _, d := range inv.versions {
		goingDefs[d.Key] = true
	}
	remaining := map[string]bool{}
	for _, rec := range inv.allDecided {
		if goingDecisions[rec.Key] {
			continue
		}
		for _, d := range rec.Decisions {
			remaining[d.ID] = true
		}
	}
	vanishing := map[string]bool{}
	for _, rec := range inv.decisions {
		for _, d := range rec.Decisions {
			if !remaining[d.ID] {
				vanishing[d.ID] = true
			}
		}
	}
	var out []decisionPinRef
	seen := map[string]bool{}
	add := func(d *deployment, id, binding string) {
		k := fmt.Sprintf("%d/%s", d.Key, id)
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, decisionPinRef{
			Key: d.Key, ProcessID: d.ProcessID, Name: d.Name, Version: d.Version, DecisionID: id, Binding: binding,
		})
	}
	for _, defKey := range s.order {
		d := s.deployments[defKey]
		if d == nil || d.cp == nil || goingDefs[defKey] {
			continue
		}
		for _, id := range d.cp.LatestBoundDecisions() {
			if pinned, ok := d.cp.PinnedDecisionKey(id); ok && goingDecisions[pinned] {
				add(d, id, pinBindingLatest)
				continue
			}
			if d.cp.LatestAtRuntime() && vanishing[id] && !s.dmnRegistry.Provides(d.Key, id) {
				add(d, id, pinBindingLatest)
			}
		}
		for _, ref := range d.cp.VersionBoundDecisions() {
			if pinned, ok := d.cp.VersionPinnedKey(ref.DecisionID, ref.Version); ok && goingDecisions[pinned] {
				add(d, ref.DecisionID, pinBindingVersion)
			}
		}
	}
	return out
}

// releaseBindingsOnLoop is releaseBindsOnLoop asked for several process ids at once:
// for each, the products whose catalogue's newest release binds it, sorted. Reads the
// catalogue store, so it runs on the run loop.
func (s *Server) releaseBindingsOnLoop(processIDs map[string]bool) (map[string][]string, error) {
	out := map[string][]string{}
	if s.catalogStore == nil || len(processIDs) == 0 {
		return out, nil
	}
	cats, err := s.catalogStore.Catalogs()
	if err != nil {
		return nil, err
	}
	seen := map[string]map[string]bool{}
	for _, c := range cats {
		rels, err := s.catalogStore.ReleasesOf(c.ID)
		if err != nil {
			return nil, err
		}
		if len(rels) == 0 {
			continue
		}
		newest := rels[0]
		for _, r := range rels[1:] {
			if r.CreatedAt > newest.CreatedAt {
				newest = r
			}
		}
		for _, it := range newest.Items {
			for _, op := range []string{catalog.OpProvision, catalog.OpDeprovision, catalog.OpChange} {
				pid := it.BindingFor(op).Process
				if !processIDs[pid] {
					continue
				}
				if seen[pid] == nil {
					seen[pid] = map[string]bool{}
				}
				seen[pid][it.ID] = true
			}
		}
	}
	for pid, items := range seen {
		for id := range items {
			out[pid] = append(out[pid], id)
		}
		sort.Strings(out[pid])
	}
	return out, nil
}

// handleApplicationDeletionPreview is GET /api/v1/applications/{id}/deletion-preview.
// It needs what the purge needs — the owner role on the application — because it
// exists to be read by the person about to purge it.
func (s *Server) handleApplicationDeletionPreview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var (
		prev      deletionPreview
		notFound  bool
		forbidden int
		fmsg      string
		opErr     error
		ending    map[string]bool
	)
	s.do(func() {
		rec, ok, err := s.projects.Get(id)
		if err != nil {
			opErr = err
			return
		}
		if !ok {
			notFound = true
			return
		}
		if code, msg := s.checkProjectRole(r, rec, ScopeRoleOwner); code != 0 {
			forbidden, fmsg = code, msg
			return
		}
		if code, msg := protectedGuard(rec); code != 0 {
			forbidden, fmsg = code, msg
			return
		}
		inv, err := s.inventoryOnLoop(rec)
		if err != nil {
			opErr = err
			return
		}
		prev = previewOf(inv)
		prev.DependentProcesses = s.decisionDependentsOnLoop(inv)
		if conflicts := decisionVersionConflicts(inv.decisionKeys(), inv.allDecided); len(conflicts) > 0 {
			prev.Refusal = versionConflictRefusal(conflicts)
		}
		ending = s.lastVersionsOnLoop(inv)
		bindings, err := s.releaseBindingsOnLoop(ending)
		if err != nil {
			opErr = err
			return
		}
		for _, pid := range slices.Sorted(maps.Keys(bindings)) {
			prev.CatalogueProducts = append(prev.CatalogueProducts, previewBinding{ProcessID: pid, Products: bindings[pid]})
		}
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read application: "+opErr.Error())
		return
	case notFound:
		httpapi.Error(w, http.StatusNotFound, "no project with that id")
		return
	case forbidden != 0:
		httpapi.Error(w, forbidden, fmsg)
		return
	}
	// The order store is a full scan, so it is read with the loop free (ADR-0427,
	// ADR-0239), once for every process the purge ends.
	if s.orderStore != nil && len(ending) > 0 {
		orders, err := s.orderStore.All()
		if err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "check orders: "+err.Error())
			return
		}
		for _, pid := range slices.Sorted(maps.Keys(ending)) {
			counts := order.LinesStarting(orders, pid)
			if len(counts) == 0 {
				continue
			}
			total := 0
			for _, c := range counts {
				total += c.Lines
			}
			prev.OrderLines = append(prev.OrderLines, previewOrders{ProcessID: pid, Lines: total, ByProduct: counts})
		}
	}
	prev.NeedsOperator = prev.RunningInstances > 0 && !s.actsAsOperator(httpapi.PrincipalFrom(r.Context()))
	httpapi.JSON(w, http.StatusOK, prev)
}

// previewOf lists an inventory the way the preview shows it.
func previewOf(inv applicationInventory) deletionPreview {
	prev := deletionPreview{
		ApplicationID: inv.app.ID, Name: inv.app.Name,
		Processes: []previewProcess{}, DecisionDeployments: []previewDecision{},
		Drafts: []previewArtifact{}, DecisionDrafts: []previewArtifact{}, DecisionReferences: []previewArtifact{},
		Forms: []previewArtifact{}, Scenarios: []previewArtifact{},
		PanoramaModels: append([]previewArtifact{}, inv.panorama...), InformationModels: append([]previewArtifact{}, inv.infomodels...),
		CatalogueProducts: []previewBinding{}, OrderLines: []previewOrders{}, DependentProcesses: []decisionPinRef{},
	}
	byID := map[string]int{}
	for _, d := range inv.versions {
		i, ok := byID[d.ProcessID]
		if !ok {
			i = len(prev.Processes)
			byID[d.ProcessID] = i
			prev.Processes = append(prev.Processes, previewProcess{ProcessID: d.ProcessID, Name: d.Name})
		}
		prev.Processes[i].Versions = append(prev.Processes[i].Versions,
			previewVersion{Key: d.Key, Version: d.Version, Running: inv.running[d.Key]})
		prev.RunningInstances += inv.running[d.Key]
	}
	for _, rec := range inv.decisions {
		pd := previewDecision{Key: rec.Key, Name: rec.ModelName}
		for _, d := range rec.Decisions {
			pd.Decisions = append(pd.Decisions, fmt.Sprintf("%s v%d", d.ID, d.Version))
		}
		prev.DecisionDeployments = append(prev.DecisionDeployments, pd)
	}
	for _, d := range inv.drafts {
		prev.Drafts = append(prev.Drafts, previewArtifact{ID: d.ProcessID, Name: d.Name})
	}
	for _, d := range inv.dmnDrafts {
		prev.DecisionDrafts = append(prev.DecisionDrafts, previewArtifact{ID: d.ID, Name: d.Name})
	}
	for _, rec := range inv.refs {
		prev.DecisionReferences = append(prev.DecisionReferences, previewArtifact{ID: rec.ID, Name: rec.Name})
	}
	for _, f := range inv.forms {
		prev.Forms = append(prev.Forms, previewArtifact{ID: f.ID, Name: f.Name})
	}
	for _, sc := range inv.scenarios {
		prev.Scenarios = append(prev.Scenarios, previewArtifact{ID: sc.ID, Name: sc.Name})
	}
	return prev
}

// purgeApplication is DELETE /api/v1/applications/{id}?purge=true.
//
// The order is what makes a failure recoverable. Runtime goes first — the instances
// and the deployed versions — then the decision deployments those versions were
// pinned to, then the design-time artifacts, and the application record last, the
// way a plain delete removes its releases before itself: a purge that stops
// half-way leaves the application in place with less in it, and the same request
// finishes the work, because every step is idempotent.
func (s *Server) purgeApplication(w http.ResponseWriter, r *http.Request, id string) {
	var (
		inv       applicationInventory
		keys      []uint64
		notFound  bool
		forbidden int
		fmsg      string
		opErr     error
		conflicts []string
	)
	s.do(func() {
		rec, ok, err := s.projects.Get(id)
		if err != nil {
			opErr = err
			return
		}
		if !ok {
			notFound = true
			return
		}
		if code, msg := s.checkProjectRole(r, rec, ScopeRoleOwner); code != 0 {
			forbidden, fmsg = code, msg
			return
		}
		if code, msg := protectedGuard(rec); code != 0 {
			forbidden, fmsg = code, msg
			return
		}
		if inv, opErr = s.inventoryOnLoop(rec); opErr != nil {
			return
		}
		keys = inv.versionKeys()
		conflicts = decisionVersionConflicts(inv.decisionKeys(), inv.allDecided)
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read application: "+opErr.Error())
		return
	case forbidden != 0:
		httpapi.Error(w, forbidden, fmsg)
		return
	case notFound:
		w.WriteHeader(http.StatusNoContent) // idempotent, as the plain delete
		return
	case len(conflicts) > 0:
		httpapi.Error(w, http.StatusConflict, versionConflictRefusal(conflicts))
		return
	}
	mayCancel := s.actsAsOperator(httpapi.PrincipalFrom(r.Context()))
	if running := inv.totalRunning(); running > 0 && !mayCancel {
		httpapi.Error(w, http.StatusForbidden, fmt.Sprintf(
			"cannot purge: %d instance(s) are running, and cancelling them needs the operator role", running))
		return
	}

	cancelled, err := s.removeDeployments(keys, mayCancel)
	if err != nil {
		removeDeploymentsError(w, err)
		return
	}

	// The rest is read again in the turn that removes it: a publish or a save between
	// the two turns would otherwise survive the purge, or lose what it was pinned to.
	var raced bool
	s.do(func() {
		fresh, err := s.inventoryOnLoop(inv.app)
		if err != nil {
			opErr = err
			return
		}
		if len(fresh.versions) > 0 {
			raced = true
			return
		}
		if conflicts = decisionVersionConflicts(fresh.decisionKeys(), fresh.allDecided); len(conflicts) > 0 {
			return
		}
		opErr = s.purgeContentsOnLoop(fresh)
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "purge application: "+opErr.Error())
		return
	case raced:
		httpapi.Error(w, http.StatusConflict,
			"the application was deployed again while it was being purged; repeat the request")
		return
	case len(conflicts) > 0:
		httpapi.Error(w, http.StatusConflict, versionConflictRefusal(conflicts))
		return
	}
	audit(r, logging.ApplicationPurged,
		"an application was deleted with everything it owned on this server",
		slog.String("applicationId", id),
		slog.String("name", inv.app.Name),
		slog.Int("versionsRemoved", len(inv.versions)),
		slog.Int("instancesCancelled", cancelled),
		slog.Int("decisionDeploymentsRemoved", len(inv.decisions)))
	w.WriteHeader(http.StatusNoContent)
}

// purgeContentsOnLoop removes what is left of an application once its deployed
// versions are gone: its decision deployments, its design-time artifacts and models,
// and finally the application itself. Must be called on the run loop.
//
// Every list here is design-time size, so the turn is bounded by what one
// application holds. The model files are removed on the loop for the same reason —
// one or two files per model the application references — and before the
// references, so a failure leaves the references that a retry needs to find them.
func (s *Server) purgeContentsOnLoop(inv applicationInventory) error {
	id := inv.app.ID
	if err := s.removeDecisionDeploymentsOnLoop(inv.decisionKeys()); err != nil {
		return err
	}
	if err := s.removeOrphanedModelsOnLoop(id, inv.refs); err != nil {
		return err
	}
	for _, rec := range inv.refs {
		if err := s.dmnrefs.Delete(rec.ID); err != nil {
			return err
		}
	}
	for _, d := range inv.dmnDrafts {
		if err := s.dmnDrafts.Delete(d.ID); err != nil {
			return err
		}
	}
	for _, d := range inv.drafts {
		if err := s.drafts.Delete(d.ProcessID); err != nil {
			return err
		}
	}
	for _, f := range inv.forms {
		if err := s.forms.Delete(f.ID); err != nil {
			return err
		}
	}
	for _, sc := range inv.scenarios {
		if err := s.playgroundScenarios.Delete(sc.ID); err != nil {
			return err
		}
	}
	if err := s.panorama.DeleteForApplicationOnLoop(id); err != nil {
		return err
	}
	if err := s.infomodel.DeleteForApplicationOnLoop(id); err != nil {
		return err
	}
	return s.deleteApplicationRecordOnLoop(id)
}

// removeDecisionDeploymentsOnLoop removes a set of decision deployments in one turn:
// each record first and then its registration, durable before visible (I2), and the
// per-decision version counter rebuilt once from what survives — the guard
// decisionVersionConflicts has already established that this lowers no counter a
// survivor holds. Must be called on the run loop.
func (s *Server) removeDecisionDeploymentsOnLoop(keys map[uint64]bool) error {
	if len(keys) == 0 {
		return nil
	}
	all, err := s.decisionDeploys.LoadAll()
	if err != nil {
		return err
	}
	removed := map[uint64]bool{}
	// Rebuilt from whatever was removed, also when a later removal fails, so the
	// live counter never disagrees with the records a restart would read.
	defer s.recountDecisionVersionsWithout(all, removed)
	for _, rec := range all {
		if !keys[rec.Key] {
			continue
		}
		if err := s.decisionDeploys.Delete(decisionKeyName(rec.Key)); err != nil {
			return err
		}
		s.dmnRegistry.UndeployDecision(rec.Key)
		removed[rec.Key] = true
	}
	return nil
}

// removeOrphanedModelsOnLoop removes the stored model file behind each of the
// application's references that no reference outside it points at. A server whose
// models live in a remote temis service keeps none, and removes none. Must be called
// on the run loop.
func (s *Server) removeOrphanedModelsOnLoop(appID string, refs []dmnRef) error {
	dir, local := s.dmnModelDir()
	if !local || len(refs) == 0 {
		return nil
	}
	all, err := s.dmnrefs.LoadAll()
	if err != nil {
		return err
	}
	elsewhere := map[string]bool{}
	for _, rec := range all {
		if rec.ProjectID != appID {
			elsewhere[rec.ModelRef] = true
		}
	}
	done := map[string]bool{}
	for _, rec := range refs {
		h := sanitizeHandle(rec.ModelRef)
		if h == "" || h != rec.ModelRef || elsewhere[h] || done[h] {
			continue
		}
		done[h] = true
		if _, err := removeStoredModel(dir, h); err != nil {
			return err
		}
	}
	return nil
}
