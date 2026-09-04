# ADR-DRAFT: Every route says where it is

- **Status:** Proposed
- **Date:** 2026-09-04
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0012 gave Atlas an app shell: a top bar with an app switcher, a per-app
secondary navigation, and a hash router that swaps views into one content mount.
The shell was built for six apps and a handful of pages. It now carries roughly
thirty-five routes across Console, Modeler, Tasks, Operations, Panorama and Data,
and the router that serves them has grown to `api/web/app.js:8333`.

What grew with it is not one router but **five separate answers to the same
question — "where am I?"** — each maintained by hand, each in a different shape:

- `route()` (`app.js:8333`) dispatches the path to a view handler, by a mix of
  `===` comparisons and regular expressions;
- `TOPNAV` (`app.js:526`) lists the secondary navigation, and `setChrome`
  (`app.js:969`) marks an entry active by exact string equality,
  `t.route === route`;
- `routeTitle` (`app.js:8296`) holds a second table, thirty-one regular
  expressions, that names the browser tab;
- the help mapping (`app.js:853–863`) holds a third, nine prefix tests in
  significant order, that points the "?" menu at a handbook chapter;
- `fullBleed` inside `setChrome` holds a fourth, five substring tests, that
  decides whether the view gets the centred content column or the whole width.

A new surface is therefore not one entry but five edits in four shapes, and
nothing fails when one of them is forgotten. Several already were:

**Fifteen detail routes have no active navigation entry.** Because the match is
string equality against a `TOPNAV` route, every deep link falls through it:
`#/modeler/d/{key}`, `#/modeler/draft/{id}`, `#/modeler/p/{id}`,
`#/modeler/form/e/{id}`, `#/modeler/dmn/{id}`, `#/modeler/new`,
`#/tasks/t/{key}`, `#/operations/p/{key}`, `#/operations/p/{key}/i/{key}`,
`#/operations/c/{key}`, `#/operations/i/{key}`, `#/operations/decisions/{id}`,
`#/panorama/models/{id}` and `#/data/m/{id}`. On any of them the secondary
navigation shows the app's pages with none of them marked — the reader is inside
Operations, and the bar declines to say which part of it.

**Eight routes have no page title.** `routeTitle` returns `""` and `setTitle`
falls back to a bare `Atlas` for `#/console/ai-access`, `#/console/audit`,
`#/operations/incidents`, `#/operations/workers`, `#/operations/outbox`,
`#/operations/ad-mock`, `#/operations/sql-mock` and
`#/operations/call-activities` — the last six because `/^#\/operations$/` is
anchored and matches none of the pages under Operations. A bookmark or a second
tab of any of them is unlabelled.

**Three canvas views miss the full-bleed layout.** `fullBleed` names the BPMN
editor, the form editor and the live view; the DMN viewer (`#/modeler/dmn/`),
the class canvas (`#/data/m/`) and the Panorama architecture view
(`#/panorama/models/`) are canvases too and render inside the centred column.

**The way back has four forms.** `class="crumbs"` with a separator and a current
element in three places, the simpler `class="crumb"` at `app.js:2875`, a plain
`← Decisions` text link at `app.js:6415` and `← Project` / `← Modeler` at
`app.js:8036`, and views with no way back at all. Nothing states a parent
relationship, so each view invents the one its author had in mind. There is no
`history.back()` anywhere, which is right — the browser's back button is not a
substitute for knowing where a page sits — but nothing replaces it either.

None of this is a defect in any one view. It is what five hand-maintained lists
do over thirty-five routes.

## Decision drivers

- One route should have one answer to "where am I", not five that can disagree.
- Adding a surface should be one entry, not five edits in four shapes — and the
  one that is forgotten should fail a test, not ship.
- The shell stays buildless (ADR-0012). No router library, no bundler.
- What the shell decides should be checkable without a browser. Today every one
  of these properties can only be observed by driving Playwright.
- The existing e2e specs keep passing unchanged: `role-navigation.spec.mjs`
  (what a role is offered, ADR-0209), `router-reentrancy.spec.mjs` (the `navGen`
  guard against a late-landing handler) and `menu-flyout.spec.mjs`.
- Roles gate navigation (`mayUse`, ADR-0209). Whatever holds the routes must
  carry the role too, or a route becomes visible that its reader cannot use.

## Considered options

1. **Repair the five lists in place.** Make the active-entry match a prefix,
   add the eight missing titles, add the three missing canvases to `fullBleed`,
   and settle on one breadcrumb markup.
1b. **Repair them, and add a test that walks every route** and asserts what a
   reader should get from it, so the class of defect cannot come back even though
   the five lists remain five.
