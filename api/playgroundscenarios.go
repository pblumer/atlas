package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pblumer/atlas/api/httpapi"
	apiplayground "github.com/pblumer/atlas/api/playground"
)

// maxScenarioBytes caps a stored scenario. The dataset rides inside it, and a
// scenario is meant to be a reproducible input rather than a data warehouse: this
// is the same ceiling a CSV upload gets, so the two ways of giving the Playground
// a dataset agree about how much is too much.
const maxScenarioBytes = 16 << 20 // 16 MiB

// scenarioMeta is a scenario's listing entry — everything but the spec and the
// baseline, so a list stays small however large the datasets inside it are.
type scenarioMeta struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ProcessID   string `json:"processId"`
	ProjectID   string `json:"projectId,omitempty"`
	SavedAt     int64  `json:"savedAt"`
	HasBaseline bool   `json:"hasBaseline"`
	BaselineAt  int64  `json:"baselineAt,omitempty"`
}

func scenarioMetaOf(s playgroundScenario) scenarioMeta {
	return scenarioMeta{
		ID: s.ID, Name: s.Name, ProcessID: s.ProcessID, ProjectID: s.ProjectID,
		SavedAt: s.SavedAt, HasBaseline: s.Baseline != "", BaselineAt: s.BaselineAt,
	}
}

