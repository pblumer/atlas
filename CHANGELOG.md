# Changelog

All notable changes to Atlas are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project aims to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

While Atlas is pre-1.0 (`0.y.z`), the public API — the HTTP surface, the MCP
tools, the on-disk WAL/state format, and the Go package layout — is **unstable
and may change in any release**. Breaking changes are called out under
_Changed_ / _Removed_ for each version.

## [Unreleased]

### Added

- **A class's attributes can be put in the order you want to read them in.** Grab a row
  by its grip and move it, or press Alt+↑ / Alt+↓ in the field you are editing. An
  «enumeration»'s literals move the same way.

  The order is not a view setting, which is why it is a model edit and not a sort: a
  class box reads top to bottom, so which attribute comes first is a statement about the
  class — and a business key belongs where a reader looks for it, not wherever it
  happened to be added. `attributes` and `literals` were already ordered arrays in the
  document, so nothing about the stored model changes; what was missing was any way to
  say the order.

  Only the grip starts a drag. Were the whole row draggable, selecting a word inside an
  attribute's name would drag the attribute instead of the text. Moving a row leaves the
  business key alone: it names attributes, and an attribute keeps its name wherever it
  sits.

- **Importing a UML class diagram: reading what somebody else drew.** A data model is
  normally drawn in a UML tool — Enterprise Architect, Papyrus, Visual Paradigm — long
  before anybody opens Atlas, and until now the only way to get it in was to retype it
  class by class. What that loses is never the class names: it is the **business key**,
  the one fact BPMN has no equivalent for and the one every cross-process capability
  rests on. **Data › Information model** now has an **Import** button, and
  `POST /api/v1/infomodel/import` behind it
  ([ADR-0232](docs/adr/0232-uml-model-import.md), extending
  [ADR-0230](docs/adr/0230-process-information-model.md)).

  Two documents are read, and the format is detected from the document itself — a UML
  tool writes `.uml`, `.xmi` and `.xml` for the same file, so the extension says
  nothing. **XMI 2.5.1** is what a UML tool exports: classes, data types and
  enumerations become the three stereotypes, an `ownedAttribute` that is an association
  end belongs to its association rather than being stated twice, bounds become the four
  multiplicities the subset has, `isID` becomes the business key, and a composite end
  is read as the *whole* — which is what the diamond marks. **Atlas's own JSON** is the
  other: exactly what `GET /api/v1/infomodel/models/{id}` hands out, so a model moves
  between applications and installations through the document the API already gives
  you.

  ADR-0230 said XMI was "an export, not an interchange … until it is tested". What
  makes it safe now is that **nothing is dropped silently**. An import goes through the
  same subset the canvas writes through, and everything the subset has no place for —
  an interface, an operation, an n-ary association, a multiplicity of `0..5`, a
  generalization that closes a cycle — comes back as a note naming the element, at one
  of three levels: *dropped* (not in the model), *adjusted* (in the model, saying
  something slightly different) or *info* (nothing lost, worth knowing — a flattened
  package, a generated layout). The Import dialog shows that report **before** anything
  is stored, from the same call that does the storing (`dryRun`), so what the preview
  promises is what lands.

  Two readings are deliberate rather than literal. An identifier whose multiplicity the
  document never states is read as **required**: the document does say something about
  that member — it identifies the instance — and reading it as optional would throw the
  business key away. And a document with no geometry is **laid out on a grid**, because
  XMI keeps the picture in a file of its own and a stack of boxes at the origin is not
  a diagram; the note says so, since what was lost is the arrangement, not the model.

  The same import is an MCP tool (`atlas_import_information_model`), so an agent can
  bring a vocabulary in before authoring against it.

- **A variable named after one of your data objects is now flagged.** The new rule
  `variable.shadows-data-object` raises a warning where the two collide: draw a data
  object `Kunde`, have a task write its result into a variable `Kunde` — or read the
  object back into a variable of the same name — and the diagram shows one thing while
  the instance holds two.

  They are not two views of one value. A data object carries the declared type, the data
  state and the recorded history of every write; a variable carries a value. They live in
  separate records, are written by separate events, and **writing one never changes the
  other** — a data association *copies*, evaluating its expression once. And only one of
  them answers to the name: FEEL is bound from the variables alone, so `Kunde` in a
  condition, a mapping or a connector payload always means the variable, even in a model
  whose whole point is the object. The two then drift apart under one name, and every
  expression quietly means just one of them.

  A warning rather than a refusal: it is legal, and wanting one name for one idea is
  reasonable. The panel says which write collides and why it matters; renaming either
  side clears it.

- **Web scraping: one row is one record, and the fetch survives the real web**
  ([ADR-0231](docs/adr/0231-webscrape-structured-extraction.md)).
  An `<atlas:webscrapeConnector>` now takes `<atlas:scrapeField>` children. With at
  least one, the selector picks **items** rather than values and every match becomes
  an object carrying the named fields — title *and* link off the same row, instead of
  two tasks returning two arrays that a following script re-zips by index (and pairs
  wrongly from the first item that has no link). With no fields the task is exactly
  what ADR-0118 described: an array of strings.

  Two switches and three fewer failures come with it:

  - `absoluteLinks="true"` (HTML) resolves `href`/`src` reads against the URL the page
    was actually served from — a relative path is not something a process can open,
    mail, or store.
  - `plainText="true"` (RSS/Atom) strips the markup from a feed entry's `description`,
    where that text is put in front of a person.
  - Feed entries additionally carry `guid`, `author`, `categories` and `image`. `guid`
    is the identity a daily run deduplicates on; a title cannot do that, because
    publishers edit titles.
  - **Character sets:** a feed declaring `encoding="ISO-8859-1"` used to fail outright
    (`Decoder.CharsetReader is nil`), and a Latin-1 page arrived in the process as
    mojibake. Both are now decoded as the document declares.
  - **RSS 1.0/RDF** (`<rdf:RDF>`, `dc:date`, `dc:creator`) is read under
    `format="rss"`, and `&nbsp;` or a bare `&` no longer rejects a document every
    reader renders.
  - **Identity and bounds:** requests carry their own `User-Agent` instead of Go's
    anonymous default, which a large share of sites answer with 403; a document past
    32 MiB fails the job instead of being truncated into a plausible half-result. And
    fetching an HTML page as a feed now says *which setting* to change, rather than
    reporting an XML syntax error at line 1.

  The Modeler offers all three: field rows (name / selector / attribute), both
  switches, and it clears the other mode's settings on a switch instead of leaving a
  model the compiler rightly rejects.

- **Example: capturing mortgage rates daily**
  ([`examples/hypothekarzinsen-migrosbank.bpmn`](examples/hypothekarzinsen-migrosbank.bpmn)).
  A timer start (`0 6 * * *`), a three-field scrape of Migros Bank's rate table, a
  gateway for the day the selector stops matching, and a still-simulated step that
  files the rows. It also shows how to address *the* table when six of them share a
  CSS class: by what it contains (`table:has(th:contains('…'))`), not by where it sits.

- **Data stores: saying where a class is kept.** BPMN has a `<dataStoreReference>` —
  the box on the diagram meaning "this outlives the process" — and says nothing about
  what it holds or what keeps it. Atlas did not even parse it. It does now, and a
  **data store is declared in the application's information model**
  ([ADR-0230](docs/adr/0230-process-information-model.md),
  slice 5b): once per application, named by every process that reaches it, with the
  class it holds and the Worker that keeps it.

  It sits *beside* the classes rather than inside one, and that is the point: an Order
  is an Order wherever it is kept, so the class stays storage-agnostic and only the
  store says where. The class canvas draws it as a cylinder with a dashed line to the
  class it holds — an annotation, not an association, because nothing in the model
  *relates* those two.

  A store may only hold a **business object with a business key**. A process reads
  from one by naming which thing it wants, and the key is the only thing that names
  one — so a store over a class with no identity could be filled and never read, and
  is refused. A value type has no existence of its own to keep.

  A deploy resolves what a process claims: a store the application does not declare,
  and a store no Worker backs, are both **warnings**. A diagram is routinely drawn
  before the store it names is modeled, and a store is modeled before somebody
  configures the Worker behind it — different days' work, and neither is a reason to
  refuse a deploy.

  The mode is **read**. Writing through a store is refused as out of subset and says
  so: it is a transaction against something outside the engine, whose durability
  guarantee stops at its own log, and that is a decision of its own.

- **Which instances are carrying this order?** The question BPMN structurally cannot
  express now has an answer: **Data › Instances** groups every data object by the type
  its model declares and then by its **business key**, so one order appearing in three
  processes reads as one datum with three instances under it
  ([ADR-0230](docs/adr/0230-process-information-model.md),
  slice 5a). `GET /api/v1/data-objects` (and `atlas_data_objects` over MCP) takes a
  `class` and a `key`, and with `history=true` sweeps finished instances too.

  Including history costs a longer walk and **nothing on disk**: a finished instance
  keeps its data objects until it is purged, and purging is opt-in
  ([ADR-0115](docs/adr/0115-history-retention-hard-delete.md)). That is why this is a sweep rather
  than a durable index. A durable one would have to live in `applyToState`, know what a
  business key is — the engine reads integer indices, and the key lives in the
  information model, which is design-time state it must not read — and be swept by a
  purge that deletes by instance-key prefix and could not reach it. Each of those is
  answerable, and none is worth answering before a sweep starts to hurt.

  So the sweep says what it did: how many instances it examined, and whether a bound
  stopped it before the end. An array cannot say whether it is complete, and a caller
  that cannot tell will read a partial answer as a whole one — which is the worst
  thing an index can do. The response is an object for that reason.

  The object diagram's one admission of its own edge — *"this customer is not in this
  instance; it lives in another instance or in a data store"* — now links straight into
  the index, which is the thing that can find it.

- **An instance's data, drawn as objects.** The Data tab in the Operations replay
  now switches between a list and an **object diagram**
  ([ADR-0230](docs/adr/0230-process-information-model.md),
  slice 4): each data object as a UML object node — `order : Order`, its name
  underlined the way the notation marks an instance — with the attributes its class
  declares, in the class's own order, its business key marked, and a member the value
  does not carry shown as *absent* rather than blank. "Not set" and "set to empty"
  are different facts about a datum.

  Two things become a line, and the diagram tells them apart because they are
  different claims. A **part inside its whole's value** is read off the value itself
  — a composition, drawn with the filled diamond. A **business key one object holds
  for another** is an inference from two values agreeing, drawn dashed. This is what
  the business key is *for*: without an identity there is nothing to match on and the
  line simply cannot be drawn.

  A reference that matches nothing in this instance is **stated rather than dropped**.
  It is not a fault — it is the edge of what one instance can see: the Customer lives
  in another instance, or in a data store. Saying so is what makes the picture
  trustworthy, and it is exactly the boundary a worker-backed data store removes.

  The graph is derived on the server (`GET /api/v1/instances/{key}/object-graph`, and
  `atlas_instance_object_graph` over MCP) for the same reason the authoring subset is
  served rather than duplicated: the rules for what relates to what are model
  semantics, and a second copy of them in the browser is a second place for them to
  be wrong. The browser gets nodes and lines to draw. An application that models
  nothing still gets its objects drawn, and the graph says it is showing less than it
  could rather than showing nothing.

- **A data object's declared type now means something.** BPMN's `itemSubjectRef` —
  the slot where a data object says what kind of thing it is — resolves against the
  owning application's information model, at deploy and in the Modeler's Problems
  panel ([ADR-0230](docs/adr/0230-process-information-model.md),
  slice 3). Three findings follow from it: a type nothing models, a write targeting a
  member the class has no attribute for, and the one
  [ADR-0053](docs/adr/0053-first-class-data-objects.md) named as the whole point of
  having a type — *"task Approve reads `order`, and nothing upstream produces it"*.

  The member check walks dotted paths (`customer.name`, ADR-0060's named follow-up)
  and refuses one that would cross a primitive or an enumeration, which has no
  members to write inside. The read-order check needs no information model at all —
  it is reachability over the compiled graph — and is deliberately conservative: a
  loop whose writer precedes its reader is not flagged, while an activity reading
  what it will only write on its own completion is. **None of these refuses a
  deploy.** A model is routinely drawn before the vocabulary it names exists, exactly
  as it is deployed before its connectors do (ADR-0158).

  The Modeler gained the **Type** field this slot always needed, suggesting the
  application's modeled classes while still accepting one that is not modeled yet.
  Adding it uncovered a silent data-loss bug and fixes it: `itemSubjectRef` is a
  *reference* in the bpmn moddle, not a string, and a reference the moddle cannot
  resolve is dropped on export — so a model carrying the shorthand
  `itemSubjectRef="Order"` came back **untyped** after being opened and saved. Missing
  declarations are now repaired on import and written as proper `<itemDefinition>`
  elements, which the compiler resolves through; the shorthand keeps working
  everywhere it already did, and a class name that is not a valid XML id (`Line item`)
  becomes expressible for the first time.

  The information model is also an MCP surface now, because an agent authoring BPMN
  meets this gap first: `atlas_infomodel_subset` states the rules before it writes,
  and the model can be listed, read, created, saved, projected to a JSON Schema and
  deleted. `atlas_data_objects` answers the question from the other side — which
  running instances are carrying an Order right now.

- **Atlas can now say what the data in a process actually *is*.** A new top-level
  **Data** area holds a **process information model**: a UML class-diagram subset,
  owned by a process application and shared by every process in it
  ([ADR-0230](docs/adr/0230-process-information-model.md),
  slice 2). A class has typed attributes with multiplicities, documentation, and a
  **business key** — the part BPMN has no equivalent for, and the fact that makes
  `Order#ORD-1` the same order in three processes. Three stereotypes carry the
  meaning: a **business object** is something you can point at and identify, a
  **value type** is a structured value with no existence of its own, an
  **enumeration** is a closed set of literals.

  This exists because BPMN structurally cannot say it. A `<dataObject>` is scoped to
  one process definition, and its `itemSubjectRef` points at a type the specification
  deliberately leaves opaque — "some other schema language". Atlas has parsed that
  attribute all along and had nothing to resolve it against. Now it does.

  **The rules are served, not duplicated.** Which relationships may run between which
  kinds of class is one table, sent to the browser: the canvas refuses a line while it
  is being dragged with the same matrix the server refuses it on write, and in the
  server's words. A refusal says which of two things it is — *out of subset* (UML
  allows it, this build does not author it) or *the notation says no* (an enumeration
  is not something a relationship can point at) — because a modeler acts on those
  differently. A model that does not validate is refused with its findings rather than
  stored, so nothing downstream ever meets a half-model.

  **The JSON Schema projection** derives the contract a *value* of a class is checked
  against, and states what it could not carry. A JSON document is a tree and a class
  model is a graph: composition — a whole that owns parts that die with it — becomes
  containment, and a plain association between two things that exist separately does
  not. It says so rather than quietly emitting a schema that describes less than you
  think.

  **Data › Instances** is the same subject one altitude down — every data object the
  running instances carry, grouped by the type their model declares, marking which of
  those types is actually modeled and which is still just a string. Next: resolving
  `itemSubjectRef` against the model at deploy time, so a task that writes
  `order.amount` into a class with no such attribute is a finding in the Problems
  panel rather than a surprise at runtime.

- **A process instance now shows the data it carries, and where each value came
  from.** The Operations replay gained a **Data** tab beside Variables and Decisions:
  every BPMN data object the instance holds, with the class the model declared it to
  be (`itemSubjectRef`), whether it is a collection, the lifecycle state it is in
  (`received` → `approved`), and its current value. Each row opens into the object's
  **state trail** — every durable write, the state it moved the object into, when, and
  **which element on the diagram made it**
  ([ADR-0230](docs/adr/0230-process-information-model.md),
  slice 1). Atlas has recorded a data object's every transition since data objects
  became first class ([ADR-0053](docs/adr/0053-first-class-data-objects.md)); until
  now nothing read that history back, so the one thing that distinguishes a data
  object from a variable — that it has a life, not just a current value — was
  invisible.

  "Which element wrote this" is newly recorded rather than guessed. A data object's
  event now carries the element instance that produced it, stamped where every write
  already funnels through — the same answer variables got in
  [ADR-0219](docs/adr/0219-variable-write-attribution.md), for the same reason: on a
  parallel fork, both branches sit inside each other's window, so a diff of two
  snapshots credits both writes to both branches. It is an **appended field**, so
  instances already on disk keep reading and simply report their writes as
  unattributed rather than naming an element that may be the wrong one.

  This is the first slice of a larger answer to process data. BPMN can say what a
  datum is called inside one process definition and nothing more: `itemSubjectRef`
  points at a type the specification deliberately leaves opaque, and two processes'
  `order` are unrelated strings. The record above proposes filling that slot with a
  **UML class-diagram subset** owned by a process application — a shared vocabulary
  above BPMN's per-definition scope, so a data object has a declared type across
  processes, the Problems panel can check data flow against it, and a data store can
  say which class it holds and which Worker backs it.

- **See who is signed in right now.** The user list under Organization gains a
  **Presence** column: *online* for somebody who did something in the last five minutes,
  *idle* for a session that is open but untouched, *offline* for an account no browser is
  reporting for. Until now the list could only say whether an account was *enabled* — the
  same answer for the person reading the screen this second and for the one who left in
  March ([ADR-0228](docs/adr/0228-user-presence.md)).

  The distinction that makes it worth having is between the last two states, and neither
  falls out of "when did a request last arrive". The Console polls on its own, so a tab
  parked behind another window would look busy forever; and a session outlives the laptop
  that was closed on it by up to twelve hours. So the browser reports two separate things
  once a minute — that the tab is still open, and whether anybody actually touched it —
  and the column reads both, asking the first question first: a session that has stopped
  reporting is *offline* whatever it was doing five minutes ago.

  **Nothing is stored.** Presence is read from the live sessions and from nothing else: no
  record, no event, nothing in a backup, and no way to ask it about yesterday. A restart
  shows nobody, which is not a gap — after a restart nobody *is* signed in. It is
  deliberately coarse (three states, never which page or which action), administrators
  only, the same reach as the user list it annotates. The column refreshes itself every
  thirty seconds without reloading the page, so a half-typed user form survives it.

- **An inbound event watch has an hourly budget.** A watch reads a foreign system and
  publishes what it finds; every event can start a process, and that process can write
  back to the system the watch reads. When it writes something the watch's own query
  matches, the loop closes and has no natural end — and nothing looks broken from inside:
  every instance is well-formed, every task succeeds, every message is delivered exactly
  once. Only the *rate* tells a loop from a busy morning
  ([ADR-0225](docs/adr/0225-inbound-watch-budget.md)).

  Each watch now carries **Max events/hour** — 60 when it names none, so the protection
  is the default rather than something to remember. A batch that would cross the ceiling
  is refused whole and the watch switches itself off, saying so in the Console in words
  that name the number to raise. The resume cursor stays put, so nothing is lost:
  enabling the watch again re-reads what was refused, with a fresh window.

  It is deliberately cause-agnostic, because the loop the engine fix below closed is not
  the only shape: two processes can build one between them with no single model being
  wrong.

- **See what a mockup run asked the database.** Operations → **Mock database** shows one
  card per SQL worker in mockup mode: every statement it was asked, in order, with the
  values the process bound — and the ones with **no prepared answer** in red, with the
  reason. Those are the entries an operator comes for: reading one and pasting it into
  the answers under Workers is how a seed gets built, and until now that meant scrolling
  the worker's log past everything else it did
  ([ADR-0224](docs/adr/0224-sql-mock-journal.md)).

  The shape is the mock directory's ([ADR-0213](docs/adr/0213-ad-mock-directory-in-the-console.md)):
  the worker snapshots its own journal and posts it, because a worker may sit in a
  network the server cannot dial back into. It is bounded at the crossing, keeps the
  newest when the bound bites, and is memory on both sides — gone on restart, in no event
  and no backup. A report that cannot be delivered is logged and dropped, and the next
  statement re-sends the whole journal, so a Console that was unreachable catches up by
  itself.

  **Two things differ from the directory view, and both are deliberate.** There is no
  table to browse: this mockup answers statements and executes none, so an `INSERT` does
  not change what a later `SELECT` returns — there is no "now" to draw, only the
  sequence. And ADR-0213 could promise that no password travels, because the mock
  directory stores none; **this one cannot**. A journal entry carries the values a
  process bound, and a process under test binds whatever it binds — a password hash on
  its way into a table is a bound parameter like any other, and nothing can tell it from
  an id. So the read is admin-only, a worker is handed the report address only while the
  mockup is on, and none of it is durable. Values render as JSON, because a string `"7"`
  and the number `7` being distinguishable is frequently the answer to "why did that
  lookup find nobody".

- **An example where a Jira ticket starts the process.** `examples/jira-ticket-eingang/`
  is the Zugangsantrag's other direction: instead of Atlas writing to Jira, Jira starts
  Atlas. A message start event waits on `jira.ticket.created`, an event watch under
  Console → Connectors → Events publishes every issue its JQL finds under that name, and
  the instance begins with no form at all — `issueKey`, `projectKey`, `summary`,
  `reporter` and the whole issue arrive from the event. It then walks the chain the
  account lookup exists for: `reporter` is a display *name*, Jira assigns to an
  `accountId`, so `search-users` sits between them and `assign-issue` after, reading
  `=konten[1].accountId`. The gateway's default is the branch **without** an assignment,
  because an empty result means three different things Jira does not distinguish. The
  examples index gained a row for it and for `jira-zugangsantrag/`, which had none.

- **The database mockup is a switch in the Console.** [ADR-0221](docs/adr/0221-sql-mock-mode.md)
  shipped mockup mode as environment variables on a worker, and named a Console switch as
  the follow-up. This is it: **Workers → Databases** has a checkbox and a field for the
  prepared answers, and saving restarts the supervised SQL workers holding it. Atlas keeps
  running, and no deployment change is involved — which was the whole complaint, because a
  variable set once at start is the wrong ceremony for a thing you flip while trying a
  process out.

  Three rules come straight from the Active Directory switch it copies. A stored "off" and
  no record at all are **different states**: without one, whatever the server was started
  with keeps deciding, so an existing install works exactly as it did until somebody
  touches the switch. The answers are **content, not a path** — the Console is org-wide and
  a path typed there belongs to whichever host runs the worker, so Atlas stores the JSON
  and writes the file itself, named by a digest of its own content so that replacing a seed
  actually reaches the worker. And the seed is **parsed on save**, so a typo is refused at
  the form with its own complaint rather than discovered as a mockup that quietly answers
  nothing.

  One switch covers all three products, as the AD one covers all directories: simulating
  SQL Server while really writing to PostgreSQL would look like a full mockup run, which is
  the one thing it must never look like.

  Two rules that were right for a real database had to move, or the switch would have been
  a checkbox with nothing behind it. A worker record with **no secret** is now handed to
  the worker while the mockup is on — normally its name is withheld, because a name with no
  DSN behind it is what a SQL worker refuses to start on, and in mockup mode that would
  leave the mockup with no name a task can address. And **creating** a database worker no
  longer demands a connection string while the mockup is on: that is a credential for a
  database nobody will dial, and it is exactly the state an operator is in when they turn
  the mockup on *because* they have no database. A connection string given anyway is still
  sealed and kept, so turning the mockup off is not a re-typing exercise.

- **A message start event says whether anything feeds it.** A model names a message and
  nothing else — what publishes it is an operational fact, so the same process can be
  started by a Jira watch, a clio subscription, a `POST /api/v1/messages` or another
  process's send task, and swapping one for another is a Console change rather than a
  redeploy. The cost of that seam was that a name typed one character differently in the
  model and in Console → Connectors → Events is two working halves that never meet: no
  error anywhere, and a process that simply never starts. The Modeler now reads
  `GET /api/v1/message-sources` and says, under the message name, which inbound watches
  publish it — the connector, its kind, the JQL or subject it follows, and whether it is
  switched off — or that none does, with where one is configured. It reports; it does not
  bind: the model still names no source. A watch's query is configuration, so it is shown
  only to a caller with viewer access to that connector; that a name is fed at all is
  answered to any modeller, like the connector picker's own listing.

- **A Jira task can look an account up.** An eighth Jira operation, `search-users`, turns
  what a process knows about a person — an address, a name — into the `accountId` Jira
  assigns an issue to ([ADR-0223](docs/adr/0223-jira-account-lookup.md)).
  The term travels as `query` on Cloud and `username` on Data Center, decided by the
  connector's own credential rather than by the model, and an optional project restricts
  the search to the accounts that project can actually assign — the ones a later
  `assign-issue` will not be refused for. The matched accounts land in the result variable
  as a JSON array, so an assign reads `=konten[1].accountId` (FEEL lists are 1-based).
  Before this, a model could only hard-code an opaque per-site id or call Jira through the
  REST connector with a second copy of the credential.

- **A database task can be tried without a database.** The SQL Worker Types — MS SQL
  Server, MariaDB, PostgreSQL — were the only ones that could not be exercised at all
  without the production dependency, and the database a process reads is the HR system
  or the ERP, which is exactly the one nobody wants a model under development pointed
  at. A worker started with `ATLAS_MSSQL_MOCK=1` (or `ATLAS_MARIADB_MOCK`,
  `ATLAS_POSTGRES_MOCK`) now answers that product's statements from **seeded answers**
  in its own memory, with no connection string
  ([ADR-0221](docs/adr/0221-sql-mock-mode.md)). Nothing in the model
  changes — the same worker name, the same statement, the same parameters; what differs
  is which worker leases the jobs, so a model that runs through in mockup mode is the
  model that later runs in production.

  `ATLAS_MSSQL_MOCK_SEED` names a JSON file mapping statements to their rows or
  affected count. An answer that names `params` or `named` applies only to that
  binding and one that names neither is the statement's fallback, which is how a seed
  says "person 42 exists and nobody else does" with no SQL engine behind it. An answer
  may also seed a **failure**, because a unique-key violation on a redelivered create
  is not an edge case in an identity process — it is what the delivery guarantee
  routinely does, and now it can be triggered on purpose.

  **It answers statements, it does not execute them,** and the one rule that makes it
  worth trusting is the refusal: a statement nobody seeded **fails, naming itself and
  its bound parameters**, and never comes back as an empty result set. "No rows" is a
  business answer — the lookup found nobody — and a mockup that invents one hands the
  process a fact in the direction hardest to notice, because the run looks like it
  worked. What that costs is stated too: an `INSERT` does not change what a later
  `SELECT` returns. Standing in for SQL Server with SQLite or a hand-written SQL subset
  was rejected for the same reason from the other side — either would differ from the
  real thing exactly where statements go wrong, and land the difference in production.

  [`examples/mssql-eintrittsmeldung.bpmn`](examples/mssql-eintrittsmeldung.bpmn) is the
  model to try it on: it looks a person up by their initials (`query-one`, which returns
  `null` rather than an empty list and fails on a second match), branches on whether
  they exist, and sets them active (`execute`, bound by name). Its documentation says
  *why the statement is literal* — the `statement` field is the one connector value with
  no `fx` toggle, because a statement assembled from process data would be an injection
  that needs no quoting bug. The handbook's *Test & simulate* chapter gained the matching
  section, and the worker-type table now says what a SQL task actually does.

- **The Console can check a database connector.** `POST /api/v1/connectors/test`
  covered mail and nothing else, and the gap hurt most for the kind whose *whole*
  configuration is one opaque string sealed into the vault: a typo in it is invisible
  from the moment it is saved, and the first thing to read it is a supervised worker
  whose failure arrives as a parked task. The **Test connection** button now appears
  for the three SQL kinds — against the string typed in the create form, and against
  the vault reference of a saved connector, which is the case that matters because an
  operator opens that dialog when something *stopped* working
  ([ADR-0220](docs/adr/0220-checking-a-database-connector.md)).

  The verdict says what it proved: *"Connected to `sa@db.example.com:1433/hr` and
  authenticated. No statement was run, so this does not prove the login may read or
  write the tables a task names."* Reading "OK" as "the task will work" is how the
  next surprise arrives as an incident.

  The engine still links no database driver: `api` declares the seam and `cmd/atlas`,
  which links both, joins them. A server built without one answers "this server cannot
  check a database connection", which is the truth rather than a connector reported
  broken.

- **A worker's database pool has limits.** `database/sql`'s defaults are an unlimited
  number of open connections and connections kept until the process exits — the two a
  database administrator would not have chosen. A worker now caps them, and recycles
  connections so that the first job after a quiet night does not meet one a firewall or
  a failover closed hours ago.

- **A Jira issue can start a process.** A Jira connector now carries inbound event
  watches beside its outbound operations
  ([ADR-0214](docs/adr/0214-jira-inbound-issue-watch.md)): Console → Connectors →
  Events takes a JQL and a message name, and every issue the query matches is
  published as an Atlas message, so a message-start process runs per new ticket and a
  waiting instance is woken. Atlas polls, so nothing has to reach the server from the
  internet. A new watch is forward-only — pointing one at a project with a long
  history does not start a process per old ticket — and the correlation key and the
  started instance see `issueKey`, `projectKey`, `issueType`, `summary`, `status`,
  `reporter` and the whole issue. A watch on changed rather than new issues is the
  same watch with its cursor field moved to `updated`. The bridge that carries this
  is no longer clio's alone: what a source *is* moved behind an interface, and an
  existing clio subscription keeps its resume position and its idempotency mark
  byte-for-byte.

- **The jira kind can be supervised.** `atlas --supervise-connector jira` refused to
  start the *server* — `KnownConnectorKinds` is a hand-written list beside the switch
  that is the real implementation, and the jira case was added without it, so the kind
  could be served by a worker started by hand and never by one Atlas supervises. Which
  is the path the Workers view shows, so it looked like a worker that does not exist.
  A test now holds the list to the switch in both directions.

- **Mock any REST API from its OpenAPI document.** `atlas mock-openapi --spec
  petstore.yaml` serves the paths a document describes, so a process with a REST
  connector task can be run end to end before the API it calls exists — or without
  pointing a draft at the real one
  ([ADR-0217](docs/adr/0217-openapi-mock-server.md)).

  Where `atlas mock-remedy` hand-implements the endpoints of one Worker Type, this
  serves whatever a document describes, and only the URL's host changes in the model.
  A response is the document's own `example` where it states one, the first of its
  named `examples` where it states those, and otherwise a value generated from the
  schema — the same bytes on every run, so a demo is reproducible and a test can assert
  on a body. A caller asks for a stated error path with `Prefer: code=404` or picks a
  named example with `Prefer: example=rex`.

  It refuses rather than improvises: a `$ref` it cannot resolve fails at startup in the
  terminal that launched it, and a status the document does not describe is a 400 that
  names what is on offer — a test written against the 404 path must not quietly pass on
  a 200. A response the document describes only in a media type this mock cannot
  generate (XML, say) loses its body rather than being answered with JSON under an XML
  label, and the startup banner names each one. What it does *not* do is validate: a
  request body is recorded, never checked, and nothing is stateful.

  **A document published as a tree of files is read as one.** That is how most large
  APIs ship — DigitalOcean's is a single entry file of references into a hundred others
  — and each reference resolves against the directory of the file it is written in, so a
  path item two directories down reaches its schemas the way its author meant. What may
  be read is bounded on purpose: this mock serves what it reads and authenticates
  nobody, so references may not climb out of the document's own directory unless
  `--spec-root` says they may, and a `$ref` to a URL is refused.

  `GET /__mock/calls` is the journal of what a run actually did — method, path,
  operation, status, the `X-Request-ID` a job carries, and the body it sent — and
  `GET /__mock/report` is that journal in the envelope the Console's Mockups view takes
  ([ADR-0216](docs/adr/0216-mockups-are-one-view.md)), so this
  mockup reports where the others are moving rather than becoming a fourth place to
  look.

