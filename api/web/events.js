// The events Atlas emits, in one place (ADR-0435).
//
// Atlas tells the world about itself through signals, messages, the CloudEvents feed
// and its log, and until this page nobody could answer "what can I listen to?" without
// reading the code. The catalogue is a list in Go, held to what the code emits by
// tests; this page reads it from GET /api/v1/event-catalog, which a modeler may read.
//
// Who listens now is a second, administrator-only route
// (GET /api/v1/event-catalog/listeners): it is a map of where personal data flows
// across every project. A modeler's page never receives it — the 403 is the answer,
// and the page simply has no "listening now" column then, rather than hiding one it
// was sent.

const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

const CHANNEL_WHAT = {
  signal: "a BPMN signal inside the engine: zero to many listeners, not buffered",
  message: "a BPMN message inside the engine, correlated to one receiver",
  feed: "the CloudEvents feed at /api/v1/events and its push delivery: durable, ordered, at least once",
  log: "a structured log line, named in the log catalogue",
};

// withheld says the server passes over this entry's events in the feed: a fact of the
// service catalogue on a server whose catalogue is switched off.
const withheld = (entry, ls) => !!(ls && ls.catalogueWithheld && entry.serviceCatalogue);

// listenersOf is everything listening to one entry: the deployed models on its name,
// and for a feed entry the feed subscriptions it reaches. A subscription receives every
// feed type it is not narrowed away from: a fact of the engine belongs to no catalogue,
// so a subscription narrowed to catalogues is not sent it, and none is sent what the
// server withholds.
function listenersOf(entry, ls) {
  if (!ls) return { processes: [], feed: [] };
  const processes = (ls.processes || []).filter((l) => l.type === entry.type);
  let feed = [];
  if ((entry.channels || []).includes("feed") && !withheld(entry, ls)) {
    feed = (ls.feed || []).filter((f) => entry.serviceCatalogue || !(f.reach || []).length);
  }
  return { processes, feed };
}

function chips(list) {
  return (list || []).map((c) => `<span class="chip" title="${esc(CHANNEL_WHAT[c] || "")}">${esc(c)}</span>`).join(" ");
}

function detailHTML(entry, ls) {
  const fields = (entry.payload || []).map((f) => `<tr>
      <td><code>${esc(f.name)}</code></td><td>${esc(f.type)}</td>
      <td>${f.always ? "always" : "optional"}</td>
      <td>${f.data === "personal" ? `<span class="pill warn" title="Personal data (ADR-0314): a receiver that keeps it keeps somebody's data">personal</span>` : ""}</td>
      <td>${esc((f.meaning || {}).en)}</td></tr>`).join("");
  const access = Object.entries(entry.access || {}).map(([ch, who]) =>
    `<li><b>${esc(ch)}</b> — ${esc(who)}</li>`).join("");
  const moment = entry.moment || {};
  const places = moment.places || [];
  const where = places.length
    ? places.map((p) => `<code>${esc(p.process)}</code> at <code>${esc(p.element)}</code>`).join(", ") + " — "
    : "";
  let listening = "";
  if (ls) {
    const { processes, feed } = listenersOf(entry, ls);
    const rows = processes.map((l) => `<tr>
        <td>${esc(l.processName || l.processId)} <span class="chip">${esc(l.processId)}</span>${l.system ? ` <span class="pill">system</span>` : ""}${l.inactive ? ` <span class="pill warn">inactive</span>` : ""}</td>
        <td>v${esc(l.version)}</td><td>${esc(l.projectId || "")}</td>
        <td><code>${esc(l.element)}</code> (${esc(l.role)})</td>
        <td>${(l.personal || []).map((p) => `<code>${esc(p)}</code>`).join(", ")}</td></tr>`).join("");
    const subs = feed.map((f) => `<tr>
        <td>${esc(f.workerName || f.workerId)}</td>
        <td>${(f.reach || []).length ? (f.reach || []).map(esc).join(", ") : "the whole feed"}</td>
        <td>${f.enabled ? "delivering" : "off"}</td></tr>`).join("");
    listening = `<div class="ev-listeners">
      <h3>Listening now</h3>
      ${processes.length ? `<table><thead><tr><th>Process</th><th>Version</th><th>Project</th><th>Element</th><th>Personal data it receives</th></tr></thead><tbody>${rows}</tbody></table>`
        : `<p class="muted">No deployed model listens to this name.</p>`}
      ${withheld(entry, ls) ? `<p class="muted ev-withheld">The service catalogue is switched off on this server: the feed passes over this event, and no subscription is sent it.</p>` : ""}
      ${feed.length ? `<h4>Feed subscriptions</h4>
        <table><thead><tr><th>Worker</th><th>Narrowed to</th><th>State</th></tr></thead><tbody>${subs}</tbody></table>` : ""}
    </div>`;
  }
  return `<div class="card ev-detail" style="margin-top:12px">
    <h2><code>${esc(entry.type)}</code></h2>
    <p>${esc((entry.meaning || {}).en)}</p>
    <p class="muted">${where}${esc(moment.producer)}</p>
    ${entry.serviceCatalogue ? `<p class="muted">Part of the service catalogue: a server whose catalogue is switched off does not emit it.</p>` : ""}
    <h3>Payload</h3>
    ${fields ? `<table><thead><tr><th>Field</th><th>Type</th><th></th><th></th><th>Meaning</th></tr></thead><tbody>${fields}</tbody></table>`
      : `<p class="muted">No fields: the event is its name.</p>`}
    <p class="muted">Never carries a secret — held by <code>${esc(entry.neverSecret)}</code>.</p>
    <h3>Who may receive it</h3><ul>${access}</ul>
    ${listening}
  </div>`;
}

