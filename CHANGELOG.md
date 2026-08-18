# Changelog

All notable changes to Atlas are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project aims to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

While Atlas is pre-1.0 (`0.y.z`), the public API — the HTTP surface, the MCP
tools, the on-disk WAL/state format, and the Go package layout — is **unstable
and may change in any release**. Breaking changes are called out under
_Changed_ / _Removed_ for each version.

## [Unreleased]

### Added

- **Every element takes a Documentation property**
  ([ADR-0025](docs/adr/0025-full-properties-panel.md)): the Modeler's Details panel now
  offers a **Documentation** field on whatever is selected — every task, gateway, event,
  sequence flow, data object and subprocess, plus the process itself (with nothing
  selected), each pool and the process it executes, and the collaboration as a whole. It
  reads and writes BPMN's own `<bpmn:documentation>` child, beside the element's name and
  id, so the description of *why* a step exists lives on the step rather than in a
  separate document. Documentation is **passthrough** — the compiler ignores it and the
  model preserves it — so documenting a process never changes what it runs: a documented
  model compiles to exactly the graph its undocumented twin compiles to, and the prose
  survives deploy, the served XML (including a server-generated layout) and auto-layout.
  Emptying the field removes the element rather than leaving an empty one behind, and the
  edit joins undo/redo like any other.

- **A runaway loop parks instead of spinning** ([ADR-0133](docs/adr/0133-standard-loop-activities.md)
  amended, reversing its "no hidden ceiling" decision): a standard loop that states no
  `loopMaximum` is bounded only by a FEEL condition, and a condition that is simply
  always true — a typo, an unset variable — repeated forever, spinning the partition's
  single writer for an activity with no external wait. Such a loop now gets **1000**
  runs and then **parks with an incident** on its body: stopped, not finished, so
  nothing downstream runs on a result it never reached, and the incident says how many
  runs happened. Resolving it grants another 1000, counting on from where it stopped
  rather than restarting, so a legitimately long loop can be carried through by hand.
  The ceiling never limits a bound the model states: a loop with `loopMaximum` is
  governed by that number alone, however far past 1000. Alongside it the compiler warns
  (`loop.unbounded`, never an error) about a condition-only loop, naming the ceiling, so
  the bound becomes a decision rather than a backstop discovered at runtime. The count
  survives restarts like any other state; a cyclic sequence flow remains unprotected, a
  known asymmetry noted in the ADR.

- **Retries is a property of every job-backed task**
  ([ADR-0135](docs/adr/0135-retries-as-a-task-property.md)): the retry budget the incident
  model spends (ADR-0061) can now be authored on **every task that creates a job** — the
  job-worker service and send task, each connector task (its own `retries` attribute,
  overriding a `<zeebe:taskDefinition>` on the same element), the polyglot script task
  (`<atlas:jobScript retries>`) and the business rule task. Before this, a script task was
  hard-coded to three attempts and a connector task modeled in the Modeler could not express
  the property at all — the kind picker removes the task definition that used to hold it — and
  the Modeler offered no Retries field anywhere. It now shows one *Failure handling → Retries*
  field on every one of those kinds, carried across a switch of implementation kind, and an
  authored `<atlas:webscrapeConnector retries>` is honoured instead of silently ignored. The
  engine, the log and recovery are untouched: the compiled details already carried the field.

- **Every activity kind can loop** ([ADR-0133](docs/adr/0133-standard-loop-activities.md)
  amended, [ADR-0077](docs/adr/0077-multi-instance-activities.md) amended): business
  rule, manual and undefined tasks now honour both BPMN loop markers, closing the last
  place where a marker drawn on the diagram was silently dropped and the activity ran
  **once**. A looping business rule task re-evaluates its decision per round (one job at
  a time, its result feeding the loop condition); a looping manual or undefined task
  repeats its pass-through, so a routing draft loops before its tasks are implemented.
  The engine needed no change — the multi-instance body/iteration dispatch already runs
  whatever behavior the node has — and the same deploy-time refusals apply (an unbounded
  standard loop, both markers on one activity). In the compiler the loop fields moved
  onto the task shapes, so the node shape gateways share carries none: a gateway still
  cannot parse a loop marker. The Modeler offers the Loop section on these tasks, and
  its "Atlas does not run this marker here" note is now reserved for the genuinely
  non-activity cases.
