# ADR-DRAFT: The Modeler Playground — batch simulation and analysis of a draft

- **Status:** Proposed
- **Date:** 2026-08-31
- **Deciders:** Atlas maintainers

## Context and problem statement

The Modeler has two top-level tabs today: **Design** (draw the flow) and
**Implement** (wire the technical detail). Between "the diagram looks right" and
"the process behaves right" there is nothing. An author who wants to know what a
model actually *does* has three unsatisfying options:

- The **token simulation** in the Design view ([ADR-0078](0078-design-view-token-simulation.md),
  [ADR-0096](0096-token-simulation-events-and-inclusive-gateways.md)) — a
  browser-side teaching aid. It moves tokens along flows; it deliberately does not
  evaluate FEEL, conditions, DMN or data. It answers "where can a token go", never
  "what happens with *this* data".
- **Deploy & run** — real, durable, versioned instances in the engine's log. It
  answers everything, at the cost of polluting the runtime with throwaway data and
  firing real side effects, and it is one instance at a time.
- Nothing at all for the question authors actually ask before a go-live: *"here are
  200 real cases — what does the process do with them, which paths does it take,
  where does it pile up, and how long does it take?"*

[ADR-0030](0030-play-mode-simulation.md) already decided the *shape* of the answer
for the single-case question — an ephemeral engine sandbox rather than a second
control-flow implementation — but it was scoped to stepping one instance by hand,
and it has not been built. The question this record answers is the broader one:

**How does the Modeler let an author run a whole dataset through a draft, on a
timeline they control, and get an analysis back — without deploying, without side
effects, and without a second engine to keep in sync with the real one?**

## Decision drivers

- **The tested model is the shipped model.** Making a model testable must not mean
  editing it. If an author has to swap a REST task for a mockup task
  ([ADR-0120](0120-mockup-service-task.md)) to try the process, what they tested is
  not what they deploy, and the swap-back is a manual step that will be forgotten.
- **Same semantics as production, by construction.** A simulator that disagrees with
  the engine teaches authors a lie. No second control-flow implementation
  (invariant I5's spirit; the reason [ADR-0030](0030-play-mode-simulation.md)
  rejected a JS engine).
- **Provably side-effect-free.** A playground run must not be able to send mail,
  call a REST endpoint, write to clio, or create a Jira issue — not "is configured
  not to", but *cannot*.
- **Nothing durable.** No version minted, no instance in Operations, no row in the
  durable log, nothing to clean up afterwards.
- **A day in a second.** "Spread these 200 cases over a working day" must not take a
  working day. Simulated time has to be decoupled from wall-clock time.
- **Reproducible.** The same dataset, config and seed must produce the same report,
  or the feature cannot be used as a regression check and its numbers cannot be
  cited in a review.
- **Scale.** 50 000 cases in one run is a stated requirement, not a stretch goal.
  That is what rules out holding the run in a request, the report in memory, or the
  case list in the browser.
- **Steppable, not only batchable.** An author must be able to stop mid-run, look at
  a case, fill in a user task themselves and carry on. Without that the "play" in
  playground is missing, and the only way to inspect a single case is to re-run it.
- **Bounded.** An abandoned or oversized run must not eat the server.

## Considered options

1. **Extend the browser token simulation** to batch mode and collect statistics
   client-side.
2. **Deploy to a throwaway namespace** on the real engine, run the dataset there,
   analyse the resulting runtime data, then delete it.
3. **An ephemeral engine sandbox per run** — the real compiler and the real
   processor over a non-durable partition with a **virtual clock**, driven by an
   arrival scheduler, with every external leaf answered by a sandbox-local stub
   policy, reporting from the sandbox's own event log.
4. **A dedicated simulation engine**: derive a queueing / discrete-event model from
   the BPMN and simulate that.

## Decision outcome

Chosen option: **Option 3 — an ephemeral engine sandbox per playground run**,
extending [ADR-0030](0030-play-mode-simulation.md) from "step one instance" to
"run a dataset on a simulated timeline and report on it".

A Playground run is a **Sandbox**: its own partition, its own single-writer
goroutine (invariant I3), its own WAL in a temp directory and its own state store,
none of them reachable from the durable engine, all discarded when the run ends. It
compiles the *current draft* with the real compiler (reusing the dry-run compile of
[ADR-0026](0026-problems-panel-and-versioned-validation.md), so a model that will
not deploy also will not run) and executes it on the real processor. Control flow,
FEEL conditions, gateways, multi-instance, boundary events, DMN decisions, data
objects: all of them are the production code path, because there is no other one.

Four pieces make it a *playground* rather than a second engine:

