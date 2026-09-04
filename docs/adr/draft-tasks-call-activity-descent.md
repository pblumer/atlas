# ADR-DRAFT: The Tasks app descends into a called process instead of navigating to it

- **Status:** Accepted
- **Date:** 2026-09-04
- **Deciders:** Atlas web UI

## Context and problem statement

[ADR-0245](0245-call-activity-drilldown.md) made the "+" on a call activity the way into
the process it calls, on every surface that draws one, and named one exception:

> **Out of scope:** the compact process view inside the Tasks app (`mountTaskProcess`).
> It is a snapshot beside a form someone is filling in, and a navigation out of the
> Tasks app is not what a double-click there should mean.

That reasoning holds. The conclusion drawn from it does not: "a navigation is wrong
here" is an argument against the *destination*, not against the gesture. A task
assignee looking at the Process tab — *what has already run, and what is still ahead* —
has exactly the same question about a call activity as everyone else, and the marker in
front of them says the answer exists.

Two things make the Operations replay the wrong place to send them:

- **The form.** The Process tab sits beside a half-filled form in the same detail pane.
  A hash change tears the Tasks view down, and the entered values with it.
- **The role.** `GET /api/v1/instances/{key}/timeline` — what the replay is built on —
  is an operator's route. The person working the task is not necessarily an operator,
  so the destination may not even open for them.

## Decision drivers

- The gesture must be the same one ADR-0245 established; a marker that behaves
  differently in one app is worse than one that does nothing there.
- Nothing the assignee has typed may be lost by inspecting a diagram.
- No new permission: whatever the Process tab can already read, the drill-in may read.
- The Tasks pane is 360px of canvas — whatever this adds has to be nearly nothing.

## Considered options

1. **Navigate to the child's replay**, as the Operations surfaces do.
2. **Open the child's replay in a new browser tab.**
3. **Descend in place**: the same panel re-renders on the child instance, with a way back.
4. **Leave it out**, as ADR-0245 did.

## Decision outcome

Chosen option: **3 — descend in place.** Double-clicking the "+" re-renders the Process
tab's viewer on the child instance the call activity started, and a bar over the canvas
says where you are (`↳ KYC-Prüfung`) and offers one way back (`↩ Antragsbearbeitung`).
The variables listed under the diagram follow the descent, because a called process is a
separate instance with separate variables and showing the caller's beside the child's
diagram would quietly answer the wrong question. Descending is unlimited in depth; the
trail pops one level at a time.

It costs no new request the view was not already making: the child instance's key is on
the caller's own timeline, which this view has loaded to draw the caller's progress, and
each level reads the same two endpoints the top level does. A call activity the token has
not reached has no child to descend into and says so, briefly, where the double-click
happened — silence there would read as a broken affordance.

Nothing changes for a task whose process calls nothing: the bar exists only once you
have descended.

### Consequences

- **Positive:** the assignee can answer "what is happening inside that box" without
  leaving the task, losing form input, or holding a role they may not have.
- **Positive:** ADR-0245's gesture is now genuinely everywhere, which is the property
  that made it worth having.
- **Negative / trade-offs accepted:** the Tasks pane now has a second reading — it can
  show a process that is not the task's own. The bar is what keeps that honest, and it is
  the only chrome added.
- **Negative:** the descent is a snapshot like the level above it; nothing polls, so a
  child that moves on while it is open shows the state it was opened at. That matches
  the rest of this view, which has never polled either.
- **Follow-ups / risks to watch:** if the Process tab ever gains its own polling, the
  descent has to poll the level on screen rather than the task's instance.

## Pros and cons of the options

### 1. Navigate to the child's replay
- Good: identical to the other four surfaces; no new UI at all.
- Bad: destroys the Tasks view and the assignee's half-filled form.
- Bad: lands on an operator route the assignee may not hold.

### 2. A new browser tab
- Good: the form survives.
- Bad: the role problem remains, and the answer arrives in a window the assignee did not
  ask for and has to close.

### 3. Descend in place (chosen)
- Good: keeps the person, their form and their permissions where they are.
- Bad: one more state the Process tab can be in — mitigated by the bar being the only
  thing on screen that says so.

### 4. Leave it out
- Good: nothing to build.
- Bad: the marker sits there meaning something everywhere else. That is the report this
  record answers.

## Links

- amends [ADR-0245](0245-call-activity-drilldown.md) — the drill-down gesture, and the
  scope line this record replaces
- relates to [ADR-0028](0028-forms-and-the-tasks-app.md) — the Tasks app and its forms
