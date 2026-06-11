# AGENTS.md

Operational guide for AI coding agents working on Chrampfer. Human contributors: see [`CONTRIBUTING.md`](CONTRIBUTING.md). This file follows the [agents.md](https://agents.md) convention; [`CLAUDE.md`](CLAUDE.md) points here.

> **Read this whole file before writing code.** Chrampfer's correctness and performance rest on a handful of non-negotiable invariants. A change that looks locally correct can silently break the engine if it violates one of them. The invariants are listed below and in [`docs/architecture/invariants.md`](docs/architecture/invariants.md).

---

## What this project is

Chrampfer is a durable, high-throughput **BPMN 2.x workflow engine** in Go. It executes business process models by moving tokens through a compiled graph, persisting every state transition as an event in an append-only log, and materializing state in an embedded key-value store.

Three pillars (each has a deep-dive doc):
- **Compiler** ([`docs/architecture/compiler.md`](docs/architecture/compiler.md)) — BPMN XML → immutable, integer-indexed `CompiledProcess`.
- **Processor** ([`docs/architecture/processor.md`](docs/architecture/processor.md)) — single-writer loop that folds commands into durable events.
- **Data model** ([`docs/architecture/data-model.md`](docs/architecture/data-model.md)) — every transition is a keyed record `(ValueType, Intent)`.

Start with [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) for the whole picture, then the decision records in [`docs/adr/`](docs/adr/) for *why*.

## The invariants (do not break these)

These are load-bearing. If your task seems to require breaking one, **stop and flag it** — it needs a new ADR, not a workaround. Full explanations in [`docs/architecture/invariants.md`](docs/architecture/invariants.md).

1. **No allocation on the hot path.** The processor batch cycle must not allocate per command. Reuse buffers, pool records, prefer value types and integer indices. (ADR-0010)
2. **Durable before visible.** Ordering is always: append to log → **one** `fsync` → commit state → run side effects. Never expose, return, ack, or notify based on an event that is not yet on disk. (ADR-0005)
3. **Single writer per partition.** One goroutine owns a partition's state. No locks on process state. No partition ever touches another partition's state directly — cross-partition interaction is async message passing only. (ADR-0002, ADR-0006)
4. **One `applyToState`.** State mutation from a record lives in exactly one function, used identically live and on recovery. Never fork or duplicate that logic; recovery correctness depends on it being the same code path. (ADR-0001)
5. **Compile, don't interpret.** Work that can happen at deploy time (XML parsing, validation, string interning, FEEL compilation) must never happen on the runtime hot path. (ADR-0004, ADR-0008)
6. **Events are facts; commands are intentions.** Only events are persisted. Generated keys and timestamps are written *into* events so replay is deterministic. Never regenerate them on replay. (ADR-0001, data-model.md)

## Commands

The single source of truth for how to build, test, and check. Run these from the repo root.

```bash
# Build everything
go build ./...

# Run all tests
go test ./...

# Run tests with the race detector — MANDATORY before considering work done
go test -race ./...

# Vet and format checks (formatting must produce no output)
go vet ./...
gofmt -l .

# Run a single package's tests
go test ./engine/...

# Run a single test by name
go test ./engine/ -run TestProcessorRecovery -v
```

**Definition of done for any code change:** `go build ./...`, `go test -race ./...`, `go vet ./...` all pass, and `gofmt -l .` is empty. Do not report a task complete until these are green.

## Repository layout

The intended package structure (see [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md#component-map)):

```
compiler/   BPMN XML → CompiledProcess (parse, resolve, intern, expr, validate, linearize)
model/      Record, header, ValueType/Intent, payload encode/decode
engine/     Partition, processor loop, batching, ProcessingContext
behavior/   Per-BPMN-element behaviors (service task, gateways, events, subprocess)
state/      State store wrapper, transactions, indexes (column families)
wal/        Write-ahead log: segmented append, group commit, replay
expr/       FEEL expression compilation and evaluation
job/        Job store, worker subscription, gRPC streaming protocol
timer/      Due-date index scanning and timer triggering
api/        Client-facing command submission and queries
```

Packages may not all exist yet — the project is at Milestone 0 (see [`ROADMAP.md`](ROADMAP.md)). Check what exists before assuming.

## How to approach a task

1. **Locate it on the roadmap.** [`ROADMAP.md`](ROADMAP.md) is organized by milestone. Confirm the task belongs to the current milestone and isn't blocked by an unstarted dependency.
2. **Read the relevant deep-dive(s)** for the package you're touching, plus any ADR they reference.
3. **Check the invariants** above against your plan *before* writing code.
4. **Write tests.** New behavior needs tests. Anything touching persistence or the processor needs a recovery/replay test (process some commands, simulate restart, replay the log, assert state matches).
5. **Run the full check sequence** (see Commands) until green, including `-race`.
6. **If you changed an architectural decision**, write a new ADR (copy [`docs/adr/template.md`](docs/adr/template.md)) instead of silently diverging, and update [`docs/adr/README.md`](docs/adr/README.md).

## Testing conventions

- **Recovery tests are first-class.** The core correctness property is "state after replay == state built live." Test it explicitly for anything that emits events.
- **Determinism.** Tests must not depend on wall-clock time or goroutine scheduling. Inject the `Clock`; drive the processor synchronously where possible.
- **Hot-path allocation.** For processor-path changes, consider `testing.AllocsPerRun` / benchmark with `-benchmem` to confirm you haven't introduced per-command allocations.
- **Table-driven tests** in standard Go style.

## Things that will trip you up

- **`applyToState` is special.** It is called both live and on recovery. Side effects (notifications, network, time reads) must *not* live here — only deterministic state mutation. Put side effects in the processor's post-fsync phase.
- **Followup commands vs. events.** Emitting an event mutates state now and is persisted now. Scheduling a followup command defers work to the next batch. Don't confuse them; see `ProcessingContext` in [`processor.md`](docs/architecture/processor.md).
- **Element IDs are integer indices**, not strings, everywhere in engine code. Strings are interned at compile time. Don't reintroduce string handling on the hot path.
- **Keys encode the partition** in their high bits. Don't invent keys by hand; use the key generator.

## Style

- Standard Go; `gofmt` is non-negotiable.
- Comments explain *why*, not *what*.
- Public APIs get doc comments.
- Small, focused changes over large ones.

## Pointers

| I need to… | Go to |
|------------|-------|
| Understand the whole system | [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) |
| Know why a decision was made | [`docs/adr/`](docs/adr/) |
| See what to build next | [`ROADMAP.md`](ROADMAP.md) |
| Look up a term | [`docs/architecture/glossary.md`](docs/architecture/glossary.md) |
| Check the rules I must not break | [`docs/architecture/invariants.md`](docs/architecture/invariants.md) |
