# ADR-0117: An AI agent task — an LLM agent as a managed connector on the job path

- **Status:** Proposed
- **Date:** 2026-08-12
- **Deciders:** Atlas engine team

## Context and problem statement

Users want a process to be able to hand a step to an **AI agent**: give a
large-language-model agent some instructions and the instance's data, let it
reason (and optionally call a bounded set of tools), and write its result back
into process variables so the flow continues — a downstream gateway routing on
the outcome, a user task reviewing it, and so on. This is the runtime
counterpart to the *authoring* copilot of [ADR-0032](0032-modeler-ai-copilot.md):
0032 puts an agent **beside** the modeler to draft diagrams; this ADR puts an
agent **inside** a running instance as an executable step.

An LLM call is the hardest possible fit for this engine's core invariant. It is:

- **non-deterministic** — the same prompt yields different output across calls;
- **a side effect** — a network round-trip to an external service;
- **slow and failure-prone** — seconds to minutes, rate limits, timeouts;
- **opaque** — the reasoning and tool calls are invisible unless captured.

The question is not *whether* Atlas can run such a step — the job path already
runs DMN decisions, REST calls, e-mail sends and polyglot scripts, all of which
are side-effecting and (REST/script) non-deterministic. The question is
**what shape** an AI agent task takes so that it inherits those guarantees
rather than inventing a parallel mechanism, and how its non-determinism is
quarantined from the event log so recovery stays exact (I4/I6).

## Decision drivers

- **Honor `applyToState` (I4) and durable-before-visible (I2/ADR-0005).** The
  LLM call must run in the post-fsync side-effect phase, on a worker goroutine,
  never on the processor and never in `applyToState`. Its *result* must be
  frozen into the completion event and re-applied verbatim on recovery — the
  agent is never re-invoked on replay. This is the exact rule
  [ADR-0047](0047-polyglot-script-tasks-via-job-workers.md) wrote for the
  script interpreter; "interpreter" reads "LLM" here.
- **No inference in the binary; provider-neutral.** Atlas is a workflow engine,
  not an AI product. It must not embed an LLM or a provider SDK, and must not
  marry itself to one vendor. This is the same stance
  [ADR-0032](0032-modeler-ai-copilot.md) took for the copilot.
- **Reuse the seam, don't add one.** The engine already has a reserved-job-type
  worker pattern (ADR-0007), a managed-connector registry with operator-set
  endpoints and vault-resolved credentials
  ([ADR-0041](0041-connector-management-and-secret-store.md)/
  [ADR-0069](0069-engine-internal-encrypted-secret-vault.md)), a service-task
  connector catalog for authoring ([ADR-0067](0067-service-task-connector-catalog.md)),
  and I/O variable mappings for data flow ([ADR-0068](0068-task-io-variable-mappings.md)).
  Adding a kind should be one entry per layer, not a new subsystem.
- **Secrets never live in a model.** An API key is a credential; a BPMN file is
  shared, versioned and rendered. The model references a server-registered
  credential by name, resolved from the vault at run time.
- **Auditability is not optional.** An agent run is opaque by nature. Operators
  and auditors must be able to see, after the fact, what prompt the agent saw,
  which model answered, which tools it called, and what it produced —
  reproducibly from the log.

## Considered options

**A. Inline in a behavior (like the FEEL script task).** Call the LLM on the
processor goroutine during command processing. **Rejected outright.** It reads
the clock and the network inside the single-writer loop, blocks the partition
for seconds, allocates on the hot path (I1), and — fatally — a replay would
re-run the call and diverge (I4/I6). ADR-0047 already rejected exactly this for
scripts; the rejection is stronger here because the output is non-deterministic
by design.

**B. A new first-class element type with its own reserved job type**
(`TypeAgentTask`, mirroring `TypeScriptJobTask` / ADR-0047). A dedicated
element, behavior, detail struct and in-process worker on a reserved global job
index. Clean, but it treats "an LLM endpoint + an API key" as engine-special
when it is precisely the shape of a *managed connector*, and it duplicates the
connector authoring/credential/registry machinery instead of reusing it.

