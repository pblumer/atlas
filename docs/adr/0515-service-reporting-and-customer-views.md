# ADR-0515: A product promises a delivery time, and three people may read how it was kept

- **Status:** Accepted
- **Implementation:** Partial
- **Date:** 2026-10-31
- **Deciders:** dbuchs7 (programme owner, TSH); the four choices (customer responsible = catalogue groups and line managers, delivery-time SLA, names permitted, owner) confirmed by the requester on 2026-10-31
- **Open question:** Should the working-day count skip public holidays, and from which calendar (federal, cantonal, per catalogue)?
- **Question checked:** 2026-10

## Context and problem statement

The catalogue states what a product is, what it costs and who must approve it. It does
not say how fast it will be delivered. Nobody can therefore tell whether the shop keeps
its promises.

The data to answer that already exists:

- the order records when it was placed;
- a line records when it was approved;
- the inventory records when the right began (`EntitlementValue.Since`).

What is missing is the promise itself, a report that compares the two, and a decision
about **who may read that report**. That last part matters most, because a report of who
holds what is a list of people's access rights.

Three readers ask for it, and each asks a different question:

| Reader | Question | Scope |
| --- | --- | --- |
| Product manager | Do my products arrive on time, and how many are held? | the catalogues they maintain |
| Line manager | What do my people hold and what have they ordered? | their direct reports |
| Customer responsible | What does our organisation hold, order and pay for? | one catalogue's audience |

This record is phase A of the tshopper programme
([plan](../planning/tshopper-program.md)). Catalogue and shop become a capability of
their own **inside** the single binary ([ADR-0011](0011-single-binary-distribution-and-web-ui.md)).

## Decision drivers

- **No new engine fact.** Everything to measure is already durable. A report that needed
  a new event would be a change to `applyToState` for the sake of a screen (I4).
- **Off the run loop.** Orders and holdings grow with the population, so they are read
  through `readOffLoop` or straight from the order store, never inside `Server.do`
  (ADR-0080, ADR-0239).
- **The promise in force when ordering.** A product manager who shortens a delivery time
  must not make last month's deliveries late after the fact. This is the same reason the
  price is frozen ([ADR-0361](0361-product-price.md)).
- **A name is a disclosure.** [ADR-0353](0353-product-usage.md) gave
  the product manager counts and no names, and that stays. Names are shown only to a
  reader with a stated relationship to the person, and every such read is recorded.
- **What Atlas cannot measure, it does not report.** Availability and incident response
  belong to monitoring and ticketing.

## Considered options

For the promise:

1. **`serviceLevel` on the product**: a name and a delivery time in working days, frozen
   into the release and copied onto the order line.
2. A separate SLA object in the catalogue that products reference.
3. Reuse `maxDays` (ADR-0344).

For when the clock runs:

1. **From approval, or from the order when no approval is owed, until the right begins.**
2. From the order until the right begins.

For the customer responsible:

1. **Directory groups named on the catalogue (`reportingGroups`).** Their members see the
   catalogue's audience.
2. A new global role `customer`, with an organisation attribute on every account.
3. Line managers only.

## Decision outcome

### 1. The promise

A product carries an optional `serviceLevel`:

- `name` — "Standard", "Express";
- `nameTexts` — the name per language, all or nothing like the category headings
  ([ADR-0412](0412-translatable-catalogue-headings.md));
- `deliveryDays` — a positive whole number of working days.

It is frozen into the release with the rest of the product. The order line copies it, so
a position is measured against the promise of the release it was ordered from. A product
without a service level is reported with its lead time and no verdict.

Option 2 is refused for now: a separate object pays off when many products share one SLA
that changes as a unit, and nothing yet asks for that. Option 3 is refused because
`maxDays` is how long a right may be held, not how long it takes to arrive.

### 2. The clock

The clock runs from the **later of the order's creation and the line's approval** until
the **start of the right** (`Since` of the entitlement the order produced).

Working days are Monday to Friday in the server's time zone, without public holidays
(see the open question). Approval is excluded because the customer's own organisation
decides it. Counting it would charge the supplier for a wait the customer controls.

