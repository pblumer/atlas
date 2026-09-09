# Architecture Decision Records

This directory records the significant architectural decisions made on Atlas, using the [MADR](https://adr.github.io/madr/)-influenced format described in [`template.md`](template.md).

An ADR captures a decision, the context that forced it, the options considered, and the consequences accepted. ADRs are immutable once accepted: if a decision changes, a new ADR supersedes the old one rather than editing it.

A number belongs to exactly one decision, and it is assigned **when the record lands on `main`** — never on a branch. `go test ./docs/adr` enforces unique, gapless numbers, keeps the index below in step with the directory, and checks that every `ADR-NNNN` citation anywhere in the repository still resolves. The index is a table of contents: one row per record, carrying that record's own `# ADR-NNNN:` heading, so what the decision *says* belongs in the record, not in the cell.

## Writing a record

**Do not pick a number.** A record in flight carries none:

1. Copy [`template.md`](template.md) to `docs/adr/draft-<slug>.md` — a kebab-case slug, no number.
2. Keep the heading as `# ADR-DRAFT: Your title`, and fill in the record.
3. Add **no** row to the index below.
4. Cite it as `ADR-draft-<slug>` from code comments and docs, or link `draft-<slug>.md`.

When the PR merges, a workflow on `main` runs `make adr-number`. That renames the
file to `NNNN-<slug>.md`, rewrites the heading, appends the index row, and rewrites
every citation of the draft to the number it just got. Nothing for you to remember;
you can also run `make adr-number` by hand on `main`.

Why the ceremony: the number used to be taken when the record was written, which is
the earliest possible moment and the one with the least information. Two open
branches both saw the same "next free" number and both took it — that is how 0090,
0103 and 0105 came to be shared by unrelated ADRs (the later record of each pair now
lives at 0139, 0140 and 0141). Once a test caught the collision, the cost became a
renumber on every merge instead: one record walked 0164 → 0169 across six of them
without a word of its content changing. Assigning the number where the question has
one answer removes both. The full argument is in
[the record on merge-time numbering](0170-adr-numbers-assigned-at-merge.md).

A number, once assigned, is never reassigned — that is what makes `(ADR-0168)` in a
comment safe to write.

### When the record rests on a question you could not answer

Some decisions are right *while* something we do not know stays unknown. ADR-0292 is
the case that prompted this rule: it renders a MIMWAL table's cells by position
because the meaning of the columns is established by no reference anyone could find,
and it would be re-argued the day one turns up. Written like any other record, that
reads a year later as settled — nothing in it separates "we decided this" from "we
decided this for now, on a gap in what we know".

So say both, in the front matter:

```
- **Open question:** MIMWAL's grid column semantics are not established from its own
  source. The positional cells rest on that gap.
- **Question checked:** 2026-09
```

The two come as a pair, and `go test ./docs/adr` insists on it: a question with no
date cannot go stale, and a date with no question says a thing was checked without
saying what. The question may wrap onto indented lines. The date is the month
somebody last **looked at the question**, not the month the record was written, and
the guard fails once it has stood for a year — the same interval, for the same
reason, as the Worker Type setup steps of [ADR-0289](0289-worker-type-setup-in-the-panel.md).

The reasoning behind the pair — including why it states a month somebody *looked*
rather than a deadline somebody *set* — is in ADR-0293.

When the check fires, the fix is never to bump the date. Go and look: if the question
now has an answer, delete both lines and check whether the decision that rested on the
gap still holds — that is the case this exists for. If it is still open, write down
what you learned and date the month you looked.

## Reading the older records: "connector"

Records written before [ADR-0203](0203-worker-execution-model.md) say *connector*
where the rest of the documentation now says **Worker Type** (an execution
capability), **Worker** (one configured target and identity of that type) or
**Worker Instance** (a process leasing its jobs) — the word meant all three
depending on where it stood, which is why it was replaced.

Those records are **not rewritten**: an ADR is immutable once accepted, and
ADR-0203 says so explicitly — older connector wording is reconciled through links,
not by editing history. So read ADR-0036, [ADR-0041](0041-connector-management-and-secret-store.md),
[ADR-0067](0067-service-task-connector-catalog.md), ADR-0154/0166/0172/0173/0201 and
their neighbours in their own vocabulary; ADR-0203 carries the mapping. The
persisted contracts kept the old spelling too, on purpose: the `connector/` package
paths, the `connector="…"` BPMN attribute, `atlas worker --connector`, the
`ATLAS_*_CONNECTORS` variables and the `/api/v1/connectors` routes.

## Index

| ADR | Title | Status | Implementation |
|-----|-------|--------|----------------|
| [0001](0001-event-sourcing-and-log-structured-state.md) | Event sourcing and log-structured state | Accepted | Landed |
| [0002](0002-single-writer-partition-model.md) | Single-writer partition model | Accepted | Landed |
| [0003](0003-pebble-as-state-store.md) | Pebble as embedded state store | Accepted | Landed |
| [0004](0004-compile-bpmn-to-indexed-graph.md) | Compile BPMN to an integer-indexed graph | Accepted | Landed |
| [0005](0005-group-commit-and-fsync-strategy.md) | Group commit and fsync strategy | Accepted | Landed |
| [0006](0006-partition-routing-and-cross-partition.md) | Partition routing and cross-partition communication | Accepted | Partial |
| [0007](0007-job-worker-protocol.md) | Job worker protocol | Accepted | Landed |
| [0008](0008-feel-expression-strategy.md) | FEEL expression compilation strategy | Accepted | Landed |
| [0009](0009-record-serialization-format.md) | Record serialization format | Accepted | Landed |
| [0010](0010-go-and-no-cgo.md) | Go as implementation language, no CGO | Accepted | Landed |
| [0011](0011-single-binary-distribution-and-web-ui.md) | Single-binary distribution with an embedded web viewer and editor | Accepted | Landed |
| [0012](0012-web-ui-app-shell.md) | A buildless, self-contained web UI app shell | Accepted | Landed |
| [0013](0013-embed-bpmn-js-modeler.md) | Embed the bpmn-js modeler as a vendored asset | Accepted | Landed |
| [0014](0014-dmn-business-rule-tasks-via-temis.md) | DMN business rule tasks via the temis engine | Accepted | Landed |
| [0015](0015-reuse-feel-engine.md) | Reuse the external FEEL engine behind an `expr` boundary | Accepted | Landed |
| [0016](0016-mcp-server-over-http-api.md) | Model Context Protocol server as a stdio adapter over the HTTP API | Accepted | Landed |
| [0017](0017-process-instance-history.md) | Retain finished process instances in a history index | Accepted | Landed |
| [0018](0018-test-driven-development.md) | Test-driven development as the default workflow | Accepted | Landed |
| [0019](0019-durable-deployments.md) | Durable deployments via an on-disk sidecar store | Accepted | Landed |
| [0020](0020-message-correlation.md) | Message events and correlation | Accepted | Landed |
| [0021](0021-diagram-drafts.md) | Diagram drafts, separate from deployments | Accepted | Landed |
| [0022](0022-element-visit-history.md) | Retain a per-element token-visit history for the Operations overlay | Accepted | Landed |
| [0023](0023-collaborations-and-pools.md) | Collaborations and pools as multi-process deployments | Accepted | Landed |
| [0024](0024-parallel-gateway-join.md) | Parallel gateway join synchronization | Accepted | Landed |
| [0025](0025-full-properties-panel.md) | Extend the hand-written properties panel instead of vendoring bpmn-js-properties-panel | Proposed | Partial |
| [0026](0026-problems-panel-and-versioned-validation.md) | A Problems panel with validation targeted at an engine version | Accepted | Landed |
| [0027](0027-element-templates.md) | Element templates for pre-configured, reusable elements | Proposed | Partial |
| [0028](0028-forms-and-the-tasks-app.md) | User tasks, forms, and the Tasks app | Accepted | Landed |
| [0029](0029-public-process-start-links.md) | Public process start via a published form link | Accepted | Landed |
| [0030](0030-play-mode-simulation.md) | Play mode — ephemeral in-Modeler process simulation | Accepted | Landed |
| [0031](0031-diagram-version-history.md) | Diagram version history in the Modeler | Proposed | Not started |
| [0032](0032-modeler-ai-copilot.md) | In-Modeler AI copilot over the MCP/HTTP surface | Proposed | Partial |
| [0033](0033-inclusive-gateway-join.md) | Inclusive gateway join synchronization | Accepted | Landed |
| [0034](0034-projects-and-artifacts.md) | Projects as containers for heterogeneous artifacts | Accepted | Landed |
| [0035](0035-message-start-events.md) | Message start events and the processInstanceKey built-in | Accepted | Landed |
| [0036](0036-clio-connector.md) | A clio connector — server-registered event-store integration | Accepted | Landed |
| [0037](0037-structured-json-variables.md) | Structured JSON variables | Accepted | Landed |
| [0038](0038-collaboration-message-flow-replay.md) | Collaboration message-flow replay | Accepted | Landed |
| [0039](0039-dmn-io-variable-mappings.md) | Input/output variable mappings for business rule tasks | Accepted | Landed |
| [0040](0040-boundary-events.md) | Boundary events — timer and message, interrupting and non-interrupting | Accepted | Landed |
| [0041](0041-connector-management-and-secret-store.md) | Connector management and the secret store | Accepted | Landed |
| [0042](0042-user-task-assignment-and-claim.md) | User-task runtime assignment and claim/unclaim | Accepted | Landed |
| [0043](0043-openapi-spec-and-embedded-api-explorer.md) | An OpenAPI spec and an embedded API explorer for the HTTP API | Accepted | Landed |
| [0044](0044-user-management-and-authentication-boundary.md) | User management and the authentication boundary | Accepted | Landed |
| [0045](0045-user-task-assignment-bound-to-identity.md) | Binding user-task assignment to real identities | Accepted | Landed |
| [0046](0046-single-process-step-replay.md) | Single-process step-by-step replay | Accepted | Landed |
| [0047](0047-polyglot-script-tasks-via-job-workers.md) | Polyglot script tasks (PowerShell, …) via job workers | Accepted | Landed |
| [0048](0048-per-step-variable-snapshots.md) | Per-step variable snapshots in the single-process replay | Accepted | Landed |
| [0049](0049-internal-service-auth-for-mcp.md) | Internal service authentication for the in-process MCP adapter | Accepted | Landed |
| [0050](0050-temis-decision-connector.md) | Central DMN decisions via a temis decision connector | Accepted | Landed |
| [0051](0051-timer-start-events.md) | Timer start events (duration, date, cycle) | Accepted | Landed |
| [0052](0052-message-end-events.md) | Message end events | Accepted | Landed |
| [0053](0053-first-class-data-objects.md) | First-class data objects — typed, event-sourced state, and lineage | Accepted | Landed |
| [0054](0054-date-cycle-timers-for-catch-and-boundary.md) | Date and cycle timers for catch and boundary events | Accepted | Landed |
| [0055](0055-feel-expression-timer-schedules.md) | FEEL-expression timer schedules for catch and boundary events | Accepted | Landed |
| [0056](0056-feel-cycles-and-feel-start-timers.md) | FEEL cycles, and FEEL on timer start events | Accepted | Landed |
| [0057](0057-first-class-feel-temporals.md) | First-class FEEL temporals for timer schedules | Accepted | Landed |
| [0058](0058-data-output-associations.md) | Data output associations — write a value and transition a data object's state | Accepted | Landed |
| [0059](0059-data-input-associations.md) | Data input associations — read a data object into an activity | Accepted | Landed |
| [0060](0060-field-level-data-object-writes.md) | Field-level data object writes — set one member of a structured object | Accepted | Landed |
| [0061](0061-incident-model.md) | Incident model — job-failure incidents, raise, resolve, resume | Accepted | Landed |
| [0062](0062-embedded-dmn-editor.md) | An embedded DMN editor (dmn-js) | Accepted | Landed |
| [0063](0063-dmn-decision-binding.md) | DMN decision binding (latest vs deployment) | Accepted | Landed |
| [0064](0064-timer-feel-failure-incidents.md) | Timer FEEL-failure incidents — park and raise instead of firing immediately | Accepted | Landed |
| [0065](0065-multi-token-process-replay.md) | Multi-token process replay and causal token lineage | Accepted | Landed |
| [0066](0066-decision-evaluation-records.md) | Durable decision-evaluation records for debugging | Accepted | Landed |
| [0067](0067-service-task-connector-catalog.md) | A service-task connector catalog, and REST with a model-authored endpoint | Accepted | Landed |
| [0068](0068-task-io-variable-mappings.md) | Task input/output variable mappings with activity-local scopes | Accepted | Landed |
| [0069](0069-engine-internal-encrypted-secret-vault.md) | An engine-internal encrypted secret vault (ADR-0041 option A3) | Accepted | Landed |
| [0070](0070-vault-on-by-default-with-generated-key.md) | The secret vault is on by default, with a generated key | Accepted | Landed |
| [0071](0071-sharing-scopes.md) | Sharing scopes — private and shared access boundaries for design-time work | Accepted | Landed |
| [0072](0072-multiple-dmn-models-per-process.md) | Multiple DMN models per process deployment | Accepted | Landed |
| [0073](0073-principals-directory.md) | A principals directory for member and assignee pickers | Accepted | Landed |
| [0074](0074-embedded-subprocesses.md) | Embedded subprocesses (scope lifecycle via child counters) | Accepted | Landed |
| [0075](0075-clio-inbound-event-bridge.md) | A clio inbound event bridge — at-least-once ingestion with engine-side idempotent delivery | Accepted | Landed |
| [0076](0076-call-activities.md) | Call activities (single-partition) | Accepted | Landed |
| [0077](0077-multi-instance-activities.md) | Multi-instance activities (parallel and sequential) | Accepted (amended) | Landed |
| [0078](0078-design-view-token-simulation.md) | Design-view token simulation — a client-side control-flow walkthrough | Accepted | Landed |
| [0079](0079-outbound-mail-connector.md) | An outbound mail connector (SMTP first) | Accepted (amended) | Landed |
| [0080](0080-runtime-aggregate-counters.md) | Sublinear runtime views via maintained aggregate counters | Accepted | Landed |
| [0081](0081-community-marketplace-for-connectors-and-tasks.md) | A community marketplace for connectors, service tasks, and script tasks | Proposed | Partial |
| [0082](0082-event-subprocesses.md) | Event subprocesses (message- and timer-triggered, interrupting and non-interrupting) | Accepted | Landed |
| [0083](0083-o1-instance-summary.md) | An O(1) instances summary — per-definition finished-count and last-activity counters | Accepted | Landed |
| [0084](0084-csv-batch-validation.md) | CSV batch validation — upload a file, validate every row against business rules, correct the failures | Accepted | Landed |
| [0085](0085-process-instance-ttl.md) | Process-instance TTL — self-cleaning via the due-timer index | Accepted | Landed |
| [0086](0086-gateway-conditions-resolve-over-scope-chain.md) | Gateway conditions resolve over the scope chain | Accepted | Landed |
| [0087](0087-in-process-csv-ingestion.md) | In-process CSV ingestion — upload in a user task, parse in the process | Accepted | Landed |
| [0088](0088-signal-events.md) | Signal events (broadcast throw/catch) | Accepted | Landed |
| [0089](0089-error-events.md) | Error events (scoped propagation to the nearest handler) | Accepted | Landed |
| [0090](0090-bulk-terminate-instances.md) | Bulk-terminate running instances — an explicit selection and a filtered scope | Accepted | Landed |
| [0091](0091-user-task-scheduling.md) | User-task scheduling — priority and due date | Accepted | Landed |
| [0092](0092-clio-key-provisioning.md) | One-click clio credential provisioning | Accepted | Landed |
| [0093](0093-native-mail-providers.md) | Native Gmail and Microsoft Graph mail providers | Accepted | Landed |
| [0094](0094-singleton-message-start.md) | Singleton message start — at most one live instance per correlation key | Accepted | Landed |
| [0095](0095-external-variable-modification.md) | External variable modification on a running instance | Accepted | Landed |
| [0096](0096-token-simulation-events-and-inclusive-gateways.md) | Token simulation — event triggers, inclusive gateways, and an auto-decide mode | Accepted | Landed |
| [0097](0097-token-simulation-message-starts-event-subprocesses-multi-instance.md) | Token simulation — message starts, event-subprocess triggers, and multi-instance | Accepted | Landed |
| [0098](0098-external-variable-modification-audit.md) | Audit trail for external variable modifications | Accepted | Landed |
| [0099](0099-archimate-enterprise-architecture-view.md) | An ArchiMate 3.2 enterprise-architecture view | Accepted | Landed |
| [0100](0100-token-simulation-configurable-multi-instance-count.md) | Token simulation — configurable multi-instance count, modelled cardinality wins | Accepted | Landed |
| [0101](0101-token-simulation-throw-delivers-to-waiting-catch.md) | Token simulation — a thrown message/signal delivers to a waiting catch | Accepted | Landed |
| [0102](0102-receive-tasks.md) | Receive tasks | Accepted | Landed |
| [0103](0103-compensation.md) | Compensation and compensation handlers | Accepted | Landed |
| [0104](0104-token-simulation-embedded-subprocesses.md) | Token simulation — entering embedded subprocesses | Accepted | Landed |
| [0105](0105-per-server-call-activity-target-overrides.md) | Per-server call-activity target overrides | Accepted | Landed |
| [0106](0106-bmc-remedy-connector.md) | A BMC Remedy connector — server-registered ITSM entry creation | Accepted | Landed |
| [0107](0107-backup-and-restore.md) | Backup and restore — a one-file download of the design-time data directory | Accepted | Landed |
| [0108](0108-bpmn-transactions.md) | BPMN transactions (cancel end event, cancel boundary, transactional compensation) | Accepted | Landed |
| [0109](0109-full-instance-snapshot.md) | Whole-instance snapshot — a full backup that includes running instances | Accepted | Landed |
| [0110](0110-event-based-gateways.md) | Event-based gateways (deferred choice) | Accepted | Landed |
| [0111](0111-incident-model-completion.md) | Completing the incident model — retry backoff and timer-FEEL failure incidents | Accepted | Landed |
| [0112](0112-send-tasks.md) | Send tasks | Accepted | Landed |
| [0113](0113-org-wide-ui-theme.md) | Org-wide UI brand theme | Accepted | Landed |
| [0114](0114-opensearch-event-exporter.md) | OpenSearch event exporter — a WAL-tailing sink, off the hot path | Accepted | Landed |
| [0115](0115-history-retention-hard-delete.md) | History retention — an export-gated, age-based hard delete of finished instances | Accepted | Landed |
| [0116](0116-terminate-end-events.md) | Terminate end events | Accepted | Landed |
| [0117](0117-ai-agent-task.md) | An AI agent task — an LLM agent as a managed connector on the job path | Accepted | Landed |
| [0118](0118-web-scraping-connector.md) | A web-scraping connector — model-authored URL + CSS selector extraction | Accepted | Landed |
| [0119](0119-deactivate-deployed-process.md) | Deactivating a deployed process | Accepted | Landed |
| [0120](0120-mockup-service-task.md) | Mockup (engine-simulated) service tasks | Accepted | Landed |
| [0121](0121-bpmn-lanes.md) | BPMN lanes | Accepted (Layer A) | Landed |
| [0122](0122-protected-system-project-and-bootstrap-deployment.md) | A protected system project and bootstrap-deployed platform processes | Accepted | Landed |
| [0123](0123-sanctioned-user-provisioning-for-system-processes.md) | A sanctioned automated user-provisioning path for system processes | Accepted (amended) | Landed |
| [0124](0124-server-side-diagram-auto-layout.md) | Server-side BPMN diagram auto-layout in Go | Accepted | Landed |
| [0125](0125-escalation-events.md) | Escalation events (non-interrupting, propagating throw/catch) | Accepted | Landed |
| [0126](0126-self-service-registration-link.md) | Self-service registration link on the login screen | Accepted | Landed |
| [0127](0127-layered-layout-pipeline-and-invariants.md) | A layered layout pipeline and executable layout invariants | Accepted (amended) | Landed |
| [0128](0128-process-applications.md) | Process applications — the project, elevated into a deployable, versioned, portable unit | Accepted | Landed |
| [0129](0129-remote-deployment-targets.md) | Remote deployment targets — publish an application to another Atlas server | Accepted | Landed |
| [0130](0130-deprecating-a-process-version.md) | Deprecating a process version — a drain state distinct from pausing | Proposed | Not started |
| [0131](0131-engine-recovery-checkpoints-and-wal-compaction.md) | Engine recovery checkpoints and WAL compaction | Accepted | Landed |
| [0132](0132-link-events.md) | Link events (intra-scope goto — a compile-time synthetic flow) | Accepted | Landed |
| [0133](0133-standard-loop-activities.md) | Standard loop activities (the ↻ marker) | Accepted (amended) | Landed |
| [0134](0134-git-backed-applications.md) | Git-backed applications — a repository as an application's source of truth | Accepted | Landed |
| [0135](0135-retries-as-a-task-property.md) | Retries as a property of every job-backed task | Accepted | Landed |
| [0136](0136-terminated-tokens-in-the-replay.md) | Terminated tokens in the step-by-step replay | Accepted | Landed |
| [0137](0137-conditional-events.md) | Conditional events (data-triggered catch/boundary) | Accepted | Landed |
| [0138](0138-adhoc-subprocesses.md) | Ad-hoc subprocesses (on-demand, unordered contained activities) | Accepted | Landed |
| [0139](0139-csv-to-json-connector.md) | A first-class "CSV to JSON" connector kind with model-authored layout | Accepted | Landed |
| [0140](0140-live-collaborative-modeling-sessions.md) | Live collaborative modeling sessions — real-time co-editing of drafts by people and AI agents | Accepted | Landed |
| [0141](0141-sharepoint-connector.md) | A SharePoint connector (create list item, via Microsoft Graph) | Accepted | Landed |
| [0142](0142-prometheus-metrics.md) | Operational metrics over a Prometheus endpoint | Accepted | Landed |
| [0143](0143-process-documentation-export.md) | Process documentation export | Accepted | Landed |
| [0144](0144-per-definition-history-ttl.md) | Per-definition history TTL — retention the model declares | Accepted | Landed |
| [0145](0145-developer-view-for-code-fields.md) | A Developer View for code-bearing fields | Accepted | Landed |
| [0146](0146-history-expiry-due-date-index.md) | History expiry as a due-date index — retention that scales with what is due | Accepted | Landed |
| [0147](0147-splitting-the-api-server-object.md) | Splitting the api Server object, without weakening the single writer | Accepted | Landed |
| [0148](0148-org-wide-brand-logo.md) | Org-wide brand logo | Accepted | Landed |
| [0149](0149-bounded-connector-call-budget.md) | A bounded outbound-call budget for every connector | Accepted (amended) | Landed |
| [0150](0150-preview-mail-provider-and-visible-incidents.md) | A preview mail provider, and incidents on the live diagram | Accepted (amended) | Landed |
| [0151](0151-incidents-beyond-the-live-diagram.md) | Incidents beyond the live diagram — the replay, the lists, and one shared action | Accepted | Landed |
| [0152](0152-rest-connector-oauth2.md) | OAuth2 client-credentials for the REST connector | Accepted | Landed |
| [0153](0153-scim-connector.md) | SCIM 2.0 provisioning connector | Accepted | Landed |
| [0154](0154-ldap-connector.md) | Generic LDAP connector | Accepted | Landed |
| [0155](0155-secret-shape-hints.md) | The Secrets panel says what a value has to be | Accepted | Landed |
| [0156](0156-in-process-vs-out-of-process-service-tasks.md) | In-process vs. out-of-process service tasks — where a step's work runs, and what we recommend | Superseded by ADR-0164 | Superseded |
| [0157](0157-worker-processes-supervision-and-console.md) | Every side-effecting task on a worker process — `atlas worker`, optional supervision, and a Workers console | Accepted | Landed |
| [0158](0158-a-connector-reference-that-explains-itself.md) | A connector reference that explains itself — and an incident you can actually resolve | Accepted | Landed |
| [0159](0159-manual-task-completion-audit.md) | Auditable manual task completion | Accepted | Landed |
| [0160](0160-fix-the-connector-from-the-incident.md) | Fix the connector from the incident | Accepted | Landed |
| [0161](0161-element-io-on-the-diagram.md) | What an element was handed, on the diagram | Accepted | Landed |
| [0162](0162-process-instance-migration.md) | Process instance migration | Accepted | Landed |
| [0163](0163-deleting-a-referenced-connector.md) | Deleting a connector deployed models still reference — and keeping a table inside its card | Accepted | Landed |
| [0164](0164-no-in-process-service-tasks.md) | No in-process service tasks — the core loop must never be able to get stuck | Accepted | Landed |
| [0165](0165-soap-connector.md) | SOAP / Web Services (WSDL) connector | Accepted | Landed |
| [0166](0166-active-directory-connector.md) | Active Directory connector | Accepted | Landed |
| [0167](0167-released-connectors-ship-in-the-marketplace.md) | A released connector ships in the marketplace | Accepted | Partial |
| [0168](0168-connector-work-on-a-worker.md) | Moving a connector onto a worker — where the task detail travels, and where the credential lives | Accepted | Landed |
| [0169](0169-incident-repair-forms.md) | A form on the incident — repairing an instance with named fields instead of raw JSON | Accepted | Landed |
| [0170](0170-adr-numbers-assigned-at-merge.md) | ADR numbers are assigned at merge, not on a branch | Accepted | Landed |
| [0171](0171-directory-file-connector.md) | A directory-file connector — LDIF and DSML | Accepted | Landed |
| [0172](0172-entra-id-connector.md) | A Microsoft Entra ID connector | Accepted | Landed |
| [0173](0173-generic-sql-connector.md) | Three SQL connectors, and the first kinds born on a worker | Accepted | Landed |
| [0174](0174-connector-payloads-are-the-input-mapping.md) | A connector task's input mappings are its outbound payload | Accepted | Landed |
| [0175](0175-replicated-partition-cells.md) | Replicated partition cells for horizontal scale-out | Proposed | Not started |
| [0176](0176-standards-boundary-and-runtime-contract.md) | Standards boundary and the Atlas runtime contract | Accepted | Landed |
| [0177](0177-reload-skips-the-deploy-gate.md) | Reload skips the deploy-time validation gate | Accepted | Landed |
| [0178](0178-responsibility-metadata-raci.md) | Responsibility metadata — RACI on the element, with R derived from the assignment | Proposed | Not started |
| [0179](0179-worker-job-history-in-clio.md) | A worker's job history lives in clio, not in Atlas | Accepted | Landed |
| [0180](0180-groups-as-members.md) | Groups as scope members | Accepted | Landed |
| [0181](0181-ad-connector-mock-mode.md) | Mock mode for the Active Directory connector | Accepted | Landed |
| [0182](0182-ad-default-offload.md) | Active Directory runs on a worker by default | Accepted | Landed |
| [0183](0183-the-modeler-asks-where-a-kind-runs.md) | The Modeler asks the server where an authored kind runs | Accepted | Landed |
| [0184](0184-grant-audit-log.md) | Grant audit log | Accepted | Landed |
| [0185](0185-live-group-membership.md) | Live group membership | Accepted | Landed |
| [0186](0186-embed-public-forms-cross-origin.md) | Embedding a public start form cross-origin (scoped CORS) | Accepted | Landed |
| [0187](0187-postgres-change-events.md) | Database change events — captured in the database, read on a worker, deduplicated in the engine | Proposed | Not started |
| [0188](0188-console-managed-sql-connectors.md) | A database is a Console entry, not a start parameter — and a worker is never a thing you create | Accepted | Landed |
| [0189](0189-panorama-architecture-modeling-and-live-overlays.md) | Panorama architecture modeling and live operational overlays | Accepted (amended) | Landed |
| [0190](0190-webscrape-feed-extraction.md) | Add explicit RSS and Atom extraction to the web-scraping connector | Accepted | Landed |
| [0191](0191-built-in-tls-listener.md) | TLS 1.3 in the binary — an optional listener with operator-supplied certificates | Accepted | Landed |
| [0192](0192-remedy-default-offload.md) | BMC Remedy runs on a worker by default | Accepted | Landed |
| [0193](0193-ad-mock-in-the-console.md) | The Active Directory mockup switch belongs in the Console | Accepted | Landed |
| [0194](0194-api-tokens.md) | API tokens — a credential a machine can actually be given | Accepted | Landed |
| [0195](0195-auth-on-by-default.md) | Requiring a login is the default | Accepted | Landed |
| [0196](0196-authenticated-mcp-transport.md) | The MCP transport is authenticated, and acts as its caller | Accepted | Landed |
| [0197](0197-login-throttle-and-audit-log.md) | A throttle on the login, and a security audit trail | Accepted | Landed |
| [0198](0198-metrics-behind-the-boundary.md) | The Prometheus exposition moves behind the boundary | Accepted | Landed |
| [0199](0199-route-access-classes.md) | Every mounted route declares its access class | Accepted | Landed |
| [0200](0200-mcp-oauth-resource-server.md) | Atlas as an OAuth resource server, so a hosted MCP client can connect | Accepted | Landed |
| [0201](0201-jira-connector.md) | Atlas Jira connector | Accepted | Landed |
| [0202](0202-atlas-manages-the-ad-mock-seed.md) | Atlas holds the AD mockup's starting entries | Accepted | Landed |
| [0203](0203-worker-execution-model.md) | Worker execution model and integration terminology | Accepted | Landed |
| [0204](0204-hosted-apps-on-an-isolated-origin.md) | Hosted apps — user HTML/JS served from an isolated origin | Proposed | Not started |
| [0205](0205-connector-ownership-and-event-delivery.md) | Who owns a connector, and who may use the events it brings in | Accepted | Landed |
| [0206](0206-ad-as-a-console-connector.md) | Active Directory is a connector you configure, not one you write into a model | Accepted | Landed |
| [0207](0207-worker-type-packaging.md) | Package Worker Types as signed external runtime artifacts | Accepted | Landed |
| [0208](0208-worker-type-packages.md) | Worker Type package contract, trust, and distribution | Accepted | Landed |
| [0209](0209-roles-per-endpoint-group.md) | Roles per endpoint group | Accepted | Landed |
| [0210](0210-federated-authentication.md) | Federated authentication | Accepted | Landed |
| [0211](0211-panorama-derived-landscape-mesh.md) | Panorama's derived landscape mesh and notation projections | Accepted | Landed |
| [0212](0212-element-template-applier.md) | The element-template applier, and what a template binding means in Atlas | Proposed | Not started |
| [0213](0213-ad-mock-directory-in-the-console.md) | The mock Active Directory is visible in the Console | Accepted | Landed |
| [0214](0214-jira-inbound-issue-watch.md) | Jira as an inbound event source — a polled issue watch, deduplicated per issue | Accepted | Landed |
| [0215](0215-modeler-playground.md) | The Modeler Playground — batch simulation and analysis of a draft | Accepted | Landed |
| [0216](0216-mockups-are-one-view.md) | Mockups are one view, not one per kind | Accepted | Landed |
| [0217](0217-openapi-mock-server.md) | A mock REST API served from an OpenAPI document | Accepted | Landed |
| [0218](0218-jira-default-offload.md) | Jira runs on a worker by default | Accepted | Landed |
| [0219](0219-variable-write-attribution.md) | Variable write attribution | Accepted | Landed |
| [0220](0220-checking-a-database-connector.md) | The Console may dial a database, and the engine still links no driver | Accepted | Landed |
| [0221](0221-sql-mock-mode.md) | A database task runs against seeded answers, not against a SQL engine | Accepted | Landed |
| [0222](0222-artifact-id-renames.md) | An artifact's id is its identity — renaming moves it, collisions are refused | Accepted | Landed |
| [0223](0223-jira-account-lookup.md) | Jira account lookup | Accepted | Landed |
| [0224](0224-sql-mock-journal.md) | A mockup run is visible, and it carries what the process bound | Accepted | Landed |
| [0225](0225-inbound-watch-budget.md) | An inbound watch has an hourly budget | Accepted | Landed |
| [0226](0226-start-events-are-triggers.md) | A start event is a trigger, and the one that fires is the one that starts | Accepted | Landed |
| [0227](0227-jira-read-bounds-and-progress.md) | A Jira read is bounded and moves forward | Accepted | Landed |
| [0228](0228-user-presence.md) | User presence in the Console | Accepted | Landed |
| [0229](0229-modeler-bar-hierarchy.md) | The Modeler's editor bar carries two acts and a menu | Accepted | Landed |
| [0230](0230-process-information-model.md) | The process information model — UML classes above BPMN's data objects | Accepted | Landed |
| [0231](0231-webscrape-structured-extraction.md) | Structured HTML extraction, richer feed entries, and a fetch that survives the real web | Accepted | Landed |
| [0232](0232-uml-model-import.md) | Importing a UML class diagram — reading what somebody else drew | Accepted | Landed |
| [0233](0233-in-process-connectors-refused.md) | Finish ADR-0164 — in-process connector work becomes a finite list, then nothing | Accepted | Landed |
| [0234](0234-google-inbound-watch.md) | Google Sheets and Drive as inbound event sources — a polled row watch and a polled folder watch | Accepted | Landed |
| [0235](0235-google-sheets-worker.md) | Google Sheets as a Worker Type — a spreadsheet is a process data source | Accepted | Landed |
| [0236](0236-repeating-non-interrupting-boundary-events.md) | A non-interrupting message or signal boundary event stays armed | Accepted | Landed |
| [0237](0237-class-canvas-on-diagram-js.md) | The class canvas on diagram-js | Accepted (amended) | Landed |
| [0238](0238-child-instance-index.md) | A reverse index from call activity to child instance | Accepted | Landed |
| [0239](0239-off-loop-queries.md) | Read-only queries run off the run loop, on a consistent view | Accepted | Landed |
| [0240](0240-modeler-variables-on-the-bar.md) | Variables stays on the Modeler's bar, as a pressed button with a shortcut | Accepted | Landed |
| [0241](0241-finding-an-instance.md) | Finding an instance — a key lookup and a per-definition index | Accepted | Landed |
| [0242](0242-one-route-table-for-the-shell.md) | One route table describes the shell | Proposed | Not started |
| [0243](0243-shared-ui-primitives.md) | The views are built from shared parts | Proposed | Not started |
| [0244](0244-searchable-variables.md) | Searchable variables — a declared value index | Accepted | Landed |
| [0245](0245-call-activity-drilldown.md) | The call activity's "+" is the way into the process it calls | Accepted | Landed |
| [0246](0246-tasks-call-activity-descent.md) | The Tasks app descends into a called process instead of navigating to it | Accepted | Landed |
| [0247](0247-instance-archive-search.md) | An instance that is gone is still findable | Accepted | Landed |
| [0248](0248-search-terms-are-literal.md) | A search term is literal, and widening is asked for | Accepted | Landed |
| [0249](0249-overlay-cancelled-tokens.md) | Cancelled tokens on the runtime overlay, and a deferred choice drawn once | Accepted | Landed |
| [0250](0250-documentation-is-markdown.md) | Documentation prose is Markdown, rendered by one closed renderer | Accepted | Landed |
| [0251](0251-adjust-a-deployed-diagram.md) | Adjusting a deployed definition's diagram without redeploying it | Accepted | Landed |
| [0252](0252-runtime-badges-clear-of-labels.md) | Runtime badges hang outside the shape, clear of its caption | Accepted | Landed |
| [0253](0253-agent-tool-calls-drive-adhoc-activation.md) | Agent tool calls drive ad-hoc activation — the toolbox is the model | Accepted | Landed |
| [0254](0254-agent-rounds-on-a-worker.md) | An agent round on a worker — the toolbox travels out, the tool calls travel back | Accepted | Landed |
| [0255](0255-agent-models-are-console-workers.md) | An agent model is a Console Worker — the one field that is not a secret | Accepted (amended) | Landed |
| [0256](0256-the-model-is-authored-the-provider-is-configured.md) | The model is authored, the provider is configured — and one call is a task | Accepted | Landed |
| [0257](0257-what-an-agent-may-read.md) | What an agent may read is authored, the way its reach already is | Accepted | Landed |
| [0258](0258-discord-worker.md) | Discord as a Worker Type — a process speaks in the channel the team already reads | Accepted | Landed |
| [0259](0259-data-object-lifecycle.md) | The data object lifecycle — what the BPMN data state resolves against | Accepted | Landed |
| [0260](0260-ai-form-generation.md) | A form is generated at design time, by the Worker an operator already configured | Accepted | Landed |
| [0261](0261-instances-on-an-element.md) | The diagram is the query — filtering instances by the element they sit on | Accepted | Landed |
| [0262](0262-discord-inbound-watch.md) | Discord as an inbound event source — a channel is a log, and a snowflake is its sequence | Accepted | Landed |
| [0263](0263-form-runtime-brand-theming.md) | The brand palette reaches the form runtime | Accepted | Landed |
| [0264](0264-row-watch-mark-per-watch.md) | A row watch's idempotency mark is its own, and the cursor is why that needs no migration | Accepted | Landed |
| [0265](0265-login-off-the-run-loop.md) | Signing in does not wait for the run loop | Accepted | Landed |
| [0266](0266-stats-and-incidents-off-the-loop.md) | The runtime counts leave the run loop, and take the write paths with them | Accepted | Landed |
| [0267](0267-console-speaks-german-first.md) | The console speaks German first, through a catalogue rather than a rewrite | Accepted | Landed |
| [0268](0268-task-folders-are-saved-filters.md) | Task folders are saved filters, stored as rules and generated into FEEL | Accepted | Landed |
| [0269](0269-atlas-namespace-deploy-warning.md) | The engine stays namespace-blind, and the deploy says so | Accepted | Landed |
| [0270](0270-bounded-job-polling.md) | A poll costs a page, not a backlog | Accepted | Landed |
| [0271](0271-durable-continuation.md) | A batch persists the work it still owes | Accepted | Landed |
| [0272](0272-execution-budget.md) | One token may not hold the writer forever | Accepted | Landed |
| [0273](0273-gateway-routing-incident.md) | A gateway that cannot route parks, it does not complete | Accepted | Landed |
| [0274](0274-in-process-job-leases.md) | The in-process runner claims what it works | Accepted | Landed |
| [0275](0275-instance-visibility.md) | Reading a running instance is an object question | Accepted | Landed |
| [0276](0276-iteration-budget.md) | A loop's size is checked before it is built | Accepted | Landed |
| [0277](0277-join-scope-identity.md) | A join synchronizes within its own execution scope | Accepted | Landed |
| [0278](0278-object-authorization.md) | Filing a deployment into a project is a write on that project | Accepted | Landed |
| [0279](0279-precomputed-join-reachability.md) | Topology is compiled, including the join's ancestors | Accepted | Landed |
| [0280](0280-prove-the-prefix.md) | Recovery proves its prefix or refuses to start | Accepted | Landed |
| [0281](0281-session-role-revocation.md) | A role change takes effect on the next request, not the next login | Accepted | Landed |
| [0282](0282-store-registry.md) | One inventory of what is on disk, and what a backup owes it | Accepted | Landed |
| [0283](0283-strict-log-corruption.md) | Only the end of the active segment may be torn | Accepted | Landed |
| [0284](0284-transactional-child-view.md) | A cancellation sees the children created in its own batch | Accepted | Landed |
| [0285](0285-wal-batch-envelope.md) | A batch is one framed unit in the log | Accepted | Landed |
| [0286](0286-a-list-carries-its-own-search.md) | A list carries its own search | Accepted | Landed |
| [0287](0287-create-the-worker-from-the-incident.md) | Create the worker from the incident, and run the deploy preflight on every deploy path | Accepted | Landed |
| [0288](0288-agent-commit-attribution.md) | An agent's commit is authored by the person who asked for it | Accepted | Landed |
| [0289](0289-worker-type-setup-in-the-panel.md) | A Worker Type carries its own setup, in the panel where it is chosen | Accepted | Landed |
| [0290](0290-per-flow-join-counting.md) | A join counts tokens per incoming flow | Accepted | Landed |
| [0291](0291-one-place-for-budgets.md) | One place names every resource budget, and one way sets them | Accepted | Landed |
| [0292](0292-mim-import-worksheet.md) | A MIM import hands over its rows as data, and counts them as work | Accepted | Landed |
| [0293](0293-open-questions-in-records-expire.md) | A record that rests on an open question says so, and the question expires | Accepted | Landed |
| [0294](0294-a-variable-is-a-record.md) | A variable is a record, a collection is a loop's harvest — two budgets | Accepted | Landed |
| [0295](0295-migration-reindexes-searchable-variables.md) | A migration re-indexes what its target declares | Accepted | Landed |
| [0296](0296-a-loop-records-its-element.md) | A loop records the element it produced, not the collection so far | Accepted | Landed |
| [0297](0297-confine-internal-worker-token.md) | Confine the internal worker token to the worker protocol | Accepted | Landed |
| [0298](0298-two-states-for-a-record.md) | A record has two states — whether the decision holds, and whether it is built | Accepted | Landed |
| [0299](0299-worker-type-admission-criteria.md) | What earns a Worker Type — admission criteria for a new kind | Proposed | Not started |

## The two states of a record

A record answers two questions, and they are not the same question. **Status** says
whether the decision holds. **Implementation** says whether it is built. `go test
./docs/adr` checks both against their word lists and against each other, and the index
above carries both.

### Status

- **Proposed** — under discussion
- **Accepted** — decided and in effect
- **Superseded by ADR-XXXX** — replaced by a later decision
- **Deprecated** — no longer relevant

A parenthetical after the word is fine and common: `Accepted (amended 2026-08-17: the
marker runs on every activity kind)`. A different word is not — the guard rejects it.

### Implementation

- **Not started** — none of the chosen option is in the tree
- **Partial** — part of what the record decided is built and part is not
- **Landed** — the chosen option is in production code, with tests
- **Superseded** — a later record replaced the decision; nothing here left to measure

This field carries no parenthetical, because the index is rendered from it. What is
built and what is not belongs in the record, where a reader is.

**Partial** is the word that needs care. An extension the record itself defers is not
what makes a record partial — ADR-0154 decided an LDAP connector and listed a delta
cookie as a follow-up, and that record is `Landed`. A piece of the decision that is
missing is what makes it partial — ADR-0027 decided that an author selects and applies
an element template, and only the store behind it exists.

### Why they are two fields

They were one until 2026-09. `Status` was asked to carry both, readers used it for the
more useful question, and the two meanings drifted apart the only way they could: 115
records whose code had shipped, with tests, still read `Proposed`, because nothing in
the process ever went back to change the line. [An audit](../audits/adr-implementation-audit-2026-08-25.md)
said so in August and named 28 of them; two weeks later not one had moved. That is a
missing mechanism, not a missing afternoon.

So there is a rule with a test behind it: **`Implementation: Landed` requires `Status:
Accepted`.** Nobody merges code against a decision that has not been made, so a record
with landed code is a record whose decision was taken — whatever the front matter
still says. The reconciliation that applied this to all 295 records, and the evidence
behind each classification, is in
[the status reconciliation](../audits/adr-status-reconciliation-2026-09-09.md).
