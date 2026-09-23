# ADR-DRAFT: The portal offers the languages its catalogue is kept in

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-23
- **Deciders:** Atlas maintainers

## Context and problem statement

The portal's language switch offered the languages the *page* is translated into
— German and English — as a fixed pair, read straight off its own message
catalogue. That was right while a catalogue was a German thing with an English
translation. It stopped being right the moment a catalogue could declare its own
list.

Two states it produces, and both are worse than no switch at all:

- **A catalogue kept only in German still shows an EN button.** Pressing it turns
  the furniture English and leaves every product name, description and heading
  German. The page has offered a language it cannot deliver — which is exactly the
  half-translated screen ADR-0267 refuses to reach by guessing at the browser, and
  the condition ADR-0313 exists to hold.
- **A catalogue kept in French has no FR button.** The French names are stored,
  frozen into releases and shown to nobody, because no locale on the page selects
  them.

The switch was answering a question about the page when the question is about the
catalogue.

## Decision drivers

- **Never offer a language the page cannot render.** ADR-0313 states the condition
  and a test holds it; a switch that widens what is offered has to respect it or
  the record is an intention.
- **Never offer a language the catalogue is not kept in.** That is the first state
  above, and it is the same defect from the other side.
- **The switch has to exist before the sign-in.** A German-speaking visitor
  meeting an English form is what the whole message catalogue exists to avoid, and
  there is no catalogue to consult at that point.
- **A choice made before the catalogue is read has to survive it.** The locale is
  picked from the address, this browser or the visitor's own list, all of which
  give a language and not one of this catalogue's tags.

## Considered options

1. **Keep the page's two.** The state of affairs.
2. **Offer the catalogue's languages, all of them**, and let the furniture fall
   back where the page has no strings.
3. **Offer the catalogue's languages that the page can render.**

## Decision outcome

Chosen option: **"offer the catalogue's languages that the page can render"**.

`offeredLocales()` is the catalogue's declared list, filtered to the tags whose
language this page has strings for — German, English, French and Italian. With no catalogue — the sign-in screen, or a
visitor who is nobody's audience — it is the page's own languages, because the
switch must be reachable there and there is nothing else to go on.

Where that leaves **one** language, no switch is drawn at all: a control with a
single position says something can be changed and then cannot.

`settleLocale()` moves the chosen language onto one of the offered tags once the
catalogue is known: the same language in another tag first (`en` meeting a
catalogue kept in `en-EN`), then the catalogue's own first language. It never
widens a choice — a reader who chose English and meets a German-only catalogue
gets German, because there is no English here to give them.

### Consequences

- **Positive:** the switch tells the truth. Every button it shows leads to a page
  that is translated, furniture and contents both.
- **Positive:** a catalogue kept in `de-CH` and `en-GB` works, because the
  filtering and the settling both go by the tag's language rather than the whole
  tag (ADR-0413, as amended).
- **Negative / trade-offs accepted:** a reader whose browser is English, meeting a
  German-only catalogue, now gets a German page and cannot switch the furniture to
  English. Before, they could read the navigation in English beside German
  products. That is a real loss for them, and it is the half-translated screen
  ADR-0313 forbids: coherence was chosen over the fragment.
- ~~**Negative / trade-offs accepted:** a catalogue kept in French still shows no
  FR button.~~ **Closed the same day.** The portal's own words were written in
  French and Italian, so it speaks four: German, English, French, Italian. A
  catalogue kept in any of them is offered in it, furniture and contents both. The
  cost this named was real for as long as it existed, and the way out of it was
  the one the record pointed at — translating the page, not widening what it
  offers.
- **Follow-ups / risks to watch:** the page's own languages are four now, and the
  number of catalogue languages is unbounded. Adding French and Italian took no
  change to this mechanism at all, which is what it was built for and is now
  evidence rather than intention. A fifth is 148 strings and nothing else.
- **Negative / trade-offs accepted:** this is the ORDERER's surface. An approval
  is an ordinary user task and is decided in the Console's inbox (ADR-0394), whose
  message catalogue is German and English (ADR-0267). So a catalogue kept in four
  languages is ordered from in four and approved in two: somebody ordering in
  French has their approval read in German, which is that catalogue's default.
  Extending the inbox to French and Italian is 81 strings and was **considered and
  declined** by the maintainer when this landed. It is not an oversight and it is
  not free to change one's mind about later — ADR-0267's reason stands, that a
  translated Tasks app inside an English Console is a half-translated screen, and
  German already pays that price deliberately.
- **Follow-ups / risks to watch:** the French and Italian were written by the
  author of this change and have not been read by a native speaker of either. They
  are consistent in register — formal throughout, as the German is — and the terms
  follow the German source rather than inventing a vocabulary. A review by someone
  who reads the language daily is worth having before this is put in front of the
  people it is for.

## Pros and cons of the options

### Option 1 — keep the page's two
- Good: nothing to do, and the sign-in case needs no special handling.
- Bad: offers a language the catalogue is not kept in, which is a button that
  half-translates the page.

### Option 2 — offer all the catalogue's languages
- Good: the French names become reachable.
- Bad: it breaks ADR-0313 head-on. The furniture would fall back to German while
  the contents are French, which is the screen that record forbids — and it would
  be offered deliberately rather than guessed at, which is worse.

### Option 3 — the intersection (chosen)
- Good: every button leads to a page that is whole.
- Bad: a language the catalogue has and the page does not is silently absent. The
  record says so out loud instead.

## Links

- relates to ADR-0313 — every string the portal renders exists in every locale it
  offers, which is the condition this narrowing keeps
- relates to ADR-0267 — the browser's language is not guessed at, for the same
  reason this does not widen
- relates to ADR-0413 — a language tag is matched by its language, which is what
  makes `de-CH` and `en-GB` work here
