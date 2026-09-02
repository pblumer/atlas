# AGENTS.md

Operational guide for AI coding agents working on Atlas. Human contributors: see [`CONTRIBUTING.md`](CONTRIBUTING.md). This file follows the [agents.md](https://agents.md) convention; [`CLAUDE.md`](CLAUDE.md) points here.

> **Read this whole file before writing code.** Atlas's correctness and performance rest on a handful of non-negotiable invariants. A change that looks locally correct can silently break the engine if it violates one of them. The invariants are listed below and in [`docs/architecture/invariants.md`](docs/architecture/invariants.md).

---

## What this project is

Atlas is a durable, high-throughput **BPMN 2.x workflow engine** in Go. It executes business process models by moving tokens through a compiled graph, persisting every state transition as an event in an append-only log, and materializing state in an embedded key-value store.

Three pillars (each has a deep-dive doc):
- **Compiler** ([`docs/architecture/compiler.md`](docs/architecture/compiler.md)) — BPMN XML → immutable, integer-indexed `CompiledProcess`.
- **Processor** ([`docs/architecture/processor.md`](docs/architecture/processor.md)) — single-writer loop that folds commands into durable events.
- **Data model** ([`docs/architecture/data-model.md`](docs/architecture/data-model.md)) — every transition is a keyed record `(ValueType, Intent)`.

Start with [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for the whole picture, then the decision records in [`docs/adr/`](docs/adr/) for *why*.

## The invariants (do not break these)

These are load-bearing. If your task seems to require breaking one, **stop and flag it** — it needs a new ADR, not a workaround. Full explanations in [`docs/architecture/invariants.md`](docs/architecture/invariants.md).

1. **No allocation on the hot path.** The processor batch cycle must not allocate per command. Reuse buffers, pool records, prefer value types and integer indices. (ADR-0010)
2. **Durable before visible.** Ordering is always: append to log → **one** `fsync` → commit state → run side effects. Never expose, return, ack, or notify based on an event that is not yet on disk. (ADR-0005)
3. **Single writer per partition.** One goroutine owns a partition's state. No locks on process state. No partition ever touches another partition's state directly — cross-partition interaction is async message passing only. (ADR-0002, ADR-0006)
4. **One `applyToState`.** State mutation from a record lives in exactly one function, used identically live and on recovery. Never fork or duplicate that logic; recovery correctness depends on it being the same code path. (ADR-0001)
5. **Compile, don't interpret.** Work that can happen at deploy time (XML parsing, validation, string interning, FEEL compilation) must never happen on the runtime hot path. (ADR-0004, ADR-0008)
6. **Events are facts; commands are intentions.** Only events are persisted. Generated keys and timestamps are written *into* events so replay is deterministic. Never regenerate them on replay. (ADR-0001, data-model.md)

## Commands

The single source of truth for how to build, test, and check. Run these from the repo root.

```bash
# Build everything
go build ./...

# Run all tests
go test ./...

# Run tests with the race detector — MANDATORY before considering work done.
# -timeout raises Go's default 10-minute *per-package* limit: the api package
# alone takes 8-11 minutes under -race, so on a slower machine the default fails
# on elapsed time rather than on a defect, and the panic names whichever test
# happened to be running. This is what CI runs, and what `make race` runs.
go test -race -timeout=25m ./...

# Vet and format checks (formatting must produce no output)
go vet ./...
gofmt -l .

# Run a single package's tests
go test ./engine/...

# Run a single test by name
go test ./engine/ -run TestProcessorRecovery -v
```

Browser end-to-end tests for the web UI (the Design-view token simulation) live in
[`e2e/`](e2e/) and run on Playwright + Chromium — see [`e2e/README.md`](e2e/README.md).
They are JS, not Go, so they are a separate CI job and are not part of the Go commands above.

```bash
cd e2e && npm ci && npx playwright install chromium && npm test
```

**Definition of done for any code change:** `go build ./...`, `go test -race -timeout=25m ./...`, `go vet ./...` all pass, and `gofmt -l .` is empty. Do not report a task complete until these are green. A `panic: test timed out` from `api` without `-timeout` is that missing flag, not a finding — re-run it with the flag before you go looking for a cause.

## Repository layout

The Go packages (see [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md#component-map)):

```
compiler/   BPMN XML → CompiledProcess (parse, resolve, intern, expr, validate, linearize)
model/      Record, header, ValueType/Intent, payload encode/decode
engine/     Partition, processor loop, batching, ProcessingContext, element behaviors
state/      State store wrapper, transactions, indexes (column families)
wal/        Write-ahead log: segmented append, group commit, replay
checkpoint/ Recovery checkpoints and WAL compaction (ADR-0131)
expr/       FEEL expression compilation and evaluation
job/        Job store, worker subscription, gRPC streaming protocol
dmn/        DMN registry, resolver, validation, and the business-rule-task worker
connector/  Worker Type implementations — one package per type (ADR-0203)
api/        HTTP API, web UI, command submission and queries
  runloop/    The single-writer boundary: a service reaches shared state only through it
  httpapi/    Response envelope, client IP, request principal — what every handler uses
  token/      Opaque share tokens: minting, and the shape guard that keeps one off a path
  layout/     BPMN diagram auto-layout (ADR-0124/0127)
  collab/     Live collaborative modeling sessions (ADR-0140)
  vault/      Encrypted secret store (ADR-0069/0070)
  sidecar/    Store[T] and the atomic-write + fsync discipline behind every design-time store
  processdoc/ Process documentation (ADR-0143) — the first per-area service (ADR-0147)
  infomodel/  The process information model: a UML class-diagram subset giving a
              BPMN data object's itemSubjectRef a type to resolve against
              (ADR-0230)
mcp/        MCP server over the HTTP API (ADR-0016)
metrics/    Prometheus metrics (ADR-0142)
opensearch/ OpenSearch event exporter (ADR-0114)
cmd/atlas/  The single binary (ADR-0011)
```

**`connector/` holds the Worker Types.** Every capability a model can put on a
service task rides the same seam — a `TypeConnectorTask` compiles to a job carrying a
reserved `compiler.*JobTypeIndex`, and a worker picks it up off the hot path, after
fsync (ADR-0007/0067). Whether that worker runs inside the server process or in one
Atlas supervises is an operator's choice, not the model's (ADR-0164/0168):

```
connector/rest/        HTTP REST outbound (ADR-0067)
connector/mail/        Outbound mail: SMTP, Gmail, Microsoft Graph (ADR-0079/0093)
connector/sharepoint/  SharePoint list items via Graph (ADR-0141)
connector/remedy/      BMC Remedy AR System (ADR-0106)
connector/jira/        Atlassian Jira issues (ADR-0201)
connector/webscrape/   Web scraping (ADR-0118)
connector/clio/        clio event store: read, write, query (ADR-0036)
connector/temis/       temis decision service (ADR-0050)
connector/script/      Polyglot script tasks: PowerShell, Python, JavaScript (ADR-0047)
connector/csvimport/   CSV-to-JSON, and the parser the upload check shares (ADR-0139/0084)
```

Adding a Worker Type is one package here plus one `managedConnectorKind` entry in
[`api/connectorkinds.go`](api/connectorkinds.go) — not edits scattered across the
server.

**Say Worker, not connector, in anything new you write.** [ADR-0203](docs/adr/0203-worker-execution-model.md)
splits the old word into three: a **Worker Type** is a capability (`jira`, `mail`,
`ad`), a **Worker** is one configured target and identity of that type — the name a
task states — and a **Worker Instance** is a process leasing its jobs. Scaling adds
Worker Instances, never a second Worker.

The old spelling stays in the contracts that cannot change without breaking deployed
models: these package paths, the `connector="…"` BPMN attribute, `atlas worker
--connector`, the `ATLAS_*_CONNECTORS` variables and the `/api/v1/connectors` routes
(aliased by `/api/v1/configured-workers`). Renaming the packages is slice 7 of
[the migration](docs/architecture/worker-execution-migration.md); do not start it as
a side effect of another change. Records written before ADR-0203 keep their wording —
they are immutable, and `docs/adr/README.md` carries the mapping.

**A new API area is a service, not more `Server` methods** (ADR-0147). Give it its
own package under `api/`, hold a `*runloop.Loop` and take every other dependency
as an explicit constructor argument, and expose handlers as exported methods the
route table in `api/openapi.go` points at — that table stays whole, so the
route/OpenAPI drift test keeps working. `api/processdoc` is the worked example.
Reaching engine or design-time state goes through the loop and nothing else; that
is the single-writer invariant (I3), and it is the one thing a review of such a
package must check line by line.

**Adding a design-time store** (drafts, projects, forms, workers, releases, …)
is one call to `sidecar.NewStore`: give it a directory, the name its errors carry,
how a record states its key, and — only if it differs from the default — the
listing order and the filename scheme. Do not hand-roll the read/write/list
mechanics; sixteen copies of it is what this replaced. Note that the filename
predicate also guards keys on the way *in*, so a request-supplied key cannot
address a file outside the store. Non-Go trees: [`docs/`](docs/), [`examples/`](examples/), [`e2e/`](e2e/),
[`deploy/`](deploy/), [`scripts/`](scripts/), [`postman/`](postman/) — with one
exception: [`docs/adr/`](docs/adr/) is a Go package, because the decision records
guard their own conventions with tests in the mandatory sweep and assign their own
numbers (`docs/adr/number.go`, `make adr-number`).

## How to approach a task

1. **Locate it on the roadmap.** [`ROADMAP.md`](ROADMAP.md) is organized by milestone. Confirm the task belongs to the current milestone and isn't blocked by an unstarted dependency.
2. **Read the relevant deep-dive(s)** for the package you're touching, plus any ADR they reference.
3. **Check the invariants** above against your plan *before* writing code.
4. **Work test-first (TDD is the default — [ADR-0018](docs/adr/0018-test-driven-development.md)).** Write a failing test that states the intended behavior, watch it fail for the right reason, then write the minimum code to make it pass, then refactor with the test as a safety net. Anything touching persistence or the processor needs a recovery/replay test written up front (process some commands, simulate restart, replay the log, assert state matches). A bug fix starts with a failing regression test. See *Testing conventions* for the narrow, stated exceptions.
5. **Run the full check sequence** (see Commands) until green, including `-race`.
6. **If you changed an architectural decision**, write a new ADR instead of silently diverging — and **do not give it a number**. Copy [`docs/adr/template.md`](docs/adr/template.md) to `docs/adr/draft-<slug>.md`, keep its `# ADR-DRAFT: Title` heading, and add **no** row to [`docs/adr/README.md`](docs/adr/README.md). The number is assigned when the record lands on main, by a workflow that runs `make adr-number` there; picking one on a branch is how two records end up sharing it, and how a record gets renumbered on every merge. Cite the record as `ADR-draft-<slug>` (or link `draft-<slug>.md`) from code and docs — the numbering rewrites those citations along with the file name and adds the index row. `go test ./docs/adr` guards all of it and runs in the normal test sweep. See [`docs/adr/README.md`](docs/adr/README.md#writing-a-record) for the whole flow.

## Testing conventions

- **Test-driven by default (red → green → refactor).** Write the test before the code it covers ([ADR-0018](docs/adr/0018-test-driven-development.md)). The point is that tests describe *intended behavior*, not whatever implementation happens to exist. Cover error and recovery paths as deliberately as happy paths — in an event-sourced engine, that's where defects hide. **Honest exceptions** (say so in the change, don't pretend): purely mechanical edits with no behavioral surface (renames, docs, gofmt, dep bumps), and throwaway design spikes that get re-done test-first before merge.
- **Coverage floor: 95% statements, repository-wide**, checked in CI as one number. It is a floor, not a ceiling, and not a per-change delta gate — we chose a single repo-wide number precisely to avoid coverage theatre (lines executed with no meaningful assertion). If a genuinely unreachable defensive branch isn't worth a contrived test, say so in review rather than gaming it.
- **Recovery tests are first-class.** The core correctness property is "state after replay == state built live." Test it explicitly for anything that emits events.
- **Determinism.** Tests must not depend on wall-clock time or goroutine scheduling. Inject the `Clock`; drive the processor synchronously where possible.
- **Hot-path allocation.** For processor-path changes, consider `testing.AllocsPerRun` / benchmark with `-benchmem` to confirm you haven't introduced per-command allocations.
- **Table-driven tests** in standard Go style.

## Things that will trip you up

- **`applyToState` is special.** It is called both live and on recovery. Side effects (notifications, network, time reads) must *not* live here — only deterministic state mutation. Put side effects in the processor's post-fsync phase.
- **Followup commands vs. events.** Emitting an event mutates state now and is persisted now. Scheduling a followup command defers work to the next batch. Don't confuse them; see `ProcessingContext` in [`processor.md`](docs/architecture/processor.md).
- **Element IDs are integer indices**, not strings, everywhere in engine code. Strings are interned at compile time. Don't reintroduce string handling on the hot path.
- **Keys encode the partition** in their high bits. Don't invent keys by hand; use the key generator.

## Style

- Standard Go; `gofmt` is non-negotiable.
- Comments explain *why*, not *what*.
- Public APIs get doc comments.
- Small, focused changes over large ones.

## Authoring BPMN models (examples, onboarding, anything deployed)

When you create or edit a BPMN diagram — the `examples/` scenarios, onboarding
flows, anything you deploy through the MCP authoring tools — the model must be
**readable**, not merely valid. A process that compiles but renders as a tangle
of overlapping edges and labels is *not done*. Treat the diagram like the code:
it will be read by humans.

- **Ship hand-authored BPMN-DI.** If you omit `<bpmndi:BPMNDiagram>`, Atlas
  auto-generates a layout — it always deploys, but routinely stacks branch
  edges on top of the main-axis nodes (see the first onboarding cut). For
  anything a human will open, lay it out yourself.
- **One straight main axis.** Keep the happy path on a single horizontal line at
  a constant `y`. Space nodes on an even pitch (≈150px), with the standard sizes:
  100×80 tasks, 50×50 gateways, 36×36 events.
- **Branches get their own lane.** Route an alternate or bypass flow above or
  below the spine with clean orthogonal waypoints — never through the main-axis
  boxes. A join gateway makes parallel/optional paths reconverge tidily.
- **Label the gateway's outgoing flows** and position each label where it
  doesn't collide with an edge or node.
- **Check the render, not just the deploy.** Open the model in the
  Operations/Modeler view (or a rendered preview) and confirm there are no
  overlaps before calling it done.
- **Document the model in the model.** Every BPMN element may carry a
  `<bpmn:documentation>`, and Atlas reads it: the Modeler shows it in the
  Documentation field, the replay shows it beside the selected element, and it
  travels with every deploy, export and version — unlike an explanation you put
  in a chat message or a commit, which the next reader will never see. Write one
  on the process (what it is for, which variables it starts from) and on every
  element whose purpose is not obvious from its name. For an example — a model
  whose whole job is to teach — also say *why it is that kind of element* (why
  this loop is parallel and not sequential), which variables it reads and writes,
  and the trap the reader is about to walk into (`sum()` over an empty list is
  null, not 0; a parallel round must not accumulate). A model that has to be
  explained alongside itself is not finished.

## Pointers

| I need to… | Go to |
|------------|-------|
| Understand the whole system | [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) |
| Know why a decision was made | [`docs/adr/`](docs/adr/) |
| Write a decision record | [`docs/adr/README.md`](docs/adr/README.md#writing-a-record) — copy the template to `draft-<slug>.md`, **do not pick a number** |
| See what to build next | [`ROADMAP.md`](ROADMAP.md) |
| Look up a term | [`docs/architecture/glossary.md`](docs/architecture/glossary.md) |
| Check the rules I must not break | [`docs/architecture/invariants.md`](docs/architecture/invariants.md) |
| Set or overwrite a running instance's variables | `POST /api/v1/instances/{key}/variables` — [ADR-0095](docs/adr/0095-external-variable-modification.md) |
| See who overrode an instance's variables (the audit trail) | `GET /api/v1/instances/{key}/variable-audit` — [ADR-0098](docs/adr/0098-external-variable-modification-audit.md) |
