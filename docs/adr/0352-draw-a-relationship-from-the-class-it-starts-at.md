# ADR-0352: Draw a relationship from the class it starts at

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Patrick Blumer

## Context and problem statement

Drawing a relationship on the class canvas is a **mode**. The author arms a kind in the
palette — association, aggregation, composition, generalization — and the next two
classes clicked become its ends. [ADR-0237](0237-class-canvas-on-diagram-js.md) brought
the canvas to diagram-js and kept that gesture, because the palette was what there was.

A mode costs the author three things at once. It has to be entered, so the kind is
chosen before the classes it will join are looked at. It has to be *remembered*, because
between the two clicks the canvas looks exactly as it did before except for one lit
button. And it has to be aimed from memory: the first click is the *from* end and the
second the *to* end, which is a direction nothing on screen states while the mode is on.
Nothing about a composition says which of the two classes owns the other except the
order the author happened to click them in.

The BPMN modeler in the same product does not work this way, and has not for as long as
there has been one. A sequence flow is drawn by selecting an element and dragging out of
the little menu that appears beside it: the gesture starts *at* the source, so the
direction is the drag, and the target lights green or red under the pointer before the
button is released. Two canvases in one product, one of them asking the author to hold a
mode in their head and the other not, is not a difference the author asked for.

## Decision drivers

- **The same gesture in both canvases.** An author who has drawn a sequence flow should
  not have to learn a second way to draw a line.
- **The direction is the drag.** Which end a composition's diamond goes on is the
  question the notation turns on, and a gesture that starts at the source answers it
  without anybody having to remember a convention.
- **A refusal belongs under the pointer.** The subset (ADR-0230) already knows what may
  be drawn between two stereotypes; asking it on hover turns a rule into feedback.
- **The document is the editor's.** Every line on the canvas is rebuilt from the
  editor's model on every reconcile, so nothing drawn may be created *in* the canvas.
- **Nothing the palette taught may be lost.** The palette's refusal explains the
  notation rather than only saying no, and it has done so since the subset existed.

## Considered options

1. **A context pad, as the BPMN modeler has.** diagram-js ships one; the entries come
   from the host the way the palette's already do, and a drag out of one starts
   diagram-js's own `connect` with live rule feedback.
2. **Keep the mode and improve it** — a banner saying which end is next, an arrow
   following the pointer. Cheaper, and it treats the symptom: the mode is the problem.
3. **Drag from the class's edge, with no menu.** No icons to place, but the kind then has
   to be chosen *after* the drop, in a popup — which puts the choice back where it was
   and adds a step.
4. **Write the pad by hand.** A floating toolbar, a drag with rule feedback and a preview
   line, all of which diagram-js already has and bpmn-js users already know.

## Decision outcome

Chosen option: **option 1 — diagram-js's context pad, fed by the host.**

### Design

**What the pad offers.** For a class, one entry per relationship kind *that class could
actually reach something with*, and a bin. The matrix is served, so the pad asks it:
offering a generalization on an enumeration would be a button whose only possible
outcome is the refusal. A value type gets association and generalization; an enumeration
gets the bin alone. A store or a relationship gets the bin. A derived line — the store's,
the lifecycle's, an attribute's type — gets nothing, because there is nothing to do to
one.

The icons are the palette's, by class name. A composition drawn from the pad and one
drawn from the palette are the same thing and must not look different.

**How a relationship is created — and where.** A drag out of the pad runs diagram-js's
`connect`, which asks the `connection.create` rule on every hover and paints the target
accordingly. The rule is asked the *narrower* question: not "may these two relate" but
"may they relate like this", because the kind is known before the target is chosen. The
kind is held in a service of its own for exactly that reason — `Connect.start` takes a
shape and a point and has nowhere to put it.

On the drop the canvas **reports** and creates nothing. diagram-js would add the
connection to its own model, where it would live until the next reconcile rebuilt every
line from the editor's document and silently dropped it. The handler runs above
diagram-js's own and stops it, so "stopped" means it never ran rather than it ran and was
undone.

Both ways of drawing then end in one function in the editor. The matrix that refuses, the
sentence it refuses with, and the shape of what is created cannot differ between them.

**A refused drop still teaches.** bpmn-js drops a gesture the rules refuse in silence: the
target never lit, and that is the whole of the feedback. This canvas has explained the
notation at exactly this moment since the palette did the drawing — *an enumeration is a
closed set of values, not something a relationship can point at* — and going quiet
because the gesture changed would be a regression dressed as parity. A refused drop says
why, in the server's own words.

**The armed palette mode stays.** It is tested, it teaches, and it is the only way to
draw a relationship without a pointer that can drag. The pad is the better path and the
one an author will find; removing the other is a separate decision with its own cost.

### Consequences

- **Positive:** the direction of a relationship is now the direction of a gesture, and
  the subset answers before the button is released rather than after.
- **Positive:** the two canvases in the product are drawn the same way.
- **Negative:** the bundle grew 15,956 bytes — the pad arrives with connect, its preview,
  overlays, interaction-events, scheduler and dragging. It is the palette's bargain
  again: library rather than renderer, replacing something Atlas would otherwise write
  and then maintain.
- **Negative:** diagram-js's container clips its own overflow, so a class sitting flush
  against the right edge of the canvas opens a pad that is partly not there. bpmn-js has
  the same geometry and the same edge; a fit gutter was tried and withdrawn, because
  reserving the room changes the zoom of every fitted model to fix a case the author
  leaves by panning.
- **Negative:** two ways to draw one line now exist. Deliberate, for the reasons above,
  and a thing to revisit rather than to leave unexamined.
- **Follow-ups:** the pad has no *append* — no "new class, already connected" — because
  that needs a stereotype and a kind per icon and would be a longer list than the palette
  it was meant to shorten. And no wrench: changing a class's stereotype is a field in the
  panel, and a popup menu is another module.
