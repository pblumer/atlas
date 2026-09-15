# ADR-0340: An outage stops at the worker, not at every token

- **Status:** Accepted (amended 2026-09-15 — the job record gains no field; the gate attributes lazily,
  and only while a breaker is open. See the amendment note below.)
- **Implementation:** Partial
- **Date:** 2026-09-15
- **Deciders:** Atlas engine team
- **Open question:** Whether a failure the *target system* caused can be told apart from one this
  engine caused while preparing the call (a FEEL payload that does not evaluate, a secret that does
  not resolve), without the worker protocol carrying a cause. The trip condition below sidesteps
  most of it by requiring distinct process instances rather than distinct attempts, and the residue
  is a false trip on a fault that hits every instance alike — which stops an integration that was
  producing nothing but incidents anyway.
- **Question checked:** 2026-09

> **Amendment (2026-09-15): no field on the job record.** The decision below priced Worker
> attribution as if the gate had to perform it for **every candidate job on every dispatch**, and
> concluded that `model.JobValue` must carry an interned Worker index stamped at command time. That
> premise is wrong, and with it the conclusion.
>
> The gate does not need to know a job's Worker. It needs to know whether a breaker that could hold
> *this* job is open — and breaker state is the engine's own, keyed by `(job type, Worker)`. A map
> from job type to the open breakers under it answers that in **one lookup per job type per dispatch
> call**, not one per job. In normal operation nothing is open, the answer is "no" before any job is
> examined, and attribution never happens at all. Its cost in the steady state is zero, which is the
> state the hot path is optimised for (I1).
>
> Attribution is therefore **lazy**: it happens only for candidates of a job type that currently has
> an open breaker under it — two point reads per candidate, inside the batch
> [ADR-0270](0270-bounded-job-polling.md) already bounds, and paid only during an outage, where the
> alternative it replaces is an outbound call per job. The reporting side attributes once per
> *failure*, on a path that is already writing to the store.
>
> The stamping precedent this record invoked does not reach here. [ADR-0244](0244-searchable-variables.md)
> stamps the model's answer into the event because `applyToState` cannot ask a compiled process
> anything, so what the model said must be in the record or it is lost on replay (I6). The breaker
> gate is not in `applyToState` — it is a live dispatch decision on the run loop, where the compiled
> process is in hand. `Server.incidentConnectorLookup` already resolves exactly this attribution,
> derivationally, on the loop, across thousands of incidents, and its own note says why that is
> right: *"Everything is derived on read from the compiled process and the worker store; nothing
> about it is durable (I6)."* A durable field would have written a fact the deployed model already
> holds into every job record in every partition, permanently, to save a read that the normal case
> never makes.
>
> The section that read **"The one thing this needs from the durable record"** is restated below as
> **"What this needs from the durable record: nothing"**; the field is also struck from the
> "Consequences" and from option 3's "Bad" line. The decision itself — gate the dispatch, per
> Worker, in runtime state — is unchanged.

## Context and problem statement

A worker whose target stops answering does not fail once. It fails once **per instance that reaches
its task**, and each of those failures walks the same path: the job is handed out, the call times
out or is refused, `FailJob` decrements the retry budget, and when the budget is gone the token
parks behind an incident (ADR-0061). An SMTP host that is down for an hour, on a process starting a
few thousand instances in that hour, produces a few thousand incidents — each with its own element
instance key, its own message, and its own place in the incident family.

[ADR-0337](0337-incident-floods.md) made that pile *readable* and *clearable*: the summary answers
in one line per cause, and one bulk resolve clears everything behind it. What it deliberately did
not do is stop the pile from forming. Its own closing line says so: an outage is still translated
one-for-one into N incidents, and the only thing Atlas does about a failing integration today is a
**worker-supplied** retry backoff (ADR-0111) — a delay one worker asks for on one job, which cannot
express "stop asking, the other end is down".

The cost of not having that is not only the incidents. It is:

- **The target gets hammered while it is trying to recover.** Handlers run concurrently
  (ADR-0157 step 6), capped by `Runner.Concurrency` but not stopped: a backlog against a dead host
  is a steady stream of outbound calls for as long as the backlog lasts.
- **Retry budgets are spent on an outage rather than on a fault.** `retries="3"` on a task means
  "this work is worth three attempts"; against a host that is down, all three are spent in seconds
  on the same unavailability, and the budget that existed for a transient fault is gone when the
  host returns.
- **Recovery is manual and proportional to the outage.** Every parked token needs an operator to
  resolve it, even though nothing about any individual token was wrong. With ADR-0337 that is one
  gesture instead of N, but it is still a gesture that would not be needed if the work had simply
  waited.

The engine already knows how to make work wait: a job whose `RetryDueDate` is in the future is held
**off** the activatable index (ADR-0111), and a job under lease is held off it too (ADR-0007).
Waiting is a first-class state. Nothing decides to use it on behalf of a whole integration.

## Decision drivers

- **Stop the flood at its source, not at its reading.** The unit of the failure is the *target*, so
  the unit of the response has to be the target too — not the token, which is what makes N of them.
- **A held token must cost nothing and recover by itself.** A job that was never handed out has
  burned no retry and raised no incident; when the target returns, the work simply proceeds. That
  property is the whole point, and it must not be traded away for a faster signal.
- **Never invent a business outcome.** Holding work back must not cancel, complete, terminate or
  fail anything. A circuit breaker that "gives up" would be deciding something only a person may
  decide.
- **Nothing durable, nothing in `applyToState` (I4/I6).** Whether a target is reachable right now
  is not a fact about the process; it is not replayable, and a recovered engine must not resurrect
  a stale opinion about a host.
- **On the single writer, bounded (I3).** The decision is taken in the dispatch path, which runs on
  the run loop. It must cost *nothing* when no breaker is open — that is the steady state — and stay
  bounded when one is.
- **Visible, or it is worse than the flood.** Work that silently does not happen is the one failure
  mode an operator cannot diagnose. A flood at least says something is wrong.

## Considered options

1. **Do nothing; rely on the read-time grouping of ADR-0337.**
2. **Gate the *outcome*:** let every job go out, but on a failure attributed to a down target, return
   the job to the queue without decrementing retries, with a long `RetryDueDate`.
3. **Gate the *dispatch*:** a breaker per Worker, consulted where jobs are handed out, that holds a
   worker's jobs off the wire while it is open and lets exactly one through to test recovery.
4. **Durable breaker state**, written as events so it survives a restart and is visible in the log.
5. **Key the breaker by job type**, which is what the activatable index is already keyed by.

## Decision outcome

Chosen option: **"gate the dispatch, per Worker, in runtime state"** — option 3, keyed as option 5
is not, and deliberately not durable.

### What a breaker is keyed by

One breaker per **Worker** in the ADR-0203 sense: one configured target and identity, the name a
task states (`connector="Patrick Blumer"`, kind `mail`). Not per Worker *Instance* — several worker
processes pulling the same type all reach the same host, so the state belongs to the thing they
share. Not per **job type** (option 5): every mail task in the estate compiles to the one reserved
mail job type, so a job-type breaker would let one dead SMTP host stop every mail task on the
server, including the ones pointing somewhere healthy. A task that names no worker — a job type an
external worker serves by name alone — falls back to its job type, because that *is* its target as
far as this engine can see.

### What trips it

**N consecutive failures from N distinct process instances**, where N is a per-Worker threshold
with a small default (3), and a completion resets the count.

The "distinct instances" half is what keeps a data fault from stopping an integration. One instance
whose variables are wrong fails its three attempts in a row; those are three failures of *one*
instance, and they never trip the breaker, which is right — one bad record is exactly the case the
per-incident surfaces already handle well. A dead host fails instances that have nothing to do with
each other, which no data fault does.

### What it does while open

The dispatch path asks the breaker before it activates a candidate job, and skips the ones whose
Worker is open. The job stays activatable, untouched, unleased; its token waits at the task exactly
as it waits for a worker that has not polled yet. Nothing is written, no event is emitted, no retry
is spent — the engine's cheapest possible response.