- **Engine recovery checkpoints & WAL compaction — ADR + manifest primitives**
  (v0.2.0 programme D, [ADR-0131](docs/adr/0131-engine-recovery-checkpoints-and-wal-compaction.md)):
  recovery replays the WAL from genesis, so it is O(total log) and no segment is ever
  deletable. ADR-0131 decides the design — a periodic **Pebble checkpoint of the state
  store at a known applied log position** plus an engine-owned **manifest**, taken on
  the run loop at a batch boundary (single-writer-safe) after a durable flush,
  published atomically (temp dir → fsync → rename → parent fsync); startup picks the
  newest valid checkpoint and replays only the **suffix after its applied position**,
  falling back to an older checkpoint or genesis on any corruption; a segment becomes
  deletable only below both a durable checkpoint and every consumer watermark
  (ADR-0114 exporter, ADR-0115 retention); it is explicitly **not** ADR-0109's
  whole-instance backup. This first slice ships the **testable manifest format
  primitives**: a new `checkpoint` package with a deterministic, versioned,
  self-checksummed binary `Manifest` codec (magic + format version + fields + trailing
  CRC) and validation, with round-trip and corruption/truncation/version tests at 100%
  coverage. No checkpoint is created and **no WAL segment is deleted** — those are the
  later ADR-0131 slices.
- **Standard loop activities** (the ↻ marker, [ADR-0133](docs/adr/0133-standard-loop-activities.md)):
  `<standardLoopCharacteristics>` now runs — an activity repeats while a FEEL
  `loopCondition` holds, one run at a time, with `testBefore` choosing the while form
  (checked before the first run, so it may be skipped) or BPMN's default repeat-until
  (always at least one run), and an optional `loopMaximum` as a hard cap. Until now the
  marker was silently dropped at parse: the activity ran **once** while the diagram
  showed ↻. It runs on the existing multi-instance body/iteration machinery
  ([ADR-0077](docs/adr/0077-multi-instance-activities.md)) — same scope lifecycle,
  counter and recovery path, no new value type — on every activity kind multi-instance
  already supported. Each run's result stays visible to the next run and to the loop
  condition, and is promoted to the enclosing scope when the loop ends, so a looping
  activity leaves behind what the same activity would leave running once. A loop with
  neither a condition nor a maximum, an invalid maximum, or both loop markers on one
  activity is refused at deploy.
- **Loop authoring in the Modeler, in sync with the icon**: the Implement panel's
  Multi-instance section is now a **Loop** section whose single Mode select covers all
  four states (none, loop, multi-instance parallel, multi-instance sequential). It reads
  and writes the very `loopCharacteristics` element bpmn-js draws the marker from, so
  the property and the icon on the shape can no longer disagree — a marker set from the
  context pad reads back as its mode, and choosing a mode redraws the shape. An element
  carrying a loop marker Atlas does not execute now says so in the panel instead of
  leaving the icon to imply behaviour. The Design-view token simulation counts a
  standard loop like a sequential multi-instance, badged ↻ and bounded by the modelled
  `loopMaximum`, and the Operations call-activity list labels a looping call activity
  **loop** rather than **multi-instance**.
