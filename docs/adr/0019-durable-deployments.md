# ADR-0019: Durable deployments via an on-disk sidecar store

- **Status:** Accepted
- **Date:** 2026-07-22
- **Deciders:** Atlas maintainers

## Context and problem statement

The single-binary server (ADR-0011) holds deployed process definitions — the
BPMN XML, its generated diagram, and the metadata the UI shows (process id,
name, version, deploy time) — in an in-memory map on the `api.Server`. Process
*instances*, by contrast, are durable: every transition is an event in the
write-ahead log, materialized into the state store and rebuilt by
`Processor.Recover` on startup (ADR-0001).

This asymmetry is a real defect, not just a missing feature. After a restart:

- Instances come back from the log, but the definitions they reference are gone,
  so the Operations UI shows orphaned rows with no name, no version, and no
  diagram to render (`GET /processes/{key}/xml` returns 404).
- The engine's own `processes` map is empty too, so a recovered *active*
  instance can no longer resolve its definition and cannot advance.
- The server's `nextKey` counter resets to 1, so the next deployment collides
  with the definition keys the surviving instances still point at.

The question: how do we persist deployments so definitions, diagrams, and the
instances that depend on them survive a restart?

## Decision drivers

- **Durable before visible (I2, ADR-0005).** A deploy must not be acknowledged
  until the definition is on disk.
- **Single writer per partition (I3, ADR-0002).** The state store is the
  partition's state, materialized solely from the WAL. Nothing outside the
  processor may write to it.
- **The store is a pure materialization of the log** (`cmd/atlas`): writing
  non-log data into it would break that invariant and recovery reasoning.
- **Operational simplicity for Milestone S.** This is the single-binary track,
  ahead of the Milestone 4 public API. We want the smallest change that fixes
  the defect without pre-committing the eventual command/event design.
- **Keep deploy off the hot path (I1, I5).** Deployment is rare; its cost is
  irrelevant to the processor batch cycle.

## Considered options

1. **Event-source deployments through the WAL.** Make deploy a command; the
   processor emits a `ProcessDeployed` event carrying the XML, applied to a
   deployments column family; `Recover` rebuilds definitions from it.
2. **Persist deployments into the existing state store** in a separate keyspace,
   written directly by the server's run loop.
3. **On-disk sidecar store owned by the server.** Write each deployment as an
   atomically-replaced JSON file under the data directory; reload them on
   startup and re-deploy the compiled definitions into the processor before it
   serves traffic.

## Decision outcome

Chosen option: **"On-disk sidecar store owned by the server" (option 3)**,
because it fixes the defect completely while touching neither the WAL format nor
the processor's hot path, and it keeps the state store a pure materialization of
the log.

The deployments live in `<data-dir>/deployments/<key>.json`, each file holding
the metadata plus the original XML. Writes are atomic (temp file → `fsync` →
rename → `fsync` dir), so a deploy is durable before the HTTP handler returns —
honoring I2 with the same discipline the log uses. On startup, `api.New` loads
every record, recompiles it (compilation is deterministic — ADR-0004), calls
`Processor.Deploy` to repopulate the engine, rebuilds the server's maps, and
restores `nextKey` to one past the highest key so new deployments never collide.

This is deliberately an **interim** mechanism for the single-binary server. When
the Milestone 4 public API lands, deployment becomes a first-class command whose
acceptance is an event in the log (option 1); at that point this sidecar store
is superseded and the on-disk format can be dropped or migrated. The sidecar
directory is owned exclusively by the server's run-loop goroutine, so the
single-writer discipline that governs process state applies to it unchanged.

### Consequences

- **Positive:** Definitions, diagrams, names, and versions survive restart;
  recovered instances resolve and advance; the Operations UI no longer shows
  orphaned rows for a live server; deploy stays off the hot path.
- **Negative / trade-offs accepted:** A second persistence mechanism exists
  alongside the WAL until Milestone 4. Deploy metadata is not event-sourced, so
  it is not part of the log's audit trail yet. Recompiling on startup costs a
  little boot time, bounded by the number of deployments.
- **Follow-ups / risks to watch:** Supersede with event-sourced deployment at
  Milestone 4. A definition whose XML no longer compiles (e.g. after a
  compiler change) would fail startup load — treated as a fatal, actionable
  error rather than silently dropped.

## Pros and cons of the options

### Option 1 — WAL event-sourced deployment
- Good: single source of truth; deployment becomes part of the audit trail;
  the eventual, correct design.
- Bad: needs a new record type carrying a variable-length XML blob, its
  `applyToState` path, a state column family, and deploy routed as a command —
  substantial surface, and it pre-empts the Milestone 4 public-API design
  before that design exists.

### Option 2 — Deployments in the state store
- Good: reuses the open store; no new file format.
- Bad: introduces a second writer to the partition's state (violates I3) or, if
  written out-of-band, breaks the "store is a pure materialization of the WAL"
  invariant that recovery reasoning depends on.

### Option 3 — On-disk sidecar store (chosen)
- Good: minimal, contained; no WAL or hot-path changes; keeps the store pure;
  durable-before-visible via atomic file replace; trivially superseded later.
- Bad: a parallel persistence mechanism until Milestone 4; deploy metadata not
  yet in the log.

## Links

- relates to ADR-0011 (single-binary distribution and web UI)
- relates to ADR-0001 (event sourcing), ADR-0002 (single-writer), ADR-0005
  (durable before visible)
- to be superseded by the Milestone 4 public-API deployment design