**A virtual clock.** The sandbox's `engine.Clock` is owned by the run's scheduler,
not by the wall. Time advances in two ways: the scheduler releases the next arrival,
or — when nothing is runnable — it jumps to the next due timer. A process with a
three-day escalation timer therefore finishes in milliseconds, and "arrivals spread
over a working day" is a property of the plan, not of how long the author waits.
The engine already takes a `Clock` (`engine.New(..., clock)`) and already freezes
timestamps into events (invariant I6), so this needs no engine change — the same
trick the conformance driver uses (`conformance/runner.go`).

**An arrival plan.** The dataset (one row = one case) is turned into a list of
`(virtual time, start variables)` pairs by a *timing profile*: all at once,
sequentially (next starts when the previous finishes), a fixed rate, a Poisson
arrival stream, or a day profile with a load curve and business hours. The plan is
computed up front from the seed, so it is part of the reproducible input.

**A stub and resource policy that lives in the run, not in the model.** Every leaf
that would leave the process — a job task, a connector task, a user task, a send
task, an inbound message — is answered by the sandbox from a per-element policy: a
duration distribution, an optional FEEL result expression, an optional failure
probability with an incident or a business error code. This is deliberately the same
semantic vocabulary as the mockup service task
([ADR-0120](0120-mockup-service-task.md)) — but supplied as **run configuration**
against the untouched draft, so the model that was tested is byte-for-byte the model
that deploys. Defaults are derived (a connector task's kind suggests a latency band;
a user task defaults to a work duration; an element's example data is the default
result), so a run is possible before a single stub is configured by hand.

The policy also carries **resource pools**: a named pool with a capacity and a
calendar, and the elements that draw on it. A job the sandbox picks up does not
start being served immediately — it queues for a free slot in its pool, and it is
only served while that pool's calendar is open. So a task's elapsed time splits into
*queue time* and *work time*, and "do three clerks suffice for 200 applications a
day" becomes a question the report answers rather than a number the author had to
guess and configure. This needs no engine change: it is the sandbox's own job runner
deciding *when* to complete a job it already holds, in virtual time.

A pool's **utilisation is measured against the calendar, not the wall clock**: the
denominator is capacity times the working time the calendar offered over the run,
so the nights and the weekend a simulated fortnight spans are not counted as
capacity the pool declined to use. Dividing by the run's span instead reports a
pool with three hundred cases queued as a quarter busy — two numbers in the same
report that cannot both be acted on.

**Isolation by absence.** The sandbox registers **no connector factories, no vault,
no mail transport, no HTTP client at all**. A REST task in the sandbox cannot reach
the network because there is nothing in the sandbox that can. Side-effect freedom is
a structural property here, not a configuration flag. Its partitions come from a
reserved range at the top of the partition space (`0xF000` and above) while the
durable engine runs far below it, so a sandbox key is recognisable on sight and can
never be mistaken for a real one.

Two smaller commitments fall out of this and are worth stating, because both were
found the hard way. Reproducibility is seeded on a key's **counter**, not the whole
key: the high bits carry the partition, which a sandbox is handed as it opens, so
seeding on the key would make the same dataset and seed produce different runs in
two sandboxes. And a session is **owned by the principal that opened it**: it can
hold the variables of a draft only that person may read, so another caller's request
for it is answered as "not found" rather than "forbidden" — an existing id must not
become an oracle.

**A run is a session, not a request.** 50 000 cases will not finish inside an HTTP
call, and stepping through one case by hand is a conversation rather than a call. So
the sandbox is created as a server-side session with a lifetime: it is started,
driven (free-run, pause, step, resume, complete a parked task by hand, publish a
message, jump the clock), polled for progress, read for results, and torn down —
explicitly, or by a TTL when the author walks away. Batch and interactive play are
the *same* session with the scheduler either free-running or held; there is no
second mode with second semantics.

The report is computed from the sandbox's **own event log** — the same
`(ValueType, Intent)` facts Operations reads, with their frozen timestamps — so the
analysis is derived from what the engine did, not from a parallel accounting the UI
keeps. That gives, for free and without new instrumentation: per-element and
per-sequence-flow visit counts (the heat map, the same `visits` shape the runtime
overlay already draws), per-element activate→complete durations, per-instance path
and outcome, incidents, and the timeline of everything that happened.

Two details of that turned out to matter enough to write down.

**A heat map has to list what it did not see.** Element counts come from the
maintained visit counters (ADR-0080), which exist only for elements a token has
been to — so a map drawn from them alone cannot say "this branch never ran with
your data", which is the coverage question an author is usually asking. The map is
therefore built from the *model's* shape, every element and every sequence flow,
with the counts filled in and the rest left at zero. Zero is its own shade on the
canvas: "never reached" is a different statement from "reached least", and
rendering them alike would answer neither question.

