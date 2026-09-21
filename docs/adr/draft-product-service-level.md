# ADR-DRAFT: A product may say how long it should take, and that clock decides nothing

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-09-21
- **Deciders:** Atlas maintainers
- **Open question:** Whether a commitment here is ever agreed in **business days**.
  Everything below rests on the assumption that a number in seconds is a usable
  second half of a promise written in prose. If an installation commits to "two
  working days" and expects the measurement to honour Swiss cantonal holidays, the
  number stops standing for the promise and this record needs its successor rather
  than a parser.
- **Question checked:** 2026-09

## Context and problem statement

The catalogue says nothing about time. That is checkable rather than impressionistic:
`api/catalog.Item` carries a name per language, a price, a category, a product group,
keywords, an approval rule, two process bindings, targets, a config form, an
eligibility list and a ceiling — and no field anywhere on it, on `Catalog`, on
`Release` or on `order.Line` says how long an order should take to arrive.

The nearby fields are all about *other* clocks, and each one is a reminder of what is
missing:

- `Item.MaxDays` ([ADR-0344](0344-time-bounded-entitlements.md)) is how long a right
  may **last** once granted. It says nothing about when it starts.
- `Item.Lifecycle{From, Until}` ([ADR-0397](0397-enforce-the-orderable-window.md)) is
  when a product may be **ordered**. Also not a duration.
- A deadline on an approval escalates it to a deputy (`api/order/assignment.go`), and a
  deadline on an incident makes somebody look (`api/order/abandon.go`). Both act on a
  thing that is *stuck*; neither states what "on time" would have been.
- Position progress ([ADR-0390](0390-position-progress.md)) tells an orderer which step
  their line is sitting on. Which step, never for how much longer.

So an orderer can see that their laptop is with an approver, and nobody — not the
portal, not the approver, not the catalogue maintainer — can say from the record
whether that is normal or late.

**What forces the decision now** is that the gap has no record. The mapping in
[`docs/comparisons/catalogue-standards.md`](../comparisons/catalogue-standards.md)
placed the catalogue against TM Forum's and found two counterparts with nothing on the
Atlas side: TMF633's `serviceLevelSpecification` on a service specification, and
TMF622's `requestedCompletionDate` / `expectedCompletionDate` on an order. Price
([ADR-0361](0361-product-price.md)) and category
([ADR-0360](0360-product-category.md)) are also poorer than the standard, and both were
*decided* that way — the records exist and can be argued with. A service level was
never put to anybody. An internal service catalogue that promises nothing about time is
unusual enough that the silence reads, a year from now, as an answer somebody gave.

**Atlas already has a vocabulary for this, and it is not in the catalogue.** A business
capability carries KPIs and SLAs ([ADR-0305](0305-business-capabilities-and-value-streams.md)),
and `capability.SLA` is a considered shape: a `Metric`, a `Threshold` in prose, an
**optional** `ThresholdSeconds`, a `Window`, a `Scope` of internal or external, and a
`Counterparty`. [ADR-0309](0309-measuring-a-capability.md) then measures it against what
actually ran, and refuses to measure a prose threshold rather than guessing:

> A threshold written as prose — "within five business days" — is not something Atlas
> can turn into a number without guessing what a business day is here, and a guessed
> SLA is worse than an unmeasured one: it would be a number somebody acts on that
> nobody authored.

The question this record answers is therefore narrower than "should Atlas have service
levels". It is: **does a catalogue product carry a commitment of its own, what may that
commitment do, and against what is it measured?**

## Decision drivers

- **What an orderer experiences is not what a process measures.** A capability SLA
  measures process instances. The span an orderer lives through starts when they place
  the order and ends when they hold the thing — and it contains an approval that may
  sit with a person for days, and a wait on a precondition in an earlier wave. Neither
  is inside the provisioning process at all, so no measurement of that process can
  answer the question the catalogue would be promising about.
- **A clock decides nothing.** The rule is already structural in two places: a deadline
  escalates an approval and never rejects it, and abandoning a line needs a principal
  because there is nothing to pass when no person is deciding. A missed commitment must
  not become an outcome, a rejection, or an incident by elapse.
- **No second SLA vocabulary.** If a product declares a commitment, it declares it in
  the words the capability map already uses, or a reader holds two grammars for one
  idea.
- **A business day is not defined anywhere in Atlas**, and inventing one in the
  catalogue would put a calendar — with cantonal holidays in it — inside a design-time
  model that has kept far smaller things out.
