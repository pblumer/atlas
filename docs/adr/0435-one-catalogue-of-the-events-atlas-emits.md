# ADR-0435: Atlas keeps one catalogue of the events it emits

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-02
- **Deciders:** Atlas maintainers
- **Open question:** whether a deployment becomes a fact on the log.
  [ADR-0019](0019-durable-deployments.md) keeps deployed definitions in a sidecar store, off
  the WAL, and `VTProcessDefinition` is declared in `model/record.go` but never written. The
  feed is state folded from the log ([ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md)
  §5), so until a deploy leaves a record there, `atlas.deployment.created` has no durable
  source the feed may read. Recording the fact, without moving the definition, would answer it;
  that is an engine decision of its own.
- **Question checked:** 2026-10

## Context and problem statement

Atlas already tells the world about itself, through five channels that grew one at a time:

1. **Messages between system processes.** The order fulfilment process starts on
   `atlas.order.placed` and advances on `atlas.order.advanced`. The names are constants in
   `api/order/service.go`, so the publisher and the model cannot drift.
2. **BPMN signals.** The intake process throws `atlas.user.requested` before its approval waits
   ([ADR-0431](0431-system-processes-announce-their-facts-as-signals.md)). An installation
   listens with a signal start of its own.
3. **The event feed.** What happened to an order position and every right granted or revoked
   leaves Atlas as CloudEvents ([ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md)
   §5). It is pulled from a cursor at `GET /api/v1/events` and pushed to a subscribed receiver
   ([ADR-0433](0433-the-event-feed-is-pushed-to-a-cloudevents-endpoint.md)). It has its own role
   ([ADR-0430](0430-the-event-feed-has-its-own-role-and-token-scope.md)), it can be narrowed by
   catalogue ([ADR-0432](0432-the-event-feed-is-narrowed-by-the-catalogue-that-maintains-the-product.md)),
   and its envelope is part of the runtime contract (`docs/runtime-contract.md`).
4. **Structured log events.** There are 92 names in `logging/events.go`, held by
   `logging/drift_test.go` so that no operational line bypasses the list.
5. **Prometheus metrics** ([ADR-0142](0142-prometheus-metrics.md)), such as
   `atlas_open_incidents`. These are aggregates, not events.

Each channel is well made on its own. Nothing says, in one place, what Atlas emits: under which
name, meaning what, carrying what, through which channel, since when, and how stable it is.

- A customizer finds a signal only by reading a system process's BPMN.
- An integrator finds only the feed, because the runtime contract lists only the feed.
- An operator reads the Go source of the log catalogue.

Nobody can answer "what can I listen to?" without reading the code.

The request that prompted [ADR-0431](0431-system-processes-announce-their-facts-as-signals.md),
and the questions that followed it on the same instance, show the gaps concretely:

- **Something waits for a person.** That is true in every system process: intake, offboarding,
  access review, and the three shop approvals. Only intake says so.
- **An incident was raised or resolved.** The fact is on the log (`VTIncident` with
  `IntentIncidentCreated` / `IntentIncidentResolved`), but no channel emits it by name. There is
  a gauge of how many are open, and no event saying that one opened.
- **A new version was deployed.** This fact is on no channel at all. It is not on the log (see
  the open question above), and it has no log event: the deployment log events are
  `deployment.reloaded_with_problems` and `deployment.diagram_updated`.

Without a catalogue, three things drift unseen:

- **Names.** Message, signal and feed names are dotted. Log names use underscores. Product
  actions declare their own types.
- **Payloads.** A signal carries every variable of the throwing instance
  ([ADR-0431](0431-system-processes-announce-their-facts-as-signals.md)). What a listener may
  rely on is whatever the instance happens to hold at that point in the model.
- **Who may listen.** A signal is engine-wide, so anyone who may deploy can receive a
  registrant's name and address. With a signal per system process, the same would apply to
  offboarding and access-review data.

The question this record answers: **how Atlas states, in one place and checked against the
code, every event it emits, so that a customizer, an integrator and an operator read the same
contract.**

A word on terms. In this record, *the event catalogue* is the list proposed here. The shop's
product catalogue ([ADR-0312](0312-portal-catalogue-order-inventory.md),
[ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md)) is always called
*the service catalogue*, and the feed's `homeCatalog` and `reach` refer to it.

## Decision drivers

