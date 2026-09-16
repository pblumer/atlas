# ADR-0363: The drawing says which members are used

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Patrick Blumer

## Context and problem statement

[ADR-0338](0338-where-a-business-object-is-used.md) built the reading that answers what a
change to a class would break, and put it on a page: **Data › Business objects**, one class
at a time.

The question is not asked there. It is asked on the class diagram, with the member under
the cursor and the decision half made — and getting the answer means leaving the drawing,
finding the class in a list, reading a table, and coming back. Most people do not make that
trip, which means the reading exists and the decision is still taken blind.

So: bring the answer onto the drawing. That is not a new reading, and this record is not
about computing anything. It is about what a *picture* is allowed to claim, which is a
harder question than it looks, because a faint member reads as "safe to delete" and nobody
hovers a tooltip before concluding that.

## Decision drivers

- **One reading.** A second computation behind the drawing would be a second answer to the
  same question, and the two would differ exactly when it mattered.
- **Faint must never be a claim the reading cannot support.** What a process writes is
  visible in the model; what a FEEL expression reads out of an object it took whole is not
  ([ADR-0301](0301-derive-the-model-from-the-processes.md)'s whole-object gap). A shading
  that implied otherwise would be worse than none.
- **The business key must never fade.** No write ever names it, and it is what every store
  lookup and cross-process correlation resolves against — a naive "nothing names it"
  would point at the one member that must not go.
- **An «enumeration» must not read as dead.** Most are declared by no data object at all,
  which is the same trap ADR-0338 named.
- **Authoring must not be shaded at somebody who did not ask.** Every attribute is unused
  the moment it is typed, and a canvas that greys out new work is a canvas people turn off.

## Considered options

1. **Always on.** Shade every class diagram from the reading, all the time.
2. **A toggle on the canvas.** Off until asked for; a legend on screen while it is on.
3. **A count beside each member** instead of a shading — "3 processes write this".
4. **Nothing on the drawing.** Leave the answer on the Business objects page.

## Decision outcome

Chosen option: **"A toggle on the canvas"** — a control beside zoom and undo, off by
default, that asks `GET /api/v1/infomodel/classes` once for this model's application and
shades from the answer.

What the shading is entitled to say, exactly:

- **A member is used** when a deployed process names it — a write targeting that member
  ([ADR-0060](0060-field-level-data-object-writes.md)) — *or* it is part of the business
  key and any deployed process uses the class at all. The key is used by every use there
  is, so it is marked used rather than exempted, which is the same statement without a
  special case in the drawing.
- **A member is not named** when nothing deployed writes it by name. Faint means *nothing
  names it*, not *nothing uses it*, and the legend says so in those words, on screen for
  as long as the shading is.
- **A class is used by nothing** only when no deployed process uses it **and** the
  vocabulary does not either. Both halves, or an enumeration greys out.
- **A name the reading has never seen is left alone.** A class added since, or renamed a
  moment ago, is neither marked nor faded: the reading made no claim about that name, and
  inventing one would turn every rename into a scare.
- **Members are shaded only where a process uses the class.** Member-level facts come only
  from process writes; where there are none, there is nothing member-level to say.
- **A literal is read through the lifecycles that borrow it.** No process ever names a
  literal; what a process names is a *state* — a `<dataState>` on a write — and a literal
  becomes a state only where some class's lifecycle takes its states from that enumeration
  ([ADR-0306](0306-a-lifecycle-may-take-its-states-from-an-enumeration.md)). A literal's
  rename *is* that state's rename, which is what makes the two the same string rather than
  two that happen to match. So the question is asked of the classes that borrow it: bright
  where a deployed process moves such a class into that state, faint where none does.
  Asked, though, only where it can be answered — an enumeration nothing borrows from, or one
  whose borrowers no deployed process uses, is left unshaded, because "no process reaches
  this state" and "no process was in a position to" are different claims and fading a state
  machine nothing drives would report the second as the first.

The rule lives with the host, not in the drawing: the canvas is handed the class ids to
fade and the member names to bring forward, the same way it is handed the shapes a
relationship cannot land on. One copy of the rule, and the vendored bundle gains no reading
of its own.

### Consequences

- **Positive:** the question gets asked where it is actually asked, and the answer is the
  same one the Business objects page gives.
- **Positive:** a diagram that has been shaded is a diagram somebody asked to shade, so the
  legend is on screen whenever a member is faint.
- **Negative / trade-offs accepted:** off by default means it is found rather than seen.
  A toggle is discoverable; a shading nobody asked for is a diagram that appears broken
  during authoring, which is the worse failure.
- **Negative:** the reading is taken once, when it is switched on. What it reads — deployed
  processes — does not change while somebody is drawing, but a deployment during a long
  editing session is not picked up until the toggle is pressed again.
- **Follow-ups / risks to watch:** if the reading ever gains member-level facts from reads,
  "faint" gets stronger and the legend has to stop hedging.

## Pros and cons of the options

### Option 1 — Always on
- Good: nothing to find; the answer is simply there.
- Bad: every new attribute is faint the moment it is typed, and the legend would have to be
  permanent furniture to keep that honest. It shades authoring at somebody who was not
  asking a question about usage.

### Option 2 — A toggle
- Good: the shading and the sentence that qualifies it arrive together; authoring is
  untouched until asked.
- Bad: a control to find, and a reading that is a snapshot rather than live.

### Option 3 — A count beside each member
- Good: says *how much*, not just whether; no interpretation needed.
- Bad: it is the reading rendered as a table on a diagram. A class box is already as wide as
  its widest member, a count widens every one of them, and a number beside a member that
  processes read whole is still zero — the same wrong claim, in stronger language.

### Option 4 — Nothing on the drawing
- Good: one place to read usage, no second surface to keep honest.
- Bad: it is the status quo, and the status quo is that the answer exists and the decision
  is taken without it.

## Links

- relates to [ADR-0338](0338-where-a-business-object-is-used.md) — the reading this shows
- relates to [ADR-0301](0301-derive-the-model-from-the-processes.md) — the whole-object gap
  that keeps "faint" from meaning "unused"
- relates to [ADR-0237](0237-class-canvas-on-diagram-js.md) — the drawing, and why the
  rules stay with the host
