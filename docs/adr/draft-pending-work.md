# ADR-DRAFT: A reminder has to be able to ask about somebody else, and a person must not

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Atlas maintainers
- **Open question:** Whether engine user tasks belong in the same answer. They are
  work addressed to a person by the same reasoning, and somebody asking what is
  waiting for them does not care which subsystem holds it. They are left out here
  because a task's assignment is the model's, it already has an inbox, a count and
  an escalation path of its own, and folding it in would make one route own two
  different notions of "addressed to". The day somebody wants one reminder rather
  than two is the day to revisit it.
- **Question checked:** 2026-09

## Context and problem statement

The portal now asks people for three different things. An approver decides an order
line ([ADR-0312](0312-portal-catalogue-order-inventory.md)). A reviewer answers a
recertification row (ADR-draft-access-recertification). Neither happens unless the
person opens Atlas and looks.

The recertification record already named this in its own consequences: *"Nothing
reminds anybody: there is no notification here, and a campaign nobody is told about
is a campaign nobody answers."* It is not a small gap. A campaign of five hundred
rows across forty managers, with nobody told, closes with four hundred and eighty
undecided — and every one of those is recorded as *not certified*, correctly and
uselessly.

**The gap is sharper than "there is no notification", and the sharp version is what
this record is about.** Atlas can already send mail: a modelled process carries a
mail task, `to="=someRef"` names a principal or a group, and `api/maildirectory.go`
resolves the address in the server at the moment of sending, so no address ever
enters a variable, an order or the log ([ADR-0314](0314-portal-personal-data.md)).
The machinery is there and it is good.

What is missing is that **every route that answers "what is waiting" answers only
for the caller.** `GET /api/v1/approvals` is "every open approval addressed to
you"; a recertification campaign read with `?mine=true` is the rows *you* may
answer. A reminder process is not the person it is reminding. There is nothing it
can ask.

**The question this record answers: how does a process learn what is waiting for
somebody else, without that becoming a way for anybody to enumerate anybody's
work?**

## Decision drivers

- People are named by reference, never by address (ADR-0314). Whatever this answers
  must keep that true.
- A confined credential's reach is a short list somebody can read in one glance
  ([ADR-0194](0194-api-tokens.md)).
- Nothing reaches a target system — or a mailbox — except through a modelled
  process (ADR-0312). Atlas must not grow a second way to send a message.

## The one hard problem: an interruption you cannot act on

A reminder is an interruption, and its whole value rests on being right. A reminder
about work that is not yours, or that you already did, does not merely waste a
minute: it teaches the reader that these messages are noise, and the next one — the
one that mattered — is deleted unread. **One wrong reminder costs more than ten
right ones earn.**

Everything here follows from that:

- **Nothing is listed that the person cannot act on right now.** A recertification
  row in a closed campaign is not waiting for anybody. A row somebody already
  decided is not waiting. An approval that has escalated away is not theirs any
  more. Each of those would otherwise produce a perfectly formatted message about
  nothing.
- **The answer says what it is about and where to go**, per item, because a
  reminder that says "you have 3 open items" sends the reader to find them.
- **It counts as well as lists.** A reminder process needs to decide whether to
  send at all, and a process that has to fetch a list to discover it is empty sends
  a mail to say nothing rather often.

## Authority: two modes, and the split is the point

`GET /api/v1/pending-work` answers **the caller's own**, at `user` — every
signed-in person may ask what is waiting for them, which is what the existing
routes already allow one subsystem at a time.

`GET /api/v1/pending-work?principal=…` answers **somebody else's**, and takes
`operator`. That is the capability a reminder needs and the one a person must not
have: a portal where any user can enumerate any other user's pending approvals has
turned an inbox into an organisation chart with workloads attached.

### A scope of its own

A reminder process runs unattended, so it carries an API token, so its reach is a
confined scope — and it is a new one rather than an addition to an existing.

`apiScopeInventory` would have been the cheap choice, because the reconciliation
run is already in it. It is the wrong one: that scope's argument is "reads and
writes about *what the estate holds*", and pending work is about what people owe.
A scope whose name no longer describes its contents is a scope nobody can reason
about, which defeats the one property ADR-0194 asks of it — that its reach is short
enough to read in a glance and see whole.

So `apiScopeReminders` reaches exactly one pattern. It cannot read an inventory, it
cannot run a comparison, and it cannot decide anything: **it can find out who owes
what, and that is all.** Sending is not in it either, because sending is a mail
task, not a route.

## What it does not do

- **It does not send.** A modelled process does, with the mail task that already
  exists. A second notification path inside Atlas would be invisible to the
  diagram, unconfigurable per installation, and immediately the wrong one for
  somebody — and ADR-0312's rule about target systems is the same rule.
- **It does not remember what it sent.** No "last reminded" marker, no suppression
  window. That belongs to whatever does the sending, because only it knows what it
  sent and to whom; a marker here would be Atlas recording a fact about a mail it
  did not send and cannot see.
- **It does not include engine user tasks** — the open question above.

### Consequences

- **Positive:** A campaign can be answered by people who were told about it, which
  is the difference between the recertification slice working and existing. The
  route is one answer across two subsystems, so a reminder is one message rather
  than one per surface.
- **Negative / trade-offs accepted:** Enumerating somebody else's work is a real
  capability and it now exists; it is behind `operator` and a one-pattern scope,
  and that is the whole of the protection. The answer is a moment's — somebody may
  decide a row between the read and the mail, and then the reminder is about
  something already done. That is the one wrong-reminder case this cannot close,
  and it shrinks with how fast the process sends.
- **Follow-ups / risks to watch:** The open question. And nothing here paces
  anything: a model that runs hourly sends hourly, and the record deliberately does
  not decide that for the operator.

## Implementation

`api/pendingwork.go` is the route and the gathering. `apiScopeReminders` is in
`api/apitokenscope.go` with its one pattern. `examples/erinnerung.bpmn` is the
modelled process: it reads the campaign, asks per reviewer what is waiting, and
sends one mail each through the existing mail task.

## Links

- [ADR-0314](0314-portal-personal-data.md) — why an address is resolved at the
  moment of sending and never carried.
- [ADR-0194](0194-api-tokens.md) — what a confined scope owes a reader.
- [ADR-0312](0312-portal-catalogue-order-inventory.md) — approvals, and the rule
  that side effects belong to modelled processes.
