# ADR-0242: One route table describes the shell

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
2. **One declarative route table.** A single array of route descriptors — the
   pattern, the app, the view handler, the title, the navigation entry it
   belongs under, the help chapter, the chrome, the parent route, the role.
   `route()`, `setChrome`, `setTitle` and the help menu all read from it.
3. **Vendor a router library**, as ADR-0013 vendors bpmn-js, and express the
   shell's properties as route metadata in its idiom.

## Decision outcome

Chosen option: **"One declarative route table"** (option 2).

A route is described once, as data:

```js
{ pattern: /^#\/operations\/i\/(\d+)$/, app: "operations", nav: "#/operations",
  title: (m) => `Instance ${m[1]}`, help: "betrieb", chrome: "default",
  parent: "#/operations", role: "operator", view: (m) => viewInstanceReplay(+m[1]) }
```

`route()` finds the first descriptor whose pattern matches and does what it says:
set the app name, mark `nav` active, compose the title, point the help menu at
`help`, apply `chrome`, render `view`. `setChrome` stops deciding anything —
it applies. The four hand-maintained lists become one, and the properties that
used to be forgotten one at a time become fields that a test can require of
every row.

The distinction between where a route *is* and where it *sits* becomes explicit:
`nav` names the navigation entry to mark (so a detail route marks its section)
and `parent` names the route one level up (so a detail view renders one
breadcrumb shape from data instead of hand-writing its own). A page that is
itself a navigation entry has `nav` equal to its own route and no `parent`.

What does not change: the re-entrancy guard. `navGen` and the `superseded(gen)`
checks stay exactly as they are — they solve a different problem (a handler
landing after a newer navigation), and `router-reentrancy.spec.mjs` keeps
guarding them.

### Consequences

- **Positive:** a new surface is one row. A test over the table can assert what
  no test can assert today — every route has a title, an anchor in the
  navigation, a help chapter and a reachable parent; every `nav` value names a
  real `TOPNAV` entry; every `role` is one `mayUse` knows. Those are table
  assertions in a plain unit test, not Playwright runs.
- **Negative / trade-offs accepted:** a migration inside an 8'479-line file,
  touching the router, the chrome and every view handler's entry point. Pattern
  order becomes load-bearing and has to be stated as such — first match wins,
  which is what the current prefix tests already rely on silently
  (`#/operations/decisions` before `#/operations`). The table is a fifth shape
  until the four it replaces are gone, so the migration is worth doing in one
  pass rather than route by route.
- **Follow-ups / risks to watch:** the `#/console/connectors` → `#/console/workers`
  redirect (the pre-ADR-0203 spelling) is a rewrite, not a view, and needs a
  place in the table rather than a special case before it. The 67
  `href="#/…"` literals across the views keep pointing at routes by hand; the
  table does not make them safe, and a later test that checks every literal
  against it would. Whether `parent` should be a route or a stack (a form inside
  a project inside the Modeler) is open — a single level covers every current
  view, and the field can grow.

## Pros and cons of the options

### Option 1 — Repair the five lists in place
- Good: small, immediate, no migration; each of the known gaps closes today.
- Bad: leaves five lists to keep in step, so the next surface re-opens the same
  gaps. Nothing fails when an entry is forgotten, which is the actual defect.

### Option 2 — One declarative route table (chosen)
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
- paired with ADR-0243, which takes the same question one
  level down: what the views inside the shell are built from
