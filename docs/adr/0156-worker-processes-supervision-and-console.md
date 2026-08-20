# ADR-0156: Every side-effecting task on a worker process — `atlas worker`, optional supervision, and a Workers console

- **Status:** Proposed
- **Date:** 2026-08-20
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0155](0155-in-process-vs-out-of-process-service-tasks.md) established what the
four execution seams cost and recommended an authoring rule, but it left the
architecture in an awkward place: the mode that is safe for the engine
(out-of-process) is the one nobody can use, and 17 of the 18 reserved job types run
their side effects on the single writer. That record's answer was a convention plus
two deferred prerequisites.

The follow-up question is whether the convention is the right shape at all: should
**an operating-system process outside the engine be responsible for every task**,
should **Atlas start and supervise those processes itself**, and should there be a
**Workers view** — who is subscribed, who holds which jobs, how many each has
processed, its logs, a restart button?

This matters because it changes ADR-0155's rejection of "ban in-process
connectors". That option was rejected because it "destroys the single-binary
experience (ADR-0011); every trivial installation pays a distributed-system tax for
a mail send to a local relay". **If Atlas launches the worker itself, from the same
binary, that objection largely dissolves** — the operator still installs one
artifact and runs one command. So the option deserves re-deciding rather than
re-citing.

### What already exists to build on

- **Atlas already spawns operating-system processes.** `connector/script`'s
  `CmdExec.Run` starts `pwsh` / `python3` / `node` per job, passing source and
  variables through the environment and killing the process at a deadline. Process
  management is not a new capability, only a new scale.
