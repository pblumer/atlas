// decision-graph.js — a decision drawn, and one case drawn on top of it.
//
// A business rule task hands a case to DMN and gets an answer back. The durable
// record of that (ADR-0066) already said what went in, what came out, and which
// rules fired; what it could not do was *show* it. The rules arrived as a table on
// hover, detached from the decision they belong to, and the decision requirements
// graph — the picture every DMN author works in — was nowhere near the case that
// ran through it.
//
// So: double-click the task and the graph opens with the case on it. Each input
// datum carries the value it was given, each decision the value it produced, and
// the rule matrices sit underneath with the rules that fired marked green. The
// point is a screen an operator can turn towards somebody from the business and
// have them read it without being taught anything first.
//
// Two rules keep the picture honest, because a decision record is evidence:
//
//   - **The model is the one that ran.** The server resolves the graph from the
//     process definition the record carries, not from the design-time model as it
//     reads today, so a decision re-modelled since does not redraw an old case.
//   - **Nothing is inferred onto a node.** A value is shown where it can be tied to
//     a node by name — the recorded inputs, the outputs, and the values the trace
//     says each table's input columns evaluated to. A decision the record cannot
//     speak for is drawn plainly, as unknown, and says so. A decision service
//     records no rule trace at all (ADR-0398), and that is stated rather than
//     rendered as an absence of rules.

import { renderTraceTable, tablesOf as traceTablesOf, fmtVal as traceValue } from "./dmn-trace.js";

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// Node geometry for a model with no diagram of its own. The server completes most
// models before it freezes their graph (ADR-0325), so this is the fallback for the
// ones it could not.
const NW = 168, NH = 64, GAPX = 36, GAPY = 60, PAD = 24;

// borderPoint returns the point on a box's border (centre cx,cy, size w×h) in the
// direction of (tx,ty), so a requirement arrow lands on the box edge, not its
// centre.
export function borderPoint(cx, cy, w, h, tx, ty) {
  const dx = tx - cx, dy = ty - cy;
  if (dx === 0 && dy === 0) return [cx, cy];
  const sx = dx !== 0 ? (w / 2) / Math.abs(dx) : Infinity;
  const sy = dy !== 0 ? (h / 2) / Math.abs(dy) : Infinity;
  const s = Math.min(sx, sy);
  return [cx + dx * s, cy + dy * s];
}

// placeDrg positions a graph's nodes: the authored DMNDI bounds when the model
// carries a diagram, otherwise layers — what a decision requires sits below it, and
// what shares a layer is spread sideways, the same shape the server's generator
// uses so a model is drawn the same way wherever its picture is built.
export function placeDrg(g) {
  const nodes = g.nodes || [];
  const hasDI = nodes.some((n) => n.width > 0);
  if (hasDI) return nodes.map((n) => ({ n, x: n.x || 0, y: n.y || 0, w: n.width || NW, h: n.height || NH }));
  const reqs = {};
  (g.edges || []).forEach((e) => { (reqs[e.target] = reqs[e.target] || []).push(e.source); });
  const level = {};
  const lvl = (id, seen) => {
    if (level[id] != null) return level[id];
    if (seen.has(id)) return 0;
    seen.add(id);
    const rs = reqs[id] || [];
    return (level[id] = rs.length ? 1 + Math.max(...rs.map((r) => lvl(r, seen))) : 0);
  };
  nodes.forEach((n) => lvl(n.id, new Set()));
  const maxL = Math.max(0, ...Object.values(level));
  const byLevel = {};
  nodes.forEach((n) => { (byLevel[level[n.id]] = byLevel[level[n.id]] || []).push(n); });
  const placed = [];
  for (let L = 0; L <= maxL; L++) {
    (byLevel[L] || []).forEach((n, i) =>
      placed.push({ n, x: PAD + i * (NW + GAPX), y: PAD + (maxL - L) * (NH + GAPY), w: NW, h: NH }));
  }
  return placed;
}

// drgFrame computes the viewBox a placement needs, so the caller can wrap whatever
// it drew in an SVG of the right size.
function drgFrame(placed) {
  const minX = Math.min(...placed.map((p) => p.x)) - PAD;
  const minY = Math.min(...placed.map((p) => p.y)) - PAD;
  return {
    minX, minY,
    W: Math.max(...placed.map((p) => p.x + p.w)) + PAD - minX,
    H: Math.max(...placed.map((p) => p.y + p.h)) + PAD - minY,
  };
}

