# ADR-0387: The catalogue keeps its own model, and answers to TMF620 at the boundary

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-17
- **Deciders:** Atlas maintainers
- **Open question:** Whether any installation needs the TM Forum resources on the
  wire, rather than in a document. That decides whether the mapping this record
  commits to ever becomes code, and no prospect has yet asked for it.
- **Question checked:** 2026-09

## Context and problem statement

The catalogue, the order and the inventory are now the better part of thirty records,
scattered between [ADR-0311](0311-portal-approval-page.md) and
[ADR-0383](0383-portal-level-names.md), plus most of `api/catalog`, `api/order` and the
portal. Not one of them names an external standard or an existing implementation, and
the omission is checkable rather than impressionistic:

- `grep -ci standard docs/adr/0312-portal-catalogue-order-inventory.md` returns zero.
  The founding record of the complex, 767 lines long, does not contain the word.
- Its *Considered options* are "one model", "two models", "three models". All three
  are internal shapes of the same build. No option reads "adopt something that
  exists".
- The same holds for the records around it: [ADR-0358](0358-order-line-configuration.md)
  (field list / form id / free text), [ADR-0360](0360-product-category.md) (entity /
  field / nothing), [ADR-0361](0361-product-price.md) (money type / string / bare
  number), [ADR-0355](0355-catalogue-search.md) (names / keywords / a search service).
- [ADR-0176](0176-standards-boundary-and-runtime-contract.md) exists precisely to
  answer "which parts of Atlas are governed by external standards". Grepping the
  catalogue records for `0176` returns nothing: not one of them cites the record
  written to place exactly this kind of question.

So the question was not decided against the standards; it was never put. That matters
for a reason that has nothing to do with whether the answers are good. A record that
does not name prior art is indistinguishable from one that examined it and refused
it — and the refusal is the valuable half, because it is the half a reviewer, a
customer or the next maintainer can check.

What the catalogue built is, in its essentials, already standardised. TM Forum
publishes the separation [ADR-0312](0312-portal-catalogue-order-inventory.md) argues
for at length as three APIs: **TMF620** Product Catalog Management, **TMF622** Product
Ordering Management, **TMF637** Product Inventory Management, grouped as the ODA
component *Core Commerce Management*. Catalogue, order, inventory — three models, not
one, for the lifetime reasons ADR-0312 gives. The record reached a correct conclusion
and left the independent confirmation of it unclaimed.

The second half of the complex has the same shape.
[ADR-0333](0333-inventory-commissioning-load.md),
[ADR-0334](0334-reconciliation.md), [ADR-0341](0341-access-recertification.md),
[ADR-0342](0342-conflicting-rights.md),
[ADR-0344](0344-time-bounded-entitlements.md) and
[ADR-0346](0346-entitlement-history.md) are the standard repertoire of identity
governance: reconciliation, recertification, separation of duties, validity-bounded
assignment, an audit trail that survives the remedy. Apache Syncope and Evolveum
midPoint have carried that repertoire in the open for years — reconciliation and
access certification under those names, in both products' own documentation. Neither
is named anywhere in the records.

And Atlas does not otherwise work this way. It executes BPMN and DMN, expresses
conditions in FEEL, provisions over SCIM 2.0 and LDAP, describes its HTTP surface with
OpenAPI ([ADR-0043](0043-openapi-spec-and-embedded-api-explorer.md)), exposes metrics
in Prometheus exposition, and reads catalogue structure out of ArchiMate.
[ADR-0381](0381-a-problem-aggregates-incidents.md) takes ITIL's words outright, with
the reason stated in one line: *"ITIL's words, because operators already have them and
inventing synonyms would buy nothing."* That sentence is at least as true of a product
catalogue. The catalogue is the one large component of Atlas built without the
comparison.

The repository even keeps documents of exactly the kind that is missing here:
`docs/comparisons/n8n.md` places Atlas against an integration platform, and
`docs/comparisons/mim.md` maps Microsoft Identity Manager's connector surface onto
Worker Types because a wishlist cited by two records had never been written down. The
second is the near miss — it is the identity domain, and it touches reconciliation
only to say an Atlas process would have to model what MIM does declaratively. Neither
looks at a catalogue, an order or an entitlement as a model. So the practice exists;
the catalogue simply never got one, which makes the gap more conspicuous rather than
more explicable.

