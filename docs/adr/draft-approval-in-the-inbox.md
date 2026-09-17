# ADR-DRAFT: An approval is read and decided where the work already is

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-17
- **Deciders:** Atlas maintainers
- **Open question:** whether the inbox should show *whose* catalogue an order came from in that catalogue's colours, rather than by name. Today the Console wears nobody's brand; the page this replaced wore the customer's, and that is the one thing it did that this does not
- **Question checked:** 2026-09

## Context and problem statement

[ADR-0311](0311-portal-approval-page.md) gave the approver a page of their own. Its
argument was about *people*: a `superior` approver is the line manager the directory
resolved, who approves perhaps four times a year, and the Console is an operator's
instrument whose navigation reads Deployments, Instances, Incidents and Workers.

That argument was right about the population and wrong about what follows from it,
and the reason is a fact that was true the whole time: **an approval is an ordinary
engine user task, and the inbox never filtered those out.** The rows were always
there. So the page did not spare anybody the Console — it added a second place to
take one decision, next to a first place that already had it and did not say so.

The two then drifted, as two places for one decision do. The page refused a
rejection with no reason; the inbox's generic Complete button did not — and worse,
completing an approval task with nothing filled in reads as `genehmigt = null`, which
is not `true`, which is a **rejection recorded with no reason and no sign that
nobody meant it**.

## Decision drivers

- One decision should have one place. Two surfaces for it will differ, and the
  question is only when and how badly.
- What made the page worth having — what was ordered, for whom, under which rule, at
  what price — is data, not a page. It comes from one call the inbox already makes.
- Nothing in the page's argument survives if the inbox is also where **all** of that
  person's other work is. A line manager who approves four times a year has a task
  inbox for exactly the same reason they have an approval.

## Considered options

1. **Keep both**, and keep them in step by discipline. That is what produced the
   drift above.
2. **Keep the page and strip the inbox's rows down** to "open the page". A row that
   says what it decides and sends somebody elsewhere to decide it is the second
   surface again, one screen further in.
3. **Read and decide the approval in the inbox, and retire the page.**

## Decision outcome

Chosen option: **3**.

- The row names what it decides — the product as the catalogue wrote it, and the
  cost. Every approval task is called "Genehmigen", so without that a queue of them
  is a column of identical lines.
- The detail leads with the rest: variant, recipient, orderer, order, and the
  catalogue the order came from.
- **Approve** and **Reject** send the decision, with the reason a rejection needs
  refused before anything is sent; where the same order has more approvals in this
  inbox, one decision covers them under one reason
  ([ADR-0362](0362-collective-approval.md)).
- For the approval Atlas ships there is exactly **one** way to answer in that screen:
  the generic Complete button and the shipped form give way, and Ctrl+Enter says so
  rather than doing it.
- An installation whose products name **its own** approval model keeps its form and
  its Complete button. `genehmigt` and `begruendung` are the shipped form's contract
  and not a general one, and two buttons answering for a model Atlas cannot read
  would complete somebody's task with variables their process never sees.

### The address outlives the page

Every approval notification ever sent carries a link to `/genehmigung.html` with the
order line in its query, and a mail cannot be recalled. So the page's **address**
stays as a forwarding stub that hands the line it was given to the inbox, which
resolves it against the approvals that person holds — by position first, then by
product, which is what every link sent before a product could be ordered twice
carries. The stub may be deleted once no notification old enough to point at it can
still name an open approval.

The models themselves link into the inbox from now on.

## Consequences

- **What is lost is the brand.** The page wore the catalogue's colours, because an
  approver decides on that customer's behalf. The Console wears nobody's, so the
  block names the catalogue in words instead. That is information kept and
  presentation dropped, and it is the one thing somebody could reasonably want back.
- An approver still meets the Console shell. Its drawer is filtered by role, so an
  ordinary account sees Console, Tasks and Portal — and the link from the mail lands
  them on the approval itself, with the block at the top of the screen.
- One surface to change when an approval learns something new, and one place where a
  rule like "a rejection needs a reason" can be true.
- ADR-0311's API decision stands untouched: one call answers everything an approval
  shows, because the chain behind it — task, order, release, catalogue — is one the
  approver may walk no step of themselves. It is what this reads.
