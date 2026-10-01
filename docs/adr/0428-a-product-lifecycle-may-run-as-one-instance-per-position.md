# ADR-0428: A product lifecycle may run as one instance per position, and its later operations are delivered to that instance

- **Status:** Accepted (amended 2026-10-01 — an operator's or a system's action may be asked through MCP; see "Landed in a second step")
- **Implementation:** Landed
- **Date:** 2026-09-29
- **Deciders:** Atlas maintainers
- **Open question:** Whether ADR-0162 migration can move an instance that waits at an
  event-based gateway or a message catch event with an open subscription, and whether
  thousands of instances waiting for years change anything the engine, the store or the
  operator views assume. Neither is measured; the decision below does not depend on the
  answer, but the rollout beyond a pilot does.
- **Question checked:** 2026-09

## Context and problem statement

ADR-0425 lets a product bind **one** lifecycle process in place of a provisioning and a
deprovisioning process. Each operation is a **message start event** in that process, and
each operation **starts a new instance** through the directed trigger. The pilot on the
demo installation (product `laptop-huelle-14`, process `proc_laptop_huelle_lebenszyklus`)
confirmed it: one instance for the issue, a second, unrelated instance for the return.

Maintainers asked for a different shape: **one strand per position**, running from the
order to the return —

```
new → provisioning → (issued) → wait ─┬─ change requested → change ─┐
                                       │         ↑___________________┘ (back to wait)
                                       └─ return requested → deprovisioning → end
```

— so that one instance is the record of one right: who issued it, in which state, every
change, and the return, with the data captured at issue (a serial number, a handover
mode, an assigned identifier) still at hand when the right is revoked.

BPMN expresses this directly, and Atlas executes every element it needs (event-based
gateway, message catch events, interrupting boundary events). What Atlas cannot do is
**reach** such an instance. Four facts found in the code decide the design:

1. **The catalogue can only address start events.** `catalog.LifecycleProblems` accepts
   an operation only if its message names a root message start event of the newest
   version (`EntryPoints`), and every consumer of a binding — the start act, the return,
   recertification, reconciliation — goes through `startBinding`, which creates an
   instance. Nothing delivers to an instance that is already running.
2. **The name-correlated publish fails silently.** `POST /api/v1/messages` answers 200
   when nothing waits (ADR-0425 §Context, reason 2), and the buffer that would hold such a
   message is decided but not built (ADR-0370, Implementation: Not started). A return sent
   that way to an instance that is momentarily not waiting is lost without a word.
3. **The line already knows its instance.** Every order line records the instances
   started for it, with the operation each performs (`order.LineInstance`, ADR-0416,
   ADR-0425). And a position is unambiguous within its order: its key is the item id plus
   the variant (ADR-0384), so `orderId + "/" + positionKey` names exactly one right.
4. **Not every held right has an instance.** Rights found by a commissioning load or a
   reconciliation (ADR-0334) never had one; rights issued before a product was converted
   have one of the old shape; an operator may have cancelled one. A return must still be
   possible for all of them.

A long-running instance also changes what "running" means, and three existing mechanisms
read it:

- **Versions.** A running instance stays on the version it started on (ADR-0019,
  ADR-0162). Under ADR-0425 a return always starts the **newest** version of the
  lifecycle process; under this shape it runs the version the right was **issued** on.
- **Expiry.** ADR-0344 already ends time-bounded rights: a modelled expiry process
  **returns the order line**, deliberately through the one existing path and not
  through a second removal path. A timer inside the lifecycle process would be that
  second path.
- **Progress.** Position progress (ADR-0390) and the open-work views read an active
  instance as work under way. Under this shape a held right is an active instance that
  waits, possibly for years.

## Decision drivers

- **One record per right.** The reason for the request; a shape that splits the strand
  again for the common case does not meet it.
- **Nothing is lost silently.** ADR-0425 removed the silent outcomes of starting; the
  same standard applies to delivering. A return that reaches nobody must say so.
- **The inventory stays the single truth** (ADR-0312). A return is still a return of the
  order line; the process performs it, it does not replace it.
- **Every held right stays returnable**, including rights that have no instance of this
  shape.
- **No loop runs without an external cause**, and no external cause can drive one without
  bound.
- **ADR-0425 stays valid.** Products that gain nothing from a long-running instance keep
  the simpler shape; this is an additional form, not a replacement.
