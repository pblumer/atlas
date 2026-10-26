# ADR-0495: A release's architecture is drawn in layers, from what Atlas already holds

- **Status:** Accepted (2026-10-26; amended 2026-10-26 — the banded layout lands with the
  first slice that has a band to draw, slice C; amended again with slice B — a Worker's
  owner and call budget are not recorded, versions rise with each slice, and list bounds
  are set in slice C; see the notes below)
- **Implementation:** Partial
- **Date:** 2026-10-26
- **Deciders:** Atlas maintainers
- **Open question:** §4 assumes the starmap's renderer (`api/web/panorama-mesh.js`, fed
  by `GET /api/v1/panorama/mesh`) can take a release snapshot as a second source
  without being forked. Nothing in this repository shows a source abstraction there
  today; the lens is planned with a spike as its first step, and §4 is revisited if
  the spike says the renderer cannot carry a second source.
- **Question checked:** 2026-10

> **Implementation status and amendment (2026-10-26): slice A has landed.** Every read of a
> release's architecture takes `layers=` (`archsketch.ParseSelection`): the application
> layer is the snapshot's logical part, the technology layer its physical part, and
> strategy, business and data are accepted names that no snapshot records yet. Without
> the technology layer a snapshot is drawn as the logical view; without the application
> layer it is drawn as the installation alone — the Atlas system software, its API, each
> worker process and each connection Atlas opens itself, with the system it reaches — in
> SVG and ArchiMate. A public start link, an application interface that only the
> installation records, is drawn only when both layers are shown. A narrowed picture
> names the layers it shows, those switched off, and those asked for but not recorded;
> an unknown layer is a 400, and a release that records none of the layers asked for a
> 404 that says which it records. The default selection renders the bytes it rendered
> before. The Console's architecture dialog offers one checkbox per recorded layer, and
> `atlas_release_architecture` takes `layers`.
>
> **Amendment:** §3's banded layout is not built in slice A. With only the application
> and technology layers holding anything, there is no band to draw, and a layout with
> nothing in it would be code no test can exercise. It lands with slice C, the first
> slice that records a new layer (data); §7 is read with that move.

> **Implementation status and amendment (2026-10-26): slice B has landed.** A snapshot
> records, per configured Worker the release names, the rest of its configuration that is
> not a secret: the mail provider, the malware scanner's endpoint, the model an agent
> Worker asks, the identity mode and enforcement (ADR-0475), and whether a credential
> reference is set, never its name. It also records the enabled inbound subscriptions
> (ADR-0205) that publish a message the release receives: the Worker and its Worker Type,
> what the subscription watches in that kind's terms (a clio subject, a JQL query, a
> spreadsheet and range, a folder, a channel, a mail folder), and the Worker's endpoint as
> scheme, host and port. The SVG writes the configuration on a fourth line of the Worker's
> box and the subscription under the message it feeds; the installation alone draws each
> subscription as a Path Atlas opens. The ArchiMate document adds the mailbox and the
> scanner as further target Nodes, and each subscription as a Path from the Atlas node to
> the system it reads from, associated with the application interface of its message.
> A reader who may not view the Worker's configuration sees the Worker named and the
> detail marked withheld (§5).
>
> **Amendment:**
>
> - **A Worker's owner is not recorded.** The owner is an Atlas account. §5 already keeps
>   Atlas usernames out of the record for capability owners, and the same reason holds
>   here: a release record is never rewritten, so an account written into it could not be
>   taken out again when the person leaves or ownership moves.
> - **The outbound-call budget is not recorded.** §1 assumed a budget per Worker kind.
>   ADR-0149 set one budget for every Worker (`nettimeout.Default`, 10 seconds), so
>   recording it per Worker would repeat one installation constant on every box. It is
>   recorded once a per-kind budget exists.
> - **Versions rise with each slice that records something new.** §6 named version 4 for
>   the whole record. Slice B takes 4; slice C takes 5, slice D 6. A snapshot of an
>   earlier version renders byte for byte as before; this includes a version-3 mailbox
>   endpoint, which stays on the third line and does not become a target system.
> - **List bounds are set in slice C.** Slice B's only new list, the inbound
>   subscriptions, is limited by the messages the release receives and the subscriptions
>   configured for them. The first lists that grow with the size of the models (data
>   objects, information-model classes) arrive in slice C, and §6's bounds are measured
>   and set there.

