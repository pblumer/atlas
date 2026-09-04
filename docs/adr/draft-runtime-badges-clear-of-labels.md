# ADR-DRAFT: Runtime badges hang outside the shape, clear of its caption

- **Status:** Proposed
- **Date:** 2026-09-04
- **Deciders:** Atlas maintainers

> **Implementation status.** Delivered. `badgeSpot()` in `api/web/editor.js` is the one
> place that decides where a badge goes; the incident, open-task and decision badges are
> the size of the token count rather than of the sentence they used to spell out; and
> `e2e/badge-labels.spec.mjs` measures the result in a real browser instead of asking a
> reviewer to look at it.

## Context and problem statement

The Operations views annotate a shape with badges: the token counts, an incident marker,
a link to a waiting user task, a button that opens an evaluated decision, an execution
count and a call-activity drill-in. Which corner each takes is a convention the code
already documents — `drawCallActivityLinks` put the drill-in "in the shape's one free
corner — the type icon, the execution count and the incident badge hold the other three".
The convention is good and this record does not change it.

What it changes is that a corner used to mean *inside* the shape, and inside the shape is
where the words are. Measured in a browser on an ordinary German process model
(`e2e/badge-labels-harness.html`, five elements, names of the length people actually
write), the old placement produced three collisions:

| badge | lands on | overlap |
|---|---|---|
| decision (`{top: 4, left: 4}`) | a business rule task's caption | 69 × 14 px |
| token count (`{bottom: 4, right: 4}`) | the start event's caption | 18 × 15 px |
| token count | the gateway's caption | 8 × 15 px |

Two different failures are behind those numbers:

- **A task's caption is drawn inside its box.** bpmn-js centres it there, and a
  three-line name in an 80px-high box leaves roughly ten pixels clear at the top and
  bottom. A 20px badge does not fit in ten pixels, so *no* inner corner of a task is free
  once the name is long — and the name is the one thing on the diagram a reader is
  looking for.
- **An event's caption is not inside it at all.** It is a separate label element, centred
  under the 36px circle and typically four to five times wider. So a badge anchored to
  that shape's bottom corner is not "in the corner of the event" — it is in the middle of
  the event's name.

The second one has a sharp edge worth writing down, because it is what made the bug
invisible in review: **diagram-js's `right` and `bottom` overlay keys are not CSS-style
anchors.** All four keys position the badge's *top-left corner*, and `right`/`bottom`
simply measure that corner from the far edge. `{bottom: 4}` therefore does not tuck a
20px badge inside the bottom border — it puts the badge's top 4px above that border and
lets the remaining 16px hang below it. Read as CSS, the old numbers look like "just
inside the corner"; what they draw is "mostly outside, downwards", straight into an
event's caption band.

None of this is visible while modelling. The badges exist only where a process is
running, so the first person to see it is an operator, on a diagram they cannot change
(which is the subject of [ADR-draft-adjust-a-deployed-diagram](draft-adjust-a-deployed-diagram.md)).

## Decision drivers

- **A badge must not cover a name.** The name is what makes a diagram readable; a badge
  is an annotation on it. When they compete, the annotation moves.
- **A badge's place is its identity.** An operator learns "the red one is bottom-left"
  and stops reading badges individually. Whatever else changes, the corner-to-meaning map
  must not.
- **The rule has to hold for shapes it was not tested on.** "These three collisions are
  gone" is not the property worth having; "a badge is never drawn where a caption is
  drawn" is.
- **Measure it, don't look at it.** bpmn-js wraps captions itself, in the font the app
  ships. Whether a pill covers a line of text is a question about rendered geometry, and
  only a browser can answer it.

## Considered options

1. **Move the labels instead**, so they dodge the badges — in the model, or in the
   auto-layout.
2. **Keep the badges where they are and shrink them.**
3. **Move every badge outside the shape**, keeping its corner, and keep the words.
4. **Move every badge outside the shape, keeping its corner, and compact the worded
   badges into a glyph plus a count.**

## Decision outcome

Chosen option: **4**, in one sentence: **a badge sits outside the shape, on the side its
caption is not — and it is the size of a count, not of a sentence.**

