# ADR-0568: An MCP server is a Worker, and one of its tools is a task

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-27
- **Deciders:** Atlas maintainers
- **Open question:** how servers in the field actually answer. The protocol was read from the
  specification's sources (revisions `2026-07-28` and `2025-11-25`) and exercised against
  servers written for the tests, not against a third party's server of either kind. In
  particular the rule that tells an older server from a current one — a 4xx whose body is
  none of the current revision's errors — is the specification's own, and how uniformly older
  servers behave that way is not known.
- **Question checked:** 2026-11

> **Implementation status (2026-11-27).** Built: the protocol client
> (`connector/mcptool/client.go`); the job type `io.atlas.mcp` and the task's
> `<atlas:mcpConnector>` (`compiler/mcp_compile.go`); the Worker record's list of permitted
> tools, its validation and the check where a job is resolved (`api/mcpworker.go`); the worker
> that makes the call (`connector/mcptool/job.go`, `worker/mcpconnector.go`), supervised by
> default; the Modeler's task and the Console's Worker form; the manifest, the Repository
> package and the setup instructions. Not built: reading a server's tool list at design time,
> the client-credentials grant, documents as results, and an example model. Nothing here has
> been run against a server other than the ones written for its tests.

## Context and problem statement

Atlas reaches another system through a Worker Type written for that system: one package under
`connector/`, in Go, in a release. Twenty-odd exist. Every system beyond them waits for the
next one.

Meanwhile the systems themselves have started to describe what can be done with them in one
common way. A Model Context Protocol server lists its tools — a name, a description, a JSON
Schema of the arguments — and runs one when asked. Teams build such servers for their
assistants, vendors ship them, and an organisation's own internal systems increasingly have
one because somebody wanted to ask it questions.

The comparison with Camunda 8 put this first among what Atlas lacks: there, an MCP server is
attached to an agent as a whole, and the agent sees whatever tools the server lists at that
moment.

The question is not whether Atlas should call MCP servers. It is **what a process is allowed
to reach when it does, and who decided that.**

## Decision drivers

- **What an agent may reach is the diagram**
  ([ADR-0253](0253-agent-tool-calls-drive-adhoc-activation.md)). A reviewer reads a model and
  sees the whole of what it can set in motion. A toolbox that grows when a server adds a tool
  is a reach nobody reviewed.
- **A server's words about itself are not trusted.** A tool's description and its annotations
  come from the server. The specification says so of annotations — "clients MUST consider tool
  annotations to be untrusted unless they come from trusted servers" — and a description put in
  front of a model is an instruction to it.
- **Every operation declares what a second delivery does**
  ([ADR-0538](0538-operations-declare-idempotency-and-effect.md)). An MCP tool's effect is
  known to whoever runs the system behind it, and to nobody else.
- **One configured credential opens many tools.** A Worker of any other type does a handful of
  things its code spells out. An MCP Worker does whatever its server offers.
- **No model call, and nothing slow, in the engine**
  ([ADR-0164](0164-no-in-process-service-tasks.md)).

## Considered options

1. **A task calls one named tool of a configured server; the server's permitted tools are
   listed on its Worker** (chosen).
2. **A server is attached to an agent as a toolbox**, its tools discovered when the agent
   runs — Camunda's shape.
3. **Both**: a task for a fixed call and a toolbox for an agent.
4. **Nothing in the engine**: an MCP server is reached by a model-authored job type and a
   worker somebody writes.

## Decision outcome

Chosen: **option 1**.

### The vocabulary

- **Worker Type `mcp`**: the ability to call a tool of an MCP server.
- **Worker**: one configured server — its endpoint, a reference to its credential in the
  vault, and **the tools a process may call on it**.
- **Task**: a service task naming a Worker, one tool and that tool's arguments.

A task of this kind placed at the root of an agent-driven ad-hoc subprocess is one of that
agent's tools, like any other activity there. Nothing is added for it: its name is its element
id, its description the modeller's `<bpmn:documentation>`, its parameters `<atlas:agentParam>`.
**The model never reads the server's description of the tool.**

### The list of permitted tools

A Worker names each tool a process may call, and for each one states the two properties
ADR-0538 asks of every operation, in that record's own words: `idempotency` (`natural`, `key`,
`check`, `none`) and `effect` (`reversible`, `irreversible`).

- **A tool not on the list is not called.** The list is empty until an operator fills it.
- **The operator states the effect, not the modeller.** Whoever configures the Worker knows the
  system; a modeller who could relabel a write as a read would.
- **The server's hints are not taken.** `readOnlyHint` and `idempotentHint` may be shown to the
  operator who fills the list in. They decide nothing.
- The check is made where a job is resolved for a worker, on the server, from the Worker's
  record — not by the worker that holds the credential. A tool that is not permitted travels
  as a refusal, and the worker makes no call; so do a Worker that does not exist and one that
  is switched off. Editing the list takes effect with the next job and restarts nothing.
- A list holds at most 200 tools, and a tool listed without its two properties is taken as
  `none` / `irreversible`.

What the list does not do: it does not stop a worker somebody runs with a configuration of
their own. Such a worker holds the credential and can call whatever the server offers. The
list governs what a *model* can make Atlas's worker do.

### What is deliberately not done

