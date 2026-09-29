# ADR-0425: A product's lifecycle is one process, and each operation is a message start it is triggered at

- **Status:** Accepted
- **Implementation:** Partial
- **Date:** 2026-09-29
- **Deciders:** Atlas maintainers
- **Open question:** what a **change** of something already held means on the order.
  ADR-0359 refuses changing what is held in place and records a corrected configuration
  without running anything. This record lets a product's process carry a change entry
  point and lets an external system trigger it, but it does not decide which line status
  a running change puts a position in, whether the portal offers it, or what the
  inventory records afterwards. The change entry point is therefore optional and is
  never triggered by the order itself until a record settles that.
- **Question checked:** 2026-09

## Context and problem statement

A catalogue product binds **two processes**: one to provision, one to deprovision
(ADR-0312). Both ids are frozen onto the order line at placement, and each is started
with an **untriggered** create — the fulfilment process through `POST /api/v1/instances`,
a return, a recertification and a reconciliation through `CreateInstance` directly.

Maintainers want the opposite shape: **one process per product**, holding provisioning,
change and deprovisioning side by side, each entered at its own start event. BPMN has
exactly that shape — alternative start events are alternative triggers — and since
ADR-0226 Atlas executes it correctly for every *triggered* create: only the start event
that fired is seeded.

The shape cannot be reached today, for three reasons found in the code:

1. **An untriggered create cannot pick a start event.** `startElementsFor` seeds the
   none starts, or, where a process has none, **every** start event (the permissiveness
   ADR-0035 recorded and ADR-0226 kept). A lifecycle process started by the API would
   provision, change and deprovision at once.
2. **The trigger that can pick one — a message — fails silently.** Message-start
   matching is by name across every definition (ADR-0035). A publish that matches
   nothing is a no-op and answers 200 (`handlePublishMessage`); the durable buffer that
   would hold it is accepted but not built (ADR-0370). A singleton start whose key is
   taken is skipped with `continue` (ADR-0094). The deactivated definition is skipped
   too (ADR-0119). In every case the caller learns nothing, and the answer carries no
   instance key.
3. **Triggers are to come from outside as well.** An HR system reporting a leaver, a
   ticket system asking for a change, a target system reporting a right it removed.
   Such callers deliver at least once, retry on timeouts, and cannot be trusted to know
   Atlas's process ids or its global message namespace.

For fulfilment a silent no-op is not a minor defect. `orderreturn.go` records a return
as under way **before** the process starts precisely so that a start which fails stays
visible. A publish that silently starts nothing leaves the line `returning` forever,
with nothing anywhere saying why.

## Decision drivers

- **The BPMN semantics are the model.** Which operations a product has, and how each
  begins, should be readable off the diagram rather than off a gateway on a variable.
- **Loud, not silent.** A trigger that starts nothing must fail with a reason the
  caller can act on. This is the rule the order code already follows.
- **Exactly once into the process, whatever the sender does.** External senders retry.
  A duplicated deprovisioning is a second conversation with a target system Atlas does
  not control.
- **The inventory stays the single truth.** A right is revoked or granted through the
  order and inventory layer (ADR-0312: *no second provisioning path*); a trigger that
  bypassed it would leave a line `done` for a right that is gone.
- **No new durable semantics where an existing one fits.** Instance creation, singleton
  start and message-flow records already exist and are recovered; only what is truly new
  becomes a new event.
- **The invariants hold.** Resolution of names happens at the API boundary and at
  deploy, never on the hot path (I1, I5); every new fact is an event folded by the one
  `applyToState` (I4, I6); nothing is answered before it is durable (I2).

## Considered options

1. **Keep two process bindings**, and let a maintainer draw one diagram per operation.
2. **One process, one none start, and a gateway on an `operation` variable.**
3. **One process, and an untriggered create that names a start event by element id.**
4. **One process with message start events, triggered by the existing name-only
   publish.**
5. **One process with message start events, triggered by a directed, reporting,
   idempotent trigger** (the hardened form of option 4).

## Decision outcome

Chosen option: **5 — a directed trigger to a message start event**, because it keeps the
BPMN shape of option 4, removes each of its silent outcomes, and is the only option in
which an external sender has a contract it can bind to (ADR-0373) rather than an element
id or a global name.

### 1. The lifecycle process

A product may bind **one** process in place of the two. It carries one root-scope
**message start event per operation**:

| Operation     | Required | Started by                                            |
|---------------|----------|-------------------------------------------------------|
| `provision`   | yes      | fulfilment, or an approval process that agreed        |
| `deprovision` | yes      | a return, a recertification, a reconciliation, or an external leaver report |
| `change`      | no       | an external system only, until the open question is settled |

The **operation** is the contract; the message name behind it is the process's own
business. The binding maps one to the other, the way ADR-0373 maps an entry-point name to
an element.