- **The WAL stops growing forever — compaction runs in the server** (v0.2.0 programme D,
  [ADR-0131](docs/adr/0131-engine-recovery-checkpoints-and-wal-compaction.md), slice 7):
  `atlas serve --compact-wal` deletes the WAL segments a recovery checkpoint and every
  consumer watermark make redundant, on the same tick that takes the checkpoint. Recovery
  time was bounded in slice 5; the log's disk is bounded now.

  It is **off by default**, unlike checkpointing and for the same reason history retention
  (ADR-0115) is: this is the one step in the feature that destroys data, so an operator
  turns it on deliberately. Everything about the wiring is fail-closed — a consumer
  watermark that cannot be read, a whole-instance snapshot streaming the WAL, or an error
  anywhere skips the pass, because the cost of skipping is disk and the cost of proceeding
  is a segment recovery still needs. The cut itself is unchanged from slice 4: the newest
  **fully verified** checkpoint at or below the store, floored by the exporter's
  high-water mark (ADR-0114) and the retention safe position (ADR-0115).

  Taking a whole-instance backup now holds compaction off for its duration, and raises
  that hold *before* it picks the checkpoint it carries — so a pass that sees no backup is
  one whose deletion the backup's later choice already accounts for. `--compact-wal`
  without checkpointing warns and does nothing; the cut comes from a checkpoint.
- **Whole-instance backup survives a compacted log** (v0.2.0 programme D,
  [ADR-0131](docs/adr/0131-engine-recovery-checkpoints-and-wal-compaction.md), slice 6;
  [ADR-0109](docs/adr/0109-full-instance-snapshot.md) amended): the whole-instance
  snapshot carries `wal/` and not `state/` because `state == replay(WAL)` — which stops
  being true the moment compaction deletes a WAL prefix. An archive taken then would have
  restored an engine **silently missing** every instance whose events were below the cut.

  The snapshot now also carries the **newest fully verified checkpoint** (exactly one,
  picked before the WAL is read so the WAL copy is a superset of the suffix it needs), and
  applying a restore installs it as the state store before recovery replays the rest. A
  published checkpoint is itself a complete Pebble directory, so this is a copy rather
  than a conversion, and the checkpoint is kept so recovery can still seed the highest log
  position and key counter that a deleted prefix no longer supplies. An archive with no
  checkpoint restores exactly as before.

  Two rules keep it safe: a staged restore **always** carries a checkpoint entry — empty
  when the archive had none — so applying it replaces the local checkpoint root and
  nothing from the replaced log survives; and an archive whose checkpoints do not verify
  is **refused** rather than degraded to a plain replay, which would be right for a whole
  log and silently lossy for a compacted one. The cost is archive size: a snapshot now
  grows by roughly the state store, against the 1 GiB restore-upload cap. Still no WAL
  segment is deleted anywhere — that is the last ADR-0131 slice, and this was the last
  consumer standing in its way.
- **Bounded restart time — the server now takes recovery checkpoints** (v0.2.0
  programme D, [ADR-0131](docs/adr/0131-engine-recovery-checkpoints-and-wal-compaction.md),
  slice 5): the mechanism built by the previous slices is switched on. `atlas serve`
  snapshots the applied state every `--checkpoint-interval` (default **5m**, `0`
  disables), keeps `--checkpoint-keep` of them (default **3**), and at startup replays
  only the log past the newest usable one instead of from genesis — so restart time
  follows the cadence rather than the whole log's length. The server publishes into,
  and startup recovery reads from, `<data-dir>/checkpoints`, both resolved through one
  function so they cannot disagree.

  It is on by default because nothing here is deleted: the WAL remains the source of
  truth, and a missing, failed, or corrupt checkpoint only means a full replay. The
  snapshot itself is taken on the run loop, between batches, which is what makes the
  position it records exact (invariant I3); an idle server publishes nothing, and a
  failed pass is logged and retried on the next tick. WAL segments are still **never
  deleted** — feeding compaction the live export/retention watermarks, and the operator
  status and controls, are the remaining ADR-0131 slice.

  One consequence for **whole-instance restores** ([ADR-0109](docs/adr/0109-full-instance-snapshot.md)):
  applying one now also drops `<data-dir>/checkpoints`. A restore replaces the WAL, so
  checkpoints taken against the replaced log describe a log that no longer exists — and
  once the restored log advanced past their position they would look usable. Dropping
  them costs the full replay a restore performs anyway.
