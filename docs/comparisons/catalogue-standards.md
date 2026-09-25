# The catalogue against the standards

This document maps Atlas's catalogue, order and inventory models onto the external
standards that cover the same ground, and lists the standards examined and found
inapplicable. It exists because
[the record on the catalogue and the standards boundary](../adr/0387-the-catalogue-against-the-standards-boundary.md)
committed to it: the catalogue was built without the comparison, and a design whose
prior art is unnamed cannot be told apart from one that never looked.

> [!IMPORTANT]
> **This is not a conformance claim.** Atlas does not implement TMF620, TMF622,
> TMF633 or TMF637, serves none of their resources, and has run no Conformance Test
> Kit. Nothing here is verified by a test.
>
> **Checked:** 2026-09-21 · **Atlas side:** `api/catalog`, `api/order` at that date ·
> **Standard side:** the **v4.0.0 Swagger definitions** in the TM Forum Open API
> repositories, read field by field for the resources named below, plus Open Service
> Broker API v2.16 and the Apache Syncope and Evolveum midPoint product
> documentation.
>
> **What "checked" means here.** Resource and field names on the standard side are
> quoted from those v4.0.0 schemas and are exact. Whether a concept *means* the same
> on both sides is a reading, not a verified equivalence, and no row is a statement
> about behaviour. **TMF620 and TMF622 also exist as v5**, which restructures parts
> of both; v5 was **not** checked, so nothing here should be cited against it.

## Why these and not others

The catalogue is an **internal service catalogue**: somebody orders a workplace, a
licence or an access right, an approver decides, processes provision it in dependency
order, and a record says who holds what. That shape rules most catalogue standards out
before the comparison starts — they describe trade items, procurement documents or web
merchandising, none of which is the problem. What is left splits in two: the TM Forum
catalogue/order/inventory family, and identity governance (Syncope, midPoint).

Inside the TM Forum family, **two catalogue APIs cover this ground and not one**.
TMF620 describes a *product* catalogue — what is offered, to which market, at what
price. TMF633 describes a *service* catalogue — what the thing actually is, and what
realizes it. Atlas's `Item` sits between them: it is offered to an audience like a
product offering, and it is provisioned into target systems like a service
specification. Both are therefore mapped, in §1 and §2, and the second is where the
sharper findings are.

**What this does not repeat.** [Microsoft Identity Manager and Atlas](mim.md) already
compares MIM's *connector surface* against Atlas Worker Types, and notes in passing
that an Atlas process must model the reconciliation logic MIM performs declaratively.
That is the integration layer. This document is about the **data model** — catalogue,
order, inventory, entitlement — which no existing comparison covers.

## 1. Catalogue — Atlas and TMF620

TM Forum's **TMF620 Product Catalog Management** is the closest external description of
what `api/catalog` holds when the catalogue is read as a shop.

