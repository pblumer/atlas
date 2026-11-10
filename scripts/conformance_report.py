#!/usr/bin/env python3
"""CON5: render the public BPMN conformance report and keep the documented counts true.

Two outputs, both derived, never hand-maintained:

* conformance/CONFORMANCE_REPORT.md, rendered from the validated candidate register,
  the programme manifest and the test sources (suite register, TCK cases, the
  differential subset and the mid-flight restart table);
* the counts written between ``<!-- con:NAME -->`` and ``<!-- /con -->`` markers in the
  documentation, so a page cannot state a number the sources no longer have.

``--check`` fails if either has drifted; without it the script rewrites both.
No percentage is printed while the register's denominator is not approved.
"""
from __future__ import annotations

import argparse
from collections import Counter
import json
from pathlib import Path
import re
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))
import conformance_registry as registry  # noqa: E402

ROOT = registry.ROOT
REPORT = ROOT / "conformance" / "CONFORMANCE_REPORT.md"
MANIFEST = ROOT / "docs" / "programs" / "bpmn-conformance" / "program.json"
SCENARIOS = ROOT / "conformance" / "scenario.go"
TCK = ROOT / "conformance" / "tck"
DIFFERENTIAL = ROOT / "conformance" / "differential" / "differential_test.go"
RECOVERY = ROOT / "conformance" / "recovery_midflight_test.go"
RECOVERY_TEST = "conformance/recovery_midflight_test.go:TestCON3MidflightRecoveryAndContinuation"
# Documents whose counts are generated. A marker anywhere else is not rewritten.
MARKED_DOCS = ("README.md", "ROADMAP.md", "conformance/README.md", "conformance/tck/README.md",
               "conformance/differential/README.md")
MARKER = re.compile(r"<!-- con:([a-z-]+) -->(.*?)<!-- /con -->")


class ReportError(ValueError):
    """The sources disagree, or the report or a documented count has drifted."""


def need(value: bool, reason: str) -> None:
    if not value:
        raise ReportError(reason)


def go_block(source: str, opener: str) -> str:
    need(opener in source, f"source no longer declares {opener!r}")
    return source.split(opener, 1)[1].split("\n}", 1)[0]


def suite_facts() -> dict:
    """Counts and lists read from the test sources themselves."""
    go = SCENARIOS.read_text(encoding="utf-8")
    features = re.findall(r'^\s*\{"([a-z0-9-]+)", "', go_block(go, "var Features = []Feature{"), re.M)
    patterns = re.findall(r'"(WCP-\d+)"', go_block(go, "var Patterns = []Pattern{"))
    scenarios = re.findall(r'\{Name: "([^"]+)", Model:', go)
    negatives = re.findall(r'^\s*\{"([a-z0-9-]+)", "', go_block(go, "var NegativeModels = []NegativeModel{"), re.M)
    tck_cases = sorted(p.name for p in (TCK / "cases").iterdir() if p.is_dir())
    tck_negative = sorted(p.name for p in (TCK / "negative").iterdir())
    differential = re.findall(r'^\s*"([a-z0-9-]+)":\s*"[^"]+\.bpmn",',
                              go_block(DIFFERENTIAL.read_text(encoding="utf-8"), "var subset = map[string]string{"), re.M)
    recovery = re.findall(r'^\s*\{"([a-z0-9-]+)", (\d+), "([a-z]+)"\},',
                          RECOVERY.read_text(encoding="utf-8"), re.M)
    need(set(tck_cases) == set(scenarios), "TCK cases and registered scenarios differ")
    need(set(differential) <= set(scenarios), "differential subset names an unregistered scenario")
    need({name for name, _, _ in recovery} <= set(scenarios), "restart table names an unregistered scenario")
    return {
        "features": features, "patterns": patterns, "scenarios": scenarios, "negatives": negatives,
        "tck_cases": tck_cases, "tck_negative": tck_negative, "differential": sorted(differential),
        "recovery": [(name, int(step), kind) for name, step, kind in recovery],
    }


def deviation_check(data: dict) -> None:
    ids = {r["id"] for r in data["requirements"]}
    seen = set()
    for d in data.get("deviations", []):
        need(re.fullmatch(r"DEV-\d{2}", d.get("id", "")) is not None and d["id"] not in seen,
             f"deviation id missing or duplicate: {d.get('id')}")
        seen.add(d["id"])
        need(d.get("requirement") in ids, f"{d['id']}: names no registered requirement")
        need(bool(d.get("statement", "").strip()), f"{d['id']}: no statement")
        need(bool(d.get("adr")), f"{d['id']}: a deviation must cite the decision that made it")
        for path in d["adr"]:
            registry.source_file(path)


