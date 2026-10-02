# ADR-DRAFT: Token simulation — a message travels along the drawn message flow

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-02
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0097 and ADR-0101 made messages *do* something in the Design-view token simulation
(ADR-0078): a thrown message reaching a message start event begins that pool's process, and
one reaching a catch that is already waiting fires it. Both rules find the receiver the same
way: by **message name**. The sender must name a message on a `MessageEventDefinition`, and
the receiver must name the same one.

In use, collaborations still stalled at the pool boundary, and the reader had to start or
fire the other pool by hand. Three model shapes fall through the name match:

- **A descriptive collaboration.** The common way to draw "who talks to whom" is a plain
  task in one pool with a **message flow** to an event or a pool on the other side. The
  sender is a task, not a throw event, and often neither end names a message. The message
  flow is the only statement in the model that a message goes from here to there, and the
  simulation ignored it. A non-executable invoice collaboration (Mail Room, Team Assistance,
  Approver, Managing director) showed the effect: after "Request invoice approval" the
  approver's pool never started.
- **Send and receive tasks.** A message-kind send task (ADR-0112) and a receive task
  (ADR-0102) hold `messageRef` on the task itself, and a send task may name the message
  through an `operationRef` whose `inMessageRef` is the message. The simulation read the name
  only from event definitions, so these tasks never sent and never received, although the
  compiler and the Modeler's message picker (`messageRefHolder`) already treat them as
  message senders and receivers.
- **A catch behind an event-based gateway.** Such a catch never holds a token of its own.
  The token waits on the gateway, which races its events (ADR-0110). A message delivered to
  the catch therefore found "nothing waiting" and was only pinged, and the race could only
  be decided by a click.

The question: how should the simulation find where a message goes, so that a collaboration
plays through without a manual fire, without teaching anything the engine contradicts?

## Decision drivers

- **Not misleading.** A dot that lands and changes nothing reads as "messages do not work",
  which is the failure ADR-0097 and ADR-0101 set out to remove.
- **The diagram is the input.** The simulation exists for the Design view, where descriptive
  collaborations are drawn and the drawn message flow is the author's statement of intent.
- **The engine is not contradicted where it has an answer.** The engine correlates on
  message names (ADR-0020); message flows compile to nothing (ADR-0023). Where both ends of
  a flow name a message, the names are the executable link, and the simulation must not
  deliver what the engine would not.
- **Engine-free and small.** No correlation keys, no buffering, no second execution engine
  in the browser (ADR-0078).

## Considered options

1. **Deliver along drawn message flows, in addition to the name match**, unless both ends
   name different messages.
2. **Keep matching by name only** and document that descriptive collaborations need a
   manual start or fire.
3. **Deliver along message flows only**, and drop the name match.

## Decision outcome

Chosen option: **"Deliver along drawn message flows, in addition to the name match"**,
because it makes the most common collaboration shape play through and leaves every rule
ADR-0097 and ADR-0101 established in force.

- **When a message goes out.** An element sends when its token leaves it if it is a throw
  (as before) **or** a message flow is drawn out of it. A plain task sends as it completes,
  an end event as the token arrives, an expanded subprocess as it completes. An activity
  left through an interrupting boundary event did not complete and sends nothing.
- **Where it goes.** A dot flies along every message flow drawn out of the sender, following
  the flow's own waypoints, and, as before, straight to every catch-like element that names
  the same message or signal. A target reached both ways receives the message once.
- **What it does on arrival.** Unchanged from ADR-0097 and ADR-0101: a start event begins a
  new instance, an event-subprocess start fires while its scope runs, a boundary event fires
  while its host holds a token, a waiting catch or receive task fires. A black-box pool or a
  plain task has nothing to release and is pinged.
- **Names still decide where both ends have one.** A flow between two ends that name
  *different* messages does not deliver: the dot arrives and pings, and the receiver keeps
  waiting. That is what the engine would do, and showing it surfaces the modelling error.
- **Send and receive tasks read their message where it is modelled**: the task's own
  `messageRef`, or for a send task the `inMessageRef` of its `operationRef`, matching the
  compiler (`resolveSendTaskOperations`) and `messageRefHolder` in the Modeler.
- **A delivered message decides an event-based race.** When the catch it reaches sits behind
  an event-based gateway that holds a token, the token takes that catch's branch, as a click
  on that flow would. This is ADR-0101's "deliver to what is waiting" applied to the one
  place a waiting token does not sit on the catch itself.

Not changed: a message that arrives before its receiver waits is still only pinged and not
buffered. The engine does not buffer either (ADR-0370 is not started), so this race is real
and the simulation keeps showing it.

### Consequences

- **Positive:** A descriptive collaboration plays end to end: the approver's pool starts when
  the approval is requested, and the clarification pool starts when the approver asks for one.
  Send and receive tasks work across pools like throw and catch events. A message decides the
  event-based race it is modelled to decide. The dot follows the drawn arrow, so the reader
  sees which flow carried the message.
- **Negative / trade-offs accepted:** On a flow where one end names no message, the
  simulation delivers what the engine would not, because nothing executable links the two
  ends. That is the descriptive case this record is for. An executable model that relies on a
  drawn flow instead of a named message will play through in the simulation and stall in the
  engine. The Playground (ADR-0215) runs the real engine and is where that shows. A model whose
  pools message each other in a cycle now keeps running until it is reset, as it would by name.
- **Follow-ups / risks to watch:** If executable models are misread because a flow delivers
  where the engine would not, prefer marking such a flow in the simulation (for example, a
  distinct dot for "delivered by the drawing, not by a name") over dropping flow delivery.

## Pros and cons of the options

### Option 1 — flows in addition to names (chosen)
- Good: descriptive collaborations play through, and named throw→catch delivery keeps
  working exactly as before, including between pools with no flow drawn.
- Good: the mismatch rule keeps the simulation from hiding a name error the engine would hit.
- Bad: a flow with an unnamed end delivers where the engine would not.

### Option 2 — names only
- Good: nothing changes; the simulation never delivers what the engine would not.
- Bad: the most common way to draw a collaboration does not play, and the reader must start
  every receiving pool by hand. This is the failure reported.

### Option 3 — flows only
- Good: one rule, read straight off the diagram.
- Bad: drops the name match ADR-0101 relies on. Two pools that correlate by name with no
  flow drawn, which is valid and executable, would stop working.

## Links

- refines ADR-0097 (a message reaching a start event) and ADR-0101 (a message delivers to a
  waiting catch)
- builds on ADR-0078 (Design-view token simulation)
- relates to ADR-0023 (message flows compile to nothing; names are the executable link),
  ADR-0102 (receive tasks), ADR-0110 (event-based gateways), ADR-0112 (send tasks),
  ADR-0370 (message buffer, not started) and ADR-0215 (Playground)
