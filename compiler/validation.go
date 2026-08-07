package compiler

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// This file is compiler stage 5 (compiler.md): graph-wide validation. Stages 1–4
// catch structural faults one element at a time as they are parsed (a bad FEEL
// expression, an unknown flow reference, an unsupported element) and fail on the
// first one. Stage 5 runs over the *linearized* graph, where topology is knowable,
// and reports every problem it finds at once — the shape ADR-0026's Problems panel
// and the future POST /api/v1/validate dry-run need.
//
// Invariant I5 ("compile, don't interpret"): every check here runs at deploy time
// on the immutable CompiledProcess. Nothing added here touches the processor hot
// path, applyToState, or runtime behavior — a model that compiled and ran before
// runs identically; validation only decides, at deploy, whether it is allowed to.

// Severity ranks a validation Problem. An error refuses deployment (compileProcess
// returns it as a fatal compile error, preserving the "fail at deploy, never at
// runtime" contract); a warning is informational and does not block a deploy. The
// string values are stable and chosen so the future JSON /validate endpoint and
// Problems panel (ADR-0026) can serialize them directly.
type Severity string

const (
	// SeverityError marks a problem that makes the model unrunnable or structurally
	// invalid, so the deploy is refused — the existing all-or-nothing compile-gate
	// behavior, now with a reason attached.
	SeverityError Severity = "error"
	// SeverityWarning marks a modeling smell that does not prevent the reachable
	// part of the process from executing correctly (e.g. dead, unreachable code).
	// It is surfaced to the author but never blocks a deploy.
	SeverityWarning Severity = "warning"
)

// Rule identifiers are stable machine slugs for the check that produced a Problem,
// so a UI can group, filter, or link to documentation by rule without parsing the
// human-readable Message (which is deliberately not a stable API). They are grouped
// by the three validation families of ROADMAP Milestone 1: reachability, gateway
// coverage, and scope consistency.
const (
	RuleReachability           = "reachability"
	RuleGatewayNoOutgoing      = "gateway.no-outgoing"
	RuleGatewayNoIncoming      = "gateway.no-incoming"
	RuleGatewayMultipleDefault = "gateway.multiple-default"
	RuleGatewayMissingDefault  = "gateway.missing-default"
	RuleBoundaryIncomingFlow   = "boundary.incoming-flow"
	RuleBoundaryInvalidHost    = "boundary.invalid-host"
	RuleFlowCrossScope         = "flow.cross-scope"
	// RuleErrorUnhandled marks an error end event with no statically matching enclosing
	// error boundary or error event subprocess in the same process (ADR-0089). A warning,
	// not an error: the catch may live at a call-activity caller one process cannot see,
	// and the runtime incident is the real terminal for a truly uncaught error.
	RuleErrorUnhandled = "error.unhandled"
	// RuleCancelEndOutsideTransaction marks a cancel end event whose enclosing scope is not a
	// transaction — an error, since BPMN allows a cancel end only within a <transaction> (ADR-0105).
	RuleCancelEndOutsideTransaction = "cancel.end-outside-transaction"
	// RuleCancelBoundaryInvalidHost marks a cancel boundary attached to something other than a
	// transaction — an error, since a cancel boundary may attach only to a <transaction> (ADR-0105).
	RuleCancelBoundaryInvalidHost = "cancel.boundary-invalid-host"
	// RuleTransactionNoCancelBoundary marks a transaction that has a cancel end event but no
	// cancel boundary (ADR-0105). A warning: the cancellation tears the transaction down with
	// no recovery route, usually a modeling mistake, but not structurally invalid.
	RuleTransactionNoCancelBoundary = "transaction.no-cancel-boundary"
)

// Rule slugs for whole-model dry-run findings that [ValidateModel] raises outside
// the per-node graph checks — a fault that stops the compile before a linearized
// graph exists, so it cannot be anchored the way the graph rules above are.
const (
	// RuleParse marks a document that will not decode at all, or a model with no
	// executable process — a model-level failure with no single owning element.
	RuleParse = "parse"
	// RuleCompile marks a per-process failure in an earlier compile stage (an
	// unknown flow reference, a bad FEEL expression) — the pool named nothing the
	// graph checks could inspect, so its error is surfaced as one Problem instead.
	RuleCompile = "compile"
)

