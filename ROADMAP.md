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
- ✅ **Inclusive gateway** (OR): the split takes every outgoing flow whose FEEL
  condition holds (or the default); the join waits until no token could still
  arrive — no live token upstream and none in flight toward it — then fires once
  for exactly the branches the split took. Correct for reconverging splits with
  pass-through branches (no double fire), and recovery-tested. Cyclic inclusive
  joins still to come (ADR-0033).
- 🚧 **Input/output variable mappings** ([ADR-0068](docs/adr/0068-task-io-variable-mappings.md)):
  generic `zeebe:ioMapping` on job-backed activities, backed by **activity-local
  variable scopes** and scope-chain FEEL resolution — Camunda-faithful semantics
  (input mappings create locals the activity sees; output mappings promote only
  selected, possibly reshaped values to the parent scope; unmapped names resolve
  up the chain). Built on the existing `FlowScopeKey`/`ScopeKey` model and the
  `DecisionInputMapping` compile path: sources compile at deploy (I5), mapping
  results freeze into variable events so replay re-applies rather than
  re-evaluates (I6), and the activity-local scope is dropped via a deterministic
  scope-drop event on completion. Delivered: scope-chain resolution
  (`ResolveVariable`), compiler parsing (`compiler.IOMapping`), the engine apply
  (activation writes locals, completion promotes, then drops), the inline and
  polyglot script workers reading up the chain, and the **Modeler properties-panel
  I/O-mapping editor** (input/output lists on service, script, and user tasks).
  Remaining: extend the scope-chain read to the DMN/REST/clio workers, and reuse
  the local-scope machinery for embedded subprocess scopes.
- 🚧 **Data objects** ([ADR-0053](docs/adr/0053-first-class-data-objects.md)):
  first-class, typed, event-sourced data — not the decoration most engines settle
  for. The foundational slice landed: a modeled `<dataObject>` (its name,
  `isCollection`, and optional `<dataState>`) compiles into the process, and at
  instance creation each is seeded as a distinct `VTDataObject` entity bound to the
  instance scope, carrying its declared initial **data-state**. Its value (a
  `VarJSON`-capable payload, ADR-0037) and every state transition are durable
  events written to a `cfDataObject` current-value family plus a `cfDataObjectSnapshot`
  history family (the ADR-0048 two-write pattern), so the data-state history and
  lineage rebuild identically on replay — recovery-tested. Seeded objects are also
  read over the HTTP API (`GET /api/v1/instances/{key}/data-objects`, name + state +
  typed value). **Data output associations now write them**
  ([ADR-0058](docs/adr/0058-data-output-associations.md)): a
  `<dataOutputAssociation>` on an activity evaluates an `<assignment><from>` FEEL
  expression over the instance's variables when the activity completes and emits a
  `DataObjectStateChanged` — setting the object's value and advancing its data state
  to the one on the target `<dataObjectReference>` (`received → approved`), so the
  state history becomes a real trail; recovery-tested. **Data input associations
  read them back** ([ADR-0059](docs/adr/0059-data-input-associations.md)): a
  `<dataInputAssociation>` on an activity, at activation, reads a source data
  object (bound into the FEEL scope under its name), optionally transforms it with
  an `<assignment><from>` expression, and writes the result into a process variable
  the activity then reads — so an `order` one step wrote flows into the next step's
  FEEL; recovery-tested. Data now flows both ways. **Field-level writes**
  ([ADR-0060](docs/adr/0060-field-level-data-object-writes.md)) let an output
  association target one member of a structured object via its `<assignment><to>`
  (e.g. `name`): the engine reads the object's current JSON, sets that member, and
  writes the merged canonical value back — so a record accrues field by field across
  steps, and writing a member into an unset object creates it. The **Modeler** now
  authors all of this (ADR-0053): a `DataObjectReference` panel (name, data state,
  collection) and an association panel (the FEEL value, the target member/variable),
  with input associations defaulting their target on draw. Next: a lineage view
  folding the `SourcePos` chain, item-definition schema validation, list-index path
  targets, and connector-backed data stores.
