#!/usr/bin/env python3
"""Generate/check the programme index; optionally audit GitHub references."""

from __future__ import annotations

import argparse
import datetime as dt
import difflib
import json
import os
from pathlib import Path
import re
import sys
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
STATES = ("active", "blocked", "paused", "planned", "complete")
REQUIRED = {"id", "name", "summary", "status", "owner", "last_reviewed",
            "plan", "milestone", "next_action", "slices"}
SLICE_FIELDS = {"id", "title", "status", "owner", "depends_on", "issues", "prs", "note"}


class ProgramError(ValueError):
    """Invalid programme metadata or references."""


def require(valid: bool, reason: str) -> None:
    if not valid:
        raise ProgramError(reason)


def md(value: str) -> str:
    return value.replace("|", r"\|").replace("\n", " ").strip()


def resolve(root: Path, manifest: Path, reference: str) -> Path:
    require(isinstance(reference, str) and reference
            and not reference.startswith("/") and "://" not in reference,
            f"{manifest}: expected repository-relative link: {reference}")
    name = reference.split("#", 1)[0]
    require(bool(name), f"{manifest}: empty local path")
    target = (manifest.parent / name).resolve()
    require(target.is_relative_to(root), f"{manifest}: path escapes repository: {reference}")
    require(target.is_file(), f"{manifest}: missing file: {reference}")
    return target


def validate(root: Path, manifest: Path, data: object) -> dict:
    label = str(manifest.relative_to(root))
    require(isinstance(data, dict), f"{label}: expected object")
    require(set(data) == REQUIRED, f"{label}: invalid manifest keys")
    name = data["id"]
    require(isinstance(name, str) and re.fullmatch(r"[a-z][a-z0-9-]*", name) is not None
            and name == manifest.parent.name, f"{label}: invalid id or directory")
    for key in ("name", "summary", "owner", "next_action"):
        require(isinstance(data[key], str) and data[key].strip(),
                f"{label}: missing {key}")
    require(data["status"] in STATES, f"{label}: invalid status")
    require(data["status"] != "complete" or not data["owner"].lower().startswith("unassigned"),
            f"{label}: completed programme requires an assigned owner")
    try:
        date = dt.date.fromisoformat(data["last_reviewed"])
        require(date.isoformat() == data["last_reviewed"], f"{label}: invalid date")
    except (TypeError, ValueError):
        raise ProgramError(f"{label}: invalid last_reviewed (YYYY-MM-DD)") from None
    for field in ("plan", "milestone"):
        resolve(root, manifest, data[field])
    slices = data["slices"]
    require(isinstance(slices, list) and slices, f"{label}: empty slices")
    graph = {}
    for item in slices:
        require(isinstance(item, dict) and SLICE_FIELDS <= set(item) <=
                SLICE_FIELDS | {"evidence"}, f"{label}: invalid slice fields")
        sid = item["id"]
        require(isinstance(sid, str) and re.fullmatch(r"[A-Z][A-Z0-9-]*", sid)
                is not None and sid not in graph, f"{label}: duplicate/invalid slice id")
        require(item["status"] in STATES, f"{label}/{sid}: invalid status")
        require(item["status"] != "complete" or not item["owner"].lower().startswith("unassigned"),
                f"{label}/{sid}: completed slice requires an assigned owner")
        for field in ("title", "owner", "note"):
            require(isinstance(item[field], str) and item[field].strip(),
                    f"{label}/{sid}: missing {field}")
        for field in ("prs", "issues", "depends_on"):
            require(isinstance(item[field], list) and
                    len(item[field]) == len(set(item[field])),
                    f"{label}/{sid}: invalid or duplicate {field}")
        for field in ("prs", "issues"):
            require(all(type(v) is int and v > 0 for v in item[field]),
                    f"{label}/{sid}: {field} must be positive numbers")
        require(all(isinstance(v, str) for v in item["depends_on"]),
                f"{label}/{sid}: invalid dependencies")
        evidence = item.get("evidence", [])
        require(isinstance(evidence, list) and all(isinstance(v, str) and v
                for v in evidence), f"{label}/{sid}: invalid evidence")
        require(item["status"] != "complete" or bool(evidence),
                f"{label}/{sid}: completion requires evidence")
        for link in evidence:
            resolve(root, manifest, link)
        graph[sid] = item["depends_on"]
    visited, visiting = set(), set()

    def walk(node: str) -> None:
        require(node not in visiting, f"{label}: dependency cycle: {node}")
        if node in visited:
            return
        visiting.add(node)
        for dep in graph[node]:
            require(dep in graph, f"{label}/{node}: unknown dependency {dep}")
            walk(dep)
        visiting.remove(node)
        visited.add(node)

    for sid in graph:
        walk(sid)
    require(data["status"] != "complete" or all(
            item["status"] == "complete" for item in slices),
            f"{label}: complete programme still has open slices")
    return data


def collect(root: Path) -> list[tuple[Path, dict]]:
    root = root.resolve()
    files = sorted((root / "docs/programs").glob("*/program.json"))
    require(bool(files), "no programme manifests under docs/programs")
    result = []
    for path in files:
        try:
            data = json.loads(path.read_text(encoding="utf-8"))
        except (json.JSONDecodeError, OSError) as error:
            raise ProgramError(f"{path}: {error}") from error
        result.append((path, validate(root, path, data)))
    return result


