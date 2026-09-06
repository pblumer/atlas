# ADR-0254: An agent round on a worker — the toolbox travels out, the tool calls travel back

- **Status:** Accepted
- **Date:** 2026-09-06
- **Deciders:** Atlas engine team

## Context and problem statement

[ADR-0253](0253-agent-tool-calls-drive-adhoc-activation.md) made an agent-driven ad-hoc
subprocess park on a round job and act on what comes back, and its phase 3 built the
Worker Type that decides a round: it reads the container's compiled tool index, asks a
model, and answers with tool calls or a final answer. That Worker Type is **registered
nowhere**, and the two obvious places both refuse it:

- **In the engine process.** One line on `s.jobRunner` would work today, and
  [ADR-0164](0164-no-in-process-service-tasks.md) is exactly the decision that says no:
  "the core loop must never be able to get stuck". A round is one model call, which can
  take minutes and can hang. If any kind belongs outside, this one does.
- **On a supervised worker,** where every credentialed kind already lives — except that
  the seam does not fit. `worker.Exec` returns `map[string]any`: variables, and nothing
  else. A round's answer is usually *not* variables, it is a choice of activities to run.
  And a worker holds no compiled process, so it cannot build the toolbox the model has to
  be offered: names are element ids, descriptions are `<bpmn:documentation>`, parameters
  are what `<atlas:agentParam>` declared, and all of it lives in `CompiledProcess`.

So an agent Worker Type appears to need the leased, streaming worker protocol
[ADR-0007](0007-job-worker-protocol.md) still defers to Milestone 4 — which is what
[ADR-0117](0117-ai-agent-task.md) predicted when it accepted "the leased, off-loop
streaming worker is the Milestone-4 gRPC concern".

**It does not.** Both halves of what an agent round needs already exist on the current
protocol, each with a working precedent:

- **Outbound.** A leased job already carries a *resolved* task. `connectorPayload{Kind,
  Fields}` is built in the lease path from a per-kind `Resolve`, and the division is
  written down in `connector/webscrape/offload.go`: "the engine owns the compiled process
  and scope chain, so it resolves FEEL-backed values … the worker owns network reach"
  ([ADR-0168](0168-connector-work-on-a-worker.md)). A toolbox is precisely a thing only
  the engine has, resolved for a worker that only has the model endpoint.
- **Inbound.** A worker already reports something that is not a variable.
  `worker.Outcome` carries a `DecisionReport` beside its variables, and its own comment
  says why: "a decision's record is durable (ADR-0066), so once the evaluation happens on
  a worker the worker has to be able to report it. Every other kind completes with
  variables alone, which is why `Exec` stays the ordinary shape."

A tool selection is the second thing in that sentence's shape: something a worker
produces that the engine must fold and that variables cannot express. This record is
therefore not a new transport. It is **one more `Resolve` and one more report**, on rails
that carry a decision today.

## Decision drivers

- **The division of ADR-0168 does not move.** The engine keeps what only it has — the
  compiled tool index and the scope chain the collected results live in. The worker keeps
  what only it has — the model endpoint and the credential behind it, which never travels
  ([ADR-0041](0041-connector-management-and-secret-store.md)/[ADR-0069](0069-engine-internal-encrypted-secret-vault.md)).
- **Never believe a worker about which task it holds.** `decisionFromReport` states the
  rule: the process, the element and the instance are read from the leased job, "so a
  report cannot attach itself to a different element than the one whose lease it holds".
  It matters more here than for a decision record, because a tool call does not describe
  work that happened — it *causes* work to happen.
- **A worker must not be able to widen an agent's reach.** ADR-0253's whole governance
  argument is that what an agent may do is the model's, versioned and reviewable. A
  worker reporting a tool the container does not carry must change nothing.
- **No new transport as a prerequisite.** An agent round needs no streaming and no
  fencing beyond the lease it already holds. Making it wait for Milestone 4 would park a
  finished Worker Type on a dependency it does not have.
- **Nothing new on the hot path (I1), and the fold stays where it is.** ADR-0253 already
  turns tool calls into activations on the command path; this record only changes where
  the calls come from.

## Considered options

1. **One more `Resolve` outbound and one more report inbound, on the existing seam
   (chosen).** `agent.Resolve` builds the round's payload the way `webscrape.Resolve`
   builds a scrape's; `worker.Outcome` grows `ToolCalls` beside `Decision`, and a
   `toolCallsFromReport` folds it exactly as `decisionFromReport` folds an evaluation.
2. **Register the agent in the engine process.** One line, works today.
3. **Let the worker read the compiled process** — an API endpoint serving a container's
   toolbox, which the worker fetches per round.
