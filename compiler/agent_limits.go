package compiler

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SafeAgentRounds is how many rounds an agent-driven ad-hoc subprocess that states no
// maxIterations gets before the engine parks it on an incident
// (ADR-draft-agent-run-record). It is the standard loop's SafeLoopCeiling applied to a
// loop whose every turn is a paid call to a model: far above what a deliberate agent
// needs, far below what a model that never stops choosing tools would cost. Resolving
// the incident grants the same number again, so the ceiling stops a runaway without
// deciding for the operator how long a legitimate run may be.
const SafeAgentRounds = 25

// The bounds an authored model-call timeout must lie between. The floor is what a call
// cannot be made in; the ceiling is not a recommendation but the point past which the
// value is certainly a mistake — a Worker Instance's lease (five minutes unless it was
// started with another) has to outlast the call, and that is the operator's to set.
const (
	MinAgentTimeout = time.Second
	MaxAgentTimeout = time.Hour
)

// RuleAgentLimit marks a bound on an <atlas:agentConnector> that cannot be honoured: a
// value that is not a positive whole number or a duration, or a bound stated on the
// host that has nothing for it to bound.
const RuleAgentLimit = "agent.limit"

// AgentLimits are the bounds a model states on an <atlas:agentConnector>
// (ADR-0117, as built by ADR-draft-agent-run-record). Zero in any field means "not
// stated", never "none allowed".
//
// MaxRounds and TokenBudget bound an agent-driven container's run and are enforced by
// the engine, which is the only party that sees every round. TimeoutNanos bounds one
// call to the model and travels to the worker, which is the only party that makes it.
//
// Problems holds what was wrong with the authored values. It is kept on the compiled
// detail rather than returned as a build error on purpose: every deployed definition is
// recompiled when the server starts, and a model deployed before these attributes were
// read may carry one this compiler now rejects. Such a model must still load — Validate
// turns each entry into an error that refuses a *deploy*, which is the only place a
// refusal belongs (ADR-0177, ADR-0393).
type AgentLimits struct {
	MaxRounds    int32
	TokenBudget  int64
	TimeoutNanos int64
	Problems     []string
}

// Rounds is how many rounds this container may run before it parks: what its model
// states, or SafeAgentRounds when it states nothing.
func (l AgentLimits) Rounds() uint32 {
	if l.MaxRounds > 0 {
		return uint32(l.MaxRounds)
	}
	return SafeAgentRounds
}

// parseAgentLimits reads the three bounds off an <atlas:agentConnector>. container says
// which host it sits on: an ai task makes one call, so it has no rounds to count and no
// run to budget, and stating either there is reported rather than ignored — an attribute
// nothing reads looks configured for a year (ADR-0256).
func parseAgentLimits(ag *xmlAgentConnector, container bool) AgentLimits {
	var l AgentLimits
	positive := func(attr, raw string) int64 {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return 0
		}
		if !container {
			l.Problems = append(l.Problems, fmt.Sprintf("sets %s, which only an agent-driven ad-hoc subprocess reads: "+
				"an ai task makes one call, so there is no run to bound — bound the call itself with timeout", attr))
			return 0
		}
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 1 {
			l.Problems = append(l.Problems, fmt.Sprintf("has an invalid %s %q (want a positive whole number)", attr, raw))
			return 0
		}
		return n
	}
	if n := positive("maxIterations", ag.MaxIterations); n > 0 {
		if n > 1<<31-1 {
			l.Problems = append(l.Problems, fmt.Sprintf("has an invalid maxIterations %q (too large)", strings.TrimSpace(ag.MaxIterations)))
		} else {
			l.MaxRounds = int32(n)
		}
	}
	l.TokenBudget = positive("tokenBudget", ag.TokenBudget)
	if raw := strings.TrimSpace(ag.Timeout); raw != "" {
		nanos, err := parseISO8601Duration(raw)
		switch {
		case err != nil:
			l.Problems = append(l.Problems, fmt.Sprintf("has an invalid timeout %q (want an ISO-8601 duration such as PT90S): %v", raw, err))
		case nanos < int64(MinAgentTimeout) || nanos > int64(MaxAgentTimeout):
			l.Problems = append(l.Problems, fmt.Sprintf("has a timeout %q outside %s to %s", raw, MinAgentTimeout, MaxAgentTimeout))
		default:
			l.TimeoutNanos = nanos
		}
	}
	return l
}

// checkAgentLimits turns the bounds a compile could not honour into errors, on the
// element that states them.
func checkAgentLimits(cp *CompiledProcess) []Problem {
	var ps []Problem
	for id := range cp.nodes {
		var problems []string
		switch cp.nodes[id].Type {
		case TypeAdHocSubProcess:
			problems = cp.AdHoc(cp.nodes[id].Detail).AgentLimits.Problems
		case TypeConnectorTask:
			problems = cp.ConnectorTask(cp.nodes[id].Detail).AgentLimits.Problems
		}
		for _, msg := range problems {
			ps = append(ps, problem(cp, int32(id), SeverityError, RuleAgentLimit,
				fmt.Sprintf("%s %s", describeNode(cp, int32(id)), msg)))
		}
	}
	return ps
}
