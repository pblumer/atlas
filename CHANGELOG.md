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

### Changed

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
