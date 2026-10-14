# ADR-0440: A worker runs the jobs of one type concurrently, up to the places it has free

- **Status:** Accepted (amended 2026-10-02 — IMAP session caps; see the amendment note below)
- **Implementation:** Landed
- **Date:** 2026-10-02
- **Deciders:** Atlas maintainers

> **Amendment (2026-10-02): IMAP session caps.** [ADR-0438](0438-mailbox-worker.md)
> landed beside this record and gave the mail worker mailbox operations. Each one opens
> an IMAP session of its own (`imapMailbox.session`), so under this record's default up
> to 16 sessions to one account can be open at once. An IMAP server that caps sessions
> per account is therefore one more target with a lower connection limit than the
> default, next to the SMTP relay and the rate-limited API named under *Consequences*.
> It answers the excess with login failures, which retries and incidents surface. The
> remedy is the same, `--worker-max-jobs`. The decision is unchanged. The handler was
> checked for this record's concurrency audit: the IMAP mailbox's fields are set once,
> when the client is built, and the Graph and Gmail mailboxes share only the
> mutex-guarded token cache, so no data race arises.

## Context and problem statement

[ADR-0157](0157-worker-processes-supervision-and-console.md) moved side-effecting work
into `atlas worker` processes, and [ADR-0233](0233-in-process-connectors-refused.md)
made that the default for every Worker Type. The move had a side effect that no record
decided.

In process, the job runner had run handlers **concurrently** since ADR-0157 step 6:
one goroutine per job, bounded by `job.DefaultConcurrency` (16). That record spelled out
why. A burst of parked jobs against a dead host had cost "one timeout after another",
and concurrent dispatch made it cost "the slowest of them".

The worker that replaced it was serial. `Worker.Run` polls each job type on its own
goroutine, which keeps one slow queue from starving another. Within a type, though,
`pollOnce` worked the jobs a poll returned one after another. The supervisor passed no
`--max-jobs`, so a supervised worker leased exactly one job at a time. Moving a kind out
of the engine therefore quietly brought back the amplification step 6 had removed:

- **Twenty REST calls at two seconds each took forty seconds**, even when the target
  could have answered them together.
- **`--max-jobs` claimed something it did not do.** Its help text said "keep it to what
  this worker can actually run at once", but a worker given `--max-jobs 5` leased five
  and ran them in turn. Jobs two to five held a lease they were not using. When the work
  was slow, those leases ran down before the jobs started: the job went back on offer,
  and it ran twice.

The question this record answers is how many jobs of one type a worker runs at once,
and what bounds that.

## Decision drivers

- **Moving a kind onto a worker changes where its work runs, not how much of it runs
  together.** The isolation ADR-0157 bought should not cost the concurrency ADR-0157
  step 6 had already bought.
- **A leased job must be a running job.** A lease is a claim other workers respect, and
  its clock runs from the moment it is granted.
- **The target's capacity is unknown to Atlas.** Any default is a guess about somebody
  else's server, so it has to be easy to lower.
- **An operator's own command is not Atlas's to parallelise.** A `--handle` or
  `--supervise` command may write a fixed temp file or assume one run at a time, and
  nothing in Atlas can know otherwise.

## Considered options

1. **Keep the worker serial**, and document `--max-jobs` as a batch size.
2. **Run a leased batch concurrently**: keep leasing `MaxJobs` per poll, and work the
   batch in parallel before polling again.
3. **Places, not batches**: each job type has `MaxJobs` places. A poll is made only when
   a place is free, asks for exactly as many jobs as are free, and every job starts the
   moment it arrives.

## Decision outcome

Chosen: **option 3**, with supervised built-in workers defaulting to the in-process
bound.

**In the worker.** `MaxJobs` now means how many jobs of one type the worker runs at once.
For each type, `Worker.serve` waits for a free place, takes every other place that is
free at that moment, and asks the engine for that many jobs. Each job runs on its own
goroutine and returns its place when its report has been sent. When every place is busy,
the worker does not poll. A leased job therefore never waits inside the worker, and a
lease runs only while its job runs. `RunOnce` works its one batch concurrently too, and
returns once every job in it has been reported. `Run` returns only after every job it
started has returned.

