package api

import (
	"errors"
	"net/http"
	"sort"
	"strconv"

	"github.com/pblumer/atlas/api/httpapi"
	"github.com/pblumer/atlas/dmn"
)

// A business rule task names a decision and a binding, and the binding decides which
// model answers (ADR-0423): the newest decision deployment when the task runs, a
// version the author picked, or the model bundled with the process. The Modeler's
// decision peek shows that model rather than whatever the decision's reference holds
// today, because "what does this step decide" is a question about the model that
// answers it (ADR-draft-decision-peek).
//
// Two perspectives, because the Modeler shows two kinds of diagram:
//
//   - a **draft** has not been deployed. Its task is resolved as a deploy now would
//     resolve it, and as the worker would once it runs: a version to the decision
//     deployment holding it, latest to the newest decision deployment or, without one,
//     to the stored model the deploy would bundle, and deployment to that bundle.
//   - a **deployed definition** is resolved by the worker's own function
//     (dmn.TaskModelOf), against the pins and the bundle it was deployed with.

// boundDecisionResp is which model a business rule task's binding resolves to, and
// that model's graph and logic.
type boundDecisionResp struct {
	DecisionID string `json:"decisionId"`
	// Perspective is "draft" or "definition" (see above).
	Perspective string `json:"perspective"`
	ProcessKey  uint64 `json:"processKey,omitempty"`
	Binding     string `json:"binding"`
	// Version is the version a version-bound task names.
	Version int32 `json:"version,omitempty"`
	// Source names what answers:
	//   - "decisionDeployment" — a decision deployed in its own right: Key, and the
	//     version it holds of the decision in DeployedVersion;
	//   - "bundle" — the model deployed with the definition (definition perspective),
	//     registered under the definition's own Key;
	//   - "storedModel" — the stored model a deploy now would bundle (draft perspective):
	//     Ref names the reference it is read through;
	//   - "newestModel" — a definition deployed before pinning, whose task resolves the
	//     newest model of either kind when it is activated (ADR-0063);
	//   - "none" — nothing would answer; Reason says why.
	Source          string `json:"source"`
	Key             uint64 `json:"key,omitempty"`
	DeployedVersion int32  `json:"deployedVersion,omitempty"`
	DeployedAt      int64  `json:"deployedAt,omitempty"`
	// ApplicationID and ModelName say where a decision deployment was published from
	// and what its model is called — what the peek files a stored model under, and
	// names it, when it creates one from the deployment.
	ApplicationID string `json:"applicationId,omitempty"`
	ModelName     string `json:"modelName,omitempty"`
	// Fallback marks a latest binding that found no decision deployment and answers
	// from the bundled model instead.
	Fallback bool `json:"fallback,omitempty"`
	// Ref is the stored model's reference, for storedModel. Hidden is set instead of
	// the graph when that reference is filed where the caller may not look.
	Ref    *boundRef `json:"ref,omitempty"`
	Hidden bool      `json:"hidden,omitempty"`
	// Reason, for "none": "versionNotDeployed" (Versions lists what is deployed),
	// "noModel", "notADecisionTask" or "notResolved" (Message says why).
	Reason   string          `json:"reason,omitempty"`
	Message  string          `json:"message,omitempty"`
	Versions []int32         `json:"versions,omitempty"`
	Graph    *dmn.ModelGraph `json:"graph,omitempty"`
	Logic    *dmn.ModelLogic `json:"logic,omitempty"`
}

type boundRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ModelRef string `json:"modelRef"`
}