- **See the mock Active Directory.** An AD worker running in mockup mode now reports
  the forests it holds, and Operations › **Mock directory** shows them: one card
  per worker, one tree per LDAP URL, every entry with its attributes, and the operation
  journal underneath
  ([ADR-0213](docs/adr/0213-ad-mock-directory-in-the-console.md)).

  It closes the gap that made the *starting entries* on the Active Directory card look
  like the directory. They are not: the seed is where every forest begins and is never
  written back, so an account a joiner created was nowhere on screen — it had to be read
  out of the worker's log, one line per operation. Now the account is in the view, and
  the page says which of the two is which.

  Nothing else changes. The report is an observation of work that already happened: a
  worker that cannot reach its Atlas logs `ad_mock.report_failed` and completes its job
  exactly as before, and no password travels, because the mock stores none. Both halves
  stay memory — restarting the worker empties its forests, restarting Atlas empties the
  view. A worker Atlas supervises is pointed at the endpoint automatically while the
  mockup is on; one you run yourself takes `ATLAS_AD_MOCK_VIEW_URL`.

- **Atlas can terminate TLS itself.** `--tls-cert` and `--tls-key` turn `--addr`
  into a TLS 1.3 listener, so the reverse proxy that used to be mandatory before
  anyone outside the host could reach the server is now a choice
  ([ADR-0191](docs/adr/0191-built-in-tls-listener.md)). Unset — both unset — is
  today's behaviour exactly: plain HTTP, nothing changes on upgrade.

  **Both files or neither.** Naming one without the other stops the server rather
  than falling back to plaintext on the port you believed you had just secured.
  The pair is re-read when either file changes, so a renewal needs no restart of a
  stateful engine; a renewal caught half-written keeps serving the certificate it
  has and logs `server.tls_reload_failed` rather than refusing handshakes.

  **TLS 1.3 only, and that is the point.** Its cipher suites are fixed by the
  protocol, so there is no cipher list to expose, nothing to weaken, and no
  `--tls-min-version`. A client that cannot negotiate 1.3 is refused rather than
  quietly downgraded.

  With TLS on, the server also opens a plaintext listener on `127.0.0.1` with an
  ephemeral port for its own children — the MCP adapter's loopback calls and any
  worker it supervises. A certificate issued for a host name carries no name for
  `127.0.0.1`, and the alternative would be a switch to skip verification, which
  Atlas does not have and will not get.

- **`--tls-ca`, so two Atlas servers with an internal CA can talk.** A deployment
  target must be `https://` ([ADR-0129](docs/adr/0129-remote-deployment-targets.md)),
  and on-prem that certificate usually comes from a CA the sending host has never
  heard of. Point `--tls-ca` at its PEM bundle on the publishing server, and at the
  same bundle on an `atlas worker --server https://…` running on another host or an
  `atlas mcp --server https://…` an agent drives it through. It is *added* to the
  host's roots, never a replacement, and it reaches only Atlas calling Atlas — a
  Worker Type calling a third party keeps the host's trust store, because that
  endpoint is somebody else's. There is no switch to skip verification anywhere,
  and a bundle that cannot be read stops the process at startup rather than
  failing every call later.

- **Helm: `atlas.tls`.** Mount a `kubernetes.io/tls` Secret (what cert-manager
  writes) and the chart passes the pair to the server and switches all three
  probes to the HTTPS scheme. Off by default, because in a cluster the Ingress
  usually holds the certificate.

  **What TLS still does not cover:** `/metrics`, `/healthz` and `/readyz` are
  unauthenticated by design, so a port reachable beyond the host still wants a
  proxy in front of those paths — encryption is not authorization.

- **The Active Directory Worker Type can search.** A new `search` operation answers
  "is this group there, and what is its distinguished name?" —
  ([ADR-0166](docs/adr/0166-active-directory-connector.md), fifth amendment). It takes
  a base DN, a scope (`base`/`one`/`sub`, default `sub`), an RFC 4515 filter and an
  entry cap, and writes `{found, count, dn, entries}` into its result variable.

  It exists because a membership change cannot be modelled without it: `add-group-member`
  addresses the group by its distinguished name, and a distinguished name is a position
  in a tree rather than something a requester supplies. Until now the answer was to bind
  the generic LDAP connector to the same directory just to ask — a second connector and
  a second place to configure it, in the middle of one lifecycle. `found` is what a
  gateway branches on (`=gruppe.found`), and `dn` is what the next task addresses
  (`=gruppe.dn`); entries come back DN-sorted, so a redelivered job writes the same
  answer. **Finding nothing completes the task with `found=false`** — checking whether
  an entry exists is the point, so "no" is an answer rather than an incident. Exceeding
  the cap fails instead of truncating, and every search is paged, so a domain
  controller's size limit does not refuse a legitimate one.

  New: [`examples/ad-gruppenzuweisung.bpmn`](examples/ad-gruppenzuweisung.bpmn) — find
  the account, find the group (create it if it is not there), assign. The AD mockup
  answers a search too, so the whole model runs without a domain controller.

- **Sign in with your identity provider.** Atlas can now be an OpenID Connect
  relying party: people reach the login screen, press **Sign in with …**, and come
  back with a session ([ADR-0210](docs/adr/0210-federated-authentication.md)).
  Point it at any compliant provider with `ATLAS_OIDC_ISSUER`,
  `ATLAS_OIDC_CLIENT_ID` and `ATLAS_OIDC_CLIENT_SECRET`, and register
  `<external-url>/auth/oidc/callback` as the redirect URI.

  **Nothing changes unless you configure one.** With no issuer set, no route is
  mounted, nothing is fetched from anybody, and the local password is the only way
  in — which is what every installation has today.

  A first sign-in creates an account linked to the provider's *subject*, not the
  email address. The local password form stays, deliberately: a provider that is
  unreachable must not lock an administrator out of their own instance.

- **Let the provider's groups decide roles.** Under **Organization → Single
  sign-on** an administrator names one claim in the token and a list of exact
  values it may carry, and each value names the Atlas roles it grants and the
  groups it puts a person in. Onboarding and offboarding become a group membership
  somebody already maintains: the role and the shared projects arrive at the next
  sign-in and go away at the sign-in after the membership does.

  **It is off until you turn it on**, and worth one decision before you do: while
  it is on, whoever administers those groups administers this instance's roles, and
  a role granted by hand is replaced at that person's next sign-in. Group
  membership follows only for the groups your rules name — a group no rule mentions
  is left alone, so a membership you added by hand there survives.

  Nothing is granted by absence: somebody the provider says nothing about matches
  no rule and holds `user`, which everybody who can sign in has either way. A rule
  that could never work — a role Atlas does not enforce, a group that has been
  deleted — is refused when you save it rather than ignored on every login.

- **The Jira Worker Type runs on a worker.** Jira was in `offloadableKinds` while
  `worker/connectors.go` had no case for it, so `--offload-connectors jira` stripped
  the in-process handler and left the job type served by nobody. Now the engine
  resolves a Jira task into plain values and hands them over, and
  `atlas worker --connector jira` performs it, holding the site URL and the Atlassian
  credential in its own environment (`ATLAS_JIRA_CONNECTORS` plus per name `_URL` and
  either `_EMAIL`/`_API_TOKEN` or `_TOKEN`) — so a worker can operate as an account the
  engine has never held. A supervised worker is handed all of it at spawn out of the
  connector store and the vault, in the one credential shape the engine itself would
  have used. The in-process handler stays as the fallback and now shares `jira.Run`
  with the worker, so the two cannot drift
  ([ADR-0168](docs/adr/0168-connector-work-on-a-worker.md),
  [ADR-0201](docs/adr/0201-jira-connector.md)).


### Changed

- **Everything a person reads now says Worker.** The Console's *Connectors* page was
  renamed to *Workers* by an adapter that rewrote the rendered DOM after the fact
  ([ADR-0203](docs/adr/0203-worker-execution-model.md), slice 2). It patched the
  handful of strings it knew about and only on that one route, so the old word stayed
  everywhere it had not been told about: the row menu offered *Configure connector*,
  a delete asked "Delete connector?", an incident said the model named a connector,
  and a deploy refused a model with `ad connector task … needs a connector`.

  The adapter is gone. `#/console/workers` is a real route the Console renders itself
  (the old `#/console/connectors` redirects to it, so a bookmark still lands), and the
  vocabulary is now the source's own — in the Console, the Modeler, the handbook,
  `atlas --help`, the OpenAPI summaries, every deploy and incident message, the
  example models and the comments in this tree. A **Worker Type** is a capability, a
  **Worker** is one configured target of it, and a **Worker Instance** is a process
  leasing its jobs.

  **Nothing on the wire moved.** A deployed model, a running worker and a scripted
  deployment all keep working unchanged: the `connector="…"` BPMN attribute and the
  `atlas:*Connector` extension elements, the `connector/` package paths, `atlas worker
  --connector`, `--offload-connectors` / `--in-process-connectors` /
  `--supervise-connector`, the `ATLAS_*_CONNECTORS` and `ATLAS_CONNECTOR_<REF>_TOKEN`
  variables, the `connectors` field a worker registration carries, and the
  `/api/v1/connectors` routes. Renaming those is a separate slice with its own
  compatibility window
  ([the migration plan](docs/architecture/worker-execution-migration.md), slice 6).

  Three words that read the same are somebody else's and were left alone: BPMN's
  **off-page connector** (link events), an AI client's **custom connector** (MCP), and
  Microsoft Identity Manager's **connector** in the MIM comparison. Records written
  before ADR-0203 — the decision records, and the release notes above — keep their
  wording, because they are dated accounts of what was true when they were written.

- **The Modeler's bar carries two buttons now, and a menu for the rest.** It ended with
  seven, added one at a time as the editor grew — Token simulation, Variables,
  Auto-layout, Save, Export XML, Documentation, Deploy — and every one of them was the
  same white button. That said they were the same size of decision, which they never
  were: Auto-layout nudges boxes, Deploy puts a definition on a server and cannot be
  taken back ([ADR-0229](docs/adr/0229-modeler-bar-hierarchy.md)).

  The bar now carries **Save** and **Deploy**, with Deploy the only filled button because
  it is the only act there that leaves the browser. The other five moved into a **…**
  menu beside them, grouped by what they touch: *View* (Token simulation, Variables) and
  *Diagram* (Auto-layout, Export XML, Documentation). A toggle in a menu cannot look
  held down, so it says it is on with a check and with `aria-pressed` — the Variables
  toggle never announced its state at all before.

  Two things that were not about any single button go with it. The bar is one row again
  at the widths people work at: it wraps, and the buttons were direct children of it, so
  a narrower window used to drop two or three of them into a second ragged row rather
  than shorten anything. And the Playground tab no longer shows **▶ Token simulation**
  directly above **▶ Run** — a drawn walkthrough with no engine
  ([ADR-0078](docs/adr/0078-design-view-token-simulation.md)) one row above a real
  sandboxed one ([ADR-0215](docs/adr/0215-modeler-playground.md)), same triangle, two
  entirely different things.

  One control could not simply move. Token simulation is not a command but a mode: while
  it is on, the diagram is played rather than edited and the modeling palette is hidden.
  Its control bar — the one that appears with the mode — now carries **Exit simulation**,
  so leaving is one visible click rather than a trip back through the menu.

  Nothing about what the controls *do* changed, and F8 still runs Auto-layout from
  wherever focus sits. The cost is honest and worth naming: five controls are a click
  further away, and someone opening the Modeler for the first time cannot see that they
  exist until they open the menu.

- **The three database Worker Types are one capability, and now say so everywhere.** MS
  SQL Server, MariaDB and PostgreSQL differ in a driver name, a placeholder syntax and —
  for SQL Server alone — the ability to bind a parameter by name
  ([ADR-0173](docs/adr/0173-generic-sql-connector.md)); in everything an operator or an
  author does with them they are the same kind. The engine, the compiler and the worker
  already served all three from one code path, but the two Console surfaces and the
  environment vocabulary did not, and both had drifted. The Worker catalog card told
  only SQL Server's reader that Atlas supervises the worker for it, only PostgreSQL's
  about the row cap, and none of the three that a database task can now be tried without
  a database at all — so which facts an operator learned depended on which of the three
  they clicked. The Modeler's properties panel repeated the same nine fields three
  times. Both are now built from one description per surface, and two guards keep them
  that way. The environment variables a SQL worker reads
  (`ATLAS_<PRODUCT>_CONNECTORS`, `_<NAME>_DSN`, `_MOCK`, `_MOCK_SEED`) are spelled once,
  by the product itself, instead of being assembled in the engine and in the worker
  separately — the same argument `connector/envname` already won for connector names.
  Nothing an operator sets or a model states changed; SQL Server is now called
  *Microsoft SQL Server* on the Workers page and in the kind picker, which is what the
  Modeler and the Worker Type registry already called it.

- **The Workers list reads like a list again.** Console → Workers gave every configured
  worker up to seven action buttons and spelled out every deployed process that resolves
  through it, so a shared mail worker drew fourteen wrapped lines of links and the rows
  were all different heights — the tallest being the ones something is wrong with
  ([ADR-0163 amendment](docs/adr/0163-deleting-a-referenced-connector.md)). The actions
  are now behind the row's **⋯** menu, where every other table in the console keeps
  them, and the usage is a count — *Used by 8 processes · 21 deployed versions · 1
  running instance* — that opens the list it stands for. That dialog groups a redeployed
  model under one entry with its versions newest-first, links each version to its
  Operations page beside the tasks that resolve through the worker, and filters past a
  handful of processes. Nothing about the numbers changed: they are read off the same
  `usedBy` the delete refusal names, so the row, the dialog and the refusal still cannot
  tell different stories, and typing a process name in the column filter still finds the
  workers it runs through. A worker with no endpoint of its own — a Gmail or Graph
  mailbox, which authenticates as its sender — now names its provider there instead of
  opening the line with a stray separator.

- **Jira runs on a worker by default.** `DefaultOffloadedKinds` now includes `jira`, so a
  Jira task is leased by a supervised worker instead of served inside the engine
  ([ADR-0218](docs/adr/0218-jira-default-offload.md)). The engine
  hands that worker the site URL and the vault credential at spawn, so nothing needs
  configuring — the same handover mail and Remedy get. It appears in Operations → Workers
  as a process with a pid, a log and a restart button, which is where an operator looks
  for it; served in-process it was a folded-away row in Job types, visible only when
  something was wrong with it. **This changes behaviour on upgrade**: the work moves out
  of the engine on the next start, and a Jira site reachable from the engine must be
  reachable from the worker. `--in-process-connectors jira` is the way back, and the
  in-process handler stays for it.

- **New Atlas mark.** The logo and the favicon are now a white peak carrying a
  cross on a black tile, replacing the blue hexagon-and-flow mark and the `A`
  letter tile the Console showed in its top bar, drawer, login screen, handbook,
  public forms and consent page. Nothing about branding *behaviour* changed: an
  org-wide logo uploaded under Appearance
  ([ADR-0148](docs/adr/0148-org-wide-brand-logo.md)) still overrides it
  everywhere, and removing that logo now restores the new mark instead of the
  letter.

- **The handbook and the examples teach Workers, not connectors.** The Console
  renamed *Connectors* to *Workers* with the first slice of
  [ADR-0203](docs/adr/0203-worker-execution-model.md); the documentation still
  called the same thing three different things. It now uses one vocabulary
  throughout: a **Worker Type** is a capability Atlas has (Jira, Mail, Active
  Directory), a **Worker** is one configured target and identity of that type —
  the name a task states — and a **Worker Instance** is a running process that
  leases jobs. *Forms & connectors* is now *Forms & workers* and explains the
  three levels and the `Task → Job → Worker` chain they sit on; the Active
  Directory chapter uses two forests to show where they come apart (a second
  directory is a second Worker, more throughput is more Worker Instances); the
  glossary gained all three terms and keeps *Connector* as a legacy entry.

  **No model changes.** A task still names its worker with `connector="…"`, the
  extension elements are still `<atlas:mailConnector>` and friends, and
  `atlas worker --connector mail` still names the worker type — the handbook now
  says so explicitly, in a note that explains why. Every example's prose moved to
  the new vocabulary; not one line of BPMN did.

- **The Modeler says Worker too, and offers the names it is asking for.** The
  Console and the handbook already speak
  [ADR-0203](docs/adr/0203-worker-execution-model.md)'s vocabulary; the properties
  panel was the last place calling all three things a connector. A service task now
  picks a **Worker type** — and the entries dropped the `… Connector` suffix every
  second one carried, so the list reads *Jira*, *PostgreSQL*, *BMC Remedy* — while
  the field naming the concrete target is **Worker**, because that is what it names:
  one endpoint and identity an operator registered on this server, not the
  capability above it and not a replica below it. The section headings followed
  (*Jira instance* → *Jira worker*, *Mail provider* → *Mail worker*): in this
  vocabulary an *instance* is a Worker Instance, which is the one thing a model
  never picks.

  **The Worker field is a dropdown.** It offers this server's configured Workers of
  that type, from `GET /api/v1/configured-workers` — Jira, Remedy, SharePoint and
  the three SQL products join mail and clio, which already had one. The name of a
  Worker is the single thing about a task that cannot be read off the model, and
  typing it from memory is how a deploy fails on a hyphen. It stays a combobox, not
  a closed list: a Worker that lives only in a worker process's own environment
  (`ATLAS_POSTGRES_<NAME>_DSN` and friends) is registered nowhere the Modeler can
  see, so a name may still be typed, and the Entra field keeps its free-text form
  because it may be a FEEL expression choosing the tenant per instance. When the
  server has no Worker of the type, the field now says so instead of opening an
  empty list that looks broken.

  Two hints stopped being true when the SQL and Entra types gained Console records
  ([ADR-0172](docs/adr/0172-entra-id-connector.md),
  [ADR-0173](docs/adr/0173-generic-sql-connector.md)) and still claimed their Worker
  could not be configured there; they now describe both places it can live.
  Nothing in the model moved: the attribute is still `connector="…"` and the
  extension elements are still `<atlas:jiraConnector>` and friends.

### Fixed

- **A data object whose declaration went missing took the whole deploy down with it.**
  A data object is two elements: the `<dataObject>` that declares it and carries its
  type, and the `<dataObjectReference>` that puts it on the canvas with its name, its
  data state and its shape. Only the second is drawn, so only the second is visibly
  there — and a model can reach Atlas having lost the first. The box still reads
  `Kunde [received]` to everybody looking at it, and it names nothing the engine can
  find.

  Two things then went wrong, neither of them the modeller's doing. The deploy was
  refused with `dataObjectRef "DataObject_0s4i37q" is unknown` — an id nobody had ever
  typed, attached to no shape, with nothing to do about it. And a type set in the
  properties panel had nowhere to be written, so it vanished on the next save without
  a word.

  Both are fixed from opposite ends. **The compiler lets the reference stand in for
  its own declaration**: a data object's identity is its name, the name is on the
  reference, and nothing about such a model is in doubt — the same fallback a
  `<dataStoreReference>` naming no root element already gets. Only the declared type
  is genuinely lost, so the object is seeded without one rather than with a guess.
  **The Modeler repairs the model on the way in**, declaring what the dangling
  reference implies, which is where the type gets somewhere to live again. That repair
  now runs beside the one for `itemSubjectRef`, and for the same reason: the bpmn
  moddle drops a reference it cannot resolve, so a model that arrives dangling comes
  back from the next save having lost more than it arrived with.

  The message left for a reference that names nothing at all no longer claims to be
  about a data *output* association when it is a read that failed.

- **An application you had just created was missing from the dialog that asked which
  application to use.** Creating an information model, creating or importing an
  architecture model, and promoting a release all asked their question through a
  `window.prompt` whose body was the choices as a numbered list, with "enter a number"
  underneath. A browser truncates a prompt body once it grows past a handful of lines
  and ends it with an ellipsis — so on a server with a dozen applications the newest
  ones, which sort last, were simply not in the list somebody was being told to choose
  from. Nothing said they had been cut off; the application looked missing, and the
  three trailing dots looked like a rendering quirk.

  All four are now the same small dialog: a real drop-down of the applications you can
  write to, and the name beside it in one step instead of a second prompt. The
  suggested name follows the picker until you type your own. Nothing has to be counted,
  a list of any length fits, and Escape, Cancel or a click outside all mean the same
  thing as before.

- **A type a modeling tool wrote in its own namespace read back as a GUID.** BPMN gives
  an `<itemDefinition>` no name of its own — a root element carries an id and nothing
  else — so `structureRef` is the only slot the specification offers for the name of the
  type being declared ([ADR-0230](docs/adr/0230-process-information-model.md)).
  A tool that does not use it has to invent somewhere, and MID Innovator does: its
  itemDefinitions are a bare GUID id with `<bpanda:property name="Name" value="Incident"/>`
  beside them. Atlas read the id, so every data object in such a model declared a type
  called `_853994e9-12f5-9cef-bf69-ca3e2b7cb6a8` — shown that way in the properties panel
  and in the Data tab, and then reported by the Problems panel as a class nothing models,
  against a name nobody could have modeled it under.

  The property is read now, by the compiler and by the Modeler alike, so the panel and
  the Problems list say Incident and agree. `structureRef` still wins wherever both are
  present, and a definition that names itself nowhere still falls back to its id, so no
  model that worked reads differently.

- **A Jira watch could get stuck on one window and hold the whole Console with it.** A
  jira watch resumes from a `created >=` / `updated >=` clause, held a safety lag behind
  the newest issue it saw so an issue Jira's index publishes late is still inside the
  next window ([ADR-0214](docs/adr/0214-jira-inbound-issue-watch.md)). That is right at
  the tip of a query. Behind a **full** page it inverted: a page that filled the bridge's
  batch limit stopped at the limit and not at the end of the result set, and subtracting
  the lag put the next cursor *inside the page just read*. The watch then re-read and
  re-published the same page every tick and never reached the issue behind it — for
  ever. A bulk import, or a bulk transition on an `updated` watch, is all it took: a few
  hundred issues sharing one minute.

  Nothing about it looked broken. The reads succeeded, the publishes were real work, and
  the engine correctly discarded every one against its durable high-water mark — while
  each round spent a Jira search, a run-loop batch and two fsyncs, and every Console
  request that has to reach the run loop queued behind them. The lag now applies only to
  a page that is not full; behind a full page the cursor lands on the newest issue's own
  minute, which `>=` re-reads, so the read moves without skipping anything. A page whose
  issues *all* share one minute — which no minute-granular cursor can page through —
  steps past that minute and logs `inbound_watch.minute_overflowed` rather than re-reading
  it for ever ([ADR-0227](docs/adr/0227-jira-read-bounds-and-progress.md)).

- **A jira watch polled every two seconds instead of every minute.** ADR-0214 gives a
  watch a `pollSeconds` of its own and a *kind's default* for one that states none —
  60 seconds for Jira, because a site rate-limits per site and a two-second poll per
  watch spends that budget on empty answers. The default was never implemented: a watch
  created without an explicit cadence fell through to the bridge's own tick and was read
  thirty times more often than intended, each time for a Jira search, a run-loop round
  trip, and the record write below.

- **Every watch rewrote its record on every tick.** The bridge recorded `lastPolledAt`
  for each due subscription on each tick, and re-saved the resume cursor even when the
  read had produced the one already stored. A design-time record is written with an
  fsync of the file and one of its directory, on the run-loop goroutine — so a handful of
  watches meant a continuous fsync stream on the single writer, in front of every request
  that needs it. `lastPolledAt` is now written only for a watch whose cadence actually
  reads it back, and a cursor only when it moved.

- **An uncapped Jira search had no ceiling at all.** `maxResults="0"` means "read every
  match", and the client paged until the site ran out, held every result in memory, and
  handed the lot to the engine as one process variable to encode and fsync — so a JQL
  that matched far more than its author believed was an out-of-memory in the server
  rather than a failed task. The account search had a second edge: it answers with a bare
  array, carrying no total and no page token, so a server that ignored `startAt` was read
  for ever. An uncapped read now stops at 5000 results with an error naming the fix. It
  fails the job — retry, then an incident — rather than truncating, because a model told
  it read everything when it read the first 5000 is the worse outcome. A task that states
  its own `maxResults` is untouched.

- **A message start event ran the branches nobody triggered — and could feed itself
  forever.** A start event is a trigger, and BPMN instantiates at the one that fired.
  Atlas seeded a token at **every** root start event whatever created the instance, which
  [ADR-0035](docs/adr/0035-message-start-events.md) recorded as a message start behaving
  "exactly like a none start". True of a process with one start event; false of a process
  with two — and the difference is not cosmetic. A Jira event watch
  ([ADR-0214](docs/adr/0214-jira-inbound-issue-watch.md)) published
  `jira.ticket.created`; the message-started instance also ran the none-start branch; that
  branch created a Jira issue; the watch matched it; the next instance created the next
  issue. The chain is visible in Operations — the instance for `PAT-13` holding
  `newTicket = PAT-14`, the one for `PAT-14` holding `PAT-15` — and it stopped only when
  the watch was deleted by hand. Nothing raised an error, because from the engine's side
  nothing went wrong.

  A trigger now instantiates at itself
  ([ADR-0226](docs/adr/0226-start-events-are-triggers.md)):
  the creation command carries the start event that fired, and the argument is
  **required**, so a fourth kind of trigger cannot inherit the old behaviour by forgetting
  it. A create nobody triggered — the API, a call activity — seeds the **none** start
  events instead, which is what pressing Start means. A process whose only entry is a
  message or a timer keeps ADR-0035's permissiveness and is seeded at every entry it has,
  because an instance with no token at all is a worse answer than a permissive one.

  **This changes behaviour for a deployed model with more than one root start event**: the
  branches that used to run on every trigger now run only on their own. Nothing about the
  recorded events changes, so recovery of an instance created before this is unaffected.

- **A database connector's setup hint was written into a hidden element.** The "New
  connector" form on Console → Workers asks the same shared description
  ([ADR-0160](docs/adr/0160-one-connector-dialog.md)) which fields a kind uses and what
  to say about them — and then hid the sentence it got, because the paragraph that
  renders it carried the mail-only class. So the one line saying that a database's
  *whole connection string* is the credential, that it is sealed into the vault, and
  that Atlas supervises the worker for it was produced for every SQL kind and shown for
  none; the same was true of Active Directory's. The edit dialog had always shown it,
  which is the disagreement between two forms that ADR-0160 exists to prevent. The hint
  now appears for any kind that has one.

- **A mock database's refusal named a pattern instead of a variable.** A statement no
  seed answers fails naming itself, its bound parameters and the seed file to add the
  answer to ([ADR-0221](docs/adr/0221-sql-mock-mode.md)) — but it quoted the literal
  `ATLAS_<PRODUCT>_MOCK_SEED`, leaving the reader to substitute their product. It now
  names the variable that worker actually reads (`ATLAS_MARIADB_MOCK_SEED`, and so on),
  which is the difference between an error you act on and one you decode.

- **A rejected REST call says what the far side objected to.** A non-2xx response from a
  REST connector task reported only `returned HTTP 400`, and threw away the body the
  server had already sent to explain it — leaving an operator to guess which of the URL,
  the headers, the query parameters or the body was wrong. The incident message now
  carries an excerpt of that response: one line, collapsed and bounded, so a proxy's HTML
  error page cannot become the incident.

- **Renaming a saved diagram or form left a duplicate — or silently overwrote another
  one.** A draft is stored under its process id and a form under the id a user task
  binds to, but the save only ever saw the id in front of it, never which record the
  author was editing. So retyping the Process ID and saving wrote a *second* draft and
  left the first in place, and if something already held the new id, the save landed on
  top of it: the artifact that was there was gone, with no warning and no question. The
  form editor had a quieter version of the same defect — the Design pane's **ID** field
  edited the schema and nothing else, so the chip in the toolbar went on showing the id
  the form was really stored under, the panel showed the id the author had typed, and
  the rename never happened (an export of that form then carried the typed id, so
  re-importing it forked a copy). The save now names the record it is editing (`?from=`
  for a draft, `"from"` for a form): a changed id **moves** the record, carrying its
  application and its creator, and an id another artifact already holds is refused with
  409 rather than overwritten. The Modeler checks the id as it is typed — the field
  turns red and names what holds it — and the form editor's chip is now the schema's id
  itself, dashed while a rename is unsaved, with Save asking first because a user task
  still bound to the old id will find no form. The two places where landing on something
  that already exists is the point rather than an accident — importing a `.bpmn`/`.form`
  file over the artifact it came from, and pulling a deployed definition back into a
  draft — now ask by name instead of doing it silently or refusing it
  ([ADR-0222](docs/adr/0222-artifact-id-renames.md)).

- **A task's *out* section showed variables the neighbouring branch produced.** The
  replay's in/out card inferred what an element wrote by diffing the variables it saw on
  entry against the ones that stood when it finished — which on a parallel fork spans the
  sibling branch's work, so both branches were credited with both writes and a task that
  fetched tickets claimed to have created one. The engine now records **which element
  wrote each variable** and the timeline carries it per step (`writes`), so the card and
  the Variables tab state what the log says instead of guessing from two snapshots; a
  gateway or an event, which writes nothing, now claims nothing. Attribution is a fact
  frozen into the variable event and rebuilt by replay like every other. An instance that
  ran before this keeps the old inference — a record cannot be back-filled with a fact it
  never carried — and says so on the section
  ([ADR-0219](docs/adr/0219-variable-write-attribution.md)).

- **The Workers view hid a connector that was working and showed one that was broken.**
  A job type the engine serves itself has nothing queued, nothing in flight and no
  worker pulling it — none may — so the fold that keeps quiet built-ins out of the way
  treated a healthy in-process connector as noise. It appeared when it failed and
  vanished when it was fixed, on the page whose whole subject is who is doing the work.
  The fold now hides what an installation does not *use* rather than what is momentarily
  quiet: a type a deployed process references stays visible.
  That leans on the Processes column, which was itself short. It was filled from service
  and send tasks alone — "the only elements carrying a job type the model authored",
  which is true of the string and false of the job. A connector task, a script task, a
  business-rule task and a user task each create a job under a reserved type, so every
  Jira, clio, Active Directory, script, DMN and user-task row said nothing about which
  process was waiting on it. All five node types are counted now.