The report states the approval wait separately, so it stays visible.

Each position gets one of these verdicts:

| Verdict | Condition |
| --- | --- |
| **met** | provisioned within `deliveryDays` |
| **missed** | provisioned after `deliveryDays` |
| **overdue** | still open and past `deliveryDays` |
| **open** | still open, within `deliveryDays` |
| **no promise** | the line carries no service level |

Rejected, cancelled, abandoned and skipped lines are not measured: nothing was
delivered, or nothing needed to be.

### 3. Who reads what

- **Product manager** — the delivery report over the catalogues they maintain:
  - per product: positions ordered, held now, median and 90th-percentile lead time,
    met, missed, overdue, approval wait;
  - filterable by period;
  - exported as CSV;
  - **no names**, as ADR-0353 holds.
- **Line manager** — every person whose account names them as `managerId` (ADR-0470).
  - What is shown: by name, what each person holds and every order placed for them,
    with its status and its verdict.
  - Scope: direct reports only. The account records one level, and Atlas does not
    invent a hierarchy ([ADR-0349](0349-ordering-for-others.md)).
- **Customer responsible** — a member of any group a catalogue names in
  `reportingGroups` (see *As built*: set by whoever may share the catalogue and in
  force at once, not frozen into the release).
  - What is shown: the organisation dashboard for that catalogue's audience, that is,
    every person to whom this catalogue is the one they see
    ([ADR-0347](0347-product-eligibility.md)). For those
    people: by name, what they hold, what has been ordered for them, the verdicts and
    the stated prices.
  - Export: CSV.
  - Who sets `reportingGroups`: the catalogue's maintainer, and it is frozen into the
    release like the audience.

**Every answer that carries a name writes an audit record:** reader, view, catalogue or
team, and number of people. The record is written before the answer is sent. A report
that cannot be audited is not answered.

### 4. Where it is computed

All three reports are computed from:

- the order store;
- the inventory, read through `readOffLoop`;
- the releases.

Nothing is cached and nothing is stored. All three routes are tagged `Catalogue`, so
`--catalogue=false` removes them ([ADR-0434](0434-the-catalogue-can-be-switched-off.md)).

The MCP surface follows ADR-0016:

- the product manager's report is a tool;
- the two reports with names are omitted from MCP. An agent working for somebody does
  not need a list of other people's rights to do it.

### Consequences

- **Positive:**
  - The product promise becomes checkable.
  - Customers and their managers see what they pay for.
  - The engine is not touched.
- **Negative / trade-offs accepted:**
  - Holidays count as working days until the open question is settled.
  - The line manager sees one level only.
  - A reader with names is a new kind of access, and the audit log grows with every read.
- **Follow-ups / risks to watch:**
  - A large installation computes the report on every call. If that proves slow, a
    maintained counter is the remedy, not a cache that may disagree.
  - Phase B (a separate binary) must carry these routes over unchanged.

### As built

- **TSH1 (2026-10-31):**
  - `catalog.ServiceLevel` (`api/catalog/servicelevel.go`) is on the product. The publish
    refuses a nameless promise and one outside 1 to 365 working days. It is copied into
    the release and onto the order line (`order.Line.ServiceLevel`).
  - The measurement lives in `api/servicereport`.
  - A done line is dated by the hold its own order produced: `Since` of the current
    entitlement, or of its history for a returned line.
  - Besides the verdicts above, the measurement adds three:
    - **awaitingApproval** for an open line whose approval is still owed;
    - **failed** for a line whose provisioning failed;
    - **unmeasured** for a delivered line whose hold no longer names this order.
      This happens where a later order re-granted the right. The report counts it
      as delivered at an unknown time rather than guessing a time.
  - The product manager's report is `GET /api/v1/catalog-products/delivery-report`:
    - a period over the order date, by default the last 90 days;
    - `?catalog=`, `?lang=` and `?format=csv` (semicolon-separated, with a byte-order
      mark);
    - Console card *Delivery and SLA*, MCP tool `atlas_catalog_delivery_report`.
  - Working days are counted in the server's time zone.
