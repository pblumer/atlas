# Chrampfer

> A blazing-fast, durable BPMN 2.x workflow engine in Go.

**Chrampfer** is named after the Bernese German word for someone who works hard and never quits. Because that's exactly what it does: it *chrampfet* through millions of process instances, batch after batch, and never drops a token.

> 🚧 **Early development.** APIs are unstable and changing fast. Not ready for production use. See the [roadmap](ROADMAP.md).

---

## Why another workflow engine?

Most BPMN engines spend their time interpreting XML at runtime and writing process state to a SQL database one transaction at a time. Both are throughput killers. Chrampfer takes a different path, borrowed from the design lineage of log-structured, event-sourced systems:

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
- **[Roadmap](ROADMAP.md)** — where this is going
- **[Contributing](CONTRIBUTING.md)** · **[Development](DEVELOPMENT.md)** · **[Security](SECURITY.md)**

**Working on this with an AI coding agent?** Start at **[`AGENTS.md`](AGENTS.md)** (Claude Code: [`CLAUDE.md`](CLAUDE.md)). It carries the invariants, the exact build/test commands, and how to approach a task.

## Goals

- Durable execution that survives crashes and runs long-lived processes (timers, message events, multi-week instances)
- Full BPMN 2.0 coverage including subprocesses, boundary events, and event subprocesses
- High throughput — many instances per second per partition
- Pure Go, no CGO (embedded LSM-tree state store, e.g. Pebble)

## Non-goals (for now)

- A graphical modeler — Chrampfer executes BPMN, it doesn't draw it
- A full-stack, batteries-included server — Chrampfer is a library/engine core first

## License

[Apache License 2.0](LICENSE). *(Proposed default — chosen for its explicit patent grant, which suits an infrastructure component others build on. Change it if you prefer MIT or another license before the first release.)*

---

*Built by someone who appreciates a good chrampfer.*
