# ADR-0558: An order line whose instance an operator ended settles as a failed report

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-11
- **Deciders:** Atlas maintainers

## Context and problem statement

An order line moves only when the process working it reports: the provisioning's
outcome (`POST /api/v1/orders/{id}/lines/{item}`), the approver's decision, the
return's outcome. Since [ADR-0416](0416-the-shop-shows-an-orders-open-tasks.md) the
order records on each line the instances started for it, with the operation each
performs ([ADR-0425](0425-a-product-lifecycle-is-one-process-with-message-triggers.md)).

An operator can end any of those instances from outside the order:
`DELETE /api/v1/instances/{key}`, the bulk terminate (`POST /api/v1/instances/terminate`,
by keys or by filter), a definition's drain (`POST /api/v1/processes/{key}/cancel-instances`),
deleting a process with its instances, and the MCP tools that call them. Doing so ends
the one thing that was going to report. Observed live: three provisioning instances
were cancelled this way, their lines said `running` for nine days, the fulfilment
orchestration waited at `Warten` for an `atlas.order.advanced` nobody was left to
publish, nothing warned, and the fulfilment repair counted the orchestration healthy.

The shop's own withdrawal (`ordercancel.go`) is not affected: it settles the line
`cancelled` first and stops its work after.

## Decision drivers

- A line must not wait on an instance that can no longer report.
- `applyToState` stays deterministic and touches engine state only (I4); the order is
  a design-time store, and settling it publishes a message.
- Durable before visible (I2): the line is settled only once the end is on disk.
- No status may be reached by a second, authorless route: rejected and cancelled are
  decisions with an author ([Line.Valid]).
- A crash between ending the instance and settling the line must not leave the orphan
  for ever.

## Considered options

1. **Settle in the cancel paths, after their drive** — the handlers already know the
   keys they cancelled; read each one's `orderId` off the loop, and settle the lines
   whose recorded working instance is that key.
2. **An engine hook on `IntentTerminated`** — a post-commit callback from the processor
   for every terminated root instance.
3. **A startup / periodic sweep** — compare every open line's recorded instance with
   the active instances.

## Decision outcome

Chosen option: **1, with the fulfilment repair as the sweep for what it misses**.

- **Where.** Every operator cancel path hands the keys it cancelled to
  `Server.settleLinesEndedBy` once `drive` has returned: single delete, terminate by
  keys (the keys that were active), terminate by filter, the drain, and the
  force-delete of a process with running instances. It reads, off the run loop, each
  key's state (it must no longer be active) and its `orderId` variable — the variable
  the start recorded the instance from — and asks the order service to settle.
- **Which line.** `order.Line.WaitsOn` names the instance a line is waiting to hear
  from: a `running` line its newest provisioning (or an instance recorded with no
  operation, as starters before ADR-0425 did), a `returning` line its newest
  deprovisioning, a `pending` line its approval while nobody approved and no
  provisioning started. Only a line waiting on exactly the ended key is settled, so a
  line that already reported, one a newer attempt took over, and one the shop withdrew
  are left alone, and settling twice changes nothing.
- **To what.** Ended provisioning → `failed`: nothing was delivered as far as anybody
  knows, and failed is not terminal, so the order stays open until somebody abandons
  the line or reports it done by hand. Ended return → `returnFailed`: the recipient
  still holds the thing, and asking for the return again is the ordinary repair.
  Ended approval → `failed`, deliberately not `rejected` (an approver's refusal, with
  author and reason) nor `cancelled` (the orderer's withdrawal): an operator stopping a
  process is neither, and filed as a rejection it would tell the orderer somebody
  refused them.
- **What else.** Exactly what `HandleReport` does for a failed report: save the order,
  then write the failed outcome (ADR-0429 §3), then publish `atlas.order.advanced`.
  The order line has no place for a failure's reason, so the outcome's `result` carries
  it: why, the instance key, and the state it ended in. The log says it too
  (`order.line_instance_ended`, `order.line_unsettled`).
- **Recovery.** `POST /api/v1/orders/fulfilment/repair` lists, under `orphanedLines`,
  every open line waiting on an instance that is no longer active — terminated,
  completed without reporting, or gone — and does not count the orchestration of such
  an order healthy. Without `dryRun` it settles them the same way, before it restarts
  anything. A cancel interrupted before the settlement is found and repaired there.

Option 2 was rejected because a processor callback for every terminated instance puts
an order-store read behind an engine event — the engine knows nothing of orders — and
fires as well for the instances the shop's own withdrawal stops. Option 3 was rejected
as an automatic act for the reason the repair route already gives: settling lines and
waking orchestrations has consequences outside Atlas, and an operator should be able to
look first.

### Consequences

- **Positive:** a line no longer waits on an instance an operator ended, through any of
  the operator's cancel paths; the orchestration moves on; the failure says why; the
  repair finds the orphans that a crash, or a process that ends without reporting,
  leaves behind.
- **Negative / trade-offs accepted:** an instance ended by another route — a parent's
  cancellation tearing down a call activity, a migration fork, a catalogue purge — is
  not settled live; the repair finds it. A failed line has no restart: the operator
  abandons it or reports it by hand, as for any failed line today.
- **Follow-ups / risks to watch:** a per-position lifecycle's strand cancelled after its
  line is `done` leaves later operations nowhere to be delivered; that is not a line
  waiting on anything and is out of scope here. An action asked of a held line
  (ADR-0429) whose instance is cancelled reports no outcome; the line's status does not
  wait on it, so nothing stalls, but the action stays open.

## Links

- relates to [ADR-0416](0416-the-shop-shows-an-orders-open-tasks.md) (instances recorded on the line)
- relates to [ADR-0425](0425-a-product-lifecycle-is-one-process-with-message-triggers.md) (operations)
- relates to [ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md) (§3, outcomes of the order's own acts)
