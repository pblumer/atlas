# ADR-0011: Single-binary distribution with an embedded web viewer and editor

- **Status:** Accepted
- **Date:** 2026-07-22
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas is a library-first engine core. To run it today you write Go, wire a
`wal`, a `state.Store`, and an `engine.Processor` together, and deploy processes
programmatically. That is the right shape for the engine, but it means there is
no way to *start a server and use Atlas* — no process to run, no surface to
deploy an XML model to, and no way to look at a model or watch an instance move.

We want a single, self-contained artifact: one binary that, when started, is a
running Atlas server offering everything needed to try the product — deploy a
BPMN model, run an instance, and **see** the model and its live state in a
browser. No external web server, no separate front-end deployment, no database
to provision.

Two earlier documents constrained this: `ROADMAP.md` and `README.md` both listed
"a graphical BPMN modeler" as an explicit non-goal ("Atlas executes models, it
doesn't draw them"). This ADR revisits that specific non-goal — not the broader
principle that the engine core stays a library.

## Decision drivers

- **Operational simplicity.** One artifact to ship, one process to run. A user
  should get a working server from `go run ./cmd/atlas` with no other moving
  parts.
- **Time-to-first-value.** Deploying a model and watching a token move is the
  single most convincing demo of an execution engine, and it is the thing the
  library-only shape cannot show.
- **A live view is our differentiator.** A standalone modeler draws BPMN; it
  cannot show runtime state because it has no engine. Atlas has the engine, so
  overlaying live token/incident state on the diagram is nearly free for us and
  impossible for a pure modeler.
- **Don't rebuild the wheel.** BPMN rendering and modeling are solved by
  `bpmn-js` (bpmn.io), the de-facto standard. Building a renderer/modeler from
  scratch would consume months better spent on the engine.
- **Preserve the invariants.** Nothing in the server may weaken the six
  load-bearing invariants (`docs/architecture/invariants.md`). The HTTP layer is
  concurrent; the partition writer is not.

## Considered options

1. **Library only (status quo).** Keep programmatic deployment; ship no server.
2. **Single binary with embedded HTTP API + embedded web UI (viewer, then
   editor), reusing `bpmn-js` for rendering/modeling.**
3. **Single binary API, but the web UI ships as a separate front-end artifact.**
4. **Build a bespoke in-house BPMN renderer and modeler.**

## Decision outcome

Chosen option: **Option 2 — a single binary that embeds both the HTTP API and a
web UI, with `bpmn-js` reused for the viewer and (later) the editor.**

Concretely:

- A new `api` package hosts an HTTP server that owns one `engine.Processor` and
  serializes all engine access onto that processor's single goroutine
  (invariant I3). Web assets are compiled into the binary with `go:embed`, so the
  binary is genuinely self-contained.
- A new `cmd/atlas` `main` wires `wal` + `state` + `engine` + `api`, recovers on
  startup, and serves.
- The web UI grows in stages: (a) a shell that lists deployments and stats; (b) a
  **read-only viewer** rendering deployed BPMN via `bpmn-js`; (c) a **live
  overlay** of runtime state (waiting tokens, incidents) on the diagram; (d) an
  **editor** by embedding the `bpmn-js` *modeler*, not by writing our own.
- The editor's authoring scope is **gated to what the compiler can execute.** The
  compiler validation from Milestone 1 is the mechanism that tells the user "this
  element can't be executed yet," so the editor never silently produces models
  the engine rejects.

We do **not** build a bespoke renderer/modeler, and the engine core remains a
library that the server merely embeds.

### Consequences

- **Positive:** One artifact, one command to a running server. The most
  compelling demo (deploy → run → watch) becomes possible. Reusing `bpmn-js`
  keeps our BPMN-rendering surface near zero. The live overlay is a genuine
  differentiator no standalone modeler can match.
- **Negative / trade-offs accepted:** Atlas now carries a front-end and an HTTP
  surface — build tooling, CSP, and asset embedding to maintain. We take a
  dependency on `bpmn-js`, whose bpmn.io license carries an attribution/watermark
  requirement that must be honored (and re-checked before any closed-source
  distribution). Deployment durability is still a later-milestone concern: until
  the public API milestone persists deployments, models registered via the server
  live in memory and are lost on restart — the server must say so rather than
  imply durability it doesn't have.
- **Follow-ups / risks to watch:** Pulling the HTTP/API surface forward overlaps
  Milestone 4 ("Public API surface"); keep the two aligned so we don't design the
  API twice. The editor is only as useful as the engine is capable, so its value
  tracks Milestone 1+ element coverage. Verify the `bpmn-js` license terms before
  first release. A polished "workbench" product is explicitly deferred.

## Pros and cons of the options

### Option 1 — Library only
- Good: smallest surface; nothing new to maintain; engine stays pure.
- Bad: there is no way to *use* Atlas without writing Go; no viewer, no demo, no
  path to the single-server story the project now wants.

### Option 2 — Single binary, embedded API + web UI, reuse bpmn-js
- Good: self-contained artifact; fastest time-to-value; live overlay
  differentiator; minimal rendering code of our own.
- Bad: adds a front-end + HTTP surface and a third-party UI dependency with a
  license obligation.

### Option 3 — Single binary API, separate front-end artifact
- Good: cleaner separation of back-end and front-end build pipelines.
- Bad: defeats the whole point — two artifacts to ship and deploy, not the "one
  server starts everything" goal.

### Option 4 — Bespoke renderer and modeler
- Good: no third-party UI dependency or license obligation; full control.
- Bad: months of work reimplementing a solved problem, pulling effort away from
  the engine, which is where Atlas's value actually is.

## Links

- revises the "graphical modeler" non-goal in `ROADMAP.md` and `README.md`
- relates to ADR-0002 (single-writer partition model) and ADR-0005 (durable
  before visible) — the server must not weaken either
- overlaps the "Public API surface" and "Operator tooling" items in Milestone 4
