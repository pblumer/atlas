// The interaction graph as a BPMN collaboration (ADR-0559 §8): one pool per
// participant, stacked — who only sends to the process above it, the process in the
// middle, everybody it sends to below — and one message flow per exchange.
//
// A collapsed pool is a participant without a process, which is BPMN's own notation
// for a pool whose inside is not shown. An expanded white box is a participant whose
// process is the deployed model's, copied in with its diagram interchange, its ids
// prefixed so that two processes using the same element id cannot collide, and moved
// into its band. A message flow then docks on the element that sends or receives the
// name, and on the pool's edge where the pool is collapsed or the element is not
// drawn.
//
// The module computes a document and nothing else: no DOM of the page, no network.
// The page renders the document with bpmn-js and keeps the maps it answers to find a
// pool or a flow again.

const BPMN = "http://www.omg.org/spec/BPMN/20100524/MODEL";
const BPMNDI = "http://www.omg.org/spec/BPMN/20100524/DI";
const DC = "http://www.omg.org/spec/DD/20100524/DC";
const DI = "http://www.omg.org/spec/DD/20100524/DI";

// Layout, in diagram units.
export const MIN_WIDTH = 1000;
const LABEL_BAND = 30;   // the band a pool's name stands in
const POOL_H = 60;       // a collapsed pool
const GAP = 90;          // between two pools: room for the flows' labels
const PAD = 30;          // around an expanded process inside its pool

const safe = (s) => String(s).replace(/[^A-Za-z0-9_-]/g, "_");
export const poolIdOf = (participantId) => "Participant_" + safe(participantId);
export const edgeKey = (from, to, message) => `${from}\u0000${to}\u0000${message}`;

// order answers the participants as they are stacked: senders, the center, receivers.
export function order(graph, labelOf) {
  const center = graph.participants.find((p) => p.center);
  const receives = new Set(graph.exchanges.filter((e) => e.from === center.id).map((e) => e.to));
  const above = [], below = [];
  for (const p of graph.participants) {
    if (p.center) continue;
    (receives.has(p.id) ? below : above).push(p);
  }
  const byName = (a, b) => labelOf(a).localeCompare(labelOf(b)) || a.id.localeCompare(b.id);
  above.sort(byName); below.sort(byName);
  return [...above, center, ...below];
}

// extract finds a deployed model's process, the ids under it and its diagram's
// shapes and edges, and how far the drawing reaches. Null when the model has no such
// process or no drawing of it.
export function extract(xml, processId) {
  const doc = new DOMParser().parseFromString(String(xml), "application/xml");
  if (doc.getElementsByTagName("parsererror").length) return null;
  const proc = [...doc.getElementsByTagNameNS(BPMN, "process")].find((p) => p.getAttribute("id") === processId);
  if (!proc) return null;
  // The plane that draws it: its own, or its collaboration's.
  const owners = new Set([processId]);
  for (const part of doc.getElementsByTagNameNS(BPMN, "participant")) {
    if (part.getAttribute("processRef") === processId) {
      const collab = part.parentNode;
      if (collab && collab.getAttribute) owners.add(collab.getAttribute("id"));
    }
  }
  const plane = [...doc.getElementsByTagNameNS(BPMNDI, "BPMNPlane")].find((p) => owners.has(p.getAttribute("bpmnElement")));
  if (!plane) return null;
  const ids = new Set();
  for (const el of [proc, ...proc.getElementsByTagName("*")]) {
    const id = el.getAttribute && el.getAttribute("id");
    if (id) ids.add(id);
  }
  // Root elements the process refers to — messages, signals, errors, escalations —
  // come along, so its events keep their definitions.
  const roots = [];
  for (const name of ["message", "signal", "error", "escalation"]) {
    for (const el of doc.getElementsByTagNameNS(BPMN, name)) {
      if (el.parentNode === doc.documentElement) { roots.push(el); ids.add(el.getAttribute("id")); }
    }
  }
  const drawn = [...plane.children].filter((d) => ids.has(d.getAttribute("bpmnElement")));
  // The drawing's own ids (start_di, its label's) are prefixed with the rest: two
  // expanded processes drawn from models that share element ids share these too.
  for (const d of drawn) {
    for (const el of [d, ...d.getElementsByTagName("*")]) {
      const id = el.getAttribute("id");
      if (id) ids.add(id);
    }
  }
  if (!drawn.some((d) => d.localName === "BPMNShape")) return null;
  let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
  for (const d of drawn) {
    for (const b of d.getElementsByTagNameNS(DC, "Bounds")) {
      const x = +b.getAttribute("x"), y = +b.getAttribute("y"), w = +b.getAttribute("width"), h = +b.getAttribute("height");
      minX = Math.min(minX, x); minY = Math.min(minY, y); maxX = Math.max(maxX, x + w); maxY = Math.max(maxY, y + h);
    }
    for (const p of d.getElementsByTagNameNS(DI, "waypoint")) {
      const x = +p.getAttribute("x"), y = +p.getAttribute("y");
      minX = Math.min(minX, x); minY = Math.min(minY, y); maxX = Math.max(maxX, x); maxY = Math.max(maxY, y);
    }
  }
  return { proc, roots, drawn, ids, box: { minX, minY, width: maxX - minX, height: maxY - minY } };
}

