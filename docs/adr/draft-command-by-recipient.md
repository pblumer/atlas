# ADR-DRAFT: A process commands what a person holds, without reading what they hold

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-03
- **Deciders:** Atlas maintainers

## Context and problem statement

A leaver process has to return the rights a person who leaves still holds: their admin
account, their licence, their memberships. [ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md)
§10 gave it the means — a shop send task in mode `command` asks a held position for an
action in the name of the process's application, when the product lists that
application in `commandedBy` — but the task names the position by its **order and
position**, and a leaver process does not know them. It knows the person.

The only route that answers which order a person's right came from is the inventory,
and reading somebody else's inventory is an administrator's alone
([ADR-0312](0312-portal-catalogue-order-inventory.md)); an API token never carries that role.
So a leaver process could not find the positions it was allowed to command.

Two further facts made the step brittle: a refused command was always a failure with
retries and an incident — no model could route it — and a person who holds nothing of
the product had no way to be "nothing to do".

## Decision drivers

- **The inventory stays the administrator's to read** (ADR-0312). A process learns no
  more about what a person holds than the command needs.
- **Authority stays on the product** (`commandedBy`, ADR-0429 §10.1).
- **"Holds nothing" is an answer**, not a failure: most leavers hold no admin account.
- **A refusal can be routed** by the model, as an order task's can (ADR-0520).

## Considered options

1. **Let an operator read anybody's inventory.** Simplest; widens what every operator
   token can see to everything everybody holds.
2. **The leaver publishes an "Austritt" message** each product's process listens for.
   No platform change, and new products need no change to the leaver — but whoever may
   publish the message may trigger returns, a weaker gate than `commandedBy`, and only a
   long-running product process can listen.
3. **A command addressed to a recipient**: the task names the person and the product;
   the server finds the position in the inventory itself.

## Decision outcome

Chosen: **option 3.**

```xml
<atlas:shopTask mode="command" product="ad-admin-konto" action="deprovision"
                recipient="=atlasKontoId" resultVariable="adRueckgabe"/>
```

- `recipient` replaces `order` and `position`; a task names one shape or the other,
  checked at deploy. The value is evaluated at the server's edge like the order task's.
- The server reads the recipient's entitlement of the product (one per person and
  product, ADR-0312) and commands its order and position — `itemId`, or
  `itemId#variantId` for a variant — through the same `commandLine` as before: the
  product must list the application, the action must be an operator's or a system's.
- **Nothing held** — or a right held without an order (adopted, legacy), which no
  order can be asked about — completes the task with `""` in the result variable.
- **A refusal** (not listed, not an operator action, not held, no such order) is thrown
  as the BPMN error `SHOP_COMMAND_REFUSED` when the process catches that code; uncaught
  it is the failure it always was. This applies to both shapes of the command task. A
  server error is always a failure.

### Consequences

- **Positive:** a leaver process returns what a person holds by naming products, under
  the products' own allow-lists, without any new read access.
- **Negative:** the leaver names each product it returns; a new product that must end
  at leaving is one more task (or one more entry in a multi-instance list) in the
  leaver. Option 2 would avoid that and remains possible beside this.
- **Follow-up:** ADR-0429 §10.3 (an operator action for every held position of a
  product) is still open; this record is its per-person sibling.

## Links

- extends ADR-0429 §10 (mode `command`); sibling of ADR-0520 (mode `order`)
- keeps ADR-0312 (the inventory is read by administrators)
