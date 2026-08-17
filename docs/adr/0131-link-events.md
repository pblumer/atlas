# ADR-0131: Link events (intra-scope goto — a compile-time synthetic flow)

- **Status:** Accepted
- **Date:** 2026-08-13
- **Deciders:** Atlas engine team

> **Implementation status.** Delivered (both phases). A **link event** is BPMN's **off-page connector**: a
> **link intermediate throw event** (`<intermediateThrowEvent><linkEventDefinition name="X"/>`) and a
> **link intermediate catch event** (`<intermediateCatchEvent><linkEventDefinition name="X"/>`),
> paired **by name within one flow scope**, that stand in for a sequence flow — reaching the throw is
> a **goto** to the matching catch, which then flows on. It is **pure diagram tidiness**: no wait, no
> subscription, no broadcast, no correlation. Atlas resolves the pair **entirely at compile time** —
> the throw→catch link compiles to a **synthetic sequence flow** (`b.Connect`) and both events reuse
> the existing `passThroughBehavior`, so a token flows throw → (synthetic edge) → catch → its real
> outgoing flow exactly as through any node. **No new runtime behavior, value type, event, or recovery
> path** — the whole feature is compile-time wiring plus two identity types for the Operations overlay
> and validation.

## Context and problem statement

A **link event** is the BPMN construct for **splitting a long or crossing flow without drawing the
sequence flow**: instead of a line snaking across the diagram (or onto another page), you drop a
**link throw** ("go to X") where the line would leave and a **link catch** ("arrive at X") where it
would land, matched by the shared **name**. Reaching the throw is *identical* to having taken a
sequence flow straight to the catch — the token continues from the catch's outgoing flow. It is the
one throw/catch pair with **no execution semantics of its own**: unlike a message (correlated wait),
a signal (broadcast), an error (failure propagation), or an escalation (raise-up-the-chain), a link
is just a **named jump within the same process**, there purely to keep a diagram readable.

Atlas cannot express one today. `<linkEventDefinition>` is not parsed — the intermediate throw/catch
structs carry only message/signal/timer/escalation/compensation pointers — and a link event falls
into the `default:` "only … supported yet" branch of the intermediate-catch and intermediate-throw
switches (`compiler/scope_compile.go:363,~400`). The Modeler blocks it outright:
`bpmn:LinkEventDefinition` is in `UNSUPPORTED_EVENT_DEFS` ("Link events aren't supported yet",
`api/web/editor.js`). A model that uses a link — common in any diagram large enough to want one —
simply won't deploy.

The question this ADR answers: **do link events need any runtime at all, or are they a compile-time
graph rewrite** — given that Atlas already turns `<sequenceFlow>` into edges (`connectScope` →
`b.Connect`) and already has a `passThroughBehavior` that completes on activation and takes its
outgoing flow?

What already exists, and is load-bearing:

- **Sequence flows are compiled edges.** `connectScope` (`compiler/scope_compile.go:792`) resolves
  every `<sequenceFlow>`'s source/target through the scope's id map and calls `b.Connect(src, tgt)`
  (`compiler/builder.go:1487`) to add the edge. A link throw→catch pair is *exactly* a sequence flow
  the modeler drew as two symbols instead of a line — so it compiles to the same edge, synthesized
  rather than read from XML.
- **A pass-through node already flows straight on.** `passThroughBehavior`
  (`engine/behavior.go:1561`) — used by an undefined `<task>` — completes on activation and takes its
  outgoing flow (`completeAndTakeFlows`), "the token flows straight through, exactly like a none
  event." A link throw (activated by its real incoming flow, taking the synthetic edge to the catch)
  and a link catch (activated by the synthetic edge, taking its real outgoing flow) are both *exactly*
  this behavior.
- **Distinct types with a shared behavior is an established pattern.** `TypeSendTask` reuses
  `serviceTaskBehavior` (ADR-0112) purely to preserve identity for Operations and validation. Link
  events do the same: `TypeLinkThrowEvent` / `TypeLinkCatchEvent` both dispatch to
  `passThroughBehavior`.
- **Scope-consistent edges are enforced.** `checkScopes` (ADR-0074) rejects a flow whose endpoints
  live in different scopes. Matching a link pair **within one flow scope** keeps the synthetic edge
  legal and matches BPMN (a link pairs within a process/scope, not across a subprocess boundary).

So the edge machinery, the pass-through behavior, the identity-type pattern, and the scope check all
already accommodate a link. What is missing is (a) parsing `<linkEventDefinition name>`, (b) the two
identity types, and (c) a **name-matching resolution pass** that adds the synthetic throw→catch edge.

## Decision drivers

- **Reuse to the point of triviality.** A link is a sequence flow the diagram split in two; resolve
  it to a real edge at compile and the entire runtime is `passThroughBehavior`, already written and
  tested. Do not invent a "link runtime".
- **Invariants hold, trivially.** No per-command allocation (I1); nothing to make durable that a
  sequence flow doesn't already (I2); `applyToState` unchanged — a link is gone by runtime, it is an
  edge (I4); the pairing is resolved at **compile** (I5); replay is unaffected because there is no new
  event or state — the synthetic edge is frozen into the CompiledProcess like any flow (I6).
