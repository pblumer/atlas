# ADR-0124: Server-side BPMN diagram auto-layout in Go

- **Status:** Accepted
- **Date:** 2026-08-13
- **Deciders:** Atlas engine team

> **Implementation status.** Accepted and in place. Atlas generates BPMN diagram interchange (DI) in
> Go, on the server, in [`api/layout.go`](../../api/layout.go). Two entry points share one generator:
> `ensureDiagramLayout` injects a layout when the browser fetches a model that carries none
> (`handleProcessXML`), and `relayoutDiagram` discards the existing DI and re-flows from scratch to
> back the Modeler's **Auto-layout** button and its **F8** shortcut (`handleLayout`). This ADR records
> **why Atlas builds and maintains its own hand-written Go generator** for layout rather than adopting
> an off-the-shelf library.

## Context and problem statement

A BPMN model has two halves: the **semantic** model (processes, flow nodes, sequence flows) and the
**diagram interchange** (`<BPMNDiagram>` — the shapes and edge waypoints that say *where* each element
is drawn). bpmn-js needs DI to render. Two situations leave us without usable DI:

- A model **deployed as pure semantic XML** (via the API, MCP, or a generator) carries no diagram.
  Per ADR-0011 the embedded viewer must still render it, so *something* has to synthesize a layout on
  read.
- A user asks to **re-flow** a diagram they have tangled by hand — the Auto-layout button / F8.

Both are served today by a hand-written layered generator in `api/layout.go` (~840 lines): longest-path
layering into columns, a traced "trunk" (happy path) kept on one straight axis, side branches stacked
off-axis, expanded subprocesses sized to their contents, boundary events ridden on their host's border,
orthogonal edge routing, pools stacked as bands, and swimlanes as horizontal lanes with shared columns.

This generator is deliberately simple, and its limits show. Recent fixes had to stop it bending a
linear happy path into a staircase and route error branches to the correct side — symptoms of a
longest-path layerer with no crossing-minimization or port model. That prompted the question this ADR
answers: **should Atlas adopt an established layout library instead of growing its own generator?**