**Sequence flows have no counter, and no id.** The engine aggregates elements, not
edges, so flow counts are folded out of the causal token history (ADR-0136), whose
activation records carry the flow each token arrived on. That is one scan of the
run's activations — 24 ms over ten thousand cases, and cheaper than the report's
own per-case reads. A compiled flow carries no BPMN id either, and adding one for
this would put a field on a structure every deployment builds; a flow travels named
by the two elements it joins instead, which the only client there is — one holding
the diagram — resolves against its own registry.

### Scope decided

Four questions were open when this record was first drafted; all four are decided
here, because each of them changes what has to be built.

- **The run's source is either a draft or a deployed version.** The draft is the
  everyday case; pointing the playground at a deployed definition answers the other
  question authors ask — "why did version 7 behave like that?" — with the same
  machinery. The source is a parameter of the session; everything after the compile
  is identical, and a deployed source is still copied into a sandbox partition and
  never touched in place.
- **Interactive stepping is in scope from the start.** Pause, step, inspect a case's
  variables, complete a parked user task through its real form, publish a message,
  jump the clock. This is what ADR-0030 called Play, folded into the same session as
  the batch run instead of shipped as a separate mode.
- **The resource model is in the first round, not a follow-up.** Without pooled
  capacity, every reported waiting time is just a sum of durations the author typed
  in, and the bottleneck ranking says nothing they did not already know. Pools are
  what make the temporal analysis worth reading.
- **The ceiling is 50 000 cases per run.** It is affordable, but not for the reason
  first written here. The estimate was taken from the engine's own baseline
  (~840 instances/sec with an fsync per batch against ~6 900–16 500/sec with the log
  in memory, `benchmarks/results/baseline-5b1b9f2.md`) and predicted "seconds to tens
  of seconds". The sandbox is slower than the raw engine, because per case it also
  draws an answer, queues it against a pool, moves a virtual clock and measures what
  happened: **`go test ./playground -bench=Batch -benchtime=50000x` measures ~2.0 ms
  per case — 50 000 cases in about a minute and a half** on the four-core VM the rest
  of these numbers come from. Ten thousand take seventeen seconds and a thousand
  take under one, which is the size an author actually iterates on.
  Three things bought a 3.5× improvement over the first working version and are load-
  bearing rather than tuning: the sandbox's log does not fsync (it is discarded, so
  the "durable before visible" cost buys nothing — `wal.Options.NoFsync`, used
  nowhere else), the work-in-progress count reads the maintained per-definition
  counter instead of scanning every instance (ADR-0080), and a run settles once per
  occurrence rather than twice. What remains is the scan of the activatable jobs on
  every settle, which is the next thing to attack and is why the number is minutes
  rather than seconds.
  Size, as opposed to speed, is what the design has to respect: the report is folded
  in one pass and holds no object per case, the case list is read page by page out of
  the sandbox's own store, and the CSV is streamed. Anything that holds one object
  per case in memory — in the server or in the browser — is ruled out by this number.

Option 1 is rejected for the reason ADR-0030 gave: a browser walker that also
produced *numbers* would be a second engine whose statistics look authoritative and
are not. Option 2 is rejected because it is exactly what Play mode exists to avoid —
durable versions, real instances, real side effects — and because the real engine
cannot fast-forward a day. Option 4 is rejected because a derived queueing model
diverges from the engine on the first non-trivial construct (event subprocesses,
compensation, correlation), and because we already own a fast, deterministic
executor of the real semantics.

### Consequences

- **Positive:** the analysis is the real engine's behavior, so it cannot drift from
  production; nothing durable is created and nothing can leave the process; the
  model under test is unmodified; a run is reproducible from (dataset, config, seed)
  and therefore usable as a saved regression scenario, not just a demo; the heat map
  and the timeline reuse the runtime overlay and the event log rather than adding an
  instrumentation path; a day-long scenario runs in milliseconds.
- **Negative / trade-offs accepted:** a sandbox is a second processor in the
  process, so isolation from durable partitions has to be guaranteed structurally
  (reserved partition range, separate stores, separate run loop) and resources have
  to be bounded (instance cap, virtual-time horizon, wall-clock budget, session
  TTL). The session lifecycle, the streaming aggregation and the paginated result
  store are all consequences of the 50 000-case ceiling, and all three are work that
  a one-shot "run and return a report" design would not have needed. Finished
  instances stay in the sandbox's state store (that is the history the report and
  the per-case replay read), so a run's footprint grows with the number of cases and
  not merely with the peak work in progress — it needs a budget and a documented
  degradation, not a promise. The stub policy is a real design surface of its own, and every number the
  report gives is only as good as the durations the author configured — the report
  must say so rather than presenting a modelled duration as a measurement. Timing
  fidelity is *modelled*, not measured: the playground answers "given these service
  times, where does it pile up", never "how fast is our REST endpoint".