- **What was ordered against must stay readable afterwards.** A commitment relaxed next
  week must not change what last week's order is reported against. This is the same
  sentence as the price's, the ceiling's and the approval rule's.
- **A field that does nothing is still read as a promise.** Adding one is not free
  merely because no code branches on it.

## Considered options

1. **Nothing, recorded as a decision.** The catalogue stays silent on time; the record
   exists so the silence is reviewable.
2. **Prose only.** A free-text commitment on the product, displayed and never computed
   — the `Item.Price` answer applied to duration.
3. **Prose plus an optional number, and nothing acts on it.** The `capability.SLA`
   shape brought to the product, frozen into the release like every other rule, with a
   later report measuring it.
4. **A commitment that acts.** The elapsed target raises an incident, escalates, or
   marks the line late.
5. **A reference to a capability SLA.** The product names a capability, and the
   commitment lives where SLAs already live — closest to TMF633, which references a
   `serviceLevelSpecification` rather than embedding one.

## Decision outcome

Chosen option: **3 — prose plus an optional number, acting on nothing.**

Concretely, and these five points are the decision:

1. **`Item.ServiceLevel`**, one value and not a list, in the words `capability.SLA`
   already uses: `Promise` (prose — "in der Regel zwei Arbeitstage", what the business
   actually agreed) and `TargetSeconds` (optional — the same commitment as a number,
   supplied by whoever has one). `Scope` and `Counterparty` are deliberately **not**
   carried over: on a capability they distinguish a promise between two teams from one
   owed to a regulator, and on an internal catalogue product the counterparty is always
   the person ordering.
2. **The span is placement to delivery.** `Order.CreatedAt` to `Grant.At`. Both already
   exist, and the second outlives the order, so the measurement survives the retention
   of the instance that produced it — the same property
   [ADR-0312](0312-portal-catalogue-order-inventory.md) built the inventory for.
3. **Nothing acts on it.** No incident, no escalation, no status. The two places where
   an order genuinely gets stuck already have a deadline that makes somebody look, and
   both of them escalate rather than decide.
4. **It travels into the release and onto the line**, like the price, the ceiling, the
   bindings and the approval rule, for the reason all four do.
5. **The report is a later slice.** This record declares the field and the span; it
   does not build the measurement, exactly as ADR-0344 declared the ceiling and
   `api/expiring.go` came afterwards.

### Why not the other four

**Option 1** is the status quo with a record attached, and it is the honest fallback if
nobody wants the field. It is refused because the question an orderer asks most often
about a position — *is this normal?* — is unanswerable today from any surface, and
because the absence currently reads as an oversight rather than a choice.

**Option 2** is cheaper and very nearly right: the price is prose for exactly this
reason, and prose cannot be wrong about a holiday. It is refused because duration
differs from price in one way that matters — Atlas *can* observe it. A price is
somebody else's arithmetic; the elapsed time between an order and a grant is a fact
this system holds on both ends, and declining to number it would leave a promise no
report can ever check. Note also what option 2 quietly requires: a product carries no
description field at all today, so prose on a product is a new text field either way.

**Option 4 is the one to steelman, because it is what a reader expects "SLA" to mean.**
The case for it is real: a commitment nobody is told about is a commitment nobody
keeps; the incident queue is where operators already look; and an order that quietly
sits eight days past a two-day promise is exactly the failure this record claims to
care about. If the point is to make lateness *felt*, a passive field is theatre.

It is still refused, on three grounds. First, a missed target is not a defect: nothing
is broken, nobody can repair it, and `StatusFailed` means an incident an operator
fixes. Filing lateness there would fill the queue with entries whose only remedy is
somebody else's decision. Second, the two causes of lateness that matter — an
unattended approval and a stuck provisioning — **already have their deadline**, and it
escalates to a person. A third clock over the same two conditions would notify twice
for one cause. Third, acting on the number is what forces the business calendar: a
field that only displays can say "two working days" in prose and carry 172,800 seconds
as an approximation nobody is held to, while a field that escalates must be right about
Good Friday in Aargau.

**Option 5** is the closest to TMF633 and is the one this record is least certain
about. Referencing a capability SLA would mean no new vocabulary at all, and the
capability map is where a business owner and a counterparty already live. It is refused
for now because it couples the catalogue to a model that is optional — a product
manager publishing a laptop should not have to model a business capability first — and
because the figure it would point at measures the wrong span (§ Decision drivers). The
reference remains addable later: a product that names both a promise and a capability
is a superset of this decision, not a contradiction of it.

