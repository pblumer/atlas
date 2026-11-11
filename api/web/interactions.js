// Who a process talks to (ADR-0559 §7, §8): the interaction graph around one
// process, every participant collapsed by default, and a replay of only the messages
// that travelled between them.
//
// The page reads two routes and computes nothing of its own about who sent what:
//   - GET /api/v1/process-interactions draws the graph — participants, each exchange
//     with its possible and observed layer, the sends that reached nobody, the
//     center's ports;
//   - GET /api/v1/process-interactions/replay lists the same arrivals one by one, page
//     by page, read backwards until the history begins, then followed with `after`.
// Both attribute rows the same way on the server, under the event log's rule of who
// reads what, so what the replay plays is what the graph counts.
//
// The layout is three columns: whoever only sends to the process on the left, the
// process in the middle, everybody it sends to on the right. A participant is a
// collapsed pool; a process can be expanded to show its message ports and opened as
// the center of its own graph. Black boxes never expand.

import { t } from "./i18n.js";
import { fmtDateTime } from "./datefmt.js";

const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

const SVG_NS = "http://www.w3.org/2000/svg";

// Layout, in SVG units.
const BOX_W = 220, BOX_H = 52, PORT_H = 18, GAP = 28, COL_X = [20, 400, 780], WIDTH = 1020, TOP = 40;

// The replay.
const LIVE_MS = 3000;         // how often Live asks for what came after the newest event
const MAX_EVENTS = 5000;      // how many events the page holds; the oldest go first
const PAGE = 1000;            // events per page read
const TICK_MS = 700;          // one replay step
const SPEEDS = [1, 5, 25];    // events per step; above one the step is drawn as counts

const WINDOWS = [["24h", "ix.window.day"], ["168h", "ix.window.week"], ["", "ix.window.retention"]];

// label is how a participant is named on the page. A process carries the name it was
// deployed under; a black box is named by its kind, and the one outside the reader's
// view has no name at all, on purpose (ADR-0472 §5).
function label(p) {
  switch (p.kind) {
    case "process": return p.name || p.processId;
    case "inbound-watch": return p.name || p.connectorId;
    case "product-action": return p.name ? `${p.name} · ${p.action}` : `${p.productId} · ${p.action}`;
    case "publish": return t("ix.kind.publish");
    case "inbound": return t("ix.kind.inbound");
    case "directed": return t("ix.kind.directed");
    case "removed": return t("ix.kind.removed");
    case "outside-view": return t("ix.kind.outside");
  }
  return p.id;
}

function kindHint(p) {
  switch (p.kind) {
    case "process": return p.center ? t("ix.kind.center") : t("ix.kind.process");
    case "inbound-watch": return t("ix.kind.watch", { kind: p.workerKind || "" });
    case "product-action": return t("ix.kind.product");
    case "outside-view": return t("ix.kind.outsideHint");
  }
  return t("ix.kind.blackbox");
}

// layout places the participants in three columns and answers each one's box.
export function layout(graph, expanded, portsOf) {
  const parts = graph.participants;
  const center = parts.find((p) => p.center);
  const receives = new Set(graph.exchanges.filter((e) => e.from === center.id).map((e) => e.to));
  const left = [], right = [];
  for (const p of parts) {
    if (p.center) continue;
    (receives.has(p.id) ? right : left).push(p);
  }
  const byName = (a, b) => label(a).localeCompare(label(b)) || a.id.localeCompare(b.id);
  left.sort(byName); right.sort(byName);
  const height = (p) => {
    if (!expanded.has(p.id) || p.kind !== "process") return BOX_H;
    const ports = portsOf(p) || [];
    return BOX_H + Math.max(ports.length, 1) * PORT_H + 12;
  };
  const boxes = new Map();
  const stack = (list, x) => {
    let y = TOP;
    for (const p of list) {
      const h = height(p);
      boxes.set(p.id, { p, x, y, w: BOX_W, h });
      y += h + GAP;
    }
    return y;
  };
  const bottom = Math.max(stack(left, COL_X[0]), stack(right, COL_X[2]), TOP + height(center) + GAP);
  const ch = height(center);
  boxes.set(center.id, { p: center, x: COL_X[1], y: Math.max(TOP, (bottom - GAP + TOP) / 2 - ch / 2), w: BOX_W, h: ch });
  return { boxes, height: bottom + 40, center };
}