// prefixed rewrites every id of the copied nodes, and every reference to one —
// attributes and the text of reference elements (incoming, outgoing, flowNodeRef,
// sourceRef, targetRef) — so two processes using the same ids live side by side.
function prefixed(nodes, ids, prefix) {
  const fix = (v) => (ids.has(v) ? prefix + v : v);
  for (const root of nodes) {
    for (const el of [root, ...root.getElementsByTagName("*")]) {
      for (const a of [...el.attributes]) {
        if (a.name === "id" || ids.has(a.value)) el.setAttribute(a.name, fix(a.value));
      }
      if (!el.children.length && el.textContent && ids.has(el.textContent.trim())) {
        el.textContent = fix(el.textContent.trim());
      }
    }
  }
}

function shift(nodes, dx, dy) {
  for (const root of nodes) {
    for (const b of root.getElementsByTagNameNS(DC, "Bounds")) {
      b.setAttribute("x", +b.getAttribute("x") + dx);
      b.setAttribute("y", +b.getAttribute("y") + dy);
    }
    for (const p of root.getElementsByTagNameNS(DI, "waypoint")) {
      p.setAttribute("x", +p.getAttribute("x") + dx);
      p.setAttribute("y", +p.getAttribute("y") + dy);
    }
  }
}

// build answers the collaboration: { xml, pools, flows, skipped }.
//   graph     the route's answer
//   labelOf   a participant's name on the page
//   flowName  an exchange's caption
//   expanded  Map participantId → deployed model XML, for each expanded white box
//   portsOf   participant → its message ports, or undefined
// pools maps a participant id to its pool's id and bounds; flows maps an exchange's
// key to its message flow's id; skipped lists the exchanges a collaboration cannot
// draw — a process sending to itself has no message flow, which joins two pools.
export function build({ graph, labelOf, flowName, expanded = new Map(), portsOf = () => undefined }) {
  const doc = document.implementation.createDocument(BPMN, "bpmn:definitions", null);
  const defs = doc.documentElement;
  defs.setAttribute("xmlns:bpmndi", BPMNDI);
  defs.setAttribute("xmlns:dc", DC);
  defs.setAttribute("xmlns:di", DI);
  defs.setAttribute("id", "Definitions_interactions");
  defs.setAttribute("targetNamespace", "http://atlas/interactions");
  const el = (ns, name, attrs = {}) => {
    const e = doc.createElementNS(ns, name);
    for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, String(v));
    return e;
  };
  const collab = el(BPMN, "bpmn:collaboration", { id: "Collaboration_interactions" });
  defs.appendChild(collab);
  const diagram = el(BPMNDI, "bpmndi:BPMNDiagram", { id: "Diagram_interactions" });
  const plane = el(BPMNDI, "bpmndi:BPMNPlane", { id: "Plane_interactions", bpmnElement: "Collaboration_interactions" });
  diagram.appendChild(plane);

  const stacked = order(graph, labelOf);
  const copies = new Map();
  stacked.forEach((p, i) => {
    if (p.kind !== "process" || !expanded.has(p.id)) return;
    const x = extract(expanded.get(p.id), p.processId);
    if (x) copies.set(p.id, { ...x, prefix: `x${i}_` });
  });
  let width = MIN_WIDTH;
  for (const c of copies.values()) width = Math.max(width, LABEL_BAND + 2 * PAD + c.box.width);

  // Pools.
  const pools = new Map();
  let y = 0;
  stacked.forEach((p) => {
    const c = copies.get(p.id);
    const h = c ? c.box.height + 2 * PAD : POOL_H;
    const id = poolIdOf(p.id);
    const part = el(BPMN, "bpmn:participant", { id, name: labelOf(p) });
    collab.appendChild(part);
    if (c) {
      const procId = c.prefix + c.proc.getAttribute("id");
      const nodes = [c.proc, ...c.roots, ...c.drawn].map((n) => doc.importNode(n, true));
      prefixed(nodes, c.ids, c.prefix);
      const [proc, ...rest] = nodes;
      const roots = rest.slice(0, c.roots.length), drawn = rest.slice(c.roots.length);
      proc.setAttribute("isExecutable", "false");
      part.setAttribute("processRef", procId);
      for (const r of roots) defs.insertBefore(r, collab);
      defs.insertBefore(proc, collab);
      shift(drawn, LABEL_BAND + PAD - c.box.minX, y + PAD - c.box.minY);
      for (const d of drawn) plane.appendChild(d);
    }
    const shape = el(BPMNDI, "bpmndi:BPMNShape", { id: id + "_di", bpmnElement: id, isHorizontal: "true" });
    if (!c) shape.setAttribute("isExpanded", "false");
    shape.appendChild(el(DC, "dc:Bounds", { x: 0, y, width, height: h }));
    plane.insertBefore(shape, plane.firstChild);
    pools.set(p.id, { id, x: 0, y, width, height: h, prefix: c ? c.prefix : null, drawnIds: c ? new Set(c.ids) : null });
    y += h + GAP;
  });

  // Where a flow docks on a participant: the element behind the message where the
  // pool shows it, else the pool's edge.
  const anchor = (p, message, out) => {
    const pool = pools.get(p.id);
    if (!pool.prefix) return null;
    const port = (portsOf(p) || []).find((pt) => pt.message === message && (pt.direction === "out") === out);
    if (!port) return null;
    for (const elementId of port.elements) {
      if (!pool.drawnIds.has(elementId)) continue;
      const di = [...plane.children].find((d) => d.getAttribute("bpmnElement") === pool.prefix + elementId && d.localName === "BPMNShape");
      if (!di) continue;
      const b = di.getElementsByTagNameNS(DC, "Bounds")[0];
      return { id: pool.prefix + elementId, x: +b.getAttribute("x"), y: +b.getAttribute("y"), w: +b.getAttribute("width"), h: +b.getAttribute("height") };
    }
    return null;
  };

  // Flows: one column each across the width, so no two run on one line.
  const byId = new Map(graph.participants.map((p) => [p.id, p]));
  const drawable = graph.exchanges.filter((e) => e.from !== e.to && pools.has(e.from) && pools.has(e.to));
  const skipped = graph.exchanges.filter((e) => !drawable.includes(e));
  const flows = new Map();
  const n = drawable.length;
  const centerPool = pools.get(stacked.find((p) => p.center).id);
  // First where each flow starts and ends, then, per gap beside the center's pool, a
  // height of its own for each flow's bend, ordered from left to right, so no two
  // flows share a horizontal stretch or a caption's line.
  const placed = drawable.map((e, i) => {
    const from = byId.get(e.from), to = byId.get(e.to);
    const sp = pools.get(e.from), tp = pools.get(e.to);
    const down = sp.y < tp.y;
    const sa = anchor(from, e.message, true), ta = anchor(to, e.message, false);
    const column = n > 1 ? LABEL_BAND + 60 + i * ((width - LABEL_BAND - 120) / (n - 1)) : width / 2;
    const sx = sa ? sa.x + sa.w / 2 : (ta ? ta.x + ta.w / 2 : column);
    const tx = ta ? ta.x + ta.w / 2 : sx;
    const sy = sa ? (down ? sa.y + sa.h : sa.y) : (down ? sp.y + sp.height : sp.y);
    const ty = ta ? (down ? ta.y : ta.y + ta.h) : (down ? tp.y : tp.y + tp.height);
    // The gap a flow bends in: above the center for a flow from a pool above, below
    // it for a flow to or from a pool below.
    const above = down ? tp === centerPool : sp === centerPool;
    return { e, i, sp, tp, sa, ta, sx, tx, sy, ty, above };
  });
  for (const above of [true, false]) {
    const inGap = placed.filter((f) => f.above === above).sort((a, b) => Math.min(a.sx, a.tx) - Math.min(b.sx, b.tx));
    const top = above ? centerPool.y - GAP : centerPool.y + centerPool.height;
    const stepY = Math.min(16, (GAP - 30) / Math.max(inGap.length - 1, 1));
    inGap.forEach((f, lane) => { f.bendY = top + 15 + lane * stepY; });
  }
  for (const f of placed) {
    const { e, i, sp, tp, sa, ta, sx, tx, sy, ty, bendY } = f;
    const id = `MessageFlow_${i + 1}`;
    collab.appendChild(el(BPMN, "bpmn:messageFlow", {
      id, name: flowName(e), sourceRef: sa ? sa.id : sp.id, targetRef: ta ? ta.id : tp.id,
    }));
    const edge = el(BPMNDI, "bpmndi:BPMNEdge", { id: id + "_di", bpmnElement: id });
    const points = sx === tx ? [[sx, sy], [tx, ty]] : [[sx, sy], [sx, bendY], [tx, bendY], [tx, ty]];
    for (const [px, py] of points) edge.appendChild(el(DI, "di:waypoint", { x: px, y: py }));
    const label = el(BPMNDI, "bpmndi:BPMNLabel");
    // Beside the line where it leaves its bend toward the target, on the side with
    // room: left of it in the right half.
    const lx = tx > width / 2 ? tx - 146 : tx + 6;
    label.appendChild(el(DC, "dc:Bounds", { x: lx, y: bendY - 15, width: 140, height: 14 }));
    edge.appendChild(label);
    plane.appendChild(edge);
    flows.set(edgeKey(e.from, e.to, e.message), id);
  }
  defs.appendChild(diagram);
  return { xml: new XMLSerializer().serializeToString(doc), pools, flows, skipped, height: y - GAP, width };
}
