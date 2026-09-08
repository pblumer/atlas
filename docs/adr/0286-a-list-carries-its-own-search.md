# ADR-0286: A list carries its own search

- **Status:** Proposed
- **Date:** 2026-09-08
- **Deciders:** Atlas maintainers

## Context and problem statement

Every list in the web UI is a `<table>` that `enhanceTable` (`api/web/table.js`)
gives click-to-sort headers and an Excel-style per-column filter row — one text box
under each column header, narrowing the rows to those whose cell contains what was
typed, and combining across columns. It is the shared part
[ADR-0243](0243-shared-ui-primitives.md) counts as working, and it exists precisely so
that no view hand-rolls sorting and filtering again.

That row was **collapsed by default**, behind a small funnel icon at the right end of
the header. The reasoning was vertical space: the row costs about 28px, and a list is
read more often than it is searched. What it actually produced:

- **The search was invisible.** What a user saw on opening any list — Applications,
  Instances, Incidents, Users, Workers — was a table with no visible way to search it.
  Nothing on the screen says the funnel is a search; a filter icon at the far right of
  a header row is not where anyone looks for one.
- **So views grew their own.** The Modeler's application detail put a *Filter
  artifacts…* box above its table, matching the name only. Operations › Instances put a
  *Filter processes by name or ID…* box above its table, matching name and process id,
  rebuilding the rows on every keystroke. Two boxes, two semantics, two implementations,
  both sitting directly above a filter row that already did the same job. The two also
  work differently on the same table: the box re-rendered the rows it kept, the column
  filter hides the rows it drops, so which of the two was narrowing a list decided what
  the list said when nothing matched.
- **Some lists had neither.** `enhanceViewTables` runs once per navigation, over the
  tables the view has already built. A list that builds its table *later* — the audit
  log after a refresh, the variable-search results, an application's Deployments tab —
  replaced the enhanced table with a plain one, and silently lost both its sort headers
  and its filter row. Nothing said so; the table simply stopped responding to clicks.
- **And some tables are not lists.** The enhancer is applied to every `<table>` in the
  view that does not opt out, which had swept in the SSO claim-rule grid and the
  information model's attribute grid. Their cells are `<input>`s and `<select>`s, so
  `textContent` is empty: sorting them does nothing and any filter typed into them
  hides every row. The defect was invisible only because the row was invisible.

The question this record answers is where a list is searched, and what a table has to
be before the shared enhancer is applied to it.

## Decision drivers

- **Discoverability is the point of a search.** A feature reachable only by clicking an
  unlabelled icon is, for most users, a feature that does not exist — the two
  hand-rolled boxes are the evidence, written by people who knew the row was there.
- **One way to narrow a list, in the same place on every list.** A user who learns the
  Applications list has learned Incidents, Workers and Instances.
- **[ADR-0012](0012-web-ui-app-shell.md) stands:** buildless vanilla JS, no framework,
  no table library. Whatever is decided is a rule about an existing helper, not a new
  dependency.
- **A rule nobody can check is a preference** ([ADR-0243](0243-shared-ui-primitives.md)).
  The default has to hold without per-view work, and be visible in a test.
- **Vertical space is real but cheap.** One row per list, and only for lists.

## Considered options

1. **Keep the row collapsed and make the funnel more obvious** — a label, a tooltip, a
   larger target — and forbid per-view search boxes.
2. **Show the row by default** and make it the way a list is searched: the funnel
   inverts into a collapse, per-view boxes over a table are removed, and only tables
   that are lists are enhanced at all.
3. **One search box per view** — a single field above the table matching across all
   columns, replacing the per-column row.

## Decision outcome

Chosen option: **"Show the row by default"** (option 2). A list carries its own search,
in its own header, and that is the only search a list needs.

Four rules follow, and together they are the whole decision:

1. **The filter row is shown with the list.** `enhanceTable` opens it; the funnel now
   collapses it for a list where the vertical space matters more than the search. That
   choice is per list and persists next to the sort column under the table's
   `data-dt-key`, so it survives a refresh and navigating away and back. A table with no
   key opens with its filters showing every time.
