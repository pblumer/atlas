# ADR-0095: Token simulation — event triggers, inclusive gateways, and an auto-decide mode

- **Status:** Accepted
- **Date:** 2026-08-06
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0078 introduced the Design-view token simulation: a client-side, engine-free
walkthrough of a diagram's control flow, so newcomers can *see* how tokens fork, join, and
choose a branch without deploying anything. It shipped deliberately narrow, and named its
own gaps as follow-ups:

- **Messages, timers, and signals were ignored.** A token reaching a message/timer catch,
  a receive task, an event-based gateway, or a boundary event either sat there or slid
  silently through. On a model like a customer-onboarding or order-fulfilment flow — where
  *"wait for the payment message"* is the whole point — the simulation had nothing to show.
- **The inclusive gateway was faked.** A diverging inclusive (OR) gateway was treated as a
  single either/or choice, and a converging one waited for *all* incoming flows like a
  parallel join. That is neither what an OR-split nor an OR-join does, and it deadlocks the
  moment the split activates only some branches.
- **Play always stopped at every gateway.** Each exclusive choice required a click, so a
  model could never simply be watched from start to end.

The question: how far do we extend the walkthrough to cover events and proper inclusive
semantics, while staying inside the ADR-0078 guardrails — buildless, self-contained, and
emphatically *not* a second execution engine in the browser?

## Decision drivers

- **Teaching value first.** The point is comprehension: the "which way?" of a gateway, the
  "now the message arrives" of a catch, the fork/merge of an inclusive split.
- **Do not conflate with execution** (ADR-0078). No FEEL evaluation, no correlation-key
  matching, no deploy, no server round-trip. The engine stays the single source of truth
  for how a process actually runs.
- **Buildless and self-contained** (ADR-0012/0013). Plain JS in the existing
  `atlasTokenSimulation` bpmn-js module, embedded by `go:embed`. No npm, no CDN.
- **Consistency with what's there.** Reuse the token-dot animation, the overlay/marker
  conventions, and the existing "the user drives the decisions" interaction model.

## Considered options

**For events (message / timer / signal / receive / boundary / event-based gateway):**

1. **Manual "fire" affordance.** A catch parks the token and shows a *fire* glyph; the user
   clicks it to say "this event occurred now". A throw passes the token through and
   animates a message/signal dot to the catches it would wake — a visual cue only.
2. **Auto-correlation.** A throw automatically releases the matching catch by message name
   / correlation key.
3. **Keep ignoring events** (status quo).

**For the inclusive gateway:**

A. **Proper subset split + quiescence OR-join.** Diverging: the user arms one *or more*
   branches. Converging: the join fires once no still-active token can reach it, merging
   whatever arrived.
B. **Keep the single-choice / wait-for-all simplification.**

## Decision outcome

Chosen: **manual fire affordances (option 1)** for events and the **subset split +
quiescence OR-join (option A)** for inclusive gateways, plus an opt-in **auto-decide** mode.

**Events.** A catch — a message/timer/signal/conditional intermediate catch event or a
receive task — parks its token and grows a ⚡/✉/⏳ *fire* affordance; clicking it (or the
element) releases the token, the teaching moment being *"the event happens now, and here is
where the token goes."* A throw — a send task, a message/signal intermediate throw, or a
message/signal end event — passes the token straight through and animates a warm dot from
the throw to every catch-like element that names the same **message** (1:1) or **signal**
(broadcast to all). The dot is a visual link only: it never fires the catch. Matching is by
name alone; correlation keys are the engine's job, not a teaching aid's. An **event-based
gateway** reuses the existing choice mechanic (pick which event fires first); the catch
immediately downstream of it does not park again, since the gateway's choice already
represented the event. **Boundary events** on an activity holding a token get their own fire
affordance — interrupting cancels the activity and routes out the boundary; non-interrupting
spawns a parallel token and leaves the activity running.

**Inclusive gateway.** A diverging inclusive gateway offers its branches as a *multi-select*:
click flows to arm a subset, click the gateway to confirm, and it forks onto exactly that
subset. A converging inclusive gateway uses a **quiescence OR-join** — it fires once at
least one token has arrived and no still-active token (resting, in-flight, or parked in
another join) can reach it over the sequence-flow graph, merging the arrivals into one
token. Reachability is a memoised graph walk, recomputed only on re-import. This is the
standard OR-join teaching model and the reason a subset split converges cleanly instead of
deadlocking.

**Auto-decide.** An off-by-default toggle. While playing, it resolves choices (the modelled
default flow if present, otherwise every branch for an inclusive gateway or the first branch
otherwise) and fires parked catch events on their own, so Play runs a model end-to-end
without clicking. Off, every choice and every event is a deliberate click — which remains
the lesson.

### Consequences

- **Positive:** the simulation now covers the elements newcomers most often ask about —
  messages, timers, signals, boundary and event-based gateways — and models the inclusive
  gateway correctly, including subset activation. Auto-decide makes a model watchable in one
  gesture. It all rides the existing module, animation, and overlay conventions, so it stays
  small and consistent, and never touches the engine.
- **Negative / trade-offs accepted:** it is still an *approximation of control flow*. It
  does not evaluate conditions or correlation keys (a throw pings every same-named catch, not
  the one true correlated instance), does not honour real timer durations (a timer is fired
  by a click or by auto-decide, not by the clock), and does not model multi-instance markers
  or message/timer *start* correlation. The quiescence OR-join is a reachability heuristic:
  it can over-wait on models with unbounded loops feeding a join. These are teaching
  simplifications, deliberately not execution semantics.
- **Follow-ups / risks to watch:** as with ADR-0078, if the gap between the simulation and
  real execution ever misleads, prefer narrowing the simulation (or labelling the limitation
  in the UI) over creeping toward a browser engine. Multi-instance and event-subprocess
  (ADR-0082) triggers are the natural next increments within the same module.

## Pros and cons of the options

### Events — option 1, manual fire (chosen)
- Good: engine-free and deterministic; consistent with the existing user-drives-decisions
  model; the fire click is itself the lesson ("the event occurs *here*").
- Bad: on a throw→catch pair the user clicks twice (throw passes, then fires the catch);
  auto-decide exists precisely to skip that when watching.

### Events — option 2, auto-correlation
- Good: fewer clicks; feels closer to real execution.
- Bad: pulls correlation-key / message-matching logic into the browser — the coupling and
  "looks like the engine" risk ADR-0078 exists to avoid.

### Inclusive — option A, subset split + quiescence join (chosen)
- Good: correct OR semantics, including partial activation; teaches the one gateway people
  most often get wrong.
- Bad: the join relies on a reachability heuristic rather than true dead-path analysis.

### Inclusive — option B, keep the simplification
- Good: no new code.
- Bad: actively wrong — deadlocks a subset split, and mis-teaches both the split and the
  join.

## Links

- extends ADR-0078 (design-view token simulation)
- builds on ADR-0012 / ADR-0013 (embedded, buildless, self-contained modeler)
- reuses the token-animation technique from ADR-0038 (collaboration replay)
- teaches the shape of features specified by ADR-0088 (signal events) and the message /
  timer / boundary / event-based-gateway machinery, without executing them