- **The invariants hold** — every new fact is an event folded by the one `applyToState`
  (I4, I6), nothing is answered before it is durable (I2), and resolution happens at the
  boundary and at publish, never on the hot path (I1, I5).

## Considered options

1. **Keep ADR-0425 only**: one instance per operation.
2. **One instance per position, later operations sent through the name-correlated publish**
   (`POST /api/v1/messages` with a correlation key).
3. **One instance per position, later operations delivered to the instance the line
   recorded, with a reporting outcome, and a start event as the fallback for rights
   without one.**
4. **As 3, without the fallback**: every deprovisioning must reach a running instance.

## Decision outcome

Chosen option: **3 — directed delivery to the position's recorded instance, with a start
event as fallback**, because it is the only option that gives one record per right for
every right issued in this shape while keeping every held right returnable and every
failed delivery visible.

### 1. The binding says which form it is

A product that binds a lifecycle process states its **instance form** explicitly:

| Form           | Meaning                                                      | Rule |
|----------------|--------------------------------------------------------------|------|
| `per-operation` | ADR-0425 as it stands: each operation starts an instance      | default; nothing changes for existing products |
| `per-position`  | `provision` starts the instance; later operations are delivered to it | this record |

The form is declared, not inferred from the diagram. A model that happens to contain a
catch event with the operation's message name must not silently change how a product is
returned; the declaration is what publishing checks the model against (I5).

The operation map stays one message name per operation. In the `per-position` form the
names are used as follows, and **publishing checks each rule** against the newest
version, the way it checks ADR-0425's start events today:

| Operation     | Must be                                                         |
|---------------|-----------------------------------------------------------------|
| `provision`   | a root message start event                                       |
| `deprovision` | a message **catch** event in the strand **and** a root message start event with the same message — the fallback of §3 |
| `change`      | optional; a message catch event in the strand                    |

Every catch event bound to an operation must declare the position as its correlation key
(`= orderId + "/" + positionId`). Delivery (§2) does not need it — it addresses the
instance by key — but a message without a correlation key would be matched by any
name-only publish, and one stray `POST /messages` would then return every right of the
product at once. For the same reason the name-correlated route refuses a message name a
catalogue product owns, exactly as the trigger route already refuses a catalogue-owned
start event (`catalogOwnerOfEntry`, ADR-0425).

### 2. Directed delivery

A new engine command delivers one message to **one instance**, named by its key, and
reports what happened. It is the sibling of ADR-0425's directed trigger and reuses its
receipt, so a repeated delivery with the same trigger id answers with the first outcome
instead of acting twice:

| Outcome       | Meaning                                                       | Caller's answer |
|---------------|---------------------------------------------------------------|-----------------|
| `Delivered`   | the instance was waiting for this message and took it          | 200 |
| `Replayed`    | the same trigger id was delivered before                        | 200, the first outcome |
| `NotWaiting`  | the instance is active but holds no subscription for this message now | 409, naming the elements it waits at |
| `Gone`        | no active instance has this key                                 | fall back (§3), or 404 where there is no fallback |

`NotWaiting` is the case the name-correlated publish loses. It is reported, never
buffered: a buffer would turn "the return is not possible now" into "the return will
happen at some later time nobody chose" (ADR-0370 decides buffering for correlation by
name; it does not apply to a delivery addressed to one instance).

The instance to deliver to is the one the line recorded with operation `provision` for
the product's lifecycle process (`order.LineInstance`). The fact that a delivery happened
is an event carrying the instance key, the element that took it and the outcome, frozen
at the time it was decided (I6), and folded by the one `applyToState` (I4).

### 3. The fallback: a right without a waiting instance

A `deprovision` whose delivery answers `Gone` — the line recorded no instance of this
shape, or the instance was cancelled — **starts** the process at its deprovision start
event through ADR-0425's directed trigger. That start event leads into the same
deprovisioning section of the strand, so the diagram still has one return path; it has
two ways in.