- **A Jira search called an endpoint Jira Cloud has removed.** The `search`
  operation posted to `/rest/api/2/search` with `startAt` paging; Atlassian
  progressively shut that endpoint down across Cloud over 2025, and a switched-over
  site answers `410 Gone`. A Cloud search now goes to `/rest/api/3/search/jql` and
  pages by its opaque `nextPageToken`, asking explicitly for `*navigable` fields
  because the replacement returns none unless told to — a model reading
  `issue.fields.summary` would otherwise have started receiving issues with nothing
  in them. It is the one call that leaves v2, which a search can afford because ADF
  governs how a description or comment body is *written* and a search only reads.
  Jira Data Center is not affected by the deprecation and keeps the offset-paged
  endpoint; the credential shape already tells the two products apart. The other six
  operations use `/rest/api/2/issue/…` and were never affected
  ([ADR-0201](docs/adr/0201-jira-connector.md)).

- **A feed scrape reached its worker without knowing it was a feed.** The resolved
  job carried `format` and `maxItems`
  ([ADR-0190](docs/adr/0190-webscrape-feed-extraction.md)), but the engine's payload
  dropped both — so an offloaded `format="rss"` task fetched the feed as HTML and
  failed compiling a CSS selector it had never authored, parking the instance on an
  incident that named a selector nobody wrote. `webscrape` is offloaded by default,
  so that was the path [`examples/blick-schlagzeilen.bpmn`](examples/blick-schlagzeilen.bpmn)
  actually took; only `--in-process-connectors webscrape` worked. Both fields travel
  now, and the entries come back as the `{title, link, description, published}`
  objects the in-process path writes — the two paths share one definition of what a
  scrape's result *is* (`webscrape.Items`) instead of building it twice.

  The same class of gap in two connectors in one week is a missing check, not two
  slips: every payload arm is now pinned against the resolved-job struct a worker
  unmarshals into, in both directions. A field the job carries and the payload omits
  fails the build, as does a key nothing on the far side reads.

- **An AD task naming a Console-configured directory reached its worker without the
  name.** The resolved job dropped `connector`, so a task authored the
  [ADR-0206](docs/adr/0206-ad-as-a-console-connector.md) way — a directory an operator
  created in the Console, rather than a `url` in the model — failed on the worker with
  "has an empty url". The name travels now, and the Modeler's own descriptor learned
  the `connector` attribute as well: bpmn-js drops an attribute it has no property for,
  so opening such a task and pressing Save silently stripped it.

### Security

- **Not every account may deploy any more.** Each of the 199 `/api/v1` routes now
  names the role it requires, and one check at the boundary enforces it for every
  credential there is — a browser session, an API token, a deploy token, an OAuth
  grant and, because a tool call runs as its caller, every MCP tool
  ([ADR-0209](docs/adr/0209-roles-per-endpoint-group.md)).

  Four roles, and an account carries several: **admin** (accounts, credentials,
  secrets, settings, backup and restore), **modeler** (author drafts, forms and
  decisions — and deploy them), **operator** (start, cancel and repair
  instances; read runtime data) and **user** (work on tasks and read what they are
  given). Deploying a model is code execution, and until now every signed-in
  account could do it.

  **Your upgrade takes nothing away.** Every existing account keeps what it could
  do — modeler, operator and user, so everything except administration — and each
  record is marked so this happens exactly once: what you narrow afterwards stays
  narrow. New accounts get `user`. An API token minted before this keeps its reach
  too, and one minted now carries its minter's roles, never admin.

  Grant the roles on the account screen under Organization, which lists each one
  with what it lets the person do. The navigation then offers only the apps and
  screens that person's roles reach.

- **An inbound connector's events reach only the processes you allow.** A
  subscription now **claims** the message name it publishes under: a process
  deployed by somebody who cannot reach that connector can no longer be delivered
  those events, and pointing a connector at a name somebody else's process already
  listens for is refused instead of silently forwarding your post to them
  ([ADR-0205](docs/adr/0205-connector-ownership-and-event-delivery.md)).

  This is what the previous entry deliberately left open. Giving a connector an
  owner stopped a stranger *configuring* it; the message name was still the whole
  authorization, and a name is not a secret.

  **Checked at both doors**, because a check at one moment is not a rule: deploying
  is refused when the definition could be delivered a claimed name its deployer
  cannot reach, and claiming is refused when a definition the claimant cannot reach
  already listens for that name. Renaming a subscription, or switching a disabled
  one back on, goes through the same door — otherwise the update endpoint would be
  the way around the create endpoint. A project bundle is refused before anything
  registers, so it stays all-or-nothing.

  **Both refusals name the message and nothing else.** Naming it is what makes it
  actionable; naming the other party would hand over exactly what a private
  connector was hiding.

  What counts as "could be delivered" is every way a definition can receive a
  message — a start event, an intermediate catch, a receive task, a boundary event,
  a message-triggered event subprocess. Not just start events: a catch in a process
  somebody starts themselves receives the payload just as well.

  **Limits, stated rather than implied.** An administrator passes both doors. A
  definition deployed before this carries no deployer, reads as ownerless, and so
  keeps any name it already listens for until it is redeployed — the alternative
  would break every upgrade where somebody claims a name their own long-standing
  process uses. And this governs delivery to a *definition*, never to a running
  instance: while a message correlates, the engine still matches on name and key
  alone.

- **A connector belongs to somebody now.** It has an owner, and only they — plus
  whoever they share it with, and administrators — can see its endpoint and
  credential reference, change it, delete it, or point it at a message name
  ([ADR-0205](docs/adr/0205-connector-ownership-and-event-delivery.md)).

  **What it replaces was worse than it sounds.** Measured on a running server with
  a login required, an account holding only the base `user` role could list every
  connector with its endpoint and sender mailbox, edit one, **delete somebody
  else's**, read every inbound subscription — which is every message name — and add
  a subscription to somebody else's connector under a name it chose. Exactly one
  endpoint on that surface required an administrator.

  Sharing is the vocabulary you already know: an owner, viewer/editor roles, and a
  member list that takes a **group** as easily as a person
  ([ADR-0180](docs/adr/0180-groups-as-members.md)), so "share it with my colleague"
  and "share it with the team" are one action. It is in the Console beside each
  connector, not only in the API. Ownership can be handed on, which matters because
  a connector nobody owns is an administrator's — so without it, one person leaving
  would make an administrator the owner of everything they ever configured.

  **Authoring is untouched, and so is running.** Every signed-in person still sees
  that a connector exists — its name, kind and whether it is usable — because the
  modeler builds its picker from that list, and a modeller staring at an empty
  dropdown is an outage, not a security measure. And nothing here reaches the
  runtime: a deployed process resolves its connector by name whoever started it,
  because execution is not authoring.

  Also closed, found while building this: `POST /api/v1/connectors/test` resolved
  whatever credential reference its body named and sent real mail with it — a "send
  mail as anyone, with anyone's credential" endpoint for every account. A credential
  reference may now be named only by somebody who may already edit a connector that
  uses it.

  **Two costs, both deliberate.** A connector stored before this carries no owner
  and becomes an administrator's to manage until one is assigned — the opposite of
  what [ADR-0071](docs/adr/0071-sharing-scopes.md) chose for legacy artifacts,
  because that record was adding a capability and this one is closing a hole. And
  this protects a connector's *configuration*, not yet the events it brings in: a
  process that names the right message still receives them. That second half is
  specified in the record and not built.

- **A person can now let an application act as them.** Atlas is an OAuth
  authorization server: a hosted client — a connector on somebody else's
  infrastructure, driven by a person in a browser — sends that person here, they
  see who is asking and what it will reach, and what comes back is a token that is
  exactly as privileged as they are
  ([ADR-0200](docs/adr/0200-mcp-oauth-resource-server.md)).

  This is what makes such a connector work at all. It had nowhere to put an API
  token, and handing it one would have meant a long-lived credential sitting in a
  third party's configuration store. Now nothing is handed over: the person
  approves, and the token that results carries **them**, so a tool call is
  attributed to them and inherits every rule their account has — the property
  [ADR-0196](docs/adr/0196-authenticated-mcp-transport.md) established, surviving a
  client nobody can configure.

  The shape is deliberately the smallest a compliant client will talk to.
  Authorization code with PKCE (`S256`) and refresh, and **no other grant type** —
  no implicit flow, no password grant, no client-credentials grant. Registration is
  an administrator's act (`POST /api/v1/oauth-clients`), unless you open
  self-registration — the next entry.

  **What a token reaches follows what was approved.** A grant for `/mcp` drives the
  transport and is refused at `/api/v1` — the audience made into something enforced
  rather than merely recorded. A grant for the server reaches what its person
  reaches.

  **An approval is a person's to withdraw**, and theirs to see: `GET
  /api/v1/oauth-grants` lists your own, an administrator's lists everyone's, and
  `DELETE` takes effect on the next request. Removing a client revokes every grant
  approved for it. Disabling or deleting an account revokes its grants — a
  connector must not outlive the account behind it — and a role or group change
  rewrites them rather than dropping them, so an administrative edit does not knock
  somebody's connector over. Eight `auth.oauth_*` events record all of it, and carry
  no secret.

  Refresh tokens **rotate**: renewing invalidates the token you renewed with, so a
  copied one is worth one use rather than standing access. Access tokens last two
  hours.

- **Connecting an AI assistant is a screen now, not a request body.**
  **Console → AI access** asks which application, creates its credentials, and hands
  you the three values the connector's own dialog is asking for — MCP server URL,
  client id, and the secret, shown once, each with a copy button
  ([ADR-0200](docs/adr/0200-mcp-oauth-resource-server.md)).

  The endpoints for this shipped first and the instruction was a `curl` command in
  the install guide. That is an instruction for whoever wrote the endpoint. The
  question an operator actually has is *what do I paste into those three fields*,
  and this answers exactly that.

  It also **checks the address Atlas publishes** against the one you are looking at,
  and says so when they differ — the `--external-url` mistake otherwise surfaces
  much later, as a connector that simply does not work, with nothing on screen to
  suggest why.

  The same page lists what is registered, marks anything that registered itself, and
  shows every approval — yours, or everyone's for an administrator — with the button
  that withdraws one. Withdrawing an approval no longer requires the API, which
  matters because it is the one thing in all of this that belongs to the person
  rather than to the operator.

- **A connector can now register itself — if you let it.**
  `--oauth-dynamic-registration` (or `ATLAS_OAUTH_DYNAMIC_REGISTRATION=1`) opens
  [RFC 7591](https://www.rfc-editor.org/rfc/rfc7591.html) client registration, so a
  hosted connector can be connected with nothing but this server's URL: no client
  id to paste, nothing for an administrator to enter first
  ([ADR-0200](docs/adr/0200-mcp-oauth-resource-server.md)).

  **It is off by default and stays off unless you say otherwise**, because it is
  the one unauthenticated endpoint in Atlas that writes durable state. Off means
  absent: the route is not mounted and `registration_endpoint` is not in the
  authorization-server metadata, so a client discovers the truth rather than being
  told to try and then refused.

  Four things make opening it defensible, and the first is the one that matters.
  **A client that registered itself is marked as such on the consent screen** — in
  as many words, above the question. Without that, opening registration would
  quietly degrade every consent decision anybody makes afterwards: "an application
  is asking for access" would stop implying that anyone had vetted it, and the
  person deciding would have no way to tell. The name on that screen is one the
  application chose for itself thirty seconds ago.

  The rest bound what abuse can achieve. **The number of self-registered clients is
  capped**, and past the cap registering *evicts* the oldest one nobody ever
  approved rather than refusing — a cap that only refuses is its own denial of
  service, since whoever fills the table first would lock everybody else out from
  outside. **An approved client is never evicted**, so a flood cannot revoke
  somebody's access. And registration is **throttled on its own budget**, separate
  from the other public endpoints, so a flood of registrations cannot throttle the
  token exchanges of clients that already registered.

  Registering still buys nothing on its own: a client id and secret let an
  application *ask*, and only a person's approval reaches anything — bounded by
  their own account. `auth.oauth_client_self_registered` records each one, kept
  distinct from an administrator's registration, because "an administrator added an
  application" and "a stranger added one" are the same sentence with different
  consequences. Self-registered clients are also flagged in `GET
  /api/v1/oauth-clients`.

- **A refused request now says what refused it.** Atlas answers `401` with
  `WWW-Authenticate: Bearer realm="atlas"` and, from now on, a `resource_metadata`
  pointer to an [RFC 9728](https://www.rfc-editor.org/rfc/rfc9728.html)
  protected-resource document — the discovery mechanism the MCP authorization
  specification makes mandatory for a server behind a login
  ([ADR-0200](docs/adr/0200-mcp-oauth-resource-server.md)).

  It closes a failure with no visible cause. A hosted MCP client — a connector
  running on somebody else's infrastructure, driven by a person in a browser — has
  nowhere to put an API token, so when it is refused it goes looking for an
  authorization flow. With nothing to go on it guesses `/authorize`, which Atlas
  does not serve, and the operator sees a `404` that explains nothing. Now it finds
  a document naming the resource that refused it.

  Two new public routes serve that document: `GET
  /.well-known/oauth-protected-resource` for the server, and
  `/.well-known/oauth-protected-resource/mcp` for the transport, which is the one
  an MCP client looks for. They carry the origin, the product name, and that a
  bearer goes in a header — no secret, and nothing that is not already public. A
  `401` from `/mcp` points at the second; everything else points at the first.

  **`--external-url` (or `ATLAS_EXTERNAL_URL`) is new**, and worth setting on
  anything behind a proxy: Atlas terminates no TLS, so the origin it derives from a
  request is `http://…`, which is not a URL a client can use. Stated once, it fixes
  the documents and the challenge together, and a forged `X-Forwarded-Proto` cannot
  move it. Left unset, the scheme follows `X-Forwarded-Proto` and the host follows
  the request — right for direct access, and right behind a proxy that sets the
  header.

  **This does not yet make a hosted connector work, and is not meant to.** Atlas
  issues no tokens and accepts none from a foreign issuer, so the document names no
  authorization server — deliberately, because sending a client through an entire
  flow only to refuse the token at the end is worse than saying at the outset that
  there is nowhere to go. What changes is that the refusal is legible instead of
  silent. The other half of ADR-0200 — an authorization server and a consent screen
  — remains an open decision.

- **`/metrics` moved behind the boundary — the last route that had not.** The
  Prometheus exposition was served without authentication since
  [ADR-0142](docs/adr/0142-prometheus-metrics.md), for a reason that has since
  stopped being true: a scraper carried no session and could not present anything,
  so the guidance was to put a proxy in front of it. With API tokens it can present
  something. `/metrics` is now gated like every other route, and a new token scope,
  `metrics`, allows exactly one pattern — `GET /metrics`, the narrowest scope in the
  system
  ([ADR-0198](docs/adr/0198-metrics-behind-the-boundary.md)).

  Worth being plain about: **the payoff here is structural, not confidential.** The
  exposition carries instance counts, batch latencies and queue depth — no process
  variables, no business data. What it buys is that "no interface is reachable
  without a credential" is now true without a footnote, and that the public list is
  short enough to read at a glance: the two probes, the login screen's own reads,
  the token-bearing share links, and the UI.

  **Breaking: every existing scrape config needs a credential.** For Prometheus that
  is two lines:

  ```yaml
  authorization:
    credentials: atlasat_…      # an API token scoped "metrics"
  ```

  A failing scrape looks like a healthy server, so this is worth doing before the
  upgrade rather than after; a refused scrape shows up as `auth.denied`. The probes
  are untouched and stay open — a readiness probe that needs a credential does not
  work in the incident it exists for — and a signed-in person still reaches the
  exposition. `--metrics=false` still turns it off entirely.

- **API tokens: a credential a machine can actually be given.** Under `--auth`, the
  only non-session credential the server accepted was the internal service token —
  minted at startup, kept in memory, served over no endpoint, and therefore
  obtainable only by the process that minted it. That was fine while its holders
  were this server's own children. It stopped being fine when a login became the
  default: **a worker on another host, a stdio MCP adapter against a remote server
  and a CI job all had nothing to present**, and the `--token` flags on
  `atlas worker` and `atlas mcp` had no value an operator could put in them.

  The workaround the code appeared to offer did not exist either. `workerTokenEnv`
  honours an operator-set `ATLAS_TOKEN` and stops injecting its own — but
  `principalFor` compared a bearer only against the internal token, so the value was
  honoured on the way out and refused on the way in: setting the variable handed
  every supervised worker a credential the server rejects at every poll.

  `POST /api/v1/api-tokens` now mints one (admin-only). The secret is returned
  exactly once — only its SHA-256 is stored — and it carries a **lifetime** and a
  **scope**. `worker` reaches the four operations `atlas worker` actually performs
  and nothing else; `full` reaches what a signed-in non-admin reaches, for a CI job
  or an MCP adapter, and is never an admin. Revocation is deletion and takes effect
  on the next request; an expired token is refused like an unknown one while its
  record stays listed. `ATLAS_TOKEN` set to an API token now works as the comment
  always claimed, and a value the server does not accept is called out at startup
  (`auth.worker_token_unknown`) instead of being discovered one failing job at a
  time ([ADR-0194](docs/adr/0194-api-tokens.md)).

  The deploy-token allowlist of
  [ADR-0129](docs/adr/0129-remote-deployment-targets.md) folds into the same scope
  mechanism rather than sitting beside it, so what *any* machine credential can
  reach is one file. Deploy tokens keep their own store, prefix and record; what
  moved is the reach check, not the identity.

- **The login is throttled, and there is a security audit trail.** Two gaps that
  mattered more the moment a login became the default. `/api/v1/auth/login` had
  nothing in front of it — the token bucket existed but guarded only the public form
  routes, so password guessing was bounded by nothing but how fast bcrypt would
  answer, which is backwards: each attempt cost the server ~100ms of CPU and the
  caller one request. And who signed in, who failed to, and who changed an account
  appeared in no log at all, so the compliance answer for that had to be "the reverse
  proxy supplies it" — an answer about somebody else's software, and one that cannot
  name the *account* an attempt was against.

  Attempts are now throttled on two keys into the same token bucket: 20 per address
  back to back (refilling every two seconds — a whole office behind one NAT address
  is an ordinary deployment), and 5 per account (refilling over 15 minutes). It is
  charged **before** the account is looked up and whether or not that account exists,
  so the throttle does not answer the question the uniform "invalid credentials"
  message is careful to leave open, and a flood costs the server a map lookup rather
  than a bcrypt verification. A successful login clears the account's budget, so two
  mistyped passwords are not carried around for a quarter of an hour, and the lockout
  always heals on its own — no operator has to lift one.

  Eleven stable `auth.*` events now record sign-ins, refused sign-ins with the reason,
  throttling, sign-outs, authorization refusals, the account lifecycle, password sets
  and deploy-token mint/revoke. Each carries the acting principal and the client
  address; none carries a password, a hash or a token, and a test drives real secrets
  through the handlers and asserts none of them reaches the log. Anonymous `401`s are
  deliberately not recorded — they would bury the meaningful lines under every probe
  that finds the port. Ship them with `--log-format=json`
  ([ADR-0197](docs/adr/0197-login-throttle-and-audit-log.md)).

  **Minor behaviour change:** a burst of failed logins now answers `429` rather than
  continuing to answer `401`.

- **`atlas serve` requires a login by default.** `--auth` was opt-in, mirroring
  `--docs` ([ADR-0044](docs/adr/0044-user-management-and-authentication-boundary.md)) —
  a reasonable call when authentication first landed and turning it on broke MCP, the
  explorer and the tests at once. Those reasons are worked through, and what was left
  was a default that every document about Atlas told you to change: the install guide,
  the Helm chart and the compliance concept all opened with "turn on `--auth`". A
  default everything tells you to change is not a default, it is a trap with
  documentation around it.

  It is now on. `--auth=false` still runs the server fully open and writes one WARN
  line at startup (`auth.disabled`) naming what that means — the API, the UI, and
  `/mcp`, which can deploy and run processes. The first start with an empty user store
  seeds one administrator from `ATLAS_ADMIN_USERNAME`/`ATLAS_ADMIN_PASSWORD`, or
  generates a password and logs it **once**; that path was always there, it is just no
  longer step 6 of the install guide. The Helm chart follows, defaulting
  `atlas.auth.enabled` to `true` and no longer refusing to render without an admin
  password source — set `atlas.auth.existingSecret` for anything beyond a scratch
  install ([ADR-0195](docs/adr/0195-auth-on-by-default.md)).

  **Breaking.** `atlas serve` with no flags now requires a login. Pass `--auth=false`
  for the old behaviour.

- **The API description and the explorer are behind the login.** `GET
  /api/v1/openapi.json` and `/api/docs` were public. Nothing on the login screen reads
  either, and the explorer's "Try it out" drives the same mutating API a session is
  required for — the argument `--docs` already makes, one step further. `--docs` still
  decides whether they are served at all; a login now decides who reads them. They moved
  together on purpose: an explorer that renders and then cannot fetch its own document
  is worse than one that says plainly it needs a login.

- **`/mcp` is behind the login, and acts as its caller.** The Model Context Protocol
  transport was mounted on a mux *beside* the API server, so the authentication
  middleware never saw it — while the adapter attached the server's internal service
  token to every loopback call it made
  ([ADR-0049](docs/adr/0049-internal-service-auth-for-mcp.md)). `--auth` therefore did
  not close `/mcp`; it supplied it with a working credential. Anything that could reach
  the port drove 71 tools as the `system:mcp` principal, `atlas_deploy` among them — and
  deploying runs script tasks as the service user, so an exposed `/mcp` was code
  execution with no authentication at all.

  It is now mounted by the API server itself (`api.WithMCP`) and gated like every other
  route: without a credential, `401` and a `WWW-Authenticate: Bearer` header. The adapter
  carries no identity of its own over HTTP — it forwards the `Authorization` or `Cookie`
  the request arrived with, so a tool call is exactly as privileged as whoever made it,
  is attributed to them, and inherits every authorization rule the API has. An admin over
  MCP can now reach an admin-gated tool; a signed-in non-admin cannot; and a deploy
  token presented there is refused outright, because the transport is not one of the
  two operations that credential is confined to
  ([ADR-0196](docs/adr/0196-authenticated-mcp-transport.md)).

  **Breaking, on servers running `--auth`.** An MCP client that reached `/mcp` without
  presenting anything now gets `401` and must send the session cookie or a bearer token.
  A server without `--auth` is unchanged — and is still open, `/mcp` included.

- **`atlas mcp --token`.** The stdio adapter had no way to present a credential, so it
  could not work against a server running `--auth` at all: every tool call came back
  `401`, while `atlas worker` has had `--token` for some time. It now takes the same
  flag, defaulting to `ATLAS_TOKEN`, and trims it — a token exported from a shell
  profile routinely carries a trailing newline, and a bearer sent with one is refused
  for a reason nothing in the `401` explains. Startup logs whether a credential is
  configured (never the credential), because "every tool returns 401" and "no token was
  set" are the same incident.

- **Every mounted route declares who may reach it.** Which requests the boundary gated
  used to be a path-prefix test — gated if and only if the path started with `/api/v1` —
  so a route was public by *omission*: anything registered elsewhere was open because of
  where it sat, not because anyone decided it should be. That is how `/mcp` and
  `/metrics` came to be reachable without a login.

  Each route now states an access class where it is mounted, and a request is classified
  by the pattern that will actually serve it; an undeclared pattern is gated, so mounting
  a route off to the side fails safe instead of inheriting the UI catch-all. The
  resulting public set — probes, metrics, the login screen's own reads, the API explorer,
  the share links and the UI — is held against a written-out list by a test, so opening a
  route is a reviewable diff rather than a side effect
  ([ADR-0199](docs/adr/0199-route-access-classes.md)).

  Because patterns carry methods, so does the class: `GET /api/v1/settings/theme`,
  `/logo` and `/registration` stay public for the login screen, while `PUT` and `DELETE`
  of those paths are now refused at the boundary rather than only by the admin check
  inside each handler. **Minor behaviour change:** an anonymous write to one of them
  answers `401` instead of `403` — nothing was presented, which is what `401` means.
  Which routes are public is otherwise unchanged; `/metrics` in particular is still
  served without a credential, now by declaration rather than by accident.

### Added

- **Panorama models can say which Atlas resource an element means.** An ArchiMate
  element in a Panorama model now carries **Atlas bindings**
  ([ADR-0189](docs/adr/0189-panorama-architecture-modeling-and-live-overlays.md)):
  an Application Component names a process application, a Business Process names a
  BPMN process id, an Application Service names a worker or job type, a Node names a
  deployment target, an Artifact names a release. Select an element in the model
  viewer to see what it is bound to, and bind it from a picker of the resources you
  may see.

  Bindings are ordinary ArchiMate properties in an `atlas.` namespace, so a bound
  model stays a standard model: it exports as Open Exchange XML like any other and
  its bindings travel into Archi or any conformant tool. The keys are an allowlist,
  which is what keeps credentials out — `atlas.credentialRef` is refused because it
  was never permitted, and a rejected value is never echoed back.

  **The document stores an opaque id and nothing else.** Names come from the server
  at read time, filtered by what you may see, so a model can never hold a stale copy
  of one. A binding that no longer resolves stays visible and says which of three
  things it is: outside your access, no longer on this server, or a kind this Atlas
  version cannot resolve yet. Removing it would make a broken binding look like an
  absent one, and the model would then look correct.

  **Editing a binding does not reformat your document.** The writer splices the
  bytes it needs to change and leaves everything else exactly as it was — comments,
  indentation, attribute order, and any standard content Atlas does not model.

- **Panorama shows the landscape you already have.** Panorama's landing view is now
  a derived mesh of the whole instance
  ([ADR-0211](docs/adr/0211-panorama-derived-landscape-mesh.md)): applications, the
  processes deployed under them, and the call activities between them, computed from
  what Atlas already holds rather than from anything anybody drew. It therefore says
  something on a server with no architecture model in it at all, and its edges are
  facts the server can point at — a call activity *is* a dependency — resolved
  through the same overrides the engine would follow, so the picture matches what
  would actually run.

  The graph is computed per requesting principal against the existing sharing scopes
  (ADR-0071); nothing new to configure. Where your access cuts a dependency, the mesh
  draws a **restricted** placeholder and keeps the edge instead of dropping it, and
  the legend states how many there are — "this process depends on nothing" would be a
  false statement when it means "you may not see what it depends on". A call target
  that no deployment provides is shown as **unresolved**, which is a different finding
  from a hidden one and is drawn differently. Clicking a process opens it in the
  Operations live view: Panorama owns the landscape, and links into the process and
  instance views rather than repeating them.

  Nothing is stored — the mesh is a projection, recomputed on request, and it never
  writes to an ArchiMate model. Above 400 nodes it collapses to applications and says
  so in the legend rather than handing your browser a graph it cannot lay out; that
  number is measured (a 400-node graph paints in about a second in Chromium), not
  guessed.

  **The landscape also draws what a process depends on besides another process:** the
  **workers** its service tasks name, and the **decisions** its business-rule tasks
  delegate to. That is the question a model cannot answer about itself — a task names
  its worker by name and carries no endpoint and no secret, so nothing inside the
  model can tell whether that name is configured on this server (ADR-0158). A process
  pointing at a worker nobody configured deploys clean and parks its first token;
  here it shows as **unresolved** before anything runs. A worker node carries its name
  and its Worker Type and nothing else — the endpoint and the credential reference
  stay on the server. Two references are deliberately *not* findings, mirroring the
  deploy-time check exactly: one whose job type no managed Worker Type claims is not a
  worker reference at all, and a name authored as a FEEL expression names no fixed
  worker, since which one it reaches is known only at call time. Configured workers
  and registered decisions that nothing references stay off the picture: the mesh is
  the dependency graph, not an inventory.

  **A search box** filters the mesh by name, kind or process id and reports how much
  it is hiding — a filtered landscape otherwise looks exactly like a small one.

- **Panorama opens ArchiMate diagrams.** An architecture model in the Panorama
  library now opens its Open Exchange Diagram views on a read-only `diagram-js`
  canvas, with ArchiMate layer colours and shapes, view tabs, zoom and pan, and
  the same canvas/properties/problems frame as the BPMN and DMN editors
  ([ADR-0189](docs/adr/0189-panorama-architecture-modeling-and-live-overlays.md)).
  Selecting an element or relationship shows its standard type and identifier;
  switching views projects the same reusable model elements into their stored
  positions. The XML remains canonical and byte-preserved: viewing issues no
  writes, while export remains available beside the canvas.

- **A Jira connector.** Atlassian Jira is a first-class connector kind
  ([ADR-0201](docs/adr/0201-jira-connector.md)): a service task marked
  `<atlas:jiraConnector connector operation …>` performs one issue-tracker operation
  against a server-registered Jira instance, off the processor loop and after fsync like
  every other connector. Seven operations cover the loop a process actually runs —
  `create-issue`, `get-issue`, `update-issue`, `transition-issue`, `add-comment`,
  `assign-issue` and `search` (JQL) — and every authored value is literal-or-FEEL,
  evaluated over the variables the task sees.

  What it saves is the four things a REST task had to do by hand: the URL, the auth
  block, Jira's nested body shape (`{"fields":{"project":{"key":…}}}`), and knowing that
  a transition and an assignment are sub-resources at all. A transition may be named by
  the button a person reads in Jira — the connector resolves its id first, so the model
  is not pinned to one workflow configuration — a search follows Jira's paging and hands
  back the issues rather than one page of an envelope, and an extra field keeps the JSON
  shape its FEEL value had, so `labels` stays a list and `priority` an object.

  The site URL and the credential live in the managed connector store and the vault, so a
  move from a test Jira to production is a Console edit rather than a redeploy. Two
  credential shapes are accepted and neither needs a flag to say which it is: `{email,
  apiToken}` is Jira Cloud (HTTP Basic, the way Atlassian documents an API token) and
  `{token}` a Data Center personal access token (bearer). The same fact decides how an
  account is addressed when assigning an issue — `accountId` on Cloud, a username on Data
  Center — so a model never has to know which product it is talking to. The transport is
  Jira's REST API v2, which both products serve and which takes a description as a
  string; v3 would make every model build an Atlassian Document Format tree to write one
  sentence. Authored via a first-class **Jira Connector** service-task type in the
  Modeler.

- **The BMC Remedy connector runs on a worker.** Remedy shipped with an in-process job
  handler only ([ADR-0106](docs/adr/0106-bmc-remedy-connector.md)), which is the
  arrangement [ADR-0164](docs/adr/0164-no-in-process-service-tasks.md) exists to end: a
  login, a create and a logout against somebody else's ITSM host, on the engine's
  single-writer loop. It now has the same split every offloaded kind has
  ([ADR-0168](docs/adr/0168-connector-work-on-a-worker.md)) — the engine resolves the task,
  because only it has the compiled process and the scope chain, and what travels is the
  connector's *name*, the form and the evaluated field values. There is nowhere in that
  payload to put a base URL or a password.

  `atlas worker --connector remedy` serves the kind from its own environment
  (`ATLAS_REMEDY_CONNECTORS`, plus `ATLAS_REMEDY_<NAME>_ENDPOINT`, `_USERNAME` and
  `_PASSWORD`), and a worker Atlas supervises is handed that configuration at spawn out of
  the connector store and the vault — so a Helix instance added in the Console is served
  without anything set by hand. A connector with no endpoint, or whose credential bundle is
  missing or half-filled, is left out rather than handed over incomplete: a named instance
  missing a field makes the worker refuse at startup, which would take down every other
  kind it serves. A worker holding no instance at all parks Remedy tasks instead of leasing
  and failing them.

  **Atlas runs that worker itself, by default** (ADR-0192). The kind
  was opt-in only for as long as there was no worker to hand the credentials to; with the
  handover built, that reason is gone, and a ticket create leaves the engine's loop on every
  installation rather than only where somebody moved it by hand. **Nothing needs to be done
  to upgrade** and nothing changes in any model — the same connector, built from the same
  three values, resolved in a different process — and `--in-process-connectors` returns the
  old arrangement wholesale. The payoff is an AR System reachable only from inside a
  customer's network: a worker sitting there can serve it, and the service account can live
  only in that worker rather than in the engine.

