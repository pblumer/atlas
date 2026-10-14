# ADR-0384: A position is a product and the shape of it, and it says so in its own key

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-17

## Context

An order line was identified by its catalogue item id. That held for exactly as
long as one product could appear in an order once.

[ADR-0312](0312-portal-catalogue-order-inventory.md) describes a resolution the
basket makes: "the same service pulled in twice in different **variants** is a
conflict the orderer resolves". One resolution is to keep both — a black phone and
a silver one. The identity could not express it. Two lines with the same id
collapse in every map the order builds (the status map in `Next`, the lookup in
`Ready`), and an outcome reported for one lands on whichever the walk reaches
first. Silently, and on somebody's phone.

The item id is not only an internal key. It is a path segment on five routes, a
process variable the fulfilment model passes to every provisioning process, and
the join between a line and its approval assignment.

## Decision

**A line's identity is its product and the shape of it, derived rather than
stored:**

```
Key() = itemId                 where no variant was chosen
        itemId + "#" + variant otherwise
```

Everything that identifies a line within an order reads that key: `Next`,
`Ready`, `Apply`, `CancelLine`, `Returnable`, `Returning`, `ReturnProcessOf`, the
inventory grant, the approval lookup and the escalation.

**Derived, not stored, and not minted.** That is the whole compatibility story: no
order is migrated, no record gains a field, and a key computed today from a line
written a year ago is the same string it was. A minted id would have needed all
three.

**A caller may still name the product.** `ResolveLine` answers a product name
wherever the order carries one position of it — which is every order placed before
this existed — and **refuses** where it carries two, naming both keys. The
alternative was to take the first match, which is how a black phone is marked
delivered because a silver one was.

**`multipleAllowed` decides how many positions.** The catalogue already states
whether somebody may hold a product more than once. A second flag for the same
question would give the catalogue two answers to it. Two positions of one shape
are refused outright: they are indistinguishable by key, and "two black phones" is
a quantity, which this catalogue does not have.

**Processes learn `positionId`.** The fulfilment model passes it beside `itemId`,
which keeps naming the product because that is what provisioning provisions. A
process written before this keeps working, and stops working loudly — with a
refusal naming both positions — exactly in the case it cannot express.

## What this does not do

**The inventory still records one hold per person and product.** `GrantEntitlement`
replaces an entitlement already held, and the variant is an attribute of the hold
rather than part of its identity. So somebody who orders a black phone and a
silver one is provisioned twice, with the right variant each time, and is recorded
as holding one phone.

That is not introduced here — it is how a repeated order has always been recorded
— but this change makes it reachable in one order, so it is named rather than left
to be discovered. Making the entitlement identity `(principal, item, variant)` is
an engine state-model change with its own replay and history consequences
([ADR-0346](0346-entitlement-history.md)), and it belongs in its own decision.

## Alternatives

**A minted line id.** Cleanest separation of product from position, and it costs a
migration of every stored order, every route and the process contract, for a case
the derived key already answers.

**Composite fields instead of a key** — `{itemId, variantId}` as a pair everywhere.
Honest, and it makes every path segment, map key and process variable a structure.
The key is that pair, written down.

**Refuse the case.** One product once per order; two colours need two orders. It is
what the code did, and it is a limit the catalogue's own description does not have.
