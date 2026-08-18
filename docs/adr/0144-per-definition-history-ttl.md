# ADR-0144: Per-definition history TTL — retention the model declares

- **Status:** Accepted
- **Date:** 2026-08-18
- **Deciders:** Atlas engine team

## Context and problem statement

Atlas bounds instance data in two places, and neither is where a modeler can reach it.

The **instance TTL** (ADR-0085) bounds the *active* set: an instance that outlives its
`atlas:instanceTtl` is terminated and lands in history. It is per definition — the model
declares it — but it deletes nothing. **History retention** (ADR-0115) bounds the
*finished* set: a sweep hard-deletes finished instances older than `--retention-max-age`
once their events are provably exported. It deletes, but it is one number for the whole
server, chosen by whoever starts the process, and ADR-0115 itself listed "a per-definition
retention override" as an open follow-up.

The gap shows up the moment two kinds of process share an engine. A recurring bulk data
check runs over tens of thousands of records; a few reruns leave tens of thousands of
finished instances that nobody will ever open again. A loan approval in the same install
must stay auditable for years. A single server-wide age cannot serve both: set it short
and the audit trail is gone, set it long and the throwaway runs pile up — and the operator
is left doing manual bulk deletes, which is precisely the housekeeping ADR-0115 set out to
remove.

The knowledge of how long a finished instance is *worth keeping* belongs to whoever models
the process, not to whoever runs the server. It should travel with the model, as the
instance TTL already does.

## Decision drivers

- **Retention belongs to the model.** The value is a property of the process, so it must be
  authored beside the process and deployed with it — versioned like the rest of the model.
- **Reuse the existing mechanism.** ADR-0115's sweep is already bounded, resumable,
  export-gated and replay-safe. A per-definition age must be an input to that sweep, not a
  second deletion path.
- **No new scan.** The ADR-0085 rule holds: nothing may become O(instances) on the loop.
- **Opt-in, no surprise.** Deletion is irreversible. Absent a declaration, behavior must be
  exactly what it is today.
- **Don't overload a setting that already means something.** `instanceTtl` has shipped with
  a defined meaning; silently widening it would delete history behind the back of every
  model that already carries one.

## Considered options

1. **A second per-definition attribute, read by the existing sweep (chosen).**
   `atlas:historyTtl` on `<process>`, compiled like `instanceTtl`; the retention sweep
   resolves each candidate's max age from its own definition, falling back to the
   server-wide setting.
