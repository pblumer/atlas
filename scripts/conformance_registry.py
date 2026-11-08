#!/usr/bin/env python3
"""Validate and render the provisional OMG BPMN 2.0.2 candidate inventory."""
from __future__ import annotations

import argparse
import json
from pathlib import Path
import re
import sys
from collections import Counter

ROOT = Path(__file__).resolve().parents[1]
REGISTRY = ROOT / "conformance" / "registry" / "registry.json"
REPORT = ROOT / "conformance" / "NORMATIVE_COVERAGE.md"
BASELINE = {"FLOW": 16, "ACT": 22, "EVT": 44, "DATA": 9, "COLL": 6, "MODEL": 8}
EXPECTED = {f"{prefix}-{index:02d}" for prefix, n in BASELINE.items() for index in range(1, n + 1)}
POSITIONS = ("top-start", "event-subprocess-start-interrupting", "event-subprocess-start-noninterrupting",
             "intermediate-catch", "boundary-interrupting", "boundary-noninterrupting", "intermediate-throw", "end")
# OMG BPMN 2.0.2 Table 10.93 (pp. 259-260). A zero denotes spec-illegal, not unsupported.
LEGALITY = {"None": "10000011", "Message": "11111111", "Timer": "11111100",
            "Error": "01001001", "Escalation": "01101111", "Cancel": "00001001",
            "Compensation": "01001011", "Conditional": "11111100", "Link": "00010010",
            "Signal": "11111111", "Terminate": "00000001", "Multiple": "11111111",
            "Parallel Multiple": "11111100"}
UNSUPPORTED = {"FLOW-08", "FLOW-09", "FLOW-10", "ACT-12", "EVT-05"}
IMPL = {"implemented", "partial", "unsupported", "unknown"}
VERIFY = {"sample-tested", "package-tested", "unverified"}
CLASSES = {"process-execution", "process-import", "process-modeling", "choreography-modeling", "bpel-execution"}


class RegistryError(ValueError):
    """Invalid or misleading inventory."""


def need(value: bool, reason: str) -> None:
    if not value:
        raise RegistryError(reason)


def source_file(path: str) -> Path:
    need(isinstance(path, str) and bool(path) and "://" not in path, f"invalid reference: {path}")
    target = (ROOT / path).resolve()
    need(target.is_relative_to(ROOT) and target.is_file(), f"missing/outside repo: {path}")
    return target


