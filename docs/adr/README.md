# Architecture Decision Records

This directory records the significant architectural decisions made on Atlas, using the [MADR](https://adr.github.io/madr/)-influenced format described in [`template.md`](template.md).

An ADR captures a decision, the context that forced it, the options considered, and the consequences accepted. ADRs are immutable once accepted: if a decision changes, a new ADR supersedes the old one rather than editing it.

## Index

> **Note on duplicate numbers.** Concurrent work assigned four numbers — **0051**, **0077**, **0080**, **0082** — to two distinct ADRs each. Both files are real and are listed here under the shared number (distinguished by title and link) rather than silently dropping one. A future cleanup may renumber one side of each pair; until then, cite ADRs by *filename* when the number is ambiguous.

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
| [0029](0029-public-process-start-links.md) | Public process start via a published form link | Proposed |
| [0030](0030-play-mode-simulation.md) | Play mode — ephemeral in-Modeler process simulation | Proposed |
| [0031](0031-diagram-version-history.md) | Diagram version history in the Modeler | Proposed |
| [0032](0032-modeler-ai-copilot.md) | In-Modeler AI copilot over the MCP/HTTP surface | Proposed |
| [0033](0033-inclusive-gateway-join.md) | Inclusive gateway join synchronization | Accepted |
| [0034](0034-projects-and-artifacts.md) | Projects as containers for heterogeneous artifacts | Accepted |
| [0035](0035-message-start-events.md) | Message start events and the processInstanceKey built-in | Accepted |
| [0036](0036-clio-connector.md) | A clio connector — server-registered event-store integration | Accepted |
| [0037](0037-structured-json-variables.md) | Structured JSON variables (objects and arrays) | Accepted |
| [0038](0038-collaboration-message-flow-replay.md) | Retain a message-flow history and replay a collaboration's messages | Accepted |
| [0039](0039-dmn-io-variable-mappings.md) | Input/output variable mappings for business rule tasks | Accepted |
| [0040](0040-boundary-events.md) | Boundary events — timer and message, interrupting and non-interrupting | Accepted |
| [0041](0041-connector-management-and-secret-store.md) | Connector management and the secret store | Accepted |
| [0042](0042-user-task-assignment-and-claim.md) | User-task runtime assignment and claim/unclaim | Accepted |
| [0043](0043-openapi-spec-and-embedded-api-explorer.md) | An OpenAPI spec and an embedded API explorer for the HTTP API | Accepted |
| [0044](0044-user-management-and-authentication-boundary.md) | User management and the opt-in authentication boundary | Accepted |
| [0045](0045-user-task-assignment-bound-to-identity.md) | Binding user-task assignment to real identities | Accepted |
| [0046](0046-single-process-step-replay.md) | Retain a per-instance element-step history and replay a single process step by step | Accepted |
| [0047](0047-polyglot-script-tasks-via-job-workers.md) | Polyglot script tasks (PowerShell, …) via job workers | Accepted |
| [0048](0048-per-step-variable-snapshots.md) | Per-step variable snapshots in the single-process replay | Accepted |
| [0049](0049-internal-service-auth-for-mcp.md) | Internal service authentication for the in-process MCP adapter | Accepted |
| [0050](0050-temis-decision-connector.md) | Central DMN decisions via a temis decision connector | Accepted |
| [0051](0051-timer-start-events.md) | Timer start events (duration, date, cycle, cron) | Proposed |
| [0051](0051-user-task-scheduling.md) | User-task scheduling — priority and due date | Accepted |
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
| [0062](0062-embedded-dmn-editor.md) | An embedded DMN editor (dmn-js) — author a decision in place, adopt its inputs/output | Accepted |
| [0063](0063-dmn-decision-binding.md) | DMN decision binding — latest vs deployment on a business rule task | Accepted |
| [0064](0064-timer-feel-failure-incidents.md) | Timer FEEL-failure incidents — park and raise instead of firing immediately | Proposed |
| [0065](0065-multi-token-process-replay.md) | Multi-token process replay and causal token lineage | Accepted |
| [0066](0066-decision-evaluation-records.md) | Durable decision-evaluation records — capture a decision's inputs, outputs, and trace for debugging | Accepted |
| [0067](0067-service-task-connector-catalog.md) | A service-task connector catalog, and REST with a model-authored endpoint | Accepted |
| [0068](0068-task-io-variable-mappings.md) | Task input/output variable mappings with activity-local scopes | Proposed |
| [0069](0069-engine-internal-encrypted-secret-vault.md) | An engine-internal encrypted secret vault (ADR-0041 option A3) | Accepted |
| [0070](0070-vault-on-by-default-with-generated-key.md) | The secret vault is on by default, with a generated key | Accepted |
| [0071](0071-sharing-scopes.md) | Sharing scopes — private and shared access boundaries for design-time work | Accepted |
| [0072](0072-multiple-dmn-models-per-process.md) | Multiple DMN models per process deployment | Accepted |
| [0073](0073-principals-directory.md) | A principals directory for member and assignee pickers | Accepted |
| [0074](0074-embedded-subprocesses.md) | Embedded subprocesses — scope lifecycle via child counters, reusing the ADR-0068 scope substrate | Accepted |
| [0075](0075-clio-inbound-event-bridge.md) | A clio inbound event bridge with engine-side idempotent delivery | Accepted |
| [0076](0076-call-activities.md) | Call activities (single-partition) — start a separate process as a child instance, linked by a caller key | Accepted |
| [0077](0077-multi-instance-activities.md) | Multi-instance activities (parallel and sequential) | Accepted |
| [0077](0077-clio-key-provisioning.md) | One-click clio credential provisioning | Accepted |
| [0078](0078-design-view-token-simulation.md) | Design-view token simulation — a client-side control-flow walkthrough | Accepted |
| [0079](0079-outbound-mail-connector.md) | An outbound mail connector (SMTP first) — provider managed like clio, message model-authored like REST | Accepted |
| [0080](0080-native-mail-providers.md) | Native Gmail and Microsoft Graph mail providers — OAuth2 (app-only + refresh-token) behind the mail.Client seam, credential bundle in the vault | Accepted |
| [0080](0080-runtime-aggregate-counters.md) | Sublinear runtime views via maintained aggregate counters | Accepted |
| [0081](0081-community-marketplace-for-connectors-and-tasks.md) | A community marketplace for connectors, service tasks, and script tasks | Proposed |
| [0082](0082-singleton-message-start.md) | Singleton message start — at most one live instance per correlation key | Accepted |
| [0082](0082-event-subprocesses.md) | Event subprocesses (message- and timer-triggered, interrupting and non-interrupting) | Proposed |
| [0083](0083-o1-instance-summary.md) | An O(1) instances summary — per-definition finished-count and last-activity counters | Accepted |
| [0084](0084-csv-batch-validation.md) | CSV batch validation — upload a file, validate every row against business rules (DMN), correct the failures | Proposed |
| [0085](0085-process-instance-ttl.md) | Process-instance TTL — self-cleaning expiry via the due-timer index | Proposed |
| [0086](0086-gateway-conditions-resolve-over-scope-chain.md) | Gateway conditions resolve over the scope chain, so a gateway inside a subprocess branches on its scope's variables | Proposed |
| [0087](0087-in-process-csv-ingestion.md) | In-process CSV ingestion — upload in a user task, layout in a script task, parse in a service task | Accepted |
| [0088](0088-signal-events.md) | Signal events (broadcast throw/catch) | Proposed |
| [0089](0089-error-events.md) | Error events (scoped propagation to the nearest handler) | Proposed |
| [0090](0090-bulk-terminate-instances.md) | Bulk-terminate running instances — an explicit selection and a filtered scope | Accepted |

## Status values

- **Proposed** — under discussion
- **Accepted** — decided and in effect
- **Superseded by ADR-XXXX** — replaced by a later decision
- **Deprecated** — no longer relevant
