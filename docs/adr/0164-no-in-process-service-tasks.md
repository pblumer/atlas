# ADR-0164: No in-process service tasks — the core loop must never be able to get stuck

- **Status:** Accepted
- **Date:** 2026-08-20
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0156](0156-in-process-vs-out-of-process-service-tasks.md) treated in-process
execution as a legitimate rung on a ladder: fine for "one short call to a
configured, infrastructure-grade endpoint that answers in well under the budget".
[ADR-0157](0157-worker-processes-supervision-and-console.md) built everything the
alternative needed, and its seven steps are now delivered — an engine-wide job-type
table, a type-keyed pull with long-poll, lease fencing, the Workers view,
`atlas worker`, handlers off the run loop, and supervision.

With that in hand the earlier position no longer holds up. It rests on a promise
the model cannot make and the engine cannot check: that a given endpoint is fast.
Every in-process connector is a bet that someone else's host stays quick, and the
bet is settled against the one goroutine the whole engine shares.

Three things sharpen this beyond the original trade-off:

- **The promise is unenforceable.** Nothing in the compiler rejects a slow endpoint,
  nothing in the modeler shows the cost, and the endpoint that was fast when the
  model was authored is the same endpoint that is down at 3am. ADR-0149 could only
  bound the damage with a ten-second budget — a number that is simultaneously far
  too long to be invisible and far too short for some legitimate integrations.
- **The alternative is no longer expensive.** ADR-0156 rejected "ban in-process
  connectors" because it "destroys the single-binary experience". It does not any
  more: `atlas serve --supervise` runs the workers itself, from the same binary, so
  the operator still installs one artifact and runs one command.
- **Step 6 made in-process *survivable*, not *right*.** Handlers now run off the run
  loop, so a slow host stalls the request that caused it rather than the engine. That
  removes the catastrophe. It does not make the engine's own process the right place
  to run someone else's integration: the work still competes for the engine's CPU,
  memory and file descriptors, still shares its address space and credentials, and
  still dies with it.

The question this record answers: is in-process execution a supported choice, or a
transitional state?

## Decision drivers

- **A guaranteed-fast core loop.** The engine's value is durable, ordered,
  replayable execution. Everything else it does competes with that.
- **Failure should be local and visible.** A worker that wedges takes down that
  worker, and the Workers view says which one.
- **One rule beats a judgement call.** "Only if the endpoint is fast" is a judgement
  every author makes once, at authoring time, with the information they have then.
- **Don't break running installations.** In-process connectors are what every
  existing Atlas deployment uses today.

## Considered options

1. **Keep ADR-0156's ladder**: in-process stays a supported rung, now made safe by
   step 6.
2. **Deprecate in-process service tasks**: they keep working, are documented as a
   transitional state, and the modeler and docs steer every new model to a worker.
3. **Remove them**: the connector kinds only run out of process; a model using one
   fails to deploy unless a worker serves it.

## Decision outcome

Chosen: **option 2 — no in-process service tasks in Atlas, reached by deprecation
rather than removal.**

**The rule.** Every side-effecting service task belongs on a worker. Atlas's own
process runs the engine: the compiler, the processor, the log, the state store, the
API. It does not run integrations. The core loop is guaranteed fast because nothing
that can be slow is allowed to live in it.

**What stays in the engine** is unchanged and unaffected, because none of it can
block: a FEEL script task (pure, deterministic, compiled at deploy), a local DMN
evaluation (CPU-bounded library code, no network), the mockup task (a timer). This
record is about *side effects*, not about all computation — see ADR-0156's ladder,
which remains correct about everything above the connector rung.

**What this changes in practice:**

- The connector kinds keep working in-process, and a running installation is not
  disturbed. They are **deprecated**: supported, documented as transitional, and not
  the shape a new model should take.
- **New connector kinds are built worker-first.** A kind that cannot yet run on a
  worker names what is missing rather than shipping in-process by default.
- The Workers view already marks an in-process type and says that relocating it
  means turning that handler off. That marking becomes a deprecation notice.
- The ten-second call budget (ADR-0149) stays as the safety net for what remains,
  and stops being the thing that makes in-process viable.

**What is still owed**, and it is the honest gap: a connector task cannot run on a
worker yet. Every connector handler resolves its configuration from the compiled
process through a `ProcessLookup` over the local state store, which a process
outside the engine does not have — found while building `atlas worker`, and recorded
in ADR-0157. Closing it means the connector's task detail travelling *with the job*,
so a worker receives everything it needs to make the call. Until that lands, this
record states the direction and the guidance; only a model-authored job type can
actually be relocated today.

Option 3 is rejected for now on exactly that ground: banning what has no
replacement yet would break every existing deployment. It becomes available once the
detail travels with the job, and this ADR is where to revisit it.

### Consequences

- **Positive:** the engine's worst case stops depending on third parties. A wedged
  integration is one worker, named in the Workers view, restartable — not a number
  in a timeout constant. Capacity for integrations scales separately from the
  engine. Arbitrary integration code stops sharing the engine's address space and
  credentials. And the guidance becomes a rule instead of a judgement call.
- **Negative / trade-offs accepted:**
  - A worker is a second thing to run. `--supervise` makes it one flag rather than a
    deployment, but it is still more than nothing.
  - A relocated call gains a network hop, so a fast local call gets slower while the
    engine gets faster. That is the intended trade.
  - Two shapes coexist until the connector detail travels with the job, and the
    deprecated one is what every current model uses.
- **Follow-ups / risks to watch:** the connector detail on the job, which is what
  makes this actionable rather than aspirational; a deprecation notice on the
  in-process kinds in the modeler and the Workers view; and a decision, once those
  land, on whether to move to option 3.

## Pros and cons of the options

### Option 1 — keep the ladder
- Good: nothing to do; step 6 already removed the catastrophic failure mode.
- Bad: keeps the engine's worst case tied to third-party hosts, and keeps a rule
  that depends on an author's estimate of someone else's latency.

### Option 2 — deprecate (chosen)
- Good: states the direction plainly without breaking anything; new work lands in
  the right shape from the start; existing models keep running.
- Bad: two shapes coexist for a while, and the deprecated one is the one in use.

### Option 3 — remove
- Good: the rule becomes structural rather than documentary.
- Bad: nothing to migrate *to* yet for connector kinds, so it would break every
  existing deployment today.

## Links

- revises [ADR-0156](0156-in-process-vs-out-of-process-service-tasks.md), whose ladder kept in-process as a supported rung
- rests on [ADR-0157](0157-worker-processes-supervision-and-console.md), whose seven steps are what make the alternative real, and on the gap it recorded
- reframes [ADR-0149](0149-bounded-connector-call-budget.md): the budget is a safety net for a deprecated path, not what makes it viable
- leaves [ADR-0047](0047-polyglot-script-tasks-via-job-workers.md)'s inline FEEL task and the local DMN evaluation untouched — neither can block
- the Workers view that makes a relocated kind observable is [ADR-0157](0157-worker-processes-supervision-and-console.md) step 4