The engine never answers with more jobs than were asked for. If it ever did, the extra
jobs would still run, because they are leased to this worker, but each would wait for a
place first, so the bound holds whatever the server says.

**For the supervisor.** A supervised built-in worker is started with `--max-jobs` set to
`--worker-max-jobs`. That flag defaults to `job.DefaultConcurrency`, the bound the
engine puts on its own handlers, and the constant is shared so the two cannot drift. A
`--supervise` command worker gets no `--max-jobs` and keeps the worker default of one at
a time. A standalone `atlas worker` also keeps that default: nobody who started one has
asked for concurrency yet.

**Why places and not a parallel batch.** Option 2 fixes throughput but keeps a waste:
the slowest job of a batch decides when the next poll happens, so places sit empty while
it finishes. Option 3 polls as soon as a place frees up. It also turns the old help
text's promise ("what this worker can actually run at once") into the definition of the
flag.

### What was checked before raising the default

Every built-in handler is now called from several goroutines at once, so each one was
audited for shared mutable state. None has a data race. Every client, registry, token
cache and mock store is either read-only after `BuiltinConnectors` returns or already
guarded by a mutex. Several of these handlers already ran concurrently in process under
`job.Runner.Work`. Three effects are behaviour, not bugs, and are accepted:

- **The mock reporters** (AD, SQL) hold their mutex across the report's POST. Concurrent
  jobs of a mock-mode worker therefore queue behind one report, and most of them then
  find nothing new to send. That keeps reports in order and deduplicated. It costs
  latency only in the mock mode.
- **The SQL pool holds `sqldb.MaxOpenConns` (16) connections.** That equals the default
  here, so no job spends its call budget waiting for a connection. Raising
  `--worker-max-jobs` above 16 makes SQL jobs wait on the pool inside their own budget.
- **Script tasks with the sandbox off share the worker's `HOME` and `TMPDIR`.** A
  model-authored script that writes a fixed path can now collide with a second run of
  itself. Such scripts already ran concurrently when the kind was served in process.
  `--script-sandbox strict` gives each run its own scratch directory.

### Consequences

- **Positive:** a slow target costs the slowest call again, not the sum of the calls. No
  job waits on a lease it is not using. `--max-jobs` does what its help text says. The
  supervised and the in-process paths have one concurrency bound.
- **Negative / trade-offs accepted:** an installation upgraded from a serial worker now
  sends up to 16 calls of one type at a time to each target. A target with a lower
  connection limit (an SMTP relay, a rate-limited API such as Discord's) answers more
  failures, which retries and incidents surface. The fix is `--worker-max-jobs`. Each
  job type gets its own places, so the script worker, which serves three languages, can
  run up to three times the bound.
- **Follow-ups / risks to watch:** a per-type or per-Worker bound in the Console, which
  is where an operator who knows a target's limit would look for it; and graceful
  shutdown of supervised workers. The supervisor still kills a worker outright on stop,
  which now abandons up to `MaxJobs` jobs per type to their leases rather than one.

## Pros and cons of the options

### Option 1 — keep it serial
- Good: nothing to change; no handler is ever called concurrently.
- Bad: a slow target costs the sum of its calls; `--max-jobs > 1` leases jobs that then
  wait, and can run them twice.

### Option 2 — a parallel batch
- Good: small change; throughput mostly restored.
- Bad: the slowest job of a batch decides when the next poll happens, so places sit
  empty while it finishes.

### Option 3 — places (chosen)
- Good: every leased job runs at once; a freed place is filled at once; the bound holds
  whatever the server returns.
- Bad: a little more code than option 2, and the poll loop is one goroutine per type
  plus one per running job.

## Links

- restores, on a worker, the concurrency [ADR-0157](0157-worker-processes-supervision-and-console.md) step 6 gave in-process handlers
- realises ADR-0157's follow-up "per-kind concurrency limits so one slow kind cannot starve another inside a worker"
- the lease and fencing a waiting job used to run down are [ADR-0007](0007-job-worker-protocol.md)'s and [ADR-0274](0274-in-process-job-leases.md)'s
- the defaults it changes are the supervised workers of [ADR-0164](0164-no-in-process-service-tasks.md) and [ADR-0233](0233-in-process-connectors-refused.md)
- its sibling on supervised workers is [ADR-0439](0439-supervised-workers-end-with-the-server.md)
