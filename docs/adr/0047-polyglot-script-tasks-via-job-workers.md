# ADR-0047: Polyglot script tasks (PowerShell, …) via job workers

- **Status:** Proposed
- **Date:** 2026-07-24
- **Deciders:** Atlas engine team

> **Implementation status.** Not implemented. This ADR decides the shape;
> PowerShell is the first target language. Python and JavaScript are explicitly
> in scope for the *same* mechanism but are follow-up work, not part of the first
> slice.

## Context and problem statement

Atlas already has a **script task**, but exactly one flavour of it: a FEEL
expression evaluated *inside* the engine. On activation, `scriptTaskBehavior`
(`engine/behavior.go`) evaluates the task's compiled FEEL expression on the
single-writer processor goroutine, writes the result to a variable, and
completes — no external worker involved. That is only sound because FEEL is
**pure, deterministic, side-effect-free, and compiled at deploy time**
(ADR-0008/0015): it may run on the hot path without violating any invariant.

Users want to author script tasks in **general-purpose languages** — PowerShell
first (many in-house PowerShell developers), then Python and JavaScript. These
languages are the opposite of FEEL in every property that made inline execution
safe: they perform I/O, spawn processes, are non-deterministic, and are slow.
Running such a script the way the FEEL task runs — inline in a behavior — would:

- block the single-writer partition on arbitrary user code and process startup
  (violates ADR-0007, and I1's no-hot-path-work spirit),
- tempt execution inside `applyToState`, which is replayed on recovery and must
  stay deterministic and side-effect-free (I4),
- run an outward side effect before the causing event is durable (violates I2 /
  ADR-0005).

So "add another script task type" is **not** a variation on the FEEL script
task. The real question is: what execution seam do non-FEEL scripts run through,
such that the engine's invariants hold and the same mechanism generalizes across
languages?

Two constraints shape the answer:

- **The trust boundary matters.** Arbitrary PowerShell/Python/JS is the largest
  new attack surface Atlas would gain. Where and under whose privileges the
  interpreter runs is a first-class design concern, not an afterthought.
- **Reuse the proven seam.** Atlas already delegates real, side-effecting work to
  external engines through the job/worker path: DMN to temis (ADR-0014), and the
  clio/REST connectors (ADR-0036), all discriminated by a **reserved job type**.
  A new task type should reuse that, not invent a parallel mechanism.

## Decision drivers

- **Invariants are non-negotiable.** Running a script is a side effect: it must
  run only in the post-fsync side-effect phase (I2 / ADR-0005), never inside
  `applyToState` (I4), never on the single-writer goroutine (ADR-0007), and must
  not allocate on the hot path (I1).
- **Determinism of replay.** The script's *result* must be frozen into the job
  completion event and re-applied verbatim on recovery (I6) — recovery must never
  re-run the interpreter.
- **One mechanism, many languages.** PowerShell is first, but Python and
  JavaScript must drop in without a new engine concept each time.
- **Isolation and least privilege.** The interpreter must not run with the
  engine's credentials or in the engine's address space; it should be movable
  into the customer's own trust domain.
- **Deploy-time validation, not deploy-time execution.** The compiler may
  validate that a script and its result variable are present; it must never
  execute user code.

## Considered options

**For the execution shape:**

1. **Inline behavior (like the FEEL task).** A new behavior runs the interpreter
   during command processing. Rejected on sight: network/process I/O on the
   single writer, invites a call from inside `applyToState`; violates I1, I4, I2,
   ADR-0007. Listed only to be explicit — it is the thing this ADR exists to
   avoid.
2. **Job task via the job/worker path (chosen).** A script task compiles to a job
   carrying a **reserved, per-language job type** (e.g. `io.atlas.script.powershell`).
   A generic `scriptJobTaskBehavior` — a near-copy of `connectorTaskBehavior` —
   only creates the job at activation and waits. An in-process worker (new
   `pwsh` package), off the processor goroutine and after fsync, resolves the
   script from the compiled process, runs the interpreter, and completes the job,
   mapping the result back to a process variable. This mirrors ADR-0014/0036
   almost verbatim.
3. **A single generic `io.atlas.script` job type with the language in the job
   detail.** One worker dispatches to the right interpreter by language. Rejected
   as the primary shape: it couples all interpreters into one deployable worker,
   whereas a customer typically wants to run *only* the PowerShell worker, in
   their environment, without pulling in a Python/JS runtime. Per-language job
   types let each worker be deployed and secured independently (and a combined
   worker can still subscribe to several types if desired).

**For where the interpreter runs:**

- **In-process worker on the engine host (first slice).** Matches the current
  architecture: the job runner is in-process today (`job/job.go` notes gRPC
  streaming workers are a later milestone). The worker shells out to `pwsh`.
- **External gRPC worker in the customer's trust domain (target).** The same
  worker, run remotely against the customer's modules/servers, once the streaming
  transport lands. This is the natural isolation boundary and the eventual
  recommended deployment.

**For how the script reaches the worker:**

- **Interned in the compiled process (chosen).** The script text and result
  variable are model-authored, deploy-time data (I5), interned into a
  `ScriptJobTaskDetail`, exactly as a connector's method/path are interned into
  `ConnectorTaskDetail`. The worker resolves them via a `ProcessLookup`, the way
  `dmn/worker.go` resolves a decision. It is not embedded in the runtime job
  payload.

## Decision outcome

Chosen: **the script-job-task via the job/worker path (option 2), with a reserved
per-language job type, the script interned in the compiled process, and an
in-process worker that shells out to the interpreter first — designed to move to
an external gRPC worker later.** The inline behavior (option 1) is rejected; the
single-generic-type shape (option 3) is rejected as the default but remains a
possible convenience for a combined worker.

The **FEEL script task is unchanged.** It remains the inline, in-engine path for
pure expressions. Non-FEEL scripts are a distinct compiled node type and a
distinct runtime path; the two never share a behavior. The modeler distinguishes
them by a **language marker** (an `atlas:` moddle extension carrying
`language="powershell"` plus the script body), so an empty/`feel` language keeps
the existing inline path and any other language compiles to the job path. The
compiler chooses the path at deploy time; the engine never inspects the language
on the hot path.

Concretely, mirroring ADR-0014/0036:

- **Compiler.** A reserved job type `PwshJobType = "io.atlas.script.powershell"`
  with a fixed interned `PwshJobTypeIndex`, reserved alongside `DMNJobType` /
  `UserTaskJobType` in the builder's reservation block. A new
  `ScriptJobTaskDetail{ JobType, Language, Source, ResultVar }` (all interned
  indices) and a `TypeScriptJobTask` node type. Deploy-time validation mirrors the
  FEEL task: an empty script or a missing result variable fails the deploy; **no
  user code is executed at compile time.**
- **Engine.** A `scriptJobTaskBehavior` that, on activation, creates a job of the
  reserved type and waits — a near-copy of `connectorTaskBehavior`. No new value
  type, no `applyToState` change, no processor change.
- **Worker.** A new `pwsh` package providing a `job.OutputHandler` registered via
  `HandleWithOutput(PwshJobTypeIndex, …)`, structured like `dmn.Handler`: resolve
  the element instance → compiled process → `ScriptJobTaskDetail`, read the
  instance's variables, run the interpreter through an **injectable `Exec`
  interface** (so tests are deterministic without `pwsh` installed), classify the
  script's output into the result variable, and return it on the `CompleteJob`
  command. A handler error leaves the job pending, exactly like any worker
  (incident/retry policy is the same later milestone the other workers await).

The script's result is written into the job-completion variable event and
re-applied verbatim on replay (I6); recovery never re-runs the interpreter,
exactly as the FEEL task's result is frozen rather than re-evaluated.

### Consequences

- **Positive:** processes can run PowerShell (then Python, JS) at modeled points
  with crash recovery and non-blocking execution inherited wholesale from the job
  protocol, and **zero hot-path or `applyToState` impact**. The interpreter
  dependency lives only in the new worker package, never in `engine`. Adding a
  language is additive: a new reserved job type and a new worker; the compiler
  node type, behavior, and recovery semantics are shared. The interpreter can be
  moved into the customer's trust domain when external workers land, without a
  model change.
- **Negative / trade-offs accepted:** a script task is at-least-once and parks the
  token until its worker is reachable — the same failure mode as any service task.
  Arbitrary interpreter execution is a real security surface that must be
  contained by the worker (see risks). The in-process-first slice runs `pwsh` on
  the engine host, which is acceptable for a controlled environment but is **not**
  the recommended production isolation posture — that is the external worker. The
  result-variable mapping leans on the variable subsystem the DMN/connector tasks
  already use.
- **Follow-ups / risks to watch:** **security is the headline risk** — run the
  interpreter under least privilege (dedicated OS user/container), enforce
  per-job timeouts and resource limits, invoke with `-NoProfile -NonInteractive`
  and never pass untrusted `-EncodedCommand`, and keep engine credentials out of
  the interpreter's environment. Define the input contract (which variables the
  script sees, and how — JSON on stdin is the current intent) and the output
  contract (how the result variable is captured) precisely, and publish it.
  Deliver the external gRPC worker so PowerShell can run in the customer's
  environment. Generalize to Python and JavaScript once PowerShell is proven
  end-to-end. Decide the incident/retry policy shared with the other workers.

