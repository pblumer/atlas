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
//     interpret — no XML parsing or FEEL compilation at runtime), and which
//     deployment's model a task evaluates is settled there too
//     (ADR-0319), so the runtime and a
//     replay choose no version.
//   - A business rule task creates a job carrying the reserved DMN job type. The
//     processor never evaluates a decision itself, so it stays allocation-free
//     (invariant I1) and free of the temis dependency.
//   - The in-process [Handler] — a job worker — pulls those jobs, evaluates the
//     decision off the processor goroutine, and completes the job, which drives
//     the token onward through the normal completion path. Evaluation is a
//     post-durability side effect, exactly like any other worker (invariant I2).
//
// A business rule task's input context is the static inputs recorded at deploy
// time overlaid with its io-mapping inputs, FEEL-evaluated over the instance's
// variables off the hot path, and its result is written back into the process
// variable the task names (ADR-0039). Each evaluation also rides back as a durable
// record carrying its inputs, outputs and temis trace (ADR-0066); the
// caller-supplied sink is an additional observation seam for tests and
// diagnostics, not the path the result travels.
package dmn

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	tdmn "github.com/pblumer/temis/dmn"
)

// Registry holds the DMN models Atlas has deployed. It compiles each model once
// with temis and keeps the immutable result, keyed by the deployment it belongs
// to, ready for cheap repeated evaluation.
//
// Two kinds of deployment put a model here, and the key space is the same one
// (the server's definition keys), so one map serves both:
//
//   - a **process deployment** bundles the models its business rule tasks need
//     and registers them under the process-definition key (ADR-0014);
//   - a **decision deployment** is a decision published in its own right, durable
//     and versioned, under a key of its own
//     (ADR-0319).
//
// A Registry is safe for concurrent evaluation once populated. Populate it (via
// Deploy / DeployDecision) before the processes that use it start running.
type Registry struct {
	engine *tdmn.Engine
	// definitions maps a deployment key to the compiled DMN models registered under
	// it. A process may reference decisions from several models (its business rule
	// tasks are not confined to one), so each key holds a list, appended to by Deploy
	// and searched by decision id at evaluation time.
	definitions map[uint64][]registered
	// latest maps a decision id to the newest model registered that provides it, of
	// either kind. It exists for one reason only: definitions deployed before
	// deploy-time pinning still resolve latest binding at task activation
	// (ADR-0063), and they must keep resolving it exactly as they did. Every
	// register overwrites it, so replaying deployments oldest-first rebuilds it
	// deterministically. New deployments never read it.
	latest map[string]registered
	// latestDecision maps a decision id to the newest *decision deployment* key
	// providing it — the deploy-time selector a latest-bound business rule task is
	// pinned against. Only registerDecision writes it, so bundling a model with a
	// process can no longer make that process the newest version of a decision.
	latestDecision map[string]uint64
	// decisionKeys is the set of keys registered as *decision* deployments rather
	// than as models bundled with a process. Only UndeployDecision reads it, and it
	// reads it for one reason: after removing a key, latestDecision has to be
	// rebuilt from what is left, and only these keys may write it.
	decisionKeys map[uint64]bool
}

// registered is one compiled model together with what was worked out about it at
// registration time, because the document it came from is only in hand there
// (invariant I5: settle it at deploy time, do not re-derive it per evaluation).
type registered struct {
	defs *tdmn.Definitions
	// names is every name a business rule task's decisionId may carry for this
	// model: its decisions under both of their names (names.go) and its decision
	// services (services.go).
	names []string
	// services describes the decision services this model publishes. temis exposes
	// no listing of them, so they are read from the document — which the registry
	// has and an evaluation does not.
	services []DecisionInfo
	// graph is the model's requirements graph, drawn once here for the same reason
	// the names are resolved here: the document is only in hand at registration, and
	// the surface that wants the picture — an operator asking how a decision was
	// reached — reaches shared state through the run loop, which is no place to
	// compile a model (invariants I1/I5).
	graph ModelGraph
}

// NewRegistry creates an empty registry over a fresh temis engine.
func NewRegistry() *Registry {
	return &Registry{
		engine:         tdmn.New(),
		definitions:    map[uint64][]registered{},
		latest:         map[string]registered{},
		latestDecision: map[string]uint64{},
		decisionKeys:   map[uint64]bool{},
	}
}