After a cooldown (exponential from a small base, capped, per Worker), the breaker goes **half-open**
and admits exactly **one** job. That job is an ordinary job with an ordinary retry budget: if it
succeeds the breaker closes and the backlog drains; if it fails the breaker opens again with the
next cooldown, and the probe's own token has spent one attempt. An outage therefore costs *one
retry per cooldown*, not N retries per instance. "Exactly one" needs no coordination because both
dispatch paths run on the single writer.

A job already leased when the breaker trips is not recalled. Its lease and its fencing epoch are
the worker's (ADR-0007/0274), and taking work back mid-flight is how a job runs twice.

### Where it lives

In runtime state on the run loop, beside the worker registry — which
[ADR-0157](0157-worker-processes-supervision-and-console.md) already established as the right shape
for exactly this kind of thing: *"derived from traffic the workers themselves generate, it may be
lost on restart without harming anything, and it is never written into the durable record or
rebuilt by `applyToState` (I4/I6)"*. A restart closes every breaker, so the first instances after
one pay the trip cost again — bounded by the threshold, and correct: a fresh process has no
standing to claim a host is still down.

Two call sites consult it, both already on the loop: `Runner.Claim` for the in-process worker
(`job/job.go`), and `handleActivateJobsByType` for the external pull. Two report to it: the
`FailJob`/`CompleteJob` reports those same two paths make. The breaker itself is a small object with
`Allow`, `Failed` and `Succeeded`, injected into the runner as a predicate so the `job` package
keeps knowing nothing about workers.

### What this needs from the durable record: nothing

*(Restated by the amendment of 2026-09-15. The original text decided a Worker field on
`model.JobValue`; it is superseded.)*

A job does not know which Worker it belongs to. `model.JobValue` carries `ProcessInstanceKey`,
`ElementInstanceKey` and `JobType`; the Worker is a property of the *element* in the compiled
process (`compiler.ConnectorRef` via `NodeConnectorRef`), and reaching it from a job takes two point
reads — the instance for its definition, the element instance for its index.

It stays that way. The gate is keyed by `(job type, Worker)`, so the question it asks first is "does
this job type have an open breaker under it?", and that is one map lookup per job type per dispatch
call. In the steady state the answer is no, and no job is attributed to anything; the hot path is
untouched (I1). Only while a breaker under that type is open does the gate resolve each candidate of
that one type — two point reads each, inside the batch [ADR-0270](0270-bounded-job-polling.md)
already bounds, during an outage, replacing an outbound call per job. The reporting side resolves
once per failure, on a path that is already writing.

The resolution itself is not new work: `Server.incidentConnectorLookup` performs it today for the
incident listing, on the run loop, across thousands of rows, deriving everything from the compiled
process and the worker store and persisting nothing (I6). The breaker uses the same shape.

The price of this decision is therefore the attribution *during an outage*, and the reader's guard
against it is the boundedness above. There is no durable cost at all.

### What an operator sees

A breaker is never silent:

- The **Workers view** (ADR-0157) gains the state on the Worker: open since, what tripped it, how
  many jobs are waiting behind it, when the next probe goes. That view already answers "is anyone
  serving this?"; this is the same question answered by the server rather than by eye.
- **Close now** — an operator who has fixed the endpoint does not wait out a cooldown.
- A Prometheus counter per Worker for trips and probes (ADR-0142), and one log line per state
  change.

No incident is raised. An incident is a fact about a token (ADR-0061), and no token here is faulty;
manufacturing one on an arbitrary instance to represent a server-wide condition would be a lie in
the durable record. The queue depth is the honest signal, and it is already on the Workers view.

### Consequences

- **Positive:** an outage costs one probe per cooldown instead of N incidents; retry budgets stay
  available for the faults they were written for; the backlog drains by itself when the target
  returns, with no operator action at all; and the target is not hammered while it recovers.
