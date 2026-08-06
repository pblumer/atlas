#!/usr/bin/env python3
"""Generate the ArchiMate SVG diagrams for the Atlas enterprise-architecture doc.

Usage:
    python3 gen_diagrams.py            # (re)writes the *.svg files in this directory

Rasterize the SVGs to 2x PNGs with headless Chromium (no extra Python deps beyond
Pillow for the final crop):

    CHROME=/path/to/chrome
    for f in overview motivation-trace business application technology; do
      read W H < <(python3 -c "import re;s=open('$f.svg').read();\
        m=re.search(r'width=\"(\d+)\" height=\"(\d+)\"',s);print(m.group(1),m.group(2))")
      "$CHROME" --headless=new --disable-gpu --no-sandbox --hide-scrollbars \
        --force-device-scale-factor=2 --default-background-color=00000000 \
        --window-size=${W},$((H+60)) --screenshot="$f.png" "file://$PWD/$f.svg"
      python3 -c "from PIL import Image;Image.open('$f.png').crop((0,0,${W}*2,${H}*2)).save('$f.png')"
    done

The +60 window pad works around a headless-Chromium clip when the viewport height
equals the SVG height exactly; the crop trims it back to the true size.
"""
import os, html

OUT = os.path.dirname(os.path.abspath(__file__))

STRONG  = {"mot":"#7E5BB0","biz":"#C9A227","app":"#3D7FBF","tec":"#3E9E6A","ext":"#8A97A6","core":"#2C6DAF"}
BOXFILL = {"mot":"#E7DBF5","biz":"#FBEFA8","app":"#CBE4FA","tec":"#CBEED7","ext":"#DDE3EA","core":"#AFD6F5"}
BOXSTRK = {"mot":"#9B7BC4","biz":"#D8B84A","app":"#5E97CE","tec":"#5FB584","ext":"#A7B1BD","core":"#4C86B6"}

FONT = "'Segoe UI',system-ui,-apple-system,Helvetica,Arial,sans-serif"
INK_BOX = "#1f2733"          # text on light boxes — always dark (readable both themes)
CHARW_BOLD = 7.7
CHARW = 6.9
BOXPADX = 15
GAP = 18
CARDPAD = 22
BAND_TITLE_H = 26
BAND_PADY = 14
INTERBAND = 50

def esc(s): return html.escape(s, quote=True)

def linew(line, bold):
    return len(line) * (CHARW_BOLD if bold else CHARW)

def box_width(lines):
    w = max(linew(l, i==0) for i, l in enumerate(lines))
    return max(150, int(w + 2*BOXPADX))

def box_height(lines):
    return 14 + 21 + (len(lines)-1)*17 + 13   # padtop + line0 + rest + padbot

def render_box(x, y, w, h, lines, layer):
    fill, strk = BOXFILL[layer], BOXSTRK[layer]
    core = layer == "core"
    sw = 2.0 if core else 1.4
    out = [f'<rect x="{x:.1f}" y="{y:.1f}" width="{w:.1f}" height="{h:.1f}" rx="8" '
           f'fill="{fill}" stroke="{strk}" stroke-width="{sw}"/>']
    cx = x + w/2
    ty = y + 26
    for i, l in enumerate(lines):
        if i == 0:
            out.append(f'<text x="{cx:.1f}" y="{ty:.1f}" text-anchor="middle" '
                       f'font-family="{FONT}" font-size="13.5" font-weight="700" '
                       f'fill="{INK_BOX}">{esc(l)}</text>')
            ty += 18
        else:
            out.append(f'<text x="{cx:.1f}" y="{ty:.1f}" text-anchor="middle" '
                       f'font-family="{FONT}" font-size="11.5" fill="{INK_BOX}" '
                       f'opacity="0.82">{esc(l)}</text>')
            ty += 17
    return "".join(out)

def band_row_width(boxes):
    ws = [box_width(b) for b in boxes]
    return sum(ws) + GAP*(len(ws)-1), ws

def svg_header(w, h):
    return (f'<svg xmlns="http://www.w3.org/2000/svg" width="{w}" height="{h}" '
            f'viewBox="0 0 {w} {h}" font-family="{FONT}">'
            '<style>'
            ':root{--card:#f6f7f9;--ink:#26313d;--edge:#e2e6eb;}'
            '@media (prefers-color-scheme:dark){:root{--card:#161d26;--ink:#dfe7ef;--edge:#2a333d;}}'
            '.card{fill:var(--card,#f6f7f9);stroke:var(--edge,#e2e6eb);}'
            '.ink{fill:var(--ink,#26313d);}'
            '.pill{fill:var(--card,#f6f7f9);stroke:var(--edge,#e2e6eb);}'
            '</style>'
            '<defs><marker id="arw" viewBox="0 0 10 10" refX="8.5" refY="5" markerWidth="7" '
            'markerHeight="7" orient="auto-start-reverse">'
            '<path d="M0,0 L10,5 L0,10 z" fill="#8894a2"/></marker></defs>')

