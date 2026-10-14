#!/usr/bin/env python3
"""Build the derived cuts of the social card from docs/brand/atlas-social.svg.

Two files are generated from that one source and drift from it silently:

  atlas-social.png       the raster card, the only form GitHub's Social preview
                         and every link unfurler will take
  atlas-social-flat.svg  the same card with its text converted to outlines, so
                         it draws identically on a machine that has neither
                         Arial nor a metric-compatible substitute installed

The outlines are not re-shaped from the font. Chromium is asked where it puts
every character through getStartPositionOfChar, and only the glyph outline is
lifted out of the font and placed there. Re-shaping would mean reproducing
kerning and letter-spacing, and being subtly wrong about both.

Usage:
    scripts/brand/social.py            rebuild both derived files
    scripts/brand/social.py --check    verify they match the source, write
                                       nothing, exit 1 on drift (for CI)

Needs python3 with fontTools and Pillow, plus a Chromium. The browser is found
through PLAYWRIGHT_BROWSERS_PATH, CHROME, or the PATH.
"""

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
BRAND = ROOT / "docs" / "brand"
SOURCE = BRAND / "atlas-social.svg"
PNG = BRAND / "atlas-social.png"
FLAT = BRAND / "atlas-social-flat.svg"

WIDTH, HEIGHT = 1200, 630
# The headless viewport comes back shorter than --window-size asks for, so the
# window is given room and the shot is cropped. Without this the footer line is
# simply missing from the render, with no error anywhere.
SHOT_HEIGHT = HEIGHT + 130
PAGE_BG = "#0A0E15"

# Rendering paths differ: text rendering hints glyphs onto the pixel grid, path
# rendering does not. That shows up only at the edges, so the comparison runs at
# 4x where it washes out, and what is left is real geometry.
COMPARE_SCALE = 4
MAX_DELTA = 128           # no pixel may differ by more than this
SOFT_DELTA, SOFT_SHARE = 64, 0.0001


def die(msg):
    print(f"social.py: {msg}", file=sys.stderr)
    sys.exit(1)


def find_chromium():
    if env := os.environ.get("CHROME"):
        if not os.access(env, os.X_OK):
            die(f"CHROME is set to {env!r}, which is not an executable")
        return env
    for base in filter(None, [os.environ.get("PLAYWRIGHT_BROWSERS_PATH")]):
        for exe in sorted(Path(base).glob("chromium-*/chrome-linux/chrome")):
            return str(exe)
    for name in ("chromium", "chromium-browser", "google-chrome", "chrome"):
        if found := shutil.which(name):
            return found
    die("no Chromium found; set CHROME=/path/to/chrome")


def page(svg_text, extra=""):
    return (
        '<!doctype html><meta charset="utf-8">\n'
        f"<style>html,body{{margin:0;padding:0;background:{PAGE_BG}}}\n"
        f"svg{{display:block;width:{WIDTH}px;height:{HEIGHT}px}}</style>\n"
        f"{svg_text}\n{extra}"
    )


def run_chromium(chrome, html, args, workdir):
    src = Path(workdir) / "page.html"
    src.write_text(html, encoding="utf-8")
    cmd = [chrome, "--headless", "--no-sandbox", "--disable-gpu",
           "--hide-scrollbars", *args, f"file://{src}"]
    proc = subprocess.run(cmd, capture_output=True, text=True)
    if proc.returncode != 0:
        die(f"chromium failed ({proc.returncode}): {proc.stderr.strip()[:400]}")
    return proc.stdout


def render(chrome, svg_text, out_path, scale=1):
    from PIL import Image
    with tempfile.TemporaryDirectory() as tmp:
        shot = Path(tmp) / "shot.png"
        run_chromium(chrome, page(svg_text), [
            f"--force-device-scale-factor={scale}",
            f"--window-size={WIDTH},{SHOT_HEIGHT}",
            f"--screenshot={shot}",
        ], tmp)
        img = Image.open(shot).convert("RGBA").crop(
            (0, 0, WIDTH * scale, HEIGHT * scale))
        if out_path:
            img.save(out_path, optimize=True)
        return img.copy()


def measure(chrome, svg_text):
    """Ask Chromium where it actually puts every character."""
    probe = """
<pre id="out" style="display:none"></pre>
<script>
const res=[];
document.querySelectorAll('svg text').forEach((t,ti)=>{
  const cs=getComputedStyle(t), s=t.textContent, chars=[];
  for(let i=0;i<t.getNumberOfChars();i++){
    const p=t.getStartPositionOfChar(i);
    chars.push({c:s[i],x:p.x,y:p.y});
  }
  res.push({i:ti,family:cs.fontFamily,size:cs.fontSize,weight:cs.fontWeight,
            fill:t.getAttribute('fill'),chars});
});
document.getElementById('out').textContent='@@'+JSON.stringify(res)+'@@';
</script>"""
    with tempfile.TemporaryDirectory() as tmp:
        dom = run_chromium(chrome, page(svg_text, probe),
                           ["--virtual-time-budget=3000", "--dump-dom"], tmp)
    if not (m := re.search(r"@@(.*?)@@", dom, re.S)):
        die("could not read the measurements back out of the page")
    return json.loads(m.group(1))


