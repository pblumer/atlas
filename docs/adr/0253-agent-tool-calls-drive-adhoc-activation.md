# ADR-0253: Agent tool calls drive ad-hoc activation — the toolbox is the model

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-06
- **Deciders:** Atlas engine team

## Context and problem statement

[ADR-0117](0117-ai-agent-task.md) put an LLM agent on the job path and shipped it
with an **empty tool catalog**, naming its own open question: "whether, and how,
the agent may call back into Atlas (start a subprocess, read variables, ask a
human)". Without tools an agent task is input variables → output variables. That
is genuinely useful — extract, classify, draft, judge — but it has two costs that
grow with use:

- **Every new capability is a code change.** A model that needs the agent to look
  up a customer, read a table or send a mail has to wait for the agent Worker Type
  to grow that ability, in Go, in a release. Atlas already *has* those abilities —
  the Worker Types under `connector/`, from `mail` and `rest` to `sqldb`, `jira`,
  `entra` and `temis` — and none of them are reachable from inside an agent run.
- **The agent's reach is invisible.** What an agent may touch lives in a worker
  binary and its configuration, not in the diagram. Nobody reading the model can
  see it, no deployment versions it, and no reviewer can diff it.

Meanwhile [ADR-0138](0138-adhoc-subprocesses.md) gave Atlas exactly the container
BPMN has for "a bag of things that may be done, in any order, zero or more times":
the ad-hoc subprocess. Its **entry rule** is the BPMN default and Zeebe's —
entering the container activates **every** entry activity at once, each an
independent token, and the container completes on its completion condition or on
scope-drain. That is right for the case-management shape the record was written
for, and precisely wrong for an agent, which needs the opposite: activate
**nothing** on entry, then run the one activity that was chosen, possibly again,
possibly a different one next, in an order nobody modelled.

Camunda 8 resolved the same tension by making the ad-hoc subprocess the agent's
toolbox: the contained root activities are the tools, the activity's documentation
is the tool description, and the agent activates them by name at run time. ADR-0138
already aligned Atlas's ad-hoc semantics with Zeebe's rather than designing from
scratch; the same reference applies here.

The question this record answers: **how does a contained activity get activated on
demand, by name, at run time** — without a second control plane beside the job
path, without a worker holding a lease for the length of an agent run, and without
letting the model's non-determinism into the log.

What already exists, and is load-bearing:

- **The activation loop itself.** `adHocSubProcessBehavior.OnActivated`
  (`engine/adhoc.go`) already creates a scoped element instance per entry activity
  from the compiled entry index, with `SourceFlowId: -1` because it was activated
  on entry rather than by a sequence flow. Activating *a named subset* is that same
  loop with a filter — no new activation path.
- **The round boundary.** `checkAdHocCompletion` already runs after each contained
  activity completes and already knows how to tell "this scope is an ad-hoc" from
  "this scope is anything else". Scope-drain already means "the contained work is
  finished" (ADR-0074).
- **Typed data riding back on a job completion.** `handleJobCompleted`
  (`engine/behavior.go`) already accepts a structured, non-variable payload from a
  worker and freezes it into history: `c.cmd.Decision` is a DMN evaluation record
  (ADR-0066). A tool selection is the same shape of thing.
- **Result collection.** `SetMultiInstance` already interns an
  `outputCollection`/`outputElement` pair for "append each iteration's result to a
  list on the container" (ADR-0077).
- **A declared, deploy-time variable schema.** `<atlas:startForm>` /
  `<atlas:startVariable name type required default>` is the existing shape for
  "these are the typed values someone must supply before this can run".

So the activation, the round boundary, the completion payload, the result
collection and the parameter schema all exist. What is missing is the rule that
composes them.

## Decision drivers

- **An agent's reach belongs in the model.** What the agent may do should be
  readable in the diagram, versioned with the deployment, and reviewable in a diff
  — not configured beside it. This is the driver that decides the whole record.
- **Reuse the seam, don't add one** — the same driver ADR-0117 wrote for the agent
  task itself. A second control plane (a side channel from a worker back into a
  running scope) is a new authorization surface, a new audit story and a new
  recovery path, for a capability the job path already has.
- **Non-determinism stays outside the log** (I4/I6, ADR-0047/ADR-0117). The model's
  choice must be frozen into the completion event and re-applied verbatim on
  replay. The agent is never re-asked during recovery.
