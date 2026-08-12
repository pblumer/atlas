# ADR-0107: Backup and restore — a one-file download of the design-time data directory

- **Status:** Accepted
- **Date:** 2026-08-10
- **Deciders:** Atlas engine team

## Context and problem statement

An operator wants a dead-simple way to safeguard their work: one click to download
a backup file, and one upload to restore it onto another instance (a fresh box, a
migration, a disaster-recovery copy). "All my models in a file I can keep."

Everything durable in Atlas lives under a single data directory (`--data-dir`):

- `wal/` — the event log, the **source of truth** (ADR-0005). State is a
  materialization of it, rebuilt on restart (ADR-0001/0003).
- `state/` — the Pebble materialization; derivable, never authoritative.
- One directory per **design-time sidecar store** — `deployments/`, `drafts/`,
  `forms/`, `projects/`, `dmnrefs/`, `dmn-models/`, `public-links/`,
  `connectors/`, `marketplace/`, `inbound-subscriptions/` — each a set of atomically
  written JSON files (ADR-0019 and friends).
- `users/` and `vault.key` — user accounts and the secret-vault master key
  (ADR-0044/0070). Secrets.

The question: **what does "a backup" contain, and how does restore apply it
without violating the engine's invariants?**

## Decision drivers

- **Simplicity for the operator.** Download → file; upload → restored. No external
  tooling, no snapshot orchestration.
- **Respect the invariants (ADR-0002/0005).** The single writer owns the WAL and
  state; a backup endpoint must not reach into them mid-write, and restore must not
  swap files under a running engine's open stores.
- **Don't leak secrets.** A backup is a portable file; the vault key and password
  hashes must not ride along by default.
- **Safe to unpack.** A restore consumes an uploaded archive — untrusted input —
  so it must resist path traversal and decompression bombs.

## Considered options

**A. Back up the design-time sidecar subtree as a gzip tar; restore overlays it
(chosen).** `GET /api/v1/backup` streams a `.tar.gz` of an explicit allowlist of
design-time directories. `POST /api/v1/restore` unpacks it back into the data
directory. Runtime (`wal/`, `state/`) and secrets (`users/`, `vault.key`) are
excluded.

**B. Full data-directory snapshot including WAL + state.** Captures running
instances too, but a consistent live snapshot requires a Pebble checkpoint plus a
fsync-bounded WAL copy, and restore cannot apply under a running engine without a
controlled restart. That is a heavier, separate feature ("full snapshot"), not the
"back up my work" ask.

**C. A structured export API (JSON bundle of artifacts).** More portable across
storage-format changes, but duplicates every store's schema and drifts from disk
truth. The on-disk files already *are* the serialization.

## Decision outcome

Chosen option: **A**.

- **Scope is an explicit allowlist** (`backupDirs`), not an "everything except"
  denylist. A sensitive store added later is never swept into a backup by accident
  — it must be added to the list deliberately. `users/` and `vault.key` are out;
  `connectors/` is in (design-time config), with the caveat that its secrets live
  in the vault and so are *not* restored — connectors need their credentials
  re-entered on the target.
- **Backup is a pure read.** The sidecar stores write atomically (temp + rename,
  ADR-0019), so a walk never captures a half-written file, and the endpoint touches
  neither the processor nor the run loop. It never reads `wal/` or `state/`, so the
  single-writer invariant is untouched.
- **Restore overlays files on disk.** The sidecar stores are read-through (each
  request re-reads its file), so restored drafts, projects, forms, decisions, etc.
  are visible immediately — no restart. **Deployments are the exception:** the
  engine compiles and registers them into memory at startup (`loadDeployments`), so
  a restored deployment set takes effect on the **next restart**. The restore
  response says so (`restartRequired: true`). Restore is an overlay (merge), not a
  wipe: it never deletes files the archive doesn't mention.
- **Both endpoints are admin-gated** when auth is on (ADR-0044): a backup is a full
  dump, a restore overwrites design-time state. They are deliberately **not** MCP
  tools — an agent authors and runs models; whole-instance backup/restore is an
  operator action.

### Consequences

- **Positive:** trivial operator UX; no engine changes; invariants untouched;
  secrets stay home; the on-disk files are the format, so nothing to keep in sync.
- **Negative / trade-offs accepted:** running instances (WAL/state) are not in the
  backup; a restore of deployments needs a restart to run; restoring a design-time
  backup onto an instance whose WAL references now-absent deployments can fail at
  the next boot — the file is a design-time backup, not a whole-engine snapshot.
- **Follow-ups / risks to watch:** a future "full snapshot" (option B) can layer on
  via a Pebble checkpoint + WAL copy + restart-time staging; a live deployment
  reload after restore would remove the restart caveat but needs an idempotent
  reload path.

## Safety of restore (untrusted input)

- **Path traversal:** each entry name is cleaned and rejected if absolute or
  escaping the data dir; only entries whose top-level directory is in the allowlist
  are written, so a crafted `state/` or `../` entry is skipped or rejected.
- **Decompression bomb:** the compressed body and the decompressed stream are both
  bounded (`maxRestoreBytes`), and the entry count is capped (`maxRestoreEntries`)
  so an archive of countless tiny members cannot spin the handler.
- **Atomic writes:** each restored file lands via temp + rename, so it appears whole
  or not at all, matching the sidecar stores' discipline.

## Links

- relates to ADR-0019 (durable deployment sidecar store), ADR-0005 (durable-before-
  visible), ADR-0044 (auth/admin), ADR-0070 (vault key), ADR-0016 (MCP is a pure
  adapter — why these endpoints carry no tool)
