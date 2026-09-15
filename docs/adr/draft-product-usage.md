# ADR-DRAFT: The catalogue answers forwards, and the person who maintains a service asks backwards

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Atlas maintainers
- **Open question:** Whether "who holds this" should ever become a list rather than
  a count. A maintainer retiring a service arguably needs to reach the forty people
  affected — but reaching them is a *campaign*, and recertification already is one,
  with a record of who decided. A list here would be the same disclosure with no
  record attached, which is why it is a count until somebody shows a use the
  campaign path cannot serve.
- **Question checked:** 2026-09

## Context and problem statement

Every question the catalogue answers runs forwards. A product names what it
contains, what it needs, what it excludes; the portal renders that, the basket
resolves it, and the fulfilment schedule is computed from it.

The person who *maintains* a service asks the opposite question, and could not ask
it at all:

- Where is this used — which wholes carry it, and integrally or optionally?
- **What needs it?** Nobody reading the VPN's own page learns that the laptop
  cannot be provisioned without it.
- What may it never be held with?
- How many people have it, and did this portal grant them or merely find them?

A product manager about to retire a service, rebind its provisioning or move it
between catalogues had no way to find out what they were about to break.

### Why this is a read and not a model change

The answer has been in the store since the first release was published. `Includes`,
`Options`, `Requires` and `Excludes` are all frozen into every release; walking them
the other way needs no new data, no migration and no second source of truth. This
is the cheapest measure in the whole plan for exactly that reason, and it serves
four user stories.

## Decision

`GET /api/v1/catalog-products/{id}/usage`, at the `productmanager` role.

### Merged across catalogues, not answered per catalogue

A service does not belong to a catalogue. The same product carried by two of them
is **one thing** a maintainer is about to change, and a per-catalogue answer would
let them fix one estate and break another without ever seeing the second. This is
the same argument the conflict report makes, and for the same reason: a person
holds products, not catalogues.

Only each catalogue's newest release is read. An older one is what the catalogue
*used to* say, and somebody planning a change needs what it says now.

### Composition and aggregation stay apart

Retiring the two has different consequences: an integral part cannot be removed
without changing what the whole *is*, an optional one can. A view that merged them
would tell a maintainer the same thing about two different situations — which is
the conflation `catalog.Edge` was split to prevent.

### Holders are counted and never listed

A list of the people holding one service is the inventory filtered to the
interesting part, which is the disclosure the inventory routes are gated for. A
maintainer does not need to know who Ada is to know that forty people would be
affected.

The count is broken down by origin, because that decides what can be done: an
ordered right can be returned through its order, an adopted or legacy one cannot.

### It is an MCP tool, unlike its siblings

Every other read this line of work added was withheld from the tool surface, and
each for the same reason: it is other people's access. This one names **no
person** — item ids, catalogue ids and a number — and the question it answers
("what breaks if I retire this") is one an assistant is good at and a human is slow
at. Withholding it would have meant writing an omission whose honest reason was
that it would be fine.

## Consequences

**A product carried by nothing still answers.** `offeredBy` is populated for a
product nothing contains, because it is offered on its own and saying otherwise
would hide it from the person maintaining it.

**An unknown product is a 404 and not an empty report.** "Nothing uses this" and
"this does not exist" are different answers, and an empty report reads as *safe to
retire* — which is precisely the wrong thing to tell somebody who mistyped an id.

**Everything is sorted.** A map has no order, and two reads of an unchanged
catalogue that disagreed would read as a change nobody made.

**It does not answer "instantiated per customer".** The user story asks for that and
Atlas has no customer: a catalogue is the closest thing, and `offeredBy` plus the
holder count is as far as the data reaches. Naming the gap is the honest answer;
inventing a customer from the catalogue would not be.
