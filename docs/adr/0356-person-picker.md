# ADR-0356: The recipient is picked from the directory Atlas already publishes, and a name nobody holds is the caller's mistake

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Atlas maintainers
- **Open question:** How two people with the same display name are told apart. The
  directory carries a type, an opaque id and a name, and two colleagues called
  "Max Muster" are indistinguishable in the list — the orderer picks one and finds
  out which months later, in somebody's record. Every disambiguator is a
  disclosure decision: a mail address, a department or a username each says more
  about a person than the picker needs, to every authenticated caller in the
  product, not only to this screen. Showing the id would be honest and unreadable.
  It stays a name until somebody can say which additional field an installation is
  willing to publish to everybody.
- **Question checked:** 2026-09

## Context and problem statement

Ordering in somebody else's name became a first-class screen, gated on the
operator role. The field it is placed through took a free string and offered no
help finding one, and the comment above it said why:

> A plain field and not a picker, because this page has no directory to search […]
> offering a dropdown would mean shipping a person search that answers for
> everybody in the estate — which is an organisation chart.

**That reasoning was wrong, and the record says so rather than quietly changing
it.** Atlas already serves exactly this list, to *any authenticated caller*, at
`GET /api/v1/principals` — the directory every member and assignee picker in the
product reads. It carries a type, an opaque id and a display name, and
deliberately nothing else: no address, no roles, no reporting line. There is no
hierarchy in it to disclose, and a hierarchy is what an organisation chart *is*.
Refusing to use it here withheld nothing; it only made one field harder to use
than every other picker in the product.

Two further things were wrong beneath it.

**The page did not know who was reading it.** It fetched a catalogue, a release,
orders, the inventory and favourites, and never asked what the account may do. So
the recipient field was drawn for every visitor, and for almost all of them the
order it produced came back 403 — which reads as a permission that failed rather
than one they never had.

**A recipient nobody holds was answered as the server's fault.** The name was not
silently accepted, as it first appeared: the eligibility check needs the
recipient's groups, and asking that fails for a name nobody holds. But it failed
as **500**. The same resolver failing because the user store could not be read was
also 500 in one place and **404** in another — the approval inbox turned an
unreadable store into "no such person". Two different failures, answered
interchangeably, in both directions.

## Decision drivers

- Nothing may be disclosed here that Atlas does not already publish to every
  authenticated caller.
- The page must not offer what the server will refuse, and must not withhold what
  it would allow.
- A caller who mistypes a name must get an answer they can act on.
- Atlas cannot evaluate a reporting line. Anything that needs one is not a
  decision this engine can make.

## Considered options

1. **Leave the free-text field** and document the id formats it accepts.
2. **A picker over `/api/v1/principals`**, shown only to the roles that may order
   in somebody else's name.
3. **A new, narrower person-search route** scoped to the caller's own area of
   responsibility.

## Decision outcome

Chosen option: **"a picker over the directory Atlas already publishes"**.

### The scope is the role, not an area of responsibility

This was the open question in the plan: should the search reach the caller's own
area, or the whole estate for a role? It is settled here rather than left open,
because the first is not something Atlas can compute.

"Own area of responsibility" means a reporting line, and Atlas has no reporting
line. The approval kind `superior` exists and resolves nothing by itself: the
escalation path has the *caller* name the superior, precisely because a directory
lookup belongs to a modelled process and not to the engine. An engine that scoped
a person search to a hierarchy would have to invent the hierarchy first, and an
invented hierarchy decides who may act in whose name.

So the scope is the one the order gate already uses — **operator or admin** —
and the list is the whole directory, which those accounts, and every other
account, can already read. The picker discloses nothing new; it is gated because
the *field* is gated, not because the *list* is.

If an installation can express responsibility — as data, in a group or a process —
that is where a narrower scope belongs, and this decision does not stand in its
way.

### The field shows a name and the order carries an id

A display name is not something the server can resolve; an opaque id is not
something a person can check. So each side gets the form it can use: picking sets
the field to "Ada Lovelace" and the request to `usr_…`.

Typing over a picked name un-picks it. Otherwise the order would be placed for
whoever was chosen before, under a name no longer on screen — a silent failure
with somebody else's record as its output.

Free text still works and is still resolved by the server, by principal id,
username, directory id or mail address. Somebody who knows the id types it; the
picker is a convenience, not a new requirement. When nothing matches, the page
says what else works rather than implying the name is wrong.

### Groups are in the directory and not in the picker

The same list carries groups, because a scope grant can name a team. An order
cannot: an entitlement is held by a person. Offering a group here would produce a
recipient the server refuses — after the orderer has filled a basket.

### A name nobody holds is 400, an unreadable store is 500

These are different failures and were answered interchangeably. `ErrNoSuchPrincipal`
now says which, and lives in `api/httpapi` because both halves of the mistake are
in different packages and that is the one both already import.

- An order for a name nobody holds: **400**, with the sentence naming the four
  spellings that resolve.
- An order placed while the user store cannot be read: **500**, unchanged.
- The approval inbox asked about somebody who does not exist: **404**, unchanged.
- The approval inbox asked while the store cannot be read: **500**, where it used
  to answer 404 — telling an operator their colleague has no account when what
  happened is that Atlas could not look.

A sentinel and not a match on the error text, because a status code decided by
string comparison is a status code that changes when somebody improves a message.

### Consequences

- **Positive:** the field is as usable as every other picker in Atlas; the page
  and the gate cannot drift apart, because the roles are read out of the server's
  own constants in a test; a mistyped recipient gets an answer it can act on.
- **Negative / trade-offs accepted:** the whole directory is loaded into the page
  for an operator, which is the same list every other picker already loads and
  shares the portal's ceiling on estate size. Two people with the same display
  name are indistinguishable — see the open question.
- **Follow-ups / risks to watch:** if an installation ever gains a machine-readable
  responsibility relation, the scope decision above is the one to revisit.

## Pros and cons of the options

### Option 1 — leave the free-text field
- Good: nothing to build; no list loaded into any page.
- Bad: it asks somebody to know an opaque id to do the thing the screen exists for,
  while the product already answers that question everywhere else.

### Option 2 — a picker over the existing directory
- Good: no new route, no new store, no disclosure that is not already made; gated
  exactly where the order is gated; the free-text path still works.
- Bad: loads the directory for an operator; no way to tell two identical names
  apart.

### Option 3 — a narrower search over an area of responsibility
- Good: the least disclosure, and the shape most people would describe if asked.
- Bad: it requires a reporting line Atlas does not have and would have to invent.
  An invented hierarchy decides who may act in whose name.

## Links

- relates to ADR-0073 — the principals directory this reads, and why it is open
  to any authenticated caller
- relates to ADR-0349 — the role gate on ordering in somebody else's name
- relates to ADR-0347 — the eligibility check the recipient resolution feeds
