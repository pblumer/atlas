# ADR-0225: An inbound watch has an hourly budget

- **Status:** Proposed
- **Date:** 2026-09-02
- **Deciders:** Atlas maintainers

## Context and problem statement

An inbound watch reads a foreign system and publishes what it finds as Atlas messages
([ADR-0075](0075-clio-inbound-event-bridge.md), [ADR-0214](0214-jira-inbound-issue-watch.md)).
Every published event can start a process, and that process can write back to the very
system the watch reads.

When it writes something the watch's own query matches, the loop closes. A Jira watch
published `jira.ticket.created`, the started instance created a Jira issue, the watch
matched that issue, and the next instance created the next one. It ran until the watch
was deleted by hand.

The immediate cause of that particular loop was an engine defect — a message-started
instance also ran the process's none-start branch, which is what created the issue — and
it is fixed in [ADR-0226](0226-start-events-are-triggers.md).
But the shape is more general than the defect. Two processes can build the same loop
between them with no single model being wrong: one watches, the other writes, and neither
knows about the other. One process can build it alone by design, if its own work happens
to land where its watch is looking.

What makes it dangerous is that nothing looks broken. Every instance is well-formed,
every task succeeds, every message is delivered exactly once. There is no failing
component to raise an incident, and no state anywhere that says "this is the fourth
hundred instance the same watch started this morning". The only thing that distinguishes
a loop from a busy morning is the **rate**.

## Decision drivers

- **The failure is silent and unbounded.** Left alone it fills somebody else's system
  with tickets and this one with instances, and the first signal is a person noticing.
- **A guard that needs configuring protects nobody.** The watches that will loop are the
  ones nobody thought about; the default has to be the protection.
- **A false trip must be cheap.** A busy project that legitimately exceeds the ceiling
  must lose nothing it cannot get back, and an operator must be able to see immediately
  what happened and what to change.
- **It cannot live in the model.** No process can see how often *it* has been started, and
  the loop can span two of them.

## Considered options

1. **Nothing in the product** — document the hazard and rely on the engine fix.
2. **Exclude what Atlas wrote**: tag issues the connector creates and have the watch's
   query skip them.
3. **An hourly budget per watch**, which switches the watch off when crossed.

## Decision outcome

Chosen option: **"an hourly budget per watch"**.

- Each watch carries `maxPerHour` — 60 when it names none, one a minute sustained. The
  count and the window start are stored on the watch itself, so a restart does not hand a
  looping watch a fresh budget.
- A batch that would cross the ceiling is **refused whole**, the watch is switched off,
  and `disabledReason` records why in words an operator can act on. Publishing up to the
  line would leave a batch half-delivered for no gain, and a runaway that is merely
  throttled is still a runaway.
- The resume cursor does not advance for a refused batch, so nothing is lost: enabling
  the watch again re-reads it. Enabling also clears the reason and starts a fresh window,
  because a watch that resumed into its old count would trip on the next event.
- The charge happens in the **same run-loop hop as the publish**, so a watch cannot be
  read as inside its budget and then publish outside it.

### Consequences

- **Positive:** the class is closed, not just the instance of it that was reported —
  including the loop two processes build between them, where no single model is wrong.
- **Positive:** the ceiling is a number an operator can raise per watch, and the reason
  text says so. The failure mode is a watch that stopped and explains itself.
- **Negative / trade-offs accepted:** a genuine burst — a bulk import into a watched
  project — trips the guard, and someone has to notice and re-enable it. That is the
  intended trade: 60 unwanted instances and a stopped watch is a much better morning than
  600 and a still-running one. It is also the same event the forward-only default
  (ADR-0075) already exists to blunt.
- **Negative:** the ceiling is per watch, not per connector or per server, so ten watches
  can still publish ten times the default between them. Per-watch is where the *cause*
  lives — a loop is a property of one query and what it matches — and a server-wide cap
  would make one busy watch switch off an unrelated one.
- **Follow-ups / risks to watch:** nothing notifies anybody when a watch trips; it is
  visible in the Console and nowhere else. Wiring it to whatever this installation uses
  for alerting is the obvious next step, and deliberately not invented here.

## Pros and cons of the options

### Option 1: document it
- Good: nothing to build, nothing to trip falsely.
- Bad: the hazard is invisible from inside the model, so documentation asks a modeller to
  hold in their head something the product could simply notice.

### Option 2: exclude what Atlas wrote
- Good: precise — it removes exactly the events that closed the loop.
- Bad: only works for the connector that has the tagging, only for the direction it knows
  about, and not at all for a loop that runs through a second process or a third system.
  It also puts a rule in every operator's query that they must not delete.

### Option 3: an hourly budget (chosen)
- Good: cause-agnostic. It does not need to know why the rate is what it is, which is
  what makes it work for the loops nobody predicted.
- Bad: a blunt instrument that can fire on legitimate load. Accepted, because the cost of
  a false trip is one Console click and the cost of a miss is unbounded.

## Links

- guards [ADR-0075](0075-clio-inbound-event-bridge.md) and
  [ADR-0214](0214-jira-inbound-issue-watch.md) (the bridge and the Jira watch)
- follows [ADR-0226](0226-start-events-are-triggers.md) (the
  engine defect that made the reported loop possible)
