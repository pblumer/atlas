package temis

import (
	"context"
	"fmt"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/dmn"
	"github.com/pblumer/atlas/job"
	"github.com/pblumer/atlas/state"
)

// Handler builds the in-process temis decision connector worker: a job handler
// that evaluates a *central* business rule task's decision on a remote temis
// instance and writes the result back as the resultVariable process variable
// (ADR-0050). Register it with a [job.Runner] via HandleCompleting for the reserved
// temis-connector job type ([compiler.TemisDecisionJobTypeIndex]).
//
// It reuses [dmn.DecisionHandler] for the shared input/output-mapping semantics
// (ADR-0039) — static-input + variable-mapping merge, result → resultVariable — so
// a central decision and a local one differ only in the [dmn.Evaluator] bound to
// them: this one resolves the task's connector from reg and calls the remote
// client. A remote evaluation returns no temis trace, so its retained
// decision-evaluation record (ADR-0064) carries inputs and outputs but an empty
// trace. A job whose connector is not registered leaves the job pending with an
// error, exactly like any worker failure. sink, if non-nil, observes each result.
func Handler(store *state.Store, lookup dmn.ProcessLookup, reg *Registry, sink func(dmn.Result)) job.CompletingHandler {
	return dmn.DecisionHandler(store, lookup, func(cp *compiler.CompiledProcess, detail *compiler.BusinessRuleTaskDetail) (dmn.Evaluator, error) {
		name := cp.Intern(detail.Connector)
		client, ok := reg.Client(name)
		if !ok {
			return nil, fmt.Errorf("temis: no connector registered as %q", name)
		}
		return func(ctx context.Context, decisionId string, inputs map[string]any) (dmn.Evaluation, error) {
			out, err := client.Evaluate(ctx, decisionId, inputs)
			return dmn.Evaluation{Outputs: out}, err
		}, nil
	}, sink)
}
