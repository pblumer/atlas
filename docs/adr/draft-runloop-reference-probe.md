# ADR-DRAFT: A reference probe measures the single writer independent of traffic

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-11-11
- **Deciders:** Atlas maintainers
- **Open question:** whether the store part's absent-key read follows read amplification closely enough to warn ahead of a compaction backlog. It is reasoned from how Pebble resolves a point read, and measured only on a small store (about 5µs). No run against a store under a real compaction debt has shown how far it moves.
- **Question checked:** 2026-11

## Context and problem statement

The run loop is the single writer (invariant I3) and the gate every request passes
through (ADR-0239). [ADR-0142](0142-prometheus-metrics.md) exports how long its turns
wait and hold (`atlas_runloop_turn_wait_seconds`, `atlas_runloop_turn_held_seconds`), and
the writer's batch durations (`atlas_wal_sync_seconds`, `atlas_state_commit_seconds`).

All of these describe the traffic the server happens to carry. When they rise, there are
three possible causes: more load, a different mix of work, or a writer that has become
slower at the same work. The histograms cannot tell these apart. A slower writer is the
one an operator most needs to see early, because it degrades every request without any
change on the caller's side, for example through CPU throttling under a container
quota, a compaction backlog in the store, or a slower volume. The histograms also stay
empty while nobody dispatches, so the night before a degraded morning shows nothing.

The question is how to measure the writer in a way that does not depend on the traffic.

## Decision drivers

- The measurement must not depend on the load or the mix of work.
- It must not write anything: no event (I6), no state, no growth of the log.
- It must not allocate on the engine's batch path (I1) and must hold the writer only
  briefly (I3).
- It must locate a slowdown at least roughly: the host or the store.
- `/healthz` stays unconditional and `/readyz` keeps its meaning (ADR-0142 slice 7).
- It must fit future multi-partition operation without a redesign.

## Considered options

1. Derive everything in Prometheus from the existing metrics.
2. Time the readiness ping: an empty closure on a fixed cadence.
3. A reference probe: a fixed cadence, the waiting time of an empty hand-over, and fixed
   read-only work on the writer, timed in two parts.
4. A synthetic process instance: start a trivial deployed process on a cadence and time
   it through to the fsync.
5. Put the measurement, or a threshold on it, into `/healthz` or `/readyz`.

## Decision outcome

Chosen option: **3, a reference probe**, because it is the only option that measures the
writer's condition independent of traffic without writing anything.

- Every five seconds a goroutine hands the run loop a probe through `runloop.Loop.Probe`.
  It records how long the probe queued before the writer took it
  (`atlas_runloop_probe_wait_seconds`).
- On the writer the probe runs fixed work in two parts, each timed separately
  (`atlas_runloop_probe_work_seconds{part}`):
  - `store`: `state.Store.ReferenceRead`, sixteen point reads of two fixed keys. One is
    the applied log position, which every batch rewrites. The other is a meta key that
    is never written; a read of an absent key consults every memtable and every table
    whose bounds cover it, so its cost grows with read amplification.
  - `compute`: FNV-1a over a fixed 64 KiB buffer. Each step depends on the previous one,
    so the duration follows the CPU time the writer's goroutine actually receives.
- A probe that is not taken and finished within ten seconds counts as
  `atlas_runloop_probe_timeouts_total` and contributes no wait sample. A failed store
  read counts as `atlas_runloop_probe_failures_total` and contributes no store sample.
- A probe is not reported as a turn, so the turn histograms keep describing the work
  callers dispatched.
- The probe runs only when the server exports metrics (`WithoutMetrics` turns it off).

`runloop.Loop.Ping`, which `/readyz` uses, is now a probe with no work.

### Consequences

- **Positive:** a slower writer becomes visible as a rising probe duration under
  constant work, including while the server is idle. The two work parts locate it: if
  both rise, the host is the likelier cause; if only the store part rises, it is the
  store or the disk under it. The wait histogram gives a steady sample of the delay any
  request would have experienced.
- **Negative / trade-offs accepted:** the probe holds the writer for about 20µs every
  five seconds, well under a thousandth of its time. It does not exercise the write path
  (`applyToState`, the fsync, the state commit); those remain covered by
  `atlas_wal_sync_seconds` and `atlas_state_commit_seconds`, which do depend on traffic.
  The cadence is a constant, not an operator setting.
- **Follow-ups / risks to watch:** whether the absent-key read tracks a real compaction
  backlog (the open question); whether the runtime health route (ADR-0502) should also
  report the probe.

### How to read it

- Store part rising against its own past:
  `histogram_quantile(0.9, sum by (le) (rate(atlas_runloop_probe_work_seconds_bucket{part="store"}[15m])))`
  compared with the same expression `offset 7d`.
- Compute part, the same way with `part="compute"`.
- Writer reachability: `increase(atlas_runloop_probe_timeouts_total[5m]) > 0`.

Thresholds and alerts belong in Prometheus and Alertmanager, not in the server.

### Scale-out assessment

- **State ownership:** the probe owns no state. It reads from the store it is
  measuring, through the owning writer.
- **Partition routing / cross-partition communication:** none. Each writer is probed
  by its own process; nothing crosses a partition.
- **Durability / failover / idempotency:** nothing is written, so there is nothing to
  replicate or replay.
- **Query consistency:** not a query; the measurement is a per-process histogram.
- **Multi-partition migration seam:** with several writers per node, one probe per
  writer with a `partition` label. That label's values are bounded by configuration, so
  ADR-0142's cardinality rule holds. Standalone mode is unchanged.

## Pros and cons of the options

### Option 1: derive from existing metrics
- Good: no code. `rate(atlas_runloop_turn_held_seconds_sum[5m])` is the writer's
  utilisation and remains the right saturation signal.
- Bad: depends on the traffic, cannot separate load from degradation, and is empty while
  nothing runs.

### Option 2: time the readiness ping
- Good: trivial, and it samples the queue at a steady rate.
- Bad: an empty closure measures only how long the writer is occupied, not how fast it
  does the same work. It is part of the chosen option, not sufficient on its own.

### Option 3: reference probe (chosen)
- Good: constant work, no writes, two parts that locate the cause.
- Bad: covers the read side and the CPU, not the write path.

### Option 4: synthetic process instance
- Good: measures the whole path, including `applyToState` and the fsync.
- Bad: every probe is a fact in the log (I6). At one probe every fifteen seconds that is
  5,760 instances a day: WAL growth, history entries, exporter traffic, and counts in
  `atlas_active_process_instances` and the operations views that nobody started. It
  would need its own retention and its own exclusion from every statistic.

### Option 5: health or readiness endpoints
- Good: probes already reach them, without a credential.
- Bad: `/healthz` must not fail for anything a restart does not fix, and a restart does
  not make a slow writer fast. A latency threshold on `/readyz` would take the only node
  of a standalone installation out of traffic, turning slow into unavailable. `/metrics`
  is behind a credential (ADR-0198), and that is where a measurement belongs.

## Links

- extends [ADR-0142](0142-prometheus-metrics.md) (the metrics surface and its cardinality rule)
- relates to [ADR-0239](0239-off-loop-queries.md) (the loop as the gate of every request)
- relates to [ADR-0502](0502-admin-profiling.md) (runtime health and profiling)
- relates to [ADR-0198](0198-metrics-behind-the-boundary.md) (why `/metrics` needs a credential)
