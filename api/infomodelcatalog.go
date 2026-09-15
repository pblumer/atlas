package api

import (
	"net/http"
	"strings"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/infomodel"
)

// The class catalogue and the where-used reading
// (ADR-0338).
//
// Both live here rather than in api/infomodel, for the reason the derived model and
// the difference do: the answer is read from two places at once — the information
// models, which the service owns, and the application's compiled processes, which only
// the server can reach. The computation itself is in the infomodel package, pure and
// testable; these handlers gather the two sides on one loop turn and authorize them.
//
// Both are reads. Nothing is created, seeded or stored, and no usage is remembered
// between two calls (ADR-0310's reason: the names are the mechanism, so there is no
// second identity to keep).

// catalogRow is one catalogue entry with the label the server owns. An information
// model knows which application it belongs to; what that application is *called* is
// the server's fact, so it is added here rather than duplicated into the model.
type catalogRow struct {
	infomodel.CatalogEntry
	ApplicationName string `json:"applicationName,omitempty"`
}

// usageResponse is one class's whole usage, with the same label added.
type usageResponse struct {
	infomodel.Usage
	ApplicationName string `json:"applicationName,omitempty"`
}

// handleInfomodelCatalog lists every class of every information model the caller may
// view — business objects, value types and enumerations together — each with the
// counts of where it is used.
//
// One list across applications on purpose: a vocabulary maintained per application is
// still one vocabulary to the person maintaining it, and the question that opens this
// page ("do we model an Order twice?") cannot be asked of one application at a time.
// ?applicationId= narrows it for the person who is only working in one.
func (s *Server) handleInfomodelCatalog(w http.ResponseWriter, r *http.Request) {
	applicationID := strings.TrimSpace(r.URL.Query().Get("applicationId"))
	var (
		entries []infomodel.CatalogEntry
		names   map[string]string
		opErr   error
	)
	s.do(func() {
		var models []infomodel.Model
		if models, opErr = s.infomodel.ModelsOnLoop(); opErr != nil {
			return
		}
		var visible []infomodel.Model
		visible, names = s.visibleModelsOnLoop(r, models, applicationID)
		entries = infomodel.Catalog(visible, func(id string) []infomodel.Process {
			return s.usageProcessesOnLoop(id)
		})
	})
	if opErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read information models: "+opErr.Error())
		return
	}
	rows := make([]catalogRow, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, catalogRow{CatalogEntry: e, ApplicationName: names[e.ApplicationID]})
	}
	httpapi.JSON(w, http.StatusOK, rows)
}

// handleInfomodelUsage answers where one class is used and how: every process that
// declares, reads, writes or stores it, with the element, the member and the state —
// and every place the vocabulary itself uses it.
//
// Addressed within its model (`?class=Order`) exactly as the JSON Schema projection
// is, because a class name is unique within a model and is the string every process
// writes. A class nobody models is a 404 rather than an empty usage: "used nowhere" is
// a claim about something that exists.
func (s *Server) handleInfomodelUsage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	class := strings.TrimSpace(r.URL.Query().Get("class"))
	if class == "" {
		httpapi.Error(w, http.StatusBadRequest, "class is required — a usage is read for one class of this model")
		return
	}
	var (
		usage   infomodel.Usage
		appName string
		status  int
		message string
	)
	s.do(func() {
		models, err := s.infomodel.ModelsOnLoop()
		if err != nil {
			status, message = http.StatusInternalServerError, "read information models: "+err.Error()
			return
		}
		var owner *infomodel.Model
		for i := range models {
			if models[i].ID == id {
				owner = &models[i]
				break
			}
		}
		if owner == nil {
			status, message = http.StatusNotFound, "no such information model"
			return
		}
		proj, ok, err := s.projects.Get(owner.ApplicationID)
		if err != nil {
			status, message = http.StatusInternalServerError, "read application: "+err.Error()
			return
		}
		// A model whose application is gone is hidden exactly as the listing hides it,
		// and hidden the same way an application the caller cannot see is: the model's
		// existence never leaks through a difference in the refusal.
		if !ok {
			status, message = http.StatusNotFound, "no such information model"
			return
		}
		if code, _ := s.checkProjectRole(r, proj, ScopeRoleViewer); code != 0 {
			status, message = http.StatusNotFound, "no such information model"
			return
		}
		appName = proj.Name
		found := false
		if usage, found = infomodel.UsageOf(models, id, class, s.usageProcessesOnLoop(owner.ApplicationID)); !found {
			status, message = http.StatusNotFound, "no class of that name in this information model"
		}
	})
	if status != 0 {
		httpapi.Error(w, status, message)
		return
	}
	httpapi.JSON(w, http.StatusOK, usageResponse{Usage: usage, ApplicationName: appName})
}

// visibleModelsOnLoop filters models to the applications the caller may view and
// returns those applications' names alongside. It runs inside a loop turn: the
// project store is the run loop's, and the role check is pure.
//
// The access rule is the one the information-model service applies — the application
// scope, not a second ACL (ADR-0128) — and a model whose application is missing or
// hidden is simply absent, which is how the model listing behaves too. A project the
// store cannot read is hidden as well: failing closed is the only safe direction, and
// this store is the loop's own in-memory one, so there is no realistic error to report.
func (s *Server) visibleModelsOnLoop(r *http.Request, models []infomodel.Model, applicationID string) ([]infomodel.Model, map[string]string) {
	names := map[string]string{}
	decided := map[string]bool{}
	visible := make([]infomodel.Model, 0, len(models))
	for _, m := range models {
		if applicationID != "" && m.ApplicationID != applicationID {
			continue
		}
		if !decided[m.ApplicationID] {
			decided[m.ApplicationID] = true
			if proj, ok, err := s.projects.Get(m.ApplicationID); err == nil && ok {
				if code, _ := s.checkProjectRole(r, proj, ScopeRoleViewer); code == 0 {
					names[m.ApplicationID] = proj.Name
				}
			}
		}
		if _, allowed := names[m.ApplicationID]; !allowed {
			continue
		}
		visible = append(visible, m)
	}
	return visible, names
}

// usageProcessesOnLoop is one application's processes as a usage reading needs them:
// the same set the derived model and the data-flow checks read — deployed, active,
// latest version — with the deployed name and version a row shows beside each.
func (s *Server) usageProcessesOnLoop(applicationID string) []infomodel.Process {
	cps := s.applicationProcessesOnLoop(applicationID)
	out := make([]infomodel.Process, 0, len(cps))
	for _, cp := range cps {
		p := infomodel.Process{Compiled: cp}
		if d, ok := s.deployments[cp.Key]; ok {
			p.Name, p.Version = d.Name, d.Version
		}
		out = append(out, p)
	}
	return out
}
