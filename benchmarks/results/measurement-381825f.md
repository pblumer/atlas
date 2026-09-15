# Can a capability's KPIs be computed without the exporter?

Evidence for one design question, not a performance baseline.

[ADR-0305](../../docs/adr/0305-business-capabilities-and-value-streams.md) filed a
business capability with the KPIs and SLAs it is held to, and carried an open
question: whether those are *computable* from what Atlas already keeps, at the
instance volumes this is aimed at, **without** the OpenSearch exporter
([ADR-0114](../../docs/adr/0114-opensearch-event-exporter.md)) that not every
installation runs. The record required the question be answered by measurement rather
than by argument. This is that measurement.

- Raw capture: [`measurement-381825f.txt`](measurement-381825f.txt)
- Benchmarks: [`../measurement_test.go`](../measurement_test.go)
- Machine: shared cloud VM, Intel Xeon @ 2.10 GHz, 4 cores, Go 1.26.0 — read the
  [caveats](README.md#what-a-baseline-is--and-is-not). Absolute values are
  illustrative; the **shapes** are what this document is for.

## The answer

**Yes, and with one condition.** Two of the four readings are free at any volume.
The other two are linear in the number of instances read, which makes them affordable
over a window and not affordable over all history — so the window is required rather
than optional.

The exporter is therefore an optimisation for unbounded historical analysis, not a
prerequisite for measuring a capability.

## What was measured

Each op is one *whole reading* over the population, so `ns/op` is the cost of
answering once. The reading is deliberately unwindowed: a window is the same scan
stopped earlier, so the unbounded walk is the upper bound on what any window costs.

| Reading | Data it needs | Where that lives |
|---|---|---|
| Outcome distribution | how often each end event fired | maintained per-element counter ([ADR-0080](../../docs/adr/0080-runtime-aggregate-counters.md)) |
| Cancellation rate | how often a token was cancelled on an element | maintained per-element counter |
| Cycle time | each instance's start and end | the instance record, walked through a completion-ordered index |
| Per-phase duration | the order of elements *within* each instance | the step trail ([ADR-0046](../../docs/adr/0046-single-process-step-replay.md)), one prefix per instance |

## Axis 1 — the population

Median of three repetitions at 20 iterations; 100k is a two-repetition override run.

| Instances | Outcome distribution | Cancellation rate | Cycle time | Per-phase duration |
|---:|---:|---:|---:|---:|
| 100 | 14.0 µs | 1.1 µs | 59.7 µs | 171 µs |
| 1 000 | 46.3 µs | 9.7 µs | 3.95 ms | 21.6 ms |
| 10 000 | 12.6 µs | 12.7 µs | 96 ms | 184 ms |
| 100 000 | 29.2 µs | — | 1.24 s | 2.86 s |

**The counters are flat, and that is the whole first half of the answer.** A
thousandfold population leaves them in microseconds — at 10 000 instances the outcome
distribution is *faster* than at 1 000, which is the LSM tree's file layout varying,
not the population. ADR-0080 claimed these were O(elements); this is that claim
measured rather than trusted.

**The two instance readings are linear once past warm-up.** Between the small
populations they look superlinear (100 → 1 000 costs 67× for a 10× population), which
is a cold store and a small absolute number, not a scaling law. From 10 000 to
100 000 the factors are 12.9× and 15.5× for a 10× population. Allocation counts are
linear throughout (≈5 and ≈6 per instance), which is what says the work itself is
proportional.

## Axis 2 — the length of the process

The population axis used a three-element process, the shortest this harness builds and
therefore the most flattering case per-phase duration will ever get. Cycle time reads
two fields off a record regardless of process length; per-phase duration reads a trail
with one entry per element activated. If that difference matters, it shows here.

Fixed 10 000 instances, two repetitions at 10 iterations, median:

| Script tasks | Cycle time (control) | Per-phase duration | Ratio |
|---:|---:|---:|---:|
| 1 | 83.3 ms | 197 ms | 2.4× |
| 10 | 97.2 ms | 207 ms | 2.1× |
| 30 | 148 ms | 284 ms | 1.9× |

**Per-phase duration does not scale with process length, and the hypothesis that it
would was wrong.** A thirtyfold longer process costs it 1.4× more, and the ratio to
its own control *falls*. Within one instance the cost is dominated by seeking to the
instance's prefix, not by walking the entries under it.

**The control moved too**, from 83 ms to 148 ms, although it reads the same two
fields per instance either way. That is the store growing — a thirty-element process
writes ten times the events of a three-element one — and it lands on both readings
alike. It is the reason the ratio narrows rather than the reason the numerator grows.

## What follows for the API

- **Outcome distribution and cancellation rate** are answerable unconditionally. They
  cost microseconds whatever has run.
- **Cycle time and per-phase duration** require a bounded window. At 100 000
  instances an unbounded reading costs 1.24 s and 2.86 s, and an endpoint that offers
  a number like that without a bound is offering a way to make somebody wait without
  telling them why. The completion-ordered index makes a window a cheaper scan rather
  than a filtered one.
- **Per-phase duration stays**, at roughly twice the cycle-time walk. It was carried
  into this measurement as the candidate for omission; the numbers say it is the same
  shape with a larger constant, not a different class of cost.
- **All four run off the run loop** ([ADR-0239](../../docs/adr/0239-off-loop-queries.md)).
  That is not a consequence of these numbers but a precondition they were taken under:
  a reading whose work grows with the population must not hold Atlas's single writer,
  whatever it costs.

## Reproducing

```bash
# the committed default: 100 / 1k / 10k, both axes
go test -run=^$ -bench='BenchmarkMeasurement' -benchmem -benchtime=20x -count=3 ./benchmarks/

# a larger population, without editing the source that produced the number
ATLAS_BENCH_POPULATIONS=100000 \
  go test -run=^$ -bench='BenchmarkMeasurement(CycleTime|PhaseDuration|OutcomeDistribution)$' \
  -benchmem -benchtime=5x -count=2 ./benchmarks/
```

The 100 000-instance run took about 21 minutes on the machine above, most of it
populating the store — which is setup, and outside the timer.
