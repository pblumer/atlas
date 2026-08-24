# ADR-0109: Whole-instance snapshot — a full backup that includes running instances

- **Status:** Accepted (amended 2026-08-18: the snapshot also carries a recovery checkpoint, so it survives a compacted WAL — option B, layered on as foreseen)
- **Date:** 2026-08-10
- **Deciders:** Atlas engine team

## Context and problem statement

The design-time backup (ADR-0107) captures an operator's *models* — projects, drafts,
deployments, forms, decisions, connectors — but deliberately excludes the runtime: the
WAL and the state store, and the secrets (user accounts, vault key). That is the right
scope for "save my work," but it is not a disaster-recovery or migration tool: it does
not bring back **running process instances**, and the restored instance cannot log in
or decrypt connector secrets.

The ask: a **whole-instance snapshot** — one file that, restored onto a fresh box,
reconstitutes a complete, immediately-usable engine, running instances and all.

The data directory holds:

- `wal/` — the append-only event log, the **source of truth** (ADR-0005); every running
  instance is a fold of it.
- `state/` — the Pebble materialization of the WAL; **derivable**, never authoritative
  (invariant: `state == replay(WAL)`, ADR-0001).
- the design-time sidecar directories (ADR-0107).
- `users/` and `vault.key` — accounts and the vault master key (ADR-0044/0070).

Two questions shape the design: **what to capture consistently while the engine runs**,
and **how to apply a restore** given the engine holds the WAL and state open under the
single-writer invariant (ADR-0002).

## Decision drivers

- **Reuse the recovery path, don't invent one.** The engine already rebuilds state from
  the WAL on every boot; a restore should lean on that, not on a second mechanism.
- **Respect the invariants.** No touching the writer's WAL/state from a handler; no
  partial or torn snapshot; `applyToState` unchanged (ADR-0001/0002/0005).
- **Consistency without pausing the engine.** An admin snapshot should not stall the
  single writer.
- **True DR.** The restored instance should be usable without re-entering secrets — the
  operator asked for completeness (see *Secrets*).

## Considered options

**A. Snapshot WAL + design-time + secrets; restore drops state and replays (chosen).**
The archive carries `wal/`, the design-time dirs, `users/` and `vault.key` — but **not**
`state/`. A restore replaces those and deletes any existing state; on the next boot,
recovery replays the restored WAL and rebuilds state.

**B. Snapshot WAL + a consistent Pebble state checkpoint.** Faster restore (recovery
replays only the tail), but requires a consistent WAL⊇state pairing and a Pebble
`Checkpoint` API on the store. More moving parts for a feature whose restore is already
a rare, restart-gated event.

**C. Live restore (stop the engine in-process, swap files, reopen).** Rejected: swapping
the open WAL and state under a running single-writer partition is fragile; a staged
apply at boot is simpler and safer.

## Decision outcome

Chosen option: **A**, applied via a **staged, restart-gated restore**.

- **State is not in the snapshot.** Because `state == replay(WAL)`, capturing it is
  redundant and would demand a consistent WAL+state pair. Dropping it and replaying on
  restore reuses the most-tested path in the engine and needs **zero** changes to the
  WAL, state store, or processor.
- **Consistency for free.** The WAL's durable prefix is captured by a plain file read:
  recovery already "stops cleanly at the last durable record" (torn-tail tolerant), so a
  live copy yields a valid point-in-time cut without pausing the writer. The WAL is
  streamed **first**, so the design-time/secret dirs captured after it are a superset of
  whatever the WAL cut references (a deployment's sidecar is written before the WAL
  record that starts an instance on it).
- **Restore is staged and applied at boot.** `POST /api/v1/restore/full` unpacks the
  archive into a `.restore-pending/` staging directory and writes a completion marker
  **last**; it responds `restartRequired: true`. On the next start, before the stores are
  opened, `ApplyPendingRestore` moves each staged entry into place, drops `state/`, and
  clears staging. It is idempotent: an entry already moved is absent from staging on a
  re-run, so a crash mid-apply simply re-runs. A marker-less (interrupted) upload is
  discarded, never applied.
- **A full restore refuses a design-time-only archive.** The staging requires a `wal/`
  entry; without it the upload is rejected, so an ADR-0107 backup can't be applied here
  (which would wipe state and running instances while leaving the current WAL in place).
- **Separate endpoints.** `GET /api/v1/backup/full` and `POST /api/v1/restore/full` sit
  beside the ADR-0107 pair, admin-gated, and are **not** MCP tools (an operator action,
  not an agent one).

### Secrets

The operator chose **true DR**: the snapshot **includes** `users/` and `vault.key`, so a
restored instance can log in and decrypt connector secrets immediately. The consequence
is explicit: the snapshot file carries the vault master key and password hashes, so it is
as sensitive as the server itself. (The design-time backup, ADR-0107, still excludes
them — the two features serve different purposes.)

### Consequences

- **Positive:** running instances survive a backup/restore; DR and migration in one file;
  no engine changes; invariants untouched; consistency without pausing the writer.
- **Negative / trade-offs accepted:** restore requires a **restart** (inherent to the
  open single-writer stores); the snapshot file contains secrets; with a checkpoint
  along (see the amendment below) the archive grows by roughly the state store's size,
  which counts against the 1 GiB restore-upload cap.
- **Follow-ups / risks to watch:** a supervisor-driven auto-restart after staging would
  remove the manual restart step.

## Amendment (2026-08-18): the snapshot carries a recovery checkpoint

Option A rests on `state == replay(WAL)`, which holds only while the WAL still begins at
genesis. ADR-0131 makes WAL segments below a recovery checkpoint deletable, and once a
prefix is gone an archive of `wal/` alone would restore a **silently partial** engine:
an instance whose events lived in the deleted prefix would not come back, with nothing
to signal the loss.

So the snapshot now also carries the **newest fully verified checkpoint** (exactly one),
and `ApplyPendingRestore` installs it as the state store before recovery replays the
remaining suffix. This is option B, which this ADR deferred only for want of a checkpoint
API; ADR-0131 built one, and a published checkpoint is itself a complete Pebble directory,
so installing it is a copy rather than a conversion. The checkpoint is selected *before*
the WAL is read, so the WAL copy is always a superset of the suffix it needs.

Two consequences are deliberate:

- **A staging always carries a checkpoint entry**, empty when the archive had none, so
  applying a restore replaces the local checkpoint root unconditionally. Checkpoints of
  the log a restore replaced must never survive it, and settling that at staging time
  keeps the apply idempotent across a crash.
- **An archive whose checkpoints do not verify is refused**, not degraded to a plain
  replay. Replaying is right for a whole log and silently lossy for a compacted one, and
  a restore cannot tell which it holds; refusing to start is the only answer that is
  never wrong.

An archive with no checkpoint restores exactly as before.

## Links

- builds on ADR-0107 (design-time backup/restore) — shares the extraction safety (path
  traversal, allowlist, decompression-bomb and entry-count caps) and the tar walk
- relates to ADR-0005 (durable-before-visible / torn-tail recovery), ADR-0001/0002
  (event sourcing, single writer), ADR-0003 (Pebble state), ADR-0044/0070 (users, vault)
