# ADR-0429: A product declares its actions, each a command whose outcome is a fact published beyond Atlas

- **Status:** Accepted (amended 2026-10-01 — the points left open are decided at acceptance; see §10)
- **Implementation:** Partial
- **Date:** 2026-09-30
- **Deciders:** Atlas maintainers
- **Open question:** what the portal costs the run loop at production order rates. The
  loop is shared by the engine and the order store, and order-store writes run inside
  it; `runloop_turn_held_seconds` measures a turn, but nobody has read it under a
  realistic mix of orders, returns and actions. The rejection of a separate log for the
  portal (§8) rests on where that cost comes from, argued from the code, not on a
  measurement.
- **Question checked:** 2026-09

## Context and problem statement

A catalogue product binds a lifecycle process whose operations are message triggers
(ADR-0425), and it may run as one instance per position that later operations are
delivered to (ADR-0428). Three things about that shape are found in the code and stop
it from carrying what maintainers now ask of it.

**The operations are a closed set of three.** `provision`, `change` and `deprovision`
are the only keys `Item.Operations` may carry; anything else is refused at publish
(`knownOperations`, `api/catalog/lifecycle.go:43`). A product has, in practice, an
unknown number of things that can happen to what somebody holds: more storage, an
upgrade within the same product, an inactivation, a password reset, a certificate
renewal. Some are asked for by the customer from the portal, some by the operators who
run the service. There is one `change` slot for all of them, and the portal offers
none: `POST /api/v1/orders/{id}/lines/{item}/change` exists (ADR-0428), but `shop.js`
never calls it.

**An operation is named as though it were a fact, and its outcome is not named at
all.** `laptop.provision` is an intention: it can be refused (a 409, `NotWaiting`, an
approval that says no). What happened afterwards reaches Atlas only as a line status on
`POST /api/v1/orders/{id}/lines/{item}` (`done`, `skipped`, `failed`, `running`). For a
change there is no status to report, so its outcome exists nowhere outside the
instance that ran it. Invariant 6 — events are facts, commands are intentions — is the
rule Atlas applies to its own log; the product vocabulary above it does not follow it.

**Nothing about a right leaves Atlas.** Billing, a CMDB and a customer notification
each need to learn that a right was granted, changed or revoked. No outbound event on
an order line or an entitlement was found in `api/order` or `api/`. ADR-0176 §2
already foresees mapping committed events to CloudEvents envelopes, consuming
already-durable facts off the processor path (the ADR-0114 boundary), and lists
"public event-export payloads and their schema versions" as part of the Atlas runtime
contract (§3).

Two further facts shape the answer:

- **The names are typed by hand in two places.** The product editor takes the start
  event names as free text (`api/web/catalog-admin.js:1775-1783`); the modeler's
  message name is free text as well (`api/web/editor.js:5236`), and the one list of
  known names Atlas serves, `GET /api/v1/message-sources` (inbound watches), is shown
  only as a hint once a name has been typed exactly (`fillMessageSources`,
  `editor.js:5273`). A Worker's events cannot be picked.
- **An inbound watch may claim a name a product owns.** `POST /messages` refuses a name
  a per-position product delivers (`catalogOwnerOfDelivered`), and the trigger route a
  catalogue-bound start (`catalogOwnerOfEntry`), but creating a watch checks only
  whether the claimant can see the definitions that receive the name
  (`definitionBlockingClaim`, `api/messageclaim.go:134`). By this reading of the code,
  a watch publishing `laptop.deprovision` would start a deprovisioning with no order
  behind it — the path around the inventory ADR-0425 §8 forbids. No test establishes
  it either way.

## Decision drivers

- **How many actions a product has is the product's business; what an action means to
  the inventory is Atlas's.** The order layer must interpret every action it is asked to
  run, so the vocabulary it interprets stays closed while the list stays open.
- **A command may be refused; a fact may not.** Names say which one they are.
- **History cannot be added after the fact; a transport can.** The facts an external
  consumer will need must be recorded from the first action onward, even if the
  delivery to that consumer comes later. This is ADR-0372's argument for carrying the
  whole envelope in its first cut.
- **The inventory stays the single truth** (ADR-0312), and it keeps exactly two events:
  there is deliberately no third for editing an entitlement (`model/record.go:444-452`).