Each branch keeps the obligation ADR-0312 places on every provisioning process: its last
step reports the outcome against `positionId`.

### 2. The directed trigger

A new route:

```
POST /api/v1/processes/{processId}/triggers/{messageName}
{ "correlationKey": "…", "variables": { … }, "triggerId": "…", "source": "…" }
```

It differs from `POST /api/v1/messages` in exactly the points that made option 4 silent:

- **Directed.** It addresses one process id and one of its message start events. It
  consults the **latest active** version of that id only — the same version a name-only
  publish would start (`supersedeStarts`) — and it never correlates a waiting
  subscription. A trigger starts; it does not continue. The global namespace problem
  disappears, because the name is resolved inside one definition.
- **Resolved at the boundary.** Process id and message name are resolved to a definition
  key and an element index before anything is enqueued. The engine receives what it
  already receives today from a correlating message: `AppendCreateInstanceCommand(defKey,
  vars, correlationKey, startElement)`. No new instance-creation path exists.
- **Reporting.** The command carries a report slot, as `CreateInstanceReporting` already
  does, and the answer is written only after the batch is durable (I2). The outcomes:

  | Outcome                                   | Status | Body                          |
  |-------------------------------------------|--------|-------------------------------|
  | instance created                          | 201    | `instanceKey`                 |
  | same `triggerId` seen before              | 200    | the original `instanceKey`, `replayed: true` |
  | singleton key already live (ADR-0094)     | 409    | the live instance's key       |
  | process unknown, or no such message start | 404    | which of the two              |
  | definition deactivated (ADR-0119)         | 409    | the definition key            |

  The singleton skip is kept as behaviour and made visible as an answer: for a lifecycle
  process, keying the start on `positionId` is what stops a deprovisioning from racing a
  provisioning of the same position.
- **Idempotent.** See §3.

`POST /api/v1/messages` is unchanged. It remains the right tool for correlation and for a
genuine broadcast.

### 3. Exactly once, per sender

`triggerId` is **required** on the directed trigger. Together with `source` (the
authenticated caller where there is one) it forms a receipt key.

ADR-0075's high-water mark does not fit: it assumes a monotonic sequence per source, and
an HR system's event ids are not one. The receipt is therefore a **set**, not a mark:

- A new value type records `TriggerReceipt{Source, TriggerID, InstanceKey, At}`. It is
  appended **in the same batch** as the create it guards, so one fsync commits both or
  neither (I2), and `applyToState` folds it into a column family keyed by
  `(source, triggerId)` (I4).
- The handler checks the receipt before creating. A hit answers with the recorded
  instance and creates nothing.
- Receipts are pruned by an explicit event carrying the cutoff timestamp, written by a
  scheduled command, so replay removes exactly what was removed live (I6). The retention
  defaults to **30 days** and is an operator setting. A retry arriving later than that is
  a new trigger, and this is stated in the route's documentation, not left to discovery.

### 4. The binding and what an order freezes

`catalog.Item` gains an alternative to `ProvisionProcess` / `DeprovisionProcess`:

```go
// Lifecycle binds one process whose operations are message start events.
LifecycleProcess string            `json:"lifecycleProcess,omitempty"`
Operations       map[string]string `json:"operations,omitempty"` // operation → message name
```

An item carries **either** the two process ids **or** the lifecycle binding, never both.
Publishing a catalogue (ADR-0312) additionally checks, against the latest active version:

- the process exists and is active;
- `provision` and `deprovision` are bound; `change` may be;
- every bound message name is a **root-scope message start event** of that process;
- the process has **no none start event**. A none start in a lifecycle process is an
  entry the catalogue never uses and an untriggered create would seed; without one, an
  untriggered create is refused altogether
  (ADR-0426);
- the process is not `FulfilmentProcess`, as today.

`order.Line` freezes the lifecycle binding at placement, beside the two ids it already
freezes and for the same reason: what was granted is revoked by the rules in force when it
was granted. An existing line carries no lifecycle binding and is fulfilled and returned
exactly as today. **Nothing is migrated.**

### 5. Starting a lifecycle process by hand is refused

A lifecycle process has no none start and several start events. An untriggered create of
such a process — the API, the MCP create tool, a call activity — would today seed every
start event and run provisioning and deprovisioning at once. That trap is not specific to
the catalogue, and it is closed for every process by its own record:
ADR-0426. This record depends on
it: a lifecycle process is entered only through one of its triggers.

### 6. The order starts its positions through a start act, not through a route choice in the models

The fulfilment process and the three approval processes start a position today by posting
`{processId, variables}` to `POST /api/v1/instances` (ADR-0411). Branching in those models
between that route and the directed trigger is possible — the REST connector's `url` is a
FEEL expression, and its body is the task's input mappings (ADR-0174) — but it is the
wrong place:

