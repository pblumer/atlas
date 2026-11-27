# ADR-0566: An agent's run is on the record, and its bounds are the engine's

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-26
- **Deciders:** Atlas engine team

> **Implementation status (2026-11-26).** Built: the `AgentRun` fact for every model call,
> the token account from both provider formats, `maxIterations`, `tokenBudget` and `timeout`
> on `<atlas:agentConnector>`, the ceiling for a container that states no bound,
> `GET /api/v1/instances/{key}/agent-runs` and the MCP tool `atlas_instance_agent_runs`.
> Built on 2026-11-27: the **Agent** tab of the instance replay in Operations, and fields
> for the bounds in the Modeler's properties panel — on the container under *Agent limits*, on
> an ai task as *Timeout*. Both are covered in a browser by `e2e/agent-runs.spec.mjs` and
> `e2e/agent-modeler.spec.mjs`.

## Context and problem statement

[ADR-0117](0117-ai-agent-task.md) put a language model on the job path and named three things
it owed: a durable record of each run (`VTAgentRun`) with its token usage, and the model-authored
limits `maxIterations`, `timeout` and `tokenBudget`. None was built. What existed instead was a
per-response output cap on the Worker and a fixed two-minute HTTP timeout.

[ADR-0253](0253-agent-tool-calls-drive-adhoc-activation.md) then made the agent a loop — a round
is a job, a round boundary is a scope drain — and left the loop's only bound to the model's own
completion condition, noting as a follow-up "a durable round counter on the container, which
would let the engine enforce a hard ceiling".

So an agent-driven container without a completion condition ran for as long as its model kept
choosing tools, nothing recorded which model had answered or what a run cost, and the only
answer to "what did the agent do in this instance" was the variables its tools left behind.

## Decision drivers

- **The engine counts, the worker accounts.** A worker is the only party that knows what a call
  cost; it must not be the party that decides which round this is or whether a bound is reached.
  [ADR-0254](0254-agent-rounds-on-a-worker.md) wrote the rule for tool calls — never believe a
  worker about which task it holds — and it applies with more force to a ceiling.
- **Nothing new on the hot path, and one `applyToState`** (I1, I4). A model call takes seconds;
  what it leaves behind may allocate. The fold may not read-modify-write.
- **A bound must not be removable by saying nothing.** A worker that reports no usage, or an
  older worker binary, must still be counted.
- **A stored model must still load** (ADR-0177, ADR-0393). ADR-0117 named the three attributes
  two months before anything read them; a deployed model may carry one this compiler rejects.
- **Personal data stays erasable** ([ADR-0314](0314-portal-personal-data.md)). A prompt quotes
  the variables it was given, and a worker's payload holds those deciphered.

## Considered options

1. **One fact per model call, written by the completion handler, with the sums in the event**
   (chosen).
2. **A counter field on the element instance**, as ADR-0253 sketched.
3. **Count by scanning the history** each time a round is asked for.
4. **Leave the count to the worker**, which already knows the round number it was sent.

## Decision outcome

Chosen: **option 1**.

### The fact

`VTAgentRun` / `IntentAgentRunRecorded` is appended in the batch that completes a job of type
`io.atlas.ai.agent` or `io.atlas.ai.task`, beside `JobCompleted`. It is append-only history
under its instance like a decision evaluation (ADR-0066), keyed
`(instance, element instance, ts, pos)`, and goes with the instance's history.
[ADR-0567](0567-agent-transcript-and-answer.md) adds a second
kind of fact, what a tool returned, and three texts to this one.

| Stated by the worker (an account) | Stated by the engine |
|---|---|
| model that answered, stop reason | process instance, element instance, job, element |
| input, output, cache-read and cache-write tokens | round number |
| duration of the call | running sums of input and output tokens |
| system prompt, prompt, answer | which bound the call reached, if any |
| | the tools the round activated (name and call id) |

Whatever a report says about the right-hand column is overwritten. A completion that carries no
report still appends the fact; a completion forced by an operator (ADR-0159) does not, because
no model was asked.

**Input tokens are everything the model read.** The Messages format reports the uncached,
cache-written and cache-read parts apart; Chat Completions reports one figure that includes the
cached part. The account normalises to the second meaning and keeps the two cache figures as
parts of it, because a budget summed over figures that mean different things means nothing.

**The sums ride in the event.** The handler reads the element instance's totals — one point
read through the in-flight transaction — adds the call, and writes round, sums and bound into
the fact. `applyToState` then makes two plain writes: the fact, and a totals row
`(instance, element instance) → rounds, input, output, bound`. Replay rebuilds both without
asking a model (I6).

### The bounds

`<atlas:agentConnector>` reads three more attributes.

