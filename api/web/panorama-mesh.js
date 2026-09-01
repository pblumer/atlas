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

const esc = (value) => String(value ?? "").replace(/[&<>"']/g, (character) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[character]);

// KIND describes every node kind the mesh can carry: how it is drawn, and what it
// is called in the legend. Restricted and unresolved are deliberately distinct —
// "you may not see it" and "it is not deployed" are different findings, and a
// picture that renders them alike answers the wrong question.
// Radii carry rank as well as kind. At a few hundred nodes the eye sorts by size
// before it reads anything, so an application has to be unmistakably the largest
// thing on screen and a leaf unmistakably the smallest — otherwise every node
// competes for attention and the picture reads as one texture.
const KIND = {
  application: { r: 30, fill: "var(--accent-soft)", stroke: "var(--accent)", label: "Application" },
  process: { r: 17, fill: "var(--surface)", stroke: "var(--border-strong)", label: "Process" },
  // --ok is a fixed green rather than a shade of the configurable accent, so its
  // soft companion is a literal here too. There is no --ok-soft at :root, and
  // defining one would change the one other rule that already asks for it.
  worker: { r: 12, fill: "#e8f5ec", stroke: "var(--ok)", label: "Worker" },
  decision: { r: 12, fill: "var(--accent-soft)", stroke: "var(--accent-hover)", label: "Decision" },
  restricted: { r: 11, fill: "var(--bg)", stroke: "var(--muted)", label: "Restricted — outside your access", dashed: true },
  unresolved: { r: 11, fill: "var(--warn-soft)", stroke: "var(--warn)", label: "Unresolved — nothing here provides it", dashed: true },
};

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
  critical: { glyph: "!", stroke: "var(--danger)", label: "Critical — it cannot do work" },
  attention: { glyph: "•", stroke: "var(--warn)", label: "Attention — something inside it went wrong" },
  ok: { glyph: "", stroke: "", label: "OK — nothing is wrong here" },
  unknown: { glyph: "?", stroke: "", label: "Unwatched — nothing here observes it" },
};

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

// WORLD_FILL is how much of the world the nodes' own cells take up. A low number is
// what makes this a universe rather than a pile: at 1.0 the nodes would tile the
// space edge to edge, and everything interesting about a force layout — which
// things sit near which — is carried by the emptiness between them.
const WORLD_FILL = 0.09;

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
    const cell = 2 * ((KIND[n.kind] || KIND.process).r + NODE_ROOM);
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
        const push = (room - d) / 2;
        a.x += (dx / d) * push; a.y += (dy / d) * push;
        b.x -= (dx / d) * push; b.y -= (dy / d) * push;
        moved = true;
      }
    }
    if (!moved) return;
  }
}

