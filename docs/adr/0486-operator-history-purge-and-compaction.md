# ADR-0486: Deleting finished instances and compacting the WAL on an operator's request

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-25
- **Deciders:** Atlas maintainers

## Context and problem statement

An operator of a development server asked for a way, in the Console, to compress the data
Atlas holds or to delete old data. On a development server much of the history interests
nobody: a check that runs a temporary process over a hundred thousand records leaves a
hundred thousand finished instances behind, and once the result is read, they can go. A
business process is different — its history belongs in OpenSearch, exported and still
findable from Atlas (ADR-0114, ADR-0247).

The installation that asked held, on the day it asked, 6.55 million finished instances
(5.34 million of one bulk-test process), a WAL of 90.5 GiB in 1,449 segments and a state
store of 23.3 GiB. Atlas already has every mechanism the request needs, but none an
operator can reach at that moment:

- **History retention** (ADR-0115, ADR-0144, ADR-0146) hard-deletes finished instances
  through a durable `IntentPurged` event, behind an export gate. It runs on a policy: a
  definition's `atlas:historyTtl`, frozen onto an instance when it finishes, or the
  server-wide `--retention-max-age`. An instance that finished before its definition
  declared a TTL has no entry in the due-date index, so only the server-wide age reaches
  it — through a key-order scan that, at the default 1,000 records a minute, takes about
  four and a half days to walk 6.5 million records, and then applies to every process
  alike. Three records name "a purge-now action for the impatient case" as an open
  follow-up.
- **WAL compaction** (ADR-0131) deletes the segments a verified checkpoint and every
  consumer watermark make redundant. It is a start flag, `--compact-wal`, off by default,
  with no environment variable — so a container deployment cannot switch it on without
  editing its command line — and the Console has no control for it at all.

The two are not independent, and that is what an operator is least likely to know:
**deleting history does not give disk back on its own.** A purge is itself an event in the
WAL, so the log grows by one record per deleted instance; the state store shrinks only
once Pebble has compacted the deletions away, and by at most what it holds. The WAL is
the larger share by far, nothing in the Console reads it (only recovery, the exporter and
the whole-instance backup do), and only compaction makes it smaller.

## Decision drivers

- **No new deletion path.** The purge event, its fold and its idempotence are proven; a
  manual purge must enqueue the same command, not a second one (I4, I6).
- **Never delete ahead of export** (ADR-0115). An instance whose events are not yet in
  OpenSearch stays.
- **Cost scales with what is selected, not with the history** (ADR-0085, ADR-0146), and
  the single writer is held only for the bounded part (I3, ADR-0382).
- **A production installation can refuse it**, and has to say so, as for the catalogue
  purge (ADR-0469): a development server needs no configuration.
- **The operator who must free disk can do it from where they see the disk.**

## Considered options

1. **Tell the operator to configure it.** Set `atlas:historyTtl` on the test processes,
   `--retention-max-age` on the server, `--compact-wal` on the command line, and restart.
2. **A manual purge per definition and cut-off date, plus compaction on request from the
   Console** (chosen).
3. **A runtime retention policy** per process id, set in the Console, read by the sweep.
4. **Delete the history straight out of the state store**, or truncate the WAL, outside
   the event log.

## Decision outcome

Chosen option: **2**, because it reaches the records that no policy can reach any more,
uses only the deletion path and the compaction pass Atlas already has, and puts both where
the disk is shown.

### Deleting finished instances

`GET /api/v1/processes/{key}/history-purge` answers what a purge of one deployed version
would remove: how many of its instances finished before the cut-off `?before=` (RFC 3339
or unix nanoseconds, default now), how many are still running and stay, which gate the
purge waits on, whether this server allows it, and what `confirm` must repeat. It counts
the `piDoneByDef` index entries of that range, which carry no value — so a preview over
five million instances reads five million keys and decodes nothing.