- **TSH2 (2026-10-31):**
  - **Reporting groups.** `Catalog.ReportingGroups` is set by whoever may share the
    catalogue: its owner or an administrator, as with members. An editor's change is
    refused.
  - **Deviation from §3.** The groups are **not** frozen into the release. Withdrawing
    access to names must take effect at once, not with the next publish. A catalogue
    document neither exports nor imports them, because they name this installation's
    groups.
  - **Routes.**
    - `GET /api/v1/shop/reports` says which tabs to draw.
    - `GET /api/v1/shop/team-report` lists the accounts whose `managerId` is the
      caller. Disabled accounts are left out.
    - `GET /api/v1/shop/organisation-report?catalog=` lists the people `catalog.Resolve`
      places in that catalogue. It answers 404 to a non-member, whether or not the
      catalogue exists.
  - **Contents.** Both report every right a person holds to a product the scope ever
    offered, with the price its order line carried, plus the positions and per-product
    counts. They answer as JSON, or as CSV of rights or positions.
  - **Audit.** Each answer logs `audit.report_names_read` before it is sent.
  - **MCP.** All three routes are omitted from MCP.
  - **Shop.** The tabs are *My team* and *Organisation*. The Console's catalogue page
    has a *Customer responsibles* section.

- **TSH3 (2026-10-31):** the tshopper shell, in the same binary.
  - **App drawer.** It names the catalogue and the shop together as *tshopper*.
  - **Catalogue app.** It gains a top bar with *Catalogues*, *Building blocks* and
    *Reporting*. *Reporting* is the delivery report on a page of its own.
  - **Building blocks.** `GET /api/v1/catalog-building-blocks` searches a content
    source and lists every page with the products that use it.
    - Products the reader maintains are named; others are counted.
    - Without parameters it answers the configured sources. The content-source list
      itself stays admin-only.
    - *Create product* makes a draft product through the existing save and link routes.
  - **Deviation from the plan.** The shop page does not carry the tshopper name. It
    keeps the brand of the reader's catalogue (ADR-0316), which is what a customer
    should see. The shop's *My team* and *Organisation* tabs (TSH2) are its dashboards.

- **TSH4 (2026-10-31):** the boundary between shop and engine.
  - **The contract.** `api/shopcontract` declares the inventory as an off-loop read
    view and the directory as accounts with groups and manager.
  - **The report service.** The four report routes moved into
    `servicereport.Service` (ADR-0147), which reaches the engine through that contract
    and an audit callback only. The server implements both in
    `api/shopcontract_adapters.go`.
  - **Guards.**
    - `TestTheShopsSideReachesTheEngineOnlyThroughTheContract` refuses an import of
      the engine, state, model or run loop in either package.
    - `TestServerFilesReachingIntoTheShopOnlyBecomeFewer` pins the 37 server files
      that still import `api/catalog` or `api/order`; the count may only fall.
  - **Open.** Moving those 37 files behind the contract is the remainder of the
    plan's A3. Starting a position stays on ADR-0425's routes.

## Pros and cons of the options

### Directory groups on the catalogue (chosen)
- Good: the organisation is already the catalogue's audience; no new account attribute.
- Good: the catalogue's maintainer, who knows the customer, decides who reads.
- Bad: a person in two catalogues' reporting groups sees two dashboards; that is
  intended, not a leak.

### A global `customer` role with an organisation attribute
- Good: independent of catalogues.
- Bad: invents an organisation model Atlas does not have, and a role a directory group
  already expresses.

### Line managers only
- Good: nothing new to configure.
- Bad: answers the team question, not the organisation's, and one level only.

## Links

- relates to [ADR-0312](0312-portal-catalogue-order-inventory.md),
  [ADR-0344](0344-time-bounded-entitlements.md),
  [ADR-0347](0347-product-eligibility.md),
  [ADR-0353](0353-product-usage.md),
  [ADR-0361](0361-product-price.md),
  [ADR-0387](0387-the-catalogue-against-the-standards-boundary.md),
  [ADR-0434](0434-the-catalogue-can-be-switched-off.md),
  [ADR-0470](0470-the-line-manager-is-on-the-account.md)