- **Faithful BPMN.** A link pairs a throw to **exactly one** catch of the same **name** within the
  scope; **one or more throws** may target the **one** catch (many gotos, one label); a throw with no
  matching catch, or two catches sharing a name, is a **deploy error** (the modeled jump has no
  destination, or an ambiguous one).
- **No new failure modes.** Because it is compile-time wiring, a link cannot hang, cannot raise an
  incident, and cannot survive a crash differently from a sequence flow — there is nothing to recover.

## Considered options

1. **Resolve the pair to a synthetic sequence flow at compile time; both events reuse
   `passThroughBehavior` (chosen).** Parse `<linkEventDefinition name>` on intermediate throw/catch;
   register a link throw as `TypeLinkThrowEvent` and a link catch as `TypeLinkCatchEvent` (both
   dispatched to `passThroughBehavior`). In a per-scope resolution pass, index the scope's link
   catches by name, and for each link throw `b.Connect(throwNode, catchNode)` — the synthetic edge. A
   throw with no matching catch, or a duplicate catch name, is a deploy error. At runtime the token
   flows throw → synthetic edge → catch → the catch's real outgoing flow, entirely on existing
   machinery. Nothing new runs.
2. **A runtime link behavior with a name registry.** Give link throw/catch real behaviors: the throw,
   on activation, looks up the catch by name in a compiled link table and activates it. Rejected: it
   invents a runtime lookup and a `linkThrowEventBehavior`/`linkCatchEventBehavior` for something that
   is *statically* a jump between two nodes in the same scope — the lookup's answer is fixed at
   deploy, so it belongs at deploy (option 1). It adds code and a dispatch path for zero behavioral
   difference.
3. **Collapse the link away entirely (rewrite the throw's real predecessor to point at the catch's
   real successors).** Delete both link nodes at compile, splicing the flow. Rejected: the token would
   never *visit* the link throw or catch, so the Operations token-visit overlay (ADR-0022) and the
   step replay (ADR-0046) would lose the two events the modeler drew — the diagram shows symbols the
   trace never touches. Option 1 keeps both as real (pass-through) nodes the token visits, so the
   overlay lights them up like any event.

## Decision outcome

Chosen: **option 1 — a link pair compiles to a synthetic sequence flow, both events reuse
`passThroughBehavior`.** The genuinely new logic is (a) parsing `<linkEventDefinition name>`, (b) the
two identity types `TypeLinkThrowEvent` / `TypeLinkCatchEvent` mapped to `passThroughBehavior`, and
(c) the per-scope name-matching resolution that emits `b.Connect(throw, catch)`. There is no new
runtime, event, value type, or recovery path.

### Compiler

- Parse `<linkEventDefinition name="…">` — a `Link *xmlLinkEventDefinition` pointer (with a `Name`
  attribute) on `xmlIntermediateThrowEvent` and `xmlIntermediateCatchEvent`.
- Add `TypeLinkThrowEvent` and `TypeLinkCatchEvent` to the `BpmnType` enum + `String()`; grow
  `numBpmnTypes`. Neither carries a detail table — a link name is only needed at compile (to match the
  pair); the compiled node keeps nothing (like a none event). Register them via
  `AddLinkThrowEvent()` / `AddLinkCatchEvent()` builder methods (bare pass-through nodes) in the
  intermediate-throw and intermediate-catch switches of `registerScope`.
