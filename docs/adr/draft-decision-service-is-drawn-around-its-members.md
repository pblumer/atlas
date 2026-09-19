# ADR-DRAFT: A decision service is drawn, and drawn around its members

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-19
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0325](0325-dmn-diagram-is-completed-on-read.md) gave DMN models a generated
diagram: `EnsureDiagram` completes one on read, `RegenerateDiagram` replaces one on
request, and a model whose diagram covers *some* elements is laid out afresh rather
than completed around whatever a half-aware tool left behind.

That generator knows `inputData`, `decision` and `businessKnowledgeModel`. It does
not know `decisionService` — the element [ADR-0398](0398-a-business-rule-task-can-call-a-decision-service.md)
made callable from a business rule task. A service is not a node in the
requirements graph; it is a **box drawn around part of it**, split by a divider
line into the decisions it returns (above) and the ones it evaluates internally
(below).

**What actually happens, measured** on a model with three decisions, a service
around two of them, and a complete hand-authored DMNDI:

- `EnsureDiagram` leaves it alone — correctly, every *node* is drawn. `fullyDrawn`
  never looks at the service, so a model whose decisions are all placed but whose
  service has no shape also counts as complete, and is handed to `dmn-js` as if it
  were somebody's finished arrangement.
- `RegenerateDiagram` (the editor's Auto-layout) strips the whole DMNDI and re-emits
  it without the service: the box and its divider line are gone.
- `dmn-js` then finds a `decisionService` with no `DMNShape` and invents an empty
  default box. Because it rereads membership from geometry — a member is an output
  decision if its centre is above the divider, encapsulated otherwise — the empty
  box says the service has no members, and the next save writes that back.

The end state is a service with **no `outputDecision`**. It still compiles, is still
listed, is still callable from a business rule task, and returns nothing. No
incident, no validation error: every instance quietly takes the default branch. One
Auto-layout click is enough, and the damage is only visible by reading the stored
XML.

## Decision drivers

- **A generated picture must not rewrite the document.** `dmn-js` derives
  membership from geometry, so geometry that contradicts the references is not a
  cosmetic defect — it is a silent edit of the model's semantics.
- **The rule from ADR-0325 already covers this case.** A diagram that draws the
  decisions but not the service is exactly "the residue of a tool that drew what it
  could": every DMN 1.3-era tool produces it. Extending `fullyDrawn` to count
  services applies the existing rule rather than inventing one.
- **Determinism.** The same model must always produce the same picture (ADR-0325).
- **No engine dependency.** The layout generator reads DMN XML with its own structs
  and writes DMN-DI back. A service's membership is in the document, so nothing here
  needs temis.

## Considered options

1. **Draw the service box around its members, and count it in `fullyDrawn`.**
2. **Preserve an existing service shape through `RegenerateDiagram`, draw none.**
   Auto-layout would keep a box it cannot place, leaving it wherever it was while
   everything around it moved — and a model that never had one stays broken.
3. **Refuse to lay out a model containing a decision service.** Honest about the
   gap, but it leaves the editor's Auto-layout doing nothing on exactly the models
   that most need tidying, and does not help a model that arrives with no diagram.

## Decision outcome

**Option 1.** `dmn/layout.go` learns `decisionService`:

- `parseDRG` reads each service's `outputDecision` and `encapsulatedDecision`.
  `inputDecision` is deliberately not read: it names the boundary the *caller*
  supplies, which DMN draws **outside** the box.
- `fullyDrawn` requires a shape for every service as well as every node. A model
  missing only the service shape is therefore laid out afresh — the ADR-0325 rule,
  applied to an element it did not yet know.
- Layering gains a constraint: every output decision of a service sits in a strictly
  deeper layer than every encapsulated one, so the box's compartments come out the
  way the document declares them. The requirements graph usually says so already;
  where it does not, the document wins, because the alternative is a picture that
  rewrites the model. These constraints are never drawn as edges.
- Within a layer, a service's members are placed side by side ahead of everything
  else, so the box is a rectangle around its own members rather than a band across
  whatever shared the row.
- `generateDMNDI` emits a `DMNShape` per service — the bounding box of its placed
  members plus a margin, with a `DMNDecisionServiceDividerLine` between the lowest
  output decision and the highest encapsulated one — **before** the shapes it holds,
  because a viewer that does not treat the box as a container paints in document
  order.
- A service whose members are all in one compartment gets the divider just inside
  the far edge. A service with nothing to draw around still gets a box, in its own
  row under the graph: without one the diagram would read as incomplete on every
  read and the model would be redrawn each time it is opened.

### Consequences

- A stored model whose decisions are drawn but whose service is not is **re-laid on
  the next read**. That moves a diagram somebody may have arranged. It is the lesser
  harm: the alternative is the silent loss of `outputDecision` described above, and
  such a diagram was not arranged by a tool that understood the service anyway.
- A decision no service names can still fall inside a generated box when the rows
  interleave. It is cosmetic only: `dmn-js` nests a decision solely when the service
  *names* it, so containment and membership cannot drift apart because of it.
- Where two services both name a decision, the picture nests it in the first, since
  a shape has one parent. Both memberships remain in the document.

## Links

- [ADR-0325](0325-dmn-diagram-is-completed-on-read.md) — the generator this extends
- [ADR-0398](0398-a-business-rule-task-can-call-a-decision-service.md) — what a decision service is, and how a task calls one
- [ADR-0379](0379-dmn-version-follows-the-document.md) — the DMNDI namespace follows the model's own
- [ADR-0012](0012-web-ui-app-shell.md) / [ADR-0062](0062-embedded-dmn-editor.md) — why the modeler is vendored as a pre-built bundle