This record makes the comparison, says what it changes and what it does not, and puts
the catalogue on the right side of ADR-0176's boundary explicitly instead of by
default.

## Decision drivers

- ADR-0176 requires that each surface be placed: governed by an external standard, part
  of the Atlas runtime contract, or an internal detail. The catalogue was never placed.
- The invariants are not negotiable. A second runtime with its own database contradicts
  [ADR-0011](0011-single-binary-distribution-and-web-ui.md) and I3/I4, so any option
  that ships foreign software fails before it is weighed.
- Interoperability has to be demonstrable without reading Go. A prospect asking "does
  this map onto our product catalogue" deserves a document, not a source tour.
- The internal model must stay free to be simpler than the standard. Atlas has one
  `Item` where TMF620 has offering and specification, and that simplification is a
  feature of a self-service catalogue, not an omission to be corrected.
- Prior art must be named even where it is refused, or the refusal cannot be reviewed.
- Nothing here may change a decision already accepted. ADRs are immutable; a record
  that re-opens one writes a successor rather than an edit.

## Considered options

1. **Adopt an existing implementation.** Akeneo or Pimcore for the catalogue, Apache
   Syncope or midPoint for the inventory, reconciliation and recertification.
2. **Adopt the standard schema as the internal model.** Store and serve TMF620/622/637
   resources as Atlas's own catalogue, order and inventory shapes.
3. **Keep the internal model and own the mapping.** Leave `api/catalog`, `api/order`
   and the inventory as they are; record the mapping onto TMF620/622/637 as
   documentation, and treat the standard as the reference at the export boundary if and
   when an export is built.
4. **Leave it undocumented.** Carry on; the code works.

## Decision outcome

Chosen option: **3 — keep the internal model and own the mapping.**

Option 1 is refused on the invariants, and the refusal is cheap: Syncope and midPoint
are Java services with their own relational store and their own transaction boundary,
so adopting either means a second writer over the same facts, an external database and
a second provisioning path — three things ADR-0011, I3 and ADR-0312's own "no second
provisioning path" driver each forbid on their own. What deserves saying is that this
argument only ever answered *"should we run someone else's software?"* It never
answered *"should we use someone else's words and schema?"*, and the two were never
separated. That conflation is the actual defect this record repairs, and it is a
process defect rather than a modelling one.

Option 2 is refused on substance. TMF620 carries market, channel, sales and agreement
concerns that an internal service catalogue has no use for, and ADR-0176 §3 deliberately
keeps Atlas's storage representations private and evolvable. More importantly it would
cost the one simplification the catalogue genuinely earned: an `Item` is a product or a
service **by its position in the graph**, not by its type, so structure nests to
arbitrary depth with one entity. Splitting it into `ProductOffering` and
`ProductSpecification` to match the standard would buy conformance and pay for it in
exactly the place the design is better than the standard.

Option 4 is what the repository does today, and it is the only option with no upside.
It leaves the strongest available evidence for the design unclaimed, and leaves a gap
that reads a year from now as a decision somebody took.

Option 3 splits the question the way it should have been split at the start. The
implementation stays Atlas's. The vocabulary is compared, the overlap is written down,
and the divergences are stated with their reasons — which is what ADR-0176 §2 already
prescribes for a boundary and what §3 prescribes for the middle.

### What this record settles

1. **The catalogue, order and inventory models belong to ADR-0176 §3** — the Atlas
   runtime contract — and not to §1. Said, now, rather than assumed.
2. **TMF620/622/637 is the recognised reference at the export boundary.** No export
   exists and none is scheduled. If one is built, it maps to those resources rather
   than inventing a third shape.
3. **The mapping is documentation, and it lives in**
   [`docs/comparisons/catalogue-standards.md`](../comparisons/catalogue-standards.md).
   It carries the concept-level correspondence, the deliberate divergences, and the
   standards examined and found inapplicable.
4. **ArchiMate stays the import boundary**, unchanged from
   [ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md) and ADR-0312.
   This is the part of the complex that was already standards-based.
5. **The identity-governance records name their prior art.** Syncope and midPoint are
   where reconciliation, recertification, separation of duties and validity-bounded
   assignment come from. Atlas takes the vocabulary and the failure modes; it does not
   take the runtime. The mapping document records which of their concepts each record
   corresponds to.
