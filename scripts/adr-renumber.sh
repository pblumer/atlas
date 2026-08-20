#!/usr/bin/env bash
# Move an ADR to a different number: the file, its heading, its row in the index,
# and every reference to it, in one step.
#
# An ADR number cannot be reserved. You pick the next free one when you write the
# record, and any branch that merges before yours can take it in the meantime —
# `go test ./docs/adr` then fails with "ADR-NNNN is used by 2 files". That is not a
# mistake anyone made; it is what parallel work costs, and the number is the *identity*
# of a decision, so the later record has to move. This script makes that a one-liner
# instead of a careful sweep through file names, headings, the index table, code
# comments and the changelog.
#
#   scripts/adr-renumber.sh docs/adr/0153-secret-shape-hints.md 0155
#
# Renumber *before* merging the branch that took your number, while the old number
# still identifies exactly one record. Afterwards `ADR-0153` in a comment could mean
# either decision, and this script will say so and leave the references alone rather
# than rewrite the wrong ones.
#
# Verify with `go test ./docs/adr`, which is what catches the collision in the first
# place: it checks that numbers are unique and contiguous and that the index matches
# the directory.
set -euo pipefail

die() { printf 'adr-renumber: %s\n' "$*" >&2; exit 1; }

[[ $# -eq 2 ]] || die "usage: scripts/adr-renumber.sh <docs/adr/NNNN-slug.md> <new-number>
       e.g. scripts/adr-renumber.sh docs/adr/0153-secret-shape-hints.md 0155"

file=$1
new=$2
root=$(git rev-parse --show-toplevel) || die "not a git repository"
cd "$root"

[[ -f $file ]] || die "no such file: $file"
[[ $new =~ ^[0-9]{4}$ ]] || die "the new number must be four digits, got: $new"

base=${file##*/}
[[ $base =~ ^([0-9]{4})-([a-z0-9-]+)\.md$ ]] || die "not an ADR file name: $base"
old=${BASH_REMATCH[1]}
slug=${BASH_REMATCH[2]}
[[ $old != "$new" ]] || die "ADR-$old already has that number"

newbase="$new-$slug.md"
newfile="docs/adr/$newbase"
[[ ! -e $newfile ]] || die "$newfile already exists"

# Is the old number this file's alone? If the collision has already landed — both
# records in the tree — then "ADR-0153" in a comment no longer names one decision, and
# a blind rewrite would repoint references to the *other* record. Say so instead.
mapfile -t sharing < <(ls docs/adr/"$old"-*.md 2>/dev/null | grep -v "^$file$" || true)

git mv "$file" "$newfile" 2>/dev/null || mv "$file" "$newfile"
sed -i "s/^# ADR-$old:/# ADR-$new:/" "$newfile"

if [[ ${#sharing[@]} -eq 0 ]]; then
  # Every ADR-<old> in the tree meant this record, so every one of them moves: code
  # comments, other ADRs, the changelog, the index row's number and its link.
  mapfile -t refs < <(git grep -l -E "ADR-$old|$old-$slug\.md" -- . || true)
  for f in "${refs[@]}"; do
    sed -i "s/ADR-$old/ADR-$new/g; s/$old-$slug\.md/$newbase/g" "$f"
  done
  printf 'adr-renumber: rewrote references in %d file(s)\n' "${#refs[@]}"
else
  printf 'adr-renumber: ADR-%s is still used by %s — references NOT rewritten.\n' \
    "$old" "${sharing[*]}" >&2
  printf 'adr-renumber: review these by hand, they may mean either record:\n' >&2
  git grep -n -E "ADR-$old" -- . >&2 || true
fi

# The index is a table in number order, so the row both changes and moves. Its link
# may already have been rewritten above, its bracketed number never is (that text is
# just "[0153]", not "ADR-0153"), so the row is normalized here from either form.
index=docs/adr/README.md
sed -i -E "s#^\| \[[0-9]{4}\]\(($old|$new)-$slug\.md\)#| [$new]($newbase)#" "$index"
row=$(grep -m1 -E "^\| \[$new\]\($newbase\) \|" "$index") || die "no index row for ADR-$old in $index (add one by hand)"
tmp=$(mktemp)
grep -vxF "$row" "$index" >"$tmp"
last=$(grep -nE '^\| \[[0-9]{4}\]\(' "$tmp" | tail -1 | cut -d: -f1)
[[ -n ${last:-} ]] || die "no ADR rows found in $index"
awk -v new="$new" -v row="$row" -v last="$last" '
  BEGIN { placed = 0 }
  {
    if (!placed && match($0, /^\| \[[0-9][0-9][0-9][0-9]\]\(/) && substr($0, 4, 4) + 0 > new + 0) {
      print row
      placed = 1
    }
    print
    if (!placed && NR == last) { print row; placed = 1 }
  }
' "$tmp" >"$index"
rm -f "$tmp"

printf 'adr-renumber: ADR-%s → ADR-%s (%s)\n' "$old" "$new" "$newfile"
printf 'adr-renumber: now run: go test ./docs/adr\n'
