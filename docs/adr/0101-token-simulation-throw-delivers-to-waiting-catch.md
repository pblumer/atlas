# ADR-0101: Token simulation — a thrown message/signal delivers to a waiting catch

- **Status:** Accepted
- **Date:** 2026-08-06
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0096 chose a manual firing model for the Design-view token simulation: a catch event
parks its token and the user clicks a ⚡ to fire it, while a throw only *animates a dot* to
the events it names. ADR-0097 then made one exception — a message/signal reaching a **start
event** spawns a token, because a start has no waiting token to release.

In use, the remaining rule proved surprising. On a collaboration where pool A throws a
message that pool B's process later catches (a plain throw→catch correlation across pools),
the dot flew to the waiting catch and… nothing happened. The catch held its token, the
message visibly arrived, and yet the token did not move — the user had to notice the ⚡ and
click it. For the single most common message pattern — "when the other side finishes, this
side continues" — the simulation implied the message was ignored.

The question: should a modelled throw that reaches a catch which is *already waiting* fire
it, or keep pinging and require a manual click?

## Decision drivers

- **Teaching value / not misleading** — the dot arriving at a waiting catch that then does
  nothing reads as "messages don't work".
- **Keep the manual model where it teaches** — an event with no modelled throw (a timer, an
  external message, a catch reached before its message) should still be a deliberate act.
- **Engine-free** — no correlation-key evaluation; match by message/signal name only.

## Decision outcome

**A thrown message/signal now delivers to a waiting catch — it fires it.** When a throw's
dot reaches its target:

- a **catch event / receive task** that already holds a token → **fires** (the correlated
  message arrived and delivers; the token continues);
- a **boundary event** whose host activity holds a token → **fires** the boundary
  (interrupting/non-interrupting as modelled);
- a **start event** → spawns a token (ADR-0097, unchanged);
- **nothing waiting** there → only a ping (the message found no one home; there is no token
  to release).

This refines ADR-0096's "a throw never releases a catch". The manual model is preserved
exactly where it still teaches: a catch with no arriving throw — a timer, an external
message, or a catch reached *before* its message — still waits for a manual ⚡ or Auto-decide.
What changed is only that a *modelled* delivery to a *waiting* catch now completes, which is
what "throw and catch correlate" is supposed to mean. Matching remains by name; correlation
keys are still the engine's job.

### Consequences

- **Positive:** the most common message pattern — a throw in one pool completing a catch in
  another — now works end-to-end without hunting for the ⚡. Boundary message/signal events
  fire on delivery too.
- **Negative / trade-offs accepted:** delivery is name-matched, so a shared message name may
  fire more catches than the engine's correlation keys would; a message that arrives before
  its catch token still needs a manual fire (the simulation does not buffer messages).
- **Follow-ups / risks to watch:** if name-only delivery ever misleads on models that lean
  on correlation keys, prefer labelling the limitation over evaluating keys in the browser.

## Pros and cons of the options

### Deliver to a waiting catch (chosen)
- Good: throw→catch correlation completes; matches user expectation; boundary delivery too.
- Bad: name-only match can over-deliver on shared message names.

### Keep pinging, require a manual click
- Good: nothing changes; strictly "you fire every event".
- Bad: the dot arrives at a waiting catch and nothing happens — reads as broken.

## Links

- refines ADR-0096 (manual firing model) and ADR-0097 (message/signal reaching a start event)
- builds on ADR-0078; relates to ADR-0020 (message correlation) and ADR-0088 (signal events),
  whose shape it teaches without executing
