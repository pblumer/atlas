package temis

import (
	"context"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/dmn"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/state"
)

// Handler builds the in-process temis decision worker: a job handler
// that evaluates a *central* business rule task's decision on a remote temis
// instance and writes the result back as the resultVariable process variable
// (ADR-0050). Register it with a [job.Runner] via HandleCompleting for the reserved
// temis job type ([compiler.TemisDecisionJobTypeIndex]).
//
// It reuses [dmn.DecisionHandler] for the shared input/output-mapping semantics
// (ADR-0039) — static-input + variable-mapping merge, result → resultVariable — so
// a central decision and a local one differ only in the [dmn.Evaluator] bound to
// them: this one resolves the task's worker from reg and calls the remote
// client. A remote evaluation returns no temis trace, so its retained
// decision-evaluation record (ADR-0066) carries inputs and outputs but an empty
// trace. A job whose worker is not registered leaves the job pending with an
// error, exactly like any worker failure. sink, if non-nil, observes each result.
func Handler(store state.Reader, lookup dmn.ProcessLookup, reg *Registry, sink func(dmn.Result)) job.CompletingHandler {
	return dmn.DecisionHandler(store, lookup, func(cp *compiler.CompiledProcess, detail *compiler.BusinessRuleTaskDetail) (dmn.Evaluator, error) {
		// Resolved down to the connector name here rather than looked up directly, so
		// the in-engine path asks the registry the same question [Run] asks it — and
		// fails with the same sentence when nothing answers.
		name := cp.Intern(detail.Connector)
		if _, ok := reg.Client(name); !ok {
			return nil, reg.Unresolved("temis", name)
		}
		return func(ctx context.Context, decisionId string, inputs map[string]any) (dmn.Evaluation, error) {
			res, err := Run(ctx, Job{Connector: name, DecisionID: decisionId, Inputs: inputs}, reg)
			if err != nil {
				return dmn.Evaluation{}, err
			}
			return dmn.Evaluation{Outputs: res.Outputs, Trace: res.Trace}, nil
		}, nil
	}, sink)
}
