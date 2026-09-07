# ADR-0265: Signing in does not wait for the run loop

- **Status:** Proposed
- **Date:** 2026-09-07
- **Deciders:** Atlas engine team

## Context and problem statement

On 7 September 2026 nobody could sign in to a shared Atlas server. The engine was
not down: `GET /api/v1/info` answered instantly throughout. `POST /api/v1/auth/login`
did not answer at all.

The store held ~50.000 active process instances and ~201.000 tokens, almost all of
them one identity-lifecycle load test parked in place, with generators still
starting and finishing instances the whole time. The run loop — one goroutine
draining an unbuffered queue of closures, one at a time, in arrival order
(`api/runloop`, I3, ADR-0002/0006) — was therefore busy essentially always.

What made this a lockout rather than a slowdown is where the login sat. `handleLogin`
dispatched two closures onto that loop:

```go
s.do(func() { u, ok, lookErr = s.users.byUsername(username) })
s.do(func() { groupIDs, grpErr = s.groups.idsForUser(u.ID) })
```

Neither reads engine state. Users and groups are durable *sidecars* — one JSON file
per record, written with the atomic-rename discipline, never through the WAL or the
processor (ADR-0044, ADR-0019/0021). They were on the loop because the loop is where
design-time state lives by convention, not because either read needs the single
writer.

ADR-0239 established the shape of the answer for *expensive* reads: a scan whose cost
grows with the instance population must not hold the writer. This incident is the
other half, and it is not about the login's own cost at all. `/incidents` timed out
alongside it with exactly **two** rows to return, and the login reads no engine state
whatsoever — neither had work to do, and both were nevertheless unreachable, because a
read that needs a turn on the loop is only as available as the engine is idle.

What they were queued *behind* is the subject of its own record, and it was not idle
time: `/stats` walks all three column families, so at this population one call was a
quarter-million-key scan, and seven write paths ran that same scan on the loop to
report the counts back (ADR-0266). That is what a
login had to wait for. It does not change this record's decision — a login must not
wait for the loop *however* long the queue happens to be — but it is why the wait was
unbounded in practice rather than merely noticeable.

That is the wrong dependency for authentication specifically, because signing in is
what an operator does *in order to* deal with the busy engine. The one credential
that reaches the cancel button was gated behind the thing it needed to cancel.

Two properties of the existing code sharpen the question rather than soften it:

- **The expensive half was already off the loop.** `checkPassword` (bcrypt, tens of
  milliseconds by design) runs outside `do`. Only the cheap half — reading a small
  file — waited.
- **The rest of the session path was already off the loop.** `principalFor` resolves
  every authenticated request from `sessionStore`, which carries its own mutex and
  never touches the loop, and group ids are snapshotted into the session precisely so
  the access path reads no store (ADR-0180/0185). Login was the one step that had not
  been moved.

## Decision drivers

- **The front door must not depend on engine load.** An operator signs in to fix a
  busy server; a login that fails because the server is busy removes the remedy at
  exactly the moment it is needed.
- **Accounts are not engine state.** Nothing about them is replayed, partitioned or
  ordered against process state; the single writer buys them nothing.
- **No new concurrency surface.** The fix must not introduce a lock, a cache or a
  second copy of the truth that can drift from the files on disk.
- **Writes stay serialized.** A check-then-write ("is this username taken?") is atomic
  only inside one loop turn, and must remain so.

## Considered options

1. **Leave the login on the loop and keep making the loop faster.** Continue ADR-0239
   until nothing holds the writer long enough to matter.
2. **Read the identity sidecars directly, off the loop (chosen).**
3. **Keep an in-memory user index**, maintained on the loop and published to readers
   through a mutex or an atomically-swapped snapshot.

## Decision outcome

Chosen: **option 2.** The three reads on the sign-in path — the password login's user
lookup and group snapshot, and the OIDC callback's group snapshot — call the store
directly. No dispatch, so a login costs zero loop turns and completes at whatever
speed the filesystem answers, no matter what the processor is doing.

This is safe because of what a `sidecar.Store` already is, and the type now says so:

- **It holds no mutable memory.** The struct is a directory path and a few pure
  functions; every method is a syscall. There is no shared state for concurrent
  readers to corrupt, which is why no lock appears anywhere in this change.
- **A record is replaced by an atomic rename** (temp → fsync → rename → dir fsync).
  A concurrent reader sees the whole old record or the whole new one, never a torn
  one. The temp file is named `<stem>.json.tmp`, which the listing's `.json` filter
  already skips, so a half-written record is never even a candidate.
