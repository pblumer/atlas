# Atlas Roadmap

This roadmap describes the intended evolution of Atlas. It is a direction, not a contract — order and scope will shift as the project learns. Milestones are deliberately vertical: each one should produce something that *runs*, not just a layer that sits unused.

Status legend: 🔲 not started · 🚧 in progress · ✅ done

---

## Milestone 0 — Foundations ✅

The skeleton that proves the three pillars fit together end to end.

- ✅ Project layout, module, CI (build, test, lint, vet, race detector)
- ✅ `model`: record header, `ValueType`/`Intent`, hand-written binary codec + round-trip tests
- ✅ `wal`: segmented append-only log, group commit (one fsync per batch), forward iteration
- ✅ `state`: Pebble-backed store, transactions, column-family/index helpers
- ✅ `engine`: single-writer processor loop, batch cycle, `ProcessingContext`
- ✅ `applyToState` used identically live and on recovery; crash-recovery test
- ✅ Minimal `compiler`: BPMN-XML parse → resolve → linearize to `CompiledProcess` (programmatic builder + `Parse`); deeper validation (reachability, gateway coverage) still to come
- ✅ Behaviors: none/start event, end event, sequence flow, **service task**
- ✅ `job`: dedicated `job` package — in-process worker subscription that pulls activatable jobs and feeds completions back (ADR-0007); gRPC streaming transport + leases/retry are Milestone 4
- ✅ **Goal: execute `Start → ServiceTask → End` and recover it across a restart** (deployment is programmatic for now, pending the XML front end)

## Milestone 1 — Core BPMN 🚧

The control-flow basics most real models use.

- ✅ `expr`: FEEL compile-once/eval-many with `inputs` analysis — reused from
  `github.com/pblumer/feel` behind an `expr` boundary ([ADR-0015](docs/adr/0015-reuse-feel-engine.md)).
- ✅ **Script tasks**: evaluate FEEL in-engine (reading input variables) and write
  the result variable, so an instance runs to completion with no external worker.
  Recovery-tested (the result is written into the event and re-applied on replay,
  never re-evaluated).
- 🚧 Process variables: a variable store with **input binding** — instances start
  with variables (`{"variables": {…}}`), script tasks read them, and Operations
  shows them per instance. Variable scopes (local vs. propagated), copy-on-write,
  and output mappings still to come.
- ✅ Exclusive gateway (data-based XOR): takes the first outgoing flow whose
  compiled-FEEL condition is true, else the default flow. Recovery-tested (the
  chosen branch is captured by which element activates, never re-evaluated).
- ✅ **Parallel gateway** (AND): forks a token onto every outgoing flow and joins
  by waiting until a token has arrived on each incoming flow, then fires once.
  Synchronization rides the element-instance lifecycle (arrived branches park on
  the join), so it replays deterministically and a half-arrived join survives a
  restart — recovery-tested. Cyclic joins and inclusive (OR) joins still to come
  (ADR-0024).
