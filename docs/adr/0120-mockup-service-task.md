# ADR-0120: Mockup (engine-simulated) service tasks

- **Status:** Accepted
- **Date:** 2026-08-13
- **Deciders:** Atlas engine team

## Context and problem statement

Developing a process that has service tasks currently requires a real
counterpart for every one of them before the flow can be exercised end-to-end:
an external job worker, or a configured connector (REST, mail, clio, …) with a
reachable endpoint and credentials. While the integration to an Umsystem is
still being built, the task simply parks — the modeller cannot play the process
through, see downstream branches, or demo the model.

We want a way to mark a service task as a **mockup** so the engine simulates it
itself: it waits a configurable (optionally random) time, optionally produces
output variables from the instance's inputs (e.g. a stand-in for a REST/Umsystem
response), and can optionally fail so error and retry paths can be exercised —
all without an external worker or connector.

## Decision drivers

- **Playability while integrations are stubbed** — the headline goal.
- **Do not block or slow the engine.** In the single-binary server, job workers
  run synchronously on the run loop (`jobRunner.Drive()` inside `do(...)`), so a
  worker that slept for the simulated duration would freeze *every* instance.
- **Replay determinism (invariant I6).** Any "random" duration or failure draw
  must be frozen into events, never recomputed on recovery.
- **No new special-casing across the codebase.** Reuse the existing timer and
  incident machinery rather than inventing a parallel one.

## Considered options

1. **A blocking in-process worker** on a reserved job type that `time.Sleep`s the
   duration and completes the job.
2. **An engine-native element** that arms a one-shot timer for the duration and
   completes (or fails) when it fires — no job.
3. **A durable "delayed job"** primitive (a job that only becomes activatable at a
   due time), then a worker.

## Decision outcome

Chosen option: **"2 — an engine-native element"**. A service task carrying an
`<atlas:mockupConnector>` extension compiles to a new node type `TypeMockupTask`
with its own `mockupTaskBehavior`:

- **On activation** it evaluates the optional FEEL result expression over the
  instance's variables and writes the result variable *immediately* (exactly like
  the inline FEEL script task), then arms a one-shot timer whose due date is
  `now + duration`. It stays activated.
- **When the timer fires**, the existing `handleTimerTriggered` drives the element
  to completion — unless the failure draw selects failure, in which case a
  **job-less incident** is raised (the element parks, mirroring the ADR-0064/0111
  timer-schedule incident). Resolving the incident re-arms a fresh attempt through
  the existing `rearmTimerElement` path.

The duration and the failure decision are pure functions of the **timer key**,
which is generated live and frozen into the `TimerCreated` event; the due date is
frozen too. On recovery neither the behavior nor `handleTimerTriggered` re-runs —
only events replay through `applyToState` — so no new nondeterministic source
enters the engine and I6 holds. Because each re-arm mints a fresh timer key, every
retry draws an independent duration and outcome, so resolving a simulated-failure
incident can eventually succeed. The failure probability is stored as
parts-per-million so the whole decision stays integer-pure across live and replay.

The result variable is written on **activation**, not on completion: the generic
completing path runs output mappings and then drops the activity-local scope
before `OnCompleting`, so a result written there would be discarded and invisible
to output mappings.

### Consequences

- **Positive:** a process is fully playable with its service tasks stubbed; the
  simulated wait is durable, non-blocking, and visible in the timeline as a
  timer; input→output is authored in FEEL (the same fx toggle as the connectors);
  failure/retry paths are exercisable. No server worker, no engine hot-path
  change, no new event type — it composes the timer, variable, and incident
  machinery that already exist.
- **Negative / trade-offs accepted:** the duration is drawn uniformly from the
  key hash (good enough for a dev mockup, not a statistical model); a simulated
  failure raises a technical **incident** rather than throwing a BPMN error.
- **Follow-ups / risks to watch:** an optional `errorCode` to throw a BPMN error
  (for error-boundary/event-subprocess playthrough) is a natural later addition.

## Pros and cons of the options

### Option 1 — blocking worker
- Good: trivially small; reuses the job path and `HandleWithOutput`.
- Bad: blocks the run loop for the whole simulated duration, freezing every
  instance; the delay is not durable (a restart re-sleeps from scratch) and is
  invisible in the timeline.

### Option 2 — engine-native timer element (chosen)
- Good: non-blocking, durable, replay-safe, timeline-visible; reuses timer +
  incident plumbing; no server worker.
- Bad: a new node type and behavior (though small and modelled on existing ones).

### Option 3 — delayed-job primitive
- Good: general — other features could use a due-time job.
- Bad: a new durable primitive and index for a dev-only need; more surface than
  the problem warrants.

## Links

- builds on ADR-0054 (timers) and ADR-0064/0111 (job-less timer incidents)
- follows the "distinct type, shared behavior" precedent of ADR-0036
  (`TypeConnectorTask`) and ADR-0112 (`TypeSendTask`)
- authored through the connector catalog of ADR-0067
