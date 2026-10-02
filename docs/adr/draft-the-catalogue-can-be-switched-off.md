# ADR-DRAFT: The catalogue can be switched off, and switching it off removes a surface, not a semantics

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-02
- **Deciders:** Atlas maintainers
- **Open question:** Whether an operator who switches the area off while orders are still
  in fulfilment needs a refusal or a warning at start, rather than the incidents the
  fulfilment process then raises. Counting the open orders at start reads the order store
  whole, which grows with the population, and no installation has reported the case yet.
- **Question checked:** 2026-10

## Context and problem statement

Milestone K grew a second product inside Atlas: a service catalogue, a shop people order
from, orders whose fulfilment is orchestrated by shipped processes, and an inventory of
held rights with reconciliation, access review, conflicts and an event feed
([ADR-0312](0312-portal-catalogue-order-inventory.md), [ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md)).
By the time of this record it is 70 of the 426 routes, 18 MCP tools, two Console apps, two
views in other apps, a page of its own and four system processes.

Every one of those is served unconditionally. An installation that runs Atlas as a
workflow engine — the product it was before Milestone K, and still the product most
installations use — carries all of it: a shop link in every employee's drawer, a
`productmanager` role to explain, order routes any signed-in user reaches, agent tools that
maintain a catalogue nobody uses, and fulfilment processes filed into its system project.
None of that is a defect. It is surface an operator did not ask for and cannot remove, and
surface is what an attacker, an auditor and a confused employee all meet first.

The question: **how does an operator turn the area off, what exactly does "off" remove,
and how does it stay complete as the area keeps growing?**

## Decision drivers

- An upgrade must change nothing: the area stays on unless somebody says otherwise.
- Off must be complete. A route of the area that stays served because it was added after
  the switch is worse than no switch, because the operator believes it is gone.
- Off must lose nothing. Catalogues, orders and held rights are evidence kept for years
  ([ADR-0312](0312-portal-catalogue-order-inventory.md)); a switch that deleted or hid them
  irrecoverably would be a retention decision taken by a configuration flag.
- One `applyToState` (I4). Entitlements, action outcomes and the feed are engine state;
  what is applied from the log must not depend on a flag, or replay on a server configured
  differently from the one that wrote the log rebuilds different state.
- A running model behaves the same under either setting. A task without a handler does not
  fail, it parks silently — the failure [ADR-0411](0411-system-processes-call-atlas-directly.md)
  was written about.
- One way of saying "off" that matches the others: `--docs=false`, `--metrics=false`,
  `--vault=false`.

## Considered options

1. **A start flag that removes the area's surface** — routes, page, menus, MCP tools,
   starmap picture, system processes — and leaves the engine and the stores untouched.
2. **A start flag that does not construct the area at all** — no services, no stores, no
   shop task handlers.
3. **A runtime setting** an administrator toggles in the Console, consulted per request.
4. **Per-role hiding only** — leave everything served and rely on nobody holding
   `productmanager`.

## Decision outcome

Chosen option: **1, a start flag that removes the area's surface**:
`--catalogue=false` (or `ATLAS_CATALOGUE=false`, `atlas.catalogue.enabled: false` in the
Helm chart), `api.WithoutCatalogue()` in the package.

### What off removes

- **Every route tagged `Catalogue` or `Order`.** They are not mounted, so each answers the
  `/api/v1` catch-all's 404 — the answer an endpoint that never existed gets, which says
  nothing about what is behind it. The filter sits in `apiRoutes()` itself, so the mux, the
  OpenAPI document and the node descriptor read one answer.
- **The shop page.** `/shop.html` and its old address `/portal.html` answer a plain 404
  that says the shop is switched off, instead of serving a static page that renders and
  then fails every call it makes.
- **The Console's way in.** `/api/v1/info` carries `catalogue`; the drawer's Shop and
  Catalogue entries and the Reconciliation and Access review views are marked
  `feature: "catalogue"` and left out by the one predicate both menus filter by, and a
  bookmark into one of the views says the area is switched off. A field an older server
  does not send reads as on.
- **The MCP tools.** Tools marked `Catalogue` are neither listed nor dispatched. The
  in-process transport is built knowing the flag; the stdio adapter, a separate process,
  asks `/api/v1/info` at start and offers everything if it cannot ask — the server still
  refuses what it does not serve.
- **The event feed, pulled or pushed.** `GET /api/v1/events` and the feed's push
  subscriptions ([ADR-0433](0433-the-event-feed-is-pushed-to-a-cloudevents-endpoint.md))
  are routes of the area, and the push delivery does not run: what the feed says — who
  holds what — does not leave Atlas by either door. Every subscription keeps its cursor,
  so delivery resumes where it stood when the area is back; one whose cursor the feed's
  retention passed in between is switched off and says so, as it would be for any
  consumer that fell that far behind.
