# ADR-DRAFT: Playground data is profiled from history and proposed from the model, and the sandbox checks both

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-27
- **Deciders:** Patrick Blumer
- **Open question:** Whether the agent Worker an installation actually runs — a small local model
  or a free hosted one — proposes cases that reach their targets within three attempts is not
  measured, any more than ADR-0445's FEEL answers were before they were counted. Stage 3 rests
  on the engine's check making a weak model usable; the outcome counters below make that a
  number. The privacy threshold of five sightings for a profiled value rests on judgement, not
  on a measurement either.
- **Question checked:** 2026-10

## Context and problem statement

[ADR-0215](0215-modeler-playground.md) gave the Playground three ways to put data into a run: a
case list typed into the panel, a CSV the server parses, and a dataset *described* rather than
listed (`playground/generate.go`). The description is what makes a large run storable and
reproducible, and it is the way authors are meant to use. It also leaves all the knowledge to
the author. To write one that reaches the paths that matter, they have to know which variables
the gateways read, which values lie on either side of each condition, and what real cases look
like. The server already holds both halves of that knowledge:

- **the model** — every sequence flow's condition, compiled (`CompiledFlow.Condition`,
  `compiler/process.go`), with its source and the names it reads (`expr.Compiled.Source`,
  `Inputs`); the declared start variables with their types and defaults (ADR-0455); the
  boundary events and the error codes the process catches; the decisions it calls;
- **the history** — every variable change of every finished instance, kept per instance with
  its timestamp and the element that wrote it (`VariableSnapshotHistory`, `state/store.go`,
  ADR-0048). A start variable is a write no element made (`ProducerKey == 0`,
  `model/value.go`).

Using none of it, the Playground falls short in three ways:

1. **The generator is flat and uniform.** A field is a scalar, so `kunde.bonitaet` or a list of
   order lines can only be stated as a constant or as a choice of whole objects. Start data in
   practice is both flat and structured (ADR-0037). Every numeric draw is uniform between two
   bounds. Real amounts are not, so a run built from honest bounds overrepresents the
   extremes and says little about volume.
2. **An author finds the untested paths only by looking.** The heat map draws them cold and
   lists them by name (`playground/heatmap.go`). It does not say what the data would have had
   to contain to reach one.
3. **The environment cannot be steered per case.** An edge case is not only a value. It can be
   a credit check that fails, a business error caught by a boundary event, or a timer that
   fires before somebody answers. A stub answers per element (`StubSet.ByElement`,
   `playground/stub.go`) and fails with a probability. A run can say "one credit check in a
   hundred fails" but not "this case's credit check fails", so an error path is reached by
   chance or not at all.

The question: **how does the Playground produce data from what the server already knows — the
process's history and its model — so that a run stays reproducible and storable as a scenario,
no production value leaves the protection it has, and whatever proposed the data, the engine
decides whether it does what it claims?**

## Decision drivers

- **Reproducible, as ADR-0215 requires.** The same scenario produces the same cases. Nothing
  nondeterministic may happen during a run or in `atlas playground` in CI, so a model may only
  be asked while an author is authoring.
- **The engine judges.** This is ADR-0445's rule: a model's proposal is something the engine
  checks, here by running it in a sandbox. A proposal that claims to reach a path and does not
  is corrected or reported, never presented as coverage.
- **Production data keeps its protection.** A value declared personal is enciphered under a
  key an erasure destroys (ADR-0314), and a plaintext copy in a scenario would outlive that
  erasure. Instance data is readable only by those `api/instancescope.go` lets read it.
  History does not go to a model.
- **No silent change to stored scenarios.** A description saved before this record draws the
  same rows after it.
- **One AI configuration.** The model, its endpoint and its key are the agent Worker's
  (ADR-0255), local or hosted, whichever the operator configured. There is no second place to
  set them.
- **Inside the sandbox and the design-time surface.** Nothing here reaches the engine's log,
  `applyToState` or a durable partition. History is read off the loop (I3,
  [ADR-0382](0382-whole-store-reads-leave-the-writer.md)).
- **Expectations come from a person.** Data that reaches a path proves coverage, not
  correctness. An expectation read off the model only confirms the model.

## Considered options

For the history:

1. **H1 — Copy a sample** of real start variables into a scenario as a case list.
2. **H2 — Profile the history into a dataset description**, in the generator's own vocabulary
   extended to structures and observed distributions.
3. **H3 — Differential replay**: real start variables with each instance's recorded outcome as
   the expectation and its recorded job answers as per-case stubs.

For the edge cases:

4. **M1 — Deterministic boundary analysis only**: derive values from the compiled conditions,
   with no model.
5. **M2 — Ask the agent Worker**, feeding it facts read deterministically off the compiled
   model and the run's gaps, and check every proposal by running it.
6. **M3 — Leave it to MCP**: an author's own agent writes cases from outside.

For the environment of one case:

7. **E1 — One scenario per environment variant**, each with a different fixed stub policy.
8. **E2 — Stub variants chosen by a FEEL condition** over the case's variables.

## Decision outcome

Chosen: **H2, a gap report, M2 and M3 over one shared check, and E2**, delivered as three
stages. H3 is deferred.

M2 and M3 stop being alternatives once the check they both need is an operation of its own
(*The trial*, below). An author in the panel gets M2: Atlas asks the configured agent Worker
and checks the answer. An agent connected over MCP gets the check directly and brings its own
model. As ADR-0445 put it for FEEL, the caller of a tool is already a model, so for that
caller a model inside Atlas is a detour; what it lacks is a way to run what it wrote.

### Stage 1 — A description read off the history

**The endpoint.** `POST /api/v1/playground/sessions/{id}/profile` reads the history of the
session's process and returns a description in the shape the run endpoint already takes (the
`generate` body), together with a report. It does not start a run. The author sees the
description in the panel's field table, edits it and saves it like one they typed. A profiled
run is therefore a scenario like any other: no new run plumbing, and nothing that has to read
the history again to repeat it. Once taken, the profile is frozen. Taking it again next month
is a new description, and the scenario's baseline comparison shows what that changed.

**Which instances.** The most recently finished instances of the session's process, through
`FinishedInstancesOfDefDesc` (`state/store.go`), newest version first. The default is 1 000
instances: deciles over a thousand cases each rest on a hundred observations. The ceiling is
10 000, which bounds the read. The request may restrict the profile to the version the session
runs.

**Which values are start values.** For each instance, the writes in its snapshot history that no
element made (`ProducerKey == 0`) and that carry the instance's creation timestamp. Three other
kinds of write also carry no producer:

- Constants and the `application` / `server` namespace objects are seeded in the same breath
  (`engine/behavior.go`, ADR-0454/0456). They are dropped by name, using the instance's own
  version's compiled process.
- Operator overrides (ADR-0098) come later. The timestamp alone excludes them.
- An applied default (ADR-0455) cannot be told apart from a supplied value that equals it. It
  is kept: it is what the instance carried.

The scan of an instance stops at the first record past its creation, so its cost is the start
data and not the instance's whole life.

**How a value becomes a field.**

| Observed | Field | Why |
|---|---|---|
| booleans | `bool` with the observed share | — |
| numbers | `quantiles` (new): eleven cut points, minimum, p10 … p90, maximum; decimals as observed | keeps the shape of a skewed distribution, which bounds cannot |
| RFC 3339 dates or instants | `timestamp`, offsets measured from **the instance's own creation** | "due 5 to 30 days after the start" stays true, while dates from last spring do not |
| strings with few distinct values | `choice`, weighted by frequency, of values seen **at least five times** | a value seen once or twice can identify a customer; rarer values are dropped and counted |
| other strings | `sequence` named after the variable | free text and identifiers are not reproduced, only their presence |
| JSON objects | `object` (new), profiled member by member | structured start data, without a constant per shape |
| JSON arrays | `list` (new): observed length bounds, one item description pooled over all items | order lines, attachments, participants |
| missing in some instances | `percentAbsent` (new, on every kind) | a missing value is an edge case, and real data has it |

A name whose kind varies across instances is profiled by its majority kind, and the report
counts the rest. A recorded null counts as absent. This differs only for a variable with a
declared default, and the report says so for such a variable.

**Personal values are never opened.** For a name the version declares personal
(`CompiledProcess.PersonalVariables`, ADR-0314), and for any value that arrives enciphered
(`VariableValue.IsEnciphered`), the profile counts presence and nothing else. It emits a
placeholder of the declared type and lists the name. The sandbox holds no key, and that is
intended. A deciphered value written into a scenario would survive the erasure that destroys
its key, which is exactly the failure ADR-0314 exists to prevent.