**C. A managed-connector kind on a service task** — an
`<atlas:agentConnector>` extension, one entry in the `managedConnectorKinds`
registry, one worker keyed on a reserved job type, authored through the
existing connector catalog (ADR-0067) with its endpoint and API-key credential
managed by an operator in the Console (ADR-0041) and resolved from the vault
(ADR-0069). The task is a `TypeConnectorTask` reusing `serviceTaskBehavior`, so
I/O mappings, boundary timeouts, retry/backoff, incidents, idempotency and
recovery are inherited unchanged.

## Decision outcome

Chosen: **Option C — an AI agent task is a managed-connector kind on the job
path.** An LLM agent endpoint plus its API key is a managed connector in exactly
the sense of `temis` (a remote reasoning service, ADR-0050) and `mail`: an
operator-configured endpoint, a vault-resolved credential, one worker serving
every deployed process. Modelling it as connector kind rather than a bespoke
element (Option B) is what keeps "no provider in the binary" honest and makes
the next agent provider a data entry rather than a code fork.

### What the model carries (deploy-time, interned — I5)

An `<atlas:agentConnector>` extension compiled into an `AgentTaskDetail`:

- **`connector`** — the name of a server-registered agent connector (endpoint +
  credential ref). Never a raw URL-with-key; the credential is resolved from the
  vault at run time (ADR-0041/0069), never stored in the model.
- **`prompt` / `instructions`** — literal **or a FEEL expression** over the
  instance's variables (the REST-connector field precedent, ADR-0067). A
  system-prompt field may be constant.
- **`tools`** — an explicit allow-list of tool/function names the agent may
  call. Empty means a pure text/reasoning step. (See *Tool calls* below — the
  first slice ships with an empty catalog.)
- **Input mappings** (`zeebe:ioMapping` inputs) — the context the agent sees,
  resolved over the scope chain into the activity-local scope (ADR-0068).
- **`resultVariable`** and an optional **output schema** — the agent's answer is
  written here on completion via the output-carrying job completion
  (ADR-0066); a declared schema lets the worker validate/coerce the structured
  output so a downstream gateway can route on it deterministically.
- **Limits** — `maxIterations`, `timeout`, `tokenBudget` — bounding cost and
  latency; exceeding a limit fails the job (below), it does not silently
  truncate.

### How it executes (side-effect phase, off the hot path — I2/I4)

Identical to every other connector. `serviceTaskBehavior.OnActivated`
(`engine/behavior.go`) creates a job carrying the reserved `AgentJobTypeIndex`
and calls `NotifyJobAvailable` (a post-fsync side effect). The in-process agent
worker — a new `agent/` package modelled on `script/worker.go` and the
`clio`/`mail`/`remedy` workers, implementing `job.OutputHandler` — picks the job
up **after fsync**, reads the instance variables via `scopeVars`, **makes the
LLM call here**, runs the bounded agentic loop, and returns the result as output
variables on `CompleteJob`. `handleJobCompleted` freezes those outputs into
`VariableCreated` events; on replay `applyToState` re-applies the stored values
and the agent is never called again (I6). A handler error routes to `FailJob` —
so a timeout or rate limit is a retry, and exhausted retries raise an incident
(ADR-0061/0111), with no special-casing.

### Auditability — a durable agent-run record

Mirroring the DMN decision-evaluation record ([ADR-0066](0066-decision-evaluation-records.md)):
the worker rides the prompt actually sent, the model/version that answered, the
ordered tool calls it made, the final output, and token usage back on the job
completion, and the processor freezes it into a durable, append-only
`VTAgentRun` history record keyed under the instance in the ADR-0048
`(scope, ts, pos)` shape. It rebuilds from the log on replay without re-running
the agent, is served over `GET /api/v1/instances/{key}/agent-runs`, and is
surfaced in Operations on the agent task — live and long after the instance
finished. This is the answer to "an agent run is opaque": the trail is a
first-class, recoverable fact, not a log line.

