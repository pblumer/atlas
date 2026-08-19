# ADR-0146: History expiry as a due-date index — retention that scales with what is due

- **Status:** Accepted
- **Date:** 2026-08-19
- **Deciders:** Atlas engine team

## Context and problem statement

ADR-0144 let a process declare `atlas:historyTtl`, so a finished instance is hard-deleted
once it outlives the TTL its own model states. The deletion rides ADR-0115's sweep: a
bounded, resumable window over the finished-instance history, `retentionBatch` records per
tick, a cursor that advances and wraps.

That window walks `cfProcessInstanceHistory` in **instance-key order**, and every record in
it costs a decode and an eligibility check — whether or not anything could ever delete it.
The batch is therefore a **scan** budget, not a **purge** budget, and the time until a due
instance is actually purged scales with the size of the whole history rather than with how
much is due.

The first production install made that concrete. Its history holds ~529,000 finished
instances, almost all from four versions of one bulk test process that declares no TTL and
is governed by no server-wide max age — they can never be eligible. A newly finished
instance of a *different* definition, carrying `historyTtl="PT1M"`, has one of the highest
keys in the family, so the cursor reaches it only at the end of a full pass: at the default
1000 records per minute, **roughly nine hours** after it was due. From the outside this is
indistinguishable from a broken feature, and the operator's only lever is to raise the
batch — which buys drain rate with run-loop time and still scans half a million records to
find three.

This is exactly the property ADR-0085 rejected when it designed the instance TTL: *expiry
must scale with what is due, not with how many instances exist.* The active set got that
property from the due-timer index. The finished set never did.

## Decision drivers

- **Scale with what is due.** A tick's cost must be a function of the instances whose TTL
  has elapsed, not of the history's size. A server with a million untouchable finished
  instances and one due record must pay for the one.
- **Reuse the index shape Atlas already has.** `cfTimer` is a due-date-ordered family
  scanned as a range up to `now` (ADR-0051). A history expiry is another due date.
- **Deterministic (I4/I6).** The schedule must rebuild identically on replay, so the due
  date rides a durable event and the index is written only by `applyToState`.
- **The export gate is untouchable.** Nothing may be deleted before its events are
  provably exported (ADR-0115); an entry that is due but not yet exportable must simply
  wait, not disappear.
- **No behavior change for the server-wide age.** `--retention-max-age` covers definitions
  that declare nothing, including everything finished before this feature existed. That
  path must keep working exactly as it does.

## Considered options

1. **Operational only: raise `--retention-batch`, lower `--retention-interval`.** Already
   possible (ADR-0144). It moves the constant, not the shape — the sweep still reads the
   entire history to find what is due, now more often. Kept as a knob for the server-wide
   age; **rejected** as the answer.
2. **A per-definition secondary index** (`defKey:piKey → nil`) plus a per-definition
   cursor, so the sweep visits only definitions that declare a TTL. Removes the wasted scan
   over foreign definitions, but still walks a TTL-bearing definition's *entire* finished
   set to find the few that are due, and adds per-definition cursor bookkeeping to the
   sweep. **Rejected** — the right key is the due date, not the definition.
3. **A due-date index (chosen).** `histExp:<dueDate>:<piKey> → nil`, written when an
   instance of a TTL-bearing definition finishes. The sweep range-scans it up to `now`;
   what it returns *is* the candidate set.
4. **Reuse `cfTimer` with a "purge" timer per finished instance.** Maximum reuse, but a
   timer's firing is a command into the processor, while a purge must first clear an
   export gate evaluated *outside* the loop — so a gated purge would have to re-arm a
   durable timer every tick, writing events to say "still not exportable". **Rejected.**

## Decision outcome

Chosen: **option 3.** A second due-date-ordered family, `cfHistoryExpiry`
(`histExp:<dueDate>:<piKey> → nil`), mirroring `cfTimer`'s shape.

- **The due date is frozen at completion and carried by the event.** `ProcessInstanceValue`
  gains a trailing, append-compatible `PurgeDueDate`. When an instance of a definition
  declaring `historyTtl` completes or is terminated, the processor stamps
  `PurgeDueDate = now + historyTtl` onto the terminal event — the same "freeze it at
  command time, carry it in the event" pattern ADR-0085 used for the instance-expiry timer.
  `applyToState` folds the terminal event into the history record *and* the index entry, so
  replay rebuilds both identically (I4/I6) and nothing reads a clock in the fold.
