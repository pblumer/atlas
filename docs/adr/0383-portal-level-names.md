# ADR-0383: A position's level is read from the graph once, and a root with no parts is not a bundle

- **Status:** Accepted (amended 2026-09-17 — the Bundle level is withdrawn)
- **Implementation:** Landed
- **Date:** 2026-09-17

## Amendment, 2026-09-17: there is no Bundle level

**The first half of this record stands and the second is withdrawn.** One function
decides the level and every view reads it — that was the defect worth fixing and it
stays fixed. What is withdrawn is the answer that function gave.

A bundle is offered as a **Marktleistung**. It holds the orchestration process; the
services behind it hold their own provisioning and deprovisioning, and a service may
stand behind several Marktleistungen, included or optional, always with the same
processes. So there is nothing for a Bundle level to name: every root is a
Marktleistung, with or without parts, and everything behind one is a service however
deep it sits.

```
levelOf(release, id) = depth(id) == 0 ? offering : service
```

That is the whole rule now. It reads depth and nothing else — not the kind of edge,
not whether the product has parts, not how it was reached.

**Why this is better than the rule it replaces, and not merely different.** The
original decision here fixed a real defect by answering a question: is this root a
bundle or an offering? The answer was correct and the question was the problem. A
catalogue that has to decide, per product, which of two words describes it will get
that decision wrong for every product somebody adds a part to later. Withdrawing the
level removes the question instead of answering it, and nothing downstream needed
the distinction: the order, the release and every provisioning call name items, not
levels.

**The cascade absorbs the vacated column.** It reads Kategorie › Produktgruppe ›
Produkt › Services. The two upper columns are attributes a product writes on itself;
the two lower ones are read off the containment graph. The cascade therefore stops
calling `levelOf` at all — its columns *are* the levels, and asking it to re-derive
what it just laid out would be the second answer this record exists to prevent. The
basket and the list of what somebody holds still ask, because they hold a set of
positions with no layout to read a level off.

**What the alternatives section below says about a stored level still holds**, and
applies to the product group as well: it is a string the product writes on itself,
with the costs ADR-0360 states and accepts.

## Context

The portal draws the catalogue as four columns — Kategorie, Bundle, Marktleistung,
Service — and Atlas has no such typing. An item is an item; the hierarchy is the
containment graph, of any depth, built from compositions and aggregations
([ADR-0312](0312-portal-catalogue-order-inventory.md)). So the column a product
appears in has to be *derived*, and two views derived it independently.

The cascade used depth: a product nothing contains is a bundle, what it directly
contains is a Marktleistung, what those contain is a Service. The basket used
**kind**: what was chosen went under BUNDLE, what came with it went under SERVICE.
The two agree on nothing in the middle. Ordering a package put its integral part
under MARKTLEISTUNG in one half of the screen and under SERVICE in the other —
one position, two names, and no way for a reader to tell which one is the product's.

Underneath that, the cascade's own rule was wrong at the root. Every item nothing
contains was called a bundle, including an item with nothing inside it. A single
product was announced as something made of other things.

## Decision

**One function decides the level, and every view calls it** — the cascade, the
basket, and the list of what a person already holds, which was deriving depth for
itself and agreed with the cascade everywhere except the case this rule changes.

```
levelOf(release, id):
  hasParts = id has any composition or aggregation part
  up       = the item that directly contains id, if any
  if no up:  hasParts ? bundle : offering
  else:      (up itself has a parent) ? service : offering
```

Three consequences worth naming:

- **A root with no parts is an offering, not a bundle.** A bundle is a thing made
  of other things. The word is the only thing the column says about a product, and
  it has to be true of it. *Offering* (Marktleistung) is what the catalogue's own
  vocabulary gives a thing that is offered on its own.
- **Both kinds of containment count.** Whether a part can be taken out of the whole
  is a different question from whether it sits inside it, and this rule is about
  where it sits. An aggregation is as much a part as a composition here.
- **Depth beyond the third level keeps the third name.** The screen has three
  columns, a deeper graph has to land somewhere, and *service* is true of it.
  Inventing a fourth word for a shape nobody has drawn would not be.

The basket's columns are the three levels. Whether a position can be taken out is
answered **per row**, by the control it carries: a disabled "−" for something that
came with the whole, an "X" for something that was chosen. That is what the two
views were confusing — kind read as level.

The cascade's first column still holds every product nothing contains, because it
is where a person starts and moving some of its rows elsewhere would take that
away. A row there whose level is not *bundle* carries its level beside it.

## Alternatives

**Rename the first column to something neutral.** Smallest change, and it removes
the false statement without adding a rule. Rejected because it removes the three
names the screen is organised around and answers nothing about the basket, which
is where the two halves actually disagreed.

**Make the level a field on the product.** Then nothing is derived and nothing can
disagree. Rejected for the reason ADR-0312 did not introduce one: the level is a
fact about the graph, a stored copy of it goes stale the moment an edge moves, and
the catalogue would then have two answers to one question — which is this defect,
made durable.

**Leave the cascade alone and only fix the basket.** Half the acceptance: the same
position would carry the same name in both views, and a single product would still
be announced as a bundle.

## Consequences

Display only. No stored release changes, no API changes, and no order in flight is
affected: the level has never been written to an order line, a release, or a
provisioning call. A release published before this renders under the new names
because the names were always computed at render time.
