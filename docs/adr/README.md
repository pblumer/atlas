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

## Index

| ADR | Title | Status |
|-----|-------|--------|
| [0001](0001-event-sourcing-and-log-structured-state.md) | Event sourcing and log-structured state | Accepted |
| [0002](0002-single-writer-partition-model.md) | Single-writer partition model | Accepted |
| [0003](0003-pebble-as-state-store.md) | Pebble as embedded state store | Accepted |
| [0004](0004-compile-bpmn-to-indexed-graph.md) | Compile BPMN to an integer-indexed graph | Accepted |
| [0005](0005-group-commit-and-fsync-strategy.md) | Group commit and fsync strategy | Accepted |
| [0006](0006-partition-routing-and-cross-partition.md) | Partition routing and cross-partition communication | Accepted |
| [0007](0007-job-worker-protocol.md) | Job worker protocol | Accepted |
| [0008](0008-feel-expression-strategy.md) | FEEL expression compilation strategy | Accepted |
| [0009](0009-record-serialization-format.md) | Record serialization format | Accepted |
| [0010](0010-go-and-no-cgo.md) | Go as implementation language, no CGO | Accepted |
| [0011](0011-single-binary-distribution-and-web-ui.md) | Single-binary distribution with an embedded web viewer and editor | Accepted |
| [0012](0012-web-ui-app-shell.md) | A buildless, self-contained web UI app shell | Accepted |
| [0013](0013-embed-bpmn-js-modeler.md) | Embed the bpmn-js modeler as a vendored asset | Accepted |
| [0014](0014-dmn-business-rule-tasks-via-temis.md) | DMN business rule tasks via the temis engine | Accepted |
| [0015](0015-reuse-feel-engine.md) | Reuse the external FEEL engine behind an `expr` boundary | Accepted |
| [0016](0016-mcp-server-over-http-api.md) | Model Context Protocol server as a stdio adapter over the HTTP API | Accepted |
| [0017](0017-process-instance-history.md) | Retain finished process instances in a history index | Accepted |
| [0018](0018-test-driven-development.md) | Test-driven development as the default workflow | Accepted |
| [0019](0019-durable-deployments.md) | Durable deployments via an on-disk sidecar store | Accepted |
| [0020](0020-message-correlation.md) | Message events and correlation | Accepted |
| [0021](0021-diagram-drafts.md) | Diagram drafts, separate from deployments | Accepted |
| [0022](0022-element-visit-history.md) | Retain a per-element token-visit history for the Operations overlay | Accepted |
| [0023](0023-collaborations-and-pools.md) | Collaborations and pools as multi-process deployments | Accepted |
| [0024](0024-parallel-gateway-join.md) | Parallel gateway join synchronization | Accepted |
| [0025](0025-full-properties-panel.md) | Extend the hand-written properties panel instead of vendoring bpmn-js-properties-panel | Proposed |
| [0026](0026-problems-panel-and-versioned-validation.md) | A Problems panel with validation targeted at an engine version | Proposed |
| [0027](0027-element-templates.md) | Element templates for pre-configured, reusable elements | Proposed |
| [0028](0028-forms-and-the-tasks-app.md) | User tasks, forms, and the Tasks app | Proposed |
| [0029](0029-public-process-start-links.md) | Public process start via a published form link | Accepted |
| [0030](0030-play-mode-simulation.md) | Play mode — ephemeral in-Modeler process simulation | Proposed |
| [0031](0031-diagram-version-history.md) | Diagram version history in the Modeler | Proposed |
| [0032](0032-modeler-ai-copilot.md) | In-Modeler AI copilot over the MCP/HTTP surface | Proposed |
| [0033](0033-inclusive-gateway-join.md) | Inclusive gateway join synchronization | Accepted |
| [0034](0034-projects-and-artifacts.md) | Projects as containers for heterogeneous artifacts | Accepted |
| [0035](0035-message-start-events.md) | Message start events and the processInstanceKey built-in | Accepted |
| [0036](0036-clio-connector.md) | A clio connector — server-registered event-store integration | Accepted |
| [0037](0037-structured-json-variables.md) | Structured JSON variables | Accepted |
| [0038](0038-collaboration-message-flow-replay.md) | Collaboration message-flow replay | Accepted |
| [0039](0039-dmn-io-variable-mappings.md) | Input/output variable mappings for business rule tasks | Accepted |
| [0040](0040-boundary-events.md) | Boundary events — timer and message, interrupting and non-interrupting | Accepted |
| [0041](0041-connector-management-and-secret-store.md) | Connector management and the secret store | Accepted |
| [0042](0042-user-task-assignment-and-claim.md) | User-task runtime assignment and claim/unclaim | Accepted |
| [0043](0043-openapi-spec-and-embedded-api-explorer.md) | An OpenAPI spec and an embedded API explorer for the HTTP API | Accepted |
| [0044](0044-user-management-and-authentication-boundary.md) | User management and the authentication boundary | Accepted |
| [0045](0045-user-task-assignment-bound-to-identity.md) | Binding user-task assignment to real identities | Accepted |
| [0046](0046-single-process-step-replay.md) | Single-process step-by-step replay | Accepted |
| [0047](0047-polyglot-script-tasks-via-job-workers.md) | Polyglot script tasks (PowerShell, …) via job workers | Accepted |
| [0048](0048-per-step-variable-snapshots.md) | Per-step variable snapshots in the single-process replay | Accepted |
| [0049](0049-internal-service-auth-for-mcp.md) | Internal service authentication for the in-process MCP adapter | Accepted |
| [0050](0050-temis-decision-connector.md) | Central DMN decisions via a temis decision connector | Accepted |
| [0051](0051-timer-start-events.md) | Timer start events (duration, date, cycle) | Proposed |
| [0052](0052-message-end-events.md) | Message end events | Accepted |
| [0053](0053-first-class-data-objects.md) | First-class data objects — typed, event-sourced state, and lineage | Accepted |
| [0054](0054-date-cycle-timers-for-catch-and-boundary.md) | Date and cycle timers for catch and boundary events | Proposed |
| [0055](0055-feel-expression-timer-schedules.md) | FEEL-expression timer schedules for catch and boundary events | Proposed |
| [0056](0056-feel-cycles-and-feel-start-timers.md) | FEEL cycles, and FEEL on timer start events | Proposed |
| [0057](0057-first-class-feel-temporals.md) | First-class FEEL temporals for timer schedules | Proposed |
| [0058](0058-data-output-associations.md) | Data output associations — write a value and transition a data object's state | Accepted |
| [0059](0059-data-input-associations.md) | Data input associations — read a data object into an activity | Accepted |
| [0060](0060-field-level-data-object-writes.md) | Field-level data object writes — set one member of a structured object | Accepted |
| [0061](0061-incident-model.md) | Incident model — job-failure incidents, raise, resolve, resume | Proposed |
| [0062](0062-embedded-dmn-editor.md) | An embedded DMN editor (dmn-js) | Accepted |
| [0063](0063-dmn-decision-binding.md) | DMN decision binding (latest vs deployment) | Accepted |
| [0064](0064-timer-feel-failure-incidents.md) | Timer FEEL-failure incidents — park and raise instead of firing immediately | Proposed |
| [0065](0065-multi-token-process-replay.md) | Multi-token process replay and causal token lineage | Accepted |
| [0066](0066-decision-evaluation-records.md) | Durable decision-evaluation records for debugging | Accepted |
| [0067](0067-service-task-connector-catalog.md) | A service-task connector catalog, and REST with a model-authored endpoint | Accepted |
| [0068](0068-task-io-variable-mappings.md) | Task input/output variable mappings with activity-local scopes | Proposed |
| [0069](0069-engine-internal-encrypted-secret-vault.md) | An engine-internal encrypted secret vault (ADR-0041 option A3) | Accepted |
| [0070](0070-vault-on-by-default-with-generated-key.md) | The secret vault is on by default, with a generated key | Accepted |
| [0071](0071-sharing-scopes.md) | Sharing scopes — private and shared access boundaries for design-time work | Accepted |
| [0072](0072-multiple-dmn-models-per-process.md) | Multiple DMN models per process deployment | Accepted |
| [0073](0073-principals-directory.md) | A principals directory for member and assignee pickers | Accepted |
| [0074](0074-embedded-subprocesses.md) | Embedded subprocesses (scope lifecycle via child counters) | Accepted |
| [0075](0075-clio-inbound-event-bridge.md) | A clio inbound event bridge — at-least-once ingestion with engine-side idempotent delivery | Accepted |
| [0076](0076-call-activities.md) | Call activities (single-partition) | Accepted |
| [0077](0077-multi-instance-activities.md) | Multi-instance activities (parallel and sequential) | Accepted (amended) |
| [0078](0078-design-view-token-simulation.md) | Design-view token simulation — a client-side control-flow walkthrough | Accepted |
| [0079](0079-outbound-mail-connector.md) | An outbound mail connector (SMTP first) | Accepted (amended) |
| [0080](0080-runtime-aggregate-counters.md) | Sublinear runtime views via maintained aggregate counters | Accepted |
| [0081](0081-community-marketplace-for-connectors-and-tasks.md) | A community marketplace for connectors, service tasks, and script tasks | Proposed |
| [0082](0082-event-subprocesses.md) | Event subprocesses (message- and timer-triggered, interrupting and non-interrupting) | Proposed |
| [0083](0083-o1-instance-summary.md) | An O(1) instances summary — per-definition finished-count and last-activity counters | Accepted |
| [0084](0084-csv-batch-validation.md) | CSV batch validation — upload a file, validate every row against business rules, correct the failures | Proposed |
| [0085](0085-process-instance-ttl.md) | Process-instance TTL — self-cleaning via the due-timer index | Accepted |
| [0086](0086-gateway-conditions-resolve-over-scope-chain.md) | Gateway conditions resolve over the scope chain | Proposed |
| [0087](0087-in-process-csv-ingestion.md) | In-process CSV ingestion — upload in a user task, parse in the process | Accepted |
| [0088](0088-signal-events.md) | Signal events (broadcast throw/catch) | Accepted |
| [0089](0089-error-events.md) | Error events (scoped propagation to the nearest handler) | Accepted |
| [0090](0090-bulk-terminate-instances.md) | Bulk-terminate running instances — an explicit selection and a filtered scope | Accepted |
| [0091](0091-user-task-scheduling.md) | User-task scheduling — priority and due date | Accepted |
| [0092](0092-clio-key-provisioning.md) | One-click clio credential provisioning | Accepted |
| [0093](0093-native-mail-providers.md) | Native Gmail and Microsoft Graph mail providers | Accepted |
| [0094](0094-singleton-message-start.md) | Singleton message start — at most one live instance per correlation key | Accepted |
| [0095](0095-external-variable-modification.md) | External variable modification on a running instance | Accepted |
| [0096](0096-token-simulation-events-and-inclusive-gateways.md) | Token simulation — event triggers, inclusive gateways, and an auto-decide mode | Accepted |
| [0097](0097-token-simulation-message-starts-event-subprocesses-multi-instance.md) | Token simulation — message starts, event-subprocess triggers, and multi-instance | Accepted |
| [0098](0098-external-variable-modification-audit.md) | Audit trail for external variable modifications | Accepted |
| [0099](0099-archimate-enterprise-architecture-view.md) | An ArchiMate 3.2 enterprise-architecture view | Accepted |
| [0100](0100-token-simulation-configurable-multi-instance-count.md) | Token simulation — configurable multi-instance count, modelled cardinality wins | Accepted |
| [0101](0101-token-simulation-throw-delivers-to-waiting-catch.md) | Token simulation — a thrown message/signal delivers to a waiting catch | Accepted |
| [0102](0102-receive-tasks.md) | Receive tasks | Accepted |
| [0103](0103-compensation.md) | Compensation and compensation handlers | Accepted |
| [0104](0104-token-simulation-embedded-subprocesses.md) | Token simulation — entering embedded subprocesses | Accepted |
| [0105](0105-per-server-call-activity-target-overrides.md) | Per-server call-activity target overrides | Accepted |
| [0106](0106-bmc-remedy-connector.md) | A BMC Remedy connector — server-registered ITSM entry creation | Accepted |
| [0107](0107-backup-and-restore.md) | Backup and restore — a one-file download of the design-time data directory | Accepted |
| [0108](0108-bpmn-transactions.md) | BPMN transactions (cancel end event, cancel boundary, transactional compensation) | Accepted |
| [0109](0109-full-instance-snapshot.md) | Whole-instance snapshot — a full backup that includes running instances | Accepted |
| [0110](0110-event-based-gateways.md) | Event-based gateways (deferred choice) | Accepted |
| [0111](0111-incident-model-completion.md) | Completing the incident model — retry backoff and timer-FEEL failure incidents | Accepted |
| [0112](0112-send-tasks.md) | Send tasks | Accepted |
| [0113](0113-org-wide-ui-theme.md) | Org-wide UI brand theme | Accepted |
| [0114](0114-opensearch-event-exporter.md) | OpenSearch event exporter — a WAL-tailing sink, off the hot path | Accepted |
| [0115](0115-history-retention-hard-delete.md) | History retention — an export-gated, age-based hard delete of finished instances | Accepted |
| [0116](0116-terminate-end-events.md) | Terminate end events | Accepted |
| [0117](0117-ai-agent-task.md) | An AI agent task — an LLM agent as a managed connector on the job path | Proposed |
| [0118](0118-web-scraping-connector.md) | A web-scraping connector — model-authored URL + CSS selector extraction | Accepted |
| [0119](0119-deactivate-deployed-process.md) | Deactivating a deployed process | Accepted |
| [0120](0120-mockup-service-task.md) | Mockup (engine-simulated) service tasks | Accepted |
| [0121](0121-bpmn-lanes.md) | BPMN lanes | Accepted (Layer A) |
| [0122](0122-protected-system-project-and-bootstrap-deployment.md) | A protected system project and bootstrap-deployed platform processes | Accepted |
| [0123](0123-sanctioned-user-provisioning-for-system-processes.md) | A sanctioned automated user-provisioning path for system processes | Accepted (amended) |
| [0124](0124-server-side-diagram-auto-layout.md) | Server-side BPMN diagram auto-layout in Go | Accepted |
| [0125](0125-escalation-events.md) | Escalation events (non-interrupting, propagating throw/catch) | Accepted |
| [0126](0126-self-service-registration-link.md) | Self-service registration link on the login screen | Accepted |
| [0127](0127-layered-layout-pipeline-and-invariants.md) | A layered layout pipeline and executable layout invariants | Accepted (amended) |
| [0128](0128-process-applications.md) | Process applications — the project, elevated into a deployable, versioned, portable unit | Proposed |
| [0129](0129-remote-deployment-targets.md) | Remote deployment targets — publish an application to another Atlas server | Proposed |
| [0130](0130-deprecating-a-process-version.md) | Deprecating a process version — a drain state distinct from pausing | Proposed |
| [0131](0131-engine-recovery-checkpoints-and-wal-compaction.md) | Engine recovery checkpoints and WAL compaction | Accepted |
| [0132](0132-link-events.md) | Link events (intra-scope goto — a compile-time synthetic flow) | Accepted |
| [0133](0133-standard-loop-activities.md) | Standard loop activities (the ↻ marker) | Accepted (amended) |
| [0134](0134-git-backed-applications.md) | Git-backed applications — a repository as an application's source of truth | Proposed |
| [0135](0135-retries-as-a-task-property.md) | Retries as a property of every job-backed task | Accepted |
| [0136](0136-terminated-tokens-in-the-replay.md) | Terminated tokens in the step-by-step replay | Accepted |
| [0137](0137-conditional-events.md) | Conditional events (data-triggered catch/boundary) | Accepted |
| [0138](0138-adhoc-subprocesses.md) | Ad-hoc subprocesses (on-demand, unordered contained activities) | Accepted |
| [0139](0139-csv-to-json-connector.md) | A first-class "CSV to JSON" connector kind with model-authored layout | Accepted |
| [0140](0140-live-collaborative-modeling-sessions.md) | Live collaborative modeling sessions — real-time co-editing of drafts by people and AI agents | Proposed |
| [0141](0141-sharepoint-connector.md) | A SharePoint connector (create list item, via Microsoft Graph) | Accepted |
| [0142](0142-prometheus-metrics.md) | Operational metrics over a Prometheus endpoint | Accepted |
| [0143](0143-process-documentation-export.md) | Process documentation export | Accepted |
| [0144](0144-per-definition-history-ttl.md) | Per-definition history TTL — retention the model declares | Accepted |
| [0145](0145-developer-view-for-code-fields.md) | A Developer View for code-bearing fields | Accepted |
| [0146](0146-history-expiry-due-date-index.md) | History expiry as a due-date index — retention that scales with what is due | Accepted |
| [0147](0147-splitting-the-api-server-object.md) | Splitting the api Server object, without weakening the single writer | Accepted |
| [0148](0148-org-wide-brand-logo.md) | Org-wide brand logo | Accepted |
| [0149](0149-bounded-connector-call-budget.md) | A bounded outbound-call budget for every connector | Accepted (amended) |
| [0150](0150-preview-mail-provider-and-visible-incidents.md) | A preview mail provider, and incidents on the live diagram | Accepted (amended) |
| [0151](0151-incidents-beyond-the-live-diagram.md) | Incidents beyond the live diagram — the replay, the lists, and one shared action | Accepted |
| [0152](0152-rest-connector-oauth2.md) | OAuth2 client-credentials for the REST connector | Proposed |
| [0153](0153-scim-connector.md) | SCIM 2.0 provisioning connector | Proposed |
| [0154](0154-ldap-connector.md) | Generic LDAP connector | Proposed |
| [0155](0155-secret-shape-hints.md) | The Secrets panel says what a value has to be | Accepted |
| [0156](0156-in-process-vs-out-of-process-service-tasks.md) | In-process vs. out-of-process service tasks — where a step's work runs, and what we recommend | Proposed |
| [0157](0157-worker-processes-supervision-and-console.md) | Every side-effecting task on a worker process — `atlas worker`, optional supervision, and a Workers console | Proposed |
| [0158](0158-a-connector-reference-that-explains-itself.md) | A connector reference that explains itself — and an incident you can actually resolve | Accepted |
| [0159](0159-manual-task-completion-audit.md) | Auditable manual task completion | Accepted |
| [0160](0160-fix-the-connector-from-the-incident.md) | Fix the connector from the incident | Accepted |
| [0161](0161-element-io-on-the-diagram.md) | What an element was handed, on the diagram | Accepted |
| [0162](0162-process-instance-migration.md) | Process instance migration | Proposed |
| [0163](0163-deleting-a-referenced-connector.md) | Deleting a connector deployed models still reference — and keeping a table inside its card | Accepted |
| [0164](0164-no-in-process-service-tasks.md) | No in-process service tasks — the core loop must never be able to get stuck | Accepted |
| [0165](0165-soap-connector.md) | SOAP / Web Services (WSDL) connector | Proposed |
| [0166](0166-active-directory-connector.md) | Active Directory connector | Proposed |
| [0167](0167-released-connectors-ship-in-the-marketplace.md) | A released connector ships in the marketplace | Proposed |
| [0168](0168-connector-work-on-a-worker.md) | Moving a connector onto a worker — where the task detail travels, and where the credential lives | Accepted |
| [0169](0169-incident-repair-forms.md) | A form on the incident — repairing an instance with named fields instead of raw JSON | Accepted |
| [0170](0170-adr-numbers-assigned-at-merge.md) | ADR numbers are assigned at merge, not on a branch | Accepted |
| [0171](0171-directory-file-connector.md) | A directory-file connector — LDIF and DSML | Proposed |
| [0172](0172-entra-id-connector.md) | A Microsoft Entra ID connector | Proposed |
| [0173](0173-generic-sql-connector.md) | Three SQL connectors, and the first kinds born on a worker | Proposed |

## Status values

- **Proposed** — under discussion
- **Accepted** — decided and in effect
- **Superseded by ADR-XXXX** — replaced by a later decision
- **Deprecated** — no longer relevant
