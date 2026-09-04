# ADR-0245: The call activity's "+" is the way into the process it calls

- **Status:** Accepted
- **Date:** 2026-09-04
- **Deciders:** Atlas web UI

## Context and problem statement

A call activity ([ADR-0076](0076-call-activities.md)) is the one element on a diagram
whose contents are somewhere else. An embedded subprocess can be drawn open; a call
activity cannot, because what it calls is a *separate model*, deployed on its own and —
at runtime — a separate instance with its own key, variables and history. BPMN says so
with a marker: the "+" in the bottom edge of the shape, which means "there is a process
in here" and points at nothing.

Atlas draws call activities on four surfaces, and until now only one of them let you
follow that pointer:

| Surface | Before |
|---|---|
| Modeler (Design / Implement) | Nothing. The called process id was in the panel as text; opening it meant remembering the id, going back to the process list, and finding it by hand. |
| Operations → live view | Nothing. |
| Operations → instance replay | A "↳" badge on any call activity whose child instance is known, plus a *Called process* link in the Details panel. |
| Operations → collaboration replay | Nothing. |

The report that forced this was from the Modeler: a call activity configured with
`identitaet-lebenszyklus` in its **Called process** panel, and no way from that screen —
or from the live view, or the replay — to get to the process it names. The reader is
holding a model that says "the rest is over there" and the tool will not take them
there. The gesture they expected is the one every modelling tool has taught them, and
the one the marker already suggests: double-click the "+".

There is prior art inside this repository, and it cuts both ways. The replay's first
drill-in *was* the marker: an invisible hotspot laid over it, on the theory that the
marker already means "there is a process inside". Operators reported never discovering
it, and it was replaced by the always-visible badge (`drawCallActivityLinks` in
`api/web/editor.js`). That experience is about **discoverability**, not about the
gesture: nothing about the badge made the marker mean less.

## Decision drivers

- **One gesture, learned once.** A drill-in that works in the Modeler but not in
  Operations is a rule the reader has to keep re-testing.
- **Discoverable without a manual.** The invisible hotspot failed on exactly this.
- **It must not collide with what the marker's shape already does.** Double-clicking an
  activity in the Modeler renames it, and that must keep working.
- **A shape has only four corners.** In the live view all four already carry a badge —
  incident, token counts, waiting task, decision — so "add another badge everywhere" is
  not available.
- **Where "in" lands differs by surface**, and the difference is the point: a modeller
  wants the callee's *model*, an operator watching a token wants the child *instance*.

## Considered options

1. **A visible badge on every surface**, as the replay has.
2. **bpmn-js's own drill-down breadcrumbs**, as used for collapsed subprocesses.
3. **Double-click on the "+" marker, plus a hover cue naming the callee**, wired
   identically on every surface, with each surface deciding where it lands.
4. **Open the callee in a new browser tab.**

## Decision outcome

