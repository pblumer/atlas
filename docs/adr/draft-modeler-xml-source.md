# ADR-DRAFT: The Modeler shows a model's source and applies a correction to it

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-14
- **Deciders:** Atlas maintainers

## Context and problem statement

A process diagram and a decision are XML documents underneath (BPMN 2.0, DMN 1.3 or
1.5); a form is a form-js schema, which is JSON. The Modeler edits all three through a
canvas and a properties panel, and that covers almost everything an author does. It
does not cover the corrections that are easier to make in the document than on a
canvas: an id that has to change in every place that names it, an attribute the panel
does not offer, a reference that a hand-written, generated or imported model got wrong,
a namespace prefix an older tool wrote differently.

The BPMN and DMN editors offered **Export XML**, which hands the document over as a
file. Correcting it from there meant editing the file elsewhere and bringing it back in
by an upload or an API call — a round trip that leaves the editor, its draft and its
live session behind. The form editor already had its schema as an editable tab
([ADR-0028](0028-forms-and-the-tasks-app.md)), labelled "Editor", which did not say that it was the
source.

The question is how an author sees a model's source in the Modeler and corrects it there.

## Decision drivers

- A correction made in the source must not cost the model it was meant to correct: a
  malformed or unreadable edit has to leave the canvas as it was.
- An applied correction is an edit like any other. Everything that protects unsaved work
  — leaving the editor, a collaborator's draft arriving over a live session
  ([ADR-0140](0140-live-collaborative-modeling-sessions.md), [ADR-0323](0323-co-editing-a-decision.md))
  — has to see it.
- The same act in the three editors, reached the same way and called the same thing.
- No new dependency: the Console is buildless ([ADR-0012](0012-web-ui-app-shell.md)), and
  every code field in the Modeler already uses one editing surface (`code-editor.js`).

## Considered options

1. **A source dialog over the editor**, opened from the bar's `⋯` menu beside Export XML,
   holding the document read off the canvas; **Apply** imports it back.
2. **A Source tab beside Design and Implement**, switching the editor body between the
   canvas and the text, as Camunda Modeler's XML tab does.
3. **Applying the edit as a command** on the modeler's command stack, so it could be
   undone like a canvas edit.

## Decision outcome

Chosen option: **"A source dialog over the editor"** (option 1), shared by the BPMN and
DMN editors in one module (`api/web/xml-source.js`). The form editor keeps its tab, which
is already exactly this for JSON, and its label now says **Source**.

The dialog shows the document as the modeler serialises it *now*, saved or not, so the
text is what the author is looking at rather than what a server last stored. It reads
the text with the browser's XML parser while the author types and refuses a malformed
document on Apply, naming and marking the line the parser stopped on — nothing that is
not XML reaches the modeler. A well-formed document is imported; if the modeler cannot
read it, the editor re-imports what was there before and says the model is unchanged.
That matters for one kind of failure in particular: bpmn-js refuses a document that is
not BPMN while parsing it, with the canvas untouched, but one that is BPMN with nothing
to draw (no diagram, no process) only after it has cleared the canvas.
An import that succeeds with warnings (an unresolved reference, an unknown element)
keeps the dialog open on them, since what was left out is exactly what an author
correcting the source needs to see. Closing with an unapplied edit asks first.

Applying is an import, not a command, and that is what the rest of the decision follows
from:

- **The undo history starts afresh.** The dialog says so before Apply is pressed.
- **The editor is told that it holds an edit.** The BPMN editor asked "is there
  anything to lose?" of the undo stack alone, which an import empties; it now also
  reads a flag an import of the editor's own making sets. The live session marked the
  canvas dirty on element events, which an import does not fire; it now has
  `markDirty()`, the counterpart of `markSaved()`, so a collaborator's draft is
  deferred rather than pulled over an applied, unsaved correction.
- **Auto-layout is the same kind of act, and is treated the same.** It replaced the
  canvas with the server's re-flowed document by the same import, and so already had
  the same gap: after an auto-layout a deployment could be left without a question, a
  collaborator's draft could overwrite the new layout in a live session, and a
  document the modeler could not draw left an empty canvas. Both editors now route it
  through the same import as the source view's Apply — rollback, the flag and
  `markDirty()` included.
