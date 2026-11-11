# ADR-DRAFT: A collaboration is a view over processes that already exist

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-11-11
- **Deciders:** Atlas maintainers
- **Open question:** whether aggregating the observed exchanges from the event log on request
  is cheap enough on the run loop at a real installation's size, or whether the first slice
  already needs a folded, per-edge table (§4). Nobody has measured how many `message.sent` rows
  a 30-day window holds on the maintainer's server.
- **Question checked:** 2026-11

## Context and problem statement

[ADR-0023](0023-collaborations-and-pools.md) made a collaboration deployable by treating it as
**one BPMN file**: every pool's process is in the file, each executable pool becomes its own
definition, and message flows compile to nothing. Everything that knows about a collaboration
today assumes that one file is the collaboration:

- `poolSiblings` (`api/handlers.go`) finds the pools of a collaboration as the deployments whose
  BPMN body is **byte-identical**. The replay of [ADR-0038](0038-collaboration-message-flow-replay.md)
  (`GET /api/v1/collaborations/{key}/runtime`) is built on it.
- Moving or adopting a process moves "the other pools of its collaboration" with it
  ([ADR-0544](0544-artifacts-belong-to-an-application.md)), which again means the other pools
  of the same file.

The models people actually build do not have that shape. An order process, a shipping process and
an invoicing process are owned, versioned and deployed separately, often in different
applications, and they talk to each other by message name. At runtime that works, because
correlation is engine-wide ([ADR-0020](0020-message-correlation.md),
[ADR-0035](0035-message-start-events.md)). What is missing is any **picture** of it:

- No model says that these three processes form one business interaction.
- The replay cannot show them together, because their bodies differ.
- An operator investigating "order 4711 was never shipped" reads one instance's timeline at a
  time and joins them by hand.

Putting all three into one file to get the picture is not an answer. A version is counted per
process id (`s.versions[pid] + 1` in the deploy path), so a pool that appears in two collaboration
files is redeployed from whichever file was deployed last, and the two files silently compete for
one process. A process can only take part in one collaboration that way.

Since ADR-0023, two records have added what a collaboration view needs, without anybody
connecting them to collaborations:

- [ADR-0446](0446-the-messages-deployed-models-send-and-receive.md) derives, **from the
  definitions**, which deployed process sends a message name and which waits for it
  (`CompiledProcess.MessageSenders`, `MessageReceivers`, `GET /api/v1/message-names`). It also
  lists the senders that are not models: the publish route, a Worker's inbound watch, a product
  action.
- [ADR-0472](0472-event-log.md) records, **from the run**, every message that was sent (with the
  sending instance, definition and element), every place it arrived and every instance it
  started, grouped by the log position that caused them, kept for `--event-log-ttl` (30 days by
  default). The `VTMessageFlow` records of ADR-0038 keep sender and receiver instance keys
  without expiry.

The remote case, a participant in another Atlas, is decided by
[ADR-0371](0371-participant-binds-a-published-interface.md) and
[ADR-0373](0373-published-process-interface.md) and is not reopened here. This record answers
the local case: **how Atlas shows a collaboration between processes that are deployed
separately on the same installation, for the modeller who documents it and for the operator who
has to understand what happened between them.**

## Decision drivers

- **Documentation and an operational view, not a contract.** For now the collaboration explains
  the connections; it does not permit, refuse or route anything. Nothing about it may change what
  the engine does.
- **Derive before asking anybody to draw.** Atlas already knows who could exchange a name
  (definitions) and who did (the event log). A view that must be drawn by hand before it shows
  anything repeats what Atlas holds and drifts from it.
- **A process takes part in more than one collaboration.** Whatever represents a collaboration
  must reference a process, not contain it.
- **Local is a white box, external is a black box.** A process deployed on this installation can
  be opened and inspected. An external system or another domain is known only through what
  crosses the boundary: a message name, an inbound watch, a product action, or a published
  interface.
- **Observed is not the same as possible, and neither is the same as intended.** "Nothing was
  seen in 30 days", "nothing could be sent" and "nobody meant this to happen" are three different
  answers and must not render alike.
