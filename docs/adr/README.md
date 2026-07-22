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
| [0019](0019-message-correlation.md) | Message events and correlation | Accepted |

## Status values

- **Proposed** — under discussion
- **Accepted** — decided and in effect
- **Superseded by ADR-XXXX** — replaced by a later decision
- **Deprecated** — no longer relevant