def arrow_label(cx, cy, text):
    w = len(text)*6.4 + 16
    return (f'<rect x="{cx-w/2:.1f}" y="{cy-11:.1f}" width="{w:.1f}" height="22" rx="11" class="pill"/>'
            f'<text x="{cx:.1f}" y="{cy+4:.1f}" text-anchor="middle" font-size="11.5" '
            f'font-weight="600" class="ink">{esc(text)}</text>')

def gen_stack(name, title, bands, labels):
    # bands: list of (layer, band_title, [ [lines...], ... ])
    rows = []
    maxrow = 0
    for layer, bt, boxes in bands:
        rw, ws = band_row_width(boxes)
        bh = BAND_TITLE_H + BAND_PADY + max(box_height(b) for b in boxes) + BAND_PADY
        rows.append((layer, bt, boxes, ws, rw, bh))
        maxrow = max(maxrow, rw)
    inner_w = maxrow + 2*24
    W = inner_w + 2*CARDPAD
    title_h = 40 if title else CARDPAD
    y = title_h
    body = []
    for idx, (layer, bt, boxes, ws, rw, bh) in enumerate(rows):
        bx = CARDPAD
        bw = inner_w
        body.append(f'<rect x="{bx}" y="{y:.1f}" width="{bw}" height="{bh:.1f}" rx="12" '
                    f'fill="{STRONG[layer]}" fill-opacity="0.13" '
                    f'stroke="{STRONG[layer]}" stroke-opacity="0.42" stroke-width="1.2"/>')
        body.append(f'<text x="{bx+16}" y="{y+19:.1f}" font-size="12.5" font-weight="700" '
                    f'letter-spacing="0.4" class="ink">{esc(bt)}</text>')
        # row of boxes centered
        rowx = bx + (bw - rw)/2
        boxtop = y + BAND_TITLE_H + BAND_PADY
        rowh = max(box_height(b) for b in boxes)
        cx0 = rowx
        for b, bwid in zip(boxes, ws):
            body.append(render_box(cx0, boxtop, bwid, rowh, b, layer))
            cx0 += bwid + GAP
        y += bh
        if idx < len(rows)-1:
            midx = bx + bw/2
            body.append(f'<line x1="{midx:.1f}" y1="{y+8:.1f}" x2="{midx:.1f}" y2="{y+INTERBAND-8:.1f}" '
                        f'stroke="#8894a2" stroke-width="1.6" marker-end="url(#arw)"/>')
            if idx < len(labels) and labels[idx]:
                body.append(arrow_label(midx, y+INTERBAND/2, labels[idx]))
            y += INTERBAND
    H = y + CARDPAD
    parts = [svg_header(W, H)]
    parts.append(f'<rect x="1" y="1" width="{W-2}" height="{H-2}" rx="16" class="card" stroke-width="1"/>')
    if title:
        parts.append(f'<text x="{CARDPAD+2}" y="27" font-size="15" font-weight="800" '
                     f'letter-spacing="0.2" class="ink">{esc(title)}</text>')
    parts += body
    parts.append('</svg>')
    open(os.path.join(OUT, name+".svg"), "w").write("".join(parts))
    return W, H

def gen_chain(name, title, boxes, labels):
    # boxes: list of (layer, [lines...]); labels between them
    ws = [box_width(b) for _, b in boxes]
    rowh = max(box_height(b) for _, b in boxes)
    lblw = [ (len(l)*6.4+16 if l else 0) for l in labels ]
    ARR = 30
    inner = 0
    for i, w in enumerate(ws):
        inner += w
        if i < len(ws)-1:
            inner += max(ARR*2, lblw[i]+16)
    title_h = 40 if title else CARDPAD
    W = int(round(inner + 2*CARDPAD + 2*10))
    H = int(round(title_h + rowh + CARDPAD))
    x = CARDPAD + 10
    ytop = title_h
    body = []
    for i, (layer, b) in enumerate(boxes):
        body.append(render_box(x, ytop, ws[i], rowh, b, layer))
        x += ws[i]
        if i < len(ws)-1:
            seg = max(ARR*2, lblw[i]+16)
            cy = ytop + rowh/2
            body.append(f'<line x1="{x+6:.1f}" y1="{cy:.1f}" x2="{x+seg-6:.1f}" y2="{cy:.1f}" '
                        f'stroke="#8894a2" stroke-width="1.6" marker-end="url(#arw)"/>')
            if labels[i]:
                body.append(arrow_label(x+seg/2, cy-16, labels[i]))
            x += seg
    parts = [svg_header(W, H)]
    parts.append(f'<rect x="1" y="1" width="{W-2}" height="{H-2}" rx="16" class="card" stroke-width="1"/>')
    if title:
        parts.append(f'<text x="{CARDPAD+2}" y="27" font-size="15" font-weight="800" class="ink">{esc(title)}</text>')
    parts += body
    parts.append('</svg>')
    open(os.path.join(OUT, name+".svg"), "w").write("".join(parts))
    return W, H