- **No lease held across a whole agent run** (ADR-0164). A run with six tool calls
  can take minutes. Holding one job open across all of it makes an agent run a
  long-lived lease with nothing durable in between, and puts the run's progress
  outside the log.
- **Every tool call is an ordinary activity.** Retries and backoff, incidents,
  boundary events, I/O mappings, the instance timeline, operator intervention and
  migration should apply to a tool call because it *is* an activity — inherited,
  not rebuilt.
- **Compile, don't interpret** (I5). The tool names, descriptions and parameter
  schemas are deploy-time facts, interned once, never rebuilt per activation (I1).

## Considered options

1. **An agent-driven ad-hoc whose rounds are driven by the container's own job
   (chosen).** Entering the container activates nothing and creates one job on the
   container. The Worker Instance calls the model and completes that job with
   either a set of tool calls or a final answer. Tool calls activate exactly those
   entry activities; when that round's work drains, the container creates the next
   job carrying the collected results. No tool calls ends the loop.
2. **An external activation command as the driver.** Add
   `ActivateAdHocActivities(instanceKey, adHocElementInstanceKey, [elementIds],
   variables)` as a command with an HTTP route, in the shape Zeebe exposes. The
   agent holds its job for the whole run and calls back into the API between
   rounds.
3. **No engine change: model the loop.** An agent service task beside a
   multi-instance ad-hoc, with a gateway looping back — the pattern Camunda
   describes as the "task connector with a loop". Possible on Atlas today.
4. **Keep the tools inside the agent Worker Type** — ADR-0117's first slice as the
   end state, growing a tool catalog in Go.

## Decision outcome

Chosen: **option 1 — an agent-driven ad-hoc subprocess whose rounds are jobs on the
container, and whose tool selection rides back as typed data on the job
completion.**

The insight that makes this small: **a round is a job, and the end of a round is a
scope drain.** Both already exist and both are already durable. The agent loop
becomes a chain of ordinary job and element events with nothing held open in
between, and the thing that was going to need a new command needs none.

### What the model carries (deploy-time, interned — I5)

**On the container.** `<atlas:agentConnector>` — the extension ADR-0117 defines —
becomes hostable on `<adHocSubProcess>` as well as on `<serviceTask>`. On a
container it means *this ad-hoc is agent-driven*: the same `connector`, `prompt` /
`instructions`, `resultVariable`, output schema and limits fields, with the tool
allow-list no longer a field because the contained activities **are** the
allow-list. `AdHocDetail` grows:

- `AgentDriven bool` — entry activates nothing; rounds are driven by the job.
- `ResultCollection`, `ResultElement` (interned indices, `-1` for none) — where a
  tool call's result is appended, the `SetMultiInstance` pair exactly.

**On each contained entry activity — a tool.** No new element and no new task type:
any activity Atlas can already run is a tool if it sits at the root of an
agent-driven ad-hoc.

- **Name:** the element id. Stable, unique within the process, already the thing
  every other index keys on.
- **Description:** the activity's `<bpmn:documentation>`. Atlas models already
  carry documentation as a matter of house style; this makes the sentence a
  modeler writes for the next human the same sentence the model reads. A tool
  whose documentation is empty is a Problems-panel **warning**, not a deploy error
  — an undescribed tool is a quality defect, not an unrunnable model.
- **Parameters:** `<atlas:agentParam name="…" type="…" required="…"
  description="…"/>` in the activity's `extensionElements`, compiled into an
  interned per-activity parameter schema. This is `<atlas:startVariable>`'s shape
  — "the typed values that must be supplied before this runs" — applied one level
  down. (Camunda marks the same thing with a `fromAi()` call inside the input
  mapping; the seam differs, the declaration is the same. A declaration is chosen
  over a marker function because it compiles to a schema at deploy rather than
  being recovered by inspecting expressions.)

**What stops the loop** needs nothing new: ADR-0138's `<completionCondition>` is
already evaluated after each contained activity completes, so a model bounds its
own agent with `= count(toolCallResults) > 8` or a predicate over the agent's own
context variable. The connector's `maxIterations` (ADR-0117) bounds it from the
worker side, where the transcript is.

### How it executes — a round is a job

