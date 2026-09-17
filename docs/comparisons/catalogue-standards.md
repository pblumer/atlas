# The catalogue against the standards

This document maps Atlas's catalogue, order and inventory models onto the external
standards that cover the same ground, and lists the standards examined and found
inapplicable. It exists because
[the record on the catalogue and the standards boundary](../adr/0387-the-catalogue-against-the-standards-boundary.md)
committed to it: the catalogue was built without the comparison, and a design whose
prior art is unnamed cannot be told apart from one that never looked.

> [!IMPORTANT]
> **This is not a conformance claim.** Atlas does not implement TMF620, TMF622 or
> TMF637, serves none of their resources, and has run no Conformance Test Kit. Nothing
> here is verified by a test. The comparison is at the level of **concepts and
> resource names**, not field by field against the specifications, and where a name
> was not verified against a primary source it says so.
>
> **Checked:** 2026-09 · **Atlas side:** `api/catalog`, `api/order` at that date ·
> **Standard side:** TM Forum Open API resource documentation, Open Service Broker API
> v2.16, Apache Syncope and Evolveum midPoint product documentation.

## Why these and not others

The catalogue is an **internal service catalogue**: somebody orders a workplace, a
licence or an access right, an approver decides, processes provision it in dependency
order, and a record says who holds what. That shape rules most catalogue standards out
before the comparison starts — they describe trade items, procurement documents or web
merchandising, none of which is the problem. What is left splits in two: the commercial
catalogue/order/inventory triple (TM Forum), and identity governance (Syncope,
midPoint).

**What this does not repeat.** [Microsoft Identity Manager and Atlas](mim.md) already
compares MIM's *connector surface* against Atlas Worker Types, and notes in passing
that an Atlas process must model the reconciliation logic MIM performs declaratively.
That is the integration layer. This document is about the **data model** — catalogue,
order, inventory, entitlement — which no existing comparison covers.

## 1. Catalogue — Atlas and TMF620

TM Forum's **TMF620 Product Catalog Management** is the closest external description of
what `api/catalog` holds.

| Atlas (`api/catalog`) | TMF620 | Note |
|---|---|---|
| `Catalog` (`id`, `texts`, `languages`, `items`) | `Catalog` | Atlas's catalogue is also the audience and theme boundary; TMF620's is not. |
| `Item` — product or service **by position in the graph** | `ProductOffering` and `ProductSpecification`, with `isBundle` | One Atlas entity against two standard ones. A deliberate divergence; see §6. |
| `Edge{Kind: composition}` — integral, never deselectable | `bundledProductOffering` with `bundledGroupStatement` | The standard expresses mandatory vs. optional bundling through the group statement rather than through two edge kinds. |
| `Edge{Kind: aggregation}` — optional, separately orderable | `bundledProductOffering`, optional | As above. |
| `Edge{Kind: requires}` — `From` cannot be provisioned before `To` | `ProductSpecificationRelationship` with `relationshipType: dependency` | Same concept. Atlas additionally computes fulfilment waves from it; the standard states the relationship only. |
| `Edge{Kind: excludes}` — never held by the same person | `ProductSpecificationRelationship` with `relationshipType: exclusivity` | Same concept, arrived at independently (ADR-0342 reaches it from separation of duties). |
| `State` — `draft`, `active`, `withdrawn` | `lifecycleStatus` | The standard's list is longer (In study, In design, In test, Active, Launched, Retired, Obsolete, Rejected). Atlas's three are a deliberate reduction. |
| `Lifecycle{From, Until}` | `validFor{startDateTime, endDateTime}` | Same concept, same purpose. |
| `Variant` (`id`, `texts`, unordered) | `ProductSpecificationCharacteristic`, or a plan-like offering | Atlas's variant carries no characteristic structure; configuration is delegated to a form. See §4. |
| `price` (a string, ADR-0361) | `ProductOfferingPrice` (amount, currency, tax, recurring period) | The standard is substantially richer. Open question; see §7. |
| `category` (a string on the item, ADR-0360) | `Category` as an entity with a hierarchy | The standard is richer. Open question; see §7. |
| `keywords` (ADR-0355) | no direct equivalent | Atlas-specific, for browser-side search. |
| `targets` (`TargetRef{system, ref}`) | no equivalent | Belongs to identity governance, not to a commercial catalogue. See §5. |
| `eligible`, `Catalog.groups`, `Catalog.rank` | no equivalent | Audience resolution is Atlas's own; TMF620 has market and channel concepts that solve a different problem. |
| `provisionProcess`, `deprovisionProcess` | no equivalent | The standard does not bind fulfilment to a process. See §4. |
| Release / `Publish` (a frozen, versioned catalogue) | `Catalog` versioning and `lifecycleStatus` | Atlas freezes a release so a pending order cannot be edited underneath it (ADR-0312). The standard has no equivalent immutability guarantee in the resource itself. |

**The relationship types are the notable find.** `dependency` and `exclusivity` are two
of the four `relationshipType` values TMF620 defines (the others being `migration` and
`substitution`), and they correspond to Atlas's `requires` and `excludes` edges. Atlas
has no equivalent of `migration` or `substitution`; a withdrawn item's replacement is
not modelled.

