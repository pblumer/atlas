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

## What this measures (and what it does not, yet)

This first slice measures the **pure engine** under the **durable profile**:

- **Pure engine** — the processor, WAL, and state store directly, with no HTTP/API
  layer. The end-to-end server path is a separate, deferred axis.
- **Durable** — a real segmented write-ahead log with a **group-commit `fsync` on
  every batch** and a real Pebble state store, over a temp directory. There is no
  synthetic/in-memory or no-fsync profile yet; the per-op time is therefore
  dominated by real disk `fsync` latency, which is the point.
- **Steady state** — throughput of a warm engine. Start-up/recovery is a separate,
  deferred axis.

Deliberately **deferred** to later programme-B slices (see *Deferred* below):
end-to-end HTTP benchmarks, an in-memory/no-fsync profile, P50/P95/P99 latency,
recovery-from-N-events, a parked-workload profile, and published baseline result
files.

## Workloads

| Benchmark | Programme workload | Shape |
|---|---|---|
| `BenchmarkLinearSelfCompleting` | #1 minimal self-completing linear | Start → Script → End; runs straight through, no worker |
| `BenchmarkServiceTaskLifecycle` | #2 service-task create/activate/complete | Start → ServiceTask → End; parks on a job, an in-process worker completes it |
| `BenchmarkVariableGatewayRouting` | #3 mixed variables + gateway routing | Start → XOR(`amount > 100`) → Script → End; a start variable picks the branch |

Each is built with the public `compiler.NewBuilder`, so the harness compiles the
process once and measures only execution.

## Running

```bash
# Quick local run (a few seconds), memory stats on:
go test -run=^$ -bench=. -benchmem ./benchmarks/

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
- This is the **durable, engine-only, steady-state** profile. Do not present it as
  end-to-end server throughput, and do not derive a no-fsync "millions/sec" figure
  from it.
- No cross-engine comparison (Camunda/Zeebe/Flowable/Temporal) until equivalent
  workloads exist.

## Deferred (next programme-B slices)

- End-to-end HTTP/API benchmarks (through `api.Server`).
- An in-memory / no-fsync profile alongside the durable one, to separate CPU cost
  from `fsync` cost.
- P50/P95/P99 end-to-end latency with a defensible sampling method.
- Recovery-from-a-known-event-count and a parked-workload (many active
  jobs/timers/messages) profile.
- A committed, dated baseline result file for a named reference machine.