1. **Entry.** `adHocSubProcessBehavior.OnActivated`, when `AgentDriven`, arms the
   scope's event subprocesses as it does today (ADR-0082) and then activates
   **nothing**. It creates a job on the container's own element instance carrying
   the reserved `AgentJobTypeIndex` and calls `NotifyJobAvailable` — the ordinary
   post-fsync side effect (I2/ADR-0005). The container stays `Activated`.
2. **The model's turn, off the engine.** A Worker Instance leases the job
   (ADR-0007/ADR-0164/ADR-0168), reads the container's tool index and the collected
   results so far, calls the model **on the worker, after fsync**, and completes the
   job with either a set of tool calls or its final answer.
3. **The selection rides back typed.** A `ToolCalls []model.ToolCallValue` field on
   the job-completion command — `{ElementId, CallId, Arguments}` — mirroring
   `c.cmd.Decision` (ADR-0066). Not a reserved variable name: a tool selection is
   engine control data, and putting it in the process variable space would leak it
   into FEEL, into the variable timeline and into every downstream expression.
4. **Activation.** A checkpoint in `handleJobCompleted` — `driveAgentRound` in
   `engine/adhoc.go`, sitting beside `checkAdHocCompletion` — sees that the job's
   element instance is an agent-driven ad-hoc. If the completion carries tool
   calls, it activates **exactly those** entry activities instead of appending
   `Completing`: the loop `OnActivated` already runs, filtered by element id and
   repeated per call, each instance carrying that call's arguments as
   activity-local variables (ADR-0068) and the call id so its result can be paired
   back. Two calls naming the same activity are two instances; that is the ad-hoc's
   *zero-or-more* semantics doing its job.
5. **Tools run as activities.** Each activated activity is an ordinary activity for
   its whole life — its own Worker Type, retries, backoff, incidents, boundary
   events, timeline row. On its completion the `ResultElement` expression is
   evaluated over its scope and appended to the container's `ResultCollection`,
   the multi-instance output-collection path.
6. **The round ends when the scope drains.** `checkAdHocCompletion` already runs
   after each contained activity completes. For an agent-driven container, when no
   contained activity is left active, it creates **the next job** on the container
   rather than completing it. The completion condition, if the model wrote one, is
   evaluated first and still wins.
7. **The end.** A completion carrying no tool calls is the agent saying it is
   done: the container takes `Completing`, drops its local scope and takes its
   outgoing flow (ADR-0074) — the existing `OnCompleting`, untouched.

The whole loop is a chain of durable job and element events. Nothing is held open
between rounds, nothing is pushed to a client, and no command exists that a worker
could use to reach into a scope out of band. On recovery the stored selections are
re-applied by the one `applyToState` and the model is never asked again (I4/I6).

### Compiler

- Parse `<atlas:agentConnector>` on `<adHocSubProcess>` → `AdHocDetail.AgentDriven`
  plus the ADR-0117 connector fields; parse `ResultCollection`/`ResultElement`.
- Parse `<atlas:agentParam>` on contained activities into an interned per-container
  **tool index** — element id, documentation, parameter schema — stored the way the
  entry index is (a slice into a shared array, no per-activation allocation, I1).
  The tool index is a subset of the entry index by construction: only entry
  activities are tools.
- Deploy-time validation: an agent-driven ad-hoc needs at least one entry activity
  (an agent with no tools is an agent task — use ADR-0117's service-task form and
  say so in the message); a parameter needs a name and a known type; a duplicate
  parameter name on one activity is an error; `ordering="Sequential"` stays refused
  as ADR-0138 refuses it, and for an agent-driven container it is meaningless
  anyway — the agent *is* the sequencer.
- `cancelRemainingInstances="false"` on an agent-driven container is refused: the
  round boundary is the drain, and "let the rest finish after the condition holds"
  has no meaning when the next round has not been asked for yet.

### Runtime

- `driveAgentRound`, called from `handleJobCompleted` before the generic
  `Completing`, and the agent-driven branch of `checkAdHocCompletion`. Both are
  command-path reads whose *effects* are persisted event chains, so they replay
  identically (I6), exactly as ADR-0138's completion checkpoint does.
- A tool call naming an element that is not in the container's tool index **fails
  the job** with a clear message rather than being silently skipped: the model
  asked for something the deploy-time index does not have, which means the worker
  offered it something it should not have. Retries and the incident model handle it
  from there (ADR-0061/ADR-0111), and the incident names the offending id.
- A tool call whose arguments do not satisfy the activity's parameter schema fails
  the same way. Validation at the boundary, once, in the engine — not trust in the
  model's output.

