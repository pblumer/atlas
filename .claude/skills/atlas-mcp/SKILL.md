---
name: atlas-mcp
description: >-
  Drive a running Atlas BPMN workflow engine over its Model Context Protocol
  (MCP) server. Use whenever an agent needs to deploy a BPMN 2.0 model, start a
  process instance, or inspect live runtime state (tokens, active instances,
  engine stats) of Atlas — the tools are named atlas_* (atlas_deploy,
  atlas_create_instance, atlas_process_runtime, ...). Read this before calling
  any atlas_* tool so you understand keys, the deploy→instance→inspect flow, and
  why a token can park on a service task.
---

# Working with Atlas over MCP

Atlas is a durable, high-throughput **BPMN 2.x workflow engine** in Go. Its MCP
server (`mcp/` package, [ADR-0016](../../../docs/adr/0016-mcp-server-over-http-api.md))
lets an agent deploy models, start instances, and read live state.

**The MCP server is a pure adapter.** It holds no engine state — every tool call
is translated into an HTTP request against a running Atlas server (`/api/v1/*`)
and the endpoint's JSON/XML body is returned to you verbatim. It therefore
cannot violate an engine invariant; it only ever makes HTTP calls. Do not expect
it to cache, transform, or reason about results — that is your job.

## How Atlas is reached

Two transports front the *same* tools (see `mcp/doc.go`):

- **Remote (Streamable HTTP)** — `atlas serve --addr :8080` mounts the transport
  at `/mcp`. A claude.ai custom connector or any remote MCP client points here.
- **Local (stdio)** — `atlas mcp --server http://localhost:8080` is a short-lived
  per-agent adapter an MCP client (Claude Desktop, Claude Code) spawns.

> ⚠️ **The `/mcp` endpoint performs no authentication.** Front it with a reverse
> proxy before exposing it publicly. Never assume the transport is a trust
> boundary.

In a Claude Code session where the `atlas` MCP server is configured, the tools
appear as `mcp__atlas__atlas_*` and are called directly — no `atlas serve`/`atlas mcp`
step is needed.

## The tools

All ten tools map one-to-one onto an Atlas HTTP endpoint.

| Tool | Args | Returns |
|------|------|---------|
| `atlas_info` | — | product + version, e.g. `{"product":"Atlas","version":"0.1.0-dev"}` |
| `atlas_deploy` | `xml` (string) | assigned `key`, `processId`, `version` |
| `atlas_list_processes` | — | every deployed definition: `key`, `processId`, `version`, `deployedAt` |
| `atlas_get_process_xml` | `key` (int) | the deployed BPMN XML (with generated diagram DI) |
| `atlas_create_instance` | `key` (int) | starts an instance, runs until the engine goes idle, returns live `stats` |
| `atlas_process_runtime` | `key` (int) | per-element token/visit counts for one definition |
| `atlas_list_instances` | — | all instances: state (`active`/`completed`/`terminated`), tokens, variables |
| `atlas_cancel_instance` | `key` (int) | terminates one running **instance**; returns `{instanceKey, state:"terminated", stats}` |
| `atlas_delete_process` | `key` (int) | deletes one **definition** (engine + disk); returns `{"deleted":true,"key":N}` |
| `atlas_stats` | — | engine-wide `activeProcessInstances`, `activeElementInstances` |

## The normal flow

1. **`atlas_deploy`** a BPMN 2.0 XML document. Atlas compiles and validates it;
   **only elements Atlas can execute are accepted** — an unsupported or malformed
   model is rejected with an error, not silently deployed. Deploy returns the
   integer **`key`** — this is the handle for every later call. Deploy is
   idempotent per content but each deploy of a changed model yields a new
   `version` (and key).
2. **`atlas_create_instance`** with that `key`. The engine runs the token until
   it goes idle, then returns current stats.
3. **`atlas_process_runtime`** (per definition) or **`atlas_list_instances`**
   (per instance) to see where tokens sit and which variables are set.

