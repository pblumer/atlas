# ADR-DRAFT: An agent is shown its own run again, and its answer can be held to a shape

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-26
- **Deciders:** Atlas engine team
- **Open question:** whether a gateway that speaks the Messages or Chat Completions format
  without their newer request fields accepts `cache_control`, `output_config` and
  `response_format`, or refuses the request. They were checked against the two providers'
  own documentation and against a recording endpoint in the tests, not against a live
  endpoint or a third-party gateway.
- **Question checked:** 2026-11

> **Implementation status (2026-11-26).** Built: the tool-result fact, the stored turn and
> context on the model-call fact, the transcript in the resolved round, replay and prompt
> caching in both provider formats, `<atlas:agentOutput>` with the request-side schema and the
> worker-side check, and the context in name order. Built on 2026-11-27: starting a
> conversation over once it outgrows `maxContextTokens`, and the Modeler's editor for answer
> fields, on both hosts (`e2e/agent-modeler.spec.mjs`).

## Context and problem statement

Three things were wrong with how a round was put to a model, and one with what came back.

**Every round was a first meeting.** [ADR-0253](0253-agent-tool-calls-drive-adhoc-activation.md)
made each round a fresh request — a single message with the goal and a numbered list of
results — and named the cost: "the transcript lives in a variable". The model never saw its
own earlier turn. What it had reasoned, which call each result answered, and that it had
already tried something were all gone; text it wrote beside a tool call was discarded on
arrival. [ADR-0254](0254-agent-rounds-on-a-worker.md) left it open: "if it is ever answered
with a stored transcript, the transcript travels on this payload."

**The round number was a count of results.** `len(results) + 1` is the round only while every
round calls one tool and the model names a result collection.

**The same facts arrived in a different order each time.** The context was rendered by ranging
over a Go map.

**An answer was text.** [ADR-0117](0117-ai-agent-task.md) said so itself: "an un-schema'd agent
is a text generator a gateway cannot decide on", and listed the output schema as not built.

## Decision drivers

- **The engine stays provider-neutral** (ADR-0117). It must not learn what a content block is.
- **A conversation that is only appended to.** A provider reuses what it has already read only
  while the request's beginning is byte for byte what it was, and current models bind their
  reasoning blocks to the conversation that produced them.
- **Nothing held between rounds** (ADR-0164). Whatever a round needs is in the instance.
- **A guarantee has to be checked where it is given.** A schema sent with a request makes a
  conforming answer likely; only a check makes it certain.
- **A stored model must still load** (ADR-0177).

## Considered options

For the transcript:

1. **Keep the model's turn opaque on the run's record and replay it** (chosen).
2. **A provider-neutral transcript** the engine understands and each adapter renders.
3. **Leave it:** results in words, every round.

For the answer:

A. **Declared fields on the element, `<atlas:agentOutput>`** (chosen).
B. **A JSON Schema document** in the model.

## Decision outcome

Chosen: **option 1 and option A**.

### The run's record becomes a transcript

The model-call fact of [ADR-draft-agent-run-record](draft-agent-run-record.md) gains three
texts the worker reports: `Wire`, naming the provider format; `Turn`, the model's whole turn
exactly as the provider returned it; and `Context`, the process variables the call was given.
The engine stores `Turn` and never reads it.

A second kind of fact, `AgentRunToolResult`, is appended when a tool the agent called
completes: the container, the tool's element, the round, the model's own call id and the
result. It is written whether or not the model names a `resultCollection` — the collection is
the process's view of a result, the fact is what the next round is told its call returned.

The facts are keyed `(instance, element instance, ts, pos)`. The element instance comes before
the time so that building a round reads exactly one agent's facts: an instance running a
thousand agents in a multi-instance activity must not make each round scan all of them.

### How a round is resolved

`agent.WithTranscript` runs where a round is resolved for a worker, after `agent.Resolve`:

- **Round** is the number of model calls on the record plus one.
- **History** is the earlier rounds — `said`, `turn`, `wire`, and the results by call id — set
  only when *every* earlier round is on the record with a readable turn. A run with one round
  that cannot be read back is not replayed in part.
- **Changed** is the context values that differ from what the last call was given.

### A conversation is appended to or begun again, never edited

A model call says whether it was put as a **new conversation** (`Fresh`) or as a continuation.
Round one always is. A later round is put fresh when it carries no history, and it carries
none when the provider's format changed, when an earlier turn cannot be read back, or when the
last request had grown past the container's `maxContextTokens` — 150,000 when the model states
none. A fresh round restates the run in words, from the tool-result facts rather than from the
result collection: the most recent twenty results in full and a count of the rest.

**A round is replayed from the last fresh one on.** So a run that outgrows its bound is
shortened once, at one point, and then grows again from there; a run whose provider is switched
picks native replay up again in the new format. What never happens is a conversation with its
beginning cut off or its middle rewritten: a provider's cache misses from the first changed
byte, and a model's reasoning blocks are valid only in the conversation that produced them.

The bound is weighed against the input tokens the *worker reported* for the last call. A worker
that reports none never triggers it.

### How an adapter replays