- **A web-scrape task can read an RSS or Atom feed.** The web-scraping connector
  ([ADR-0118](docs/adr/0118-web-scraping-connector.md)) shipped with exactly one way to
  read a document: a CSS selector over static HTML, yielding an array of strings. It now
  carries an explicit `format="html|rss|atom"` and an optional `maxItems="N"`
  ([ADR-0190](docs/adr/0190-webscrape-feed-extraction.md)). In a feed mode one entry
  arrives as one object — `title`, `link`, `description` and `published`, and all four
  keys are always there — so a later step addresses `=schlagzeilen[1].link` instead of
  zipping four unrelated arrays back together. A field the source omits is empty; a
  publication date is passed through as the publisher wrote it, because reformatting it
  would turn a source value into an Atlas interpretation.

  **The format is model intent, and it is decided at deployment.** Atlas does not
  inspect the response to pick a parser. Feeds are routinely served as
  `application/xml` or worse, and a URL that answers differently after a redirect would
  otherwise silently change the *shape of a process variable* — exactly the runtime
  interpretation the compile-don't-interpret invariant exists to prevent. What the
  authored format does change is the Accept header the fetch sends, which is content
  negotiation, not detection.

  **A misleading combination is refused rather than half-ignored.** A feed mode with a
  CSS `selector` or an `attribute` fails at deploy, as do an unknown format and a
  negative or non-numeric `maxItems`. `maxItems` cuts after extraction in document
  order — the first N selector matches, or the first N feed entries.

  **Nothing about an existing model changes.** `html` is the default and no bound is
  the default, so a web-scrape task authored before this returns the same `[]string` it
  returned before. The trade-off worth knowing when you write a new one: the element
  type of the result variable now depends on the authored format — strings for HTML,
  objects for a feed — and Atlas has no static variable schema to check that against, so
  the Modeler says which you get and this note says it too.

  Nothing moved onto the engine to make this work: the fetch and the XML decoding happen
  on the web-scrape worker, after fsync, and a document that will not decode as the
  authored format fails the job and retries like any other scrape. Authored in the
  Modeler through a **Format** choice on the Web Scraping Connector, which hides
  Selector and Attribute in the feed modes because the compiler rejects them there.
  [`examples/blick-schlagzeilen.bpmn`](examples/blick-schlagzeilen.bpmn) is the
  end-to-end example: a news feed into a process variable, filtered by a FEEL script,
  routed on by a gateway.

  Deliberately out of scope for this slice, and worth knowing before you plan around it:
  extension namespaces such as Dublin Core and Media RSS are ignored, and there is no
  conditional request (`ETag`/`If-Modified-Since`), no feed discovery from a page's
  `<link rel="alternate">`, and no cross-run deduplication. A scrape stays a read-once
  GET; what has already been seen is the process's business, not the connector's.

### Changed

- **The Active Directory mockup is switched on in the Console now, not on the command line.**
  [ADR-0181](docs/adr/0181-ad-connector-mock-mode.md) gave the AD connector a mockup mode and put
  the switch in the worker's environment. The reasoning — the operator owns this decision, not the
  model — still holds; the ceremony did not. Since [ADR-0182](docs/adr/0182-ad-default-offload.md)
  the AD worker is a child Atlas starts itself, so "set the variable" meant **restart the server**,
  and restarting the worker from the Workers view did not help: it re-inherits the environment of
  the running parent, where the variable is still absent. The switch that exists to make drafting
  cheap cost an engine restart, and the person who most wants to flip it is the least placed to
  take everyone else's instance down.

  It now sits in **Console › Connectors**, on an Active Directory card beside the managed connectors
  and the vault: a checkbox, an optional seed file, Save. The AD worker restarts holding the new
  setting and Atlas keeps running — through exactly the rendering ADR-0182 already built to hand
  that worker its bind passwords. The card also says which state it is in, which is a better answer
  to "did that account really get created?" than reading a log.

  **Nothing changes until somebody uses it.** No stored setting means the server's own
  `ATLAS_AD_MOCK` keeps deciding, exactly as before. A stored one decides either way — a stored
  "off" overrides an inherited "on", because a switch that says off while the worker still
  simulates would be lying to the person who flipped it. The Console writes the same two variables
  a hand-run worker reads, so a worker in another network is configured exactly as it was, and
  there is no private channel between a supervised worker and its parent. The model still says
  nothing about being mocked. See ADR-0193.

### Fixed

- **An upgraded server no longer hands a returning browser half of the old UI.** The
  embedded UI is a graph of ES modules that import each other by name, and it was served
  with **no cache validator at all**: an embedded file has a zero modtime, so
  `http.ServeContent` omits `Last-Modified`, and `http.FileServerFS` sets no `ETag`. That
  leaves the browser to guess how long each file stays fresh, and it guesses *per file* —
  so after an upgrade it could hold a new `editor.js` beside a cached `formviewer.js` and
  die on `does not provide an export named …`, with a hard reload the only way out. Every
  asset now carries a strong `ETag` over its own bytes and `Cache-Control: no-cache` —
  "reuse it, but ask first", not "do not store it": the browser keeps its copy and
  revalidates, and an unchanged file costs a 304 with no body.

- **A menu's flyout opens to the right, and can be reached.** The "Move to" submenu on an
  artifact row opened to the *left*, which is not where a submenu opens anywhere else, so
  the hand went the wrong way first; it opens right now, and flips left only when the
  right would run off screen. Reaching it was the worse half. The flyout is
  `position: fixed` — a card's overflow would clip it otherwise — and was shown by
  `.submenu:hover`, with a 5px gap to cross. A hand moving diagonally from the row to the
  flyout crosses the menu rows in between, and every one of them is outside the pair, so
  the flyout closed under the hand before it arrived: getting into it was a knack rather
  than an action. It now sits flush against the parent menu, and which flyout is open is
  held in a class rather than in `:hover`, so it survives a moment (260ms) after the
  pointer leaves — the diagonal reach is forgiven, settling anywhere else still closes it,
  and dismissing the menu closes it at once rather than after the grace period.

- **Every properties group in the Form and DMN editors reads the same again.** form-js and
  dmn-js mark a group whose entries are all unset with the class `empty` — their own state
  flag, on the group's header. `app.css` carried a bare `.empty` for our "nothing here yet"
  placeholders: centred text and 34px of padding all round. Nothing scoped it, so it reached
  straight into the vendored panel, and every unset group became a **68px** block against
  the **27px** of the groups that happened to have something set — with its title pushed
  inward by the padding and clipped by the centring, so *Custom properties* appeared as
  *Custom p*. Six rows in two shapes, for no reason a reader could see. The placeholder rule
  is now held **off** that panel rather than overridden inside it, so the vendored widget's
  own styling stands rather than being replaced by more of ours; our placeholders elsewhere
  are untouched.

- **The coverage floor is a floor again.** `scripts/check-coverage.sh` compared the total
  that `go tool cover -func` prints, and that number is rounded to one decimal. The
  rounding was not cosmetic — it *was* the comparison, so a repository sitting at
  94.918% reported `95.0` and passed the 95% floor
  ([ADR-0018](docs/adr/0018-test-driven-development.md)), and went on passing for as long
  as it stayed above 94.95%. A floor that a below-floor repository satisfies is not a
  floor, and the gap it hid grew in silence, because every run said OK. The total is now
  computed from the merged profile itself — two sums over the per-block statement counts,
  with no rounding at any step. The repository was brought back over the real line with
  tests for behaviour that had none rather than with filler: the connector-name collisions
  that would hand one supervised worker another's credential (mail's was covered, Entra's
  and Remedy's were not), an `ATLAS_TOKEN` set to something this server will not accept,
  a resolved job detail a worker cannot read, and what the last recovery actually
  replayed. Both outcomes now say where the line is in statements rather than in tenths
  of a percent: how many more would reach the floor, or how many could lapse before it
  fails.

- **The Active Directory mockup no longer asks you for a file path, and a typo in it no
  longer takes the AD worker down.** The mockup's *starting entries* — the accounts and
  groups a process expects to find, because a joiner creates its own account while a
  leaver has nothing to disable in an empty directory — were configured as a **path on
  the worker's host**, typed into an org-wide Console that cannot see that host. A
  relative one resolved against the supervised child's working directory, which is not
  something anybody can predict from a browser, and the field's free-text shape implied
  a choice among several directories when there is exactly one.

  Worse, it was fatal. A path that did not resolve made the worker refuse to start; the
  supervisor restarts a child that exits, so the AD worker sat in a restart loop — the
  Workers view showing **failed**, several hundred starts, and one log line every thirty
  seconds. An optional field made every AD task in the instance unservable, indefinitely.

  Now **Atlas holds the entries**. Pick an LDIF or DSML file or paste the content; the
  Console parses it while you watch, refuses one it cannot read, and tells you how many
  entries it found. Atlas writes the file the worker reads and names it after a digest of
  its own content — which is what makes *replacing* a seed actually reach a running
  worker, since the supervisor restarts a child only when its rendered environment
  differs. An *Example* button fills in a small directory (an OU, two accounts, a group)
  for the common case of not knowing what to put there. And a seed a worker cannot read
  now starts an **empty** directory with a warning instead of refusing to start: a mock
  touches nothing real, so an empty one costs a leaver one visible incident rather than
  costing every AD task an outage
  ([ADR-0202](docs/adr/0202-atlas-manages-the-ad-mock-seed.md)).

  The request carrying it also has its own size limit now — 256 KiB, refused as too
  large rather than silently truncated. It shared the theme's 4 KiB before and was read
  through a truncating reader, so any real directory export came back as "invalid JSON
  body".

  `ATLAS_AD_MOCK_SEED` still takes a path for a worker you start yourself, which Atlas
  has nowhere to write to.

- **An Active Directory `create-user` with an empty entry object no longer crashes the
  worker.** The connector wrote the default `objectClass` into the job's attribute map,
  and a `create-user`, `create-group` or `create-contact` whose `entryVariable` resolved
  to nothing left that map nil — so a misspelled variable name panicked the worker with
  `assignment to entry in nil map`, against a real domain controller exactly as readily
  as against a mockup. Such a create is now refused, saying what is empty, which also
  prevents the quieter bad outcome: an account created in a real directory carrying an
  objectClass and no name.

- **Active Directory is a connector you configure, like every other one.** AD was the
  one credential-bearing integration an operator could not create: the domain
  controller's URL and the bind account lived in the *model*, on every task. That put it
  on the wrong side of the line the rest of the catalogue draws — mail, Entra, Remedy,
  Jira, SharePoint and the three SQL products are records you add in the Console, and
  AD is a domain controller with a service account and a password, not an address like a
  REST endpoint. It had inherited the model-authored shape from the LDAP connector
  rather than from an argument
  ([ADR-0206](docs/adr/0206-ad-as-a-console-connector.md)).

  Now **Console › Connectors › New connector › Active Directory**: the LDAP URL, and a
  credential reference naming a vault bundle `{"bindDN": …, "password": …}` — the Remedy
  and Entra shape, so the record holds no credential and not even the service account's
  name. A task then says `connector="prod-forest"` and nothing else about the directory.

  **Several directories are several connectors,** served by one worker — which is the
  question that had no good answer before. You do not need a worker per forest, and one
  would not separate anything: jobs are handed out by type, not by target, so two AD
  workers would race for the same queue.

  **Nothing existing breaks.** A task carrying its own `url`, `bindDN` and `bindSecret`
  compiles and runs unchanged. Only *both at once* is refused rather than resolved by
  precedence — the two point at different forests, and a silent winner writes to the
  wrong one.

  The endpoint's scheme is checked when you save: AD refuses to set a password over an
  unencrypted channel, so an `ldap://` directory works for every operation except the one
  a joiner needs most, and would otherwise only say so on a real run.

- **The AD mockup keeps several directories apart.** It served every URL from one set of
  entries, so a process addressing two forests found that creating the same account in
  the *second* failed with "entry already exists" — which no real pair of domain
  controllers would ever do. The mockup was least trustworthy in exactly the topology
  that most needs one. Each LDAP URL now gets its own in-memory directory, with its own
  entries **and its own DirSync change history** — a shared counter was the subtler half
  of the same bug, since a reconciliation loop over one forest would have reported writes
  that happened in another, with a cookie making it look authoritative. The starting
  entries are a template: every directory gets its own copy and diverges from the first
  write. The switch itself stays org-wide on purpose — simulating one directory while
  really writing to another is a half-state whose whole risk is that it looks like a full
  mockup run.

## [0.4.0] — 2026-08-26

This release is about connectors you can actually run. `--supervise-connector` gives
any connector kind the pairing the four Atlas offloads had by default — its own worker,
started by the server, handed the server's token at spawn — so a kind that was reachable
only by running `atlas worker` yourself now takes one flag, on an authenticated server
included. **Active Directory runs on a worker by default**, with the engine rendering the
bind passwords its *deployed* models name into that worker's environment, and
`ATLAS_AD_MOCK=1` serves the whole joiner/mover/leaver lifecycle against a directory in
memory that refuses what a real domain controller refuses — so an identity process can be
run before anybody goes near a real forest. Entra ID can now be asked a question, not only
told what to do. In the Console the catalog, the configured connectors and the vault leave
Organization for a **page of their own** at `#/console/connectors`, and the Modeler's Type
picker is one line per kind rather than four screens of cards.

**Multi-instance loops got the pass they were owed.** A loop inside an ad-hoc subprocess
and a loop a gateway routes into both keep their results; a loop that also has an I/O
mapping no longer runs past its maximum; a loop body no longer writes a null over the
process; the badge counts rounds rather than activations; and a finished round no longer
leaves a token behind on the replay. A loop also **says what it was told to repeat while**
and what it decided each round, so one that ends early is readable rather than guessed at.

**Two silent modelling mistakes are now refused at deploy** instead of doing something
plausible and wrong: a dotted write target (`variable.dotted-target`), which used to create
a variable with a dot in its name beside the structure it was meant to extend, and a mapping
onto `loopCounter` (`loop.counter-mapping`), which overwrote the count the engine reads back
to know which round finished. **Both refuse models that deployed before**; each entry names
the element and the way to write what was meant. Running instances are unaffected — both
rules run at deploy.

### Added

- **Entra ID delta queries — `delta-users` and `delta-groups`**
  ([ADR-0172](docs/adr/0172-entra-id-connector.md), amended). Change detection instead of
  a full compare: a delta operation enumerates the directory the first run and returns
  only what changed on every run after, which is what makes an hourly identity sync
  affordable. The `@odata.deltaLink` cursor round-trips through the process — the
  operation takes an optional `deltaLink` (empty on the first run, the previous run's
  cursor thereafter) and returns `{ value, deltaLink }` so a model persists the cursor
  and hands it back next time. Deletions arrive in `value` marked `@removed`; `$select`,
  `$top` and the `maxUsers` cap apply, while `$filter`/`$search`/advanced query do not
  (Graph's delta endpoint runs none of them, and the compiler refuses them at deploy).
  This closes the third Entra capability tracked in [issue #433](https://github.com/pblumer/atlas/issues/433).
  Fixed alongside: `newPassword` and `deltaLink` were being dropped from the job payload
  the engine hands the worker, so `reset-password` resolved an empty secret — both now
  cross the wire and are covered by a worker round-trip test.

- **`--supervise-connector` — a connector kind served by a worker Atlas starts itself**
  ([ADR-0164](docs/adr/0164-no-in-process-service-tasks.md),
  [ADR-0168](docs/adr/0168-connector-work-on-a-worker.md),
  [ADR-0181](docs/adr/0181-ad-connector-mock-mode.md)). Offloading a kind and running a
  worker for it were only ever paired for the four Atlas offloads by default:
  `--offload-connectors` takes a kind off the engine and leaves its jobs parked for a
  worker somebody else runs, and `--supervise` names a *job type* with an external
  command, so neither can ask for a built-in connector. Every other kind was therefore
  reachable only by running `atlas worker --connector <kind>` yourself — and on a server
  with `--auth` that is not friction but a wall: the job pull is authenticated, and the
  only bearer credentials are the server's own internal token (minted per boot, handed
  to its children, never published) and a deploy token allowlisted to two endpoints.
  There is nothing an outside worker could hold, so the kind's jobs park forever. That
  hit the AD connector's mock mode, whose follow-up in ADR-0181 anticipated exactly this,
  and every worker-only kind alike — `entra` above all, which has no in-process handler at
  all. Naming a kind here now gets it the same pairing the defaults get: its own
  supervised worker, handed this server's token and environment at spawn, and the kind
  taken off the engine so that worker is what leases its jobs. A worker-only kind is
  supervised without being offloaded, since it has no in-process handlers to remove and
  the offload list refuses it. Asking for a kind that is already supervised is a no-op
  rather than a second worker racing the first, and an unknown kind is refused at startup.
  So `atlas serve --auth --supervise-connector ad` with `ATLAS_AD_MOCK=1` in the server's
  environment is a full mockup directory on an authenticated server, configured with one
  flag and one variable.

- **The Active Directory connector gets a mock mode, so an identity process can be run
  before anybody goes near a real forest.** The connector could do the whole lifecycle
  (ADR-0166) and could run on a worker (ADR-0168), and neither made it *testable*: the
  directory a joiner/mover/leaver touches is production by definition, so the only ways to
  try a draft were to swap the task for an ADR-0120 mockup — which throws the AD
  configuration away and proves nothing about the task — or to find a test forest. Now
  `atlas worker --connector ad` with `ATLAS_AD_MOCK=1` serves AD jobs against a directory in
  its own memory. Every line of the connector but the transport is the production one: the
  mock implements the same `Dialer`/`Conn` the go-ldap adapter does, so `Resolve`, `Run`,
  `dispatch` and the DirSync pass are the code that runs against a domain controller.

  **The model does not change**, and that is the point of putting the switch on the worker
  rather than on the task: a mockup flag in the model is a flag that eventually gets deployed
  still set, and a task reporting success while touching nothing is the worst failure
  available. Promoting a mockup run to a real one is an environment variable on a worker, not
  an edit and a redeploy.

  **It refuses what Active Directory refuses**, because a mock that accepts more teaches a
  model to be wrong and the lesson arrives in production: a replayed create fails with "entry
  already exists" (delivery is at-least-once), `unicodePwd` may only be written over an
  encrypted channel and must carry AD's quoted UTF-16LE encoding, a group member cannot be
  added or removed twice, a container with children cannot be deleted, a simple bind naming a
  DN with no password behind it is refused — what an unset `ATLAS_CONNECTOR_<REF>_TOKEN` looks
  like on the wire — and DirSync is answered only at a naming context root. The delta is real:
  every write stamps a change counter, a delete leaves a tombstone carrying `isDeleted`, and
  the cookie *is* that counter, so a reconciliation loop converges against the mock exactly as
  it does against a real domain controller, `more` signal and `maxEntries` cap included.
  A set-password is checked and then dropped — the entry records `pwdLastSet`, never the
  value, and the operation journal redacts it.

  `ATLAS_AD_MOCK_SEED` fills the directory from an LDIF or DSML file, read with the
  directory-file connector's own parser (ADR-0171), because a leaver has nothing to disable in
  an empty forest. And the worker says what it is doing: a warning at startup that no
  directory is being written, then one line per simulated operation in the log the Workers
  console shows (ADR-0157) — that log being the only place a mock worker is distinguishable
  from a working one. See ADR-0181.

- **The handbook takes on the process developer's role, and builds a whole application in
  front of you.** Everything the handbook taught so far was a *piece*: a recipe per BPMN
  pattern, a tutorial per process. The question it left unanswered is the one an author
  actually faces on day one — not "how does a boundary timer work" but "what belongs in the
  form, what in the decision table, what in its own process, and how do the four artifacts
  become one deployable thing". Two new chapters answer it. **Die Rolle des
  Prozess-Developers** states what the role owns (the application, the models, the forms,
  the decisions, the connections, the releases), the eight-step working cycle from clarifying
  the domain to migrating running instances, a placement table that settles nine cases out of
  ten (a field that depends on another → the form; a rule with many combinations → a DMN
  table; something that can happen at any time → an event subprocess), eight rules of the
  craft, and a definition of done. **Werkstatt: eine kleine Applikation bauen** then builds
  one — an applicant-management application of two processes, three forms and one decision —
  in ten steps, each explaining *why that element*, and installs it into the reader's own
  instance at the press of a button: application, decision, forms, both drafts, publish as
  release 1, one case started. The models render from the page itself, so the diagram in the
  chapter is the diagram the Modeler shows.

  The application is deliberately past toy size, because that is where the interesting
  questions live: the DMN decision's output `runden` is a **list**, and a sequential
  multi-instance call activity iterates it — so *the decision table decides how often the
  interview subprocess runs*, and a third round is a row in a table rather than a change to a
  model. The call is an explicit **contract** (`propagateAll…="false"` plus an `ioMapping`),
  and *both* ends of the called process satisfy it — including the one where the deadline
  expired and nobody answered. Every foreign system is an ADR-0120 **mockup**, so the whole
  thing runs end to end before a single real connection exists, and "create the contract in
  the HR system" fails one run in five on purpose, so the error boundary that turns an
  unreachable system into a task for a human is something the reader *experiences* rather
  than reads about. It ships as `examples/bewerbermanagement/` with its own README, and
  `go test ./examples` regenerates the copy embedded in the handbook, so the chapter cannot
  drift from the files it teaches.

- **Entra ID can be asked a question, not only told what to do** (ADR-0172, amended).
  The Entra connector could address a user and change one; it could not *find* one.
  A joiner/mover/leaver process routinely starts from a question — who is in this
  department, which accounts are still enabled, does this UPN already exist — and
  `get-user` needs that answer as its input. **`list-users`** authors an OData
  `$filter` (literal-or-FEEL, so a process can list the department it is actually
  about), a `$select` projection, a page size and a cap, and writes every matched
  user into one process variable.
  **The connector follows Graph's paging itself.** A collection in Graph arrives one
  page at a time behind an `@odata.nextLink`, and a model never sees it: the result
  variable receives the whole listing as a JSON array, not a page of it — a process
  looping over a continuation token would be carrying Graph's paging protocol in its
  diagram, which is the encoding this connector exists to keep out of one.
  Three bounds keep that safe. `maxUsers` defaults to 1000 and a listing that exceeds
  it **fails rather than truncating**, for the reason the LDAP connector's entry cap
  does — a short result set is a wrong answer, not a partial one. An unbounded
  listing still terminates, at a ceiling of 1000 requests, so a server that offers a
  next page forever fails visibly instead of holding a worker until its lease
  expires. And a continuation may only stay on the connector's own endpoint: a paged
  result is the one place a *response* names the next URL, and the token behind it
  can read an entire directory, so a redirected page is refused rather than followed.
  **A listing can also run as an advanced query.** Graph gates `endsWith`, `ne`, `not`
  and `$search` behind advanced query support, and refuses them otherwise — "which
  mailboxes are on this domain" is an `endsWith`, so this was not an exotic corner.
  `advancedQuery="true"` sends the two halves Graph only accepts together, the
  `ConsistencyLevel: eventual` header and `$count=true`, so there is no way to author
  half of it. A `search` term carries Graph's own quoting (a compound `"a" AND "b"`
  has quotes inside it, so the connector encodes the term but does not invent quotes
  around it) and implies the advanced query, because Graph runs a `$search` no other
  way. It is never inferred from the filter text: a FEEL filter has no text at deploy,
  and eventual consistency means a listing may be slightly stale — the author's call,
  not a substring match's. The header rides on every page, since Graph rejects a
  continuation fetched without it. `$orderby` stays the REST connector's.

- **A loop says what it was told to repeat while — and what it decided**
  (ADR-0077/ADR-0133). A looping activity's replay could say which round a step was and
  what that round read and wrote, but not the one thing an author asks when a loop does
  not do what they meant: *what was it told, and why did it stop there?* Every round now
  carries its loop's condition as the author wrote it, the values that condition's own
  variables held for that round, the stated maximum, and what followed — another round,
  or the end of the loop with the bound that ended it (the maximum, the condition no
  longer holding, or the engine's safety ceiling). The loop's body carries the same
  reading for the loop as a whole, including how many rounds ran — and, for a
  multi-instance, **what it was told to iterate over and what that name held**, which is
  the one case where a loop does nothing at all and says nothing about it: a collection
  expression that comes out as anything but a list seeds no rounds, so the activity is
  walked past as if it had no work. It shows up in the
  replay's Details tab in prose and on the diagram card in one line, so a model that runs
  nine times because it states no condition says exactly that, instead of leaving the
  reader to guess between a cap, a condition and a bug.
  Nothing is re-evaluated to produce it: the condition and the cap are model facts, read
  through the definition in force at that step's own position (ADR-0162), and whether a
  round led to another is a fact about the log. What the record cannot settle — which of
  its two bounds ended a multi-instance — is left unsaid rather than guessed. Compiled
  FEEL keeps its source text for this (`expr.Compiled.Source`), at deploy time only.

- **A structured variable opens where it stands, and says what shape it is**: the
  Variables tab summarised an object or a list and previewed its raw text, both of which
  truncate — and `[{"Nachname":"Blumer",…` and `{"Nachname":"Blumer",…` differ only in the
  bracket that falls off the left edge. An operator watching a loop hand one element of a
  list to each round read the element as the whole list and concluded the loop was binding
  the wrong thing. The summary now carries the brackets (`[3 items]` against
  `{3 fields}`), so the shape is the first thing read rather than the last, and the row
  expands in place into pretty-printed JSON — the structure, where the reader already is.
  The window is still one step further in, for values too big to read in a row, and an
  expansion survives the 1.5-second poll and the filter that rewrite the rows under it —
  but not a move to another element, whose variables are a different set: carried there,
  an opening nobody asked for reads as "these come open by default". Everything starts
  closed, an open structure is bounded against the viewport rather than a fixed height,
  and the toolbar carries one control — **Expand all**, becoming **Collapse all** once
  anything is open — whenever the table holds a structure at all. A chevron per row is
  enough for one value, but an opened structure's JSON can push the rows either side of it
  off the screen, and a way out that only appears after the fact is not there when it is
  first looked for. Expanding follows the name filter: what is not on screen is not what
  "all" means to the reader looking at it.

### Changed

- **Deploy & run opens the process's start form.** A process whose start event links a
  form ([ADR-0028](docs/adr/0028-forms-and-the-tasks-app.md)) already says what it starts
  with, in a form somebody laid out — labels, required marks, field types and all. Deploy
  & run ignored it and offered the free-form JSON textarea it offers a process with no
  declaration, so the author had to retype, as untyped JSON, exactly the values the form
  existed to collect. The deploy panel now names the form, and Deploy & run renders it in
  a modal with **Send** and **Cancel**, through the same viewer the Tasks app uses; what
  Send submits becomes the instance's start variables, and the form's own validation
  stands between an empty required field and a started instance. The form is asked
  *before* anything is deployed, so Cancel (or Escape) leaves the server exactly as it
  was — "Deploy & run" is one action, and backing out of it should not leave a deployed
  version behind. Deploy only never opens it: nothing is being started, so there are no
  start values to collect.

- **The Modeler's Variables panel says what a variable holds, not just that it exists.**
  It listed a name and who writes it. That answers "does this variable exist"; in front of
  a connector result it does not answer the two questions an author actually has — what
  type is this, and what is inside it. Each row now carries a type badge, and where a
  value can be shown it is shown:
  - **The type, where the model declares one.** A start variable states its own (it used
    to be a word inside the origin line, "start variable · number"; it is a badge like
    every other type now). A form field's component type *is* a type — a checkbox writes a
    boolean whatever it is labelled, a dynamic list binds an array under its path
    ([ADR-0028](docs/adr/0028-forms-and-the-tasks-app.md)). And what a connector kind writes is a fact about
    the kind, known before anything runs: fifteen of the catalog's kinds
    ([ADR-0067](docs/adr/0067-service-task-connector-catalog.md)) now declare their result type
    machine-readably, several of them per operation — a SQL `query` returns rows, `query
    one` a row, an `execute` a count. Where nothing declares a type — a FEEL script's
    result is whatever its expression evaluates to — the row carries **no badge**: a badge
    reads as knowledge, and a guessed one is worse than the blank it replaces.
  - **The value it last actually held**, read from a real instance of this process, with a
    line above the list naming which run it was and its state. A structure opens where it
    stands, with the same collapsed summary the replay's Variables tab uses (the brackets
    carry the difference between a list and one of its elements, which truncated text
    does not) — and opening it is the only way at design time to see that a row carries
    `kundennr` and not `id`. An observed type wins over a declared one, and the badge's
    tooltip names both, so a run that contradicts the model is visible rather than quietly
    overwritten. A diagram that was never deployed — the state of most diagrams being
    written — shows its declarations and no values, which is the honest answer, not an
    error.

- **Connectors are their own page in the Console.** They live at `#/console/connectors`,
  beside Organization in the navigation. The connector catalog, the connectors this instance has
  actually configured ([ADR-0041](docs/adr/0041-connector-management-and-secret-store.md)) and the encrypted
  vault their credentials resolve from ([ADR-0069](docs/adr/0069-engine-internal-encrypted-secret-vault.md))
  were the last three cards of Organization — below the user roster, the groups and the
  brand-colour picker. Organization answers "who uses this instance and what does it look
  like"; a connector is not a person, and as the catalog grew past a dozen kinds the page
  had become mostly integrations with the people at the top. The three move together and in
  the order the work happens — pick a kind, point it somewhere, give it a credential —
  because a token *reference* and the vault secret it resolves to are one setting entered in
  two places. Organization keeps users, groups and appearance. The deep links follow: an
  incident whose model names a connector nobody configured, the incident table's
  "Configure connector ↗", and the handbook's note on where credentials live all point at
  the new page, and the contextual help button on it opens the connector chapter.

- **The connector picker is one line per kind.** The Modeler's Type picker lists nineteen
  kinds ([ADR-0067](docs/adr/0067-service-task-connector-catalog.md)), and each was a two-line card:
  name, then the catalog's one-sentence description underneath. That put a single choice
  across roughly four screens of scrolling — the list stood 2015px tall, and the tallest
  entry alone took 211px — so the way to find a kind was to search for it, and the way to
  discover one was not to. Sixteen of the names then ended in the word "Connector", which in
  a list of connectors separates nothing while pushing the words that do separate them
  further right, into a 270px panel that has no room to spare. A row is now the name without
  that shared suffix, the placement badge parked at the right edge where a column of them
  can be read down, and the description as the row's tooltip — spelled out under the one
  kind actually chosen, which is the one being read rather than scanned past. The same list
  is 1084px, and nothing is lost on the way: the full name is still what the search box
  matches (so "connector" still finds all sixteen), the description is still searched, and
  the heading under the picker still names the chosen kind in full, where it reads as a
  title rather than as one of nineteen. Fixes an unclosed CSS rule that had been swallowing
  the picker's group headings since they were added, leaving them unstyled.