- 🔲 Compiler validation: reachability, gateway coverage, scope consistency
- 🔲 Conformance tests against a curated BPMN model set
- 🚧 **Business rule tasks** (DMN via the embedded [temis](https://github.com/pblumer/temis)
  engine, [ADR-0014](docs/adr/0014-dmn-business-rule-tasks-via-temis.md)): the
  element, its behavior, and evaluation through the job path landed as a vertical
  slice. **The single-binary server now executes them end to end**: a project
  bundle-deploy ([ADR-0034](docs/adr/0034-projects-and-artifacts.md)) resolves the
  DMN reference, snapshots and registers the model with the deployed process, and
  an in-process DMN worker evaluates the decision when a token reaches the task —
  so a deployed instance runs to completion instead of parking, and the model
  re-registers from its snapshot on restart. The DMN job type is pinned to a
  reserved global index so one worker serves every process without colliding with
  service-task types. **Input/output variable mappings are now wired**
  ([ADR-0039](docs/adr/0039-dmn-io-variable-mappings.md)): a decision reads its
  inputs from process variables via Zeebe io-mapping inputs
  (`<zeebe:input source="=…" target="…">`, FEEL evaluated over the instance off the
  hot path, overriding constant `<atlas:decisionInput>` values), and its result is
  written back into the `resultVariable` process variable through an output-carrying
  job completion — so a downstream gateway routes on the decision. **A second
  evaluation mode landed** ([ADR-0050](docs/adr/0050-temis-decision-connector.md)):
  a business rule task marked `<atlas:temisConnector connector="…">` is a *central*
  decision, evaluated by a remote temis service through the connector/job path
  (ADR-0036/0041) instead of the embedded library — same authoring and I/O mappings,
  only the evaluation locus differs, and a central decision needs no local model at
  deploy. The `temis` connector trio (registry/client/worker) and the shared
  `dmn.DecisionHandler` core landed, and **the connector worker is now wired into
  the single-binary server run loop**, and **connectors are now operator-managed in
  the Console** ([ADR-0041](docs/adr/0041-connector-management-and-secret-store.md)):
  durable connector instances (`{name, endpoint, credentialsRef, enabled}`) live in
  a sidecar store with CRUD on Organization → Connectors, the endpoint token is a
  reference resolved from `ATLAS_CONNECTOR_<REF>_TOKEN` at runtime (never stored),
  and a change rebuilds the live registry — so a central decision runs against the
  configured temis service without a restart (server end-to-end tested). Environment
  config (`ATLAS_TEMIS_CONNECTORS` + `ATLAS_TEMIS_<NAME>_URL`/`_TOKEN`) still works
  as the base. Wiring the clio/REST workers the same way, health probes, and
  external vendor workers remain ADR-0041 follow-ups.
  **A decision can now be authored in Atlas** ([ADR-0062](docs/adr/0062-embedded-dmn-editor.md)):
  the business rule task panel has "＋ Neue Decision" / "Bearbeiten" buttons that open
  an embedded **dmn-js** editor (vendored, same family as the bpmn-js modeler) — a
  DRD + decision-table authoring surface. On save the model is stored (new reference,
  or overwritten in place when editing) and the decision's inputs and output are
  adopted into the task automatically through the existing picker path, so the
  empty-dropdown round trip (author elsewhere → export → upload → pick) is gone. This
  reverses ADR-0014's "no DMN authoring" non-goal for the decision-table case;
  authoring the FEEL/logic and model versioning still live in temis.
  **Decision binding landed** ([ADR-0063](docs/adr/0063-dmn-decision-binding.md)):
  a business rule task's `zeebe:calledDecision` now honors `bindingType` — `latest`
  (the default, Camunda-style) evaluates the newest deployed version of the
  decision, `deployment` pins to the version snapshotted with the process — surfaced
  as a "Binding" dropdown on the task. `versionTag` (pin to a numbered version) is
  the next step, once models are stored with version history.
  **A decision is now debuggable end to end**
  ([ADR-0066](docs/adr/0066-decision-evaluation-records.md)): the DMN worker
  requests temis's rules-fired **trace** during its off-path evaluation and rides
  the inputs, outputs, and trace back on the job completion (the ADR-0039
  output-carrying completion, widened again), which the processor freezes into a
  durable `VTDecisionEvaluation` history record — append-only, keyed under the
  instance in the ADR-0048 `(scope, ts, pos)` shape, so it rebuilds from the log on
  replay without re-running the decision. Served over
  `GET /api/v1/instances/{key}/decisions` and surfaced in Operations: the live
  viewer badges each decided business rule task and shows the inputs it saw, the
  outputs it produced, and a rules-fired view of the trace — live and long after
  the instance finished (remote ADR-0050 decisions record an empty trace).
  Recovery-tested. Retention/compaction for the new family remains future work.
  Next: explicit `<zeebe:output>` mappings, decimal precision across the temis
  boundary, and off-loop streaming evaluation as the Milestone-4 gRPC job-worker
  concern (the single binary drives jobs synchronously).
- 🚧 **Connectors** ([ADR-0036](docs/adr/0036-clio-connector.md)): a service task
  bearing an `<atlas:clioConnector>` extension is a connector task that appends an
  event to a **server-registered** clio event store through the job path (like the
  DMN worker) — endpoint and credentials live in the server config, the model
  refers to a connector by name. The `clio:write-events` slice (registry, client,
  worker, recovery) landed; each write is idempotency-keyed by the job key so
  at-least-once delivery is safe against clio's append-only log. Wiring the worker
  into the server run loop, a `clio:query` operation, and a WAL→clio event mirror
  are follow-ups. **A service-task connector catalog now makes connector kinds a
  data entry, not a bespoke panel** ([ADR-0067](docs/adr/0067-service-task-connector-catalog.md)):
  the modeler carries an array of kind entries `{id, name, description, icon,
  extension, fields[]}` and renders a searchable "Implement" picker over them —
  approximating the bpmn.io element-templates popup within the buildless panel
  (ADR-0012/0027) — while the compiler keeps discriminating by extension/job type
  and one worker serves each kind; the plain job-worker task, clio, and REST are
  its first three entries, and the next kind is additive at every layer. The same
  ADR gives the **REST connector a model-authored endpoint**: the model carries the
  method, the full URL, and a result variable the JSON response is written into on
  completion (the ADR-0066 output-mapping path), and **the REST worker is wired into
  the run loop** so a REST task is authored and executed end to end. This revises
  ADR-0036's "endpoint by registry name only" rule **for REST alone** (clio stays
  registry-only); credentials are still never authored in a model — an auth type
  plus a server-registered credential reference is a follow-up, alongside
  headers/query maps and FEEL-in-fields.

