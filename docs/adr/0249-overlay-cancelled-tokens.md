# ADR-0249: Cancelled tokens on the runtime overlay, and a deferred choice drawn once

- **Status:** Accepted
- **Date:** 2026-09-04
- **Deciders:** Atlas engine team

> **Implementation status.** Delivered. Two changes to what the live diagram says, one
> counter behind them. A terminated element instance now bumps its own retained counter
> (`elTerm` per instance, `elTermAgg` per definition, mirroring the visit counters of
> ADR-0022/ADR-0080), the runtime response carries it as `terminated`, and the overlay
> splits the old gray "passed through" badge into **gray = completed here and moved on**
> and **amber = cancelled here**. On top of that, an event-based gateway's race
> (ADR-0110) is drawn as the one wait it is: the live count sits on the gateway, and its
> armed branches are marked `armed` instead of each repeating that same count.

## Context and problem statement

The live overlay draws two numbers per element: green `tokens` (live here now) and gray
`visits − tokens` ("passed through"). Both come from maintained counters (ADR-0080) over
the visit history (ADR-0022), and a visit is recorded on **activation**.

That makes "passed through" mean *arrived and is no longer here* — which merges two
facts an operator needs apart. A token leaves an element either by **completing** (it
hands its token to a successor) or by being **terminated** (a losing event-gateway
branch, an activity a boundary event interrupted, a scope torn down). `applyToState`
already treats them as distinct facts — the replay trail records `ReplayCompleted` and
`ReplayTerminated` separately (ADR-0136) — but the counters the overlay reads do not.

An **event-based gateway** (ADR-0110) turns that from imprecision into a wrong answer,
because it is the one construct where cancellation is not an exception but half of
every outcome:

- The gateway arms *every* branch's catch event and completes itself, so a waiting
  instance holds a token on each branch and none on the gateway. The diagram therefore
  shows the same wait once per branch — N branches, N green counts, no green on the
  gateway — and the total token count of a raced instance reads as N.
- Every decided race activates both branches and leaves both: one completed, one
  terminated. So **both branches always carry the identical gray count**, whatever
  actually happened. A production diagram with 50 002 instances waiting and 10 941 races
  decided shows `10 941 / 50 002` on the message branch and `10 941 / 50 002` on the
  timer branch — which reads as "these two events arrived equally often" and is not what
  either number means. Which event actually won is only visible further downstream,
  where the branches stop sharing elements.

This is the question a user arrived with, on exactly that diagram. The engine is right;
the picture of it is not.

## Decision drivers

- **A number on a diagram must mean what it looks like it means.** The failure here is
  not a wrong count, it is a right count answering a question nobody asked.
- **Do not change the engine's semantics to fix a picture.** ADR-0110 chose to arm the
  real catch instances deliberately, and that choice reuses the correlate/timer/signal
  paths wholesale. Making the gateway itself the single waiter (its option 3) to make
  the overlay simpler would be invasive to the correlation machinery for a cosmetic gain.
- **Invariants hold.** The new counter is a write-only merge on the fold path (I1),
  derived from the committed event alone so replay rebuilds it identically (I4/I6), and
  read in O(elements) from a maintained aggregate rather than a scan (ADR-0080).
- **The overlay's own conventions.** Amber is not the incident red: a cancelled token is
  not a fault and nothing is waiting for an operator.

## Considered options

1. **Count terminations in their own retained counter and split the badge; collapse an
   event gateway's race onto the gateway in the view (chosen).**
2. **Derive the cancelled count in the API from the replay trail** (`ReplayTerminated`)
   instead of a counter. Rejected: the trail is per instance and unbounded in length, so
   the definition-wide overlay — the one this is for — would go from O(elements) to a
   walk of every instance's history, on the run loop (ADR-0080 exists to prevent exactly
   that), and the count would vanish when history is purged rather than being retained
   like the heatmap it belongs to.
3. **Make the visit counter count completions instead of activations.** Rejected: it
   would silently change what every existing gray badge, the heatmap and the playground
   mean, and it loses "a token got here", which is the fact an armed branch is made of.
4. **Change the engine so the gateway is the waiting element** (ADR-0110 option 3), so
   the overlay needs no special case. Rejected again, for ADR-0110's reasons.
5. **Leave the race drawn per branch and explain it in a tooltip.** Rejected: the
   misreading is in the numbers, and a tooltip does not stop two identical numbers from
   being compared.