| Atlas (`api/catalog`) | TMF620 v4.0.0 | Note |
|---|---|---|
| `Catalog` (`id`, `texts`, `languages`, `items`) | `Catalog` (`name`, `catalogType`, `category`, `lifecycleStatus`, `validFor`, `version`, `relatedParty`) | Atlas's catalogue is also the audience and theme boundary; TMF620's is not. |
| `Item` — product or service **by position in the graph** | `ProductOffering` and `ProductSpecification`, each with `isBundle` | One Atlas entity against two standard ones. A deliberate divergence; see §7. |
| `Edge{Kind: composition}` — integral, never deselectable | `ProductOffering.bundledProductOffering[]`, each carrying a `bundledProductOfferingOption` | The standard expresses mandatory vs. optional bundling through that option's `numberRelOfferLowerLimit` / `numberRelOfferUpperLimit` / `numberRelOfferDefault`, rather than through two edge kinds. Atlas has no cardinality at all; see §8. |
| `Edge{Kind: aggregation}` — optional, separately orderable | as above, with a lower limit of zero | As above. |
| `Edge{Kind: requires}` — `From` cannot be provisioned before `To` | `ProductSpecificationRelationship` with `relationshipType: "dependency"` | Same concept. Atlas additionally computes fulfilment waves from it; the standard states the relationship only. |
| `Edge{Kind: excludes}` — never held by the same person | `ProductSpecificationRelationship` with `relationshipType: "exclusivity"` | Same concept, arrived at independently (ADR-0342 reaches it from separation of duties). |
| `State` — `draft`, `active`, `withdrawn` | `lifecycleStatus` | **The schema types this as a free string, not an enum.** The familiar eight-value list (In study, In design, In test, Active, Launched, Retired, Obsolete, Rejected) is a TM Forum lifecycle *guideline*, not something the v4 API constrains. Atlas's three are a reduction against that guideline, and no API validation distinguishes them. |
| `Lifecycle{From, Until}` | `validFor{startDateTime, endDateTime}` | Same concept, same purpose. The standard also carries `validFor` on relationships, characteristics and categories, where Atlas has nothing. |
| `Variant` (`id`, `texts`, unordered) | `ProductSpecificationCharacteristic` (`valueType`, `minCardinality`, `maxCardinality`, `configurable`, `isUnique`, `regex`), or a plan-like offering | Atlas's variant carries no characteristic structure at all; configuration is delegated to a form. See §5. |
| `price` (a string, ADR-0361) | `ProductOfferingPrice` (`price` as a `Money` of `value` + `unit`, plus `priceType`, `tax`, `recurringChargePeriodType`, `recurringChargePeriodLength`, `unitOfMeasure`) | The standard is substantially richer. Open question; see §8. |
| `category` (a string on the item, ADR-0360) | `Category`, an entity with `parentId`, `isRoot`, `subCategory` and its own `lifecycleStatus` and `validFor` | The standard is richer. Open question; see §8. |
| `productGroup` (a second string on the item, ADR-0383) | the same `Category` hierarchy, one level deeper | Atlas's cascade is two fixed string levels with no entity behind either, so a group has no category of its own — the chain is only ever read off the products that carry both strings. |
| `keywords` (ADR-0355) | no direct equivalent | Atlas-specific, for browser-side search. |
| `targets` (`TargetRef{system, ref}`) | no equivalent in TMF620 | But there is one in TMF633 — see §2, which revises the earlier reading that this belongs only to identity governance. |
| `eligible`, `Catalog.groups`, `Catalog.rank` | no equivalent | Audience resolution is Atlas's own; TMF620 has `marketSegment`, `channel` and `place` on an offering, which solve a different problem. |
| `provisionProcess`, `deprovisionProcess` | no equivalent | The standard does not bind fulfilment to a process. See §5. |
| `approval`, `maxDays`, `configForm`, `multipleAllowed` | no equivalent | Ordering policy, not catalogue description. `configForm` has a counterpart in §5 and in TMF633's `targetEntitySchema` (§2). |
| Release / `Publish` (a frozen, versioned catalogue) | `Catalog.version` and `lifecycleStatus` | Atlas freezes a release so a pending order cannot be edited underneath it (ADR-0312). The standard has no equivalent immutability guarantee in the resource itself. |
| no equivalent | `attachment` on offering and specification | A product carries no picture or datasheet in Atlas. The portal shows text only. |
| no equivalent | `isSellable`, `productNumber`, `brand`, `agreement`, `serviceLevelAgreement` | Commercial and contractual concerns an internal catalogue has no use for — except the SLA, which returns in §2 as the one row this mapping turned into a record. |

**The relationship types are the notable find.** `dependency` and `exclusivity`
correspond to Atlas's `requires` and `excludes` edges. Two cautions the earlier
version of this document did not carry: `relationshipType` is typed as a **free
string** — the four values (`migration`, `substitution`, `dependency`, `exclusivity`)
appear in the field's *description*, not in an enum — so the correspondence is a
convention, not a constraint. And Atlas has no equivalent of `migration` or
`substitution`: a withdrawn item's replacement is not modelled, which §8 now records
as an open question rather than an aside.

