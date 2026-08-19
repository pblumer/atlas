#!/usr/bin/env bash
# Turn raw `go test -bench` output into a Markdown table.
#
# The raw output is the machine-readable record (the same format `benchstat`
# consumes); this script renders a human-readable summary from it. It derives
# instances/sec from ns/op and passes the harness's custom metrics through.
#
# Usage:
#   go test -run=^$ -bench=. -benchmem -count=6 ./benchmarks/ | tee benchmarks/raw.txt
#   benchmarks/summarize.sh benchmarks/raw.txt > benchmarks/summary.md
#
# With multiple -count runs each benchmark appears several times; this renders
# every line. For a statistically reduced comparison across commits, use
# `benchstat` on the raw file instead (see README.md).
set -euo pipefail

in="${1:-/dev/stdin}"

awk '
  /^Benchmark/ {
    name = $1
    sub(/-[0-9]+$/, "", name)   # strip the trailing -GOMAXPROCS
    iters = $2
    delete m
    for (i = 3; i < NF; i += 2) m[$(i+1)] = $i   # value/unit pairs
    ns = m["ns/op"] + 0
    inst = (ns > 0) ? sprintf("%.0f", 1e9 / ns) : "-"
    rows[++n] = sprintf("| %s | %s | %s | %s | %s | %s | %s | %s |", \
      name, iters, m["ns/op"], inst, m["events/op"], m["walB/op"], m["allocs/op"], m["B/op"])
  }
  END {
    if (n == 0) { print "No benchmark lines found in input." > "/dev/stderr"; exit 1 }
    print "| Benchmark | iters | ns/op | instances/sec | events/op | walB/op | allocs/op | B/op |"
    print "|---|---:|---:|---:|---:|---:|---:|---:|"
    for (i = 1; i <= n; i++) print rows[i]
  }
' "$in"
