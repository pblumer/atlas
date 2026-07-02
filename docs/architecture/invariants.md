# Invariants

The load-bearing rules of Atlas. Every one of these is something the engine's **correctness or performance depends on** — they are not style preferences. Breaking one may not fail a local test, but it breaks the system's guarantees.

This document is the compact, checkable reference. The reasoning lives in the linked ADRs and deep-dives. If a change must break an invariant, that requires a new ADR superseding the relevant decision — not a workaround.

---

## I1 — No allocation on the hot path

**Rule:** The processor batch cycle must not allocate per command or per event. Reuse buffers, pool records, prefer value types and integer indices.

**Why:** Go's GC pauses scale with allocation rate and live-pointer count. The throughput target depends on a GC-quiet hot path. (ADR-0010)

**How to check:** `testing.AllocsPerRun` around processor-path code; benchmark with `-benchmem`; watch for `make`/`append`-growth/boxing in the batch loop and behaviors.

**Common violations:** building a slice/map per command; `fmt.Sprintf` on the hot path; interface boxing of values; closures capturing per-iteration data outside the post-fsync side-effect phase.

---

## I2 — Durable before visible

**Rule:** The order is fixed: append to log → **one** `fsync` per batch → commit state → run side effects. Nothing externally observable (client ack, worker notification, response, network call) may happen before that batch's `fsync` returns.

**Why:** A side effect that precedes durability can act on an event lost in a crash (e.g. a worker processing a job the engine then "forgets"). (ADR-0005)

**How to check:** All side effects go through the processor's post-fsync phase (`ProcessingContext.SideEffect`). No I/O, no notifications, no client responses inside command handling or `applyToState`.

**Common violations:** notifying a worker inside `OnActivated`; replying to a client before the batch flushes; calling out to the network during command processing.

---

## I3 — Single writer per partition

**Rule:** Exactly one goroutine processes a partition's commands and mutates its state. No locks on process state. No partition reads or writes another partition's state directly.

**Why:** Determinism (for replay-based recovery) and lock-free throughput depend on it. (ADR-0002)

**How to check:** No mutexes guarding process state; no shared mutable state between partition goroutines; cross-partition effects emitted as messages (durable events delivered to the other partition's queue), never as direct calls. (ADR-0006)

**Common violations:** adding a lock "just to be safe"; reaching into another partition's state store; sharing a mutable cache across partitions.

---

## I4 — One `applyToState`, deterministic and side-effect-free

**Rule:** State mutation from a record happens in exactly one function, used identically by the live processor and by recovery replay. It must be deterministic and free of side effects (no time reads, no RNG, no I/O, no notifications).

**Why:** Recovery correctness is "replaying events through the same code reproduces the same state." Any divergence between live and replay paths, or any non-determinism inside, breaks that. (ADR-0001)

**How to check:** `applyToState` (and everything it calls) only mutates the passed transaction. Timestamps and generated keys are read *from the record*, never produced inside.

**Common violations:** calling `time.Now()` or `keyGen.Next()` inside `applyToState`; emitting a notification from it; having the live path mutate state in a way replay doesn't.

---

## I5 — Compile, don't interpret

**Rule:** Work that can be done once at deploy time — XML parsing, model validation, string interning, FEEL expression compilation — must not happen on the runtime hot path.

**Why:** Execution happens orders of magnitude more often than deployment. Repeated parsing/lookups on the hot path destroy throughput. (ADR-0004, ADR-0008)

**How to check:** Engine/behavior code indexes into the immutable `CompiledProcess` (integer indices, interned strings, pre-compiled expressions). No XML, no FEEL text parsing, no `map[string]` lookups during execution.

**Common violations:** parsing a FEEL condition when a gateway is evaluated; looking up an element by string ID at runtime; re-deriving topology that the compiler already linearized.

---

## I6 — Events are facts, commands are intentions

**Rule:** Only events are persisted. Commands may be rejected and are never written to the log. Non-deterministic values (generated keys, timestamps) are computed once, written *into* the event, and on replay are read back — never regenerated.

**Why:** This is what makes the log a faithful, replayable record of what actually happened. (ADR-0001, data-model.md)

**How to check:** Recovery replays events only and ignores commands. Generated keys/timestamps appear in event payloads/headers and are consumed (not produced) during replay.

**Common violations:** persisting commands; regenerating a key during replay; deciding control flow on replay differently than it was decided live.

---

## Quick pre-commit checklist for agents

Before considering a change done, confirm:

- [ ] No new per-command allocation on the processor path (I1)
- [ ] All side effects run post-fsync; nothing observable precedes durability (I2)
- [ ] No new locks on process state; no cross-partition direct access (I3)
- [ ] `applyToState` stays deterministic and side-effect-free; live == replay (I4)
- [ ] No parsing/interning/string-lookup added to the hot path (I5)
- [ ] Only events persisted; keys/timestamps frozen into events, not regenerated on replay (I6)
- [ ] `go build ./...`, `go test -race ./...`, `go vet ./...` pass; `gofmt -l .` empty
- [ ] New behavior has tests; persistence/processor changes have a recovery test
- [ ] Any architectural change is captured in a new ADR