## Milestone 2 — Events and timers 🚧

Making processes wait, react, and time out.

- ✅ Timer events + due-date index scanning: **intermediate timer catch events**
  (`<timeDuration>` or `<timeDate>`, literal **or a FEEL expression** over the
  instance's variables — e.g. `=orderTimeout`, or FEEL date arithmetic like
  `=deadline + duration("P2D")` read as an exact first-class temporal, ADR-0057)
  execute: the token waits, a server-side scheduler fires due timers on the
  partition goroutine, and the event continues. Recovery-tested (a pending timer
  is restored from the log and fires afterward). A cycle on a plain catch is a
  compile error (a catch fires once). An incident instead of firing immediately
  when a FEEL timer can't resolve is still to come (ADR-0055/0056/0057).
- ✅ **Timer start events** (duration, date, cycle, cron): a `<startEvent>` with a
  `<timerEventDefinition>` starts a fresh instance on its schedule —
  `<timeDuration>` (once, after a delay), `<timeDate>` (once, at an absolute
  instant), or `<timeCycle>` as an ISO-8601 repeating interval (`R3/PT1H`) or a
  5-field **cron** expression (`0 * * * *`, wall-clock-aligned "every full hour").
  A schedule may also be a **constant FEEL** expression (ADR-0056; a start event
  has no instance, so it may not read variables). The schedule is armed as a
  durable timer at deploy, fired by the existing scheduler through the same
  create-instance path an API create uses, and a cycle re-arms its next
  occurrence. A new version supersedes the prior version's schedule. Recovery-
  tested (an armed timer survives a restart and fires; a fired date timer does not
  re-fire).
