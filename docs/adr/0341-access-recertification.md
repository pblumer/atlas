# ADR-0341: Recertification asks whether a right is still justified, and refuses to let silence answer

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Atlas maintainers
- **Open question:** Who reviews a right whose holder has no reviewer — an
  unassigned row today lands with the campaign's owner, which is correct and does
  not scale: an estate of five thousand rights with no manager relation in the
  directory makes the owner the reviewer of everything, which is the
  rubber-stamping this record exists to prevent, wearing a different hat. The fix
  is a reviewer derived from the product rather than from the person, and it is
  the wrong trade until somebody has run a campaign and seen how many rows land
  unassigned.
- **Question checked:** 2026-09

## Context and problem statement

Three questions can be asked about somebody's access, and until now Atlas could
ask two.

1. *May they have it?* — ordering answers it, with the approval rule the catalogue
   declares ([ADR-0312](0312-portal-catalogue-order-inventory.md)).
2. *Do they actually have it?* — reconciliation answers it, in both directions
   ([ADR-0334](0334-reconciliation.md)).
3. *Do they still need it?* — **nothing asks this at all.**

The third is not a smaller version of the other two. A right that was correctly
approved, correctly provisioned and is correctly recorded can still be wrong, and
in most estates that is the *dominant* way wrong access accumulates: people change
roles and keep what the old one needed. Nobody granted anything improperly; nobody
removed anything either, because removing is somebody's job and it is nobody's job.

**The question this record answers: how does Atlas put a right in front of a person
who can judge whether it is still needed, and record what they decided, without the
recording becoming the point?**

## Decision drivers

- Nothing reaches a target system except through a modelled process (ADR-0312).
- Atlas does not resolve line managers. `api/escalation.go` settled that: the
  directory lookup belongs to a modelled process, which already does it for the
  `superior` approval rule.
- The output is evidence kept for years, so it must be as hard to falsify as the
  journal beside it.
- A decision is made by a line manager, who is an ordinary `user` — not an
  operator. The confined-credential split of ADR-0334 does not transfer.
- Every ceiling on external input reads a named budget.

## The one hard problem: the signature that means nothing

A recertification campaign that shows four hundred rows and a **Certify all**
button produces a signed attestation containing no information. It is not merely
useless — it is worse than nothing, because an auditor reading it has evidence
that somebody looked, and nobody did. Every real recertification fails this way,
and it fails for a reason that is structural rather than lazy: the reviewer is
asked four hundred questions and rewarded for answering them quickly.

Three consequences follow, and they are the design:

**No bulk decision.** Each row is its own call, by the person the row was addressed
to. This is the same shape as ADR-0334's three actions and the same argument as its
confirmation rule: what is cheap gets done without reading. Here the reading *is*
the product, so nothing may be decided in bulk — not by the reviewer, not by the
owner, not by an operator.

**Silence is never a decision.** A campaign that closes with rows nobody answered
reports them as **undecided**, and a row that is undecided is not certified. The two
tempting defaults are both wrong in the same way: "silence means keep" invents an
attestation nobody gave, and "silence means revoke" locks people out because a
manager was on holiday. Neither is a decision, and recording a non-decision as one
is precisely the falsification this exists to prevent. It is the same refusal
ADR-0334 made about a reading that carried nothing: what is special about the empty
case is the prior, not the logic.

**The row carries what the reviewer was shown.** Origin, since when, the order it
came from, and whether the right is currently in dispute. A decision taken without
those is not an informed one, and a reader a year later must be able to tell the
difference. Storing them on the row rather than resolving them at render time is
deliberate: what is being recorded is *what the person saw*, and a row that
re-resolved would quietly show a later reader a different question from the one
that was answered.

## Certifying an inventory nobody has checked

A recertification certifies the **inventory**, and the inventory asserts something
Atlas cannot guarantee. Run a campaign over an unreconciled estate and every
signature is against a record that may not be true — a manager attesting that Alice
still needs a right she lost three months ago.

This is why the slice comes after ADR-0334 rather than before it, and the ordering
is load → reconcile → certify for a reason that is not taste. It is also why a row
whose (principal, item) pair has an **open finding** is marked as disputed, and the
campaign report counts them: certifying a disputed right is signing a statement
about something two systems currently disagree about.

It is marked rather than refused. Refusing would make one open discrepancy block a
campaign covering thousands of rights, and the reviewer of *this* row may well know
exactly what happened. What they must not do is answer without being told.

## Who reviews, and why Atlas does not work it out

The caller names the reviewer per row. Atlas does not derive it, and the reason is
settled rather than new: `api/escalation.go` already argues that a line-manager
lookup is a directory question and therefore a modelled process's, not the engine's
— the `superior` approval rule works exactly this way.

**An empty reviewer is not an error.** It is the answer "nobody", and such a row is
reported as unassigned and lands with the campaign's owner. That is the same shape
as the escalation's empty superior, and the alternative — refusing the campaign —
would mean one person without a manager in the directory stops the recertification
of an entire estate.

### Authority is per row, not per role

The three reconciliation actions are in no confined scope because each takes access
away or changes what Atlas asserts. A recertification decision does both of those
too, but it is made by a line manager, and a line manager is an ordinary `user`.