- **An external consumer must never hold back the engine.** WAL compaction waits for
  every consumer watermark (ADR-0131); a consumer that disappears must not be one.
- **Do not add work to the run loop that is not the engine's**, and say where the loop's
  cost actually comes from before moving anything to relieve it.
- **The invariants hold:** every new fact is an event folded by the one `applyToState`
  (I4), carries its generated values (I6), and is visible only once durable (I2);
  names are resolved at publish and at the API boundary, not on the hot path (I1, I5).

## Considered options

1. **Keep the three operations**; a change carries a `changeType` variable and the
   process branches on it.
2. **An open list of actions, outcomes kept on the order**, and a webhook fired by the
   order service when a line changes.
3. **An open list of actions with closed effect classes; outcomes as engine facts; an
   external feed as a projection of those facts, read off the loop; push delivery
   prepared, not built.**
4. **As 3, with the portal's facts in a log of their own**, beside the engine's WAL.

## Decision outcome

Chosen option: **3**, because it is the only option in which the list is open while
the meaning stays closed, in which every outcome is a durable fact from the first day,
and in which external consumers read state rather than the log.

### 1. A product declares its actions

`catalog.Item` gains a list that replaces the operation map:

```go
// Action is one thing that can be asked of what somebody holds (ADR-0429).
type Action struct {
    Key      string            `json:"key"`      // "storage-extend": the contract
    Message  string            `json:"message"`  // "mailbox.storage.extend": the process's business
    Effect   string            `json:"effect"`   // provision | deprovision | change | service
    Triggers []string          `json:"triggers"` // customer | operator | system
    Labels   map[string]string `json:"labels"`   // language → what the button says
    Form     string            `json:"form,omitempty"`     // an Atlas form for what the action needs (ADR-0358)
    Outcomes map[string]string `json:"outcomes,omitempty"` // outcome → event type; see §3
}
Actions []Action `json:"actions,omitempty"`
```

- **The key** follows ADR-0305's rules for a key: lower-case letters, digits and dashes,
  1 to 64 characters, not renamed in place. It is what the portal, a requirement
  (ADR-0407) and a capability's interface can point at later without a migration.
- **The effect is closed:**

  | Effect | What it does to the position | Per product |
  |---|---|---|
  | `provision` | `pending` → `running` → `done`; grants | exactly one |
  | `deprovision` | `done` → `returning` → `returned`; revokes | exactly one |
  | `change` | stays `done`; the configuration of what is held changes | any number |
  | `service` | nothing about what is held changes | any number |

- **A change never changes the item or the variant.** The entitlement is untouched, as
  the inventory's two-event rule requires; price (ADR-0361), eligibility (ADR-0347) and
  conflicting rights (ADR-0342) are stated per item and variant, so an action cannot
  pass around them. Anything that would change item or variant is a return and a new
  order, as ADR-0359 already says of what is held.
- **Suspension is not an effect in this record.** An inactivation that can be undone
  needs an entitlement state the inventory does not have; until a record adds one, an
  inactivation is either a `service` action the process carries out, or a
  `deprovision`.
- **Triggers:** `customer` is whoever may return the line today (the orderer, or an
  operator for any order), and the recipient who holds the right (§10, decision 4); `operator` is `RoleOperator`; `system` is something
  observed about the held right rather than asked for by a person — see below. The
  inventory's own sweeps — expiry (ADR-0344), recertification (ADR-0341),
  reconciliation (ADR-0334) — keep using the `deprovision` action and are not
  declared.
- **The editor pre-fills** `<item>.provision` and `<item>.deprovision` and does not let
  either be removed.

**A `system` trigger is a threshold on what is held**: a mailbox above nine tenths of
its quota, a certificate a month from expiry. Four rules keep it runnable and keep it
from loading the engine:

- **The condition lives inside the position's instance.** A per-position strand has the
  right's context; a conditional catch, boundary event or event subprocess reads the
  measurement as a variable and starts the action's branch (ADR-0137). A conditional
  start event at process level has no instance to read from; it used to compile as a
  plain start with its condition dropped, and is now refused at deploy (§9).
