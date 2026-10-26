# Whitepapers

Architecture and product concept documents for atlas. Whitepapers explain
motivation, design choices and intended architecture; they are **not**
acceptance evidence for ADRs, implemented capabilities, security guarantees
or production readiness. Check the linked ADR and programme before quoting
implementation status.

## Library

- [Identity-Aware Workflow Execution](identity-aware-workflow-execution.md) —
  user/service identities, delegated execution, policy decision/enforcement,
  obligations and auditable actions. German Markdown edition,
  **23 October 2026**. Related:
  [IAE delivery programme](../planning/identity-aware-execution-program.md) and
  [ADR-0475](../adr/0475-identity-aware-worker-execution.md).
- **Horizontal Scaling** —
  [PDF](atlas-technisches-whitepaper-horizontale-skalierung.pdf) and
  [editable Word document](atlas-technisches-whitepaper-horizontale-skalierung.docx).
  Architecture concept for scaling the durable BPMN workflow engine.
  See [roadmap](../../ROADMAP.md) and the relevant
  [architecture decisions](../adr/README.md) for current implementation status.

Keep all future whitepapers here, under `docs/whitepapers/`. The former
singular `docs/whitepaper/` directory has been consolidated into this
library; update references instead of reintroducing that directory.
