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
//
// The page's second section is the installation's own signals and messages (ADR-0447,
// ADR-0446): what the deployed models throw, send and receive, derived from the
// definitions by GET /api/v1/signals and GET /api/v1/message-names, with what is wrong
// with a name. They are not in the catalogue, because the catalogue is atlas's
// contract and an own signal promises nothing; a modeler looking for a model's signal
// in the first list is the misunderstanding that section answers.
//
// Each section folds away on its own, and a row's detail opens in a dialog over the
// page. Drawn below its list, the detail of the second list opened out of sight, and
// nothing on screen said that the click had done anything.

import { locale, t, plural } from "./i18n.js";

const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// The page's words are the Console's catalogue's (ADR-0267), under events.*. A word
// for an id the server sends — a kind, a stability, a role, a finding's rule — is
// looked up by the id; go test ./api holds the catalogue to every id there is.
const tx = (key, params) => esc(t(key, params));
const word = (prefix, id) => tx(`${prefix}.${id}`);

// english marks a sentence the server composes in English, which this page shows as
// written, so a screen reader reads it in the right voice.
const english = (s) => `<span lang="en">${esc(s)}</span>`;

// said renders a catalogue sentence — an event's meaning, a payload field's — in the
// Console's language (ADR-0267): German unless English was chosen. The catalogue's
// tests hold every sentence to both languages; the fallback to the other one only
// keeps a gap from rendering as nothing. The page around it is still English; the sentence is
// catalogue content, like a model's name, not interface text. It carries its
// language, so a screen reader reads it in the right voice.
function said(text) {
  const t = text || {};
  const lang = [locale(), "en", "de"].find((l) => t[l]);
  return lang ? `<span lang="${lang}">${esc(t[lang])}</span>` : "";
}

// Whether a section is folded, per browser. Open unless folded here before; a browser
// whose storage throws keeps both open.
const FOLD_KEY = { catalog: "atlas.events.catalog", own: "atlas.events.own" };
const isFolded = (which) => {
  try { return localStorage.getItem(FOLD_KEY[which]) === "0"; } catch { return false; }
};
const rememberFold = (which, open) => {
  try { localStorage.setItem(FOLD_KEY[which], open ? "1" : "0"); } catch { /* not storable */ }
};

// closeDetail closes the dialog open now, if one is: there is one at a time.
let closeDetail = () => {};

// openDetail shows a row's detail in the Console's dialog frame. The dialog hangs off
// <body>, outside the view the router redraws, so a navigation closes it — a link
// inside it leads to another page, and the dialog must not stand over that page.
// Closing it otherwise returns the focus to the row it was opened from.
//
// It takes the focus itself rather than its Close button: a row opened with the space
// bar would otherwise hand the key's release to that button, and close again at once.
function openDetail({ title, body, cls, from }) {
  closeDetail();
  const ov = document.createElement("div");
  ov.className = "modal-ov";
  ov.innerHTML = `
    <div class="modal ev-modal ${cls}" role="dialog" aria-modal="true" aria-labelledby="ev-modal-title" tabindex="-1" lang="${esc(locale())}">
      <div class="modal-head">
        <h2 id="ev-modal-title">${title}</h2>
        <button type="button" class="icon-btn" data-x aria-label="${tx("common.close")}" title="${tx("common.close")}">✕</button>
      </div>
      <div class="modal-body">${body}</div>
      <div class="modal-foot"><button type="button" class="btn" data-done>${tx("common.close")}</button></div>
    </div>`;
  document.body.appendChild(ov);
  const close = (refocus) => {
    ov.remove();
    document.removeEventListener("keydown", onKey);
    window.removeEventListener("hashchange", onNav);
    closeDetail = () => {};
    if (refocus && from && from.isConnected) from.focus();
  };
  const onKey = (e) => { if (e.key === "Escape") close(true); };
  const onNav = () => close(false);
  document.addEventListener("keydown", onKey);
  window.addEventListener("hashchange", onNav);
  ov.addEventListener("mousedown", (e) => { if (e.target === ov) close(true); });
  ov.querySelector("[data-x]").addEventListener("click", () => close(true));
  ov.querySelector("[data-done]").addEventListener("click", () => close(true));
  closeDetail = () => close(false);
  ov.querySelector(".modal").focus();
}

