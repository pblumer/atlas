# Atlas benchmarks

Reproducible performance harness for the Atlas engine — work programme **B**
("reproducible benchmark baseline") of the v0.2.0 *Proof of Reliability &
Performance* initiative.

The goal is **evidence, not marketing**: a way to measure the engine the same way
twice, on a named machine at a named commit, so a change's effect on throughput,
allocations, and storage is checkable rather than asserted. Any number produced
here is specific to one machine and one commit. It is **not** a universal product
claim, and this harness must **not** be used to compare Atlas against another
engine until the workloads are demonstrably equivalent.

**Published baseline:** [`results/`](results/) holds the first committed, reproducible
baseline — a machine-labelled raw capture plus a `benchstat` summary. See
[`results/README.md`](results/README.md) and the latest
[`results/baseline-5b1b9f2.md`](results/baseline-5b1b9f2.md).

## What this measures (and what it does not, yet)

Two axes, three profiles:

- **Where** the workload is driven —
  - *pure engine*: the processor, WAL, and state store driven directly, no HTTP;
  - *end-to-end HTTP/API*: the same engine driven through `api.Server`'s HTTP
    handlers (served in-process via `ServeHTTP`, so TCP/client cost is excluded — it
    isolates the API layer over the engine, not a network round-trip).
- **Durability** of the WAL —
  - *durable*: WAL segments on real disk, so the **group-commit `fsync` on every
    batch** is a real disk `fsync`; the per-op time is dominated by that latency,
    which is the point of the durable numbers.
  - *in-memory*: the same WAL and state store on a RAM-backed tmpfs. The state store
    already commits with `pebble.NoSync`, so the WAL `fsync` is the only durability
    cost; on tmpfs it hits RAM and returns almost immediately. Comparing an
    in-memory benchmark to its durable twin **splits the per-instance cost into
    engine CPU (what remains) and disk-`fsync` latency (the difference)**. It is a
    measurement profile, not a durability mode — no claim that Atlas runs without
    `fsync` in production. The in-memory benchmarks skip when no tmpfs is available
    (set `ATLAS_BENCH_TMPFS` to a RAM-backed dir to force one).
- **Steady state vs recovery** — most benchmarks measure a warm engine's
  throughput; the recovery benchmarks instead measure **startup/recovery**: how fast
  a fresh engine rebuilds state by replaying the WAL from genesis (there is no
  checkpoint yet, so recovery replays everything).
- **Throughput vs latency distribution** — `ns/op` is a mean, which the skewed
  `fsync` latency understates. The latency benchmarks sample each operation and
  report **P50/P95/P99 (and max)**, so the tail a client actually sees is visible.

Deliberately **deferred** to later programme-B slices (see *Deferred* below):
a loopback-socket (real TCP) HTTP variant, service-task
completion over HTTP, an in-memory HTTP profile, and published baseline result files.

## Workloads

Engine-level (built with the public `compiler.NewBuilder`, so the process compiles
once and only execution is measured):

| Benchmark | Programme workload | Shape |
|---|---|---|
| `BenchmarkLinearSelfCompleting` | #1 minimal self-completing linear | Start → Script → End; runs straight through, no worker |
| `BenchmarkServiceTaskLifecycle` | #2 service-task create/activate/complete | Start → ServiceTask → End; parks on a job, an in-process worker completes it |
| `BenchmarkVariableGatewayRouting` | #3 mixed variables + gateway routing | Start → XOR(`amount > 100`) → Script → End; a start variable picks the branch |

End-to-end HTTP/API (`POST /api/v1/processes/{key}/instances` per iteration,
against a deployed BPMN model). Each mirrors an engine-level workload's *shape*, so
subtracting the two isolates the API-layer cost — the same `events/op` and `walB/op`
with extra `allocs/op` and `B/op` from request decode, routing, the run-loop handoff,
and response encode:

| Benchmark | Mirrors | Path |
|---|---|---|
| `BenchmarkHTTPLinearCreate` | `BenchmarkLinearSelfCompleting` | create → self-complete, over HTTP |
| `BenchmarkHTTPVariableGatewayCreate` | `BenchmarkVariableGatewayRouting` | create with a start variable → gateway → self-complete, over HTTP |

In-memory (RAM-backed tmpfs) twins of the engine-level workloads — same workload,
same `events/op`/`walB/op`/`allocs/op`, `fsync` on RAM instead of disk. The `ns/op`
gap to the durable twin is the disk-`fsync` cost:

| Benchmark | Durable twin |
|---|---|
| `BenchmarkInMemoryLinearSelfCompleting` | `BenchmarkLinearSelfCompleting` |
| `BenchmarkInMemoryServiceTaskLifecycle` | `BenchmarkServiceTaskLifecycle` |
| `BenchmarkInMemoryVariableGatewayRouting` | `BenchmarkVariableGatewayRouting` |