### What the measurement must get right when it is built

Stated here because they are decisions, not implementation detail, and because a report
that gets them wrong will be believed:

- **A skipped line has no grant from this order** — the recipient already held the
  thing. It is not a delivery and must not be counted as a fast one.
- **A rejected or cancelled line is not a missed commitment.** Somebody decided; that
  is the system working. Counting it as lateness would report approvers' judgement as
  operational failure.
- **An abandoned line has no end.** It is a commitment that was never met and has no
  duration to average, so it is reported as its own count rather than folded into a
  mean — the shape `capability.NotMeasured` already uses to keep an absence from
  reading as a zero.
- **The approval span is reported separately** from the provisioning span. Placement to
  delivery is what the orderer experienced and is the right headline; a report that
  only shows the total tells an operations team their provisioning is slow when an
  approval sat for a week. `Assignment.AssignedAt` and `Line.DecidedAt` already hold
  the split.

### Consequences

- **Positive:** the question "is this normal?" becomes answerable from the record, for
  the orderer and for whoever maintains the catalogue. The commitment is authored by
  the person who can actually make it — the product manager — rather than inferred from
  history.
- **Positive:** the gap the standards mapping found is closed with a decision rather
  than with silence, and the catalogue keeps one vocabulary for commitments.
- **Negative / trade-offs accepted:** a declared commitment that nothing measures yet is
  a promise with no observer, and it will be read as stronger than it is between this
  record and the report. Seconds are not what a business agrees in, which is exactly
  why the prose is the authored half and the number the optional one. And placement-to-
  delivery includes time an approver was on holiday — honest to the orderer, unfair to
  operations, which is why the split above is part of the decision and not a nicety.
- **Follow-ups / risks to watch:** a product still has no description field, so the
  prose commitment would be the first text on a product beyond its name — worth noticing
  before it becomes the place people write everything else; TMF622's
  `requestedCompletionDate` (the orderer naming a date *they* need) stays unanswered and
  is a different record; and §2 and §8 of
  [`docs/comparisons/catalogue-standards.md`](../comparisons/catalogue-standards.md)
  need updating if this is accepted.

## Pros and cons of the options

### Nothing, recorded as a decision
- Good: no field, no promise, no drift. Reviewable, unlike today's silence.
- Bad: leaves the commonest question about a position unanswerable, and leaves the
  catalogue the one surface that says nothing about time.

### Prose only
- Good: cheapest; cannot be wrong about a calendar; matches how the price was decided.
- Bad: unmeasurable forever, for a quantity Atlas can actually observe on both ends.

### Prose plus an optional number, acting on nothing
- Good: authored in the business's words, measurable where somebody supplies a figure,
  no calendar, no second vocabulary, and no clock deciding anything.
- Bad: a field that does nothing until a report exists; seconds approximate a promise
  made in working days.

### A commitment that acts
- Good: lateness is felt rather than filed; uses the queue operators already watch.
- Bad: turns a decision or a queue into a defect, notifies twice for causes that already
  escalate, and forces a business calendar into the catalogue.

### A reference to a capability SLA
- Good: no new vocabulary at all; closest to TMF633; owner and counterparty already
  modelled.
- Bad: couples publishing a product to modelling a capability, and the figure measures
  the process rather than the span the orderer lives through.

## Links

- extends [ADR-0312](0312-portal-catalogue-order-inventory.md) — the three models this
  field and its measurement sit across
- follows [ADR-0344](0344-time-bounded-entitlements.md) — a value on the product,
  frozen into the release, read later by a report rather than acted on
- reads on [ADR-0305](0305-business-capabilities-and-value-streams.md) and
  [ADR-0309](0309-measuring-a-capability.md) — where Atlas's SLA vocabulary and its
  refusal to guess a prose threshold come from
- contrasts with [ADR-0361](0361-product-price.md) — prose alone, for a quantity Atlas
  cannot observe
- prompted by [ADR-0387](0387-the-catalogue-against-the-standards-boundary.md) and the
  mapping it produced, where TMF633's `serviceLevelSpecification` has no Atlas
  counterpart
- keeps the rule of [ADR-0390](0390-position-progress.md) — the orderer is answered
  about their own position, without an operations surface