def counts(data: dict, facts: dict) -> dict:
    reqs = data["requirements"]
    return {
        "candidates": len(reqs),
        "legal-event-cells": sum(e["legal"] for e in data["eventPositions"]),
        "wcp": len(data["wcp"]),
        "features": len(facts["features"]),
        "patterns": len(facts["patterns"]),
        "scenarios": len(facts["scenarios"]),
        "negatives": len(facts["negatives"]),
        "tck-cases": len(facts["tck_cases"]),
        "tck-negatives": len(facts["tck_negative"]),
        "differential": len(facts["differential"]),
        "tck-not-independent": len(facts["tck_cases"]) - len(facts["differential"]),
        "restart-checkpoints": len(facts["recovery"]),
    }


def cell(x: object) -> str:
    return registry.cell(x)


def render(data: dict, manifest: dict, facts: dict) -> str:
    reqs = data["requirements"]
    n = counts(data, facts)
    differential = set(facts["differential"])
    out = [
        "# BPMN 2.0.2 conformance report (provisional)", "",
        "<!-- Generated by scripts/conformance_report.py; do not edit manually. -->", "",
        "**This is not an OMG conformance claim or certification.** The normative denominator is "
        + ("approved." if data["denominatorApproved"] else "**not approved**, so this report gives counts, never percentages."),
        "", "Every figure below is read from the [candidate register](registry/registry.json), the "
        "[programme manifest](../docs/programs/bpmn-conformance/program.json) and the test sources; "
        "a figure that is not derivable from them is not stated.", "",
        "## Review state", "",
        f"- Standard: {data['standard']}; target class: `{data['target']}`.",
        f"- Register review state: `{data['reviewState']}` (reviewed {data['reviewDate']}); "
        f"denominator approved: **{'yes' if data['denominatorApproved'] else 'no'}**.",
        "- Rows independently reviewed against the specification: **0**.", "",
        "| Slice | Status | Title |", "| --- | --- | --- |",
    ]
    for s in manifest["slices"]:
        out.append(f"| {s['id']} | {s['status']} | {cell(s['title'])} |")
    out += ["", "A slice is `complete` only when its owner accepts it; green CI is not acceptance.", "",
            "## Candidates by conformance class", "",
            "Each class is its own denominator. Rows outside `process-execution` and `process-import` "
            "never count towards execution support.", "",
            "| Class | Rows | Implemented | Partial | Unsupported | Unknown | Scenario sample | Package test | Unverified |",
            "| --- | --- | --- | --- | --- | --- | --- | --- | --- |"]
    by_class: dict[str, list] = {}
    for r in reqs:
        by_class.setdefault(r["conformanceClass"], []).append(r)
    for cls in sorted(by_class):
        rows = by_class[cls]
        impl = Counter(r["implementation"] for r in rows)
        ver = Counter(r["verification"] for r in rows)
        out.append(f"| `{cls}` | {len(rows)} | {impl['implemented']} | {impl['partial']} | {impl['unsupported']} "
                   f"| {impl['unknown']} | {ver['sample-tested']} | {ver['package-tested']} | {ver['unverified']} |")
    execution = [r for r in reqs if r["conformanceClass"] == "process-execution"]
    restart = [r for r in execution if RECOVERY_TEST in r["testRefs"]]
    independent = [r for r in execution if differential & set(r["scenarioIds"])]
    out += ["", "## Evidence strength (process execution)", "",
            "The axes are separate; a row can sit on several. None of them is a specification review.", "",
            "| Evidence | Rows |", "| --- | --- |",
            f"| Registered scenario sample (golden trace and replay projection) | "
            f"{sum(r['verification'] == 'sample-tested' for r in execution)} |",
            f"| At least one linked test function (package or CON2/CON3 oracle) | {sum(bool(r['testRefs']) for r in execution)} |",
            f"| Mid-flight restart from the WAL into a fresh store | {len(restart)} |",
            f"| Independent reference engine (bpmn-engine) | {len(independent)} |",
            "| Specification-reviewed expected outcome | 0 |", ""]

    def listing(title: str, rows: list, note: str) -> None:
        out.extend([f"## {title} ({len(rows)})", "", note, ""])
        if not rows:
            out.extend(["None.", ""])
            return
        out.extend(["| ID | Candidate | Section | Limitations |", "| --- | --- | --- | --- |"])
        for r in rows:
            out.append(f"| {r['id']} | {cell(r['title'])} | {r['clause']} | {cell(r['limitations'])} |")
        out.append("")

    listing("Unsupported", [r for r in reqs if r["implementation"] == "unsupported"],
            "Refused at deploy with a diagnostic. A refusal is evidence of a truthful diagnostic, never of support.")
    listing("Partially supported", [r for r in reqs if r["implementation"] == "partial"],
            "Some variants run; the limitations say which do not.")
    listing("Implemented but without runnable evidence",
            [r for r in reqs if r["implementation"] == "implemented" and r["verification"] == "unverified"],
            "Code exists; no test in this repository shows the variant running. Not a pass.")
    listing("Unknown", [r for r in reqs if r["implementation"] == "unknown"],
            "Not yet classified by source audit. Unknown is never counted as supported.")
    out += ["## Known deviations", "",
            "Behaviour atlas chose deliberately where the specification says otherwise or leaves the "
            "outcome to the engine; each is decided in the cited record.", "",
            "| ID | Candidate | Deviation | Decided in |", "| --- | --- | --- | --- |"]
    for d in data.get("deviations", []):
        links = ", ".join(f"[{Path(p).stem.split('-')[0]}](../{p})" for p in d["adr"])
        out.append(f"| {d['id']} | {d['requirement']} | {cell(d['statement'])} | {links} |")
    legal = [e for e in data["eventPositions"] if e["legal"]]
    out += ["", "## Event positions and control-flow patterns", "",
            f"- Event-position cells (OMG Table 10.93): **{n['legal-event-cells']}** legal, "
            f"**{len(data['eventPositions']) - n['legal-event-cells']}** spec-illegal. Implementation of the legal cells: "
            + ", ".join(f"{k} {v}" for k, v in sorted(Counter(e['implementation'] for e in legal).items())) + ".",
            f"- Workflow Control-Flow Patterns: **{n['wcp']}** inventoried, **{n['patterns']}** realised by a "
            "registered scenario, **0** independently verified. WCP is a separate axis, not OMG conformance.", "",
            "## Test assets", "",
            f"- Registered suite: **{n['features']}** features, **{n['patterns']}** patterns, "
            f"**{n['scenarios']}** positive scenarios, **{n['negatives']}** negative models.",
            f"- Portable TCK: **{n['tck-cases']}** positive cases, **{n['tck-negatives']}** negative models.",
            f"- Independent reference engine: **{n['differential']}** translated cases ("
            + ", ".join(f"`{x}`" for x in facts["differential"]) + f"); the other **{n['tck-not-independent']}** "
            "TCK cases are checked by atlas alone.",
            f"- Mid-flight restart checkpoints: **{n['restart-checkpoints']}** ("
            + ", ".join(f"`{name}@{step}` {kind}" for name, step, kind in facts["recovery"]) + ").", "",
            "## Regenerate", "",
            "`python3 scripts/conformance_report.py` rewrites this report and the marked counts in the "
            "documentation; `--check` fails if either has drifted.", ""]
    return "\n".join(out)


