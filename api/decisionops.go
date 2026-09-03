package api

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/pblumer/atlas/model"

	"github.com/pblumer/atlas/api/httpapi"
)

// deployedDecisionView is one row of the Operations "Decisions" overview: a DMN
// decision that deployed process definitions reference and/or that instances have
// evaluated, rolled up across every process and version — the decision analogue of
// the one-row-per-process instances list. Processes names the deployed definitions
// whose business rule tasks call it (latest version per process id); Evaluations
// and LastEvaluatedAt summarize its retained evaluation history (ADR-0066).
type deployedDecisionView struct {
	DecisionID      string               `json:"decisionId"`
	Local           bool                 `json:"local"`
	Processes       []decisionProcessRef `json:"processes"`
	Evaluations     int                  `json:"evaluations"`
	LastEvaluatedAt int64                `json:"lastEvaluatedAt"`
}

// decisionProcessRef points a decision row back at a deployed process that uses it,
// so the UI can link a decision to the process (and version) that calls it.
type decisionProcessRef struct {
	ProcessID string `json:"processId"`
	Name      string `json:"name"`
	Key       uint64 `json:"key"`
	Version   int32  `json:"version"`
}

// decisionAgg accumulates one decision's deployment references and evaluation usage
// while the handler folds deployments and the evaluation history together.
type decisionAgg struct {
	local  bool
	procs  map[string]decisionProcessRef // keyed by process id, kept at its latest version
	evals  int
	lastAt int64
}

// note records that a deployment's process references this decision, keeping only
// the latest version per process id — so a decision used by five versions of the
// same process shows one process reference, mirroring the instances view's
// one-row-per-process rollup.
func (a *decisionAgg) note(d *deployment) {
	if d == nil {
		return
	}
	if cur, ok := a.procs[d.ProcessID]; ok && cur.Version >= d.Version {
		return
	}
	a.procs[d.ProcessID] = decisionProcessRef{
		ProcessID: d.ProcessID, Name: d.Name, Key: d.Key, Version: d.Version,
	}
}

// handleDeployedDecisions returns the deployed-and-used DMN decisions, one row per
// decision id, for the Operations "Decisions" overview (the decision counterpart of
// the process instances list). A decision appears if a deployed definition's
// business rule task references it (so a never-run decision still shows, like a
// deployed process with zero instances) or if any instance evaluated it. Deployment
// references and the process linked to each evaluation are resolved on the run loop
// (they read the deployments map); the evaluation history is a store scan folded on
// the same loop. Rows are ordered by usage then id so the busiest decisions lead.
func (s *Server) handleDeployedDecisions(w http.ResponseWriter, _ *http.Request) {
	byDecision := map[string]*decisionAgg{}
	get := func(id string) *decisionAgg {
		a := byDecision[id]
		if a == nil {
			a = &decisionAgg{procs: map[string]decisionProcessRef{}}
			byDecision[id] = a
		}
		return a
	}

	var scanErr error
	s.do(func() {
		// Deployed decisions: every local business rule decision across all deployed
		// definitions. These are the decisions snapshotted into the DMN registry, so a
		// decision that has never been evaluated still lists with its referencing
		// processes.
		for _, key := range s.order {
			d := s.deployments[key]
			if d == nil || d.cp == nil {
				continue
			}
			for _, id := range d.cp.BusinessRuleDecisions() {
				a := get(id)
				a.local = true
				a.note(d)
			}
		}
		// Usage: fold every retained evaluation, grouping by decision id. Attaching the
		// evaluation's own process (via its process-definition key) covers decisions the
		// deployment sweep misses — a central/worker decision has no local model, so
		// it is not in BusinessRuleDecisions, but its evaluations still tie it to the
		// process that ran it.
		scanErr = s.store.EachDecisionEvaluation(func(_ uint64, ts int64, v *model.DecisionEvaluationValue) error {
			a := get(v.DecisionId)
			a.evals++
			if ts > a.lastAt {
				a.lastAt = ts
			}
			if d, ok := s.deployments[v.ProcessDefKey]; ok {
				a.note(d)
			}
			return nil
		})
	})
	if scanErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list decision evaluations: "+scanErr.Error())
		return
	}

	out := make([]deployedDecisionView, 0, len(byDecision))
	for id, a := range byDecision {
		procs := make([]decisionProcessRef, 0, len(a.procs))
		for _, p := range a.procs {
			procs = append(procs, p)
		}
		sort.Slice(procs, func(i, j int) bool { return procs[i].ProcessID < procs[j].ProcessID })
		out = append(out, deployedDecisionView{
			DecisionID:      id,
			Local:           a.local,
			Processes:       procs,
			Evaluations:     a.evals,
			LastEvaluatedAt: a.lastAt,
		})
	}
	// Busiest decisions first, then alphabetically for a stable order among ties
	// (including all the zero-evaluation deployed decisions).
	sort.Slice(out, func(i, j int) bool {
		if out[i].Evaluations != out[j].Evaluations {
			return out[i].Evaluations > out[j].Evaluations
		}
		return out[i].DecisionID < out[j].DecisionID
	})
	httpapi.JSON(w, http.StatusOK, out)
}

// decisionEvaluationRow is one evaluation of a given decision — a "decision
// instance" — as shown when drilling into a decision from the overview: when it
// ran, the process instance it ran in, the diagram element that drove it, and the
// exact inputs it saw, outputs it produced, and temis trace explaining which rules
// fired (ADR-0066). The inputs are the debugging payload: they show the precise
// evaluated value (with its JSON type and quoting), so a decision that silently
// returns nothing — a string compared against the wrong type, a trailing space, a
// number where a string was expected — is diagnosable from what it actually saw.
type decisionEvaluationRow struct {
	At          int64           `json:"at"`
	InstanceKey uint64          `json:"instanceKey"`
	ProcessID   string          `json:"processId"`
	ElementID   string          `json:"elementId"`
	Inputs      json.RawMessage `json:"inputs"`
	Outputs     json.RawMessage `json:"outputs"`
	Trace       json.RawMessage `json:"trace,omitempty"`
}

// handleDecisionEvaluations returns every retained evaluation of one decision id,
// newest first — the drill-down from the Operations decisions overview into a single
// decision's "instances". It folds the whole decision-evaluation history and keeps
// the records for the named decision, resolving each to its process id and diagram
// element via the owning deployment. Like the other convenience reads, an unknown or
// never-evaluated decision id yields an empty array and a 200, not a 404.
func (s *Server) handleDecisionEvaluations(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	out := []decisionEvaluationRow{}
	var scanErr error
	s.do(func() {
		scanErr = s.store.EachDecisionEvaluation(func(_ uint64, ts int64, v *model.DecisionEvaluationValue) error {
			if v.DecisionId != id {
				return nil
			}
			row := decisionEvaluationRow{
				At:          ts,
				InstanceKey: v.ProcessInstanceKey,
				Inputs:      rawJSONOr(v.InputsJSON, "{}"),
				Outputs:     rawJSONOr(v.OutputsJSON, "{}"),
			}
			if v.TraceJSON != "" {
				row.Trace = json.RawMessage(v.TraceJSON)
			}
			if d, ok := s.deployments[v.ProcessDefKey]; ok {
				row.ProcessID = d.ProcessID
				row.ElementID = d.cp.ElementBpmnId(v.ElementId)
			}
			out = append(out, row)
			return nil
		})
	})
	if scanErr != nil {
		httpapi.Error(w, http.StatusInternalServerError, "list decision evaluations: "+scanErr.Error())
		return
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At > out[j].At })
	httpapi.JSON(w, http.StatusOK, out)
}
