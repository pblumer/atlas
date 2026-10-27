package api

import (
	"cmp"
	"net/http"
	"slices"
	"strings"

	"github.com/pblumer/atlas/api/archsketch"
	"github.com/pblumer/atlas/api/catalog"
	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/order"
	"github.com/pblumer/atlas/compiler"
)

// The business and strategy layers of a release's architecture (ADR-0495 slice D).
//
// Both are taken in the same run-loop step as the rest of the snapshot. The business
// layer reads the release's compiled processes — their user tasks and lanes — and the
// newest published release of each catalogue, for the products a release process
// carries out. The strategy layer reads the capability register for the capabilities
// a release process or a Worker the release names realises, and the value streams
// whose stages use them.

// collectBusiness adds one process's business process and the roles its user tasks
// and lanes name. A candidate group is recorded as the model writes it; an assignee is
// counted, never named, because a literal assignee is an Atlas account (ADR-0495 §5).
func collectBusiness(b *archsketch.Business, cp *compiler.CompiledProcess, processID, name string) {
	p := archsketch.BusinessProcess{ProcessID: processID, Name: name}
	lanes := map[string]bool{}
	for id := range int32(cp.NodeCount()) {
		if path := slices.DeleteFunc(cp.LanePath(id), func(n string) bool { return n == "" }); len(path) > 0 {
			lanes[strings.Join(path, " / ")] = true
		}
		n := cp.Node(id)
		if n == nil || n.Type != compiler.TypeUserTask {
			continue
		}
		d := cp.UserTask(n.Detail)
		switch {
		case d.AssigneeExpr != nil || d.CandidateGroupsExpr != nil:
			p.RuntimeTasks++
		case cp.Intern(d.Assignee) != "":
			p.PersonTasks++
		case cp.Intern(d.CandidateGroups) != "":
			p.GroupTasks++
			for _, group := range strings.Split(cp.Intern(d.CandidateGroups), ",") {
				if group = strings.TrimSpace(group); group != "" {
					b.Roles = append(b.Roles, archsketch.BusinessRole{Name: group, Kind: archsketch.RoleCandidateGroup,
						UsedBy: []archsketch.Use{{ProcessID: processID, ElementID: cp.ElementBpmnId(id)}}})
				}
			}
		default:
			p.OpenTasks++
		}
	}
	for lane := range lanes {
		b.Roles = append(b.Roles, archsketch.BusinessRole{Name: lane, Kind: archsketch.RoleLane, UsedBy: []archsketch.Use{{ProcessID: processID}}})
	}
	b.Processes = append(b.Processes, p)
}

// businessPart completes the business layer with the catalogue products a release
// process carries out, as each catalogue last published them: what a person could
// order when the release was taken. Nil when the catalogue store cannot be read — a
// release recorded as carrying out no product because the read failed would be a
// claim. With the catalogue switched off there are no products to carry out. Run-loop
// goroutine only.
func (s *Server) businessPart(b *archsketch.Business, shipped map[string]bool) *archsketch.Business {
	if s.catalogStore == nil || s.catalogueOff {
		return b
	}
	catalogs, err := s.catalogStore.Catalogs()
	if err != nil {
		return nil
	}
	names := map[string]string{}
	for _, c := range catalogs {
		names[c.ID] = catalogWords(c.Texts, c.Languages, c.ID)
	}
	for _, c := range catalogs {
		rels, err := s.catalogStore.ReleasesOf(c.ID)
		if err != nil {
			return nil
		}
		if len(rels) == 0 {
			continue
		}
		for _, it := range rels[0].Items {
			if p, ok := releaseProduct(it, shipped); ok {
				p.CatalogName = names[p.CatalogID]
				p.Name = catalogWords(it.Texts, c.Languages, it.ID)
				b.Products = append(b.Products, p)
			}
		}
	}
	return b
}

// releaseProduct is the product an item is to the release, when a release process
// carries out one of its operations.
func releaseProduct(it catalog.Item, shipped map[string]bool) (archsketch.Product, bool) {
	p := archsketch.Product{ID: it.ID, CatalogID: it.HomeCatalog, State: string(it.State)}
	if it.UsesLifecycleProcess() {
		if !shipped[it.LifecycleProcess] {
			return p, false
		}
		p.Bindings = []archsketch.ProductBinding{{Op: "lifecycle", ProcessID: it.LifecycleProcess}}
		for _, a := range it.ActionList() {
			action := archsketch.ProductAction{Key: a.Key, Message: a.Message, Effect: a.Effect, Triggers: slices.Clone(a.Triggers)}
			for _, outcome := range []string{catalog.OutcomeCompleted, catalog.OutcomeRejected, catalog.OutcomeFailed} {
				action.Outcomes = append(action.Outcomes, archsketch.ProductOutcome{Outcome: outcome, EventType: order.EventTypeOf(it.ID, a, outcome)})
			}
			p.Actions = append(p.Actions, action)
		}
		return p, true
	}
	for _, op := range []string{catalog.OpProvision, catalog.OpDeprovision} {
		if b := it.BindingFor(op); b.Bound() && shipped[b.Process] {
			p.Bindings = append(p.Bindings, archsketch.ProductBinding{Op: op, ProcessID: b.Process, Message: b.Message})
		}
	}
	return p, len(p.Bindings) > 0
}