// Problem is one structured validation finding on a compiled process, shaped for
// ADR-0026's Problems panel and the future POST /api/v1/validate endpoint. Element
// is the source BPMN element id it anchors to (the id bpmn-js uses, e.g.
// "Gateway_1"; "" for a process-level finding or a node compiled without a source
// id); Severity ranks it; Rule is the stable machine slug of the check that raised
// it; Message is a human-readable explanation.
type Problem struct {
	Element  string   `json:"element"`
	Severity Severity `json:"severity"`
	Rule     string   `json:"rule"`
	Message  string   `json:"message"`
}

// Validate runs the compiler's graph-wide checks (compiler.md stage 5, ROADMAP
// Milestone 1) over a linearized CompiledProcess and returns every structured
// Problem it finds — not just the first — so a Problems panel can list them all in
// one pass. It never mutates cp and is safe to call concurrently on an immutable
// process. compileProcess calls it as the final compile stage and refuses the
// deploy when [HasErrors] holds; the future /validate dry-run returns the full
// list (errors and warnings) verbatim.
//
// Problems are returned in a deterministic order — by check family, then by node
// or flow index within each — so a caller (and a test) sees a stable sequence.
func Validate(cp *CompiledProcess) []Problem {
	var ps []Problem
	ps = append(ps, checkReachability(cp)...)
	ps = append(ps, checkGateways(cp)...)
	ps = append(ps, checkScopes(cp)...)
	ps = append(ps, checkErrorHandling(cp)...)
	ps = append(ps, checkTransactions(cp)...)
	return ps
}

// HasErrors reports whether any Problem is error severity — the condition under
// which a deploy is refused. Warnings alone leave a model deployable.
func HasErrors(ps []Problem) bool {
	for i := range ps {
		if ps[i].Severity == SeverityError {
			return true
		}
	}
	return false
}

// ValidateModel runs the compiler's real parse → resolve → build → validate
// pipeline over a BPMN model as a *dry run* — it mints no keys, registers no
// definition, and starts no instance — and returns every validation Problem
// (errors and warnings) across all of the model's executable pools. It is the
// single source of validation truth behind ADR-0026's Problems panel and the
// POST /api/v1/validate endpoint: the panel never re-implements these rules
// (that would be the interpret-don't-compile failure mode I5 forbids), it renders
// what this returns.
//
// Unlike ParseAll, which stops at the first fault so a deploy fails fast, the dry
// run reports everything at once — that is what a Problems panel needs. Faults the
// graph checks cannot anchor to a node still surface as Problems so the panel
// renders them uniformly: a document that will not parse, or a model with no
// executable process, becomes one RuleParse error; a pool that fails an earlier
// compile stage becomes one RuleCompile error rather than aborting the whole run
// and blinding the panel to the other pools.
//
// The returned error is always nil today — every modeling fault is reported as a
// Problem, not an error — but the signature keeps an error so a future source
// that does I/O can report a read failure distinctly from a modeling one.
func ValidateModel(r io.Reader) ([]Problem, error) {
	defs, err := decodeDefinitions(r)
	if err != nil {
		return []Problem{{Severity: SeverityError, Rule: RuleParse, Message: err.Error()}}, nil
	}
	resolveMsg := buildMessageResolver(defs)
	resolveSig := buildSignalResolver(defs)
	resolveErr := buildErrorResolver(defs)
	var ps []Problem
	executable := 0
	for _, proc := range defs.Processes {
		if len(proc.StartEvents) == 0 {
			continue // black-box pool: nothing to run, so nothing to validate (ParseAll skips it too)
		}
		// The key is irrelevant to a dry run — the compiled process is inspected and
		// discarded, never registered — so a per-pool ordinal keeps it deterministic
		// without touching the server's key counter.
		cp, cerr := compileProcess(uint64(executable), 1, proc, resolveMsg, resolveSig, resolveErr)
		executable++
		if cerr != nil {
			// A graph-level failure carries its element-anchored Problems; hand them
			// through verbatim. Any other compile error stopped before the graph
			// existed, so report it as one unanchored compile Problem.
			var ve *ValidationError
			if errors.As(cerr, &ve) {
				ps = append(ps, ve.Problems...)
				continue
			}
			ps = append(ps, Problem{Severity: SeverityError, Rule: RuleCompile, Message: cerr.Error()})
			continue
		}
		ps = append(ps, Validate(cp)...)
	}
	if executable == 0 {
		ps = append(ps, Problem{
			Severity: SeverityError,
			Rule:     RuleParse,
			Message:  "no executable <process> (a process needs a start event)",
		})
	}
	return ps, nil
}