// drgEdges draws the requirement arrows between placed nodes.
function drgEdges(g, pos) {
  return (g.edges || []).map((e) => {
    const a = pos[e.source], b = pos[e.target];
    if (!a || !b) return "";
    const ax = a.x + a.w / 2, ay = a.y + a.h / 2, bx = b.x + b.w / 2, by = b.y + b.h / 2;
    const [x1, y1] = borderPoint(ax, ay, a.w, a.h, bx, by);
    const [x2, y2] = borderPoint(bx, by, b.w, b.h, ax, ay);
    const dash = e.type === "knowledgeRequirement" ? ` stroke-dasharray="5 4"` : "";
    return `<line x1="${x1.toFixed(1)}" y1="${y1.toFixed(1)}" x2="${x2.toFixed(1)}" y2="${y2.toFixed(1)}" stroke="#94a3b8" stroke-width="1.5"${dash} marker-end="url(#drg-arrow)"/>`;
  }).join("");
}

// The shape each kind of node is drawn as, per the DRD notation (DMN 1.5 §5.3.3,
// Table 5-2). The shape carries the meaning here, so it is not a style choice:
//
//   - a decision is a PLAIN rectangle. Rounding its corners makes it read as an
//     input datum or a decision service, which are the two things it is not;
//   - input data is a stadium — a rectangle with fully rounded ends;
//   - a business knowledge model is a rectangle with its top-left and bottom-right
//     corners cut off.
//
// The cut is proportional to the box so a small node does not lose its corners
// entirely, and capped so a large one keeps the notch the notation shows.
function nodeShape(type, x, y, w, h, attrs) {
  if (type === "inputData") {
    return `<rect x="${x}" y="${y}" width="${w}" height="${h}" rx="${h / 2}" ${attrs}/>`;
  }
  if (type === "businessKnowledgeModel") {
    const c = Math.min(14, w * 0.11, h * 0.29);
    const pts = [
      [x, y + h], [x + c, y], [x + w, y], [x + w - c, y + h],
    ].map(([px, py]) => `${px.toFixed(1)},${py.toFixed(1)}`).join(" ");
    return `<polygon points="${pts}" ${attrs}/>`;
  }
  return `<rect x="${x}" y="${y}" width="${w}" height="${h}" ${attrs}/>`;
}

// drgSvg wraps drawn edges and nodes in the frame their placement needs.
function drgSvg(placed, inner) {
  const { minX, minY, W, H } = drgFrame(placed);
  return `<svg viewBox="${minX.toFixed(0)} ${minY.toFixed(0)} ${W.toFixed(0)} ${H.toFixed(0)}" width="${W.toFixed(0)}" height="${H.toFixed(0)}" style="max-width:100%;height:auto;display:block;font-family:system-ui,-apple-system,sans-serif">
    <defs><marker id="drg-arrow" markerWidth="10" markerHeight="8" refX="8" refY="3" orient="auto" markerUnits="strokeWidth">
      <path d="M0,0 L8,3 L0,6 z" fill="#94a3b8"/></marker></defs>
    ${inner}</svg>`;
}

// renderDrgSvg draws a model's decision requirements graph, read-only: no case on
// it, just the decisions, the input data and the requirements between them. It is
// the Modeler's DMN view, and the plain half of the picture the evaluation view
// annotates.
export function renderDrgSvg(g) {
  const placed = placeDrg(g);
  if (!placed.length) return `<p class="muted" style="padding:16px">This model has no decisions to show.</p>`;
  const pos = {};
  placed.forEach((p) => { pos[p.n.id] = p; });
  const nodes = placed.map(({ n, x, y, w, h }) => {
    const input = n.type === "inputData";
    const bkm = n.type === "businessKnowledgeModel";
    const fill = input ? "#eff6ff" : bkm ? "#f5f3ff" : "#ffffff";
    const stroke = input ? "#3b82f6" : bkm ? "#8b5cf6" : "#111827";
    const sub = input ? (n.dataType || "input data") : bkm ? "knowledge model" : (n.hasTable ? "decision table" : "decision");
    return `<g>
      ${nodeShape(n.type, x, y, w, h, `fill="${fill}" stroke="${stroke}" stroke-width="1.5"`)}
      <text x="${x + w / 2}" y="${y + h / 2 - 3}" text-anchor="middle" font-size="13" font-weight="600" fill="#111827">${esc(n.name || n.id)}</text>
      <text x="${x + w / 2}" y="${y + h / 2 + 14}" text-anchor="middle" font-size="10.5" fill="#6b7280">${esc(sub)}</text>
    </g>`;
  }).join("");
  return drgSvg(placed, drgEdges(g, pos) + nodes);
}

