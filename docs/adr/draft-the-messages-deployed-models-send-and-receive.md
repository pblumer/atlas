# ADR-DRAFT: Atlas shows the messages its deployed models send and receive

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-10-02
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-draft-the-signals-deployed-models-throw-and-receive](draft-the-signals-deployed-models-throw-and-receive.md)
gives the installation's own signals a view: who throws a name, who receives it, and where it
crosses a project. It leaves messages out, because messages already have a partial view and more
sources than the models show. This record decides the message half.

A message couples models by name and correlation key:

- **Every match is delivered.** A message goes to every open subscription with the same name and
  correlation key (`correlateMessage` in `engine/behavior.go`, `CorrelatableSubscriptions` in
  `state/tx.go`). It also starts every newest, active definition with a message start of that
  name, unless a singleton start already holds the key
  ([ADR-0035](0035-message-start-events.md), [ADR-0094](0094-singleton-message-start.md),
  [ADR-0119](0119-deactivate-deployed-process.md)).
- **Not buffered.** A message that matches nothing is a no-op
  ([ADR-0020](0020-message-correlation.md)). The durable buffer of
  [ADR-0370](0370-durable-message-buffer.md) is not built.
- **Every variable is the payload.** A message throw or message end event sends every variable
  of the throwing instance (`messageThrowEventBehavior`). Nothing on the throw narrows it.
- **Engine-wide.** As for signals, sharing scopes govern authoring, not execution
  ([ADR-0071](0071-sharing-scopes.md)). A message crosses projects whenever a name and key meet.
- **More senders than models.** Besides a model's throw, a message is sent by the publish route
  (`POST /api/v1/messages`, `operator`), by an inbound watch of a Worker, and by a product action
  ([ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md)).

What exists today is `GET /api/v1/message-sources` (ADR-0429 §6). For each message name it lists
inbound watches, product actions, and the processes that wait for it, and the Modeler's message
picker reads it. Three things are missing:

1. **A model's own throw is not a source.** A message throw event, a message end event and a
   message-kind send task ([ADR-0112](0112-send-tasks.md)) send a name, but no row says so. The
   question "is this name fed" therefore answers "no" for a name that a deployed model sends.
2. **A message event subprocess is not a receiver.** `CompiledProcess.MessageReceivers` reads
   `MessageCatchPoints`, which covers catch events, receive tasks and message boundaries, and
   `MessageStartEvents`, which covers root-scope starts only. An event subprocess's message
   trigger (`EventSubProcessDetail.MessageName`) is in neither. Message-sources, and the
   administrator's listener view of [ADR-0435](0435-one-catalogue-of-the-events-atlas-emits.md)
   §7, miss it for the same reason.
3. **No overview.** Message-sources is a flat list the picker filters by name. Nothing groups it,
   shows it outside the Modeler, or says what is wrong with a name.

The question this record answers: **how Atlas shows the messages the installation's own models
send and receive, beside the sources that are not models, so that a modeler and an operator can
see who sends a name, who waits for it, and where it crosses a project.**

## Decision drivers

The drivers of the signal record hold unchanged: an observation is not a contract; no engine
change, no new store, no cost that grows with instances; one answer on every surface; no role
reads more than it already can; ADR-0435 stays whole. Two are particular to messages:

- **One list of sources, not two.** Message-sources is already the place that answers where a
  message comes from. A second list of senders would drift from it.
- **Claim only what definitions decide.** A message can arrive from the publish route, which no
  definition shows. A finding that depends on the publish route is not decidable here and is not
  made.

## Considered options

1. **Status quo.**
2. **One inventory for both channels.** Merge signals and messages into one route and one shape.
3. **Complete message-sources, and group it.** Add the missing senders and receivers to
   message-sources, and serve its rows grouped by name, with findings, from a second route built
   on the same function.
4. **Record every publish** and show what was actually sent and received.

## Decision outcome

Chosen: **option 3.** Option 4 is the runtime follow-up the signal record already names.

### 1. Message-sources becomes complete

- **A fourth kind, `process-throw`.** A deployed process that sends the name: a message throw
  event, a message end event, or a send task that names a message, which compiles to a message
  throw. It needs a `MessageSenders` beside `MessageReceivers`, reading the message throw detail
  table that all three share.
- **Event subprocesses are receivers.** `MessageReceivers` reports a message event subprocess,
  named by its start event, as `SignalPoints` already does for signals. The `process` rows of
  message-sources, and ADR-0435's listener view, gain them with that change. The view of
  ADR-0435 does not change otherwise.
- **The shape stays flat.** The picker keeps reading message-sources as it does. Each row gains
  only the fields a point needs that it lacks: version and whether it is the newest, project,
  system process, role, and for a boundary or event subprocess whether it interrupts.

### 2. A grouped view with findings

`GET /api/v1/message-names` (`modeler`) serves the rows of message-sources grouped by name, with
the findings of §3. It calls the same function as message-sources, so the two cannot disagree,
and it applies the same filters (§4).

Versions are read as in the signal record: every deployed version is listed, findings read only
the newest version of each process.

### 3. Findings

| Finding | When | What it means |
|---|---|---|
| `message.unreceived` | A sender inside Atlas, that is a process throw, an enabled inbound watch, or an active product action, sends a name that no newest, active definition waits for. | What it sends is not received: a message is not buffered (ADR-0020). |
| `message.crosses-projects` | A process in one project sends the name, and a process in another project, or one without a project, waits for it. | Not an error. Every variable of the sending instance is written into an instance of the other project, whose members may read it ([ADR-0275](0275-instance-visibility.md)). The correlation key decides which instance, not what it receives. |
| `message.reserved-name` | A model outside the system project sends an `atlas.*` name. | It speaks for Atlas. A name a system process waits for reaches that process. |

