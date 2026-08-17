# ADR-0127: A layered layout pipeline and executable layout invariants

- **Status:** Accepted (invariant gate, phases 0–1) — phases 2–3 proposed
- **Date:** 2026-08-17
- **Deciders:** Atlas engine team

> **Implementation status.** The invariant gate and phases 0–1 are in place in
> [`api/layout.go`](../../api/layout.go) and
> [`api/layout_invariants_test.go`](../../api/layout_invariants_test.go): layout quality is checked by
> executable predicates over a model corpus, cycles are removed before layering, and the trunk follows
> the longest run rather than the furthest column. Phases 2–3 (dummy nodes with crossing minimization,
> and a port model) are decided in principle here but not built; each lands as its own change with the
> invariants as the acceptance test. This ADR does **not** reopen ADR-0124 — the generator stays
> hand-written Go, in-process, CGO-free.

## Context and problem statement

ADR-0124 kept Atlas's hand-written layered generator over every library option, and named the price:
"we own a layout algorithm and its long tail of edge cases … improvements arrive one shape-class at a
time." It set a condition for revisiting: "watch for models the generator visibly mishandles … only if
hand-tuning stops keeping pace does a library become a question worth reopening."

That watch has now produced evidence, and it points somewhere other than a library.

**The defects are not a long tail of unrelated edge cases.** In a single session, a corpus check found
four defects, none of which any existing test caught:

- A **rework loop** — the most ordinary cyclic BPMN there is — made longest-path layering run away. Each
  relaxation pass pushed the loop's target one column further, so `Review` was drawn to the *right* of
  the `Ok` gateway and the `Done` event that follow it, with its incoming flow cutting through both.
- **`markTrunk` followed the bypass.** Choosing the successor in the furthest column means a
  one-hop shortcut always wins over the real sequence, so the shortcut became the happy path and the
  main line `A → B → C` was exiled to a branch row.
- **Column-skipping edges were drawn through everything in between** — a bypass ran straight across
  three task boxes.
- **Two boundary events on one host overlapped.** Spreading them evenly across a 100px border puts two
  36px events 33px apart.

Each of these is a *missing phase of the standard layered-drawing pipeline*, not a mis-tuned constant:
no cycle removal, no path-weighted trunk selection, no dummy nodes reserving space for multi-layer
edges, no port model. The generator implements the pipeline's two easy phases (layering, coordinate
assignment) and skips the rest, so each skipped phase surfaces as its own class of defect — and each is
currently patched at the coordinate level, where the fixes do not compose.

**The tests made this worse rather than catching it.** Layout regressions were guarded by coordinate
assertions — "this label's right edge is at the event's center-x". That pins the implementation, not the
goal. When the boundary-label anchor moved and the *picture improved*, its own regression test failed;
meanwhile the four defects above lived in models no test named. Four fixes landed on the same few lines
in one week without the suite ever going red on the actual defect.

So the question is not "should we buy a layouter" (ADR-0124 answered that, and nothing here changes its
constraints). It is: **how do we raise layout quality in a way that converges, inside the Go generator
we chose to own?**

## Decision drivers

- **Convergence.** Fixes must compose rather than trade against each other. Four coordinate patches to
  one label in a week is the signal to watch.
- **Quality must be measurable**, not anecdotal — judged across a corpus, not on whichever diagram is
  open.
- **ADR-0124's constraints are unchanged:** in-process on the server read path, CGO-free single binary,
  deterministic input→output, BPMN-aware (pools, lanes, subprocesses, boundary events).
- **Testability at the ADR-0018 coverage floor.** Whatever we own, we test.
- **Incrementality.** Each step must ship on its own with a visible quality delta; no big-bang rewrite.
- **Determinism.** `ensureDiagramLayout` runs on every fetch — no wall-clock, no RNG, no map-order
  dependence.

## Considered options

1. **Continue coordinate-level patching** — fix each reported diagram as it appears.
2. **Adopt the standard layered-pipeline structure, plus an invariant-based quality gate** — implement
   the phases the generator skips, one per change, with quality defined by executable predicates over a
   corpus.
3. **Reopen ADR-0124 and adopt ELK/elkjs** — buy crossing minimization and port-aware routing.
4. **Constraint solver for coordinates** — express placement as a QP/LP (separation constraints,
   minimized weighted edge length) and solve.

## Decision outcome

Chosen option: **"Adopt the standard layered-pipeline structure, plus an invariant-based quality gate"
(Option 2).**

The decision has two halves, and the second is the one that makes the first safe.

### Quality is defined by invariants over a corpus, not by coordinates

Layout tests assert properties of the *picture*:

- no two shapes overlap (a container enclosing its children, and a boundary event straddling its host,
  are the declared exceptions);
- no edge crosses the interior of a box it does not connect;
- an explicit label overlaps no shape and is crossed by no edge;
- every forward sequence flow reads left-to-right (back edges exempt — a loop is *supposed* to return);
- the happy path is the longest chain through the model, and it is drawn straight;
- every flow node gets a shape and every sequence flow an edge.

These run over a corpus spanning the shape classes (linear, rework loop, bypass, boundary error, several
boundaries on one host, gateway fan, lanes, nested subprocess). Adding a model to the corpus protects a
shape class with every invariant at once, and the semantic roles that license the exceptions come from
the same parser the generator uses, so the checker and the generator cannot drift apart on what an
element *is*.

