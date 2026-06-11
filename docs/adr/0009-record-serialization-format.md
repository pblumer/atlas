# ADR-0009: Record serialization format

- **Status:** Accepted
- **Date:** 2026-06-11
- **Deciders:** Core team

## Context and problem statement

Records (events) are written to the log millions of times per second in the throughput aspiration, and read back on recovery. The serialization format is therefore directly throughput-relevant. It must encode/decode with minimal CPU and allocation, while remaining debuggable and able to evolve as the schema changes.

## Decision drivers

- Minimal encode/decode CPU and allocation on the hot path
- Schema evolution as value types and fields change
- Debuggability (we can inspect the log)
- Avoid premature complexity

## Considered options

1. **JSON** — human-readable, ubiquitous
2. **Protobuf** — schema-versioned, good tooling
3. **Hand-written binary** into a pre-allocated buffer, with a version byte
4. **Zero-copy / SBE-style** — read fields by offset directly from the byte slice, never materialize structs

## Decision outcome

Chosen option: **hand-written binary encoding with an explicit version byte**, as the starting point. It writes directly into a reused, pre-allocated buffer with no reflection — far faster than Protobuf — while staying debuggable. Schema evolution is handled explicitly via the version byte plus additive field rules.

Zero-copy (option 4) is deliberately **deferred** until profiling proves (de)serialization is the hotspot; adopting it earlier would be premature optimization that complicates the code before it's justified.

### Consequences

- **Positive:** Fast, allocation-light encoding/decoding; full control of layout; debuggable with a small reader. A clear upgrade path to zero-copy later for the hottest records.
- **Negative / trade-offs accepted:** We own schema evolution discipline (version byte, additive changes, migration on read). Hand-rolled codecs are more code to test than generated ones. No cross-language schema artifact (mitigated: the *external* job-worker API uses gRPC/Protobuf; this format is internal to the log).
- **Follow-ups:** Codec test suite incl. round-trip and version-skew tests; revisit zero-copy after profiling; keep the external API (Protobuf) separate from the internal log format.

## Pros and cons of the options

### JSON
- Good: readable, universal.
- Bad: reflection, allocation, parsing cost; disqualified for the hot path.

### Protobuf
- Good: schema evolution, tooling, cross-language.
- Bad: not the fastest on the hottest paths; allocation per message; reflection-ish overhead in Go.

### Hand-written binary
- Good: fastest practical to start; full control; debuggable.
- Bad: we own evolution and correctness.

### Zero-copy / SBE
- Good: ultimate throughput; read directly off the log bytes.
- Bad: most complex; premature before profiling proves the need.

## Links

- format used by the WAL (ADR-0001, ADR-0005)
- external worker API uses Protobuf/gRPC (ADR-0007), kept separate from this internal format