### Audit

Each tool call is visible **twice**, and deliberately so. As an ordinary activity
in the instance timeline, with its own job, its own variables and its own
incidents; and as a row in the agent-run record ADR-0117 defines (`VTAgentRun`),
which gains the round number and the ordered calls of each round. The first tells
an operator what ran; the second tells an auditor why it was chosen.

### Governance — the agent can now cause side effects, and that is the point

With the toolbox in the model, an agent-driven ad-hoc can activate a mail send, a
REST call or a database write. That is a real widening compared with ADR-0117's
empty catalog, and it is a safer one than the alternative it replaces:

- The reach is **in the diagram**, versioned with the deployment and visible in a
  review, instead of in a worker's configuration.
- Each call is an **audited activity** with its own retries and its own incident,
  instead of an opaque step inside a worker's loop.
- Idempotency is a per-activity concern, as it already is for every other job —
  not a new problem this record introduces.

A model that wants a human between the agent and a consequence models it the way
BPMN already does: a user task inside the toolbox as a tool, or a user task after
the container. ADR-0117's rule holds unchanged — human-in-the-loop is a real
`userTask`, never something hidden inside the agent.

### Phased implementation plan (test-first, ADR-0018)

- **Phase 1 — Compile.** `AgentDriven`, the result-collection pair, the tool index,
  `<atlas:agentParam>`, and the validations. *Tests:* an agent-driven ad-hoc with
  two documented tools compiles with both in the tool index and neither activated
  by the entry index consumer; a tool's parameter schema interns; a chained inner
  activity (a→b) is not a tool; an agent-driven ad-hoc with no entry activity is
  refused with the message pointing at ADR-0117's service-task form.
- **Phase 2 — Runtime rounds.** Entry creates the job and activates nothing;
  `driveAgentRound`; result collection; the next-round-on-drain branch; the
  no-tool-calls ending. *Tests:* entry parks with one job and no contained
  activity; a completion with one tool call activates exactly that activity;
  two calls to the same activity produce two instances; results land in the
  collection in call order; a drain creates the next job; a completion with no
  calls completes the container and takes its outgoing flow; an unknown element id
  fails the job; a **recovery** test — a container parked mid-round with an active
  tool job replays and continues on that job's completion, with no second call to
  the model.
- **Phase 3 — The agent Worker Type.** The worker that reads the tool index, builds
  the model-facing tool definitions from it, calls the model, and completes with
  calls or an answer — ADR-0117's Worker Type, extended. *Tests:* the tool
  definitions built from a compiled container; a scripted model transcript drives
  a two-round run end to end against a fake.
- **Phase 4 — Modeler, docs, ROADMAP.** The agent fields on an ad-hoc in the
  Implement panel, the parameter editor on a contained activity, the
  missing-documentation warning in Problems, and the ADR accepted.

### Consequences

- **Positive:** every Worker Type Atlas already ships becomes reachable to an
  agent without a line of agent code, and the set a given agent may reach is a
  property of the model. The agent loop becomes durable and inspectable — each
  round is a job, each call an activity, the whole run replayable without ever
  re-asking the model. No new command, no new authorization surface, no
  long-held lease, no new recovery path. ADR-0117's deferred question is answered
  in the direction that keeps its own driver ("reuse the seam") intact.
- **Negative / trade-offs accepted:** an agent-driven ad-hoc is a second entry
  semantics on one element — a model reader must know which one is in force, and
  the Modeler has to make that visible. Each round costs a job round-trip, so an
  eight-call run is eight jobs and their fsyncs; that is the price of the loop
  being durable, and it is the same price every other multi-step model pays. The
  agent's transcript between rounds lives in a variable the worker maintains, so a
  long run grows a large variable — bounded by the model's completion condition
  and the connector's `maxIterations`, not by the engine. And the widening in
  *Governance* above is real: a badly modelled toolbox is a badly governed agent,
  which is why the reach is where reviewers can see it.