// handleSaveScenario creates or overwrites a saved Playground scenario. Saving the
// same id overwrites, so this doubles as update.
//
// The spec is validated for shape only — three JSON objects that the Playground's
// own endpoints could be handed. Whether the stub policy inside it names a real
// element is the sandbox's answer to give when the scenario runs, and deciding it
// twice is how the two answers start disagreeing.
func (s *Server) handleSaveScenario(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxScenarioBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var payload struct {
		ID        string                 `json:"id"`
		Name      string                 `json:"name"`
		ProcessID string                 `json:"processId"`
		ProjectID string                 `json:"projectId"`
		Spec      apiplayground.Scenario `json:"spec"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	id := strings.TrimSpace(payload.ID)
	if id == "" {
		httpapi.Error(w, http.StatusBadRequest, "scenario id is required")
		return
	}
	processID := strings.TrimSpace(payload.ProcessID)
	if processID == "" {
		httpapi.Error(w, http.StatusBadRequest, "a scenario belongs to a diagram: processId is required")
		return
	}
	if err := payload.Spec.Validate(); err != nil {
		httpapi.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	spec, err := json.Marshal(payload.Spec)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "spec: "+err.Error())
		return
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		name = id
	}
	rec := playgroundScenario{
		ID: id, Name: name, ProcessID: processID,
		ProjectID: strings.TrimSpace(payload.ProjectID),
		SavedAt:   time.Now().Unix(),
		Spec:      string(spec),
	}

	// Authoring a scenario inherits project scope (ADR-0071), like a form: an
	// overwrite needs editor on its current scope, and filing into a project needs
	// editor there.
	var (
		existing playgroundScenario
		existed  bool
		getErr   error
	)
	s.do(func() { existing, existed, getErr = s.playgroundScenarios.Get(id) })
	if getErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read scenario: "+getErr.Error())
		return
	}
	if existed {
		rec.OwnerID = existing.OwnerID
		// The baseline belongs to the scenario, not to this request: editing what a
		// scenario runs must not silently throw away the run it is measured against.
		rec.Baseline, rec.BaselineAt = existing.Baseline, existing.BaselineAt
		if code, msg := s.authorizeArtifact(r, existing.ProjectID, existing.OwnerID, ScopeRoleEditor); code != 0 {
			httpapi.Error(w, code, msg)
			return
		}
	} else {
		rec.OwnerID = s.artifactOwnerOnCreate(r)
	}
	if rec.ProjectID != "" {
		if code, msg := s.authorizeArtifact(r, rec.ProjectID, rec.OwnerID, ScopeRoleEditor); code != 0 {
			httpapi.Error(w, code, msg)
			return
		}
	}
	var saveErr error
	s.do(func() { saveErr = s.playgroundScenarios.Save(rec) })
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "save scenario: "+saveErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, scenarioMetaOf(rec))
}

// handleListScenarios lists saved scenarios (metadata only), most recently saved
// first. An optional ?processId= narrows the list to one diagram's — which is what
// the Modeler asks for, since a scenario is only meaningful beside the diagram it
// exercises.
func (s *Server) handleListScenarios(w http.ResponseWriter, r *http.Request) {
	filter := r.URL.Query().Get("processId")
	list := []scenarioMeta{}
	var loadErr error
	s.do(func() {
		var recs []playgroundScenario
		if recs, loadErr = s.playgroundScenarios.LoadAll(); loadErr != nil {
			return
		}
		var projs map[string]project
		if projs, loadErr = s.projectsByID(); loadErr != nil {
			return
		}
		for _, rec := range recs {
			if filter != "" && rec.ProcessID != filter {
				continue
			}
			if !s.canViewArtifact(r, rec.ProjectID, rec.OwnerID, projs) {
				continue
			}
			list = append(list, scenarioMetaOf(rec))
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list scenarios: "+loadErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, list)
}

// handleGetScenario returns one scenario with its spec and baseline — everything
// a client needs to run it and to set the result beside the last one.
func (s *Server) handleGetScenario(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.readScenario(w, r, ScopeRoleViewer)
	if !ok {
		return
	}
	out := map[string]any{
		"id": rec.ID, "name": rec.Name, "processId": rec.ProcessID,
		"projectId": rec.ProjectID, "savedAt": rec.SavedAt,
		"spec": json.RawMessage(rec.Spec),
	}
	if rec.Baseline != "" {
		out["baseline"] = json.RawMessage(rec.Baseline)
		out["baselineAt"] = rec.BaselineAt
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// handleSaveScenarioBaseline records a report as the scenario's baseline: the run
// the next one is measured against.
//
// It is a separate call from saving the scenario because the two are separate
// decisions. Editing what a run does should not throw away what it is compared
// with, and keeping a run as the new baseline is a thing somebody chooses after
// looking at it — a green run is worth keeping, a red one is what the baseline
// exists to catch.
func (s *Server) handleSaveScenarioBaseline(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.readScenario(w, r, ScopeRoleEditor)
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxScenarioBytes))
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}
	var report map[string]any
	if err := json.Unmarshal(body, &report); err != nil {
		httpapi.Error(w, http.StatusBadRequest, "a baseline is a run's report as a JSON object")
		return
	}
	rec.Baseline, rec.BaselineAt = string(body), time.Now().Unix()
	var saveErr error
	s.do(func() { saveErr = s.playgroundScenarios.Save(rec) })
	if saveErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "save baseline: "+saveErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, scenarioMetaOf(rec))
}

// handleDeleteScenario removes a scenario by id.
func (s *Server) handleDeleteScenario(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var (
		rec    playgroundScenario
		found  bool
		getErr error
	)
	s.do(func() { rec, found, getErr = s.playgroundScenarios.Get(id) })
	if getErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read scenario: "+getErr.Error())
		return
	}
	// An absent scenario still succeeds — deleting is idempotent — but a present one
	// needs editor on its scope (ADR-0071).
	if found {
		if code, msg := s.authorizeArtifact(r, rec.ProjectID, rec.OwnerID, ScopeRoleEditor); code != 0 {
			httpapi.Error(w, code, msg)
			return
		}
	}
	var delErr error
	s.do(func() { delErr = s.playgroundScenarios.Delete(id) })
	if delErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "delete scenario: "+delErr.Error())
		return
	}
	httpapi.JSON(w, http.StatusOK, map[string]any{"deleted": id})
}

// readScenario loads the scenario named in the path and checks the caller may
// reach it at the given scope role. It answers the request itself on any failure,
// reporting false — an unreadable scenario is a 404 rather than a 403, so its
// existence is not leaked to somebody who may not see it.
func (s *Server) readScenario(w http.ResponseWriter, r *http.Request, role string) (playgroundScenario, bool) {
	id := r.PathValue("id")
	var (
		rec     playgroundScenario
		found   bool
		loadErr error
	)
	s.do(func() { rec, found, loadErr = s.playgroundScenarios.Get(id) })
	switch {
	case loadErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read scenario: "+loadErr.Error())
		return playgroundScenario{}, false
	case !found:
		httpapi.Error(w, http.StatusNotFound, "no scenario with that id")
		return playgroundScenario{}, false
	}
	if code, msg := s.authorizeArtifact(r, rec.ProjectID, rec.OwnerID, role); code != 0 {
		httpapi.Error(w, code, msg)
		return playgroundScenario{}, false
	}
	return rec, true
}
