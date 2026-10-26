// The Operations heatmap (ADR-0496): the live view's diagram shaded
// by how much each element and sequence flow was used, or by where the time went.
//
// The live view (editor.js, mountLive) owns the polling, the instance list and the
// variables panel; this module owns what the heatmap adds to it — the strip over the
// diagram that chooses what is shown, the shading itself in two styles, and the panel of
// figures behind the colours. It is a leaf with no side effects, so a harness can import
// the live view without importing anything else.
//
// What the colours mean is decided here once:
//
//   - The scale is relative to the largest value on screen, on a square root. Linear
//     puts every branch but the main path into the palest step — a process with one
//     path taken a thousand times and one taken thirty draws the second as nearly
//     nothing — and "rare" is exactly the reading a heatmap is opened for.
//   - Zero has a look of its own, dashed and faded, on the counts only: there it means
//     "no token ever came here", a coverage statement. On the times, zero means "no
//     waiting", the ordinary case, and drawing most of a healthy diagram as unreached
//     would be a lie (the Playground's rule, ADR-0215).
//   - Times are medians. Waiting is skewed by nights and weekends, and a mean lets the
//     one case that sat over a holiday speak for every other.

import { fmtCount } from "./numfmt.js";

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// HEAT_MODES are what the diagram can show, one at a time. "live" is the view as it was
// before the heatmap: where tokens are now, where they have been. `field` names the
// statistics summary a time mode reads.
export const HEAT_MODES = [
  { key: "live", label: "Live", title: "Where tokens are now and where they have been" },
  { key: "runs", label: "Runs", title: "How many tokens passed each element and sequence flow" },
  { key: "waiting", label: "Waiting", field: "waiting",
    title: "How long a task waited until a person claimed it or a Worker picked it up (median)" },
  { key: "working", label: "Working", field: "working",
    title: "How long a task took once claimed or picked up (median)" },
  { key: "duration", label: "Duration", field: "duration",
    title: "How long an element took from activation to completion (median)" },
];

// HEAT_PERIODS are the windows the statistics read. "all" is since deployment: the
// counts then come from the engine's counters, which are exact and outlive a history
// purge; a window is folded from the instances' history by the statistics endpoint.
export const HEAT_PERIODS = [
  { key: "all", label: "Since deployment", ms: 0 },
  { key: "30d", label: "Last 30 days", ms: 30 * 864e5 },
  { key: "7d", label: "Last 7 days", ms: 7 * 864e5 },
  { key: "24h", label: "Last 24 hours", ms: 864e5 },
];

export const HEAT_STYLES = [
  { key: "glow", label: "Glow", title: "Halos under the diagram that add up where the traffic is" },
  { key: "outline", label: "Outline", title: "Each shape and arrow outlined in its shade" },
];

const LEVELS = 5;

// heatIntensity places a value on the scale as 0..1. Anything above zero gets at least a
// faint share, so a path taken once in a thousand still shows.
export function heatIntensity(v, max) {
  if (!(v > 0) || !(max > 0)) return 0;
  return Math.max(0.15, Math.sqrt(Math.min(v, max) / max));
}

// heatLevel is the intensity in the outline style's five steps; 0 is "never".
export function heatLevel(v, max) {
  const i = heatIntensity(v, max);
  return i === 0 ? 0 : Math.max(1, Math.ceil(LEVELS * i));
}

