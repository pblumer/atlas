# ADR-0469: An administrator can delete a catalogue for good, unless the server opts out

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-21
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0312](0312-portal-catalogue-order-inventory.md) gives the catalogue no delete:
"Nothing is ever deleted from a catalogue — items are withdrawn. An order placed years
ago and an entitlement still held both resolve through the item, and a store that can
delete one of those is a store that can orphan evidence."
[ADR-0376](0376-catalogue-maintenance-over-mcp.md) kept that answer for the MCP tools
and left one question open: whether a catalogue ever needs a delete, given that an
agent can file twenty draft products in a minute and nobody can clear them away.

The question has been answered by use, and not by an agent. On a development or test
installation somebody imports a catalogue, publishes it, orders from it to watch the
fulfilment run, and then wants to start over. Every such round leaves a catalogue, its
products, its releases, its orders and the holds the orders granted behind for good. A
leftover catalogue also keeps its rank, and ranks must be unique, so an old test
catalogue can refuse the next publish. Everything else Atlas imports — drafts,
information models, Panorama models, DMN models, deployments, repository templates —
can be deleted again; the catalogue cannot.

The rule of ADR-0312 is right for what it was written for: records an order and an
access review resolve through. It is wrong on an installation whose orders are tests.
The question: **how can the catalogue be deleted where that is wanted, and how does an
installation where the evidence matters keep it from being deleted?**

## Decision drivers

- A production installation must be able to refuse the delete of orders and held
  rights altogether, not merely put it behind a role.
- A development installation, where the purge is wanted, should need no configuration
  for it. This is the driver the switch's default was decided on: the people who asked
  for the purge run development installations, and asked not to have to define anything
  there — only where they do not want it.
- "Delete" has to mean everything the import and the test created, or the next round
  starts on leftovers: the catalogue, its products, the orders, the work still running
  for them and the holds they granted.
- Whoever confirms has to see what goes before it goes, in the place they confirm.
- No new engine event. The inventory is engine state (I4/I6); a purge that needed a
  fourth inventory event would be a change to the log for a development convenience.
- A purge that stops halfway must be repeatable, and must never leave running work
  nothing names any more.

## Considered options

1. **Keep the rule.** Withdraw products, empty the catalogue, take its audience away.
2. **A conditional delete for everybody who maintains the catalogue:** allowed only
   while nothing resolves through it — no order, no hold.
3. **An administrator's purge behind a start flag** that deletes the catalogue with its
   orders, cancels their running instances and takes their holds out of the inventory,
   after a preview and a confirmation by id — with the flag either
   - **3a. opt-in**, off by default, or
   - **3b. opt-out**, on by default.
4. **Option 3 that also erases the inventory's history** of the removed holds.

## Decision outcome

Chosen option: **3b, an administrator's purge behind an opt-out start flag**, because
it is the only one that serves the case that asked for it — import, order, start over —
with nothing to configure where it is wanted, while a server started with the flag stays
exactly as ADR-0312 describes it.

- **The switch.** `--catalogue-purge=false` (`ATLAS_CATALOGUE_PURGE=false`, Helm
  `atlas.catalogue.purge: false`, `api.WithoutCataloguePurge()`) switches the purge off;
  **without it the purge is on**, the same direction as `--catalogue`
  ([ADR-0434](0434-the-catalogue-can-be-switched-off.md)). The first implementation was
  opt-in (3a), on the argument that the safe reading of a missing flag is the server
  that cannot delete. The requester decided for opt-out: they run the installations that
  want the purge, and would rather state the exception than the rule. That moves the
  burden onto every production installation, which has to set the flag, and the record
  says so wherever the flag is described. The environment variable is read as strictly
  as `ATLAS_CATALOGUE`, so a mistyped `false` stops the start instead of leaving the
  purge on. And while the purge is on, **every start logs a WARN**,
  `event=server.catalogue_purge_enabled`, naming the flag that switches it off: an
  installation upgraded without the flag serves normally and would otherwise give no
  sign, so the log says it at every start until the flag is set, under a stable name an
  alert can match. A server with `--catalogue=false` does not write it, because its
  catalogue routes, the purge among them, are not served.
- **The routes.** `GET /api/v1/catalogs/{id}/deletion` and
  `GET /api/v1/catalog-products/{id}/deletion` answer the plan and change nothing, on
  any server, with `enabled` saying whether this one allows the purge.
  `DELETE /api/v1/catalogs/{id}?confirm={id}` and
  `DELETE /api/v1/catalog-products/{id}?confirm={id}` carry it out: 403 with the
  switch off, 400 unless `confirm` repeats the id. All four are the administrator's, carry
  the `Catalogue` tag, and go with the area when `--catalogue=false`.
- **What goes.** A catalogue takes its releases, its brand mark and every product whose
  home it is. A product that goes is stripped from every catalogue that stays, items and
  edges, with that catalogue's revision advanced. An order goes when it was placed
  against a release of the catalogue, or when every line of it is a product that goes.
  Every running instance the order recorded on a line, and every running instance whose
  `orderId` names an order that goes, is cancelled. Every hold of a product that goes,
  or granted by an order that goes, is revoked through the existing
  `IntentEntitlementRevoked`, as **corrected** and by the administrator — corrected
  because a purge knows nothing about whether the access ended, and returned would say
  it did. The import's memory of the deleted records is forgotten, so a later import
  asks about a record of the same id instead of treating it as edited here.
