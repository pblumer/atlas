# ADR-DRAFT: A precondition the recipient does not hold is added to the order

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-23
- **Deciders:** Atlas maintainers
- **Open question:** Whether a provisioning process can be relied on to succeed without
  acting when the right it grants is already present in the target system. § 3 rests
  on that for an inventory that is not yet complete. Nothing in the catalogue states
  it of a bound process, and no installation has reported how its processes behave.
- **Question checked:** 2026-10

## Context and problem statement

A `requires` edge says that one service cannot be provisioned before another
([ADR-0312](0312-portal-catalogue-order-inventory.md)). The release carries each
item's direct preconditions, and the fulfilment order is computed over them.

They are only honoured **within one order**. `requiresFor` in `api/order/service.go`
keeps a precondition only if the same order carries it. `api/order/advance.go` states
the consequence: "A precondition this order does not carry is not waited for: whether
the recipient already holds it is the provisioning process's question."

So the following order is placed, approved and started without objection:

- The Teams add-on `requires` a Microsoft 365 licence.
- Somebody orders the add-on for a recipient who holds no licence and has not ordered
  one.
- The order carries no licence line, so the add-on is not blocked, and its
  provisioning process is the first to notice — against the target system, after the
  approval.

What happens next depends entirely on how carefully that process was modelled. It
fails with an incident an operator has to read, or it reports success for a feature
nobody can use. Either way the orderer learns about it last, and could have acted on
it first. They could have ordered the licence in the same basket.

Atlas already knows everything needed to catch this at placement. The release holds
the preconditions, and the inventory holds what the recipient has: placement already
reads it for the basket's "already held" resolution (`linesFor`, ADR-0312).

The question arises with any catalogue. It is pressing for one built as a
construction kit ([ADR-0395](0395-product-construction-kit.md)), and especially when
services are maintained by product owners outside Atlas
([ADR-draft-service-content-from-an-external-source](draft-service-content-from-an-external-source.md)):
there, the product manager combining services is not the person who knows what each
one needs.

## Decision drivers

- **Catch a precondition before provisioning, not during it.** The orderer is the one
  person who can still fix a missing precondition cheaply.
- **Do not refuse people who do hold the right.** The inventory is complete only where
  a commissioning load attributed what exists
  ([ADR-0333](0333-inventory-commissioning-load.md)). A rule that trusts its absence
  absolutely refuses legitimate orders.
- **Never provision a second copy.** Placement already refuses when the inventory is
  unreadable, rather than ordering something twice, and the same care applies here.
- **Show, do not decide silently.** The basket's existing resolutions are shown to the
  orderer rather than decided for them (ADR-0312). Something that costs money and needs
  approval must be visible before it is ordered.
- **One rule for every client.** The shop is not the only way to place an order: an
  integration manager orders for others ([ADR-0349](0349-ordering-for-others.md)), and
  MCP and the API place orders too. The rule must hold on the server.

## Considered options

1. **Leave it to the lifecycle process** — today's behaviour.
2. **Refuse the line** whose precondition is neither carried nor held.
3. **Add the precondition to the order** as a position of its own, and resolve it like
   any other.
4. **Warn and place anyway.**

## Decision outcome

Chosen option: **3 — add the precondition to the order.**

### 1. The rule

At placement, for every line and every direct precondition of its item:

| The precondition is … | Then |
|---|---|
| carried by this order | nothing changes; the existing wave order applies |
| held by the recipient (inventory) | nothing is added; the line is not blocked |
| pending or running in another open order for the same recipient | the line is **refused**, naming that order (§ 4) |
| none of the above, and orderable for the recipient | it is **added** as a position of its own |
| none of the above, and not orderable for the recipient | the line is **refused**, naming the precondition (§ 2) |

The rule is applied transitively: an added precondition's own preconditions go
through the same table. That terminates, because publish proves the `requires` graph
acyclic (ADR-0312).

An added position is an ordinary line. It is approved under its own rule, frozen from
the release like the rest, and fulfilled in the wave before the line that needed it.
It records **why** it is there — "required by" the line that brought it in — so that
a reader of the order months later can tell a position somebody chose from one the
rule added.

### 2. When the precondition cannot be ordered

A precondition is not orderable for a recipient when the catalogue they see does not
offer it, or when its eligibility excludes them
([ADR-0347](0347-product-eligibility.md)). The line that requires it is then refused at
placement, and the refusal names the missing precondition and why it cannot be
ordered here. Placing the line anyway would start a provisioning that is known to be
unable to succeed.

### 3. The orderer sees it first

The basket applies the same rule before submission and shows an added position as
**required by** the line that brought it in, with its price
([ADR-0361](0361-product-price.md)). It cannot be removed on its own. Removing it means
removing the line that requires it, which the basket offers as one choice.