// Deploy compiles a DMN model and registers it under the process-definition key of
// the process whose business rule tasks reference it. A process may bundle several
// models (its tasks can call decisions from different models), so Deploy appends —
// call it once per bundled model. Compilation happens here, at deploy time, never
// at evaluation time (invariant I5). It returns an error if temis cannot parse or
// compile the model.
//
// It does *not* make this model the newest version of the decisions it provides:
// that is [Registry.DeployDecision]'s job, and keeping the two apart is what stops
// deploying a process from silently becoming "a new version of the decision"
// (ADR-0319). The one index a bundled
// model still moves is the legacy runtime-latest pointer the definitions deployed
// before pinning read — see the Registry field comments.
func (r *Registry) Deploy(defKey uint64, dmnXML []byte) error {
	defs, diags, err := r.engine.Compile(context.Background(), dmnXML)
	if err != nil {
		return fmt.Errorf("dmn: compile model for def %d: %w", defKey, err)
	}
	if diags.HasErrors() {
		return fmt.Errorf("dmn: model for def %d has errors: %v", defKey, diags)
	}
	r.register(defKey, defs, dmnXML)
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
	r.register(defKey, defs, dmnXML)
	if !diags.HasErrors() {
		return "", nil
	}
	return formatDiagnostics(diags), nil
}

// register indexes a compiled model under the deployment key it was registered
// with, and as the newest model providing every decision it declares — the legacy
// pointer only pre-pinning definitions read (ADR-0063). Shared by Deploy, Reload
// and registerDecision so every accepted model is indexed identically.
func (r *Registry) register(defKey uint64, defs *tdmn.Definitions, src []byte) registered {
	reg := registered{defs: defs, services: describeServices(defs, src), graph: modelGraph(r.engine, defs, src)}
	reg.names = append(addressableDecisions(defs), serviceNames(reg.services)...)
	r.definitions[defKey] = append(r.definitions[defKey], reg)
	for _, id := range reg.names {
		r.latest[id] = reg
	}
	return reg
}

// DeployDecision compiles a DMN model published as a decision deployment — a
// decision in its own right rather than a model bundled with some process — and
// registers it under its own definition key
// (ADR-0319). It additionally makes that
// key the newest deployed version of every decision the model provides, which is
// what a latest-bound business rule task is pinned against when its process is
// deployed.
//
// Like Deploy it is the deploy-time gate: a model temis cannot compile, or whose
// diagnostics carry errors, is refused and leaves nothing behind (invariant I5 —
// compilation happens here, never at evaluation time).
func (r *Registry) DeployDecision(key uint64, dmnXML []byte) error {
	defs, diags, err := r.engine.Compile(context.Background(), dmnXML)
	if err != nil {
		return fmt.Errorf("dmn: compile decision %d: %w", key, err)
	}
	if diags.HasErrors() {
		return fmt.Errorf("dmn: decision %d has errors: %v", key, diags)
	}
	r.registerDecision(key, defs, dmnXML)
	return nil
}

// ReloadDecision is DeployDecision for a decision that is *already* deployed —
// one coming back from its durable record at startup — and so it does not
// re-apply the deploy-time gate, for the reason [Registry.Reload] gives
// (ADR-0177): refusing it here undeploys nothing, it only keeps the server from
// starting. The rendered error diagnostics are returned ("" when there are none)
// for the caller to report.
func (r *Registry) ReloadDecision(key uint64, dmnXML []byte) (string, error) {
	defs, diags, err := r.engine.Compile(context.Background(), dmnXML)
	if err != nil {
		return "", fmt.Errorf("dmn: compile decision %d: %w", key, err)
	}
	r.registerDecision(key, defs, dmnXML)
	if !diags.HasErrors() {
		return "", nil
	}
	return formatDiagnostics(diags), nil
}

// registerDecision indexes a decision deployment: under its key like any other
// model, and as the newest deployed version of every decision it declares. Shared
// by DeployDecision and ReloadDecision so both index identically.
func (r *Registry) registerDecision(key uint64, defs *tdmn.Definitions, src []byte) {
	reg := r.register(key, defs, src)
	r.decisionKeys[key] = true
	for _, id := range reg.names {
		r.latestDecision[id] = key
	}
}