// layout settles the graph with repulsion between every pair, springs along edges,
// and a pull toward the centre. Returns the elapsed milliseconds so the caller can
// report what the budget actually costs on this machine.
function layout(nodes, edges, { width, height, iterations = 220 } = {}) {
  const started = performance.now();
  const random = mulberry32(0x5EED);
  const index = new Map(nodes.map((n, i) => [n.id, i]));
  const cx = width / 2, cy = height / 2;
  // The frame is usually wider than it is tall. Both the initial scatter and the
  // centring pull are shaped by that ratio, so the settled graph is the shape of the
  // space it has to live in.
  const aspect = Math.max(width, 1) / Math.max(height, 1);
  const pullX = 1 / aspect, pullY = aspect;

  for (const n of nodes) {
    // Seeded scatter on an ellipse, so nothing starts coincident (which would make
    // the repulsion term divide by zero and fling nodes to infinity).
    const angle = random() * Math.PI * 2;
    const radius = 0.15 + random() * 0.85;
    n.x = cx + Math.cos(angle) * radius * width * 0.42;
    n.y = cy + Math.sin(angle) * radius * height * 0.42;
    n.vx = 0; n.vy = 0;
  }

  const links = edges
    .map((e) => [index.get(e.from), index.get(e.to)])
    .filter(([a, b]) => a !== undefined && b !== undefined);

  // Repulsion scales with the graph so density stays roughly constant instead of
  // rising with node count — the "Klüngel" a fixed constant produces, where fifty
  // nodes are comfortable and three hundred are one dark blob.
  // Both scale with the world: a graph settled at a fixed spring length inside a
  // world sized for its content is a knot in the middle of an empty field, and
  // enlarging that knot is not the same as spreading it out.
  const reach = Math.min(width, height);
  const repulsion = 5200 * Math.max(1, Math.sqrt(nodes.length / 40)) * Math.max(1, reach / 720);
  const spring = 0.012, damping = 0.85;
  const rest = Math.max(130, reach * 0.16);
  // Every node's own footprint, so the separation pass below knows what "touching"
  // means for this pair rather than assuming one radius for all of them.
  const radii = nodes.map((n) => (KIND[n.kind] || KIND.process).r);
  for (let step = 0; step < iterations; step++) {
    for (let i = 0; i < nodes.length; i++) {
      for (let j = i + 1; j < nodes.length; j++) {
        const a = nodes[i], b = nodes[j];
        let dx = a.x - b.x, dy = a.y - b.y;
        let d2 = dx * dx + dy * dy;
        if (d2 < 0.01) { dx = 0.1; dy = 0.1; d2 = 0.02; }
        const force = repulsion / d2;
        const d = Math.sqrt(d2);
        const fx = (dx / d) * force, fy = (dy / d) * force;
        a.vx += fx; a.vy += fy; b.vx -= fx; b.vy -= fy;

        // Separation. Repulsion alone is a soft force that a spring can overpower,
        // so two nodes joined by an edge will happily sit on top of each other —
        // which is the one arrangement that makes a picture unreadable rather than
        // merely tight. This pushes overlapping circles apart directly, and it is
        // in the same pass because that pass already visits every pair.
        const room = radii[i] + radii[j] + NODE_ROOM;
        if (d < room) {
          const push = (room - d) * 0.5;
          a.x += (dx / d) * push; a.y += (dy / d) * push;
          b.x -= (dx / d) * push; b.y -= (dy / d) * push;
        }
      }
    }
    for (const [ai, bi] of links) {
      const a = nodes[ai], b = nodes[bi];
      const dx = b.x - a.x, dy = b.y - a.y;
      const d = Math.hypot(dx, dy) || 0.01;
      const force = (d - rest) * spring;
      const fx = (dx / d) * force, fy = (dy / d) * force;
      a.vx += fx; a.vy += fy; b.vx -= fx; b.vy -= fy;
    }
    for (const n of nodes) {
      // The pull toward the centre is anisotropic, weaker along the wider axis, so
      // the graph settles into the shape of the frame instead of into a disc. A disc
      // in a wide viewport is what produced the empty bands on either side: the
      // content was never the shape of the space it had.
      n.vx += (cx - n.x) * 0.0012 * pullX;
      n.vy += (cy - n.y) * 0.0012 * pullY;
      n.vx *= damping; n.vy *= damping;
      n.x += n.vx; n.y += n.vy;
    }
  }
  fitToFrame(nodes, width, height);
  // And once more where the circles are actually drawn. The fit scales positions
  // and leaves radii alone, so whatever the settle guaranteed is only true again
  // after this. Anything it moves outside the world is pulled back by the re-fit.
  separate(nodes, radii, NODE_ROOM);
  fitToFrame(nodes, width, height);
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

// filterGraph keeps the matching nodes and the edges whose both ends survived.
// A search is the viewer's own choice, unlike a sharing scope, so a dropped edge
// here is not a lie — but the header still reports how much is hidden, because a
// filtered mesh looks exactly like a small one.
function filterGraph(graph, term) {
  if (!term) return graph;
  const nodes = graph.nodes.filter((n) => matches(n, term));
  const keep = new Set(nodes.map((n) => n.id));
  return {
    ...graph,
    nodes,
    edges: graph.edges.filter((e) => keep.has(e.from) && keep.has(e.to)),
  };
}

function legendHTML(graph, layoutMs) {
  const present = new Set(graph.nodes.map((n) => n.kind));
  const swatches = Object.entries(KIND)
    .filter(([kind]) => present.has(kind))
    .map(([kind, style]) => `<span class="mesh-swatch">
      <svg width="14" height="14" aria-hidden="true"><circle cx="7" cy="7" r="5"
        fill="${style.fill}" stroke="${style.stroke}" stroke-width="2"
        ${style.dashed ? 'stroke-dasharray="3 2"' : ""}/></svg>${esc(style.label)}</span>`)
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

function renderGraph(graph, layoutMs, highlight, frame, selected) {
  // The graph is laid out in a world of its own size, not in the viewport. The
  // frame only decides that world's shape, so the opening view fills the window
  // without letterboxing.
  const world = worldFor(graph.nodes, frame);
  const { width, height } = world;
  const nodes = graph.nodes.map((n) => ({ ...n }));
  const ms = layout(nodes, graph.edges, { width, height }) + layoutMs;
  const at = new Map(nodes.map((n) => [n.id, n]));

  const edges = graph.edges.map((e) => {
    const a = at.get(e.from), b = at.get(e.to);
    if (!a || !b) return "";
    const dashed = e.kind === "contains";
    const state = highlight
      ? (highlight.has(e.from) && highlight.has(e.to) ? " mesh-in-impact" : " mesh-dimmed")
      : "";
    return `<line x1="${a.x.toFixed(1)}" y1="${a.y.toFixed(1)}"
      x2="${b.x.toFixed(1)}" y2="${b.y.toFixed(1)}"
      data-from="${esc(e.from)}" data-to="${esc(e.to)}"
      class="mesh-edge${dashed ? " mesh-edge-contains" : ""}${state}"/>`;
  }).join("");

  const circles = nodes.map((n) => {
    const style = KIND[n.kind] || KIND.process;
    const label = n.kind === "restricted" ? "" : esc(n.name || "");
    // Selecting a node is what runs impact analysis, so the node itself is the
    // control. The drilldown into Operations moved into the selection panel: a node
    // cannot both navigate away and select, and selecting is the more frequent act.
    const state = highlight
      ? (highlight.has(n.id) ? " mesh-in-impact" : " mesh-dimmed")
      : "";
    const prov = PROVENANCE[n.provenance] || PROVENANCE.derived;
    const sev = SEVERITY[n.severity] || SEVERITY.unknown;
    // Severity is drawn as a badge on the node's own outline rather than by
    // recolouring it: the fill already carries the ArchiMate layer and the kind, and
    // ADR-0189 §6 keeps those. The glyph is what makes the finding readable without
    // colour perception at all.
    const badge = sev.glyph
      ? `<g class="mesh-badge" transform="translate(${(style.r * 0.72).toFixed(1)},${(-style.r * 0.72).toFixed(1)})">
           <circle r="7" class="mesh-badge-dot"/>
           <text text-anchor="middle" dy="3.5" class="mesh-badge-glyph">${esc(sev.glyph)}</text>
         </g>`
      : "";
    // Every node carries its name. Which of them are painted is the stylesheet's
    // decision, from the current magnification (see labelTier) — so zooming reveals
    // names with no re-render, and a selected, hovered or focused node keeps its own
    // whatever the zoom is.
    const named = Boolean(label) || n.id === selected;
    return `<g transform="translate(${n.x.toFixed(1)},${n.y.toFixed(1)})"
      class="mesh-node mesh-${n.kind} mesh-prov-${esc(n.provenance || "derived")} mesh-sev-${esc(n.severity || "unknown")}${named ? " mesh-named" : ""}${state}"
      data-node-id="${esc(n.id)}" data-severity="${esc(n.severity || "unknown")}"
      tabindex="0" role="button" aria-label="${esc(nodeTitle(n))}">
      ${prov.ring ? `<circle r="${style.r + 4}" fill="none" stroke="${style.stroke}" stroke-width="1" opacity="0.55"/>` : ""}
      <circle class="mesh-body" r="${style.r}" fill="${prov.ghost ? "none" : style.fill}" stroke="${sev.stroke || style.stroke}"
        stroke-width="${sev.stroke ? 3 : 2}" ${style.dashed || prov.ghost ? 'stroke-dasharray="4 3"' : ""}/>
      ${n.children ? `<text class="mesh-count" text-anchor="middle" dy="4">${n.children}</text>` : ""}
      ${badge}
      <text class="mesh-label" text-anchor="middle" dy="${style.r + 14}"><tspan class="mesh-label-ink">${label}</tspan></text>
      <title>${esc(nodeTitle(n))}</title></g>`;
  }).join("");

  // The viewBox starts as the whole world, because that is what fitToFrame put the
  // content inside. The world carries the frame's own aspect ratio, so with
  // preserveAspectRatio's default there is nothing to letterbox — the opening
  // picture is the entire landscape, filling the window.
  return { ms, world, svg: `<svg class="mesh-canvas" viewBox="0 0 ${width} ${height}"
    role="img" aria-label="Derived landscape mesh">
    <g class="mesh-edges">${edges}</g>${circles}</svg>` };
}


// impactPanelHTML states the answer in words beside the picture. The counts are the
// point — a highlighted subgraph tells you *which*, a count tells you *how many*,
// and "17 things depend on this worker" is the sentence somebody repeats in a
// change-approval meeting.
function impactPanelHTML(node, result, direction, depth) {
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
      </aside>
    </div>
  </div>`;

  const search = document.getElementById("mesh-search");
  const surface = document.getElementById("mesh-surface");
  const zoomIn = document.getElementById("mesh-zoom-in");
  const zoomOut = document.getElementById("mesh-zoom-out");
  const zoomFit = document.getElementById("mesh-zoom-fit");
  const legendSlot = document.getElementById("mesh-legend-slot");
  const count = document.getElementById("mesh-count");
  const panel = document.getElementById("mesh-panel-slot");
  const dirSelect = document.getElementById("mesh-direction");
  const depthSelect = document.getElementById("mesh-depth");

  let selected = null;
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
    const shown = filterGraph(graph, term);
    // A selection that the filter removed is no longer selected: highlighting a node
    // that is not on screen would leave the panel describing something invisible.
    if (selected && !shown.nodes.some((n) => n.id === selected)) selected = null;

    const direction = dirSelect.value;
    const depth = depthSelect.value === "all" ? Infinity : Number(depthSelect.value);
    const result = selected ? impactFrom(shown, selected, { direction, depth }) : null;
    const highlight = result ? new Set(result.nodes) : null;

    measure();
    const painted = renderGraph(shown, 0, highlight, frame, selected);
    const { ms, svg } = painted;
    world = painted.world;
    surface.innerHTML = shown.nodes.length
      ? svg
      : `<p class="mesh-empty-filter">Nothing matches “${esc(term)}”.</p>`;
    applyView();
    legendSlot.innerHTML = legendHTML(shown, ms);
    panel.innerHTML = impactPanelHTML(
      shown.nodes.find((n) => n.id === selected) || null, result, direction, depth);
    count.textContent = term
      ? `${shown.nodes.length} of ${graph.nodes.length} node(s)`
      : `${graph.nodes.length} node(s), ${graph.edges.length} edge(s)`;
  }

  function select(id) {
    selected = selected === id ? null : id; // clicking the selection again clears it
    paint();
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
    const node = event.target.closest?.(".mesh-node");
    relate(node ? node.dataset.nodeId : null);
  });
  surface.addEventListener("pointerleave", () => relate(null));
  // Keyboard reaches the same answer: the relationships are part of what the view
  // says, not a reward for owning a mouse.
  surface.addEventListener("focusin", (event) => {
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

  // dragged suppresses the click that ends a pan. Panning and selecting share the
  // same surface, and a drag that also selected whatever it started on would make
  // the picture impossible to move without changing the answer beside it.
  let panning = null, dragged = false;
  surface.addEventListener("pointerdown", (event) => {
    if (event.button !== 0) return;
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
  const endPan = () => { panning = null; };
  surface.addEventListener("pointerup", endPan);
  surface.addEventListener("pointercancel", endPan);
  surface.addEventListener("pointerleave", endPan);

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
    else selected = null, paint();
  });
  surface.addEventListener("keydown", (event) => {
    if (event.key !== "Enter" && event.key !== " ") return;
    const node = event.target.closest("[data-node-id]");
    if (!node) return;
    event.preventDefault();
    select(node.getAttribute("data-node-id"));
  });
  dirSelect.addEventListener("change", paint);
  depthSelect.addEventListener("change", paint);

  // Re-laying out on every keystroke is the wrong trade at 400 nodes, where the
  // simulation costs a few hundred milliseconds. A short debounce keeps typing
  // responsive and still feels immediate.
  let pending;
  search.addEventListener("input", () => {
    clearTimeout(pending);
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