// ---------- one case, drawn on the graph ----------

// nodeKey is the name a node answers to in an evaluation's data. temis binds,
// keys and traces by the FEEL identifier (a decision's `<variable name>`, an input
// datum's), falling back to the label where a model declares none — so that is the
// name a recorded input, a recorded output and a trace's input column all carry,
// and the only one a value can be tied to a node by (ADR-0385).
const nodeKey = (n) => n.varName || n.name || n.id;

// caseValues ties an evaluation's values to the graph's nodes, by name and by
// name only. Three sources, in order of authority:
//
//   - **given** — the input context the task assembled. For an input datum that is
//     what it was handed; for a *decision* it means the caller supplied the result
//     instead of the model computing it, which is what a decision service's
//     boundary looks like from outside and is worth saying out loud.
//   - **derived** — a value the trace reports a table's input column evaluated to.
//     This is how an intermediate decision's result becomes visible: it is read by
//     the table below it, and the trace records what that read returned.
//   - **result** — what the evaluation returned. Last, because for the decision it
//     names nothing else is more authoritative.
//
// A column whose expression is not a bare name ("count(items) > 0") ties to no node
// and is simply not looked up, which is the correct outcome rather than a missed one.
function caseValues(view) {
  const vals = new Map();
  const put = (k, value, source) => { if (k) vals.set(k, { value, source }); };
  for (const [k, v] of Object.entries(view.inputs || {})) put(k, v, "given");
  for (const t of traceTablesOf(view.trace)) {
    for (const col of (t.inputs || [])) if (!vals.has(col.expression)) put(col.expression, col.value, "derived");
  }
  for (const [k, v] of Object.entries(view.outputs || {})) put(k, v, "result");
  return vals;
}

// clip keeps a value legible inside a node box. The full text stays reachable —
// every node carries it as a tooltip, and the rule matrices below show it in full.
const clip = (s, max = 24) => (s.length > max ? s.slice(0, max - 1) + "…" : s);

// SOURCE_NOTE says, in the node, where its value came from. "given" reads
// differently on a decision than on an input datum: an input datum is always given,
// while a decision whose value was given is one the caller supplied rather than the
// model computing it — the case that makes a decision service's boundary visible.
const SOURCE_NOTE = {
  given: "given to the decision — supplied, not computed",
  derived: "computed on the way, and read by the rule above it",
  result: "what the decision returned",
};

// renderCaseDrg draws the graph with the case on it: every node that carries a
// value shows it, and every node that does not is drawn muted and says so. Nodes
// stay the size the diagram gives them, so the value rides under the name where the
// subtitle would otherwise be — the subtitle says what kind of thing the node is,
// which the shape already says.
function renderCaseDrg(view, vals) {
  const placed = placeDrg(view);
  if (!placed.length) {
    return `<p class="ops-empty">This decision's model has no graph to draw — the server could not resolve the model this evaluation ran against.</p>`;
  }
  const pos = {};
  placed.forEach((p) => { pos[p.n.id] = p; });

  const nodes = placed.map(({ n, x, y, w, h }) => {
    const input = n.type === "inputData"; // the shape: an input datum is drawn as a stadium
    const bkm = n.type === "businessKnowledgeModel";
    const hit = vals.get(nodeKey(n));
    const isResult = hit && hit.source === "result";
    const isGiven = hit && hit.source === "given";
    // Colour says where the value came from, not what kind of node holds it — the
    // shape already says that, and where a value came from is the thing a reader is
    // actually asking. So a *decision* handed in by the caller is drawn like an input
    // datum, because that is what it was: a service's boundary, supplied rather than
    // computed, and invisible in every other view. A node the case did not touch is
    // drawn back, since "this played no part" is part of the account. The decision
    // that produced the answer is the one the eye should land on.
    const fill = !hit ? "#f8fafc" : isResult ? "#dcfce7" : isGiven ? "#eff6ff" : bkm ? "#f5f3ff" : "#ffffff";
    const stroke = !hit ? "#cbd5e1" : isResult ? "#15803d" : isGiven ? "#3b82f6" : bkm ? "#8b5cf6" : "#111827";
    const nameFill = hit ? "#111827" : "#94a3b8";
    const text = hit ? traceValue(hit.value) : "";
    const sub = hit
      ? `<text x="${x + w / 2}" y="${y + h / 2 + 15}" text-anchor="middle" font-size="11.5" font-weight="700"
           font-family="ui-monospace, SFMono-Regular, Menlo, monospace" fill="${isResult ? "#15803d" : isGiven ? "#1d4ed8" : "#334155"}">${esc(clip(text))}</text>`
      : `<text x="${x + w / 2}" y="${y + h / 2 + 15}" text-anchor="middle" font-size="10.5" fill="#94a3b8">not part of this case</text>`;
    const title = hit
      ? `${n.name || n.id} = ${text}\n${SOURCE_NOTE[hit.source] || ""}`
      : `${n.name || n.id}\nThis evaluation records no value for it.`;
    return `<g class="drg-node${hit ? "" : " is-idle"}${isResult ? " is-result" : ""}">
      <title>${esc(title)}</title>
      ${nodeShape(n.type, x, y, w, h, `fill="${fill}" stroke="${stroke}" stroke-width="${isResult ? 2.5 : 1.5}"`)}
      <text x="${x + w / 2}" y="${y + h / 2 - 4}" text-anchor="middle" font-size="13" font-weight="600" fill="${nameFill}">${esc(clip(n.name || n.id, 26))}</text>
      ${sub}</g>`;
  }).join("");

  return drgSvg(placed, drgEdges(view, pos) + nodes);
}