## 2. Service catalogue — Atlas and TMF633

**TMF633 Service Catalog Management** is the half of the TM Forum catalogue family
ADR-0387 did not examine, and it is the closer fit for what Atlas actually is: a
catalogue of things that get *realized* in target systems, rather than sold.

> [!NOTE]
> ADR-0387 names TMF620/622/637 as the recognised reference at the export boundary
> and does not mention TMF633. Nothing in that record is changed by this section: the
> mapping below is documentation. If TMF633 is to count at the export boundary too,
> that wants a successor record, not an edit to this file.

| Atlas | TMF633 v4.0.0 | Note |
|---|---|---|
| `Catalog.Items []string` + `Item.HomeCatalog` | `ServiceCandidate` (`serviceSpecification`, `category`, `lifecycleStatus`, `validFor`, `version`) — the entity that "makes a ServiceSpecification available to a catalog" | **The sharpest finding in this document.** Atlas has this relation and not this entity, so nothing catalogue-specific can hang on it. One item in two catalogues has one price, one approval rule, one ceiling and one lifecycle everywhere — while its *structure* (`Catalog.Edges`) is per catalogue. That asymmetry is not stated in any record, and the candidate is the shape that resolves it if it ever has to be. |
| `Item` | `ServiceSpecification`, with `isBundle` | The same one-against-two split as §1, one layer down. Atlas's single `Item` stands in for candidate and specification both. |
| `Edge{Kind: requires}` / `Edge{Kind: excludes}` | `serviceSpecRelationship.relationshipType` — description names `dependency`, `substitution`, `exclusivity`; also a free string | As §1. `serviceSpecRelationship` additionally carries `role` and `validFor`, which Atlas edges do not. |
| `Variant` + `ConfigForm` | `specCharacteristic` (`ServiceSpecCharacteristic`) and `targetEntitySchema` | `targetEntitySchema` is the standard's "here is the shape of what you must supply", which is the role `configForm` plays (and the role the Open Service Broker parameter schema plays in §5). Atlas names a form id and interprets nothing. |
| `TargetRef{system, ref}` | `resourceSpecification[]` on the specification, and the customer-facing / resource-facing split the standard is built around | **This revises §1.** A service specification pointing at what realizes it is exactly the join `TargetRef` makes; it is not solely an identity-governance idea. Atlas's version is deliberately a readable claim rather than a resolvable reference (ADR-0333). |
| `Category`, `ProductGroup` | `ServiceCategory` (`parentId`, `isRoot`, `subCategory`, `serviceCandidate`) | A hierarchy of any depth, against two fixed string levels. |
| no counterpart in the model **yet**; decided by [ADR-draft-product-service-level](../adr/draft-product-service-level.md) | `serviceLevelSpecification` | This row was the gap that prompted that record, and it is now a decision rather than a silence: a product carries a promise in prose plus an optional target in seconds, and nothing acts on it. Two divergences survive the decision — Atlas **embeds** the promise where the standard **references** a specification, and the span measured is placement to delivery rather than the realizing process's own runtime. Nothing is built, so the Atlas column stays empty until it is. |
| no equivalent | `attachment`, `constraint`, `featureSpecification`, `relatedParty` | `relatedParty` (with `role`) is how the standard names a service owner. Atlas has `Catalog.Members` and `OwnerID`, which are access control on the catalogue, not a stated owner of the product. |
| `provisionProcess` / `deprovisionProcess` | no equivalent | TMF633 describes the specification, not its fulfilment — same gap as TMF620, and the reason §5 exists. |
| Release / `Publish` | no equivalent | As §1. |