4. **Wait for the Milestone-4 worker protocol** (ADR-0007) and register nothing until
   then.

## Decision outcome

Chosen: **option 1 — the toolbox travels out on the leased job, the tool calls travel
back on the completion.**

### Outbound: a resolved round

`connector/agent` gains a `Resolve` beside its `Toolbox`, in the shape every offloaded
kind already has:

```go
// Job is an agent round with everything already resolved: what travels with a leased job.
type Job struct {
    Goal    string            `json:"goal"`
    Context map[string]string `json:"context,omitempty"`
    Tools   []Tool            `json:"tools"`
    Results []string          `json:"results,omitempty"`
    Round   int               `json:"round"`
}

func Resolve(store state.Reader, cp *compiler.CompiledProcess,
    ei *model.ElementInstanceValue, elementInstanceKey uint64) (Job, error)
```

and the lease path grows one arm beside the others:

```go
case compiler.AgentJobTypeIndex:
    // No credential travels: what the worker adds is a model endpoint and the key
    // behind it, held under the Worker's own name. What only the engine has is the
    // toolbox — element ids, the modeler's documentation, the declared parameters —
    // and what the earlier rounds' calls returned.
    j, err := agent.Resolve(s.store, cp, ei, jv.ElementInstanceKey)
    …
    return &connectorPayload{Kind: "agent", Fields: map[string]any{
        "goal": j.Goal, "context": j.Context, "tools": j.Tools,
        "results": j.Results, "round": j.Round,
    }}
```

The `Model` interface phase 3 defined does not change, and neither does `HTTPModel`: the
worker builds its `agent.Request` from the payload instead of from a `CompiledProcess`,
and everything behind that call is already tested.

### Inbound: a tool-call report

`worker.Outcome` grows the field its `Decision` already made room for in spirit:

```go
type Outcome struct {
    Variables map[string]any
    Decision  *DecisionReport
    // ToolCalls is a worker's account of what an agent chose to run next (ADR-0253).
    // Like a DecisionReport it is an account, not a record: the engine reads which
    // container this is from the leased job, and resolves every name against that
    // container's compiled tool index before anything is activated.
    ToolCalls []ToolCallReport
}

type ToolCallReport struct {
    Tool      string         `json:"tool"`      // the tool activity's BPMN id
    CallId    string         `json:"callId"`    // the model's own id for the call
    Arguments map[string]any `json:"arguments"` // what the model supplied
}
```

The completion endpoint accepts a `toolCalls` array beside the `decision` object it
accepts today, and `toolCallsFromReport` folds it into `[]model.ToolCall` for the
completion command — the same command phase 2's in-process path already builds. Four
rules, three of them lifted verbatim from `decisionFromReport`:

- **The container comes from the lease,** never from the report.
- **A report on a job that is not an agent round job is dropped, not refused.** The
  completion is still valid and the variables still land; refusing would turn a worker
  sending a field the engine does not want into a failed job.
- **Arguments are believed; names are checked.** Resolving a tool name against the
  container's compiled index stays exactly where ADR-0253 put it, in `driveAgentRound`,
  and an unknown name still raises an incident naming what *is* on offer. The report adds
  no trust: a worker cannot name an activity the model does not carry and have it run.
- **An empty `toolCalls` is an ending, not an omission** — the same meaning it has
  in-process, so a worker says "the run is finished" by reporting no calls.

### Registration

`worker/connectors.go` gains `case "agent"`, reading its endpoint and key from the
worker's own environment as `ldap` and the credentialed kinds do, and registering a
`CompletingExecFunc` — the shape that exists for exactly this. **No in-process handler is
registered**, and ADR-0164 holds without an exception: an agent round is the clearest
case that record has.

### What does not change

The engine's fold, the activation, the round boundary at the scope drain, the completion
condition, the incident on an unknown tool — all of ADR-0253's runtime is untouched. So
is `connector/agent`'s decision logic and its Messages-API client. This record moves
where a round is decided, not what deciding it means, which is why the tests written
against `Model` keep their value unchanged.

### Phased implementation plan (test-first, [ADR-0018](0018-test-driven-development.md))

- **Phase 1 — Outbound.** `agent.Resolve` and the lease-path arm. *Tests:* a resolved
  round carries the container's tools with their documentation and declared parameters; a
  second round carries the results the first round's calls produced; resolving a job that
  is not an agent container is an error, not an empty round.
- **Phase 2 — Inbound.** `ToolCallReport`, the endpoint field, `toolCallsFromReport`.
  *Tests:* a reported call becomes an activation of that contained activity; a report
  naming a tool the container does not carry raises the incident ADR-0253 already
  defines, rather than activating anything; a report on a non-agent job is dropped and
  its variables still land; an empty report completes the container.
