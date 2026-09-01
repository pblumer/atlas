# ADR-0213: The mock Active Directory is visible in the Console

- **Status:** Accepted
- **Date:** 2026-08-31
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0181](0181-ad-connector-mock-mode.md) gave the AD worker a mock directory: the same
resolved job, the same `Run`, against entries that live in the worker's memory.
[ADR-0193](0193-ad-mock-in-the-console.md) moved the switch into the Console and
[ADR-0202](0202-atlas-manages-the-ad-mock-seed.md) moved the *seed* — the entries every
forest starts from — in with it, so an operator uploads or pastes a starting directory
instead of naming a file on a host they cannot see.

What none of that gave anyone is a way to see the directory. A mockup run leaves two
traces:

- the **seed**, on the Active Directory card. It is an input. The worker reads it once
  at startup and never writes back, by design — the file is content-addressed precisely
  so that *changing* it restarts the worker (ADR-0202), which would throw away the very
  forest a write-back would be recording.
- the worker's **log**, one `ad_mock.performed` line per operation, read under
  Operations › Workers.

So "did that joiner create the account?" is answered by reading a log, and "what is in
the directory now?" is not answered at all. Worse, the seed is the only
directory-shaped thing on the screen, which makes it exactly where somebody looks for
an account that was never going to be there — the question that prompted this record,
asked by an operator who had added a user and went looking for it under *Edit* on the
seed file.

A mockup exists to be tried out. A directory nobody can look at is a poor thing to try
a joiner/mover/leaver process against, and the workaround — reading a DN out of a log
line — is exactly the kind of thing the Console exists to remove.

## Decision drivers

- **A mockup has to be inspectable**, or the run it makes possible cannot be checked.
- **The directory lives in the worker**, which may be a supervised child on this host
  or an external process in a network this server cannot dial into (ADR-0168).
- **Nothing about a mock is durable**, and nothing about it may become durable: it is
  runtime state, never an event, never replayed (I4/I6).
- **The seed must not become a write target.** It is an input, and making it one would
  restart the worker on every write, losing what it recorded.
- **Directory-shaped data is not public.** The seed's content is admin-only for a
  reason; live entries are the same data.

## Considered options

1. **The worker reports its forest; the Console shows it.** The worker snapshots what it
   holds and posts it to the Atlas whose Console the operator is watching, which keeps
   the newest report per worker in memory and renders it.
2. **The worker serves its own small page.** An HTTP listener in the worker process with
   a forest view on it.
3. **The server pulls from the worker.** An endpoint on the worker the engine polls.
4. **Write the forest back into the seed.** Keep using the card that already exists.

## Decision outcome

Chosen option: **"the worker reports its forest; the Console shows it"**, because it is
the only one of the four that works for every worker Atlas can have, and because the
same problem already has this answer: the preview mail outbox (ADR-0150), where a
worker frames a message in its own process and posts it back to the server whose
Operations view the operator is reading.

Concretely:

- `MockDirectory.Snapshot(maxEntries)` takes one consistent picture across every forest
  — one lock, so a two-directory run is not reported half from before a write — plus the
  seed count and the operation journal.
- An AD worker in mockup mode posts it to `POST /api/v1/ad/mock-directory` after every
  job and once at startup, under `ATLAS_WORKER_ID` and behind its own token.
  `ATLAS_AD_MOCK_VIEW_URL` says where; a supervised worker is handed it at spawn, and
  only while the mockup is on.
- The server keeps the newest snapshot per worker in an `ad.MockView` — memory, its own
  lock, off the run loop, bounded to eight workers — and serves it at
  `GET /api/v1/ad/mock-directory`, admin-only.
- Operations › **Mock directory** renders one card per worker, one containment tree per
  LDAP URL, every entry's attributes, and the operation journal underneath.

**A report is an observation, never part of the work.** It describes an operation that
has already happened, so a report that cannot be delivered is logged
(`ad_mock.report_failed`) and dropped — the job succeeds either way. The alternative,
failing a job because a view could not be updated, would make a directory nobody can
see into a directory nobody can write to.

**The bounds are load-bearing.** A snapshot carries at most 2000 entries and says
`truncated` with the number the forest actually holds; the report body is capped; the
view keeps eight workers, dropping the one heard from longest ago. A mock forest is
unbounded — a bulk import against it is an ordinary thing to try — and a view of one
must not be.

**No password travels**, because none is stored: a set-password is validated and then
not kept (ADR-0181), and the journal redacts it. A test asserts this now that entries
leave the worker's process.

### Consequences

- **Positive:** The question that prompted this — "I added a user, why is it not in the
  worker config under Edit?" — is now answered by a screen instead of by an explanation.
  The seed stops being the only place to look, and the view says in words what it is
  and what the seed is. A mockup run against two forests is legible as two forests.
- **Negative / trade-offs accepted:** One HTTP round trip per AD job in mockup mode
  (skipped when the directory has not changed), and a second copy of the forest in the
  server's memory. Both are bounded, both exist only in mockup mode, and neither touches
  the engine's run loop. The view can be one report behind — it is a report, not a
  query — which the "reported at" stamp on every card makes visible.
- **Follow-ups / risks to watch:** The report is a whole snapshot each time, which is
  right for a directory small enough to look at and would be wrong for a large one; if
  mock forests ever grow past the entry bound in practice, the answer is a delta or a
  paged read, not a bigger bound. Nothing here is durable, which is deliberate: if a
  mockup run ever needs to be kept, that is an export, not a store.

## Pros and cons of the options

### Option 1 — the worker reports, the Console shows
- Good: works for a supervised worker and for one in a network the server cannot reach.
- Good: the pattern, the direction and the security posture are the preview outbox's,
  already reviewed and already understood.
- Good: the view lands where the operator already is, next to the switch and the log.
- Bad: the view is a report and can be behind; a worker that cannot reach its server
  shows nothing (and says so in its log).

### Option 2 — the worker serves its own page
- Good: no server changes at all; the forest never leaves the process that owns it.
- Bad: a port to configure, expose and secure per worker, on a process that today needs
  no inbound connectivity whatever — and the Console could not link to it in general.
- Bad: a second, differently-shaped UI to maintain beside the Console.

### Option 3 — the server pulls
- Good: always current, with no report to be behind.
- Bad: requires the engine to reach the worker, which ADR-0168 explicitly does not
  assume; an external worker behind a firewall is unreachable and would show nothing.

### Option 4 — write the forest back into the seed
- Good: no new surface at all.
- Bad: the seed is an input whose *content* names the file the worker reads (ADR-0202),
  so writing to it restarts the worker and empties the forest it was recording.
- Bad: it would make "what a forest starts from" and "what a forest holds" the same
  field, which is the confusion this record exists to end.

## Links

- builds on [ADR-0181](0181-ad-connector-mock-mode.md) (mock mode), [ADR-0193](0193-ad-mock-in-the-console.md)
  (the switch in the Console) and [ADR-0202](0202-atlas-manages-the-ad-mock-seed.md) (Atlas owns the seed)
- follows [ADR-0150](0150-preview-mail-provider-and-visible-incidents.md) (a worker reporting into the server's
  own view) and [ADR-0168](0168-connector-work-on-a-worker.md) (connector work on a worker)
- relates to [ADR-0206](0206-ad-as-a-console-connector.md) (a forest per LDAP URL)
