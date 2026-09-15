# ADR-DRAFT: A right may carry an end, and an end that has passed is a debt rather than a fact

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Atlas maintainers
- **Open question:** Whether the ceiling alone is enough, or whether an order has
  to be able to name a shorter end within it. The ceiling is a policy — *nobody
  holds this for more than ninety days* — and it is the control. A project that
  ends in March is a different fact, known by the person ordering and by nobody
  else, and today they cannot say it. It is not built because an order-chosen date
  needs a field on the line, an input in the portal, and the approver seeing what
  they are approving; and because an order-chosen date without a ceiling above it
  is somebody typing 2099. The ceiling first is the half that is a control.
- **Question checked:** 2026-09

## Context and problem statement

Everything the portal grants, it grants forever.

That is not a gap anybody notices on the day it is built, and it is the reason the
three slices before this one exist. The commissioning load
([ADR-0333](0333-inventory-commissioning-load.md)) records what the estate already
holds; reconciliation ([ADR-0334](0334-reconciliation.md)) checks whether the record
is still true; recertification (ADR-0341) asks a person
whether it is still justified. All three are **detective** controls: they find
access that should not be there, after it is there.

Recertification in particular is the manual compensation for the absence of an
expiry, and it is paid for in the scarcest resource in the whole system — a line
manager's attention. Its own record says why that matters: the failure mode of a
recertification is the signature nobody read, and what produces that signature is
four hundred questions. **A question that did not need to be asked is worth more
than a better way of asking it.**

Where the end of somebody's access is knowable when the access is granted, asking a
manager about it four times a year is waste.

**The question this record answers: how can a right end by itself, without Atlas
becoming a system that takes access away on a timer?**

## Decision drivers

- Nothing reaches a target system except through a modelled process (ADR-0312).
- The inventory says what is **true now**. Anything that makes it say otherwise
  corrupts the evidence reconciliation exists to protect.
- A grant is undone by the rules in force when it was made — the frozen release,
  not the catalogue as it stands today (ADR-0312, and `order.Line` already carries
  its processes and its approval rule for exactly this reason).
- Adding a field to an append-only column family costs a migration unless the
  encoding was built to take one. `model.EntitlementValue`'s was.

## The one hard problem: an expiry is not a removal

The tempting reading of "this right ends on 3 March" is that on 4 March the person
does not have it. That reading is false, and building on it corrupts the inventory.

On 4 March the target system still has the group membership. Nothing has run. What
is true is that **Atlas said the access should have ended and it has not** — which
is a debt, not a state of the world. So:

**An expired entitlement is still held.** It is not removed, not hidden, and not
silently dropped from what the inventory reports. It is marked **overdue**, and the
number of overdue rights is a figure somebody can be held to.

The alternative — deleting the record when the clock passes — would make Atlas
assert that somebody does not have access they demonstrably do have. That is
precisely the `missing` direction of a reconciliation finding, which ADR-0334 calls
"the one that corrupts the evidence, because an inventory wrong in this direction
answers *who had access when* with a confident falsehood". Manufacturing that
falsehood on a timer, deliberately, would be worse than decaying into it.

### So what does the removing?

A modelled process, as everything else does. `GET /api/v1/entitlements/expiring`
answers what is due or overdue; the process **returns the order line**, which is the
mechanism that already exists. Atlas reports; the diagram acts.

And the order's return is a stronger mechanism than either sibling can use. Only an
ordered right ever carries an end, so an expiring right always has an order behind
it — and a return revokes by the release the order was placed against, **frozen when
it was placed**. Reconciliation and recertification both have to read the catalogue
as it stands now and both say so as a weakness, because a right nobody ordered has
no frozen release to read. This path has one, and nothing had to be built to use it.

That also decides what *cannot* be ended, and it is a real case rather than a
hypothetical. An entitlement deliberately outlives the order that produced it — the
instance behind that order is eligible for retention deletion long before a
multi-year right ends. A right whose order is gone cannot be returned, so no run of
the expiry process will ever clear it. Those are counted separately as
**unendable**: a number that never moves has to say why rather than look like a
backlog somebody is behind on.

**This is not the same posture as reconciliation's, and the difference is worth
being precise about.** Reconciliation refuses to act because it would be acting on
an *inference* — one worker's answer about somebody else's system, which is the
thing Atlas is least entitled to be confident about. An expiry is not an inference:
the end was part of what was decided and approved when the right was granted.
Honouring it is not a judgement.