2. **A view does not build a search box over a table it owns.** If the column filters
   express it, they are it. A search they cannot express is a *different* feature and
   has to say which: a server-side query over data the page does not hold (the
   variable-content search, the data-instance class/key lookup), a search over something
   that is not a table (the Tasks inbox, the Repository's card grid), or a filter that
   drives more than the rows (the live view's variables panel, whose box also feeds the
   count and the expand-all control). Everything else is the column filter's job.
3. **Only a list is enhanced.** A `<table>` that is a grid of inputs, an editing surface
   or a matrix carries `class="no-enhance"` and says why. The test is whether its cells
   hold text a reader could sort or filter by: if they hold form controls, or the row
   order is meaningful and set by dragging, it is not a list.
4. **A list built after the route's pass enhances itself.** Any code that replaces a
   table — a refresh that rewrites its container, a lazily-loaded pane, a results panel —
   calls `enhanceViewTables()` when it is done. Rebuilding only the `<tbody>` needs
   nothing: the enhancer's own observer re-applies the sort and the live filters.

### Consequences

- **Positive:** the way to search a list is on screen when the list is, and it is the
  same mechanism everywhere — one place to fix, one behaviour to learn, and per-column
  narrowing (name *and* type *and* date) that a single box cannot express. Two
  hand-rolled filter implementations are gone. Three lists that had quietly lost their
  sorting and filters have them back, and the class of defect where a rebuilt table
  loses its behaviour now has a stated remedy. Two grids where filtering would have
  hidden every row are out of the enhancer, where they always belonged.
- **Negative / trade-offs accepted:** every list is about 28px taller, which is most
  noticeable on the short ones (a two-row secrets table now carries a filter row it
  will rarely need). The funnel's highlight changes meaning — it marks *filters set*,
  not *row open*, because with the row shown by default the old highlight would have
  marked every table in the UI as special. Screenshots of lists in the handbook's
  training nuggets are one row out of date until they are re-taken.
- **Follow-ups / risks to watch:** rule 4 is a convention a reviewer has to notice; the
  honest fix would be for `enhanceViewTables` to observe the view, which
  [ADR-0012](0012-web-ui-app-shell.md)'s buildless shell can afford everywhere except
  the modeler's constantly-mutating SVG — worth revisiting if the convention is missed
  twice. **Missed once, 2026-09-08:** the replay's Data tab in `api/web/editor.js`
  rebuilds its list on every element selection, so the list arrived with a filter row
  and lost it on the reader's first click. It re-enhances itself now — but by calling
  `enhanceTable` from `table.js` rather than `enhanceViewTables()` as the rule words it,
  because `app.js` is the shell's entry module and runs `initShell()`, the router and
  three server syncs at module scope: importing it from a view would boot the whole
  application inside each of the thirty-five e2e harnesses that mount that view alone.
  The rule holds; only its wording assumes the caller is already inside `app.js`. One
  more miss and the observer is the answer.

  The same fix turned up something rule 3 does not cover and a reader would not think to
  look for: a table can be a list *and* still hold rows that are not its data. The
  replay's state trail is one, and it carried no `data-dt-detail`, so sorting put it
  under a stranger and a filter hid it under a row it had kept. `no-enhance` would have
  been the wrong answer there — the list around it is a real list. Worth knowing that
  the detail-row marker is as easy to miss as rule 4, and fails more visibly. `no-enhance` is still the only opt-out and it is not checkable: a new editing
  grid that forgets it gets filter boxes that hide its rows, and only a reader will
  notice. If a list ever needs a fuzzy search *across* columns, that is a case for a
  fourth kind of search under rule 2 — and it belongs in the shared helper, not above
  one view's table.

## Pros and cons of the options

### Option 1 — Keep it collapsed, make the funnel obvious
- Good: costs no vertical space; no view changes at all.
- Bad: it is the state we were already in — the icon has a `title` and an `aria-label`
  today, and two views still grew their own boxes rather than rely on it. Making an icon
  louder does not make a hidden feature discoverable; it makes a hidden feature loud.

### Option 2 — Show the row by default (chosen)
- Good: the search is where the list is; one mechanism, learned once; per-column
  narrowing; the per-view boxes lose their reason to exist, so the duplication cannot
  come back without a reviewer seeing it.
- Bad: a row of boxes on lists that will never be searched, and a visibly denser UI on
  a page with several small tables.

### Option 3 — One search box per view
- Good: familiar, cheap, one field.
- Bad: it throws away what the row can do — narrowing by column, and combining columns —
  for the thing two views had already hand-rolled and got subtly different from each
  other. It also keeps the search outside the table, which is what let a filter and a
  table's own sorting disagree.

## Links

- builds on [ADR-0012](0012-web-ui-app-shell.md) — buildless shell, shared DOM helpers
- builds on [ADR-0243](0243-shared-ui-primitives.md) — `enhanceTable` as a shared part a
  view is expected to use rather than re-implement
- relates to [ADR-0163](0163-deleting-a-referenced-connector.md) — a table lives inside
  its card and scrolls there; the filter row is inside the same box