- **Atlas receives crossings, not samples.** The observer — a Worker reading the target
  system on a schedule, or a monitoring system — reports through the action act of §2,
  under an operator credential as every external caller does until scoped grants exist
  (ADR-0425 §8). Each report is a directed delivery with a receipt and a condition
  re-check. Ten thousand mailboxes measured every five minutes are 2.88 million of those
  a day; one report per crossing is a handful. Filtering belongs at the source.
- **The threshold is not a literal in the model.** A per-position strand runs for years
  on the version it was issued on (ADR-0428 §5), so `usedGB > 100` would stay in force for
  every mailbox issued under it until each is migrated. It is stated relative to what the
  right holds (`usedGB > quotaGB * 0.9`), or delivered with the measurement from where it
  is maintained.
- **Repeated crossings need a loop, not a re-armed event.** A non-interrupting conditional
  boundary or event subprocess fires once per arm (ADR-0137). A strand that must react
  again waits at a conditional catch in a loop — which counts as a wait for ADR-0428's
  cycle rule (`waitsForOutside`, `compiler/lifecycleshape.go`) — and the relative
  threshold keeps it from firing again the moment the quota grew. A conditional catch
  cannot be one alternative of an event-based gateway, which accepts message, timer and
  signal catches only (`isCatchEvent`, `compiler/validation.go`); it runs as a boundary
  or event subprocess beside the strand's wait, or in a branch of its own.

**Existing products are read, not migrated.** An item with `operations` is read as the
actions `provision`, `change` and `deprovision` it names, with the effect of the same
name and the trigger each has today. Order lines keep what they froze; a line placed
after this lands freezes `actions` in the same way (ADR-0312, ADR-0427).

**Publishing checks each action** against the newest version, as ADR-0425 and ADR-0428
check operations today: in the `per-operation` form every action's message is a root
message start event; in the `per-position` form `change` and `service` actions are
message catch events correlated by the position (ADR-0428 §1). A message name is owned
by one action of one product; an inbound watch may not claim it, and creating or
editing a watch refuses such a name — closing the gap in the context above.

### 2. Triggering an action

```
POST /api/v1/orders/{id}/lines/{item}/actions/{action}
{ "commandId": "…", "reason": "…", "variables": { … } }
```

It generalises the change route of ADR-0428, which stays and means the action keyed
`change` for a line that froze the old map. `commandId` is required and becomes the
trigger id, so a retry answers with the first outcome (ADR-0425 §3). The act checks the
order first — the line exists, is held, the action is one its frozen binding declares
and the caller is one of its triggers — and then fires in-process, never through the
public trigger route (ADR-0425 §7):

- `per-position`: directed delivery to the strand (ADR-0428 §2), with its answers;
- `per-operation`: the directed trigger at the action's start event (ADR-0425 §2).

The portal learns which buttons to show from
`GET /api/v1/orders/{id}/lines/{item}/actions`: the customer actions of the frozen
binding and, for a strand, whether it is waiting for each message **now**. The BPMN
model therefore decides when an upgrade is possible, and the catalogue holds no second
copy of that rule. The route reads off the loop (ADR-0239).

Triggering one operator action for every held position of a product is not decided
here.

### 3. The outcome is a fact

The process states how an action ended with the shop send task (§4); a process that
reports over REST uses the route behind it:

```
POST /api/v1/orders/{id}/lines/{item}/actions/{commandId}/outcome
{ "outcome": "completed" | "rejected" | "failed", "result": { … } }
```

`provision` and `deprovision` may keep the report route they use today or state their
outcome with the shop send task; either way their statuses map to the same three
outcomes (`done` → `completed`, `failed` → `failed`, `returned` → `completed`,
`returnFailed` → `failed`), and an approver's refusal is the `rejected` of `provision`. The outcome vocabulary is closed for the same
reason the effects are. The event type of each outcome is declared on the action and
defaults to `<message>.<outcome>`, so `mailbox.storage.extend` completes as
`mailbox.storage.extend.completed` unless the product names it
`mailbox.storage.extended`.

A new value type records it:

- `VTActionOutcome`, intent `IntentActionCompleted`, carrying the command id and its
  source, the action key, effect, outcome and event type, the principal **id**, item,
  variant, order and position, the instance key, the moment (read at command time and
  frozen, I6) and `result`.
