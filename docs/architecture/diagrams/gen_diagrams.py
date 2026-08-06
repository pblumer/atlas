#!/usr/bin/env python3
"""Generate the ArchiMate SVG diagrams for the Atlas enterprise-architecture doc.

Usage:
    python3 gen_diagrams.py            # (re)writes the *.svg files in this directory

Each box carries the standard ArchiMate element-type icon in its top-right corner
(service = rounded rect, process = arrow, object = rect+header, component = rect with
two tabs, node = 3D box, role/stakeholder = circle+socket, driver = steering wheel,
goal = concentric circles, principle = "!" in a rounded square, requirement =
parallelogram). Icon shapes follow the Archi / marcelomg archimate-symbols set.

Rasterize the SVGs to 2x PNGs with headless Chromium (Pillow only for the final crop):

    CHROME=/path/to/chrome
    for f in overview motivation-trace business application technology; do
      read W H < <(python3 -c "import re;s=open('$f.svg').read();\\
        m=re.search(r'width=\\"(\\d+)\\" height=\\"(\\d+)\\"',s);print(m.group(1),m.group(2))")
      "$CHROME" --headless=new --disable-gpu --no-sandbox --hide-scrollbars \\
        --force-device-scale-factor=2 --default-background-color=00000000 \\
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
INK_BOX = "#1f2733"          # text + icons on light boxes — dark, readable in both themes
CHARW_BOLD = 7.7
CHARW = 6.9
BOXPADX = 15
ICON_COL = 36                # reserved width on the right for the ArchiMate icon
GAP = 18
CARDPAD = 22
BAND_TITLE_H = 26
BAND_PADY = 14
INTERBAND = 50
ICON_S = 18                  # icon glyph box size

def esc(s): return html.escape(s, quote=True)

def linew(line, bold):
    return len(line) * (CHARW_BOLD if bold else CHARW)

def box_width(box):
    lines = box[1]
    w = max(linew(l, i == 0) for i, l in enumerate(lines))
    return max(162, int(w + 2 * BOXPADX + ICON_COL))

def box_height(box):
    lines = box[1]
    return 14 + 21 + (len(lines) - 1) * 17 + 13

# ---------------- ArchiMate element-type icons ----------------

def draw_icon(kind, ix, iy):
    """Return SVG for a ~18px ArchiMate element icon with top-left at (ix, iy)."""
    S = ICON_S
    c = INK_BOX
    st = f'stroke="{c}" stroke-width="1.3" fill="none" stroke-linejoin="round"'
    cx, cy = ix + S / 2, iy + S / 2
    p = []
    if kind == "service":
        p.append(f'<rect x="{ix+1}" y="{iy+5}" width="{S-2}" height="{S-10}" rx="4" {st}/>')
    elif kind == "process":
        pts = f'{ix+1},{iy+6} {ix+10},{iy+6} {ix+10},{iy+3} {ix+16},{iy+9} {ix+10},{iy+15} {ix+10},{iy+12} {ix+1},{iy+12}'
        p.append(f'<polygon points="{pts}" {st}/>')
    elif kind == "object":
        p.append(f'<rect x="{ix+2}" y="{iy+3}" width="{S-4}" height="{S-6}" rx="1" {st}/>')
        p.append(f'<line x1="{ix+2}" y1="{iy+7}" x2="{ix+S-2}" y2="{iy+7}" {st}/>')
    elif kind == "component":
        p.append(f'<rect x="{ix+5}" y="{iy+2}" width="{S-6}" height="{S-4}" rx="1" {st}/>')
        p.append(f'<rect x="{ix}" y="{iy+5}" width="6" height="3.4" {st}/>')
        p.append(f'<rect x="{ix}" y="{iy+10.5}" width="6" height="3.4" {st}/>')
    elif kind == "node":
        p.append(f'<rect x="{ix+1}" y="{iy+6}" width="11" height="9" {st}/>')
        p.append(f'<polygon points="{ix+1},{iy+6} {ix+5},{iy+2} {ix+16},{iy+2} {ix+12},{iy+6}" {st}/>')
        p.append(f'<polygon points="{ix+12},{iy+6} {ix+16},{iy+2} {ix+16},{iy+11} {ix+12},{iy+15}" {st}/>')
    elif kind == "artifact":
        p.append(f'<path d="M{ix+3},{iy+2} L{ix+11},{iy+2} L{ix+15},{iy+6} L{ix+15},{iy+16} L{ix+3},{iy+16} Z" {st}/>')
        p.append(f'<path d="M{ix+11},{iy+2} L{ix+11},{iy+6} L{ix+15},{iy+6}" {st}/>')
    elif kind == "role":
        p.append(f'<line x1="{ix+2}" y1="{cy}" x2="{ix+10}" y2="{cy}" {st}/>')
        p.append(f'<circle cx="{ix+12.5}" cy="{cy}" r="3.2" {st}/>')
    elif kind == "driver":
        r = 7
        p.append(f'<circle cx="{cx}" cy="{cy}" r="{r}" {st}/>')
        p.append(f'<line x1="{cx-r}" y1="{cy}" x2="{cx+r}" y2="{cy}" {st}/>')
        p.append(f'<line x1="{cx}" y1="{cy-r}" x2="{cx}" y2="{cy+r}" {st}/>')
        d = r * 0.71
        p.append(f'<line x1="{cx-d}" y1="{cy-d}" x2="{cx+d}" y2="{cy+d}" {st}/>')
        p.append(f'<line x1="{cx-d}" y1="{cy+d}" x2="{cx+d}" y2="{cy-d}" {st}/>')
        p.append(f'<circle cx="{cx}" cy="{cy}" r="2.2" fill="{c}" stroke="none"/>')
    elif kind == "goal":
        p.append(f'<circle cx="{cx}" cy="{cy}" r="7.3" {st}/>')
        p.append(f'<circle cx="{cx}" cy="{cy}" r="4.6" {st}/>')
        p.append(f'<circle cx="{cx}" cy="{cy}" r="2" fill="{c}" stroke="none"/>')
    elif kind == "principle":
        p.append(f'<rect x="{ix+2}" y="{iy+2}" width="{S-4}" height="{S-4}" rx="3" {st}/>')
        p.append(f'<path d="M{cx-1.1},{iy+5} L{cx+1.1},{iy+5} L{cx+0.7},{iy+11} L{cx-0.7},{iy+11} Z" fill="{c}" stroke="none"/>')
        p.append(f'<rect x="{cx-0.9}" y="{iy+12.4}" width="1.8" height="1.8" fill="{c}" stroke="none"/>')
    elif kind == "requirement":
        pts = f'{ix+4},{iy+5} {ix+16},{iy+5} {ix+13},{iy+13} {ix+1},{iy+13}'
        p.append(f'<polygon points="{pts}" {st}/>')
    elif kind == "systemsoftware":
        p.append(f'<circle cx="{ix+7.5}" cy="{iy+10.5}" r="6" {st}/>')
        p.append(f'<circle cx="{ix+12.5}" cy="{iy+6.5}" r="3.6" {st}/>')
    elif kind == "plateau":
        p.append(f'<rect x="{ix+6}" y="{iy+3}" width="10" height="2.6" fill="{c}" stroke="none"/>')
        p.append(f'<rect x="{ix+3.5}" y="{iy+7.7}" width="10" height="2.6" fill="{c}" stroke="none"/>')
        p.append(f'<rect x="{ix+1}" y="{iy+12.4}" width="10" height="2.6" fill="{c}" stroke="none"/>')
    elif kind == "gap":
        p.append(f'<circle cx="{cx}" cy="{cy}" r="6.3" {st}/>')
        p.append(f'<line x1="{ix+1.5}" y1="{iy+7}" x2="{ix+16.5}" y2="{iy+7}" {st}/>')
        p.append(f'<line x1="{ix+1.5}" y1="{iy+11.5}" x2="{ix+16.5}" y2="{iy+11.5}" {st}/>')
    elif kind == "deliverable":
        p.append(f'<path d="M{ix+3},{iy+3} L{ix+15},{iy+3} L{ix+15},{iy+12} '
                 f'C{ix+13},{iy+15} {ix+11},{iy+11} {ix+9},{iy+13} '
                 f'C{ix+7},{iy+15} {ix+5},{iy+12} {ix+3},{iy+13} Z" {st}/>')
    return "".join(p)

def render_box(x, y, w, h, box, layer):
    icon, lines = box[0], box[1]
    fill, strk = BOXFILL[layer], BOXSTRK[layer]
    core = layer == "core"
    sw = 2.0 if core else 1.4
    out = [f'<rect x="{x:.1f}" y="{y:.1f}" width="{w:.1f}" height="{h:.1f}" rx="8" '
           f'fill="{fill}" stroke="{strk}" stroke-width="{sw}"/>']
    # ArchiMate icon, top-right corner
    if icon:
        out.append(draw_icon(icon, x + w - ICON_S - 9, y + 9))
    cx = x + w / 2
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
    return sum(ws) + GAP * (len(ws) - 1), ws

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
    w = len(text) * 6.4 + 16
    return (f'<rect x="{cx-w/2:.1f}" y="{cy-11:.1f}" width="{w:.1f}" height="22" rx="11" class="pill"/>'
            f'<text x="{cx:.1f}" y="{cy+4:.1f}" text-anchor="middle" font-size="11.5" '
            f'font-weight="600" class="ink">{esc(text)}</text>')

def gen_stack(name, title, bands, labels):
    rows = []
    maxrow = 0
    for layer, bt, boxes in bands:
        rw, ws = band_row_width(boxes)
        bh = BAND_TITLE_H + BAND_PADY + max(box_height(b) for b in boxes) + BAND_PADY
        rows.append((layer, bt, boxes, ws, rw, bh))
        maxrow = max(maxrow, rw)
    inner_w = maxrow + 2 * 24
    W = inner_w + 2 * CARDPAD
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
        rowx = bx + (bw - rw) / 2
        boxtop = y + BAND_TITLE_H + BAND_PADY
        rowh = max(box_height(b) for b in boxes)
        cx0 = rowx
        for b, bwid in zip(boxes, ws):
            body.append(render_box(cx0, boxtop, bwid, rowh, b, layer))
            cx0 += bwid + GAP
        y += bh
        if idx < len(rows) - 1:
            midx = bx + bw / 2
            body.append(f'<line x1="{midx:.1f}" y1="{y+8:.1f}" x2="{midx:.1f}" y2="{y+INTERBAND-8:.1f}" '
                        f'stroke="#8894a2" stroke-width="1.6" marker-end="url(#arw)"/>')
            if idx < len(labels) and labels[idx]:
                body.append(arrow_label(midx, y + INTERBAND / 2, labels[idx]))
            y += INTERBAND
    H = y + CARDPAD
    parts = [svg_header(W, H)]
    parts.append(f'<rect x="1" y="1" width="{W-2}" height="{H-2}" rx="16" class="card" stroke-width="1"/>')
    if title:
        parts.append(f'<text x="{CARDPAD+2}" y="27" font-size="15" font-weight="800" '
                     f'letter-spacing="0.2" class="ink">{esc(title)}</text>')
    parts += body
    parts.append('</svg>')
    open(os.path.join(OUT, name + ".svg"), "w").write("".join(parts))
    return W, H

def gen_chain(name, title, boxes, labels):
    ws = [box_width(b) for b in boxes]
    rowh = max(box_height(b) for b in boxes)
    lblw = [(len(l) * 6.4 + 16 if l else 0) for l in labels]
    ARR = 30
    inner = 0
    for i, w in enumerate(ws):
        inner += w
        if i < len(ws) - 1:
            inner += max(ARR * 2, lblw[i] + 16)
    title_h = 40 if title else CARDPAD
    W = int(round(inner + 2 * CARDPAD + 2 * 10))
    H = int(round(title_h + rowh + CARDPAD))
    x = CARDPAD + 10
    ytop = title_h
    body = []
    for i, b in enumerate(boxes):
        body.append(render_box(x, ytop, ws[i], rowh, b, b[2]))
        x += ws[i]
        if i < len(ws) - 1:
            seg = max(ARR * 2, lblw[i] + 16)
            cy = ytop + rowh / 2
            body.append(f'<line x1="{x+6:.1f}" y1="{cy:.1f}" x2="{x+seg-6:.1f}" y2="{cy:.1f}" '
                        f'stroke="#8894a2" stroke-width="1.6" marker-end="url(#arw)"/>')
            if labels[i]:
                body.append(arrow_label(x + seg / 2, cy - 16, labels[i]))
            x += seg
    parts = [svg_header(W, H)]
    parts.append(f'<rect x="1" y="1" width="{W-2}" height="{H-2}" rx="16" class="card" stroke-width="1"/>')
    if title:
        parts.append(f'<text x="{CARDPAD+2}" y="27" font-size="15" font-weight="800" class="ink">{esc(title)}</text>')
    parts += body
    parts.append('</svg>')
    open(os.path.join(OUT, name + ".svg"), "w").write("".join(parts))
    return W, H

# ---------------- diagram definitions ----------------
# each box is (icon, [text lines]); chain boxes are (icon, [lines], layer)

gen_stack("overview", "Atlas — ArchiMate 3.2 layered view", [
    ("mot", "MOTIVATION  ·  why Atlas is shaped this way", [
        ("driver", ["Drivers", "throughput · durability", "long-running · conformance"]),
        ("principle", ["Principles", "compile-don't-interpret", "event sourcing · single writer"]),
        ("requirement", ["Requirements", "the six invariants"])]),
    ("biz", "BUSINESS  ·  process automation as a capability", [
        ("service", ["Business services", "Automation · Human Tasks", "Decisions · Monitoring"]),
        ("process", ["Processes", "lifecycle · order-to-cash"]),
        ("object", ["Objects", "BPMN · DMN · Form · Instance"])]),
    ("app", "APPLICATION  ·  the software that delivers it", [
        ("component", ["Atlas Engine", "Compiler · Processor", "Data model"]),
        ("component", ["Channels", "Web UI · REST API · MCP"]),
        ("component", ["Connectors", "REST · Mail · Script", "DMN · clio"])]),
    ("tec", "TECHNOLOGY  ·  the runtime that hosts it", [
        ("node", ["Single binary", "Go runtime, no CGO"]),
        ("node", ["Partitions", "single-writer"]),
        ("node", ["Pebble + WAL", "filesystem · group commit"])]),
], ["shapes", "realized by", "realized by / served by"])

gen_chain("motivation-trace", "Motivation trace — one concern, all the way down", [
    ("role", ["Stakeholder", "Operations"], "mot"),
    ("driver", ["Driver", "durability"], "mot"),
    ("goal", ["Goal", "survive crashes"], "mot"),
    ("principle", ["Principle", "event sourcing"], "mot"),
    ("requirement", ["Requirement", "durable before visible", "(invariant 2)"], "mot"),
    ("component", ["Processor + WAL", "group commit"], "app"),
], ["has", "influences", "realized by", "realized by", "realized by"])

gen_stack("business", "Business layer", [
    ("biz", "ROLES  ·  active structure", [
        ("role", ["Process Modeler"]), ("role", ["Task Performer"]), ("role", ["Operations"])]),
    ("biz", "BUSINESS SERVICES  ·  behaviour", [
        ("service", ["Process", "Automation"]), ("service", ["Human-Task", "Handling"]),
        ("service", ["Decision", "Making (DMN)"]), ("service", ["Monitoring", "& Audit"])]),
    ("biz", "BUSINESS OBJECTS  ·  passive structure", [
        ("object", ["BPMN model"]), ("object", ["Process instance"]),
        ("object", ["User task"]), ("object", ["Incident"])]),
], ["assigned to", "access"])

gen_stack("application", "Application layer", [
    ("app", "CHANNELS", [
        ("component", ["Web UI", "bpmn-js"]), ("component", ["REST / HTTP API", "+ OpenAPI"]),
        ("component", ["MCP Server"])]),
    ("core", "ATLAS ENGINE  ·  core", [
        ("component", ["Graph", "Compiler"]), ("component", ["Processor", "single-writer"]),
        ("component", ["Data model", "applyToState"]), ("component", ["WAL", "manager"]),
        ("component", ["State-store", "wrapper"])]),
    ("app", "CONNECTORS  ·  job workers", [
        ("component", ["REST"]), ("component", ["Mail"]), ("component", ["Script"]),
        ("component", ["DMN / temis"]), ("component", ["clio bridge"])]),
], ["serve requests to", "create jobs for"])

gen_stack("technology", "Technology layer", [
    ("tec", "ATLAS SINGLE BINARY  ·  Go runtime, no CGO", [
        ("node", ["Partition 0", "processor · WAL · state"]),
        ("node", ["Partition 1", "processor · WAL · state"]),
        ("node", ["Partition N", "…"])]),
    ("tec", "DURABLE STORAGE", [
        ("node", ["Filesystem", "WAL segments + Pebble SST", "fsync · group commit"])]),
    ("ext", "EXTERNAL SYSTEMS  ·  via connectors (gRPC / HTTPS)", [
        ("node", ["temis", "DMN/FEEL"]), ("node", ["clio", "event store"]),
        ("node", ["Gmail /", "MS Graph"]), ("node", ["polyglot", "job workers"])]),
], ["group commit", "gRPC / HTTPS"])

gen_stack("deployment", "Deployment view — one binary, N partitions, local durability", [
    ("tec", "DEPLOYMENT NODE  ·  the atlas host (single OS process)", [
        ("node", ["Host / container", "Linux · one process"]),
        ("systemsoftware", ["Go runtime", "goroutines · GC · no CGO"]),
        ("artifact", ["atlas binary", "embeds the web UI assets"])]),
    ("tec", "PARTITIONS  ·  single-writer execution environments", [
        ("node", ["Partition 0", "queue · processor"]),
        ("node", ["Partition 1", "queue · processor"]),
        ("node", ["Partition N", "routed by instanceKey % N"])]),
    ("tec", "LOCAL DURABLE STORE  ·  private to each partition", [
        ("artifact", ["WAL segments", "append-only · one fsync/batch"]),
        ("systemsoftware", ["Pebble (LSM-tree)", "column-family indexes"]),
        ("artifact", ["SST files", "materialized state"])]),
    ("ext", "SEPARATE NODES  ·  reached over the network", [
        ("node", ["Job workers", "gRPC stream"]),
        ("node", ["temis", "DMN / FEEL"]),
        ("node", ["clio", "event store"]),
        ("node", ["Gmail / Graph", "HTTPS"])]),
], ["hosts", "each persists to", "integrate via gRPC / HTTPS"])

gen_chain("implementation", "Implementation roadmap — plateaus from M0 to M6", [
    ("plateau", ["M0 Foundations", "done"], "tec"),
    ("plateau", ["M1 Core BPMN", "in progress"], "biz"),
    ("plateau", ["M2 Events & timers", "in progress"], "biz"),
    ("plateau", ["M3 Structure", "planned"], "ext"),
    ("plateau", ["M4 Operability", "planned"], "ext"),
    ("plateau", ["M5 Scale-out", "planned"], "ext"),
    ("plateau", ["M6 Ecosystem", "planned"], "ext"),
], ["", "", "", "", "", ""])

if __name__ == "__main__":
    print("SVGs written to", OUT)
    for f in sorted(os.listdir(OUT)):
        if f.endswith(".svg"):
            print(" ", f, os.path.getsize(os.path.join(OUT, f)), "bytes")
