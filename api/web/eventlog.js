// The event log (ADR-0472): every signal thrown and message sent, and where
// each arrived, newest first.
//
// The Events page says what the deployed models could exchange; this page says what
// they did. It reads GET /api/v1/event-log, whose rows are folded from the engine's own
// records, and which hands each row through the rule that governs reading its instance
// (ADR-0275): an operator sees every row, a project member the rows of their projects.
//
// A send and what it reached are written while one command is processed, so their rows
// share a cause and stand next to each other: the page shows the send and, under it,
// the receptions, starts and the buffer's keeping. Live follows the log with the
// `after` cursor every few seconds; "Older" pages back with `before`.

import { fmtDateTime } from "./datefmt.js";

const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// LIVE_MS is how often Live asks for what came after the newest row it holds.
const LIVE_MS = 3000;
// KEEP is how many rows the page holds while following: the oldest go first.
const KEEP = 2000;

const SENDS = new Set(["signal.thrown", "message.sent"]);

// procHTML names an instance on its process: the process (a link to its deployed
// version), the element, and the instance (a link to it).
function procHTML(p) {
  if (!p) return `<span class="muted">—</span>`;
  const parts = [];
  if (p.definitionKey) {
    const label = p.processName || p.processId || `definition ${p.definitionKey}`;
    parts.push(`<a href="#/operations/p/${esc(p.definitionKey)}">${esc(label)}</a>${p.version ? ` <span class="muted">v${esc(p.version)}</span>` : ""}`);
  }
  if (p.elementId) parts.push(`<code>${esc(p.elementId)}</code>`);
  if (p.instanceKey) parts.push(`<a href="#/operations/i/${esc(p.instanceKey)}" class="chip" title="Open the instance">#${esc(p.instanceKey)}</a>`);
  return parts.join(" ") || `<span class="muted">—</span>`;
}

const SOURCE_TEXT = {
  process: "",
  publish: "published (route or atlas)",
  inbound: "an inbound watch",
  directed: "a directed delivery",
};

// whatHTML is the row's verb, and for a send how far it reached. A send that reached
// nobody is the case this page exists for, so it is marked — unless the buffer kept the
// message for a subscription that opens later, which the group says (kept).
function whatHTML(r, kept) {
  switch (r.kind) {
    case "signal.thrown":
    case "message.sent": {
      const verb = r.kind === "signal.thrown" ? "thrown" : "sent";
      const reach = `reached ${r.reached ?? 0}${r.started ? ` · started ${r.started}` : ""}`;
      const nobody = !r.reached && !r.started;
      return `<b>${verb}</b> <span class="muted">${reach}</span>${nobody
        ? (kept ? ` <span class="pill" title="Kept in the buffer for its time to live">kept</span>`
          : ` <span class="pill warn" title="Nothing waited for it: a ${r.channel} is not buffered, so it is lost">nobody received it</span>`)
        : ""}`;
    }
    case "signal.received":
    case "message.received":
      return "↳ received";
    case "signal.started":
    case "message.started":
      return "↳ started an instance";
    case "message.kept":
      return `↳ kept${r.expiresAt ? ` <span class="muted">until ${esc(fmtDateTime(r.expiresAt))}</span>` : ""}`;
    case "message.taken":
      return "↳ taken from the buffer";
    case "message.expired":
      return `<span class="pill warn" title="Its time to live passed while it waited: nothing ever received it">expired unreceived</span>`;
  }
  return esc(r.kind);
}

function fromHTML(r) {
  if (SENDS.has(r.kind)) {
    if (r.source === "process") return procHTML(r.sender);
    return `<span class="muted">${esc(SOURCE_TEXT[r.source] || r.source || "")}</span>`;
  }
  if (r.sender && r.sender.instanceKey) return procHTML(r.sender);
  return "";
}

function toHTML(r) {
  if (r.kind === "message.sent" && r.source === "directed") return procHTML(r.target);
  return r.receiver ? procHTML(r.receiver) : "";
}

// rowsHTML renders rows newest first, a reception indented under the send it shares
// a cause with.
function rowsHTML(rows) {
  return rows.map((r, i) => {
    const sameAsAbove = i > 0 && rows[i - 1].cause === r.cause;
    const kept = SENDS.has(r.kind) && rows.some((o) => o.cause === r.cause && o.kind === "message.kept");
    const nameCell = `<code>${esc(r.name)}</code>${r.correlationKey ? ` <span class="chip" title="Correlation key">${esc(r.correlationKey)}</span>` : ""}`;
    return `<tr class="el-row${sameAsAbove ? " el-child" : ""}" data-kind="${esc(r.kind)}" data-pos="${esc(r.position)}" data-cause="${esc(r.cause)}">
      <td class="muted" style="white-space:nowrap">${esc(fmtDateTime(r.at))}</td>
      <td><span class="chip">${esc(r.channel)}</span></td>
      <td>${sameAsAbove ? "" : nameCell}</td>
      <td>${whatHTML(r, kept)}</td>
      <td>${fromHTML(r)}</td>
      <td>${toHTML(r)}</td></tr>`;
  }).join("");
}

