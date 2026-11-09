## Why / outcome

<!-- What user-observable behavior, correction, or delivery gate does this PR provide? -->

## Programme and slice (if applicable)

- Programme ID / slice ID:
- Programme owner / integration owner:
- Linked issue:
- Dependency PRs and accepted ADRs:
- Paths/operations intentionally out of scope:

## Scale-out readiness (mandatory)

<!-- Review docs/architecture/scale-out-readiness.md for EVERY feature.
Do not describe proposed ADR-0175 functionality as implemented. -->
- [ ] Reviewed for horizontal scale-out compatibility (or documented why N/A)
- Assessment: Compatible / Requires follow-up / N/A (reason):
- State owner and request/job routing:
- Durability, retry/idempotency, failover/fencing and side-effect visibility:
- Cross-node reads / consistency / service-local state, if relevant:
- Standalone compatibility and any required migration seam:
- Linked ADR, issue or Milestone-5 follow-up for a remaining dependency:

## Verification

<!-- State the commands actually run and their results; do not claim success for pending or unrun tests. -->
- [ ] Applicable Go build, tests, race, vet, format and coverage checks passed
- [ ] Recovery/security/compatibility tests added where required, or reason documented
- [ ] Applicable documentation and API/MCP/Modeler surfaces reviewed

## Delivery and handoff (for programme PRs)

- [ ] Programme manifest updated with this PR, status and next action
- [ ] Generated docs/programs/README.md checked (python3 scripts/programs.py --check)
- [ ] ROADMAP/ADR/plan statuses reconciled, or deferred follow-up issue linked
- [ ] Explicit remaining work and next owner / executable slice recorded

<!-- A green CI or merged PR does not by itself complete the slice. The integration owner confirms acceptance evidence. -->
