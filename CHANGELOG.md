# Changelog

All notable changes to Atlas are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and Atlas aims to
follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html) once the API
stabilizes. While the project is pre-1.0 the public API and on-disk format may
change between minor versions.

## [Unreleased]

## [0.1.0] - 2026-07-28

First tagged release and the first published binary. This is an early
developer preview: the engine runs real BPMN end to end and survives crashes,
but APIs and the on-disk format are still moving fast and it is not ready for
production use.

### Distribution

- **Single self-contained binary.** The engine, HTTP API, and browser-based
  BPMN modeler/operator UI ship in one executable, with the web assets embedded
  via `go:embed` — no runtime dependencies and no external database
  (ADR-0011).
- **Pre-built binaries** for Linux, macOS, and Windows (amd64 and arm64),
  published on each `v*` tag by the new release workflow together with a
  `SHA256SUMS.txt` manifest.
- **`atlas version`** command reports the build version, commit, and date,
  stamped in at build time (or read from Go's embedded build info for
  `go install`).

### Engine

- Event-sourced, single-writer processor: commands fold into durable events,
  made durable with one `fsync` per batch (group commit), with state
  materialized in an embedded key-value store.
- Crash recovery by log replay, with a single `applyToState` used identically
  live and on recovery.
- BPMN graph compiler: XML is compiled once at deploy time into an immutable,
  integer-indexed execution graph with interned strings and pre-compiled FEEL
  expressions.

### BPMN & execution

- Start/end events, sequence flows, service tasks, user tasks, exclusive
  (data-based) gateways, and process variables with input binding.
- Script tasks evaluated in-engine (FEEL) or via external interpreters
  (JavaScript, Python, PowerShell), gated by flags with a per-task timeout.
- Business-rule tasks backed by DMN/FEEL decisions.
- Job protocol with an in-process worker subscription and an operator HTTP
  endpoint to complete jobs by hand.

### Server & tooling

- `atlas serve` runs the engine, HTTP API, and web UI; `atlas mcp` runs the
  Model Context Protocol adapter so an AI agent can drive a running engine
  (ADR-0016).
- Optional login/auth, an encrypted secret vault (ADR-0070), and an
  OpenAPI spec served with a Scalar API explorer.
- Browser-based BPMN modeler (bpmn-js) with project scopes and sharing.

[Unreleased]: https://github.com/pblumer/atlas/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/pblumer/atlas/releases/tag/v0.1.0
