# ADR-0416: The shop shows an order's open tasks, and the order records the instances that work it

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-24
- **Deciders:** Atlas maintainers
- **Open question:** task names are written by modellers for the people who do the work ("Prozess fehlt, bitte modellieren"); whether an orderer should read them as they are, or a model should give a task a separate name for the person waiting on it, is not settled
- **Question checked:** 2026-09

## Context and problem statement

An order's row in the shop said, per position, where it stood — "Wartet", "Provisioniert" —
and not on whom. A position waiting for a line manager's approval, for a group in IT, or
for a process nobody has modelled yet reads the same. Asked for by the people the page is
for: under each position, the open tasks of the processes working it, whom each waits
for, and — for whoever holds one — a way to answer it there.

Two things stood in the way.

**Nothing linked a position to the instances working it.** The fulfilment orchestration
starts each position's process (its approval, or its provisioning) through
`POST /api/v1/instances` with `orderId` and `positionId` among the variables; the
approval model starts the provisioning the same way; the return route starts a
deprovisioning. The only way back from a position to those instances was a search of
every running instance's variables — the walk [ADR-0390](0390-position-progress.md)'s
progress route makes, and the one the portal's process link made until it hung on an
installation with 50 000 live instances. And the start route could not have recorded the
link if it had wanted to: the instance key is minted while the command is processed, and
the route answered with the definition's key alone.

**The server does not know who somebody's line manager is.** That is a directory
question, answered by a model through a worker ([`api/escalation.go`](../../api/escalation.go));
the server holds no directory credential and asks no directory. But "the line manager
should see it" was one of the three audiences asked for.

## Decision drivers

- The cost of reading an order's tasks must be the order, not the server.
- The link from a position to its instances must be certain, not inferred from variables
  two unrelated processes could both carry.
- Nobody sees an order they have no part in; nobody answers a task that is not theirs.
- No second path from the server to a directory.

## Considered options

1. **Walk the open tasks per request** and keep those whose instance carries the order's
   id. No schema change; cost grows with every open task on the server, bounded by the
   folder scan budget — past it, tasks go missing without anybody knowing.
2. **Record the instances on the order when they are started**, and read their tasks
   through each instance's own element index. Needs the start to learn the key it
   created.
3. **Ask the directory for line managers** so every manager sees their reports' orders.

## Decision outcome

Chosen option: **2**, with the line manager reached through the task they hold rather
than through option 3.

- **The engine reports the key it minted.** `Command.Created` is an optional pointer the
  processor writes the new instance's key into while processing the creation
  (`Processor.CreateInstanceReporting`). It rides only on the creation intent, allocates
  nothing (invariant I1), is read by the caller only after the batch committed (I2), and
  is classified *external* for the continuation codec — commands are never replayed (I6).
- **Every API start answers with `instanceKey`**, and when its variables carry a string
  `orderId` and `positionId` naming an order this server holds, the instance is appended
  to that position's `instances` (key, process id, start time). The return route does the
  same. A start whose order cannot be noted still succeeds — the instance is running, and
  a failure would make the caller retry and start the work twice — and logs
  `order.instance_unrecorded`.
- **`GET /api/v1/shop/tasks`** answers every open task of the caller's orders (as orderer
  or recipient), read from the recorded instances, and of the orders in which the caller
  **holds** an addressed task. The second set is how the line manager who approves is
  reached: the server cannot ask whose manager somebody is, but it knows who holds a
  task. Finding those orders walks the open tasks, bounded like the approval list, and
  says `truncated` when the bound bit; the caller's own orders never depend on that walk.
  A held order is returned without the answers given on the products' forms.
- **Whom a task waits for** is named as the reader knows it: for an approval, by the rule
  the line is approved under (`fixed`, `role`, `superior`); otherwise `person`, `group`
  or `open`, with display names resolved on the server.
- **Answering is the task route's.** Whoever holds the task — or an operator — opens its
  own form in the row, prefilled from the task's variables under
  [ADR-0275](0275-instance-visibility.md)'s field allowlist, and completes it through
  `POST /api/v1/tasks/{key}/complete`. Any task, not only approvals. One exception is
  narrower than that route: a task the model addressed to nobody is anybody's by BPMN
  convention, and the route keeps it so, but the shop does not offer its form to
  somebody who does not operate — the reader of an order's row is first of all the
  person who ordered, and a manual provisioning step of their own order with a button
  beside it invites them to report their own laptop handed over.

## Consequences

- [ADR-0408](0408-portal-shows-no-process-links-per-position.md) removed per-position links
  into a process because the status beside the position already answered the question
  they asked. This record puts something back on the position row, and the difference is
  the question: not *where the process is* but *whom the position waits for*, which the
  status does not say. It is text and a form, not a link into an operations surface.
- Orders placed before this landed carry no recorded instances and show no tasks. They are
  not backfilled; on the installation this was asked for they were demonstration data and
  are removed.
- A task inside an instance started by a **call activity** of a recorded instance is not
  found: only instances started through the API are recorded, and the element index of
  one instance does not reach into another's.
- A line manager who holds no task of an order does not see it. That is the price of not
  giving the server a directory, and the reason it is named here.
- `createInstanceResp` gains `instanceKey`, which every caller of the start routes can
  now use — the order-to-cash demo's embedded client included.
- Withdrawing a position — the whole order, or one line — cancels every still-running
  instance recorded on it, not only its approval. A pending line may have its
  provisioning started already, and a step of it left in somebody's inbox is work for a
  position that no longer exists. Cancelling stops the work; what the process already
  did in a target system is not compensated. Orders placed before instances were
  recorded fall back to finding the approval by walking the open tasks, as before.