An adapter replays only turns in its own format. Otherwise it puts the round as before: one
message, the results in words. So a run recorded before this existed, a run whose provider was
switched half-way, and a run whose texts were erased all still work — and each of them is a
fresh round the rounds after it are replayed from.

In the Messages format a replayed round is: the first prompt as it was sent; then per earlier
round the model's turn, unchanged — reasoning blocks and their signatures included — and one
user message answering **every** call of that turn in the turn's own order. A call with no
result on the record is answered as an error saying it did not finish, because a provider
refuses a call left unanswered. What changed in the context follows the last results as text.
Chat Completions gets the same run as an assistant message, one `tool` message per call and a
user message for what changed; the stored assistant message keeps its role, its words and its
calls and drops what an endpoint decorated it with.

**What a call's record keeps as its prompt is what that call added** — the whole first prompt
for round one, the changed context after that. Replaying those texts verbatim is what keeps the
conversation's beginning identical from round to round.

A round's request carries `cache_control: {"type": "ephemeral"}` at the top level, so the
provider reuses the tools, the system prompt and the earlier turns. An ai task is asked once
and carries none.

### The answer's shape

`<atlas:agentOutput name type required description values>` sits in the `extensionElements` of
the element that hosts the agent — an ai task or an agent-driven container — and is
`<atlas:agentParam>` turned round: a typed value the model must supply, in its answer rather
than in a call. `type` is `string`, `number` or `boolean`; `values` limits a string to a set.

With any declared:

- the request asks for the object — `output_config.format` with a JSON Schema in the Messages
  format, `response_format` in Chat Completions, exact (`strict`) there only when every field
  is required — and the system prompt states the fields in words;
- **the worker checks the answer**: one JSON object, every required field present, each of its
  declared type and within its values, and no field that was not declared. An answer that fails
  fails the job, which retries and then raises an incident;
- the answer is written as one JSON variable in canonical form, so a gateway reads
  `= urteil.entscheid = "ja"`.

A faulty declaration is an error of rule `agent.output` that refuses the deploy and not the
reload, like a faulty bound.

### Personal data

`Turn` and `Context` are sealed with the prompt and the answer where the process declares
personal data ([ADR-0314](0314-portal-personal-data.md)), and opened where a round is resolved
for a worker — the edge that record deciphers at. Once the subject's key is destroyed the run
is counted but no longer replayed. A tool result is written by the engine, which holds no key:
it carries what the engine evaluated, so a personal value appears in it as its envelope.

### Consequences

- **Positive:** the model keeps its reasoning and its calls across rounds; each result answers
  the call that asked for it; rounds after the first re-read a cached prefix; a gateway can
  route on an agent's answer; ADR-0253's and ADR-0254's transcript question and ADR-0117's
  output schema are answered.
- **Negative / trade-offs accepted:**
  - **The log grows with the run.** Each round stores its turn; reasoning blocks can be large.
    The payload to a worker grows with the run too, which is what a conversation costs.
  - **A changed context value is told once, late.** The first prompt is frozen, so a value a
    tool replaced is corrected after the next results, not in place.
  - **A result is what the called activity finished with.** A tool that is the first of
    several activities wired together inside the container answers its call when that first
    one completes; the ones after it add no result.
  - **Answer fields are scalars.** An object or a list declared by name alone says nothing
    about what is inside, and a provider's structured output needs every object closed.
  - **Restating loses what words do not carry.** A run begun again keeps what its tools
    returned and drops the model's own reasoning up to that point, and of a long run only the
    last twenty results are repeated.
- **Follow-ups / risks to watch:** the open question above; whether a restated run should be summarised by a model rather than listed; whether
  the model's reasoning summary should be shown in Operations.

## Pros and cons of the options

### Option 1 — the turn kept opaque and replayed (chosen)
- Good: the engine learns nothing about a provider; the replay is exact, which reasoning
  blocks require; one more field on a fact that already exists.
- Bad: a turn is readable by one format only.

### Option 2 — a provider-neutral transcript
- Good: a run survives a change of provider.
- Bad: reasoning blocks cannot be translated — they are valid only as their provider returned
  them — so the neutral form would drop the thing a transcript is kept for.

### Option 3 — leave it
- Good: nothing to store.
- Bad: every cost named in the context, on every round.

### Option A — declared fields (chosen)
- Good: the shape `<atlas:agentParam>` already has; compiles to a schema at deploy; readable on
  the element.
- Bad: scalars only.

### Option B — a JSON Schema document
- Good: any shape.
- Bad: a second language inside the model, most of which neither provider's structured output
  accepts, and nothing a modeller can read off the diagram.

## Links

- answers the transcript question of [ADR-0253](0253-agent-tool-calls-drive-adhoc-activation.md)
  and [ADR-0254](0254-agent-rounds-on-a-worker.md)
- completes [ADR-0117](0117-ai-agent-task.md)'s output schema
- builds on [ADR-draft-agent-run-record](draft-agent-run-record.md)
- follows [ADR-0257](0257-what-an-agent-may-read.md) for what the context is
- applies [ADR-0314](0314-portal-personal-data.md) to a stored turn