export async function viewEventLog({ api, view, isSuperseded }) {
  const params = new URLSearchParams((location.hash.split("?")[1] || ""));
  view.innerHTML = `
    <div class="between">
      <h1>Event log</h1>
      <span style="display:flex;gap:8px;align-items:center">
        <label title="Ask for new rows every few seconds"><input type="checkbox" id="el-live" checked> Live</label>
        <button class="btn neutral" id="el-refresh" title="Read the newest rows again">Refresh</button>
      </span>
    </div>
    <p class="muted">Every signal thrown and every message sent, and where each arrived, newest first: a
    throw with the waiting elements it reached and the instances it started, a message with its receiver,
    and what the buffer kept, handed over or let expire. A send that reached nobody is marked. Nothing an
    instance held is logged — names, keys and moments only. What the models <i>could</i> exchange is on the
    <a href="#/console/events">Events</a> page. You see the rows of the instances you may see; rows are kept
    for the server's retention (30 days unless the operator set another).</p>
    <div class="card">
      <div class="row" style="gap:8px;flex-wrap:wrap;margin-bottom:8px">
        <select id="el-channel" aria-label="Channel" style="width:auto"><option value="">All channels</option><option value="signal">Signals</option><option value="message">Messages</option></select>
        <input id="el-name" class="filter-input" type="search" placeholder="Name, exactly" aria-label="Name">
        <input id="el-instance" class="filter-input" type="search" placeholder="Instance key" aria-label="Instance key" inputmode="numeric">
        <button class="btn neutral" id="el-apply">Filter</button>
        <button class="btn ghost" id="el-clear">Clear</button>
        <span class="muted" id="el-status"></span>
      </div>
      <table data-dt-key="event-log">
        <thead><tr><th>When</th><th>Channel</th><th>Name</th><th>What</th><th>From</th><th>To</th></tr></thead>
        <tbody id="el-rows"><tr><td colspan="6" class="empty">Loading…</td></tr></tbody>
      </table>
      <div id="el-more" style="margin-top:8px"></div>
    </div>`;

  const $ = (id) => view.querySelector("#" + id);
  const channelEl = $("el-channel"), nameEl = $("el-name"), instEl = $("el-instance"), liveEl = $("el-live");
  if (["signal", "message"].includes(params.get("channel"))) channelEl.value = params.get("channel");
  nameEl.value = params.get("name") || "";
  instEl.value = params.get("instance") || "";

  let rows = [];
  let next = "", latest = "0", more = false;
  let timer = null;

  const query = (extra) => {
    const q = new URLSearchParams(extra);
    if (channelEl.value) q.set("channel", channelEl.value);
    if (nameEl.value) q.set("name", nameEl.value);
    if (instEl.value.trim()) q.set("instance", instEl.value.trim());
    const s = q.toString();
    return "/api/v1/event-log" + (s ? "?" + s : "");
  };

  const render = () => {
    $("el-rows").innerHTML = rows.length ? rowsHTML(rows)
      : `<tr><td colspan="6" class="empty">Nothing was sent${channelEl.value || nameEl.value || instEl.value ? " that matches the filter" : ""} — or nothing you may see.</td></tr>`;
    $("el-more").innerHTML = more ? `<button class="btn neutral" id="el-older">Older</button>` : "";
    const older = $("el-older");
    if (older) older.addEventListener("click", loadOlder);
  };

  const fail = (err) => {
    $("el-status").textContent = err && err.status === 400 ? (err.message || "Bad filter") : `Could not read the event log: ${err && err.message || err}`;
  };

  const load = async () => {
    $("el-status").textContent = "";
    let page;
    try { page = await api("GET", query()); } catch (err) { if (!isSuperseded()) fail(err); return; }
    if (isSuperseded() || !page) return;
    rows = page.rows || [];
    next = page.next || "";
    latest = page.latest || "0";
    more = !!page.more;
    render();
  };

  async function loadOlder() {
    let page;
    try { page = await api("GET", query({ before: next })); } catch (err) { fail(err); return; }
    if (isSuperseded() || !page) return;
    rows = rows.concat(page.rows || []);
    next = page.next || next;
    more = !!page.more;
    render();
  }

  // follow reads what came after the newest row held and puts it on top. A page cut at
  // its limit is read again at once on the next tick.
  const follow = async () => {
    if (isSuperseded()) { stop(); return; }
    let page;
    try { page = await api("GET", query({ after: latest })); } catch { return; }
    if (isSuperseded() || !page) return;
    latest = page.latest || latest;
    const fresh = page.rows || [];
    if (!fresh.length) return;
    rows = fresh.concat(rows).slice(0, KEEP);
    render();
  };

  const stop = () => { if (timer) { clearInterval(timer); timer = null; } };
  const start = () => { stop(); if (liveEl.checked) timer = setInterval(follow, LIVE_MS); };
  window.__atlasCleanup = stop;

  $("el-apply").addEventListener("click", () => { load(); });
  $("el-clear").addEventListener("click", () => { channelEl.value = ""; nameEl.value = ""; instEl.value = ""; load(); });
  $("el-refresh").addEventListener("click", () => { load(); });
  channelEl.addEventListener("change", () => { load(); });
  for (const el of [nameEl, instEl]) {
    el.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); load(); } });
  }
  liveEl.addEventListener("change", start);

  await load();
  if (!isSuperseded()) start();
}