- **WAL compaction — old segments finally become deletable** (v0.2.0 programme D,
  [ADR-0131](docs/adr/0131-engine-recovery-checkpoints-and-wal-compaction.md), slice 4):
  the log no longer grows without bound. `wal.Log.Compact` deletes the segments a replay
  would skip, computed by the *same* rule `ReplayFrom` uses, so the deleted set and the
  skipped set cannot drift apart; the segment being written is structurally undeletable.
  `engine.Processor.CompactLog(root, consumerLimits)` gates the cut on the newest
  **fully verified** checkpoint — manifest *and* state files, a stricter check than
  recovery makes, because once the prefix is deleted those files are the only way to
  rebuild it — and on every consumer watermark the caller passes (the exported-log
  high-water mark, ADR-0114, and the retention safe position, ADR-0115). A checkpoint
  that is corrupt, for another partition, or ahead of the store licenses **no** deletion,
  and with no usable checkpoint nothing is deleted at all: the log stays the sole
  recovery source rather than being trimmed on an unverifiable promise. Compaction is an
  optimization like the checkpoint itself — skipping it costs disk, never correctness.
  Nothing wires this into the server yet; the cadence and the operator surface are the
  last ADR-0131 slice.
- **Engine recovery checkpoints — restore and suffix replay** (v0.2.0 programme D,
  [ADR-0131](docs/adr/0131-engine-recovery-checkpoints-and-wal-compaction.md), slice 3):
  recovery can now *use* a checkpoint, which is what turns O(total log) startup into
  O(suffix). `wal.Log.ReplayFrom` skips whole segment files — log positions increase
  monotonically, so a segment lies entirely below the cut whenever the next one starts
  just past it, and only each segment's first record is read to find out.
  `engine.Processor.RecoverFrom(root)` picks the newest checkpoint for this partition
  whose applied position is at or below the store's, replays only past it, and seeds the
  highest log position and key counter from the manifest — the values the skipped prefix
  would otherwise have supplied (without them a restored engine would restart its key
  counter and mint keys that collide with live instances). `Recover()` is now
  `RecoverFrom("")`, so every existing caller keeps replaying from genesis unchanged. A
  checkpoint that is corrupt, for another partition, or *ahead* of the store is refused
  in favour of an older one and ultimately genesis — always correct, only slower, so
  durability (I2) is untouched. Only the manifest is read, never the snapshot's state
  files, since a suffix replay does not touch them. Nothing wires this into the server
  yet and **no WAL segment is deleted**; compaction and the operator surface are the
  remaining ADR-0131 slices.
- **Engine recovery checkpoints — create and atomically publish** (v0.2.0 programme D,
  [ADR-0131](docs/adr/0131-engine-recovery-checkpoints-and-wal-compaction.md), slice 2):
  the engine can now *produce* a recovery checkpoint. `state.Store.Snapshot` flushes the
  memtable — ordinary commits are `pebble.NoSync`, so without the flush a snapshot could
  inherit that trailing property and silently omit applied state — then writes a Pebble
  checkpoint. `checkpoint.Publish` runs that snapshot into a `tmp-` directory, records a
  content checksum in the manifest, fsyncs the manifest and directory, and **renames** it
  into place before fsyncing the parent: the rename is the publication point, so a crash
  at any earlier step leaves only an ignorable temporary directory and the next attempt
  clears it. `checkpoint.List`/`Load`/`Verify` enumerate and validate published
  checkpoints (re-hashing the state files against the manifest), and `Prune` bounds disk
  by keeping the newest N, never fewer than one. `engine.Processor.Checkpoint` gathers the
  applied position, highest position, key counter, partition, and deployment refs **on the
  single-writer goroutine at a batch boundary** — which is what makes the recorded position
  exact — and is purely additive to durability: a failed checkpoint costs a slower
  recovery, never correctness. Nothing reads a checkpoint yet and **no WAL segment is
  deleted**; restore-and-suffix-replay and compaction are the next ADR-0131 slices.
