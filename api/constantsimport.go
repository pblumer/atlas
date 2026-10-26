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
		plan := planConstantImport(offered, prev.Constants, bases, decide, admin)
		resp.Artifacts = append(resp.Artifacts, plan.artifacts...)
		resp.NotSet, undecided = plan.notSet, plan.undecided
		if decide.DryRun || len(undecided) > 0 {
			return
		}
		set, err := s.setImportedConstants(id, prev.Constants, plan.toSet, updatedBy, func(appID string, changes []constantChange) error {
			return s.recordConstantChanges(r, appID, changes)
		})
		if err != nil {
			opErr = err
			return
		}
		resp.Set = append(resp.Set, set...)
		opErr = s.recordImportBaselines(applicationBaselineScope(id), plan.reconciled)
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

// constantImportPlan is how the constants an import offers compare with those the
// application holds here and with what the last import offered, and what the
// decisions make of each. The constants route and the source import, whose tree may
// carry constants.json, plan alike, so a value is settled by one rule whichever way
// it travelled.
type constantImportPlan struct {
	artifacts []importplan.Artifact
	undecided []string
	// toSet is what is to be written: the outcome writes, and the caller is an
	// administrator.
	toSet []heldConstant
	// notSet names what would be written for an administrator and is not, because
	// the caller is not one.
	notSet []string
	// reconciled is the hash of each constant the import settled, written or kept:
	// the base the next import compares with. A value left unset for want of the
	// role is not among them, so it is offered again.
	reconciled map[string]string
}

// planConstantImport compares each offered constant (ref constant:NAME) with the one
// held under its name and with its base. A constant held here that the import does not
// offer is not touched, and not listed.
func planConstantImport(offered, held []heldConstant, bases map[string]string, decide importplan.Decisions, admin bool) constantImportPlan {
	byName := make(map[string]heldConstant, len(held))
	for _, c := range held {
		byName[c.Name] = c
	}
	plan := constantImportPlan{artifacts: []importplan.Artifact{}, reconciled: map[string]string{}}
	for _, c := range offered {
		ref := "constant:" + c.Name
		h, ok := byName[c.Name]
		in := importplan.Hash([]byte(c.Type), []byte(c.Value))
		status := importplan.Classify(ok, importplan.Hash([]byte(h.Type), []byte(h.Value)), in, bases[ref])
		outcome := decide.Outcome(ref, status)
		plan.artifacts = append(plan.artifacts, importplan.Artifact{Ref: ref, Name: c.Name, Status: status, Outcome: outcome})
		switch {
		case outcome == importplan.Undecided:
			plan.undecided = append(plan.undecided, ref)
		case importplan.Writes(outcome) && !admin:
			plan.notSet = append(plan.notSet, c.Name)
		case importplan.Writes(outcome):
			plan.toSet = append(plan.toSet, c)
			plan.reconciled[ref] = in
		default:
			plan.reconciled[ref] = in
		}
	}
	return plan
}

// setImportedConstants writes what an import settled into the constants the
// application holds: each one replaces the value of its name or joins the set, the
// set is checked and persisted, and only then handed to the processor (I2), with the
// change recorded through audit as a PUT records it. It names what it set, nothing
// when toSet is empty. Run loop only.
func (s *Server) setImportedConstants(appID string, held, toSet []heldConstant, updatedBy string, audit func(string, []constantChange) error) ([]string, error) {
	if len(toSet) == 0 {
		return nil, nil
	}
	merged := append([]heldConstant(nil), held...)
	set := make([]string, 0, len(toSet))
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
		set = append(set, c.Name)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].Name < merged[j].Name })
	list, vals, err := cleanHeldConstants(merged)
	if err != nil {
		return nil, err
	}
	rec := applicationConstants{ApplicationID: appID, Constants: list, UpdatedAt: time.Now().Unix(), UpdatedBy: updatedBy}
	if err := s.appConstants.Save(rec); err != nil {
		return nil, err
	}
	if s.proc != nil {
		s.proc.SetApplicationConstants(appID, vals)
	}
	if audit != nil {
		if err := audit(appID, diffHeldConstants(held, list)); err != nil {
			return nil, err
		}
	}
	return set, nil
}
