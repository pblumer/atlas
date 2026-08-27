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

# The total is computed from the profile rather than read off `go tool cover -func`,
# which prints it rounded to one decimal. That rounding was not cosmetic: it is the
# comparison this script performs, so a repository sitting at 94.918% reported "95.0"
# and passed a 95% floor for as long as it stayed above 94.95%. A floor that a
# below-floor repository satisfies is not a floor, and the gap it hid grew silently,
# because every run said OK.
#
# The profile's own lines are the source: "file.go:l.c,l.c numStmt count", one per
# block, so the exact ratio is two sums and needs no rounding at any step.
read -r covered statements total < <(
  awk '!/^mode:/ { n = $(NF-1); if ($NF + 0 > 0) k += n; s += n }
       END { if (s == 0) exit 1; printf "%d %d %.4f\n", k, s, 100 * k / s }' "${profile}"
) || {
  echo "check-coverage: could not determine total coverage" >&2
  exit 1
}

awk -v t="${total}" -v min="${threshold}" 'BEGIN { exit !(t + 0 >= min + 0) }' || {
  # Say how far, in the unit the reader can act on: statements, not percent. A tenth
  # of a percentage point is a number nobody can plan against; "cover 42 more" is.
  need="$(awk -v s="${statements}" -v k="${covered}" -v min="${threshold}" \
    'BEGIN { printf "%d", (min / 100 * s) - k + 0.999999 }')"
  echo "FAIL: total statement coverage ${total}% is below the ${threshold}% floor (ADR-0018)." >&2
  echo "      ${covered} of ${statements} statements covered; ${need} more would reach the floor." >&2
  echo "Lowest-covered functions:" >&2
  # Sort on a numeric key awk emits, not on the report's own columns: those are
  # separated by a variable number of tabs and the percentage carries a `%`, so the
  # obvious `sort -k3 -n` silently sorted by filename and pointed at the wrong code.
  # `head` is left out for the same reason it was a problem: it closes the pipe, and
  # under `set -o pipefail` the resulting SIGPIPE ended the script here — with sort's
  # status, before the exit below could give the intended one.
  go tool cover -func="${profile}" \
    | awk '$1 !~ /^total:/ { pct = $NF; sub(/%/, "", pct)
                             if (pct + 0 < 100) printf "%6.1f%%  %s  %s\n", pct, $1, $2 }' \
    | sort -n | awk 'NR <= 20' >&2
  exit 1
}

# Say the margin as well as the number, for the same reason the failure says how far
# short it is: "95.0%" reads like room to spare and can be four statements. The margin
# is how many covered statements could stop being covered before the next run fails —
# which is the number a change is planned against.
margin="$(awk -v s="${statements}" -v k="${covered}" -v min="${threshold}" \
  'BEGIN { printf "%d", k - (min / 100 * s) }')"
echo "OK: total statement coverage ${total}% meets the ${threshold}% floor (${covered}/${statements} statements)."
echo "    Margin: ${margin} covered statements could lapse before this fails."