// foldable wires a section's <details> to the browser's memory of it.
function foldable(el, which) {
  el.addEventListener("toggle", () => rememberFold(which, el.open));
}

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
  return (list || []).map((c) => `<span class="chip" title="${tx(`events.channel.${c}.what`)}">${esc(c)}</span>`).join(" ");
}

// roleText says what an element does with a name, and for a boundary or an event
// subprocess whether it interrupts.
function roleText(p, receiving) {
  const role = word("events.role", p.role);
  if (!receiving || p.interrupting === undefined) return role;
  return tx(p.interrupting ? "events.role.interrupting" : "events.role.nonInterrupting", { role: t(`events.role.${p.role}`) });
}

function detailHTML(entry, ls) {
  const fields = (entry.payload || []).map((f) => `<tr>
      <td><code>${esc(f.name)}</code></td><td>${esc(f.type)}</td>
      <td>${tx(f.always ? "events.detail.always" : "events.detail.optional")}</td>
      <td>${f.data === "personal" ? `<span class="pill warn" title="${tx("events.detail.personalTitle")}">${tx("events.detail.personal")}</span>` : ""}</td>
      <td>${said(f.meaning)}</td></tr>`).join("");
  const access = Object.entries(entry.access || {}).map(([ch, who]) =>
    `<li><b>${esc(ch)}</b> — ${english(who)}</li>`).join("");
  const moment = entry.moment || {};
  const places = moment.places || [];
  const where = places.length
    ? places.map((p) => t("events.detail.place", { process: `<code>${esc(p.process)}</code>`, element: `<code>${esc(p.element)}</code>` })).join(", ") + " — "
    : "";
  let listening = "";
  if (ls) {
    const { processes, feed } = listenersOf(entry, ls);
    const rows = processes.map((l) => `<tr>
        <td>${esc(l.processName || l.processId)} <span class="chip">${esc(l.processId)}</span>${l.system ? ` <span class="pill">${tx("events.pill.system")}</span>` : ""}${l.inactive ? ` <span class="pill warn">${tx("events.state.inactive")}</span>` : ""}</td>
        <td>v${esc(l.version)}</td><td>${esc(l.projectId || "")}</td>
        <td><code>${esc(l.element)}</code> (${roleText(l, false)})</td>
        <td>${(l.personal || []).map((p) => `<code>${esc(p)}</code>`).join(", ")}</td></tr>`).join("");
    const subs = feed.map((f) => `<tr>
        <td>${esc(f.workerName || f.workerId)}</td>
        <td>${(f.reach || []).length ? (f.reach || []).map(esc).join(", ") : tx("events.detail.wholeFeed")}</td>
        <td>${tx(f.enabled ? "events.state.delivering" : "events.state.off")}</td></tr>`).join("");
    listening = `<div class="ev-listeners">
      <h3>${tx("events.detail.listening")}</h3>
      ${processes.length ? `<table><thead><tr><th>${tx("events.detail.process")}</th><th>${tx("events.detail.version")}</th><th>${tx("events.detail.project")}</th><th>${tx("events.detail.element")}</th><th>${tx("events.detail.personalReceived")}</th></tr></thead><tbody>${rows}</tbody></table>`
        : `<p class="muted">${tx("events.detail.noListener")}</p>`}
      ${withheld(entry, ls) ? `<p class="muted ev-withheld">${tx("events.detail.withheld")}</p>` : ""}
      ${feed.length ? `<h4>${tx("events.detail.subscriptions")}</h4>
        <table><thead><tr><th>${tx("events.detail.worker")}</th><th>${tx("events.detail.narrowed")}</th><th>${tx("events.detail.state")}</th></tr></thead><tbody>${subs}</tbody></table>` : ""}
    </div>`;
  }
  return `<p>${said(entry.meaning)}</p>
    <p class="muted">${where}${english(moment.producer)}</p>
    ${entry.serviceCatalogue ? `<p class="muted">${tx("events.detail.serviceCatalogue")}</p>` : ""}
    <h3>${tx("events.detail.payload")}</h3>
    ${fields ? `<table><thead><tr><th>${tx("events.detail.field")}</th><th>${tx("events.detail.type")}</th><th></th><th></th><th>${tx("events.col.meaning")}</th></tr></thead><tbody>${fields}</tbody></table>`
      : `<p class="muted">${tx("events.detail.noFields")}</p>`}
    <p class="muted">${t("events.detail.neverSecret", { test: `<code>${esc(entry.neverSecret)}</code>` })}</p>
    <h3>${tx("events.detail.access")}</h3><ul>${access}</ul>
    ${listening}`;
}