- **The starmap's catalogue, and the Modeler's product actions.** Both read the store
  directly rather than through a route of the area, so each is told separately: the
  starmap draws no catalogue or product, and `GET /api/v1/message-sources` lists no
  product action among the message names a model can wait for.
- **The shop's machinery in the system project.** The fulfilment process and the three
  approval processes, and the approval form, are not filed. Their ids stay protected
  platform processes, so one an earlier start deployed cannot be deleted.

### What off leaves alone, on purpose

- **The engine.** Entitlements, action outcomes and the feed are applied from the log
  exactly as before, and the feed is pruned by the retention sweep as before. The shop
  send tasks (`io.atlas.shop`, `io.atlas.shop.command`) keep their handlers, so a product
  process already running reaches them and completes.
- **The stores.** Catalogues, releases, orders, favourites, inventory loads, discrepancies
  and recertification campaigns stay on disk, read by nobody. Switching back on is a
  restart, and everything is there.
- **The guards that protect product processes.** Refusing to publish a catalogue product's
  message, to trigger its operation from outside the shop, and to delete a process a
  release binds all stay — they protect the engine from the area's data, which is still
  there.
- **The roles and token scopes.** `productmanager`, `feedreader` and the `events`,
  `reminders` and `inventory` scopes can still be granted; with the area off they reach
  nothing.
- **The Modeler's shop task element.** It is BPMN the compiler accepts and the engine runs;
  hiding it would make the same model authorable on one server and not on another.

### How it stays complete

The tag is the boundary because every route already has to carry one. Two more statements
of the same boundary live in `api/catalogueoptout_internal_test.go` and fail when they
disagree with it: the first path segment of every route of the area, and the package of
the handler (`api/catalog`, `api/order`). A route of the area that arrives under some other
tag is a failing test, and so is a new segment the list does not know. The system processes
held back are checked against what they call, and the MCP tools against the drift guard's
own tool→route classification. The tests that read the area from the OpenAPI document
read it whole, so they cover routes added after them.

### Consequences

- **Positive:** an installation without a shop can say so once and be believed by the
  API, the explorer, the Console, the agent tools and the starmap. The default changes
  nothing, and off is reversible without loss.
- **Negative / trade-offs accepted:** the route tags `Catalogue` and `Order` now carry
  meaning beyond the explorer's grouping. An order still in fulfilment when the area is
  switched off fails its next call to the order routes like any REST task meeting a 404:
  the job is retried and, its retries spent, becomes an incident, which can be retried
  once the area is on again. That follows from the REST worker's ordinary failure handling
  and is not separately tested here. Approval tasks already in an inbox stay there and can
  be completed; the approval process's call back then fails the same way.
- **Follow-ups / risks to watch:** the open question above. A server that switched the area
  off after deploying the shop's system processes keeps those definitions; nothing starts
  them, and removing them is a deliberate act this record does not automate. Static assets
  of the area (`shop.js`, `catalog-admin.js`) are still served: they hold no data and every
  call they make is refused.

## Pros and cons of the options

### Option 1 — a start flag that removes the surface
- Good: complete, reversible, invisible to replay; matches `--docs` and `--metrics`.
- Good: the engine's behaviour for a deployed model does not depend on the flag.
- Bad: a restart to change it; in-flight orders surface as incidents rather than being
  refused up front.

### Option 2 — do not construct the area at all
- Good: the strongest reading of "off", and the least code running.
- Bad: the shop send tasks would lose their handlers and park silently (ADR-0411); the
  guards that refuse to start or delete a product's process would lose the data they read;
  and roughly twenty call sites that reach the services would each need a nil guard, every
  one of them a place a future change can forget. Steelmanned, it is "off means nothing of
  it runs" — but what still runs is the engine executing models that were deployed, which
  is not the area's to switch.

### Option 3 — a runtime setting
- Good: no restart; an administrator can do it from the Console.
- Bad: every request of the area consults it, the route table and the OpenAPI document stop
  being fixed at mount, and the MCP tool list has to be re-derived per call. A runtime
  switch is also one an administrator account can flip, which is a weaker guarantee than
  the deployment's own configuration for an operator who turned the area off for
  attack-surface reasons.

### Option 4 — per-role hiding only
- Good: nothing to build.
- Bad: the shop is offered to `user`, which is everybody, and the order and inventory
  routes answer whoever holds a role; it hides nothing that matters.

## Links

- relates to [ADR-0312](0312-portal-catalogue-order-inventory.md) (the area this switches)
- relates to [ADR-0043](0043-openapi-spec-and-embedded-api-explorer.md) (`--docs`, the pattern)
- relates to [ADR-0411](0411-system-processes-call-atlas-directly.md) (why the shop tasks keep their handlers)
- relates to [ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md) (the feed and the shop tasks)