- **Active Directory now runs on a worker by default, and the engine hands that worker the
  bind passwords it needs.** [ADR-0164](docs/adr/0164-no-in-process-service-tasks.md) made
  out-of-process the default for every connector kind a supervised worker could actually
  serve — and Active Directory, of all kinds, was not one of them. Not for want of a worker:
  [ADR-0166](docs/adr/0166-active-directory-connector.md) had built that half. The obstacle
  was the credential. An AD task names its bind password as a *reference* the model authors,
  and that reference resolves out of the engine's encrypted vault, which a worker cannot
  read. Defaulting the kind would have moved every vault-backed directory task to a worker
  holding nothing to bind with, so it stayed opt-in — which meant that in practice, a dial,
  a bind and a modify against somebody else's domain controller kept running on the engine's
  single-writer loop.

  The engine now renders exactly the references its **deployed models** name into the
  supervised AD worker's environment, resolved through the same vault-or-environment
  resolver it used itself. That is the narrowest set that works: the worker holds the
  passwords for the directories the deployed models actually bind to, and nothing else in
  the vault. It is re-rendered whenever a secret changes and whenever a model is deployed
  that names one, and the worker is restarted only when what it holds actually changed — so
  a first AD deploy cycles it once and an ordinary redeploy costs nothing. A reference
  nothing answers to is left out rather than handed over empty, because a blank variable
  reads as a configured blank password; the worker's own error names the variable to set
  instead. Two references that fold to one environment name cannot both be handed over, so
  the second is skipped and said out loud.

  **Nothing needs to be done to upgrade**, and nothing changes in any model: the same
  reference, resolved in a different process. Only the AD worker is given these — a script
  worker, which runs model-authored code and inherits its whole environment, is never handed
  a directory service account, and a test holds that. `--in-process-connectors` still returns
  the old arrangement wholesale. And because a supervised worker inherits the server's
  environment, `ATLAS_AD_MOCK=1` on `atlas serve` puts its AD worker into mock mode
  ([ADR-0181](docs/adr/0181-ad-connector-mock-mode.md)) — one variable, no flags, and a
  joiner runs end to end against a directory that does not exist. See
  ADR-0182.

- **A dot in a write target is refused at deploy** (new rule `variable.dotted-target`).
  Every place a model names a variable to write — a script or decision result, a
  `zeebe:ioMapping` target, a loop's input element or output collection — names a
  *variable*, not a path. Writing `customers.gesamtumsatz` therefore did exactly what it
  said and nothing the author meant: a new variable called `customers.gesamtumsatz`,
  sitting beside the `customers` it was supposed to extend, with no error and a variable
  list that reads as if the field had been added. Nothing downstream finds it either —
  `customers.gesamtumsatz` as an *expression* reads the field inside `customers`, which
  is still absent. A deploy now refuses the model and says so, naming the element, the
  kind of write, and the way to do what was meant: build the structure in the expression
  and write that (FEEL `context put(customers, "gesamtumsatz", …)`).
  **This refuses models that deployed before.** A model with a dotted target must rename
  the target, or move the dot into the expression; running instances are unaffected, as
  the rule runs at deploy. Data-object associations keep their dots: an association's
  `<assignment><to>` *is* a member path (ADR-0058), and a dot there means what it says.

### Fixed

- **The handbook's recipes are compiled by a test now — and one of them did not deploy.**
  The recipe chapter ships 28 models as XML inside the page, each with a button that
  deploys and starts exactly that XML. They are the most-copied models Atlas has and the
  only ones no test ever parsed: `go test ./examples` walks `.bpmn` files on disk, and a
  recipe is not a file. `variable.dotted-target` above therefore turned the ioMapping
  recipe into a model the deploy gate refuses, and the page went on teaching
  `target="scoring.value"` while its own ▶ button failed for every reader who pressed it.
  The recipe now nests where nesting belongs — `source="={value: result}"` into
  `target="scoring"` — and explains why, since the dotted target is exactly what a reader
  reaches for next. Two new tests give the recipes the floor every shipped model has: each
  one compiles through the same `compiler.ParseAll` a deploy uses, gate included, and each
  card's `data-proc` must name a process its own model declares — so the next compiler
  rule catches the documentation with the code.
- **The call-activity recipe no longer promises an incident that never comes.** Its hint
  said that without a deployed `kyc-check` the instance "pauses with an incident (which
  you can inspect nicely in Operations)". It does not: the call activity parks with the
  token on it, no child instance and no incident — ADR-0076 leaves deploy-then-retry and
  an incident as follow-up work — so a reader who took the hint at its word went to
  Operations looking for the one thing that is not there. The hint now describes the
  parking it really does, and says to deploy the called process and start again.
- **A server that requires a login no longer calls itself single-user mode.** The account
  menu's label was written for the case where nobody *can* sign in — enforcement off, the
  API and UI open — but it was rendered whenever nobody *is* signed in, which on a server
  with `--auth` is the login screen itself. So an instance that was refusing an operator
  entry told them, in the menu right beside that refusal, that it had no login at all. The
  tooltip on the same button had told the two apart all along; only the menu did not. It
  now reads "Not signed in" where a login is enforced and keeps "Single-user mode" where
  it is the truth. Found while diagnosing an instance whose operator concluded from that
  label that a deploy had turned their authentication off.
- **The Modeler stops guessing where a task's work runs — and starts saying it in all
  three panels that choose an implementation**
  ([ADR-0164](docs/adr/0164-no-in-process-service-tasks.md),
  [ADR-0168](docs/adr/0168-connector-work-on-a-worker.md),
  [ADR-0173](docs/adr/0173-generic-sql-connector.md)). Every kind but the plain job
  worker carried an **in-engine** badge, decided by a constant compiled into the
  browser — written when that was true of all of them, and left behind twice over.
  Five kinds (Active Directory, Text File, E-Mail, script, Web Scraping) now run on a
  worker the server starts and supervises *by default*, and the SQL and Entra ID connectors were born on
  a worker with no in-engine form at all — so the badge contradicted the E-Mail
  connector's own runtime, and sat directly beside "…against a SQL Server database **on
  a worker**". It could also never reflect `--offload-connectors` or
  `--in-process-connectors`, which are the server's command line. The Modeler now asks
  the server (`GET /api/v1/connector-kinds`), which derives the answer from the registry
  an offload actually removes a handler from, and the badge says one of four things:
  *in-engine*, *in-engine* with no worker form to move to, *on a worker* (offloaded
  here), or *worker only* (born there). The notice under the chosen kind follows, so a
  kind that is already on a worker is no longer advised to move to one, a kind with no
  out-of-process form is not given advice it cannot take, and a kind whose credential
  lives at the worker says so. A kind the server reports nothing for — the plain job
  worker, the Mockup — shows no badge, and so does a Modeler that cannot reach a server:
  silence rather than a confident wrong answer.

  The same badge now appears in the two panels that never said anything at all, while
  authoring work the same flag moves. A **script task** says where its language runs, *per
  language*: scripts are among the kinds offloaded by default, and since each language can
  also be turned off on its own, Python can be waiting for a worker on a server where
  PowerShell is not. What it is told differs from a connector's advice, because it has to:
  an in-engine script is not told to "prefer a job worker" — it cannot become one — but
  that a hanging script holds the engine's loop with it, and an offloaded one that the
  interpreter has to exist where that worker runs. A **business rule task** says it per
  binding, embedded DMN and temis moving separately; its Evaluation select stops claiming
  a placement in an option label ("In-engine (embedded DMN)" → "Embedded DMN — a decision
  deployed here"), since `--offload-connectors dmn` made that label false too. A FEEL
  script still says nothing: it is evaluated inline and creates no job to place.

- **A loop contained in an ad-hoc subprocess keeps its result too**
  ([ADR-0077](docs/adr/0077-multi-instance-activities.md) with [ADR-0138](docs/adr/0138-adhoc-subprocesses.md)).
  The sibling of the gateway fix in this release, and the last of them. Activating a node
  says, among other things, whether what is being activated is a multi-instance activity's
  *body* — the scope that seeds the iterations and, once they have drained, promotes the
  assembled output collection into the enclosing scope. An ad-hoc subprocess activates its
  contained activities itself rather than over a sequence flow, and that activation left the
  role out: a loop inside an ad-hoc ran every iteration and collected every result, then
  dropped the lot on the way out, so the ad-hoc's own output mapping had a null to hand on.
  The rule now lives in one function (`miRoleOf`) that every activation site shares — taking
  a flow, entering an ad-hoc, running a compensation handler — because this is the second
  time it was decided in a copy and the second time a copy forgot it. Covered by a replay
  test as well as a live one: the role is a fact in the log, so a loop parked mid-sequence
  in an ad-hoc still promotes its collection after a crash.

- **A loop a gateway routes into keeps its result** ([ADR-0077](docs/adr/0077-multi-instance-activities.md)).
  Taking a sequence flow is one operation with one rule about multi-instance activities: the
  flow activates the *body*, the scope that seeds the iterations and, when they have all
  drained, promotes the assembled output collection to the enclosing scope. Every
  flow-taking behaviour went through the shared primitive that knows this — except the
  exclusive gateway, which built the element instance itself and left the role out. The loop
  still ran: the seeding gate does not look at the role, so every iteration executed and
  wrote its output element into the collection. Only the ending was wrong. The body completed
  as an ordinary activity, nothing promoted the collection, and the scope holding it was
  dropped — so a multi-instance whose only entrance was a gateway silently lost its entire
  result, and every expression downstream read null. `= count(bewertungen)` returning 1 for
  three rounds, with no incident and nothing in the log to point at, is the shape that bug
  took. The gateway now takes its flow through the same primitive as everything else.

- **A new validation rule can no longer take down a running server**
  ([ADR-0177](docs/adr/0177-reload-skips-the-deploy-gate.md)):
  deployed definitions are recompiled from their records at startup, and that reload ran
  the deploy-time validation gate again — so a rule added to the compiler *after* a model
  was deployed refused it on the next upgrade, and refusing it failed the startup load.
  The server exited, the supervisor restarted it, and it exited again: a crash loop over
  a model nobody had touched, with every other definition and every running instance
  unreachable behind it, and no way in, because the API that could fix or replace the
  definition only exists once the server is up. The way out was to clear the data
  directory or hand-edit XML inside a JSON record.
  Validation is a gate on *deploying* a model, not a condition for running one — the
  compiled process is identical either way — so the reload no longer applies it. A
  definition that passed the gate of the day it was deployed comes back and keeps
  running, its instances advance unchanged, and Atlas warns once per record
  (`event=deployment.reloaded_with_problems`) naming the deployment and the rules it
  would fail today, so the drift is visible rather than silent. Deploying that model
  still fails, with the rule named, where the author can act on it. A record that yields
  no compiled process at all — it does not decode, names no such process, holds an
  expression that will not compile — is still a hard startup error, since there is
  nothing to bring back; it now names the record's path so it can be acted on.
  The DMN models snapshotted with a deployment reload the same way, and were the same
  outage arriving through a dependency bump: a decision that no longer compiles under a
  newer temis failed the startup load and took the server with it. They are now
  registered with their diagnostics reported, which is safe because temis leaves the
  rest of a model compiled and the affected decision present but not executable — so a
  broken decision fails when something evaluates it, as a job error the engine already
  has an answer for, while every other decision in the model keeps answering. A DMN
  model temis cannot parse at all stays a hard startup error, as before.

- **A structure in the Variables tab no longer comes open by itself** — and the way out
  of one appears again. Every table in a view is handed to the shared sort/filter helper,
  which drove each row's `hidden` from what its filter matched. The rows that hold an
  expanded value are not data rows, but the helper owned their `hidden` too, so it forced
  every structure open — on arrival, and again on every rewrite of the rows, which is
  every 1.5 seconds. Clicking one closed it for an instant. And because the toolbar's
  **Collapse all** watches the openings the *reader* made, and the reader had made none,
  it never appeared: a tab that opened itself and offered no way to close. The helper now
  leaves a row marked `data-dt-detail` alone — it never opens one, and only hides one
  along with the row it belongs to when a filter removes that row; sorting moves it with
  that row rather than stranding it under someone else's. The Workers view's per-worker
  log, the same shape, stops springing open with it.

- **A BPMN file with no layout renders when you import it, not only when you deploy it**
  ([ADR-0124](docs/adr/0124-server-side-auto-layout.md)): BPMN-DI is optional in the
  standard, so a model from a generator, an export from another tool, or a hand-written
  file routinely carries none. Deployed, Atlas already lays such a model out as the
  editor fetches it — imported as a draft it did not, so the *same file* opened onto an
  empty canvas depending on which way it came in. A draft that arrives without diagram
  interchange is now laid out on the way in, and the import says so, because the
  arrangement the author is about to edit is Atlas's rather than the one their file
  described. Reading a draft lays out too, for the ones stored before this. A model that
  brings its own layout is stored byte for byte — generating over an author's
  arrangement would throw it away.



- **A loop's badge counts its rounds, not its activations**: the engine activates a
  looping activity once as the loop's *body* and once more per round, so the replay's
  execution-count badge read 6 for a loop that ran five times — arithmetic the reader had
  to work out and then distrust. On a looping element the badge now says how often the
  loop ran, carries the ↻ so it reads as a round count, and keeps the arithmetic in its
  tooltip. A loop that ran no rounds at all badges **0** rather than nothing: an activity
  reached and walked past is exactly the case worth seeing.

- **Token markers on one element no longer overlap**: the replay fans them out along the
  shape's top edge, but at 16px apart for a 20px marker, so every pair was drawn partly on
  top of the one before it. That is not a corner case — a loop puts two on the shape at
  all times, its body and the round running under it. They now clear each other, and past
  what a shape has room for the rest collapse into a "+n" rather than marching off its
  edge. The legend below the diagram still names every token.

- **A looping activity that also has an I/O mapping no longer loops forever**
  (ADR-0068 with ADR-0077/ADR-0133). A `zeebe:ioMapping` gives an activity a local
  scope, dropped when it completes — and that is the same scope the engine binds a
  loop's own `loopCounter` (and a multi-instance iteration's `item`) into. The drop
  ran first, so the loop then looked for a counter that was no longer there and every
  run read as the first one: a standard loop never reached its `loopMaximum` — a model
  that said "at most 9" ran until someone terminated it, each run reporting
  `loopCounter` 2 — and a sequential multi-instance re-ran its second element forever
  instead of walking the collection. The same early drop emptied a loop *body*'s scope
  before it was promoted, so the output collection an ∥/≡ activity had assembled, and
  everything a ↻ activity's runs wrote — which is a standard loop's whole result —
  were dropped instead of escaping into the process. The loop's element instances now
  drop their scope where they always did, after the loop has read what it keeps there.
  An activity without a loop marker is unchanged.

- **A loop body no longer writes a null over the process** (ADR-0068 with
  ADR-0077/ADR-0133). Each round of a looping activity evaluates the activity's
  `zeebe:ioMapping` outputs over its own scope, where its result is, and promotes them.
  The body then did it a second time — over the body scope, which holds no round's raw
  result — so the mapping evaluated to null and wrote that null into the enclosing
  scope: a variable no run produced, landing on top of whatever the process already held
  under that name. On a standard loop the real result overwrote it a moment later, so it
  showed up as a phantom write on the replay; on a multi-instance activity it was the
  last word. The body no longer re-runs mappings that were never its own. Collecting
  across rounds is still `outputCollection`, and a standard loop's result still escapes.

- **A finished loop round no longer leaves a token behind on the replay**
  (ADR-0077/ADR-0133). The replay keeps a completed element instance visible until the
  activation it causes appears, so a token does not flicker between the two log
  positions it takes to move — but a loop round activates nothing: the body owns the
  activity's outgoing flow and takes it once, when the loop ends. Every finished round
  was therefore left waiting for a successor that never came, so a five-round loop drew
  six tokens stacked on one shape and a runaway loop drew hundreds. A round's token is
  now dropped when the round ends, like a termination or an end event, leaving the body
  and the round running under it.

- **A model that maps onto `loopCounter` is refused at deploy** (new rule
  `loop.counter-mapping`, ADR-0077/ADR-0133): a round's counter lives in that round's own
  local scope — the same scope a `zeebe:ioMapping` input writes into — and the engine
  reads it back to know which round just finished. A mapping onto that name overwrote the
  count, and the loop then ran exactly as the defect above did: past its maximum, until
  someone cancelled the instance. There is no model behind it worth keeping — the counter
  is the engine's to set and every round can already read it — so the deploy now says so,
  naming the element and the mapping.

- **The Modeler stops calling every script task a FEEL one**: the task-type select
  labeled `bpmn:ScriptTask` "Script task (FEEL)" even when the task carried an
  `<atlas:jobScript>` in PowerShell (ADR-0047) — the Language select right beneath it
  said otherwise. It now names both, so the type and the language stop contradicting
  each other.

## [0.3.0] — 2026-08-21

This release moves the work that can be slow out of the engine. `atlas worker` makes
the same binary a worker process, `atlas serve --supervise` runs one for you, and the
**Workers view** says what is queued, what is in flight and who is doing it. The rule
behind it ([ADR-0164](docs/adr/0164-no-in-process-service-tasks.md)) is that Atlas's
own process runs the engine and not somebody else's integrations: with no flags at
all, `atlas serve` now offloads the csv, mail, script and webscrape connectors to a
worker it starts and supervises itself, and every remaining in-process kind is
deprecated.

Around that, the **connector catalogue roughly doubled**: Active Directory (with a
DirSync delta), Microsoft Entra ID, generic LDAP, LDIF and DSML files, SCIM 2.0,
SOAP, and SQL Server, MariaDB and PostgreSQL — the last two families with no
in-process half at all, so a database or directory credential never enters the
engine's address space at all. The REST connector gained an OAuth2 client-credentials
grant, and the text-file connector two more formats and a write direction.

For the people who run processes rather than author them, this is the release where a
**stuck instance stops being a dead end**. Incidents show up on the live diagram, in
the replay and in a list that can be scoped; the connector behind one can be
reconfigured from the incident itself; a task can carry a repair form, so a park is
fixed through named fields instead of raw JSON; a step can be completed by hand with a
mandatory, audited reason; and a running instance can be **migrated** onto a corrected
version, with a dry run that shows what the move would do before anything is written.

### Added

- **`atlas worker`: the same binary, working jobs outside the engine**
  ([ADR-0157](docs/adr/0157-worker-processes-supervision-and-console.md)): a service task's
  work no longer has to run in the engine's process. `atlas worker --server
  http://localhost:8080 --handle send-email=/opt/send.sh` leases jobs of the types it names
  and runs a command per job — whatever JSON object the command prints becomes the variables
  the job completes with, a non-zero exit fails it with stderr as the message. Three pieces
  ADR-0007 designed and could not finish make it possible. An **engine-wide job-type table**
  (`jobtype.Registry`), resolved at every deploy and reload, so `send-email` means the same
  index in every definition — indices were interned per compiled process, so a type-keyed
  pull would have handed one process's jobs to another process's worker.
  **`POST /api/v1/jobs/activate`**, which leases the next jobs of a *named* type and answers
  with everything the worker needs in one call, including the variables visible **at the
  task** — the element instance's scope chain, so an activity-local input mapping shadows the
  instance value exactly as it does in-process, rather than making the worker fetch variables
  separately and race a concurrent write. And a **long poll**, so an idle worker costs one
  parked request instead of a spin. A lease is **fenced** with a token the worker presents on
  completion: a worker that stalls past its lease, has its job handed to someone else and
  then comes back cannot complete work that is no longer its own. Jobs written before the
  table are a declared discontinuity, not a migration (pre-1.0, as `checkpoint/manifest.go`
  already states): they stay listed, leasable by key and completable, and the set drains as
  those instances finish.

- **Atlas supervises its own worker processes**
  ([ADR-0157](docs/adr/0157-worker-processes-supervision-and-console.md)): `atlas serve
  --supervise mailer-1=send-email=/opt/send.sh` runs that worker as a child and keeps it
  running — restart with a doubling backoff capped at thirty seconds, output captured to a
  bounded tail, everything stopped with the server — so moving work out of the engine costs
  a flag rather than a second deployment, and the one-binary, one-command install (ADR-0011)
  survives it. The child is the same `atlas worker` an operator could start by hand, speaking
  the same HTTP protocol: a private path between parent and child would quietly become the
  only tested one, which is how out-of-process work became second-class in the first place.
  Nothing from a request ever becomes a command — the supervisor spawns `os.Executable` and
  only that, with an argv built from typed configuration read off the server's own command
  line, so the API can restart a worker the operator already configured and can do nothing
  else to it: it cannot introduce one, and it cannot name a command. Off unless asked for;
  under systemd or Kubernetes the platform owns process lifecycle. A supervised worker is
  handed this server's own internal token, so it can still poll on a server started with
  `--auth`, and one with nothing to serve parks itself with its own exit status instead of
  being restarted forever into the same emptiness.

- **The Workers view: what is queued, what is in flight, and who is doing it**
  ([ADR-0157](docs/adr/0157-worker-processes-supervision-and-console.md)): an external worker
  used to be invisible — an operator saw jobs not moving and had no way to tell whether a
  worker was absent, wedged, or failing. This is why the console came before supervision: a
  restart button on something nobody can see the state of is a worse product than a view with
  no buttons at all. `GET /api/v1/workers` answers in two halves and Operations draws both.
  Every **job type** carries its queue depth, how much of it is leased right now, its
  incidents, and whether Atlas serves it in-process (in which case no external worker can
  lease it). Every **worker** seen this run carries the types it pulls, what it holds in
  flight, and its pulled/completed/failed counts. The state worth catching is the join of the
  two — a type with a growing queue, nothing in flight and no worker against it. Opening a
  worker lists the jobs it ran; a supervised one can be restarted from there
  (`POST /api/v1/workers/{id}/restart`). The view also names the processes each job type
  belongs to, flags a stored type sitting on an index a newer build has since reserved, and
  lists the connector kinds nothing is configured to serve.

- **A connector task runs on a worker, credential and all**
  ([ADR-0168](docs/adr/0168-connector-work-on-a-worker.md)): moving connector work out of the
  engine was blocked on two things, and only one of them was plumbing. The engine now
  **resolves the task's detail into plain values** — which connector, and the literal-or-FEEL
  recipients, URLs and bodies evaluated against the instance's variables — and those travel
  with the leased job, because FEEL is compiled at deploy (ADR-0008/0015) and a worker has
  neither the compiled process nor the scope chain to evaluate it against. The second
  question is where the credential lives, and it is the one that shapes the product: it lives
  **on the worker**. A model still names a connector and never a secret (ADR-0041); what
  changes is which process holds the value behind that name — so the worker that sits next to
  the mail relay or the ERP holds the credential for it, it crosses no boundary at all, and
  the engine stops being worth attacking for someone else's integrations. Configuration
  leaving the Console is a real loss, and the answer is that a worker reports the connector
  names it is configured for when it announces itself, so the Workers view still says which
  names are served, by whom, and which are configured nowhere. A **supervised** worker is the
  exception that keeps the single-node install simple: it is this process's own child, on
  this host, under this user, so Atlas writes the connector's configuration into the child's
  environment at spawn — the same variables an external worker's operator would set by hand,
  never a private channel — which is what lets a kind whose credentials live in the server's
  connector store be offloaded by default at all.

- **An Active Directory connector that speaks AD, not LDAP**
  ([ADR-0166](docs/adr/0166-active-directory-connector.md)): `<atlas:adConnector>` on a
  service task offers the operations a modeler should be picking from — create-user,
  create-group, update-attributes, set-password, enable, disable, move, delete, and add or
  remove a group member — instead of making every process re-encode AD's rules. Those rules
  are the whole point: a password is `unicodePwd` written as quote-wrapped UTF-16LE over TLS,
  disabling an account is the `ACCOUNTDISABLE` bit flipped inside `userAccountControl` with
  every other flag preserved, and a membership change is an incremental add or delete of one
  `member` value. Each is a foot-gun a generic LDAP task would push into the model, where it
  is written once by someone who read it up that morning. A **`sync` operation reads a
  DirSync delta** — one pass per run, presenting the cookie it finds and writing back the one
  the server returns — so a process can ask a directory what changed instead of re-reading it
  whole; it is the only AD operation that reads rather than writes, and the only one that
  needs the replication right, which the connector says plainly rather than failing
  obscurely. The bind password is a secret reference (ADR-0041), and the connector runs on a
  worker (ADR-0168). `examples/` gains GALSync as a process built from it.

- **A Microsoft Entra ID connector** ([ADR-0172](docs/adr/0172-entra-id-connector.md)):
  identity lifecycle in Entra ID (formerly Azure AD) through Microsoft Graph — create-user,
  get-user, update-user, delete-user, enable, disable, and adding or removing a group member.
  A process *could* already reach Graph with the REST connector, which speaks HTTP and JSON
  and now holds an OAuth2 grant; what it cannot do is say what a call *means*. Disabling an
  account is a `PATCH` of `accountEnabled`, removing a member is a `DELETE` of a `$ref`
  sub-resource, and adding one is a `POST` to a `$ref` collection whose body carries an
  absolute `@odata.id` URL that has to name the right cloud. Those are encodings, not
  business decisions, and their failure mode is a 404 that reads like a missing user. Like
  the SQL connectors this kind is **worker-only** — the tenant id, client id and client
  secret live in the worker's environment and the engine holds no Entra credential at all,
  which matters more here than almost anywhere: an app registration with `User.ReadWrite.All`
  and `Group.ReadWrite.All` can create and disable accounts across the whole directory.

- **A generic LDAP connector** ([ADR-0154](docs/adr/0154-ldap-connector.md)):
  `<atlas:ldapConnector>` with search, add, modify, delete and RFC 3062 modify-password
  against any directory. A search writes its entries — DN plus multi-valued attributes — into
  a result variable as a JSON array; add and modify take the entry's attributes from a named
  JSON-object variable, coercing scalars and arrays into LDAP's multi-valued form. Bind is
  anonymous, by password from a secret reference, or **by client certificate**; searches are
  **paged and bounded**, so a directory that answers with fifty thousand entries neither
  truncates silently nor exhausts the worker; a modify can change **individual values**
  rather than replacing an attribute wholesale; and bound connections are **pooled** rather
  than dialled per job. go-ldap is vendored behind a `Dialer`/`Conn` interface — hand-writing
  LDAP/BER is a large, security-sensitive surface for no benefit, and shelling out to the CLI
  tools would need them present on the host and would pass credentials through argv — so the
  pure-Go, single-binary posture (ADR-0011) holds.

- **Directory files: LDIF and DSML** ([ADR-0171](docs/adr/0171-directory-file-connector.md)):
  a separate `ldif` kind reads and writes directory entries as files, in either format. It is
  deliberately not a format on the text-file connector: LDIF and DSML produce
  `{"dn": …, "attributes": {…}}` — the shape an `ldap` search and an `ad` sync return — and
  folding them in would have made **the result shape depend on a dropdown**, so a process
  downstream of that task could no longer be written against a known shape. Keeping them
  apart is what lets a file read feed exactly the handling a live directory read does. It is
  not part of the LDAP connector either: a file is not a server, and needs no endpoint, no
  bind and no credential.

- **Three SQL connectors — SQL Server, MariaDB, PostgreSQL**
  ([ADR-0173](docs/adr/0173-generic-sql-connector.md)): `query` (rows into a variable),
  `query-one` (a single row, or nothing) and `execute` (rows affected), with parameters bound
  positionally rather than pasted into the statement. One kind per product rather than one
  with a dialect field, because a statement written with `$1` is a PostgreSQL statement and
  pointing it at SQL Server is a mismatch a model would otherwise express silently; each kind
  carries its own placeholder form. These are **the first kinds with no in-process half at
  all**: the DSN lives in the worker's environment (`ATLAS_<KIND>_CONNECTORS` plus
  `ATLAS_<KIND>_<NAME>_DSN`), never in the model and never in the engine. That is the
  ADR-0164 rule applied to the credential an organization usually values most, and it is also
  why a DSN is not model data — keeping a password out of a model-authored connection string
  would mean parsing and rewriting each vendor's connection-string grammar, which is a
  credential-handling path invented for the convenience of putting an address in a model.

- **A SCIM 2.0 provisioning connector** ([ADR-0153](docs/adr/0153-scim-connector.md)):
  `<atlas:scimConnector>` with create, get, replace, patch, delete and search against any
  SCIM endpoint — the base URL, resource, resource id and filter as literal-or-FEEL values,
  the body from a named variable. It sends and accepts `application/scim+json`, carries the
  job key as an `Idempotency-Key`, and turns a SCIM error object's `detail`/`scimType` into
  the job's failure message instead of a bare status code.

- **A SOAP / Web Services connector** ([ADR-0165](docs/adr/0165-soap-connector.md)):
  `<atlas:soapConnector>` wraps an authored body in the envelope, sets the
  version-appropriate `Content-Type` and `SOAPAction` (1.1 or 1.2), and turns a `Fault` into
  a job failure that names it. It is model-authored rather than WSDL-bound: binding at deploy
  time buys little for the legacy services that still speak SOAP, whose WSDLs are frequently
  incomplete or non-conformant, and an endpoint is naturally model data.

- **OAuth2 client-credentials for the REST connector**
  ([ADR-0152](docs/adr/0152-rest-connector-oauth2.md)): `authType="oauth2"` with a token URL,
  client id, scope and a client-secret **reference** — so the exchange is a mechanism of the
  connector rather than modeling homework nobody should be doing by hand. Tokens are cached
  per token URL, client id and scope until thirty seconds before expiry, so a run of jobs
  reuses one. A missing secret or a token endpoint that refuses fails the job — retry, then
  incident (ADR-0061) — rather than calling the API unauthenticated and reporting whatever
  the API says about that.

- **The text-file connector reads two more formats, and writes**
  ([ADR-0139](docs/adr/0139-csv-to-json-connector.md), amended): fixed-width and
  attribute-value-pair files join delimited ones, and the connector gained a **write**
  direction. All three describe a table of records and produce the same rows, so they are
  formats of one kind rather than three kinds — unlike the SQL split above, a fixed-width
  layout applied to a delimited file does not quietly produce plausible rows, it fails on the
  first record. A fixed-width column carries its width as `name:width` in the same `columns`
  attribute: required for that format, since a positional field has nothing else to find it
  by, and rejected for the others, because an authored width the connector would ignore is an
  author believing something untrue. Widths count runes, so an umlaut does not shift every
  column after it.

- **The Repository ships connector templates**
  ([ADR-0081](docs/adr/0081-community-marketplace-for-connectors-and-tasks.md),
  [ADR-0167](docs/adr/0167-released-connectors-ship-in-the-marketplace.md)): the SCIM and
  LDAP connectors arrive as installable templates, and the Modeler catalogs every registered
  kind — so a connector that exists in the binary is also a thing you can find without
  reading the changelog.

