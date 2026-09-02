# ADR-0226: A start event is a trigger, and the one that fires is the one that starts

- **Status:** Proposed
- **Date:** 2026-09-02
- **Deciders:** Atlas maintainers

## Context and problem statement

A BPMN process may carry more than one start event, and they are alternatives: a message
start for *"a ticket arrived"*, a none start for *"somebody pressed Start"*, a timer start
for *"every night at two"*. Each is a trigger, and the one that happens is the one that
brings an instance into existence, at itself.

Atlas seeded a token at **every** root-scope start event, whatever created the instance.
[ADR-0035](0035-message-start-events.md) recorded that deliberately — a message start "is
in `StartEvents()` and behaves at runtime exactly like a none start" — and for every model
that existed then it was indistinguishable from the correct behaviour, because those models
had exactly one start event.

It stopped being indistinguishable the moment a process had two, and the way it surfaced
was not a wrong token count in a test. A user pointed a Jira event watch
([ADR-0214](0214-jira-inbound-issue-watch.md)) at a project and modelled, in one
definition, a message start for the arriving ticket beside a none start whose branch
*created* a Jira issue. Then:

1. A ticket arrives. The watch publishes `jira.ticket.created`.
2. Atlas creates an instance — and seeds **both** start events, because that is what it
   did.
3. The none-start branch runs and creates a new Jira issue.
4. The watch's JQL matches that issue. Back to 1.

Each run created the next run's trigger. The instances are visible in Operations as a
chain — the instance correlated to `PAT-13` holding `newTicket = PAT-14`, the one for
`PAT-14` holding `PAT-15` — and it stopped only when the watch was deleted by hand. No
error was raised anywhere, because from the engine's point of view nothing went wrong.

## Decision drivers

- **The semantics are not a matter of taste.** BPMN 2.0 is explicit that multiple start
  events are alternative triggers. Running the untriggered ones is not a lenient reading;
  it is a different process.
- **The failure is silent and unbounded.** Nothing about the loop looks like a defect from
  inside: every instance is well-formed, every task succeeds. The only signal is the
  count, and by then a Jira project is full of tickets.
- **ADR-0035's permissiveness has a reason.** A process whose only entry is a message start
  can still be created through the API "and then just flows on". That is a real
  convenience — it is how such a process is tested — and it should survive.
- **The creation path is load-bearing.** Every instance in the system is born here, and
  recovery replays the events it emits. A change here must not alter what is written.

## Considered options

1. **Leave it, document the trap.** Tell modellers to put a message-started process in its
   own definition.
2. **Refuse a definition with more than one root start event at deploy.**
3. **Seed the start event that fired**, carried on the creation command.

## Decision outcome

Chosen option: **"seed the start event that fired"**.

- The instance-creation command gains `StartElements []int32`: the root start events to
  seed. A trigger fills it with exactly the one that fired —
  `AppendCreateInstanceCommand` now *requires* it, so a fourth kind of trigger cannot
  quietly inherit the old behaviour by forgetting an optional argument.
- `nil` — the zero value, and therefore the value a command that says nothing carries —
  means an **untriggered** create: the API, a call activity. Those seed the process's
  **none** start events, which are what "start this by hand" means.
- A process with **no** none start event keeps ADR-0035's permissiveness and is seeded at
  every entry it has. Narrowing it to nothing would create an instance with no token at
  all: one that never ends and is waiting for nothing, which is a worse answer than the
  permissive one.
- The signal-start index now carries each start event's element id beside its definition
  key, the way the message-start index already did. That is the whole of the change to the
  three triggers; the timer already had the element on the timer record.

Nothing about the **events** changes — only which element-activating commands the handler
appends. Recovery replays those events, so an instance created before this change
reconstructs exactly as it did (invariant I4), and an instance created after it records
the same shapes.

### Consequences

- **Positive:** a message-started process no longer runs the branches nobody triggered.
  The specific loop above cannot form: the branch that created the next ticket is not
  seeded by the message.
- **Positive:** the argument is required, so the compiler carries the discipline. The
  defect was possible because "which start fired" was information the trigger had and
  threw away.
- **Negative / trade-offs accepted:** a model that *relied* on the old behaviour — a none
  start branch that also ran on every message — changes meaning. That is a behaviour
  change to running systems, and it is the point: the old meaning was not one anybody
  would author on purpose. It is called out in the changelog rather than hidden in a
  patch note.
- **Negative:** "seed the none starts" is a rule about start-event *kind*, so a process
  with two none start events still seeds both on an API create. That is the same
  ambiguity BPMN itself has for that shape, and there is nothing in a create request that
  would pick one; leaving it is deliberate.
- **Follow-ups / risks to watch:** an inbound watch whose query matches what its own
  process writes is still a loop a modeller can build — with one process writing and
  another watching, this change does not prevent it. A guard belongs to the watch (a rate
  ceiling, or refusing a query that matches issues Atlas itself created), not to
  instantiation.

## Pros and cons of the options

### Option 1: document the trap
- Good: no engine change, no behaviour change to any running system.
- Bad: the documentation would have to say "Atlas runs start events that did not fire",
  which is not a rule anybody can hold in their head — and the cost of forgetting it is an
  unbounded loop against somebody else's system.

### Option 2: refuse multiple root start events at deploy
- Good: makes the ambiguity impossible rather than resolving it.
- Bad: refuses models BPMN allows and that are perfectly sensible — a process reachable
  both by hand and by a nightly timer is a normal thing to draw. It would also break
  deployed models on upgrade, which is a far bigger blast radius than fixing the
  semantics.

### Option 3: seed what fired (chosen)
- Good: the BPMN semantics, and the smallest change that has them — one field on a
  transient command, three call sites, one handler.
- Bad: changes behaviour for existing models with more than one start event. Accepted,
  because for those models the old behaviour was the bug.

## Links

- amends [ADR-0035](0035-message-start-events.md) (which recorded the seed-everything
  behaviour as "exactly like a none start")
- surfaced by [ADR-0214](0214-jira-inbound-issue-watch.md) (the Jira watch that published
  the message)
- relates to [ADR-0088](0088-signal-events.md) (signal starts, whose index now carries the
  element id too)
