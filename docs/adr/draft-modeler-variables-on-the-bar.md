# ADR-DRAFT: Variables stays on the Modeler's bar, as a pressed button with a shortcut

- **Status:** Accepted
- **Date:** 2026-09-02
- **Deciders:** Modeler UI

## Context and problem statement

[ADR-0229](0229-modeler-bar-hierarchy.md) reduced the Modeler's editor bar from seven
equally-weighted buttons to Save and Deploy, and sent the other five into a "…" overflow
menu. The rule it applied was: the bar carries what you *act on*, the menu carries the
rest. Applied evenly, that rule put **Variables** in the menu, and ADR-0229 said so.

Using it there showed the rule does not fit that one control. Variables is neither an act
(Save, Deploy, Export XML, Documentation, Auto-layout all *do* something to the diagram or
to a file) nor a mode (Token simulation takes the editor over and puts up its own bar).
It opens a panel that answers two questions — what is this variable called, and who writes
it — and those are questions an author has *while writing something else*: a FEEL
condition, an output mapping, a script. The panel is wanted for seconds, repeatedly, in
the middle of another task. A menu charges two clicks and a change of focus for every one
of those glances, and it charges them at the exact moment attention is elsewhere.

ADR-0229 also noted a second problem that made the menu placement worse for this control
specifically: a menu row cannot look pressed. Its state had to be carried by a check mark,
which is the weakest possible rendering of "this panel is open right now" — and being open
is the whole of what this control has to say.

The question is whether the bar's rule admits an exception, and on what grounds.

## Decision drivers

- How often a control is reached for, and whether the reach interrupts something else.
- A toggle should look like what it is: a button that stays down, not a row with a tick.
- One fact per state. ADR-0229 left this toggle carrying both an `.active` class and
  `aria-pressed`, which is two places to get it wrong.
- The bar must not creep back towards seven buttons; any exception has to be arguable
  rather than convenient.

## Considered options

1. **Leave it in the menu.** ADR-0229 as written; add a shortcut so the frequent case has
   a keyboard path.
2. **Take it out, onto the bar, as a two-state button** — and give it a shortcut as well.
3. **Take it out as a plain button** that opens the panel, with the panel's own ✕ to close.

## Decision outcome

Chosen option: **"Onto the bar, as a two-state button"** (option 2). This revises one part
of ADR-0229 — where Variables lives. **Everything else in that record stands**: Save and
Deploy on the bar with Deploy filled, the four remaining controls in the menu,
`wireBarMenu` carrying its own behaviour, and the simulation mode's own exit.

It sits left of Save, behind a rule that separates what the bar *shows* from what the bar
*does* — so the exception is visible in the design rather than smuggled in as a fourth
button of the same kind.

**The state is `aria-pressed` and nothing else.** The pressed look is drawn from the
attribute (`.btn.toggle` in `app.css`, sharing its declarations with the older
class-driven `.btn.neutral.on`), and the `.active` class the toggle used to carry
alongside it is gone. One fact about the panel now feeds both what a screen reader is told
and what the eye sees, so the two cannot come apart. The Live view's Variables toggle —
the same control under the same id — already drove itself from `aria-pressed`, so this is
that idea applied properly rather than a new one.

**It answers F4**, alongside Auto-layout's F8: a bare function key, which is the
convention this editor already set, named in the tooltip so it can be found at all.
Unlike F8 it deliberately does **not** stand down while a field has focus. F8 rewrites the
diagram, so firing it mid-typing would be a surprise edit; this only shows or hides a
panel, and the moment an author most wants to know what a variable is called is while
typing the expression that uses it. F4 produces no text, so letting it through eats
nothing that was meant for the field.

Option 3 was rejected for the reason ADR-0229 itself gives: a control with a state should
show it. An open/close button that looks identical in both states leaves the author to
remember, or to look at the panel to find out — and if you have to look at the panel, the
button has told you nothing.

### Consequences

- **Positive:** the reach that happens most often costs one click or one key, and the
  control finally looks like the toggle it is. The state has one source. The menu is down
  to four entries, which also let its two group headers go — a heading over a single row
  was noise.
- **Negative / trade-offs accepted:** the bar has four visible controls again rather than
  three, and this record is an exception to ADR-0229's rule barely any time after that
  rule was written. That is a fair thing to hold against it.
- **This is a judgement about how people work, not a measurement.** What pulled Variables
  back out is an assumption about how often it is consulted and how badly a menu
  interrupts. Nobody measured it. If the bar starts collecting further exceptions on
  similar reasoning, the rule is gone and what should follow is a record that replaces
  ADR-0229's outcome outright — not a third exception.
- **Follow-ups / risks to watch:** the Live view's Variables toggle does the same job
  under the same id and the same `aria-pressed` state, but does not use `.btn.toggle` — it
  dims rather than tints, so the two look slightly different. Adopting the class there is
  a one-line change for whoever next touches that view. F4 and F8 are still advertised
  only in tooltips; the editor has no place that lists its shortcuts.

## Pros and cons of the options

### Option 1 — leave it in the menu
- Good: keeps ADR-0229's rule intact and the bar at three controls.
- Good: a shortcut alone would serve anyone who learns it.
- Bad: charges two clicks for the most frequent, most interrupting reach in the editor.
- Bad: leaves the state on a check mark, which is the weakest rendering of "open now".

### Option 2 — a two-state button on the bar (chosen)
- Good: matches how often the control is used and what it has to say.
- Good: `aria-pressed` driving the look removes a second flag that could disagree.
- Bad: an exception to a rule written in the immediately preceding record.
- Bad: one more thing on the bar, which is what ADR-0229 set out to reduce.

### Option 3 — a plain button, closed by the panel's ✕
- Good: simplest possible control; no state to render.
- Bad: pressing it when the panel is already open does nothing visible, or worse, reads as
  broken.
- Bad: gives up the thing that makes a toggle worth having.

## Links

- revises one part of [ADR-0229](0229-modeler-bar-hierarchy.md) — the bar's hierarchy,
  which otherwise stands. ADR-0229 is left as written: a record accepted on `main` is
  immutable (see [README](README.md)), and it is not wholly superseded, so its status line
  is untouched too.
- relates to [ADR-0078](0078-design-view-token-simulation.md) — Token simulation, the one
  control that stays in the menu because it is a mode
- relates to [ADR-0028](0028-forms-and-the-tasks-app.md) — the linked form whose fields the panel groups