## 2. Order — Atlas and TMF622

**TMF622 Product Ordering Management** covers the order.

| Atlas (`api/order`) | TMF622 | Note |
|---|---|---|
| `Order` (`id`, `releaseId`, `orderer`, `recipient`, `lines`) | `ProductOrder` | Atlas's order is a BPMN process instance; the standard's is a resource. |
| `Line` | `ProductOrderItem` (`[1..*]` per order) | Same decomposition. |
| `Status` — `running`, `completed`, `partial`, `unfulfilled`, `cancelled` | `state`, of `ProductOrderStateType` (Acknowledged, Rejected, InProgress, Pending, …) | Both have an order state and a per-item state; the value lists differ and were not compared item by item. |
| `LineStatus` — `pending`, `running`, `done`, `skipped`, `failed`, `rejected`, `abandoned` | `ProductOrderItem.state`, of `ProductOrderItemStateType` | Atlas's `skipped` (the recipient already holds it) and `abandoned` (a failure nobody will repair) have no obvious counterpart. |
| `releaseId` pinning the order to a frozen catalogue | no equivalent found | Atlas's answer to "the catalogue changed while the approval was pending". |
| `waves`, `requires` copied onto the order | no equivalent | The standard relates specifications; it does not carry a computed fulfilment schedule on the order. |
| `Assignment`, `Escalation` (approval routing and its history) | no equivalent | Approval is Atlas's own; it belongs to the process layer. |

Atlas's order is deliberately **not** a stored state machine — the process instance is
the order, and its standing is derived from its lines rather than stored (ADR-0312).
That is a real divergence from a resource-shaped standard, and it is the one that would
matter most if an export were ever built.

## 3. Inventory — Atlas and TMF637

**TMF637 Product Inventory Management** covers what a party holds.

| Atlas (`api/order`, `Grant`) | TMF637 | Note |
|---|---|---|
| `Grant{Principal, ItemID, VariantID, OrderID, At, Until}` | the inventory `Product` resource | Same role: the record of a thing somebody has, outliving the order that produced it. |
| `OrderID` on the grant | order reference on the inventory item | Provenance back to the request and its approval. |
| `Until` (a promise about when access should end) | validity period on the inventory item | Atlas's is explicitly a promise, not a statement that access ceased (ADR-0344). |
| a grant with no order behind it (adopted by a commissioning load) | no equivalent found | ADR-0333. The standard assumes the inventory is fed by orders. This is exactly the case ADR-0312 refused to model as "the successful orders". |
| entitlement history (ADR-0346) | notification/event history | Atlas keeps the row when a hold ends, so a remedy cannot destroy the evidence. |

The three-way split — catalogue, order, inventory — that ADR-0312 argues for over
several pages is the same split TM Forum publishes as TMF620/TMF622/TMF637 and groups
as the ODA component *Core Commerce Management* (TMFC005). ADR-0312 reached it
independently, on lifetime and truth arguments the standard does not spell out.

## 4. Provisioning — Atlas and the Open Service Broker API

The **Open Service Broker API** (v2.16) is the closest standard to the part TMF620 does
not cover: a catalogue whose entries can actually be provisioned.

| Atlas | Open Service Broker API | Note |
|---|---|---|
| `Item` with `provisionProcess` and `deprovisionProcess`, both required to publish | a `service` in the broker `catalog`, with `provision` and `deprovision` operations | Same insistence: an entry that can only be granted is not a lifecycle. |
| `Variant` | a `plan` | Both are "one orderable shape of the thing". |
| `configForm` — an Atlas form id, its answers on the order line (ADR-0358) | `schemas.service_instance.create.parameters` — a JSON Schema | Both delegate per-order configuration to a declared shape. The standard's is a JSON Schema; Atlas's is a form the modeller already has. |
| `Grant` | a service instance, keyed by `instance_id` | |

Atlas's forms are not JSON Schema, so this is a structural parallel rather than a
reusable format. Worth knowing if a catalogue ever has to be driven by an external
platform.

## 5. Identity governance — Atlas, Apache Syncope and midPoint

Six records describe what open-source identity governance has covered for over a
decade. Atlas takes the vocabulary and the known failure modes; it takes no runtime,
because a second writer over the same facts contradicts ADR-0011 and invariant I3.

| Atlas | Syncope / midPoint | Note |
|---|---|---|
| commissioning load, applied only after a report somebody reads (ADR-0333) | reconciliation with a simulated or preview run (midPoint calls it simulation) | Same insight: a bulk attribution has to be checkable before it is written. |
| reconciliation, absence as a finding, only inside a scope somebody declared complete (ADR-0334) | reconciliation | Atlas's scope condition is a sharper statement than either product's documentation makes. |
| recertification, silence is not an answer (ADR-0341) | access certification / recertification | Same concept and same name. |
| `Edge{Kind: excludes}`, a conflict is a fact about a pair (ADR-0342) | separation of duties | Atlas avoids the term; the concept is identical. |
| `maxDays` ceiling and `Grant.Until` (ADR-0344) | validity-bounded assignment | Atlas's is a policy ceiling on the product rather than a date on the assignment. |
| entitlement history (ADR-0346) | audit trail | |
| `TargetRef{system, ref}` | resource and entitlement mapping (ConnId) | Atlas keeps the join as readable data on purpose, so a load's report can be checked. |