- For `provision` and `deprovision` it is appended **in the same batch** as the grant or
  revocation it accompanies, so one fsync commits both or neither (I2).
- It is idempotent per order, position and command id: a repeated identical report
  writes nothing, and a report of a different outcome for the same command is a 409.
- Like an entitlement, it outlives the order it came from. It is kept with the
  entitlement history (ADR-0346), and that record's open question about erasure applies
  to it unchanged.
- `result` is bounded in size and holds scalars an action declares. It must not carry
  personal data beyond the principal id (ADR-0314); that is a rule for the product's
  author, stated here because the log cannot enforce it.

### 4. The shop's receive task and send task

A product's process meets the shop at exactly two kinds of point: where it **takes** a
command, and where it **states** a fact or **issues** a command of its own. BPMN has an
element for each — the receive task (ADR-0102) and the send task (ADR-0112) — and the
product vocabulary is carried on them rather than on new element types.

**The shop receive: a plain receive task, or a message catch event.** Its message is an
action's (§1), picked from the product actions the modeler offers (§6), with the
correlation key preset to the position (`= orderId + "/" + positionId`, ADR-0428 §1). It
adds no runtime behaviour: the engine opens the same subscription it opens for any
receive task, and the catalogue's publish check already finds it by message
(`MessageCatchPoints`, `compiler/lifecycleshape.go:27-50`). It is therefore **not** a
declared kind; what marks it as the shop's is that a product owns its message.

One limit carries over: an event-based gateway does not yet accept a receive task as a
target (ADR-0110, deferred). Where a strand waits for any of several actions, the
alternatives are message catch events after the gateway; a receive task serves where the
strand waits for one.

**The shop send: a send-task kind of its own, `shop`,** beside the message kind and the
Worker Types the send task already offers (`sendTaskKind`, `api/web/editor.js:4611-4648`).
Unlike the receive, it has behaviour no existing element has, so it is declared on the
element and compiled at deploy (I5), never inferred from a message name at runtime. It
has two modes:

| Mode | Declares | Does |
|---|---|---|
| `outcome` | the action key and the outcome (`completed`, `rejected`, `failed`) | records the outcome of the command this instance is carrying out (§3), through the order layer |
| `command` | the product, the action key, and where the position comes from | issues an action on a position through the order act of §2 — an HR leaver process returning a right, a maintenance process resetting a password |

Both compile to a **reserved job type the server serves itself**, added to
`engineOnlyJobTypes` (`api/connectorplacement.go:139`). That list is the one exception
ADR-0164 and ADR-0233 allow — work that only mutates state the run loop owns, with no
system to reach and nothing for a worker to hold — and adding to it needs a record of its
own. This is that record, and the case is the same as user provisioning's (ADR-0123): an
outcome changes the order store and appends an engine fact; a command goes through the
order act and the in-process trigger or delivery. Neither leaves the server, so neither
can park the loop on a network call.

What the kind gives that a REST task cannot:

- **A publish check that every action is answered.** Publishing a catalogue refuses a
  product whose process has no `outcome` send task for an action's `completed`, or has one
  naming an action the product does not declare. An arbitrary REST call is not
  recognisable as a report, so without the kind a branch that never reports is found by a
  command that stays open.
- **No credential in the model.** The shipped models reach Atlas over the REST Worker
  Type with an operator token held as the `atlas` secret (ADR-0411); an in-process task
  holds none, which removes the concern ADR-0425 §7 raised about operator tokens in
  models for this path.
- **No hand-built URL.** `orderId`, `positionId` and `commandId` are read from the
  instance's scope — the first two are set by every delivery today
  (`api/orderchange.go:138-143`), and the action act of §2 adds `commandId`. A missing one
  is an incident on the send task, not a 404 somebody finds later.

The command a `command` send task issues carries a command id derived from its job key,
which is stable across the job's retries, so a retried job answers with the first
outcome (ADR-0425 §3). It passes every check the order act makes about the line.