// ValidationError is the fatal compile error compileProcess returns when
// graph-wide validation finds an error-severity Problem, so a deploy is refused
// (invariant I5, preserving today's compile-gate behavior). It carries the full
// Problem list — warnings included — so a caller that wants the structured findings
// (a future /validate endpoint reusing the compile path) can recover them with a
// type assertion; Error() renders only the error-severity findings into one line,
// matching how the other compile failures read.
type ValidationError struct {
	Problems []Problem
}

func (e *ValidationError) Error() string {
	var b strings.Builder
	b.WriteString("compiler: validation failed:")
	for _, p := range e.Problems {
		if p.Severity != SeverityError {
			continue
		}
		fmt.Fprintf(&b, " %s (%s)", p.Message, p.Rule)
	}
	return b.String()
}

// checkReachability flags every flow node not reachable from a start event by
// following sequence flows — dead code (compiler.md). It is a warning, not an
// error: an unreachable element cannot break the reachable part of the process
// (no token ever enters it), so it is a modeling smell worth surfacing but not a
// reason to refuse a deploy. A boundary event is reachable when its host is (a
// token never flows *into* a boundary; it is armed by the running host), so the
// walk treats a reached host's attached boundary events as reached and continues
// from their outgoing flows.
func checkReachability(cp *CompiledProcess) []Problem {
	reachable := make([]bool, len(cp.nodes))
	var stack []int32
	push := func(id int32) {
		if !reachable[id] {
			reachable[id] = true
			stack = append(stack, id)
		}
	}
	for _, s := range cp.startEvents {
		push(s)
	}
	// An event subprocess is reached via its start event's trigger, not by a token
	// flowing in, so seed each event-subprocess container as a reachability root — the
	// subprocess case below then reaches its inner nodes (ADR-0082).
	for id := range cp.nodes {
		if cp.nodes[id].EventSub >= 0 {
			push(int32(id))
		}
	}
	// A compensation handler is reached when its compensation throw runs, not by a token
	// flowing in (it sits off the normal flow, isForCompensation), so seed each handler —
	// the activity a compensation boundary links to — as a reachability root (ADR-0103).
	for i := range cp.boundaryEventDets {
		if d := &cp.boundaryEventDets[i]; d.Kind == BoundaryCompensation && d.CompensationHandler >= 0 {
			push(d.CompensationHandler)
		}
	}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, fid := range cp.Outgoing(n) {
			push(cp.Flow(fid).Target)
		}
		for _, be := range cp.BoundaryEvents(n) {
			push(be)
		}
		// A token entering a subprocess enters its scope through the subprocess's
		// own start event(s), so a reached subprocess makes its inner starts reached
		// and the walk continues from there (ADR-0074).
		if cp.nodes[n].Type == TypeSubProcess {
			for id := range cp.nodes {
				if isStartEvent(cp.nodes[id].Type) && cp.nodes[id].FlowScope == n {
					push(int32(id))
				}
			}
		}
	}
	var ps []Problem
	for id := range cp.nodes {
		if !reachable[id] {
			ps = append(ps, Problem{
				Element:  cp.ElementBpmnId(int32(id)),
				Severity: SeverityWarning,
				Rule:     RuleReachability,
				Message:  fmt.Sprintf("%s is not reachable from a start event", describeNode(cp, int32(id))),
			})
		}
	}
	return ps
}

