# ADR-DRAFT: The decision editor is a page, not an overlay

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-13
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0062](0062-embedded-dmn-editor.md) embedded `dmn-js` to close one specific
dead end. To use a decision in a business rule task an author had to leave Atlas,
model it elsewhere, export a `.dmn`, upload it, and only then pick it — and the
picker's dropdown was **empty** until that whole round trip completed. The editor
was therefore built where the dead end was: as a **modal overlay launched from the
picker**, resolving with the stored model so the task could adopt its inputs and
output.

That was the right shape for what a decision was at the time. Under ADR-0062 a
decision is a *reference* — a display name and a handle pointing at a model file
that some process happens to use. An overlay is a reasonable home for something
you step into from a form field and step back out of.

[ADR-0319](0319-durable-versioned-decision-deployments.md) changed what a decision
*is*. It is now a durable, versioned runtime artifact: published in its own right,
carrying a definition key and a version, named in the application release manifest,
restored after a restart, and publishable from an application with **no BPMN in it
at all**. A BPMN diagram and a form are each edited on a routed page of their own.
A decision — now the third deployable artifact — is edited in a modal.

The gap is not stylistic. Four things follow from the overlay that do not follow
from a page:

1. **A decision has no address.** "Look at the eligibility table" is a URL for a
   process and for a form, and a click path for a decision. It cannot be
   bookmarked, linked from an incident, or sent to a colleague.
2. **Browser back closes the editor and drops the edit.** The BPMN editor survives
   navigation; the overlay is dismissed by the same gesture that navigates.
3. **Saving is publishing.** The overlay's save writes the model file every
   reference resolves to. For BPMN the draft/deploy split exists precisely so that
   editing is not shipping; the decision editor has no such split, and the overlay
   is what made that invisible.
4. **Publishing is somewhere else.** An author who has just modelled a decision
   must close the overlay, find the application, and press Publish.

The question: **what is the authoring surface for an artifact that is now deployed
the way a process is deployed?**

## Decision drivers

- **One Modeler, one grammar.** A BPMN diagram, a form and a decision should be
  reached, edited, saved and left the same way. An author should not have to learn
  that one of the three behaves differently because of where it came from.
- **Do not regress ADR-0062.** Its achievement was that authoring a decision from a
  business rule task fills in the decision id, the input mappings and the result
  variable with no separate step. Whatever replaces the overlay has to keep that.
- **Reuse the chrome that exists.** `api/web/form-editor.js` is the closest sibling
  — a routed editor over a vendored third-party editing surface — and it already
  answers breadcrumb, tabs, save, status, teardown and URL-after-first-save. Copy
  its answers rather than invent new ones.
- **Stay off the engine.** This is a design-time UI concern. It must not touch the
  processor, the log, recovery, or the ADR-0319 deployment path.
- **Phased.** The parity gap is wide (drafts, deploy-from-editor, export,
  documentation, collaboration). The move off the overlay is the part everything
  else depends on, and it has to ship alone.

## Considered options

1. **Keep the overlay, give it a deep link.** Add a route that opens the modal over
   whatever page happens to be underneath.
2. **The decision editor is a routed page (chosen).** Mount `dmn-js` into the view,
   in the chrome `form-editor.js` established, at routes of its own.
3. **A routed page built on a component shared with `editor.js`.** Extract the
   editor bar, crumbs, tab strip and dirty-state handling from the BPMN editor
   first, then build the decision editor on it.

## Decision outcome

Chosen option: **"The decision editor is a routed page" (option 2).** This reverses
ADR-0062's overlay choice and nothing else about it: `dmn-js` remains the editor,
temis remains the engine, and Atlas still builds no DMN renderer of its own.

### The page

`api/web/dmn-editor.js` stops exporting `openDmnEditor` (which built a
`.dmn-overlay` into `document.body`) and exports `mountDmnEditor(root, …)` plus a
`cleanup()`, the shape `mountFormEditor` uses — including claiming
`window.__atlasCleanup` so navigating away tears the modeler down, and the
generation guard that keeps a superseded mount from instantiating `dmn-js` into a
detached container.

The chrome is the form editor's, field for field: `.editor > .editor-bar` carrying
a `.crumbs` back link that resolves to the owning application, the `.etabs` tab
strip (here the `dmn-js` view switcher: the DRG overview and each decision's own
table), the model handle as a `.chip`, a status line, and **Save**. The labels are
English, like the rest of the Modeler — the overlay was German-only.

Save behaves as the form editor's does: it stays on the page, reports "Saved", and
`history.replaceState`s the URL onto the edit route, so a second Save updates the
decision rather than creating a second one.

### The routes

