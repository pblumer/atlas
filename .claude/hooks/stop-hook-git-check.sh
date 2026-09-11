#!/bin/bash
#
# Claude Code Stop hook: refuse to end a session with work that exists only in
# this container. Copied here so the customisation survives the container it
# runs in; it is NOT executed from the repository. To use it:
#
#   cp .claude/hooks/stop-hook-git-check.sh ~/.claude/stop-hook-git-check.sh
#   chmod +x ~/.claude/stop-hook-git-check.sh
#
# and register it as a Stop hook in ~/.claude/settings.json.
#
# It reads the hook JSON on stdin and exits 2 with a message on stderr when it
# finds uncommitted changes, untracked files, commits GitHub would mark
# Unverified, or commits that exist on no remote ref.

# Read the JSON input from stdin
input=$(cat)

# Check if stop hook is already active (recursion prevention)
stop_hook_active=$(echo "$input" | jq -r '.stop_hook_active')
if [[ "$stop_hook_active" = "true" ]]; then
  exit 0
fi

# Check if we're in a git repository - bail if not
if ! git rev-parse --git-dir >/dev/null 2>&1; then
  exit 0
fi

# Bail if there's no remote to push to. Every error path below asks the user
# to "push to the remote branch" — meaningless without a remote, and
# unsatisfiable if signing also requires a source. This case arises when the
# session was launched against a local repo with no GitHub remote and the
# container's working directory holds a leftover .git from a cached resume.
if [[ -z "$(git remote)" ]]; then
  exit 0
fi

# Check for uncommitted changes (both staged and unstaged)
if ! git diff --quiet || ! git diff --cached --quiet; then
  echo "There are uncommitted changes in the repository. Please commit and push these changes to the remote branch." >&2
  exit 2
fi

# Check for untracked files that might be important
untracked_files=$(git ls-files --others --exclude-standard)
if [[ -n "$untracked_files" ]]; then
  echo "There are untracked files in the repository. Please commit and push these changes to the remote branch." >&2
  exit 2
fi

current_branch=$(git branch --show-current)
if [[ -n "$current_branch" ]]; then
  # Check for local commits that GitHub will show as "Unverified": either no
  # signature at all, or a committer email other than the one the agent's
  # signing key is registered to. Only run when commit signing is configured.
  #
  # Signature presence is tested via the raw gpgsig/gpgsig-sha256 header, NOT
  # %G?. The remote environment configures SSH signing but never sets
  # gpg.ssh.allowedSignersFile, so %G? reports 'N' even for correctly
  # SSH-signed commits.
  #
  # Scope to commits that are on NO remote ref. A "$upstream..HEAD" range
  # would sweep in teammates' already-published commits whenever this branch
  # was cut from a feature branch that has no same-named remote branch yet
  # (github.com/anthropics/claude-code#69586).
  #
  # Gate on origin/$current_branch resolving, and deliberately not on
  # origin/HEAD: a single-branch or shallow clone checked out on a
  # non-default branch has origin/HEAD but no ref covering that branch's
  # base, so 'HEAD --not --remotes' would sweep in published commits there.
  # SHA-pinned init+fetch checkouts do not set origin/$current_branch either,
  # and a later 'git fetch origin main' does not set it, so those shapes skip
  # this block — as they did before. This also skips agent-created branches
  # ('git checkout -b fix-xyz') in full clones: a deliberate false-negative
  # window, traded for never re-introducing an origin/HEAD fallback, whose
  # destructive rewrite advice on shallow clones is the bug this gate fixes.
  if [[ "$(git config --type=bool commit.gpgsign 2>/dev/null)" == "true" ]] &&
     git rev-parse -q --verify "origin/$current_branch" >/dev/null 2>&1; then
    local_count="$(git rev-list HEAD --not --remotes --count 2>/dev/null)"
    if [[ -n "$local_count" && "$local_count" -gt 0 ]]; then
      unverifiable=""
      while read -r sha ce; do
        if [[ "$ce" != "noreply@anthropic.com" ]] ||
           ! git cat-file commit "$sha" 2>/dev/null | sed '/^$/q' | grep -qE '^gpgsig(-sha256)? '; then
          unverifiable+="${sha:0:7} $ce"$'\n'
        fi
      done < <(git log --format='%H %ce' HEAD --not --remotes 2>/dev/null)
      if [[ -n "$unverifiable" ]]; then
        # Derive a safe rebase boundary: the parent of the oldest local-only
        # commit, falling back to --root when it has none. Only advise the
        # range rebase when local-only history is a linear chain on top of
        # the boundary — rebase replays the whole <boundary>..HEAD range,
        # not the 'on no remote' set, so a non-linear graph (e.g. after
        # 'git merge origin/main') would replay+reset-author the published
        # commits brought in by the merge.
        oldest_local="$(git rev-list HEAD --not --remotes 2>/dev/null | tail -1)"
        if git rev-parse -q --verify "$oldest_local^" >/dev/null 2>&1; then
          rebase_onto="$oldest_local^"
          range_count="$(git rev-list "$rebase_onto..HEAD" --count 2>/dev/null)"
        else
          rebase_onto="--root"
          range_count="$(git rev-list HEAD --count 2>/dev/null)"
        fi
        echo "There are commit(s) on branch '$current_branch' that GitHub will show as Unverified (missing signature, or committer email is not noreply@anthropic.com):" >&2
        printf '%s' "$unverifiable" >&2
        if [[ "$range_count" == "$local_count" ]]; then
          echo "Please run 'git config user.email noreply@anthropic.com && git config user.name Claude', then 'git commit --amend --no-edit --reset-author' for the tip commit, or 'git rebase --exec \"git commit --amend --no-edit --reset-author\" $rebase_onto' for earlier commits, then push." >&2
        else
          echo "Please run 'git config user.email noreply@anthropic.com && git config user.name Claude', then 'git commit --amend --no-edit --reset-author' for each listed commit (local history is non-linear, so a range rebase would rewrite published commits), then push." >&2
        fi
        exit 2
      fi
    fi
  fi

  # Count commits that are on NO remote ref — the only ones that can actually be
  # lost. Deliberately NOT "origin/$current_branch..HEAD": that measures distance
  # from one ref, so resetting a branch onto an up-to-date base after its PR has
  # merged ('git checkout -B <branch> origin/main', which the remote session's
  # branch rules prescribe for follow-up work) reports every commit the base gained
  # meanwhile — teammates' commits included — as unpushed local work, when
  # nothing local is unpublished at all. Same primitive and same rationale as
  # the signature block above (github.com/anthropics/claude-code#69586).
  #
  # Nor "origin/<default-branch>..HEAD": that would flag a feature branch that
  # *has* been pushed to its own remote branch but is not yet merged, which is
  # the normal state of work in progress.
  unpushed=$(git rev-list HEAD --not --remotes --count 2>/dev/null) || unpushed=0
  if [[ "$unpushed" -gt 0 ]]; then
    if git rev-parse -q --verify "origin/$current_branch" >/dev/null 2>&1; then
      echo "There are $unpushed unpushed commit(s) on branch '$current_branch'. Please push these changes to the remote repository." >&2
    else
      echo "Branch '$current_branch' has $unpushed unpushed commit(s) and no remote branch. Please push these changes to the remote repository." >&2
    fi
    exit 2
  fi
fi

exit 0
