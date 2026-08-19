# Atlas benchmark baseline — commit `5b1b9f2`

The first published, reproducible Atlas performance baseline (v0.2.0 programme B).
Generated from [`baseline-5b1b9f2.txt`](baseline-5b1b9f2.txt) with `benchstat`
(median ± 95% CI over 10 repetitions).

> **Read this first.** These numbers were captured in the Atlas CI / dev container —
> a **shared, ephemeral cloud VM whose CPU clock varied between runs** — and are
> dominated by disk `fsync` latency. They are **illustrative and reproducible**, not a
> universal product claim, not a hardware reference, and not a cross-engine comparison.
> See [`README.md`](README.md).

## Environment

| | |
|---|---|
| Commit | `5b1b9f2` (clean checkout of `main`) |
| Go | `go1.26.0 linux/amd64` |
| GOMAXPROCS | 4 |
| CPU | Intel Xeon, 4 cores — variable clock (2.1–2.8 GHz observed across runs) |
| OS / kernel | Linux 6.18.5-fc-v20 |
| Durable store | ext4 on a virtual disk (`/dev/vda`) — where the WAL fsync lands |
| In-memory | tmpfs (`/dev/shm`) |
| Command | `go test -run=^$ -bench=. -benchmem -benchtime=2000x -count=10 ./benchmarks/` |

## Headline (median sec/op → instances/sec)

**Durable, steady-state (pure engine):**

| Workload | sec/op | ~instances/sec |
|---|---:|---:|
| Linear self-completing | 1.188 ms ±6% | ~840 |
| Service-task lifecycle | 1.379 ms ±6% | ~725 |
| Variable + gateway routing | 1.546 ms ±7% | ~650 |

**Durable, end-to-end HTTP:**

| Workload | sec/op | ~instances/sec |
|---|---:|---:|
| HTTP linear create | 1.823 ms ±8% | ~550 |
| HTTP variable + gateway create | 2.126 ms ±5% | ~470 |

**In-memory (tmpfs — `fsync` on RAM), same engine workloads:**

| Workload | sec/op | ~instances/sec |
|---|---:|---:|
| Linear self-completing | 60.6 µs ±4% | ~16,500 |
| Service-task lifecycle | 144.5 µs ±7% | ~6,900 |
| Variable + gateway routing | 92.6 µs ±5% | ~10,800 |

**Recovery (WAL replay from genesis, per recovered instance):**

| Workload | sec/op | ~instances/sec recovered |
|---|---:|---:|
| Linear (completed history) | 44.0 µs ±6% | ~22,700 |
| Service-task (parked + jobs) | 20.4 µs ±10% | ~48,900 |

**Latency distribution (durable, ms):**

| Path | p50 | p95 | p99 | max |
|---|---:|---:|---:|---:|
| HTTP create (end-to-end) | 1.728 | 2.622 | 4.777 | 24.35 |
| Engine (pure) | 1.066 | 1.552 | 2.566 | 12.28 |

## What the numbers say

- **`fsync` dominates the durable path.** Linear self-completing costs 1.188 ms
  durable but only 60.6 µs in-memory, so the disk `fsync` is **~95%** of the per-op
  time and the engine's own CPU cost is ~61 µs/instance. Every durable throughput
  number here is really a `fsync`-latency number for this VM's disk.
- **The API layer adds a bounded cost.** HTTP linear create's median is ~0.6 ms above
  the engine's (both `fsync`-bound, so read that loosely); the stable signal is
  allocations — the HTTP path is 237 allocs/op vs the engine's 148, i.e. ~90 extra
  allocations and ~9 KiB per request for decode, routing, the run-loop handoff, and
  response encode. `events/op` and `walB/op` are identical across the two, confirming
  the same durable work.
- **Recovery is cheaper than execution.** Replaying a completed linear instance's 9
  events costs ~44 µs — pure `applyToState`, no FEEL or command logic — for a recovery
  rate of ~22,700 instances/sec (~204k events/sec). Recovery still replays from
  genesis; this is the number a checkpoint (programme D) would improve.
- **The mean hides the tail.** The durable HTTP create's median latency is 1.73 ms but
  its p99 is 4.78 ms and its worst sample 24 ms — ~14× the median. On a shared VM the
  `fsync` tail is long; the percentiles surface what `ns/op` alone would not.

## Full statistics