- **One name per fact across channels.** If a fact is both a signal and a feed event, it is the
  same string in both, so that a listener and a CloudEvents consumer can tell they mean the same
  thing.
- **A contract per event**, covering:
  - meaning;
  - the moment it is emitted;
  - its payload, with every field marked as personal data or not;
  - a guarantee that it never carries a secret;
  - the version it arrived in;
  - whether it is stable.
- **Checked, not asked.** The catalogue and the code must agree in both directions, tested the
  way the log catalogue already is. A list that is only written down falls behind the first
  time someone forgets it.
- **The invariants hold.**
  - A platform fact is emitted only from an already-durable record, off the processor path
    (I1, I2).
  - A feed row is written by the one `applyToState` (I4).
  - Event export never becomes a second execution path
    ([ADR-0176](0176-standards-boundary-and-runtime-contract.md) §2).
- **No new protocol** ([ADR-0176](0176-standards-boundary-and-runtime-contract.md) §5). The
  envelope is CloudEvents, an in-engine event is a BPMN signal or message, and any machine
  description (AsyncAPI) is generated from the catalogue, never the other way round.
- **Access is decided per event, not per channel.** What decides who may receive an event is
  what it carries.

## Considered options

1. **Status quo.** Each channel documents itself.
2. **A prose page** in the handbook and the runtime contract listing the events.
3. **A machine-readable catalogue in Go as the single source**, with drift tests in both
   directions and the documentation generated from it.
4. **An external schema registry, or an AsyncAPI document as the source of truth.**

## Decision outcome

Chosen: **option 3, a catalogue in Go.** The documentation, the runtime contract's table and
any AsyncAPI description are generated from it, and tests hold it equal to what the code
emits.

### 1. What an entry says

| Field | Meaning |
|---|---|
| `Type` | The name, `atlas.<subject>.<fact>`, used verbatim as the signal, the message or the CloudEvents `type`. |
| `Kind` | `domain`: a fact of a system process. `platform`: a fact of the engine or the server. |
| `Meaning` | One sentence: what has happened when this is emitted. |
| `Moment` | Where it is emitted. For a domain fact, the system process and element id. For a platform fact, the record intent or server component it is derived from. |
| `Channels` | Any of `signal`, `message`, `feed`, `log`. A `log` channel names its event in `logging`'s catalogue. |
| `Payload` | The fields a receiver may rely on. Each has a name and a type, is `always` or `optional`, and is marked as personal data or not. |
| `NeverSecret` | Always true. The test that guards it is named in the entry. |
| `Since` | The Atlas version it arrived in. |
| `Stability` | `stable` (additive changes only) or `experimental` (may change, called out in the changelog). |
| `Access` | Who may receive it, per channel. |

Metrics stay outside the catalogue: they are aggregates, not events. An entry may name the
metric that counts it.

### 2. Names

- **Form.** `atlas.<subject>.<fact in the past tense>`, lower case, with a hyphen inside a word
  (`atlas.access-review.due`). An event states a fact, never a command: there is no
  `atlas.notify-admin`, because what a receiver does with a fact is not Atlas's to know
  ([ADR-0343](0343-pending-work.md)).
- **Existing names already conform.** `atlas.order.placed`, `atlas.order.advanced`,
  `atlas.entitlement.granted`, `atlas.entitlement.revoked` and `atlas.user.requested` stay as
  they are.
- **Product actions are named by their authors.** An action's declared event type (default
  `<message>.<outcome>`) is the product author's name, not Atlas's. The catalogue describes its
  shape once and does not list each product's names.
- **Log names stay.** Renaming 92 log events buys nothing and breaks every alert that matches
  them. An entry whose fact is also logged names its log event, and that mapping is the
  bridge between the two styles.

### 3. Two kinds, two producers

**Domain facts** are emitted by the system process models, as a signal (zero to many
listeners) or as a message (one receiver correlated). The rules of
[ADR-0431](0431-system-processes-announce-their-facts-as-signals.md) become the rules for every
domain entry:

- **An event is emitted at one of two kinds of moment.**
  - Something now waits for a person. This is what an installation most wants to be told.
  - An outcome is final. This is what another system wants to reconcile against.
  - A process does not emit an event at every step.
- **The payload holds no secret.** The throw sits where the instance holds none, and a test
  pins that position.
- **Every domain payload carries the instance key of the process that emitted it,**
  `atlasInstance`, so a receiver can point back at the request. The intake signal does not yet
  carry it. Adding it is the first change made under this record.

