# ADR-0131: Engine recovery checkpoints and WAL compaction

- **Status:** Accepted
- **Date:** 2026-08-17
- **Deciders:** Atlas engine team

## Context and problem statement

Recovery replays the engine WAL **from genesis**. `Processor.Recover` opens the
Pebble state store, reads its durable `LastAppliedPosition`, then calls
`log.Replay` — which iterates **every segment from the beginning** — folding each
event whose position is greater than the applied position back into state, and
scanning the whole log to restore the highest position (`p.position`) and the
partition key counter (`p.keygen.counter`).

Two costs follow, both measured on the v0.2.0 benchmark baseline (programme B):

- **Recovery is O(total log), not O(un-applied suffix).** Even though only events
  past `LastAppliedPosition` are *applied*, the whole WAL is *read and decoded* to
  find `maxPos`/`maxCounter`. A completed instance recovers in ~44 µs (~22,700/sec),
  so a log of millions of events is seconds-to-minutes of startup replay that grows
  without bound.
- **No WAL segment can ever be deleted.** Because recovery starts at genesis, every
  segment is load-bearing forever, so disk grows monotonically with history.

The state store is *already* a materialized view of applied state, but it is
committed **`pebble.NoSync`** (ADR-0005): on a crash it may **trail** the WAL, which
is exactly why the WAL — fsynced once per batch — is the source of truth and recovery
replays it. So the store cannot, by itself, be treated as a durable recovery
checkpoint at a known position: we do not know *which* position its on-disk state is
consistent to after a crash, and Pebble may have lost recent un-flushed commits.

We want **bounded recovery time** and **bounded disk** for a single node: recovery
that replays only a recent suffix, and old WAL segments that become deletable — the
last open Milestone-4 reliability item.

This is architecture-sensitive: getting the checkpoint's consistency boundary or its
publication atomicity wrong corrupts recovery silently. Per the repository rule, it
starts with this ADR. **This ADR is not implemented in one change**; it defines the
design and the first slice delivers only the accepted decision plus the testable
manifest/format primitives (no checkpoint is created and no segment is deleted yet).

### Not the ADR-0109 whole-instance snapshot

ADR-0109 is a **downloadable whole-instance backup**: an operator-triggered copy of
the *entire data directory* (WAL, state, secrets, design-time stores) for disaster
recovery and migration, restored onto a fresh box. It is **not** an engine recovery
checkpoint and must not be reused as one:

- its consistency boundary is "a whole data dir at some wall-clock moment," not "the
  state store exactly at applied log position P";
- it copies the WAL *in full* (its point is to preserve running instances), so it
  neither bounds recovery replay nor makes any segment deletable;
- it is a one-off artifact, not a rotating, automatically-maintained recovery input.

A recovery checkpoint has a different contract (a precise applied position, safe to
replay-after, automatically produced and rotated) and is a different mechanism. The
two share no on-disk format.

## Decision drivers

- **Bounded recovery** — replay proportional to a recent suffix, not to all history.
- **Bounded disk** — old WAL segments must eventually be deletable.
- **Never weaken durability (I2).** A checkpoint is an *optimization*; genesis replay
  must always remain a correct fallback. A missing, incomplete, or corrupt checkpoint
  degrades to full replay, never to wrong state.
- **Single writer (I3).** No new goroutine may touch partition state; the checkpoint's
  consistency boundary must be a run-loop batch boundary.
- **Operational simplicity, CGO-free** (ADR-0010) — reuse Pebble's own machinery, add
  the minimum new format.
- **Consumer safety** — the exported-log watermark (ADR-0114), history retention
  (ADR-0115), and backup/restore (ADR-0107/0109) must never be undercut by segment
  deletion.

## Considered options

Two orthogonal decisions.

**A. Checkpoint format — how the applied state is captured at position P:**

1. **Pebble checkpoint** (`DB.Checkpoint`) — a consistent, hard-linked snapshot of the
   state store directory, plus a small manifest recording P.
2. **Logical state export** — iterate the state store and write a custom, engine-owned
   serialization of every family at P.
3. **Reuse the ADR-0109 whole-instance snapshot** as the checkpoint.

**B. Consistency boundary — when/where P is fixed:**

1. **On the run loop, between batches** — request a checkpoint via the single-writer
   loop; it snapshots at the current `LastAppliedPosition`.
2. **Off the loop, concurrently** — take a Pebble checkpoint from another goroutine
   and read the position afterward.

## Decision outcome

**Format: a Pebble checkpoint of the state store (option A1), plus an engine-owned
manifest.** **Boundary: taken on the run loop at a batch boundary (option B1).**

