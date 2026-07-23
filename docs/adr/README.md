# Architecture Decision Records

This directory records the significant architectural decisions made on Atlas, using the [MADR](https://adr.github.io/madr/)-influenced format described in [`template.md`](template.md).

An ADR captures a decision, the context that forced it, the options considered, and the consequences accepted. ADRs are immutable once accepted: if a decision changes, a new ADR supersedes the old one rather than editing it.

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
| [0041](0041-connector-management-and-secret-store.md) | Connector management and the secret store | Proposed |
| [0042](0042-user-task-assignment-and-claim.md) | User-task runtime assignment and claim/unclaim | Accepted |

## Status values

- **Proposed** — under discussion
- **Accepted** — decided and in effect
- **Superseded by ADR-XXXX** — replaced by a later decision
- **Deprecated** — no longer relevant