// strategyPart is the strategy layer: each capability a release process realises — by
// the application's portable key and the process id (ADR-0134, ADR-0305) — or a Worker
// the release names, and each value stream a stage of which uses one. The owner is
// recorded by name and role; their contact, their account and who confirmed or edited
// the record are not (ADR-0495 §5). Nil when the register or the application cannot be
// read. Run-loop goroutine only: the register's stores are read directly, as the
// landscape does.
func (s *Server) strategyPart(appID string, shipped map[string]bool, deps []archsketch.Dependency) *archsketch.Strategy {
	if s.capabilityRecords == nil || s.valueStreamRecords == nil {
		return &archsketch.Strategy{}
	}
	proj, ok, err := s.projects.Get(appID)
	if err != nil {
		return nil
	}
	key := ""
	if ok {
		key = proj.Key
	}
	workers := map[string]bool{}
	for _, d := range deps {
		if d.Kind == archsketch.DependencyWorker {
			workers[d.Name] = true
		}
	}
	caps, err := s.capabilityRecords.LoadAll()
	if err != nil {
		return nil
	}
	streams, err := s.valueStreamRecords.LoadAll()
	if err != nil {
		return nil
	}
	st := &archsketch.Strategy{}
	realized := map[string]bool{}
	for _, c := range caps {
		var by []archsketch.Realization
		for _, r := range c.Realizations {
			switch {
			case r.Kind == archsketch.RealizedByProcess && key != "" && r.ApplicationKey == key && shipped[r.ProcessID]:
				by = append(by, archsketch.Realization{Kind: archsketch.RealizedByProcess, ProcessID: r.ProcessID})
			case r.Kind == archsketch.RealizedByWorker && workers[r.WorkerRef]:
				by = append(by, archsketch.Realization{Kind: archsketch.RealizedByWorker, Worker: r.WorkerRef})
			}
		}
		if len(by) == 0 {
			continue
		}
		cp := archsketch.Capability{Key: c.Key, Name: cmp.Or(c.Name, c.Key), State: c.State, ConfirmedAt: c.Confirmation.At, RealizedBy: by}
		if c.Owner.Name != "" || c.Owner.Role != "" {
			cp.Owner = &archsketch.Owner{Name: c.Owner.Name, Role: c.Owner.Role}
		}
		for _, k := range c.KPIs {
			cp.KPIs = append(cp.KPIs, archsketch.KPI{Name: k.Name, Metric: k.Metric, Goal: k.Goal, Direction: k.Direction})
		}
		for _, l := range c.SLAs {
			cp.SLAs = append(cp.SLAs, archsketch.SLA{Name: l.Name, Metric: l.Metric, Threshold: l.Threshold, Window: l.Window, Scope: l.Scope})
		}
		st.Capabilities = append(st.Capabilities, cp)
		realized[c.Key] = true
	}
	for _, v := range streams {
		vs := archsketch.ValueStream{Key: v.Key, Name: cmp.Or(v.Name, v.Key)}
		uses := false
		for _, stage := range v.Stages {
			used := []string{}
			for _, k := range stage.Capabilities {
				if realized[k] {
					used = append(used, k)
				}
			}
			uses = uses || len(used) > 0
			vs.Stages = append(vs.Stages, archsketch.Stage{Key: stage.Key, Name: cmp.Or(stage.Name, stage.Key), Capabilities: used})
		}
		if !uses {
			continue
		}
		if v.Owner.Name != "" || v.Owner.Role != "" {
			vs.Owner = &archsketch.Owner{Name: v.Owner.Name, Role: v.Owner.Role}
		}
		st.ValueStreams = append(st.ValueStreams, vs)
	}
	return st
}

// withholdProducts is the business layer as this reader may see it: a product only for
// a reader who may maintain its home catalogue, the check the starmap applies
// (ADR-0396); a product whose catalogue is gone is withheld. Run-loop goroutine only.
func (s *Server) withholdProducts(r *http.Request, b archsketch.Business) archsketch.Business {
	p := httpapi.PrincipalFrom(r.Context())
	return b.Redacted(func(catalogID string) bool {
		if s.catalogStore == nil || s.catalogs == nil {
			return false
		}
		c, ok, err := s.catalogStore.Catalog(catalogID)
		return err == nil && ok && s.catalogs.MayMaintain(c, p)
	})
}