- **Negative / trade-offs accepted:** work *waits* instead of failing, so a long outage grows a
  queue rather than a pile of incidents — which is cheaper and self-healing, but it is still
  unbounded, and an operator who would rather the process took its error path now has to say so.
  A breaker also delays the discovery of a genuine, permanent misconfiguration: what used to surface
  as an incident within seconds now surfaces as a held queue, which is why the visibility above is
  part of the decision and not a follow-up.
- **Follow-ups / risks to watch:** a breaker tripping is a natural trigger for restarting a
  *supervised* worker process (ADR-0157), and deliberately not wired here — stopping the flood and
  repairing the worker are two decisions, and bundling them would restart a process over a failure
  that was never its fault. The long poll needs care: a worker waiting on a type whose breaker is
  open must not be woken by every new job of that type, or the held queue becomes a spin. And the
  candidate scan needs care for the same reason the gate is cheap: it collects candidates from the
  activatable index *until* `want` is reached, and only a filter applied after that would drop the
  held ones — so ten thousand held jobs of a type can fill the batch and starve the runnable jobs of
  another Worker sharing it. The filter has to sit inside the scan, not after it.

## Pros and cons of the options

### Option 1 — do nothing, read the flood by cause
- Good: no new mechanism; ADR-0337 already makes the pile one line and one action.
- Bad: leaves every cost that is not the *reading* — the retries spent, the calls made into a
  recovering target, and the fact that recovery needs a person at all. It treats a system condition
  as a reporting problem.

### Option 2 — gate the outcome, hold the job with a due date
- Good: reuses ADR-0111's retry timer exactly; needs nothing new in the dispatch path, and no
  attribution before the job goes out.
- Bad: every job still goes out and still fails once per cooldown. Against ten thousand parked
  instances that is ten thousand calls into a host that is already down — the amplification the
  breaker exists to remove. It also makes "do not decrement retries" a rule the fail path has to get
  right, which is the path a worker controls.

### Option 3 — gate the dispatch (chosen)
- Good: the only option where the failing target stops being called; costs nothing per held token;
  recovers on its own; and holds work in a state the engine already has.
- Bad: needs the Worker on the job, which is two point reads — free in the steady state, because a
  job type with no open breaker is answered before any job is looked at, but a real cost per
  candidate while one is open. Holds back healthy work when it trips wrongly — bounded by the probe,
  which re-opens the gate within a cooldown.

### Option 4 — durable breaker state
- Good: survives a restart; the log shows when a target was considered down.
- Bad: "is this host reachable" is not a fact to replay. A recovered engine would start with an
  opinion formed before the restart and possibly before the fix; and the fold would have to make
  time-based state deterministic on replay, which means freezing trip and probe instants into events
  for a judgement that has no business being in the durable record at all.

### Option 5 — key by job type
- Good: free — the activatable index is already keyed by job type, so no attribution is needed.
- Bad: far too coarse to be correct. Every mail task in the estate shares one reserved job type, so
  one dead SMTP host would stop mail for every process on the server. A breaker that punishes
  healthy integrations for a neighbour's outage is worse than none.

## Links

- stops the flood [ADR-0337](0337-incident-floods.md) reads and clears
- builds on [ADR-0061](0061-incident-model.md) (what an incident is, and whose fact it is)
- composes with [ADR-0111](0111-incident-model-completion.md) (a worker's per-job backoff; this is
  its aggregate)
- lives where [ADR-0157](0157-worker-processes-supervision-and-console.md) put runtime worker state
- speaks the vocabulary of [ADR-0203](0203-worker-execution-model.md) (Worker Type / Worker / Worker
  Instance)
- respects the lease and fencing of [ADR-0007](0007-job-worker-protocol.md)
- contrast: [ADR-0244](0244-searchable-variables.md) stamps the model's answer at command time
  because `applyToState` cannot ask a compiled process; this gate runs outside the fold, so it asks
- shares the dispatch path bounded by [ADR-0270](0270-bounded-job-polling.md)
- contrast: [ADR-0272](0272-execution-budget.md) stops a runaway *instance* with an incident,
  because there the token really is the thing at fault