export async function viewEvents({ api, view, isSuperseded }) {
  view.innerHTML = `
    <div class="between"><h1>Events</h1></div>
    <p class="muted">Everything atlas emits, in one place: the signals and messages of its system
    processes, the CloudEvents feed, and the log lines that belong to them. Each entry says what has
    happened when it is emitted, what it carries — personal data marked — and who may receive it.
    A model listens with a signal start or catch on the name; a system beyond atlas reads the feed.</p>
    <div class="card" style="padding:0">
      <table data-dt-key="events">
        <thead><tr id="ev-head"><th>Event</th><th>Kind</th><th>Channels</th><th>Meaning</th><th>Since</th><th>Stability</th></tr></thead>
        <tbody id="ev-rows"><tr><td colspan="6" class="empty">Loading…</td></tr></tbody>
      </table>
    </div>
    <div id="ev-detail"></div>`;

  let cat;
  try { cat = await api("GET", "/api/v1/event-catalog"); }
  catch (err) {
    if (isSuperseded()) return;
    view.querySelector("#ev-rows").innerHTML = `<tr><td colspan="6" class="empty">${
      esc(err.status === 403 ? "The event catalogue is available to modelers and administrators." : err.message)}</td></tr>`;
    return;
  }
  // Who listens: an administrator's. A refusal is the answer for everybody else.
  let ls = null;
  try { ls = await api("GET", "/api/v1/event-catalog/listeners"); } catch { ls = null; }
  if (isSuperseded()) return;

  const entries = (cat && cat.entries) || [];
  if (ls) view.querySelector("#ev-head").insertAdjacentHTML("beforeend", "<th>Listening now</th>");
  const cols = ls ? 7 : 6;
  view.querySelector("#ev-rows").innerHTML = entries.length ? entries.map((e, i) => {
    const n = ls ? (() => { const l = listenersOf(e, ls); return l.processes.length + l.feed.length; })() : 0;
    return `<tr data-type="${esc(e.type)}" data-i="${i}" style="cursor:pointer" tabindex="0">
      <td><code>${esc(e.type)}</code>${e.shaped ? ` <span class="pill" title="A shape: the product's author names each one">shape</span>` : ""}</td>
      <td>${esc(e.kind)}</td><td>${chips(e.channels)}</td>
      <td>${esc((e.meaning || {}).en)}</td><td>${esc(e.since)}</td>
      <td>${e.stability === "stable" ? "stable" : `<span class="pill warn">${esc(e.stability)}</span>`}</td>
      ${ls ? `<td class="ev-count">${n}</td>` : ""}</tr>`;
  }).join("") : `<tr><td colspan="${cols}" class="empty">The catalogue is empty.</td></tr>`;

  // An atlas.* name a model waits for that atlas never emits is a model that waits
  // forever, and the administrator is the one who can see every project's.
  if (ls) {
    const stray = (ls.processes || []).filter((l) => !l.catalogued);
    if (stray.length) {
      view.querySelector("#ev-detail").insertAdjacentHTML("beforebegin", `<div class="card ev-stray" style="margin-top:12px; border-color:var(--warn)">
        <b>Waiting for names atlas never emits.</b>
        <ul>${stray.map((l) => `<li><code>${esc(l.type)}</code> — ${esc(l.processName || l.processId)} v${esc(l.version)}, <code>${esc(l.element)}</code></li>`).join("")}</ul></div>`);
    }
  }

  const show = (i) => {
    view.querySelector("#ev-detail").innerHTML = detailHTML(entries[i], ls);
    view.querySelectorAll("#ev-rows tr").forEach((tr) => tr.classList.toggle("sel", tr.dataset.i === String(i)));
  };
  view.querySelector("#ev-rows").addEventListener("click", (e) => {
    const tr = e.target.closest("tr[data-i]");
    if (tr) show(Number(tr.dataset.i));
  });
  view.querySelector("#ev-rows").addEventListener("keydown", (e) => {
    const tr = e.target.closest("tr[data-i]");
    if (tr && (e.key === "Enter" || e.key === " ")) { e.preventDefault(); show(Number(tr.dataset.i)); }
  });
}
