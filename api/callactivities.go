package api

import (
	"net/http"
)

// callActivityRow is one call activity in the per-server management view: the
// caller it lives in, its static configuration (which process it calls, the
// binding, the propagation flags, whether it is a multi-instance loop), and how
// it currently resolves on this server. Resolution is a per-server fact — a
// caller may deploy before its callee — so the same model on two servers can
// resolve differently; that is exactly what this view surfaces (ADR-0076).
type callActivityRow struct {
	CallerKey          uint64 `json:"callerKey"`
	CallerProcessID    string `json:"callerProcessId"`
	CallerName         string `json:"callerName"`
	CallerVersion      int32  `json:"callerVersion"`
	ElementID          string `json:"elementId"`
	CalledProcessID    string `json:"calledProcessId"`
	Binding            string `json:"binding"`
	PropagateAllParent bool   `json:"propagateAllParent"`
	PropagateAllChild  bool   `json:"propagateAllChild"`
	MultiInstance      bool   `json:"multiInstance"`
	// Resolved is true when a process with CalledProcessID is currently deployed
	// on this server, so the call would start a child rather than park. Target*
	// name the definition the runtime's `latest` resolution reaches (the
	// highest-version deployment of that id); they are zero when unresolved.
	Resolved      bool   `json:"resolved"`
	TargetKey     uint64 `json:"targetKey"`
	TargetName    string `json:"targetName"`
	TargetVersion int32  `json:"targetVersion"`
}

// handleCallActivities lists every call activity across the server's deployed
// processes with its current resolution status — the per-server "which process
// calls which, and does the target exist" management view (ADR-0076). It is a
// read-only projection over the deployment registry and the compiled processes;
// it changes no runtime state and honours the invariants (I4/I5 — it reads the
// immutable compiled model, mutates nothing). Callers are listed in registration
// order; a caller's call activities in node order.
//
// Resolution mirrors the engine's runtime rule (latestDefKey): a call resolves to
// the newest deployed definition of its calledProcessId, regardless of the
// configured binding, because that is what the child-create actually reaches
// today. The binding is reported as authored so an operator can see intent versus
// what the server currently resolves.
func (s *Server) handleCallActivities(w http.ResponseWriter, _ *http.Request) {
	rows := []callActivityRow{}
	s.do(func() {
		for _, key := range s.order {
			d := s.deployments[key]
			for _, ref := range d.cp.CallActivities() {
				row := callActivityRow{
					CallerKey:          d.Key,
					CallerProcessID:    d.ProcessID,
					CallerName:         d.Name,
					CallerVersion:      d.Version,
					ElementID:          ref.ElementId,
					CalledProcessID:    ref.CalledProcessId,
					Binding:            ref.Binding.String(),
					PropagateAllParent: ref.PropagateAllParent,
					PropagateAllChild:  ref.PropagateAllChild,
					MultiInstance:      ref.MultiInstance,
				}
				if target := s.latestDeploymentByProcessID(ref.CalledProcessId); target != nil {
					row.Resolved = true
					row.TargetKey = target.Key
					row.TargetName = target.Name
					row.TargetVersion = target.Version
				}
				rows = append(rows, row)
			}
		}
	})
	writeJSON(w, http.StatusOK, rows)
}