def validate(data: dict) -> None:
    need(isinstance(data, dict) and data.get("schemaVersion") == 1, "schemaVersion must be 1")
    need(data.get("standard") == "OMG BPMN 2.0.2", "wrong normative edition")
    need(data.get("target") == "process-execution", "wrong conformance class")
    need(data.get("denominatorApproved") is False and data.get("reviewState") == "provisional",
         "normative denominator requires separate reviewed owner sign-off")
    reqs = data.get("requirements", [])
    need(isinstance(reqs, list), "requirements must be an array")
    ids = [item["id"] for item in reqs]
    need(len(ids) == len(set(ids)), "duplicate requirement id")
    need(set(ids) == EXPECTED, f"frozen CON0 IDs changed: missing={sorted(EXPECTED-set(ids))} extra={sorted(set(ids)-EXPECTED)}")
    go = source_file("conformance/scenario.go").read_text(encoding="utf-8")
    scenarios = set(re.findall(r'\{Name: "([^"]+)", Model:', go))
    pattern_source = go.split("var Patterns = []Pattern{", 1)[1].split("\n}", 1)[0]
    registered = set(re.findall(r'"(WCP-\d+)"', pattern_source))
    for item in reqs:
        ident = item["id"]
        need(isinstance(item.get("title"), str) and item["title"].strip(), f"{ident}: title absent")
        need(bool(re.fullmatch(r"\d+(?:\.\d+)*", item.get("clause", ""))), f"{ident}: invalid OMG section")
        need(item.get("conformanceClass") in CLASSES, f"{ident}: invalid conformanceClass")
        need(item.get("applicability") in {"required", "non-operational", "out-of-scope"}, f"{ident}: invalid applicability")
        need(item.get("implementation") in IMPL and item.get("verification") in VERIFY, f"{ident}: invalid status")
        need(item.get("reviewState") == "provisional", f"{ident}: unapproved certification")
        need(bool(item.get("limitations")), f"{ident}: missing context")
        if item["conformanceClass"] not in {"process-execution", "process-import"}:
            need(item["applicability"] == "out-of-scope", f"{ident}: separate conformance class counted in execution")
        source_refs = item.get("sourceRefs", [])
        need(isinstance(source_refs, list) and len(source_refs) > 0, f"{ident}: no code references")
        for path in source_refs:
            source_file(path)
        tests = item.get("testRefs", [])
        need(isinstance(tests, list), f"{ident}: testRefs not a list")
        for ref in tests:
            need(isinstance(ref, str) and ref.count(":") == 1, f"{ident}: malformed test reference {ref}")
            path, name = ref.split(":", 1)
            need(re.fullmatch(r"Test\w+", name) is not None and f"func {name}(" in source_file(path).read_text(encoding="utf-8"),
                 f"{ident}: missing test {ref}")
        links = item.get("scenarioIds", [])
        need(isinstance(links, list) and len(links) == len(set(links)) and set(links) <= scenarios,
             f"{ident}: missing/duplicate scenario reference")
        if item["verification"] == "sample-tested":
            need(bool(links), f"{ident}: sample evidence missing")
        elif item["verification"] == "package-tested":
            need(bool(tests), f"{ident}: package-test evidence missing")
        else:
            need(not links and not tests, f"{ident}: evidence exists but marked unverified")
    by_id = {r["id"]: r for r in reqs}
    for ident in UNSUPPORTED:
        need(by_id[ident]["implementation"] == "unsupported", f"{ident}: known gap silently marked supported")
    need(by_id["EVT-09"]["implementation"] == "partial", "intermediate timeCycle cannot be claimed fully implemented")
    need(by_id["ACT-01"]["applicability"] == "non-operational", "`13.1 Manual/Abstract Task exemption lost")
    need(by_id["MODEL-07"]["conformanceClass"] == "bpel-execution", "BPEL is a separate conformance class")

    events = data.get("eventPositions", [])
    need(isinstance(events, list) and len(events) == 104, "event matrix must have exactly 13x8 cells")
    keyed = {(e.get("family"), e.get("position")): e for e in events}
    need(len(keyed) == 104, "duplicate event matrix cell")
    for family, mask in LEGALITY.items():
        for i, position in enumerate(POSITIONS):
            item = keyed.get((family, position))
            ident = "EVPOS-" + family.upper().replace(" ", "-") + "-" + position.upper()
            need(item is not None and item.get("id") == ident, f"missing event cell {ident}")
            legal = mask[i] == "1"
            need(item.get("legal") is legal and item.get("clause") == "10.5.5", f"{ident}: contradicts OMG table")
            need(item.get("reviewState") == "provisional", f"{ident}: approved without review")
            if legal:
                need(item.get("applicability") == "required" and item.get("implementation") in IMPL
                     and item.get("verification") == "unverified", f"{ident}: unfounded normative pass")
            else:
                need((item.get("applicability"), item.get("implementation"), item.get("verification"))
                     == ("not-applicable", "not-applicable", "not-applicable"), f"{ident}: illegal construct counted as gap")
    need(sum(x["legal"] for x in events) == 63, "wrong event legal-cell total")
    need(keyed[("Conditional", "top-start")]["implementation"] == "unsupported", "root conditional start gap lost")

    patterns = data.get("wcp", [])
    need(isinstance(patterns, list) and len(patterns) == 43, "WCP inventory must have 43 patterns")
    need({p["id"] for p in patterns} == {f"WCP-{n}" for n in range(1, 44)}, "WCP identifier missing")
    need({p["id"] for p in patterns if p.get("suiteRegistered")} == registered,
         "WCP registration drift from scenario.go")
    need(registered == {"WCP-1", "WCP-2", "WCP-3", "WCP-4", "WCP-5", "WCP-6", "WCP-7", "WCP-16", "WCP-21"},
         "suite changed: review WCP baseline")
    for item in patterns:
        need(bool(item.get("name")) and bool(item.get("group")), "WCP title/category missing")
        need(item.get("implementation") == "unknown" and item.get("verification") == "unverified"
             and item.get("reviewState") == "provisional", f"{item['id']}: registration is not proof")
    need(next(x for x in patterns if x["id"] == "WCP-37")["name"] == "Acyclic Synchronizing Merge",
         "WCP-37 must match 2006 revised patterns source")


def cell(x: object) -> str:
    return str(x).replace("|", "\\|").replace("\n", " ").strip()