At a batch boundary the run loop:

1. reads the current `LastAppliedPosition` P and the live `p.position` /
   `p.keygen.counter`;
2. **flushes the state store durably to P** — a checkpoint must not inherit the
   `NoSync` trailing property, so the state memtable is flushed and fsynced so the
   checkpoint's files provably contain applied state through P (Pebble's
   `Checkpoint` with `WithFlushedWAL`, or an explicit flush first);
3. writes the Pebble checkpoint into a **temporary** directory
   (`checkpoints/tmp-<seq>/`);
4. writes the **manifest** (below) into that directory, fsyncs the checkpoint files
   and the manifest;
5. **atomically publishes** by renaming the temp directory to
   `checkpoints/<P>/` and fsyncing the parent `checkpoints/` directory, so the
   directory either exists complete or not at all;
6. optionally prunes older checkpoints, keeping at least the newest complete one.

Because it runs on the single writer at a batch boundary, no state mutation races the
snapshot and P is exactly the applied position. Pebble checkpoints are cheap
(hard-linked SSTables), so the loop stall is bounded; the flush is the only real cost
and is amortized by a coarse cadence (time- or event-count-based, well above one per
batch).

**Startup selection and replay.** `Recover` lists `checkpoints/`, picks the
**newest** directory whose manifest is present, well-formed (magic + format version +
self-checksum), matches this partition, and whose checkpoint content matches the
manifest's content checksum. It opens the state store **from that checkpoint** (or
verifies the live store is already at ≥ P) and replays the WAL **strictly after
`AppliedPosition`**, restoring `p.position`/`p.keygen.counter` from the manifest
rather than by scanning the pruned prefix. If no usable checkpoint exists, or the
newest fails validation, it **falls back to the next-older checkpoint, and ultimately
to genesis replay** — always correct, only slower.

**WAL compaction.** A segment is deletable only once it is entirely below a durable
checkpoint's `AppliedPosition` **and** below every required consumer's watermark: the
exported-log high-water mark (ADR-0114) when export is enabled, and the retention
safe position (ADR-0115). Compaction is gated on `min(newest durable checkpoint P,
consumer watermarks)` and is **never** performed in the same change that first
introduces the checkpoint format.

### The manifest

A checkpoint directory carries a `manifest` file — the engine-owned, versioned
descriptor that makes the Pebble snapshot interpretable and verifiable. Fields:

- **format version** — the on-disk manifest/checkpoint version; recovery **ignores a
  checkpoint whose version it does not understand** and falls back (forward-safe,
  pre-1.0 migration policy: no in-place migration, only ignore-and-replay).
- **partition** — the partition the checkpoint belongs to; a mismatch is ignored.
- **applied position** — the highest log position folded into the snapshot; replay
  begins strictly after it. The load-bearing field.
- **key counter** — the partition keygen counter at P, so recovery restores it without
  scanning the pruned prefix.
- **highest position** — the highest log position seen at checkpoint time (≥ applied
  position), restoring `p.position`.
- **state checksum** — a checksum over the checkpoint's state files, verified at
  restore so a torn/corrupt snapshot is rejected (→ fallback).
- **creation metadata** — creation wall-clock time and the Atlas build version, for
  diagnostics and operator visibility (not load-bearing).
- **deployment references** — the `(defKey, version)` set the replayed events assume
  is registered. Deployments are already durable and reloaded independently (ADR-0019)
  *before* replay; the manifest records them so a checkpoint taken against a
  deployment set that no longer resolves is detected rather than mis-replayed.

The manifest is self-describing and self-verifying: a magic prefix, the format
version, the fields above, and a trailing checksum over the whole body so a truncated
or corrupt manifest is caught at decode and the checkpoint is skipped. Its encoding is
a hand-written, deterministic binary codec in the model/wal style (no CGO, ADR-0010),
kept independent of the state-store format so the manifest can be validated without
opening Pebble.

### Consequences

- **Positive:** recovery becomes O(suffix after the newest checkpoint); WAL disk
  becomes boundable; the durability contract is unchanged because genesis replay
  stays a correct fallback and the WAL stays the source of truth.
- **Negative / trade-offs accepted:** a periodic flush+checkpoint stalls the run loop
  briefly (bounded, coarse cadence); more on-disk state formats to version and
  migrate; compaction adds cross-consumer coordination (exporter/retention) that must
  be conservative.
- **Follow-ups / risks to watch:** the exact flush cost and cadence tuning; ensuring
  Pebble's checkpoint durability guarantee is actually leveraged (flush before
  snapshot); a corrupt-newest-but-valid-older selection path that is easy to get
  subtly wrong and needs dedicated crash tests (programme C harness); making
  checkpoint/compaction status operator-visible (Milestone 4 operability).

