# Programme Delivery Protocol

This is the mandatory delivery process for work in atlas spanning multiple
reviewable slices, pull requests, decisions, subsystems or agents. It applies
equally to maintainers, contributors and AI coding agents. A single change
still follows [AGENTS.md](../../AGENTS.md) and
[CONTRIBUTING.md](../../CONTRIBUTING.md).

This **adds coordination**, not a second engineering standard: the
[engine invariants](../architecture/invariants.md), TDD, ADR decisions,
recovery tests, and the repository's full verification gates still apply.

## One programme; distinct sources of truth

| Source | Responsibility |
| --- | --- |
| [Programme manifest and generated index](../programs/README.md) | **Operational delivery status**: owner, slices, dependencies, issue/PR references, next action, acceptance evidence |
| [ROADMAP.md](../../ROADMAP.md) | **Strategic intent**: scope, milestones and high-level progression |
| Plan in [docs/planning](../planning/) and [ADRs](../adr/) | **Implementation and architecture contract**: acceptance criteria, constraints, migration, risks, decisions |
| GitHub Issues and PRs | **Execution/review evidence**; no second manually maintained status table |

GitHub Projects may present linked issues and PRs, but must not become
another independently updated delivery registry.

Each programme has one structured source under
**docs/programs/PROGRAMME-ID/program.json**. The index is generated, not
edited by hand. Only Python 3's standard library is required:

    python3 scripts/programs.py
    python3 scripts/programs.py --check
    python3 -m unittest discover -s scripts -p 'test_programs.py'

An optional read-only reference audit queries GitHub without changing
programme state:

    GITHUB_TOKEN=... python3 scripts/programs.py --check --audit-github

Do not commit API tokens or report a merged PR as a completed slice.

## 1. Programme charter before parallel work

The accountable **human programme owner** (or maintainer accepting
programme accountability) establishes:

- An end-to-end user outcome and an executable demonstration to prove it,
  including negative cases, operations, security and replay where applicable.
- Scope, explicit non-goals, relevant invariants, architecture constraints
  and the strategic milestone(s).
- Vertical slices with acceptance gates, owners, dependencies, integration
  order and an explicit next executable step.
- Required ADR decisions and their status. Never implement a change
  contradicting a standing decision or engine invariant as a workaround.
- A detailed programme plan, a ROADMAP milestone and a validated manifest.

A new programme may be registered with owner **unassigned**, but must
show that as an unresolved responsibility. **Do not start additional
parallel slices until a human maintainer accepts programme ownership.**
The initial CDA register intentionally shows this assignment gap.

## 2. One responsible owner per slice

The programme has one accountable owner. Each slice has one coordinating
owner. Other contributors and coding agents can implement parts of that
slice but an agent is an **execution actor**, not assumed to remain online,
retain chat state, or independently own a multi-week programme.

Give every agent assignment:

- The exact programme/slice ID and a linked issue once build work starts.
- Its allowed files/API surface, testable output, branch and integration PR.
- Explicit upstream contracts, decisions and dependency commit/PR.
- Boundaries it must not change and a named handoff/integration recipient.
- Tests and release gates, including recovery and security where relevant.

**Only one owner changes a shared contract at a time.** If agents need
to modify the same API, ADR, events, ROADMAP section or storage format,
first agree and publish the interface in a small reviewable PR. Do not
let parallel branches silently choose incompatible contracts.

## 3. WIP limit and dependency discipline

Default to **at most two active integration slices per programme**, unless
the programme owner records an exception. Research may run in parallel;
unreviewed and unmerged branches are never treated as shipped dependencies.

Before beginning, read the programme manifest, detailed plan, ADRs,
AGENTS.md and relevant tests. Confirm that the slice remains necessary,
its preceding slices are complete or it has an explicitly pinned reviewed
interface, and the latest main has not invalidated its assumptions.
A Proposed decision or missing enforcement is a real gate, not a
signal to code around it.

Prefer vertical end-to-end functionality to isolated layers that have
no runnable or reviewable delivery result.

