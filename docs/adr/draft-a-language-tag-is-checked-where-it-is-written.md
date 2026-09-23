# ADR-DRAFT: A language tag is checked where it is written, and nowhere else

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-23
- **Deciders:** Atlas maintainers

## Context and problem statement

A catalogue declares the languages it is kept in, as a list of tags. Every text on
every product is a map keyed by those tags: the name, the description, the variant
names, and — since ADR-0412 — the two heading wordings. The Console draws one box
per declared language. The portal looks up `texts[locale]`.

Nothing anywhere checked that a tag is a tag.

It was found in a live installation. A catalogue had been saved with the single
language `de; en`. The list is read comma-separated, and the maintainer typed the
separator they had been taught one screen down, where ADR-0412 had just introduced
a semicolon convention for the heading fields. What followed was total and silent:

- the product form drew **one** box, labelled `de; en`;
- both names were typed into it, separated by a semicolon, following the same
  convention;
- the portal looked up `texts['de']` and `texts['en']`, found neither, and fell
  through to the first value it had;
- so the language switch did nothing at all, for every product in that catalogue,
  in both languages, with no screen anywhere saying why.

Each layer behaved correctly. A language tag is an opaque map key to all of them,
and none can tell a tag from a sentence: they look it up, find nothing, and fall
back — which is indistinguishable from a product nobody translated. The defect was
reported weeks later as "switching the language does not translate the products",
and the cause was two screens away from the symptom.

## Decision drivers

- **The mistake has to be caught where it is still cheap**, which is the keystroke
  that makes it. Every layer after that is a lookup that cannot distinguish a bad
  key from a missing translation.
- **An installation that already has the defect must be able to fix it.** Whatever
  is refused, the catalogue carrying a bad tag has to stay readable, editable and
  publishable — or the installation with the problem is the one locked out of the
  screen that corrects it.
- **The refusal must name the tag.** "Invalid languages" against a list of four is
  a guess, and a guess is what put the bad tag there.

## Considered options

1. **Normalise on the way in**: split on semicolons too, lowercase, trim.
2. **Refuse at the write**, and leave every read alone.
3. **Refuse at publish**, where the catalogue is already proved (I5).

## Decision outcome

Chosen option: **"refuse at the write, and leave every read alone"**.

`ValidLanguageTag` accepts a primary subtag of two or three letters followed by
any number of subtags of one to eight letters or digits, separated by single
hyphens: `de`, `en`, `de-CH`, `zh-Hans`, `pt-BR`. `LanguageListProblem` runs it
over a catalogue's list and also refuses a repeated tag. Both catalogue write
handlers call it and answer 400 naming the offending entry.

Deliberately narrower than BCP 47, which also admits grandfathered and private-use
forms. A tag this refuses and the standard allows is a tag no catalogue in this
product has ever carried; the cost of that narrowness is one refusal a person can
read, and the cost of being liberal is the section above.

Case is accepted as written and not normalised. A tag is compared as stored, and
re-casing somebody's list would move the key that their texts are already filed
under — turning a cosmetic fix into a silent data loss of exactly the kind this
record exists to prevent.

### Why not at publish

Because publishing is a read of stored data, and the installation that needs this
most is the one that already has the bad tag. Refusing there would mean: you
cannot publish until you fix it, and the screen that fixes it is the one you must
be able to save. The write is the only place where refusing costs nothing to
somebody who has not made the mistake yet.

A guard holds that boundary — a catalogue already carrying `de; en` publishes.

### Why not normalise

Because `de; en` has two readings and only the maintainer knows which: two
languages, or one wrongly typed. Splitting it silently would create an English
column in a catalogue somebody may not have meant to offer in English, and would
do it without saying so. Guessing here is the same class of act as the fallback
that hid the defect in the first place.

### Consequences

- **Positive:** the failure mode is gone at the source, and the one place it can
  still be written — an existing record, or a direct store write — reads and
  publishes exactly as before.
- **Positive:** duplicates are caught too, which is a defect of its own: two boxes
  writing one key, where the second silently wins and the first looks ignored.
- **Negative / trade-offs accepted:** an installation carrying a bad tag is not
  repaired by this. It is refused only when somebody next edits the list, which
  means the fix is prompted rather than applied. Applying it would be a migration
  that guesses what the maintainer meant.
- **Negative / trade-offs accepted:** the Console does not yet flag an existing bad
  tag before somebody tries to save. The refusal arrives on save rather than on
  open, which is later than ideal and still far earlier than a reader noticing.
- **Follow-ups / risks to watch:** the same lack of checking applies to any other
  opaque key this product stores in a map — the accepted narrowness here is a
  pattern worth reaching for the next time a map key is typed by a person.

## Pros and cons of the options

### Option 1 — normalise on the way in
- Good: nobody is ever refused, and the obvious mistake is silently fixed.
- Bad: it guesses. `de; en` may be two languages or one wrongly typed, and
  creating an English column somebody did not ask for is the same silent
  helpfulness that hid the original defect.

### Option 2 — refuse at the write (chosen)
- Good: caught at the keystroke, and every read is untouched, so the installation
  with the defect can still open, edit and publish.
- Bad: existing bad data is prompted rather than repaired.

### Option 3 — refuse at publish
- Good: one gate, where catalogues are already proved.
- Bad: it locks the installation that has the defect out of shipping anything
  until it is fixed, and the mistake has already travelled into every product's
  texts by then.

## Links

- relates to ADR-0412 — the semicolon convention whose separator leaked into this
  field, since replaced by one box per language
- relates to ADR-0360 — the two headings, whose per-language wordings are keyed by
  these tags
