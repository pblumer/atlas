package compiler

import (
	"fmt"
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
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, fid := range cp.Outgoing(n) {
			push(cp.Flow(fid).Target)
		}
		for _, be := range cp.BoundaryEvents(n) {
			push(be)
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
		TypeUserTask, TypeConnectorTask, TypeTask:
		return true
	default:
		return false
	}
}
