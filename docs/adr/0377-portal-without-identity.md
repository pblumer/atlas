# ADR-0377: With nobody to be, the portal shows a catalogue and refuses an order

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-16
- **Open question:** Whether the single-user mode should instead synthesise a
  configurable principal — a name and a set of groups the server pretends is
  signed in. That would make every per-account surface work rather than only the
  catalogue, and it is the shape an installation would want for a demo that shows
  ordering end to end. It is not built because it adds a second meaning of "nobody
  is signed in" to a product that has exactly one today, and the mode is for
  development rather than for demonstration to a customer. It stays unbuilt until
  somebody needs to show the whole flow without accounts.
- **Question checked:** 2026-09

## Context and problem statement

`--auth=false` is Atlas's documented development and demo mode: enforcement off,
no sessions, no principal. Every gate in the product reads that the same way —
**enforcement off means there is nobody to be, not nobody who may** — and the
catalogue package states it beside its own predicates.

The portal did not. Which catalogue somebody sees is resolved from the groups they
carry: no principal, no groups, so `ReachedBy` answers false for every catalogue
and `Resolve` finds none. The mode's one screen said:

> Ihnen ist kein Katalog zugeordnet.

to somebody there is no "you" to assign one to. The portal was unusable in the
mode it is documented to be usable in, and the message misdescribed why.

## Decision drivers

- The rule already exists and is stated: absence of enforcement is absence of
  identity, not absence of permission.
- A catalogue with no audience reaches nobody. That is **fail-closed on purpose**:
  a freshly created catalogue has no groups yet, and the dangerous default is the
  one where it is open to everybody while somebody is still filling it. Any
  exception to a fail-closed default has to earn itself.
- An order belongs to somebody. The server refuses one with no orderer, because it
  has nobody to notify and nobody to hold responsible — and that refusal is right.
- A control that is offered and then refused teaches somebody the page is broken.

## Considered options

1. **Resolve the highest-ranked catalogue** when there is nobody to be.
2. **Leave it.** The portal stays empty without accounts.
3. **Synthesise a principal** in the single-user mode.

## Decision outcome

**Resolve the highest-ranked catalogue**, and say on the page why an order cannot
be placed.

### Why the exception is safe here and nowhere else

Not because the audience matters less in this mode — because it cannot matter at
all. With enforcement off, **every catalogue is already readable through the
administration routes by anybody who can reach the port.** The audience rule
protects one customer's catalogue from another customer; in a mode with no
customers it withholds a catalogue from a person who does not exist, at the cost
of the only screen the mode is for.

It is written as two conditions, not one: **no principal is present**, *and*
there is nobody to be. The second alone would be a statement about the mode; both
together make it a statement about *absence*, which is what the argument above
rests on. Today no configuration can satisfy the second without the first — with
`--auth=false` no principal is ever built — so the first is asked directly by a
test rather than through a mode. It is the guard that keeps option 3, if it ever
arrives, from silently handing everybody the top-ranked catalogue.

**Rank** is the answer to "which one", because rank is already what the product
uses to decide which of several catalogues a person sees, and publishing refuses a
rank tie — so "highest" is an answer rather than a coin toss.

A signed-in administrator is unaffected: they get the catalogue their groups
reach, not the top-ranked one. Being allowed to read every catalogue is not the
same as being the audience for one, and the portal asks the second question.

### What is still refused, and why the page says so

An order. `HandlePlace` refuses one with no orderer, and that stays: an order with
nobody attached has nobody to notify, nobody to hold responsible, and nothing for
an approver to decide about. So the mode is **read the catalogue, do not order
from it**, and the page is built to say that rather than to discover it:

- The order button is replaced by a sentence naming the reason and the remedy.
- The basket control is shown **disabled**, like an integral part is, because a
  basket somebody can fill and never empty is the same failure moved one step
  earlier. Disabled rather than hidden, so the column still reads as the
  decomposition the screen exists to show.
- The favourite mark is not offered: a favourite belongs to an account.
- The recipient field is not offered: ordering in somebody else's name is a
  question about authority, and it only arises where an order can be placed at all.

Whether an order is possible is read from **the identity the session carries**,
mirroring the server's own rule, rather than inferred from the mode. The two mean
the same thing today and a page that inferred it would disagree on the day they
stop.

### The per-account lists

The inventory and the favourites answer about an account, and their routes refuse
a caller with none rather than inventing an empty answer. That is right of them,
and it used to take the page down: one 400 threw out of the portal's load and the
screen showed an error instead of a shop. They are now read defensively — a
per-account list that is not there is a list missing, not a catalogue missing.

### Consequences

- **Positive:** the documented mode shows what it is documented to show; the
  fail-closed rule is untouched wherever there is somebody; the page explains the
  mode instead of failing in it.
- **Negative / trade-offs accepted:** a demo cannot show ordering; an installation
  with several catalogues gets the top-ranked one, which may not be the one the
  person running the demo had in mind; a mode-specific branch exists in the
  resolution, which is one more thing to keep true.
- **Follow-ups / risks to watch:** the branch is the first exception to
  `ReachedBy`'s fail-closed default. If a second surface wants one, that is the
  moment to ask whether the rule is still the right one rather than to add another
  exception beside this.

## Pros and cons of the options

### Option 1 — resolve the highest-ranked catalogue
- Good: the mode works; the argument for safety is a property of the mode rather
  than a judgement; no new configuration.
- Bad: an exception to a fail-closed default, and a branch that only one mode takes.

### Option 2 — leave it
- Good: nothing to undo; the fail-closed rule stays absolute.
- Bad: the documented development mode cannot show the portal at all, and says
  something untrue about why.

### Option 3 — synthesise a principal
- Good: every per-account surface works, ordering included.
- Bad: a second meaning of "nobody is signed in", and a configured identity that
  looks like a real one in the record. It is the larger decision this one does not
  foreclose.

## Links

- relates to ADR-0316 — the portal's catalogue and its audience
- relates to ADR-0180 — the groups a principal carries from login, which are what
  is absent here
- relates to ADR-0349 — ordering in somebody else's name, the field this mode
  stops offering