Chosen option: **3 — double-click the "+", with a hover cue**, because it is the
gesture the marker already means, it costs no corner of the shape, and it is the same
motion on all four surfaces. Discoverability — the one thing that sank the invisible
hotspot — is answered by the cue rather than by giving up the gesture: hovering
anywhere on the shape rings the marker and names what is behind it ("Double-click **+**
to open “identitaet-lebenszyklus”"), so the affordance appears before the pointer is
anywhere near the 14px target.

The implementation is one function, `wireCallDrilldown` in `api/web/editor.js`. It
keys off the element being a `bpmn:CallActivity` — the marker's own condition, so a
callee that is not named yet still gets the gesture and an explanation — and hit-tests
the pointer against the marker's rectangle *in diagram coordinates*, because bpmn-js
covers every shape with one hit rect and the marker beneath it never sees a pointer
event of its own. The handler sits at eventBus priority 1500: above bpmn-js's direct
label editing (1000), so the marker never opens the rename box, and below the token
simulation's blanket gesture block (2000), so a walkthrough in progress still owns the
canvas.

Where "in" lands is each surface's own answer:

| Surface | Opens |
|---|---|
| Modeler | The callee's **draft** if one holds that process id (that is where the work is), else its newest deployed version. Neither: a toast pointing at "＋ Create new process", which is the affordance for that. |
| Live view, one instance selected | The **child instance** this caller started, on its own live view. |
| Live view, "All instances" | The called process's own live view — no single child to mean. |
| Instance replay | The **child instance's replay**, the same place the badge goes. A call activity that started no child falls back to the called process, and says so. |
| Collaboration replay | The called process's live view. |

Leaving the Modeler must not cost the caller's unsaved edits, so the drill-in owns the
save that has to happen first: a session that addresses a draft saves it (what "＋
Create new process" already did before navigating), and one that does not — a deployed
definition opened read-only, or a diagram never saved — asks before discarding. The
Called-process panel also gains an **Open called process** button, which is the same
door for a keyboard, which a double-click on a 14px marker never is.

### Consequences

- **Positive:** the model reads as one thing again — a call activity is followable from
  wherever it is drawn. The replay's badge and Details link stay: they are the
  always-visible affordance, and the gesture is now a second way to the same place.
- **Positive:** no new API. Every surface already holds the diagram, so the called
  process id comes off the model; only the runtime child link (one timeline read on the
  gesture) and the callee's deployed key cost a request, and neither rides the 1.5s
  poll.
- **Negative / trade-offs accepted:** the target is 14px plus a 6px tolerance, and it
  scales with the zoom like everything else on the canvas — on a diagram zoomed far out
  it is a small target. The panel button and the replay badge are the fallbacks.
- **Negative:** the hover cue is one more thing that appears over a diagram. It is
  bounded to call activities, which are rare, and it takes no pointer events.
- **Follow-ups / risks to watch:** the marker's geometry is bpmn-js's
  (`SubProcessMarker`), mirrored here as a constant. The e2e tests locate the marker by
  the `data-marker` attribute bpmn-js's own renderer writes, so a vendored bpmn-js that
  moved it fails the tests rather than silently unlanding the gesture.
- **Out of scope:** the compact process view inside the Tasks app
  (`mountTaskProcess`). It is a snapshot beside a form someone is filling in, and a
  navigation out of the Tasks app is not what a double-click there should mean.

## Pros and cons of the options

### 1. A visible badge on every surface
- Good: discoverable by construction; already proven in the replay.
- Bad: in the live view every corner of a shape is taken (incident, token counts,
  waiting task, decision) — the badge would have to displace one of them.
- Bad: it teaches a click on a badge, not the marker, so the marker stays inert
  everywhere a badge cannot be drawn.

### 2. bpmn-js drill-down breadcrumbs
- Good: no new gesture; bpmn-js already does this for collapsed subprocesses.
- Bad: it is built on *planes* — one file holding the parent and the child diagram. A
  called process is a separate model with its own deployment and lifecycle, so there is
  no plane to descend into, and faking one would put a second process's XML inside the
  caller's editor.

### 3. Double-click the "+", with a hover cue (chosen)
- Good: the gesture the marker already suggests, identical on every surface, costing no
  space on the shape.
- Bad: a small target, and undiscoverable without the cue — which is why the cue is not
  optional.

### 4. Open the callee in a new tab
- Good: never loses the caller's unsaved work.
- Bad: the app is a single-page hash-routed shell; a second tab means a second editor
  session on the same draft and a live collaboration session ([ADR-0140](0140-live-collaborative-modeling-sessions.md))
  against yourself.

## Links

- relates to [ADR-0076](0076-call-activities.md) — call activities, and the Modeler's
  Called-process panel
- relates to [ADR-0046](0046-single-process-step-replay.md) — the replay this drills out of
- relates to [ADR-0105](0105-per-server-call-activity-target-overrides.md) — a server
  may redirect or pin what a called process id resolves to; the drill-in follows the
  model's id, and the Call activities inventory is where the override is visible