// edgePath is the path of one exchange between two boxes. Exchanges between the same
// two boxes are spread by lane, and one direction runs above the other.
function edgePath(a, b, lane) {
  if (a === b) {
    const x = a.x + a.w / 2, y = a.y;
    const r = 34 + lane * 14;
    return `M ${x - 30} ${y} C ${x - 30} ${y - r}, ${x + 30} ${y - r}, ${x + 30} ${y}`;
  }
  const forward = a.x < b.x;
  const sx = forward ? a.x + a.w : a.x, tx = forward ? b.x : b.x + b.w;
  // Left to right runs above the middle of the boxes, right to left below it, so the
  // two directions between the same pair never share a line or a label.
  const off = (forward ? -1 : 1) * (10 + lane * 16);
  const sy = a.y + Math.min(a.h, BOX_H) / 2 + off, ty = b.y + Math.min(b.h, BOX_H) / 2 + off;
  const mx = (sx + tx) / 2;
  return `M ${sx} ${sy} C ${mx} ${sy}, ${mx} ${ty}, ${tx} ${ty}`;
}

function edgeText(e) {
  if (!e.observed) return t("ix.edge.possible", { message: e.message });
  const started = e.observed.started ? t("ix.edge.started", { n: e.observed.started }) : "";
  return t("ix.edge.observed", { message: e.message, n: e.observed.received }) + started;
}

function edgeTitle(e) {
  const parts = [e.possible ? t("ix.edge.titlePossible") : t("ix.edge.titleNotPossible")];
  if (e.observed) parts.push(t("ix.edge.titleLast", { at: fmtDateTime(e.observed.lastAt) }));
  else parts.push(t("ix.edge.titleNotObserved"));
  return parts.join(" · ");
}

// trunc keeps a text inside its box; the box's tooltip carries the whole of it.
const trunc = (s, n) => (s.length > n ? s.slice(0, n - 1) + "…" : s);

// fmtRetention says a Go duration such as 720h0m0s in days when it is whole days.
export function fmtRetention(d) {
  const m = /^(\d+)h0m0s$/.exec(d || "");
  if (m && Number(m[1]) % 24 === 0) return t("ix.days", { n: Number(m[1]) / 24 });
  return d || "";
}

const edgeKey = (from, to, message) => `${from}\u0000${to}\u0000${message}`;

