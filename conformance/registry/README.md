# BPMN 2.0.2 conformance candidate registry

The [machine-readable inventory](registry.json) is the **source of truth for the CON0/CON1 provisional catalogue**; the [normative candidate report](../NORMATIVE_COVERAGE.md) is generated from it. This is deliberately separate from the [existing executable-scenario catalogue](../scenario.go) and its [registered-only coverage](../COVERAGE.md).

## Status and denominator

The 105 core semantic **candidate** rows, 13 × 8 event-family/position cells (63 legal, 41 spec-illegal) and 43 Workflow Control-Flow Patterns are **distinct taxonomies**. They cannot be added together into a single percentage. This release makes no claim that the 105 rows exhaust all SHALL-level requirements, and it makes no OMG Process Execution Conformance claim. `denominatorApproved` remains `false`; nothing should automatically switch it to true. CON0 issue [#1615](https://github.com/pblumer/atlas/issues/1615) tracks approval of precise clauses, variant splits and exclusions.

The [normative scope audit](../../docs/planning/bpmn-conformance-con0-audit.md) distinguishes §§2.2–2.5 and the §13.1 non-operational exceptions. BPMN Process Execution (§2.3) has **execution semantics AND process/defining-Collaboration import** obligations; modeling, choreography and BPEL are separate claims. OMG issue [BPMN21-443](https://issues.omg.org/issues/spec/BPMN/2.0.2) makes the Ad-Hoc Process/Sub-Process exemption ambiguous; retain the explicitly unsupported sequential Ad-Hoc Sub-Process, rather than declaring it irrelevant.

## Schema and review semantics

Each requirement has a stable `id`, normative `clause` (currently a section-level pointer, **not** verified SHALL-level mapping), `conformanceClass`, `applicability`, `implementation`, `verification`, `reviewState`, `limitations` and `sourceRefs`/`testRefs`/`scenarioIds`.

- `applicability` expresses **required / non-operational / out-of-scope**, not whether atlas happens to implement the behavior. Invalid event positions have a separate `not-applicable` state.
- `implementation` is **implemented / partial / unsupported / unknown**, provisional until reviewed.
- `verification` is **sample-tested / package-tested / unverified**; a positive sample or negative rejection **never** proves normative conformance. No row is marked spec-reviewed yet.
- `reviewState` is provisional until a maintainer accepts the explicit formal scope, detailed semantics and evidence. `denominatorApproved` cannot be set just because a generator/test passes.
- `wcp` holds all 43 classic workflow control-flow pattern titles; `suiteRegistered` is derived from the **nine** registrations in `conformance/scenario.go`, not an OMG requirement or a claim of semantic WCP implementation.

## Commands

```sh
python3 scripts/conformance_registry.py
python3 scripts/conformance_registry.py --check
python3 -m unittest discover -s scripts -p 'test_conformance_registry.py'
go test ./conformance/...
```

All Python commands use the standard library, are deterministic, require no network or running engine and run in GitHub Actions **for every PR**, including docs-only changes. The validator rejects missing/duplicate candidate IDs; drift of the 13×8 legality matrix or 43 WCPs; missing paths, test names or scenario references; unsupported cases incorrectly claimed as implemented; invalid spec positions counted as gaps; any unapproved normative denominator; and stale report text. To introduce new valid variants, extend the frozen-ID contract deliberately with reviewed references and tests, not by deleting existing requirements.

## Handoff

CON1 issue [#1617](https://github.com/pblumer/atlas/issues/1617) tracks acceptance. CON2 adds independently reviewed positive/negative semantic fixtures; CON3 adds deterministic mid-flight recovery and full-state equivalence; CON4 provides a non-skipping external reference suite. Each consumer must use the **registry as an offline documentation asset**. Never introduce runtime interpretation, hot-path allocations, shared mutable cluster state, cross-partition direct reads, or effects before the WAL fsync. See the [scale-out checklist](../../docs/architecture/scale-out-readiness.md) and [ADR-0175](../../docs/adr/0175-replicated-partition-cells.md). This tooling has no state ownership or recovery format to migrate.
