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
	"encoding/json"
	"fmt"
	"sort"

	tdmn "github.com/pblumer/temis/dmn"
)

// Registry holds the DMN models deployed alongside process definitions. It
// compiles each model once with temis and keeps the immutable result, keyed by
// the owning process-definition key, ready for cheap repeated evaluation.
//
// A Registry is safe for concurrent evaluation once populated. Populate it
// (via Deploy) before the processes that use it start running.
type Registry struct {
	engine *tdmn.Engine
	// definitions maps a process-definition key to the compiled DMN models bundled
	// with it. A process may reference decisions from several models (its business
	// rule tasks are not confined to one), so each key holds a list, appended to by
	// Deploy and searched by decision id at evaluation time.
	definitions map[uint64][]*tdmn.Definitions
	// latest maps a decision id to the newest deployed model that provides it, for
	// latest-bound business rule tasks (ADR-0063). Deploy overwrites it, so after
	// replaying deployments oldest-first the pointer holds the last-deployed model.
	latest map[string]*tdmn.Definitions
}

// NewRegistry creates an empty registry over a fresh temis engine.
func NewRegistry() *Registry {
	return &Registry{
		engine:      tdmn.New(),
		definitions: map[uint64][]*tdmn.Definitions{},
		latest:      map[string]*tdmn.Definitions{},
	}
}

// Deploy compiles a DMN model and registers it under the process-definition key of
// the process whose business rule tasks reference it. A process may bundle several
// models (its tasks can call decisions from different models), so Deploy appends —
// call it once per bundled model. It also updates the latest-version pointer for
// every decision the model provides (ADR-0063), so a latest-bound task resolves the
// newest deployed version. Compilation happens here, at deploy time, never at
// evaluation time (invariant I5). It returns an error if temis cannot parse or
// compile the model.
func (r *Registry) Deploy(defKey uint64, dmnXML []byte) error {
	defs, diags, err := r.engine.Compile(context.Background(), dmnXML)
	if err != nil {
		return fmt.Errorf("dmn: compile model for def %d: %w", defKey, err)
	}
	if diags.HasErrors() {
		return fmt.Errorf("dmn: model for def %d has errors: %v", defKey, diags)
	}
	r.register(defKey, defs)
	return nil
}

// Reload is Deploy for a model that is *already* deployed — one snapshotted into a
// deployment record, coming back at startup — and so it does not re-apply the
// deploy-time gate above (ADR-0177). Refusing the
// model here would not undeploy anything; it would only keep the server from
// starting, with every other definition and every running instance behind it,
// because a diagnostic that did not exist when the model was deployed exists now.
//
// The model is registered and the error diagnostics are returned rendered for
// display ("" when there are none), for the caller to report. Rendered, not
// structured, because temis documents diagnostic messages as human-readable and
// explicitly not a stable API: they are for an operator to read, not for code to
// act on.
//
// This is safe because of what temis guarantees about a diagnostic: malformed XML
// is a hard error, but per-decision problems leave the rest of the model compiled,
// and a decision whose logic failed to compile is "present but not executable" —
// so evaluating *that* decision fails the way a failing decision has always failed,
// as a job error on a worker, while every other decision in the model still
// answers. A hard compile error still returns an error here: there is no model to
// bring back.
func (r *Registry) Reload(defKey uint64, dmnXML []byte) (string, error) {
	defs, diags, err := r.engine.Compile(context.Background(), dmnXML)
	if err != nil {
		return "", fmt.Errorf("dmn: compile model for def %d: %w", defKey, err)
	}
	r.register(defKey, defs)
	if !diags.HasErrors() {
		return "", nil
	}
	return formatDiagnostics(diags), nil
}

// register indexes a compiled model under the deployment key it was bundled with,
// and as the latest provider of every decision it declares (ADR-0063). Shared by
// Deploy and Reload so both index a model identically once it is accepted.
func (r *Registry) register(defKey uint64, defs *tdmn.Definitions) {
	r.definitions[defKey] = append(r.definitions[defKey], defs)
	for _, id := range defs.Index().Decisions {
		r.latest[id] = defs
	}
}

// modelProviding returns the model in the list that provides the decision id, or
// nil if none does — how a deployment-bound evaluation finds the bundled model that
// declares its decision when a process bundles several.
func modelProviding(list []*tdmn.Definitions, decisionId string) *tdmn.Definitions {
	for _, defs := range list {
		for _, id := range defs.Index().Decisions {
			if id == decisionId {
				return defs
			}
		}
	}
	return nil
}

// Evaluate runs the named decision from the model deployed under defKey against
// the given input context and returns its outputs (decision name → value). It is
// the runtime hot spot of the integration, but it runs on a worker, off the
// processor goroutine.
func (r *Registry) Evaluate(ctx context.Context, defKey uint64, decisionId string, in map[string]any) (map[string]any, error) {
	out, _, err := r.EvaluateTraced(ctx, defKey, decisionId, in)
	return out, err
}