- **Four models would carry the same branch**, one of them multi-instance, and each would
  have to compute a `triggerId` that is stable across retries yet new for a deliberate
  second attempt. Only the order knows which attempt this is.
- **The link between a position and its instance is noted by the server** at the moment
  it is certain (`notePositionInstance`, ADR-0416), and today that happens on the create
  path only. A model posting to a message route would bypass it, and the shop would stop
  showing the position's open tasks.
- **Where a line may start is the order's statement**, not a model's. A route choice in a
  model is a second copy of it.

So a new act on the order does it:

```
POST /api/v1/orders/{id}/lines/{position}/start      { "operation": "provision" }
```

It reads the line's **frozen** binding, starts the two-process form through the existing
create path or the lifecycle form through the directed trigger's engine call, derives the
`triggerId` from the order, the position, the operation and the attempt, and records the
instance on the line. The fulfilment and approval models post to it instead of to
`/api/v1/instances`: one input mapping fewer, no branch, and the same call for both
binding shapes. A return, a recertification and a reconciliation already start
server-side and call the same function directly.

Rollout order follows from the embedded bundle (ADR-0122): the models are redeployed as a
new version when the binary changes, so the server and its bundle are upgraded first, and
products are bound in the lifecycle form afterwards. A line in the lifecycle form carries
no `provisionProcess`, so an unchanged older model that still posts to
`/api/v1/instances` gets a 400 for the empty process id — loud, not silent.

### 7. Authority is decided by the order's state, not by who is calling

The shipped models authenticate with an operator API token held as the `atlas` secret
(ADR-0411). Any other operator client — an HR system among them — presents the same kind
of credential, and anybody allowed to deploy a model can author one that uses the `atlas`
secret. The internal service identity of ADR-0049 does not change that: it is minted per
process start for the in-process MCP adapter and is not something a model's REST task
holds. **No caller identity available today separates "the catalogue layer" from any
other operator.**

The gate is therefore not an identity but the absence of a door:

- The directed trigger route **refuses unconditionally** an entry point a published
  catalogue binds. There is no exception for a privileged caller, so there is nothing to
  impersonate.
- The catalogue layer never uses that route. The start act (§6), a return, a
  recertification and a reconciliation fire the trigger through an **in-process call**
  after checking the order: the line exists, its status permits the operation, and it was
  not already started for this attempt.
- An operator token can therefore still start a position — exactly as it can today
  through `POST /api/v1/instances` — but only the one the order says may start, and only
  once per attempt.

### 8. External triggers go through the inventory, not around it

An external system is given two kinds of door, never a third:

- **Catalogue acts.** A leaver report or a revocation request names a principal and a
  product, or a position. It enters through the same layer a return or a reconciliation
  does today, which records the line's transition durably first and then fires the
  trigger in-process (§7). The inventory therefore never says `done` about a right a
  process has already removed.
- **Uncatalogued entry points.** A message start that no catalogue binds may be triggered
  directly through the directed trigger route. Once ADR-0373 lands, this is what a
  published interface's **send** grant covers; until then the route requires
  `RoleOperator`, as `POST /api/v1/messages` does today.

### 9. Converting an existing product

Nothing is migrated by this record: lines placed before a product is converted keep the
two frozen ids. How long the old processes are then still needed, what protects them, and
how lines may be moved to the lifecycle binding is decided in
ADR-0427.

### As built

What is in the tree, and where it went further or stopped short of the text above:

- **Engine.** `Processor.TriggerStart` creates the instance in the same batch as its
  receipt (`VTTriggerReceipt`, `IntentTriggerReceived`), so one fsync commits both; a
  repeated delivery reads the receipt through the transaction and answers
  `TriggerReplayed`, including a second delivery in the same batch. Receipts are dropped
  by `IntentTriggerReceiptsPruned` carrying its cutoff, written by the retention sweep at
  most once an hour and only when a receipt is older than `--trigger-receipt-ttl`
  (`ATLAS_TRIGGER_RECEIPT_TTL`, default 720h).
- **Route.** `POST /api/v1/processes/{processId}/triggers/{message}`, and the MCP tool
  `atlas_trigger_start`. The receipt's source is the caller's principal, optionally
  narrowed by the body's `source` — never the body alone, so no caller can replay or read
  another's triggers. The catalogue layer records its own under `atlas:order`, which the
  route cannot produce.
- **Start act.** The fulfilment process calls it for *every* ready position, and it
  decides whether the approval or the provisioning starts: a gated position nobody
  approved starts its approval process, and the approval process calls the act again with
  `approvedBy`, which records the approval (`order.Approve`) in the same act that starts
  the provisioning. That is one call where §7's follow-up foresaw two, and it makes the
  approval a precondition the act checks rather than a report it trusts. A started
  position is marked `running`, so `/next` no longer offers it twice, and a repeated call
  answers with the instance already recorded.
