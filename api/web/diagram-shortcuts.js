// Favourite and recent diagrams, across the Modeler, Data and Panorama
// (ADR-0460).
//
// Two things live here: the star a list row or the menu draws next to a diagram, and
// the top-bar menu that lists the stars and the diagrams opened last. Both read the
// same per-account record on the server — this file keeps no copy in the browser's
// storage, because a shortcut that does not follow a person to their other machine is
// half a shortcut.
//
// What a diagram *is* is the server's to answer: the Console names a kind and an id
// and gets back a name and the route that opens it, or nothing when the caller can no
// longer see it. So this file never builds a diagram's route itself, except to read
// which diagram a route the router already matched is showing.
//
// It takes its dependencies as arguments, like aiaccess.js, so the import stays
// one-directional: app.js imports this, never the other way round.

import { t } from "./i18n.js";

let deps = null;
// account is the id of whoever is signed in, empty with nobody. A mark belongs to an
// account, so with nobody signed in — or with enforcement off, where there is nobody
// to be — the stars and the menu are not drawn at all rather than drawn and refused.
// It is the id and not a yes or no because everything below is that one account's: a
// tab that signs out and somebody else signs in at must not keep the first list.
let account = "";
// marked is the set of "kind:id" the caller has starred, as the server last said.
let marked = new Set();
let last = { favourites: [], recent: [] };

const keyOf = (kind, id) => `${kind}:${id}`;

// installDiagramShortcuts wires the top-bar menu once. api, esc and toast are app.js's.
export function installDiagramShortcuts({ api, esc, toast }) {
  deps = { api, esc, toast };
  const btn = document.getElementById("shortcuts-btn");
  if (btn) {
    btn.title = t("shortcuts.title");
    btn.setAttribute("aria-label", t("shortcuts.title"));
    // Painted from what is known straight away, then refreshed: the menu opens on
    // the click (app.js's delegated dropdown), and a list that waited for the
    // network first would open empty.
    btn.addEventListener("click", () => { paintMenu(); refresh(); });
  }
  // Capture, so a star inside an open menu does not reach app.js's document click,
  // which closes every menu: pressing a star is not leaving the menu.
  document.addEventListener("click", onStarClick, true);
}

// setShortcutsAccount is called on every navigation, the login screen's included, with
// the id of whoever is signed in or nothing. A new account loads its record once; the
// rest of the time it is current from the answers to the writes. Any change of account
// first drops what the last one's record put in memory and in the menu's markup: the
// login screen keeps the top bar, and a sign-out must not leave the names of the
// diagrams somebody marked and opened one click — or one look at the page — away.
export function setShortcutsAccount(id) {
  const next = id ? String(id) : "";
  if (next === account) return;
  account = next;
  marked = new Set();
  last = { favourites: [], recent: [] };
  const menu = document.getElementById("shortcuts-menu");
  if (menu) menu.innerHTML = "";
  const wrap = document.getElementById("shortcuts-dropdown");
  if (wrap) wrap.hidden = !account;
  if (account) refresh();
}

// starButton is the star a row draws beside a diagram's name. Empty when nobody is
// signed in, so a list built before the state is known simply has no stars.
export function starButton(kind, id) {
  if (!account || !deps || !id) return "";
  const on = marked.has(keyOf(kind, id));
  const label = t(on ? "shortcuts.unmark" : "shortcuts.mark");
  return `<button type="button" class="dg-star${on ? " on" : ""}" data-dg-kind="${deps.esc(kind)}" ` +
    `data-dg-id="${deps.esc(id)}" aria-pressed="${on}" title="${deps.esc(label)}" aria-label="${deps.esc(label)}">` +
    `${on ? "★" : "☆"}</button>`;
}

// noteDiagramOpened records a visit for the route the router is about to show, if it
// shows a diagram. Fire and forget: nothing waits on history, and a visit that is
// refused (a diagram the caller cannot open) is simply not history.
export function noteDiagramOpened(path) {
  if (!account || !deps) return;
  const body = diagramOfRoute(path);
  if (!body) return;
  deps.api("POST", "/api/v1/diagram-shortcuts/recent", body).catch(() => {});
}