So the decision routes take `user` and are authorised **by the row**: the reviewer
the row names may decide it, and so may an operator or an administrator. This is
`api/taskauthority.go`'s rule, not a new one — a task the model addressed belongs to
whoever it was addressed to, and operators keep everything because they can already
reach it another way. An unassigned row is the one exception, and it is deliberately
*not* open to everybody the way an unassigned user task is: a row nobody was
addressed about is the campaign owner's to answer, because somebody has to be
accountable for a signature nobody was asked for.

## What a decision does

| Decision | What it records | What it does |
|---|---|---|
| **keep** | that this person, at this moment, saw this right and judged it still needed | nothing else — no entitlement is written, because nothing changed |
| **revoke** | the same, with the opposite judgement | runs the product's deprovisioning process |

Revoking runs the process, never a direct worker call, for ADR-0312's reason. It
uses the catalogue as it stands now, with the same weaker guarantee ADR-0334
accepted and for the same reason.

A right an order granted is the exception: it goes back through that order, as a
return does, so that its process knows the order and can report the line returned
([ADR-0418](0418-a-withdrawn-ordered-right-goes-back-through-its-order.md)).

**The decision is recorded when the person decides, not when the target system has
caught up.** What is being attested is a judgement; the process's outcome is the
process's to report. That is `closedDeprovisioning`'s argument, reused.

A right already gone by the time a revoke arrives is recorded and starts nothing.
The campaign is a snapshot and the estate moves under it; a decision about
something that is no longer there is still a decision somebody made, and starting a
deprovisioning against nothing would be an instance raising an incident about a
fact rather than a fault.

### Deliberately absent

- **No auto-revoke at the deadline.** A deadline that revoked what nobody answered
  would take access away because a manager was on holiday — non-response is not a
  decision, which is the same sentence as above and the reason it is worth saying
  twice.
- **No re-grant.** Granting is ordering, and ordering carries the approval rule.
  Identical to ADR-0334's absent fourth action.
- **No reopening a decided row.** A campaign is a moment. Changing one's mind is a
  new campaign or an order, both of which leave their own evidence; an editable
  attestation is not one.

## Where the rows come from

The same whole-inventory walk ADR-0334 built, off the loop, once per campaign:
`state.queries.Entitlements`. Opening a campaign is therefore a population-sized
read, and it is rare — once per campaign rather than once per decision — which is
what makes it affordable. The rows are frozen into the campaign at that moment,
which the snapshot argument above requires anyway.

### Consequences

- **Positive:** The third question can be asked. A right's justification has an
  owner, a date and a name against it. "Undecided" is a first-class outcome, so a
  campaign reports the truth about itself rather than a completion percentage.
- **Negative / trade-offs accepted:** Opening a campaign is a population-sized
  read. Rows are a snapshot and the estate moves under them. The reviewer comes
  from the caller, so a campaign is only as well-addressed as the process that
  opened it — and a badly addressed one lands wholesale on its owner. Nothing
  reminds anybody: there is no notification here, and a campaign nobody is told
  about is a campaign nobody answers.
- **Follow-ups / risks to watch:** The open question above. Notification is the
  obvious next slice and is deliberately not in this one — a reminder that goes to
  the wrong person is worse than none, and who the right person is, is exactly what
  the open question says is unsettled.

## Implementation

`api/recertify.go` builds a campaign's rows from the inventory and decides what a
row is worth showing; `api/recertifystore.go` is the durable record;
`api/recertify_http.go` the routes and `api/recertifyactions.go` the two decisions.
`api/web/recertification.js` is where a reviewer answers.

It sits under **Tasks**, and that is a decision rather than a placement. Not
Operations, where reconciliation is: a finding is repair and the operator's, while
this asks a line manager whether somebody on their team still needs something, and a
line manager has never opened Operations. Not one of the two portal pages either —
those carry the *catalogue's* brand and are written for people outside the tooling,
somebody ordering a laptop. The audience here is internal and it is the Tasks
audience exactly: work addressed to you, waiting for a decision. It is a second kind
of inbox, so it sits beside the first.

The screen's own constraint is the record's: it offers no way to answer more than one
row, and a test holds that, because everything the server refuses to make cheap an
interface can make cheap again with a single button.

`examples/rezertifizierung.bpmn` is the modelled process that opens a campaign with
reviewers resolved from the directory. It reads `manager` from `delta-users`, which
is a Graph navigation property rather than an ordinary column — whether this worker
returns it has **not been verified**, and the model says so: if it does not, every
row lands unassigned, which is visible in `counts.unassigned` rather than silent.

## Links

- [ADR-0312](0312-portal-catalogue-order-inventory.md) — the three models, the
  inventory this certifies, and the rule that nothing reaches a target system
  except through a process.
- [ADR-0334](0334-reconciliation.md) — the truth of the inventory this assumes, the
  refusal of a heuristic for the empty case, and the deprovisioning path reused.
- [ADR-0333](0333-inventory-commissioning-load.md) — where the rights a first
  campaign certifies most likely came from.
- [ADR-0275](0275-instance-visibility.md) — the per-object authority this extends to
  the decision routes.