// EvaluateTraced is Evaluate plus the temis trace explaining how the decision was
// made — which tables ran, which rules matched, and why (ADR-0066). The trace is
// canonical JSON (temis's [tdmn.Trace] tree) or nil for a decision with no table
// logic (a literal expression). The DMN worker uses it to retain a debuggable
// record of the evaluation. Tracing runs off the processor goroutine, so its extra
// allocation is not on any hot path (temis's WithTrace, ADR-0013/WP-51).
func (r *Registry) EvaluateTraced(ctx context.Context, defKey uint64, decisionId string, in map[string]any) (map[string]any, []byte, error) {
	list, ok := r.definitions[defKey]
	if !ok || len(list) == 0 {
		return nil, nil, fmt.Errorf("dmn: no model deployed for def %d", defKey)
	}
	defs := modelProviding(list, decisionId)
	if defs == nil {
		return nil, nil, fmt.Errorf("dmn: decision %q in no model deployed for def %d", decisionId, defKey)
	}
	return evalDecision(ctx, defs, decisionId, in, fmt.Sprintf("def %d", defKey))
}

// EvaluateLatest runs the named decision from the newest deployed model that
// provides it (ADR-0063), for a latest-bound business rule task. It is otherwise
// identical to Evaluate; only the model selection differs.
func (r *Registry) EvaluateLatest(ctx context.Context, decisionId string, in map[string]any) (map[string]any, error) {
	out, _, err := r.EvaluateLatestTraced(ctx, decisionId, in)
	return out, err
}

// EvaluateLatestTraced is EvaluateLatest plus the temis trace (see EvaluateTraced).
func (r *Registry) EvaluateLatestTraced(ctx context.Context, decisionId string, in map[string]any) (map[string]any, []byte, error) {
	defs, ok := r.latest[decisionId]
	if !ok {
		return nil, nil, fmt.Errorf("dmn: no model deployed providing decision %q", decisionId)
	}
	return evalDecision(ctx, defs, decisionId, in, "the latest deployed model")
}

// DeployedDecision is a decision available from a deployed model, described for the
// Modeler's decision picker (ADR-0050): its id, declared inputs and output, and the
// name of the model that provides it. It lets an author select — and auto-fill the
// inputs of — a decision that is deployed (and thus runnable) even when no separate
// DMN reference artifact exists for it.
type DeployedDecision struct {
	ID     string
	Name   string
	Model  string
	Inputs []DecisionField
	Output DecisionField
}

// DeployedDecisions describes every decision provided by the newest deployed model
// that supplies it (ADR-0063 latest binding), for the Modeler's picker. It reads the
// registry's already-compiled models — no XML parsing or resolver I/O — so it must
// run on the registry's owning goroutine (the run loop), the same single-writer
// discipline as Deploy. Results are sorted by decision id for a stable picker.
func (r *Registry) DeployedDecisions() []DeployedDecision {
	out := make([]DeployedDecision, 0, len(r.latest))
	for id, defs := range r.latest {
		for _, di := range describeDecisions(defs) {
			if di.ID != id {
				continue // describe the one decision this model is latest for
			}
			out = append(out, DeployedDecision{
				ID:     di.ID,
				Name:   di.Name,
				Model:  defs.ModelName(),
				Inputs: di.Inputs,
				Output: di.Output,
			})
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// evalDecision evaluates one decision from an already-compiled model, requesting
// the temis trace so the worker can retain how the decision was made (ADR-0066).
// `where` names the model in errors ("def 7" or "the latest deployed model"). The
// returned trace bytes are the JSON image of the temis trace tree, or nil when the
// decision produced none (a literal-expression decision has no tables to trace).
func evalDecision(ctx context.Context, defs *tdmn.Definitions, decisionId string, in map[string]any, where string) (map[string]any, []byte, error) {
	dec, err := defs.Decision(decisionId)
	if err != nil {
		return nil, nil, fmt.Errorf("dmn: decision %q in %s: %w", decisionId, where, err)
	}
	res, err := dec.Evaluate(ctx, tdmn.Input(in), tdmn.WithTrace())
	if err != nil {
		return nil, nil, fmt.Errorf("dmn: evaluate %q in %s: %w", decisionId, where, err)
	}
	var trace []byte
	if res.Trace != nil {
		// The trace tree carries JSON tags as its wire contract (temis dmn/trace.go);
		// a marshal failure would only mean an unexpected value shape, so degrade to no
		// trace rather than failing the evaluation the token depends on.
		if b, err := json.Marshal(res.Trace); err == nil {
			trace = b
		}
	}
	return res.Outputs, trace, nil
}