- **The Modeler offers the registered connectors as a dropdown**: a connector field on a
  service task used to be a name typed from memory, which is a deploy-time failure written at
  authoring time. It now lists what the server actually has, and says when a kind still runs
  inside the engine (ADR-0164) rather than on a worker.

- **A step can be completed by hand, and the record says who and why**
  ([ADR-0159](docs/adr/0159-manual-task-completion-audit.md)): an instance can park on a task
  that will never succeed here — a connector refuses the call, the far system is unreachable,
  or the work was simply done out of band and the account really was created. *Resolve &
  retry* only repeats what cannot work. Every incident row now offers **✓ Complete
  manually…**, with optional output variables and a **mandatory reason**:
  `POST /api/v1/jobs/{key}/complete` refuses a blank one with 400, and the dialog says so
  before the request rather than after it. What makes it safe is that the intervention is a
  durable fact rather than an invisible shortcut — a new `OperatorActionValue` history
  record, keyed under its instance exactly like the variable audit (ADR-0098), carrying who,
  when, which element, which job and why. It is minted only behind an explicit `Manual` flag
  on the command, never inferred from a non-empty reason, so a worker completing its own
  leased job can never mint one. The timeline attaches it to the step and the replay renders
  it as a *Completed manually* block, so a step a person forced never reads as one the engine
  drove. The action kind is a byte with a closed vocabulary, leaving room for cancel and
  resolve to join the same record rather than inventing a second mechanism.

- **Variables before *and* after a step** ([ADR-0159](docs/adr/0159-manual-task-completion-audit.md)):
  a timeline step gains `variablesAfter`, the variable fold at the element's *completion*
  position, where `variables` has always been the fold at its activation. A task that writes
  its result on completion — a job's outputs, an output mapping — showed nothing under "as of
  activation", so the replay reported that an element which had plainly produced values had
  none. The Variables tab now offers **Input** and **Output** for a finished element and
  marks what the element itself wrote. It is what makes a forced step reviewable: the reason
  says why it was forced, the output says what was asserted.

- **What an element was handed, on the diagram**
  ([ADR-0161](docs/adr/0161-element-io-on-the-diagram.md)): selecting an element in the replay
  hangs a small card under it — **in**, the step's input-mapping locals as they actually
  evaluated, and **out**, what the element itself wrote, being the difference between the
  variables it saw on entry and the ones it left behind. *still running* is a different
  statement from *wrote nothing*, and the card makes both. A mapping source is the model's
  intent; the evaluated local is the fact, and only the fact is worth putting on a canvas.
  The card is capped at six rows a side, takes no pointer events so a click always reaches
  the element underneath, and is toggleable from the transport bar with the preference
  remembered — it is about how a person reads a diagram, not about one instance. `inputs` is
  deliberately not folded into `variables`: a local belongs to one element instance, and
  merging it into the shared running set would leak it onto every concurrent step's snapshot.
  Nothing new is persisted; these values were already on the log.

- **A loop's rounds are told apart** ([ADR-0161](docs/adr/0161-element-io-on-the-diagram.md),
  amended): a loop runs the same node again and again, so its history was a column of
  identical rows, and the one value that distinguishes them — which round this is — was
  being filtered out as "not an input". `loopCounter` and, for a collection loop, the round's
  item now join `inputs`, and the counter is surfaced as `iteration` on the step, so the
  history can label a row without the frontend fishing a known variable name out of a list.
  **A loop body is a scope, and is now folded like one:** a standard loop holds what its
  iterations write at the body scope so each round can read the last one's result (ADR-0133),
  and a multi-instance loop assembles its output collection there (ADR-0077). Neither was
  folded, so a finished round truthfully reported that nothing in the process scope had
  changed — which the diagram card stated as *wrote nothing* about a round that had plainly
  done work. Bodies are identified from the log (an iteration is activated carrying its
  body's token as `ParentTokenID`), not guessed.

- **An org-wide logo, beside the theme** ([ADR-0148](docs/adr/0148-org-wide-brand-logo.md)):
  `PUT /api/v1/settings/logo` replaces the built-in mark everywhere the Console draws one,
  so an installation can look like the organization running it; a CD Bund colour template
  ships alongside it. `GET` is public, because the login screen shows the mark before anyone
  is authenticated; `PUT` and `DELETE` are admin-gated, because they change what everyone
  sees. Only PNG and SVG are accepted, up to 512 KiB, and the server re-validates the bytes
  against the declared type — the PNG signature, or well-formed UTF-8 with an `<svg` root —
  so a mislabelled upload is never persisted. An uploaded SVG is untrusted markup and is
  therefore never inlined: it is rendered through `<img>`, where SVG script does not run, and
  served with `nosniff` and a strict sandboxing CSP. That is a serving-time guarantee rather
  than sanitisation that has to stay ahead of the next trick. The file lives in the settings
  directory under the shared atomic-write discipline, so the design-time backup (ADR-0107)
  captures it with no extra wiring.

- **"What's New" on the Console landing page**: the Welcome view gains a compact,
  collapsible feed of the newest user-facing changes in plain language — bilingual (DE/EN,
  per-visitor toggle), each entry linking to its ADR or PR and, where the UI is involved,
  carrying a short step-by-step tutorial and a **Try it** deep link into the screen it talks
  about. `CHANGELOG.md` stays the single source of truth: `scripts/whats-new/gen.mjs` derives
  each entry's structure from it and merges `scripts/whats-new/overrides.json`, which
  supplies the layman-friendly summaries and can hide a dev-only bullet. The generated
  `whats-new.json` is committed and served off the embedded FS, so the web UI stays buildless
  (ADR-0012) — and CI regenerates it and fails on the diff, so a changelog entry cannot
  silently leave the feed stale.

- **Job leases: a worker can hold work, and a dead worker's work comes back** (v0.2.0
  programme F, [ADR-0007](docs/adr/0007-job-worker-protocol.md), amended): activating a job
  takes it off the activatable index for a bounded time and records who holds it, so two
  workers pulling the same type are not handed the same job. When the lease elapses the job
  is offered again — which is what makes a worker crash recoverable with no operator
  involved. `POST /api/v1/jobs/{key}/activate` grants one (409 while someone else holds it),
  and the lease survives a restart: it is durable state rebuilt from the log, expiry timer
  and all.

  The mechanism is the one ADR-0111 already proved for retry backoff — hold the job off the
  index, arm a timer, let the timer put it back — rather than a second one. Both holds can
  sit on one job at once (a worker leases it, then fails it with a backoff), so each timer
  releases **only its own hold** and the job returns when nothing holds it; otherwise a
  lease expiring mid-backoff would hand out a job the worker asked to defer.

  **Two findings came out of building it, both recorded in the ADR.** The lease is not
  stored in `JobValue.Deadline` because that field already means the *user task due date* —
  conflating them took every user task with a due date off the worker-visible index, which
  is how it was noticed. And the type-keyed pull a worker really wants ("give me the next
  `send-email` job") is **blocked, not merely unbuilt**: job type indices are interned per
  compiled process while the activatable index is global, so `send-email` in one process and
  `charge-card` in another both intern to index 16 and a subscriber would be handed the
  wrong jobs. That needs a global job-type registry first. Leasing by key is unambiguous and
  works today.

- **Distributed traces, opt-in** (v0.2.0 programme E,
  [ADR-0142](docs/adr/0142-prometheus-metrics.md), slice 9): point Atlas at an OTLP/HTTP
  collector — `--trace-endpoint http://collector:4318`, or the standard
  `OTEL_EXPORTER_OTLP_ENDPOINT` — and every `/api/v1` request is exported as an
  OpenTelemetry server span. Off unless configured. Metrics say *that* a request was
  slow; a trace says *where* the time went, and an incoming W3C `traceparent` is
  continued, so a request arriving from another traced service lands in that trace
  instead of starting an island.

  **The engine is not traced, and a test enforces it.** A span costs an allocation, a
  clock read and a lock — nothing next to an HTTP request, all three per batch on the
  goroutine that owns the partition. `TestTheWriterIsNeverInstrumented` fails if
  `engine`, `state`, `wal`, `model`, `compiler` or `checkpoint` ever imports a tracing
  package. Probes and the metrics scrape are not traced either: they run forever on a
  timer and would bury the spans someone is looking for.

  **Span names are bounded by the code, not by traffic** — the same rule the metric
  labels are under. A span is named for the route *pattern*
  (`GET /api/v1/instances/{key}`), never the URL that matched it, and the attributes are
  method, route and status; the raw target and query string are not recorded, so a key
  cannot ride along into a backend's index.

  The exporter is written here rather than taken off the shelf, and that is the
  dependency decision: the official OTLP exporter pulls in protobuf and — even in its
  HTTP form — gRPC, 66 gRPC packages and about 13MB of binary, for a service that speaks
  no gRPC anywhere else. OTLP over HTTP has a documented JSON encoding, so Atlas takes
  the OpenTelemetry API and SDK for the parts that are spec-bound and subtle (span model,
  sampling, batching, W3C propagation) and writes the serializer. Measured: **+1.7MB and
  five modules, no protobuf, no gRPC.**

  Three deliberate limits: a 4xx is not an error (it is the caller being told no, and
  counting it as a server failure makes the error rate meaningless), a caller that
  already sampled is always honored whatever `--trace-sample-ratio` says (a
  half-recorded distributed trace is worse than none), and a collector that is down
  cannot take the server with it — export runs on its own goroutine after the response.

- **Import Microsoft Identity Manager (MIM/FIM) workflows as BPMN**: the new
  `atlas import-mim` command converts a MIM/FIM XOML workflow — or an
  `Export-FIMConfig` XML that embeds one — into deployable BPMN 2.0. Control flow
  (Sequence, IfElse, Parallel, While) maps to native flow nodes and gateways, and
  leaf activities map by intent (Approval → user task, Notification → service
  task, and so on). The translation is loss-aware: any construct without a
  faithful BPMN counterpart is preserved verbatim in an `<atlas:mimSource>`
  extension element and listed, with a `native`/`preserved`/`manual-review`
  status, in a per-node report. Every generated model is checked against the
  compiler so it always deploys. Library: `mimimport`. The Modeler exposes it too
  — **Create new → Import MIM workflow (XOML)…** uploads a workflow, opens the
  converted diagram as a draft, and shows the conversion report (status badge and
  note per node) with a shortcut into the Modeler (`POST /api/v1/imports/mim`).

- **A form on the incident — repairing an instance with named fields instead of raw JSON**
  ([ADR-0169](docs/adr/0169-incident-repair-forms.md)): a service, send or business rule
  task may now carry a `zeebe:formDefinition`, meaning "if this task parks, this is the
  form for repairing it". The incident then offers **⚑ Repair…** beside the existing
  **✎ Fix variables…**, rendering that form prefilled from the instance's current values
  — so the person repairing a stuck process at an awkward hour is shown the two fields
  that matter rather than forty variables as a JSON document they must also keep valid.
  Whoever authored the task knows which values its retry depends on; that knowledge had
  nowhere to go until now. Nothing new is written: submitting goes through the audited
  operator override (ADR-0098), so every change still records who made it and still shows
  up on the replay, and **only the keys the form binds are sent** — sending the whole
  variable set back would rewrite untouched values under the operator's name. The binding
  is compiled into the model, so it is versioned with the task, costs nothing at runtime,
  and rides an instance's migration (ADR-0162). The Modeler offers it: the task kinds
  that can park get a **Repair form** section in the Implement panel, picking from the
  deployed forms — a repair form is authored where the task is, by the person who knows
  what it needs. The raw editor never goes away: a form covers the failure its author
  anticipated, and an incident nobody anticipated still has to be repairable — a task
  that binds no form is exactly as it was.

- **Migrating an instance from Operations**
  ([ADR-0162](docs/adr/0162-process-instance-migration.md)): the plan endpoint and the
  migrate endpoint now have a surface. A running instance's replay carries a
  **Migrate…** button that opens the target version, reads the plan for it, and shows
  what that migration would do *before* anything is written — which elements pair across
  a changed id, how many matched unchanged, and every reason it would be refused. A plan
  that cannot go through cannot be submitted: the refusal an operator would otherwise
  have discovered by trying is the thing the dry run exists to show them first. Changing
  the target re-reads the plan for that target, because a plan shown beside a different
  selection is the one way this dialog could mislead about live state. A reason is
  required and lands on the instance's timeline at the point it happened. From the
  Operations process list, **Migrate running instances…** drains one deployed version
  onto another in the bounded batches the server hands out, and reports both numbers:
  each instance is its own command, so the ones that could not move are listed by key
  with what is wrong — still running, unchanged, on the version they were already on —
  rather than being lost behind a success count.

- **A migrated instance's replay reads under the version each step ran on**
  ([ADR-0162](docs/adr/0162-process-instance-migration.md)): the instance timeline now
  resolves every element through the definition in force at that step's own log
  position, rather than through the version the instance happens to be on now. Element
  records name their element by its *compiled index*, and an index means whatever the
  graph it was compiled against says it means — so before this, a step recorded on v1
  was read back through v2 and reported the token as having been on whichever element
  now held that index: a step that never happened, on an element that did not exist when
  it supposedly ran. Nothing rewrites history to achieve it, because history is fact:
  each migration's operator-action record already carries the definition the instance
  left and the position it left it at, and a migration's target is by construction the
  next one's source, so the chain closes without any new field. A version deleted after
  the instance moved off it resolves to no graph and its steps are left unnamed —
  labelling them wrong is worse than leaving them blank, because only the second is
  visibly missing. The migration itself now appears in the replay's history as a
  boundary between the steps it separates: which version the instance left, which it
  arrived on, who moved it and why.

- **Process instance migration: the API and the MCP tools**
  ([ADR-0162](docs/adr/0162-process-instance-migration.md)): a running instance can be
  moved to another version of its process over HTTP.
  `POST /instances/{key}/migrate/plan` answers **what a migration would do and writes
  nothing** — the derived element mapping and every reason it would be refused — because
  the mapping comes from two graphs an operator cannot diff by eye and "which of my
  tokens would be stranded" is the question they actually have.
  `POST /instances/{key}/migrate` does it, and when the mapping does not hold it refuses
  with **409 carrying that same plan**, so a rejection is as informative as a dry run.
  `POST /processes/{key}/migrate-instances` is the batch form: each instance is its own
  command and its own event, so a refusal on one does not roll back the rest, and the
  response names every instance it left behind and why.
  Elements are paired by **BPMN element id** — the identity a modeler controls and the
  one stable across an ordinary edit — with per-element overrides for ids that moved;
  the ids are resolved to compiled indices before anything reaches the log. A reason is
  required and recorded as an operator action, as for a manual completion. Admin-only
  when auth is on. The same three operations are exposed as `atlas_migration_plan`,
  `atlas_migrate_instance` and `atlas_migrate_instances`.

- **Process instance migration: the engine half**
  ([ADR-0162](docs/adr/0162-process-instance-migration.md)): a running instance can now
  be rebound from one deployed version of its process to another without being
  cancelled. `IntentMigrating` carries the operator's request, `IntentMigrated` is the
  durable fact carrying both definition keys and the **frozen** element mapping, and an
  operator-action record beside it says who migrated, why, and which version the
  instance came from. The fold rewrites the instance's binding, its element instances
  and their live-token counters, its incidents and compensable records — preserving
  every element instance key, which is what lets variables, data objects, jobs and the
  whole scope tree ride through untouched. Nine validation rules refuse rather than
  guess: an unmapped token, a changed element type, a scope or multi-instance role
  change, a detached boundary event, a broken event-gateway race group, a changed
  message name (the subscription is keyed by it), an index the target does not have.
  Covered by a recovery test that replays a migrated instance into a fresh store and
  demands identical state. No API or UI yet — that is the next slice.

- **A connector says what it is for, and deleting one that models use is refused**
  ([ADR-0163](docs/adr/0163-deleting-a-referenced-connector.md)): ADR-0158 added a
  deploy-time check that every connector a model references actually resolves — and it
  ran in exactly one place. Deleting a connector three deployed processes referenced
  returned `204` and said nothing, and their tasks then parked with `no connector
  registered as "…"`: the same failure that record was written about, produced this time
  by the operator who deleted it. Each connector row now says which processes resolve
  through it and how many instances are running on them, `DELETE` answers **409 with
  that list** rather than a bare count (`?force=true` proceeds), and the Console asks a
  second time with the processes in hand. Deploying a model before its connectors exist
  still only warns — that is a plan; deleting one in use is a loss.

- **The connector behind an incident can be reconfigured from the incident**
  ([ADR-0160](docs/adr/0160-fix-the-connector-from-the-incident.md)): ADR-0158 let an
  operator correct an incident's *variables*; a mail task parked on `dial tcp:
  connection refused` has nothing wrong with its variables — what is wrong is the thing
  it talks to. Every incident row now names the connector its task resolves through and
  gains **⚙ Connector…**, which opens that connector's configuration prefilled — with
  the runtime's own reason it could not be built, and a **Test connection** button —
  and offers **Save & retry**, writing the change and handing the parked job one more
  attempt against it. A connector reference nobody has configured points at the Console
  instead, where one is created. Connector configuration is operator-managed runtime
  state, so the change takes effect at once, with no redeploy; what the *model* says
  stays immutable by design, and moving a running instance to a new version is instance
  migration, which Atlas does not have yet.

- **A stored connector has a real edit form** (ADR-0160): editing one used to be two
  `window.prompt` boxes offering `endpoint` and `credentialsRef` to every kind,
  including the kinds that use neither. Organization › Connectors now opens the same
  dialog the incident does — the fields this kind and provider actually use, the
  enabled switch, and the connection test — and both it and the create form derive
  those fields from one shared description, so they cannot drift apart.

- **A parked connector task now says what is actually wrong**
  ([ADR-0158](docs/adr/0158-a-connector-reference-that-explains-itself.md)): a mail task
  reported `no connector registered as "Patrick Blumer"` about a connector that was
  configured, enabled and visible in the list. The registry rebuild skips connectors it
  cannot build — correctly, so they never send wrongly — but threw the reason away, so
  *never configured*, *disabled*, *configured as another kind* and *configured but
  broken* all came out as the one sentence describing the only case that had not
  happened. The reason now travels: the incident says `connector "X" is configured but
  not usable: the connector is disabled` (or the provider's own error — a missing
  credential, a credential that will not parse, an endpoint naming no host), and the
  connector row in Organization › Connectors shows it before a token parks at all.
  Behind it, the five identical per-kind registries became one.

- **A deploy warns about connector references that will not resolve** (ADR-0158): a
  model naming a connector that does not exist — or exists as another kind, or cannot
  be built — used to deploy silently and fail at the first instance. The deploy response
  now carries warnings and the Modeler shows them beside the success. It stays a
  warning: deploying a model before its connectors are provisioned is legitimate.

- **An incident can be fixed, not just retried** (ADR-0158): every incident row — live
  view, replay, incidents table — gains **✎ Fix variables…**, which opens the instance's
  variables, writes them through the audited operator override (ADR-0098) and optionally
  resolves in the same step. A retry alone repeats whatever failed, and until now the
  correction was reachable only over the API. And the replay finally shows **who** set a
  variable by hand — the audit has recorded it and the timeline returned it since
  ADR-0098; nothing rendered it.

- **The Operations nav counts what is stuck**
  ([ADR-0151](docs/adr/0151-incidents-beyond-the-live-diagram.md) follow-up): the
  **Incidents** entry carries a red count of the tokens parked behind an unresolved
  incident, polled by the shell while Operations is open. Every other incident surface
  says "this is stuck" only once you are already looking at it; this one finds you. It
  reads a new `unresolvedIncidents` field on `GET /api/v1/stats`, counted from state
  rather than from a maintained counter — an incident also leaves state *with* the
  element instance it sits on (an instance cancel, an interrupting boundary), which no
  resolution event announces, so a maintained number would drift while a count cannot.
  Resolving from the incidents table updates the badge at once instead of waiting out
  the poll.

- **The Secrets panel says what a value has to be**
  ([ADR-0155](docs/adr/0155-secret-shape-hints.md)): a vault secret is a name and an
  opaque string, and because it is write-only nobody can look at a stored value
  afterwards and say what is wrong with it — so the form now says it beforehand. The
  moment a name matches a connector's token reference, the panel names the connector
  that resolves it and the shape it needs, offers an insertable skeleton for the JSON
  credential bundles, and refuses a value that cannot be one: a bare Google refresh
  token pasted where the bundle belongs is caught at the field, with the connector
  named, instead of surfacing later as `invalid character '/' after top-level value`.
  Each secret's row also says which connectors use it, so a rotation is no longer done
  blind. Rotation itself moved out of a one-line browser prompt — the wrong instrument
  for a multi-line bundle, and structurally unable to carry an explanation — into an
  inline panel with a real text field.

- **A mail task you can run before you own a mail server**
  ([ADR-0150](docs/adr/0150-preview-mail-provider-and-visible-incidents.md)): a mail
  connector can now use the **`preview`** provider, which asks for no submission host
  and no OAuth credential — it frames the message with the very same code the SMTP and
  Gmail providers send and delivers it to an in-server outbox, readable under
  **Operations › Outbox** (and over `GET /api/v1/mail/outbox`, or the `atlas_mail_outbox`
  MCP tool). What you read there is the RFC 5322 message that would have gone on the
  wire, so a preview run proves something about the message and not just about the
  model. The same sender/recipient checks a real provider applies are applied here, so
  it is a rehearsal rather than a bypass. The outbox is bounded and not durable:
  nothing in it was ever sent, and nothing in it survives a restart.
- **The live diagram says why a token is not moving** (ADR-0150): the runtime overlay
  now carries the unresolved incidents on a definition, so the Operations live view
  marks a parked element red, badges it with the failure's own message, and offers
  **Resolve & retry** next to the diagram. Previously a token parked behind an incident
  was drawn identically to one legitimately waiting for a worker — the engine had been
  raising the incident correctly (ADR-0061) since the first failure, but only the
  separate Incidents view ever showed it.

- **A connector can be checked before it is trusted with anything**
  ([ADR-0150](docs/adr/0150-preview-mail-provider-and-visible-incidents.md)): the
  connector form has a **Test connection** button, and every configured mail connector
  a **Test** action. The check runs against what is *typed* — nothing is saved to run it
  — and each provider answers the question its own configuration raises: SMTP opens the
  session a send opens (connect, STARTTLS, authenticate) and hangs up without a message;
  Gmail and Microsoft Graph acquire an access token, which is exactly the step a revoked
  or expired refresh token fails at; preview confirms it has an outbox. Give the check a
  recipient and it sends a real test message instead, the only thing that proves
  delivery. Both failures behind the outage this release fixes — a revoked Gmail refresh
  token and an endpoint that could not dial — are now answered in a second, at the form,
  by the person who typed them.

- **The step-by-step replay says why an instance stopped**
  ([ADR-0151](docs/adr/0151-incidents-beyond-the-live-diagram.md)): ADR-0150 put a parked
  token on the *live* diagram; the replay — the view whose whole purpose is
  reconstructing what an instance did — still drew the stuck element like any other. It
  now keeps that element outlined at **every** position of the playhead (an incident is a
  fact about now, not about the frame being replayed), flags its row in the Instance
  History, counts incidents beside the instance state, and resolves from the Details
  panel with the same one-click **Resolve & retry** the live view offers. It costs no new
  endpoint: the incidents ride the per-instance runtime overlay the replay already polls.

- **The Instances overview flags what is stuck** (ADR-0151): a per-process **Incidents**
  column that links to the *version* actually holding them — not the latest, which is the
  wrong diagram whenever the fault sits on an older one — and a flag on any
  variable-search hit that is parked. A stuck instance is counted as *running* like any
  other, so "3 running" read as healthy while one of the three had not moved in a week.
  Counts come from the incident list, not from the per-definition summary, which stays
  O(1) per definition (ADR-0083); a page-capped count says so instead of quietly
  undercounting.

- **`GET /api/v1/incidents` can be scoped** (ADR-0151): `?instance=` for one process
  instance, `?process=` for one deployed definition — also on the `atlas_list_incidents`
  MCP tool. A client that wants one instance's incidents no longer pulls every incident
  on the server and hopes its own survive the 5000-row page cap.

### Changed

- **Breaking (default behaviour): connectors run outside the engine now, and `atlas serve`
  starts the worker itself** ([ADR-0164](docs/adr/0164-no-in-process-service-tasks.md),
  [ADR-0168](docs/adr/0168-connector-work-on-a-worker.md)). The rule is that the engine's
  process runs the engine — the compiler, the processor, the log, the state store, the API —
  and does not run anybody's integrations, because the core loop cannot be guaranteed fast
  while something that can be slow is allowed to live in it. In-process execution rested on a
  promise the model cannot make and the engine cannot check: that a given endpoint is quick.
  Nothing rejects a slow one at deploy, and the endpoint that was fast when the model was
  authored is the same endpoint that is down at 3am. So `atlas serve` with no flags at all
  now offloads **csv, mail, script and webscrape** to a worker it supervises itself, rather
  than running them on the engine's goroutine. `--offload-connectors kind,…` adds more kinds,
  and **`--in-process-connectors` returns to the previous arrangement wholesale**. The
  boundary of the default set is a design, not a shortlist: a kind is defaulted only when
  Atlas can hand its configuration to the child at spawn, so no task is ever routed to a
  worker that lacks what the call needs — a test walks the default set against the managed
  kinds so nobody can quietly add one that isn't. Every other kind keeps its in-process
  handler and is **deprecated**: supported, documented as transitional, and not the shape a
  new model should take. New connector kinds are built worker-first, and the SQL and Entra
  kinds in this release have no in-process half at all.

- **In-process job handlers run off the run loop**
  ([ADR-0149](docs/adr/0149-bounded-connector-call-budget.md),
  [ADR-0157](docs/adr/0157-worker-processes-supervision-and-console.md)): a round of work is
  now three steps — *claim* collects activatable jobs on the loop, *work* runs the handlers
  off it, *submit* applies the outcomes back on the loop. Until now the whole drive happened
  inside the single writer, so every in-process connector's outbound call held it for the
  call's duration, and a burst amplified that: fifty parked jobs against a dead host cost
  fifty consecutive timeouts, serially, on the goroutine everything else needs. The caller
  still waits for quiescence — every request path and every test would otherwise change
  meaning — but the waiting happens on the goroutine that asked for the work. Drivers are
  serialized, so two callers never claim the same job twice, and a round runs a bounded
  number of handlers at once, because trading a serial stall for a thousand simultaneous
  outbound calls would only move the failure.

- **Dynamic job-type indices are issued from a fixed floor of 1000**
  ([ADR-0157](docs/adr/0157-worker-processes-supervision-and-console.md)): they used to be
  issued from one past the reserved range, so adding a built-in connector walked that range
  over indices already handed out — SOAP and AD did exactly that — and jobs parked under an
  index kept a number that had come to mean something else. The gap is dead space in an
  int32 and costs nothing. Stores written before the floor hold good assignments between the
  old reserved count and 1000, and those are *not* treated as built-in: "below the floor"
  must not come to mean "reserved", or a load would drop every one of them. The Workers view
  flags a stored type that now sits on a reserved index instead of silently mis-resolving it.

- **A connector task's input mappings now reach its worker — and shape what it sends.**
  Input mappings write an activity-local variable scope (ADR-0068), but every connector
  worker read the process-instance scope flat, so none of them could see its own task's
  mappings. A clio write task whose payload came from mappings appended events with an
  **empty body**; a REST url could not interpolate a mapped local; a SCIM task failed
  with "body variable is not set on the instance" for a resource a mapping had just
  built; LDAP and AD could not find a mapped entry variable. Every worker — clio, REST,
  mail, SCIM, SOAP, LDAP, AD, SharePoint, Remedy, web scrape, and user provisioning —
  now resolves up the task's scope chain, with its own locals shadowing what it
  inherits. **Where a payload *is* a variable scope** — the clio event body, the REST
  request body for methods that carry one, and the SCIM body when no payload variable
  is named — a task's input mappings, when it has any, are now exactly that payload:
  the model says what leaves it rather than shipping every scratch and internal
  variable into an external system, which is what makes a registered clio event schema
  or a strict SCIM endpoint satisfiable from a model at all. A task that maps nothing
  is unchanged and still sends everything it sees, so existing models keep working; a
  mapped clio/REST/SCIM task's payload does change, from "everything" (or, for clio,
  nothing) to "what you mapped". See
  [ADR-0174](docs/adr/0174-connector-payloads-are-the-input-mapping.md);
  the Event type, Method and Payload variable fields in the Modeler say which rule
  applies.

- **Breaking: the "Marketplace" area is now called "Repository"** — the same feature
  under a name that says what it is: the place a server's reusable building blocks live.
  The Modeler navigation entry and its route (`#/modeler/repository`) change, and so do
  the five HTTP routes, which move from `/api/v1/marketplace/…` to `/api/v1/repository/…`:
  `GET /packages`, `GET /packages/{id}`, `POST /packages/{id}/install`, `GET /installed`
  and `DELETE /installed/{id}`. Request and response bodies are untouched, and the OpenAPI
  tag is now `Repository`. On disk the design-time directory moves from `<data>/marketplace`
  to `<data>/repository`; an existing install is migrated automatically on the first start
  after the upgrade, so installed templates carry across without operator action. The
  backup archive (ADR-0107) therefore carries a `repository/` member instead of
  `marketplace/`; restoring a backup taken before this release maps that member onto
  the new name, so an older archive still comes back in full. The decisions behind the
  feature are unchanged;
  [ADR-0081](docs/adr/0081-community-marketplace-for-connectors-and-tasks.md) and
  [ADR-0167](docs/adr/0167-released-connectors-ship-in-the-marketplace.md) keep their
  original wording as dated records and carry a note about the new name.

- **Breaking (HTTP API): `DELETE /api/v1/connectors/{id}` can return 409**
  (ADR-0163) when deployed processes still reference the connector. The response body
  carries `usedBy` — the processes, their versions, the elements that resolve through
  it, and their running instance counts. Pass `?force=true` to delete anyway. A
  connector nothing references still deletes with `204`, unchanged.

- **`PATCH /api/v1/connectors/{id}` accepts `provider`, and validates like a create**
  (ADR-0160): switching a mail connector's provider was the one field the update could
  not change, and re-creating it under the same name is not a workaround — the name is
  the binding every deployed model references. The update now also re-runs the kind's
  full create validation against the patched record instead of only re-normalizing an
  SMTP endpoint, so switching to Gmail without a credential bundle is refused with the
  reason, and switching to the preview transport clears the endpoint and credential it
  no longer dials. Other kinds pass through unchanged.

- **Breaking (HTTP API): `elementId` in the incident list is now the BPMN diagram id**
  ([ADR-0151](docs/adr/0151-incidents-beyond-the-live-diagram.md)). The list previously
  returned the *compiled-graph* element index under that name, which no view can draw
  with and which contradicts every other endpoint, where `elementId` is the diagram id.
  The integer survives under its own name, `elementIndex`. The list also gained
  `processDefKey`, `processId` and `type` (`"job"` or `"timer"`), all resolved on read —
  the durable `IncidentValue` is unchanged, so `applyToState` and replay are untouched.

- **The incidents table's instance link works** (ADR-0151). It fed a *process instance*
  key to the live view's *definition* route, so the link never landed on the instance it
  named; it now opens that instance on its version's live diagram, with a replay link
  beside it. Its resolve prompt became a dialog with room to say that a timer incident
  re-arms and ignores the retry count.


### Fixed

- **Every outbound call a connector makes is bounded**
  ([ADR-0149](docs/adr/0149-bounded-connector-call-budget.md)): a connector built on
  `http.DefaultClient` waits forever by default, and because connector handlers ran on the
  run-loop goroutine, one hung host parked the entire engine — the API kept answering
  `/info` while every request that touched the loop hung. Each connector now carries a
  bounded budget (ten seconds by default) covering the whole call, and a test parses the
  `connector` and `dmn` trees to an AST and fails if an unbounded client is ever
  reintroduced, so the hazard is caught when it is written rather than when someone
  remembers to look. ADR-0164 and the change above take the handlers off the loop entirely;
  this remains the safety net for what still runs there.

- **A wide table no longer draws outside its card** (ADR-0163). A cell that cannot
  wrap makes a table's minimum width larger than the card holding it, and a table
  cannot be laid out narrower than that — so it was drawn past the card's right edge,
  border and header rule stopping short while the cells hung in the page beside it. The
  Operations **Incidents** table did exactly that once ADR-0160 added a third action
  button to every row. Two fixes: any card holding a table now scrolls it horizontally
  instead of letting it escape, and the incidents row keeps **Resolve…** as its one
  visible action with *Fix variables…* and *Configure connector…* behind the **⋯** menu
  the rest of the console already uses — so a fourth way out of an incident costs no
  width at all. The incident block beside a diagram keeps all of its buttons and wraps
  them instead, because that panel is resizable.


- **The SMTP client speaks over one transport that a check can share** (ADR-0150,
  ADR-0149): the send no longer goes through `net/smtp.SendMail` but through a session
  Atlas opens itself — the shared connector call budget as its ceiling, TLS from the
  first byte on the submissions port (465), STARTTLS wherever a server offers it,
  authentication after the upgrade, then the envelope. Each step names itself, so a
  rejection points at the address it was about ("recipient x@y refused") instead of at
  the send as a whole, and a connector check walks the same connection a send does. A
  send is also bounded by the context of the job that asked for it, which it never was
  before.
- **An SMTP endpoint written without a port is completed instead of failing at send
  time** (ADR-0150): `mail.example.com` now becomes `mail.example.com:587` (and
  `smtps://…` becomes `:465`), a pasted URL's path is dropped, a bare IPv6 literal is
  bracketed, and an endpoint that cannot dial — a mailbox address in the server field,
  a non-numeric port — is refused with a message naming what was typed. This runs on
  create, on `PATCH /api/v1/connectors/{id}` (which carries no kind or provider and so
  never reached the create validator at all), and when the client is built, so a
  connector already stored in the old shape starts working instead of parking one token
  per attempt behind `dial tcp: missing port in address`.

## [0.2.0] — 2026-08-19

Milestone 1's BPMN surface is essentially complete: this release lands the last
unimplemented **intermediate-event trigger** (conditional), the last major
**structural** element (ad-hoc subprocesses), plus link, escalation and terminate
events, lanes, and the loop markers on every activity kind.

Around that engine work the platform grew up. **Process applications** make a
project a versioned, deployable unit — git-backed as real source, publishable to
another server, with versions that can be deprecated to drain. The engine gained
**recovery checkpoints and WAL compaction**, so a restart no longer replays from
genesis and the log's disk is bounded. **Retention** became a property of the
process (`atlas:historyTtl`) and runs off a due-date index rather than a scan.
And Atlas became **observable**: Prometheus metrics, named log lines you can
alert on, and a readiness probe that means something.

For the people who read processes rather than run them, documentation now lives
on the elements themselves — shown to the assignee in the Tasks app and in the
Operations replay, and **exportable as a PDF** for anyone without an account.

### Added

- **A process declares how long its finished instances are kept**
  ([ADR-0144](docs/adr/0144-per-definition-history-ttl.md)): a model can now carry
  `atlas:historyTtl="P30D"` on its `<bpmn:process>`. The instance TTL (ADR-0085) bounds how long an
  instance may *run*; history retention (ADR-0115) is what *deletes* — but that was a single
  `--retention-max-age` for the whole server, which serves a recurring bulk data check and a
  years-retained approval equally badly. Retention is a property of the process, so it now travels
  **with the model**, versioned and deployed alongside it, and falls back to the server default when
  a process says nothing. The sweep's cadence and per-tick batch — what actually decides how fast a
  backlog drains — become operator settings too: `--retention-interval` and `--retention-batch`
  (`ATLAS_RETENTION_INTERVAL` / `ATLAS_RETENTION_BATCH`), previously internal constants.

- **Layout reserves corridors for column-skipping edges**
  ([ADR-0127](docs/adr/0127-layered-layout-pipeline-and-invariants.md) phase 2): a forward edge that
  skips columns had nowhere to run — the layers it passed over reserved no space — so it was routed
  through a channel beneath the whole diagram, dipping far below the lowest shape. The layout now
  **reserves the space in the layers the edge crosses** instead of detouring around them, so such an
  edge runs where it belongs.

- **The documentation export gains code, landscape pages and pruning**
  ([ADR-0143](docs/adr/0143-process-documentation-export.md)): three follow-ups to the export above.
  The document now includes **element code** — a script task's job source (PowerShell/Python/JS),
  FEEL expressions and mappings — so a reader sees what a step actually does, not only its prose. A
  **large diagram is laid out landscape** so it stays legible instead of being squeezed onto a
  portrait page. And the archive stops growing without bound: a **prune** keeps the newest N versions
  and deletes the rest (record and PDF), offered per version and as "keep newest N" in the export
  panel, both confirmed — a deliberate act rather than an automatic policy.

- **History purges run off a due-date index, so a short TTL actually takes effect**
  ([ADR-0146](docs/adr/0146-history-expiry-due-date-index.md)): the per-definition TTL above rode
  the existing key-order sweep, whose per-tick batch is a **scan** budget — finished instances that
  can never be eligible still consumed it. The first production install made that concrete: with
  ~529k finished instances of definitions carrying no TTL, a newly finished instance with
  `historyTtl="PT1M"` sat behind all of them in key order and would have waited about **nine hours**
  at the default 1000/minute. From the outside that is indistinguishable from a broken feature.
  Purges are now scheduled on a **due-date index**, so the sweep visits what is actually due rather
  than scanning history in key order — the same shape ADR-0085 established for the active set.

- **An HTML body for the mail connector** ([ADR-0079](docs/adr/0079-outbound-mail-connector.md), amended):
  `<atlas:mailConnector bodyHtml="…">` beside the existing plain-text `body`, a literal or a FEEL
  expression like every other message field, so markup can be composed from the instance's variables
  and a broken expression fails the **deploy** rather than the send. What goes out follows what was
  authored: text only → `text/plain` (framed exactly as before, so nothing changes for an existing
  process), HTML only → `text/html`, **both → `multipart/alternative`** with the plain text first and
  the markup last, so a client renders the richest part it can and a text-only reader still gets a
  readable mail. The multipart boundary is derived from the deterministic Message-ID and extended
  until it collides with neither body, keeping the framing deterministic and clock-free. The
  Microsoft Graph provider, which carries a single typed body, declares `contentType: "HTML"` when
  markup is present. In the Modeler the field is a real code field — HTML highlighting inline and the
  Developer View on <kbd>F2</kbd> (ADR-0145).


- **A Developer View for code-bearing fields** ([ADR-0145](docs/adr/0145-developer-view-for-code-fields.md)):
  <kbd>F2</kbd> in a field that holds code — a FEEL expression, a PowerShell/Python/JavaScript job
  script, a JSON value, a Markdown documentation text — lifts it into a full-screen editor with room
  for what a property column cannot hold. The code area is the **same `code-editor.js` surface as
  inline** (same highlighting, <kbd>Ctrl</kbd>+<kbd>Space</kbd> completion, live validation, gutter,
  variable drops), so nothing new has to be learned; the modal adds a side panel with the **variables
  in scope grouped by where they come from** (this element's input mappings, what it writes, process
  scope, linked-form fields, data objects — click or drag to insert at the caret), a browsable
  **function reference** with signatures, **help pages** with worked examples, ready-made **example
  snippets**, and the existing FEEL-evaluate / script-run round trips as a **Test panel**. Markdown
  and HTML gained syntax highlighting on the way. Apply writes the value back through the field's own
  `input`/`change` events, so the property panel stays the only writer and undo/redo is unchanged;
  <kbd>Esc</kbd> with unsaved changes asks before discarding. A field opts in with one
  `data-devlang` attribute, which is how every JSON editor in the app got it at once. The side panel
  folds away to a rail when a wide script wants the whole modal, and remembers that choice. The
  window is arrangeable and stays that way: **drag the divider** between the code and the reference,
  **drag the header** to move the modal, **resize it from its corner** — each remembered across
  openings, with floors that keep neither pane squeezable away and always leave a grabbable strip of
  the header on screen; a double-click on the header re-centres and forgets the arrangement. Each
  variable also shows **the value it actually holds in a real instance** of the process (newest
  deployed version, running instance first), and the Test panel's sample variables are prefilled from
  that same instance — so "what shape is this thing?" is answered by the running system instead of
  guessed from the name. **Which** instance is a picker in the pane, since the one that took the
  branch being written about is not always the newest. Lazy, memoized per process (switching
  instances costs no request) and refreshable; a process that has never run simply says so.

- **Logs with names you can alert on** (v0.2.0 programme E,
  [ADR-0142](docs/adr/0142-prometheus-metrics.md), slice 8): every operational line Atlas
  writes now carries a stable `event=` name beside the sentence, and the values that used
  to be interpolated into English arrive as typed fields. `event=checkpoint.published
  position=48213` is something an alert can match and a chart can read;
  `"checkpoint: published at log position 48213"` was not, except by regular expression
  against wording that changes whenever someone rewords a sentence.

  The sentence is kept, not replaced — "will retry next tick" is guidance a bare event
  name loses — and **text remains the default format**, because an operator watching
  `atlas serve` in a terminal is the audience Atlas has always had. New `--log-format=json`
  emits the same records as one JSON object per line for a log shipper. A typo in the
  value fails the boot rather than silently picking a format nobody asked for.

  Two rules are enforced by construction rather than by review: a call site **cannot
  invent an event name** (`logging.Event` carries an unexported field, so the catalogue in
  `logging/events.go` is the complete set, and a duplicate or malformed name panics at
  init), and **nothing logs around the catalogue** (a test parses every non-test file in
  the tree and fails on a direct `log.Printf` or `slog.Info` — it found all 37 call sites
  that existed when it was written). Event names are treated as an API: renaming one is a
  breaking change and will appear here under _Changed_. Secrets never become fields — the
  generated bootstrap admin password stays inside the message text, because a field is
  what a shipper extracts and keeps.

  Built on `log/slog`, so no dependency is added, and the engine still does not log at
  all — the single writer's hot path is untouched. OpenTelemetry traces are the other
  half of this slice and wait for their own change.

- **A readiness probe that means something** (v0.2.0 programme E,
  [ADR-0142](docs/adr/0142-prometheus-metrics.md), slice 7): `GET /readyz`, separate from
  `/healthz` and unauthenticated like it. The audit that opened this slice found the split
  was not merely missing but wrong — the bundled Helm chart pointed the startup, liveness
  **and** readiness probes at `/healthz`, which returns an unconditional `ok`, so a pod
  whose state store could not answer a read, or whose single writer was wedged, said `ok`
  and kept receiving traffic.

  `/readyz` returns `503` with a one-line reason while the server is shutting down, while
  startup recovery is incomplete, while a point read of the state store fails, and while
  the run-loop goroutine does not answer within two seconds. That last check is the one
  `/healthz` structurally cannot make: a blocked fsync on a hung volume leaves the process
  alive and answering HTTP while the writer is stuck. It is an empty closure with a
  deadline — a probe must fail rather than hang alongside what it is probing.

  **`/healthz` is unchanged and stays unconditional**, with a test guarding it from the
  other side: the only remedy a liveness probe has is a restart, so it must not fail for
  anything a restart would not fix. A liveness probe that waited for recovery would kill a
  pod mid-replay, and every restart makes that replay start over.

  Chart (0.2.0): readiness and startup now probe `/readyz`, liveness stays on `/healthz`,
  and the startup budget goes from 60s to 10m — the server does not open its port until
  recovery finishes, so the old budget restarted a slow replay into a replay that started
  over. Draining on shutdown is *not* solved here: the reason exists and fires, but the
  process stops accepting connections at SIGTERM anyway, so a pre-stop grace period is
  what would make a readiness-based drain observable.

- **The backlog, the job flow, and what a restart cost** (v0.2.0 programme E,
  [ADR-0142](docs/adr/0142-prometheus-metrics.md), slices 4–6): three additions that
  together answer "is anything stuck, is anything moving, and how long was this down?"

  **Open work, durably counted** (slice 4): `atlas_open_jobs`, `atlas_pending_timers` and
  `atlas_message_subscriptions`, engine-wide merge counters maintained inside
  `applyToState`, backfilled once at open for stores written before they existed, and
  recovery-tested — a rebuild from the log alone lands on the numbers the live run
  produced. The correctness condition is pairing: increment on the event that *creates*
  the entity, decrement on the one that removes it, and nothing on a re-put. Failing a
  job re-puts it with a decremented retry count; the job was already open and still is,
  so the gauge must not move — which would otherwise inflate it on exactly the processes
  an operator is watching. **Incidents are absent on purpose**: an incident is also
  removed by the unconditional delete that runs when any element terminates, with no
  event of its own, so counting them needs an explicit resolution event first — arguably
  a log-fidelity fix in its own right, since an incident can currently vanish with
  nothing in the log saying so.

  **Job flow** (slice 5): `atlas_jobs_created_total`, `_completed_total`, `_failed_total`
  and `_canceled_total`, counted from each batch's own records after it is durable. A
  gauge alone cannot say whether anything is moving; a counter alone cannot say how big
  the backlog is. Activations, lease expiries and timeouts are absent rather than zero —
  the lease-based worker protocol (ADR-0007) does not exist yet, and a permanent zero on
  a timeout counter would read as "nothing is timing out".

  **What a restart cost** (slice 6): `atlas_recovery_seconds` and
  `atlas_recovery_replayed_records` — the number ADR-0131's checkpoint cadence exists to
  shrink, so that "bounded recovery time" stops being a claim with no evidence in
  production. Records *read*, not events applied, since that is what a checkpoint
  changes; absent rather than zero before a recovery has happened.

- **How much is running, as a metric** (v0.2.0 programme E,
  [ADR-0142](docs/adr/0142-prometheus-metrics.md), slice 3): `/metrics` now reports
  `atlas_active_process_instances` and `atlas_live_element_tokens` — the first questions an
  operator asks, and until now answerable only by a request to `/api/v1/stats`, which
  *scans the runtime set* and so costs more the busier the engine is. New
  `Store.TotalActiveInstances` / `TotalLiveTokens` sum the per-definition counters
  ADR-0080 already maintains instead, reading one key per deployed definition.

  Measuring that turned up a qualification worth stating: those are Pebble **merge**
  counters, so a read also folds in whatever operands are not compacted yet — right after
  a burst of starts the sum costs O(recent writes), not O(definitions). After a flush it
  is flat, with 2,000 running instances read as fast as 100, and flushes happen on their
  own (the ADR-0131 checkpoint cadence forces one every few minutes). Even un-compacted it
  beats the scan. `BenchmarkTotalActiveInstances` measures all three states so the claim
  can be rechecked rather than believed.

  Jobs, timers, message subscriptions and incidents are deliberately **not** in this
  slice: they have no maintained counter at all, and exporting them as scans would break
  the rule ADR-0142 set rather than bend it. They need durable counters of their own,
  which is a change to state and so its own change.

- **Export a process as a document** ([ADR-0143](docs/adr/0143-process-documentation-export.md)):
  a BPMN model used to be readable only inside Atlas, which left out exactly the people who most
  need to read a process — auditors, a compliance officer, a new employee, the business owner
  signing it off — none of whom have a Modeler open, and often no account at all. The Modeler's
  toolbar now has a **Documentation** panel that collects the process's prose (the element
  documentation above), lays it out as a document with the diagram, and **publishes it as a PDF**
  anyone can be handed.

  The picture is rendered **in the browser and stored on the server**: the export reuses the very
  bpmn-js canvas the modeller is looking at, so the diagram in the document cannot drift from the
  one in the Modeler — the alternatives re-derive it, and would have cost either a Chromium binary
  in the container or a second BPMN renderer in Go to keep faithful. The PDF is written by a small
  **dependency-free** writer shipped with the web UI (`api/web/pdf.js`) rather than a vendored PDF
  library: pages, the standard Helvetica faces, wrapped paragraphs, element-prose tables and one
  embedded diagram raster are a small subset of PDF, and a focused writer is cheaper to own than a
  general one. The diagram goes in as a JPEG embedded untranscoded (`DCTDecode`).

  A **process documentation version** is an immutable record with a per-process, 1-based counter —
  the same layering process applications use above deployment versions — stored as a JSON sidecar
  with the PDF beside it, so "which documented state of this process is that?" has an answer, and a
  published document can be shared by link.