The framing constraint is that **there is no mature Go library for BPMN or general layered-graph
layout.** The ecosystem is JavaScript (bpmn.io's `bpmn-auto-layout`, `elkjs`, `dagre`) or a native
binary (Graphviz). Atlas is Go, CGO-free (ADR-0010), single-binary (ADR-0011), and does this work
server-side — so "just use a library" is really "change where and in what runtime layout happens."

## Decision drivers

- **Layout-less models must render server-side.** The read path (`handleProcessXML`) has no browser in
  the loop; it must produce DI in-process. (ADR-0011)
- **CGO-free, single static binary.** No native `.so`/Graphviz dependency; nothing that breaks
  cross-compilation or the single-artifact distribution. (ADR-0010, ADR-0011)
- **Determinism.** `ensureDiagramLayout` runs on every fetch and must be stable input→output (stable
  tests, no diff churn, no wall-clock/RNG). Whatever we use has to be reproducible.
- **BPMN-specific structure**, not just a DAG: pools (ADR-0023), swimlanes (ADR-0121), expanded/nested
  subprocesses, and boundary events attached to activity borders.
- **Operational simplicity.** No second language runtime shipped in the server process if we can help
  it.
- **Layout quality ceiling** — crossing minimization, port-aware orthogonal routing — for dense models
  with many gateways.
- **Coverage floor (ADR-0018).** Whatever we own, we test to the 95% floor; a large vendored algorithm
  is not something we unit-test line by line.

## Considered options

1. **Keep and incrementally improve the Go generator** (`api/layout.go`).
2. **ELK (Eclipse Layout Kernel) via `elkjs`** — run the gold-standard layered layouter, either in an
   embedded JS runtime server-side or by moving layout into the Modeler client.
3. **bpmn.io `bpmn-auto-layout`** in the Modeler client — the official BPMN-native auto-layout, JS.
4. **`dagre`** — a small JS layered-DAG layouter.
5. **Graphviz (`dot`)** — shell out to the native binary for coordinates.

## Decision outcome

Chosen option: **"Keep and incrementally improve the Go generator" (Option 1).** Atlas builds and owns
its layout generator.

It is the only option that satisfies the hard constraints as they stand: it runs in-process on the
server read path, ships in the single CGO-free binary, is deterministic, and already models the
BPMN-specific structure (pools, lanes, subprocesses, boundary events) that a generic DAG layouter does
not. The recent defects were shortcomings of *our algorithm*, not evidence that the approach is wrong;
they were fixable in tens of lines with regression tests. We improve the generator one shape-class at a
time and grow its test suite with it.

Every library option is rejected, all for the same root reason — they trade our in-process, CGO-free,
deterministic Go read path for a JavaScript runtime or a native binary, and none of the BPMN-aware ones
is enough better to justify that. `bpmn-auto-layout` and `dagre` are barely more capable than what we
have. Graphviz is a native dependency that breaks the single-binary distribution. **ELK** is the only
one that would genuinely raise layout quality (crossing minimization, port-constrained orthogonal
routing) — but it is JavaScript and carries the same runtime-and-architecture cost, so it too is not
adopted here. Should layout *quality* ever become the binding constraint, reopening that question is a
fresh decision for a future ADR, not a plan this one commits to.

### Consequences

- **Positive:** No new dependency or runtime; the single-binary, CGO-free, deterministic properties
  hold. Full control over BPMN-specific placement. Small, testable changes at the 95% coverage floor.
  The server read path stays self-contained.
- **Negative / trade-offs accepted:** We own a layout algorithm and its long tail of edge cases. It has
  a real quality ceiling — no crossing minimization, no port model — so dense or highly branched models
  will look worse than an ELK-quality tool would render them. Improvements arrive one shape-class at a
  time.
- **Follow-ups / risks to watch:** Watch for models the generator visibly mishandles (many-way
  gateways, dense cross-links, deep nesting) and improve the generator to cover them. Only if
  hand-tuning stops keeping pace does a library become a question worth reopening — as a fresh ADR, not
  a pile of special cases. Keep the generator deterministic and side-effect-free — the read path
  depends on it.

## Pros and cons of the options

### Option 1 — Keep the Go generator
- **Good:** In-process on the server read path; CGO-free single binary; deterministic; already BPMN-aware
  (pools, lanes, subprocesses, boundary events); small diffs, unit-testable to the coverage floor; no
  new runtime.
- **Bad:** We own the algorithm; simplistic layerer with a quality ceiling (no crossing minimization or
  port routing); each element class is hand-tuned.

### Option 2 — ELK / elkjs
- **Good:** Best-in-class layered layout — crossing minimization, port-aware orthogonal routing,
  hierarchical nesting; well-maintained; would materially raise quality for complex models.
- **Bad:** JavaScript; requires an embedded JS runtime server-side (to preserve the layout-less read
  path) or moving layout into the client and reshaping the DI-generation architecture; async, heavier;
  mapping BPMN semantics (boundary events, lanes, pools) onto ELK's graph model is real work; large
  vendored surface we don't unit-test.

### Option 3 — bpmn.io `bpmn-auto-layout`
- **Good:** BPMN-native, produces DI directly, aligns with the vendored bpmn-js modeler (ADR-0013).
- **Bad:** JavaScript, client-side — same runtime/architecture cost as ELK; and it is itself a basic
  layouter (limited lane/boundary handling), so the cost buys little over what we already have.

### Option 4 — dagre
- **Good:** Small, simple, widely used layered-DAG layout.
- **Bad:** Not BPMN-aware (no boundary events, lanes, pools, ports); JavaScript; brings the runtime
  migration for roughly parity with the current generator.

### Option 5 — Graphviz (dot)
- **Good:** Mature, high-quality layered layouts; callable per subprocess.
- **Bad:** Native binary dependency — breaks the CGO-free single-binary distribution (ADR-0010,
  ADR-0011); not BPMN-aware; coordinate mapping and process management overhead.

## Links

- Depends on ADR-0011 (single-binary distribution with an embedded web viewer/editor) — the reason
  layout is synthesized server-side.
- Relates to ADR-0013 (embed the bpmn-js modeler) — the client that consumes the DI.
- Relates to ADR-0010 (Go, no CGO) — rules out the native-binary option.
- Serves ADR-0023 (collaborations and pools) and ADR-0121 (BPMN lanes) — structures the generator lays
  out.
- Constrained by ADR-0018 (test-driven development / coverage floor).
- Implemented in [`api/layout.go`](../../api/layout.go); entry points `handleProcessXML` /
  `handleLayout` in [`api/handlers.go`](../../api/handlers.go).
