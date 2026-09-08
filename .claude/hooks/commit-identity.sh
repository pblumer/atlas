#!/usr/bin/env bash
# SessionStart hook: record the human as the author of the agent's commits.
#
# Without it Claude Code commits as `Claude <noreply@anthropic.com>`, an
# address that belongs to the GitHub account `claude` — which is why agent
# work shows up under that account in this repository's contributor
# statistics instead of under the person who asked for it.
#
# Git keeps separate config keys for the two identities on a commit
# (`author.*` / `committer.*`, git 2.22 and later). The hook uses those
# rather than overwriting `user.*`:
#
#   author    -> the human  (this is what GitHub counts as a contribution)
#   committer -> the agent  (provenance stays visible, and a commit
#                            signature stays bound to the agent whose key
#                            produced it)
#
# It writes only to the repository-local config, and only when the commit
# would otherwise be made under an agent identity. Anyone working in their
# own clone with their own git identity is left alone.
set -euo pipefail

root="${CLAUDE_PROJECT_DIR:-$(git rev-parse --show-toplevel 2>/dev/null || true)}"
[ -n "$root" ] && [ -d "$root/.git" ] || exit 0
cd "$root"

# Only step in when the effective identity is the agent's.
current_email="$(git config --get user.email 2>/dev/null || true)"
current_name="$(git config --get user.name 2>/dev/null || true)"
case "$current_email" in
  *@anthropic.com) ;;
  *) exit 0 ;;
esac

key="${CLAUDE_CODE_USER_EMAIL:-}"
map="$root/.claude/commit-identities"
if [ -z "$key" ] || [ ! -f "$map" ]; then
  echo "Commit identity: nothing to map from, commits stay under ${current_name:-Claude}."
  exit 0
fi

entry="$(awk -v k="$key" '
  { sub(/#.*/, "") }
  { n = index($0, "="); if (n == 0) next
    lhs = substr($0, 1, n - 1); rhs = substr($0, n + 1)
    gsub(/^[ \t]+|[ \t]+$/, "", lhs); gsub(/^[ \t]+|[ \t]+$/, "", rhs)
    if (tolower(lhs) == tolower(k) && rhs != "") { print rhs; exit } }' "$map")"

name="${entry%%<*}"; name="${name%"${name##*[![:space:]]}"}"
email="${entry#*<}"; email="${email%>*}"

if [ -z "$name" ] || [ -z "$email" ] || [ "$email" = "$entry" ]; then
  echo "Commit identity: no entry for $key in .claude/commit-identities, commits stay under ${current_name:-Claude}."
  exit 0
fi

git config --local author.name "$name"
git config --local author.email "$email"
git config --local committer.name "${current_name:-Claude}"
git config --local committer.email "$current_email"

echo "Commit identity: author $name <$email>, committer ${current_name:-Claude} <$current_email>. Keep the Co-Authored-By trailer on every commit."
