# ADR-0505: Every topic of a Console page folds, the same way, and the browser remembers

- **Status:** Accepted (amended 2026-10-30: a topic outside a card asks with `fold-topic`, and the pages the rule missed are shaped so it finds them; amended 2026-11-02: the `<details>` folds move onto it, closing the open question)
- **Implementation:** Landed
- **Date:** 2026-10-27
- **Deciders:** pblumer (maintainer; asked on 2026-10-27 for every topic to fold and remember its state, the same way everywhere)

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

## Amendment (2026-10-30): the topics the rule did not find

Asked on 2026-10-30 for every page with two or more sections to fold all of them (a page
with one section may keep it unfolded), an audit of every route found topics the rule
above could not see, in four shapes:

- **A heading that is not an `<h2>`.** Operations › Workers headed its cards with a bold
  word, the catalogue list its sections with `<h3>`, Data › Instances its type cards with
  spans, Backup its two cards with `<h1>`. Each is now an `<h2>` (Backup keeps one `<h1>`,
  the page's title, above two `<h2>` cards); nothing in `fold.js` changed for them.
- **A topic outside a card**: an `<h2>` over a list of cards (Connected accounts,
  Approvals), a heading above the card it introduces (the catalogue list's import and
  its names from other installations), the sections of a business object's page, the
  two columns of Planned against built, the choices of Display settings. The rule finds cards, and making these
  cards would draw frames where the pages have none, so such an element asks with class
  `fold-topic`. It folds like a card, by the same CSS, and is the only shape that has to
  ask; a `<section>` that does not ask is left alone, because the Console uses `<section>`
  for panels and groups that are not topics.
- **A card filled after the route ran.** The dashboard's alerts and storage card arrive
  in slots each module fills when its answer comes, and Operations › Workers fills a card
  on a later Refresh. As the decision foresaw, those pages fold again themselves: the
  dashboard watches each slot's own children, and the Workers page calls `foldCards` after
  it redraws.
- **A tab that loads later.** An application's Constants tab draws its two lists with
  `sectionHead`, as its Deployments tab does; on Admin › Engine the same lists stay parts
  of one card, which folds as a whole.

Operations › Workers' job types, one of the `<details>` folds of the open question, moved
onto this mechanism with them, because on that page it was the one card that folded
differently. It starts folded, as it did: a card may say `data-fold-default="closed"`,
and is closed until somebody opens it, then remembered like any other. Its old key,
`atlas.workers.types`, is no longer read, so a browser that had opened it sees it folded
once more.

A headless card directly under a page's title (the Modeler's applications, the incident
causes) is the page's own subject, not a section, and has no heading to carry a caret.
Pane layouts (the task inbox, the live view, an instance's replay) are not stacked topics
and are left as they are. The other `<details>` folds of the open question — Console ›
Events, the mockup cards, a catalogue's own page, and the dashboard's release notes and
key features — still fold, each the same way across its page, and remain as they were.

## Amendment (2026-11-02): the `<details>` folds move onto it

Asked on 2026-11-02, the open question is answered yes: every fold drawn as a
`<details>` of its own now folds the same way, with the same caret and the same memory.

- **Console › Events**: its two sections are `fold-topic`s, each with its heading and the
  line saying what it holds and warns about in the heading row. A link from the Modeler
  that leads to a row in a folded section opens it with `unfold`, which `fold.js` exports
  for a page that is about to show something inside a topic; it also works before the
  router has folded the page, because it writes what that pass reads.
- **The mockup cards** of Operations › Mockups, Mock directory and Mock database: each
  worker's card is headed by its `<h2>`, keyed by the worker, and the page folds its
  cards again after each refresh that redraws them. The folds inside a card — a list,
  the operations, an entry — stay `<details>`, remembered across a refresh as before: they
  are parts of a topic, not topics.
- **The dashboard's release notes and key features**: each a card headed by its `<h2>`,
  keyed independently of the language it is read in, and folded again when it is
  repainted. The releases and changes inside the release notes stay `<details>`.
- **A catalogue's own page**: each section is a `fold-topic` that starts folded
  (`data-fold-default`), with its step number inside the heading and its one-line
  status beside it, so a folded section still says what it holds. The page folds its
  sections itself, so the harness that mounts it without the router folds them too.

The keys these folds were remembered under (`atlas.events.catalog`, `atlas.events.own`,
`atlas.catalog.pageSections`) are no longer read: a browser that had folded or opened one
sees it in its default state once more. What stays a `<details>` is a fold inside a
topic — a product form's groups, an outbox message, a release in the release notes —
and the panels of the canvases, which are not stacked topics. Such a fold is marked
`fold-sub` and draws the caret a topic draws, turning the same way, so the two read as
one; a `<details>` a library draws (form-js, the API reference) is not marked and keeps
its own marker.

## Links

- relates to [ADR-0286](0286-a-list-carries-its-own-search.md) (the same pass for lists)
- relates to [ADR-0243](0243-shared-ui-primitives.md) (shared UI primitives)
