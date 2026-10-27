package playground

import (
	"context"
	"fmt"

	"github.com/pblumer/atlas/compiler"
	"github.com/pblumer/atlas/dmn"
)

// decisionBinding chooses which deployed model a sandbox's business rule task
// evaluates, and evaluates it there.
//
// It is production's choice (dmn.Handler) wherever a sandbox can make the same one,
// and it differs in two places, because a sandbox's processes were compiled from XML a
// moment ago rather than deployed:
//
//   - A deployment-bound task evaluates the model bundled with the deployment the
//     session runs (bundled). A draft has no deployed snapshot, so its task evaluates
//     the latest deployed decision of that id. The evaluation records which deployment
//     answered, so the choice is visible on the case.
//   - A task routed to a temis Worker (ADR-0050) is evaluated here, from the registry.
//     A sandbox dials nothing; that is how its side-effect freedom is guaranteed
//     rather than configured, and a decision is no exception.
//
// What cannot be found is an error, which the sandbox turns into an incident naming
// the decision. It never falls back to a version nobody chose.
func decisionBinding(reg *dmn.Registry, bundled uint64) dmn.Bind {
	return func(cp *compiler.CompiledProcess, detail *compiler.BusinessRuleTaskDetail) (dmn.Evaluator, error) {
		return func(ctx context.Context, decisionId string, inputs map[string]any) (dmn.Evaluation, error) {
			key, err := decisionKey(reg, bundled, cp, detail, decisionId)
			if err != nil {
				return dmn.Evaluation{}, err
			}
			out, trace, err := reg.EvaluateTraced(ctx, key, decisionId, inputs)
			return dmn.Evaluation{Outputs: out, Trace: trace, Key: key}, err
		}, nil
	}
}

// decisionKey is the deployment whose model answers a task's decision.
func decisionKey(reg *dmn.Registry, bundled uint64, cp *compiler.CompiledProcess, detail *compiler.BusinessRuleTaskDetail, decisionId string) (uint64, error) {
	switch detail.Binding {
	case compiler.BindingVersion:
		// Resolved when the sandbox opened, exactly as a deploy resolves it. A task
		// naming v2 must never run v3.
		if key, ok := cp.VersionPinnedKey(decisionId, detail.Version); ok {
			return key, nil
		}
		return 0, fmt.Errorf("decision %q version %d is not deployed on this server", decisionId, detail.Version)
	case compiler.BindingDeployment:
		if bundled != 0 && reg.Provides(bundled, decisionId) {
			return bundled, nil
		}
	}
	if key, ok := reg.LatestDecisionKey(decisionId); ok {
		return key, nil
	}
	return 0, fmt.Errorf("decision %q is not deployed on this server", decisionId)
}
