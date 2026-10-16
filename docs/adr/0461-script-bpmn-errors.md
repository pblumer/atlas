# ADR-0461: A script throws a BPMN error by giving its error a code the process catches

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-16
- **Deciders:** Atlas maintainers

## Context and problem statement

ADR-0089 gave Atlas error events. An error boundary or an error event subprocess catches
an error by its code, and three things can throw one:

- an error end event;
- an external worker, over `POST /api/v1/jobs/{key}/error`;
- an operator, by hand.

A script task could not throw one. The script worker knew two outcomes: a result, or a
failure. A failure is retried and then waits on an incident. So the Modeler let an author
draw an error boundary on a script task — «userPrincipalName already exists» on a task
that creates Teams resource accounts — and nothing could ever reach it. The case the
boundary was drawn for arrived as an incident after three identical attempts, and somebody
had to resolve it by hand.

Two things were missing, and one more had to be settled:

- **A way for a script to say which error it raised.** It has to work in PowerShell,
  Python and JavaScript, and it has to leave the script runnable unchanged at a prompt
  (ADR-0458).
- **A way for the in-process runner to throw a BPMN error.** The external worker had one
  (`worker.BPMNError`); `job.Runner` had none.
- **A rule for when a raised error is a BPMN error rather than a failure.** Almost every
  error carries some identifier. Treating every identifier as a code would skip the
  retries that a transient fault is owed.

## Decision drivers

- **The model decides what is a business error.** A code is a BPMN error because the
  process has a path for it, not because a script said so.
- **Each language's own idiom.** A PowerShell author writes `Write-Error -ErrorId`, a
  Python author an exception with a `code`, a JavaScript author an `Error` with a `code`.
  At a prompt each of these is an ordinary error, so nothing has to be stubbed out to run
  the script there.
- **Retries are kept for faults.** An identifier that the model does not catch must not
  change how a failure is handled.
- **One decision for both paths.** The in-process runner and an external worker must
  agree, so the decision lives in the code they share (`script.Run`).

## Considered options

1. **The script gives its error a code, and Atlas throws it when the process catches that
   code** (chosen).
2. **A mapping in the model:** a FEEL expression on the task turns the failure message
   into a code, for example `if contains(error.message, "already exists") then "UPN_EXISTS"`.
3. **A reserved shape in the result:** an object such as `{bpmnError: {code, message}}`
   returned instead of thrown.
4. **Throw every code**, and let an uncaught one wait on an incident.

## Decision outcome

Chosen option: **1**.

### What a script writes

| Language | The code is | Example |
|---|---|---|
| PowerShell | the error's ErrorId: what `Write-Error -ErrorId` sets, or the text of a bare `throw` | `Write-Error -Message $msg -ErrorId 'UPN_EXISTS' -Category ResourceExists` |
| Python | the exception's `code` attribute, a string or an integer | `e = RuntimeError(msg); e.code = 'UPN_EXISTS'; raise e` |
| JavaScript | the error's `code` property, a string or a number | `throw Object.assign(new Error(msg), { code: 'UPN_EXISTS' })` |

- In PowerShell, an ErrorId raised inside a function carries the function's name after a
  comma (`UPN_EXISTS,New-Account`). The code is the part before the comma.
- Python's `SystemExit` carries a `code` too, the exit status, and is not a code:
  `sys.exit` is how a script ends, not an error it raises. Only `Exception` is examined.
- The message is the error's own message, without the interpreter's framing.

### How it reaches Atlas

- **Bootstrap:** each language's bootstrap catches a terminating error and writes
  `{"atlasError":{"code":…,"message":…}}` to stdout. It then rethrows the error unchanged,
  so the run fails exactly as before: same stderr, same exit status, and in PowerShell the
  same rendered position in the script. stdout is free for the report because a failed run
  has no result.
- **`script.CmdExec`:** reads the report into a `script.CodedError`. That error still reads
  as the interpreter's own failure.
- **`script.Run`:** throws a `job.BPMNError` when the code is one the task's process
  catches. Any other coded failure stays a failure.
- **Which codes a process catches:** `CompiledProcess.CaughtErrorCodes()` holds every code
  that one of its error boundaries or error event subprocesses names. A code-less catch-all
  is not in it: it catches every code but names none, and counting it would make every
  identified failure skip its retries. The set is computed once in `Builder.Build` (I5).
- **Out-of-process workers:** the set travels with the resolved job (`script.Job.ErrorCodes`,
  the `errorCodes` field of the lease payload), because a worker has no compiled process
  to ask.
- **In-process runner:** `job.Runner.Submit` routes a `job.BPMNError` to the engine's
  `ThrowJobErrorWithMessage`. A BPMN error without a code is a failure, exactly as
  `worker.Worker` already treated one. The gate hears a success: the job's target answered.
  `worker.BPMNError` is now an alias of `job.BPMNError`, so one value works on both paths.
- **Test run:** `/api/v1/scripts/run` has no process context. It reports the code the
  script raised (`errorCode`) beside the failure, and the Modeler shows it.

### Consequences

- **Positive:**
  - An error boundary on a script task works. The instance takes the boundary's path at
    once, with no attempt spent on a retry.
  - The script still runs unchanged at a prompt, where its error is an ordinary one.
  - The same contract holds in three languages, and a process that does not catch a code
    behaves exactly as before.
- **Negative / trade-offs accepted:**
  - A code thrown from a child process and caught only by the parent's call activity is not
    a code the child catches, so it fails in the child. To catch it there, the child must
    catch the code itself (and may rethrow it with an error end event).
  - A code the process catches somewhere, but not around the failing task, is thrown and
    waits on an incident without being retried. That is ADR-0089's rule for every uncaught
    throw.
  - A script that catches its own errors and ends with `exit 1` raises nothing, so it has no
    code. It has to rethrow with a code instead.
- **Follow-ups / risks to watch:**
  - A FEEL mapping in the model (option 2) remains possible for a failure whose script
    cannot be changed. It would feed the same `job.BPMNError` path.

## Pros and cons of the options

### Option 1 — a code on the error, thrown when the process catches it (chosen)
- Good: the script decides with full knowledge of the error, its type and its message.
  The model decides whether that code has a path.
- Good: native in all three languages, and harmless at a prompt.
- Bad: the rule depends on the model, so the same script fails in one process and throws
  in another. That is the point, but it has to be documented.

### Option 2 — a FEEL mapping on the task
- Good: no change to the script at all.
- Bad: the mapping matches the text of a message, which is fragile and in some
  environments localised.
- Bad: it needs a new task field, editor support, and FEEL compiled and evaluated on the
  failure path.

### Option 3 — a reserved result shape
- Good: no bootstrap change.
- Bad: a script would have to return what is an error, and at a prompt it would "succeed"
  with an object nobody inspects.
- Bad: it collides with results that legitimately contain such a key.

### Option 4 — throw every code
- Good: simplest.
- Bad: every terminating error has an identifier, from `CommandNotFoundException` to the
  text of a `throw`. Every fault would skip its retries and land straight on an incident.

## Links

- relates to ADR-0089 (error events): adds the script task as a thrower
- relates to ADR-0047 (polyglot script tasks) and ADR-0458 (scripts unchanged at a prompt):
  extends the bootstrap contract
- relates to ADR-0168 (resolved jobs for out-of-process workers): the caught codes travel
  with the job
