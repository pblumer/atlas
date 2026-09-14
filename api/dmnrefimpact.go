package api

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/pblumer/atlas/compiler"

	"github.com/pblumer/atlas/api/httpapi"
)

// What deleting a DMN reference would break
// (ADR-0331).
//
// A reference is what supplies the *model* a business rule task's decision is
// bundled from. Deleting the last one providing a decision breaks no running
// instance — a process deployment and a decision deployment both carry their own
// XML — but it can make a process undeployable from then on, and the message that
// says so arrives at the next deploy, with nothing in it about a deletion.
//
// This computes the same condition the deploy preflight applies (decisionCoverage),
// run forwards: which decisions lose their last model, and which definitions and
// drafts could then not be deployed. The Console renders it in the confirm.

// refImpactArtifact is one thing that could no longer be deployed: a deployed
// definition or a draft, the decision it names, and how that task binds.
type refImpactArtifact struct {
	// Kind is "deployed" or "draft".
	Kind      string `json:"kind"`
	ProcessID string `json:"processId"`
	Name      string `json:"name,omitempty"`
	// Key and Version are set for a deployed definition only.
	Key     uint64 `json:"key,omitempty"`
	Version int32  `json:"version,omitempty"`
	// DecisionID is the decision that would lose its model, and Binding how this
	// artifact's task binds to it — "deployment" or "latest" (ADR-0063).
	DecisionID string `json:"decisionId"`
	Binding    string `json:"binding"`
}

// refImpactResp is what deleting one reference would cost.
type refImpactResp struct {
	ModelRef string `json:"modelRef"`
	// Resolved is false when the handle names no model, in which case there is
	// nothing to lose and every list below is empty.
	Resolved bool `json:"resolved"`
	// Decisions are the decision ids this reference's model provides.
	Decisions []string `json:"decisions"`
	// Exclusive are those of them no *other* reference provides — the ones that
	// would lose their last model source.
	Exclusive []string `json:"exclusive"`
	// Blocked are the artifacts that could then not be deployed.
	Blocked []refImpactArtifact `json:"blocked"`
	// BlockedHidden counts further affected drafts in applications the caller
	// cannot see (ADR-0071). They are counted rather than named: understating the
	// damage because of who is looking would be a warning that lies to exactly the
	// person about to act.
	BlockedHidden int `json:"blockedHidden"`
}

// deployedDecisionUse is one deployed definition's business rule decisions, copied
// off the run loop so the matching below runs without it. Compiled state is
// immutable after deploy, but copying what is needed is the discipline the rest of
// this package follows.
type deployedDecisionUse struct {
	key         uint64
	processID   string
	name        string
	version     int32
	all         []string
	bundleBound []string
}

