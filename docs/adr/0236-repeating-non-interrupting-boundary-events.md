# ADR-0236: A non-interrupting message or signal boundary event stays armed

- **Status:** Accepted
- **Date:** 2026-09-03
- **Deciders:** Atlas engine team

## Context and problem statement

A non-interrupting boundary event is a *reminder*: it fires while its host activity
keeps working, spawns a parallel token down its own outgoing flow, and leaves the host
alone. BPMN puts no limit on how often it does that — for as long as the host runs, every
occurrence of the trigger fires it again.

[ADR-0040](0040-boundary-events.md) built one firing path for every boundary kind: the
timer/message path drives the boundary element instance to `Completing`, and its
behaviour interrupts the host if interrupting and then takes the outgoing flow. Taking
the flow *completes the boundary element instance*, and its subscription retires with
it. [ADR-0054](0054-date-cycle-timers-for-catch-and-boundary.md) noticed this for
recurring timers and carved out an exception — `fireRecurringBoundary` takes the
outgoing flow **without** completing the instance, then arms the next occurrence — but
that exception was written for cycle timers only.

So a non-interrupting **message** or **signal** boundary fired exactly once. The second
message correlated to nothing and was dropped: no incident, no log line, no way for the
sender to tell. A load test modelling one long-running process instance per employee hit
this while looking for somewhere to record repeated product events, and had to fall back
to a loop in the root scope — the only construction left that both repeats and can write.

The same question had already been answered the other way one layer up: an event
subprocess ([ADR-0082](0082-event-subprocesses.md)) re-arms its message and signal
trigger after each firing. Two constructions that a modeller reasonably reads as
interchangeable disagreed about whether "non-interrupting" means "repeatedly".

## Decision drivers

- **BPMN conformance.** The specification does not make a non-interrupting boundary
  one-shot, and neither Camunda nor Zeebe do.
- **Consistency with what already exists.** A recurring timer boundary and a
  non-interrupting event-subprocess trigger both already repeat.
- **Silent failure is the worst kind.** The old behaviour lost messages with no signal
  of any sort — the deploy succeeded and the model ran.
- **Invariants.** Whatever the mechanism, it must be event-sourced so replay rebuilds it
  (I4/I6), and it must not open a window in which a scope can be left holding an element
  instance that can never finish.

## Considered options

1. **Stay armed** — take the outgoing flow and re-open the subscription on the *same*
   element instance, never completing it. What `fireRecurringBoundary` does for timers.
2. **Complete and re-arm** — complete the boundary instance as today, then activate a
   fresh one attached to the same host. What `armEventSubTrigger` does for an event
   subprocess.
3. **Leave it, document it** — declare a non-interrupting message boundary one-shot and
   say so in the handbook.

## Decision outcome

Chosen option: **"Stay armed"**. On firing, a non-interrupting message or signal
boundary takes its outgoing flow and re-opens its subscription, and its element instance
never leaves `Activated`. It is disarmed exactly as before — by `disarmBoundaryEvents`
when the host completes, or by the scope teardown when something interrupts it.

Arming and re-arming share one function (`openBoundarySubscription`), so a re-armed
boundary waits on precisely what a freshly armed one waits on, including a correlation
key re-evaluated against the instance's current variables.

Option 2 looks equivalent and is not. The replacement instance is only a *queued
command* for the rest of the batch, while `disarmBoundaryEvents` scans live element
instances. A host completing inside that window disarms the boundary that no longer
exists and misses the one about to exist — and unlike an event-subprocess trigger, a
boundary instance **is** counted in its scope's active-child counter, so the survivor is
an element instance holding open a scope that has already drained. Staying armed has no
such window: there is never a moment when the boundary is not a live element instance.

Option 3 was rejected on the drivers: it is not what the specification says, not what the
neighbouring constructions do, and the failure it preserves is silent.

### Consequences

- **Positive:** a non-interrupting message/signal boundary now repeats, matching BPMN,
  matching recurring timer boundaries, and matching event subprocesses. The reminder
  pattern ("nudge every time procurement pings us, while the case stays open") is
  expressible on a boundary event again.
- **Positive:** one arming path for message and signal boundaries instead of two copies
  of the same subscription literal.
- **Negative / trade-offs accepted:** a model that relied on the old one-shot behaviour
  now fires more than once. This is a behaviour change for deployed models, taken
  deliberately: the old behaviour was a defect, silent, and had no way to be depended on
  on purpose. A model that wants exactly one firing has the interrupting flag, or a
  guard on the reminder branch.
- **Negative:** the boundary element instance's visit count no longer increments per
  firing (it is activated once); the reminder branch's own counts are the record of how
  often it fired. This already held for recurring timer boundaries.
- **Follow-ups / risks to watch:** the *escalation* and *conditional* boundary kinds are
  still one-shot when non-interrupting. They fire by a different mechanism — they arm
  inert and are *found* by `propagateEscalation` or by a variable-change re-check rather
  than by a subscription — so they are a separate question, deliberately left open here
  rather than changed by inference.

## Pros and cons of the options

### Option 1 — stay armed
- Good: no disarm window; no child-counter hazard; reuses the ADR-0054 shape.
- Good: the subscription is re-opened by an ordinary event, so replay rebuilds it.
- Bad: the boundary instance's own lifecycle no longer reflects "it fired" — the
  reminder branch does. (Already true for recurring timers.)

### Option 2 — complete and re-arm
- Good: mirrors the event-subprocess trigger, which is the closest existing analogue.
- Bad: a queued re-arm can outlive the disarm that was meant to retire it, and a boundary
  instance is counted against its scope — the leak is a scope that never completes.

### Option 3 — document the limitation
- Good: no behaviour change for deployed models.
- Bad: not BPMN; inconsistent with two neighbouring constructions; the failure mode it
  keeps is a silently dropped message.

## Links

- relates to [ADR-0040](0040-boundary-events.md) — the boundary-event lifecycle this refines
- relates to [ADR-0054](0054-date-cycle-timers-for-catch-and-boundary.md) — the same
  "fire without completing" shape, for recurring timers
- relates to [ADR-0082](0082-event-subprocesses.md) — the non-interrupting trigger re-arm
- relates to [ADR-0088](0088-signal-events.md) — signal boundaries share the fire path