## Context and problem statement

[ADR-0471](0471-release-architecture-sketch.md) gave every release an architecture
snapshot, taken in the same write as the release record. After S1, S2 and S2b it
records two parts:

- **the logical part:** the messages the release receives, the workers and job types
  it names, the local decisions it evaluates, and the calls it makes into processes
  of other applications;
- **the physical part:** the installation and its environment, the external URL, the
  public start links, and for each worker its placement, its endpoint (configured or
  reported) and the worker instances seen serving it.

It is drawn as one static picture in three columns (ways in, the application on its
node, dependencies), and written as an ArchiMate document. A reader can switch
nothing on or off, and cannot follow an element any further.

Atlas holds a good deal more that bears on how a release meets its environment and
why the release exists. Every item below is a record or a compiled fact in this
repository today:

| Topic | What Atlas holds | Where |
|---|---|---|
| Capabilities | capability records with business owner, state, declared KPIs and SLAs, and realizations by a process (`applicationKey` + `processId`) or by a Worker (`workerRef`) | [ADR-0305](0305-business-capabilities-and-value-streams.md), `api/capability` |
| Value streams | value streams whose stages name the capabilities they use | ADR-0305, `capability.Stage.Capabilities` |
| Products and services | catalogue products whose `provisionProcess`, `deprovisionProcess` or `lifecycleProcess` is a deployed process, and the actions a product declares | [ADR-0396](0396-catalogue-on-the-starmap.md), [ADR-0425](0425-a-product-lifecycle-is-one-process-with-message-triggers.md), [ADR-0429](0429-product-actions-are-commands-with-published-outcomes.md), `api/catalog` |
| Human roles | user-task assignment (literal candidate groups and assignees, or a FEEL expression) and BPMN lanes | [ADR-0045](0045-user-task-assignment-bound-to-identity.md), [ADR-0121](0121-bpmn-lanes.md), `compiler` |
| Data | typed BPMN data objects with states and read/write associations per activity, and named data stores | [ADR-0053](0053-first-class-data-objects.md), ADR-0058, ADR-0059, ADR-0259, `CompiledProcess.DataObjects`, `DataStores` |
| Information model | UML classes above the data objects, derived from the processes that use them | [ADR-0230](0230-process-information-model.md), [ADR-0301](0301-derive-the-model-from-the-processes.md), [ADR-0338](0338-where-a-business-object-is-used.md), `api/infomodel` |
| Personal data | the variables a process declares personal and its data-subject variable, and which compiled expressions a worker evaluates (where personal values may be read in plaintext) | [ADR-0314](0314-portal-personal-data.md), `CompiledProcess.PersonalVariables`, `compiler/personal.go` |
| Message coupling | which deployed models send and receive which message and signal names, across applications | [ADR-0446](0446-the-messages-deployed-models-send-and-receive.md), [ADR-0435](0435-one-catalogue-of-the-events-atlas-emits.md) |
| Inbound events | inbound subscriptions that turn an external system's event (a worker, a watched subject) into an Atlas message | [ADR-0205](0205-connector-ownership-and-event-delivery.md), `api/inboundsubstore.go` |
| Outbound events | the event feed pushed to a CloudEvents endpoint | [ADR-0433](0433-the-event-feed-is-pushed-to-a-cloudevents-endpoint.md) |
| Worker detail | per configured Worker: mail provider, IMAP mailbox endpoint, malware scanner endpoint, the model an agent Worker asks, the identity policy (mode, enforcement), whether a credential reference is set, owner and sharing, and the outbound-call budget of its kind | `api/connectorstore.go`, [ADR-0255](0255-agent-models-are-console-workers.md), [ADR-0475](0475-identity-aware-worker-execution.md), [ADR-0149](0149-bounded-connector-call-budget.md) |
| Runtime | instance counts, incidents, circuit-breaker state, worker liveness | Panorama's observation states ([ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md) §6), the Workers view |

Atlas does **not** hold, and this record does not invent:

- where a release was promoted to: deployment targets exist, but no record says
  which release went to which target;
- network zones, firewalls or data volumes;
- the name of the system behind an external endpoint, beyond its host and the
  Worker's own name.