- **Resolution (per scope, in `connectScope`).** After the real `<sequenceFlow>` edges are wired,
  index the scope's link catches by name (`map[string]int32` name→catch node). For each link throw in
  the scope, look up its name and `b.Connect(throwNode, catchNode)` — the synthetic edge. Errors:
  - a link **throw** whose name matches no catch in the scope → deploy error ("link throw 'X' has no
    matching link catch in this scope");
  - **two link catches** with the same name in one scope → deploy error ("duplicate link catch name
    'X'"); one catch per name is the destination.
  - (Many throws for one catch name is allowed — several gotos to one label.)
- **Scope semantics:** a link pairs **within one flow scope** — the process root, or the same
  embedded subprocess — matching BPMN and keeping the synthetic edge scope-consistent
  (`checkScopes`). A throw and a catch in different scopes do not pair (the throw errors as unmatched
  in its own scope), which is the correct BPMN reading and avoids a cross-scope edge.

### Runtime

- **None.** `p.behaviors[TypeLinkThrowEvent] = passThroughBehavior{}` and
  `p.behaviors[TypeLinkCatchEvent] = passThroughBehavior{}`. A link throw is activated by its real
  incoming flow, completes, and takes the synthetic edge; the catch is activated by that edge,
  completes, and takes its real outgoing flow. Identical to a token passing through a none event.

### Modeler

- Drop `bpmn:LinkEventDefinition` from `UNSUPPORTED_EVENT_DEFS` (`api/web/editor.js`). bpmn-js already
  draws the link marker and offers the link throw/catch variants via the wrench menu, and
  `bpmn:LinkEventDefinition` is a native moddle type — so no diagram-rendering or moddle change is
  needed.
- A small **link-name field** on a link throw and a link catch (the `name` on the
  `linkEventDefinition`), with a hint that a throw jumps to the catch of the same name in the same
  scope. Optionally surface a Problems-panel hint for an unmatched name, mirroring the deploy error.

### Phased implementation plan (test-first)

- **Phase 1 — Compile, wire, and run.** Parse `<linkEventDefinition name>`; `TypeLinkThrowEvent` /
  `TypeLinkCatchEvent` + builder methods (pass-through) + the register switches; the per-scope
  name-matching resolution emitting `b.Connect(throw, catch)`; the unmatched-throw and duplicate-catch
  deploy errors; register both types to `passThroughBehavior`. Because the runtime is the existing
  pass-through, this phase is **already runnable**. *Tests:* a `start → linkThrow("A") ⇢
  linkCatch("A") → end` runs to completion, the token **visiting** both the throw and the catch and
  reaching the end (an engine test on visit counts); **two throws, one catch** both reach it; a link
  pair **inside a subprocess** resolves within that scope; an **unmatched throw** and a **duplicate
  catch name** are deploy errors; a link pair survives **compile determinism** (the synthetic edge is
  in the CompiledProcess, so recovery replays it like any flow — a recovery test).
- **Phase 2 — Modeler + docs.** Drop `bpmn:LinkEventDefinition` from `UNSUPPORTED_EVENT_DEFS`; a
  link-name field on the throw/catch in the Implement panel; accept this ADR and update the ROADMAP.

### Consequences

- **Positive:** the engine gains BPMN's off-page connector — the last common intermediate-event type —
  for **almost no code**: two identity types, a parse arm, and a compile-time name-match that emits an
  edge Atlas already knows how to run. No runtime, event, value type, or recovery path; a link cannot
  hang, incident, or recover differently from a sequence flow because by runtime it *is* one. It
  completes the intermediate throw/catch family (message, signal, timer, escalation, compensation,
  link) and unblocks any real-world diagram that uses a link to stay readable.
- **Negative / trade-offs accepted:** two new `BpmnType` values (dispatch-table growth) for nodes with
  no behavior of their own; a per-scope resolution pass in `connectScope`; the link name lives only at
  compile (no runtime record), so a mid-flight rename requires a redeploy like any structural change
  (expected).
- **Follow-ups / risks to watch:** (1) **A stray real flow** — a link throw drawn *with* an outgoing
  sequence flow, or a link catch *with* an incoming one — would fork/join rather than act as a pure
  goto; bpmn-js discourages it, but a raw XML deploy could express it. Phase 1 tolerates it (the token
  machinery handles multiple edges); a Problems-panel/validation warning is a possible follow-up.
  (2) **Cross-scope intent** — a modeler expecting a subprocess throw to reach a root catch gets an
  unmatched-throw deploy error; the message should name the scope so the fix is obvious. (3) **Link
  vs. a same-named message/signal** — the name space is separate (a link name is not a message name),
  which the parse keeps distinct by living on `linkEventDefinition`; no collision, but worth a test
  that a link "Retry" and a message "Retry" coexist.

## Pros and cons of the options

### Option 1 — synthetic sequence flow + `passThroughBehavior` (chosen)
- Good: reuses the edge compiler, the pass-through behavior, the identity-type pattern, and the scope
  check; resolved at compile (I5) and frozen into the graph (I6); no runtime, event, value type, or
  recovery path; the token visits both nodes so the Operations overlay and step replay work.
- Bad: two behaviorless `BpmnType` values; a resolution pass; the name is compile-only.

### Option 2 — a runtime link behavior with a name registry (rejected)
- Good: keeps the two nodes without a synthetic edge.
- Bad: invents a runtime lookup and two behaviors for a statically-fixed jump — the lookup's answer is
  known at deploy, so it belongs at deploy; more code and a dispatch path for zero behavioral gain.

### Option 3 — splice the link away at compile (rejected)
- Good: the smallest compiled graph (no link nodes at all).
- Bad: the token never visits the throw or catch, so the token-visit overlay (ADR-0022) and step
  replay (ADR-0046) lose the two events the modeler drew — the diagram shows symbols the trace never
  touches.

## Links

- builds directly on the sequence-flow compiler (`connectScope` / `b.Connect`), `passThroughBehavior`
  (the undefined-task pass-through), and `checkScopes` (ADR-0074, scope-consistent edges)
- reuses the identity-type-over-shared-behavior pattern from **ADR-0112** (`TypeSendTask` →
  `serviceTaskBehavior`)
- keeps the token-visit overlay (**ADR-0022**) and single-process step replay (**ADR-0046**) working
  by keeping both link events as visited pass-through nodes
- honors I1, I2, I4, I5, I6 and **ADR-0018** (test-first, recovery test up front)
- ROADMAP Milestone 1/2; the last common intermediate throw/catch event type, after message, signal,
  timer, escalation (**ADR-0125**), and compensation (**ADR-0103**)