export async function viewInteractions({ api, view, processId, isSuperseded }) {
  const params = new URLSearchParams((location.hash.split("?")[1] || ""));
  let windowArg = params.get("window") ?? "24h";
  if (!WINDOWS.some(([w]) => w === windowArg)) windowArg = "24h";

  view.innerHTML = `
    <div class="between">
      <h1 id="ix-title">${esc(t("ix.title", { name: processId }))}</h1>
      <span class="row" style="gap:8px;align-items:center">
        <select id="ix-window" aria-label="${esc(t("ix.window.label"))}" style="width:auto">${WINDOWS.map(([w, k]) =>
          `<option value="${esc(w)}"${w === windowArg ? " selected" : ""}>${esc(t(k))}</option>`).join("")}</select>
        <button class="btn neutral" id="ix-expand-all">${esc(t("ix.expandAll"))}</button>
        <button class="btn neutral" id="ix-collapse-all">${esc(t("ix.collapseAll"))}</button>
      </span>
    </div>
    <p class="muted">${t("ix.intro")}</p>
    <div class="card ix-card">
      <div id="ix-legend" class="ix-legend muted">${t("ix.legend")}</div>
      <div id="ix-canvas" class="ix-canvas"><p class="muted">${esc(t("ix.loading"))}</p></div>
      <div id="ix-unreceived"></div>
    </div>
    <div class="card ix-card">
      <h2>${esc(t("ix.replay.title"))}</h2>
      <div class="muted" id="ix-history"></div>
      <div class="row ix-transport" style="gap:8px;align-items:center;flex-wrap:wrap;margin:8px 0">
        <button class="btn neutral" id="ix-rewind" title="${esc(t("ix.replay.rewind"))}">⏮</button>
        <button class="btn" id="ix-play">${esc(t("ix.replay.play"))}</button>
        <button class="btn neutral" id="ix-step" title="${esc(t("ix.replay.step"))}">⏭</button>
        <select id="ix-speed" aria-label="${esc(t("ix.replay.speed"))}" style="width:auto">${SPEEDS.map((s) =>
          `<option value="${s}">${esc(t("ix.replay.perStep", { n: s }))}</option>`).join("")}</select>
        <input type="range" id="ix-scrub" min="0" max="0" value="0" aria-label="${esc(t("ix.replay.scrub"))}" style="flex:1;min-width:160px">
        <label><input type="checkbox" id="ix-live"> ${esc(t("ix.replay.live"))}</label>
      </div>
      <div class="muted" id="ix-now"></div>
      <div class="muted" id="ix-mode"></div>
      <ol id="ix-feed" class="ix-feed"></ol>
    </div>`;

  const $ = (id) => view.querySelector("#" + id);
  const expanded = new Set();
  const portsCache = new Map(); // processId → ports, read from that process's own graph
  let graph = null;
  let shape = null;              // the last layout
  const paths = new Map();       // edgeKey → SVG path element

  const query = (path, extra) => {
    const q = new URLSearchParams({ processId, ...extra });
    if (windowArg) q.set("window", windowArg);
    return `${path}?${q}`;
  };

  // ---------- the graph ----------

  const portsOf = (p) => (p.center ? graph.ports : portsCache.get(p.processId));

  const draw = () => {
    shape = layout(graph, expanded, portsOf);
    const svg = document.createElementNS(SVG_NS, "svg");
    svg.setAttribute("viewBox", `0 0 ${WIDTH} ${shape.height}`);
    svg.setAttribute("width", "100%");
    svg.setAttribute("class", "ix-svg");
    svg.setAttribute("role", "img");
    svg.setAttribute("aria-label", t("ix.title", { name: label(shape.center) }));
    const defs = document.createElementNS(SVG_NS, "defs");
    defs.innerHTML = `<marker id="ix-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 z" class="ix-arrowhead"/></marker>`;
    svg.appendChild(defs);

    // Edges first, under the boxes.
    paths.clear();
    const lanes = new Map();
    for (const e of graph.exchanges) {
      const a = shape.boxes.get(e.from), b = shape.boxes.get(e.to);
      if (!a || !b) continue;
      const pair = [e.from, e.to].sort().join("\u0000") + (e.from === e.to ? "" : (a.x < b.x ? ">" : "<"));
      const lane = lanes.get(pair) || 0;
      lanes.set(pair, lane + 1);
      const g = document.createElementNS(SVG_NS, "g");
      const cls = ["ix-edge", e.observed ? "observed" : "possible-only", e.possible ? "" : "not-possible"].filter(Boolean).join(" ");
      g.setAttribute("class", cls);
      g.dataset.from = e.from; g.dataset.to = e.to; g.dataset.message = e.message;
      const path = document.createElementNS(SVG_NS, "path");
      path.setAttribute("d", edgePath(a, b, lane));
      path.setAttribute("marker-end", "url(#ix-arrow)");
      const title = document.createElementNS(SVG_NS, "title");
      title.textContent = edgeTitle(e);
      path.appendChild(title);
      g.appendChild(path);
      const len = path.getTotalLength ? path.getTotalLength() : 0;
      const mid = len && path.getPointAtLength ? path.getPointAtLength(len / 2) : { x: (a.x + b.x) / 2, y: (a.y + b.y) / 2 };
      const text = document.createElementNS(SVG_NS, "text");
      // A label sits on the outer side of its line: above one that runs to the right,
      // below one that runs to the left.
      const below = e.from !== e.to && a.x > b.x;
      text.setAttribute("x", mid.x); text.setAttribute("y", below ? mid.y + 14 : mid.y - 5);
      text.setAttribute("text-anchor", "middle");
      text.setAttribute("class", "ix-edge-label");
      text.textContent = edgeText(e);
      g.appendChild(text);
      svg.appendChild(g);
      paths.set(edgeKey(e.from, e.to, e.message), path);
    }

    for (const box of shape.boxes.values()) {
      const p = box.p;
      const g = document.createElementNS(SVG_NS, "g");
      const open = expanded.has(p.id) && p.kind === "process";
      g.setAttribute("class", `ix-box kind-${p.kind}${p.center ? " center" : ""}${open ? " expanded" : ""}${p.inactive ? " inactive" : ""}`);
      g.dataset.id = p.id;
      g.setAttribute("transform", `translate(${box.x} ${box.y})`);
      const rect = document.createElementNS(SVG_NS, "rect");
      rect.setAttribute("width", box.w); rect.setAttribute("height", box.h);
      rect.setAttribute("rx", 6);
      g.appendChild(rect);
      const name = document.createElementNS(SVG_NS, "text");
      name.setAttribute("x", 12); name.setAttribute("y", 22);
      name.setAttribute("class", "ix-box-name");
      name.textContent = trunc(label(p), p.kind === "process" ? 22 : 26);
      g.appendChild(name);
      const hint = document.createElementNS(SVG_NS, "text");
      hint.setAttribute("x", 12); hint.setAttribute("y", 40);
      hint.setAttribute("class", "ix-box-hint");
      hint.textContent = trunc(kindHint(p) + (p.inactive ? " · " + t("ix.kind.inactive") : ""), 34);
      g.appendChild(hint);
      if (p.kind === "process") {
        const toggle = document.createElementNS(SVG_NS, "text");
        toggle.setAttribute("x", box.w - 14); toggle.setAttribute("y", 22);
        toggle.setAttribute("text-anchor", "end");
        toggle.setAttribute("class", "ix-box-toggle");
        toggle.textContent = open ? "−" : "+";
        g.appendChild(toggle);
        if (!p.center) {
          // Opening a neighbour as the center of its own graph is how the picture is
          // extended one step (ADR-0559 §3).
          const go = document.createElementNS(SVG_NS, "text");
          go.setAttribute("x", box.w - 34); go.setAttribute("y", 22);
          go.setAttribute("text-anchor", "end");
          go.setAttribute("class", "ix-box-open");
          go.textContent = "↗";
          const goTip = document.createElementNS(SVG_NS, "title");
          goTip.textContent = t("ix.box.open");
          go.appendChild(goTip);
          g.appendChild(go);
        }
      }
      if (open) {
        const ports = portsOf(p);
        const lines = ports && ports.length ? ports.map((pt) => `${pt.direction === "out" ? "→" : "←"} ${pt.message} (${pt.elements.join(", ")})`)
          : [ports ? t("ix.ports.none") : t("ix.loading")];
        lines.forEach((line, i) => {
          const tx = document.createElementNS(SVG_NS, "text");
          tx.setAttribute("x", 12); tx.setAttribute("y", BOX_H + 6 + i * PORT_H);
          tx.setAttribute("class", "ix-port");
          tx.textContent = line;
          g.appendChild(tx);
        });
      }
      const tip = document.createElementNS(SVG_NS, "title");
      tip.textContent = (p.kind === "process" ? `${label(p)} · ` + t("ix.box.tip", { process: p.processId }) : `${label(p)} · ${kindHint(p)}`);
      g.appendChild(tip);
      svg.appendChild(g);
    }

    const canvas = $("ix-canvas");
    canvas.innerHTML = "";
    canvas.appendChild(svg);
    if (!graph.exchanges.length) {
      const p = document.createElement("p");
      p.className = "muted";
      p.textContent = t("ix.none");
      canvas.appendChild(p);
    }
    $("ix-unreceived").innerHTML = graph.unreceived.length
      ? `<p class="ix-unreceived"><span class="pill warn">${esc(t("ix.unreceived.title"))}</span> ${graph.unreceived.map((u) =>
        esc(t("ix.unreceived.item", { message: u.message, n: u.count, at: fmtDateTime(u.lastAt) }))).join(" · ")}</p>`
      : "";
    const center = shape.center;
    $("ix-title").textContent = t("ix.title", { name: label(center) });
    const partial = graph.observed.partial ? " " + t("ix.partial", { at: fmtDateTime(graph.observed.readFrom) }) : "";
    $("ix-legend").innerHTML = t("ix.legend") + esc(partial);
  };

  const loadPorts = async (p) => {
    if (p.center || portsCache.has(p.processId)) return;
    try {
      const g = await api("GET", `/api/v1/process-interactions?processId=${encodeURIComponent(p.processId)}&window=1ns`);
      portsCache.set(p.processId, g.ports || []);
    } catch {
      portsCache.set(p.processId, []);
    }
  };

  const toggle = async (id) => {
    const box = shape && shape.boxes.get(id);
    if (!box || box.p.kind !== "process") return;
    if (expanded.has(id)) { expanded.delete(id); draw(); return; }
    expanded.add(id);
    draw();
    await loadPorts(box.p);
    if (!isSuperseded()) draw();
  };

  $("ix-canvas").addEventListener("click", (e) => {
    const g = e.target.closest(".ix-box");
    if (!g) return;
    const box = shape.boxes.get(g.dataset.id);
    if (box && e.target.closest(".ix-box-open")) {
      location.hash = `#/operations/interactions/${encodeURIComponent(box.p.processId)}`;
      return;
    }
    toggle(g.dataset.id);
  });
  $("ix-expand-all").addEventListener("click", async () => {
    for (const p of graph.participants) if (p.kind === "process") expanded.add(p.id);
    draw();
    await Promise.all(graph.participants.filter((p) => p.kind === "process").map(loadPorts));
    if (!isSuperseded()) draw();
  });
  $("ix-collapse-all").addEventListener("click", () => { expanded.clear(); draw(); });

  const loadGraph = async () => {
    try {
      graph = await api("GET", query("/api/v1/process-interactions"));
    } catch (err) {
      if (isSuperseded()) return false;
      $("ix-canvas").innerHTML = `<p class="muted">${esc(err && err.status === 404 ? t("ix.unknown", { process: processId }) : t("ix.failed", { error: (err && err.message) || err }))}</p>`;
      return false;
    }
    if (isSuperseded()) return false;
    draw();
    return true;
  };

  // ---------- the replay ----------

  let events = [];
  let index = 0;               // the next event to play
  let latest = "0";
  let playing = null, liveTimer = null;
  let reachedStart = true;

  const nowLabel = () => {
    const ev = events[Math.min(index, events.length) - 1];
    $("ix-now").textContent = events.length
      ? t("ix.replay.at", { at: ev ? fmtDateTime(ev.at) : "—", i: index, n: events.length })
      : t("ix.replay.empty");
    $("ix-scrub").max = String(events.length);
    $("ix-scrub").value = String(index);
  };

  const historyLabel = () => {
    if (!events.length) { $("ix-history").textContent = ""; return; }
    const from = fmtDateTime(events[0].at);
    $("ix-history").textContent = reachedStart
      ? t("ix.history.complete", { at: from, retention: fmtRetention(graph ? graph.retention : "") })
      : t("ix.history.cut", { at: from });
  };

  const feedLine = (ev) => {
    const name = (id) => {
      const p = graph && graph.participants.find((x) => x.id === id);
      return p ? label(p) : id;
    };
    const what = {
      received: t("ix.event.received"), started: t("ix.event.started"),
      unreceived: t("ix.event.unreceived"), outside: t("ix.event.outside", { n: ev.count }),
    }[ev.kind] || ev.kind;
    const route = ev.to ? `${name(ev.from)} → ${name(ev.to)}` : name(ev.from);
    const li = document.createElement("li");
    li.className = `ix-ev kind-${ev.kind}`;
    li.dataset.kind = ev.kind;
    li.innerHTML = `<span class="muted">${esc(fmtDateTime(ev.at))}</span> <code>${esc(ev.message)}</code> ${esc(route)} · ${esc(what)}${ev.correlationKey ? ` <span class="chip">${esc(ev.correlationKey)}</span>` : ""}`;
    return li;
  };

  const showFeed = (played) => {
    const feed = $("ix-feed");
    for (const ev of played) feed.prepend(feedLine(ev));
    while (feed.children.length > 8) feed.lastChild.remove();
  };

  // envelope moves a dot along the exchange's path; a send that reached nobody stops
  // at the edge of its sender.
  const envelope = (ev) => {
    const svg = view.querySelector(".ix-svg");
    if (!svg || !shape) return;
    const dot = document.createElementNS(SVG_NS, "circle");
    dot.setAttribute("r", 6);
    dot.setAttribute("class", `ix-envelope kind-${ev.kind}`);
    svg.appendChild(dot);
    const path = ev.to ? paths.get(edgeKey(ev.from, ev.to, ev.message)) : null;
    const from = shape.boxes.get(ev.from);
    const t0 = performance.now(), dur = TICK_MS * 0.85;
    const frame = (now) => {
      const f = Math.min((now - t0) / dur, 1);
      let pt;
      if (path && path.getPointAtLength) {
        pt = path.getPointAtLength(path.getTotalLength() * f);
      } else if (from) {
        pt = { x: from.x + from.w + 24 * f, y: from.y + Math.min(from.h, BOX_H) / 2 };
      } else { dot.remove(); return; }
      dot.setAttribute("cx", pt.x); dot.setAttribute("cy", pt.y);
      if (f < 1) requestAnimationFrame(frame);
      else setTimeout(() => dot.remove(), ev.kind === "unreceived" ? 400 : 0);
    };
    requestAnimationFrame(frame);
  };

  // pulse marks an exchange with how many of its events one step played.
  const pulse = (counts) => {
    for (const [key, n] of counts) {
      const path = paths.get(key);
      if (!path) continue;
      const g = path.parentNode;
      g.classList.add("pulse");
      setTimeout(() => g.classList.remove("pulse"), TICK_MS * 0.8);
      const len = path.getTotalLength ? path.getTotalLength() : 0;
      const pt = len && path.getPointAtLength ? path.getPointAtLength(len * 0.8) : null;
      if (!pt) continue;
      const tag = document.createElementNS(SVG_NS, "text");
      tag.setAttribute("x", pt.x); tag.setAttribute("y", pt.y - 8);
      tag.setAttribute("class", "ix-pulse-count");
      tag.textContent = `+${n}`;
      path.ownerSVGElement.appendChild(tag);
      setTimeout(() => tag.remove(), TICK_MS * 0.9);
    }
  };

  const speed = () => Number($("ix-speed").value) || 1;

  const step = () => {
    if (index >= events.length) { pause(); return; }
    const n = speed();
    const played = events.slice(index, index + n);
    index += played.length;
    if (n === 1) {
      $("ix-mode").textContent = t("ix.mode.single");
      played.forEach(envelope);
    } else {
      $("ix-mode").textContent = t("ix.mode.dense");
      const counts = new Map();
      for (const ev of played) {
        const key = ev.to ? edgeKey(ev.from, ev.to, ev.message) : "";
        if (key) counts.set(key, (counts.get(key) || 0) + ev.count);
      }
      pulse(counts);
    }
    showFeed(played);
    nowLabel();
  };

  const pause = () => {
    if (playing) { clearInterval(playing); playing = null; }
    $("ix-play").textContent = t("ix.replay.play");
  };
  const play = () => {
    if (index >= events.length) index = 0;
    pause();
    $("ix-play").textContent = t("ix.replay.pause");
    step();
    playing = setInterval(() => { if (isSuperseded()) { stopAll(); return; } step(); }, TICK_MS);
  };

  // loadHistory reads the replay backwards until the history begins or the page holds
  // as many events as it keeps.
  const loadHistory = async () => {
    events = []; index = 0; latest = "0"; reachedStart = true;
    $("ix-feed").innerHTML = "";
    let before = "";
    for (;;) {
      let page;
      try {
        page = await api("GET", query("/api/v1/process-interactions/replay", { limit: String(PAGE), ...(before ? { before } : {}) }));
      } catch { break; }
      if (isSuperseded() || !page) return;
      if (latest === "0") latest = page.latest || "0";
      events = (page.events || []).concat(events);
      if (!page.more) break;
      if (events.length >= MAX_EVENTS) { reachedStart = false; break; }
      before = page.next;
    }
    if (events.length > MAX_EVENTS) { events = events.slice(events.length - MAX_EVENTS); reachedStart = false; }
    historyLabel();
    nowLabel();
  };

  // follow reads what came after the newest event and appends it; an exchange the
  // graph does not have yet redraws the graph first.
  const follow = async () => {
    if (isSuperseded()) { stopAll(); return; }
    let page;
    try { page = await api("GET", query("/api/v1/process-interactions/replay", { after: latest, limit: String(PAGE) })); } catch { return; }
    if (isSuperseded() || !page) return;
    latest = page.latest || latest;
    const fresh = page.events || [];
    if (!fresh.length) return;
    const unknown = fresh.some((ev) => ev.to && !paths.has(edgeKey(ev.from, ev.to, ev.message)));
    if (unknown) await loadGraph();
    const wasAtEnd = index >= events.length;
    events = events.concat(fresh);
    if (events.length > MAX_EVENTS) {
      const drop = events.length - MAX_EVENTS;
      events = events.slice(drop); index = Math.max(0, index - drop); reachedStart = false;
    }
    historyLabel();
    if (wasAtEnd && !playing) { const n = fresh.length; for (let i = 0; i < n; i++) step(); }
    nowLabel();
  };

  const stopAll = () => {
    pause();
    if (liveTimer) { clearInterval(liveTimer); liveTimer = null; }
  };
  window.__atlasCleanup = stopAll;

  $("ix-play").addEventListener("click", () => (playing ? pause() : play()));
  $("ix-step").addEventListener("click", () => { pause(); step(); });
  $("ix-rewind").addEventListener("click", () => { pause(); index = 0; $("ix-feed").innerHTML = ""; nowLabel(); });
  $("ix-scrub").addEventListener("input", (e) => { pause(); index = Number(e.target.value) || 0; nowLabel(); });
  $("ix-live").addEventListener("change", (e) => {
    if (liveTimer) { clearInterval(liveTimer); liveTimer = null; }
    if (e.target.checked) { index = events.length; nowLabel(); liveTimer = setInterval(follow, LIVE_MS); }
  });
  $("ix-window").addEventListener("change", async (e) => {
    windowArg = e.target.value;
    const q = new URLSearchParams({ window: windowArg });
    history.replaceState(null, "", `#/operations/interactions/${encodeURIComponent(processId)}?${q}`);
    stopAll();
    $("ix-live").checked = false;
    if (await loadGraph()) await loadHistory();
  });

  if (await loadGraph()) await loadHistory();
}