Panorama's starmap ([ADR-0211](0211-panorama-derived-landscape-mesh.md),
[ADR-0396](0396-catalogue-on-the-starmap.md)) already offers an interactive, derived
landscape: element kinds can be hidden, a drilldown follows a path over a number of
hops, views are saved, and the narrowed picture exports as ArchiMate. Its drilldown
ends in views Atlas already has. But it shows the installation **as it is now**, not
a release **as it was recorded**.

The question: how does a release's sketch gain the strategy, business and data
layers and richer technology detail, with layers a reader switches on and off and a
drilldown, without building a second interactive graph renderer, and without
turning a release record into something that changes after the fact?

## Decision drivers

- **A release record is a record.** What it says is what held when it was taken
  (ADR-0471 §1), and its renderings stay deterministic.
- **Every element is a fact Atlas can point at.** That is the starmap's test
  (ADR-0211, ADR-0396), and it holds here too. Nothing is drawn because it usually
  exists.
- **One interactive renderer, not two.** The starmap's hiding, drilldown, saved
  views and export are built and tested; a second implementation would drift.
- **Disclosure stays per reader.** The sketch's existing read-time redaction
  (ADR-0471 §3, §5) extends to every new kind. A catalogue's audience (ADR-0347,
  ADR-0396) and a Worker's sharing (ADR-0205) hold here as they hold everywhere.
  Personal data appears only as a declaration, never as a value (ADR-0314).
- **The static, embeddable picture stays.** SVG, ArchiMate XML and JSON remain, and a
  layer selection reaches them too.
- **A picture says what it leaves out.** Caps are stated, not silent (ADR-0211 §7);
  the stamp names the selected layers and what was cut.
- **The invariants stay untouched.** The snapshot is design-time sidecar data, taken
  on the run loop at publish and import; nothing enters the WAL or `applyToState`.
- **The ArchiMate document stays valid.** It passes `panorama.Validate`. Panorama's
  authoring subset already includes Capability, Value Stream, Business Role,
  Business Process, Business Service, Business Object, Application Interface and
  Data Object, and every relationship between subset types passes
  `panorama.MayConnect`. Product, Business Event and Application Event are valid
  ArchiMate 3.2 but outside that subset. They are written the way ADR-0471 §3 already
  writes Path: valid in a derived document, refused by `MayConnect` only as *out of
  subset*, and tested as exactly that.

## Considered options

1. **Leave the sketch as it is,** and send readers to the starmap for layers and
   drilldown.
2. **Build an interactive layered viewer for the sketch,** with its own renderer in
   the Console.
3. **Extend the snapshot by layers; give the static renderings a layer selection;
   offer show/hide and drilldown by opening the snapshot in the starmap as a
   "release lens".**
4. As option 3, but **resolve the strategy, business and data layers live at read
   time** instead of recording them in the snapshot.

## Decision outcome

Chosen option: **3, "layers in the snapshot, a layer selection in the renderings,
and a release lens in the starmap"**. It is the only option that keeps the release a
record, adds the layers people asked for, and reuses the one interactive renderer
Atlas has.

### 1. The layers and what fills them

Each recorded element carries one layer. The content of each layer is restricted to
what is tied to the release, as the last column states.

| Layer | Content | ArchiMate | Tie to the release |
|---|---|---|---|
| Strategy | capabilities realized by a release process or by a Worker the release names, with owner (name and role only), state, confirmation date, and declared KPIs and SLAs as declared; the value streams and stages that use those capabilities | Capability, Value Stream; realization from the process or Worker to the Capability; serving from the Capability to the Value Stream | a realization naming a release process, or a Worker the release names |
| Business | the release's BPMN processes as business processes; products whose provision, deprovision or lifecycle process is a release process, with the actions they declare; human roles from user-task assignment and lanes | Business Process, Product, Business Service, Business Event, Business Role; assignment from the role to the process | a product binding naming a release process; an assignment or lane inside a release process |
| Application | today's logical part, plus the forms people use (start and task forms), the other applications that send the messages this release receives or receive the messages and signals it throws, and the event feed | Application Component, Application Interface, Application Service, Application Event; flow and triggering to other Application Components | an element of a release process; a name this release receives or throws |
| Data | data objects with their states and read/write per activity; data stores; the information-model classes the release uses; which variables are declared personal, and which Workers receive personal values in the expressions they evaluate | Data Object, Business Object; access (read/write); realization from Data Object to Business Object; a property `atlas.personal` | an association, store or declaration inside a release process |
| Technology | today's physical part, plus per Worker: its Worker Type, mail provider, IMAP and malware-scanner endpoints, the model an agent Worker asks, identity mode and enforcement, whether a credential is configured, owner, and its kind's outbound-call budget; and the inbound subscriptions that bring an external system's events in as messages the release receives | Node, System Software, Path, Technology Interface; the scanner and mailbox as further target Nodes | a Worker the release names; a subscription publishing a message the release receives |