- **No engine change in the first slice.** Nothing on the processor path, nothing in the WAL,
  nothing in `applyToState` (invariants I1, I2, I4).
- **No role reads more than it already can.** Rows and definitions are filtered as the event log
  and message-sources already filter them ([ADR-0275](0275-instance-visibility.md),
  [ADR-0071](0071-sharing-scopes.md)).

## Considered options

1. **Status quo.** A collaboration is one file (ADR-0023); the lists of ADR-0446 and ADR-0472
   remain lists.
2. **Derived only.** Compute an interaction graph from definitions and the event log, and show
   it. No artifact.
3. **Authored only, in the form the BPMN specification offers.** A separate collaboration file
   that references the processes through `<import>` and `processRef`, with message flows drawn
   between elements of the referenced processes.
4. **Derived graph first, an optional saved collaboration on top.** The graph of option 2 is the
   basis. A modeller may save a collaboration as a document that selects participants, names the
   external parties and fixes the layout. Atlas shows how it differs from what it derives.
5. **Local participants bind published interfaces too.** Use ADR-0371/0373 for local processes
   as well, so that one mechanism covers both cases.

## Decision outcome

Chosen option: **4 — derived graph first, an optional saved collaboration on top**, because it
is the only option in which the view exists without anybody drawing it, a process can take part
in several collaborations, and the difference between possible, observed and intended stays
visible.

### 1. Three layers, never merged

| Layer | Source | Answers | Lifetime |
|---|---|---|---|
| **Possible** | definitions: `MessageSenders` / `MessageReceivers` and the non-model sources of message-sources (ADR-0446) | who *could* exchange a name | as long as the definitions are deployed |
| **Observed** | the event log (ADR-0472): `message.sent`, `message.received`, `message.started`, grouped by source position | who *did* exchange it, how often, when last, and how many sends reached nobody | the event-log window |
| **Intended** | a saved collaboration (§5) | which participants and exchanges a modeller *means* to show | as long as the document is kept |

An edge is drawn with the layers it is in: possible and observed, possible only, observed only
(impossible after a redeploy that removed the element, and worth showing). "Not observed" is
always labelled with the window it refers to. A process that sends once a year is not observed
in a 30-day window, and the view must say "not in the last 30 days", not "never".

### 2. Participants: white box and black box

- **White box.** A process deployed on this installation, referenced by **process id**. It is
  expandable: the `+` shows the newest version's diagram read-only, the way a call activity's `+`
  opens what it calls ([ADR-0245](0245-call-activity-drilldown.md)). A reader who may not view the
  process sees a named pool without contents, as the event log already shows a sending instance's
  key without its data (ADR-0472 §5).
- **Black box.** Everything that is not a local process. It is never expandable, and it is
  described only by what crosses the boundary. Derived black boxes come from the non-model
  sources ADR-0446 already lists:
  - an inbound watch of a Worker, named after the Worker, the external system it watches;
  - a product action ([ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md));
  - the publish route `POST /api/v1/messages`, shown as one participant, "published through the
    API", because the route records no caller identity beyond the role (ADR-0446 §6).

  A saved collaboration may add black boxes no source names (a customer, a bank), and may bind
  one to a published interface once ADR-0371 exists. A participant is a white box or a black box;
  a remote Atlas behind ADR-0371 is a black box, because only its contract is known.

**The words differ from ADR-0374, and this record says so.** ADR-0374 calls the expansion of a
*remote* participant a "white box", although what it shows is a cached contract, not the process.
In this record a white box is a participant whose **process** Atlas can show, and that is only
ever a local one. ADR-0374's expansion is, in these terms, a black box with its interface drawn.
The two records describe the same line; whichever of them is amended next should align the
words.

**No version binding.** A white box references a process id, not a version. A documentation
view has nothing to pin: the possible layer reads the newest active version, as ADR-0446's
findings do, and the observed layer reads the versions that actually ran, which each row already
names. Pinning belongs to a contract, and this record decides none.

### 3. What is derived, and around what