// ownNames folds the two inventories into one list of names, each with its channel,
// who sends and who receives it, and its findings. A server older than the routes
// answers something that is not the inventory; that is an empty list, not an error.
function ownNames(signals, messages) {
  const out = [];
  for (const g of (signals && Array.isArray(signals.signals)) ? signals.signals : []) {
    const points = g.points || [];
    out.push({
      name: g.name, channel: "signal", findings: g.findings || [],
      senders: points.filter((p) => p.role === "throw"),
      receivers: points.filter((p) => p.role !== "throw"),
      others: [],
    });
  }
  for (const g of (messages && Array.isArray(messages.messages)) ? messages.messages : []) {
    const rows = g.sources || [];
    out.push({
      name: g.name, channel: "message", findings: g.findings || [],
      senders: rows.filter((r) => r.sourceKind === "process-throw"),
      receivers: rows.filter((r) => r.sourceKind === "process"),
      others: rows.filter((r) => r.sourceKind === "inbound-watch" || r.sourceKind === "product-action"),
    });
  }
  return out.sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : a.channel < b.channel ? -1 : 1));
}

// nameHTML shows an own name as the engine matches it. A name with a space at either
// end is another name than the one without, and a space is invisible in code, so it is
// shown quoted and said.
function nameHTML(name) {
  const n = String(name ?? "");
  if (n === n.trim()) return `<code>${esc(n)}</code>`;
  return `<code title="${tx("events.own.spaceTitle")}">"${esc(n)}"</code> <span class="pill warn">${tx("events.own.space")}</span>`;
}

// processesText names the processes of some points once each, the newest marked by
// nothing and an older version by its number: "billing, hr-notify (v1)".
function processesText(points) {
  const seen = new Map();
  for (const p of points) {
    const id = p.processId || "?";
    const c = seen.get(id) || { newest: false, versions: [] };
    if (p.newest) c.newest = true; else c.versions.push(p.version);
    seen.set(id, c);
  }
  return [...seen.entries()].sort((a, b) => (a[0] < b[0] ? -1 : 1)).map(([id, c]) =>
    `${esc(id)}${c.newest ? "" : ` <span class="muted">(v${[...new Set(c.versions)].map(esc).join(", v")})</span>`}`).join(", ");
}

// othersText names the senders that are not models: a Worker's watch, a product action.
function othersText(rows) {
  return rows.map((r) => r.sourceKind === "inbound-watch"
    ? `${tx("events.own.watchOf", { name: r.connectorName || r.connectorId || "?" })}${r.enabled ? "" : ` (${tx("events.state.off")})`}`
    : `${tx("events.own.actionOf", { action: r.action || "?", product: r.productName || r.productId || "?" })}${r.enabled ? "" : ` (${tx("events.state.inactive")})`}`).join(", ");
}

function pointRows(points, receiving) {
  return points.map((p) => `<tr>
      <td><a href="#/operations/p/${esc(p.definitionKey)}">${esc(p.processName || p.processId)}</a> <span class="chip">${esc(p.processId)}</span>${p.system ? ` <span class="pill">${tx("events.pill.system")}</span>` : ""}${(p.inactive || p.enabled === false) ? ` <span class="pill warn">${tx("events.state.inactive")}</span>` : ""}</td>
      <td>v${esc(p.version)}${p.newest ? ` <span class="pill">${tx("events.pill.newest")}</span>` : ""}</td>
      <td>${esc(p.projectId || "—")}</td>
      <td><code>${esc(p.elementId)}</code></td>
      <td>${roleText(p, receiving)}</td></tr>`).join("");
}