## Decision outcome

Chosen: **option 1**, in two halves that stand on their own.

### The counter

- `cfElementTermination` (`elTerm:<procDefKey>:<piKey>:<elementId>`) and
  `cfElementTerminationAgg` (`elTermAgg:<procDefKey>:<elementId>`) mirror the visit
  families exactly — same key shape, same merge-counter mechanics, same retention. The
  per-instance rows go with the rest of an instance's history when it is purged
  (ADR-0146); the aggregate is retained, like the visit aggregate.
- `applyToState` bumps both on `IntentTerminated`, beside the existing
  `RecordElementReplay(… ReplayTerminated)`. Nothing is decremented and nothing is read,
  so the fold stays allocation-free and deterministic.
- The runtime response gains `terminated` per element, from
  `ElementTerminationTotals` (definition-wide) or `ElementTerminationHistory`
  (single instance) — the same pair of reads the visit counts use, so the two halves of
  the history always come from the same place.
- **No backfill.** Terminations before this change were never recorded, so the aggregate
  starts at zero on an existing store and the gray badge keeps its old meaning for
  history already written. A missing count reads as "nothing was cancelled here", which
  is the pre-existing state of knowledge, not a new claim.

### The overlay

- Gray is now `visits − tokens − terminated` — *completed here and moved on*. Amber is
  `terminated` — *cancelled here*. Green is unchanged.
- An event-based gateway's race is drawn once. The browser derives the group from the
  diagram (an event gateway's outgoing targets), not from the engine: a catch joins its
  gateway's group only when that gateway is its **sole** incoming flow, so a catch also
  reachable from elsewhere keeps its own count. The gateway shows the race's live count
  (the minimum over its armed branches, so a branch carrying tokens from elsewhere
  cannot inflate it) and each armed branch shows the word `armed` in place of a number.
- Deliberately a word, not a number: the count belongs to the race, and repeating it per
  branch is the misreading this record exists to remove.

## Consequences

- **Positive:** the diagram can now answer "which event actually arrived, and how often"
  at the branch itself, and a deferred choice reads as one wait. The split also pays off
  away from event gateways — an interrupting boundary event's host, a cancelled
  transaction's activities and a terminated scope's children all stop being counted as
  though they had completed.
- **Negative / trade-offs accepted:** two more column families and one more merge on the
  termination path; one more O(elements) counter read per runtime poll; and the overlay
  now knows something about BPMN structure (which catches an event gateway arms) that it
  previously did not.
- **Follow-ups / risks to watch:** the collaboration overlay carries `terminated` but
  does not yet draw it. The step-by-step instance replay (ADR-0046/0151) still shows a
  race as one token per armed branch — there that is the literal history it is replaying,
  but the two views now describe the same moment differently. The playground's own heat
  map still reads visits only. A future parallel event gateway (ADR-0110's deferred
  option 4) would arm branches that all win, and the "one race, one count" rule would
  need to say so.

## Pros and cons of the options

### Option 1 — a termination counter plus a collapsed race (chosen)
- Good: each fact gets its own number; the engine is untouched; both reads stay O(elements).
- Bad: two new column families; no retroactive data; the view gains a structural rule.

### Option 2 — derive it from the replay trail
- Good: no new state at all.
- Bad: turns the definition-wide overlay into a per-instance history walk on the run
  loop, and the count disappears with a history purge.

### Option 3 — redefine the visit counter as completions
- Good: no new counter.
- Bad: silently changes every existing gray badge and the heatmap, and loses "a token
  arrived here" — the fact an armed branch consists of.

### Option 4 — make the gateway the waiting element
- Good: the overlay would need no special case at all.
- Bad: ADR-0110 weighed and rejected this; it forces the shared correlate/timer/signal
  paths to special-case a multi-subscription waiter.

### Option 5 — a tooltip
- Good: nothing to build.
- Bad: the misreading lives in the numbers, and the numbers would still be there.

## Links

- extends the element-visit heatmap (ADR-0022) and the maintained runtime counters
  (ADR-0080); uses the completed/terminated distinction the replay fold already draws
  (ADR-0136)
- fixes what the event-based gateway (ADR-0110) looks like at runtime, without changing
  what it does
- honors I1, I2, I4, I6; per-instance rows purge with the rest of an instance's history
  (ADR-0146)
