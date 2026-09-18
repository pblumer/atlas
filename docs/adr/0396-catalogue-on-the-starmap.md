# ADR-0396: The service catalogue is drawn on the starmap, as derived fact

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-18
- **Deciders:** Atlas maintainers
- **Open question:** Whether reading the catalogue and item stores on every starmap
  request stays affordable. The two scope-bearing stores the mesh already reads per
  request (projects, workers) are the cheapest it touches; the item store is the
  first one that grows with a customer's catalogue rather than with their estate, and
  no installation has yet filled one large enough to say where that stops being free.
- **Question checked:** 2026-09

## Context and problem statement

Atlas holds two halves of one estate and has never drawn them on one picture.

The **starmap** ([ADR-0211](0211-panorama-derived-landscape-mesh.md)) is the derived
half: applications, deployed processes, the workers and decisions they use, the peers
they promote to — every edge a fact the server can point at rather than something
somebody drew. The **service catalogue** ([ADR-0312](0312-portal-catalogue-order-inventory.md))
is the other: what a group of people may order, what each product is assembled from,
and the BPMN process that provisions each one.

They are joined in the data and separated on every screen. A product names a process
id; whether anything is deployed under that id is a question the catalogue screen
cannot answer, because it cannot see the engine, and the Operations view cannot
answer either, because it has never heard of the catalogue. The failure that follows
is the ordinary one: a product is published bound to a process nobody deployed, it
looks orderable, and the first person to order it waits for a laptop while the order
parks.

Issue #1022 asks for the catalogue to be drawn in Panorama. The question this record
settles is **which Panorama** — the drawn half or the derived half — and what the
picture may and may not say once it is there.

## Decision drivers

- Every line on the starmap is a fact the server can point at. Whatever is added has
  to meet that test or it does not belong.
- The catalogue is the one part of Atlas that holds *composition* and *aggregation*
  as data. ADR-0211's ArchiMate projection had to declare, in its loss list, that no
  relationship here was either of them. That stopped being true.
- A catalogue carries an audience, approval rules and a price list. Who may look at
  one is a decision it already makes, and a derived picture must honour it rather
  than become the way around it (ADR-0211 §3).
- A picture that quietly omits something is worse than one that says it omits it.

## Considered options

1. **An authored ArchiMate document** — the inverse of the catalogue's existing
   ArchiMate import: generate a Panorama model from a catalogue, owned by an
   application, edited from then on like any drawn model.
2. **A screen of its own in Panorama** — a second canvas beside the starmap, drawing
   catalogues only.
3. **More derived kinds on the starmap** — catalogues and products join the mesh as
   node kinds, their arrangement joins it as edge kinds, and the product's
   provisioning process is the edge that ties the two halves together.

## Decision outcome

Chosen option: **more derived kinds on the starmap**.

The mesh gains two node kinds, `catalog` and `product`, and four edge kinds:
`offers` (a catalogue puts a product in front of the people it reaches),
`composition` and `aggregation` (what a product is assembled from, integrally and
optionally), and `requires` (precedence). A product's `provisionProcess` and
`deprovisionProcess` resolve to the deployed process node through the same three-way
resolution a call activity already uses — the process, a **restricted** placeholder
for one this reader may not see, or an **unresolved** node for one nothing here
provides.

That last line is the feature. *A product bound to a process nobody deployed now
appears on the landscape as a product pointing at a hole*, in the same ink as every
other broken dependency on the picture, without anybody modelling anything.

### What the picture will not say

- **Incompatibility is not drawn.** A catalogue can record that two rights must never
  be held by the same person ([ADR-0342](0342-conflicting-rights.md)); it is the one
  catalogue relationship that means the opposite of every other line on the canvas,
  and drawn in the same ink it would read as a dependency. It is named in the derived
  notation's loss list — which until now was empty, because the derivation had nothing
  to declare.
- **Precedence is drawn and not exported.** ArchiMate's Triggering runs between
  behaviours and its Serving asserts a provider and a consumer; neither is what "this
  cannot be provisioned before that" says about two products. Rather than pick the
  nearest wrong relationship, the exporter leaves it out and the document says so —
  and the export now counts a relationship it has no *word* for apart from one whose
  *end* is missing, because those send a reader to two different places.
- **No orders, and no prices.** An order is runtime, not landscape; a price is on the
  catalogue's own screen behind the catalogue's own rules. The starmap draws what is
  offered and what it depends on.
- **A product nothing offers is not drawn.** The mesh is the dependency picture and
  not an inventory — the same rule that keeps a configured worker nothing uses off the
  canvas.