function ownDetailHTML(n) {
  const head = `<thead><tr><th>${tx("events.detail.process")}</th><th>${tx("events.detail.version")}</th><th>${tx("events.detail.project")}</th><th>${tx("events.detail.element")}</th><th>${tx("events.detail.role")}</th></tr></thead>`;
  // A finding's rule is an id and its sentence the server's own, in English.
  const findings = n.findings.length
    ? `<ul class="own-findings">${n.findings.map((f) => `<li><span class="pill warn">${esc(f.rule)}</span> ${english(f.message)}</li>`).join("")}</ul>`
    : `<p class="muted">${tx("events.own.noFinding")}</p>`;
  const others = n.others.length ? `<h3>${tx("events.own.others")}</h3><table><thead><tr><th>${tx("events.own.source")}</th><th>${tx("events.own.what")}</th><th>${tx("events.detail.state")}</th></tr></thead><tbody>${
    n.others.map((r) => r.sourceKind === "inbound-watch"
      ? `<tr><td>${t("events.own.watch", { name: `<b>${esc(r.connectorName || r.connectorId)}</b>`, kind: esc(r.kind) })}</td><td>${esc(r.description || "")}</td><td>${tx(r.enabled ? "events.state.on" : "events.state.off")}</td></tr>`
      : `<tr><td>${t("events.own.action", { action: `<b>${esc(r.action)}</b>`, product: esc(r.productName || r.productId) })}</td><td>${esc(r.effect || "")}</td><td>${tx(r.enabled ? "events.state.active" : "events.state.inactive")}</td></tr>`).join("")}</tbody></table>` : "";
  const signal = n.channel === "signal";
  return `${findings}
    <h3>${tx(signal ? "events.own.thrownBy" : "events.own.sentBy")}</h3>
    ${n.senders.length ? `<table>${head}<tbody>${pointRows(n.senders, false)}</tbody></table>` : `<p class="muted">${tx("events.own.noModel")}</p>`}
    ${others}
    <h3>${tx("events.own.receivedBy")}</h3>
    ${n.receivers.length ? `<table>${head}<tbody>${pointRows(n.receivers, true)}</tbody></table>` : `<p class="muted">${tx("events.own.noModel")}</p>`}
    <p class="muted">${t(signal ? "events.own.unseen.signal" : "events.own.unseen.message")}</p>
    <p><a href="#/operations/event-log?name=${encodeURIComponent(n.name)}&amp;channel=${n.channel}" class="own-log-link">${tx("events.own.log")}</a></p>
    <p class="muted">${tx(signal ? "events.own.versions.signal" : "events.own.versions.message")}</p>`;
}

