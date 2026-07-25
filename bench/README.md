# Atlas performance suite

A benchmark suite for tracking Atlas's performance **over time** — catching
regressions between versions, not producing a marketing headline. Its guiding
principle is honesty: every number is one you could defend, with its caveats
stated rather than hidden.

## What it measures

Four axes, because a workflow engine is judged on more than one number:

| Axis | Benchmark | Why it matters |
|------|-----------|----------------|
| **Throughput** | `BenchmarkThroughput` | Instances/sec through the group-commit path — the busy-server number. |
| **Latency** | `BenchmarkLatency` | Command→durable time for one instance paying its own fsync — what a single request feels. |
| **Recovery** | `BenchmarkRecovery` | Replay speed of a populated log into an empty store — the core "state after replay == state built live" promise (invariant I4). |
| **End-to-end** | `BenchmarkE2ECreateInstance` (`e2e/`) | The same work over the real HTTP API, including transport and serialization — the client's view. |

Throughput additionally reports `allocs/op` end-to-end as a coarse drift signal.
The *precise* no-allocation-on-the-hot-path guard (invariant I1) is
`engine.BenchmarkInstanceLifecycle`, which isolates the processor path.

## Two levels

- **Level 1 — in-process** (`bench/`): drives the engine directly, no network.
  Deterministic and low-noise; the honest *engine* number.
- **Level 2 — end-to-end** (`bench/e2e/`): drives the engine through the HTTP
  API against an in-process server. Noisier by design — it includes everything a
  client pays.

Both levels run the **same catalogue** (`bench/scenarios/`), defined once as both
a programmatic build (level 1) and the equivalent BPMN XML (level 2). Tests pin
that the two stay behaviorally equivalent, so a scenario can't drift between
levels.

## The scenario catalogue

One process per BPMN feature, so a number traces back to the feature it measures
— a single linear "hello world" would say nothing about gateways, expressions,
or recovery:

| Scenario | Feature | Shape |
|----------|---------|-------|
| `linear` | service task | Start → ServiceTask → End (one external job) |
| `exclusive` | data-based XOR | gateway routes on a FEEL condition |
| `parallel` | AND gateway | fork onto two branches, synchronizing join |
| `script` | script task | in-engine FEEL evaluation + variable write |
| `mixed` | combined | XOR → (fork → 2 scripts → join), a realistic multi-feature path |

## Why the numbers are honest

Four ways an engine benchmark commonly lies, and what this suite does instead:

1. **fsync on tmpfs.** `b.TempDir()` often lands on a RAM disk in CI, where
   `fsync` is nearly free — so you'd measure throughput *without* durability.
   `scripts/bench.sh` detects and **reports the storage medium**, and warns if
   it's tmpfs. A throughput number is only comparable next to its medium. Point
   the suite at real disk with `TMPDIR=/path/on/disk`.
2. **Batching defined away.** Driving one instance per fsync measures fsync
   latency, not throughput. `BenchmarkThroughput` folds many `CreateInstance`
   commands through the batch cycle (group commit, one fsync for up to ~1024
   commands) — the path a busy deployment actually runs. It is reported
   *separately* from `BenchmarkLatency`, because group commit means throughput
   ≠ 1/latency, and showing only one would misrepresent the engine.
3. **Happy path only.** The catalogue exercises gateways, expressions and a
   realistic mix — not just a linear pass-through.
4. **A single noisy sample.** `scripts/bench.sh` runs with `-count` so
   [benchstat] can report medians and variance; results carry a header with the
   commit, Go version, CPU, and medium.

## Running it

```bash
# print results in Go benchmark format (env header goes to stderr)
make bench                      # or: scripts/bench.sh [count] [benchtime]

# honest, durability-real numbers: run on real disk, not tmpfs
TMPDIR=/var/tmp/atlas-bench make bench

# just the in-process level, quickly
go test -run '^$' -bench . -benchmem ./bench/

# one scenario / axis
go test -run '^$' -bench 'Throughput/mixed' ./bench/
```

## Tracking regressions over time

```bash
# 1. Baseline on a known-good commit (checked in)
scripts/bench.sh > bench/baseline.txt

# 2. On a change, capture again and compare
scripts/bench.sh > /tmp/new.txt
benchstat bench/baseline.txt /tmp/new.txt
```

benchstat reports the delta with a confidence interval, so noise doesn't read as
a regression. Regenerate `bench/baseline.txt` deliberately (on a controlled
machine and medium — note them), not on every change; a baseline captured on
tmpfs is not comparable to one captured on disk.

`bench/baseline.txt` is committed as a **reference shape**, not an absolute
target: the ratios between scenarios and the throughput-vs-latency gap are
portable, while absolute nanoseconds depend on the machine and medium recorded in
its header. Compare a new run against a baseline captured on the *same* hardware.

[benchstat]: https://pkg.go.dev/golang.org/x/perf/cmd/benchstat
