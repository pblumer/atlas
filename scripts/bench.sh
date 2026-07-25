#!/usr/bin/env bash
# Run the Atlas performance suite and print results in Go benchmark format,
# ready for benchstat (https://pkg.go.dev/golang.org/x/perf/cmd/benchstat).
#
# The suite tracks performance regressions over time. The workflow is:
#
#   # capture a baseline on a known-good commit (checked into bench/baseline.txt)
#   scripts/bench.sh > bench/baseline.txt
#
#   # on a change, capture again and compare
#   scripts/bench.sh > /tmp/new.txt
#   benchstat bench/baseline.txt /tmp/new.txt
#
# Honesty: the benchmarks fsync for real (invariant I2), so the number depends
# entirely on the storage medium under them. An fsync on a tmpfs RAM disk is
# nearly free and does NOT reflect a durable engine. This script reports the
# medium (and warns if it is tmpfs) so a result is never read without it. Point
# the benchmarks at real disk by exporting TMPDIR to a path on that disk.
#
# Usage: scripts/bench.sh [count] [benchtime]
#   count     benchstat sample count (default 6)
#   benchtime go test -benchtime value (default 1s)
set -euo pipefail

count="${1:-6}"
benchtime="${2:-1s}"
probe="${TMPDIR:-/tmp}"

# --- Environment header (to stderr, so stdout stays clean for benchstat) -------
{
  echo "# Atlas performance suite"
  echo "# commit:    $(git rev-parse --short HEAD 2>/dev/null || echo '(no git)')"
  echo "# go:        $(go version | awk '{print $3, $4}')"
  echo "# os/arch:   $(go env GOOS)/$(go env GOARCH)"
  echo "# cpus:      $(nproc 2>/dev/null || echo '?') (GOMAXPROCS ${GOMAXPROCS:-default})"
  echo "# count:     ${count}   benchtime: ${benchtime}"

  # Storage medium the benchmarks fsync to — the honesty line.
  fstype="$(stat -f -c %T "${probe}" 2>/dev/null || echo unknown)"
  echo "# tmpdir:    ${probe}"
  echo "# medium:    ${fstype}"
  if [ "${fstype}" = "tmpfs" ] || [ "${fstype}" = "ramfs" ]; then
    echo "# WARNING: benchmarks run on ${fstype} (RAM) — fsync is near-free here."
    echo "#          Throughput/latency will be optimistic and NOT durability-honest."
    echo "#          Export TMPDIR to a path on real disk for a comparable number."
  fi
} >&2

# --- Run: level 1 (in-process) and level 2 (HTTP), benchmarks only -------------
# -run '^$' skips Test functions; -benchmem adds allocs; -count enables benchstat
# variance analysis.
exec go test \
  -run '^$' \
  -bench . \
  -benchmem \
  -benchtime "${benchtime}" \
  -count "${count}" \
  ./bench/ ./bench/e2e/