6. **No conformance claim is made.** Atlas does not claim TMF620 conformance, is not a
   TM Forum member as far as this record knows, and has run no Conformance Test Kit.
   ADR-0176's rule that a support claim is stated against a named version and verified
   by tests applies here too, and nothing here is verified by tests.

### What this record does not settle

Two accepted decisions look different once the standard is on the table, and neither is
re-opened here — an ADR is immutable, and each needs its own record with its own
argument:

- **[ADR-0361](0361-product-price.md), price as a string.** TMF620 carries
  `ProductOfferingPrice` with an amount, a currency, a tax rate and a recurring period.
  A string cannot be totalled, charged back to a cost centre or reported on. For an
  internal catalogue that may still be the right trade; the point is that it was chosen
  without the alternative in view.
- **[ADR-0360](0360-product-category.md), category as a field on the product.** TMF620
  has `Category` as an entity with a hierarchy, and eCl@ss and UNSPSC exist as external
  classifications. ADR-0360's title — a heading a product writes on itself — is a
  deliberate counter-position, taken without meeting its counterpart.

### Consequences

- **Positive:** the design's strongest external corroboration becomes citable. A
  prospect's "how does this relate to our catalogue" has a document as its answer. The
  next domain built on Atlas has a worked example of the comparison ADR-0176 asks for.
- **Positive:** the two decisions above are now visibly open questions rather than
  invisible ones.
- **Negative / trade-offs accepted:** a mapping document is a second description of the
  model, and no test holds it against the structs, so it can drift. It is dated and
  says what it was checked against, which is the weakest of the available guards and
  the only one this record is prepared to pay for now.
- **Negative:** the mapping is concept-level. It was not verified field by field
  against the TM Forum specifications, and says so in its own header. Anyone who needs
  field fidelity has to do that work.
- **Follow-ups / risks to watch:** the price and category records; whether
  `docs/adr/template.md` should carry a *prior art examined* line so the next complex
  does not repeat this (a process change, and one that wants its own record); and
  whether a test could hold the mapping's Atlas column against the JSON tags in
  `api/catalog` — cheap, and it would turn the weakest guard above into a real one.

## Pros and cons of the options

### Adopt an existing implementation
- Good: a decade of somebody else's edge cases, in reconciliation especially, where the
  edge cases are the product.
- Bad: a second writer, an external database and a second provisioning path. Fails
  ADR-0011, I3 and ADR-0312's own drivers independently of each other.

### Adopt the standard schema as the internal model
- Good: conformance becomes a test rather than a claim; integration work moves to the
  other side of the boundary.
- Bad: imports market and sales concerns an internal catalogue has no use for; freezes
  a storage shape ADR-0176 §3 deliberately keeps private; and gives up the
  position-determines-kind simplification that makes one `Item` sufficient.

### Keep the internal model and own the mapping
- Good: costs nothing in the engine, claims nothing untestable, and makes the
  comparison reviewable. Separates "run their software" from "use their words", which
  is the distinction that was missing.
- Bad: documentation can drift from code, and a mapping nobody exports is a mapping
  nobody exercises.

### Leave it undocumented
- Good: no work.
- Bad: the confirmation stays unclaimed, and an unasked question reads as an answered
  one to every later reader.

## Links

- constrained by [ADR-0176](0176-standards-boundary-and-runtime-contract.md) — the
  standards boundary this record finally applies to the catalogue
- complements [ADR-0312](0312-portal-catalogue-order-inventory.md) — which reached the
  three-model split on its own reasoning; nothing in it is changed here
- constrained by [ADR-0011](0011-single-binary-distribution-and-web-ui.md) — why no
  foreign catalogue or governance runtime is adoptable
- names the prior art behind [ADR-0333](0333-inventory-commissioning-load.md),
  [ADR-0334](0334-reconciliation.md), [ADR-0341](0341-access-recertification.md),
  [ADR-0342](0342-conflicting-rights.md),
  [ADR-0344](0344-time-bounded-entitlements.md) and
  [ADR-0346](0346-entitlement-history.md)
- leaves open [ADR-0360](0360-product-category.md) and
  [ADR-0361](0361-product-price.md) — each wants a successor record, not an edit
- follows the example of [ADR-0381](0381-a-problem-aggregates-incidents.md) — taking an
  established vocabulary because operators already hold it
- reads on [ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md) — the
  ArchiMate binding that is the import boundary
- the mapping itself: [`docs/comparisons/catalogue-standards.md`](../comparisons/catalogue-standards.md)