## Pros and cons of the options

### Format A1 — Pebble checkpoint + manifest (chosen)
- Good: reuses Pebble's consistent-snapshot machinery (hard links, no full copy);
  cheap; the state format is already Pebble's, so no second serialization to maintain.
- Good: the manifest is tiny and format-independent, so it can be validated without
  opening the store.
- Bad: couples the checkpoint to Pebble's on-disk format; a Pebble format bump is a
  checkpoint-format concern (mitigated by the version-and-ignore policy).

### Format A2 — logical state export
- Good: engine-owned, Pebble-independent format.
- Bad: a whole second serialization of every state family to write, version, and keep
  bug-for-bug consistent with `applyToState`; expensive to produce (full scan); more
  code and more ways to diverge from the live materialization.

### Format A3 — reuse ADR-0109 whole-instance snapshot
- Good: it exists.
- Bad: wrong contract entirely — no precise applied position, copies the WAL in full,
  one-off not rotating; would bound neither recovery nor disk. Explicitly rejected by
  the problem statement.

### Boundary B1 — on the run loop, between batches (chosen)
- Good: P is exactly the applied position with no race; single-writer invariant
  intact; trivially correct consistency boundary.
- Bad: briefly stalls the loop (bounded; coarse cadence).

### Boundary B2 — off the loop, concurrently
- Good: no loop stall.
- Bad: the applied position read races ongoing commits, so P is fuzzy; reconciling the
  snapshot with an exact position needs extra coordination that reintroduces
  single-writer complexity for no real gain given Pebble checkpoints are cheap.

## Implementation status

The design is delivered in the focused slices below, in order. **No WAL segment is
deleted until slice 4**, and until slice 3 a checkpoint is written but never read, so
recovery behaves exactly as it did before.

1. **Landed** — this accepted ADR plus the **manifest format primitives**: a
   `checkpoint.Manifest` with a deterministic, versioned, self-checksummed binary
   codec and validation.
2. **Landed** — **create and atomically publish** a checkpoint:
   - `state.Store.Snapshot` flushes the memtable (so the snapshot cannot inherit the
     `NoSync` trailing property) and writes a Pebble checkpoint;
   - `checkpoint.Publish` runs that snapshot into a `tmp-` directory, records a
     content checksum in the manifest, fsyncs the manifest and the directory, and
     **renames** it to its published name before fsyncing the parent — the rename is
     the publication point, so a crash anywhere earlier leaves only an ignorable
     temporary directory;
   - `checkpoint.List` / `Load` / `Verify` enumerate and validate published
     checkpoints (`Verify` re-hashes the state files against the manifest), and
     `Prune` bounds disk by keeping the newest N, never fewer than one;
   - `engine.Processor.Checkpoint` gathers the applied position, highest position, key
     counter, partition, and deployment references **on the single-writer goroutine at
     a batch boundary**, which is what makes the recorded position exact.
3. **Landed** — **restore from the newest usable checkpoint and replay only the
   suffix**:
   - `wal.Log.ReplayFrom` skips whole segment files. Positions increase monotonically
     in append order, so a segment is entirely at or below the cut whenever the *next*
     segment starts one past it; ReplayFrom reads just the first record of each segment
     to learn where it starts. The boundary segment (and the final one, which has no
     successor to bound it) is replayed whole, so filtering the few records at or below
     the cut stays the caller's job.
   - `engine.Processor.RecoverFrom(root)` consults the newest checkpoint that is for
     this partition and whose applied position is **at or below the store's**, replays
     only past it, and seeds the highest log position and key counter from the manifest
     — the values the skipped prefix would otherwise have supplied. `Recover()` is now
     `RecoverFrom("")`, i.e. genesis, so every existing caller is unchanged.
   - A checkpoint *ahead* of the store is refused: this slice skips **reading** a
     prefix, it does not install state files, so the store must already hold what the
     prefix produced. Anything unreadable, foreign, or too new falls back to an older
     checkpoint and ultimately to genesis — always correct, only slower.
   - Only the manifest is read, not the snapshot's state files: its own checksum makes
     the seeded fields trustworthy, and a suffix replay never touches the snapshot.
     Verifying the state checksum belongs to whatever installs those files.
4. Compact eligible WAL segments once a checkpoint is durable and every consumer
   watermark allows it.
5. Expose checkpoint/compaction status and operator controls, and choose the
   production cadence at which the server takes checkpoints.
