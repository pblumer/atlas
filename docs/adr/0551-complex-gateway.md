# ADR-0551: Run the Complex Gateway, on an inclusive join that re-checks when a token leaves

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-11-10
- **Deciders:** Patrick Blumer (programme owner, BPMN conformance)
- **Open question:** OMG BPMN 2.0.2 contradicts itself on reset. §10.6.5 (p. 295) says "a token
  MUST NOT be produced upon reset". Table 13.5 (p. 438) lets the reset give a token to every
  outgoing flow whose condition is true and accepts that it may give none. This record follows
  clause 13, the programme's execution-semantics authority, and reads the §10.6.5 sentence as an
  editorial slip. An OMG issue or errata entry on it has not been searched for. Also open: how
  other engines bind `activationCount` in an expression; the binding below is atlas's own.
- **Question checked:** 2026-11

## Context and problem statement

Atlas refuses `<complexGateway>` at deploy. It is refused by name and, since ADR-0547, anchored on
the element (`TestCON2ComplexGatewayIsNotAFalsePositive`, register row FLOW-10, unsupported). The
gateway is the only BPMN gateway atlas does not run. It is how BPMN expresses the discriminator
and partial-join patterns: Table 13.5 lists Structured Discriminator (WCP-9), Blocking
Discriminator (WCP-28), Structured Partial Join (WCP-30) and Blocking Partial Join (WCP-31).

### What the specification says

These are verbatim from formal/2013-12-09, read on 2026-11-10. §10.6.5 is on pp. 294–296 and
Table 13.5 on pp. 437–438.

- **Model.** `activationCondition: Expression [0..1]` "Determines which combination of incoming
  tokens will be synchronized for activation of the Gateway." `default: SequenceFlow [0..1]` is
  the flow that receives a token "when none of the conditionExpressions on other Sequence Flows
  evaluate to true". The bpmn-moddle metamodel (9.0.1) agrees: `activationCondition` is a child
  `Expression` and `default` an attribute. §13.4.5 calls the same expression
  `activationExpression`.
- **Instance attributes** (Table 10.126). `activationCount: integer` "the number of tokens that
  are present on an incoming Sequence Flow". `waitingForStart: boolean = true` is the gateway's
  state, and "can be used in the conditions of the outgoing Sequence Flows".
- **Waiting for start.** "The activationExpression is not evaluated before there is at least one
  token on some incoming Sequence Flow. When it becomes true, a token is consumed from each incoming
  Sequence Flow that has a token." Outgoing conditions are evaluated; "those and only those that
  evaluate to true receive a token". If none is true, the default receives one. "If no default
  flow is specified an exception is thrown." The gateway "remembers from which of the incoming
  Sequence Flows it consumed tokens in the first phase" and changes to waiting for reset.
- **Waiting for reset.** The gateway "waits for a token on each of those incoming Sequence Flows
  from which it has not yet received a token in the first phase unless such a token is not
  expected according to the join behavior of an inclusive Gateway". The precise rule: it resets
  when, for every path from a sequence flow f that has a token to an incoming flow that "has no
  token and has not consumed a token in the first phase", there is also a path from f to an
  incoming flow "that has a token or from which a token was consumed in the first phase". Paths do
  not visit the gateway, and do not cross the boundary of its sub-process.
- **Reset.** "It consumes a token from each incoming Sequence Flow that has a token and from which
  it had not yet consumed a token in the first phase." Outgoing conditions are evaluated again;
  if none is true, the default receives a token. The gateway goes back to waiting for start. "Note
  that the Gateway might not produce any tokens in this phase and no exception is thrown."
- **Advice.** `activationCount` "should only be used in subexpressions of the form expr >= const"
  to prevent oscillation.

### What atlas has

- **Joins park tokens.** An arrival at a join is an element instance parked on the gateway. A
  firing consumes the oldest arrival per incoming flow (`OldestPerFlow`, `consumeSet`; ADR-0290)
  and records it as `Completed` events, so replay re-applies the decision and never re-takes it
  (I4, I6).
