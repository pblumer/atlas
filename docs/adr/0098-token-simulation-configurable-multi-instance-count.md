# ADR-0098: Token simulation — configurable multi-instance count, modelled cardinality wins

- **Status:** Accepted
- **Date:** 2026-08-06
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0097 made multi-instance activities visible in the Design-view token simulation by
running their body a fixed constant (`3`) number of times, and recorded the constant as a
limitation: "Multi-instance count is a fixed simulation constant, not the model's real
collection size." In practice a hard-coded `3` is both arbitrary and, for models that *do*
pin a fixed loop cardinality, needlessly wrong — the diagram already says how many.

The question: how do we let the number reflect intent without pulling data/FEEL evaluation
(a collection's size) into the browser, which ADR-0078 forbids?

## Decision drivers

- **Teaching accuracy** where the model makes it possible, without evaluating data.
- **User control** for the genuinely data-driven case, where no static count exists.
- **Stay engine-free** — no evaluating `inputCollection`, no variables.

## Decision outcome

Two changes, smallest thing that removes the arbitrariness:

1. **A modelled fixed loop cardinality wins.** If a multi-instance activity carries a
   `<loopCardinality>` that is a plain positive integer (optionally FEEL-prefixed, `=3`), the
   simulation runs exactly that many instances. This is a static read of the model — no
   evaluation — so it is accurate for models that specify it.
2. **The default is configurable.** For data-driven multi-instance (a collection, no static
   cardinality), the simulation uses a count set from a small `Instances` control in the
   simulation toolbar (1–20, default 3), applied to activities entered from then on. The
   badge now reads `left/total` and labels its source ("modelled cardinality" vs.
   "simulated"), so it is honest about which number it is showing.

### Consequences

- **Positive:** the count now reflects the model when the model states it, and is a knob
  otherwise — the arbitrary constant is gone. Still no data evaluation, still engine-free.
- **Negative / trade-offs accepted:** an expression or collection-based cardinality still
  can't be evaluated, so those fall back to the configured default; the setting is a single
  global, not per-activity.
- **Follow-ups / risks to watch:** a per-activity override is a small future increment if a
  single global proves too blunt.

## Links

- refines ADR-0097 (multi-instance in the token simulation) and builds on ADR-0096 / ADR-0078
- teaches the shape of ADR-0077 (multi-instance) without executing it