// checkGateways validates gateway coverage: a gateway with nowhere to route a
// token (no outgoing) or that nothing can reach (no incoming) is a structural
// error, and a data-based (exclusive/inclusive) split's condition/default coverage
// is checked by checkDataGatewayCoverage.
func checkGateways(cp *CompiledProcess) []Problem {
	var ps []Problem
	for id := range cp.nodes {
		n := &cp.nodes[id]
		if !isGateway(n.Type) {
			continue
		}
		// A gateway must both be reachable by a token and have somewhere to send it;
		// either missing side leaves a token dead-ended (split) or the gateway
		// unreachable (join). Both are errors: unlike dead code, a broken gateway on
		// a live path deadlocks the instance at runtime, so it must fail at deploy.
		if n.OutgoingCount == 0 {
			ps = append(ps, problem(cp, int32(id), SeverityError, RuleGatewayNoOutgoing,
				fmt.Sprintf("%s has no outgoing sequence flow", describeNode(cp, int32(id)))))
		}
		if n.IncomingCount == 0 {
			ps = append(ps, problem(cp, int32(id), SeverityError, RuleGatewayNoIncoming,
				fmt.Sprintf("%s has no incoming sequence flow", describeNode(cp, int32(id)))))
		}
		if n.Type == TypeExclusiveGateway || n.Type == TypeInclusiveGateway {
			ps = append(ps, checkDataGatewayCoverage(cp, int32(id))...)
		}
	}
	return ps
}

// checkDataGatewayCoverage validates the outgoing-flow coverage of one
// exclusive/inclusive gateway split (compiler.md). Two findings:
//
//   - More than one default flow is an error: the runtime picks "the" default when
//     no condition holds, and two candidates is a contradiction the model must
//     resolve before it deploys. (The XML front end cannot express this — the
//     `default` attribute is single-valued — but the programmatic Builder can, and
//     it is cheap to guard.)
//   - Conditional flows with no default and no unconditional fallback is a warning:
//     if no condition holds at runtime the token has nowhere to go and the branch
//     deadlocks. It is only a warning because whether a condition holds is
//     data-dependent and unknowable at deploy — the model may be correct for every
//     real input — so it is surfaced but does not refuse the deploy.
func checkDataGatewayCoverage(cp *CompiledProcess, id int32) []Problem {
	var defaults, conditional, unconditional int
	for _, fid := range cp.Outgoing(id) {
		f := cp.Flow(fid)
		switch {
		case f.Default:
			defaults++
		case f.Condition != nil:
			conditional++
		default:
			unconditional++
		}
	}
	var ps []Problem
	if defaults > 1 {
		ps = append(ps, problem(cp, id, SeverityError, RuleGatewayMultipleDefault,
			fmt.Sprintf("%s has %d default flows, at most one is allowed", describeNode(cp, id), defaults)))
	}
	if defaults == 0 && conditional > 0 && unconditional == 0 {
		ps = append(ps, problem(cp, id, SeverityWarning, RuleGatewayMissingDefault,
			fmt.Sprintf("%s has only conditional flows and no default flow; a token deadlocks if no condition holds", describeNode(cp, id))))
	}
	return ps
}

