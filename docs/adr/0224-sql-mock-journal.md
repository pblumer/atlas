# ADR-0224: A mockup run is visible, and it carries what the process bound

- **Status:** Proposed
- **Date:** 2026-09-02
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0221](0221-sql-mock-mode.md) gave the SQL workers a mock database: the same
resolved job, the same `Run`, against answers that live in the worker's memory. Its
amendment moved the switch and the seed into the Console, so an operator turns the
mockup on and pastes prepared answers instead of setting variables on a host.

What none of that gave anyone is a way to see the run. A mockup leaves two traces:

- the **prepared answers**, on the Databases card. They are an input. The worker reads
  them once at startup and never writes back, and the file is content-addressed
  precisely so that *changing* it restarts the worker — which would throw away the very
  run a write-back would be recording.
- the worker's **log**, reached under Operations › Workers by scrolling past everything
  else that worker did.

So "what did my process actually ask the database?" is answered by reading a log, and
the follow-up an operator always has — "and with which values?" — is answered by
reading it more carefully.

That question is not incidental to this mockup, it is the whole point of it. A mock
that refuses an unseeded statement is telling the operator exactly what to add to the
seed; today that message is in a log line, and the workflow is copy-from-log,
paste-into-Console.

## Decision drivers

- **A mockup exists to be tried out**, and a run nobody can look at is a poor thing to
  try a process against.
- **The journal lives in the worker**, which may be a supervised child on this host or
  an external process in a network this server cannot dial into (ADR-0168).
- **Nothing about a mock is durable**, and nothing about it may become durable: it is
  runtime state, never an event, never replayed (I4/I6).
- **The bound values are the useful part and the dangerous part**, and one decision has
  to serve both.

## Considered options

1. **The worker reports its journal; the Console shows it.** The same shape
   [ADR-0213](0213-ad-mock-directory-in-the-console.md) chose for the mock directory.
2. **The worker serves its own page.** An HTTP listener in the worker process.
3. **The server pulls from the worker.** An endpoint the engine polls.
4. **Leave it in the log** and teach the log line.

## Decision outcome

Chosen: **option 1**, unchanged from ADR-0213, because the problem is the same one and
already has this answer — and because the preview mail outbox (ADR-0150) established the
direction before either of them.

- `MockDatabase.Snapshot(max)` takes one consistent picture under one lock, bounded at
  the crossing rather than by a promise made elsewhere, keeping the **newest** when the
  bound bites: "what did it just do" is the question being asked.
- `Version()` is the journal's sequence, so a worker leasing jobs of other kinds does
  not post the same journal after every one of them.
- A SQL worker in mockup mode posts to `POST /api/v1/sql/mock-journal` after every job
  and once at startup, under `ATLAS_WORKER_ID` and behind its own token.
  `ATLAS_SQL_MOCK_VIEW_URL` says where; a supervised worker is handed it at spawn, and
  **only while the mockup is on**.
- The server keeps the newest snapshot per worker in a `sqldb.MockJournalView` — memory,
  its own lock, off the run loop, bounded to eight workers — and serves it at
  `GET /api/v1/sql/mock-journal`, admin-only.
- Operations › **Mock database** renders one card per worker: every statement in order,
  the values bound to it, and the refusals in red with their reason.

**A report is an observation, never part of the work.** The statement it describes has
already been answered and the job has already settled, so a report that cannot be
delivered is logged (`sql_mock.report_failed`) and dropped. The version is not recorded
as sent either, so the next statement re-sends the whole journal and a Console that was
unreachable catches up by itself rather than staying wrong.

### Where this departs from the mock directory, and it is not cosmetic

Two differences, and both change what the record can promise.

**There is no state to show.** The AD view answers "what is in the directory now", so it
draws a tree. This mock holds nothing: it answers statements and executes none, so an
`INSERT` changes nothing a later `SELECT` would see (ADR-0221). There is no "now" to
draw. What there is, is the sequence — which is why the page is a list of statements and
not a table browser, and why the journal here is *the* view rather than a companion to
one.

**The bound values travel, and ADR-0213's central safety claim does not transfer.** That
record could say plainly: *no password travels, because none is stored* — the AD mock
validates a set-password and then keeps nothing. This one records what a process bound,
and a process under test binds whatever it binds. A password hash on its way into a
table is a bound parameter like any other, and **nothing here can tell it from an id**.

That was put to the decider as a choice between showing the values, redacting them to a
count, and hiding them behind a click. Showing them was chosen, because the redacted
version loses the case the view exists for: "why did that lookup find nobody" is almost
always a question about what was bound, and a journal that shows `SELECT … WHERE kuerzel
= @p1` without the value answers the second question and not the first. Values render as
JSON, so a string `"7"` and the number `7` are distinguishable — which is frequently the
answer.

What that costs is paid for in gates rather than in redaction:

- the read is **admin-only**, the same posture the prepared answers already have;
- a worker is handed the report URL **only while the mockup is on**, so a worker talking
  to a real database reports nothing — a journal view fed by a live one would be the
  worst possible thing for that screen to be;
- it is **memory on both sides**, gone on restart, in no event and no backup.

This is a weaker guarantee than ADR-0213's and is stated as one rather than left to be
inferred from a family resemblance.

### Consequences

- **Positive:** the loop a mockup is for closes inside the Console — run the process,
  read what it asked, paste the statement it could not answer into the prepared answers,
  run it again. The refusals are the entries an operator comes for, so they are coloured
  rather than merely listed.
- **Negative / trade-offs accepted:** one HTTP round trip per SQL job in mockup mode
  (skipped when the journal has not changed), and a second copy of the journal in the
  server's memory. Both are bounded, both exist only in mockup mode, and neither touches
  the run loop. The view can be one report behind — it is a report, not a query — which
  the "reported at" stamp makes visible. And the values are on a screen: admin-only, but
  on a screen.
- **Follow-ups / risks to watch:** if a mockup run ever needs to be kept, that is an
  export and not a store. If the values turn out to be too much for some installation,
  the redaction option this record rejected is the thing to revisit, and it should be a
  setting rather than a second view.

## Pros and cons of the options

### Option 1 — the worker reports, the Console shows (chosen)
- Good: works for a supervised worker and for one in a network the server cannot reach.
- Good: the pattern, the direction and the posture are the outbox's and the mock
  directory's, already reviewed.
- Bad: the view is a report and can lag; the values leave the worker's process.

### Option 2 — the worker serves its own page
- Good: nothing leaves except to whoever asks.
- Bad: a listener, a port and an auth story per worker, for a page nobody would find.

### Option 3 — the server pulls
- Bad: does not work at all for a worker in a network the server cannot dial into,
  which is the deployment ADR-0168 exists to support.

### Option 4 — leave it in the log
- Good: nothing to build.
- Bad: it is what there is today, and the copy-from-log workflow is exactly what the
  Console exists to remove.

## Links

- follows [ADR-0213](0213-ad-mock-directory-in-the-console.md) in shape, and departs
  from its safety claim where the data differs
- extends [ADR-0221](0221-sql-mock-mode.md), whose mockup this makes visible
- rides [ADR-0150](0150-preview-mail-provider-and-visible-incidents.md)'s direction:
  the worker posts, the server never dials
- honors [ADR-0168](0168-connector-work-on-a-worker.md), which is why the server cannot
  pull
