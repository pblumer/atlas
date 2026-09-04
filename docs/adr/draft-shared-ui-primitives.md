# ADR-DRAFT: The views are built from shared parts

- **Status:** Proposed
- **Date:** 2026-09-04
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0012 chose a buildless vanilla-JS shell and accepted the trade-off in
writing: "manual DOM code; no components". It also named the mitigation —
"keep DOM-building helpers small and shared so views stay readable".

Three such helpers exist and work: `toast` (`api/web/app.js:75`) for transient
feedback, `enhanceTable` (`table.js:26`) for sort-and-filter on every data table,
and `openPickModal` (`pickmodal.js:27`) for the pick-a-thing-and-name-it dialog.
The first two are used across a dozen files each and are exactly the shape the
ADR asked for.

Outside those three, the same widget is written again in each file that needs it.
The result is not that any one view is wrong — each is reasonable on its own —
but that the same object looks and behaves slightly differently depending on
which file drew it.

**Dialogs: eight class families for one thing.** `app.css` carries `.modal` /
`.modal-ov` (the generic one), plus `.confirm-modal`, `.conn-modal`, `.dev-modal`
/ `.dev-overlay`, `.dmn-modal` / `.dmn-overlay`, `.inc-vars-modal`, `.json-modal`
/ `.json-modal-overlay` and `.mig-modal`. Twenty-two dialogs across ten files
build their markup by hand (`app.js` 5, `incidents.js` 4, `editor.js` 3,
`migrationdialog.js` 3, `workerdialog.js` 2, and one each in `dev-view.js`,
`infomodel-import.js`, `json-editor.js`, `panorama-viewer.js` and
`pickmodal.js`), and each re-implements the same behaviour: append an overlay, `role="dialog"` and
`aria-modal`, close on Escape, put the focus somewhere sensible. Where an
implementation leaves a piece out, nothing notices — `infomodel-import.js` opens
its import report with neither a `focus()` call nor an `autofocus` attribute, so
that dialog opens with the focus still behind it. No file uses the platform's
`<dialog>` element.

**Two button sizes with different names and different metrics.** `.btn.sm`
(`app.css:240`, `padding: 4px 10px; border-radius: 5px`) and `.btn.small`
(`app.css:1501`, `padding: 2px 8px`) are two rules, not two names for one rule:
a `small` button and an `sm` button next to each other are visibly different
heights. `small` is used 62 times across eight files, `sm` 21 across four
(`app.js` 8, `editor.js` 6, `incidents.js` 6, `secret-shapes.js` 1).

**The editor bar is a Modeler idea, not a shared one.** ADR-0229 worked out what
the top of an editor is for and gave a reasoned answer — two acts and a menu,
the irreversible act filled, state and command in different shapes. That
reasoning was not specific to BPMN, but its expression is: `editor-bar` appears
in `editor.js`, `form-editor.js` and `panorama-viewer.js`; `infomodel-editor.js`
brings `im-bar`; `playground.js` has `pg-bar-in` / `pg-bar-out`; `dmn-editor.js`
has no bar at all. The rank rules from ADR-0229 hold in exactly one of them.

**A diagram is zoomable wherever a framework supplies it, and nowhere else.**
The BPMN modeler and its live and replay views, the DMN editor, the class canvas
and the Panorama viewer are all diagram-js underneath, so they zoom because
diagram-js zooms. `renderDrgSvg` (`app.js:8092`) draws the decision requirements
graph as plain SVG and says so in its own comment — "Read-only: no interaction,
just a faithful picture" — inside a frame whose only concession to size is
`overflow:auto`. Whether a reader can get closer to a diagram therefore depends
on which library happened to draw it, which is not a property of the diagram.

The question this record answers is not "which shade of grey". It is whether
"small and shared" from ADR-0012 is a hope or a rule, and what it applies to.

## Decision drivers

- The reasoning in ADR-0229 is about how a reader tells acts apart. That reader
  does not know which file drew the bar, so the rule cannot be per file.
- ADR-0012's buildless constraint stands: no framework, no component library, no
  build step. Shared parts here mean exported functions and CSS classes.
- A convention nobody can check is a preference. Whatever is decided has to be
  visible in review, ideally failing a test.
- Existing behaviour keeps working. Every dialog, bar and button is wired by id
  or class across several thousand lines and a dozen e2e specs;
  `editor-bar.spec.mjs` pins the Modeler bar's structure specifically.
- `editor.js` mounts standalone in the e2e harnesses with no `app.js` in the
  page (ADR-0229). A shared part it depends on must be importable on its own,
  not reachable only through the shell.

## Considered options

1. **Write the conventions down** in a `docs/design/` guide — one dialog shape,
   one button scale, the ADR-0229 bar rules generalised — and hold new code to
   it in review. No code moves.
2. **Extract the parts, then require them.** Add the missing primitives next to
   `toast` / `enhanceTable` / `openPickModal` — a dialog opener, one button
   scale, an editor-bar builder that encodes ADR-0229's ranks — migrate the
   existing call sites, and let a test guard the ones that can be tested (no
   `.btn.sm`, no hand-rolled overlay class outside the primitive).
3. **Adopt a component library** and rebuild the views on it, revisiting the
   buildless half of ADR-0012.

## Decision outcome

Chosen option: **"Extract the parts, then require them"** (option 2).

Three primitives, each an ES module export usable without `app.js`:

- **A dialog opener.** Takes a title, a body node, and actions; owns the overlay,
  `role="dialog"` / `aria-modal`, Escape, the initial focus and the teardown.
  The eight bespoke class families collapse onto `.modal` / `.modal-ov`;
  per-dialog styling stays a modifier class on the same structure, so
  `.json-modal`'s body layout survives without `.json-modal-overlay` existing.

  Two of the twenty-two are not dialogs and do not move. The process search
  (`.sp`) is a **command palette**: no title, no actions, an input that filters as
  it is typed and arrow keys that walk the results. The Developer view
  (`.dev-modal`, ADR-0145) is a **tool window**: its header is a drag handle
  carrying a language badge, a dirty marker and its actions, it can be moved and
  resized, and it holds a split panel with tabs. Both would have to be argued into
  the shape of a dialog and would be worse for it. That there are three patterns
  here rather than one is the finding; what the rule refuses is a *fourth* dialog
  written by hand.
- **One button scale.** `.btn.small` wins on use (54 to 15) and `.btn.sm` is
  removed, its 21 call sites rewritten. One rule, one name.
- **The editor bar's ranks as a checked rule, not a builder.** This is the one
  place the record's first answer did not survive contact with the other four bars.
  A builder was proposed on the assumption that they had the same problem the
  Modeler's had. Measured, they do not: the Modeler's bar had seven equal buttons
  mixing four kinds of act, while the form editor has two, the class canvas has
  one, and the Panorama viewer has two with its save in the canvas toolbox. There
  is no rank there to get wrong, and a builder would be placing a single button for
  three of the five.

  Their left-hand halves are not the same object either — breadcrumbs, a title with
  a revision, a row of view tabs — and `.im-bar` differs from `.editor-bar` because
  the class canvas sits in a card and the others are full-bleed. A builder general
  enough for all of that is the configuration language this record warned about.

  What is worth keeping is the reasoning rather than the markup, so it is held as a
  test over the rendered bars: at most one filled act, every control that holds a
  state saying so in the vocabulary for what it is, tabs inside a tablist, and no
  bar growing back past a handful of direct acts without an overflow menu. That
  found something a builder would have papered over: the Modeler's and the form
  editor's view tabs said "active" in a class and nothing else, so which view you
  were in reached sighted readers alone. The Panorama viewer's view tabs were the
  only ones already doing it properly.

- **A diagram zoom**: zoom in, zoom out, fit, ctrl+wheel, and the current factor
  stated rather than inferred. It attaches to already-rendered markup, the way
  `groupifyPanel` does, so a renderer stays a renderer. It works two ways from one
  control — over a canvas that owns its zoom it only asks (diagram-js answers), and
  over a picture Atlas drew as SVG it resizes. That distinction matters because the
  ability was never the missing part on the framework-backed canvases: diagram-js has
  zoomed on ctrl+wheel all along, and nothing on screen said so. A control a reader
  cannot see is a control they do not have.

  A surface whose zoom buttons already share a box with other tools — both Panorama
  surfaces do, the viewer's with undo, redo and save, the mesh's with "release" —
  mounts the control into that box rather than floating a second one over the same
  canvas. That is what "one control" has to mean to be worth stating: not one
  position, which no two of these surfaces could agree on, but one set of buttons,
  one behaviour and one stated factor wherever they sit.

The rule that follows: **a view does not draw a dialog, a bar, or a button size
of its own, and does not present a diagram that cannot be zoomed.** If a view needs something the primitives do not offer, the
primitive grows — that change is reviewed once for everyone, which is the point.

### Consequences

- **Positive:** the ADR-0229 reasoning applies wherever there is a bar rather
  than only in `editor.js`. A dialog behaves the same everywhere, including the
  parts that are easy to forget. A diagram can be approached whoever drew it.
  Two of the rules are grep-checkable and can be a test: no `btn sm` in
  `api/web`, no overlay class outside the primitive's.
- **Negative / trade-offs accepted:** a migration across ten files for dialogs,
  all of it behaviour-preserving but none of it free, and a window in which both
  the primitive and the hand-written version exist. The bar builder was the
  trade-off that did not pay: it had to be general enough for five editors without
  becoming a configuration language, and it could not be — recorded above as a
  finding rather than forced.
- **Follow-ups / risks to watch:** the diagram rule is the one with no mechanical
  check — "this SVG is a diagram" is not something grep can decide, so it holds by
  review, and a hand-drawn diagram added without zoom would pass CI. `editor.js`
  is large and mounts standalone,
  so its imports need checking against every harness that loads it. Whether the
  DMN editor should have a bar at all is a product question this record does not
  settle — it only says that if it gets one, the one it gets is the shared one.
  If the primitives grow past what hand-written DOM helpers do well, that is the
  evidence ADR-0012 asked us to watch for, and it belongs in a new record about
  the build, not in more helpers.

## Pros and cons of the options

### Option 1 — Write the conventions down
- Good: costs nothing, breaks nothing, useful the day it is written.
- Bad: the current state was already reachable under ADR-0012's "keep helpers
  small and shared". A second sentence saying it harder does not check anything.

### Option 2 — Extract the parts, then require them (chosen)
- Good: the convention becomes the path of least resistance instead of a rule to
  remember; the omissions (a dialog without focus, a second button scale) stop
  being possible rather than being caught.
- Bad: a real migration, and a primitive that has to fit five editors.

### Option 3 — Adopt a component library
- Good: the problem is what component libraries are for.
- Bad: reopens ADR-0012's central decision for a UI whose shared surface is
  three widgets. If the buildless choice is to be revisited, it should be
  revisited on its own evidence, not as a side effect of tidying dialogs.

## Links

- follows ADR-0012, which accepted "no components" and asked that helpers stay
  small and shared
- generalises ADR-0229, which decided what an editor bar is for, in the Modeler
- paired with ADR-draft-every-route-says-where-it-is, which takes the same
  question one level up: what the shell around these views decides
