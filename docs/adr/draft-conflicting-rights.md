# ADR-DRAFT: A conflict is a fact about a pair, and no rule can say which half is wrong

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Atlas maintainers
- **Open question:** Whether a conflict needs an exception path — "these two may be
  held together if X approves". Every real separation-of-duties regime has one,
  because the rule that admits no exception is the rule somebody works around
  outside the system. It is not built here for a concrete reason rather than a
  vague one: an order line carries **one** approval rule, copied from the release,
  and an exception is a second rule on the same line. The order model has nowhere
  to put it, and inventing somewhere is a larger change than this slice. Until then
  an exception is an order the catalogue refuses, and the honest workaround is to
  change the catalogue.
- **Question checked:** 2026-09

## Context and problem statement

Everything the portal has learned to do about access is **detective or temporal**.
The commissioning load records what was there ([ADR-0333](0333-inventory-commissioning-load.md));
reconciliation checks whether the record is true ([ADR-0334](0334-reconciliation.md));
recertification asks whether it is justified (ADR-0341);
an expiry ends it by itself (ADR-draft-time-bounded-entitlements). All of them look
at one right at a time, and all of them look *after*.

None of them can express the oldest control in access governance: **these two things
must never be held by the same person.** The clerk who can create a supplier must
not also approve payments to it. The developer who writes the deployment must not
also release it. Neither right is wrong; the *combination* is.

The catalogue can say what may be ordered, who approves it and what it requires. It
cannot say what it excludes.

**The question this record answers: how does a catalogue declare that two rights are
incompatible, and what happens to the people who already hold both?**

## Decision drivers

- The catalogue already models relations between items. A third question about a
  pair of items belongs where the other two are, or it will be a second graph
  nobody keeps in step.
- Atlas must not enforce by removing. Every record in this line of work says it,
  and a conflict is the case where removing is most obviously wrong — see below.
- A release freezes what the catalogue said. Whatever is declared has to survive
  into one.

## The one hard problem: no single culprit

Every mechanism the portal has acts on one `(principal, item)` pair. An entitlement
is one. A reconciliation finding is one. A recertification row is one. An expiry is
one.

**A conflict is a pair of pairs**, and that breaks the shape of everything built so
far. Asked "what is wrong here", the honest answer is *neither right, and both
together* — and no rule can pick. The clerk needs one of them to do their job;
which one is a question about the job, not about the catalogue.

So:

- **Nothing is ever revoked automatically**, and here that is not caution but
  arithmetic: an automatic remedy would have to choose a half, and choosing wrongly
  takes away the right the person actually needs while leaving the one they should
  not have.
- **A finding names both sides**, and is one finding rather than two. Two findings
  would invite two people to each fix "their" half, which either does nothing or
  removes both.
- **The remedy is an order's return or a recertification**, both of which already
  exist and both of which record who decided. This slice adds no new way to take
  access away, which makes it the first one in this line of work to add none.

## Where it is declared: a third edge kind

`catalog.Edge` already answers two questions about a pair of items — structure
(what belongs to what) and precedence (what must exist first) — and its own comment
records that those two were conflated once and had to be separated. Incompatibility
is a **third** question, and it is put beside them rather than encoded in one of
them, for the same reason the first two are apart: an edge kind that means two
things is an edge kind nobody can read.

**`EdgeExcludes` is symmetric, and it is the only one that is.** Composition,
aggregation and precedence all mean something different read backwards; "A must not
be held with B" is exactly "B must not be held with A". That asymmetry with the
other kinds is a hazard rather than a curiosity: a reader that checks only the
declared direction finds half the violations and reports the estate as half clean.

So publishing is where the symmetry is resolved, and it resolves it by **writing
both directions down**. The release carries `Excludes` as a map from each item to
everything it is incompatible with, with every declared pair present under both
ids. Duplicates collapse and a self-conflict is refused — an item that excluded
itself would make holding it once a violation.

Both directions rather than one canonical pair, and that is the decision rather than
an implementation detail. A canonical form makes a reader responsible for knowing
the convention, and a reader that forgets finds half the violations and reports the
estate as half clean — silently, because nothing about a one-directional check looks
wrong. Storing it expanded makes the question "what does this item exclude" a single
lookup that cannot be half-asked. The cost is that each pair appears twice in a
release, which is a few bytes against a class of bug that reports a clean estate.

## What happens when somebody orders into a conflict

The order is **refused at placement**, against both what the recipient already holds
and the rest of the same basket.

Refused rather than granted-and-detected, and the reason is the whole point of a
preventive control: detection means the combination exists in the real world for as
long as detection takes. A control that lets the thing it forbids happen and then
reports it is a detective control with extra steps.

The refusal names **both** items and which side is already held, because "this order
is refused" without the other half sends somebody to ask why.

## What happens to the people who already hold both

They are reported, and the rule applies to them immediately.

This is the deliberate opposite of the expiry ceiling, which never reaches a right
granted before it was declared. The two look similar and are not: an expiry is
**part of what was granted** — it belongs to that grant and cannot be applied
retroactively without inventing a start date nobody agreed. A conflict is a
statement about what may **coexist now**, and it is either true of the estate today
or it is decoration.

So `GET /api/v1/conflicts` walks the inventory whole — the walk ADR-0334 built —
and reports every person holding an excluded pair, against the **current** release
rather than the one each right was ordered under. Declaring a rule therefore
surfaces its violations the same day, which is the only behaviour that makes
declaring one worth doing.

### Consequences

- **Positive:** The catalogue can express the control auditors ask about first
  after "who approved this". It is the only preventive control here — everything
  else finds a problem after it exists.
- **Negative / trade-offs accepted:** No exception path, so a legitimate
  combination is an order the catalogue refuses until somebody changes the
  catalogue — the open question above. Detection reads the whole inventory, like
  its siblings. And a rule declared over a large estate can produce a great many
  findings at once, none of which anything will clear automatically; that is
  correct and it is also a lot of work arriving in one morning.
- **Follow-ups / risks to watch:** The exception path. And a conflict between more
  than two items — "no two of these three" — which this cannot express and which a
  pairwise edge can only approximate.

## Implementation

`catalog.EdgeExcludes` is the kind; `catalog.Publish` normalises the pair and
refuses a self-conflict; `Release.Excludes` is the frozen, canonical list.
`api/order` refuses a placement that would create one. `api/conflicts.go` is the
route over the estate. `examples/unvereinbarkeit.bpmn` is the modelled process that
reports them.

## Links

- [ADR-0312](0312-portal-catalogue-order-inventory.md) — the catalogue, the release
  and the order.
- [ADR-0334](0334-reconciliation.md) — the whole-inventory walk this reuses, and the
  posture about not removing what nobody decided to remove.
- [ADR-0239](0239-off-loop-queries.md) — why the walk runs off the loop.
