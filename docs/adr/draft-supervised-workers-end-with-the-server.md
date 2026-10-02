# ADR-DRAFT: On Windows, a supervised worker ends with the server, however the server ends

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-02
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0157](0157-worker-processes-supervision-and-console.md) made Atlas a process
supervisor and listed what that costs: "zombie reaping, graceful shutdown, restart
storms, log rotation, and Windows, which has no fork and different signals". The
supervisor handles one of those exits: when the server shuts down, `quit` closes and
every supervised worker is killed.

That covers every exit the server takes part in, and none of the ones it does not: a
crash, an out-of-memory kill, "End task" in the Task Manager, or a service wrapper that
gives up waiting and terminates the process. Two properties combine badly in those
cases:

- **Windows has no parent-death signal.** A child process does not learn that its
  parent is gone. Linux has `PR_SET_PDEATHSIG`; Windows has nothing equivalent per
  process.
- **A worker outlasts an unreachable server on purpose.** `Worker.Run` logs a failed
  poll and retries it. It must not exit, because a server restart and a type not yet
  deployed are both ordinary and both resolve.

So when the server dies without stopping its workers, every worker keeps running and
polls a dead address. A script interpreter a worker started keeps running too. When the
service wrapper restarts the server, the new server starts a full second set of workers
beside the orphans. Lease fencing keeps the orphans from completing the same job twice,
but they hold memory and handles. If the new server listens where the old one did, the
orphans also lease work with the configuration they were spawned with, which may be
stale.

This was raised by an operator looking at a Windows service: one `atlas.exe` server and
ten `atlas.exe` workers in the Task Manager. A clean stop removed all of them. The
question was what an unclean one would leave behind.

## Decision drivers

- **The guarantee has to hold when the server cannot act.** A crash runs no cleanup
  code, so whatever ends the workers must be done by the operating system on the
  server's behalf.
- **Don't fight a platform that owns process lifecycle.** ADR-0157 already said the
  supervisor "declines to fight a platform". Under systemd or Kubernetes the control
  group or the PID namespace ends the children.
- **Windows is a shipped target.** CI runs the API and the Worker Types on a Windows
  runner because a Windows binary ships and these packages are process behaviour.
- **A failure to bind must not stop a worker from starting.** The worker serves either
  way; only the cleanup guarantee is lost, and that is a warning, not an outage.

## Considered options

1. **Leave it to the service wrapper.** Some wrappers stop a process tree, but only on
   a stop they perform, and not when the server crashes between stops.
2. **The worker watches its parent.** Pass the server's PID, and have the worker poll
   for it or wait on a handle to it, and exit when it goes.
3. **A job object with `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`.** The server creates one
   job, puts each worker into it as it starts, and holds the only handle. When the
   server's process ends, for any reason, Windows closes the handle and ends every
   process in the job.

## Decision outcome

Chosen: **option 3, on Windows; nothing new elsewhere.**

`childLifetime` (`api/supervisor_lifetime_windows.go`) creates the job on the first
worker the supervisor starts, and assigns every worker to it right after `Start`. The
handle is not inheritable, so no child can keep the job alive. Processes a worker
starts, such as a script interpreter, are in the job too, because a child of a process
in a job is created in that job. The script connector's own per-interpreter job then
nests inside it, which Windows supports from Windows 8 and Server 2012. On an older host
that nesting fails, and the script connector already falls back to killing the
interpreter directly.

If the job cannot be created or a worker cannot be assigned, the supervisor logs
`worker.supervise_failed` and lets the worker run unbound. Other platforms get a no-op
`childLifetime`, for ADR-0157's reason.

**Why not option 2.** It puts the guarantee in the worker, so every worker binary has to
get it right, including an operator's own `--supervise` command. It also only notices on
its next poll, and needs a parent PID passed on argv, which a PID reused by Windows can
make point at the wrong process. A job object makes it the kernel's guarantee and needs
nothing from the child.

**Why not option 1.** It is not a guarantee: it depends on which wrapper is used and how
it is configured, and it does nothing about the case that motivated this record.

**The window that remains.** Between `cmd.Start` and the assignment, a server crash
would still orphan that one worker. Closing it would mean starting the child suspended
and resuming it after the assignment, which `os/exec` cannot do. The window is a few
instructions long, and a worker starts nothing of its own until its first lease
answers, so this record accepts it.

### Consequences

- **Positive:** on Windows, a crashed, killed or force-stopped server leaves no worker
  and no interpreter behind, and a restarted server never runs beside a previous set of
  its own workers.
- **Negative / trade-offs accepted:** a Windows-only code path, tested only on the
  Windows runner. On a host that refuses the job (an unusual job around Atlas itself on
  a pre-Windows-8 system), the old behaviour returns, with a warning.
- **Follow-ups / risks to watch:** graceful shutdown. The normal stop still kills workers
  outright, so a job in flight is abandoned to its lease, and since
  [ADR-draft-worker-runs-jobs-concurrently](draft-worker-runs-jobs-concurrently.md) that
  can be several jobs per type. A signal-and-wait stop with a deadline, inside the job,
  would let them finish.

## Pros and cons of the options

### Option 1 — the service wrapper
- Good: no code.
- Bad: covers only stops the wrapper performs, varies by wrapper, and misses the crash.

### Option 2 — the worker watches its parent
- Good: portable in principle.
- Bad: every worker has to implement it, including operators' own commands; it notices
  late; PID reuse makes it fragile.

### Option 3 — a kill-on-close job object (chosen)
- Good: enforced by the kernel; covers every way the server can end; includes
  grandchildren; needs nothing from the worker.
- Bad: Windows only, and a short window between start and assignment.

## Links

- closes a cost [ADR-0157](0157-worker-processes-supervision-and-console.md) listed for the supervisor ("Windows, which has no fork and different signals"), and keeps its rule that the supervisor does not fight a platform
- the worker's retry-forever poll loop that makes orphans possible is ADR-0157 step 5's, deliberately
- the per-interpreter job it nests is the script connector's (`connector/script/process_windows.go`, [ADR-0047](0047-polyglot-script-tasks-via-job-workers.md))
- sibling: [ADR-draft-worker-runs-jobs-concurrently](draft-worker-runs-jobs-concurrently.md)