- **Follow-ups / risks to watch:** the batch identifies its cases by key order
  rather than by looking each one up as it is created — keys are minted from a
  monotonic counter, so ascending key order is arrival order, which costs one sorted
  key list for the whole run instead of a scan per case. The interactive path still
  scans for the newest instance, which is fine for the handful a person starts by
  hand. The activatable-job scan on every settle is the remaining cost at scale; the
  robust fix is a watermark over new job keys checked against the maintained
  open-job counter, so a job that appears out of order is still found rather than
  silently never answered. The fsync in the batch cycle is the one thing
  standing between the durable path and the in-memory numbers above, so the sandbox
  needs a log that does not fsync (a WAL option, or a temp dir on tmpfs) — a
  deliberate, contained deviation, since nothing outside the sandbox observes it.
  Per-case replay needs the log kept whole; at 50 000 cases that is a byte budget
  worth capping, with the report staying complete (it is aggregated live) even where
  the retained log no longer reaches back. The sandbox's WAL is non-durable by construction,
  which is a deliberate, contained deviation from "durable before visible"
  (invariant I2) — nothing outside the sandbox ever observes it, and it must stay
  that way. Deciding how far the *saved scenario* goes (a design-time store keyed to
  the draft, and later a CLI runner so the same scenarios can gate a deploy in CI)
  is deliberately left to a follow-up record. Resource capacity modelling ("three
  clerks share this queue") is a natural extension of the stub policy and is
  explicitly out of scope here.

## Pros and cons of the options

### Option 1 — batch mode in the browser token simulation
- Good: no server work, instant, already exists for control flow.
- Bad: no FEEL, no data, no DMN, no timers — so it cannot answer any question a
  dataset asks; and statistics coming out of a second control-flow implementation
  would be trusted exactly as far as they are wrong.

### Option 2 — deploy to a throwaway namespace
- Good: unquestionably the real engine; no new code path.
- Bad: durable versions and instances for throwaway work; real side effects unless
  every connector is rewired; cannot compress time, so a day-long scenario takes a
  day; cleanup is manual and fallible.

### Option 3 — ephemeral engine sandbox (chosen)
- Good: real semantics by construction; provably side-effect-free; nothing durable;
  virtual time; reproducible; reuses compiler, processor, event log and the existing
  runtime overlay.
- Bad: a second processor to isolate and bound; a stub-policy design surface;
  modelled durations can be mistaken for measurements if the UI is careless.

### Option 4 — a derived discrete-event simulation model
- Good: fast, and the natural home for queueing and capacity questions.
- Bad: a second semantics that must be kept in step with the engine — the fork the
  invariants exist to prevent; diverges first on exactly the constructs authors most
  need help reasoning about.

## Links

- extends [ADR-0030](0030-play-mode-simulation.md) (ephemeral engine sandbox) from
  single-instance stepping to batch runs and analysis
- reuses [ADR-0026](0026-problems-panel-and-versioned-validation.md) (dry-run
  compile), [ADR-0084](0084-csv-batch-validation.md) and
  [ADR-0139](0139-csv-to-json-connector.md) (CSV parsing and the row-list shape),
  [ADR-0025](0025-full-properties-panel.md) (example data as default stub results),
  the runtime overlay's visit counters ([ADR-0080](0080-runtime-aggregate-counters.md))
- borrows the stub vocabulary of [ADR-0120](0120-mockup-service-task.md) as run
  configuration rather than model content
- leaves [ADR-0078](0078-design-view-token-simulation.md) /
  [ADR-0096](0096-token-simulation-events-and-inclusive-gateways.md) in place: the
  Design view keeps its engine-free teaching aid
- follows [ADR-0147](0147-splitting-the-api-server-object.md) (a new API area is its own
  service package) and [ADR-0012](0012-web-ui-app-shell.md) /
  [ADR-0013](0013-embed-bpmn-js-modeler.md) (buildless, self-contained UI)
- honors invariants I3 (single writer per partition — the sandbox owns its own),
  I5 (compile, don't interpret) and I6 (frozen timestamps, deterministic replay);
  deliberately non-durable, unlike the design-time sidecar stores of [ADR-0019](0019-durable-deployments.md)
