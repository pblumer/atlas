# ADR-DRAFT: A person sees every catalogue they are the audience for

- **Status:** Proposed
- **Implementation:** Partial
- **Date:** 2026-11-03
- **Deciders:** Patrick Blumer (product owner), Claude (implementation)

## Context and problem statement

ADR-0312 decision 11 gave every person exactly one catalogue: of the catalogues whose
audience their groups reach, the highest-ranked. Rank resolved a many-to-many
membership into one answer, publishing refused a rank tie, and the shop asked
`GET /api/v1/shop/catalog` for that one catalogue and read its newest release.

Against a real Microsoft 365 tenant the rule hid what it was meant to organise. A
catalogue of Exchange Server products, given the same audience as the staff catalogue
but a lower rank, was seen by nobody: everybody in its audience also reached a
higher-ranked catalogue, which was their whole shop. The only way to show its products
was to copy them into every catalogue that outranks it. ADR-0347 had already met the
same wall from the other side — it could not narrow a product by putting it in a
stricter catalogue, because a second catalogue hides or replaces the first.

Customers do not think of a shop as one catalogue. They are in several groups, each
group is offered something, and they expect to see all of it — with a heading such
as "Mail and calendar" shown once, whichever catalogues fill it.

## Decision drivers

- A product offered to somebody must be orderable by them; reaching an audience must
  not be undone by reaching a second one.
- An order stays a snapshot of one release (ADR-0312): what was ordered cannot change
  because a catalogue was edited while an approval was pending.
- The server's check of who may order from a catalogue is already per catalogue
  (`MayOrderFrom`) and must stay the gate.
- No change to the order record, the waves of fulfilment or the inventory.

## Considered options

1. **Keep one catalogue per person** and copy products into every catalogue that
   should offer them.
2. **Every reached catalogue is shown; a mixed basket is placed as one order per
   release.**
3. **Every reached catalogue is shown; one order may name several releases.**

## Decision outcome

Chosen option: **2**.

- `GET /api/v1/shop/catalogs` answers every catalogue the caller's groups reach,
  highest rank first; reaching none is an empty list. `GET /api/v1/shop/catalog`
  stays, answering the top-ranked one, for what reads a single catalogue.
- The shop reads the newest release of each and lays them over one another. A product
  several of them carry is taken from the highest-ranked; what makes it up comes from
  that same release. Categories are headings over the merged list, so equal category
  names of different catalogues become one heading.
- Rank no longer decides *whether* somebody sees a catalogue. It decides which copy of
  a product they see, whose name, theme and languages the shop wears, and the order the
  catalogues are read in. Publishing still refuses a rank tie, which keeps that answer
  deterministic.
- Placing a basket groups it by the release each product came from and places one
  order per release, each with its own products, the parts they are made of, their
  answers and their shapes. Each order is the snapshot ADR-0312 describes, checked by
  `MayOrderFrom` for its own catalogue.

### Consequences

- **Positive:** a catalogue offered to somebody is ordered from by them; a catalogue
  for a narrower audience adds to the shop instead of hiding it; equal headings read as
  one. Nothing changes in the order record, fulfilment or inventory, and the server's
  gate is the one it was.
- **Negative / trade-offs accepted:** a basket from two catalogues becomes two orders
  with two numbers, two approvals and two progress views. A *requires* edge between
  products of different catalogues is not expressible: edges live inside a catalogue.
  If a product is required by one catalogue and also offered by a higher-ranked one,
  the two may land in different orders; the shop does not yet warn about that.
  If the second of two orders is refused, the first has been placed: the shop takes
  the placed products out of the basket and says what failed.
- **Follow-ups / risks to watch:** whether people want the two orders shown as one
  basket afterwards (a shared basket id); languages are those of the top-ranked
  catalogue, not the union; ADR-0347's argument against a stricter catalogue now reads
  "it adds the product for that audience" and should be revisited there.

## Pros and cons of the options

### Option 1 — one catalogue per person
- Good: no change.
- Bad: every product offered to several audiences is copied into every catalogue that
  outranks the one it belongs to; a narrower catalogue cannot add anything.

### Option 2 — several catalogues, one order per release
- Good: the order stays a snapshot of one release; the server needs one read-only
  route; fulfilment, approvals and inventory are untouched.
- Bad: a mixed basket becomes several orders.

### Option 3 — one order across releases
- Good: one order per basket, as the person sees it.
- Bad: the order record, its waves, its approval and the snapshot rule all change, for
  every order, to serve the mixed basket.

## Links

- supersedes decision 11 of [ADR-0312](0312-portal-catalogue-order-inventory.md)
  (one catalogue per person); the rest of ADR-0312 stands
- touches the reasoning of [ADR-0347](0347-product-eligibility.md), which builds on
  decision 11
- categories: [ADR-0360](0360-product-category.md)
