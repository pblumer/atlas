# ADR-0414: A missing translation is reported, not refused

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-23
- **Deciders:** Atlas maintainers

## Context and problem statement

Publishing refused a product named in one of its catalogue's declared languages
and not another, and — once there was one at all — a description or a heading
wording missing in any of them. The argument was written down and it was a good
one: a portal that shows one audience a product and the other an empty row is
worse than no catalogue at all, and the moment to catch that is the moment the
catalogue is proved (I5).

The premise was wrong, and it had been wrong since before the rule existed. **The
portal never shows an empty row.** `textOf` asks for the reader's locale, then
German, then English, then whatever the catalogue does have, because a name in a
language somebody does not read is better than no name; `descriptionOf` learned
the same lesson separately and carries a comment about it. So the refusal was not
protecting a reader from a blank. What it actually did was hold a usable
catalogue back until the last translation arrived — which means the readers of
the first language waited on the translator of the second.

That cost is not hypothetical either. A catalogue kept in German and English, with
forty products written in German, cannot go live at all until somebody has
translated all forty. The rule that was meant to stop a half-finished catalogue
reaching readers stopped a *usable* one reaching them, and the alternative it left
was to not declare the second language — which is worse, because then nothing
anywhere records that the translations are owed.

## Decision drivers

- **A reader must never meet a blank or an id.** This is the part of the old rule
  that was real, and it has to survive whatever replaces it.
- **A gap must not become invisible.** The refusal's genuine value was that it
  could not be ignored. Removing a gate and putting nothing in its place is how
  the half-translated catalogue comes back, and it would arrive months later as
  "the French portal reads oddly", reported by a reader rather than found by a
  maintainer.
- **Publishing answers a question about ordering**, not about polish. Whether a
  release can be ordered against and whether it is as good as it should be are two
  questions, and a refusal can only answer the first.
- **The installed base must keep publishing.** Every catalogue that declares one
  language is unaffected; every catalogue that declares two and is fully
  translated is unaffected. The change must not make anything that publishes today
  stop publishing.

## Considered options

1. **Keep the refusal.** Translate everything before going live.
2. **Refuse the name and report the rest.** The name is what a row *is*; a
   description is an extra.
3. **Report all of it, and refuse only a product with no name in any language.**

## Decision outcome

Chosen option: **"report all of it, and refuse only a product with no name in any
language"**.

`Publish` no longer refuses any per-language gap. What it refuses instead is new
and is not the same rule made smaller: **a product whose name is empty in every
language**. There the fallback has nothing to fall back to, and the portal would
render the product id — a string nobody chose for a reader, on a row whose
maintainer cannot see from the catalogue screen that it is wrong. Whitespace is
not a name, for the reason it is not a description: it is what a cleared box
leaves behind.

`TranslationGaps` is the other half, and it is what makes the removal safe rather
than merely convenient. It is a pure function of the same `Input` a publish is
computed from, so what it reports is exactly what the next publish would have to
live with. It is served at `GET /api/v1/catalog-products/translation-gaps`, over
MCP as `atlas_catalog_translation_gaps`, and drawn as a card on the catalogue
screen beside the approver report.

### What counts as a gap, and what deliberately does not

A field is a gap where the product says it in at least one language and not in
this one. A product nobody described has no description gap — a description is
optional as a whole and always was. A heading carrying its key and no wordings has
none either: the key renders in every language, which is what carrying no wording
means and what every product written before ADR-0412 carries. Reporting those
would name the whole installed base on the first screen somebody opens, and a list
that is never empty is a list nobody reads.

The languages are the **catalogue's**, and one product is offered by several
catalogues declaring different ones (ADR-0315), so a gap names both the catalogue
and the product. The same missing French is a gap in the catalogue that declares
French and no statement at all about the one that does not.

### Consequences

- **Positive:** a catalogue goes live when it is useful rather than when it is
  finished, and what is still owed is written down in one place instead of being
  enforced in a way that stops the work.
- **Positive:** the report reads the catalogues **as they stand** and not their
  releases, unlike the two reports beside it. Those read the newest release because
  only what is published can park an order; this is a list of work to do, and work
  to do is about what is being edited — a maintainer who has just added a second
  language to a catalogue wants the list *before* publishing.
- **Negative / trade-offs accepted:** an installation can now publish a catalogue
  that reads half in German to its French audience, and nothing stops it. That is
  the point of the change, and it is why the card exists and why the card says in
  as many words that a gap is not a refusal — read as one, people would clear it
  before shipping and the removed gate would come back as a habit.
- **Negative / trade-offs accepted:** the guarantee that a published release is
  complete in every declared language is gone, and some installation somewhere was
  relying on it without knowing. The report is the replacement, and it is a weaker
  thing: it has to be looked at.
- **Follow-ups / risks to watch:** ~~variant names are not covered.~~ **Closed the
  same day**: the shapes a product is ordered in are reported too, and they are
  not a cosmetic gap like the others — the portal draws them in the basket, where
  an orderer has to *choose* one, so a German word in an English basket is the
  moment somebody picks. A shape named in **no** language is reported as its own
  finding rather than once per declared language, because it needs naming and not
  translating; it is reported and not refused, unlike a product named nowhere,
  because a shape's id is very often the word itself (`black`, `large`) and the
  portal falling back to it is frequently adequate.

## Pros and cons of the options

### Option 1 — keep the refusal
- Good: a published release is complete, and nobody has to read anything.
- Bad: it blocks on a premise that is false. No reader was ever being protected,
  because the portal falls back.
- Bad: the workaround is to not declare the language, which loses the record that
  the translation is owed — strictly worse than the state being refused.

### Option 2 — refuse the name, report the rest
- Good: keeps the strongest half of the guarantee.
- Bad: the name is exactly where the fallback is oldest and most certain. If the
  fallback is good enough for the description and the heading, the case for
  singling out the name is that it is more visible — which is an argument for
  reporting it first, not for refusing it.

### Option 3 — report all, refuse namelessness (chosen)
- Good: the refusal that remains is the one case the fallback cannot answer.
- Good: one rule to explain, and it is about the reader rather than about the
  language list.
- Bad: the completeness guarantee is gone, and what replaces it must be read.

## Links

- relates to ADR-0412 — the heading as a key and a wording per language, whose
  all-or-nothing rule this relaxes with the rest
- relates to ADR-0315 — a product is referenced by catalogues rather than owned by
  one, which is why a gap names the catalogue as well as the product
- relates to ADR-0018 — every assertion here was watched fail before the code
