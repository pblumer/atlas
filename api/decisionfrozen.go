package api

// A definition deployed under ADR-0319 froze each latest-bound decision reference
// on the version that was newest at its deploy, and keeps evaluating that version
// until it is redeployed. ADR-0423 made latest mean the newest version when the
// task runs, and left those definitions as they were deployed — changing what a
// deployed definition evaluates without anybody deploying it is the one thing the
// record promised would not happen. What it could not leave as it was is that
// nothing says so: the Modeler's binding field reads "Latest — newest version when
// the task runs" on a definition that does not, and the owner has no way to find
// out short of reading an evaluation's version.
//
// These are the facts the surfaces need to say it: which definition is frozen, on
// what, and whether that is still what latest resolves to.

// frozenDecisionResp is one decision a definition's latest-bound business rule
// tasks were frozen on when it was deployed (ADR-0319), and what latest resolves to
// now.
type frozenDecisionResp struct {
	DecisionID string `json:"decisionId"`
	// Key is what the tasks evaluate for as long as this definition exists: a
	// decision deployment, or the definition's own key when the decision had none at
	// deploy time and the model bundled with the process was what latest meant then.
	Key uint64 `json:"key"`
	// Version is Key's version of the decision, absent when Key is the bundled model.
	Version int32 `json:"version,omitempty"`
	// LatestKey and LatestVersion are the newest decision deployment of the id —
	// what a latest-bound task on a definition deployed now evaluates when it runs
	// (ADR-0423). Absent when the decision was never deployed on its own.
	LatestKey     uint64 `json:"latestKey,omitempty"`
	LatestVersion int32  `json:"latestVersion,omitempty"`
	// Behind says the frozen model is no longer what latest resolves to: deploying
	// the process again would change which model its tasks evaluate.
	Behind bool `json:"behind"`
}

// frozenDecisionsOf reports the decisions a definition is frozen on, nil for every
// definition that is not: one deployed under the runtime policy follows latest,
// and one deployed before ADR-0319 resolves latest when its task is activated. A
// reference the frozen record carries no key for is left out for the same reason —
// the worker resolves it at activation (dmn.decisionModelKey).
//
// It reads the deployments' compiled pins and the decision version index, which
// are run-loop state, so it runs on the loop.
func (s *Server) frozenDecisionsOf(d *deployment) []frozenDecisionResp {
	if d == nil || d.cp == nil || !d.cp.DecisionsPinned() {
		return nil
	}
	var out []frozenDecisionResp
	for _, id := range d.cp.LatestBoundDecisions() {
		key, ok := d.cp.PinnedDecisionKey(id)
		if !ok {
			continue
		}
		f := frozenDecisionResp{DecisionID: id, Key: key, Version: s.decisionVersionOf(key, id)}
		if latest, ok := s.dmnRegistry.LatestDecisionKey(id); ok {
			f.LatestKey = latest
			f.LatestVersion = s.decisionVersionOf(latest, id)
			f.Behind = latest != key
		}
		out = append(out, f)
	}
	return out
}