`badgeSpot(element, corner)` is the only place that decides this. The corner keeps its
meaning (`tl` decision, `tr` open task / execution count, `bl` incident, `br` token
counts / call-activity drill-in). What the corner resolves to depends on one fact about
the element: whether it renders an external label. If it does — events, gateways, data
objects — the band below the shape belongs to the caption, so *both* bottom badges are
placed above it instead. If it does not, the caption is inside the box and the badges use
the space above and below the border.

The size change is not decoration; option 3 does not work without it. A pill spelling out
"⚠ 2 incidents" is 90px, "📋 Open task" 87px, "⚖ decision" 73px. That is most of a task's
width and three times an event's, so two of them on one edge overlap each other wherever
they are anchored, and one of them beside a 36px event covers whatever is next to it.
Reducing them to a glyph, plus a count when there is more than one, makes every case fit
with room to spare — and the words are not lost: they are the badge's `title` and its
`aria-label`, and the thing they name is listed in the panel below the diagram either
way. For the two badges that are controls rather than labels — the task link and the
decision button — this is a return to what they already were in kind: the call-activity
drill-in has been a 20px icon button since [ADR-0245](0245-call-activity-drilldown.md),
which is also the record that established icon-and-tooltip is enough *provided the
control is always visible*. It is; the failure that record diagnosed was a hotspot that
appeared only on hover.

### Consequences

- **Positive:** a name is never covered, on any shape, by construction rather than by
  luck. Badges no longer overlap each other on small shapes. The four-corner convention
  survives intact and is now written down in one function instead of in seven literals.
  The overlay reads lighter, which matters most on the dense diagrams where it was worst.
- **Negative / trade-offs accepted:** an operator who read "incident" now reads "⚠" and
  has to hover, or look at the panel, to get the sentence. On a shape that both carries an
  external caption and needs its two bottom badges moved above it, the space above the
  shape is doing more work than before, and on a very dense diagram a badge above one
  shape can reach the row over it — which is a diagram-density problem the auto-layout's
  row spacing already governs.
- **Follow-ups / risks to watch:** the Design view's token simulation, the Playground's
  heat overlay and the Modeler's implementation badges still use their own inner-corner
  literals. They are not part of this change — the complaint is about Operations, and the
  Design views draw no captions under running counts — but if they grow the same problem,
  `badgeSpot` is what they should adopt rather than a second convention.

## Pros and cons of the options

### Option 1 — move the labels
- Good: attacks the collision at the source, and where it works the diagram itself
  improves.
- Bad: it cannot work where it is needed most. bpmn-js positions a label from the diagram
  interchange only for elements whose label is *external* — events, gateways, data
  objects, sequence flows. A task's caption is drawn inside its box by the renderer and
  has no DI bounds at all, so the worst collision in the table above is not addressable
  this way. And Atlas's auto-layout only runs on a model that carries no diagram, or when
  somebody presses Auto-layout: every model drawn in the Modeler skips it, which is
  every model this complaint is about.

### Option 2 — shrink in place
- Good: the smallest change, and it does fix the event and gateway cases.
- Bad: a 20px badge still does not fit in the ten pixels a three-line task caption leaves
  clear, so the worst case survives.

### Option 3 — outside the shape, keep the words
- Good: keeps the sentence on the diagram, where it needs no hover.
- Bad: two 90px pills on one edge of a 100px shape overlap each other; beside a 36px event
  they are the diagram. Trading "covers its own caption" for "covers its neighbour" is not
  a fix.

### Option 4 — outside the shape, compact badges (chosen)
- Good: every case fits, the convention is preserved, and the rule is one sentence that
  generalises to shapes nobody has drawn yet.
- Bad: loses the word at a glance; relies on tooltip and panel for the detail.

## Links

- relates to [ADR-0150](0150-preview-mail-provider-and-visible-incidents.md) and [ADR-0151](0151-incidents-beyond-the-live-diagram.md) — the incident badge this compacts
- relates to [ADR-0245](0245-call-activity-drilldown.md) — the four-corner convention, and icon-plus-tooltip as an always-visible control
- relates to [ADR-0249](0249-overlay-cancelled-tokens.md) — the token badges whose corner this moves
- relates to [ADR-0066](0066-decision-evaluation-records.md) — the decision badge
- relates to [ADR-draft-adjust-a-deployed-diagram](draft-adjust-a-deployed-diagram.md) — the escape hatch for the collisions no convention can prevent
