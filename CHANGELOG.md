# Changelog

All notable changes to Atlas are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project aims to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

While Atlas is pre-1.0 (`0.y.z`), the public API — the HTTP surface, the MCP
tools, the on-disk WAL/state format, and the Go package layout — is **unstable
and may change in any release**. Breaking changes are called out under
_Changed_ / _Removed_ for each version.

## [Unreleased]

### Fixed

- **Saving a product said "apiBytes is not defined" and quietly left the product
  offered by nothing.** The catalogue screen hands its event handlers a bag of what
  the shell owns — the API caller, the byte uploader, the toast. The product form's
  save reached for the byte uploader to put the picture up, and the bag it was
  called with did not carry it. The name resolved to nothing, and not at load, where
  review would have caught it, but on the press that reached the line.

  What the message named was the picture. What it cost was the offering: the record
  was already written, and the step after the picture is the one that tells the
  catalogue to offer a new product — so the save ended with the product stored, the
  catalogue unchanged, and a product that is offered by nobody, which is invisible
  on every screen its maintainer has. The picture step now goes last, after the
  offering, because it is the step whose failure can be afforded: losing a picture
  costs one upload and is visibly missing.

- **The portal's link into an order's process answered where nobody was looking.**
  Pressing "Prozess ansehen" searches for the fulfilment orchestration and opens it.
  Both ways that search can come back without one — nothing started or nothing left
  — wrote their answer above the table, so an order further down the page produced a
  message off-screen and a button that read as broken. The answer is now under the
  button that asked for it.

  And one of those two was not a message at all. The instance search falls back to
  the exported event log when this server's own index has nothing, marking what it
  answers with: those rows describe an instance the server no longer holds. The link
  followed one like any other, into a replay view that could only say "Could not
  load this instance's replay." It is now said here, in words that name the cause
  this server is actually certain of.

- **A folded decision service stays folded.** `EnsureDiagram` lays a model's whole
  graph out afresh whenever its diagram covers only some of the nodes, on the reasoning
  that a partial diagram is the residue of a tool that drew what it could. A collapsed
  decision service looks exactly like that from the outside and is the opposite: DMN
  draws one by leaving its definition out of the view, so a diagram missing exactly its
  members is a diagram somebody arranged that way. Re-laying it unfolded the fold on
  every read, which meant a fold could never survive being saved. The exception is
  narrow — a collapsed service's own members and nothing else; the service still needs
  its own shape, because one with no box at all is the residue the rule exists for.

- **A decision service is offered where the author looks for it.** The Modeler's
  decision picker is built from two lists: what an application's DMN references offer,
  and — as a fallback — what the engine has deployed. Describing a reference returned
  only its decisions, never the decision services, so a service could reach the picker
  only by the second route: with no model handle, and therefore in no application. The
  one thing a business rule task is meant to call sat under "other decisions", below
  every decision it is made of.

  A task addresses either with the same one string, so a catalog that carries one has
  to carry the other. It now does, and the list is cut up the way an author reads it:
  one group per decision file, this application's files first, and inside a file the
  published interfaces before the decisions. A decision a service is made of says which
  one — calling it works and answers correctly, which is exactly why it is worth
  saying, because it reaches past the interface the service exists to be. An input
  decision carries no such marker: that is the boundary the caller supplies, and it
  sits outside the service rather than within it.

- **A product's description was stored, frozen into the release and never shown.** The
  portal asked for it in the language the *page* is read in — German or English, taken
  from the browser — and treated a missing key as no description at all. But publishing
  guarantees a description in every language the **catalogue** declares, and those are
  different lists. A catalogue offered in German and French is complete by that rule and
  had nothing whatever to say to a reader whose browser is English: two descriptions
  written, neither on screen, and no rule anywhere broken.

  The reader's own language is still asked for first — that is what makes the choice a
  choice once more than one exists — and the other languages are reached after it. A
  paragraph somebody has to translate is worse than one they read and better than the
  blank they were getting. A key that is present and blank is not taken as an answer,
  because that is the shape a half-filled form leaves behind and it would end the search
  before the language that does say something.

  The picture needed no change and is shown beside it, where the catalogue carries one.

- **The info button on the basket did nothing.** Every row there drew it, it responded,
  and nothing opened: the button sets which product to explain and the *view* has to
  draw the panel, and the basket made the first statement without the second. The
  catalogue page and "my services" had both. So on the one screen where somebody decides
  whether to actually order the thing, the price, the approval rule, the description and
  the picture were unreachable — a row was a name and two buttons, and the name was all
  they had.

  Guarded per view rather than per file from now on: `infoPanel` appears three times, so
  a search across the page would have found it however many views had forgotten it.

- **A decision service that answers with nothing is refused rather than deployed.**
  DMN gives a decision service one or more output decisions: they are what it returns,
  and the whole reason to address a service instead of the decision inside it. Atlas
  accepted one with none. It compiled, it was listed, the decision picker offered it, a
  business rule task called it — and the task completed with the variable it was to
  fill still unset. No error, in the engine or in the log; the process simply carried
  on past a decision that was never made.

  That is not hypothetical. A service's membership lives in its references and its
  picture in the diagram, nothing in the format holds the two in step, and an editor
  that rewrote the picture wrote the interface away with it. Every check between there
  and the disk said the model was fine.

  The check now runs where a model arrives and where a deploy asks whether one is
  sound, and it names the service rather than the file, so an author knows which box
  to fix. A model already stored with the fault reads as invalid in the model list and
  cannot be deployed until it is repaired. Work in progress is unaffected: an
  unfinished service belongs in a draft, which is saved without this gate.

### Removed

- **The two links on a portal order's position rows.** A position row offered "Wo
  steht das?" — the step that position is sitting on — and a link into the
  position's own process instance. Both are gone at the request of the people the
  page is for: the status beside the position's name answers the same question out
  of the order's own record, one column over and without a press. Withdrawing a
  position and correcting its details stay; they act on the position rather than
  look at a process.

  `GET /api/v1/portal/orders/{id}/lines/{position}/progress` is unchanged. It is API
  surface with callers that are not this page, and a screen that stopped drawing a
  button for a route is not a reason to withdraw the route.

### Added

- **A decision service can be folded away on the canvas.** A DRD carrying several
  decision services is unreadable with every decision inside every one of them on
  screen, and DMN has an answer for it: a collapsed service, drawn as a box with its
  name and nothing of its definition (1.5 §6.2.4). The Modeler now offers it on the
  service's context pad, both ways, as one undoable step.

  A fold takes away depiction, not model. The decisions stay in the graph and stay
  editable — the view list is built from the graph rather than from the diagram — and
  what is saved is a diagram without their shapes, with the service marked as
  collapsed. Neither of the obvious implementations would have done that: deleting the
  shapes takes the decisions out of the model, and re-creating a requirement on unfold
  tears it out of the decision that owns it.

  One limit, stated rather than hidden: where the decisions were does not survive a
  save. DMN offers nowhere to keep the position of something a diagram does not show,
  so unfolding restores the arrangement exactly within a session and lays it out afresh
  after a reload.

- **The run graph can be drawn as a cloud, and the cloud turns out not to need the graph.** [ADR-0404](docs/adr/0404-the-whole-graph-can-be-walked.md) §5 says the cloud is an aggregation rather than a clustering. Building it showed what that buys: a group-by is not a graph operation, so `rungraph.BuildCloud` costs one scan of the store and a map sized by the number of *cells* — no ordinal map, no CSR, no union-find, and none of the 2,448 MB the structure costs at 110 million nodes. An installation large enough that §9 refuses the whole-graph *walk* can still be shown the whole-graph *cloud*. What it loses is the walk and the drill-down from a cell to its members, not the density.

  Every cloud carries the axis it groups by and the log position it is true as of, as fields
  rather than as documentation, because §5's own rendering rule forbids a picture that cannot
  say what it is dense in and as of when — and a renderer cannot add either honestly if the
  number does not carry them.

  **The measurement also corrected the record's premise, which is now the third time a
  W-item has done that.** §5 calls its five dimensions ones the nodes "already carry":
  definition, element, worker, incident state, time bucket. The node carries **two** of them
  — the definition and the element — plus the BPMN element type as a refinement of the
  second. Worker lives on a job, incident state on an incident, and the value has no
  timestamp at all, so each of those three is a join plus four bytes per node to carry the
  result: 420 MB at 110 million nodes, per dimension, and the same budget unit whose doubling
  the membership decision had just refused. The three free axes are what ships; the other
  three are a cost for the record to weigh rather than a default.

  Two smaller things the implementation settled, each a wrong answer avoided rather than a
  preference. An element cell is scoped to its **definition**, because `ElementId` is an
  index into the compiled graph and not a global identifier, so element 1 of two processes is
  two elements and grouping by the number alone would report a density over a cell that does
  not exist. And the cell order is imposed rather than inherited from a Go map's deliberately
  random iteration, because §5 chose components over a Louvain partition partly for being
  stable across rebuilds, and a cloud whose cells reshuffle breaks the same promise.

  No HTTP route and no screen: the surface this feeds needs a renderer decision that has not
  been taken, and the axis question is settled here, in the data, rather than implicitly
  inside a renderer where it would be most expensive to correct.

- **The run graph says when it is true, and how far the log has drifted from it since.** [ADR-0404](docs/adr/0404-the-whole-graph-can-be-walked.md) §4 asks for a projection seeded from the state store and kept current from the tailer, "starting at the snapshot's position". Nothing could: a built graph carried no statement about *when* it was true. It does now — a source must report its `LastAppliedPosition`, the ordinal map records it, `Graph.Position()` reads it, and a source that cannot state one fails the build rather than publishing a projection that claims "as of 0" and is indistinguishable from one genuinely at genesis.

  On top of that position, `rungraph.Follower` reports **drift**: how many element instances
  have arrived and departed since the seed, read from the durable log and bounded by the
  caller's durability watermark. `Drift.RebuildDue` turns that into the decision the projection
  actually needs.

  **It measures rather than mutates, and the structure decided that, not preference.** A
  completing element instance is *deleted* from state, so the node set shrinks as fast as it
  grows in any steady-state installation; the CSR is packed and undirected, so one new edge has
  to be inserted into the middle of both endpoints' adjacency lists inside a 2,448 MB array;
  and union-find cannot un-merge, so a removed edge costs the whole 7.8-second component pass
  anyway. An increment that can only add would diverge from reality in the common case. Drift
  plus a rebuild keeps the projection exactly as of its position, which is the one property
  every consumer needs.

  Three consequences are worth naming. Drift counts **node changes and not records**, because a
  busy installation writes far more variables and jobs than element instances and a record
  count would call for rebuilds nothing needed. The follower **skips the seed's records by
  position instead of seeking past them** — a `wal.Cursor` cannot be constructed — which also
  makes it correct under the re-delivery a restart guarantees, since a cursor resumes from
  genesis by design. And a record more than one position past the seed is reported as a **gap**
  that means rebuild: positions are one dense sequence, so the records in between are gone and
  nothing can supply them, which is what §4's "cannot be reconciled" looks like from the
  inside. The arithmetic is checked against something outside the log — arrivals minus
  departures must equal the change in the store's own count of live element instances — and the
  density claim is verified on engine-written state rather than assumed.

- **The run graph answers "what is this connected to" as a lookup, and the measurement renamed the question.** [ADR-0404](docs/adr/0404-the-whole-graph-can-be-walked.md) §5's projection gains the half it was missing: one union-find pass over the CSR, and a `rungraph.Membership` over the labels it produces. `SameComponent` — the query an impact analysis asks a million times — is a binary search and one array read, touching no part of the graph. Enumeration (`Members`, `Size`, `Count`) is one pass over the label array and says so in its own documentation, because the two costs are different and a caller has to know which it is paying.

  It is a wrapper rather than an index, and that is a budget decision. §2 sizes *one*
  union-find array — 420 MB at 110 million nodes — and the pass already flattens it so a
  lookup is a single read. Grouping members by component for O(1) enumeration would double
  that, to make the rare query faster on the rare graph: §9 makes the narrow scope the entry
  and the whole graph the exception, so enumerating happens on thousands of nodes, where a
  scan is free.

  **The acceptance measurement disproved the record's own sentence, which is the second time
  a W-item has done that.** §5 said connected components "for this topology *is* the
  instance-family decomposition". Measured on engine-written state: a nested shape gives one
  component per instance; a flat one with two parallel top-level branches gives **two**, and
  sixty-four instances become a hundred and twenty-eight components with zero edges between
  the branches. The cause is exact — a live element instance at the process instance's own
  scope points at the *process instance*, which is not an element instance and therefore not
  a node, so nothing joins two top-level siblings.

  So a component is the **reference-connected** group, which is what impact analysis wants,
  and it is not the instance — a grouping that needs no union-find at all, since
  `ProcessInstanceKey` is a field on the value the store already hands over. The API is named
  `SameComponent` and not `SameFamily` for exactly that reason: the wrong name would have had
  every caller believe it answered the cheaper question. §5 now carries the table, the cause
  and the distinction, and W1's own comment claiming the same thing is corrected.

### Added

- **Personal data can be erased: a declared variable is enciphered under its data subject's own key, and destroying that key makes every copy unreadable.**
  A process names the variables that hold personal data and the one variable holding the id
  of the person they are about — `atlas:personal="vorname,nachname"` and
  `atlas:dataSubject="personalnummer"`. Each data subject gets a random key of their own,
  stored as an ordinary secret in the vault and therefore sealed under the master key: no
  new key material on disk and no second store. Erasing that person deletes the one key.

  What makes this an answer to a deletion request rather than another retention setting:
  **every copy carries the same ciphertext.** The WAL segment, the state record, the
  recovery checkpoint, the exported OpenSearch document, an instance snapshot somebody
  exported, last year's backup tape — all of them hold bytes the destroyed key decrypted.
  Nothing has to be found, coordinated or reached, which is something no retention schedule
  can claim. What is *not* claimed: this renders the data permanently unreadable, it does
  not remove the bytes, and whether that satisfies a given supervisory authority is a legal
  judgement for the operator's data protection officer.

  The engine never enciphers and never deciphers. A command already carries ciphertext, so
  nothing is enciphered per command on the hot path; state stores and returns bytes, never
  holds a key and never fails because one is gone, so an erased subject's instance replays
  exactly as it did before. Sealing happens where values enter — a start submission, a
  public form, a CSV batch, a worker's completion, a task's submitted form, an operator's
  override — and opening happens at the two places a person or a worker actually needs the
  value: the payload handed to a worker, and the form a person fills in. Everywhere else —
  the timeline, the variable audit, instance lists — the value is reported as what it is,
  "personal, for subject X", rather than as base64 nobody can read.

  Four things are refused rather than warned about. A declaration with no data subject, and
  a data subject with nothing declared, because personal data with no subject could never be
  erased and a subject with nothing personal protects nothing. A variable declared both
  personal and searchable, because the index would hold ciphertext under a random nonce and
  no search could ever match it. An engine-evaluated expression that writes *into* a declared
  variable, because the engine cannot encipher and the value would be stored in the clear. And
  a deployment declaring personal data on a server started with `--vault=false`, because
  there would be no key to destroy. On top of that the engine refuses two things at the write
  itself: any declared value that arrives readable — the check that makes the rule hold on
  paths no deploy can see, such as a message payload that correlates into a running instance
  — and a write that would change an instance's data subject after values are already sealed
  under the previous one, because that would split one person's data across two keys and then
  erasing either would leave the other half readable. Correcting the subject before anything
  is sealed goes through.

  Erasure is its own admin-gated route (`DELETE /api/v1/personal-data/{subject}`, with
  `GET /api/v1/personal-data` listing who is still erasable), and it writes one line to the
  security audit trail naming the subject and who acted — the only evidence that survives
  it, since the key is gone and the subject leaves no other trace, and being able to *show*
  that a request was honoured is half of what the obligation asks for. The secrets endpoints
  refuse the reserved name region outright, and audit the attempt: overwriting a data key
  would make somebody's data unreadable without erasing it, silently and with no record that
  it happened.

  The honest cost, paid in the one real example. Encipherment needs a subject and a deletion
  request needs an id it can name, so `account-bestellung`'s start form grew a
  Personalnummer no business requirement asked for — and for a new joiner that id comes from
  outside Atlas, because the account being ordered is the reason they have no account yet.
  And erasing a subject with a running instance leaves that instance unable to provision:
  its job is withheld and the reason is logged, which is correct and is not yet the clear
  message it should be.

- **A process can declare which variables hold personal data, and the compiler refuses a
  deployment that computes on one.** The declaration is one attribute —
  `atlas:personal="vorname,nachname"` — in the same shape and the same place as the
  searchable-variable list. What it buys is not a warning: a declared variable is
  enciphered before it ever becomes a command, and ciphertext cannot be compared, matched
  or routed on, so a process that reads one in a gateway condition, a mapping, a script or
  a timer expression **does not deploy**. The error names the variable, the kind of
  expression and the expression itself.

  That is the whole point of doing it here. The modelling recommendation this answers has
  been amber for exactly one reason — it relied on a modeller remembering — and a compiler
  that holds both the declaration and every compiled expression can simply decide it.

  The rule's edge is where the code evaluates the expression, not where it would be
  convenient. A worker's own expressions are outside it: that is the one place the record
  permits plaintext, for the duration of one call, and it is where a transform combining
  personal values belongs. So `= "Hallo " + vorname` in a mail body is fine and the same
  text in an output mapping is not — because one runs in the worker and the other in the
  engine.

  Nothing changes for a process that declares nothing, which is every existing model.

### Changed

- **The Account-Bestellung example builds its UPN in the worker, and lost a gateway doing
  it.** It is the proof the personal-data rule needed: the example took a first and last
  name from a public form and built a UPN, a mailNickname and a display name out of them,
  in one script and three output mappings the engine evaluated. All four moved into the
  create-user task's own attributes expression. It deploys, and **no exception to the rule
  was needed** — which is what its record could not establish against any process that
  existed.

  The cost is stated rather than quietly absorbed: a fail-closed gateway used to check the
  computed UPN against `jml-test-*@contoso.com` before any write, and there is no such
  process variable any more. The test-object boundary is now the `jml-test-` literal inside
  the connector's attributes expression — in the model, visible in review, but structural
  instead of checked at runtime. Here that is a small loss, because the gateway was
  checking a value the same process had built two steps earlier; where a derived value
  arrives from outside the process, it would not be.

### Fixed

- **Renaming a catalogue, or changing the languages it is offered in, failed with
  "list is not a function".** Both go through one form on the catalogue detail
  screen, and neither reached the server: the save threw before it got there.

  `catalog-admin.js` has a `list` helper that splits a comma-separated field into
  trimmed entries, and the save calls it for the languages. A hundred lines above,
  inside the same function, a DOM element had been bound as
  `const list = view.querySelector(".product-list")` — which shadowed the helper for
  the whole of it. The save called an HTML element, and the submit handler caught the
  `TypeError` and showed its message as a toast.

  That last part is why it was hard to place. A page that cannot run reported itself
  as a refusal, so the message read like the server rejecting the rename rather than
  like the screen being broken. The element is named `listEl` now.

  Three guards drive the real detail view: what a rename sends, that the languages
  arrive as a trimmed list, and that a working save reports nothing. Each fails when
  the shadowing is put back.

### Added

- **The catalogue screen now says what the portal is actually offering.** A catalogue
  and its portal are two different things on purpose: the portal reads a **release** —
  a frozen copy taken when somebody published — so an order cannot change under the
  person placing it, while everything a maintainer edits goes to the live records
  beside it. Nothing on either screen said so. The catalogue page listed its releases
  by date and left the reader to work out whether today's catalogue was one of them,
  which is not a question a date answers.

  One direction of that was not merely unstated but invisible. Take a product out of a
  catalogue and it leaves the product table at once; the release goes on offering it.
  It is then absent from every screen its maintainer has and present on the one they
  do not — which is how a product nobody can find in the catalogue keeps appearing in
  the portal, and why it looks like a corpse rather than a release doing its job.

  **`GET /api/v1/catalogs/{id}/unpublished`** answers the question directly: what
  publishing this catalogue would change for the people ordering. Products it would
  **add**, products the portal is **still offering** that it would take away, and
  products **edited since** the release being served. The catalogue screen draws it
  above the Publish button, removals first, because that is the group nothing else
  can show.

  Three decisions in it. "Edited since" is decided on the record's **revision** and
  not on a comparison of fields: every writer advances the revision — a test names
  them all — and a comparison of the fields this package happened to think of would
  miss the next field added. It therefore reports a little more than a reader might
  expect, and that is the safe direction, because the remedy is publishing and
  publishing loses nothing. A removed product is named from the **frozen** copy, not
  the live record, because the frozen name is the one on the portal and naming it any
  other way would describe something the reader cannot see. And a product offered here
  but homed in a catalogue the caller may not maintain is **never** reported as
  changed (ADR-0315): it is not theirs to compare, and reporting it would ask them to
  publish away a difference they have no way of seeing.

  The quiet answer is drawn too — "the portal is offering this catalogue exactly as it
  stands" — because a panel that speaks up only when something is wrong cannot be told
  apart from one that failed to check. A read that does not answer draws neither
  sentence, for the same reason.

- **A product can say what it is, and a process can capture one.** Two halves of the
  same gap: the product record had no description, and creating a product meant a
  console form with twenty fields and a hope that somebody looked.

  **`Description`** is a text per language tag, like the name beside it and
  deliberately unlike the keywords: keywords are for *finding*, and a searcher's
  language is not the catalogue's, while a description is for *showing* and is read
  in the language the portal is read in. A release demands nothing of it until there
  is one — most products need no paragraph — and then demands it in every declared
  language, because a product described to one audience and not another leaves the
  other an empty panel. The portal shows it without falling back across languages,
  unlike the name: a label in the wrong language still identifies the thing, a
  paragraph in one somebody cannot read is noise where an explanation was promised.

  **`examples/produkt-erfassung/`** is the capture process: catalogue, product data,
  what it is assembled from, prices — saved as a **draft**, then a **verification**
  showing every field again and still editable, and only then active, with the
  question whether to publish the catalogue. Every service task writes back through
  Atlas's own HTTP API with the `rest` connector and a connection named `atlas`, the
  route the shipped order fulfilment already takes.

  Three things in it are decisions. The product is saved **before** it is assembled,
  because the assembly is edges on the catalogue and the catalogue refuses an id no
  product answers to. The catalogue is **read afresh** before it is written and its
  revision carried along — a PATCH replaces items and edges whole, and minutes pass
  in which somebody else may have added a product; without it this process would be
  exactly the silent overwrite the revision field warns about. And the appearance is
  **a task of its own for administrators**, because a theme belongs to
  administration and not to catalogue maintenance — the process models that rather
  than working around it.

  **The logo is picked in the task form and never becomes a process variable.** It
  goes straight from the browser to the catalogue's own logo endpoint when the task
  is completed. The obvious alternative — base64 through the process — is the one
  thing this must not do, and `engine/budget.go` says why in the comment on
  `DefaultMaxVariable`: past a megabyte "it is a document, and a document in a
  token's scope is rewritten into the log on every touch". A logo is capped at half
  a megabyte, about 683 KB once base64 has grown it, and every step the process
  takes afterwards would write it into the write-ahead log again. ADR-0316 kept the
  same bytes out of the catalogue *record* for a weaker version of that reason.

  It is not a side channel: the endpoint is the one the Console's catalogue screen
  uses, called by the same browser with the same credentials, and it still demands
  PNG or SVG, half a megabyte and an administrator — which is the group the theme
  task is assigned to. The model names a **catalogue**, never a URL: a URL would let
  a model make whoever completes a task issue any request as them. A failed upload
  leaves the task open and says why, because a task that finished while its logo did
  not is a process that believes the catalogue is branded.

  Still deliberately absent: languages beyond German and French, which a static form
  cannot read off the catalogue. Said in the example's README rather than left to be
  discovered.

  The capture itself is **one task and not five**. Choosing the catalogue, entering
  the product, saying what it is assembled from and setting the prices are the same
  work by the same person in one sitting; five tasks would mean claiming and
  completing four more times, which is slower than the console form the process
  replaces. What the process is actually for — the verification — stays a station of
  its own.

  **Two new guards, and both found real defects.** One holds every user task to the
  form it names: a dangling `formId` compiles, deploys and runs, and the task simply
  reaches an inbox with nothing to fill in. It immediately found two shipped
  connection tests pointing at start forms nobody had written; both now exist, with
  the fields those models already documented.

  The other evaluates the FEEL in a shipped model against sample variables and
  states what must come back — because compiling proves almost nothing here. It
  found two defects in this very process: `append(a, b)` appends a whole list as
  **one element**, so the catalogue was being sent nested edges it cannot read, and
  `split("de, fr", ",")` leaves the space on, so a keyword arrived as `" M365"` and
  would never be matched. Both are valid FEEL doing the wrong thing in silence.
  A third trap is documented rather than relied on: a filter over a list of contexts
  returns the whole list in this build instead of filtering.

- **Narrow the starmap to the offerings you mean.** The element-type filter beside it
  answers "which kinds of thing do I want to see". It cannot answer "show me only what
  is actually orderable", because that is not a kind — it is a property of one — and the
  search cannot answer it either: a product's state is not a word in its name.

  The Product Map now carries the two facts a catalogue keeps about an offering that
  nothing else on the picture has. **Product state** lists draft, active and withdrawn;
  **Approval** lists whether ordering it stops for an approver. Both are boxes and both
  are on, like the element types above them.

  Three boxes for the state rather than one "active only" switch, because the two states
  that are not active are not the same thing and the difference is usually the point: a
  draft is being written, a withdrawn product was real and was retired. Collapsed into
  "not active" they become one heap and "what did we retire" cannot be asked at all.
  Three boxes contain the switch anyway — untick two and keep active.

  The approval side is a binary although the rule's kind is not. An installation can
  register its own approval process under any name, so listing the kinds would grow this
  control with somebody's own vocabulary and answer a question this picture is not
  about: which *route* an approval takes is a catalogue matter, and whether an order
  stops for a human at all is an estate one. A kind Atlas has never heard of counts as
  stopping for a human, which is the safe reading.

  **It only ever removes products.** A catalogue has no state and a process has no
  approver, so neither can be filtered by one. That is load-bearing rather than obvious:
  "carries no approval rule" is exactly how a product without one reads, so a filter
  that did not first ask what it was looking at would answer "ordered without approval"
  for every catalogue and every process on the picture, and one unticked box would empty
  the canvas. A process left with nothing attached stays drawn — that is what switching
  the whole Product type off already does, and a deployed process is part of the estate
  in its own right, not an appendage of whatever offers it.

  Like the element-type filter, the cut runs **before** the search and the drilldown, so
  neither reaches *through* a product you have put down. Emptying the canvas this way
  says which control did it, rather than sending you to the type boxes or to a search you
  never typed.

  **An export says which products were switched off**, and this is the narrowing that
  most needs saying: switching a type off removes a whole layer and the picture looks
  like it, while filtering products leaves the catalogues, the processes and the shape of
  the thing intact and quietly removes some tiles. The stamp names what was put down and
  states the consequence — a catalogue in the file may offer more than the products shown
  under it. A saved view carries the setting, stored as the catalogue's own words so a
  view reopened next year still selects the same products however the boxes are worded by
  then.

- **Put a whole element type down on the starmap.** The Product Map draws four kinds at
  once — the catalogues, the products they offer, the processes those products bind and
  a marker where nothing is deployed — and a reader who came to look at one of them had
  no way to set the other three aside. The search narrows by *name*, which answers a
  different question: "show me just the catalogues" is not a string anybody can type.

  The right-hand column now lists every element type the picture holds, each with a box,
  and **every box is on**. It is for putting part of a picture down, not for building one
  up: the landscape the server sent is the one it meant to send, and a kind nobody has an
  opinion about stays drawn — including one that arrives later, such as a catalogue
  somebody shares with you tomorrow.

  Switching a type off takes it off **before** anything else narrows the picture, and that
  ordering is the whole of the feature. A search keeps a hop of neighbours so a match can
  be read in place, and a drilldown follows the depth on screen; cut afterwards, both
  would reach *through* a hidden kind and leave what they found stranded on the canvas.
  So a process that was only on the picture because a product bound it goes with the
  products, and an edge whose other end is gone goes with it — a line to nothing is not a
  claim about the estate.

  Everything that describes the picture follows it. The count over the canvas is the
  count of what is drawn, not of what arrived; the key stops explaining what is no longer
  there; and switching *everything* off says so in words rather than borrowing the
  sentence a search that missed would use, which would send somebody to clear a search
  they never typed. The boxes are named in whatever vocabulary the picture is read in, so
  on an ArchiMate projection they say Grouping and Product.

  **An export says which types were off.** A picture narrowed this way leaves no trace on
  the canvas — no term in a box, no breadcrumb, just fewer things — so a file that called
  it "the whole starmap" would be exactly the export the stamp exists to prevent. The
  stamp now names them, and spells out the part a reader of the file cannot work out:
  what is missing is not only the hidden kind, it is everything that was reachable only
  through one. A saved view carries the setting for the same reason it carries the search
  term and the depth: a view is the whole question somebody saved.

- **The run graph's ordinal map and CSR, and the measurement that qualified them.** The
  projection [ADR-0404](docs/adr/0404-the-whole-graph-can-be-walked.md) decided on is
  built: a persisted key-to-ordinal map, and the two flat arrays a walk reads. The map is
  the sorted key array and the ordinal is the index, so there is no second structure to
  keep consistent and the inverse comes free. Every edge is held at both endpoints,
  because impact analysis asks in both directions. The build refuses to publish anything
  partial — a half-built CSR answers wrongly in a way no reader can see.

  The record's own premise turned out to be conditional, which is what building it found.
  §10 assumed that assigning ordinals in key order makes a component's nodes contiguous,
  and everything it concludes about where the projection may live rests on that. Measured
  against state the engine actually wrote, it holds **exactly** where instances arrive
  spread over time and **degrades linearly** with a burst: the span of a component's
  ordinal range is 1.00 at sequential arrival, 5.67 at eight instances in flight, 43.00 at
  sixty-four and 341.67 at five hundred and twelve. The law behind those points is exact
  rather than fitted, because the engine mints an instance's elements across as many batch
  phases as it has elements. It is pinned as that law in the tests, so a change to the
  engine's batching surfaces there instead of as an unexplained slow walk, and the record
  now carries the numbers, what they cost in pages, and the two ways out — without picking
  one, because nothing depends on it until a walk is wired.

### Added

- **A process definition and a deployment of it are two different things.** The starmap's
  `process` node has been both at once: what a model *is* — process id and version — and
  the fact that this server holds it, with instances, counters and a state. That is
  harmless with one server and unanswerable with several, because two installations both
  mint partition 0 counter 1 and nothing in a key says which one did.

  Both halves are now named. Every derived graph says which runtime derived it, and every
  deployment on it says which runtime holds it, so a key travels with the other half of
  its estate-wide name instead of alone. Nothing new is minted for this and no published
  id changes — the runtime id and the key both already existed, and the pair is the name.

  The definition is derived for every deployment and **drawn only where it has more than
  one**. On a single server it has exactly one, always, so drawing it would put a
  permanent twin beside every process: a node in a fixed one-to-one relation to another
  node is a field, not a second thing, and it would spend the measured 400-node layout
  budget to restate what the deployment already says. Where several servers hold one
  model, that definition is what they have in common, and *this process runs in three
  domains* becomes a line rather than a comparison somebody does by opening two tabs.

  Nothing on a single-server picture looks different yet. What changed is that the
  identity underneath it is now the one an estate can be drawn in, and that features
  stop accumulating on the conflated form — see [ADR-0401](docs/adr/0401-graph-identity-across-several-logs.md).

### Fixed

- **A decision service can be laid out and wired up.** Dragging one on the canvas was
  refused outright — the cursor went red and the box stayed where the import had put
  it — which is the one thing a diagram carrying several services cannot do without.
  When it did move, the modeler re-decided which compartment each of its decisions
  belongs to, and for a decision drawn outside the box, which an imported model may
  well have, the divider travelling past it turned the service's output decision into
  an internal one: the service silently lost the interface it publishes. A decision
  service also could not be connected to anything. DMN makes one an invocable, like a
  knowledge model, so a decision invokes it through a knowledge requirement; a model
  that already said so opened and drew correctly, but the connection could not be made
  by hand. All three are fixed, and the eleven connections the specification permits
  between DRD elements are now each covered by a test.

  The notation itself is held to the specification as well, in both pictures Atlas
  draws. Input data is a stadium at any size rather than only at the default one; a
  decision service carries the heavy border the specification asks for; and an element
  is drawn under the text its diagram gives it rather than its own name, where the two
  differ. In the decision graph window a knowledge model was drawn as a parallelogram
  instead of a rectangle with two corners cut off, and a knowledge requirement ended in
  the filled arrowhead that belongs to an information requirement — the two say
  different things, and the arrowhead is half of what says which.

### Fixed

- **A decision graph is drawn the way DMN draws one.** The DRD notation is not
  styling: the shape is how a reader tells one kind of node from another. A decision
  is a plain rectangle, input data a stadium with fully rounded ends, a business
  knowledge model a rectangle with two corners cut off, and a decision service a
  rounded rectangle with its name in the top right. Atlas drew decisions and
  knowledge models with rounded corners, which made a decision read as an input
  datum or a service, and drew a decision service square-cornered with its name
  centred over whatever it contains. Both pictures now follow the notation, so a
  model opened in Atlas looks like the same model opened anywhere else.

  The modeler follows too: the vendored dmn-js carries the same correction, and a
  decision service that declares itself collapsed is now drawn as one — name over a
  plus marker, no divider, nothing nested inside a box the document says is closed —
  instead of looking expanded.

### Fixed

- **A decision service survives Auto-layout.** Atlas generates a DMN model's diagram
  when one is missing and redraws it on request, but the generator only knew decisions,
  input data and knowledge models. A decision service — the box drawn around part of the
  graph, split by a divider line into what the service returns and what it works out
  internally — was not drawn at all. Auto-layout therefore deleted the box, and the
  modeler, which reads a service's membership back out of where its decisions sit,
  concluded the service had no members and wrote that into the model on the next save.
  The result was a service with no output decision: still deployable, still callable,
  and returning nothing, with no incident and no validation error to show for it. One
  click was enough, and only the stored XML showed the damage.

  The generator now draws the service around the decisions it publishes and encapsulates,
  with the divider between them, and a model whose service is undrawn is laid out afresh
  instead of being handed to the modeler half-finished. The bundled modeler treats the box
  as a container too: it is drawn beneath what it holds rather than over it, and moving it
  carries its decisions with it. A decision the service names as its input boundary stays
  outside the box, where DMN puts it.

### Added

- **Eine Entscheidung hinter einer Schnittstelle zeigt jetzt auch ihre Regeln.** Ein
  Business-Rule-Task kann einen **Decision Service** aufrufen — DMN's veröffentlichte
  Schnittstelle über einen Teil des Entscheidungsgraphen. Bis jetzt behielt eine solche
  Auswertung ihre Eingaben und ihr Ergebnis und nichts darüber, wie sie dorthin kam: die
  Engine bot für einen Service keine Option zum Aufzeichnen an. Damit fehlte die Erklärung
  genau dort, wo sie am wenigsten zu entbehren ist — die Entscheidungen hinter einer
  Schnittstelle sind in der Regel Tabellen, und wer einen Fall verantworten muss, konnte
  „es wurden keine Regeln aufgezeichnet" nicht von „keine Regel hat getroffen"
  unterscheiden.

  Die Option ist upstream nachgezogen worden (temis#226) und wird hier durchgereicht. Eine
  Service-Auswertung ab jetzt trägt die Tabellen, die der Service hinter seiner
  Schnittstelle ausgeführt hat — im Entscheidungsgraph-Fenster mit grün gezogener Regel wie
  bei jeder anderen Entscheidung. **Die Grenze gilt auch in der Spur:** eine Input-Decision
  liefert der Aufrufer, der Service berechnet sie nie, also taucht ihre Tabelle nicht auf.

  Was **vorher** aufgezeichnet wurde, trägt weiterhin keine Regeln und wird es nie: ein
  Datensatz ist eingefrorene Geschichte, nichts, was man nachträglich neu rechnet. Die
  Oberflächen sagen darum jetzt, *welche* Stille sie vor sich haben — die des Alters eines
  Datensatzes, nicht die eines Unvermögens der Engine.

- **The starmap says which dependencies are actually used.** Every line on the starmap
  was a *declared* dependency: a call activity names a process, a service task names a
  worker, a business rule task names a decision, and the picture drew all of them the
  same. So a worker nobody has ever called and one carrying the whole business looked
  identical, and the two questions an operator actually brings to a landscape — what can
  I retire, and what carries the load — were the two it could not answer.

  Each of those lines now carries how often it was taken. It costs no new bookkeeping:
  the engine has folded a cumulative activation count per element since the Operations
  heatmap needed one, every derived line is anchored on exactly one element, and the
  count is a join between the two. One decision called from two tasks is still one line,
  and its count is both tasks together.

  A line nothing has taken is drawn quieter and says so on hover, **always with the
  window it was counted over** — never as a verdict. The counter belongs to the deployed
  version, so a redeploy starts a fresh one, and a zero on a version deployed this
  morning says nothing about the path. A quarterly reconciliation, a compensation branch
  and an error handler are each correctly zero for months and each load-bearing, and a
  picture that called one of them dead would be believed once and distrusted afterwards.

- **Double-click a business rule task and the decision opens.** An evaluation has been
  durable history for a while — what went in, what came out, which rules fired — and it
  reached an operator as a card with the rule matrix behind a *hover*. A hover is not an
  affordance: nobody can see it, it is unreachable on a touch device, and what it reveals
  cannot be scrolled, selected, or pointed at while somebody else is reading the screen.
  So the one thing the record exists to answer was the one thing hidden.

  Double-clicking the task now opens the decision itself — the same gesture a call
  activity already answers with the process behind it — in a window holding the
  **decision requirements graph with the case drawn on it**. Every input datum carries
  the value it was given, every decision the value it produced, and the decision that
  answered is marked as the answer. Underneath sit the rule matrices, with the rule that
  carried the result in green and, on the rules that did not fire, the condition that
  ruled each one out. The Decisions tab's cards carry the same door, for a reader
  scrolling the list rather than looking at the diagram.

  It is drawn to be turned towards somebody who does not use Atlas, so nothing on it is
  inferred. The graph is the model the evaluation *ran* against, not the model as it
  reads today. A value appears on a node only where the record ties it there by name; a
  node nothing can speak for is drawn back and says "not part of this case" rather than
  being left blank. Colour says where a value came from rather than what kind of node
  holds it, which is the first time a decision service's boundary — a decision the
  caller supplied instead of the model computing it — is visible anywhere.

  One thing it cannot show: a **decision service** records no rule-by-rule trace, because
  the engine offers none for one. The window says that in as many words and keeps drawing
  the values, which are exact — rather than rendering a silence that would read as "no
  rules matched".
- **A business rule task can call a decision service.** DMN lets a model publish an
  interface over part of its decision graph: a decision service names what it returns,
  what it works out internally, and — the part nothing else can express — which
  decisions it does *not* compute, because the caller supplies their results as a
  boundary. Atlas ignored them entirely. A model carrying one deployed, the service was
  simply never looked at, and the only way through was to call the decision inside it:
  the same value, but the caller then has to know which decision is the right one and
  which inputs the whole graph happens to need, so nothing inside can be rearranged
  without breaking every process that calls it.

  A task's decision now names either. The deploy gate, the version pointers, the test
  panel and the picker all answer for a service exactly as they do for a decision, and
  a decision inside one stays callable, so nothing already deployed changes. A model
  that gives one name to both — or to two services — is refused with the name said out
  loud, rather than one of the two meanings being picked silently. What a service
  offers as its inputs is what a caller must supply: its input data and its input
  decisions, nothing else from the graph behind it.

  Two things are worth knowing. A service evaluation records its inputs and outputs but
  no trace, because the engine offers none for a service. And the editor still cannot
  *draw* a decision service — a model that has one comes from the temis Modeler, from
  Camunda, or from hand-written XML.
- **An incompatibility can be declared on the screen that declares everything else
  about a catalogue.** A product may exclude another — the clerk who may create a
  supplier must not also approve payments to it — and the record has carried that
  since the decision was made. Publishing freezes it into a release in **both**
  directions, and an order that would produce the combination is refused rather than
  reported afterwards.

  The Console knew three edge kinds of four. An incompatibility could not be
  declared there at all, and one that already existed appeared in no table and could
  not be removed, because the only remove button is on a row that is drawn. It was
  reachable over REST and MCP and by nobody using the screen — while being enforced
  the whole time.

  It now sits beside structure and precedence as the third question the relations
  section asks, and the pairwise form offers it, because it *is* pairwise: "never
  these two together" is a statement about two products that belongs to neither.
  Its symmetry is handled rather than passed on. One fact is **one row** however
  many directions were stored, removing it removes both — taking away the one that
  was drawn would leave the mirror, and the row would come straight back with
  nothing to say why — and adding the mirror of one already recorded is refused
  instead of stored as a second fact.

  The comment above the Console's copy of these vocabularies claimed they were
  "pinned by a test against the Go source so the two cannot drift". **No such test
  existed**, which is why the drift went unnoticed for as long as it did. It exists
  now, and it reads the constants themselves rather than a list kept beside them:
  a value added to the states, the approval kinds or the edge kinds fails it until
  the screen carries the value, can author it, and shows it.

- **A product can be offered from a date, until a date — and now that means
  something.** A catalogue item has carried an orderable window since the catalogue
  was designed, and nothing ever read it. Publishing checked that the window did not
  end before it began, which is a sanity check on the pair and not on the present;
  no reader anywhere asked whether *today* was inside it. So a product with a window
  was orderable exactly like a product without one, and the Console had no control
  for it either — which is how it went unnoticed for so long: nobody could fill it
  in from the screen, so nobody found out it did nothing.

  It is enforced now, where every other rule about a basket is read: an order
  carrying a product outside its window is **refused by the server**, with the
  product and the date named and which side of the window the moment fell on. The
  portal keeps the same window so a basket cannot be filled with something the
  placement will refuse — shown with a disabled control and the date rather than
  hidden, because a product that has not opened yet is exactly the case the field
  exists for, and the one thing somebody wants to know is when.

  Two boundaries decided deliberately. **Both ends are inclusive**, and the editor
  stores the end of the last day: somebody who writes 31.10. means the product is
  orderable on the 31st, and storing midnight would have closed it a day early,
  every time, with nothing about it looking wrong. And the **clock is read once per
  placement**, so the instant checked against the window is the instant the order
  records as its creation — read twice, an order placed across a boundary could be
  refused for a window that had already opened at the moment the order says it was
  placed.

  An integral part outside its window closes the product carrying it, and the
  refusal says so rather than naming a part nobody can deselect. The window governs
  **ordering** and nothing else: a right already held when it closes keeps running,
  because when a right *ends* is the ceiling beside it.

  **On upgrade:** an installation that filled the field in while it did nothing has
  products that will now refuse orders outside their dates. That is the correction
  rather than a regression, and it arrives without warning — worth a look at the
  windows in your catalogue before this lands.

- **The four fields the portal read and the product editor could not set.** A product
  carries twelve fields the portal acts on and the editor rendered eight. The four it
  did not render were not decoration: the **orderable shapes** are what the basket
  refuses to place an order without, the **search terms** are what makes a service
  findable by somebody who does not know its name, the **eligible groups** decide who
  may receive it, and the **ceiling** decides how long the right lasts. All four were
  settable over REST and MCP and by nobody else — which is to say, not by the person
  whose job it is.

  They are on the form now. The shapes read as one line each (`gross = 15 Zoll`, per
  language where the catalogue declares several), the terms as a comma-separated list,
  the eligible groups as a picker over the directory that falls back to an id field
  when the directory cannot be read, and the ceiling as a number of days where zero
  means a right that does not end.

  Rendering them changes who owns them: a save is a **full replace**, so a control
  somebody can empty has to be able to empty the field, while a field with no control
  has to survive the save untouched. Both halves are now proved in a browser against
  the real assembly rather than by reading the source. One field still has no control
  on purpose — the orderable window, which nothing anywhere enforces; a control for it
  would promise an effect that does not exist, and it gets one when the window is
  enforced.

  The editor also says, for the product open in front of you, **where the two headings
  are read**. Kategorie and Produktgruppe are collected from the products nothing
  contains, so a product that is a part of another one is reached through the product
  carrying it and its own heading is never read there. The hint claimed the category
  was "the heading this product sits under", which is false for a part — so a
  maintainer could fill in a column that had already stopped reading the field, with
  nothing to say so. Where the catalogue open carries this product inside another, the
  form now names the carrier and says the heading is read there. It stays a note and
  not a hidden field: containment belongs to a catalogue, so the same product is
  legitimately a part here and offered in its own right next door, where the heading
  *is* read.

- **A Product Map on the starmap: what you offer, beside what has to run for it.**
  Atlas held two halves of one estate and drew them on two screens. The starmap is the derived half — applications,
  deployed processes, the workers they use, every edge a fact the server can point at.
  The catalogue is the other: what a group of people may order, what each product is
  assembled from, and the process that provisions it.

  They are joined in the data and were separated on every screen. A product names a
  BPMN process id; whether anything is deployed under that id is a question the
  catalogue screen cannot answer, because it cannot see the engine, and Operations
  cannot answer either, because it has never heard of the catalogue. So a product goes
  out bound to a process nobody deployed, looks orderable, and the first person to
  order it waits while the order parks.

  The starmap's picker — now called **View**, because only some of its entries are
  vocabularies — gains **Product Map**. It draws the catalogues, the products, the
  arrangement between them (*included*, *optional*, *precedence*) and the processes
  each product binds to provision and revoke it, resolved exactly as a call activity
  is: the deployed process, a placeholder for one you may not see, or the same
  **unresolved** shape everything missing on this picture already takes. A product
  bound to nothing is a product pointing at a hole, in the same ink as every other
  broken dependency, with nobody having modelled anything.

  **The landscape itself carries no product.** They are two pictures rather than one
  busier one, for two reasons: an operator opening the starmap because something is
  stuck does not want a hundred products between them and it, and every product would
  otherwise spend the size budget — so one large catalogue could collapse somebody
  else's landscape to applications, and that somebody would never learn why. The
  product map carries the bound processes and nothing else of the estate: not the
  workers they use, not the applications that hold them, not the peers. One hop,
  because the second hop is the landscape's question and the landscape is one entry
  away on the same control.

  **What it will not say is as deliberate as what it will.** Incompatibility — two
  rights that must never be held by the same person — is not drawn: it is the one
  catalogue relationship that means the opposite of every other line on the canvas,
  and it is named in the picture's own loss list instead, which until now was empty
  because the derivation had nothing to declare. Nothing here carries a health state,
  so an unpublished catalogue is not a fault; a catalogue somebody is still filling
  would otherwise be red for a week. Orders and prices are absent: an order is
  runtime, a price is on the catalogue's own screen behind the catalogue's own rules.

  **In ArchiMate's vocabulary** — reached through the export, which follows whichever
  picture you are on — a catalogue is a Grouping and a product a Product, and
  an integral part is a **Composition** while an optional one is an **Aggregation** —
  the first relationships this landscape can name exactly rather than approximately,
  drawn with the standard's own diamonds and written into the exported document.
  Precedence is drawn and not exported, because ArchiMate has no relationship that
  means "this cannot be provisioned before that", and the export says so rather than
  picking the nearest wrong one.

  **Who sees it follows the catalogue, not the picture.** A catalogue is on the
  starmap for whoever maintains it — its owner, an editor, somebody it was shared with
  — and never for the people it is offered to: reaching a catalogue as a customer says
  what you may order and nothing about the estate behind it. A modeler who maintains
  no catalogue therefore sees none, exactly as they see no application nobody shared
  with them — and the picture **says so in words** rather than leaving an empty canvas
  to be read as a broken feature. It names both reasons and picks neither: whether no
  catalogue exists yet or none is yours is the one thing this picture must not tell
  you, because telling you would disclose that catalogues exist which you may not see.

- **A product is assembled from the services that exist, instead of related pairwise.**
  A catalogue is built out of services that each provision themselves; what a product
  adds is an arrangement — which of them come with it and cannot be deselected, and
  which are offered beside it. That arrangement was authored as edges: pick a *from*,
  pick a relationship, pick a *to*, one triple at a time into a table sorted by
  relationship. It was the data as it is stored, and never showed what one product is
  made of.

  The catalogue screen now has a **construction kit** per product. It lists every other
  product the catalogue offers and asks the one question, once per row, with three
  answers that leave nothing out: **not part of it**, **included**, **optional** — each
  pre-selected from how it stands today. One save writes that product's whole
  structure, which is why "not part of it" is an answer here at all and was a removal
  before.

  What the kit was not asked about it does not touch: every other product's
  arrangement, and every precedence edge including the assembled product's own. The
  save carries the revision the page was read at, so a second maintainer's arrangement
  cannot vanish into it.

  A choice that closes a loop is **refused before it is written**, naming the product
  that already contains this one, directly or through another. Publishing still proves
  the whole catalogue — that is where the proof belongs — but a refusal is worth most
  at the moment the choice is made, rather than three screens later about a catalogue
  that has since been edited.

  The pairwise form stays, narrowed to **precedence** only. Precedence is a statement
  about two products and belongs to neither, so it is the one relationship a pairwise
  form is the right shape for; structure had two ways to be said, and two ways drift.

- **An approval is read and decided in the inbox.** An approval is an ordinary user
  task, so the rows were always in `Tasks` — rendered like every other row, saying
  nothing about the product, the price or the person waiting, and decided by opening a
  second surface in another tab. Every approval task is called "Genehmigen", so a
  queue of them was a column of identical lines.

  Each row now names what it decides — the product as the catalogue wrote it, and the
  cost — and the detail leads with the rest: the variant, who it is for, who ordered
  it, and the order. All of it comes from `GET /api/v1/approvals`, which the inbox
  already called to know which of its rows are approvals; nothing on the server
  changed.

  **The decision happens there too**: Approve and Reject, with the reason a rejection
  needs, and — where the same order has more approvals in this inbox — an offer to
  decide them together under one reason, which is what `POST /api/v1/approvals/decide`
  exists for.

  For the approval Atlas ships there is now exactly **one** way to answer in that
  screen. The generic Complete button and the form's own "Genehmigen" checkbox
  answered the same question by accident: a task completed with no variables reads as
  `genehmigt = null`, which is not `true`, which is a rejection — recorded with no
  reason and no sign that nobody meant it. So for that model the form and the Complete
  button give way to the two buttons, and Ctrl+Enter says so rather than doing it.

  An installation whose products name **its own** approval model keeps its form and its
  Complete button untouched: `genehmigt` and `begruendung` are the shipped form's
  contract and not a general one, and two buttons answering for a model Atlas cannot
  read would complete somebody's task with variables their process never sees. The
  block still says what is being decided, because that half is true of any approval.

  The standalone approval page stays: it is what an approval notification links to,
  and somebody arriving from a mail has no inbox to arrive in.

- **A product can carry a picture.** A catalogue row was a name and a price, and
  somebody choosing between two phones was choosing between two names. A product now
  has a picture — a photograph of the thing or the vendor's mark, PNG, JPEG or SVG —
  uploaded in the product editor and shown in the portal when the product is opened.

  It is stored the way a catalogue's brand mark is: a file beside the stores, keyed by
  the product, with **no flag on the record saying one exists**. The file is the fact,
  and a second copy of that fact is a second copy to be wrong after a restore that
  brought the JSON and not the image. What was uploaded is what is served — no
  resizing and no re-encoding, because a server that re-encodes somebody's picture
  decides their product looks near enough.

  Who may see it is the question the portal actually asks: **does any catalogue this
  person may read offer this product**, and not "may they read its home catalogue".
  A product is referenced by catalogues rather than owned by one, so a customer of one
  catalogue legitimately orders a product whose home is another — gated on the home
  alone, that customer would see a name and no picture. Changing it stays with
  whoever maintains the product: somebody who may not rename it may not re-illustrate
  it either.

  **A release does not freeze it.** A release freezes what was promised — the product,
  its variants, the approval rule, the ceiling, the price. A picture is how a thing is
  shown and not what was agreed, so a better photograph of the same laptop appears on
  orders already placed rather than a second picture being kept for them.

- **Where your own position stands, without an operations surface.** A position in
  "Meine Aufträge" now answers *which step* it is sitting on — "Genehmigen",
  "Provisionierung starten" — to whoever the order belongs to, and not only to a
  reader holding the operator role.

  The link that existed leads into the console, and the console shows the whole
  engine state of that instance, variables included. Widening it would have handed
  out an operations surface to answer a question about one line, so the orderer is
  answered by a route of their own instead:
  `GET /api/v1/portal/orders/{id}/lines/{position}/progress`, gated on **owning the
  order** rather than on a role. Somebody else's order answers 404 and not 403 —
  whether it exists is not something this confirms — and no process variable leaves
  through it: the caller already knows their own order, and the route says *where*,
  not *what*.

  The server finds the instance by the two variables the fulfilment model passes,
  `orderId` **and** `positionId`, and by both: the order id alone also matches the
  order's own orchestration, and a position key alone is unique only inside one
  order. Only live instances are walked, which bounds the cost by the work in
  flight rather than by everything the store has ever run — and a finished instance
  has no step to report. The step is the name the model gives the element, read from
  the deployed document, falling back to its BPMN id where it is unnamed.

- **A model fix now reaches an instance even when its tokens cannot be carried across.**
  Migrating a running instance onto a corrected version rebinds it in place and keeps
  everything it has done — but only where every token's element still exists in the new
  version, as the same kind of element, in the same scope. That refusal is deliberate: a
  token left on an element that means something else corrupts an instance in a way no
  later fix repairs. Until now it was also the end of the road, and the operator was back
  to cancelling the instance and re-entering its data by hand — in precisely the case
  where the fix matters most, because a model that was genuinely restructured is the one
  whose elements moved.

  An instance can now be **continued in a new instance** of the target version instead.
  The instance is ended where it is, a successor of the new version starts at the elements
  you name — proposed from where its tokens are now, whenever the ids survived the edit —
  and its variables and data objects come across with it. Both records name the other, so
  the old replay says "continued as …" and the new one says "continues …", and each is one
  click from the other. Nothing already done is undone, and the old instance stays
  readable exactly as it ran.

  It is a different operation from a migration, not a fallback the server takes on its
  own: work in flight — open jobs, user tasks, incidents, armed timers and subscriptions —
  ends with the instance it belonged to, and the dialog says so, with the counts, before
  anything is written. The migration dialog plans both readings of "move this instance to
  that version" in one call and shows the fork below the rebinding, dimmed while the
  rebinding is still on the table. A reason is required and recorded on both instances.
  Refused before anything is written when there is nowhere to resume, when a resume point
  could not run on its own (a boundary event, an event subprocess, a joining gateway, an
  element inside a subprocess), and for a call activity's child, whose caller waits on the
  instance being ended. New: `POST /api/v1/instances/{key}/migrate/fork` and the
  `atlas_fork_instance` MCP tool; `…/migrate/plan` now answers both.

- **Every position carries its own way into the process working on it.** "Meine Aufträge"
  already listed each position and what it was doing, out of the order's own
  record, and that stays the answer for every reader. A reader who may open an
  instance — the operator role, which is what every route that finds or opens one
  requires — now also gets a link into the running instance, looked up when it is
  pressed rather than resolved for every row.

  The order's link is narrowed to the fulfilment process, because every provisioning
  sub-process carries the order id too and the first hit would be one position's
  process wearing the order's name. **Each position carries a link of its own**, and
  it is found by the position's id rather than the order's: the search answers with
  only the variables that matched the query, so a search for the order returns every
  instance it started, each carrying `orderId` and nothing else, with nothing left on
  the page to tell them apart by. That matters because "the order is running" and
  "this line is waiting on an approval" are different answers, and only the second is
  what somebody reading their own order wants.

  There is deliberately no fallback to the product id: it matches instances from every
  order that ever carried that product, and the answer cannot be narrowed by the
  order. A position whose instance is not found — history retention removes one long
  before the order it fulfilled — is said rather than approximated.

- **One product may be ordered in two shapes at once.** The catalogue describes the
  same service pulled in twice in different variants as a conflict the orderer
  resolves, and keeping both is a resolution — a black phone and a silver one. It
  was not expressible: a line was identified by its product, so two of them
  collapsed in every map the order builds and an outcome reported for one landed on
  whichever came first.

  A position is now identified by its product **and** the shape of it —
  `itemId#variantId`, and plainly `itemId` where there is no variant, so no order is
  migrated, no record gains a field, and a process built against `/lines/{itemId}`
  keeps working. Where an order carries two positions of one product, naming the
  product is **refused** with both position names rather than applied to one of
  them. `POST /api/v1/orders` takes one entry per position in `variants`; a second
  entry is refused unless the catalogue says the product may be held more than once,
  and two entries of the same shape are refused outright, because two identical
  positions cannot be told apart and this catalogue has no quantities. The fulfilment
  process passes `positionId` beside `itemId`, and `/next` names each position
  (ADR-0384).

  **One limit, named rather than left to be found:** the inventory still records one
  hold per person and product, so somebody who orders two shapes is provisioned
  twice, correctly, and recorded as holding one. That is how a repeated order has
  always been recorded; making the entitlement identity carry the variant is an
  engine change with its own replay consequences.

- **How long the single writer is held is now a metric.** Two histograms,
  `atlas_runloop_turn_held_seconds` and `atlas_runloop_turn_wait_seconds`, pushed from
  the run loop itself.

  The run loop is the one duration in Atlas that is about the whole server rather than
  one request: it is the single writer, and it is the gate every request passes
  through — a read-only one included, since opening a consistent view takes a turn
  (ADR-0239). A turn that runs long does not slow one caller down, it stops everything,
  and until now nothing measured it. The batch counters do not: a batch is work the
  processor did, while a turn is work somebody dispatched, and the two that hurt most —
  publishing a checkpoint, resolving a compaction cut — are not batches at all. That is
  exactly how they held the writer for seconds without a number anywhere saying so
  (ADR-0382).

  Both halves are reported because they answer different questions. `held` is the
  cause: how long a closure occupied the writer, which is the number a fix like ADR-0382
  moves. `wait` is the effect: how long a caller queued before its closure even started,
  which is what a person experiences as the interface hanging. A server can have one
  long turn and nobody notices; it can have many medium ones and a queue nobody gets
  through. `held` alone cannot tell those apart.

  Buckets run from 100µs to about 13 seconds — wider at the top than the fsync
  histograms beside them, because a turn is the whole server standing still and the
  range has to reach the failures worth alerting on rather than saturate at the first
  one. `Ping` is deliberately not counted: its closure is empty, so counting it would
  fill the histogram with work nobody performed. An uninstrumented loop reports nothing
  and does not even read the clock.

- **A standing list of the approval rules that reach nobody.** An approval rule names
  an approver, and what that name has to be differs by kind: a named person becomes a
  task's assignee, matched against a username, and a group becomes its candidate
  groups, matched against a group id or name. **Neither is checked when the rule is
  written, and neither failure is reported when it fires.** The approval is created, it
  lands in nobody's inbox, and the order waits without saying why — the first person to
  notice is whoever is waiting for the laptop.

  The catalogue listing now carries the list: which products name an approver that
  resolves to nobody, what each names, why it reaches nobody, and a link to the
  catalogue where it is corrected. Scoped to the products you may maintain, because it
  names people.

  It deliberately stays quiet about three things, so that what it does say is worth
  reading: a rule that still reaches somebody (candidate groups are a list, and one
  live entry is enough), a kind that names an approval process directly (there is
  nothing to check it against), and a leftover reference beside a kind that needs none
  (untidy, not broken).

  Where the server cannot read accounts or groups at all, it refuses and the page says
  so. The two available guesses are both worse: every rule reported as broken, or a
  clean estate nobody checked — and the second is the one somebody wants to believe.
- **An outage now stops at the worker instead of at every token.** A worker whose target
  stopped answering did not fail once. It failed once **per instance that reached its
  task**: each failure spent a retry, each exhausted budget parked a token behind its own
  incident, and every one of those calls went into a host that was already struggling. An
  hour of SMTP being down, on a process starting a few thousand instances in that hour, was
  a few thousand incidents for somebody to clear — and the only thing Atlas could say about
  a failing integration was a backoff one worker asked for on one job, which cannot express
  "stop asking, the other end is down".

  A **circuit breaker per Worker** now sits in the dispatch path, on both halves of it: the
  in-process runner and the external pull. Three consecutive failures from three *distinct*
  process instances judge a target down, and its jobs stop being handed out. They stay
  activatable, unleased, with their retry budgets untouched, waiting exactly as they wait
  for a worker that has not polled yet. One job per cooldown goes out as a probe — ten
  seconds, doubling to five minutes — and a success closes the breaker, after which the
  backlog drains by itself with nobody resolving anything.

  The distinctness is the whole trip condition. One instance with a bad record fails its
  entire retry budget against a perfectly healthy host, and stopping the integration over it
  would punish every other instance for one bad record; a dead host fails instances that
  have nothing to do with each other, which no data fault does.

  Nothing about this is durable. Whether a host is reachable right now is not a fact about a
  process, so a restarted engine starts with no opinion about anybody's target, and no job
  record grew a field. Holding work back never invents a business outcome either: a held
  token is not cancelled, completed or failed, and no incident is raised for it — an
  incident is a fact about a token, and none of these tokens is at fault.

  **Held work is visible, because silence is the one failure mode nothing else surfaces.**
  A growing queue with no incidents under it used to mean "nobody is serving this"; it can
  now also mean "Atlas has stopped serving this", and those need telling apart. So
  Operations → Workers grows a **Held back** card above the queue depths, naming each
  target, since when it has been held, what it last failed with and when the next attempt
  is due. **Close now** on the row releases it for an operator who has already fixed the
  endpoint and will not wait out a cooldown — and if the target is in fact still down, the
  next three failures simply hold it again, which is why closing is safe to expose and
  "open this by hand" is not offered at all.

  Every state change is logged as `worker.breaker_open` / `worker.breaker_closed`, and
  `/metrics` carries `atlas_worker_breakers_open` with the totals
  `atlas_worker_breaker_trips_total`, `_probes_total` and `_refused_total`. Those are
  aggregates without labels on purpose: a Worker's name comes from a deployed model, and a
  metric label carrying one would be a label whose values the data invents — which an
  estate of a few hundred Workers turns into a few hundred time series. *Which* target is
  a question for the Workers view, which is how ADR-0142 says a per-thing breakdown should
  be answered.

  The handbook says all of it under **Operations & incidents**, in both languages,
  including the warning that matters most: a queue growing without incidents is not
  evidence that everything is fine.

  An agent sees it too. `atlas_workers` now carries the held rows, and its description
  says so where it matters: the diagnosis it used to teach — a deep queue with nothing
  in flight and nobody pulling — is exactly what a held target looks like, and reading
  one as the other sends an agent after the wrong thing. `atlas_close_breaker` is the
  one action, for an agent that has just fixed the configuration it was holding on.

- **A product manager maintains the catalogue over MCP.** The portal's catalogue was
  the one substantial surface an agent could not reach. The omission was recorded and
  deliberate — a tool is a public contract, and the catalogue was half-built when the
  note was written. It is not half-built any more: ordering, approvals, releases, the
  inventory, reconciliation, search, categories, prices and eligibility all landed
  since, and the note outlived its own argument.

  Nine tools now cover what a product manager does: list, read, create and change
  catalogues; list and save products; publish a release and read the releases; and
  derive drafts from an ArchiMate model. Each is one HTTP operation and nothing more,
  so an agent reads the same refusal a person reads — a publish that is refused still
  answers with every problem at once, naming the product each belongs to.

  Two things a screen teaches for free had to be said out loud, because an agent has
  none. **Saving a product replaces it**, so a field left out is a field cleared —
  every write tool says to read the record first and send it whole. And **nothing
  deletes**: a product is withdrawn through the ordinary save, because an order placed
  years ago and an entitlement still held both resolve through it, so there is no
  delete tool to look for and not find.

  Authority is the caller's and is not widened anywhere: the routes need the
  `productmanager` role plus editor on the catalogue, and the adapter carries the
  credential the tool call arrived with. Over the stdio adapter, which authenticates
  with an API token, the read tools work and the write tools are refused — no API
  token can carry `productmanager`, deliberately, so that no account is handed
  catalogue control by an upgrade.

- **A product can be saved without overwriting somebody else's edit.** The product
  write stores the record it is given, which is right for a form that renders every
  field and posts every field back, and dangerous for anything that changes one field
  of a record it read a minute ago: the other maintainer's change disappears with
  nothing to say it existed.

  A product now carries a `revision`, and a caller may state the one it read. The
  write is then refused as a conflict unless the stored product is still on it. It is
  the same rule, spelled the same way, that the capability map has used since it was
  built. Stating it is optional and omitting it replaces unconditionally, so the
  Console — which builds its body from form fields and knows no revision — is
  untouched.

  It counts revisions rather than comparing the `updatedAt` beside it, and that is not
  a preference: `updatedAt` is Unix nanoseconds, past the 2^53 where a float64 stops
  representing integers exactly, so every client that decodes JSON numbers as doubles
  would hand back a value a few hundred nanoseconds off and be told its own read was
  stale.

### Removed

- **The standalone approval page.** It existed because the Console is an operator's
  instrument and most approvers are not operators — right about the people, and wrong
  about what followed from it: an approval *is* an ordinary user task and the inbox
  never filtered those out, so the rows were always there. The page did not spare
  anybody the Console; it was a second place to take one decision, and the two drifted
  over whether a rejection needs a reason.

  The decision is in the inbox now (see the entry above). What stays is the page's
  **address**: every approval notification ever sent links to `/genehmigung.html` with
  the order line in its query, and a mail cannot be recalled — so it forwards, handing
  that line to the inbox, which resolves it against the approvals the reader holds. The
  three shipped approval models link into the inbox from now on, and the menu entry
  under Tasks is gone: it led to a redirect back into the screen it sat under.

  **What is lost is the brand.** The page wore the catalogue's colours, because an
  approver decides on that customer's behalf; the Console wears nobody's, so the block
  names the catalogue in words instead. Information kept, presentation dropped.

### Changed

- **Every process Atlas ships names one mail worker, and it is called `mail`.** The
  platform processes (ADR-0122) addressed their mail tasks to two different workers,
  and neither name said what the worker was. The three user management processes —
  intake, access review, offboarding — named an individual, and the access review sent
  its "action required" mail to that person's fixed private address; the three
  approval processes, and the examples built on them, named `portal`. Both values are
  embedded in the binary and bootstrap-deployed into the system project, so a server
  nobody had configured yet listed a private person's name under **Workers nothing can
  serve**, next to a second entry for the same job — two workers to configure for one
  way of sending mail, one of them keyed to someone else's name.

  There is now one name across all six, and it says what the worker is rather than who
  first configured one or which screen the mail was sent from: **`mail`**. The
  review's recipient is asked for on the start form (`meldung_an`, required, validated
  as an e-mail address) instead of being frozen into the model, which is what the other
  two processes already did with their own recipients. Nothing about the mail path
  itself changed: `connector="…"` is still the attribute (ADR-0203 renamed the
  vocabulary, not the models).

  **For an existing instance:** a mail worker configured under either old name is no
  longer found — rename it to `mail` under *Console → Workers*, and where both existed,
  keep the one whose provider you want and delete the other. The changed bytes make the
  next start deploy one new version of each of the six processes; instances already
  running stay on the version they started on.

- **The product editor opens beside the product list, level with the row it was
  opened from.** It used to render under the table, which is fine with three products
  and unusable with forty: editing a row near the bottom put the form below everything
  offered, so it was read after a long scroll and with no sight of the product it
  belonged to — the id in the first field being the only thing saying which one was
  open.

  The list and the editor are now two columns, and the panel is pushed down to its
  row: the product, its row and its form are on one line across the page, and the row
  is marked while its form is open. **The catalogue's page drops the centred content
  column** to carry them, the way the Tasks inbox does — 1120px divides into a table
  of products and a form of about 520px each, and both hold more than that. What is
  prose on the page keeps its own measure, so nothing turns a paragraph into a line
  across a 2000px screen, and the tables' action buttons move to the right edge —
  the console's own rule for an action column, which a full-width table needs and a
  1120px one could do without. The offset is measured in the browser rather than
  stated in the stylesheet, because the shared table enhancer sorts and filters the
  rows underneath it — sorting the list moves the panel with the row it belongs to.

  Opening the panel takes a column off the list, so the rows above rewrap and the row
  that was clicked would be pushed down the page by text nobody is reading. It is held
  still: how far it travelled is measured and the page is scrolled back by exactly
  that, so the list does not jump away from the click.

  **The kit opens in the same place.** What a product is made of is a question about
  one row exactly as its form is, so it uses the same column and the same alignment,
  and a row has one of the two open at a time — opening either gives the panel up.
  Its choice cells now carry the control alone: the column heading already says which
  answer it is, and repeating that word in every cell cost the table 180px of width.
  A reader who cannot see the column still hears both, from the radio's own label.

  **The catalogue's own two cards are read side by side**, at the top of the page:
  what a catalogue is, and what it looks like. They are the two questions about the
  catalogue itself rather than about anything in it, and stacked down the left edge
  they left the first screenful of a widened page half empty — the page was wide and
  did not read as wide. A number field is drawn like every other field while this is
  here: `Rank` was the one control on that card wearing the browser's own default.

  **Every list on the catalogue page has its form beside it**, because the page is
  four times one shape: the catalogues and the one being created, the products and
  the panel that edits them, the relations and the pair being related, who maintains
  it and who is being added. The shape is stated once, and each section says which
  half it is — a fifth of them cannot invent a fifth layout.

  **The stacked layout remains, and it is the fallback rather than a lesser page.**
  The breakpoint is measured rather than chosen, and it is one number for the whole
  page although the smaller pairs would fit earlier: a page that puts its lists beside
  their forms at three different widths is three pages to somebody dragging a window
  edge. The widest pair decides it — the product list cannot be drawn under 853px
  (five columns, a process id, and a row offering edit, assemble and remove) and the
  kit beside it needs 464px — which at 35% of the page is 1400px of window. Below
  that one of the two would be narrower than its own content and would scroll sideways
  inside its box, losing the column with the buttons in it first; stacked, each of them
  gets the whole page. The panel sticks to the top of the
  window while it is scrolled and carries its own scrollbar, because a form longer
  than the screen that cannot scroll inside hides the Save button it exists for.

- **The catalogue reads Kategorie › Produktgruppe › Produkt › Services, and there is
  no Bundle level.** A bundle is offered as a *Marktleistung*: it holds the
  orchestration process, and the services behind it hold their own provisioning and
  deprovisioning — a service may stand behind several Marktleistungen, included or
  optional, always with the same processes.

  So the Bundle level had nothing to name. Every root is a Marktleistung, with or
  without parts, and everything behind one is a service however deep it sits; the
  level rule is now depth and nothing else. This withdraws the *answer* the previous
  rule gave, not the rule that there is one: a catalogue that has to decide per
  product which of two words describes it gets that wrong for every product somebody
  adds a part to later, and nothing downstream needed the distinction — an order, a
  release and a provisioning call name items, not levels.

  The vacated column holds the **product group**, a second heading a product writes
  on itself beside its category. It is a string with the costs ADR-0360 states and
  accepts, and the chain is therefore a *display* chain: the group has no record and
  no category of its own, so the relation is read off the products carrying both. A
  group whose products sit in two categories appears under both, and a group with no
  products does not exist — neither is an error state, because nothing claims a group
  belongs to one category.

  The cascade stops deriving a level altogether: its columns are the levels. The
  basket and the list of what somebody holds still derive one, because they hold a
  set of positions with no layout to read it off. ADR-0383 carries the amendment.

- **Approvals moved under Tasks, and the inbox says which of its rows decide an
  order.** Approvals was advertised as an application beside Modeler and Operations,
  and it was empty for almost everybody who saw it — there is no approver role to
  gate on, because a product names a person, a group, or the orderer's superior, so
  anybody signed in may hold an approval tomorrow without holding one today.

  It was also, already, in the inbox. An approval is an ordinary engine user task,
  the task list does not filter those out, and the inbox never knew the word — so
  the same decision sat in two places and neither said it was the same thing. The
  entry now sits under Tasks, where Access review already sits for the same reason,
  and an inbox row that decides an order carries a chip saying so and leading to
  where it is decided. The link names the order line rather than the task, because
  that is what the approvals page takes: a task key does not exist until the task
  activates, and it changes when the task is reassigned, while the order and the
  product do not.

  The page itself is unchanged and still opens in its own window — a sub-navigation
  entry rendered as a plain link would have replaced the console in the same tab,
  which is the behaviour the drawer's `separate` flag exists to avoid.

- **The catalogue screen wears the console's buttons.** It had a button vocabulary of
  its own — `primary` on the eight actions that commit something, `linkish` on the five
  that remove a row, nothing at all on five more — and the stylesheet declares none of
  the three. All eighteen rendered as the browser's default button, grey and square and
  a different size, on a page where every other screen draws the accent-filled one. It
  only reads as wrong beside the Modeler, and the two are never on screen together,
  which is why nobody reported it.

- **The state store is configured for the size it has grown to, not for Pebble's
  defaults.** It was opened with only a merger set, which left an 8 MB block cache, a
  4 MB write buffer that stops writes at two unflushed, and a single compaction
  goroutine — sensible for an embedded store of a few thousand keys, and the reason a
  store holding millions sends scans to disk and turns a compaction backlog into a write
  stall, which stalls the run loop that issued it.

  `state.Open` now takes options. Compaction concurrency is raised for every store, since
  it costs CPU and an idle store starts none; the block cache (`--state-cache-mb`,
  default 64) and write buffer (`--state-memtable-mb`, default 16) are set by the server
  for its own long-lived store only, because a process may hold several — the Playground
  opens one per session — and resident memory would multiply. Both accept 0 to fall back
  to Pebble's default.

  The write buffer's trade-off, stated because it is real: a larger one means the store
  trails the log further after a crash, so recovery replays a longer suffix. That costs
  recovery time and never durability — the WAL's fsync is the durability point (ADR-0005)
  — and the checkpoint cadence bounds how long the suffix gets. The sizes themselves are
  reasoned rather than measured against a production store; the record carries that as an
  open question, and the flags exist so the answer can be corrected without a rebuild.
- **The approver is picked, and picked differently depending on the kind.** This was
  the last typed identifier on the catalogue screen and the one that cost the most,
  because nothing reports a wrong value: an approval whose approver matches nobody is
  created, reaches no inbox, and simply waits. The first person to notice is whoever is
  waiting for the laptop.

  A named person is now chosen from the accounts list and stored as a **username**,
  because that is what an assignee is matched against. A group is chosen from the
  directory and stored as its **id**, because candidate groups are matched against ids
  first — so a group renamed afterwards keeps its approver. Neither is a preference;
  each is what matches at the other end.

  The two kinds that resolve without an approver — no approval, and the orderer's
  superior — now show no field at all instead of one labelled "empty otherwise", and
  switching to them **clears** an approver already there. Left behind, it rode along in
  a rule with no use for it, indistinguishable to the next reader from a rule that
  meant it.
- **The accounts and groups screen shows the ids it asks you to type.** Catalogue
  sharing used to ask for `usr_…` or a group id, with a hint saying to read it from
  Console → Organization — a screen that showed names only, because the id lived in a
  markup attribute meant for a click handler. The hint pointed at a place that did not
  have the answer. The pickers removed most of the need; the rest is here, because an
  id is what every scope grant and every audit line is written in.
- **A catalogue's people are chosen from a list instead of typed as an id.** The
  catalogue screen already refused free text where it mattered — a product binds a
  process from what is deployed, because a product naming a process nobody wrote is an
  order that fails while somebody waits for a laptop. Every field about *people* was
  the exception, and each asked for an opaque id from memory.

  It was not only inconvenient. The audience field's placeholder read `kunde-a, kunde-b`
  — names — while the server compares those entries against **group ids**. Following the
  placeholder produced a catalogue that reaches nobody, and nothing said so: an
  unreachable catalogue looks exactly like one nobody has filled in yet. The sharing
  form's hint, meanwhile, said to read the id from Console → Organization — a screen that
  shows group names and not their ids.

  The audience is now a list of groups to tick, and sharing is one choice carrying both
  halves of a grant, so "one account" can no longer stand in front of a group id. The
  audience offers **groups only**, because that is what the server compares; the sharing
  list offers both. Two lists on one page showing different sets is not an
  inconsistency — it is the truth about two different questions.

  A group the directory has lost keeps its place, ticked. A list of boxes has a property
  a text field does not: not drawing a value and unticking it save the same thing, so
  without this, deleting a group would make the next save of an unrelated field quietly
  drop an audience. And where the directory cannot be read, both controls come back as
  the old id field and say why — a picker with no options and no explanation is worse
  than the input it replaced, because it looks like an answer to a question it never
  asked.
- **The product form is filled in the order a product is thought about.** It answers two
  questions to two different readers and used to interleave them: a name, a heading and a
  price are what somebody browsing the catalogue meets, while the approval, the processes
  and the target-system references are what happens after the basket. The fields
  alternated between the two four times down a single column, so answering either
  question meant reading past the other.

  Two sections now — *what the catalogue shows*, then *how an order is handled* — in two
  columns, reflowing to one at the same width as the console's other two-column layout.
  Fields that carry an explanation keep the full width; prose in a half column is a
  column of syllables. No colour is spelled out, so the form follows a theme change like
  everything else on the page.

  The administrative half also answers a question the form has no field for: **who else
  may maintain this product**. A product carries no deputy of its own — it is referenced
  by several catalogues and maintained through its home one, so a stand-in is an editor
  of that catalogue. The form names the people who already may, and says where that is
  changed, rather than leaving a considered absence to read as an oversight.
- **The portal's catalogue tab is called the catalogue.** "Katalog durchsuchen" described
  an activity where its two neighbours name a place — "Meine Aufträge", "Meine
  Leistungen". In a row of three, one verb phrase among two nouns reads as a different
  kind of control, and the tab does not browse anything: it shows the catalogue.
- **Breaking: the capped list endpoints answer with `{items, total, totalExact,
  truncated, nextCursor}` instead of a bare array.** Affected:
  `GET /api/v1/tasks` (global, `?processInstance=` and `?folder=`),
  `GET /api/v1/instances`, `GET /api/v1/instances/search`, `GET /api/v1/incidents`,
  `GET /api/v1/approvals` and `GET /api/v1/audit`. The `X-*-Truncated` and
  `X-*-Next-Cursor` response headers are gone with it, and `GET /api/v1/incidents` no
  longer wraps its rows in `{"incidents": […]}`. Any client reading these six endpoints
  has to be changed; there is no compatibility mode and no versioned alias.

  The previous record moved five wrong numbers onto the thing that owns them. It did not
  take the wrong number out of reach: on a bare array, `response.length` exists, is a
  number, and is the size of the page rather than of the population — and what the
  response knew about itself lived in headers, which the cheap call drops. That is not a
  theory about how the five defects happened; the MCP server held two builders and a
  client method whose only job was to fold an array and its truncation header back into
  one object, because an agent cannot use a list that will not say whether it is
  complete.

  On the envelope, `response.length` is `undefined` and `response.map` throws. Both are
  loud where a short count is silent. `total` says how many there are and `totalExact`
  says whether that is a count or a floor, so a caller is never left to assume the
  flattering one: the instances listing is exact where a maintained counter answers the
  query (one definition's live or finished half, the engine's live half) and a floor
  where none does (both halves of the whole engine, or a filter to one element).
  `items` is never `null`, so an empty listing does not need a guard.

  The uncapped listings — `/api/v1/processes`, `/api/v1/users`, and the per-instance
  sub-resources such as `…/instances/{key}/jobs` — still answer with arrays, because
  their length *is* their population. A fourth guard in
  `api/pagecount_internal_test.go` now asks each capped listing over HTTP and refuses a
  body that is an array or that cannot say whether the cap bit, so the two sets cannot
  quietly drift; a fifth reads the published Postman collection, which nothing else here
  runs and which people copy from. The Console's audit log, which had no browser test at
  all, gets one — a windowed log now says how many changes there are rather than
  rendering the window as the whole history.

  Tests in `worker/` and `conformance/` read these listings too, and were converted with
  everything else. The conformance gallery page is generated from a template in
  `conformance/gallery_test.go`; run `go test ./conformance -update` after touching it.
  The Postman collection and its README walkthrough were updated as well — that `curl`
  line is meant to be copied, and it was teaching `json.load(…)[0]["key"]`. The Golden
  Path now asserts the envelope rather than only the status code.
  (ADR-0378)

- **The info panel is reachable from every column of the catalogue, not only from
  services.** This was not a missing feature but an inconsistency inside one page.
  The panel already worked for a bundle: picking one out of the search opens it, and
  the "my services" view has carried the button on all four levels since it was
  built. So a maintainer could write a price onto a bundle, see it under what they
  hold, find it through the search — and not reach it from the column the bundle
  lives in.

  The bundle and offering columns now carry the same round **i** the service column
  has. The panel itself needed no change, and a test says why: it reads what any
  product carries — id, texts, price, approval, whether it repeats — and nothing in
  it asks which level was clicked. A panel that branched on the level would be a
  second thing to keep true, and the first place it would go wrong is the level
  nobody clicks.

- **The coverage floor runs as its own CI job, so a healthy run stops being cancelled
  for being slow.** The main check job carried two full passes over the test suite in
  sequence: the race detector, and then the statement floor, which is the same suite
  again with different instrumentation. On `main` those measured 24m30s and 3m59s —
  28m29s of a 30-minute cap that exists to catch a hang, not to be a deadline.

  A ceiling that close to the real figure is not a ceiling. It is a coin toss decided
  by runner variance, and it started coming up tails: run 2160 on `main` was cancelled
  with both test runs green, having been cut mid-way through the trailing benchmark
  smoke. Nothing was wrong with the commit, and nothing in the log said so — a
  cancelled job reads like a failure and is not one.

  The floor is now a job beside the race detector rather than behind it. The two share
  nothing but the checkout, so each finishes well inside its own cap and neither can
  cancel the other by being slow; they also overlap instead of queueing, which is the
  smaller benefit and the one worth naming as smaller. The main job is renamed to
  `build · vet · fmt · race` accordingly, and a second check, `cover · statement
  floor`, appears beside it. Nothing here requires either by name — `main` carries no
  branch protection — so the rename costs nothing; a fork that has added required
  checks is the one place it has to be told the two new names.

  Nothing is skipped, relaxed or reordered: every test still runs, the floor is still
  94% checked against the same script, and `make check` on a contributor's machine is
  unchanged — one laptop has one set of cores, so splitting there would buy nothing.

  Moving it also exposed a latent defect in the floor's own script, which the split
  then had to fix: `check-coverage.sh` ran `go test` with no `-timeout`, so it used
  Go's ten-minute default per package. `AGENTS.md` says in as many words that the
  flag is not optional, because the `api` package runs for minutes on its own — and
  the first run on a cold runner proved it, ending in `FAIL api 600.194s`, the
  default to the millisecond. It carries `-timeout=25m` now — not the race
  command's figure, because this pass is the same tests without the detector and `api`
  under instrumentation measured 198s and 202s, with the third reading (600s) being the
  default cutting it short rather than its duration. The job's cap is 40 so that limit
  is the one that fires: Go names the package and prints a goroutine dump, a cap
  cancels the job with no line saying why.

- **The feed generator is Go, so the Go checks stop needing Node.** The Console's
  "What's New" feed is generated from `CHANGELOG.md` and committed, because ADR-0012
  keeps the web UI buildless. CI regenerates it to check the commit is current — and
  because the generator was a Node script, **four Go jobs installed a JavaScript
  toolchain for that one step**: the main `build · vet · fmt · race · cover` job, the
  docs job, the ADR-numbering workflow and the feed-sync workflow. ADR-0012's own
  driver says a front-end toolchain must not become a prerequisite for building or
  testing Atlas in CI; the feed generator was exactly that, in the job that decides
  whether a change is good.

  It is now `go run ./scripts/whats-new`, with the rules in a package beside it so the
  guards call them directly instead of starting a process and reading what it printed.
  Node remains in the two places where it is the technology rather than an accident:
  the browser end-to-end suite and the screenshot capture.

  **Byte-for-byte the same output, verified rather than assumed.** Two Go defaults
  point the wrong way — its encoder escapes `<`, `>` and `&`, and it writes struct
  fields in declaration order where `JSON.stringify` writes keys in insertion order —
  so both were turned around and the field order was made the wire contract. Both
  implementations were then run over the same tree with the entry cap lifted: all
  **418** entries, every bullet in a 9,310-line changelog, came out identical.

  **One rule is new, and it was earned.** The original ignored keys it did not know,
  and an override carried `route` at the top level instead of inside `try` — so that
  entry's "Try it" link did nothing and nothing anywhere said so. Unknown keys are now
  refused, for the reason the orphan check already exists one level up: a key that
  does nothing is indistinguishable from a key nobody wrote. The one file that had one
  is corrected and gains the link it was always meant to have.
- **The portal's corner names whoever the order is for, and the help moved to the end
  of the row.** The corner said **"mich selbst"** to everybody. That was true, and it
  was true of every reader alike, so it identified nobody — and on a screen where the
  next click can place an order in somebody else's name, the one thing the corner is
  there for is to say whose name that is.

  It now names the chosen recipient, or the person reading where none is chosen, and
  falls back to "mich selbst" only where neither is known — with enforcement off there
  is nobody to be, and saying so is the honest answer. The name is the account's
  display name, its username where it has none: the other half of the same label is a
  recipient's display name, and two kinds of thing in one place would read as two
  different questions being answered.

  The round **?** moved from beside the first destination to the far end of the row,
  past the person. Among the destinations it was a round button the same height as its
  neighbours in a row where everything else navigates the catalogue, so it read as a
  fourth place to go. At the end it reads as what it is: part of the corner that is
  about the reader rather than about what they are reading.

### Fixed

- **The rule that fired is green, like the conditions that held.** The rule matrix
  marked a satisfied condition green and the rule that carried the result blue, which
  made the row a reader is looking for a third thing to learn on a grid already saying
  two. Green now means one thing — the case satisfied this — at two strengths, and the
  row that won carries its output in bold so a rule whose conditions all held but which
  the hit policy passed over still reads apart from it. The Modeler's Test panel, the
  Operations hover and the decision graph share one renderer, so all three changed
  together.
- **A decision's result no longer sits a panel-width away from what it answers.** The
  output name and its value were laid out in a three-column grid with the value
  right-aligned — fine in the narrow table it was written for, and wrong in the replay
  panel, where the answer ended up against the far edge of a full-width card with empty
  space between it and the name it belongs to. Name, answer and the rule that produced
  it now read as one line, left to right, with the answer carrying the emphasis rather
  than its label. The Operations decision table and the live view's decision panel share
  the markup, so all three are fixed together.

- **The decision-service tool in the editor's palette was a button that never worked.**
  DMN's published interface over part of a decision graph could be carried, edited and
  round-tripped by the editor, but not drawn: the palette offered the tool and the
  canvas refused every drop, because the modeling rule that decides what may be created
  did not list the type. From an author's side it read as a broken button, and the
  workaround was to write the element into the XML by hand or bring the model from
  another tool.

  The rule now lists it, the tool has its own icon instead of borrowing the decision's,
  and a service created this way comes with the divider line that separates what it
  returns from what it works out internally. The vendored modeler is rebuilt from the
  fork that carries all of it. What Atlas ships is held to it by a test of its own: the
  palette offers the tool, the rule allows the drop, and the result survives to the
  document — asking the rule the interactive path asks, which is the thing that was
  false.

- **The test panel blamed the model for a silence that belongs to the engine.** A
  decision service is evaluated without a rule matrix — the engine reports none for one
  — so testing a service handed the panel an answer with no trace at all. The panel
  said "this decision has no table logic, so there are no rules to trace", which is
  false twice over: the decisions behind the interface are usually tables, and nothing
  about the model is the reason. It now says that a service reports no rule matrix and
  points at the thing that does: test a decision inside it.

  The two silences are kept apart properly rather than papered over. A trace that
  exists and holds no table is a statement about the model — a literal expression, a
  boxed context — and still reads as one. A missing trace is a statement about the run,
  and reads as one. Measured on the way: a decision whose own logic is a boxed context
  but which requires a decision table still traces that table, so the older message was
  right about every case it used to see.

- **The portal asks you to sign in instead of showing you an error.** On an instance
  started with `--auth`, opening the service portal without a session produced an
  error line with an HTTP status in it, no catalogue, nothing saying a sign-in was
  needed and nowhere to give one. The way in was to know that `/index.html` is a
  different page, that it has a login, and that coming back afterwards would work —
  knowledge about Atlas' internals, held by exactly the readers this page is not for.

  Every route the portal reads needs a session, and the server answers an anonymous
  caller 401 before any of them runs. That is right, and it is unchanged. What the
  page did with those refusals was not: the catalogue read treated its 401 as the
  ordinary "you are the audience for nothing" answer, so an authentication problem
  was reported as **"Ihnen ist kein Katalog zugeordnet"**, and the orders read threw
  out of the load entirely.

  The portal now asks who is reading before it reads anything else, and a refusal
  draws **a sign-in** rather than a failure. It is the portal's own screen and not a
  detour through the Console: it returns to the page the visitor asked for rather
  than to a shell they hold no role for, and the language switch sits above it, so a
  German-speaking customer is not met by an English-only form. It carries the
  instance's mark, because a page that asks for a password while saying nothing
  about who is asking is the shape of a phishing page.

  Three things it gets right that a login form usually does not. **Only a refusal
  counts**: an instance that cannot answer at all loads the portal as before, rather
  than showing every customer a form that cannot possibly work. **The throttle is
  named as itself** — after five wrong guesses the server refuses the attempt for a
  quarter of an hour without looking at the password, and told it was their password
  somebody spends that quarter of an hour retyping one that is already correct.
  **A session that runs out** while the page stands open returns to the sign-in
  saying so, instead of turning into the same error one step later.

  Where a login is federated, the provider is offered above the password form — an
  installation that has one has no password to type — and the callback now **lands
  where the login started** instead of always on the Console. Which page that may be
  is an allowlist of the two Atlas serves before anybody is signed in: the value
  travels through the browser, and anything that could express an arbitrary
  destination would be an open redirect carrying a login's authority.

- **An agent's save no longer clears a product's group.** `atlas_save_catalog_product`
  is a full replace and forwards exactly the fields its schema declares. `productGroup`
  was added to the record and to the Console and missed there, so the loop the tool's
  own description prescribes — read the product with `atlas_list_catalog_products`,
  change one field, send the whole record back — dropped it. The portal's Produktgruppe
  column emptied itself for every product an agent had touched, and nothing anywhere
  said so.

  The field is declared now, and the schema is held against the record by reflection
  rather than against a list kept beside it: the list is what was already wrong. A
  field added to a product from here on is either declared or named as deliberately
  absent, and neither can happen quietly.

- **What you already hold is filed under the heading you ordered it under.** "Meine
  Leistungen" drew the same Kategorie and Produktgruppe columns as the catalogue and
  read them off a different set of products: the catalogue collects them from the
  products nothing contains, and this screen read them straight off each entitlement.
  Both strings belong to the offering, so a service two edges down has never had a
  heading of its own to carry — and services are most of what a person actually holds.
  The columns therefore showed "Ohne Kategorie" for things the catalogue filed under a
  real heading. Nothing looked broken; the column was simply empty for the products
  people have. Each entitlement is now resolved to the product it belongs to and the
  heading read there, which is the heading that was on screen when it was ordered.

- **"No process instance is left for this order" named a cause it could not know.** It
  said retention had removed the instance. That is one of three reasons the portal
  finds none, and the least likely of them: an order whose fulfilment never started has
  no instance to remove, and that is what somebody reads this about on the day they
  ordered — which is exactly what the defect below produced for every order on an
  installation. The message now says what it knows: none has started yet, it has
  finished, or retention removed it. The order and one position are also told apart,
  because the order's orchestration and a position's own process are two different
  absences.

- **Checking a mail worker now says which of the two checks it is running.** The check
  has two modes and they are not degrees of the same thing: one connects,
  authenticates and stops at the door, the other puts a real message in a real
  person's inbox. The server tells them apart by whether a recipient was given, which
  is the right contract for an API and was the wrong question to put to a person: it
  was asked as a browser prompt saying "leave empty to only check the connection", so
  the harmless mode had to be expressed by typing nothing into the same box that means
  "send mail to this address", and a stray character sent it.

  A browser prompt is also refusable. A sandboxed frame, or the "prevent this page from
  creating additional dialogs" box somebody ticks once, makes it return nothing without
  ever opening — which the page read as Cancel and the operator read as the check not
  happening at all, with no way to tell the two apart. The modes are now a control that
  names them, in a dialog the page draws itself, with the connection check preselected
  and the address checked for being an address before anything is sent.

  The flow moved to `workerdialog.js` beside the worker's edit and delete dialogs, for
  the reason those are there: what a check does is a decision, and it was sitting in
  the one file a test cannot open.

- **A catalogue's existing products are now picked from a list rather than typed.**
  "Offer an existing product" opened a `window.prompt` that printed every product this
  catalogue does not yet carry as a line of text — id, a dash, the name — and asked for
  the id back. Nothing in that list could be clicked, because prompt body is not a
  control: picking meant reading an id off the wall of lines and typing it exactly, and
  a typo was answered with "No product with that id" and the whole list to re-read.

  It also cut the list off. A browser truncates a prompt body past a handful of lines,
  so on a server with a few dozen products the ones that sort last were not in the list
  somebody was told to choose from — and a product created a minute earlier is exactly
  the one being looked for. This is the failure the application picker had before it
  became a dialog, and it gets the same fix: the console's pick dialog, whose list is a
  `<select>` with no length limit and nothing to count.

  The button beside it was drawn from a comparison of two counts — products on the
  server against ids this catalogue offers. An id may be offered and no longer defined,
  which the product table already reports as "offered but not defined", and one such
  entry made the counts equal while products nobody had offered were sitting there: the
  button vanished, reading as "there is nothing to add". It is drawn from the list of
  products this catalogue does not carry, which is the question it was always asking.

- **"Test expression" no longer certifies an expression that cannot work.** A BPMN deploy
  already refuses a call this build can only ever answer with null — an unknown function
  name, or a built-in called with an argument count its signature cannot take. Four other
  places compiled FEEL without asking, and each already refused an expression that failed to
  *parse*, at the point somebody wrote it. Only this one class of fault walked through a gate
  that was already standing.

  The worst of them did not merely stay silent. `POST /api/v1/feel/validate` — the route the
  Modeler's expression fields call while you type, and whose entire purpose is to say whether
  an expression is valid — answered **valid** for `is defined(x)`. Somebody who asked exactly
  the right question was told the wrong answer, which is worse than never having been asked:
  a "no" leaves you looking, a "yes" gives you a reason to stop. It now answers no, and names
  the standard way to write the same thing.

  The quietest of them was an **inbound worker's correlation key**. A key that evaluates to
  null correlates an incoming message to nothing, so events arrive, the sender gets its 2xx,
  the worker reports healthy, and no instance is ever woken — with nothing to see anywhere.
  It is now refused at registration, where a syntax error already was, because by the time an
  event arrives there is nobody left to tell. The same holds when an existing subscription's
  key is edited.

  **Playground rules** say it too, for `when` and for `then` alike: such a rule compiles,
  then selects no case or fails every one, and the verdict is reached for a reason that has
  nothing to do with the run.

  **Task-folder rules turned out not to need it**, against the expectation that opened this:
  a folder rule's expression is generated from a closed catalogue, and every value reaching
  it is an escaped string, a bounded integer or a duration held to a pattern, so no text a
  caller sends can become a call. Typing `is defined(x)` into a folder's name field looks for
  tasks called that, which is what it says. What is real there is a property of the
  catalogue, and it is now pinned in the test that already holds every advertised
  field/operator pair against the generator — where an operator added later would break the
  build rather than a person's folder.

  `POST /api/v1/feel/evaluate` is deliberately untouched: if the expression yields null, null
  is the honest answer and the one the engine really gives. The engine is untouched too — a
  DMN decision still answers null, as the specification requires.

- **A stored process definition could stop the server from starting.** The compiler
  recently learned to refuse a FEEL call to a function this build does not have — a
  call that can only ever evaluate to null, so it cannot be doing what its author
  meant. The rule is right at a deploy. It was not right at a *restart*: it ran while
  the process was still being built, which is before the point where a reload can tell
  "this model would be refused today" apart from "there is nothing here to bring back".

  So a definition deployed months ago, under a build that had no such rule, made the
  new build exit during startup. The supervisor restarted it; it exited again. Every
  other definition and every running instance sat behind the one record that would not
  load, and the only way in — the API that could replace the model — needs a server
  that is up.

  A rule the compiler gains later decides whether a model may be **deployed**, never
  whether it may be **loaded**. A deploy still refuses the call, with the same message
  naming the task and the function. A reload now compiles the expression exactly as the
  build that stored it did — the engine answers the unknown call with null, which is
  what that definition has been doing all along — and logs
  `deployment.reloaded_with_problems` naming the deployment and the expression to fix.
  The server starts.

- **The fulfilment orchestration never learned which order it was working on.** Its
  model documents `orderId` as a start variable, builds every request from it
  (`"/api/v1/orders/" + orderId + "/next"`) and correlates the message that wakes it
  on it. The wake passed it as the message's **correlation key** only — and a message
  *start* event's key is evaluated from the payload, so `=orderId` over a payload
  without it resolved to nothing: the instance recorded no key, and the variable the
  model reads was never written.

  Nothing failed, which is the part worth knowing. FEEL propagates null, so the first
  service task was activated with `path = null`: the orchestration asked its REST
  worker for nothing, was never woken by a settled line, and the order sat at
  "Wartet" with no incident for anybody to find.

  Deploying the fix does not repair an instance that is already running — the
  variable it needed was never there to write. **`POST /api/v1/orders/fulfilment/repair`**
  (operator) ends the orchestrations that name no order, or name one this server no
  longer holds, and starts one again for every open order left without one. Both
  halves together: ending alone leaves the order where it was, and starting alone
  would put a second orchestration beside a healthy one, where both would ask what may
  start and both would start it. It is idempotent, and `?dryRun=true` reports what it
  would do and changes nothing — which is what to run first.

- **An approval of a product ordered twice vanished from the approver's inbox.** What
  makes a task an approval is the order behind it: the instance names a line, and the
  order agrees that this process decides that line. Naming the line is what the
  position key changed — a process passes `positionId` beside `itemId`, because
  "phone" is two lines when somebody ordered a black one and a silver one. The reader
  was written for that and the collection was not: `positionId` was read out of a map
  that gathered every other variable, so the fallback to the product always fired.
  For an order carrying one position of a product that fallback is right, which is why
  nothing showed; for a product ordered twice it resolves to nothing — correctly,
  because the product names two lines — and the task was then not recognised as an
  approval at all, in the inbox, on the approval page, or in the escalation lookup.

- **A product's name in the basket wrapped one letter per line.** The optional column
  read "Schutzhü / lle / transpare / nt" beside a price that had all the width. Two
  decisions made it together, and each was enough on its own: the price and the level
  were built into the row's *trail* — the slot that carries its controls, and
  therefore promises never to give width back — and the name was set to
  `overflow-wrap:anywhere`, which lets a box shrink below its longest word, so there
  was no floor under it to stop at.

  A row now has three slots with one rule between them: `lead` and `trail` carry
  controls, `meta` carries text about the row, and text shrinks. The price, the level
  a position will sit at and the "found under" line of a search result moved into
  `meta`, which renders under the name and wraps; the name itself keeps its longest
  word as a floor. The search results carried their "found under" line in the trail
  for the same reason and moved with it.

- **An expression calling a function that does not exist no longer deploys clean and answers
  null.** The FEEL engine compiles a call to a name it does not know into a constant null,
  deliberately: DMN requires a decision to stay executable, and Atlas evaluates decisions
  through that same engine. For a BPMN model it produced a defect with no visible surface
  anywhere. `= is defined(kunde.geburtsdatum)` deployed without a word, evaluated to null —
  without even reading `kunde`, so the null carried no trace of where it came from — and a
  gateway condition on that null took its default flow. Three steps, nothing said, and a
  customer set INACTIV who should have been ACTIVE.

  `is defined` is a Camunda extension and one of the first things somebody arriving from
  there writes, but the dialect is not what made it a defect: a misspelling produced the
  identical silence, and more often. Nor was the silence consistent — `get or else` and
  `last day of month` *did* fail, because `else` and `of` are FEEL keywords, so whether you
  were told depended on whether the missing name happened to collide with one.

  A BPMN deploy now refuses a call it can only ever answer with null, and says how to write
  it instead: *`is defined` is not a FEEL function — write `x != null`*, *`put` is another
  engine's name for `context put`, which this build has*. The same refusal covers a built-in
  called with an argument count its signature cannot take, which is the other half of the
  same silence — `date()` with no argument binds to null exactly as an unknown name does.

  The engine is untouched: a DMN decision evaluated through it still answers null, as the
  specification requires and the conformance suite pins. The refusal belongs to the deploy,
  which is the one moment where the model is not running and its author is looking at it.

  It errs quiet. A callee that could hold a function — a parameter, an iterator, a context
  key, a declared variable — is left alone, because a false refusal blocks a model that works
  while a missed one only leaves the old behaviour in place.

- **The handbook's "compute a value" recipe taught an expression that always answered null.**
  `= round(gross / 1.19, 2)` — except FEEL has no `round`. It has `decimal`, `round up`,
  `round down`, `round half up` and `round half down`, and a call to a name none of them
  matches evaluates to null. The recipe deployed, its ▶ button worked, and `net` came out
  empty for every reader who pressed it. Now `decimal(gross / 1.19, 2)`, which is what the
  recipe meant.

  It was found by the refusal above rather than by a reader, on the first run of the test
  suite after that check existed — which is the argument for the check, made by the
  repository's own documentation.

- **A decision whose name is not a FEEL identifier is deployable again.** Per DMN a
  decision has two names: the label on the diagram (`name`) and the FEEL identifier its
  result is bound to (`<variable name>`), and they need not be the same string. The DMN
  engine Atlas pinned bound a required decision under its *label*, so a model valid per
  the specification — `Decision A` declaring `<variable name="alpha"/>`, `Decision B`
  reading `alpha * 10` — was refused at deploy time with `unknown variable "alpha"`: a
  message naming the symptom and not the cause. The only way through was to name every
  decision in a chain like a FEEL identifier, which rules out `Kunden-Risiko` and
  `Decision A` alike. A model authored in the temis Modeler, in Camunda or by hand was
  rejected on arrival, and trying it before deploying reproduced the same refusal, so
  nothing distinguished an Atlas limitation from a modelling error.

  The engine now binds by the identifier, as DMN says, and **Atlas accepts both names
  everywhere a decision is addressed** — the registry's version pointers, the model a
  business rule task resolves to, the try-a-decision membership check and the deploy
  gate's coverage report. A task deployed under the label keeps evaluating; one naming
  the identifier resolves too. A decision is still *published* under exactly one name,
  its label, so a deployment record, a version count and a listing read as before.
  Inputs get the same treatment: an input whose label differs from the identifier it
  binds is accepted under either, so a task that recorded its input keys before the
  distinction existed still finds them. Measured across every model on the reference
  installation: none is affected, and the DRD that prompted this now deploys and
  evaluates.

- **A decision that returns a number wrote its result as a string.** The decision engine
  hands a FEEL number back as its exact decimal string — deliberately, so an amount is
  not rounded on the way out — and Atlas stored it as what it saw: text. A sequence-flow
  condition comparing that variable to a number is then a FEEL type mismatch, which
  evaluates to `null`, which is not `true`, so the token took the **default flow** with
  no incident, no diagnostic and no trace entry. The process simply routed the wrong way,
  and an instance's variables read `{"alter": 19, "praemie": "1250"}` — the two from a
  form numbers, the one from a decision a string.

  **The model's own type declarations now decide**, and nothing else: a result the model
  declares `number` is stored as a number, exactly, without reparsing or rounding. A
  string that merely looks like a decimal is left alone, so a policy number, an article
  code and `"0800"` keep their leading zeros and their type. It holds for a decision
  table's output columns, a boxed context's entries and every element of a `COLLECT`
  list. An output the model leaves untyped stays a string — the honest answer, since
  guessing would trade a visible wrong type for an invisible wrong value.

- **The icons at the end of a catalogue row broke onto a second line.** Each of them
  already refused to shrink, but they sat in a plain `<span>` carrying no rule at
  all: a flex item that may shrink, holding inline boxes that wrap inside it. The
  cascade is four columns across, so in a narrow one the star, the ± and the "i"
  wrapped and a single row read as two.

  The row's cell now wraps whatever trails it, in one place rather than at each of
  the seven callers — a rule applied per caller is a rule the next caller forgets.
  The wrapper is a flex row that does not shrink, which is the whole fix: a flex row
  does not wrap by default, and the icons cannot give up width.

- **A catalogue could offer a product nobody had created, and only said so much
  later.** The write that introduced the dangling id answered 200; the refusal
  appeared at the next publish, as `unknown item <id>`, against a catalogue the
  person had stopped thinking about. Two symptoms of one fact, with nothing on
  screen connecting them: publishing refused an id that looked like a product, and
  the product behind that id read as `revision: 0` — a stored product always carries
  at least revision 1, because the save that creates one sets it, so zero means the
  record was never written at all.

  The catalogue's own screens already assumed the rule, rendering such a row as
  "offered but not defined — publishing will refuse this"; a rule a screen explains
  and a route does not enforce holds until somebody uses the API. Offering a product
  that does not exist is now refused where it is written, naming the id. Only what a
  write *adds* is checked, so a catalogue already carrying bad ids stays repairable —
  otherwise the only way out of the mistake would be the mistake. The check is
  existence and not visibility: a product is referenced by several catalogues and
  edited through exactly one.

- **"Meine Aufträge" showed principal ids where it meant people.** The Person column
  printed `usr_703f410b40336d21476152fb`. Nothing was wrong with the record — an
  order names people by principal id and by nothing else, because a name copied into
  a record outlives the reason for holding it (ADR-0314) — but that decision leaves
  the other half to the screen: a name is resolved when the screen is rendered, and
  the table was not resolving. It reads the directory once per load and names the
  person; where nothing knows the id, the id is still shown, because an empty cell
  reads as a broken column rather than as an unresolved one. The column's filter
  searches both, so a pasted id still finds its row.

- **One position in the portal carried two different level names.** The catalogue
  screen and the basket both label a position Bundle, Marktleistung or Service, and
  Atlas has no such typing — the level is derived from the containment graph. It was
  derived twice, differently: the cascade used depth, the basket used whether a part
  came with the whole. The direct part of a package was a Marktleistung in one half
  of the screen and a Service in the other. Underneath that, every product nothing
  contained was called a bundle, so a single product with nothing inside it was
  announced as something made of other things.

  One rule now decides the level and all three views read it — the catalogue, the
  basket and what a person already holds, which was a third derivation again: a root with parts is a
  bundle, a root without them is an offering, a direct part of a root is an
  offering, and anything deeper is a service. Whether a position can be taken out is
  answered per row by the control it carries, which is the different question the
  basket had been answering with the level. Display only — no stored release, API or
  order in flight is affected, because the level has never been written down
  (ADR-0383).

- **An order never said which shape of a product was ordered.** A variant is one
  orderable shape — a colour, a licence tier — and the catalogue has carried them from
  the start. Nothing ever wrote one down: the order line had the field, the fulfilment
  process passed `position.variantId` to provisioning, and it arrived empty for every
  order ever placed, because the basket never asked and `POST /api/v1/orders` had
  nowhere to put the answer. Provisioning was told to hand over a phone and not which
  one.

  The basket now asks, for every line that comes in more than one shape, including the
  ones that arrived as integral parts of a bundle and were never named by the orderer.
  Nothing is pre-selected: variants are unordered on purpose, so there is no first one
  to fall back on. Ordering waits until every open choice is made, and the server
  refuses an order that leaves one open, names a shape the product does not come in,
  or names one for a product that comes in a single shape — a rule the page keeps and
  the server does not is not a rule. `POST /api/v1/orders` takes a new optional
  **`variants`** object, keyed by item id and holding one shape per position; a body
  without it is unchanged for every product that has no variants.

- **The portal showed what a product comes with and not what it is offered with.**
  A release carries two kinds of containment: a composition arrives with the whole and
  cannot be dropped, an aggregation is an offer standing beside it. The cascade drew
  both. The info panel named neither, and the basket pulled in compositions and
  stopped — so the offers hanging under a bundle were reachable only from the column
  the bundle happens to open, and only until the reader navigated away.

  The panel now lists both groups, kept apart, and the basket carries an Optional
  column holding every offer the chosen products make: unticked, each with its price
  and its "i", and ticked through the same control the cascade uses. Nothing about the
  stored release or the order contract moves — a ticked option is an ordinary id in
  the basket, so the placed order carries the bundle, its integral parts and the
  options actually chosen, each as its own line with its own provisioning process.

- **A catalogue could only be published while every other catalogue was empty.**
  Publishing validates one catalogue, and it is handed every catalogue — because a
  rank has to be unique across the set, and a tie can only be seen against somebody
  else. It was handed only **that one catalogue's** products, though, and it then
  resolved *every* catalogue's product references against that single list. Each of
  the others came back "unknown item", and the publish was refused.

  The two messages are why it read as a contradiction rather than as a defect:
  publishing *Informatik* blamed the other catalogue's products, publishing the other
  blamed *Informatik*'s, and neither message named the catalogue anybody had asked to
  publish. There was no order in which both could succeed, and no way to read the pair
  as anything but the product disagreeing with itself.

  A publish now says which catalogue it is for. The rank check still looks at the whole
  set; the product references are resolved for the subject alone. A publish that does
  **not** say — which is unambiguous for one catalogue and for no other number — is
  refused rather than guessed at, because the guess is precisely the defect above.

  Nothing about the workaround is needed any more, and nothing published under it has
  to be redone: the refusal happened before anything was written.

- **The server froze for seconds at a time, on a cadence, once its store grew.** Every
  list in the Console stopped, everything the browser already had stayed responsive, and
  after some seconds the whole backlog arrived at once. Nothing in the code had changed;
  the store had — to ~50.000 active instances carrying ~200.000 tokens, over 2.000.000
  finished instances of history behind them.

  Three pieces of work grew with that store, and all three ran on the run loop, which is
  the single writer *and* the gate every request passes through — an off-loop reader
  still takes a loop turn to open its view, so holding the writer holds everything.

  The checkpoint was the cadence. `checkpoint.Publish` checksums the snapshot it takes,
  which means reading every SST file in the store, and it did that inside the `do()` turn
  that took the snapshot. Measured at 735 MB/s with a warm page cache — 2,9 s for a 2 GB
  store, linear from there — on the default five-minute interval. WAL compaction did the
  same read again, through `checkpoint.Verify`, in a turn whose own comment called it
  "bounded work — a few unlinks and one directory fsync". And `readStats` counted active
  instances and live tokens by walking their column families: 48,9 ms at that population,
  paid by `GET /api/v1/stats` — which the incident badge polls every five seconds for one
  field — and by seven write paths that report the counts back in their response,
  including `POST /api/v1/messages`, so a message-driven model paid it per message.

  Only the snapshot needs the writer stopped; once taken it is hard links to immutable
  files under a name nothing else looks at. So `Publish` splits into `Stage` and
  `Staged.Commit`, `CompactLog` into `CompactionCut` and `CompactLogAt`, and the counts
  come from the maintained ADR-0080 counters — 1,2 ms, and rising by half where the scan
  rises elevenfold. Both single calls remain for tests and synchronous embedding. The
  incident count stays a scan on purpose: an incident leaves state two ways, so a
  maintained number would drift where a scan cannot.

  What that costs, stated because it is real: a scan cannot be wrong, a counter can. If
  any write ever put one of those records without its counter beside it, the number would
  drift silently. `applyToState` is the only place either is written and it puts the two
  in one `firstErr`, and `TestStatsReadFromCountersAgreeWithTheScan` holds the readings
  against each other — but the guarantee is now maintenance rather than construction.

  Two tests hold the line rather than a convention —
  `TestCheckpointCommitRunsWithTheRunLoopFree` and
  `TestCompactionVerificationRunsWithTheRunLoopFree` ask the loop whether it is free at
  the moment each read begins — and two benchmarks keep the numbers above honest
  (`BenchmarkChecksumDirBySize`, `BenchmarkStatsAtProductionSize`).
  See `docs/adr/0382-whole-store-reads-leave-the-writer.md`.

- **A Google Sheets task whose spreadsheet resolved to nothing now says so, instead of
  asking Google about no spreadsheet at all.** A model addresses a spreadsheet by a value
  it may author as FEEL — `spreadsheet="=tabelle"` is the ordinary shape, with the id or
  the pasted browser URL arriving as a start variable. An instance started without that
  variable resolves it to FEEL null, and a null value resolves to the empty string, as it
  does for every Worker Type. In an optional value that is exactly right and means "leave
  it out".

  In a required one it meant the worker called `/v4/spreadsheets//values/A1:C1` and
  reported what Google answers for that: **HTTP 404, "Requested entity was not found"** —
  the message for a file somebody deleted. It sent its operator to look at a spreadsheet
  that was exactly where they had left it, and nothing had been refused at deploy,
  because the attribute *was* there; what was missing was the instance's variable.

  The Worker Instance now refuses such a job before the call and names the operation and
  the attribute that came up empty. The check reads the same operation table the compiler
  and the properties panel do, so it covers every value an operation needs — the
  spreadsheet, the sheet, the range, the title, the rows to write — and an operation added
  to that table cannot be forgotten in it. The job's fate is unchanged (pending, retried,
  then an incident); what changed is that the incident names the fix.

- **A task folder edited twice in quick succession no longer keeps filtering by its
  previous rule.** The sidebar compiles each folder's rule once and remembers the
  result; the memo was keyed by the folder's `updatedAt`, a clock in milliseconds. Two
  saves inside one millisecond therefore shared a key, and the second was served the
  first's matcher — the folder showed the old filter until something else evicted it.

  Reachable by any caller that edits faster than a person clicks: a script, an API
  client, an agent over MCP. Measured in the repository's own test for this
  (`TestMatcherIsRecompiledAfterAnEdit`), which had been passing on the luck of the
  clock ticking between two writes: **98 failures in 400 runs**.

  The memo is now keyed by the rule itself — specifically the FEEL it compiles to,
  which is the whole of what the compiler reads. That is correct by construction
  rather than a finer clock: the same rule always yields the same matcher and a
  different one never reuses it, whatever a clock does, including a record restored
  from a backup carrying its original timestamp.


- **A catalogue could say a part was integral and optional at the same time.** The
  release keeps *what a product is made of* apart from *what is offered alongside it*,
  because they mean opposite things to a basket: an inclusion is ordered as a
  consequence of ordering the whole, and an option is an offer. Nothing stopped one
  pair of products from carrying both kinds of link, and such a pair landed in both
  lists — the same part ordered without asking and offered as a choice, on one screen.

  It needed no mistake to produce. Importing an ArchiMate model merges links by
  **adding** them, deliberately, because an import is not a synchronisation. So
  redrawing an integral part as an optional one in the model and importing again left
  the catalogue holding both, with the old link the one nobody remembers. Publishing
  now refuses it and names the pair rather than picking one: there is no honest rule
  for which of the two an author meant.
- **The console showed three borders that were never drawn.** A style rule reading a
  colour name that nothing defines is not a rule with a default — the whole declaration
  is invalid and the browser discards it. Three of them read a name defined only in an
  unrelated demo page, so the token legend lost its divider and the profile-picture
  field lost both of its borders. Missing hairlines read as a design that never had
  any, which is why nobody reported it. A test now refuses the whole class.
- **The console said a group membership takes effect at the next sign-in.** It has
  applied from the member's next request since that was made live. A stale claim about
  a delay is worse than no claim: an administrator waits for it, and tells a colleague
  to sign out and back in for nothing.

- **Two maintainers adding a product to the same catalogue, and one of them erased
  the other.** A catalogue's patch is partial, so it cannot clear a field nobody
  mentioned. What it could still lose is a list: `items`, `edges` and `members` are
  each replaced whole when sent, and every surface that sends one computes it out of
  the copy it was rendered from. Adding one product posts every product plus that
  one.

  So two people adding a product a second apart, and the second write is the first
  one's disappearance — no error, no trace, and the person who lost the change is the
  one who did nothing wrong. It is the same shape as the product form's defect one
  level up, and it survived that fix because a partial patch looks safe.

  A catalogue now carries a `revision`, and a caller may state the one it read. The
  write is refused as a conflict unless the catalogue is still on it. Stating it stays
  optional, because a form whose every field is on the screen has no snapshot problem
  — the caller holding a snapshot is the one that should say so. The Console states it
  on exactly the six places that rebuild a list and on none of the others, and the MCP
  tool that changes a catalogue takes it too.

  **Every writer advances it**, which is the part that makes the guard worth having
  rather than worth believing: a path that changed a catalogue without advancing its
  revision would be a path whose changes a stale caller overwrites in silence, and it
  would be found by somebody losing work. The patch, the appearance and the ArchiMate
  import are each named in a test, so a seventh writer has to be added deliberately.
- **CI failed a change on a slow runner rather than on a defect, for the second time.**
  The race-detector step carries a per-package timeout because the `api` package needs
  most of it on its own. At Go's 10-minute default that step once passed at 526s and
  timed out at 600s on the next run, where the only change between them was a line in an
  unrelated test file; the limit was raised to 25 minutes. It has now timed out at 1500s
  on a run where the same package took 1254s two hours earlier — on a runner that was
  slower across the board, not only there: `engine` 82s → 219s, `conformance` 8s → 45s,
  `mcp` 19s → 48s, `state` 4s → 17s, with nothing in the change touching any of them.

  The limit is 45 minutes, and the job's own cap moves with it to 60. That pairing is the
  part worth writing down: both bound the same run, so raising the inner one alone would
  have changed nothing — the job is killed first, and the failure turns from "timed out"
  into "cancelled" with no line saying why. The inner limit has to fire first, because it
  is the one that names the package.

  Forty-five and not thirty-five because thirty-five was measured too: the widest pair on
  one tree is 1352s and 2048s, an hour apart on the same day, and 35 minutes clears the
  second of those by fifty-two seconds. That is the same coin toss one draw further out.

  Nothing is skipped or quarantined: every test still runs, and a hang still ends the job
  inside the cap. **The number buys headroom and does not fix the cause** — the `api`
  package is most of what the step measures, and a limit raised three times is a package
  that wants splitting or parallelising. That is now #1001, with the measurements, rather
  than a sentence nobody is accountable for.

  The command is written across seven files — the Makefile, the CI workflow, and the
  documents that say what "done" means, including `CONTRIBUTING.md` and the invariants
  checklist, which were a number behind. A comment asking the next person to change all
  of them reaches only whoever reads that one file, so a test now holds them to one
  number: a contributor whose local flag is the older, smaller one reproduces neither
  failure and is told their change is fine.
- **With authentication off, the portal could never find a catalogue at all.**
  Atlas's documented development and demo mode is `--auth=false`. Which catalogue
  somebody sees is resolved from the groups they carry — so with no principal there
  are no groups, `ReachedBy` answers false for every catalogue, and the mode's one
  screen said *"Ihnen ist kein Katalog zugeordnet"* to somebody there is no "you" to
  assign one to. The portal was unusable in the mode it is documented to be usable
  in, and the message misdescribed why.

  Every other gate in the product reads enforcement-off the same way — **there is
  nobody to be, not nobody who may** — and the portal now does too: with no principal
  and nobody to be, the audience question is not asked and the highest-ranked
  catalogue is the answer. Rank, because that is already what decides which of
  several catalogues a person sees, and publishing refuses a rank tie.

  **The exception is narrow and earns itself.** A catalogue with no audience reaches
  nobody, fail-closed on purpose, and that is unchanged wherever there *is* somebody:
  with enforcement on a caller with no session still reaches nothing, and a signed-in
  administrator still gets the catalogue their groups reach rather than the
  top-ranked one — being allowed to read every catalogue is not the same as being the
  audience for one. What makes it safe here is that in this mode the rule protects
  nothing: every catalogue is already readable through the administration routes by
  anybody who can reach the port.

  **An order is still refused, and the page now says so instead of discovering it.**
  An order belongs to somebody; one with no orderer has nobody to notify and nobody
  to hold responsible. So the mode is read the catalogue, do not order from it: the
  order button is replaced by the reason and the remedy, the basket control is shown
  disabled like an integral part, and the favourite mark and the recipient field are
  not offered. Whether an order is possible is read from the identity the session
  carries — the server's own rule mirrored, not inferred from the mode.

  And one 400 from a per-account list no longer takes the page down. The inventory
  and the favourites answer about an account and refuse a caller with none, which is
  right of them; the portal now treats a missing per-account list as a list missing
  rather than as a catalogue missing.

- **Editing a product in the Console silently cleared five of its fields.** Saving a
  product replaces it — the record that arrives is the record that is stored — and the
  catalogue's product form does not render every field a product has. It has no control
  for variants, for the orderable window, for the search keywords, for the groups
  eligible to receive the product, or for the ceiling on how long the right may last.
  It built its body out of the controls it does have, so correcting a price cleared all
  five, and moved the creation date to today.

  Nothing said so, which is what made it worth finding rather than merely fixing: the
  save succeeded, the page reloaded, and everything the form shows looked right. The
  fields it dropped are exactly the ones it never displays, so the damage was invisible
  on the screen that caused it and turned up later — in a portal that stopped offering
  a product to the group that was eligible for it, or a search that stopped finding one
  by the word everybody uses.

  The form now starts from the stored product and lays its own fields over it. Texts
  are merged the same way and for the same reason one level down: a product is shared
  between catalogues, the form renders one box per language *this* catalogue declares,
  and a text in a language it does not declare belongs to a catalogue that does.
  Emptying a box that is rendered still clears that text.

  It also carries the product's `revision` now, so a colleague's edit in between is
  refused rather than overwritten. A person has no revision to state, so the refusal is
  translated where it is shown: nothing was saved, the page shows the other version,
  open the product again and reapply the change.
- **A JavaScript script task could not start under the strict sandbox.** Node's bundled
  OpenSSL opens `/etc/ssl/openssl.cnf` before it will execute a line, and the strict
  profile's allowlist named `/etc/ssl/certs` but not that file — so node exited 13 with
  an OpenSSL configuration error on any host that keeps its interpreter in one of the
  sandbox's runtime roots, which is where an ordinary install puts it. The file is now
  allowed for reading; `/etc/ssl` as a whole deliberately is not, because that directory
  also holds `/etc/ssl/private`.

  It went unnoticed because the proof that starts every installed interpreter under the
  profile **skips** one it finds outside those roots — the honest answer on a host whose
  toolchain unpacks runtimes elsewhere, and exactly what CI was while it installed node
  into a toolchain cache. The JavaScript half of that proof had therefore never run. It
  runs now, and a second guard reads the allowlist directly, so the rule no longer
  depends on where a host happens to keep its binaries.
- **A knowledge model's expression opened unstyled.** dmn-js does not show a business
  knowledge model in the literal-expression view a decision's expression opens in. A
  knowledge model is a FEEL *function* — it has an expression language, formal parameters
  and a body, none of which a decision's literal expression has — so dmn-js opens it in a
  different component, the boxed-expression view, with its own container class and its own
  two stylesheets. The decision editor loaded the other views' stylesheets and neither of
  those.

  The failure was silent in the way that is hardest to catch. The view rendered: every
  element was in the DOM, editing worked, saving worked, nothing errored, nothing 404'd.
  It was simply raw — the `F` kind marker and the `()` parameter list as bare text against
  the page edge, no boxes, no borders, and the edit buttons that are meant to be clipped
  away until their section is hovered sitting permanently on top of the expression.
  Neither the Go suite nor the browser suite could see it, because the only thing wrong
  was what it looked like.

  Both stylesheets are loaded now, and the list is checked against the vendored bundle
  rather than maintained by hand: the bundle names the view containers it can create, and
  a test fails when a stylesheet that styles one of them is not loaded — so the next view
  the pinned fork adds cannot arrive unstyled. The two expression views also gained the
  gutter and the surface that let them read as one box on the Modeler's grey canvas, and
  the hint under the canvas now describes the view that is open rather than describing the
  decision table under all four of them.

- **With authentication off, a catalogue's appearance could not be set at all.** The
  predicate every gate in the catalogue package asks is `!authEnabled || (p != nil &&
  p.HasRole(admin))` — true for everybody when nobody is signed in, which is the rule
  stated beside it: *enforcement off means there is nobody to be, not nobody who may*.
  Three gates wrote `p != nil && s.admin(p)` in front of it, and so turned "everybody"
  back into "nobody".

  So with `--auth=false` — the documented development and demo mode — a catalogue's
  colour, typeface and brand mark could not be set or removed. Every attempt was **403**,
  telling somebody the appearance is an administrator's while, as far as the server was
  concerned, they were nobody. The nil check was never load-bearing: with enforcement on,
  the predicate already answers false for a nil principal. It only ever subtracted.

  It survived because every test in that package builds its service with the
  enforcement-**on** shape of the predicate, so nothing modelled the mode in which it
  bites. There is now a service built the way the server builds one with `--auth=false`,
  and the other half beside it: an unauthenticated request is still refused while
  enforcement is on.

- **The catalogue screen said an empty audience means everybody. It means nobody.**
  `ReachedBy` returns false for a catalogue naming no group — deliberately, and held by a
  test, because the dangerous default is the one where a catalogue somebody is still
  filling is already open to all. The field said "empty means everybody" and the list
  showed an empty audience as "everybody".

  An operator therefore created a catalogue, was told it was open to everybody, and every
  visitor read "no catalogue is assigned to you" — with the one screen that could have
  explained it saying the opposite. The behaviour is right; the sentence was the defect.
  The field now says what happens, and the form says it again under the input while no
  group is named.

- **A refused publish said "not published" and withheld every reason.** Publishing is
  the moment a catalogue is proved — both graphs acyclic, every binding resolved, a text
  for every declared language, ranks unique — and the server answers **422 with every
  problem at once**, each naming the catalogue or the item it belongs to. The authoring
  screen's own opening comment says that list is what it renders, "because the problems
  are the work, and hiding them behind 'publish failed' would make the screen useless
  exactly when it matters".

  It did the opposite. The page read `err.message`, which the shared fetch wrapper fills
  from the body's `error` key — a key a 422 does not have — falling back to
  `res.statusText`, which is **the empty string over HTTP/2**, because HTTP/2 carries no
  reason phrase. So a product manager pressed Publish and got a red card reading "Not
  published. Nothing was frozen" above an empty box, with no way to learn what to fix and
  nothing on screen admitting that anything had been withheld. The one honest sentence on
  it — "Never published. Until it is, the portal shows this catalogue to nobody" — then
  read as a dead end rather than as a to-do list.

  The refusal is now rendered as what it is: every problem, with the product or catalogue
  it belongs to named. Two tests hold the two halves together — one against the real 422
  so that renaming `problems` or adding an `error` key fails loudly, one over the page so
  that reading the wrong half of the body fails.

  **And the empty message was never only this page's.** `apiRaw` backs every screen in the
  console, and any error body without an `error` key became an `Error` with no message at
  all. It falls back to the status number now, which is not a good message and is a great
  deal better than a blank box.

- **"Who is this?" failing for two different reasons was answered as though it were
  one.** Resolving a person fails because the name is nobody's — the caller's input is
  wrong — or because the user store could not be read, which is the server's fault. Both
  came back the same way, and in both directions: an order for a misspelled recipient
  answered **500**, and the approval inbox turned an unreadable user store into a **404**
  saying the person does not exist. An operator was told their colleague has no account
  when what happened is that Atlas could not look.

  `httpapi.ErrNoSuchPrincipal` now says which. An order for a name nobody holds is
  **400**, with the sentence naming the four spellings that resolve; an unreadable store
  stays **500**; the approval inbox keeps its **404** for a name nobody holds and stops
  giving it for a store it could not read. A sentinel and not a match on the error text,
  because a status code decided by string comparison changes the day somebody improves a
  message.

- **An order could be placed in anybody's name.** `HandlePlace` took the recipient straight
  out of the request body and asked nothing about it, so any account that could reach a
  catalogue could put an order — and an approval in that person's manager's inbox, a line in
  their record, and eventually a provisioning run — in a colleague's name.

  It stayed harmless only because nothing exercised it: the portal never sent a recipient, and
  no shipped model places an order at all — every `recipient` in a BPMN file *reads* the one
  the order already carries and passes it down. The mockups end that, by making ordering for
  somebody else a first-class screen. A latent hole with no caller becomes an open path with a
  button.

  Naming somebody else as recipient now needs the **operator** role. Naming yourself is
  unchanged, so a self-service portal stays self-service.

  **A role and not a manager relationship, because Atlas cannot evaluate one** — and that is
  settled rather than open: the escalation path has the *caller* name the superior precisely
  because a directory lookup belongs to a modelled process and not to the engine. An engine
  that gated on a hierarchy it had to invent would decide who may act in whose name from a
  guess.

  The product-eligibility check beside it does not cover this and was never going to: it asks
  whether *this person* may have *this product*, and would wave through an order placed in a
  colleague's name for something the colleague is perfectly entitled to. What is wrong there
  is the name on the order, not the product.

### Added

- **An «enumeration»'s literals are shaded by use too, read through the lifecycles that
  borrow them.** The class diagram can say which members a deployed process names; a literal
  was left unshaded, because no process ever names one. What a process names is a *state* — a
  `<dataState>` on a write — and a literal becomes a state only where some class's lifecycle
  takes its states from that enumeration. A literal's rename is that state's rename, which is
  what makes the two the same string rather than two that happen to match.

  So the question is asked of the classes that borrow it. A literal is bright where a deployed
  process moves such a class into that state, and faint where none does — which is the reading
  people want from a state machine: the states nothing has ever reached.

  It is asked only where it can be answered. An enumeration nothing borrows from, or one whose
  borrowers no deployed process uses, is left unshaded: "no process reaches this state" and "no
  process was in a position to" are different claims, and fading a state machine nothing drives
  would report the second as the first.

- **A face can come from the directory, and it arrives the way every other directory
  fact does.** A tenant that already holds a photo for everybody should not be asked
  to collect them a second time. The constraint that shaped this is not about
  pictures: **Atlas holds no tenant credential** and must not start holding one, so
  the mirror *pulls* — a process reads Graph through the Entra worker and reports
  what it read, and nothing in the server calls Graph.

  The worker gained one operation, **`get-user-photo`**, and with it the ability to
  read bytes at all: every Graph call Atlas had returned JSON, and a photo does not.
  The change is one field on the request rather than a second method on the client,
  because what differs is a property of *the request*. The result reaches a process
  as `{contentType, data}` with the data base64 — a process variable is FEEL, and
  FEEL has no bytes — and `null` where there is no photo, so a model asks whether
  there is one instead of comparing an empty string.

  **A 404 is an answer, not a failure**, and that is the one place in this worker
  where a non-2xx is not an error. Graph answers 404 both for a person with no photo
  and for an id that is not anybody's, and its error code distinguishing them is not
  something to hang a directory run on. The trade is stated rather than hidden: a
  mistyped id reads as "no photo", where the other way round every person without
  one would fail a job — in a tenant where most have none, an incident queue nobody
  can read. It is confined to binary requests and held by a test, because the day it
  leaks into the JSON path is the day a failed directory read looks like an empty
  one. A body past the limit is **refused rather than cut short**: the magic is at
  the front, so half a JPEG passes every format check and is still broken.

  The synchronisation message carries the pictures in a field of its own — not on the
  user object, which is documented as one object from `/users/delta` and would have
  been a small lie in the file where a reader most needs to know what came from
  where. **Removal is explicit**, because the absence of an entry has to keep meaning
  "not fetched": without a way to say "there is none", a photo deleted in the tenant
  would stay on the account for ever.

  **A mirror does not overwrite a choice.** A picture somebody uploaded is left where
  it is, in both directions — the directory may replace or remove what the directory
  gave, and neither what a person picked for themselves. The run counts how often it
  stood back rather than writing a line per person; what is surprising, bytes that
  are not a picture, is a note, and it never costs the account the rest of its page.

  The account carries a **fingerprint** of its picture, and that is what keeps
  "unchanged" true. The mirror decides an account unchanged by comparing the record
  before and after; a photo that changed while the record did not would be planned as
  unchanged and written anyway, which breaks the one rule that makes the reporting
  mode worth reading — the plan says what the apply does. It also makes the write
  idempotent, so a process that fetches photos every run does not report a change on
  every account for ever.

- **A person can have a face.** Atlas showed people as strings: an approval said
  `usr_4be5b4ad`, the portal's corner drew an empty circle, and a recipient picked
  out of the directory was a name in a list of names. That is fine while somebody
  works with three colleagues, and it stops being fine first exactly where the
  mistake is expensive — ordering in somebody else's name, deciding somebody else's
  request.

  An account now carries a **picture**: `PUT /api/v1/users/{id}/avatar` takes the
  bytes, `GET` serves them to anybody signed in, `DELETE` takes them away. It is
  shown in the portal's corner beside whoever the order is for, and in the
  console's user administration, where it is also uploaded and removed.

  **Set by the account itself or by an administrator — not by an operator.** An
  operator runs what is deployed, and changing the face a colleague wears to
  everybody else is not running anything. Read by everybody signed in, which is the
  point of having one: it is read beside a name in a task list, an approval and a
  recipient picker, by colleagues rather than by administrators, and it discloses
  less than the principals directory the same caller already reads.

  **Stored beside the account record**, and two things follow without anybody
  arranging them: a snapshot that carries the accounts carries their pictures, and
  deleting an account deletes its picture — in the store rather than in a handler,
  so every deletion path does it. Ids are assigned, so a file left behind is not
  untidy but wrong: the next account handed that id would inherit a stranger's
  face.

  **PNG or JPEG, and deliberately not SVG.** A brand mark may be a vector — it is
  drawn, it is scaled, a designer delivers one — and the serve headers make a
  hostile one inert. A photograph has no such reason: it comes from a camera or
  from a directory, and both give raster bytes. Accepting a document format with
  scripting in it, in the one place where the uploader is *every account* rather
  than an administrator, would be widening the surface for nothing. So the image
  package now has a set per surface over one content check: which types a surface
  takes is a policy and the surfaces differ, while whether bytes really are the
  type they claim has one answer everywhere.

  The account records **where the picture came from** — uploaded, or from the
  directory — because nothing in a JPEG says who chose it, and that is exactly what
  somebody looking at a wrong picture needs: whether to change it here or in the
  directory. The directory half is not in this change: the photo will arrive the
  way every other directory fact arrives, read through the Entra worker by a
  process and reported here, because Atlas holds no tenant credential and must not
  start holding one for a picture.


- **The decision editor says when a knowledge model is never invoked, or invoked without
  being required.** A knowledge model is a reusable FEEL function, and DMN says the
  decision invoking one declares a knowledge requirement for it — the arrow the
  requirements graph draws. temis does not enforce that: a decision whose expression calls
  a knowledge model by name evaluates correctly with no arrow at all. Both of the
  disagreements that follow deploy, run, and are reported by nothing.

  A knowledge model nothing invokes is dead weight. The model is valid, its decisions
  deploy, the engine never complains — so there is no later moment at which anybody finds
  out, and on the canvas it looks exactly like one that is called: the only difference is
  an arrow that is not there. A decision that calls one without requiring it is worse in a
  quieter way. It runs, and draws a graph that omits the dependency — and the graph is
  what gets reviewed, and what goes into the decision's published documentation.

  The editor now says both, while the model is on screen: a strip under the canvas naming
  what is wrong and what follows from it, and a warning badge on the shape in the
  requirements graph. Clicking a finding goes to its element, from a decision's own view
  as well — back to the graph first, since pointing at a shape in a view that does not
  draw it would point at nothing. Both are warnings and never errors, because each
  describes a model that deploys and runs, and both are biased towards silence: an
  invocation is anything that reads as the knowledge model's name followed by an open
  parenthesis in any other element's expression, so an unusual way of calling one costs a
  missed warning rather than a false one. A warning an author learns to ignore is worse
  than no warning.

  The second finding carries its repair: **Draw the requirement** draws the missing edge
  from the knowledge model to the decision that calls it. It is offered only there, because
  only there is the fix determinate — which decision ought to call an uninvoked knowledge
  model is the author's to decide, and a button that guessed would be writing their model
  for them. dmn-js's own rules are asked whether the connection may be made rather than the
  element being constructed, so the button cannot force a connection the palette would
  refuse, and says why when it is refused. What it draws is left selected *and* the canvas
  is given focus, which is both ways of taking it back within reach: the connection's
  context pad has one entry, the bin, and Ctrl+Z works. The focus is the part that is not
  obvious — dmn-js binds its keyboard to the canvas SVG rather than to the document, so a
  button in the strip below the canvas has to hand focus back, or the author's first
  Ctrl+Z would go nowhere and they would reasonably conclude the edit could not be undone.
  Clicking a finding to jump to its element hands focus back for the same reason.

- **The class diagram can say which members anything actually uses.** Where a business object
  is used has been readable since **Data › Business objects** arrived — one class at a time,
  on a page of its own. The question is asked on the class diagram, with the member under the
  cursor and the decision half made, and getting the answer meant leaving the drawing, finding
  the class in a list and coming back. Most people do not take that trip, so the reading
  existed and the decision was still taken blind.

  A control beside zoom and undo shades the drawing from that same reading. A member some
  deployed process names comes forward; one none of them names recedes; a class used by no
  deployed process and by nothing in the model either is faint as a whole.

  What it will not claim is the more important half. Faint means *nothing names it*, not
  *nothing uses it*: a read takes the whole object, and what an expression then reads out of
  it is not a fact of the model — the legend says so in those words, on screen for as long as
  the shading is. A business key is never faint, because no write ever names one and it is
  what every store lookup and cross-process correlation resolves against. An «enumeration» is
  not faint for having no process use, because most of them are declared by no data object at
  all. And a name the reading has never seen — a class added since, or renamed a moment ago —
  is left exactly as it was drawn, so a rename is not a scare about a member nothing had said
  anything about.

  Off until it is asked for: every attribute is unused the moment it is typed, and a canvas
  that greys out new work is one people turn off.

- **The class canvas judges the model while it is being edited, not when it is saved.** The
  Problems panel showed the findings of the *last save*. So every edit that broke the model —
  a store left naming a class that was renamed away, an attribute typed with something that is
  gone, a lifecycle whose states drifted from the enumeration they came from, a business object
  switched to a kind that cannot be stored — was silent while it was being made, and the
  refusal arrived afterwards, naming an edit whoever made it had stopped thinking about.

  The panel is live now. Every change is judged as it is made, and the bar and the marks on the
  drawing say so at once. That closes the category rather than one edit at a time, which is how
  the three known cases had been treated.

  The rules are served, not copied into the browser. `POST /api/v1/infomodel/validate` judges a
  document the caller is holding and stores nothing — no saved revision, no application scope,
  and an invalid document is an answer carrying findings rather than an error, because a model
  mid-edit is *expected* to be invalid. Two copies of a rule set are two rule sets, and the copy
  the author sees is the one that would drift from the one Save enforces. The same route is an
  MCP tool, `atlas_validate_information_model`, so an agent can check a model it is composing
  before writing it anywhere.

  When the server cannot answer, the last verdict stands rather than the bar going blank: a
  stale finding is closer to the truth than a clean bill of health nobody checked.

- **An approver decides a request once, instead of deciding it twelve times.** An
  approval in Atlas is one user task per order line — the approval process is
  started multi-instance from the order's ready lines, so a workplace ordered as
  twelve products is twelve process instances and twelve tasks. That shape is
  right and is unchanged: a line is what gets provisioned, refused, escalated,
  reassigned and returned, and each of those needs its own instance.

  What was wrong was the surface. The approver of a twelve-line workplace pressed
  Genehmigen twelve times, read the same recipient twelve times, and on a refusal
  typed the same reason twelve times. A person doing the same thing for the fourth
  time is no longer reading it: a surface producing twelve identical clicks has not
  obtained twelve judgements, it has obtained one and a habit.

  The decision card for a position that is part of a larger request now names **the
  rest of the request** — each position with its price, not a count, because the
  thing being agreed to is "I have seen what is in this request" — and offers one
  checkbox. Ticked, one call decides all of that order's open approvals the caller
  holds, with one reason, and **each is still completed as its own task**, because
  each is still its own process instance and each still has to act on what it was
  told. The count moves onto the buttons, since the button is the last thing
  somebody reads before the decision is irreversible. A request with one position
  gets no checkbox and still takes the single-task route.

  **The record is read as one decision, not counted as twelve.** Twelve completions
  in the same second by the same person on the same order with the same reason are
  the legible signature of one collective decision — where twelve clicks a minute
  apart, from somebody who stopped reading after the third, look like twelve
  examinations and are indistinguishable from them.

  **There is no atomicity and the page says so.** Nothing spans twelve process
  instances, and a completion that went through has already handed its answer to
  its process, which may have started provisioning. So the answer is per line:
  what was decided, and what was not with the reason for each, named on screen.
  "Eleven of twelve" is a number nobody can act on; "the laptop is still open
  because it was decided in another tab" is.

  Refused, on the server and not only in the browser: keys from more than one order
  (one reason cannot cover two people's requests), a refusal with no reason, and
  more than a hundred keys — which is not a resource limit but a statement about
  what one decision can plausibly be. The gate is the approval list's and has no
  operator bypass: an operator who must step in does it on the task itself, where
  the record says an operator did.

- **A product says what kind of thing it is, and the portal's first column finally
  carries data.** The portal's cascade has drawn four columns since the layout
  landed — Kategorie, Bundle, Angebot, Service. The first one was filled with the
  catalogue's own name and a note reading *"Atlas has no category level above the
  bundle today"*: a placeholder telling the truth, because there was nowhere for a
  product to say what kind of thing it was. A catalogue of eight products does not
  need headings. A catalogue of two hundred is unusable without them.

  A product now carries a **category**, and it is a **plain string the maintainer
  types** while they have the product open, offered back through a list of the
  headings already in the catalogue so the second product is spelled like the
  first. The column shows **Alle** above the headings, so it is never a dead end;
  the headings alphabetically, by the locale's own rule; and **Ohne Kategorie**
  last, appearing only when something is in it — a heading for nothing is a heading
  nobody can use, and hiding uncategorised products instead would lose them. The
  services view groups what a person already holds by the same headings, so "where
  do I find this" has one answer on both sides of the portal. Publishing refuses a
  category that is present and **blank**, because blank is the bucket's own value
  and a product that meant to say something and lost it would be invisible against
  one that never said anything.

  **A heading, not an entity, and the three costs are stated rather than hidden.**
  Nothing in Atlas branches on a category — no rule, no approval, no eligibility,
  no process binding reads it; it is a way of *looking* at a release. Every property
  that would justify an entity is a property something else would need, and no such
  something exists. So: the headings have **no ordering of their own** (a rank on a
  category is the entity this refused, arriving through the back door, and a test
  holds the sort against it); they are **not translated**, unlike every other text
  on a product, which is a genuine regression against the rest of the surface; and
  **two spellings are two categories**, recorded as a deliberate non-check so that
  the day it becomes intolerable, the reason it was tolerable is on file.

- **A product can say what it costs, and the approver sees it.** There was **no price
  field anywhere in Atlas** — not on a product, not on an order line, not on the
  approval surface — so an approver was asked to approve a laptop without being told
  what it cost.

  A product now carries a price, and it is a **string written as the catalogue's
  maintainer wants it read**: `CHF 1'200.–`, `49.– / Monat`, `ab 10 Stück CHF 39.–`,
  `im Grundpaket enthalten`. None of those is a number, and every one of them is an
  answer an approver can act on.

  **Displayed and never computed, on purpose.** A number invites a total; a total
  invites two products in different currencies; that invites a rate and an effective
  date. Every one of those belongs to an installation's finance rules, and a catalogue
  storing a number would have started deciding them by implication before anybody had
  chosen. The cost is stated rather than hidden: **nothing adds these up.** That is
  survivable because one approval decides one line, so the one figure it shows is the
  one figure it needs — and a test asserts that no page parses a price into a number,
  because a single `Number(price)` somewhere is the whole money model, invented without
  being chosen.

  **It is frozen like a rule although it is not one.** Nothing branches on a price, and
  it travels into the release and onto the order line anyway, for the sentence that
  governs the approval rule and the ceiling beside it: an approver saw a figure and
  decided on it, and a catalogue edit next week must not make the record show a
  different one. The approval surface therefore reads it **from the order line** — the
  line is the order's own record of what was decided on, and reading from the catalogue
  would give the same answer today and a different one the day somebody edits a price,
  which is exactly when it matters and nobody is looking.

  Publishing refuses one thing: a price that is present and blank. That is worse than
  saying nothing, because the portal renders an empty field where a figure belongs and
  a reader cannot tell "we do not say" from "somebody left it blank" — so the portal
  says the first out loud instead. It shows on the product's details, on the approval
  panel, and on the approval **row**, because a list of forty is scanned rather than
  opened one at a time.

- **One position can be withdrawn on its own, and its details corrected.** The story
  asks to modify or delete positions directly. Deleting existed only for a **whole
  order**, so somebody who no longer wanted the second screen had to take the laptop
  back with it — the per-line transition had been in the package since it was written,
  with nothing calling it. Modifying did not exist at all.

  **"Modify" is two different acts, and treating them as one is how a record starts
  lying.**

  Changing *what is held* — another product, another variant — is **not offered**. A
  line that was provisioned and then quietly became a different product leaves the
  access record unable to answer what somebody had and when, which is the one question
  it exists for. The honest path already exists: give it back, order the other thing,
  and the record carries both with the dates that make it readable.

  Correcting *what was recorded about it* — the answers to the product's configuration
  form — **is** offered, and what it may do is asked of the status machine that already
  decides what can still change, rather than decided a second time beside it:

  - A position **not yet attempted** is simply corrected. No amendment is recorded:
    nothing was delivered under the old answers, and recording one would tell a reader
    that something had been.
  - A position the recipient **already holds** is corrected *and the correction is
    recorded* — what the answers said before, who changed them, when, and why. The
    laptop is at the wrong site and correcting the record does not move it; an
    overwrite would leave the order saying something that was never true of the
    delivery, and a reader could not tell the corrected record from an accurate one.
    The amendments are a list and not a slot, because details having been wrong twice
    is a different fact from their having been wrong once.
  - A position **being provisioned now** is refused, and the refusal says to wait. A
    process has the line, which is a conversation with a system Atlas does not control.
  - A **rejected, cancelled or abandoned** position is refused: a closed record of a
    request that produced nothing.

  Whether a field is required is still the form's own statement, not a second copy of
  that rule in the order service.

  **A position its whole always carries cannot be withdrawn on its own.** The basket
  will not let anybody deselect an integral part — a workplace is not a workplace
  without its account — and a rule enforced when ordering and not afterwards is not a
  rule. The order could not tell, because it carries the precedence graph and not the
  composition one, so the line now carries that too, frozen at placement like every
  other statement about the release. The refusal names what carries the part, because
  the answer somebody needs is "take back the workplace instead".

- **A product can ask the orderer for what its name does not say.** A laptop is not
  fully described by being a laptop: somebody has to say which cost centre it is booked
  to and which site it goes to. Nothing could hold that — a product declared no fields
  and an order line carried no values — so every order needing more than a product name
  finished as a phone call, and the answer lived in whatever the caller wrote down.
  Variants do not solve it: a variant is a fixed shape chosen in advance, and a cost
  centre is not one of a list.

  A product now names **one Atlas form**. The basket renders it — the last screen before
  an order exists, and the one that already shows what will actually be provisioned —
  and the answers travel with the order line, beside the id of the form they answered.

  **A form id and not a field list of its own**, because Atlas already has forms: a
  definition, an editor, a generator, a renderer, and two surfaces rendering them. A
  second way to declare "these are the fields somebody fills in" would be a second thing
  to author, a second thing to render, and a second set of types, validation rules and
  localisation to keep level with the first — behind on the day it shipped. The
  catalogue names an id and interprets nothing; which questions there are, which are
  required and what counts as valid stay the form's own statements, checked by the form
  runtime before anything is sent.

  **The release freezes the id and the line freezes the answers.** A release freezes
  *rules* — the approval, the ceiling, the bindings — because a rule relaxed next week
  must not change what somebody was held to this week. A form is not a rule: what has to
  survive is what was answered, and "cost centre 4711" stays true whatever the form does
  afterwards. Copying the schema into every release would put a rendering artifact inside
  a design-time model that has kept rendering out of itself, and send it to every browser
  that opens the portal.

  Answers are keyed by item, because two laptops in one basket are two cost centres and a
  flat map would keep one of them. Two things are refused rather than dropped, both
  because the alternative is an order that silently loses something somebody typed:
  answers for a product the order does not carry (a stale basket), and answers for a
  product that asks nothing (nothing would read them). A form left *unanswered* is not
  refused there — that is the form's own rule, and a second copy of it in the order
  service would be wrong the first time somebody marks a field optional.

  The product editor offers the forms that exist, never free text — the same rule the
  process bindings follow, because a product bound to a form nobody wrote is a basket the
  orderer cannot get past, found by them rather than by whoever bound it.


- **A catalogue's appearance is set on the screen that fills it.** A catalogue has carried
  its own colour, typeface and brand mark since it was built — the portal and the approval
  page paint themselves from it — and no screen offered any of it. The one thing that makes
  a catalogue somebody *else's* was reachable only by whoever was willing to write JSON by
  hand, which is the exact state the authoring page exists to end.

  An accent colour with a picker beside the field, the four typefaces the binary ships, and
  a brand mark uploaded and removed with a preview. Empty means the catalogue wears the
  instance's appearance, and a button says so in those words.

  The typefaces are a list and not a URL, as the server has it: a web font would reach a
  third party on every portal page load, carrying the visitor's address there — an outbound
  dependency on pages that must render when nothing else is reachable. A test holds the four
  on screen against the four the server ships, in both directions: an option the server
  refuses is a control that cannot work, and one it accepts but the page omits is a
  capability lost to a forgotten line.

  Administration and not catalogue maintenance, like the server has it: an editor may change
  what a catalogue offers and not whose it looks like. The form is drawn for an administrator
  only, because offering one that always ends in 403 is its own kind of lie.

- **The recipient of an order is picked, not typed — and the field is only shown to
  accounts that may use it.** Ordering in somebody else's name became a first-class
  screen gated on the operator role, and the field it goes through took a free string
  and offered no help finding one. The comment above it said a picker would mean
  shipping an organisation chart.

  **That was wrong, and it is worth saying so rather than quietly changing it.** Atlas
  already serves exactly this list, to any authenticated caller, at
  `GET /api/v1/principals` — the directory every member and assignee picker in the
  product reads. It carries a type, an opaque id and a display name, and deliberately
  nothing else: no address, no roles, no reporting line. There is no hierarchy in it to
  disclose, and a hierarchy is what an organisation chart is.

  The field now suggests from that list as somebody types, shows the person's name, and
  sends the id — a display name is not something the server can resolve, and an id is
  not something a person can check. Typing over a picked name un-picks it, or the order
  would be placed for whoever was chosen before under a name no longer on screen. Free
  text still resolves, by principal id, username, directory id or mail address.

  Groups are in that directory and are not offered here: an entitlement is held by a
  person, so a group would be a recipient the server refuses after the basket is
  already full.

  **The scope is the role and not an "area of responsibility"**, and that is settled
  rather than left open: an area of responsibility means a reporting line, and Atlas
  has no reporting line. The `superior` approval kind has the caller name the superior
  precisely because a directory lookup belongs to a modelled process and not to the
  engine. Scoping a person search to a hierarchy would mean inventing the hierarchy
  first, and an invented hierarchy decides who may act in whose name.

  **The page also learns who is reading it.** It fetched a catalogue, a release, orders,
  the inventory and favourites and never asked what the account may do, so the recipient
  field was drawn for every visitor and answered 403 for almost all of them — which
  reads as a permission that failed rather than one they never had.

- **A catalogue can be searched, and by words it does not display.** The portal browsed
  and did not find. Four columns cascade from the catalogue to the individual service,
  which works for somebody who knows roughly where a thing sits and is useless to
  everybody else — the cascade shows what a thing is *part of*, and that is exactly the
  knowledge the searcher does not have. "Power BI Pro" sits two levels under "Productivity
  Enabling", and nobody looking for a reporting tool has a reason to open either.

  A product now carries **keywords**: the synonym, the abbreviation, the vendor's own
  term, the name of the thing it replaced. They are searched together with every name the
  item carries, and a publish refuses a blank one — an empty string is contained in every
  query, so one product holding one would surface for everything anybody typed.

  **The list is flat and not per locale**, unlike every other text on an item. A synonym
  list is for finding, not for displaying; nothing renders it; and a searcher's language is
  not the catalogue's. Somebody reading a German catalogue types "laptop" as readily as
  "Notebook", and "M365" belongs to no language at all. For the same reason the search
  reads *every* locale's name rather than the one on screen: refusing to match a word the
  catalogue itself carries would be the search failing at its only job.

  **A query replaces the cascade rather than filtering it.** Filtering the four columns
  was the obvious shape and is the wrong one — a match three levels deep would leave an
  empty column on screen and the person would conclude the catalogue does not carry it.
  So the columns are replaced by a flat list, and each hit says the path it sits on: the
  answer is both *what* and *where*. Choosing a hit opens the cascade at that item rather
  than ordering from a list that does not show what the thing comes with.

  The search runs in the browser over the release the page already fetched. Not for speed:
  a route would re-send data the page has, an index would be a second copy of the
  catalogue to keep true, and — the part that matters — a server-side search would need
  its own audience filter, correct forever, in a second place. The page can only search
  what it was given, and it was given exactly one catalogue.

- **The approval list can be searched and ordered.** It rendered every open approval in
  whatever order the endpoint returned — newest first — which is fine at three and a wall at
  forty. The story asks for what a wall needs.

  A search field, a sort control and a count. Deliberately **not** a table with a filter per
  column, for the reason [ADR-0311](docs/adr/0311-portal-approval-page.md) gives: the common
  approver is a line manager who decides perhaps four times a year, and a page that grew into
  a console is one they will ask a colleague to operate. One field matches across the product,
  the recipient, the orderer, the order id and the catalogue, because somebody looking for
  "the laptop for Ada" does not know which column they are searching.

  **Oldest first is now the default**, which changes what the page did. What has waited
  longest is what nobody has looked at — the argument the recertification campaign and the
  conflict report each make about their own lists.

  Age is the job key, because a user task carries no created-at and the approvals endpoint
  already pages by it; a clock reading taken in the browser would be a number nobody can
  check. A row shows a due date where the model set one and *passed on* where an assignment
  record exists — and says nothing where it does not, because that absence is the answer
  "nobody has had to chase this".

  Due dates sort ahead of everything undated: a task somebody put a deadline on is a different
  thing from one nobody did, and sorting the undated in among them would bury the deadlines.

### Fixed

- **Two pages rendered the literal word "null".** `render()` passed `cond ? node : null` to
  `replaceChildren`, which — unlike the `el()` helper beside it — turns a non-node argument
  into a *text* node. The approval page has three such slots (an error, a stale link, a
  truncation notice) and none is usually filled, so an ordinary load showed `nullnullnull`
  above the list and `null` below it; the portal showed one under its header. Both have
  carried it since they were written. A `paint()` helper filters, in both.

- **The catalogue can now be read backwards.** Every question it answered ran forwards: a
  product names what it contains, what it needs, what it excludes. That is the question an
  *order* asks, and the portal, the basket and the fulfilment schedule are all built on it.

  The person who **maintains** a service asks the opposite, and could not ask it at all. Where
  is this used, and integrally or optionally? **What needs it** — nobody reading the VPN's own
  page learns that the laptop cannot be provisioned without it. What may it never be held
  with? How many people have it, and did this portal grant them or merely find them? A product
  manager about to retire a service, rebind its provisioning or move it between catalogues had
  no way to find out what they were about to break.

  `GET /api/v1/catalog-products/{id}/usage` answers all of it out of the edges every release
  already froze. **No new data, no migration**: the answer has been in the store since the
  first release was published, with nothing to ask it.

  Merged across catalogues, because a service does not belong to one — the same product
  carried by two of them is one thing somebody is about to change, and a per-catalogue answer
  would let them fix one estate and break another. Composition and aggregation stay apart,
  because retiring an integral part changes what the whole *is* and retiring an optional one
  does not.

  **Holders are counted and never named.** A list of the people holding one service is the
  inventory filtered to the interesting part. The count is broken down by origin, because that
  decides what can be done: an ordered right can be returned through its order, an adopted or
  legacy one cannot.

  It is an **MCP tool** (`atlas_product_usage`), unlike every other read this line of work
  added — those were withheld because they are other people's access, and this one names no
  person at all.

  An unknown product answers 404 rather than an empty report: "nothing uses this" and "this
  does not exist" are different answers, and an empty one reads as *safe to retire*.

- **Products can be marked as favourites.** The smallest measure in the plan, and the one
  whose two decisions are the kind that get made by accident.

  **A favourite is a bookmark and never an entitlement.** It stores a product id and nothing
  else — no release, no catalogue, no variant. It says "show me this again", not "I may have
  this", and everything deciding whether the person may still *order* it is asked at read time
  by the routes that already decide it.

  The tidier-looking alternative is a trap: validating a mark against the caller's catalogue
  at write time would mean a catalogue reassignment starts **refusing** marks the person
  already has, and a withdrawn product makes an existing list unwritable — the list would
  break on exactly the events it should survive. Marks that no longer resolve are counted
  rather than hidden, because a star that stopped appearing with no word looks like the page
  lost it.

  **Yours only, with no `?principal=`.** Every other portal read has one for an operator
  administering an estate. Nothing needs to see what another person bookmarked, and a
  parameter nobody needs is a surface to keep closed.

  One product per call rather than a list per call: a replace-the-list write would silently
  drop whatever a second tab marked in between. Marking what is already marked writes nothing,
  so a star pressed twice does not churn a stored file, and the list is sorted on write so the
  stored bytes are a function of the set rather than of the order somebody pressed things in.

  In the portal it is a filter over the columns and not a fourth destination — a favourite is
  still a product in the catalogue, and a separate screen would hide what it is part of. A
  bundle is kept when something under it is marked, or starring a service would hide the way
  to reach it.

- **A product can now say who may receive it.** A catalogue carries an audience and that gate
  is fail-closed — but it was the *only* gate: whoever was in a catalogue's audience could
  order anything in it, and the sole thing between a person and domain administration was an
  approval rule, which says *who decides* rather than *who may ask*.

  "Put it in a stricter catalogue" is the obvious workaround and does not work, for a reason
  written into the design: **a person sees exactly one catalogue**, the highest-ranked one
  their groups reach. A second, stricter catalogue does not restrict a product — it hides it
  behind the shop that person already has. A product offered to part of a catalogue's audience
  could not be expressed at all, short of duplicating the whole catalogue per audience.

  `eligible` on a product names the groups whose members may receive it, frozen into the
  release like the ceiling and the approval rule beside it. **It narrows; it never replaces.**
  An item naming no group inherits the catalogue's restriction rather than removing one, which
  is why the first test in the file is the one proving an unrestricted product still works.

  **Checked against the recipient, never the orderer.** An order has two people, and the
  question is who ends up holding the thing. Checking the caller would refuse a manager
  ordering a workplace for a new hire — the ordinary case — and would equally let an eligible
  manager order a restricted product *for* somebody who may not have it.

  A refusal over an integral part names the product that carries it: a `composition` part is
  never deselectable, so "you may not receive a licence" about a licence nobody chose reads as
  a bug rather than as a rule. 403 and not 409 — a conflict is a state of the estate that
  giving something back would resolve, this is a statement about who the recipient is.

  Publishing refuses a blank group id and deliberately **not** an eligible list disjoint from
  the catalogue's audience: one person is in many groups at once, and being reached through one
  while being eligible through another is the ordinary way this is used.

- **A hold that ends now leaves a record that it existed.** The inventory is present tense by
  construction — a grant writes a row, a revocation deletes it — and the order behind a right
  is deleted by retention long before the right ends, which is why the inventory is engine
  state at all. Put those two facts together and a third follows that nothing had a place for:
  when a right ends, *everything* about it goes, and the estate can no longer say whether the
  person ever held the thing, under whose approval, or for how long.

  It got worse as detection got better. Every finding the last three slices added is about a
  **held** right, and every remedy ends the hold — so "this person held `create-supplier` and
  `approve-payment` together for six months" is a finding that ceases to exist the moment
  anybody acts on it. **The remedy destroyed the evidence of the problem**, and an estate that
  remembers only the mistakes nobody fixed has the record backwards.

  Closing a hold now writes a row into a new engine-state column family, in the same
  transaction that deletes the live entitlement. It **copies** the hold rather than referring
  to it, because there is nothing left to refer to.

  **The reason it ended changes what the row means.** A `returned` hold is evidence the person
  *had* the access; a `corrected` one — reconciliation found the target system did not have it
  — is evidence only that Atlas *claimed* they did, which is all `handleRevokeDiscrepancy`
  ever decided. Writing the second as the first would assert, in a record kept for years, that
  somebody had access nobody can show they had. Every row carries the word and the flag.

  `GET /api/v1/entitlements/history` lists what has ended, and `?at=` answers the question an
  access review actually asks — what the record said on a given day, drawn from the ended holds
  *and* from what is still held. It is not an MCP tool: an assistant that could read it would
  assemble a person's whole access biography in one call, and `?at=` reconstructs a past day.

  The fold reads the hold through its own transaction rather than taking a frozen copy, which
  stays inside I4/I6 — those require determinism, not the absence of reads — and is what makes
  a double revocation write one row instead of two. `Line.ReturnedBy` joins `DecidedBy` and
  `AbandonedBy`, recorded when a return is *asked for*: what completes one is a deprovisioning
  process, and naming that as the decider would attribute a decision to a robot.

  This is the first slice in this line of work that needs **no modelled process at all** — the
  record accrues as a consequence of what the portal already does.

- **The catalogue can now say what must never be held together.** Everything the portal had
  learned about access was **detective or temporal**: the commissioning load records what was
  there, reconciliation checks whether the record is true, recertification asks whether it is
  justified, an expiry ends it by itself. All four look at one right at a time, and all four
  look *after*. None could express the oldest control in access governance — the clerk who
  may create a supplier must not also approve payments to it.

  A catalogue declares it as a third edge kind, `excludes`, beside structure and precedence.
  It is the **only symmetric** kind — "A must not be held with B" is exactly the reverse — so
  publishing writes **both directions** into the release. A release recording one would make
  every reader responsible for knowing which, and a reader that got it wrong would find half
  the violations and report the estate as half clean, silently. Publishing refuses an item
  that excludes itself.

  **An order that would create a forbidden combination is refused at placement**, against
  what the recipient already holds and against the rest of the same basket. Detecting instead
  would let the combination exist for as long as detection takes, which is a detective
  control with extra steps. The refusal names both items and which side is already held.

  `GET /api/v1/conflicts` reports who already holds one, against the **current** release —
  and that is the deliberate opposite of the expiry ceiling, which never reaches a right
  granted before it was declared. An expiry is part of what was granted; an incompatibility
  is a statement about what may coexist now, so declaring a rule surfaces its violations the
  same day.

  **A conflict has no culprit**, and that is why nothing here acts: it is a fact about a
  pair, no rule can say which half is wrong, and an automatic remedy would have to choose —
  taking away the right the person actually needs while leaving the other. The remedy is an
  order's return or an access review, both of which already exist and both of which record
  who decided. This is the first slice in this line of work that adds no new way to take
  access away. `examples/unvereinbarkeit.bpmn` is the modelled process.

- **A reminder can now ask what is waiting for somebody else.** The portal asks people for
  three different things — decide an order line, answer a recertification row, do a task —
  and none of it happens while nobody opens Atlas and looks. A campaign of five hundred rows
  across forty managers, with nobody told, closes with four hundred and eighty undecided:
  each correctly recorded as *not certified*, and useless.

  The gap was sharper than "there is no notification". Atlas could already send mail — a
  modelled process carries a mail task, `to=` names a principal or a group, and the address
  is resolved in the server at the moment of sending, so it never enters a variable, an
  order or the event log. **What was missing is that every route answering "what is waiting"
  answers only for the caller**, and a reminder process is not the person it is reminding.

  `GET /api/v1/pending-work` answers the caller's own; `?principal=` answers somebody
  else's and is the **operator's**, because a portal where any user can enumerate any other
  user's pending work has turned an inbox into an organisation chart with workloads
  attached. A reminder's token carries the new `reminders` scope, which reaches exactly that
  one route — it cannot read an inventory, run a comparison or decide anything.

  **One wrong reminder costs more than ten right ones earn**, so nothing is listed that the
  person cannot act on right now: not a row in a campaign that has closed, not one somebody
  already decided, not an approval that has escalated away. It counts as well as lists,
  because the first decision a reminder makes is whether to send at all. **Atlas does not
  send** — `examples/erinnerung/` does, one mail per person rather than one per row.

- **A right can now end by itself.** Everything the portal grants, it granted forever — which
  nobody notices on the day it is built, and which is why the commissioning load,
  reconciliation and recertification all exist: three controls that find access which should
  not be there, *after* it is there. Recertification in particular is the manual compensation
  for a missing expiry, paid for in the scarcest resource in the system, a line manager's
  attention. **A question that did not need to be asked is worth more than a better way of
  asking it.**

  A product declares a ceiling with `maxDays`, it travels into the order line frozen from the
  release — like the provisioning process, the deprovisioning process and the approval rule
  already do — and a grant made under it carries an end. Products without one grant
  open-ended rights, which is every product until somebody sets a ceiling.

  **An expiry is not a removal.** The day after the end the target system still has the
  membership and nothing has run; all that is true is that Atlas said the access should have
  ended. So an expired right stays **held** and is reported overdue — dropping the record
  when a clock ticks would make Atlas assert that somebody does not have access they
  demonstrably do, which is the direction of wrongness that corrupts the evidence.

  `GET /api/v1/entitlements/expiring` answers what is due within a window and what is past
  its end. The removing is done by a modelled process returning the **order line**, which is
  a stronger mechanism than either sibling can use: only an ordered right ever carries an
  end, so an expiring right always has an order behind it, and a return revokes by the
  release it was placed against, frozen when it was placed. A right whose order has since
  been deleted by retention cannot be returned at all, and those are counted apart as
  `unendable` — a number that never moves has to say why rather than look like a backlog.

  **The ceiling never reaches an adopted or legacy right.** A commissioning load records a
  found right's start as the moment it was *found*, so a ceiling measured from it would
  schedule an entire estate to expire on the anniversary of the day somebody switched the
  portal on. `examples/befristung.bpmn` is the modelled process, and a recertification row
  whose right ends by itself now says so — those are questions that did not need asking.

- **A business object says where it is used.** The information model gave a data object's
  `itemSubjectRef` a type to resolve against, and every reading built on it since has run
  from the process outwards. The vocabulary itself had none: somebody about to rename
  `Order.total`, retire an enumeration literal or drop a state could see what an Order *is*
  and nothing whatever about what the change would break.

  **Data › Business objects** is the vocabulary read as a vocabulary — every class of every
  information model you can see, business objects, value types and enumerations together, in
  the console's shared sort-and-filter table. One list across applications, because two
  applications each modelling an `Order` is the failure the information model exists to
  prevent, one level up, and a per-model view cannot show it. Each row carries what the class
  holds — its members, its business key, its states — and how much of the estate depends on
  it.

  Opening one answers the question a change actually asks. Every deployed process that
  declares a data object of that class, and **every element that reads it, writes it, writes
  one member of it, moves it into a state, or names the store it is kept in** — with the
  element, the member and the state named, so the answer is precise enough to act on. Beside
  it, the model's own uses: an attribute typed with it, an association, a lifecycle taking its
  states from it, a store holding it. Those are listed apart rather than added in, because an
  «enumeration» is normally declared by no data object at all — a reading that counted only
  processes would report the vocabulary's most shared elements as dead, and somebody would
  eventually act on that.

  Two things it deliberately does not do. It does not guess: a data object with no declared
  type is not read as a use of the class its name resembles, because an inference in a list
  somebody is about to act on is worse than a gap. And it reads only what this installation
  runs — the deployed, active, latest version of each process — so a Modeler draft is not in
  it, which the page says where it makes the claim rather than leaving it to be assumed.
  `GET /api/v1/infomodel/classes` and `GET /api/v1/infomodel/models/{id}/usage?class=Order`
  serve both readings; both are computed on every call and stored nowhere. Both are MCP tools
  too — `atlas_class_catalog` and `atlas_class_usage` — because an agent proposing a rename is
  exactly the caller that cannot otherwise see what it would break.
- **An incident flood is read by cause, and cleared in one action.** Every incident surface
  built so far answers "what is stuck here" one incident at a time, which is the right size
  until a worker stops answering: then every instance that reaches its task parks, and a few
  thousand incidents are one cause with one fix that the product treats as a few thousand
  problems. The list returned rows — megabytes of near-identical JSON per refresh, thousands
  of DOM rows in a table that filters in the browser — and clearing them was one dialog per
  incident.

  `GET /api/v1/incidents/summary` answers instead in **one line per cause**: the (definition,
  element, kind) triple, with how many tokens are behind it, the window it has been running,
  a representative message, and the worker the parked task resolves through — so the fix is
  reachable from the cause and not only from a row. Its size is the number of causes, not the
  number of incidents.

  `POST /api/v1/incidents/resolve` is the matching action, in the shape bulk termination
  settled (ADR-0090): an explicit set of ticked keys, or a scope — `processDefKey`,
  `processInstanceKey`, `elementId` (or `elementIndex`, for a group whose definition is
  no longer deployed and so resolves to no id), `type`, `message` — resolved in bounded batches
  (`remaining=true` → repeat). A scope must name at least one selector; resolving every
  incident on the server is asked for deliberately with `{"type":"job"}` rather than by
  leaving a field out. The listing gained `?element=`, `?elementIndex=`, `?type=` and `?message=` and evaluates
  the **same selector**, so what an operator reads and what the action touches cannot
  disagree. Both tools exist over MCP too (`atlas_incident_summary`,
  `atlas_resolve_incidents`).

  **Operations → Incidents** opens on those causes, with *Resolve all*, the worker fix and a
  scoped row page beside each; the rows keep every per-incident way out and gain tick-boxes
  for a hand-picked set. Repairing the worker from a cause retries the whole cause, because
  the fix was to the thing all of them share. The Instances overview reads its Incidents
  column from the summary — the same column, from a couple of hundred bytes instead of
  megabytes.

- **The third question about somebody's access can now be asked.** Ordering answers *may they
  have it*; reconciliation answers *do they actually have it*; nothing asked *do they still
  need it*. That third one is not the smaller sibling of the other two — a right that was
  properly approved, properly provisioned and is correctly recorded can still be wrong, and
  in most estates it is the dominant way wrong access accumulates. People change roles and
  keep what the old one needed. Nobody granted anything improperly; nobody removed anything
  either, because removing is somebody's job and therefore nobody's.

  `POST /api/v1/recertification` turns what the inventory records into questions, each
  addressed to the person who can judge it. **Who reviews is named by the caller**, because
  Atlas does not resolve line managers — a directory lookup belongs to a modelled process,
  exactly as it does for the `superior` approval rule. A holder nobody names gives an
  *unassigned* row, which lands with the campaign's owner rather than stopping the campaign.

  The whole design is a refusal to make a signature cheap. **There is no way to answer more
  than one row** — not in the screen and not in the API — because a campaign answered in bulk
  is an attestation with no reading behind it, which is worse than none: an auditor believes
  it. **Silence is never a decision**: a campaign closes with unanswered rows in it and they
  stay unanswered, so `undecided` is a first-class count rather than a remainder. There is no
  auto-revoke at the deadline, and no re-grant — granting is ordering, and ordering carries
  the approval rule.

  Each row carries what the reviewer was shown, frozen: origin, order, how long it has been
  held, and whether an open reconciliation finding disputes it. Certifying a disputed right
  is signing a statement about something two systems currently disagree about, so it is
  marked — and marked rather than refused, because one finding must not block a campaign over
  an estate. Withdrawing a right runs the product's own deprovisioning process, never a
  direct worker call. `examples/rezertifizierung.bpmn` is the modelled process, and **Tasks →
  Access review** is where somebody answers — Tasks rather than Operations, because the
  reviewer is a line manager who has never opened Operations.

- **A deployed decision version can now be removed, and so can a model file nothing
  points at.** Both stores only ever grew: every Deploy in the decision editor minted a
  version carrying the full DMN source, and every upload left a file behind.
  [ADR-0329](docs/adr/0329-a-decision-deployment-is-not-deletable.md) had written the
  rule such a delete would need before any route existed; this is that route, with that
  rule.

  `DELETE /api/v1/decision-deployments/{key}` refuses while a deployed process is
  **pinned** to the key — it resolved a `latest`-bound reference to that exact version
  and carries no copy of the model, and a pin outlives the instances that used it, so
  "no running instances" is not the test. It also refuses the **current** version of a
  decision that still has older versions behind it: removing it would send the next
  deploy quietly back a version, and free a version number the surviving records no
  longer account for. Removing a version history therefore goes oldest first, and the
  refusal says which of the two it is. The registry's "newest model providing this
  decision" pointers are rebuilt from the survivors in deployment order, so what the
  server answers after a delete and what it answers after a reboot cannot diverge.

  `DELETE /api/v1/dmn-models/{ref}` refuses while any DMN reference points at the
  handle, because that would leave them unresolved. A decision deployment's `modelRef`
  does **not** block it: that field is provenance, the record carries its own XML, and
  the decision keeps evaluating after the file is gone.

  Operations' decision page gains a **Deployed versions** table listing every version
  with what is pinned to it — the first answer anywhere to "what is using this version"
  — and offers Delete on the ones that may go. Not assigned gains Delete on an
  unreferenced model. `atlas_delete_decision_deployment` is the MCP counterpart, so the
  tool count is now 107. ([issue #919](https://github.com/pblumer/atlas/issues/919))

- **The inventory can now be checked rather than trusted.** An entitlement asserts that a
  right exists in another system — an assertion Atlas cannot guarantee, because target
  systems are changed from outside it. So it decays silently, and an inventory nobody
  checks is a list of things that were once true.

  `POST /api/v1/reconciliation` compares one reading of one target system against the
  inventory and finds both directions: rights held that nothing here granted, and rights
  recorded that the target system does not have. The second is the one that corrupts the
  evidence, because an inventory wrong that way answers "who had access when" with a
  confident falsehood.

  The whole design hangs on one required field. A commissioning load reports what it
  *found* and never what it did not; reconciliation reads absence as a finding, which
  makes the same silence dangerous. So a run names what it read **completely**, and
  outside that scope nothing is concluded — a right outside it is not missing, it is
  unexamined. There is no default: "nothing" is useless and "everything" is a guess that
  turns a truncated read into a report that the estate has lost its access.

  The scope has **two axes**, and one run may use both: `refs` names references read
  whole, `subjects` names the people whose holdings were read whole. The second is what
  answers an offboarding — *is this person out of everything?* — which a group listing
  structurally cannot: you would have to reconcile every group in the system and observe
  the person's absence from all of them. A subject-scoped run is confined to the system it
  names, so a leaver check against Active Directory never reports somebody's Jira rights
  as missing, and a subject that resolves to no account is reported rather than counted
  clean — **the absence of an account is not the absence of access**. The clean result
  gets its own sentence in the report, because a verification that returns nothing
  otherwise looks exactly like a run that did nothing.

  It records **transitions, not samples**: ten runs over one disagreement make one record,
  and the run where it goes away closes it. Nothing is ever acted on automatically — adopt
  (`origin: adopted`, the first writer that origin has had), deprovision through the
  product's own process, or revoke the record are three separate calls by a person, and
  none of them is reachable with the worker credential that may run the comparison.
  `examples/abgleich.bpmn` is the nightly modelled process and
  `examples/austrittspruefung.bpmn` the leaver check, and **Operations → Reconciliation**
  is where somebody reads a finding before acting on it — the three actions are not
  guarded alike, because adopt and revoke are recoverable and deprovisioning is not.

- **The inventory is taken before it is enforced.** `model.OriginLegacy` has existed since
  the portal's three models were decided and has had no writer, which meant the inventory
  could only ever contain what Atlas itself had granted. On the day an installation goes
  live that is nothing, while reality is full — so the reconciliation that comes next would
  report every privilege in the estate as a discrepancy, each carrying an executable
  "remove it in the target system".

  `POST /api/v1/inventory-load` takes the rights one reading of one target system found
  and records them as pre-existing. It resolves both halves itself: the subject against the
  mirrored accounts, the right against the new `targets` on a catalogue product — what that
  product is called in AD, in Entra, in Jira. That join is data rather than something a
  worker does, because the load's output is evidence somebody has to be able to disagree
  with: the report says *Alice is in `CN=VPN-Users`, and the catalogue says that group is
  VPN access*, not merely that Alice holds VPN access.

  It writes nothing unless `apply` is true, so an omitted field reports. It never writes
  over a right an order granted, never moves the start date of one it already recorded, and
  only ever adds — a right a batch does not mention is not revoked, because a batch is one
  system's partial answer. What it cannot attribute it names: subjects with no account, and
  `unmapped`, the rights the estate grants that no product claims, most-held first. That
  last list is the one nothing could produce before. `examples/bestandsaufnahme.bpmn` is the
  modelled process, deliberately without a timer — a commissioning load is an act somebody
  performs, not a schedule.

- **Atlas keeps its accounts and groups from a Microsoft Entra tenant, and the first run
  writes nothing.** An account only ever came into being when somebody signed in through
  OIDC, so a fresh installation starts with an almost empty user store — and the next
  piece of work, taking an inventory of the rights that already exist, has to attribute
  every right it finds to an account that is not there.

  Atlas now reads the directory rather than waiting to be told about it. A scheduled
  process (`examples/entra-verzeichnis-abgleich.bpmn`, timer start `R/PT1H`) runs the
  Entra Worker's `delta-users` and `delta-groups` change-tracking queries and reports what
  changed to `POST /api/v1/directory-sync`, which creates accounts, updates them, merges
  the directory onto an account that already existed, and disables the people who have
  left. `GET /api/v1/directory-sync` says where the next run resumes from. Nothing is
  published outbound and there is no inbound provisioning endpoint: the decision record
  carries the argument against SCIM, and against reading it as an oversight.

  **The first run reports and writes nothing**, because an empty cursor enumerates the
  whole tenant against an empty store and a defect there reaches everybody at once. It is
  not a preview with an implementation of its own — the same code decides in both modes
  and only the last step, the write, is skipped — the cursor does not move, so the run may
  be repeated as often as somebody likes, and the report gives counts for the expected and
  whole lines for the notable: merges, disables, refusals, memberships that cannot yet be
  resolved. The mode is a field of the message spelled `apply`, so an omission reports
  rather than provisions, and every report that wrote nothing says so and why.

  Disabling somebody is not only a record: the run that writes it also ends their live
  sessions, revokes their standing OAuth grants, and pushes every mirrored group
  membership it changed into the sessions that are already open — a session carries the
  group ids it was opened with, so without that half a mirror would be a quieter way to
  disable somebody than the administration button that says so.

  An account mirrored onto one a federated login created keeps both identities: Entra's
  ID-token `sub` is pairwise per application and is therefore never the directory object
  id, so the object id lives in a new `directoryId` field and the pairwise subject stays
  where a sign-in looks for it. A created account holds `user` and nothing else, from a
  literal that no input reaches; existing roles are never widened or narrowed; and the
  last enabled administrator is never disabled. A mirrored group keeps the directory's own
  member ids beside the translated ones, so a membership that arrives before its account
  resolves on a later run instead of being lost. The credential the process carries is an
  API token of the new `directory` scope, which reaches those two routes and nothing else
  — it cannot deploy — and both routes refuse outright on a server running without
  authentication. Three budgets bound the message, the batch and the report
  (`ATLAS_LIMIT_DIRECTORY_SYNC`, `_DIRECTORY_OBJECTS`, `_DIRECTORY_REPORT`).
  ([ADR-0332](docs/adr/0332-entra-directory-provisioning.md))

- **A relationship is drawn from the class it starts at, the way a sequence flow is.**
  Selecting a class on the information model's canvas now opens the little menu beside
  it that the BPMN modeler has had all along: the relationship kinds that class could
  actually reach something with, and a bin. Drag one onto the class at the other end and
  the line is drawn.

  Drawing used to be a mode. The kind was armed in the palette, and the next two classes
  clicked became its ends — which had to be entered before the classes were looked at,
  remembered between the two clicks, and aimed from a convention nothing on screen
  stated. Which end a composition's diamond goes on is the question the notation turns
  on, and it was answered by the order somebody happened to click.

  The subset now answers under the pointer rather than after the drop: a target that
  cannot take this kind of relationship never lights, and the question asked is the
  narrow one — not whether two classes may relate, but whether they may relate *like
  this*. A drop on a refused target still says why, in the same words the deploy would
  use. The BPMN modeler drops such a gesture in silence; this canvas has explained the
  notation at that exact moment since the palette did the drawing.

  The armed palette mode still works. It is the only way to draw a relationship without
  a pointer that can drag, and removing it is a separate decision.
  ([ADR-0352](docs/adr/0352-draw-a-relationship-from-the-class-it-starts-at.md),
  [ADR-0237](docs/adr/0237-class-canvas-on-diagram-js.md))
- **An «enumeration» now says which values a member may take, and is drawn as part of the
  class diagram.** Four questions an author answers while drawing a write arrow have the
  same shape, and only three of them were asked that way: which class is this data
  object, which state does the write move it into, which member does it target — and
  then, in free text, what goes in. Where the member's type is an «enumeration», the
  model has already written down the complete list of values it may hold. The write row
  offers them, and a value that is computed still takes any FEEL expression, because a
  picker that cannot be left would be lying about what the field is.

  At deploy, a value that is **constant** is checked against the literals, and one that
  is none of them is a warning worded like the unknown-state warning, for the same
  reason: a model that is merely behind its process is not broken. Constant means an
  expression that reads no variable — the inputs decide, not what an evaluation happens
  to return, because `=if x then "approved" else "approvd"` with `x` unbound hands back a
  perfectly concrete else branch that the process may never write.

  On the class diagram, an «enumeration» that types an attribute is joined to the class
  that uses it, derived and never authored, the way a data store's line and the
  `«lifecycle»` line already are. Until now it was the one box that floated: the
  compartment said `status : Lebenszustand` and nothing held the two together. One line
  per pair, labelled with the attributes that justify it, and none where the `«lifecycle»`
  line already joins them — a derived line is routed straight, so a second would be drawn
  on the first. A straight line's label also moved to its midpoint, where it was landing
  on the target box.
  ([ADR-0351](docs/adr/0351-an-enumeration-says-which-values-a-member-may-take.md),
  [ADR-0306](docs/adr/0306-a-lifecycle-may-take-its-states-from-an-enumeration.md))
- **A write arrow can set several members of a data object at once.** A step that
  captures a form's worth of fields writes them from one arrow with a row per field,
  rather than one arrow per field. BPMN always allowed this — a data association carries
  `assignment [0..*]` — and Atlas read one and silently dropped the rest, so a model
  another tool wrote deployed and quietly did something other than what it said.

  The writes are applied in the order they are listed and recorded as **one** change to
  the object, not one per field: an activity that fills in a record did one thing, and a
  timeline showing four half-built identities would be an artefact of how the write was
  compiled rather than something that happened. Order is load-bearing and falls out of
  that: two writes to the same member mean the later one, and a member write after a
  whole-object write on the same arrow lands on the new value.

  This is also the way out of the trade-off the previous release left standing. Writing
  the whole object from one FEEL expression drew well and told the model nothing — the
  members inside an expression cannot be read at deploy time, so the write went
  unchecked and the class derived as having none. Named on their own rows, every member
  is a static fact again: checked against the class, listed in the derived model, and
  compared rather than excluded by the difference reading.
  ([ADR-0350](docs/adr/0350-a-write-arrow-may-set-several-members.md),
  [ADR-0060](docs/adr/0060-field-level-data-object-writes.md))

### Fixed

- **An operations number is a counter or a walk, never the length of a page.** The live
  diagram's wrong incident counts had a shape worth searching for: a list is fetched
  with a page cap, the console counts its rows, and the count is rendered as a fact
  about the population. That agrees with the truth until an installation is busy enough
  to need the number — and because every capped list here is ordered, what falls off is
  a contiguous slice rather than a sample, so a whole class of subject goes missing
  together and the count reads zero rather than low. Zero is not a floor.

  Every number the Operations views state was audited against what produced it. Most
  were already right: the overview's Running and Finished columns (per-definition
  counters), the Incidents view's cause table (a complete walk), the nav badge, and
  every floor that says it is one — the Workers view's queue depth with its `+`, the
  mock directory and mock database printing "showing n of m held", the saved task
  folders' badges. Three were not:

  - **A search hit that is stuck now says so on its own row.** The flag came from
    bucketing the server's whole incident list — capped at 5 000 rows, with a
    truncation header the console never read. Measured on a store holding 5 200 parked
    instances, 200 running instances that were each parked behind an incident rendered
    as a plain "active", on the surface an operator opens to debug one. The count is now
    part of the row, taken through that instance's own element index, and the 5 000-row
    transfer per search is gone with it.
  - **The task inbox's fixed folder badges count the inbox.** "All tasks", "Assigned to
    me", "Unassigned" and "Group tasks" were counted in the browser off the newest-first
    page it had already loaded. Measured: with 700 open tasks, claiming the oldest one
    for a user left their "Assigned to me" reading 0 while the task sat in their inbox.
    The four predicates now live in one place, and the badge comes from the server's own
    walk once the page stops holding the whole inbox — an uncapped page *is* the inbox,
    so counting its rows there is both exact and free.
  - **Nothing walks the incident family on the run loop any more.** `incidentsByJobType`
    walks it whole and does a point read per parked token, and it ran inside a run-loop
    turn — once for the Workers view, once for every Starmap page load. On a flooded
    engine that dispatches tens of thousands of reads onto the goroutine that executes
    process instances, which is exactly what
    [ADR-0266](docs/adr/0266-stats-and-incidents-off-the-loop.md) removed from `/stats`.
    Both callers now take the tally off the loop, before their turn.

  The rule is now checked rather than written down. Five instances of one mistake, none
  caught by review, is not a case for another paragraph of guidance:
  `api/pagecount_internal_test.go` fails a build where `fmtCount()` is handed a list
  length, where a raw read of a capped listing drops the headers that say it is capped,
  or where such a listing is read with nothing nearby that names its bound. Each rule
  was verified by putting the original defect back and watching it fail, and the
  patterns themselves are pinned against known-bad and known-good lines so a guard
  cannot quietly stop matching and pass as coverage. They do not follow data flow, so
  they are a tripwire at the places the mistake has been made rather than a proof that
  it cannot be made again.

  ([ADR-0365](docs/adr/0365-a-number-is-a-counter-or-a-walk.md))

- **The live diagram counts every parked token, not the ones a bounded scan reached.**
  One process, two deployed versions, both under the same broken worker: the Operations
  overview reported 10 910 stuck tokens, the live view of the current version reported
  none at all, and the previous version's diagram badged "50" on each of two tasks
  holding some 5 452 each. Only the overview was right, and the current version's
  diagram — the surface an operator opens *because* the overview flagged the process —
  drew a process whose every running instance was parked as a healthy one.

  The overlay collected its incidents on the run loop, so it was bounded twice, and it
  walked the incident family in key order, attributing each entry to its definition only
  after reading it. Incidents are keyed by element instance and those keys ascend, so
  the budget was spent oldest-first: a version deployed after a flood sat entirely past
  it and was never reached. The per-element numbers were then read off what the scan had
  returned, which is where "50" came from — the page held 100 rows, they fell on two
  tasks, and each badge reported its share of the page rather than of the process.

  The overlay now reads what
  [the cause summary](docs/adr/0337-incident-floods.md) reads, the way it reads it: one
  walk of the incident family off the run loop against a snapshot, through the same
  attribution every other incident surface uses, held for five seconds so a 1.5-second
  poll does not pay for one each time and dropped the moment anything is resolved. The
  count and the detail page are now separate things — `incidentTotal` and
  `elements[].incidents` are exact, `incidents[]` stays a 100-row page for the resolve
  panel, and `incidentCountsExact` says which is which. A definition with nothing parked
  now *states* that it has nothing parked, where before it could not be told apart from
  one the scan had not got to. Nothing walks the incident family on the run loop any
  more.

  Two more readings were wrong for the same reason and are fixed with it: isolating a
  single instance stopped counting its parked tokens once its detail page filled, so an
  instance holding more than 100 reported exactly 100; and the browser hid the incident
  pill whenever the detail list was empty, which is what turned an unreached definition
  into a silent one.
  ([ADR-0366](docs/adr/0366-the-live-diagram-counts-every-parked-token.md))
- **Restoring a backup from another installation no longer attaches this one's history
  to a foreign process.** The portable design-time backup
  ([ADR-0107](docs/adr/0107-backup-and-restore.md)) carries `deployments/` and
  `decisions/`, which are filed by definition key — and it does not carry the counter
  that issues those keys, because that counter is runtime. So the archive held records
  whose identity was minted by a sequence it left behind, and the restore resolved the
  ambiguity by overwriting.

  Measured: install A deploys `alpha`, which takes key 1. Install B deploys `beta`,
  which also takes key 1, and runs one instance to completion. Restore A's backup onto
  B — the documented use — and after the restart key 1 is `alpha`, `beta` is gone from
  the listing, and `alpha` reports one finished instance plus a visit on element
  `wait`, which it has never reached. The per-element aggregates are keyed by
  definition key and element index, so a foreign definition did not merely inherit the
  numbers, it redistributed them across its own elements.

  Measured too: the archive carried `settings/node.json`, so B came back answering with
  A's node id — two running installations claiming one identity, and no provenance left
  to tell where the rest of the archive came from.

  A restored deployment record is now written only when its key is free, or when the
  record already there is the same deployment — the same `processId` at the same
  `version`, or for a decision deployment the same decisions at the same versions.
  Anything else is held back, and the response and the Console name the keys and both
  sides of the clash. The node identity no longer travels, on the way out or the way
  in, so an archive taken before this cannot carry one either. Restoring an
  installation's own backup, including an older one, is unchanged.

  The whole-instance snapshot ([ADR-0109](docs/adr/0109-full-instance-snapshot.md)) was
  never affected and is unchanged: it carries the key space, the job-type table and the
  WAL together and drops the derived state, so it is one consistent point in time —
  measured, a restore left nothing inherited and no key reused.
  ([issue #919](https://github.com/pblumer/atlas/issues/919))


- **A job-type index is never issued twice, so a worker cannot be handed another
  type's work.** The engine-wide job-type table maps a model-authored task type to the
  integer index a job on disk carries, and a worker polling by type is resolved through
  it ([ADR-0007](docs/adr/0007-job-worker-protocol.md)). The table has always stated
  that an index, once issued, is permanent — "jobs already on disk carry it … not even
  after a record is removed by hand" — and derived its counter from the entries that
  survived a reload, which is not the same thing.

  Two things lowered it. Measured: remove the highest entry file and restart, and the
  next new task type is issued that index (`ship-parcel` = 1001, where `send-email`
  was). And with no editing at all — a stored type whose *name* a later build turns
  into a built-in is dropped on load, correctly, but its claim on its index was dropped
  with it, so a store holding such a name at 1005 issued 1005 again to an unrelated
  type five interns later. A parked job carries the number, not the name.

  The dynamic indices now have a durable high-water mark, kept beside the table and
  raised **before** the index it covers is issued, so a crash in between costs a gap
  rather than a repeat; and the load counts every index the store shows it, whether or
  not this build can still use the name it went to. An installation upgrading gets its
  mark written on the boot that upgrades, from what its entries still say — the one
  moment that knowledge exists.
  ([issue #919](https://github.com/pblumer/atlas/issues/919))

- **A deleted definition's key is never issued again, so a new process cannot inherit
  its history.** Every process definition and every decision deployment draws a key
  from one counter, and that key is what the engine files a great deal of durable state
  under: completed instances, the finished count, per-element visit and termination
  aggregates, last activity, message-flow history — plus a release manifest's members
  and a decision deployment's pins. The counter was rebuilt at startup as the highest
  key across *surviving* records, which is correct only while nothing is ever deleted.

  Measured: deploy a process, run one instance to completion, delete the definition,
  restart, deploy a different process — the new one takes the same key and reports one
  finished instance and a visit on an element it had never reached. `DELETE
  /api/v1/processes/{key}` has had this since
  [ADR-0019](docs/adr/0019-durable-deployments.md); the decision delete
  ([ADR-0336](docs/adr/0336-cleaning-up-the-decision-store.md)) reaches it too.

  The key space now has a durable floor: the highest key ever issued, persisted
  **before** the keys it covers are used, and read at boot alongside the surviving
  records. A crash between the two therefore costs a gap in the numbering, which is
  free, rather than a repeat, which is not. Both mint sites go through one reservation,
  so a collaboration of five pools or a publish of five decisions costs one write. An
  installation upgrading finds no floor on its first boot and is exactly as safe as
  before until its next deploy — the information was never written down.
  ([issue #919](https://github.com/pblumer/atlas/issues/919))

- **Deleting a DMN reference now says what it would break.** The confirm read "Delete
  this DMN reference? The temis model itself is not affected" — true, and not the
  thing a reader needs. What a reference decides is not the file on disk; it is
  whether anything can still be *deployed* against the decisions that model provides.
  Delete the last one and a `deployment`-bound business rule task has no model to
  bundle, so its process can never be deployed again — a refusal that arrives weeks
  later, in a message that does not mention the deletion.

  `GET /api/v1/dmnrefs/{id}/impact` answers it beforehand: the decisions this model
  provides, which of them no other reference provides, and the deployed definitions
  and drafts that could then not be deployed, each with its binding. It is the deploy
  preflight's own condition read forwards, so the warning and the refusal cannot
  drift apart. Running instances are never affected and the confirm says so. Nothing
  is blocked — the reference stays the author's to delete — and an impact that cannot
  be fetched falls back to the plain sentence. A draft in an application the caller
  cannot see is counted, never named (ADR-0071).
  ([issue #919](https://github.com/pblumer/atlas/issues/919))

- **A DMN model whose last reference is deleted is no longer lost.** Every surface
  that shows decisions reads DMN *references*: the catalog, the picker, the Modeler's
  artifact list, a publish. The model file itself was reachable only by a handle you
  had to already know, so deleting a reference took the model out of the product while
  leaving it on disk — and the usual recovery, re-uploading it, files a second copy
  under a suffixed handle.

  `GET /api/v1/dmn-models` lists the store: one row per stored handle with what the
  model declares, whether it compiles, and whether any reference points at it. The
  Console shows the unreferenced ones under **Not assigned**, where artifacts
  belonging to no application already live, with **Add reference** on each — which
  re-uses the existing handle, so the model is recovered rather than copied. A model
  that no longer compiles is listed too, because an author who has to fix it has to
  find it first. The delete confirm now says where the file went instead of reassuring
  that it is unaffected. ([issue #919](https://github.com/pblumer/atlas/issues/919))

- **A decision that is only deployed no longer blocks the deploy of a process that
  names it.** The deploy preflight demanded a stored DMN model behind a reference for
  every decision a business rule task called, and refused otherwise with "no DMN model
  provides it — create the decision (or add its reference) in Atlas". Since a decision
  became deployable on its own ([ADR-0322](docs/adr/0322-deploying-one-decision.md)) an
  author could deploy exactly such a decision, see it offered by the task's picker, and
  then be told to create the thing they had just deployed.

  The guard exists to stop a business rule task whose job can never evaluate, and for a
  `latest`-bound task that premise was false: the deploy resolves that reference to the
  newest decision deployment and only falls back to the bundled model when there is
  none, so a covered task never consults the bundle. The preflight is now binding-aware
  on both paths that run it — the single-diagram deploy and the application publish. A
  `deployment`-bound task still needs a model, because it evaluates the one bundled
  under its own key, and its refusal now says that instead of repeating advice the
  author has already followed. ([issue #919](https://github.com/pblumer/atlas/issues/919))

- **A decision whose logic is a literal expression no longer covers the editor's own
  bar.** dmn-js uses `editor` as a state class inside its own components — its literal
  expression view is `<div class="literal-expression textarea editor">` — and Atlas's
  `.editor` is the full-bleed page shell, pinned to the viewport. Since the decision
  editor became a page ([ADR-0320](docs/adr/0320-the-decision-editor-is-a-page.md)) that
  collision drew the expression editor across the whole window, so the view tabs, Save,
  Save to model and Deploy underneath it could not be clicked; a fixed element is not
  clipped by the canvas, so nothing else stopped it. The four properties that rule sets
  are now undone inside the dmn-js subtree, leaving dmn-js's own styling for the class
  alone. ([issue #919](https://github.com/pblumer/atlas/issues/919))
- **The hosted example apps find the instance they just started again, on an engine of
  any size.** Each of them — `reisebuchung-kunde.html`, `reisebuchung-einschritt-kunde.html`,
  `order-to-cash-live.html` and `order-to-cash-jobs.html` — located its own process
  instance by reading `GET /api/v1/instances` and searching the result. That listing is
  a page, not the set: unscoped it is capped at 1000 rows per half, and its active half
  is scanned in ascending instance-key order — oldest first — so the newest instance is
  the first row the cap drops.

  On a server holding more than a thousand active instances the page therefore cannot
  contain the instance the app has just created. The diff came back empty and the start
  failed with `Cannot read properties of undefined (reading 'key')`, while the instance
  itself was running correctly and sitting on its first user task. Nothing in the pages
  had changed; the number of instances in front of them had.

  They now read what they actually need. `GET /instances?process=<defKey>` lists one
  definition's instances off its own index, newest first, and is what the start diff
  compares; `GET /instances/search?q=<instanceKey>` is a point read of a single
  instance, live or finished, and is what the poll for "has my instance ended" asks.
  Both are index-backed, so neither grows with the engine. A start that still cannot
  find its instance now says so in words rather than throwing on an undefined row.

  Three tests hold this down, because the failure is invisible in any environment small
  enough to develop against: a page that reads the unscoped listing passes every manual
  check on a fresh engine and then breaks months later, in production, without a deploy.
  A browser test drives the real wizard against a mocked engine whose bare listing is
  full and never carries the instance; a Go test pins the two endpoints the pages now
  rely on; and a guard over the embedded pages refuses the pattern's return.

  What scoping does not repair is that finding your own instance by elimination is still
  a guess when two callers start the same definition at once. Closing that means the
  start answering with the key it minted, which is a change to the engine's command path
  rather than to a page, and is tracked in
  [issue #933](https://github.com/pblumer/atlas/issues/933).
- **PowerShell runs under `--script-sandbox=strict`, and a profile that cannot start an
  enabled interpreter refuses to boot.** The strict allowlist admitted the installed
  runtimes, the loader and trust files, and a private scratch directory — everything
  Python and JavaScript need to start. The .NET runtime behind `pwsh` needs more: it
  sizes its heap from `/proc`, reads `/proc/mounts`, and resolves its user through
  `/etc/passwd`. None of those were allowed, so CoreCLR refused to start with
  `E_OUTOFMEMORY` and every PowerShell script task failed. The other two languages
  worked throughout, which is the asymmetry that let this land unnoticed.

  The allowlist now carries this process's own `/proc` entry, `/proc/meminfo`,
  `/proc/mounts` and `/etc/passwd`. Its own entry and no other: a rule on `/proc` as a
  whole would hand model-authored code every same-uid process's environment, which is
  where the engine's token and its vault key are, and that is the opposite of what the
  profile exists for.

  Startup no longer takes the profile on trust either. It proved the Landlock ABI and
  stopped there, so a language the sandbox could not run looked healthy until the first
  job failed one at a time. Selecting `strict` now starts each enabled interpreter once,
  inside the real policy, on an empty program. One that cannot start is a startup error
  naming the language and the three ways out; one that is simply not installed stays the
  warning it has always been, because that is a host that was never going to run it.
  ([ADR-0303](docs/adr/0303-script-sandbox-isolation.md),
  [issue #892](https://github.com/pblumer/atlas/issues/892))
- **The Starmap's ArchiMate view now draws ArchiMate's relationships too.** The nodes
  were already ArchiMate's own symbols; the lines between them were still Atlas's — one
  solid, one dashed, one dotted. For a reader who works in the notation that is half
  the alphabet: ArchiMate tells **Assignment**, **Triggering** and **Serving** apart by
  what sits at the ends of an otherwise identical solid line.

  Each is now drawn that way — a ball at the source and a filled arrowhead at the
  target for Assignment, a filled arrowhead for Triggering, an open one for Serving —
  and the lines are solid, because in ArchiMate a dashed line with an open arrowhead is
  a Flow and a dotted one a Realization. Keeping Atlas's dash would not have been a
  missing statement but a wrong one.

  A Serving relationship points the other way from the fact it comes from: ArchiMate
  runs Serving from the provider to the consumer, so the arrowhead sits on the process
  rather than on the worker it names — the same reversal the exported document has
  always made. The key says so in words as well, for the reader who does not already
  know the notation by sight. The marks travel into an exported file, where there is no
  key to hover over.

  The relationship table is now served by the server alongside the element table,
  instead of the browser keeping a second copy. A picture with Triggering's filled
  arrowhead on an edge the exported file calls Serving would be two answers to one
  question, and nothing on either surface would say which was true. Three relationships
  is also all there can be: Atlas knows that an application holds a process, that a
  process calls another, and that a process uses a worker or a decision. Nothing here
  is a Flow or a Realization, and an absent relationship type means Atlas cannot see
  one — never that there is none.

  A served notation is also a copy now. The element table, the relationship table and
  the loss list were shared with every caller, so an edit anywhere would have changed
  the mapping for everybody, silently.

- **The Starmap's ArchiMate view now draws ArchiMate's own symbols and layer colours.**
  Picking **ArchiMate 3.2** under Notation mapped each node to an ArchiMate element type
  and wrote that type under its name — and then drew Atlas's own circles and squares.
  For the one reader that view exists for, that is the vocabulary without the script:
  ArchiMate is recognised by its silhouettes.

  The five mapped kinds are now drawn as the elements themselves — an Application
  Component with its two lugs, an Application Process as an arrow, an Application
  Service as a rounded lozenge, an Application Function as a chevron, and a deployment
  target as a Node's three-dimensional box. The standard allows either a box with a
  small type icon in the corner or the icon at full size; at the size a node is drawn
  here the corner icon would be a pixel or two, so the icon is the node. The written
  type stays beside it for readers who do not know the notation by sight.

  The fills are the layer colours everyone recognises — Application `#B5FFFF`,
  Technology `#C9E7B7` — and the key says plainly what they are: **ArchiMate 3.2
  defines no colours at all**, and these are the convention its own figures and the
  Archi tool use. They are pale by design, so a red or amber finding still stands out
  above them. A draft, a restricted placeholder and an unresolved dependency keep
  Atlas's own shape and colour: ArchiMate has no element for them, and dressing them as
  one would claim something the notation does not.

- **The Starmap opens using the whole window, whatever the size of the estate.** A
  landscape of a handful of nodes was drawn as a handful of small circles adrift in an
  empty canvas, and a single unattached process could sit out at the far edge holding
  two thirds of the window open behind it. Both come from how the picture is scaled:
  the graph is laid out in a world sized from its own content, the opening view shows
  the whole of that world, and so the world's size decides the magnification.

  Two things were working against that. The world had a floor of a window's worth of
  area, put there so that small landscapes would not change when the world became
  content-sized — but the floor stopped binding only past about twenty-five nodes, so
  every smaller estate was laid out in a world several times larger than it needed and
  shown correspondingly small. Measured on the rendered page at 1400x900, as the share
  of the window the nodes and their spacing occupy: five nodes covered 7% where a
  hundred and twenty-five covered 17%. The floor is gone, and the same five nodes now
  cover 18% — the same picture, at the size it should always have been drawn.

  The second is the stranded piece. A process attached to nothing — or a handful of
  processes that call each other and nothing else, which is what a conformance sample
  or a test flow looks like — is held near the picture only by the pull toward its
  centre, against a repulsion that falls away with distance, and that balance puts it
  a long way out. The cost is not the piece itself: the view is framed from the box
  that contains everything, so one thing far out decides how small the rest is drawn.
  On the shapes this was reported on, the furthest piece sat at two to three times the
  picture's own spacing. It is now bounded at 1.5, and the whole piece moves together
  so that nothing the diagram says about the processes inside it changes. It is still
  the outlying thing it is, on the side it settled on, but it no longer sets the scale
  for everything else. Nothing moves in a landscape that has none, and nothing moves at
  all once you have arranged the picture by hand.

  Both corrections are in the layout, which is one function for every notation, so the
  Instances and Incidents heatmaps and the ArchiMate and C4 projections get them too:
  there was never a per-view layout to fix.

- **The script-timeout test no longer reads a pid as an identity.** The fix above gave
  `TestTimeoutKillsTheInterpretersWholeProcessGroup` a probe that can tell a zombie
  from a live process, and it went red in CI again — on a commit whose diff contained
  no Go at all, on a head whose parent had passed the same job twenty minutes earlier,
  and without reproducing once in a full race build or in ten consecutive focused runs.
  The mechanism is not known. What is known is that the assertion rested on a number:
  nothing tied the pid in the file back to the process it was written for, so "that
  number still answers a signal" and "the descendant survived" were being treated as
  one fact when they are two.

  So the test now reads the descendant's own evidence. It is given a second of work
  and a file to write at the end of it; if the group kill reached it, the file is never
  written. Nothing about the pid namespace can confound that. Both halves are shown to
  catch what they are for: killing the child instead of its group trips the elapsed
  check, and a descendant that escapes the group with `setsid` writes the file. The
  signal probe stays as a *diagnostic* — an assertion that can fail while the system is
  correct is unsound whatever its subject, but what it reports is the only lead on the
  open question, so a failure now names the program behind the pid instead of only its
  number.

- **A script's liveness probe read a zombie as a running process.** ADR-0303 made a
  timed-out script take its whole process group with it, and
  `TestTimeoutKillsTheInterpretersWholeProcessGroup` checks that by asking whether the
  descendant is still there — with `kill(pid, 0)`, which is the one question that
  cannot distinguish the two states that matter. The same signal that kills the
  descendant orphans it onto PID 1, and until PID 1 reaps it, it keeps an entry in the
  process table that `kill(2)` goes on addressing. Whether that reap is prompt belongs
  to the environment's init, not to Atlas: under an init that reaps (a CI runner) the
  test passes, and under one that does not (a container started from a plain process,
  a devbox) it fails on a kill that worked perfectly. `processExists` now reads the
  process state from `/proc` after the probe and reports a zombie as gone, which is
  what it is; where `/proc` is absent — macOS, the BSDs — the probe behaves exactly as
  before. A new test states that contract directly against a zombie made on purpose,
  so the property is checked everywhere rather than only where init is slow.

- **Removing someone from an application now takes their Starmap away on their next
  request.** The Starmap holds a 30-second reading of what this server is, so that
  twenty people with the view open cost the engine one reading rather than twenty. It
  decided who may see what on every request — but from that held reading, and the held
  reading included the application records themselves. An application's members are
  written on the application, so re-deciding against a record that is half a minute old
  gives the answer from half a minute ago: somebody removed from an application, an
  application sealed to private, an ownership transfer, an application deleted — in
  each case the person who just lost access kept receiving that application's processes
  for the rest of the window. What they received is the material the Starmap otherwise
  replaces with an anonymous placeholder: process names and ids, the call graph,
  running and finished instance counts, incident counts and ages, and the incident
  sites with their raw worker error text. The view's own auto-refresh reached the
  window without anybody doing anything, since an open tab re-polls on its own.

  The two stores a sharing scope lives in — applications and workers — are now read on
  every request, exactly as every other listing on this server reads them. Only
  structure that carries no scope stays cached, so the reason the cache exists is
  intact: the draft store with its diagrams and the walk of every deployed process are
  the expensive part, and they are untouched.

  Two things get better with it. An application or a worker you create or delete now
  appears on the Starmap at once instead of within half a minute. And which configured
  worker a task's `connector="…"` name points at is resolved per request, so a worker
  deleted a moment ago is no longer pointed at (ADR-0211 §7).

- **A Starmap left open against a server that is down asks once every five minutes
  again, not six times a minute.** The view backs off when the server will not answer,
  and the back-off was measured from the last *successful* read. During an outage there
  is no successful read, so the measured age only grew: past the five-minute ceiling
  every ten-second tick counted as due, and the view a page was left open on hammered a
  server that was already in trouble. It is measured from the last *attempt* now.
  The freshness line still counts from the last success, which is the number it is
  about — a failed attempt must never let a stale picture claim to be current.

- **Switching Drafts on the Starmap while it happens to be refreshing no longer undoes
  itself.** The view re-reads the landscape on its own, and that read and the Drafts
  switch's read could be in flight at the same time. If the automatic one landed second
  it overwrote the picture and put the switch back, with nothing said — the drafts
  appeared and then vanished under the hand that had just asked for them. The
  reader's answer wins now: an automatic read that lands after they asked for a
  different landscape is dropped, and the timer does not start one while the switch's
  own request is still running.

- **A diagram filed under no application no longer answers with silence.** A process
  deployed outside an application has no application, so it has no information model,
  so nothing about its data can be resolved — and until now the Modeler said so
  nowhere. The class field quietly became plain text, the class card and the "no such
  class" note never appeared, and the Problems panel read **"No problems"**, which is
  the one reading a person must not be given: a clean bill that was never earned looks
  exactly like one that was.

  Both now say what is happening. The panel names the reason and the remedy — open the
  process's draft from its application, or deploy it from there — and distinguishes it
  from the other reason the picker can be empty, an application that models nothing
  yet, because the two need different remedies. The Problems summary reads **"No
  problems found — data not checked"** when there is no model behind the diagram, so
  an empty list is never mistaken for a checked one.

  The field is also called **Class** now rather than *Type*: what it holds is the class
  from the information model, and calling it by that name is a shorter explanation than
  the paragraph underneath (ADR-0230).

- **A data object whose state a task advances is no longer treated as one the task
  writes whole.** An association with no assignment moves the object's data state and
  leaves its value alone, so it replaces nothing and hides nothing. The derivation read
  it as a whole-object write anyway, which withheld the member comparison from every
  class whose lifecycle is driven by state-only transitions — most of them. Shipped in
  the same release as the exclusion it defeated, and never released.
  ([ADR-0301](docs/adr/0301-derive-the-model-from-the-processes.md))
- **A class a process writes whole no longer fills the difference reading with work that
  is already done.** A write with no target path replaces a data object's entire value
  with whatever a FEEL expression evaluates to at run time, so none of the fields it sets
  can be read from the model. Derivation produced an empty member list for such a class,
  and the difference between built and planned read that silence as an answer: every
  member the model declared came back as *planned, not built*. On a real model one such
  write invented five of them — exactly the kind of false backlog item that costs the
  list its credibility.

  Derivation now records the fact it could not see inside, as a gap stated on that class,
  and the difference honours it: the member comparison is withheld and the exclusion says
  so by name, in the same place it lists. The *states* of such a class are still compared,
  because a data state is written on the object rather than inside its value, so a
  whole-object write hides none of them.
  ([ADR-0301](docs/adr/0301-derive-the-model-from-the-processes.md),
  [ADR-0310](docs/adr/0310-read-the-difference-between-what-is-built-and-what-is-planned.md))

### Added

- **A process document now shows the decision behind each business rule task.** The
  document already set a script task's source and a sequence flow's FEEL condition
  verbatim, under the rule that the prose says what a step is for and the code says
  what it runs. A business rule task is the one element whose behaviour lives entirely
  outside the diagram, and it was the one the document said least about.

  Each such section now carries what the diagram holds — the decision id, the binding
  and what it means, the result variable, and the inputs the task feeds in — and, below
  it, the decision's own rule table, drawn by the same renderer the decision document
  uses. The rules are read from the model behind the decision's reference where there
  is one, and otherwise from its deployment, with the document naming which. A task
  evaluated by a temis Worker says so and names the worker rather than implying it
  holds the rules; a decision that cannot be read costs its table, not the export.

  `GET /api/v1/decision-deployments/{key}/xml` is widened from `operator` to any
  signed-in identity for this, matching `GET /api/v1/processes/{key}/xml`, which is
  already open to any identity and carries strictly more.
  ([issue #919](https://github.com/pblumer/atlas/issues/919))

- **A decision is published as its own document, and two people can edit one together.**
  The last two things a diagram had and a decision did not.

  **Documentation.** A decision table is the business rule — the thing a compliance
  officer signs off and an auditor asks about — and it was readable only inside Atlas.
  The editor's `⋯` menu now publishes it as a structured PDF: the requirements graph,
  then every decision with its prose, the input data it reads with declared types, and
  its rule table set as a real table (hit policy, columns, one row per rule, each rule's
  own annotation below it). A decision whose logic is a literal expression shows the
  expression. Versions are numbered per decision, immutable, and shareable through a
  revocable public link — [ADR-0143](docs/adr/0143-process-documentation-export.md)'s
  design for a second artifact kind. The version line is about sign-off rather than
  about what is running: a business rule is usually approved *before* it is deployed,
  which is when the deployment record ([ADR-0319](docs/adr/0319-durable-versioned-decision-deployments.md))
  does not exist yet.

  **Co-editing.** A decision draft now holds a live session
  ([ADR-0140](docs/adr/0140-live-collaborative-modeling-sessions.md)): who else is here,
  what they are looking at, and a lock so two people cannot overwrite each other. The
  rule needed an answer dmn-js forced: a decision-table view is a grid, and a rule, a
  cell or a column has no id a session could name. **So the lock is the decision** — in
  the requirements graph that is literally ADR-0140's per-element rule, and opening a
  decision's table claims that decision. Two people can work on two decisions of one
  model at once; two cannot fill in one table together, and the editor says which it is.
  The session handlers are now parameterised by subject rather than copied, so a third
  artifact with a draft costs a binding rather than an implementation.
  ([ADR-0324](docs/adr/0324-decision-documentation.md),
  [ADR-0323](docs/adr/0323-co-editing-a-decision.md),
  [issue #919](https://github.com/pblumer/atlas/issues/919))

- **A decision can be tried against sample inputs, and a DMN model with no diagram now
  renders.** Two gaps closed in the decision editor, both of which made it a worse place
  to work than the diagram editor beside it.

  **Test.** A decision table is a program, and the first question its author asks is
  whether it does what they meant. Answering it meant saving the decision, deploying it,
  deploying a process with a business rule task that calls it, starting an instance and
  reading the result off it — five steps, three of them about processes, to answer a
  question about one table. The bar now carries **Test**: fill in the inputs, press Run,
  and see what came back together with the rule matrix saying which rules fired and why —
  the same matrix Operations draws for a decision a running process evaluated, because it
  is now literally the same renderer. The model tried is the one on screen, compiled for
  that one call and thrown away: no key, no record, no registry entry, nothing to clean
  up, and a decision that is stored nowhere yet can be tried like any other.
  `atlas_try_decision` exposes the same act over MCP.

  **A diagram for models that have none.** Almost every DMN model that reaches Atlas
  carries no `DMNDI` — an agent writing a decision table over MCP writes logic, not a
  picture, and so does temis, and so does a hand. dmn-js needs one to draw anything, so
  such a model opened in the editor showed a single box: the input data and the arrows
  between were silently absent, and the graph could not be seen or rewired. Worse, saving
  from that state wrote back a diagram covering only what had been drawn, so one visit
  to the editor left the model rendering worse than it was found. Atlas now completes a
  DMN model's diagram on the way to the editor, the way it has always done for a
  layout-less BPMN model, and **Auto-layout** in the new `⋯` menu re-flows the whole
  requirements graph on request. One generator serves both the editor and the read-only
  DRG viewer, so the same model is drawn the same way in both. **Export XML** is in that
  menu too.
  ([ADR-0326](docs/adr/0326-trying-a-decision-before-it-runs.md),
  [ADR-0325](docs/adr/0325-dmn-diagram-is-completed-on-read.md),
  [ADR-0124](docs/adr/0124-server-side-diagram-auto-layout.md),
  [issue #919](https://github.com/pblumer/atlas/issues/919))

- **A single decision can be deployed from its editor, and the editor says which version
  is running.** A decision reached the engine through one door: the application's
  **Publish**, which ships everything the application holds. An author who had just
  finished a decision and wanted to see it evaluate had to publish other people's drafts
  with it, give every other decision in the application a new version, and mint a release
  nobody had asked for. A single diagram has had its own **Deploy** since the beginning;
  a single decision had none.

  The decision editor's bar now carries **Deploy** beside the two save verbs, and a chip
  saying which version this decision is deployed at and under which key — the answer to
  "is what I am looking at what is running", which until now meant leaving for
  Operations. Deploy ships what is on screen through the very function an application
  publish calls: one durable record, written before anything is registered, carrying its
  own DMN source, versioned per decision id, and taking the `latest` pointer a process
  deployed afterwards binds to. Nothing about the storage model or the binding rules
  changes — this adds a caller to that path, not a variant of it.

  The three verbs stay distinct, which is the point: **Save** keeps your draft,
  **Save to model** writes what every reference resolves, **Deploy** changes what the
  engine evaluates. A decision that has never been written to the model can still be
  deployed — the record carries its own source — and the editor says plainly that no
  business rule task can name it until it is in the model. `atlas_deploy_decision`
  exposes the same act over MCP, and the deployed-decision listing is now readable by
  any signed-in identity, as the deployed-process listing already was.
  ([ADR-0322](docs/adr/0322-deploying-one-decision.md),
  [ADR-0319](docs/adr/0319-durable-versioned-decision-deployments.md),
  [issue #919](https://github.com/pblumer/atlas/issues/919))

- **A decision has a draft, so saving it is no longer the same as writing the model
  everything resolves.** The decision editor's Save wrote `eligibility.dmn` itself — the
  model the business-rule-task picker resolves, the model another application's
  reference may point at, and the model the next Publish ships. There was nowhere to put
  an unfinished decision, and pressing Save had consequences an author could not see:
  one half-typed FEEL expression refused a *colleague's* publish of that application,
  with a message about a decision they had never touched; a table whose output column was
  still called `result` offered `result` to the next business rule task that adopted it;
  and the first save of a second decision named *Eligibility* quietly became
  `eligibility-2.dmn` with its own reference, leaving two rows with the same name.

  The editor now carries the BPMN editor's pairing: **Save** keeps a draft — your work,
  which nothing else resolves — and **Save to model** writes the handle every reference,
  every picker and the next Publish resolve. A draft lives in a store of its own, filed
  into its application, and exists only while it differs from the model: writing the
  model clears it. A decision that has one is marked **Draft** in the application's
  artifact list, and a decision that has *only* a draft is listed as its own row saying
  it is not in the model yet, because a publish ships the model and does not carry it.
  **Discard draft** goes back to the stored model.

  Writing the model no longer forks a copy either: a handle another decision already
  holds is refused, named, and offered as a deliberate replacement, the same rule drafts
  and forms have had since ids became identity. An import, a source-tree apply and the
  MCP authoring tools are untouched — they never claimed to be editing one decision, and
  keep the plain upsert.
  ([ADR-0321](docs/adr/0321-decision-drafts.md),
  [ADR-0222](docs/adr/0222-artifact-id-renames.md),
  [issue #919](https://github.com/pblumer/atlas/issues/919))

- **A data object's state is on the diagram, and says whether anything acts on it.** A
  `<dataObjectReference>` carries a data state — the `[ARCHIVIERT]` BPMN writes under
  the box — and Atlas has read it end to end since ADR-0053: the compiler interns it, the
  engine advances the object into it, and the Operations replay shows every transition.
  The one place it was missing is the place a model is read. bpmn-js parses `<dataState>`
  and draws nothing with it, and the properties panel has been able to *edit* the state
  all along, so a diagram could carry a lifecycle no view showed. In the identity example
  that meant six boxes reading `identitaet` and nine reading `services`, identical to the
  eye, with the one thing that tells them apart held back in a side panel.

  The state is now written under the object's name, in square brackets, on the Modeler
  canvas and in all four read-only views. It rides under the *label* rather than the
  symbol, so it stays with the name wherever an author drags it, and it follows the name
  live as the state is typed, cleared or undone.

  **It is drawn with its role, because the same string means two different things.** A
  state on a box a write points at is the target state the compiler puts on the
  `DataOutputAssociation`: the engine advances the object into it, the transition lands
  in the log with its attribution, and `CheckDataFlow` matches it against the class's
  lifecycle (ADR-0259). A state on a box that is only *read* is dropped — "its state
  ignored on a read" — so it never reaches the compiled model, no engine acts on it, and
  no check can reach it, not even the typo check that exists for exactly this mistake.
  Drawing both the same way would have the diagram claim something the model does not do,
  so the second is set back and its hover title says why. Same notation, same place, one
  of them quieter — which is the honest rendering of what Atlas will actually do with it.

- **The decision editor is a page of the Modeler, not a window over one.** A decision
  used to be edited in a modal overlay. That fitted what a decision was when the editor
  was built: a reference to a model file some process happened to use, stepped into from
  the business-rule-task picker and stepped back out of. Since a decision became a
  durable, versioned artifact published in its own right, an overlay costs four things a
  page gives for nothing — a decision had no address to bookmark or send, the browser's
  back button dismissed the editor and dropped the edit, saving was indistinguishable
  from publishing the model every reference resolves to, and publishing was somewhere
  else entirely.

  A decision is now edited at `#/modeler/dmn/new` or `#/modeler/dmn/e/{ref}`, in the
  chrome the BPMN and form editors wear: a breadcrumb back to the application by name,
  the same tab strip (the DRG overview and each decision's own table), a model-handle
  chip, a status line and **Save**. Save stays on the page and moves the URL onto the
  decision it just wrote, so a second Save updates it rather than creating a second one.
  The labels are English, like the rest of the Modeler — the overlay was German only,
  and so was the starter model it seeded.

  **Authoring a decision from a business rule task still takes one button.** It now
  leaves the diagram instead of covering it: the diagram is saved as a draft first (the
  rule the call-activity drill-down already used), and what the editor saved is adopted
  by the task on the way back — decision id, input mappings and result variable filled
  in, exactly as before. A deployed definition opened read-only has no draft to return
  to, so it asks before leaving and the decision is picked afterwards.
  ([ADR-0320](docs/adr/0320-the-decision-editor-is-a-page.md),
  [issue #919](https://github.com/pblumer/atlas/issues/919))

- **A DMN decision is a durable, versioned deployment artifact, and a deployed process is
  frozen to the version it was deployed against.** A decision used to exist only as a
  model bundled into some process's deployment. An application whose only artifact was
  `eligibility.dmn` therefore published *successfully* and deployed nothing at all — the
  bundle deploy iterated BPMN drafts and collected the models those drafts referenced, so
  with no draft there was no loop iteration, no registry entry, and nothing on disk. After
  a restart there was still nothing.

  Publishing an application now deploys its DMN models as **decision deployments**:
  durable records in a new `decisions/` store, keyed from the same definition key space
  process definitions come from, versioned per decision id, and carrying the validated DMN
  source plus its checksum. No compiled temis structure is persisted — the registry is
  rebuilt by compiling the stored source again at startup, off the processor and before
  the loop serves traffic. `GET /api/v1/decision-deployments` lists them and
  `.../{key}/xml` serves the exact source a running process evaluates, which is not the
  same thing as the model file behind a handle: that file is edited in place.

  **`latest` binding is now resolved when the process is deployed, not when a token
  arrives.** It was a lookup on the worker against a pointer every deploy overwrote, which
  meant publishing a new decision silently changed the behaviour of processes already
  running — and meant a version was being chosen outside the log, which a replay has no
  way to reproduce. A deployment now resolves each `latest` reference once, to the newest
  decision deployment providing it (or, when the decision was never published on its own,
  to the model bundled with the process), and stores the answer in its record. The runtime
  makes no version choice at all, and neither does recovery.

  `deployment` binding is unchanged. **Deployments written before this keep their old
  behaviour**: a record with no binding-policy marker still resolves `latest` at
  activation, exactly as it was deployed to, and nothing on disk changes meaning under an
  upgrade. Redeploying the process is what moves it to the pinned policy.

  An application release now names the decisions it shipped alongside its processes, and
  an application can be built from decisions with no BPMN in it at all — "Create new →
  Decision (DMN)" authors one in the embedded editor and files it under the application.
  ([ADR-0319](docs/adr/0319-durable-versioned-decision-deployments.md),
  [issue #915](https://github.com/pblumer/atlas/issues/915))

- **A capability's service levels are measured, not only declared.** Every KPI and SLA
  on a business capability was prose the API labelled as a declaration, because nothing
  computed one. `GET /api/v1/capabilities/{key}/measurement?windowDays=N` now returns,
  per realising process, how often each end event fired, how often a token was
  cancelled, the cycle time over the window, and each declared SLA's attainment.

  **The window is required, and that is a measured finding rather than a preference.**
  The decision record behind the register carried an open question — whether this is
  computable at volume without the OpenSearch exporter, which not every installation
  runs — and required that it be answered by measurement. It was. The per-element
  counters are flat: a thousandfold population leaves them in microseconds, and at
  10 000 instances the outcome distribution is *faster* than at 1 000. The instance walk
  is linear, costing 1.24 seconds over 100 000 finished instances. So an unbounded
  reading is not offered: `windowDays` is required and at most 400, which is generous
  enough for an annual SLA and small enough that seconds of waiting cannot be asked for
  by accident. The exporter is an optimisation for unbounded historical analysis, not a
  prerequisite.

  **The response mixes two kinds of number on purpose and says which is which.** The
  counts come from maintained counters and are all-time — a counter holds a total, not
  a series — while the cycle time is windowed. Both are integers on a screen, so the
  body carries a sentence for each basis rather than leaving a client to assume.

  **An SLA is measured only where it carries a number.** The new optional
  `thresholdSeconds` sits beside the prose threshold rather than replacing it: "within
  five business days" is what the business agreed, and no parser should decide what a
  business day means at your installation. One without it is listed under `notMeasured`
  with the remedy — and every KPI is listed there too, because which recorded figure
  "disburse within three days" refers to is a judgement, and a guess would put a number
  somebody acts on under a name nobody authored.

  Two kinds of absence stay distinct, as in the gap report: a realisation you may not
  see is restricted, one this server does not deploy is not deployed, and neither is
  zero-filled. An SLA over a window that held no case is not 100% attained and not 0%.

  This is the one read in the area that runs off the run loop, because it is the one
  whose work grows with the instance population.

- **Per-phase duration was measured and left out, for a different reason than
  expected.** It went into the measurement as the candidate for omission, on the
  reasoning that its cost scales with the length of the process while cycle time's does
  not. A second benchmark axis — the same population over processes of 1, 10 and 30
  tasks — refuted that: a thirtyfold longer process costs it 1.4× more, and its ratio to
  its own control *falls* from 2.4× to 1.9×. Within an instance the cost is the seek to
  the prefix, not the walk under it.

  So it is not omitted for cost. It is omitted because a phase is a span between two
  points a reader names, and the register has no field naming them; offering the
  duration between two element ids a caller passes in would be a process-analytics
  endpoint wearing a capability's name. The cost question is settled and the modelling
  question is not.

- **Atlas now reads the difference between what your processes build and what your model
  plans.** [ADR-0301](docs/adr/0301-derive-the-model-from-the-processes.md) settled that
  Atlas holds two statements about the same subject and must not merge them: the derived
  model is what is *built*, the authored one is what is *wanted*, and their difference is
  the work not yet done. It then stopped, because it could not settle the shape and
  because it named a blocker — a comparison "needs a stable identity for a derived class
  across two derivations, which nothing yet provides".

  That blocker belonged to a *reconciliation*, which has to remember which change you
  rejected last time. This reading remembers nothing: both sides are computed fresh and
  compared by name, so there is no identity to keep across anything. And the names are
  already the mechanism — `itemSubjectRef` resolves a class by name, a write path names a
  member, and a lifecycle state's name **is** its identity because it is the string every
  process writes.

  **Data → Planned against built** shows two lists, never blended, because a reader acts
  on them differently. *Planned, not built* is in the model and in no process: the
  backlog, a decision taken and not yet implemented, and explicitly not a defect —
  `data.unreachable-state` already reported exactly one case of this, and this generalises
  it to members, states, transitions and whole classes. *Built, not described* is in the
  processes and in no model, which usually means write it down and occasionally means a
  process is doing something nobody agreed to.

  **What it never compares is the half that makes it trustworthy**, and it is said where
  it lists rather than in a footnote: the business key, attribute types and multiplicity,
  which states are final, associations and documentation. Derivation cannot see any of
  them ([ADR-0301](docs/adr/0301-derive-the-model-from-the-processes.md) §2), so a
  difference there would be a fact about derivation rather than about your system — and
  every one would sit on every class for ever. A short list is therefore not a clean bill,
  and the screen says so.

  Two more silences for the same reason. An «enumeration» is never reported as unbuilt: it
  is machinery of the model — an attribute's type, or the states a lifecycle takes
  ([ADR-0306](docs/adr/0306-a-lifecycle-may-take-its-states-from-an-enumeration.md)) — and
  no process carries one. And an application that models nothing produces no findings at
  all, rather than a wall of rows that are only the absence of a document nobody has
  started.

  Also readable as `GET /api/v1/infomodel/difference?applicationId=…` and as the MCP tool
  `atlas_model_difference`. Nothing is written to either model.

- **A drawing and the capability register are now one architecture.** Panorama holds an
  architect's ArchiMate model; the register holds what has to be done, with an owner, a
  scope and SLAs. Draw *Underwrite a loan*, file a capability keyed `loan-underwriting`,
  and nothing connected them but the fact that somebody wrote a similar phrase twice —
  and renaming either end lost even that, silently and in the direction of still looking
  right.

  Two binding keys close it: `atlas.capabilityKey` on an ArchiMate `Capability` and
  `atlas.valueStreamKey` on a `ValueStream`. They are ordinary ArchiMate properties, so
  a bound model stays a standard model and the binding travels with it into any
  conformant tool. What travels is the record's **key** and nothing else: the name is
  resolved by the server on every read, so a drawing cannot go stale about the register,
  and a binding whose record was deleted reads as *missing* rather than as a name that
  quietly stopped matching.

  The key rather than an opaque id, which is the opposite of every other binding here.
  Those carry an id because the resource's own name is mutable; a capability's key is
  not — it is the filename on disk, it is not renameable in place, and it is what an
  export carries — so it is the stable identifier the rule asks for.

  **Each key is refused on the other's element.** A `Capability` and a `ValueStream` are
  both strategy-layer behaviour elements binding a key from the same register, which
  makes them the pair a later edit is likeliest to treat as interchangeable and the pair
  where doing so would be least visible: both keys would still resolve, against a
  register holding both.

  Every signed-in caller may resolve one, unlike every other binding, and that is the
  register's own rule rather than a shortcut. A capability says what the organisation
  must be able to do and nothing about what this server runs. What *is* scoped are the
  processes it names as realisations, and those are resolved elsewhere, through their
  own sharing scope.

- **A value stream is an element you can draw.** ArchiMate's `ValueStream` was accepted
  by Panorama's validator and absent from its palette, so a model containing value
  streams could be opened, edited around, and never added to — the worst of the three
  states an element can be in, because reading works and nothing looks broken.

  It is authorable now, on the strategy layer with a behaviour aspect, where the
  standard puts it and where `Capability` already sat. The relationship matrix is
  predicates over layer and aspect rather than a table of type pairs, so it inherits
  exactly the rules a capability has and none were touched; a test holds the two to that
  equivalence across every relationship and every partner, in both directions.

- **A write into a data object now offers the members its class declares.** A data
  output association writes one member of a structured object — `customer.name`
  ([ADR-0060](docs/adr/0060-data-object-write-paths.md)) — and the path was free text.
  `customer.nmae` deploys, runs, and writes a member nobody will ever read. The class the
  object's type points at already declares what its members *are*, so the field now asks
  the same question the class picker and the data-state picker ask, the same way: a list,
  with an escape for a member nothing models yet.

  Each entry carries what the model says about it — the type, the multiplicity where it
  is not one, and the key mark on an attribute that is part of the business key. Where a
  member's own type is another class in the model, that class's members are offered one
  level down as `customer.name`, because a dotted path is exactly the case where the
  first segment is structured and something else says what is inside it. One level and no
  further: below that the model repeats itself, and a picker that walks it forever is one
  nobody can read. An **untyped** member offers nothing inside it, because nothing knows.

  A path the class does not declare is kept and named rather than dropped — a diagram is
  routinely drawn before the model catches up — and it comes back in the list saying it
  is not a member of that class, instead of looking like any other entry.

  **What this is not:** a data object is not a process variable. Nothing binds one into
  the FEEL scope, so these members say what the write *target* is shaped like and nothing
  about what the expression above them can read. The panel says so where it matters,
  beside the field that takes a FEEL expression, because a member list read as a variable
  list is exactly the wrong lesson to take from it.

- **A milestone is an element you can draw now.** BPMN's marker for a point on the path
  where no work sits is a **none intermediate throw event**: an intermediate throw event
  with no event definition, named after the point it marks. *Identity verification
  started* is one — the work is what follows it, so there is no task there to record.
  Atlas refused it, and refused it at Deploy rather than at author time: the Modeler drew
  one, validated it and said nothing, because the element was never in the list of things
  bpmn-js can draw that the engine cannot run.

  It compiles. It waits for nothing and needs no worker, so its execution is the same as
  having drawn nothing at all — and that is not what it is for. What it produces is the
  record: the per-definition visit counters count it, the instance's step trail carries it
  in order, and the Operations overlay lights it up. That is the whole difference between
  a milestone and a label on a sequence flow, and it is what makes "when did this case
  reach verification" answerable per case rather than only where a task happens to sit.

  It is a node type of its own rather than a reused undefined task or link throw, because
  everything that reads a compiled node back reads its type — the overlay, the step
  replay, the process documentation, a migration plan matching elements across versions.
  A milestone stored as a task would be drawn and described as a task.

  **Making the empty case compile did not make the wrong case compile.** "No event
  definition this compiler implements" and "no event definition at all" used to be one
  state, and both were refused; with the second one compiling, the first would have become
  a pass-through that silently does nothing the model asked for. A throw event carrying a
  timer — which BPMN allows only on a catch — would have run straight through instead of
  waiting. So an unmatched `*EventDefinition` child is now refused by name, and by its
  suffix rather than by a list of the five that are wrong today, because such a list goes
  stale silently and in the direction of accepting something.

- **A capability record now says when somebody last read it and meant it.** The gap
  report checks a realisation against what is deployed, because that is a fact Atlas can
  see. The rest of a capability — who owns it, what it is and is not responsible for,
  what it has promised — is prose about people and promises, and Atlas took all of it on
  trust. A map whose realisations are green and whose owners left two years ago is worse
  than no map: it is confidently wrong in exactly the fields somebody escalates against.

  Both records now carry a confirmation: when, by whom, who they asked, and one line on
  what the review found. `POST /api/v1/capabilities/{key}/confirmation` is the only
  thing that sets it, and creating a record counts, because writing something down is an
  assertion.

  **No edit sets it** — not even one that rewrites the owner or an SLA. If saving
  refreshed the date, fixing a typo in the summary would assert that every field had
  been re-checked, which is precisely the lie the mechanism exists to prevent, made
  automatic and leaving no diff in which anybody could have noticed it. For the same
  reason there is no bulk confirm.

  The confirmation also records **who was asked**. The confirmer is almost never the
  owner, because the owner is free text precisely to accommodate people with no Atlas
  account — so without that field the map confirms itself and a reader cannot tell that
  from a review the owner sat in. Leaving it empty is a legitimate confirmation and a
  weaker one, and the record says which. A self-confirmation is shown beside it and
  never reported: in a four-person installation the architect is the only person who
  *can* confirm, and a report that fires on the normal case stops being read.

  A confirmation stays fresh for twelve months, the interval this repository already
  uses for the two other things it dates and cannot verify. Unlike those, it is
  configurable — `PUT /api/v1/settings/confirmation`, admin only — because those govern
  content here and this governs a customer's map reviewed on their own cadence. Setting
  it to something nothing outlives does silence the check, and that is allowed and made
  legible instead: it is one visible number, and every report says which interval it
  applied.

  What lapses becomes two new gap findings and a `?stale=true` listing — the review
  backlog, the exact twin of `?realized=false`, the automation one. A stale record is
  flagged everywhere it is read and never withheld, because hiding it would make the map
  least useful at the moment it most needs attention. Both are also MCP tools, whose
  descriptions say in as many words that only what was actually re-read may be
  confirmed.

  Nine of the report's ten findings are facts Atlas checked. These two are not, and the
  report does not pretend otherwise: the only honest thing it can say about prose is
  that nobody has stood behind it lately.

- **Atlas now holds what the organisation must be able to do, not only what it runs.** A
  deployed process could be found by its name and by nothing else: not by the business
  capability it realises, not by who owns that capability, and not by what would stall
  without it. The answer to all three lived in a slide deck, if anywhere.

  Two design-time records close that, following the business architecture of Ruecker
  and Strauch's *Enterprise Process Orchestration*. A **business capability** says what
  has to be done, independently of how — its scope (including what it is explicitly
  *not* responsible for), its input and output, its business owner, the resources it
  draws on, what it requires from other capabilities, and the KPIs and SLAs it is held
  to. A **value stream** is the ordered activity that meets a customer need, its stages
  naming the capabilities that perform them.

  A capability says how it is currently done in one of four ways: an executable process
  here, a Worker, a purchased system, or a person. The last two are the point. A map
  that could only record what Atlas already runs would tell you nothing the deployment
  list does not, and `GET /api/v1/capabilities?realized=false` — everything nothing
  currently automates — is the adoption backlog the whole thing exists to shrink.

  Nothing about a realisation is stored beyond a portable key. Whether the process still
  exists, at which version, with how many instances running, is resolved every time you
  read, so the record cannot go stale about the installation. **Coverage** answers that
  for one capability, along with what it depends on, what each of those has promised, who
  depends on it, and which value-stream stages it performs.

  The reverse direction is computed and never stored. `GET
  /api/v1/business-architecture/gaps` compares the map against what this server actually
  runs: capabilities nothing realises, realisations pointing at what is not here,
  deployed processes no capability claims, stages with no capability, dependencies naming
  no capability, and — the one worth the most — a call activity crossing from one
  capability's process into another's that the caller never declared. It is a comparison
  and never a merge: the method's black box is normally a service task, so a declared
  dependency with no call activity is the ordinary case and raises nothing. Two things it
  refuses to report: a purchased system or a person, which Atlas cannot see and will not
  call a defect, and anything outside your sharing scope, which reads as restricted
  rather than missing — with a count, so a clean report can be told from a blind one.

  Capabilities are a **flat, tagged list**, and the record has no parent field. That is
  the method's own advice and it is now structural: an "end-to-end" capability is
  regularly invoked from inside another one, so any tree is wrong from some direction,
  and a test asserts the field's absence rather than a comment asking for it. There is
  one identity, the key, and it is also the filename — the map reads on disk as
  `capabilities/loan-underwriting.json` and diffs like source. The price, stated in the
  refusal that enforces it, is that a key cannot be renamed in place.

  Both stores are design-time, so the existing export and restore already carry the map
  between installations. The whole surface is available as MCP tools as well, because an
  agent that deploys a process has no other way to say what the process is for.

  Every KPI and SLA in the registry is a **declaration**. Atlas computes none of them,
  and the coverage answer says so in a field rather than letting a client render a goal
  as an achievement. The data to compute them is already there; whether it can be
  aggregated at the volumes this is aimed at is an open question the decision record
  carries, and measurement is a separate slice.

  The method and how to work it are in `docs/architecture/business-architecture.md`,
  including two things checking it against the tree turned up: a **none intermediate
  throw event** — the method's milestone marker — does not compile, and Atlas's
  Prometheus surface is operational rather than business-level, so a KPI dashboard
  planned against `/metrics` will not find what it needs.

- **A lifecycle can now take its states from an «enumeration» you already wrote.**
  [ADR-0259](docs/adr/0259-data-object-lifecycle.md) gave a class a state machine, and it
  was written against a real model that already had one — drawn as an enumeration. That
  model is the whole problem: its author had written the five states of an identity as
  literals, with a paragraph of documentation on each, *because that was the only place
  the states could be written down at all*. Adding a lifecycle beside it made the model
  say the same five strings twice, with nothing connecting them and nothing noticing when
  they drifted.

  A business object's lifecycle now names an enumeration in the same model, and that
  enumeration's literals **are** its states. The name of a state is written in one place.
  Everything an enumeration cannot hold stays on the lifecycle, which is most of what a
  lifecycle is for: which state instances are created in, which end the life, what may
  follow what, and where each sits on the canvas.

  Renaming a literal renames the state and rewrites every transition that names it —
  which is exactly what renaming a state already does, because a state's name *is* the
  string every process writes. Removing a literal removes the state and the arrows
  touching it. Adding a state on the lifecycle sheet writes the literal, since that is
  where the names live. On a lifecycle fed this way the state's name is shown read-only
  and says where it is renamed, rather than taking an edit and dropping it.

  **The class diagram finally shows the tie**: a dashed `«lifecycle»` line from the class
  to the enumeration. It is derived from the reference and never drawn by hand — the same
  construction as a data store's line to its class, for the same reason. A class and an
  enumeration do not *relate*; one *takes its states from* the other, so the relationship
  rules are untouched and nothing that counts relationships counts it.

  The server refuses the three ways a document can contradict itself here: a reference to
  a class that is not there, a reference to something that is not an enumeration, and a
  state the enumeration does not declare. A literal with no state yet is *not* refused —
  a machine half drawn is the normal condition, and that is incompleteness rather than a
  contradiction. A lifecycle that names no enumeration behaves exactly as it did before.

- **General-purpose scripts now have an opt-in, fail-closed OS sandbox.**
  `--script-sandbox=strict` (or `ATLAS_SCRIPT_SANDBOX=strict`) gives every
  PowerShell, Python and JavaScript execution private scratch, restricts file reads
  and execution to the installed runtime with Linux Landlock, and denies creation
  of network and Unix-domain sockets with seccomp. Atlas checks for Landlock ABI 3+
  before starting a strict server or worker; it never silently falls back. The
  initial default is `off`, deliberately, so upgrading does not break deployed
  scripts that intentionally use mounted files or services. Independently of that
  setting, a script timeout on Unix now kills the interpreter's complete process
  group, so a spawned child cannot survive its timed-out parent.
  ([ADR-0303](docs/adr/0303-script-sandbox-isolation.md))

- **The Console landing page says what Atlas is, in both languages**: the dashboard
  opened on "Welcome to Atlas" and three steps — it told a newcomer what to click, not
  what they are running. A **Key features / Kernmerkmale** tile now sits below the
  dashboard's own tiles: sixteen short entries (one binary, durability, the compiler,
  throughput, the Modeler, token visibility, Panorama, human work, DMN, the information
  model, checkable BPMN coverage, integrations, agents, operations, deployment, licence), collapsible and carrying the
  same EN/DE toggle as What's New. The copy is a static asset
  (`api/web/key-features.json`, guarded by a test) rather than markup, and the landing
  page's two bilingual sections now share one language setting, so it is never half
  English and half German.

  A tile that enumerates what a product *is* goes stale the way the handbook's
  screenshots do — silently, because the page still renders and the capability nobody
  mentioned is simply absent. So the file carries a `reviewedThrough` marker naming the
  newest `### Added` bullet it has been held against, and `go test ./api` fails while
  bullets sit above it. The question a feature has to answer is one line long — does
  this change what Atlas is? — and the usual answer is no, which moves the marker and
  writes nothing. What the marker buys is that it is asked by the person who knows the
  feature rather than by nobody.

- **The information model can now be read off the processes instead of typed in beside
  them.** [ADR-0230](docs/adr/0230-process-information-model.md) and
  [ADR-0259](docs/adr/0259-data-object-lifecycle.md) both run in one direction: a person
  models the vocabulary, and the processes are checked against it. Neither record priced
  what that puts in front of the first user, which is a blank page — until classes exist,
  the Modeler's class picker is empty, the data-state field is free text, and the Problems
  panel reports nothing because there is nothing to report against. Meanwhile the engine
  already knew most of it: a data object declares a name and often a type, every data
  output association names the path it writes (`customer.name`), every data object may
  carry a data state, and the compiled graph already says which writes can follow which.

  **Data → As built** draws what an application's processes actually carry. A class per
  data object, named by its `itemSubjectRef` or, failing that, by the object itself; a
  member per write path; and, per class, the state machine its data states imply, with a
  transition wherever the compiled graph says one write can precede another. It is a read
  over the newest active version of each of the application's processes — the same set
  the deploy checks assemble — so it costs a request and no storage, and it is drawn on
  the same two canvases the authored model and the run-time overlay use, in their
  read-only mode.

  The two readings are deliberately different statements rather than two copies of one.
  What is derived is what is **built**; what somebody models by hand is what is
  **wanted**. Neither is written into the other, because the point is not to make them
  agree — their difference is the work not yet done. A `cancelled` state in the model that
  no process ever writes is a backlog item, which is the same fact `data.unreachable-state`
  reports from the other side.

  What derivation cannot see is said above the drawing rather than under it, because a
  derived picture mistaken for a complete one is worse than no picture. No derived class
  carries a business key — nothing in BPMN says which attribute identifies a thing, and
  it is the one fact every cross-process capability rests on, so it stays the first thing
  to add by hand. Attributes are untyped, since a FEEL expression's result type is not a
  static fact of the model. Per class it also says when the name came from the data object
  rather than a declared type, which is the case most likely to be spelled wrongly, and
  when a dotted write path proved a member has members of its own that nothing in BPMN
  names. Nothing on the view is editable: it is evidence about the processes, not a
  document about the business.

  Also readable as `GET /api/v1/infomodel/derived?applicationId=…` and, for agents, as the
  MCP tool `atlas_derived_information_model`.

- **A lifecycle that is ahead of the processes that write it now says so.** A class can
  declare that an order may be `cancelled`; whether anything ever cancels one is a
  question about the *application*, not about any one process, so it could not be asked
  where the other two lifecycle checks live — `CheckDataFlow` reads one compiled process
  at a time, and "nothing ever writes this" is false until every process has been looked
  at. Asking it there would mean either passing the other processes into a per-process
  check, where the same finding repeats once per process and is attached to whichever one
  happened to be deployed, or answering it wrong.

  It is asked once, of the set: the newest version of each of the application's
  processes, minus the deactivated ones, plus whatever is being deployed or drawn right
  now — so the process that finally cancels an order clears the finding as it arrives
  rather than one deploy later. The result is one sentence per class naming every state
  nothing reaches, carrying no element, because it is a fact about the model rather than
  about any element of any process. Like its two siblings it is a warning and refuses
  nothing: a lifecycle is routinely drawn before the process that will write it.

  The state instances are created in counts as reached, since every instance begins
  there — so a data object that carries no data state at all leaves the lifecycle's own
  starting state unreached, which is worth saying because the remedy is one field in the
  Modeler. A class no process handles is not reported at all: that is a lifecycle drawn
  before its processes, which is the normal order of work rather than a defect
  (`data.unreachable-state`, ADR-0259).

- **An OpenAPI document can configure the task that calls it.** `atlas openapi-template
  --spec petstore.yaml --out ./packages` writes one element-template package per
  operation, in the shape the repository catalog already uses
  ([ADR-0300](docs/adr/0300-openapi-element-templates.md)).

  It is the reader behind `atlas mock-openapi` pointed the other way: the same document
  that makes an API answer now also fills in the task that calls it. Method is fixed to
  the operation's; the URL is the document's server plus the path, literal where there
  is nothing to substitute and a FEEL expression where there is —
  `="https://api.digitalocean.com/v2/droplets/" + string(droplet_id)` — with the
  description naming each variable the process must hold. A URL that carries no host is
  called out, including the relative-server case (`/api/v3`) that looks filled in and is
  not.

  What the document cannot decide stays empty: headers, authentication and the
  credential reference. Security schemes are deliberately not mapped onto Atlas's auth
  types, because the useful ones need a token endpoint and a client id that live on the
  server, and a guess there is a wrong answer wearing a filled-in field.

  **What you can do with the result today is limited, and the command says so where it
  writes them.** Applying a template to a task is
  [ADR-0212](docs/adr/0212-element-template-applier.md), which is not built, and a
  running server's catalog is compiled in — so these are files to commit or to keep,
  not to install.

- **An instance's data objects are drawn on the lifecycle their class declares.** The
  state trail was already on disk — every durable write, with the element that made it
  — and the state machine was already in the information model. Nothing read them
  against each other, so the question *where has this order got to, and what moved it
  there* meant reading a list of writes and holding the machine in your head.

  The replay's **Data** tab gained a third reading beside List and Diagram. It draws
  the whole declared machine — including the ways out this instance never took, because
  a picture of only what happened answers a different question — with the states this
  datum has been through filled in, the one it is in now ringed, and each edge it
  travelled carrying the BPMN element that moved it along. That last part is the thing
  no class diagram can say, and the reason to draw this rather than list the trail.

  The half worth the whole feature is what it says when something is wrong. A move the
  machine does not join is drawn as what it is — dashed, apart, and never mistakable
  for something the model says — and named in words underneath with the element that
  made it. A state the class never declared is named too, since it cannot be drawn.
  Together they are the run-time twin of the `data.illegal-transition` and
  `data.unknown-state` deploy checks, and they catch what those cannot see: an instance
  that started before the lifecycle was drawn, and a process the check never ran
  against. Where two transitions join the same pair of states the trail cannot tell
  them apart — it records states, not transition ids — so both are marked and the
  panel says so rather than picking one.

  This adds no event, no record type and no migration, and touches nothing in
  `applyToState`: it is a read over what the log already said. `GET
  /api/v1/instances/{key}/lifecycle` serves it, and `atlas_instance_lifecycle` puts the
  same answer in front of an agent (ADR-0259).
- **The deploy says when a searchable declaration cannot be honoured.** The Modeler marks
  such a name while it is typed, but a model deployed from a pipeline or over the API
  never passes through the Modeler, and `atlas:searchable` is accepted whatever it names:
  the search then stays empty forever with nothing saying why. The deploy response now
  carries the same reading, beside the worker and namespace warnings it already gives —
  never a refusal, because a model is routinely deployed before the rest of its world
  exists. It reads the model's own bytes rather than the compiled process, so it counts
  writers generically, by attribute: every Worker Type, script and decision writes into a
  `resultVariable`, including the kinds added after this was written. A name the model
  itself declares as a JSON start variable is reported for any process, because the
  declaration settles it. A name nothing in the model produces is reported only where the
  model has stated its inputs — it declares start variables and links no form, whose
  fields are a separate resource this cannot read — and the sentence says plainly that a
  worker's own output or a write through the variables API makes it fine.

- **A searchable declaration that indexes nothing now says so.** `atlas:searchable` names
  variables, and nothing checked that the model writes any: a typo, or a name holding
  JSON, is accepted by the deploy and then answers an empty search forever, with no screen
  saying why. The field now paints a chip per declared name, read against the same static
  analysis the Variables panel uses. Red where the model settles it — a repeated name the
  deploy refuses, or a name the model itself says holds a structured value, which the
  index cannot hold. Amber where it is a question rather than a verdict: nothing in the
  diagram writes that name, which is usually a typo but not always, because a worker's
  output or the variables API can write a name the diagram never mentions. Each chip
  carries the reason as its tooltip, and they are painted as the name is typed.

- **A migration now re-indexes what its target declares.** [ADR-0244](docs/adr/0244-searchable-variables.md)
  argued that a declared searchable variable needs no backfill, and for the case it looked
  at that holds: the attribute postdates every definition that could lack it. It missed the
  one way an instance changes version after it has written values — migration
  ([ADR-0162](docs/adr/0162-process-instance-migration.md)). An instance started on a
  version that declares nothing and migrated onto one that declares `identityId` held a
  value stamped "not indexed", so the version-scoped search — which for a declared name is
  answered from the index alone — returned nothing for an instance the engine was holding.
  A wrong answer, not a slow one, and a silent one.

  A migration now emits one membership correction per variable whose answer differs under
  the target's declaration, in both directions: a name the target declares and the source
  did not is added, one it no longer declares is dropped. The comparison happens at command
  time against the compiled process — the fold cannot ask one anything, which is ADR-0244's
  own finding — so what reaches the log is the answer, and a replay rebuilds the identical
  index. A migration between two versions that declare the same names emits nothing.

  For the instances migrated before this,
  **`POST /api/v1/processes/{key}/reindex-instances`** (admin, `?limit=`, default 500, max
  5000) queues the same correction for a bounded batch of a definition's running instances
  and reports what that definition declares. It is idempotent: an instance already in step
  emits no events at all, so running it twice writes nothing the second time. Running
  instances only — a finished instance's membership can no longer change through any normal
  path, and reaching into the history family from a command handler was not worth it for a
  strictly historical case.
  ([ADR-0295](docs/adr/0295-migration-reindexes-searchable-variables.md))

- **The Modeler can now say what a process is found by.** `atlas:searchable`
  ([ADR-0244](docs/adr/0244-searchable-variables.md)) turns an operator's value search
  into a seek, but it shipped as an attribute with no field and no moddle property, so
  the only way to declare a searchable variable was to hand-edit the exported XML
  outside the tool. The process properties now carry a **Searchable variables** field
  beside the two TTLs, validated the way they are: a nameless entry or a name given
  twice is what the deploy refuses, so the panel says so while authoring and still
  stores what was typed rather than dropping the author's value.

  What was missing was never the round trip — moddle keeps an attribute it has no
  property for in `$attrs` and writes it back, so a hand-authored declaration was
  invisible rather than lost. It was that nothing could *read or write* it: the panel
  reads `rootBo.searchable` and writes through `updateProperties`, and both go through
  moddle's properties. A drift test now fails for any future `<bpmn:process>` attribute
  the compiler reads that `atlas-moddle.json` does not declare, so the next one cannot
  ship unauthorable (`api/moddle_drift_test.go`, `e2e/searchable-modeler.spec.mjs`).

### Changed

- **The class canvas's palette is drawn in the notation now, not in Unicode.** Its marks
  were characters — `▭` for a business object, `▢` for a value type, `☰` for an
  enumeration, `◇` and `◆` for the two kinds of whole. That was a defensible trade when
  there was nothing to vendor: bpmn-js ships an icon font for BPMN's shapes and there is
  no UML equivalent, and four kilobytes of font for eight marks buys little. What it cost
  was that a palette entry looked like whatever the reader's system had for that
  codepoint, and that the three classifiers were three near-identical rectangles.

  Each entry is now a miniature of the shape the click produces, drawn as inline SVG in
  the stylesheet. No font, no image files, nothing to fetch — the same reasoning that
  keeps the canvases buildless ([ADR-0012](docs/adr/0012-web-ui-app-shell.md)). There is
  no official UML icon set to take: the standard fixes the shapes on the *diagram* and
  says nothing about a toolbar, so the miniatures are drawn from the notation itself.

  The entries split in two, and the split is what each entry *is* rather than a
  preference. A classifier is a button — one click adds one, it has no state — so it
  carries its kind in colour: a business object with the key knocked out of its name
  compartment, because identity is what makes it one; a value type with that compartment
  empty, because nothing identifies it; an «enumeration» whose body is a list of literals
  rather than rows of attributes; a data store as its cylinder. A relationship is a
  *mode*: one of them is armed while the next two clicks draw that line, and the armed
  entry has to be recoloured to say so — which a baked-in colour cannot do. So the four
  relationships and the two tools are stencils that take the palette's own colour, and
  they keep lighting on hover and reversing out of the accent when armed.

- **A refused write through MCP now says why, not just that.** A validation refusal has
  always carried every reason at once — an author fixing a form should not make one round
  trip per mistake — but the MCP client read only the one-line summary out of it. So an
  agent saving an information model got "the model is not valid" and nothing else. It has
  no form to read the details out of, so it retried blind, which is the failure mode the
  tool surface exists to avoid. The shared client now appends the findings to the message,
  reading both shapes in use, and skips a finding it cannot parse rather than losing the
  whole refusal to one odd entry.

- **The Console landing page carries the brand mark.** "Welcome to Atlas" opened on a
  bare heading, so the one page a newcomer lands on was the one page that showed no
  mark at all — the glyph sat in the top bar above it and nowhere in the card itself.
  The heading now leads with the same `.mark` box the bar uses, at 48px. It is the
  shared box rather than a copy of the glyph, so an organisation that has uploaded its
  own logo (ADR-0148) sees that logo here too, and a later upload or removal repaints
  this mark along with every other one. The logo setting names the landing page along
  with the top bar and the login screen, so what it promises is what it does.

- **The Starmap reads its structure once for everybody, and everybody's health for
  themselves.** With every open Starmap now re-reading itself, the cost of deriving one
  scaled with the audience: a landscape is built on the engine's run loop — the single
  writer — and costs a directory listing and a JSON decode per record across four
  stores, plus a walk of every compiled process. Twenty tabs is one operations team,
  and it was twenty of those readings, competing for the loop that executes process
  instances.

  The server now holds that reading for **30 seconds** — the view's own re-read floor,
  deliberately: a shorter one bounds nothing, because readers do not poll in step. What
  it holds is the whole design:

  - **Health is never cached.** Parked work, incident ages, running instances, which
    workers have polled — all read fresh on every request. They are what an operator
    opens the view for, and they are engine point reads rather than disk. A status view
    that made trouble wait out a timer would be saving the wrong cost.
  - **Visibility is never cached.** Every access decision is made on the request, from
    the request. The held reading carries the *inputs* a decision is made from and
    never a decision, so one person's landscape can never be served to another.

  Deploying a process, writing a call override and creating a deployment target drop
  the reading at once — those are the changes somebody makes and then immediately looks
  for on this picture. A new application or worker appears within the 30 seconds, and
  the picture says how old it is while it waits: the landscape is dated by when its
  *structure* was read, not by when the answer was served, so the freshness line is
  true of a cached answer as much as a fresh one (ADR-0211 §7).

- **The Starmap says when it was read, and keeps itself true.** Everything on that
  canvas has a shelf life — the severity badges are an observation, the incident counts
  move as an operator works through them, and the three new weightings below are live
  quantities, one of them measured against a clock. A landscape opened at nine and
  still open at eleven showed two-hour-old numbers with nothing on the page saying so,
  which is the failure the export's stamp already exists to prevent, happening on the
  screen the stamp is copied from.

  The observation time is now on the page beside the node count (**"observed 4 min
  ago"**), rewritten every ten seconds, and a **Live** switch beside it — on by
  default — re-reads the landscape from the server while the view is open.

  The cadence is paced by what the picture costs rather than by a constant: the mesh is
  derived on the engine's run loop, so the interval is a twentieth of what the last
  derive actually took, floored at 30 seconds and ceilinged at 5 minutes. A landscape
  that derives in 40 ms is re-read on the floor; one that takes four seconds backs off
  to well over a minute by itself. Nothing is asked behind a hidden tab, or while a
  node is being dragged. A refusal keeps the picture, says **"could not re-read"**, and
  backs off to the ceiling — a server that is down does not want thirty requests a
  minute from every open tab. The filter, the drilldown, the selection, the pins and
  the zoom all survive a re-read. Turning Live off stops it; turning it back on asks at
  once rather than waiting out another interval (ADR-0211 §7).

- **The Starmap can be sized by what is running on it, by what is stuck on it, or by
  how long it has been stuck.** The instance counts were a checkbox beside the Notation
  picker — an overlay ticked onto
  whatever was on screen — and that offered a picture with no reading. Size on the
  Starmap is one channel and it already carried connectivity, so a landscape with the
  box ticked had radii meaning structure while its labels meant load, and the one
  question somebody ticks it to ask, *where is the work*, was the one it could not
  answer.

  The checkbox is gone. The Notation picker now offers two **heatmaps** beside *Atlas
  (derived)* and the two projections, because every entry there decides how the
  landscape is drawn and only one of them can be chosen at a time:

  - **Instances (heatmap)** — *where is the work.* A node's size is what is running on
    it: capacity, reading a load test, finding the process actually carrying the estate.
  - **Incidents (heatmap)** — *where is it stuck.* A node's size is how many unresolved
    incidents the engine holds against it. The severity badges already said **which**
    nodes have a finding; what they could not say is how much is parked behind each,
    and a process holding four hundred stuck tokens wore the same badge as one holding
    a single retry. The badge stays the classification; the size is now the magnitude.
  - **Incident age (heatmap)** — *how long has it been stuck.* A node's size is how long
    its earliest unresolved incident has been standing. This is the one that changes a
    decision: four hundred incidents from the last five minutes is a worker that has
    just fallen over and drains itself once somebody restarts it, and three standing
    since Friday is a process nobody is coming back to. The count ranks those the wrong
    way round, every time.

  For the third one the mesh payload carries a new fact: **`oldestIncident`**, the
  moment a node's earliest unresolved incident was raised. The oldest rather than the
  newest, because that is the age of the *problem* — a process where one token parked
  on Friday and three hundred piled up behind it has been stuck since Friday. It is
  absent, never zero, where there is nothing to date, including an incident raised
  before the engine recorded the moment: "not known" and "raised at the epoch" are
  different facts, and a zero would draw the process as the oldest trouble on the
  estate. A collapsed application carries the earliest of the processes it stands for.
  Collecting it costs nothing — the incident scan already reads every record, and the
  raise time is a field on the record it is reading.

  The panel states the exact age for whichever node is selected (**"Oldest still parked
  5 d ago"**), which is the number a circle cannot give.

  On any of them the size is a **ratio scale**. A node carrying nothing sits at a floor;
  a node carrying the least the weighting counts — one running instance, one incident, a
  minute stuck — is already a clear step above it; and from there the size grows with
  each *tenfold*, so equal steps of size are equal multiples of the tally and the largest
  node on the landscape is the largest circle. That is the question a heatmap is opened
  with: an estate's instance counts run from one to several thousand, and what an
  operator wants of a circle is how many times, not how much.

  The key **draws** that scale rather than only describing it: a row of reference
  circles — nothing at all, then the tallies the scale is marked at, up to the busiest
  node — each at the size a node carrying that much is drawn. They come out of the same
  arithmetic the nodes did, so a circle in the key is the circle on the picture, and
  the row travels into an exported file as well, where there is no key to scroll to.

  Each circle is also a **filter**. Click the one marked 100 and the picture narrows to
  the nodes running between a hundred and the next mark, with their neighbours kept for
  context exactly as a search keeps them; click it again to widen. It combines with the
  search box rather than replacing it — a term and a band together show what matches
  both — and a saved view remembers which band it was looking at.

  Every node keeps a **floor**, whatever its tally, so nothing drops off the picture: an
  idle process, a worker, a decision and an application whose load sits on the processes
  it holds are all still nodes somebody can see and click, and "nothing here" stays
  distinguishable from "not on this server". On the incident picture that also makes the
  good news legible — a flat landscape is the answer, and the key says so rather than
  leaving you to wonder whether anything was measured. Kind is unaffected: it was never
  carried by size alone, and shape and colour still carry it.

  The reference is the largest node on the **whole** landscape rather than on what the
  filter has left on screen, so narrowing to two nodes cannot swell the smaller of them
  into the worst thing on the estate — and it is named in the key and in the export's
  stamp, because an area with no stated reference is a decoration rather than a
  quantity. A saved view stored while the counts were a switch reopens as the weighting
  it stood for.

  **The ranking column follows the weighting too.** It ranks by blast radius on the
  derived drawing, as it always has; with a heatmap on it ranks by the same quantity
  the canvas is sized by, so the largest circle and the first row are the same node.
  Two orderings on one screen, with nothing on it saying they answer different
  questions, is a contradiction a reader cannot resolve. It is not a re-listing of the
  picture: a circle gives neither the exact number — nobody reads 41 against 38 off two
  areas — nor the name, which zoomed out is not painted at all. The blast radius stays
  as the second number on each row, which is what turns a count into a priority: forty
  incidents on a leaf process is a contained problem, twelve on something two hundred
  things need is an outage (ADR-0211 §6, §8).

- **A Worker Type's setup folds away once you have set it up.** The section that says
  where a type's work runs and what has to exist at the provider stood open above the
  fields. That is right the first time and wrong every time after: on a 270-pixel panel
  it is most of a screen, and an author who has already configured the type scrolls past
  all of it to reach the field they came for. It is a group now — the chevron, the title
  and the collapse memory that Operation, Failure handling and the mapping lists already
  have, and *Collapse all* reaches it like the rest.

  It does not simply start folded, which would undo what it is for. It opens by itself
  when this server has **no** Worker of that type configured *and* the type names one at
  all — someone meeting a type they have not set up. A type that configures nothing (a
  REST call, a mockup, user provisioning) has a setup worth one read, so it starts
  folded. An explicit toggle beats both and survives the next selection, because one
  title serves every Worker Type: not wanting to read it is a statement about the
  section, not about Jira.

### Security

- **Model-authored scripts no longer inherit Atlas credentials.** The supervised script
  worker starts from an explicit runtime allowlist, and each interpreter receives only
  that small runtime environment plus its source and process variables — never the
  worker token or arbitrary deployment secrets. The internal credential handed to a
  supervised worker is additionally confined to the worker protocol, so a leaked token
  cannot read processes, instances or tasks. The existing PowerShell, Python and
  JavaScript switches now reach the supervised worker as well; disabling all three parks
  script jobs without starting an arbitrary-code worker.
  ([ADR-0297](docs/adr/0297-confine-internal-worker-token.md))

## [0.6.0] — 2026-09-09

**This release is about arriving from somewhere else.** A Microsoft Identity Manager
workflow now imports as the process it *is* rather than as the elements it is made of.
MIMWAL states conditionality and iteration in attributes instead of in branch and loop
elements, so twenty guarded steps used to arrive as an unconditional chain — a diagram
that looked like your process and did not behave like it. Conditions become gateways and
iterations become multi-instance loops; each activity's queries and assignments are
carried over as addressable rows and **counted as the work they are**, so the number a
migration is planned with is the honest one; and an import that would land on an id
somebody already holds now refuses with the impact spelled out — which deployed version,
how many instances are running on it, which of its elements the incoming model still has.

**The second half is for the empty installation.** Every Worker Type selectable on a
service task now carries its own setup where the type is chosen: whether it needs a
configured Worker and a credential at all, the steps at the provider in order, the
failure it is usually reported with, and a link into the handbook this server serves
itself. The Console's *New worker* form shows the same thing, which is where an operator
actually stands. Both the short form and the handbook's long one say **when they were
last read against the real thing**, and a test says so when they have not been — these
steps name menus in somebody else's product, and nothing here can notice when that
product is rearranged.

**And the class canvas grew up.** An imported Active Directory schema — sixty classes,
forty-character attribute names — showed three faults at once: boxes that were 200px
whatever was written in them, relationships drawn as straight lines through whatever
stood between their ends, and several of them landing exactly on top of each other so a
click could only ever reach the last one drawn. Boxes size themselves now, lines are
routed at right angles by the same router a sequence flow gets, and the palette is
diagram-js's own — the one the process modeler already puts down its left edge.

**One thing to read before upgrading.** A parallel or inclusive join now counts tokens
**per incoming sequence flow**, as BPMN 2.0.2 §13.4 requires and as Atlas did not. The
change moves in the direction of *less* progress: a model that relied on a join firing on
two tokens arriving over one branch now parks there instead — a deadlock you can see and
terminate, where the old behaviour continued silently and swallowed the surplus. No API,
no stored format and no default changes; this one is worth checking your models against.

### Added

- **One place names every resource budget, and one way sets them.** Atlas bounded
  external input in about ninety places: thirty-odd named constants declared next to the
  handler that used them, plus a scattering of bare literals written straight into the
  call. Each was defensible where it stood; together they were not a policy, because
  nothing said what the set *was* — and a set nobody can enumerate is a set nobody
  notices a hole in. `limits.Limits` is that set, grouped by what a budget holds rather
  than by which handler reads it, with defaults that are exactly the numbers the code
  already carried. Names, environment variables and parsing are derived from the struct
  itself, because a second list to keep in step is the failure this ends rather than one
  to repeat. Configure with `ATLAS_LIMIT_*`; a value that is missing, not positive, or
  too large for its field leaves the default standing and says so at startup. **There is
  no way to turn a budget off.** One limit stated plainly: components running inside a
  worker's process — a model provider's answer, a Remedy call, the error snippets in
  tracing — read the named default rather than this installation's configured value,
  because the server's environment does not reach them.

- **A class says which states its instances move through, and the Modeler offers
  them.** BPMN puts a data state under a data object — `order [received]` →
  `[approved]` — and says nothing whatever about which states exist or which may
  follow which. Atlas parsed it, interned it onto the compiled model, persisted every
  transition of it with attribution and drew it on the object diagram, and still
  nothing declared what was legal: the state was a string somebody typed, and
  `[aproved]` was written once and then never matched anything again.

  A «businessObject» may now carry a **lifecycle**: named states, one of them where
  instances start, any number of them final, and transitions between them. It is drawn
  the way the class model is drawn — the same canvas, the same palette, the same one
  Save — reached from the class's own panel, because the class is what owns it. Only a
  business object has one: a value type is equal to any other with the same contents,
  so there is no *this one, later* to track. A class without one is the normal case and
  is silent everywhere.

  The Modeler reads it. A data object's **Type** is a list of the classes the
  application models, grouped by the model they live in and carrying each one's
  business key — the fact that tells two similarly named classes apart — instead of a
  text field with an invisible `<datalist>` behind it. Its **Data state** is a list of
  the states that class declares, marking where instances start and where they end.
  Both keep a way to name something the vocabulary has not heard of yet, because a
  diagram is routinely drawn before the model it names exists, and a state or a class
  nothing declares is reported in the Problems panel rather than refused at deploy.
  Two new data-flow checks say when a process writes a state its class does not
  declare, and when it moves an object between two states the lifecycle does not join.

  The information model editor gained the same treatment where it was still missing:
  a transition's two ends and a relationship's two ends are lists now, so an end aimed
  at the wrong state or the wrong class is corrected in place instead of being deleted
  and drawn again — which used to take its name, its roles and its multiplicities with
  it. A class a relationship cannot reach, or a state nothing leaves, is shown disabled
  with the reason on it rather than hidden (ADR-0259).

- **The handbook's worker runbooks say when they were last checked, too.** The panel's
  short setup steps started carrying that date; the handbook's long-form cards — which
  name the same menus in the same products, at more length — did not, so the more
  detailed of the two was the one with nothing to say about its own age. Each of the 23
  runbook cards now closes with the month it was last walked, in both languages, and a
  test fails when a card has stood unread for a year, is dated in the future, or claims a
  different month than the panel entry for the same Worker Type. They are one instruction
  at two lengths: dating them apart is how one gets re-read while the other quietly does
  not (ADR-0289).

- **A MIM import now hands over a worksheet, not a node inventory.** The serialised
  .NET collections a MIMWAL activity carries — the named queries it runs and the
  assignments it makes — were already decoded into a readable table on the step's
  documentation. They are now also emitted as `<atlas:mimCollection>` extension
  elements on the same element, one `<atlas:mimRow>` per row and one
  `<atlas:mimCell column="…">` per cell, verbatim, so a tool can address a single row
  without re-parsing the XOML kept in `atlas:mimSource`. And every one of those rows
  is its own item of the conversion report: an `UpdateResources` with five
  assignments and a query is one preserved node and **six** pieces of work, so the
  manual-review count says six rather than one, and the number a migration is planned
  with is the honest one. The rows stay out of the graph on purpose — which target
  system a row writes to is not in the XOML at all but in MIM's sync rules, and MIM
  applies the whole table as one request, so importing each row as its own task would
  put a structure into the diagram that the source does not contain. A cell says
  where it sat, never what it means: naming the columns would state something no
  reference settles.

- **A MIM import asks before it lands on something, and says what is at stake.**
  Importing a workflow whose process id was already taken replaced the draft
  there without a word — the one outcome [ADR-0222](docs/adr/0222-artifact-id-renames.md)
  rules out for every design-time store, and the MIM import was the path that
  did not follow it. It now works out what each id holds *before* writing
  anything and answers `409` with the impact; the Console shows it and asks.

  The impact is not just "an id is taken". When the id is also a deployed
  process, Atlas migrates a running instance by matching element ids
  ([ADR-0162](docs/adr/0162-instance-migration.md)) — so the report names the
  deployed version, how many instances are running on it, which of its elements
  the imported model still has, which it does not, and which of its data objects
  the model no longer declares. Those are the instances a later deploy of the
  imported model could not carry over, named one by one rather than left to be
  discovered at migration time. The import itself still only writes a draft and
  deploys nothing.

  An export holding several `WorkflowDefinition` resources now converts them all
  — one draft each, named after its own resource — rather than the first and
  silence. `ConvertAll` is the library entry point for it, `atlas import-mim`
  writes one file per workflow beside the one `--out` names, and the resource's
  own fields (`DisplayName`, `Description`, `RequestPhase`, `RunOnPolicyUpdate`,
  `ObjectID`) reach the process documentation and the import response: MIM keeps
  them outside the XOML, and the XOML is all the importer used to read.

- **A Worker Type's setup steps say when they were last checked.** The steps beside a
  Worker Type name menu paths in somebody else's product — *IAM & Admin → Service
  accounts*, *Certificates & secrets*, *Reset Token* — which is what makes them worth
  writing and what silently stops being true when that product is rearranged. Nothing
  here can observe Google's console, so the text would go on looking authoritative while
  sending its reader in a circle. Each entry now carries the month it was last walked at
  the provider, the panel prints it at the foot of the steps ("Steps last checked
  September 2026. Where a provider has moved a menu since, the provider is right and this
  is out of date"), and a test fails once an entry has stood unread for a year. That test
  reads the wall clock, which the testing conventions otherwise forbid and which is
  exactly the point: a freshness check that can only fail when somebody edits the file
  would never fire (ADR-0289).

- **Every Worker Type says how it is set up, where it is chosen.** Picking a Worker
  Type on a service task now shows, beside its fields, what has to exist before the
  task can run — whether it needs a configured Worker and a credential at all, the
  ordered steps to get there (create the Google service account key, grant admin
  consent to the Entra app registration, invite the Discord bot to the server), the
  failure that type is actually reported with, and a link into this server's own
  handbook for the long version. The same block appears on the Console's *New worker*
  form and in the worker dialog, which is where an operator stands, and on the business
  rule task's temis binding. A type that needs nothing says so, instead of leaving the
  absence of a dialog to be read as a missing step. The handbook gained a runbook for
  the types that had none — Discord, the AI worker, clio (split from temis), SCIM, SOAP,
  directory files, the plain job worker and user provisioning — plus per-product anchors
  for the three SQL types, and a Go guard now refuses a Worker Type that ships without
  its setup or a link into a handbook card that no longer exists
  (ADR-0289).

- **MIM and MIMWAL workflows import as what they mean, not as what their elements
  say.** The MIM importer was written against XOML as it is documented; run against
  a real 25-activity MIMWAL workflow it produced a model that misrepresented the
  process. Four things it could not see before:

  - **Conditionality.** MIMWAL does not use `IfElseActivity`: an activity runs only
    when its `ActivityExecutionCondition` holds, so a workflow of twenty conditional
    steps contains no branch element at all and was imported as an unconditional
    chain — a model asserting a semantics the source does not have, with nothing in
    the report to say so. A guarded activity is now wrapped in an exclusive
    split/merge, entered on a condition and bypassed by the gateway default.

  - **Iteration.** MIMWAL runs an activity once per value of its `Iteration`
    expression — `SplitString` of a delimited attribute, typically — which was
    modelled as a single step. Such an activity now carries a sequential
    `multiInstanceLoopCharacteristics`.

  - **What a step actually does.** The serialised .NET collections that hold an
    activity's work — an `UpdateResources`' `UpdatesTable` and `QueriesTable`, a
    `GenerateUniqueValue`'s `ValueExpressions` and `LdapQueriesTable` — are thousands
    of characters of `Hashtable` markup in the source. They are now rendered as a
    small table on the activity's documentation, so a reviewer can read what a step
    queries and assigns without reading the markup. Columns are rendered by position
    and **not named**: MIMWAL's editor labels the updates grid Target | Value | Allow
    Null, but in the workflow this was checked against, column 1 holds a literal in
    nine rows and column 0 a query result in six, and neither can be assigned to — so
    naming them would state something unverified about every imported activity. The
    `Count` MIMWAL writes into each table is treated as the check it is (it agreed in
    all 46) and reported only when it disagrees.

  - **Which library an activity came from.** XOML binds each activity library to a
    prefix on the workflow root, which is what tells a stock MIM activity from a
    MIMWAL one of the same local name and names the assembly it was authored against.
    Go's decoder resolves prefixes away, so preserved markup was written from the
    local name alone and the fragment referred to prefixes nothing declared — it did
    not parse on its own. A fragment now carries and declares the prefixes it uses,
    and `<atlas:mimSource>` names the fully qualified .NET `type` and `assembly`.
    MIMWAL's `GenerateUniqueValue` is recognised too, mapping to a `mim-uniquevalue`
    service task rather than an unrecognised placeholder.

  Neither MIM expression is translated to FEEL. The MIM function library
  (`ConvertToBoolean`, `ParametersContain`, `IsPresent`, `RegexMatch`) has semantics
  this package cannot reproduce faithfully, and its data references
  (`[//Target/x]`, `[//WorkflowData/y]`) have no agreed FEEL counterpart — a
  translation would risk a model that looks right and is not, the one outcome worse
  than an untranslated one. Placeholders (`= true`, `=[1]`) keep the generated
  process behaving exactly as it did before these were modelled, and each original
  expression is documented on the model and flagged `manual-review`, naming the
  single expression to fill in.

### Changed

- **A join counts tokens per incoming sequence flow.** This is the semantic debt
  [ADR-0024](docs/adr/0024-parallel-gateway-join.md) wrote down and accepted:
  no token-count-per-flow, so a single incoming flow feeding two tokens was not
  distinguished from two flows feeding one each. That is not the rare case it was taken
  for — a fork whose branches rejoin through an exclusive merge produces it — and the
  consequence was the one thing a parallel join exists to prevent: it fired while a
  branch had not arrived, then consumed every token on the node, so the surplus vanished
  with it.

  A parallel gateway now activates when there is at least one token on **each** incoming
  sequence flow and consumes exactly one from each, as OMG BPMN 2.0.2 §13.4 states.
  Every waiting token already recorded the flow it arrived on, so the correct rule needs
  no new state, no new event and no extra scan — only a different question asked of the
  same walk. The inclusive join had the same defect in its own idiom: it fires when
  nothing more can arrive, so its surplus is resolved then and there rather than parked
  waiting for an arrival that cannot come, and its continuation is now a fresh token, as
  the parallel join's already was.

  **Behaviour moves in the direction of less progress.** A model that relied on a join
  firing on two tokens from one branch now parks — a deadlocked join, visible in
  Operations and terminable, where before it continued silently. Worth a look at any
  model that forks and rejoins through an exclusive merge.

- **A refused variable write now stops what comes next, instead of only saying so.**
  The variable and collection budgets refuse a value past their ceiling and raise an
  incident on the element that produced it. That was half a refusal: terminating an
  element clears the incident it carries, so a site that refused a write and then let
  its element finish left nothing behind at all — not the value, and not the report.
  The run looked successful, and the only evidence was a variable that was not there.

  Every site at which a model's or a worker's value becomes a variable now answers
  what happens next, and each answer follows from that site's own semantics. A message
  or signal catch does not complete, because its subscription is already correlated
  and neither is delivered twice. A call activity does not resume without the result
  it called for, because the child instance is already gone. An output mapping does
  not let its activity finish having promoted nothing, and keeps the activity's local
  scope — that is where the raw result the mapping reads still is, so resolving
  re-evaluates over it. An input mapping stops the behaviour *before* it runs, rather
  than handing a worker a job missing what the model promised it.

  **Upgrade note:** an instance whose write is refused now stays where it is, with an
  incident naming the variable and both sizes. Resolving it retries the write, so
  correcting the data — or raising `ATLAS_LIMIT_VARIABLE` / `ATLAS_LIMIT_COLLECTION` —
  lets it carry on. Before this, such an instance could complete as though nothing had
  happened.

- **The class canvas got its toolbox, and its boxes stopped overflowing.** Three
  things about the drawing were wrong on any model larger than the examples, and an
  imported Active Directory schema — forty-character attribute names, sixty classes —
  showed all three at once.

  **A class box was 200 pixels wide whatever was written in it**, so
  `msDS-ManagedPasswordPreviousId: string [0..*]` simply ran out over the border and
  across whatever stood beside it. A box now grows to hold its members, up to 380px.
  The members are set in a monospace face, so that width is arithmetic rather than a
  guess. Past 380 a member is shortened **in its name** — `msDS-Managed…Id` — because
  the type and the multiplicity are the shorter half and the half a reader is after.

  **Relationships were straight lines between box centres**, which is fine for six
  classes and unreadable for sixty: the line left at whatever angle the geometry made
  and crossed every box between its ends. They are routed at right angles now, by
  `ManhattanLayout` — the same router bpmn-js gives a sequence flow, so the two
  canvases bend their lines alike. Two further faults sat under that one: several
  relationships into the same class came out **exactly on top of each other**, one
  line where there were three, and a *click could only ever reach the last one drawn*;
  they are spread across five corridors now. And the dashed line from a data store to
  its class was being routed like a relationship, which on a real model is three
  hundred pixels of vertical line crossing everything in between — for a line that
  only ever meant *this one*. It is an annotation (ADR-0230 §7), so it points
  straight again.

  **The palette is diagram-js's own** — the one bpmn-js and dmn-js put down their left
  edge, with its chrome, its groups and its separators, whose stylesheet was already
  in the vendored bundle and unused. Where a row of text buttons sat in the title bar,
  as far from the sheet as the window allows, there is now a toolbox against it: the
  three stereotypes and the data store, then the four relationships carrying the marks
  that tell them apart on the drawing (◇ aggregation, ◆ composition, △ generalization),
  and the lasso above them in the tools group where the process modeler keeps it. The
  entries are built from the served subset, so the palette offers exactly what the
  write path accepts and gains a stereotype the day the server does.

  Fitting now takes the palette's width off before choosing the zoom rather than
  scrolling it away afterwards — a fitted model used to open with its first column
  behind the toolbox, and scrolling a diagram that already filled the window only
  trades the left edge for the right one.

  One thing in the properties panel with it: its textareas hold documentation and
  nothing else — no FEEL, no scripts — and were being drawn in the monospace face a
  textarea defaults to. Prose set in code face reads as something to be executed. The
  Modeler met this first and answered it the same way for its own documentation field.

- **Every list opens with its search boxes showing.** Each data table has carried a
  per-column filter row (`table.js`) since it replaced the hand-rolled sort and filter
  code in each view — but collapsed behind a funnel icon in the header, so the first
  thing anyone saw on a list was a table with no visible way to search it. The search
  was there all along, one click away and invisible. The row is part of the list now:
  the boxes are on screen when the list is. The funnel still collapses them for a list
  where the vertical space matters more, and a table with a `data-dt-key` remembers
  that choice the way it already remembers its sort column.

  With one way to narrow a list, in the same place on every list, the two boxes that
  did it their own way above a table are gone: the *Filter artifacts…* box in the
  Modeler's application detail and the *Filter processes by name or ID…* box in
  Operations › Instances. Both are what the Name/Process column's own box does, and it
  does slightly more — the column filter also reaches the id line beneath the name. The
  Tasks inbox and the Repository keep their search boxes: neither is a table, so a
  column filter cannot stand in for them.

  Three lists that had quietly lost their sorting and filters have them back. The audit
  log after a refresh, the variable-search results and an application's Deployments tab
  each build their table after the route's one enhancement pass has run, which left
  them the only lists in the UI with no sort headers and no filter row at all.

  Two tables that are not lists opted out of the enhancer instead: the SSO claim rules
  in Console › Organization and the attribute grid of an information-model class. Both
  are grids of inputs — there is no cell text to sort or filter by, so a filter typed
  there would have hidden every row — and the attribute grid's order is set by dragging
  and means something. Each already has the search that suits it.

  Where a list is searched, and what a table has to be before the shared enhancer is
  applied to it, is now a decision record (ADR-0286)
  rather than a habit: the row is shown with the list, a view builds no search box over
  a table it owns, a table that is not a list carries `no-enhance` and says why, and
  code that replaces a whole table enhances it again.

- **An imported model keeps its ids, and its diagram reads forward.** A node used to
  be `Activity_1`, `Activity_2`, … in emission order, so inserting one activity in
  MIM shifted every id below it: a re-import of a barely changed workflow produced a
  diff touching everything, stranding any hand-made adjustment. Ids now derive from
  the activity's `x:Name`, with a guard's gateways named `<id>_gate` and `<id>_join`
  after the activity they wrap, and a join or loop exit after its split. Every id
  goes through one table, so a name the workflow reuses, one that collides with the
  process id, and the diagram-interchange ids all step aside instead of producing a
  document the compiler rejects for a duplicate id.

  The diagram lays out by longest path rather than shortest. A split that both enters
  an activity and bypasses it reaches the merge in one hop and through the activity in
  two, so shortest-path layering put the merge in the same column as the activity and
  drew the edge between them pointing backwards — a defect the empty if/else and
  parallel branches already had, and one a guard per activity would have made
  pervasive. Back edges (a while loop's return) are excluded from the layering, as
  before.

### Fixed

- **Four ways a MIM workflow's own logic went missing.** Each was silent, and each
  produced a model that read as if the source said something it does not:

  - **A branch condition written as a WF property element was read as a step.**
    `<IfElseBranchActivity.Condition>` is how a real MIM branch carries its
    condition; XAML writes a property that way. The importer treated any child as
    an activity, so the branch gained a task named after the property, doing
    nothing, while the condition inside it went unread. Any `Type.Property`
    element is now the property it is — and a condition is read from it,
    including the name of the declarative rule it points to.
  - **The last branch became the default whatever it carried.** Its condition was
    dropped with no note, and an `IfElseActivity` with a single conditional
    branch — plain "if X then do Y" — became an unconditional path reported as
    native. A branch is the default only when it carries no condition; when every
    branch is conditional the default bypasses them all, which is what WF does
    when none holds.
  - **A `ConditionedActivityGroup` disappeared into a plain sequence** — the group
    element, its markup, its `UntilCondition` and every child's `WhenCondition`,
    without a single note. Repetition and per-child conditionality are the whole
    difference between a CAG and a sequence. It is now the repeat-until loop it
    is, with each child guarded by its own condition.
  - **A real `Export-FIMConfig` export was not recognised.** The importer looked
    for the attribute's name written as an XML attribute; FIMAutomation writes it
    as a child element, so nothing was found and a whole export converted into a
    process of `ExportObject` placeholder tasks. Both shapes are read now.

- **The replay's data-object list keeps its search, and an open state trail stays with
  its row.** Showing every list's filter row by default
  ([ADR-0286](docs/adr/0286-a-list-carries-its-own-search.md)) reached one list that was
  not ready for it, and it went two ways at once.

  The list arrived from Operations › a single instance › **Data** with a filter row, and
  lost it again the moment the operator clicked anything: selecting an element on the
  diagram re-renders the inspector, which rebuilds that table from scratch, and a rebuilt
  table has to ask for its sorting and its search back. That is ADR-0286's own rule 4 and
  this was the first place it was missed — worth recording, because that record says the
  convention should be replaced by something automatic if it is missed twice.

  Underneath it sat a second one, which fixing the first would have made visible rather
  than fixed: the **state trail** a row opens into is not a row of the table's own data,
  and it was not marked as the detail row it is. Sorted, it went by the text in its own
  single cell and landed under a stranger's object — descending, it sorted to the very
  top, nowhere near the object whose writes it lists. Filtered, it vanished under a row
  the filter had *kept*, because the trail's one cell holds nothing in the Class or State
  column a filter was typed into. It now carries `data-dt-detail`, exactly as the
  Variables tab's expanded value already did, so it travels with its row when the list is
  sorted and is hidden with it only when its row goes.

  Found by reading dbuchs7's change against this view rather than by a report, and
  measured through the real app shell — a harness that mounts the replay directly never
  runs the route's enhancement pass, so neither half is visible from one.
  `e2e/replay-tables.spec.mjs` covers all three behaviours through `index.html`; each was
  watched failing first, and two of them were rewritten after the first versions passed
  for the wrong reason (ascending sort happens to put a stray trail next to its own row
  anyway, which is a coincidence and not the property).

- **A throttled sign-in no longer reports itself as a wrong password.** The login screen
  turned every failed login request into "Invalid username or password.", including the
  429 the login throttle (ADR-0197) answers with. That throttle refuses the *attempt*
  before the password is looked at: five wrong guesses and an account is refused for up
  to a quarter of an hour, so from the sixth attempt on the correct password looked
  exactly like a wrong one. Somebody who mistypes a generated admin password a few times
  therefore goes hunting for a credential that is already right, and the only workaround
  the screen leaves them is restarting the server — the one action that clears the
  throttle's in-memory buckets. A 401 still says only that the credentials were refused,
  which is what keeps the login from answering whether an account exists; a 429 now says
  the attempt was throttled and the password was not checked; anything else points at the
  server log instead of blaming the password. The OAuth consent screen's own sign-in form
  carried the same three answers in one sentence and now makes the same distinction.
- **An incident that says "no worker registered as X" can now create X, instead of pointing
  at the Console.** The one incident whose cause is named in its own message was the one
  incident with no way out of it: the row offered a link to Console › Workers, which is the
  detour ADR-0160 exists to remove — read the incident, carry the name and the Worker Type in
  your head, find the add form, navigate back, resolve. It is not a choice to make there
  anyway: the deployed model states both the name and the type, and a worker created under
  anything else leaves the task parked. So every incident surface now opens the same worker
  dialog in a create mode with those two fixed, and **Add & retry** writes it and hands the
  parked job one more attempt (ADR-0287).
- **Publishing an application runs the deploy-time preflight that only the Modeler's Deploy
  button ran.** The check that says a model names a worker nobody configured — along with the
  information model's data-flow findings and the foreign-namespace check — lived inside the
  single-model deploy handler. Publishing an application and importing a release reach the
  deploy directly and reported none of it; `projectDeployResp` had no `warnings` field at all,
  so it could not have. That is how a model reaches production naming a worker that does not
  exist, and Publish is how most applications get there. The three checks are one function
  now, called by all three paths, and the Console shows what a publish warned about
  (ADR-0287).

- **The MIM importer reads the workflows MIM actually writes.** Three defects
  kept `atlas import-mim` (and `POST /api/v1/imports/mim`) from doing its job on
  real exports:

  - A workflow root whose `xmlns` declarations are serialised **without quotes**
    around the value — which is how MIM writes them — failed to parse at all, so
    the whole import returned an error. Such input is now repaired once before
    parsing, and the repair is reported — as a `Report` warning on the CLI and in
    the API response, and as a note in the generated process documentation.
    Input that is broken for any other reason still fails with the parser's own
    diagnosis.
  - Activities from the **MIMWAL** activity library carry the author's label in
    `ActivityDisplayName` and a WF designer id (`actionActivity6`) in `x:Name`.
    Only the latter was recognised, so every node in a MIMWAL workflow was named
    after the designer id and the imported diagram was unreadable.
    `ActivityDisplayName` is now the first label consulted.
  - Markup preserved in `<atlas:mimSource>` was wrapped in a **CDATA** section
    after its attribute values had been escaped. CDATA suppresses entity
    resolution, so a quotation mark inside a MIM expression was preserved as the
    literal text `&#34;` — silently changing every `ActivityExecutionCondition`,
    `Iteration` and `ConflictFilter` it appeared in. Preserved markup is now
    written as escaped character data, and re-parses to the activity's original
    attribute values.

### Security

- **A script task's output had no ceiling.** The worker collected a script's stdout with
  `cmd.Output()`, which grows a buffer to whatever arrives — and a script's output is
  written by code the model author controls. `while true: print(x)` was an unbounded
  allocation on the machine the worker runs on, held back only by the 30-second timeout,
  which at a gigabyte a second is not a bound. Both streams now go through a bounded
  reader. The bound is one of the budgets above, so it has a name and can be raised.

  Two things the fix had to survive, recorded because each looked correct and was not:
  the first bounded reader embedded a `bytes.Buffer`, which promotes `ReadFrom` — and
  `io.Copy` prefers it, so every byte landed in the buffer without `Write` ever being
  called, and the ceiling looked like a cap while being none. And the test meant to prove
  every budget is enumerated walked no files at all, because the root entry is named
  `..` and its own skip-dotted-directories rule matched it; it reported success. It now
  counts what it found and fails below forty.

## [0.5.0] — 2026-09-08

**This release closes the boundary.** `atlas serve` requires a login by default — `--auth`
was opt-in, which meant the safe configuration was the one you had to know to ask for — and
each of the 199 `/api/v1` routes names the role it requires, enforced in one place for every
credential there is. `/metrics`, `/mcp`, the API description and the explorer moved behind
that boundary, the last routes that had not. Atlas can now be an **OpenID Connect relying
party** and, in the other direction, an **OAuth authorization server** a person can let an
application act through; it terminates **TLS** itself with `--tls-cert`/`--tls-key`; and a
machine can be given an **API token** rather than somebody's password. Upgrading from 0.4.0
means the server that answered anonymously yesterday asks who you are today.

**Connectors are Workers, and they run like it.** The rename goes all the way through — the
Console, the Modeler, the handbook and the examples — and it is not only a word: central
decisions, SCIM, SharePoint, SOAP, LDAP, clio and Jira all moved off the engine's own loop,
which leaves no kind running in-process. The new Worker Types are **Discord**, **Google
Sheets**, **Jira** and **web scraping**, each with the other direction as well — a Discord
channel, a spreadsheet row, a Drive folder or a Jira issue can *start* a process.

**Atlas can now say what the data in a process actually is.** A new top-level information
model gives a data object a class you can see, drawn on a real diagram canvas with the
Modeler's own properties panel; an instance shows the data it carries and where each value
came from; a data store says where a class is kept; and "which instances are carrying this
order?" — the question BPMN structurally cannot answer — has an answer. The Tasks app's
sidebar folders became **saved filters** built from listboxes and evaluated on the server,
and the console **speaks German** on the screens that have been translated.

**And an external architecture and code audit was worked through end to end.** Sixteen of
its seventeen findings are fixed, the seventeenth half. The log frames a batch rather than a
record and carries a format version; a batch persists the work it still owes, so an instance
interrupted at a batch boundary moves again; corruption in a sealed segment is a hard error;
startup proves its log prefix or refuses to serve; one inventory of what is on disk drives
backup and restore, with twelve directories that had drifted out of the full backup — the
vault among them — back in it; a join synchronizes in its own execution scope; a gateway
that cannot route parks with an incident instead of dropping the token; and reading a running
instance is an object question rather than a role question. The records are ADR-0270 through
ADR-0285.

### Added

- **Discord is a Worker Type: a process can speak in the channel the team already
  reads.** A running process regularly has something to say to people, and outbound mail
  is the wrong shape for a team that works in a chat channel. What they want is a message
  where they are already looking, a thread under it, and a status the process keeps
  current instead of repeating (ADR-0258).

  Six named operations — send, edit, delete and read a message, list a channel, and open
  a thread — each with its values checked at deploy rather than dropped at call time. The
  bot token lives in the Worker record and is resolved server-side by name, so a model
  refers to a Discord Worker by name only and never carries a token. Replying in a thread
  is deliberately not a seventh operation: in Discord a thread *is* a channel, and its id
  is on what `create-thread` returns, so a reply is `send-message` addressing that id.

- **A Discord channel can start a process.** The other direction: a watch polls a channel
  and publishes each new message as an Atlas message, so somebody reporting a fault in
  chat opens a case without leaving the channel. Configure it under Workers › *Events…*
  with the channel id and the message name your model starts on (ADR-0262).

  A message id is a snowflake — monotonic by construction, assigned at creation, never
  moved by an edit — so the watch is sequenced on the id itself and needs neither a lag
  window nor a cursor field. A new watch is forward-only: the messages already in the
  channel are skipped, rather than starting one process per item of history. The started
  instance is seeded with `messageId`, `channelId`, `content`, `authorId`, `authorName`,
  `authorBot`, `timestamp`, `eventType` and the whole `message`.

  **If the Worker also posts into the channel it watches, guard on `authorBot`.** Its own
  messages come back through the watch, and without that gate each one starts another
  round — a loop in which every instance looks correct on its own.

- **A deploy now says when a model's `atlas:` namespace is not Atlas'.** The compiler
  matches an extension element on its local name alone, so a model that binds the `atlas`
  prefix to the wrong URI compiles, deploys and runs exactly like a correct one. The
  Modeler is not lenient in the same way: it resolves `atlas:*` against the single URI in
  its moddle, and an element outside it is not an Atlas element at all — importing appears
  to work, and then every Save fails with `no namespace uri given for prefix <ns0>`
  (ADR-0269).

  The parser stays lenient, because a deployed definition is recompiled from its stored
  XML on recovery and rejecting a stray namespace would strand every model already
  deployed with one. Instead the deploy warns, naming the elements, the namespace found
  and the one line to change. The check reads the moddle the Modeler itself loads, so it
  cannot drift from what the Modeler accepts.

- **Task folders: the Tasks app's sidebar folders are now saved filters somebody builds
  from listboxes.** The sidebar had four fixed folders. The question a person actually
  arrives with in the morning is a different one — "what is open on customer enquiries?" —
  and until now it was retyped into the search box every day.

  A folder is a saved filter. *＋ Neuer Ordner* asks for a name, who may see it, and
  conditions as rows of three listboxes: field, operator, value. The fields are the task's
  own metadata — process, task, assignee, candidate group, lane, priority, due date, how
  long the instance has been running, and whether it has a form. **Nothing that can be
  mistyped is typed:** the process list comes from the deployments, the task names and
  candidate groups from the compiled models, the users from the directory.

  What is stored is the *rule*, not the expression. The FEEL is generated from it and shown
  under the conditions, with a match count that follows every click — you watch your
  listbox choices become the thing the engine evaluates, and the folder reopens later as
  the same rows. That direction is the whole design: a generated expression can always be
  rendered back into the controls that produced it, a hand-written one cannot.

  It is evaluated on the server and off the run loop (`readOffLoop`). That is not a detail:
  the task list is capped at 500 rows, so a filter applied in the browser would have
  reported an empty folder under load while matching work existed. Compilation happens once
  at save, never on a read. Every folder's badge comes from a single scan.

  A folder can be shared with a group or the whole organisation. Only its owner may change
  it — a shared worklist that anyone can rewrite is not one a team can rely on. And a
  folder is a view, not a permission: which tasks a person may see is still their role's
  answer.

  New: `GET/POST/PUT/DELETE /api/v1/task-folders`, `/task-folders/fields`,
  `/task-folders/counts`, `/task-folders/preview`, and `?folder=<id>` on `/api/v1/tasks`.
  See `docs/adr/0268-task-folders-are-saved-filters.md`.

- **The console speaks German, on the screens that have been translated.** The interface
  was English, hard-coded wherever a string appears. That does not hold for the Tasks app:
  its readers are the people doing the work, and their processes and forms are German
  already.

  `api/web/i18n.js` is a message catalogue with no dependency and no build step — ADR-0012
  still stands. German is the default and deliberately *not* the browser's language: the
  rest of the console is still English, and half a translated screen because of a setting
  nobody made is worse than an untranslated one. Another language is chosen explicitly
  (`?lang=en`) and remembered per browser. A missing key renders as the key, so a hole in
  the catalogue fails in review rather than falling back silently.

  The boundary is the API: the server sends ids and model data, never interface text.
  Translation proceeds per screen rather than per release; the folders are the first.
  See `docs/adr/0267-console-speaks-german-first.md`.

- **Every task row shows its key.** A queue of a dozen identically named tasks was
  unreadable: nothing on the row told them apart, so there was no way to say which one you
  meant or to notice that one of them was done. It is the same key the deep link carries.
  The lane gave up its place on the row for it — it is on the detail pane with its full
  path, where it already said more than a truncated leaf name in the list.

- **A deployed process can be filed under an application after the fact.**
  `PATCH /api/v1/processes/{key}` with `{"projectId": "..."}` moves a deployed
  definition into an application, or out of one (an empty id means Ungrouped).

  A deployment carried its own application, stamped once when it was deployed — from
  what the editor sent, or inherited from the matching draft at that moment, or
  nothing. Afterwards there was no way to change it: moving the *draft* moved the
  draft, so a process deployed through the API, or before its application existed,
  stayed Ungrouped for good — on the Modeler home and on the Starmap as a process
  belonging to nothing — with a redeploy, and a version bump, the only way out.

  It is metadata and nothing else: the version, the model, the active flag and
  everything running are untouched, and the engine never reads the filing at all. Two
  things move that the caller does not name, because the alternative is an estate that
  cannot be put back together: **every version** of the definition, since filing
  belongs to the process rather than to one of its versions; and **the other pools of
  a collaboration**, since they are one drawing, listed as one row, with no way to
  address the others separately. Editor rights are needed at both ends, as moving a
  draft already requires, and the platform-managed application refuses to be written
  into (ADR-0122).

- **Clicking an element in Operations lists the instances sitting on it.** A live view
  badged "25 205 here now" beside a page of fifty instances was a dead end: the count
  said how many were waiting and nothing said *which*. Finding them meant a variable
  search for a value the operator would have to know already.

  The diagram is the query now. Click an element and the panel lists exactly the
  instances whose token is sitting on it; click another and it switches; click the
  process — the canvas around the shapes, or a collaboration's pool — and every instance
  is back. A chip names what the list is narrowed to and offers the way out, the diagram
  outlines the element, and an empty result says *"no instance is sitting here right
  now"* rather than the listing's *"no instances yet"* — with thousands of gray visits
  beside it, those are different sentences.

  It works for every element a token can rest on, not only user tasks, and it costs the
  page you are shown. The obvious implementation — walk the version's live instances and
  keep the ones holding a token there — is a scan that grows with the instance population,
  on a view that re-reads its list every 1.5 seconds. So a new index
  (`piByEl:<procDefKey>:<elementId>:<piKey>:<elKey>`) is written and dropped by exactly
  the two calls that move the ADR-0080 live-token counter: the number badged on a shape
  and the rows in the panel are two readings of one fact. Existing stores are seeded once
  at open — a missing index here does not read low, it reads *empty*.

  `GET /api/v1/instances` gained `?element=`, scoped to `?process=` and live-only (a
  finished instance holds no token), and `atlas_list_instances` (MCP) gained `process`,
  `element`, `state` and `limit`, so an agent can put the same question to the engine
  instead of sieving a page it happened to get. The click it takes over is the decision
  inspection's, which keeps the ⚖ badge that was already its affordance
  ([ADR-0261](docs/adr/0261-instances-on-an-element.md)).

- **Your brand colour reaches the forms.** Setting an organisation's accent under
  *Settings → Appearance* used to tint the Console around a form and stop at its edge:
  the form itself — its fields, labels, focus ring, buttons and typeface — came from
  the form renderer's own stylesheet and stayed stock blue. It no longer does. A user
  task, an incident's repair form, the preview in the form editor and the public start
  form behind a share link are all painted from the same palette as everything else,
  in the same typeface, on the same borders
  ([ADR-0263](docs/adr/0263-form-runtime-brand-theming.md)).

- **The public start form and the sign-in consent screen carry your branding.** Both
  are shown before anyone has a session, and both used to display the built-in Atlas
  mark and the default blue no matter what an admin had configured. They now ask the
  server for the organisation's colour and logo like every other page — which matters
  most for the start form, since that is the page an organisation's own customers
  open. If the settings are momentarily unreachable the page still appears, in the
  default colours: a form that is late is worse than a form that is unbranded.

- **A button label that stays readable on your brand colour.** Text on an accent fill
  used to be white, fixed. White on a deep blue or a federal red is fine; white on a
  brand yellow or a pale mint is about 1.5:1, which nobody can read. Atlas now derives
  the label colour from the accent you chose — white or near-black, whichever the
  colour's own luminance makes legible — and applies it everywhere the accent is a
  background: primary buttons, count badges, active segments, the submit button inside
  a form.

- **Forms written by the AI Worker.** The form editor has a **✨ Generate** button.
  Describe what the form should ask for — in your own words, in your own language —
  and, if you like, point it at the process the form belongs to and the step it is
  for. What comes back is a form-js schema, open in the editor and **not saved**: you
  read it, change what you want, and press Save yourself, exactly as with a form you
  laid out by hand. Generating again over an open form is a refinement rather than a
  fresh start, so "add a field for the period" adds one instead of replacing the other
  twelve.

  You mostly will not open it from there, though. In the Modeler, **"Create a new
  form"** on a user task or on a start event now carries that step with it: the form
  editor opens with the generator already up, on that process and that step, and the
  only thing left to write is the sentence about what the form should ask for.
  Pressing that link *was* you saying what the form is for, and you should not have to
  say it twice. (A **repair form**'s link stays the plain one — that is a different kind
  of form, the values an operator corrects to get a parked task moving, and the
  generator does not write those.)

  The half you do not have to type is the process. Naming one lets the generator read
  the model's own words — the process documentation, each step's documentation, the
  conditions on its sequence flows, and the variable names its mappings and data
  objects already use — out of the **draft you are working on** (or the deployed
  version when there is no draft). So the keys it writes are the names the process
  already calls those things by, and a form for a step that is five minutes old sees
  that step. Nothing about a running instance is read: no case data ever goes to a
  model this way.

  It asks **the AI Worker an operator already configured** for the runtime
  ([ADR-0255](docs/adr/0255-agent-models-are-console-workers.md)) — one endpoint, one
  credential in the vault, one place to change the model, and no key in the browser.
  With several configured you choose which one writes the form; with one there is
  nothing to choose. Where none is configured the button is simply absent, rather
  than being a button that only ever fails.

  What a model sends back is checked before you ever see it: the form keeps the id
  the editor was holding (so a generated form cannot quietly unbind the user task
  that binds it), every input gets a usable, unique key, the document is bounded, and
  a component a task form cannot render is refused by name rather than dropped — a
  form quietly missing the field you asked for would be worse than one that says it
  could not be written. An answer that is not a form comes back as a sentence in the
  dialog, with your brief still in it, so you can rephrase or simply try again.

  See [ADR-0260](docs/adr/0260-ai-form-generation.md) for why
  this runs where it does, and why it is authoring rather than a service task.
- **The training nuggets play full screen, for showing one to a room.** A nugget sat in
  the flow of the handbook at reading size, which is right for reading and wrong for the
  case it keeps being used for: an onboarding session with the thing on a projector. The
  ⛶ button hands the nugget the whole screen — dark surround, the caption set large and
  centred underneath, and the space bar, arrow keys, Home and End driving it, so the
  presenter is not aiming a mouse at a 24-pixel control. Going full screen starts the run
  if nothing has played yet; leaving it stops the run rather than letting it animate on
  behind whatever came next. The keys bind to the nugget only while it owns the screen —
  bound globally they would take space and the arrows away from anyone scrolling the
  handbook.

  The picture keeps its own shape instead of filling the screen. That is not cosmetic and
  it cost a build to learn: the highlight ring and the cursor are percentages *of the
  stage*, so a stage wider than the picture inside it puts the ring beside the button
  instead of on it — and a ring pointing at nothing looks exactly like a ring pointing at
  something. The full-screen stage therefore carries the shots' 1200×703 and is centred in
  what is left. `e2e/nuggets.spec.mjs` measures the ring against the picture at two screen
  shapes; the check that does the work there is the one on the picture's aspect ratio,
  because an `<img>` element box goes on filling its stage even when the picture inside it
  does not.

  Where a browser has no Fullscreen API, or an embedding forbids it, the button is not
  offered rather than offered and inert.

- **A weekly job asks whether the handbook's screenshots still match the product.**
  `make nuggets` re-takes them, but nobody re-takes screenshots on a schedule — and
  staleness here is silent: a shot of a UI that has since moved still renders, and a
  highlight ring drawn on a button that moved still looks deliberate. Nothing throws.
  The reader finds out months later, by looking for a button where the picture put it.

  The **Nugget screenshots** workflow runs `capture.mjs --check` every Monday: it starts
  a throwaway Atlas exactly as the capture does, measures where every highlighted element
  actually is, and compares that against the committed block. It writes nothing — no
  images, no commits. A difference opens an issue labelled `nuggets-stale` naming the
  targets that moved, with the measurements; a difference still there the following week
  comments on that issue instead of opening a second one.

  Deliberately the cheap half. Regenerating and committing the images automatically would
  keep the chapter current without anybody looking, at the price of a bot writing ~800 KB
  of image data into the history on a schedule — and of captions drifting away from
  pictures nobody read.

  **Two bugs in the capture surfaced while proving the check works, and both were mine.**
  The first run reported the UI had moved when it had not: an earlier capture that
  outlived its `timeout` was still serving on the port, the next run seeded *that* engine
  on top, and the process list grew by four rows between runs. `capture.mjs` now refuses
  to run against a server it did not start, and kills its own on a signal rather than only
  in a `finally` block that a signal skips. The second was in the comparison itself —
  matching a measurement to the nearest committed rectangle is guesswork the moment two
  targets sit close together, and Claim and Complete are neighbours on the task pane. The
  target's name now travels with its rectangle, so the comparison is exact.

  The generated block gains that `target` name per highlight; `e2e/nuggets.spec.mjs`
  holds it against `scenes.mjs` like everything else.

- **The README says which images a re-take actually changes.** Measured rather than
  assumed: seven of the twenty, and always the same seven — the ones carrying a clock or
  a live count. A diff touching only those is the capture re-photographing the clock; a
  diff touching the other thirteen means something moved.

- **The nugget screenshots are output now, not artifacts somebody once made.** Their
  pictures are captures of the running product, which buys recognition and costs
  staleness: a shot of a UI that has since moved still renders, and a ring drawn on a
  button that moved still looks deliberate. Nothing throws, and the reader is the one who
  finds out.

  `make nuggets` re-takes the set. It builds the current tree, runs it on a throwaway data
  directory with auth off, seeds it with this repo's own `order-to-cash` example — five
  instances with baskets on both sides of the gateway, plus a built-in process whose user
  task carries a real form — takes every shot, writes them as WebP, rewrites the
  `#nug-data` block, then stops the server and deletes the data. Chromium encodes the
  WebP, so the script adds no image dependency.

  **Coordinates are never typed.** `scripts/nuggets/scenes.mjs` is the source and it names
  targets rather than places: a scene says *highlight the Deploy button*, and the capture
  reads that button's bounding box out of the live page. A button that moves is
  re-measured; a target that disappears fails the capture loudly instead of leaving a ring
  on empty space. This was not theoretical — between two runs the `order-to-cash` row moved
  from a quarter down the process list to three quarters down, because the list sorts by
  last activity, and the highlight followed it both times without anybody touching a number.

  `e2e/nuggets.spec.mjs` holds the source and the generated block together: same nuggets in
  the same order, same scenes, same captions, a measured rectangle wherever the source asks
  for one and none where it does not. Written by confirming it fails against a changed
  `scenes.mjs` that was never re-captured, a rectangle hand-edited into the block, and a
  scene naming an image no shot produces.

  What no test can catch, and the README says so plainly: a caption that no longer
  describes its picture. That failure has already happened once in this chapter. Read them.

- **A deployed diagram can be tidied up without redeploying it.** You only find out that
  a picture is wrong once the process is running: a task's name disappears under a token
  badge, a label lands on an edge, a row of shapes that read fine on the modelling canvas
  is unreadable with incident markers on it. None of that is visible while authoring,
  because none of those overlays are drawn until there are instances.

  Until now the only answer was to redeploy, and redeploying is the wrong instrument for
  it. It mints a version that differs from its predecessor in nothing the engine can see,
  so the Deployments view fills up with "moved a label" and the versions that are real
  changes stop being findable. It re-arms start timers, re-registers DMN models and
  supersedes message and signal subscriptions — all correct, all unnecessary. And it does
  not even fix the case you were looking at: **running instances keep the old picture**,
  because they resolve their definition by key, until somebody migrates them — writing an
  audit record and a timeline entry for a change that moved a box twenty pixels.

  Open a deployment in the Modeler, move what needs moving, and pick **Save layout to
  deployment** from the "…" menu. The definition keeps its key, its version, its compiled
  process and its running instances; only the drawing changes, and it changes for every
  view of that definition at once — including the instances already running, which is the
  case a redeploy could not reach at all. A collaboration's pools are updated together, so
  the halves of one picture cannot disagree about where they are.

  Nothing but the picture *can* move, and that is a property of the mechanism rather than
  a promise: the server keeps the stored model's bytes for everything outside the
  `<BPMNDiagram>` block and takes only the diagram from what you send, so the model behind
  the compiled process is bit-for-bit the one it already had — script bodies with
  load-bearing indentation included. If the submitted model differs from the deployed one
  in anything else, the save is refused with a message saying to deploy it instead, rather
  than half-applied. Reformatting is forgiven: an editor round-trip renames prefixes,
  reorders attributes and re-stamps the root, and none of that is a change to the process.

  The adjustment is on the audit trail as `deployment.diagram_updated`, and the process
  listing carries `diagramUpdatedAt` / `diagramUpdatedBy` — the stamp that says this
  drawing is no longer the one that was deployed, and who to ask about it. The picture is
  not a historical fact: what happened is in the event log, and a replay from March opened
  after an adjustment shows the same steps with the same values, laid out better.

  Also on the API as `PUT /api/v1/processes/{key}/diagram` and to an agent as
  `atlas_save_process_diagram`
  ([ADR-0251](docs/adr/0251-adjust-a-deployed-diagram.md)).

- **The handbook plays.** Reading how to claim a task is not the same as being shown
  where to press, and the gap costs the most for exactly the people who have the least
  patience for a manual: somebody handed an Atlas login who wants to be useful this
  morning. The handbook gains **training nuggets** — short animated click-throughs that
  play in the page.

  The lead one is the **Roundhouse Kick**: two and a half minutes that go from "what is
  this" to "what could I do with it" — the diagram is the program, a deploy, a token
  moving, a task reaching a person, a worker doing a step, a rule in a table, an
  incident that loses nothing, the log every step is written to before it is visible,
  the six apps, the Playground, the landscape, and a single binary with no database and
  no broker behind all of it.

  Then one path per role Atlas actually has ([ADR-0209](docs/adr/0209-roles-per-endpoint-group.md)),
  each under seventy seconds, because the fastest way to be useful is to be shown your
  own job and not everyone else's: `user` claims a task and completes it, `modeler` goes
  from diagram to deploy through the Playground, `operator` finds an instance and
  resolves an incident, `admin` grants roles deliberately and puts a credential in the
  vault.

  **The stages are drawn, not photographed.** A screenshot is a photograph of one build
  at one window size: it ships as a binary, is unreadable in the other colour scheme,
  needs a second copy per language, and goes stale in the one way nobody notices. These
  stages are markup built from the page's own theme variables, so they follow the colour
  scheme, scale to a phone, stay searchable, and carry both languages in one file
  through the `data-l` mechanism the page already had — the language toggle moves a
  running animation with it, with no code in the player for it at all. Nothing
  autoplays, a nugget pauses when it scrolls out of view, and starting one stops the
  one already running.

  A scene points its cursor at an **element**, not at a coordinate, and this is the part
  that earns its test. Percentages were the first attempt and they miss: the stage has
  padding, the caption takes a variable slice of the bottom, and both move with the
  width — so a number tuned at one size lands beside the button at another, and the
  reader is taught to press empty space. Nothing throws and nothing logs; even a
  screenshot of the right frame looks fine. `e2e/nuggets.spec.mjs` drives every scene
  that aims a cursor and asserts the pointer's tip is inside the element the scene
  names, alongside the catalogue matching the chapter in both directions and no scene
  shipping one language twice. All three were written by confirming they fail — against
  a selector pointing at nothing, a removed container, and a caption copied across
  languages — rather than by observing that they pass.

- **An element's documentation is Markdown now, and the people who read it see it as
  such.** `<bpmn:documentation>` is the one field every element carries, and the Modeler
  has treated it as Markdown for as long as the Developer View has existed: it highlights
  the field as Markdown and offers a real editor for it. The surfaces that *show* the
  prose printed it literally, so a checklist reached the person doing the work as a
  column of hyphens and an emphasised "do not" as asterisks. The reasoning, and what the
  renderer deliberately does not support, are in
  [ADR-0250](docs/adr/0250-documentation-is-markdown.md).

  A new renderer (`api/web/markdown.js`) turns it into structure in the **Tasks** app's
  work instruction, the **Operations** instance replay's Details tab and the **Panorama**
  properties panel: headings, bullet and numbered lists, block quotes, inline and fenced
  code, bold, italic, strikethrough and links.

  Prose written before this keeps reading the way it was written. A line break stays a
  line break — the renderer's one deliberate divergence from CommonMark, which would
  otherwise join consecutive lines into one paragraph — four leading spaces do not turn a
  sentence into code, and `order_id` stays a variable name rather than becoming italics.

  The renderer escapes the whole source before it parses any of it and builds every tag
  itself, so a documentation text can give the block it is shown in structure but can
  never script the console; a link's destination has to pass an allowlist (http, https,
  mailto, or a route inside Atlas) or the link renders as its words. Nothing about the
  model changes: the file, the compiler's interned copy and the API all still carry the
  source text, and the engine still never reads it.

- **The handbook is caught up with the product: six apps, the Playground, and the two
  Console screens nobody had written up.** The shell has offered six apps for a while —
  Console, Modeler, Tasks, Operations, **Panorama** and **Data** — and the welcome
  chapter still opened with "the four apps". A missing chapter behaves differently from
  a wrong sentence: nothing fails, nobody notices, and the app nobody reads about is the
  app nobody discovers. The word "Panorama" appeared in the page zero times,
  "Informationsmodell" zero times, "Playground" zero times.

  Both apps now have a chapter of their own, in both languages, under a new **Landscape
  & data** group in the table of contents:

  - **Panorama** ([ADR-0189](docs/adr/0189-panorama-architecture-modeling-and-live-overlays.md),
    [ADR-0211](docs/adr/0211-panorama-derived-landscape-mesh.md)) leads with the
    distinction the app turns on: the landscape is **derived** and the architecture
    views are **drawn**. Which store each node comes from, what each edge is a fact
    *about* rather than an assertion of, how to read size and why the layout is
    reproducible — and the one thing about saved views a reader has to know before
    relying on them: they live in that browser and are shared with nobody.
  - **The information model** ([ADR-0230](docs/adr/0230-process-information-model.md),
    [ADR-0232](docs/adr/0232-uml-model-import.md)) starts where BPMN stops, because that
    is what makes the app make sense: a `dataObject` is scoped to one process
    definition, `itemSubjectRef` points into a schema language BPMN deliberately leaves
    open, and so "which processes touch the same order?" has no answer at all. Then the
    business key as the fact that answers it, the three steps from a class to a resolved
    deploy, and what an import does with what it cannot keep.

  Three more gaps closed in chapters that already existed:

  - **The Playground** ([ADR-0215](docs/adr/0215-modeler-playground.md)) is a section of
    its own in *Test & simulate*, which until now offered a reader the token simulation
    and a real deploy and nothing in between. It is positioned against the token
    simulation by what that one deliberately does *not* do — no FEEL, no conditions, no
    DMN, no data — and it says the two things that surprise people: editing the diagram
    invalidates the run, and leaving the editor releases the sandbox, because a sandbox
    is a live engine on the server.
  - **AI access** ([ADR-0200](docs/adr/0200-mcp-oauth-resource-server.md)) answers the
    operator's actual question — what the page hands you, and the two things it checks
    first, one of which (the published origin behind a TLS proxy) is otherwise met as
    "the connector just doesn't work". Its second half is for everybody: a person's own
    approvals are theirs to withdraw.
  - **The audit log** ([ADR-0184](docs/adr/0184-grant-audit-log.md)) gets its four
    actions, who may read it, and the case that surprises — with authentication off
    there is no actor, so nothing is recorded at all.

  The **contextual help** knows about all of it: the "?" menu's *On this page* entry had
  no rule for Panorama, Data, AI access or the audit log, so all four fell through to
  "Welcome to Atlas" — help that lands a reader at the top of a page reads as help that
  does not work. Two tests now hold the join the two files cannot see between them:
  every anchor `handbookHelp()` hands out must be a section the handbook has, and every
  app the shell offers must be a card in the welcome chapter. Both were written by
  confirming they fail against a broken anchor and a wrong route, rather than by
  observing that they pass.

- **An instance that is gone is still findable.** History retention hard-deletes a
  finished instance once the exporter has it ([ADR-0115](docs/adr/0115-history-retention-hard-delete.md)) —
  that was the whole bargain of [ADR-0114](docs/adr/0114-opensearch-event-exporter.md):
  delete the data corpses, but keep them searchable somewhere first. Somewhere turned
  out to be nowhere an operator could reach. The search asked this server's own store,
  the store no longer had the instance, and the answer came back empty, which reads
  exactly like "this never existed".

  The search now falls back to the exported log. It asks two questions rather than one,
  because the export is a stream of events and not a table of instances: first which
  instances held a matching variable — a terms aggregation over the scope key, so a
  value written five times is one answer and the response stays small — then what those
  instances were, as a bounded page of hits with an explicit field list.

  A row that comes back this way is marked **archived**, and that is the point rather
  than a detail. The instance does not exist here: it cannot be opened, replayed or
  terminated, and what the row reports is what the log recorded, not what is true now.
  So the panel offers no Replay and no task link beside it, the picker says the word
  too, and the row carries no element instance count — the archive knows of no live
  tokens, and a zero meaning "none recorded" must not be dressed up as a measurement.

  An empty result now distinguishes its causes. "Nothing matched" is about the data;
  "no event log is exported", "the store declined" and "the store could not be reached"
  are about this server, and an operator told the first when the truth is one of the
  others stops looking for an instance that exists.
  ([ADR-0247](docs/adr/0247-instance-archive-search.md))

- **A call activity's `+` is now the way into the process it calls.** A call activity is
  the one element on a diagram whose contents are somewhere else — a separate model,
  deployed on its own, and at runtime a separate instance. BPMN says so with the `+` in
  the bottom edge of the shape, and until now that marker pointed at nothing: in the
  Modeler the called process id sat in the panel as text, and reaching the process it
  named meant remembering the id, going back to the process list and finding it by hand.
  The live view and the collaboration replay offered nothing at all; only the instance
  replay had a way in, through its `↳` badge.

  **Double-click the `+`** and the called process opens — the same gesture in the
  Modeler, the live view, the instance replay and the collaboration replay. Hovering the
  shape rings the marker, silently, so the way in is visible before the pointer is
  anywhere near a 14px target; putting the pointer on that ring spells the gesture out.
  The replay's badge and *Called process* link stay exactly where they were. Where "in" lands is what each surface knows: the Modeler
  opens the callee's **draft** where one holds that id and its newest deployed version
  otherwise, the live view opens the **child instance** this caller started (or, under
  *All instances*, the called process's own live view), and the replay opens the child's
  replay — falling back to the called process, and saying so, for a call activity that
  never started one. Leaving the Modeler saves the caller first when the session is
  editing a draft, and asks before discarding when there is nowhere to put the edits.
  The **Called process** panel also gained an *Open called process* button, which is the
  same door for a keyboard.
  ([ADR-0245](docs/adr/0245-call-activity-drilldown.md),
  `e2e/call-activity-modeler.spec.mjs`, `e2e/call-activity-live.spec.mjs`,
  `e2e/call-activity-replay.spec.mjs`)

- **The Tasks app follows a call activity too — down, not away.** The Process tab beside a
  task shows what has already run and what is still ahead, and its call activities carry
  the same `+` as everywhere else. It was the one surface the drill-down left out, for a
  good reason and with the wrong conclusion: a hash change out of the Tasks app would tear
  down the half-filled form in the pane next to it, and the Operations replay it would
  land on is an operator's route the assignee may not hold.

  So here the gesture **descends in place**: the panel re-renders on the child instance
  the call activity started, a bar over the diagram says where you are and offers one way
  back, and the variables listed underneath follow the descent — a called process is a
  separate instance with separate variables. It costs no request the view was not already
  making (the child's key is on the caller's own timeline) and no permission it did not
  already have. A call activity the token has not reached says so rather than doing
  nothing, and a task whose process calls nothing looks exactly as it did.
  ([ADR-0246](docs/adr/0246-tasks-call-activity-descent.md),
  `e2e/tasks-call-activity.spec.mjs`)

- **A model can say what it wants to be found by.** [ADR-0241](docs/adr/0241-finding-an-instance.md)
  made a version's instances a bounded page and an instance key a point read, and named
  the layer it left out: the question an operator actually arrives with is a business
  value — "where is MT-1998?" — and answering that still meant reading every instance of
  the version and every one of its variables. Now a process declares what matters:

  ```xml
  <bpmn:process id="identitaet" atlas:searchable="identityId,item">
  ```

  For a declared variable, `identityId=MT-1998` is a **seek** into a value index rather
  than a walk — its cost is the number of matches, whatever the engine holds — and a
  trailing `*` asks for a prefix instead. It also means *exactly* that value rather than
  a substring of it, which changes no answer anybody gets today: a declaration is the
  only way into that path, and no model could carry one before now. An undeclared name
  keeps the substring search it always had.
  ([ADR-0244](docs/adr/0244-searchable-variables.md))

  **A process that declares nothing pays nothing** — one length check per variable
  write, and no index entries. That is the whole reason it is a declaration and not a
  default: indexing every value would double the variable write path and fill the index
  with JSON blobs nobody searches for.

  Two things building it moved, both recorded. `applyToState` holds the record and
  nothing else — it cannot ask a compiled process whether a name is searchable — so the
  decision is made at command time and frozen into the event, in the one place every
  variable event passes through, the way the producer key already is (ADR-0219). And no
  backfill is needed: `atlas:searchable` did not exist before this change, so every
  definition that can declare it is deployed after it and carries the flag from its
  first write.

  What the index deliberately will not do: substring, free text, structured values, or
  anything past 256 bytes. An ordered key-value store answers equality and prefix; a
  truncated key would answer an exact query with a wrong row, and full text over cold
  history belongs in the OpenSearch export (ADR-0114).


- **Finding one instance among a few hundred thousand.** An operator's most common
  question is about a single instance — "where is MT-1998?", "what happened to the
  instance this ticket names?" — and Atlas answered every version of it by reading
  through every instance in the engine.
  [ADR-0239](docs/adr/0239-off-loop-queries.md) took the first half of that away:
  those queries no longer hold the engine's single writer while they run. They still
  *walked*, though, and off the loop a walk still costs the operator the wait —
  `?process=` filtered after the scan, so listing a version with three instances cost
  a walk of every instance in the store; the finished half was collected whole and
  sorted in memory to show the ten most recent completions; and the search was, in
  its own words, "a full scan with no value index".
  ([ADR-0241](docs/adr/0241-finding-an-instance.md))

  **A bare instance key is now a point read.** Paste a key into the search box and
  it resolves against the live record and then the history — two reads, no walk,
  and the whole instance with all its variables comes back. A number that is not an
  instance key falls through to the content search, so `3098` still finds
  `zip=3098`.

  **Instances are indexed by their definition.** Two new column families —
  `piByDef:<procDefKey>:<piKey>` and `piDoneByDef:<procDefKey>:<completedAt>:<piKey>`
  — are maintained in `applyToState` alongside the records they index, so replay
  rebuilds them (I4/I6) and an existing store is seeded once at open, the same way
  the ADR-0080/0083 counters were. `GET /api/v1/instances?process=` now reads them:
  a version's instances cost the page rather than the store, and history comes back
  most-recently-finished first without sorting anything in memory — which the
  history family's own key order cannot give you, since an instance started first
  can finish last.

  **The listing pages.** `?state=active|finished` returns one half and, when the
  page is capped, hands back `X-Instances-Next-Cursor` for the next (older) one —
  the same newest-first cursor paging the task inbox uses. The finished cursor
  carries the completion time as well as the key, for that same reason. `?before=`
  without `?process=` is refused rather than ignored: a silently dropped paging
  parameter is how a client loops over one page forever. `state=all` and
  `state=completed` keep working — callers wrote them back when the parameter was
  ignored, and they now do what those callers meant. `GET
  /api/v1/instances/search` takes `?process=` too, scoping a content search to one
  version's index and letting it stop as soon as the cap is met.

  **The live view's instance panel stops loading everything.** It used to fetch
  every instance of the version, with every variable on every row, on a 1.5-second
  poll, and render one card each. It now asks for one page per half, says what it
  is showing out of what exists ("80 of 150" — a page reported as a total is
  believed), walks the cursor on **Load more**, and carries a search box: an
  instance key, or `name=value` over that version's variables. `GET
  /processes/{key}/runtime` gained a `finished` count so that total costs nothing.

  Still a walk, deliberately: an unscoped content search, and an unscoped
  `?state=finished`. An ordered key-value store answers equality and prefix, not
  substring — the record says what a variable-value index would have to look like
  (declarative, resolved at deploy time, exact and prefix only) and why full text
  over cold history belongs in the OpenSearch export (ADR-0114) rather than in a
  new engine index.

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

- **Google Sheets as a Worker Type, and a spreadsheet or a Drive folder as an event
  source.** A lot of the data a process runs on lives in a spreadsheet somebody
  maintains by hand — the applicant list, the budget lines, the tracking table the team
  actually looks at. Atlas could not reach one at all, and not for want of a package:
  Google needs a signed JWT-bearer assertion, which the generic REST Worker Type's
  bearer/basic/apiKey surface cannot produce, so the gap was structural.

  Everything a person reads about it says **Worker**, per ADR-0203. The old spelling
  survives only where changing it would break something: the `connector/` package path,
  the `connector="…"` attribute and the `<atlas:googleSheetsConnector>` element (both
  authored in deployed models, and the guard that stops bpmn-js silently dropping an
  extension only recognizes `*Connector` elements), and the `"connector"` key in the
  offload payload.

  **Outbound** ([ADR-0235](docs/adr/0235-google-sheets-worker.md))
  is eight operations that are steps a process takes: create a spreadsheet, add a tab,
  read a range, write one, append rows, clear a range, delete a tab, move the file to
  the trash. `values` takes the three shapes a process actually holds — a list of rows,
  a flat list of cells, or a list of objects projected through the task's `columns`; a
  list of objects with no columns is refused at deploy rather than written in an order
  nobody chose. A read with `header` answers with objects keyed by the first row, which
  is the shape a multi-instance subprocess iterates. `delete-spreadsheet` **trashes
  rather than purges**: a process that deletes the wrong file is a bad afternoon either
  way, but only one of the two is survivable. The credential — a service account's key,
  or a refresh token — lives in the vault behind a Worker name, never in a model.

  **Inbound** ([ADR-0234](docs/adr/0234-google-inbound-watch.md))
  is the two intake channels people already have. A **row watch** publishes each new row
  of a spreadsheet as an Atlas message — the "a Google Form writes its responses into a
  sheet" case — and a **folder watch** publishes each file put into a Drive folder,
  because the folder is a queue people already use. Both ride the existing inbound
  bridge, so an event starts or wakes a process through ordinary message correlation,
  and Atlas polls rather than exposing anything to the internet. A row's sequence is its
  own row number, which is monotonic for appends; a file has none, so its mark is scoped
  per file id exactly as a Jira issue's is (ADR-0214).

  Two things are stated rather than hidden. Delivery is at-least-once and a spreadsheet
  has no idempotency key, so a retried append can duplicate a row — a process that
  cannot tolerate that needs a marker column it reads first. And a row watch loses rows
  if rows are *deleted* from the watched range, because a row's only identity is its
  place; the sheets people watch are the append-only ones.

- **The Google service-account grant is now shared machinery.** `connector/oauth2` had
  named it "the one such case" a caller supplies its own `Fetcher` for. A second caller
  needs it, and two copies of a JWT signer is the duplication that package was
  extracted to end, so it moved into `oauth2.ServiceAccount` and the mail Worker Type
  delegates to it.

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
  APIs ship — DigitalOcean's is a single entry file of references into a thousand more
  — and each reference resolves against the directory of the file it is written in, so a
  path item two directories down reaches its schemas the way its author meant. What may
  be read is bounded on purpose: this mock serves what it reads and authenticates
  nobody, so references may not climb out of the document's own directory unless
  `--spec-root` says they may, and a `$ref` to a URL is refused. DigitalOcean's own
  published document — 659 operations assembled from 1141 files — is served in under
  half a second, with their example data in it.

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

  **Nothing is left stranded at the edge of the picture.** Reported three times as
  "single nodes far away from the rest", and the first two fixes missed it because
  both were about framing and this was about the settle. The pull that centres the
  graph is deliberately weakest along the wide axis, so the picture takes the shape of
  the frame — and that was tuned for a node its edges are also holding. A node with
  **no edge** has none: the pull is all that keeps it near the picture, against a
  repulsion that falls off with distance, and the balance sat far outside everything
  else. On a thirty-four-node estate with ten unattached processes, two of them ended
  hard against the left and right edges with the rest squeezed into the middle. That
  is not a rare shape — a process deployed through the API, or before its application
  existed, belongs to no application and is drawn with no edge at all. The pull is now
  twice as strong on a node with nothing attached to it, which is measured rather than
  reasoned: higher packs the loose nodes into a lump of their own instead.

  **A Drafts switch** adds the diagrams nobody has deployed. The picture's subject is
  what this server *runs*, so a saved draft is absent from it by default — which
  answers "is this deployed?" only if you already knew the process existed. Switch
  drafts on and they appear beside the processes of the application that holds them,
  in the process square so they read as the same kind of thing, with a lighter fill and
  the dashed outline the placeholders already use: what is drawn is not running. The
  fill is lighter rather than merely different — its first version was a warm tone of
  exactly the same brightness as a deployed process, which on a projector or in print
  left the dash doing all the work. They
  claim nothing about running — no version, no instances, no status, and they can
  never make an application look worse — and their only edge is the one that says
  which application holds them, because a draft's call activities are a plan and
  drawing them would put an intention on the canvas in the same ink as the facts.
  A draft opens in the Modeler, where it exists, rather than in Operations, where it
  does not. Off by default because an estate holds several drafts per deployed
  process, and a landscape that collapsed to applications on account of undeployed
  diagrams would be a worse picture than one that leaves them out; a saved view
  remembers the switch, and an exported image says in its stamp that the drafts are
  in it. Neither the ArchiMate nor the C4 export carries them, and each says so in
  its declared loss: those documents describe a system that exists.

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

- **The log frames a batch, not a record — and a segment carries a format version.** `Sync`
  wrote one frame per record, each with its own length and checksum, and the reader accepted
  every whole frame it found before a torn one. A crash in the middle of a batch therefore
  recovered *part* of it: the events before the tear were durable facts, the rest never
  happened, and nothing in the log said the two belonged together (ADR-0285, audit F02).

  A batch is now one frame with one length and one checksum, so batch atomicity falls
  together with the frame integrity the reader already enforced — a torn batch is
  indistinguishable from a torn frame, and those it has always discarded. No commit marker,
  no second state machine in the reader.

  **The on-disk format is version 2.** Existing logs are read as before: a version-1 segment
  is recognised by its shape and decoded record-per-frame, so an installation upgrades
  without a migration. The reverse does not hold — a segment written by 0.5.0 is not
  readable by 0.4.0 — so this is a **breaking** change to the WAL format in the downgrade
  direction. `maxRecordSize` is a batch bound now, and the reader's contract moved from
  "one payload is one record" to "one payload is n records".

- **Corruption in a sealed segment is a hard error, with the file and the offset.** The
  reader could not tell the active tail of the log from a segment that was closed long ago,
  and treated an invalid length, a checksum failure and truncated data the same way in both:
  as end of file. In the active segment that is correct — a crash mid-write leaves exactly
  that. In a sealed one it is data loss reported as success (ADR-0283, audit F03).

  The reader is told which it is reading. In a sealed segment each of those cases now fails
  with the filename and the byte offset; even in the last segment a checksum failure
  *followed by more data* is no longer a harmless tail. The same lenient decoding lived a
  second time in `wal/tailer.go`, where it silently truncated the OpenSearch export stream —
  frame decoding is one implementation now, so the export path cannot drift lenient again.

- **Startup proves its log prefix or refuses to serve.** Recovery reconstructs state from the
  log; after compaction the beginning of that log is a checkpoint rather than genesis. If the
  checkpoint was missing, `RecoverFrom` fell back to replaying what it could find and
  *succeeded* — with an installation that had quietly lost everything before the gap
  (ADR-0280, audit F04).

  The start now establishes which prefix the state on disk actually stands for. If it is
  missing, Atlas installs a verified checkpoint and replays the gapless suffix — the same
  code the snapshot restore path uses, shared rather than copied — or it refuses to start
  and says what to restore. A missing prefix is never again read as a successful replay from
  genesis. ADR-0131 asked for this; this is where the contract is paid.

- **A join synchronizes within its own execution scope.** Join state was keyed by process
  instance and node. Parallel iterations of a multi-instance subprocess share both, so two
  iterations reaching the same gateway synchronized across each other: one iteration's token
  satisfied another's join (ADR-0277, audit F06). The key is now (process instance, execution
  scope, gateway).

  The inclusive join had the same confusion in both of its halves — it waited for siblings
  that could never arrive, and when it did fire it consumed every token parked on the node,
  including the other iteration's. The risk here is over-correcting rather than
  under-correcting, so a subprocess on one branch has a test of its own.

  Counting per incoming sequence flow, which is what OMG BPMN 2.0.2 §13.4 actually
  prescribes, is deliberately **not** part of this: it revises the simplification ADR-0024
  accepted knowingly, and that is a specification alignment with its own risk, not a bug fix.

- **A gateway that cannot route parks with an incident instead of dropping the token.** An
  exclusive gateway whose conditions all evaluated false, with no default flow, returned
  without a word — and the token was gone. The instance sat there looking healthy, one token
  short, with nothing anywhere saying why (ADR-0273, audit F08).

  The ordering was the whole defect: the gateway wrote its `Completed` event *before* the
  route was known, so by the time it found there was none it could no longer park. It decides
  first now. Three lines away sat the more dangerous half: an evaluation **error** was read as
  `false`, so a condition that could not be evaluated at all quietly took the default flow —
  a branch nobody chose. The two are fixed separately and tested separately.

- **A token may not hold the writer forever: element activations are budgeted.** A cycle with
  no wait state in it — a loop of automatic tasks, a self-referential flow — drove the
  partition's single writer without ever yielding, and every other instance on that partition
  waited for a process that was never going to finish (ADR-0272, audit F12).

  The budget counts **per token**, not per instance: a cycle is one token going round, while
  fifty thousand multi-instance iterations are fifty thousand tokens taking one step each.
  Any per-instance ceiling that stops the first stops the second too. A newly minted token
  inherits its parent's count, or a cycle through any fork, join or subprocess exit would
  reset its own budget. The default is 10 000 steps per token per run (`SetExecutionBudget`),
  and it cannot be switched off — "off" is the behaviour it removes.

  Stopping on the budget raises an incident on the element, which meant incidents needed a
  reason: a jobless incident used to be resolved by *node type*, and that only works while a
  node type has exactly one way to get stuck. `model.IncidentValue` now carries a `Reason`
  behind the message; older records are one byte shorter and read as unclassified.

- **A multi-instance activity's iteration count is checked before it is allocated.** The count
  comes from the model or, more often, from an instance variable, and between the number and
  the allocation stood nothing: a variable holding a billion meant a billion `expr.Value` in
  one call on the processor goroutine, and the partition was gone before anyone could say why
  (ADR-0276, audit F16).

  The limit is checked *before* the allocation, defaults to 100 000 iterations
  (`SetMaxIterations`), and the rejection is an incident on the body — resolvable once the
  data is corrected, rather than a process nobody can reach. The limit itself is allowed;
  refusing it would make it a budget of one less.

- **`atlas_list_instances` (MCP) returns a page, not a bare array.** It answered with
  a plain JSON array, which cannot say it is a *page* — and the endpoint behind it caps
  at 1000 rows and flags the cut in a header the body does not carry. An agent handed
  the array alone read the first page of three hundred thousand instances as though it
  were the whole population, and acted on it.

  It now answers with `{items, truncated, nextCursor}` — the envelope
  `atlas_list_tasks` already used — and takes a `before` cursor to resume. The two list
  tools are one protocol now: hand `nextCursor` back as `before`, never parse it. A
  `truncated` page without a `nextCursor` means there is more but this listing has no
  position to resume from; narrowing it (`process` plus a single `state`) is what gets
  you one. **Breaking** for anything that parsed the array directly — read `items`.

- **The object diagram is drawn on diagram-js now, so it zooms and pans.** The
  instance's objects and the lines between them were built here as SVG strings, with a
  layout of their own, and the cost showed up as things a reader expects and does not
  find: a diagram bigger than the panel could only be scrolled, nothing could be
  clicked, and there was no way to make it fit. That is word for word the complaint
  [ADR-0237](docs/adr/0237-class-canvas-on-diagram-js.md) made about the *class*
  canvas a fortnight ago, one altitude down — the look was downstream of the
  substrate — and it is the follow-up
  [ADR-0259](docs/adr/0259-data-object-lifecycle.md) named.

  So the drawing moved onto the same shared bundle the class canvas and Panorama
  already use, as `AtlasCanvas.uml.ObjectCanvas`, and the diagram gained zoom, pan,
  selection and the same three controls — the same icons, the same step, the same
  corner — that the two canvases beside it carry. Zooming a diagram is the same act
  on all three surfaces, and a near-miss between them is worse than any one of the
  choices on its own.

  **Nothing about the notation changed, deliberately.** An object still reads as its
  label underlined, its state in brackets, its members as `name = value` with the
  business key marked and an absent member saying so; a containment still carries the
  composition diamond and a key-resolved reference is still dashed and bare, because
  those are different claims. The three e2e tests that state all of that were left
  exactly as they were and still pass — which is the evidence the port changed the
  substrate and not the picture. One detail did have to be put back deliberately:
  diagram-js draws in insertion order, so the lines came out *over* the boxes where
  they had always passed behind them. They are inserted ahead of the shapes now, in
  their own order, and both halves of that have a test.

  Two things are new rather than moved. The canvas **survives a re-render**: selecting
  an element re-renders the whole inspector, and a live instance does it again on
  every poll that brings new frames, so rebuilding the drawing each time would have
  thrown away the zoom and the pan the reader had just set — the two things the port
  exists to give them. And the diagram is **read-only on purpose**: the graph is
  derived by the server, so there is no document to write back to and a box dragged
  here would be put back by the next refresh. Move, resize and connect are absent
  rather than refused, because a canvas that offers a gesture it silently discards is
  worse than one that does not offer it.

  Where a box *sits* is still decided in the browser, and that is not an oversight:
  the server owns what relates to what because that is model semantics, and layout is
  drawing. It just lives beside the renderer that uses it now instead of in a
  twelve-thousand-line view file. The bundle grew 4,476 bytes for the whole notation
  — one copy of diagram-js is the expensive part, and it was already paid for.

- **The training nuggets show the real Atlas, not a drawing of it.** The stages
  shipped as markup built from the handbook's own theme tokens, and the reasoning
  for that was sound as far as it went: no binary weight, both colour schemes, both
  languages in one file. What it missed is what a nugget is *for*. Somebody watching
  one is trying to recognise the screen later, and a drawing has to guess the layout
  — this one guessed a sidebar where Atlas runs its navigation across the top, and
  drew the app switcher as a grid popup where the product opens a drawer. A learner
  who trusted it would look in the wrong place twice before finding anything.

  Every scene is now a capture of the running product: the Modeler with a real BPMN
  model on the canvas, Operations showing five instances at once with their token
  counts and their actual variables, the task inbox with its four filters, the
  worker list, the audit log, the landscape. Twenty WebP images under `web/nuggets/`,
  about 855 KB in total, fetched only when a nugget is played — opening the chapter
  still costs nothing.

  **A modelling error went out with the drawn version and is fixed by the same
  change.** Two scenes drew an exclusive gateway with a single outgoing flow, which
  is not a gateway at all: it branches or it is a waste of a shape. That is a poor
  thing to teach anywhere and worse in material about BPMN. The shots carry a model
  where the gateway genuinely splits — `Summe > 100 EUR?` into a human approval on
  one side and straight through on the other, then a parallel gateway for picking,
  shipping and invoicing — and the modeller nugget now says out loud that a gateway
  with one exit would not be one.

  Highlights and the cursor are percentages of the *image* rather than of the stage,
  which is what makes them stable: an image keeps its aspect ratio at every width,
  so a ring drawn on the Deploy button stays on it from a phone to a desktop. The
  measurements are not eyeballed — the capture script reads each target's bounding
  box out of the live page and writes it into the scene.

  `e2e/nuggets.spec.mjs` follows the new failure modes: a scene naming an image that
  is not shipped, a shipped image no scene uses (dead weight in a `//go:embed`
  binary), a highlight running off the frame, a tap with no cursor, and every
  referenced screenshot actually being served. Each was written by confirming it
  fails against exactly that mistake.

  The caption moved out of the picture and under it. Overlaying it looked tidier and
  ate the bottom of every shot — which is where Atlas prints the legend explaining
  the token markers, so the one scene that most needed its whole picture was the one
  losing it.

- **Every shipped model now carries its own diagram.** Four of them did not:
  `order-fulfillment`, `galsync`, `entra-create-account` and `pruefe-datensaetze` shipped
  with no `<bpmndi:BPMNDiagram>`, and Atlas generated one on deploy. That is enough to run
  a model and not enough to read one — which stopped being a detail the moment the
  handbook began rendering every example on its card, because a generated layout is what
  the reader then sees first.

  They are laid out by hand now, to the conventions in `AGENTS.md`: one straight main
  axis, every branch in a lane of its own, orthogonal waypoints that go around boxes
  rather than through them, and every gateway exit labelled with its answer — which meant
  naming six branches in `galsync` and two in `pruefe-datensaetze` that had no name at
  all, so a reader could not tell which way "deleted?" went. The two subprocesses are
  drawn expanded, because the branching inside them is the example; collapsed, all that is
  left is a box that explains nothing.

  Each was checked as a rendered picture and not only as a deploy, which is the only way
  the two rounds of label collisions in `order-fulfillment` were ever going to surface: a
  gateway label centred over its own branch line reads fine, the same label lying across a
  task box does not.

- **Three mechanisms the engine has always had now have an example.** Signal, escalation
  and compensation were demonstrated by no scenario in `examples/` — only as isolated
  patterns in the conformance gallery and the recipe chapter, which show *that* they work
  and never *what they are for*. The handbook's mechanism matrix said so out loud. It no
  longer has to:

  - **`examples/mahnwesen/`** chases an unpaid invoice, and is the escalation example. Two
    boundaries hang on the same subprocess and their difference is the whole business
    logic: the message "payment received" **interrupts**, because the dunning run is then
    moot; the escalation does **not**, because the run should finish *and* the owner
    should be asked. The subprocess is not cosmetic — an escalation is caught on the
    enclosing activity, so without one there is none. Its deadlines are start variables,
    since `<timeDuration>` takes FEEL (ADR-0055): the same process runs in seconds instead
    of weeks.
  - **`examples/preisaenderung/`** recalculates every open quote when the price list
    changes, and is the signal example — deliberately paired with the one above, because
    the pair is the lesson: a **message** hits exactly one instance, the one whose
    correlation key matches; a **signal** hits **all** that are waiting and does not know
    how many that is. Verified against a live server: three waiting quotes, one instance
    of the thrower, three recalculated quotes.
  - **`examples/reisestorno/`** books a flight and a hotel, has the payment declined, and
    takes both back — the compensation example. An error jumps out of an activity that
    just went wrong; a compensation undoes activities that completed *successfully* long
    ago, which is the case a rollback is actually about. It unwinds backwards, and the
    handlers hang off an `<association>` rather than a sequence flow — the proof being
    that a run with `zahlungOk: true` carries no cancellation variables at all.

  All three are framed for the readers the examples were thinnest on: a small business and
  a private person. All three run with no worker, no credential and no network.

  A fourth, **`examples/umzug/`**, is there for the audience alone rather than a
  mechanism: organising a move, because a workflow engine reads as something for
  corporations until somebody shows it doing a private person's Saturday. It happens to be
  the only model in the tree that fires a timer on a **computed date** rather than after a
  duration — and its two trip hazards are documented because both actually happened while
  it was being built, and both produce the same incident: a process variable is persisted
  as JSON and comes back a string (so the date has to be parsed again), and a zone id
  where the timer needs an offset.

- **The handbook now shows every example Atlas ships, and what it takes to run one.**
  Thirty scenarios live under `examples/` — a shopping cart that computes a total in
  FEEL, an exam with a hard deadline, a CSV checked row by row, a directory recertified
  against the HR system, a Google Form whose every new row becomes a case. The handbook
  showed two of them. `examples/README.md`, the only overview there was, is written for
  developers, is half in English, and was missing five examples entirely, because nothing
  checked.

  The new **Beispiele** chapter describes all of them in both languages and on two levels
  at once: what the scenario is *for* — who has the problem, what they get out of it — and
  how it is *built*, down to the trap the reader is about to walk into (`query-one`
  returns null and fails on two hits; a Sheets cell is text, so comparing it with a number
  is `null` in FEEL; a Jira user search without the browse permission finds nobody
  *without failing*). Each card renders the real diagram, and installs the real artifacts —
  application, decision, forms, processes, publish — into the reader's own instance in one
  click. Nine of them then start with one more click, most running to an end event with no
  worker configured at all.

  Alongside it, **Worker in Betrieb nehmen**: a runbook per worker type for the half that
  happens outside Atlas and is where commissioning actually fails. The Google service
  account and the sharing step without which a document you have open in front of you
  answers 403; the Entra app registration with the two application permissions that cover
  a joiner/mover/leaver flow and the one to remove if it is there; the Atlassian API token
  and the global permission an assignment needs; the AD service account that should be
  delegated on an OU rather than made a domain admin; the SQL user that should be granted
  on views. With, for each, the symptom that tells you what is missing — a parked token
  with no incident is a worker that is not running, an empty search result is usually a
  permission.

  The models are not copied into the page. They travel as one generated asset,
  `api/web/examples-catalog.json`, which `go test ./examples -update` builds from the
  files; a plain run fails when the served catalogue has drifted from them, when an
  example has no card, when a card names an example that does not exist, or when two
  examples would ship the same form id and installing the second would silently overwrite
  the first one's form. That last one was not hypothetical: `onboarding` and
  `entra-onboarding-selfservice` both shipped a form called `onb-start`, and the
  self-service pair is now `eonb-start`/`eonb-freigabe`.

- **The two diagram-js canvases ship as one bundle.** The ArchiMate canvas
  ([ADR-0189](docs/adr/0189-panorama-architecture-modeling-and-live-overlays.md)) and
  the UML class canvas ([ADR-0237](docs/adr/0237-class-canvas-on-diagram-js.md)) each
  carried their own copy of the library, because the second arrived later and merging
  them then would have meant touching Panorama's shipped canvas for a saving that was
  real but not urgent. ADR-0237 named the merge as the follow-up; this is it.

  Both now ship as `api/web/vendor/canvas/atlas-canvas.js` under one global with a
  namespace each — 123,109 bytes where the two were 211,888, and one cache entry rather
  than two. Neither canvas's own code is touched: two entry files became two modules
  under one entry that exports both. The honest cost is on the other side: a page that
  opens only one of the two now carries both renderers, some 15 KB more than its own
  bundle was — the right way round, since the renderers are the small part.

  Loading it moved into one place (`api/web/canvas-bundle.js`), because two views
  fetching the same file is new: whichever is opened first fetches it and the second
  gets what is there, rather than a second `<script>` for the same bytes.

- **Runtime badges no longer sit on the names they are pointing at.** The Operations
  views annotate a shape with badges — token counts, an incident marker, a link to a
  waiting task, a decision to inspect — and each has its own corner, which is how an
  operator learns to read them without reading them. The corners are unchanged. What was
  wrong is that a corner meant *inside* the shape, and inside the shape is where the words
  are.

  Measured in a browser on an ordinary model, the old placement covered 69 of the 74
  pixels of a line of a business rule task's name with the decision badge, and put the
  token count on the captions of both the start event and the gateway. Two different
  causes: a task's caption is drawn inside its box and a three-line name leaves about ten
  pixels clear, which a 20px badge does not fit in; and an event's caption is not inside it
  at all but centred underneath, four to five times wider than the circle — exactly where a
  badge anchored to the bottom corner lands, because diagram-js's `bottom` and `right`
  overlay keys position a badge's top-left corner rather than anchoring its far edge.

  A badge now hangs outside the shape, on the side its caption is not: above and below for
  a task, above only for an event, a gateway or a data object, whose name is drawn
  underneath them. And it is the size of a count rather than of a sentence — a pill wide
  enough for "⚠ 2 incidents" is 90px, which is most of a task and three times an event, so
  two of them collide with each other wherever they are put. The incident marker, the task
  link and the decision button are a glyph plus a count now; the words they used to spell
  out are their tooltip and their accessible name, and the thing they name is listed in the
  panel below the diagram either way
  ([ADR-0252](docs/adr/0252-runtime-badges-clear-of-labels.md)).

- **A count on the diagram is grouped in thousands.** Reported from a running process:
  badges reading `25864`, `50002`, `23436`, `2428` around the shapes of one diagram.
  Every number was right and none of them was legible — a five- or six-digit run is read
  by counting digits, and two of them side by side cannot be compared at a glance at
  all, which is the only reason the counts are drawn on the shapes instead of listed in
  a table.

  Every count the runtime views print now groups in threes — `25 864`, `50 002`: the
  live view's three token badges and their tooltips, the replay's execution-count
  badges, the incident badges, the Playground's run and heat-map badges, the count pills
  in those views' headers, and — same engine counters, same problem — the Starmap's
  running total on a node and its running/finished tally in the panel. Anything under a
  thousand is untouched; a separator on `999` is noise in a pill that small.

  The separator is a **narrow no-break space** (U+202F), not a locale's own mark.
  A process is modelled in one country and operated from another: `25.864` is
  twenty-five thousand to one reader and twenty-five point eight to the next, `25,864`
  the same disagreement mirrored, and a badge has no room to say which it meant. A space
  is the one grouping mark no locale reads as a decimal point (ISO 31-0), and the
  no-break variant keeps a badge on one line at any count. `toLocaleString()` was the
  other candidate, and it is wrong here for the reason it looks right: it would make the
  separator a property of whoever is looking, so the same screenshot pasted into a
  ticket would say something different to the person who received it. The whole choice
  is one constant in `api/web/numfmt.js` — a house that wants the Swiss `25'864` changes
  it there, in one place, and every badge follows.

- **Google Sheets runs on a worker, like everything else.** It shipped with an
  in-engine handler and no supervised form, so the Modeler's properties panel showed it
  as the one Worker Type reading IN-ENGINE while the twelve around it said otherwise —
  and a fresh install called Google, with a service-account private key, from the
  engine's run loop. ADR-0164 has one exception left and it is the FEEL script task;
  this was not meant to be a second.

  The engine now hands each configured Google identity to the worker it supervises
  (`ATLAS_GOOGLESHEETS_*`, the whole credential bundle as one opaque value, the way
  SharePoint and the SQL kinds do), the `worker` package serves the kind, and
  `googlesheets` joins the default offload set. The in-engine form stays as the opt-in
  `--in-process-connectors` fallback every managed kind keeps.

  The guard that should have caught this was a canary asserting the *opposite* — that
  some managed kind was still unprovisioned, so a related check could not become a
  tautology. Google Sheets was the last one. It is now inverted: every managed Worker
  Type must be handed to its supervised worker, so the next kind added without that
  fails a test instead of a properties panel.

- **The live diagram tells a token that got through from one that was cancelled — and
  draws a deferred choice once.** An element on the runtime overlay carried two numbers:
  green for the tokens live on it, gray for the ones that had "passed through". Gray was
  `visits − tokens`, and a visit is recorded on *activation*, so it counted every token
  that had arrived and left — whether it completed and moved on, or was terminated: a
  losing event-gateway branch, an activity a boundary event interrupted, a scope torn
  down.

  On an **event-based gateway** ([ADR-0110](docs/adr/0110-event-based-gateways.md)) that
  was not imprecision but a wrong answer, because cancellation there is not an exception
  — it is half of every outcome. The gateway arms *all* of its branches and completes
  itself, so a waiting instance holds a token on each branch and none on the gateway,
  and every decided race activates both branches and leaves both. The two branches
  therefore showed the *identical* pair of numbers whatever had actually happened — on
  one production diagram, `10 941` gray and `50 002` green on the message branch and the
  same on the timer branch, which reads as "these two events arrive equally often" and is
  not what either number means.

  Three things changed, none of them in the engine's semantics. A terminated element
  instance now bumps a retained counter of its own, per instance and per definition,
  mirroring the visit counters beside it ([ADR-0022](docs/adr/0022-element-visit-history.md),
  [ADR-0080](docs/adr/0080-runtime-aggregate-counters.md)) — a write-only merge on the
  fold path, derived from the committed event alone, so replay rebuilds it. The overlay
  splits the old gray badge into **gray = completed here and moved on** and **amber =
  cancelled here**, so which event actually arrived is now readable at the branch itself.
  And an event gateway's race is drawn as the one wait it is: the green count moves onto
  the **gateway**, and its armed branches carry a dashed green outline and no live count
  of their own — what that outline means is said once, in the live view's legend, which
  shows the entry only for a diagram that has an event gateway in it. A catch joins its
  gateway's group only when the gateway is its sole way in, so one reachable from
  elsewhere as well keeps its own count.

  **The history is reconstructed, not started from zero.** Terminations were never
  counted before this, and a missing one is not a neutral gap: gray is *derived* as
  visits − live − terminated, so every uncounted cancellation reads as a completion. On
  a real event gateway with 70 563 visits and 20 561 decided races that was 19 881 old
  cancellations sitting in gray, making both branches look like near-equal winners —
  precisely the misreading this change is about. The lifecycle trail
  ([ADR-0136](docs/adr/0136-terminated-tokens-in-the-replay.md)) has recorded every one of
  them all along, so the counters are rebuilt from it once at startup, alongside the
  other one-time counter seedings. It tops each instance up to what the trail says
  rather than summing, so a store that already ran the counting build is corrected
  instead of doubled. Two limits, stated rather than hidden: an instance whose history
  has been purged has no trail left to count, and a migrated instance's older
  cancellations land on the version it runs under now.
  ([ADR-0249](docs/adr/0249-overlay-cancelled-tokens.md))

- **A data object can be pointed at a class you can see.** The **Type** of a data object
  in the Modeler is the link the whole information model turns on — it is what lets two
  processes agree that their `order` is the same kind of thing, and what a write to a
  member of it is checked against. It was made by remembering a class name and typing it
  into a box labelled *optional*, with the modelled classes hidden in a `<datalist>` that
  nothing on the field mentioned.

  The field now offers **the classes this application models**, grouped by the model they
  live in and each carrying its business key — the fact that tells two similarly named
  classes apart, and the thing you are actually trying to recall. It fills a free-text
  field rather than replacing it: a diagram is routinely drawn before the vocabulary it
  names exists, and typing a class nothing models yet has to stay possible
  ([ADR-0230](docs/adr/0230-process-information-model.md)).

  Below it, **the class itself is shown** — its kind, its members with their types and
  cardinalities, and its business key marked exactly as the class canvas marks it — with
  a link that opens the model in a new tab. Reading a name back tells you nothing about
  whether it is the right class; its business key does, and that was one application of
  the console away.

  And when the type names a class nothing models yet, **Model it now** adds it where it
  belongs instead of sending you off to do it by hand. It is added as a business object
  with no attributes and no business key: those are the author's to choose, and guessing
  them would be worse than leaving them open.

- **A data object's value opens as formatted JSON.** In an instance's **Data** tab, a
  structured value showed as `{3 fields}` and the whole of it was reachable only as a
  tooltip — unreadable past a few lines, impossible to scroll, select or copy from, and
  absent altogether on a touch device. The summary is now a button, and it opens the
  same pretty-printed, syntax-highlighted window the **Variables** tab opens, with the
  same Copy JSON. A data object is variable-shaped by design
  ([ADR-0053](docs/adr/0053-first-class-data-objects.md)), so the two tabs
  should answer "what is actually in there" with one surface.

  Every write in the state trail opens too, and each window says which write it is
  showing — a trail of four `{3 fields}` is unreadable if every window is titled the
  same. Scalars are left alone: a string is already whole in its cell, and a button
  around it would promise a second reading that does not exist.

- **The class diagram's properties panel is the Modeler's panel.** Selecting a class, a
  data store or a relationship under **Data › Information model** now gives you the same
  panel the BPMN Modeler does: a header naming what is selected — its kind in small
  type, its own name in bold, a type chip beside it — and collapsible property groups
  below, each with a chevron and a filled dot when it carries content. Fields look like
  fields do everywhere else in Atlas.

  It is the same panel because it is the **same code**, not a lookalike. The Modeler had
  grown the shape first, as a function inside `editor.js` that turns a rendered panel's
  sections into groups. That is exactly the kind of thing worth having once: a copy
  drifts from its model the first time either side is touched, and then two panels a
  person uses in one sitting disagree about what a group is. It moved to
  `api/web/pgroup.js`, and both panels call it.

  What the two panels do *not* share is which groups start open, because the honest
  answer differs. A BPMN element has a dozen sections and opening one of them is the
  point, so only **General** starts open. A class has three, and one of them is its
  attributes — the attributes *are* the class, so hiding them behind a click on every
  selection would be worse than having no groups at all. So the class panel opens
  everything, and collapsing is there for when a long attribute list is in the way.

- **Central decisions run on a worker now — and the last in-process kind is gone**
  ([ADR-0233](docs/adr/0233-in-process-connectors-refused.md), slice 7).
  A call to a decision service somebody else operates no longer happens on the loop
  that owns the partition's state. `temis` joins the kinds Atlas offloads and
  supervises by itself, which empties the record's "owed a worker half" table: every
  kind that reaches another system now has one.

  This slice did not copy the six before it. A central decision is a **business rule
  task**, and its completion carries something no other job's does — a durable
  evaluation record with the inputs, the outputs and the service's trace, retained so
  an operator can see how a decision was made (ADR-0066). Nothing in the
  engine↔worker protocol could carry one, so the completion contract widened: a
  worker may now report the evaluation it performed, and `atlas worker` sends it.

  The division is the part worth knowing. The worker is believed about the
  **evaluation** and about nothing else: which element instance it belongs to is
  stamped by the engine from the job the worker held a lease on, so a report cannot
  attach itself to a task it did not run. A completion *by hand* never carries one at
  all — an operator override is recorded as an intervention (ADR-0159), and letting
  that path write an evaluation would put a decision nobody made into the audit trail.

  What has **not** changed, though it looks like it should have: the record's
  provenance. A central decision's trace was always the remote service's account of
  its own evaluation. Offloading moved which process makes the call, not who authored
  the trace.

  `--in-process-connectors` keeps working. The record originally said it would become
  an error once the table emptied; that promise contradicted its own driver that no
  running deployment may break, and the amendment explains which of the two was
  wrong.

- **SCIM tasks run on a worker now**
  ([ADR-0233](docs/adr/0233-in-process-connectors-refused.md), slice 6).
  Creating, reading or searching a user at an identity provider no longer happens on
  the loop that owns the partition's state. `scim` joins the kinds Atlas offloads and
  supervises by itself.

  It is REST's slice a third time, and the collector says so: `scimWorkerEnv` is the
  third caller of one function rather than a third copy of it, and a test now deploys
  a REST, a SOAP and a SCIM task together and holds that each worker gets its own
  kind's secret and none of the others' — the failure a shared implementation makes
  easy, and one that every per-kind test would pass.

  One choice is deliberate: the payload carries the **authored** operation, base URL,
  resource, id and filter, not the HTTP method and URL derived from them. A parked
  job's payload is something an operator reads, and "operation: create, resource:
  Users" answers what they came to ask where "POST .../Users" makes them work
  backwards — and the derivation's own refusals (a `get` with no id would otherwise
  become a list of every user) belong in `Run`, where both halves reach them.

  One kind remains in-engine for want of a worker: `temis`.

- **SharePoint tasks run on a worker now**
  ([ADR-0233](docs/adr/0233-in-process-connectors-refused.md), slice 5).
  Creating a list item — a token fetch and an HTTP round trip to Microsoft Graph — no
  longer happens on the loop that owns the partition's state. `sharepoint` joins the
  kinds Atlas offloads and supervises by itself.

  It is Jira's handover with a document library in place of an issue tracker
  ([ADR-0141](docs/adr/0141-sharepoint-connector.md)): the task names its instance and
  nothing more, because the Graph endpoint and the OAuth bundle are a worker record and
  a vault secret — a URL is half a credential — so `sharepointWorkerEnv` renders the
  instances the engine has configured.

  One difference is deliberate: the credential is handed over as the **whole bundle**,
  one opaque value, rather than field by field. The bundle has no public half worth
  splitting — tenant and client ids sit in the same vault secret as the client secret
  and the refresh token — and splitting it would mean deciding the grant's shape a
  second time, where getting it wrong yields a worker that fails every job instead of
  one that will not start. That is the SQL kinds' arrangement for the SQL kinds' reason.

  An instance whose bundle does not resolve is left out rather than handed over empty,
  so one unfinished record cannot stop a worker that also serves other kinds.

  Two kinds remain in-engine for want of a worker: `scim`, `temis`.

- **SOAP tasks run on a worker now**
  ([ADR-0233](docs/adr/0233-in-process-connectors-refused.md), slice 4).
  A call to somebody else's web service no longer happens on the loop that owns the
  partition's state. `soap` joins the kinds Atlas offloads and supervises by itself.

  It is REST's slice with an envelope around it, and that is the whole argument: the
  endpoint, the SOAPAction and the body are model data and travel resolved with the
  job, while the credential behind the task's `authSecret` is a vault **reference**
  that is resolved where it is used. `soapWorkerEnv` is `restWorkerEnv` with a
  different job type — the two collectors are now one function called twice rather
  than two that drift, and a test holds that neither picks up the other kind's tasks.

  Both halves go through one `soap.Resolve`/`soap.Run` pair; the in-process handler
  was rewritten onto it, and the result travels through `soap.Result` so a task naming
  no result variable completes with nothing rather than with an empty object.

  Three kinds remain in-engine for want of a worker: `sharepoint`, `scim`, `temis`.

- **LDAP tasks run on a worker now**
  ([ADR-0233](docs/adr/0233-in-process-connectors-refused.md), slice 3).
  A bind, a search or a modify against a directory somebody else operates no longer
  happens on the loop that owns the partition's state. `ldap` joins the kinds Atlas
  offloads and supervises by itself, so a fresh install gets it without configuring
  anything.

  It is Active Directory's handover exactly (ADR-0182), because the two kinds share a
  shape: an LDAP task authors its own server and bind DN, so those travel with the
  job, while its bind password and client certificate are vault **references** — and a
  reference is resolved where it is used. `ldapWorkerEnv` renders the references the
  deployed models actually name, resolved through the vault, under the
  `ATLAS_CONNECTOR_<REF>_TOKEN` names the worker already reads. Both flavours are
  covered: a certificate reference nothing answers to is a bind that cannot present an
  identity, which is no better an outcome than a missing password.

  The worker keeps the connection pool ADR-0154 introduced, so relocating the work
  does not give back the reason binds were pooled in the first place. And both halves
  now go through one `ldap.Resolve`/`ldap.Run` pair — the in-process handler was
  rewritten onto it — so an offloaded search and an in-engine one cannot drift about
  what an LDAP task means.

  Four kinds remain in-engine for want of a worker: `sharepoint`, `scim`, `soap`,
  `temis`.

- **A non-interrupting message or signal boundary event now fires every time, not once**
  ([ADR-0236](docs/adr/0236-repeating-non-interrupting-boundary-events.md),
  refining [ADR-0040](docs/adr/0040-boundary-events.md)). Non-interrupting is how a model
  says *reminder*: fire beside the host activity and leave it running. For as long as the
  host runs, every occurrence should fire it again — and a recurring **timer** boundary
  already did ([ADR-0054](docs/adr/0054-date-cycle-timers-for-catch-and-boundary.md)),
  as does a non-interrupting event-subprocess trigger
  ([ADR-0082](docs/adr/0082-event-subprocesses.md)).

  A **message** or **signal** boundary did not. Taking its outgoing flow completed the
  boundary's element instance, and its subscription retired with it, so the second message
  correlated to nothing: no incident, no log line, nothing for the sender to see. Two
  constructions a modeller reasonably reads as interchangeable disagreed about whether
  "non-interrupting" means "repeatedly".

  It now fires the way a recurring timer boundary fires — take the outgoing flow, re-open
  the subscription, never complete the element instance. Staying armed rather than
  completing and arming a replacement is deliberate: a replacement is only a queued
  command for the rest of the batch, and a boundary instance is counted against its
  scope, so a host completing in that window would leave an armed instance holding open a
  scope that has already drained.

  This changes behaviour for deployed models: one that relied on hearing the message once
  will now hear it each time. The old behaviour was a defect and gave no way to depend on
  it deliberately; a model that wants exactly one firing has the interrupting flag, or a
  guard on the reminder branch. The escalation and conditional boundary kinds are
  untouched — they arm inert and are *found* rather than waiting on a subscription, which
  is a separate question.

- **clio tasks run on a worker now**
  ([ADR-0233](docs/adr/0233-in-process-connectors-refused.md), slice 2).
  Writing an event, folding a subject's state, reading its history: three round trips
  to an event store somebody else operates, all of them on the loop that owns the
  partition's state. clio joins the kinds Atlas offloads and supervises by itself.

  It is Remedy's handover with an event store in place of an ITSM instance — the
  endpoint is a connector record, the token a vault reference behind it, and
  `clioWorkerEnv` renders the stores the engine has configured. One difference is
  deliberate: a store with **no** token is still handed over, because clio can be
  reached without one and dropping it would leave a working instance unserved.

  A clio write also carries something no other kind does — the event **body**, which is
  the task's input mappings or the variables it sees. That is engine state, so it is
  resolved in the engine and travels in the payload beside the idempotency key that
  de-duplicates a retry. Both halves now go through one `clio.Run`: the in-process
  handlers were rewritten to call it, so an offloaded write and an in-engine one cannot
  disagree about what a clio task means.

  Five kinds remain in-engine for want of a worker: `sharepoint`, `scim`, `ldap`,
  `soap`, `temis`.

- **REST and LDIF tasks no longer run on the engine's own loop**
  ([ADR-0233](docs/adr/0233-in-process-connectors-refused.md),
  finishing [ADR-0164](docs/adr/0164-no-in-process-service-tasks.md)). ADR-0164 decided
  two years ago that a side-effecting service task belongs on a worker, and then chose
  deprecation over a ban for one stated reason: a connector task could not run on a
  worker yet. ADR-0168 closed that, and the worker halves have landed kind by kind
  since — but the *default* never moved, so a fresh install still made outbound HTTP
  calls from the processor's own process. `rest` and `ldif` now join the kinds Atlas
  offloads and supervises by itself.

  REST needed what Active Directory needed: its endpoint travels with the job, but its
  `authSecret` is a vault reference, and a reference is resolved where it is used — on
  a supervised worker, a child process with no vault. The engine now renders exactly
  the references its deployed models name into that child's environment, under the
  `ATLAS_CONNECTOR_<REF>_TOKEN` names the worker already reads. Only what is deployed
  travels: the running models' secrets, not the vault.

  What is still in the engine is now a list rather than a condition — `clio`,
  `sharepoint`, `scim`, `ldap`, `soap` and `temis` each need a worker half, one slice
  each, and the record names them. Beside them stands the closed list of what stays
  in-engine on purpose: FEEL, local DMN, the mockup task, timers, user tasks, and user
  provisioning (which mutates Atlas's own store and has no endpoint to reach).
  `--in-process-connectors` keeps working and now says at startup that it puts every
  integration back on the run loop.

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

- **The Modeler's bar carries three controls now, and a menu for the rest.** It ended
  with seven, added one at a time as the editor grew — Token simulation, Variables,
  Auto-layout, Save, Export XML, Documentation, Deploy — and every one of them was the
  same white button. That said they were the same size of decision, which they never
  were: Auto-layout nudges boxes, Deploy puts a definition on a server and cannot be
  taken back ([ADR-0229](docs/adr/0229-modeler-bar-hierarchy.md),
  [ADR-0240](docs/adr/0240-modeler-variables-on-the-bar.md)).

  The bar now carries **Variables**, **Save** and **Deploy**, with Deploy the only filled
  button because it is the only act there that leaves the browser. Token simulation,
  Auto-layout, Export XML and Documentation moved into a **…** menu beside them, where a
  toggle says it is on with a check and with `aria-pressed`.

  **Variables stayed on the bar, as a proper two-state button** — tinted while the panel
  is open, muted while it is shut, and separated from Save and Deploy by a rule, because
  what the bar *shows* and what the bar *does* are different kinds of thing. It is the one
  control here that is neither an act nor a mode but a reading aid, consulted briefly and
  often while writing something else, and a menu taxes that every time. It answers **F4**
  now, alongside Auto-layout's F8 — and unlike F8 it works while a field has focus, since
  the moment you most want to know what a variable is called is while typing the
  expression that uses it. Its pressed look is drawn straight from `aria-pressed`, so what
  a screen reader is told and what you see cannot drift apart; the toggle never announced
  its state at all before.

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
  wherever focus sits. The cost is honest and worth naming: four controls are a click
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

- **The class canvas is a real diagram canvas.** **Data › Information model** now draws
  on diagram-js — the same library the BPMN modeller runs on — so a class box has a
  selection outline you can see, moves with the rest of what you selected, and stays put
  when you type. Marquee-select a group of classes, drag the sheet to pan, scroll to
  zoom, nudge a box with the arrow keys, and undo a move you did not mean with Ctrl+Z.

  None of that was missing on purpose. The canvas it replaces was hand-rolled SVG plus a
  pointer-drag, which is a diagram library with everything hard left out: no selection
  model, no undo, no zoom, no keyboard. Writing those is not the interesting part of a
  class diagram — how a class, a data store and the four association kinds are drawn is,
  and Atlas still owns exactly that, plus which of them the subset permits between which.

  The drawing is now **reconciled rather than redrawn**. The editor re-renders on every
  keystroke, and a redraw would have thrown away the zoom, the selection and the undo
  stack on each character — so an edit updates the shapes that changed and leaves the
  view alone.

  It edits locally, which is the one place it parts company with the Panorama canvas
  beside it ([ADR-0189](docs/adr/0189-panorama-architecture-modeling-and-live-overlays.md)). That canvas never
  creates anything: the server owns the document and the view is re-read. An information
  model is a working copy with an explicit **Save**, which is what lets you draw three
  classes and two relationships and *then* decide — and what makes an undo stack mean
  anything at all. The rules still refuse whatever the served subset refuses; they just
  refuse it at the point of drawing rather than at the point of writing, and say the same
  sentence either way.

  Honest cost: the binary now carries **two copies of diagram-js**, one for each canvas.
  Merging them into a single bundle that exports both viewers is the named follow-up in
  the record; it was left out of this change so this change would not touch Panorama's
  shipped canvas.

- **Every vendored bundle is held to the checksum its own recipe records,** not just the
  first one that was. `ATLAS-VENDORED.txt` says "do not edit this by hand" and records
  the SHA-256 the documented rebuild produces; the guard that checked that named one
  bundle, so a second one arrived uncovered. It now walks the vendor directories, which
  is how it immediately found that the DMN Modeler bundle recorded no sum at all — a
  hand-edit or a forgotten rebuild there would have been invisible. That sum is recorded
  now.

### Fixed

- **A batch persists the work it still owes, so an instance interrupted at a batch boundary
  moves again.** Only events were durable, and an event says what *happened* — not what the
  batch had queued up to do next. A crash between two batches therefore recovered an instance
  that was materialised perfectly correctly and then stood still forever, because the internal
  follow-up commands that would have carried it on had never been written down
  (ADR-0271, audit F01).

  The batch frame carries the internal commands still outstanding at its end, and recovery
  seeds the queue from them. This touches invariant I6 ("only events are persisted") in the
  letter and not in the spirit: the section is never folded by `applyToState`, and it is not a
  fact about the process but the log's own bookkeeping that this batch had not finished its
  work. I4 is untouched.

  Two defects surfaced only because the acceptance matrix interrupts at *every* batch boundary,
  with both a preserved and a rebuilt state — neither was reachable by thinking about it, and
  neither showed up in the auditor's single reproduction. A batch that owes nothing writes a
  zero-length continuation, because writing none left the previous one newest and replayed it:
  duplicate jobs. And the continuation lifts the key counter above every key it carries, because
  a queued command already holds a key no event has mentioned yet — the event is precisely what
  the crash prevented — and the restart would otherwise mint that number a second time. The
  symptom of *that* was a missing element instance and no error at all.

  A reflection test enumerates the fields of `engine.Command` and fails as soon as one appears
  that it cannot classify. The continuation only needs the closed subset the internal follow-ups
  use; without the guard, every future field would be silent data loss on restart.

- **The full backup contains every store, because one inventory says what is on disk.**
  `backupDirs`, `fullBackupDirs` and the restore allowlist were three hand-kept lists living
  nowhere near the code that creates the stores, and twelve directories had drifted out of the
  full backup — the vault among them. Nothing reported it: the backup succeeded, and what it did
  not contain was discovered at restore (ADR-0282, audit F05).

  Every persistent store now registers itself with its directory, its backup class (design-time,
  credential, secret, runtime, ephemeral) and its restore dependency, and all three lists are
  *derived* from that. The archive carries a version and content manifest, and an import refuses
  one missing a mandatory part.

  The test is the durable half: it starts a server, reads the real data directory, and fails as
  soon as a directory exists that no backup class claims. That is what stops the thirteenth
  store from reopening the same hole — the twelve did not drift out through carelessness. It
  found two categories no list would have: stores that come into being only when their feature
  is used (`checkpoints`, `dmn-models`, `exporter` — absence is fine, omission is not), and one
  the archive carries by its own mechanism rather than by walking the tree (only the newest
  verified checkpoint goes in, deliberately). Both are properties of the registry now, not
  footnotes. It has already earned its keep once: `main` added a `task-folders` store, and the
  merge ran aground on it rather than a backup omitting it in silence.

- **A cancellation sees the child created in its own batch.** `ChildInstancesOf` read through
  the committed store rather than the running transaction, so terminating a parent missed any
  child instance created in the same batch — the cascade walked past it and it kept running with
  nobody left to own it (ADR-0284, audit F07). ADR-0238 justified the commit-only view with
  determinism; that derivation is wrong, and reversing a recorded decision takes a record rather
  than a quiet fix: the already-applied events of the running transaction are a deterministic
  part of command processing.

  The transactional read finds the child but does not finish the job. The cascade queues a
  terminating command for the child while the child's own start event is still in the queue; that
  runs first and rebuilds exactly the execution the cancellation had just removed, leaving an
  element instance and an activatable job behind. A terminated instance now drops its unprocessed
  work. Commands are never persisted and never replicated (I6), so this changes what runs next
  and nothing about what recovery reconstructs.

  A review of the surrounding code found the feared surface does not exist: in the whole `engine`
  package exactly two reads bypass the transaction — this one, and `ForEachStartTimer`, which runs
  at deploy time and not inside a batch.

- **A hanging worker no longer blocks unrelated work.** `driveMu` covered the entire drain loop,
  `jobRunner.Work` included, so one slow or wedged handler held the lock for its whole timeout and
  every unrelated start, completion and cancellation queued up behind it (ADR-0274, audit F13).

  Narrowing the lock was the smaller half. The identity was the work: an in-process claim had no
  claim identity, so loosening the mutex would have introduced double execution. `Claim` leases
  now — activating under the name `atlas:in-process`, which takes the job out of the activation
  index before `Claim` returns, so a second claim cannot see it — and `Submit` checks lease and
  epoch before applying, behind the same fence an external worker meets at the HTTP completion.
  Only then could `driveMu` be narrowed to claim and submit.

  It is deliberately not removed. "The work my request started is done when it returns" is a
  contract every request path and a great many tests depend on, and serialising two short steps
  keeps it at no measurable cost.

- **An inclusive join's ancestor set is compiled, not rebuilt on every arrival.** Every token
  reaching an inclusive join rederived reachability across the whole graph — reverse adjacency,
  map and stack, allocated and thrown away per token movement — when a compiled process is
  immutable and its ancestors had been the same the previous thousand times. That is invariants
  I1 and I5 at once (ADR-0279, audit F14).

  It is computed once at build, as a bitset per relevant join site: maps would have done the same
  job and cost about half a megabyte per deployment for ten joins in a thousand nodes, where the
  bitset costs a good kilobyte. The name moved with it — `NodesReaching` sounded like a general
  graph query and answered for any node, `InclusiveJoinReach` says which ones it is for, and that
  matters: an empty set at a real join reads as "nothing upstream" and fires it too early. A
  compiler test holds every inclusive join to having one.

- **A poll costs a page, not the backlog.** `ActivatableJobs` walked the entire index even after
  it had the jobs it was asked for, because the callback kept returning `nil` past `want`. The
  capability was never missing — the scan has always stopped on a callback error, and the API layer
  has had `errListTruncated`/`unlessTruncated` for exactly this; the two polling sites simply had
  not used it (ADR-0270, audit F15).

  The worker pull stops at the page it asked for, and the in-process `Claim` takes a round instead
  of the whole backlog, with an equal share per served type. Both callers already loop until empty,
  so the bound costs a round and never a job. The durable half of the correction is in the doc
  comment on `ActivatableJobs`, where the next caller reads what the contract is.

- **Both HTTP servers have explicit read-header and idle timeouts.** A client could open a
  connection and take as long as it liked to send its request headers, and an unused keep-alive
  connection was never reclaimed (audit F17). `ReadHeaderTimeout` is 10s and `IdleTimeout` 120s on
  both of this process's servers — the public listener and the loopback one.

  `ReadTimeout` and `WriteTimeout` stay at zero, and that is a decision rather than an oversight:
  the first would bound the whole request read and a restore upload is legitimately long, the
  second would bound the whole response write and a worker long-polling for a job or a streamed
  backup would be cut off mid-flight. Those endpoints carry their own deadlines per handler, where
  the right number is known.

- **The What's New generator refuses a conflicted CHANGELOG instead of shipping both
  sides of it.** `api/web/whats-new.json` is generated and committed, and
  `.gitattributes` marks it unmergeable so git raises a conflict rather than
  interleaving two generated files. The documented resolution is to take the merged
  `CHANGELOG.md` and re-run the generator — but the two files change together, so at
  that moment the CHANGELOG is usually conflicted too, and the generator read straight
  past the markers: it looks for `- **bullets**`, and `<<<<<<< HEAD` is not one.

  Both sides then became two entries in a feed that looked perfectly well-formed, and
  CI's staleness check *passed*, because the committed file really was what the
  generator produced from that source. Only a reader would ever have found out. Measured,
  not assumed: a conflicted CHANGELOG produced a clean exit and a feed containing both
  bullets.

  It now refuses, naming the file and why — for `CHANGELOG.md` and for a conflicted
  override, where the JSON parse error would otherwise send the reader looking for a
  typo rather than for the merge they are in the middle of. A Go test drives the real
  script against a throwaway tree, so the guard is exercised rather than asserted in
  prose.

  `make whats-new-resolve` is the resolution in one command: it regenerates the feed
  from the merged CHANGELOG and stages it, and refuses while any *other* conflict is
  still open — regenerating from a half-merged CHANGELOG being exactly what the guard
  above exists to stop.

- **The handbook blamed itself for a diagram the reader was simply not signed in to
  see.** The recipes in _Rezepte_ ship their models without BPMN-DI, so the coordinates
  come from `POST /api/v1/layout` — an endpoint that carries the `modeler` role, on a
  page that is public. A reader who was not signed in therefore got no picture on any
  of the 28 cards, and the note under each one said Atlas *"cannot lay this pattern out
  completely yet"*. That was never true: Atlas lays them out fine, the request was
  refused. The note now separates the three answers — sign in (with a link that takes
  you there), a session that lacks the `modeler` role, and an actual layout limit, which
  is the only one that is about the model. The first refusal also settles the chapter,
  so the 27 further requests that could only fail the same way are no longer sent.

- **Every instance start scanned the whole runtime on the single writer.** `/stats` looks
  like an aggregate and is not one: all three of its counts walk a whole column family,
  so on a server holding 50.000 instances and 200.000 tokens a single call was a
  quarter-million-key scan. ADR-0080 introduced maintained counters, but for the
  per-definition sums the Prometheus path uses — the counts behind `/stats` are the
  authoritative scans it is explicitly contrasted with.

  The endpoint was not the main caller. Seven of the eight callers are **write paths**
  that report the counts back in their response — starting an instance, publishing a
  message, cancelling or terminating a batch, a CSV upload — and each took a run-loop
  turn of its own purely for that read-back. So every instance start paid a full
  population scan on the single writer, and a load generator kept the engine executing
  one per write, indefinitely. That is what made the API unreachable; the parked test
  instances were not the load, they were the size that made each read-back expensive.

  `readStats` now takes a `state.ReadView` instead of the live store, which moves the
  counting off the run loop and makes it impossible to spell the on-loop version: a
  caller must obtain a view. All eight sites go through one helper, so the write paths
  were fixed by the same change as the endpoint. `GET /api/v1/incidents` was the same
  defect in its milder form — two rows to return and its whole walk, its per-instance
  lookups and its deployment-map reads inside the loop — and now runs off it too. Both
  reads are snapshots, so a page can no longer mix an instance counted before a write
  with a token counted after it. Both also answer 503 while the server is shutting down
  rather than a 200 that cannot be told apart from a true empty answer: the old code
  reported `{"activeProcessInstances":0,…}`, which reads as "the engine is empty". The
  reasoning is in [the record on the runtime counts](docs/adr/0266-stats-and-incidents-off-the-loop.md).

  `/stats` is no faster for its own caller — it still walks the population. It simply no
  longer walks it for everybody else.

- **Signing in waited for the engine, so a busy server locked everybody out.** On a
  server carrying ~50.000 parked process instances, with load generators still starting
  and finishing more, `POST /api/v1/auth/login` stopped answering while
  `GET /api/v1/info` answered instantly. Nothing was down and nothing was slow: the
  login was *queued*. Both of its lookups were dispatched onto the single-writer run
  loop ([ADR-0002](docs/adr/0002-single-writer-partition-model.md)), which executes one
  closure at a time in arrival order, so authentication was only ever as available as
  the processor was idle — and an operator signs in precisely in order to deal with a
  processor that is not.

  Neither lookup reads engine state. Accounts and groups are durable sidecar records
  ([ADR-0044](docs/adr/0044-user-management-and-authentication-boundary.md)) that never
  travel through the WAL or the processor; they sat on the loop by convention. They now
  read directly off it, so a login costs zero loop turns and answers at the speed of the
  filesystem regardless of engine load — matching the rest of the session path, which
  never needed the loop either ([ADR-0180](docs/adr/0180-groups-as-members.md),
  [ADR-0185](docs/adr/0185-live-group-membership.md)). Writes are untouched: the run loop
  remains the single writer of design-time state, and the OIDC callback's account
  resolution deliberately stays on it, because that path may *create* the account it is
  resolving and the check-then-write is atomic only inside one turn. A listing that meets
  a record deleted from under it now skips that record instead of failing outright, which
  is what an off-loop reader can legitimately see. The reasoning is in
  [the record on signing in off the run loop](docs/adr/0265-login-off-the-run-loop.md).

  What the login was queued *behind* is the entry below.

- **The replay drew a deferred choice as several tokens, and parked one on the gateway
  that was not there.** The live diagram stopped drawing an event-based gateway's race
  literally in [ADR-0249](docs/adr/0249-overlay-cancelled-tokens.md): the engine arms
  every branch's catch at once ([ADR-0110](docs/adr/0110-event-based-gateways.md)), so a
  waiting instance holds a token on each branch and none on the gateway, and drawn
  one-for-one that says the same wait once per branch. The step-by-step instance replay
  still drew it the old way — a token dot on every branch and a chip for each of them in
  the legend below — so the two views described the same moment differently, which is what
  a reader of both actually reported.

  Two things were wrong, and the second one was a token drawn where no token was. The
  frame fold ([ADR-0046](docs/adr/0046-single-process-step-replay.md),
  [ADR-0136](docs/adr/0136-terminated-tokens-in-the-replay.md)) keeps a completed
  element's token visible until the activation it causes appears, so the token never
  flickers out between the two. An event gateway is the one element whose successors
  activate *before* it completes — it arms the branches on activation and only then
  completes itself, taking no outgoing flow of its own — so it waited for an arrival that
  had already been and gone, and its token stayed on the gateway for the rest of the
  replay. On a looping model that is a race drawn as still running a full round after it
  was decided, which is what a production instance showed: three tokens on a two-branch
  race, one of them a ghost.

  The gateway's token is now released when it completes, like a leaf's and a loop round's
  — the other two hand-offs that go to nobody. And the replay draws the race the way the
  live view does: one token on the **gateway**, the armed branches outlined dashed and
  without a dot of their own, and one chip in the legend that names the race and says what
  it is waiting for — *waiting for the first of 2 events* — rather than one chip per
  branch. The rule is read off the diagram, exactly as the live view reads it (a catch
  joins its gateway's race only when that gateway is its sole way in), and off the token
  that forked it: every armed catch is a fork of the gateway's own token, so two races
  running at once on one gateway stay two races. Once an event has fired and the losers
  are cancelled, what is left on a branch is the winner running there, and it is drawn as
  itself again.

- **The class canvas could not take hold of more than one class at a time.**
  [ADR-0237](docs/adr/0237-class-canvas-on-diagram-js.md) put the canvas on diagram-js
  for marquee selection among other things, and the marquee was the one it did not
  reach: diagram-js ships the tool, but a plain drag on empty sheet pans — it has to,
  or a diagram larger than its window could not be moved — so the gesture was never
  offered to it, and ten boxes were still moved one at a time.

  There is now a control for it beside zoom and undo, and holding Shift while dragging
  does the same without it. Escape gives the drag back to panning. What the box takes
  hold of moves together, and the panel says what it is holding — it still edits one
  element at a time, because a name, a type and a multiplicity each belong to exactly
  one thing, so it lists what is selected and each line is the way back to editing that
  one on its own.

- **A class with a hundred attributes had no room to show their names.** The panel was
  340px wide and would not budge, and inside it the two selects — which carry every
  class name in the model as options — took what they liked, leaving the name column a
  stub that read `allowedA…` for forty members running.

  The panel now takes the Modeler's divider: drag it to widen, double-click to put it
  back, and the width is remembered. A person moves between the two surfaces in one
  session, so it is the same divider with the same behaviour rather than a second one
  of its own. Inside the table the layout is fixed, so the room goes to the name and
  the selects keep the width they need and no more. And because the row being typed in
  is deliberately *not* repainted — that is what keeps the caret in the field — the
  name's tooltip and what the filter matches it against are now kept current as it is
  typed, rather than lagging until the next repaint.

  The view itself also stops sitting in the console's centred 1120px column when a
  model is open, and takes the whole window the way the Modeler does — no column, no
  page gutter and no frame around the editor, because a drawing surface that stops
  22px short of the edge is a window inside a window. The list of models beside it
  keeps the reading column; a list read across a 2000px screen is a worse list.

  And the fit now uses the room it is given. diagram-js fits by shrinking only, never
  magnifying past 100%, which is right for diagrams usually larger than the viewport
  and wrong for a class diagram of six classes on a wide screen: it was drawn at its
  own size in the middle of the window with the width going to nothing on either side.
  A model with room to grow is now grown into it, up to 1.6× — past that a class box
  has nothing more to say for the extra pixels. A model larger than the window is
  shrunk to fit exactly as before.

- **The class canvas could not be zoomed, searched, or undone.**
  Two complaints from the same place: Data › Information model, on a model bigger
  than the window.

  **Zoom, pan and fit were there and invisible.** They have been the canvas's own
  since it moved onto diagram-js ([ADR-0237](docs/adr/0237-class-canvas-on-diagram-js.md))
  — the wheel scrolls, ctrl and the wheel zoom, a drag on empty sheet pans — and
  nothing on the screen said so, so a diagram wider than the viewport could only be
  scrolled at by somebody who already knew the gesture. The canvas now carries the
  same three controls the Panorama canvas does, in the same corner with the same
  icons and the same step, off the same CSS rather than a copy of it: zooming a
  diagram is the same act on both surfaces, and a near-miss between two canvases a
  person uses in one session is worse than either choice alone.

  **And a model outgrows its window in two directions.** A sheet with thirty classes
  on it, and a class with forty members in it — so there is now one search field in
  the bar for both. It matches class names, attribute names, attribute *types* and
  enumeration literals, and a hit says which class it is in (`Order · placedOn`).
  Picking one selects the class, scrolls the sheet to it rather than fitting the
  whole diagram, and — this is the point — narrows that class's panel to the member
  that was searched for. Answering "where is `placedOn`" by selecting a class with
  forty attributes and leaving the reader to scroll would hide the answer it just
  gave.

  **And undo came with the same key.** The canvas has kept a command stack since the
  port and nothing ever asked it for anything: no button, no binding, so a mis-drag
  was repaired by dragging back. It is now ↺ and ↻ beside the zoom controls, and
  Ctrl/⌘ + Z — except while the caret is in a field, where Ctrl+Z belongs to what is
  being typed. What it undoes is what the canvas does, which is moving something; a
  renamed class or a retyped attribute is the panel editing the document and is not
  on that stack, so the control says *the last move* rather than the last change.

  The panel's filter is there on its own too, above the attributes and the literals,
  matching name and type. It hides rows rather than removing them, so every row keeps
  the index its editing and its reordering read, and it is applied to the DOM rather
  than rendered — the panel re-renders on every keystroke, and a filter that
  re-rendered would take the caret out of the field being typed in. Reordering is
  refused while the list is narrowed, because dragging a row past rows that are not
  on screen moves it somewhere nobody chose.

- **The waiting-task badge on the live diagram counted its own page.** A user task with
  1 275 people's work parked on it showed "500" on its 📋 link, and went on showing "500"
  as the queue was worked down in the Tasks app — while the green token badge on the same
  shape counted correctly. Nothing was stuck. 500 is the cap on one page of
  `GET /api/v1/tasks`, and the badge was counting the rows it had been handed, so it could
  not have said anything else until the queue fell below the cap.

  The same cap could also delete the badge outright, which is the worse half: it is
  applied across every definition *before* the list is filtered to the one on screen, so
  enough waiting tasks on another process pushed this one's off the page and took the link
  to a plainly waiting task with it.

  Both facts — whether to draw the link, and whether it goes to a form or to the inbox —
  now come from the element's live-token counter, which is exact at any scale
  ([ADR-0080](docs/adr/0080-runtime-aggregate-counters.md)) and is what the "All
  instances" total in the picker beside it has always been read from. The link no longer
  carries a count at all: the green badge on that shape is that number, and on a user task
  "tokens waiting here" and "tasks waiting here" are one fact, so a second copy of it was
  the same wait read twice — the reason an armed branch does not restate its gateway's
  race either. The task list is still read for the one thing only it can say: which task
  to open when exactly one is waiting.

  Isolating a single instance now asks for *that instance's* tasks rather than filtering
  the global page, so its form stays one click away under a flood — the endpoint has
  resolved an instance's tasks through its own element index all along, and the live view
  was the caller not using it. And completing a task in the Tasks app reloads the inbox
  through the path that reads the paging headers, instead of only the body: the "more
  exist" banner now goes away when the queue drains, and "Load older" no longer pages from
  a cursor that has moved.

- **Saving a layout onto a deployment was refused on diagrams nobody had edited.** The
  first real use of "Save layout to deployment" hit the guard that is supposed to catch a
  changed *process*, on a document whose process had not changed at all.

  bpmn-js leaves out an attribute whose value equals the default its schema declares. A
  deployed model carrying `cancelActivity="true"` on an interrupting boundary event —
  which the BPMN examples write, and which every model copied from one carries — comes
  back from the editor without it. The check compared the serialised attributes, saw one
  missing, and refused. It was right about the bytes and wrong about the question, which
  was never "are these two documents equal" but "is this picture of this model". Any
  model with an interrupting boundary event, an event subprocess or a multi-instance
  activity spelled out that way was affected, and there was nothing the operator could do
  about it.

  Writing an attribute at its default and leaving it out are the same statement in the
  schema, and Atlas's compiler already reads them as the same statement. So the check now
  reads them that way too, for the nineteen attributes BPMN gives a default. Two things
  deliberately unchanged: it applies to BPMN's own attributes only — a `zeebe:` or
  `atlas:` attribute that happens to share a name is a different attribute — and only to
  the default value, so switching a boundary event to `cancelActivity="false"` is still
  the real change it is, and still refused.

  The refusal also says *what* differs now, by element and id, instead of only that
  something does. That is the sentence somebody needs most in exactly this situation:
  when they are sure they changed nothing, and are right
  ([ADR-0251](docs/adr/0251-adjust-a-deployed-diagram.md), amended).

- **The edges in the landscape nugget missed the nodes they connect.** The scene that
  shows Panorama drew its edges as divs rotated by an angle computed from percentage
  coordinates — and x is a share of the container's width while y is a share of its
  height, so on anything that is not square both the angle and the length come out of
  mixed units. On the 745×280 stage the page actually renders, an edge landed 22 degrees
  off and 41 pixels too long, running straight past the node it was supposed to reach.

  It is the failure mode this whole chapter is built to avoid, and it still got through:
  the picture renders, the scene advances, no selector breaks, nothing throws and nothing
  logs. It surfaced only from looking at a rendered frame.

  The edges are an SVG now, with `preserveAspectRatio="none"`, so an endpoint sits
  exactly on its coordinate whatever the aspect ratio, and `vector-effect:
  non-scaling-stroke` keeps the line from being stretched with it. `e2e/nuggets.spec.mjs`
  gains the assertion that was missing: for every edge, both ends land on a node.
  Confirmed by moving one edge's endpoint and watching it fail — the first attempt at
  that check was itself broken, matching against unescaped quotes that the JSON block
  does not contain, so it never challenged the test at all.

- **A search term found more than it was asked for.** Reported from use:
  `kdnr=MT-100` also returned MT-10001. The instance search widened every term into a
  substring match, so an operator who named one customer got a list holding another one
  beside it, with nothing on either row to tell them apart — and no way to ask about
  only the one they meant.

  It was not even consistent with itself. The value index
  ([ADR-0244](docs/adr/0244-searchable-variables.md)) answers a
  declared name exactly, so the same query matched exactly when the model carried
  `atlas:searchable` for that name and matched as a substring when it did not. Whether
  a name is declared is a property of the model: invisible from the search box,
  changeable by a redeployment, and it had come to decide what a query means.

  A term is now matched **whole**, and widening is something you ask for, in the two
  shapes everyone knows from shells and file pickers: `*` for any run of characters,
  `?` for exactly one, and a backslash to escape either, so a value that really contains
  a star is still reachable. One rule for declared and undeclared names, for
  `name=value` and free text, for the live index, the instance walk and the archive.
  Under the index a pattern splits into its literal head and the rest: no wildcard is
  the exact seek that already existed, and a wildcard seeks to a neighbourhood and
  matches the full pattern before reporting anything — without that, `MT-1?` would
  answer with every `MT-1` value the index holds.

  The same predicate filters **bulk termination**, so an implicit widening there
  selected instances the operator had not named. That is the version of this bug that
  does not merely confuse.

  This is a behaviour change: free text that used to match a value it occurred in now
  matches one it equals, so `retail` becomes `*retail*`. The search hint, the handbook,
  the OpenAPI summary and the MCP tool description all state the rule, because changing
  what a query means in silence would be worse than the behaviour it replaces.
  ([ADR-0248](docs/adr/0248-search-terms-are-literal.md))

- **Opening a deployed process in the Modeler lost the application it belongs to, and
  with it the whole vocabulary behind a data object's Type.** A draft carries its
  application; a deployed version carries it too — the deploy records it — but the
  route that opens one (`#/modeler/d/{key}`) does not name it and nothing looked it up.

  The result was a Type field that had quietly stopped working: no classes offered, no
  class shown for the one already set, and not even the "nothing models this yet"
  warning — because *nothing is modelled* and *the vocabulary never loaded* are
  different answers and only the first is safe to state. It looked exactly like a plain
  text box, which is what the field was before there was an information model at all.
  The breadcrumb gave it away: it named the process but not the application.

- **A widened Properties column in the form editor gave its width to white space, not
  to the panel.** The Design tab's side columns are resizable — our own affordance on
  top of the vendored form-js Playground ([ADR-0028](docs/adr/0028-forms-and-the-tasks-app.md)) —
  and the drag sets the width of the *column*. But form-js pins the properties panel
  inside that column to a fixed `--properties-panel-width: 250px`. So an author who
  pulled the divider left to get room for a long FEEL expression got a 510px column
  holding a 250px panel, and 260px of blank white between the panel and the window's
  right edge, which stayed there across sessions because the width is remembered. The
  mirror case was worse and quieter: dragged narrower than 250px, the panel was clipped
  by the column rather than shrunk with it, so the rightmost part of every property row
  was simply not reachable.

  The panel now follows the column it lives in. The palette on the other side always
  did — its content is fluid — which is why only one of the two columns showed it.
  `e2e/form-side-columns.spec.mjs` holds the outcome at the default width, after a real
  drag, for a width a previous session saved, and with the column collapsed to its rail.

- **"Loading form…" could stand there for good.** Deploy & run opens the process's
  start form in a modal (ADR-0028), and the modal waited on two things — the vendored
  form-js viewer and the form definition — with a deadline on neither. A request that
  hangs instead of failing is an ordinary thing in the wild (a stalled asset, a proxy
  holding the connection, a server that stops answering), and it left the placeholder
  on screen for the rest of the session: no error, no way to retry, and a disabled
  **Send** beside it. Reported from a running server, where the start form of a
  process being deployed never appeared.

  A stall is now a failure somebody can act on. Both halves of the load carry a
  deadline, the message names which half did not arrive, and a **Try again** costs
  nothing — whatever did arrive in the meantime is in the browser's cache. The
  memoized viewer import no longer remembers a failure either: one bad fetch used to
  fail every later form in the tab, leaving a page reload as the only way back.
  Cancelling while it loads now also drops the late arrival rather than building a
  live form into a container already detached.

  `e2e/deploy-start-form-stall.spec.mjs` holds a definition, then the bundle, open —
  the modal has to report it, offer the retry, and render the form when the retry
  arrives, having deployed nothing throughout.

- **The Data area's Import button did nothing at all.** Its click handler called a
  helper that a change to the Console had removed in the meantime — the picker for
  which application a new model belongs to, which became a dialog rather than a
  numbered `window.prompt`. Git merged both changes without a word, because neither
  touched the other's lines. The result was a reference error thrown inside an async
  listener: no file dialog, no message, nothing on the page to say what had happened
  ([ADR-0232](docs/adr/0232-uml-model-import.md)).

  The flow now uses the same dialog as the rest of the Console, and it asks for the
  file **first** — a browser will not open a file picker from a task that no longer
  counts as a user gesture, and a dialog in front of it costs exactly that. With one
  writable application there is nothing to ask and the picker is skipped.

  It also moved out of `app.js` into `api/web/infomodel-import.js`, for the reason
  `pickmodal.js` and `connectordialog.js` did: `app.js` boots the whole Console on
  import, so anything left inside it is only ever exercised by hand. The new
  `e2e/infomodel-import.spec.mjs` drives the real Console — a click has to open a file
  dialog, and a chosen file has to produce the report — which is the crude assertion
  that would have caught this and did not exist.

- **A data association drawn inside a subprocess was thrown away at compile time.** A
  `<dataInputAssociation>` or `<dataOutputAssociation>` is drawn on the *activity*, and
  an activity may sit in any scope a model nests — an ordinary subprocess, an event
  subprocess, an ad-hoc. The compiler wired I/O mappings by walking the whole scope tree
  and wired data associations by walking only the process root's element lists. So an
  activity inside a subprocess kept its `zeebe:ioMapping` and silently lost its
  associations.

  Nothing said so. The model validated, deployed, started, ran through the activity and
  finished, and the data object it was drawn as writing stayed empty — the read variable
  `null` — with no error, no warning and no incident. The two walks are now one function
  with a comment saying they must stay in step.

- **An event subprocess with a `zeebe:ioMapping` overwrote the variable it was meant to
  update, with `null`, the first time it fired.** An armed event-subprocess trigger is an
  element instance whose element id is the *handler container* — that is how it finds the
  message or timer it waits on. The generic per-activity work is keyed off the element
  id, so the trigger also ran the handler's input and output mappings as if it were the
  handler.

  The timing is what made it fatal. A trigger arms when its scope is *entered* — at
  instance creation for a root-level one — so the input mapping evaluated against a scope
  where the main flow had not yet written anything, and the output mapping promoted that
  `null` into the parent scope the moment a message arrived. The handler then read the
  register the trigger had just destroyed. Data associations on the handler had the same
  problem. The trigger now runs neither; the handler run applies both, at the moment its
  values exist.

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

- **Two Google Sheets row watches on one Worker no longer share an idempotency mark.**
  A row watch's mark was composed from the Worker's id and a field only clio watches
  fill, which for every other kind is the empty string. So two watches on the same
  Worker — two different spreadsheets — composed the *same* mark, and whichever polled
  first advanced it past the other's rows. The second watch's form responses were
  silently never delivered: no error, no incident, just processes that did not start
  (ADR-0264).

  A row watch now keys its mark on the spreadsheet it watches. Existing watches need no
  migration and replay nothing: a row watch reads from its own stored cursor, and only
  rows past that cursor are ever emitted, so the mark's only job is to catch a duplicate
  within one page.

### Security

- **Deploying asks whether you may write to the project.** `POST /api/v1/deployments`
  resolved its target project — from `?projectId=`, or from the draft's own assignment —
  and then deployed into it without ever asking whether the caller belonged to it. Any
  account that could reach the route could file a definition into any project on the
  server, and MCP took the same path (ADR-0278, audit F09).

  The membership check now runs *before* `claimBlockingModel` and `deployModel`, so a
  refusal leaves neither a sidecar file nor a registry entry behind. It is the same
  object-level write check the other project operations already made; the deploy route
  was the one that resolved a project and then forgot to ask about it.

- **A withdrawn role takes effect on the next request, not the next login.** Taking a role
  away from somebody changed the record and nothing else: every session they already held
  kept the roles it had been issued with, for as long as it lived. The mechanism to push a
  change into live sessions existed and had been used for group membership for a long
  time — it had simply never been wired for roles (ADR-0281, audit F10).

  API tokens are deliberately not swept along: a token carries rights of its own, and the
  console now says so where you can see it rather than quietly aligning them with the
  owner's.

- **Reading a running instance is an object question, not a role question.** The endpoint a
  task form reads its values from answered for any signed-in identity: the variables of any
  instance on the server, whether or not the caller had anything to do with it
  (ADR-0275, audit F11). Lifting the route to `operator` would have been the wrong repair —
  it locks out exactly the people the route exists for — so the role stays `any` and the
  handler asks the object question instead.

  Answering it needed a rule Atlas had never written down: what a BPMN candidate group,
  free text in the model, has to do with an identity group. **An unclaimed task matches a
  caller's group by name (case-insensitively) or by group id; a claimed task belongs to its
  owner alone.** That is the one place this work decides new product behaviour rather than
  protecting what exists, and it is written out in `api/instancescope.go` and in the record.

  The result is *narrower* than before, including for people who could already read: a task
  owner now gets the fields their form declares and nothing else, and a task with no form
  grants nothing, because there is no declared set and guessing one is how an allowlist
  becomes a formality. MCP and public start forms hold to the same extent — otherwise the
  gap would only have moved. This also closes the code half of ISDS point O-02.

  **Still open, and named rather than left to be found:** `GET /api/v1/tasks/{key}` has the
  same gap in a smaller format, and the remaining instance-scoped reads are guarded by role
  rather than by relationship — an `operator` anywhere sees every instance on the server.

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

[Unreleased]: https://github.com/pblumer/atlas/compare/v0.6.0...HEAD
[0.6.0]: https://github.com/pblumer/atlas/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/pblumer/atlas/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/pblumer/atlas/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/pblumer/atlas/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/pblumer/atlas/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/pblumer/atlas/releases/tag/v0.1.0
