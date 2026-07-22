# ADR-0014: Reuse the external FEEL engine behind an `expr` boundary

- **Status:** Accepted
- **Date:** 2026-07-22
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas needs FEEL evaluation for the control-flow features of Milestone 1 and
beyond: script tasks (compute a result variable), gateway conditions, and
input/output variable mappings. ADR-0008 already set the *strategy* — compile
expressions once at deploy time to a prepared form, evaluate with no re-parsing
and minimal allocation, and record each expression's `inputs` so the engine
loads only the variables it reads. ADR-0008 explicitly left the door open:

> Where a suitable existing FEEL library offers a genuine compile-once/eval-many
> API, prefer it; otherwise compile a pragmatic FEEL subset ourselves.

A FEEL front end (lexer, parser, type checker, compiler, builtins) has been
extracted into its own module, `github.com/pblumer/feel`, from the `temis`
project. The question is whether to adopt it, and how to depend on it without
coupling Atlas to another application.

## Decision drivers

- The ADR-0008 contract: compile-once/eval-many, allocation-light eval, `inputs`
  analysis, deploy-time validation.
- No cgo (ADR-0010); minimal dependency surface.
- **Decoupling:** Atlas is an engine; it must not depend on another application's
  internals or release cadence.
- Not owning a full FEEL implementation and its correctness burden if we don't
  have to.

## Considered options

1. **Build our own FEEL subset** in `expr/` (AST or bytecode).
2. **Depend on `github.com/pblumer/temis`** and import its FEEL packages.
3. **Depend on the extracted `github.com/pblumer/feel` module**, behind a small
   Atlas-owned `expr` boundary package.

## Decision outcome

Chosen option: **Option 3 — depend on `github.com/pblumer/feel`, wrapped by an
Atlas-owned `expr` package.**

`feel` satisfies the ADR-0008 contract directly:
- `feel.CompileString` / `CompileStringRefs` parse, type-check and lower an
  expression to a `CompiledExpr` closure — compile once.
- `CompiledExpr(*Scope) (value.Value, error)` evaluates with minimal allocation —
  eval many.
- `CompileStringRefs` returns the variable names the expression references — the
  `inputs` analysis ADR-0008 requires, with no separate AST walk.
- Decimal numbers (`apd`), three-valued logic, execution limits, pure Go, no cgo.

**Boundary.** Atlas code never imports `feel` directly except in one place: the
`expr` package. `expr` exposes `Compile(src, vars...) (*Compiled, error)`,
`(*Compiled).Eval(map[string]expr.Value)`, and `(*Compiled).Inputs()`. This keeps
the rest of the engine depending on a small, stable, Atlas-owned surface, and
lets us swap or upgrade the FEEL backend without touching the compiler or the
processor.

**Dependency rule.** Atlas depends on `github.com/pblumer/feel` only — **never on
`github.com/pblumer/temis`.** The engine must not pull in another application's
surface or versioning. The extraction of FEEL into its own module is what makes
this reuse clean; that separation is a precondition of this decision, not an
afterthought.

### Consequences

- **Positive:** full, spec-tracking FEEL (builtins, dates, ranges, decimals) for
  the cost of one module; compile-once/eval-many and `inputs` come for free;
  deploy-time validation (an undeclared variable or a syntax error fails
  `Compile`, i.e. deploy). No FEEL correctness burden owned by Atlas.
- **Negative / trade-offs accepted:** a new external dependency (transitively
  `apd/v3`) on the engine's critical path; we must track `feel` versions
  deliberately (pinned by a go.mod pseudo-version today). `feel` returns
  `value.Value` (interface) results — converting FEEL values to Atlas's stored
  variable representation is our responsibility and an allocation source to watch
  on the eval path.
- **Follow-ups / risks to watch:** define how `expr.Value` results are encoded
  into Atlas variable records (a follow-up ADR/data-model note); confirm
  eval-path allocation with benchmarks once script tasks land; tag `feel`
  releases so Atlas can require a version rather than a commit pseudo-version.

## Pros and cons of the options

### Option 1 — Own subset
- Good: no external dependency; full control; tailor to exactly our needs.
- Bad: we own FEEL correctness and coverage forever; slow to reach useful breadth
  (dates, ranges, builtins); duplicates work already done in `feel`.

### Option 2 — Depend on temis
- Good: reuse without a new module.
- Bad: couples the engine to an application and its release cadence and
  dependencies; wrong architectural boundary.

### Option 3 — Depend on the extracted `feel` module, behind `expr` (chosen)
- Good: reuse with a clean module boundary; ADR-0008 satisfied; swappable backend.
- Bad: external dependency to track; value-conversion work at the boundary.

## Links

- implements the reuse path left open by ADR-0008 (FEEL expression strategy)
- respects ADR-0010 (pure Go, no cgo)
- `expr` package is the boundary; the FEEL backend is `github.com/pblumer/feel`