**Platform facts** are produced by the engine or the server from durable records. They are
delivered through the feed, when the fact is on the log, and as log events.

They are **not** thrown as BPMN signals under this record. An engine-thrown
`atlas.incident.raised` would start a listener whose own failure raises the incident that
triggers it again. Excluding the listener is possible, but that is a rule the engine would
have to know about the listener. A process that wants to react to a platform fact reads the
feed, through a subscription, like any other receiver.

### 4. The channels and what they promise

| Channel | Reach | Guarantee | For |
|---|---|---|---|
| Signal | Inside the engine, process to process | Zero to many listeners, not buffered: a listener that is not deployed misses it. Payload is every instance variable at the throw until payloads can be declared. | Customizing inside Atlas |
| Message | Inside the engine | Correlated to one receiver | System processes that drive each other |
| Feed | Outside Atlas, pulled or pushed | Durable, at least once, ordered, from a cursor, kept for the retention window | Integrations, and any notice that must not be missed |
| Log | Operations | None beyond the log pipeline | Alerting and diagnosis |

An entry may use several channels. When it does, the payload is the same in each, apart from
what the channel's envelope adds.

### 5. Where the catalogue lives and how it is held true

- **A package `eventcatalog`** at the root of the module, with no dependencies of its own,
  holds `var Entries []Entry`. It imports nothing from `engine`, `api` or `state`, so every one
  of them may import it.
- **Drift tests:**
  - Every signal and message thrown by an embedded system process (`api/systemprocesses/`) is
    a `domain` entry with that moment. Every `domain` entry is thrown where it says.
  - Every feed `type` Atlas itself names (as opposed to product-declared types) is an entry
    with the `feed` channel. Every such entry is produced by the feed.
  - Every entry with a `log` channel names an event in `logging`'s catalogue.
  - A `stable` entry may gain payload fields and never lose one. A golden file of the stable
    entries makes a removal a failing test, not a review comment.
- **Generated from the catalogue:**
  - a handbook chapter, *Ereignisse / Events*, in both languages;
  - the event table of `docs/runtime-contract.md`;
  - optionally an AsyncAPI 3.0 description served beside the OpenAPI one.
- **The Modeler offers catalogued names.** A signal or message field offers the catalogued
  `atlas.*` names a model may listen to, the way the message picker already offers message
  sources.
- **The Console shows it.** A Console page lists the entries, and for an administrator the
  listeners (§7).

### 6. Who may listen

- **Feed:** the existing rules hold. That means the `feedreader` role, the `events` token, and
  the service-catalogue narrowing for events that have a `homeCatalog`
  ([ADR-0430](0430-the-event-feed-has-its-own-role-and-token-scope.md),
  [ADR-0432](0432-the-event-feed-is-narrowed-by-the-catalogue-that-maintains-the-product.md)).
  A platform fact belongs to no service catalogue, so an `events` token narrowed by `reach`
  does not receive it. Whether platform facts need a narrowing of their own is decided with the
  first of them.
- **The feed can be switched off with the shop.** Today the feed is a route of the
  service-catalogue area. When an installation switches that area off
  ([ADR-0434](0434-the-catalogue-can-be-switched-off.md)), neither the pull route nor the push
  delivery runs. A platform fact must not fall silent because the shop is off.
  - So before the first platform entry lands in the feed, the feed has to leave that area.
  - With the area off, the feed withholds the service catalogue's types and still delivers the
    platform types.
  - The alternative is a second feed for platform facts. That would split the cursor, the
    subscription and the retention a receiver has to manage, so it is the weaker choice.
  - Which of the two is decided with the first platform entry.