## 4. PR lifecycle

A focused PR references programme ID, slice, issue, dependency PRs,
applicable ADRs, acceptance tests, review results and remaining work.
Update the linked refs in the programme manifest when the PR opens.

Before any merge:

1. Execute the checks in AGENTS.md: Go build, tests, race, vet, format
   and meaningful coverage. Changes affecting events, state, jobs,
   routing or authorization require deterministic live/replay tests.
2. Review the actual slice acceptance criteria, correctness, visibility,
   privacy, BPMN semantics, backwards compatibility and operations.
3. Refresh against main and resolve merge conflicts without discarding
   concurrent changes; do not force-push shared history silently.
4. Update the manifest **in the same PR**, or a specifically linked
   integration PR where simultaneous editors would conflict. Set
   completion only after all gates; attach evidence and next action.
5. Regenerate the index and update ROADMAP, actual ADR implementation
   status, detailed plan and CHANGELOG when those facts change.

Green CI means **tested**, not **approved**. An approved but open PR
is **not integrated**. A merged PR is **integrated**, not proof that
the complete slice has delivered its user-visible outcome.

## 5. Mandatory handoff after integration

The responsible actor leaves a durable handoff in the PR or issue
and synchronizes the programme manifest:

- **Landed:** merged PR/commit, real behavior and migration notes.
- **Verified:** exact test and CI outcomes; no claims for unrun checks.
- **Outstanding:** incidents, limitations, gaps, with linked follow-up
  issue, responsibility and priority.
- **Next executable slice:** owner, blocker, next action and entry point
  for another agent.
- **Architectural divergence:** stop and submit an ADR amendment or
  replacement before implementation continues.

An agent ending a chat without this handoff has **not completed the
integration task**. New agents recover context from committed records
and GitHub evidence, not from the previous agent's chat memory.

## 6. State meaning and completion gates

| Manifest state | Meaning |
| --- | --- |
| planned | Scoped, not being integrated |
| active | Work/integration in progress (an unassigned owner must be visible) |
| blocked | A named dependency, decision or incident prevents progress; record it in the note |
| paused | Deliberately deferred; record how to restart |
| complete | Integrated, accepted, verified with explicit evidence and no untracked remainder |

**Slice completion** requires approved review, merged implementation PRs,
CI and relevant recovery/security tests, acceptance evidence, matching
ADR/roadmap status, and tracked residual work. A merged PR by itself
cannot set this state.

**Programme completion** requires every slice complete **plus**
a tested end-to-end outcome, documented operations and rollback,
necessary negative/recovery tests, and an explicit owner signoff.
Counting closed PRs is not a substitute.

The generator rejects duplicate/invalid IDs, missing local documents,
unknown/cyclic dependencies, unsupported states, malformed reference
numbers, missing completion evidence and a completed programme
containing unfinished slices. It cannot judge semantic correctness;
that is the maintainer's acceptance responsibility.

## 7. Review cadence and stalled work

At every merge, and at least once per planning cycle for active programmes,
review the [programme index](../programs/README.md). Look for unassigned
owners, waiting ADR decisions, aged handoffs, unmerged or conflicted PRs,
unowned follow-ups and conflicting edits to common contracts.

If a slice stalls, **stop starting unrelated parallel slices**.
Unblock it, reduce it to a meaningful vertical slice, explicitly
pause/cancel it, or document its recovery/next review condition.
Do not leave silent orphan branches or misleading complete flags.

## Pilot: Capability-driven Automation

The [CDA detailed plan](../planning/capability-driven-automation-program.md)
and [CDA manifest](../programs/cda/program.json) form the pilot.
CDA0 has code merged but architecture review still open; CDA1 is a draft;
later slices depend on conformance, graph and identity-policy decisions.
This coordination PR does not deliver the unimplemented runtime features.

This protocol is an engineering working convention, **not a new engine
ADR**. Any later change to runtime, durability, routing or a standing
architecture decision still needs a separately reviewed ADR.