export async function viewEvents({ api, view, isSuperseded }) {
  // The view is shared by every page and the Console's document says it is English,
  // so the page states its own language around itself.
  view.innerHTML = `<div lang="${esc(locale())}">
    <div class="between"><h1>${tx("events.title")}</h1></div>
    <p class="muted">${tx("events.intro")}</p>
    <details class="ev-section" id="ev-section"${isFolded("catalog") ? "" : " open"}>
      <summary><h2>${tx("events.catalog.title")}</h2><span class="muted small" id="ev-sum"></span></summary>
      <p class="muted">${tx("events.catalog.intro")}</p>
      <div class="card" id="ev-card" style="padding:0">
        <table data-dt-key="events">
          <thead><tr id="ev-head"><th>${tx("events.col.event")}</th><th>${tx("events.col.kind")}</th><th>${tx("events.col.channels")}</th><th>${tx("events.col.meaning")}</th><th>${tx("events.col.since")}</th><th>${tx("events.col.stability")}</th></tr></thead>
          <tbody id="ev-rows"><tr><td colspan="6" class="empty">${tx("events.loading")}</td></tr></tbody>
        </table>
      </div>
    </details>
    <details class="ev-section" id="own-section"${isFolded("own") ? "" : " open"}>
      <summary><h2 id="own-title">${tx("events.own.title")}</h2><span class="muted small" id="own-sum"></span></summary>
      <p class="muted">${t("events.own.intro")}</p>
      <div class="row" style="gap:12px;align-items:center;margin-bottom:8px">
        <label>${tx("events.own.channel")} <select id="own-channel"><option value="">${tx("events.own.channel.all")}</option><option value="signal">${tx("events.own.channel.signal")}</option><option value="message">${tx("events.own.channel.message")}</option></select></label>
        <label><input type="checkbox" id="own-findings"> ${tx("events.own.onlyFindings")}</label>
      </div>
      <div class="card" style="padding:0">
        <table data-dt-key="own-events">
          <thead><tr><th>${tx("events.own.col.name")}</th><th>${tx("events.own.col.channel")}</th><th>${tx("events.own.col.senders")}</th><th>${tx("events.own.col.receivers")}</th><th>${tx("events.own.col.findings")}</th></tr></thead>
          <tbody id="own-rows"><tr><td colspan="5" class="empty">${tx("events.loading")}</td></tr></tbody>
        </table>
      </div>
    </details></div>`;
  foldable(view.querySelector("#ev-section"), "catalog");
  foldable(view.querySelector("#own-section"), "own");

  // The installation's own names are asked for beside the catalogue: neither waits
  // for the other, and a failure of one leaves the other's section standing.
  const settle = (p) => Promise.resolve(p).then((v) => ({ v }), (err) => ({ err }));
  const ownReq = Promise.all([settle(api("GET", "/api/v1/signals")), settle(api("GET", "/api/v1/message-names"))]);

  let cat;
  try { cat = await api("GET", "/api/v1/event-catalog"); }
  catch (err) {
    if (isSuperseded()) return;
    view.querySelector("#ev-rows").innerHTML = `<tr><td colspan="6" class="empty">${
      err.status === 403 ? tx("events.catalog.forbidden") : esc(err.message)}</td></tr>`;
    view.querySelector("#own-rows").innerHTML = `<tr><td colspan="5" class="empty">${
      err.status === 403 ? tx("events.own.forbidden") : ""}</td></tr>`;
    return;
  }
  // Who listens: an administrator's. A refusal is the answer for everybody else.
  let ls = null;
  try { ls = await api("GET", "/api/v1/event-catalog/listeners"); } catch { ls = null; }
  if (isSuperseded()) return;

  const entries = (cat && cat.entries) || [];
  if (ls) view.querySelector("#ev-head").insertAdjacentHTML("beforeend", `<th>${tx("events.col.listening")}</th>`);
  const cols = ls ? 7 : 6;
  view.querySelector("#ev-rows").innerHTML = entries.length ? entries.map((e, i) => {
    const n = ls ? (() => { const l = listenersOf(e, ls); return l.processes.length + l.feed.length; })() : 0;
    return `<tr data-type="${esc(e.type)}" data-i="${i}" style="cursor:pointer" tabindex="0">
      <td class="nowrap"><code>${esc(e.type)}</code>${e.shaped ? ` <span class="pill" title="${tx("events.shape.title")}">${tx("events.shape")}</span>` : ""}</td>
      <td>${word("events.kind", e.kind)}</td><td>${chips(e.channels)}</td>
      <td>${said(e.meaning)}</td><td>${e.since === "Unreleased" ? tx("events.since.unreleased") : esc(e.since)}</td>
      <td>${e.stability === "stable" ? word("events.stability", e.stability) : `<span class="pill warn">${word("events.stability", e.stability)}</span>`}</td>
      ${ls ? `<td class="ev-count">${n}</td>` : ""}</tr>`;
  }).join("") : `<tr><td colspan="${cols}" class="empty">${tx("events.catalog.empty")}</td></tr>`;

  // An atlas.* name a model waits for that atlas never emits is a model that waits
  // forever, and the administrator is the one who can see every project's. The
  // section's summary says so too, so folding the section does not fold the warning.
  const stray = ls ? (ls.processes || []).filter((l) => !l.catalogued) : [];
  if (stray.length) {
    view.querySelector("#ev-card").insertAdjacentHTML("afterend", `<div class="card ev-stray" style="margin-top:12px; border-color:var(--warn)">
      <b>${tx("events.catalog.strayTitle")}</b>
      <ul>${stray.map((l) => `<li><code>${esc(l.type)}</code> — ${esc(l.processName || l.processId)} v${esc(l.version)}, <code>${esc(l.element)}</code></li>`).join("")}</ul></div>`);
  }
  view.querySelector("#ev-sum").innerHTML = `${esc(plural("events.catalog.sum", entries.length))}${stray.length
    ? ` · <span class="ev-sum-warn">${esc(plural("events.catalog.stray", stray.length))}</span>` : ""}`;

  const show = (i, from) => {
    openDetail({ title: `<code>${esc(entries[i].type)}</code>`, body: detailHTML(entries[i], ls), cls: "ev-detail", from });
    view.querySelectorAll("#ev-rows tr").forEach((tr) => tr.classList.toggle("sel", tr.dataset.i === String(i)));
  };
  view.querySelector("#ev-rows").addEventListener("click", (e) => {
    const tr = e.target.closest("tr[data-i]");
    if (tr) show(Number(tr.dataset.i), tr);
  });
  view.querySelector("#ev-rows").addEventListener("keydown", (e) => {
    const tr = e.target.closest("tr[data-i]");
    if (tr && (e.key === "Enter" || e.key === " ")) { e.preventDefault(); show(Number(tr.dataset.i), tr); }
  });

  const [sig, msg] = await ownReq;
  if (isSuperseded()) return;
  renderOwn(view, sig, msg);
}

