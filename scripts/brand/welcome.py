#!/usr/bin/env python3
"""Draw the Console's welcome illustration: a rocket built of atlas's modules.

The rocket is a small 3D model — stacked sections, a command capsule, two boosters and
three engines — projected by a perspective camera and painted back to front as SVG
paths. Each section is a module and carries the module's symbol, read from the
Console's own drawing in api/web/module-icons.js: Operations is the engine section,
Data, Tasks, Modeler and Panorama are the stages above it, the Console is the command
capsule and the Marketplace (Catalogue and Shop) are the two boosters. The glow, the
orbit, the planet, the exhaust and the stars are flat.

It writes the same file to docs/brand/atlas-welcome.svg and api/web/welcome.svg (the
copy the Console embeds); api/modulesymbols_internal_test.go holds the two together.

    scripts/brand/welcome.py            # draw both
    scripts/brand/welcome.py --check    # write nothing, exit 1 if either differs

Python 3 only, no packages.
"""
import json, math, os, random, re, sys

ROOT = os.path.normpath(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", ".."))
SOURCE = os.path.join(ROOT, "api", "web", "module-icons.js")
TARGETS = [os.path.join(ROOT, "docs", "brand", "atlas-welcome.svg"), os.path.join(ROOT, "api", "web", "welcome.svg")]

def read_icons():
    src = open(SOURCE, encoding="utf-8").read()
    m = re.search(r"/\* JSON \*/(.*?)/\* /JSON \*/", src, re.S)
    if not m:
        sys.exit(f"{SOURCE}: no table between /* JSON */ and /* /JSON */")
    return json.loads(m.group(1))

# (id, name, colour, glyph) in the Console's order
MODULES = [(mid, ic["name"], ic["colour"], ic["glyph"]) for mid, ic in read_icons().items()]

def v_add(a, b): return (a[0]+b[0], a[1]+b[1], a[2]+b[2])
def v_sub(a, b): return (a[0]-b[0], a[1]-b[1], a[2]-b[2])
def v_mul(a, k): return (a[0]*k, a[1]*k, a[2]*k)
def v_dot(a, b): return a[0]*b[0]+a[1]*b[1]+a[2]*b[2]
def v_cross(a, b): return (a[1]*b[2]-a[2]*b[1], a[2]*b[0]-a[0]*b[2], a[0]*b[1]-a[1]*b[0])
def v_unit(a):
    n = math.sqrt(v_dot(a, a)); return (a[0]/n, a[1]/n, a[2]/n)

# camera: above and in front of the ship's right side, the nose pointing right
EYE, AIM = (2.0, 4.2, 15.5), (0.2, 0.3, 0.0)
FWD = v_unit(v_sub(AIM, EYE)); RIGHT = v_unit(v_cross(FWD, (0, 1, 0))); UP = v_cross(RIGHT, FWD)
FOCAL, CX, CY = 585.0, 250, 146
LIGHT = v_unit((0.3, 0.9, 0.7))

def to_cam(p):
    d = v_sub(p, EYE); return (v_dot(d, RIGHT), v_dot(d, UP), v_dot(d, FWD))
def project(p):
    x, y, z = to_cam(p); return (CX + FOCAL*x/z, CY - FOCAL*y/z), z

FACES = []
def tint(rgb, n, lo):
    k = lo + (1-lo)*max(0.0, v_dot(n, LIGHT))
    return "#%02x%02x%02x" % tuple(min(255, int(c*k)) for c in rgb)
def poly(pts, rgb, n=None, lo=0.78, cull=True, bias=0.0):
    if n is None: n = v_unit(v_cross(v_sub(pts[1], pts[0]), v_sub(pts[2], pts[0])))
    c = tuple(sum(p[i] for p in pts)/len(pts) for i in range(3))
    if cull and v_dot(n, v_sub(EYE, c)) <= 0: return
    FACES.append((to_cam(c)[2] + bias, pts, tint(rgb, n, lo)))

def frame(axis):
    a = v_unit(axis); t = (0, 1, 0) if abs(a[1]) < .9 else (1, 0, 0)
    u = v_unit(v_cross(a, t)); return a, u, v_cross(a, u)

def frustum(p0, p1, r0, r1, rgb, n=28, cap0=False, cap1=False, cap_rgb=None, lo=0.78):
    a, u, v = frame(v_sub(p1, p0))
    ring = lambda c, r, t: v_add(c, v_add(v_mul(u, r*math.cos(t)), v_mul(v, r*math.sin(t))))
    slope = (r0 - r1) / max(1e-6, math.dist(p0, p1))
    for i in range(n):
        t0, t1 = 2*math.pi*i/n, 2*math.pi*(i+1)/n; tm = (t0+t1)/2
        nn = v_unit(v_add(v_add(v_mul(u, math.cos(tm)), v_mul(v, math.sin(tm))), v_mul(a, slope)))
        poly([ring(p0, r0, t0), ring(p0, r0, t1), ring(p1, r1, t1), ring(p1, r1, t0)], rgb, nn, lo)
    if cap1 and r1 > 0: poly([ring(p1, r1, 2*math.pi*i/n) for i in range(n)], cap_rgb or rgb, a, lo)
    if cap0 and r0 > 0: poly([ring(p0, r0, 2*math.pi*i/n) for i in range(n)][::-1], cap_rgb or rgb, v_mul(a, -1), lo)

def dome(c, axis, r, rgb, steps=5, n=28):
    """A hemisphere on c, bulging along axis, as stacked frustums."""
    a = v_unit(axis)
    for k in range(steps):
        t0, t1 = math.pi/2*k/steps, math.pi/2*(k+1)/steps
        frustum(v_add(c, v_mul(a, r*math.sin(t0))), v_add(c, v_mul(a, r*math.sin(t1))), r*math.cos(t0), max(r*math.cos(t1), .001), rgb, n)

HULL, CAP, EDGE, RING, DARK = (250, 251, 255), (232, 237, 247), (196, 205, 222), (188, 197, 214), (60, 72, 96)
COL = {m[0]: tuple(int(m[2][i:i+2], 16) for i in (1, 3, 5)) for m in MODULES}
GLASS = (31, 52, 128)

# ---------------- the rocket ----------------
AX = v_unit((1.0, 0.36, 0.0))                      # the rocket points up and to the right
PERP = v_unit(v_cross((0, 0, 1), AX))              # "up" across the rocket, in the picture's plane
def at(t, off=(0, 0, 0)): return v_add(v_mul(AX, t), off)

R = .82
SECTIONS = [  # module, from, to (along the axis); the engine section first
    ("operations", -3.9, -2.95),
    ("data", -2.95, -1.85),
    ("tasks", -1.85, -.75),
    ("modeler", -.75, .35),
    ("panorama", .35, 1.25),
]
for i, (mid, t0, t1) in enumerate(SECTIONS):
    body = HULL if i % 2 == 0 else CAP
    frustum(at(t0), at(t1), R, R, body)
    m = (t0 + t1) / 2
    frustum(at(m + .18), at(m + .32), R*1.025, R*1.025, COL[mid])          # the module's band
    frustum(at(t1 - .045), at(t1 + .045), R*1.035, R*1.035, RING)           # the joint to the next section
frustum(at(-4.05), at(-3.9), R*1.04, R*1.04, RING, cap0=True, cap_rgb=DARK)
# Panorama's section carries a ring of portholes
for k in range(10):
    ang = 2*math.pi*k/10
    _, u, w = frame(AX)
    p = v_add(at(.62), v_add(v_mul(u, R*1.01*math.cos(ang)), v_mul(w, R*1.01*math.sin(ang))))
    n = v_unit(v_sub(p, at(.62)))
    if v_dot(n, v_unit(v_sub(EYE, p))) > .15:
        frustum(p, v_add(p, v_mul(n, .03)), .1, .1, GLASS, 12, cap1=True, cap_rgb=(96, 130, 220))
# the command capsule: a cone with the bridge's visor, then the tip
frustum(at(1.25), at(1.95), R, R*.72, HULL)
frustum(at(1.95), at(2.55), R*.72, R*.42, GLASS, lo=.9)
frustum(at(2.55), at(3.05), R*.42, R*.12, HULL)
dome(at(3.05), AX, R*.12, CAP, 3)
frustum(at(1.2), at(1.3), R*1.035, R*1.035, COL["console"])
# engines: three bells under the engine section
NOZZLES = []
_, u, w = frame(AX)
for k, ang in enumerate((math.pi/2, math.pi/2 + 2.1, math.pi/2 - 2.1)):
    off = v_add(v_mul(u, .42*math.cos(ang)), v_mul(w, .42*math.sin(ang)))
    p0 = v_add(at(-4.05), off); p1 = v_add(at(-4.6), off)
    frustum(p0, p1, .2, .3, EDGE, 18, cap1=True, cap_rgb=(90, 200, 230))
    NOZZLES.append(p1)
# two boosters, the Marketplace: Catalogue and Shop, strapped to the lower stages
BOOST = []
for mid, side in (("catalog", 1), ("portal", -1)):
    off = v_add(v_mul(PERP, side*(R + .44)), (0, 0, .25))
    frustum(at(-3.75, off), at(-1.3, off), .42, .42, HULL, 22)
    frustum(at(-1.3, off), at(-.75, off), .42, .1, CAP, 22)
    dome(at(-.75, off), AX, .1, CAP, 2, 16)
    frustum(at(-2.35, off), at(-2.22, off), .435, .435, COL[mid], 22)
    frustum(at(-3.9, off), at(-3.75, off), .3, .42, EDGE, 18)
    for t in (-3.2, -1.75):                                                  # struts to the core
        frustum(at(t, v_mul(off, .55)), at(t, v_mul(off, .9)), .05, .05, EDGE, 8)
    NOZZLES.append(at(-4.15, off))
    BOOST.append((mid, off))

# ---------------- where each module's symbol sits ----------------
def face_point(p, axis, r):
    a = v_unit(axis); e = v_unit(v_sub(EYE, p))
    side = v_unit(v_sub(e, v_mul(a, v_dot(e, a))))
    (sx, sy), z = project(v_add(p, v_mul(side, r*1.03))); return sx, sy, FOCAL/z
ANCHORS = {mid: face_point(at((t0 + t1)/2 - .12), AX, R) for mid, t0, t1 in SECTIONS}
ANCHORS["console"] = face_point(at(1.62), AX, R*.86)
for mid, off in BOOST:
    ANCHORS[mid] = face_point(at(-2.85, off), AX, .42)

def badge(mid, x, y, s):
    name, col, glyph = next((m[1], m[2], m[3]) for m in MODULES if m[0] == mid)
    k = s / 58.0
    return (f'<g transform="translate({x:.1f} {y:.1f}) scale({k:.3f})"><circle r="15.5" fill="#fff" stroke="{col}" stroke-opacity=".35" stroke-width="1.5"/>'
            f'<rect x="-11" y="-11" width="22" height="22" rx="6.5" fill="{col}"/><g transform="translate(-8.5 -8.5) scale(.708)">{glyph}</g></g>')

def sparkle(x, y, r, fill, op=1):
    k = r*.28
    return (f'<path d="M{x} {y-r}Q{x+k} {y-k} {x+r} {y}Q{x+k} {y+k} {x} {y+r}Q{x-k} {y+k} {x-r} {y}Q{x-k} {y-k} {x} {y-r}Z" fill="{fill}" opacity="{op}"/>')

def build():
    o = []; a = o.append
    names = ", ".join(m[1] for m in MODULES)
    a('<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 480 320" role="img" aria-labelledby="t">')
    a(f'<title id="t">A rocket climbing through orbit, seen in perspective, built of the modules of atlas — {names} — each section marked with its symbol: the Console in the command capsule, Operations at the engines, the Marketplace as the two boosters; a red planet behind it</title>')
    a('<!-- The welcome illustration of the Console (scripts/brand/welcome.py): no text, no external resources. The rocket is a small 3D model projected in perspective. -->')
    a('<defs>')
    a('<radialGradient id="glow" cx="50%" cy="50%" r="50%"><stop offset="0" stop-color="#7c9dff" stop-opacity=".40"/>'
      '<stop offset=".6" stop-color="#a78bfa" stop-opacity=".14"/><stop offset="1" stop-color="#a78bfa" stop-opacity="0"/></radialGradient>')
    a('<radialGradient id="planet" cx="34%" cy="28%" r="78%"><stop offset="0" stop-color="#ecc9b6"/><stop offset=".45" stop-color="#c98a70"/><stop offset=".8" stop-color="#a5634d"/><stop offset="1" stop-color="#7f4a3b"/></radialGradient><clipPath id="mars"><circle cx="352" cy="196" r="46"/></clipPath>')
    a('<radialGradient id="flame" cx="0%" cy="50%" r="100%"><stop offset="0" stop-color="#ffffff"/><stop offset=".25" stop-color="#67e8f9"/>'
      '<stop offset=".6" stop-color="#818cf8" stop-opacity=".55"/><stop offset="1" stop-color="#c084fc" stop-opacity="0"/></radialGradient>')
    a('<filter id="lift" x="-30%" y="-30%" width="160%" height="170%"><feDropShadow dx="0" dy="3" stdDeviation="3" flood-color="#1e2a55" flood-opacity=".18"/></filter>')
    a('<filter id="soft" x="-10%" y="-10%" width="120%" height="130%"><feDropShadow dx="0" dy="8" stdDeviation="9" flood-color="#1e2a55" flood-opacity=".18"/></filter>')
    a('<filter id="blur" x="-50%" y="-50%" width="200%" height="200%"><feGaussianBlur stdDeviation="1.6"/></filter>')
    a('</defs>')
    a('<circle cx="240" cy="150" r="178" fill="url(#glow)"/>')
    a('<ellipse cx="240" cy="150" rx="216" ry="102" transform="rotate(-8 240 150)" fill="none" stroke="#9fb4ff" stroke-width="1.5" stroke-dasharray="3 7" opacity=".7"/>')
    a('<g transform="translate(-36 -26)"><circle cx="352" cy="196" r="46" fill="url(#planet)"/><g clip-path="url(#mars)" fill="#8a4a38"><path d="M300 186c18-7 40-8 60-4s30 2 44-3v10c-14 6-28 6-44 3s-40-4-60 3Z" opacity=".22"/><path d="M306 214c16-5 34-5 50-2s30 2 42-2v8c-12 5-26 5-42 2s-34-3-50 2Z" opacity=".16"/><ellipse cx="368" cy="176" rx="6" ry="4.4" opacity=".28"/><ellipse cx="340" cy="220" rx="8" ry="5.4" opacity=".24"/><ellipse cx="376" cy="210" rx="4" ry="3" opacity=".26"/><ellipse cx="332" cy="190" rx="3.4" ry="2.4" opacity=".22"/></g><path d="M330 158a46 46 0 0 1 40-6" stroke="#fff7f2" stroke-width="2.6" fill="none" opacity=".4" stroke-linecap="round"/><circle cx="352" cy="196" r="47.5" fill="none" stroke="#e7b9a2" stroke-width="2" opacity=".22"/></g>'
      '')
    # exhaust behind each nozzle, along the axis as the picture shows it
    (ax0, ay0), _ = project(at(-4.0)); (ax1, ay1), _ = project(at(-5.0))
    ang = math.degrees(math.atan2(ay1 - ay0, ax1 - ax0))
    for p in NOZZLES:
        (sx, sy), dz = project(p); s = FOCAL/dz
        a(f'<g transform="translate({sx:.1f} {sy:.1f}) rotate({ang:.1f})"><ellipse cx="{s*.75:.1f}" cy="0" rx="{s*.95:.1f}" ry="{s*.24:.1f}" fill="url(#flame)" filter="url(#blur)"/></g>')
    a('<g filter="url(#soft)" stroke-width=".6" stroke-linejoin="round">')
    for depth, pts, fill in sorted(FACES, key=lambda f: -f[0]):
        q = [project(p)[0] for p in pts]
        if abs(sum(q[i][0]*q[i-1][1] - q[i-1][0]*q[i][1] for i in range(len(q)))) < 0.4:
            continue   # a sliver too thin to see
        d = "M" + "L".join("%.1f %.1f" % xy for xy in q) + "Z"
        a(f'<path d="{d}" fill="{fill}" stroke="{fill}"/>')
    a('</g>')
    a('<g filter="url(#lift)">')
    for mid, (x, y, s) in sorted(ANCHORS.items(), key=lambda kv: kv[1][2]):
        a(badge(mid, x, y, s*1.18))
    a('</g>')
    a(sparkle(150, 30, 8, "#7c5cff") + sparkle(392, 26, 6, "#f59e0b") + sparkle(462, 196, 6, "#14b8a6") + sparkle(26, 120, 4.5, "#ec4899", .85))
    for x, y, c, op in [(352, 268, "#f43f5e", .8), (96, 44, "#7c5cff", .6), (446, 34, "#7c5cff", .5), (22, 34, "#14b8a6", .5), (466, 150, "#0ea5e9", .6)]:
        a(f'<circle cx="{x}" cy="{y}" r="2.6" fill="{c}" opacity="{op}"/>')
    a('</svg>')
    return "\n".join(o)

def main():
    check = "--check" in sys.argv[1:]
    svg = build() + "\n"
    stale = [t for t in TARGETS if not os.path.exists(t) or open(t, encoding="utf-8").read() != svg]
    for t in stale:
        rel = os.path.relpath(t, ROOT)
        if check:
            print(f"stale: {rel}")
        else:
            open(t, "w", encoding="utf-8").write(svg)
            print(f"wrote {rel}")
    if check and stale:
        sys.exit(1)

if __name__ == "__main__":
    main()
