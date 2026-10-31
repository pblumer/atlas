# tshopper (TSH) delivery programme — phase A

**Programme ID:** TSH.  
**Baseline:** `pblumer/atlas` main, reviewed 2026-10-31.  
**Strategic track:** [Milestone K — Service catalogue, orders & the portal](../../ROADMAP.md#milestone-k--service-catalogue-orders--the-portal-).  
**Owner:** dbuchs7 (named by the requester on 2026-10-31).  
**Operational status:** [programme manifest](../programs/tsh/program.json).  
**Architecture contract:** [ADR-0515](../adr/0515-service-reporting-and-customer-views.md).

## Outcome

The service catalogue and the shop become a capability of their own, called **tshopper**
(working title). Atlas remains the engine that fulfils what tshopper sells, and
wikiwikibangbang remains the place where the product owner describes a service.

Phase A draws that boundary **inside the single Atlas binary**. It also gives every
party the reporting it lacks today:

- the **product manager** sees offering, consumption and delivery-SLA reporting for the
  catalogues they maintain;
- a **line manager** sees what their direct reports hold and have ordered, with names;
- a **customer responsible** sees what their organisation holds, has ordered and what
  it costs. This is a person named per catalogue through a directory group. The view
  shows names and exports as CSV.

Phase B moves tshopper into a binary of its own, and Phase C optionally moves the
inventory there. Neither is part of this programme. Each needs its own decision once
Phase A is in use, because Phase A is meant to be a stable place to stop.

**End-to-end proof:**

1. A product manager gives a product the service level "Standard, 3 working days" and
   publishes.
2. Two people order it. One order needs approval; the approval time does not count
   against the delivery promise.
3. One position is provisioned within the promise and one after it.
4. The product manager's report shows one met and one missed position. The customer
   responsible of that catalogue sees both people by name. The line manager of one of
   them sees only that person.
5. A person who is neither of these three is refused all three reports.
6. Every read of a report with names is recorded in the audit log.

## Baseline (what exists)

| Surface | Today | Gap |
| --- | --- | --- |
| Product commitments | price, approval rule, `maxDays` ceiling on a held right (ADR-0344) | no delivery promise or service level |
| Order line timing | order `createdAt`, line `approvedAt`/`decidedAt`, instance `startedAt` | a line records no "provisioned at"; that moment is the entitlement's `since` |
| Product-manager reports | approver report, fulfilment report, translation gaps, product usage (counts, ADR-0353) | no consumption over time, no lead time, no SLA |
| Line manager | `managerId` on the account, used for `superior` approvals and escalation (ADR-0470) | no view of the team's holdings or orders |
| Customer organisation | catalogue audience groups (ADR-0347) | no role that may see an organisation's holdings |
| Wiki | products bind to wikiwikibangbang pages (`api/contentsource`) | building blocks are not browsable as a list in the catalogue editor |
| Switching off | `--catalogue=false` removes the whole surface (ADR-0434) | — |

## Slices

| Slice | Content | Depends on |
| --- | --- | --- |
| **TSH0** | This charter, the manifest, the ROADMAP entry and the ADR draft | — |
| **TSH1** | Service level and delivery days on a product, frozen into the release and copied onto the order line. A delivery report for product managers: orders, holdings, lead time, SLA met, overdue, CSV. Console dashboard, MCP tool. | TSH0 |
| **TSH2** | Customer views: the line manager's team view, and the catalogue's customer-responsible groups with an organisation dashboard (names, CSV). Every read with names is audited. Shown in the shop. | TSH1 |
| **TSH3** | tshopper shell in the same binary: the shop page carries the tshopper name and the app switcher, and its dashboards sit beside Shop, My services and My orders. Wiki building blocks are listed in the catalogue editor. | TSH2 |
| **TSH4** | The boundary contract: one Go package the order side uses to reach the engine, so that the routes outside `api/catalog` and `api/order` stop importing both packages wherever a contract call suffices. Documented against TMF620/622/637 (ADR-0387). | TSH1 |

## Constraints

- All six engine invariants hold. **No slice adds an engine event or touches
  `applyToState`.** Reports read the order store and the inventory **off the run loop**
  (ADR-0080, ADR-0239).
- The service level is frozen into the release, like the price (ADR-0361). The SLA a
  position is measured against is the one in force when it was ordered.
- A name in a report is a disclosure. It is shown only where the ADR permits it, and
  each such read writes an audit record.
- `--catalogue=false` removes every new route and page, and the switch test keeps it
  that way (ADR-0434).
- Each surface ships complete: route table, MCP classification, handbook, CHANGELOG,
  e2e spec and every locale.

## Non-goals

- A second binary, a second store or a network protocol between shop and engine
  (Phase B).
- Availability, incident-response or other SLAs that Atlas cannot measure. They need
  an import from monitoring or ticketing, decided separately.
- Public holidays in the working-day count. Monday to Friday is counted, and holidays
  are an open question in the ADR.
- Cost allocation to a finance system. Prices are shown as the catalogue writes them,
  never computed (ADR-0361).

## Immediate next action

Merge TSH0, then start TSH1 test-first: the product's `serviceLevel` survives a
publish and a release read, and is copied onto the order line.