`POST` on the same path deletes one bounded batch of them, newest first, and answers
`purged`, `held` (passed over by the export gate), `remaining` and a `nextCursor` to pass
as `?after=` on the next call. The selection walks the same index from the cursor on a
read view, off the run loop; only the purge commands go onto it, at most `?limit=` of them
(default 1,000, at most 5,000 — the retention sweep's batch). Each call is one run-loop
turn of bounded length, the loop stays free between calls, and the Console repeats the
call until `remaining` is false. A walk passes over at most ten times the limit before it
answers, so a run of held instances cannot turn one call into a scan of the history.

- **The gate.** With the exporter on, an instance goes only when `0 < CompletedPosition ≤`
  the exporter's high-water mark — ADR-0115's rule, unchanged. With the exporter off, the
  gate is the durable position, and there a record with no `CompletedPosition` is purged
  too: it finished before ADR-0115 recorded the position, it is in the state store, and
  invariant I2 means what the state store holds is in the WAL. ADR-0115 skips such a
  record because it cannot be proven *exported*; with no exporter there is nothing to
  prove. The automatic sweep keeps its rule — changing what it deletes on upgrade is a
  decision of its own.
- **Only finished instances.** A running instance is never touched; the existing bulk
  termination ends it first.
- **Who and where.** Administrators only. Refused with 403 on a server started with
  `--instance-purge=false` (`ATLAS_INSTANCE_PURGE=false`), which a production installation
  should be; the preview answers either way and says so in `enabled`. Refused with 400
  unless `?confirm=` repeats the process id. Refused with 507 like every other write while
  the disk is below its reserve (ADR-0481) — a purge adds to the WAL before it frees
  anything. A server that allows it says so with a WARN at every start, as the catalogue
  purge does. Every batch is logged with the principal, the definition and the count
  (`history.purged`).
- **What stays.** The per-definition finished count and the heatmap's cumulative counters
  are monotonic by design (ADR-0080, ADR-0083): the Operations overview still reads the
  number of instances that ever finished. The WAL keeps every event until compaction
  removes the prefix, and OpenSearch keeps what it was sent.

### Compacting on request

`POST /api/v1/checkpoints?compact=true` runs the checkpoint pass with compaction on that
one pass, whether or not the server was started with `--compact-wal`. It is the same pass
on the same goroutine as the cadence, with every gate the scheduled one has: the newest
fully verified checkpoint at or below the store, the exporter and retention watermarks,
and a whole-instance backup in flight. The flag decides whether compaction runs *on its
own*; an administrator who asks, after a confirmation that says it is irreversible, has
made the choice the flag exists to leave to a person. The pass reports `compaction` —
whether it compacted — beside `segmentsRemoved`.

That makes the control already allowed on a frozen server (ADR-0481) the way out of it
without a restart: before, a server frozen without `--compact-wal` could give WAL space
back only after it had been restarted with the flag.

`--compact-wal` also reads `ATLAS_COMPACT_WAL`, so a container switches it on in its
environment, where its other settings are.

`GET /api/v1/checkpoints?verify=false` answers the configuration, the last pass, the WAL's
size and the published checkpoints from their manifests, without re-hashing their state
files. The full read verifies every checkpoint, which on a 23 GiB store reads tens of GiB;
the dashboard asks for the cheap one, and `verified` is absent from its rows because
nothing was checked.

### The Console

The storage card on the dashboard says whether compaction runs, what the last pass did,
and offers *Checkpoint now* and, when compaction is off, *Compact once*, with a
confirmation. A process row in Operations offers *Delete finished instances…*: a version
or all of them, a cut-off date, the preview's counts, the process id typed to confirm, and
progress while the batches run, which can be stopped between two. The dialog says that
the disk is given back by compaction, not by the purge.

### MCP

`atlas_finished_instances_deletion` (the preview) and `atlas_delete_finished_instances`
(one batch, `confirm` required) proxy the two routes, so an agent that ran a test over a
hundred thousand records can clear its runs. They carry every server-side limit the
Console has. The checkpoint control stays off MCP, as ADR-0131 decided.

### Consequences

- **Positive:** an operator can delete the history no policy reaches, by process and date,
  in minutes rather than days, and give the disk back from the same screen; nothing new is
  written to the log and nothing new is folded, so recovery is the recovery ADR-0115
  already tested; the WAL can be compacted from a frozen server without a restart.
- **Negative / trade-offs accepted:** a purge of millions of instances is thousands of
  calls, each holding the writer for one bounded batch; the log grows by one event per
  deleted instance until compaction; the state store shrinks only as Pebble compacts the
  deletions, so the card shows the space late; an administrator can now compact a log on a
  server whose operator chose not to compact automatically, and refusing that would need a
  switch of its own, which this record does not add; two rules for a zero
  `CompletedPosition` exist side by side, the sweep's and the manual one.
- **Follow-ups / risks to watch:** whether the automatic sweep should adopt the manual
  gate for records with no position when no exporter runs; the history of a definition
  that is no longer deployed is reachable by its key through the API but not offered in
  the Console, which lists deployed versions only; the per-purge cost on a store of this
  size was measured on a test store, not on the installation that asked.

## As built

- `api/historypurge.go`: the two handlers, `historyPurgeable` (the gate), the cursor and
  the limits; `WithoutInstancePurge`. The selection walks
  `state.queries.FinishedInstancesOfDefDesc` on a read view; the preview counts with the new
  key-only `CountFinishedOfDefBefore`. Only `Processor.PurgeInstance` and `RunUntilIdle` run
  on the loop.
- `api/checkpointstatus.go`, `api/server.go`: `checkpointRequest` carries `compact` to the
  checkpoint goroutine; `checkpointPass.Compaction`; `?verify=false` and
  `checkpointManifests`. Log events `history.purged`, `server.instance_purge_enabled`,
  `wal_compaction.requested`.
- `cmd/atlas`: `--instance-purge` / `ATLAS_INSTANCE_PURGE`, `ATLAS_COMPACT_WAL`; Helm
  `atlas.instancePurge`.
- Console: `api/web/historypurge.js` (the dialog), `api/web/storage.js` (the reclaim
  section); MCP: `atlas_finished_instances_deletion`, `atlas_delete_finished_instances`.
- Tests: `api/historypurge_test.go` (preview, cut-off, confirmation, switch, batches and
  cursor, export gate, malformed parameters, role, the gate rule, and a replay from the
  log alone after the state store is thrown away), `api/checkpoint_compactonce_test.go`,
  `state/finishedcount_internal_test.go`, `mcp/tools_historypurge_test.go`,
  `cmd/atlas/instancepurge_test.go`, `e2e/history-purge.spec.mjs`.
- Corrected after it shipped: the first build's `?verify=false` still verified every
  checkpoint and only then answered from the manifests, and the storage card waited for
  that answer, so on the 23 GiB store the card did not appear at all. The handler now
  reads one or the other, `TestCheckpointStatusWithoutVerificationReadsNoStateFile` counts
  the verifications a request makes, and the card draws the disk before it asks for the
  checkpoint status.
- Measured on a test store of 6,000 finished instances (not on the installation that
  asked): one purge costs about 190 µs end to end, so a batch of 1,000 holds the writer for
  roughly 0.2 s and one of 5,000 for 1.2 s; one purge event adds 128 bytes to the WAL for a
  short process id, so five million deletions add about 0.6 GiB before compaction. A large
  store reads from disk rather than from cache, and its numbers will be higher.

## Pros and cons of the options

### Option 1 — configuration only
- Good: no code; retention and compaction stay declared, reviewable and automatic.
- Bad: reaches nothing that finished before a TTL existed except through the server-wide
  age, which is days per pass and the same for every process; needs a restart and, for
  compaction, an edit of the command line. It is the right answer for *future* test
  processes (`atlas:historyTtl`), and the dialog says so.

### Option 2 — manual purge and compaction on request (chosen)
- Good: reuses the purge event, the export gate, the compaction pass and its gates; cost
  scales with the selection; reachable where the disk is shown and over MCP.
- Bad: a destructive control in the Console, bounded by role, confirmation and a switch.

### Option 3 — a runtime retention policy
- Good: no redeploy to change retention.
- Bad: a second, mutable source of truth beside the model (ADR-0144 rejected it for that
  reason), and a policy still waits for the sweep's cadence — it answers "keep less from
  now on", not "delete these now".

### Option 4 — out-of-band deletion
- Good: immediate.
- Bad: the state store is a fold of the log; recovery rebuilds what was deleted (ADR-0115
  rejected it), and a WAL rewritten outside compaction breaks the checkpoint contract
  (ADR-0131).

## Links

- extends ADR-0115 (history retention — the purge event and the export gate), ADR-0144 and
  ADR-0146 (the "purge now" follow-up they name)
- extends ADR-0131 (checkpoints and WAL compaction — the pass and its operator surface)
- relates to ADR-0481 (the storage guard — the card, the reserve and the frozen allowlist),
  ADR-0469 (the catalogue purge — the opt-out switch and the confirmation), ADR-0114 and
  ADR-0247 (export, and finding an instance that is gone), ADR-0090 (bounded bulk work on
  the run loop), ADR-0016 (MCP over the HTTP API)
