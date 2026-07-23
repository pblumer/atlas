# ADR-0026: A Problems panel with validation targeted at an engine version

- **Status:** Proposed
- **Date:** 2026-07-23
- **Deciders:** Atlas maintainers

## Context and problem statement

Today the only way to learn a model is invalid is to press **Deploy & run** and
read the compiler's rejection (ADR-0013 gates authoring on the compiler). That is
a blunt, all-or-nothing signal at the end of the flow. The reference modeler we
orient on shows a persistent **Problems** panel that lists validation findings as
you edit, and a selector — "Check problems against: Camunda 8.9" — that validates
against a *chosen execution-platform version*, because what a given engine
release supports changes over time.

Atlas has the same shape of problem. The compiler's rules (reachability, gateway
coverage, supported element set) evolve milestone to milestone (Milestone 1). A
model that is valid against a newer Atlas may use elements an older deployed Atlas
rejects. Users need to see problems continuously and to check them against the
Atlas version they intend to deploy to.

The question: **where does validation run for the panel, and how is it versioned**,
without duplicating the compiler's rules in the browser (which would violate
"compile, don't interpret" and drift from the real engine)?

## Decision drivers

- **One source of validation truth.** The compiler already owns validation. The
  panel must not fork those rules into JavaScript, or they will drift and lie.
- **Continuous, non-destructive feedback.** Checking must not deploy, version, or
  run anything — it inspects a draft.
- **Version-aware.** The result must be attributable to a specific Atlas
  capability level, so "valid" means "valid *for the engine you'll deploy to*".
- **Cheap enough to run on edit** (debounced), over the buildless UI (ADR-0012).

## Considered options

1. **Client-side validator in JS** mirroring the compiler's rules.
2. **A server validate endpoint** — `POST /api/v1/validate` compiles the XML in a
   dry run and returns structured problems (element id, severity, message, rule),
   tagged with the engine version; the panel debounces calls and renders them.
3. **Deploy-only**, as today (no panel).

## Decision outcome

Chosen option: **Option 2 — a dry-run validate endpoint.** The compiler is the
validator; a new endpoint runs the real parse/resolve/validate pipeline **without
registering a definition, minting a version, or starting an instance**, and
returns a structured problem list plus the engine version that produced it. The
Modeler shows a Problems panel that calls it (debounced) on edit and on demand,
links each problem to its element on the canvas, and offers a version selector
whose value is passed to the endpoint. When the selected version differs from the
running server, the server reports what that capability level would accept
(initially just "this server's version"; a capability catalog can grow later).

Option 1 is rejected outright — it duplicates compiler rules off the hot path but
guarantees drift, exactly the interpret-don't-compile failure mode. Option 3 is
the status quo the panel exists to replace.

### Consequences

- **Positive:** Problems come from the real compiler, so they never lie; feedback
  is continuous and element-anchored; checking is side-effect-free; the version
  selector makes "valid" precise. Reuses the compiler that already exists.
- **Negative / trade-offs accepted:** A validate call is a full compile; debounce
  and cache to avoid hammering it on every keystroke. Cross-version checking is
  only as rich as the capability metadata the engine exposes — initially just the
  running version.
- **Follow-ups / risks to watch:** Define a stable problem schema (id, severity,
  rule, message, element ref) up front. Consider surfacing warnings, not just
  hard errors, as the compiler grows a lint layer.

## Links

- builds on ADR-0004 (compile BPMN to an indexed graph — the validator)
- relates to ADR-0013 (authoring gated on the compiler), ADR-0021 (drafts are
  what gets validated), ADR-0012 (buildless UI)
