# ADR-0395: A product's structure is assembled per product, not related pairwise

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-18
- **Deciders:** Atlas maintainers

## Context and problem statement

A catalogue is built out of services. Each one carries its own `provisionProcess` and
`deprovisionProcess` (ADR-0312), so it provisions without asking a whole what to do,
and the same record is offered by several catalogues and used by several wholes. What
a *product* adds on top of that is an **arrangement**: which services come with it and
cannot be deselected (`composition`), and which are offered beside it and ordered only
if ticked (`aggregation`).

The authoring surface did not ask for an arrangement. It asked for edges: a form with
*from*, *relationship* and *to*, writing one triple at a time into a table sorted by
relationship. That is the data as it is stored. Assembling one product through it means
finding that product's rows among every other product's, adding the new ones one at a
time, and removing the ones that no longer hold — with the whole arrangement never on
screen at once.

Two consequences followed from that, and both were about the same missing question:

- **Nothing showed what a product is made of.** The edge table answers "which pairs are
  related", and the question a product manager arrives with is "what comes with the
  laptop". Those are the same data and not the same screen.
- **A loop was only found at publish.** Publishing proves both graphs acyclic and
  refuses with the whole list of problems (ADR-0312). That is the right place for the
  proof and a late place for the feedback: the refusal arrives about a catalogue the
  reader has since edited, naming a pair rather than the choice that closed the loop.

## Decision drivers

- The question somebody has is per product, and asked once per service.
- Structure and precedence are different questions and are validated separately.
  A screen that offers them in one list teaches the conflation the model corrects.
- One authoring surface per fact. Two surfaces writing the same edges drift, and the
  one that drifts here decides what somebody is actually ordering.
- A refusal is worth most at the moment the choice is made.

## Considered options

1. **Keep the pairwise form and add a per-product view that only reads.**
2. **Assemble per product, and keep the pairwise form for structure as well.**
3. **Assemble per product, and narrow the pairwise form to precedence.**

## Decision outcome

Chosen option: **"assemble per product, and narrow the pairwise form to precedence"**.

The kit lists every *other* product the catalogue offers and asks one question per row,
with three exhaustive and mutually exclusive answers — not part of it, included,
optional — pre-selected from the edges as they stand. One save writes that product's
whole structure: the edges out of it are replaced by what the form says, and everything
else is carried over untouched, including every other product's arrangement and every
precedence edge, the assembled product's own included.

Before writing, the kit refuses a choice that closes a loop, naming the product that
already contains this one — directly or through another. The rule is the publish rule;
it is applied here as well because this is where the reader still knows which choice
they just made. Publish remains the proof: the kit checks what it is about to write
and cannot vouch for a catalogue somebody else is editing at the same time.

The pairwise form keeps `requires` and loses the two structure kinds. Precedence stays
pairwise because it *is* pairwise: "the account before the mailbox" is a statement
about two products and belongs to neither of them.

### Consequences

- **Positive:** a product's structure is authored, and read, in one place and at one
  time. A loop is refused where it is made. The vocabulary somebody reads — *included*,
  *optional* — is the vocabulary of the portal they are building, not of the store.
- **Negative / trade-offs accepted:** the kit is quadratic on screen — every product
  lists every other — so a catalogue of a hundred products gets a long panel. That is
  the shape of the question and not of the implementation; a catalogue that large needs
  a search inside the kit, which is a later change and not a different decision.
  Structure can no longer be written pairwise at all, so the edge kinds the store
  accepts are wider than the ones this screen writes. That is deliberate: the API keeps
  the vocabulary, the screen keeps the question.
- **Follow-ups / risks to watch:** the edge table below the kit still *shows* structure
  and still removes a row. It is the reading surface for the whole catalogue and the
  kit is the writing surface for one product; if that pair starts to read as two ways
  to author the same fact, the remove button is the thing to take out.

## Pros and cons of the options

### Option 1 — a read-only per-product view
- Good: nothing to unlearn, no write path to get wrong.
- Bad: leaves the authoring exactly where it was. The reader now sees the arrangement
  and still has to edit it as triples somewhere else, which is one more screen for the
  same work.

### Option 2 — both surfaces write structure
- Good: nothing is taken away; an existing habit keeps working.
- Bad: two ways to say one thing. The pairwise form cannot express "not part of it"
  without a removal, so the two surfaces do not even have the same vocabulary, and the
  cycle refusal would have to live in both or be worth nothing in either.

### Option 3 — assemble per product, precedence stays pairwise
- Good: one surface per question, each shaped like the question. The refusal has one
  place to live.
- Bad: somebody who thinks in edges has to think in arrangements instead, and a
  structure edge to a product the catalogue does not offer can no longer be authored —
  which publish refuses anyway.

## Links

- relates to [ADR-0312](0312-portal-catalogue-order-inventory.md) — the catalogue,
  order and inventory model, where `composition` and `aggregation` are defined and
  where publish proves both graphs acyclic.
- relates to [ADR-0315](0315-portal-roles-and-responsibilities.md) — a product is
  edited through its one home catalogue, and another catalogue may offer it. The
  arrangement is the *catalogue's* and not the product's, which is why the kit is
  drawn on the catalogue screen and may be used for a guest product.
- relates to [ADR-0376](0376-catalogue-maintenance-over-mcp.md) — the `revision`
  precondition the kit's save carries, because it replaces `edges` whole.
- answers the first half of issue #1022. The second half — drawing the catalogue in
  Panorama — is untouched by this record.