- **Deterministic crash-and-recovery harness** (v0.2.0 programme C): a new
  engine-level test harness (`engine/crash_recovery_test.go`) that turns the
  durability contract into checkable evidence. It runs a workload to a durable point,
  edits the on-disk WAL to model a crash, recovers into a fresh, empty state store,
  and compares the rebuilt state family by family (instances, element instances,
  jobs, timers, incidents, variables, applied position) against a snapshot of the live
  state. Modelling the crash on the WAL's own boundaries (Append buffers a batch;
  one Sync per batch writes and fsyncs it, so a batch's frames land atomically at a
  known offset) lets it drop an un-fsynced batch at a clean boundary with no
  production fault hooks: it asserts that recovering the intact log equals the live
  state (invariant I4), that an un-fsynced / torn / CRC-corrupt trailing batch is
  absent while the acknowledged prefix stays fully consistent, and that restart is
  idempotent. Test-only, so the coverage floor is untouched. Deferred to later
  programme-C increments: in-process phase-boundary crash hooks for the exact
  after-append/after-commit cut points, child-process (SIGKILL) crashes, the
  no-side-effect-before-durability ordering assertion, and richer workloads (timers,
  messages, incidents).
- **Reproducible benchmark harness** (v0.2.0 programme B): a new
  [`benchmarks/`](benchmarks/) package measures the pure engine under the durable
  profile (a real segmented WAL with a group-commit `fsync` per batch and a real
  Pebble state store). It ships idiomatic Go benchmarks for three steady-state
  workloads — a minimal self-completing linear process, a service-task
  create/activate/complete lifecycle, and a mixed variables-plus-gateway routing
  process — reporting `ns/op` (→ instances/sec), `events/op` (from the applied log
  position), `walB/op` (on-disk WAL growth), and `-benchmem` allocations. A
  `summarize.sh` renders the machine-readable raw output as a Markdown table, a CI
  smoke step runs the harness at one iteration each (no performance threshold on PR
  CI), and [`benchmarks/README.md`](benchmarks/README.md) documents the commands,
  metrics, the environment metadata to record, and the standing caveat that results
  are specific to one machine and commit — not a product claim. All harness code
  lives in `_test.go` files, so it adds nothing to the coverage floor. An
  in-memory/no-fsync profile, latency percentiles, and recovery benchmarks are
  deferred to later programme-B slices.
- **End-to-end HTTP benchmark profile** (v0.2.0 programme B): the benchmark harness
  gained an API-layer profile that drives the same durable engine through
  `api.Server`'s HTTP handlers (in-process via `ServeHTTP`, so TCP/client cost is
  excluded). `BenchmarkHTTPLinearCreate` and `BenchmarkHTTPVariableGatewayCreate`
  mirror the shapes of their engine-level twins, so the difference is the API-layer
  overhead — the same `events/op`/`walB/op` with the extra `allocs/op`/`B/op` of
  request decode, routing, the run-loop handoff, and response encode. The existing
  `-bench=.` CI smoke step covers them; still deferred are a loopback-socket (real
  TCP) variant and service-task completion over HTTP.
- **In-memory benchmark profile** (v0.2.0 programme B): RAM-backed (tmpfs) twins of
  the three engine-level workloads (`BenchmarkInMemory…`). The state store already
  commits with `pebble.NoSync`, so the WAL `fsync` is the only durability cost;
  running it on tmpfs makes that `fsync` hit RAM, so comparing an in-memory
  benchmark to its durable twin splits the per-instance cost into engine CPU (what
  remains) and disk-`fsync` latency (the difference — on the CI machine, ~95% of the
  durable time). Same `events/op`/`walB/op`/`allocs/op` as the durable twin. It is a
  measurement profile, not a durability mode; the benchmarks skip when no tmpfs is
  available (`ATLAS_BENCH_TMPFS` overrides the mount). Still test-only, so the
  coverage floor is untouched, and the `-bench=.` CI smoke step covers them.
