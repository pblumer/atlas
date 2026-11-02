# ADR-0518: Rebuilding the multi-instance history index for stores that predate it

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-02
- **Deciders:** atlas maintainers, Operations History at Scale programme owner
- **Approved by:** programme owner, 2026-11-02 (shadow replay; the swap may delay a startup)
- **Open question:** whether compacted logs get per-instance readiness (option 2) or stay unavailable (option 3)
- **Question checked:** 2026-11

## Context and problem statement

ADR-0517 indexes multi-instance (MI) history in `applyToState`, and serves it
only from a store whose index is proven complete: a durable readiness marker is
written when a store is created empty. Every store that recorded history before
the index (or before its schema v2, or before decision attribution) answers
`history_unavailable` for the bounded MI routes, for good. ADR-0517 promised "a
one-time bounded background backfill for pre-index histories" without saying how
one is proven complete. This record decides that.

What the retained state can and cannot prove (verified 2026-11-02):

- **State alone is insufficient.** The retained element replay rows
  (`ElementReplayValue`) carry token lineage but neither the MI role nor the
  `FlowScopeKey`. A finished iteration's own element instance and its
  `loopCounter` variable are deleted when it ends (ADR-0068). Inferring body
  membership from token trails or element ids is unsafe for repeated, nested and
  migrated bodies — exactly what ADR-0517 forbids.
- **The WAL is sufficient when it is complete.** Every fact the fold needs is in
  the events: the MI role and `FlowScopeKey` on `Activated`, the `loopCounter`
  variable event before it, incidents and decisions. But the fold reads *live
  state* as it applies an event — the iteration's `loopCounter` variable, the
  scope chain of an incident's element — so the events must be applied in order
  to a state that has everything earlier events wrote. Replaying only the MI
  fold over today's state would attribute wrongly.
- **WAL compaction is opt-in and off by default** (`--compact-wal`, ADR-0131).
  A store whose log still starts at position 1 can be rebuilt exactly; a
  compacted one cannot, for any instance whose events lie before the cut.
- **A checkpoint restore** (ADR-0131) gives state as of a position without the
  events before it; the same rule applies.

## Decision drivers

- Never present an incomplete MI history as complete (ADR-0517, I6).
- One `applyToState`: a rebuild may only reuse the deterministic fold (I4).
- No unbounded work on the single writer, no single huge transaction (I3).
- Restart-safe, idempotent, resumable; durable progress.
- Purged instances stay purged.

## Considered options

1. **Shadow replay.** Replay the retained WAL from position 1 through the
   unchanged `applyToState` into a temporary store, off the writer, then copy the
   MI families into the live store in bounded batches and set the readiness
   markers once the copy reaches the live store's position. Only possible when
   the log starts at position 1.
2. **Per-instance readiness.** Like option 1, but for a compacted log mark each
   instance whose *first* event lies after the cut as complete, and keep the rest
   unavailable — answered per instance instead of per store.
3. **Keep the store unavailable** (today's behaviour) and document that only
   instances recorded after an upgrade on a fresh store can be explored.
4. **Legacy fallback:** answer from the full timeline for small instances.

## Decision outcome

Chosen: **option 1**, with the swap at startup; option 3 remains the explicit
answer wherever option 1's precondition fails. Option 2 for compacted logs is
not decided and stays an open question.

Shadow replay, in detail:

- Precondition, checked before any work: the WAL's first retained record is
  position 1 and there is no gap up to the live store's last applied position
  *P*. Otherwise the store stays unavailable and says why.
- Phase 1: replay records 1..P through the unchanged `applyToState` into a
  temporary store under the data directory (`mi-history-rebuild`), committing
  every 50,000 events with the applied position. This only reads the log.
  Progress is the temporary store's own last-applied position: a restart resumes
  it. As built, phase 1 also runs at startup, before the processor accepts
  commands — the programme owner accepted the startup delay — so the live store
  does not move while it runs.
- Phase 2, the swap. The live store's MI families cannot simply be re-folded for
  positions after *P*: the fold reads state as of each event, and live state has
  moved on (an ended iteration's variables are gone). So the temporary store
  keeps replaying to the live position, and the swap happens where the writer is
  not serving: at the next startup, after `Recover` and before the processor
  accepts commands. The programme owner accepted that this delays that startup
  by the time the copy takes. The temporary store catches up the last records, the live MI families are
  deleted by range tombstones, the temporary store's MI families are copied in
  batches with a durable "copied through key K" marker, and the readiness
  markers are written in the batch that completes the copy. The MI families are
  a small part of the state (about 220 bytes per iteration before compression),
  so the swap is bounded by the MI history, not by the log.
- A restart in any phase resumes it: phase 1 from the temporary store's
  last-applied position, phase 2 from K. Until the markers are written, every
  bounded MI read keeps answering `history_unavailable`.
- Purge: a purge event in the replayed range deletes the instance's ranges in
  the temporary store, so a purged instance is not resurrected.

### Consequences

- **Positive:** existing installations that never compacted their log get the
  bounded MI history for every instance, proven by the same fold the recovery
  tests already hold to live state.
- **Negative / trade-offs accepted:** phase 1 costs a full replay of the retained
  log in time and temporary disk (measured: 2.9 s for a log of about one million
  events on a 4-vCPU container; a long-lived installation can have far more);
  the swap delays one startup (or needs one deliberate pause) by the time it takes to copy the MI families.
- **Follow-ups / risks to watch:** disk reserve (ADR-0481) for the temporary
  store; an operator-visible progress indicator; a test matrix for interruption
  in every phase, a compacted log, a checkpoint restore, purge during the copy,
  and live MI activity during the copy.

### As built (2026-11-02)

`engine.(*Processor).RebuildMIHistory` (called by `atlas serve` after recovery),
`state.(*Store).AdoptMIHistory` and `MIHistoryRebuildNeeded`. All MI families
lie in one contiguous key range (`0x38`–`0x43`, guarded by a test), so the swap
is one range tombstone and one ordered walk, committed with `fsync` every 10,000
keys under the progress marker `mi_history_rebuild_copy`. Tests: the rebuilt
index of a store stripped of its MI families and markers equals the live index
(bodies, iterations, state and incident pages, incident records); a compacted
log is refused with a reason; an interrupted copy resumes to the same keys; a
shadow store at another position or without its own markers is refused.
Startup logs `server.mi_history_rebuilt` or `server.mi_history_unavailable`.

Not built: an operator-visible progress indicator; a disk-reserve check before
the shadow store is written (it holds a full copy of the state, ADR-0481);
per-instance readiness for compacted logs (open question).

## Pros and cons of the options

### Option 1 — Shadow replay
- Good: exact; reuses the one fold; resumable.
- Bad: proportional to the whole retained log; needs temporary disk; impossible
  after compaction.

### Option 2 — Per-instance readiness
- Good: rescues compacted installations for every instance started after the cut.
- Bad: changes ADR-0517's store-wide marker into a per-instance check on every
  read; needs the first-event position of each instance, which is not indexed
  today.

### Option 3 — Stay unavailable
- Good: no new mechanism, already correct.
- Bad: an upgraded installation never sees its existing large executions in the
  bounded view.

### Option 4 — Legacy fallback from the timeline
- Good: available immediately for small instances.
- Bad: its cost grows with the square of the iteration count (139 MB at 1,000
  iterations, 2.2 GB at 4,000), so it cannot be bounded, and it would be a second
  source of MI history to keep consistent. Rejected.

## Links

- relates to ADR-0517, ADR-0131, ADR-0068, ADR-0481
- [Issue #1481](https://github.com/pblumer/atlas/issues/1481)