**Settled at acceptance (§10, decision 1); the paragraph below records why it was open.**
**Not settled: in whose name a `command` send task acts.** The order act checks that the
caller is one of the action's triggers (§2), and a process has no caller in that sense:
it runs as whoever deployed it, and anybody allowed to deploy could author one that
returns every right of every person. Until a record binds the command mode to an
identity and a scope — the deploying project, a grant per product, or operator actions
only — the `outcome` mode is built and the `command` mode is not.

Considered and not taken: a REST task with an element template generated from Atlas's
own OpenAPI description (ADR-0300). It passes ADR-0299's gates no better — its surface is
"call this endpoint" — and gives neither the publish check nor the credential-free path.
The outcome route of §3 stays for processes that report over REST today.

### 5. The feed: what leaves Atlas

**The public contract is a CloudEvents 1.0 envelope** in structured JSON, as ADR-0176 §2
foresees. It is a versioned part of the Atlas runtime contract, not the internal record
(§4 of that record):

| Attribute | Value |
|---|---|
| `id` | node identity (ADR-0357, ADR-0401) + partition + log position |
| `source` | the installation's external URL + `/catalog` |
| `type` | the declared event type, or `atlas.entitlement.granted` / `atlas.entitlement.revoked` |
| `subject` | `orders/{orderId}/positions/{position}`, or `principals/{id}/items/{itemId}` for a right no order produced |
| `time` | the frozen moment of the fact |
| `dataschema` | the schema of this `type` at its version |
| `data` | the fact's payload, principal as an id only |

Entitlement grants and revocations belong to the feed alongside action outcomes, because
a CMDB needs rights reconciliation adopted or corrected, which no action produced.

**The feed is state, not the log.** `applyToState` folds every `IntentActionCompleted`,
`IntentEntitlementGranted` and `IntentEntitlementRevoked` into a feed column family,
keyed by partition and log position, both deterministic on replay (I4). Rows are
dropped by an explicit prune event carrying its cutoff, written by the retention sweep,
exactly as trigger receipts are (`IntentTriggerReceiptsPruned`, ADR-0425). Retention
defaults to 30 days and is an operator setting. Consumers therefore never hold back WAL
compaction (ADR-0131): the log may be compacted while the feed keeps its window.

**The projection lands with the first outcome record**, not with the route. That is the
part of "prepared" that cannot be deferred: once the route exists, the feed is complete
back to the retention window rather than starting on the day it was switched on.

**Pull delivery:** `GET /api/v1/events?after={cursor}&limit={n}` reads the feed off the
loop (ADR-0239), answers at least once, and leaves deduplication to the consumer by
`id`. A cursor older than the retention window answers **410** with the oldest cursor
still held — loud, not silent. It requires `operator` in the first cut; a role scoped to
the feed alone (ADR-0209) and scoped tokens (ADR-0194) are follow-ups, as ADR-0425
already notes for operator tokens held by external systems.

**Push delivery is prepared and not built.** A later slice delivers the same envelopes
through a Worker (ADR-0203) with a server-held cursor per subscription, the retry ladder
of the task and a circuit breaker per endpoint (ADR-0340). It reads the feed; it adds no
fact and no second path into `applyToState`.

### 6. The modeler offers the names

`GET /api/v1/message-sources` gains a `sourceKind` for each entry:

- `inbound-watch` — what it lists today;
- `product-action` — each action's message, with its product, effect and triggers;
- `process` — the message start and catch events of deployed processes, which the server
  already computes (`processLookup.EntryPoints`, `CatchPoints`) and does not serve.

The modeler's message name becomes a list grouped by source. Free text stays allowed,
because a process may be modelled before the action that triggers it is declared; the
publish checks of §1 remain the last word. In a process a catalogue product binds, an
inbound-watch name is marked as not usable for an action, so the trap in the context is
visible where it is set. Offering the inbound-watch names is independent of the rest of
this record and may land first.

