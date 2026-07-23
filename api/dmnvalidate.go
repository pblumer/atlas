package api

import "net/http"

// dmnRefValidationResp reports the deploy-time resolution + validation of one DMN
// reference: whether its temis model resolved and, if so, whether it compiles.
type dmnRefValidationResp struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	ModelRef  string   `json:"modelRef"`
	Resolved  bool     `json:"resolved"`
	Valid     bool     `json:"valid"`
	ModelName string   `json:"modelName,omitempty"`
	Decisions []string `json:"decisions,omitempty"`
	Message   string   `json:"message,omitempty"`
}

// projectValidationResp is the project-level preflight: every DMN reference in
// the project resolved and validated. OK is true only if all are valid (a
// project with no references passes trivially), so a deploy can gate on it.
type projectValidationResp struct {
	ID         string                 `json:"id"`
	Name       string                 `json:"name"`
	OK         bool                   `json:"ok"`
	References []dmnRefValidationResp `json:"references"`
}

// handleValidateDmnRef resolves one DMN reference's temis model and validates it
// — the deploy-time check that a stored handle names a real, compilable decision
// model (ADR-0034 Phase 2). Resolution and compilation touch no engine or store
// state, so they run off the run-loop goroutine (only the record read is on it).
func (s *Server) handleValidateDmnRef(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var (
		rec    dmnRef
		ok     bool
		getErr error
	)
	s.do(func() { rec, ok, getErr = s.dmnrefs.get(id) })
	switch {
	case getErr != nil:
		writeError(w, http.StatusInternalServerError, "read dmn reference: "+getErr.Error())
		return
	case !ok:
		writeError(w, http.StatusNotFound, "no dmn reference with that id")
		return
	}
	res, err := s.dmnValidator.Validate(r.Context(), rec.ModelRef)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "resolve dmn model: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, dmnRefValidationResp{
		ID:        rec.ID,
		Name:      rec.Name,
		ModelRef:  rec.ModelRef,
		Resolved:  res.Resolved,
		Valid:     res.Valid,
		ModelName: res.ModelName,
		Decisions: res.Decisions,
		Message:   res.Message,
	})
}

// handleValidateProject is the deploy-time preflight for a whole project: it
// resolves and validates every DMN reference filed into the project and reports
// per-reference results plus an overall OK flag. A future project bundle-deploy
// runs this gate first and refuses to deploy when OK is false.
func (s *Server) handleValidateProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var (
		proj            project
		ok              bool
		getErr, loadErr error
		refs            []dmnRef
	)
	s.do(func() {
		proj, ok, getErr = s.projects.get(id)
		if getErr != nil || !ok {
			return
		}
		var all []dmnRef
		if all, loadErr = s.dmnrefs.loadAll(); loadErr != nil {
			return
		}
		for _, rec := range all {
			if rec.ProjectID == id {
				refs = append(refs, rec)
			}
		}
	})
	switch {
	case getErr != nil:
		writeError(w, http.StatusInternalServerError, "read project: "+getErr.Error())
		return
	case !ok:
		writeError(w, http.StatusNotFound, "no project with that id")
		return
	case loadErr != nil:
		writeError(w, http.StatusInternalServerError, "list dmn references: "+loadErr.Error())
		return
	}
	out := projectValidationResp{ID: proj.ID, Name: proj.Name, OK: true, References: []dmnRefValidationResp{}}
	for _, rec := range refs {
		res, err := s.dmnValidator.Validate(r.Context(), rec.ModelRef)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "resolve dmn model: "+err.Error())
			return
		}
		if !res.Valid {
			out.OK = false
		}
		out.References = append(out.References, dmnRefValidationResp{
			ID:        rec.ID,
			Name:      rec.Name,
			ModelRef:  rec.ModelRef,
			Resolved:  res.Resolved,
			Valid:     res.Valid,
			ModelName: res.ModelName,
			Decisions: res.Decisions,
			Message:   res.Message,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