- **Phase 3 — Registration.** `case "agent"` in the supervised worker, its environment,
  and the Workers view showing the kind. *Tests:* the kind registers under
  `io.atlas.ai.agent` and under nothing else (the guard #770 added); a worker with no
  endpoint configured reports that rather than polling forever.
- **Phase 4 — An end-to-end round through the worker transport,** against a stub model
  endpoint: park, lease, decide, report, activate, drain, next round.

### Consequences

- **Positive:** an agent run leaves the engine process, so a model that hangs cannot park
  the core loop — the thing ADR-0164 exists to prevent. The agent's reach stays the
  compiled one, because the worker never chooses the tool set and cannot widen it. No new
  transport, no gRPC prerequisite, and the Worker Type built in ADR-0253 phase 3 needs no
  change behind its `Model` interface.
- **Negative / trade-offs accepted:** one more payload arm and one more report kind to
  keep in step with the compiler — the standing cost of this seam, paid once per kind.
  The toolbox is resolved and sent *per round*, so a container with many tools sends them
  repeatedly; that is bounded by the model's own tool count and by the completion
  condition, and caching a toolbox on a worker would trade a real staleness bug for a
  small saving. And an agent round now crosses the process boundary twice per round,
  which is latency the in-process shortcut would not have had — and exactly the latency
  ADR-0164 decided to pay everywhere else.
- **Follow-ups / risks to watch:** the durable agent-run record ADR-0117 defines
  (`VTAgentRun`) is a third thing a worker will want to report, and it should ride this
  same seam rather than a fourth one. Whether a worker should also report *why* it chose
  — the model's own reasoning — is that record's question, not this one's. And the
  transcript question ADR-0253 left open (each round is a fresh request) is unchanged
  here: if it is ever answered with a stored transcript, the transcript travels on this
  payload.

## Pros and cons of the options

### Option 1 — one more Resolve, one more report (chosen)
- Good: uses the seam that already carries a non-variable report; keeps ADR-0168's
  division exactly as written; no new transport; the compiled tool index stays the only
  authority on what an agent may run; the Worker Type and its tests are untouched.
- Bad: two more things to keep in step per kind; the toolbox travels every round.

### Option 2 — register the agent in the engine process
- Good: one line, works today, no protocol work at all.
- Bad: it is precisely what ADR-0164 forbids, for precisely the reason it forbids it — a
  round is one model call, minutes long and able to hang. Adopting it would make the
  newest kind the strongest argument against that record.

### Option 3 — let the worker read the compiled process
- Good: no payload growth; a worker could resolve its own toolbox.
- Bad: it inverts ADR-0168 — compile-time state would leave the engine and every worker
  would become a second consumer of it, with a second round trip per round and a second
  place for the tool set to be stale. The authority on what an agent may run should not
  have two readers.

### Option 4 — wait for the Milestone-4 worker protocol
- Good: nothing to build twice if that protocol changes the shape of a completion.
- Bad: an agent round needs neither streaming nor fencing beyond its lease, so this parks
  a finished Worker Type behind a dependency it does not have. The report added here is
  additive; a later transport carries it or replaces it with the same information.

## Links

- registers the Worker Type built in [ADR-0253](0253-agent-tool-calls-drive-adhoc-activation.md)
  phase 3, whose runtime fold, activation and incident-on-unknown-tool this record leaves
  untouched
- honors [ADR-0164](0164-no-in-process-service-tasks.md) without an exception, and keeps
  the division of [ADR-0168](0168-connector-work-on-a-worker.md) exactly as written
- follows [ADR-0066](0066-decision-evaluation-records.md)'s report pattern — an account,
  not a record, with the task read from the lease — on the seam of
  [ADR-0007](0007-job-worker-protocol.md), whose Milestone-4 transport this deliberately
  does not wait for
- the credential stays with the Worker per [ADR-0041](0041-connector-management-and-secret-store.md)
  / [ADR-0069](0069-engine-internal-encrypted-secret-vault.md); the supervised worker is
  [ADR-0157](0157-worker-processes-supervision-and-console.md), the vocabulary
  [ADR-0203](0203-worker-execution-model.md)
- answers the deferral [ADR-0117](0117-ai-agent-task.md) accepted for a leased agent
  worker; its `VTAgentRun` record is the next thing to ride this seam
- test-first per [ADR-0018](0018-test-driven-development.md); honors I1 and the
  command-path fold of [ADR-0001](0001-event-sourcing-and-log-structured-state.md)
