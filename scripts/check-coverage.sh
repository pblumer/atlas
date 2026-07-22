#!/usr/bin/env bash
# Enforce the repository-wide statement-coverage floor (see docs/adr/0018).
#
# Computes total statement coverage across all packages from a single merged
# profile and fails if it drops below the threshold. This is a floor on one
# repo-wide number, deliberately not a per-change delta gate — see ADR-0018 for
# why (avoiding coverage theatre).
#
# Usage: scripts/check-coverage.sh [threshold]   (threshold defaults to 95)
set -euo pipefail

threshold="${1:-95}"
outdir="coverage"
profile="${outdir}/cover.out"

mkdir -p "${outdir}"

# One merged profile across every importable library package. The floor covers
# library code, not the thin cmd/* binary entrypoints (which carry no tests):
# excluding main packages also keeps this independent of the `covdata` tool,
# which some trimmed toolchains omit and which is only needed to instrument
# main packages.
mapfile -t pkgs < <(go list -f '{{if ne .Name "main"}}{{.ImportPath}}{{end}}' ./... | grep .)
go test -covermode=atomic -coverprofile="${profile}" "${pkgs[@]}"

total="$(go tool cover -func="${profile}" | awk '/^total:/ {gsub(/%/,"",$3); print $3}')"

if [ -z "${total}" ]; then
  echo "check-coverage: could not determine total coverage" >&2
  exit 1
fi

# Integer-safe comparison to two decimals (bash has no floats).
awk -v t="${total}" -v min="${threshold}" 'BEGIN { exit !(t+0 >= min+0) }' || {
  echo "FAIL: total statement coverage ${total}% is below the ${threshold}% floor (ADR-0018)." >&2
  echo "Lowest-covered functions:" >&2
  go tool cover -func="${profile}" | awk '$3+0 < 100 && $1 !~ /^total:/' | sort -t$'\t' -k3 -n | head -20 >&2
  exit 1
}

echo "OK: total statement coverage ${total}% meets the ${threshold}% floor."