This is what makes phase-by-phase work convergent: a phase is accepted when it satisfies the invariants
on the whole corpus, and a later phase cannot silently regress an earlier one. Coordinate assertions
remain only where a specific geometric contract is genuinely the point; they are not the general
mechanism.

### The generator adopts the pipeline's missing phases, one at a time

| Phase | Before | Adopted | Fixes |
|-------|--------|---------|-------|
| 0 · Cycle removal | none | DFS back-edge classification, excluded from the forward pass | loop layering runaway |
| 1 · Layering & trunk | longest-path; trunk by furthest column | trunk by longest run ahead | bypass seizing the happy path |
| 2 · Dummy nodes & crossing minimization | none | virtual nodes per intermediate layer; median/barycenter sweeps | multi-layer edges drawn through boxes; crossings |
| 3 · Ports | implicit, hand-computed offsets | named attachment points carrying edge ends and labels | the recurring label-placement class |

Phases 0–1 are implemented. Phase 2 is the largest and the one that lifts ADR-0124's stated quality
ceiling; until it lands, a multi-layer edge that would cut through a node is detoured through a clear
channel below the diagram — an explicitly symptom-level mitigation, recorded as such so it is replaced
rather than built upon. Phase 3 ends the label-patching pattern by making a label's position a property
of the port it hangs from instead of an offset recomputed per case.

Coordinate assignment (Brandes–Köpf and similar) is deliberately **not** adopted: the current row-band
placement satisfies the invariants once phase 2 reserves space properly, and there is no evidence it is
the binding constraint.

### Consequences

- **Positive:** Layout quality becomes a property the suite enforces across a corpus rather than a
  judgement call on one diagram. Defects are found by construction — the corpus surfaced two of the four
  above that nobody had reported. Each phase is independently testable and ships on its own. ADR-0124's
  constraints (in-process, CGO-free, deterministic, BPMN-aware) are untouched, and the library question
  stays closed.
- **Negative / trade-offs accepted:** We are now explicitly implementing a layered-drawing algorithm
  rather than a heuristic placer — more code, and phase 2 changes the node model (dummy nodes take part
  in placement but emit no shape). The existing coordinate-based layout tests overlap with the
  invariants and will need pruning as phases land. The channel detour is a known stopgap in the tree.
- **Follow-ups / risks to watch:** Phase 2 must preserve determinism — sweep order and tie-breaks fixed
  by declaration order, never map iteration. Watch that the corpus grows with each reported bad
  diagram; an invariant suite is only as good as the models it runs on. The `api` package sits at
  94.7% against ADR-0018's 95% floor (pre-existing, not introduced here) and should be brought back
  above it. If phase 2 lands and layout quality *still* fails to keep pace, that — not this ADR — is
  the trigger to reopen ADR-0124.

## Pros and cons of the options

### Option 1 — Continue coordinate-level patching
- **Good:** Smallest possible diffs; no new concepts; each fix is obviously scoped to the diagram that
  prompted it.
- **Bad:** Demonstrably non-convergent — four patches to one label in a week, while four unreported
  defects sat in ordinary models. Fixes trade against each other (the label anchor that suits a single
  boundary event is wrong for two). No definition of "good", so no way to know whether a change helped.

### Option 2 — Layered pipeline + invariant gate (chosen)
- **Good:** Attacks the causes rather than the symptoms; each defect class maps to a named phase.
  Quality becomes measurable and regressions become mechanical to catch. Incremental — every phase ships
  alone. Keeps every ADR-0124 constraint. Phases 0–1 already show the pattern working: the invariants
  found the defects, the phases fixed them.
- **Bad:** We are writing a real layout algorithm, with the maintenance that implies. Phase 2 is a
  substantial change to the node model. Invariants can be satisfied by an ugly-but-legal layout — they
  bound correctness, not beauty.

### Option 3 — Reopen ADR-0124, adopt ELK/elkjs
- **Good:** Best-in-class crossing minimization and port-aware orthogonal routing; would clear the
  entire defect class at once.
- **Bad:** Every reason ADR-0124 rejected it still holds — JavaScript runtime server-side or a
  re-architected client-side layout path, trading the in-process CGO-free deterministic read path.
  Nothing in the evidence above says our *approach* is unworkable; it says we skipped phases. Buying a
  runtime to avoid implementing them is disproportionate.

### Option 4 — Constraint solver for coordinates
- **Good:** Declarative and genuinely "definable" — separation and alignment expressed as constraints
  rather than placement code.
- **Bad:** Solves the phase we do not have a problem with. Every defect found was in cycle handling,
  ordering, or routing — none in coordinate assignment. Adds a solver (and its determinism and
  performance questions) to the read path for no observed gain.

## Links

- Builds on ADR-0124 (server-side diagram auto-layout in Go) — inherits its constraints and answers the
  "improve the generator one shape-class at a time" follow-up with a structure for doing so. Does not
  supersede it; the library decision stands.
- Constrained by ADR-0018 (test-driven development / coverage floor) — the invariant corpus is how
  layout meets it.
- Constrained by ADR-0010 (Go, no CGO) and ADR-0011 (single-binary distribution) — the reasons the
  library options stay closed.
- Serves ADR-0023 (collaborations and pools) and ADR-0121 (BPMN lanes) — structures the pipeline must
  keep laying out correctly.
- Relates to ADR-0013 (embedded bpmn-js modeler) — the client that renders the generated DI.
