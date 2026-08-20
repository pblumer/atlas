# ADR-0155: In-process vs. out-of-process service tasks — where a step's work runs, and what we recommend

- **Status:** Proposed
- **Date:** 2026-08-20
- **Deciders:** Atlas maintainers

## Context and problem statement

The same unit of work can be modelled in Atlas in at least four ways, and today
nothing in the product tells an author which one to pick. Sending a mail can be a
FEEL script task that builds a payload plus a REST connector, a mail connector
task, a PowerShell script task shelling out to a mailer, or a plain job-worker
service task whose worker lives in the customer's network. All four deploy, all
four run, and they have wildly different failure and latency characteristics.

Worse, the difference is invisible in the model. The BPMN element looks the same;
what changes is **which goroutine pays for the work**. That is the question this
record exists to answer.

### What the four seams actually do today

**1. Inline in the engine, during command processing.** `scriptTaskBehavior`
(`engine/behavior.go:1730`) evaluates a FEEL script task's compiled expression on
the processor goroutine and completes the task in the same batch — no job, no
worker. ADR-0047 spells out why that is sound: FEEL is pure, deterministic,
side-effect-free and compiled at deploy time, and the *result* is written into the
event so replay re-applies it rather than re-evaluating (I5/I6). The mockup task
(ADR-0120) is the other inline citizen; it simulates work with a timer.

**2. In-process worker, on the run-loop goroutine.** A `TypeConnectorTask` or
`TypeScriptJobTask` creates a job carrying a reserved `compiler.*JobTypeIndex`, and
`job.Runner` (`job/job.go`) dispatches it to a handler registered at startup. This
is the path every connector kind rides — REST, mail, SharePoint, Remedy, clio,
temis, web scrape, SCIM, LDAP, CSV import, the DMN worker, and the polyglot script
languages.

The phrase used throughout the codebase for this is "off the hot path, after
fsync", and it is routinely misread as "asynchronous". It is not.
`job.Runner.Drive()` is called from inside `s.do(…)` — that is,
[`api/runloop`](../../api/runloop/runloop.go)'s `Loop.Do`, which hands a closure to
**one** goroutine that runs closures one at a time. The processor has no goroutine
of its own; `Drive()` alternates `RunUntilIdle()` and `PollOnce()` on the caller's
goroutine, and `PollOnce` runs every activatable job's handler **serially, inline**.
So a connector's outbound call is off the *processor batch cycle* and off
`applyToState` — which is what the invariants require — but squarely **on** the
single writer, which is the only goroutine that can serve any other request.
ADR-0149 already recorded this and the production wedge it caused.

Two consequences follow that are easy to miss:

- **It happens with nobody asking.** The timer scheduler calls `Drive()` once a
  second (`go s.timerScheduler(time.Second)`, `api/server.go:1048`), so parked
  connector jobs are executed on the writer whether or not a request is in flight.
- **A burst is amplified.** `PollOnce` collects *all* activatable jobs of a
  registered type and runs them one after another, and `Drive()` repeats until
  none are left. Fifty parked mail jobs against a dead host cost the engine
  fifty consecutive timeouts, not one.

The only bound on that stall is a wall-clock budget: `nettimeout.Default = 10s`
for every network connector (ADR-0149), and `--script-timeout`, **30s** by default,
for a script task. Nothing else stops a step from holding the entire server.

**3. Out-of-process job worker, over HTTP.** A plain `<zeebe:taskDefinition>`
service or send task (`TypeServiceTask`/`TypeSendTask`) creates a job that no
in-process handler subscribes to. It parks on the activatable index until an
external worker leases it (`POST /api/v1/jobs/{key}/activate`), works it, and
reports the outcome as a command (`…/complete`, `…/fail`). This is ADR-0007's
design, and it is the only shape that costs the engine nothing but a durable event.

**4. The same seam, deliberately unserved.** Turning an in-process handler off is
already how Atlas chooses mode: `--powershell=false` leaves PowerShell script jobs
parked rather than failing them, and a connector task whose connector is not
configured parks until it is (`api/connectorkinds.go`). The job does not care who
picks it up.

### The imbalance

Of the 18 reserved job types (`compiler/builder.go`, indices 0–17), **17 are served
by an in-process handler**. The one exception is the user task (index 1): its jobs
park on the activatable index and are leased and completed by the Tasks app over
HTTP. It is also the only job type whose out-of-process protocol Atlas has actually
finished — precisely because a human takes minutes or days, so nobody was ever
tempted to run it on the writer.

For everything else the out-of-process path is technically available and
practically second-class. ADR-0007's amendment records why: job-type indices are
interned **per compiled process**, while the activatable index is global, so
`send-email` in one process and `charge-card` in another can both be index 16. A
type-keyed pull would hand a worker the wrong jobs. Until a global job-type
registry lands, a worker can lease only **by key**, which means it must first know
which instance to look at. There is no long-poll either.