- **A job already records its holder.** `JobValue.Assignee` names who leased it and
  `LeaseExpiresAt` when the claim runs out (ADR-0007's amendment), with a timer
  releasing the hold. That is half of a worker registry.
- **The events for counters exist.** `JobActivated` and `JobTimedOut` are emitted;
  ADR-0007 lists their counters as still-open item 4.
- **The binary already has subcommands.** `serve`, `mcp`, `reset-password`,
  `import-mim` (`cmd/atlas/main.go`), so `atlas worker` is an ordinary addition.
- **A worker can be authenticated like the MCP adapter** (ADR-0049), and the API it
  would call is already described by an OpenAPI document (ADR-0043).

### What blocks all of it

Unchanged from ADR-0007's amendment: job-type indices are interned **per compiled
process** while the activatable index is global, so a type-keyed pull would hand a
worker another process's jobs. Until a global job-type registry exists, a worker can
lease only by key. No amount of supervision or UI changes that.

## Decision drivers

- **Availability of the single writer (I3)** is the reason this is being asked at
  all; any answer that leaves arbitrary I/O on `Loop`'s goroutine has not answered it.
- **One protocol, not two.** A worker Atlas launched and a worker an operator
  deployed must be the same program speaking the same API, or the two paths drift
  and only one gets tested.
- **ADR-0011's single binary.** An installation that wants "it just works" must not
  have to assemble a distributed system, and one that has Kubernetes must not have
  Atlas's supervisor fighting it.
- **Spawning is an attack surface.** Anything that turns operator input into a
  command line is remote code execution wearing a feature's clothes.
- **Observability before automation.** A restart button on a worker nobody can see
  the state of is a worse product than a view with no buttons at all.
- **Few dependencies (ADR-0010),** which is what decides the transport question below.

## Considered options

**For where the work runs:**

1. **Goroutine pool inside the engine process** — ADR-0149's option 3, ADR-0155's
   stated prerequisite. Handlers move off `Loop`'s goroutine, everything else stays.
2. **A separate worker process per subscribed kind, deployed by the operator** —
   ADR-0007 finished as designed.
3. **Option 2, plus Atlas as a supervisor** that launches, restarts and observes
   those processes.
4. **A process per job**, like the script connector does today, generalized to
   every kind.

**For the transport:**

5. **HTTP long-poll** over the existing API and OpenAPI document.
6. **A gRPC service**, as ADR-0007 originally specified before its amendment.

## Decision outcome

Chosen: **option 3, reached through option 2, with option 1 as the in-process
fallback that stays supported; option 4 rejected as a general model; and the
transport stays HTTP (option 5).** Concretely, in six parts.

### 1. "Every task" means every *side-effecting* task — not every task

Moving all task execution out of the engine would be wrong, and the boundary is the
one ADR-0047 already drew. A FEEL script task is pure, deterministic,
side-effect-free and compiled at deploy time; it completes in microseconds inside
the batch that activated it. Handing that to another process would cost a job, a
durable event, an IPC round trip and a scheduling delay — several thousand times the
work — to isolate something that cannot block, cannot fail slowly, and cannot escape.
The same holds for the mockup task (a timer) and for a local DMN evaluation, which
is CPU-bounded library code with no network.

The rule is therefore **I/O and foreign code leave; pure computation stays**. That
covers the connector kinds (REST, mail, SharePoint, Remedy, clio, temis, web scrape,
SCIM, LDAP), CSV import, the user-provisioning connector, and the polyglot script
languages — everything in ADR-0155's table that charges the engine for someone else's
latency.

### 2. A worker is a long-lived process, not a process per job

Option 4 is what the script connector does today, and it is right *there*: an
interpreter is a fresh sandbox per script and the cost is already paid by the
language. As a general model it is wrong — process startup per job destroys
throughput, forfeits connection reuse (an LDAP bind, an OAuth2 token cache, an HTTP
keep-alive), and makes a mail burst N process spawns. A worker starts once,
subscribes to the kinds it serves, and pulls many jobs. Script tasks keep spawning an
interpreter *inside* their worker, which is the correct nesting.

### 3. The worker is `atlas worker` — the same binary, the same handlers

The standard worker is a mode of the existing binary: `atlas worker --server=…
--kinds=mail,rest`. It runs the *existing* connector handler code, unchanged, with
the job source swapped — pulling over HTTP instead of from the in-process `Runner`,
and reporting completion as a command instead of a direct call. This is what makes
the whole change affordable: no connector is rewritten, and there is exactly one
implementation of "what a mail job does".

It also keeps ADR-0011 intact. One binary is still installed; whether it runs as one
process or four is a flag, not a different product. And a customer-written worker in
any language remains a first-class option — the API is the contract, not the binary.

### 4. Supervision is a convenience, never an execution mode

Atlas may launch and restart worker processes, and this must not become a third way
that work executes. The supervised worker and the operator-deployed worker are the
same program on the same protocol; supervision only answers *who ran the command*.
If that separation is not held, the supervised path becomes the tested one and the
external path rots — which is precisely how out-of-process ended up second-class today.

Three constraints on the supervisor, and the first is not negotiable:

- **It spawns `os.Executable()` and nothing else**, with an argv the server
  constructs from typed configuration (kinds, concurrency, server URL). No operator
  string ever becomes a command, an argument, or a shell fragment, from any API,
  form or connector record. A supervisor that runs configured command lines is a
  remote-code-execution endpoint, and no amount of authorization makes that a good
  trade in a workflow engine that already executes model-authored steps.
- **It is off by default and declines to fight a platform.** Under systemd or
  Kubernetes the operator owns process lifecycle; Atlas's supervisor is for the
  single-node installation that has no such thing.
- **Restart is bounded and backs off**, and a worker that fails to start repeatedly
  is reported, not retried forever.

### 5. The Workers console comes *before* supervision, and works without it

This is the part with the best ratio of value to risk, and it is independent of who
started the process. Today an external worker is invisible: an operator sees jobs
not moving and has no way to tell whether a worker is absent, wedged, or failing.
The view is what makes out-of-process operable at all — so it is built first, and
supervision is layered on later.

It needs a **worker registry**: a worker announces itself (id, kinds, concurrency,
version), heartbeats, and is aged out when it stops. Against that, per worker:
subscribed kinds; jobs currently leased (already derivable from `Assignee` /
`LeaseExpiresAt`); completed, failed and timed-out counters (ADR-0007's open item 4,
which ADR-0142 deferred for want of exactly these); last seen; and the incidents its
failures raised.

The registry is **runtime state, not engine state**: it is derived, it may be lost on
restart without harm, and it must never be written into the durable record or
reconstructed by `applyToState` (I4/I6) — the same discipline the mail outbox follows
(ADR-0150).

**Logs and restart are honestly asymmetric.** Atlas can capture stdout/stderr and
restart a process it launched; for a worker running in someone else's cluster it can
show heartbeat, leases and counters, and nothing more. The console must show that
difference rather than hide it: a supervised worker gets a log tail and a restart
action, an external one gets a "managed elsewhere" marker. Pretending otherwise
produces a restart button that silently does nothing.

### 6. The transport stays HTTP — gRPC is declined, again and with a measurement

ADR-0007 originally specified gRPC and its amendment replaced that with HTTP on
dependency grounds. Nothing here reopens it, and this decision *weakens* the case
for gRPC further:

- The measured cost is on record. ADR-0142 declined the official OTLP exporter
  because it "means taking protobuf and — even in its HTTP form — gRPC: 66 gRPC
  packages and about 13MB of binary, for a service that speaks no gRPC anywhere
  else", and hand-wrote the serializer instead: "+1.7MB of binary and five modules,
  no protobuf, no gRPC". `go.mod` confirms the state that bought: no gRPC, and
  protobuf only indirectly through `prometheus/client_model`.
- What gRPC would actually buy is bidirectional streaming, flow control, and codegen
  for polyglot workers. Long-poll on the post-fsync notification that ADR-0005
  already fires covers the push; backpressure is intrinsic to the job model (no
  worker pulls, jobs queue in state); and codegen is what the OpenAPI document
  (ADR-0043) is for.
- **Decision 3 removes most of the remaining argument.** If the standard worker is
  the same Go binary, there is no language boundary for gRPC to bridge — it would
  be a second protocol serving one process that could have called the first.

The condition under which to reopen it, stated so it is a measurement and not a
preference: **sustained job throughput where per-job HTTP framing is demonstrably
the bottleneck** — thousands of jobs per second across many workers, profiled, with
long-poll and connection reuse already in place. That would be its own ADR carrying
its own numbers, exactly as ADR-0142's was.

### Order of work

1. **Global job-type registry** — the correctness prerequisite. Nothing else is possible first.
2. **Type-keyed pull + long-poll** (ADR-0007's owed protocol). Out-of-process becomes usable.
3. **Fencing** (ADR-0007's open item 3) — a lease epoch presented on completion. It moves ahead of the console deliberately: a restart button makes double execution *more* likely, so it must not ship before the guard does.
4. **Worker registry + counters + Workers console**, read-only.
5. **`atlas worker`** — the same handlers, hosted as an HTTP client.
6. **In-process handlers off the run loop** (ADR-0149 option 3), so the single-node default stops being a stall.
7. **Optional supervision** — spawn, restart with backoff, log capture, and the console's actions.

Steps 4 and 6 each stand alone: the console is worth having with no worker
processes, and moving handlers off the loop is worth doing even if nothing is ever
relocated.

### Consequences

- **Positive:** the engine stops paying for third-party latency; a worker crash
  stops being an engine crash; kinds scale and restart independently; a customer can
  put a worker in the network where the credentials and the reachable systems live
  (ADR-0047's stated target for scripts); and the operator finally has a view of the
  thing that does the work. Because the worker is the same binary running the same
  handlers, none of that costs a connector rewrite.
- **Negative / trade-offs accepted:**
  - **Atlas becomes a process supervisor**, a genuine category expansion with real
    surface: zombie reaping, graceful shutdown, restart storms, log rotation, and
    Windows, which has no fork and different signals.
  - Every relocated call gains a network hop, so a fast local call gets *slower*
    while the engine gets faster. That is the intended trade and should be measured
    once, not argued about.
  - Two hosting modes to test — in-process and worker — for one set of handlers. The
    single implementation keeps that to a hosting matrix rather than a behavioral one.
  - The console's actions are unavailable for externally managed workers, forever.
- **Follow-ups / risks to watch:** worker authentication and least privilege
  (ADR-0049's shape, one credential per worker, not the admin's); per-kind
  concurrency limits so one slow kind cannot starve another inside a worker; whether
  the retry/backoff policy belongs to the worker or the engine once workers set
  their own backoff (ADR-0111); resource limits per worker process, which is where
  ADR-0047's "true resource limits" item finally has a home; and log retention
  bounds, since a chatty worker must not fill the data directory.

## Pros and cons of the options

### Option 1 — goroutine pool in the engine process
- Good: smallest change that fixes the stall; no new deployment concept; keeps the
  single-node experience exactly as it is.
- Bad: no isolation of any kind — a panic, a leak, or a runaway allocation is still
  the engine's; no independent scaling or restart; arbitrary script code still runs
  with the engine's privileges in the engine's address space.

### Option 2 — operator-deployed worker processes
- Good: real isolation, real independent scaling, the customer's trust domain; it is
  ADR-0007 as designed.
- Bad: every installation must now deploy and monitor something; without the console
  the operator is blind; the smallest installation pays the largest tax.

### Option 3 — option 2 plus supervision (chosen)
- Good: keeps the one-binary, one-command install while making out-of-process the
  normal case; supervised and external workers stay one program on one protocol.
- Bad: the supervisor is a real subsystem with a real security surface, and it is a
  thing Atlas would own forever.

### Option 4 — a process per job
- Good: maximal isolation per unit of work; no cross-job state to leak.
- Bad: startup cost per job destroys throughput and forfeits every connection,
  token and keep-alive a connector caches.

### Option 5 — HTTP long-poll (chosen)
- Good: the transport the product already speaks and documents; no new dependency;
  workable from any language and from `curl`.
- Bad: per-job framing overhead; long-poll needs care around proxies and timeouts.

### Option 6 — gRPC
- Good: streaming, flow control, generated clients.
- Bad: 66 packages and ~13MB, measured, for a product that speaks no gRPC anywhere;
  a second protocol next to the OpenAPI one; and no language boundary left to bridge
  once the standard worker is the same binary.

## Links

- extends [ADR-0155](0155-in-process-vs-out-of-process-service-tasks.md) — it classified the seams and deferred this; this record decides the target
- completes [ADR-0007](0007-job-worker-protocol.md) (the pull protocol, fencing, and the counters it still owes) and keeps its amended HTTP transport
- supersedes nothing, but turns [ADR-0149](0149-bounded-connector-call-budget.md)'s option 3 from a deferred idea into step 6 of a sequence
- realizes [ADR-0047](0047-polyglot-script-tasks-via-job-workers.md)'s stated target (the interpreter in the customer's trust domain) and gives its "true resource limits" item a home
- preserves [ADR-0011](0011-single-binary-distribution-and-web-ui.md) (one binary, one command) and [ADR-0010](0010-go-and-no-cgo.md) (few dependencies), which is what decides the transport
- the dependency measurement that decides against gRPC is [ADR-0142](0142-prometheus-metrics.md)'s
- worker authentication follows [ADR-0049](0049-internal-service-auth-for-mcp.md); the registry is runtime-only state, like [ADR-0150](0150-preview-mail-provider-and-visible-incidents.md)'s outbox
- failures keep surfacing through [ADR-0061](0061-incident-model.md)/[ADR-0111](0111-incident-model-completion.md) with retries per [ADR-0135](0135-retries-as-a-task-property.md)