- **A DMN document of another version is refused in words.** The DMN modeler is built
  for one version when the page opens and cannot change (#994); importing a 1.5
  document into a 1.3 modeler fails with a parse error that never mentions a version.
  The source view checks the namespace first and says which two versions disagree.

### Consequences

- **Positive:** A correction to the source is made where the model is open, and lands
  as an ordinary unsaved edit — Save, Deploy, the draft and the live session treat it
  the way they treat any other. The three editors offer the same act under the same
  name.
- **Negative / trade-offs accepted:** An applied correction cannot be undone with
  Ctrl+Z, and the canvas edits made before it cannot be either. The author can close
  without saving, or apply the earlier text again; the dialog states the cost rather
  than hiding it. A change of DMN version cannot be made here at all.
- **The Playground hears an import too.** Its "the diagram changed" hint
  ([ADR-0215](0215-modeler-playground.md)) listened for element edits only, and an import
  also took the run's markers off the shapes it replaced. It now listens for every
  import — an applied source, an auto-layout, a collaborator's draft, the editor putting
  a refused one back — repaints the run onto the new shapes, and gives the hint when
  the canvas no longer serialises to the document the sandbox started from, so a
  diagram that was put back is not reported as changed.
- **Follow-ups / risks to watch:** A live session's own re-import of a peer's saved
  draft is deliberately *not* marked as a local edit: it is what the server already
  holds. A sandbox opened from a saved scenario is compared with the scenario's
  document, which may be serialised differently from the canvas; there the hint errs
  towards appearing.

### As built

The dialog is `api/web/xml-source.js`; the editors pass it `read` and `apply`, and own
the rollback and the dirty marking — `importOver` and `markImportedEdit` in `editor.js`,
`importLocal` in `dmn-editor.js` — which Auto-layout uses too. Its key handler sits on the window in the capture phase, because the
editor's own F4/F8 and the Console's Ctrl+K and Ctrl+Shift+E are bound on the document
in that phase and would otherwise act on the canvas behind the dialog.

`code-editor.js` redraws its whole highlighted backdrop and line gutter on every
keystroke. For a field that costs nothing; for a document it did not. Measured in
headless Chromium with real key presses, the largest example model (37 KB) took about
90 ms from key to frame and a 200 KB document about 500 ms, almost all of it the DOM and
layout for lines nobody could see. The editor therefore gained an opt-in `virtual` mode,
used only here: the text is still tokenised whole on every edit, but only the lines in
view and thirty either side are drawn, moved onto the text area's lines by a transform.
The same measurement then read about 23 ms and 38 ms. It requires one drawn line per
logical line, so it is refused for a wrapping field and for one with a locked prefix,
and every existing field keeps the path it had.

## Pros and cons of the options

### A source dialog over the editor
- Good: the canvas, its selection and the editor's state stay where they are; the dialog
  is one module both editors call, and the bar keeps its two acts and a menu
  ([ADR-0229](0229-modeler-bar-hierarchy.md)).
- Bad: the diagram and its source are not visible side by side.

### A Source tab beside Design and Implement
- Good: familiar from Camunda Modeler.
- Bad: the tabs are levels of detail over one canvas ([ADR-0229](0229-modeler-bar-hierarchy.md)
  notes the bar is not tab-aware); a tab that replaces the canvas would make every
  canvas-bound feature — the properties panel, the Variables panel, the Playground —
  ask whether it is showing, and the DMN editor's tab strip already lists its views.

### Applying the edit as a command
- Good: one Ctrl+Z would take a correction back.
- Bad: neither bpmn-js nor dmn-js can import into a command — an import replaces the
  diagram the command stack belongs to — so this would mean diffing two documents into
  model operations, which is a merge engine, not a source view.

## Links

- relates to [ADR-0028](0028-forms-and-the-tasks-app.md) (the form editor's tabs)
- relates to [ADR-0229](0229-modeler-bar-hierarchy.md) (the bar's menu)
- relates to [ADR-0140](0140-live-collaborative-modeling-sessions.md) and
  [ADR-0323](0323-co-editing-a-decision.md) (the live session it must not be overwritten by)
- relates to [ADR-0379](0379-dmn-version-follows-the-document.md) (which DMN version a model is written in)