Rights that have never had an order line (ADR-0334's `unmanaged`) are reconciled as
today and use the start event directly; they have no instance to deliver to.

There is **no** fallback for `NotWaiting`: an instance that exists but cannot take the
return right now is a real state — the right is still being provisioned, or a change is
in progress without a return boundary — and starting a second instance beside it would
run two strands for one right.

### 4. The line stays the authority on what is held

A `per-position` instance is active while the right is held. That says nothing about
progress. The line's status remains the answer to "where does this position stand":
`done` means held, whatever the instance is doing. Position progress (ADR-0390) and the
open-work views report the instance's open tasks, as they do today, and report a strand
that only waits as **held**, not as work under way. Order cancellation is unchanged: it
applies to lines that are not yet done and cancels their recorded instances (ADR-0416),
which in this form is exactly the strand of a right that was never issued. No
cancellation boundary is needed in the model.

### 5. Versions: a right is revoked by the version it was issued on

A `per-position` instance stays on the version it started on, and so does its return.
This is the rule ADR-0427 already chose for bindings — a grant is undone by the rules in
force when it was made — applied to the version as well as the process id. It follows
that:

- deploying a new version changes nothing for rights already held. The deploy answer
  states how many active instances remain on older versions, so the choice is visible
  when it is made;
- moving held rights to a new version is an explicit ADR-0162 migration, never a side
  effect of deploying — the counterpart of ADR-0427's explicit rebind;
- an old version cannot be deleted while a right is held on it: the engine already
  refuses to delete a definition with running instances. Versions accumulate by design.

### 6. Stops against loops

A change loop is a cycle through a waiting element, and two rules keep it bounded:

- **A cycle without a wait state is already stopped by the engine.** ADR-0272 raises an
  incident when one token runs automatic steps beyond the execution budget. Nothing new.
- **Every cycle through the strand must wait for something outside the token.**
  Publishing refuses a `per-position` process in which any cycle passes no message catch
  event, receive task, user task, timer, signal or conditional catch — a cycle that would
  re-enter on its own is a modelling error in this form. (Amended at implementation: the
  check covers every cycle in the process rather than only those reachable from the
  `provision` start, and a timer, signal or condition counts as a wait, since each
  advances once per outside event and cannot run away.)
- **The number of changes is bounded by the model**, not by the engine: the strand counts
  its changes and refuses those beyond a limit the product sets. Atlas does not impose a
  number; a product with legitimate daily changes and one with two changes in its life
  need different ones.
- **Expiry is not modelled in the strand.** A time-bounded right ends through ADR-0344:
  the expiry process returns the line, which delivers `deprovision` (§2). A timer in the
  strand would be a second removal path the inventory does not see.
- **A return during a change is not lost**: the change activity carries an interrupting
  boundary event for the `deprovision` message, so the strand waits for a return at every
  element after the right is issued. Where a model does not, delivery answers
  `NotWaiting` (§2) instead of dropping the return.

### Consequences

- **Positive:**
  - One instance is the complete record of one right; data captured at issue is at hand
    at return without a second read.
  - The order of operations is enforced by the model: nothing is changed or returned
    before it is issued.
  - Every delivery has an answer; nothing is dropped silently.
  - Products that gain nothing keep ADR-0425 unchanged.
- **Negative / trade-offs accepted:**
  - Every held right is an active instance, possibly for years. Instance counts, lists
    and runtime views grow with the inventory, not with the work.
  - A correction to a return procedure does not reach rights already held until they are
    migrated; old versions stay deployed until their last right is returned.
  - Two ways into the deprovisioning section (catch event and fallback start) must be
    kept consistent by the modeller.
  - The binding gains a field and publishing gains checks; the console and the MCP save
    tool must offer both.
- **Follow-ups / risks to watch:**
  - Measure the engine, the store and the operator views with realistic numbers of
    waiting instances before converting more than a pilot product (open question).
  - Establish whether ADR-0162 migration moves an instance waiting at an event-based
    gateway (open question); until it does, a `per-position` product cannot have its held
    rights moved to a corrected version.
  - The `change` operation has no route from the order or the portal yet (ADR-0425 left it
    to external senders); this record gives it a place in the strand, not a trigger.
  - Retention: an instance that runs for years is never finished and never purged; its
    history grows with every change.

## Implementation

Landed:

- **Engine.** `Processor.DeliverMessage` (`engine/delivery.go`) delivers one message to
  one instance by key through a command-only intent, `IntentDelivering`, and answers
  `Delivered`, `Replayed`, `NotWaiting` or `Gone`. It correlates only that instance's
  open subscriptions for the message under the given key, through the same helper the
  name-correlated publish uses (`deliverToSubscriptions`), so a message arrives the same
  way whichever path sent it — intermediate catch, event-based gateway branch or message
  boundary. The receipt is ADR-0425's (`IntentTriggerReceived`), written in the same
  batch as the correlation.
- **Compiler.** `CompiledProcess.MessageCatchPoints` and `WaitlessCycle`
  (`compiler/lifecycleshape.go`) answer what publishing asks of the strand.
- **Catalogue.** `Item.LifecycleForm` (`per-operation` | `per-position`), checked by
  `checkBindings`; the per-position rules in `LifecycleProblems` read the strand through
  `ShapeLookup`, which the server's process lookup implements.
- **Order.** `Line.LifecycleForm` is frozen with the binding; `Line.StrandOf` finds the
  instance to deliver to. `RecordInstance` now keeps one entry per instance *and*
  operation, so a return carried by the strand counts as an attempt.
- **Server.** `startReturn` — the orderer's return and a recertification's revoke —
  delivers a per-position line's return through `deliverOrStart`, falling back to the
  deprovision start event when the strand is gone. `POST /api/v1/messages` refuses a
  message a per-position product delivers (`catalogOwnerOfDelivered`). The shop's open
  tasks list a strand once.
- **Surfaces.** The product form offers the form; the MCP save tool declares
  `lifecycleForm`.

Landed in a second step:

- **Deploy.** Deploying a version of a process a per-position product binds warns how
  many instances still run on older versions of it (`heldOnOlderVersionsOnLoop`, part
  of the deploy warnings), so the choice between leaving them and migrating them is
  made knowingly (§5).
- **Change.** `POST /api/v1/orders/{id}/lines/{item}/change` delivers the product's
  `change` message to the strand of a held per-position line, with a required
  `changeId` as the delivery's idempotency key. It has no fallback: a strand that is
  gone, or not waiting for a change, is a 409 (`deliverChange`). It is not an MCP tool,
  for the reason a return is not: changing a right somebody holds reaches the target
  system.
  *Since ADR-0429 slice B* the route is the action act for the action keyed `change`: the
  recipient may ask for it beside the orderer and an operator, and on a per-operation line
  it starts the change instead of refusing it. *Amended 2026-10-01:* the exclusion from
  MCP stands for what a customer asks; an action whose triggers include `operator` or
  `system` may be asked by an agent through `atlas_ask_order_line_action`, which names that
  trigger and is refused by the server for any action that does not declare it (ADR-0429 §2).
- **Progress.** Position progress (ADR-0390) answers `held` for a held per-position line
  whose strand only waits — at catch events, an event-based gateway, a receive task or a
  timer — and `active` while a change runs.

## Pros and cons of the options

### 1. Keep ADR-0425 only
- Good: built, piloted, and every return takes the newest version.
- Good: instance counts follow the work, not the inventory.
- Bad: the record of one right is spread over several instances; data captured at issue
  must be read again at return.
- Bad: does not meet the request.

### 2. Name-correlated publish
- Good: no engine change; `POST /messages` exists.
- Bad: a message that finds nobody waiting is dropped with a 200 (ADR-0425's reason 2);
  a return sent while a change is in progress is lost without a word.
- Bad: correlation by name reaches every instance whose key matches; a wrong or missing
  key returns the wrong right or none, and the caller cannot tell.
- Bad: waits for ADR-0370's buffer, and a buffer delays a return to an undetermined time
  instead of refusing it.

### 3. Directed delivery with fallback (chosen)
- Good: addresses the instance the line already recorded; no name matching.
- Good: every outcome is reported; `NotWaiting` is visible, not lost.
- Good: rights without an instance of this shape stay returnable.
- Bad: a new engine command, a new event and publishing checks.
- Bad: two ways into the deprovisioning section.

### 4. Directed delivery without fallback
- Good: one way into the return, simplest model.
- Bad: rights issued before conversion, found by reconciliation, or whose instance was
  cancelled could never be returned through the process — the class ADR-0344 calls
  unendable, created on purpose.

## Links

- extends ADR-0425 (lifecycle process with message triggers); does not supersede it
- relates to ADR-0426 (an untriggered create never seeds several start events)
- relates to ADR-0427 (converting a product; frozen bindings and explicit rebind)
- relates to ADR-0344 (time-bounded entitlements; expiry returns the line)
- relates to ADR-0370 (durable message buffer; not used for directed delivery)
- relates to ADR-0384 (position key), ADR-0416 (recorded instances), ADR-0390 (position progress)
- relates to ADR-0162 (instance migration), ADR-0272 (execution budget), ADR-0334 (reconciliation)