- **Nothing here carries a state.** No catalogue and no product is ever coloured as a
  finding: nothing observes either of them, so both take the neutral rendering
  (ADR-0211 §4). In particular an unpublished catalogue is *not* drawn as a fault —
  a catalogue somebody is still filling would be red for a week, which is how a status
  view teaches people to ignore it. That it shows nobody anything is said on the
  catalogue screen, in words.

### Who sees it

A catalogue is on the picture for whoever **maintains** it — its owner, an editor, a
viewer it was shared with, and an administrator — and not for the people it is offered
to. Reaching a catalogue as a customer says what you may order; it says nothing about
the estate behind it, and a landscape that drew every audience member the catalogue's
products, their processes and the engine around them would be using that store to
answer a question it never agreed to answer. A product follows its **home** catalogue
(ADR-0315), which is the rule the product listing already applies.

The consequence is deliberate and worth stating plainly: **a modeler who maintains no
catalogue sees no catalogues**, exactly as they see no applications that were never
shared with them. The starmap needs the `modeler` role and the catalogue screen the
`productmanager` role, so the reader who sees both halves is holding two roles at
once. An installation that wants its architects to see the catalogue shares it with
them as viewer, which is one grant that already exists.

### The vocabulary

The ArchiMate projection maps a catalogue to **Grouping** and a product to
**Product**, `offers` and `aggregation` to **Aggregation**, and `composition` to
**Composition**. The last two are the first relationships on this landscape ArchiMate
can name *exactly* rather than approximately, which is why the mapping version moves
to 2: a document generated before and after is a different document about one estate,
and that is the question the version exists to answer.

### Consequences

- **Positive:** the join that nothing could make is made, and made from facts. The
  ArchiMate export gains two element types and two relationship types it can state
  honestly. The blast-radius answer reaches the catalogue: what breaks if this service
  stops is now also *which products cannot be delivered*.
- **Negative / trade-offs accepted:** the catalogue and item stores are read on every
  starmap request, because both are read per request or neither can be — the
  catalogue record carries its own member list, and caching the items while reading
  the offers would let a product added a moment ago be offered by a catalogue while no
  item exists for it, drawing a dangling reference on the one picture where a dangling
  reference means something is broken. Catalogues and products also spend the node
  budget (ADR-0211 §7), and over it they collapse into their catalogues the way
  processes collapse into their applications.
- **Follow-ups / risks to watch:** there is no binding key for a catalogue or a
  product ([ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md) §4),
  so neither can be compared against a drawn model and neither counts towards drift —
  otherwise the drift number would grow by one every time somebody added a product,
  reporting a debt nobody could ever pay. When a key exists for them they join that
  count. The open question above is the one to watch first.

## Pros and cons of the options

### Option 1 — an authored ArchiMate document
- Good: the result is editable, shareable and diffable as a model, and the catalogue
  already has an ArchiMate *import*, so the inverse is a short step.
- Bad: it is a copy. The moment it is generated it begins to disagree with the
  catalogue, and nothing in the picture says which of the two is the estate. It also
  answers a question nobody asked — the catalogue is already authored, on a screen
  built for authoring it — while leaving the question that was asked, "is what we
  promise actually deployed", unanswerable, because a drawn model cannot see the
  engine either.

### Option 2 — a screen of its own
- Good: no existing picture changes; the catalogue gets a canvas shaped for it.
- Bad: two surfaces, and the edge that matters runs between them. A product and the
  process that provisions it would be on different screens, which is the state this
  change exists to end.

### Option 3 — derived kinds on the starmap (chosen)
- Good: one picture, one derivation, one authorization rule, one export. The
  catalogue's edges are facts, so they meet the test every other line here meets.
- Bad: the starmap gets busier, and an operator who only wants the running estate now
  has two node kinds to filter out. Products also grow with a customer's catalogue
  rather than with their estate, so the size budget is reached by a dimension the
  budget was not measured against.

## Links

- extends [ADR-0211](0211-panorama-derived-landscape-mesh.md) — §1's derivation
  table, §3's per-reader filtering, §4's neutral rendering, §7's collapse and §8's
  projection all apply unchanged; this adds rows to them.
- relates to [ADR-0312](0312-portal-catalogue-order-inventory.md) — the catalogue,
  order and inventory model these facts come from.
- relates to [ADR-0315](0315-portal-roles-and-responsibilities.md) — a product is
  edited through its one home catalogue, which is how this picture decides who may
  see one and which catalogue a product collapses into.
- relates to [ADR-0342](0342-conflicting-rights.md) — incompatibility, the one
  catalogue edge this picture refuses to draw.
- answers the second half of issue #1022.