| Attribute | Host | Meaning | Enforced by |
|---|---|---|---|
| `maxIterations` | container | rounds the agent may run | engine |
| `tokenBudget` | container | input plus output tokens the run may use | engine |
| `timeout` | container, ai task | ISO-8601 duration one call may take, 1 s to 1 h | worker |

A container that states no `maxIterations` gets `compiler.SafeAgentRounds` (25), on the
precedent of `SafeLoopCeiling` for a standard loop that states no maximum (ADR-0133).

The round boundary — `startNextAgentRound`, the one place a round after the first is asked for —
reads the totals row. If the last call reached a bound, the container **parks**: it stays
active, takes no outgoing flow, and carries an incident with reason `IncidentAgentLimit`,
listed and filtered as the incident kind `agent`.
Resolving the incident asks for the next round.

Both tests are stateless, in `parkRunawayLoop`'s shape: the round bound is reached whenever the
count is a multiple of the allowance, the budget whenever the sum crosses a multiple of it. So a
resolve writes nothing and grants exactly one more allowance. An agent that *answers* on its
last allowed round never reaches the boundary and simply finishes.

`timeout` travels to the worker in the resolved payload (`timeoutMs`). The worker bounds the
call's context with it and hands it to the adapter's HTTP client; a call that runs out fails the
job like any unreachable model.

An attribute that cannot be honoured — not a positive whole number, not a duration, or a run
bound on an ai task — is an error of rule `agent.limit` that refuses the **deploy**. It is kept
on the compiled detail and reported by `Validate`, not raised while building, so a stored model
carrying one still loads.

### Personal data

Where the job's process declares personal variables, the completion route seals the prompt and
the answer under the data subject's key before it issues the command, and the read route opens
them. Erasing the subject makes them unreadable (`text: "erased"`). Where there is no key to
seal under — no vault, or no subject known for the instance — the texts are not stored
(`text: "withheld"`). The numbers and the standing instruction are kept either way: they are
about the call, not about anybody.

### Two guards the boundary gained

`startNextAgentRound` now returns without asking when the container already holds a round job
or an incident. The scope can drain while the container is not between rounds — an event
subprocess inside it ends while the round is with the model — and asking then would put two
questions to the model at once. This was found by reading the code, not by a failing instance,
and it has no test of its own: constructing the drain needs an event subprocess inside an
agent-driven container, which no test in the tree builds yet.

### Consequences

- **Positive:** a run is bounded whether or not its author thought about it; what a run cost
  and which model answered is a recoverable fact; a worker cannot hide a round or move one;
  ADR-0117's two oldest open items and ADR-0253's follow-up (2) are closed.
- **Negative / trade-offs accepted:**
  - **A behaviour change for deployed models.** A container that states no bound and ran more
    than 25 rounds now parks at 25. The alternative was a ceiling that only binds new models,
    which is not a ceiling.
  - **Failed attempts are not counted.** A call that fails and is retried consumed tokens the
    provider billed and no fact records. The record is of completed calls.
  - **The token budget is checked after the call that crosses it.** A run can exceed its budget
    by one call's worth. Nothing can know a call's cost before it is made.
  - **Token counts are unverifiable.** They are capped at 2^40 per figure so a broken worker
    cannot wrap the sums; beyond that they are believed.
  - **Texts are stored without a size bound of their own** — only the completion route's body
    budget applies. A long prompt is a long record in the log.
- **Follow-ups / risks to watch:** whether a bound should be catchable in the model (a boundary error event) instead of only
  resolvable by an operator; a cost figure in money, which needs a price list this engine does
  not hold.

## Pros and cons of the options

### Option 1 — one fact per call, sums in the event (chosen)
- Good: no change to `ElementInstanceValue`; the fold is two writes; history and enforcement
  come from the same events; the count cannot be skipped by a silent worker.
- Bad: a second column family for the totals; the sums are denormalised into every fact.

### Option 2 — a counter on the element instance
- Good: one row fewer.
- Bad: a codec change on the most-written record in the engine for a field one element type
  uses, and a re-put of the container per round.

### Option 3 — scan the history per round
- Good: no totals row.
- Bad: a prefix scan on the command path that grows with the run, to learn three numbers.

### Option 4 — the worker counts
- Good: no engine change.
- Bad: the party being bounded holds the count. Rejected on the first driver.

## Links

- completes [ADR-0117](0117-ai-agent-task.md)'s run record and limits
- closes follow-up (2) of [ADR-0253](0253-agent-tool-calls-drive-adhoc-activation.md)
- follows [ADR-0254](0254-agent-rounds-on-a-worker.md) for what a worker's report may decide
- follows [ADR-0066](0066-decision-evaluation-records.md) for the shape of the history
- follows [ADR-0133](0133-standard-loop-activities.md) for parking at a ceiling and granting it again
- applies [ADR-0314](0314-portal-personal-data.md) to the texts of a call
