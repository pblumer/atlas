# ADR-DRAFT: A category is a heading a product writes on itself, not a thing the catalogue owns

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Open question:** Whether a heading ever needs to be **addressed** — renamed
  everywhere at once, translated, given a description, ordered by something other
  than the alphabet, or made the subject of a rule ("everything under *Hardware*
  needs the asset manager"). None of that can be had from a string, and all of it
  arrives the same way: the heading becomes an entity with an identity, and the
  field on the product becomes a reference to it. This decision does not block
  that migration and does not begin it. It stays a string until somebody names a
  surface that must talk *about* a category rather than *show* one.
- **Question checked:** 2026-09

## Context and problem statement

The portal's cascade has drawn four columns since the layout landed —
**Kategorie**, Bundle, Angebot, Service. The first one was filled with the
catalogue's own name and a note reading "Atlas has no category level above the
bundle today". It was a placeholder that told the truth: there was nowhere for a
product to say what kind of thing it was.

> As a person ordering I want to find what I need without reading the whole
> catalogue.

A catalogue of eight products does not need headings. A catalogue of two hundred
is unusable without them, and the column where they belong was already on screen,
apologising for being empty.

## Decision drivers

- The column exists and must either carry data or go away. A note that outlives
  its cause is worse than no note: it tells a reader who is looking at their own
  headings that the feature does not exist.
- A catalogue's maintainer must be able to add a heading without an administrator
  creating anything first.
- Nothing in Atlas branches on a category. No rule, no approval, no eligibility,
  no process binding reads it. It is a way of *looking* at a release.
- Whatever is chosen is frozen into the release like everything else on a product,
  because the release is what the portal reads.

## Considered options

1. **An entity**: categories as their own objects with an id, texts per locale,
   a rank, and a reference from each product.
2. **A field**: a plain string on the product.
3. **Nothing**: keep the placeholder, group by bundle only.

## Decision outcome

Chosen option: **a field** — `Item.Category string`, frozen into the release,
grouped by the portal, with a named bucket for the products that carry none.

### Why a field

Because nothing reads it but the eye. Every property that would justify an entity
— identity, translation, ordering, being referred to by a rule — is a property
something *else* needs, and no such something exists. An entity built for a
consumer that does not exist is an entity whose shape is guessed, and the guess is
then frozen into an API, a store and an admin surface before anybody has learned
what a category has to do here.

A string can also be typed by the person who already has the product open. That
is not a convenience: it is the difference between "add a heading" and "ask
whoever administers categories to add a heading, then come back". The catalogue
this serves is maintained by people, not by a data governance process.

**The three costs are real and are accepted, not hidden:**

- **No ordering of its own.** The headings sort alphabetically, by the locale's own
  rule (`localeCompare`). A maintainer who wants *Hardware* above *Software* cannot
  have it. A rank on a category is exactly the entity this decision refused,
  arriving through the back door, and a test holds the sort against it.
- **No translation.** The heading reads the same in every locale, unlike every
  other text on a product. A person using the English portal sees `Arbeitsplatz`.
  That is a genuine regression against the rest of the surface and the honest price
  of not having an entity with texts.
- **Two spellings are two categories.** `Hardware` and `hardware ` are two
  headings, and nothing notices. A test records this as a deliberate non-check
  rather than a gap, so that the day it becomes intolerable, the reason it was
  tolerable is on file.

### What publishing checks, and what it does not

One thing: a category that is **present and blank**. Blank is already the bucket's
own value, so a product with `"category": ""` would be indistinguishable from one
that never said anything — except that somebody meant to say something and lost
it. Publishing refuses it; a product with no category field at all is normal and
lands in the named bucket.

Nothing else is checked. A catalogue cannot know whether two spellings are one
heading, and a validator that normalised case or whitespace would be inventing the
identity this decision declined to give.

### How the column behaves

- **Alle** sits above the headings, so the column is never a dead end: a heading
  opened by accident can be closed again.
- The headings follow, alphabetically.
- **Ohne Kategorie** is last, and appears *only when something is in it*. A heading
  for nothing is a heading nobody can use; hiding uncategorised products instead
  would lose them, which is the one outcome worse than an ugly row.
- Picking a heading clears the columns to its right. A bundle that does not belong
  under the new heading would otherwise stay open beside a column that no longer
  selects it.

The services view — what a person already holds — groups by the same headings, so
"where do I find this" has one answer on both sides of the portal.

### Consequences

- **Positive:** a two-hundred-product catalogue is navigable; the placeholder note
  is gone because its cause is gone; a maintainer adds a heading by typing it, with
  a datalist of the headings already in the catalogue so the second product spells
  it the same way as the first.
- **Negative / trade-offs accepted:** the three costs above — no ordering, no
  translation, no identity.
- **Follow-ups / risks to watch:** the migration to an entity, if it comes, is
  mechanical (strings become references, the distinct strings become the first
  entities) but it is a migration, and the longer a catalogue runs the more
  near-duplicate headings it will have accumulated to reconcile.

## Pros and cons of the options

### Option 1 — an entity
- Good: ordering, translation, descriptions, rules that address a category;
  one spelling by construction.
- Bad: an id, a store, an admin surface and an API for something nothing reads;
  its shape guessed from no consumer; a maintainer who cannot add a heading
  without help.

### Option 2 — a field
- Good: the column carries data today; the maintainer is the person with the
  product open; no shape is frozen before it is understood.
- Bad: untranslated, unordered, unreconciled.

### Option 3 — nothing
- Good: no decision to undo.
- Bad: leaves a column on screen whose only content is an apology, and leaves the
  large catalogue unusable. The story was asked for.

## Links

- relates to ADR-draft-product-price — the other field added to a product for the
  eye rather than for a rule, and decided the same way
- relates to ADR-0312 — the three models, and why the release is the frozen one