// fmtMillis prints a measured time at the unit it is read in: a Worker's call in
// milliseconds or seconds, a person's task in minutes, hours or days.
export function fmtMillis(ms) {
  if (ms == null || !Number.isFinite(ms)) return "—";
  if (ms < 1) return "<1ms";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  const s = ms / 1000;
  if (s < 60) return `${s < 10 ? s.toFixed(1) : Math.round(s)}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return m % 60 ? `${h}h ${m % 60}m` : `${h}h`;
  const d = Math.floor(h / 24);
  return h % 24 ? `${d}d ${h % 24}h` : `${d}d`;
}

// FLOW_NODE is every shape a token can sit on — what the heatmap shades and what
// "never reached" is about. Pools, lanes, data and annotations are not.
const FLOW_NODE = /^bpmn:(\w*(Task|Event|Gateway)|SubProcess|CallActivity|Transaction|AdHocSubProcess)$/;
// TIMED is the subset a duration means something on: work, and waiting for an event.
// A gateway or a start event completes in the batch it activated in.
const TIMED = /^bpmn:(\w*Task|SubProcess|CallActivity|Transaction|AdHocSubProcess|IntermediateCatchEvent)$/;
const ACTIVITY = /^bpmn:(\w*Task|SubProcess|CallActivity|Transaction|AdHocSubProcess)$/;

const isFlowNode = (el) => !!el && !el.labelTarget && FLOW_NODE.test(el.type);
const isFlow = (el) => !!el && el.type === "bpmn:SequenceFlow";
const nameOf = (el) => {
  const bo = el && el.businessObject;
  return (bo && (bo.name || bo.id)) || (el && el.id) || "";
};

// heatValues reads what one mode shades, from the runtime overlay (counts since
// deployment) or the statistics (a window, and every time). It answers every flow node
// and every flow of the diagram — at zero where nothing arrived — so "never" can be
// drawn; for a time mode only the elements a time was measured on.
export function heatValues(registry, mode, period, rt, stats) {
  const values = new Map();
  const flows = new Map();
  const shapes = registry.filter(isFlowNode);
  if (mode === "runs") {
    for (const s of shapes) values.set(s.id, 0);
    for (const c of registry.filter(isFlow)) flows.set(c.id, 0);
    if (period === "all") {
      for (const e of (rt && rt.elements) || []) if (values.has(e.elementId)) values.set(e.elementId, e.visits || 0);
      for (const f of (rt && rt.flows) || []) if (flows.has(f.flowId)) flows.set(f.flowId, f.visits || 0);
    } else {
      for (const e of (stats && stats.elements) || []) if (values.has(e.elementId)) values.set(e.elementId, e.runs || 0);
      for (const f of (stats && stats.flows) || []) if (flows.has(f.flowId)) flows.set(f.flowId, f.runs || 0);
    }
  } else {
    const spec = HEAT_MODES.find((m) => m.key === mode);
    const timed = new Set(shapes.filter((s) => TIMED.test(s.type)).map((s) => s.id));
    for (const e of (stats && stats.elements) || []) {
      const sum = spec && e[spec.field];
      if (sum && timed.has(e.elementId)) values.set(e.elementId, sum.p50Millis);
    }
  }
  let max = 0;
  for (const v of values.values()) max = Math.max(max, v);
  for (const v of flows.values()) max = Math.max(max, v);
  return { values, flows, max };
}

// The glow's colour ramp, from a pale blue halo to red: the Playground's five shades
// (app.css, pg-heat-*), so the two heatmaps in the product read alike. The filter maps
// an intensity 0..1 through these tables — the alpha table makes the low end a
// transparent haze and the high end nearly opaque.
const GLOW_R = "0.478 0.478 0.302 0.753 0.910 0.851";
const GLOW_G = "0.655 0.655 0.525 0.478 0.471 0.188";
const GLOW_B = "1 1 1 0.839 0.235 0.145";
const GLOW_A = "0 0.4 0.55 0.68 0.8 0.9";
const SVG = "http://www.w3.org/2000/svg";

const svgEl = (name, attrs) => {
  const n = document.createElementNS(SVG, name);
  for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, String(v));
  return n;
};

// drawHeat shades the diagram and returns the markers it set, for the caller to clear
// with its own. Badges are overlays of type "heat", reaped by the caller's
// overlays.clear() like every other badge of the live view.
//
// The glow is one SVG layer *below* the diagram: every element and arrow is painted
// white at an opacity that is its intensity, blurred so neighbours add up, and coloured
// by intensity through a component transfer. The shapes are made see-through (the
// heat-glow class) so a task's own halo shows inside it.
export function drawHeat(viewer, { values, flows, max, mode, style, format, badgePosition }) {
  const canvas = viewer.get("canvas");
  const overlays = viewer.get("overlays");
  const registry = viewer.get("elementRegistry");
  const marked = [];
  const cold = mode === "runs";
  const glow = style !== "outline";
  const layer = canvas.getLayer("atlas-heat", -1);
  while (layer.firstChild) layer.removeChild(layer.firstChild);
  canvas.getContainer().classList.toggle("heat-glow", glow);

  if (glow) {
    let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
    for (const el of registry.getAll()) {
      if (el.waypoints) {
        for (const p of el.waypoints) { x0 = Math.min(x0, p.x); y0 = Math.min(y0, p.y); x1 = Math.max(x1, p.x); y1 = Math.max(y1, p.y); }
      } else if (Number.isFinite(el.x) && Number.isFinite(el.y) && el.width > 0) {
        x0 = Math.min(x0, el.x); y0 = Math.min(y0, el.y);
        x1 = Math.max(x1, el.x + el.width); y1 = Math.max(y1, el.y + el.height);
      }
    }
    if (Number.isFinite(x0)) {
      const pad = 100;
      const filter = svgEl("filter", {
        id: "atlas-heat-glow", filterUnits: "userSpaceOnUse",
        x: x0 - pad, y: y0 - pad, width: x1 - x0 + 2 * pad, height: y1 - y0 + 2 * pad,
        "color-interpolation-filters": "sRGB",
      });
      filter.appendChild(svgEl("feGaussianBlur", { stdDeviation: 15 }));
      filter.appendChild(svgEl("feColorMatrix", { type: "matrix", values: "0 0 0 1 0  0 0 0 1 0  0 0 0 1 0  0 0 0 1 0" }));
      const ct = svgEl("feComponentTransfer", {});
      ct.appendChild(svgEl("feFuncR", { type: "table", tableValues: GLOW_R }));
      ct.appendChild(svgEl("feFuncG", { type: "table", tableValues: GLOW_G }));
      ct.appendChild(svgEl("feFuncB", { type: "table", tableValues: GLOW_B }));
      ct.appendChild(svgEl("feFuncA", { type: "table", tableValues: GLOW_A }));
      filter.appendChild(ct);
      const defs = svgEl("defs", {});
      defs.appendChild(filter);
      layer.appendChild(defs);
      const g = svgEl("g", { filter: "url(#atlas-heat-glow)", class: "atlas-heat-glow" });
      for (const [id, v] of flows) {
        const c = registry.get(id);
        const i = heatIntensity(v, max);
        if (!c || !c.waypoints || i === 0) continue;
        g.appendChild(svgEl("polyline", {
          points: c.waypoints.map((p) => `${p.x},${p.y}`).join(" "),
          fill: "none", stroke: "#fff", "stroke-width": 32, "stroke-linecap": "round",
          "stroke-linejoin": "round", "stroke-opacity": i, "data-heat": id,
        }));
      }
      for (const [id, v] of values) {
        const s = registry.get(id);
        const i = heatIntensity(v, max);
        if (!s || !Number.isFinite(s.x) || !(s.width > 0) || i === 0) continue;
        g.appendChild(glowShape(s, i));
      }
      layer.appendChild(g);
    }
  }

  const mark = (id, v) => {
    if (glow || !registry.get(id)) return;
    const cls = `pg-heat-${heatLevel(v, max)}`;
    canvas.addMarker(id, cls);
    marked.push([id, cls]);
  };
  for (const [id, v] of values) {
    const s = registry.get(id);
    if (!s) continue;
    if (v === 0 && cold) {
      // Never reached is drawn the same in both styles: there is no halo to show it.
      canvas.addMarker(id, "pg-heat-0");
      marked.push([id, "pg-heat-0"]);
    } else {
      mark(id, v);
    }
    if (/Gateway$/.test(s.type) || (v === 0 && !cold)) continue;
    const text = format(v);
    try {
      overlays.add(id, "heat", {
        position: badgePosition ? badgePosition(s) : { top: -24, right: 16 },
        html: `<div class="token-badges"><div class="token-badge heat${v === 0 ? " cold" : ""}" title="${esc(text)}">${esc(text)}</div></div>`,
      });
    } catch { /* a shape without graphics */ }
  }
  for (const [id, v] of flows) {
    if (!registry.get(id)) continue;
    if (v === 0 && cold) {
      canvas.addMarker(id, "pg-heat-0");
      marked.push([id, "pg-heat-0"]);
    } else {
      mark(id, v);
    }
  }
  return marked;
}

// glowShape is one element's footprint in the glow layer. A container — an expanded
// subprocess — glows along its border rather than over its whole area, or it would
// drown the elements inside it.
function glowShape(s, i) {
  const { x, y, width: w, height: h } = s;
  if (/Event$/.test(s.type)) {
    return svgEl("circle", { cx: x + w / 2, cy: y + h / 2, r: w / 2 + 8, fill: "#fff", "fill-opacity": i, "data-heat": s.id });
  }
  if (/Gateway$/.test(s.type)) {
    const cx = x + w / 2, cy = y + h / 2, r = w / 2 + 8;
    return svgEl("polygon", {
      points: `${cx},${cy - r} ${cx + r},${cy} ${cx},${cy + r} ${cx - r},${cy}`,
      fill: "#fff", "fill-opacity": i, "data-heat": s.id,
    });
  }
  if (w > 200 || h > 140) {
    return svgEl("rect", { x, y, width: w, height: h, rx: 10, fill: "none", stroke: "#fff", "stroke-width": 32, "stroke-opacity": i, "data-heat": s.id });
  }
  return svgEl("rect", { x: x - 6, y: y - 6, width: w + 12, height: h + 12, rx: 14, fill: "#fff", "fill-opacity": i, "data-heat": s.id });
}

// clearHeat takes the glow away when the view goes back to Live. The markers are the
// caller's to remove, with its own.
export function clearHeat(viewer) {
  const canvas = viewer.get("canvas");
  const layer = canvas.getLayer("atlas-heat", -1);
  while (layer.firstChild) layer.removeChild(layer.firstChild);
  canvas.getContainer().classList.remove("heat-glow");
}

// heatStripHTML is the strip over the diagram: what it shows, how, over which period,
// and the scale — a colour means nothing until it says what its darkest end is worth.
export function heatStripHTML(prefs, { max, format, note }) {
  const mode = HEAT_MODES.find((m) => m.key === prefs.mode) || HEAT_MODES[0];
  const live = mode.key === "live";
  const btn = (attr, key, label, title, on) =>
    `<button type="button" data-${attr}="${key}" class="${on ? "active" : ""}" aria-pressed="${on}" title="${esc(title)}">${esc(label)}</button>`;
  const steps = [1, 2, 3, 4, 5].map((i) => `<i class="pg-heat-key-${i}"></i>`).join("");
  const scale = live ? "" : `
    <span class="pg-scale heat-scale" title="${esc(mode.title)}">
      <span class="muted">${mode.key === "runs" ? "1" : "short"}</span>
      ${prefs.style === "outline" ? steps : `<i class="heat-ramp"></i>`}
      <span class="muted">${max > 0 ? esc(format(max)) : "—"}</span>
      ${mode.key === "runs" ? `<i class="heat-cold-key"></i><span class="muted">never reached</span>` : `<span class="muted">median</span>`}
    </span>`;
  return `
    <span class="muted">Overlay</span>
    ${HEAT_MODES.map((m) => btn("heat-mode", m.key, m.label, m.title, m.key === mode.key)).join("")}
    ${live ? "" : `
      <span class="heat-sep"></span>
      <span class="muted">Style</span>
      ${HEAT_STYLES.map((s) => btn("heat-style", s.key, s.label, s.title, s.key === prefs.style)).join("")}
      <label class="heat-period"><span class="muted">Period</span>
        <select id="heat-period">${HEAT_PERIODS.map((p) =>
          `<option value="${p.key}"${p.key === prefs.period ? " selected" : ""}>${esc(p.label)}</option>`).join("")}</select></label>`}
    <span style="flex:1"></span>
    ${note ? `<span class="heat-note-inline">${esc(note)}</span>` : ""}
    ${scale}`;
}

// heatPanelHTML is the figures behind the colours, at the head of the side panel: the
// instances, the tasks with their runs and times, how each branching gateway split, and
// what the period never reached. It is built from the same values the diagram is
// shaded with, so the panel and the picture cannot disagree.
export function heatPanelHTML(registry, { mode, period, values, flows, rt, stats, error }) {
  const p = HEAT_PERIODS.find((x) => x.key === period) || HEAT_PERIODS[0];
  const runs = mode === "runs" ? { values, flows } : heatValues(registry, "runs", period, rt, stats);
  const byId = new Map(((stats && stats.elements) || []).map((e) => [e.elementId, e]));
  const head = `<div class="vp-head"><span class="vp-title">Statistics · ${esc(p.label)}</span></div>`;
  if (error) return `<div class="heat-panel">${head}<p class="err">${esc(error)}</p></div>`;

  const inst = (stats && stats.instances) || {};
  const all = period === "all";
  const running = all && rt ? rt.instances : inst.running;
  const finished = all && rt ? rt.finished : inst.finished;
  const started = all && rt ? (rt.instances || 0) + (rt.finished || 0) : inst.started;
  const tile = (label, value, sub = "") => `<div class="heat-kpi"><span class="muted">${esc(label)}</span>
    <b>${value == null ? "…" : esc(value)}</b>${sub ? `<span class="muted">${esc(sub)}</span>` : ""}</div>`;
  const dur = inst.duration;
  const kpis = `<div class="heat-kpis">
    ${tile("Started", started == null ? null : fmtCount(started))}
    ${tile("Finished", finished == null ? null : fmtCount(finished))}
    ${tile("Running", running == null ? null : fmtCount(running))}
    ${tile("Instance duration", stats ? (dur ? fmtMillis(dur.p50Millis) : "—") : null, dur ? `median · p90 ${fmtMillis(dur.p90Millis)}` : "")}
  </div>`;

  // The tasks, ordered by what the diagram shows: the busiest or the slowest first,
  // which is the ranking an operator opened the heatmap for.
  const field = (HEAT_MODES.find((m) => m.key === mode) || {}).field;
  const tasks = registry.filter((el) => isFlowNode(el) && ACTIVITY.test(el.type)).map((el) => {
    const e = byId.get(el.id) || {};
    return { id: el.id, name: nameOf(el), runs: runs.values.get(el.id) || 0, waiting: e.waiting, working: e.working, duration: e.duration };
  });
  const sortKey = (t) => (field ? (t[field] ? t[field].p50Millis : -1) : t.runs);
  tasks.sort((a, b) => sortKey(b) - sortKey(a) || a.name.localeCompare(b.name));
  const cell = (sum) => (sum ? `<span title="${esc(`${fmtCount(sum.count)} measured · mean ${fmtMillis(sum.meanMillis)} · p90 ${fmtMillis(sum.p90Millis)} · max ${fmtMillis(sum.maxMillis)} · total ${fmtMillis(sum.totalMillis)}`)}">${esc(fmtMillis(sum.p50Millis))}</span>` : `<span class="muted">—</span>`);
  const th = (key, label) => `<th class="num${(field || "runs") === key ? " sorted" : ""}">${esc(label)}</th>`;
  const table = tasks.length ? `
    <div class="heat-table-wrap"><table class="heat-table">
      <thead><tr><th>Task</th>${th("runs", "Runs")}${th("waiting", "Waiting")}${th("working", "Working")}${th("duration", "Duration")}</tr></thead>
      <tbody>${tasks.map((t) => `<tr class="${t.runs === 0 ? "cold" : ""}"><td>${esc(t.name)}</td>
        <td class="num">${fmtCount(t.runs)}</td><td class="num">${cell(t.waiting)}</td>
        <td class="num">${cell(t.working)}</td><td class="num">${cell(t.duration)}</td></tr>`).join("")}</tbody>
    </table></div>
    <p class="muted heat-foot">Times are medians over the task instances completed in the period. Waiting runs from
      activation until a person claimed the task or a Worker picked it up, working from then until completion; a task
      completed without being claimed has a duration but no split. Times are kept per task, never per person.</p>` : "";

  // How each branching gateway split its tokens, by the arrows that leave it.
  const branches = registry.filter((el) => isFlowNode(el) && /Gateway$/.test(el.type))
    .map((gw) => ({ gw, out: (gw.outgoing || []).filter(isFlow) }))
    .filter((b) => b.out.length > 1)
    .map(({ gw, out }) => {
      const rows = out.map((f) => ({ name: (f.businessObject && f.businessObject.name) || `→ ${nameOf(f.target)}`, n: runs.flows.get(f.id) || 0 }));
      const total = rows.reduce((s, r) => s + r.n, 0);
      return `<div class="heat-branch"><div class="muted">${esc(nameOf(gw))} · ${fmtCount(total)}</div>
        ${rows.map((r) => {
          const pct = total ? Math.round((100 * r.n) / total) : 0;
          return `<div class="heat-bar-row${r.n === 0 ? " cold" : ""}"><span>${esc(r.name)}</span>
            <span class="heat-bar"><i style="width:${pct}%"></i></span>
            <span class="num">${pct}% · ${fmtCount(r.n)}</span></div>`;
        }).join("")}</div>`;
    }).join("");

  const never = [
    ...[...runs.values].filter(([, v]) => v === 0).map(([id]) => nameOf(registry.get(id))),
    ...[...runs.flows].filter(([, v]) => v === 0).map(([id]) => {
      const f = registry.get(id);
      return `${nameOf(f.source)} → ${nameOf(f.target)}`;
    }),
  ];
  const shown = never.slice(0, 12);
  const neverHTML = never.length ? `
    <div class="heat-section"><div class="heat-sub">Never reached in this period</div>
      <ul class="heat-never">${shown.map((n) => `<li>${esc(n)}</li>`).join("")}</ul>
      ${never.length > shown.length ? `<p class="muted">and ${never.length - shown.length} more</p>` : ""}</div>` : "";

  const truncated = stats && stats.truncated
    ? `<p class="heat-warn">Read from the newest ${fmtCount(stats.scanned)} instances only — the period holds more.</p>` : "";
  return `<div class="heat-panel">${head}${truncated}${kpis}
    ${table ? `<div class="heat-section"><div class="heat-sub">Tasks</div>${table}</div>` : ""}
    ${branches ? `<div class="heat-section"><div class="heat-sub">Branches</div>${branches}</div>` : ""}
    ${neverHTML}</div>`;
}

// heatFormat is how a mode prints one of its values on a badge and the scale.
export const heatFormat = (mode) => (mode === "runs" ? (v) => fmtCount(v) : (v) => fmtMillis(v));

// Preferences are this browser's, like the live view's badge switches: which picture
// somebody reads a diagram in is about how *they* read, not about the process. Guarded,
// because a browser with site data blocked throws on the first touch of localStorage.
const PREFS_KEY = "atlas.live.heat";

export function loadHeatPrefs() {
  const def = { mode: "live", style: "glow", period: "all" };
  try {
    const raw = JSON.parse(localStorage.getItem(PREFS_KEY) || "{}");
    return {
      mode: HEAT_MODES.some((m) => m.key === raw.mode) ? raw.mode : def.mode,
      style: HEAT_STYLES.some((s) => s.key === raw.style) ? raw.style : def.style,
      period: HEAT_PERIODS.some((p) => p.key === raw.period) ? raw.period : def.period,
    };
  } catch {
    return def;
  }
}

export function saveHeatPrefs(prefs) {
  try { localStorage.setItem(PREFS_KEY, JSON.stringify(prefs)); } catch { /* not storable */ }
}

// heatStatsQuery is the statistics request for a period: since deployment reads all
// that is retained, a window starts that long before now.
export function heatStatsQuery(period, now = Date.now()) {
  const p = HEAT_PERIODS.find((x) => x.key === period);
  return p && p.ms ? `?from=${encodeURIComponent(new Date(now - p.ms).toISOString())}` : "";
}
