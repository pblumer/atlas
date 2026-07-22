# ADR-0016: Model Context Protocol server as a stdio adapter over the HTTP API

- **Status:** Accepted
- **Date:** 2026-07-22
- **Deciders:** Atlas maintainers

## Context and problem statement

Atlas now runs as a single binary with an HTTP API and a web UI (ADR-0011): a
human can deploy a BPMN model, start an instance, and watch tokens move. The
next audience is an *AI agent*. An agent that could deploy a process, start
instances, and read live runtime state would let users drive Atlas in natural
language and let automated workflows treat Atlas as a tool.

The [Model Context Protocol](https://modelcontextprotocol.io) (MCP) is the
emerging standard for exposing tools to AI agents (Claude Desktop, Claude Code,
and others). The question is not *whether* to speak MCP but *where the MCP
server sits relative to the engine*, and *how much machinery to pull in* to
speak the protocol.

The hard constraint is invariant I3, the single-writer partition model
(ADR-0002): exactly one goroutine may touch a partition's processor and state.
The api package already solves this behind its HTTP surface by funnelling every
processor access through one run-loop goroutine. Any MCP surface must not
reopen that problem.

## Decision drivers

- **Preserve the single-writer invariant with zero new risk.** The engine's
  correctness must not depend on getting concurrency right a second time in a
  second place.
- **Match the repository's dependency posture.** Atlas hand-writes its binary
  codec and avoids heavy SDKs (ADR-0009, ADR-0010). A protocol as small as MCP's
  tools surface should not be an exception.
- **Meet agents where they are.** MCP clients spawn a per-agent server process
  and speak JSON-RPC on its stdin/stdout. The stdio transport is the common
  denominator.
- **An agent should see what a human sees.** Deployments and instances an agent
  creates should be the same ones visible in the web UI, not a private, parallel
  world.

## Considered options

1. **Embed the engine in the MCP process.** `atlas mcp` opens its own
   `wal`/`state`/`engine` and serves MCP directly against it.
2. **Adopt an MCP SDK** (e.g. a Go MCP library) and wire it to either an
   embedded engine or the HTTP API.
3. **Hand-write a stdio MCP server that proxies to the HTTP API.** `atlas mcp`
   is a thin JSON-RPC-over-stdio adapter whose every tool call becomes an HTTP
   request to a running `atlas serve`.

## Decision outcome

Chosen option: **"3 — a hand-written stdio MCP server that proxies to the HTTP
API"**, because it is the only option that touches no engine internals at all.
The MCP server holds no partition state, starts no processor, and calls no
processor method — it makes HTTP calls. The single-writer invariant is therefore
enforced in exactly one place (the api run loop) and cannot be violated by the
new surface, whatever bugs it might have.

The protocol is implemented by hand in the `mcp` package. A tools-only MCP
server needs just four methods — `initialize`, `tools/list`, `tools/call`,
`ping` — plus notification handling and JSON-RPC 2.0 framing (newline-delimited
JSON on stdio). That is a few hundred lines with no new dependency, consistent
with how the rest of Atlas is built.

Eight tools map one-to-one onto existing endpoints: `atlas_info`,
`atlas_deploy`, `atlas_list_processes`, `atlas_get_process_xml`,
`atlas_process_runtime`, `atlas_create_instance`, `atlas_list_instances`,
`atlas_stats`. Each returns the endpoint's own JSON (or XML) body as the tool's
text content, so a model receives the server's structured response verbatim.

### Consequences

- **Positive:** The engine invariants are untouched; the adapter is
  unprivileged by construction. No new dependency. An agent and a human share
  one server, so they see the same deployments and instances. The adapter is
  trivially testable against an `httptest` server backed by a real engine.
- **Negative / trade-offs accepted:** Two processes are required —
  `atlas serve` and `atlas mcp` — which is mildly against the single-binary
  ethos, though it is exactly how MCP clients expect to spawn a server. Every
  tool call pays one loopback HTTP round-trip; negligible for this control-plane
  traffic. The MCP surface is only as capable as the HTTP API, so it inherits
  that API's current limits (in-memory deployments, no job-worker surface yet).
- **Follow-ups / risks to watch:** As the HTTP API grows (durable deployments,
  message publication, job completion, queries — Milestone 4), add matching
  tools. If an embedded, engine-local MCP server is ever wanted (e.g. an
  offline single-process demo), it can reuse the same `mcp` protocol code with a
  different backing client; the protocol layer is independent of the transport
  to the engine.

## Pros and cons of the options

### Option 1 — embed the engine in the MCP process
- Good: one process; no HTTP hop.
- Bad: re-solves the single-writer concurrency problem in a second place;
  gives the agent a private engine that diverges from what the web UI shows;
  more surface for an invariant bug.

### Option 2 — adopt an MCP SDK
- Good: less protocol code to own; tracks spec changes upstream.
- Bad: a heavy dependency for a tiny surface, against ADR-0010's posture; ties
  Atlas's release cadence to the SDK's; the protocol we need is small enough to
  own outright.

### Option 3 — hand-written stdio proxy (chosen)
- Good: zero engine coupling; no new dependency; shared state with the UI;
  easy to test.
- Bad: requires a running `atlas serve`; one HTTP round-trip per call.

## Links

- relates to ADR-0011 (single-binary distribution and web UI) — the HTTP API this adapts
- relates to ADR-0002 (single-writer partition model) — the invariant this design protects
- relates to ADR-0007 (job worker protocol) — future job tools depend on it
