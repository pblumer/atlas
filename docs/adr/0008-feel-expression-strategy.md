# ADR-0008: FEEL expression compilation strategy

- **Status:** Accepted
- **Date:** 2026-06-11
- **Deciders:** Core team

## Context and problem statement

BPMN models contain expressions everywhere: gateway conditions, input/output variable mappings, timer definitions, message correlation keys. The de-facto standard language is **FEEL** (from the DMN spec). These expressions sit on the hot path — a gateway condition is evaluated every time a token reaches the gateway. Parsing FEEL text at evaluation time would be a severe throughput drain and an allocation source.

## Decision drivers

- Zero parsing on the hot path
- Minimal allocation per evaluation
- Knowing statically which variables an expression reads (to avoid loading the whole scope)
- Reasonable FEEL coverage without owning a full language implementation forever

## Considered options

1. **Parse FEEL text at evaluation time** (per gateway hit)
2. **Use an existing FEEL library** with a compile-once / eval-many API
3. **Use an existing library that re-parses each call**
4. **Compile a FEEL subset ourselves** to an AST or stack-VM bytecode at deploy time

## Decision outcome

Chosen option: **compile expressions at deploy time to a prepared form (AST now, stack-VM bytecode later if needed); evaluate with no parsing and no allocation on the hot path.** Where a suitable existing FEEL library offers a genuine compile-once/eval-many API, prefer it; otherwise compile a pragmatic FEEL subset ourselves.

Each `CompiledExpression` also records its `inputs` (the variable indices it reads), so the processor loads only those variables instead of materializing the whole scope.

### Consequences

- **Positive:** No parsing on the hot path; evaluation is allocation-light. The `inputs` hint minimizes variable loading. Conditions and mappings are validated at deploy time.
- **Negative / trade-offs accepted:** If we compile our own subset, we must document exactly which FEEL features are supported and grow coverage over time. Vetting third-party libraries for a non-reparsing API is required; many parse on each call and are unsuitable.
- **Follow-ups:** Decide AST vs. bytecode after profiling; publish a FEEL coverage matrix; conformance tests against DMN FEEL examples.

## Pros and cons of the options

### Parse at eval time
- Good: trivial.
- Bad: parsing + allocation on the hottest path; unacceptable throughput cost.

### Existing library, compile-once
- Good: full FEEL coverage without owning it.
- Bad: must confirm the API truly compiles once; dependency surface.

### Existing library, re-parses
- Good: easy to adopt.
- Bad: same hot-path cost as option 1.

### Own subset → AST/bytecode
- Good: full control, fastest, `inputs` analysis built in.
- Bad: we own coverage and correctness; ongoing maintenance.

## Links

- part of the compiler pipeline (ADR-0004)
- detailed in `docs/architecture/compiler.md`