- **A record can now vanish mid-listing.** `LoadAll` reads the directory and then each
  file; off the loop, a record may be deleted in between. That used to fail the whole
  listing. It now skips the entry, which is the answer `Get` has always given for a
  record that is not there — and the alternative would let one administrator's delete
  refuse an unrelated person's login.

This is not a new assumption about the sidecar, either. `atlas reset-password` already
opens the same user directory from a *separate process* and writes it while the server
is running, and says so in as many words: "running it against a live server is safe
(writes are atomic)". Concurrent out-of-process writes were already sanctioned; what
this record adds is concurrent in-process reads, which is the weaker claim.

**Writes do not move.** The run loop remains the single writer of design-time state.
The OIDC callback's *account resolution* also stays on it deliberately: that path may
create the account it is resolving, and the check-then-write is atomic only inside one
turn (the same reason `freeUsername` documents for staying there).

### Consequences

- **Positive:** Signing in is independent of engine load. The `/info`-vs-`/login`
  asymmetry that made this incident so confusing to diagnose is gone: authentication
  now behaves like the rest of the session path, which never needed the loop either.
- **Positive:** `LoadAll` is honest about a concurrently deleted record instead of
  turning it into an error for every caller.
- **Negative / trade-offs accepted:** A login's group snapshot may now read group
  records of mixed vintage if it races an edit — each record is whole, but the *set*
  is not a point-in-time view. This is already tolerated by design: membership changes
  are pushed into open sessions (ADR-0185), so a snapshot taken a moment early
  converges rather than sticking.
- **Negative:** A login is no longer serialized against a concurrent account edit. The
  observable outcomes are the same either way — the login sees the account as it was
  just before or just after the edit — but "just before" is now genuinely possible
  where the loop previously made the ordering total.
- **Follow-ups / risks to watch:** This fixes the front door, not the building. There
  are still ~230 `s.do` dispatch sites in `api/` against 11 `readOffLoop` ones, and the
  same queueing keeps `/stats` and `/incidents` from answering under load even though
  neither has real work to do — `/incidents` additionally still walks its family
  *inside* `do`, which is the ADR-0239 shape and wants converting. The engine's own
  timer tick also runs through `s.do` every second, so it is a co-tenant of the same
  queue rather than a separate lane; giving reads a lane of their own is the larger
  question this record does not answer.

## Pros and cons of the options

### Option 1 — keep the login on the loop, make the loop faster
- Good: no change to the concurrency model at all; one place still owns everything.
- Good: it is the work ADR-0239 already started, and it helps every caller at once.
- Bad: it cannot succeed as stated. The loop is not *accidentally* busy — executing
  commands is its job, and a server under real load will always have a queue. Making
  the queue shorter changes how long the login waits, never whether it can wait
  forever.
- Bad: it makes authentication's availability a performance property, so every future
  regression anywhere on the loop is also a lockout.

### Option 2 — read the identity sidecars off the loop
- Good: removes the dependency rather than shortening it; a login cannot be starved by
  engine work it never touches.
- Good: no new state, no lock, no cache — it deletes code rather than adding a
  mechanism, and the durability discipline that makes it safe was already there.
- Good: it matches what the rest of the auth path already does (`sessionStore`,
  `principalFor`).
- Bad: gives up total ordering between a login and a concurrent account edit, and
  requires `LoadAll` to treat a vanished record as absent rather than as a failure.

### Option 3 — an in-memory user index published to readers
- Good: the fastest possible lookup, and it would also remove the per-login directory
  scan that `byUsername` still performs.
- Bad: a second copy of the truth. The files on disk stay authoritative — backup,
  restore and external edits all write them — so the index can drift, and the drift
  shows up as somebody being unable to log in.
- Bad: it buys speed, and speed was never the problem; the login was queued, not slow.
  Adding a cache to fix a queueing bug leaves the queueing bug.

## Links

- relates to [ADR-0044](0044-user-management-and-authentication-boundary.md) — accounts as a durable sidecar, not engine state
- relates to ADR-0239 — read-only queries off the run loop, for the reads that are expensive rather than merely queued
- relates to ADR-0080 — maintained counters, the first answer to "one query stops the engine"
- relates to ADR-0185 — live group-membership push into open sessions
- constrained by [ADR-0002](0002-single-writer-partition-model.md) / ADR-0006 (I3) — the single writer, which this record does not weaken: writes stay on the loop
