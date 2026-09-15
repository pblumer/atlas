# ADR-DRAFT: An approval list is a work list, so it starts where nobody has looked

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Atlas maintainers
- **Open question:** Whether the sort choice should be remembered between visits.
  It is not, because this page is usually reached from a mail link naming one
  approval, where the list order never comes up — and a preference that only
  matters on the rare visit is a preference nobody will notice is wrong. If the
  list becomes the ordinary arrival, this changes.
- **Question checked:** 2026-09

## Context and problem statement

The approver's page rendered every open approval as a plain list in whatever order
the endpoint returned, which is newest first. With three approvals that is fine.
With forty it is a wall, and the story asks for what a wall needs: *geordnet und
sortierbar*.

The temptation is a table with a column per fact and a filter over each, which is
what the portal's order list now has. That would be wrong here, and
[ADR-0311](0311-portal-approval-page.md) says why: the common approver is a line
manager who decides perhaps four times a year, and a page that grew into a console
is a page they will ask a colleague to operate.

## Decision

A search field, a sort control, and a count. Not a table.

### One search field, not one per column

An approver looking for "the laptop for Ada" does not know which column they are
searching, and a row of five fields would ask them to. One field matches across the
product (in words and by id), the recipient, the orderer, the order id and the
catalogue.

### Oldest first, which changes the default

The endpoint answers newest first and the page took that. A work list read from the
top should start where nobody has looked: **what has waited longest is what nobody
has looked at**, which is the argument the recertification campaign and the conflict
report each make about their own lists. Making it the default here rather than an
option somebody has to find is the same decision those made.

### Age is the job key, because there is no other age

A user task carries no created-at. The job key is monotonic and the approvals
endpoint already pages by it, so a higher key is a newer task — this reuses the
server's own convention rather than inventing a second one. A clock reading taken
in the browser would be a number nobody can check.

### A row says only what the approval carries

A due date where the model set one, and *passed on* where an assignment record
exists. That record appears once a deadline or a person has moved the approval, and
its **absence is the answer** "nobody has had to chase this" — so a row without one
says nothing rather than showing an invented age. This is the same rule
ADR-0343's `waitingSince` follows.

### Due dates sort ahead of everything undated

A task somebody put a deadline on is a different thing from one nobody did. Sorting
the undated in among them by their own age would bury the deadlines, which is the
opposite of what choosing that order asks for.

### The controls appear from the first row

Rather than past a threshold. A list that grew a search box at the eleventh approval
would be a different page each time somebody arrived, and this page is visited
rarely enough that every visit should look the same.

## Consequences

**A defect this surfaced and did not introduce.** `render()` passed `cond ? node :
null` to `replaceChildren`, which — unlike this page's own `el()` — turns a non-node
into a *text* node. Three of those slots are conditional and none is usually filled,
so an ordinary load has always shown the literal word `nullnullnull` above the list
and `null` below it. `paint()` filters. The portal carried the same defect and is
fixed the same way, which makes it a pattern rather than an accident: any page
building children this way has it.

**The search is client-side and bounded by what the page already loaded.** The
endpoint caps the walk and flags a capped page, and that notice still shows: a
search that found nothing must not read as "there is nothing" when the answer is
"not on this page".