The derived graph is always computed **around a starting point**: a process, or a saved
collaboration. It is never computed for the whole installation, because message names are
engine-wide and the connected component of a busy installation is most of it. Around a process,
the graph holds that process, every participant that sends a name it receives or receives a name
it sends, and the edges between them, to a depth of one by default. The reader may extend it one
step at a time.

Edges are **messages only** in the first slice. A signal is a broadcast without an addressee and
has no BPMN message flow to be drawn as ([ADR-0447](0447-the-signals-deployed-models-throw-and-receive.md)).
A call activity is a parent–child relation, already shown by ADR-0245 and the Panorama mesh
([ADR-0211](0211-panorama-derived-landscape-mesh.md)). Both may be shown as annotations later;
neither is a message flow.

### 4. The observed layer, read on request

The first slice aggregates the event log on request: it reads the rows of the window, groups
each reception under its send by source position (ADR-0472 §3), and counts per
`(sender, receiver, name)` the deliveries, the instances started, the sends that reached nobody,
and the newest timestamp. The read is bounded, as an event-log page already is. A view whose
window holds more rows than the bound answers with what it read and says that it is partial; it
never extrapolates.

If that proves too expensive (the open question above), the follow-up is a folded table of
edges in `applyToState`, keyed by sender definition, receiver definition and name. Its size is
bounded by definitions × names, not by instances, and it is deterministic, because every value it
needs is already in the records. It is an engine change, so it has its own record. It is not
built speculatively.

### 5. The saved collaboration

A saved collaboration is a **design-time document**, filed under an application like every
other artifact (ADR-0544), exported with it ([ADR-0128](0128-process-applications.md)), and
**never deployed**. It is a BPMN `<definitions>` holding one `<collaboration>` and no executable
process, so bpmn-js edits it as it edits any collaboration:

```xml
<bpmn:collaboration id="Collab_OrderToCash">
  <bpmn:participant id="P_Order"    name="Bestellung" atlas:processRef="order" />
  <bpmn:participant id="P_Shipping" name="Versand"    atlas:processRef="shipping" />
  <bpmn:participant id="P_Customer" name="Kunde" />
  <bpmn:messageFlow id="F_1" sourceRef="P_Order" targetRef="P_Shipping"
                    messageRef="Msg_OrderReleased" />
</bpmn:collaboration>
<bpmn:message id="Msg_OrderReleased" name="OrderReleased" />
```

- **`atlas:processRef`** names a local process by id. It is an `atlas:` extension and is
  disclosed as one ([ADR-0176](0176-standards-boundary-and-runtime-contract.md),
  [ADR-0269](0269-atlas-namespace-deploy-warning.md)). The BPMN
  `processRef` is not used, because it must resolve to a `<process>` in the document or in an
  `<import>`ed file, and an Atlas process is identified by id on a server, not by a file. Another
  BPMN tool renders such a participant as an empty pool, which is what it is in that tool.
- **A message flow connects participants, never foreign elements**, and names the message
  through the standard `messageRef`. Which element of a white box sends or receives the name is
  derived (ADR-0446), so a refactoring inside a process does not break the collaboration. This is
  the argument of ADR-0373 against element ids, applied without needing a contract.
- **The document is the intended layer only.** It holds no counts, no versions and no status.
  Everything else is derived when it is opened.

Opening a saved collaboration shows the intended graph with the other two layers laid over it,
and these findings. They are hints, not errors, and they are **not** in the Problems panel, for
the reason ADR-0446 gives: they depend on other deployments, and deploy order is free.

| Finding | When |
|---|---|
| `collaboration.participant-unresolved` | `atlas:processRef` names a process id with no active deployment on this server. On a server the application was exported to, this is the expected state until the process is deployed there, not a broken document. |
| `collaboration.flow-not-sent` | A message flow's source is a white box whose newest version sends no message of that name. |
| `collaboration.flow-not-received` | A message flow's target is a white box whose newest version waits for no message of that name. |
| `collaboration.flow-not-observed` | A drawn flow has no observed delivery in the window. Labelled with the window. |
| `collaboration.undrawn-exchange` | Two participants of the collaboration exchanged, or could exchange, a name no flow shows. |
| `collaboration.reaches-outside` | A name a flow carries is also received by a process that is not a participant. Every match is delivered (ADR-0446), so the collaboration does not describe everything that message does. |

