# Atlas

> A blazing-fast, durable BPMN 2.x workflow engine in Go.

**Atlas** is named after the Titan who bears an immense load without ever letting it drop. That's exactly what it does: it carries millions of process instances, batch after batch, and never drops a token.

> 🚧 **Early development.** APIs are unstable and changing fast. Not ready for production use. See the [roadmap](ROADMAP.md).
>
> ⚖️ **Temporary project name and release gate.** `Atlas` is currently a development name under trade-mark review. It must not be used for a public or commercial release unless it is professionally cleared; the preferred remediation is a rename before release. See the [IP and licence review](docs/legal/IP_AND_LICENSE_REVIEW.md), [remediation plan](docs/legal/REMEDIATION_PLAN.md), [trade-mark notice](TRADEMARKS.md), and [third-party notices](THIRD_PARTY_NOTICES.md).

---

## Why another workflow engine?

Most BPMN engines spend their time interpreting XML at runtime and writing process state to a SQL database one transaction at a time. Both are throughput killers. Atlas takes a different path, borrowed from the design lineage of log-structured, event-sourced systems:

- **Compile, don't interpret.** BPMN models are compiled once at deploy time into a flat, integer-indexed execution graph. At runtime there are no string lookups, no XML parsing, no map access on the hot path — just pointer arithmetic over cache-friendly slices.
- **Event sourcing over state mutation.** State is never written in place. Every state transition is an append-only event in a write-ahead log. The live state is a materialization of that log, kept in an embedded key-value store.
- **Group commit.** Many events are made durable with a *single* `fsync`. One fsync per event caps you at a few thousand per second; one fsync per thousand events unlocks millions.
- **Single writer per partition.** Each partition is driven by one goroutine processing commands sequentially — no locks, no mutex contention, cache-friendly state access, and trivially deterministic recovery via log replay. Scale horizontally by adding partitions, not threads.

## Design at a glance

```
Command → [Single-writer Processor] → State mutation (in-memory tx) + Events
                                              │
                                    Batched WAL append + one fsync
                                              │
                                    State commit → followup commands → side effects
                                              │
                                    (Recovery: replay events → state)
```

The three core pillars:

1. **The graph compiler** turns hierarchical BPMN XML into immutable, integer-indexed slices — nodes, flows, and scopes — with interned strings and pre-compiled expressions. Expensive once, cheap a million times.
2. **The processor** moves tokens through that graph as a deterministic fold over an event log. A single batch loop collects commands, processes them purely in-memory against a transaction, makes the whole batch durable with one fsync, then runs visible side effects.
3. **The data model** makes every step a keyed record with a `(ValueType, Intent)` discriminator. The same `applyToState` function runs live and during recovery, so the log and the state can never diverge.

## Documentation

- **[Architecture overview](docs/ARCHITECTURE.md)** — the canonical reference for how the system fits together
  - [Graph compiler](docs/architecture/compiler.md)
  - [Processor](docs/architecture/processor.md)
  - [Data model](docs/architecture/data-model.md)
  - [Glossary](docs/architecture/glossary.md)
  - [Invariants](docs/architecture/invariants.md) — the rules the engine's correctness depends on
- **[Architecture Decision Records](docs/adr/)** — *why* things are the way they are
- **[Postman onboarding kit](postman/)** — import the collection + environment and drive the HTTP API (deploy, run instances, work user tasks) in five minutes
- **[Roadmap](ROADMAP.md)** — where this is going
- **[Legal and IP review](docs/legal/IP_AND_LICENSE_REVIEW.md)** · **[Remediation plan](docs/legal/REMEDIATION_PLAN.md)**
- **[Trade marks](TRADEMARKS.md)** · **[Third-party notices](THIRD_PARTY_NOTICES.md)**
- **[Contributing](CONTRIBUTING.md)** · **[Development](DEVELOPMENT.md)** · **[Security](SECURITY.md)**

**Working on this with an AI coding agent?** Start at **[`AGENTS.md`](AGENTS.md)** (Claude Code: [`CLAUDE.md`](CLAUDE.md)). It carries the invariants, the exact build/test commands, and how to approach a task.

## Goals

- Durable execution that survives crashes and runs long-lived processes (timers, message events, multi-week instances)
- Full BPMN 2.0 coverage including subprocesses, boundary events, and event subprocesses
- High throughput — many instances per second per partition
- Pure Go, no CGO (embedded LSM-tree state store, e.g. Pebble)

## Non-goals (for now)

- A *bespoke* graphical modeler — Atlas ships a browser viewer/editor by embedding the standard [`bpmn-js`](https://bpmn.io) toolkit ([ADR-0011](docs/adr/0011-single-binary-distribution-and-web-ui.md)), rather than reimplementing BPMN rendering from scratch
- A full-stack, batteries-included server beyond the single self-contained binary — the engine core stays a library first, embedded by the server

## Independence and compatibility

This is an independent project. It is not affiliated with or endorsed by the Object Management Group, the Apache Software Foundation, Camunda or bpmn.io. BPMN, DMN, Camunda, Zeebe and other third-party names are used only to identify standards, upstream software or specific interoperability behaviour. See [TRADEMARKS.md](TRADEMARKS.md).

## License

Original project code is licensed under the [Apache License 2.0](LICENSE), unless a file states otherwise. Embedded and vendored third-party components retain their own licences and attribution requirements; see [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

---

*Built by someone who appreciates a good atlas.*
