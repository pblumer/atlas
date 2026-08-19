# ADR-0142: Operational metrics over a Prometheus endpoint

- **Status:** Accepted
- **Date:** 2026-08-18
- **Deciders:** Atlas engine team

## Context and problem statement

Atlas has no metrics. Everything an operator can observe is either a JSON read of
current state (`GET /api/v1/stats`, `GET /api/v1/checkpoints`) or a line in the
process log. There is no way to graph throughput, alert on a stalled exporter, or
answer "was the engine slow at 03:00 last night?" — the questions that decide whether
a system is operable, and the ones a *developer preview* has to answer before anyone
runs it for real.

The v0.2.0 programme calls for a specific minimum set: commands and events processed,
batch throughput and size, queue depth, fsync and state-commit duration and failures,
active instances/jobs/timers/messages/incidents, job activation and completion
counters, recovery duration and replayed event count, checkpoint age/position/duration
/failure, retained WAL bytes, and exporter lag.

Three constraints make this architecture-sensitive rather than a matter of sprinkling
counters:

- **Invariant I1 — no hot-path allocation.** The engine's batch loop is the throughput
  path. Instrumentation that allocates per command (a label-map lookup, an interface
  box, a `time.Since` escaping to the heap) taxes the very thing it measures.
- **Invariant I2 — durable before visible.** A metric is a *claim about the world*. If
  a counter says 1,000 events were processed and only 900 are in the fsynced WAL, the
  metric is lying in exactly the direction that matters after a crash.
- **Cardinality.** A Prometheus label whose value is an instance key, a job key, a
  correlation key, or a URL turns one metric into unboundedly many time series and
  takes the scrape target down with it.

So this ADR settles the contract before any counter is added, and the work lands in
slices behind it.

## Decision drivers

- **Cost-free on the hot path** — an instrumented engine must benchmark like an
  uninstrumented one (I1).
- **Never claim more than is durable** (I2).
- **Bounded cardinality by construction**, not by convention.
- **Extend the existing operational surface** rather than growing a parallel one: the
  same server, alongside `/healthz` and `/api/v1/checkpoints`.
- **CGO-free, few dependencies** (ADR-0010).
- **Standard exposition** — an operator should point an existing Prometheus at it and
  reuse existing dashboards and alerting, not learn an Atlas-specific format.

## Considered options

**A. Exposition mechanism:**

1. **`prometheus/client_golang`** — the reference client: registry, exposition
   handler, counters/gauges/histograms.
2. **A hand-rolled text encoder** over our own atomic counters — no dependency.
3. **OpenTelemetry metrics SDK** with a Prometheus exporter.

**B. Where the numbers come from:**

1. **Scrape-time collection from durable state** — a `Collector` reads the state
   store, the checkpoint root and the WAL directory when Prometheus scrapes.
2. **Push from the hot path** — the engine increments counters as it works.
3. Both, for the metrics each suits.

## Decision outcome

**Mechanism: `prometheus/client_golang` (A1), on a dedicated registry.**
**Sources: both (B3), split by what a metric actually is.**

### Why the reference client

`github.com/prometheus/client_golang` is **already in this module's graph** — Pebble
depends on it, so it is already compiled into every Atlas binary. Using it directly
promotes an existing indirect dependency rather than adding one: no new transitive
tree, no CGO, no build-tag surprises. Against that, a hand-rolled encoder would mean
reimplementing histogram bucketing and the exposition format's escaping, `_bucket` /
`_sum` / `_count` conventions and `# HELP` / `# TYPE` blocks — work that is easy to get
subtly wrong and impossible to get *differently* right, since the only correct answer
is the one every scraper already expects. The OpenTelemetry SDK is a heavier
dependency for the same result, and the programme places traces explicitly *after* the
metric contract is stable; adopting its metrics SDK now would decide the tracing
question by accident.

Atlas uses its **own registry**, not `prometheus.DefaultRegisterer`. A package-level
default is global mutable state shared with every library in the process: it makes
what is exported depend on the import graph, and it makes tests order-dependent. An
owned registry means the exported set is exactly what Atlas registered, and a test can
scrape a server without a global.

Go runtime and process collectors are **opt-in, not automatic**, for the same reason:
what appears at `/metrics` should be a decision, not a side effect.

### Two sources, by what a metric is

A **gauge over durable state** answers "what is true right now" — the applied log
position, the newest checkpoint's age, retained WAL bytes, the exporter's watermark.
These are read at **scrape time** from the store, the checkpoint root, and the WAL
directory. This is not merely convenient: it satisfies I2 *by construction*, because
there is no in-memory number that could run ahead of what is on disk. It also costs
nothing when nobody scrapes.

A **counter or histogram over work done** answers "what has happened" — commands and
events processed, batch sizes, fsync durations. These cannot be recovered from state,
so the engine increments them as it works. They are subject to the hot-path rules
below, and to one durability rule: a counter describing durable work is incremented
**after** the durability point it describes, never before. A crash then loses the
count of work that was itself lost — the metric and the log agree.

