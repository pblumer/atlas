// The Multi-Instance Explorer (ADR-0517, OHS2): one multi-instance body execution
// of a process instance, browsed through the bounded history API rather than the
// full replay timeline.
//
// Everything here is a page the server cut: at most `limit` iterations are ever
// fetched or drawn, a filter is applied by the server's index, and a jump to an
// iteration number is a seek on the server, not a scroll through rows the browser
// holds. The DOM therefore never holds more than one page of rows, whatever the
// iteration count.
//
// The module has no state of its own between mounts and no dependency on the
// replay view: it is given the `api` function, an instance key and a body, and
// reports a selected iteration through a callback, so the replay can draw it.

import { t as tr } from "./i18n.js";
import { fmtCount } from "./numfmt.js";

// BOUNDED_HISTORY_THRESHOLD is the iteration count above which the replay does not
// load the full timeline. The legacy timeline grows with the square of the
// iteration count (139 MB at 1,000 iterations, 2.2 GB at 4,000 — ADR-0517), so this
// is a ceiling on harm, not a tuning knob.
export const BOUNDED_HISTORY_THRESHOLD = 500;

export const PAGE_SIZE = 100;

const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => (
  { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// Counts follow the viewer's chosen number format (Display settings).
const fmtNum = (n) => fmtCount(Number(n || 0));

const base = (key) => `/api/v1/instances/${encodeURIComponent(String(key))}/multi-instances`;

// unavailableCode reports why a history read could not be answered as a page:
// "history_unavailable" (the index is not verified complete), "cursor_stale" or
// "cursor_expired" (start again), or "" for any other failure.
function failureCode(err) {
  const code = err && err.body && err.body.code;
  return typeof code === "string" ? code : "";
}

// loadMIBodies reads the top-level (or one iteration's nested) multi-instance body
// executions of an instance. It never throws for an unavailable index: it reports
// it, because that answer means "not known", not "none".
export async function loadMIBodies(api, key, { parentIteration = 0, cursor = "" } = {}) {
  const q = new URLSearchParams();
  if (parentIteration) q.set("parentIteration", String(parentIteration));
  if (cursor) q.set("cursor", cursor);
  const qs = q.toString();
  try {
    const data = await api("GET", base(key) + (qs ? `?${qs}` : ""));
    return { available: true, ...data, bodies: data.bodies || [] };
  } catch (err) {
    if (failureCode(err) === "history_unavailable") {
      return { available: false, reason: err.message, bodies: [] };
    }
    throw err;
  }
}

// loadOutline reads one page of an instance's outline: its steps without those
// inside multi-instance iterations (ADR-0517). Like loadMIBodies it reports an
// unavailable outline instead of throwing, because that means "not known".
export async function loadOutline(api, key, { cursor = "" } = {}) {
  const qs = cursor ? `?cursor=${encodeURIComponent(cursor)}` : "";
  try {
    const data = await api("GET", `/api/v1/instances/${encodeURIComponent(String(key))}/outline${qs}`);
    return { available: true, ...data, steps: data.steps || [] };
  } catch (err) {
    if (failureCode(err) === "history_unavailable") return { available: false, steps: [] };
    throw err;
  }
}

// largestBody returns the number of iterations of the biggest body in a listing.
export function largestBody(bodies) {
  return (bodies || []).reduce((max, b) => Math.max(max, Number(b.iterations) || 0), 0);
}

function modeLabel(mode) {
  return tr(`ops.mi.mode.${["parallel", "sequential", "loop"].includes(mode) ? mode : "unknown"}`);
}

// bodySummaryHTML is the one-line summary a collapsed body shows wherever it is
// listed: counts the server keeps, never counts the browser derives.
export function bodySummaryHTML(body) {
  const parts = [
    tr("ops.mi.stat.completed", { count: fmtNum(body.completed) }),
    tr("ops.mi.stat.active", { count: fmtNum(body.active) }),
    tr("ops.mi.stat.terminated", { count: fmtNum(body.terminated) }),
  ];
  if (body.incidentsRaised) parts.push(tr("ops.mi.stat.incidents", { count: fmtNum(body.incidentsRaised) }));
  return `<span class="mi-summary">${parts.map(esc).join(" · ")}</span>`;
}

function iterationLabel(it) {
  return it.iteration == null ? tr("ops.mi.iteration.unknown") : `#${fmtNum(it.iteration)}`;
}

function stateClass(state) {
  return ["active", "completed", "terminated"].includes(state) ? state : "unknown";
}

// mountMIExplorer draws the explorer for one body into `root` and returns a handle
// with destroy(). onSelect(iterationDetail) is called when the operator opens an
// iteration; onOpenBody(body) when they open a body nested in one.
export function mountMIExplorer(root, { api, instanceKey, body, onSelect, onOpenBody, onClose }) {
  const st = {
    state: "",
    incidents: false,
    from: 0,
    cursor: "",
    stack: [], // cursors of the pages before this one, for "previous"
    rows: [],
    next: "",
    body,
    loading: false,
    error: null,
    notice: "",
    selected: "", // element-instance key of the open iteration, as a string
    detail: null,
    detailError: null,
    technical: false, // include rule traces: detail for the person who asks for it
  };
  let seq = 0; // the newest request; an older answer arriving late is dropped
  let destroyed = false;

  root.innerHTML = `
    <section class="mi-explorer" aria-label="${esc(tr("ops.mi.explorer.title"))}">
      <header class="mi-ex-head">
        <div class="mi-ex-titles">
          <h3 class="mi-ex-title"></h3>
          <div class="mi-ex-sub muted"></div>
        </div>
        <button type="button" class="btn neutral small mi-ex-close">${esc(tr("ops.mi.close"))}</button>
      </header>
      <div class="mi-ex-stats" aria-live="polite"></div>
      <form class="mi-ex-controls" novalidate>
        <label class="mi-ex-field"><span>${esc(tr("ops.mi.search.label"))}</span>
          <input type="number" min="1" step="1" inputmode="numeric" class="mi-ex-jump" placeholder="37421"/></label>
        <button type="submit" class="btn neutral small">${esc(tr("ops.mi.search.go"))}</button>
        <label class="mi-ex-field"><span>${esc(tr("ops.mi.filter.state"))}</span>
          <select class="mi-ex-state">
            <option value="">${esc(tr("ops.mi.filter.all"))}</option>
            <option value="active">${esc(tr("ops.mi.state.active"))}</option>
            <option value="completed">${esc(tr("ops.mi.state.completed"))}</option>
            <option value="terminated">${esc(tr("ops.mi.state.terminated"))}</option>
          </select></label>
        <label class="mi-ex-check"><input type="checkbox" class="mi-ex-inc"/> ${esc(tr("ops.mi.filter.incidents"))}</label>
      </form>
      <div class="mi-ex-notice" role="status" hidden></div>
      <div class="mi-ex-list" role="listbox" tabindex="0" aria-label="${esc(tr("ops.mi.list.label"))}"></div>
      <nav class="mi-ex-pager">
        <button type="button" class="btn neutral small mi-ex-first">${esc(tr("ops.mi.page.first"))}</button>
        <button type="button" class="btn neutral small mi-ex-prev">${esc(tr("ops.mi.page.prev"))}</button>
        <span class="mi-ex-range muted"></span>
        <button type="button" class="btn neutral small mi-ex-next">${esc(tr("ops.mi.page.next"))}</button>
      </nav>
      <div class="mi-ex-detail" aria-live="polite"></div>
    </section>`;

  const $ = (sel) => root.querySelector(sel);
  const listEl = $(".mi-ex-list");
  const noticeEl = $(".mi-ex-notice");

  function drawHead() {
    const b = st.body;
    $(".mi-ex-title").textContent = b.elementId || tr("ops.mi.explorer.title");
    $(".mi-ex-sub").textContent = `${tr("ops.mi.iterations", { count: fmtNum(b.iterations) })} · ${modeLabel(b.mode)}${
      b.state === "active" ? "" : ` · ${tr(`ops.mi.state.${stateClass(b.state)}`)}`}`;
    const cells = [
      ["completed", b.completed], ["active", b.active], ["terminated", b.terminated],
      ["incidents", b.incidentsRaised],
    ];
    if (b.openIncidents != null) cells.push(["open", b.openIncidents]);
    $(".mi-ex-stats").innerHTML = cells.map(([k, v]) =>
      `<div class="mi-stat ${k}"><span>${esc(tr(`ops.mi.statlabel.${k}`))}</span><b>${esc(fmtNum(v))}${
        k === "open" && b.openIncidentsExact === false ? "+" : ""}</b></div>`).join("");
  }

  function drawList() {
    // Redrawing replaces the rows; keep keyboard focus on the row that had it.
    const focused = document.activeElement && document.activeElement.closest &&
      document.activeElement.closest(".mi-row") ? document.activeElement.dataset.key : "";
    listEl.setAttribute("aria-busy", st.loading ? "true" : "false");
    if (st.error) {
      listEl.innerHTML = `<div class="mi-ex-error"><p>${esc(st.error)}</p>
        <button type="button" class="btn neutral small mi-ex-retry">${esc(tr("ops.mi.retry"))}</button></div>`;
      listEl.querySelector(".mi-ex-retry").addEventListener("click", () => load());
    } else if (!st.rows.length && !st.loading) {
      listEl.innerHTML = `<p class="ops-empty">${esc(tr(st.next ? "ops.mi.empty.budget" : "ops.mi.empty"))}</p>`;
    } else if (!st.rows.length) {
      listEl.innerHTML = `<p class="ops-empty">${esc(tr("ops.mi.loading"))}</p>`;
    } else {
      // Rows stay on screen while the next page loads, so paging does not flicker.
      listEl.innerHTML = st.rows.map((it) => {
        const open = it.openIncidents > 0;
        const inc = it.incidentsRaised
          ? `<span class="mi-row-inc${open ? " open" : ""}" title="${esc(tr(open ? "ops.mi.row.incidents.open" : "ops.mi.row.incidents.past", { count: it.incidentsRaised }))}">&#9888; ${esc(fmtNum(open ? it.openIncidents : it.incidentsRaised))}</span>`
          : "";
        const sel = String(it.elementInstanceKey) === st.selected;
        return `<div class="mi-row${sel ? " sel" : ""}" role="option" tabindex="-1"
            aria-selected="${sel}" data-key="${esc(it.elementInstanceKey)}">
          <span class="mi-row-n">${esc(iterationLabel(it))}</span>
          <span class="mi-row-state ${stateClass(it.state)}">${esc(tr(`ops.mi.state.${stateClass(it.state)}`))}</span>
          ${inc}
        </div>`;
      }).join("");
    }
    const first = st.rows[0], last = st.rows[st.rows.length - 1];
    $(".mi-ex-range").textContent = first && first.iteration != null && last.iteration != null
      ? tr("ops.mi.page.range", { from: fmtNum(first.iteration), to: fmtNum(last.iteration), size: PAGE_SIZE })
      : "";
    if (focused) {
      const again = [...listEl.querySelectorAll(".mi-row")].find((r) => r.dataset.key === focused);
      if (again) again.focus();
    }
    $(".mi-ex-prev").disabled = st.loading || !st.stack.length;
    $(".mi-ex-first").disabled = st.loading || (!st.stack.length && !st.from);
    $(".mi-ex-next").disabled = st.loading || !st.next;
  }

  function drawNotice() {
    noticeEl.hidden = !st.notice;
    noticeEl.textContent = st.notice;
  }

  function drawDetail() {
    const el = $(".mi-ex-detail");
    if (st.detailError) {
      el.innerHTML = `<p class="mi-ex-error">${esc(st.detailError)}</p>`;
      return;
    }
    const d = st.detail;
    if (!d) { el.innerHTML = ""; return; }
    const it = d.iteration;
    const incidents = d.incidents.length
      ? `<ul class="mi-inc-list">${d.incidents.map((inc) =>
          `<li class="${esc(inc.status)}"><b>${esc(tr(`ops.mi.incident.${inc.status}`))}</b>
            <span class="muted">${esc(tr("ops.mi.detail.incidentAt", { el: inc.elementInstanceKey, pos: inc.raisedPosition }))}</span></li>`).join("")}</ul>${
          d.incidentsTruncated ? `<p class="muted">${esc(tr("ops.mi.detail.truncated"))}</p>` : ""}`
      : `<p class="muted">${esc(tr("ops.mi.detail.noincidents"))}</p>`;
    const nested = d.nestedBodies.length
      ? `<h5>${esc(tr("ops.mi.detail.nested"))}</h5><ul class="mi-nested">${d.nestedBodies.map((b) =>
          `<li><span>${esc(b.elementId || b.bodyElementInstanceKey)}</span> ${bodySummaryHTML(b)}
            <button type="button" class="btn neutral small mi-open-nested" data-key="${esc(b.bodyElementInstanceKey)}">${esc(tr("ops.mi.explore"))}</button></li>`).join("")}</ul>`
      : "";
    const variables = d.variables && d.variables.length
      ? `<table class="mi-vars"><thead><tr><th>${esc(tr("ops.mi.detail.var.name"))}</th><th>${esc(tr("ops.mi.detail.var.value"))}</th><th>${esc(tr("ops.mi.detail.var.at"))}</th></tr></thead><tbody>${
          d.variables.map((v) => `<tr><td class="mono">${esc(v.name)}</td><td class="mono mi-var-${esc(v.kind)}">${esc(v.value)}</td><td class="mono muted">${esc(v.position)}</td></tr>`).join("")}</tbody></table>${
          d.variablesTruncated ? `<p class="muted">${esc(tr("ops.mi.detail.truncated.vars"))}</p>` : ""}`
      : `<p class="muted">${esc(tr("ops.mi.detail.novars"))}</p>`;
    const json = (x) => esc(JSON.stringify(x));
    const decisions = d.decisionsAvailable === false
      ? `<p class="muted">${esc(tr("ops.mi.detail.decisions.unavailable"))}</p>`
      : (d.decisions && d.decisions.length
        ? `<ul class="mi-dec-list">${d.decisions.map((x) => `<li><b class="mono">${esc(x.decisionId)}</b>
            <span class="muted">${esc(tr("ops.mi.detail.decision.at", { pos: x.position }))}</span>
            <div class="mi-dec-io"><span>${esc(tr("ops.mi.detail.decision.in"))}</span> <code>${json(x.inputs)}</code></div>
            <div class="mi-dec-io"><span>${esc(tr("ops.mi.detail.decision.out"))}</span> <code>${json(x.outputs)}</code></div>
            ${st.technical && x.trace ? `<pre class="mi-dec-trace">${esc(JSON.stringify(x.trace, null, 2))}</pre>` : ""}</li>`).join("")}</ul>${
            d.decisionsTruncated ? `<p class="muted">${esc(tr("ops.mi.detail.truncated.decisions"))}</p>` : ""}`
        : `<p class="muted">${esc(tr("ops.mi.detail.nodecisions"))}</p>`);
    el.innerHTML = `<h4>${esc(tr("ops.mi.detail.title", { n: iterationLabel(it) }))}
        <span class="mi-row-state ${stateClass(it.state)}">${esc(tr(`ops.mi.state.${stateClass(it.state)}`))}</span></h4>
      <dl class="mi-facts">
        <dt>${esc(tr("ops.mi.detail.key"))}</dt><dd class="mono">${esc(it.elementInstanceKey)}</dd>
        <dt>${esc(tr("ops.mi.detail.positions"))}</dt><dd class="mono">${esc(it.activationPosition)} &rarr; ${esc(it.endPosition || "…")}</dd>
      </dl>
      <h5>${esc(tr("ops.mi.detail.incidents"))}</h5>${incidents}
      <h5>${esc(tr("ops.mi.detail.variables"))}</h5>${variables}
      <h5>${esc(tr("ops.mi.detail.decisions"))}</h5>${decisions}${nested}
      <label class="mi-ex-check mi-ex-technical"><input type="checkbox" class="mi-ex-tech"${st.technical ? " checked" : ""}/> ${esc(tr("ops.mi.detail.technical"))}</label>
      <p class="muted mi-ex-later">${esc(tr("ops.mi.detail.replaynote"))}</p>`;
    const techBox = el.querySelector(".mi-ex-tech");
    if (techBox) techBox.addEventListener("change", () => { st.technical = techBox.checked; openIteration(st.selected); });
    el.querySelectorAll(".mi-open-nested").forEach((btn) => btn.addEventListener("click", () => {
      const nb = d.nestedBodies.find((b) => String(b.bodyElementInstanceKey) === btn.dataset.key);
      if (nb && onOpenBody) onOpenBody(nb);
    }));
  }

  function query() {
    const q = new URLSearchParams();
    q.set("limit", String(PAGE_SIZE));
    if (st.state) q.set("state", st.state);
    if (st.incidents) q.set("incidents", "true");
    if (st.from) q.set("from", String(st.from));
    if (st.cursor) q.set("cursor", st.cursor);
    return q.toString();
  }

  function restart(notice) {
    st.cursor = ""; st.stack = []; st.notice = notice || "";
    return load();
  }

  async function load() {
    const mine = ++seq;
    st.loading = true; st.error = null;
    drawList();
    try {
      const data = await api("GET", `${base(instanceKey)}/${encodeURIComponent(String(st.body.bodyElementInstanceKey))}/iterations?${query()}`);
      if (destroyed || mine !== seq) return;
      st.rows = data.iterations || [];
      st.next = data.nextCursor || "";
      if (data.body) { st.body = { ...st.body, ...data.body }; drawHead(); }
      if (st.from && !st.rows.length && !st.cursor) st.notice = tr("ops.mi.notfound", { n: fmtNum(st.from) });
    } catch (err) {
      if (destroyed || mine !== seq) return;
      const code = failureCode(err);
      if (code === "cursor_stale") { st.loading = false; return restart(tr("ops.mi.stale")); }
      if (code === "cursor_expired") { st.loading = false; return restart(tr("ops.mi.expired")); }
      st.rows = []; st.next = "";
      st.error = code === "history_unavailable" ? tr("ops.mi.unavailable") : tr("ops.mi.error", { message: err.message });
    } finally {
      if (!destroyed && mine === seq) {
        st.loading = false;
        drawNotice(); drawList();
      }
    }
  }

  async function openIteration(raw) {
    const key = String(raw);
    st.selected = key;
    drawList();
    st.detailError = null;
    try {
      const d = await api("GET", `${base(instanceKey)}/${encodeURIComponent(String(st.body.bodyElementInstanceKey))}/iterations/${encodeURIComponent(String(key))}${st.technical ? "?technical=true" : ""}`);
      if (destroyed || st.selected !== key) return;
      st.detail = d;
      drawDetail();
      if (onSelect) onSelect(d);
    } catch (err) {
      if (destroyed || st.selected !== key) return;
      st.detail = null;
      st.detailError = failureCode(err) === "history_unavailable" ? tr("ops.mi.unavailable") : tr("ops.mi.error", { message: err.message });
      drawDetail();
    }
  }

  $(".mi-ex-controls").addEventListener("submit", (e) => {
    e.preventDefault();
    const n = Number($(".mi-ex-jump").value);
    st.from = Number.isInteger(n) && n > 0 ? n : 0;
    restart("");
  });
  $(".mi-ex-state").addEventListener("change", (e) => { st.state = e.target.value; restart(""); });
  $(".mi-ex-inc").addEventListener("change", (e) => { st.incidents = e.target.checked; restart(""); });
  $(".mi-ex-next").addEventListener("click", () => {
    if (!st.next) return;
    st.stack.push(st.cursor); st.cursor = st.next; st.notice = "";
    load();
  });
  $(".mi-ex-prev").addEventListener("click", () => {
    if (!st.stack.length) return;
    st.cursor = st.stack.pop(); st.notice = "";
    load();
  });
  $(".mi-ex-first").addEventListener("click", () => { st.from = 0; $(".mi-ex-jump").value = ""; restart(""); });
  $(".mi-ex-close").addEventListener("click", () => { if (onClose) onClose(); });
  listEl.addEventListener("click", (e) => {
    const row = e.target.closest(".mi-row");
    if (row) openIteration(row.dataset.key);
  });
  // Keyboard: arrows move between rows, Enter or Space opens one, Home/End jump to
  // the ends of the page. Focus stays inside the list, which is one tab stop.
  listEl.addEventListener("keydown", (e) => {
    const rows = [...listEl.querySelectorAll(".mi-row")];
    if (!rows.length) return;
    const at = rows.indexOf(document.activeElement);
    let to = -1;
    if (e.key === "ArrowDown") to = Math.min(rows.length - 1, at + 1);
    else if (e.key === "ArrowUp") to = Math.max(0, at < 0 ? 0 : at - 1);
    else if (e.key === "Home") to = 0;
    else if (e.key === "End") to = rows.length - 1;
    else if ((e.key === "Enter" || e.key === " ") && at >= 0) {
      e.preventDefault();
      openIteration(rows[at].dataset.key);
      return;
    } else return;
    e.preventDefault();
    rows[to].focus();
  });

  drawHead();
  drawList();
  load();
  return {
    destroy() { destroyed = true; root.innerHTML = ""; },
    // Exposed for the replay's history: the explorer's own state is never shared.
    get selected() { return st.selected; },
  };
}