### Worked example (verified against Atlas 0.1.0-dev)

Deploy a minimal process — start → service task → end:

```xml
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0">
  <process id="order" isExecutable="true">
    <startEvent id="start"/>
    <serviceTask id="task">
      <extensionElements><zeebe:taskDefinition type="payment" retries="5"/></extensionElements>
    </serviceTask>
    <endEvent id="end"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="task"/>
    <sequenceFlow id="f2" sourceRef="task" targetRef="end"/>
  </process>
</definitions>
```

- `atlas_deploy` → `{"key":7,"processId":"order","version":1,...}`
- `atlas_create_instance {key: 7}` → `{"definitionKey":7,"stats":{...}}`
- `atlas_process_runtime {key: 7}` →
  `{"instances":1,"tokens":1,"elements":[{"elementId":"task","type":"ServiceTask","tokens":1,...}]}`

## Deleting and cleaning up

Two levels of deletion, and **order matters** — a definition will not delete
while any of its instances still run:

1. **`atlas_cancel_instance {key: <instanceKey>}`** — terminate a running
   instance. Pass the *large* instance key from `atlas_list_instances`, not the
   definition key. All tokens are discarded; the instance becomes `terminated`.
2. **`atlas_delete_process {key: <definitionKey>}`** — remove a deployed
   definition from the engine and disk. **Refused with a conflict error if the
   definition still has running instances** (`cannot delete: N running
   instance(s); cancel them first`). Cancel every active instance of that
   definition, then delete.

So to fully remove a definition and everything it spawned: `atlas_list_instances`
→ `atlas_cancel_instance` each active instance of that definition →
`atlas_delete_process`. This is exactly how you clean up a test model whose token
parked on an unworked service task. Deletion follows "durable before visible"
(ADR-0019/I2): the on-disk record is removed first, so an acknowledged deletion
never reappears on restart. `completed`/`terminated` instances are historical and
do not block deletion.

## Gotchas — read these before diagnosing "a hang"

- **A token parking on a service task is normal, not a bug.** A `serviceTask`
  needs an external job worker to complete its work. With no worker attached, the
  token sits on the task and the instance stays `active` forever (see the example
  above — the `payment` task holds one token). This is correct engine behavior.
  To see a process run to completion via MCP alone, deploy a model whose path is
  automatic (e.g. start → gateway → end, no external tasks).
- **Keys are integers.** Definition keys are small (`7`); instance keys are large
  (`281474976710744`). Pass the *definition* key to the `key`-taking tools.
- **Deploy generates diagram layout.** `atlas_get_process_xml` returns your model
  plus an auto-generated `<bpmndi:BPMNDiagram>` block even if you deployed none —
  don't treat the extra DI as corruption.
- **Instance state vocabulary:** `active` (tokens still moving/parked),
  `completed` (reached an end state), `terminated` (cancelled). `atlas_stats` and
  the `stats` in a create-instance reply count only *active* instances/tokens.
- **The server is stateless per call.** There is no session or transaction across
  tool calls; each call is an independent HTTP request. Re-list to get fresh
  state; never assume a cached key is still the latest version.

## When NOT to use these tools

- Authoring/validating BPMN offline, or engine internals (compiler, processor,
  WAL, state) — that is source-code work; read `AGENTS.md` and the docs, don't
  poke the running server.
- DMN/FEEL decisions live in a *different* engine (temis), not Atlas. Boxed
  logic and decision tables are not Atlas MCP tools.

## References

- `mcp/doc.go` — package overview, transports, how to run it.
- `mcp/tools.go` — the authoritative tool list and argument schemas.
- [ADR-0016](../../../docs/adr/0016-mcp-server-over-http-api.md) — why MCP is an
  adapter over the HTTP API rather than an engine embedding.
- `docs/ARCHITECTURE.md` and `AGENTS.md` — the engine itself and its invariants.
