package api

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/pblumer/atlas/compiler"

	"github.com/pblumer/atlas/api/httpapi"
)

// Decision deployments (ADR-0319).
//
// A DMN model an application publishes becomes a runtime artifact of its own: a
// durable, versioned record in the decisions store, compiled into the DMN registry
// under a key from the same global definition key space process definitions use.
// That is what lets an application consisting of nothing but `eligibility.dmn`
// publish something the engine can run, and what a process's `latest` reference is
// pinned to when the process is deployed.
//
// Everything here runs on the run loop: it allocates keys, advances version
// counters, writes the durable records and registers the compiled models — all
// single-writer state (I3). The compiling and validating that precedes it happens
// off the loop, in the bundle's preflight.

// pinDecisions resolves every latest-bound decision reference of a freshly
// compiled definition to an exact decision deployment, records the answer on the
// definition, and returns it for the durable deployment record.
//
// The rule, per reference:
//
//  1. the newest *decision deployment* providing that decision id, if there is one;
//  2. otherwise this process deployment's own key — the model bundled with it.
//
// Case 2 is what keeps a process whose decision was never published on its own
// behaving as it always has: `latest` and `deployment` then name the same model,
// the equivalence ADR-0063 already noted for a decision deployed once.
//
// It must run on the run loop (it reads the registry) and before the definition is
// handed to the processor, which is where deployModel calls it.
func (s *Server) pinDecisions(cp *compiler.CompiledProcess) []persistedDecisionBinding {
	refs := cp.LatestBoundDecisions()
	if len(refs) == 0 {
		// Still a pinned deployment: the policy is the deployment's, not the map's.
		cp.PinDecisions(nil)
		return nil
	}
	pins := make(map[string]uint64, len(refs))
	out := make([]persistedDecisionBinding, 0, len(refs))
	for _, id := range refs {
		key, ok := s.dmnRegistry.LatestDecisionKey(id)
		if !ok {
			key = cp.Key
		}
		pins[id] = key
		out = append(out, persistedDecisionBinding{DecisionID: id, Key: key})
	}
	cp.PinDecisions(pins)
	return out
}

// decisionDeployment is one DMN model about to be deployed as a decision: what the
// bundle's off-loop preflight resolved, ready for the on-loop write.
type decisionDeployment struct {
	artifactID string
	modelRef   string
	modelName  string
	decisions  []string
	xml        []byte
}

// deployDecisions publishes an application's DMN models as decision deployments,
// in one run-loop turn: every record is written durably first, and only then is
// any model registered with the DMN registry.
//
// That order is invariant I2 for this path. A decision becomes evaluable — and
// claimable by a release manifest — only after its record is on disk, so a publish
// that fails partway leaves nothing behind that the next restart would not also
// produce. The reverse order would make a decision runnable that a restart forgets.
//
// It returns the records it wrote, in deployment order, for the publish response
// and the release manifest. Compilation was already proven off the loop by the
// bundle's DMN preflight, so a registry refusal here is a should-not-happen; it is
// reported rather than swallowed, and the durable record stays — which is the safe
// side of the boundary, not the unsafe one.
func (s *Server) deployDecisions(models []decisionDeployment, appID, deployedBy string, deployedAt int64) ([]persistedDecision, error) {
	if len(models) == 0 {
		return nil, nil
	}
	// Spend the keys durably before any record claims one
	// (ADR-0339): one reservation for
	// the whole batch, so a publish of five decisions costs one write and not five.
	next, err := s.reserveKeys(len(models))
	if err != nil {
		return nil, err
	}
	recs := make([]persistedDecision, 0, len(models))
	for _, m := range models {
		key := next
		next++
		entries := make([]deployedDecisionEntry, 0, len(m.decisions))
		for _, id := range m.decisions {
			version := s.decisionVersions[id] + 1
			s.decisionVersions[id] = version
			entries = append(entries, deployedDecisionEntry{ID: id, Version: version})
		}
		recs = append(recs, persistedDecision{
			Key:           key,
			ApplicationID: appID,
			ArtifactID:    m.artifactID,
			ModelRef:      m.modelRef,
			ResourceName:  decisionResourceName(m.modelRef, m.decisions),
			ModelName:     m.modelName,
			Decisions:     entries,
			Checksum:      modelChecksum(m.xml),
			DeployedAt:    deployedAt,
			DeployedBy:    deployedBy,
			XML:           string(m.xml),
		})
	}
	// Durable first — all of them — so a write failure registers nothing.
	for _, rec := range recs {
		if err := s.decisionDeploys.Save(rec); err != nil {
			return nil, err
		}
	}
	for i, rec := range recs {
		if err := s.dmnRegistry.DeployDecision(rec.Key, []byte(models[i].xml)); err != nil {
			return nil, fmt.Errorf("register decision %s: %w", rec.ResourceName, err)
		}
	}
	return recs, nil
}

// decisionResourceName is the file-shaped name a decision deployment carries into
// a release manifest: the resolver handle with the extension an author would
// recognize it by.
//
// A publish always has a handle — a reference without one is dropped before it gets
// here, since there would be nothing to resolve. A decision deployed straight from
// the editor may have none, because the record carries its own XML and therefore
// needs no model behind it (ADR-0322). Rather than claim a
// model that does not exist, such a deployment names itself from its own first
// decision id.
func decisionResourceName(modelRef string, decisions []string) string {
	if modelRef != "" {
		return modelRef + ".dmn"
	}
	for _, id := range decisions {
		if h := sanitizeHandle(id); h != "" {
			return h + ".dmn"
		}
	}
	return "decision.dmn"
}