- **Signals.** Deploying a process with a signal start or catch on an `atlas.*` name requires a
  role, proposed: `admin`. This closes the trade-off
  [ADR-0431](0431-system-processes-announce-their-facts-as-signals.md) accepted, where anyone who
  may deploy can receive a requester's data. It closes it before offboarding and access-review
  facts make the same trade worse.
  - **Refused at deploy.** The error names the element, the event, the personal-data fields the
    listener would receive, the role the deploy needs, and the caller's role.
  - **Reported before the deploy.** The Problems panel's validation (`POST /api/v1/validate`,
    [ADR-0026](0026-problems-panel-and-versioned-validation.md)) runs the same check and reports the same
    finding as an error on the element. A modeler learns of the rule while modelling, not from a
    refused deploy.
  - **One check, two callers.** The deploy gate and the validation call the same function, so
    the two cannot disagree. This is the reason ADR-0026 gives for validating through the real
    compiler instead of a copy of its rules.
  - **The finding depends on who asks.** Until now a validation result depends on the model and,
    with `applicationId`, on its application ([ADR-0230](0230-process-information-model.md)).
    This adds the caller's role: the same draft is clean for an administrator and has an error
    for a modeler. The finding therefore states both the role it needs and the caller's role, so
    the two views explain each other. The MCP deploy tools (`atlas_deploy`,
    `atlas_deploy_project`) act as their caller, so the same rule holds there.

### 7. Who may read the catalogue

The catalogue holds two kinds of information, and they are not equally sensitive.

- **The entries.** Name, meaning, moment, payload, guarantees and access rule describe what
  Atlas can say, not what one installation does with it. The same text is in the repository and
  the handbook. They are readable by `modeler` and above, because choosing an event to listen
  to is modelling. The Modeler's name picker reads them.
- **Who listens now.** This covers:
  - which deployed definitions have a signal start or catch on an `atlas.*` name, with process,
    version, project and the personal-data fields each receives;
  - which feed subscriptions receive which types.

  Together that is a map of where personal data flows in this installation, across every
  project. The feed subscriptions are already administrator configuration
  ([ADR-0433](0433-the-event-feed-is-pushed-to-a-cloudevents-endpoint.md)). This part is
  readable by `admin` only.
- **Two routes, not one route with a hidden column.** The listeners are served by a route of
  their own that requires `admin`. A modeler's answer therefore never contains them, rather than
  containing them and having the page hide them. An MCP read tool follows the same split.
- **A modeler still sees the models they may open.** Project sharing
  ([ADR-0071](0071-sharing-scopes.md)) decides that, unchanged. What this record withholds is
  the view across every project at once, which only an administrator has elsewhere too.
- **The Console view.** A Console page *Events* lists the entries for every caller with
  `modeler` or above. For an administrator it adds the "listening now" column and the
  listeners of the selected event.

### 8. The first entries

| Type | Kind | Channels | State |
|---|---|---|---|
| `atlas.order.placed` | domain | message | exists |
| `atlas.order.advanced` | domain | message | exists |
| `atlas.entitlement.granted` | platform | feed | exists |
| `atlas.entitlement.revoked` | platform | feed | exists |
| action outcome (`<message>.<outcome>`, product-declared) | domain | feed | exists, described by shape |
| `atlas.user.requested` | domain | signal | exists ([ADR-0431](0431-system-processes-announce-their-facts-as-signals.md)), gains `atlasInstance` |
| `atlas.user.created`, `atlas.user.rejected` | domain | signal | planned |
| `atlas.user.offboarding-requested`, `atlas.user.disabled` | domain | signal | planned |
| `atlas.access-review.due`, `atlas.access-review.completed` | domain | signal | planned |
| `atlas.approval.requested`, `atlas.approval.granted`, `atlas.approval.denied` | domain | signal | planned, for the three shop approval processes |
| `atlas.incident.raised`, `atlas.incident.resolved` | platform | feed, log | planned, folded from `IntentIncidentCreated` / `IntentIncidentResolved` |
| `atlas.deployment.created` | platform | log; feed once the open question is answered | planned |

A planned entry lands with the change that emits it, never before. The drift tests would refuse
an entry nothing produces.

**The incident entries carry their cause.** The payload is the process definition key, the
element id and the incident type: the grouping [ADR-0337](0337-incident-floods.md) and
[ADR-0381](0381-a-problem-aggregates-incidents.md) use. A receiver can then tell a new cause
from the thousandth incident of a known one, without a flood reaching a chat channel one
incident at a time. Whether the feed should rather carry the cause opening and clearing, as
facts of their own, is decided when the entries are built.

### 9. What this record does not decide

- **Declared signal payloads.** Narrowing what a throw sends by input mappings on the throw
  event, instead of every instance variable, is an engine change with its own record.
- **A deployment as a fact on the log.** See the open question.
- **Notifications.** Whether a fact becomes a Discord message, a mail or a ticket stays a
  modelled process, as [ADR-0343](0343-pending-work.md) requires. The catalogue says what can be
  listened to, not who is told.