def resolve_font(family_list, weight):
    """Resolve a CSS font stack the way fontconfig does, to a file on disk."""
    first = family_list.split(",")[0].strip().strip("'\"")
    pattern = f"{first}:bold" if int(weight) >= 600 else first
    out = subprocess.run(["fc-match", "-f", "%{file}", pattern],
                         capture_output=True, text=True)
    if out.returncode != 0 or not out.stdout.strip():
        die(f"fc-match could not resolve {pattern!r}")
    return out.stdout.strip()


def outline(measurements):
    """One <path> per text element, glyphs placed where Chromium put them."""
    from fontTools.ttLib import TTFont
    from fontTools.pens.svgPathPen import SVGPathPen
    from fontTools.pens.transformPen import TransformPen
    from fontTools.misc.transform import Transform

    cache, paths, used = {}, [], {}
    for t in measurements:
        path = resolve_font(t["family"], t["weight"])
        used[f'{t["family"]} @ {t["weight"]}'] = Path(path).name
        if path not in cache:
            cache[path] = TTFont(path)
        font = cache[path]
        cmap, glyphs = font.getBestCmap(), font.getGlyphSet()
        scale = float(t["size"].removesuffix("px")) / font["head"].unitsPerEm
        pen = SVGPathPen(glyphs, ntos=lambda v: f"{v:.2f}")
        for ch in t["chars"]:
            if ch["c"].isspace():
                continue
            if (name := cmap.get(ord(ch["c"]))) is None:
                die(f"{path} has no glyph for {ch['c']!r}; Chromium drew it "
                    "from a fallback font this script cannot see")
            glyphs[name].draw(TransformPen(
                pen, Transform(scale, 0, 0, -scale, ch["x"], ch["y"])))
        paths.append({"d": pen.getCommands(), "fill": t["fill"]})
    return paths, used


def build_flat(source_text, paths):
    it = iter(paths)

    def swap(m):
        p = next(it)
        label = re.sub(r"\s+", " ", re.sub(r"<[^>]*>", "", m.group(0))).strip()
        return f'  <!-- {label} -->\n  <path fill="{p["fill"]}" d="{p["d"]}"/>'

    out, n = re.subn(r"[ \t]*<text\b.*?</text>", swap, source_text, flags=re.S)
    if n != len(paths):
        die(f"replaced {n} text elements but measured {len(paths)}")
    # Nothing left in this cut is typeset, so a font-family attribute here would
    # only claim a dependency the file no longer has.
    out = re.sub(r'\s+font-family="[^"]*"', "", out)
    return out.replace(
        "<!-- Wordmark + copy -->",
        "<!-- Wordmark + copy, as outlines. Generated by scripts/brand/social.py\n"
        "       from atlas-social.svg: edit that file, never this one. -->")


def compare(chrome, a_text, b_text):
    """Render both and report the worst pixel difference between them."""
    from PIL import ImageChops
    a = render(chrome, a_text, None, COMPARE_SCALE).convert("RGB")
    b = render(chrome, b_text, None, COMPARE_SCALE).convert("RGB")
    hist = ImageChops.difference(a, b).convert("L").histogram()
    total = (WIDTH * COMPARE_SCALE) * (HEIGHT * COMPARE_SCALE)
    return sum(hist[MAX_DELTA:]), sum(hist[SOFT_DELTA:]) / total


def main():
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument("--check", action="store_true",
                    help="verify the derived files, write nothing")
    args = ap.parse_args()

    try:
        import fontTools  # noqa: F401
        import PIL  # noqa: F401
    except ImportError as e:
        die(f"missing dependency: {e.name} (pip install fonttools pillow)")

    chrome = find_chromium()
    source = SOURCE.read_text(encoding="utf-8")

    paths, fonts = outline(measure(chrome, source))
    flat = build_flat(source, paths)
    for stack, file in fonts.items():
        print(f"  {stack:<38} -> {file}")

    hard, soft = compare(chrome, source, flat)
    if hard or soft > SOFT_SHARE:
        die(f"the outlines do not match the source: {hard} pixels over "
            f"{MAX_DELTA}/255, {soft:.4%} over {SOFT_DELTA}/255. The fonts "
            "resolved here are probably not the ones the card was drawn with.")
    print(f"  outlines match the source ({soft:.4%} of pixels differ softly)")

    if args.check:
        stale = [p.name for p, want in ((FLAT, flat),)
                 if p.read_text(encoding="utf-8") != want]
        from PIL import Image, ImageChops
        fresh = render(chrome, source, None).convert("RGB")
        on_disk = Image.open(PNG).convert("RGB")
        if on_disk.size != fresh.size or ImageChops.difference(
                on_disk, fresh).getbbox() is not None:
            stale.append(PNG.name)
        if stale:
            die("stale, rerun scripts/brand/social.py: " + ", ".join(stale))
        print("  atlas-social.png and atlas-social-flat.svg are up to date")
        return

    FLAT.write_text(flat, encoding="utf-8")
    render(chrome, source, PNG)
    print(f"  wrote {PNG.relative_to(ROOT)} and {FLAT.relative_to(ROOT)}")


if __name__ == "__main__":
    main()
