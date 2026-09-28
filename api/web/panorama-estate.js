// The estate altitude (ADR-0402): one node per domain — this runtime
// and every configured deployment target — joined where a promotion recorded a join.
//
// **Its own view rather than a third subject on the Starmap, deliberately and for now.**
// ADR-0402 §5 says the estate is an extension of the existing altitudes, and that is still
// where it ends up: the picker is where a reader switches altitude, and the Starmap's
// renderer is the one that carries provenance, severity, drill-down and the export. What
// this file is, is the step before that — the new situation drawn on its own, so the shipped
// landscape is not touched while the altitude is still being learned. Integration is a
// separate change, made once this has been read against a real estate, and it is where this
// module's decisions get held against the renderer's.
//
// What that buys, concretely: the Starmap keeps one subject, one request shape and one set of
// controls, and a defect in the estate cannot take the landscape with it. What it costs is
// stated rather than hidden — this picture has no filter, no saved views, no notation
// projection, no ArchiMate export and no drill-down, because those belong to the renderer it
// is not using yet.
//
// **The layout is a star, and that is the topology rather than a choice.** Every edge the
// record admits is a promotion *from here to there* (§4), so the reader's own domain is the
// centre and the peers stand around it. No simulation is needed, nothing overlaps by
// construction, and two reads of one estate come out identically — the server already orders
// the peers, and a ring over that order is stable.
//
// **Every domain says how wide the credential that drew it was** (§1). That is not decoration
// on this picture; it is what makes the picture readable at all. A domain's count is as wide
// as the credential that fetched it — the reader's own rights on the domain they are standing
// in, the stored credential of each peer — so the numbers are not comparable across domains,
// and both the credential and the part it could not see are written on every row.

// shapeVertices is imported rather than copied: the polygon a pentagon is, at a radius, is
// geometry the Starmap already states once. Importing a pure function is not touching the
// view — nothing in that module changes, and nothing here depends on its state.
import { shapeVertices } from "./panorama-mesh.js";

const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;" }[c]));

// The observation states a domain can be in, in a reader's words. Five, because ADR-0402 §3
// adds one to ADR-0189 §6's four and the addition is the point: a peer that answered and does
// not serve this read is a version boundary, not a fault. Reading it as unreachable sends an
// operator to look at a network; reading it as stale implies there was once an answer.
const STATE = {
  healthy: { text: "answered", glyph: "", tone: "ok" },
  stale: { text: "history — the refresh failed", glyph: "•", tone: "attention" },
  unreachable: { text: "not reached", glyph: "•", tone: "attention" },
  unserved: { text: "answered — does not serve this view", glyph: "?", tone: "unknown" },
  unbound: { text: "not asked", glyph: "?", tone: "unknown" },
};
const stateOf = (node) => STATE[node.state] || { text: node.state || "unknown", glyph: "?", tone: "unknown" };

// The ring: how far the peers stand from the centre, and how large a domain is drawn.
//
// One size for every domain, and the number inside it carries the size of its landscape
// instead. Drawing the node itself larger for a larger landscape was the first idea and it is
// wrong for the reason §1 gives: a domain's count is as wide as the credential that drew it,
// so a radius would make a narrow credential look like a small installation — the one reading
// this picture must not invite.
const NODE_R = 46;
const RING_R = 190;
const PAD = NODE_R + 28;

// place puts the local domain in the centre and the peers on a ring, clockwise from the top.
// The order is the server's (peers sorted by id), so the arrangement is stable between reads.
function place(nodes) {
  const peers = nodes.slice(1);
  const at = new Map([[nodes[0]?.id, { x: 0, y: 0 }]]);
  peers.forEach((node, i) => {
    // A single peer sits above the centre rather than beside it, so the one line on the
    // picture reads as the vertical it is.
    const angle = -Math.PI / 2 + (i * 2 * Math.PI) / Math.max(peers.length, 1);
    at.set(node.id, { x: Math.cos(angle) * RING_R, y: Math.sin(angle) * RING_R });
  });
  return at;
}

// sentence is what a domain says about itself, in the order a reader needs it: which
// installation, how big its landscape, how much of it the credential missed, whose credential
// that was, and then the state and the reason the collector resolved.
function sentence(node) {
  const said = [node.name || node.id];
  if (node.holds) said.push(`${node.holds} node(s) in its own landscape`);
  if (node.restricted) said.push(`${node.restricted} outside that credential's reach`);
  said.push(node.drawnBy
    ? `drawn with the credential configured for ${node.drawnBy}`
    : "drawn with your own rights");
  said.push(stateOf(node).text);
  if (node.reason) said.push(node.reason);
  return said.join(" · ");
}