2. **Extend `instanceTtl` to also mean "purge this long after finishing."** One field, two
   effects. Rejected: it changes the meaning of a deployed attribute, and it forces one
   number onto two unrelated questions ("how long may it run" and "how long do we keep the
   record"), which are rarely the same duration.
3. **A per-instance purge timer**, armed on completion at `CompletedAt + TTL`, firing
   `IntentPurging`. Rejected: it puts the export gate — a runtime, non-deterministic
   watermark — inside a timer handler, needs a re-arm loop whenever export lags, and costs
   one durable timer per *finished* instance, exactly in the bulk-run scenario that
   motivates the feature.
4. **A runtime retention policy per process id** (an API-set value, no redeploy).
   Rejected for now: a second, mutable source of truth beside the model, with its own
   durability and recovery story. The compiled attribute can be layered under one later.

## Decision outcome

Chosen: **option 1.** A process definition may declare `atlas:historyTtl` — an ISO-8601
duration on `<process>`, parsed at deploy time to nanoseconds and carried on
`CompiledProcess` (`HistoryTtlNanos()`), exactly as `instanceTtl` is (ADR-0085). A
malformed or non-positive value fails the deploy rather than silently leaving history
unbounded.

- **The sweep resolves the age per instance.** `sweepRetention` (ADR-0115) reads each
  candidate's `ProcessDefKey`, looks the definition up in the deployment registry, and uses
  its `historyTtl` when it declares one, otherwise the server-wide `--retention-max-age`. A
  zero result means nothing retains that instance and it is skipped. The lookup happens
  inside the sweep's `do()` turn, so the registry is read on the single writer as
  everywhere else (invariant I3), and it costs one map hit per candidate — no new scan.
- **Everything else about the delete is unchanged.** The same durable `IntentPurging` →
  `IntentPurged` pair, the same `applyToState` fold, the same export gate
  (`0 < CompletedPosition ≤ safePosition`), the same bounded, resumable, cursor-driven
  window. The per-definition TTL only decides *when* an instance becomes a candidate.
- **The sweeper always runs.** It used to start only when `--retention-max-age > 0`. A
  definition carrying a TTL can be deployed at any moment, so the standing tick is what
  lets that deploy take effect; a tick with neither a server-wide age nor any declaring
  definition returns after one map walk — no store read, no scan.
- **The drain rate is configurable.** What bounds how fast a backlog of finished
  instances actually disappears is the sweep's cadence and per-tick batch — 1000 per
  minute by default. A bulk run leaving tens of thousands of finished instances is
  precisely the case this ADR serves, and at the defaults such a backlog drains over
  hours, so both are now operator settings: `--retention-interval` / `--retention-batch`
  (`ATLAS_RETENTION_INTERVAL` / `ATLAS_RETENTION_BATCH`), defaulting to the engine's own
  `api.DefaultRetentionInterval` / `api.DefaultRetentionBatch` so the CLI's help text
  cannot drift from the values the server uses. The batch stays a cap for the reason
  ADR-0115 gave it one: the sweep runs on the single-writer loop, so a raised batch buys
  drain rate with loop time.

- **Two orthogonal knobs.** `instanceTtl` bounds how long an instance may *run*;
  `historyTtl` bounds how long its *finished record* is kept. A definition may carry
  either, both, or neither. The Modeler shows them as adjacent fields, and the instance-TTL
  hint now names its sibling — the confusion this ADR grew out of was reading
  "self-cleaning" as "deleted", when a TTL-terminated instance is very much still there.

### Consequences

- **Positive:** retention becomes a modeling decision, versioned and reviewed with the
  process it belongs to; a throwaway bulk check and a long-lived audit trail coexist on one
  server; the operator's server-wide age keeps working as the default for everything that
  declares nothing; no new deletion path, event, or state family — the whole change is one
  compiled attribute and one resolution step inside an existing sweep.
- **Negative / trade-offs accepted:** a model can now cause deletion without an operator
  flag — deliberate (it is the point), bounded by the same export gate, and opt-in per
  definition; the retention sweeper's ticker now exists on every server (one map walk a
  minute when nothing is configured); an *undeployed* definition's finished instances fall
  back to the server-wide age, since the compiled TTL is gone with the deployment; a
  shortened TTL applies to already-finished instances at the next sweep, because the age is
  resolved at sweep time rather than frozen on the instance.
- **Follow-ups / risks to watch:** surfacing a definition's effective retention in
  Operations, so an operator can see why a record is about to vanish; the runtime override
  of option 4; a purge-now action for the impatient case the sweep's drain rate does not
  cover; and the standing ADR-0115 note that purging reclaims the *state store*, not the
  WAL — log space needs `--compact-wal` (ADR-0131).

## Pros and cons of the options

### Option 1 — a second per-definition attribute, read by the existing sweep
- Good: retention travels with the model; reuses the bounded, export-gated, replay-safe
  sweep unchanged; opt-in; leaves `instanceTtl` alone; one map hit per candidate.
- Bad: two TTL fields to explain in the Modeler; a model author can trigger deletion.

### Option 2 — extend `instanceTtl` to purge after completion
- Good: one field, nothing new to learn.
- Bad: silently changes what an already-deployed attribute does; conflates run-length with
  retention; no way to keep a short-running process's history for long.

### Option 3 — a per-instance purge timer
- Good: event-driven, no sweep at all.
- Bad: the export gate is a non-deterministic watermark that does not belong in a timer
  handler; needs a re-arm loop when export lags; one durable timer per finished instance.

### Option 4 — a runtime per-process-id retention policy
- Good: change retention without a redeploy.
- Bad: a second, mutable source of truth beside the model, with its own durability,
  recovery and audit story; can layer on top of the compiled attribute later.

## Links

- extends ADR-0115 (history retention — the sweep, the purge event, the export gate)
- complements ADR-0085 (instance TTL — bounds the active set, per definition)
- relates to ADR-0114 (OpenSearch exporter — the gate's high-water mark)
- relates to ADR-0131 (recovery checkpoints and WAL compaction — reclaiming the log)
