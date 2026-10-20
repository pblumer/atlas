package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/api/importplan"
)

// Importing an application's constants (ADR-0467, amended): the values a package
// suggests, compared with what this installation holds and with what the last import
// suggested. A value it lacks is set. One it holds as the last import brought it,
// and the package moved on, is updated. One an administrator set here while the
// package suggests what it suggested before is kept — INT and PROD rightly differ,
// and that difference is exactly the case worth not asking about every time. Only a
// value both sides moved is a question, decided with ?overwrite= or ?keep= as on the
// other import routes.
//
// Setting a value is an administrator's (PUT …/constants). This route is a modeler's
// with a role on the application, because comparing and remembering are not setting:
// a caller who is not an administrator gets the plan and the list of values an
// administrator still has to set, and nothing is written for them — not even the
// base, so the suggestion is offered again next time instead of being taken as
// settled.

type constantsImportResp struct {
	ApplicationID string                `json:"applicationId"`
	DryRun        bool                  `json:"dryRun,omitempty"`
	Artifacts     []importplan.Artifact `json:"artifacts"`
	// Set names the constants this import wrote.
	Set []string `json:"set"`
	// NotSet names those it would have written and did not, because the caller is
	// not an administrator.
	NotSet []string `json:"notSet,omitempty"`
}

// handleImportApplicationConstants serves POST /api/v1/applications/{id}/constants/import.
func (s *Server) handleImportApplicationConstants(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, code, msg := s.authorizeProject(r, id, ScopeRoleEditor); code != 0 {
		httpapi.Error(w, code, msg)
		return
	}
	decide, err := importplan.Parse(r.URL.Query())
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	offered, _, err := s.readHeldConstantsBody(r)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	admin := s.isAdmin(r)
	updatedBy := ""
	if p := httpapi.PrincipalFrom(r.Context()); p != nil {
		updatedBy = p.UserID
	}

	resp := constantsImportResp{ApplicationID: id, DryRun: decide.DryRun, Artifacts: []importplan.Artifact{}, Set: []string{}}
	var (
		undecided []string
		opErr     error
	)
	s.do(func() {
		prev, _, err := s.appConstants.Get(id)
		if err != nil {
			opErr = err
			return
		}
		bases, err := s.importBaselines(applicationBaselineScope(id))
		if err != nil {
			opErr = err
			return
		}
		held := map[string]heldConstant{}
		for _, c := range prev.Constants {
			held[c.Name] = c
		}
		reconciled := map[string]string{}
		var toSet []heldConstant
		for _, c := range offered {
			ref := "constant:" + c.Name
			h, ok := held[c.Name]
			in := importplan.Hash([]byte(c.Type), []byte(c.Value))
			status := importplan.Classify(ok, importplan.Hash([]byte(h.Type), []byte(h.Value)), in, bases[ref])
			outcome := decide.Outcome(ref, status)
			resp.Artifacts = append(resp.Artifacts, importplan.Artifact{Ref: ref, Name: c.Name, Status: status, Outcome: outcome})
			switch {
			case outcome == importplan.Undecided:
				undecided = append(undecided, ref)
			case importplan.Writes(outcome) && !admin:
				resp.NotSet = append(resp.NotSet, c.Name)
			case importplan.Writes(outcome):
				toSet = append(toSet, c)
				reconciled[ref] = in
			default:
				reconciled[ref] = in
			}
		}
		if decide.DryRun || len(undecided) > 0 {
			return
		}
		if len(toSet) > 0 {
			merged := append([]heldConstant(nil), prev.Constants...)
			for _, c := range toSet {
				replaced := false
				for i := range merged {
					if merged[i].Name == c.Name {
						merged[i], replaced = c, true
					}
				}
				if !replaced {
					merged = append(merged, c)
				}
				resp.Set = append(resp.Set, c.Name)
			}
			sort.Slice(merged, func(i, j int) bool { return merged[i].Name < merged[j].Name })
			list, vals, err := cleanHeldConstants(merged)
			if err != nil {
				opErr = err
				return
			}
			rec := applicationConstants{ApplicationID: id, Constants: list, UpdatedAt: time.Now().Unix(), UpdatedBy: updatedBy}
			if opErr = s.appConstants.Save(rec); opErr != nil {
				return
			}
			s.proc.SetApplicationConstants(id, vals)
			if opErr = s.recordConstantChanges(r, id, diffHeldConstants(prev.Constants, list)); opErr != nil {
				return
			}
		}
		opErr = s.recordImportBaselines(applicationBaselineScope(id), reconciled)
	})
	switch {
	case opErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "import constants: "+opErr.Error())
	case !decide.DryRun && len(undecided) > 0:
		httpapi.JSON(w, http.StatusConflict, map[string]any{
			"error":     sourceUndecided{refs: undecided}.Error(),
			"undecided": undecided, "plan": resp,
		})
	default:
		httpapi.JSON(w, http.StatusOK, resp)
	}
}
