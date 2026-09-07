# ADR-DRAFT: An information model that no single application owns

- **Status:** Proposed
- **Date:** 2026-09-07
- **Deciders:** Patrick Blumer

## Context and problem statement

[ADR-0230](0230-process-information-model.md) made an information model a document
**owned by a process application**, inheriting that application's sharing scope
(ADR-0071/0128) rather than inventing a second ACL. That was the right call for the
first cut: it gave the document an owner, a permission model and a place in the
navigation on day one, and it cost nothing while every model belonged to one
application anyway.

It stops being right the moment two applications mean the same thing by `Customer`.

The whole argument of ADR-0230 is that BPMN leaves `itemSubjectRef` opaque, so two
processes that both handle an order have two unrelated strings named `order` and
nothing says they mean the same thing. Scoping the answer to one application solves
that *inside* an application and reproduces it exactly one level up: two applications
that both handle a customer now have two unrelated `Customer` classes, each with its
own attributes, its own business key, and no statement anywhere that they are the
same customer. The attributes are typed twice, drift independently, and the identity
that ADR-0230 calls load-bearing — the part that makes `Order#ORD-1` the same order
in three processes — stops working at the application boundary for no reason a
modeler would recognise.

Reported plainly from use: *"ich will Attribute bestenfalls nur einmal anlegen und
mehreren Applications zur Verfügung stellen können."*

So the question is not whether a model may be shared. It is **what a model with no
application is** — because the application is currently the model's owner, its
permission anchor, and the key its vocabulary is resolved by.

## Decision drivers

- **The identity claim has to survive the application boundary.** A shared `Customer`
  is the point; anything that keeps two of them has not solved the problem.
- **Do not invent a second ACL.** ADR-0230 refused to, and that refusal is still
  right; whatever governs a model without an application should be a rule already in
  the building, not a new grant table.
- **Existing models must keep working, unmigrated.** Every model today has an
  application, and every deploy resolves against it.
- **Ambiguity in resolution is worse than an inconvenience.** `itemSubjectRef`
  resolves to *one* class. Two candidates for one name is not a merge problem, it is
  a question nobody can answer from the model.
- **A deploy must not start failing.** ADR-0230 §3: an unresolved reference is a
  Problems-panel finding, never a deploy failure. Nothing here may turn a running
  application red.

## Considered options

1. **Every model becomes global.** Drop `applicationId` entirely.
2. **A model belongs to an application and gains a share list.** Keep the owner, add
   the applications it is offered to.
3. **A model may have no application, and one that has none is a library.** Keep
   per-application models exactly as they are; make the field optional.

## Decision outcome

Chosen option: **"3 — a model may have no application, and one that has none is a
library"**, because it adds the capability without moving anything that already
works, and because it is the option whose migration is empty.

### 1. `applicationId` becomes optional, and its absence means something

A model with an `applicationId` is what it is today: owned by that application,
sharing its scope, resolved by the processes in it.

A model **without** one is a **library model**. It belongs to the server, every
application resolves against it, and it is where a `Customer` that three applications
mean the same way is authored once.

There is no third state and no migration: every existing model carries an
application, so every existing model keeps its meaning, its permissions and its
resolution unchanged.

### 2. What governs a library model

The application is the permission anchor, and a library model has no application. The
rule is therefore the route's own role rather than a new grant table — no second ACL,
per the drivers:

| Act | Who |
|-----|-----|
| Read | any **modeler** — every information-model route already requires that role |
| Create, edit | any **modeler** |
| Delete | **administrator** only |

Deleting is the one act that is deliberately narrower than creating it. A library
model is resolved against by every application on the server, so deleting one reaches
diagrams its author never saw; creating or editing one is visible in the Problems
panel of everything it touches, while a deletion is visible only as an absence.
Requiring a login is the default ([ADR-0195](0195-auth-on-by-default.md)); where an
installation has turned it off the server is open by declaration
([ADR-0205](0205-connector-ownership-and-event-delivery.md) says it in those words),
so every caller is an administrator and none of the rows above ever refuses.

