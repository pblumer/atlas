package dmn

import (
	"context"
	"fmt"

	tdmn "github.com/pblumer/temis/dmn"
)

// Standalone is one compiled decision held outside the registry, by a caller that owns
// its lifecycle: an authorization policy embeds a decision table and is compiled when
// the policy is published (ADR-0475 §6).
//
// It is not a deployment. Nothing is keyed or registered, so publishing a policy can
// never become "a new version" of a decision a process is bound to (ADR-0319), and the
// one temis engine stays behind this package, as FEEL stays behind expr (ADR-0015).
//
// A compiled model is immutable, so a Standalone is safe for concurrent evaluation.
type Standalone struct {
	defs       *tdmn.Definitions
	decisionID string
}

// CompileStandalone compiles src once and checks that it provides decisionID. A model
// with errors, or without that decision, is refused here rather than at the first
// evaluation: publication is where an author can still fix it (invariant I5).
func CompileStandalone(ctx context.Context, src []byte, decisionID string) (*Standalone, error) {
	defs, diags, err := tdmn.New().Compile(ctx, src)
	if err != nil {
		return nil, fmt.Errorf("dmn: compile: %w", err)
	}
	if diags.HasErrors() {
		return nil, fmt.Errorf("dmn: the model has errors: %v", diags)
	}
	if _, err := defs.Decision(decisionID); err != nil {
		return nil, fmt.Errorf("dmn: the model provides no decision %q: %w", decisionID, err)
	}
	return &Standalone{defs: defs, decisionID: decisionID}, nil
}

// Evaluate runs the decision over in and returns the decision's own result: a context
// (map) for a table with several outputs, the bare value for one with a single output.
// It is the registry's evaluation path — aliased input names, the type check that
// refuses a mismatched input instead of letting every rule quietly miss, and numbers
// restored from their decimal strings — so a decision answers here exactly as it would
// on a business rule task.
func (s *Standalone) Evaluate(ctx context.Context, in map[string]any) (any, error) {
	out, _, err := evalDecision(ctx, s.defs, s.decisionID, in, "a standalone model")
	if err != nil {
		return nil, err
	}
	// temis answers with one entry, under the decision's name; anything else is a shape
	// this caller cannot read and must not guess at.
	if len(out) != 1 {
		return nil, fmt.Errorf("dmn: decision %q answered %d results, want 1", s.decisionID, len(out))
	}
	for _, v := range out {
		return v, nil
	}
	return nil, nil
}