- **Follow-ups / risks to watch:** (1) **The external activation command** —
  option 2 remains the right answer for *human*-driven case management ("a case
  worker picks the next activity"), and would reuse this record's activation
  primitive; it is deliberately not built here because the agent does not need it.
  (2) **A durable round counter on the container**, which would let the engine
  enforce a hard ceiling instead of leaving it to the model's completion condition
  and the worker — the same codec-change question ADR-0138 deferred for sequential
  ordering. (3) **Exporting the tool index over MCP** (ADR-0016's surface), so the
  same toolbox is inspectable by an agent outside the instance. (4) **Nested
  agent-driven containers** — an ad-hoc as a tool of another ad-hoc composes on
  paper; it needs a test before it is claimed. (5) **Parallel calls within a
  round** are already the default (a round activates every named call at once and
  ends on the drain); whether a model should be able to force one-at-a-time is the
  sequential-ordering follow-up wearing a different hat.

## Pros and cons of the options

### Option 1 — agent-driven ad-hoc, rounds driven by the container's job (chosen)
- Good: no new command, no new client channel, no long-held lease; a round is a
  job and a round boundary is a scope drain, both already durable and replayable;
  the tool set is the model; every tool call inherits the whole activity path;
  ADR-0117's non-determinism contract survives unchanged.
- Bad: a second entry semantics on `<adHocSubProcess>`; one job round-trip per
  round; the transcript lives in a variable.

### Option 2 — an external activation command as the driver
- Good: the shape Zeebe exposes, and the natural fit for human-driven case
  management; the agent keeps one conversation in memory across the run.
- Bad: as the *agent's* driver it holds a lease for the length of the run
  (against ADR-0164), leaves the run's progress outside the log between calls, and
  adds a command whose authorization and audit semantics sit uncomfortably between
  a worker's own work and an operator intervention (ADR-0159). Kept as a
  follow-up for the case it actually fits.

### Option 3 — no engine change: model the loop
- Good: runs on Atlas today; nothing to build.
- Bad: the loop, the result collection and the round bookkeeping must be
  hand-modelled in every process that wants an agent, and the tools are a modelled
  fan-out rather than a bag of on-demand activities — the ad-hoc's *any-order,
  zero-or-more* semantics, the thing that makes a toolbox a toolbox, is exactly
  what a modelled loop cannot express.

### Option 4 — keep the tools inside the agent Worker Type
- Good: no engine change; the worker controls exactly what exists.
- Bad: every new capability is a Go change and a release; the agent's reach is
  invisible to the model, unversioned with the deployment and undiffable in
  review; and every Worker Type Atlas already ships stays unreachable from inside
  an agent run.

## Links

- answers the **tool catalog** follow-up of [ADR-0117](0117-ai-agent-task.md) (AI
  agent task — the empty first-slice catalog, the frozen-result determinism
  contract, the `VTAgentRun` audit record, human-in-the-loop as a real user task)
- builds on [ADR-0138](0138-adhoc-subprocesses.md) (ad-hoc subprocesses — the entry
  index, the completion-condition checkpoint, the scope-drain completion) and
  through it on [ADR-0074](0074-embedded-subprocesses.md) (scope machinery),
  [ADR-0077](0077-multi-instance-activities.md) (completion-condition eval and the
  `outputCollection`/`outputElement` pair) and
  [ADR-0082](0082-event-subprocesses.md) (arming event subprocesses in a scope)
- the tool selection rides a job completion the way a decision record does
  ([ADR-0066](0066-decision-evaluation-records.md)); tool arguments and results
  flow through [ADR-0068](0068-task-io-variable-mappings.md); the writes are
  attributed per [ADR-0219](0219-variable-write-attribution.md)
- the tool activities are jobs on the ordinary seam
  ([ADR-0007](0007-job-worker-protocol.md),
  [ADR-0067](0067-service-task-connector-catalog.md)) run by Worker Instances, never
  in the engine process ([ADR-0164](0164-no-in-process-service-tasks.md),
  [ADR-0168](0168-connector-work-on-a-worker.md),
  [ADR-0203](0203-worker-execution-model.md) for the Worker Type / Worker / Worker
  Instance vocabulary this record uses)
- failures follow [ADR-0061](0061-incident-model.md) /
  [ADR-0111](0111-incident-model-completion.md); the determinism contract is
  [ADR-0047](0047-polyglot-script-tasks-via-job-workers.md)'s, with "interpreter"
  reading "model"
- honors I1, I2, I4, I5, I6, [ADR-0005](0005-group-commit-and-fsync-strategy.md)
  (durable before visible) and [ADR-0001](0001-event-sourcing-and-log-structured-state.md)
  (one `applyToState`); test-first per [ADR-0018](0018-test-driven-development.md)