**What TMF633 changes about the earlier reading.** ADR-0387 argues that Atlas's one
`Item` is better than TMF620's offering-plus-specification split, and that argument
stands. TMF633 shows the split is not two-way but **three-way** — catalogue entry
(candidate), definition (specification), and what realizes it (resource) — and that
Atlas collapses all three into `Item` plus `TargetRef`. The collapse is still
defensible for a single-tenant internal catalogue. It is what would have to be undone
first if one item ever has to be offered differently in two catalogues.

## 3. Order — Atlas and TMF622

**TMF622 Product Ordering Management** covers the order.

| Atlas (`api/order`) | TMF622 v4.0.0 | Note |
|---|---|---|
| `Order` (`id`, `releaseId`, `orderer`, `recipient`, `lines`) | `ProductOrder` | Atlas's order is a BPMN process instance; the standard's is a resource. |
| `Line` | `ProductOrderItem` (`[1..*]` per order) | Same decomposition. |
| `Status` — `running`, `completed`, `partial`, `unfulfilled`, `cancelled`, derived by `Derive` | `state`, of `ProductOrderStateType`: `acknowledged`, `rejected`, `pending`, `held`, `inProgress`, `cancelled`, `failed`, `completed`, `partial`, `assessingCancellation`, `pendingCancellation` | `partial` and `cancelled` coincide; `running` is `inProgress`. Atlas has no `acknowledged` or `held`, and `unfulfilled` has no counterpart — the standard would report that order as `failed` or `rejected`. Atlas's is derived from its lines and never stored, so it cannot disagree with them; the standard's is a stored attribute. |
| `LineStatus` — `pending`, `running`, `done`, `skipped`, `failed`, `rejected`, `abandoned`, `cancelled`, `blocked`, `returning`, `returnFailed`, `returned` | `ProductOrderItem.state`, of `ProductOrderItemStateType` (the same values minus `partial`) | Atlas's list is the longer one, and the extra values carry fulfilment truth the standard has nowhere to put: `skipped` (already held), `blocked` with `blockedBy`, `abandoned` (a failure nobody will repair), and the three return states. |
| return and amendment as **states of the original line** | `ProductOrderItem.action` — `add`, `modify`, `delete`, `noChange` — on a **new** order | **The structural divergence of this section.** The standard models a change or a revocation as another order; Atlas moves the existing line. The consequence is a reporting one: "how many returns last quarter" is a question about line history in Atlas, not a question about orders. |
| `releaseId` pinning the order to a frozen catalogue | no equivalent found | Atlas's answer to "the catalogue changed while the approval was pending". |
| `waves`, `requires` copied onto the order | `productOrderItemRelationship` states item-to-item relations, but carries no computed schedule | The standard relates items; it does not carry a fulfilment schedule on the order. |
| `Assignment`, `Escalation` (approval routing and its history) | no equivalent | Approval is Atlas's own; it belongs to the process layer. |
| one line per item **and variant** (`Line.Key()` = `itemId#variantId`) | `ProductOrderItem.quantity` | Atlas cannot order two of the same thing in the same variant — the key would collide. The code says so itself (`api/order/service.go`: "a quantity — which this catalogue does not…"). See §8. |
| `Price` frozen on the line, a string | `itemPrice`, `itemTotalPrice`, `orderTotalPrice` | Follows from §1's price. Nothing can be totalled. |
| no equivalent | `externalId` | There is nowhere on an Atlas order to put the identifier of the request in the system it came from. Cheap to add, and the first thing an integration asks for. See §8. |
| no equivalent | `requestedCompletionDate`, `expectedCompletionDate`, `completionDate`, `priority`, `note`, `channel` | Atlas has no date or priority concept on an order at all. Related to the missing SLA in §2. |
| no equivalent | `billingAccount`, `payment`, `quote`, `agreement`, `productOfferingQualification` | Commercial concerns, deliberately absent. |
| `Orderer`, `Recipient` (two fixed principal ids) | `relatedParty[]` with a `role` per party | Atlas's two roles are fixed by the model; the standard admits any number. |

