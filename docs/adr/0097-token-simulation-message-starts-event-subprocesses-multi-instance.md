# ADR-0097: Token simulation — message starts, event-subprocess triggers, and multi-instance

- **Status:** Accepted
- **Date:** 2026-08-06
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0096 extended the Design-view token simulation (ADR-0078) with event triggers, proper
inclusive-gateway semantics, and an auto-decide mode. It closed with two named follow-ups —
multi-instance and event-subprocess triggers — and made one deliberate simplification that
turned out to mislead in practice:

- **A thrown message did not start a process.** ADR-0096 ruled that a throw only *pings* the
  catches it names and never fires them, because a catch has a waiting token the user should
  release. But a **message/signal start event** has no waiting token — the message *is* what
  creates the instance. So on a collaboration where pool A throws `send_new_event` to pool
  B's message start event, the dot arrived and pool B just… sat there. The one thing the
  picture should teach — "this message starts that process" — didn't happen.
- **Event subprocesses (ADR-0082) were invisible.** An event-subprocess start event got the
  ordinary process-start spawn glyph, as if it were a normal entry point, with no notion of
  "fires while the enclosing scope runs" or interrupting vs. non-interrupting.
- **Multi-instance activities (ADR-0077) were invisible.** A parallel/sequential
  multi-instance task animated as a single pass-through, teaching nothing about the marker.

The question: how do we teach these three, staying inside the ADR-0078 guardrails
(client-side, engine-free, buildless, not a second execution engine)?

## Decision drivers

- **Teaching value first**, and specifically *not misleading* — the message-start gap was a
  case of the simulation implying the wrong thing.
- **Do not conflate with execution** — no correlation-key evaluation, no data-driven
  instance counts, no real scope model.
- **Consistency** with the affordance / animation / marker conventions already in the module.

## Decision outcome

**Message/signal reaching a start event spawns a token.** When a thrown message/signal dot
lands, the outcome now depends on the target: a **process start event** spawns a token
(begins a new instance); an **event-subprocess start** triggers its handler (below); a
**catch or boundary event** is still only pinged (a token must already be waiting, and the
user fires it). This refines — it does not overturn — ADR-0096's "a throw never releases a
catch": a start event is not a catch, it has no token to release, and the only alternative
was an unintuitive manual click on the start. Matching stays by message/signal name; the
simulation still does not evaluate correlation keys.

**Event-subprocess triggers.** An event-subprocess start event no longer shows the spawn
glyph; instead, while the process is running (any live token), it shows a *fire* affordance.
Firing it drops a token on the handler's start. An **interrupting** trigger first terminates
the scope — every other live token, join, and in-flight animation — because the handler
pre-empts the process; a **non-interrupting** trigger runs alongside. A matching thrown
message/signal fires it too, while the scope is live. The simulation is flat, so it treats
the scope as the whole process: a nested event subprocess reads as process-scoped. This is
noted as a limitation, consistent with the teaching-aid framing.

**Multi-instance.** A multi-instance activity holds its token and runs its body a fixed,
clearly-labelled number of times (a badge `‖ 3` / `≡ 3` counting down) before the token
moves on — parallel and sequential are distinguished by the marker and the label. The real
multiplicity is data-driven (a collection size), which the simulation deliberately does not
evaluate; the fixed count exists only to make the marker's meaning legible.

### Consequences

- **Positive:** the collaboration message-start story now reads correctly end-to-end; event
  subprocesses and multi-instance markers finally *do* something a newcomer can watch. It
  all reuses the existing fire-affordance, badge, and animation conventions, and still never
  touches the engine.
- **Negative / trade-offs accepted:** the multi-instance count is a fixed simulation
  constant, not the model's real collection size; an interrupting event subprocess is scoped
  to the whole process rather than to a nested subprocess; message starts spawn on name match
  without correlation-key evaluation (so a broadcast-looking message may start more instances
  than the engine would). These are teaching simplifications, labelled where they show.
- **Follow-ups / risks to watch:** if the fixed multi-instance count or the process-wide
  event-sub scope ever misleads, prefer labelling or narrowing over modelling real scopes and
  collections in the browser. A user-set instance count is a small, safe future increment.

## Pros and cons of the options

### Message start — spawn on arrival (chosen)
- Good: teaches the actual semantics ("this message starts that process"); removes the
  friction of a pointless manual click on a start event.
- Bad: without correlation-key evaluation it can start more instances than the engine would
  on a shared message name.

### Message start — keep pinging only
- Good: nothing new.
- Bad: actively misleading — the dot arrives and the process visibly fails to start.

### Multi-instance — fixed labelled count (chosen)
- Good: makes the marker legible with no data model; honest about being simulated.
- Bad: the number is arbitrary, not the model's real multiplicity.

### Multi-instance — evaluate the collection
- Good: real counts.
- Bad: requires FEEL/variable evaluation — the engine coupling ADR-0078 exists to avoid.

## Links

- extends ADR-0096 (token simulation — events, inclusive gateways, auto-decide) and ADR-0078
- teaches the shape of ADR-0082 (event subprocesses) and ADR-0077 (multi-instance) without
  executing them
- relates to ADR-0035 (message start events) and ADR-0088 (signal events)