// UndeployDecision removes a decision deployment from the registry and leaves the
// two "newest model providing this decision" pointers exactly as a restart would
// build them — which is the only definition of correct available here
// (ADR-0336).
//
// Removing the key is the easy half. The hard half is that both pointers are
// last-write-wins: each holds *the newest* provider and keeps no history, so a
// pointer aimed at the removed key cannot simply be cleared — it has to fall back
// to the next-newest, and nothing here remembers which that was.
//
// It is rebuilt rather than remembered. Keys come from one monotonic counter, so
// ascending key order *is* registration order, live and on recovery alike; walking
// the survivors in that order reproduces exactly the state the same records would
// produce after a restart. That is what makes a delete survivable: what the server
// answers now and what it answers after rebooting cannot diverge.
//
// Removing a key that is not a decision deployment — a process's bundled model, or
// nothing at all — is a no-op, so an unknown key is not an error here.
func (r *Registry) UndeployDecision(key uint64) {
	if !r.decisionKeys[key] {
		return
	}
	delete(r.decisionKeys, key)
	delete(r.definitions, key)

	keys := make([]uint64, 0, len(r.definitions))
	for k := range r.definitions {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	r.latest = make(map[string]registered, len(r.latest))
	r.latestDecision = make(map[string]uint64, len(r.latestDecision))
	for _, k := range keys {
		for _, reg := range r.definitions[k] {
			for _, id := range reg.names {
				// register's rule: every accepted model, of either kind, moves the legacy
				// pointer.
				r.latest[id] = reg
				// registerDecision's rule: only a decision deployment moves the pinning
				// selector.
				if r.decisionKeys[k] {
					r.latestDecision[id] = k
				}
			}
		}
	}
}

// LatestDecisionKey returns the key of the newest decision deployment providing
// the decision id, and ok=false when the decision has never been deployed on its
// own. It is the deploy-time selector behind latest binding: a process deployment
// resolves each latest-bound reference through it once and stores the answer, so
// nothing is re-decided at task activation or on replay (invariants I5/I6).
//
// It reads registry state, so it runs on the registry's owning goroutine — the
// run loop — the same discipline as Deploy.
func (r *Registry) LatestDecisionKey(decisionId string) (uint64, bool) {
	key, ok := r.latestDecision[decisionId]
	return key, ok
}

// LatestDecisionIDs returns the set of decision ids that have a decision
// deployment — the ones a latest-bound reference resolves to something other than
// the model bundled with its own process
// (ADR-0327).
//
// The deploy-time gate reads it to tell a business rule task that can evaluate
// from one that cannot: a latest-bound reference in this set needs no model
// bundled, because pinDecisions will resolve it to that deployment's key. It
// reads registry state, so it runs on the registry's owning goroutine — the run
// loop — and the caller hands the answer to the off-loop preflight.
func (r *Registry) LatestDecisionIDs() map[string]bool {
	out := make(map[string]bool, len(r.latestDecision))
	for id := range r.latestDecision {
		out[id] = true
	}
	return out
}

// modelProviding returns the model in the list that provides the decision id, or
// nil if none does — how a deployment-bound evaluation finds the bundled model that
// declares its decision when a process bundles several.
func modelProviding(list []registered, decisionId string) *tdmn.Definitions {
	if reg := regProviding(list, decisionId); reg != nil {
		return reg.defs
	}
	return nil
}

// regProviding is modelProviding over the whole registration, for the callers that
// want what was worked out about the model rather than the model itself.
func regProviding(list []registered, decisionId string) *registered {
	for i, reg := range list {
		for _, id := range reg.names {
			if id == decisionId {
				return &list[i]
			}
		}
	}
	return nil
}

// Graph returns the requirements graph of the model that the deployment under
// defKey evaluates decisionId from — the picture of the decision as the evaluation
// saw it, not as the design-time model reads today. It is the graph frozen at
// registration, so this is a map lookup: it runs on the run loop, where compiling
// anything is out of the question (invariants I1/I5).
//
// defKey 0, or a key whose deployment is gone, falls back to the newest model
// providing the decision. That is the same fallback a latest-bound task evaluates
// under, and it is honest about what it can still answer: a process definition
// deleted after its instances ran leaves evaluations behind whose model is no
// longer addressable any other way. The second return is false when nothing on this
// server provides the decision at all.
func (r *Registry) Graph(defKey uint64, decisionId string) (ModelGraph, bool) {
	if reg := regProviding(r.definitions[defKey], decisionId); reg != nil {
		return reg.graph, true
	}
	if reg, ok := r.latest[decisionId]; ok {
		return reg.graph, true
	}
	return ModelGraph{Nodes: []GraphNode{}, Edges: []GraphEdge{}}, false
}

// IsService reports whether the name addresses a decision *service* — DMN's
// published interface over part of the graph — rather than one decision in it, in
// the model the deployment under defKey resolves it from. It answers one question
// a reader of an evaluation cannot otherwise settle: a service evaluation retains
// no rule trace (ADR-0398), and "no rules were recorded" and "no rules ran" are
// not the same thing to say to somebody asking how a case was decided.
func (r *Registry) IsService(defKey uint64, decisionId string) bool {
	reg := regProviding(r.definitions[defKey], decisionId)
	if reg == nil {
		if latest, ok := r.latest[decisionId]; ok {
			reg = &latest
		}
	}
	if reg == nil {
		return false
	}
	for _, svc := range reg.services {
		if svc.ID == decisionId {
			return true
		}
	}
	return false
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
	reg, ok := r.latest[decisionId]
	if !ok {
		return nil, nil, fmt.Errorf("dmn: no model deployed providing decision %q", decisionId)
	}
	return evalDecision(ctx, reg.defs, decisionId, in, "the latest deployed model")
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
	// Service marks a decision service — the published interface over part of a
	// model rather than one decision in it. A task calls either the same way.
	Service bool
}

// DeployedDecisions describes every decision provided by the newest deployed model
// that supplies it (ADR-0063 latest binding), for the Modeler's picker. It reads the
// registry's already-compiled models — no XML parsing or resolver I/O — so it must
// run on the registry's owning goroutine (the run loop), the same single-writer
// discipline as Deploy. Results are sorted by decision id for a stable picker.
func (r *Registry) DeployedDecisions() []DeployedDecision {
	out := make([]DeployedDecision, 0, len(r.latest))
	for id, reg := range r.latest {
		// A decision service is offered beside the decisions, because a business rule
		// task calls either the same way (services.go).
		described := append(describeDecisions(reg.defs), reg.services...)
		for _, di := range described {
			if di.ID != id {
				continue // describe the one decision this model is latest for
			}
			out = append(out, DeployedDecision{
				ID:      di.ID,
				Name:    di.Name,
				Model:   reg.defs.ModelName(),
				Inputs:  di.Inputs,
				Output:  di.Output,
				Service: di.Service,
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
		// A decision service is addressed by the same `decisionId` and answers the same
		// way, so it is tried here rather than at every caller
		// (ADR-0398). A decision is
		// preferred only to settle the case the deploy gate refuses — one model giving
		// the same name to both — for a model that reached the registry some other way,
		// such as a reload of an older artifact.
		if svc, serviceErr := defs.Service(decisionId); serviceErr == nil {
			return evalService(ctx, defs, svc, decisionId, in, where)
		}
		return nil, nil, fmt.Errorf("dmn: decision %q in %s: %w", decisionId, where, err)
	}
	// The DRG nodes carry both of a decision's names and both of an input's, and
	// both steps around the evaluation read them, so the graph is built once here.
	nodes := defs.Graph().Nodes
	// An input the model labels differently from the identifier it binds is
	// accepted under either spelling, so a task deployed before temis told the two
	// apart still finds its input (names.go).
	aliased := aliasedInputs(nodes, in)
	if err := refuseTypeMismatch(defs, decisionId, aliased, where); err != nil {
		return nil, nil, err
	}
	res, err := dec.Evaluate(ctx, tdmn.Input(aliased), tdmn.WithTrace())
	if err != nil {
		return nil, nil, fmt.Errorf("dmn: evaluate %q in %s: %w", decisionId, where, err)
	}
	// temis returns a FEEL number as its exact decimal string; the model's own
	// declarations say which of these strings are numbers. Restored here, at the
	// one point every caller passes through, so the variable a business rule task
	// writes, the try-a-decision answer and the retained record cannot disagree
	// (numbers.go).
	outputs := restoreNumbers(defs, nodes, decisionId, res.Outputs)
	var trace []byte
	if res.Trace != nil {
		// The trace tree carries JSON tags as its wire contract (temis dmn/trace.go);
		// a marshal failure would only mean an unexpected value shape, so degrade to no
		// trace rather than failing the evaluation the token depends on.
		if b, err := json.Marshal(res.Trace); err == nil {
			trace = b
		}
	}
	return outputs, trace, nil
}

// refuseTypeMismatch stops an evaluation whose input does not carry the type the
// model declares for it (ADR-0419).
//
// The failure it prevents is the one this whole family of changes is about. A
// value of the wrong type does not raise anything in FEEL: it makes every
// comparison that reads it null, so no rule matches, the catch-all row answers,
// and the token carries on with a plausible wrong result that nothing downstream
// can tell from a right one. There is no diagnostic and no trace entry to find it
// by — only a process that went the other way. Refusing is what turns that into
// something somebody sees: the handler returns the error, the job fails, and its
// retries run out into an incident (ADR-0061).
//
// It validates against the decision's *reachable* inputs, not its directly
// declared ones, and that distinction is the difference between a check that
// works and one that only looks like it. A business rule task supplies the leaf
// inputs of a whole requirements cone: for `Kreditentscheid`, which requires only
// the decisions `Bonität` and `Tragbarkeit`, the task sends `betrag`,
// `laufzeitMonate`, `einkommen` and `zahlungsstoerungen` — and that decision
// declares none of them directly. Its own InputSchema is empty, so a check built
// on it would pass every input of every layered model without looking at one,
// which is exactly the shape a well-factored DRG has at the top.
//
// Only TYPE_MISMATCH, deliberately, though temis reports four codes:
//
//   - MISSING_INPUT would be redundant. temis already refuses a missing required
//     input from Evaluate itself, as MISSING_REQUIRED_INPUT, and names the input
//     it wanted; doing it here first would only replace that message with a worse
//     one.
//   - UNKNOWN_INPUT stays ignored. A business rule task's io-mapping may carry a
//     row the decision does not declare — one mapping shared across decisions, or
//     a row left behind when a column went away — and temis simply does not read
//     it. Failing the job over a value that costs nothing would break processes
//     that run correctly today.
//   - VALUE_NOT_ALLOWED is a different decision than this record made. It is a
//     value question, not a type question, and a model can constrain an input more
//     narrowly than any deployed task knows; it is left for its own record.
//
// Every mismatch is named rather than only the first, so an operator reading an
// incident sees the whole picture instead of fixing one input and meeting the
// next.
func refuseTypeMismatch(defs *tdmn.Definitions, decisionId string, in map[string]any, where string) error {
	probs, err := defs.ValidateReachableInput(decisionId, tdmn.Input(in))
	if err != nil {
		// The decision was resolved a few lines up, so this cannot be "no such
		// decision" in practice; treating it as "nothing to check" keeps a future
		// engine change from turning a working evaluation into a failed job.
		return nil
	}
	var bad []string
	for _, p := range probs {
		if p.Code != "TYPE_MISMATCH" {
			continue
		}
		bad = append(bad, p.Message)
	}
	if len(bad) == 0 {
		return nil
	}
	return fmt.Errorf("dmn: evaluate %q in %s: %s", decisionId, where, strings.Join(bad, "; "))
}

// evalService evaluates a decision service — DMN's published interface over part
// of a model. The caller supplies the service's input data *and* the results of
// its input decisions, which it does not compute; everything else behind the
// interface is evaluated internally and stays invisible here.
//
// One thing differs from a decision, and it is the engine's shape rather than a
// choice made here: its outputs are keyed by **output-decision name**, so each
// value's declared type is that decision's — which is why the restoration runs
// once per key (numbers.go).
//
// A service is traced like a decision. It was not, for as long as temis's
// service evaluation took no option to trace with: a task that called a model
// through its published interface retained its inputs and outputs and nothing
// about how it got there, which is the one silence ADR-0066 exists to prevent.
// The option landed upstream (temis#226) and is threaded through here, so an
// evaluation made from today carries the tables the service ran behind the
// interface. The boundary holds in the trace as it does in the result: an input
// decision is supplied rather than computed, so its table never ran and never
// appears.
//
// Evaluations recorded *before* that still carry no trace, and nothing here can
// give them one — the record is frozen history (ADR-0066), not a thing to
// recompute. A reader of an old service record still sees exact values and no
// rules, and the surfaces say so.
func evalService(ctx context.Context, defs *tdmn.Definitions, svc *tdmn.CompiledService, name string, in map[string]any, where string) (map[string]any, []byte, error) {
	nodes := defs.Graph().Nodes
	res, err := svc.Evaluate(ctx, tdmn.Input(aliasedInputs(nodes, in)), tdmn.WithTrace())
	if err != nil {
		return nil, nil, fmt.Errorf("dmn: evaluate service %q in %s: %w", name, where, err)
	}
	outputs := make(map[string]any, len(res.Outputs))
	for key, v := range res.Outputs {
		one := restoreNumbers(defs, nodes, key, map[string]any{key: v})
		outputs[key] = one[key]
	}
	var trace []byte
	if res.Trace != nil {
		// Same degradation as a decision's: a marshal failure would only mean an
		// unexpected value shape, so it costs the trace rather than the evaluation
		// the token depends on.
		if b, err := json.Marshal(res.Trace); err == nil {
			trace = b
		}
	}
	return outputs, trace, nil
}