- ✅ **Message events + subscriptions + correlation** (single-partition):
  intermediate **message catch** events subscribe on a FEEL correlation key and
  wait; intermediate **message throw** events, **message end** events, and an HTTP
  `POST /api/v1/messages` publish, correlating against open subscriptions through
  one shared path and carrying an optional variable payload into the woken
  instance. Recovery-tested (an open subscription is restored from the log and
  correlates afterward). Message buffering, message boundary events, and
  cross-partition correlation still to come (ADR-0020). A **message end event**
  publishes its message, then ends the instance — the send-and-stop counterpart of
  the throw event (ADR-0052).
- ✅ **Message start events** (single-partition): a `<startEvent>` with a
  `messageEventDefinition` is instantiated by a correlating message (throw or API
  publish), seeded with the message payload, so a two-pool request/response runs
  end to end. Matching is by message name; a throw event carries its instance's
  variables as the payload, and the reserved FEEL identifier `processInstanceKey`
  exposes an instance's own key so a reply can correlate back to the requester.
  Recovery-tested. A start-event correlation key and buffering remain (ADR-0035).
- 🔲 Signal events (broadcast)
- 🔲 Error events and error propagation
- ✅ Boundary events: timer and message, interrupting and non-interrupting,
  attached to waiting activities. An interrupting boundary cancels the host (and
  its job) and routes out its flow; a non-interrupting one spawns a parallel
  token. Timer boundaries take a `<timeDuration>`, a `<timeDate>`, or — on a
  non-interrupting boundary — a `<timeCycle>` (interval or cron) that **recurs**,
  a repeating reminder that fires while the host runs (ADR-0054); each field may be
  a literal or a **FEEL expression** over the instance's variables, including a
  FEEL cycle re-evaluated each occurrence (ADR-0055/0056). Recovery-tested
  (ADR-0040, ADR-0054). Error/signal boundaries and boundaries on subprocesses
  remain.
