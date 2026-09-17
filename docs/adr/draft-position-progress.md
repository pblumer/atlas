# ADR-DRAFT: The orderer is told where their position stands, from a route of their own

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-17
- **Deciders:** Atlas maintainers
- **Open question:** whether the same route should also answer for the order as a whole — the orchestration's own step — rather than only per position
- **Question checked:** 2026-09

## Context and problem statement

Somebody who ordered something can read their order: every position, and the
status each one reached. What they cannot read is *where* a position is. "Läuft"
says a process has it; it does not say whether it is waiting for a superior's
approval or being provisioned.

[ADR-0384](0384-order-position-key.md) gave every position a key, and the portal
now offers a link from a position into the process working on it. That link is an
operator's, and not by accident:

- every route that finds or opens an instance is `RoleOperator` — `GET
  /api/v1/instances`, `/instances/search`, `/instances/{key}/timeline`;
- the console's instance view shows the whole engine state of the instance,
  variables included, and a process holding two people's data holds both.

So the question is not "how do we let the orderer through that door". It is
"what is the answer they actually asked for", which is smaller than what is behind
the door.

## Decision drivers

- An orderer needs no operations surface to learn where their own order is.
- Nothing belonging to anybody else may leave through a route gated on a role that
  most accounts have.
- Whatever the answer costs, it is paid per position of somebody's own order — not
  by the engine, and not by every reader of the page.

## Considered options

1. **Widen the instance routes to the order's owner.** One gate change, no new
   route — and it hands out the instance's variables along with the step.
2. **Record the instance key on the order line when fulfilment starts it.** A point
   read afterwards, and the cheapest possible answer. It changes the fulfilment
   contract: the model would have to report the key it got back, and every order
   placed before that reports nothing.
3. **A portal route of its own, gated on owning the order, that finds the instance
   server-side and answers with the step and nothing else.**

## Decision outcome

Chosen option: **3**.

```
GET /api/v1/portal/orders/{id}/lines/{position}/progress
Role: user, gated on owning the order
→ {"state": "active" | "none", "steps": ["Genehmigung durch Vorgesetzten"]}
```

- **Ownership, not role.** The caller is the order's orderer or its recipient.
  Anybody else gets **404** and not 403: whether somebody else's order exists is
  not something this confirms. It is the answer reading the order itself gives.
- **No process variable.** The process carries the recipient, the orderer, the
  order id and the approval reference. The caller already knows their own order,
  and this route says *where*, not *what*.
- **`{position}` is the position key**, resolved with `order.ResolveLine` like
  every other line route, so naming a product the order carries twice is refused
  rather than guessed.

### How the instance is found

By the two variables the fulfilment model passes when it starts one — `orderId`
and `positionId` — and by both. The order id alone also matches the order's own
orchestration and every other position of it; a position key alone is unique only
inside one order, and "laptop" is the first position of thousands of orders.

**Only live instances are walked.** A position's process is whichever process the
product named, so there is no definition to scope a search to and no value index
to seek in — the alternative is the content walk the operator search makes, over
the retained history as well. Bounding it to the active family bounds the cost by
the work actually in flight rather than by everything the store has ever run, and
a finished instance has no step to report.

### Two states and not three

`"none"` covers both "not started yet" and "no longer running". The third word —
"finished" — would cost a walk of the retained history on every call to
distinguish an instance that ended from one retention has since deleted, and it
would tell the reader nothing: whether the position was provisioned, rejected or
withdrawn is its own status, on the same row, already on their screen. What this
route adds is the step, and a position nothing is running for has none.

### The step is a name

Element names are read from the deployed BPMN document and cached per definition
key, which an installation never reissues
([ADR-0339](0339-the-definition-key-space-never-goes-backwards.md)). The compiled process keeps a name only
for user tasks — it is what a task inbox needs — and the step an order is sitting
on is as often a service task or a catch event. An unnamed element is answered
with its BPMN id: it says less, and it is still where the position is.

What holds no token of its own is left out — a subprocess scope, a multi-instance
body, an armed event-subprocess start, a boundary event. Reporting one would
answer "where are you" with the room rather than with the desk.

## Consequences

- A reader with no role beyond `user` sees which step their own position is on,
  and the operator's link stays exactly where it was, for the readers who have it.
- The walk is the route's cost and it is bounded by the live instance population.
  A portal that drew this for every row of every order would pay it per row, so
  the page asks when somebody presses, not when it loads.
- A process that stores the order id as something other than a string is not
  matched. That is deliberate: guessing at a rendering would match things that are
  not the order.