// checkScopes validates scope consistency (compiler.md). Today the flat model puts
// every flow node at the process root (FlowScope -1) — embedded subprocesses are a
// Milestone 3 concern — so the load-bearing checks now are the two boundary-event
// structural rules, with the same-scope sequence-flow check written to hold as the
// scope model grows. Cross-pool sequence flows need no check here: ParseAll
// compiles each pool into its own CompiledProcess, so a flow crossing a pool
// boundary already fails stage 2 as an unknown sourceRef/targetRef.
func checkScopes(cp *CompiledProcess) []Problem {
	var ps []Problem
	for id := range cp.nodes {
		n := &cp.nodes[id]
		if n.Type != TypeBoundaryEvent {
			continue
		}
		// A boundary event is triggered by its host, never by a token flowing into
		// it, so an incoming sequence flow is structurally invalid BPMN — an error.
		if n.IncomingCount > 0 {
			ps = append(ps, problem(cp, int32(id), SeverityError, RuleBoundaryIncomingFlow,
				fmt.Sprintf("%s is a boundary event and must have no incoming sequence flow", describeNode(cp, int32(id)))))
		}
		// A boundary event must attach to an activity — the only element a token
		// dwells in long enough to arm it. Attached to a gateway or event it could
		// never fire, so the attachment is an error rather than dead metadata.
		host := cp.boundaryEventDets[n.Detail].HostNode
		if !isActivity(cp.nodes[host].Type) {
			ps = append(ps, problem(cp, int32(id), SeverityError, RuleBoundaryInvalidHost,
				fmt.Sprintf("%s attaches to %s, which is not an activity", describeNode(cp, int32(id)), describeNode(cp, host))))
		}
	}
	// A sequence flow connects two elements of the same scope level. With the flat
	// model this holds by construction; the check earns its keep once nested scopes
	// (subprocesses) land, at which point a flow crossing a scope boundary is a real
	// structural error rather than an impossibility.
	for fi := range cp.flows {
		f := &cp.flows[fi]
		if cp.nodes[f.Source].FlowScope != cp.nodes[f.Target].FlowScope {
			ps = append(ps, problem(cp, f.Source, SeverityError, RuleFlowCrossScope,
				fmt.Sprintf("sequence flow from %s crosses a scope boundary to %s", describeNode(cp, f.Source), describeNode(cp, f.Target))))
		}
	}
	return ps
}

// checkErrorHandling warns for each error end event that no enclosing error handler in the
// same process statically catches its code (ADR-0089) — the compile-time shadow of the
// runtime propagateError walk. It is a warning, not an error, on purpose: an error
// unhandled here may still be caught at a call-activity caller (ADR-0076), which one
// process cannot see, and a genuinely uncaught error becomes a runtime incident, not a
// deploy failure. Matching mirrors propagation: a catch matches when its code equals the
// thrown code or the catch is code-less (a catch-all).
func checkErrorHandling(cp *CompiledProcess) []Problem {
	var ps []Problem
	for id := range cp.nodes {
		n := &cp.nodes[id]
		if n.Type != TypeErrorEndEvent {
			continue
		}
		code := cp.errorEnds[n.Detail].ErrorCode
		if errorCaught(cp, n.FlowScope, code) {
			continue
		}
		msg := "error end event throws"
		if code != "" {
			msg += fmt.Sprintf(" %q", code)
		}
		msg += ", but no enclosing error boundary or error event subprocess in this process catches it"
		ps = append(ps, Problem{Element: cp.ElementBpmnId(int32(id)), Severity: SeverityWarning, Rule: RuleErrorUnhandled, Message: msg})
	}
	return ps
}

// errorCaught reports whether an error with the given code, thrown in the scope rooted at
// scope (a subprocess node id, or -1 for the process root), has a statically matching
// enclosing error boundary or error event subprocess (ADR-0089). It walks up the FlowScope
// chain; at each level it checks the scope's error event subprocesses and, for a subprocess
// scope, the error boundaries on that subprocess — the static shadow of propagateError. A
// catch matches by equal code or a code-less catch-all. Bounded by the node count (a scope
// chain has no cycles), so a malformed FlowScope cannot loop forever.
func errorCaught(cp *CompiledProcess, scope int32, code string) bool {
	for steps := 0; steps <= len(cp.nodes); steps++ {
		handlers := cp.RootEventSubprocesses()
		if scope >= 0 {
			handlers = cp.EventSubprocesses(scope)
		}
		for _, h := range handlers {
			d := cp.EventSubProcess(cp.nodes[h].EventSub)
			if d.Kind == BoundaryError && errorCodeMatches(d.ErrorCode, code) {
				return true
			}
		}
		if scope < 0 {
			return false // reached the process root with no catch
		}
		for _, be := range cp.BoundaryEvents(scope) {
			d := cp.BoundaryEvent(cp.nodes[be].Detail)
			if d.Kind == BoundaryError && errorCodeMatches(d.ErrorCode, code) {
				return true
			}
		}
		scope = cp.nodes[scope].FlowScope
	}
	return false
}