- **Ad-hoc subprocesses** ([ADR-0138](docs/adr/0138-adhoc-subprocesses.md)): the last major
  **structural** BPMN element — an `<adHocSubProcess>` whose contained activities are **not driven
  by sequence flow**. Entering it activates every **entry activity** (a contained node with no
  incoming flow) **at once**, each an independent token in its scope; contained activities may still
  be wired to each other and a token then flows on inside the scope. It finishes either when its
  scope **drains** or, if it carries a boolean **FEEL completion condition**, the first time that
  condition holds at the checkpoint run after each contained activity completes — cancelling the
  still-running activities (`cancelRemainingInstances`, the BPMN default) or, with `"false"`, letting
  them finish. This is BPMN's construct for **flexible / case-management** work. Built on the
  existing subprocess scope (ADR-0074) and the multi-instance completion-condition eval plus
  `terminateScope` cancel (ADR-0077), so it adds **no value type, event, or recovery path** —
  boundary events on it, interrupts, and recovery come for free (recovery-tested). Authored in the
  Modeler. `ordering="Sequential"` is **refused at deploy** rather than silently run as parallel.

- **Conditional events** ([ADR-0137](docs/adr/0137-conditional-events.md)): the one BPMN event
  family triggered by **process data** rather than a message, timer, signal, or throw — a
  conditional **intermediate catch**, **boundary event**, and **event subprocess**, each carrying a
  boolean **FEEL condition over the instance's variables**. The condition compiles at deploy (the
  gateway-condition machinery) and is **re-evaluated when a variable it reads changes**: every
  committed write funnels through the one `AppendVariableEvent` chokepoint, which marks the instance
  dirty and schedules a transient, command-path-only re-check that fires the armed conditionals now
  true. It self-evaluates at arm, opens **no subscription**, and reacts correctly to an external
  `SetVariables` with no activity completing. The re-check runs live only and the fire is an ordinary
  persisted event, so recovery replays it identically; a process with no conditional pays nothing on
  a variable write. Interrupting forms fire once; non-interrupting forms fire once per arm
  (repeatable false→true edge-triggering is a documented follow-up). The last unimplemented BPMN
  intermediate-event trigger.

- **Link events** ([ADR-0132](docs/adr/0132-link-events.md)): BPMN's **off-page connector** — a link
  intermediate **throw** ("go to X") and **catch** ("arrive at X"), paired by name within one flow
  scope, standing in for a sequence flow so a long or crossing diagram stays readable. Atlas resolves
  the pair **entirely at compile time**: the throw→catch link becomes a **synthetic sequence flow**
  and both events reuse the existing pass-through behavior, so a token flows throw ⇢ catch ⇢ onward
  exactly as through a none event — **no new runtime behavior, value type, event, or recovery path**.
  A deploy rejects an unmatched throw or a duplicate catch name. Authored in the Modeler.

- **Escalation events** ([ADR-0125](docs/adr/0125-escalation-events.md)): an **escalation** is a
  matter raised up the scope chain — an escalation **throw** or **end event** raises it and the
  nearest enclosing escalation **boundary** or **event subprocess** with a matching code catches it.
  Unlike an error it may be caught **non-interrupting** (the activity keeps running while the handler
  runs alongside), an **intermediate throw continues** on its outgoing flow, and an **uncaught
  escalation is benign** — no incident. Codes match by value, a code-less catch is a catch-all, and
  an escalation **propagates out of a call activity** to the caller. Authored in the Modeler, with a
  shared escalation manager.

- **Terminate end events** ([ADR-0116](docs/adr/0116-terminate-end-events.md)): a
  `<terminateEventDefinition/>` on an end event ends its **enclosing flow scope** at once — every
  other live token in that scope is terminated and its jobs cancelled, then the scope completes. At
  the process root that ends the instance; inside an embedded subprocess it ends that subprocess and
  the parent continues on its outgoing flow. It reuses the existing scope-teardown wholesale, adding
  one element type and a two-method behavior with **no new subscription, value type, or recovery
  path**. Previously refused at deploy.

- **BPMN lanes** ([ADR-0121](docs/adr/0121-bpmn-lanes.md), Layer A): a `<laneSet>`/`<lane>`
  partitions a process's flow nodes into **organizational lanes** — the role, team, or system
  responsible. Atlas adopts them as **metadata with no execution semantics** (spec-faithful): the
  compiler records each node's lane and exposes it over the API and in the Tasks app; the engine,
  `applyToState`, and token flow are untouched. Lane→group assignment defaults and instance-level
  access control are designed but deferred.

- **Mockup (engine-simulated) service tasks** ([ADR-0120](docs/adr/0120-mockup-service-task.md)):
  a service task can be marked as simulated by the engine itself — on activation it writes an
  optional FEEL result and waits a random duration, then completes, or raises an incident per a
  configured failure probability. No external worker or connector is needed, so a process with
  service tasks can be exercised end to end before any of them is implemented.

- **Bulk-terminate running instances** ([ADR-0090](docs/adr/0090-bulk-terminate-instances.md)):
  an operator can terminate many instances at once — an explicit selection, or every instance
  matching the current filter — instead of one key at a time.

- **Process applications** ([ADR-0128](docs/adr/0128-process-applications.md)): the project is
  elevated into a **deployable, versioned, portable unit** — its BPMN, DMN and form artifacts are
  validated and deployed **together** as one release, with the deployment recorded so an application
  has a history rather than a scatter of individual deploys.

- **Git-backed applications** ([ADR-0134](docs/adr/0134-git-backed-applications.md)): a repository
  becomes an application's **source** — a curated layout of real `.bpmn`/`.dmn`/form files plus a
  manifest, so changes diff legibly, rather than the opaque sidecar JSON a backup wants. Uses
  `go-git`, so the binary stays CGO-free and self-contained. **Atlas never merges**: a diverged
  branch is refused rather than three-way merged, because a plausible-looking BPMN merge can deploy
  and be silently wrong. A **portable application key** in the manifest survives a clone.

- **Remote deployment targets** ([ADR-0129](docs/adr/0129-remote-deployment-targets.md)): an
  application can be **published to another Atlas server** — promoting what is deployed from one
  environment to the next, rather than re-uploading artifacts by hand.

- **Deprecating a process version** ([ADR-0130](docs/adr/0130-deprecating-a-process-version.md)):
  a deployed version can be marked **deprecated** — a *drain* state distinct from pausing: running
  instances finish, but no new instance starts on it, so an application's Deployments view can retire
  old versions without terminating work in flight.

- **Server-side diagram auto-layout** ([ADR-0124](docs/adr/0124-server-side-diagram-auto-layout.md),
  [ADR-0127](docs/adr/0127-layered-layout-pipeline-and-invariants.md)): Atlas generates BPMN diagram
  interchange (DI) in Go on the server, so a model that carries no layout still renders, and the
  Modeler's **Auto-layout** button (**F8**) re-flows one from scratch. ADR-0127 restructured the
  generator into a **layered pipeline with executable layout invariants**, measured phase by phase,
  after ADR-0124's hand-written generator hit the edge cases it predicted.

- **A protected system project and bootstrap-deployed platform processes**
  ([ADR-0122](docs/adr/0122-protected-system-project-and-bootstrap-deployment.md)): Atlas models its
  own operations — user intake, access review, offboarding — as Atlas processes, bootstrap-deployed
  into a protected project that ordinary project management cannot delete or corrupt.

- **A sanctioned user-provisioning path for system processes**
  ([ADR-0123](docs/adr/0123-sanctioned-user-provisioning-for-system-processes.md)): the platform
  processes above stop short of the privileged act; this gives them one **narrow, audited** way to
  actually provision an account, instead of a general-purpose "create any user" capability.

- **Self-service registration link** ([ADR-0126](docs/adr/0126-self-service-registration-link.md)):
  the login screen can offer a registration link that starts the user-intake process, so a request
  for access is a modeled, approvable flow rather than an out-of-band email.
- **Every element takes a Documentation property, and a user task shows it to the person
  doing the work** ([ADR-0025](docs/adr/0025-full-properties-panel.md) amended, reversing
  its "the compiler ignores it" clause): the Modeler's Details panel now offers a
  **Documentation** field on whatever is selected — every task, gateway, event, sequence
  flow, data object and subprocess, plus the process itself (with nothing selected), each
  pool and the process it executes, and the collaboration as a whole. It reads and writes
  BPMN's own `<bpmn:documentation>` child, beside the element's name and id, so the
  description of *why* a step exists lives on the step rather than in a separate document.
  Emptying the field removes the element rather than leaving an empty one behind, and the
  edit joins undo/redo like any other.

  Documentation used to be invisible outside the model file, which meant only someone
  opening the Modeler could read it. It is now read where the process is *run*, not just
  where it is drawn:

  - The **Tasks app** shows a user task's documentation as the **work instruction**, above
    the form, where the assignee reads it before doing anything. For that the compiler
    **carries** the prose — interned per element, `CompiledProcess.ElementDocumentation` /
    `Documentation` — and `GET /api/v1/tasks` and `/api/v1/tasks/{key}` (and so
    `atlas_list_tasks` / `atlas_get_task`) return it as `documentation`.
  - The **Operations instance replay** shows it in the Details tab of the selected
    element, and the process's own below the instance summary. That surface already
    imports the diagram to draw it, so it reads the prose straight off the rendered model
    — no request, and it covers **every** element. Including one the instance never
    reached: selecting an un-taken branch used to fall back to the process panel, and now
    names the element, says *Not reached in this instance*, and shows what it would have
    done.

  Documenting a process still never changes what it runs, and that is now tested rather
  than assumed: a documented model compiles to *exactly* the graph its undocumented twin
  compiles to, and the prose survives deploy, the served XML (including a
  server-generated layout) and auto-layout. The processor reads none of it; nothing about
  the event log, the record format or recovery changes, since the compiled process is
  rebuilt from the stored XML.

- **A runaway loop parks instead of spinning** ([ADR-0133](docs/adr/0133-standard-loop-activities.md)
  amended, reversing its "no hidden ceiling" decision): a standard loop that states no
  `loopMaximum` is bounded only by a FEEL condition, and a condition that is simply
  always true — a typo, an unset variable — repeated forever, spinning the partition's
  single writer for an activity with no external wait. Such a loop now gets **1000**
  runs and then **parks with an incident** on its body: stopped, not finished, so
  nothing downstream runs on a result it never reached, and the incident says how many
  runs happened. Resolving it grants another 1000, counting on from where it stopped
  rather than restarting, so a legitimately long loop can be carried through by hand.
  The ceiling never limits a bound the model states: a loop with `loopMaximum` is
  governed by that number alone, however far past 1000. Alongside it the compiler warns
  (`loop.unbounded`, never an error) about a condition-only loop, naming the ceiling, so
  the bound becomes a decision rather than a backstop discovered at runtime. The count
  survives restarts like any other state; a cyclic sequence flow remains unprotected, a
  known asymmetry noted in the ADR.

- **Retries is a property of every job-backed task**
  ([ADR-0135](docs/adr/0135-retries-as-a-task-property.md)): the retry budget the incident
  model spends (ADR-0061) can now be authored on **every task that creates a job** — the
  job-worker service and send task, each connector task (its own `retries` attribute,
  overriding a `<zeebe:taskDefinition>` on the same element), the polyglot script task
  (`<atlas:jobScript retries>`) and the business rule task. Before this, a script task was
  hard-coded to three attempts and a connector task modeled in the Modeler could not express
  the property at all — the kind picker removes the task definition that used to hold it — and
  the Modeler offered no Retries field anywhere. It now shows one *Failure handling → Retries*
  field on every one of those kinds, carried across a switch of implementation kind, and an
  authored `<atlas:webscrapeConnector retries>` is honoured instead of silently ignored. The
  engine, the log and recovery are untouched: the compiled details already carried the field.

- **Every activity kind can loop** ([ADR-0133](docs/adr/0133-standard-loop-activities.md)
  amended, [ADR-0077](docs/adr/0077-multi-instance-activities.md) amended): business
  rule, manual and undefined tasks now honour both BPMN loop markers, closing the last
  place where a marker drawn on the diagram was silently dropped and the activity ran
  **once**. A looping business rule task re-evaluates its decision per round (one job at
  a time, its result feeding the loop condition); a looping manual or undefined task
  repeats its pass-through, so a routing draft loops before its tasks are implemented.
  The engine needed no change — the multi-instance body/iteration dispatch already runs
  whatever behavior the node has — and the same deploy-time refusals apply (an unbounded
  standard loop, both markers on one activity). In the compiler the loop fields moved
  onto the task shapes, so the node shape gateways share carries none: a gateway still
  cannot parse a loop marker. The Modeler offers the Loop section on these tasks, and
  its "Atlas does not run this marker here" note is now reserved for the genuinely
  non-activity cases.
- **Engine recovery checkpoints & WAL compaction — ADR + manifest primitives**
  (v0.2.0 programme D, [ADR-0131](docs/adr/0131-engine-recovery-checkpoints-and-wal-compaction.md)):
  recovery replays the WAL from genesis, so it is O(total log) and no segment is ever
  deletable. ADR-0131 decides the design — a periodic **Pebble checkpoint of the state
  store at a known applied log position** plus an engine-owned **manifest**, taken on
  the run loop at a batch boundary (single-writer-safe) after a durable flush,
  published atomically (temp dir → fsync → rename → parent fsync); startup picks the
  newest valid checkpoint and replays only the **suffix after its applied position**,
  falling back to an older checkpoint or genesis on any corruption; a segment becomes
  deletable only below both a durable checkpoint and every consumer watermark
  (ADR-0114 exporter, ADR-0115 retention); it is explicitly **not** ADR-0109's
  whole-instance backup. This first slice ships the **testable manifest format
  primitives**: a new `checkpoint` package with a deterministic, versioned,
  self-checksummed binary `Manifest` codec (magic + format version + fields + trailing
  CRC) and validation, with round-trip and corruption/truncation/version tests at 100%
  coverage. No checkpoint is created and **no WAL segment is deleted** — those are the
  later ADR-0131 slices.