```
goos: linux
goarch: amd64
pkg: github.com/pblumer/atlas/benchmarks
cpu: Intel(R) Xeon(R) Processor @ 2.10GHz
                                    │ baseline-5b1b9f2.txt │
                                    │                          sec/op                          │
HTTPLinearCreate-4                                                                1.823m ±  8%
HTTPVariableGatewayCreate-4                                                       2.126m ±  5%
InMemoryLinearSelfCompleting-4                                                    60.60µ ±  4%
InMemoryServiceTaskLifecycle-4                                                    144.5µ ±  7%
InMemoryVariableGatewayRouting-4                                                  92.61µ ±  5%
LatencyHTTPLinearCreate-4                                                         1.840m ±  9%
LatencyEngineLinearSelfCompleting-4                                               1.163m ±  6%
RecoveryLinearCompleted-4                                                         44.04µ ±  6%
RecoveryServiceTaskParked-4                                                       20.44µ ± 10%
LinearSelfCompleting-4                                                            1.188m ±  6%
ServiceTaskLifecycle-4                                                            1.379m ±  6%
VariableGatewayRouting-4                                                          1.546m ±  7%
geomean                                                                           397.0µ

                                 │ baseline-5b1b9f2.txt │
                                 │                        events/op                         │
HTTPLinearCreate-4                                                               9.000 ± 0%
HTTPVariableGatewayCreate-4                                                      12.00 ± 0%
InMemoryLinearSelfCompleting-4                                                   9.000 ± 0%
InMemoryServiceTaskLifecycle-4                                                   10.00 ± 0%
InMemoryVariableGatewayRouting-4                                                 12.00 ± 0%
RecoveryLinearCompleted-4                                                        9.000 ± 0%
RecoveryServiceTaskParked-4                                                      5.000 ± 0%
LinearSelfCompleting-4                                                           9.000 ± 0%
ServiceTaskLifecycle-4                                                           10.00 ± 0%
VariableGatewayRouting-4                                                         12.00 ± 0%
geomean                                                                          9.448

                                 │ baseline-5b1b9f2.txt │
                                 │                         walB/op                          │
HTTPLinearCreate-4                                                               944.0 ± 0%
HTTPVariableGatewayCreate-4                                                     1.239k ± 0%
InMemoryLinearSelfCompleting-4                                                   944.0 ± 0%
InMemoryServiceTaskLifecycle-4                                                  1.050k ± 0%
InMemoryVariableGatewayRouting-4                                                1.239k ± 0%
RecoveryLinearCompleted-4                                                        944.0 ± 0%
RecoveryServiceTaskParked-4                                                      525.0 ± 0%
LinearSelfCompleting-4                                                           944.0 ± 0%
ServiceTaskLifecycle-4                                                          1.050k ± 0%
VariableGatewayRouting-4                                                        1.239k ± 0%
geomean                                                                          986.7

                                    │ baseline-5b1b9f2.txt │
                                    │                           B/op                           │
HTTPLinearCreate-4                                                               13.49Ki ±  0%
HTTPVariableGatewayCreate-4                                                      16.03Ki ±  0%
InMemoryLinearSelfCompleting-4                                                   4.611Ki ±  1%
InMemoryServiceTaskLifecycle-4                                                   5.789Ki ±  0%
InMemoryVariableGatewayRouting-4                                                 6.698Ki ±  0%
LatencyHTTPLinearCreate-4                                                        13.49Ki ±  0%
LatencyEngineLinearSelfCompleting-4                                              4.644Ki ±  1%
RecoveryLinearCompleted-4                                                        13.13Ki ± 27%
RecoveryServiceTaskParked-4                                                      7.125Ki ±  3%
LinearSelfCompleting-4                                                           4.655Ki ±  1%
ServiceTaskLifecycle-4                                                           5.812Ki ±  0%
VariableGatewayRouting-4                                                         6.699Ki ±  0%
geomean                                                                          7.664Ki

                                    │ baseline-5b1b9f2.txt │
                                    │                        allocs/op                         │
HTTPLinearCreate-4                                                                  237.0 ± 0%
HTTPVariableGatewayCreate-4                                                         311.0 ± 0%
InMemoryLinearSelfCompleting-4                                                      148.0 ± 1%
InMemoryServiceTaskLifecycle-4                                                      184.0 ± 0%
InMemoryVariableGatewayRouting-4                                                    211.0 ± 0%
LatencyHTTPLinearCreate-4                                                           237.0 ± 0%
LatencyEngineLinearSelfCompleting-4                                                 148.0 ± 0%
RecoveryLinearCompleted-4                                                           95.00 ± 0%
RecoveryServiceTaskParked-4                                                         49.00 ± 0%
LinearSelfCompleting-4                                                              148.0 ± 0%
ServiceTaskLifecycle-4                                                              184.0 ± 0%
VariableGatewayRouting-4                                                            211.0 ± 0%
geomean                                                                             164.7

                                    │ baseline-5b1b9f2.txt │
                                    │                          max-ms                          │
LatencyHTTPLinearCreate-4                                                         24.35 ±  51%
LatencyEngineLinearSelfCompleting-4                                               12.28 ± 116%
geomean                                                                           17.29

                                    │ baseline-5b1b9f2.txt │
                                    │                          p50-ms                          │
LatencyHTTPLinearCreate-4                                                           1.728 ± 7%
LatencyEngineLinearSelfCompleting-4                                                 1.066 ± 4%
geomean                                                                             1.357

                                    │ baseline-5b1b9f2.txt │
                                    │                          p95-ms                          │
LatencyHTTPLinearCreate-4                                                          2.622 ± 14%
LatencyEngineLinearSelfCompleting-4                                                1.552 ±  6%
geomean                                                                            2.017

                                    │ baseline-5b1b9f2.txt │
                                    │                          p99-ms                          │
LatencyHTTPLinearCreate-4                                                          4.777 ± 22%
LatencyEngineLinearSelfCompleting-4                                                2.566 ± 32%
geomean                                                                            3.501
```