Recovery (startup/recovery axis, programme workload #5). Each populates a WAL with
`b.N` instances (batched into few fsyncs, excluded from timing) and measures the WAL
replay into a fresh, empty state store — so `ns/op` is the per-instance recovery cost
and the summary's derived instances/sec is the recovery rate:

| Benchmark | Recovers |
|---|---|
| `BenchmarkRecoveryLinearCompleted` | `b.N` self-completing instances (full lifecycle replayed, land completed in history) |
| `BenchmarkRecoveryServiceTaskParked` | `b.N` instances parked on an activatable job (active instances + jobs restored) |

Latency distribution (`Benchmark­Latency…`) — the same create-to-completion work as
their throughput twins, but each op is timed and the run reports P50/P95/P99/max
instead of only the mean. They intentionally **do not** report `events/op`/`walB/op`
(see the throughput twin for those), so those columns are blank in the summary table;
the percentiles show in the raw `-bench` output and via `benchstat`.

| Benchmark | Layer |
|---|---|
| `BenchmarkLatencyHTTPLinearCreate` | end-to-end HTTP create-to-completion latency |
| `BenchmarkLatencyEngineLinearSelfCompleting` | pure-engine per-instance latency (subtract to attribute the API-layer tail) |

## Running

```bash
# Quick local run (a few seconds), memory stats on. A fixed -benchtime keeps it
# quick: without it the recovery benchmarks scale their instance count to ~1s of
# replay and each takes ~10s.
go test -run=^$ -bench=. -benchmem -benchtime=200x ./benchmarks/

# Reproducible run: fixed iteration count, several repetitions, saved raw output.
# The raw file is the machine-readable record (the format benchstat consumes).
go test -run=^$ -bench=. -benchmem -benchtime=2000x -count=6 ./benchmarks/ \
  | tee benchmarks/raw.txt

# Render a Markdown summary from the raw output:
benchmarks/summarize.sh benchmarks/raw.txt > benchmarks/summary.md

# Compare two commits statistically (install once: go install
# golang.org/x/perf/cmd/benchstat@latest), capturing raw.txt on each:
benchstat old.txt new.txt
```

`-run=^$` skips unit tests (there are none here); `-benchtime=Nx` fixes the
iteration count so runs are comparable; `-count=6` repeats each benchmark so
`benchstat` can judge variance.

## Metrics

`go test -bench -benchmem` reports, per benchmark:

- **`ns/op`** — wall time per instance. **instances/sec = 1e9 / ns_per_op**; the
  summary script derives this column for you. Under the durable profile this is
  gated by `fsync`, so it reflects your disk, not just the CPU.
- **`allocs/op`, `B/op`** — heap allocations and bytes per instance. Watch these
  across commits; a jump is a regression signal. (Per-command hot-path allocation
  is guarded separately by `engine`'s in-package allocation benchmark, invariant
  I1 — this figure is the *whole-instance* cost, which legitimately allocates.)

The harness adds two custom per-op metrics:

- **`events/op`** — durable event records applied per instance, read from the
  monotonic applied log position (a record's position is its sequence number).
  **events/sec = events/op × instances/sec.**
- **`walB/op`** — on-disk WAL bytes grown per instance. WAL segments grow by
  append (no preallocation), so this is real storage growth per instance.

The latency benchmarks add the distribution instead of throughput metrics:

- **`p50-ms`, `p95-ms`, `p99-ms`, `max-ms`** — the operation-latency distribution in
  milliseconds. Computed by **nearest-rank** on the sorted per-op samples: the sample
  at position `ceil(p/100 × N)`. One sample per iteration, so a run collects `N = b.N`
  samples — the tail is only meaningful with a large `N` (P99 needs ≥100 samples;
  it stabilizes around a few thousand), so run these with `-benchtime=2000x` or more.
  These appear in the raw `-bench` output and in `benchstat`, which renders every
  metric.

## Interpretation & method

- **Warm-up.** `b.ResetTimer()` runs after deploy + recovery, so setup is excluded.
  The first few iterations still warm caches; prefer `-benchtime=Nx` with a
  reasonably large `N` (e.g. `2000x`) plus `-count` repetitions over a single short
  run.
- **Repetitions & variance.** Use `-count=6` (or more) and reduce with `benchstat`;
  a single run is not a baseline. `fsync`-bound numbers are especially sensitive to
  what else touches the disk.
- **Isolation.** Close other disk-heavy work; pin CPU frequency if you can. Report
  the machine, don't average across different ones.
- **Record the environment** with every result you keep:
  - CPU model and core count; whether frequency scaling/turbo was fixed
  - OS and kernel version
  - **Storage type** (NVMe/SSD/HDD, local vs. network) — dominates the durable numbers
  - filesystem
  - Go version (`go version`) and `GOMAXPROCS`
  - Atlas commit SHA (`git rev-parse HEAD`) and whether the tree was clean

The `goos`/`goarch`/`cpu` header that `go test -bench` prints captures part of
this; record the rest alongside the raw file.

## Caveats

- Numbers are **machine- and commit-specific**. Do not quote them as "Atlas does X
  ops/sec" without the machine and commit.
- The durable figures are **steady-state**. The HTTP figure is the in-process
  handler path (no TCP), not a network round-trip. The **in-memory figures are not a
  throughput claim** — they exist only to isolate engine CPU from `fsync` latency;
  never quote them as what Atlas does, and never derive a "millions/sec" number from
  them.
- No cross-engine comparison (Camunda/Zeebe/Flowable/Temporal) until equivalent
  workloads exist.

## Deferred (next programme-B slices)

- A loopback-socket (real TCP + `http.Client`) HTTP variant, and service-task
  completion driven over HTTP, on top of the in-process create benchmarks here.
- An in-memory profile for the HTTP path too (the RAM-backed profile currently
  covers the engine-level workloads only).
- A large parked-workload steady-state profile (many active jobs/timers/messages
  concurrently), beyond the parked instances the recovery benchmark restores.
- A committed, dated baseline result file for a named reference machine.
