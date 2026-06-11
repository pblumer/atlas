# ADR-0003: Pebble as embedded state store

- **Status:** Accepted
- **Date:** 2026-06-11
- **Deciders:** Core team

## Context and problem statement

The materialized state (the fold of the event log) needs a fast, embedded, ordered key-value store that supports range scans (for the timer due-date index, the activatable-job index, etc.). The reference design in this space (Zeebe) uses RocksDB. RocksDB in Go means CGO, which interferes with the goroutine scheduler, complicates builds and cross-compilation, and adds operational friction. We want the LSM-tree benefits without CGO.

## Decision drivers

- Ordered KV with efficient prefix/range scans
- High write throughput (LSM-tree, append-friendly, pairs with our log-structured design)
- Pure Go (no CGO): clean builds, easy cross-compilation, no scheduler interference
- Production maturity

## Considered options

1. **RocksDB via CGO** (gorocksdb / grocksdb)
2. **Pebble** (CockroachDB's pure-Go LSM store, RocksDB-compatible design)
3. **Badger** (pure-Go LSM store)
4. **BoltDB / bbolt** (pure-Go B+tree)

## Decision outcome

Chosen option: **Pebble.** It is a pure-Go, production-proven LSM-tree store (it backs CockroachDB), with the RocksDB-style design we want, efficient range scans, and no CGO.

### Consequences

- **Positive:** No CGO — clean static builds, trivial cross-compilation, no goroutine-scheduler interference. LSM design matches our append-heavy, log-structured workload. Battle-tested at scale. Good range-scan performance for our index column families.
- **Negative / trade-offs accepted:** Pebble's API and tuning differ from RocksDB; we own the tuning (block cache, compaction). The store is abstracted behind our own `StateStore` interface so it can be swapped if needed.
- **Follow-ups:** Tuning pass once realistic workloads exist; benchmark Badger as a comparison point if Pebble tuning proves difficult.

## Pros and cons of the options

### RocksDB (CGO)
- Good: the reference; extremely mature.
- Bad: CGO — scheduler interference, build/cross-compile pain, operational friction.

### Pebble
- Good: pure Go, production-proven, RocksDB-like, good range scans.
- Bad: we own tuning; smaller ecosystem of tuning knowledge than RocksDB.

### Badger
- Good: pure Go, LSM, popular.
- Bad: value-log/GC model differs; mixed reports under some write-heavy patterns.

### bbolt
- Good: simple, reliable, pure Go.
- Bad: B+tree with a single writer lock and copy-on-write pages; weaker for high write throughput.

## Links

- materializes state for ADR-0001
- abstracted behind `state.StateStore` to keep the choice reversible
