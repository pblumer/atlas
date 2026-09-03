# ADR-DRAFT: A reverse index from call activity to child instance

- **Status:** Proposed
- **Date:** 2026-09-03
- **Deciders:** Atlas engine team

## Context and problem statement

A call activity starts a child process instance, and the child records which
call-activity element instance started it (`ParentElementInstanceKey`, ADR-0076).
That is the direction the child needs: on completion it resumes its caller.

The engine also needs the *other* direction. When a call-activity element instance
is terminated — the caller is cancelled, or an interrupting boundary event fires —
its child must be torn down with it, or the child outlives its caller and runs
forever. `terminateChildInstance` answered "what did this element start?" the only
way the store allowed: by walking **every live process instance** and comparing
each one's parent link.

That walk is O(instances), it runs inside the processor (so it holds the single
writer, I3), and it runs **once per call-activity element instance being torn
down**. Cancelling a caller that holds many children is therefore quadratic in the
instance population. A September 2026 test made the shape concrete: one seeder
instance held 10,000 children among ~11,000 live instances, so cancelling it would
have compared on the order of 10^8 records — with the engine stopped throughout.

The replay view has the same missing direction, for the same reason (it builds a
parent→child map so a call-activity step can link to the instance it started), and
solves it the same expensive way.

## Decision drivers

- **The engine's own path must be sublinear.** A read endpoint that scans can at
  least be moved off the loop (ADR-draft-off-loop-queries). The processor cannot:
  the teardown *is* the write.
- **Derived state must be event-driven.** Anything added has to be written from the
  event that causes it, so replay rebuilds it identically (I4/I6).
- **No behaviour may depend on the upgrade.** An existing store holds children whose
  link was never indexed. If the index is simply "empty until new children arrive",
  a cancel after upgrade silently orphans every pre-existing child — a correctness
  regression dressed as a performance fix.
- **Reuse the existing shape.** Atlas already keeps `elByProc` (element instances of
  a process) as exactly this kind of reverse index.

## Considered options

1. **Keep the walk, make it cheaper.** Cache the parent→child map per batch, so one
   cancel walks once instead of once per call activity.
2. **A `childByParent` column family (chosen).** Index the link on the child's
   activation, drop it on the child's terminal event, read it as a prefix scan.
3. **Store the child key on the call-activity element instance.** Write the link
   into the parent's own record when the child is created.

## Decision outcome

Chosen: **option 2**, a `cfChildByParent` column family keyed
`<callElementInstanceKey>:<childProcessInstanceKey>` with no value.

- **Written from `applyToState`** on the child's `IntentActivated`, from the child's
  own record, and deleted on its `IntentCompleted`/`IntentTerminated` alongside the
  active record it mirrors. Both come off the event, so replay rebuilds the index
  and recovery is unaffected (I4/I6).
- **Live children only.** The teardown asks about children that still exist, and a
  link left behind after a child finished would make every later cancel of a
  long-lived caller walk a growing list of the dead.
- **Seeded once on open** for stores written before the index existed
  (`backfillChildIndexIfNeeded`, marker `child_index_v1`), the same one-time
  migration shape as the ADR-0080/0083/0142 counter backfills. Without it, upgrading
  would break the very teardown the index exists to serve.
- **The engine reads committed state**, exactly as the walk it replaced did, so the
  teardown remains a pure function of what is durable.

### Consequences

- **Positive:** Tearing down a call activity is O(its own children) instead of
  O(every live instance), so cancelling a caller with many children stops being
  quadratic — and stops holding the run loop while it is.
- **Positive:** The index makes "what did this call activity start?" a first-class
  question, which is also what the replay's drill-in link wants.
- **Negative / trade-offs accepted:** One more derived family to keep consistent,
  and one more one-time migration on open. Both follow patterns already in the
  store, but they are still surface that can drift from the record it mirrors.
- **Follow-ups / risks to watch:** The replay timeline still builds its
  parent→child map by walking both instance families, because it wants *finished*
  children too and this index deliberately holds only live ones. Serving that view
  from an index means either retaining terminal links (and purging them with the
  instance, ADR-0115) or a second, history-side index. Until then that walk is
  merely off the run loop, not cheap.

## Pros and cons of the options

### Option 1 — cache the walk per batch
- Good: no new persisted state, no migration, no recovery surface.
- Bad: still O(instances) per batch. It turns a quadratic cost into a linear one but
  leaves the engine walking every instance to cancel one caller.
- Bad: a per-batch cache in the processor is exactly the kind of shared mutable
  state I3 warns about.

### Option 2 — a reverse column family
- Good: O(children) lookup, event-driven, replay-safe, and the same shape as
  `elByProc`.
- Bad: derived state to maintain on two events, plus a migration for existing stores.

### Option 3 — store the child key on the parent element instance
- Good: no new family; the answer sits on the record already being read.
- Bad: the parent's record would have to be rewritten *after* the child is created,
  so one logical fact would be written by two events and could disagree if only one
  replayed. It also cannot hold more than one child without becoming a list, which
  is a column family with extra steps.

## Links

- relates to ADR-0076 (call activities and the parent link this indexes in reverse)
- relates to ADR-0080 (sublinear views via maintained state) — same doctrine, applied
  to the processor's own path rather than a read endpoint
- relates to ADR-draft-off-loop-queries, which moved the *read* side's remaining
  walks off the run loop
- relates to ADR-0115 (history retention) — a future history-side variant of this
  index would have to be purged with the instance