A FEEL-authored name (a candidate group, a Worker name) is recorded as *decided at
run time*, as ADR-0471 already does for Worker names.

"Data" is a band in the picture, not an ArchiMate layer: the ArchiMate document puts
Business Objects in the business layer and Data Objects in the application layer, as
the standard does.

**Runtime is not recorded.** Instance counts, incidents, breaker states and worker
liveness change by the second. They appear only in the lens (§4), as ADR-0189's
observation states, and never in the release record.

### 2. Recorded at snapshot time; drift shown only live

Every layer's content is recorded with its keys and names as they were when the
snapshot was taken.

The strongest case against this is option 4. Capabilities, value streams and
products are maintained independently of releases; a recorded copy starts going
stale the moment somebody confirms a capability or rebinds a product, and ADR-0308
itself says to carry the identifier and resolve the rest at read time.

That rule is right for a Panorama view, which is a living document. A release record
answers a different question: *what was this release part of when it shipped?* A
record that silently changes cannot answer it, and its picture would stop being
deterministic. So the lens (§4) does both: it shows the release as recorded, and can
mark drift against today's registers (a capability deprecated since, a product now
bound to another process) without writing anything back.

### 3. A layer selection in the static renderings

- The JSON, SVG and XML routes take `layers=` with any of `strategy`, `business`,
  `application`, `data`, `technology`.
- **The default is `application,technology`,** which is today's picture. An embed
  or a reader who chooses nothing sees what they saw before.
- **Layout:** horizontal bands from top to bottom (strategy, business, application,
  data, technology). The application band keeps today's three columns.
- **Caps:** each band is capped. What does not fit is counted in the stamp's list of
  what is not shown; the JSON is always complete within the snapshot's own bounds.
- **Stamp:** it names the selected layers, so a picture with layers switched off
  says so.
- **No script and no links in the SVG.** A picture embedded with `<img>` ignores
  links, and a script is what the sandbox CSP exists to refuse. Drilldown belongs to
  the lens.
- **Console:** the architecture dialog gets one checkbox per layer. The preview and
  both downloads follow the selection.

### 4. The release lens in Panorama

- The architecture dialog gains **Open in Panorama**. It opens the starmap with the
  release snapshot as its source: nodes and edges mapped from the snapshot, using the
  starmap's kinds where they exist (application, process, worker, job type,
  decision, catalogue, product) and new kinds for the rest (capability, value
  stream, role, data object, business object, external endpoint).
- **Show and hide** are the starmap's hidden kinds, grouped by layer.
- **Drilldown** is the starmap's path drill. It ends where it ends today: at the
  element in the Modeler, the capability record, the product, the Worker.
- **Saved views and export** work as they do today.
- **Header:** the lens says *Release vN of <application>, as recorded <time>*, so it
  is never mistaken for the live landscape.
- **Live overlay:** optional. It adds the observation states and the drift markings
  of §2.

This is the part the open question is about. The first step is a spike that tries
the second source in `panorama-mesh.js`. If the renderer cannot carry it without a
fork, this section is revisited before any lens code lands.

### 5. Disclosure

The sketch keeps ADR-0471's rule: the snapshot records what the installation held,
and a read withholds what the reader may not see, saying that it withheld it.

| What | Who sees it | Everyone else sees |
|---|---|---|
| endpoints, provider, mailbox and scanner endpoints, agent model, identity mode, credential presence, inbound subscription subjects | readers who may view that Worker's configuration (ADR-0205) | the Worker and its Worker Type, marked *withheld* |
| worker instances, runtime overlay | operators | a count |
| products and their actions | readers within the catalogue's audience, by the check the starmap applies (ADR-0396) | a count of withheld products |
| another application's name in message coupling | readers who may see that application | *another application*, the starmap's restricted placeholder |
| capability owner | application readers: name and role. Contact details and the Atlas username are never recorded | – |
| personal data | application readers: the declaration (which variables, which Workers receive them). Values never reach the snapshot | – |