- 🔲 Receive tasks
- 🚧 **Incident model**: a job whose retries a worker exhausts raises a durable
  **incident** on its element instead of hanging or retrying forever; the token
  parks off the activatable index until an operator resolves the incident, which
  re-activates the job with fresh retries (raise / resolve / resume). Keyed by
  element instance, so cancelling an instance clears its incidents. Recovery-
  tested; exposed over HTTP (`POST /jobs/{key}/fail`, `GET /incidents`,
  `POST /incidents/{key}/resolve`) (ADR-0061). Its completion mirror
  `POST /jobs/{key}/complete` lets an operator finish a parked service-task job by
  hand (outputs as `{"variables": …}`) — a synchronous affordance, not the leased
  gRPC worker protocol (still Milestone 4, ADR-0007). **Timer FEEL failures now raise
  incidents too** ([ADR-0064](docs/adr/0064-timer-feel-failure-incidents.md)): a
  catch or boundary timer whose FEEL schedule can't be evaluated parks its token
  and raises a job-less incident (the failing field in its message) instead of
  firing immediately; resolving re-arms the timer against the instance's current
  variables (re-raising if it still fails). Recovery-tested. Recurring-boundary
  re-arm failures, start-event timer FEEL, retry backoff, and an operator UI still
  to come.

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
- 🔲 Embedded subprocesses (scope lifecycle via child counters) — plan in [ADR-0074](docs/adr/0074-embedded-subprocesses.md)
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
- ✅ **Multi-token replay & causal token lineage**
  ([ADR-0065](docs/adr/0065-multi-token-process-replay.md)): element-instance
  events now carry a durable token id, an optional parent token id, and the
  sequence-flow index that activated them — a fork mints a new id per target and
  records the parent, a parallel join retains every arrival and consumes them into
  one continuation, an exclusive gateway never synchronizes. `applyToState` derives
  a per-instance lifecycle history from these facts, and the timeline endpoint folds
  it by log position into **frames** carrying the complete active token set, so a
  replay shows genuinely parallel tokens instead of guessing concurrency from
  timestamps. Recovery rebuilds the same ids, relationships, and frames; old
  payloads stay decodable and the legacy linear `steps` response remains as a
  fallback (supersedes the single-token visualization assumption of ADR-0046).
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
- 🚧 **Projects & artifacts** ([ADR-0034](docs/adr/0034-projects-and-artifacts.md)):
  the Modeler groups work into named **projects** that hold **artifacts**.
  Phases 1–2 landed. Projects are a durable sidecar store
  (create/list/rename/delete); artifacts carry an optional `projectId`, and the
  Modeler home lists each project's artifacts plus an **Ungrouped** bucket, moving
  one between projects from a per-row dropdown. Two artifact types so far:
  **BPMN drafts** (Phase 1) and **DMN references** — a DMN artifact is a *pointer*
  to a temis-authored model (display name + temis handle), never DMN XML, so Atlas
  organizes and deploys the decision without becoming a DMN editor, honoring the
  "no DMN authoring surface" non-goal (Phase 2, ADR-0014). A DMN reference is
  **resolved and validated at deploy time**: a pluggable `dmn.Resolver` (default:
  a `<data-dir>/dmn-models/` folder of temis-exported models; a temis git/service
  source — `dmn.ServiceResolver`, an HTTP model source selected by
  `ATLAS_DMN_RESOLVER_URL` with an optional bearer token — replaces it behind the
  interface) fetches the model XML and the
  embedded temis engine compiles it — the Modeler shows each reference as
  valid / invalid / unresolved, and a project preflight
  (`POST /api/v1/projects/{id}/validate`) gates on all references being valid.
  **Project bundle-deploy** (`POST /api/v1/projects/{id}/deploy`) ties it together:
  it runs the DMN preflight and then deploys every BPMN draft as a runnable
  definition, "validate all then deploy all" so a non-compiling draft or an
  invalid reference refuses the whole bundle before anything is registered; the
  Modeler's per-project **Deploy** button drives it, and a deployed process's
  business rule tasks now **execute** the resolved decision in-process (see
  Milestone 1). A project is a design-time grouping layer only (below the HTTP
  API, no engine impact). The Modeler presents this as a **two-level view**: a
  clean project landscape (one row per project + an *Ungrouped* bucket) and a
  per-project detail page with a single unified artifact table, a **Create new ▾**
  dropdown (BPMN diagram / DMN reference / Form, filing new artifacts into the
  project), a filter, and per-row action menus. Next: further artifact types
  (element templates, READMEs, nested folders), and — later — **importing or
  backing up a whole project from/to a git repository** (a natural fit for the
  same `Resolver`/sidecar seam that already externalizes DMN models).