def render(data: dict) -> str:
    reqs, events, patterns = data["requirements"], data["eventPositions"], data["wcp"]
    counts = Counter(r["implementation"] for r in reqs)
    proof = Counter(r["verification"] for r in reqs)
    out = [
        "# BPMN 2.0.2 normative candidate coverage", "",
        "<!-- Generated by scripts/conformance_registry.py; do not edit manually. -->", "",
        "**No OMG conformance or certification claim. Normative denominator NOT approved.**", "",
        "This is a source/test inventory of candidates, not a complete SHALL-level assessment.",
        "A covering scenario or compiler test does not establish full normative semantics or crash recovery.",
        "The legacy [scenario-local report](COVERAGE.md) remains a different measure.", "",
        "## Provisional inventory", "",
        f"- Candidate requirements: **{len(reqs)}**; implementation provisionally "
        f"**{counts['implemented']}** implemented, **{counts['partial']}** partial, "
        f"**{counts['unsupported']}** unsupported, **{counts['unknown']}** unknown.",
        f"- Evidence levels: **{proof['sample-tested']}** scenario samples, "
        f"**{proof['package-tested']}** package tests, **{proof['unverified']}** unverified; "
        "**0 independently specification-reviewed passes**.",
        f"- Event-position cells: **{sum(e['legal'] for e in events)} legal**, "
        f"**{sum(not e['legal'] for e in events)} spec-illegal**, not a normative support denominator.",
        f"- Workflow Control-Flow Patterns: **{len(patterns)}** inventoried; "
        f"**{sum(p['suiteRegistered'] for p in patterns)}** registered; "
        "**0 independently verified WCP semantics**. WCP is not OMG conformance.", "",
        "## Candidate requirements", "",
        "| ID | Candidate variant | OMG section | Conformance class | Applicability | Implementation | Evidence |",
        "| --- | --- | --- | --- | --- | --- | --- |",
    ]
    for r in reqs:
        out.append("| " + " | ".join(cell(r[k]) for k in
                   ("id", "title", "clause", "conformanceClass", "applicability", "implementation", "verification")) + " |")
    out += ["", "## Known unsupported candidates", ""]
    for r in reqs:
        if r["implementation"] == "unsupported":
            out.append(f"- **{r['id']}** {cell(r['title'])} — {cell(r['limitations'])}")
    out += ["", "A negative compile/test case proves a diagnostic only, not execution support.", "",
            "## Event legality and evidence matrix", "",
            "Legal cells show *preliminary implementation status*, never a conformance pass.", "",
            "| Family | " + " | ".join(POSITIONS) + " |",
            "| --- | " + " | ".join("---" for _ in POSITIONS) + " |"]
    grid = {(e["family"], e["position"]): e for e in events}
    for family in LEGALITY:
        cells = [(grid[(family, p)]["implementation"] if grid[(family, p)]["legal"] else "—") for p in POSITIONS]
        out.append("| " + family + " | " + " | ".join(cells) + " |")
    out += ["", "## Control-flow patterns (independent, non-normative)", "",
            "| WCP | Name | Registered scenario feature? | Semantic verification |",
            "| --- | --- | --- | --- |"]
    for p in patterns:
        out.append(f"| {p['id']} | {cell(p['name'])} | "
                   f"{'yes' if p['suiteRegistered'] else 'no'} | {p['verification']} |")
    out += ["", "## Limits and review gates", "",
            "The 105 rows are a **frozen candidate seed**, not an approved normative denominator.",
            "The 63 legal event-position cells need further expansion by attribute, scope,",
            "trigger combination, interruption, repetition and crash-recovery variants.",
            "OMG issue BPMN21-443 exposes the Ad-Hoc Process versus Sub-Process `13.1 ambiguity.",
            "Review [CON0 source audit](../docs/planning/bpmn-conformance-con0-audit.md) and",
            "[event legality](../docs/planning/bpmn-conformance-con0-event-matrix.md).", "",
            "Run: `python3 scripts/conformance_registry.py`", "",
            "Check: `python3 scripts/conformance_registry.py --check`", ""]
    return "\n".join(out)


def main() -> int:
    args = argparse.ArgumentParser(description=__doc__)
    args.add_argument("--check", action="store_true")
    opts = args.parse_args()
    try:
        data = json.loads(REGISTRY.read_text(encoding="utf-8"))
        validate(data)
        report = render(data)
        if opts.check:
            current = REPORT.read_text(encoding="utf-8") if REPORT.is_file() else ""
            if current != report:
                import difflib
                sys.stderr.writelines(difflib.unified_diff(
                    current.splitlines(True), report.splitlines(True),
                    fromfile="checked-in", tofile="generated"))
                raise RegistryError("generated report out of date: run python3 scripts/conformance_registry.py")
        else:
            REPORT.write_text(report, encoding="utf-8")
    except (OSError, json.JSONDecodeError, RegistryError) as exc:
        print(f"CONFORMANCE REGISTRY ERROR: {exc}", file=sys.stderr)
        return 1
    print("CON0/CON1 provisional inventory validated")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