Where the precondition has variants, the orderer chooses one, as for any position. A
placement that names no variant for an added position with variants is refused, the
same way an ordered position with variants is.

The server enforces the rule regardless of what the basket did. A client that skips
the basket — the API, MCP, an integration manager's tool — gets the same additions
and the same refusals.

### 4. Why an open order refuses rather than adds

When the recipient's licence is still being provisioned by another order, adding it
again would provision a second copy, the one outcome placement already refuses for an
unreadable inventory. Waiting across orders is not possible either: an order waits
only on its own lines. The line is therefore refused with the open order's id, and the
orderer places it again once that order has settled. This is the rare case, and the
refusal says exactly what to wait for.

### 5. An inventory that is not yet complete

Where the inventory lacks a right the recipient does hold, the rule adds a position
that was not needed. It is shown, priced and approved, so it is not silent. When it is
provisioned, a process that finds the right already present in the target system
completes without acting. That is the property a provisioning process needs for every
retry anyway, and the open question above names the fact that this record relies on
it.

Refusing in that case (option 2) would block the order altogether, and only a
correction of the inventory would unblock it. Adding asks only for an approval that
turns out to be redundant.

### 6. What the lifecycle process still checks

This rule catches what Atlas can know from the release and the inventory. Whether the
target system really has the licence assigned is still only known to the target
system, and checking it remains the lifecycle process's business
([ADR-0425](0425-a-product-lifecycle-is-one-process-with-message-triggers.md)). The
comment in `advance.go` stays true for that case and is updated to say so.

### Consequences

- **Positive:** a missing licence is found in the basket, where the orderer can still
  act on it, instead of in an incident after approval. The rule needs no new data: the
  release already holds the preconditions, and placement already reads the inventory.
  It holds for every client alike.
- **Negative / trade-offs accepted:** an order can contain positions nobody explicitly
  chose. They are marked, priced and approved, but an approver now sees lines the
  orderer did not pick. An incomplete inventory produces redundant positions until a
  commissioning load fills it. A precondition in flight in another order refuses the
  line instead of waiting for it.
- **Follow-ups / risks to watch:**
  - If refusals for an open order turn out to be frequent, waiting across orders
    becomes worth designing, as a change to fulfilment rather than to this rule.
  - The approval page should show "required by" next to an added position, so the
    approver can see why it is there.
  - Returning a precondition is already refused while something still held requires
    it (ADR-0312). This record makes the ordering side symmetric to that rule.

## Pros and cons of the options

### Option 1 — leave it to the lifecycle process
- Good: nothing to build. The process is the only party that knows the target system,
  and the current comment in `advance.go` says so deliberately.
- Bad: the check runs last, after approval, and its quality depends on each modeller
  remembering it in each process. The orderer, who could have fixed it, hears about it
  last or not at all. Atlas has the data and does not use it.

### Option 2 — refuse the line
- Good: simple, explicit, never orders anything extra.
- Bad: it trusts the inventory's absences absolutely and refuses people who hold the
  right where the inventory is incomplete. Where the precondition is missing, it hands
  the orderer a refusal instead of the obvious fix, which is to order it too.

### Option 3 — add the precondition (chosen)
- Good: turns the failure into the fix. Builds on the basket's existing "held"
  resolution and the release's existing preconditions. Tolerates an incomplete
  inventory at the price of a visible, redundant position.
- Bad: orders things nobody clicked. Needs the "required by" link on a line, and a
  refusal for the case of a precondition in flight elsewhere.

### Option 4 — warn and place anyway
- Good: never blocks, never adds.
- Bad: a warning that is clicked away is today's behaviour with one extra click, and
  through the API there is nobody to warn.

## Links

- relates to [ADR-0312](0312-portal-catalogue-order-inventory.md) — `requires`,
  waves, the basket's "already held" resolution, and the refusal to return a
  precondition still required.
- relates to [ADR-0333](0333-inventory-commissioning-load.md) — why the inventory's
  absences are not trusted absolutely.
- relates to [ADR-0347](0347-product-eligibility.md) — when a precondition is not
  orderable for the recipient.
- relates to [ADR-0349](0349-ordering-for-others.md) — orders placed without the shop's
  basket.
- relates to [ADR-0361](0361-product-price.md) — the added position shows its price.
- relates to [ADR-0395](0395-product-construction-kit.md) — the construction kit,
  where precedence stays pairwise.
- relates to [ADR-0425](0425-a-product-lifecycle-is-one-process-with-message-triggers.md)
  — what the lifecycle process still checks.
- relates to [ADR-draft-service-content-from-an-external-source](draft-service-content-from-an-external-source.md)
  — the record this one was split from.