Whether correlation keys of sender and receiver can meet is not computed. ADR-0446 gives the
reason: it is not decidable from definitions.

A saved collaboration may be **proposed** from the derived graph: "save what you see around this
process" writes a document with the current participants and flows, which the modeller then
trims, names and lays out. This is how the dynamic discovery becomes documentation without
anybody drawing from nothing.

### 6. The operational view

The Operations app gains the same graph with the observed layer in front: per edge the
deliveries, instances started, sends that reached nobody, and the newest timestamp, in the
chosen window. Sends that reached nobody are the case the event log was built for (ADR-0472),
and here they are attached to the exchange where they belong.

Selecting a **correlation key** narrows the observed layer to the rows carrying it, which turns
the graph into the path of one business case across processes: which instances took part, in
which order, and where it stopped. A correlation key is not a conversation identifier. Two
unrelated exchanges may share one, and the view must say "rows with key 4711", not "the
conversation 4711". The conversation identifier of [ADR-0369](0369-cross-instance-message-addressing.md)
answers that question across nodes once it exists; this record does not anticipate it.

The replay of ADR-0038 is not replaced. For a collaboration in one file it keeps working as it
does. Extending its transport to a saved collaboration, whose pools come from the document rather
than from byte-identical bodies, is a follow-up: the message-flow records it needs are kept per
receiving definition already, whichever file the definition came from.

### 7. What this record does not change

- **ADR-0023 stands.** A collaboration in one file deploys and runs as before, and its pools
  appear in the derived graph like any other deployed process.
- **No engine change.** The first slice reads definitions, the event log and design-time
  documents. Nothing is written to the WAL, nothing is folded in `applyToState`, and the
  processor path is untouched.
- **No contract.** A saved collaboration permits, refuses and routes nothing. It does not take
  part in deploy. If it is later meant to be checked on deploy, that is a new record, and the
  vocabulary here (`atlas:processRef`, flows by `messageRef`) is chosen so that such a record can
  build on it.

### 8. Naming

"Collaboration" is already taken twice in this tree: `api/collab` and the MCP `atlas_join_session`
family are the live co-editing of [ADR-0140](0140-live-collaborative-modeling-sessions.md), and
`/api/v1/collaborations/{key}/runtime` is the replay of ADR-0038, keyed by the first pool's
definition key. The service of this record therefore does not live in `api/collab`. Its routes
are a separate decision of the first slice; whatever they are called, they must not reuse the
replay's `{key}` for a document id, because a definition key and a document id are different
spaces.

### Consequences

- **Positive:**
  - The connections between separately deployed processes become visible without anybody
    drawing them, and stay correct when the processes change.
  - A process can take part in several collaborations, because a collaboration references it and
    does not contain it.
  - An operator sees per exchange what was sent, what arrived and what reached nobody, and can
    follow one business case across processes by its correlation key.
  - The modeller's intent, once saved, is compared with what Atlas derives. The drift becomes a
    finding instead of an outdated drawing.
  - White box and black box are defined by what Atlas can inspect, which is the same line
    ADR-0371 draws for the remote case.
- **Negative / trade-offs accepted:**
  - The observed layer is only as long as the event-log window and only as wide as one
    partition (ADR-0472 §7). Rare exchanges are invisible in it. This is why the possible layer
    is always shown beside it.
  - `atlas:processRef` makes a saved collaboration meaningful only to Atlas. Another tool shows
    empty pools and flows between them, which is correct but thin.
  - Three layers are more to read than one drawing. Their rendering has to keep them apart
    without overwhelming the view; that is design work of the first slice.
  - The derived graph exposes the engine-wide coupling of message names that ADR-0446 names
    (`message.crosses-projects`). Making it visible is the point, but it will surface couplings
    that nobody intended.