Atlas's order is deliberately **not** a stored state machine — the process instance is
the order, and its standing is derived from its lines rather than stored (ADR-0312).
That is a real divergence from a resource-shaped standard, and together with the
`action` row above it is what would matter most if an export were ever built.

## 4. Inventory — Atlas and TMF637

**TMF637 Product Inventory Management** covers what a party holds.

| Atlas (`api/order` `Grant`, `api/inventory.go`) | TMF637 v4.0.0 | Note |
|---|---|---|
| `Grant{Principal, ItemID, VariantID, OrderID, At, Until}` | the inventory `Product` resource | Same role: the record of a thing somebody has, outliving the order that produced it. |
| `OrderID` on the grant | `productOrderItem` on the product | Provenance back to the request and its approval. |
| `Until` (a promise about when access should end) | `terminationDate`, with `productTerm` for the agreed duration | Atlas's is explicitly a promise, not a statement that access ceased (ADR-0344). |
| `Origin` — `ordered`, `adopted`, `legacy` (ADR-0333) | no equivalent | The standard assumes the inventory is fed by orders. This is exactly the case ADR-0312 refused to model as "the successful orders". |
| held / not held, read from the presence of the row | `status`, of `ProductStatusType`: `created`, `pendingActive`, `active`, `suspended`, `pendingTerminate`, `terminated`, `cancelled`, `aborted` | Atlas carries the in-flight truth on the **order line** (`returning`, `returnFailed`) and keeps the inventory row binary. A suspension — held but not usable — cannot be expressed on either side of Atlas. |
| entitlement history, with `reason` of `returned` or `corrected` and a `held` flag (ADR-0346) | notification and event history | Atlas keeps the row when a hold ends, so a remedy cannot destroy the evidence, and it distinguishes "the access ended" from "Atlas was wrong about it". |
| no equivalent | `realizingService`, `realizingResource`, `productSerialNumber`, `place`, `isCustomerVisible` | `realizingService` is the TMF633 link (§2) seen from the inventory side. |

The three-way split — catalogue, order, inventory — that ADR-0312 argues for over
several pages is the same split TM Forum publishes as TMF620/TMF622/TMF637 and groups
as the ODA component *Core Commerce Management* (TMFC005). ADR-0312 reached it
independently, on lifetime and truth arguments the standard does not spell out.

## 5. Provisioning — Atlas and the Open Service Broker API

The **Open Service Broker API** (v2.16) is the closest standard to the part TMF620 and
TMF633 do not cover: a catalogue whose entries can actually be provisioned.

| Atlas | Open Service Broker API | Note |
|---|---|---|
| `Item` with `provisionProcess` and `deprovisionProcess`, both required to publish | a `service` in the broker `catalog`, with `provision` and `deprovision` operations | Same insistence: an entry that can only be granted is not a lifecycle. |
| `Variant` | a `plan` | Both are "one orderable shape of the thing". |
| `configForm` — an Atlas form id, its answers on the order line (ADR-0358) | `schemas.service_instance.create.parameters` — a JSON Schema | Both delegate per-order configuration to a declared shape. The standard's is a JSON Schema; Atlas's is a form the modeller already has; TMF633 calls the same idea `targetEntitySchema`. |
| `Grant` | a service instance, keyed by `instance_id` | |

Atlas's forms are not JSON Schema, so this is a structural parallel rather than a
reusable format. Worth knowing if a catalogue ever has to be driven by an external
platform.

## 6. Identity governance — Atlas, Apache Syncope and midPoint

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
| `TargetRef{system, ref}` | resource and entitlement mapping (ConnId) | Atlas keeps the join as readable data on purpose, so a load's report can be checked. TMF633's `resourceSpecification` (§2) is the same join stated in catalogue terms. |

