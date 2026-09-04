// Panorama's derived landscape mesh (ADR-0211).
//
// The graph on screen is not a drawing: the server computes it from resources
// Atlas already holds — applications, deployed processes, and the call activities
// between them — so this view says something on an instance where nobody has
// modeled anything. Nothing here is stored, and nothing here is authored.
//
// Layout runs in the browser, in plain JS with no bundler and no CDN (ADR-0012).
// It is a fixed-iteration force simulation rather than an animated one: the same
// graph must land in the same place every time, or a reload looks like a change.
// Beyond the server's size budget the payload arrives already collapsed to
// applications and says so, which this view repeats rather than hides.

import {
  captureView, frameFor, pinsFor, readViews, removeView, saveView, writeViews,
} from "./panorama-views.js";
import {
  exportName, exportStyles, rasterise, save, standaloneSVG, stampLines,
} from "./panorama-export.js";

const esc = (value) => String(value ?? "").replace(/[&<>"']/g, (character) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[character]);

// KIND describes every node kind the mesh can carry: how it is drawn, and what it
// is called in the legend. Restricted and unresolved are deliberately distinct —
// "you may not see it" and "it is not deployed" are different findings, and a
// picture that renders them alike answers the wrong question.
//
// Radii carry rank as well as kind. At a few hundred nodes the eye sorts by size
// before it reads anything, so an application has to be unmistakably the largest
// thing on screen and a leaf unmistakably the smallest — otherwise every node
// competes for attention and the picture reads as one texture.
//
// `r` is the floor of a kind's band and `grow` is how far connectivity may carry a
// node up it (see radiusFor). The bands are deliberately closed: the top of one is
// below the floor of the next, so a much-used worker is drawn larger than a lonely
// one and still smaller than any process. Size therefore says two things at once
// without either overwriting the other — what kind of thing this is, and how much
// of the landscape hangs off it.
const KIND = {
  application: { r: 30, grow: 12, shape: "circle", fill: "var(--accent-soft)", stroke: "var(--accent)", label: "Application" },
  process: { r: 17, grow: 5, shape: "square", fill: "var(--surface)", stroke: "var(--mesh-ink)", label: "Process" },
  // --ok is a fixed green rather than a shade of the configurable accent, so its
  // soft companion is a literal here too. There is no --ok-soft at :root, and
  // defining one would change the one other rule that already asks for it.
  worker: { r: 12, grow: 3.5, shape: "hexagon", fill: "#e8f5ec", stroke: "var(--ok)", label: "Worker" },
  decision: { r: 12, grow: 3.5, shape: "triangle", fill: "var(--accent-soft)", stroke: "var(--accent-hover)", label: "Decision" },
  // A placeholder for something real whose kind we may not learn, so it takes the
  // shape that is not any kind's. Drawing it as one of them would be a guess wearing
  // the same clothes as a fact.
  restricted: { r: 11, grow: 3, shape: "diamond", fill: "var(--bg)", stroke: "var(--muted)", label: "Restricted — outside your access", dashed: true },
  // Shape comes from the id, which names the kind of thing that is missing — see
  // shapeForNode. The fallback is the same "no kind" diamond.
  unresolved: { r: 11, grow: 3, shape: "diamond", fill: "var(--warn-soft)", stroke: "var(--warn)", label: "Unresolved — nothing here provides it", dashed: true },
  // A peer Atlas this server can promote to. Drawn large, because it is the only
  // thing on this landscape whose state was fetched over the network — and therefore
  // the only one that can be *unreachable* or *stale*, which is exactly what somebody
  // scanning for trouble needs to find first.
  target: { r: 24, grow: 6, shape: "pentagon", fill: "var(--surface)", stroke: "var(--accent-hover)", label: "Deployment target — a peer this server can promote to" },
};

// Shape is the third channel, after colour and size, and the one that survives what
// they do not: a printout, a projector, and a reader who does not separate the hues.
// Colour already carries the kind *and* the ArchiMate layer; size already carries
// rank and connectivity. Form was the last thing left, and a landscape of four
// hundred identical circles was spending it on nothing.
//
// Every shape is **inscribed in the circle the layout reserved** — no vertex is
// further from the centre than the radius the simulation kept clear. That is what
// makes the change free: the separation guarantee is stated in circles, and a shape
// that never leaves its circle cannot break it. It also means the shapes are a
// little smaller than the circles they replace, which is the right way round: the
// application stays the largest thing on screen.

// shapeForNode is which outline a node is drawn with.
//
// An unresolved dependency takes the shape of the thing that is *missing* rather
// than a shape meaning "missing": its id names the kind — a deployment, a worker, a
// decision — and drawing the gap in the silhouette of what should fill it says what
// is wrong at a glance. The dashes already say it is not there.
export function shapeForNode(node, notation) {
  // A projection speaks for the kinds it has a word for and stays silent about the
  // rest, so a placeholder keeps the shape that says what it is rather than being
  // dressed as an element of a notation that has no such element.
  const projected = notation ? typeIn(node?.kind, notation) : null;
  if (projected) return projected.shape;
  if (node?.kind === "unresolved") {
    const missing = String(node.id || "").split(":")[1];
    return KIND[missing]?.shape || KIND.unresolved.shape;
  }
  return (KIND[node?.kind] || KIND.process).shape;
}

// shapeVertices returns a shape's corners at radius r, and an empty list for the
// circle, which has none. Exported because "no vertex leaves the reserved circle" is
// the property the layout depends on, and a property is worth checking as arithmetic
// rather than trusting to the drawing code that happens to implement it.
export function shapeVertices(shape, r) {
  const at = (sides, rotation) => Array.from({ length: sides }, (_, i) => {
    const angle = rotation + (i * 2 * Math.PI) / sides;
    return [Math.cos(angle) * r, Math.sin(angle) * r];
  });
  switch (shape) {
    // Apex up, the way a warning triangle and a flowchart decision both point.
    case "triangle": return at(3, -Math.PI / 2);
    // Flat top and bottom, which is what reads as a hexagon rather than as a blob.
    case "hexagon": return at(6, 0);
    // Point up, so it is told from the hexagon by silhouette rather than by counting
    // corners — which nobody does at a glance, and nobody can do at all zoomed out.
    case "pentagon": return at(5, -Math.PI / 2);
    case "diamond": return at(4, -Math.PI / 2);
    // Axis-aligned, so it reads as a tile rather than as a rotated diamond. Its
    // half-diagonal is r, which is what keeps it inside the reserved circle.
    case "square": return at(4, -Math.PI / 4);
    default: {
      // The wide rectangles the notation projections draw in (see NOTATION_SHAPES). Same
      // rule as every other shape: the corners sit *on* the reserved circle, so the
      // separation guarantee transfers unchanged and a projection cannot make two
      // nodes overlap that did not overlap before.
      const rect = RECTS[shape];
      if (!rect) return [];
      const half = r / Math.hypot(rect.aspect, 1);
      const wide = half * rect.aspect;
      return [[-wide, -half], [wide, -half], [wide, half], [-wide, half]];
    }
  }
}

// RECTS are the shapes drawn as rectangles rather than as polygons: aspect is width
// over height, round is the corner radius as a fraction of the short side.
//
// ArchiMate's own convention is the reason there are two of them — a structure
// element is a rectangle and a behaviour element is a rounded one — and C4 draws
// everything as a rounded box and tells its types apart by the annotation under the
// name rather than by silhouette.
const RECTS = {
  square: { aspect: 1, round: 0.26 },
  box: { aspect: 1.9, round: 0.08 },
  rounded: { aspect: 1.9, round: 0.42 },
};

// bodyElement is the node's own outline, as SVG. Everything downstream keys off the
// mesh-body class rather than off the element name, so severity, hover and impact
// styling are unchanged by a node being a square.
//
// data-r carries the radius the layout reserved. The drawn outline is inscribed in
// it and no longer reports it as an attribute of its own, and it is the reserved
// circle — not the polygon — that the separation guarantee is about.
function bodyElement(shape, r, attrs) {
  const common = `class="mesh-body" data-r="${r.toFixed(1)}" ${attrs}`;
  const rect = RECTS[shape];
  if (rect) {
    const half = r / Math.hypot(rect.aspect, 1);
    const wide = half * rect.aspect;
    return `<rect ${common} x="${(-wide).toFixed(1)}" y="${(-half).toFixed(1)}"
      width="${(wide * 2).toFixed(1)}" height="${(half * 2).toFixed(1)}"
      rx="${(half * rect.round * 2).toFixed(1)}"/>`;
  }
  const vertices = shapeVertices(shape, r);
  if (!vertices.length) return `<circle ${common} r="${r.toFixed(1)}"/>`;
  const points = vertices.map(([x, y]) => `${x.toFixed(1)},${y.toFixed(1)}`).join(" ");
  return `<polygon ${common} points="${points}"/>`;
}

// The notations the landscape can be drawn in (ADR-0211 §8).
//
// The *mapping* — what each notation calls each kind, and what it cannot carry —
// comes from the server, and that is the point. Three things read it: these labels,
// the stamp on the image export, and the ArchiMate document the server generates
// from the same landscape. A copy of the table here would eventually have the
// picture calling a node an Application Process beside a file that called it
// something else, which is the failure ADR-0189's connection subset is served to the
// browser to avoid.
//
// What stays here is the half the server has no business having an opinion about:
// which outline to draw. ArchiMate's own convention is a rectangle for structure and
// a rounded one for behaviour; C4 draws everything as the same box and tells its
// types apart by the annotation under the name. Both are rendering.
const NOTATION_SHAPES = {
  "archimate-3.2": {
    application: "box", process: "rounded", worker: "rounded",
    decision: "rounded", target: "box",
  },
  "c4-projection": {
    application: "rounded", process: "rounded", worker: "rounded",
    decision: "rounded", target: "box",
  },
};

// The landscape drawn as itself: Atlas's own kinds, no projection, nothing to
// declare. It is here rather than fetched because it is what the view falls back to
// when the mapping cannot be read at all — a picture in its own vocabulary is never
// wrong about which vocabulary it is in.
const DERIVED_NOTATION = {
  id: "atlas", label: "Atlas (derived)", short: "Atlas",
  projection: false, mappingVersion: 0, types: {}, loss: [],
};

let notations = { atlas: DERIVED_NOTATION };

// useNotations takes what the server serves and adds this side's shapes to it. An
// entry with no shapes is still usable — every kind falls back to its derived
// outline — so a notation the server learns about before this file does degrades to
// a vocabulary change rather than to a blank canvas.
export function useNotations(served) {
  const next = { atlas: DERIVED_NOTATION };
  for (const notation of Array.isArray(served) ? served : []) {
    if (!notation?.id || notation.id === "atlas") continue;
    const shapes = NOTATION_SHAPES[notation.id] || {};
    next[notation.id] = {
      id: notation.id,
      label: notation.label || notation.id,
      short: notation.short || notation.label || notation.id,
      projection: Boolean(notation.projection),
      mappingVersion: notation.mappingVersion ?? 0,
      loss: Array.isArray(notation.loss) ? notation.loss : [],
      // The served row carries what a person is shown *and* the notation's own
      // machine token; the picture wants the first and the exported document the
      // second, and they come from one row so the two cannot drift apart.
      types: Object.fromEntries(Object.entries(notation.types || {}).map(([kind, type]) =>
        [kind, { name: type?.name || kind, type: type?.type || "", shape: shapes[kind] || null }])),
    };
  }
  notations = next;
  return notations;
}

// notationsAvailable is what the picker offers, in the order the server listed them.
export function notationsAvailable() {
  return Object.values(notations);
}

// notationOf resolves an id, falling back to the derived vocabulary. An unknown id
// is a stale saved view or a hand-edited URL, and drawing the landscape as itself is
// the answer that cannot mislead.
export function notationOf(id) {
  return notations[id] || notations.atlas;
}

// typeIn is what a notation calls this kind of node, or null where it has no word
// for it. Null is a real answer and never an empty string: the caller draws the
// derived shape and the legend lists the kind as loss.
export function typeIn(kind, notation) {
  return notationOf(notation?.id ?? notation).types[kind] || null;
}

// DEGREE_FULL is the number of dependencies at which a node is drawn at the top of
// its band. It is a fixed reference rather than the busiest node in this particular
// graph, and that is the point: normalising against the graph would make the same
// node change size when a filter removes something else, so its size would describe
// the current screen rather than the node. Twelve is where the curve below flattens
// — past it, "very connected" is the whole of the answer and the exact count is
// what the panel is for.
const DEGREE_FULL = 12;

// radiusFor draws a node at its kind's floor plus however far its connectivity
// carries it up the kind's band.
//
// Logarithmic, because the difference between one dependency and four is the one
// worth seeing: it is the difference between a leaf and a small hub. Between forty
// and fifty there is nothing left to say that the size could carry, and a linear
// scale would spend the whole band saying it.
export function radiusFor(node, degree) {
  const style = KIND[node.kind] || KIND.process;
  const reach = Math.log2(1 + Math.max(0, degree || 0)) / Math.log2(1 + DEGREE_FULL);
  return style.r + (style.grow || 0) * Math.min(1, reach);
}

// A target is not part of the dependency graph — no edge is derived to it, because
// a promotion is an act rather than a stored relationship and this server does not
// record which of its applications is running over there. It sits beside the
// landscape rather than in it, which is what it is.

// PROVENANCE describes how a node is known (ADR-0211 §2). It is rendered on every
// node, always: a picture that mixed what Atlas found with what somebody declared,
// without saying which is which, is exactly the conflation the record exists to
// prevent. Shape carries it as well as colour — a dashed ring for something only
// declared, a second ring for something known from both sides.
const PROVENANCE = {
  derived: { label: "Derived — Atlas has it, nothing models it" },
  both: { label: "Both — Atlas has it and a model binds to it", ring: true },
  modeled: { label: "Modeled — a model declares it, Atlas does not have it", ghost: true },
};

// SEVERITY is ADR-0211 §4's three classes plus the neutral one, as this view draws
// them. Two rules from the record are structural here rather than stylistic:
//
//   - Color is never the only channel. Every class carries a distinct glyph, and the
//     legend is text, so the picture is readable without color perception.
//   - Unknown is neutral, not a fourth level of badness. Most nodes on a young
//     instance are unobserved, and drawing them as a problem makes the whole mesh a
//     problem — which is how a status view teaches people to ignore it.
const SEVERITY = {
  critical: { glyph: "!", stroke: "var(--danger)", beats: true, label: "Critical — it cannot do work" },
  attention: { glyph: "•", stroke: "var(--mesh-attention)", beats: true, label: "Attention — something inside it went wrong" },
  ok: { glyph: "", stroke: "", label: "OK — nothing is wrong here" },
  unknown: { glyph: "?", stroke: "", label: "Unwatched — nothing here observes it" },
};

// EDGE_KEY is what each derived edge means, in the order the key lists them.
//
// Three kinds, three claims — this application holds that process, this process
// invokes that one, this process needs that worker — and until now two of them were
// drawn alike. The ArchiMate export has always told them apart (Assignment,
// Triggering, Serving), so the canvas was the one surface where the distinction the
// data carries was not on screen.
//
// The strokes live in the stylesheet, keyed by the same kind (.mesh-edge-*), and the
// legend draws its swatches with those classes rather than with a copy of them. The
// order here is the order the key reads in, and it is deliberate: the two kinds that
// carry a failure path first, the structure they hang on last.
const EDGE_KEY = [
  ["calls", "Solid line — calls: a process invokes another process"],
  ["uses", "Dashed line — uses: a process depends on a worker or a decision"],
  ["contains", "Dotted line — belongs to: an application and the processes it holds"],
];

// PULSE_BUDGET is how many beating nodes the view will animate at once.
//
// A landscape where three things are wrong should draw the eye to those three. One
// where two hundred are wrong is not a picture of three problems, it is a picture of
// an outage — and two hundred simultaneous animations say less than a still frame
// does while costing a great deal more to paint. Past the budget the rings stay,
// unmoving: the findings are still marked, they have simply stopped competing.
const PULSE_BUDGET = 80;

// STATE_TEXT names the observation state under a severity (ADR-0189 §6). The class
// is a reading aid for a zoomed-out picture and never a replacement: an operator
// acting on a finding needs the state, so both are shown wherever there is room.
const STATE_TEXT = {
  healthy: "healthy",
  degraded: "degraded",
  "not-ready": "not ready",
  unreachable: "unreachable",
  stale: "stale",
  unbound: "unbound — nothing observes it",
};

// LABEL_TIERS decides which names are painted, from how large they will actually
// come out on screen.
//
// This used to be a rule about *density*: under about twenty-five nodes every name
// was painted, above it only the applications, because a few hundred names on one
// canvas is a wall of text with circles behind it. That rule was solving the wrong
// problem. Names collided because the graph was compressed into the viewport, so
// there was no room between nodes for them — and the count was standing in for the
// crowding it caused.
//
// With the graph laid out in a world of its own size there is always room beside a
// node for its name, in world units. What decides whether it can be *read* is how
// much of the screen a world unit gets, which is the zoom. So the question becomes
// the honest one: at this magnification, is this text large enough to read? A name
// is painted when it is, and it is not painted when it would be a smear — which is
// what a zoomed-out universe should look like, and why zooming in is how you read
// it.
//
// Applications cross the threshold first. They are the largest things on screen and
// carry the largest names, and they are what somebody navigates by: "where is
// Billing" is the first question asked of this view, and it has to be answerable
// before the detail is.
//
// Everything else still shows its name on hover and on keyboard focus, which the
// stylesheet does with no re-render, and the selected node keeps it while selected.
const LABEL_TIERS = {
  // The font sizes, in world units, that the stylesheet paints each tier at. They
  // are sized against the circles they belong to — a process is 17 units across the
  // radius, an application 30 — so a name stays proportionate to its node at every
  // magnification instead of swelling or shrinking relative to it.
  all: 15,
  anchors: 26,
  // readable is the smallest rendered text worth painting, in screen pixels. Below
  // it a name is not small, it is noise sitting on top of the structure the picture
  // is carrying.
  readable: 7,
};

// labelTier reports which names a given magnification can carry. scale is screen
// pixels per world unit.
export function labelTier(scale) {
  if (LABEL_TIERS.all * scale >= LABEL_TIERS.readable) return "all";
  if (LABEL_TIERS.anchors * scale >= LABEL_TIERS.readable) return "anchors";
  return "none";
}

// mulberry32 is a small seeded PRNG. The seed is fixed so the initial scatter —
// and therefore the settled layout — is identical on every load of the same graph.
function mulberry32(seed) {
  return function () {
    seed |= 0; seed = (seed + 0x6D2B79F5) | 0;
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

// NODE_ROOM is the personal space a node needs beyond its own circle: enough for
// its name and a gap to the next one. It is what makes the difference between a
// graph that is technically non-overlapping and one somebody can read.
const NODE_ROOM = 34;

// WORLD_FILL is how much of the world the nodes' own cells take up.
//
// It is what decides between a pile and a void, and it was tuned by measuring
// rather than by taste. The thing to hold on to is that it trades air against
// magnification and nothing else: the opening view fits the whole world into the
// canvas, so a roomier world is shown at a smaller scale, and the nodes and their
// names give up exactly what the gaps between them gain. Far in either direction
// costs the picture — at 0.09 a hundred nodes settle with far more space than
// reading needs and the landscape is too small on screen to carry any names at all;
// well above 0.3 the names are large and sit on each other.
//
// 0.22 rather than the 0.28 it was, because the landscape dropped the centred
// content column (see .landscape-mode in app.css) and the width that freed had to
// be spent on one or the other. Measured on the 42-node fixture the e2e suite uses,
// at a 1400px window: in the old column, nodes 6.4-14.8px in the radius with 56px
// between the average nearest pair. Widened at 0.28 they are 7.7-18.0px with 62px —
// the same picture, magnified. Widened at 0.22 they are 6.5-15.2px, the size they
// already were, with 72px. So the reclaimed width goes into the space between
// things rather than into making them bigger, which is what a landscape that is
// hard to take in at a glance actually needs.
const WORLD_FILL = 0.22;

// radiusOf is the radius a node is actually drawn at. renderGraph sizes every node
// once, from its connectivity, and everything downstream — the world budget, the
// separation pass, the circle itself — asks here rather than re-deriving it, so
// they cannot disagree about how big a node is.
function radiusOf(node) {
  return node.r ?? (KIND[node.kind] || KIND.process).r;
}

// worldFor sizes the space the graph is laid out in, from the graph rather than
// from the viewport.
//
// This is the correction that matters. The layout used to settle inside the frame
// and then be scaled to fill it — and fitToFrame scales *positions* while radii
// stay fixed, so any graph whose settled extent exceeded the frame was compressed
// into it with its circles left at full size. That is arithmetic that guarantees
// overlap, and it got worse with every node added, which is exactly how a landscape
// ends up as a knot of interpenetrating bubbles.
//
// So the world grows with the content instead. The frame is a window onto it, the
// opening view shows the whole thing, and reading it closely is what the zoom is
// for. A small graph still gets at least a frame's worth of world, so nothing
// changes for the handful-of-nodes case that was already comfortable.
function worldFor(nodes, frame) {
  let cells = 0;
  for (const n of nodes) {
    // The node's own radius, not its kind's floor: connectivity has already sized
    // it, and a world budgeted from the floor would be too small for the hubs.
    const cell = 2 * (radiusOf(n) + NODE_ROOM);
    cells += cell * cell;
  }
  const aspect = Math.max(frame.width, 1) / Math.max(frame.height, 1);
  const area = Math.max(cells / WORLD_FILL, frame.width * frame.height);
  const width = Math.sqrt(area * aspect);
  return { width, height: width / aspect };
}

// separate pushes overlapping circles apart until none intersect, in whatever
// coordinates it is handed.
//
// It runs *after* the fit as well as inside the settle, and that is the point: the
// settle's guarantee is made in layout coordinates, and a rescale carries positions
// across while radii stay behind. Re-establishing it where the circles are actually
// drawn is the only place the guarantee means anything.
function separate(nodes, radii, gap, rounds = 24) {
  for (let round = 0; round < rounds; round++) {
    let moved = false;
    for (let i = 0; i < nodes.length; i++) {
      for (let j = i + 1; j < nodes.length; j++) {
        const a = nodes[i], b = nodes[j];
        let dx = a.x - b.x, dy = a.y - b.y;
        let d = Math.hypot(dx, dy);
        if (d < 0.01) { dx = 0.1; dy = 0.1; d = 0.1414; }
        const room = radii[i] + radii[j] + gap;
        if (d >= room) continue;
        // A node somebody is holding does not move: the other one gets out of its
        // way instead. Splitting the push evenly would slide a held node out from
        // under the pointer, which is the one thing a drag must never do.
        const [pushA, pushB] = share(room - d, a.held, b.held);
        a.x += (dx / d) * pushA; a.y += (dy / d) * pushA;
        b.x -= (dx / d) * pushB; b.y -= (dy / d) * pushB;
        moved = true;
      }
    }
    if (!moved) return;
  }
}

// share splits an overlap between two nodes, giving the whole of it to whichever
// one is free to move. Both held is a standoff: neither moves, and the arrangement
// somebody placed by hand is left exactly as they placed it.
function share(overlap, aHeld, bHeld) {
  if (aHeld && bHeld) return [0, 0];
  if (aHeld) return [0, overlap];
  if (bHeld) return [overlap, 0];
  return [overlap / 2, overlap / 2];
}

// forcesFor derives the constants the simulation runs on from the world it runs in.
//
// Everything scales with the world rather than being a fixed number, because a
// graph settled at a fixed spring length inside a world sized for its content is a
// knot in the middle of an empty field — and enlarging the knot is not the same as
// spreading it out. Repulsion also scales with the node count so density stays
// roughly constant instead of rising with it: the "Klüngel" a fixed constant
// produces, where fifty nodes are comfortable and three hundred are one dark blob.
function forcesFor(nodes, width, height) {
  const reach = Math.min(width, height);
  // The frame is usually wider than it is tall. Both the initial scatter and the
  // centring pull are shaped by that ratio, so the settled graph is the shape of
  // the space it has to live in.
  const aspect = Math.max(width, 1) / Math.max(height, 1);
  return {
    cx: width / 2, cy: height / 2,
    pullX: 1 / aspect, pullY: aspect,
    repulsion: 5200 * Math.max(1, Math.sqrt(nodes.length / 40)) * Math.max(1, reach / 720),
    spring: 0.012,
    damping: 0.85,
    rest: Math.max(130, reach * 0.16),
  };
}

// settle runs the simulation over nodes that already have positions, for as many
// steps as it is given. It is the whole of the physics, and it is a function of its
// own so that a drag can run a few steps of exactly the same thing the initial
// layout runs two hundred of — a graph that settled one way while being dragged and
// another way on the next paint would be two layouts wearing one name.
//
// A node marked `held` is not simulated: its position is whatever put it there, and
// everything else arranges itself around it.
function settle(nodes, links, radii, force, iterations) {
  for (let step = 0; step < iterations; step++) {
    for (let i = 0; i < nodes.length; i++) {
      for (let j = i + 1; j < nodes.length; j++) {
        const a = nodes[i], b = nodes[j];
        let dx = a.x - b.x, dy = a.y - b.y;
        let d2 = dx * dx + dy * dy;
        if (d2 < 0.01) { dx = 0.1; dy = 0.1; d2 = 0.02; }
        const magnitude = force.repulsion / d2;
        const d = Math.sqrt(d2);
        const fx = (dx / d) * magnitude, fy = (dy / d) * magnitude;
        a.vx += fx; a.vy += fy; b.vx -= fx; b.vy -= fy;

        // Separation. Repulsion alone is a soft force that a spring can overpower,
        // so two nodes joined by an edge will happily sit on top of each other —
        // which is the one arrangement that makes a picture unreadable rather than
        // merely tight. This pushes overlapping circles apart directly, and it is
        // in the same pass because that pass already visits every pair.
        const room = radii[i] + radii[j] + NODE_ROOM;
        if (d < room) {
          const [pushA, pushB] = share((room - d) * 0.5, a.held, b.held);
          a.x += (dx / d) * pushA; a.y += (dy / d) * pushA;
          b.x -= (dx / d) * pushB; b.y -= (dy / d) * pushB;
        }
      }
    }
    for (const [ai, bi] of links) {
      const a = nodes[ai], b = nodes[bi];
      const dx = b.x - a.x, dy = b.y - a.y;
      const d = Math.hypot(dx, dy) || 0.01;
      const magnitude = (d - force.rest) * force.spring;
      const fx = (dx / d) * magnitude, fy = (dy / d) * magnitude;
      a.vx += fx; a.vy += fy; b.vx -= fx; b.vy -= fy;
    }
    for (const n of nodes) {
      // A held node keeps its place and its stillness: carrying velocity through a
      // drag would make it spring away the moment it was let go.
      if (n.held) { n.vx = 0; n.vy = 0; continue; }
      // The pull toward the centre is anisotropic, weaker along the wider axis, so
      // the graph settles into the shape of the frame instead of into a disc. A disc
      // in a wide viewport is what produced the empty bands on either side: the
      // content was never the shape of the space it had.
      n.vx += (force.cx - n.x) * 0.0012 * force.pullX;
      n.vy += (force.cy - n.y) * 0.0012 * force.pullY;
      n.vx *= force.damping; n.vy *= force.damping;
      n.x += n.vx; n.y += n.vy;
    }
  }
}

// linksAmong resolves edges to index pairs, dropping any whose ends are not on
// screen — a filtered graph carries edges to nodes it no longer contains.
function linksAmong(nodes, edges) {
  const index = new Map(nodes.map((n, i) => [n.id, i]));
  return edges
    .map((e) => [index.get(e.from), index.get(e.to)])
    .filter(([a, b]) => a !== undefined && b !== undefined);
}

// tethersFor records the edges that a drag will actually move, and how long each of
// them is right now.
//
// Recording the *current* length rather than the layout's rest length is the whole
// trick. A settled graph has been fitted since it was simulated, and the fit
// rescales every distance — so its edges are not at the simulation's rest length,
// and a spring aiming for that length would haul the neighbourhood inward the
// moment somebody touched a node, without them having moved it anywhere. Aiming for
// the length the edge has at the instant of the grab means nothing moves until the
// node does, and then only in proportion to how far it went.
//
// Only edges touching a held node are recorded. Those are the ones whose geometry
// is about to change; everything else must stay exactly where the reader last saw
// it, and moves only if something ends up on top of it.
function tethersFor(nodes, edges) {
  const index = new Map(nodes.map((n, i) => [n.id, i]));
  const out = [];
  for (const e of edges) {
    const a = index.get(e.from), b = index.get(e.to);
    if (a === undefined || b === undefined) continue;
    if (!nodes[a].held && !nodes[b].held) continue;
    out.push([a, b, Math.hypot(nodes[a].x - nodes[b].x, nodes[a].y - nodes[b].y)]);
  }
  return out;
}

// follow moves the neighbours of whatever is being held, and gets everything else
// out of the way. This is what a drag calls on every frame.
//
// Deliberately local, and deliberately not the layout's own physics. Resuming the
// full simulation from a settled picture reorganises the whole landscape the moment
// a node is touched — the picture on screen has been fitted since it was simulated,
// so it is not at the simulation's equilibrium and restarting it there is a second
// layout wearing the first one's coordinates. What somebody dragging a node is
// asking for is smaller and more useful than that: the things joined to it come
// along, and whatever they run into moves aside.
function follow(nodes, tethers, radii, { steps = 3 } = {}) {
  for (let step = 0; step < steps; step++) {
    for (const [ai, bi, rest] of tethers) {
      const a = nodes[ai], b = nodes[bi];
      const dx = b.x - a.x, dy = b.y - a.y;
      const d = Math.hypot(dx, dy) || 0.01;
      // Toward the length the edge had when it was grabbed, a fraction at a time, so
      // a neighbour trails the node it is joined to instead of being welded to it.
      const [moveA, moveB] = share((d - rest) * 0.28, a.held, b.held);
      a.x += (dx / d) * moveA; a.y += (dy / d) * moveA;
      b.x -= (dx / d) * moveB; b.y -= (dy / d) * moveB;
    }
    // Two rounds rather than one pass to convergence: a drag is continuous, so each
    // frame only has to move the picture a little way toward being untangled, and
    // the next frame carries on from there.
    separate(nodes, radii, NODE_ROOM, 2);
  }
  return nodes;
}

// layout settles the graph with repulsion between every pair, springs along edges,
// and a pull toward the centre. Returns the elapsed milliseconds so the caller can
// report what the budget actually costs on this machine.
//
// `pinned` is the map of nodes somebody has dragged somewhere. It changes two
// things, and both are consequences of one rule — a hand-placed node stays where it
// was placed:
//
//   - Pinned nodes are held throughout, so the simulation arranges the rest of the
//     landscape around them instead of pulling them back.
//   - The fit is skipped while anything is pinned, because fitting rescales every
//     position and would slide the pins off the spots they were dropped on. So the
//     picture stops re-framing itself once you start arranging it by hand, which is
//     the trade: your arrangement is worth more than the last few percent of margin.
//
// `from` is where the nodes already are, so a repaint continues the picture on
// screen rather than re-deriving one around the pins. Without it, filtering after a
// drag would keep the pinned nodes and re-scatter everything else — the arrangement
// would survive and its context would not, which is the worse half of both.
function layout(nodes, edges, { width, height, iterations = 220, pinned, from, margin = LABEL_MARGIN } = {}) {
  const started = performance.now();
  const random = mulberry32(0x5EED);
  const force = forcesFor(nodes, width, height);
  // Anchored by the pins that are actually on screen: a pin on something a filter
  // removed must not stop the rest of the picture from being fitted.
  const anchored = Boolean(pinned) && nodes.some((n) => pinned.has(n.id));

  for (const n of nodes) {
    const pin = pinned?.get(n.id);
    const was = from?.get(n.id);
    if (pin) {
      // Wherever it was put, world or no world. The clamp that used to be here was
      // the same argument place() made — a pin outside the world is somewhere the
      // fitted view never shows — and it stopped holding when the fit began framing
      // the content: a pin beyond the edge is inside the next Fit, and a resize that
      // shrinks the world no longer drags an arrangement back through it.
      n.x = pin.x;
      n.y = pin.y;
      n.held = true;
    } else if (anchored && was) {
      n.x = was.x; n.y = was.y;
    } else {
      // Seeded scatter on an ellipse, so nothing starts coincident (which would make
      // the repulsion term divide by zero and fling nodes to infinity).
      const angle = random() * Math.PI * 2;
      const radius = 0.15 + random() * 0.85;
      n.x = force.cx + Math.cos(angle) * radius * width * 0.42;
      n.y = force.cy + Math.sin(angle) * radius * height * 0.42;
    }
    n.vx = 0; n.vy = 0;
  }

  // Every node's own footprint, so the separation pass knows what "touching" means
  // for this pair rather than assuming one radius for all of them.
  const radii = nodes.map(radiusOf);
  settle(nodes, linksAmong(nodes, edges), radii, force, iterations);
  if (!anchored) fitToFrame(nodes, width, height, margin);
  // And once more where the circles are actually drawn. The fit scales positions
  // and leaves radii alone, so whatever the settle guaranteed is only true again
  // after this. Anything it moves outside the world is pulled back by the re-fit.
  separate(nodes, radii, NODE_ROOM);
  if (!anchored) fitToFrame(nodes, width, height, margin);
  return performance.now() - started;
}

// LABEL_MARGIN is the room a node needs around its own centre. It is asymmetric
// because a node's label is: the text hangs below the circle (dy = r + 14) and is
// centred, so the bottom carries more than the top does.
//
// The vertical figures are the largest node's own arithmetic rather than a guess.
// fitToFrame places *centres* inside this margin, so anything the margin does not
// cover leaves the frame: the biggest application is drawn at r = 42 (KIND.r 30 plus
// its full growth), and its name — 26px, baseline at r + 14 — reaches about 62 below
// that centre. At the 26 and 42 these were, a node landing on the top edge could put
// 16 units of its own outline outside the frame and one on the bottom edge 20 units
// of its name; 46 and 68 are those two numbers with a little room over.
//
// (An earlier note here blamed the margin for an export whose lowest names were drawn
// across the provenance stamp. That was a different bug — the file laid the picture
// out against the whole page rather than against its band, see standaloneSVG — and
// the margin is only what the arithmetic above says it is.)
//
// The sides stay under the widest name on purpose. A long name is centred, so
// covering it would mean reserving half a label on both edges — the picture would
// shrink by more than the overhang costs, and a name overhanging into empty canvas
// is legible where a smaller picture is not.
const LABEL_MARGIN = { top: 46, right: 46, bottom: 68, left: 46 };

// fitToFrame maps the settled graph onto the frame so it fills it, leaving only the
// margin a label needs. The scale is uniform: stretching the axes independently
// would fill the last pixel of the frame and misreport distance, and distance is the
// one thing a force layout is trying to say.
//
// Positions are scaled, radii are not, so nodes stay round and legible at any graph
// size. A single node — or a set that settled on one line — has no extent on some
// axis, so that axis falls back to the frame rather than dividing by zero.
export function fitToFrame(nodes, width, height, pad = LABEL_MARGIN) {
  if (!nodes.length) return nodes;
  // The margin is what a node's own decoration needs, not decoration itself: a label
  // hangs below its circle, so the bottom needs more than the top, and a long name is
  // centred so it overhangs sideways. Sizing each side to what it actually carries is
  // what turns the leftover space back into picture.
  const m = typeof pad === "number" ? { top: pad, right: pad, bottom: pad, left: pad } : pad;
  const xs = nodes.map((n) => n.x), ys = nodes.map((n) => n.y);
  const minX = Math.min(...xs), maxX = Math.max(...xs);
  const minY = Math.min(...ys), maxY = Math.max(...ys);
  const spanX = maxX - minX, spanY = maxY - minY;
  const usableW = Math.max(width - m.left - m.right, 1);
  const usableH = Math.max(height - m.top - m.bottom, 1);
  const scale = Math.min(
    spanX > 0.001 ? usableW / spanX : Infinity,
    spanY > 0.001 ? usableH / spanY : Infinity,
  );
  // Everything coincident: nothing to scale, so centre it and stop.
  const k = Number.isFinite(scale) ? scale : 1;
  const offsetX = m.left + (usableW - spanX * k) / 2 - minX * k;
  const offsetY = m.top + (usableH - spanY * k) / 2 - minY * k;
  for (const n of nodes) {
    n.x = n.x * k + offsetX;
    n.y = n.y * k + offsetY;
  }
  return nodes;
}

// contentBox is the box the drawn nodes actually occupy, in world units, including
// each node's own footprint and the room its name needs beside it.
//
// It exists because the world and the picture in it are not the same thing. The
// world is an *area budget* — sized from the graph so the layout has room to settle
// — and the layout normally spreads the content across it, so the two coincide and
// framing the world frames the picture. They stop coinciding the moment a node is
// pinned: the fit is skipped then (it would drag the pins off the spots they were
// dropped on), and the content is left wherever it settled. Framing the world after
// that shows the picture in one corner of a mostly empty sheet, which is exactly
// what "Fit" is for and exactly what it stopped doing.
export function contentBox(nodes, pad = LABEL_MARGIN) {
  if (!nodes.length) return { x: 0, y: 0, width: 0, height: 0 };
  const m = typeof pad === "number" ? { top: pad, right: pad, bottom: pad, left: pad } : pad;
  let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
  for (const n of nodes) {
    const r = radiusOf(n);
    minX = Math.min(minX, n.x - r - m.left);
    maxX = Math.max(maxX, n.x + r + m.right);
    minY = Math.min(minY, n.y - r - m.top);
    maxY = Math.max(maxY, n.y + r + m.bottom);
  }
  return { x: minX, y: minY, width: maxX - minX, height: maxY - minY };
}

// fitView frames a content box in a viewport, and keeps a corner of that viewport
// clear.
//
// Two things it does that framing the world did not:
//
//   - **The returned view has the viewport's own aspect ratio**, so the SVG has
//     nothing to letterbox and the content is centred in what is left rather than
//     pushed to one side of a box of a different shape.
//   - **It holds `reserve` pixels of the bottom-right corner free.** The zoom
//     controls float over the canvas there, and a node underneath them cannot be
//     clicked or dragged — the pointer lands on the panel. That is not a rare
//     coincidence either: the fit pushes content to the edges by construction, so
//     the corner is where a node reliably ends up, and it happens most in a filtered
//     or drilled picture, where there are few enough nodes for one of them to be the
//     one you wanted. Reserving the corner in the *framing* rather than in the
//     layout keeps the arithmetic exact: the scale is known here, so pixels of chrome
//     convert to world units without a second guess.
//
// The chrome sits in a *corner*, and that is the whole subtlety. Subtracting its
// width and its height both — the obvious reading — reserves two full strips whose
// intersection is the corner, and gives away a quarter of a wide canvas to a panel
// two hundred pixels across. What the picture actually has to avoid is the corner
// rectangle, and the largest rectangle that avoids it is one of exactly two: the
// frame minus the panel's width, or the frame minus its height. Whichever holds the
// content at the larger scale wins, which on a landscape-shaped canvas with a short
// panel is nearly always the second and costs almost nothing.
export function fitView(box, frame, reserve = { width: 0, height: 0 }) {
  const boxW = Math.max(box.width, 1), boxH = Math.max(box.height, 1);
  // Chrome is chrome: it may never take more than half the picture, whatever it
  // reports its size as.
  const takeW = Math.min(reserve.width || 0, frame.width * 0.5);
  const takeH = Math.min(reserve.height || 0, frame.height * 0.5);
  const options = [
    { w: Math.max(frame.width - takeW, 1), h: Math.max(frame.height, 1), takeW, takeH: 0 },
    { w: Math.max(frame.width, 1), h: Math.max(frame.height - takeH, 1), takeW: 0, takeH },
  ];
  let best = null;
  for (const option of options) {
    const scale = Math.min(option.w / boxW, option.h / boxH);
    if (!best || scale > best.scale) best = { ...option, scale };
  }
  const { scale } = best;
  const w = frame.width / scale, h = frame.height / scale;
  // Whatever the box does not use of the region it is allowed, split evenly, so the
  // picture sits in the middle of the space it can actually be reached in.
  const leftoverX = Math.max(w - best.takeW / scale - boxW, 0);
  const leftoverY = Math.max(h - best.takeH / scale - boxH, 0);
  return { x: box.x - leftoverX / 2, y: box.y - leftoverY / 2, w, h };
}

// ZOOM_RANGE bounds how far the viewer can push the frame, as multiples of the
// fitted one. In is capped where a node fills the frame and there is nothing further
// to see. Out was capped at 1.6 on the argument that zooming past the content only
// adds the empty space the fit exists to remove — true when the content could not
// leave the world, and no longer true now that a node can be dragged anywhere: past
// the fitted frame there is arrangement to find, and pulling back to look for it is
// how somebody finds it without giving up their arrangement to Fit.
const ZOOM_RANGE = { min: 1 / 24, max: 4 };

// zoomView returns the frame after zooming by `factor` about a point, in the same
// user units as the frame itself. Zooming about the pointer rather than the centre
// is what makes a wheel feel like a map instead of a slider: whatever is under the
// cursor stays under it.
//
// Pure, so the behaviour can be checked without a browser: the frame is data, and
// the only thing the DOM does with it is carry it into a viewBox attribute.
export function zoomView(view, factor, focus, base) {
  const limitOut = base.w * ZOOM_RANGE.max, limitIn = base.w * ZOOM_RANGE.min;
  const w = Math.min(Math.max(view.w * factor, limitIn), limitOut);
  const applied = w / view.w; // what the clamp actually allowed
  const h = view.h * applied;
  return {
    x: focus.x - (focus.x - view.x) * applied,
    y: focus.y - (focus.y - view.y) * applied,
    w,
    h,
  };
}

// DEPENDENCY_EDGES are the edge kinds impact analysis walks. Containment is
// deliberately absent: an application *contains* its processes, it does not depend
// on them, and walking it would drag every sibling into the answer through their
// shared application — which would make "what breaks if this goes down" name half
// the landscape and mean nothing.
const DEPENDENCY_EDGES = new Set(["calls", "uses"]);

// degreesOf counts, for every node, how many dependency edges touch it.
//
// Containment is left out for the same reason impact analysis leaves it out: an
// application does not depend on the processes it holds, and counting them would
// make every application a hub by construction and say nothing. What is counted is
// what would actually propagate — calls and uses — so the count means "how much
// traffic runs through this", which is what makes a node worth noticing.
//
// Both ends of an edge are counted, and self-edges once, so a node that calls three
// things and is called by two has degree five.
export function degreesOf(graph) {
  const degree = new Map(graph.nodes.map((n) => [n.id, 0]));
  const bump = (id) => { if (degree.has(id)) degree.set(id, degree.get(id) + 1); };
  for (const e of graph.edges) {
    if (!DEPENDENCY_EDGES.has(e.kind)) continue;
    bump(e.from);
    if (e.to !== e.from) bump(e.to);
  }
  return degree;
}

// impactFrom answers ADR-0211 §6's question over the graph the viewer already has:
// what breaks if this node goes down (direction "dependents", walking edges
// backwards), or what this node needs to work at all ("dependencies", forwards).
// "both" walks either way.
//
// It runs on the delivered graph rather than through a second endpoint, and that is
// the point: the answer must be about the picture on screen. A server-side walk over
// an unfiltered graph could name resources this viewer cannot see, and a second
// implementation could disagree with the drawing it is supposed to explain.
//
// Two rules carry the honesty of the answer:
//
//   - A restricted placeholder is included but never walked through. We may not see
//     past it, so the reachable set beyond it is unknown — the result records every
//     placeholder it stopped at and reports complete: false. An impact answer that
//     quietly stopped at a permission boundary would read as "nothing further
//     depends on this", which is the one thing it must not say.
//   - Unknown ids return null rather than an empty set, for the same reason: an
//     empty answer means "nothing depends on this", and that is a claim.
//
// Returns { starts, nodes, direct, edges, truncatedBy, complete } or null.
export function impactFrom(graph, startId, opts = {}) {
  return impactOf(graph, [startId], opts);
}

// impactOf is the same question asked about several nodes at once: what stops if all
// of these go down together.
//
// It is one walk from all of them rather than a walk each, and that is the whole
// point rather than an optimisation. Blast radii overlap, so the union is not the
// sum — two services that share a queue break the same eleven things, and a window
// panel that added their counts would say twenty-two where the truth is eleven.
// Seeding the frontier with every start makes the union the thing that is computed
// and the individual totals the thing derived from it (see windowOverlap), which is
// the direction that cannot produce a number nobody could reach.
//
// An id that is not in this picture makes the whole answer null, as it does for one:
// a window is a claim about a specific set, and silently planning around two of the
// three nodes somebody named is worse than refusing.
export function impactOf(graph, startIds, { direction = "dependents", depth = Infinity } = {}) {
  const starts = [...new Set(startIds || [])];
  if (!starts.length) return null;
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
  if (starts.some((id) => !byId.has(id))) return null;
  const walked = walkImpact(edgeIndex(graph), byId, starts, { direction, depth });
  return {
    starts,
    nodes: [...walked.hops.keys()],
    // The nodes with an edge straight to one of these. They are the difference
    // between "who do I call about this" and "how far does it go": a direct dependent
    // breaks at its own boundary, and everything past it breaks because that one did.
    direct: [...walked.hops].filter(([, hop]) => hop === 1).map(([id]) => id),
    edges: walked.edges,
    truncatedBy: walked.truncatedBy,
    complete: walked.truncatedBy.length === 0,
  };
}

// edgeIndex builds the adjacency an impact walk needs, once. It is separate from
// the walk because the ranking below walks from every node in the graph, and
// rebuilding this per node would turn an O(N·E) answer into an O(N·E) answer with a
// large constant in front of it for no reason.
function edgeIndex(graph) {
  const forward = new Map(), backward = new Map();
  for (const e of graph.edges) {
    if (!DEPENDENCY_EDGES.has(e.kind)) continue;
    if (!forward.has(e.from)) forward.set(e.from, []);
    if (!backward.has(e.to)) backward.set(e.to, []);
    forward.get(e.from).push(e);
    backward.get(e.to).push(e);
  }
  return { forward, backward };
}

// walkImpact is the traversal itself, shared by the single answer and the ranking.
// One walk with two callers rather than two walks: the ranking's numbers and the
// panel's have to be the same numbers, and the only way to guarantee that is for
// them to come from the same code.
//
// hops maps every reached node to its distance from the start, which is what makes
// "direct" answerable; edges are collected only when a caller wants to draw them.
function walkImpact(index, byId, starts, { direction, depth, edges: wantEdges = true }) {
  const step = (id) => {
    const out = [];
    if (direction !== "dependencies") {
      out.push(...(index.backward.get(id) || []).map((e) => [e, e.from]));
    }
    if (direction !== "dependents") {
      out.push(...(index.forward.get(id) || []).map((e) => [e, e.to]));
    }
    return out;
  };

  const from = new Set(starts);
  const hops = new Map([...from].map((id) => [id, 0]));
  const seenEdges = new Set();
  const edges = [];
  const truncatedBy = [];
  // Asking about the boundary itself is a different question from arriving at one.
  // The edges *into* a placeholder are ours — the nodes at their other end are in
  // this caller's own picture — so they are walked; what is beyond it is not, and
  // the payload draws no edge out of a placeholder for exactly that reason. So the
  // walk runs and the answer is a floor. Skipping the start instead would have
  // answered "nothing depends on this", which is the one thing a boundary must
  // never be allowed to say.
  for (const id of from) {
    if (byId.get(id)?.kind === "restricted") truncatedBy.push(id);
  }
  let frontier = [...from];
  for (let hop = 0; hop < depth && frontier.length; hop++) {
    const next = [];
    for (const id of frontier) {
      // A placeholder stands for something we may not see, so its own edges are not
      // ours to follow — it is a boundary, not a waypoint.
      if (!from.has(id) && byId.get(id)?.kind === "restricted") continue;
      for (const [edge, other] of step(id)) {
        if (wantEdges && !seenEdges.has(edge)) {
          seenEdges.add(edge);
          edges.push(edge);
        }
        if (hops.has(other)) continue;
        hops.set(other, hop + 1);
        if (byId.get(other)?.kind === "restricted") truncatedBy.push(other);
        next.push(other);
      }
    }
    frontier = next;
  }
  return { hops, edges, truncatedBy };
}

// impactSummary is the answer's shape rather than only its size: how many, how many
// of them are already in trouble, and how many are one edge away.
//
// The severity mix is *not* a causal claim, and the panel that renders it says so.
// A node's class is what that node reports about itself; that three of a worker's
// twelve dependents are critical may be the worker's fault, may be why the worker
// looks busy, or may be unrelated. What the mix is good for is triage — a blast
// radius that is already burning is a different morning from one that is quiet —
// and stating causation from a correlation is how a panel stops being believed.
export function impactSummary(graph, result, startId) {
  if (!result) return null;
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
  // Every node the question was asked about, not only the one named here. With a
  // window of three, the other two are things the reader is taking down on purpose —
  // counting them as collateral would inflate the answer with the plan itself.
  const from = new Set(result.starts || [startId]);
  const others = result.nodes.filter((id) => !from.has(id));
  const bySeverity = { critical: 0, attention: 0, ok: 0, unknown: 0 };
  for (const id of others) {
    const sev = byId.get(id)?.severity;
    bySeverity[sev in bySeverity ? sev : "unknown"] += 1;
  }
  return {
    total: others.length,
    direct: result.direct.length,
    indirect: others.length - result.direct.length,
    bySeverity,
    complete: result.complete,
  };
}

// impactList names the nodes the count is counting, worst first.
//
// The count and the highlight together still leave the reader hunting: on four
// hundred circles "twelve depend on this" means finding twelve lit dots, and the
// three that matter are the ones already reporting a problem. This is the findings
// list's argument applied to the impact answer — the same nodes, as an index.
//
// Direct before transitive within a severity, because those are the ones somebody
// has to be told about first.
export function impactList(graph, result, startId, { limit = 8 } = {}) {
  if (!result) return [];
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
  const direct = new Set(result.direct);
  const from = new Set(result.starts || [startId]);
  return result.nodes
    .filter((id) => !from.has(id))
    .map((id) => ({ ...(byId.get(id) || { id }), direct: direct.has(id) }))
    .sort((a, b) =>
      ((SEVERITY_ORDER[b.severity] ?? 0) - (SEVERITY_ORDER[a.severity] ?? 0)) ||
      (Number(b.direct) - Number(a.direct)) ||
      String(a.name || a.id).localeCompare(String(b.name || b.id)))
    .slice(0, limit);
}

// blastRanking answers the impact question without being asked about a node first.
//
// Until now the analysis needed a selection, which means the reader had to already
// suspect the node that matters — and "which of these four hundred would hurt most"
// is the question they actually arrive with, especially before a change. So the walk
// runs from every node and the results are ranked.
//
// It is O(N·E) over the graph already in the browser, which the size budget (§7)
// bounds: past it the payload arrives collapsed to applications, so N is a few dozen
// rather than a few hundred exactly where the ranking would have cost the most.
//
// Ties go to the node that is itself in trouble. Two nodes carrying twelve each are
// not the same finding when one of them is already failing.
export function blastRanking(graph, { direction = "dependents", depth = Infinity, limit = 6 } = {}) {
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
  const index = edgeIndex(graph);
  const rows = [];
  for (const node of graph.nodes) {
    const walked = walkImpact(index, byId, [node.id], { direction, depth, edges: false });
    const total = walked.hops.size - 1;
    if (total <= 0) continue;
    rows.push({
      id: node.id, name: node.name, kind: node.kind, severity: node.severity,
      total,
      direct: [...walked.hops.values()].filter((hop) => hop === 1).length,
      // A walk that stopped at a permission boundary produces a floor, not a total —
      // and in a *ranking* that matters twice over, because the order is a claim
      // about the rows as well as the numbers.
      complete: walked.truncatedBy.length === 0,
    });
  }
  rows.sort((a, b) =>
    (b.total - a.total) ||
    ((SEVERITY_ORDER[b.severity] ?? 0) - (SEVERITY_ORDER[a.severity] ?? 0)) ||
    String(a.name || a.id).localeCompare(String(b.name || b.id)));
  return rows.slice(0, limit);
}

// windowOverlap is the arithmetic a maintenance window needs and a count cannot give.
//
// The question behind it is not "what does each of these break" but "what does the
// evening cost". Those differ, and the difference is the whole reason to plan a
// window rather than three changes: blast radii overlap, so three services that
// break twelve, nine and seven things do not break twenty-eight. Adding the counts
// is the mistake this function exists to make impossible — the union is walked (see
// impactOf) and the individual totals are reported beside it, so the two numbers are
// on screen together and the reader can see how much of the cost is shared.
//
// Two things it reports that a single answer has no way to say:
//
//   - **shared** — how many nodes sit in more than one radius. It is a count of
//     nodes rather than the difference between the two totals, because with three or
//     more starts a node reached by all of them is double-counted twice and "sum
//     minus union" would name a number that is not a set of anything.
//   - **covered** — the selected nodes that another selected node already takes down.
//     Taking those down changes nothing the window does not already do, which is
//     worth knowing before writing it into a change request.
//
// Every individual total excludes the other selected nodes for the same reason the
// union does: they are going down on purpose, and counting the plan as its own
// collateral inflates every number in the panel.
export function windowOverlap(graph, startIds, { direction = "dependents", depth = Infinity } = {}) {
  const union = impactOf(graph, startIds, { direction, depth });
  if (!union) return null;
  const from = new Set(union.starts);
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
  const index = edgeIndex(graph);

  const reachedBy = new Map();
  const each = union.starts.map((id) => {
    const walked = walkImpact(index, byId, [id], { direction, depth, edges: false });
    const reach = [...walked.hops.keys()].filter((other) => !from.has(other));
    for (const other of reach) reachedBy.set(other, (reachedBy.get(other) || 0) + 1);
    const node = byId.get(id) || {};
    return {
      id, name: node.name, kind: node.kind, severity: node.severity,
      total: reach.length,
      // The other selected nodes this one reaches, which is what makes one of them
      // redundant in the window rather than merely overlapping with it.
      covers: [...walked.hops.keys()].filter((other) => other !== id && from.has(other)),
      complete: walked.truncatedBy.length === 0,
    };
  });

  const covered = new Set();
  for (const row of each) for (const id of row.covers) covered.add(id);
  return {
    starts: union.starts,
    each,
    total: union.nodes.filter((id) => !from.has(id)).length,
    sum: each.reduce((n, row) => n + row.total, 0),
    shared: [...reachedBy.values()].filter((times) => times > 1).length,
    covered: [...covered],
    complete: union.complete,
  };
}

// hrefFor is the drilldown. A process node leads to the Operations live view —
// Panorama owns the landscape and application altitudes and links into the
// process and instance ones rather than reimplementing them (ADR-0211 §5).
function hrefFor(node) {
  if (node.kind === "process") {
    const key = node.id.slice("process:".length);
    return `#/operations/p/${encodeURIComponent(key)}`;
  }
  return "";
}

function nodeTitle(node, notation) {
  // What the notation calls it, first, because in a projection that is the word the
  // reader is looking at. Atlas's own name for the kind follows it rather than being
  // replaced: the projection is a way of speaking about these resources, not a claim
  // that they are something else.
  const typed = typeIn(node.kind, notation);
  if (typed) {
    const parts = [node.name || node.id, typed.name,
      `Atlas ${(KIND[node.kind] || {}).label?.split(" — ")[0]?.toLowerCase() || node.kind}`];
    if (node.state && node.state !== "unbound") parts.push(STATE_TEXT[node.state] || node.state);
    if (node.reason) parts.push(node.reason);
    return parts.join(" · ");
  }
  if (node.kind === "target") {
    // Never its base URL: that is this operator's map of where their infrastructure
    // lives, and a landscape is opened by anybody with modeler access.
    const parts = [node.name || node.id, "deployment target"];
    if (node.state && node.state !== "unbound") parts.push(STATE_TEXT[node.state] || node.state);
    if (node.reason) parts.push(node.reason);
    return parts.join(" · ");
  }
  if (node.kind === "restricted") {
    return "A resource outside your access. The dependency is real; its identity is not shown.";
  }
  if (node.kind === "unresolved") {
    // The id carries what kind of thing is missing, which is what makes the
    // sentence actionable: a missing deployment and a missing worker are fixed in
    // different places.
    const of = node.id.split(":")[1] || "dependency";
    return `Nothing on this server provides the ${of} "${node.name}". Work reaching it would park.`;
  }
  const parts = [node.name || node.id];
  if (node.modelName && node.modelName !== node.name) parts.push(`modeled as “${node.modelName}”`);
  if (node.processId) parts.push(`${node.processId} v${node.version}`);
  if (node.workerType) parts.push(`${node.workerType} worker`);
  if (node.children) parts.push(`${node.children} process(es) collapsed`);
  // The state, then the reason, then — if it was inherited — which descendant it
  // came from. ADR-0211 §4 requires the last of those: a red parent that cannot say
  // which child is red is not actionable, and trains an operator to ignore the color.
  if (node.state && node.state !== "unbound") parts.push(STATE_TEXT[node.state] || node.state);
  if (node.reason) parts.push(node.reason);
  if (node.severityFrom) parts.push(`inherited from ${node.severityFrom}`);
  return parts.join(" · ");
}

// matches decides what a search term keeps. It reads the name, the kind, and a
// process's BPMN id — the three things somebody actually types — and never the
// node id, whose prefixes would make every term match its own kind by accident.
function matches(node, term) {
  if (!term) return true;
  // Severity and state are search axes, not only colours (ADR-0211 §6 names status
  // among the things the mesh must be filterable by): typing "critical" is how an
  // operator gets from a few hundred nodes to the handful that are broken.
  const hay = [node.name, node.kind, node.processId, node.workerType, node.severity, node.state]
    .filter(Boolean).join(" ").toLowerCase();
  return hay.includes(term);
}

// CONTEXT_HOPS is how far around a match the filter reaches for context.
//
// One, and it is not a placeholder for a setting. A filtered node on its own is a
// circle in an empty field: it answers "does this exist" and nothing else, when the
// question somebody types a name to ask is nearly always "and what is it attached
// to". One hop answers that. Two would not answer it better — nearly everything in a
// landscape hangs off some hub, and reaching through one drags most of the graph back
// onto the screen, which is the filter failing to filter.
const CONTEXT_HOPS = 1;

// filterGraph keeps the matching nodes, the immediate neighbourhood around them, and
// every edge between what is left.
//
// The neighbourhood is *context*, and it is marked as such rather than presented as a
// result: `matched` names the nodes that actually matched the term, the drawing draws
// the rest more faintly, and the header counts them separately. A search that
// silently returned things which do not match the search would be a worse answer than
// the empty field it is fixing.
//
// A search is the viewer's own choice, unlike a sharing scope, so what is left out
// here is not a lie — but the header still reports how much is hidden, because a
// filtered mesh looks exactly like a small one.
function filterGraph(graph, term) {
  if (!term) return graph;
  const matched = new Set(graph.nodes.filter((n) => matches(n, term)).map((n) => n.id));
  return around(graph, matched, CONTEXT_HOPS);
}

// around cuts the graph down to a set of nodes and whatever is within `hops` of
// them, marking which of the survivors were asked for and which are only there to
// explain them.
//
// One walk for both ways of narrowing the picture — a search and a drilldown — for
// the reason every other rule in this file is written once: two implementations of
// "and its neighbours" would eventually disagree about what a neighbour is, and the
// difference would show up as a picture that answers a slightly different question
// depending on how you got to it.
//
// Direction is not consulted. "What is this attached to" includes the application it
// sits in and the worker it calls alike; a hop that only counted arrows pointing one
// way would answer a question nobody asked while looking like this one.
function around(graph, seeds, hops) {
  const keep = new Set(seeds);
  let frontier = [...seeds];
  for (let hop = 0; hop < hops && frontier.length; hop++) {
    const reached = new Set(frontier);
    const next = [];
    for (const e of graph.edges) {
      for (const [here, there] of [[e.from, e.to], [e.to, e.from]]) {
        if (!reached.has(here) || keep.has(there)) continue;
        keep.add(there);
        next.push(there);
      }
    }
    frontier = next;
  }
  return {
    ...graph,
    matched: new Set(seeds),
    nodes: graph.nodes.filter((n) => keep.has(n.id)),
    edges: graph.edges.filter((e) => keep.has(e.from) && keep.has(e.to)),
  };
}

// drillInto is the whole landscape reduced to one node and what it touches.
//
// It is the same cut as a search that matched exactly one thing, and deliberately
// so: reading a landscape is mostly narrowing, and there should be one idea of what
// narrowed looks like rather than one per gesture. What differs is the reach — a
// search keeps one hop because a name can match a hundred nodes, while a drilldown
// starts from exactly one and can afford to follow the depth already on screen.
//
// Returns null when the node is not in this graph. A drilldown onto nothing is not
// an empty landscape; it is a question that can no longer be asked, and the caller
// has to say so rather than draw a blank canvas.
export function drillInto(graph, id, hops) {
  if (!graph.nodes.some((n) => n.id === id)) return null;
  return around(graph, new Set([id]), hops);
}

// legendEntries is the key to the picture: one swatch per thing the picture
// actually contains, drawn by the same functions that drew it.
//
// It exists as data rather than as markup because the legend now has two readers.
// Beside the canvas it is HTML in the page; inside an exported file it is SVG in
// the artifact, where it matters more — the app has a legend one scroll away and a
// file that travels has nothing at all, so a hexagon or an orange dot arrives
// undefined. Two renderers over one list, for the same reason the swatch is drawn
// by the node's own function: a second list would eventually explain a picture it
// no longer matched.
//
// Only what is present is listed, in every group. A legend describing findings the
// picture does not contain is a legend nobody reads twice.
function legendEntries(graph, notation) {
  const spoken = notationOf(notation?.id ?? notation);
  const present = new Set(graph.nodes.map((n) => n.kind));
  const entries = Object.entries(KIND)
    .filter(([kind]) => present.has(kind))
    .map(([kind, style]) => {
      const typed = typeIn(kind, spoken);
      return {
        group: "kind",
        tone: "",
        // In a projection the swatch is labelled with the notation's word and keeps
        // Atlas's own beside it. Replacing it outright would leave a reader unable
        // to get from the picture back to the thing it is about, which is the whole
        // reason they opened the landscape.
        label: typed ? `${typed.name} — ${style.label.split(" — ")[0]}` : style.label,
        mark: `<g transform="translate(8,8)">${bodyElement(typed?.shape || style.shape, 6,
          `fill="${style.fill}" stroke="${style.stroke}" stroke-width="2" ` +
          (style.dashed ? 'stroke-dasharray="3 2"' : ""))}</g>`,
      };
    });

  // The line styles — the half of the picture the shapes above do not explain.
  // Every swatch so far names a *thing*; every line on the canvas is a claim about
  // how the estate hangs together, and a reader could see that some edges were drawn
  // differently with no way to find out from what.
  //
  // One row per kind the picture actually contains, and the swatch is drawn with the
  // canvas's own class rather than with a copy of its dash pattern — the same rule
  // paints both, so the key cannot come to disagree with the picture it explains.
  // That holds in an exported file too: the harvested stylesheet carries every
  // `.mesh-` rule, this one included.
  const edgeKinds = new Set((graph.edges || []).map((e) => e.kind));
  for (const [kind, label] of EDGE_KEY) {
    if (!edgeKinds.has(kind)) continue;
    entries.push({
      group: "edge", tone: "",
      label,
      mark: `<line x1="1" y1="8" x2="15" y2="8" class="mesh-edge mesh-edge-${kind}"/>`,
    });
  }

  const severityPresent = new Set(graph.nodes.map((n) => n.severity).filter(Boolean));
  for (const key of ["critical", "attention", "ok", "unknown"]) {
    if (!severityPresent.has(key)) continue;
    entries.push({
      group: "severity",
      // The class the swatch's colour comes from, carried on its own rather than
      // baked into a class list: the page wants it beside .mesh-swatch, and the
      // export wants it without — an inline-flex rule means nothing on an SVG
      // group and only invites the browser to interpret it.
      tone: `mesh-sev-${key}`,
      label: SEVERITY[key].label,
      mark: `<circle cx="8" cy="8" r="6" fill="var(--surface)"
        stroke="${SEVERITY[key].stroke || "var(--border-strong)"}" stroke-width="2"/>` +
        (SEVERITY[key].glyph
          ? `<text x="8" y="11.5" text-anchor="middle" class="mesh-sev-glyph">${esc(SEVERITY[key].glyph)}</text>`
          : ""),
    });
  }

  // Provenance only once a model has been overlaid: with none, everything is
  // derived and three swatches saying so would be three swatches about nothing.
  if (graph.modeled > 0 || graph.unmodeled > 0 || graph.outOfScope > 0) {
    for (const key of ["derived", "both", "modeled"]) {
      entries.push({
        group: "provenance",
        tone: "",
        label: PROVENANCE[key].label,
        mark: (PROVENANCE[key].ring
          ? `<circle cx="8" cy="8" r="7" fill="none" stroke="var(--muted)" stroke-width="1" opacity="0.55"/>`
          : "") +
          `<circle cx="8" cy="8" r="5" fill="${PROVENANCE[key].ghost ? "none" : "var(--surface)"}"
            stroke="var(--muted)" stroke-width="2"
            ${PROVENANCE[key].ghost ? 'stroke-dasharray="3 2"' : ""}/>`,
      });
    }
  }
  return entries;
}

function legendHTML(graph, layoutMs, notation, instances = false) {
  const spoken = notationOf(notation?.id ?? notation);
  const swatch = (entry) => `<span class="mesh-swatch ${entry.tone}">
    <svg width="16" height="16" aria-hidden="true">${entry.mark}</svg>${esc(entry.label)}</span>`;
  const entries = legendEntries(graph, spoken);
  const swatches = entries.filter((e) => e.group === "kind").map(swatch).join("");
  // On a row of its own under the shapes: the labels are sentences rather than
  // words, and threaded in among the kinds they would push the shapes off the line.
  const rules = entries.filter((e) => e.group === "edge").map(swatch).join("");

  const notes = [];
  if (graph.restricted > 0) {
    notes.push(`<p class="mesh-note"><b>${graph.restricted}</b> node(s) are hidden by your
      access. Their dependencies are drawn, their identities are not — this picture is
      filtered, and says so rather than looking complete.</p>`);
  }
  if (graph.clustered) {
    notes.push(`<p class="mesh-note">This starmap exceeded the size budget, so it is
      collapsed to applications. Each one states how many nodes it stands for.</p>`);
  }
  // What the count on the canvas does and does not say. A process with nothing
  // running carries no number, and a reader who did not know that would read its
  // absence as "not measured" — which is the one thing it does not mean.
  if (instances) {
    notes.push(`<p class="mesh-note">Running instances are drawn under the names that
      have any. A process with none carries no number; select it to see the zero, and
      what it has finished.</p>`);
  }
  // The comparison counts only mean something once a model has been overlaid; with
  // none, saying "0 unmodeled" would imply the landscape had been checked.
  const compared = graph.modeled > 0 || graph.unmodeled > 0 || graph.outOfScope > 0;
  if (compared) {
    if (graph.modeled > 0) {
      notes.push(`<p class="mesh-note"><b>${graph.modeled}</b> node(s) are declared by a
        model and not present here. That is drift the drawing alone could not show.</p>`);
    }
    if (graph.unmodeled > 0) {
      notes.push(`<p class="mesh-note"><b>${graph.unmodeled}</b> node(s) exist here and no
        model mentions them.</p>`);
    }
    if (graph.outOfScope > 0) {
      notes.push(`<p class="mesh-note"><b>${graph.outOfScope}</b> binding(s) point at
        releases, deployment targets or runtimes. This view does not draw those, so they
        are neither matched nor missing — counted here so they are not simply dropped.</p>`);
    }
  }

  const status = graph.status || {};
  const severity = entries.filter((e) => e.group === "severity").map(swatch).join("");

  // What the picture cannot see is stated beside what it can. Without this an
  // instance nothing observes renders as uniformly well, and a green view that has
  // no way to go red is worse than no view.
  if (Array.isArray(status.unavailable) && status.unavailable.length) {
    notes.push(`<p class="mesh-note"><b>Not watched here:</b>
      ${status.unavailable.map((u) => esc(STATE_TEXT[u.state] || u.state)).join(", ")}.
      ${esc(status.unavailable[0].reason)}</p>`);
  }
  if (status.partial) {
    notes.push(`<p class="mesh-note mesh-truncated">Counting parked work stopped at its
      bound, so a node reported as OK here is a floor rather than a verdict.</p>`);
  }

  const provenance = entries.filter((e) => e.group === "provenance").map(swatch).join("");

  // A projection has to say that it is one, and say what it drops (ADR-0211 §8). A
  // picture in somebody else's vocabulary that does not name the vocabulary is a
  // picture claiming to be a model of it, and the loss list is the difference
  // between a projection and a lie of omission.
  const projection = spoken.projection ? `<details class="mesh-projection">
    <summary><b>Projected into ${esc(spoken.label)}</b>
      <span class="muted">mapping v${esc(spoken.mappingVersion)} · read-only · what it drops</span></summary>
    <p class="mesh-note">Atlas's own resources, drawn in ${esc(spoken.short)}'s vocabulary.
      Nothing on this landscape was modelled, and this projection cannot be edited or
      exported as a ${esc(spoken.short)} document.</p>
    <ul class="mesh-loss">${spoken.loss.map((l) => `<li>${esc(l)}</li>`).join("")}</ul>
  </details>` : "";

  return `<div class="mesh-legend">
    ${projection}
    <div class="mesh-swatches">${swatches}</div>
    ${rules ? `<div class="mesh-swatches mesh-rules">${rules}</div>` : ""}
    ${severity ? `<div class="mesh-swatches">${severity}</div>` : ""}
    ${provenance ? `<div class="mesh-swatches">${provenance}</div>` : ""}
    <div class="mesh-meta">${compared
      ? `Compared against the architecture models you can see. Everything unmarked is
         <b>derived</b> from this server's resources.`
      : `Everything here is <b>derived</b> from this server's resources — nothing on this
         view was drawn.`}
      <span class="muted">Laid out in ${Math.round(layoutMs)} ms.</span></div>
    ${notes.join("")}
  </div>`;
}

function renderGraph(graph, layoutMs, frame, { pinned, from, notation, instances = false } = {}) {
  const spoken = notationOf(notation?.id ?? notation);
  // A projected node carries a second line under its name, so the margin the layout
  // reserves has to carry it too — otherwise the type annotation is the one thing
  // that ends up outside the frame.
  // A projected node carries a second line under its name, and the type is routinely
  // longer than the thing it is typing — "[Application Component]" against
  // "Onboarding". Both directions have to grow, or the annotation is the one part of
  // the picture that ends up over the edge of it.
  // Two things can hang a line under a node's name — the notation's word for it, and
  // its running-instance count — and the margin has to carry however many are on.
  const underlines = (spoken.projection ? 1 : 0) + (instances ? 1 : 0);
  const margin = underlines
    ? { top: LABEL_MARGIN.top, right: LABEL_MARGIN.right + (spoken.projection ? 44 : 0),
        bottom: LABEL_MARGIN.bottom + 16 * underlines,
        left: LABEL_MARGIN.left + (spoken.projection ? 44 : 0) }
    : LABEL_MARGIN;
  // Sized before anything else asks how big they are: connectivity decides the
  // radius, and the world budget, the separation pass and the circle all read it
  // back off the node (see radiusOf) rather than working it out again.
  const degree = degreesOf(graph);
  const nodes = graph.nodes.map((n) => ({ ...n, r: radiusFor(n, degree.get(n.id)) }));
  // The graph is laid out in a world of its own size, not in the viewport. The
  // frame only decides that world's shape, so the opening view fills the window
  // without letterboxing.
  const world = worldFor(nodes, frame);
  const { width, height } = world;
  const ms = layout(nodes, graph.edges, { width, height, pinned, from, margin }) + layoutMs;
  const at = new Map(nodes.map((n) => [n.id, n]));

  // One class per derived kind; the stylesheet gives each its own stroke and EDGE_KEY
  // names it for the legend. A kind this build does not know still draws — as the
  // plain line, unexplained — rather than not drawing at all.
  const edges = graph.edges.map((e) => {
    const a = at.get(e.from), b = at.get(e.to);
    if (!a || !b) return "";
    return `<line x1="${a.x.toFixed(1)}" y1="${a.y.toFixed(1)}"
      x2="${b.x.toFixed(1)}" y2="${b.y.toFixed(1)}"
      data-from="${esc(e.from)}" data-to="${esc(e.to)}"
      class="mesh-edge mesh-edge-${esc(e.kind || "calls")}"/>`;
  }).join("");

  const circles = nodes.map((n) => {
    const style = KIND[n.kind] || KIND.process;
    const r = radiusOf(n);
    const label = n.kind === "restricted" ? "" : esc(n.name || "");
    const prov = PROVENANCE[n.provenance] || PROVENANCE.derived;
    const sev = SEVERITY[n.severity] || SEVERITY.unknown;
    // Severity is drawn as a badge on the node's own outline rather than by
    // recolouring it: the fill already carries the ArchiMate layer and the kind, and
    // ADR-0189 §6 keeps those. The glyph is what makes the finding readable without
    // colour perception at all.
    const badge = sev.glyph
      ? `<g class="mesh-badge" transform="translate(${(r * 0.72).toFixed(1)},${(-r * 0.72).toFixed(1)})">
           <circle r="7" class="mesh-badge-dot"/>
           <text text-anchor="middle" dy="3.5" class="mesh-badge-glyph">${esc(sev.glyph)}</text>
         </g>`
      : "";
    // Every node carries its name. Which of them are painted is the stylesheet's
    // decision, from the current magnification (see labelTier) — so zooming reveals
    // names with no re-render, and a selected, hovered or focused node keeps its own
    // whatever the zoom is.
    const named = Boolean(label);
    // Context, not a result: this node is on screen because something next to it
    // matched. Drawn more faintly so the filter is still answering the question it
    // was asked, and named all the same — context nobody can read is not context.
    const context = graph.matched ? !graph.matched.has(n.id) : false;
    // What the notation calls this, written under the name. It is C4's own idiom and
    // ArchiMate's corner icon spelled out, and it is the only thing that makes a
    // canvas of identical boxes readable at all.
    const typed = typeIn(n.kind, spoken);
    // How much is running here, when the reader has asked for it. Only where there
    // is something to say: on a landscape of four hundred processes, "0 running"
    // four hundred times is a wall of text that hides the eleven numbers somebody
    // turned this on to find. The panel says the zero for whichever node is
    // selected, and the legend says that the canvas does not.
    const running = instances && n.runtime && n.runtime.running > 0 ? n.runtime.running : 0;
    const runsAt = r + 28 + (typed ? 14 : 0);
    return `<g transform="translate(${n.x.toFixed(1)},${n.y.toFixed(1)})"
      class="mesh-node mesh-${n.kind} mesh-prov-${esc(n.provenance || "derived")} mesh-sev-${esc(n.severity || "unknown")}${named ? " mesh-named" : ""}${context ? " mesh-context" : ""}${n.held ? " mesh-pinned" : ""}"
      data-node-id="${esc(n.id)}" data-severity="${esc(n.severity || "unknown")}"
      tabindex="0" role="button" aria-label="${esc(nodeTitle(n, spoken))}">
      ${sev.beats ? `<circle class="mesh-beat" r="${r.toFixed(1)}"/>` : ""}
      <circle class="mesh-halo" r="${(r + 6).toFixed(1)}"/>
      ${prov.ring ? `<circle r="${(r + 4).toFixed(1)}" fill="none" stroke="${style.stroke}" stroke-width="1" opacity="0.55"/>` : ""}
      ${bodyElement(shapeForNode(n, spoken), r,
        `fill="${prov.ghost ? "none" : style.fill}" stroke="${sev.stroke || style.stroke}" ` +
        `stroke-width="${sev.stroke ? 3 : 2.2}" ${style.dashed || prov.ghost ? 'stroke-dasharray="4 3"' : ""}`)}
      <circle class="mesh-pin" r="4" cx="${(-r * 0.72).toFixed(1)}" cy="${(r * 0.72).toFixed(1)}"/>
      ${n.children ? `<text class="mesh-count" text-anchor="middle" dy="4">${n.children}</text>` : ""}
      ${badge}
      <text class="mesh-label" text-anchor="middle" dy="${(r + 14).toFixed(1)}"><tspan class="mesh-label-ink">${label}</tspan></text>
      ${typed ? `<text class="mesh-type" text-anchor="middle" dy="${(r + 28).toFixed(1)}"><tspan class="mesh-label-ink">[${esc(typed.name)}]</tspan></text>` : ""}
      ${running ? `<text class="mesh-runs" text-anchor="middle" dy="${runsAt.toFixed(1)}"><tspan class="mesh-label-ink">${running} running</tspan></text>` : ""}
      <title>${esc(nodeTitle(n, spoken))}</title></g>`;
  }).join("");

  // Beating is switched on for the whole canvas rather than per node, so the budget
  // is one decision about this picture instead of a rule each node applies to itself.
  const beating = nodes.filter((n) => (SEVERITY[n.severity] || {}).beats).length;

  // The viewBox starts as the whole world, because that is what fitToFrame put the
  // content inside. The world carries the frame's own aspect ratio, so with
  // preserveAspectRatio's default there is nothing to letterbox — the opening
  // picture is the entire landscape, filling the window.
  return { ms, world, nodes, margin, svg: `<svg class="mesh-canvas${
    beating && beating <= PULSE_BUDGET ? " mesh-beating" : ""}" viewBox="0 0 ${width} ${height}"
    role="img" aria-label="Derived starmap">
    <g class="mesh-edges">${edges}</g>${circles}</svg>` };
}


// rankingHTML answers "where is the risk on this landscape" without a selection.
//
// Impact analysis has always needed one, which quietly assumes the reader already
// suspects the right node. Before a change, or on an instance somebody has just
// been handed, that assumption is exactly wrong: the question is which node would
// hurt most, and the only way to answer it by clicking was to click all of them.
//
// It follows the direction and depth controls rather than fixing its own, so this
// list and the panel are always answering the same question. Two blast-radius
// numbers on one page that were measured differently would be worse than one.
function rankingHTML(graph, direction, depth) {
  const rows = blastRanking(graph, { direction, depth });
  const heading = direction === "dependencies" ? "Most dependent"
    : direction === "both" ? "Most entangled" : "Biggest blast radius";
  const sub = direction === "dependencies" ? "how much each one needs to work"
    : direction === "both" ? "how much each one is connected to"
    : "how much stops if this one does";
  const reach = depth === Infinity ? "any" : depth;

  if (!rows.length) {
    // Said as a fact about the edges rather than as reassurance: a landscape whose
    // processes call nothing has no blast radius to rank, and that is not the same
    // as a safe one.
    return `<div class="mesh-rank">
      <div class="mesh-rank-head"><b>${esc(heading)}</b></div>
      <p class="mesh-note">Nothing here depends on anything else within ${esc(reach)}
      hop(s), so there is no radius to rank. Containment is not counted: an
      application holds its processes, it does not depend on them.</p></div>`;
  }
  return `<div class="mesh-rank">
    <div class="mesh-rank-head">
      <b>${esc(heading)}</b>
      <span class="muted">${esc(sub)}, within ${esc(reach)} hop(s)</span>
    </div>
    <ol class="mesh-rank-list">${rows.map((r) => `<li>
      <button type="button" class="mesh-rank-go mesh-sev-${esc(r.severity || "unknown")}"
        data-finding="${esc(r.id)}">
        <span class="mesh-rank-who">${esc(r.name ||
          String((KIND[r.kind] || {}).label || r.kind || r.id).split(" — ")[0])}</span>
        <span class="mesh-rank-count">${r.complete ? "" : "at least "}<b>${r.total}</b>
          node(s)<span class="muted"> · ${r.direct} direct</span></span>
      </button></li>`).join("")}</ol>
  </div>`;
}

// findingsHTML lists every node with something wrong with it, worst first.
//
// The picture already says which nodes those are, and on a landscape of four hundred
// circles that is not the same as being able to read them: finding three red dots
// means hunting, and hunting is what somebody does instead of noticing. The list is
// the same findings as an index — the count, the state, and the sentence behind it —
// and clicking one goes there.
//
// It counts incidents where there are incidents to count. An incident belongs to a
// token and only a process has tokens, so a node without a count is a node that
// cannot have one rather than a node with none: the two are different facts and the
// list never renders them alike.
function findingsHTML(graph) {
  const found = graph.nodes
    .filter((n) => (SEVERITY[n.severity] || {}).beats)
    .sort((a, b) => (SEVERITY_ORDER[b.severity] - SEVERITY_ORDER[a.severity]) ||
      ((b.incidents || 0) - (a.incidents || 0)) ||
      String(a.name || a.id).localeCompare(String(b.name || b.id)));

  const incidents = found.reduce((sum, n) => sum + (n.incidents || 0), 0);
  // The list describes the picture, so a filtered picture gets a filtered list — and
  // then has to say so. "Findings" over a landscape showing one node in seven would
  // otherwise read as the findings, which is a claim about the six that are not
  // there.
  const scope = graph.matched ? " in the filtered starmap" : "";
  if (!found.length) {
    // Not "everything is fine": most nodes in a young landscape are unobserved, and
    // an empty findings list over an unwatched instance would be a claim nobody made.
    return `<div class="mesh-findings">
      <div class="mesh-findings-head"><b>Findings${esc(scope)}</b></div>
      <p class="mesh-note">Nothing here is reporting a problem. What is not watched is
      listed in the legend — an empty list is not the same as everything being
      well.</p></div>`;
  }
  return `<div class="mesh-findings">
    <div class="mesh-findings-head">
      <b>Findings${esc(scope)}</b>
      <span class="muted">${found.length} node(s)${incidents ? `, ${incidents} incident(s)` : ""}</span>
    </div>
    <ul class="mesh-findings-list">${found.map((n) => `<li>
      <button type="button" class="mesh-finding-go mesh-sev-${esc(n.severity)}"
        data-finding="${esc(n.id)}">
        <span class="mesh-finding-name">${esc(n.name || n.id)}</span>
        <span class="mesh-finding-state">${esc(STATE_TEXT[n.state] || n.state || "")}${
          n.incidents ? ` · ${n.incidents} incident(s)` : ""}</span>
        ${n.reason ? `<span class="mesh-finding-why">${esc(n.reason)}</span>` : ""}
      </button>${sitesHTML(n)}</li>`).join("")}</ul>
  </div>`;
}

// sinceText is how long ago something happened, in the coarsest unit that still
// says it. A landscape is read at a glance and the question behind this number is
// "did anything move lately", not "when exactly" — so minutes are rounded and
// anything past a week stops pretending to be precise.
function sinceText(nanos, now = Date.now()) {
  if (!Number.isFinite(nanos) || nanos <= 0) return "";
  const seconds = Math.max(0, Math.round((now - nanos / 1e6) / 1000));
  if (seconds < 90) return "just now";
  const minutes = Math.round(seconds / 60);
  if (minutes < 90) return `${minutes} min ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 36) return `${hours} h ago`;
  return `${Math.round(hours / 24)} d ago`;
}

// runtimeHTML is what the engine has recorded about a process: how much is live,
// how much has been, and whether anything has happened lately.
//
// It is stated for whichever node is selected whether or not the canvas is showing
// counts, and the zero is stated: "nothing is running here" is an answer, and it is
// the one the canvas deliberately does not have room to give four hundred times.
//
// Finished is a lifetime total rather than a rate, and says so — a number that only
// ever grows is misread as throughput otherwise.
function runtimeHTML(node) {
  const rt = node?.runtime;
  if (!rt) return "";
  const last = sinceText(rt.lastActivity);
  // "Never started" is claimed only where nothing has run *and* nothing has
  // finished. A definition with a lifetime total and no timestamp is one this build
  // has no activity clock for, and saying it was never started beside seven finished
  // instances would be a contradiction the reader has to resolve. Then it says
  // nothing, which is what it knows.
  const never = !last && rt.running === 0 && rt.finished === 0;
  return `<div class="mesh-runtime">
    <span class="mesh-runtime-now"><b>${rt.running}</b> running</span>
    <span class="muted"><b>${rt.finished}</b> finished, all time</span>
    ${last ? `<span class="muted">last activity ${esc(last)}</span>` : ""}
    ${never ? `<span class="muted">never started</span>` : ""}
  </div>`;
}

// sitesHTML lists where in a process the parked work actually is.
//
// "Three tokens are parked" says there is a problem; "three on the service task
// charge-card, and the last one said 502 Bad Gateway" says where to go. The element
// is named by its BPMN id and type rather than by a label, because only user tasks
// carry a title in a compiled process — an identifier that is sometimes there is
// worse than one that is always there — and because that is what Operations shows,
// so the two name the same thing the same way.
function sitesHTML(node) {
  if (!Array.isArray(node?.sites) || !node.sites.length) return "";
  return `<ul class="mesh-sites">${node.sites.map((site) => `<li>
    <span class="mesh-site-where">
      ${site.elementType ? `<span class="muted">${esc(site.elementType)}</span> ` : ""}
      <code>${esc(site.elementId)}</code>
      ${site.count > 1 ? `<span class="mesh-site-count">${site.count}×</span>` : ""}
    </span>
    ${site.message ? `<span class="mesh-site-why">${esc(site.message)}</span>` : ""}
  </li>`).join("")}</ul>`;
}

// SEVERITY_ORDER ranks the classes for the findings list. It is the same order the
// server aggregates by (ADR-0211 §4) — unknown below ok, because a node nothing
// observes is unobserved rather than well — and it is here only to sort a list the
// server does not sort.
const SEVERITY_ORDER = { critical: 3, attention: 2, ok: 1, unknown: 0 };

// shortLabel is a node in one word. A restricted placeholder has no name to give —
// that is the point of it — so it falls back to what its kind is called.
const shortLabel = (n) => n.name ||
  String((KIND[n.kind] || {}).label || n.kind || n.id).split(" — ")[0];

// The three pieces an impact answer is made of, written once because two panels now
// render them: one node's radius, and a maintenance window's. Two copies would have
// drifted the first time one of them was worded better.

// impactMixHTML is how bad the radius already is, and how much of it is one edge
// away. The mix is read for triage and never as cause: these nodes report their own
// state, and a panel implying the selection produced it would be wrong the first
// time somebody checked — so `cause` names what is *not* being claimed.
function impactMixHTML(summary, cause) {
  if (!summary || !summary.total) return "";
  const classes = ["critical", "attention", "ok", "unknown"].filter((k) => summary.bySeverity[k]);
  return `
    <div class="mesh-impact-mix">${classes.map((k) => `
      <span class="mesh-impact-chip mesh-sev-${k}"><b>${summary.bySeverity[k]}</b>
        ${esc(SEVERITY[k].label.split(" — ")[0].toLowerCase())}</span>`).join("")}
    </div>
    <p class="mesh-note"><b>${summary.direct}</b> directly, <b>${summary.indirect}</b>
      further out. What those nodes report is their own state — this says what the
      blast radius currently looks like, not what ${esc(cause)} caused.</p>`;
}

// impactListHTML names the nodes the count is counting, so the answer can be acted
// on rather than only repeated.
function impactListHTML(graph, result, startId, summary) {
  const named = graph && result ? impactList(graph, result, startId) : [];
  if (!named.length) return "";
  return `
    <ul class="mesh-impact-list">${named.map((n) => `<li>
      <button type="button" class="mesh-impact-go mesh-sev-${esc(n.severity || "unknown")}"
        data-finding="${esc(n.id)}">
        <span class="mesh-impact-who">${esc(shortLabel(n))}</span>
        <span class="mesh-impact-hop">${n.direct ? "directly" : "further out"}${
          n.state ? ` · ${esc(STATE_TEXT[n.state] || n.state)}` : ""}</span>
      </button></li>`).join("")}</ul>
    ${summary && summary.total > named.length
      ? `<p class="mesh-note">${summary.total - named.length} more, worst first above.</p>`
      : ""}`;
}

// An answer that stopped at a permission boundary must not read as a complete one.
// This is the rule the mesh applies to the picture, applied to the analysis over it:
// the count is a floor, not a total.
function truncationHTML(result) {
  if (!result || result.complete) return "";
  return `<p class="mesh-note mesh-truncated"><b>Incomplete.</b> The walk stopped at
    ${result.truncatedBy.length} node(s) outside your access, so there may be more
    beyond them. Treat the count as a lower bound.</p>`;
}

// impactPanelHTML states the answer in words beside the picture. The counts are the
// point — a highlighted subgraph tells you *which*, a count tells you *how many*,
// and "17 things depend on this worker" is the sentence somebody repeats in a
// change-approval meeting.
function impactPanelHTML(node, result, direction, depth,
  { pinned = false, graph = null, notation = null } = {}) {
  if (!node) {
    return `<div class="mesh-panel mesh-panel-empty">
      <b>Nothing selected</b>
      <p>Select a node to see what depends on it, and what it depends on.</p>
      <p>Hold ${modifierName()} while clicking to plan a maintenance window over
        several nodes at once.</p></div>`;
  }
  const typed = typeIn(node.kind, notation);
  const kindLabel = typed
    ? `${typed.name} · ${((KIND[node.kind] || {}).label || node.kind).split(" — ")[0]}`
    : (KIND[node.kind] || {}).label || node.kind;
  const others = result ? result.nodes.length - 1 : 0;
  const word = direction === "dependents" ? "depend on this" : "are needed by this";
  const drill = node.kind === "process"
    ? `<a class="mesh-drill" href="${hrefFor(node)}">Open in Operations →</a>`
    : "";
  // Releasing one hand-placed node lives here, beside the node it is about. It used
  // to be a double-click, which is a thing you have to be told; a button on the
  // thing itself is a thing you can see.
  const release = pinned
    ? `<button type="button" class="mesh-unpin" data-unpin="${esc(node.id)}">
        Release this node</button>`
    : "";
  // An answer that stopped at a permission boundary must not read as a complete
  // one. This is the same rule the mesh applies to the picture, applied to the
  // analysis over it: the count below is a floor, not a total.
  // How bad, and how close — the two things a count alone leaves out. The mix is
  // read for triage and never as cause: these nodes report their own state, and a
  // panel that implied this one produced it would be wrong the first time somebody
  // checked.
  const summary = graph && result ? impactSummary(graph, result, node.id) : null;
  const mix = impactMixHTML(summary, "this node");
  const list = impactListHTML(graph, result, node.id, summary);
  const truncation = truncationHTML(result);
  // The finding, in words, above the impact count. A node's colour says which class
  // it is in; only the state and the reason say what to do about it, and where the
  // severity was inherited the panel names the descendant it came from — a red
  // parent that cannot say which child is red is not actionable (ADR-0211 §4).
  const sev = SEVERITY[node.severity] || SEVERITY.unknown;
  const inherited = node.severityFrom
    ? `<span class="muted"> — inherited from ${esc(node.severityFrom)}</span>`
    : "";
  const finding = `<div class="mesh-finding mesh-sev-${esc(node.severity || "unknown")}">
      <b>${esc(sev.label.split(" — ")[0])}</b>
      <span class="muted">${esc(STATE_TEXT[node.state] || node.state || "unbound")}</span>
      ${node.reason ? `<p>${esc(node.reason)}${inherited}</p>` : ""}
      ${sitesHTML(node)}
    </div>`;
  return `<div class="mesh-panel">
    <div class="mesh-panel-head">
      <b>${esc(node.name || kindLabel)}</b>
      <span class="muted">${esc(kindLabel)}</span>
    </div>
    ${finding}
    ${runtimeHTML(node)}
    <div class="mesh-impact-count"><b>${others}</b> node(s) ${word}
      <span class="muted">within ${depth === Infinity ? "any" : depth} hop(s)</span></div>
    ${mix}
    ${truncation}
    ${list}
    ${drill}
    ${release}
    <p class="mesh-note">Hold ${modifierName()} and click another node to plan a
      maintenance window over both — their blast radii overlap, and the window says by
      how much.</p>
  </div>`;
}

// modifierName is the key this platform expects for "add to the selection". Naming
// the wrong one is worse than naming none: a reader who tries Ctrl on a Mac and gets
// a context menu concludes the feature is broken rather than that the hint was.
function modifierName() {
  const platform = globalThis.navigator?.platform || "";
  const agent = globalThis.navigator?.userAgent || "";
  return /Mac|iPhone|iPad/.test(`${platform} ${agent}`) ? "⌘" : "Ctrl";
}

// windowPanelHTML answers the question a maintenance window actually asks: not what
// each of these breaks, but what the evening costs.
//
// The two numbers are on screen together on purpose. "One at a time these come to
// 28; together 19" is the whole finding — it is what tells somebody that batching
// three changes into one window is nearly free, or that it is not — and either
// number alone invites the other to be guessed, always by adding. See windowOverlap
// for why the union is the thing computed and the individual totals the thing
// derived.
function windowPanelHTML(nodes, result, direction, depth, { graph = null, overlap = null } = {}) {
  const summary = graph && result ? impactSummary(graph, result, null) : null;
  const others = summary ? summary.total : 0;
  const word = direction === "dependents" ? "depend on these" : "are needed by these";
  const hops = depth === Infinity ? "any" : depth;
  const covered = new Set(overlap?.covered || []);
  const each = new Map((overlap?.each || []).map((row) => [row.id, row]));

  // What is in the window, each with what it costs on its own — because the reader
  // assembled this set one node at a time and the next question is which of them is
  // carrying the cost. The remove button is on the chip rather than in a menu: a set
  // somebody builds by clicking has to be unbuildable the same way.
  const members = nodes.map((n) => {
    const row = each.get(n.id);
    const notes = [];
    if (row) notes.push(`${row.total} on its own`);
    // A node another member already takes down. Worth saying plainly: it makes no
    // difference to the window, and somebody is about to write it into a change
    // request as though it did.
    if (covered.has(n.id)) notes.push("already down with the others");
    // Two lines in one button, the shape the findings and the impact list already
    // use: the name is what a reader scans for and must never be the part that gets
    // squeezed out when the sub-line is long.
    return `<li class="mesh-window-item">
      <button type="button" class="mesh-window-who mesh-sev-${esc(n.severity || "unknown")}"
        data-window-go="${esc(n.id)}" title="Find ${esc(shortLabel(n))} on the canvas">
        <span class="mesh-window-name">${esc(shortLabel(n))}</span>
        <span class="mesh-window-cost">${esc(notes.join(" · "))}</span>
      </button>
      <button type="button" class="mesh-window-drop" data-window-drop="${esc(n.id)}"
        aria-label="Take ${esc(shortLabel(n))} out of this window">×</button>
    </li>`;
  }).join("");

  // The comparison, stated as two totals and the reason they differ. `shared` is a
  // count of nodes rather than the arithmetic difference, which past two nodes is
  // not the size of anything (see windowOverlap).
  const compare = overlap && overlap.starts.length > 1 ? `
    <p class="mesh-note">One at a time these come to <b>${overlap.sum}</b>;
      together <b>${overlap.total}</b>.
      ${overlap.shared
        ? `<b>${overlap.shared}</b> node(s) sit in more than one radius, which is
           where the difference is.`
        : `Nothing is in two of these radii, so the window costs each of them in
           full.`}</p>` : "";

  return `<div class="mesh-panel">
    <div class="mesh-panel-head">
      <b>Maintenance window</b>
      <span class="muted">${nodes.length} node(s) going down together</span>
    </div>
    <ul class="mesh-window-list">${members}</ul>
    <div class="mesh-impact-count"><b>${others}</b> node(s) ${word}
      <span class="muted">within ${hops} hop(s)</span></div>
    ${impactMixHTML(summary, "this window")}
    ${compare}
    ${truncationHTML(result)}
    ${impactListHTML(graph, result, null, summary)}
    <p class="mesh-note">Hold ${modifierName()} and click to add or remove a node.</p>
    <button type="button" class="mesh-window-close" data-window-clear="1">Close this window</button>
  </div>`;
}

export async function mountPanoramaMesh(view, { api, toast }) {
  view.innerHTML = `<div class="card"><h1>Starmap</h1><p class="muted">Deriving…</p></div>`;
  let graph;
  const fetched = performance.now();
  try {
    // The mapping is additive: a landscape is worth drawing even when the notations
    // cannot be read, and without them the picker offers only the derived vocabulary
    // — which is the one that cannot be wrong about what it is.
    const [mesh, served] = await Promise.all([
      api("GET", "/api/v1/panorama/mesh"),
      api("GET", "/api/v1/panorama/notations").catch(() => []),
    ]);
    graph = mesh;
    useNotations(served);
  } catch (e) {
    view.innerHTML = `<div class="card empty"><h1>Starmap</h1>
      <p>${esc(e.message)}</p></div>`;
    return;
  }
  const fetchMs = performance.now() - fetched;

  if (!graph.nodes.length) {
    view.innerHTML = `<div class="card empty"><h1>Starmap</h1>
      <p>Nothing is deployed on this server yet. The landscape is derived from what
      Atlas holds, so it fills in as you deploy — there is nothing to model first.</p></div>`;
    return;
  }

  view.innerHTML = `<div id="mesh-root" class="card mesh-card">
    <div class="mesh-head">
      <h1>Starmap</h1>
      <input id="mesh-search" type="search" class="mesh-search" autocomplete="off"
        placeholder="Filter by name, kind or process id…" aria-label="Filter the starmap"/>
      <!-- Going *into* the selected node, as a control rather than only as a
           double-click. A gesture you have to be told about is one most readers never
           find, and the drilldown is the thing this view is for once the landscape is
           bigger than a screenful. -->
      <button id="mesh-drill-in" type="button" class="mesh-drill-in" disabled
        aria-label="Zoom into the selected node"
        title="Zoom into the selected node and what it touches">→</button>
      <button id="mesh-drill-out" type="button" class="mesh-drill-chip" hidden></button>
      <!-- Which vocabulary the picture is drawn in. Beside the picture rather than in
           the side column, because it changes the drawing rather than the answer
           about it (ADR-0211 §8). -->
      <label class="mesh-notation" for="mesh-notation">Notation</label>
      <select id="mesh-notation" class="mesh-notation-pick">${notationsAvailable()
        .map((n) => `<option value="${esc(n.id)}">${esc(n.label)}</option>`).join("")}</select>
      <!-- How much is running, on the picture rather than only in the panel. Off by
           default and asked for by name: it is a second number on every node, and a
           structural picture that always carried it would be a status board that
           happens to have arrows. -->
      <label class="mesh-instances" title="Show how many instances are running, on the processes that have any">
        <input id="mesh-instances" type="checkbox"/> Instances
      </label>
      <!-- Beside the picture's own controls rather than in the side column: what is
           exported is the picture, including whatever the search box and the
           drilldown have done to it. -->
      <span class="mesh-export" role="group" aria-label="Export this starmap">
        <button id="mesh-export-svg" type="button"
          title="Save this starmap as an SVG, stamped with when and where it was observed">SVG</button>
        <button id="mesh-export-png" type="button"
          title="Save this starmap as a PNG, stamped with when and where it was observed">PNG</button>
        <!-- Generated by the server from the same landscape, so it is the whole of it
             rather than whatever the search box has narrowed this picture to — and it
             carries structure only, never health. A plain navigation, so the session
             cookie authenticates it, exactly as the application source download does. -->
        <button id="mesh-export-archimate" type="button"
          title="Download the whole starmap as an ArchiMate Open Exchange model — structure only, generated, not drawn">ArchiMate XML</button>
      </span>
    </div>
    <!-- How much of the landscape is on screen, on its own line under the controls.
         It was in the head, where it was the one thing with no fixed size: every
         control kept its width and the sentence was squeezed into a column one word
         wide. It describes the picture rather than acting on it, so it reads better
         under the row that does. -->
    <div class="mesh-subhead">
      <span id="mesh-count" class="muted"></span>
    </div>
    <div class="mesh-body">
      <div class="mesh-plot">
        <!-- The stage is the canvas and the controls that float over it, and nothing
             else: the surface's contents are replaced on every repaint, so anything
             inside it is drawn once and then gone. -->
        <div class="mesh-stage">
          <div id="mesh-surface" class="mesh-surface"></div>
          <div class="mesh-zoom" role="group" aria-label="Zoom">
            <button id="mesh-zoom-in" type="button" title="Zoom in">+</button>
            <button id="mesh-zoom-out" type="button" title="Zoom out">−</button>
            <button id="mesh-zoom-fit" type="button" title="Fit the whole starmap">Fit</button>
            <button id="mesh-release" type="button" disabled
              title="Put every node you have dragged back where the layout puts it">Release</button>
          </div>
        </div>
        <!-- The key sits under the picture: it is a reference, consulted while looking
             at the canvas rather than read on the way to it, and above the picture it
             pushed the thing it explains down the page. Under the canvas and not under
             the whole body, so it stays against the picture rather than below
             whichever of the two columns happens to be taller. -->
        <div id="mesh-legend-slot"></div>
      </div>
      <aside class="mesh-side">
        <div class="mesh-controls">
          <!-- Explicit for/id rather than a wrapping label: a select nested inside
               its label takes the option text into its accessible name, which makes
               the control hard to address by name for a screen reader and for a test
               alike. -->
          <label for="mesh-direction">Show</label>
          <select id="mesh-direction">
            <option value="dependents">what depends on it</option>
            <option value="dependencies">what it depends on</option>
            <option value="both">both directions</option>
          </select>
          <label for="mesh-depth">Depth</label>
          <select id="mesh-depth">
            <option value="1">1 hop</option>
            <option value="2" selected>2 hops</option>
            <option value="all">all</option>
          </select>
        </div>
        <div id="mesh-panel-slot"></div>
        <div id="mesh-findings-slot"></div>
        <div id="mesh-ranking-slot"></div>
        <div class="mesh-views">
          <div class="mesh-views-head">
            <b>Saved views</b>
            <!-- Said plainly rather than discovered later: these live in this
                 browser, and are not shared and do not follow you to another one. -->
            <span class="muted">this browser only</span>
          </div>
          <ul id="mesh-view-list" class="mesh-view-list"></ul>
          <form id="mesh-view-save" class="mesh-view-save">
            <input id="mesh-view-name" type="text" maxlength="60" autocomplete="off"
              placeholder="Name this view…" aria-label="Name for the saved view"/>
            <button type="submit">Save</button>
          </form>
          <p id="mesh-view-note" class="mesh-note" hidden></p>
        </div>
      </aside>
    </div>
  </div>`;

  const search = document.getElementById("mesh-search");
  const drillOut = document.getElementById("mesh-drill-out");
  const drillIn = document.getElementById("mesh-drill-in");
  const surface = document.getElementById("mesh-surface");
  const zoomIn = document.getElementById("mesh-zoom-in");
  const zoomOut = document.getElementById("mesh-zoom-out");
  const zoomFit = document.getElementById("mesh-zoom-fit");
  const release = document.getElementById("mesh-release");
  const instancesToggle = document.getElementById("mesh-instances");
  const legendSlot = document.getElementById("mesh-legend-slot");
  const count = document.getElementById("mesh-count");
  const panel = document.getElementById("mesh-panel-slot");
  const dirSelect = document.getElementById("mesh-direction");
  const depthSelect = document.getElementById("mesh-depth");
  const findingsSlot = document.getElementById("mesh-findings-slot");
  const rankingSlot = document.getElementById("mesh-ranking-slot");
  const viewList = document.getElementById("mesh-view-list");
  const viewForm = document.getElementById("mesh-view-save");
  const viewName = document.getElementById("mesh-view-name");
  const viewNote = document.getElementById("mesh-view-note");
  const notationPick = document.getElementById("mesh-notation");
  const exportSvgBtn = document.getElementById("mesh-export-svg");
  const exportModelBtn = document.getElementById("mesh-export-archimate");
  const exportPngBtn = document.getElementById("mesh-export-png");

  // The selection is a list rather than one id, because a maintenance window is a
  // question about several nodes at once and the union of their blast radii is not
  // the sum (see windowOverlap). One entry is the ordinary case and reads exactly as
  // it did; the panel changes shape at two.
  let picked = [];
  const only = () => (picked.length === 1 ? picked[0] : null);
  // drilled is the node the landscape has been reduced to, or null for all of it.
  //
  // A drilldown is a *place to stand*, not a filter over names: it is the one node
  // somebody double-clicked and whatever is within the depth already on screen. It
  // and the search box are two ways of asking the same kind of question, so only one
  // of them is ever in force — entering one clears the other, because two narrowings
  // compounding invisibly is how a picture ends up showing something nobody asked
  // for and nobody can undo.
  let drilled = null;
  // pinned holds every node somebody has dragged, by id, at the world coordinates
  // they dropped it on. It is the whole of the arrangement: the layout reads it on
  // every paint, so a hand-placed node survives filtering, selecting and resizing —
  // and clearing this map is all that "Release" has to do.
  //
  // It deliberately keeps entries for nodes a filter has hidden. A search is a
  // temporary question, and losing your arrangement by asking one would make the
  // arrangement not worth making.
  const pinned = new Map();
  // placed is the laid-out graph — the same node objects the SVG was rendered from,
  // with live coordinates. A drag mutates these and writes the result straight into
  // the DOM, so the picture follows the pointer without a re-render.
  let placed = [];
  let at = new Map();
  let shown = graph;
  let nodeEls = new Map();
  let edgeEls = [];
  // frame is the drawing surface in its own units, taken from the element rather
  // than assumed, so the layout settles into the shape the viewer actually has.
  // frameView is the part of it currently on screen; null means fitted, which is where
  // every paint starts — the opening picture is the whole landscape.
  let frame = { width: 1200, height: 720 };
  let frameView = null;
  // world is the box the graph was actually laid out in, and the base view is that
  // box rather than the frame: the frame is a window, not the canvas.
  let world = { width: 1200, height: 720 };

  function measure() {
    const width = Math.max(surface.clientWidth || 0, 320);
    const height = Math.max(surface.clientHeight || 0, 280);
    frame = { width, height };
  }

  function applyView() {
    const svg = surface.querySelector("svg");
    if (!svg) return;
    const v = frameView || baseView();
    svg.setAttribute("viewBox", `${v.x.toFixed(2)} ${v.y.toFixed(2)} ${v.w.toFixed(2)} ${v.h.toFixed(2)}`);
    svg.classList.toggle("mesh-zoomed", frameView !== null);
    // Screen pixels per world unit, which is what decides whether a name can be
    // read. Toggling a class is the whole of it: names appear and disappear as the
    // view moves, with nothing re-rendered and no layout recomputed.
    const tier = labelTier(frame.width / Math.max(v.w, 1));
    svg.classList.toggle("mesh-names-all", tier === "all");
    svg.classList.toggle("mesh-names-anchors", tier === "anchors");
  }

  // chromeReserve is how much of the canvas the zoom panel floats over, measured
  // rather than assumed: it holds a different number of buttons depending on what
  // the picture can do, and a hard-coded box would be wrong the next time one is
  // added. The margin is the panel's own inset plus a little air.
  function chromeReserve() {
    const panel = surface.parentElement?.querySelector(".mesh-zoom");
    const box = panel?.getBoundingClientRect();
    if (!box?.width) return { width: 0, height: 0 };
    return { width: box.width + 22, height: box.height + 22 };
  }

  // fitted is what "Fit" means: the drawn nodes, framed in the space they can
  // actually be read and reached in. Not the world — see contentBox for why the two
  // stop being the same picture the moment anything is pinned.
  //
  // It is *stored* rather than derived on demand, and that is not an optimisation.
  // Every screen-to-world conversion goes through it, and a drag moves the content
  // it is computed from — so a view recomputed per call would shift the coordinate
  // system under the pointer as the node crossed it, and the node would drift away
  // from the cursor by however much it had already moved the picture's own bounds.
  // The frame a gesture started in is the frame it finishes in; reframing is
  // something the reader asks for.
  let fitted = { x: 0, y: 0, w: 1200, h: 720 };
  // The room the current notation's labels need, handed back by the render so the
  // framing reserves exactly what the layout did. A projection's type annotation is
  // part of the picture, and a frame that cut it off would be a frame that disagreed
  // with the drawing it is showing.
  let labelMargin = LABEL_MARGIN;
  function refit() {
    fitted = placed.length
      ? fitView(contentBox(placed, labelMargin), frame, chromeReserve())
      : { x: 0, y: 0, w: world.width, h: world.height };
    return fitted;
  }
  function baseView() {
    return fitted;
  }

  // zoom keeps whatever is under `focus` under it, so the wheel behaves like a map.
  // focus is in the frame's units; omitting it zooms about the centre, which is what
  // the buttons want.
  function zoom(factor, focus) {
    const base = baseView();
    const current = frameView || base;
    const about = focus || { x: current.x + current.w / 2, y: current.y + current.h / 2 };
    frameView = zoomView(current, factor, about, base);
    applyView();
  }

  function paint() {
    const term = search.value.trim().toLowerCase();
    const hops = depthSelect.value === "all" ? Infinity : Number(depthSelect.value);
    // A drilldown onto a node that is no longer in the landscape is not an empty
    // landscape — it is a question that can no longer be asked, and saying so beats
    // drawing a blank canvas somebody would read as "everything is gone".
    const drilledGraph = drilled ? drillInto(graph, drilled, hops) : null;
    if (drilled && !drilledGraph) {
      drilled = null;
      toast("That node is no longer in this starmap.");
    }
    shown = drilledGraph || filterGraph(graph, term);
    paintDrillChip();
    // A selection that the filter removed is no longer selected: highlighting a node
    // that is not on screen would leave the panel describing something invisible.
    picked = picked.filter((id) => shown.nodes.some((n) => n.id === id));

    measure();
    // Where everything currently is, so a repaint while something is pinned carries
    // the picture on screen forward instead of settling a fresh one around the pins.
    const from = new Map(placed.map((n) => [n.id, { x: n.x, y: n.y }]));
    const spoken = notationOf(notationPick.value);
    const painted = renderGraph(shown, 0, frame, {
      pinned, from, notation: spoken, instances: instancesToggle.checked,
    });
    const { ms, svg } = painted;
    world = painted.world;
    placed = painted.nodes;
    labelMargin = painted.margin;
    at = new Map(placed.map((n) => [n.id, n]));
    surface.innerHTML = shown.nodes.length
      ? svg
      : `<p class="mesh-empty-filter">Nothing matches “${esc(term)}”.</p>`;
    index();
    // The rendered SVG carries none of the hover highlight, so the record of what is
    // lit has to be cleared with it — otherwise pointing back at the same node would
    // be a no-op and the highlight would never come back.
    lit = null;
    refit();
    applyView();
    legendSlot.innerHTML = legendHTML(shown, ms, spoken, instancesToggle.checked);
    findingsSlot.innerHTML = findingsHTML(shown);
    paintRanking();
    // Matches and context counted apart. "5 of 101" over a picture where only one
    // node matched the term would be the header agreeing with the drawing and both
    // of them misreporting the search.
    const context = shown.nodes.length - (shown.matched?.size ?? shown.nodes.length);
    if (drilled) {
      count.textContent = `${context} of ${graph.nodes.length} node(s) within ` +
        `${depthSelect.value === "all" ? "any" : depthSelect.value} hop(s)`;
    } else {
      count.textContent = term
        ? `${shown.matched?.size ?? 0} of ${graph.nodes.length} node(s) match` +
          (context ? `, ${context} shown for context` : "")
        : `${graph.nodes.length} node(s), ${graph.edges.length} edge(s)`;
    }
    refresh();
  }

  // paintRanking is kept out of refresh deliberately. The ranking is about the
  // graph and the two controls, not about the selection — recomputing a walk from
  // every node each time somebody clicks a circle would spend the whole budget on an
  // answer that had not changed.
  function paintRanking() {
    rankingSlot.innerHTML = rankingHTML(
      shown, dirSelect.value,
      depthSelect.value === "all" ? Infinity : Number(depthSelect.value));
  }

  // refresh answers the impact question about the current selection and shows the
  // answer — the highlight on the picture, the counts beside it.
  //
  // It never re-lays-out, and that is the point. Selecting is the most frequent thing
  // anybody does here, and running a two-hundred-iteration simulation to answer "what
  // depends on this" was both slow at a few hundred nodes and wrong in kind: the
  // answer is about the picture on screen, so the picture must not move while it is
  // being given. The classes go on and come off exactly as the hover highlight's do.
  function refresh() {
    const direction = dirSelect.value;
    const depth = depthSelect.value === "all" ? Infinity : Number(depthSelect.value);
    const result = picked.length ? impactOf(shown, picked, { direction, depth }) : null;
    const highlight = result ? new Set(result.nodes) : null;
    const inWindow = new Set(picked);
    for (const [id, g] of nodeEls) {
      g.classList.toggle("mesh-in-impact", Boolean(highlight?.has(id)));
      g.classList.toggle("mesh-dimmed", Boolean(highlight) && !highlight.has(id));
      // Which of the lit nodes are the ones going down, as opposed to the ones that
      // go with them. Without it a window of three reads as one undifferentiated
      // blob and the reader cannot check the set they assembled against the picture.
      g.classList.toggle("mesh-picked", inWindow.has(id));
    }
    for (const line of edgeEls) {
      const inside = Boolean(highlight?.has(line.dataset.from) && highlight.has(line.dataset.to));
      line.classList.toggle("mesh-in-impact", inside);
      line.classList.toggle("mesh-dimmed", Boolean(highlight) && !inside);
    }
    // Nothing selected is nothing to go into, and being already inside a node is not
    // a place you can go into again.
    // Going into a node needs one node. Several is a window rather than a place.
    drillIn.disabled = !only() || drilled === only();
    if (picked.length > 1) {
      const nodes = picked.map((id) => shown.nodes.find((n) => n.id === id)).filter(Boolean);
      panel.innerHTML = windowPanelHTML(nodes, result, direction, depth, {
        graph: shown, overlap: windowOverlap(shown, picked, { direction, depth }),
      });
      return;
    }
    panel.innerHTML = impactPanelHTML(
      shown.nodes.find((n) => n.id === only()) || null, result, direction, depth,
      { pinned: pinned.has(only()), graph: shown, notation: notationOf(notationPick.value) });
  }

  // What the panel's own buttons do. Releasing a hand-placed node lives beside the
  // node it is about; the window's do too, because a set assembled by clicking has to
  // come apart without a modifier anybody has to be told about.
  panel.addEventListener("click", (event) => {
    const target = event.target;
    const drop = target.closest?.("[data-window-drop]")?.getAttribute("data-window-drop");
    if (drop) {
      picked = picked.filter((id) => id !== drop);
      refresh();
      return;
    }
    if (target.closest?.("[data-window-clear]")) {
      picked = [];
      refresh();
      return;
    }
    const go = target.closest?.("[data-window-go]")?.getAttribute("data-window-go");
    if (go) { frameOn(go); return; }
    const id = target.closest?.("[data-unpin]")?.getAttribute("data-unpin");
    if (!id || !pinned.has(id)) return;
    pinned.delete(id);
    const node = at.get(id);
    if (node) node.held = false;
    updateRelease();
    paint();
  });

  // index caches the elements a drag writes to. Looking them up once per render and
  // then writing coordinates straight onto them is what keeps a drag at frame rate:
  // the alternative is re-rendering the whole SVG on every pointer move, which at a
  // few hundred nodes is a slideshow.
  function index() {
    const svg = surface.querySelector("svg");
    nodeEls = new Map();
    edgeEls = [];
    if (!svg) return;
    for (const g of svg.querySelectorAll(".mesh-node")) nodeEls.set(g.dataset.nodeId, g);
    edgeEls = [...svg.querySelectorAll(".mesh-edge")];
  }

  // applyPositions writes the live coordinates into the SVG that is already there.
  function applyPositions() {
    for (const [id, g] of nodeEls) {
      const n = at.get(id);
      if (n) g.setAttribute("transform", `translate(${n.x.toFixed(1)},${n.y.toFixed(1)})`);
    }
    for (const line of edgeEls) {
      const a = at.get(line.dataset.from), b = at.get(line.dataset.to);
      if (!a || !b) continue;
      line.setAttribute("x1", a.x.toFixed(1)); line.setAttribute("y1", a.y.toFixed(1));
      line.setAttribute("x2", b.x.toFixed(1)); line.setAttribute("y2", b.y.toFixed(1));
    }
  }

  // A plain click asks about one node; the same click with the platform's own
  // add-to-selection key builds a maintenance window out of several. Toggling either
  // way, because a set somebody assembles by clicking has to come apart the same way
  // — and clicking the lone selection again still clears it, which is how this
  // behaved before there was more than one.
  function select(id, { add = false } = {}) {
    if (!id) picked = [];
    else if (add) picked = picked.includes(id) ? picked.filter((x) => x !== id) : [...picked, id];
    else picked = picked.length === 1 && picked[0] === id ? [] : [id];
    refresh();
  }

  // Which bubble is connected to which, shown by pointing at one.
  //
  // Impact analysis already answers this properly — it walks the dependency edges
  // to whatever depth is asked for, and states the answer in words beside the
  // picture. But it needs a click, and it answers a bigger question than the one
  // somebody has while reading: *what is this touching?* That question is asked
  // dozens of times while scanning a landscape and deserves to cost nothing.
  //
  // So hovering a node lifts its immediate neighbours and the edges to them, and
  // lets everything else fall back. It is one hop deliberately: the transitive
  // answer is what selecting is for, and a hover that lit up half the landscape
  // would be a worse version of it rather than a different tool. Nothing is
  // re-laid-out and nothing is re-rendered — the classes go on and come off, so the
  // picture cannot move under the pointer while it is being read.
  let lit = null;
  function relate(id) {
    if (lit === id) return;
    lit = id;
    const svg = surface.querySelector("svg");
    if (!svg) return;
    svg.classList.toggle("mesh-relating", id !== null);
    const neighbours = new Set();
    for (const line of svg.querySelectorAll(".mesh-edge")) {
      const from = line.dataset.from, to = line.dataset.to;
      const touches = id !== null && (from === id || to === id);
      line.classList.toggle("mesh-related-edge", touches);
      if (touches) neighbours.add(from === id ? to : from);
    }
    for (const node of svg.querySelectorAll(".mesh-node")) {
      const nodeId = node.dataset.nodeId;
      node.classList.toggle("mesh-related", neighbours.has(nodeId));
      node.classList.toggle("mesh-relating-self", id !== null && nodeId === id);
    }
  }
  surface.addEventListener("pointerover", (event) => {
    // While a node is being dragged the highlight belongs to the node in hand, not
    // to whatever the pointer happens to sweep over on the way.
    if (moving) return;
    const node = event.target.closest?.(".mesh-node");
    relate(node ? node.dataset.nodeId : null);
  });
  surface.addEventListener("pointerleave", () => relate(null));
  // Keyboard reaches the same answer: the relationships are part of what the view
  // says, not a reward for owning a mouse.
  surface.addEventListener("focusin", (event) => {
    if (moving) return;
    const node = event.target.closest?.(".mesh-node");
    if (node) relate(node.dataset.nodeId);
  });
  surface.addEventListener("focusout", (event) => {
    if (!surface.contains(event.relatedTarget)) relate(null);
  });
  // pointToFrame maps a browser point onto the frame's own units, through whatever
  // the current viewBox is. Without it a wheel zoom would drift: the pointer is in
  // CSS pixels and the frame is not.
  function pointToFrame(event) {
    const svg = surface.querySelector("svg");
    if (!svg) return null;
    const rect = svg.getBoundingClientRect();
    if (!rect.width || !rect.height) return null;
    const v = frameView || baseView();
    return {
      x: v.x + ((event.clientX - rect.left) / rect.width) * v.w,
      y: v.y + ((event.clientY - rect.top) / rect.height) * v.h,
    };
  }

  // Picking a bubble up and putting it somewhere.
  //
  // The layout answers "where does this graph want to sit", which is the right
  // first answer and never the last one: the person reading it knows things the
  // simulation does not — that these four belong together, that this hub should be
  // out of the way — and until now had no way to say so. Dragging is how they say
  // it, and the graph rearranging itself around the node in hand is what makes the
  // answer legible: neighbours follow, everything else gets out of the way, and
  // what was connected to what is visible in the motion rather than only in the
  // lines.
  //
  // A dropped node stays dropped. The simulation here settles once rather than
  // running continuously, so releasing a node back into it would put it straight
  // back where it started and make the whole gesture pointless. It is pinned
  // instead — marked on the node, undone per node by double-clicking it and
  // wholesale by "Release", so it is never a state somebody is stuck in.
  let moving = null;

  // place puts a node where the hand put it, anywhere.
  //
  // It used to clamp into the world, on the argument that the fitted view shows the
  // world and a node outside it would be invisible at the view somebody would use to
  // find it. That argument stopped being true when Fit started framing the *content*
  // rather than the world: the fit follows whatever has been dragged, so a node moved
  // past the old edge is still one press of Fit away. What the clamp actually did was
  // refuse the gesture — a node against the top of the canvas simply stopped, which
  // reads as the picture being broken rather than as a boundary being enforced.
  //
  // So the world is a budget for the *layout* to settle in, not a fence around the
  // arrangement. Panning is unconditional for the same reason (see the pointerdown
  // handler): once a node can be anywhere, the frame has to be able to go there.
  function place(node, x, y) {
    node.x = x;
    node.y = y;
  }

  // pin records where a node has been put, and marks it as put there.
  function pin(id, node, element) {
    node.held = true;
    pinned.set(id, { x: node.x, y: node.y });
    (element || nodeEls.get(id))?.classList.add("mesh-pinned");
    updateRelease();
  }

  // grip is what a drag moves: the edges that will pull, at the lengths they had
  // when it started, and every node's footprint. Taken once per gesture rather than
  // per frame, so the geometry a drag is working against cannot drift under it.
  function gripOn() {
    return {
      tethers: tethersFor(placed, shown.edges),
      radii: placed.map((n) => n.r ?? 12),
    };
  }
  let grip = null;

  function beginDrag(id, event) {
    const node = at.get(id);
    const grabbed = node && pointToFrame(event);
    if (!grabbed) return false;
    // The grab offset, so the node does not jump its own centre onto the pointer.
    moving = {
      id, node, pointer: event.pointerId, shifted: false,
      dx: node.x - grabbed.x, dy: node.y - grabbed.y,
      from: { x: node.x, y: node.y },
    };
    node.held = true;
    grip = gripOn();
    relate(id);
    return true;
  }

  // nudge settles the graph a little way toward its new shape and writes the result
  // into the SVG that is already on screen. One frame's worth per frame: a full
  // settle on every pointer move would be slower and would make the picture jump
  // rather than follow.
  let nudging = 0;
  function nudge() {
    if (nudging) return;
    nudging = requestAnimationFrame(() => {
      nudging = 0;
      // No early return when the drag has already ended: the last pointer move can
      // land between two frames, and skipping that frame would leave the node drawn
      // where it was a moment before it was dropped — pinned at one place and
      // painted at another until something else repainted the view.
      if (grip) follow(placed, grip.tethers, grip.radii);
      applyPositions();
    });
  }

  function endDrag() {
    if (!moving) return;
    const { id, node, shifted, pointer } = moving;
    moving = null;
    try { surface.releasePointerCapture(pointer); } catch { /* never captured */ }
    if (!shifted) {
      // A press that never moved is a click, not a drag: it leaves nothing pinned.
      node.held = pinned.has(id);
      grip = null;
      return;
    }
    pin(id, node);
    // Drawn where it was dropped, now, rather than on whichever frame happens to run
    // next. Everything downstream — the pin, the panel, the next repaint — agrees
    // about where this node is, so the picture must not be the one thing that does not.
    follow(placed, grip.tethers, grip.radii);
    applyPositions();
    grip = null;
    dragged = true; // the click that ends this drag must not also select
  }

  function updateRelease() {
    release.disabled = pinned.size === 0;
  }

  // dragged suppresses the click that ends a pan or a drag. Panning, dragging and
  // selecting share the same surface, and a gesture that also selected whatever it
  // started on would make the picture impossible to move without changing the
  // answer beside it.
  let panning = null, dragged = false;
  surface.addEventListener("pointerdown", (event) => {
    if (event.button !== 0) return;
    // Whatever the last gesture suppressed, this one is a fresh question. Reset here
    // rather than in the click handler: a gesture that ends outside the canvas never
    // produces a click, and the flag would then swallow the next real one.
    dragged = false;
    // A node under the pointer is the thing being moved; the background is the view.
    const node = event.target.closest?.("[data-node-id]");
    // Not preventDefault() here, however tempting: cancelling pointerdown also
    // cancels the compatibility mouse events behind it, and the click that selects a
    // node is one of them. The browser's own gesture is held off by the stylesheet
    // instead (the canvas takes no text selection) and by the move below.
    if (node && beginDrag(node.getAttribute("data-node-id"), event)) return;
    // Panning always, at any magnification. It used to be refused at the fitted
    // frame — everything was on screen there, so a drag could only push the picture
    // into the empty space the fit exists to remove. That is no longer the whole
    // truth: a node can now be dragged anywhere (see place), so there is somewhere to
    // pan *to*, and a canvas that only moves when zoomed in is a canvas whose rules
    // a reader has to discover. Fit is the way back, and it is one button.
    const from = pointToFrame(event);
    if (!from) return;
    panning = { from, start: frameView || baseView(), id: event.pointerId };
    dragged = false;
  });
  surface.addEventListener("pointermove", (event) => {
    if (moving && event.pointerId === moving.pointer) {
      const point = pointToFrame(event);
      if (!point) return;
      // Cancelling the *move* is safe where cancelling the press was not, and it is
      // what stops a touch drag from being taken over by the page's own scrolling.
      event.preventDefault();
      const node = moving.node;
      place(node, point.x + moving.dx, point.y + moving.dy);
      if (!moving.shifted && Math.hypot(node.x - moving.from.x, node.y - moving.from.y) > 3) {
        moving.shifted = true;
        // Captured only now that this is a drag rather than a press. Capturing on
        // the press would retarget the compatibility mouse events behind it to the
        // canvas, and the click that selects a node is one of them — so a press that
        // never moved would stop selecting anything. From here on there is no click
        // to protect: this gesture suppresses its own.
        try { surface.setPointerCapture(event.pointerId); } catch { /* no capture, no harm */ }
      }
      nudge();
      return;
    }
    if (!panning || event.pointerId !== panning.id) return;
    const svg = surface.querySelector("svg");
    const rect = svg?.getBoundingClientRect();
    if (!rect?.width) return;
    const dx = ((event.clientX - rect.left) / rect.width) * panning.start.w;
    const dy = ((event.clientY - rect.top) / rect.height) * panning.start.h;
    const moveX = panning.from.x - (panning.start.x + dx);
    const moveY = panning.from.y - (panning.start.y + dy);
    if (Math.abs(moveX) + Math.abs(moveY) > 4) dragged = true;
    if (!dragged) return;
    frameView = { ...panning.start, x: panning.start.x + moveX, y: panning.start.y + moveY };
    applyView();
  });
  const endGesture = () => { endDrag(); panning = null; };
  surface.addEventListener("pointerup", endGesture);
  surface.addEventListener("pointercancel", endGesture);
  // Leaving the canvas ends a pan but not a drag: the pointer was captured for the
  // drag, so it is still this gesture's, and dropping the node at the boundary is
  // the one thing somebody dragging toward the edge is not asking for.
  surface.addEventListener("pointerleave", () => { if (!moving) endGesture(); });

  // Double-clicking a node goes into it.
  surface.addEventListener("dblclick", (event) => {
    const id = event.target.closest?.("[data-node-id]")?.getAttribute("data-node-id");
    if (!id) return;
    event.preventDefault();
    drillTo(id);
  });

  release.addEventListener("click", () => {
    if (!pinned.size) return;
    pinned.clear();
    for (const n of placed) n.held = false;
    // With nothing pinned the layout fits again, so the view goes back to the whole
    // landscape rather than to whatever corner the arrangement had been read from.
    frameView = null;
    updateRelease();
    paint();
  });

  surface.addEventListener("wheel", (event) => {
    const focus = pointToFrame(event);
    if (!focus) return;
    event.preventDefault();
    zoom(event.deltaY > 0 ? 1.18 : 1 / 1.18, focus);
  }, { passive: false });

  zoomIn.addEventListener("click", () => zoom(1 / 1.3));
  zoomOut.addEventListener("click", () => zoom(1.3));
  // Fit reframes onto the content as it is now, arrangement included: somebody who
  // has dragged half the landscape into a shape and then asks to see all of it is
  // asking about the shape they made, not about the one the layout proposed.
  zoomFit.addEventListener("click", () => { frameView = null; refit(); applyView(); });

  surface.addEventListener("click", (event) => {
    if (dragged) { dragged = false; return; }
    const node = event.target.closest("[data-node-id]");
    // Shift as well as the platform key: it is what a list expects, and on a canvas
    // there is no range for it to mean anything else.
    const add = event.ctrlKey || event.metaKey || event.shiftKey;
    if (node) select(node.getAttribute("data-node-id"), { add });
    else if (!add) select(null); // a modifier-click on the background is a miss, not a clear
  });
  // Drilling into a node: the landscape reduced to it and what it touches.
  //
  // Double-click, because it is the gesture for "open this" everywhere else and
  // because the single click is already spoken for by selecting. Which cost the
  // double-click its previous job — releasing one pinned node — and that moved into
  // the panel beside the node it is about, where it is visible instead of being
  // folklore.
  function paintDrillChip() {
    if (!drilled) {
      drillOut.hidden = true;
      return;
    }
    const node = graph.nodes.find((n) => n.id === drilled);
    drillOut.hidden = false;
    drillOut.textContent = `Inside ${node?.name || drilled} ✕`;
    drillOut.title = "Back to the whole starmap";
  }

  function drillTo(id) {
    drilled = id;
    // The search box and the drilldown are two ways of asking the same kind of
    // question, so entering one clears the other rather than compounding with it.
    search.value = "";
    picked = [id];
    // Refitted, because the picture that comes back is a different graph in a
    // different world, and a frame from the old one lands on nothing.
    frameView = null;
    paint();
  }

  function drillOutOf() {
    if (!drilled) return;
    drilled = null;
    frameView = null;
    // The selection is left alone, so the landscape comes back with the thing that
    // was being read still marked in it: leaving a drilldown should not mean having
    // to find the node again.
    paint();
  }

  // The arrow goes where the double-click goes. Two ways into the same place rather
  // than two behaviours: a reader who has clicked a node and wants to see only it has
  // a control to press, and one who already knows the gesture keeps it.
  drillIn.addEventListener("click", () => {
    if (only()) drillTo(only());
  });

  drillOut.addEventListener("click", drillOutOf);
  // Escape is what leaves a thing you have gone into, in every other view — and it
  // has to work wherever the focus happens to be, because somebody who has just
  // double-clicked a circle has not focused anything in particular.
  //
  // Bound to the document for that reason, and guarded on this view still being on
  // the page: the landscape is one route of a single-page app, and a handler that
  // outlived it would act on a picture nobody is looking at.
  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || !drilled) return;
    if (!document.body.contains(view)) return;
    event.preventDefault();
    drillOutOf();
  });

  // Exporting the picture (ADR-0211 §10).

  // exportMeta describes the artifact about to be written: when the server read
  // this landscape, which instance it came from, and everything the picture is not
  // showing.
  //
  // It is assembled from the payload and from this view's own state, because
  // between them they are the only place the answer exists — and it is assembled at
  // the moment of export rather than at load, since a filter or a drilldown is
  // exactly what changes what the file contains.
  // windowStamp is the maintenance window as the file has to carry it (ADR-0211 §10):
  // who is in it, and what it costs together and apart. Computed here rather than
  // read off the panel, because the panel is markup and the numbers have to be the
  // ones the analysis produced.
  function windowStamp() {
    if (picked.length < 2) return null;
    const direction = dirSelect.value;
    const depth = depthSelect.value === "all" ? Infinity : Number(depthSelect.value);
    const overlap = windowOverlap(shown, picked, { direction, depth });
    if (!overlap) return null;
    const byId = new Map(shown.nodes.map((n) => [n.id, n]));
    return {
      members: picked.map((id) => shortLabel(byId.get(id) || { id })),
      total: overlap.total,
      sum: overlap.sum,
      direction,
      hops: depth === Infinity ? "any" : depth,
    };
  }

  function exportMeta() {
    const term = search.value.trim();
    const status = graph.status || {};
    const spoken = notationOf(notationPick.value);
    const scope = drilled
      ? {
          kind: "drill",
          name: (graph.nodes.find((n) => n.id === drilled) || {}).name || drilled,
          hops: depthSelect.value,
        }
      : term ? { kind: "filter", term } : { kind: "all" };
    return {
      // The server's reading, never this browser's clock: one dates the facts, the
      // other dates the save, and an export exists to be read later.
      observedAt: graph.observedAt,
      source: location.host,
      // Which vocabulary the file is drawn in, and what that vocabulary drops. A
      // reader who receives a C4-looking picture has no other way to learn that it
      // was projected from something else.
      notation: spoken.projection
        ? { label: spoken.label, short: spoken.short, projection: true,
            loss: spoken.loss, mappingVersion: spoken.mappingVersion }
        : null,
      scope,
      drawn: { nodes: shown.nodes.length },
      total: graph.nodes.length,
      // Landscape-level facts, taken from the payload rather than from the filtered
      // picture: "hidden by your access" is true of the whole regardless of how much
      // of it this file happens to show.
      restricted: graph.restricted || 0,
      clustered: Boolean(graph.clustered),
      // What the rings on the picture mean. Only when there is more than one: a
      // single selection is not a window, and its ring is explained by the fact that
      // somebody clicked it.
      window: windowStamp(),
      // Whether the numbers under the names are in this file. Said in the stamp
      // rather than left to be inferred: a reader who receives a picture with counts
      // on some nodes and not others has no way to tell "nothing running" from "this
      // export was taken with counts off".
      instances: instancesToggle.checked,
      partial: Boolean(status.partial),
      unavailable: (status.unavailable || []).map((u) => ({
        ...u, label: STATE_TEXT[u.state] || u.state,
      })),
    };
  }

  async function exportPicture(kind) {
    const canvas = surface.querySelector("svg");
    if (!canvas) {
      toast("There is nothing here to export.");
      return;
    }
    const theme = getComputedStyle(document.documentElement);
    const token = (name, fallback) => theme.getPropertyValue(name).trim() || fallback;
    // The page's own surface colour rather than a fixed white: the accent and the
    // neutrals are configurable per instance, and a file that ignored them would not
    // look like the landscape it was taken from.
    const background = token("--surface", "#ffffff");
    try {
      const built = standaloneSVG(canvas, {
        stamp: stampLines(exportMeta()),
        // The key travels with the picture. Beside the canvas it is one scroll away;
        // in a file that has been pasted into a ticket there is nothing to scroll to,
        // and a hexagon nobody can name is a shape rather than a worker.
        legend: legendEntries(shown, notationOf(notationPick.value)),
        css: exportStyles(canvas.outerHTML),
        // The whole world, not the window: the canvas's own viewBox is wherever the
        // reader has zoomed to, and a file cropped to that would drop nodes without
        // saying it had.
        extent: `0 0 ${world.width} ${world.height}`,
        background,
        ink: token("--text", "#111111"),
        muted: token("--muted", "#666666"),
        rule: token("--border", "#dddddd"),
      });
      if (kind === "png") {
        save(await rasterise(built, { background }), exportName("png"));
      } else {
        save(new Blob([built.svg], { type: "image/svg+xml;charset=utf-8" }), exportName("svg"));
      }
    } catch (e) {
      toast("export failed: " + e.message, "err");
    }
  }

  // A different vocabulary is a different drawing, so the picture is painted again.
  // The arrangement survives it: paint() carries the positions on screen forward and
  // every notation's shape is inscribed in the same reserved circle, so nothing moves
  // except the outlines.
  notationPick.addEventListener("change", paint);
  // A repaint rather than a class toggle: the count is a line under every name, so
  // switching it on changes how much room a node needs and therefore the layout that
  // reserves it (see the margin in renderGraph).
  instancesToggle.addEventListener("change", paint);

  exportModelBtn.addEventListener("click", () => {
    window.location.href = "/api/v1/panorama/mesh/archimate";
  });

  exportSvgBtn.addEventListener("click", () => exportPicture("svg"));
  exportPngBtn.addEventListener("click", () => exportPicture("png"));

  // Saved views.

  //
  // Reading a landscape is not a single act: somebody watching one node filters down
  // to it, zooms in, arranges what is around it — and a reload puts them back at the
  // whole landscape with all of it to do again. A saved view is that setup with a
  // name on it, and opening one is the only thing on this page that changes five
  // controls at once, which is why it is a list of names rather than a URL to
  // remember.
  //
  // What is saved is the whole question: the filter, the direction and depth, the
  // node being watched, how far in the view is zoomed, and the arrangement. What is
  // *not* saved is the graph — the landscape is derived and changes as things are
  // deployed, so a view is a way of looking rather than a snapshot of what was there.
  // Reached once and guarded: in a sandboxed frame or with site data blocked, even
  // *touching* window.localStorage throws, and that must cost the landscape nothing
  // more than its saved views.
  const store = (() => { try { return window.localStorage; } catch { return null; } })();
  let views = readViews(store);

  function say(message) {
    viewNote.textContent = message || "";
    viewNote.hidden = !message;
  }

  function renderViews() {
    viewList.innerHTML = views.length
      ? views.map((v) => `<li>
          <button type="button" class="mesh-view-open" data-view="${esc(v.id)}"
            title="${esc(viewSummary(v))}">${esc(v.name)}</button>
          <button type="button" class="mesh-view-drop" data-drop="${esc(v.id)}"
            aria-label="Forget the view ${esc(v.name)}" title="Forget this view">×</button>
        </li>`).join("")
      : `<li class="mesh-view-empty muted">Set the starmap up the way you want to
          find it, then name it here.</li>`;
  }

  // viewSummary says what a name stands for, so a list of names is still readable a
  // month later. It describes what was saved rather than what would be shown now:
  // the landscape may have moved on, and the view is the question, not the answer.
  function viewSummary(v) {
    const parts = [];
    if (v.term) parts.push(`filter “${v.term}”`);
    if (v.instances) parts.push("with instance counts");
    if (v.picked?.length) parts.push(`a window of ${v.picked.length} node(s)`);
    else if (v.selected) parts.push(`watching ${v.selected}`);
    if (v.zoom < 1) parts.push(`zoomed to ${Math.round(v.zoom * 100)}%`);
    if (v.pins?.length) parts.push(`${v.pins.length} node(s) placed by hand`);
    return parts.length ? parts.join(" · ") : "the whole landscape";
  }

  // openView puts the controls, the arrangement and the frame back.
  //
  // Twice through paint when there is an arrangement, and deliberately: the pins are
  // stored as fractions of the world, and the world is sized from whatever the filter
  // leaves on screen — so the first paint is what establishes the world the second
  // one places them in. Opening a saved view is a rare, deliberate act; paying two
  // layouts for it is cheaper than storing coordinates that mean somewhere else on a
  // different screen.
  function openView(v) {
    search.value = v.term || "";
    dirSelect.value = v.direction || "dependents";
    depthSelect.value = v.depth ?? "2";
    // A view saved before notations existed carries none, and the derived drawing is
    // what it was looking at.
    notationPick.value = notationOf(v.notation).id === v.notation ? v.notation : "atlas";
    // A view saved before the counts existed carries none, and false is the picture
    // it was looking at.
    instancesToggle.checked = Boolean(v.instances);
    picked = [];
    pinned.clear();
    frameView = null;
    paint();
    if (v.pins?.length) {
      for (const [id, at] of pinsFor(v, world)) pinned.set(id, at);
      paint();
    }
    updateRelease();
    // The selection last, and only the members still on screen. A landscape is
    // derived: what a view was watching can have been undeployed since, and the panel
    // must not describe something that is not there. A window that lost one member is
    // reopened as the rest of itself and says so — silently planning around two of
    // the three nodes somebody saved would be the worse answer.
    const wanted = v.picked?.length ? v.picked : (v.selected ? [v.selected] : []);
    picked = wanted.filter((id) => shown.nodes.some((n) => n.id === id));
    if (picked.length) refresh();
    frameView = frameFor(v, world, (id) => at.get(id));
    applyView();
    const lost = wanted.length - picked.length;
    say(lost
      ? `Opened “${v.name}”. ${lost} of the node(s) it was watching ${
        lost === 1 ? "is" : "are"} no longer in this starmap.`
      : "");
  }

  // Clicking a finding goes to it: selected, so the panel above explains it, and
  // framed, so it is on screen rather than somewhere in a landscape of four hundred
  // circles. Going *to* a finding is the whole reason the list is worth having.
  // goToNode selects a node and brings the view to it. Three lists now offer the
  // same gesture — the findings, the impact answer's own list, and the ranking — and
  // they behave identically because they are one function.
  function goToNode(id) {
    if (!at.get(id)) return;
    picked = [id];
    refresh();
    frameOn(id);
  }

  // Bringing a node on screen without answering a different question about it. The
  // window panel needs exactly this: its members are buttons, and pressing one to
  // find it on the canvas must not collapse the window into a selection of one.
  function frameOn(id) {
    const node = id && at.get(id);
    if (!node) return;
    // Held at whatever magnification is already in use if the view is zoomed, so a
    // reader working close in is not thrown back out; otherwise close enough to read
    // the node and what is immediately around it.
    const w = frameView ? frameView.w : world.width * 0.3;
    const h = frameView ? frameView.h : world.height * 0.3;
    frameView = { x: node.x - w / 2, y: node.y - h / 2, w, h };
    applyView();
  }

  for (const slot of [findingsSlot, rankingSlot, panel]) {
    slot.addEventListener("click", (event) => {
      const id = event.target.closest?.("[data-finding]")?.getAttribute("data-finding");
      if (id) goToNode(id);
    });
  }

  viewForm.addEventListener("submit", (event) => {
    event.preventDefault();
    const captured = captureView({
      name: viewName.value,
      term: search.value.trim(),
      direction: dirSelect.value,
      depth: depthSelect.value,
      notation: notationPick.value,
      selected: only(),
      picked,
      instances: instancesToggle.checked,
      frameView,
      world,
      pinned,
    });
    const outcome = saveView(views, captured);
    if (outcome.error) { say(outcome.error); return; }
    const replaced = views.length === outcome.views.length;
    views = outcome.views;
    renderViews();
    viewName.value = "";
    // A save the browser refused has to be said out loud. Storage can be full or off
    // entirely, and a view that quietly evaporated is worse than one that was
    // refused: the reader would find out by coming back for it.
    say(writeViews(store, views)
      ? `${replaced ? "Updated" : "Saved"} “${captured.name}”.`
      : "This browser is not storing anything, so the view is here until you reload.");
  });

  viewList.addEventListener("click", (event) => {
    const open = event.target.closest?.("[data-view]");
    if (open) {
      const v = views.find((entry) => entry.id === open.getAttribute("data-view"));
      if (v) openView(v);
      return;
    }
    const drop = event.target.closest?.("[data-drop]");
    if (!drop) return;
    views = removeView(views, drop.getAttribute("data-drop"));
    renderViews();
    writeViews(store, views);
    say("");
  });

  renderViews();

  // Arranging the landscape without a mouse. The arrangement is a convenience rather
  // than information — every relationship this view carries is already reachable by
  // focusing a node — but a convenience only some people can have is not one, and
  // stepping a focused node is four lines of the same machinery a drag uses.
  const ARROWS = {
    ArrowLeft: [-1, 0], ArrowRight: [1, 0], ArrowUp: [0, -1], ArrowDown: [0, 1],
  };
  surface.addEventListener("keydown", (event) => {
    const step = ARROWS[event.key];
    if (!step) return;
    const element = event.target.closest?.("[data-node-id]");
    const id = element?.getAttribute("data-node-id");
    const node = id && at.get(id);
    if (!node) return;
    // Arrows scroll the page by default, which is the opposite of what somebody who
    // has focused a node and pressed one is asking for.
    event.preventDefault();
    // A step is a share of the world rather than a fixed number of units, so it
    // covers the same fraction of the picture whatever size the landscape is. Shift
    // is the coarse one: crossing a large landscape a pixel at a time is not a
    // keyboard equivalent of a drag, it is a punishment for not having a mouse.
    const distance = Math.max(12, world.width * 0.02) * (event.shiftKey ? 5 : 1);
    // Held first, so the edges this step will pull are the ones recorded; pinned
    // after the move, so what is recorded is where the node ended up.
    node.held = true;
    const stepGrip = gripOn();
    place(node, node.x + step[0] * distance, node.y + step[1] * distance);
    pin(id, node, element);
    follow(placed, stepGrip.tethers, stepGrip.radii);
    applyPositions();
  });

  surface.addEventListener("keydown", (event) => {
    if (event.key !== "Enter" && event.key !== " ") return;
    const node = event.target.closest("[data-node-id]");
    if (!node) return;
    event.preventDefault();
    select(node.getAttribute("data-node-id"),
      { add: event.ctrlKey || event.metaKey || event.shiftKey });
  });
  dirSelect.addEventListener("change", () => {
    refresh();
    paintRanking();
  });
  // Depth means two things at once, and only one of them is free. Inside a drilldown
  // it decides how far the picture reaches, so changing it is a different graph and
  // has to be laid out again; outside one it only bounds the impact walk, which is
  // classes on the nodes already drawn.
  depthSelect.addEventListener("change", () => {
    // A drilldown is cut to the depth on screen, so changing it re-cuts the picture
    // and paint() repaints the ranking with it; otherwise only the answers change.
    if (drilled) return paint();
    refresh();
    paintRanking();
  });

  // Re-laying out on every keystroke is the wrong trade at 400 nodes, where the
  // simulation costs a few hundred milliseconds. A short debounce keeps typing
  // responsive and still feels immediate.
  let pending;
  search.addEventListener("input", () => {
    clearTimeout(pending);
    // Typing is asking about the whole landscape again. Leaving the drilldown in
    // force would search inside it while the box says otherwise.
    drilled = null;
    // A filter changes what is on screen, so the frame the viewer had zoomed into is
    // about a picture that no longer exists. Refitting is the honest reset; keeping
    // the old frame would land them on empty space and read as a broken view.
    frameView = null;
    pending = setTimeout(paint, 120);
  });
  paint();

  // The layout is a function of the frame, so a resized window is a different
  // picture. Debounced, because a drag-resize fires continuously and the simulation
  // is the expensive part.
  let resizing;
  const onResize = () => {
    clearTimeout(resizing);
    resizing = setTimeout(() => { frameView = null; paint(); }, 200);
  };
  window.addEventListener("resize", onResize);

  if (fetchMs > 2000) toast(`The starmap took ${Math.round(fetchMs)} ms to derive.`);
}