// checkTransactions validates the transaction constructs (ADR-0105). A cancel end event must
// sit directly inside a transaction, and a cancel boundary may attach only to a transaction —
// both errors (BPMN restricts them so, and the runtime relies on it). A transaction that has a
// cancel end event but no cancel boundary is a warning: the cancellation runs, but the token
// leaves the transaction with no recovery route, usually a modeling mistake.
func checkTransactions(cp *CompiledProcess) []Problem {
	var ps []Problem
	for id := range cp.nodes {
		n := &cp.nodes[id]
		switch {
		case n.Type == TypeCancelEndEvent:
			if n.FlowScope < 0 || !cp.IsTransaction(n.FlowScope) {
				ps = append(ps, problem(cp, int32(id), SeverityError, RuleCancelEndOutsideTransaction,
					"cancel end event is not directly inside a transaction — a cancel end event may appear only within a <transaction>"))
			}
		case n.Type == TypeBoundaryEvent && cp.boundaryEventDets[n.Detail].Kind == BoundaryCancel:
			if !cp.IsTransaction(cp.boundaryEventDets[n.Detail].HostNode) {
				ps = append(ps, problem(cp, int32(id), SeverityError, RuleCancelBoundaryInvalidHost,
					"cancel boundary event is not attached to a transaction — a cancel boundary may attach only to a <transaction>"))
			}
		}
	}
	// A transaction with a cancel end event but no cancel boundary: warn.
	for id := range cp.nodes {
		if !cp.nodes[id].Transaction || !scopeHasCancelEnd(cp, int32(id)) {
			continue
		}
		hasCancelBoundary := false
		for _, be := range cp.BoundaryEvents(int32(id)) {
			if cp.BoundaryEvent(cp.Node(be).Detail).Kind == BoundaryCancel {
				hasCancelBoundary = true
				break
			}
		}
		if !hasCancelBoundary {
			ps = append(ps, problem(cp, int32(id), SeverityWarning, RuleTransactionNoCancelBoundary,
				"transaction has a cancel end event but no cancel boundary — the cancellation would tear the transaction down with no recovery route"))
		}
	}
	return ps
}

// scopeHasCancelEnd reports whether any cancel end event sits directly in the scope rooted at
// the transaction node id.
func scopeHasCancelEnd(cp *CompiledProcess, txID int32) bool {
	for j := range cp.nodes {
		if cp.nodes[j].Type == TypeCancelEndEvent && cp.nodes[j].FlowScope == txID {
			return true
		}
	}
	return false
}

// errorCodeMatches reports whether a catch with catchCode catches a thrown throwCode: an
// equal code, or a code-less catch-all (ADR-0089).
func errorCodeMatches(catchCode, throwCode string) bool {
	return catchCode == "" || catchCode == throwCode
}

// problem builds a Problem anchored to node id, resolving its source BPMN id.
func problem(cp *CompiledProcess, id int32, sev Severity, rule, msg string) Problem {
	return Problem{Element: cp.ElementBpmnId(id), Severity: sev, Rule: rule, Message: msg}
}

// describeNode renders a node for a human-readable message: its type plus its
// source BPMN id when one was recorded (e.g. `ExclusiveGateway "Gateway_1"`),
// falling back to the type alone.
func describeNode(cp *CompiledProcess, id int32) string {
	if bid := cp.ElementBpmnId(id); bid != "" {
		return fmt.Sprintf("%s %q", cp.nodes[id].Type, bid)
	}
	return cp.nodes[id].Type.String()
}

// isGateway reports whether a node type is a gateway — the elements the gateway
// coverage checks apply to.
func isGateway(t BpmnType) bool {
	return t == TypeExclusiveGateway || t == TypeInclusiveGateway || t == TypeParallelGateway
}

// isActivity reports whether a node type is an activity — an element a token
// dwells in and thus a valid boundary-event host. Events and gateways are not
// activities; the undefined/manual pass-through task is (it is structurally an
// activity even though it does not wait).
func isActivity(t BpmnType) bool {
	switch t {
	case TypeServiceTask, TypeScriptTask, TypeScriptJobTask, TypeBusinessRuleTask,
		TypeUserTask, TypeConnectorTask, TypeTask, TypeReceiveTask, TypeSubProcess, TypeCallActivity:
		return true
	default:
		return false
	}
}
