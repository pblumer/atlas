# Whitepapers

Architecture and product concept documents for atlas. Whitepapers explain
motivation, design choices and intended architecture; they are **not**
acceptance evidence for ADRs, implemented capabilities, security guarantees
or production readiness. Check the linked ADR and programme before quoting
implementation status.

## Library

- **atlas hub — Signed Distribution of Process Applications** —
  [dark screen PDF](atlas-hub-signierte-verteilung-dark.pdf),
  [print-friendly PDF](atlas-hub-signierte-verteilung-print.pdf) and
  [Markdown source](atlas-hub-signierte-verteilung.md).
  German technical whitepaper, **6 November 2026**, about Blueprint Registry v1,
  publishing, pinned trust, controlled imports, operations and known gaps.
  This is a design/status explanation, not independent security certification.
- [Identity-Aware Workflow Execution](identity-aware-workflow-execution.md) —
  user/service identities, delegated execution, policy decision/enforcement,
  obligations and auditable actions. German Markdown edition,
  **23 October 2026**. Related:
  [IAE delivery programme](../planning/identity-aware-execution-program.md) and
  [ADR-0475](../adr/0475-identity-aware-worker-execution.md).
- **Worker Architecture and Lifecycle** —
  [PDF](atlas-worker-whitepaper.pdf),
  [HTML](atlas-worker-whitepaper.html) and
  [Markdown source](atlas-worker-whitepaper.md).
  German technical whitepaper (draft 0.1) about external Worker Types,
  job delivery, credential handling, isolation, the atlas hub trust model,
  and the migration's documented limitations. Implementation status is
  based on the supplied facts and is not independently audited.
- **Horizontal Scaling** —
  [PDF](atlas-technisches-whitepaper-horizontale-skalierung.pdf) and
  [editable Word document](atlas-technisches-whitepaper-horizontale-skalierung.docx).
  Architecture concept for scaling the durable BPMN workflow engine.
  See [roadmap](../../ROADMAP.md) and the relevant
  [architecture decisions](../adr/README.md) for current implementation status.

## Authoring prompts

Prompts that commission a whitepaper from a coding agent live in
[`prompts/`](prompts/). They are briefs, not whitepapers:
[`prompts/atlas-applikationen-whitepaper.md`](prompts/atlas-applikationen-whitepaper.md)
commissions the process applications whitepaper in the `pblumer/atlas-cloud`
design;
[`prompts/atlas-worker-whitepaper.md`](prompts/atlas-worker-whitepaper.md)
is the self-contained brief (facts A–O, for a model without repository access)
the worker architecture whitepaper was written from.

Keep all future whitepapers here, under `docs/whitepapers/`. The former
singular `docs/whitepaper/` directory has been consolidated into this
library; update references instead of reintroducing that directory.
