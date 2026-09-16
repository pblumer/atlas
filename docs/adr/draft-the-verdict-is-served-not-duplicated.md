# ADR-DRAFT: The verdict is served, not duplicated

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-15
- **Deciders:** Patrick Blumer
- **Open question:** whether the same route should serve the BPMN canvas, whose data-flow
  checks are also run only on deploy. The reasoning is identical; the document is far
  larger and the checks read deployed neighbours, so neither the cost nor the "reads no
  stored model" property carries over unexamined. This record decides the information
  model only.
- **Question checked:** 2026-09

## Context and problem statement

[ADR-0230](0230-process-information-model.md) put one rule set behind the information
model and made a point of not copying it into the browser: the authorable subset — which
relationship may connect which pair of stereotypes — is *served*, so that the canvas
refuses mid-drag exactly what the server refuses on write.

Everything the subset cannot express was left on the other side of Save. `Validate` also
judges whether a store names a class that exists, whether an attribute's type resolves,
whether a lifecycle's states still match the enumeration it took them from
([ADR-0306](0306-a-lifecycle-may-take-its-states-from-an-enumeration.md)), whether a
business object declares an identity. The canvas has a Problems panel, but what it showed
were the findings of the **last save**. So an edit that broke the model was silent while it
was being made, and the refusal arrived later, naming an edit the author had already
stopped thinking about.

That is one defect with three faces, and two of them were filed separately before this
record was written: a rename that left a store pointing at a class name no longer present
(#946, fixed by following the rename), a delete that did not say what held the class
(fixed by naming the holders, [ADR-0331](0331-deleting-a-dmn-reference-says-what-it-breaks.md)),
and a stereotype switched away from `businessObject`, which invalidates a store and was
left open on the roadmap (#947). Each fix addressed one edit. The category is not "these
three edits"; it is that the model is judged at a different time from when it is changed.

The question this record answers is where the judgement runs.

## Decision drivers

- **One rule set.** ADR-0230 refused two copies of the far smaller relationship matrix.
  `Validate` is an order of magnitude larger and changes more often; two copies of it
  would drift, and the copy the author sees is the one that would be wrong.
- **The finding must reach the author while the edit is in their hands.** A finding is
  worth what the author's context is worth at the moment it appears.
- **Cheap enough to ask on every edit.** If the check costs a loop turn, a lock, or an
  application scope, it cannot be asked while typing, and it will be asked on save again.
- **A model mid-edit is expected to be invalid.** Half a class is the normal state of the
  document, not a fault.
- **The caret must survive the answer.** Whatever the answer repaints, it cannot be the
  row that is being typed in.

## Considered options

1. **Port `Validate` to JavaScript.** The canvas judges its own document.
2. **Serve the verdict.** A route takes a document the caller is holding, judges it, stores
   nothing, and answers with the findings.
3. **Validate the saved draft.** Keep judging on save, but save more often (autosave), so
   the verdict is never far behind.
4. **Extend the subset.** Express more rules in the served table so the canvas can apply
   more of them itself.

## Decision outcome

Chosen option: **"Serve the verdict"** — `POST /api/v1/infomodel/validate` takes classes,
associations and stores, returns `Validate`'s result, and touches nothing.

Three properties are what make it usable from a keystroke:

- **It reads no stored model,** so it needs no loop turn and no application scope. Like the
  subset route, asking discloses nothing about what exists — the caller already holds the
  document it is asking about.
- **It writes nothing.** The route is a pure function of its request body, which is also
  what makes it trivially testable and safe to call at any rate.
- **An invalid document is `200` with findings, not an error.** A model mid-edit is expected
  to be invalid; answering the normal case with a fault would teach the canvas to ignore the
  answer, which is the failure mode this record exists to remove.

The canvas calls it debounced from `markDirty`, drops any answer that a newer edit has
already overtaken, and repaints only the problems bar and the marks on the drawing — never
the side panel, which is what keeps the caret where the author left it. When the server
cannot answer, the last verdict stands rather than the bar blanking: a stale finding is
closer to the truth than a clean bill of health nobody checked.

### Consequences

- **Positive:** the whole "silent until save" category closes at once, including the
  stereotype switch left open on #947, rather than one edit at a time. Save's refusal stops
  being a surprise, because the bar has been saying it since the edit was made.
- **Positive:** there is still exactly one copy of the rules. When `Validate` gains a check,
  the canvas gains it with no second change.
- **Negative / trade-offs accepted:** the panel now depends on the network. Typing offline
  shows the last verdict rather than a current one; the bar does not claim a document is
  clean when it does not know.
- **Negative:** a request per edit burst. It is a pure computation over a document that is
  already in memory on the client side and small by construction — a document too large for
  this to be cheap is a document the canvas cannot draw either.
- **Follow-ups / risks to watch:** if `Validate` ever grows a check that reads stored state,
  the route stops being scope-free and this record has to be revisited before that check
  lands.

## Pros and cons of the options

### Option 1 — Port `Validate` to JavaScript
- Good: instant, offline, no request per edit.
- Bad: two rule sets. ADR-0230 rejected this for the relationship matrix, which is one
  table; `Validate` is several hundred lines and changes with every modelling rule added.
  The drift would appear as the canvas passing a document that Save then refuses — which is
  precisely today's defect, with the blame moved.

### Option 2 — Serve the verdict
- Good: one rule set; findings arrive with the edit; nothing stored; no scope needed.
- Bad: needs the server to be reachable; one request per edit burst.

### Option 3 — Validate the saved draft (autosave)
- Good: reuses the existing path exactly.
- Bad: writes on every edit — a stored revision per keystroke burst, and a document that is
  *deliberately* half-finished becomes durable state other readers can see. It makes saving
  the thing that judges, which is the coupling this record is undoing.

### Option 4 — Extend the subset
- Good: keeps the current shape; the canvas stays self-sufficient.
- Bad: the remaining rules are not a table. "This store names a class that exists" is a
  question about the document as a whole; expressing it as served data means serving the
  document back to itself.

## Links

- relates to [ADR-0230](0230-process-information-model.md) — the rule set, and the decision
  not to duplicate the subset
- relates to [ADR-0306](0306-a-lifecycle-may-take-its-states-from-an-enumeration.md) — one
  of the checks that was silent until save
- relates to [ADR-0331](0331-deleting-a-dmn-reference-says-what-it-breaks.md) — saying what
  an edit breaks, at the moment it is made
