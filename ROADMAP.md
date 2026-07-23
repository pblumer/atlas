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
  Message buffering, message start/boundary events, and cross-partition
  correlation still to come (ADR-0020).
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
- 🔲 Full properties panel — the hand-written Details panel grows group by group
  ([ADR-0025](docs/adr/0025-full-properties-panel.md)) rather than vendoring the
  ES-module-only `bpmn-js-properties-panel`. Enumerated in **Milestone A** below.
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

## Milestone A — Modeler & authoring experience 🔲

A parallel track alongside Milestone S: bring the Modeler's *authoring* surface up
to what a real BPMN "Implement" workspace offers, captured feature-by-feature from
a reference modeler and translated into Atlas decisions. Every item respects the
buildless, self-contained UI rule ([ADR-0012](docs/adr/0012-web-ui-app-shell.md))
and the compiler gate ([ADR-0013](docs/adr/0013-embed-bpmn-js-modeler.md)) — the
panel only ever authors what the engine actually runs. The ADRs below are
**Proposed** (feature intentions, not yet decided or built).

**Properties panel** ([ADR-0025](docs/adr/0025-full-properties-panel.md)) — extend
the hand-written Details panel one vertical slice at a time:
- 🔲 General: element id, name.
- 🔲 Documentation: `<bpmn:documentation>` as passthrough (compiler ignores it,
  codec preserves it).
- 🔲 Input/output variable mappings (`zeebe:ioMapping`) — depends on the
  Milestone 1 variable subsystem.
- 🔲 Execution listeners (`zeebe:executionListeners`) mapped to engine hooks.
- 🔲 Extension properties (`zeebe:properties`) — generic name/value pairs, stored
  and round-tripped even when Atlas assigns them no meaning.
- 🔲 Example data — editor-only mock data used by Play mode, never by the runtime.

**Validation & problems** ([ADR-0026](docs/adr/0026-problems-panel-and-versioned-validation.md)):
- 🔲 A `POST /api/v1/validate` dry-run compile (no register, no version, no run)
  returning structured problems (element ref, severity, rule, message).
- 🔲 A Problems panel that calls it debounced on edit, links each problem to its
  element, and shows a version selector ("check problems against Atlas <version>").

**Element & connector templates** ([ADR-0027](docs/adr/0027-element-templates.md)):
- 🔲 Adopt the bpmn.io element-templates JSON schema; a server-served catalog.
- 🔲 "Template → Select" applies a template's bound properties through the
  ADR-0025 write path, rendering only the template's declared fields.

**Human tasks & forms** ([ADR-0028](docs/adr/0028-forms-and-the-tasks-app.md)):
- 🔲 `<bpmn:userTask>` that parks a token and creates an activatable human task
  via the existing job/task lifecycle (ADR-0007).
- 🔲 Form model (adopt the bpmn.io form-js schema) + a server-side form store.
- 🔲 The **Tasks** app (reserved in ADR-0012) as the human "worker": task inbox,
  form rendering, submit-completes-task.
- 🔲 Form binding + a **Test** tab that previews a form against example data.

**Publication** ([ADR-0029](docs/adr/0029-public-process-start-links.md)):
- 🔲 Opt-in, revocable public start links: a scoped, unauthenticated
  `POST /public/forms/{token}/start` bound to one process + start form, reusing the
  single-writer start path. Needs rate limiting / abuse mitigation before shipping.

**Play mode** ([ADR-0030](docs/adr/0030-play-mode-simulation.md)):
- 🔲 An ephemeral engine sandbox: the real compiler + processor over an in-memory,
  non-durable partition seeded from the draft, external effects mocked, driven
  from the Modeler and overlaid with the existing runtime overlay. No JS token
  simulator — identical semantics to production by construction.

**Version history** ([ADR-0031](docs/adr/0031-diagram-version-history.md)):
- 🔲 A **Versions** control: explicit named checkpoints (immutable snapshots)
  beside the overwrite-in-place draft (ADR-0021) — history without autosave spam,
  distinct from deployment versions. Browse, label, restore.

**In-Modeler AI copilot** ([ADR-0032](docs/adr/0032-modeler-ai-copilot.md)):
- 🔲 Extend the MCP/HTTP surface (ADR-0016) with model-authoring tools (return
  XML, validate candidate XML, accept generated XML into a draft).
- 🔲 A copilot panel over a user-configured agent endpoint that drops generated
  models into a reviewable draft — no LLM or provider SDK in the binary, every
  result passes the compiler + Problems gate before deploy.

**Canvas polish** (bpmn-js affordances; mostly no ADR needed, toolkit features):
- 🔲 Minimap, align/distribute, element color/appearance.
- 🔲 Element comments / annotations.
- 🔲 Projects/folders to organize diagrams (draft/deployment listing grows a tree).

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