**Who may take a profile.** Only someone who could read those instances in full, by the rule
`api/instancescope.go` already states: authentication off, an operator or admin, or a member of
the project the definition was deployed from. Holding a task on one instance grants that task's
form fields and not a profile of all of them. Anyone else is refused as the instance endpoints
refuse them.

**The report** says how many instances were read, over which versions, and what was done to
each name:

- the kind chosen, the number of observations and the share absent;
- the names dropped as constants;
- how many rare values were left out;
- the personal names replaced by placeholders;
- the instances that had no start data recorded — instances that predate ADR-0048 have none,
  and the profile counts them rather than guessing;
- the names in the history that the draft does not declare, and the declared names the history
  never shows. This is the drift a profile taken against a changed draft has to show, in the
  same spirit as ADR-0424's unread names.

**The generator grows, and existing descriptions do not move.** `quantiles`, `object` and
`list` are new kinds, and `percentAbsent` is a new property on every field:

- A top-level field of today's kinds draws on exactly what it draws on now: the seed, the
  case's position, and `generatorSalt` plus its index.
- A nested field draws on a salt derived from its path in the tree, and a list item from that
  path and the item's index.
- `percentAbsent` draws on a salt of its own, so setting it does not move the value draw of
  the cases that keep the field.

Depth and list length are bounded at validation, against the variable budget (ADR-0294), since
fifty thousand cases times an unbounded list is not a dataset.

### Stage 2 — What the data never reached, and what guards it

The heat map response gains `gaps`, computed in the same fold:

- every sequence flow no case took, with its condition's source, the names it reads, and
  whether it is a gateway's default;
- every boundary event that never fired, with its kind and its error code or timer;
- every element reachable only through those.

The panel lists the gaps under the heat map. Each gap offers **require it**, which adds the
existing `minVisits` expectation for its target element. A gap closed once then stays closed in
CI, because the verdict fails the day the data stops reaching it.

As for the heat map, this covers the root process: a call activity's child runs on its own
graph. Stage 2 is useful without any model. It is also the input stage 3 starts from.

### Stage 3 — Edge cases proposed by the model and checked by running them

**The service.** `api/casegen` is shaped like `api/feelgen`:

- it is stateless and reaches the server only through the closures form generation already
  uses (`agentWorkersForGeneration`, `dialAgentWorker`, `processSourceForGeneration` in
  `api/formgeneration.go`);
- it has a prompt version with a fingerprint test, and outcome counters labelled only by closed
  lists (ADR-0142).

The route is `POST /api/v1/playground/sessions/{id}/proposals`.

**What the model is told** is assembled deterministically from the session's compiled model:

- the declared start variables with their types and defaults;
- per gateway, its outgoing flows with condition source and inputs;
- the decisions the process calls, with their tables;
- the boundary events with their kinds and error codes;
- the elements a stub answers;
- the JSON Schema projection of the process's information model, where it has one (ADR-0230);
- the run's current gaps;
- optionally, one sentence of intent from the author.

**What it is never told:** the history, a profile's values, or any run's case variables.
Aggregated production data is still production data, and the endpoint is one an operator chose
for authoring help, not for data. Because the facts do include condition sources and decision
tables, which are business logic, the panel names the Worker and the model before anything is
sent.

**The contract.** A list of cases, each with:

- a name;
- an intent in a sentence;
- the BPMN ids it means to reach (its targets);
- its start variables, structured as the process takes them;
- optionally, a `when` that selects it among the cases.

Beside the cases come stub variants (below) for the environment edge cases. The answer is read
as feelgen reads one: a contract, a fenced block, or prose that cannot be used.

**The check** is a trial (below), never the author's own session. The trial gets the session's
own open request plus the proposed variants, and the proposed cases. Each case's path is then
compared with its targets: its elements, and its flows folded out of the causal token history
as the heat map folds them (ADR-0136). A path through a business rule task is only worth
comparing once the sandbox actually evaluates the decision. Today it does not; making it do so
is the prerequisite in ADR-draft-playground-data-run. The following go back to the model with
the engine's own words:

- a target missed ("case 3 aimed at `Flow_hoch` (`betrag > 50000`) and went through
  `Flow_tief`; its `betrag` was 50000");
- a value of the wrong declared type;
- a `when` that does not compile, or that is refused (`expr.CompileAuto`,
  `expr.CheckCallsError`, ADR-0388);
- a `when` that does not select its own case.

That is at most two correction rounds, as in ADR-0445. The author then receives the last answer
with each case's verdict, reached or the targets it missed, whichever way it went.

**Accepting.** Accepted cases become the inline case list of a scenario, and accepted variants
go into its open request. The model is not asked again for that scenario, and CI never asks it.
A proposed scenario re-runs on an installation with no agent Worker at all.

**Two scenarios, not one.** The run endpoint refuses a list and a description in one request
(ADR-0215), and this record keeps that rule. A profiled description answers "what does volume
do", and an edge-case list answers "does every path behave". They are two scenarios with
different expectations, and putting both in one run would blur both verdicts.

**No expectations from the model.** A `then` written by the model would state what the model
does, which the run already shows. For each accepted case the panel instead offers **add a
rule**: the `when` is the case's checked selector, the `then` is left empty for the author, and
`minMatched` is 1 where ADR-0424 is implemented. The oracle stays with the person who knows
what the process is for.

### The trial: one check, two callers

`POST /api/v1/playground/trials` runs a small batch in one request and leaves nothing behind.
It opens a sandbox, starts the cases, runs them to rest, judges them and closes the sandbox.

**Request**
- `open`: the body `POST /playground/sessions` takes, with model source, seed, stubs and
  variants.
- Either `cases` or `generate`, never both, as the run endpoint already insists.
- Optionally `expect`, the verdict body.

**Response**
- Per case: its position, its state, the end it reached, the elements and flows it went
  through, its variables and its incidents.
- The report's outcome counts.
- The gaps (stage 2).
- The verdict, when `expect` was sent.

**Why one request.** ADR-0215 rejected a one-shot "run and return a report" for the batch run
only because of its 50 000-case ceiling, which needed streaming aggregation and paging. A trial
is bounded far below that, so the reason does not apply:
- at most 1 000 cases;
- a wall-clock budget, 30 seconds by default.

**Not a session.** A trial never holds one of the eight session places
(`playgroundMaxSessions`, `api/server.go`), so an agent cannot crowd out an author. It has
quotas of its own: one trial in flight per principal and two per server. A caller over the
limit gets 429 with `Retry-After`. Both numbers sit beside the session bounds as resource
limits. They are a starting point, not a measurement.

**Two callers.**
- Stage 3's check is a trial run on the server's own behalf.
- Over MCP, `atlas_playground_trial` proxies it, one tool for one operation, as ADR-0016 has
  every tool.

`mcp/tool_registry_drift_test.go` moves the trial from the omitted routes to the tools and
amends the omission's own reasoning. The reasoning sent an agent to the durable surface ("deploy
it and drive the instance"), which is the path with versions, real instances and real side
effects that ADR-0215 exists to avoid. The interactive session routes stay omitted. They are
made for a person stepping through a run, and twenty tools for that would cost an agent context
and buy it nothing.

**Saved scenarios stay off MCP.** A saved scenario is a CI gate, and `atlas playground` leaves
status 3 when it fails. An agent that saves scenarios decides whether builds go red. Who
reviews such a gate before it binds is a decision of its own, and this record does not take
it.

### Stub variants

An element's stub in the open request may carry `variants`: a list of `{when, …stub fields}`.
When the sandbox answers a job of that element, the first variant whose `when` is true over
the case's variables at that moment replaces the element's stub. If none is true, the
element's stub answers as today.

- Each `when` is compiled once, when the session opens. One that does not compile is refused
  there, naming the element, the same door that already refuses a bad pool (ADR-0392).
- Only elements that have variants evaluate anything. A run without variants pays nothing.
- It is deterministic: FEEL over variables that the dataset and the seed decide.

Variants are not only a test hook. "The credit check fails for amounts over a million" is a
realistic stub, and an author may write one by hand. The model under test is unchanged. A
variable that exists only to steer a variant is data the model never reads.

### What stays out: differential replay (H3)

Replaying real start variables, with each instance's recorded outcome as the expectation, is a
real oracle, which neither the model nor a profile can be. It is deferred for two reasons:

- It copies production values into a scenario, which is the crypto-shredding problem stage 1
  avoids.
- Replaying recorded job answers needs stubs keyed by case and element, a dimension the stub
  policy does not have.

It deserves a record of its own once stages 1 to 3 are in use and show how often the gap they
leave matters.

### Consequences

- **Positive:**
  - An author gets a realistic volume dataset in one click, and a set of edge cases whose
    coverage the engine has confirmed rather than asserted.
  - Both end up as ordinary scenarios: reviewable, repeatable, and runnable in CI without a
    model.
  - Structured start data becomes describable by hand as well.
  - The gap report closes a coverage hole permanently through `minVisits`.
  - Environment edge cases become reachable per case.
  - An agent over MCP can run what it wrote without deploying it, side-effect free. That
    replaces the only path it had, which minted versions and fired real side effects.
  - No personal value is copied, opened or sent anywhere. Nothing reaches the engine.
- **Negative / trade-offs accepted:**
  - **Correlations are lost.** A profile draws each field independently, so a profiled run has
    realistic marginals and not realistic combinations: grade-A customers no longer place the
    large orders. The edge-case scenario and its rules carry the combinations that matter.
  - **History is incomplete.** A profile reflects only instances since ADR-0048 (2026-07) that
    retention has not purged. A purge deletes the snapshot history with the instance
    (`state/tx.go`, ADR-0115/0486), and there is no backfill.
  - **Quantile tails are coarse.** Between p90 and the maximum the draw is linear, interpolated
    from two values.
  - **The model sees business logic** of the process: conditions and decision tables. Where
    that may not leave the house, the operator points the agent Worker at a local model.
  - **Proposals cost.** One proposal costs up to three model calls and three sandbox runs.
  - **The panel grows.** The field table has to render nested fields, the heat map a gap list,
    and the stub editor variants.
  - **Trials cost the server.** Every trial costs a sandbox for up to its wall-clock budget.
    The per-principal and per-server quotas bound that, and a refused trial says when to come
    back.
- **Follow-ups / risks to watch:**
  - A joint choice over frequently seen tuples of low-cardinality fields, under the same
    five-sightings threshold, would restore the most important correlations without copying a
    rare combination.
  - Gaps inside called processes.
  - H3, as above.
  - Saved scenarios over MCP, once it is decided who reviews a gate an agent wrote.
  - Whether the threshold of five is right is a question for the data protection officer, not
    for the code. It is one constant.

### What the implementation starts with (ADR-0018)

**Stage 1**

- A finished instance started with `betrag` and `kunde.grade` contributes both. A constant and
  the `application` namespace seeded in the same batch do not. An operator override written
  later does not.
- A value seen four times does not appear in a choice, and the report counts it.
- A variable declared personal is never deciphered: it becomes a placeholder of its declared
  type and is listed. An enciphered value of a name no longer declared personal is treated the
  same.
- A principal who holds only a task on one instance of the definition is refused.
- Every description of today's kinds draws byte-identical rows before and after the change.
- `quantiles` never draws outside its first and last points. `list` never draws outside its
  bounds. `percentAbsent` set to 0 changes nothing, and set to 100 leaves the field out
  everywhere.
- A date observed ten days after its instance's creation becomes an offset of ten days.
- An instance with no snapshot history is counted, not profiled.

**Stage 2**

- An exclusive gateway whose `betrag > 50000` flow no case took yields a gap naming the flow,
  its source and `betrag`.
- An unfired error boundary names its error code.
- *require it* adds `minVisits` for the gap's target element.

**Stage 3**

- A case that misses its target goes back with the engine's sentence. There are at most two
  corrections, and the last answer comes back with its verdicts.
- The prompt for a session with a profiled description contains none of that description's
  choice values.
- A `when` that does not select its own case is reported.
- The prompt-fingerprint test moves with the prompt version.

**Trial**

- A trial returns each case's path and the gaps, and leaves no directory behind.
- A trial with more than 1 000 cases, or with both `cases` and `generate`, is refused.
- A second concurrent trial for one principal gets 429 with `Retry-After`.
- A trial never takes one of the eight session places.
- The drift test lists `atlas_playground_trial` as a tool and still omits the session routes.

**Variants**

- The first true `when` wins.
- If none is true, the element's stub answers.
- A `when` that does not compile is refused at open, naming the element.
- An element without variants evaluates no FEEL.

**End to end**

- An accepted proposal is stored as a scenario of cases and variants, and re-running that
  scenario on a server with no agent Worker gives the same verdict.

## Pros and cons of the options

### H1 — Copy a sample

- Good: the most realistic data there is, with every correlation intact. Trivial to build:
  the cases are what the instances carried.
- Bad: it writes production values into a design-time store that every author of the draft
  reads, and that a later package export may carry. A personal value copied there survives
  its erasure. It is also exactly as large as the sample, which is the reason ADR-0215 gave
  for describing rather than listing.

### H2 — Profile into a description (chosen)

- Good: no single record is reproduced. It is stored in the vocabulary scenarios already
  keep, it scales to fifty thousand cases, it is reproducible, and it is editable afterwards.
- Bad: correlations are lost, the tails are approximate, and the generator grows three kinds.

### H3 — Differential replay (deferred)

- Good: the only option with a real oracle. "In June this case was approved; the draft
  rejects it" is the regression an author most wants to see.
- Bad: H1's privacy problem in full, plus per-case recorded stubs, without which every path
  that depends on a worker's answer or a person's decision diverges from history for reasons
  that have nothing to do with the draft.

### M1 — Deterministic boundary analysis only

- Good: reproducible, free and provider-neutral, and nothing leaves the server. For a plain
  comparison such as `betrag > 50000` it is strictly better than a model. That is why stage 2
  states the condition and the inputs, and an author can act on them directly.
- Bad: a path condition is rarely one comparison. It composes what script tasks compute, what
  decisions return, lists, contexts and functions. Solving that is symbolic execution over
  FEEL, a project larger than the Playground. The FEEL compiler does not expose its syntax
  tree today either (`expr` exports `Inputs` and `Source`).

### M2 — The agent Worker, checked by the sandbox (chosen)

- Good: cheap where M1 is expensive. The model's nondeterminism is confined to a proposal the
  engine verifies and the author freezes. It reuses one configuration, one credential and
  feelgen's measured loop.
- Bad: up to three calls per proposal, business logic sent to the configured endpoint, and
  quality on a weak model unmeasured (the open question).

### M3 — Leave it to MCP (chosen beside M2, through the trial)

- Good: an agent the user already runs brings a model at least as strong as the agent Worker,
  at no cost to the installation, and needs no prompt maintained in Atlas. With the trial it
  has the whole loop: propose, run, read the paths and gaps, correct.
- Bad: on its own it serves only people who have an MCP client, while authors work in the
  panel. Hence beside M2, not instead of it. An earlier draft of this record rejected M3
  because the Playground had no MCP tools. That argued from a choice this record is free to
  change, and the trial changes it.

### E1 — One scenario per environment variant

- Good: no new stub surface, and each variant is plainly visible.
- Bad: scenarios multiply with every error path. A case that needs one service to fail and
  another to succeed is a scenario of its own. The coverage of the whole set is read across
  runs rather than from one.

### E2 — Stub variants by condition (chosen)

- Good: one scenario reaches every path, the variants are deterministic, and they double as
  realistic stubs.
- Bad: FEEL evaluated per answered job on elements that have variants, and one more editor in
  the stub panel.

## Links

- extends [ADR-0215](0215-modeler-playground.md) (the Playground, its generator and its
  scenarios)
- applies [ADR-0445](0445-feel-assistant.md)'s propose-and-check loop, and
  [ADR-0255](0255-agent-models-are-console-workers.md) /
  [ADR-0260](0260-ai-form-generation.md)'s agent Worker access
- reads [ADR-0048](0048-per-step-variable-snapshots.md)'s variable history; respects
  [ADR-0314](0314-portal-personal-data.md)'s enciphered personal data
- adds one tool to [ADR-0016](0016-mcp-server-over-http-api.md)'s MCP surface; depends on
  the sandbox evaluating decisions (ADR-draft-playground-data-run)
- composes with [ADR-0424](0424-a-playground-rule-can-demand-the-cases-it-speaks-about.md)
  (`minMatched`) and [ADR-0230](0230-process-information-model.md) (the information model's
  schema)
