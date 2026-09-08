# ADR-0280: Recovery proves its prefix or refuses to start

- **Status:** Proposed
- **Date:** 2026-09-07
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0131](0131-engine-recovery-checkpoints-and-wal-compaction.md) gave recovery
two things: a checkpoint it can start after instead of replaying from genesis,
and compaction, which deletes the WAL segments a checkpoint covers. The second
one makes the first one load-bearing — after compaction those records exist
nowhere but the checkpoint.

`checkpointSeed` only ever implemented the optimisation. It picks a checkpoint to
skip *reading* past, and it refuses any checkpoint whose applied position is
ahead of the store's, because the store must already hold what the skipped prefix
produced. It never installs a checkpoint's state files.

With an empty or stale store, no checkpoint qualifies. `after` comes back zero,
and recovery falls through to what it takes for a replay from genesis — except
genesis was deleted. An external review reproduced it: run an instance to an open
service task, checkpoint, compact three segments, then recover into a fresh store.
`RecoverFrom` reports success and produces **0 process instances, 1 element
instance, 1 job** where the uncompacted run has 1/1/1. The server comes up with
orphaned records and no error anywhere.

A replay of what remains is indistinguishable from a replay of everything unless
somebody checks where "what remains" begins. Nobody did.

## Decision drivers

- A recovery that cannot be complete must not report success. Missing instances
  look exactly like instances that never existed.
- Where a checkpoint *can* close the gap, the server should start. Refusing when
  the answer is on disk is its own kind of wrong.
- Installing state files means replacing files under a store, which is only
  possible before that store is opened.
- Compaction is off by default (`--compact-wal`), so this is a latent trap rather
  than a live outage — but the fallback it relies on is wrong with a whole log
  too, and only luck makes it harmless there.

## Considered options

1. Let `RecoverFrom` install a checkpoint's state itself.
2. Split it: `RecoverFrom` proves the prefix and refuses, and startup installs a
   checkpoint before the store is opened.
3. Make `checkpointSeed` accept a checkpoint ahead of the store.

## Decision outcome

Chosen option: **option 2**, because the two halves belong at different moments.

**Recovery proves the prefix.** Before replaying, `RecoverFrom` asks the log for
its oldest surviving position and compares it with the highest position already
accounted for — what the store has applied, or what a usable checkpoint stands in
for, whichever reaches further. If the log begins above that, the records in
between are in neither, and recovery fails with a message naming the missing
range and what would close it.

**Startup installs the checkpoint.** Before the store is opened,
`SeedStateFromCheckpoint` gives a data directory with no state store its starting
point from the newest checkpoint that verifies. It is the same installation the
whole-instance restore performs — called, not copied, so the two cannot come to
disagree about what a checkpoint restores to. A directory that already has a
state store is left alone: that store is the newer answer, and replacing it would
discard everything applied since.

Together: the common case starts automatically, and the case nothing can fix
stops loudly.

Option 1 was rejected on mechanics rather than taste. `RecoverFrom` is handed an
open `*state.Store`; installing means replacing the files underneath it, which
Pebble does not permit and which would make the entry point responsible for a
lifecycle it does not own. Option 3 was rejected because it inverts the meaning of
the check: a checkpoint ahead of the store is exactly the case where the store is
*missing* state, and skipping the prefix would leave the gap between them
unapplied — the same hole by a different route.

### Consequences

- **Positive:** the reproduction now refuses, naming the positions that are gone
  and the two ways to fix it. A compacted log with a good checkpoint boots on its
  own.
- **Positive:** `EarliestPosition` gives the log a way to say where it starts,
  which is the fact this whole class of bug turned on and which nothing exposed.
- **Negative / trade-offs accepted:** an installation with a compacted log and no
  usable checkpoint now refuses to start where it used to boot. That is the
  intent, and it is still a behaviour change an operator can be surprised by;
  the message is written for that person.
- **Negative:** `SeedStateFromCheckpoint` lives in `api` because that is where the
  restore path already had it, and `cmd/atlas` already depends on `api`. The
  `checkpoint` package is the better long-term home — it is about checkpoints,
  not HTTP — and moving it is worth doing when something else touches that file.
- **Negative:** the proof costs one extra read at startup: the first record of the
  oldest segment.
- **Follow-ups / risks to watch:** the acceptance criterion asks for the full
  matrix of empty/stale/valid state × valid/corrupt/missing checkpoint ×
  whole/compacted log. The corners that matter are covered; a stale-but-present
  store with a newer checkpoint still recovers by replaying the suffix, which is
  correct only while the prefix survives — worth a test when compaction stops
  being opt-in.

## Pros and cons of the options

### Option 1 — RecoverFrom installs the checkpoint
- Good: one place, no startup wiring.
- Bad: the store is already open by then; the entry point cannot own that.

### Option 2 — prove in recovery, install at startup
- Good: each half sits where it is possible; the loud failure survives even if
  the installation is never reached.
- Bad: two places to understand instead of one.

### Option 3 — accept a checkpoint ahead of the store
- Good: no new check.
- Bad: that is the case where the store is missing state, so it makes the hole
  official rather than closing it.

## Links

- completes [ADR-0131](0131-engine-recovery-checkpoints-and-wal-compaction.md),
  whose restore contract this entry point never implemented
- relates to [ADR-0109](0109-full-instance-snapshot.md) (the restore path whose
  installation is shared here)
- reported as F04 in
  [`docs/planning/audit-2026-09-07-umsetzungsplan.md`](../planning/audit-2026-09-07-umsetzungsplan.md)
