---
name: atlas-mcp
description: >-
  Drive a running Atlas BPMN workflow engine over MCP. Use for atlas_* tools
  that manage design-time projects, deploy BPMN, start and inspect instances,
  paginate user tasks, complete human tasks, and clean up test resources.
---

# Working with Atlas over MCP

Atlas is a durable BPMN 2.x workflow engine. Its MCP server is a thin adapter
around the public Atlas HTTP API (ADR-0016): it owns no processor or partition
state and never bypasses the API run loop.

## Transports

The same tool registry and dispatch path serve both transports:

- Streamable HTTP: `atlas serve --addr :8080` mounts `/mcp`.
- stdio: `atlas mcp --server http://localhost:8080`.

The `/mcp` transport is not an authentication boundary. Put a publicly reachable
endpoint behind an authenticating reverse proxy.

## Runtime tools

| Tool | Arguments | Result |
|---|---|---|
| `atlas_info` | none | product and build metadata |
| `atlas_deploy` | `xml` | deployed definitions and keys |
| `atlas_list_processes` | none | deployed definitions |
| `atlas_get_process_xml` | `key` | deployed BPMN XML |
| `atlas_create_instance` | `key`, `variables?` | starts an instance (optionally seeded with start variables) and returns stats |
| `atlas_process_runtime` | `key` | per-element token and visit counts |
| `atlas_list_instances` | none | bounded legacy instance list |
| `atlas_instance_variables` | `key` | one instance's variables as a typed object |
| `atlas_instance_jobs` | `key` | one instance's activatable jobs (key, element, type) |
| `atlas_instance_timeline` | `key` | one instance's step-by-step replay timeline |
| `atlas_cancel_instance` | `key` | terminates one active instance |
| `atlas_cancel_instances` | `key`, `limit?` | bounded bulk termination |
| `atlas_delete_process` | `key` | deletes one deployed definition |
| `atlas_stats` | none | engine-wide active counts |
| `atlas_publish_message` | `name`, `correlationKey?`, `variables?` | correlates a message to waiting instances; returns stats |
| `atlas_complete_job` | `key`, `variables?` | completes a job by hand (operator counterpart to a worker) |
| `atlas_fail_job` | `key`, `retries?`, `message?` | fails a job; `retries` 0 (default) raises an incident, positive re-activates |
| `atlas_list_incidents` | `limit?` | `{incidents, truncated}` — the operator "what's stuck" view |
| `atlas_resolve_incident` | `key`, `retries?` | resolves an incident by elementInstanceKey and retries its job |

## Authoring and human-task tools

| Tool | Arguments | Result |
|---|---|---|
| `atlas_create_project` | `name` | project id and metadata |
| `atlas_list_projects` | none | visible projects |
| `atlas_delete_project` | `id` | `{deleted:true,id}` |
| `atlas_save_draft` | `xml`, `projectId?` | saved BPMN draft |
| `atlas_save_form` | `id`, `schema`, `name?`, `projectId?` | saved form-js form |
| `atlas_upload_decision_model` | `handle`, `xml` | stored DMN model |
| `atlas_register_decision` | `name`, `modelRef`, `projectId?` | registered decision reference |
| `atlas_deploy_project` | `id` | deployed project definitions |
| `atlas_list_tasks` | `limit?`, `before?`, `processInstance?` | task page envelope |
| `atlas_complete_task` | `key`, `variables?` | completed user task |

A typical authoring flow is:

`atlas_create_project` -> save forms/decision/draft ->
`atlas_deploy_project` -> `atlas_create_instance` ->
`atlas_list_tasks` -> `atlas_complete_task`.

## Make the diagram readable, not just valid

`atlas_save_draft` / `atlas_deploy` accept a model with no `<bpmndi:BPMNDiagram>`
and auto-generate a layout. It always deploys, but the auto-layout routinely
stacks branch and bypass edges on top of the main-axis nodes — fine for a
throwaway probe, not for anything a human opens in Operations or the Modeler.

For any model someone will look at, **author the BPMN-DI by hand**: one straight
horizontal main axis at a constant `y`, even node pitch (≈150px; 100×80 tasks,
50×50 gateways, 36×36 events), every branch/bypass on its own lane with clean
orthogonal waypoints, and gateway-flow labels placed so they don't collide.
Verify the render (Operations view or a preview), not just that the deploy
succeeded. See `AGENTS.md` → "Authoring BPMN models" and
`examples/onboarding/onboarding.bpmn` for a worked layout.

## Task pagination

`atlas_list_tasks` returns:

```json
{
  "items": [],
  "truncated": true,
  "nextCursor": 281474976710744
}
```

The global task list is newest-first. When `truncated` is true and
`nextCursor` is present, pass that value as `before` to fetch the next older
page. `limit` defaults to 500 and is capped by the HTTP API at 5000.

`processInstance` restricts the lookup to one instance. A scoped lookup may be
truncated but has no continuation cursor. Do not invent one.

## Project deletion is not process deletion

`atlas_delete_project {id}` deletes only the design-time grouping folder. It is
idempotent. Drafts and decision references remain and become ungrouped.
Deployed definitions and process instances are unaffected. With API
authentication enabled, the caller must satisfy the project owner rule.

To delete a deployed process definition, first terminate every active instance
of that definition and then call `atlas_delete_process`. The API rejects process
deletion while active instances remain.

## Keys and execution behaviour

- Definition keys identify deployed process versions.
- Instance keys identify one process execution and are usually much larger.
- Task keys identify user-task jobs and are accepted by `atlas_complete_task`.
- A token parked on a service task is normal when no worker completes its job.
- Deploy-time compilation rejects unsupported or malformed BPMN rather than
  interpreting it dynamically at runtime.
- Each tool call is independent. Re-list state instead of relying on cached
  results.

## Boundaries

Use these tools to operate a running Atlas server. Do not use them to modify
processor, WAL, state, compiler, FEEL, DMN, or recovery internals. Source-code
changes must follow `AGENTS.md`, the architecture documents, and the repository
Definition of Done.

## References

- `mcp/tools.go` and `mcp/tools_authoring.go`: authoritative tool schemas
- `mcp/doc.go`: package and transport overview
- `docs/adr/0016-mcp-server-over-http-api.md`: MCP adapter decision
- `docs/ARCHITECTURE.md` and `AGENTS.md`: engine invariants and development rules