- **The inclusive join (ADR-0033)** asks `TokenCanStillReach`. The question is whether any live
  token, or any queued element-activating command, sits on a node from which the join is
  reachable. The check runs **only when a token arrives at the join**.
- **A finding this record depends on.**
  `TestCON2InclusiveJoinWhoseUpstreamTokenEndsElsewhere` (in `conformance/normative_joins_test.go`)
  parks one branch at an inclusive join and routes the other branch to an end event. Table 13.3
  (p. 435) states activation as a condition on the tokens in the diagram, so the join must then
  fire. Atlas leaves the token parked, and the instance never completes. The complex gateway's
  reset uses the same rule; built on the current join, it would hang the same way.
- **A second, milder difference.** Atlas blocks an inclusive join while any token sits on any
  ancestor node. Table 13.3 lets a token through that can also reach an incoming flow that already
  has a token. The specification can therefore fire earlier and then fire again; atlas waits and
  fires once. This does not hang. It is recorded here to be classified, not changed by this
  decision.

## Decision drivers

- **Correct by clause 13**, including a race (a discriminator) and a partial join, not only the
  easy cases.
- **Deterministic replay and recovery** (I4, I6). Every decision is an event; expressions are
  evaluated live only. A gateway waiting for reset survives a restart.
- **No new value type if the element-instance lifecycle can carry it.** Joins already park as
  element instances; a new column family for one gateway's state would duplicate that.
- **Compile, don't interpret** (I5). Reachability sets and the activation expression compile at
  deploy; nothing is parsed or walked from scratch per command.
- **Fix the shared join rule once.** The inclusive join and the complex gateway's reset are the
  same clause-13 rule; it must not be implemented twice.
- **Scale-out.** All state stays partition-local, inside one process instance's scope.

## Considered options

For the join rule the reset depends on:

- **J1. Re-check parked joins when a token leaves the upstream** of a join in its scope: it ends,
  is terminated, or completes on a flow that does not lead back to the join.
- **J2. Leave it.** Document the hang as a limitation of the inclusive join, and refuse complex
  gateways for the same reason.

For the gateway:

1. **Full clause-13 semantics.** Two phases, the reset state carried by runtime-only element
   instances, one per incoming flow consumed in the first phase.
2. **The same, with the reset state in a new value type** (one record per waiting gateway and
   scope).
3. **Discriminator only.** The first token passes, the rest are swallowed; no activation
   expression.
4. **Keep refusing.**
5. **Compile a complex gateway to an inclusive gateway.**

## Decision outcome

Chosen: **J1, then option 1**, as two PRs.

### Part 1 — the inclusive join re-checks when its upstream loses a token

When a token in a scope stops being a possible arrival at a join, the engine re-checks every join
parked in that scope whose reach set contains the node the token left. A token stops being a
possible arrival when its element instance completes on a flow outside the join's reach, ends,
is terminated, or its scope ends. The re-check is a follow-up command in the same batch, so it
sees the same in-flight view ADR-0033 relies on. Its outcome is the existing `Completed` /
`Activating` events. Replay applies those events and never runs the check (I4). Joins with no
parked arrival cost nothing; the parked-join lookup is per scope, off the per-command path.

`TestCON2InclusiveJoinWhoseUpstreamTokenEndsElsewhere` then becomes an execution oracle expecting
completion, and FLOW-05 is re-assessed. The ancestor-versus-path difference above stays as it is.

### Part 2 — the complex gateway

**Compile.**
- `<complexGateway>` compiles to a new `TypeComplexGateway`.
- `activationCondition` is a FEEL expression and is **required**: without one the deploy is
  refused (`complex.activation-condition`). The specification allows it to be absent but says
  nothing about what that means.
