// Atlas web UI — buildless app shell (ADR-0012). A tiny hash router swaps views
// into #view; heavy widgets (the BPMN modeler) are loaded on demand by editor.js.

import {
  PRESETS, normalizeHex, currentAccent, applyAccent, applyCurrent,
  setServerAccent, resetServerAccent, syncFromServer,
} from "./theme.js";
import {
  LOGO_URL, BUILTIN_MARK, hasLogoCached, applyLogo, syncLogoFromServer,
  setServerLogo, deleteServerLogo,
} from "./logo.js";
import { enhanceTable } from "./table.js";
import { copyText } from "./clipboard.js";
import {
  incidentPill, fmtRaised, resolveIncidentFlow, fixVariablesFlow, fixConnectorFlow,
  incidentConnectorChip,
  repairFormFlow,
} from "./incidents.js";
import { editConnectorFlow, connectorShape, connectorCreateBody, connectorUsageHTML, openConnectorUsage, deleteConnectorFlow } from "./connectordialog.js";
import { migrateProcessFlow } from "./migrationdialog.js";
// The form-js viewer is shared with the incident's repair form (ADR-0169), so its lazy
// import and one-time stylesheet injection live in one module rather than here.
import { loadFormViewer } from "./formviewer.js";
import { secretShapeFor, checkSecretValue, secretHintHTML, secretValueFieldHTML } from "./secret-shapes.js";
// Giving an AI assistant access, as a screen (ADR-0200). Its own module because it
// is a self-contained page and this file is long enough; it takes its dependencies
// as arguments so the import stays one-directional, like every other module here.
import { viewAIAccess } from "./aiaccess.js";

const view = document.getElementById("view");

// navGen guards the async router against re-entrancy. route() is async and
// re-fires on every hashchange, so two navigations can be in flight at once. Each
// bumps navGen; a view handler that renders (or installs a poll timer, or claims
// window.__atlasCleanup) only *after* an await captures navGen at entry and bails
// if a newer navigation has since landed — otherwise its late write clobbers the
// newer view or leaks a timer. editor.js has its own equivalent guard for the
// mounts it owns; this covers the plain view* handlers and the pre-mount awaits in
// their app.js wrappers, which that guard can't see.
let navGen = 0;
const superseded = (gen) => gen !== navGen;

// ---------- API ----------
// apiRaw is the fetch wrapper that also returns the response headers, for the few
// endpoints whose headers carry pagination signals (X-Tasks-Truncated /
// X-Tasks-Next-Cursor). Most callers want just the body — see api().
export async function apiRaw(method, path, body, isXML) {
  const opts = { method };
  if (body !== undefined) {
    opts.body = isXML ? body : JSON.stringify(body);
    opts.headers = { "Content-Type": isXML ? "application/xml" : "application/json" };
  }
  const res = await fetch(path, opts);
  const text = await res.text();
  let data = text;
  try { data = text ? JSON.parse(text) : null; } catch { /* keep text */ }
  if (!res.ok) {
    // The message is the readable half; the status and the decoded body ride along for
    // the few callers that need to *act* on the failure rather than report it — a 409
    // that names what is in the way, say (ADR-0163).
    const err = new Error((data && data.error) || res.statusText);
    err.status = res.status;
    err.body = data;
    throw err;
  }
  return { data, headers: res.headers };
}

export async function api(method, path, body, isXML) {
  return (await apiRaw(method, path, body, isXML)).data;
}

export function toast(msg, kind) {
  const t = document.getElementById("toast");
  t.textContent = msg; t.className = kind || ""; t.hidden = false;
  clearTimeout(toast._t);
  toast._t = setTimeout(() => { t.hidden = true; }, 3200);
}

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

const fmtTime = (unix) => unix ? new Date(unix * 1000).toLocaleString() : "—";

// ---------- Auth ----------
// AUTH mirrors GET /api/v1/auth/me: whether login is enforced and, if so, who is
// signed in. It gates the whole app in route() and drives the account menu. When
// enforcement is off (the default single-binary build) enabled is false and the
// app behaves exactly as before.
let AUTH = { enabled: false, user: null, loaded: false };

async function loadAuth() {
  try {
    const m = await api("GET", "/api/v1/auth/me");
    AUTH = { enabled: !!(m && m.authEnabled), user: (m && m.user) || null, loaded: true };
  } catch {
    // A 401 from /auth/me means enforcement is on and nobody is signed in.
    AUTH = { enabled: true, user: null, loaded: true };
  }
}

// mayUse reports whether the signed-in person holds a role, which is what the
// server asks of every route they are about to reach
// (ADR-0209). It drives the navigation only: the API is
// the authority, and this keeps the Console from offering a screen whose every
// call would come back 403.
//
// With enforcement off there is nobody to hold a role and nothing is enforced, so
// everything is offered — the same answer the server gives.
const mayUse = (role) => {
  if (!role || role === "any" || !AUTH.enabled) return true;
  const roles = (AUTH.user && AUTH.user.roles) || [];
  return roles.includes("admin") || roles.includes(role);
};

const initials = (name) => {
  const s = String(name || "").trim();
  if (!s) return "?";
  const p = s.split(/\s+/);
  return (p.length > 1 ? p[0][0] + p[1][0] : s.slice(0, 2)).toUpperCase();
};

// updateAccount reflects the signed-in user in the top-bar avatar and its menu.
function updateAccount() {
  const btn = document.querySelector(".topbar .avatar");
  const menu = window.__acctMenu;
  if (!btn) return;
  if (AUTH.enabled && AUTH.user) {
    const label = AUTH.user.displayName || AUTH.user.username;
    btn.textContent = initials(label);
    btn.title = label;
    if (menu) menu.innerHTML =
      `<div class="mlabel">Signed in as <b>${esc(AUTH.user.username)}</b></div>` +
      `<button type="button" data-act="logout" title="Sign out of Atlas">Log out</button>`;
  } else {
    // Nobody is signed in — which is two different things. With enforcement off the
    // server is open and there is nothing to sign in to; with it on, this is the
    // login screen, and calling that "single-user mode" tells an operator their
    // server has no login when it is refusing them one. The title told them apart
    // already; the menu did not.
    const label = AUTH.enabled ? "Not signed in" : "Single-user mode";
    btn.textContent = "A";
    btn.title = AUTH.enabled ? "Account" : "Single-user mode";
    if (menu) menu.innerHTML = `<div class="mlabel">${label}</div>`;
  }
}

async function logout() {
  try { await api("POST", "/api/v1/auth/logout"); } catch { /* already gone */ }
  await loadAuth();
  location.hash = "#/console";
  route();
}

// viewLogin is the sign-in screen shown whenever enforcement is on and no session
// is active. A successful login re-reads auth and drops the user on the Console.
function viewLogin() {
  view.innerHTML = `
    <div class="card" style="max-width:380px; margin:8vh auto">
      <h1>Sign in</h1>
      <p class="muted">This Atlas instance requires you to sign in.</p>
      <p id="sso-error" class="muted err" hidden>Signing in with your identity provider did not work. Try again, or ask an administrator to check the server log.</p>
      <div id="sso-providers" hidden></div>
      <form id="login-form">
        <label class="field">Username
          <input name="username" autocomplete="username" autofocus required></label>
        <label class="field">Password
          <input name="password" type="password" autocomplete="current-password" required></label>
        <div class="row" style="margin-top:6px"><button class="btn" type="submit" title="Sign in with the username and password above">Sign in</button></div>
        <p id="login-error" class="muted" hidden></p>
      </form>
      <div style="margin-top:10px">
        <button type="button" id="forgot-password" class="linklike" title="Show how to recover your password">Forgot password?</button>
        <p id="forgot-help" class="muted" style="margin-top:6px" hidden>
          Atlas doesn't send password-reset emails. An administrator resets
          passwords for this instance &mdash; ask yours to set a new one for your
          account, then sign in with it.
        </p>
      </div>
      <p id="register-line" class="muted" style="margin-top:10px" hidden>
        Noch kein Konto? <a id="register-link" href="#">Registrieren</a>
      </p>
    </div>`;
  // A federated login, when an operator configured one
  // (ADR-0210). The endpoint is public and answers an
  // empty list on an instance with no provider, which is the ordinary case — so
  // nothing appears unless there is somewhere to go, and a server that cannot
  // answer leaves the password form exactly as it is.
  //
  // The callback sends a failed attempt back here with ?sso=failed and no reason.
  // The reason is in the server's audit log, where an operator can read it, rather
  // than in a URL anybody could send somebody.
  if (new URLSearchParams(location.search).get("sso") === "failed") {
    document.getElementById("sso-error").hidden = false;
  }
  (async () => {
    try {
      const providers = await api("GET", "/api/v1/auth/providers");
      if (!Array.isArray(providers) || providers.length === 0) return;
      const host = document.getElementById("sso-providers");
      host.innerHTML = providers.map((p) =>
        `<a class="btn" style="display:block; text-align:center; margin-bottom:8px" href="${esc(p.start)}"` +
        ` title="Sign in with ${esc(p.name)}">Sign in with ${esc(p.name)}</a>`).join("") +
        `<p class="muted" style="text-align:center; margin:0 0 10px">or sign in with a password</p>`;
      host.hidden = false;
    } catch { /* no provider, or the server could not say — the password form stands */ }
  })();
  // Self-service registration (ADR-0126): the login screen asks the server whether
  // registration is enabled and, if so, reveals a link to the public start form of
  // the configured intake process. The endpoint is public (served before login),
  // and a failure or a disabled instance simply leaves the link hidden.
  (async () => {
    try {
      const cfg = await api("GET", "/api/v1/settings/registration");
      if (cfg && cfg.enabled && cfg.url) {
        const line = document.getElementById("register-line");
        document.getElementById("register-link").setAttribute("href", cfg.url);
        line.hidden = false;
      }
    } catch { /* registration off or unreachable — leave the link hidden */ }
  })();
  const f = document.getElementById("login-form");
  // Password recovery on a self-hosted, admin-managed instance is an admin
  // action (POST /users/{id}/password), not a self-service email flow — there is
  // no transactional mail sender and email is optional per user (ADR-0044). So
  // this affordance points the user at the recovery path that actually exists
  // rather than a dead-end "invalid password" error.
  const forgot = document.getElementById("forgot-password");
  forgot.addEventListener("click", () => {
    const help = document.getElementById("forgot-help");
    help.hidden = !help.hidden;
    forgot.setAttribute("aria-expanded", String(!help.hidden));
  });
  f.addEventListener("submit", async (e) => {
    e.preventDefault();
    const fd = new FormData(f);
    const err = document.getElementById("login-error");
    err.hidden = true;
    try {
      await api("POST", "/api/v1/auth/login", {
        username: fd.get("username"), password: fd.get("password"),
      });
      await loadAuth();
      location.hash = "#/console";
      route();
    } catch {
      err.textContent = "Invalid username or password.";
      err.style.color = "var(--danger)";
      err.hidden = false;
    }
  });
}

// ---------- Dropdown menus ----------
// One delegated document click drives every dropdown: clicking a .dropdown-toggle
// opens its menu (closing others); clicking anywhere else closes them all.
function closeAllMenus() {
  for (const m of document.querySelectorAll(".dropdown-menu:not([hidden]):not(.submenu-menu)")) m.hidden = true;
  // Its flyouts go with it, and now rather than after the grace period: a menu that has
  // been dismissed must not leave one standing on screen.
  clearTimeout(submenuTimer);
  submenuTimer = null;
  closeSubmenus();
  openMenu = openTrigger = null;
}
// placeMenu puts an open menu under its trigger, or above it when there is no room
// below. Menus are position:fixed so no ancestor's overflow can clip them, which means
// CSS cannot place them — these are viewport coordinates. Right-aligned to the trigger,
// as the absolute rule used to be, and held inside the viewport.
function placeMenu(menu, trigger, preferAbove) {
  menu.style.left = menu.style.top = "";
  const t = trigger.getBoundingClientRect(), w = menu.offsetWidth, h = menu.offsetHeight;
  const fitsBelow = t.bottom + 4 + h <= window.innerHeight - 8, fitsAbove = t.top - 4 - h >= 8;
  // A menu that opened above its trigger stays above while that side still has room, so
  // scrolling moves it with the row rather than flipping it across the row.
  const above = preferAbove ? fitsAbove || !fitsBelow : !fitsBelow && fitsAbove;
  menu.style.left = `${Math.round(Math.max(8, Math.min(t.right - w, window.innerWidth - w - 8)))}px`;
  menu.style.top = `${Math.round(above ? Math.max(8, t.top - h - 4) : Math.min(t.bottom + 4, window.innerHeight - h - 8))}px`;
  return above;
}

// A fixed menu does not travel with the page, so it is re-placed when the page moves
// under it — not dismissed. Dismissing looked reasonable until the obvious case: the
// click that opens a row's menu near the bottom of a list first scrolls that row into
// view, and that scroll then closed the menu the same click had just opened. The menu
// is only given up once its trigger has left the viewport entirely. Scrolling *inside*
// a menu — a long flyout — moves nothing and is ignored.
let openMenu = null, openTrigger = null, openAbove = false;

function repositionMenus() {
  if (!openMenu || openMenu.hidden || !openTrigger || !openTrigger.isConnected) return;
  const t = openTrigger.getBoundingClientRect();
  if (t.bottom < 0 || t.top > window.innerHeight) { closeAllMenus(); return; }
  openAbove = placeMenu(openMenu, openTrigger, openAbove);
  for (const sm of openMenu.querySelectorAll(".submenu")) {
    const fly = sm.querySelector(".submenu-menu");
    if (fly && getComputedStyle(fly).display !== "none") placeSubmenu(sm);
  }
}
window.addEventListener("scroll", (e) => {
  if (e.target instanceof Element && e.target.closest(".dropdown-menu")) return;
  repositionMenus();
}, true);
window.addEventListener("resize", () => repositionMenus());

// placeSubmenu puts an open flyout beside its row. It is position:fixed so the card's
// overflow cannot clip it, which means CSS cannot place it — these are viewport
// coordinates. It opens to the *right*, which is where a submenu opens everywhere else
// and therefore where the hand goes, and flips to the left only when the right would
// run off screen (a row's action menu does sit at the right edge of a table). It is held
// inside the viewport vertically so a row near the bottom of a long list still shows a
// full list rather than a sliver.
//
// It is placed flush against the parent menu, with no gap. The flyout is shown by
// `.submenu:hover`, and hover follows the DOM — the flyout is a child of .submenu, so
// being over it keeps the pair open however far from its row it is painted. A gap
// between the two is the one thing that breaks that: crossing it, the pointer is over
// neither, and the flyout closed before the hand arrived.
function placeSubmenu(sm) {
  const menu = sm.querySelector(".submenu-menu"), parent = sm.closest(".dropdown-menu");
  if (!menu || !parent) return;
  const row = sm.getBoundingClientRect(), box = parent.getBoundingClientRect();
  const w = menu.offsetWidth, h = menu.offsetHeight;
  if (!w || !h) return; // not displayed yet — the hover rule has not applied
  const toLeft = box.right + w + 8 > window.innerWidth && box.left - w - 8 >= 0;
  const x = toLeft ? Math.max(8, box.left - w) : Math.min(box.right, window.innerWidth - w - 8);
  const top = Math.max(8, Math.min(row.top - 7, window.innerHeight - h - 8));
  // Which side it took drives the hover bridge — the few pixels of slack that keep a
  // diagonal reach for the flyout from falling between the two boxes.
  menu.classList.toggle("sm-left", toLeft);
  menu.style.left = `${Math.round(x)}px`;
  menu.style.top = `${Math.round(top)}px`;
}
// Which flyout is open is driven here rather than by `.submenu:hover`, because a hand
// moving from the row to the flyout cuts the corner: it crosses the rows in between, and
// every one of those is outside the pair. Under :hover the flyout closed under the hand
// before it arrived — reliably enough that reaching it was a knack. So it is held open
// for a moment after the pointer leaves, and the moment is cancelled the instant the
// pointer is back on the pair: the reach across is forgiven, and settling anywhere else
// still closes it, only a fraction of a second later.
const SUBMENU_GRACE_MS = 260;
let submenuTimer = null;

function closeSubmenus() {
  for (const sm of document.querySelectorAll(".submenu.sm-open")) sm.classList.remove("sm-open");
}

function openSubmenu(sm) {
  clearTimeout(submenuTimer);
  submenuTimer = null;
  for (const other of document.querySelectorAll(".submenu.sm-open")) {
    if (other !== sm) other.classList.remove("sm-open");
  }
  sm.classList.add("sm-open");
  placeSubmenu(sm);
}

// Starts the grace period, or leaves a running one alone — the pointer sweeping across
// a dozen elements must not keep pushing the deadline back for as long as it moves.
function closeSubmenusSoon() {
  if (submenuTimer || !document.querySelector(".submenu.sm-open")) return;
  submenuTimer = setTimeout(() => { submenuTimer = null; closeSubmenus(); }, SUBMENU_GRACE_MS);
}

for (const ev of ["mouseover", "focusin"]) {
  document.addEventListener(ev, (e) => {
    const el = e.target instanceof Element ? e.target : null;
    if (!el) return;
    const sm = el.closest(".submenu");
    if (sm) openSubmenu(sm); else closeSubmenusSoon();
  });
}

document.addEventListener("click", (e) => {
  // A flyout's own row is not an action and must not count as "clicked outside": it
  // opens on hover and on keyboard focus, and a click on it should leave the menu
  // standing rather than dismiss the lot.
  if (e.target.closest(".submenu-toggle")) { e.preventDefault(); e.stopPropagation(); return; }
  const toggle = e.target.closest(".dropdown-toggle");
  if (toggle) {
    e.preventDefault(); e.stopPropagation();
    const menu = toggle.nextElementSibling, wasOpen = menu && !menu.hidden;
    closeAllMenus();
    if (menu) {
      menu.hidden = wasOpen;
      if (!menu.hidden) { openMenu = menu; openTrigger = toggle; openAbove = placeMenu(menu, toggle, false); }
    }
    return;
  }
  closeAllMenus();
});

// dropdown renders a trigger button + its menu. items entries are:
//   { label, icon?, href? } — a link; or { label, icon?, act, data?, danger? } — a
//   JS action dispatched by onMenuAction; or { sep:true } | { header:"…" } |
//   { label, icon?, items:[…] } — a nested list, which opens as a flyout.
function menuItemsHTML(items) {
  return items.map((it) => {
    if (it.sep) return `<div class="sep"></div>`;
    if (it.header) return `<div class="mlabel">${esc(it.header)}</div>`;
    const icon = `<span class="mi-icon">${it.icon || ""}</span>`;
    // A nested list: one row that reveals its own menu beside it, so a long set of
    // choices does not stretch the parent menu down the page.
    if (it.items) {
      return `<div class="submenu"><button type="button" class="submenu-toggle" aria-haspopup="true">` +
        `${icon}${esc(it.label)}<span class="submenu-caret" aria-hidden="true">&#8250;</span></button>` +
        `<div class="dropdown-menu submenu-menu">${menuItemsHTML(it.items)}</div></div>`;
    }
    if (it.href) return `<a href="${it.href}">${icon}${esc(it.label)}</a>`;
    const data = it.data ? Object.entries(it.data).map(([k, v]) => `data-${k}="${esc(v)}"`).join(" ") : "";
    return `<button type="button" data-act="${esc(it.act)}" ${data}${it.danger ? ' class="danger"' : ""}>${icon}${esc(it.label)}</button>`;
  }).join("");
}

function dropdown(label, triggerClass, items) {
  return `<div class="dropdown"><button type="button" class="${triggerClass} dropdown-toggle">${esc(label)}</button>` +
    `<div class="dropdown-menu" hidden>${menuItemsHTML(items)}</div></div>`;
}

// onMenuAction dispatches menu-item clicks in container to fn(act, buttonEl).
function onMenuAction(container, fn) {
  for (const b of container.querySelectorAll(".dropdown-menu button[data-act]"))
    b.addEventListener("click", () => fn(b.dataset.act, b));
}

// A ready-to-run demo process. Its "Review order" service task creates a job
// that no worker completes, so a token parks there and the instance stays active
// — giving the Operations views (and the live token total) something to show
// without hand-modelling a wait point first. The server auto-lays-out models
// that carry no BPMN diagram interchange, so no DI is needed here.
const DEMO_BPMN = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"
             xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
             targetNamespace="http://atlas/demo">
  <process id="order-review" isExecutable="true">
    <startEvent id="start" name="Order received"/>
    <serviceTask id="review" name="Review order">
      <extensionElements><zeebe:taskDefinition type="review" retries="3"/></extensionElements>
    </serviceTask>
    <serviceTask id="charge" name="Charge payment">
      <extensionElements><zeebe:taskDefinition type="charge" retries="3"/></extensionElements>
    </serviceTask>
    <endEvent id="end" name="Done"/>
    <sequenceFlow id="f1" sourceRef="start" targetRef="review"/>
    <sequenceFlow id="f2" sourceRef="review" targetRef="charge"/>
    <sequenceFlow id="f3" sourceRef="charge" targetRef="end"/>
  </process>
</definitions>`;

// deployDemo deploys DEMO_BPMN, starts one instance, and opens its live view so
// the parked token (and the token total) is visible immediately.
async function deployDemo() {
  const dep = await api("POST", "/api/v1/deployments", DEMO_BPMN, true);
  await api("POST", `/api/v1/processes/${dep.key}/instances`, {});
  toast(`Started ${dep.processId} v${dep.version} — a token is parked on “Review order”`, "ok");
  location.hash = `#/operations/p/${dep.key}`;
}

// ---------- Apps (Atlas naming; reference product names removed) ----------
// Each app names the role its screens need, and the drawer offers only the ones
// this person holds. The Console itself is "any": its dashboard, connectors and AI
// access are everybody's, and the admin screens inside it say so individually
// below.
const APPS = [
  { id: "console", name: "Console", route: "#/console", on: true, role: "any" },
  { id: "modeler", name: "Modeler", route: "#/modeler", on: true, role: "modeler" },
  { id: "tasks", name: "Tasks", route: "#/tasks", on: true, role: "user" },
  { id: "operations", name: "Operations", route: "#/operations", on: true, role: "operator" },
  { id: "panorama", name: "Panorama", route: "#/panorama/landscape", on: true, role: "modeler" },
];

// Secondary (in-app) navigation.
const TOPNAV = {
  console: [
    { name: "Dashboard", route: "#/console", role: "any" },
    { name: "Engine", route: "#/console/engine", role: "admin" },
    { name: "Logs", route: "#/console/logs", role: "admin" },
    { name: "Backup", route: "#/console/backup", role: "admin" },
    { name: "Organization", route: "#/console/org", role: "admin" },
    { name: "Connectors", route: "#/console/connectors", role: "any" },
    { name: "AI access", route: "#/console/ai-access", role: "any" },
    { name: "Audit log", route: "#/console/audit", role: "admin" },
  ],
  modeler: [
    { name: "Home", route: "#/modeler", role: "modeler" },
    { name: "Repository", route: "#/modeler/repository", role: "modeler" },
  ],
  operations: [
    { name: "Instances", route: "#/operations", role: "operator" },
    { name: "Incidents", route: "#/operations/incidents", badge: "incidents", role: "operator" },
    { name: "Workers", route: "#/operations/workers", role: "operator" },
    { name: "Outbox", route: "#/operations/outbox", role: "operator" },
    { name: "Mock directory", route: "#/operations/ad-mock", role: "admin" },
    { name: "Mock database", route: "#/operations/sql-mock", role: "admin" },
    { name: "Decisions", route: "#/operations/decisions", role: "operator" },
    { name: "Call activities", route: "#/operations/call-activities", role: "any" },
  ],
  tasks: [
    { name: "Inbox", route: "#/tasks", role: "user" },
    { name: "Start", route: "#/tasks/start", role: "operator" },
  ],
  panorama: [
    { name: "Landscape", route: "#/panorama/landscape", role: "modeler" },
    { name: "Models", route: "#/panorama", role: "modeler" },
  ],
};

// Connectors are the sibling engines Atlas hands work off to. They live under
// Organization because they're an org-wide integration, not per-process wiring.
// "status" is honest about what this single-binary build actually talks to:
//   active — embedded and used at runtime/deploy time;
//   planned — a supported integration that this build isn't wired to yet.

// The three database Worker Types are one capability with three drivers (ADR-0173), so
// the card that describes them says the same things about all three. Written out three
// times they had already drifted: only SQL Server's mentioned that Atlas supervises the
// worker, only PostgreSQL's mentioned the row cap, and none of them mentioned that a
// database task can be tried without a database (ADR-0221) — so which facts an operator
// learned depended on which of the three they happened to click.
//
// What genuinely differs is the product's name, its environment prefix and its
// placeholder syntax, plus the one sentence about binding by name. Everything else is
// shared, and a fact added here reaches all three.
function sqlWorkerTypeDesc(product, envPrefix, placeholder, binding) {
  return `Runs one statement against a ${product} database \u2014 query for many rows, query one for a single row, execute for an insert, update or delete \u2014 on a worker, off the processor loop. ` +
    `The statement is literal by construction: it is the one connector field with no fx toggle, because a statement assembled from process data would be an injection that needs no quoting bug. ` +
    `Values reach it as bound parameters (${placeholder}). ${binding} ` +
    `A query carries a row cap (1000 by default) and exceeding it fails the task rather than truncating, because a short result set is a wrong business answer and a process that branches on the row count would branch on it confidently. ` +
    `Configure each database below: the whole connection string is the credential, sealed into the vault. ` +
    `Worker-only \u2014 the engine never holds a database credential \u2014 so a SQL task needs a worker, which Atlas supervises for you. ` +
    `Or a mockup: the Databases switch on Console \u203a Workers makes every database worker answer from prepared answers in its own memory, and a worker you run yourself reads the same decision from ATLAS_${envPrefix}_MOCK — either way a model that reads or writes a database runs end to end before anyone has a connection string.`;
}

const CONNECTORS = [
  {
    id: "temis", name: "temis", kind: "Decision engine",
    desc: "DMN 1.5 / FEEL. Evaluates business-rule tasks off the processor loop and validates a project's DMN references at deploy time.",
    refs: "ADR-0014 · ADR-0034", status: "active", statusLabel: "embedded",
  },
  {
    id: "clio", name: "clio", kind: "Event store",
    desc: "Durable event log with registered schemas and reduce specs. A clio connector task sends, queries, or reads events off the processor loop; the endpoint and token are managed below and resolved from the vault. Authored on a service task with the clio Event Store Worker Type.",
    refs: "ADR-0036 · ADR-0041", status: "active", statusLabel: "configurable",
  },
  {
    id: "rest", name: "HTTP REST", kind: "REST API",
    desc: "Calls a model-authored REST endpoint from a service task off the processor loop — method, URL, headers, query parameters, and basic/bearer/apiKey auth (secrets resolved server-side) — writing the JSON response into a result variable. Authored on a service task with the REST Outbound Worker Type.",
    refs: "ADR-0036 · ADR-0041 · ADR-0067", status: "active", statusLabel: "embedded",
  },
  {
    id: "mail", name: "Mail", kind: "Outbound e-mail",
    desc: "Sends an e-mail from a service task off the processor loop via a managed provider — SMTP (any server, incl. Google/Microsoft 365 submission) or the native Gmail and Microsoft Graph APIs (OAuth2 app-only or refresh-token) — or the “preview” provider, which needs neither and delivers to the in-app Outbox so a mail task can be tried before a real provider exists. Recipients, subject, and body are model-authored (FEEL-capable); the provider, default sender, and credentials are managed below and resolved from the vault. Authored on a service task with the E-Mail Outbound Worker Type.",
    refs: "ADR-0041 · ADR-0079 · ADR-0093", status: "active", statusLabel: "configurable",
  },
  {
    id: "sharepoint", name: "SharePoint", kind: "List item",
    desc: "Creates a list item in a Microsoft SharePoint site from a service task off the processor loop via the Graph API (OAuth2 app-only or refresh-token). The site, list, and item fields are model-authored (FEEL-capable) and the created item's JSON is written into a result variable; the Graph base and credentials are managed below and resolved from the vault. Authored on a service task with the SharePoint Worker Type.",
    refs: "ADR-0041 · ADR-0093 · ADR-0141", status: "active", statusLabel: "configurable",
  },
  {
    id: "jira", name: "Jira", kind: "Issue tracker",
    desc: "Performs one Atlassian Jira operation from a service task off the processor loop via the REST API: create an issue, read one, update it, move it through its workflow, comment on it, assign it, or search with JQL. The operation and its values are model-authored (FEEL-capable) and what Jira returned is written into a result variable; the site URL and the credential bundle — {email, apiToken} for Jira Cloud or {token} for a Data Center personal access token — are managed below and resolved from the vault. Authored on a service task with the Jira Worker Type.",
    refs: "ADR-0041 · ADR-0201", status: "active", statusLabel: "configurable",
  },
  {
    id: "remedy", name: "BMC Remedy", kind: "ITSM",
    desc: "Creates an entry (e.g. an incident) in a BMC Remedy / Helix ITSM form from a service task off the processor loop via the AR System REST API. The form and its field values are model-authored (FEEL-capable) and the created entry's id is written into a result variable; the base URL and the {username,password} credential bundle are managed below and resolved from the vault. Authored on a service task with the BMC Remedy Worker Type.",
    refs: "ADR-0041 · ADR-0106", status: "active", statusLabel: "configurable",
  },
  {
    id: "ad", name: "Active Directory", kind: "Directory",
    desc: "Creates a user, group or contact, sets a password, enables or disables an account, moves or deletes an entry, manages group membership and reads a DirSync delta \u2014 on a worker, off the processor loop. Configure each directory below: its LDAP URL and a vault bundle holding the service account. A model then names the connector and says nothing else about the directory. Tasks written before this that carry their own url and bindDN keep working.",
    refs: "ADR-0166 \u00b7 ADR-0181", status: "active", statusLabel: "configured below",
  },
  {
    id: "entra", name: "Entra ID", kind: "Cloud directory",
    desc: "Creates, licenses, disables, lists or delta-syncs accounts and groups in a Microsoft Entra ID tenant via the Graph API \u2014 on a worker, off the processor loop. A Graph collection arrives page by page and the worker follows the pages itself, so a list operation writes a whole list into a result variable rather than a continuation token. Configure each tenant below: its {tenantId, clientId, clientSecret} bundle lives in the vault and never enters a model. Worker-only, so the tenant credential never reaches the engine.",
    refs: "ADR-0172", status: "active", statusLabel: "configured below",
  },
  {
    id: "mssql", name: "Microsoft SQL Server", kind: "Database",
    desc: sqlWorkerTypeDesc("Microsoft SQL Server", "MSSQL", "@p1", "SQL Server is the one product of the three that also binds them by name (@id), which is why an object-shaped parameters variable is accepted here and refused by the other two."),
    refs: "ADR-0173 \u00b7 ADR-0188 \u00b7 ADR-0221", status: "active", statusLabel: "configured below",
  },
  {
    id: "mariadb", name: "MariaDB", kind: "Database",
    desc: sqlWorkerTypeDesc("MariaDB", "MARIADB", "?", "MariaDB binds only positionally, so an object-shaped parameters variable is refused rather than flattened into an order nobody wrote \u2014 which is why the three database products are three Worker Types and not one with a dialect field."),
    refs: "ADR-0173 \u00b7 ADR-0188 \u00b7 ADR-0221", status: "active", statusLabel: "configured below",
  },
  {
    id: "postgres", name: "PostgreSQL", kind: "Database",
    desc: sqlWorkerTypeDesc("PostgreSQL", "POSTGRES", "$1", "PostgreSQL binds only positionally, so an object-shaped parameters variable is refused rather than flattened into an order nobody wrote \u2014 which is why the three database products are three Worker Types and not one with a dialect field."),
    refs: "ADR-0173 \u00b7 ADR-0188 \u00b7 ADR-0221", status: "active", statusLabel: "configured below",
  },
];

// ---------- Shell ----------
// openSearchPalette is the global "jump to a process" command palette behind the
// topbar search icon (and Ctrl/⌘-K, and "/"). It loads the deployed processes and the
// design drafts once, filters them by process id or name as you type, and navigates to
// the picked one. A deployed process offers two destinations — the Modeler diagram or
// the live Operations view — pickable per row (click a pill, or ←/→ then Enter); the
// last choice is remembered. A draft only has the Modeler editor. Typing an exact
// process id and pressing Enter jumps straight there, since an exact id match ranks
// first.
let __searchOpen = false;
async function openSearchPalette() {
  if (__searchOpen) { const i = document.getElementById("sp-input"); if (i) i.focus(); return; }
  __searchOpen = true;
  // Remembered destination for deployed processes ("modeler" | "operations").
  let target = localStorage.getItem("atlas.search.target") === "operations" ? "operations" : "modeler";
  const setTarget = (t) => { target = t; try { localStorage.setItem("atlas.search.target", t); } catch { /* ignore */ } };

  const ov = document.createElement("div");
  ov.className = "sp-ov";
  ov.innerHTML = `
    <div class="sp" role="dialog" aria-modal="true" aria-label="Search processes">
      <div class="sp-head">
        <svg class="sp-icon" viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="7"/><path d="M21 21l-4.3-4.3"/></svg>
        <input id="sp-input" type="text" placeholder="Jump to a process by id or name…" autocomplete="off" spellcheck="false" />
        <kbd class="sp-esc">Esc</kbd>
      </div>
      <div class="sp-results" id="sp-results"></div>
      <div class="sp-foot"><span><kbd>↵</kbd> open</span><span><kbd>←</kbd><kbd>→</kbd> Modeler / Operations</span><span><kbd>↑</kbd><kbd>↓</kbd> move</span></div>
    </div>`;
  document.body.appendChild(ov);
  const input = ov.querySelector("#sp-input");
  const list = ov.querySelector("#sp-results");
  let items = []; // {name, id, kind, version, hrefModeler, hrefOps, href, hay}
  let view = [];  // current filtered slice
  let sel = 0;

  const close = () => { __searchOpen = false; document.removeEventListener("keydown", onKey, true); ov.remove(); };
  // Destination href for an item under the current (or an explicit) target.
  const hrefFor = (it, t) => it.kind === "deployed" ? ((t || target) === "operations" ? it.hrefOps : it.hrefModeler) : it.href;
  const go = (it, t) => { if (!it) return; const h = hrefFor(it, t); if (!h) return; close(); location.hash = h; };

  list.innerHTML = `<div class="sp-empty">Loading…</div>`;
  const [procs, drafts] = await Promise.all([
    api("GET", "/api/v1/processes").catch(() => []),
    api("GET", "/api/v1/drafts").catch(() => []),
  ]);
  if (!__searchOpen) return; // closed while loading
  for (const g of groupByProcess(procs || [])) {
    const l = g.latest;
    items.push({ name: l.name || l.processId, id: l.processId, kind: "deployed", version: l.version,
      hrefModeler: `#/modeler/d/${l.key}`, hrefOps: `#/operations/p/${l.key}` });
  }
  for (const d of (drafts || [])) {
    items.push({ name: d.name || d.processId, id: d.processId, kind: "draft", href: `#/modeler/draft/${encodeURIComponent(d.processId)}` });
  }
  for (const it of items) it.hay = `${it.id} ${it.name}`.toLowerCase();

  const rank = (it, q) => {
    const id = it.id.toLowerCase(), name = it.name.toLowerCase();
    if (id === q) return 0;
    if (id.startsWith(q)) return 1;
    if (name.startsWith(q)) return 2;
    return 3;
  };
  const rowHTML = (it, i) => {
    const tail = it.kind === "deployed"
      ? `<span class="sp-meta">v${it.version}</span>
         <span class="sp-targets">
           <span class="sp-tgt${target === "modeler" ? " on" : ""}" data-tgt="modeler" title="Open the diagram in the Modeler">Modeler</span>
           <span class="sp-tgt${target === "operations" ? " on" : ""}" data-tgt="operations" title="Open the live Operations view">Operations</span>
         </span>`
      : `<span class="sp-badge draft">draft</span>`;
    return `<div class="sp-item${i === sel ? " on" : ""}" role="option" data-i="${i}">
      <span class="sp-name">${esc(it.name)}</span>
      <span class="sp-id">${esc(it.id)}</span>
      ${tail}
    </div>`;
  };
  const render = () => {
    const q = input.value.trim().toLowerCase();
    view = (q ? items.filter((it) => it.hay.includes(q)) : items.slice())
      .sort((a, b) => (q ? rank(a, q) - rank(b, q) : 0) || a.name.localeCompare(b.name))
      .slice(0, 50);
    if (sel >= view.length) sel = view.length ? view.length - 1 : 0;
    if (!view.length) {
      list.innerHTML = `<div class="sp-empty">${items.length ? "No process matches." : "No processes or drafts yet."}</div>`;
      return;
    }
    list.innerHTML = view.map(rowHTML).join("");
    const on = list.querySelector(".sp-item.on");
    if (on) on.scrollIntoView({ block: "nearest" });
  };
  render();
  input.focus();

  input.addEventListener("input", () => { sel = 0; render(); });
  list.addEventListener("click", (e) => {
    const row = e.target.closest(".sp-item"); if (!row) return;
    const it = view[Number(row.dataset.i)]; if (!it) return;
    // A target pill jumps to exactly that destination and remembers the choice; a
    // click anywhere else on the row uses the current default.
    const pill = e.target.closest("[data-tgt]");
    if (pill && it.kind === "deployed") { setTarget(pill.dataset.tgt); go(it, pill.dataset.tgt); return; }
    go(it);
  });
  list.addEventListener("mousemove", (e) => {
    const row = e.target.closest(".sp-item"); if (!row) return;
    const i = Number(row.dataset.i);
    if (i !== sel) { sel = i; list.querySelectorAll(".sp-item").forEach((el, j) => el.classList.toggle("on", j === sel)); }
  });
  ov.addEventListener("mousedown", (e) => { if (e.target === ov) close(); });
  function onKey(e) {
    if (e.key === "Escape") { e.preventDefault(); close(); return; }
    if (e.key === "ArrowDown") { e.preventDefault(); if (view.length) { sel = (sel + 1) % view.length; render(); } return; }
    if (e.key === "ArrowUp") { e.preventDefault(); if (view.length) { sel = (sel - 1 + view.length) % view.length; render(); } return; }
    // ←/→ flip the deployed destination (Modeler ↔ Operations) that Enter will use.
    if (e.key === "ArrowRight" || e.key === "ArrowLeft") {
      if (view[sel] && view[sel].kind === "deployed") { e.preventDefault(); setTarget(target === "modeler" ? "operations" : "modeler"); render(); }
      return;
    }
    if (e.key === "Enter") { e.preventDefault(); if (view[sel]) go(view[sel]); return; }
  }
  document.addEventListener("keydown", onKey, true);
}

function initShell() {
  const drawer = document.getElementById("drawer");
  const scrim = document.getElementById("scrim");
  const openDrawer = () => { drawer.hidden = false; scrim.hidden = false; };
  const closeDrawer = () => { drawer.hidden = true; scrim.hidden = true; };
  document.getElementById("app-switcher").addEventListener("click", openDrawer);
  document.getElementById("drawer-close").addEventListener("click", closeDrawer);
  scrim.addEventListener("click", closeDrawer);

  // Global "jump to a process" search: the topbar icon, Ctrl/⌘-K anywhere, and "/"
  // when not already typing in a field.
  const searchBtn = document.getElementById("global-search");
  if (searchBtn) searchBtn.addEventListener("click", openSearchPalette);
  document.addEventListener("keydown", (e) => {
    if ((e.metaKey || e.ctrlKey) && (e.key === "k" || e.key === "K")) { e.preventDefault(); openSearchPalette(); return; }
    if (e.key === "/" && !__searchOpen) {
      const el = document.activeElement;
      if (el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.isContentEditable)) return;
      e.preventDefault(); openSearchPalette();
    }
  });

  const nav = document.getElementById("drawer-apps");
  paintApps();
  nav.addEventListener("click", closeDrawer);

  // Turn the static avatar into an account dropdown (reusing the delegated
  // dropdown machinery): its menu shows who is signed in and offers Log out.
  const acct = document.querySelector(".topbar .avatar");
  if (acct && !acct.classList.contains("dropdown-toggle")) {
    const wrap = document.createElement("div");
    wrap.className = "dropdown";
    acct.parentNode.insertBefore(wrap, acct);
    acct.classList.add("dropdown-toggle");
    wrap.appendChild(acct);
    const menu = document.createElement("div");
    menu.className = "dropdown-menu";
    menu.hidden = true;
    wrap.appendChild(menu);
    window.__acctMenu = menu;
    menu.addEventListener("click", (e) => {
      if (e.target.closest("[data-act=logout]")) { closeAllMenus(); logout(); }
    });
  }

  api("GET", "/api/v1/info").then((i) => {
    document.querySelectorAll(".org").forEach((e) => { e.textContent = "Atlas Org"; });
    initHelpMenu(!!(i && i.docs));
  }).catch(() => { initHelpMenu(false); });
}

// handbookHelp maps the current route to the most relevant handbook chapter, so
// the "?" menu can offer help *for the view you're looking at* rather than only a
// generic link. The checks mirror the router's order (most specific first); the
// anchor is a section id in handbuch.html and the label is the (English, matching
// the chrome) menu text. Adding a route here is all it takes to give it contextual
// help — the handbook page and the menu wiring are unchanged.
function handbookHelp(path) {
  const H = (anchor, label) => ({ anchor, label });
  if (/^#\/modeler\/dmn\//.test(path)) return H("dmn", "Learn DMN");
  if (/^#\/modeler\/form\b/.test(path)) return H("formulare", "Forms & workers");
  // An application's detail view is where its artifacts are gathered and published —
  // the workshop chapter is the one that builds exactly that, end to end.
  if (/^#\/modeler\/p\//.test(path)) return H("werkstatt", "Building an application");
  if (path.startsWith("#/modeler")) return H("designen", "Designing processes");
  if (path.startsWith("#/tasks")) return H("formulare", "Tasks & forms");
  if (path.startsWith("#/operations/decisions")) return H("dmn", "Learn DMN");
  if (path.startsWith("#/operations/call-activities")) return H("elemente", "BPMN elements");
  if (path.startsWith("#/operations")) return H("betrieb", "Operations & incidents");
  if (path.startsWith("#/console/engine")) return H("konzepte", "Core concepts");
  // Organization pointed at the worker chapter only because the worker cards used to
  // sit on it; with those on their own page it points there instead, and Organization
  // falls through to the Console's own chapter. Both spellings of the route resolve:
  // #/console/workers is canonical, #/console/connectors the compatibility alias the
  // legacy router still receives (ADR-0203).
  if (path.startsWith("#/console/workers") || path.startsWith("#/console/connectors")) return H("formulare", "Forms & workers");
  if (path.startsWith("#/console")) return H("schnellstart", "Quick start");
  return H("willkommen", "Welcome to Atlas");
}

// The help menu is built once (async, after /info resolves) but its contextual
// entry has to follow navigation, which happens independently. We stash the docs
// flag and the current route so either event can (re)render correctly regardless
// of which lands first: initHelpMenu renders using the last route setChrome saw,
// and setHelpContext refreshes the entry in place on every later navigation.
let helpDocsEnabled = false;
let helpRoutePath = "#/console";

// setHelpContext points the "On this page" entry at the chapter for `path`. Safe
// to call before the menu exists (early navigations) — it just records the route.
function setHelpContext(path) {
  helpRoutePath = path;
  const ctx = document.getElementById("help-ctx");
  if (!ctx) return;
  const { anchor, label } = handbookHelp(path);
  ctx.href = `/handbuch.html#${anchor}`;
  ctx.innerHTML = `${esc(label)} <span class="ext" aria-hidden="true">↗</span>`;
}

// initHelpMenu fills the top-bar "?" dropdown. It leads with a contextual
// "On this page" link into the handbook chapter for the current view (see
// handbookHelp), then the general references: the Handbook (a self-contained
// bilingual page served regardless of the --docs flag), the Scalar API Explorer
// (/api/docs) when docs are enabled — otherwise an inert hint about --docs=false
// so the missing link is self-explanatory rather than a dead button (ADR-0043) —
// and the Conformance Gallery (always shown; a static asset). Open/close is
// handled by the shared delegated .dropdown-toggle machinery above.
function initHelpMenu(docsEnabled) {
  helpDocsEnabled = docsEnabled;
  const menu = document.getElementById("help-menu");
  if (!menu) return;
  const explorer = docsEnabled
    ? `<a role="menuitem" href="/api/docs" target="_blank" rel="noopener">API Explorer <span class="ext" aria-hidden="true">↗</span></a>`
    : `<span class="help-note">API Explorer is disabled<br><span class="muted">start the server without <code>--docs=false</code></span></span>`;
  const gallery = `<a role="menuitem" href="/conformance-gallery.html" target="_blank" rel="noopener">Conformance Gallery <span class="ext" aria-hidden="true">↗</span></a>`;
  const handbook = `<a role="menuitem" href="/handbuch.html" target="_blank" rel="noopener">Handbook <span class="ext" aria-hidden="true">↗</span></a>`;
  const context = `<div class="mlabel">On this page</div>` +
    `<a id="help-ctx" role="menuitem" target="_blank" rel="noopener" href="/handbuch.html"></a>` +
    `<div class="sep"></div>`;
  menu.innerHTML = context + handbook + explorer + gallery;
  setHelpContext(helpRoutePath); // fill the contextual entry for the current view
}

// ---------- Operations nav incident badge ----------
// A stuck token is now marked on the live diagram, in the replay and in the lists
// (ADR-0150/0151) — but each of those only says so once you are already looking at
// it. The nav badge is the one that finds *you*: while the Operations app is open,
// the shell polls the live counts and puts the number of parked tokens on the
// Incidents entry, so "something is stuck" arrives without a view being opened.
// It polls only in Operations (nowhere else shows the entry) and stops on the way
// out, so no other app pays for it.
const INCIDENT_BADGE_INTERVAL = 5000;
let incidentCount = 0;
let incidentBadgeTimer = null;

// paintIncidentBadge writes the current count into whatever badge slot the chrome
// has right now. setChrome rebuilds the nav on every navigation, so the value is
// re-applied rather than assumed to survive.
function paintIncidentBadge() {
  const slot = document.querySelector('#topnav .nav-badge[data-badge="incidents"]');
  if (!slot) return;
  slot.hidden = incidentCount === 0;
  // A four-digit pill would push the nav around; past 999 the exact number does not
  // change what an operator does next.
  slot.textContent = incidentCount > 999 ? "999+" : String(incidentCount);
  slot.title = incidentCount === 1
    ? "1 token is parked behind an unresolved incident"
    : `${incidentCount} tokens are parked behind unresolved incidents`;
}

// refreshIncidentBadge re-reads the count. The incidents table calls it straight
// after a resolve, so the nav agrees at once instead of waiting out the interval.
async function refreshIncidentBadge() {
  let stats;
  try { stats = await api("GET", "/api/v1/stats"); }
  catch { return; } // transient; the badge keeps its last value
  incidentCount = (stats && stats.unresolvedIncidents) || 0;
  paintIncidentBadge();
}

function syncIncidentBadge(appId) {
  if (appId !== "operations") {
    if (incidentBadgeTimer) { clearInterval(incidentBadgeTimer); incidentBadgeTimer = null; }
    return;
  }
  paintIncidentBadge(); // the freshly rendered nav starts from what we already know
  refreshIncidentBadge();
  if (!incidentBadgeTimer) incidentBadgeTimer = setInterval(refreshIncidentBadge, INCIDENT_BADGE_INTERVAL);
}

// paintApps fills the drawer with the apps this person may use. It runs at boot,
// before /auth/me has answered — when nothing is known and everything is offered —
// and again on every navigation, which is the first paint that knows the roles.
// The click handler lives on the container, so repainting never loses it.
function paintApps() {
  const nav = document.getElementById("drawer-apps");
  if (!nav) return;
  nav.innerHTML = APPS.filter((a) => mayUse(a.role)).map((a) =>
    `<a href="${a.route}" data-app="${a.id}">${a.name}${a.on ? "" : '<span class="soon">soon</span>'}</a>`
  ).join("");
}

function setChrome(appId, route) {
  document.getElementById("app-name").textContent =
    (APPS.find((a) => a.id === appId) || {}).name || "Atlas";
  paintApps();
  const topnav = document.getElementById("topnav");
  topnav.innerHTML = (TOPNAV[appId] || []).filter((t) => mayUse(t.role)).map((t) =>
    `<a href="${t.route}" class="${t.route === route ? "active" : ""}">${t.name}` +
    (t.badge ? `<span class="nav-badge" data-badge="${t.badge}" hidden></span>` : "") + `</a>`
  ).join("");
  syncIncidentBadge(appId); // the nav says how many tokens are stuck, before anything is opened
  document.querySelectorAll("#drawer-apps a").forEach((a) =>
    a.classList.toggle("active", a.dataset.app === appId));
  setHelpContext(route); // keep the "?" menu's contextual help pointed at this view
  const fullBleed = route.includes("/modeler/d/") || route.includes("/modeler/draft/") || route.includes("/modeler/form/") || route.includes("/modeler/new") || route.includes("/operations/p/");
  document.body.classList.toggle("editor-mode", fullBleed);
  // The Tasks inbox is a wide three-pane layout, so it drops the centered
  // max-width the default content column uses while keeping normal padding.
  document.body.classList.toggle("tasks-mode", appId === "tasks");
}

// ---------- What's New ----------
// The Console landing page surfaces recent, user-facing features from
// /whats-new.json, which scripts/whats-new/gen.mjs generates from CHANGELOG.md and a
// curated bilingual overrides file (see scripts/whats-new/README.md). It is DE/EN
// with a local toggle, and every layer is collapsible so it stays compact: the whole
// section is a <details>, only the newest few entries show at first, and each entry
// is a <details> whose body carries the plain-language summary, an optional
// step-by-step tutorial, a link to the PR/ADR, and an optional "Try it" deep link.
const WN_STRINGS = {
  en: { title: "What's New", latest: "New", tutorial: "Try it out", more: "Show older", less: "Show fewer", empty: "" },
  de: { title: "Neu in Atlas", latest: "Neu", tutorial: "Ausprobieren", more: "Ältere anzeigen", less: "Weniger anzeigen", empty: "" },
};
// Only these hash-route prefixes are accepted as a "Try it" target, so a bad or
// hostile route in the data can never point the button somewhere unexpected.
const WN_ROUTE_OK = /^#\/(console|modeler|operations|tasks)(\/|$)/;
const WN_INITIAL = 4;

function wnLang() {
  try {
    const s = localStorage.getItem("atlas.whatsnew.lang");
    if (s === "de" || s === "en") return s;
  } catch { /* ignore */ }
  return /^de/i.test(navigator.language || "") ? "de" : "en";
}
function wnSetLang(l) {
  try { localStorage.setItem("atlas.whatsnew.lang", l); } catch { /* ignore */ }
}

// wnText resolves a {en, de} field for the active language, falling back to English.
const wnText = (b, lang) => (b && (b[lang] != null ? b[lang] : b.en)) || "";

async function renderWhatsNew(slot) {
  if (!slot) return;
  let doc;
  try {
    const res = await fetch("/whats-new.json", { headers: { Accept: "application/json" } });
    if (!res.ok) return; // no What's New shipped; leave the slot empty and silent
    doc = await res.json();
  } catch { return; } // offline or malformed — the landing page works without it
  const entries = (doc && Array.isArray(doc.entries)) ? doc.entries : [];
  if (entries.length) paintWhatsNew(slot, entries, wnLang());
}

function wnEntryHTML(e, lang, t) {
  const title = esc(wnText(e.title, lang));
  const when = e.date
    ? `<span class="wn-date">${esc(e.date)}</span>`
    : `<span class="wn-date wn-new">${esc(t.latest)}</span>`;
  const tags = (e.tags || []).map((tag) => `<span class="chip">${esc(tag)}</span>`).join("");
  const summary = esc(wnText(e.summary, lang));

  let tutorial = "";
  const steps = e.tutorial && wnText(e.tutorial, lang);
  if (Array.isArray(steps) && steps.length) {
    tutorial = `<div class="wn-tutorial"><div class="wn-tut-label">${esc(t.tutorial)}</div>` +
      `<ol>${steps.map((s) => `<li>${esc(s)}</li>`).join("")}</ol></div>`;
  }

  const links = [];
  if (e.link && /^https?:\/\//.test(e.link.url)) {
    links.push(`<a class="wn-link" href="${esc(e.link.url)}" target="_blank" rel="noopener">${esc(e.link.label)} <span class="ext" aria-hidden="true">↗</span></a>`);
  }
  if (e.try && WN_ROUTE_OK.test(e.try.route || "")) {
    links.push(`<a class="btn neutral wn-try" href="${esc(e.try.route)}">${esc(wnText(e.try.label, lang))} →</a>`);
  }
  const linksHTML = links.length ? `<div class="wn-links">${links.join("")}</div>` : "";

  return `<details class="wn-item">` +
    `<summary>${when}<span class="wn-item-title">${title}</span>${tags}</summary>` +
    `<div class="wn-body"><p>${summary}</p>${tutorial}${linksHTML}</div>` +
    `</details>`;
}

function paintWhatsNew(slot, entries, lang) {
  const t = WN_STRINGS[lang] || WN_STRINGS.en;
  const head = entries.slice(0, WN_INITIAL).map((e) => wnEntryHTML(e, lang, t)).join("");
  const rest = entries.slice(WN_INITIAL);
  const restHTML = rest.length
    ? `<div class="wn-rest" hidden>${rest.map((e) => wnEntryHTML(e, lang, t)).join("")}</div>` +
      `<button type="button" class="wn-more" title="Show older What’s-new entries">${esc(t.more)} (${rest.length})</button>`
    : "";
  slot.innerHTML =
    `<div class="card whats-new"><details class="wn-root" open>` +
    `<summary class="wn-head"><span class="wn-title">${esc(t.title)}</span>` +
    `<span class="wn-lang">` +
    `<button type="button" data-lang="en" class="${lang === "en" ? "active" : ""}" title="Show these notes in English">EN</button>` +
    `<button type="button" data-lang="de" class="${lang === "de" ? "active" : ""}" title="Show these notes in German">DE</button>` +
    `</span></summary>` +
    `<div class="wn-list">${head}${restHTML}</div>` +
    `</details></div>`;

  // The language toggle lives inside the <summary>; stop the click from also toggling
  // the section open/closed, switch language, remember it, and repaint in place.
  slot.querySelectorAll(".wn-lang button").forEach((b) => b.addEventListener("click", (ev) => {
    ev.preventDefault();
    ev.stopPropagation();
    const l = b.dataset.lang;
    wnSetLang(l);
    paintWhatsNew(slot, entries, l);
  }));
  const more = slot.querySelector(".wn-more");
  if (more) more.addEventListener("click", () => {
    const r = slot.querySelector(".wn-rest");
    if (r) r.hidden = false;
    more.remove();
  });
}

// ---------- Views ----------
async function viewConsoleDashboard() {
  view.innerHTML = `
    <div class="card">
      <h1>Welcome to Atlas</h1>
      <p class="muted">Atlas is a durable, high-throughput BPMN&nbsp;2.x workflow engine that runs
      from a single self-contained binary. This Console manages deployments and shows engine health;
      the Modeler lets you design and deploy BPMN models in the browser.</p>
      <ol class="steps">
        <li><b>Model a process</b> — open the Modeler and draw a BPMN diagram, or import existing XML.</li>
        <li><b>Deploy</b> — make a model runnable straight from the editor, and optionally start an instance in one step with <b>Deploy &amp; run</b>.</li>
        <li><b>Watch it execute</b> — tokens move through the engine and land as durable events.</li>
      </ol>
      <div class="row">
        <a class="btn" href="#/modeler">Open Modeler</a>
        <a class="btn ghost" href="#/console/engine">View engine</a>
        <a class="btn ghost" href="/handbuch.html" target="_blank" rel="noopener">Handbook ↗</a>
      </div>
    </div>
    <div id="whats-new-slot"></div>
    <div class="grid2" style="margin-top:18px">
      <div class="card">
        <div class="between"><h2>Deployments</h2><a href="#/modeler">View all</a></div>
        <p id="dep-summary" class="muted">Loading…</p>
        <a class="btn neutral" href="#/modeler/new">New diagram</a>
      </div>
      <div class="card">
        <div class="between"><h2>Engine</h2><a href="#/operations">Instances</a></div>
        <div class="stats" style="margin-top:6px">
          <div class="stat"><b id="s-pi">0</b><span>active process instances</span></div>
          <div class="stat"><b id="s-ei">0</b><span>active element instances</span></div>
        </div>
      </div>
    </div>`;
  renderWhatsNew(document.getElementById("whats-new-slot")); // fills its own slot; safe if it fails
  try {
    const [procs, stats] = await Promise.all([
      api("GET", "/api/v1/processes"),
      api("GET", "/api/v1/stats"),
    ]);
    document.getElementById("dep-summary").textContent = procs.length
      ? `${procs.length} process definition${procs.length === 1 ? "" : "s"} deployed.`
      : "No processes deployed yet.";
    document.getElementById("s-pi").textContent = stats.activeProcessInstances;
    document.getElementById("s-ei").textContent = stats.activeElementInstances;
  } catch (e) { toast(e.message, "err"); }
}

async function viewConsoleEngine() {
  view.innerHTML = `
    <div class="card">
      <div class="between"><h1>Engine</h1><span class="pill ok"><span class="dot"></span>running</span></div>
      <p class="muted">Single-node, single partition. State is materialized from an append-only
      write-ahead log; every transition is durable before it becomes visible.</p>
      <div class="stats" style="margin-top:14px">
        <div class="stat"><b id="e-pi">0</b><span>active process instances</span></div>
        <div class="stat"><b id="e-ei">0</b><span>active element instances</span></div>
        <div class="stat"><b id="e-dep">0</b><span>deployed definitions</span></div>
        <div class="stat"><b>1</b><span>partition</span></div>
      </div>
    </div>`;
  view.innerHTML += `
    <div class="card" id="node-card" style="margin-top:14px">
      <h2>This node</h2>
      <p class="muted">The identity this server presents to other Atlas servers (ADR-0189). The id is
      minted once and survives restarts — it is what a landscape view correlates against, and what an
      architecture model binds to with <code>atlas.runtimeId</code>. The name is for people; the id is
      for machines, and changing the name never changes the id.</p>
      <div id="node-info" class="muted">loading…</div>
    </div>`;
  view.innerHTML += `
    <div class="card" id="build-card" style="margin-top:14px">
      <h2>Build</h2>
      <p class="muted">Which commit this running server was built from — check it against the merged code to confirm the deployed binary is up to date.</p>
      <div id="build-info" class="muted">loading…</div>
    </div>`;
  try {
    const [procs, stats, info, node] = await Promise.all([
      api("GET", "/api/v1/processes"),
      api("GET", "/api/v1/stats"),
      api("GET", "/api/v1/info"),
      api("GET", "/api/v1/node"),
    ]);
    document.getElementById("e-pi").textContent = stats.activeProcessInstances;
    document.getElementById("e-ei").textContent = stats.activeElementInstances;
    document.getElementById("e-dep").textContent = procs.length;
    document.getElementById("build-info").innerHTML = buildInfoHTML(info);
    mountNodeCard(node);
  } catch (e) { toast(e.message, "err"); }
}

// nodeInfoHTML renders the node descriptor. The id is shown in full and
// monospaced: it is a value somebody copies into a model binding, and a truncated
// identifier is one that gets pasted wrong.
//
// The features list is what this node advertises it can be asked for. It is
// derived server-side from the routes actually mounted, so it is worth showing
// verbatim rather than summarising: an operator comparing two servers is looking
// for the difference.
function nodeInfoHTML(n) {
  n = n || {};
  const labels = Object.entries(n.labels || {});
  const rows = [
    ["Runtime id", `<span style="font-family:ui-monospace,monospace">${esc(n.id || "—")}</span>`],
    ["Name", esc(n.name || "—")],
    ["Environment", esc(n.environment || "—")],
    ["Partition", `${esc(String(n.partition ?? "—"))} of ${esc(String(n.partitions ?? "—"))}`],
    ["Labels", labels.length
      ? labels.map(([k, v]) => `<span class="pill">${esc(k)}=${esc(v)}</span>`).join(" ")
      : "<span class=\"muted\">none</span>"],
    ["Features", (n.features || []).length
      ? (n.features || []).map((f) => `<code>${esc(f)}</code>`).join(" ")
      : "<span class=\"muted\">none advertised</span>"],
  ];
  return `<table class="kv-table">${rows.map(([k, v]) =>
    `<tr><td style="padding:2px 16px 2px 0; color:var(--muted); vertical-align:top">${k}</td><td>${v}</td></tr>`).join("")}</table>`;
}

// mountNodeCard renders the descriptor and, for an administrator, the form that
// names it. Naming is admin-only on the server (ADR-0209), so the form is not
// offered to anybody else rather than offered and refused.
function mountNodeCard(node) {
  const slot = document.getElementById("node-info");
  if (!slot) return;
  const editable = mayUse("admin");
  slot.innerHTML = nodeInfoHTML(node) + (editable ? `
    <form id="node-form" class="row" style="gap:10px; margin-top:12px; flex-wrap:wrap; align-items:flex-end">
      <label class="field" style="margin:0"><span>Name</span>
        <input id="node-name" type="text" maxlength="200" placeholder="e.g. Zurich primary"
          value="${esc(node.name || "")}" /></label>
      <label class="field" style="margin:0"><span>Environment</span>
        <input id="node-env" type="text" maxlength="200" placeholder="e.g. production"
          value="${esc(node.environment || "")}" /></label>
      <button class="btn" type="submit">Save</button>
    </form>` : "");
  if (!editable) return;
  document.getElementById("node-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      const saved = await api("PUT", "/api/v1/node", {
        name: document.getElementById("node-name").value,
        environment: document.getElementById("node-env").value,
      });
      mountNodeCard(saved);
      toast("Node identity saved");
    } catch (e) { toast(e.message, "err"); }
  });
}

// buildInfoHTML renders the version/VCS metadata from GET /api/v1/info: the version
// string, the git commit (short) with a dirty marker, the build time, and the Go
// toolchain. A missing revision means the binary was built outside a git checkout.
function buildInfoHTML(i) {
  i = i || {};
  const rev = i.revision ? i.revision.slice(0, 12) + (i.modified ? " (modified)" : "") : "unknown (built outside a git checkout)";
  const rows = [
    ["Version", esc(i.version || "—")],
    ["Commit", esc(rev)],
    ["Built", esc(i.buildTime || "—")],
    ["Go", esc(i.go || "—")],
  ];
  return `<table class="kv-table">${rows.map(([k, v]) =>
    `<tr><td style="padding:2px 16px 2px 0; color:var(--muted)">${k}</td><td style="font-family:ui-monospace,monospace">${v}</td></tr>`).join("")}</table>`;
}

// viewConsoleLogs shows the recent server-log tail (GET /api/v1/logs) so an
// operator can diagnose from the browser without shell access — e.g. to read the
// script-worker startup lines. It auto-refreshes; the interval is cleared on route
// change via __atlasCleanup.
async function viewConsoleLogs() {
  const gen = navGen;
  view.innerHTML = `
    <div class="card">
      <div class="between">
        <h1>Server logs</h1>
        <div class="row" style="gap:12px; align-items:center">
          <label class="field inline" style="margin:0"><input type="checkbox" id="log-follow" checked> Auto-refresh</label>
          <button class="btn neutral" id="log-copy" title="Copy the whole visible log to the clipboard">Copy</button>
          <button class="btn neutral" id="log-refresh" title="Reload the latest log lines now">Refresh</button>
        </div>
      </div>
      <p class="muted">The most recent server log lines (an in-memory tail). Look here for the
      script-worker startup lines, e.g. <code>powershell script worker enabled (pwsh found on PATH)</code>
      or a <code>WARNING: … not found on PATH</code>. With authentication enabled this is admin-only.</p>
      <pre id="log-out" style="max-height:62vh; overflow:auto; background:var(--bg); padding:12px; border-radius:8px; font-size:12px; line-height:1.5; white-space:pre-wrap; margin:0">loading…</pre>
    </div>`;
  const out = document.getElementById("log-out");
  const follow = document.getElementById("log-follow");
  const load = async () => {
    try {
      const r = await api("GET", "/api/v1/logs");
      const lines = (r && r.lines) || [];
      const atBottom = out.scrollHeight - out.scrollTop - out.clientHeight < 40;
      out.textContent = lines.length ? lines.join("\n") : "(no log lines captured yet)";
      if (atBottom) out.scrollTop = out.scrollHeight;
    } catch (e) {
      out.textContent = "Failed to load logs: " + (e && e.message || e);
    }
  };
  await load();
  // Navigated away while the first load was in flight: don't install a poll that
  // now writes into a detached node, and don't overwrite the new view's cleanup.
  if (superseded(gen)) return;
  document.getElementById("log-refresh").addEventListener("click", load);
  // Copying beats selecting here: the tail repaints every two seconds, so a
  // hand-made selection is gone before it can be dragged to the end. The whole
  // visible tail goes at once, which is also what someone pasting into an issue or
  // a chat actually wants.
  document.getElementById("log-copy").addEventListener("click", async () => {
    const text = out.textContent || "";
    if (!text.trim()) {
      toast("Nothing to copy yet", "err");
      return;
    }
    const lines = text.split("\n").length;
    if (await copyText(text)) {
      toast(`Copied ${lines} log line${lines === 1 ? "" : "s"}`);
    } else {
      toast("Could not copy \u2014 select the text and copy it by hand", "err");
    }
  });
  const timer = setInterval(() => { if (follow.checked) load(); }, 2000);
  window.__atlasCleanup = () => clearInterval(timer);
}

// viewConsoleAudit is the global admin audit view (ADR-0184): the access-control
// history across every application in one place — who shared, revoked, changed
// visibility, or transferred ownership, and when. Admin-only on the server; a
// signed-in non-admin gets a notice rather than an error card. Subject and owner ids
// are resolved to names via the principals directory when it loads.
async function viewConsoleAudit() {
  const gen = navGen;
  view.innerHTML = `
    <div class="card">
      <div class="between">
        <h1>Audit log</h1>
        <div class="row" style="gap:12px; align-items:center">
          <label class="field inline" style="margin:0">Action
            <select id="audit-action">
              <option value="">All</option>
              <option value="share">Share</option>
              <option value="unshare">Revoke</option>
              <option value="visibility">Visibility</option>
              <option value="transfer">Transfer</option>
            </select>
          </label>
          <button class="btn neutral" id="audit-refresh" title="Reload the audit log">Refresh</button>
        </div>
      </div>
      <p class="muted">Access-control changes across every application — shares, revokes, visibility changes, and ownership transfers — newest first. The 200 most recent are shown.</p>
      <div id="audit-out">loading…</div>
    </div>`;

  // Resolve subject/owner ids to names when the directory is available; degrade to
  // the raw id otherwise. Admins can always read the principals directory (ADR-0073).
  let byId = new Map();
  try {
    const dir = await api("GET", "/api/v1/principals");
    byId = new Map((dir || []).map((p) => [p.id, p]));
  } catch { /* leave empty — ids show raw */ }
  const nameOf = (id) => { const p = byId.get(id); return p ? p.name : id; };
  const isGroup = (id) => { const p = byId.get(id); return p ? p.type === "group" : false; };

  const detail = (e) => {
    switch (e.action) {
      case "share": return `shared with ${esc(nameOf(e.subjectId))}${isGroup(e.subjectId) ? " (group)" : ""} as ${esc(e.role)}`;
      case "unshare": return `removed ${esc(nameOf(e.subjectId))}${isGroup(e.subjectId) ? " (group)" : ""}`;
      case "visibility": return `visibility ${esc(e.from)} → ${esc(e.to)}`;
      case "transfer": return `ownership ${esc(nameOf(e.from))} → ${esc(nameOf(e.to))}`;
      default: return esc(e.action);
    }
  };
  const actionPill = (a) => {
    const cls = { share: "ok", unshare: "err", visibility: "warn", transfer: "warn" }[a] || "";
    return `<span class="pill ${cls}">${esc(a)}</span>`;
  };

  const out = document.getElementById("audit-out");
  const load = async (action) => {
    let events;
    try {
      events = await api("GET", "/api/v1/audit" + (action ? "?action=" + encodeURIComponent(action) : ""));
    } catch (e) {
      if (superseded(gen)) return;
      if (/admin/i.test(e.message)) {
        out.innerHTML = `<p class="muted">The global audit log is available to administrators only.</p>`;
      } else {
        out.innerHTML = `<p class="muted">Failed to load the audit log: ${esc(e.message || String(e))}</p>`;
      }
      return;
    }
    if (superseded(gen)) return;
    if (!events || !events.length) {
      out.innerHTML = `<p class="muted">No access-control changes recorded yet.</p>`;
      return;
    }
    const rows = events.map((e) => `
      <tr>
        <td class="muted small" style="white-space:nowrap">${esc(fmtTime(e.at))}</td>
        <td>${actionPill(e.action)}</td>
        <td>${esc(e.applicationName || e.applicationId)}</td>
        <td>${detail(e)}</td>
        <td>${esc(e.actorName || e.actorId || "—")}</td>
      </tr>`).join("");
    out.innerHTML = `
      <table class="table" data-dt-key="audit">
        <thead><tr><th>When</th><th>Action</th><th>Application</th><th>Change</th><th>By</th></tr></thead>
        <tbody>${rows}</tbody>
      </table>`;
  };

  await load("");
  if (superseded(gen)) return;
  const sel = document.getElementById("audit-action");
  document.getElementById("audit-refresh").addEventListener("click", () => load(sel.value));
  sel.addEventListener("change", () => load(sel.value));
}

// viewConsoleBackup is the one-file backup/restore of design-time data (ADR-0107):
// Download streams GET /api/v1/backup as a .tar.gz (a plain same-origin anchor, so
// the session cookie authenticates it); Restore POSTs the chosen file to
// /api/v1/restore. User accounts and the vault key are never in the archive; with
// authentication enabled both endpoints are admin-only.
async function viewConsoleBackup() {
  const gen = navGen;
  view.innerHTML = `
    <div class="card">
      <h1>Backup &amp; restore</h1>
      <p class="muted">Download a single archive of your design-time data — projects, drafts,
      deployments, forms, decisions, connectors — to keep or move to another instance.
      User accounts and the secret vault key are never included. With authentication enabled this is admin-only.</p>
      <div class="row" style="gap:12px; align-items:center; margin-top:8px">
        <a class="btn" href="/api/v1/backup" download>Download backup (.tar.gz)</a>
      </div>

      <h3 style="margin:22px 0 6px">Restore</h3>
      <p class="muted">Uploading a backup overwrites artifacts that share an id. Restored drafts,
      projects, forms and decisions appear immediately; <strong>deployed processes take effect after the
      next server restart</strong>. Connectors keep their configuration but need their secrets re-entered.</p>
      <div class="row" style="gap:12px; align-items:center">
        <input type="file" id="restore-file" accept=".gz,.tgz,application/gzip">
        <button class="btn neutral" id="restore-btn" title="Upload the chosen backup archive and restore its artifacts">Restore from file</button>
      </div>
      <p id="restore-status" class="muted" style="margin-top:10px" hidden></p>
    </div>

    <div class="card" style="margin-top:16px">
      <h1>Full snapshot</h1>
      <p class="muted">A whole-instance snapshot for disaster recovery or migration: it also captures the
      <strong>WAL — every running instance</strong> — plus user accounts and the vault key, so a restore
      reconstitutes a complete, immediately-usable engine elsewhere. The derivable state store is rebuilt
      from the WAL on restore. The file carries secrets — treat it like the server itself. Admin-only when
      authentication is enabled.</p>
      <div class="row" style="gap:12px; align-items:center; margin-top:8px">
        <a class="btn" href="/api/v1/backup/full" download>Download full snapshot (.tar.gz)</a>
      </div>

      <h3 style="margin:22px 0 6px">Restore full snapshot</h3>
      <p class="muted"><strong>This replaces the entire engine</strong> — running instances, design-time data,
      users and the vault key. It cannot be applied live: the upload is staged and applied on the
      <strong>next server restart</strong>, which then rebuilds state from the restored WAL.</p>
      <div class="row" style="gap:12px; align-items:center">
        <input type="file" id="restore-full-file" accept=".gz,.tgz,application/gzip">
        <button class="btn neutral" id="restore-full-btn" title="Upload a full snapshot; it is applied on the next server restart">Stage full restore</button>
      </div>
      <p id="restore-full-status" class="muted" style="margin-top:10px" hidden></p>
    </div>`;
  if (superseded(gen)) return;

  // wireRestore binds a file picker + button to an upload endpoint, reporting into a
  // status line. onOk builds the success message from the JSON response.
  const wireRestore = (fileId, btnId, statusId, url, confirmMsg, onOk) => {
    const status = document.getElementById(statusId);
    document.getElementById(btnId).addEventListener("click", async () => {
      const file = document.getElementById(fileId).files[0];
      if (!file) { toast("Choose a backup file first", "error"); return; }
      if (!window.confirm(confirmMsg)) return;
      status.hidden = false;
      status.textContent = "Uploading…";
      try {
        const res = await fetch(url, { method: "POST", headers: { "Content-Type": "application/gzip" }, body: file });
        const data = await res.json().catch(() => null);
        if (!res.ok) throw new Error((data && data.error) || res.statusText);
        status.textContent = onOk(data || {});
        toast("Restore complete", "ok");
      } catch (e) {
        status.textContent = "Restore failed: " + (e && e.message || e);
        toast("Restore failed", "error");
      }
    });
  };

  wireRestore(
    "restore-file", "restore-btn", "restore-status", "/api/v1/restore",
    "Restore from this file? Artifacts sharing an id will be overwritten.",
    (d) => `Restored ${d.restored || 0} file(s).` + (d.restartRequired ? " Restart the server to activate restored deployments." : ""),
  );
  wireRestore(
    "restore-full-file", "restore-full-btn", "restore-full-status", "/api/v1/restore/full",
    "Restore a FULL snapshot? This replaces the entire engine — running instances, design-time data, users and the vault key — and is applied on the next server restart.",
    (d) => `Staged ${d.restored || 0} file(s). Restart the server to apply the full restore.`,
  );
}

// userForm renders the create or edit form for a user. In edit mode the username
// is immutable (it identifies existing sessions and references) and the password
// has its own action, so neither appears here.
// GRANTABLE_ROLES is the four roles an account can be given, in the order the form
// offers them, each with what it lets the person do. The wording matters more than
// it looks: an administrator picking roles is deciding who may deploy a model,
// which is code execution, and "modeler" alone does not say that.
const GRANTABLE_ROLES = [
  { id: "admin", name: "Administrator", what: "accounts, credentials, secrets, settings, backup and restore" },
  { id: "modeler", name: "Modeller", what: "author drafts, forms and decisions — and deploy them" },
  { id: "operator", name: "Operator", what: "start, cancel and repair instances; read runtime data" },
  { id: "user", name: "User", what: "work on tasks and read what they are given" },
];

function userForm(u) {
  const isEdit = !!u;
  const held = (isEdit && u.roles) || ["user"];
  const boxes = GRANTABLE_ROLES.map((r) =>
    `<label class="field inline" title="${esc(r.what)}">` +
    `<input type="checkbox" name="role-${r.id}"${held.includes(r.id) ? " checked" : ""}> ` +
    `${r.name} <span class="muted">— ${esc(r.what)}</span></label>`).join("");
  return `<div class="card" style="margin:0 0 14px; background:var(--bg)">
    <h3 style="margin:0 0 8px">${isEdit ? "Edit user" : "New user"}</h3>
    <form class="user-form">
      ${isEdit ? "" : `<label class="field">Username<input name="username" autocomplete="off" required></label>`}
      <label class="field">Display name<input name="displayName" value="${isEdit ? esc(u.displayName || "") : ""}"></label>
      <label class="field">Email<input name="email" type="email" value="${isEdit ? esc(u.email || "") : ""}"></label>
      ${isEdit ? "" : `<label class="field">Password<input name="password" type="password" autocomplete="new-password" required></label>`}
      <div class="field"><b>Roles</b><div class="muted" style="margin:2px 0 6px">What this account may do. Untick everything and it can only sign in.</div>${boxes}</div>
      ${isEdit ? `<label class="field inline"><input type="checkbox" name="disabled"${u.disabled ? " checked" : ""}> Disabled</label>` : ""}
      <div class="row" style="margin-top:4px"><button class="btn" type="submit" title="${isEdit ? "Save changes to this user" : "Create the user account"}">${isEdit ? "Save changes" : "Create user"}</button></div>
    </form></div>`;
}

// rolesFrom reads the ticked roles back off the form.
//
// It keeps any role the form does not offer. Roles are a free-form list on the
// record — an installation may carry one of its own for its own reporting — and an
// edit dialog that silently dropped it would be an edit nobody made.
const rolesFrom = (fd, u) => {
  const picked = GRANTABLE_ROLES.filter((r) => fd.get("role-" + r.id)).map((r) => r.id);
  const known = new Set(GRANTABLE_ROLES.map((r) => r.id));
  const kept = ((u && u.roles) || []).filter((r) => !known.has(r));
  return picked.concat(kept);
};

async function createUser(fd, reload) {
  try {
    await api("POST", "/api/v1/users", {
      username: (fd.get("username") || "").trim(),
      displayName: (fd.get("displayName") || "").trim(),
      email: (fd.get("email") || "").trim(),
      password: fd.get("password"),
      roles: rolesFrom(fd),
    });
    toast("User created", "ok");
    reload();
  } catch (e) { toast("could not create user: " + e.message, "err"); }
}

async function saveUser(id, fd, reload, u) {
  try {
    await api("PATCH", `/api/v1/users/${encodeURIComponent(id)}`, {
      displayName: (fd.get("displayName") || "").trim(),
      email: (fd.get("email") || "").trim(),
      roles: rolesFrom(fd, u),
      disabled: !!fd.get("disabled"),
    });
    toast("User updated", "ok");
    reload();
  } catch (e) { toast("could not update user: " + e.message, "err"); }
}

async function resetUserPassword(u, reload) {
  const pw = window.prompt(`New password for "${u.username}" (at least 8 characters)`);
  if (pw == null) return;
  try {
    await api("POST", `/api/v1/users/${encodeURIComponent(u.id)}/password`, { password: pw });
    toast("Password updated", "ok");
  } catch (e) { toast("could not set password: " + e.message, "err"); }
  reload();
}

async function toggleUserDisabled(u, reload) {
  try {
    await api("PATCH", `/api/v1/users/${encodeURIComponent(u.id)}`, { disabled: !u.disabled });
    toast(u.disabled ? "User enabled" : "User disabled", "ok");
  } catch (e) { toast("could not update user: " + e.message, "err"); }
  reload();
}

async function deleteUser(u, reload) {
  if (!window.confirm(`Delete user "${u.username}"? This cannot be undone.`)) return;
  try {
    await api("DELETE", `/api/v1/users/${encodeURIComponent(u.id)}`);
    toast(`Deleted "${u.username}"`, "ok");
  } catch (e) { toast("could not delete user: " + e.message, "err"); }
  reload();
}

// ---------- Groups (ADR-0180) ----------
async function createGroup(reload) {
  const name = window.prompt("Group name");
  if (name == null) return;
  const trimmed = name.trim();
  if (!trimmed) { toast("Group name is required", "err"); return; }
  try {
    await api("POST", "/api/v1/groups", { name: trimmed });
    toast(`Created group "${trimmed}"`, "ok");
  } catch (e) { toast("could not create group: " + e.message, "err"); }
  reload();
}

async function renameGroup(g, reload) {
  const name = window.prompt("Rename group", g.name);
  if (name == null) return;
  const trimmed = name.trim();
  if (!trimmed) { toast("Group name is required", "err"); return; }
  try {
    await api("PATCH", `/api/v1/groups/${encodeURIComponent(g.id)}`, { name: trimmed });
    toast("Renamed group", "ok");
  } catch (e) { toast("could not rename group: " + e.message, "err"); }
  reload();
}

async function deleteGroup(g, reload) {
  if (!window.confirm(`Delete group "${g.name}"? Projects shared with it lose that access.`)) return;
  try {
    await api("DELETE", `/api/v1/groups/${encodeURIComponent(g.id)}`);
    toast(`Deleted "${g.name}"`, "ok");
  } catch (e) { toast("could not delete group: " + e.message, "err"); }
  reload();
}

async function addGroupMember(groupId, userId, reload) {
  if (!userId) return;
  try {
    await api("PUT", `/api/v1/groups/${encodeURIComponent(groupId)}/members/${encodeURIComponent(userId)}`);
  } catch (e) { toast("could not add member: " + e.message, "err"); }
  reload();
}

async function removeGroupMember(groupId, userId, reload) {
  try {
    await api("DELETE", `/api/v1/groups/${encodeURIComponent(groupId)}/members/${encodeURIComponent(userId)}`);
  } catch (e) { toast("could not remove member: " + e.message, "err"); }
  reload();
}

// viewConsoleConnectors is the Console's Connectors page: what Atlas can delegate to,
// what this instance has actually configured, and the vault those configurations
// resolve their credentials from. The three read in that order because that is the
// order an operator works in — pick a kind, point it somewhere, give it a credential —
// and the secrets stay with the connectors because a token *reference* and the secret
// it resolves to are one setting entered in two places (ADR-0041 · ADR-0069).
//
// They were the bottom three cards of Organization, under the user roster, the groups
// and the colour picker. That page answers "who uses this instance"; a connector is not
// a person, and the integrations are what an operator comes back to — past everything
// they were filed behind.
async function viewConsoleConnectors() {
  const gen = navGen;
  const pill = (c) => c.status === "active"
    ? `<span class="pill ok"><span class="dot"></span>${esc(c.statusLabel)}</span>`
    : `<span class="pill warn"><span class="dot"></span>${esc(c.statusLabel)}</span>`;
  const connectorRow = (c) => `<tr>
      <td>
        <span class="chip">${esc(c.name)}</span>
        <span class="muted" style="font-size:12px; margin-left:6px">${esc(c.kind)}</span>
        <div class="muted" style="font-size:13px; margin-top:4px">${esc(c.desc)}${
          c.refs ? ` <span style="opacity:.7">(${esc(c.refs)})</span>` : ""}</div>
      </td>
      <td style="text-align:right; white-space:nowrap; vertical-align:top">${pill(c)}</td>
    </tr>`;

  // Managed connector instances (ADR-0041): operator-configured integrations,
  // secret references only. Today the runtime wires the temis decision connector.
  let connectors = [];
  try { connectors = (await api("GET", "/api/v1/connectors")) || []; } catch { /* leave empty */ }
  // What a connector's row shows depends on what this caller may do with it
  // (ADR-0205). The server sends the full record with a `role` to anybody at viewer
  // or above, and a catalog entry — name, kind, enabled — to everybody else, so that
  // a modeller can still author against a connector whose configuration is not
  // theirs. `role` is absent exactly in that second case.
  const connScope = (c) => ({
    role: c.role || "",
    configurable: !!c.role,
    editor: c.role === "editor" || c.role === "owner",
    owner: c.role === "owner",
  });
  const ownershipPill = (c) => {
    const sc = connScope(c);
    if (!sc.configurable) {
      return `<span class="pill vis" title="Somebody else configures this connector. You can still reference it by name in a model.">not yours</span>`;
    }
    if (!sc.owner) {
      return `<span class="pill vis" title="Shared with you as ${esc(sc.role)}">shared with you</span>`;
    }
    const shared = (c.members || []).length;
    return shared
      ? `<span class="pill vis" title="Shared with ${shared} other ${shared === 1 ? "principal" : "principals"}">shared · ${shared}</span>`
      : `<span class="pill vis" title="Only you (and administrators) can configure this">private</span>`;
  };
  // connectorMenu is the row's actions, behind the ⋯ menu every other table in the
  // console puts them behind (ADR-0163). Drawn on the row they were up to seven buttons
  // wide — a wall of identical blue that made every worker look alike, pushed the two
  // that matter (Test, Delete) to the far edge, and grew the table past the width of its
  // card on a laptop. In the menu they cost no width and can be grouped by what they
  // are: what this Worker Type can do, who may configure it, and the two that change or
  // remove it.
  const connectorMenu = (c) => {
    const sc = connScope(c);
    const items = [];
    // Kind-specific first: these are the reasons an operator came to this row rather
    // than to any other, and they exist on no other kind.
    if (c.kind === "clio") items.push({ label: "Provision access…", icon: "🔑", act: "provision" });
    if (c.kind === "clio" || c.kind === "jira") items.push({ label: "Events…", icon: "⇄", act: "subs" });
    // Every kind the check covers: mail connects and authenticates (or sends a test
    // message), a SQL connector dials its connection string. connectorShape is the one
    // place that knows, so the menu does not go stale the next kind that gains one.
    if (connectorShape(c.kind, c.provider).test) items.push({ label: "Test…", icon: "✔", act: "test" });
    if (items.length && (sc.editor || sc.owner)) items.push({ sep: true });
    if (sc.editor) items.push({ label: "Edit…", icon: "✎", act: "edit" });
    if (sc.owner) items.push({ label: "Share…", icon: "👤", act: "share" });
    if (sc.editor) {
      items.push({
        label: c.enabled ? "Disable" : "Enable",
        icon: c.enabled ? "⊘" : "▶",
        act: "toggle",
      });
    }
    if (sc.owner) items.push({ sep: true }, { label: "Delete…", icon: "🗑", act: "delete", danger: true });
    return items;
  };
  // What this worker actually points at, in one line. A native mail provider (Gmail,
  // Microsoft Graph) dials no endpoint at all — it authenticates as the sender — so the
  // line names the provider where the endpoint would be, rather than opening with the
  // stray separator of an empty one.
  const connectorTarget = (c) => {
    const where = c.endpoint || (c.kind === "mail" && c.provider ? c.provider : "");
    const cred = c.credentialsRef ? `token: <code>${esc(c.credentialsRef)}</code>` : "no token";
    return where ? `${esc(where)} · ${cred}` : cred;
  };
  // The Worker cell's text is what the table's column filter searches (table.js), and
  // the usage list it used to spell out was most of that text — typing a process name
  // found the workers it runs through. Collapsed to a count, that text is gone from the
  // cell, so the cell states it: the same words, off the same records, where only the
  // filter reads them.
  const connectorFilterText = (c) => [
    c.name, c.kind, c.provider, c.endpoint, c.credentialsRef,
    ...(c.usedBy || []).map((u) => `${u.name || u.processId} v${u.version}`),
  ].filter(Boolean).join(" ");
  const statusCell = (c) => `<td class="conn-status-col">${!c.enabled
      ? '<span class="pill warn"><span class="dot"></span>disabled</span>'
      : c.problem
        // Stored and enabled, but the runtime could not build its client — so its
        // tasks park. Saying it here is the difference between finding out now and
        // finding out from an incident that claims the connector does not exist
        // (ADR-0155).
        ? `<span class="pill err"><span class="dot"></span>not usable</span>
           <div class="conn-problem" title="${esc(c.problem)}">${esc(c.problem)}</div>`
        : '<span class="pill ok"><span class="dot"></span>enabled</span>'}</td>`;
  const managedRow = (c) => connScope(c).configurable
    ? `<tr data-id="${esc(c.id)}">
      <td data-filter="${esc(connectorFilterText(c))}"><span class="chip">${esc(c.name)}</span>
        <span class="muted" style="font-size:12px; margin-left:6px">${esc(c.kind)}</span>
        ${ownershipPill(c)}
        <div class="muted" style="font-size:12px; margin-top:3px">${connectorTarget(c)}</div>
        ${connectorUsageHTML(c.usedBy)}
        <div class="conn-share" id="share-${esc(c.id)}" hidden></div></td>
      ${statusCell(c)}
      <td class="row-actions">${dropdown("⋯", "icon-btn", connectorMenu(c))}</td></tr>`
    : `<tr data-id="${esc(c.id)}" class="conn-foreign">
      <td data-filter="${esc(connectorFilterText(c))}"><span class="chip">${esc(c.name)}</span>
        <span class="muted" style="font-size:12px; margin-left:6px">${esc(c.kind)}</span>
        ${ownershipPill(c)}
        <div class="muted" style="font-size:12px; margin-top:3px">Configured by somebody else.
        A model can still reference it by name — what it connects to is theirs to see.</div></td>
      ${statusCell(c)}
      <td class="row-actions"></td></tr>`;
  const managedCard = `
    <div class="card" style="padding:0; margin-top:18px">
      <div class="between" style="padding:16px 18px 0">
        <h2>Configured connectors</h2><button class="btn" id="new-connector" title="Configure a new connector">New connector</button>
      </div>
      <p class="muted" style="padding:0 18px; margin:6px 0 12px">Managed <b>temis</b> decision
      and <b>clio</b> event-store connectors a task references by name (ADR-0036/0041/0050). The
      endpoint is stored; the token is a <b>reference</b> resolved from the vault (or
      <code>ATLAS_CONNECTOR_&lt;REF&gt;_TOKEN</code>) at runtime — never stored here.</p>
      <div id="connector-form-slot" style="padding:0 18px"></div>
      <table data-dt-key="connectors">
        <thead><tr><th>Connector</th><th class="conn-status-col">Status</th><th></th></tr></thead>
        <tbody id="connector-rows">${connectors.map(managedRow).join("")
          || `<tr><td colspan="3" class="muted" style="padding:14px 18px">None configured. Business rule tasks marked <i>External (temis connector)</i> resolve by name to these.</td></tr>`}</tbody>
      </table>
    </div>`;

  // The Active-Directory mockup switch (ADR-0181). It sits with the connectors
  // because that is where an operator goes to decide what an integration talks to —
  // and because a worker that simulates every write looks, from everywhere else in
  // Atlas, exactly like one that works.
  let adMock = null;
  try {
    adMock = await api("GET", "/api/v1/settings/ad-mock");
  } catch (e) {
    adMock = null; // an older server, or unreachable: leave the card out entirely
  }
  const adMockCard = !adMock ? "" : `
    <div class="card" style="margin-top:18px">
      <div class="between"><h2>Active Directory</h2>
        <span class="pill ${adMock.enabled ? "warn" : "ok"}"><span class="dot"></span>${
          adMock.enabled ? "mockup" : "real directory"}</span></div>
      <p class="muted" style="margin:6px 0 12px">In <b>mockup mode</b> the AD worker performs every
      operation against a directory in its own memory: no domain controller is touched, and nothing
      in a model changes. It still refuses what Active Directory refuses — creating the same account
      twice, a password over an unencrypted connection, an account that is not there. Switching
      restarts the worker; Atlas keeps running.</p>
      <label style="display:flex; align-items:center; gap:10px; margin-bottom:10px">
        <input type="checkbox" id="admock-on" ${adMock.enabled ? "checked" : ""}>
        <span>Serve Active Directory tasks against a mockup</span>
      </label>
      <div style="margin-bottom:10px">
        <div class="muted" style="font-size:13px; margin-bottom:6px">Starting entries (optional) — the
        accounts and groups a process expects to find. A joiner creates its own account and needs none;
        a leaver has nothing to disable in an empty directory. Atlas keeps this and hands it to the
        worker, so there is no file path to type and nothing to place on the worker's host.
        This is where every forest <i>starts</i> and is never written back: what a mockup run has
        actually put in one is under <a href="#/operations/ad-mock">Operations &rsaquo; Mock directory</a>.</div>
        <div class="between" style="gap:10px; flex-wrap:wrap">
          <span id="admock-seed-state" data-seed-name="${esc(adMock.seedName || "")}" style="font-size:13px">${
            adMock.hasSeed
              ? `<b>${esc(adMock.seedName || "Starting entries")}</b> — ${
                  adMock.seedEntries || "?"} entries, ${esc((adMock.seedFormat || "ldif").toUpperCase())}`
              : `<span class="muted">No starting entries — the mockup begins with an empty directory.</span>`}</span>
          <span style="white-space:nowrap">
            <input type="file" id="admock-seed-file" accept=".ldif,.dsml,.xml,text/plain" style="display:none">
            <button class="btn ghost" id="admock-seed-pick" title="Read an LDIF or DSML file from this computer">Choose a file…</button>
            <button class="btn ghost" id="admock-seed-edit" title="Paste or edit the entries directly">Paste or edit</button>
            <button class="btn ghost" id="admock-seed-example" title="A small directory to try a leaver against">Example</button>
            <button class="btn ghost danger" id="admock-seed-clear" title="Start from an empty directory">Remove</button>
          </span>
        </div>
        <textarea id="admock-seed" rows="9" spellcheck="false" style="display:none; width:100%; margin-top:8px;
                  font-family:ui-monospace,SFMono-Regular,Menlo,monospace; font-size:12px">${esc(adMock.seed || "")}</textarea>
      </div>
      <div class="between">
        <button class="btn" id="admock-save">Save</button>
        <span class="muted" id="admock-note" style="font-size:13px">${
          adMock.configured
            ? "Decided here."
            : "Not decided here yet — whatever the server was started with (ATLAS_AD_MOCK) applies."}</span>
      </div>
    </div>`;

  // The database mockup switch (ADR-0221). It sits beside the AD one
  // because it answers the same question for the other kind of system nobody can
  // safely try a half-finished process against — and because a worker answering from
  // a seed looks, from everywhere else in Atlas, exactly like one talking to a
  // database.
  let sqlMock = null;
  try {
    sqlMock = await api("GET", "/api/v1/settings/sql-mock");
  } catch (e) {
    sqlMock = null; // an older server, or unreachable: leave the card out entirely
  }
  const sqlMockCard = !sqlMock ? "" : `
    <div class="card" style="margin-top:18px">
      <div class="between"><h2>Databases</h2>
        <span class="pill ${sqlMock.enabled ? "warn" : "ok"}"><span class="dot"></span>${
          sqlMock.enabled ? "mockup" : "real databases"}</span></div>
      <p class="muted" style="margin:6px 0 12px">In <b>mockup mode</b> every SQL Server, MariaDB and
      PostgreSQL worker answers statements from prepared answers in its own memory: no database is
      touched, no connection string is needed, and nothing in a model changes. One switch covers all
      three — simulating one product while really writing to another looks like a full mockup run,
      which is the one thing it must never look like. Switching restarts the workers; Atlas keeps
      running.</p>
      <label style="display:flex; align-items:center; gap:10px; margin-bottom:10px">
        <input type="checkbox" id="sqlmock-on" ${sqlMock.enabled ? "checked" : ""}>
        <span>Answer database tasks from a mockup</span>
      </label>
      <div style="margin-bottom:10px">
        <div class="muted" style="font-size:13px; margin-bottom:6px">Prepared answers — what each
        statement returns. A statement with no answer here <b>fails</b> and names itself and the values
        it was given, so you can paste them straight in; it never comes back as an empty result,
        because "no rows" is a real business answer and a mockup that invents one would hand your
        process a fact. An answer may also carry an <code>error</code>, which is how you try the case
        a real database hands you sooner or later.</div>
        <div class="between" style="gap:10px; flex-wrap:wrap">
          <span id="sqlmock-seed-state" data-seed-name="${esc(sqlMock.seedName || "")}" style="font-size:13px">${
            sqlMock.hasSeed
              ? `<b>${esc(sqlMock.seedName || "Prepared answers")}</b> — ${
                  sqlMock.seedAnswers ? `${sqlMock.seedAnswers} answer${sqlMock.seedAnswers === 1 ? "" : "s"}` : "? answers"}`
              : `<span class="muted">No prepared answers — every statement will fail, naming itself.</span>`}</span>
          <span style="white-space:nowrap">
            <input type="file" id="sqlmock-seed-file" accept=".json,application/json,text/plain" style="display:none">
            <button class="btn ghost" id="sqlmock-seed-pick" title="Read a seed file from this computer">Choose a file…</button>
            <button class="btn ghost" id="sqlmock-seed-edit" title="Paste or edit the answers directly">Paste or edit</button>
            <button class="btn ghost" id="sqlmock-seed-example" title="A small seed to try a lookup and an update against">Example</button>
            <button class="btn ghost danger" id="sqlmock-seed-clear" title="Start from a mockup with no answers">Remove</button>
          </span>
        </div>
        <textarea id="sqlmock-seed" rows="12" spellcheck="false" style="display:none; width:100%; margin-top:8px;
                  font-family:ui-monospace,SFMono-Regular,Menlo,monospace; font-size:12px">${esc(sqlMock.seed || "")}</textarea>
      </div>
      <div class="between">
        <button class="btn" id="sqlmock-save">Save</button>
        <span class="muted" id="sqlmock-note" style="font-size:13px">${
          sqlMock.configured
            ? "Decided here."
            : "Not decided here yet — whatever the server was started with (ATLAS_MSSQL_MOCK and friends) applies."}</span>
      </div>
    </div>`;

  // Encrypted secret vault (ADR-0069): credentials a connector's token reference
  // resolves to, sealed at rest. Every op is admin-gated and the vault may be
  // unconfigured (no master key), so distinguish those states from a populated list.
  let secrets = [];
  let secretsState = "ok"; // "ok" | "denied" | "unconfigured"
  try {
    secrets = (await api("GET", "/api/v1/secrets")) || [];
  } catch (e) {
    secretsState = /admin/i.test(e.message) ? "denied" : "unconfigured";
  }
  // A secret's value is write-only, so the one thing the list can still say about it
  // is what it is *for* — which connector resolves this reference, and therefore what
  // shape the value has to have. Without that a rotation is done blind (ADR-0155).
  // The key line names the *vault master key* — ADR-0069's keyId, a fingerprint of the
  // one active key that seals every secret — not a per-secret key. The same value on
  // every row is therefore the healthy state; a row that differs was sealed under a key
  // that has since been rotated and no longer opens, which is why the line is shown.
  const secretRow = (c) => {
    const users = (connectors || []).filter((k) => k.credentialsRef === c.name);
    const usedBy = users.length
      ? users.map((k) => `<span class="chip">${esc(k.name)}</span> <span class="muted">${esc(k.kind)}${k.provider ? " · " + esc(k.provider) : ""}</span>`).join(", ")
      : `<span class="muted">not referenced by any connector</span>`;
    return `<tr data-name="${esc(c.name)}">
      <td><span class="chip">${esc(c.name)}</span>
        <div class="muted" style="font-size:12px; margin-top:3px">used by ${usedBy}</div>
        <div class="muted" style="font-size:12px; margin-top:3px" title="Fingerprint of the vault master key this secret is sealed under. One key seals them all, so it reads the same on every secret until the key is rotated.">sealed under vault key <code>${esc(c.keyId)}</code> · updated ${esc(fmtTime(c.updatedAt))}</div></td>
      <td style="text-align:right; white-space:nowrap">
        <button class="btn ghost" data-sact="set" title="Set or rotate this secret’s value">Set value</button>
        <button class="btn ghost danger" data-sact="delete" title="Delete this secret from the vault">Delete</button>
      </td></tr>`;
  };
  const secretsCard = secretsState === "denied"
    ? `<div class="card" style="margin-top:18px"><h2>Secrets</h2><p class="muted">Managing secrets requires the admin role.</p></div>`
    : secretsState === "unconfigured"
    ? `<div class="card" style="margin-top:18px"><h2>Secrets</h2><p class="muted">The encrypted
        secret vault is not configured. Start the server with <code>ATLAS_VAULT_KEY</code> (a
        32-byte key, base64 or hex) or <code>ATLAS_VAULT_KEY_FILE</code> to store connector
        credentials here, encrypted at rest (ADR-0069).</p></div>`
    : `<div class="card" style="padding:0; margin-top:18px">
        <div class="between" style="padding:16px 18px 0">
          <h2>Secrets</h2><button class="btn" id="new-secret" title="Store a new secret in the encrypted vault">New secret</button>
        </div>
        <p class="muted" style="padding:0 18px; margin:6px 0 12px">Credentials a connector's
        <b>token reference</b> resolves to, sealed at rest with AES-256-GCM (ADR-0069). The value
        is <b>never</b> shown after it is set — only its name and metadata. A reference resolves
        from the vault first, then <code>ATLAS_CONNECTOR_&lt;REF&gt;_TOKEN</code>.</p>
        <div id="secret-form-slot" style="padding:0 18px"></div>
        <table data-dt-key="secrets">
          <thead><tr><th>Secret</th><th></th></tr></thead>
          <tbody id="secret-rows">${secrets.map(secretRow).join("")
            || `<tr><td colspan="2" class="muted" style="padding:14px 18px">None stored. Add one, then point a connector's token reference at its name.</td></tr>`}</tbody>
        </table>
      </div>`;

  if (superseded(gen)) return; // navigated away while the connectors/secrets loaded
  view.innerHTML = `
    <div class="card" style="padding:0">
      <div class="between" style="padding:16px 18px 0"><h1 style="margin:0">Connectors</h1></div>
      <p class="muted" style="padding:0 18px; margin:6px 0 12px">Sibling engines Atlas
      delegates to. Each is an org-wide integration, shared across every process.</p>
      <table><tbody>${CONNECTORS.map(connectorRow).join("")}</tbody></table>
    </div>
    ${managedCard}
    ${adMockCard}
    ${sqlMockCard}
    ${secretsCard}`;
  wireConnectorManagement(connectors);
  wireADMock();
  wireSQLMock();
  wireSecretsManagement(secrets, secretsState, connectors);
}

async function viewConsoleOrg() {
  const gen = navGen;

  // Load the user roster. A 403 means a signed-in non-admin — show a notice
  // rather than an error card.
  let users = null;
  let denied = false;
  try {
    users = await api("GET", "/api/v1/users");
  } catch (e) {
    denied = /admin/i.test(e.message);
    // If a newer navigation superseded us, swallow the error rather than let it
    // reach route()'s catch, which would render an error card over the new view.
    if (!denied) { if (superseded(gen)) return; throw e; }
  }

  const me = AUTH.user;
  const roleChips = (roles) => (roles || []).map((r) => `<span class="chip">${esc(r)}</span>`).join(" ");
  const statusPill = (u) => u.disabled
    ? `<span class="pill warn"><span class="dot"></span>disabled</span>`
    : `<span class="pill ok"><span class="dot"></span>active</span>`;
  const userRow = (u) => `<tr data-id="${esc(u.id)}">
      <td><span class="chip">${esc(u.username)}</span>${
        me && u.id === me.id ? ' <span class="muted" style="font-size:12px">(you)</span>' : ""}</td>
      <td>${esc(u.displayName || "—")}${u.email ? `<div class="muted" style="font-size:12px">${esc(u.email)}</div>` : ""}</td>
      <td>${roleChips(u.roles)}</td>
      <td>${statusPill(u)}</td>
      <td style="text-align:right; white-space:nowrap">
        <button class="btn ghost" data-act="edit" title="Edit this user’s details and roles">Edit</button>
        <button class="btn ghost" data-act="password" title="Set a new password for this user">Password</button>
        <button class="btn ghost" data-act="toggle" title="${u.disabled ? "Re-enable this account" : "Disable this account (blocks sign-in)"}">${u.disabled ? "Enable" : "Disable"}</button>
        <button class="btn ghost danger" data-act="delete" title="Delete this user account">Delete</button>
      </td></tr>`;

  const usersCard = denied
    ? `<div class="card"><h2>Users</h2><p class="muted">Managing users requires the admin role.</p></div>`
    : `<div class="card" style="padding:0">
        <div class="between" style="padding:16px 18px 0">
          <h2>Users</h2><button class="btn" id="new-user" title="Create a new user account">New user</button>
        </div>
        <p class="muted" style="padding:0 18px; margin:6px 0 12px">${AUTH.enabled
          ? "Login is enforced for this instance."
          : "Login is <b>not</b> enforced — start the server with <code>--auth</code> to require these accounts."}
          Every route names the role that reaches it: <span class="chip">admin</span> for this page and the
          rest of the instance's configuration, <span class="chip">modeler</span> to deploy and to author,
          <span class="chip">operator</span> to run what is deployed, <span class="chip">user</span> for a
          person's own task list.</p>
        <div id="user-form-slot" style="padding:0 18px"></div>
        <table data-dt-key="users">
          <thead><tr><th>User</th><th>Name</th><th>Roles</th><th>Status</th><th></th></tr></thead>
          <tbody id="user-rows">${(users || []).map(userRow).join("")
            || `<tr><td colspan="5" class="muted" style="padding:14px 18px">No users yet.</td></tr>`}</tbody>
        </table>
      </div>`;

  // Groups (ADR-0180): a named set of users a project can be
  // shared with at once. Admin-gated like users, so only load when not denied.
  let groups = [];
  if (!denied) {
    try { groups = (await api("GET", "/api/v1/groups")) || []; } catch { /* leave empty */ }
  }
  const usersById = new Map((users || []).map((u) => [u.id, u]));
  const memberName = (id) => { const u = usersById.get(id); return u ? (u.displayName || u.username) : id; };
  const groupRow = (g) => {
    const memberIds = new Set(g.members || []);
    const chips = (g.members || []).length
      ? (g.members || []).map((uid) => `<span class="chip">${esc(memberName(uid))}<button type="button" class="chip-x" data-gact="rmmember" data-gid="${esc(g.id)}" data-uid="${esc(uid)}" title="Remove from group">✕</button></span>`).join(" ")
      : `<span class="muted" style="font-size:12px">No members yet.</span>`;
    const eligible = (users || []).filter((u) => !memberIds.has(u.id));
    const addCtl = eligible.length
      ? `<span class="add-row" style="display:inline-flex; gap:6px; margin-top:8px">
           <select class="field" data-add-for="${esc(g.id)}" style="margin:0">
             ${eligible.map((u) => `<option value="${esc(u.id)}">${esc(u.displayName || u.username)}</option>`).join("")}
           </select>
           <button type="button" class="btn ghost" data-gact="addmember" data-gid="${esc(g.id)}">Add</button>
         </span>`
      : `<div class="muted" style="font-size:12px; margin-top:8px">Every user is a member.</div>`;
    return `<tr data-id="${esc(g.id)}">
      <td><span class="chip">${esc(g.name)}</span>
        <div style="margin-top:6px">${chips}</div>
        ${addCtl}</td>
      <td style="text-align:right; white-space:nowrap; vertical-align:top">
        <button class="btn ghost" data-gact="rename" title="Rename this group">Rename</button>
        <button class="btn ghost danger" data-gact="delete" title="Delete this group">Delete</button>
      </td></tr>`;
  };
  const groupsCard = denied ? "" : `
    <div class="card" style="padding:0; margin-top:18px">
      <div class="between" style="padding:16px 18px 0">
        <h2>Groups</h2><button class="btn" id="new-group" title="Create a new group">New group</button>
      </div>
      <p class="muted" style="padding:0 18px; margin:6px 0 12px">A named set of users. Share a project
      with a group and every member gets that role (ADR-0180). A membership change
      takes effect on the member's next sign-in.</p>
      <table data-dt-key="groups">
        <thead><tr><th>Group</th><th></th></tr></thead>
        <tbody id="group-rows">${groups.map(groupRow).join("")
          || `<tr><td colspan="2" class="muted" style="padding:14px 18px">No groups yet.</td></tr>`}</tbody>
      </table>
    </div>`;

  // Single sign-on: which provider (if any) this instance offers, and what its
  // claims are allowed to decide here. Admin-gated like the roster, so it is only
  // loaded when the roster was not denied.
  let provider = null;
  let mapping = null;
  if (!denied) {
    try { provider = ((await api("GET", "/api/v1/auth/providers")) || [])[0] || null; } catch { /* none */ }
    if (provider) {
      try { mapping = await api("GET", "/api/v1/settings/oidc-mapping"); } catch { mapping = null; }
    }
  }

  if (superseded(gen)) return; // navigated away while the roster loaded
  view.innerHTML = `
    <div class="card">
      <h1>Organization</h1>
      <p class="muted">${AUTH.enabled
        ? `Signed in as <b>${esc((me && me.username) || "")}</b>. This instance has multi-user access enabled.`
        : "Single-user mode: the API and UI are open. Enable login with <code>--auth</code> to enforce the accounts below."}</p>
    </div>
    ${usersCard}
    ${groupsCard}
    ${denied ? "" : ssoCard(provider, mapping, groups)}
    ${appearanceCard()}`;

  // Appearance is wired before the (admin-gated) user handlers so it works even when
  // the user roster is denied to a non-admin.
  wireAppearance();
  wireSSO(groups);

  if (denied) return;
  const reload = () => viewConsoleOrg();
  const slot = document.getElementById("user-form-slot");
  document.getElementById("new-user").addEventListener("click", () => {
    if (slot.dataset.mode === "new") { slot.innerHTML = ""; slot.dataset.mode = ""; return; }
    slot.dataset.mode = "new";
    slot.innerHTML = userForm(null);
    slot.querySelector(".user-form").addEventListener("submit", (e) => {
      e.preventDefault();
      createUser(new FormData(e.target), reload);
    });
  });
  document.getElementById("user-rows").addEventListener("click", (e) => {
    const btn = e.target.closest("button[data-act]");
    if (!btn) return;
    const u = (users || []).find((x) => x.id === btn.closest("tr").dataset.id);
    if (!u) return;
    switch (btn.dataset.act) {
      case "edit":
        slot.dataset.mode = "edit";
        slot.innerHTML = userForm(u);
        slot.querySelector(".user-form").addEventListener("submit", (ev) => {
          ev.preventDefault();
          saveUser(u.id, new FormData(ev.target), reload, u);
        });
        slot.scrollIntoView({ block: "nearest" });
        break;
      case "password": resetUserPassword(u, reload); break;
      case "toggle": toggleUserDisabled(u, reload); break;
      case "delete": deleteUser(u, reload); break;
    }
  });

  document.getElementById("new-group").addEventListener("click", () => createGroup(reload));
  document.getElementById("group-rows").addEventListener("click", (e) => {
    const btn = e.target.closest("button[data-gact]");
    if (!btn) return;
    const row = btn.closest("tr");
    const g = (groups || []).find((x) => x.id === (row && row.dataset.id));
    switch (btn.dataset.gact) {
      case "rename": if (g) renameGroup(g, reload); break;
      case "delete": if (g) deleteGroup(g, reload); break;
      case "addmember": {
        const sel = row.querySelector(`select[data-add-for="${btn.dataset.gid}"]`);
        if (sel) addGroupMember(btn.dataset.gid, sel.value, reload);
        break;
      }
      case "rmmember": removeGroupMember(btn.dataset.gid, btn.dataset.uid, reload); break;
    }
  });
}

// ---------- Single sign-on (ADR-0210) ----------
//
// Two questions are answered here, and only these two: which claim in the
// provider's token to read, and what its values grant. Where the provider is —
// issuer, client id, secret — is start-up configuration and deliberately not
// editable from a browser.
//
// The switch is worth the sentence it carries: from the moment it is on, whoever
// administers the provider's groups administers this instance's roles, and a role
// granted by hand here is replaced at that person's next sign-in.

// SSO_ROLES is what a rule may grant. `user` is missing on purpose — everybody who
// can sign in at all holds it, so offering it as a grant would suggest it could be
// withheld.
const SSO_ROLES = ["admin", "modeler", "operator"];

function ssoRuleRow(rule, groups) {
  const roles = new Set(rule.roles || []);
  const inGroups = new Set(rule.groups || []);
  const box = (kind, value, label, on) => `<label class="field inline" style="margin:0 12px 4px 0">
      <input type="checkbox" data-sso="${kind}" value="${esc(value)}"${on ? " checked" : ""} />
      <span>${esc(label)}</span></label>`;
  return `<tr class="sso-rule">
      <td style="vertical-align:top"><input class="field" data-sso="value" style="margin:0"
        value="${esc(rule.value || "")}" placeholder="atlas-modeller" spellcheck="false"
        aria-label="Claim value" /></td>
      <td style="vertical-align:top">${SSO_ROLES.map((r) => box("role", r, r, roles.has(r))).join("")}</td>
      <td style="vertical-align:top">${groups.length
        ? groups.map((g) => box("group", g.id, g.name, inGroups.has(g.id))).join("")
        : `<span class="muted" style="font-size:12px">No groups yet.</span>`}</td>
      <td style="text-align:right; vertical-align:top">
        <button type="button" class="btn ghost danger" data-sso-act="remove"
          title="Delete this rule">Remove</button></td>
    </tr>`;
}

function ssoCard(provider, mapping, groups) {
  if (!provider) {
    return `
      <div class="card" style="margin-top:18px">
        <h2>Single sign-on</h2>
        <p class="muted" style="margin:6px 0 0">No identity provider is configured, so everybody signs in
        with a username and password. Start the server with <code>--oidc-issuer</code>,
        <code>--oidc-client-id</code> and <code>--oidc-client-secret</code> to offer one.</p>
      </div>`;
  }
  const m = mapping || { enabled: false, claim: "", rules: [] };
  const rules = m.rules || [];
  return `
    <div class="card" id="sso-card" style="margin-top:18px">
      <div class="between"><h2>Single sign-on</h2>
        <button type="button" class="btn" id="sso-save" title="Store this mapping">Save mapping</button></div>
      <p class="muted" style="margin:6px 0 14px">People can sign in with
      <b>${esc(provider.name || provider.id)}</b>. Below is what that provider's claims decide here.
      A rule matches one claim value exactly and grants what it names; somebody the provider says
      nothing about matches nothing and is granted nothing. Everybody who can sign in holds
      <span class="chip">user</span> either way.</p>
      <label class="field inline" style="margin:0 0 10px">
        <input type="checkbox" id="sso-enabled"${m.enabled ? " checked" : ""} />
        <span>Let the provider's claims decide roles and group membership</span>
      </label>
      <p class="muted" style="margin:0 0 14px; font-size:12px">While this is on, whoever administers the
      provider's groups administers this instance's roles: a role granted by hand is replaced at that
      person's next sign-in, and so is their membership of the groups named below. Groups no rule names
      are left alone.</p>
      <label class="field" style="max-width:420px">
        <span>Claim to read</span>
        <input type="text" id="sso-claim" value="${esc(m.claim || "")}" spellcheck="false"
          placeholder="groups" aria-label="Claim to read" />
      </label>
      <p class="muted" style="margin:6px 0 14px; font-size:12px">A claim name, or a dotted path for the
      providers that nest it — <code>groups</code>, <code>roles</code>,
      <code>realm_access.roles</code>.</p>
      <table>
        <thead><tr><th style="width:26%">Claim value</th><th style="width:28%">Grants roles</th>
          <th>Adds to groups</th><th></th></tr></thead>
        <tbody id="sso-rules">${rules.map((r) => ssoRuleRow(r, groups)).join("")}</tbody>
      </table>
      <button type="button" class="btn ghost" id="sso-add" style="margin-top:12px"
        title="Add a rule">Add rule</button>
    </div>`;
}

function wireSSO(groups) {
  const card = document.getElementById("sso-card");
  if (!card) return;
  const rows = document.getElementById("sso-rules");
  card.addEventListener("click", async (e) => {
    if (e.target.closest("#sso-add")) {
      rows.insertAdjacentHTML("beforeend", ssoRuleRow({}, groups));
      return;
    }
    const rm = e.target.closest("button[data-sso-act='remove']");
    if (rm) { rm.closest("tr").remove(); return; }
    if (!e.target.closest("#sso-save")) return;
    const body = {
      enabled: document.getElementById("sso-enabled").checked,
      claim: document.getElementById("sso-claim").value.trim(),
      rules: [...rows.querySelectorAll("tr.sso-rule")].map((tr) => ({
        value: tr.querySelector("[data-sso='value']").value.trim(),
        roles: [...tr.querySelectorAll("[data-sso='role']:checked")].map((c) => c.value),
        groups: [...tr.querySelectorAll("[data-sso='group']:checked")].map((c) => c.value),
      })),
    };
    try {
      await api("PUT", "/api/v1/settings/oidc-mapping", body);
      toast(body.enabled ? "Sign-on mapping saved and in effect" : "Sign-on mapping saved (switched off)", "ok");
    } catch (err) {
      // The server refuses a mapping that could not do what it says, and its reason
      // names the rule — so show it rather than a generic failure.
      toast(err.message || "Couldn't save the mapping", "err");
    }
  });
}

// appearanceCard renders the "Appearance" panel in Organization: a row of brand
// presets plus a custom colour picker that re-tints the whole UI to a company
// colour (theme.js). The choice is org-wide — persisted on the server and applied
// for every user of the instance; setting it needs the admin role when auth is on.
function appearanceCard() {
  const active = currentAccent();
  const swatch = (p) => {
    const on = normalizeHex(p.color) === active;
    return `<button type="button" class="theme-swatch${on ? " active" : ""}" data-color="${esc(p.color)}"
        title="${esc(p.name)}" aria-pressed="${on}">
        <span class="theme-dot" style="background:${esc(p.color)}"></span>${esc(p.name)}</button>`;
  };
  return `
    <div class="card" style="margin-top:18px">
      <div class="between"><h2>Appearance</h2>
        <button type="button" class="btn ghost sm" id="theme-reset" title="Clear the custom brand colour and restore the default accent">Reset to default</button></div>
      <p class="muted" style="margin:6px 0 14px">Tint the interface with your organisation's brand
      colour. The accent recolours buttons, links, the active navigation and highlights across every
      view — applied for everyone on this instance.</p>
      <div class="theme-swatches" id="theme-presets">${PRESETS.map(swatch).join("")}</div>
      <div class="theme-custom">
        <label class="field inline" style="margin:0">
          <input type="color" id="theme-color" value="${esc(active)}" aria-label="Custom brand colour" />
          <span>Custom colour</span>
        </label>
        <input type="text" id="theme-hex" class="theme-hex" value="${esc(active)}"
          spellcheck="false" aria-label="Brand colour hex" />
        <span class="theme-preview">
          <button type="button" class="btn sm" tabindex="-1">Primary</button>
          <a href="#" onclick="return false" class="theme-link">Link</a>
          <span class="pill">Accent</span>
        </span>
      </div>

      <div class="logo-row">
        <div class="between"><h3 style="margin:0">Logo</h3>
          <button type="button" class="btn ghost sm" id="logo-remove"${hasLogoCached() ? "" : " hidden"} title="Remove the uploaded logo and restore the built-in mark">Remove logo</button></div>
        <p class="muted" style="margin:6px 0 12px">Replace the built-in mark with your organisation's logo —
        a PNG or SVG up to 512&nbsp;KiB, shown in the top bar and on the login screen for everyone on this instance.</p>
        <div class="logo-controls">
          <span class="mark logo-sample${hasLogoCached() ? " has-logo" : ""}" aria-hidden="true">${
            hasLogoCached() ? `<img class="mark-img" alt="" src="${esc(LOGO_URL)}" />` : BUILTIN_MARK
          }</span>
          <label class="btn sm" style="cursor:pointer">
            Upload logo…
            <input type="file" id="logo-file" accept="image/png,image/svg+xml" hidden />
          </label>
        </div>
      </div>
    </div>`;
}

// wireAppearance connects the preset swatches, the colour picker and the hex box
// to theme.js. Picking a colour previews it instantly (a local root re-tint), then
// persists it org-wide on the server; a failed write (e.g. a non-admin) is surfaced
// and the preview reverts. Reset clears the org-wide override.
function wireAppearance() {
  const presets = document.getElementById("theme-presets");
  const picker = document.getElementById("theme-color");
  const hex = document.getElementById("theme-hex");
  const reset = document.getElementById("theme-reset");
  if (!presets || !picker || !hex || !reset) return;

  // reflectActive re-syncs the controls to the accent now in effect: the picker,
  // the hex text, and which preset (if any) reads as selected.
  const reflectActive = () => {
    const active = currentAccent();
    picker.value = active;
    hex.value = active;
    presets.querySelectorAll(".theme-swatch").forEach((b) => {
      const on = normalizeHex(b.dataset.color) === active;
      b.classList.toggle("active", on);
      b.setAttribute("aria-pressed", on ? "true" : "false");
    });
  };

  // preview re-tints the UI locally for immediate feedback (e.g. while dragging the
  // picker) without persisting — commit() is what saves.
  const preview = (color) => { const n = normalizeHex(color); if (n) applyAccent(n); };

  // commit persists the chosen colour org-wide, then reflects it; on failure it
  // reverts the preview and the controls to whatever is actually in effect.
  const commit = async (color) => {
    const norm = normalizeHex(color);
    if (!norm) { reflectActive(); applyCurrent(); return; }
    try {
      await setServerAccent(norm);
      reflectActive();
      toast("Theme updated for everyone", "ok");
    } catch (e) {
      applyCurrent();
      reflectActive();
      toast(e.message || "Couldn't save theme", "err");
    }
  };

  presets.addEventListener("click", (e) => {
    const btn = e.target.closest(".theme-swatch");
    if (btn) { preview(btn.dataset.color); commit(btn.dataset.color); }
  });
  // The native picker fires input continuously while dragging — preview live and
  // persist once, on change (drag end).
  picker.addEventListener("input", () => preview(picker.value));
  picker.addEventListener("change", () => commit(picker.value));
  // The hex box commits on Enter or blur, reverting the field if it doesn't parse.
  const commitHex = () => {
    if (normalizeHex(hex.value)) { preview(hex.value); commit(hex.value); }
    else { hex.value = currentAccent(); }
  };
  hex.addEventListener("keydown", (e) => { if (e.key === "Enter") { e.preventDefault(); commitHex(); } });
  hex.addEventListener("blur", commitHex);

  reset.addEventListener("click", async () => {
    try {
      await resetServerAccent();
      applyAccent(null);
      reflectActive();
      toast("Theme reset for everyone", "ok");
    } catch (e) {
      toast(e.message || "Couldn't reset theme", "err");
    }
  });

  wireLogo();
}

// wireLogo connects the Appearance panel's logo upload and remove controls to
// logo.js. Uploading previews the new mark instantly (applyLogo runs against the
// whole shell) and persists it org-wide; a failed write (e.g. a non-admin) is
// surfaced. Remove clears it back to the built-in Atlas mark.
function wireLogo() {
  const file = document.getElementById("logo-file");
  const remove = document.getElementById("logo-remove");
  const sample = document.querySelector(".logo-sample");
  if (!file || !remove || !sample) return;

  // reflectLogo re-syncs the panel's own preview and the Remove button to the
  // presence now in effect. The shell marks are repainted by logo.js directly.
  const reflectLogo = (present) => {
    remove.hidden = !present;
    if (present) {
      if (!sample.querySelector("img")) {
        sample.textContent = "";
        const img = document.createElement("img");
        img.className = "mark-img";
        img.alt = "";
        img.src = LOGO_URL + "?t=" + Date.now(); // bypass the cache so a re-upload shows
        sample.appendChild(img);
        sample.classList.add("has-logo");
      } else {
        sample.querySelector("img").src = LOGO_URL + "?t=" + Date.now();
      }
    } else {
      sample.classList.remove("has-logo");
      sample.textContent = "A";
    }
  };

  file.addEventListener("change", async () => {
    const chosen = file.files && file.files[0];
    if (!chosen) return;
    try {
      await setServerLogo(chosen);
      reflectLogo(true);
      toast("Logo updated for everyone", "ok");
    } catch (e) {
      toast(e.message || "Couldn't save logo", "err");
    } finally {
      file.value = ""; // allow re-choosing the same file after a failure
    }
  });

  remove.addEventListener("click", async () => {
    try {
      await deleteServerLogo();
      reflectLogo(false);
      toast("Logo removed for everyone", "ok");
    } catch (e) {
      toast(e.message || "Couldn't remove logo", "err");
    }
  });
}

// groupByProcess collapses deployment versions into one entry per process id,
// newest version first, so the list shows a process — not a row per version.
function groupByProcess(procs) {
  const byId = new Map();
  for (const p of procs) {
    if (!byId.has(p.processId)) byId.set(p.processId, []);
    byId.get(p.processId).push(p);
  }
  const groups = [...byId.entries()].map(([processId, versions]) => {
    versions.sort((a, b) => b.version - a.version);
    return { processId, versions, latest: versions[0] };
  });
  groups.sort((a, b) => b.latest.deployedAt - a.latest.deployedAt);
  return groups;
}

function sectionState(id) {
  try { return localStorage.getItem("atlas.sec." + id) !== "0"; } catch { return true; }
}
function toggleSection(id, btn) {
  const body = document.getElementById("sec-" + id);
  if (!body) return;
  const open = body.hidden;
  body.hidden = !open;
  btn.setAttribute("aria-expanded", String(open));
  try { localStorage.setItem("atlas.sec." + id, open ? "1" : "0"); } catch { /* ignore */ }
}

// viewModelerHome is the application landscape: a clean table of process
// applications (each a container of artifacts — the ADR-0034 project reframed by
// ADR-0128) plus a collapsible list of deployed definitions. Artifact editing
// happens inside an application (viewProjectDetail), which keeps this overview
// tidy. "Create new" is a single dropdown.
async function viewModelerHome() {
  view.innerHTML = `
    <div class="between">
      <h1>Applications</h1>
      ${dropdown("Create new", "btn", [
        { label: "New application", icon: "📦", act: "new-project" },
        { sep: true },
        { header: "Blank resources" },
        { label: "BPMN diagram", icon: "⚙", href: "#/modeler/new" },
        { label: "Form", icon: "▤", href: "#/modeler/form/new" },
        { sep: true },
        { label: "Import file…", icon: "📥", act: "import" },
        { label: "Import MIM workflow (XOML)…", icon: "🔁", act: "import-mim" },
        { label: "Import source tree…", icon: "🗂", act: "import-source" },
      ])}
    </div>
    <div class="card" style="padding:0; margin-top:14px">
      <table data-dt-key="projects">
        <thead><tr><th>Name</th><th>Artifacts</th><th>Last changed</th><th></th></tr></thead>
        <tbody id="proj-rows"><tr><td colspan="4" class="empty">Loading…</td></tr></tbody>
      </table>
    </div>
    <h2 style="margin:22px 0 10px"><button class="section-toggle" aria-expanded="${sectionState("deployed")}" data-section="deployed" title="Show or hide the deployed processes">Deployed</button></h2>
    <div class="section-body" id="sec-deployed"${sectionState("deployed") ? "" : ' hidden'}>
      <div id="rows"><p class="muted" style="padding:14px 2px">Loading…</p></div>
    </div>`;
  for (const t of view.querySelectorAll(".section-toggle"))
    t.addEventListener("click", () => toggleSection(t.dataset.section, t));
  const rows = document.getElementById("rows");
  const projRows = document.getElementById("proj-rows");

  const renderProjects = async () => {
    let projects = [], drafts = [], refs = [], forms = [];
    try {
      [projects, drafts, refs, forms] = await Promise.all([
        api("GET", "/api/v1/applications"),
        api("GET", "/api/v1/drafts"),
        api("GET", "/api/v1/dmnrefs"),
        api("GET", "/api/v1/forms"),
      ]);
    } catch (e) { projRows.innerHTML = `<tr><td colspan="4" class="empty">${esc(e.message)}</td></tr>`; return; }

    const known = new Set(projects.map((p) => p.id));
    const all = [...drafts, ...refs, ...forms];
    const countIn = (pid) => all.filter((a) => (a.projectId || "") === pid).length;
    const ungrouped = all.filter((a) => !a.projectId || !known.has(a.projectId));

    const projectRow = (p) => {
      const n = countIn(p.id);
      const href = `#/modeler/p/${encodeURIComponent(p.id)}`;
      const owner = p.myRole === "owner";
      const items = [{ label: "Open", icon: "→", href }];
      if (AUTH.enabled && owner) items.push({ label: "Share…", icon: "👤", act: "share", data: { id: p.id } });
      if (owner) items.push(
        { label: "Rename", icon: "✎", act: "rename", data: { id: p.id, name: p.name } },
        { sep: true },
        { label: "Delete", icon: "🗑", act: "del", data: { id: p.id, name: p.name }, danger: true },
      );
      return `<tr>
        <td data-filter="${esc(p.name)}"><div class="artifact-name"><span class="mi-icon">📦</span><a href="${href}"><b>${esc(p.name)}</b></a>${visBadge(p)}</div></td>
        <td class="muted">${n}</td>
        <td class="muted" data-sort="${p.updatedAt || 0}">${esc(fmtTime(p.updatedAt))}</td>
        <td class="row-actions">${dropdown("⋯", "icon-btn", items)}</td>
      </tr>`;
    };
    const ungroupedRow = ungrouped.length ? `<tr>
        <td><div class="artifact-name"><span class="mi-icon">🗂</span><a href="#/modeler/p/ungrouped">Not assigned</a>
          <span class="muted" style="font-size:12px">· not in an application</span></div></td>
        <td class="muted">${ungrouped.length}</td><td class="muted">—</td><td></td>
      </tr>` : "";

    projRows.innerHTML = (projects.map(projectRow).join("") + ungroupedRow) ||
      `<tr><td colspan="4" class="empty">No applications yet. Use <b>Create new</b> to add one.</td></tr>`;
    onMenuAction(projRows, (act, b) => {
      if (act === "rename") renameProject(b.dataset.id, b.dataset.name, renderProjects);
      if (act === "del") deleteProject(b.dataset.id, b.dataset.name, renderProjects);
      if (act === "share") { const pr = projects.find((x) => x.id === b.dataset.id); if (pr) shareProject(pr, renderProjects); }
    });
  };
  onMenuAction(view, (act) => {
    if (act === "new-project") createProject(renderProjects);
    if (act === "import") importArtifact("", renderProjects);
    if (act === "import-mim") importMIM("", renderProjects);
    if (act === "import-source") importApplicationSource(renderProjects);
  });

  // One deployed process = one row. A process may have several deployed versions;
  // groupByProcess collapsed them, so the row shows the latest and a version count.
  const deployRow = (g) => {
    const older = g.versions.length > 1
      ? ` <span class="muted">· ${g.versions.length} versions</span>` : "";
    const label = g.latest.name || g.processId;
    const sub = g.latest.name
      ? `<div class="muted" style="font-size:12px">${esc(g.processId)}</div>` : "";
    // A deactivated definition stays deployed but does not auto-start new instances
    // from its timer/message/signal start events (ADR-0119). Flag it and offer the
    // inverse toggle.
    const inactive = g.latest.active === false;
    const badge = inactive
      ? ` <span class="pill warn" title="Deployed but paused: no new instances auto-start from its timer, message, or signal start events">Inactive</span>`
      : "";
    const toggleLabel = inactive ? "Activate" : "Deactivate";
    const toggleTitle = inactive
      ? "Resume automatic starts (timer/message/signal start events)"
      : "Pause automatic starts: keep it deployed but stop new instances from starting on their own";
    return `<tr>
      <td><a href="#/modeler/d/${g.latest.key}"><b>${esc(label)}</b></a>${badge}${sub}</td>
      <td>v${g.latest.version}${older}</td>
      <td class="muted">${esc(fmtTime(g.latest.deployedAt))}</td>
      <td style="text-align:right; white-space:nowrap">
        <button class="btn ghost" data-toggle="${g.latest.key}" data-active="${inactive ? "1" : "0"}" title="${toggleTitle}">${toggleLabel}</button>
        <a class="btn ghost" href="#/modeler/d/${g.latest.key}">Open</a>
        <button class="btn ghost danger" data-del="${esc(g.processId)}" title="Delete this deployed process and all its versions">Delete</button>
      </td>
    </tr>`;
  };
  const deployTable = (gs) => `<div class="card" style="padding:0">
      <table class="no-enhance">
        <thead><tr><th>Process</th><th>Latest</th><th>Deployed</th><th></th></tr></thead>
        <tbody>${gs.map(deployRow).join("")}</tbody>
      </table>
    </div>`;

  const render = async () => {
    try {
      const [procs, projects] = await Promise.all([
        api("GET", "/api/v1/processes"),
        api("GET", "/api/v1/applications"),
      ]);
      const groups = groupByProcess(procs);
      if (!groups.length) {
        rows.innerHTML = `<p class="empty" style="padding:14px">
          Nothing deployed yet. <a href="#/modeler/new">Create a diagram</a>, save it as a draft, then Deploy &amp; run.</p>`;
        return;
      }
      // Group deployed definitions into the same project folders as design-time
      // artifacts (ADR-0034): bucket by each process's projectId, ordering the
      // buckets like the projects table above, then a trailing "Ungrouped" bucket for
      // definitions with no (or an unknown) project. With no project at all, fall back
      // to the plain flat table so a project-less install is unchanged.
      const known = new Map(projects.map((p) => [p.id, p.name]));
      const byProject = new Map();
      for (const g of groups) {
        const pid = g.latest.projectId && known.has(g.latest.projectId) ? g.latest.projectId : "";
        if (!byProject.has(pid)) byProject.set(pid, []);
        byProject.get(pid).push(g);
      }
      const buckets = [];
      for (const p of projects) {
        if (byProject.has(p.id)) buckets.push({ id: p.id, name: p.name, icon: "📦", groups: byProject.get(p.id) });
      }
      if (byProject.has("")) buckets.push({ id: "ungrouped", name: "Not assigned", icon: "🗂", groups: byProject.get("") });

      if (buckets.length === 1 && buckets[0].id === "ungrouped") {
        rows.innerHTML = deployTable(buckets[0].groups);
      } else {
        rows.innerHTML = buckets.map((b) => {
          const sec = "dep-" + b.id;
          const open = sectionState(sec);
          return `<div style="margin-bottom:14px">
            <h3 style="margin:0 0 8px; font-size:15px">
              <button class="section-toggle" aria-expanded="${open}" data-section="${esc(sec)}">
                <span class="mi-icon">${b.icon}</span>${esc(b.name)}
                <span class="muted" style="font-weight:normal">· ${b.groups.length}</span>
              </button>
            </h3>
            <div class="section-body" id="sec-${esc(sec)}"${open ? "" : " hidden"}>${deployTable(b.groups)}</div>
          </div>`;
        }).join("");
        for (const t of rows.querySelectorAll(".section-toggle"))
          t.addEventListener("click", () => toggleSection(t.dataset.section, t));
      }
      for (const b of rows.querySelectorAll("button[data-del]")) {
        b.addEventListener("click", () => deleteProcess(b.dataset.del, groups, render));
      }
      for (const b of rows.querySelectorAll("button[data-toggle]")) {
        b.addEventListener("click", () =>
          toggleProcessActive(Number(b.dataset.toggle), b.dataset.active === "1", render));
      }
    } catch (e) {
      rows.innerHTML = `<p class="empty" style="padding:14px">${esc(e.message)}</p>`;
    }
  };
  await Promise.all([renderProjects(), render()]);
}

// viewProjectDetail is one project's workspace: a single unified table of its
// artifacts (BPMN drafts, DMN references, forms) with a "Create new" dropdown and
// per-row action menus, plus Deploy and project-level actions. id === "ungrouped"
// shows artifacts that belong to no project (read-only container: no deploy or
// project actions). This is the tidy, Camunda-style per-project view (ADR-0034).
async function viewProjectDetail(id) {
  const ungrouped = id === "ungrouped";
  view.innerHTML = `<div id="pd"><p class="muted">Loading…</p></div>`;
  const root = document.getElementById("pd");

  const render = async () => {
    let projects = [], drafts = [], refs = [], forms = [];
    try {
      [projects, drafts, refs, forms] = await Promise.all([
        api("GET", "/api/v1/applications"),
        api("GET", "/api/v1/drafts"),
        api("GET", "/api/v1/dmnrefs"),
        api("GET", "/api/v1/forms"),
      ]);
    } catch (e) { root.innerHTML = `<div class="card empty">${esc(e.message)}</div>`; return; }

    const known = new Set(projects.map((p) => p.id));
    const proj = ungrouped ? { id: "ungrouped", name: "Not assigned" } : projects.find((p) => p.id === id);
    if (!proj) {
      root.innerHTML = `<div class="card empty">This application no longer exists. <a href="#/modeler">Back to Modeler</a></div>`;
      return;
    }
    setTitle(`${proj.name || "Application"} · Modeler`);
    const mine = (a) => ungrouped ? (!a.projectId || !known.has(a.projectId)) : a.projectId === id;
    const dl = drafts.filter(mine), rl = refs.filter(mine), fl = forms.filter(mine);

    // Scope gating (ADR-0071). Ungrouped is the un-scoped personal/legacy bucket,
    // so it stays fully writable; a real project's actions follow the caller's
    // role. With auth off the server reports "owner", so everything shows as before.
    const isOwner = !ungrouped && proj.myRole === "owner";
    const canWrite = ungrouped || roleRank(proj.myRole) >= 2;

    // "Move to" items for a row's action menu: Ungrouped plus every project, with
    // the current one marked. Forms have no move endpoint, so only drafts/refs get it.
    const moveItems = (currentPid, act, key) => [{
      label: "Move to", icon: "&#128450;",
      items: [
        { label: "Not assigned", icon: currentPid ? "" : "•", act, data: { pid: "", key } },
        ...projects.map((p) => ({ label: p.name, icon: p.id === currentPid ? "•" : "", act, data: { pid: p.id, key } })),
      ],
    }];

    const nameCell = (chip, title, sub, href) => {
      const link = href ? `<a href="${href}"><b>${esc(title)}</b></a>` : `<b>${esc(title)}</b>`;
      return `<td><div class="artifact-name"><span class="chip">${chip}</span>${link}</div>` +
        `<div class="muted" style="font-size:12px; padding-left:26px">${sub}</div></td>`;
    };

    const draftRow = (d) => {
      const href = `#/modeler/draft/${encodeURIComponent(d.processId)}`;
      const items = [{ label: "Open", icon: "→", href }];
      if (canWrite) items.push(
        ...moveItems(d.projectId, "movedraft", d.processId),
        { sep: true },
        { label: "Delete", icon: "🗑", act: "deldraft", data: { key: d.processId }, danger: true },
      );
      return `<tr data-name="${esc((d.name || d.processId).toLowerCase())}">
        ${nameCell("BPMN", d.name || d.processId, esc(d.processId), href)}
        <td class="muted">Diagram</td>
        <td class="muted" data-sort="${d.savedAt || 0}">${esc(fmtTime(d.savedAt))}</td>
        <td class="row-actions">${dropdown("⋯", "icon-btn", items)}</td></tr>`;
    };
    const refRow = (r) => {
      const href = `#/modeler/dmn/${encodeURIComponent(r.id)}`;
      const items = [{ label: "View", icon: "▦", href }, { label: "Validate", icon: "✔", act: "valref", data: { id: r.id } }];
      if (canWrite) items.unshift(
        { label: "Bearbeiten", icon: "✎", act: "editref", data: { id: r.id, ref: r.modelRef, pid: r.projectId || "", name: r.name } });
      if (canWrite) items.push(
        ...moveItems(r.projectId, "moveref", r.id),
        { sep: true },
        { label: "Delete", icon: "🗑", act: "delref", data: { id: r.id }, danger: true },
      );
      return `<tr data-name="${esc(r.name.toLowerCase())}">
        ${nameCell("DMN", r.name, `temis model: ${esc(r.modelRef)} · <span data-refstatus="${esc(r.id)}">not validated</span>`, href)}
        <td class="muted">Decision ref</td>
        <td class="muted" data-sort="${r.createdAt || 0}">${esc(fmtTime(r.createdAt))}</td>
        <td class="row-actions">${dropdown("⋯", "icon-btn", items)}</td></tr>`;
    };
    const formRow = (f) => {
      const href = `#/modeler/form/e/${encodeURIComponent(f.id)}`;
      const items = [{ label: "Open", icon: "→", href }];
      if (canWrite) items.push(
        { sep: true },
        { label: "Delete", icon: "🗑", act: "delform", data: { id: f.id }, danger: true },
      );
      return `<tr data-name="${esc((f.name || f.id).toLowerCase())}">
        ${nameCell("FORM", f.name || f.id, esc(f.id), href)}
        <td class="muted">Form</td>
        <td class="muted" data-sort="${f.savedAt || 0}">${esc(fmtTime(f.savedAt))}</td>
        <td class="row-actions">${dropdown("⋯", "icon-btn", items)}</td></tr>`;
    };

    const bodyRows = dl.map(draftRow).join("") + rl.map(refRow).join("") + fl.map(formRow).join("");
    const newDiagramHref = ungrouped ? "#/modeler/new" : `#/modeler/new/p/${encodeURIComponent(id)}`;
    const newFormHref = ungrouped ? "#/modeler/form/new" : `#/modeler/form/new/p/${encodeURIComponent(id)}`;
    const createItems = [
      { header: "Blank resources" },
      { label: "BPMN diagram", icon: "⚙", href: newDiagramHref },
      { label: "DMN model (upload .dmn)", icon: "▦", act: "newref" },
      { label: "Form", icon: "▤", href: newFormHref },
      { sep: true },
      { label: "Import file…", icon: "📥", act: "import" },
      { label: "Import MIM workflow (XOML)…", icon: "🔁", act: "import-mim" },
    ];

    const projItems = ungrouped ? [] : [
      { label: "Download source…", icon: "🗂", act: "srcexport" },
      ...(rl.length ? [{ label: "Validate DMN", icon: "✔", act: "valproj" }] : []),
      ...(AUTH.enabled && isOwner ? [{ label: "Share…", icon: "👤", act: "shareproj" }] : []),
      ...(isOwner ? [{ label: "Rename application", icon: "✎", act: "renproj" }] : []),
      ...(isOwner ? [{ sep: true }, { label: "Delete application", icon: "🗑", act: "delproj", danger: true }] : []),
    ];
    root.innerHTML = `
      <div class="crumb"><a href="#/modeler">Home</a> › ${esc(proj.name)}</div>
      <div class="between">
        <h1>${esc(proj.name)}${ungrouped ? "" : " " + visBadge(proj)}</h1>
        <div class="row">
          ${(!ungrouped && canWrite) ? `<button class="btn" id="pd-deploy" title="Publish this application as a new release and deploy it">Publish</button>` : ""}
          ${canWrite ? dropdown("Create new", "btn neutral", createItems) : ""}
          ${projItems.length ? dropdown("⋯", "icon-btn", projItems) : ""}
        </div>
      </div>
      ${ungrouped ? "" : `<div class="tabs" id="pd-tabs">
        <button data-pane="artifacts" class="active" title="Show this application’s design-time artifacts">Artifacts</button>
        <button data-pane="deployments" title="Show what this application currently has deployed">Deployments</button>
      </div>`}
      <div id="pane-artifacts">
        <input class="filter-input" id="pd-filter" placeholder="Filter artifacts…" autocomplete="off">
        <div class="card" style="padding:0">
          <table data-dt-key="project-artifacts">
            <thead><tr><th>Name</th><th>Type</th><th>Last changed</th><th></th></tr></thead>
            <tbody id="pd-rows">${bodyRows ||
              `<tr><td colspan="4" class="empty">${canWrite
                ? "No artifacts yet — use <b>Create new</b> to add one."
                : "No artifacts in this application yet."}</td></tr>`}</tbody>
          </table>
        </div>
      </div>
      ${ungrouped ? "" : `<div id="pane-deployments" hidden>
        <p class="muted" style="padding:2px 2px 12px">What this application currently has deployed on this server.</p>
        <div id="pd-deployments"><p class="muted" style="padding:14px 2px">Loading…</p></div>
      </div>`}`;

    const filter = document.getElementById("pd-filter");
    filter.addEventListener("input", () => {
      const q = filter.value.trim().toLowerCase();
      for (const tr of root.querySelectorAll("#pd-rows tr[data-name]"))
        tr.hidden = q !== "" && !tr.dataset.name.includes(q);
    });

    // Tabs: Artifacts (design-time) and Deployments (what's live for this
    // application, ADR-0128). The deployments pane loads lazily on first open.
    const tabs = document.getElementById("pd-tabs");
    if (tabs) {
      let loadedDeployments = false;
      tabs.addEventListener("click", (e) => {
        const b = e.target.closest("button[data-pane]");
        if (!b) return;
        for (const t of tabs.querySelectorAll("button")) t.classList.toggle("active", t === b);
        document.getElementById("pane-artifacts").hidden = b.dataset.pane !== "artifacts";
        document.getElementById("pane-deployments").hidden = b.dataset.pane !== "deployments";
        if (b.dataset.pane === "deployments" && !loadedDeployments) {
          loadedDeployments = true;
          renderAppDeployments(id);
        }
      });
    }

    onMenuAction(root, (act, b) => {
      switch (act) {
        case "import": importArtifact(ungrouped ? "" : id, render); break;
        case "import-mim": importMIM(ungrouped ? "" : id, render); break;
        case "srcexport": downloadApplicationSource(id); break;
        case "newref": createDmnRef(ungrouped ? "" : id, render); break;
        case "shareproj": shareProject(proj, render); break;
        case "renproj": renameProject(id, proj.name, render); break;
        case "delproj": deleteProject(id, proj.name, () => { location.hash = "#/modeler"; }); break;
        case "valproj": validateProject(id); break;
        case "valref": validateDmnRef(b.dataset.id); break;
        case "editref": editDmnRef({ id: b.dataset.id, modelRef: b.dataset.ref, projectId: b.dataset.pid, name: b.dataset.name }, render); break;
        case "deldraft": deleteDraft(b.dataset.key, render); break;
        case "delref": deleteDmnRef(b.dataset.id, render); break;
        case "delform": deleteForm(b.dataset.id, render); break;
        case "movedraft": moveDraft(b.dataset.key, b.dataset.pid, render); break;
        case "moveref": moveDmnRef(b.dataset.key, b.dataset.pid, render); break;
      }
    });
    const deployBtn = document.getElementById("pd-deploy");
    if (deployBtn) deployBtn.addEventListener("click", () => publishApplication(id, render));
  };
  await render();
}

// renderAppDeployments fills the Deployments tab: the application's published
// version, its live definitions with per-definition instance counts, and its
// release history (ADR-0128). One call each, both scoped to this application.
async function renderAppDeployments(id) {
  const host = document.getElementById("pd-deployments");
  if (!host) return;
  let view, releases;
  try {
    [view, releases] = await Promise.all([
      api("GET", `/api/v1/applications/${encodeURIComponent(id)}/deployments`),
      api("GET", `/api/v1/applications/${encodeURIComponent(id)}/releases`),
    ]);
  } catch (e) { host.innerHTML = `<div class="card empty">${esc(e.message)}</div>`; return; }

  // Two independent facts, shown as two pills rather than one merged "state":
  // whether this is the version that still starts instances (current vs
  // superseded — deploying a new version retires the previous one's message,
  // signal, and timer starts), and whether an operator paused it (ADR-0119).
  // A superseded version keeps its running instances and can still be targeted
  // deliberately, e.g. by a pinned call activity (ADR-0105).
  const stateCell = (d) => {
    const life = d.current
      ? `<span class="pill ok" title="Newest version — starts new instances">current</span>`
      : `<span class="pill" title="A newer version has taken over; running instances continue, this one no longer starts any by itself">superseded</span>`;
    const paused = d.active ? "" :
      ` <span class="pill warn" title="An operator paused this definition (ADR-0119)">paused</span>`;
    return `<td>${life}${paused}</td>`;
  };

  const defRow = (d) => `<tr${d.current ? "" : ' class="muted-row"'}>
    <td><div class="artifact-name"><span class="mi-icon">⚙</span><a href="#/modeler/d/${encodeURIComponent(d.key)}"><b>${esc(d.name || d.processId)}</b></a></div>
      <div class="muted" style="font-size:12px; padding-left:26px">${esc(d.processId)}</div></td>
    <td><span class="chip">v${d.version}</span></td>
    ${stateCell(d)}
    <td class="muted">${d.running}</td>
    <td class="muted">${d.finished}</td>
    <td class="muted" data-sort="${d.deployedAt || 0}">${esc(fmtTime(d.deployedAt))}</td>
  </tr>`;

  // Current versions first, then each process's superseded versions newest-first,
  // so the versions that actually run head the table instead of being buried.
  const orderedDefs = [...view.definitions].sort((a, b) =>
    (b.current - a.current) ||
    a.processId.localeCompare(b.processId) ||
    (b.version - a.version));

  // Deployment targets (ADR-0129): what each peer server runs for this
  // application. Fetched separately from the local view because it makes an
  // outbound call per bound target — a peer being slow or down must not delay or
  // empty the rest of the page, so a failure here renders as an unreachable row.
  let targets = [];
  try {
    targets = await api("GET", `/api/v1/applications/${encodeURIComponent(id)}/targets`);
  } catch { /* best-effort: the local view still renders without the target section */ }

  const targetRow = (t) => {
    // Three distinct states, because they mean different things operationally:
    // never shipped there, shipped and answering, shipped and not answering.
    let state, detail;
    if (!t.bound) {
      state = `<span class="pill">not deployed</span>`;
      detail = `<span class="muted">—</span>`;
    } else if (t.reachable) {
      state = `<span class="pill ok">live</span>`;
      detail = `<span class="chip">v${t.version || "?"}</span>`;
    } else {
      state = `<span class="pill err">unreachable</span>`;
      detail = `<span class="muted" title="${esc(t.error || "")}">${esc(t.error || "no answer")}</span>`;
    }
    return `<tr${t.bound ? "" : ' class="muted-row"'}>
      <td><div class="artifact-name"><span class="mi-icon">🛰</span><b>${esc(t.targetName)}</b></div>
        <div class="muted" style="font-size:12px; padding-left:26px">${esc(t.baseUrl)}</div></td>
      <td>${state}</td>
      <td>${detail}</td>
      <td class="muted">${t.reachable ? t.running : "—"}</td>
      <td class="muted">${t.reachable ? t.finished : "—"}</td>
    </tr>`;
  };

  const targetsSection = targets.length ? `
    <h2 style="margin:22px 0 10px; font-size:15px">Deployment targets</h2>
    <div class="card" style="padding:0">
      <table>
        <thead><tr><th>Target</th><th>State</th><th>Version</th><th>Running</th><th>Finished</th></tr></thead>
        <tbody>${targets.map(targetRow).join("")}</tbody>
      </table>
    </div>` : "";

  const relRow = (r) => `<tr>
    <td><span class="chip">v${r.version}</span></td>
    <td class="muted" data-sort="${r.publishedAt || 0}">${esc(fmtTime(r.publishedAt))}</td>
    <td class="muted">${(r.members || []).length}</td>
    <td class="muted">${esc(r.note || "—")}</td>
    <td class="row-actions">${targets.length
      ? `<button class="btn ghost sm" data-promote="${r.version}" title="Ship this release to a deployment target">Promote…</button>`
      : ""}</td>
  </tr>`;

  host.innerHTML = `
    <div class="card" style="margin-bottom:16px">
      <div class="stats">
        <div class="stat"><b>${view.version ? "v" + view.version : "—"}</b><span>Published version</span></div>
        <div class="stat"><b>${view.processes}</b><span>Process${view.processes === 1 ? "" : "es"}</span></div>
        <div class="stat"><b>${view.running}</b><span>Running instances</span></div>
        <div class="stat"><b>${view.finished}</b><span>Finished instances</span></div>
      </div>
    </div>
    <div class="card" style="padding:0">
      <table>
        <thead><tr><th>Definition</th><th>Version</th><th>State</th><th>Running</th><th>Finished</th><th>Deployed</th></tr></thead>
        <tbody>${orderedDefs.map(defRow).join("") ||
          `<tr><td colspan="6" class="empty">Nothing deployed yet — use <b>Publish</b> to ship this application.</td></tr>`}</tbody>
      </table>
    </div>
    ${targetsSection}
    <h2 style="margin:22px 0 10px; font-size:15px">Release history</h2>
    <div class="card" style="padding:0" id="pd-releases">
      <table>
        <thead><tr><th>Version</th><th>Published</th><th>Artifacts</th><th>Note</th><th></th></tr></thead>
        <tbody>${releases.map(relRow).join("") ||
          `<tr><td colspan="5" class="empty">No releases yet.</td></tr>`}</tbody>
      </table>
    </div>`;

  for (const b of host.querySelectorAll("button[data-promote]"))
    b.addEventListener("click", () => promoteRelease(id, Number(b.dataset.promote), targets));
}

// promoteRelease ships an existing release to a chosen target (ADR-0129). The
// release is already frozen, so this sends exactly what was published — the user
// picks *where*, never *what*. Results come back per target, so a refusal by one
// peer is reported as that peer's, not as a failed action.
async function promoteRelease(appID, version, targets) {
  const names = targets.map((t, i) => `${i + 1}) ${t.targetName}`).join("\n");
  const answer = window.prompt(
    `Promote v${version} to which target?\n\n${names}\n\nEnter a number:`, "1");
  if (answer == null) return;
  const pick = targets[Number(answer) - 1];
  if (!pick) { toast("No such target", "err"); return; }

  try {
    const res = await api("POST",
      `/api/v1/applications/${encodeURIComponent(appID)}/releases/${version}/promote`,
      { targetIds: [pick.targetId] });
    const r = (res.results || [])[0];
    if (r && r.ok) toast(`Promoted v${version} to ${pick.targetName}`, "ok");
    else toast(`${pick.targetName}: ${(r && r.error) || "promotion failed"}`, "err");
  } catch (e) {
    toast("promotion failed: " + e.message, "err");
  }
  await renderAppDeployments(appID);
}

async function deleteDraft(processId, reload) {
  if (!window.confirm(`Delete draft "${processId}"?`)) return;
  try {
    await api("DELETE", `/api/v1/drafts/${encodeURIComponent(processId)}`);
    toast(`Deleted draft "${processId}"`, "ok");
  } catch (e) {
    toast("could not delete draft: " + e.message, "err");
  }
  await reload();
}

async function deleteProcess(processId, groups, reload) {
  const group = groups.find((g) => g.processId === processId);
  if (!group) return;
  const n = group.versions.length;
  if (!window.confirm(`Delete process "${processId}"${n > 1 ? ` and all ${n} versions` : ""}?`)) return;
  let failed = 0;
  for (const v of group.versions) {
    try { await api("DELETE", `/api/v1/processes/${v.key}`); }
    catch (e) { failed++; }
  }
  if (failed) toast(`Could not delete ${failed} version(s) — running instances?`, "err");
  else toast(`Deleted "${processId}"`, "ok");
  await reload();
}

// toggleProcessActive activates or deactivates a deployed definition (ADR-0119). A
// deactivated process stays deployed and keeps its running instances, but no longer
// auto-starts new ones from its timer/message/signal start events. `key` is the latest
// version's definition key; `inactive` is its current state (true → the click activates).
async function toggleProcessActive(key, inactive, reload) {
  const activate = inactive; // clicking "Activate" on an inactive one activates it
  try {
    await api("PUT", `/api/v1/processes/${key}/active`, { active: activate });
    toast(activate ? "Process activated" : "Process deactivated — automatic starts paused", "ok");
  } catch (e) {
    toast("could not change activation: " + e.message, "err");
  }
  await reload();
}

// ---------- Applications (ADR-0034 project, reframed by ADR-0128) ----------
// These call the canonical /api/v1/applications endpoints. The pre-rename
// project paths still work as deprecated aliases for external callers, but the
// UI is on the new names. The artifact tag stays `projectId` — ADR-0128 renames
// the API/UI boundary only, not the on-disk shape.
async function createProject(reload) {
  const name = window.prompt("Application name");
  if (name == null) return; // cancelled
  const trimmed = name.trim();
  if (!trimmed) { toast("Application name is required", "err"); return; }
  try {
    await api("POST", "/api/v1/applications", { name: trimmed });
    toast(`Created application "${trimmed}"`, "ok");
  } catch (e) { toast("could not create application: " + e.message, "err"); }
  await reload();
}

async function renameProject(id, current, reload) {
  const name = window.prompt("Rename application", current);
  if (name == null) return;
  const trimmed = name.trim();
  if (!trimmed) { toast("Application name is required", "err"); return; }
  try {
    await api("PATCH", `/api/v1/applications/${encodeURIComponent(id)}`, { name: trimmed });
    toast("Renamed application", "ok");
  } catch (e) { toast("could not rename application: " + e.message, "err"); }
  await reload();
}

async function deleteProject(id, name, reload) {
  if (!window.confirm(`Delete application "${name}"? BPMN, DMN, and form artifacts are kept and become Not assigned. Panorama models must be deleted first.`)) return;
  try {
    await api("DELETE", `/api/v1/applications/${encodeURIComponent(id)}`);
    toast(`Deleted application "${name}"`, "ok");
  } catch (e) { toast("could not delete application: " + e.message, "err"); }
  await reload();
}

// ---------- Sharing scopes (ADR-0071) ----------
// A sharing scope is a design-time access boundary on a project: private (owner
// only) or shared with named members as viewer/editor. It is only meaningful when
// auth is enforced; in single-user mode the controls are hidden and the server
// reports the caller as owner of every project, so the classic open flow is
// unchanged. Runtime/Operations isolation is out of scope for this slice.
const roleRank = (r) => ({ viewer: 1, editor: 2, owner: 3 }[r] || 0);

// visBadge renders a project's private/shared pill, or "" when auth is off (there
// is nothing to share in single-user mode).
function visBadge(p) {
  if (!AUTH.enabled) return "";
  const n = (p.members || []).length;
  if (p.visibility === "shared")
    return `<span class="pill vis shared" title="Shared with ${n} ${n === 1 ? "person" : "people"}">Shared${n ? " · " + n : ""}</span>`;
  return `<span class="pill vis" title="Only the owner can see this project">Private</span>`;
}

// shareProject loads the principals directory (to resolve member names and
// populate the add-picker) and opens the share dialog. The directory is readable
// by any authenticated caller (ADR-0073), so an owner needn't be an admin; if the
// fetch fails for any reason the dialog degrades to adding members by id.
async function shareProject(proj, reload) {
  let users = null, degraded = false;
  try { users = await api("GET", "/api/v1/principals"); }
  catch { degraded = true; }
  openShareModal(proj, users, degraded, reload);
}

// openShareModal renders the share dialog and wires its controls to the project
// scope endpoints. It keeps a local copy of the project, updated from each
// mutation's response so the dialog reflects server truth without a full refetch;
// closing runs reload() once to refresh the underlying page (badges and gating).
// confirmTerminateAll gates a bulk terminate behind a modal whose friction scales
// with the blast radius: a plain confirm for a small count, and a type-the-count gate
// above 50 so draining a flooded process can't be a single click. Resolves true only
// when confirmed (and, when gated, the exact count was typed).
function confirmTerminateAll(name, count) {
  const TYPE_THRESHOLD = 50;
  return new Promise((resolve) => {
    const gated = count > TYPE_THRESHOLD;
    const ov = document.createElement("div");
    ov.className = "modal-ov";
    ov.innerHTML = `
      <div class="modal confirm-modal" role="dialog" aria-modal="true" aria-label="Confirm termination">
        <div class="modal-head"><h2>Terminate ${count} running instance${count === 1 ? "" : "s"}?</h2></div>
        <div class="modal-body">
          <p class="muted" style="margin:0 0 10px">This discards each token and moves every running instance of <b>${esc(name)}</b> (across all its versions) to the finished list as <b>terminated</b>. This can't be undone.</p>
          ${gated ? `<label class="field"><span>Type <b>${count}</b> to confirm</span>
            <input id="term-all-input" type="text" inputmode="numeric" autocomplete="off" spellcheck="false" placeholder="${count}"/></label>` : ""}
        </div>
        <div class="modal-foot">
          <button class="btn neutral" data-cancel title="Cancel without terminating any instances">Cancel</button>
          <button class="btn danger" data-confirm ${gated ? "disabled" : ""} title="Terminate every running instance of this process">Terminate ${count}</button>
        </div>
      </div>`;
    document.body.appendChild(ov);
    const input = ov.querySelector("#term-all-input");
    const confirmBtn = ov.querySelector("[data-confirm]");
    const close = (ok) => { ov.remove(); document.removeEventListener("keydown", onKey); resolve(ok); };
    const onKey = (e) => { if (e.key === "Escape") close(false); };
    document.addEventListener("keydown", onKey);
    if (input) {
      input.addEventListener("input", () => { confirmBtn.disabled = input.value.trim() !== String(count); });
      input.focus();
    } else {
      confirmBtn.focus();
    }
    ov.querySelector("[data-cancel]").addEventListener("click", () => close(false));
    confirmBtn.addEventListener("click", () => { if (!confirmBtn.disabled) close(true); });
    ov.addEventListener("click", (e) => { if (e.target === ov) close(false); });
  });
}

function openShareModal(proj, users, degraded, reload) {
  let p = proj;
  const byId = new Map((users || []).map((u) => [u.id, u]));
  const nameOf = (id) => { const u = byId.get(id); return u ? u.name : id; };
  const me = AUTH.user && AUTH.user.id;

  const ov = document.createElement("div");
  ov.className = "modal-ov";
  ov.innerHTML = `
    <div class="modal share-modal" role="dialog" aria-modal="true" aria-label="Share project">
      <div class="modal-head">
        <h2>Share “${esc(p.name)}”</h2>
        <button type="button" class="icon-btn" data-x aria-label="Close" title="Close">✕</button>
      </div>
      <div class="modal-body" id="share-body"></div>
      <div class="modal-foot">
        <span class="muted small">Sharing controls who can view and edit this project’s diagrams. Running instances are not affected.</span>
        <button type="button" class="btn" data-done title="Close this dialog">Done</button>
      </div>
    </div>`;
  document.body.appendChild(ov);
  const body = ov.querySelector("#share-body");

  const close = () => { ov.remove(); document.removeEventListener("keydown", onKey); reload(); };
  const onKey = (e) => { if (e.key === "Escape") close(); };
  document.addEventListener("keydown", onKey);
  ov.addEventListener("mousedown", (e) => { if (e.target === ov) close(); });
  ov.querySelector("[data-x]").addEventListener("click", close);
  ov.querySelector("[data-done]").addEventListener("click", close);

  const roleSelect = (value, attrs) =>
    `<select ${attrs} class="role-sel">
       <option value="viewer"${value === "viewer" ? " selected" : ""}>Viewer</option>
       <option value="editor"${value === "editor" ? " selected" : ""}>Editor</option>
     </select>`;

  const renderBody = () => {
    const members = p.members || [];
    const memberIds = new Set(members.map((m) => m.ref.id));
    const eligible = (users || []).filter((u) => u.id !== p.ownerId && !memberIds.has(u.id));

    const personRow = (id, right, extra = "", isGroup = false) => `
      <div class="member-row"${extra}>
        <span class="avatar sm">${esc(initials(nameOf(id)))}</span>
        <div class="member-id">${esc(nameOf(id))}${isGroup ? ' <span class="muted">· group</span>' : (id === me ? ' <span class="muted">(you)</span>' : "")}</div>
        ${right}
      </div>`;

    const ownerRow = personRow(p.ownerId, `<span class="muted">Owner</span>`);
    const memberRows = members.map((m) => personRow(
      m.ref.id,
      `${roleSelect(m.role, `data-role-for="${esc(m.ref.id)}"`)}
       <button type="button" class="icon-btn danger" data-remove="${esc(m.ref.id)}" title="Remove access">✕</button>`,
      ` data-uid="${esc(m.ref.id)}"`,
      m.ref.type === "group",
    )).join("");

    const addControl = degraded
      ? `<div class="add-row">
           <input class="field" id="add-uid" placeholder="User ID (usr_…)" autocomplete="off">
           ${roleSelect("viewer", 'id="add-role"')}
           <button type="button" class="btn" id="add-btn" title="Give this person access">Add</button>
         </div>
         <p class="muted small">Could not load the directory — add by user ID for now.</p>`
      : eligible.length
        ? `<div class="add-row">
             <select class="field" id="add-uid">
               ${eligible.map((u) => `<option value="${esc(u.id)}">${esc(u.name)}${u.type === "group" ? " (group)" : ""}</option>`).join("")}
             </select>
             ${roleSelect("viewer", 'id="add-role"')}
             <button type="button" class="btn" id="add-btn" title="Give this person or group access">Add</button>
           </div>`
        : `<p class="muted small">Everyone already has access.</p>`;

    const privateHint = p.visibility !== "shared" && members.length
      ? `<p class="muted small">Members are kept but have no access while the project is private.</p>`
      : "";

    // Ownership can be handed to any other user (never a group — ownership is a
    // single principal). Only the owner (admin included, who reports as owner) may
    // transfer, and only when the directory loaded so targets carry real names.
    const xferTargets = (users || []).filter((u) => u.type !== "group" && u.id !== p.ownerId);
    const canTransfer = p.myRole === "owner" && !degraded && xferTargets.length > 0;
    const transferSec = canTransfer
      ? `<div class="share-sec">
           <div class="mlabel">Transfer ownership</div>
           <div class="add-row">
             <select class="field" id="xfer-uid">
               ${xferTargets.map((u) => `<option value="${esc(u.id)}">${esc(u.name)}</option>`).join("")}
             </select>
             <button type="button" class="btn danger" id="xfer-btn" title="Hand this application over to another person">Transfer…</button>
           </div>
           <p class="muted small">The new owner gains full control.${me === p.ownerId ? " You will lose owner access unless they share it back." : ""}</p>
         </div>`
      : "";

    body.innerHTML = `
      <div class="share-sec">
        <div class="seg" role="group" aria-label="Visibility">
          <button type="button" data-vis="private" class="${p.visibility !== "shared" ? "active" : ""}" title="Keep this project private to its owner and members">Private</button>
          <button type="button" data-vis="shared" class="${p.visibility === "shared" ? "active" : ""}" title="Make this project visible to the people you add below">Shared</button>
        </div>
        ${privateHint}
      </div>
      <div class="share-sec">
        <div class="mlabel">People with access</div>
        ${ownerRow}${memberRows}
      </div>
      <div class="share-sec">
        <div class="mlabel">Add people</div>
        ${addControl}
      </div>
      ${transferSec}
      ${p.myRole === "owner" ? `<div class="share-sec" id="share-activity"></div>` : ""}`;
    wire();
    refreshActivity();
  };

  // The Activity section is the grant audit log (ADR-0184): who
  // changed access to this project and when. It is owner-only on the server, so it is
  // fetched and shown only for an owner, and re-fetched after every mutation below so
  // a share/revoke/visibility change appears without reopening the dialog.
  const auditLine = (e) => {
    const who = `<b>${esc(e.actorName || e.actorId || "someone")}</b>`;
    const subj = () => esc(nameOf(e.subjectId));
    switch (e.action) {
      case "share": return `${who} shared with ${subj()} as ${esc(e.role)}`;
      case "unshare": return `${who} removed ${subj()}`;
      case "visibility": return `${who} set visibility to ${esc(e.to)}`;
      case "transfer": return `${who} transferred ownership to ${esc(nameOf(e.to))}`;
      default: return `${who} changed access`;
    }
  };
  const refreshActivity = async () => {
    const host = body.querySelector("#share-activity");
    if (!host) return;
    let events;
    try { events = await api("GET", `/api/v1/applications/${encodeURIComponent(p.id)}/audit`); }
    catch { host.innerHTML = ""; return; }
    if (!events || !events.length) {
      host.innerHTML = `<div class="mlabel">Activity</div><p class="muted small">No access changes recorded yet.</p>`;
      return;
    }
    const rows = events.slice(0, 10).map((e) =>
      `<div class="audit-row"><span>${auditLine(e)}</span><span class="muted small">${esc(fmtTime(e.at))}</span></div>`).join("");
    const more = events.length > 10 ? `<p class="muted small">Showing the 10 most recent of ${events.length} changes.</p>` : "";
    host.innerHTML = `<div class="mlabel">Activity</div>${rows}${more}`;
  };

  const apply = async (fn) => { try { p = await fn(); renderBody(); } catch (e) { toast(e.message, "err"); } };
  const setVisibility = (v) => apply(() =>
    api("PATCH", `/api/v1/applications/${encodeURIComponent(p.id)}`, { visibility: v }));
  // A member ref is a user by default, or a group; resolve the type from the
  // existing member (role change) or the directory entry (fresh add), so the PUT
  // records the right kind (ADR-0180).
  const memberType = (id) => {
    const m = (p.members || []).find((x) => x.ref.id === id);
    if (m) return m.ref.type;
    const e = byId.get(id);
    return e ? e.type : "user";
  };
  const setMember = (uid, role) => uid && apply(() =>
    api("PUT", `/api/v1/applications/${encodeURIComponent(p.id)}/members/${encodeURIComponent(uid)}`, { role, type: memberType(uid) }));
  const removeMember = (uid) => apply(() =>
    api("DELETE", `/api/v1/applications/${encodeURIComponent(p.id)}/members/${encodeURIComponent(uid)}`));
  // transferOwnership hands the project to another user (ADR-0071). It is a clean
  // handoff — the previous owner keeps no access — and mostly irreversible for the
  // current owner, so it is gated behind a confirm. On success the dialog closes:
  // the caller is no longer the owner (unless admin) and the page re-gates on reload.
  const transferOwnership = async (uid) => {
    if (!uid) return;
    const name = nameOf(uid);
    const iAmOwner = me === p.ownerId;
    const msg = `Transfer ownership of “${p.name}” to ${name}?\n\n` +
      (iAmOwner
        ? "You will no longer be the owner and lose access unless the new owner shares it back."
        : "This replaces the current owner.");
    if (!window.confirm(msg)) return;
    try {
      await api("PATCH", `/api/v1/applications/${encodeURIComponent(p.id)}`, { ownerId: uid });
      toast(`Ownership transferred to ${name}`, "ok");
      close();
    } catch (e) { toast(e.message, "err"); }
  };

  function wire() {
    for (const b of body.querySelectorAll("[data-vis]"))
      b.addEventListener("click", () => { if (b.dataset.vis !== (p.visibility === "shared" ? "shared" : "private")) setVisibility(b.dataset.vis); });
    for (const s of body.querySelectorAll("[data-role-for]"))
      s.addEventListener("change", () => setMember(s.dataset.roleFor, s.value));
    for (const b of body.querySelectorAll("[data-remove]"))
      b.addEventListener("click", () => removeMember(b.dataset.remove));
    const addBtn = body.querySelector("#add-btn");
    if (addBtn) addBtn.addEventListener("click", () => {
      const uid = (body.querySelector("#add-uid").value || "").trim();
      setMember(uid, body.querySelector("#add-role").value);
    });
    const xferBtn = body.querySelector("#xfer-btn");
    if (xferBtn) xferBtn.addEventListener("click", () =>
      transferOwnership((body.querySelector("#xfer-uid").value || "").trim()));
  }

  renderBody();
}

// moveDraft reassigns a draft to a project (or to Ungrouped when projectId is "").
async function moveDraft(processId, projectId, reload) {
  try {
    await api("PATCH", `/api/v1/drafts/${encodeURIComponent(processId)}`, { projectId });
  } catch (e) { toast("could not move draft: " + e.message, "err"); }
  await reload();
}

// pickFile opens the OS file dialog and resolves with the chosen File, or null if
// the dialog is cancelled. There is no reliable cross-browser "cancel" event, so a
// window-focus fallback resolves null shortly after the dialog closes with no
// selection.
function pickFile(accept) {
  return new Promise((resolve) => {
    const inp = document.createElement("input");
    inp.type = "file";
    if (accept) inp.accept = accept;
    inp.style.display = "none";
    let done = false;
    const finish = (f) => {
      if (done) return;
      done = true;
      window.removeEventListener("focus", onFocus, true);
      inp.remove();
      resolve(f);
    };
    const onFocus = () => setTimeout(() => finish(inp.files && inp.files[0] ? inp.files[0] : null), 300);
    inp.addEventListener("change", () => finish(inp.files && inp.files[0] ? inp.files[0] : null), { once: true });
    window.addEventListener("focus", onFocus, true);
    document.body.appendChild(inp);
    inp.click();
  });
}

// createDmnRef adds a DMN model to a project by uploading a .dmn file: the model is
// validated and stored in the local model folder, and a reference to it is created
// (ADR-0034/0050). Authoring still lives in temis — this is just "pick the model
// you exported and use it". When models come from a remote temis service (nothing
// local to upload to), it falls back to referencing the model by its handle there.
async function createDmnRef(projectId, reload) {
  const file = await pickFile(".dmn,.xml,application/xml,text/xml");
  if (!file) return;
  let up;
  try {
    const xml = await file.text();
    up = await api("POST", "/api/v1/dmn-models?name=" + encodeURIComponent(file.name), xml, true);
  } catch (e) {
    const modelRef = window.prompt("Couldn't upload the file (models may be served by a remote temis service).\nReference an existing temis model by name instead:");
    if (!modelRef || !modelRef.trim()) { toast("DMN model not added: " + e.message, "err"); return; }
    const refName = (window.prompt("Reference name (how it shows in Atlas)", modelRef.trim()) || modelRef).trim();
    try {
      await api("POST", "/api/v1/dmnrefs", { name: refName, modelRef: modelRef.trim(), projectId });
      toast(`Added DMN reference "${refName}"`, "ok");
      await reload();
    } catch (e2) { toast("could not add DMN reference: " + e2.message, "err"); }
    return;
  }
  const name = (up.modelName || file.name.replace(/\.(dmn|xml)$/i, "") || "Decision").trim();
  const n = (up.decisions || []).length;
  try {
    await api("POST", "/api/v1/dmnrefs", { name, modelRef: up.modelRef, projectId });
    toast(`Added DMN model "${name}" — ${n} decision${n === 1 ? "" : "s"}`, "ok");
  } catch (e) { toast("could not add DMN reference: " + e.message, "err"); return; }
  await reload();
}

// saveOrConfirmOverwrite runs a save that refuses to land on an id something else
// already holds, and turns that refusal into a question rather than a dead end.
//
// `save(from)` is called twice at most: first with "" — "this is new, so refuse a
// collision" — and, if the author confirms the replacement, with null, which omits the
// parameter entirely and gets the plain overwrite-by-id every non-interactive writer
// uses. Omitting it is what lets the retry work without the caller having to know the
// id inside the file: the server reads it from the document either way.
//
// Importing a file whose id already exists is not by itself a mistake — it is usually a
// corrected export of the same artifact. What it must never be is silent, which is what
// it was: you picked a file and the draft or form you already had was gone
// (ADR-0222).
// Declining the replacement returns null — not an error, since keeping what was there
// is a perfectly good outcome — so the caller reports it as a cancellation rather than
// a failure.
async function saveOrConfirmOverwrite(save, question) {
  try {
    return await save("");
  } catch (e) {
    if (e.status !== 409) throw e;
    if (!window.confirm(`${e.message}\n\n${question}`)) return null;
    return await save(null);
  }
}

// importArtifact imports a BPMN diagram, DMN model, or form from an uploaded file and
// files it into the given project ("" = ungrouped). The kind is detected from the
// extension and, for an ambiguous .xml, from the root element's namespace: a BPMN
// diagram is saved as a draft (the backend derives its process id/name), a DMN model
// is uploaded and referenced (the createDmnRef two-step), and a form-js .form/.json is
// saved as a form. The list is refreshed on success.
async function importArtifact(projectId, reload) {
  const file = await pickFile(".bpmn,.dmn,.form,.xml,.json,application/xml,text/xml,application/json");
  if (!file) return;
  const ext = (file.name.split(".").pop() || "").toLowerCase();
  const base = file.name.replace(/\.[^.]+$/, "");
  let text;
  try { text = await file.text(); } catch (e) { toast("Import failed: " + e.message, "err"); return; }
  try {
    // Form — a form-js JSON schema (.form, or a .json whose object carries components).
    if (ext === "form" || ext === "json") {
      let schema;
      try { schema = JSON.parse(text); } catch { throw new Error("not valid JSON"); }
      if (!schema || typeof schema !== "object" || Array.isArray(schema)) throw new Error("not a form (expected a JSON object)");
      if (!Array.isArray(schema.components)) throw new Error("not a form-js form (no components array)");
      const id = (typeof schema.id === "string" && schema.id.trim()) || ("form-" + Date.now().toString(36) + Math.random().toString(36).slice(2, 6));
      const name = (typeof schema.name === "string" && schema.name.trim()) || base || id;
      // "from" empty says this is a new form, so an id something already holds comes
      // back 409 instead of quietly replacing that form
      // (ADR-0222). Importing a file over a form you already have
      // is a real intent — a corrected export of the same form — so it is offered by
      // name rather than refused; it is just never the default.
      const savedForm = await saveOrConfirmOverwrite(
        (from) => api("POST", "/api/v1/forms",
          from === null ? { id, name, schema, projectId } : { id, name, schema, projectId, from }),
        `A form with the id “${id}” already exists. Replace it with this file?`);
      if (!savedForm) { toast(`Import cancelled — the form “${id}” you already had was kept`); return; }
      toast(`Imported form “${name}”`, "ok");
      await reload();
      return;
    }
    // DMN — an explicit .dmn, or an .xml in the DMN namespace. Upload the model, then
    // reference it (mirrors createDmnRef; the dedicated DMN item keeps the remote-temis
    // fallback for setups with no local model store).
    if (ext === "dmn" || (ext !== "bpmn" && /spec\/DMN\//i.test(text))) {
      const up = await api("POST", "/api/v1/dmn-models?name=" + encodeURIComponent(file.name), text, true);
      const name = (up.modelName || base || "Decision").trim();
      await api("POST", "/api/v1/dmnrefs", { name, modelRef: up.modelRef, projectId });
      const n = (up.decisions || []).length;
      toast(`Imported DMN model “${name}” — ${n} decision${n === 1 ? "" : "s"}`, "ok");
      await reload();
      return;
    }
    // BPMN — save the diagram as a draft; the backend rejects XML with no <process id>.
    // BPMN-DI is optional in the standard, so a file may carry no layout at all; the
    // backend lays one out on the way in and says so, and the author is told rather than
    // left to assume the arrangement in front of them is the one their file described.
    const draftPath = (from) => {
      const q = [];
      if (from !== null) q.push("from=" + encodeURIComponent(from));
      if (projectId) q.push("projectId=" + encodeURIComponent(projectId));
      return "/api/v1/drafts" + (q.length ? "?" + q.join("&") : "");
    };
    const d = await saveOrConfirmOverwrite(
      (from) => api("POST", draftPath(from), text, true),
      "A draft with this diagram's process id already exists. Replace it with this file?");
    if (!d) { toast("Import cancelled — the draft you already had was kept"); return; }
    toast(d.layoutGenerated
      ? `Imported diagram “${d.name || d.processId}” — the file carried no layout, so one was generated`
      : `Imported diagram “${d.name || d.processId}”`, "ok");
    await reload();
  } catch (e) {
    toast("Import failed: " + e.message, "err");
  }
}

// importMIM converts a Microsoft Identity Manager (MIM/FIM) XOML workflow — or an
// Export-FIMConfig XML that embeds one — into a BPMN draft via POST
// /api/v1/imports/mim, then shows the per-node conversion report. The import
// lands as a draft (never a deploy); constructs without a faithful BPMN mapping
// are preserved in atlas:mimSource and flagged for review in the report.
async function importMIM(projectId, reload) {
  const file = await pickFile(".xoml,.xml,application/xml,text/xml");
  if (!file) return;
  let text;
  try { text = await file.text(); } catch (e) { toast("Import failed: " + e.message, "err"); return; }
  const base = file.name.replace(/\.[^.]+$/, "");
  const path = "/api/v1/imports/mim?name=" + encodeURIComponent(base) +
    (projectId ? "&projectId=" + encodeURIComponent(projectId) : "");
  let res;
  try { res = await api("POST", path, text, true); }
  catch (e) { toast("MIM import failed: " + e.message, "err"); return; }
  const r = res.report || { native: 0, preserved: 0, manualReview: 0, notes: [] };
  toast(`Imported “${res.name || res.processId}” — ${r.native} native, ${r.preserved} preserved, ${r.manualReview} to review`, "ok");
  if (reload) await reload();
  showMIMReport(res);
}

// showMIMReport renders the conversion report as a modal: per-node status badges
// (native / preserved / manual-review), the node id, the source activity and a
// reviewer note, plus a shortcut to open the freshly created draft in the Modeler.
function showMIMReport(res) {
  const r = res.report || { native: 0, preserved: 0, manualReview: 0, notes: [] };
  const color = (s) => ({ "native": "#1a7f37", "preserved": "#6a737d", "manual-review": "#9a6700" }[s] || "#6a737d");
  const badge = (s) => `<span style="display:inline-block;padding:1px 8px;border-radius:10px;font-size:11px;color:#fff;white-space:nowrap;background:${color(s)}">${esc(s)}</span>`;
  const rows = (r.notes || []).map((n) =>
    `<tr><td>${badge(n.status)}</td><td><code>${esc(n.nodeId)}</code></td><td>${esc(n.kind)}</td><td>${esc(n.activity)}</td><td class="muted">${esc(n.detail || "")}</td></tr>`).join("");
  const ov = document.createElement("div");
  ov.className = "modal-ov";
  ov.innerHTML = `
    <div class="modal" role="dialog" aria-modal="true" aria-label="MIM import report" style="max-width:860px">
      <div class="modal-head"><h2>MIM import — ${esc(res.name || res.processId)}</h2></div>
      <div class="modal-body">
        <p class="muted" style="margin:0 0 10px">${r.native} native · ${r.preserved} preserved · ${r.manualReview} to review. Preserved and review nodes keep their original XOML in the element's <b>atlas:mimSource</b> — check them before deploying.</p>
        <div style="max-height:52vh; overflow:auto">
          <table><thead><tr><th>Status</th><th>Node</th><th>Kind</th><th>Activity</th><th>Note</th></tr></thead>
            <tbody>${rows || `<tr><td colspan="5" class="muted">No nodes.</td></tr>`}</tbody></table>
        </div>
      </div>
      <div class="modal-foot">
        <button class="btn neutral" data-close title="Close this report">Close</button>
        <button class="btn" data-open title="Open the imported draft in the Modeler">Open in Modeler</button>
      </div>
    </div>`;
  document.body.appendChild(ov);
  const close = () => { ov.remove(); document.removeEventListener("keydown", onKey); };
  const onKey = (e) => { if (e.key === "Escape") close(); };
  document.addEventListener("keydown", onKey);
  ov.querySelector("[data-close]").addEventListener("click", close);
  ov.querySelector("[data-open]").addEventListener("click", () => {
    close();
    location.hash = "#/modeler/draft/" + encodeURIComponent(res.processId);
  });
  ov.addEventListener("click", (e) => { if (e.target === ov) close(); });
}

// a "New connector" inline form and per-row Edit / Enable-Disable / Delete. Each
// change hits the connector API, which rebuilds the runtime registry, then the page
// re-renders. Only a token *reference* is ever entered — never a secret value
// (ADR-0041).
function wireConnectorManagement(connectors) {
  const reload = () => viewConsoleConnectors();
  const slot = document.getElementById("connector-form-slot");
  const newBtn = document.getElementById("new-connector");
  if (newBtn && slot) {
    newBtn.addEventListener("click", () => {
      if (slot.dataset.open === "1") { slot.innerHTML = ""; slot.dataset.open = ""; return; }
      slot.dataset.open = "1";
      slot.innerHTML = `<form class="connector-form" style="display:flex;flex-wrap:wrap;gap:8px;align-items:end;margin:4px 0 14px">
        <label class="field" style="margin:0"><span>Kind</span><select name="kind"><option value="temis">temis</option><option value="clio">clio</option><option value="mail">mail</option><option value="sharepoint">sharepoint</option><option value="remedy">remedy</option><option value="jira">jira</option><option value="entra">entra</option><option value="ad">Active Directory</option><option value="postgres">PostgreSQL</option><option value="mariadb">MariaDB</option><option value="mssql">Microsoft SQL Server</option></select></label>
        <label class="field mail-only" style="margin:0"><span>Provider</span><select name="provider"><option value="smtp">SMTP</option><option value="gmail">Gmail API</option><option value="microsoft">Microsoft Graph</option><option value="preview">Preview (in-app outbox)</option></select></label>
        <label class="field" style="margin:0;flex:1 1 160px"><span>Name</span><input name="name" placeholder="risk-service" required/></label>
        <label class="field endpoint-field" style="margin:0;flex:1 1 200px"><span>Endpoint</span><input name="endpoint" placeholder="https://temis.internal" required/></label>
        <label class="field mail-only" style="margin:0;flex:1 1 180px"><span>Sender</span><input name="sender" placeholder="bot@example.com"/></label>
        <label class="field sql-only" style="margin:0;flex:1 1 100%"><span>Connection string</span><input name="connectionString" type="password" autocomplete="new-password"/></label>
        <label class="field credref-field" style="margin:0;flex:1 1 180px"><span class="credref-label">Token reference (optional)</span><input name="credentialsRef" placeholder="risk_token"/></label>
        <button class="btn" type="submit" title="Add this connector">Add</button>
        <button class="btn neutral conn-f-test" type="button" id="conn-test" title="Connect and authenticate with what is typed above — nothing is saved and no message is sent">Test connection</button>
        <p class="conn-test-result" style="flex:1 1 100%;margin:0;font-size:12.5px" hidden></p>
        <p class="muted conn-hint" style="flex:1 1 100%;margin:0;font-size:12.5px"></p></form>`;
      // Adapt the form to the kind and mail provider: SMTP needs a host:port endpoint
      // and (optionally) a password reference; a native provider (Gmail/Graph) needs no
      // endpoint but a credentialsRef naming a vault JSON auth bundle, and sends as the
      // sender mailbox. A SharePoint connector likewise defaults its Graph API base
      // (no endpoint) and needs a credentialsRef naming a vault OAuth bundle. The
      // mail-only fields hide for temis/clio/sharepoint.
      const form = slot.querySelector("form");
      const kindSel = form.querySelector('[name="kind"]');
      const providerSel = form.querySelector('[name="provider"]');
      const endpointIn = form.querySelector('[name="endpoint"]');
      const senderIn = form.querySelector('[name="sender"]');
      const credRefIn = form.querySelector('[name="credentialsRef"]');
      const credRefLabel = form.querySelector(".credref-label");
      const endpointField = form.querySelector(".endpoint-field");
      // Which fields a kind and provider actually use is one description, shared with
      // the edit dialog (ADR-0160) so a rule changed in one place cannot leave the
      // other asking for a credential nobody needs — or worse, not asking for one
      // that is required. A native mail provider and SharePoint default their API base
      // and authenticate with a vault bundle; SMTP, temis, clio and remedy dial an
      // endpoint; preview dials nothing at all, which is the whole point of it
      // (ADR-0150), so a field left standing there would read as if it were used.
      const sync = () => {
        const sh = connectorShape(kindSel.value, providerSel.value);
        form.querySelectorAll(".mail-only").forEach((el) => { el.style.display = sh.mail ? "" : "none"; });
        // The connection string is a SQL connector's whole configuration, so it is the
        // one field that appears for those kinds and for no other. It is not marked
        // required: an operator who already keeps the DSN in the vault names its key in
        // the reference field instead, and the server refuses a record with neither.
        // Hidden is not enough: a display:none input is still in the form, so its value
        // is still submitted — and a type="password" field is exactly what a password
        // manager fills in whether or not anyone can see it. Disabling it takes it out
        // of the FormData entirely, and clearing it means switching kinds cannot carry
        // a DSN typed for a previous one into the next create.
        // The example follows the product. It is the only thing on screen that says
        // which of the three connection-string syntaxes is expected, and they have
        // none in common — a MariaDB DSN is not even a URL.
        const connStrIn = form.querySelector('[name="connectionString"]');
        if (connStrIn) connStrIn.placeholder = sh.dsnPlaceholder;
        form.querySelectorAll(".sql-only").forEach((el) => {
          el.style.display = sh.sql ? "" : "none";
          el.querySelectorAll("input").forEach((inp) => {
            if (!sh.sql) inp.value = "";
            inp.disabled = !sh.sql;
          });
        });
        // The check covers mail and the SQL databases; the other kinds have none yet.
        form.querySelector(".conn-f-test").style.display = sh.test ? "" : "none";
        senderIn.required = sh.sender;
        endpointField.style.display = sh.endpoint ? "" : "none";
        endpointIn.required = sh.endpoint;
        endpointIn.placeholder = sh.endpointPlaceholder;
        form.querySelector(".credref-field").style.display = sh.credRef === "none" ? "none" : "";
        credRefIn.required = sh.credRef === "required";
        credRefIn.placeholder = sh.credRefPlaceholder;
        credRefLabel.textContent = sh.credRefLabel;
        // What this kind needs, said where it is chosen rather than discovered from a
        // failed job hours later. The hint is *not* mail-only: connectorShape writes
        // one for Active Directory and for the three databases too, and while it
        // carried the mail-only class the form wrote all of them into an element it had
        // just hidden — so the one sentence saying that a database's whole connection
        // string is the credential, and that Atlas supervises the worker for it, was
        // written for every SQL kind and shown for none. The edit dialog always showed
        // it, which is exactly the kind of disagreement ADR-0160 put the shape in one
        // place to prevent.
        const hintEl = form.querySelector(".conn-hint");
        hintEl.innerHTML = sh.hint;
        hintEl.style.display = sh.hint ? "" : "none";
      };
      kindSel.addEventListener("change", sync);
      providerSel.addEventListener("change", sync);
      sync();
      // Check what is typed, before it is stored — the moment a wrong host or a dead
      // credential is cheapest to fix, and the one where somebody is actually looking.
      const testBtn = form.querySelector("#conn-test");
      const testOut = form.querySelector(".conn-test-result");
      testBtn.addEventListener("click", async () => {
        const f = new FormData(form);
        testOut.hidden = false;
        testOut.className = "conn-test-result muted";
        testOut.textContent = "Checking…";
        testBtn.disabled = true;
        try {
          const res = await api("POST", "/api/v1/connectors/test", {
            name: (f.get("name") || "unnamed").trim(),
            kind: (f.get("kind") || "mail").trim(),
            provider: (f.get("provider") || "smtp").trim(),
            endpoint: (f.get("endpoint") || "").trim(),
            sender: (f.get("sender") || "").trim(),
            credentialsRef: (f.get("credentialsRef") || "").trim(),
            // A SQL connector's whole configuration is this string, so checking it
            // before it is sealed is the only moment the operator can still fix it in
            // the field they are looking at. The field is disabled for other kinds, so
            // FormData carries nothing for them.
            connectionString: (f.get("connectionString") || "").trim(),
          });
          testOut.className = "conn-test-result " + (res.ok ? "ok" : "err");
          testOut.textContent = (res.ok ? "✓ " : "✕ ") + (res.detail || (res.ok ? "Works." : "Failed."));
        } catch (err) {
          testOut.className = "conn-test-result err";
          testOut.textContent = "✕ " + err.message;
        } finally { testBtn.disabled = false; }
      });
      form.addEventListener("submit", async (e) => {
        e.preventDefault();
        const body = connectorCreateBody(e.target);
        try {
          await api("POST", "/api/v1/connectors", body);
          toast("Connector added", "ok");
          reload();
        } catch (err) { toast("Could not add connector: " + err.message, "err"); }
      });
    });
  }
  const rows = document.getElementById("connector-rows");
  if (rows) {
    rows.addEventListener("click", async (e) => {
      // Two things a row's cell can start: the usage count opens the list behind it,
      // and every action now arrives from the row's ⋯ menu rather than from a button
      // drawn on the row. The menu lives inside the <tr>, so which worker it belongs
      // to is still read off the row.
      const usage = e.target.closest("button[data-usage]");
      const btn = usage || e.target.closest(".dropdown-menu button[data-act]");
      if (!btn) return;
      const row = btn.closest("tr");
      if (!row) return;
      const id = row.dataset.id;
      const c = (connectors || []).find((x) => x.id === id);
      if (!c) return;
      if (usage) {
        openConnectorUsage({ connector: c });
        return;
      }
      const act = btn.dataset.act;
      try {
        if (act === "subs") {
          await toggleInboundSubs(row, id, c.kind);
          return;
        } else if (act === "share") {
          await toggleConnectorShare(c, viewConsoleConnectors);
          return;
        } else if (act === "provision") {
          toggleProvisionClio(row, id, c.name);
          return;
        } else if (act === "test") {
          // Empty recipient = stop at the door (connect, authenticate). A recipient
          // makes it a real send, which is the only thing that proves delivery. Only
          // mail has that second half: a database check dials and stops, because the
          // equivalent of "send one to see" would be running a statement.
          let to = "";
          if (c.kind === "mail") {
            to = window.prompt(
              `Test "${c.name}".\n\nSend a test message to which address?\nLeave empty to only check the connection and credential.`, "");
            if (to == null) return;
          }
          // The menu the button was in is already gone by now, so a disabled button is
          // no longer the feedback it used to be: a check can take seconds, and without
          // a word here nothing at all happens until the result lands.
          toast(`Checking "${c.name}"…`);
          btn.disabled = true;
          try {
            const res = await api("POST", "/api/v1/connectors/test", {
              name: c.name, kind: c.kind, provider: c.provider, endpoint: c.endpoint,
              sender: c.sender, credentialsRef: c.credentialsRef, to: to.trim(),
            });
            toast(res.detail || (res.ok ? "Connector works" : "Check failed"), res.ok ? "ok" : "warn");
          } catch (err) {
            toast("Check failed: " + err.message, "warn");
          } finally { btn.disabled = false; }
          return;
        } else if (act === "toggle") {
          await api("PATCH", "/api/v1/connectors/" + encodeURIComponent(id), { enabled: !c.enabled });
        } else if (act === "edit") {
          // The same dialog an operator reaches from an incident (ADR-0160) — which is
          // where most connector edits start, and why it is worth more than the two
          // window.prompts that used to stand here: it knows which fields this kind
          // and provider actually use, and it can check the result before saving.
          if (!(await editConnectorFlow({ api, toast, connector: c }))) return;
        } else if (act === "delete") {
          // The server refuses a delete that would park deployed models' tasks
          // (ADR-0163), so this asks only about what it can see and lets the refusal
          // carry the rest — the confirm names the processes instead of a bare count.
          if (!(await deleteConnectorFlow({ api, connector: c }))) return;
        }
        reload();
      } catch (err) { toast("Connector update failed: " + err.message, "err"); }
    });
  }
}

// toggleProvisionClio expands (or collapses) an inline panel under a clio connector
// row that provisions the connector's credential in one step: the operator supplies
// a clio admin token once, and Atlas mints a scoped read key on the clio instance
// and seals it as this connector's token — no copy-pasting a key. The admin token is
// sent once and never stored.
function toggleProvisionClio(row, connectorId, connectorName) {
  const existing = row.nextElementSibling;
  if (existing && existing.classList.contains("provision-row")) {
    existing.remove();
    return;
  }
  // Collapse a sibling Events panel if open, so only one panel shows at a time.
  if (existing && existing.classList.contains("subs-row")) existing.remove();

  const panel = document.createElement("tr");
  panel.className = "provision-row";
  panel.innerHTML = `<td colspan="3" style="background:var(--surface); padding:12px 18px">
    <div class="muted" style="margin-bottom:8px">Provision access — Atlas mints a scoped clio key with your admin token and stores it as this connector's credential. The admin token is used once and never stored.</div>
    <form id="prov-form" style="display:grid;gap:8px;grid-template-columns:1fr 1fr auto;align-items:end">
      <label class="field" style="margin:0"><span>clio admin token</span><input name="adminToken" type="password" autocomplete="off" placeholder="kid.secret (admin)" required/></label>
      <label class="field" style="margin:0"><span>Read subject</span><input name="subject" placeholder="/employees" required/></label>
      <button class="btn" type="submit" title="Mint the scoped key and store it as this connector’s credential">Provision</button>
      <label class="check" style="grid-column:1 / -1;margin:0;display:flex;gap:8px;align-items:center">
        <input type="checkbox" name="recursive" checked/>
        <span>Recursive — grant read on the whole subtree (<code>read:/employees/*</code>), needed to watch child subjects.</span>
      </label>
      <div class="muted" style="grid-column:1 / -1">Requires a clio <b>admin</b> key. The minted key is read-only on the subject above; nothing writes your admin token to disk.</div>
    </form></td>`;
  row.after(panel);
  panel.querySelector("#prov-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = new FormData(e.target);
    try {
      const res = await api("POST", "/api/v1/connectors/" + encodeURIComponent(connectorId) + "/provision-clio-key", {
        adminToken: f.get("adminToken") || "",
        subject: (f.get("subject") || "").trim(),
        recursive: f.get("recursive") === "on",
        keyName: "atlas-" + connectorName,
      });
      toast("Provisioned — token " + (res && res.credentialsRef ? res.credentialsRef : "stored") + " (" + (res && res.scope) + ")", "ok");
      panel.remove();
    } catch (err) { toast("Provisioning failed: " + err.message, "err"); }
  });
}

// toggleInboundSubs expands (or collapses) an inline panel under a clio connector row
// listing its inbound event subscriptions (ADR-0075) with an add form and per-row
// delete. A subscription watches a clio subject and republishes each new event as an
// Atlas message that starts/wakes processes; the correlation key is a FEEL expression
// over the event body (blank = keyless).
// toggleConnectorShare opens the panel that decides who else may configure one
// connector (ADR-0205).
//
// It exists because the endpoints alone are not the feature. Ownership landed on
// connectors so that a person can own a mailbox and share it with whom they choose;
// "whom they choose" through a curl command is a capability only its author has.
// The same shape as an application's sharing, deliberately: a person who has shared
// one has already learned this one.
async function toggleConnectorShare(c, reload) {
  const slot = document.getElementById("share-" + c.id);
  if (!slot) return;
  if (!slot.hidden) { slot.hidden = true; slot.innerHTML = ""; return; }

  // The directories are admin-only, so a non-admin owner shares by pasting an id
  // rather than picking from a list they may not read. Said out loud in the panel
  // rather than left as an empty dropdown that looks broken.
  let users = [];
  let groups = [];
  try { users = (await api("GET", "/api/v1/users")) || []; } catch { /* not an admin */ }
  try { groups = (await api("GET", "/api/v1/groups")) || []; } catch { /* not an admin */ }
  const byID = new Map([
    ...users.map((u) => [u.id, u.displayName || u.username]),
    ...groups.map((g) => [g.id, g.name]),
  ]);
  const canPick = users.length > 0 || groups.length > 0;

  const memberChip = (m) => `<span class="chip">${esc(m.ref.type === "group" ? "👥 " : "")}${
    esc(byID.get(m.ref.id) || m.ref.id)} · ${esc(m.role)}<button type="button" class="chip-x"
    data-unshare="${esc(m.ref.id)}" title="Withdraw this access">✕</button></span>`;
  const members = (c.members || []).length
    ? (c.members || []).map(memberChip).join(" ")
    : `<span class="muted" style="font-size:12px">Nobody else. Only you and administrators can configure it.</span>`;

  const picker = canPick
    ? `<select class="field" id="share-who-${esc(c.id)}" style="margin:0; min-width:180px">
         ${users.map((u) => `<option value="user:${esc(u.id)}">${esc(u.displayName || u.username)}</option>`).join("")}
         ${groups.map((g) => `<option value="group:${esc(g.id)}">👥 ${esc(g.name)}</option>`).join("")}
       </select>`
    : `<input class="field" id="share-who-${esc(c.id)}" style="margin:0; min-width:180px"
         placeholder="user:usr_… or group:grp_…" />`;

  slot.hidden = false;
  slot.innerHTML = `
    <div class="conn-share-head">Who else may configure <b>${esc(c.name)}</b></div>
    <div style="margin:6px 0">${members}</div>
    <div style="display:flex; gap:6px; align-items:center; flex-wrap:wrap; margin-top:8px">
      ${picker}
      <select class="field" id="share-role-${esc(c.id)}" style="margin:0">
        <option value="viewer">may see it</option>
        <option value="editor">may change it</option>
      </select>
      <button type="button" class="btn ghost" data-share-add="${esc(c.id)}">Share</button>
    </div>
    ${canPick ? "" : `<p class="muted" style="font-size:12px; margin:8px 0 0">Paste the id of the
      person or group. The directory is administrators-only, so it cannot be offered as a list here.</p>`}
    <p class="muted" style="font-size:12px; margin:8px 0 0">Sharing never reaches a running
    process: a deployed model resolves this connector by name whoever started it.</p>`;

  slot.querySelector("[data-share-add]").addEventListener("click", async () => {
    const raw = String(document.getElementById("share-who-" + c.id).value || "").trim();
    const role = document.getElementById("share-role-" + c.id).value;
    const [type, id] = raw.includes(":") ? [raw.slice(0, raw.indexOf(":")), raw.slice(raw.indexOf(":") + 1)] : ["user", raw];
    if (!id) { toast("Name who to share it with", "warn"); return; }
    try {
      await api("PUT", `/api/v1/connectors/${encodeURIComponent(c.id)}/members/${encodeURIComponent(id)}`,
        { role, type });
      toast("Shared");
      reload();
    } catch (e) { toast(e.message, "err"); }
  });
  slot.addEventListener("click", async (e) => {
    const x = e.target.closest("button[data-unshare]");
    if (!x) return;
    try {
      await api("DELETE", `/api/v1/connectors/${encodeURIComponent(c.id)}/members/${encodeURIComponent(x.dataset.unshare)}`);
      toast("Access withdrawn");
      reload();
    } catch (err) { toast(err.message, "err"); }
  });
}

async function toggleInboundSubs(row, connectorId, kind) {
  const existing = row.nextElementSibling;
  if (existing && existing.classList.contains("subs-row")) {
    existing.remove();
    return;
  }
  const subs = (await api("GET", "/api/v1/connectors/" + encodeURIComponent(connectorId) + "/inbound-subscriptions")) || [];
  const list = subs.map((s) => `<tr data-sid="${esc(s.id)}">
      <td><code>${esc(s.jql || s.watchedSubject)}</code>${s.recursive ? ' <span class="muted">(recursive)</span>' : ""}${s.jql ? ` <span class="muted">(on ${esc(s.cursorField || "created")})</span>` : ""}</td>
      <td>→ message <span class="chip">${esc(s.messageName)}</span>${s.correlationKey ? ` on <code>${esc(s.correlationKey)}</code>` : ""}</td>
      <td>${s.enabled ? '<span class="pill ok"><span class="dot"></span>on</span>' : '<span class="pill warn"><span class="dot"></span>off</span>'}</td>
      <td style="text-align:right"><button class="btn ghost danger" data-sdel title="Delete this subscription">Delete</button></td>
    </tr>`).join("") || `<tr><td colspan="4" class="muted" style="padding:10px">No subscriptions. Add one below to have clio events start or wake processes.</td></tr>`;
  const isJira = kind === "jira";
  const what = isJira
    ? `<div class="muted" style="margin-bottom:8px">Inbound event watches — the issues a JQL matches are published as Atlas messages, so a new ticket starts a process (ADR-0214). Atlas polls; nothing has to reach this server from the internet.</div>`
    : `<div class="muted" style="margin-bottom:8px">Inbound event subscriptions — a watched clio subject's events are published as Atlas messages (ADR-0075).</div>`;
  const source = isJira
    ? `<label class="field" style="margin:0"><span>JQL</span><input name="jql" placeholder="project = OPS AND issuetype = Bug" required/></label>`
    : `<label class="field" style="margin:0"><span>Watched subject</span><input name="watchedSubject" placeholder="/employees" required/></label>`;
  const extra = isJira
    ? `<label class="check" style="grid-column:1 / -1;margin:0;display:flex;gap:12px;align-items:center">
        <span>Watch</span>
        <select name="cursorField" class="input" style="width:auto">
          <option value="created">new issues</option>
          <option value="updated">changed issues</option>
        </select>
        <span class="muted">Write no <code>ORDER BY</code>: the watch orders by this field itself, because that is what makes its resume position mean anything. The query must restrict what it matches — Jira refuses an unbounded one — so name at least a project.</span>
      </label>
      <div class="muted" style="grid-column:1 / -1">The correlation key (FEEL) sees <code>issueKey</code>, <code>issueId</code>, <code>projectKey</code>, <code>issueType</code>, <code>summary</code>, <code>status</code>, <code>reporter</code>, <code>created</code>, <code>updated</code>, <code>eventType</code>, and <code>issue</code> — the whole issue, for anything not named here. These are also seeded as process variables on the started instance. A new watch is forward-only: existing issues are skipped, so adding one to a project with a long history does not start a process per old ticket.</div>`
    : `<label class="check" style="grid-column:1 / -1;margin:0;display:flex;gap:8px;align-items:center">
        <input type="checkbox" name="recursive"/>
        <span>Recursive — also watch the subject's subtree (a watch on <code>/employees</code> catches an event written to <code>/employees/E-123456</code>).</span>
      </label>
      <div class="muted" style="grid-column:1 / -1">The correlation key (FEEL) sees the event body plus <code>subject</code>, <code>subjectTail</code> (the last path segment, e.g. <code>E-123456</code>), <code>eventType</code> and <code>eventId</code>. These are also seeded as process variables on the started/woken instance.</div>`;
  const panel = document.createElement("tr");
  panel.className = "subs-row";
  panel.innerHTML = `<td colspan="3" style="background:var(--surface); padding:12px 18px">
    ${what}
    <table style="width:100%"><tbody id="subs-body">${list}</tbody></table>
    <form id="subs-form" style="display:grid;gap:8px;grid-template-columns:1fr 1fr 1fr auto;align-items:end;margin-top:10px">
      ${source}
      <label class="field" style="margin:0"><span>Message name</span><input name="messageName" placeholder="${isJira ? "jira.ticket.created" : "employee.created"}" required/></label>
      <label class="field" style="margin:0"><span>Correlation key (FEEL, optional)</span><input name="correlationKey" placeholder="${isJira ? "= issueKey" : "= subjectTail"}"/></label>
      <button class="btn" type="submit" title="Add this inbound event watch">Add</button>
      ${extra}
    </form></td>`;
  row.after(panel);
  panel.querySelector("#subs-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const f = new FormData(e.target);
    try {
      const body = {
        messageName: (f.get("messageName") || "").trim(),
        correlationKey: (f.get("correlationKey") || "").trim(),
      };
      if (isJira) {
        body.jql = (f.get("jql") || "").trim();
        body.cursorField = f.get("cursorField") || "created";
      } else {
        body.watchedSubject = (f.get("watchedSubject") || "").trim();
        body.recursive = f.get("recursive") === "on";
      }
      await api("POST", "/api/v1/connectors/" + encodeURIComponent(connectorId) + "/inbound-subscriptions", body);
      toast("Subscription added", "ok");
      panel.remove();
      await toggleInboundSubs(row, connectorId, kind);
    } catch (err) { toast("Could not add subscription: " + err.message, "err"); }
  });
  panel.querySelector("#subs-body").addEventListener("click", async (e) => {
    const del = e.target.closest("button[data-sdel]");
    if (!del) return;
    const sid = del.closest("tr").dataset.sid;
    try {
      await api("DELETE", "/api/v1/inbound-subscriptions/" + encodeURIComponent(sid));
      panel.remove();
      await toggleInboundSubs(row, connectorId, kind);
    } catch (err) { toast("Could not delete subscription: " + err.message, "err"); }
  });
}

// AD_SEED_EXAMPLE is a directory small enough to read and complete enough to run a
// leaver against: an OU, two accounts, and the group one of them is in. It exists
// because "starting entries" is a question most people cannot answer on the spot —
// the honest answer is "whatever your process expects to find", which is no help when
// you are trying a process out for the first time. One click and a leaver has
// something to disable.
const AD_SEED_EXAMPLE = [
  "dn: ou=Mitarbeitende,dc=example,dc=com",
  "objectClass: organizationalUnit",
  "ou: Mitarbeitende",
  "",
  "dn: cn=Ada Lovelace,ou=Mitarbeitende,dc=example,dc=com",
  "objectClass: user",
  "cn: Ada Lovelace",
  "sAMAccountName: ada.lovelace",
  "userPrincipalName: ada.lovelace@example.com",
  "department: IT",
  "userAccountControl: 512",
  "",
  "dn: cn=Grace Hopper,ou=Mitarbeitende,dc=example,dc=com",
  "objectClass: user",
  "cn: Grace Hopper",
  "sAMAccountName: grace.hopper",
  "userPrincipalName: grace.hopper@example.com",
  "department: IT",
  "userAccountControl: 512",
  "",
  "dn: cn=IT,ou=Gruppen,dc=example,dc=com",
  "objectClass: group",
  "cn: IT",
  "member: cn=Ada Lovelace,ou=Mitarbeitende,dc=example,dc=com",
  "",
].join("\n");

// wireADMock binds the Active-Directory mockup switch: one PUT, and the supervised
// AD worker is restarted holding the new setting (ADR-0181). Writing it is
// admin-gated server-side, so a non-admin gets the refusal as a message rather than
// as a control that silently does nothing.
//
// The starting entries are *content*, not a path
// (ADR-0202). A file chosen here is read in the
// browser and posted as text, so what an operator picks is what Atlas stores — no
// filename that has to mean the same thing on somebody else's disk, and nothing to
// place on the worker's host.
function wireADMock() {
  const save = document.getElementById("admock-save");
  if (!save) return;
  const note = document.getElementById("admock-note");
  const box = document.getElementById("admock-seed");
  const state = document.getElementById("admock-seed-state");
  const file = document.getElementById("admock-seed-file");
  // Seeded from what is stored, so an operator who only flips the switch and saves
  // does not silently strip the name off a seed they never touched.
  let seedName = (state && state.dataset.seedName) || "";

  // describe reports what is loaded without claiming to have parsed it: the entry
  // count comes back from the server, which is the only side that actually parses.
  const describe = (text, name) => {
    if (!state) return;
    state.innerHTML = text.trim()
      ? `<b>${esc(name || "Starting entries")}</b> — <span class="muted">not saved yet</span>`
      : `<span class="muted">No starting entries — the mockup begins with an empty directory.</span>`;
  };

  const pick = document.getElementById("admock-seed-pick");
  if (pick && file) {
    pick.addEventListener("click", () => file.click());
    file.addEventListener("change", async () => {
      const f = file.files && file.files[0];
      if (!f) return;
      try {
        box.value = await f.text();
        seedName = f.name;
        describe(box.value, seedName);
        if (note) note.textContent = "Read from " + f.name + " — press Save to hand it to the worker.";
      } catch (e) {
        if (note) note.textContent = "Could not read that file: " + e.message;
      }
      file.value = ""; // so picking the same file again still fires a change
    });
  }

  const edit = document.getElementById("admock-seed-edit");
  if (edit) {
    edit.addEventListener("click", () => {
      box.style.display = box.style.display === "none" ? "block" : "none";
      if (box.style.display === "block") box.focus();
    });
  }

  const example = document.getElementById("admock-seed-example");
  if (example) {
    example.addEventListener("click", () => {
      box.value = AD_SEED_EXAMPLE;
      seedName = "example-directory.ldif";
      box.style.display = "block";
      describe(box.value, seedName);
      if (note) note.textContent = "An example directory — press Save to hand it to the worker.";
    });
  }

  const clear = document.getElementById("admock-seed-clear");
  if (clear) {
    clear.addEventListener("click", () => {
      box.value = "";
      seedName = "";
      describe("", "");
      if (note) note.textContent = "Removed — press Save to start from an empty directory.";
    });
  }

  save.addEventListener("click", async () => {
    const enabled = document.getElementById("admock-on").checked;
    const seed = box.value.trim();
    save.disabled = true;
    try {
      await api("PUT", "/api/v1/settings/ad-mock", { enabled, seed, seedName });
      toast(enabled
        ? "Mockup on — the AD worker restarts and writes to no directory."
        : "Mockup off — the AD worker restarts and talks to the domain controller again.");
      await viewConsoleConnectors();
    } catch (e) {
      if (note) note.textContent = e.message;
      save.disabled = false;
    }
  });
}

// SQL_SEED_EXAMPLE is a seed small enough to read and complete enough to run a whole
// process against: the lookup examples/mssql-eintrittsmeldung.bpmn makes, the update it
// makes afterwards, and the answer for a person who is not there — which is the branch
// most models get wrong and the one a mockup is most useful for.
const SQL_SEED_EXAMPLE = JSON.stringify({
  answers: [
    {
      statement: "SELECT id, mail, abteilung FROM personen WHERE kuerzel = @p1",
      params: ["abo"],
      columns: ["id", "mail", "abteilung"],
      rows: [[7, "arno.boesch@example.com", "IT"]],
    },
    {
      // No params: the fallback for every other kuerzel. Nobody found, which is a
      // business answer and not a failure — the model's gateway is what reads it.
      statement: "SELECT id, mail, abteilung FROM personen WHERE kuerzel = @p1",
      columns: ["id", "mail", "abteilung"],
      rows: [],
    },
    {
      statement: "UPDATE personen SET aktiv = @aktiv WHERE id = @id",
      named: { id: 7, aktiv: true },
      affected: 1,
    },
    {
      // What a redelivered job meets on a real database, available on demand here.
      statement: "INSERT INTO personen (kuerzel, mail) VALUES (@p1, @p2)",
      error: "Violation of UNIQUE KEY constraint 'UQ_personen_kuerzel'",
    },
  ],
}, null, 2);

// wireSQLMock binds the database mockup switch: one PUT, and the supervised SQL
// workers are restarted holding the new setting. Writing it is admin-gated
// server-side, so a non-admin gets the refusal as a message rather than as a control
// that silently does nothing.
//
// The prepared answers are *content*, not a path, for the reason the AD seed is
// (ADR-0202): the Console is org-wide, and a path typed here belongs to whichever host
// happens to run the worker. A file chosen here is read in the browser and posted as
// text, so what an operator picks is what Atlas stores.
function wireSQLMock() {
  const save = document.getElementById("sqlmock-save");
  if (!save) return;
  const note = document.getElementById("sqlmock-note");
  const box = document.getElementById("sqlmock-seed");
  const state = document.getElementById("sqlmock-seed-state");
  const file = document.getElementById("sqlmock-seed-file");
  // Seeded from what is stored, so an operator who only flips the switch and saves does
  // not silently strip the name off a seed they never touched.
  let seedName = (state && state.dataset.seedName) || "";

  // describe reports what is loaded without claiming to have parsed it: the answer
  // count comes back from the server, which is the only side that actually parses.
  const describe = (text, name) => {
    if (!state) return;
    state.innerHTML = text.trim()
      ? `<b>${esc(name || "Prepared answers")}</b> — <span class="muted">not saved yet</span>`
      : `<span class="muted">No prepared answers — every statement will fail, naming itself.</span>`;
  };

  const pick = document.getElementById("sqlmock-seed-pick");
  if (pick && file) {
    pick.addEventListener("click", () => file.click());
    file.addEventListener("change", async () => {
      const f = file.files && file.files[0];
      if (!f) return;
      try {
        box.value = await f.text();
        seedName = f.name;
        describe(box.value, seedName);
        if (note) note.textContent = "Read from " + f.name + " — press Save to hand it to the workers.";
      } catch (e) {
        if (note) note.textContent = "Could not read that file: " + e.message;
      }
      file.value = ""; // so picking the same file again still fires a change
    });
  }

  const edit = document.getElementById("sqlmock-seed-edit");
  if (edit) {
    edit.addEventListener("click", () => {
      box.style.display = box.style.display === "none" ? "block" : "none";
      if (box.style.display === "block") box.focus();
    });
  }

  const example = document.getElementById("sqlmock-seed-example");
  if (example) {
    example.addEventListener("click", () => {
      box.value = SQL_SEED_EXAMPLE;
      seedName = "example-answers.json";
      box.style.display = "block";
      describe(box.value, seedName);
      if (note) note.textContent = "An example seed — press Save to hand it to the workers.";
    });
  }

  const clear = document.getElementById("sqlmock-seed-clear");
  if (clear) {
    clear.addEventListener("click", () => {
      box.value = "";
      seedName = "";
      describe("", "");
      if (note) note.textContent = "Removed — press Save to start from a mockup with no answers.";
    });
  }

  save.addEventListener("click", async () => {
    const enabled = document.getElementById("sqlmock-on").checked;
    const seed = box.value.trim();
    save.disabled = true;
    try {
      await api("PUT", "/api/v1/settings/sql-mock", { enabled, seed, seedName });
      toast(enabled
        ? "Mockup on — the SQL workers restart and reach no database."
        : "Mockup off — the SQL workers restart and talk to their databases again.");
      await viewConsoleConnectors();
    } catch (e) {
      if (note) note.textContent = e.message;
      save.disabled = false;
    }
  });
}

// wireSecretsManagement binds the encrypted-vault panel (ADR-0069): a "New secret"
// upsert form, per-row "Set value" (rotate) and "Delete". Secrets are keyed by name,
// have no enable/disable, and are write-only — the value is never read back, so a set
// is an idempotent PUT and the UI only ever sends values, never displays them. When
// the vault is denied (non-admin) or unconfigured there is nothing to wire.
function wireSecretsManagement(secrets, state, connectors) {
  if (state !== "ok") return;
  const reload = () => viewConsoleConnectors();
  const put = (name, value) => api("PUT", "/api/v1/secrets/" + encodeURIComponent(name), { value });
  const slot = document.getElementById("secret-form-slot");
  const newBtn = document.getElementById("new-secret");
  if (newBtn && slot) {
    newBtn.addEventListener("click", () => {
      if (slot.dataset.open === "1") { slot.innerHTML = ""; slot.dataset.open = ""; return; }
      slot.dataset.open = "1";
      slot.innerHTML = `<form class="secret-form" style="margin:4px 0 14px">
        <div style="display:grid;gap:8px;grid-template-columns:1fr 1fr auto;align-items:end">
          <label class="field" style="margin:0"><span>Name</span><input name="name" placeholder="risk_token" required/></label>
          <div id="secret-value-slot">${secretValueFieldHTML(null)}</div>
          <button class="btn" type="submit" title="Save this secret to the vault">Save</button>
        </div>
        <div id="secret-hint-slot"></div>
        <p class="secret-error" hidden></p></form>`;
      const form = slot.querySelector("form");
      const nameIn = form.querySelector('[name="name"]');
      const valueSlot = form.querySelector("#secret-value-slot");
      const hintSlot = form.querySelector("#secret-hint-slot");
      const err = form.querySelector(".secret-error");
      let shape = null;
      // The name *is* the binding: the moment it matches a connector's token
      // reference, the form knows what the value has to be and says so — before it
      // is typed, rather than after it has failed.
      const syncShape = () => {
        const next = secretShapeFor(connectors, nameIn.value.trim());
        const kindChanged = Boolean(next && next.skeleton) !== Boolean(shape && shape.skeleton);
        shape = next;
        hintSlot.innerHTML = nameIn.value.trim() ? secretHintHTML(shape) : "";
        if (kindChanged) valueSlot.innerHTML = secretValueFieldHTML(shape);
      };
      nameIn.addEventListener("input", syncShape);
      form.addEventListener("click", (e) => {
        if (!e.target.closest("[data-fill]") || !shape) return;
        form.querySelector('[name="value"]').value = JSON.stringify(shape.skeleton, null, 2);
      });
      form.addEventListener("submit", async (e) => {
        e.preventDefault();
        const f = new FormData(e.target);
        const name = (f.get("name") || "").trim();
        const value = f.get("value") || "";
        const problem = checkSecretValue(secretShapeFor(connectors, name), value);
        err.hidden = !problem;
        err.textContent = problem;
        if (problem) return;
        try {
          await put(name, value);
          toast("Secret saved", "ok");
          reload();
        } catch (e2) { toast("Could not save secret: " + e2.message, "err"); }
      });
    });
  }
  const rows = document.getElementById("secret-rows");
  if (rows) {
    rows.addEventListener("click", async (e) => {
      const btn = e.target.closest("button[data-sact]");
      if (!btn) return;
      const name = btn.closest("tr").dataset.name;
      if (!name) return;
      try {
        if (btn.dataset.sact === "set") {
          toggleSetSecret(btn.closest("tr"), name, connectors, put, reload);
          return;
        } else if (btn.dataset.sact === "delete") {
          if (!window.confirm(`Delete secret "${name}"? A connector referencing it will resolve to no token.`)) return;
          await api("DELETE", "/api/v1/secrets/" + encodeURIComponent(name));
        }
        reload();
      } catch (err) { toast("Secret update failed: " + err.message, "err"); }
    });
  }
}

// toggleSetSecret expands (or collapses) an inline panel under a secret's row for
// rotating its value (ADR-0155). It replaced a one-line window.prompt, which was the
// wrong instrument for the job in two ways: a JSON credential bundle is several lines
// that have to be pasted and read back, and a prompt can say nothing about what the
// value is supposed to be — so the field that most needed an explanation was the one
// field in the console that could not carry one.
function toggleSetSecret(row, name, connectors, put, reload) {
  const existing = row.nextElementSibling;
  if (existing && existing.classList.contains("secret-set-row")) {
    existing.remove();
    return;
  }
  const shape = secretShapeFor(connectors, name);
  const panel = document.createElement("tr");
  panel.className = "secret-set-row";
  panel.innerHTML = `<td colspan="2" style="background:var(--surface); padding:12px 18px">
    <form class="secret-form">
      ${secretHintHTML(shape)}
      ${secretValueFieldHTML(shape)}
      <p class="muted" style="font-size:12px;margin:6px 0 0">Replaces the stored value for
        <span class="chip">${esc(name)}</span>. It is sealed at rest and never shown again.</p>
      <p class="secret-error" hidden></p>
      <div style="display:flex;gap:8px;margin-top:10px">
        <button class="btn" type="submit" title="Save the new value for this secret">Save value</button>
        <button class="btn neutral" type="button" data-cancel title="Cancel without changing the secret">Cancel</button>
      </div>
    </form></td>`;
  row.after(panel);
  const form = panel.querySelector("form");
  const err = form.querySelector(".secret-error");
  form.querySelector("[data-cancel]").addEventListener("click", () => panel.remove());
  const fill = form.querySelector("[data-fill]");
  if (fill) {
    fill.addEventListener("click", () => {
      form.querySelector('[name="value"]').value = JSON.stringify(shape.skeleton, null, 2);
    });
  }
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const value = new FormData(e.target).get("value") || "";
    const problem = checkSecretValue(shape, value);
    err.hidden = !problem;
    err.textContent = problem;
    if (problem) return;
    try {
      await put(name, value);
      toast("Secret updated", "ok");
      reload();
    } catch (e2) {
      err.hidden = false;
      err.textContent = e2.message;
    }
  });
  form.querySelector('[name="value"]').focus();
}

// editDmnRef opens the embedded DMN editor (ADR-0062) on a reference's model and,
// on save, keeps the Project Explorer in sync. Editing overwrites the model in
// place under the same handle, so the reference (and any business-rule-task
// selection) stays valid; only the display name can drift, so a rename in the
// editor is mirrored onto the reference here. The editor module is imported lazily
// — same discipline as the BPMN editor — so the Modeler home stays light. When the
// model can't be edited locally (a remote temis service, or a dangling handle) the
// editor surfaces the failure itself and resolves to null, leaving the row as-is.
async function editDmnRef(ref, reload) {
  if (!ref.modelRef) {
    toast("Diese DMN-Referenz hat kein lokal editierbares Modell.", "err");
    return;
  }
  const { openDmnEditor } = await import("./dmn-editor.js");
  const result = await openDmnEditor({ api, toast, projectId: ref.projectId || "", modelRef: ref.modelRef });
  if (!result) return; // cancelled or failed (the editor already reported why)
  // Editing keeps the handle; mirror a decision rename onto the reference so the
  // Explorer label doesn't go stale.
  const newName = (result.name || "").trim();
  if (newName && newName !== ref.name) {
    try {
      await api("PATCH", `/api/v1/dmnrefs/${encodeURIComponent(ref.id)}`, { name: newName });
    } catch (e) { toast("Modell gespeichert, Umbenennen fehlgeschlagen: " + e.message, "err"); }
  }
  await reload();
}

// moveDmnRef reassigns a DMN reference to a project (or to Ungrouped when "").
async function moveDmnRef(id, projectId, reload) {
  try {
    await api("PATCH", `/api/v1/dmnrefs/${encodeURIComponent(id)}`, { projectId });
  } catch (e) { toast("could not move reference: " + e.message, "err"); }
  await reload();
}

async function deleteDmnRef(id, reload) {
  if (!window.confirm("Delete this DMN reference? The temis model itself is not affected.")) return;
  try {
    await api("DELETE", `/api/v1/dmnrefs/${encodeURIComponent(id)}`);
    toast("Deleted DMN reference", "ok");
  } catch (e) { toast("could not delete reference: " + e.message, "err"); }
  await reload();
}

async function deleteForm(id, reload) {
  if (!window.confirm("Delete this form? A user task still bound to it will show no form until it is re-linked.")) return;
  try {
    await api("DELETE", `/api/v1/forms/${encodeURIComponent(id)}`);
    toast("Deleted form", "ok");
  } catch (e) { toast("could not delete form: " + e.message, "err"); }
  await reload();
}

// refStatusHTML renders a DMN reference's deploy-time validation outcome: valid
// (with decision count), resolved-but-invalid, or unresolved.
function refStatusHTML(res) {
  if (res.valid) {
    const n = (res.decisions || []).length;
    return `<span class="pill ok"><span class="dot"></span>valid</span>${n ? ` <span class="muted" style="font-size:12px">· ${n} decision${n === 1 ? "" : "s"}</span>` : ""}`;
  }
  if (res.resolved) return `<span class="pill err"><span class="dot"></span>invalid</span>`;
  return `<span class="pill warn"><span class="dot"></span>unresolved</span>`;
}

// applyRefStatus writes a validation result into a reference row's status cell.
function applyRefStatus(id, res) {
  const el = document.querySelector(`[data-refstatus="${id}"]`);
  if (!el) return;
  el.className = "";
  el.style.fontSize = "12px";
  el.innerHTML = refStatusHTML(res);
  el.title = res.message || "";
}

// validateDmnRef resolves one reference's temis model and compiles it — the same
// deploy-time gate the server runs — and shows the outcome inline.
async function validateDmnRef(id) {
  const el = document.querySelector(`[data-refstatus="${id}"]`);
  if (el) { el.className = "muted"; el.textContent = "validating…"; }
  try {
    applyRefStatus(id, await api("POST", `/api/v1/dmnrefs/${encodeURIComponent(id)}/validate`));
  } catch (e) {
    if (el) { el.className = "muted"; el.textContent = "not validated"; }
    toast("could not validate: " + e.message, "err");
  }
}

// validateProject runs the project preflight — resolve + validate every DMN
// reference — and reflects each result plus an overall verdict.
async function validateProject(projectId) {
  try {
    const rep = await api("POST", `/api/v1/applications/${encodeURIComponent(projectId)}/validate`);
    for (const r of rep.references) applyRefStatus(r.id, r);
    toast(rep.ok ? "All DMN references are valid" : "Some DMN references are unresolved or invalid",
      rep.ok ? "ok" : "err");
  } catch (e) { toast("could not validate application: " + e.message, "err"); }
}

// deployProject publishes the whole application: the server validates its DMN
// references (the deploy-time gate) and, only if all pass, deploys its BPMN
// diagrams together as runnable definitions (the ADR-0128 headline "Publish"
// action). A refusal (409) carries the reason and the per-reference results,
// which we surface without a reload; a success reloads so the new definitions
// show under "Deployed". Uses a raw fetch so the refusal body (which is not an
// {error} shape) is read instead of thrown away.
async function deployProject(id, reload) {
  if (!window.confirm("Publish this application? Its DMN references are validated, then its BPMN diagrams are deployed together as runnable definitions.")) return;
  let rep;
  try {
    const res = await fetch(`/api/v1/applications/${encodeURIComponent(id)}/deploy`, { method: "POST" });
    rep = await res.json();
    if (res.ok && rep.deployed) {
      const n = (rep.definitions || []).length;
      toast(n ? `Published ${n} definition${n === 1 ? "" : "s"}` : "Nothing to publish in this application", "ok");
      await reload();
      return;
    }
  } catch (e) {
    toast("publish failed: " + e.message, "err");
    return;
  }
  // Refused (or a server error): show why and reflect any DMN results in place.
  toast(rep.reason || rep.error || "Publish refused", "err");
  for (const r of rep.references || []) applyRefStatus(r.id, r);
}

// downloadApplicationSource downloads the application's source tree (ADR-0134): a
// manifest plus its drafts and forms as native .bpmn and .form.json files, in one
// .tar.gz. A plain same-origin navigation, so the session cookie authenticates it,
// exactly as the console's backup download does.
function downloadApplicationSource(id) {
  window.location.href = `/api/v1/applications/${encodeURIComponent(id)}/source`;
}

// importApplicationSource reads a source tree back in. Which application it lands
// in is not this dialog's choice: the tree's manifest carries the portable key, so
// the server updates the application that key names or creates it when this server
// has never seen it (ADR-0134). An import never deletes — artifacts the tree omits
// are reported back and left alone.
function importApplicationSource(reload) {
  const picker = document.createElement("input");
  picker.type = "file";
  picker.accept = ".gz,.tgz,application/gzip";
  picker.addEventListener("change", async () => {
    const file = picker.files[0];
    if (!file) return;
    try {
      const res = await fetch("/api/v1/applications/source", {
        method: "POST",
        headers: { "Content-Type": "application/gzip" },
        body: file,
      });
      const data = await res.json().catch(() => null);
      if (!res.ok) throw new Error((data && data.error) || res.statusText);
      const n = (data.processes || 0) + (data.forms || 0) + (data.decisions || 0);
      toast(
        `${data.created ? "Created" : "Updated"} ${data.name} — ${n} artifact${n === 1 ? "" : "s"}` +
        (data.untracked && data.untracked.length
          ? ` · ${data.untracked.length} local artifact${data.untracked.length === 1 ? "" : "s"} not in the tree, kept`
          : ""),
        "ok");
      await reload();
    } catch (e) {
      toast("Import failed: " + (e && e.message || e), "err");
    }
  });
  picker.click();
}

// publishApplication is the ADR-0128 headline action: ship the whole application as
// one bundle and record the release it becomes. It shows the version the publish
// will mint (v(n) → v(n+1)) and takes an optional changelog note. A refused bundle
// deploys nothing and records no release, so the version does not advance.
async function publishApplication(id, reload) {
  let next = 1;
  try {
    const releases = await api("GET", `/api/v1/applications/${encodeURIComponent(id)}/releases`);
    if (releases.length) next = releases[0].version + 1;
  } catch { /* best-effort: fall back to "the next version" without a number */ }

  const note = window.prompt(
    `Publish this application as v${next}?\n\n` +
    "Its DMN references are validated, then its artifacts are deployed together as " +
    "one release.\n\nOptional note describing what changes:", "");
  if (note == null) return; // cancelled

  let rep;
  try {
    const res = await fetch(`/api/v1/applications/${encodeURIComponent(id)}/publish`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ note: note.trim() }),
    });
    rep = await res.json();
    if (res.ok && rep.deployed) {
      const n = (rep.definitions || []).length;
      const v = rep.release ? `v${rep.release.version}` : "";
      toast(`Published ${v} — ${n} definition${n === 1 ? "" : "s"}`, "ok");
      await reload();
      return;
    }
  } catch (e) {
    toast("publish failed: " + e.message, "err");
    return;
  }
  toast(rep.reason || rep.error || "Publish refused", "err");
  for (const r of rep.references || []) applyRefStatus(r.id, r);
}

// summarizeFromServer builds the per-process running/finished rollup the Instances
// overview renders from the server's lean instance summary (one row per definition,
// GET /api/v1/instances/summary), so the page never fetches and enriches every
// instance — the shape that made this page unreachable during a large-scale flood.
// Rows share a processId across versions, so counts are summed per processId.
function summarizeFromServer(rows) {
  const byProc = new Map();
  for (const r of rows) {
    if (!r.processId) continue; // orphaned instance (its definition was deleted)
    let s = byProc.get(r.processId);
    if (!s) { s = { running: 0, finished: 0, latestCompletedAt: 0 }; byProc.set(r.processId, s); }
    s.running += r.active;
    s.finished += r.completed;
    if (r.latestCompletedAt > s.latestCompletedAt) s.latestCompletedAt = r.latestCompletedAt;
  }
  return byProc;
}

// runningByDefinition keeps the per-*version* running counts the rollup above sums
// away. The overview only ever shows the process-level total, but migrating drains one
// deployed version onto another, so its picker has to say which version is actually
// holding instances — a choice the summed number cannot answer (ADR-0162).
function runningByDefinition(rows) {
  const byDef = new Map();
  for (const r of rows) byDef.set(String(r.processDefKey), r.active || 0);
  return byDef;
}

async function viewInstances() {
  view.innerHTML = `
    <div class="between">
      <h1>Instances</h1>
      <div class="row">
        <button class="btn" id="demo" title="Deploy a ready-made demo process with a task that parks a token">Deploy demo</button>
        <button class="btn neutral" id="refresh" title="Reload the instance list now">Refresh</button>
      </div>
    </div>
    <p class="muted">One row per deployed process. Open a process to pick a version, then
    watch all of its instances at once (every token on the diagram) or select a single
    instance to isolate it — with its variables shown below the diagram. Each instance
    listed under the diagram has a <b>&#9654; Replay</b> link to walk it step by step. A
    process with a stuck token is flagged in the <b>Incidents</b> column, which opens the
    version holding it. Start the demo to park a token on a waiting task.</p>
    <div class="ops-toolbar">
      <span class="ops-search">
        <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true"><circle cx="7" cy="7" r="4.5"/><path d="M11 11l3 3"/></svg>
        <input id="proc-filter" type="text" placeholder="Filter processes by name or ID…" aria-label="Filter processes" spellcheck="false"/>
      </span>
      <form class="ops-jump" id="inst-jump" title="Open a specific instance's replay by its key">
        <input id="inst-key" type="text" inputmode="numeric" placeholder="Instance key…" aria-label="Instance key" spellcheck="false"/>
        <button class="btn neutral ops-jump-go" type="submit" title="Open this instance's replay" aria-label="Open replay">&rarr;</button>
      </form>
    </div>
    <form class="ops-varsearch" id="var-search" title="Find instances by the content of their process variables">
      <span class="ops-search">
        <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true"><circle cx="7" cy="7" r="4.5"/><path d="M11 11l3 3"/></svg>
        <input id="var-q" type="text" placeholder="Search instances by variable — e.g. customerType=Business" aria-label="Search instances by variable" spellcheck="false" autocomplete="off"/>
      </span>
      <button class="btn" type="submit" title="Find instances whose process variables match">Search variables</button>
      <button class="btn ghost" type="button" id="var-clear" hidden title="Clear the variable search and its results">Clear</button>
    </form>
    <p class="muted var-hint" style="font-size:12px;margin:-4px 2px 12px">Contains <code>=</code> → structured <code>name=value</code> (name exact, value substring); otherwise free text across variable names and values.</p>
    <div id="var-panel" hidden></div>
    <div id="ops-inc-note"></div>
    <div class="card" id="proc-card" style="padding:0">
      <table data-dt-key="instances">
        <thead><tr><th>Process</th><th>Versions</th><th>Running</th><th>Incidents</th><th>Finished</th><th>Last activity</th><th></th></tr></thead>
        <tbody id="rows"><tr><td colspan="7" class="empty">Loading…</td></tr></tbody>
      </table>
    </div>`;
  const tbody = document.getElementById("rows");

  let allGroups = [];
  let summary = new Map();
  let runningByDef = new Map();
  // Unresolved incidents, bucketed for the two tables below: by definition (the
  // per-process rows) and by instance (the variable-search results). Keys are
  // stringified so a JSON number and a string key can't miss each other (ADR-0151).
  let incByDef = new Map();
  let incByInstance = new Map();
  let incTruncated = false;
  // Short and fixed-width-ish (dd.mm.yyyy hh:mm): an overview column wants the day and
  // the time, not seconds, and it must not wrap onto a second line. completedAt is ns.
  const fmtNano = (ns) => ns
    ? new Date(ns / 1e6).toLocaleString(undefined, {
      day: "2-digit", month: "2-digit", year: "numeric", hour: "2-digit", minute: "2-digit",
    })
    : "—";

  // loadIncidents pulls the server's unresolved incidents once per refresh. This
  // overview is server-wide, so it is the unscoped list — capped like the Incidents
  // page, and a capped page is said out loud rather than quietly undercounting. It is
  // deliberately a separate read: the summary endpoint is O(1) per definition by
  // design (ADR-0083) and must not grow a scan.
  const loadIncidents = async () => {
    try {
      const { data, headers } = await apiRaw("GET", "/api/v1/incidents");
      const rows = (data && data.incidents) || [];
      incTruncated = headers.get("X-Incidents-Truncated") === "true";
      incByDef = new Map();
      incByInstance = new Map();
      for (const r of rows) {
        if (r.processDefKey) {
          const d = String(r.processDefKey);
          incByDef.set(d, (incByDef.get(d) || 0) + 1);
        }
        const i = String(r.processInstanceKey);
        incByInstance.set(i, (incByInstance.get(i) || 0) + 1);
      }
    } catch { /* best-effort: the lists still render, just without the flags */ }
    const note = document.getElementById("ops-inc-note");
    if (note) {
      note.innerHTML = incTruncated
        ? `<p class="muted" style="font-size:12px;margin:0 2px 8px">More incidents than one page holds — the counts below are a lower bound. Work through them in <a href="#/operations/incidents">Incidents</a>.</p>`
        : "";
    }
  };

  // incidentCell renders one process row's Incidents cell: the total over every
  // version, linking to the version that actually holds them. Linking to the latest
  // version instead would land the operator on an empty diagram whenever the fault
  // sits on an older one — the case where this flag matters most.
  const incidentCell = (g) => {
    let total = 0, top = 0, topVersion = null, versionsWith = 0;
    for (const v of g.versions) {
      const n = incByDef.get(String(v.key)) || 0;
      if (!n) continue;
      total += n;
      versionsWith++;
      if (n > top) { top = n; topVersion = v; }
    }
    if (!total) return { total: 0, html: '<span class="muted">0</span>' };
    const title = versionsWith > 1
      ? `${total} unresolved incidents across ${versionsWith} versions — opens v${topVersion.version}, which holds ${top}; the version picker reaches the rest`
      : `${total} unresolved incident${total === 1 ? "" : "s"} on v${topVersion.version} — open its live view, where the diagram marks every stuck token`;
    return {
      total,
      html: `<a class="pill err" href="#/operations/p/${topVersion.key}" title="${esc(title)}">&#9888; ${total}</a>`,
    };
  };

  // renderRows draws the process rows, narrowed by the top filter box (name or process
  // id). Column sorting and per-column filtering are handled by the shared table
  // enhancer over the rendered rows, so this only builds the rows and their sort keys.
  function renderRows() {
    if (!allGroups.length) {
      tbody.innerHTML = `<tr><td colspan="7" class="empty">
        No processes deployed. Click <b>Deploy demo</b> above, or create one in the
        <a href="#/modeler">Modeler</a>.</td></tr>`;
      return;
    }
    const q = (document.getElementById("proc-filter").value || "").trim().toLowerCase();
    const filtered = q
      ? allGroups.filter((g) => ((g.latest.name || "") + " " + g.processId).toLowerCase().includes(q))
      : allGroups;
    if (!filtered.length) {
      tbody.innerHTML = `<tr><td colspan="7" class="empty">No processes match “${esc(q)}”.</td></tr>`;
      return;
    }
    tbody.innerHTML = filtered.map((g) => {
      const s = summary.get(g.processId) || { running: 0, finished: 0, latestCompletedAt: 0 };
      const label = g.latest.name || g.processId;
      const sub = g.latest.name
        ? `<div class="muted" style="font-size:12px">${esc(g.processId)}</div>` : "";
      const tag = g.latest.versionTag ? ` <span class="ver-tag" title="Version tag">${esc(g.latest.versionTag)}</span>` : "";
      // A deactivated definition stays deployed and keeps its running instances, but does
      // not auto-start new ones from its timer/message/signal start events (ADR-0119).
      // Flag it here too; the Activate/Deactivate toggle lives in the Modeler's Deployed list.
      const inactiveBadge = g.latest.active === false
        ? ` <span class="pill warn" title="Deployed but paused: no new instances auto-start from its timer, message, or signal start events">Inactive</span>`
        : "";
      // Compact and non-wrapping: the latest version reads at a glance, the count of
      // older ones is a small badge (the full phrasing lives in the tooltip), so the
      // cell can't break mid-phrase in a narrow column.
      const versions = (g.versions.length === 1
        ? `v${g.latest.version}`
        : `v${g.latest.version} <span class="ver-count" title="${g.versions.length} versions deployed · latest v${g.latest.version}">${g.versions.length}</span>`) + tag;
      const running = s.running
        ? `<span class="pill ok"><span class="dot"></span>${s.running}</span>`
        : '<span class="muted">0</span>';
      const collab = g.latest.collaborationKey
        ? `<a class="replay-link" href="#/operations/c/${g.latest.collaborationKey}" title="Replay the message flow between pools">⇄ Replay</a>`
        : "";
      // Row actions: one primary Open plus an overflow menu, so every row is the same
      // height and the destructive bulk-terminate doesn't shout from each row (it is
      // one click deeper, the same ⋯ pattern the Modeler's rows use).
      const openHref = `#/operations/p/${g.latest.key}`;
      const menuItems = [{ label: "Open", icon: "→", href: openHref }];
      if (s.running) {
        // Migrating sits above terminating deliberately: both drain a version, but one
        // keeps the work and the other discards it, and the one that keeps it should be
        // the one an operator reaches first (ADR-0162).
        if (g.versions.length > 1) {
          menuItems.push({ sep: true }, { label: "Migrate running instances…", icon: "⇄", act: "migrate", data: { proc: g.processId } });
        }
        menuItems.push(
          { sep: true },
          { label: "Terminate all running", icon: "⛔", act: "term", data: { proc: g.processId }, danger: true },
        );
      }
      // A running count says nothing about a token being *stuck*: an instance parked
      // behind an incident is counted as running like any other (ADR-0151).
      const inc = incidentCell(g);
      return `<tr>
        <td data-filter="${esc(label + " " + g.processId)}"><a href="#/operations/p/${g.latest.key}"><b>${esc(label)}</b></a>${inactiveBadge}${collab}${sub}</td>
        <td data-sort="${g.versions.length}">${versions}</td>
        <td data-sort="${s.running || 0}">${running}</td>
        <td data-sort="${inc.total}">${inc.html}</td>
        <td data-sort="${s.finished || 0}">${s.finished || '<span class="muted">0</span>'}</td>
        <td class="muted nowrap" data-sort="${s.latestCompletedAt || 0}">${esc(fmtNano(s.latestCompletedAt))}</td>
        <td class="row-actions"><a class="btn ghost" href="${openHref}">Open</a>${dropdown("⋯", "icon-btn", menuItems)}</td>
      </tr>`;
    }).join("");
  }

  // Bulk-terminate every running instance of a process straight from the overview —
  // the coarse "drain this process" action, no drilling into a version. It drains each
  // deployed version in bounded batches (the server caps per call, reports remaining).
  // Reached from the row's ⋯ menu, so it takes a confirm before it drains anything.
  onMenuAction(tbody, async (act, b) => {
    // Move a version's running instances onto another version rather than discarding
    // them — the answer to "the model was wrong" that keeps the work (ADR-0162).
    if (act === "migrate") {
      const g = allGroups.find((x) => x.processId === b.dataset.proc);
      if (!g) return;
      await migrateProcessFlow({
        api, toast,
        processId: g.processId,
        processName: g.latest.name || g.processId,
        versions: g.versions,
        // How many instances a version holds: the per-version running counts the
        // overview already read, so the picker says which one is worth draining.
        runningOf: (v) => runningByDef.get(String(v.key)) || 0,
        onDone: load,
      });
      return;
    }
    if (act !== "term") return;
    const proc = b.dataset.proc;
    const g = allGroups.find((x) => x.processId === proc);
    const s = summary.get(proc) || { running: 0 };
    if (!g || !s.running) return;
    if (!(await confirmTerminateAll(g.latest.name || g.processId, s.running))) return;
    try {
      let total = 0;
      for (const v of g.versions) {
        for (let guard = 0; guard < 1000; guard++) {
          const res = await api("POST", `/api/v1/processes/${v.key}/cancel-instances`);
          total += res.canceled || 0;
          if (!res.remaining) break;
        }
      }
      toast(`Terminated ${total} instance${total === 1 ? "" : "s"}`, "ok");
      await load();
    } catch (err) {
      toast("terminate failed: " + err.message, "err");
    }
  });

  const load = async () => {
    try {
      const [procs, rows] = await Promise.all([
        api("GET", "/api/v1/processes"),
        api("GET", "/api/v1/instances/summary"),
        loadIncidents(),
      ]);
      allGroups = groupByProcess(procs);
      summary = summarizeFromServer(rows);
      runningByDef = runningByDefinition(rows);
      renderRows();
    } catch (e) {
      tbody.innerHTML = `<tr><td colspan="7" class="empty">${esc(e.message)}</td></tr>`;
    }
  };
  document.getElementById("refresh").addEventListener("click", load);
  document.getElementById("proc-filter").addEventListener("input", renderRows);

  document.getElementById("inst-jump").addEventListener("submit", (e) => {
    e.preventDefault();
    const key = (document.getElementById("inst-key").value || "").trim();
    if (/^\d+$/.test(key)) location.hash = `#/operations/i/${key}`;
    else toast("Enter a numeric instance key", "err");
  });

  // Variable-content search: query the backend for instances whose variables
  // match, then render a per-instance results table (each matched variable
  // highlighted) in place of the per-process list. Clearing restores the list.
  const varPanel = document.getElementById("var-panel");
  const procCard = document.getElementById("proc-card");
  const varClear = document.getElementById("var-clear");
  const varInput = document.getElementById("var-q");
  // needleOf mirrors the server's parse: an "=" makes it structured (the value
  // side is the highlight needle); otherwise the whole query is the needle.
  const needleOf = (q) => {
    const i = q.indexOf("=");
    return (i >= 0 && q.slice(0, i).trim() ? q.slice(i + 1) : q).trim().toLowerCase();
  };
  const highlight = (s, needle) => {
    if (!needle) return esc(s);
    const i = s.toLowerCase().indexOf(needle);
    if (i < 0) return esc(s);
    return esc(s.slice(0, i)) + "<mark>" + esc(s.slice(i, i + needle.length)) + "</mark>" + esc(s.slice(i + needle.length));
  };
  const showList = () => {
    varPanel.hidden = true;
    varPanel.innerHTML = "";
    procCard.hidden = false;
    varClear.hidden = true;
  };
  const runVarSearch = async (q) => {
    q = q.trim();
    if (!q) { showList(); return; }
    procCard.hidden = true;
    varClear.hidden = false;
    varPanel.hidden = false;
    varPanel.innerHTML = `<div class="card"><div class="empty">Searching…</div></div>`;
    let rows;
    try {
      // Refresh the incident buckets with the search: these rows are individual
      // instances, and a stale flag on the surface an operator debugs from is worse
      // than the extra read.
      await loadIncidents();
      rows = await api("GET", "/api/v1/instances/search?q=" + encodeURIComponent(q));
    } catch (e) {
      varPanel.innerHTML = `<div class="card"><div class="empty">${esc(e.message)}</div></div>`;
      return;
    }
    if (!rows.length) {
      varPanel.innerHTML = `<div class="card"><div class="empty">No instance has a variable matching “${esc(q)}”.</div></div>`;
      return;
    }
    const needle = needleOf(q);
    const body = rows.map((r) => {
      const label = r.processId || `#${r.processDefKey}`;
      const tag = r.versionTag ? ` <span class="ver-tag" title="Version tag">${esc(r.versionTag)}</span>` : "";
      const state = r.state === "active"
        ? '<span class="pill ok"><span class="dot"></span>active</span>'
        : `<span class="pill">${esc(r.state)}</span>`;
      // A matched instance that is stuck says so here rather than only once opened —
      // "active" alone reads as healthy (ADR-0151).
      const incN = incByInstance.get(String(r.key)) || 0;
      const incFlag = incN
        ? ` <a class="pill err" href="#/operations/i/${r.key}" title="${esc(`${incN} unresolved incident${incN === 1 ? "" : "s"} — open the replay, where the stuck element is marked and can be resolved`)}">&#9888; ${incN}</a>`
        : "";
      const hits = (r.variables || []).map((v) =>
        `<div class="var-hit"><b>${esc(v.name)}</b> = ${highlight(v.value, needle)} <span class="var-kind">${esc(v.kind)}</span></div>`
      ).join("");
      return `<tr>
        <td><b>${esc(label)}</b>${tag}<div class="muted" style="font-size:12px">${esc(String(r.key))}</div></td>
        <td>v${r.version}</td>
        <td>${state}${incFlag}</td>
        <td class="muted" data-sort="${r.completedAt || r.createdAt || 0}">${esc(fmtNano(r.completedAt || r.createdAt))}</td>
        <td>${hits}</td>
        <td style="text-align:right"><a class="replay-link" href="#/operations/i/${r.key}">&#9654; Replay</a></td>
      </tr>`;
    }).join("");
    const capped = rows.length >= 200 ? ' <span class="muted">(showing first 200)</span>' : "";
    varPanel.innerHTML = `
      <p class="muted" style="font-size:12px;margin:0 2px 8px">${rows.length} instance${rows.length === 1 ? "" : "s"} matched${capped} · full scan</p>
      <div class="card" style="padding:0">
        <table class="var-results" data-dt-key="instance-search">
          <thead><tr><th>Process</th><th>Version</th><th>State</th><th>Started</th><th>Matched variable(s)</th><th></th></tr></thead>
          <tbody>${body}</tbody>
        </table>
      </div>`;
  };
  document.getElementById("var-search").addEventListener("submit", (e) => {
    e.preventDefault();
    runVarSearch(varInput.value);
  });
  varClear.addEventListener("click", () => { varInput.value = ""; showList(); varInput.focus(); });
  const demoBtn = document.getElementById("demo");
  demoBtn.addEventListener("click", async () => {
    demoBtn.disabled = true;
    try { await deployDemo(); }
    catch (e) { toast("demo failed: " + e.message, "err"); demoBtn.disabled = false; }
  });
  await load();
}

// viewDecisions is the Operations "Decisions" overview: one row per DMN decision
// that a deployed process references and/or that instances have evaluated — the
// decision counterpart of the one-row-per-process instances list (ADR-0066). It
// shows where each decision is used (its processes) and how heavily (evaluations
// and last-evaluated time), and links each evaluation-bearing process back to its
// live view so an operator can drill from "which decision" to "which instance".
async function viewDecisions() {
  view.innerHTML = `
    <div class="between">
      <h1>Decisions</h1>
      <button class="btn neutral" id="refresh" title="Reload the deployed decisions">Refresh</button>
    </div>
    <p class="muted">One row per deployed DMN decision. A decision appears once any
    deployed process references it — even before it has run — and its usage grows as
    instances evaluate it. Open a decision to inspect each evaluation — the exact
    inputs it saw, the outputs it produced, and the rule trace — or open a process to
    watch the instances that drove it.</p>
    <div class="card" style="padding:0">
      <table data-dt-key="decisions">
        <thead><tr><th>Decision</th><th>Evaluation</th><th>Used by</th><th>Evaluations</th><th>Last evaluated</th></tr></thead>
        <tbody id="rows"><tr><td colspan="5" class="empty">Loading…</td></tr></tbody>
      </table>
    </div>`;
  const tbody = document.getElementById("rows");
  // lastEvaluatedAt is unix nanoseconds; Date wants milliseconds.
  const fmtNano = (ns) => ns ? new Date(ns / 1e6).toLocaleString() : "—";

  const load = async () => {
    try {
      const decisions = await api("GET", "/api/v1/decisions/deployed");
      if (!decisions.length) {
        tbody.innerHTML = `<tr><td colspan="5" class="empty">
          No decisions deployed. Add a business rule task to a process in the
          <a href="#/modeler">Modeler</a> and deploy it.</td></tr>`;
        return;
      }
      tbody.innerHTML = decisions.map((d) => {
        const locus = d.local
          ? '<span class="pill"><span class="dot"></span>local</span>'
          : '<span class="pill muted">central</span>';
        const procs = (d.processes || []).length
          ? (d.processes || []).map((p) =>
              `<a href="#/operations/p/${p.key}">${esc(p.name || p.processId)}</a>`
            ).join(", ")
          : '<span class="muted">—</span>';
        const detail = `#/operations/decisions/${encodeURIComponent(d.decisionId)}`;
        const evals = d.evaluations
          ? `<a href="${detail}"><span class="pill ok"><span class="dot"></span>${d.evaluations}</span></a>`
          : '<span class="muted">0</span>';
        return `<tr>
          <td><a href="${detail}"><b>${esc(d.decisionId)}</b></a></td>
          <td>${locus}</td>
          <td>${procs}</td>
          <td>${evals}</td>
          <td class="muted" data-sort="${d.lastEvaluatedAt || 0}">${esc(fmtNano(d.lastEvaluatedAt))}</td>
        </tr>`;
      }).join("");
    } catch (e) {
      tbody.innerHTML = `<tr><td colspan="5" class="empty">${esc(e.message)}</td></tr>`;
    }
  };
  document.getElementById("refresh").addEventListener("click", load);
  await load();
}

// viewCallActivities is the per-server management view for call activities: one
// row per call activity across every deployed process, showing which process it
// calls, its version binding and variable propagation, whether the target is
// deployed here, and — the active part — a per-server target Override an operator
// edits inline (ADR-0076/0105). A caller may be deployed before its callee, so a
// call activity can sit "not deployed" (it would park at runtime); and an operator
// can redirect the target to another process, pin it to a version, or disable it on
// this server without touching the model. Overrides key on the called process id, so
// editing one affects every caller of that target — the Caller column shows exactly
// who that is.
async function viewCallActivities() {
  view.innerHTML = `
    <div class="between">
      <h1>Call activities</h1>
      <button class="btn neutral" id="refresh" title="Reload the call-activity list">Refresh</button>
    </div>
    <p class="muted">One row per call activity across the processes deployed on this
    server. A call activity starts another deployed process as a child instance; it
    <b>resolves</b> only when a process with the called id is deployed here. The
    <b>Override</b> reshapes resolution <b>on this server only</b>: <b>redirect</b> to
    another process, <b>pin</b> to a version, or <b>disable</b> (the call then waits).
    An override keys on the called process id, so it applies to every caller of that
    target listed here.</p>
    <div class="card" style="padding:0">
      <table data-dt-key="call-activities">
        <thead><tr><th>Caller</th><th>Element</th><th>Calls</th><th>Binding</th><th>Variables</th><th>Resolves to</th><th>Override</th></tr></thead>
        <tbody id="rows"><tr><td colspan="7" class="empty">Loading…</td></tr></tbody>
      </table>
    </div>`;
  const tbody = document.getElementById("rows");

  const load = async () => {
    try {
      const rows = await api("GET", "/api/v1/call-activities");
      if (!rows.length) {
        tbody.innerHTML = `<tr><td colspan="7" class="empty">
          No call activities deployed. Add a call activity to a process in the
          <a href="#/modeler">Modeler</a> and deploy it.</td></tr>`;
        return;
      }
      tbody.innerHTML = rows.map((r) => {
        const caller = `<a href="#/operations/p/${r.callerKey}">${esc(r.callerName || r.callerProcessId)}</a>`
          + ` <span class="muted">v${r.callerVersion}</span>`;
        const binding = `<span class="pill">${esc(r.binding)}</span>`
          + (r.multiInstance ? ' <span class="pill muted">multi-instance</span>' : "")
          + (r.loop ? ' <span class="pill muted">loop</span>' : "");
        // Propagation: "all" both ways is the Zeebe default; call it out only where a
        // direction is isolated (off), since that changes what crosses the boundary.
        const vars = (r.propagateAllParent && r.propagateAllChild)
          ? '<span class="muted">all in · all out</span>'
          : `${r.propagateAllParent ? "all in" : "mapped in"} · ${r.propagateAllChild ? "all out" : "mapped out"}`;
        const target = r.resolved
          ? `<a href="#/operations/p/${r.targetKey}"><span class="pill ok"><span class="dot"></span>${esc(r.targetName || r.calledProcessId)}</span></a>`
            + ` <span class="muted">v${r.targetVersion}</span>`
          : `<span class="pill warn"><span class="dot"></span>not deployed</span>`;
        return `<tr>
          <td>${caller}</td>
          <td style="font-family:ui-monospace,monospace">${esc(r.elementId)}</td>
          <td style="font-family:ui-monospace,monospace">${esc(r.calledProcessId)}</td>
          <td>${binding}</td>
          <td>${vars}</td>
          <td>${target}</td>
          <td>${overrideCell(r)}</td>
        </tr>`;
      }).join("");
    } catch (e) {
      tbody.innerHTML = `<tr><td colspan="7" class="empty">${esc(e.message)}</td></tr>`;
    }
  };

  // One delegated handler for every row's override picker (the tbody persists across
  // reloads; the rows inside it do not, so a per-row listener would leak). Redirect
  // and pin need a value, so they prompt; default clears the override.
  tbody.addEventListener("change", async (e) => {
    const sel = e.target.closest("select.ca-ov");
    if (!sel) return;
    const pid = sel.dataset.pid;
    const path = `/api/v1/call-activities/overrides/${encodeURIComponent(pid)}`;
    try {
      if (sel.value === "default") {
        await api("DELETE", path);
        toast(`Override cleared for ${pid}`, "ok");
      } else if (sel.value === "disable") {
        await api("PUT", path, { action: "disable" });
        toast(`Calls to ${pid} disabled on this server`, "ok");
      } else if (sel.value === "redirect") {
        const t = prompt(`Redirect all calls to “${pid}” to which process id?`, sel.dataset.target || "");
        if (t === null || !t.trim()) { await load(); return; }
        await api("PUT", path, { action: "redirect", targetProcessId: t.trim() });
        toast(`Calls to ${pid} → ${t.trim()}`, "ok");
      } else if (sel.value === "pin") {
        const v = prompt(`Pin calls to “${pid}” to which deployed version number?`, sel.dataset.version || "");
        if (v === null || !v.trim()) { await load(); return; }
        const n = parseInt(v, 10);
        if (!Number.isInteger(n) || n <= 0) { toast("Enter a positive version number", "warn"); await load(); return; }
        await api("PUT", path, { action: "pin", targetVersion: n });
        toast(`Pinned ${pid} to v${n}`, "ok");
      }
    } catch (err) {
      toast(err.message || "Override failed", "warn");
    }
    await load();
  });

  document.getElementById("refresh").addEventListener("click", load);
  await load();
}

// overrideCell renders a row's per-server target-override picker plus a pill
// describing the active override, if any (ADR-0105). The picker's data-* carry the
// current target/version so the redirect/pin prompts pre-fill sensibly.
function overrideCell(r) {
  const ov = r.override;
  const act = ov ? ov.action : "default";
  const opt = (val, label) => `<option value="${val}" ${act === val ? "selected" : ""}>${label}</option>`;
  const sel = `<select class="ca-ov" data-pid="${esc(r.calledProcessId)}"`
    + ` data-target="${esc((ov && ov.targetProcessId) || "")}" data-version="${(ov && ov.targetVersion) || ""}">`
    + opt("default", "Default (latest)")
    + opt("redirect", "Redirect…")
    + opt("pin", "Pin version…")
    + opt("disable", "Disabled")
    + `</select>`;
  let note = "";
  if (ov) {
    const label = ov.action === "redirect" ? `→ ${esc(ov.targetProcessId)}`
      : ov.action === "pin" ? `pin v${ov.targetVersion}`
        : "disabled";
    note = ` <span class="pill warn"><span class="dot"></span>${label}</span>`;
  }
  return sel + note;
}

// viewIncidents is the Operations "Incidents" view: every unresolved incident on
// this server — the operator "what's stuck" list (ADR-0061). Two shapes land here:
// a *job* incident (a service-task job whose retries ran out and parked) and a
// job-less *timer* incident (a boundary / event-subprocess timer whose FEEL schedule
// stopped resolving, ADR-0064/0111). Each row links to the stuck element on the live
// diagram and to that instance's replay (ADR-0151), and resolves in place over POST
// /incidents/{elementInstanceKey}/resolve: a job incident re-activates its job with a
// fresh retry budget; a timer incident re-arms the element against the instance's
// current variables (re-raising if it still fails). The list shares the task list's
// ceiling and flags a capped page the same way (X-Incidents-Truncated), newest scan
// order.
// incidentMenu is the row's non-primary actions, behind the ⋯ menu every other table
// in the console puts them behind. Resolving is the one action that belongs on the row;
// correcting the data and reconfiguring the integration are the two ways to make that
// resolve *work*, and they were three visible buttons until the third one pushed the
// table past the width of its card (ADR-0163). Behind the menu, a fourth way out costs
// no width at all.
function incidentMenu(r, i) {
  const items = [];
  // The modeler's own repair form leads, when the task named one (ADR-0169): it is the
  // most specific thing on offer — the fields whoever wrote the task said matter, rather
  // than the whole variable set. Most tasks name none, which is why the raw editor below
  // it never goes away.
  if (r.repairForm) {
    items.push({ label: "Repair…", icon: "⚑", act: "repair", data: { row: i } });
  }
  items.push({ label: "Fix variables…", icon: "✎", act: "fixvars", data: { row: i } });
  if (r.connector && r.connectorId) {
    items.push({ label: "Configure connector…", icon: "⚙", act: "fixconn", data: { row: i } });
  } else if (r.connector) {
    // Nothing to open — the model names a connector nobody configured, so the way out
    // is the Console, where one is created.
    items.push({ label: "Configure connector ↗", icon: "⚙", href: "#/console/connectors" });
  }
  return items;
}

async function viewIncidents() {
  view.innerHTML = `
    <div class="between">
      <h1>Incidents</h1>
      <button class="btn neutral" id="refresh" title="Reload the incident list">Refresh</button>
    </div>
    <p class="muted">Every unresolved incident on this server — where a token is
    stuck waiting for an operator (ADR-0061). A <b>job</b> incident is a service task
    whose retries ran out and parked; a <b>timer</b> incident is a recurring boundary
    or event-subprocess timer whose FEEL schedule stopped resolving (ADR-0111).
    <b>Resolve</b> re-activates the work: a parked job retries with the budget you
    grant, a timer re-arms against the instance's current variables — re-raising if it
    still fails.</p>
    <div id="inc-note"></div>
    <div class="card" style="padding:0">
      <table data-dt-key="incidents">
        <thead><tr><th>Instance</th><th>Element</th><th>Cause</th><th>Raised</th><th>Message</th><th></th></tr></thead>
        <tbody id="rows"><tr><td colspan="6" class="empty">Loading…</td></tr></tbody>
      </table>
    </div>`;
  const tbody = document.getElementById("rows");
  const note = document.getElementById("inc-note");
  let current = []; // the rendered page, so a Resolve click has the whole incident

  const load = async () => {
    try {
      const { data, headers } = await apiRaw("GET", "/api/v1/incidents");
      const rows = (data && data.incidents) || [];
      current = rows;
      note.innerHTML = headers.get("X-Incidents-Truncated") === "true"
        ? `<p class="muted">Showing the first ${rows.length}. Resolve some and refresh to see the rest.</p>`
        : "";
      if (!rows.length) {
        tbody.innerHTML = `<tr><td colspan="6" class="empty">No incidents — nothing is stuck.</td></tr>`;
        return;
      }
      tbody.innerHTML = rows.map((r, i) => {
        // The instance opens on the live diagram of its own version, with the token
        // (and now the incident badge) on the stuck element; ▶ replays it step by step.
        const inst = r.processDefKey
          ? `<a href="#/operations/p/${r.processDefKey}/i/${r.processInstanceKey}" title="Open this instance on its live diagram">${r.processInstanceKey}</a>
             <a class="replay-link" href="#/operations/i/${r.processInstanceKey}" title="Replay this instance step by step">&#9654;</a>`
          : `<span title="This instance's definition is no longer deployed">${r.processInstanceKey}</span>`;
        // The element is named by its diagram id — what the modeler and the diagram
        // call it; the element instance key (the resolve key) and the compiled index
        // ride along as a title for anyone cross-referencing the graph.
        const el = `<span style="font-family:ui-monospace,monospace" title="Element instance ${r.elementInstanceKey} · compiled element index #${r.elementIndex}">${esc(r.elementId || r.elementInstanceKey)}</span>`;
        const cause = `${incidentPill(r)}${r.jobKey ? ` <span class="muted" style="font-family:ui-monospace,monospace">${r.jobKey}</span>` : ""}`;
        return `<tr>
          <td>${inst}</td>
          <td>${el}</td>
          <td>${cause}</td>
          <td data-sort="${r.raisedAt || 0}">${esc(fmtRaised(r.raisedAt))}</td>
          <td>${esc(r.message || "—")}${incidentConnectorChip(r)}</td>
          <td class="row-actions">
            <button class="btn sm" data-resolve="${i}" title="Resolve this incident">Resolve…</button>
            ${dropdown("⋯", "icon-btn", incidentMenu(r, i))}</td>
        </tr>`;
      }).join("");
    } catch (e) {
      tbody.innerHTML = `<tr><td colspan="6" class="empty">${esc(e.message)}</td></tr>`;
    }
  };

  // One delegated handler for every row's Resolve button (the tbody persists across
  // reloads; the rows inside it do not, so a per-row listener would leak). The dialog
  // and the POST are the shared incident flow every surface uses (ADR-0151).
  tbody.addEventListener("click", async (e) => {
    const btn = e.target.closest("button[data-resolve], .dropdown-menu button[data-act]");
    if (!btn) return;
    // Both the row's primary button and its menu items carry the row index; the menu
    // items say *which* action in data-act.
    const incident = current[Number(btn.dataset.resolve ?? btn.dataset.row)];
    if (!incident) return;
    // Correcting the variables first is the other half of resolving: a retry alone
    // repeats whatever failed (ADR-0158). Reconfiguring the connector is the third
    // way, for when the message is about the integration and not the data (ADR-0160).
    // The repair form is the same correction as the first, through the fields the task's
    // author named rather than through raw JSON (ADR-0169).
    const act = btn.dataset.act;
    const changed = act === "repair"
      ? !!(await repairFormFlow({ api, toast, incident }))
      : act === "fixvars"
        ? !!(await fixVariablesFlow({ api, toast, incident }))
        : act === "fixconn"
          ? !!(await fixConnectorFlow({ api, toast, incident }))
          : await resolveIncidentFlow({ api, toast, incident });
    if (changed) {
      await load();
      refreshIncidentBadge(); // don't make the nav wait out its interval to agree
    }
  });

  document.getElementById("refresh").addEventListener("click", load);
  await load();
}

// viewMailOutbox is the Operations "Outbox" view: the messages a mail connector on
// the *preview* provider delivered in-server instead of sending (ADR-0150).
//
// It is what makes preview worth having. A first mail task can be modeled, run and
// read here before anyone owns a submission host or an OAuth bundle — and what is
// shown is not a paraphrase of the message but the very bytes the SMTP and Gmail
// providers would put on the wire, framed by the same code. So the headers, the MIME
// structure and the encoding are checkable here, which is precisely the part an author
// cannot verify by re-reading their own model.
//
// The HTML body is rendered in a sandboxed, script-less iframe: a mail body is
// composed from process variables, so it is untrusted markup by the same reasoning
// that keeps an uploaded SVG out of the DOM (ADR-0148).
// The Workers view (ADR-0157): who is doing the engine's out-of-process work, and
// what is waiting for someone to do it.
//
// The two tables only mean something read together. A job type's queue depth says
// how much is waiting; the workers table says whether anyone is taking it. Neither
// alone distinguishes "busy" from "abandoned", which is the question an operator
// actually arrives with — so the join is done here rather than left to the eye: a
// type with work queued, nothing in flight and no worker pulling it is drawn as a
// problem, and the worker that used to serve it is flagged for having gone quiet.
async function viewWorkers() {
  view.innerHTML = `
    <div class="between">
      <h1>Workers</h1>
      <span><button class="btn neutral" id="refresh" title="Reload the workers and queue depths now">Refresh</button></span>
    </div>
    <p class="muted">Who is doing the engine&rsquo;s work. A job type with a growing queue and no worker
      against it is the state worth catching here &mdash; counters cover this run of the server, while the
      queue depths come from durable state.</p>
    <div class="card wk-card" id="wk-collisions" hidden></div>
    <div class="card wk-card" id="wk-types"><p class="empty">Loading&hellip;</p></div>
    <div class="card wk-card" id="wk-workers"></div>
    <div class="card wk-card" id="wk-gaps" hidden></div>
    <div class="card wk-card" id="wk-supervised" hidden></div>`;

  const types = document.getElementById("wk-types");
  const workers = document.getElementById("wk-workers");
  const collisions = document.getElementById("wk-collisions");
  const gaps = document.getElementById("wk-gaps");
  const supervised = document.getElementById("wk-supervised");
  let showAllTypes = false;

  // A worker is counted as pulling a type while it holds one in flight, or while it
  // reported an outcome for it recently enough to still be at work. Anything else is
  // a type nobody is serving, which is what the red row says.
  const pullersOf = (list, type) => list.filter((w) => (w.types || {})[type] > 0);
  const fmtSeen = (ns) => {
    if (!ns) return "\u2014";
    const secs = Math.max(0, Math.round((Date.now() - ns / 1e6) / 1000));
    if (secs < 60) return `${secs}s ago`;
    if (secs < 3600) return `${Math.round(secs / 60)}m ago`;
    if (secs < 86400) return `${Math.round(secs / 3600)}h ago`;
    return new Date(ns / 1e6).toLocaleString();
  };
  // Past this a worker has almost certainly stopped rather than paused: the default
  // lease is five minutes, so anything beyond it has let its work go back on offer.
  const STALE_MS = 5 * 60 * 1000;
  const isStale = (w) => w.lastSeen && Date.now() - w.lastSeen / 1e6 > STALE_MS;

  // Whether the job-type table is unfolded. Default closed: the page's question is
  // "is anything unserved?", which the summary answers, and the table is the detail
  // behind it. try/catch because a locked-down browser can make storage throw.
  const typesOpen = () => {
    try { return localStorage.getItem("atlas.workers.types") === "1"; } catch { return false; }
  };
  const setTypesOpen = (open) => {
    try { localStorage.setItem("atlas.workers.types", open ? "1" : "0"); } catch { /* not storable */ }
  };

  const servedBy = (row, pullers) => {
    if (row.servedInProcess) {
      return `<span class="pill" title="Atlas works this type itself, so no external worker can lease it">in-process</span>`;
    }
    // A type nothing is *meant* to serve is not a type nobody *is* serving: a user
    // task waits for a person and the pull refuses it, so it must never be drawn as
    // an abandoned queue.
    if (!row.leasable) {
      return `<span class="pill vis" title="Not worker work — a user task is claimed by a person">people</span>`;
    }
    if (pullers.length) {
      return `<span class="pill ok"><span class="dot"></span>${pullers.length} worker${pullers.length === 1 ? "" : "s"}</span>`;
    }
    return `<span class="pill err"><span class="dot"></span>nobody</span>`;
  };

  const load = async () => {
    let data;
    try {
      data = await api("GET", "/api/v1/workers");
    } catch (e) {
      types.innerHTML = `<p class="empty">${esc(e.message)}</p>`;
      workers.innerHTML = "";
      gaps.hidden = true;
      collisions.hidden = true;
      return;
    }
    const allTypes = (data && data.types) || [];
    const workerRows = (data && data.workers) || [];
    // An engine knows two dozen built-in job types and a given installation uses two
    // of them. Listing every idle one buries the types someone actually deployed, so a
    // quiet built-in stays folded away until asked for.
    //
    // "Quiet" has to mean *unused*, which is what that sentence was always about — not
    // merely "nothing on the queue right now". A kind the engine serves itself has
    // nothing queued, nothing in flight and no puller (none may pull it), so on a
    // busy-only test a connector that is working perfectly was folded away and one that
    // was failing was not: it appeared when it broke and vanished when it was fixed. An
    // operator looking for their Jira worker found nothing on a page whose whole subject
    // is who is doing the work.
    //
    // A deployed process referencing the type is what says an installation uses it, so
    // that keeps a row visible too. It is the same signal the Processes column shows,
    // which is why that column had to start counting connector, script, rule and user
    // tasks before this could lean on it.
    const busy = (t) => t.parked > 0 || t.inFlight > 0 || t.incidents > 0 || pullersOf(workerRows, t.type).length > 0;
    const inUse = (t) => busy(t) || (t.processes || []).length > 0;
    const idleBuiltIns = allTypes.filter((t) => t.builtIn && !inUse(t));
    const typeRows = showAllTypes ? allTypes : allTypes.filter((t) => !idleBuiltIns.includes(t));
    const unserved = typeRows.filter(
      (t) => t.leasable && t.parked > 0 && !t.inFlight && !pullersOf(workerRows, t.type).length);

    types.innerHTML = `
      <details class="wk-fold" id="wk-types-fold"${typesOpen() ? " open" : ""}>
      <summary class="wk-head">
        <b><span class="wk-caret" aria-hidden="true">&#9656;</span> Job types</b>
        <span class="muted small">${typeRows.length} shown${unserved.length
          ? ` &middot; <span class="wk-unserved" title="${esc(`${unserved.length} job type${unserved.length === 1
            ? " has" : "s have"} work queued and nothing pulling it`)}">${unserved.length} unserved</span>` : ""}${
          idleBuiltIns.length ? ` &middot; <a href="#" id="wk-toggle-all">${showAllTypes
            ? "hide" : "show"} ${idleBuiltIns.length} idle built-in${idleBuiltIns.length === 1 ? "" : "s"}</a>` : ""}</span>
      </summary>
      ${typeRows.length ? `<table data-dt-key="wk-types">
        <thead><tr>
          <th>Type</th><th>Served by</th>
          <th class="wk-num">Queued</th><th class="wk-num">In flight</th><th class="wk-num">Incidents</th>
        </tr></thead>
        <tbody>${typeRows.map((row) => {
          const pullers = pullersOf(workerRows, row.type);
          const stuck = unserved.includes(row);
          // Which process is waiting on this type. Fifty "nobody" rows say nothing
          // until you can see that, so the link is in the row rather than a drill-in.
          const used = (row.processes || []).map((u) =>
            `<a href="#/operations/p/${u.processDefKey}" title="${esc(`${u.processId} v${u.version}`)}">${esc(u.name || u.processId)}</a>`).join(", ");
          return `<tr class="${stuck ? "wk-stuck" : ""}">
            <td data-filter="${esc(row.type + " " + (row.processes || []).map((u) => `${u.name} ${u.processId}`).join(" "))}">
              <b>${esc(row.type)}</b>
              ${used ? `<span class="wk-used">${used}</span>` : ""}
              ${stuck ? `<span class="wk-why">Work is queued and nothing is pulling this type</span>` : ""}</td>
            <td>${servedBy(row, pullers)}</td>
            <td class="wk-num ${stuck ? "wk-count-bad" : ""}">${row.parked}${row.truncated ? "+" : ""}</td>
            <td class="wk-num ${stuck ? "wk-count-bad" : ""}">${row.servedInProcess ? "&mdash;" : row.inFlight}</td>
            <td class="wk-num">${row.incidents
              ? `<a href="#/operations/incidents">${row.incidents}</a>` : `<span class="muted">0</span>`}</td>
          </tr>`;
        }).join("")}</tbody>
      </table>` : `<p class="empty">No job types yet &mdash; deploy a process with a service task.</p>`}
      <p class="wk-note">An <b>in-process</b> type is worked by Atlas itself and cannot be leased from
        outside; relocating it to a worker means turning that handler off.</p>
      </details>`;

    workers.innerHTML = `
      <div class="wk-head">
        <b>Workers</b>
        <span class="muted small">${workerRows.length} seen this run</span>
      </div>
      ${workerRows.length ? `<table data-dt-key="wk-workers">
        <thead><tr>
          <th>Worker</th><th>Pulls</th><th>Connectors held</th>
          <th class="wk-num">In flight</th><th class="wk-num">Pulled</th>
          <th class="wk-num">Completed</th><th class="wk-num">Failed</th><th class="wk-num">Last seen</th>
        </tr></thead>
        <tbody>${workerRows.map((w) => `<tr class="${isStale(w) ? "wk-stale" : ""}">
          <td><b><a href="#" class="wk-open" data-worker="${esc(w.worker)}"
            title="Show the jobs this worker ran">${w.worker ? esc(w.worker) : "(unnamed)"}</a></b></td>
          <td>${Object.keys(w.types || {}).sort()
            .map((t) => `<span class="pill-kv">${esc(t)}</span>`).join(" ") || `<span class="muted">&mdash;</span>`}</td>
          <td>${(w.connectors || []).map((c) => `<span class="pill-kv">${esc(c)}</span>`).join(" ")
            || `<span class="muted">&mdash;</span>`}</td>
          <td class="wk-num">${w.leased}</td>
          <td class="wk-num">${w.pulled}</td>
          <td class="wk-num">${w.completed}</td>
          <td class="wk-num ${w.failed ? "wk-count-bad" : "muted"}">${w.failed}</td>
          <td class="wk-num ${isStale(w) ? "wk-stale-at" : "muted"}">${esc(fmtSeen(w.lastSeen))}</td>
        </tr>`).join("")}</tbody>
      </table>` : `<p class="empty">No worker has pulled a job yet. A worker appears here the moment it
        leases its first job &mdash; point one at <span class="pill-kv">POST /api/v1/jobs/activate</span>
        and name the job type it serves.</p>`}
      <p class="wk-note">Counters are since this server started and are not restored on restart.
        <b>In flight</b> is what a worker holds a lease on right now. <b>Connectors held</b> is what a
        worker reports it has credentials for &mdash; only it knows, since Atlas holds none for a kind
        it has handed over. Open a worker\u2019s name for the jobs it ran.</p>`;

    // Opening a worker asks for its recent jobs. They are deliberately not part of the
    // polled payload: the variables in them are process data, the endpoint is
    // admin-only, and a view that refreshes every few seconds should not carry them.
    workers.querySelectorAll(".wk-open").forEach((a) => {
      a.addEventListener("click", (ev) => {
        ev.preventDefault();
        showWorkerJobs(a.dataset.worker || "");
      });
    });

  // showWorkerJobs opens one worker's recent jobs: what it leased, what it was handed,
  // what it returned, and what failed.
  //
  // The counters above say how much; this says which. A failure that still has retries
  // left raises no incident, so before this its message existed nowhere an operator
  // could reach — the whole reason the dialog carries the error text at all.
  //
  // It is a tail in the server's memory, not history: a restart empties it, and a busy
  // worker pushes older jobs out. The dialog says so rather than letting an operator
  // read an empty list as "nothing ran".
  async function showWorkerJobs(worker) {
    const ov = document.createElement("div");
    ov.className = "modal-ov";
    ov.innerHTML = `
      <div class="modal wkjobs-modal" role="dialog" aria-modal="true" aria-label="Worker jobs">
        <div class="modal-head">
          <h2>${worker ? esc(worker) : "(unnamed worker)"} \u2014 recent jobs</h2>
          <button type="button" class="icon-btn" data-x aria-label="Close" title="Close">\u2715</button>
        </div>
        <div class="modal-body" id="wkjobs-body"><p class="empty">Loading\u2026</p></div>
        <div class="modal-foot">
          <span class="muted small">The last jobs this worker leased, newest first. The top of the
            list is the server\u2019s memory \u2014 a restart empties it, and older jobs age out.
            Anything under \u201cEarlier\u201d comes from the configured job-history connector and
            outlives a restart. The durable account of the run itself stays the instance
            timeline.</span>
          <button type="button" class="btn" data-done title="Close this dialog">Done</button>
        </div>
      </div>`;
    document.body.appendChild(ov);
    const close = () => { ov.remove(); document.removeEventListener("keydown", onKey); };
    const onKey = (e) => { if (e.key === "Escape") close(); };
    document.addEventListener("keydown", onKey);
    ov.addEventListener("mousedown", (e) => { if (e.target === ov) close(); });
    ov.querySelector("[data-x]").addEventListener("click", close);
    ov.querySelector("[data-done]").addEventListener("click", close);

    const body = ov.querySelector("#wkjobs-body");
    let jobs = [];
    try {
      const out = await api("GET", `/api/v1/workers/${encodeURIComponent(worker)}/jobs`);
      jobs = (out && out.jobs) || [];
    } catch (e) {
      // The endpoint is admin-only, and "you may not see this" is a different answer
      // from "nothing ran here" — saying which is the point.
      body.innerHTML = `<p class="empty">${esc(String(e && e.message || e))}</p>`;
      return;
    }
    if (!jobs.length) {
      // Nothing in this run — but the history may still hold the failure someone came
      // here to read, so the section below is still asked for.
      body.innerHTML = `<p class="empty">No jobs recorded for this worker in this run.</p>`;
      await appendHistory(ov, worker);
      return;
    }
    renderRuns(body, jobs);
    await appendHistory(ov, worker);
  }

  // renderRuns draws a list of job runs. The ring and the clio history share it: they
  // are the same rows, and showing them differently would suggest they are not.
  function renderRuns(body, jobs) {
    // Variables are shown collapsed: the list is for scanning outcomes, and a row of
    // JSON per job would bury the one that failed.
    const vars = (label, text) => text
      ? `<details class="wkjob-vars"><summary>${label}</summary><pre>${esc(prettyJSON(text))}</pre></details>`
      : `<span class="muted small">${label}: none</span>`;
    body.innerHTML = `<div class="wkjob-list">${jobs.map((j) => {
      const took = j.settledAt && j.leasedAt
        ? `${Math.max(0, Math.round((j.settledAt - j.leasedAt) / 1e6))} ms` : "";
      return `<div class="wkjob wkjob-${esc(j.outcome.replace(/ /g, "-"))}">
        <div class="wkjob-head">
          <b>${esc(j.type)}</b>
          <span class="pill-kv">${esc(j.outcome)}</span>
          ${took ? `<span class="muted small">${esc(took)}</span>` : ""}
          ${j.elementId ? `<span class="muted small">${esc(j.elementId)}</span>` : ""}
          ${j.processInstanceKey
            ? `<a href="#/operations/i/${j.processInstanceKey}" title="Open the process instance">instance ${j.processInstanceKey}</a>`
            : ""}
        </div>
        ${j.error ? `<div class="wkjob-err">
          <div class="wkjob-attempts ${j.retries > 0 ? "" : "wkjob-parked"}">${j.retries > 0
            ? `${j.retries} ${j.retries === 1 ? "attempt" : "attempts"} left`
            : "No attempts left \u2014 this one has parked"}</div>
          <pre>${esc(j.error)}</pre></div>` : ""}
        <div class="wkjob-io">${vars("Handed in", j.in)}${vars("Returned", j.out)}</div>
      </div>`;
    }).join("")}</div>`;
  }

  // appendHistory adds what the configured clio connector holds, under the ring.
  //
  // It is a second request rather than part of the first because it reaches another
  // service: a dialog that waited on clio before showing anything would make the ring
  // — which is always there — hostage to a store that may not be. So the memory tail
  // renders first, and the history arrives under it or says why it did not.
  async function appendHistory(ov, worker) {
    const body = ov.querySelector("#wkjobs-body");
    if (!body) return;
    const box = document.createElement("div");
    box.className = "wkjob-history";
    box.innerHTML = `<div class="wkjob-history-head muted small">Loading the history\u2026</div>`;
    body.appendChild(box);

    let out;
    try {
      out = await api("GET", `/api/v1/workers/${encodeURIComponent(worker)}/history`);
    } catch (e) {
      box.innerHTML = `<div class="wkjob-history-head muted small">History unavailable: ${esc(String(e && e.message || e))}</div>`;
      return;
    }
    if (!out || !out.configured) {
      // Not a failure: this server was not asked to keep one. Naming the flag is more
      // use than an empty section that reads as "nothing ever ran".
      box.innerHTML = `<div class="wkjob-history-head muted small">${esc((out && out.note) || "No job history is configured on this server.")}</div>`;
      return;
    }
    const jobs = out.jobs || [];
    const head = `<div class="wkjob-history-head">
      <b>Earlier, from ${esc(out.connector)}</b>
      <span class="muted small">${jobs.length} entr${jobs.length === 1 ? "y" : "ies"}${
        out.scope === "failed" ? ", failures only" : ""}${
        out.truncated ? " \u2014 more than this window holds; query clio directly for the rest" : ""}${
        out.dropped ? ` \u2014 ${out.dropped} dropped while clio was slow or unreachable` : ""}</span>
    </div>`;
    if (!jobs.length) {
      box.innerHTML = head + `<p class="empty">Nothing in the history for this worker yet.</p>`;
      return;
    }
    const list = document.createElement("div");
    // The stored rows carry the same field names the ring does, so the same renderer
    // draws them. Only `in`/`out` arrive as text either way, which is why the ring
    // stores them as text in the first place.
    renderRuns(list, jobs);
    box.innerHTML = head;
    box.appendChild(list.firstElementChild);
  }

  // prettyJSON re-indents the stored JSON text for reading, and leaves it alone when it
  // will not parse — a clipped value is still worth showing as the text it is.
  function prettyJSON(text) {
    try { return JSON.stringify(JSON.parse(text), null, 2); } catch { return text; }
  }

    // Job types whose stored index a built-in has since taken. This is a data-directory
    // condition rather than a worker one, but it belongs where job types are shown, and
    // it has to be visible somewhere other than a container log: the server warns at
    // startup and `atlas check-job-types` answers offline, and neither reaches someone
    // running the engine on a machine they would rather not shell into.
    const collided = (data && data.jobTypeCollisions) || [];
    if (collided.length) {
      collisions.hidden = false;
      collisions.innerHTML = `
        <div class="wk-head"><b>Job types on an index a built-in has taken</b>
          <span class="muted small">${collided.length} type${collided.length === 1 ? "" : "s"}</span></div>
        <table class="no-enhance">
          <thead><tr><th>Job type</th><th class="wk-num">Index</th><th>That index now means</th></tr></thead>
          <tbody>${collided.map((c) => `<tr class="wk-stuck">
            <td><b>${esc(c.name)}</b></td>
            <td class="wk-num">${c.index}</td>
            <td><span class="pill-kv">${esc(c.nowMeans)}</span></td>
          </tr>`).join("")}</tbody>
        </table>
        <p class="wk-note">These types were given their index before a built-in connector claimed it.
          Jobs already parked under it still carry it, so the engine would hand them to the built-in
          named above, while new jobs of the same type get a fresh index. Nothing is lost yet and the
          server runs normally &mdash; but do not treat these queues as healthy, and say so before the
          next deploy.</p>`;
    } else {
      collisions.hidden = true;
      collisions.innerHTML = "";
    }

    // Connectors nothing can serve. This is the gap handing a credential to a worker
    // opens up: Atlas used to refuse an unconfigured connector when it held every
    // credential itself, and for a kind it has handed over it no longer can. The
    // engine knows which names models ask for and the workers report which they hold;
    // only here do the two halves meet, which is why this is worth a card of its own
    // rather than a column somewhere.
    //
    // Every row names its version, because the deployment behind it need not be the
    // process's current one: an instance can still be running on a superseded version,
    // and a call activity can be pinned to one. Without the version the row reads as
    // "your current model is broken" about a model whose current version is fine.
    const missing = (data && data.unservedConnectors) || [];
    if (missing.length) {
      gaps.hidden = false;
      gaps.innerHTML = `
        <div class="wk-head"><b>Connectors nothing can serve</b>
          <span class="muted small">${missing.length} name${missing.length === 1 ? "" : "s"}</span></div>
        <table class="no-enhance">
          <thead><tr><th>Connector</th><th>Kind</th><th>Used by</th></tr></thead>
          <tbody>${missing.map((m) => `<tr class="wk-stuck">
            <td><b>${esc(m.name)}</b></td>
            <td><span class="pill-kv">${esc(m.jobType)}</span></td>
            <td>${(m.processes || []).map((p) => `<a href="#/operations/p/${p.processDefKey}" title="${
              esc(`${p.processId} v${p.version}`)}">${esc(p.name || p.processId)} <span class="ver-of">v${p.version}</span></a>`).join(", ")
              || `<span class="muted">&mdash;</span>`}</td>
          </tr>`).join("")}</tbody>
        </table>
        <p class="wk-note">These models name a connector, and neither Atlas nor any worker seen this run
          holds a configuration for it &mdash; so their tasks will park. Configure the name on a worker
          that serves this kind, or point the model at one that is configured. A worker that has not
          polled yet reports nothing, so a name may clear itself on its first poll. Only versions that
          can still create a job are counted: each process&rsquo;s current one, plus any an instance is
          running on or a call activity is pinned to &mdash; a version you superseded is not one of them.</p>`;
    } else {
      gaps.hidden = true;
      gaps.innerHTML = "";
    }

    // Supervised workers: the ones Atlas launched itself, and the only ones it can
    // restart or show logs for. A worker running in someone else's cluster gets the
    // counters above and nothing more — the view says so rather than offering a
    // button that would silently do nothing.
    const sup = (data && data.supervised) || [];
    if (sup.length) {
      supervised.hidden = false;
      supervised.innerHTML = `
        <div class="wk-head"><b>Supervised by this server</b>
          <span class="muted small">${sup.length} process${sup.length === 1 ? "" : "es"}</span></div>
        <table class="no-enhance">
          <thead><tr><th>Worker</th><th>Serves</th><th>State</th><th class="wk-num">PID</th>
            <th class="wk-num">Starts</th><th></th></tr></thead>
          <tbody>${sup.map((c) => `<tr class="${c.state === "failed" ? "wk-stuck" : ""}">
            <td><b>${esc(c.id)}</b>${c.lastExit
              ? `<span class="wk-why">${esc(c.lastExit)}</span>` : ""}</td>
            <td>${(c.kinds || []).map((k) => `<span class="pill-kv">${esc(k)}</span>`).join(" ")}</td>
            <td><span class="pill ${c.state === "running" ? "ok" : c.state === "failed" ? "err" : "warn"}">
              <span class="dot"></span>${esc(c.state)}</span></td>
            <td class="wk-num">${c.pid || "&mdash;"}</td>
            <td class="wk-num">${c.starts}</td>
            <td class="row-actions">
              ${(c.log || []).length ? `<button class="btn neutral sm" data-log="${esc(c.id)}" title="Show this worker’s recent log">Log</button>` : ""}
              <button class="btn neutral sm" data-restart="${esc(c.id)}" title="Restart this supervised worker process">&#8635; Restart</button></td>
          </tr>${(c.log || []).length ? `<tr class="wk-log-row" data-dt-detail data-log-for="${esc(c.id)}" hidden>
            <td colspan="6"><pre class="wk-log">${esc((c.log || []).join("\n"))}</pre></td></tr>` : ""}`).join("")}
          </tbody></table>`;
      for (const b of supervised.querySelectorAll("[data-restart]")) {
        b.onclick = async () => {
          b.disabled = true;
          try {
            await api("POST", `/api/v1/workers/${encodeURIComponent(b.dataset.restart)}/restart`);
            toast("Restarting " + b.dataset.restart);
          } catch (e) { toast(e.message, "err"); }
          setTimeout(load, 400);
        };
      }
      for (const b of supervised.querySelectorAll("[data-log]")) {
        b.onclick = () => {
          const row = supervised.querySelector(`[data-log-for="${CSS.escape(b.dataset.log)}"]`);
          if (row) row.hidden = !row.hidden;
        };
      }
    } else {
      supervised.hidden = true;
      supervised.innerHTML = "";
    }

    const toggle = document.getElementById("wk-toggle-all");
    if (toggle) {
      // The link sits inside the <summary>, where a click would also fold the card.
      toggle.onclick = (e) => { e.preventDefault(); e.stopPropagation(); showAllTypes = !showAllTypes; load(); };
    }
    const fold = document.getElementById("wk-types-fold");
    if (fold) fold.addEventListener("toggle", () => setTypesOpen(fold.open));
    enhanceViewTables();
  };

  document.getElementById("refresh").onclick = load;
  await load();
}

async function viewMailOutbox() {
  view.innerHTML = `
    <div class="between">
      <h1>Outbox</h1>
      <span>
        <button class="btn neutral" id="refresh" title="Reload the outbox">Refresh</button>
        <button class="btn ghost danger" id="clear" title="Delete all messages from the preview outbox">Empty outbox</button>
      </span>
    </div>
    <p class="muted">Messages a mail connector using the <b>preview</b> provider
    delivered here instead of sending them (ADR-0150) — the zero-configuration way to
    see what a mail task actually produces, before a real provider exists. The message
    is framed by the same code that sends over SMTP or the Gmail API, so what you read
    here is what would go out. Nothing here was ever delivered to a recipient, and the
    outbox is memory only: it holds the newest messages and empties on restart.</p>
    <div class="card" id="ob-list"><p class="empty">Loading…</p></div>`;
  const list = document.getElementById("ob-list");
  const fmtNano = (ns) => ns ? new Date(ns / 1e6).toLocaleString() : "—";
  const addrs = (a) => (a || []).join(", ");

  const load = async () => {
    let data;
    try {
      data = await api("GET", "/api/v1/mail/outbox");
    } catch (e) {
      list.innerHTML = `<p class="empty">${esc(e.message)}</p>`;
      return;
    }
    const msgs = (data && data.messages) || [];
    if (!msgs.length) {
      list.innerHTML = `<p class="empty">Nothing here yet. Add a mail connector with the
        <b>Preview</b> provider under Organization &rsaquo; Connectors, point a mail task at it,
        and every message it sends lands here.</p>`;
      return;
    }
    list.innerHTML = (data.truncated
      ? `<p class="muted" style="margin:10px 12px 0">Older messages have been dropped — the outbox keeps the newest ones.</p>`
      : "") + msgs.map((m) => `<details class="ob-msg">
        <summary>
          <b>${esc(m.subject || "(no subject)")}</b>
          <span class="muted">· to ${esc(addrs(m.to)) || "—"}</span>
          <span class="muted">· ${esc(fmtNano(m.at))}</span>
          <span class="pill">${esc(m.connector || "?")}</span>
        </summary>
        <div class="ob-body">
          <div class="ob-head">
            <div><b>From</b> ${esc(m.from || "—")}</div>
            <div><b>To</b> ${esc(addrs(m.to)) || "—"}</div>
            ${m.cc && m.cc.length ? `<div><b>Cc</b> ${esc(addrs(m.cc))}</div>` : ""}
            ${m.bcc && m.bcc.length ? `<div><b>Bcc</b> ${esc(addrs(m.bcc))} <span class="muted">(never written into a header)</span></div>` : ""}
            ${m.messageId ? `<div><b>Message-ID</b> <span class="chip">${esc(m.messageId)}</span></div>` : ""}
          </div>
          ${m.body ? `<h3>Text</h3><pre class="ob-pre">${esc(m.body)}</pre>` : ""}
          ${m.html ? `<h3>HTML</h3><iframe class="ob-html" sandbox="" srcdoc="${esc(m.html)}" title="Rendered HTML body"></iframe>` : ""}
          <h3>Source</h3><pre class="ob-pre">${esc(m.raw || "")}</pre>
        </div>
      </details>`).join("");
  };

  document.getElementById("refresh").addEventListener("click", load);
  document.getElementById("clear").addEventListener("click", async () => {
    if (!confirm("Empty the preview outbox? Nothing here was ever sent.")) return;
    try {
      await api("DELETE", "/api/v1/mail/outbox");
      toast("Outbox emptied", "ok");
    } catch (e) { toast(e.message || "Could not empty the outbox", "warn"); }
    await load();
  });
  await load();
}

// viewADMockDirectory is the Operations "Mock directory" view: what the Active
// Directory workers running in mockup mode actually hold
// (ADR-0213).
//
// It is here because the Console used to show two things about a mocked directory and
// neither of them was the directory. The seed card under Organization > Workers holds
// the entries every forest *starts* from and is never written back, so an account a
// joiner created was not in it — reasonably mistaken for the directory all the same,
// since it was the only directory-shaped thing on screen. The other was the worker's
// log, one line per operation, which says what was asked for and not what is there.
//
// So this view is the directory: one card per worker, one tree per forest, the
// attributes of every entry, and the operation journal underneath. None of it is
// durable on either side — the forest dies with its worker, the report dies with this
// server — which the page says in as many words, because a view that looks like a
// database is how a mockup gets trusted with something it should not be.
async function viewADMockDirectory() {
  const gen = navGen;
  view.innerHTML = `
    <div class="between">
      <h1>Mock directory</h1>
      <span class="row" style="gap:12px; align-items:center">
        <label class="field inline" style="margin:0"><input type="checkbox" id="admockdir-follow" checked> Auto-refresh</label>
        <button class="btn neutral" id="admockdir-refresh" title="Reload what the workers have reported">Refresh</button>
      </span>
    </div>
    <p class="muted">What the Active Directory workers in <b>mockup mode</b> hold right now — the
    forests they simulate in their own memory, one per LDAP URL a task dialled. Nothing here
    reached a domain controller. It is not the <b>starting entries</b> under Organization &rsaquo;
    Workers: those are what every forest begins from and are never written back, so an account a
    process created appears here and not there. A worker restart empties its forests and they
    start from the seed again.</p>
    <div id="admockdir-list"><p class="empty">Loading…</p></div>`;
  const list = document.getElementById("admockdir-list");
  const fmtNano = (ns) => ns ? new Date(ns / 1e6).toLocaleString() : "—";

  // splitDN breaks a DN into its RDNs, honouring the backslash escape — "cn=Meier\,
  // Ada,ou=users" is two components and not three, and a tree that got that wrong
  // would file an account under a container named after half its own name.
  const splitDN = (dn) => {
    const out = [];
    let cur = "";
    for (let i = 0; i < dn.length; i++) {
      const ch = dn[i];
      if (ch === "\\" && i + 1 < dn.length) { cur += ch + dn[++i]; continue; }
      if (ch === ",") { out.push(cur.trim()); cur = ""; continue; }
      cur += ch;
    }
    out.push(cur.trim());
    return out.filter((p) => p !== "");
  };

  // treeOf builds the containment tree from the DNs alone: the mock accepts an entry
  // whose parent does not exist (it has no schema and demanding a seeded OU chain
  // would cost every trial run a fixture), so a container is often implied by its
  // children rather than being an entry of its own. Such a node renders as a plain
  // label with no attributes, which is exactly what it is.
  const treeOf = (entries) => {
    const root = { children: new Map() };
    for (const e of entries || []) {
      const parts = splitDN(e.dn || "").reverse(); // dc=com, dc=example, ou=users, cn=Arno
      let node = root;
      for (const part of parts) {
        const key = part.toLowerCase();
        if (!node.children.has(key)) node.children.set(key, { rdn: part, children: new Map() });
        node = node.children.get(key);
      }
      node.entry = e;
    }
    return root;
  };

  const attrTable = (attrs) => {
    const names = Object.keys(attrs || {}).sort((a, b) => a.localeCompare(b));
    if (!names.length) return `<p class="muted" style="margin:6px 0 0">No attributes.</p>`;
    return `<table class="admock-attrs"><tbody>${names.map((n) => `<tr>
      <th>${esc(n)}</th><td>${(attrs[n] || []).map((v) => `<code>${esc(v)}</code>`).join("<br>")}</td>
    </tr>`).join("")}</tbody></table>`;
  };

  const renderNode = (node) => {
    const kids = [...node.children.values()].sort((a, b) => a.rdn.localeCompare(b.rdn));
    const inner = kids.length ? `<ul class="admock-tree">${kids.map(renderNode).join("")}</ul>` : "";
    if (!node.entry) {
      // Implied container: no entry of its own, so nothing to open.
      return `<li><span class="admock-node muted">${esc(node.rdn)}</span>${inner}</li>`;
    }
    const attrs = node.entry.attributes || {};
    const count = Object.keys(attrs).length;
    // data-k is the entry's identity across renders: what you opened stays open when
    // the view refreshes under you. The DN is unique within a forest, and the forest
    // and worker are prefixed by the callers below.
    return `<li><details class="admock-entry" data-k="e|${esc(node.entry.dn || "")}">
        <summary><span class="admock-node">${esc(node.rdn)}</span>
          <span class="muted">· ${count} attribute${count === 1 ? "" : "s"}</span></summary>
        <div class="admock-detail">
          <div class="muted admock-dn">${esc(node.entry.dn || "")}</div>
          ${attrTable(attrs)}
        </div>
      </details>${inner}</li>`;
  };

  const renderForest = (f) => {
    const entries = f.entries || [];
    const tree = treeOf(entries);
    const body = entries.length
      ? `<ul class="admock-tree admock-root">${[...tree.children.values()]
          .sort((a, b) => a.rdn.localeCompare(b.rdn)).map(renderNode).join("")}</ul>`
      : `<p class="empty">This forest is empty — nothing has been created in it yet.</p>`;
    return `<div class="admock-forest" data-forest="${esc(f.url || "?")}">
      <div class="between">
        <h3><code>${esc(f.url || "?")}</code></h3>
        <span class="muted">${f.held || 0} entr${(f.held || 0) === 1 ? "y" : "ies"}</span>
      </div>
      ${f.truncated ? `<p class="muted">Showing the first ${entries.length} of ${f.held} —
        a forest this large is past what this view carries.</p>` : ""}
      ${body}
    </div>`;
  };

  const renderWorker = (w) => {
    const forests = w.forests || [];
    const ops = w.operations || [];
    // The whole worker is one collapsible card: a mockup run against several forests
    // is a lot of screen, and an operator watching two workers wants to put one away
    // without losing it. Open by default — a card you have to open to learn there is
    // anything in it is the state this view exists to end.
    return `<div class="card admock-worker" data-worker="${esc(w.worker || "?")}">
      <details class="admock-card" data-k="card" open>
        <summary>
          <span class="admock-card-head">
            <h2>${esc(w.worker || "?")}</h2>
            <span class="muted">${w.seeded || 0} starting entr${(w.seeded || 0) === 1 ? "y" : "ies"} ·
            ${forests.length} forest${forests.length === 1 ? "" : "s"} dialled</span>
          </span>
          <span class="admock-card-state">
            <span class="pill warn"><span class="dot"></span>mockup</span>
            <span class="muted" style="margin-left:8px">reported ${esc(fmtNano(w.at))}</span>
          </span>
        </summary>
        ${forests.length ? forests.map(renderForest).join("")
          : `<p class="empty">No directory dialled yet. This worker is in mockup mode and has served
             no Active Directory task since it started — the first one creates the forest it names.</p>`}
        ${ops.length ? `<details class="admock-ops" data-k="ops"><summary>${ops.length} operation${ops.length === 1 ? "" : "s"}</summary>
          <table class="admock-attrs"><tbody>${ops.map((o) => `<tr>
            <th>${esc(o.op || "")}</th>
            <td><code>${esc(o.dn || "")}</code>${o.detail ? ` <span class="muted">${esc(o.detail)}</span>` : ""}</td>
          </tr>`).reverse().join("")}</tbody></table></details>` : ""}
      </details>
    </div>`;
  };

  // openKeys is which entries the reader has expanded, addressed by worker, forest and
  // DN rather than by position — a refresh must not close what somebody is reading, and
  // an entry that moved because a sibling was created is still the same entry.
  const keyOf = (d) => {
    const w = d.closest("[data-worker]"), f = d.closest("[data-forest]");
    return `${w ? w.dataset.worker : ""}|${f ? f.dataset.forest : ""}|${d.dataset.k || ""}`;
  };
  const foldState = () => {
    const state = new Map();
    for (const d of list.querySelectorAll("details")) state.set(keyOf(d), d.open);
    return state;
  };
  // Both directions are remembered, which is why this is a map and not a set of open
  // keys: a card is open by default, so "closed" is a decision too. A section the
  // reader has never seen — a worker reporting for the first time — is not in the map
  // and keeps whatever the markup says.
  const refold = (state) => {
    for (const d of list.querySelectorAll("details")) {
      const was = state.get(keyOf(d));
      if (was !== undefined) d.open = was;
    }
  };

  // The last payload rendered. A poll that brings nothing new leaves the DOM alone
  // entirely: the view refreshes every few seconds, and rebuilding an identical tree
  // would cost the reader their place — and their open entries — for no news at all.
  let rendered = null;
  const load = async () => {
    let data;
    try {
      data = await api("GET", "/api/v1/ad/mock-directory");
    } catch (e) {
      rendered = null; // re-render once it answers again, whatever it says
      list.innerHTML = `<p class="empty">${esc(e.message)}</p>`;
      return;
    }
    const fresh = JSON.stringify(data);
    if (fresh === rendered) return;
    const folds = foldState();
    rendered = fresh;
    const workers = (data && data.workers) || [];
    if (!workers.length) {
      list.innerHTML = `<div class="card"><p class="empty">No worker has reported a mock directory.
        Switch <b>Serve Active Directory tasks against a mockup</b> on under Organization &rsaquo;
        Workers; the worker restarts and reports itself, and every AD task it then serves shows up
        here.</p></div>`;
      return;
    }
    list.innerHTML = workers.map(renderWorker).join("");
    refold(folds);
  };

  await load();
  if (superseded(gen)) return;
  document.getElementById("admockdir-refresh").addEventListener("click", load);
  const follow = document.getElementById("admockdir-follow");
  // Five seconds: a mockup run is something you watch while clicking through a
  // process, and the read is a small in-memory list on a server that is not the
  // engine's run loop.
  const timer = setInterval(() => { if (follow.checked) load(); }, 5000);
  window.__atlasCleanup = () => clearInterval(timer);
}

// viewSQLMockJournal is the Operations "Mock database" view: what the SQL workers in
// mockup mode were actually asked (ADR-0224).
//
// It is the mock directory's sibling with one difference that decides its whole shape.
// That view answers "what is in the directory now" — state, so it draws a tree. This
// mock holds no state at all: it answers statements and executes nothing, so an INSERT
// changes nothing a later SELECT would see. There is no "now" to draw. What there is,
// is the sequence — and the sequence is the answer to the question a mockup run is made
// to ask: what did my process do?
//
// The refusals are the entries that matter most. A statement nobody seeded fails and
// names itself and its values, and that is how an operator builds the seed: read it
// here, paste it into the card under Workers.
async function viewSQLMockJournal() {
  const gen = navGen;
  view.innerHTML = `
    <div class="between">
      <h1>Mock database</h1>
      <span class="row" style="gap:12px; align-items:center">
        <label class="field inline" style="margin:0"><input type="checkbox" id="sqlmock-follow" checked> Auto-refresh</label>
        <button class="btn neutral" id="sqlmock-refresh" title="Reload what the workers have reported">Refresh</button>
      </span>
    </div>
    <p class="muted">Every statement the SQL workers in <b>mockup mode</b> have been asked, newest
    last. Nothing here reached a database. A statement shown in red had <b>no prepared answer</b> —
    it failed, and it names itself and the values it was given so you can paste it into the
    <b>Databases</b> card under Organization &rsaquo; Workers. There is no table to browse: this
    mockup answers statements and executes none, so an <code>INSERT</code> does not change what a
    later <code>SELECT</code> returns. A worker restart empties its journal.</p>
    <div id="sqlmock-list"><p class="empty">Loading…</p></div>`;
  const list = document.getElementById("sqlmock-list");
  const fmtNano = (ns) => ns ? new Date(ns / 1e6).toLocaleString() : "—";

  // A bound value, as it goes to the driver. Rendered as JSON so a string "7" and the
  // number 7 are distinguishable — which is the whole question when a lookup found
  // nobody, and the reason this view carries the values at all.
  const val = (v) => `<code>${esc(JSON.stringify(v))}</code>`;
  const boundList = (st) => {
    const named = st.named || null;
    if (named && Object.keys(named).length) {
      return Object.keys(named).sort().map((k) => `${esc(k)} = ${val(named[k])}`).join(", ");
    }
    const params = st.params || [];
    if (!params.length) return "";
    // Numbered the way the statement addresses them, so the reader can line the value
    // up with the placeholder rather than counting commas.
    return params.map((p, i) => `<span class="muted">${i + 1}</span> ${val(p)}`).join(", ");
  };

  const renderStatement = (st) => {
    const bound = boundList(st);
    return `<tr class="${st.failed ? "sqlmock-failed" : ""}">
      <td class="sqlmock-seq muted">${esc(st.seq || "")}</td>
      <td>
        <code class="sqlmock-stmt">${esc(st.statement || "")}</code>
        ${bound ? `<div class="muted sqlmock-bound">${bound}</div>` : ""}
        ${st.failed && st.detail ? `<div class="sqlmock-detail">${esc(st.detail)}</div>` : ""}
      </td>
    </tr>`;
  };

  const renderWorker = (w) => {
    const sts = w.statements || [];
    const failed = sts.filter((s) => s.failed).length;
    return `<div class="card sqlmock-worker" data-worker="${esc(w.worker || "?")}">
      <details class="sqlmock-card" data-k="card" open>
        <summary>
          <span class="admock-card-head">
            <h2>${esc(w.worker || "?")}</h2>
            <span class="muted">${w.seeded || 0} prepared answer${(w.seeded || 0) === 1 ? "" : "s"} ·
            ${w.held || 0} statement${(w.held || 0) === 1 ? "" : "s"} asked${
              failed ? ` · <b>${failed}</b> with no answer` : ""}</span>
          </span>
          <span class="admock-card-state">
            <span class="pill warn"><span class="dot"></span>mockup</span>
            <span class="muted" style="margin-left:8px">reported ${esc(fmtNano(w.at))}</span>
          </span>
        </summary>
        ${w.truncated ? `<p class="muted">Showing the newest ${sts.length} of ${w.held} — a run this
          long is past what this view carries.</p>` : ""}
        ${sts.length
          ? `<table class="admock-attrs sqlmock-table"><tbody>${sts.map(renderStatement).join("")}</tbody></table>`
          : `<p class="empty">Nothing asked yet. This worker is in mockup mode and has served no
             database task since it started.</p>`}
      </details>
    </div>`;
  };

  // Which cards the reader has put away, addressed by worker rather than by position —
  // a refresh must not reopen what somebody closed.
  const keyOf = (d) => {
    const w = d.closest("[data-worker]");
    return `${w ? w.dataset.worker : ""}|${d.dataset.k || ""}`;
  };
  const foldState = () => {
    const state = new Map();
    for (const d of list.querySelectorAll("details")) state.set(keyOf(d), d.open);
    return state;
  };
  const refold = (state) => {
    for (const d of list.querySelectorAll("details")) {
      const was = state.get(keyOf(d));
      if (was !== undefined) d.open = was;
    }
  };

  // The last payload rendered. A poll that brings nothing new leaves the DOM alone: the
  // view refreshes every few seconds, and rebuilding an identical table would cost the
  // reader their scroll position for no news at all.
  let rendered = null;
  const load = async () => {
    let data;
    try {
      data = await api("GET", "/api/v1/sql/mock-journal");
    } catch (e) {
      rendered = null; // re-render once it answers again, whatever it says
      list.innerHTML = `<p class="empty">${esc(e.message)}</p>`;
      return;
    }
    const fresh = JSON.stringify(data);
    if (fresh === rendered) return;
    const folds = foldState();
    rendered = fresh;
    const workers = (data && data.workers) || [];
    if (!workers.length) {
      list.innerHTML = `<div class="card"><p class="empty">No worker has reported a mockup run.
        Switch <b>Answer database tasks from a mockup</b> on under Organization &rsaquo; Workers;
        the worker restarts and reports itself, and every database task it then serves shows up
        here.</p></div>`;
      return;
    }
    list.innerHTML = workers.map(renderWorker).join("");
    refold(folds);
  };

  await load();
  if (superseded(gen)) return;
  document.getElementById("sqlmock-refresh").addEventListener("click", load);
  const follow = document.getElementById("sqlmock-follow");
  // Five seconds, like the mock directory: a mockup run is something you watch while
  // clicking through a process, and the read is a small in-memory list on a server that
  // is not the engine's run loop.
  const timer = setInterval(() => { if (follow.checked) load(); }, 5000);
  window.__atlasCleanup = () => clearInterval(timer);
}

// viewDecisionDetail lists every evaluation of one decision — its "instances" —
// newest first, each showing the exact inputs it saw, the outputs it produced, and
// (expandable) the temis trace of which rules fired (ADR-0066). This is the
// drill-down for debugging a decision that isn't returning what you expect: the
// inputs show the precise evaluated value, with its JSON type and quoting, so a
// string compared against a number, a stray space, or a wrong type is visible.
async function viewDecisionDetail(id) {
  setTitle(`${id} · Decisions`);
  view.innerHTML = `
    <div class="between">
      <div>
        <div class="muted" style="font-size:12px"><a href="#/operations/decisions">← Decisions</a></div>
        <h1>${esc(id)}</h1>
      </div>
      <button class="btn neutral" id="refresh" title="Reload this decision’s evaluations">Refresh</button>
    </div>
    <p class="muted">Every evaluation of this decision, newest first — one row per
    evaluation. <b>Inputs</b> is what the decision actually saw (each value with its
    type); <b>Result</b> is what it produced, tagged with the rule that fired. Hover a
    result marked <b>&#8862;</b> to see the decision table with the matched rule
    highlighted — a rule that never matches (a string compared against a number, a
    stray space, a wrong type) shows its condition in red.</p>
    <div class="card" style="padding:0">
      <table data-dt-key="decision-evals">
        <thead><tr><th>When</th><th>Instance</th><th>Element</th><th>Inputs</th><th>Result</th></tr></thead>
        <tbody id="rows"><tr><td colspan="5" class="empty">Loading…</td></tr></tbody>
      </table>
    </div>
    <div class="dec-pop" id="dec-pop" hidden></div>`;
  const tbody = document.getElementById("rows");
  const pop = document.getElementById("dec-pop");
  const fmtNano = (ns) => ns ? new Date(ns / 1e6).toLocaleString() : "—";
  const fmtVal = (v) => (v === null || v === undefined ? "null" : typeof v === "string" ? v : JSON.stringify(v));
  const cellText = (t) => { const s = (t ?? "").trim(); return s === "" || s === "-" ? "–" : s; };
  const tablesOf = (r) => (r && r.trace && Array.isArray(r.trace.tables)) ? r.trace.tables : [];
  const matchedNums = (r) => {
    const nums = [];
    for (const t of tablesOf(r)) for (const rule of (t.rules || [])) if (rule.matched) nums.push(rule.index + 1);
    return [...new Set(nums)];
  };

  // miniTable renders one decision table as a compact matrix (mirrors temis' Operate
  // view): a row per rule, input columns + output, the matched rule highlighted and
  // each cell tinted by whether its condition held.
  const miniTable = (tt, n) => {
    const matched = (tt.rules || []).filter((r) => r.matched).map((r) => r.index + 1);
    const policy = (tt.hitPolicy || "U") + (tt.aggregation ? " " + tt.aggregation : "");
    const head = matched.length ? `Rule ${matched.join(", ")} fired` : "no rule fired";
    const ins = tt.inputs || [];
    const hr = `<tr><th class="mcol-idx">#</th>${ins.map((i) =>
      `<th>${esc(i.expression)} <code>= ${esc(fmtVal(i.value))}</code></th>`).join("")}<th>&rarr;</th></tr>`;
    const body = (tt.rules || []).map((r) => {
      const cells = ins.map((_, k) => {
        const c = r.conditions && r.conditions[k];
        const cls = c ? (c.matched ? "mcell is-ok" : "mcell is-no") : "mcell is-skip";
        return `<td class="${cls}">${c ? esc(cellText(c.entry)) : ""}</td>`;
      }).join("");
      const out = r.matched && r.outputs ? esc(r.outputs.map(fmtVal).join(", ")) : "";
      return `<tr class="mrule${r.matched ? " is-hit" : ""}"><td class="mcol-idx">${r.index + 1}</td>${cells}<td class="mout">${out}</td></tr>`;
    }).join("");
    return `<div class="mtable"><div class="mtable-head">${n ? `Table ${n} · ` : ""}${esc(head)}<span class="mtable-policy">${esc(policy)}</span></div>` +
      `<table class="mgrid">${hr}${body}</table></div>`;
  };

  let evals = [];
  const load = async () => {
    try {
      evals = await api("GET", `/api/v1/decisions/${encodeURIComponent(id)}/evaluations`) || [];
      if (!evals.length) {
        tbody.innerHTML = `<tr><td colspan="5" class="empty">
          This decision has not been evaluated yet. Start a process instance whose
          business rule task calls it.</td></tr>`;
        return;
      }
      tbody.innerHTML = evals.map((r, i) => {
        const ins = r.inputs && typeof r.inputs === "object" ? Object.entries(r.inputs) : [];
        const pills = ins.length
          ? `<div class="in-pills">${ins.map(([k, v]) => `<span class="pill-kv"><b>${esc(k)}</b> = ${esc(fmtVal(v))}</span>`).join("")}</div>`
          : '<span class="muted">—</span>';
        const outs = r.outputs && typeof r.outputs === "object" ? Object.entries(r.outputs) : [];
        const nums = matchedNums(r);
        const badge = nums.length ? `<span class="res-rule">Rule ${nums.join(", ")}</span>` : "";
        const hoverable = tablesOf(r).length ? " hoverable" : "";
        const result = outs.length
          ? `<div class="res">${outs.map(([k, v], oi) =>
              `<div class="res-row${hoverable}" data-ev="${i}"${hoverable ? ' tabindex="0"' : ""}>
                <span class="res-key">${esc(k)}</span>
                <span class="res-val">${esc(fmtVal(v))}</span>
                ${oi === 0 ? badge : ""}
              </div>`).join("")}</div>`
          : '<span class="muted">—</span>';
        return `<tr>
          <td class="muted" data-sort="${r.at || 0}">${esc(fmtNano(r.at))}</td>
          <td><a href="#/operations/i/${r.instanceKey}" title="Replay this instance step by step">&#9654; ${r.instanceKey}</a></td>
          <td class="muted">${esc(r.elementId || "—")}</td>
          <td>${pills}</td>
          <td>${result}</td>
        </tr>`;
      }).join("");
    } catch (e) {
      tbody.innerHTML = `<tr><td colspan="5" class="empty">${esc(e.message)}</td></tr>`;
    }
  };

  // A shared, viewport-positioned popover reveals the matched-rule table on hover —
  // the "table as graphic" from temis, without a column of raw JSON.
  const showPop = (row) => {
    const r = evals[Number(row.dataset.ev)];
    const tables = tablesOf(r);
    if (!tables.length) return;
    pop.innerHTML = `<div class="pop-title">${esc(id)}</div>` +
      tables.map((tt, i) => miniTable(tt, tables.length > 1 ? i + 1 : 0)).join("");
    pop.hidden = false;
    const box = row.getBoundingClientRect();
    const top = box.top - pop.offsetHeight - 10;
    pop.style.left = Math.max(8, Math.min(box.left, window.innerWidth - pop.offsetWidth - 8)) + "px";
    pop.style.top = (top < 8 ? box.bottom + 10 : top) + "px";
  };
  const hidePop = () => { pop.hidden = true; };
  tbody.addEventListener("pointerover", (e) => {
    const row = e.target.closest(".res-row.hoverable");
    if (row && tbody.contains(row)) showPop(row);
  });
  tbody.addEventListener("pointerout", (e) => {
    const row = e.target.closest(".res-row.hoverable");
    if (row && !(e.relatedTarget && e.relatedTarget.closest && e.relatedTarget.closest(".res-row.hoverable") === row)) hidePop();
  });
  tbody.addEventListener("focusin", (e) => { const row = e.target.closest(".res-row.hoverable"); if (row) showPop(row); });
  tbody.addEventListener("focusout", hidePop);

  document.getElementById("refresh").addEventListener("click", load);
  await load();
}

// ---------- Tasks (Outlook-style inbox, ADR-0028) ----------

// A task's display title: the user task's element name, falling back to its BPMN
// id so a task authored without a name is still recognizable.
const taskTitle = (t) => t.name || t.elementId || "User task";

// taskPriority defaults an absent/zero priority to 50 (the model default), so
// sorting and display are stable regardless of what the API omits.
const taskPriority = (t) => (t.priority && t.priority > 0 ? t.priority : 50);

// taskOrder sorts the inbox like a real task list: tasks with a due date come
// first, earliest due first (an overdue task floats to the very top); then higher
// priority; then, as a stable tiebreak, older tasks (smaller key) first.
function taskOrder(a, b) {
  const da = a.dueDate || 0, db = b.dueDate || 0;
  if (!!da !== !!db) return da ? -1 : 1; // a due task before an undated one
  if (da && db && da !== db) return da - db; // sooner due first
  const pa = taskPriority(a), pb = taskPriority(b);
  if (pa !== pb) return pb - pa; // higher priority first
  return a.key < b.key ? -1 : a.key > b.key ? 1 : 0;
}

// dueInfo turns a task's Unix-ms due date into a short relative label and an
// overdue flag, or null when the task has no due date.
function dueInfo(t) {
  if (!t.dueDate) return null;
  const ms = t.dueDate - Date.now();
  const overdue = ms < 0;
  const mins = Math.round(Math.abs(ms) / 60000);
  let rel;
  if (mins < 1) rel = overdue ? "just now" : "in <1 min";
  else if (mins < 60) rel = `${mins} min`;
  else if (mins < 60 * 24) rel = `${Math.round(mins / 60)} h`;
  else rel = `${Math.round(mins / (60 * 24))} d`;
  const label = overdue ? `Overdue by ${rel}` : `Due in ${rel}`;
  const abs = new Date(t.dueDate).toLocaleString();
  return { overdue, label, rel, abs };
}

// The inbox folders. Each is a predicate over a task plus the current identity —
// there is no auth yet (ADR-0028 leaves assignment/authorization open), so "me"
// is a display-only identity the user types, and folder membership is derived
// purely from the task's assignment metadata.
const TASK_FOLDERS = [
  { id: "all", label: "All tasks", match: () => true },
  { id: "mine", label: "Assigned to me", match: (t, me) => !!me && t.assignee === me },
  { id: "unassigned", label: "Unassigned", match: (t) => !t.assignee },
  { id: "group", label: "Group tasks", match: (t) => !!t.candidateGroups },
];

async function viewTasks(preselectKey) {
  // With auth on, identity is the signed-in user (server-authoritative); with auth
  // off it stays a typed, display-only identity (ADR-0045).
  const authOn = AUTH.enabled;
  const state = {
    tasks: [],
    // A deep link (…/tasks/t/{jobKey}, e.g. from the Operations live view) lands on
    // the "All tasks" folder so the linked task is always in view, and preselects it.
    folder: "all",
    selected: preselectKey != null ? preselectKey : null, // job key of the selected task
    me: authOn ? ((AUTH.user && AUTH.user.username) || "") : (localStorage.getItem("atlas.tasks.me") || ""),
    assignable: [], // enabled users a task can be assigned to, for the picker
    mountedForm: null, // the live form-js viewer instance for the selected task, if any
    mountedProc: null, // the read-only process-view handle for the selected task, if mounted
    detailTab: "form", // which detail tab shows — "form" | "process" (kept across selections)
    query: "", // free-text filter over the visible tasks (name/process/assignee/…)
    sort: localStorage.getItem("atlas.tasks.sort") || "smart", // sort key, see SORTS
    selectMode: false, // multi-select for bulk actions
    picked: new Set(), // job keys ticked for a bulk action
    truncated: false, // the server returned a capped page (more tasks exist)
    nextCursor: null, // job key to pass as ?before= for the next (older) page
  };

  // SORTS are the orderings the toolbar offers over the visible tasks. "smart" is
  // the default inbox order (taskOrder); the rest are single-key sorts an operator
  // reaches for when triaging a big queue. Age uses the monotonic job key.
  const SORTS = {
    smart: { label: "Smart (due · priority)", cmp: taskOrder },
    due: { label: "Due date", cmp: (a, b) => (a.dueDate || 8.64e15) - (b.dueDate || 8.64e15) || a.key - b.key },
    priority: { label: "Priority", cmp: (a, b) => taskPriority(b) - taskPriority(a) || a.key - b.key },
    name: { label: "Name (A–Z)", cmp: (a, b) => taskTitle(a).localeCompare(taskTitle(b)) || a.key - b.key },
    process: { label: "Process", cmp: (a, b) => (a.processId || "").localeCompare(b.processId || "") || a.key - b.key },
    newest: { label: "Newest first", cmp: (a, b) => b.key - a.key },
    oldest: { label: "Oldest first", cmp: (a, b) => a.key - b.key },
  };

  const identity = authOn
    ? `<div class="tasks-identity"><span>You</span><div class="tasks-me">${esc(state.me) || "—"}</div></div>`
    : `<label class="tasks-identity"><span>You</span>
        <input id="task-me" type="text" placeholder="e.g. editor" value="${esc(state.me)}" spellcheck="false" /></label>`;

  view.innerHTML = `
    <div class="tasks">
      <aside class="tasks-folders">
        ${identity}
        <nav id="task-folder-nav"></nav>
      </aside>
      <section class="tasks-list-pane">
        <header class="tasks-list-head">
          <h2 id="task-list-title">All tasks</h2>
          <button class="btn ghost small" id="task-select" title="Select multiple tasks to act on them together">Select</button>
          <button class="btn ghost small" id="task-refresh" title="Reload the task list now">Refresh</button>
        </header>
        <div class="tasks-toolbar">
          <span class="tasks-search">
            <svg width="13" height="13" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true"><circle cx="7" cy="7" r="4.5"/><path d="M11 11l3 3"/></svg>
            <input id="task-q" type="text" placeholder="Filter tasks…" aria-label="Filter tasks" spellcheck="false"/>
          </span>
          <label class="tasks-sort">Sort
            <select id="task-sort">${Object.entries(SORTS).map(([k, s]) => `<option value="${k}"${k === state.sort ? " selected" : ""}>${esc(s.label)}</option>`).join("")}</select>
          </label>
        </div>
        <div class="tasks-bulk" id="task-bulk" hidden></div>
        <div class="tasks-trunc" id="task-trunc" hidden></div>
        <ul class="tasks-list" id="task-list"><li class="tasks-empty muted">Loading&hellip;</li></ul>
        <div class="tasks-col-resizer" id="tasks-col-resizer" title="Drag to resize" role="separator" aria-orientation="vertical"></div>
      </section>
      <section class="tasks-detail" id="task-detail"></section>
    </div>`;

  const nav = document.getElementById("task-folder-nav");
  const listEl = document.getElementById("task-list");
  const detailEl = document.getElementById("task-detail");
  const titleEl = document.getElementById("task-list-title");

  // Make the list | detail divider draggable, so the detail (and its form) can be
  // widened or narrowed by resizing the list column. The width persists across
  // sessions. Folders stay fixed; the detail is the flexible remainder (1fr).
  (function wireDetailResize() {
    const grid = view.querySelector(".tasks");
    const listPane = view.querySelector(".tasks-list-pane");
    const rez = document.getElementById("tasks-col-resizer");
    if (!grid || !listPane || !rez) return;
    const MINW = 240, MAXW = 760;
    const foldersW = Math.round((view.querySelector(".tasks-folders") || {}).getBoundingClientRect
      ? view.querySelector(".tasks-folders").getBoundingClientRect().width : 210) || 210;
    const clamp = (w) => Math.min(MAXW, Math.max(MINW, w));
    const saved = parseInt(localStorage.getItem("atlas.tasks.listW"), 10);
    let listW = Number.isFinite(saved) ? clamp(saved) : 340;
    const apply = () => { grid.style.gridTemplateColumns = `${foldersW}px ${listW}px 1fr`; };
    apply();
    const move = (e) => { listW = clamp(Math.round(e.clientX - listPane.getBoundingClientRect().left)); apply(); };
    const up = () => {
      document.removeEventListener("pointermove", move);
      document.removeEventListener("pointerup", up);
      rez.classList.remove("dragging");
      document.body.style.userSelect = "";
      localStorage.setItem("atlas.tasks.listW", String(listW));
    };
    rez.addEventListener("pointerdown", (e) => {
      e.preventDefault();
      rez.classList.add("dragging");
      document.body.style.userSelect = "none";
      document.addEventListener("pointermove", move);
      document.addEventListener("pointerup", up);
    });
  })();

  // visible applies the folder, then the free-text query, then the chosen sort.
  const matchesQuery = (t, q) =>
    (taskTitle(t) + " " + (t.processId || "") + " " + (t.assignee || "") + " " +
      (t.candidateGroups || "") + " " + (t.elementId || "")).toLowerCase().includes(q);
  const visible = () => {
    const f = TASK_FOLDERS.find((x) => x.id === state.folder) || TASK_FOLDERS[0];
    const q = state.query.trim().toLowerCase();
    const items = state.tasks.filter((t) => f.match(t, state.me) && (!q || matchesQuery(t, q)));
    return items.sort((SORTS[state.sort] || SORTS.smart).cmp);
  };

  function renderFolders() {
    nav.innerHTML = TASK_FOLDERS.map((f) => {
      const n = state.tasks.filter((t) => f.match(t, state.me)).length;
      const active = f.id === state.folder ? " active" : "";
      return `<button class="tasks-folder${active}" data-folder="${f.id}">
        <span>${esc(f.label)}</span><span class="tasks-count">${n}</span>
      </button>`;
    }).join("");
    nav.querySelectorAll(".tasks-folder").forEach((b) => {
      b.addEventListener("click", () => {
        state.folder = b.dataset.folder;
        state.selected = null;
        renderAll();
      });
    });
  }

  const byKey = (k) => state.tasks.find((t) => t.key === k);

  function renderList() {
    const items = visible();
    const f = TASK_FOLDERS.find((x) => x.id === state.folder) || TASK_FOLDERS[0];
    titleEl.textContent = f.label;
    renderBulk();
    if (!items.length) {
      listEl.innerHTML = `<li class="tasks-empty muted">${state.query.trim() ? "No tasks match your filter." : "No tasks in this folder."}</li>`;
      return;
    }
    const selMode = state.selectMode;
    listEl.innerHTML = items
      .map((t) => {
        const sel = t.key === state.selected ? " selected" : "";
        const picked = state.picked.has(t.key);
        const who = t.assignee ? esc(t.assignee) : t.candidateGroups ? esc(t.candidateGroups) : "Unassigned";
        const hi = taskPriority(t) >= 70 ? `<span class="prio-dot" title="High priority (${taskPriority(t)})"></span>` : "";
        const d = dueInfo(t);
        const due = d ? `<span class="due-badge${d.overdue ? " overdue" : ""}" title="${esc(d.abs)}">${esc(d.overdue ? "Overdue" : "Due " + d.rel)}</span>` : "";
        const cb = selMode ? `<input type="checkbox" class="tasks-check"${picked ? " checked" : ""} aria-label="Select task"/>` : "";
        return `<li class="tasks-item${sel}${picked ? " picked" : ""}" data-key="${t.key}">
          ${cb}
          <div class="tasks-item-body">
            <div class="tasks-item-top">
              <span class="tasks-item-title">${hi}${esc(taskTitle(t))}</span>
              ${t.lane ? `<span class="chip" title="Lane">${esc(t.lane)}</span>` : ""}
              <span class="chip">${esc(t.processId || "")}</span>
            </div>
            <div class="tasks-item-sub muted"><span>${who}</span>${due}</div>
          </div>
        </li>`;
      })
      .join("");
    listEl.querySelectorAll(".tasks-item").forEach((li) => {
      li.addEventListener("click", () => {
        const key = Number(li.dataset.key);
        if (state.selectMode) { togglePick(key, li); return; }
        state.selected = key;
        renderList();
        renderDetail();
      });
    });
  }

  // togglePick flips one task's bulk selection. It updates just that row (and the
  // bulk bar) rather than rebuilding the whole list, so ticking through a long
  // queue stays snappy.
  function togglePick(key, li) {
    if (state.picked.has(key)) state.picked.delete(key); else state.picked.add(key);
    const on = state.picked.has(key);
    if (li) {
      li.classList.toggle("picked", on);
      const cb = li.querySelector(".tasks-check");
      if (cb) cb.checked = on;
    }
    renderBulk();
  }

  // renderBulk draws the bulk-action bar while in select mode: a count, "all
  // visible", and the actions (claim / unclaim / assign / complete). Hidden
  // otherwise. Rebuilt on each pick, which is cheap (a handful of controls).
  function renderBulk() {
    const bulkEl = document.getElementById("task-bulk");
    if (!bulkEl) return;
    if (!state.selectMode) { bulkEl.hidden = true; bulkEl.innerHTML = ""; return; }
    const vis = visible();
    const n = state.picked.size;
    const allVisPicked = vis.length > 0 && vis.every((t) => state.picked.has(t.key));
    const assignOpts = state.assignable
      .map((u) => `<option value="${esc(u.username)}">${esc(u.displayName || u.username)}</option>`).join("");
    bulkEl.hidden = false;
    bulkEl.innerHTML = `
      <label class="tasks-bulk-all"><input type="checkbox" id="bulk-all"${allVisPicked ? " checked" : ""}/> All visible</label>
      <span class="tasks-bulk-count">${n} selected</span>
      <span class="tasks-bulk-actions">
        <button class="btn small" id="bulk-claim"${n ? "" : " disabled"} title="Claim the selected tasks">Claim</button>
        <button class="btn small" id="bulk-unclaim"${n ? "" : " disabled"} title="Release the selected tasks">Unclaim</button>
        ${state.assignable.length ? `<select class="tasks-assign" id="bulk-assign"${n ? "" : " disabled"}><option value="">Assign to&hellip;</option>${assignOpts}</select>` : ""}
        <button class="btn small" id="bulk-complete"${n ? "" : " disabled"} title="Complete the selected tasks">Complete</button>
        <button class="btn ghost small" id="bulk-clear"${n ? "" : " disabled"} title="Clear the current selection">Clear</button>
      </span>`;
    bulkEl.querySelector("#bulk-all").addEventListener("change", (e) => {
      if (e.target.checked) vis.forEach((t) => state.picked.add(t.key));
      else vis.forEach((t) => state.picked.delete(t.key));
      renderList();
    });
    bulkEl.querySelector("#bulk-claim").addEventListener("click", () => bulkAction("claim"));
    bulkEl.querySelector("#bulk-unclaim").addEventListener("click", () => bulkAction("unclaim"));
    bulkEl.querySelector("#bulk-complete").addEventListener("click", bulkComplete);
    bulkEl.querySelector("#bulk-clear").addEventListener("click", () => { state.picked.clear(); renderList(); });
    const asg = bulkEl.querySelector("#bulk-assign");
    if (asg) asg.addEventListener("change", (e) => { if (e.target.value) bulkAction("assign", e.target.value); });
  }

  // bulkAction claims / unclaims / assigns every picked task. The per-task calls run
  // in parallel (the engine serializes writes) and it reports how many succeeded.
  async function bulkAction(kind, assignee) {
    const keys = [...state.picked];
    if (!keys.length) return;
    if (kind === "claim" && !authOn && !state.me) { toast("Set your identity (top left) to claim", "err"); return; }
    const call = (k) => {
      if (kind === "unclaim") return api("POST", `/api/v1/tasks/${k}/unclaim`);
      if (kind === "assign") return api("POST", `/api/v1/tasks/${k}/claim`, { assignee });
      return api("POST", `/api/v1/tasks/${k}/claim`, authOn ? undefined : { assignee: state.me });
    };
    const verb = kind === "unclaim" ? "Unclaimed" : kind === "assign" ? `Assigned to ${assignee}` : "Claimed";
    const results = await Promise.allSettled(keys.map(call));
    const ok = results.filter((r) => r.status === "fulfilled").length;
    const fail = results.length - ok;
    toast(`${verb}: ${ok} done${fail ? `, ${fail} failed` : ""}`, fail ? "err" : "ok");
    state.picked.clear();
    await load();
  }

  // bulkComplete completes the picked tasks that have no form (a form needs its
  // own data, so those are skipped with a note). Confirms first, since completing
  // is irreversible.
  async function bulkComplete() {
    const tasks = [...state.picked].map(byKey).filter(Boolean);
    const formless = tasks.filter((t) => !t.formId);
    const withForm = tasks.length - formless.length;
    if (!formless.length) {
      toast(withForm ? "Selected tasks all have a form — complete those individually." : "Nothing to complete.", "err");
      return;
    }
    const skip = withForm ? ` (${withForm} with a form will be skipped)` : "";
    if (!confirm(`Complete ${formless.length} task${formless.length === 1 ? "" : "s"}?${skip}`)) return;
    const results = await Promise.allSettled(formless.map((t) => api("POST", `/api/v1/tasks/${t.key}/complete`)));
    const ok = results.filter((r) => r.status === "fulfilled").length;
    const fail = results.length - ok;
    toast(`Completed ${ok}${fail ? `, ${fail} failed` : ""}${withForm ? `, skipped ${withForm} with forms` : ""}`, fail ? "err" : "ok");
    state.picked.clear();
    await load();
  }

  // completeCurrent completes the selected task, then lands on the task that slides
  // into its place (auto-advance), so an operator can clear a queue without
  // reaching for the mouse between tasks. Bound to the button and Ctrl/⌘+Enter.
  async function completeCurrent() {
    const t = state.tasks.find((x) => x.key === state.selected);
    if (!t) return;
    let payload;
    if (state.mountedForm) {
      const { data, errors } = state.mountedForm.submit();
      if (errors && Object.keys(errors).length > 0) { toast("Please fix the highlighted fields", "err"); return; }
      // A file field (form-js filepicker) holds the picked File client-side, not its
      // bytes — so read the selected file as text and submit it as the `csvText`
      // variable a CSV-import service task parses (ADR-0087). This keeps the upload a
      // normal user-task step rather than a side-channel endpoint.
      const fileInput = document.querySelector("#task-form input[type=file]");
      if (fileInput && fileInput.files && fileInput.files.length) {
        try { data.csvText = await fileInput.files[0].text(); }
        catch (err) { toast("Datei konnte nicht gelesen werden: " + err.message, "err"); return; }
      }
      payload = { variables: data };
    }
    const btn = document.getElementById("task-complete");
    if (btn) btn.disabled = true;
    const idx = visible().findIndex((x) => x.key === t.key); // the slot to land back on
    try {
      await api("POST", "/api/v1/tasks/" + t.key + "/complete", payload);
      toast("Task completed");
      state.tasks = await api("GET", "/api/v1/tasks");
      state.tasks.sort(taskOrder);
      const after = visible();
      state.selected = after.length ? after[Math.min(Math.max(idx, 0), after.length - 1)].key : null;
      renderAll();
    } catch (err) {
      toast("Complete failed: " + err.message, "err");
      if (btn) btn.disabled = false;
    }
  }

  // destroyForm tears down the live form-js instance (if any) before the detail
  // pane is re-rendered or the selection changes, so no viewer leaks.
  function destroyForm() {
    if (state.mountedForm) {
      try { state.mountedForm.destroy(); } catch { /* already gone */ }
      state.mountedForm = null;
    }
  }

  // mountForm loads the vendored form-js viewer, the task's bound form schema,
  // and the instance's current variables, then renders the form prefilled — a
  // field whose key matches a variable shows its value (ADR-0028). Guards against
  // the selection changing while the (async) load is in flight.
  async function mountForm(t) {
    const host = document.getElementById("task-form");
    if (!host) return;
    try {
      const [{ Form }, def, data] = await Promise.all([
        loadFormViewer(),
        api("GET", "/api/v1/forms/" + encodeURIComponent(t.formId)),
        // Prefill from the task's own element-instance scope — where its input-mapped
        // fields live — so a task nested in a subprocess or multi-instance body fills
        // from its own fields, not just the process root (ADR-0084). Falls back to the
        // process instance when a task carries no element-instance key. A failed read
        // just yields a blank form rather than blocking the task.
        api("GET", "/api/v1/instances/" + (t.elementInstanceKey || t.processInstanceKey) + "/variables").catch(() => ({})),
      ]);
      if (state.selected !== t.key) return; // selection moved on; drop this mount
      host.innerHTML = "";
      const form = new Form({ container: host });
      await form.importSchema(def.schema, data || {});
      if (state.selected !== t.key) { try { form.destroy(); } catch { /* noop */ } return; }
      state.mountedForm = form;
    } catch (err) {
      host.innerHTML = `<p class="muted err">Failed to load form: ${esc(err.message)}</p>`;
    }
  }

  // destroyProc tears down the read-only process viewer before the detail pane is
  // re-rendered or the selection changes, so no bpmn-js viewer leaks.
  function destroyProc() {
    if (state.mountedProc) {
      try { state.mountedProc.destroy(); } catch { /* already gone */ }
      state.mountedProc = null;
    }
  }

  // mountProc lazily loads the read-only process view — the BPMN diagram of the
  // task's instance with its progress overlaid — into the expanded panel. Guards
  // against the selection changing while the (async) import/mount is in flight.
  async function mountProc(t) {
    const host = document.getElementById("tp-canvas");
    if (!host) return;
    destroyProc();
    try {
      const mod = await import("./editor.js");
      if (state.selected !== t.key || !document.getElementById("tp-canvas")) return;
      state.mountedProc = await mod.mountTaskProcess(host, {
        api, instanceKey: t.processInstanceKey, activeElementId: t.elementId,
      });
    } catch (err) {
      host.innerHTML = `<p class="muted err" style="padding:16px">Could not load the process view: ${esc(err.message)}</p>`;
    }
    renderProcVars(t); // the instance's current variables, beneath the diagram
  }

  // renderProcVars fills the Process tab's Variables list with the instance's
  // current process variables, so the assignee sees the data the process carries,
  // not just where the token is. Guards against the selection moving on mid-fetch.
  async function renderProcVars(t) {
    if (!document.getElementById("tp-vars-body")) return;
    let vars;
    try {
      vars = await api("GET", "/api/v1/instances/" + t.processInstanceKey + "/variables");
    } catch (err) {
      const b = document.getElementById("tp-vars-body");
      if (b && state.selected === t.key) b.innerHTML = `<p class="muted err">Could not load variables: ${esc(err.message)}</p>`;
      return;
    }
    if (state.selected !== t.key) return;
    const b = document.getElementById("tp-vars-body");
    if (!b) return;
    const entries = vars && typeof vars === "object" ? Object.entries(vars) : [];
    if (!entries.length) { b.innerHTML = `<p class="muted">No variables set yet.</p>`; return; }
    const fmt = (v) => (v === null || v === undefined ? "null" : typeof v === "object" ? JSON.stringify(v) : String(v));
    b.innerHTML = `<table class="tp-vars-table"><tbody>${entries
      .sort((a, c) => a[0].localeCompare(c[0]))
      .map(([k, v]) => `<tr><td class="tp-var-k">${esc(k)}</td><td class="tp-var-v"><code>${esc(fmt(v))}</code></td></tr>`)
      .join("")}</tbody></table>`;
  }

  function renderDetail() {
    destroyForm();
    destroyProc();
    const t = state.tasks.find((x) => x.key === state.selected);
    if (!t) {
      detailEl.innerHTML = `<div class="tasks-detail-empty muted">Select a task to see its details.</div>`;
      return;
    }
    const row = (label, val) =>
      `<div class="tasks-field"><span class="tasks-field-label muted">${label}</span><span>${val}</span></div>`;
    // Claim toggles to unclaim once the task is mine; claiming needs an identity.
    const mine = !!state.me && t.assignee === state.me;
    const claimLabel = mine ? "Unclaim" : "Claim";
    const claimHint = !state.me && !mine ? ` title="Set your identity (top left) to claim"` : "";
    const claimDisabled = !state.me && !mine ? " disabled" : "";
    // Assign-to picker, sourced from real users (ADR-0045). Shown when accounts
    // exist; selecting one assigns the task to that user.
    const assignSelect = state.assignable.length
      ? `<select class="tasks-assign" id="task-assign" title="Assign to a user">
          <option value="">Assign to&hellip;</option>
          ${state.assignable.map((u) =>
            `<option value="${esc(u.username)}"${u.username === t.assignee ? " selected" : ""}>${esc(u.displayName || u.username)}</option>`).join("")}
        </select>`
      : "";
    const formArea = t.formId
      ? `<div class="tasks-form" id="task-form"><p class="muted">Loading form&hellip;</p></div>`
      : `<div class="tasks-form-placeholder"><p class="muted">This task has no form; completing it
         records no variables.</p></div>`;
    // The form and a read-only view of the whole process instance sit side by side
    // as tabs, so the assignee can flip to "what has run and what's still ahead"
    // without the form scrolling away below.
    const tab = state.detailTab === "process" ? "process" : "form";
    const tabBar = `
      <div class="tasks-detail-tabs" id="task-dtabs" role="tablist">
        <button type="button" role="tab" data-dtab="form"${tab === "form" ? ' class="active" aria-selected="true"' : ' aria-selected="false"'} title="Show the task form">Form</button>
        <button type="button" role="tab" data-dtab="process"${tab === "process" ? ' class="active" aria-selected="true"' : ' aria-selected="false"'} title="Show the process diagram and variables">Process</button>
      </div>`;
    const procPane = `
      <div class="tasks-tabpane" id="pane-process"${tab === "process" ? "" : " hidden"}>
        <div class="tp-legend">
          <span><i class="tp-sw here"></i> This task</span>
          <span><i class="tp-sw active"></i> Active</span>
          <span><i class="tp-sw done"></i> Completed</span>
        </div>
        <div class="tp-canvas" id="tp-canvas"><p class="tp-msg muted">Loading&hellip;</p></div>
        <div class="tp-vars" id="tp-vars">
          <h3 class="tp-vars-head">Variables</h3>
          <div id="tp-vars-body"><p class="tp-msg muted">Loading&hellip;</p></div>
        </div>
      </div>`;
    // The element's <bpmn:documentation> (ADR-0025) is the modeler's instruction for
    // whoever picks the task up — what to check, which rule applies, when to refuse. It
    // leads the detail, above the metadata rows, because it is what the assignee needs
    // before doing anything; a task whose element carries none simply shows no block.
    const docBlock = (t.documentation || "").trim()
      ? `<div class="tasks-doc"><h2>What to do</h2><p>${esc(t.documentation.trim())}</p></div>`
      : "";
    detailEl.innerHTML = `
      <header class="tasks-detail-head">
        <h1>${esc(taskTitle(t))}</h1>
        <div class="tasks-detail-actions">
          ${assignSelect}
          <button class="btn neutral" id="task-claim"${claimDisabled}${claimHint || ` title="${mine ? "Release this task back to the queue" : "Claim this task so it is assigned to you"}"`}>${claimLabel}</button>
          <button class="btn" id="task-complete" title="Complete (Ctrl/⌘ + Enter)">Complete task</button>
        </div>
      </header>
      ${docBlock}
      <div class="tasks-fields">
        ${row("Process", esc(t.processId || "—"))}
        ${row("Element", `<span class="chip">${esc(t.elementId || "—")}</span>`)}
        ${row("Assignee", esc(t.assignee || "—"))}
        ${row("Candidate groups", esc(t.candidateGroups || "—"))}
        ${t.lane ? row("Lane", esc((t.lanePath && t.lanePath.length > 1 ? t.lanePath : [t.lane]).join(" › "))) : ""}
        ${row("Priority", `${taskPriority(t)}${taskPriority(t) >= 70 ? ' <span class="prio-dot" title="High priority"></span>' : ""}`)}
        ${row("Due", (() => { const d = dueInfo(t); return d ? `<span class="${d.overdue ? "due-text overdue" : "due-text"}" title="${esc(d.abs)}">${esc(d.label)} · ${esc(d.abs)}</span>` : "—"; })())}
        ${row("Instance", `<span class="chip">${t.processInstanceKey}</span>`)}
        ${row("Task key", `<span class="chip">${t.key}</span>`)}
      </div>
      ${tabBar}
      <div class="tasks-tab-body">
        <div class="tasks-tabpane" id="pane-form"${tab === "form" ? "" : " hidden"}>${formArea}</div>
        ${procPane}
      </div>`;
    document.getElementById("task-complete").addEventListener("click", () => completeCurrent());
    document.getElementById("task-claim").addEventListener("click", async (e) => {
      const btn = e.currentTarget;
      btn.disabled = true;
      try {
        if (mine) {
          await api("POST", "/api/v1/tasks/" + t.key + "/unclaim");
          toast("Task released");
        } else {
          // With auth on the server claims for the signed-in user (empty body);
          // with auth off we pass the typed identity.
          await api("POST", "/api/v1/tasks/" + t.key + "/claim", authOn ? undefined : { assignee: state.me });
          toast("Task claimed");
        }
        await load(); // keeps the selection; the detail re-renders with the new assignee
      } catch (err) {
        toast("Claim failed: " + err.message, "err");
        btn.disabled = false;
      }
    });
    const assignEl = document.getElementById("task-assign");
    if (assignEl) {
      assignEl.addEventListener("change", async (e) => {
        const username = e.target.value;
        if (!username) return;
        try {
          await api("POST", "/api/v1/tasks/" + t.key + "/claim", { assignee: username });
          toast("Assigned to " + username);
          await load();
        } catch (err) {
          toast("Assign failed: " + err.message, "err");
          e.target.value = t.assignee || "";
        }
      });
    }
    // Always mount the form (when the task has one) so Complete has its data even
    // while the Process tab is showing. The process diagram mounts lazily the first
    // time its tab is opened; the chosen tab is kept across task selections.
    if (t.formId) mountForm(t);
    const dtabs = document.getElementById("task-dtabs");
    const paneForm = document.getElementById("pane-form");
    const paneProc = document.getElementById("pane-process");
    dtabs.addEventListener("click", (e) => {
      const b = e.target.closest("button[data-dtab]");
      if (!b) return;
      const next = b.dataset.dtab;
      if (next === state.detailTab) return;
      state.detailTab = next;
      for (const btn of dtabs.querySelectorAll("button")) {
        const on = btn.dataset.dtab === next;
        btn.classList.toggle("active", on);
        btn.setAttribute("aria-selected", on ? "true" : "false");
      }
      paneForm.hidden = next !== "form";
      paneProc.hidden = next !== "process";
      if (next === "process" && !state.mountedProc) mountProc(t);
    });
    if (tab === "process") mountProc(t);
  }

  function renderAll() {
    renderFolders();
    renderTrunc();
    renderList();
    renderDetail();
  }

  // renderTrunc shows a banner when the server returned a capped page, so the count
  // never reads as "these are all the tasks" when it isn't. It offers a "Load older"
  // affordance (the cursor page) alongside the hint to narrow by filter.
  function renderTrunc() {
    const el = document.getElementById("task-trunc");
    if (!el) return;
    if (!state.truncated) { el.hidden = true; el.innerHTML = ""; return; }
    el.hidden = false;
    el.innerHTML =
      `<span>Showing the newest ${state.tasks.length} tasks — more exist. Filter to narrow, or load older.</span>` +
      `<button class="btn ghost small" id="task-older"${state.nextCursor ? "" : " disabled"} title="Load the next page of older tasks">Load older</button>`;
    const older = document.getElementById("task-older");
    if (older) older.addEventListener("click", loadOlder);
  }

  async function load() {
    try {
      // The list is capped and newest-first; a capped page flags X-Tasks-Truncated and
      // hands back X-Tasks-Next-Cursor for paging to older tasks (see loadOlder).
      const { data, headers } = await apiRaw("GET", "/api/v1/tasks");
      state.tasks = data;
      state.truncated = headers.get("X-Tasks-Truncated") === "true";
      state.nextCursor = headers.get("X-Tasks-Next-Cursor") || null;
      // A deep-linked task (…/tasks/t/{key}, e.g. from the Operations live view) can
      // sit outside the capped task-list page during a flood. Rather than silently
      // dropping the selection — which left the form unreachable — fetch that one task
      // by key and fold it in, so it stays selectable and its form mounts. A 404 means
      // there is no open task with that key (e.g. it was completed): clear it then.
      if (state.selected != null && !state.tasks.some((t) => t.key === state.selected)) {
        try {
          state.tasks.push(await api("GET", "/api/v1/tasks/" + encodeURIComponent(state.selected)));
        } catch {
          state.selected = null;
        }
      }
      state.tasks.sort(taskOrder);
      renderAll();
    } catch (e) {
      listEl.innerHTML = `<li class="tasks-empty err">Failed to load tasks: ${esc(e.message)}</li>`;
    }
  }

  // loadOlder pages to the next (older) slice of tasks using the cursor the last
  // capped page handed back, and folds the new rows into the current set — so an
  // operator can reach tasks beyond the newest page without narrowing by filter.
  async function loadOlder() {
    if (!state.nextCursor) return;
    try {
      const { data, headers } = await apiRaw("GET", "/api/v1/tasks?before=" + encodeURIComponent(state.nextCursor));
      const seen = new Set(state.tasks.map((t) => t.key));
      for (const t of data) if (!seen.has(t.key)) state.tasks.push(t);
      state.truncated = headers.get("X-Tasks-Truncated") === "true";
      state.nextCursor = headers.get("X-Tasks-Next-Cursor") || null;
      state.tasks.sort(taskOrder);
      renderAll();
    } catch (e) {
      toast("Load older failed: " + e.message, "err");
    }
  }

  async function loadAssignable() {
    try { state.assignable = await api("GET", "/api/v1/users/assignable"); }
    catch { state.assignable = []; }
  }

  const meInput = document.getElementById("task-me");
  if (meInput) {
    meInput.addEventListener("input", (e) => {
      state.me = e.target.value.trim();
      localStorage.setItem("atlas.tasks.me", state.me);
      renderFolders();
      renderList();
    });
  }
  document.getElementById("task-refresh").addEventListener("click", load);

  const qEl = document.getElementById("task-q");
  if (qEl) qEl.addEventListener("input", (e) => { state.query = e.target.value; renderList(); });
  const sortEl = document.getElementById("task-sort");
  if (sortEl) sortEl.addEventListener("change", (e) => {
    state.sort = SORTS[e.target.value] ? e.target.value : "smart";
    localStorage.setItem("atlas.tasks.sort", state.sort);
    renderList();
  });
  const selectBtn = document.getElementById("task-select");
  if (selectBtn) selectBtn.addEventListener("click", () => {
    state.selectMode = !state.selectMode;
    selectBtn.classList.toggle("on", state.selectMode);
    selectBtn.textContent = state.selectMode ? "Done" : "Select";
    if (!state.selectMode) state.picked.clear();
    renderList();
  });

  // Ctrl/⌘+Enter completes the selected task (auto-advancing to the next), so an
  // operator can clear a queue from the keyboard. A self-removing capture listener
  // keeps it scoped to the tasks view — the SPA replaces view.innerHTML on
  // navigation, so once .tasks leaves the DOM the handler unbinds itself.
  const tasksRoot = view.querySelector(".tasks");
  const onTasksKey = (e) => {
    if (!tasksRoot || !document.body.contains(tasksRoot)) { document.removeEventListener("keydown", onTasksKey, true); return; }
    if ((e.ctrlKey || e.metaKey) && e.key === "Enter" && !state.selectMode && state.selected != null) {
      e.preventDefault();
      completeCurrent();
    }
  };
  document.addEventListener("keydown", onTasksKey, true);

  await loadAssignable();
  await load();
}

// ---------- Start a process via its start form (ADR-0028) ----------
async function viewStartProcess() {
  const state = { procs: [], selected: null, form: null, links: [] };
  view.innerHTML = `
    <div class="tasks" id="start-grid">
      <section class="tasks-list-pane">
        <header class="tasks-list-head"><h2>Start a process</h2>
          <button class="btn ghost small" id="start-refresh" title="Reload the list of startable processes">Refresh</button></header>
        <ul class="tasks-list" id="start-list"><li class="tasks-empty muted">Loading&hellip;</li></ul>
      </section>
      <section class="tasks-detail" id="start-detail"></section>
    </div>`;
  const listEl = view.querySelector("#start-list");
  const detailEl = view.querySelector("#start-detail");

  function destroyForm() {
    if (state.form) { try { state.form.destroy(); } catch { /* noop */ } state.form = null; }
  }

  async function mountStartForm(p) {
    const host = detailEl.querySelector("#start-form");
    if (!host) return;
    try {
      const [{ Form }, def] = await Promise.all([
        loadFormViewer(),
        api("GET", "/api/v1/forms/" + encodeURIComponent(p.startFormId)),
      ]);
      if (state.selected !== p.key) return;
      host.innerHTML = "";
      const form = new Form({ container: host });
      await form.importSchema(def.schema, {});
      state.form = form;
    } catch (e) {
      host.innerHTML = `<p class="muted err">Failed to load the start form: ${esc(e.message)}</p>`;
    }
  }

  function renderDetail() {
    destroyForm();
    const p = state.procs.find((x) => x.key === state.selected);
    if (!p) {
      detailEl.innerHTML = `<div class="tasks-detail-empty muted">Select a process to start it via its form.</div>`;
      return;
    }
    detailEl.innerHTML = `
      <header class="tasks-detail-head">
        <h1>${esc(p.name || p.processId)}</h1>
        <button class="btn" id="start-go" title="Start a new instance with the form values above">Start process</button>
      </header>
      <div class="tasks-fields">
        <div class="tasks-field"><span class="tasks-field-label muted">Process</span><span class="chip">${esc(p.processId)}</span></div>
        <div class="tasks-field"><span class="tasks-field-label muted">Version</span><span>${p.version}</span></div>
      </div>
      <div class="tasks-form" id="start-form"><p class="muted">Loading form&hellip;</p></div>
      <div class="tasks-publish" id="start-publish"></div>`;
    renderPublish(p);
    detailEl.querySelector("#start-go").addEventListener("click", async (e) => {
      const btn = e.currentTarget;
      let variables = {};
      if (state.form) {
        const { data, errors } = state.form.submit();
        if (errors && Object.keys(errors).length > 0) { toast("Please fix the highlighted fields", "err"); return; }
        variables = data;
      }
      btn.disabled = true;
      try {
        await api("POST", `/api/v1/processes/${p.key}/instances`, { variables });
        toast("Process started");
        location.hash = "#/tasks"; // land in the inbox where its first task appears
      } catch (err) { toast("Start failed: " + err.message, "err"); btn.disabled = false; }
    });
    mountStartForm(p);
  }

  // renderPublish draws the "Public link" section: an unauthenticated URL anyone
  // can use to start this process via its form (ADR-0029). It offers publish,
  // copy, and revoke, and re-renders itself after each change.
  function renderPublish(p) {
    const host = detailEl.querySelector("#start-publish");
    if (!host) return;
    const link = state.links.find((l) => l.processId === p.processId);
    if (!link) {
      host.innerHTML = `
        <div class="publish-head"><span class="tasks-field-label muted">Public link</span></div>
        <p class="muted publish-hint">No public link yet. Publish one to let anyone start this process from a form &mdash; no sign-in required.</p>
        <button class="btn ghost small" id="publish-create" title="Create a public, sign-in-free link to start this process">Publish public link</button>`;
      host.querySelector("#publish-create").addEventListener("click", async (e) => {
        e.currentTarget.disabled = true;
        try {
          const created = await api("POST", "/api/v1/public-links", { processId: p.processId });
          state.links = state.links.filter((l) => l.processId !== p.processId).concat(created);
          toast("Public link published");
          renderPublish(p);
        } catch (err) { toast("Publish failed: " + err.message, "err"); e.currentTarget.disabled = false; }
      });
      return;
    }
    const url = location.origin + link.url;
    host.innerHTML = `
      <div class="publish-head"><span class="tasks-field-label muted">Public link</span>
        <span class="chip ok">Live</span></div>
      <div class="publish-row">
        <input class="publish-url" id="publish-url" type="text" readonly value="${esc(url)}" />
        <button class="btn ghost small" id="publish-copy" title="Copy the public link to the clipboard">Copy</button>
        <a class="btn ghost small" href="${esc(link.url)}" target="_blank" rel="noopener">Open</a>
      </div>
      <button class="btn ghost small danger" id="publish-revoke" title="Revoke the public link so its URL stops working">Revoke</button>`;
    host.querySelector("#publish-url").addEventListener("focus", (e) => e.currentTarget.select());
    host.querySelector("#publish-copy").addEventListener("click", async () => {
      try { await navigator.clipboard.writeText(url); toast("Link copied"); }
      catch { const el = host.querySelector("#publish-url"); el.focus(); el.select(); toast("Press Ctrl/Cmd+C to copy", "err"); }
    });
    host.querySelector("#publish-revoke").addEventListener("click", async (e) => {
      if (!confirm("Revoke this public link? The URL will stop working immediately.")) return;
      e.currentTarget.disabled = true;
      try {
        await api("DELETE", "/api/v1/public-links/" + encodeURIComponent(link.token));
        state.links = state.links.filter((l) => l.token !== link.token);
        toast("Public link revoked");
        renderPublish(p);
      } catch (err) { toast("Revoke failed: " + err.message, "err"); e.currentTarget.disabled = false; }
    });
  }

  function renderList() {
    if (!state.procs.length) {
      listEl.innerHTML = `<li class="tasks-empty muted">No process has a start form. Link one on a start event in the Modeler's Implement tab.</li>`;
      return;
    }
    listEl.innerHTML = state.procs.map((p) => {
      const sel = p.key === state.selected ? " selected" : "";
      return `<li class="tasks-item${sel}" data-key="${p.key}">
        <div class="tasks-item-top"><span class="tasks-item-title">${esc(p.name || p.processId)}</span>
          <span class="chip">v${p.version}</span></div>
        <div class="tasks-item-sub muted">${esc(p.processId)}</div></li>`;
    }).join("");
    listEl.querySelectorAll(".tasks-item").forEach((li) =>
      li.addEventListener("click", () => { state.selected = Number(li.dataset.key); renderList(); renderDetail(); }));
  }

  async function load() {
    try {
      const [all, links] = await Promise.all([
        api("GET", "/api/v1/processes"),
        api("GET", "/api/v1/public-links").catch(() => []),
      ]);
      state.links = Array.isArray(links) ? links : [];
      // Only executable processes with a start form; keep the latest version per
      // process id. A non-executable process is descriptive-only and never offered
      // to start (the API refuses it too).
      const latest = new Map();
      for (const p of all) {
        if (!p.startFormId || p.executable === false) continue;
        const cur = latest.get(p.processId);
        if (!cur || p.version > cur.version) latest.set(p.processId, p);
      }
      state.procs = [...latest.values()].sort((a, b) => (a.name || a.processId).localeCompare(b.name || b.processId));
      if (!state.procs.some((p) => p.key === state.selected)) state.selected = null;
      renderList();
      renderDetail();
    } catch (e) {
      listEl.innerHTML = `<li class="tasks-empty err">Failed to load processes: ${esc(e.message)}</li>`;
    }
  }
  view.querySelector("#start-refresh").addEventListener("click", load);
  await load();
}

function viewComingSoon(appId) {
  const name = (APPS.find((a) => a.id === appId) || {}).name || "This app";
  view.innerHTML = `
    <div class="card empty">
      <h1>${esc(name)}</h1>
      <p class="muted">${esc(name)} is on the Atlas roadmap and isn't part of this build yet.</p>
      <a class="btn ghost" href="#/console">Back to Console</a>
    </div>`;
}

// Panorama keeps the Open Exchange XML canonical. The library owns documents;
// its diagram-js viewer is a read-only projection and therefore cannot discard
// unsupported standard content when a model is opened (ADR-0189).
async function viewPanoramaModels() {
  view.innerHTML = `<p class="muted">Loading architecture models…</p>`;

  let models, applications;
  try {
    [models, applications] = await Promise.all([
      api("GET", "/api/v1/panorama/models"),
      api("GET", "/api/v1/applications"),
    ]);
  } catch (e) {
    view.innerHTML = `<div class="card empty"><h1>Panorama</h1><p>${esc(e.message)}</p></div>`;
    return;
  }

  const byApplication = new Map(applications.map((app) => [app.id, app]));
  const writable = applications.filter((app) => !app.protected && roleRank(app.myRole) >= 2);
  const canEdit = (model) => {
    const app = byApplication.get(model.applicationId);
    return !!app && !app.protected && roleRank(app.myRole) >= 2;
  };

  const row = (model) => {
    const app = byApplication.get(model.applicationId);
    const editable = canEdit(model);
    const actions = [
      { label: "Export XML", icon: "↓", href: `/api/v1/panorama/models/${encodeURIComponent(model.id)}/xml` },
    ];
    if (editable) actions.push(
      { label: "Rename", icon: "✎", act: "rename-panorama", data: { id: model.id } },
      { sep: true },
      { label: "Delete", icon: "🗑", act: "delete-panorama", data: { id: model.id }, danger: true },
    );
    return `<tr data-name="${esc(`${model.name} ${app ? app.name : ""}`.toLowerCase())}">
      <td><div class="artifact-name"><span class="chip">ARCHI</span>
        <a href="#/panorama/models/${encodeURIComponent(model.id)}"><b>${esc(model.name)}</b></a></div>
        <div class="muted" style="font-size:12px; padding-left:54px">${esc(model.id)}</div></td>
      <td>${app ? `<span class="mi-icon">📦</span>${esc(app.name)}` : `<span class="muted">Missing application</span>`}</td>
      <td><span class="chip">ArchiMate 3.2</span></td>
      <td class="muted">r${model.revision}</td>
      <td class="muted" data-sort="${model.updatedAt || 0}">${esc(fmtTime(model.updatedAt))}</td>
      <td class="row-actions">${dropdown("⋯", "icon-btn", actions)}</td>
    </tr>`;
  };

  view.innerHTML = `<div id="panorama-root">
    <div class="between">
      <div>
        <h1>Architecture models</h1>
        <p class="muted" style="margin:0">ArchiMate 3.2 models stored as interoperable Open Exchange XML.</p>
      </div>
      ${writable.length ? dropdown("Create new", "btn", [
        { label: "Blank ArchiMate model", icon: "◇", act: "new-panorama" },
        { label: "Import Open Exchange XML…", icon: "📥", act: "import-panorama" },
      ]) : ""}
    </div>
    ${applications.length ? "" : `<div class="card empty" style="margin-top:16px">
      <h2>Create an application first</h2>
      <p>Every architecture model belongs to a Process Application and inherits its sharing permissions.</p>
      <a class="btn ghost" href="#/modeler">Open Modeler</a>
    </div>`}
    <div class="card" style="padding:0; margin-top:16px">
      <table data-dt-key="panorama-models">
        <thead><tr><th>Model</th><th>Application</th><th>Notation</th><th>Revision</th><th>Last changed</th><th></th></tr></thead>
        <tbody>${models.map(row).join("") || `<tr><td colspan="6" class="empty">${writable.length
          ? "No architecture models yet — create a blank model or import Open Exchange XML."
          : "No architecture models are visible to you."}</td></tr>`}</tbody>
      </table>
    </div>
  </div>`;

  const panoramaRoot = document.getElementById("panorama-root");

  const chooseApplication = () => {
    if (!writable.length) return null;
    if (writable.length === 1) return writable[0];
    const choices = writable.map((app, i) => `${i + 1}) ${app.name}`).join("\n");
    const answer = window.prompt(`Store the architecture model in which application?\n\n${choices}\n\nEnter a number:`, "1");
    if (answer == null) return null;
    const selected = writable[Number(answer) - 1];
    if (!selected) toast("No such application", "err");
    return selected || null;
  };

  const saveImported = async (xml, suggestedName) => {
    const validation = await api("POST", "/api/v1/panorama/validate", xml, true);
    if (!validation.valid) {
      const first = validation.problems && validation.problems[0];
      throw new Error(first ? first.message : "The document is not valid ArchiMate Open Exchange XML");
    }
    const app = chooseApplication();
    if (!app) return;
    const name = window.prompt("Model name:", validation.name || suggestedName || "Architecture model");
    if (name == null || !name.trim()) return;
    await api("POST", "/api/v1/panorama/models", {
      applicationId: app.id, name: name.trim(), notation: "archimate-3.2", xml,
    });
    toast(`${name.trim()} imported`, "ok");
    return route();
  };

  onMenuAction(panoramaRoot, async (act, button) => {
    try {
      if (act === "import-panorama") {
        const input = document.createElement("input");
        input.type = "file";
        input.accept = ".xml,.archimate,application/xml,text/xml";
        input.addEventListener("change", async () => {
          const file = input.files && input.files[0];
          if (!file) return;
          try { await saveImported(await file.text(), file.name.replace(/\.(archimate\.)?xml$/i, "")); }
          catch (e) { toast(e.message, "err"); }
        });
        input.click();
      }
      if (act === "new-panorama") {
        const app = chooseApplication();
        if (!app) return;
        const name = window.prompt("Model name:", "Application landscape");
        if (name == null || !name.trim()) return;
        const identifier = `model-${globalThis.crypto && globalThis.crypto.randomUUID ? globalThis.crypto.randomUUID() : Date.now()}`;
        const xml = `<?xml version="1.0" encoding="UTF-8"?>\n` +
          `<model xmlns="http://www.opengroup.org/xsd/archimate/3.0/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" identifier="${identifier}">\n` +
          `  <name xml:lang="en">${esc(name.trim())}</name>\n</model>\n`;
        await api("POST", "/api/v1/panorama/models", {
          applicationId: app.id, name: name.trim(), notation: "archimate-3.2", xml,
        });
        toast(`${name.trim()} created`, "ok");
        return route();
      }
      if (act === "rename-panorama") {
        const model = models.find((item) => item.id === button.dataset.id);
        if (!model) return;
        const name = window.prompt("Model name:", model.name);
        if (name == null || !name.trim() || name.trim() === model.name) return;
        await api("PUT", `/api/v1/panorama/models/${encodeURIComponent(model.id)}`, {
          expectedRevision: model.revision, name: name.trim(),
        });
        toast("Architecture model renamed", "ok");
        return route();
      }
      if (act === "delete-panorama") {
        const model = models.find((item) => item.id === button.dataset.id);
        if (!model || !window.confirm(`Delete architecture model “${model.name}”?`)) return;
        await api("DELETE", `/api/v1/panorama/models/${encodeURIComponent(model.id)}`);
        toast("Architecture model deleted", "ok");
        return route();
      }
    } catch (e) {
      toast(e.message, "err");
    }
  });
}

// The derived landscape mesh (ADR-0211). Panorama's landing view: it is computed
// from what this server already holds, so it has something to show before anybody
// has modeled anything.
async function viewPanoramaLandscape() {
  const gen = navGen;
  const mod = await import("./panorama-mesh.js");
  if (superseded(gen)) return;
  await mod.mountPanoramaMesh(view, { api, toast });
}

async function viewPanoramaModel(id) {
  const gen = navGen;
  const mod = await import("./panorama-viewer.js");
  if (superseded(gen)) return;
  await mod.mountPanoramaViewer(view, { api, toast, id });
}

// resolveProject looks up a project's display name so the editor can render a
// breadcrumb link back to it. Returns {id, name} or null when the id is empty
// or unknown (a new/ungrouped artifact, or a best-effort lookup that failed —
// in which case the editor just falls back to a Home-only trail).
async function resolveProject(projectId) {
  if (!projectId) return null;
  try {
    const projects = await api("GET", "/api/v1/applications");
    const p = projects.find((x) => x.id === projectId);
    return p ? { id: p.id, name: p.name } : null;
  } catch { return null; }
}

async function viewEditor(key, projectId) {
  const gen = navGen;
  const mod = await import("./editor.js");
  const project = await resolveProject(projectId);
  // The mount's own generation guard can't see this wrapper's pre-mount awaits: a
  // superseded wrapper would run the mount to completion and clobber the newer view.
  if (superseded(gen)) return;
  await mod.mountEditor(view, { api, toast, key, projectId, project });
}

async function viewEditorDraft(id) {
  const gen = navGen;
  const mod = await import("./editor.js");
  // An existing draft carries its own projectId; resolve it so the editor can
  // offer a "back to project" breadcrumb (the route alone doesn't name it).
  let projectId = "";
  try {
    const drafts = await api("GET", "/api/v1/drafts");
    const d = drafts.find((x) => x.processId === id);
    projectId = (d && d.projectId) || "";
  } catch { /* best-effort: fall back to a Home-only crumb */ }
  const project = await resolveProject(projectId);
  if (superseded(gen)) return; // a newer navigation landed during the pre-mount fetches
  await mod.mountEditor(view, { api, toast, draftId: id, projectId, project });
}

async function viewFormEditor(formId, projectId) {
  const gen = navGen;
  const mod = await import("./form-editor.js");
  if (superseded(gen)) return; // don't mount over a newer view after the dynamic import
  await mod.mountFormEditor(view, { api, toast, formId, projectId });
}

async function viewLive(key, instance) {
  const mod = await import("./editor.js");
  await mod.mountLive(view, { api, toast, key, instance });
}

async function viewCollaboration(key) {
  const mod = await import("./editor.js");
  await mod.mountCollaboration(view, { api, toast, key });
}

async function viewInstanceReplay(key) {
  const mod = await import("./editor.js");
  await mod.mountInstanceReplay(view, { api, toast, key });
}

// ---------- Router ----------
// viewDmnViewer renders a referenced DMN model: its decision requirements graph
// (decisions, input data, and the requirements between them) drawn read-only from
// the graph the embedded engine exposes, with a Bearbeiten button that opens the
// embedded dmn-js editor (ADR-0062) on the same model. The SVG itself is a
// picture, not an edit surface — editing happens in the modeler overlay, and on
// save the view re-renders from the updated model.
async function viewDmnViewer(refId) {
  const gen = navGen;
  view.innerHTML = `<div class="card"><p class="muted">Loading decision model…</p></div>`;
  let g, ref = null;
  try {
    // The graph carries no model handle, so the reference is fetched alongside it
    // to know which model the Bearbeiten button should open.
    const [graph, refs] = await Promise.all([
      api("GET", `/api/v1/dmnrefs/${encodeURIComponent(refId)}/graph`),
      api("GET", "/api/v1/dmnrefs").catch(() => []),
    ]);
    g = graph;
    ref = (refs || []).find((r) => r.id === refId) || null;
  } catch (e) {
    if (superseded(gen)) return;
    view.innerHTML = `<div class="card empty"><h1>Could not load model</h1><p class="muted">${esc(e.message)}</p></div>`;
    return;
  }
  if (superseded(gen)) return; // navigated away while the graph loaded
  const title = g.modelName || (ref && ref.name) || "DMN model";
  // Back link mirrors a process/form opened from a project: return to the owning
  // project (resolved from the ref's projectId) rather than the Modeler home, so
  // the operator lands where they came from. Ungrouped models keep "← Modeler".
  const projId = (ref && ref.projectId) || "";
  const back = projId
    ? `<a href="#/modeler/p/${encodeURIComponent(projId)}" id="dmn-back">← Project</a>`
    : `<a href="#/modeler" id="dmn-back">← Modeler</a>`;
  const resolveBack = async () => {
    if (!projId) return;
    try {
      const projects = await api("GET", "/api/v1/applications");
      const p = (projects || []).find((x) => x.id === projId);
      const el = document.getElementById("dmn-back");
      if (p && el && !superseded(gen)) el.textContent = `← ${p.name}`;
    } catch { /* keep the generic "← Project" label, which still links correctly */ }
  };
  const editBtn = ref && ref.modelRef
    ? `<button class="btn" id="dmn-edit" title="Edit this decision in Atlas">Bearbeiten</button>` : "";
  // Re-render from the updated model once the editor closes on a save; also
  // resolves the back link's project name.
  const wireEdit = () => {
    const b = document.getElementById("dmn-edit");
    if (b) b.addEventListener("click", async () => {
      await editDmnRef({ id: ref.id, modelRef: ref.modelRef, projectId: ref.projectId || "", name: ref.name }, () => viewDmnViewer(refId));
    });
    resolveBack();
  };
  if (!g.valid) {
    view.innerHTML = `<div class="card">${back}
      <div class="between"><h1>${esc(title)}</h1><div class="row">${editBtn}</div></div>
      <p class="muted">${g.resolved ? "This model has errors and can't be shown:" : "This reference doesn't resolve to a temis model."}</p>
      <pre class="muted" style="white-space:pre-wrap">${esc(g.message || "unavailable")}</pre></div>`;
    wireEdit();
    return;
  }
  view.innerHTML = `<div class="card">${back}
    <div class="between">
      <h1>${esc(title)} <span class="muted" style="font-size:14px;font-weight:normal">· DMN view</span></h1>
      <div class="row">${editBtn}</div>
    </div>
    <div id="dmn-canvas" style="overflow:auto;border:1px solid #e5e7eb;border-radius:10px;background:var(--diagram-bg);padding:8px">${renderDrgSvg(g)}</div>
    <p class="muted" style="font-size:12px">Diese Entscheidung kann direkt in Atlas bearbeitet (<b>Bearbeiten</b>) oder in einem Business-Rule-Task über den Decision-Picker des Modelers verwendet werden.</p></div>`;
  wireEdit();
}

// borderPoint returns the point on a box's border (centre cx,cy, size w×h) in the
// direction of (tx,ty), so a requirement arrow lands on the box edge, not its
// centre.
function borderPoint(cx, cy, w, h, tx, ty) {
  const dx = tx - cx, dy = ty - cy;
  if (dx === 0 && dy === 0) return [cx, cy];
  const sx = dx !== 0 ? (w / 2) / Math.abs(dx) : Infinity;
  const sy = dy !== 0 ? (h / 2) / Math.abs(dy) : Infinity;
  const s = Math.min(sx, sy);
  return [cx + dx * s, cy + dy * s];
}

// renderDrgSvg draws a model's decision requirements graph as an SVG. It uses the
// authored DMNDI bounds when the model has a diagram; otherwise it lays the graph
// out in layers (input data at the bottom, decisions stacked above by requirement
// depth). Read-only: no interaction, just a faithful picture.
function renderDrgSvg(g) {
  const NW = 168, NH = 64, GAPX = 36, GAPY = 60, PAD = 24;
  const hasDI = (g.nodes || []).some((n) => n.width > 0);
  let placed;
  if (hasDI) {
    placed = g.nodes.map((n) => ({ n, x: n.x || 0, y: n.y || 0, w: n.width || NW, h: n.height || NH }));
  } else {
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
    g.nodes.forEach((n) => lvl(n.id, new Set()));
    const maxL = Math.max(0, ...Object.values(level));
    const byLevel = {};
    g.nodes.forEach((n) => { (byLevel[level[n.id]] = byLevel[level[n.id]] || []).push(n); });
    placed = [];
    for (let L = 0; L <= maxL; L++) {
      (byLevel[L] || []).forEach((n, i) =>
        placed.push({ n, x: PAD + i * (NW + GAPX), y: PAD + (maxL - L) * (NH + GAPY), w: NW, h: NH }));
    }
  }
  if (!placed.length) return `<p class="muted" style="padding:16px">This model has no decisions to show.</p>`;
  const pos = {};
  placed.forEach((p) => { pos[p.n.id] = p; });

  const edges = (g.edges || []).map((e) => {
    const a = pos[e.source], b = pos[e.target];
    if (!a || !b) return "";
    const ax = a.x + a.w / 2, ay = a.y + a.h / 2, bx = b.x + b.w / 2, by = b.y + b.h / 2;
    const [x1, y1] = borderPoint(ax, ay, a.w, a.h, bx, by);
    const [x2, y2] = borderPoint(bx, by, b.w, b.h, ax, ay);
    const dash = e.type === "knowledgeRequirement" ? ` stroke-dasharray="5 4"` : "";
    return `<line x1="${x1.toFixed(1)}" y1="${y1.toFixed(1)}" x2="${x2.toFixed(1)}" y2="${y2.toFixed(1)}" stroke="#94a3b8" stroke-width="1.5"${dash} marker-end="url(#drg-arrow)"/>`;
  }).join("");

  const nodes = placed.map(({ n, x, y, w, h }) => {
    const input = n.type === "inputData";
    const bkm = n.type === "businessKnowledgeModel";
    const fill = input ? "#eff6ff" : bkm ? "#f5f3ff" : "#ffffff";
    const stroke = input ? "#3b82f6" : bkm ? "#8b5cf6" : "#111827";
    const rx = input ? h / 2 : 10;
    const sub = input ? (n.dataType || "input data") : bkm ? "knowledge model" : (n.hasTable ? "decision table" : "decision");
    return `<g>
      <rect x="${x}" y="${y}" width="${w}" height="${h}" rx="${rx}" fill="${fill}" stroke="${stroke}" stroke-width="1.5"/>
      <text x="${x + w / 2}" y="${y + h / 2 - 3}" text-anchor="middle" font-size="13" font-weight="600" fill="#111827">${esc(n.name || n.id)}</text>
      <text x="${x + w / 2}" y="${y + h / 2 + 14}" text-anchor="middle" font-size="10.5" fill="#6b7280">${esc(sub)}</text>
    </g>`;
  }).join("");

  const minX = Math.min(...placed.map((p) => p.x)) - PAD;
  const minY = Math.min(...placed.map((p) => p.y)) - PAD;
  const W = Math.max(...placed.map((p) => p.x + p.w)) + PAD - minX;
  const H = Math.max(...placed.map((p) => p.y + p.h)) + PAD - minY;
  return `<svg viewBox="${minX.toFixed(0)} ${minY.toFixed(0)} ${W.toFixed(0)} ${H.toFixed(0)}" width="${W.toFixed(0)}" height="${H.toFixed(0)}" style="max-width:100%;height:auto;display:block;font-family:system-ui,-apple-system,sans-serif">
    <defs><marker id="drg-arrow" markerWidth="10" markerHeight="8" refX="8" refY="3" orient="auto" markerUnits="strokeWidth">
      <path d="M0,0 L8,3 L0,6 z" fill="#94a3b8"/></marker></defs>
    ${edges}${nodes}</svg>`;
}

// setTitle sets the browser tab / history title with the distinctive part first, so
// several open Atlas tabs are told apart at a glance. "" falls back to plain "Atlas".
function setTitle(label) {
  document.title = label ? `${label} · Atlas` : "Atlas";
}

// routeTitle derives a tab title from the route alone (set immediately on navigation).
// Views with a dynamic subject — a diagram, an instance, a decision — refine it once
// their data loads (see setTitle calls in the editor/live/replay mounts).
// viewRepository is the community repository gallery (ADR-0081): browse the
// curated catalog of connectors, service tasks and script tasks and install one
// into this server's template store. The trust split is the load-bearing UI
// signal — a data-only connector/service task installs in one click ("Data only"),
// while a script task carries code, so it reads "Runs code" and installs through a
// review affordance (and is admin-gated server-side). No secret ever travels in a
// shared package.
async function viewRepository() {
  view.innerHTML = `
    <div class="between">
      <h1>Repository</h1>
      <button class="btn neutral" id="repo-refresh" title="Reload the repository catalog">Refresh</button>
    </div>
    <p class="muted">Connectors, service tasks and scripts the community already built,
    packaged as element templates. Install one and it lands in your palette ready to
    configure. Data-only connectors install in a click; a script task carries code, so it
    is imported for review. Credentials never travel in a shared package — you connect
    those on your server.</p>
    <div class="ops-toolbar">
      <input id="repo-q" class="filter-input" type="search" placeholder="Search connectors, tasks and scripts…" aria-label="Search the repository">
      <div class="seg" id="repo-kinds" role="tablist">
        <button class="active" data-kind="" role="tab" title="Show all packages">All</button>
        <button data-kind="connector" role="tab" title="Show connectors only">Connectors</button>
        <button data-kind="service-task" role="tab" title="Show service tasks only">Service tasks</button>
        <button data-kind="script-task" role="tab" title="Show script tasks only">Script tasks</button>
      </div>
    </div>
    <div id="repo-grid" class="repo-grid"><div class="card empty">Loading…</div></div>`;

  const grid = document.getElementById("repo-grid");
  const kindLabel = { "connector": "Connector", "service-task": "Service task", "script-task": "Script task" };
  let packages = [];
  let installed = new Set();
  let kind = "";

  const render = () => {
    const q = document.getElementById("repo-q").value.trim().toLowerCase();
    const list = packages.filter((p) => {
      if (kind && p.kind !== kind) return false;
      if (q && !(`${p.title} ${p.description} ${p.author}`).toLowerCase().includes(q)) return false;
      return true;
    });
    if (!list.length) {
      grid.innerHTML = `<div class="card empty">Nothing matches. Try another term or tab.</div>`;
      return;
    }
    grid.innerHTML = list.map((p) => {
      const isInstalled = installed.has(p.id);
      const trust = p.carriesCode
        ? '<span class="pill warn"><span class="dot"></span>Runs code</span>'
        : '<span class="pill ok"><span class="dot"></span>Data only</span>';
      const action = isInstalled
        ? `<button class="btn ghost danger" data-act="uninstall" data-id="${esc(p.id)}" title="Remove this template from your server">Remove</button>`
        : p.carriesCode
          ? `<button class="btn neutral" data-act="install" data-id="${esc(p.id)}" title="Review the code, then install this template">Review &amp; install</button>`
          : `<button class="btn" data-act="install" data-id="${esc(p.id)}" title="Install this template into your palette">Install</button>`;
      const installedTag = isInstalled ? '<span class="pill ok"><span class="dot"></span>Installed</span>' : "";
      return `<div class="repo-card card">
        <div class="repo-head">
          <div class="repo-title">
            <h3>${esc(p.title)}</h3>
            <div class="muted repo-author">${esc(p.author)}</div>
          </div>
          <span class="chip">${esc(kindLabel[p.kind] || p.kind)}</span>
        </div>
        <p class="repo-desc">${esc(p.description)}</p>
        <div class="repo-meta">
          <span class="chip">v${esc(p.version)}</span>
          <span class="chip">Atlas ${esc(p.engineCompat)}</span>
          ${trust}
          ${installedTag}
        </div>
        <div class="repo-foot">${action}</div>
      </div>`;
    }).join("");
  };

  const load = async () => {
    grid.innerHTML = `<div class="card empty">Loading…</div>`;
    try {
      const [pkgs, inst] = await Promise.all([
        api("GET", "/api/v1/repository/packages"),
        api("GET", "/api/v1/repository/installed"),
      ]);
      packages = pkgs || [];
      installed = new Set((inst || []).map((r) => r.id));
      render();
    } catch (e) {
      grid.innerHTML = `<div class="card empty">${esc(e.message)}</div>`;
    }
  };

  grid.addEventListener("click", async (e) => {
    const btn = e.target.closest("button[data-act]");
    if (!btn) return;
    const id = btn.dataset.id;
    const pkg = packages.find((p) => p.id === id);
    const name = pkg ? pkg.title : "Package";
    btn.disabled = true;
    try {
      if (btn.dataset.act === "install") {
        const res = await api("POST", `/api/v1/repository/packages/${encodeURIComponent(id)}/install`);
        installed.add(id);
        toast(res && res.reviewRequired ? `${name} imported for review` : `${name} installed`, "ok");
      } else {
        await api("DELETE", `/api/v1/repository/installed/${encodeURIComponent(id)}`);
        installed.delete(id);
        toast(`${name} removed`, "ok");
      }
      render();
    } catch (err) {
      toast(err.message, "err");
      btn.disabled = false;
    }
  });

  document.getElementById("repo-q").addEventListener("input", render);
  document.getElementById("repo-kinds").addEventListener("click", (e) => {
    const b = e.target.closest("button[data-kind]");
    if (!b) return;
    kind = b.dataset.kind;
    document.querySelectorAll("#repo-kinds button").forEach((x) => x.classList.toggle("active", x === b));
    render();
  });
  document.getElementById("repo-refresh").addEventListener("click", load);
  await load();
}

function routeTitle(path) {
  const opsInst = path.match(/^#\/operations\/i\/(\d+)$/);
  if (opsInst) return `Instance ${opsInst[1]} · Operations`;
  const rules = [
    [/^#\/(console)?$/, "Console"],
    [/^#\/console\/engine$/, "Engine · Console"],
    [/^#\/console\/logs$/, "Logs · Console"],
    [/^#\/console\/backup$/, "Backup · Console"],
    [/^#\/console\/org$/, "Organization · Console"],
    [/^#\/console\/connectors$/, "Connectors · Console"],
    [/^#\/modeler\/new/, "New diagram · Modeler"],
    [/^#\/modeler\/form\/new/, "New form · Modeler"],
    [/^#\/modeler\/form\//, "Form · Modeler"],
    [/^#\/modeler\/dmn\//, "Decision · Modeler"],
    [/^#\/modeler\/(d|draft)\//, "Diagram · Modeler"],
    [/^#\/modeler\/p\//, "Project · Modeler"],
    [/^#\/modeler\/repository$/, "Repository · Modeler"],
    [/^#\/modeler$/, "Modeler"],
    [/^#\/tasks\/start$/, "Start a process · Tasks"],
    [/^#\/tasks\/t\//, "Task · Tasks"],
    [/^#\/tasks$/, "Tasks"],
    [/^#\/operations\/decisions\//, "Decision · Operations"],
    [/^#\/operations\/decisions$/, "Decisions · Operations"],
    [/^#\/operations\/c\//, "Collaboration · Operations"],
    [/^#\/operations\/p\//, "Live view · Operations"],
    [/^#\/operations$/, "Instances · Operations"],
    [/^#\/panorama\/landscape$/, "Landscape · Panorama"],
    [/^#\/panorama\/models\//, "Architecture view · Panorama"],
    [/^#\/panorama$/, "Models · Panorama"],
  ];
  for (const [re, label] of rules) if (re.test(path)) return label;
  return "";
}

async function route() {
  // Any navigation closes the app switcher and tears down an editor/live view.
  document.getElementById("drawer").hidden = true;
  document.getElementById("scrim").hidden = true;
  if (window.__atlasCleanup) { try { window.__atlasCleanup(); } catch { /* ignore */ } }
  navGen++; // supersede any view handler still awaiting from a previous navigation

  const hash = location.hash || "#/console";
  const [path, arg] = [hash.replace(/\?.*$/, ""), hash];
  let appId = "console";

  if (path.startsWith("#/modeler")) appId = "modeler";
  else if (path.startsWith("#/tasks")) appId = "tasks";
  else if (path.startsWith("#/operations")) appId = "operations";
  else if (path.startsWith("#/panorama")) appId = "panorama";

  // Gate the whole app behind login when enforcement is on and no session is
  // active. Auth off (the default) skips this entirely.
  if (!AUTH.loaded) await loadAuth();
  if (AUTH.enabled && !AUTH.user) {
    document.getElementById("app-name").textContent = "Atlas";
    document.getElementById("topnav").innerHTML = "";
    paintApps(); // nobody is signed in, so nobody holds a role: the drawer says so too
    syncIncidentBadge(""); // the login screen has no nav to badge, and must not poll
    updateAccount();
    return viewLogin();
  }

  setChrome(appId, path);
  setTitle(routeTitle(path));
  updateAccount();
  window.scrollTo(0, 0);

  try {
    if (path === "#/" || path === "#/console") return await viewConsoleDashboard();
    if (path === "#/console/engine") return await viewConsoleEngine();
    if (path === "#/console/logs") return await viewConsoleLogs();
    if (path === "#/console/backup") return await viewConsoleBackup();
    if (path === "#/console/org") return await viewConsoleOrg();
    if (path === "#/console/connectors") return await viewConsoleConnectors();
    if (path === "#/console/ai-access") {
      const gen = navGen;
      return await viewAIAccess({ api, toast, view, isSuperseded: () => superseded(gen) });
    }
    if (path === "#/console/audit") return await viewConsoleAudit();
    if (path === "#/modeler") return await viewModelerHome();
    if (path === "#/modeler/repository") return await viewRepository();
    const pd = path.match(/^#\/modeler\/p\/(.+)$/);
    if (pd) return await viewProjectDetail(decodeURIComponent(pd[1]));
    const dnew = path.match(/^#\/modeler\/new(?:\/p\/(.+))?$/);
    if (dnew) return await viewEditor(null, dnew[1] ? decodeURIComponent(dnew[1]) : "");
    const fnew = path.match(/^#\/modeler\/form\/new(?:\/p\/(.+))?$/);
    if (fnew) return await viewFormEditor(null, fnew[1] ? decodeURIComponent(fnew[1]) : "");
    const fe = path.match(/^#\/modeler\/form\/e\/(.+)$/);
    if (fe) return await viewFormEditor(decodeURIComponent(fe[1]));
    const dm = path.match(/^#\/modeler\/draft\/(.+)$/);
    if (dm) return await viewEditorDraft(decodeURIComponent(dm[1]));
    const dv = path.match(/^#\/modeler\/dmn\/(.+)$/);
    if (dv) return await viewDmnViewer(decodeURIComponent(dv[1]));
    const m = path.match(/^#\/modeler\/d\/(\d+)$/);
    if (m) return await viewEditor(Number(m[1]));
    if (path === "#/tasks") return await viewTasks();
    if (path === "#/tasks/start") return await viewStartProcess();
    // A single task can be deep-linked (…/t/{jobKey}) — the Operations live view
    // links a running instance's active user task straight to its form here, so an
    // operator jumps from "where is the token" to "work the task" in one click.
    const tk = path.match(/^#\/tasks\/t\/(\d+)$/);
    if (tk) return await viewTasks(Number(tk[1]));
    if (path === "#/operations") return await viewInstances();
    if (path === "#/operations/incidents") return await viewIncidents();
    if (path === "#/operations/workers") return await viewWorkers();
    if (path === "#/operations/outbox") return await viewMailOutbox();
    if (path === "#/operations/ad-mock") return await viewADMockDirectory();
    if (path === "#/operations/sql-mock") return await viewSQLMockJournal();
    if (path === "#/operations/decisions") return await viewDecisions();
    if (path === "#/operations/call-activities") return await viewCallActivities();
    if (path === "#/panorama/landscape") return await viewPanoramaLandscape();
    if (path === "#/panorama") return await viewPanoramaModels();
    const pm = path.match(/^#\/panorama\/models\/(.+)$/);
    if (pm) return await viewPanoramaModel(decodeURIComponent(pm[1]));
    // Drill into one decision's evaluations (its "instances"). The id is URL-encoded
    // because a DMN decision id may contain spaces or other reserved characters.
    const dd = path.match(/^#\/operations\/decisions\/(.+)$/);
    if (dd) return await viewDecisionDetail(decodeURIComponent(dd[1]));
    // A specific instance can be deep-linked (…/i/{instanceKey}) — the Modeler's
    // Deploy & run builds this so a roundtrip lands straight on the started
    // instance. The plain form defaults the picker to "All instances".
    const li = path.match(/^#\/operations\/p\/(\d+)\/i\/(\d+)$/);
    if (li) return await viewLive(Number(li[1]), Number(li[2]));
    const lm = path.match(/^#\/operations\/p\/(\d+)$/);
    if (lm) return await viewLive(Number(lm[1]));
    const cm = path.match(/^#\/operations\/c\/(\d+)$/);
    if (cm) return await viewCollaboration(Number(cm[1]));
    // A single instance can be replayed step by step (…/i/{instanceKey}) — the
    // token walks the diagram in activation order (ADR-0046).
    const im = path.match(/^#\/operations\/i\/(\d+)$/);
    if (im) return await viewInstanceReplay(Number(im[1]));
    if (appId !== "console" && appId !== "modeler" && appId !== "tasks" && appId !== "panorama") return viewComingSoon(appId);
    // Unknown route → dashboard.
    location.hash = "#/console";
  } catch (e) {
    view.innerHTML = `<div class="card empty"><h1>Something went wrong</h1><p class="muted">${esc(e.message)}</p></div>`;
  } finally {
    // One integration point for every data table: after a view renders, give each of
    // its tables shared column sorting + a per-column filter row (table.js). A table
    // opts out with class "no-enhance"; enhanceTable itself skips any table without a
    // header row and never double-enhances, so this is safe to run on every route.
    enhanceViewTables();
  }
}

// enhanceViewTables applies the shared sort/filter enhancer to every eligible table
// currently in the main view. It runs once per navigation (not as a live observer),
// so it never watches the modeler's heavy SVG; each enhanced table then keeps itself
// current via its own lightweight tbody observer as rows refresh.
function enhanceViewTables() {
  for (const t of view.querySelectorAll("table:not(.no-enhance)")) {
    enhanceTable(t, { key: t.dataset.dtKey || undefined });
  }
}

initShell();
window.addEventListener("hashchange", route);
loadAuth().then(route);
// Reconcile the org-wide brand colour with the server. The inline bootstrap in
// index.html has already applied the cached palette (no flash); this confirms or
// updates it once the network responds, and is the first paint of the accent for a
// browser that has no cache yet.
syncFromServer();
// Paint the brand logo from the cached flag immediately (no flash of the "A" for a
// branded instance), then reconcile the actual presence with the server.
applyLogo(hasLogoCached());
syncLogoFromServer();