def check_no_percentage(report: str, data: dict) -> None:
    """Until the denominator is approved, a percentage would read as a conformance figure."""
    if not data["denominatorApproved"]:
        need("%" not in report, "a percentage appears although the denominator is not approved")


def rewrite_markers(text: str, values: dict, where: str) -> str:
    def put(m: re.Match) -> str:
        name = m.group(1)
        need(name in values, f"{where}: unknown count marker {name!r}")
        return f"<!-- con:{name} -->{values[name]}<!-- /con -->"
    return MARKER.sub(put, text)


def main() -> int:
    args = argparse.ArgumentParser(description=__doc__)
    args.add_argument("--check", action="store_true")
    opts = args.parse_args()
    try:
        data = json.loads(registry.REGISTRY.read_text(encoding="utf-8"))
        registry.validate(data)
        deviation_check(data)
        manifest = json.loads(MANIFEST.read_text(encoding="utf-8"))
        facts = suite_facts()
        values = counts(data, facts)
        report = render(data, manifest, facts)
        check_no_percentage(report, data)
        drift = []
        if (REPORT.read_text(encoding="utf-8") if REPORT.is_file() else "") != report:
            drift.append(str(REPORT.relative_to(ROOT)))
            if not opts.check:
                REPORT.write_text(report, encoding="utf-8")
        for rel in MARKED_DOCS:
            path = ROOT / rel
            text = path.read_text(encoding="utf-8")
            updated = rewrite_markers(text, values, rel)
            if updated != text:
                drift.append(rel)
                if not opts.check:
                    path.write_text(updated, encoding="utf-8")
        if opts.check and drift:
            raise ReportError("out of date, run python3 scripts/conformance_report.py: " + ", ".join(drift))
    except (OSError, json.JSONDecodeError, registry.RegistryError, ReportError) as exc:
        print(f"CONFORMANCE REPORT ERROR: {exc}", file=sys.stderr)
        return 1
    print("CON5 conformance report and documented counts are current")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
