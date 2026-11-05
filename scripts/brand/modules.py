#!/usr/bin/env python3
"""Write the module symbols under docs/brand/icons/modules from the Console's drawing.

The symbols of atlas's modules are drawn once, in api/web/module-icons.js, as a JSON
table between /* JSON */ and /* /JSON */. This writes one SVG per module — the glyph
on its colour, as a 36-unit tile — for documents and slides.

    scripts/brand/modules.py            # write the files
    scripts/brand/modules.py --check    # write nothing, exit 1 if a file differs

Python 3 only, no packages.
"""
import json, os, re, sys

ROOT = os.path.normpath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", ".."))
SOURCE = os.path.join(ROOT, "api", "web", "module-icons.js")
OUT = os.path.join(ROOT, "docs", "brand", "icons", "modules")


def read_icons():
    src = open(SOURCE, encoding="utf-8").read()
    m = re.search(r"/\* JSON \*/(.*?)/\* /JSON \*/", src, re.S)
    if not m:
        sys.exit(f"{SOURCE}: no table between /* JSON */ and /* /JSON */")
    return json.loads(m.group(1))


def tile(mid, icon):
    return (f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 36 36" width="72" height="72" role="img" aria-label="{icon["name"]}">'
            f'<title>{icon["name"]}</title>'
            f'<rect width="36" height="36" rx="10" fill="{icon["colour"]}"/>'
            f'<g transform="translate(6 6)">{icon["glyph"]}</g></svg>\n')


def main():
    check = "--check" in sys.argv[1:]
    icons = read_icons()
    stale = []
    if not check:
        os.makedirs(OUT, exist_ok=True)
    for mid, icon in icons.items():
        path = os.path.join(OUT, f"module-{mid}.svg")
        want = tile(mid, icon)
        have = open(path, encoding="utf-8").read() if os.path.exists(path) else None
        if have != want:
            stale.append(os.path.relpath(path, ROOT))
            if not check:
                open(path, "w", encoding="utf-8").write(want)
    known = {f"module-{mid}.svg" for mid in icons}
    extra = [f for f in (os.listdir(OUT) if os.path.isdir(OUT) else []) if f.endswith(".svg") and f not in known]
    if check:
        for f in stale:
            print(f"stale: {f}")
        for f in extra:
            print(f"no longer a module: docs/brand/icons/modules/{f}")
        sys.exit(1 if stale or extra else 0)
    for f in stale:
        print(f"wrote {f}")
    for f in extra:
        print(f"note: docs/brand/icons/modules/{f} belongs to no module any more; remove it")


if __name__ == "__main__":
    main()
