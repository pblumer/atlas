# ADR-0032: In-Modeler AI copilot over the MCP/HTTP surface

- **Status:** Proposed
- **Date:** 2026-07-23
- **Deciders:** Atlas maintainers

## Context and problem statement

The reference modeler has a **Copilot** affordance on the canvas: describe a
process in natural language and it generates or edits the diagram for you. It
lowers the "blank canvas" barrier and turns editing into a conversation.

Atlas already speaks to AI agents — ADR-0016 exposes the engine over MCP so an
external agent (Claude Desktop, Claude Code, a claude.ai connector) can deploy a
model, start an instance, and read runtime state. What ADR-0016 does *not* do is
put a generation assistant **inside the Modeler**, editing the canvas the author
is looking at. The question: **how does Atlas offer an in-editor copilot** without
(a) bundling a model/inference dependency into the single binary, (b) hardcoding a
specific AI provider, or (c) violating the buildless rule (ADR-0012)?

## Decision drivers

- **No inference in the binary.** Atlas is a workflow engine; it must not embed an
  LLM or a provider SDK. Generation happens *outside* Atlas.
- **Provider-neutral.** The copilot must not marry Atlas to one AI vendor; the
  same surface should serve any agent.
- **Reuse the interface we already exposed.** ADR-0016 already models "an AI agent
  manipulates Atlas" as MCP tools over the HTTP API. Diagram generation is more of
  the same, not a new integration style.
- **Author stays in control.** Generated XML is a *proposed* draft the author
  reviews, not an auto-deploy. It must pass the same compiler gate (ADR-0013) and
  Problems check (ADR-0026) as hand-drawn models.

## Considered options

1. **Embed an LLM / provider SDK** in the binary and call it from the Modeler.
2. **Extend the MCP/HTTP surface** with model-authoring tools (produce/patch BPMN
   XML, validate it) so *any* MCP-speaking agent can drive the canvas; the Modeler
   offers a copilot panel that talks to a user-configured agent endpoint and drops
   the returned XML into a reviewable draft (ADR-0021).
3. **No in-editor copilot** — authoring stays fully manual; agents drive Atlas
   only from outside via ADR-0016.

## Decision outcome

Chosen option: **Option 2 — extend the MCP/HTTP surface with authoring tools and
render results into a reviewable draft.** ADR-0016's tool set grows a
model-authoring group: return a process's XML, validate candidate XML (reusing
ADR-0026's dry-run compile), and accept generated/patched XML into a draft
(ADR-0021). The Modeler's copilot panel is a thin client over a **user-configured**
agent endpoint (their own Claude connector or local agent) — Atlas ships the
tools and the panel, not the model. Whatever the agent returns is loaded as a
*draft* the author reviews on the canvas and against the Problems panel before
deploying; nothing auto-runs.

Option 1 is rejected: embedding inference contradicts "Atlas is an engine, not an
AI product", bloats the binary, and picks a vendor. Option 3 leaves a real
authoring accelerator on the table when the MCP groundwork (ADR-0016) already
makes Option 2 cheap.

### Consequences

- **Positive:** No model/provider dependency in the binary; provider-neutral;
  reuses the ADR-0016 MCP surface and ADR-0021 drafts and ADR-0026 validation; the
  author reviews every generation through the normal compiler/Problems gate.
- **Negative / trade-offs accepted:** The copilot's quality depends on an external
  agent the user configures — Atlas can't guarantee good output, only that bad
  output is caught by the compiler before deploy. Round-tripping XML edits
  (patch vs. regenerate) is a non-trivial UX/format problem.
- **Follow-ups / risks to watch:** Define the authoring tool schema (generate vs.
  patch, how selection/context is passed); guard against a generated model
  silently overwriting unsaved work; the `/mcp` endpoint's unauthenticated caveat
  (ADR-0016) applies to any exposed authoring tools too.

## Links

- extends ADR-0016 (MCP server over the HTTP API) with authoring tools
- reuses ADR-0021 (drafts as the landing zone) and ADR-0026 (validation gate)
- constrained by ADR-0010/0012 (no heavy deps, buildless UI)