- **No pinning of a tool's definition.** Pinning guards against a server changing a description
  after it was approved. Atlas shows no model that description, so the change reaches nothing.
  A changed *argument schema* is the server's to enforce, and it answers a call that no longer
  fits with an error.
- **No request from the server is served.** The client declares no capability: it supplies no
  model's answer, no person's input and no roots.
- **No session is kept between jobs.** One job is one call. A tool that only makes sense after
  another call on the same connection — open a page, then click — is not what this is for.

### The protocol

The specification changed shape in its `2026-07-28` revision. Before it a client opened a
session with `initialize`; since it every request stands alone, with the negotiated facts in
`_meta` and in HTTP headers. Servers of both kinds exist.

The client speaks the current revision first and falls back as that revision's
backward-compatibility rule describes: a 4xx to a current request whose body is none of the
current errors (`-32020`, `-32021`, `-32022`) means a server of the older kind. That is learned
on `tools/list`, which changes nothing and has to be asked anyway, and remembered.

- **Current revision:** `tools/list` once per lifetime of the list (the server's `ttlMs`, else
  five minutes), because a client must mirror the arguments a tool marks `x-mcp-header` into
  `Mcp-Param-*` headers. A tool whose annotation cannot be honoured is not called. A
  `HeaderMismatch` re-reads the list and retries once.
- **Older revisions** (`2025-11-25`, `2025-06-18`, `2025-03-26`): `initialize`,
  `notifications/initialized`, the call with the session id and the agreed version, then the
  session is ended. A `ping` on the response stream is answered; any other request from the
  server is told the client does not do that.
- **An answer is JSON or an event stream**, and both are read.
- **Transport:** the remote one, over HTTP. A server started as a local process is not
  supported: a Worker record that names a command is a way to run that command on the host.

### What comes back

- `structuredContent` becomes a JSON variable; otherwise the result's text blocks, joined,
  become a string.
- Images, audio and resources are named in the outcome and not carried: Atlas has no way yet
  to put a document in front of a model.
- **A tool that ran and reported failure** (`isError`) throws the BPMN error `MCP_TOOL_ERROR`
  where the process catches that code, and fails the job where it does not — the rule every
  Worker Type follows for a coded failure. **The protocol or the transport failing** is a job
  failure, retried and then an incident. The specification draws the same line.

### The task

`<atlas:mcpConnector connector="…" tool="…" input='{…}' resultVariable="…" timeout="PT30S">`.
`connector` names the Worker, literally or as a FEEL expression. `input` is a JSON object of the
tool's arguments whose string values starting with `=` are FEEL, as on an Atlas report task.
`timeout` is an ISO-8601 duration between one second and one hour for the one call.

### Credentials

The specification standardises OAuth 2.1 and permits other arrangements "negotiated" between
the two sides. A fixed bearer token from the vault is such an arrangement and is what is built
first. The client-credentials grant is an official extension and the natural second step.
Calling as the person a process acts for belongs to
[ADR-0475](0475-identity-aware-worker-execution.md).

### Consequences

- **Positive:** any system with an MCP server is reachable without a Worker Type of its own;
  what a process and an agent may do with it is on the diagram and on the Worker, reviewed in
  both places; a server cannot instruct a model through its own description.
- **Negative / trade-offs accepted:**
  - **One task per tool.** An agent that should use twenty tools of a server needs twenty
    activities. Option 2 needs none.
  - **What a tool returns is still read by a model.** The list of permitted tools and the
    modeller's description guard what is *called*; a result is content from another system and
    reaches the agent as such.
  - **The worst case is declared for the type.** The manifest of `mcp` can state only
    `none` / `irreversible`, because the type does not know its tools; the truth is on each
    Worker's list.
  - **No stateful tools**, as above.
- **Follow-ups / risks to watch:** the open question above; reading a server's tool list at
  design time, to fill a Worker's list in and to propose a task with its parameters; the
  client-credentials grant; documents as results.

## Pros and cons of the options

### Option 1 — one tool, one task, a list on the Worker (chosen)
- Good: the reach is modelled and reviewed; fits the Worker model without a new concept; an
  agent gets MCP tools through the mechanism it already has.
- Bad: modelling effort grows with the number of tools.

### Option 2 — a server as an agent's toolbox
- Good: nothing to model per tool; new tools arrive by themselves.
- Bad: new tools arrive by themselves. The reach changes without a deployment, and the model
  reads descriptions the server wrote.

### Option 3 — both
- Good: each case its fitting shape.
- Bad: the second shape is option 2 with its cost; nothing is gained by having the first
  beside it.

### Option 4 — nothing in the engine
- Good: no code.
- Bad: every installation writes the same worker, and none of them declares what a tool does.

## Links

- applies [ADR-0253](0253-agent-tool-calls-drive-adhoc-activation.md): an agent's reach is the diagram
- applies [ADR-0538](0538-operations-declare-idempotency-and-effect.md) to tools the type does not know
- follows [ADR-0164](0164-no-in-process-service-tasks.md) and [ADR-0168](0168-connector-work-on-a-worker.md): the call is made on a worker
- follows [ADR-0117](0117-ai-agent-task.md) in writing the wire format by hand
- leaves calling as a person to [ADR-0475](0475-identity-aware-worker-execution.md)
- is unrelated to [ADR-0016](0016-mcp-server-over-http-api.md), Atlas's own MCP server