- 🔲 Inclusive gateway
- 🔲 Input/output variable mappings
- 🔲 Compiler validation: reachability, gateway coverage, scope consistency
- 🔲 Conformance tests against a curated BPMN model set
- 🚧 **Business rule tasks** (DMN via the embedded [temis](https://github.com/pblumer/temis)
  engine, [ADR-0014](docs/adr/0014-dmn-business-rule-tasks-via-temis.md)): the
  element, its behavior, and off-hot-path evaluation through the job path landed
  as a vertical slice. It currently feeds a decision static inputs and surfaces
  outputs via a sink; wiring real input/output variable mappings depends on the
  variable subsystem above.
- 🚧 **Connectors** ([ADR-0026](docs/adr/0026-clio-connector.md)): a service task
  bearing an `<atlas:clioConnector>` extension is a connector task that appends an
  event to a **server-registered** clio event store through the job path (like the
  DMN worker) — endpoint and credentials live in the server config, the model
  refers to a connector by name. The `clio:write-events` slice (registry, client,
  worker, recovery) landed; each write is idempotency-keyed by the job key so
  at-least-once delivery is safe against clio's append-only log. Wiring the worker
  into the server run loop, a `clio:query` operation, and a WAL→clio event mirror
  are follow-ups.

## Milestone 2 — Events and timers 🚧

Making processes wait, react, and time out.

- 🚧 Timer events + due-date index scanning: **intermediate timer catch events**
  with an ISO-8601 **duration** (e.g. PT30S) execute — the token waits, a
  server-side scheduler fires due timers on the partition goroutine, and the
  event continues. Recovery-tested (a pending timer is restored from the log and
  fires afterward). Date/cycle timers, boundary timers, and FEEL duration
  expressions still to come.
- ✅ **Message events + subscriptions + correlation** (single-partition):
  intermediate **message catch** events subscribe on a FEEL correlation key and
  wait; intermediate **message throw** events and an HTTP `POST /api/v1/messages`
  publish, correlating against open subscriptions through one shared path and
  carrying an optional variable payload into the woken instance. Recovery-tested
  (an open subscription is restored from the log and correlates afterward).
  Message buffering, message boundary events, and cross-partition correlation
  still to come (ADR-0020).
- ✅ **Message start events** (single-partition): a `<startEvent>` with a
  `messageEventDefinition` is instantiated by a correlating message (throw or API
  publish), seeded with the message payload, so a two-pool request/response runs
  end to end. Matching is by message name; a throw event carries its instance's
  variables as the payload, and the reserved FEEL identifier `processInstanceKey`
  exposes an instance's own key so a reply can correlate back to the requester.
  Recovery-tested. A start-event correlation key and buffering remain (ADR-0025).
- 🔲 Signal events (broadcast)
- 🔲 Error events and error propagation
- 🔲 Boundary events: interrupting and non-interrupting
- 🔲 Receive tasks
- 🔲 Incident model: raise/resolve, operator resume

## Milestone 3 — Structure 🔲

Composition and reuse.

- ✅ **Collaborations & pools** (participants): a `<collaboration>` deploys one
  runnable definition per pool (each executable `<process>`), keyed and versioned
  independently and reloadable after a restart; a black-box pool (no process) is
  allowed. The pools' runtime link is message correlation (Milestone 2) — a
  message flow is the diagram's depiction of a message catch/throw pair. The
  viewer auto-lays-out DI-less collaborations as stacked pools; the editor
  authors pools, message flows, and pool names (ADR-0023). Atomic multi-pool
  deploy and message-flow validation still to come.
- 🔲 Embedded subprocesses (scope lifecycle via child counters)
- 🔲 Event subprocesses (interrupting and non-interrupting)
- 🔲 Call activities (single-partition)
- 🔲 Multi-instance activities (sequential and parallel)
- 🔲 Compensation and compensation handlers
- 🔲 BPMN transactions (with cancel/compensation)

## Milestone 4 — Operability 🔲

What it takes to run this for real.

- 🔲 Public API surface (deploy, create instance, publish message, complete job, queries)
- 🔲 gRPC job-worker protocol (streaming pull, leases, fencing) — ADR-0007
- 🔲 Worker SDK (Go first)
- 🔲 Metrics (throughput, batch size, fsync latency, queue depth), structured logs, OTel traces
- 🔲 Log compaction / snapshotting so recovery doesn't replay from genesis
- 🔲 Exported-log stream for downstream analytics
- 🔲 Operator tooling: list/inspect instances, incidents, jobs

## Milestone 5 — Scale-out 🔲

Beyond a single node.

- 🔲 Networked inter-partition message transport (ADR-0006)
- 🔲 Cross-partition message correlation and call activities
- 🔲 Multi-node deployment, partition placement
- 🔲 Replication of the WAL for high availability
- 🔲 Partition rebalancing / failover
- 🔲 Idempotency/dedupe for delivered cross-partition messages

## Milestone 6 — Ecosystem 🔲

Adoption and polish.

- 🔲 Worker SDKs in more languages
- 🔲 BPMN modeler interoperability (import from common tools)
- 🔲 Benchmark suite and published performance numbers
- 🔲 Documentation site, tutorials, examples
- 🔲 1.0 API stability commitment

## Milestone S — Single-binary server & web UI 🚧

A parallel track (not strictly sequential with the engine milestones): make Atlas
something you *start*, not only something you *import*. Everything ships in one
self-contained binary. See [ADR-0011](docs/adr/0011-single-binary-distribution-and-web-ui.md).

- ✅ `api` + `cmd/atlas`: single binary embedding the engine over an HTTP surface,
  serving an embedded web UI (`go:embed`). Deploy XML, create instance, stats,
  health, process list/XML, info.
- ✅ **App shell** (ADR-0012): top bar, app switcher, Atlas app naming
  (Console, Modeler, Tasks, Operations, Insights), hash router; Console dashboard
  and Modeler home wired to real engine data.
- ✅ **BPMN editor** (ADR-0013): embedded `bpmn-js` modeler (canvas, palette,
  context pad), a hand-written Details panel, and **Deploy & run** (deploy the
  drawn XML, then start an instance). The panel authors executable tasks — pick a
  task type (script/service) and set a **script task's FEEL expression + result
  variable** or a **service task's job type**, written as the `zeebe:script` /
  `zeebe:taskDefinition` extensions the engine runs (the zeebe moddle is vendored
  alongside bpmn-js). Authoring is gated by the compiler.
- ✅ **Live overlay** of runtime state on the diagram (Operations → a process's
  live view): active elements highlighted with token counts, polled from a
  `/processes/{key}/runtime` endpoint — the differentiator a standalone modeler
  can't offer. Incidents/history overlays still to come.
- ✅ **Instance management** view: Operations lists running process instances
  (process, version, tokens, status) and links each to its live diagram.
- ✅ Auto-layout for deployed models that carry no BPMN-DI: a generated
  left-to-right layered layout is injected when serving XML, so API-deployed
  semantic-only models render in the editor and the live overlay.
- ✅ **MCP server** ([ADR-0016](docs/adr/0016-mcp-server-over-http-api.md)): the
  Model Context Protocol over two transports — an `atlas mcp` subcommand on
  **stdio** for a local client (Claude Desktop, Claude Code), and a **Streamable
  HTTP** endpoint mounted at `/mcp` in `atlas serve` for a remote connector (e.g.
  a claude.ai custom connector). Both proxy tool calls to the Atlas HTTP API, so
  an AI agent can deploy a model, start an instance, and read live runtime state.
  Hand-written, no new dependency; the engine invariants stay behind the HTTP API.
  The `/mcp` endpoint is unauthenticated — front it with a reverse proxy before
  exposing it publicly.
- 🔲 Full properties panel (would vendor a pre-bundled `bpmn-js-properties-panel`).
- ✅ Durable deployments ([ADR-0019](docs/adr/0019-durable-deployments.md)): the
  server persists each deployment (XML + metadata) to an on-disk sidecar store and
  reloads it on startup, re-registering definitions with the processor — so
  diagrams, versions, and recovered instances survive a restart. An interim
  mechanism until the Milestone 4 public API makes deploy a first-class event.
- ✅ Diagram drafts ([ADR-0021](docs/adr/0021-diagram-drafts.md)): a **Save**
  action in the Modeler persists work-in-progress (raw, uncompiled XML) to a
  durable draft store keyed by process id, so incomplete models survive and can be
  reopened — distinct from deploying, which compiles, versions, and runs.
- 🔲 Later: a polished "workbench" experience on top.

---

## Explicit non-goals (for now)

- **A *bespoke* graphical BPMN modeler.** Atlas ships a viewer/editor by embedding
  the standard `bpmn-js` toolkit (see Milestone S / ADR-0011); it does not
  reimplement BPMN rendering or modeling from scratch.
- A batteries-included application server beyond the single-binary server above —
  the engine core stays a library first, with the server embedding it.
- A standalone DMN authoring/product surface. Atlas *executes* the DMN decisions
  a model references, via business rule tasks that delegate to the embedded temis
  engine ([ADR-0014](docs/adr/0014-dmn-business-rule-tasks-via-temis.md)); it does
  not ship a DMN modeler or decision-management product of its own. (FEEL is also
  used internally for expressions.)

## Guiding constraints

Every milestone must respect the architecture's load-bearing decisions:

- No allocation on the hot path; immutable compiled graphs; value tokens.
- Durable before visible (fsync → commit → side effects).
- Single writer per partition; cross-partition only via async messaging.
- Same `applyToState` live and on recovery.