# ---------------- diagram definitions ----------------

gen_stack("overview", "Atlas — ArchiMate 3.2 layered view", [
    ("mot","MOTIVATION  ·  why Atlas is shaped this way", [
        ["Drivers","throughput · durability","long-running · conformance"],
        ["Principles","compile-don't-interpret","event sourcing · single writer"],
        ["Requirements","the six invariants"]]),
    ("biz","BUSINESS  ·  process automation as a capability", [
        ["Business services","Automation · Human Tasks","Decisions · Monitoring"],
        ["Processes","lifecycle · order-to-cash"],
        ["Objects","BPMN · DMN · Form · Instance"]]),
    ("app","APPLICATION  ·  the software that delivers it", [
        ["Atlas Engine","Compiler · Processor","Data model"],
        ["Channels","Web UI · REST API · MCP"],
        ["Connectors","REST · Mail · Script","DMN · clio"]]),
    ("tec","TECHNOLOGY  ·  the runtime that hosts it", [
        ["Single binary","Go runtime, no CGO"],
        ["Partitions","single-writer"],
        ["Pebble + WAL","filesystem · group commit"]]),
], ["shapes","realized by","realized by / served by"])

gen_chain("motivation-trace", "Motivation trace — one concern, all the way down", [
    ("mot",["Stakeholder","Operations"]),
    ("mot",["Driver","durability"]),
    ("mot",["Goal","survive crashes"]),
    ("mot",["Principle","event sourcing"]),
    ("mot",["Requirement","durable before visible","(invariant 2)"]),
    ("app",["Processor + WAL","group commit"]),
], ["has","influences","realized by","realized by","realized by"])

gen_stack("business", "Business layer", [
    ("biz","ROLES  ·  active structure", [
        ["Process Modeler"],["Task Performer"],["Operations"]]),
    ("biz","BUSINESS SERVICES  ·  behaviour", [
        ["Process","Automation"],["Human-Task","Handling"],
        ["Decision","Making (DMN)"],["Monitoring","& Audit"]]),
    ("biz","BUSINESS OBJECTS  ·  passive structure", [
        ["BPMN model"],["Process instance"],["User task"],["Incident"]]),
], ["assigned to","access"])

gen_stack("application", "Application layer", [
    ("app","CHANNELS", [
        ["Web UI","bpmn-js"],["REST / HTTP API","+ OpenAPI"],["MCP Server"]]),
    ("core","ATLAS ENGINE  ·  core", [
        ["Graph","Compiler"],["Processor","single-writer"],["Data model","applyToState"],
        ["WAL","manager"],["State-store","wrapper"]]),
    ("app","CONNECTORS  ·  job workers", [
        ["REST"],["Mail"],["Script"],["DMN / temis"],["clio bridge"]]),
], ["serve requests to","create jobs for"])

gen_stack("technology", "Technology layer", [
    ("tec","ATLAS SINGLE BINARY  ·  Go runtime, no CGO", [
        ["Partition 0","processor · WAL · state"],
        ["Partition 1","processor · WAL · state"],
        ["Partition N","…"]]),
    ("tec","DURABLE STORAGE", [
        ["Filesystem","WAL segments + Pebble SST","fsync · group commit"]]),
    ("ext","EXTERNAL SYSTEMS  ·  via connectors (gRPC / HTTPS)", [
        ["temis","DMN/FEEL"],["clio","event store"],
        ["Gmail /","MS Graph"],["polyglot","job workers"]]),
], ["group commit","gRPC / HTTPS"])

print("SVGs written to", OUT)
for f in sorted(os.listdir(OUT)):
    print(" ", f, os.path.getsize(os.path.join(OUT, f)), "bytes")
