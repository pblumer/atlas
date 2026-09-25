# ADR-DRAFT: A withdrawn ordered right goes back through its order

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-25
- **Deciders:** Atlas maintainers

## Context and problem statement

A recertification revoke ([ADR-0341](0341-access-recertification.md)) started the
product's deprovisioning with three variables: the product (`itemId`), the holder
(`recipient`) and a `reason`. It took the process from the catalogue as it stands
now, the path reconciliation built for a right nobody ordered
([ADR-0334](0334-reconciliation.md)).

For a right an order granted, which is the ordinary case in a shop, that path could
not complete:

- **The process could not find what it provisioned.** A provisioning names what it
  creates after the order, because the order is what it was started with. The
  deprovisioning of "Benutzeraccount intern" on the demo installation, for example,
  finds the account by a marker `atlas-auftrag:<order>/<position>` written at
  creation. Without the order it finds nothing, and a person has to type the
  address in.
- **The process could not report the outcome.** An ordered right leaves the
  inventory only when its line reaches `returned`
  ([ADR-0346](0346-entitlement-history.md)), and a line accepts `returned` only
  while it is `returning` — the guard that stops a provisioning worker from
  recording something as given back with nothing having run. A recertification
  never put the line into `returning`. The right therefore stayed in the inventory
  after the account was gone, and the next campaign asked about it again.
- **It could race a return.** Nothing looked at the order, so a revoke of a line
  the orderer was already giving back started a second deprovisioning against the
  same target.

## Decision drivers

- A withdrawn right has to end in the inventory, or the next campaign certifies
  something that is not there.
- A deprovisioning process is written once, for the return, and has to work for a
  withdrawal without a second branch for "no order".
- Two revocations of one right must not run at once.
- The existing path must keep working for a right no order stands behind.

## Considered options

1. **Keep the catalogue path; let the process cope.** Each deprovisioning process
   looks the order up itself (by recipient and product) and asks for the return.
2. **Pass the order's variables to the catalogue path.** Add `orderId` and
   `positionId` to what a revoke starts, but leave the line as it is.
3. **Go through the order's return.** When the right has an order and that order
   still carries its position, a revoke does what the orderer's return does: the
   line becomes `returning`, recorded as by the reviewer, and the process the
   order froze starts with the order, the position, the product, the recipient
   and the reason.

## Decision outcome

Chosen option: **3**.

- **An ordered right goes back through its order.** The inventory's record names
  the order (`OrderID`). If that order still exists, names the holder as its
  recipient and carries the position, the revoke runs `order.Returning` and the
  same start as `POST /orders/{id}/lines/{item}/return`, plus `reason`. The row's
  outcome is `return-started`.
- **The process is the order's, not the catalogue's.** For a return that is
  already the rule: what was granted is revoked by the process in force when it was
  granted. A withdrawal now follows it too, so the process that finds the account
  by the order's marker is the one that runs.
- **A return the order refuses decides nothing.** A line already going back, or
  still needed by something held beside it, is answered `409` with the order's
  reason, and the row stays undecided. It can be answered once the state has
  changed. Nothing is started, so a second deprovisioning cannot race the first.
- **Everything else is unchanged.** An adopted or legacy right has no order, and
  an order may have been deleted by retention long before the right ends; both take
  the catalogue's deprovisioning as before, outcome `deprovisioning-started`.

### Consequences

- **Positive:** a withdrawn ordered right ends in the inventory when its process
  reports the line returned, and the order shows who gave it back and why. The
  deprovisioning task appears under the order's position in the shop
  ([ADR-0416](0416-the-shop-shows-an-orders-open-tasks.md)), as a return's does.
- **Negative / trade-offs accepted:** a revoke of an ordered right now uses the
  process the order froze, which can differ from the catalogue's current one. That
  is the return's rule, and for the same reason. A reviewer whose withdrawal is
  refused has to come back to the row.
- **Follow-ups / risks to watch:** a right without an order still has no path that
  ends it in the inventory except reconciliation. That is not new, and it is not
  settled here.

## Pros and cons of the options

### Option 1: the process copes
- Good: no change to Atlas.
- Bad: every deprovisioning process carries the same lookup and the same call, and
  the order logic that decides whether a line may go back is duplicated in BPMN.

### Option 2: pass the variables, leave the line
- Good: the process can find what it provisioned.
- Bad: it still cannot report the line returned, because the line is not
  returning, so the right still stays in the inventory. It also still races a
  return already under way.

### Option 3: through the order's return
- Good: one path for giving an ordered right back, whoever asks. The guard against
  two revocations is the order's own.
- Bad: two ways to deprovision remain, chosen by whether an order stands behind the
  right.

## Links

- amends [ADR-0341](0341-access-recertification.md) (what a revoke does)
- relates to [ADR-0334](0334-reconciliation.md) (the catalogue path kept for rights no order stands behind), [ADR-0346](0346-entitlement-history.md) (a returned line ends an ordered right), [ADR-0312](0312-portal-catalogue-order-inventory.md) (the order's frozen processes)
- tests: `api/recertifyreturn_http_test.go`