// renderRules draws the rule matrices, one per decision table the evaluation ran,
// in the order it ran them — the "which rules fired" half of the question, with the
// matched rule green and every condition marked as it was tested.
//
// The three silences here are different things, and saying the wrong one is worse
// than saying nothing (see dmn-trace.js, which draws the matrix itself):
//
//   - a service evaluation recorded before the engine could trace one carries no
//     rules (ADR-0398, fixed since) — the values above are still exact, because the
//     record is frozen history and nothing here recomputes it;
//   - a trace with no tables means the decision's logic is not a table;
//   - no trace and no service means nothing was recorded, which is the remote-decision
//     case (ADR-0050).
function renderRules(view) {
  const tables = traceTablesOf(view.trace);
  if (tables.length) {
    return tables.map((tt, i) => renderTraceTable(tt, tables.length > 1 ? i + 1 : 0)).join("");
  }
  if (view.service) {
    return `<p class="ops-empty">No rule matrix was recorded for this case. It ran through a decision
      <b>service</b> — DMN's published interface over part of the graph — and a service reported no rules
      until the engine learned to trace one. So this is an older record: the values on the graph above are
      exactly what it carried, and what is missing is only which row of which table matched. A case decided
      since shows its rules here, and nothing can add them to this one — the record is what happened, not a
      thing to re-run.</p>`;
  }
  if (view.trace === null || view.trace === undefined) {
    return `<p class="ops-empty">No trace was recorded for this evaluation, so there are no rules to show.</p>`;
  }
  return `<p class="ops-empty">This decision's logic is not a decision table, so there is no rule matrix to show.
    The graph above carries what it was given and what it returned.</p>`;
}

// ---------- the modal ----------

let openOverlay = null; // at most one graph is open, and Escape closes that one

// closeDecisionGraph takes the open graph down and puts focus back where the
// gesture came from.
export function closeDecisionGraph() {
  if (!openOverlay) return;
  const { el, restore } = openOverlay;
  openOverlay = null;
  el.remove();
  document.removeEventListener("keydown", onKeydown, true);
  window.removeEventListener("hashchange", closeDecisionGraph);
  if (restore && document.contains(restore)) { try { restore.focus(); } catch { /* it went with its view */ } }
}

function onKeydown(e) {
  if (e.key === "Escape" && openOverlay) { e.stopPropagation(); closeDecisionGraph(); }
}

// shell renders the modal chrome around whatever body it is given, so the loading,
// failed and loaded states are the same window rather than three. `title` is text
// and is escaped here; `subtitle` and `body` are markup, so every caller escapes
// what it puts in them — all of which is model and record data, none of it typed by
// the person reading it, but all of it authored by somebody.
function shell(title, subtitle, body) {
  return `<div class="drg-modal" role="dialog" aria-modal="true" aria-labelledby="drg-title">
    <header class="drg-head">
      <div class="drg-titles">
        <h2 id="drg-title">${esc(title)}</h2>
        <p class="drg-sub">${subtitle}</p>
      </div>
      <button type="button" class="drg-x" data-drg-close aria-label="Close">&times;</button>
    </header>
    <div class="drg-body">${body}</div>
  </div>`;
}