// handleDmnRefImpact reports what deleting this DMN reference would break. It is a
// read, and it refuses nothing: the reference stays the author's to delete.
//
// Reference records, drafts and the deployed definitions are run-loop state and are
// read there; resolving and compiling models, and compiling candidate drafts, is
// I/O and CPU and runs off it — the same split every other DMN route makes.
func (s *Server) handleDmnRefImpact(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var (
		rec      dmnRef
		ok       bool
		getErr   error
		allRefs  []dmnRef
		drafts   []draft
		projs    map[string]project
		deployed []deployedDecisionUse
		covered  map[string]bool
	)
	s.do(func() {
		if rec, ok, getErr = s.dmnrefs.Get(id); getErr != nil || !ok {
			return
		}
		if allRefs, getErr = s.dmnrefs.LoadAll(); getErr != nil {
			return
		}
		if drafts, getErr = s.drafts.LoadAll(); getErr != nil {
			return
		}
		if projs, getErr = s.projectsByID(); getErr != nil {
			return
		}
		// The decisions a deployment already covers: a latest-bound task naming one
		// of these is not at risk, because the deploy pins that deployment and never
		// reads the bundle (ADR-0327).
		covered = s.dmnRegistry.LatestDecisionIDs()
		for _, key := range s.order {
			d := s.deployments[key]
			if d == nil || d.cp == nil {
				continue
			}
			all := d.cp.BusinessRuleDecisions()
			if len(all) == 0 {
				continue
			}
			deployed = append(deployed, deployedDecisionUse{
				key: d.Key, processID: d.ProcessID, name: d.Name, version: d.Version,
				all: all, bundleBound: d.cp.BundleBoundDecisions(),
			})
		}
	})
	switch {
	case getErr != nil:
		httpapi.Error(w, http.StatusInternalServerError, "read dmn reference: "+getErr.Error())
		return
	case !ok:
		httpapi.Error(w, http.StatusNotFound, "no dmn reference with that id")
		return
	}
	if code, msg := s.authorizeArtifact(r, rec.ProjectID, rec.OwnerID, ScopeRoleViewer); code != 0 {
		httpapi.Error(w, code, msg)
		return
	}

	out := refImpactResp{ModelRef: rec.ModelRef, Decisions: []string{}, Exclusive: []string{}, Blocked: []refImpactArtifact{}}

	// Off the loop: what this reference provides, and what every other one does.
	// Exclusivity is a fact about a decision id, so it is computed over *all*
	// references — a reference the caller cannot see still provides its decisions,
	// and pretending otherwise would over-warn.
	mine, err := s.dmnValidator.Validate(r.Context(), rec.ModelRef)
	if err != nil {
		httpapi.Error(w, http.StatusInternalServerError, "resolve dmn model: "+err.Error())
		return
	}
	out.Resolved = mine.Resolved
	if !mine.Valid || len(mine.Decisions) == 0 {
		httpapi.JSON(w, http.StatusOK, out)
		return
	}
	out.Decisions = mine.Decisions

	elsewhere := map[string]bool{}
	for _, other := range allRefs {
		if other.ID == rec.ID || other.ModelRef == "" {
			continue
		}
		res, err := s.dmnValidator.Validate(r.Context(), other.ModelRef)
		if err != nil {
			httpapi.Error(w, http.StatusInternalServerError, "resolve dmn model: "+err.Error())
			return
		}
		if !res.Valid {
			continue
		}
		for _, d := range res.Decisions {
			elsewhere[d] = true
		}
	}
	exclusive := map[string]bool{}
	for _, d := range mine.Decisions {
		if !elsewhere[d] {
			exclusive[d] = true
			out.Exclusive = append(out.Exclusive, d)
		}
	}
	if len(exclusive) == 0 {
		httpapi.JSON(w, http.StatusOK, out)
		return
	}

	for _, d := range deployed {
		for _, hit := range blockedDecisions(d.all, d.bundleBound, exclusive, covered) {
			out.Blocked = append(out.Blocked, refImpactArtifact{
				Kind: "deployed", ProcessID: d.processID, Name: d.name,
				Key: d.key, Version: d.version,
				DecisionID: hit.decision, Binding: hit.binding,
			})
		}
	}

	for _, dr := range drafts {
		// Compiling every draft in the system for one dialog would be wasteful, so a
		// draft is compiled only when its XML mentions one of the exclusive decision
		// ids. The prefilter can admit a draft that turns out not to name the
		// decision; it can never skip one that does, because a task naming a decision
		// carries that id in its XML.
		if !mentionsAny(dr.XML, out.Exclusive) {
			continue
		}
		deployables, err := compiler.ParseAll(1, 1, bytes.NewReader([]byte(dr.XML)))
		if err != nil {
			continue // a draft that does not compile cannot be deployed for its own reasons
		}
		var all, bundleBound []string
		for i := range deployables {
			all = append(all, deployables[i].Process.BusinessRuleDecisions()...)
			bundleBound = append(bundleBound, deployables[i].Process.BundleBoundDecisions()...)
		}
		hits := blockedDecisions(all, bundleBound, exclusive, covered)
		if len(hits) == 0 {
			continue
		}
		if !s.canViewArtifact(r, dr.ProjectID, dr.OwnerID, projs) {
			out.BlockedHidden += len(hits)
			continue
		}
		for _, hit := range hits {
			out.Blocked = append(out.Blocked, refImpactArtifact{
				Kind: "draft", ProcessID: dr.ProcessID, Name: dr.Name,
				DecisionID: hit.decision, Binding: hit.binding,
			})
		}
	}
	httpapi.JSON(w, http.StatusOK, out)
}

// decisionHit is one decision an artifact would lose, with the binding that makes
// losing it fatal.
type decisionHit struct {
	decision string
	binding  string
}

// blockedDecisions applies the deploy preflight's rule forwards: which of the
// decisions this artifact names would stop it deploying if `exclusive` lost their
// last model.
//
//   - a `deployment`-bound task always: it evaluates the model bundled with its own
//     process, and there would be none to bundle;
//   - a `latest`-bound task only when no decision deployment covers the id, because
//     one that is covered pins that deployment and never reads the bundle
//     (ADR-0327);
//   - anything else, not at all.
//
// Results are ordered by `all` so the same artifact reports the same way twice
// running.
func blockedDecisions(all, bundleBound []string, exclusive, covered map[string]bool) []decisionHit {
	bundle := make(map[string]bool, len(bundleBound))
	for _, id := range bundleBound {
		bundle[id] = true
	}
	var out []decisionHit
	seen := map[string]bool{}
	for _, id := range all {
		if seen[id] || !exclusive[id] {
			continue
		}
		switch {
		case bundle[id]:
			seen[id] = true
			out = append(out, decisionHit{decision: id, binding: "deployment"})
		case !covered[id]:
			seen[id] = true
			out = append(out, decisionHit{decision: id, binding: "latest"})
		}
	}
	return out
}

// mentionsAny is the draft prefilter: does this XML contain any of these decision
// ids as a substring. Cheap, and wrong only in the direction that costs a compile
// rather than a miss.
func mentionsAny(xml string, ids []string) bool {
	for _, id := range ids {
		if id != "" && strings.Contains(xml, id) {
			return true
		}
	}
	return false
}