// polygon is one domain's outline, at this node's centre.
function polygon(x, y) {
  return shapeVertices("pentagon", NODE_R)
    .map(([dx, dy]) => `${(x + dx).toFixed(1)},${(y + dy).toFixed(1)}`).join(" ");
}

// edgeLine trims a line to the two outlines it joins, so an arrow does not disappear under a
// node and a count does not sit on top of one.
function edgeLine(from, to) {
  const dx = to.x - from.x;
  const dy = to.y - from.y;
  const len = Math.hypot(dx, dy) || 1;
  const trim = NODE_R + 4;
  return {
    x1: from.x + (dx / len) * trim, y1: from.y + (dy / len) * trim,
    x2: to.x - (dx / len) * trim, y2: to.y - (dy / len) * trim,
    mx: from.x + dx / 2, my: from.y + dy / 2,
  };
}

// draw renders the whole picture. One function rather than a class, because nothing here has
// state: the graph arrives, the picture is a function of it, and a refresh redraws.
function draw(graph) {
  const nodes = graph.nodes || [];
  if (!nodes.length) return `<p class="muted">This estate has no domains, which cannot happen:
    the domain you are reading from is always one of them.</p>`;

  const at = place(nodes);
  const reach = Math.max(RING_R + PAD, PAD * 2);
  const box = `${-reach} ${-reach} ${reach * 2} ${reach * 2}`;

  const edges = (graph.edges || []).map((e) => {
    const from = at.get(e.from);
    const to = at.get(e.to);
    if (!from || !to) return "";
    const line = edgeLine(from, to);
    const label = e.promoted
      ? `<text class="estate-edge-count" x="${line.mx.toFixed(1)}" y="${line.my.toFixed(1)}"
           text-anchor="middle" dy="-4">${e.promoted}</text>`
      : "";
    return `<g class="estate-edge">
      <line x1="${line.x1.toFixed(1)}" y1="${line.y1.toFixed(1)}"
            x2="${line.x2.toFixed(1)}" y2="${line.y2.toFixed(1)}"/>
      ${label}
      <title>${esc(e.promoted
        ? `${e.promoted} application(s) were promoted along this join. It says a promotion happened, never that they are still deployed there.`
        : "A promotion recorded between these two domains.")}</title>
    </g>`;
  }).join("");

  const drawn = nodes.map((node) => {
    const pos = at.get(node.id) || { x: 0, y: 0 };
    const state = stateOf(node);
    const badge = state.glyph
      ? `<g class="estate-badge" transform="translate(${(NODE_R * 0.66).toFixed(1)},${(-NODE_R * 0.66).toFixed(1)})">
           <circle r="9"/><text text-anchor="middle" dy="4">${esc(state.glyph)}</text></g>`
      : "";
    return `<g class="estate-node" data-domain-id="${esc(node.id)}" data-tone="${esc(state.tone)}"
      data-local="${node.drawnBy ? "false" : "true"}" tabindex="0" role="img"
      aria-label="${esc(sentence(node))}" transform="translate(${pos.x.toFixed(1)},${pos.y.toFixed(1)})">
      <polygon points="${polygon(0, 0)}"/>
      ${node.holds ? `<text class="estate-holds" text-anchor="middle" dy="4">${node.holds}</text>` : ""}
      <text class="estate-name" text-anchor="middle" y="${(NODE_R + 18).toFixed(0)}">${esc(node.name || node.id)}</text>
      ${badge}
      <title>${esc(sentence(node))}</title>
    </g>`;
  }).join("");

  return `<svg class="estate-canvas" viewBox="${box}" role="group"
    aria-label="The estate: ${nodes.length} domain(s)">${edges}${drawn}</svg>`;
}

// rows is the same estate read as a table, and it is not a fallback for the picture. Four of
// the five facts a domain carries are words rather than shapes — the credential, what it could
// not see, the state and the reason — and a picture can only put those behind a hover. This is
// where they are read, sorted the way the server ordered them so the two agree.
function rows(graph) {
  const row = (node) => {
    const state = stateOf(node);
    return `<tr data-domain-id="${esc(node.id)}">
      <td><b>${esc(node.name || node.id)}</b>
        ${node.drawnBy ? "" : `<span class="chip">you are here</span>`}
        ${node.runtimeId ? `<div class="muted estate-runtime"><code>${esc(node.runtimeId)}</code></div>` : ""}</td>
      <td class="estate-num">${node.holds ? node.holds : `<span class="muted">—</span>`}</td>
      <td class="estate-num">${node.restricted
        ? `<span title="Outside the reach of the credential that drew this domain">${node.restricted}</span>`
        : `<span class="muted">—</span>`}</td>
      <td>${node.drawnBy
        ? esc(node.drawnBy)
        : `<span class="muted">your own rights</span>`}</td>
      <td data-tone="${esc(state.tone)}">${esc(state.text)}
        ${node.reason ? `<div class="muted estate-reason">${esc(node.reason)}</div>` : ""}</td>
    </tr>`;
  };
  return `<table class="estate-table">
    <thead><tr>
      <th>Domain</th>
      <th class="estate-num" title="How many nodes that domain's own landscape holds">Holds</th>
      <th class="estate-num" title="How many of those the credential that drew it could not see">Unseen</th>
      <th>Drawn with</th><th>State</th>
    </tr></thead>
    <tbody>${(graph.nodes || []).map(row).join("")}</tbody>
  </table>`;
}