// deployedDecisionResp is one deployed decision as the API reports it: the runtime
// identity a business rule task binds to, and where it came from. One row per
// decision, so a model providing two decisions reports two rows sharing a key —
// the key is the deployment, the id and version are the decision's own lineage.
type deployedDecisionResp struct {
	Key           uint64 `json:"key"`
	DecisionID    string `json:"decisionId"`
	Version       int32  `json:"version"`
	ApplicationID string `json:"applicationId,omitempty"`
	ModelRef      string `json:"modelRef,omitempty"`
	ResourceName  string `json:"resourceName,omitempty"`
	ModelName     string `json:"modelName,omitempty"`
	Checksum      string `json:"checksum,omitempty"`
	DeployedAt    int64  `json:"deployedAt"`
	DeployedBy    string `json:"deployedBy,omitempty"`
	// Current says this is the newest deployed version of its decision id — the one
	// a process deployed now would pin its latest-bound reference to. A superseded
	// version stays deployed and addressable: processes pinned to it keep running
	// against exactly it, which is the point of the whole record.
	Current bool `json:"current"`
	// PinnedBy are the deployed process definitions that resolved a latest-bound
	// reference to this deployment's key. It is what stops it being deleted
	// (ADR-0336), so it is reported before the act
	// rather than only in the refusal after it. Filled by the listing; absent on the
	// rows a deploy echoes back, which are new and can be pinned by nothing.
	PinnedBy []decisionPinRef `json:"pinnedBy,omitempty"`
}

// decisionResponses flattens decision-deployment records into one row per
// decision, newest deployment first, marking the current version of each id.
func decisionResponses(recs []persistedDecision) []deployedDecisionResp {
	newest := map[string]int32{}
	for _, rec := range recs {
		for _, d := range rec.Decisions {
			if d.Version > newest[d.ID] {
				newest[d.ID] = d.Version
			}
		}
	}
	out := []deployedDecisionResp{}
	for _, rec := range recs {
		for _, d := range rec.Decisions {
			out = append(out, deployedDecisionResp{
				Key:           rec.Key,
				DecisionID:    d.ID,
				Version:       d.Version,
				ApplicationID: rec.ApplicationID,
				ModelRef:      rec.ModelRef,
				ResourceName:  rec.ResourceName,
				ModelName:     rec.ModelName,
				Checksum:      rec.Checksum,
				DeployedAt:    rec.DeployedAt,
				DeployedBy:    rec.DeployedBy,
				Current:       d.Version == newest[d.ID],
			})
		}
	}
	// Newest deployment first, then by decision id, so a listing leads with what was
	// just published and reads deterministically among a model's own decisions.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key > out[j].Key
		}
		return out[i].DecisionID < out[j].DecisionID
	})
	return out
}

// handleListDecisionDeployments lists the decisions deployed on this server as
// runtime artifacts — one row per decision, newest deployment first, every version
// present (ADR-0319). It is the decision
// counterpart of GET /api/v1/processes: what an operator can point a business rule
// task at, and what a pinned reference resolved to.
//
// ?applicationId= narrows to one application, ?decisionId= to one decision's
// version history — the two questions this listing is asked, answered without a
// second route.
func (s *Server) handleListDecisionDeployments(w http.ResponseWriter, r *http.Request) {
	appID := r.URL.Query().Get("applicationId")
	decisionID := r.URL.Query().Get("decisionId")
	var (
		recs    []persistedDecision
		loadErr error
	)
	// The pins are run-loop state (the deployments map and their compiled pins), so
	// they are read in the same turn as the records. An operator deciding what to
	// clean up needs to see what is holding a version before clicking, not after.
	pinsFor := map[uint64][]decisionPinRef{}
	s.do(func() {
		if recs, loadErr = s.decisionDeploys.LoadAll(); loadErr != nil {
			return
		}
		for _, rec := range recs {
			if pins := s.definitionsPinnedTo(rec.Key); len(pins) > 0 {
				pinsFor[rec.Key] = pins
			}
		}
	})
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list decision deployments: "+loadErr.Error())
		return
	}
	if appID != "" {
		kept := recs[:0]
		for _, rec := range recs {
			if rec.ApplicationID == appID {
				kept = append(kept, rec)
			}
		}
		recs = kept
	}
	out := decisionResponses(recs)
	for i := range out {
		out[i].PinnedBy = pinsFor[out[i].Key]
	}
	if decisionID != "" {
		kept := out[:0]
		for _, row := range out {
			if row.DecisionID == decisionID {
				kept = append(kept, row)
			}
		}
		out = kept
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// handleDecisionDeploymentXML serves one decision deployment's DMN source —
// exactly the bytes the registry was rebuilt from, not a re-export of whatever the
// model folder holds now. That distinction is the point: the file behind a
// modelRef is edited in place by the DMN editor, so only the deployment record
// still knows what a running process is actually evaluating.
func (s *Server) handleDecisionDeploymentXML(w http.ResponseWriter, r *http.Request) {
	key, err := strconv.ParseUint(r.PathValue("key"), 10, 64)
	if err != nil {
		httpapi.Error(w, http.StatusBadRequest, "invalid decision deployment key")
		return
	}
	var (
		rec     persistedDecision
		ok      bool
		loadErr error
	)
	s.do(func() { rec, ok, loadErr = s.decisionDeploys.load(key) })
	if loadErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "read decision deployment: "+loadErr.Error())
		return
	}
	if !ok {
		httpapi.Error(w, http.StatusNotFound, "no decision deployment with that key")
		return
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write([]byte(rec.XML))
}