2. **One declarative route table.** A single array of route descriptors — the
   pattern, the app, the view handler, the title, the navigation entry it
   belongs under, the help chapter, the chrome, the parent route, the role.
   `route()`, `setChrome`, `setTitle` and the help menu all read from it.
3. **Vendor a router library**, as ADR-0013 vendors bpmn-js, and express the
   shell's properties as route metadata in its idiom.

## Decision outcome

Chosen option: **"Repair the lists, and guard them with a route sweep"**
(option 1b) — with the route table (option 2) held as a named option rather than
taken now.

The first draft of this record chose the table, on the argument that the thirty
symptoms were not detectable without a browser. That argument does not hold: the
project runs six hundred browser tests, so "needs a browser" is not a cost here.
Once a test can walk every route and say what is missing, the table is no longer
what makes the shell checkable — it is only what makes it cheaper to extend.

So the defects are fixed where they are, and `e2e/route-sweep.spec.mjs` walks
every route the dispatcher serves and asserts two things a reader should always
get:

- **the browser tab names the page** — `routeTitle` gained the eight rules it was
  missing, six of them because `/^#\/operations$/` is anchored and no page under
  Operations inherited from it;
- **the secondary navigation marks where the route sits** — `setChrome` now finds
  the longest entry the route sits under instead of comparing strings, so
  `#/operations/i/9` marks Instances and `#/operations/decisions/x` marks
  Decisions rather than Instances. Equality alone left every deep link marking
  nothing.

The test carries its own list of routes, which is a sixth list and deliberately
so: a test that read the router's own tables could only agree with them. Adding a
route means adding a line there, and that line is what fails when the new route
has no title.

**What the table would still buy, and when to take it.** One entry instead of
five when a surface is added, and the two properties this sweep does not assert —
which chrome a route gets (`fullBleed` still names five routes by substring and
misses three canvases) and where a route's way back points (four markup shapes,
and some views with none). Neither is a defect a reader would call a bug, and
both are why the table is worth keeping on the table. Take it when adding a
surface has been five edits often enough to be felt, or when the way back is
being unified anyway — with the sweep already in place, so the migration has
something to prove itself against.

What does not change either way: the re-entrancy guard. `navGen` and the
`superseded(gen)` checks stay exactly as they are — they solve a different problem
(a handler landing after a newer navigation), and `router-reentrancy.spec.mjs`
keeps guarding them.

### Consequences

- **Positive:** the two defects a reader meets — an unnamed tab, a navigation bar
  that marks nothing — are gone across all forty-one routes, and a forty-second
  route cannot reintroduce them silently. The cost was two rules and one comparison,
  in a file that is the highest-churn in the tree, rather than a rewrite of its
  router.
- **Negative / trade-offs accepted:** the five lists are still five, so adding a
  surface is still five edits in four shapes — two of which now fail a test when
  forgotten, and three of which do not. The sweep's route list has to be kept in
  step by hand; it is one more list, and the one that is supposed to disagree.
- **Follow-ups / risks to watch:** `fullBleed` and the way back are unasserted, and
  both are what the table would settle. The longest-prefix match is a rule about
  route shape: a future entry that is a prefix of an unrelated route would mark the
  wrong section, which the sweep would not catch because it only counts that
  *something* is marked.

## Pros and cons of the options

### Option 1 — Repair the five lists in place
- Good: small, immediate, no migration; each of the known gaps closes today.
- Bad: leaves five lists to keep in step, so the next surface re-opens the same
  gaps. Nothing fails when an entry is forgotten, which is the actual defect.

### Option 1b — Repair them, and sweep every route (chosen)
- Good: closes the same gaps and, unlike option 1, makes the class of defect fail
  a test. A tenth of the work of the table and none of its blast radius.
- Bad: does not reduce five lists to one, so the maintenance cost stays; and it
  asserts what a reader gets, not what a route declares, so chrome and the way
  back stay outside it.

### Option 2 — One declarative route table
- Good: one place to add a surface; the shell's decisions become data and
  therefore testable without a browser; the "where am I" and "where does this
  sit" questions get explicit fields.
- Bad: a one-time migration through the router and every view entry point;
  ordering semantics must be written down rather than implied.

### Option 3 — Vendor a router library
- Good: solved problem, nested routes and parameter parsing included.
- Bad: a dependency and its idiom for a thirty-five-route hash router, against
  ADR-0012's reason for being buildless. The properties we are missing — title,
  help chapter, chrome, parent — are ours, not the library's, so we would be
  writing the same table inside someone else's shape.

## Links

- builds on ADR-0012 (the buildless app shell this table describes)
- honours ADR-0209 (roles decide what a person is offered)
- paired with ADR-draft-shared-ui-primitives, which takes the same question one
  level down: what the views inside the shell are built from
