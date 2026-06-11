# ADR-0004: Compile BPMN to an integer-indexed graph

- **Status:** Accepted
- **Date:** 2026-06-11
- **Deciders:** Core team

## Context and problem statement

BPMN models are XML: verbose, hierarchical, full of string IDs and references. Interpreting that XML at runtime — walking a DOM, resolving string IDs through maps, parsing FEEL expressions on each gateway decision — puts heavy, repeated work on the hot path. Since execution happens orders of magnitude more often than deployment, this is the wrong place to spend cycles.

## Decision drivers

- Minimal hot-path cost (no XML, no string lookups, no map access)
- Cache-friendly memory layout for token traversal
- Validate models once, at deploy time, never at runtime
- Lock-free concurrent reads of the compiled model
- Support multiple coexisting process versions

## Considered options

1. **Interpret the BPMN DOM at runtime**
2. **Pre-process into an object graph of pointers** (nodes referencing nodes)
3. **Compile into flat, integer-indexed, immutable slices** (struct-of-arrays + detail tables)

## Decision outcome

Chosen option: **compile into flat, integer-indexed, immutable slices.** Deployment runs a compiler pipeline (parse → resolve → intern → compile expressions → validate → linearize) producing a `CompiledProcess`. Element IDs become array indices; topology lives in shared contiguous arrays (offset+count); type-specific data lives in detail tables; strings are interned to `int32`; expressions are pre-compiled.

### Consequences

- **Positive:** Hot path is pointer arithmetic over cache-friendly slices — no XML, no strings, no maps, no locks. Validation happens at deploy with a human watching. The immutable result is read concurrently without synchronization. Versioning is clean: one `CompiledProcess` per version, instances pinned to their birth version.
- **Negative / trade-offs accepted:** A non-trivial compiler to build and maintain; the in-memory format is bespoke (must be (re)built on deploy and on restart from stored definitions). Debuggability requires keeping the intern table for export.
- **Follow-ups:** Persist compiled definitions vs. recompile on startup; compiler diagnostics surfaced to deployers.

## Pros and cons of the options

### Interpret the DOM
- Good: least up-front work.
- Bad: repeated parsing/lookups on the hot path; runtime validation; poor cache behavior.

### Pointer object graph
- Good: simpler than full linearization.
- Bad: pointer-chasing causes cache misses; many small allocations; GC pressure.

### Flat integer-indexed slices
- Good: cache-friendly, allocation-light, lock-free reads, deploy-time validation.
- Bad: compiler complexity; bespoke format.

## Links

- detailed in `docs/architecture/compiler.md`
- pairs with ADR-0008 (FEEL compilation)
