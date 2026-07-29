# ADR-0085: Process-instance TTL — self-cleaning via the due-timer index

- **Status:** Proposed
- **Date:** 2026-07-29
- **Deciders:** Atlas engine team

## Context and problem statement

An instance that parks — on a user task, a message catch, an un-worked service task —
stays `active` forever until something completes or cancels it. A misconfiguration can
create a great many such instances (the reported `/employees` flood: ~529k parked on a
user task). The flood mitigations (per-poll inbound cap, forward-only subscriptions,
singleton message start) stop *new* runaways, and the bulk-cancel drain + one-click
"Cancel running" clear an *existing* one. What is still missing is a **standing,
automatic** bound: instances that outlive their usefulness should expire on their own,
so an operator never has to notice and drain a pile-up after the fact.

The bound must be **self-cleaning without a background full scan**. A periodic sweeper
that scans every instance for `age > TTL` is exactly the O(instances) pattern the
recent work removed from the read path — at 500k it would re-introduce a loop-blocking
scan, just on a timer.

## Decision drivers

- **No O(instances) sweep.** Expiry must scale with what is *due*, not with how many
  instances exist — the property the due-timer index already has (ADR-0051's
  `DueTimers` is a range scan up to `now`, not a full scan).
- **Deterministic recovery (I4/I6).** The expiry schedule and the termination must
  rebuild identically on replay — so both ride durable events, not a wall-clock read in
  `applyToState`.
- **Opt-in, no surprise.** A TTL that silently kills a legitimately long-running
  instance is worse than a leak. Default **off**; configured per definition; the value
  must exceed the longest expected instance lifetime.
- **Reuse, don't invent.** Atlas already schedules, persists, recovers, and fires
  timers (ADR-0051 start timers, ADR-0040 timer boundary events). A TTL is another due
  date, not a new subsystem.

## Considered options

1. **Background sweeper.** A goroutine periodically scans all active instances and
   terminates those older than the TTL. Simple, but O(instances) per sweep — the
   loop-blocking full scan we just eliminated, on a schedule. **Rejected.**
2. **A per-instance TTL timer on the due-timer index (chosen).** On activation, an
   instance with a TTL schedules a durable "instance-expiry" timer at
   `CreatedAt + TTL`. The existing timer scheduler fires due timers in due order
   (O(due)); firing an expiry timer terminates its instance through the same path
   `CancelInstance` uses. Event-driven, recovery-safe, no full scan.
3. **Lazy expiry on access.** Expire an instance only when something next touches it.
   Cheap, but it never cleans *idle* instances — precisely the ones that pile up. It
   bounds nothing on its own. **Rejected.**

## Decision outcome

Chosen: **option 2.** A process definition may declare an **instance TTL** — an
ISO-8601 duration on the `<process>` (a plain attribute like `versionTag`/
`singletonStart`, e.g. `instanceTtl="P7D"`), compiled onto the `CompiledProcess`. An
optional server-level default (env/flag) provides a global safety net; the per-
definition value wins.

- **Schedule on activation.** When an instance of a TTL-bearing definition activates,
  the processor emits a durable `TimerCreated` for an **instance-expiry** timer due at
  `CreatedAt + TTL`, keyed to the process-instance key. The due date and key come from
  the activation event, so replay recreates the identical timer (I4/I6). The timer
  rides the same `cfTimer` due-date index as every other timer — so scheduling and
  scanning are already O(due), allocation-free on the hot path (I1).
- **Fire → terminate.** The timer scheduler (ADR-0051) already scans `DueTimers(now)`
  off no full-instance scan and hands each due timer to the processor. An expiry timer's
  handler terminates its instance exactly as `CancelInstance` does — one
  `IntentTerminating` command, folded to the terminal `IntentTerminated` events that
  drop the active records, decrement the per-definition counters (ADR-0083), and retire
  any waiting job/subscription (self-retiring, as cancel already relies on). The
  instance lands in history as `terminated`.
- **Cleared on completion.** An instance that finishes before its TTL cancels its
  expiry timer in the same batch that completes it (the timer delete is idempotent, so
  a normal completion pays a single extra delete; mirrors how a boundary timer is
  retired when its host completes, ADR-0040).

Because expiry is a durable timer and a command-driven termination, the whole mechanism
is deterministic on replay and adds **no** background scan.

### Consequences

- **Positive:** parked instances self-clean, standing-bounding growth without operator
  intervention; scales with due timers, not instance count; reuses the timer subsystem
  wholesale; recovery-safe by construction. Complements the flood mitigations (prevent),
  bulk-cancel/Cancel-running (drain-now), and the O(1) UI (survive) with a
  **prevent-standing** layer.
- **Negative / trade-offs:** one extra durable timer per TTL-bearing instance (bounded
  by live instances, and its due-date write is the same cheap merge every timer pays).
  A misjudged (too-short) TTL terminates work early — mitigated by opt-in + default-off
  and by documenting that the TTL must exceed the longest legitimate instance lifetime.
- **Follow-ups / open questions:**
  - **History retention.** A TTL moves instances to the *history* (terminated), which
    itself grows unbounded. The O(1) summary (ADR-0083) makes that survivable, but a
    history-retention/compaction policy (age-based prune of terminated records) is the
    natural companion and a separate ADR — TTL bounds the *active* set, retention would
    bound the *finished* set. An alternative is a TTL that hard-deletes rather than
    terminates-to-history; deferred pending the retention decision.
  - **Config surface.** Per-`<process>` attribute vs. a `zeebe`-style extension vs. a
    runtime-adjustable per-definition setting (so a TTL can be added without a
    redeploy). Start with the compiled attribute; a runtime override can layer on later.
  - **Granularity.** A single process-level TTL to begin with; a per-activity "max time
    parked here" (a implicit timer boundary) is a richer, separate feature.

## Links

- reuses ADR-0051 (timer start events / `DueTimers` scheduler) and ADR-0040 (timer
  boundary events) for scheduling, firing, and recovery; terminates through the
  ADR-0017 cancellation path; decrements the ADR-0083/0080 per-definition counters;
  honors I1 (no hot-path allocation), I4/I6 (durable, replay-deterministic). The
  standing-prevention complement to the ADR-0075 flood mitigations and the bulk-cancel
  drain.
