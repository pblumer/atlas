# Atlas UI ADR impact audit

**Repository:** `pblumer/atlas`
**Branch:** `claude/atlas-uix-consistency-adr-9n6e5t`
**Inspected commit:** `8659a5a07f721c5f9a183ea8f07fa3cde6182915` (`main` at `e522114`)
**Audit date:** 2026-09-04
**Scope:** `api/web/` (44 ES modules, `app.css`, `index.html`) and `e2e/` (76 specs),
audited against the two records proposed in
[`draft-one-route-table-for-the-shell.md`](../adr/draft-one-route-table-for-the-shell.md)
and [`draft-shared-ui-primitives.md`](../adr/draft-shared-ui-primitives.md).
Engine, compiler, state and API packages are out of scope — neither record touches them.

## 1. Executive summary

Both records describe real, measurable drift. Neither describes a broken product:
every surface works, and the defects are of the kind a reader notices as
inconsistency rather than as failure.

| Dimension | Measured | Assessment |
|---|---:|---|
| Routes served by the hash router | 41 | One dispatcher, four further lists describing the same routes |
| Lists that must agree per route | 5 | `route()`, `TOPNAV`+`setChrome`, `routeTitle`, help mapping, `fullBleed` |
| Routes with no active navigation entry | 15 | Every detail/deep-link route |
| Routes with no page title | 8 | Tab reads a bare `Atlas` |
| Canvas views missing full-bleed layout | 3 | DMN viewer, class canvas, Panorama model |
| Distinct "way back" markup shapes | 4 | Plus views with none |
| Dialogs built by hand | 22 in 10 files | 8 CSS class families for one object |
| Button-size call sites to unify | 21 (`sm`) vs 62 (`small`) | Two rules with different metrics |
| Editor surfaces with a bar | 5 | 3 class names, 1 editor with no bar |
| Diagram surfaces without zoom | 1 of 6 | The hand-drawn one; the other five inherit it from diagram-js |
| Diagram surfaces with visible zoom controls | 1 of 6 | Elsewhere zoom is ctrl+wheel, which is not discoverable |
| Shared primitives that already work | 3 | `toast`, `enhanceTable`, `openPickModal` |

The two records differ sharply in shape, and that matters more than their
combined size:

- **The route table is one concentrated change with broad blast radius.** The code
  that changes is roughly 190 lines of router plus a ~50-line declaration — about
  3% of `app.js`. What it touches is everything: 36 view handlers, 22 e2e specs
  that navigate by hash, and every page of the product.
- **The shared primitives are a broad change with narrow blast radius.** 22
  dialogs and 21 button call sites across 12 files, each edit local, each
  verifiable on its own, none of them able to break a page the edit did not touch.

That asymmetry is the main input to sequencing (§6).

## 2. Method and confidence

Every figure above was produced by counting occurrences in the current working
tree, not by estimation, and each is reproducible from the locations cited in §3
and §4. Two limits apply.