def link(root: Path, index: Path, manifest: Path, reference: str) -> str:
    target = resolve(root, manifest, reference)
    path = Path(os.path.relpath(target, index.parent)).as_posix()
    suffix = "#" + reference.split("#", 1)[1] if "#" in reference else ""
    return path + suffix


def render(root: Path, programs: list[tuple[Path, dict]]) -> str:
    root = root.resolve()
    index = root / "docs/programs/README.md"
    totals = {s: sum(p["status"] == s for _, p in programs) for s in STATES}
    summary = " · ".join(f"{k}: {totals[k]}" for k in STATES)
    out = [
        "# Programmes", "",
        "<!-- Generated by scripts/programs.py; do not edit manually. -->", "",
        "Operational overview of multi-slice programmes for people and coding agents.",
        "See [delivery protocol](../development/program-delivery.md) and",
        "[strategic roadmap](../../ROADMAP.md).", "",
        f"**{len(programs)} {'programme' if len(programs) == 1 else 'programmes'}** · {summary}", "",
        "| Programme | Status | Completed slices | Owner | Next action |",
        "| --- | --- | --- | --- | --- |",
    ]
    for manifest, p in programs:
        done = sum(s["status"] == "complete" for s in p["slices"])
        target = link(root, index, manifest, p["plan"])
        out.append(f"| [{md(p['name'])}]({target}) | {p['status']} "
                   f"| {done}/{len(p['slices'])} | {md(p['owner'])} "
                   f"| {md(p['next_action'])} |")
    for manifest, p in programs:
        target = link(root, index, manifest, p["plan"])
        road = link(root, index, manifest, p["milestone"])
        source = Path(os.path.relpath(manifest, index.parent)).as_posix()
        out += ["", f"## {md(p['name'])} ({p['id'].upper()})", "", md(p["summary"]), "",
                f"**[Programme plan]({target}) · [Roadmap track]({road}) · "
                f"[Manifest]({source}) · Reviewed: {p['last_reviewed']}**", "",
                "| Slice | Status | Owner | Dependencies | GitHub | Next gate / evidence |",
                "| --- | --- | --- | --- | --- | --- |"]
        for s in p["slices"]:
            refs = ([f"[PR #{n}](https://github.com/pblumer/atlas/pull/{n})"
                     for n in s["prs"]] +
                    [f"[Issue #{n}](https://github.com/pblumer/atlas/issues/{n})"
                     for n in s["issues"]])
            evidence = [f"[Evidence {i + 1}]({link(root, index, manifest, v)})"
                        for i, v in enumerate(s.get("evidence", []))]
            note = md(s["note"]) + ("; " + ", ".join(evidence) if evidence else "")
            out.append(f"| **{md(s['id'])}** — {md(s['title'])} | {s['status']} "
                       f"| {md(s['owner'])} | {', '.join(s['depends_on']) or '—'} "
                       f"| {', '.join(refs) or '—'} | {note} |")
    out += ["", "## Updating the register", "",
            "Edit the relevant programme manifest, not this generated index.",
            "A merged PR alone does not close a slice: update ownership, dependencies,",
            "evidence and the next handoff when the change is integrated.",
            "Regenerate: python3 scripts/programs.py",
            "Check: python3 scripts/programs.py --check",
            "Optional read-only remote link audit: python3 scripts/programs.py --audit-github", ""]
    return "\n".join(out)


def audit_github(programs: list[tuple[Path, dict]]) -> None:
    numbers = {(kind, num) for _, p in programs for s in p["slices"]
               for kind, vals in (("pulls", s["prs"]), ("issues", s["issues"]))
               for num in vals}
    token = os.getenv("GITHUB_TOKEN")
    headers = {"Accept": "application/vnd.github+json",
               "User-Agent": "atlas-programmes"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    result = {}
    for kind, num in sorted(numbers):
        url = f"https://api.github.com/repos/pblumer/atlas/{kind}/{num}"
        request = urllib.request.Request(url, headers=headers)
        try:
            with urllib.request.urlopen(request, timeout=12) as response:
                result[(kind, num)] = json.load(response)
        except (OSError, ValueError) as error:
            raise ProgramError(f"{url}: {error}") from error
    for _, p in programs:
        for item in p["slices"]:
            for num in item["prs"]:
                require(item["status"] != "complete" or result[("pulls", num)]["merged"],
                        f"{item['id']}: marked complete but PR #{num} is not merged")
    print(f"Audited {len(result)} GitHub references; completion remains an explicit review gate.")


def main(argv: list[str] | None = None, root: Path = ROOT) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true",
                        help="fail if index differs from source manifests")
    parser.add_argument("--audit-github", action="store_true",
                        help="optionally query public GitHub for PR/issue references")
    args = parser.parse_args(argv)
    try:
        programs = collect(root)
        output = render(root, programs)
        index = root / "docs/programs/README.md"
        if args.check:
            current = index.read_text(encoding="utf-8") if index.exists() else ""
            if current != output:
                print("Stale index: run python3 scripts/programs.py", file=sys.stderr)
                sys.stderr.writelines(difflib.unified_diff(
                    current.splitlines(True), output.splitlines(True),
                    fromfile="actual", tofile="expected"))
                return 1
        else:
            index.write_text(output, encoding="utf-8")
            print(f"Updated {index.relative_to(root)}")
        if args.audit_github:
            audit_github(programs)
        return 0
    except (ProgramError, OSError) as error:
        print(f"Programme validation failed: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