Atlas's own connectors and Worker Types (ADR-0203, ADR-0299) occupy the place ConnId
holds in both products. That was decided on other grounds and is not re-opened here.

## 7. Where Atlas deliberately differs

- **One `Item`, not candidate plus specification plus offering.** A product is an item
  nothing composes, a service one with no parts, a bundle both — determined by
  position, so structure nests to arbitrary depth with a single entity. Splitting it
  to match TMF620 or TMF633 would buy conformance at the price of the design's best
  simplification. What §2 adds is the price of the collapse, stated: catalogue-specific
  product attributes are unreachable.
- **The order is a process instance, not a resource.** Durable, replayable, with
  incidents and retries. Its standing is derived from its lines so the two cannot
  disagree.
- **A change is a move, not a new order.** Returns and amendments act on the original
  line rather than producing an order with `action: delete` or `modify` (§3).
- **The release is frozen.** An order is immune to catalogue edits made while it waits
  for approval. Nothing in the standards requires this.
- **Three lifecycle states, not eight.** `draft`, `active`, `withdrawn`, with no
  deleted state at all, because an order placed years ago and a right still held both
  resolve through the item. Note that the standard's eight are a guideline rather than
  an enum the API enforces (§1).
- **Audience, not market.** One catalogue per person, resolved by rank over group
  membership, narrowed per product by `eligible`. TMF620's `marketSegment`, `channel`
  and `place` answer a different question.

## 8. Where the standard is richer, and it is an open question

All of these were decided — or never put — without the standard in view. None is
re-opened by the mapping; each needs its own record. One of them now has one: the
service level below is decided and not yet built, and it stays in this section until
the model carries it, because until then the standard is still the richer of the two.

- **Price.** `price` is a string (ADR-0361). `ProductOfferingPrice` carries a `Money`,
  a tax rate, a price type and a recurring period. A string cannot be totalled,
  charged back or reported on.
- **Category.** `category` and `productGroup` are strings on the item (ADR-0360,
  ADR-0383). TMF620's `Category` and TMF633's `ServiceCategory` are hierarchy entities,
  and eCl@ss and UNSPSC exist as external classifications for the same job.
- **Quantity.** An order line is keyed by item and variant (`Line.Key()`), so the same
  product in the same variant cannot be ordered twice. `ProductOrderItem.quantity` and
  TMF620's `numberRelOffer*` limits both express it. The catalogue's own code names the
  limitation; no record decides it.
- **Succession.** `migration` and `substitution` relationships have no Atlas edge kind.
  A withdrawn item names no replacement, which recertification and catalogue
  maintenance both have a use for.
- **A service level — decided, not yet built.**
  [ADR-draft-product-service-level](../adr/draft-product-service-level.md) accepts a
  promise on the product: prose, plus an optional target in seconds borrowed from
  `capability.SLA`, frozen into the release and copied onto the line, measured from
  placement to delivery by a report that is a later slice. Nothing acts on the target,
  and that refusal is the load-bearing half — a missed commitment is not a defect an
  operator repairs, and the two conditions that actually stall an order already carry a
  deadline that escalates to a person. Two things stay open beside it: TMF633
  *references* a `serviceLevelSpecification` where Atlas will embed one, and TMF622's
  `requestedCompletionDate` — the orderer naming a date they need — is answered by
  nothing and wants its own record.
- **An external identifier.** TMF622's `externalId` has no place on an Atlas order or
  item, so a request that originated in another system cannot carry its own id.
- **Catalogue-specific product attributes.** See §2: the `ServiceCandidate` shape.

## 9. Examined and not applicable

