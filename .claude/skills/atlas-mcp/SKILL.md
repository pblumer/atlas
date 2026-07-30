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
| `atlas_create_instance` | `key` | starts an instance and returns stats |
| `atlas_process_runtime` | `key` | per-element token and visit counts |
| `atlas_list_instances` | none | bounded legacy instance list |
| `atlas_cancel_instance` | `key` | terminates one active instance |
| `atlas_cancel_instances` | `key`, `limit?` | bounded bulk termination |
| `atlas_delete_process` | `key` | deletes one deployed definition |
| `atlas_stats` | none | engine-wide active counts |

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