Atlas's own connectors and Worker Types (ADR-0203, ADR-0299) occupy the place ConnId
holds in both products. That was decided on other grounds and is not re-opened here.

## 6. Where Atlas deliberately differs

- **One `Item`, not offering plus specification.** A product is an item nothing
  composes, a service one with no parts, a bundle both — determined by position, so
  structure nests to arbitrary depth with a single entity. Splitting it to match TMF620
  would buy conformance at the price of the design's best simplification.
- **The order is a process instance, not a resource.** Durable, replayable, with
  incidents and retries. Its standing is derived from its lines so the two cannot
  disagree.
- **The release is frozen.** An order is immune to catalogue edits made while it waits
  for approval. Nothing in the standards requires this.
- **Three lifecycle states, not eight.** `draft`, `active`, `withdrawn`, with no
  deleted state at all, because an order placed years ago and a right still held both
  resolve through the item.
- **Audience, not market.** One catalogue per person, resolved by rank over group
  membership, narrowed per product by `eligible`. TMF620's market and channel concepts
  answer a different question.

## 7. Where the standard is richer, and it is an open question

Both were decided without the standard in view. Neither is re-opened by the mapping;
each needs its own record.

- **Price.** `price` is a string (ADR-0361). `ProductOfferingPrice` carries an amount, a
  currency, a tax rate and a recurring period. A string cannot be totalled, charged
  back or reported on.
- **Category.** `category` is a string on the item (ADR-0360). TMF620 has `Category` as
  a hierarchy entity, and eCl@ss and UNSPSC exist as external classifications for the
  same job.

## 8. Examined and not applicable

| Standard or product | Why not |
|---|---|
| GS1 GDSN, GS1 GPC | Master data for physical trade items between trading partners. An internal service catalogue has no trade items. |
| eCl@ss, ETIM, UNSPSC | Classification schemes for procurement categories. Potentially useful for `category` (§7), not for the model. |
| BMEcat, SAP OCI, cXML PunchOut | Catalogue interchange and punch-out for e-procurement. Would become relevant if the SAP import ADR-0312 mentions is ever built; nothing in Atlas needs it today. |
| schema.org `Product` / `Offer` | Markup for public web merchandising and search indexing. The portal's catalogue is audience-gated and not indexed. |
| CPSV-AP (EU / ISA²) | Vocabulary for public-service catalogues. Would matter if public administrations were a target; it describes services to citizens, not provisionable entitlements. |
| Akeneo, Pimcore (PIM) | Marketing and commerce attribute management. External runtime with its own database, and the wrong domain. |
| Backstage software catalogue | Developer-portal catalogue of software components, not of orderable services for people. |
| Apache Syncope, Evolveum midPoint | The right domain; see §5. Refused as runtimes on ADR-0011 and I3, adopted as vocabulary. |

## 9. Keeping this honest

No test holds this document against the code, which is stated as an accepted trade-off
in the record behind it. The cheap improvement, if it proves worth doing, is a test
that checks the Atlas column's field names against the `json` tags in `api/catalog` —
that would catch a rename, which is the most likely way this drifts. Until then: when
the catalogue model changes, change the date in the header and the rows that moved.

## Sources

- [TMF620 Product Catalog Management](https://www.tmforum.org/oda/open-apis/directory/product-catalog-management-api-TMF620/v5.0) and [the Open API repository](https://github.com/tmforum-apis/TMF620_ProductCatalog) (Apache 2.0)
- [ProductOffering in the TM Forum data model](https://datamodel.tmforum.org/en/latest/Product/ProductOffering/) and [ProductSpecificationRelationship](https://datamodel.tmforum.org/en/latest/Product/ProductSpecificationRelationship/)
- [TMF622 Product Ordering Management](https://www.tmforum.org/resources/standard/tmf622-product-ordering-management-api-user-guide-v5-0-0/) and [its Open API repository](https://github.com/tmforum-apis/TMF622_ProductOrder)
- [TMF637 Product Inventory Management](https://www.tmforum.org/resources/specification/tmf637-product-inventory-management-api-rest-specification-r18-0-0/)
- [ODA component Core Commerce Management (TMFC005)](https://www.tmforum.org/oda/directory/components-map/core-commerce-management/TMFC005)
- [Open Service Broker API specification](https://github.com/openservicebrokerapi/servicebroker/blob/master/spec.md)
- [Apache Syncope reference guide](https://syncope.apache.org/docs/4.1/reference-guide.html)
- [Evolveum midPoint documentation](https://docs.evolveum.com/faq/)
- [CPSV-AP reference implementation](https://github.com/OPSILab/Service-Catalogue)
