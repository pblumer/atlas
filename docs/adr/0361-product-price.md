# ADR-0361: A price is a sentence the catalogue writes, frozen like a rule and computed by nothing

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Atlas maintainers
- **Open question:** Whether a **total** is ever wanted — a basket's, or a bundle's.
  It cannot be had from what this decides: adding two prices needs a currency, a
  rate and a date, which are an installation's finance rules and not the
  catalogue's. Nothing here blocks that measure; it simply is not this one, and
  the string would become the human-readable face of a real money type rather than
  being parsed into one. It stays unbuilt until somebody names the surface that
  needs a figure the per-line ones cannot give them.
- **Question checked:** 2026-09

## Context and problem statement

There was **no price field anywhere in Atlas** — not on a product, not on an order
line, not on the approval surface. That blocked the stories directly:

> As an approver I want to see the costs when I decide.

An approver was being asked to approve a laptop without being told what it cost.

The measure was originally scoped as a price and cost model, with the product
owner's profitability stories attached to it. The decision that shapes everything
below was the answer to one question put to the product owner: **is the price
displayed, or charged?** The answer was *displayed*.

## Decision drivers

- The approver needs a figure at the moment of deciding.
- An approver's figure must not change afterwards, for the same reason the
  approval rule and the ceiling do not.
- Nothing about currency, rounding, exchange rates or effective dates belongs to
  a catalogue. Those are an installation's finance rules.
- A field nobody can read the intent of is worse than no field.

## Considered options

1. **A money type**: minor units plus an ISO currency code.
2. **A string**, written as the catalogue's maintainer wants it read.
3. **A number without a currency.**

## Decision outcome

Chosen option: **a string**, on the product, frozen into the release and copied
onto the order line.

### Why a string

Because it is displayed and never computed, and a string is the honest shape of
"this is what it says on the shelf". It carries what a price actually is in a
catalogue somebody maintains: `CHF 1'200.–`, `49.– / Monat`, `ab 10 Stück CHF
39.–`, `im Grundpaket enthalten`. None of those is a number, and all of them are
answers an approver can act on.

A number invites a total; a total invites two products in different currencies;
that invites a rate and a date. Every one of those is a decision belonging to an
installation's finance rules, and a catalogue that stored a number would have
started making them by implication, before anybody had chosen.

**The cost is stated rather than hidden: nothing can add these up.** That is
survivable because of how approvals are shaped — **one approval decides one
line**, so the one figure it shows is the one figure it needs. The basket has no
total, and the open question above is where that goes if it is ever wanted.

### Why it is frozen like a rule, although it is not one

A release freezes rules: the approval rule, the ceiling, the eligibility, the
process bindings. A price is not a rule — nothing branches on it.

It travels with them anyway, and the sentence is the same one: **an approver saw a
figure and decided on it.** A catalogue edit next week must not make the record
show a different figure than the one that was approved. So it is in the release
*and* on the order line, and the approval surface reads it **from the line** — the
line is the order's own record of what was decided on, and it is what a reader
sees years later. Reading it from the catalogue would give the same answer today
and a different one the day somebody edits a price, which is precisely when the
difference matters and nobody is looking.

### What publishing checks, and what it does not

One thing: a price that is present and blank. That is worse than saying nothing —
the portal renders an empty field where a figure belongs, and a reader cannot tell
"we do not say" from "somebody left it blank". Publishing refuses the second, and
the portal says the first out loud.

Nothing else is checked, because nothing else is knowable here. A catalogue cannot
say whether `1200` means francs or euros, and a validator that guessed would be
the money model arriving by the back door.

### Where it shows

- The **portal's** product details, as written, with a sentence where there is no
  price.
- The **approval** panel, and the approval **row** — a list of forty is scanned,
  not opened one at a time, and an approver who has to open each to compare costs
  is doing the thing the list exists to prevent.
- The **product editor**, with the explanation of why nothing sums them.

### Consequences

- **Positive:** the approver's story is answered with one field and no money model;
  a figure cannot change under a decision; a maintainer writes what the business
  already writes.
- **Negative / trade-offs accepted:** no totals, no sorting by cost, no comparison.
  Two products can say `CHF 50` and `50 CHF` and nothing notices.
- **Follow-ups / risks to watch:** a test asserts that no page parses a price into
  a number, because that is the one way this decision gets undone by accident — a
  single `Number(price)` somewhere is the whole money model, invented without being
  chosen.

## Pros and cons of the options

### Option 1 — a money type
- Good: totals, sorting, comparison; correct by construction.
- Bad: it decides currency, rounding and effective dates on the installation's
  behalf, and asks a catalogue maintainer for precision the shelf label does not
  have. It is the measure the product owner explicitly did not ask for.

### Option 2 — a string
- Good: exactly "displayed"; carries ranges, conditions and "included" as easily as
  a figure; no invented semantics.
- Bad: nothing computes; two spellings of the same price are two strings.

### Option 3 — a number without a currency
- Good: sortable.
- Bad: the worst of both. It looks computable and is not, and the first report that
  summed it would be adding francs to euros with nothing anywhere saying so.

## Links

- relates to ADR-0312 — the three models, and why the release is the frozen one
- relates to ADR-0344 — the ceiling, the other thing frozen so a later edit cannot
  retroact
- relates to ADR-0311 — the approver's own surface, which is where the figure is
  read
