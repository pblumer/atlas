# ADR-0465: A step's form is opened from the step, and the way back links it

- **Status:** Accepted (amended 2026-10-19: the canvas has the door too — "Open form" in the context pad of an element that names a form, and in the Implement view a form mark on a start event or user task that is a link to it; the canvas asks the server whether the form exists before it saves or leaves anything; amended 2026-10-20: a form created through "＋ New form" is filed under the diagram's application)
- **Implementation:** Landed
- **Date:** 2026-10-19
- **Deciders:** Atlas maintainers
- **Open question:** A form is bound by id and has no versions (ADR-0028): a save made
  through these doors changes the form for every running task and every process that
  names it, at once. Whether the form editor should say what binds a form before it
  saves — or whether a form should be versioned with the processes that bind it — is not
  settled here. The doors make that save easier to reach, not different in kind.
- **Question checked:** 2026-10

## Context and problem statement

A plain start event and a user task each name a form (`zeebe:formDefinition`,
ADR-0028), and a service, send or business rule task can name a repair form (ADR-0169).
The Implement panel showed which form, in a picker, and offered one link: "Create a new
form", which opened a second tab and ended with "then reopen this to link it".

So the form a step names could not be reached from the step. Opening it meant leaving
for the Modeler home and finding it in a list; creating one meant a second tab, a save
there, and a return to a panel whose picker did not know the new form yet. The request
was put plainly: when a process starts with a form, it should be possible to jump into
that form, and the same for a user task.

The other two artifacts a diagram links already had that door. A business rule task has
"＋ New decision" and "Edit", saves the diagram before it leaves, carries the way back in
the decision editor's address, and adopts what was authored on return (ADR-0320). A
call activity opens the process it calls from its "+" and from an Open button
(ADR-0245). The form was the one linked artifact without one, against ADR-0320's own
driver: one Modeler, one grammar.

## Decision drivers

- **The decision editor's grammar.** Reached, left and come back from the same way:
  save first, the way back in the address, the result adopted on return.
- **Leaving costs nothing.** Neither the diagram's edits nor the form's.
- **A stale record must not rewrite a link.** What a round trip leaves behind outlives
  the trip when the author does not come back the way they went.
- **Keep the plain link.** A middle click, "open in new tab" and "copy link" reaching
  the form's own address are worth keeping for an author who wants both side by side.
- **Stay off the engine.** A design-time UI concern: nothing in the compiler, the
  processor, the log or the form store changes.

## Considered options

1. **Keep the new-tab link and add an Open link beside it, both `target=_blank`.**
2. **Doors in the panel, a round trip in the same tab, with the way back and a link on
   return (chosen).**
3. **The form editor as an overlay over the diagram.**

## Decision outcome

Chosen option: **"Doors in the panel, a round trip in the same tab" (option 2)**, because
it is the grammar the decision editor already taught, and the new-tab case survives
inside it.

- **Two doors under every form picker:** "＋ New form" and "Open form", on a start
  event's start form, a user task's form and a repair form alike. Both are real links.
  Open follows the picker before anything is saved, and is shut — no href,
  `aria-disabled` and a title saying why — while nothing is picked or the picked form is
  not on this server.
- **A plain click is the round trip.** The diagram is saved first, or the author is
  asked where there is no draft to save it to (`makeKeepEdits`, the question every door
  out of the Modeler asks). The form editor opens with a `/from/{draft}/{element}` tail
  on its address, which the router takes off before the form routes match, so each keeps
  the shape it had. A modified click is left to the browser: the form opens in a new
  tab at its plain address, and the diagram stays open here with nothing saved.
- **Back goes to the diagram.** With a `/from/` tail the form editor's back link names
  the draft and opens it; a save keeps the tail on the address.
- **The trip is a record in sessionStorage** (`api/web/form-return.js`), written as the
  diagram leaves with what the element named then, and completed by the form editor's
  save with the id the form is stored under. Coming back to that draft takes it,
  once: the element is selected again, and the saved id is linked when it differs from
  what the element names — a form created for it, or the one it opened, renamed under
  ADR-0222. Only when the element still names what it named as it left: anything else
  means the link has changed since, and the record is the stale one. Opening any draft
  ends the trip, as the decision editor's `takeAdoption` does.
- **The way back asks before it drops the form's edits.** The form editor compares the
  form on screen with the one form-js held when it opened or was last saved, and the
  back link asks before discarding a difference.
- **A repair form's "＋ New form" stays the plain one**: the generator writes work forms
  and start forms, not the subset of variables an operator corrects (ADR-0260). It
  still carries the way back.

### Consequences

- **Positive:** A step's form is one click from the step, and a form created for a step
  arrives linked to it. A form renamed on the way no longer leaves its step naming an
  id that does not exist — the one case ADR-0222 left to the author, closed for the
  path most renames now take.
- **Positive:** The new-tab behaviour is not lost; it is what a modified click does.
- **Negative / trade-offs accepted:** A plain click now leaves the diagram, where the
  old link opened a second tab. An author who wanted both side by side has to know the
  modified click.
- **Negative / trade-offs accepted:** A deployed version opened in the Modeler has no
  draft to come back to, so its doors open the form plainly, without a way back or a
  link on return — the decision editor's behaviour from the same place.
- **Negative / trade-offs accepted:** The trip lives in one tab's sessionStorage. A
  `/from/` address opened in another tab gets the way back and nothing more, and a
  browser that refuses session storage gets the way back without the link.
- **Follow-ups / risks to watch:** The canvas has no form door yet — a context-pad
  entry on an element that names a form would be the next step, and it would be a
  different click on the same `wireFormDoors` path. The browser's own back button is
  not guarded in the form editor, as it is not in the decision editor.

### Amendment (2026-10-19): the canvas has the door too

As landed, the doors were in the panel alone, and the record named the canvas as the next
step. The author is usually looking at the drawing, not at the panel, when they want the
form behind a step — the reason ADR-0245 put the call activity's door on its marker.

**Decision.** The canvas gets the same door, through the same round trip
(`walkFormDoor`): save first, the trip recorded, the way back on the address.

- **"Open form" in the context pad** of an element that names a form — a start event, a
  user task, a repair form's task — and of no other. On an element without one there is
  nothing to open, and creating one stays the panel's "＋ New form", beside the picker
  that links it. A modified click opens the form's plain address in a new tab and leaves
  the diagram as it is, as the panel's links do.
- **A form mark in the Implement view** on a start event and a user task that name a
  form: a small sheet in a user task's top-right corner, opposite the person bpmn-js
  draws, and on a start event's upper left, away from the context pad that opens on its
  right. It is drawn with the implementation badges and only where they are, so the
  Design view stays the drawing it is. The mark is a link: a click is the same door, a
  modified click a new tab. A repair form is not marked — it is an operator's aid for an
  incident, not a step anybody fills in, and a mark on every such task would say
  otherwise. The context pad and the panel still reach it.
- **The canvas asks the server whether the form exists before it leaves.** The panel
  knows from its forms list and shuts its door on a missing form; the canvas holds no
  such list, and a door that opened on a failure would have cost a save for nothing. A
  form the server does not hold is said in a message, and nothing is saved or left.
- **A double-click stays the label editor's**, for ADR-0245's reason: a task has no
  marker that means "form", and taking the whole shape would take renaming with it.

**Consequences.** A step's form is reachable from where the step is drawn. The mark adds
one more badge to the Implement view, where a reader already looks for how a step is
implemented. The existence check is one request per click, made only on the canvas.

### Amendment (2026-10-20): a new form is filed under the diagram's application

As landed, "＋ New form" opened the form editor at `#/modeler/form/new/for/…`, an address
that named the process and the step for the generator but no application — the "Create a
new form" link it replaced had never named one either. So every form created from a step
was saved under none: it showed among the ungrouped forms, apart from the process it was
written for, and an application's release did not carry it. The round trip made such
forms more frequent, which is what brought the gap up.

**Decision.** A diagram filed under an application names it on its "＋ New form" door —
`#/modeler/form/new/p/{application}/for/…`, and `#/modeler/form/new/p/{application}` for
a repair form — and the form editor files the new form there, as it already does for a
form created from the application's own "Create new" menu.

- **The application is the diagram's**: the draft's, a deployed version's (the one it was
  deployed from), or the one a new diagram is created in. A diagram filed under none
  files its forms under none, as before.
- **It is part of the link**, not looked up by the form editor, so a modified click into
  a new tab files the form the same way, and a deployed version, which has no draft to
  come back to, does too.
- **Access is unchanged.** Filing a form into an application needs the editor role there
  (ADR-0071), the same role that saves the draft the form was created from; a save the
  server refuses is reported like any other failed save.
- **An existing form keeps its application.** "Open form" opens a form where it is; only
  a form created through the door is filed.

## Pros and cons of the options

### Option 1 — two new-tab links
- Good: nothing is saved or left; the diagram keeps its zoom, selection and live session.
- Bad: the picker in the first tab does not learn about a new form, nothing is linked,
  and a renamed form leaves its step naming a missing id. It is the gap described above
  with a second link in it.

### Option 3 — an overlay
- Good: no navigation at all.
- Bad: ADR-0320 reversed exactly this for decisions: an overlay has no address, browser
  back drops its edits, and the form-js Playground is a whole page of editor that the
  form editor already hosts.

## Links

- relates to ADR-0028 (forms bound by id), ADR-0169 (repair forms), ADR-0222 (a changed
  id is a rename), ADR-0245 (the call activity's way into its callee), ADR-0260 (the
  generator opens on the step), ADR-0320 (the decision editor is a page)