*As built — the inbound-watch slice.* An element that waits for a message (a start event,
an intermediate catch, a boundary event, a receive task) gains an «Events from Workers»
group in its message picker, read from the existing `GET /api/v1/message-sources`: one
choice per message name, every publishing worker named, a watch that is off marked, and a
name the diagram already declares left out. Picking one declares a message of that name or
links the one already declared; the name field suggests the same names
(`offerWorkerEvents`, `api/web/editor.js`). An element that throws a message is offered
none, since a Worker already sends the name. The listing names every watch on the server,
as the hint under the field always has; a name claimed by a watch the deployer may not
reach is still refused at deploy (ADR-0205), so the picker can offer a name the deploy then
refuses — loudly, at the door ADR-0205 placed. Narrowing the offer by reach would need the
route to say what each caller reaches, which it does not today. The listing is read once
per render and shared with the hint. No `sourceKind` was added yet: with one
source there is nothing to group by, and the field arrives with the second source.
`e2e/worker-events-modeler.spec.mjs` covers it.

**The shop's tasks are marked, not redrawn.** BPMN lets a tool add markers to its
elements to show a subtype, as long as the element's own shape and markers stay what the
standard says they are. The modeler already does exactly that for what a task runs: an
implementation badge in the Implement view and the runtime views, the plain BPMN marker
in the Design view (`drawImplBadges`, `api/web/editor.js:1598-1620`). The shop's tasks use
the same mechanism, with one difference: that badge covers the task's top-left marker,
and on a send or receive task that marker is the envelope saying which way the message
goes. The shop badge therefore sits **beside** the envelope, never over it, and it is
derived — for the receive task from a product owning its message, for the send task from
its declared kind — so it cannot disagree with what the element does. A reader with any
other BPMN tool still sees a correct send or receive task.

### 7. What this adds to the engine

Per action, one `VTActionOutcome` record and one feed row, folded in the same apply;
per provisioned or returned line, one record beside the grant or revocation it already
writes. The instance that carries out the action writes a record for every element it
enters and leaves, every job and every variable, so the facts this record adds are a
small share of what the same action already costs the engine.

The feed is read off the loop, and the pull route never touches the processor.

### 8. Why the portal does not get a log of its own

Option 4 was examined because the concern behind it is right: the portal must not
crowd the engine. It fails on where the crowding actually comes from.

- **The portal's durable state already lives mostly outside the WAL.** Orders and
  catalogues are sidecar JSON stores. What the portal shares with the engine is the
  **run loop**, not the log: order-store writes run inside it (`RecordInstance`,
  `api/order/service.go:866-878`, and every other order mutation), each an atomic
  sidecar write with its own two fsyncs (`api/sidecar/sidecar.go:21-47, 78-86`) outside
  the WAL's group commit; and one person's shop listing walks every order on the loop
  (`Store.For`, `api/order/service.go:803`, already named in ADR-0427). A second log
  relieves none of that.
- **What a second log would hold is the smallest part.** Entitlements, their history and
  action outcomes are a few records per order line. The shop's and the products'
  processes — fulfilment, approval, every lifecycle instance — are ordinary instances in
  the engine's log, and they stay there whichever way this is decided.
- **It undoes a decision on purpose.** ADR-0312 put entitlements in the engine log
  precisely so that access records are rebuilt from the same log as every other engine
  fact (`model/record.go:130-135`).
- **Everything around the log knows exactly one.** A server opens one WAL, one state
  store and one processor for partition 1 (`cmd/atlas/main.go:547, 574, 587`); the state
  store keeps one applied position (`state/store.go:30-31`); checkpoints, compaction,
  backup and restore go through a store registry with one `wal` entry
  (`api/storeregistry.go:92`, ADR-0131, ADR-0107, ADR-0109); the exporter tails
  `<data-dir>/wal` by name (`api/server.go:2025-2037`, ADR-0114). Each would have to learn
  a second log.
- **It is possible, but only as a second engine.** The playground runs its own log, store
  and processor on a partition of its own (`playground/sandbox.go:286`), which is the
  separation a portal log would need too; the two could then talk only asynchronously
  (I3), and a trigger receipt must stay in the same batch as the instance it created
  (ADR-0425), so the split could never be clean. A second log per node is ADR-0175's
  subject, and it is not started.

If the open question's measurement shows the portal crowding the engine, the remedy is
**a loop of its own for the portal's stores**, and reads off the loop wherever the
portal only reads (ADR-0239, ADR-0382) — not a log of its own. That is a separate
record, and it is the one this record's open question exists to trigger.

### 9. A conditional start event at process level is refused at deploy

