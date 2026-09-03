# ADR-0237: The class canvas on diagram-js

- **Status:** Proposed
- **Date:** 2026-09-03
- **Deciders:** Patrick Blumer

## Context and problem statement

ADR-0230 gave Atlas an information model and a canvas to draw it on. The canvas was
written from scratch: `api/web/infomodel-editor.js` builds an SVG document by hand
with `createElementNS`, and redraws the whole of it on every edit. That was the right
way to find out whether the notation was worth having. It is not the right way to
keep it.

What it costs shows up as things a modeler expects and does not find. There is no
zoom and no pan, so a model larger than the viewport can only be scrolled. There is
no marquee and no multi-select, so ten boxes are moved one at a time. There is no
undo — a mis-drag is repaired by dragging back, and a deleted class by cancelling the
whole editing session. Keyboard navigation reaches the side panel and stops at the
canvas. None of these are missing because anybody decided against them; they are
missing because each one is a feature a canvas library has and a hand-rolled SVG does
not.

It also looks unlike the rest of Atlas. The BPMN Modeler is bpmn-js, the Panorama
canvas is diagram-js, and the class canvas is neither — so the palette, the selection
outline and the panel chrome are all a near-miss of two surfaces a person uses in the
same session. That is the complaint that prompted this record, and it is a symptom
rather than the problem: the surfaces differ because the substrate differs.

Atlas already runs a second notation on diagram-js. ADR-0189 vendored
`api/web/vendor/archimate/`: diagram-js 15.26.0, plus an Atlas-owned parser, renderer
and rules for the Open Group exchange format, built with esbuild into one buildless
IIFE (ADR-0012). It is editable — modeling, move, resize, outline and rules — and it
is a working demonstration that a notation Atlas owns can sit on the same library the
Modeler does.

A UML class diagram is closer to that canvas than to BPMN. It is boxes with
compartments and four typed relationships, drawn against a subset the server owns and
serves. The parser, which is most of the ArchiMate work, does not exist here at all:
the information model already arrives as JSON from `/api/v1/infomodel/models/{id}`.

## Decision drivers

- **The behaviour is the point, not the skin.** Restyling the hand-rolled canvas
  would make it *look* like the Modeler and still not zoom, undo or multi-select.
  The look is downstream of the substrate.
- **The precedent is in the repository.** A second Atlas-owned diagram-js notation is
  not a new idea here; it is the second instance of one.
- **The subset is already served.** `GET /api/v1/infomodel/subset` hands the canvas
  the stereotypes, the association kinds and the relationship matrix. A rules
  provider reads that table rather than restating it.
- **Buildless stays buildless (ADR-0012).** Whatever ships must be a pre-built
  artifact in `api/web/vendor/`, rebuilt by a documented command, never compiled at
  runtime.

## Considered options

### What the canvas is built on

1. **Keep the hand-rolled SVG and restyle it.** Cheapest, and it buys the appearance
   only. Every capability above stays missing, and the work is discarded the day the
   canvas moves anyway.
2. **Port onto the vendored diagram-js (chosen).** Selection, move, zoom, pan,
   outline, undo/redo and keyboard come from the library. What Atlas writes is
   the part that is Atlas's: how a class is drawn, and what may be connected to what.
3. **Use bpmn-js.** It is already loaded by the Modeler, so nothing new is vendored —
   but its moddle, its element registry and its rules are BPMN's. A class diagram
   modeled as BPMN elements would fight the library at every step.

### One bundle or two

A second `vendor/uml/` bundle carries its own copy of diagram-js — roughly 100 KB
beside the 104 KB the ArchiMate bundle already ships. One bundle exporting both
viewers carries one copy.

**Two bundles now, one later.** The merge is easy once both exist — two `src/index.js`
entries become one that exports two viewers — and it is easy in a way that does not
change either canvas. Doing it first would mean this change touches Panorama's
canvas, which is shipped and in use, for a saving that is real but not urgent. This
record names the merge as the follow-up rather than pretending the duplication is
free.

## Decision outcome

**The class canvas is rebuilt on diagram-js, as an Atlas-owned renderer and rules
provider vendored beside the ArchiMate one.**

### 1. What the canvas keeps owning

The renderer draws a UML class box: a header carrying the «stereotype» and the name,
and a compartment of attributes, each `name: type [multiplicity]` with the business
key marked. An «enumeration» carries literals where the others carry attributes. A
data store is a cylinder, joined to its class by a dashed annotation — not an
association, because a store and its class do not relate; one *is kept in* the other
(ADR-0230 §7).

The four association kinds are drawn as UML draws them, which is the whole reason to
draw them at all: a plain line for an association, a hollow diamond at the whole for
an aggregation, a filled one for a composition, and a hollow triangle at the general
end for a generalization.

### 2. The rules provider reads the served matrix

`allowedBetween(sourceStereotype, targetStereotype)` answers from the table the server
sent, exactly as the ArchiMate canvas does. The canvas offers no connection the write
path would refuse, and it restates no rule the server owns.

### 3. But the edit is local, and that is the difference from ADR-0189

The Panorama canvas never creates anything itself: the server owns the document, so a
new element is written server-side and the view is re-read. The information model does
not work that way and should not. It is a working copy with an explicit **Save** and an
optimistic revision, which is what lets somebody draw three classes and two
relationships and then decide, and what makes an undo stack meaningful.

So this canvas allows create, connect, move and delete locally — because a local edit
here *does* reach the document, when the author saves it. The rules still refuse what
the subset refuses; they refuse it at the point of drawing rather than at the point of
writing.

Resize is refused, and that is not an omission. A class box is as tall as its members
make it, so a dragged corner would either lie about the class or be silently undone by
the next render. What the geometry is free to say is *where* a class sits, which is why
move is local and position is saved.

### 4. The side panel stays HTML, and stays as it is

The properties panel is not part of this change at all. It is a form — it edits names,
types, multiplicities, roles and the business key, and it was made to reorder
attributes by hand in the change immediately before this one — and a form is what it
should be; bpmn-js's own properties panel is HTML for the same reason.

That leaves one half of the complaint that prompted this record unanswered: the
panel's chrome is still Atlas's own rather than the Modeler's collapsible groups. That
is deliberately separate. It is a stylesheet change with no behaviour behind it, and
folding it in here would mix it with a port that replaces the canvas's whole
substrate — where a visual difference is evidence and needs to be read, not explained
away by a restyle landing in the same commit.

## Consequences

- The class canvas gains zoom, pan, marquee selection, multi-select move, undo/redo
  and keyboard handling, and looks like the two canvases beside it.
- `api/web/infomodel-editor.js` loses its drawing half — the hand-built SVG and the
  pointer-drag it dragged boxes with, some 300 lines — and gains the reconciliation
  that replaces the redraw: 1096 lines to 924. It keeps the panel, the validation
  strip and the save path.
- A second vendored diagram-js copy ships until the two bundles are merged. That merge
  is the named follow-up.
- The e2e suite addresses shapes through diagram-js's element registry rather than by
  querying raw SVG, so the existing selectors in `e2e/infomodel.spec.mjs` change even
  where the behaviour does not.

## Links

- ADR-0230 — the information model this canvas draws
- ADR-0189 — the Panorama canvas, and the vendoring pattern this follows
- ADR-0012 — buildless web assets, which is why the bundle is pre-built and committed
