# ADR-0358: A product names one Atlas form, and the order line carries the answers

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Atlas maintainers
- **Open question:** Whether a release should ever pin a form *version*. Forms have
  no versions today, and the answers a line carries are what has to survive — but a
  form that gains a **required** field next week leaves every earlier order without
  a value for it, and a provisioning process reading that field finds nothing. That
  is true of every variable a process reads and is the process's business, until the
  day somebody makes an access review depend on a field that half the orders predate.
  Versioning forms is a change to the form store, not to the catalogue, and it stays
  unmade until something needs it.
- **Question checked:** 2026-09

## Context and problem statement

A laptop is not fully described by being a laptop. Somebody has to say which cost
centre it is booked to, which site it goes to, which employee number it belongs
to. The story names the case directly:

> As a requester I want to record **additional information that pins down the
> configuration** when I order.

Nothing in Atlas could hold that. A product declared no fields, and an order line
carried no values, so every order needing more than a product name finished as a
phone call — and the answer, when it was given, lived in whatever the caller wrote
down. Variants do not solve it: a variant is a fixed shape the catalogue author
chose in advance, and a cost centre is not one of a list.

## Decision drivers

- Whatever declares the fields has to be authorable by the person who maintains the
  product, and renderable without a second UI being written for it.
- The answers are evidence. An approver reads them, a provisioning process acts on
  them, and an access review months later asks what was booked where.
- The catalogue must not start interpreting field types. It is a design-time model
  that has kept rendering out of itself on purpose.
- A release freezes what an order was placed against. Whatever is added has to have
  an answer to what freezing means for it.

## Considered options

1. **A field list on the product**, declared in the catalogue's own vocabulary.
2. **A form id on the product**, naming an Atlas form.
3. **Free text on the order**, one note per line.

## Decision outcome

Chosen option: **"a form id on the product"** — `Item.ConfigForm`, and
`Line.ConfigForm` + `Line.Config` on the order.

### Why a form id and not a field list of its own

Atlas already has forms: a definition with an id, an editor, a generator, a
renderer, and two surfaces rendering them — a user task's work form and an
incident's repair form. A second way to say "these are the fields somebody fills
in" would be a second thing to author, a second thing to render, and a second set
of field types, validation rules and localisation to keep level with the first. It
would be behind on the day it shipped.

So the catalogue names an id and **interprets nothing**. Which questions there are,
which are required and what counts as valid are the form's own statements, and the
portal renders them with the runtime the rest of the product already uses.

### The catalogue never resolves the id

It cannot: the form store belongs to the `api` package and `api/catalog` cannot see
it — exactly as it cannot see which processes are deployed, which is why the two
provisioning bindings are stored as plain ids too. Publishing checks only that the
id is not whitespace, which is the one thing a string can be wrong about on its own
terms.

The real check is where somebody chooses: the authoring screen offers the forms
that exist and nothing else, the same rule the process bindings follow, for the
same reason — a product bound to a form nobody wrote is a basket the orderer cannot
get past, found by them rather than by the person who bound it. A product bound to
a form that has since been deleted still shows its id, marked, so that opening the
editor and saving cannot quietly clear the binding.

### The release freezes the id; the line freezes the answers

This is the part worth being precise about, because the freeze argument is easy to
over-apply.

A release freezes **rules** — the approval rule, the ceiling on how long a right may
last, the provisioning bindings — and the reason is always the same sentence: a
rule relaxed next week must not change what somebody was held to this week. Those
are claims about the past that a later edit would falsify.

A form is not a rule. It is a set of questions, and what has to survive is the
**answers**: "cost centre 4711" stays true whatever the form does afterwards. So
the line carries the answers with their field keys, and the form's id beside them —
because a map of keys with no form is data nobody can interpret, and a form id on a
placed line with no answers means the question was never asked.

The steelman for the other choice, copying the schema into the release, is real:
the release would then be self-describing, and a provisioning process's inputs
would be stable by construction. It was refused for two reasons. It puts a
rendering artifact inside a design-time model that has kept rendering out of
itself, and it sends that artifact to every browser that opens the portal — the
release is what the page downloads to draw the catalogue. The cost of refusing it
is stated in the open question above rather than hidden.

### Answers are keyed by item, and strays are refused

Two products in one basket legitimately ask the same question — two laptops, two
cost centres — so a flat map would silently keep one answer. They are keyed by item
id.

Two things are refused rather than dropped, both because the alternative is an
order that silently loses something somebody typed:

- **Answers for a product the order does not carry.** Almost always a stale basket:
  the product was taken out and its answers were not. Dropping them would leave
  somebody certain they had given a cost centre the order does not have.
- **Answers for a product that asks nothing.** The catalogue does not know what to
  do with them and no process will read them, so storing them would put data in the
  record that nothing can interpret.

What is deliberately **not** refused here is a form left unanswered. Whether a
field is required is the form's own statement, checked by the form runtime in the
browser before anything is sent. Re-deciding it in the order service would be a
second copy of a rule that already exists, wrong the first time somebody marks a
field optional.

The count of answers per line is bounded, because the map arrives whole in a
request body and without a ceiling one request can grow the order store without
bound.

### Consequences

- **Positive:** the story is answered with one field on the product and one on the
  line; nothing new to author, render or localise; the answers are evidence an
  approver reads and a process can act on.
- **Negative / trade-offs accepted:** the catalogue holds an id it cannot verify,
  and a deleted form is only visible as a stale binding. A form that gains a
  required field does not reach back into orders already placed — see the open
  question.
- **Follow-ups / risks to watch:** the answers are on the order and not yet in the
  fulfilment process's start variables. A process that needs them reads the order,
  which is correct but indirect; if a model ever needs them at the moment a line
  starts, that is the next slice.

## Pros and cons of the options

### Option 1 — a field list in the catalogue's own vocabulary
- Good: self-contained; a release could freeze it without reaching outside.
- Bad: a second form system, behind the first on the day it ships — types,
  validation, localisation, an editor, a renderer, all again.

### Option 2 — a form id on the product
- Good: one field; reuses the authoring, generation and rendering that exist; the
  catalogue interprets nothing.
- Bad: an id the catalogue cannot verify; the form is not frozen with the release.

### Option 3 — free text per line
- Good: nothing to declare, nothing to render.
- Bad: nothing can act on it. "Kostenstelle 4711, bitte Bern" is a sentence a human
  reads and a provisioning process cannot, which is the phone call written down
  rather than replaced.

## Links

- relates to ADR-0028 — the form store this names
- relates to ADR-0312 — the three models, and why the catalogue is one of them
- relates to ADR-0344 — the ceiling, an example of what a release *does* freeze and
  why