- `default` follows the exclusive and inclusive gateways, including ADR-0547's
  `gateway.default-not-outgoing`.
- For each incoming flow the compiler precomputes the set of nodes from which that flow is
  reachable without visiting the gateway. This is the per-flow form of `InclusiveJoinReach`. It is
  what Table 13.5's path rule needs, and it handles a cycle through the gateway as the
  specification defines it.

**Binding.**
- In `activationCondition`, `activationCount` is a FEEL context from incoming-flow id to the
  number of tokens parked on that flow in this scope. `activatedFlows` is the number of incoming
  flows with at least one token.
- "Three of five branches" is therefore `activatedFlows >= 3`; per-flow logic is
  `activationCount.f1 >= 1`, or `get value(activationCount, "f-1")` for an id FEEL cannot name.
- In outgoing conditions, `waitingForStart` is `true` on activation and `false` on reset.
- These names shadow process variables of the same name inside these expressions only; the
  modeler warns if the process writes one of them.

**Run: waiting for start.**
- An arrival parks. If nothing of this gateway is waiting for reset in the scope, the activation
  condition is evaluated over the parked arrivals.
- When it holds, the oldest arrival per occupied flow is consumed, and the outgoing conditions are
  evaluated with `waitingForStart = true`. If none is true and there is no default, the gateway
  raises an incident and keeps its arrivals parked: the ADR-0273 form of the specification's
  exception, recorded as a deviation like the exclusive gateway's.
- For each flow it consumed, the gateway activates one **reset marker**: a runtime-only element
  instance of a new `TypeComplexGatewayReset`, on the gateway, carrying that flow in
  `SourceFlowId`. Then it takes its flows.

**Run: waiting for reset.**
- While markers exist for the gateway in a scope, an arrival only parks.
- The reset is checked on each arrival and by Part 1's re-check. The rule is Table 13.5's, over
  the per-flow reach sets: the gateway resets when no live or in-flight token sits on a node that
  reaches a missing flow without also reaching a marked or occupied one.
- On reset it consumes the oldest arrival on each occupied, unmarked flow, completes its markers,
  and takes the flows whose conditions hold with `waitingForStart = false`, or the default. If
  none applies, it produces no token and raises no incident, as Table 13.5 says.
- It then re-evaluates the activation condition at once over whatever is still parked, so a token
  that arrived on a marked flow during reset counts towards the next activation.

**Markers in the scope.**
- They are active children of their scope, so a scope cannot complete while its gateway waits for
  reset. They are torn down with their scope like any element instance.
- `TypeComplexGatewayReset` is persisted as a new value of the element-type byte in element-instance
  records. There is no new value type, column family or payload field.

### Why not the alternatives

**Option 2 (a new value type).** Its strongest form: one record is easier to read than markers,
and it holds the phase and the consumed set in one place. It would also need its own `applyToState`
branch, its own recovery path and its own snapshot family, for state the element-instance
lifecycle already carries. Every existing teardown — scope end, termination, cancellation,
migration — would need to learn about it. Markers get all of that for free.

**Option 3 (discriminator only).** WCP-9 is the most common use, and a small change. It would still
leave atlas refusing three of the four patterns Table 13.5 names. It would also make
`activationCondition` an attribute that is silently ignored, which is the failure ADR-0547 exists
to prevent.

**Option 5 (compile to an inclusive gateway).** It loses the activation condition and the reset
phase, so the deployed model would run differently from the one drawn.

**J2.** It leaves a hang in a gateway atlas already runs, and blocks this one.

### Consequences

- **Positive:** atlas runs every BPMN gateway. FLOW-10 can move from unsupported to implemented once
  the tests below pass, and WCP-9, -28, -30 and -31 get scenarios. The inclusive join stops hanging
  when a branch ends elsewhere, which corrects FLOW-05 for models atlas already runs.
