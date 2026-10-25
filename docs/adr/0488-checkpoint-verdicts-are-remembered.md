# ADR-0488: A checkpoint's verdict is remembered, not re-read on every scrape

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-10-25
- **Deciders:** Atlas maintainers

## Context and problem statement

The `/metrics` durability gauges (ADR-0142) report how many recovery checkpoints are on
disk and the position and age of the newest one that still verifies (ADR-0131). They read
it at scrape time through the same helper `GET /api/v1/checkpoints` uses, and that helper
verifies every checkpoint: it re-hashes every byte of its state files against the
manifest.

On a store of 23 GiB, with the default of three checkpoints kept, one scrape therefore
reads up to three times the store. A Prometheus scraping every fifteen seconds asks for
that four times a minute; the scrape outlives its timeout long before the read ends, so the
work is done for nobody, and a second scrape starts the same read beside the first.
ADR-0382 moved whole-store reads off the run loop; this one was never on it, but a read
of that size on a cadence is a load the disk carries whether or not anybody looks at the
metric.

The fact the gauge reports changes rarely. A published checkpoint is renamed into place
complete, and nothing writes into it afterwards (ADR-0131).

## Decision drivers

- A scrape must cost what the answer costs, not a read of the store.
- A checkpoint whose files were replaced, truncated or rewritten must stop being credited
  at the next scrape: crediting it tells an operator a restart is shorter than it is.
- Rot that changes no file metadata must still be reported, within a bound.
- The operator's explicit question — `GET /api/v1/checkpoints`, "does it still verify" —
  keeps its meaning.

## Considered options

1. **Keep verifying on every scrape.**
2. **Remember each checkpoint's verdict, keyed by a fingerprint of its files, and renew it
   daily** (chosen).
3. **Report the newest checkpoint by its manifest alone,** without verifying.
4. **Verify in the background on a timer** and let scrapes read the result.

## Decision outcome

Chosen option: **2**.

- The verdict for each checkpoint is kept with a **fingerprint** of its state files —
  name, size and modification time, from a directory walk that opens no file — and the
  server clock's time of the verification. A scrape uses it when the fingerprint is
  unchanged and the verdict is younger than **24 hours**; otherwise it verifies, and
  remembers the new verdict. A fingerprint that cannot be taken is never trusted.
- A checkpoint **this server publishes** is remembered as verified when it is published,
  because the commit has just hashed exactly those files. Without that, every new
  checkpoint — one per cadence — would be read again by the next scrape. A pass that finds
  its position already published hashes nothing and renews nothing, so the daily
  verification still comes due on an idle server.
- One mutex holds the verdicts and is held across a verification, so concurrent scrapes
  of a cold cache wait for one read instead of starting one each. Verdicts of checkpoints
  no longer on disk are forgotten.
- `GET /api/v1/checkpoints` still verifies afresh, and `?verify=false` still reads the
  manifests only (ADR-0486).

### Consequences

- **Positive:** a scrape reads file metadata, not the store; after a restart each kept
  checkpoint is read once; a replaced or rewritten file is reported at the next scrape,
  as before.
- **Negative / trade-offs accepted:** rot in place that changes neither size nor
  modification time is reported up to a day late; the verdicts live in memory and start
  over at every restart; a scrape of a cold cache still takes as long as one verification,
  and holds a checkpoint pass that finishes at the same moment until it is done.
- **Follow-ups / risks to watch:** compaction's cut (`engine.Processor.CompactionCut`)
  verifies the newest checkpoint on every pass and could use the same verdicts; the
  checkpoint commit itself hashes the whole store on every cadence, which is the cost
  ADR-0131 accepted and the next thing to measure on a large store.

## Pros and cons of the options

### Option 1 — verify on every scrape
- Good: always the current answer.
- Bad: reads the store per checkpoint per scrape; on a large store the scrape times out
  and the read goes on.

### Option 2 — remembered verdict with fingerprint and daily renewal (chosen)
- Good: metadata-cheap scrapes; replaced files caught at once; rot caught within a day.
- Bad: a day's delay for silent rot; state in memory.

### Option 3 — manifest only
- Good: cheapest.
- Bad: credits a damaged checkpoint indefinitely — the case the gauge exists to report.

### Option 4 — background verification on a timer
- Good: scrapes never wait.
- Bad: a second loop and its scheduling, and the same daily bound as option 2 without
  catching a replaced file at once.

## Links

- amends ADR-0142 (the durability gauges' checkpoint position and age)
- relates to ADR-0131 (published checkpoints are immutable), ADR-0382 (whole-store reads),
  ADR-0486 (the dashboard reads checkpoints without verifying them)