- **Returns, recertification, reconciliation** start through the same function, which
  takes either binding form. A return's trigger id counts the deprovisioning attempts
  already recorded on the line, so a return asked for again after `returnFailed` is a new
  trigger. `LineInstance` gained `operation`.
- **Catalogue.** `lifecycleProcess` and `operations` on the product, in the editor and
  the MCP schema; the fulfilment report asks once for a lifecycle process; the landscape
  draws one edge to it.
- **Not built: `change`.** The binding is stored and checked at publish, but nothing
  starts it: the order has no act for it (the open question), and §8 refuses a
  catalogued start event on the trigger route. That is the one piece of the decision
  missing, and why this record is `Partial`.

### Consequences

- **Positive:** a product's whole lifecycle is one diagram, and every way into it is a
  start event somebody can see.
- **Positive:** every trigger has an answer: an instance key, a replay of one, or a reason.
  The silent outcomes of the name-only publish do not exist on this route.
- **Positive:** an external sender can retry freely. A duplicated leaver report creates
  one deprovisioning, not two.
- **Negative / trade-offs accepted:** a new value type, column family and pruning event
  for receipts. This is new durable state and a new recovery path, and it is justified
  only because external at-least-once senders are in scope.
- **Negative:** two binding shapes coexist in the catalogue, in the fulfilment report,
  in the landscape mesh (`panorama/mesh.go`) and in the portal, for as long as any
  product uses the old one.
- **Follow-ups / risks to watch:**
  - The shipped approval models record a rejection but not an approval. Recording one
    is already possible (`POST …/decision` with `approved: true` calls `order.Approve`,
    and is optional today). If the models record it before posting the start act, the
    start act can refuse an approval-gated line without an approver on record — a check
    on the order's state, which is what §7 relies on. Until then the start act can check
    that a line is open and not yet started, but not that it was approved, which is the
    same trust the create route extends today.
  - `TestEverySystemProcessCallsARouteThatExists` must learn the start act's route in the
    same change that the four models move to it.
  - When ADR-0370's buffer lands, it must **not** apply to the directed trigger: a start
    that cannot happen now is a 404 or a 409, never a message waiting for a definition.
  - Scoping an operator token to fewer acts than every operator act is outside this
    record. It matters more once external systems hold such tokens, and belongs with
    ADR-0373's grants.

## Pros and cons of the options

### Option 1 — keep two process bindings
- Good: nothing changes; every current invariant and test stays as it is.
- Bad: provisioning and deprovisioning of one product live in two diagrams that must be
  kept consistent by hand, and a third operation means a third binding.

### Option 2 — none start and a gateway on `operation`
- Good: no engine change at all; deterministic; the smallest diff.
- Bad: the triggers are hidden in a gateway instead of drawn as start events. A caller
  that forgets `operation` silently takes the default branch. An external sender has to
  know a variable convention instead of a contract.

### Option 3 — untriggered create by start-event element id
- Good: small API change; `StartElements` already exists on the command.
- Bad: couples every caller, including external ones, to element ids, which ADR-0373
  explicitly declines to make a contract. It says nothing about idempotency or external
  senders, so both would have to be added anyway.

### Option 4 — message starts over the existing publish
- Good: no engine change; fully BPMN; the Jira and clio bridges already publish this way.
- Bad: every failure is silent (no match, deactivated, singleton taken, no buffer), the
  answer names no instance, names are global so products must invent unique ones, and
  nothing deduplicates a retrying external sender.

### Option 5 — directed, reporting, idempotent trigger (chosen)
- Good: keeps option 4's BPMN shape and removes each of its silent outcomes; external
  senders get a stable contract and retry safely; reuses the existing create command.
- Bad: new durable state for receipts; a new route; two binding shapes during transition.

## Links

- builds on ADR-0226 (start events are triggers) and ADR-0035 (message start events)
- amends ADR-0312 (a product binds one lifecycle process as an alternative to two)
- relates to ADR-0359 (changing what is held — the open question above)
- relates to ADR-0094 (singleton message start, now answered rather than skipped)
- relates to ADR-0075 (idempotent inbound delivery; this record uses a receipt set
  because external ids are not monotonic)
- relates to ADR-0335 (starting an instance returns its key)
- relates to ADR-0370 (the durable buffer, which must not cover this route)
- relates to ADR-0373 (published interfaces and the **send** grant for external callers)
- relates to ADR-0119 (a deactivated definition is answered, not skipped)
- depends on ADR-0426
- continued by ADR-0427
- relates to ADR-0411 (the shipped models call Atlas with an operator token) and
  ADR-0049 (the internal service identity, which does not reach a model)
- relates to ADR-0416 (the server notes which instance works a position)