```
#/modeler/dmn/new                                  a new decision
#/modeler/dmn/new/p/{applicationId}                …filed into an application
#/modeler/dmn/new/for/{processId}/{elementId}      …for a business rule task
#/modeler/dmn/e/{refId}                            edit an existing decision
#/modeler/dmn/e/{refId}/for/{processId}/{elementId}
#/modeler/dmn/{refId}                              the read-only DRG viewer (unchanged)
```

The `for/…` shape is not invented here: it is what
[ADR-0260](0260-ai-form-generation.md) established for "Create a new
form" pressed on a step (`#/modeler/form/new/for/{processId}/{elementId}`). A
decision authored from a business rule task is the same gesture, so it gets the
same route shape.

The viewer route keeps its address and must be matched **after** the editor routes,
since `new` and `e/…` would otherwise be read as reference ids.

### Leaving the diagram, and coming back

Pressing "＋ New decision" on a business rule task now navigates. Two things make
that safe, and both already exist in `editor.js` for the call-activity drill-down:

- **`keepEdits`** — a session addressing a draft saves it (`saveDraft`) before
  leaving; one that does not asks before discarding. Navigating away never silently
  costs work.
- **adoption on return** — the editor stashes what it authored, and the BPMN
  editor's business-rule-task panel consumes it on the next render of that element
  and runs the existing `adoptAuthored`. The decision id, the input mappings and
  the result variable are filled in exactly as the overlay filled them.

So the ADR-0062 flow survives the move: the author presses one button, models the
decision, presses back, and the task is wired. What changed is that the decision
was modelled on a page that has an address, in the full window, with the diagram
safely a draft rather than held in a tab behind a dimmed backdrop.

### What this record does not decide

Deliberately left to later slices, because each is a decision of its own and none
of them blocks the move off the overlay: a **draft** state for a decision (so that
saving is not publishing), **Deploy** from inside the editor, **Export XML**,
**documentation** ([ADR-0143](0143-process-documentation-export.md)), DRD **auto-layout**,
a **collaborative session** ([ADR-0140](0140-live-collaborative-modeling-sessions.md)),
and a **test panel** that evaluates a decision against sample inputs.

### Consequences

- **Positive:** a decision is addressable, linkable and reloadable; the browser's
  own back and forward work; the editor gets the whole window rather than a modal
  inside it; the three artifact kinds are reached and left the same way; the chrome
  is the form editor's, so there is one answer to breadcrumb/tabs/save/teardown
  rather than two; the overlay's German-only labels are gone.
- **Negative / trade-offs accepted:** authoring a decision from a business rule
  task now leaves the diagram, which costs a navigation and — for a diagram never
  saved — a draft the author did not explicitly ask for. The alternative was to
  keep the modal on that one path, which would have left two ways to edit a
  decision, and that is the thing this record exists to end. `dmn-editor.js` and
  `editor.js` still duplicate the bar's markup; see option 3.
- **Follow-ups / risks to watch:** the phases listed above; and whether the shared
  editor component of option 3 is worth extracting once both editors are routed —
  a question worth answering with two routed editors in hand rather than guessed at
  now.

## Pros and cons of the options

### Option 1 — keep the overlay, give it a deep link
- Good: smallest change; the picker flow is untouched.
- Bad: answers only the addressability complaint. Back still dismisses, the editor
  is still a window inside a window, saving is still publishing, and the Modeler
  still has two interaction models for three artifacts. A route that opens a modal
  over an arbitrary page is also a route whose back behaviour nobody can predict.

### Option 2 — a routed page (chosen)
- Good: parity where it is felt — the address bar, the back button, the chrome, the
  words. Reuses an existing, proven mount shape. Leaves the engine untouched.
- Bad: authoring from a business rule task becomes a navigation; a little markup is
  duplicated between the two editors.

### Option 3 — extract a shared editor component first
- Good: one bar, one crumb trail, one dirty-state rule, forever.
- Bad: it puts a refactor of a 13 000-line module that works in front of a change
  authors are asking for today, and it would touch the BPMN editor — the highest-
  traffic surface in the product — for a benefit nobody can see. Parity in
  *operation* comes from the same grammar, not from the same source file; whether
  the shared file is then worth it is a better question with both editors routed.

## Links

- reverses the overlay choice of [ADR-0062](0062-embedded-dmn-editor.md); its
  editor (`dmn-js`), its storage and evaluation boundary (temis), and its
  inputs/output adoption all stand
- follows from [ADR-0319](0319-durable-versioned-decision-deployments.md) — a
  decision became a deployable, versioned artifact
- copies the route shape of [ADR-0260](0260-ai-form-generation.md) and
  the mount shape of the form editor (ADR-0028)
- relates to [ADR-0128](0128-process-applications.md) — the application is where a
  decision is published from
