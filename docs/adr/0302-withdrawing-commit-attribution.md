# ADR-0302: The repository stops rewriting who authored an agent's commit

- **Status:** Accepted
- **Implementation:** Landed
- **Date:** 2026-09-11
- **Deciders:** Atlas maintainers

## Context and problem statement

[ADR-0288](0288-agent-commit-attribution.md) decided on 2026-09-08 that a commit an
agent makes here is *authored* by the person who asked for it and *committed* by the
agent, so that GitHub counts the change for a person rather than for the account
`claude`. Three pieces implemented it: `.claude/hooks/commit-identity.sh` set
`author.*` and `committer.*` at session start, `.claude/commit-identities` corrected a
sign-in address GitHub could not place, and `.github/workflows/attribution.yml` failed
a pull request whose commits still named an agent — because a hook fails silently and
CI is the only place the invariant can still be repaired.

The mechanism did what it claimed. Of the 173 commits that reached `main` between the
hook landing (`c86dee0`, 2026-09-08 12:34 UTC) and `31e2eff` (2026-09-10), 164 name a
person as author. The nine that still name the agent are all dated 2026-09-08, from
sessions whose containers were cloned before the hook existed — the failure ADR-0288
itself predicted and built the check for.

What it also does is prescribe a history rewrite. When the check fails, the only remedy
it offers — and the only one that works — is

```bash
git rebase origin/main --exec 'git commit --amend --no-edit --author=…'
git push --force-with-lease
```

over the *whole* branch, because the author field is part of every commit object and
cannot be corrected in place. `attribution.yml` ran 59 times between 2026-09-08 and
2026-09-10 and failed four, on four different branches, each of which then had to be
rewritten end to end. Branches here routinely carry `Merge main: …` commits; a rebase
replays what those merges settled, so a conflict resolved once comes back to be
resolved again, and the rewritten branch conflicts against `main` afresh on its next
merge. The maintainers report this as the recurring cost: conflicts, repeatedly, on
branches that were mergeable before the check asked for the rewrite.

That is the trade this record settles. The contributor graph is a reporting nicety.
The branch rewrites are work, on every branch that trips the check, and they land on
the part of the process that is hardest to automate — resolving a conflict somebody
already resolved.

## Decision drivers

- A repository convention should not make a mergeable branch unmergeable.
- The provenance of a change — what wrote it — must stay on the record either way.
- Half a mechanism is a different decision, not a cheaper version of the same one: the
  hook without the check is exactly the silent-drift shape ADR-0288 argued against.
- Whatever is decided has to hold in a session that knows nothing about this record.

## Considered options

1. **Keep the mechanism as ADR-0288 left it.**
2. **Keep the hook, drop the CI check.**
3. **Remove the hook, the table, and the check** — the repository configures no commit
   identity at all.
4. **Re-author on `main` instead of on the branch**, with a workflow that amends after
   merge.

## Decision outcome

Chosen option: **"3 — remove the mechanism"**, because the cost it imposes is paid on
every branch it touches and the benefit it buys is a statistic. The contributor graph
will again count agent sessions for the account `claude`; the maintainers accept that
in exchange for never being asked to rewrite a branch's history to satisfy a check.

Option 2 is the strongest alternative and deserves saying plainly why it is not the
choice: the hook is where the whole effect comes from, it costs nothing at merge time,
it never prescribes a rewrite, and the measurements above show it working. Its weakness
is the one ADR-0288 wrote down and then proved within four hours — a hook that stops
running fails silently, and its only symptom is a number drifting back over months.
Keeping it without the check does not make that failure less likely; it makes it
invisible again, which returns the repository to a mechanism whose effect nobody
verifies. If the graph becomes worth having again, restoring the hook alone is a small
change and the honest first move — but it is a decision to take then, with its guard
argued again, not a remnant to leave behind now.

Option 4 was rejected because it rewrites `main`: every commit SHA cited in these
records and in code comments would change, and every clone would be invalidated —
ADR-0288 declined the same move for the 129 commits that predated it.

### Consequences

- **Positive:** No branch is ever asked to rewrite its history for an attribution
  check, so the conflicts that rewrite produced stop. A pull request can no longer be
  red for who signed a commit. A clone of this repository behaves like any other:
  `.claude/` carries no commit machinery and git's identity is whatever the session
  or the person configured.
- **Negative / trade-offs accepted:** Commits made by an agent session count for the
  GitHub account `claude` again, so the contributor view names the tool rather than the
  people — the exact defect ADR-0288 was written to fix, knowingly reaccepted. Commits
  already attributed to people stay attributed to them; nothing is rewritten backwards.
- **Follow-ups / risks to watch:** Provenance does not depend on any of this: every
  agent commit keeps its `Co-Authored-By:` trailer, and the committer field still names
  whatever produced the commit object, both visible in `git log` and in the GitHub
  commit view. If the contributor statistics matter again, read ADR-0288 first — the
  argument for the split is unchanged; only the price of enforcing it was.

## Pros and cons of the options

### Option 1 — keep it
- Good: the contributor view names the people accountable for the repository; the
  invariant is guarded where it can still be repaired.
- Bad: four branch-wide history rewrites in three days, and the conflicts each one
  reopens.

### Option 2 — hook only, no check
- Good: keeps the effect, drops the part that prescribes the rewrite; measurably works
  in the sessions that run it.
- Bad: unguarded, it fails silently by construction — the condition ADR-0288 documented
  and the nine commits of 2026-09-08 demonstrated.

### Option 3 — remove it (chosen)
- Good: nothing left to fail, nothing left to enforce, no rewrite ever prescribed.
- Bad: gives up the contributor statistics the split bought.

### Option 4 — re-author after merge
- Good: branches are never touched.
- Bad: rewrites `main`, invalidating every clone and every SHA these records cite.

## Links

- [ADR-0288](0288-agent-commit-attribution.md) — the decision this record withdraws;
  its argument for the split, and the failure modes of enforcing it, still stand
- [`AGENTS.md`](../../AGENTS.md) § Commit attribution — the operating instruction that
  follows from this record
