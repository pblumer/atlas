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
const KIND = {
  application: { r: 26, fill: "var(--accent-soft)", stroke: "var(--accent)", label: "Application" },
  process: { r: 18, fill: "var(--card)", stroke: "var(--border-strong)", label: "Process" },
  // --ok is a fixed green rather than a shade of the configurable accent, so its
  // soft companion is a literal here too. There is no --ok-soft at :root, and
  // defining one would change the one other rule that already asks for it.
  worker: { r: 15, fill: "#e8f5ec", stroke: "var(--ok)", label: "Worker" },
  decision: { r: 15, fill: "var(--accent-soft)", stroke: "var(--accent-hover)", label: "Decision" },
  restricted: { r: 14, fill: "var(--bg)", stroke: "var(--muted)", label: "Restricted — outside your access", dashed: true },
  unresolved: { r: 14, fill: "var(--warn-soft)", stroke: "var(--warn)", label: "Unresolved — nothing here provides it", dashed: true },
};

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

// layout settles the graph with repulsion between every pair, springs along edges,
// and a pull toward the centre. Returns the elapsed milliseconds so the caller can
// report what the budget actually costs on this machine.
function layout(nodes, edges, { width, height, iterations = 220 } = {}) {
  const started = performance.now();
  const random = mulberry32(0x5EED);
  const index = new Map(nodes.map((n, i) => [n.id, i]));
  const cx = width / 2, cy = height / 2;

  for (const n of nodes) {
    // Seeded scatter on a ring, so nothing starts coincident (which would make the
    // repulsion term divide by zero and fling nodes to infinity).
    const angle = random() * Math.PI * 2;
    const radius = 40 + random() * Math.min(width, height) * 0.35;
    n.x = cx + Math.cos(angle) * radius;
    n.y = cy + Math.sin(angle) * radius;
    n.vx = 0; n.vy = 0;
  }

  const links = edges
    .map((e) => [index.get(e.from), index.get(e.to)])
    .filter(([a, b]) => a !== undefined && b !== undefined);

  const repulsion = 5200, spring = 0.012, rest = 110, damping = 0.85;
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
      n.vx += (cx - n.x) * 0.0012;
      n.vy += (cy - n.y) * 0.0012;
      n.vx *= damping; n.vy *= damping;
      n.x += n.vx; n.y += n.vy;
    }
  }
  return performance.now() - started;
}

// viewBoxFor frames the settled graph with a margin, so the whole mesh is visible
// without the caller guessing a zoom level.
function viewBoxFor(nodes, width, height) {
  if (!nodes.length) return `0 0 ${width} ${height}`;
  const xs = nodes.map((n) => n.x), ys = nodes.map((n) => n.y);
  const pad = 70;
  const minX = Math.min(...xs) - pad, maxX = Math.max(...xs) + pad;
  const minY = Math.min(...ys) - pad, maxY = Math.max(...ys) + pad;
  return `${minX} ${minY} ${Math.max(maxX - minX, 1)} ${Math.max(maxY - minY, 1)}`;
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
  if (node.processId) parts.push(`${node.processId} v${node.version}`);
  if (node.workerType) parts.push(`${node.workerType} worker`);
  if (node.children) parts.push(`${node.children} process(es) collapsed`);
  return parts.join(" · ");
}

// matches decides what a search term keeps. It reads the name, the kind, and a
// process's BPMN id — the three things somebody actually types — and never the
// node id, whose prefixes would make every term match its own kind by accident.
function matches(node, term) {
  if (!term) return true;
  const hay = [node.name, node.kind, node.processId, node.workerType]
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
  return `<div class="mesh-legend">
    <div class="mesh-swatches">${swatches}</div>
    <div class="mesh-meta">Everything here is <b>derived</b> from this server's
      resources — nothing on this view was drawn.
      <span class="muted">Laid out in ${Math.round(layoutMs)} ms.</span></div>
    ${notes.join("")}
  </div>`;
}

function renderGraph(graph, layoutMs) {
  const width = 1200, height = 720;
  const nodes = graph.nodes.map((n) => ({ ...n }));
  const ms = layout(nodes, graph.edges, { width, height }) + layoutMs;
  const at = new Map(nodes.map((n) => [n.id, n]));

  const edges = graph.edges.map((e) => {
    const a = at.get(e.from), b = at.get(e.to);
    if (!a || !b) return "";
    const dashed = e.kind === "contains";
    return `<line x1="${a.x.toFixed(1)}" y1="${a.y.toFixed(1)}"
      x2="${b.x.toFixed(1)}" y2="${b.y.toFixed(1)}"
      class="mesh-edge${dashed ? " mesh-edge-contains" : ""}"/>`;
  }).join("");

  const circles = nodes.map((n) => {
    const style = KIND[n.kind] || KIND.process;
    const href = hrefFor(n);
    const label = n.kind === "restricted" ? "" : esc(n.name || "");
    const shape = `<circle r="${style.r}" fill="${style.fill}" stroke="${style.stroke}"
        stroke-width="2" ${style.dashed ? 'stroke-dasharray="4 3"' : ""}/>
      ${n.children ? `<text class="mesh-count" text-anchor="middle" dy="4">${n.children}</text>` : ""}
      <text class="mesh-label" text-anchor="middle" dy="${style.r + 14}">${label}</text>
      <title>${esc(nodeTitle(n))}</title>`;
    const body = `<g transform="translate(${n.x.toFixed(1)},${n.y.toFixed(1)})"
      class="mesh-node mesh-${n.kind}">${shape}</g>`;
    return href ? `<a href="${href}">${body}</a>` : body;
  }).join("");

  return { ms, svg: `<svg class="mesh-canvas" viewBox="${viewBoxFor(nodes, width, height)}"
    role="img" aria-label="Derived landscape mesh">
    <g class="mesh-edges">${edges}</g>${circles}</svg>` };
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
    <div id="mesh-surface" class="mesh-surface"></div>
  </div>`;

  const search = document.getElementById("mesh-search");
  const surface = document.getElementById("mesh-surface");
  const legendSlot = document.getElementById("mesh-legend-slot");
  const count = document.getElementById("mesh-count");

  function paint() {
    const term = search.value.trim().toLowerCase();
    const shown = filterGraph(graph, term);
    const { ms, svg } = renderGraph(shown, 0);
    surface.innerHTML = shown.nodes.length
      ? svg
      : `<p class="mesh-empty-filter">Nothing matches “${esc(term)}”.</p>`;
    legendSlot.innerHTML = legendHTML(shown, ms);
    count.textContent = term
      ? `${shown.nodes.length} of ${graph.nodes.length} node(s)`
      : `${graph.nodes.length} node(s), ${graph.edges.length} edge(s)`;
  }

  // Re-laying out on every keystroke is the wrong trade at 400 nodes, where the
  // simulation costs a few hundred milliseconds. A short debounce keeps typing
  // responsive and still feels immediate.
  let pending;
  search.addEventListener("input", () => {
    clearTimeout(pending);
    pending = setTimeout(paint, 120);
  });
  paint();

  if (fetchMs > 2000) toast(`The landscape took ${Math.round(fetchMs)} ms to derive.`);
}
