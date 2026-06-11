# ADR-0010: Go as implementation language, no CGO

- **Status:** Accepted
- **Date:** 2026-06-11
- **Deciders:** Core team

## Context and problem statement

A high-throughput, durable workflow engine could be written in several systems languages. We need a language that delivers strong concurrency ergonomics, predictable performance, easy deployment, and a healthy ecosystem for the surrounding tooling (gRPC, storage, observability) — while keeping the build and operational story simple.

## Decision drivers

- First-class concurrency (the design is goroutine-per-partition)
- Predictable performance and a manageable GC story
- Single-binary deployment, easy cross-compilation
- Strong ecosystem (gRPC, Pebble, Prometheus, OpenTelemetry)
- Team familiarity and contributor accessibility

## Considered options

1. **Go**, avoiding CGO
2. **Rust**
3. **Java / JVM** (the language of the reference engines)
4. **C++**

## Decision outcome

Chosen option: **Go, with a hard rule against CGO in the core.**

Go's goroutines and channels map directly onto the single-writer-per-partition design; the batch loop's `select`/`default` group-commit is idiomatic. Single static binaries and trivial cross-compilation suit an infrastructure component. The ecosystem (Pebble, gRPC, OTel) is strong. The CGO ban (see ADR-0003) keeps builds clean and avoids goroutine-scheduler interference from C calls.

GC is the main concern; the design mitigates it directly: no allocation on the hot path (pooled records, reused buffers), integer-indexed immutable graphs (few pointers for the GC to scan), and value-based tokens.

### Consequences

- **Positive:** Concurrency model fits the language; simple deploys; strong libraries; broad contributor pool. CGO-free builds are fast and portable.
- **Negative / trade-offs accepted:** GC exists and must be engineered around (we accept the discipline of allocation-free hot paths). Less control over memory layout than Rust/C++ (mitigated by struct-of-arrays and pooling). We forgo Rust's compile-time memory-safety guarantees in favor of Go's simplicity and velocity.
- **Follow-ups:** Allocation/escape-analysis gates in CI; GC-pause monitoring under load; `sync.Pool` usage where it measurably helps.

## Pros and cons of the options

### Go (no CGO)
- Good: ideal concurrency fit, simple deploys, strong ecosystem, fast portable builds.
- Bad: GC to engineer around; less layout control.

### Rust
- Good: no GC, maximal control, memory safety.
- Bad: steeper contribution curve; slower iteration; async ecosystem heavier for this design.

### Java/JVM
- Good: the reference engines live here; mature.
- Bad: JVM deploy/footprint; GC tuning; not the simplicity we want.

### C++
- Good: maximal performance/control.
- Bad: memory-safety burden; build/deploy complexity; smaller contributor pool.

## Links

- pairs with ADR-0003 (Pebble, pure-Go, no CGO)
- GC mitigation realized in the processor design (`docs/architecture/processor.md`)