## Pros and cons of the options

### Option 1 — inline behavior like the FEEL task (rejected)
- Good: no job round-trip; trivial to wire.
- Bad: arbitrary I/O and process startup on the single writer; invites execution
  inside `applyToState`; non-deterministic; violates I1, I2, I4, ADR-0007. Not
  viable.

### Option 2 — job task via the job/worker path (chosen)
- Good: reuses ADR-0014/0036/0007 wholesale (recovery, non-blocking, dependency
  isolation, near-zero engine change); result frozen for deterministic replay
  (I6); interpreter movable into the customer's trust domain; validated at deploy
  time without executing code.
- Bad: at-least-once; parks the token if the worker/interpreter is down; adds an
  external runtime dependency to the deployment; security of arbitrary execution
  must be handled in the worker.

### Option 3 — one generic `io.atlas.script` type, language in the detail (rejected as default)
- Good: a single worker; one job type to reserve.
- Bad: couples all interpreters into one deployable; a customer wanting only
  PowerShell must still ship/trust the Python/JS runtimes; weaker per-language
  isolation and independent deployment. Kept only as an optional convenience for a
  deliberately combined worker.

## Links

- mirrors ADR-0014 (DMN business rule tasks via temis) and ADR-0036 (clio/REST
  connectors) — the work-via-job/reserved-job-type pattern — and ADR-0007 (job
  worker protocol)
- contrasts with ADR-0008 / ADR-0015 (FEEL compiled and evaluated inline) — the
  reason the FEEL script task stays in-engine while polyglot scripts do not
- depends on ADR-0005 / I2 (side effects only after fsync) and honors I1, I4, I6
- relates to ADR-0041 (connector management and secret store) for how an external
  worker's environment and credentials are governed