So the honest state of things is: **the mode that is safe for the engine is the one
that is hardest to use, and the mode that is easy to use is the one that can stall
the engine.** That is the imbalance this ADR addresses.

## Decision drivers

- **The single writer must stay available (I3).** One goroutine is authoritative;
  that is only safe if nothing it does blocks without bound. A step's latency must
  not become the whole server's latency.
- **A model should say *what*, not *where*.** BPMN is a portable, versioned,
  shared artifact. Whether a mail is sent by the engine host or by a worker in the
  customer's DMZ is an operations fact that changes per environment, and it must
  not force a redeploy of the model.
- **Authors need one rule, not a matrix.** "Use whichever element you like" is how
  a 30-second PowerShell task ends up on the run loop of a production engine.
- **Don't invent a fifth seam.** The job path already carries retries (ADR-0135),
  incidents (ADR-0061), at-least-once delivery keyed by the job key, and replay.
  Everything here should be a choice *within* that seam.
- **Be honest about what exists.** Any recommendation that depends on the
  type-keyed pull is a recommendation about the future, and must be labelled as one.

## Considered options

1. **Guidance only.** Write down which element to use for which work; keep the
   shared 10s/30s budgets as the safety net; change no code.
2. **Model the mode.** Give the modeler two variants per kind — "Mail
   (in-engine)" and "Mail (worker)" — and let the author choose.
3. **Mode as a deployment property.** The model names the *kind* of work; the
   operator decides per job type whether Atlas serves it in-process or leaves it
   parked for an external worker. Requires finishing ADR-0007's pull protocol
   before it is a real choice.
4. **Move the in-process runner off the run loop** — ADR-0149's option 3: a bounded
   worker pool dispatching handlers on their own goroutines, submitting completions
   back through `Loop.Do`.
5. **Ban in-process connectors.** Every side effect goes to an external worker;
   Atlas ships the workers as separate binaries.

## Decision outcome

Chosen: **option 1 as the authoring rule, now; option 3 as the architecture; option
4 as the prerequisite that makes option 3 safe rather than merely available.**
Options 2 and 5 are rejected.

### 1. The authoring rule: work that can fail slowly does not belong in the engine

Each rung down this ladder costs strictly more, and an author should stop at the
first rung that fits:

| The work is… | Use | Costs |
|---|---|---|
| A pure computation over variables — map, format, derive, aggregate | **FEEL script task** | Microseconds on the writer. No job, no event beyond the variable write. |
| Business logic with rules, tables, or a decision to explain | **Business rule task (DMN)** | One job, evaluated in-process; CPU-bounded, no network (ADR-0014). |
| Not integrated yet | **Mockup task** (ADR-0120) | A timer. |
| One short call to a configured, infrastructure-grade endpoint that answers in well under the budget — mail submission, an internal REST API, LDAP, SharePoint | **Connector service task** | One job, plus **the whole engine** for the duration of the call, capped at 10s. |
| Long, unbounded, bursty, batchy, internet-facing, or subject to someone else's SLA | **Job-worker service task** | One job. Zero writer time. Latency paid by the worker. |
| Arbitrary code in a general-purpose language | **Polyglot script task**, and out-of-process wherever isolation matters | One job, plus up to `--script-timeout` of the whole engine while in-process. |
| Waiting for a person | **User task** | One job, parked. The shape everything else should aspire to. |

Two rules make that table operational:

- **Never step down a rung to do a job the rung above does.** A REST connector that
  fetches a value FEEL could have computed buys a job, an event, and a network
  round trip on the writer. A PowerShell script that sends a mail buys an
  interpreter process for what the mail connector does natively.
- **A connector task is a promise about latency, not just about protocol.** By
  choosing it, an author asserts the call is fast and the endpoint is operated like
  infrastructure. If that is not true — a third-party SaaS, a scraping run, a
  report that takes a minute — the step belongs on a worker, and the answer to "it
  is slower than 10s" is never a longer budget.

The sound-bite version, and the one worth repeating in the modeler: **Arbeit, die
langsam scheitern kann, gehört nicht in die Engine.** Work that can fail slowly
does not belong in the engine.

### 2. The architecture: the model says what, the operator says where

A connector task already carries only *what* — kind, endpoint, mappings — and never
*where*. We make that explicit and complete rather than accidental:

- A reserved job type whose in-process handler is **not registered** parks its jobs
  for an external worker. This is already the behaviour of `--powershell=false` and
  of an unconfigured connector; we promote it from an implementation detail to the
  supported way to relocate a kind, with a per-kind operator switch rather than a
  flag per language.
- **The same model runs either way.** Moving mail out of the engine is a server
  configuration change, not a redeploy of every process that sends mail.
- **The out-of-process path must be finished for this to be a choice at all**, in
  ADR-0007's stated order: the global job-type registry, then the type-keyed pull
  (`POST /api/v1/jobs/activate`), then long-poll on the post-fsync notification.
  Until then, this ADR's option 3 is a direction and the authoring rule above is
  what actually protects the engine.