Scrape-time collection must never scan. A collector that counts by walking every
active instance turns a 15-second scrape into a repeated full scan of the runtime set,
which is the cost ADR-0080/0085 exist to avoid. A number that has no O(1) maintained
counter behind it does not become a metric until it does.

### Hot-path rules (I1)

1. **Every metric handle is resolved once, at construction.** A labeled metric is
   resolved to its concrete child (`WithLabelValues`) when the registry is built and
   stored as a field. The hot path touches a `prometheus.Counter`, never a `*Vec` — no
   map lookup, no label slice, no allocation.
2. **No per-command timing that escapes.** Durations are measured around a *batch*,
   not a command, and observed into a pre-resolved histogram.
3. **The proof is a benchmark**, not a review: the instrumented engine is benchmarked
   against the uninstrumented one, `-benchmem`, and a regression in `allocs/op` on the
   steady-state path is a failure. Programme B's harness already reports it.

### Cardinality

Labels are an **allowlist**, and the allowed dimensions are ones whose value set is
fixed by the code, not by the data: the partition id, a small closed enum where a metric
genuinely has variants (e.g. an outcome of `success` / `failure`), and the `le` /
`quantile` labels a histogram or summary generates from bucket boundaries chosen in the
source. The rule is that a label's values must not come from the data — not that no
label may exist.

Never a label: instance key, element instance key, job key, correlation key, message
name, process id, definition key, element id, user, tenant, URL, or any other
value an operator or a model can invent. A per-definition breakdown is a *query over
the API*, not a time series — `GET /api/v1/processes` already answers it, bounded and
paginated, and the API can say "1,400 definitions" where a scrape target can only fall
over.

### The endpoint

`GET /metrics`, on the existing server next to `/healthz` — same process, same port,
no parallel surface. It is **unauthenticated**, like `/healthz` and unlike the JSON
API, because that is what a Prometheus scrape expects and because the cardinality rule
means the exposition contains only aggregates: no instance data, no variable payloads,
no identifiers. It is nonetheless an operational surface, so it can be turned off, and
the deployment guidance is the same one `/mcp` already carries: put a reverse proxy in
front of anything you expose beyond the host.

## Consequences

- **Positive:** standard exposition an existing Prometheus scrapes with no adapter; no
  new dependency tree; durable-state gauges that cannot over-report by construction; a
  cardinality rule that is checkable in review because it names what is forbidden.
- **Negative / trade-offs accepted:** `client_golang` moves from an indirect to a
  direct dependency, so its API is now ours to track. `/metrics` exposes aggregate
  throughput and instance counts to anyone who can reach the port. Metrics that lack an
  O(1) source are deferred rather than approximated.
- **Risks to watch:** a later metric added with a `*Vec` lookup on the hot path would
  silently undo rule 1 — the allocation benchmark is what catches it, so it must stay
  green rather than be regenerated.

## Implementation status

This ADR is **not implemented in one change**. The slices:

1. **Landed** — this ADR, the `metrics` package (own registry, the exposition
   handler), the `/metrics` endpoint, and the **durability metrics**: applied log
   position, checkpoint count/position/age and last-pass outcome, retained WAL segments
   and bytes, and the exporter watermark and lag. All of them are scrape-time reads of
   durable state, so nothing here touches the engine or its hot path, and the I2 rule
   holds trivially. They also reuse the collectors ADR-0131 slice 8 already built for
   `GET /api/v1/checkpoints` rather than introducing a second way to read the same
   facts.
2. **Landed** — **engine hot-path counters and histograms**. The batch loop reports each
   committed batch through a small `engine.Metrics` interface — commands consumed, events
   made durable, queue depth after the batch, and the fsync and state-commit durations —
   and the server maps those onto pre-resolved Prometheus handles. The engine never
   imports Prometheus: it hands out plain numbers, so the exposition format, the buckets
   and the naming stay out of the partition's writer.

   Three properties carry it, each with a test rather than an assertion in prose:
   - **Reported only once durable.** `BatchCommitted` is called after the state commit;
     a batch whose fsync or commit fails is reported as a *failure* and never counted as
     committed. A counter that ran ahead of the log would claim work a crash then loses.
   - **Free on the hot path.** One interface call per *batch*, passing a struct by value.
     `engine.TestReportBatchNoAlloc` pins the call shape and
     `api.TestEngineMetricsReportNoAlloc` pins the real Prometheus implementation — the
     latter is what fails if a future metric is added with a per-batch `WithLabelValues`.
     A nil `Metrics` means the loop does not even read the clock.
   - **No zero observations.** A batch that produced no events had nothing to fsync and
     nothing to commit, so its durations are not observed; feeding a latency histogram
     zeros would report a p99 no real write ever achieved. Its commands still count.

   The overhead is measured, not asserted: `BenchmarkInstrumented` against
   `BenchmarkUninstrumented` in `benchmarks/` shows identical `allocs/op`, with `ns/op`
   inside the fsync's own run-to-run spread.