// handleDecisionBinding answers GET /api/v1/decisions/{id}/binding. Query:
// binding (latest | version | deployment) and version for a draft's task, or
// processKey and elementId for a task of a deployed definition, whose compiled task
// is then the authority and binding/version are ignored.
//
// Run-loop state (the definition, the version index, the decision records, the
// references) is read in one turn; compiling a stored model happens off it, as the
// reference graph read does.
func (s *Server) handleDecisionBinding(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	q := r.URL.Query()
	resp := boundDecisionResp{DecisionID: id, Perspective: "draft", Binding: q.Get("binding")}
	if resp.Binding == "" {
		resp.Binding = "latest"
	}
	if resp.Binding != "latest" && resp.Binding != "version" && resp.Binding != "deployment" {
		httpapi.Error(w, http.StatusBadRequest, "binding must be latest, version or deployment")
		return
	}
	if v := q.Get("version"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n <= 0 {
			httpapi.Error(w, http.StatusBadRequest, "version must be a positive number")
			return
		}
		resp.Version = int32(n)
	}
	if resp.Binding == "version" && resp.Version == 0 {
		httpapi.Error(w, http.StatusBadRequest, "a version binding needs a version")
		return
	}
	var processKey uint64
	if v := q.Get("processKey"); v != "" {
		k, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			httpapi.Error(w, http.StatusBadRequest, "invalid processKey")
			return
		}
		processKey = k
	}
	elementID := q.Get("elementId")

	var (
		found    bool // the definition exists and the caller may read it
		tm       dmn.TaskModel
		tmErr    error
		refs     []dmnRef
		projs    map[string]project
		loadErr  error
		decRec   persistedDecision
		versions []int32
	)
	p := httpapi.PrincipalFrom(r.Context())
	s.do(func() {
		if processKey != 0 {
			d, ok := s.deployments[processKey]
			if !ok || !definitionReadable(p, d) {
				return
			}
			found = true
			tm, tmErr = dmn.TaskModelOf(s.dmnRegistry, d.cp, elementID)
		} else {
			switch resp.Binding {
			case "version":
				if key, ok := s.decisionVersionKeys[id][resp.Version]; ok {
					resp.Source, resp.Key, resp.DeployedVersion = "decisionDeployment", key, resp.Version
				} else {
					resp.Source, resp.Reason = "none", "versionNotDeployed"
					for v := range s.decisionVersionKeys[id] {
						versions = append(versions, v)
					}
				}
			case "latest":
				if key, ok := s.dmnRegistry.LatestDecisionKey(id); ok {
					resp.Source, resp.Key, resp.DeployedVersion = "decisionDeployment", key, s.decisionVersionOf(key, id)
				}
			}
			if resp.Source == "" {
				// latest without a decision deployment, or deployment: the bundle a deploy
				// now would carry, chosen from the references in the order the deploy reads them.
				if refs, loadErr = s.dmnrefs.LoadAll(); loadErr == nil {
					projs, loadErr = s.projectsByID()
				}
			}
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list dmn references: "+loadErr.Error())
		return
	}

	if processKey != 0 {
		if !found {
			httpapi.Error(w, http.StatusNotFound, "no deployment with that key")
			return
		}
		resp.Perspective, resp.ProcessKey = "definition", processKey
		resp.Binding, resp.Version = tm.Binding, tm.Version
		if tm.DecisionID != "" {
			resp.DecisionID = tm.DecisionID
		}
		switch {
		case errors.Is(tmErr, dmn.ErrNotADecisionTask):
			resp.Source, resp.Reason = "none", "notADecisionTask"
		case tmErr != nil:
			resp.Source, resp.Reason, resp.Message = "none", "notResolved", tmErr.Error()
		case !tm.Resolved:
			resp.Source = "newestModel"
		case tm.Key == processKey:
			resp.Source, resp.Key = "bundle", tm.Key
			resp.Fallback = tm.Binding == "latest"
		default:
			resp.Source, resp.Key = "decisionDeployment", tm.Key
		}
		if resp.Source == "decisionDeployment" {
			s.do(func() { resp.DeployedVersion = s.decisionVersionOf(tm.Key, resp.DecisionID) })
		}
	} else if resp.Source == "" {
		ref, err := s.bundledRefFor(r, refs, id)
		if err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "resolve dmn model: "+err.Error())
			return
		}
		if ref == nil {
			resp.Source, resp.Reason = "none", "noModel"
		} else {
			resp.Source = "storedModel"
			resp.Fallback = resp.Binding == "latest"
			resp.Ref = &boundRef{ID: ref.ID, Name: ref.Name, ModelRef: ref.ModelRef}
			if !s.canViewArtifact(r, ref.ProjectID, ref.OwnerID, projs) {
				resp.Hidden, resp.Ref = true, nil
			}
		}
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	resp.Versions = versions

	// The model itself. A deployed one is read from the registry, where it was
	// compiled when it was deployed; a stored one is compiled here, off the loop.
	switch resp.Source {
	case "decisionDeployment", "bundle", "newestModel":
		key := resp.Key // 0 for newestModel: the registry's newest-model fallback
		if g, ok := s.dmnRegistry.Graph(key, resp.DecisionID); ok {
			resp.Graph = &g
		}
		if l, ok := s.dmnRegistry.Logic(key, resp.DecisionID); ok {
			resp.Logic = &l
		}
		if resp.Source == "decisionDeployment" {
			s.do(func() {
				var ok bool
				if decRec, ok, _ = s.decisionDeploys.load(resp.Key); ok {
					resp.DeployedAt, resp.ApplicationID, resp.ModelName = decRec.DeployedAt, decRec.ApplicationID, decRec.ModelName
				}
			})
		}
	case "storedModel":
		if resp.Hidden {
			break
		}
		g, err := s.dmnValidator.Graph(r.Context(), resp.Ref.ModelRef)
		if err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "resolve dmn model: "+err.Error())
			return
		}
		l, err := s.dmnValidator.Logic(r.Context(), resp.Ref.ModelRef)
		if err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "resolve dmn model: "+err.Error())
			return
		}
		resp.Graph, resp.Logic = &g, &l
	}
	httpapi.JSON(w, http.StatusOK, resp)
}

// bundledRefFor is the reference whose stored model a deploy now would bundle for the
// decision: the first, in the order the deploy reads them, whose model compiles and
// answers to the id under any of its names — the choice coverModelsReport makes.
// nil when none does.
func (s *Server) bundledRefFor(r *http.Request, refs []dmnRef, decisionID string) (*dmnRef, error) {
	for i := range refs {
		res, err := s.dmnValidator.Validate(r.Context(), refs[i].ModelRef)
		if err != nil {
			return nil, err
		}
		if !res.Valid {
			continue
		}
		for _, names := range [][]string{res.Decisions, res.Aliases, res.Services} {
			for _, n := range names {
				if n == decisionID {
					return &refs[i], nil
				}
			}
		}
	}
	return nil, nil
}
