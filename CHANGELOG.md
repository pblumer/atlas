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

[Unreleased]: https://github.com/pblumer/atlas/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/pblumer/atlas/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/pblumer/atlas/releases/tag/v0.1.0