// legend states the two things this picture cannot be read without: what the one line means,
// and that a domain's size is as wide as the credential that drew it (§1). The second is the
// disclosure the record requires, and it is deliberately not merged with the Starmap's own
// restricted note — that one says what *you* may not see here, this one says what somebody
// else's credential could not see there, and a sum of the two answers neither question.
function legend(graph) {
  const unseen = (graph.nodes || []).reduce((sum, node) => sum + (node.restricted || 0), 0);
  const joins = (graph.edges || []).length;
  return `<div class="estate-legend">
    <p><b>Each domain is as wide as the credential that drew it.</b> Your own rights on the
      domain you are standing in, and the credential configured for each peer — named on every
      row. ${unseen > 0
        ? `<b>${unseen}</b> node(s) across the peer domains were outside those credentials' reach.`
        : ""} A domain's number is what its own landscape holds, so the numbers are
      <b>not comparable across domains</b>: a small one may be a small installation or a narrow
      credential, and only the credential named beside it can tell you which.</p>
    <p class="muted">${joins === 1 ? "The one line is" : `The ${joins} lines are`} a promotion
      somebody recorded, with how many applications travelled it. It says a promotion
      <i>happened</i> — never that the application is still deployed over there, which nothing
      on this side can know. Nothing else is drawn: a message flow somebody sketched is not a
      fact here, and what a peer promotes onward is not known here.</p>
    <p class="muted">Nothing of a peer's landscape crosses. A domain carries a name, a count, a
      state and a join; opening one is a read against that installation, where that reader's own
      rights are the only ones that can be resolved.</p>
  </div>`;
}

export async function mountPanoramaEstate(root, { api, toast }) {
  root.innerHTML = `<div class="estate-root">
    <div class="between">
      <div>
        <h1>Estate</h1>
        <p class="muted" style="margin:0">One node per domain — this installation and every
          deployment target configured on it — joined where a promotion recorded a join. An
          altitude above the landscape: a domain stands for a whole starmap rather than for one
          thing on it.</p>
      </div>
      <div class="estate-actions">
        <button class="btn neutral" id="estate-refresh">Read again</button>
        <a class="btn neutral" href="#/panorama/starmap">This starmap →</a>
      </div>
    </div>
    <div id="estate-body"><p class="muted" style="margin-top:16px">Asking every configured
      peer for its landscape…</p></div>
  </div>`;

  const body = root.querySelector("#estate-body");

  async function read() {
    let graph;
    try {
      graph = await api("GET", "/api/v1/panorama/estate");
    } catch (e) {
      // The whole answer or none: a partial estate is a picture of a smaller estate, and
      // nothing on it would say so. The server refuses for the same reason.
      body.innerHTML = `<div class="card empty"><p>${esc(e.message)}</p></div>`;
      return;
    }
    if (!root.isConnected) return; // navigated away while the peers were being asked
    body.innerHTML = `<div class="estate-picture card">${draw(graph)}</div>
      ${legend(graph)}
      <div class="card estate-rows">${rows(graph)}</div>`;

    // Going into the domain you are standing in is the landscape that already exists. A peer
    // has no such link, and inventing one would promise a picture this server cannot draw.
    for (const node of body.querySelectorAll('.estate-node[data-local="true"]')) {
      node.classList.add("estate-open");
      const enter = () => { window.location.hash = "#/panorama/starmap"; };
      node.addEventListener("dblclick", enter);
      node.addEventListener("keydown", (event) => {
        if (event.key === "Enter") enter();
      });
    }
  }

  root.querySelector("#estate-refresh").addEventListener("click", async (event) => {
    const button = event.currentTarget;
    button.disabled = true;
    try {
      await read();
    } catch (e) {
      toast?.(e.message, "err");
    } finally {
      button.disabled = false;
    }
  });

  await read();
}
