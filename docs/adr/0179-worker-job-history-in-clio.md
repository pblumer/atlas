# ADR-0179: A worker's job history lives in clio, not in Atlas

- **Status:** Accepted
- **Date:** 2026-08-21
- **Deciders:** Atlas maintainers

## Context and problem statement

The Workers view can now show what a worker ran: each job's type, element, instance,
duration, outcome, the variables handed in and the ones returned, and — the part that
had nowhere else to be — the worker's own error message on a failure that still has
retries left, which raises no incident and therefore appeared nowhere an operator
could reach.

It is a bounded ring in the server's memory, like the mail outbox (ADR-0150): the
newest fifty jobs per worker, emptied by a restart. That is right for "what is
happening now" and useless for the question an operator actually brings a day later —
"what failed on Tuesday, and what was it given". A restart of Atlas is exactly the
event most likely to *precede* that question.

So: where does a job history that outlives the process live, with a retention of a
day or two, without the engine paying for it?

## Decision drivers

- **A job must never wait on telemetry.** ADR-0168 moved connector work off the loop
  because a slow SMTP host was stalling other work. Recording what a job did must not
  reintroduce the same coupling in a smaller shape.
- **Retention is already a promise.** ADR-0115 deletes finished instances after their
  age. Anything holding those instances' variables independently would outlive the
  data it describes.
- **The single-binary posture** (ADR-0011) limits how much operator machinery any
  answer may add: a second thing to back up, compact and restore is a real cost.
- Atlas already speaks clio (ADR-0036), with a managed connector, an endpoint and a
  credential in the connector store (ADR-0041).

## Considered options

1. A sidecar store (`api/sidecar`), as connectors and job types use.
2. A second Pebble instance beside the state store.
3. The event log — job runs as events, applied to state.
4. clio: append each settled job run to an operator-named clio connector.
5. Nothing durable; the memory ring is the whole answer.

## Decision outcome

Chosen option: **"clio"**, because a job history *is* a stream of events, an event
store is the thing built to hold one, and Atlas already talks to this one.

**What it removes.** No new storage in Atlas, so no new invariant surface, no second
backup path, no compaction. Retention stops being another Atlas flag and becomes the
operator's own policy in their own store — which is also the only place it can
honestly live, since how long job telemetry is kept is a question about the
operator's data protection posture, not about a workflow engine.

**What it costs, and the rule that bounds it.** The export must never slow a job
down. A settled run is handed to a bounded buffer with a **non-blocking** send from
the run loop; a goroutine drains it and does the network write outside. A clio that
is slow or gone fills the buffer, and a full buffer **drops and counts** — an engine
that stalls to record what it did is worse than one with a gap in its telemetry, and
the gap is reported so nobody reads "no failures" as "nothing failed".

**Opt-in, by naming a connector on the command line.** `--worker-history <name>`, and
`--worker-history-scope all|failed` for operators who want the record without the
volume. An operator who names none keeps the ring and nothing else changes — the
zero-configuration server stays zero-configuration.

**The ring stays.** It needs no configuration and it answers when clio does not.
The console shows it first and appends the history under it, so a store that is
unreachable costs the always-available half nothing.

**Reading it back.** clio reads oldest-first from a cursor, so the console takes a
bounded read and returns its newest page, saying when it hit that bound. Past that
the answer is a clio query, which is a better tool for "every mail failure this
month" than a dialog will ever be — and is the second reason for choosing an event
store over a table: the questions worth asking of a history are not the ones a fixed
view can enumerate.

### Consequences

- **Positive:** the history outlives restarts and is queryable with the operator's own
  tools. Atlas gains no storage, no retention setting, and no new failure mode on the
  hot path. Aggregate questions — which task fails most, how long a mail send takes —
  become answerable, which the ring could never do.
- **Negative / trade-offs accepted:** the durable half is only available to operators
  who run clio, so the product has two tiers of answer. Job variables leave Atlas for
  another service, which is a data-flow decision an operator must make deliberately —
  hence opt-in, and hence naming the connector rather than defaulting to one. One
  HTTP append per settled job: fine at the rates a single engine produces, and the
  drop counter is what says when it is not.
- **Follow-ups / risks to watch:** a batched append if the per-event write proves too
  chatty; a reduce spec so the console reads a prepared summary instead of a windowed
  scan; and the question of whether the ring and the history should ever disagree
  about a job — today they cannot, because the ring is written first and the history
  is derived from the same row.

## Pros and cons of the options

### Option 1 — a sidecar store
- Good: already exists, already backed up with the data directory.
- Bad: one fsync'd JSON file per record and a full directory read per query. On the
  lease path at job rates that is not a store, it is a brake. And it inherits the
  retention problem in full.

### Option 2 — a second Pebble instance
- Good: fast range reads per worker, ordered by time; deletion by range.
- Bad: a second store to back up, compact and restore, for a debugging window. Still
  the operator's retention problem, now with a second knob.

### Option 3 — the event log
- Good: durable by construction, already replicated by every existing mechanism.
- Bad: the resolved input variables are in no event today — they are produced at lease
  time — so this would put process data into the WAL to be replayed forever and read
  twice on recovery. It would also make a debugging aid part of the durable record
  (I4/I6), which is precisely the line the mail outbox and the worker counters are on
  the other side of.

### Option 4 — clio
- Good: the right shape of store for the right shape of data; no new Atlas storage;
  retention and querying become the operator's, in a system they already run.
- Bad: only available where clio is; job variables cross a service boundary; an
  append per settled job.

### Option 5 — nothing durable
- Good: nothing to build; no data leaves the process.
- Bad: leaves the question that motivated the view unanswered one restart later.

## Links

- the memory ring this extends, and the discipline it follows: [ADR-0150](0150-preview-mail-provider-and-visible-incidents.md)
- the clio connector and its managed configuration: [ADR-0036](0036-clio-connector.md), [ADR-0041](0041-connector-management-and-secret-store.md)
- the "never stall the loop for an outbound call" rule this obeys: [ADR-0168](0168-connector-work-on-a-worker.md)
- the retention promise this deliberately does not undercut: [ADR-0115](0115-history-retention-hard-delete.md)
- the Workers view it appears in: [ADR-0157](0157-worker-processes-supervision-and-console.md)
