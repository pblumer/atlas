# ADR-DRAFT: A scope drains only when no token is in transit into it

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-11
- **Deciders:** Atlas maintainers

## Context and problem statement

A scope — a process instance's root or an embedded subprocess — completes when its
last token leaves it. `completeScope` decides that from the scope's active-child
counter: `applyToState` adds one when an element instance is `Activated` and takes
one away when it is `Completed` or `Terminated` (ADR-0001, ADR-0074).

The counter does not see a token between two elements. An element that completes and
takes a sequence flow writes its `Completed` now and its successor's `Activating` as a
followup, which the next batch runs (`completeAndTakeFlows`, `activateElement`). For
the length of that one batch the token is in neither place the counter counts. A
branch that ended inside the window — an end event, or a dead end such as a
compensation handler — found the counter at zero and completed the scope. The
successor was then activated inside an instance that had already written its
`Completed`.

ADR-0284 found this as a compensation defect: a liveness guard on activation dropped
the token a compensation throw was handing on, because the throw's scope had drained
on the handler's completion first. #1013 recorded it and asked whether the
neighbouring throw events behaved the same. Measured for this record, the shape is not
specific to compensation or to throw events. Any element that hands its token on over
a flow while another branch of the same scope ends does it — a plain task, a none,
signal or escalation throw, a link throw and its catch, a compensation throw whose
handler finishes at once.

What it costs a user is not only a wrong order of events. When the token in transit
was headed for a service task, the instance reported itself completed and the task
was activated afterwards with a job for it: an active element and an activatable job
belonging to an instance that the Operations list, the history exporter, retention
and any caller waiting on completion all consider finished — and that no completion
will ever collect.

## Decision drivers

- An instance reports completed only when no work is left in it (BPMN: a process
  instance completes when it has no tokens).
- Recovery must reach the same decision (I4, I6): the rule may not depend on anything
  a restart loses.
- `applyToState` and the log format stay as they are, so existing logs replay into
  the state they always did.
- No allocation on the processor path (I1); the cost must be measurable and small.

## Considered options

1. **Treat a queued flow activation into the scope as a token.** `completeScope` also
   asks whether an element activation over a sequence flow into the scope is still in
   the batch's queue or followups (`TokenEntering`), and waits if one is.
2. **Count the token in transit in `applyToState`.** Raise the scope's counter when an
   element completes with an outgoing flow, or lower it only when the successor
   activates.
3. **Guard every activation on the instance being live** — ADR-0284's rejected
   option 3: drop an activation whose instance has completed.
4. **Defer the drain decision to a followup command** that re-reads the counter a
   batch later.

## Decision outcome

Chosen option: **1, a queued flow activation into the scope is a token**, because it
fixes the decision where it is made, on the command path, and reads only what a
restart restores: the queue a batch has not run yet is its continuation, persisted
with it and put back on recovery (ADR-0271). `TokenCanStillReach` already decides the
inclusive join the same way, from the same two lists.

Only activations over a sequence flow count (`SourceFlowId >= 0`). An armed boundary
event, an event-subprocess trigger, a compensation handler and an ad-hoc activity are
entered some other way; none of them is a token the scope waits for, and counting an
armed boundary whose host has gone would keep a scope open for good.

The token in transit ends in an end event or a dead end of its own, and both call
`completeScope` again, so a scope that waited completes when that token leaves it.

### Consequences

- **Positive:** an instance no longer reports completed with a token on its way, and
  no element is activated, and no job created, in an instance that has completed.
  This holds for every model with parallel branches of different lengths, not only
  for compensation.
- **Positive:** `applyToState`, the record formats and the counter are unchanged, so
  every existing log replays exactly as before. A resumed run reaches the same outcome
  at every batch boundary, with the state store kept or rebuilt from the log
  (`TestAScopeDrainResumesAtEveryBatchBoundary`).
- **Negative / trade-offs accepted:** `completeScope` scans the unrun part of the
  batch's queue and its followups when a scope's counter reaches zero — once per
  scope completion, O(queue), allocation-free. Measured: 2000 instances started at
  once and run to completion took the same time with and without the scan, and a
  single instance's lifecycle allocates 219 times either way.
- **Negative:** an instance that completed early before this change keeps its log:
  its `Completed` stays where it was written, and an element instance or job created
  in it afterwards stays in the state store. This record fixes the decision, not the
  data an earlier one left behind.
- **Follow-ups / risks to watch:** a terminate end event, a cancel end event and an
  interrupting event end the element instances of their scope, and nothing ends a
  token in transit into it. Such a token runs on after the terminate — measured with
  a fork whose other branch passes a task on its way to a service task: the instance
  stays active at that task. That gap predates this record and behaves the same with
  and without it; it needs its own decision about dropping a queued activation into a
  terminated scope, the scope-level counterpart of ADR-0284's instance filter.
  The liveness-guard trap ADR-0284 described stays a trap: a guard that drops
  activations into a finished instance would now hide the next defect of this kind
  instead of exposing it.

## Pros and cons of the options

### Option 1 — a queued flow activation is a token (chosen)
- Good: one decision point, on the command path; restored by the continuation.
- Good: no change to `applyToState`, the log or the counter.
- Bad: a scan of the queue per scope completion.

### Option 2 — count the token in `applyToState`
- Good: O(1) at the decision.
- Bad: changes what the counter means, so a log written before the change replays into
  different counters than it produced — exactly what I4 forbids — unless the change
  ships with a migration of every stored counter.
- Bad: `applyToState` would need the compiled graph to know whether an element has an
  outgoing flow.

### Option 3 — guard activation on a live instance
- Good: small.
- Bad: drops the token instead of waiting for it; the instance has already reported
  completed, so the work is lost and the defect hidden. ADR-0284 rejected it for
  breaking compensation.

### Option 4 — defer the drain to a followup
- Good: no scan.
- Bad: a new command kind in the log and the continuation, and a batch of latency on
  every completion, for the same outcome as option 1.

## Links

- Fixes #1013.
- Relates to ADR-0284 (where the defect was found), ADR-0271 (the persisted
  continuation this relies on), ADR-0103 (compensation), ADR-0074 (subprocess scopes),
  ADR-0001 (one `applyToState`).
