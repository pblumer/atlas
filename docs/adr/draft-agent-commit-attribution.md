# ADR-DRAFT: An agent's commit is authored by the person who asked for it

- **Status:** Proposed
- **Date:** 2026-09-08
- **Deciders:** Atlas maintainers

## Context and problem statement

Most of the code in this repository is written by an agent working on behalf of a person. Until 2026-09-08 every such commit carried `Claude <noreply@anthropic.com>` in both of git's identity fields. That address is not an unmatched string GitHub ignores — it is registered to the account [`claude`](https://github.com/claude) (id 81847), and GitHub attributes a commit to whichever account holds its **author** address. Of the last 200 commits on `main` before the change, 129 were counted for that account, 41 for `pblumer`, 15 for `dbuchs7` — and every one of those 15 was a merge commit the web UI wrote when a person pressed the button. The people who ask for the work, review it, and carry it appeared nowhere else.

Two things follow from that, and they pull in opposite directions. A contributor graph that names the agent for nearly everything is not a useful record of who is responsible for this codebase. But the author field is also the only machine-readable statement of who wrote a change, and overwriting it wholesale would trade one wrong record for another.

## Decision drivers

- The contributor statistics should name the people accountable for the repository.
- The agent's part in a change must stay on the record, in a form a later reader will actually encounter — not a convention someone has to know about.
- Whatever we choose has to survive an agent session that knows nothing about this decision.
- The repository has more than one human contributor. A mechanism that guesses which one is worse than no mechanism.

## Considered options

1. **Leave it.** Accept that the contributor view names the agent.
2. **Author and committer both become the human.** The agent disappears from git's identity fields entirely, leaving only the `Co-Authored-By:` trailer.
3. **Author becomes the human, committer stays the agent.** Git has carried both fields since its first release and has had separate config keys for them since 2.22.
4. **Rely on the `Co-Authored-By:` trailer alone** and hope the contributor view counts co-authors.

## Decision outcome

Chosen option: **"3 — author becomes the human, committer stays the agent"**, because it is the only option that moves the count without deleting anything. The author field answers "whose change is this", which is what GitHub counts and what the graph is read as; the committer field answers "what produced this commit object", which is the agent; the `Co-Authored-By:` trailer names the model. All three are true simultaneously, and each is visible in `git log` and in the GitHub commit view without anyone needing to know the convention.

Option 4 was rejected for a reason worth writing down: it is documented that co-authored commits count towards a *personal* contribution graph, but we could not establish that the repository's contributors view counts them, and a mechanism whose effect we cannot verify is not a mechanism. Option 2 was rejected because the agent's authorship is a fact about the change, and the committer field costs nothing to keep it in. Option 1 was rejected because the record it produces is wrong in the specific way that matters — it names the tool, not the people answerable for the result.

`.claude/hooks/commit-identity.sh` applies the split at session start, from the table in `.claude/commit-identities` that maps a session's signed-in person to a git identity their GitHub account actually carries. It writes repository-local config only, and only when the commit would otherwise be made under an agent identity, so a human's own clone is untouched.

A hook alone is not enough, which the first four hours after it landed demonstrated: eight agent-authored commits reached `main` from sessions whose containers had been cloned before the hook existed. Nothing failed — that is the whole problem. The mechanism has failure modes that are *silent by construction* (an environment variable that stops being set, a changed default identity, a session that predates the hook, a tool version that does not run project hooks), and their only symptom is a statistic drifting back over months. So the invariant is also checked in CI, on pull requests, where the commits can still be repaired; `.github/workflows/attribution.yml` fails a PR that would add a commit authored by an agent identity, and prints the exact rebase that fixes it.

### Consequences

- **Positive:** The contributor view names the people responsible. The agent's part stays on every commit in two independent places. A silent failure of the hook becomes a red check on the pull request that introduces it, not a discovery a quarter later.
- **Negative / trade-offs accepted:** `git log --author` no longer separates agent-written commits from hand-written ones; the committer field is where that question is answered now. A branch opened before this landed will fail the new check and needs a rebase before it can merge. Someone with no entry in `.claude/commit-identities` gets a red check rather than a silent fallback — deliberately, because the silent fallback is what this record exists to prevent.
- **Follow-ups / risks to watch:** The 129 commits already attributed to the agent stay that way; rewriting them would need a force-push to `main`, which invalidates every clone and every commit SHA cited in these records. The check reads a single address pattern (`@anthropic.com`); an agent identity that does not match it would pass unnoticed.

## Pros and cons of the options

### Option 1 — leave it
- Good: nothing to build, nothing to maintain, no way for it to fail.
- Bad: the repository's public record of who builds it is wrong in the way that matters.

### Option 2 — human as author and committer
- Good: simplest to configure; one pair of config keys.
- Bad: deletes the agent from git's own fields, leaving a trailer as the only trace — and a trailer is a convention, not a field anything enforces.

### Option 3 — split (chosen)
- Good: every fact keeps a field of its own; uses git as designed; verifiable through the GitHub API on any commit.
- Bad: two config keys instead of one, and a mechanism that has to run in every session — which is why the CI check exists.

### Option 4 — trailer only
- Good: no configuration at all; the trailer is already written on every agent commit.
- Bad: we could not establish that it affects the repository's contributor view, so its effect is unverified.

## Links

- [`AGENTS.md`](../../AGENTS.md) § Commit attribution — the operating instruction this record explains
- `.claude/hooks/commit-identity.sh`, `.claude/commit-identities`, `.github/workflows/attribution.yml` — the mechanism and its guard