- **Recovery benchmark profile** (v0.2.0 programme B): the startup/recovery axis —
  how fast a fresh engine rebuilds state by replaying the WAL from genesis (there is
  no checkpoint yet). `BenchmarkRecoveryLinearCompleted` and
  `BenchmarkRecoveryServiceTaskParked` populate a WAL with `b.N` instances (batched
  into few fsyncs so setup stays cheap and is excluded from the timer), then measure
  the replay into a fresh, empty state store. `ns/op` is the per-instance recovery
  cost (so the derived instances/sec is the recovery rate, and recovery-events/sec =
  `events/op` × instances/sec); the two workloads recover completed history and
  parked instances-plus-jobs respectively. Test-only, so the coverage floor is
  untouched; the `-bench=.` CI smoke step covers them.
- **Published benchmark baseline** (v0.2.0 programme B): the first committed,
  reproducible Atlas performance baseline lives in [`benchmarks/results/`](benchmarks/results/)
  — a machine-labelled raw `go test -bench` capture (`baseline-<commit>.txt`, with an
  environment-metadata header) plus a `benchstat`-reduced Markdown summary
  (`baseline-<commit>.md`, median ± 95% CI over 10 repetitions across all four
  profiles: durable engine, HTTP, in-memory, recovery, and latency percentiles). It is
  labelled as illustrative and `fsync`-dominated, captured on a shared, ephemeral VM —
  not a product claim, hardware reference, or cross-engine comparison — and documents
  the exact command to reproduce or refresh it.
- **Latency-percentile benchmark profile** (v0.2.0 programme B): `ns/op` is a mean,
  which the skewed `fsync` latency understates, so `BenchmarkLatencyHTTPLinearCreate`
  and `BenchmarkLatencyEngineLinearSelfCompleting` sample each operation's wall-clock
  latency and report **P50/P95/P99 and max** (computed by nearest-rank on the sorted
  samples). They make the tail visible — on the CI machine the durable HTTP create's
  median is ~2 ms but its max is ~50 ms — and cover both the end-to-end HTTP path and
  the pure engine, so the API-layer tail can be attributed. Run with `-benchtime=Nx`
  for a fixed, meaningful sample count (P99 wants a few thousand); the percentiles
  appear in the raw `-bench` output and via `benchstat`. Test-only, coverage floor
  untouched; the `-bench=.` CI smoke step covers them.
- **Deactivate a deployed process** ([ADR-0119](docs/adr/0119-deactivate-deployed-process.md)):
  a deployed definition can be paused so it stays deployed and keeps its running
  instances, but no longer auto-starts new ones from its timer, message, or signal
  start events — for holding a timer-driven process during a maintenance window, for
  example. Reversible with no redeploy and no lost timers; a recurring timer resumes on
  reactivation. Exposed as `PUT /api/v1/processes/{key}/active` and an `active` flag on
  the process listing, and toggled from the Modeler's Deployed list (an "Inactive" badge
  and an Activate/Deactivate button). The flag persists on the deployment sidecar and is
  re-applied on restart; an explicit operator/API start is not gated.
- **Web-scraping connector** ([ADR-0118](docs/adr/0118-web-scraping-connector.md)):
  a `<serviceTask>` bearing an `<atlas:webscrapeConnector url selector attribute
  resultVariable>` extension fetches a model-authored page and extracts the elements
  matching a CSS selector, writing the values into a process variable as a JSON array.
  Like the REST connector, the URL and selector are authored in the model (each
  literal or a FEEL expression); extraction runs off the hot path in an in-process
  worker under the reserved `WebScrapeJobTypeIndex`. Authorable in the Modeler via the
  service-task connector catalog.

### Fixed

- **An interrupted activity no longer leaves a ghost token in the replay**
  ([ADR-0136](docs/adr/0136-terminated-tokens-in-the-replay.md)): when an interrupting
  boundary event fired, the Operations replay kept drawing a live token on the activity the
  interrupt had torn down — it survived to the last frame, so a `completed` instance appeared
  to still hold a token. The replay's token fold defers an element's consumption until the
  activation it causes appears (so a token does not flicker between two log positions), but a
  *terminated* element takes no outgoing flow, so that deferral never resolved. Termination is
  now recorded as its own fact, distinct from completion, and such a token is retired at once.
  A finished instance ends with no token, as its live counters always said. Instances that
  finished before this change keep their ghost token on intermediate frames; their final frame
  is repaired on read.