// renderOwn fills the page's second section. A link from the Modeler names the name
// and its channel in the hash (#/console/events?name=…&channel=…), and the section
// unfolds and opens it.
function renderOwn(view, sig, msg) {
  const rowsEl = view.querySelector("#own-rows");
  const failed = [sig, msg].find((r) => r.err);
  if (failed) {
    rowsEl.innerHTML = `<tr><td colspan="5" class="empty">${failed.err.status === 403
      ? tx("events.own.forbidden") : esc(failed.err.message)}</td></tr>`;
    return;
  }
  const names = ownNames(sig.v, msg.v);
  const withFinding = names.filter((n) => n.findings.length).length;
  view.querySelector("#own-sum").innerHTML = `${esc(plural("events.own.sum", names.length))}${withFinding
    ? ` · <span class="ev-sum-warn">${esc(plural("events.own.sumFindings", withFinding))}</span>` : ""}`;
  const channelEl = view.querySelector("#own-channel");
  const findingsEl = view.querySelector("#own-findings");
  const params = new URLSearchParams((location.hash.split("?")[1] || ""));
  const want = { name: params.get("name") || "", channel: params.get("channel") || "" };
  if (want.channel === "signal" || want.channel === "message") channelEl.value = want.channel;

  const draw = () => {
    const ch = channelEl.value;
    const shown = names.map((n, i) => ({ n, i })).filter(({ n }) =>
      (!ch || n.channel === ch) && (!findingsEl.checked || n.findings.length));
    rowsEl.innerHTML = shown.length ? shown.map(({ n, i }) => `<tr data-own="${i}" data-name="${esc(n.name)}" data-channel="${n.channel}" style="cursor:pointer" tabindex="0">
        <td class="nowrap">${nameHTML(n.name)}</td>
        <td><span class="chip">${esc(n.channel)}</span></td>
        <td>${[processesText(n.senders), othersText(n.others)].filter(Boolean).join(", ") || `<span class="muted">—</span>`}</td>
        <td>${processesText(n.receivers) || `<span class="muted">—</span>`}</td>
        <td>${n.findings.map((f) => `<span class="pill warn" title="${esc(f.message)}">${word("events.finding", f.rule)}</span>`).join(" ")}</td></tr>`).join("")
      : `<tr><td colspan="5" class="empty">${tx(names.length ? "events.own.noMatch" : "events.own.none")}</td></tr>`;
  };
  const show = (i, from) => {
    const n = names[i];
    openDetail({ title: `${nameHTML(n.name)} <span class="chip">${esc(n.channel)}</span>`, body: ownDetailHTML(n), cls: "own-detail", from });
    view.querySelectorAll("#own-rows tr").forEach((tr) => tr.classList.toggle("sel", tr.dataset.own === String(i)));
  };
  channelEl.addEventListener("change", draw);
  findingsEl.addEventListener("change", draw);
  rowsEl.addEventListener("click", (e) => {
    const tr = e.target.closest("tr[data-own]");
    if (tr && !e.target.closest("a")) show(Number(tr.dataset.own), tr);
  });
  rowsEl.addEventListener("keydown", (e) => {
    const tr = e.target.closest("tr[data-own]");
    if (tr && (e.key === "Enter" || e.key === " ")) { e.preventDefault(); show(Number(tr.dataset.own), tr); }
  });
  draw();
  if (want.name) {
    const i = names.findIndex((n) => n.name === want.name && (!want.channel || n.channel === want.channel));
    if (i >= 0) {
      // The row stays where the dialog closes onto, so its section is unfolded for it.
      view.querySelector("#own-section").open = true;
      const tr = rowsEl.querySelector(`tr[data-own="${i}"]`);
      if (tr) tr.scrollIntoView({ block: "center" });
      show(i, tr);
    }
  }
}