**A measurement trap that changed the numbers.** `api/web/editor.js` contains a
literal NUL byte at line 6534 — a composite key whose separator was written into
the source as a raw byte rather than as an escape sequence. `grep` therefore
classifies the file as binary and **silently skips it** unless given `-a`. The
first pass of this audit undercounted `.btn.sm` (15 instead of 21) and missed
dialogs in the largest UI file (11'073 lines) for exactly this reason; the two
draft records were corrected against the `-a` figures before this audit was
written. Any grep-based lint, review search or CI check over `api/web` has the
same blind spot today. See §7.

**History depth.** This is a shallow clone (12 grafted tips, 321 commits from
2026-08-31 to 2026-09-04). Change-frequency figures below are a lower bound over
the visible window, not a project-lifetime rate.

## 3. Against `draft-one-route-table-for-the-shell`

### 3.1 The five lists, measured

| List | Location | Entries | Shape |
|---|---|---:|---|
| Dispatcher | `app.js:8333` `route()`, 124 lines | 41 | `===` and regex, first match wins |
| Secondary nav | `app.js:526` `TOPNAV` + `setChrome` (`app.js:969`, 19 lines) | 24 | exact string equality |
| Page title | `app.js:8296` `routeTitle`, 36 lines | 28 | anchored regex |
| Help chapter | `app.js:853–863` | 12 | prefix test, order-significant |
| Full-bleed chrome | inside `setChrome` | 5 | `String.includes` |

Adding a surface therefore means five edits in four shapes, with no failure mode
when one is missed.

### 3.2 What is already missed

Verified by evaluating the live rules against every route the dispatcher serves:

- **15 routes mark no navigation entry.** `#/modeler/new`, `#/modeler/d/{key}`,
  `#/modeler/draft/{id}`, `#/modeler/p/{id}`, `#/modeler/form/new`,
  `#/modeler/form/e/{id}`, `#/modeler/dmn/{id}`, `#/tasks/t/{key}`,
  `#/operations/p/{key}`, `#/operations/p/{key}/i/{key}`, `#/operations/c/{key}`,
  `#/operations/i/{key}`, `#/operations/decisions/{id}`, `#/panorama/models/{id}`,
  `#/data/m/{id}`.
- **8 routes have no page title.** `#/console/ai-access`, `#/console/audit`,
  `#/operations/incidents`, `#/operations/workers`, `#/operations/outbox`,
  `#/operations/ad-mock`, `#/operations/sql-mock`, `#/operations/call-activities`.
  Six of the eight share one cause: `/^#\/operations$/` is anchored, so no page
  under Operations inherits from it.
- **3 canvas views render in the centred column:** `#/modeler/dmn/`, `#/data/m/`,
  `#/panorama/models/`.
- **4 markup shapes for the way back**, plus views with none: `.crumbs` with
  separator and current element (3 sites), `.crumb` (`app.js:2875`), a bare
  `← Decisions` link (`app.js:6415`), `← Project` / `← Modeler` (`app.js:8036`).

That is 30 concrete symptoms, all of one cause.

### 3.3 Migration surface

| What moves | Measure |
|---|---:|
| Router code rewritten | ~190 lines (`route`, `setChrome`, `routeTitle`, help) |
| Declarations merged into the table | ~50 lines (`APPS`, `TOPNAV`) |
| View handlers whose call site changes | 36 in `app.js`, 1 in `aiaccess.js` |
| `href="#/…"` literals left pointing at routes by hand | 67 (32 with interpolation) |
| `location.hash` writes / redirects | 16 |
| e2e specs navigating by hash | 22 of 76 |
| Specs pinning nav or bar structure specifically | 5 |

`app.js` was touched in 49 of the 321 commits in the visible history and
`editor.js` in 49 — the highest-churn files in the tree. A migration held open
across many days will collide; one landed in a single pass will not.

## 4. Against `draft-shared-ui-primitives`

### 4.1 Dialogs

22 dialogs (`role="dialog"`) across 10 files: `app.js` 5, `incidents.js` 4,
`editor.js` 3, `migrationdialog.js` 3, `workerdialog.js` 2, and one each in
`dev-view.js`, `infomodel-import.js`, `json-editor.js`, `panorama-viewer.js`,
`pickmodal.js`. `app.css` carries 29 selector lines across 8 class families for
the same object: `.modal` / `.modal-ov`, `.confirm-modal`, `.conn-modal`,
`.dev-modal` / `.dev-overlay`, `.dmn-modal` / `.dmn-overlay`, `.inc-vars-modal`,
`.json-modal` / `.json-modal-overlay`, `.mig-modal`. No file uses the platform
`<dialog>` element.

The behaviour that is re-implemented each time — overlay, `role="dialog"`,
`aria-modal`, Escape, initial focus — is mostly present in most of them, which is
why the omissions are easy to miss: `infomodel-import.js` opens its import report
with neither a `focus()` call nor `autofocus`, so focus stays behind the dialog.

### 4.2 Button sizes

`.btn.sm` (`app.css:240`, `padding: 4px 10px; border-radius: 5px`) and
`.btn.small` (`app.css:1501`, `padding: 2px 8px`) are two rules with different
metrics, not two spellings. Call sites: `small` 62 across eight files, `sm` 21
across four (`app.js` 8, `editor.js` 6, `incidents.js` 6, `secret-shapes.js` 1).
Unifying on `small` is 21 mechanical edits and the deletion of one CSS rule.

### 4.3 Diagram zoom

Six surfaces present a diagram. Five are diagram-js underneath — the BPMN modeler
and its live and replay views (`editor.js`), the DMN editor (`dmn-editor.js`), the
class canvas (`infomodel-editor.js`), the Panorama viewer and mesh
(`panorama-viewer.js`, `panorama-mesh.js`) — and zoom because diagram-js zooms.
The sixth, `renderDrgSvg` (`app.js:8092`), is drawn by hand as plain SVG and states
its own position in a comment: "Read-only: no interaction, just a faithful
picture." Its frame offered `overflow:auto` and nothing else, so the only way to
get closer to a decision requirements graph was the browser's page zoom, which
scales the console around it.

Visible zoom controls are rarer still: only the Panorama mesh has them
(`mesh-zoom-in` / `-out` / `-fit`). Everywhere else zoom is ctrl+wheel, which
works but is not discoverable.

### 4.4 Editor bars

Five editing surfaces, three bar class names, one surface without a bar:
`editor-bar` (`editor.js`, `form-editor.js`, `panorama-viewer.js`), `im-bar`
(`infomodel-editor.js`), `pg-bar-in` / `pg-bar-out` (`playground.js`), and no bar
in `dmn-editor.js`. ADR-0229's rank rules — the irreversible act filled, state and
command in different shapes, overflow behind one menu — hold in `editor.js` only.

This is the least mechanical of the three: a builder general enough for five
surfaces is a design question, not a rewrite, and `editor-bar.spec.mjs` pins the
Modeler bar's structure while it is answered.

### 4.5 What already works

`toast` (`app.js:75`, used in 15 files), `enhanceTable` (`table.js:26`, applied to
every view's tables on every navigation) and `openPickModal` (`pickmodal.js:27`)
are the shape ADR-0012 asked for and are not in question. The record generalises
an existing, working pattern rather than introducing one.

## 5. Impact assessment

**Order of magnitude, not an estimate in days.** The audit measures surface, not
effort; the figures below are what a planner needs to form their own estimate.

| | Route table | Shared primitives |
|---|---|---|
| Files touched | 2 (`app.js`, `aiaccess.js`) | ~12 |
| Edits | 1 structural rewrite + 37 call sites | 22 dialogs, 21 buttons, 5 bars |
| Divisible into independent steps | Poorly — the table replaces four lists at once | Well — three independent tracks |
| Regression risk if wrong | High: every page, all 22 navigating specs | Low per edit, contained to the widget |
| Detectable by test after the change | Yes, and newly so: table assertions in a unit test | Partly: two of three rules are grep-checkable |
| Defects closed on landing | 30 named symptoms | 1 named (focus), plus the visual inconsistency |
| Defects prevented afterwards | The whole class — a missing field fails a test | The whole class for buttons and dialogs |

**The strongest argument for the route table is not the 30 symptoms.** It is that
none of them is currently detectable without a browser: today the only way to
learn that a route has no title is to visit it. A table turns five hand-checked
properties into fields a unit test can require of every row. That is a change in
what the project can know about its own UI, and it does not decay.

**The strongest argument against doing it now is churn.** `app.js` is the
highest-churn file in the tree. A structural rewrite of its router is a
merge-conflict magnet for anything else in flight, and it is the one piece of
work here that cannot be split into small, independently reviewable steps.

**Two risks the records name but do not resolve.** The bar builder may not fit
five surfaces without becoming a configuration language — that finding, if it
comes, is worth recording rather than forcing. And `parent` as a single route
covers every current view but not obviously the next one (a form inside a project
inside the Modeler).

## 6. Sequencing recommendation

The two records are independent: neither blocks the other, and they touch
disjoint code. If both are accepted, the low-risk order is:

1. **Button scale** — 21 edits, one deleted CSS rule, a grep test. Hours, not
   days, and it establishes the "primitive plus test" pattern cheaply.
2. **Dialog opener** — 22 sites, each independently reviewable, closing the focus
   defect on the way through.
3. **Route table** — one pass, landed quickly to avoid churn, with the table
   assertions written first (they are what makes the change worth its risk).
4. **Editor-bar builder** — last, because it is the one whose design question is
   genuinely open, and because the three before it will have shown whether the
   primitive-plus-test pattern holds in this codebase.

Steps 1 and 2 are worth doing even if the route table is rejected. The reverse is
also true; nothing here is all-or-nothing.

## 7. Incidental finding: the NUL byte in `editor.js` (fixed in this branch)

`api/web/editor.js:6534` embeds a raw NUL byte in the source as a key separator
instead of writing an escape sequence for it. Consequences: `grep` treats the file
as binary and skips it without `-a`, so the largest UI file is invisible to every
text-based search, review grep and any grep-driven CI check; and the byte survives
into the `go:embed`'d asset.

It was also not only a tooling nuisance, which is what the first pass of this audit
assumed. The same key is written into a `data-key` attribute and read back from it,
and the HTML parser replaces U+0000 in an attribute value with U+FFFD — measured in
Chromium: an attribute written as `A<NUL>B` reads back as `41,fffd,42`, while
`U+001F` survives as `41,1f,42`. The key read back therefore never equalled the key
written, the lookup in `editor.js` fell through to "first decision with this id",
and a business rule task picking between two models that share a decision id
adopted the wrong model's inputs and result variable — the one case the composite
key exists for.

Fixed on this branch: the separator is now `U+001F`, written as an escape sequence.
`e2e/decision-picker.spec.mjs` covers the behaviour (it fails against the old
separator), and `TestOurEmbeddedSourcesAreTextToGrep` in
`api/webassets_internal_test.go` keeps a raw NUL out of the embedded sources.

## 8. What this audit does not cover

- **Visual and interaction quality.** Nothing here judges spacing, colour or
  wording; it counts structural divergence only.
- **Accessibility beyond what was measured.** The focus finding in
  `infomodel-import.js` came out of the dialog census. No systematic keyboard,
  screen-reader or contrast audit was run, and the absence of further findings is
  not evidence of their absence.
- **Effort in days.** No velocity data was available in a shallow clone.
- **Whether the drafts should be accepted.** That is the review's decision; this
  document supplies the measured basis for it.
