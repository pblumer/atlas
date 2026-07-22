// Package dmn integrates the temis DMN decision engine
// (github.com/pblumer/temis) into Atlas, so a BPMN business rule task can
// delegate a decision and get an answer back.
//
// The integration deliberately mirrors how service tasks reach external workers
// (ADR-0007), so it inherits the engine's durability guarantees without
// touching the hot path (ADR-0014):
//
//   - A DMN model is compiled by temis once, at deploy time, into immutable
//     thread-safe decisions held in a [Registry] (invariant I5: compile, don't
//     interpret — no XML parsing or FEEL compilation at runtime).
//   - A business rule task creates a job carrying the reserved DMN job type. The
//     processor never evaluates a decision itself, so it stays allocation-free
//     (invariant I1) and free of the temis dependency.
//   - The in-process [Handler] — a job worker — pulls those jobs, evaluates the
//     decision off the processor goroutine, and completes the job, which drives
//     the token onward through the normal completion path. Evaluation is a
//     post-durability side effect, exactly like any other worker (invariant I2).
//
// Because there is no process-variable subsystem yet (Milestone 1), a business
// rule task feeds its decision a static input context recorded at deploy time
// and its outputs are surfaced through a caller-supplied sink rather than written
// back as variables. Wiring real input/output variable mappings is future work.
package dmn

import (
	"context"
	"fmt"

	tdmn "github.com/pblumer/temis/dmn"
)

// Registry holds the DMN models deployed alongside process definitions. It
// compiles each model once with temis and keeps the immutable result, keyed by
// the owning process-definition key, ready for cheap repeated evaluation.
//
// A Registry is safe for concurrent evaluation once populated. Populate it
// (via Deploy) before the processes that use it start running.
type Registry struct {
	engine      *tdmn.Engine
	definitions map[uint64]*tdmn.Definitions
}

// NewRegistry creates an empty registry over a fresh temis engine.
func NewRegistry() *Registry {
	return &Registry{
		engine:      tdmn.New(),
		definitions: map[uint64]*tdmn.Definitions{},
	}
}

// Deploy compiles a DMN model and registers it under the process-definition key
// of the process whose business rule tasks reference it. Compilation happens
// here, at deploy time, never at evaluation time (invariant I5). It returns an
// error if temis cannot parse or compile the model.
func (r *Registry) Deploy(defKey uint64, dmnXML []byte) error {
	defs, diags, err := r.engine.Compile(context.Background(), dmnXML)
	if err != nil {
		return fmt.Errorf("dmn: compile model for def %d: %w", defKey, err)
	}
	if diags.HasErrors() {
		return fmt.Errorf("dmn: model for def %d has errors: %v", defKey, diags)
	}
	r.definitions[defKey] = defs
	return nil
}

// Evaluate runs the named decision from the model deployed under defKey against
// the given input context and returns its outputs (decision name → value). It is
// the runtime hot spot of the integration, but it runs on a worker, off the
// processor goroutine.
func (r *Registry) Evaluate(ctx context.Context, defKey uint64, decisionId string, in map[string]any) (map[string]any, error) {
	defs, ok := r.definitions[defKey]
	if !ok {
		return nil, fmt.Errorf("dmn: no model deployed for def %d", defKey)
	}
	dec, err := defs.Decision(decisionId)
	if err != nil {
		return nil, fmt.Errorf("dmn: decision %q in def %d: %w", decisionId, defKey, err)
	}
	res, err := dec.Evaluate(ctx, tdmn.Input(in))
	if err != nil {
		return nil, fmt.Errorf("dmn: evaluate %q in def %d: %w", decisionId, defKey, err)
	}
	return res.Outputs, nil
}