// openDecisionGraph opens one evaluation's decision graph over the current view.
//
// It takes the instance and the evaluation's timestamp because that pair is what
// identifies a decision record, and asks the server for the rest in one read: the
// graph of the model that ran, and the case that ran through it. Everything the
// window shows comes from that one answer, so there is no state here to go stale.
export async function openDecisionGraph({ api, instanceKey, at, decisionId = "", taskLabel = "", restoreFocusTo = null }) {
  closeDecisionGraph();
  const el = document.createElement("div");
  el.className = "drg-ov";
  el.innerHTML = shell(decisionId || "Decision", `<span class="muted">Loading the decision…</span>`, `<p class="ops-empty">Loading…</p>`);
  document.body.appendChild(el);
  openOverlay = { el, restore: restoreFocusTo || document.activeElement };
  document.addEventListener("keydown", onKeydown, true);
  // The window is appended to the body rather than to the view, so that it is not
  // clipped by the panel the gesture came from — which means navigating away does not
  // take it with it. A fixed overlay left standing over the next screen is the worst
  // kind of bug to reproduce, so leaving is a close.
  window.addEventListener("hashchange", closeDecisionGraph);
  el.addEventListener("click", (e) => {
    // The backdrop closes; the window itself does not, or every click inside it would.
    if (e.target === el || e.target.closest("[data-drg-close]")) closeDecisionGraph();
  });
  const x = el.querySelector(".drg-x");
  if (x) x.focus();

  let view;
  try {
    view = await api("GET", `/api/v1/instances/${instanceKey}/decisions/${at}/graph`);
  } catch (err) {
    if (openOverlay && openOverlay.el === el) {
      el.innerHTML = shell(decisionId || "Decision", `<span class="muted">${esc(taskLabel)}</span>`,
        `<p class="ops-empty">This decision could not be loaded: ${esc(err.message)}</p>`);
    }
    return;
  }
  if (!openOverlay || openOverlay.el !== el) return; // closed while it loaded

  const vals = caseValues(view);
  const when = view.at ? new Date(view.at / 1e6).toLocaleString() : "";
  const sub = [
    view.modelName ? `model <b>${esc(view.modelName)}</b>` : "",
    view.service ? `<span class="drg-tag">decision service</span>` : "",
    taskLabel ? `at <b>${esc(taskLabel)}</b>` : "",
    when ? esc(when) : "",
  ].filter(Boolean).join(" · ");

  const outs = Object.entries(view.outputs || {});
  const result = outs.length
    ? `<div class="res">${outs.map(([k, v]) =>
        `<div class="res-row"><span class="res-key">${esc(k)}</span><span class="res-val">${esc(traceValue(v))}</span></div>`).join("")}</div>`
    : `<span class="muted">This evaluation returned nothing.</span>`;

  el.innerHTML = shell(view.decisionId || decisionId || "Decision", sub, `
    <section class="drg-canvas">${renderCaseDrg(view, vals)}</section>
    <p class="drg-legend">
      <span class="drg-key"><i class="sw given"></i>given to the decision</span>
      <span class="drg-key"><i class="sw derived"></i>computed on the way</span>
      <span class="drg-key"><i class="sw result"></i>the answer</span>
      <span class="drg-key"><i class="sw idle"></i>not part of this case</span>
    </p>
    <section class="drg-split">
      <div class="drg-rules">
        <h3>Which rules fired</h3>
        ${renderRules(view)}
      </div>
      <div class="drg-result">
        <h3>Result</h3>
        ${result}
        <p class="muted drg-foot">Read this with the graph: the green box is the decision that
        produced the answer, and the matrix beside it is the table it used. A row is
        green where the case satisfied that condition and red where it did not, and the
        rule that carried the result is the highlighted one.</p>
        <p class="drg-foot"><a href="#/operations/decisions/${encodeURIComponent(view.decisionId || "")}"
          title="Every evaluation of this decision, across all instances">Every case this decision decided &rarr;</a></p>
      </div>
    </section>`);
  const close = el.querySelector(".drg-x");
  if (close) close.focus();
}