- **Attached and armed elements no longer claim a phantom predecessor**
  ([ADR-0136](docs/adr/0136-terminated-tokens-in-the-replay.md)): an armed boundary event, an
  armed event-subprocess trigger and a compensation handler are not entered over a sequence
  flow, but recorded flow index `0` — a real flow — instead of "none". The replay animated
  such a token along an edge that does not exist, and the frame fold could retire an unrelated
  live token by mistaking the arming for that edge's successor.

### Changed

- **Handbook: umlauts in the loop recipe's labels** ([ADR-0133](docs/adr/0133-standard-loop-activities.md)):
  the ↻ recipe shipped ASCII-fied German — "Zaehler starten", "Pruefen", and a process
  named "Pruefen bis in Ordnung" — while every other recipe in the German handbook uses
  umlauts. They now read **"Zähler starten"**, **"Prüfen"** and **"Prüfen bis es passt"**
  (matching the recipe's own heading), both on the rendered card and in the process name
  the deploy reports. Labels only: the recipe's XML structure and its loop
  characteristics are untouched, and it still deploys and runs from the card.

- **A retry budget below 1 is refused at deploy**
  ([ADR-0135](docs/adr/0135-retries-as-a-task-property.md)): `retries="0"` (or a negative
  count) used to deploy and then hang — a job is on the activatable index only while it has
  retries left, so it was never handed to a worker, never failed, and never raised the incident
  an operator could resolve. It is now a compile error naming the element, alongside the
  existing "invalid retries" error, which every task kind now words identically. Use
  `retries="1"` for a single attempt with no retry.

- **Deterministic history-retention tests** (v0.2.0 reliability foundation): the
  retention sweep (ADR-0115) gained two test seams — an injectable clock for its
  eligibility cutoff and an explicit sweep trigger in place of the real ticker. The
  black-box retention tests, which previously raced a wall-clock cadence and had to
  widen a max age to 500ms so a sweep tick would not fire during setup (PR #313), are
  replaced by deterministic ones that share a single fake clock with the engine (so a
  finished instance's `CompletedAt` and the sweep's "now" are exact) and drive each
  sweep through a channel handshake (no `time.Sleep`, no polling). They now assert the
  exact age boundary and an exact one-per-tick drain, honoring the repository rule that
  tests must not depend on wall-clock time or goroutine scheduling (invariant I4,
  AGENTS.md). Production behavior is unchanged — a real ticker and the system clock
  still drive the sweep in the running server.
- **Deterministic OpenSearch-exporter test** (v0.2.0 reliability foundation): the
  exporter loop (ADR-0114) gained a test seam — an injectable tick trigger in place of
  its real ticker, with a completion signal. The exporter test previously fired a 15ms
  export interval and polled `stub.count()` under a 3s deadline with a `time.Sleep`,
  racing the background ticker; it is replaced by one that triggers a single export pass
  and awaits it via a channel handshake, then asserts the instance's events were
  mirrored to the configured index — no wall-clock cadence, no polling, no `time.Sleep`.
  This follows the same seam the history-retention sweep uses and honors the repository
  rule that tests must not depend on wall-clock time or goroutine scheduling (AGENTS.md).
  Production behavior is unchanged — a real ticker still drives the loop in the running
  server.

## [0.1.0] — 2026-08-11

The first tagged release: a **developer preview**. Atlas already runs a broad
slice of BPMN 2.x durably on a single node, but the operability surface a
production deployment needs (a streaming job-worker protocol, metrics/traces,
log compaction, multi-node scale-out) is still ahead on the [roadmap](ROADMAP.md).
Not for production use.

### Added

**Engine core**

- Durable, event-sourced, single-writer processor: every state transition is an
  append-only WAL record made durable with one group-commit `fsync`, then
  materialized into an embedded Pebble state store. One `applyToState` runs
  identically live and on recovery, so replay and live state can never diverge.
- Compile-don't-interpret pipeline: BPMN XML is compiled once at deploy time
  into an immutable, integer-indexed `CompiledProcess` with interned strings and
  pre-compiled FEEL expressions — no XML, string lookups, or map access on the
  hot path.

**BPMN coverage**

- Control flow: none/start and end events, sequence flows, service tasks,
  script tasks (in-engine FEEL and polyglot workers), and exclusive, parallel,
  and inclusive gateways (split and join), all recovery-tested.
- Process variables with input binding, activity-local variable scopes, and
  Camunda-faithful `zeebe:ioMapping` input/output mappings resolved up the scope
  chain.
- First-class, event-sourced **data objects**: typed values with a data-state
  history, data input/output associations, and field-level writes.
- Events and timers: intermediate/boundary/start **timer** events (duration,
  date, cycle, cron, and FEEL expressions), **message** events with
  subscriptions and correlation, **signal** broadcast events, and **error**
  events with structural propagation to the nearest handler.
- **Receive tasks**, and **boundary events** (timer, message, signal;
  interrupting and non-interrupting).
- Structure and reuse: **embedded** and **event subprocesses**, **call
  activities**, **multi-instance** activities (sequential and parallel), and
  **compensation** with compensation handlers.
- **Business rule tasks (DMN)** via the embedded [temis](https://github.com/pblumer/temis)
  engine or a remote temis connector, with I/O mappings, decision binding
  (`latest`/`deployment`), and durable, replayable decision-evaluation records.
- **Collaborations & pools** with message-flow correlation between participants.
- **Incident model**: a job that exhausts its retries parks and raises a durable
  incident an operator can resolve, resume, or complete by hand.

**Connectors**

- A service-task **connector catalog** — a plain job worker, a clio event-store
  writer, a model-authored **REST** connector, and email/SharePoint/Remedy
  connectors — each served by one worker.

**Single-binary server, web UI & tooling**

- `cmd/atlas serve`: one self-contained binary embedding the engine behind an
  HTTP API and a `go:embed`-ed web UI (Console, Modeler, Tasks, Operations,
  Insights) — deploy XML, run instances, work user tasks.
- Embedded **bpmn-js Modeler** with a hand-written properties/"Implement" panel,
  an embedded **dmn-js** decision-table editor, projects & artifacts, diagram
  drafts, and durable deployments that survive a restart.
- **Operations**: live runtime overlay on the diagram (active elements, token
  counts), instance management, and multi-token replay with causal token
  lineage.
- **Forms** and the **Tasks** app for human tasks.
- **User management & auth boundary** (opt-in `--auth`): durable accounts,
  bcrypt passwords, RBAC-ready roles, identity-bound user-task assignment.
- **Encrypted secret vault** (AES-256-GCM, on by default with a generated key)
  for connector credentials, resolved through a `credentialsRef` indirection —
  secrets never touch the WAL, state, or variables.
- **MCP server** (ADR-0016) over stdio (`atlas mcp`) and Streamable HTTP
  (`/mcp`), so an AI agent can deploy a model, start an instance, and read live
  runtime state.
- **Backup & restore** of the design-time state and whole-instance snapshots
  over the HTTP API.
- `atlas version` reports the product version plus the binary's embedded VCS
  build metadata (commit, build time, dirty flag, Go toolchain).

**Deployment**

- A container **`Dockerfile`** and a **Helm chart** (`deploy/helm/atlas`) for
  running the server on Kubernetes, plus the tag-driven release workflow that
  publishes cross-compiled binaries — linux (amd64, arm64, and 32-bit arm/v6 for
  Raspberry Pi), macOS (amd64, arm64), and windows (amd64) — with a
  `SHA256SUMS` file.

### Notes

- **License:** Atlas is released under the **GNU Affero General Public License
  v3.0 only** (`AGPL-3.0-only`).
- Single-node only. Cross-partition messaging, replication, and multi-node
  deployment are on the roadmap (Milestone 5).
- Recovery replays the log from genesis; log compaction / snapshotting is not
  yet implemented (Milestone 4).

[Unreleased]: https://github.com/pblumer/atlas/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/pblumer/atlas/releases/tag/v0.1.0