The `system` trigger was first drawn as a conditional start event — "Mailbox Storage >
100GB" in front of the process. It compiled without a word: the start became a plain
start and its condition was dropped (`registerScope`, `compiler/scope_compile.go`), so
the model deployed, never started on its own, and ran unconditionally when started by
hand. The Modeler had no entry for it either (`UNSUPPORTED_EVENT_DEFS` was empty).

Atlas runs a conditional event only inside an instance (ADR-0137), and at process level
there is nothing for the condition to read. The fault is therefore closed where every
other rule the compiler gains later is closed (ADR-0177, ADR-0393):

- **The compile marks it and stage 5 refuses it.** A root-scope start that carries a
  conditional event definition still compiles to the plain start it has always been,
  and is recorded on the compiled process; the stage-5 rule `start.conditional`
  (`checkConditionalStarts`, `compiler/validation.go`) reports it as an error anchored
  to the start event. A deploy and the Modeler's dry run refuse it; a definition
  already deployed with one is brought back on reload unchanged, with the finding
  logged beside it.
- **An event subprocess is untouched.** Its conditional start is its trigger and runs
  while the parent scope does; it is never in root scope, so the mark never reaches it.
- **The Modeler warns while the author draws.** `conditionalStartReason`
  (`api/web/editor.js`) puts the unsupported badge and a Problems warning on any
  conditional start outside an event subprocess; the server's finding on the same
  element replaces the warning once validation answers, so it is not listed twice.
- A test in `api` holds the two halves together, since nothing else links a rule in Go
  to a warning in JavaScript.

This part of the record is built, as is the inbound-watch slice of §6; the rest is not,
which is why the record is `Partial`.

### 10. Decided at acceptance (amended 2026-10-01)

The maintainers accepted this record with the points it left open decided as follows.
Each is binding on the slice that builds it; none changes a slice already landed.

1. **The `command` send task acts in the name of an allow-list on the product.** A
   product names the process applications whose processes may command it, and those
   processes may issue only actions whose triggers include `operator` or `system`,
   never one that is `customer` only. The order act checks both. Until the list exists
   on a product, nothing may command it.
2. **Inactivation is a `service` action.** It changes nothing about what the inventory
   holds; the process carries it out. A suspended state is a record of its own, written
   only if billing or recertification must know about it.
3. **Triggering an operator action for every held position of a product** is wanted,
   as a later slice: bounded, idempotent per position, asynchronous, with a report of
   each position's answer.
4. **`customer` includes the recipient.** Whoever holds the right may trigger its
   customer actions, beside the orderer and an operator. A right ordered for somebody
   else (ADR-0349) is changed by the person who uses it.
5. **A different variant is a return and a new order**, as §1 says. An action that
   switches the variant through the order, with approval and price, is a record of its
   own if it is needed often.
6. **A customer action is approved inside its process** — a user task whose refusal is
   the outcome `rejected` — not by a stage in the order.
7. **A missing translation of an action's label is reported, not refused**, as every
   other missing translation is (ADR-0414).
8. **The feed keeps 30 days** by default, and its pull route requires `operator` in
   the first cut, as §5 says.
9. **The run loop is measured** (the open question) before a per-position product is
   rolled out beyond a pilot.
10. **The slices land in this order:** A — actions on the product; B — the action act,
    availability and the portal's buttons; C — the outcome fact and the shop send task;
    D — product actions and badges in the modeler; E — the feed. The plan beside this
    record (`docs/planning/0429-product-actions-plan.md`) holds them.

### Consequences

- **Positive:** a product has as many actions as it needs, and the order layer still
  understands every one of them through four effects.
- **Positive:** every command has a named outcome, and every outcome is a durable fact —
  for a change too, which today leaves no trace outside its instance.
- **Positive:** external systems get a versioned contract and a complete window of facts
  the day the route is switched on, and cannot hold back compaction.
- **Positive:** the modeler offers the names instead of asking for them to be typed, and
  a Worker's events become selectable.
- **Positive:** a product process takes commands at ordinary receive tasks and states
  facts at a send task publishing can check, with no credential in the model.
- **Negative / trade-offs accepted:** a new value type, a new column family and a prune
  event — new durable state and a new recovery path, justified because consumers outside
  Atlas are in scope.