- **Negative / trade-offs accepted:**
  - A binary older than this change cannot replay a log that contains `TypeComplexGatewayReset`.
    Downgrading past it is not supported, as for any new element type.
  - The re-check adds work when a token leaves a join's upstream in a scope with a parked join.
  - The `activationCount` / `activatedFlows` binding is atlas's own; the specification names the
    attribute but not its syntax in an expression.
- **Negative:** an inclusive join that hung before now fires. A running instance that is hung
  today will move on at the next event in its scope. That change in behaviour must be in the
  release note. A stored definition is not recompiled differently; only its instances advance.
- **Follow-ups / risks to watch:**
  - instance migration must map reset markers;
  - the modeler loses its `complexGateway` refusal and gains an activation-condition field;
  - token simulation in the Design view;
  - the ancestor-versus-path difference of the inclusive join still needs a decision.

### Delivery and acceptance

1. **PR 1 — join re-check (Part 1).**
   - Engine change, with `engine` unit tests for an end event, a termination and an interrupting
     boundary on a branch.
   - The CON2 characterisation turns into an oracle; a CON3 restart checkpoint with one branch
     parked at the join.
   - CHANGELOG under *Fixed*; FLOW-05 limitations updated.
2. **PR 2 — complex gateway (Part 2).**
   - Compiler, engine, modeler, and the `complex.activation-condition` rule.
   - Execution oracles, one each for WCP-9, WCP-28, WCP-30 and WCP-31, plus:
     - a reset that produces no token;
     - the incident on an activation that cannot route;
     - a second activation fed by a token that arrived during reset.
   - A CON3 restart checkpoint in waiting for reset; the register rows FLOW-10 and the WCP rows
     updated; TCK cases.
3. Both: `go build ./...`, `go test -race -timeout=45m ./...`, `go vet ./...`, `gofmt -l .`, the
   Windows vet line, and `python3 scripts/conformance_registry.py --check` and
   `python3 scripts/conformance_report.py --check`.

**Invariants.** I1: no allocation on the per-command path; reach sets are precomputed and the
re-check runs only where a parked join exists. I2, I3: unchanged. I4: every decision is an event
applied by the one `applyToState`. I5: expressions and reach sets compile at deploy. I6: no
decision is re-taken on replay.

**Scale-out readiness.** Compatible. All state is element instances of one process instance in
one partition. There is no cross-partition read, no new routing and no host-local singleton.
Standalone mode is unchanged.

## Pros and cons of the options

### J1 — re-check on leaving the upstream
- Good: the clause-13 rule as a condition, not an arrival event; fixes a hang atlas has today.
- Bad: one more trigger to reason about; running instances that hang today will advance.

### J2 — leave it
- Good: no change.
- Bad: keeps the hang, and blocks the complex gateway.

### Option 1 — markers in the element-instance lifecycle
- Good: no new value type; recovery, teardown and snapshots inherited.
- Bad: a runtime-only element type and its reach sets; markers must stay invisible where users
  look at tokens.

### Option 2 — a new value type
- Good: one record per waiting gateway.
- Bad: a second lifecycle to keep in step with every teardown path.

### Option 3 — discriminator only
- Good: small.
- Bad: one of four patterns, and an ignored attribute.

### Option 4 — keep refusing
- Good: nothing to build.
- Bad: atlas stays without a gateway BPMN defines.

### Option 5 — compile to inclusive
- Good: no new behaviour.
- Bad: runs a different model from the one drawn.

## Links

- relates to ADR-0024 (parallel gateway), ADR-0033 (inclusive join), ADR-0273 (a gateway that
  cannot route raises an incident), ADR-0290 (one token per incoming flow per firing), ADR-0547
  (default must be an outgoing flow)
- evidence: `conformance/normative_joins_test.go`, `conformance/normative_oracles_test.go`
  (`TestCON2ComplexGatewayIsNotAFalsePositive`); OMG BPMN 2.0.2 §10.6.5, Tables 13.3 and 13.5