- **Follow-ups / risks to watch:**
  - Measure the aggregation of §4 at the maintainer's installation before deciding on the folded
    edge table.
  - The replay transport for saved collaborations (§6).
  - Signals and call activities as annotations on the graph (§3).
  - Binding a black box to a published interface, once ADR-0371 is built.
  - Where a saved collaboration is stored: as a draft kind beside the process drafts of
    [ADR-0021](0021-diagram-drafts.md), which would give it the editor, co-editing and version
    history ([ADR-0031](0031-diagram-version-history.md)) for free but must keep Deploy away from
    it, or in its own design-time store. Not decided here.

## Pros and cons of the options

### Option 1 — status quo
- Good: nothing to build. A collaboration in one file is atomic, replays, and needs no
  resolution. Everyone who wants a picture can put the pools into one file.
- Bad: a process can be in one such file only, because a second file competes for its versions.
  Processes owned by different applications cannot share a file without one application
  deploying the other's process. The lists of ADR-0446 and ADR-0472 answer questions per name and
  per row, never per collaboration.

### Option 2 — derived only
- Good: the strongest form of "do not store what can be derived". No document to drift, no
  store, no editor work. It is exactly the principle Panorama follows (ADR-0211: a projection,
  never a stored document), and it answers the question "could collaborations be found
  dynamically" with yes.
- Bad: a derived graph has no boundary. Message names are engine-wide, so "everything connected
  to the order process" grows into the installation. It cannot name an external party that no
  source identifies (the customer), cannot keep a layout an operator recognises, and cannot
  express that an exchange is *meant* to exist, so a missing one is never a finding. Option 4
  keeps all of option 2 and adds only what derivation cannot provide.

### Option 3 — authored, with `<import>` and element-level message flows
- Good: closest to the letter of the BPMN specification. A separate collaboration file that
  imports the process files is what the specification allows, and it needs no `atlas:` extension
  on the participant.
- Bad: `<import>` locates a file, and an Atlas process is identified by an id on a server, so the
  location would be an Atlas convention dressed as a standard; another tool could not resolve it
  either, which makes the portability nominal. Message flows to foreign element ids break when the
  publisher refactors, the reason ADR-0373 exists. It derives nothing, so it is wrong the day a
  process changes. To the authors' knowledge, bpmn-js renders one definitions document and does not
  resolve imported processes into a diagram, so the rendering would be Atlas's own work anyway.

### Option 4 — derived graph first, optional saved collaboration (chosen)
- Good: useful before anything is saved; a process may take part in many collaborations; intent
  and reality are compared; the vocabulary can later carry a contract without being redrawn.
- Bad: two mechanisms (derivation and document) to build and keep consistent; an `atlas:`
  extension; the observed layer is bounded by the event-log window.

### Option 5 — local participants bind published interfaces
- Good: one mechanism for local and remote. Every exchange gets a contract, a version and a
  stable entry-point name, and a local participant can move to another node without being
  redrawn.
- Bad: it forces every process that is talked to locally to publish a contract, with grants and
  contract versions, in order to be *documented*. Within one installation, and often within one
  application, the modeller may read the process itself, so the contract hides nothing the reader
  could not see. It turns a documentation need into a governance step. The door stays open: a
  black box may bind an interface (§2), and a local process that is later moved to another domain
  changes from `atlas:processRef` to ADR-0371's `atlas:interfaceRef` on one participant.

## Links

- narrows the use of ADR-0023 (collaborations and pools) for processes deployed separately;
  ADR-0023 itself stands
- builds on ADR-0446 (possible layer) and ADR-0472 (observed layer)
- extends the view of ADR-0038 (collaboration message-flow replay) as a follow-up
- the local counterpart of ADR-0371, ADR-0373 and ADR-0374, which decide the remote case
- applies the derive-then-annotate principle of ADR-0211 and ADR-0189
- respects ADR-0544 (artifacts belong to an application), ADR-0071 and ADR-0275 (visibility)
- extension disclosure per ADR-0176 and ADR-0269
- leaves the conversation identifier to ADR-0369
- naming distinct from ADR-0140 (live collaborative modeling sessions)