- **Standard loop activities** (the ↻ marker, [ADR-0133](docs/adr/0133-standard-loop-activities.md)):
  `<standardLoopCharacteristics>` now runs — an activity repeats while a FEEL
  `loopCondition` holds, one run at a time, with `testBefore` choosing the while form
  (checked before the first run, so it may be skipped) or BPMN's default repeat-until
  (always at least one run), and an optional `loopMaximum` as a hard cap. Until now the
  marker was silently dropped at parse: the activity ran **once** while the diagram
  showed ↻. It runs on the existing multi-instance body/iteration machinery
  ([ADR-0077](docs/adr/0077-multi-instance-activities.md)) — same scope lifecycle,
  counter and recovery path, no new value type — on every activity kind multi-instance
  already supported. Each run's result stays visible to the next run and to the loop
  condition, and is promoted to the enclosing scope when the loop ends, so a looping
  activity leaves behind what the same activity would leave running once. A loop with
  neither a condition nor a maximum, an invalid maximum, or both loop markers on one
  activity is refused at deploy.
- **Loop authoring in the Modeler, in sync with the icon**: the Implement panel's
  Multi-instance section is now a **Loop** section whose single Mode select covers all
  four states (none, loop, multi-instance parallel, multi-instance sequential). It reads
  and writes the very `loopCharacteristics` element bpmn-js draws the marker from, so
  the property and the icon on the shape can no longer disagree — a marker set from the
  context pad reads back as its mode, and choosing a mode redraws the shape. An element
  carrying a loop marker Atlas does not execute now says so in the panel instead of
  leaving the icon to imply behaviour. The Design-view token simulation counts a
  standard loop like a sequential multi-instance, badged ↻ and bounded by the modelled
  `loopMaximum`, and the Operations call-activity list labels a looping call activity
  **loop** rather than **multi-instance**.
- **Engine throughput and latency metrics** (v0.2.0 programme E,
  [ADR-0142](docs/adr/0142-prometheus-metrics.md), slice 2): `/metrics` now reports what
  the partition writer is actually doing — `atlas_batches_total`,
  `atlas_commands_processed_total`, `atlas_events_written_total`, the events-per-batch
  histogram, and the two that matter when the engine feels slow:
  **`atlas_wal_sync_seconds`** (the one group-commit fsync per batch, the usual
  bottleneck) and **`atlas_state_commit_seconds`**, with
  `atlas_wal_sync_failures_total` / `atlas_state_commit_failures_total` beside them and
  `atlas_command_queue_depth` as the backpressure signal. None of these can be read off
  disk, so unlike slice 1's gauges they are pushed from the batch loop.

  That puts them on the hot path, under three rules each pinned by a test rather than a
  comment. They are reported **after** the state commit, so a counter never claims work
  a crash then loses; a batch whose fsync fails is reported as a failure and is *not*
  counted as committed. Reporting is one interface call per **batch** passing a struct by
  value, and two allocation tests hold it there — one on the call shape, one on the real
  Prometheus handles, which is what fails if a future metric is added with a per-batch
  label lookup. And a batch that wrote nothing observes no durations, because feeding a
  latency histogram zeros would report a p99 no real write ever achieved.

  The engine never imports Prometheus: it hands out plain numbers through a small
  `engine.Metrics` interface and the server maps them onto pre-resolved handles, so the
  exposition format and the bucket choices stay out of the single writer. The overhead is
  measured rather than asserted — `BenchmarkInstrumented` against
  `BenchmarkUninstrumented` in `benchmarks/` shows **identical `allocs/op`**, with
  `ns/op` inside the fsync's own run-to-run spread.
- **Prometheus metrics at `/metrics`** (v0.2.0 programme E,
  [ADR-0142](docs/adr/0142-prometheus-metrics.md), slice 1): Atlas had no metrics at all —
  everything observable was a JSON read of the present moment or a line in the log, so
  "was the engine slow at 03:00 last night?" had no answer. The server now serves a
  Prometheus exposition beside `/healthz`, on its **own registry** rather than the
  process-wide default, so what an operator scrapes is what Atlas registered and not
  whatever else in the binary happened to publish.

  This first slice exports the **durability** metrics: the applied log position, the
  checkpoints on disk and the position and age of the newest that **still verifies**, the
  last checkpoint pass's timestamp, failure and segments removed, the WAL's segments and
  bytes, and — only when an exporter is configured — its position and lag. Every one is
  read from durable state *when Prometheus scrapes*, which is the design rather than an
  implementation detail: there is no in-memory counter that could run ahead of the disk,
  so a metric cannot claim more than is durable, and the engine's hot path is untouched.
  A corrupt checkpoint is counted (it occupies disk) but never credited with a position
  or an age — it shortens no restart, and saying otherwise would be the one lie that
  matters.

  Two rules ADR-0142 fixes and a test enforces: labels must be bounded by the code and
  never by the data (no instance, job or correlation key, no process id or URL — a
  per-definition breakdown is an API query, which can paginate, not a time series), and
  every labeled handle is resolved once at construction so a future hot-path counter
  touches a `Counter` and not a `*Vec`. `prometheus/client_golang` was already in the
  module graph via Pebble, so this promotes an existing dependency rather than adding
  one. `/metrics` is on by default and unauthenticated like `/healthz` — it carries only
  aggregates — with `--metrics=false` to turn it off.
- **Checkpoint and compaction status, and a checkpoint-now control** (v0.2.0 programme D,
  [ADR-0131](docs/adr/0131-engine-recovery-checkpoints-and-wal-compaction.md), slice 8 —
  completing the ADR): everything checkpointing and compaction did was visible only in the
  server log, so an operator could not answer the two questions that actually come up.
  Now `GET /api/v1/checkpoints` reports what is configured, every published checkpoint
  with **whether it still verifies**, the last pass's outcome, and the WAL's current
  segment count and bytes — so "how much log would a restart replay?" and "why has my log
  stopped shrinking?" have answers without shell access. A checkpoint whose state files no
  longer match its manifest is flagged rather than listed as if healthy: that is exactly
  the one that licenses no deletion and would not carry a restore.

  `POST /api/v1/checkpoints` takes one on demand — and compacts, when compaction is on —
  for an operator about to restart who would rather replay seconds of log than hours. The
  pass runs on the checkpoint goroutine rather than beside it, so an on-demand pass and a
  scheduled one serialize by construction and are the same code; with checkpointing
  disabled the endpoint says so (409) instead of hanging or quietly doing nothing. Both
  endpoints are admin-gated like backup/restore, and neither is an MCP tool: this is
  storage housekeeping, not something an agent drives a scenario with.
- **The WAL stops growing forever — compaction runs in the server** (v0.2.0 programme D,
  [ADR-0131](docs/adr/0131-engine-recovery-checkpoints-and-wal-compaction.md), slice 7):
  `atlas serve --compact-wal` deletes the WAL segments a recovery checkpoint and every
  consumer watermark make redundant, on the same tick that takes the checkpoint. Recovery
  time was bounded in slice 5; the log's disk is bounded now.

  It is **off by default**, unlike checkpointing and for the same reason history retention
  (ADR-0115) is: this is the one step in the feature that destroys data, so an operator
  turns it on deliberately. Everything about the wiring is fail-closed — a consumer
  watermark that cannot be read, a whole-instance snapshot streaming the WAL, or an error
  anywhere skips the pass, because the cost of skipping is disk and the cost of proceeding
  is a segment recovery still needs. The cut itself is unchanged from slice 4: the newest
  **fully verified** checkpoint at or below the store, floored by the exporter's
  high-water mark (ADR-0114) and the retention safe position (ADR-0115).

  Taking a whole-instance backup now holds compaction off for its duration, and raises
  that hold *before* it picks the checkpoint it carries — so a pass that sees no backup is
  one whose deletion the backup's later choice already accounts for. `--compact-wal`
  without checkpointing warns and does nothing; the cut comes from a checkpoint.
- **Whole-instance backup survives a compacted log** (v0.2.0 programme D,
  [ADR-0131](docs/adr/0131-engine-recovery-checkpoints-and-wal-compaction.md), slice 6;
  [ADR-0109](docs/adr/0109-full-instance-snapshot.md) amended): the whole-instance
  snapshot carries `wal/` and not `state/` because `state == replay(WAL)` — which stops
  being true the moment compaction deletes a WAL prefix. An archive taken then would have
  restored an engine **silently missing** every instance whose events were below the cut.

  The snapshot now also carries the **newest fully verified checkpoint** (exactly one,
  picked before the WAL is read so the WAL copy is a superset of the suffix it needs), and
  applying a restore installs it as the state store before recovery replays the rest. A
  published checkpoint is itself a complete Pebble directory, so this is a copy rather
  than a conversion, and the checkpoint is kept so recovery can still seed the highest log
  position and key counter that a deleted prefix no longer supplies. An archive with no
  checkpoint restores exactly as before.

  Two rules keep it safe: a staged restore **always** carries a checkpoint entry — empty
  when the archive had none — so applying it replaces the local checkpoint root and
  nothing from the replaced log survives; and an archive whose checkpoints do not verify
  is **refused** rather than degraded to a plain replay, which would be right for a whole
  log and silently lossy for a compacted one. The cost is archive size: a snapshot now
  grows by roughly the state store, against the 1 GiB restore-upload cap. Still no WAL
  segment is deleted anywhere — that is the last ADR-0131 slice, and this was the last
  consumer standing in its way.
- **Bounded restart time — the server now takes recovery checkpoints** (v0.2.0
  programme D, [ADR-0131](docs/adr/0131-engine-recovery-checkpoints-and-wal-compaction.md),
  slice 5): the mechanism built by the previous slices is switched on. `atlas serve`
  snapshots the applied state every `--checkpoint-interval` (default **5m**, `0`
  disables), keeps `--checkpoint-keep` of them (default **3**), and at startup replays
  only the log past the newest usable one instead of from genesis — so restart time
  follows the cadence rather than the whole log's length. The server publishes into,
  and startup recovery reads from, `<data-dir>/checkpoints`, both resolved through one
  function so they cannot disagree.

  It is on by default because nothing here is deleted: the WAL remains the source of
  truth, and a missing, failed, or corrupt checkpoint only means a full replay. The
  snapshot itself is taken on the run loop, between batches, which is what makes the
  position it records exact (invariant I3); an idle server publishes nothing, and a
  failed pass is logged and retried on the next tick. WAL segments are still **never
  deleted** — feeding compaction the live export/retention watermarks, and the operator
  status and controls, are the remaining ADR-0131 slice.

  One consequence for **whole-instance restores** ([ADR-0109](docs/adr/0109-full-instance-snapshot.md)):
  applying one now also drops `<data-dir>/checkpoints`. A restore replaces the WAL, so
  checkpoints taken against the replaced log describe a log that no longer exists — and
  once the restored log advanced past their position they would look usable. Dropping
  them costs the full replay a restore performs anyway.
- **WAL compaction — old segments finally become deletable** (v0.2.0 programme D,
  [ADR-0131](docs/adr/0131-engine-recovery-checkpoints-and-wal-compaction.md), slice 4):
  the log no longer grows without bound. `wal.Log.Compact` deletes the segments a replay
  would skip, computed by the *same* rule `ReplayFrom` uses, so the deleted set and the
  skipped set cannot drift apart; the segment being written is structurally undeletable.
  `engine.Processor.CompactLog(root, consumerLimits)` gates the cut on the newest
  **fully verified** checkpoint — manifest *and* state files, a stricter check than
  recovery makes, because once the prefix is deleted those files are the only way to
  rebuild it — and on every consumer watermark the caller passes (the exported-log
  high-water mark, ADR-0114, and the retention safe position, ADR-0115). A checkpoint
  that is corrupt, for another partition, or ahead of the store licenses **no** deletion,
  and with no usable checkpoint nothing is deleted at all: the log stays the sole
  recovery source rather than being trimmed on an unverifiable promise. Compaction is an
  optimization like the checkpoint itself — skipping it costs disk, never correctness.
  Nothing wires this into the server yet; the cadence and the operator surface are the
  last ADR-0131 slice.
- **Engine recovery checkpoints — restore and suffix replay** (v0.2.0 programme D,
  [ADR-0131](docs/adr/0131-engine-recovery-checkpoints-and-wal-compaction.md), slice 3):
  recovery can now *use* a checkpoint, which is what turns O(total log) startup into
  O(suffix). `wal.Log.ReplayFrom` skips whole segment files — log positions increase
  monotonically, so a segment lies entirely below the cut whenever the next one starts
  just past it, and only each segment's first record is read to find out.
  `engine.Processor.RecoverFrom(root)` picks the newest checkpoint for this partition
  whose applied position is at or below the store's, replays only past it, and seeds the
  highest log position and key counter from the manifest — the values the skipped prefix
  would otherwise have supplied (without them a restored engine would restart its key
  counter and mint keys that collide with live instances). `Recover()` is now
  `RecoverFrom("")`, so every existing caller keeps replaying from genesis unchanged. A
  checkpoint that is corrupt, for another partition, or *ahead* of the store is refused
  in favour of an older one and ultimately genesis — always correct, only slower, so
  durability (I2) is untouched. Only the manifest is read, never the snapshot's state
  files, since a suffix replay does not touch them. Nothing wires this into the server
  yet and **no WAL segment is deleted**; compaction and the operator surface are the
  remaining ADR-0131 slices.
- **Engine recovery checkpoints — create and atomically publish** (v0.2.0 programme D,
  [ADR-0131](docs/adr/0131-engine-recovery-checkpoints-and-wal-compaction.md), slice 2):
  the engine can now *produce* a recovery checkpoint. `state.Store.Snapshot` flushes the
  memtable — ordinary commits are `pebble.NoSync`, so without the flush a snapshot could
  inherit that trailing property and silently omit applied state — then writes a Pebble
  checkpoint. `checkpoint.Publish` runs that snapshot into a `tmp-` directory, records a
  content checksum in the manifest, fsyncs the manifest and directory, and **renames** it
  into place before fsyncing the parent: the rename is the publication point, so a crash
  at any earlier step leaves only an ignorable temporary directory and the next attempt
  clears it. `checkpoint.List`/`Load`/`Verify` enumerate and validate published
  checkpoints (re-hashing the state files against the manifest), and `Prune` bounds disk
  by keeping the newest N, never fewer than one. `engine.Processor.Checkpoint` gathers the
  applied position, highest position, key counter, partition, and deployment refs **on the
  single-writer goroutine at a batch boundary** — which is what makes the recorded position
  exact — and is purely additive to durability: a failed checkpoint costs a slower
  recovery, never correctness. Nothing reads a checkpoint yet and **no WAL segment is
  deleted**; restore-and-suffix-replay and compaction are the next ADR-0131 slices.
- **Deterministic crash-and-recovery harness** (v0.2.0 programme C): a new
  engine-level test harness (`engine/crash_recovery_test.go`) that turns the
  durability contract into checkable evidence. It runs a workload to a durable point,
  edits the on-disk WAL to model a crash, recovers into a fresh, empty state store,
  and compares the rebuilt state family by family (instances, element instances,
  jobs, timers, incidents, variables, applied position) against a snapshot of the live
  state. Modelling the crash on the WAL's own boundaries (Append buffers a batch;
  one Sync per batch writes and fsyncs it, so a batch's frames land atomically at a
  known offset) lets it drop an un-fsynced batch at a clean boundary with no
  production fault hooks: it asserts that recovering the intact log equals the live
  state (invariant I4), that an un-fsynced / torn / CRC-corrupt trailing batch is
  absent while the acknowledged prefix stays fully consistent, and that restart is
  idempotent. Test-only, so the coverage floor is untouched. Deferred to later
  programme-C increments: in-process phase-boundary crash hooks for the exact
  after-append/after-commit cut points, child-process (SIGKILL) crashes, the
  no-side-effect-before-durability ordering assertion, and richer workloads (timers,
  messages, incidents).
- **Reproducible benchmark harness** (v0.2.0 programme B): a new
  [`benchmarks/`](benchmarks/) package measures the pure engine under the durable
  profile (a real segmented WAL with a group-commit `fsync` per batch and a real
  Pebble state store). It ships idiomatic Go benchmarks for three steady-state
  workloads — a minimal self-completing linear process, a service-task
  create/activate/complete lifecycle, and a mixed variables-plus-gateway routing
  process — reporting `ns/op` (→ instances/sec), `events/op` (from the applied log
  position), `walB/op` (on-disk WAL growth), and `-benchmem` allocations. A
  `summarize.sh` renders the machine-readable raw output as a Markdown table, a CI
  smoke step runs the harness at one iteration each (no performance threshold on PR
  CI), and [`benchmarks/README.md`](benchmarks/README.md) documents the commands,
  metrics, the environment metadata to record, and the standing caveat that results
  are specific to one machine and commit — not a product claim. All harness code
  lives in `_test.go` files, so it adds nothing to the coverage floor. An
  in-memory/no-fsync profile, latency percentiles, and recovery benchmarks are
  deferred to later programme-B slices.
- **End-to-end HTTP benchmark profile** (v0.2.0 programme B): the benchmark harness
  gained an API-layer profile that drives the same durable engine through
  `api.Server`'s HTTP handlers (in-process via `ServeHTTP`, so TCP/client cost is
  excluded). `BenchmarkHTTPLinearCreate` and `BenchmarkHTTPVariableGatewayCreate`
  mirror the shapes of their engine-level twins, so the difference is the API-layer
  overhead — the same `events/op`/`walB/op` with the extra `allocs/op`/`B/op` of
  request decode, routing, the run-loop handoff, and response encode. The existing
  `-bench=.` CI smoke step covers them; still deferred are a loopback-socket (real
  TCP) variant and service-task completion over HTTP.
- **In-memory benchmark profile** (v0.2.0 programme B): RAM-backed (tmpfs) twins of
  the three engine-level workloads (`BenchmarkInMemory…`). The state store already
  commits with `pebble.NoSync`, so the WAL `fsync` is the only durability cost;
  running it on tmpfs makes that `fsync` hit RAM, so comparing an in-memory
  benchmark to its durable twin splits the per-instance cost into engine CPU (what
  remains) and disk-`fsync` latency (the difference — on the CI machine, ~95% of the
  durable time). Same `events/op`/`walB/op`/`allocs/op` as the durable twin. It is a
  measurement profile, not a durability mode; the benchmarks skip when no tmpfs is
  available (`ATLAS_BENCH_TMPFS` overrides the mount). Still test-only, so the
  coverage floor is untouched, and the `-bench=.` CI smoke step covers them.
- **Recovery benchmark profile** (v0.2.0 programme B): the startup/recovery axis —
  how fast a fresh engine rebuilds state by replaying the WAL from genesis (there is
  no checkpoint yet). `BenchmarkRecoveryLinearCompleted` and
  `BenchmarkRecoveryServiceTaskParked` populate a WAL with `b.N` instances (batched
  into few fsyncs so setup stays cheap and is excluded from the timer), then measure
  the replay into a fresh, empty state store. `ns/op` is the per-instance recovery
  cost (so the derived instances/sec is the recovery rate, and recovery-events/sec =
  `events/op` × instances/sec); the two workloads recover completed history and
  parked instances-plus-jobs respectively. Test-only, so the coverage floor is
  untouched; the `-bench=.` CI smoke step covers them.
- **Published benchmark baseline** (v0.2.0 programme B): the first committed,
  reproducible Atlas performance baseline lives in [`benchmarks/results/`](benchmarks/results/)
  — a machine-labelled raw `go test -bench` capture (`baseline-<commit>.txt`, with an
  environment-metadata header) plus a `benchstat`-reduced Markdown summary
  (`baseline-<commit>.md`, median ± 95% CI over 10 repetitions across all four
  profiles: durable engine, HTTP, in-memory, recovery, and latency percentiles). It is
  labelled as illustrative and `fsync`-dominated, captured on a shared, ephemeral VM —
  not a product claim, hardware reference, or cross-engine comparison — and documents
  the exact command to reproduce or refresh it.
- **Latency-percentile benchmark profile** (v0.2.0 programme B): `ns/op` is a mean,
  which the skewed `fsync` latency understates, so `BenchmarkLatencyHTTPLinearCreate`
  and `BenchmarkLatencyEngineLinearSelfCompleting` sample each operation's wall-clock
  latency and report **P50/P95/P99 and max** (computed by nearest-rank on the sorted
  samples). They make the tail visible — on the CI machine the durable HTTP create's
  median is ~2 ms but its max is ~50 ms — and cover both the end-to-end HTTP path and
  the pure engine, so the API-layer tail can be attributed. Run with `-benchtime=Nx`
  for a fixed, meaningful sample count (P99 wants a few thousand); the percentiles
  appear in the raw `-bench` output and via `benchstat`. Test-only, coverage floor
  untouched; the `-bench=.` CI smoke step covers them.
- **Deactivate a deployed process** ([ADR-0119](docs/adr/0119-deactivate-deployed-process.md)):
  a deployed definition can be paused so it stays deployed and keeps its running
  instances, but no longer auto-starts new ones from its timer, message, or signal
  start events — for holding a timer-driven process during a maintenance window, for
  example. Reversible with no redeploy and no lost timers; a recurring timer resumes on
  reactivation. Exposed as `PUT /api/v1/processes/{key}/active` and an `active` flag on
  the process listing, and toggled from the Modeler's Deployed list (an "Inactive" badge
  and an Activate/Deactivate button). The flag persists on the deployment sidecar and is
  re-applied on restart; an explicit operator/API start is not gated.
- **Web-scraping connector** ([ADR-0118](docs/adr/0118-web-scraping-connector.md)):
  a `<serviceTask>` bearing an `<atlas:webscrapeConnector url selector attribute
  resultVariable>` extension fetches a model-authored page and extracts the elements
  matching a CSS selector, writing the values into a process variable as a JSON array.
  Like the REST connector, the URL and selector are authored in the model (each
  literal or a FEEL expression); extraction runs off the hot path in an in-process
  worker under the reserved `WebScrapeJobTypeIndex`. Authorable in the Modeler via the
  service-task connector catalog.

### Fixed

- **The Modeler no longer drops an example's extension elements**: opening
  `examples/order-fulfillment.bpmn` reported *script task "register_order" has no expression* in the
  Problems panel while the very same file deployed and ran — and both were right. `compiler.Parse`
  matches elements by **local name** and ignores the namespace, so it saw the extensions; the
  Modeler, which is namespace-correct, did not, and dropped them on load. The examples now namespace
  their extension elements properly, so what the Modeler shows and what the compiler reads agree.

- **An interrupted activity no longer leaves a ghost token in the replay**
  ([ADR-0136](docs/adr/0136-terminated-tokens-in-the-replay.md)): when an interrupting
  boundary event fired, the Operations replay kept drawing a live token on the activity the
  interrupt had torn down — it survived to the last frame, so a `completed` instance appeared
  to still hold a token. The replay's token fold defers an element's consumption until the
  activation it causes appears (so a token does not flicker between two log positions), but a
  *terminated* element takes no outgoing flow, so that deferral never resolved. Termination is
  now recorded as its own fact, distinct from completion, and such a token is retired at once.
  A finished instance ends with no token, as its live counters always said. Instances that
  finished before this change keep their ghost token on intermediate frames; their final frame
  is repaired on read.

- **Attached and armed elements no longer claim a phantom predecessor**
  ([ADR-0136](docs/adr/0136-terminated-tokens-in-the-replay.md)): an armed boundary event, an
  armed event-subprocess trigger and a compensation handler are not entered over a sequence
  flow, but recorded flow index `0` — a real flow — instead of "none". The replay animated
  such a token along an edge that does not exist, and the frame fold could retire an unrelated
  live token by mistaking the arming for that edge's successor.

### Changed

- **Handbook: umlauts in the loop recipe's labels** ([ADR-0133](docs/adr/0133-standard-loop-activities.md)):
  the ↻ recipe shipped ASCII-fied German — "Zaehler starten", "Pruefen", and a process
  named "Pruefen bis in Ordnung" — while every other recipe in the German handbook uses
  umlauts. They now read **"Zähler starten"**, **"Prüfen"** and **"Prüfen bis es passt"**
  (matching the recipe's own heading), both on the rendered card and in the process name
  the deploy reports. Labels only: the recipe's XML structure and its loop
  characteristics are untouched, and it still deploys and runs from the card.

- **A retry budget below 1 is refused at deploy**
  ([ADR-0135](docs/adr/0135-retries-as-a-task-property.md)): `retries="0"` (or a negative
  count) used to deploy and then hang — a job is on the activatable index only while it has
  retries left, so it was never handed to a worker, never failed, and never raised the incident
  an operator could resolve. It is now a compile error naming the element, alongside the
  existing "invalid retries" error, which every task kind now words identically. Use
  `retries="1"` for a single attempt with no retry.

- **Deterministic history-retention tests** (v0.2.0 reliability foundation): the
  retention sweep (ADR-0115) gained two test seams — an injectable clock for its
  eligibility cutoff and an explicit sweep trigger in place of the real ticker. The
  black-box retention tests, which previously raced a wall-clock cadence and had to
  widen a max age to 500ms so a sweep tick would not fire during setup (PR #313), are
  replaced by deterministic ones that share a single fake clock with the engine (so a
  finished instance's `CompletedAt` and the sweep's "now" are exact) and drive each
  sweep through a channel handshake (no `time.Sleep`, no polling). They now assert the
  exact age boundary and an exact one-per-tick drain, honoring the repository rule that
  tests must not depend on wall-clock time or goroutine scheduling (invariant I4,
  AGENTS.md). Production behavior is unchanged — a real ticker and the system clock
  still drive the sweep in the running server.
- **Deterministic OpenSearch-exporter test** (v0.2.0 reliability foundation): the
  exporter loop (ADR-0114) gained a test seam — an injectable tick trigger in place of
  its real ticker, with a completion signal. The exporter test previously fired a 15ms
  export interval and polled `stub.count()` under a 3s deadline with a `time.Sleep`,
  racing the background ticker; it is replaced by one that triggers a single export pass
  and awaits it via a channel handshake, then asserts the instance's events were
  mirrored to the configured index — no wall-clock cadence, no polling, no `time.Sleep`.
  This follows the same seam the history-retention sweep uses and honors the repository
  rule that tests must not depend on wall-clock time or goroutine scheduling (AGENTS.md).
  Production behavior is unchanged — a real ticker still drives the loop in the running
  server.

## [0.1.0] — 2026-08-11

The first tagged release: a **developer preview**. Atlas already runs a broad
slice of BPMN 2.x durably on a single node, but the operability surface a
production deployment needs (a streaming job-worker protocol, metrics/traces,
log compaction, multi-node scale-out) is still ahead on the [roadmap](ROADMAP.md).
Not for production use.

### Added

**Engine core**

- Durable, event-sourced, single-writer processor: every state transition is an
  append-only WAL record made durable with one group-commit `fsync`, then
  materialized into an embedded Pebble state store. One `applyToState` runs
  identically live and on recovery, so replay and live state can never diverge.
- Compile-don't-interpret pipeline: BPMN XML is compiled once at deploy time
  into an immutable, integer-indexed `CompiledProcess` with interned strings and
  pre-compiled FEEL expressions — no XML, string lookups, or map access on the
  hot path.

**BPMN coverage**

- Control flow: none/start and end events, sequence flows, service tasks,
  script tasks (in-engine FEEL and polyglot workers), and exclusive, parallel,
  and inclusive gateways (split and join), all recovery-tested.
- Process variables with input binding, activity-local variable scopes, and
  Camunda-faithful `zeebe:ioMapping` input/output mappings resolved up the scope
  chain.
- First-class, event-sourced **data objects**: typed values with a data-state
  history, data input/output associations, and field-level writes.
- Events and timers: intermediate/boundary/start **timer** events (duration,
  date, cycle, cron, and FEEL expressions), **message** events with
  subscriptions and correlation, **signal** broadcast events, and **error**
  events with structural propagation to the nearest handler.
- **Receive tasks**, and **boundary events** (timer, message, signal;
  interrupting and non-interrupting).
- Structure and reuse: **embedded** and **event subprocesses**, **call
  activities**, **multi-instance** activities (sequential and parallel), and
  **compensation** with compensation handlers.
- **Business rule tasks (DMN)** via the embedded [temis](https://github.com/pblumer/temis)
  engine or a remote temis connector, with I/O mappings, decision binding
  (`latest`/`deployment`), and durable, replayable decision-evaluation records.
- **Collaborations & pools** with message-flow correlation between participants.
- **Incident model**: a job that exhausts its retries parks and raises a durable
  incident an operator can resolve, resume, or complete by hand.

**Connectors**

- A service-task **connector catalog** — a plain job worker, a clio event-store
  writer, a model-authored **REST** connector, and email/SharePoint/Remedy
  connectors — each served by one worker.

**Single-binary server, web UI & tooling**

- `cmd/atlas serve`: one self-contained binary embedding the engine behind an
  HTTP API and a `go:embed`-ed web UI (Console, Modeler, Tasks, Operations,
  Panorama) — deploy XML, run instances, work user tasks.
- Embedded **bpmn-js Modeler** with a hand-written properties/"Implement" panel,
  an embedded **dmn-js** decision-table editor, projects & artifacts, diagram
  drafts, and durable deployments that survive a restart.
- **Operations**: live runtime overlay on the diagram (active elements, token
  counts), instance management, and multi-token replay with causal token
  lineage.
- **Forms** and the **Tasks** app for human tasks.
- **User management & auth boundary** (opt-in `--auth`): durable accounts,
  bcrypt passwords, RBAC-ready roles, identity-bound user-task assignment.
- **Encrypted secret vault** (AES-256-GCM, on by default with a generated key)
  for connector credentials, resolved through a `credentialsRef` indirection —
  secrets never touch the WAL, state, or variables.
- **MCP server** (ADR-0016) over stdio (`atlas mcp`) and Streamable HTTP
  (`/mcp`), so an AI agent can deploy a model, start an instance, and read live
  runtime state.
- **Backup & restore** of the design-time state and whole-instance snapshots
  over the HTTP API.
- `atlas version` reports the product version plus the binary's embedded VCS
  build metadata (commit, build time, dirty flag, Go toolchain).

**Deployment**

- A container **`Dockerfile`** and a **Helm chart** (`deploy/helm/atlas`) for
  running the server on Kubernetes, plus the tag-driven release workflow that
  publishes cross-compiled binaries — linux (amd64, arm64, and 32-bit arm/v6 for
  Raspberry Pi), macOS (amd64, arm64), and windows (amd64) — with a
  `SHA256SUMS` file.

### Notes

- **License:** Atlas is released under the **GNU Affero General Public License
  v3.0 only** (`AGPL-3.0-only`).
- Single-node only. Cross-partition messaging, replication, and multi-node
  deployment are on the roadmap (Milestone 5).
- Recovery replays the log from genesis; log compaction / snapshotting is not
  yet implemented (Milestone 4).

[Unreleased]: https://github.com/pblumer/atlas/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/pblumer/atlas/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/pblumer/atlas/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/pblumer/atlas/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/pblumer/atlas/releases/tag/v0.1.0