So an automatic removal would be defensible here where it is not there. It is still
not done, and for a different reason: the act reaches a target system, and ADR-0312
admits no exception for acts Atlas is confident about. Confidence is not the
criterion. A second removal path invisible to the diagram, the replay and the audit
trail would be one, and the trade — saving somebody a six-line process — is not
close.

## Where the end comes from

**The product declares a ceiling**, in days, and a grant of that product ends that
many days after it began. A product with no ceiling grants open-ended rights, which
is every product today and stays the default.

The ceiling travels with the order line, copied from the release when the order is
placed — exactly as the provisioning process, the deprovisioning process and the
approval rule already do, and for the identical reason. A ceiling relaxed in the
catalogue next week must not lengthen a right somebody was granted this week under
the stricter one.

### It applies only to rights Atlas granted

An `adopted` or `legacy` right gets no end, whatever the product declares, and this
is the sharpest decision in the record.

A commissioning load records `Since` as the moment the right was **found**, not the
moment it began — its own record says so, and says that never moving that date is
load-bearing. Applying a ninety-day ceiling to what a load found would therefore
schedule the expiry of an entire estate ninety days after somebody first switched
the portal on: a mass deprovisioning on the anniversary of commissioning, computed
from a date that was never a start date. That is the "locks a company out of
itself" failure the whole inventory line of work was built to avoid, arriving on a
calendar.

An adopted right is the same case in miniature. Atlas does not know when it began,
and a ceiling measured from a date Atlas made up is a deadline Atlas made up.

## What recertification does with it

A row whose right already carries an end is **marked**, and the campaign counts
them. It is not dropped from the campaign.

Marking rather than skipping, for the reason the disputed marker gives: skipping
hides, and a reviewer who is not shown a row cannot notice that its end is wrong.
What the marker buys is the reviewer's attention going where it is worth something
— a right that ends in three weeks by itself is not what a quarterly review is for.

### Deliberately absent

- **No extension.** Atlas never moves an end date. Extending access is ordering it
  again, and ordering carries the approval rule the catalogue declares. This is the
  third time this line of work has refused a second granting path, and the reason
  has not changed.
- **No warning mail.** Nothing here notifies anybody, for the reason the
  recertification record gives: a reminder to the wrong person is worse than none,
  and who the right person is, is unsettled.
- **No expiry on a right the portal did not grant** — above.

## Implementation

`model.EntitlementValue` gains `Until`, appended to an encoding whose own comment
anticipated a field being added and made ending early rather than erroring the way
to take one. `catalog.Item` gains `MaxDays`; `order.Line` carries it frozen from
the release, and `order.Service.recordInventory` turns it into the grant's end.
`api/expiring.go` is the route and the overdue arithmetic — `classifyExpiring` is
the whole rule, pure and separate from the walk that calls it.
`examples/befristung.bpmn` is the process that acts on it, daily, by returning the
order lines of what is already overdue. It looks no further ahead than zero days on
purpose: returning what expires tomorrow would take somebody's access away today.

### Consequences

- **Positive:** Access can end without anybody deciding to end it, which is the
  only control here that costs no human attention. A recertification campaign over
  an estate where temporary access expires by itself is a smaller campaign, and a
  smaller campaign is one somebody reads.
- **Negative / trade-offs accepted:** An end is a ceiling and not a date somebody
  chose, so "until the project ends in March" cannot be said — the open question
  above. An overdue right stays held until a process runs, so an installation that
  never models one accumulates overdue rights and gains only a report. That is the
  honest consequence of not enforcing, and the report is what makes it visible
  rather than silent. And a right whose order has been deleted by retention can
  never be ended this way at all; it is counted rather than hidden, and clearing it
  is a reconciliation's job or a person's.
- **Follow-ups / risks to watch:** The open question. An order-chosen end within
  the ceiling is the obvious next step and needs the portal and the approver to
  show it.

## Links

- [ADR-0312](0312-portal-catalogue-order-inventory.md) — the three models, the
  frozen release, and the rule that nothing reaches a target system except through
  a process.
- [ADR-0334](0334-reconciliation.md) — the direction of wrongness this refuses to
  manufacture, and the posture this deliberately does not copy.
- [ADR-0333](0333-inventory-commissioning-load.md) — why a found right's `Since` is
  not a start date, which is what keeps the ceiling off the legacy estate.