### 6. Versions and size

- **Versions:** the format and mapping versions become 4. A version-1 to version-3
  snapshot renders as before, with only the layers it has.
- **Bounds:** every recorded list is bounded per kind, with the overflow recorded as
  a count. The bounds are set in the first slice, from measurements on real
  applications, not guessed here.
- **Lists:** the release list already leaves the snapshot out (ADR-0471), so a larger
  snapshot does not slow the history view.

### 7. Slices

| Slice | Content |
|---|---|
| A | layer tag on every element; `layers=` on all routes; the Console checkboxes. Existing content is tagged application or technology, so nothing changes by default (the banded layout moved to C; see the amendment) |
| B | technology depth: Worker detail and inbound subscriptions |
| C | the banded layout; the data layer: data objects, stores, information-model classes, personal-data declarations and the Workers that receive personal values |
| D | the business and strategy layers: business processes, roles, products and actions, capabilities, value streams |
| E | cross-application coupling: senders and receivers of message and signal names, the event feed |
| F | the release lens: spike first (open question), then the lens with show/hide, drilldown, live overlay and drift |

Each slice ships its own CHANGELOG entry, handbook section, tests, and an amendment
note in this record.

### Consequences

- **Positive:**
  - A release documents itself from strategy to technology, from facts Atlas already
    holds, with nothing drawn by hand.
  - The data layer answers a data-protection question no other view answers today:
    which personal data a release declares, and which external systems receive it.
  - One interactive renderer serves both the live landscape and a release.
  - Existing embeds and readers see the same picture as before.
- **Negative / trade-offs accepted:**
  - The snapshot grows, and the snapshot code gains five new sources, each with its
    own redaction rule.
  - A recorded business layer is stale relative to today's registers by design; the
    drift view is the answer, not a refresh of the record.
  - The banded static layout is denser than today's three columns and relies on caps.
  - The data layer shows declared personal data only. An undeclared personal value
    is invisible here, as it is to the crypto-shredding of ADR-0314.
- **Follow-ups / risks to watch:**
  - The open question (a second source for the starmap) decides the shape of slice F.
  - If embeddable links are adopted (discussed separately), they render at the
    anonymous disclosure level of §5, and `layers=` becomes a link parameter.
  - Recording which release went to which deployment target would close the one gap
    in the technology layer that this record names.

## Pros and cons of the options

### Option 1: leave the sketch, use the starmap
- Good: no new code.
- Bad: the starmap shows the installation now, so a reader cannot see a release as it
  shipped, and nothing in it is versioned or embeddable per release.

### Option 2: an own interactive viewer
- Good: complete freedom for the release view.
- Bad: a second graph renderer with its own hiding, drilldown, export and tests, which
  will drift from the starmap. Of everything in this record, it is the most code for
  the least gain.

### Option 3: layers in the snapshot, selection in the renderings, a lens in the starmap
- Good: the release stays a record; layers reach the embeddable picture; drilldown
  reuses tested code; disclosure follows existing rules.
- Bad: depends on the starmap taking a second source (open question); the snapshot
  grows.

### Option 4: option 3 with live business and data layers
- Good: the business layer is never stale.
- Bad: the release record changes after the fact and its renderings stop being
  deterministic; "what was this release part of when it shipped" can no longer be
  answered. The drift view of §2 gives the live answer without this cost.

## Links

- extends [ADR-0471](0471-release-architecture-sketch.md) (the release snapshot)
- relates to [ADR-0189](0189-panorama-architecture-modeling-and-live-overlays.md),
  [ADR-0211](0211-panorama-derived-landscape-mesh.md),
  [ADR-0396](0396-catalogue-on-the-starmap.md) (the starmap),
  [ADR-0308](0308-panorama-binds-the-capability-register.md) (capability bindings)
- relates to ADR-0053, ADR-0230, ADR-0301, ADR-0314 (data and personal data)
- relates to ADR-0205, ADR-0255, ADR-0475, ADR-0149 (Worker detail and disclosure)
- relates to ADR-0435, ADR-0446, ADR-0433 (messages and events)