- **Renaming log events.**

### Consequences

- **Positive:**
  - One answer to "what can I listen to?", read the same way by a customizer in the Modeler, an
    integrator in the runtime contract and an operator in the handbook.
  - Payloads and personal data become reviewed contracts instead of whatever a model happens to
    hold.
  - Drift is a failing test, as it already is for log events.
  - The `admin` rule is seen while modelling, in the Problems panel, rather than first as a
    refused deploy.
  - Every later event is added the same way, so the first one sets the pattern rather than each
    one inventing its own.
- **Negative / trade-offs accepted:**
  - A new package and a set of tests that every change emitting an event must keep green. That
    is the point, and it is also friction.
  - Generating documentation adds a step like `make whats-new`: forgetting it fails CI rather
    than shipping stale pages.
  - The `admin` rule for `atlas.*` listeners narrows who may customize. An installation that
    trusts every modeler gains nothing from it.
  - Validation now depends on the caller as well as the model. Two people can see different
    findings on the same draft; the finding names both roles so that this is visible.
  - A modeler cannot see who else listens across projects and has to ask an administrator. That
    is accepted: that view is exactly what the rule protects.
- **Follow-ups / risks to watch:**
  - The open question about deployments on the log.
  - The feed leaving the service-catalogue area, so that platform facts keep flowing when the
    shop is switched off ([ADR-0434](0434-the-catalogue-can-be-switched-off.md)).
  - Declared signal payloads.
  - Whether a process may subscribe to the feed directly, rather than through a `cloudevents`
    endpoint. That would give in-Atlas reactions to platform facts the feed's guarantees.
  - The contract version in API metadata, already an open item of the runtime contract. A
    receiver branching on an event's version needs it.

## Pros and cons of the options

### Option 1: status quo
- Good: nothing to build. Each channel's documentation sits next to its code.
- Bad:
  - No one place answers what Atlas emits.
  - Names and payloads drift between channels.
  - Personal data in payloads is reviewed nowhere.

### Option 2: a prose page
- Good: quick, and readable by anyone.
- Bad: it is the list that falls behind, and it cannot fail a build. The repository has two
  cases of this:
  - `examples/README.md` came to be missing five examples before `examples/catalog_test.go`
    checked it.
  - The handbook said "thirty" examples while it carried thirty-eight cards, until
    `examples/handbookcounts_test.go` held the number to the cards.

### Option 3: a catalogue in Go, checked and generated (chosen)
- Good:
  - one source, checked in both directions;
  - documentation that cannot fall behind;
  - the same precedent as `logging/events.go`;
  - available to the Modeler and the server alike.
- Bad: a package, tests and a generator to maintain, and every new event costs an entry.

### Option 4: an external registry, or AsyncAPI as the source
- Good: a standard description that tools understand without Atlas.
- Bad:
  - It makes a description format the source of truth for behaviour, which ADR-0176 §2 rules
    out ("CloudEvents does not define the event payload").
  - It moves the contract out of the code that must keep it.
  - Generating AsyncAPI from the catalogue gives the tooling without that cost.

## Links

- builds on [ADR-0431](0431-system-processes-announce-their-facts-as-signals.md) (system
  processes announce facts as signals), whose rules become those of every domain entry
- relates to [ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md) §5,
  [ADR-0430](0430-the-event-feed-has-its-own-role-and-token-scope.md),
  [ADR-0432](0432-the-event-feed-is-narrowed-by-the-catalogue-that-maintains-the-product.md)
  and [ADR-0433](0433-the-event-feed-is-pushed-to-a-cloudevents-endpoint.md) (the event feed:
  envelope, access, narrowing, push)
- relates to [ADR-0434](0434-the-catalogue-can-be-switched-off.md) (the service-catalogue
  area, and with it the feed, can be switched off)
- relates to [ADR-0176](0176-standards-boundary-and-runtime-contract.md) (standards boundary
  and runtime contract)
- relates to [ADR-0019](0019-durable-deployments.md) (deployments off the log), the open question
- relates to [ADR-0088](0088-signal-events.md) (signal semantics),
  [ADR-0142](0142-prometheus-metrics.md) (metrics), [ADR-0343](0343-pending-work.md) (Atlas does
  not send), [ADR-0337](0337-incident-floods.md) and
  [ADR-0381](0381-a-problem-aggregates-incidents.md) (incident causes)
