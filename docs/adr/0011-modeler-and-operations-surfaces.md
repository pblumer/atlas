# ADR-0011: Modeler and operations surfaces

- **Status:** Proposed
- **Date:** 2026-07-03
- **Deciders:** Core team

## Context and problem statement

Atlas so far is a library: it executes BPMN models but deliberately does not
draw them, and it ships no application server. Two entries in [`ROADMAP.md`](../../ROADMAP.md)
codify this as non-goals ("A graphical BPMN modeler — Atlas executes models, it
doesn't draw them"; "A batteries-included application server"), and a graphical
modeler was only ever contemplated as import interoperability in Milestone 6.

Practice is pushing back on that stance. A workflow engine that no one can
author for, deploy to, or observe is hard to adopt. We keep arriving at the same
conclusion: we cannot avoid offering a modeler, and we need an operations
surface alongside it. The question this ADR answers is **not "should we build a
UI"** — that is now taken as given — but **how it is structured**: how many
binaries, where design-time artifacts live, and what the modeler is built on.

The need is best understood through the people who touch a process across its
lifecycle:

| Persona | Time domain | Needs from the system |
|---|---|---|
| **Business + business engineer** — *draws* the process | Design-time | BPMN canvas, collaboration/comments, a model repository with versioning |
| **Business engineer / developer** — *automates* the process | Design-time → **test** | Service/job wiring, event & message definitions, form building, deploy **and a test-run loop against a live engine** |
| **Operator** — *runs & optimizes* the process | Run-time | Operations console: instances, incidents, jobs, metrics, re-deploy/migration |
| *(End user — completes user tasks via forms)* | Run-time | *A tasklist surface — recognized but out of initial scope (see follow-ups)* |

The personas span two time domains (design vs. run) but depend on **one** shared
substrate: the public API. Notably, the automation persona's core activity —
**testing** — requires a running engine, so an offline, engine-less modeler does
not serve the persona that would most use it.

## Decision drivers

- **One shared API, many audiences.** All personas ultimately act on the same
  public API surface (deploy, query, complete job, publish message).
- **Respect the engine's invariants.** The runtime store (`wal` + `state`) is
  single-writer, append-only, event-sourced (invariants 2–6, ADR-0001/0002/0005).
  Design-time artifacts are mutable, collaborative, and versioned — a different
  consistency model entirely.
- **Operational simplicity.** Fewer deployment units is better unless a hard
  requirement forces separation.
- **Don't reinvent BPMN drawing.** The engine's value is execution, not a canvas.
- **Phaseability.** We should be able to ship something useful early without
  building the whole lifecycle at once.

## Considered options

**On topology (how many binaries):**

1. **Single server binary** (`cmd/atlasd`) hosts the public API, a model
   repository, and serves every UI surface as embedded static assets.
2. **Separate modeler binary** independent of the engine server.
3. **Modeler folded into a future engine server binary** as one monolith with no
   separation of the model-repository concern.

**On modeler basis:**

- A. **Embed `bpmn-js`** (Apache-2.0; the core behind the Camunda modeler) as a
  static web bundle; we build integration + deploy/test wiring only.
- B. **Build our own** editor (Go/WASM canvas, routing, XML serialization).
- C. **Import only** — no drawing, ingest models authored elsewhere.

## Decision outcome

**Topology: option 1 — a single `cmd/atlasd` binary.** It exposes the public API,
owns a **model/resource repository stored separately from the engine's runtime
store**, and serves the UI surfaces as `go:embed`-ded static assets. The three
personas are UI *surfaces* (a design studio for the draw/automate personas, an
operations console for the operator), not separate deployments — they share the
API and auth layer and differ only in *when* in the lifecycle they act.

**Modeler basis: option A — embed `bpmn-js`.** The modeler is a web frontend, not
a Go program. We wrap `bpmn-js` and add the Atlas-specific layers: automation
authoring (service/job wiring, event/message definitions, forms) and a
deploy-and-test loop against the engine.

**Phasing.** Ship in the order that maximizes early value and minimizes new
concepts per step:

1. **Operations console (operator persona).** Depends only on the query API; it
   is Milestone-4 work and useful the moment there is anything to run.
2. **Model repository + import/deploy + test loop (automation persona).**
   Introduces the design-time store and the test-run loop.
3. **Full editor + collaboration (draw persona).** The heaviest surface; built
   last on top of the now-proven repository and API.

### Consequences

- **Positive:** One deployment unit and one API/auth layer serve all personas.
  The engine core stays a pure library; the UI lives entirely in `atlasd`.
  Design-time artifacts get a fitting store instead of being forced into the
  event log. Reusing `bpmn-js` avoids rebuilding a canvas and keeps us
  interoperable with the wider BPMN toolchain. Phasing lets the operator surface
  ship independently of the modeler.
- **Negative / trade-offs accepted:** This reverses two stated non-goals — Atlas
  is no longer "library only." `atlasd` introduces a **second persistent store**
  (design-time, mutable) with its own backup/versioning story, kept strictly out
  of the runtime hot path. Embedding `bpmn-js` brings a JS/Node build toolchain
  into the project. There is no engine-less offline modeler — modeling that needs
  testing needs a reachable engine.
- **Follow-ups / risks to watch:** Choose the design-time store (it must **not**
  reuse or contaminate the runtime `wal`/`state` — likely plain Pebble or files,
  separate lifecycle). Decide whether the **end-user / tasklist** persona is in
  scope; if so it is a fourth surface on the same binary. Define the model
  repository's versioning and deployment-artifact model. Keep the API the single
  entry point so no UI reaches into engine internals. Update `ROADMAP.md`
  non-goals and milestones once this is Accepted.

## Pros and cons of the options

### Topology 1 — single `atlasd` binary
- Good: one deployment unit, one API/auth layer, shared across surfaces; engine
  stays a library; natural home for the model repository.
- Bad: the binary grows a UI and a JS toolchain; a second store to operate.

### Topology 2 — separate modeler binary
- Good: modeler could run standalone/offline; independent release cadence.
- Bad: the automation persona must **test** against a live engine, so standalone
  buys little; duplicated API/auth/operational surface. Only justified if
  engine-less modeling is a hard requirement — it is not.

### Topology 3 — monolith with no repository separation
- Good: fewest moving parts on day one.
- Bad: pressures design-time artifacts toward the event-sourced runtime store,
  which violates its purpose and the invariants. Rejected.

### Modeler A — embed `bpmn-js`
- Good: proven BPMN serialization; least effort; toolchain interoperability.
- Bad: adds a JS build; we live with an external library's model.

### Modeler B — build our own
- Good: full control, no JS toolchain.
- Bad: very high effort; hardest contradiction of the "don't draw" spirit.

### Modeler C — import only
- Good: closest to the current roadmap; smallest surface.
- Bad: does not serve the draw/automate personas, who need to author, not just
  ingest.

## Links

- reverses non-goals in [`ROADMAP.md`](../../ROADMAP.md); relates to Milestone 4
  (Operability) and Milestone 6 (modeler interoperability)
- respects ADR-0001, ADR-0002, ADR-0005 (runtime store stays event-sourced,
  single-writer, durable-before-visible; design-time store is separate)
- the public API this builds on is itself Milestone-4 scope
- the UI surfaces here are on-by-default and opt-out per ADR-0012