This is why option 2 is rejected: putting the mode in the model freezes an
environment-specific operational decision into a portable artifact, and doubles the
connector catalog. And why option 5 is rejected: a single binary that just works
is ADR-0011's whole premise, and an in-process connector against a well-operated
internal endpoint is genuinely the right answer for most installations.

### 3. The prerequisite: connector workers off the run loop

Option 4 remains the structural fix, and this ADR restates it as a **prerequisite
for option 3 being safe** rather than a stand-alone nicety. As long as handlers run
on `Loop`'s goroutine, "in-process" means "charged to every other request", the
budgets are the only thing between a bad host and a wedged server, and the
amplification above is unbounded in the number of parked jobs. Once handlers run on
their own goroutines and submit completions back through `Loop.Do`, in-process
becomes an ordinary deployment choice with a latency cost paid by the instance
rather than the engine, and the budget becomes policy instead of a safety net.

### Consequences

- **Positive:** authors get one rule that is short enough to remember and is
  justified by a cost they can see. Mode becomes an operations decision, so a model
  authored on a laptop and a model running in a locked-down network are the same
  file. Nothing here invents a mechanism: it is the job path plus one registration
  switch. And the three open items land in a defensible order, each useful alone.
- **Negative / trade-offs accepted:**
  - Until the type-keyed pull exists, "move it to a worker" means an external
    worker that leases **by key**, which is only practical for a kind whose
    instances are known — so for most kinds the honest advice today is still
    "in-process, and keep it fast".
  - The authoring rule is a convention. Nothing in the compiler rejects a
    30-second connector call, and the modeler does not yet show the cost.
  - Two different stall budgets remain (10s for connectors, 30s for scripts) even
    though both are charged to the same goroutine. They should become one number
    with a name — the engine's stall budget — and the script's should not be the
    larger of the two.
- **Follow-ups / risks to watch:**
  1. Reconcile the budgets into one documented stall budget; scripts should not out-spend connectors.
  2. Correct the "off the run loop" wording in `api/connectorkinds.go` and anywhere else it reads as "asynchronous" — the phrase is how this stopped being obvious.
  3. Global job-type registry → type-keyed pull → long-poll (ADR-0007).
  4. Per-kind in-process/out-of-process switch, generalizing `--powershell=false`.
  5. Connector workers off the run loop (ADR-0149 option 3), after which the budget is policy.
  6. Surface the cost where the choice is made: the connector catalog entry (ADR-0067) should say which mode a kind runs in and what it charges.

## Pros and cons of the options

### Option 1 — guidance only (chosen, as the near-term rule)
- Good: costs nothing, applies today, and addresses the actual failure — an author
  choosing a seam without knowing what it charges.
- Bad: unenforced; a convention protects nothing against someone who has not read it.

### Option 2 — model the mode
- Good: explicit at author time; no operator coordination needed.
- Bad: freezes an environment-specific fact into a portable, versioned artifact;
  doubles the catalog; relocating a kind becomes a redeploy of every model using it.

### Option 3 — mode as a deployment property (chosen, as the architecture)
- Good: one model everywhere; matches how `--powershell=false` and an unconfigured
  connector already behave; no engine concept added.
- Bad: only a real choice once the pull protocol is finished; an operator can now
  strand a kind by unregistering it with no worker on the other side, so the
  Console must show which kinds are served and by whom.

### Option 4 — connector workers off the run loop (chosen, as the prerequisite)
- Good: removes the hazard structurally rather than bounding it; makes in-process a
  latency decision instead of an availability decision.
- Bad: changes the engine's concurrency model next to I3 — the reason ADR-0149
  deferred it — and needs its own recovery and race testing. Still wants a timeout.

### Option 5 — ban in-process connectors
- Good: the engine could not stall on a third party at all.
- Bad: destroys the single-binary experience (ADR-0011); every trivial installation
  pays a distributed-system tax for a mail send to a local relay.

## Links

- extended by [ADR-0156](0156-worker-processes-supervision-and-console.md), which re-decides this record's rejected option 5 once Atlas launches the worker itself, and sequences the follow-ups below
- protects invariant I3 (single writer) — see [invariants](../architecture/invariants.md)
- bounds and re-frames [ADR-0149](0149-bounded-connector-call-budget.md) (the call budget; its option 3 is this record's prerequisite)
- depends on [ADR-0007](0007-job-worker-protocol.md) (the job path, and the pull protocol still owed)
- classifies the seams of [ADR-0047](0047-polyglot-script-tasks-via-job-workers.md) (inline FEEL vs. polyglot-via-job) and [ADR-0067](0067-service-task-connector-catalog.md) (the connector catalog)
- failure surfaces through [ADR-0061](0061-incident-model.md)/[ADR-0111](0111-incident-model-completion.md) and is bounded by [ADR-0135](0135-retries-as-a-task-property.md) (retries)
- the not-yet-integrated rung is [ADR-0120](0120-mockup-service-task.md); the fully-parked rung is [ADR-0028](0028-forms-and-the-tasks-app.md) (user tasks)
