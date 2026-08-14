# Published baseline results

This directory holds Atlas's first **published, reproducible** benchmark baseline
(work programme B of the v0.2.0 *Proof of Reliability & Performance* initiative).

Each baseline is two files sharing a name:

- `baseline-<commit>.txt` — the raw, machine-readable `go test -bench` output, with
  an environment-metadata header. This is the record of truth; `benchstat` consumes
  it directly.
- `baseline-<commit>.md` — a human-readable summary generated from the raw file: the
  environment, a `benchstat`-reduced table (median ± variance across repetitions),
  and the throughput table from `../summarize.sh`.

## What a baseline is — and is not

A baseline here is **the numbers a specific machine produced at a specific commit**,
nothing more. It exists so a change's effect is checkable against the same setup, and
so anyone can reproduce the measurement. It is **not**:

- a universal product claim ("Atlas does X ops/sec") — the numbers are dominated by
  the recording machine's disk `fsync` latency and CPU, and this repository's baseline
  was captured on a **shared, ephemeral cloud VM** whose CPU clock even varied between
  runs, so treat its absolute values as illustrative;
- a comparison against any other engine — that needs demonstrably equivalent workloads
  (see the top-level [`../README.md`](../README.md) caveats);
- a threshold gate — PR CI only smoke-runs the harness (one iteration each); it does
  not compare against these numbers.

## Reproducing / refreshing

Capture on your own machine (record its specs — the raw file's header shows what to
note) and regenerate the summary:

```bash
commit=$(git rev-parse --short HEAD)
go test -run=^$ -bench=. -benchmem -benchtime=2000x -count=5 ./benchmarks/ \
  > benchmarks/results/baseline-$commit.txt
# prepend an environment-metadata header (see the existing file for the format),
# then build the summary:
benchmarks/summarize.sh benchmarks/results/baseline-$commit.txt   # throughput table
benchstat benchmarks/results/baseline-$commit.txt                 # reduced stats + percentiles
```

`benchstat` (install once with `go install golang.org/x/perf/cmd/benchstat@latest`)
reduces the `-count` repetitions to a median and a variance estimate, and renders the
custom metrics — including the latency percentiles the throughput table omits.
