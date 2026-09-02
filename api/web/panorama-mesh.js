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
  process: { r: 17, grow: 5, shape: "square", fill: "var(--surface)", stroke: "var(--border-strong)", label: "Process" },
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
export function shapeForNode(node) {
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
    default: return [];
  }
}

// bodyElement is the node's own outline, as SVG. Everything downstream keys off the
// mesh-body class rather than off the element name, so severity, hover and impact
// styling are unchanged by a node being a square.
//
// data-r carries the radius the layout reserved. The drawn outline is inscribed in
// it and no longer reports it as an attribute of its own, and it is the reserved
// circle — not the polygon — that the separation guarantee is about.
function bodyElement(shape, r, attrs) {
  const common = `class="mesh-body" data-r="${r.toFixed(1)}" ${attrs}`;
  if (shape === "square") {
    const half = r / Math.SQRT2;
    return `<rect ${common} x="${(-half).toFixed(1)}" y="${(-half).toFixed(1)}"
      width="${(half * 2).toFixed(1)}" height="${(half * 2).toFixed(1)}"
      rx="${(half * 0.26).toFixed(1)}"/>`;
  }
  const vertices = shapeVertices(shape, r);
  if (!vertices.length) return `<circle ${common} r="${r.toFixed(1)}"/>`;
  const points = vertices.map(([x, y]) => `${x.toFixed(1)},${y.toFixed(1)}`).join(" ");
  return `<polygon ${common} points="${points}"/>`;
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
// rather than by taste. At 0.09 a hundred nodes settle with 189 units between the
// closest pair — far more air than reading needs, and enough that the whole
// landscape is too small on screen to carry any names at all. At 0.28 the same
// graph keeps 87 units between its closest pair, which is still more than two node
// diameters of clear space, and the world has shrunk enough that a forty-node
// landscape shows every name at the opening view and a hundred-node one shows its
// applications.
//
// That is the reason for this number rather than a rounder one: it is where the
// space stops being empty and starts being the thing that makes names readable.
const WORLD_FILL = 0.28;

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
function layout(nodes, edges, { width, height, iterations = 220, pinned, from } = {}) {
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
      // Clamped, because the world is sized from the graph and the frame's shape:
      // a resize can make it smaller than it was when the pin was placed, and a pin
      // outside the world would put the node somewhere the fitted view never shows.
      n.x = Math.min(Math.max(pin.x, 0), width);
      n.y = Math.min(Math.max(pin.y, 0), height);
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
  if (!anchored) fitToFrame(nodes, width, height);
  // And once more where the circles are actually drawn. The fit scales positions
  // and leaves radii alone, so whatever the settle guaranteed is only true again
  // after this. Anything it moves outside the world is pulled back by the re-fit.
  separate(nodes, radii, NODE_ROOM);
  if (!anchored) fitToFrame(nodes, width, height);
  return performance.now() - started;
}

// LABEL_MARGIN is the room a node needs around its own centre. It is asymmetric
// because a node's label is: the text hangs below the circle (dy = r + 14) and is
// centred, so the bottom and the sides carry more than the top does.
//
// It is smaller than the widest label because a name is painted only once the zoom
// makes it readable (see labelTier), and reserving room for text that is not on
// screen is how the picture ends up smaller than the space it was given.
const LABEL_MARGIN = { top: 26, right: 46, bottom: 42, left: 46 };

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

// ZOOM_RANGE bounds how far the viewer can push the frame, as multiples of the
// fitted one. Out is capped because zooming out past the content only adds the empty
// space the fit exists to remove; in is capped where a node fills the frame and
// there is nothing further to see.
const ZOOM_RANGE = { min: 1 / 24, max: 1.6 };

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
// Returns { nodes, edges, truncatedBy, complete } or null.
export function impactFrom(graph, startId, { direction = "dependents", depth = Infinity } = {}) {
  const byId = new Map(graph.nodes.map((n) => [n.id, n]));
  if (!byId.has(startId)) return null;

  const forward = new Map(), backward = new Map();
  for (const e of graph.edges) {
    if (!DEPENDENCY_EDGES.has(e.kind)) continue;
    if (!forward.has(e.from)) forward.set(e.from, []);
    if (!backward.has(e.to)) backward.set(e.to, []);
    forward.get(e.from).push(e);
    backward.get(e.to).push(e);
  }
  const step = (id) => {
    const out = [];
    if (direction !== "dependencies") out.push(...(backward.get(id) || []).map((e) => [e, e.from]));
    if (direction !== "dependents") out.push(...(forward.get(id) || []).map((e) => [e, e.to]));
    return out;
  };

  const seen = new Set([startId]);
  const edges = [];
  const truncatedBy = [];
  let frontier = [startId];
  for (let hop = 0; hop < depth && frontier.length; hop++) {
    const next = [];
    for (const id of frontier) {
      // A placeholder stands for something we may not see, so its own edges are not
      // ours to follow — it is a boundary, not a waypoint.
      if (byId.get(id)?.kind === "restricted") continue;
      for (const [edge, other] of step(id)) {
        if (!edges.includes(edge)) edges.push(edge);
        if (seen.has(other)) continue;
        seen.add(other);
        if (byId.get(other)?.kind === "restricted") truncatedBy.push(other);
        next.push(other);
      }
    }
    frontier = next;
  }

  return {
    nodes: [...seen],
    edges,
    truncatedBy,
    complete: truncatedBy.length === 0,
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

function nodeTitle(node) {
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

function legendHTML(graph, layoutMs) {
  const present = new Set(graph.nodes.map((n) => n.kind));
  // The swatch is drawn by the same function the node is, so a legend cannot come to
  // disagree with the picture it explains.
  const swatches = Object.entries(KIND)
    .filter(([kind]) => present.has(kind))
    .map(([kind, style]) => `<span class="mesh-swatch">
      <svg width="16" height="16" aria-hidden="true"><g transform="translate(8,8)">
        ${bodyElement(style.shape, 6,
          `fill="${style.fill}" stroke="${style.stroke}" stroke-width="2" ` +
          (style.dashed ? 'stroke-dasharray="3 2"' : ""))}
      </g></svg>${esc(style.label)}</span>`)
    .join("");

  const notes = [];
  if (graph.restricted > 0) {
    notes.push(`<p class="mesh-note"><b>${graph.restricted}</b> node(s) are hidden by your
      access. Their dependencies are drawn, their identities are not — this picture is
      filtered, and says so rather than looking complete.</p>`);
  }
  if (graph.clustered) {
    notes.push(`<p class="mesh-note">This landscape exceeded the size budget, so it is
      collapsed to applications. Each one states how many nodes it stands for.</p>`);
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

  // Severity swatches list only the classes actually on screen, for the same reason
  // the kind swatches do: a legend describing findings the picture does not contain
  // is a legend nobody reads twice.
  const status = graph.status || {};
  const severityPresent = new Set(graph.nodes.map((n) => n.severity).filter(Boolean));
  const severity = ["critical", "attention", "ok", "unknown"]
    .filter((key) => severityPresent.has(key))
    .map((key) => `<span class="mesh-swatch mesh-sev-${key}">
      <svg width="16" height="16" aria-hidden="true">
        <circle cx="8" cy="8" r="6" fill="var(--surface)" stroke="${SEVERITY[key].stroke || "var(--border-strong)"}"
          stroke-width="2"/>
        ${SEVERITY[key].glyph ? `<text x="8" y="11.5" text-anchor="middle" class="mesh-sev-glyph">${esc(SEVERITY[key].glyph)}</text>` : ""}
      </svg>${esc(SEVERITY[key].label)}</span>`).join("");

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

  const provenanceKeys = compared ? ["derived", "both", "modeled"] : [];
  const provenance = provenanceKeys.map((key) => `<span class="mesh-swatch">
    <svg width="16" height="16" aria-hidden="true">
      ${PROVENANCE[key].ring ? `<circle cx="8" cy="8" r="7" fill="none" stroke="var(--muted)" stroke-width="1" opacity="0.55"/>` : ""}
      <circle cx="8" cy="8" r="5" fill="${PROVENANCE[key].ghost ? "none" : "var(--surface)"}"
        stroke="var(--muted)" stroke-width="2"
        ${PROVENANCE[key].ghost ? 'stroke-dasharray="3 2"' : ""}/>
    </svg>${esc(PROVENANCE[key].label)}</span>`).join("");

  return `<div class="mesh-legend">
    <div class="mesh-swatches">${swatches}</div>
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

function renderGraph(graph, layoutMs, frame, { pinned, from } = {}) {
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
  const ms = layout(nodes, graph.edges, { width, height, pinned, from }) + layoutMs;
  const at = new Map(nodes.map((n) => [n.id, n]));

  const edges = graph.edges.map((e) => {
    const a = at.get(e.from), b = at.get(e.to);
    if (!a || !b) return "";
    const dashed = e.kind === "contains";
    return `<line x1="${a.x.toFixed(1)}" y1="${a.y.toFixed(1)}"
      x2="${b.x.toFixed(1)}" y2="${b.y.toFixed(1)}"
      data-from="${esc(e.from)}" data-to="${esc(e.to)}"
      class="mesh-edge${dashed ? " mesh-edge-contains" : ""}"/>`;
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
    return `<g transform="translate(${n.x.toFixed(1)},${n.y.toFixed(1)})"
      class="mesh-node mesh-${n.kind} mesh-prov-${esc(n.provenance || "derived")} mesh-sev-${esc(n.severity || "unknown")}${named ? " mesh-named" : ""}${context ? " mesh-context" : ""}${n.held ? " mesh-pinned" : ""}"
      data-node-id="${esc(n.id)}" data-severity="${esc(n.severity || "unknown")}"
      tabindex="0" role="button" aria-label="${esc(nodeTitle(n))}">
      ${sev.beats ? `<circle class="mesh-beat" r="${r.toFixed(1)}"/>` : ""}
      <circle class="mesh-halo" r="${(r + 6).toFixed(1)}"/>
      ${prov.ring ? `<circle r="${(r + 4).toFixed(1)}" fill="none" stroke="${style.stroke}" stroke-width="1" opacity="0.55"/>` : ""}
      ${bodyElement(shapeForNode(n), r,
        `fill="${prov.ghost ? "none" : style.fill}" stroke="${sev.stroke || style.stroke}" ` +
        `stroke-width="${sev.stroke ? 3 : 2}" ${style.dashed || prov.ghost ? 'stroke-dasharray="4 3"' : ""}`)}
      <circle class="mesh-pin" r="4" cx="${(-r * 0.72).toFixed(1)}" cy="${(r * 0.72).toFixed(1)}"/>
      ${n.children ? `<text class="mesh-count" text-anchor="middle" dy="4">${n.children}</text>` : ""}
      ${badge}
      <text class="mesh-label" text-anchor="middle" dy="${(r + 14).toFixed(1)}"><tspan class="mesh-label-ink">${label}</tspan></text>
      <title>${esc(nodeTitle(n))}</title></g>`;
  }).join("");

  // Beating is switched on for the whole canvas rather than per node, so the budget
  // is one decision about this picture instead of a rule each node applies to itself.
  const beating = nodes.filter((n) => (SEVERITY[n.severity] || {}).beats).length;

  // The viewBox starts as the whole world, because that is what fitToFrame put the
  // content inside. The world carries the frame's own aspect ratio, so with
  // preserveAspectRatio's default there is nothing to letterbox — the opening
  // picture is the entire landscape, filling the window.
  return { ms, world, nodes, svg: `<svg class="mesh-canvas${
    beating && beating <= PULSE_BUDGET ? " mesh-beating" : ""}" viewBox="0 0 ${width} ${height}"
    role="img" aria-label="Derived landscape mesh">
    <g class="mesh-edges">${edges}</g>${circles}</svg>` };
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
  const scope = graph.matched ? " in the filtered landscape" : "";
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

// impactPanelHTML states the answer in words beside the picture. The counts are the
// point — a highlighted subgraph tells you *which*, a count tells you *how many*,
// and "17 things depend on this worker" is the sentence somebody repeats in a
// change-approval meeting.
function impactPanelHTML(node, result, direction, depth, { pinned = false } = {}) {
  if (!node) {
    return `<div class="mesh-panel mesh-panel-empty">
      <b>Nothing selected</b>
      <p>Select a node to see what depends on it, and what it depends on.</p></div>`;
  }
  const kindLabel = (KIND[node.kind] || {}).label || node.kind;
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
  const truncation = result && !result.complete
    ? `<p class="mesh-note mesh-truncated"><b>Incomplete.</b> The walk stopped at
        ${result.truncatedBy.length} node(s) outside your access, so there may be more
        beyond them. Treat the count as a lower bound.</p>`
    : "";
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
    <div class="mesh-impact-count"><b>${others}</b> node(s) ${word}
      <span class="muted">within ${depth === Infinity ? "any" : depth} hop(s)</span></div>
    ${truncation}
    ${drill}
    ${release}
  </div>`;
}

export async function mountPanoramaMesh(view, { api, toast }) {
  view.innerHTML = `<div class="card"><h1>Landscape</h1><p class="muted">Deriving…</p></div>`;
  let graph;
  const fetched = performance.now();
  try {
    graph = await api("GET", "/api/v1/panorama/mesh");
  } catch (e) {
    view.innerHTML = `<div class="card empty"><h1>Landscape</h1>
      <p>${esc(e.message)}</p></div>`;
    return;
  }
  const fetchMs = performance.now() - fetched;

  if (!graph.nodes.length) {
    view.innerHTML = `<div class="card empty"><h1>Landscape</h1>
      <p>Nothing is deployed on this server yet. The landscape is derived from what
      Atlas holds, so it fills in as you deploy — there is nothing to model first.</p></div>`;
    return;
  }

  view.innerHTML = `<div id="mesh-root" class="card mesh-card">
    <div class="mesh-head">
      <h1>Landscape</h1>
      <input id="mesh-search" type="search" class="mesh-search" autocomplete="off"
        placeholder="Filter by name, kind or process id…" aria-label="Filter the landscape"/>
      <button id="mesh-drill-out" type="button" class="mesh-drill-chip" hidden></button>
      <span id="mesh-count" class="muted"></span>
    </div>
    <div id="mesh-legend-slot"></div>
    <div class="mesh-body">
      <div class="mesh-stage">
        <div id="mesh-surface" class="mesh-surface"></div>
        <div class="mesh-zoom" role="group" aria-label="Zoom">
          <button id="mesh-zoom-in" type="button" title="Zoom in">+</button>
          <button id="mesh-zoom-out" type="button" title="Zoom out">−</button>
          <button id="mesh-zoom-fit" type="button" title="Fit the whole landscape">Fit</button>
          <button id="mesh-release" type="button" disabled
            title="Put every node you have dragged back where the layout puts it">Release</button>
        </div>
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
  const surface = document.getElementById("mesh-surface");
  const zoomIn = document.getElementById("mesh-zoom-in");
  const zoomOut = document.getElementById("mesh-zoom-out");
  const zoomFit = document.getElementById("mesh-zoom-fit");
  const release = document.getElementById("mesh-release");
  const legendSlot = document.getElementById("mesh-legend-slot");
  const count = document.getElementById("mesh-count");
  const panel = document.getElementById("mesh-panel-slot");
  const dirSelect = document.getElementById("mesh-direction");
  const depthSelect = document.getElementById("mesh-depth");
  const findingsSlot = document.getElementById("mesh-findings-slot");
  const viewList = document.getElementById("mesh-view-list");
  const viewForm = document.getElementById("mesh-view-save");
  const viewName = document.getElementById("mesh-view-name");
  const viewNote = document.getElementById("mesh-view-note");

  let selected = null;
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

  function baseView() {
    return { x: 0, y: 0, w: world.width, h: world.height };
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
      toast("That node is no longer in this landscape.");
    }
    shown = drilledGraph || filterGraph(graph, term);
    paintDrillChip();
    // A selection that the filter removed is no longer selected: highlighting a node
    // that is not on screen would leave the panel describing something invisible.
    if (selected && !shown.nodes.some((n) => n.id === selected)) selected = null;

    measure();
    // Where everything currently is, so a repaint while something is pinned carries
    // the picture on screen forward instead of settling a fresh one around the pins.
    const from = new Map(placed.map((n) => [n.id, { x: n.x, y: n.y }]));
    const painted = renderGraph(shown, 0, frame, { pinned, from });
    const { ms, svg } = painted;
    world = painted.world;
    placed = painted.nodes;
    at = new Map(placed.map((n) => [n.id, n]));
    surface.innerHTML = shown.nodes.length
      ? svg
      : `<p class="mesh-empty-filter">Nothing matches “${esc(term)}”.</p>`;
    index();
    // The rendered SVG carries none of the hover highlight, so the record of what is
    // lit has to be cleared with it — otherwise pointing back at the same node would
    // be a no-op and the highlight would never come back.
    lit = null;
    applyView();
    legendSlot.innerHTML = legendHTML(shown, ms);
    findingsSlot.innerHTML = findingsHTML(shown);
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
    const result = selected ? impactFrom(shown, selected, { direction, depth }) : null;
    const highlight = result ? new Set(result.nodes) : null;
    for (const [id, g] of nodeEls) {
      g.classList.toggle("mesh-in-impact", Boolean(highlight?.has(id)));
      g.classList.toggle("mesh-dimmed", Boolean(highlight) && !highlight.has(id));
    }
    for (const line of edgeEls) {
      const inside = Boolean(highlight?.has(line.dataset.from) && highlight.has(line.dataset.to));
      line.classList.toggle("mesh-in-impact", inside);
      line.classList.toggle("mesh-dimmed", Boolean(highlight) && !inside);
    }
    panel.innerHTML = impactPanelHTML(
      shown.nodes.find((n) => n.id === selected) || null, result, direction, depth,
      { pinned: pinned.has(selected) });
  }

  // Releasing the one node the panel is about, without disturbing the arrangement
  // around it.
  panel.addEventListener("click", (event) => {
    const id = event.target.closest?.("[data-unpin]")?.getAttribute("data-unpin");
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

  function select(id) {
    selected = selected === id ? null : id; // clicking the selection again clears it
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

  // place puts a node at a point and keeps it inside the world, because the world is
  // what the fitted view shows: a node moved past its edge would be invisible at the
  // very view somebody would use to go looking for it.
  function place(node, x, y) {
    const r = node.r ?? 12;
    node.x = Math.min(Math.max(x, r), world.width - r);
    node.y = Math.min(Math.max(y, r), world.height - r);
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
    // Panning is only meaningful once something is off-screen. At the fitted frame
    // the whole landscape is already visible, so a drag there could only push it
    // out of view and reintroduce the empty space the fit exists to remove.
    if (!frameView || frameView.w >= world.width) return;
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
  zoomFit.addEventListener("click", () => { frameView = null; applyView(); });

  surface.addEventListener("click", (event) => {
    if (dragged) { dragged = false; return; }
    const node = event.target.closest("[data-node-id]");
    if (node) select(node.getAttribute("data-node-id"));
    else select(null);
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
    drillOut.title = "Back to the whole landscape";
  }

  function drillTo(id) {
    drilled = id;
    // The search box and the drilldown are two ways of asking the same kind of
    // question, so entering one clears the other rather than compounding with it.
    search.value = "";
    selected = id;
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
      : `<li class="mesh-view-empty muted">Set the landscape up the way you want to
          find it, then name it here.</li>`;
  }

  // viewSummary says what a name stands for, so a list of names is still readable a
  // month later. It describes what was saved rather than what would be shown now:
  // the landscape may have moved on, and the view is the question, not the answer.
  function viewSummary(v) {
    const parts = [];
    if (v.term) parts.push(`filter “${v.term}”`);
    if (v.selected) parts.push(`watching ${v.selected}`);
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
    selected = null;
    pinned.clear();
    frameView = null;
    paint();
    if (v.pins?.length) {
      for (const [id, at] of pinsFor(v, world)) pinned.set(id, at);
      paint();
    }
    updateRelease();
    // The selection last, and only if the node is still there. A landscape is
    // derived: what a view was watching can have been undeployed since, and the
    // panel must not describe something that is not on the screen.
    if (v.selected && shown.nodes.some((n) => n.id === v.selected)) {
      selected = v.selected;
      refresh();
    }
    frameView = frameFor(v, world, (id) => at.get(id));
    applyView();
    say(v.selected && !selected
      ? `Opened “${v.name}”. The node it was watching is no longer in this landscape.`
      : "");
  }

  // Clicking a finding goes to it: selected, so the panel above explains it, and
  // framed, so it is on screen rather than somewhere in a landscape of four hundred
  // circles. Going *to* a finding is the whole reason the list is worth having.
  findingsSlot.addEventListener("click", (event) => {
    const id = event.target.closest?.("[data-finding]")?.getAttribute("data-finding");
    const node = id && at.get(id);
    if (!node) return;
    selected = id;
    refresh();
    // Held at whatever magnification is already in use if the view is zoomed, so a
    // reader working close in is not thrown back out; otherwise close enough to read
    // the node and what is immediately around it.
    const w = frameView ? frameView.w : world.width * 0.3;
    const h = frameView ? frameView.h : world.height * 0.3;
    frameView = { x: node.x - w / 2, y: node.y - h / 2, w, h };
    applyView();
  });

  viewForm.addEventListener("submit", (event) => {
    event.preventDefault();
    const captured = captureView({
      name: viewName.value,
      term: search.value.trim(),
      direction: dirSelect.value,
      depth: depthSelect.value,
      selected,
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
    select(node.getAttribute("data-node-id"));
  });
  dirSelect.addEventListener("change", refresh);
  // Depth means two things at once, and only one of them is free. Inside a drilldown
  // it decides how far the picture reaches, so changing it is a different graph and
  // has to be laid out again; outside one it only bounds the impact walk, which is
  // classes on the nodes already drawn.
  depthSelect.addEventListener("change", () => (drilled ? paint() : refresh()));

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

  if (fetchMs > 2000) toast(`The landscape took ${Math.round(fetchMs)} ms to derive.`);
}