- 🚧 **User management & the authentication boundary**
  ([ADR-0044](docs/adr/0044-user-management-and-authentication-boundary.md)):
  accounts are a durable sidecar store with an enterprise-ready `User` model — a
  stable opaque id, a **role list** (RBAC-ready, only `admin` enforced today),
  `Disabled` for deactivation, and `Source`/`ExternalID` hooks for external
  identity providers. Passwords are bcrypt-hashed and never leave the server.
  Enforcement is **opt-in** (`--auth` / `WithAuth()`, off by default, mirroring
  `--docs`): with it on, `/api/v1` requires a session (opaque HttpOnly cookie),
  managing users requires `admin`, and a fresh instance seeds an admin from
  `ATLAS_ADMIN_USERNAME`/`ATLAS_ADMIN_PASSWORD` (a generated password is logged
  once — no hardcoded credential). The Console's **Organization** page is now a
  real user-management surface (create/edit/roles/deactivate/delete), with a login
  gate and an account menu. A last-admin lockout guard prevents an instance from
  locking every operator out. **User-task assignment is now bound to this
  identity** ([ADR-0045](docs/adr/0045-user-task-assignment-bound-to-identity.md)):
  claim is authoritative (an empty body claims for the signed-in user; a named
  assignee must be a real, enabled account), the Tasks inbox uses the signed-in
  user as its identity, and an "Assign to…" picker is sourced from a non-admin
  `GET /api/v1/users/assignable`. Next: external identity (OIDC/SAML/LDAP) via the
  `Source`/`ExternalID` hooks, per-endpoint RBAC beyond `admin`, groups, durable
  sessions, multi-tenancy, and audit logging. Under `--auth` the in-process **MCP
  adapter authenticates its loopback calls** with an internal, non-admin service
  token ([ADR-0049](docs/adr/0049-internal-service-auth-for-mcp.md)), so enabling
  auth no longer breaks MCP; the external `/mcp` transport is still unauthenticated
  and should be fronted by a reverse proxy (ADR-0016).
- ✅ **Engine-internal encrypted secret vault**
  ([ADR-0069](docs/adr/0069-engine-internal-encrypted-secret-vault.md),
  [ADR-0070](docs/adr/0070-vault-on-by-default-with-generated-key.md)): closes
  ADR-0041's deferred **A3** so an operator can set a connector credential from the
  Console/API on a single node without provisioning env vars or an external secret
  manager. Secrets are sealed with **AES-256-GCM** (standard-library crypto, no
  CGO) into a sidecar store mirroring the connector store — one record
  `{name, keyId, nonce, ciphertext, …}` per secret, atomic write + fsync — and
  resolved through the same `credentialsRef` indirection: the vault is consulted
  first, falling through to the ADR-0041 A2 environment lookup on a miss. The
  admin-guarded HTTP surface (`GET`/`PUT`/`DELETE /api/v1/secrets`) takes secrets in
  and lists names + metadata but **never hands a value back**. It is entirely a
  side-effect-phase concern: no new value type, no `applyToState` change, and the
  event log/WAL/variables still never see a secret (I6). The vault is **on by
  default with a generated key** (ADR-0070): an operator `ATLAS_VAULT_KEY` /
  `_FILE` wins and is never persisted; absent one, Atlas generates a 32-byte key at
  `<data-dir>/vault.key` (`0600`) and logs how to harden it; `--vault=false`
  disables it entirely — one flag, three postures. Follow-ups: key rotation/re-seal
  tooling via `keyId`, a KMS/envelope (A4) backend, extending the vault to the DMN
  resolver and other `ATLAS_*_TOKEN` references, and a Console Organization →
  Secrets panel over the CRUD endpoints.
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
- ✅ Input/output variable mappings (`zeebe:ioMapping`) — the properties-panel
  editor (input/output lists on service, script, and user tasks) landed with the
  Milestone 1 variable subsystem ([ADR-0068](docs/adr/0068-task-io-variable-mappings.md)).
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
  not ship a DMN **modeler/editor** or decision-management product of its own —
  decisions are authored in temis. (Atlas does offer a **read-only** view of a
  referenced model's decision requirements graph, and a decision picker that
  auto-reads inputs/outputs, so an author can *use* a decision without leaving the
  Modeler; that is a look-and-use surface, not an authoring one.) FEEL is also used
  internally for expressions.

## Guiding constraints

Every milestone must respect the architecture's load-bearing decisions:

- No allocation on the hot path; immutable compiled graphs; value tokens.
- Durable before visible (fsync → commit → side effects).
- Single writer per partition; cross-partition only via async messaging.
- Same `applyToState` live and on recovery.