| Standard or product | Why not |
|---|---|
| GS1 GDSN, GS1 GPC | Master data for physical trade items between trading partners. An internal service catalogue has no trade items. |
| eCl@ss, ETIM, UNSPSC | Classification schemes for procurement categories. Potentially useful for `category` (§8), not for the model. |
| BMEcat, SAP OCI, cXML PunchOut | Catalogue interchange and punch-out for e-procurement. Would become relevant if the SAP import ADR-0312 mentions is ever built; nothing in Atlas needs it today. |
| schema.org `Product` / `Offer` | Markup for public web merchandising and search indexing. The shop's catalogue is audience-gated and not indexed. |
| CPSV-AP (EU / ISA²) | Vocabulary for public-service catalogues. Would matter if public administrations were a target; it describes services to citizens, not provisionable entitlements. |
| Akeneo, Pimcore (PIM) | Marketing and commerce attribute management. External runtime with its own database, and the wrong domain. |
| Backstage software catalogue | Developer-portal catalogue of software components, not of orderable services for people. |
| Apache Syncope, Evolveum midPoint | The right domain; see §6. Refused as runtimes on ADR-0011 and I3, adopted as vocabulary. |

## 10. Keeping this honest

No test holds this document against the code, which is stated as an accepted trade-off
in the record behind it. **That drift has now happened once and been repaired:** the
first version of this document mapped `category` and never mentioned `productGroup`,
which `api/catalog` had gained meanwhile. The same pass also corrected three claims
about the standard side that the v4.0.0 schemas do not support — a
`bundledGroupStatement` field that does not exist in v4, `relationshipType` described
as a closed set of four values when the schema types it as a free string, and
`lifecycleStatus` described as an eight-value list when it is likewise a free string.

So the cheap improvement §9 of the earlier version proposed is no longer hypothetical:
a test that checks this document's Atlas column against the `json` tags in
`api/catalog` and `api/order` would have caught the first of those, which is the most
likely way this drifts again. Until it exists: when the catalogue model changes, change
the date in the header and the rows that moved.

## Sources

Resource and field names on the standard side were read from these v4.0.0 Swagger
definitions. The directory pages are given for context and are not what was checked;
for TMF620 and TMF622 they describe **v5**, which this document does not cover.

- [TMF620 Product Catalog Management](https://www.tmforum.org/oda/open-apis/directory/product-catalog-management-api-TMF620/v5.0) — checked against [`TMF620-ProductCatalog-v4.0.0.swagger.json`](https://github.com/tmforum-apis/TMF620_ProductCatalog) (Apache 2.0)
- TMF633 Service Catalog Management — checked against [`TMF633-ServiceCatalog-v4.0.0.swagger.json`](https://github.com/tmforum-apis/TMF633_ServiceCatalog) (Apache 2.0). No directory page is cited here because none was verified; the repository is the source that was read.
- [TMF622 Product Ordering Management](https://www.tmforum.org/resources/standard/tmf622-product-ordering-management-api-user-guide-v5-0-0/) — checked against [`TMF622-ProductOrder-v4.0.0.swagger.json`](https://github.com/tmforum-apis/TMF622_ProductOrder) (Apache 2.0)
- [TMF637 Product Inventory Management](https://www.tmforum.org/resources/specification/tmf637-product-inventory-management-api-rest-specification-r18-0-0/) — checked against [`TMF637-ProductInventory-v4.0.0.swagger.json`](https://github.com/tmforum-apis/TMF637_ProductInventory) (Apache 2.0)
- [The TM Forum data model](https://datamodel.tmforum.org/en/latest/Product/ProductOffering/), for the entity descriptions behind the schemas
- [ODA component Core Commerce Management (TMFC005)](https://www.tmforum.org/oda/directory/components-map/core-commerce-management/TMFC005)
- [Open Service Broker API specification](https://github.com/openservicebrokerapi/servicebroker/blob/master/spec.md)
- [Apache Syncope reference guide](https://syncope.apache.org/docs/4.1/reference-guide.html)
- [Evolveum midPoint documentation](https://docs.evolveum.com/faq/)
- [CPSV-AP reference implementation](https://github.com/OPSILab/Service-Catalogue)