3. **Landed (partly)** — **runtime population gauges**: `atlas_active_process_instances`
   and `atlas_live_element_tokens`, summed at scrape time from the maintained
   per-definition counters ADR-0080 already keeps (`Store.TotalActiveInstances` /
   `TotalLiveTokens`), never by walking the runtime set.

   The scan-avoidance rule this ADR set turned out to need a qualification, found by
   measuring rather than reasoning. The per-definition counters are Pebble **merge**
   counters, so a read also folds in whatever operands have not been compacted yet: right
   after a burst of starts the sum costs O(recent writes), not O(definitions). A flush
   collapses them, after which it is flat — 2,000 running instances read as fast as 100 —
   and flushes happen on their own, with the ADR-0131 checkpoint cadence forcing one every
   few minutes. Even un-compacted the sum stays cheaper than the scan it replaces.
   `BenchmarkTotalActiveInstances` measures all three states so the claim is checkable
   rather than asserted. So the rule stands, read correctly: *a metric needs a maintained
   counter, and "maintained" means amortized-O(1), not O(1) at every instant.*

   Jobs, timers, message subscriptions and incidents are **deliberately not here**. They
   have no maintained counter at all — only a full scan — and shipping them as scans would
   break the rule above rather than bend it. They need their own durable merge counters
   inside `applyToState`, with a backfill for existing stores and recovery tests, which is
   a change to durable state and so its own slice.
4. **Landed (partly)** — **durable counters for open work**: `atlas_open_jobs`,
   `atlas_pending_timers` and `atlas_message_subscriptions`, engine-wide signed merge
   counters maintained inside `applyToState` (one key per kind), backfilled once at
   `Open` for stores written before they existed, and recovery-tested — a rebuild from
   the log alone lands on the same numbers the live run produced.

   The correctness condition is **pairing**: an increment on the event that creates the
   entity, a decrement on the event that removes it, and *nothing* on an event that
   merely re-puts one. Failing a job re-puts it with a decremented retry count and
   assigning re-puts it with an assignee; the job was already open and still is, so
   neither may move the count. Counting every put would inflate the gauge on exactly the
   processes an operator is most likely watching — the ones that are retrying.

   **Incidents are not countable this way**, which is why they are absent. An incident is
   removed by its own `IncidentResolved` event *and* by the unconditional, idempotent
   `DeleteIncident` that runs whenever any element terminates — a delete with no event of
   its own, on the hottest path in the engine, where telling "there was one" from "there
   was not" would need a read. Counting them needs an explicit resolution event for that
   path, which changes what the log contains and so belongs in its own change; it is
   arguably a log-fidelity improvement in its own right, since today an incident can
   vanish with nothing in the log saying so.
5. **Landed (partly)** — **job lifecycle counters**: `atlas_jobs_created_total`,
   `_completed_total`, `_failed_total` and `_canceled_total`, counted from the batch's
   own records after it is durable and carried on `BatchStats`, so they inherit slice 2's
   durability ordering and its single call per batch. The count walks records already in
   cache into locals, so it allocates nothing, and it runs only when metrics are attached.

   Counting them *here* rather than in `applyToState` is the point: `applyToState` runs
   identically on replay (invariant I4), so a counter incremented there would be
   double-counted by every recovery.

   Four pre-resolved counters, not one labelled by outcome. The label's values would be a
   closed enum and so permitted, but resolving a child per batch is what rule 1 forbids,
   and four fields cost nothing.

   **Activations, lease expiries and timeouts are absent**, not zero: the lease-based
   worker protocol (ADR-0007, programme F) does not exist yet, so there are no events to
   count. A permanent zero on a timeout counter reads as "nothing is timing out", which
   would be true and misleading. They land with that protocol.
6. Recovery duration and replayed event count, exported once recovery can report them
   without changing its shape.
7. Readiness semantics: a readiness probe distinct from liveness that fails while
   startup recovery is incomplete or a required local store cannot operate.
8. Structured log event names and essential context, and only then OpenTelemetry
   traces — after the metric contract has settled, with sampling and export kept off
   the single-writer path.

## Links

- builds on ADR-0010 (CGO-free, few dependencies), ADR-0005 (durable-before-visible),
  ADR-0001/0002 (event sourcing, single writer)
- reads what ADR-0131 (recovery checkpoints and WAL compaction) publishes, and reuses
  ADR-0114's (OpenSearch export) high-water mark and ADR-0115's (history retention)
  safe position as the exporter-lag inputs
- respects ADR-0080/ADR-0085 (maintained counters, no full scans) — a metric with no
  O(1) source waits for one
- ADR-0007 (job worker protocol) supplies the job counters in a later slice