Three findings are deliberately not computed:

- **"Nothing sends it."** The publish route can send any name, and no definition shows its
  callers. Message-sources already tells the Modeler which known sources feed a name, and an
  empty answer stays a hint, not a finding.
- **Correlation keys that cannot meet.** A key is a FEEL expression evaluated over each
  instance's variables at runtime. Whether a sender's key and a receiver's key produce the same
  value is not decidable from definitions.
- **A receiver of an `atlas.*` name.** It stays in ADR-0435's administrator view, as for signals.

**Not in the Problems panel**, for the reason the signal record gives: whether a receiver exists
depends on other deployments, and deploy order is free.

### 4. Who may read it

- **The filters of message-sources hold unchanged.**
  - A product action is listed only for the catalogues the caller maintains or was shared
    (`productActionSources` in `api/messagesources.go`).
  - A watch's description and correlation key are filled only for a caller with viewer access to
    its Worker.
- **Process rows are shown to every modeler.** Message-sources already does so today, and the
  same facts are in each definition's XML. The signal record's open question about that XML
  applies here too: if definitions become object-gated, these rows narrow with them.
- **No personal-data column.** Nothing declares which variables of an own message are personal.

### 5. Surfaces

- **The Console.** The signal record's section *Signals of this installation* becomes *Signals
  and messages of this installation*, with a channel filter. A message name shows its senders
  and receivers, including the sources that are not models: a watch, a product action.
- **The Modeler.**
  - The message picker groups `process-throw` rows like the other kinds.
  - A sending element shows one line: "received by N deployed processes", or "no deployed
    process waits for it", with a link to the page.
- **MCP.** A tool `atlas_message_names` proxies the grouped route, because its findings are what
  an agent cannot read elsewhere. The tool count in `README.md` moves with it. Message-sources
  itself stays outside MCP, for the reason `mcp/tool_registry_drift_test.go` gives.
- **The handbook.** The section the signal record adds to *Ereignisse / Events* covers messages,
  including the senders that are not models.

### 6. What this record does not decide

- **Refusing an `atlas.*` send by a model outside the system project.** The finding shows such a
  model. Whether its deploy is refused, as a throw of an `atlas.*` signal is warned about today
  in the Modeler, is a rule at deploy with its own record. Such a rule must not stop a server from
  starting with a model deployed before it (`AGENTS.md`, ADR-0177).
- **Publishes through the route.** Who called `POST /api/v1/messages` with which name is a runtime
  fact. It belongs with the runtime counts the signal record defers.
- **Declared message payloads.** As for signals, choosing on the throw which variables a message
  carries is an engine change with its own record.
- **The durable buffer** (ADR-0370). Once it is built, `message.unreceived` must say that a
  message with a TTL waits for a receiver instead of being lost.

### Consequences

- **Positive:**
  - "Is this name fed" counts the installation's own senders. A name a deployed model sends no
    longer reads as unfed.
  - Message event subprocesses are seen as receivers, in message-sources and in ADR-0435's
    listener view alike.
  - A name sent and never received, and a name shared by two projects, become visible.
  - One function answers the picker, the overview and MCP.
- **Negative / trade-offs accepted:**
  - Messages have senders the definitions do not show, so the overview can say "received by
    nobody" but never "sent by nobody".
  - `message.crosses-projects` and `message.reserved-name` name what they cannot stop.
  - Message-sources gains fields, and its rows grow by one kind. A client that assumed three
    kinds has to accept a fourth.
- **Follow-ups / risks to watch:**
  - The deploy rule for `atlas.*` sends (§6).
  - The signal record's open question about definition visibility.
  - Runtime observation of publishes, with the signal record's runtime counts.
  - `message.unreceived` once ADR-0370's buffer is built.

## Pros and cons of the options

### Option 1: status quo
- Good: nothing to build.
- Bad: a name a deployed model sends reads as unfed, and message event subprocesses are missing
  from every view of receivers.

### Option 2: one inventory for both channels
- Good: one route and one shape for the Console.
- Bad:
  - Messages have sources that are not models, filtered by rules signals do not have. One shape
    either carries fields that are empty for signals or loses them for messages.
  - Message-sources would have to stay beside it for the picker. That makes two lists of where a
    message comes from.

### Option 3: complete message-sources, and group it (chosen)
- Good:
  - One function and one set of filters behind the picker, the overview and MCP.
  - Fixes the missing senders and event subprocesses where they already matter: the picker and
    ADR-0435's listener view.
- Bad:
  - Two routes for one list, flat and grouped.
  - Findings stop short of the publish route.

### Option 4: record every publish
- Good: shows what was actually sent, including through the route.
- Bad: the same costs as for signals, a record or counter on the processor path (I1) and
  unbounded cardinality per name. It still says nothing about a name not yet sent.

## Links

- [ADR-draft-the-signals-deployed-models-throw-and-receive](draft-the-signals-deployed-models-throw-and-receive.md):
  the signal half, whose drivers, version rule and Console section this record shares.
- [ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md) §6: message-sources,
  which this record completes.
- [ADR-0435](0435-one-catalogue-of-the-events-atlas-emits.md): the catalogue and the listener
  view, which gain message event subprocesses.
- [ADR-0020](0020-message-correlation.md), [ADR-0035](0035-message-start-events.md),
  [ADR-0094](0094-singleton-message-start.md): correlation and message starts.
- [ADR-0112](0112-send-tasks.md): a send task that names a message is a throw.
- [ADR-0370](0370-durable-message-buffer.md): the buffer that would change `message.unreceived`.
- [ADR-0071](0071-sharing-scopes.md), [ADR-0275](0275-instance-visibility.md): why a message
  crossing projects matters.