- **Positive:** a threshold on a held right becomes an action with a place in the
  model, and the conditional start that looked like one is refused instead of deployed
  as a start that never fires.
- **Negative:** a model deployed today with a process-level conditional start is refused
  at its next deploy. It keeps running as it did until then, and the reload names it.
- **Negative:** two binding shapes on the product (the operation map and actions) for as
  long as a line froze the old one.
- **Negative:** a public event contract is a compatibility promise; a breaking change to
  a `type`'s payload is a new schema version.
- **Follow-ups / risks to watch:**
  - Measure `runloop_turn_held_seconds` with orders, returns and actions at a realistic
    rate (open question), before per-position products are rolled out beyond a pilot.
  - The `per-position` strand must wait for every change and service action it declares;
    a strand with many actions makes the event-based gateway crowded. Non-interrupting
    event subprocesses would be the natural shape, but `MessageCatchPoints`
    (`compiler/lifecycleshape.go:27-50`) does not count their start events, and whether
    directed delivery reaches them has not been checked.
  - Triggering an operator action across every position of a product, and a suspended
    state for entitlements, each need a record of their own.
  - A feed-scoped role and scoped tokens, before an external system holds a credential.
  - `engineOnlyJobTypes` grows by the shop send task (§4). Its handler must stay inside
    what the list admits — the order store and engine facts — and never grow a call to
    another system; the day it needs one, it is a Worker Type and moves.
  - An event-based gateway accepting receive tasks as targets (ADR-0110's deferred
    follow-up) would let a strand wait for several actions at receive tasks.
  - The publish check of §4 proves an outcome task exists on the action's path, not
    that every path through the branch reaches it.

## Pros and cons of the options

### Option 1 — keep three operations, branch on a variable
- Good: no change to the catalogue, the order or the publish checks; the change route
  exists; the order layer keeps interpreting exactly three things.
- Bad: the actions hide in a gateway, the pattern ADR-0425 rejected for operations; a
  missing variable takes the default branch silently.
- Bad: availability per action cannot be read from the strand, which waits for `change`
  and not for `storage-extend`; the portal would offer buttons the process then refuses.
- Bad: the outcome is still unnamed, and nothing leaves Atlas.

### Option 2 — outcomes on the order, a webhook from the order service
- Good: no engine change; the order already records statuses.
- Bad: the order is deleted by retention long before the right ends (ADR-0346); the
  outcome of a change would go with it.
- Bad: a webhook fired from the order service is a side effect with no durable record
  of what was sent; a consumer that was down misses facts, and there is no window to
  read them back from.

### Option 3 — open actions, closed effects, facts in the engine, a feed as state (chosen)
- Good: open where the product needs it, closed where Atlas must interpret it; durable
  facts from the first day; a public contract under ADR-0176; consumers decoupled from
  compaction.
- Bad: new durable state and a prune path; a public contract to keep compatible; two
  binding shapes during transition.

### Option 4 — as 3, with a log of the portal's own
- Good: the portal's facts would have their own fsync stream, retention and backup
  cadence, and could in principle be moved to another node.
- Bad: relieves the loop of the smallest part of the portal's cost and none of its
  largest (§8); reverses ADR-0312's reason for putting entitlements in the engine log;
  every consumer of "the log" would have to learn a second one.

## Links

- extends ADR-0425 (lifecycle process with message triggers) and ADR-0428 (one instance
  per position); opens their closed operation set
- relates to ADR-0312 (inventory as engine state), ADR-0346 (entitlement history),
  ADR-0359 (what is held is never changed in place)
- relates to ADR-0176 (standards boundary; CloudEvents for event export), ADR-0114
  (export off the processor path), ADR-0131 (compaction waits for consumers)
- relates to ADR-0372 (the whole envelope in the first cut), ADR-0203 (Workers),
  ADR-0340 (circuit breaker)
- relates to ADR-0205 (message name claims) and ADR-0075 (inbound events)
- relates to ADR-0305 (capabilities and their interfaces) and ADR-0407 (requirements)
- relates to ADR-0239 and ADR-0382 (reads off the loop), ADR-0357 and ADR-0401 (node
  identity), ADR-0209 and ADR-0194 (roles and tokens)
