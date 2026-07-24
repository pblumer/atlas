# ADR-0052: Message end events

- **Status:** Accepted
- **Date:** 2026-07-24
- **Deciders:** Atlas engine team

## Context and problem statement

ADR-0020 gave Atlas message correlation, and its throwing side is an
**intermediate message throw event**: on activation it publishes a named message
with a correlation key, wakes any instance waiting on it, then takes its outgoing
flow. The catching side comes in four shapes the modeler already exposes — message
start, message intermediate catch, and message boundary events — each of which the
properties panel offers a picker to select or create the shared `<bpmn:message>`.

BPMN has one more throwing shape the modeler let authors draw but the engine did
not implement: a **message end event** — an `<endEvent>` carrying a
`<messageEventDefinition>`, i.e. "publish this message, then end". The stock
bpmn-js replace menu (the wrench) offers it, so authors created them, but:

- The compiler registered **every** `<endEvent>` as a none end event, silently
  dropping the `messageEventDefinition`. A model that looked like it sent a
  message on completion deployed and threw nothing — a silent no-op, the worst
  kind of surprise.
- The Implement-tab properties panel had no branch for `bpmn:EndEvent`, so a
  message end event showed only its name/ID with no way to select or create the
  message — the gap a user reported against the modeler.

The question: everywhere a message *can* be sent, the modeler should let you
configure it and the engine should execute it. A message end event was the one
send point where neither was true.

## Decision drivers

- **No silent drops.** A modeled message send must either execute or fail loudly
  at deploy — never compile to nothing.
- **Hold the invariants.** Correlation must be a command-path side effect with a
  deterministic, side-effect-free `applyToState` on recovery (I4), keys frozen
  into events (I6), no per-command allocation (I1).
- **One correlation mechanism.** Reuse the ADR-0020 publish/throw path, not a
  parallel one.
- **UI consistency.** A message end event should offer the same message
  select/create picker as every other message event.

## Considered options

1. **Execute it** — compile a message end event to its own runtime behavior that
   throws like a message throw event and ends like a none end event, and show the
   picker in the panel.
2. **Reject it** — make the compiler error on a message end event (as it already
   does for send/receive tasks) and have the panel point authors to an
   intermediate message throw event before a plain end.
3. **Leave it** — status quo: silent drop, blank panel.

## Decision outcome

Chosen option: **"Execute it"**, because a message end event is standard BPMN and
its semantics fall out of machinery Atlas already has — it is the send-and-stop
*union* of two existing behaviors, needing no new correlation, subscription, or
recovery story.

- **Compiler.** A new `TypeMessageEndEvent`. `<endEvent>` gains an optional
  `messageEventDefinition`; when present the node compiles via
  `AddMessageEndEvent`, which reuses the **throw** detail table (message name +
  compiled correlation-key expression) since it throws identically. A plain
  `<endEvent>` is unchanged — still a none end event. An unknown `messageRef` is a
  compile error, exactly like throw/catch.
- **Engine.** `messageEndEventBehavior` is the union: `OnActivated` correlates
  exactly like the throw behavior (read payload, `correlateMessage`, evaluate the
  key on the command path — I4/I6); `OnCompleting` ends the instance exactly like
  the none end event (emit `Completed`, and if it was the last active child,
  complete the process instance) rather than taking outgoing flows.
- **UI.** The Implement tab gains a `bpmn:EndEvent` branch: with a message
  definition it renders the shared message picker (the generic
  `#f-msgref`/`#f-msgname`/`#f-corrkey` wiring already handles any element that
  carries a `MessageEventDefinition`); without one it explains that the wrench
  turns it into a message end event.

### Consequences

- **Positive:** The last message *send* point is now configurable and executable;
  no BPMN message send in the modeler silently does nothing. A message end event
  reuses the throw path, so a throw event and a message end event correlate
  identically.
- **Negative / trade-offs accepted:** A twentieth `BpmnType` and a small amount of
  behavior code duplicated between `messageEndEventBehavior.OnCompleting` and
  `endEventBehavior.OnCompleting` (the instance-completion check). Keeping them as
  separate, obvious methods was preferred over a shared helper that both event
  kinds route through.
- **Follow-ups / risks to watch:** Message **end** events inside an embedded
  subprocess scope complete that scope's child count like any end event, but
  subprocess scoping is not yet exercised here; revisit when subprocesses land.
  Send/receive **tasks** remain unimplemented (ROADMAP) and still reject at
  deploy — this ADR is about the end-event send point only.

## Pros and cons of the options

### Option 1 — Execute it
- Good: standard BPMN; no silent drop; reuses existing correlation + recovery;
  completes the modeler's message-send story.
- Bad: a new type and behavior to maintain.

### Option 2 — Reject it
- Good: smallest change; honest.
- Bad: refuses a standard, cheap-to-support BPMN element and pushes a diagram
  rewrite onto the author for no engine reason.

### Option 3 — Leave it
- Good: none.
- Bad: silently deploys a message send that throws nothing; blank panel.

## Links

- builds on ADR-0020 (message correlation) and ADR-0035 (message start events)
- relates to ADR-0040 (boundary events) — the message boundary event is the other
  reuse of the catch/throw machinery
- test-driven per ADR-0018