- **What changes and stays.** An order with other lines stays; its lines on a deleted
  product are withdrawn with a reason where they had not started, and its approvals and
  orchestration are told, as a cancelled order's are. The processes the products were
  bound to stay deployed. Another catalogue's published release keeps offering a deleted
  product until that catalogue is published again.
- **The order of the work.** Engine first (cancel, revoke, made durable before anything
  else is written, I2), then the kept orders, then the deleted orders, the stripped
  catalogues, the products and pictures, the releases, the import memory, and the
  catalogue last. A purge that stops halfway is run again: every step is idempotent, and
  the catalogue that names the rest is the last thing to go.
- **Every surface.** The Console's catalogue page shows an administrator a *Delete for
  good* card that reads the preview, lists what goes and the warnings, and enables its
  button only once the id is typed again. MCP offers `atlas_catalog_deletion`,
  `atlas_delete_catalog`, `atlas_catalog_product_deletion` and
  `atlas_delete_catalog_product`. The shop handbook says when to use it and when not.

### Consequences

- **Positive:**
  - A development installation can be cleared of a test catalogue and everything its
    orders produced, and the same document imported again starts from nothing — with
    nothing to configure.
  - A production installation started with `--catalogue-purge=false` is unchanged:
    nothing can be deleted, and the preview is all anybody learns.
  - No change to the log, the engine or `applyToState`: the purge uses commands that
    already exist, so recovery is the recovery that already holds.
- **Negative / trade-offs accepted:**
  - The purge deletes evidence ADR-0312 protects. That is its purpose on the
    installations it is for; the switch, the role and the confirmation are what keep it
    there.
  - **Opt-out puts the burden on production.** An installation upgraded into this record
    without setting the flag lets every administrator — and an agent calling the MCP
    tools with an administrator's credential — delete a catalogue with its orders and
    holds. The role and the confirmation by id remain; the switch is the only thing that
    takes the possibility away, and it has to be set.
  - The inventory's history keeps a *corrected* row for every hold removed, and the
    event feed announces every removal to its subscribers. Erasing those would need a
    new engine event (option 4) and is not done.
  - Revoking a hold in Atlas runs no deprovisioning: access a test granted in a real
    target system stays there. Cancelling an instance does not compensate what it did.
    The preview says both.
  - An order placed against the catalogue in the seconds between the plan and the delete
    is not in the plan. Its catalogue, release and products are gone, and its instance is
    left running. The purge is meant for an installation nobody is ordering on.
  - A line that is running when its product is deleted from an order that stays keeps
    its status; only a line that had not started can be withdrawn.
- **Follow-ups / risks to watch:** Production installations that upgrade without
  setting `--catalogue-purge=false`; the start WARN is how they show up. Whether anybody needs the history erased too
  (option 4). Whether the confirmation by id is enough for an agent, which can repeat an
  id as easily as read one; the switch is the real guard there.

## Pros and cons of the options

### Option 1 — keep the rule
- Good: nothing changes; no evidence can ever be lost.
- Bad: the case that asked for it stays impossible. Orders and holds of tests stay for
  good, and a leftover catalogue's rank can refuse the next publish.

### Option 2 — a conditional delete for maintainers
- Good: safe on production, because it refuses exactly where evidence exists.
- Bad: refuses exactly the case that asked for it — a catalogue somebody ordered from.
  Clearing it would mean deleting the orders first, which is option 3 by another route.

### Option 3 — an administrator's purge behind a start flag
- Good: does what was asked, on the installations it was asked for; no new event; the
  preview makes the cost visible before it is paid.
- Bad: a flag somebody may set wrongly. The role and the confirmation are the second and
  third line, not a substitute.

#### 3a — opt-in
- Good: a forgotten flag leaves a server that cannot delete, which is the safe mistake;
  an upgrade changes nothing on production.
- Bad: every development installation has to set it, and the people asking for the
  purge are the ones who run those.

#### 3b — opt-out (chosen)
- Good: a development installation needs nothing; the flag says the exception, the way
  `--catalogue=false` does.
- Bad: a forgotten flag leaves a production server that can delete orders and holds, and
  an upgrade grants that to every administrator until the flag is set.

### Option 4 — option 3 plus erasing the history
- Good: nothing at all left behind.
- Bad: a fourth inventory event, applied on replay on every installation, for a
  development convenience — and the one family ADR-0346 built to outlive retention
  would gain a way to be emptied.

## Links

- amends [ADR-0312](0312-portal-catalogue-order-inventory.md) — "nothing is ever deleted
  from a catalogue" now holds on a server started with `--catalogue-purge=false`
- answers the open question of [ADR-0376](0376-catalogue-maintenance-over-mcp.md)
- relates to [ADR-0434](0434-the-catalogue-can-be-switched-off.md) (the other catalogue
  start flag), [ADR-0346](0346-entitlement-history.md) (the history a revocation writes),
  [ADR-0416](0416-the-shop-shows-an-orders-open-tasks.md) (the instances an order records),
  [ADR-0467](0467-application-package-export.md) (the import's memory)