This is deliberately a rule and not an ACL. If it turns out that a library needs
owners and members of its own, that is a record of its own, and the connector scope
([ADR-0205](0205-connector-ownership-and-event-delivery.md)) is the pattern it would
follow.

### 3. Resolution is the union, and a name in two places is refused

`VocabularyOnLoop(applicationID)` becomes the union of the library models and that
application's own. That is the whole feature: a process in any application resolves
`itemSubjectRef="Customer"` against the library's `Customer` without anybody copying
it.

**A class name defined in both a library model and an application model is refused at
the point of writing**, in both directions: a library model whose class name an
application already defines is refused, and an application model whose class name the
library already defines is refused. The refusal names the class and the model on the
other side.

The alternative — letting one side win — was considered and rejected. Precedence is
easy to implement and impossible to read: a modeler looking at `Customer` in their
application's model would have no way to tell, from anything on screen, whether that
is the definition their process actually resolves to. A rule that silently changes
what a diagram means is worse than a refusal that says what to do about it.

Refusing at **write** time, not deploy time, is what keeps ADR-0230 §3 intact. A
deploy still never fails on the vocabulary; the state a deploy would have had to
refuse cannot be reached, because the write that would create it is where the refusal
lives. Two *applications* defining the same name remain untouched and legal — they
are separate vocabularies and always were.

### 4. What this does not do

- It does not merge two models. A library model and an application model stay two
  documents; the union is computed at resolution, never written.
- It does not let an application "import" or fork a library class. Reuse is by
  resolving the same name, not by copying a definition.
- It does not give the library a namespace or a prefix. A class name is a name, and
  §3 keeps it unique across everything an application can see.

### Consequences

- **Positive:** an attribute is authored once and resolves in every application; the
  business key survives the application boundary, which is what makes cross-process
  identity mean anything at more than one application's scale.
- **Positive:** no migration. Every existing model, permission and deploy is
  bit-for-bit what it was.
- **Negative / trade-offs accepted:** a modeler with the role may edit the shared
  vocabulary of the whole server. That is broader than an application's editor right,
  and it is the price of not inventing an ACL in this record.
- **Negative / trade-offs accepted:** the name-clash refusal can block a write that
  used to succeed — an application adding a `Customer` the library already defines.
  That is the strict reading, chosen deliberately over a silent precedence rule.
- **Follow-ups / risks to watch:** if libraries multiply, "which library" becomes a
  question this record does not answer — there is one, flat, and that is on purpose.
  Ownership for library models (per ADR-0205's connector scope) is the named
  follow-up if the role rule proves too open.

## Pros and cons of the options

### Option 1 — every model becomes global
- Good: the simplest possible statement of the goal, and one vocabulary by construction.
- Bad: removes the permission anchor from every existing model, requires migrating
  all of them, and makes every class name on the server significant at once —
  including the ones two applications legitimately disagree about today.

### Option 2 — a model with a share list
- Good: keeps the owner and the existing ACL; mirrors how applications are shared.
- Bad: does not actually answer the question. A shared `Customer` still belongs to
  whichever application happened to author it first, and the second application's
  modeler cannot maintain the vocabulary they depend on without rights on somebody
  else's application. It solves reuse and leaves ownership in the wrong place.

### Option 3 — a library model (chosen)
- Good: additive, unmigrated, and the absence of an application is a statement rather
  than a missing field.
- Bad: needs a rule for what governs a model no application owns, and that rule is
  coarser than an ACL.

## Links

- relates to [ADR-0230](0230-process-information-model.md) — the information model this widens
- relates to [ADR-0071](0071-sharing-scopes.md) — the sharing scope a per-application model keeps
- relates to [ADR-0128](0128-process-applications.md) — process applications
- relates to [ADR-0205](0205-connector-ownership-and-event-delivery.md) — the ownership pattern a library would follow if the role rule proves too open
- relates to [ADR-0195](0195-auth-on-by-default.md) — requiring a login is the default, so the role rule is the normal case