### Authoring

One catalog entry `{id, name, description, icon, extension, fields[]}` in the
service-task connector catalog (ADR-0067) plus the moddle type that preserves
`<atlas:agentConnector>` on save. The compiler keeps discriminating by extension
element / reserved job type. The operator registers the agent connector
(endpoint + credential ref) on Console → Connectors (ADR-0041); the key lives in
the vault (ADR-0069).

### Consequences

- **Positive:** the hardest-to-fit step in the engine inherits the whole job
  path — recovery, retry/backoff, incidents, boundary timeouts, I/O and data
  mappings, idempotency (job key), multi-instance — for the cost of one entry
  per layer. Non-determinism is quarantined outside the log by construction
  (frozen result, re-applied on replay). No provider or inference dependency
  enters the binary; the next agent vendor is a connector registration, not a
  fork. Agent runs are auditable and reproducible.
- **Negative / trade-offs accepted:** the synchronous in-process worker holds a
  job for the duration of an LLM call; long agent runs lean on the incident/
  backoff model and, for hard deadlines, a boundary timer (or an event-based
  gateway racing the agent against a timeout, ADR-0110) — the leased,
  off-loop streaming worker is the Milestone-4 gRPC concern (ADR-0007), the same
  deferral DMN and REST already accept. Output quality is the external agent's,
  not Atlas's; a declared output schema is the only guarantee the engine
  offers, and an un-schema'd agent is a text generator a gateway cannot decide
  on. Cost/latency are real and bounded only by the model-authored limits.
- **Follow-ups / risks to watch:** the **tool catalog** — whether, and how, the
  agent may call back into Atlas (start a subprocess, read variables, ask a
  human). The first slice ships an **empty** catalog: the agent is a black box,
  input variables → output variables, and human-in-the-loop is modelled as a
  real BPMN `userTask` beside the agent task, never hidden inside it. Also:
  streaming/partial results, structured-output schema validation depth, prompt/
  output size limits versus the variable store, token-usage accounting for
  quotas, and redaction of secrets from the audit record.

## Pros and cons of the options

### Option A — inline in a behavior
- Good: no worker; simplest wiring.
- Bad: violates I1/I2/I4/I6 — clock and network on the single writer, blocks the
  partition, and a replay re-invokes the model and diverges. Non-viable.

### Option B — bespoke first-class element type
- Good: a clean, self-contained element; the ADR-0047 precedent maps directly.
- Bad: treats an endpoint-plus-credential as engine-special when it is a managed
  connector; duplicates the connector authoring, registry and vault machinery;
  makes "provider-neutral, nothing in the binary" harder to hold.

### Option C — managed-connector kind (chosen)
- Good: reuses the connector registry, catalog, credential/vault path and job
  seam wholesale; provider-neutral by construction; the next agent is a data
  entry.
- Bad: constrained to the connector authoring shape (acceptable — that shape is
  exactly right for an endpoint + key); still inherits the synchronous-worker
  latency trade-off pending the gRPC worker protocol.

## Links

- runtime counterpart to ADR-0032 (in-modeler AI copilot — no provider in the binary)
- follows ADR-0047 (polyglot script tasks — result frozen, never re-run on replay) for the determinism contract
- built on ADR-0007 (job-worker protocol), ADR-0067 (service-task connector catalog), ADR-0041 (connector management & secret store), ADR-0069 (encrypted secret vault)
- data flow via ADR-0068 (task I/O variable mappings); output-carrying completion via ADR-0066 (decision evaluation records), whose audit-record pattern the agent-run record mirrors
- failure handling via ADR-0061 / ADR-0111 (incident model); hard deadlines via ADR-0110 (event-based gateways)
- honors I1/I2/I4/I5/I6 and ADR-0005 (durable before visible), ADR-0001 (one `applyToState`)
