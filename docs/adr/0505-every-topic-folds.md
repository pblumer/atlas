# ADR-0505: Every topic of a Console page folds, the same way, and the browser remembers

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-27
- **Deciders:** pblumer (maintainer; asked on 2026-10-27 for every topic to fold and remember its state, the same way everywhere)
- **Open question:** whether the folds drawn as `<details>` — Console › Events' two sections, Operations › Workers' type list, the cards of Operations › Diagnostics › Mockups — should move onto this mechanism too; they look different, the first two remember their state under keys of their own and the mockup cards do not remember it at all, and this record leaves them as they are
- **Question checked:** 2026-10

## Context and problem statement

A Console page is a column of cards, and most cards are one topic under an `<h2>`:
Console › Administration › Security has five.
Whether a topic could be folded away depended on whether the page that draws it had
been written to allow it, and when it had been, on which of three ways it chose:

- `sectionHead` / `sectionBody` in `app.js`: a caret button in the heading, the state
  in `localStorage` under `atlas.sec.<id>`. Console › Workers and an application's
  Deployments tab use it, five sections in all.
- `<details>`: Console › Events (`events.js`) and the Worker Types on Operations ›
  Workers, each remembered under a key of its own, and the cards of Operations ›
  Diagnostics › Mockups (`mockups.js`), not remembered.
- Nothing: every other page, the Security page among them.

So a person learnt per page whether a topic folds, and how, and the pages that most
need it, the long administration pages, were the ones that did not. The question is
how every topic gets the same fold without every page having to be rewritten to ask for
it, and without the next page forgetting to.

## Decision drivers

- **The same on every page.** One look, one behaviour, one memory.
- **No page has to ask.** A rule each page must remember is a rule the next page
  forgets: that is how three variants came about.
- **Remembered in the browser,** per page and per topic, and open until somebody folds it.
- **Nothing hidden that should not be.** A form a heading's button opens must not open
  inside a folded card, and a card redrawn into something else must not stay hidden.
- **No cost on the modeler.** Its canvas mutates by the thousand; nothing may watch it.

## Considered options

1. **Convert every page to `sectionHead`.** Each card's markup changes to the helper.
2. **Enhance by structure, once, in the router**, the way `enhanceViewTables` gives every
   list its sort and filter row (ADR-0286): a card whose first element is its `<h2>`, or a
   row holding its `<h2>`, is a topic, and gets the toggle wherever it is drawn.
3. **A live observer over the whole view's subtree**, enhancing whatever card appears.

## Decision outcome

Chosen option: **2, enhance by structure**, because it is the only one that reaches the
pages nobody revisits, and it reaches the next page too without anybody remembering.

- `api/web/fold.js` (a leaf module, like `table.js`) finds the topics under a root and
  gives each the toggle `sectionHead` draws: the heading's words become a
  `.section-toggle` button with the caret, `aria-expanded` says the state. A heading that
  carries a link or a button keeps it, and gets the caret beside its words.
- A topic is a `.card` whose first element is an `<h2>`, or holds one as a direct child
  (the `.between` row with the card's actions). An `<h1>` card is the page's title, an
  `<h3>` heads a form or a part of a topic, a heading with nothing under it has nothing to
  fold, and a section drawn with `sectionHead` keeps its own toggle. A card opts out with
  class `no-fold`.
- Folded, the card shows its heading row and nothing else (`app.css`:
  `.card[data-folded] > :not(.fold-head)`), the card's own actions included, so "New
  policy" or "New secret" stays at hand. A click on such an action opens the card before
  the action runs, because what it opens appears below the heading.
- The state is kept under `atlas.sec.<page>/<topic>`, `sectionHead`'s namespace. The page
  is the route's first two segments, so every application's page shares one state; the
  topic is the card's `data-fold`, else its `id`, else its heading's words without digits
  (a count in a heading would otherwise make a new topic every time it changed).
- `route()` folds the view's cards after it renders, next to `enhanceViewTables`. A page
  that redraws itself by replacing the view's contents is caught by an observer on the
  view's own children only, and a card that redraws its contents by one on that card's
  own children only: neither watches a subtree, so the modeler's canvas costs nothing.

### Consequences

- **Positive:** every topic on every page folds the same way, without a line on those
  pages: Security's five topics and the mockup cards on Console › Workers are the first.
- **Positive:** the `sectionHead` sections keep working and look the same, so the two
  mechanisms read as one.
- **Negative / trade-offs accepted:** a topic keyed by its heading's words is keyed by
  their language: switched from German to English, it opens again once. A page that wants
  the state to survive that gives the card an `id` or `data-fold`.
- **Negative / trade-offs accepted:** a card drawn later into a container other than the
  view itself, without going through `route()`, is not found until the next navigation;
  `foldCards` is exported for the page that does that, as ADR-0286 asks a page that
  rebuilds a table to call `enhanceViewTables`.
- **Follow-ups / risks to watch:** the `<details>`-based folds named in the open question.

## Pros and cons of the options

### Option 1 — convert every page to `sectionHead`
- Good: explicit, each section names its key.
- Bad: some 190 card templates across the web modules to rewrite, and every future card
  has to remember it; the gap that produced three variants stays open.

### Option 2 — enhance by structure in the router
- Good: one place, every page, including those not yet written.
- Bad: a key derived from words, unless the card names one; a card must be shaped like a
  topic to be found.

### Option 3 — a live observer over the view's subtree
- Good: catches every card, however late it is drawn.
- Bad: sees every mutation of the modeler's canvas, which is the cost `enhanceViewTables`
  was written to avoid.

## Links

- relates to [ADR-0286](0286-a-list-carries-its-own-search.md) (the same pass for lists)
- relates to [ADR-0243](0243-shared-ui-primitives.md) (shared UI primitives)