// diagramOfRoute reads which diagram a Console route opens, or null. The patterns are
// the router's own (app.js route()); "new" routes open nothing stored yet.
export function diagramOfRoute(path) {
  const dec = (s) => { try { return decodeURIComponent(s); } catch { return s; } };
  let m;
  if ((m = path.match(/^#\/modeler\/draft\/(.+)$/))) return { kind: "process", id: dec(m[1]) };
  if ((m = path.match(/^#\/modeler\/d\/(\d+)$/))) return { kind: "process", deploymentKey: Number(m[1]) };
  if ((m = path.match(/^#\/modeler\/dmn\/(?:e|d)\/([^/]+)/))) return { kind: "decision", id: dec(m[1]) };
  if (/^#\/modeler\/dmn\/new\b/.test(path)) return null;
  if ((m = path.match(/^#\/modeler\/dmn\/([^/]+)$/))) return { kind: "decision", id: dec(m[1]) };
  if ((m = path.match(/^#\/data\/m\/(.+)$/))) return { kind: "infomodel", id: dec(m[1]) };
  if ((m = path.match(/^#\/panorama\/models\/(.+)$/))) return { kind: "panorama", id: dec(m[1]) };
  return null;
}

async function refresh() {
  if (!account || !deps) return;
  const asked = account;
  try {
    adopt(await deps.api("GET", "/api/v1/diagram-shortcuts"), asked);
  } catch { /* the menu keeps what it last knew */ }
}

// adopt takes the server's answer as the truth and repaints every star on screen and
// the menu if it is open — unless the answer was asked for an account that has since
// signed out, which a slow answer arriving after the sign-out would otherwise restore.
function adopt(resp, asked) {
  if (asked !== account) return;
  last = { favourites: (resp && resp.favourites) || [], recent: (resp && resp.recent) || [] };
  marked = new Set(last.favourites.map((f) => keyOf(f.kind, f.id)));
  document.querySelectorAll(".dg-star").forEach(paintStar);
  const menu = document.getElementById("shortcuts-menu");
  if (menu && !menu.hidden) paintMenu();
}

function paintStar(b) {
  const on = marked.has(keyOf(b.dataset.dgKind, b.dataset.dgId));
  const label = t(on ? "shortcuts.unmark" : "shortcuts.mark");
  b.classList.toggle("on", on);
  b.setAttribute("aria-pressed", String(on));
  b.title = label;
  b.setAttribute("aria-label", label);
  b.textContent = on ? "★" : "☆";
}

async function onStarClick(e) {
  const b = e.target.closest(".dg-star");
  if (!b) return;
  e.preventDefault();
  e.stopPropagation();
  const { dgKind: kind, dgId: id } = b.dataset;
  const on = marked.has(keyOf(kind, id));
  // Optimistic, then corrected by the answer: the answer is the whole list, so a
  // refusal or another tab's change both land on screen the same way.
  if (on) marked.delete(keyOf(kind, id)); else marked.add(keyOf(kind, id));
  document.querySelectorAll(".dg-star").forEach(paintStar);
  const asked = account;
  try {
    adopt(await deps.api(on ? "DELETE" : "PUT",
      `/api/v1/diagram-shortcuts/favourites/${encodeURIComponent(kind)}/${encodeURIComponent(id)}`), asked);
  } catch (err) {
    await refresh();
    const msg = err && err.message ? err.message : String(err);
    deps.toast(t("shortcuts.failed", { reason: msg }), "err");
  }
}

const KIND_CHIP = { process: "BPMN", decision: "DMN", infomodel: "UML", panorama: "ARCHI" };

function paintMenu() {
  const menu = document.getElementById("shortcuts-menu");
  if (!menu || !deps || !account) return;
  const { esc } = deps;
  const row = (d, withStar) => {
    const chip = `<span class="chip dg-chip">${KIND_CHIP[d.kind] || esc(d.kind)}</span>`;
    if (!d.available) {
      return `<div class="dg-row dg-gone">${chip}<span class="dg-name muted" title="${esc(d.id)}">` +
        `${esc(t("shortcuts.unavailable"))}</span>${starButton(d.kind, d.id)}</div>`;
    }
    const where = d.deployed ? ` <span class="muted dg-sub">${esc(t("shortcuts.deployed"))}</span>` : "";
    return `<div class="dg-row"><a role="menuitem" href="${esc(d.href)}">${chip}` +
      `<span class="dg-name">${esc(d.name)}</span>${where}</a>${withStar ? starButton(d.kind, d.id) : ""}</div>`;
  };
  const favs = last.favourites.length
    ? last.favourites.map((d) => row(d, true)).join("")
    : `<span class="help-note">${esc(t("shortcuts.emptyFavourites"))}</span>`;
  const recent = last.recent.length
    ? last.recent.map((d) => row(d, true)).join("")
    : `<span class="help-note">${esc(t("shortcuts.emptyRecent"))}</span>`;
  menu.innerHTML = `<div class="mlabel">${esc(t("shortcuts.favourites"))}</div>${favs}` +
    `<div class="sep"></div><div class="mlabel">${esc(t("shortcuts.recent"))}</div>${recent}`;
}