- **The sweep asks the index, not the history.** Each tick range-scans `cfHistoryExpiry`
  from the family start to `now`, up to `retentionBatch` entries — every one of them due by
  definition. For each, it reads the instance's history record, applies the unchanged
  export gate (`0 < CompletedPosition ≤ safePosition`), and enqueues the same
  `IntentPurging` command. The batch now bounds **purges**, which is what an operator
  tuning it means.
- **`applyToState(IntentPurged)` drops the index entry** along with the history record and
  every per-instance family, from the `PurgeDueDate` the purge event carries — so the index
  never outlives what it points at, on the live path and on replay alike.
- **The key-order scan survives, narrowed.** It now runs **only** when a server-wide
  `--retention-max-age` is configured — the case it was built for, and the only way to
  reach records that predate this index. With no server-wide age, a tick that finds nothing
  due does one bounded range scan and stops; the deployment-registry walk ADR-0144 used to
  decide whether a sweep was worth running is gone, because the index answers that
  directly.
- **A gated entry waits at the front.** An entry whose export gate is still shut stays in
  the index and is re-read on the next tick, in due order. Nothing is lost, and nothing
  advances past a record it could not delete.

### Consequences

- **Positive:** purge latency is now a function of what is due — the field case above goes
  from ~9 hours to the next tick; a tick on an idle server costs one empty range scan
  instead of 1000 decodes; `--retention-batch` means what its name says; the delete path,
  the export gate, and the server-wide age are all unchanged; the index reuses the exact
  shape (`appendOrderedInt64` + key suffix, range scan to `now`) that the timer index has
  used since ADR-0051, so it inherits its ordering and recovery properties.
- **Negative / trade-offs accepted:** one extra index row per finished instance of a
  TTL-bearing definition (16-byte key, empty value), written in the same batch as the
  history record; the due date is **frozen at completion**, so redeploying a definition with
  a different `historyTtl` governs only instances that finish afterwards — predictable, and
  the same rule the instance TTL already follows, but no longer the "resolved at sweep
  time" behavior ADR-0144 shipped; instances that finished **before** this feature, or
  before their definition carried a TTL, have no index entry and are reachable only through
  the server-wide age — deliberately the same posture ADR-0115 took for records with no
  `CompletedPosition`, and preferable to a startup backfill that would write state no event
  ever produced (I4); a permanently shut export gate holds the front of the window, which
  is a stall on the gate, not on the index.
- **Follow-ups / risks to watch:** surfacing a finished instance's purge due date in
  Operations (it is now a real, queryable schedule, not a computation); a "purge now"
  action for the impatient case; and the standing ADR-0115 note that purging reclaims the
  *state store*, not the WAL.

## Pros and cons of the options

### Option 1 — raise the batch, shorten the interval
- Good: no code; already available.
- Bad: keeps the O(history) shape; buys drain rate with run-loop time; still reads
  half a million ineligible records to find three due ones.

### Option 2 — per-definition index with per-definition cursors
- Good: skips foreign definitions entirely.
- Bad: still scans a TTL-bearing definition's whole finished set; cursor bookkeeping per
  definition; wrong key — due-ness, not ownership, is what the sweep selects on.

### Option 3 — due-date index
- Good: candidates are exactly what the scan returns; ordered oldest-first; reuses the
  timer-index shape; replay-safe by construction.
- Bad: a second family to maintain; a frozen due date; no entries for legacy records.

### Option 4 — purge timers on `cfTimer`
- Good: no new family at all.
- Bad: conflates engine timers (which fire commands) with an out-of-loop export gate; a
  gated purge would re-arm a durable timer per tick, writing events to record inaction.

## Links

- extends ADR-0144 (per-definition history TTL — the `atlas:historyTtl` this schedules)
- extends ADR-0115 (history retention — the purge event, the export gate, the sweep)
- mirrors ADR-0051 (due-timer index — the range-scan-to-now shape reused here)
- follows ADR-0085 (instance TTL — "scale with what is due", and the frozen due date
  carried in the event)
