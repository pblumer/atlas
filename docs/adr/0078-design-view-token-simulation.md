# ADR-0078: Design-view token simulation — a client-side control-flow walkthrough

- **Status:** Accepted
- **Date:** 2026-07-29
- **Deciders:** Atlas maintainers

## Context and problem statement

The Design view (ADR-0011/0012/0013) is where a model is authored, before it is
executable. People new to BPMN read a static diagram and cannot easily tell what the
gateways *do*: which branch an exclusive gateway takes, that a parallel gateway forks and
then waits to join, how a token reaches an end event. The established teaching aid for
this — popularised by the Camunda Modeler's "token simulation" — is to animate tokens
moving through the diagram so the control flow becomes visible and explorable.

Atlas already animates tokens, but only for *real* runs: the live view, the collaboration
replay (ADR-0038), and the instance replay (ADR-0046) all poll the engine and paint
tokens from persisted events. None of those help at authoring time, on an unsaved
diagram, before anything is deployed.

The question: how do we let someone *play* a diagram in the Design view — fork, join,
choose a branch, reach the end — without deploying it, and without breaking the
buildless, self-contained, no-CDN constraints the modeler already lives under?

## Decision drivers

- **Teaching value.** The point is comprehension for newcomers: the "which way?" moment
  at a gateway, the fork/join of a parallel split, a token reaching the end.
- **Buildless and self-contained** (ADR-0012/0013). No npm dependency, no CDN, no build
  step. Whatever we add is plain JS embedded by `go:embed`, like the rest of `api/web`.
- **Do not conflate with execution.** This is not the engine. It must never deploy, call
  the server, or imply that FEEL conditions, scripts, or connectors ran. It animates the
  *shape* of the control flow, nothing more.
- **Reuse the existing modeler.** It should ride on the vendored `bpmn-js` instance the
  Design view already builds, not a second rendering stack.

## Considered options

1. **Vendor `bpmn-js-token-simulation`** — the upstream Camunda plugin, bundled offline
   like the DMN stack (ADR-0062) and committed as a minified blob.
2. **Write a small, self-contained simulation module** as a `bpmn-js` additional module
   in plain JS, registered only on the editor's modeler.
3. **Simulate server-side** — deploy a throwaway instance to the engine and reuse the
   live token overlay.

## Decision outcome

Chosen option: **Option 2 — a hand-written `bpmn-js` additional module**
(`api/web/token-simulation.js`), exposing an `atlasTokenSimulation` service.

This mirrors the precedent already set in ADR-0013: rather than pull in the ES-module
properties panel, Atlas ships a small hand-written Details panel that talks to the
`bpmn-js` API. The token simulation is the same shape of decision — a focused, buildless
module over the vendored modeler — and it keeps full control over the semantics we teach.

The module walks the compiled diagram graph client-side:

- **Start events** get a spawn affordance; clicking one drops a token.
- **Sequence flows** animate a token dot along their waypoints (a dedicated SVG layer in
  diagram coordinates, the same technique as the ADR-0038 message dots).
- **Parallel gateways** fork onto every outgoing flow and, converging, wait for a token on
  every incoming flow before emitting one (the join is the lesson).
- **Exclusive / inclusive / event-based gateways** *pause* and highlight their outgoing
  flows; the user clicks one to send the token — the "which way?" teaching moment. The
  modelled default flow is emphasised.
- **End events** consume the token and count a completion.

Play/pause/step/reset and a speed control drive it; editing gestures are cancelled while
active (high-priority `bpmn-js` event handlers) and the palette/context pad are hidden by
CSS, so a click means "spawn / choose a path", not "add a shape".

### Consequences

- **Positive:** newcomers can explore a model's control flow interactively, at authoring
  time, with zero setup and no deploy. It reuses the existing modeler, animation
  technique, and marker/overlay conventions, so it is small and consistent.
- **Negative / trade-offs accepted:** it is an *approximation of control flow*, not the
  engine. It does not evaluate conditions, run scripts/connectors, honour timers/messages,
  or model multi-instance or event subprocesses. Inclusive-gateway semantics are
  simplified (a diverging inclusive gateway is treated as a single choice; a converging
  one waits for all incoming branches, like a parallel join). These are teaching
  simplifications, deliberately not execution semantics — the engine remains the single
  source of truth for how a process actually runs.
- **Follow-ups / risks to watch:** if the gap between the simulation and real execution
  ever misleads, prefer narrowing the simulation (or labelling the limitation in the UI)
  over creeping toward a second execution engine in the browser. Richer element support
  (boundary events, multi-instance) can be added incrementally within the same module.

## Pros and cons of the options

### Option 1 — vendor the upstream plugin
- Good: the exact, battle-tested Camunda experience; broad element coverage.
- Bad: a large third-party bundle to build offline and commit; its execution model and UI
  are ours to maintain-by-proxy but not to shape; heavier than the teaching goal needs.

### Option 2 — hand-written module (chosen)
- Good: buildless and self-contained; small; full control over exactly what we teach and
  how it looks; reuses our animation/overlay conventions; no new dependency.
- Bad: we own the BPMN-semantics subset it covers, and must resist scope-creep toward a
  second engine.

### Option 3 — simulate server-side
- Good: real execution semantics, for free.
- Bad: requires deploying an unsaved draft, evaluating real conditions/scripts, and a
  round-trip per step — exactly the coupling and side effects a design-time aid must
  avoid. Wrong tool for "play with the shape of the flow".

## Links

- builds on ADR-0011 / ADR-0012 / ADR-0013 (embedded, buildless, self-contained modeler)
- reuses the token-animation technique from ADR-0038 (collaboration replay)
- complements the runtime token views: ADR-0046 (instance replay), ADR-0065 (multi-token
  replay) — those paint *real* runs; this one is a design-time walkthrough
