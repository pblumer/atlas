// Atlas web UI — buildless app shell (ADR-0012). A tiny hash router swaps views
// into #view; heavy widgets (the BPMN modeler) are loaded on demand by editor.js.

const view = document.getElementById("view");

// ---------- API ----------
export async function api(method, path, body, isXML) {
  const opts = { method };
  if (body !== undefined) {
    opts.body = isXML ? body : JSON.stringify(body);
    opts.headers = { "Content-Type": isXML ? "application/xml" : "application/json" };
  }
  const res = await fetch(path, opts);
  const text = await res.text();
  let data = text;
  try { data = text ? JSON.parse(text) : null; } catch { /* keep text */ }
  if (!res.ok) throw new Error((data && data.error) || res.statusText);
  return data;
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
      `<button type="button" data-act="logout">Log out</button>`;
  } else {
    btn.textContent = "A";
    btn.title = AUTH.enabled ? "Account" : "Single-user mode";
    if (menu) menu.innerHTML = `<div class="mlabel">Single-user mode</div>`;
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
      <form id="login-form">
        <label class="field">Username
          <input name="username" autocomplete="username" autofocus required></label>
        <label class="field">Password
          <input name="password" type="password" autocomplete="current-password" required></label>
        <div class="row" style="margin-top:6px"><button class="btn" type="submit">Sign in</button></div>
        <p id="login-error" class="muted" hidden></p>
      </form>
    </div>`;
  const f = document.getElementById("login-form");
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
  for (const m of document.querySelectorAll(".dropdown-menu:not([hidden])")) m.hidden = true;
}
document.addEventListener("click", (e) => {
  const toggle = e.target.closest(".dropdown-toggle");
  if (toggle) {
    e.preventDefault(); e.stopPropagation();
    const menu = toggle.nextElementSibling, wasOpen = menu && !menu.hidden;
    closeAllMenus();
    if (menu) menu.hidden = wasOpen;
    return;
  }
  closeAllMenus();
});

// dropdown renders a trigger button + its menu. items entries are:
//   { label, icon?, href? } — a link; or { label, icon?, act, data?, danger? } — a
//   JS action dispatched by onMenuAction; or { sep:true } | { header:"…" }.
function dropdown(label, triggerClass, items) {
  const body = items.map((it) => {
    if (it.sep) return `<div class="sep"></div>`;
    if (it.header) return `<div class="mlabel">${esc(it.header)}</div>`;
    const icon = `<span class="mi-icon">${it.icon || ""}</span>`;
    if (it.href) return `<a href="${it.href}">${icon}${esc(it.label)}</a>`;
    const data = it.data ? Object.entries(it.data).map(([k, v]) => `data-${k}="${esc(v)}"`).join(" ") : "";
    return `<button type="button" data-act="${esc(it.act)}" ${data}${it.danger ? ' class="danger"' : ""}>${icon}${esc(it.label)}</button>`;
  }).join("");
  return `<div class="dropdown"><button type="button" class="${triggerClass} dropdown-toggle">${esc(label)}</button>` +
    `<div class="dropdown-menu" hidden>${body}</div></div>`;
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
const APPS = [
  { id: "console", name: "Console", route: "#/console", on: true },
  { id: "modeler", name: "Modeler", route: "#/modeler", on: true },
  { id: "tasks", name: "Tasks", route: "#/tasks", on: true },
  { id: "operations", name: "Operations", route: "#/operations", on: true },
  { id: "insights", name: "Insights", route: "#/insights", on: false },
];

// Secondary (in-app) navigation.
const TOPNAV = {
  console: [
    { name: "Dashboard", route: "#/console" },
    { name: "Engine", route: "#/console/engine" },
    { name: "Logs", route: "#/console/logs" },
    { name: "Organization", route: "#/console/org" },
  ],
  modeler: [{ name: "Home", route: "#/modeler" }],
  operations: [
    { name: "Instances", route: "#/operations" },
    { name: "Decisions", route: "#/operations/decisions" },
  ],
  tasks: [{ name: "Inbox", route: "#/tasks" }, { name: "Start", route: "#/tasks/start" }], insights: [],
};

// Connectors are the sibling engines Atlas hands work off to. They live under
// Organization because they're an org-wide integration, not per-process wiring.
// "status" is honest about what this single-binary build actually talks to:
//   active — embedded and used at runtime/deploy time;
//   planned — a supported integration that this build isn't wired to yet.
const CONNECTORS = [
  {
    id: "temis", name: "temis", kind: "Decision engine",
    desc: "DMN 1.5 / FEEL. Evaluates business-rule tasks off the processor loop and validates a project's DMN references at deploy time.",
    refs: "ADR-0014 · ADR-0034", status: "active", statusLabel: "embedded",
  },
  {
    id: "clio", name: "clio", kind: "Event store",
    desc: "Durable event log with registered schemas and reduce specs, queried to project read-side state. Not wired into this build yet.",
    refs: "", status: "planned", statusLabel: "not configured",
  },
  {
    id: "http-rest", name: "HTTP REST", kind: "REST API",
    desc: "Calls a model-authored REST endpoint from a service task off the processor loop — method, URL, headers, query parameters, and basic/bearer/apiKey auth (secrets resolved server-side) — writing the JSON response into a result variable. Authored via the REST Outbound Connector service-task type.",
    refs: "ADR-0036 · ADR-0041 · ADR-0067", status: "active", statusLabel: "embedded",
  },
];

// ---------- Shell ----------
function initShell() {
  const drawer = document.getElementById("drawer");
  const scrim = document.getElementById("scrim");
  const openDrawer = () => { drawer.hidden = false; scrim.hidden = false; };
  const closeDrawer = () => { drawer.hidden = true; scrim.hidden = true; };
  document.getElementById("app-switcher").addEventListener("click", openDrawer);
  document.getElementById("drawer-close").addEventListener("click", closeDrawer);
  scrim.addEventListener("click", closeDrawer);

  const nav = document.getElementById("drawer-apps");
  nav.innerHTML = APPS.map((a) =>
    `<a href="${a.route}" data-app="${a.id}">${a.name}${a.on ? "" : '<span class="soon">soon</span>'}</a>`
  ).join("");
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
    if (i && i.version) document.title = `Atlas ${i.version}`;
    initHelpMenu(!!(i && i.docs));
  }).catch(() => { initHelpMenu(false); });
}

// initHelpMenu fills the top-bar "?" dropdown. Its API Explorer entry opens the
// Scalar explorer (/api/docs) in a new tab when the server was started with docs
// enabled; otherwise it shows an inert hint about --docs=false, so the missing
// link is self-explanatory rather than a dead button (ADR-0043). Open/close is
// handled by the shared delegated .dropdown-toggle machinery above.
function initHelpMenu(docsEnabled) {
  const menu = document.getElementById("help-menu");
  if (!menu) return;
  menu.innerHTML = docsEnabled
    ? `<a role="menuitem" href="/api/docs" target="_blank" rel="noopener">API Explorer <span class="ext" aria-hidden="true">↗</span></a>`
    : `<span class="help-note">API Explorer is disabled<br><span class="muted">start the server without <code>--docs=false</code></span></span>`;
}

function setChrome(appId, route) {
  document.getElementById("app-name").textContent =
    (APPS.find((a) => a.id === appId) || {}).name || "Atlas";
  const topnav = document.getElementById("topnav");
  topnav.innerHTML = (TOPNAV[appId] || []).map((t) =>
    `<a href="${t.route}" class="${t.route === route ? "active" : ""}">${t.name}</a>`
  ).join("");
  document.querySelectorAll("#drawer-apps a").forEach((a) =>
    a.classList.toggle("active", a.dataset.app === appId));
  const fullBleed = route.includes("/modeler/d/") || route.includes("/modeler/draft/") || route.includes("/modeler/form/") || route.includes("/modeler/new") || route.includes("/operations/p/");
  document.body.classList.toggle("editor-mode", fullBleed);
  // The Tasks inbox is a wide three-pane layout, so it drops the centered
  // max-width the default content column uses while keeping normal padding.
  document.body.classList.toggle("tasks-mode", appId === "tasks");
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
      </div>
    </div>
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
    <div class="card" id="build-card" style="margin-top:14px">
      <h2>Build</h2>
      <p class="muted">Which commit this running server was built from — check it against the merged code to confirm the deployed binary is up to date.</p>
      <div id="build-info" class="muted">loading…</div>
    </div>`;
  try {
    const [procs, stats, info] = await Promise.all([
      api("GET", "/api/v1/processes"),
      api("GET", "/api/v1/stats"),
      api("GET", "/api/v1/info"),
    ]);
    document.getElementById("e-pi").textContent = stats.activeProcessInstances;
    document.getElementById("e-ei").textContent = stats.activeElementInstances;
    document.getElementById("e-dep").textContent = procs.length;
    document.getElementById("build-info").innerHTML = buildInfoHTML(info);
  } catch (e) { toast(e.message, "err"); }
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
  view.innerHTML = `
    <div class="card">
      <div class="between">
        <h1>Server logs</h1>
        <div class="row" style="gap:12px; align-items:center">
          <label class="field inline" style="margin:0"><input type="checkbox" id="log-follow" checked> Auto-refresh</label>
          <button class="btn neutral" id="log-refresh">Refresh</button>
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
  document.getElementById("log-refresh").addEventListener("click", load);
  const timer = setInterval(() => { if (follow.checked) load(); }, 2000);
  window.__atlasCleanup = () => clearInterval(timer);
}

// userForm renders the create or edit form for a user. In edit mode the username
// is immutable (it identifies existing sessions and references) and the password
// has its own action, so neither appears here.
function userForm(u) {
  const isEdit = !!u;
  const admin = isEdit && (u.roles || []).includes("admin");
  return `<div class="card" style="margin:0 0 14px; background:var(--bg)">
    <h3 style="margin:0 0 8px">${isEdit ? "Edit user" : "New user"}</h3>
    <form class="user-form">
      ${isEdit ? "" : `<label class="field">Username<input name="username" autocomplete="off" required></label>`}
      <label class="field">Display name<input name="displayName" value="${isEdit ? esc(u.displayName || "") : ""}"></label>
      <label class="field">Email<input name="email" type="email" value="${isEdit ? esc(u.email || "") : ""}"></label>
      ${isEdit ? "" : `<label class="field">Password<input name="password" type="password" autocomplete="new-password" required></label>`}
      <label class="field inline"><input type="checkbox" name="admin"${admin ? " checked" : ""}> Administrator</label>
      ${isEdit ? `<label class="field inline"><input type="checkbox" name="disabled"${u.disabled ? " checked" : ""}> Disabled</label>` : ""}
      <div class="row" style="margin-top:4px"><button class="btn" type="submit">${isEdit ? "Save changes" : "Create user"}</button></div>
    </form></div>`;
}

// rolesFrom maps the admin checkbox to a stored role list. Every account keeps the
// base "user" role; ticking Administrator adds "admin" on top.
const rolesFrom = (fd) => fd.get("admin") ? ["admin", "user"] : ["user"];

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

async function saveUser(id, fd, reload) {
  try {
    await api("PATCH", `/api/v1/users/${encodeURIComponent(id)}`, {
      displayName: (fd.get("displayName") || "").trim(),
      email: (fd.get("email") || "").trim(),
      roles: rolesFrom(fd),
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

async function viewConsoleOrg() {
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

  // Load the user roster. A 403 means a signed-in non-admin — show a notice
  // rather than an error card.
  let users = null;
  let denied = false;
  try {
    users = await api("GET", "/api/v1/users");
  } catch (e) {
    denied = /admin/i.test(e.message);
    if (!denied) throw e;
  }

  // Managed connector instances (ADR-0041): operator-configured integrations,
  // secret references only. Today the runtime wires the temis decision connector.
  let connectors = [];
  try { connectors = (await api("GET", "/api/v1/connectors")) || []; } catch { /* leave empty */ }
  const managedRow = (c) => `<tr data-id="${esc(c.id)}">
      <td><span class="chip">${esc(c.name)}</span>
        <span class="muted" style="font-size:12px; margin-left:6px">${esc(c.kind)}</span>
        <div class="muted" style="font-size:12px; margin-top:3px">${esc(c.endpoint)}${
          c.credentialsRef ? ` · token: <code>${esc(c.credentialsRef)}</code>` : " · no token"}</div></td>
      <td>${c.enabled
        ? '<span class="pill ok"><span class="dot"></span>enabled</span>'
        : '<span class="pill warn"><span class="dot"></span>disabled</span>'}</td>
      <td style="text-align:right; white-space:nowrap">
        <button class="btn ghost" data-cact="edit">Edit</button>
        <button class="btn ghost" data-cact="toggle">${c.enabled ? "Disable" : "Enable"}</button>
        <button class="btn ghost danger" data-cact="delete">Delete</button>
      </td></tr>`;
  const managedCard = `
    <div class="card" style="padding:0; margin-top:18px">
      <div class="between" style="padding:16px 18px 0">
        <h2>Configured connectors</h2><button class="btn" id="new-connector">New connector</button>
      </div>
      <p class="muted" style="padding:0 18px; margin:6px 0 12px">Managed temis decision
      connectors a business rule task references by name (ADR-0041/0050). The endpoint is
      stored; the token is a <b>reference</b> resolved from
      <code>ATLAS_CONNECTOR_&lt;REF&gt;_TOKEN</code> at runtime — never stored here.</p>
      <div id="connector-form-slot" style="padding:0 18px"></div>
      <table>
        <thead><tr><th>Connector</th><th>Status</th><th></th></tr></thead>
        <tbody id="connector-rows">${connectors.map(managedRow).join("")
          || `<tr><td colspan="3" class="muted" style="padding:14px 18px">None configured. Business rule tasks marked <i>External (temis connector)</i> resolve by name to these.</td></tr>`}</tbody>
      </table>
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
  const secretRow = (c) => `<tr data-name="${esc(c.name)}">
      <td><span class="chip">${esc(c.name)}</span>
        <div class="muted" style="font-size:12px; margin-top:3px">key <code>${esc(c.keyId)}</code> · updated ${esc(fmtTime(c.updatedAt))}</div></td>
      <td style="text-align:right; white-space:nowrap">
        <button class="btn ghost" data-sact="set">Set value</button>
        <button class="btn ghost danger" data-sact="delete">Delete</button>
      </td></tr>`;
  const secretsCard = secretsState === "denied"
    ? `<div class="card" style="margin-top:18px"><h2>Secrets</h2><p class="muted">Managing secrets requires the admin role.</p></div>`
    : secretsState === "unconfigured"
    ? `<div class="card" style="margin-top:18px"><h2>Secrets</h2><p class="muted">The encrypted
        secret vault is not configured. Start the server with <code>ATLAS_VAULT_KEY</code> (a
        32-byte key, base64 or hex) or <code>ATLAS_VAULT_KEY_FILE</code> to store connector
        credentials here, encrypted at rest (ADR-0069).</p></div>`
    : `<div class="card" style="padding:0; margin-top:18px">
        <div class="between" style="padding:16px 18px 0">
          <h2>Secrets</h2><button class="btn" id="new-secret">New secret</button>
        </div>
        <p class="muted" style="padding:0 18px; margin:6px 0 12px">Credentials a connector's
        <b>token reference</b> resolves to, sealed at rest with AES-256-GCM (ADR-0069). The value
        is <b>never</b> shown after it is set — only its name and metadata. A reference resolves
        from the vault first, then <code>ATLAS_CONNECTOR_&lt;REF&gt;_TOKEN</code>.</p>
        <div id="secret-form-slot" style="padding:0 18px"></div>
        <table>
          <thead><tr><th>Secret</th><th></th></tr></thead>
          <tbody id="secret-rows">${secrets.map(secretRow).join("")
            || `<tr><td colspan="2" class="muted" style="padding:14px 18px">None stored. Add one, then point a connector's token reference at its name.</td></tr>`}</tbody>
        </table>
      </div>`;

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
        <button class="btn ghost" data-act="edit">Edit</button>
        <button class="btn ghost" data-act="password">Password</button>
        <button class="btn ghost" data-act="toggle">${u.disabled ? "Enable" : "Disable"}</button>
        <button class="btn ghost danger" data-act="delete">Delete</button>
      </td></tr>`;

  const usersCard = denied
    ? `<div class="card"><h2>Users</h2><p class="muted">Managing users requires the admin role.</p></div>`
    : `<div class="card" style="padding:0">
        <div class="between" style="padding:16px 18px 0">
          <h2>Users</h2><button class="btn" id="new-user">New user</button>
        </div>
        <p class="muted" style="padding:0 18px; margin:6px 0 12px">${AUTH.enabled
          ? "Login is enforced for this instance."
          : "Login is <b>not</b> enforced — start the server with <code>--auth</code> to require these accounts."}
          Roles are the hook for finer permissions later; today only <span class="chip">admin</span> is enforced (it gates this page).</p>
        <div id="user-form-slot" style="padding:0 18px"></div>
        <table>
          <thead><tr><th>User</th><th>Name</th><th>Roles</th><th>Status</th><th></th></tr></thead>
          <tbody id="user-rows">${(users || []).map(userRow).join("")
            || `<tr><td colspan="5" class="muted" style="padding:14px 18px">No users yet.</td></tr>`}</tbody>
        </table>
      </div>`;

  view.innerHTML = `
    <div class="card">
      <h1>Organization</h1>
      <p class="muted">${AUTH.enabled
        ? `Signed in as <b>${esc((me && me.username) || "")}</b>. This instance has multi-user access enabled.`
        : "Single-user mode: the API and UI are open. Enable login with <code>--auth</code> to enforce the accounts below."}</p>
    </div>
    ${usersCard}
    <div class="card" style="padding:0; margin-top:18px">
      <div class="between" style="padding:16px 18px 0"><h2>Connectors</h2></div>
      <p class="muted" style="padding:0 18px; margin:6px 0 12px">Sibling engines Atlas
      delegates to. Each is an org-wide integration, shared across every process.</p>
      <table><tbody>${CONNECTORS.map(connectorRow).join("")}</tbody></table>
    </div>
    ${managedCard}
    ${secretsCard}`;

  // Connector management is wired before the (admin-gated) user handlers so it
  // works even when the user roster is denied to a non-admin.
  wireConnectorManagement(connectors);
  wireSecretsManagement(secrets, secretsState);

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
          saveUser(u.id, new FormData(ev.target), reload);
        });
        slot.scrollIntoView({ block: "nearest" });
        break;
      case "password": resetUserPassword(u, reload); break;
      case "toggle": toggleUserDisabled(u, reload); break;
      case "delete": deleteUser(u, reload); break;
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

// viewModelerHome is the project landscape: a clean table of projects (each a
// container of artifacts, ADR-0034) plus a collapsible list of deployed
// definitions. Artifact editing happens inside a project (viewProjectDetail),
// which keeps this overview tidy. "Create new" is a single dropdown.
async function viewModelerHome() {
  view.innerHTML = `
    <div class="between">
      <h1>Modeler</h1>
      ${dropdown("Create new", "btn", [
        { label: "New project", icon: "📁", act: "new-project" },
        { sep: true },
        { header: "Blank resources" },
        { label: "BPMN diagram", icon: "⚙", href: "#/modeler/new" },
        { label: "Form", icon: "▤", href: "#/modeler/form/new" },
      ])}
    </div>
    <div class="card" style="padding:0; margin-top:14px">
      <table>
        <thead><tr><th>Name</th><th>Artifacts</th><th>Last changed</th><th></th></tr></thead>
        <tbody id="proj-rows"><tr><td colspan="4" class="empty">Loading…</td></tr></tbody>
      </table>
    </div>
    <h2 style="margin:22px 0 10px"><button class="section-toggle" aria-expanded="${sectionState("deployed")}" data-section="deployed">Deployed</button></h2>
    <div class="section-body" id="sec-deployed"${sectionState("deployed") ? "" : ' hidden'}>
    <div class="card" style="padding:0">
      <table>
        <thead><tr><th>Process</th><th>Latest</th><th>Deployed</th><th></th></tr></thead>
        <tbody id="rows"><tr><td colspan="4" class="empty">Loading…</td></tr></tbody>
      </table>
    </div></div>`;
  for (const t of view.querySelectorAll(".section-toggle"))
    t.addEventListener("click", () => toggleSection(t.dataset.section, t));
  const rows = document.getElementById("rows");
  const projRows = document.getElementById("proj-rows");

  const renderProjects = async () => {
    let projects = [], drafts = [], refs = [], forms = [];
    try {
      [projects, drafts, refs, forms] = await Promise.all([
        api("GET", "/api/v1/projects"),
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
        <td><div class="artifact-name"><span class="mi-icon">📁</span><a href="${href}"><b>${esc(p.name)}</b></a>${visBadge(p)}</div></td>
        <td class="muted">${n}</td>
        <td class="muted">${esc(fmtTime(p.updatedAt))}</td>
        <td class="row-actions">${dropdown("⋯", "icon-btn", items)}</td>
      </tr>`;
    };
    const ungroupedRow = ungrouped.length ? `<tr>
        <td><div class="artifact-name"><span class="mi-icon">🗂</span><a href="#/modeler/p/ungrouped">Ungrouped</a>
          <span class="muted" style="font-size:12px">· not in a project</span></div></td>
        <td class="muted">${ungrouped.length}</td><td class="muted">—</td><td></td>
      </tr>` : "";

    projRows.innerHTML = (projects.map(projectRow).join("") + ungroupedRow) ||
      `<tr><td colspan="4" class="empty">No projects yet. Use <b>Create new</b> to add one.</td></tr>`;
    onMenuAction(projRows, (act, b) => {
      if (act === "rename") renameProject(b.dataset.id, b.dataset.name, renderProjects);
      if (act === "del") deleteProject(b.dataset.id, b.dataset.name, renderProjects);
      if (act === "share") { const pr = projects.find((x) => x.id === b.dataset.id); if (pr) shareProject(pr, renderProjects); }
    });
  };
  onMenuAction(view, (act) => { if (act === "new-project") createProject(renderProjects); });

  const render = async () => {
    try {
      const groups = groupByProcess(await api("GET", "/api/v1/processes"));
      if (!groups.length) {
        rows.innerHTML = `<tr><td colspan="4" class="empty">
          Nothing deployed yet. <a href="#/modeler/new">Create a diagram</a>, save it as a draft, then Deploy &amp; run.</td></tr>`;
        return;
      }
      rows.innerHTML = groups.map((g) => {
        const older = g.versions.length > 1
          ? ` <span class="muted">· ${g.versions.length} versions</span>` : "";
        const label = g.latest.name || g.processId;
        const sub = g.latest.name
          ? `<div class="muted" style="font-size:12px">${esc(g.processId)}</div>` : "";
        return `<tr>
          <td><a href="#/modeler/d/${g.latest.key}"><b>${esc(label)}</b></a>${sub}</td>
          <td>v${g.latest.version}${older}</td>
          <td class="muted">${esc(fmtTime(g.latest.deployedAt))}</td>
          <td style="text-align:right; white-space:nowrap">
            <a class="btn ghost" href="#/modeler/d/${g.latest.key}">Open</a>
            <button class="btn ghost danger" data-del="${esc(g.processId)}">Delete</button>
          </td>
        </tr>`;
      }).join("");
      for (const b of rows.querySelectorAll("button[data-del]")) {
        b.addEventListener("click", () => deleteProcess(b.dataset.del, groups, render));
      }
    } catch (e) {
      rows.innerHTML = `<tr><td colspan="4" class="empty">${esc(e.message)}</td></tr>`;
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
        api("GET", "/api/v1/projects"),
        api("GET", "/api/v1/drafts"),
        api("GET", "/api/v1/dmnrefs"),
        api("GET", "/api/v1/forms"),
      ]);
    } catch (e) { root.innerHTML = `<div class="card empty">${esc(e.message)}</div>`; return; }

    const known = new Set(projects.map((p) => p.id));
    const proj = ungrouped ? { id: "ungrouped", name: "Ungrouped" } : projects.find((p) => p.id === id);
    if (!proj) {
      root.innerHTML = `<div class="card empty">This project no longer exists. <a href="#/modeler">Back to Modeler</a></div>`;
      return;
    }
    const mine = (a) => ungrouped ? (!a.projectId || !known.has(a.projectId)) : a.projectId === id;
    const dl = drafts.filter(mine), rl = refs.filter(mine), fl = forms.filter(mine);

    // Scope gating (ADR-0071). Ungrouped is the un-scoped personal/legacy bucket,
    // so it stays fully writable; a real project's actions follow the caller's
    // role. With auth off the server reports "owner", so everything shows as before.
    const isOwner = !ungrouped && proj.myRole === "owner";
    const canWrite = ungrouped || roleRank(proj.myRole) >= 2;

    // "Move to" items for a row's action menu: Ungrouped plus every project, with
    // the current one marked. Forms have no move endpoint, so only drafts/refs get it.
    const moveItems = (currentPid, act, key) => [
      { header: "Move to" },
      { label: "Ungrouped", icon: currentPid ? "" : "•", act, data: { pid: "", key } },
      ...projects.map((p) => ({ label: p.name, icon: p.id === currentPid ? "•" : "", act, data: { pid: p.id, key } })),
    ];

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
        <td class="muted">${esc(fmtTime(d.savedAt))}</td>
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
        <td class="muted">${esc(fmtTime(r.createdAt))}</td>
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
        <td class="muted">${esc(fmtTime(f.savedAt))}</td>
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
    ];

    const projItems = ungrouped ? [] : [
      ...(rl.length ? [{ label: "Validate DMN", icon: "✔", act: "valproj" }] : []),
      ...(AUTH.enabled && isOwner ? [{ label: "Share…", icon: "👤", act: "shareproj" }] : []),
      ...(isOwner ? [{ label: "Rename project", icon: "✎", act: "renproj" }] : []),
      ...(isOwner ? [{ sep: true }, { label: "Delete project", icon: "🗑", act: "delproj", danger: true }] : []),
    ];
    root.innerHTML = `
      <div class="crumb"><a href="#/modeler">Home</a> › ${esc(proj.name)}</div>
      <div class="between">
        <h1>${esc(proj.name)}${ungrouped ? "" : " " + visBadge(proj)}</h1>
        <div class="row">
          ${(!ungrouped && canWrite) ? `<button class="btn" id="pd-deploy">Deploy</button>` : ""}
          ${canWrite ? dropdown("Create new", "btn neutral", createItems) : ""}
          ${projItems.length ? dropdown("⋯", "icon-btn", projItems) : ""}
        </div>
      </div>
      <input class="filter-input" id="pd-filter" placeholder="Filter artifacts…" autocomplete="off">
      <div class="card" style="padding:0">
        <table>
          <thead><tr><th>Name</th><th>Type</th><th>Last changed</th><th></th></tr></thead>
          <tbody id="pd-rows">${bodyRows ||
            `<tr><td colspan="4" class="empty">${canWrite
              ? "No artifacts yet — use <b>Create new</b> to add one."
              : "No artifacts in this project yet."}</td></tr>`}</tbody>
        </table>
      </div>`;

    const filter = document.getElementById("pd-filter");
    filter.addEventListener("input", () => {
      const q = filter.value.trim().toLowerCase();
      for (const tr of root.querySelectorAll("#pd-rows tr[data-name]"))
        tr.hidden = q !== "" && !tr.dataset.name.includes(q);
    });

    onMenuAction(root, (act, b) => {
      switch (act) {
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
    if (deployBtn) deployBtn.addEventListener("click", () => deployProject(id, render));
  };
  await render();
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

// ---------- Projects (ADR-0034) ----------
async function createProject(reload) {
  const name = window.prompt("Project name");
  if (name == null) return; // cancelled
  const trimmed = name.trim();
  if (!trimmed) { toast("Project name is required", "err"); return; }
  try {
    await api("POST", "/api/v1/projects", { name: trimmed });
    toast(`Created project "${trimmed}"`, "ok");
  } catch (e) { toast("could not create project: " + e.message, "err"); }
  await reload();
}

async function renameProject(id, current, reload) {
  const name = window.prompt("Rename project", current);
  if (name == null) return;
  const trimmed = name.trim();
  if (!trimmed) { toast("Project name is required", "err"); return; }
  try {
    await api("PATCH", `/api/v1/projects/${encodeURIComponent(id)}`, { name: trimmed });
    toast("Renamed project", "ok");
  } catch (e) { toast("could not rename project: " + e.message, "err"); }
  await reload();
}

async function deleteProject(id, name, reload) {
  if (!window.confirm(`Delete project "${name}"? Its diagrams are kept and become Ungrouped.`)) return;
  try {
    await api("DELETE", `/api/v1/projects/${encodeURIComponent(id)}`);
    toast(`Deleted project "${name}"`, "ok");
  } catch (e) { toast("could not delete project: " + e.message, "err"); }
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

// shareProject loads the user directory (to resolve member names and populate the
// add-picker) and opens the share dialog. A non-admin owner cannot list users
// (ADR-0044), so on a 403 the dialog degrades to adding members by id.
async function shareProject(proj, reload) {
  let users = null, denied = false;
  try { users = await api("GET", "/api/v1/users"); }
  catch (e) {
    denied = /admin/i.test(e.message);
    if (!denied) { toast("could not load users: " + e.message, "err"); return; }
  }
  openShareModal(proj, users, denied, reload);
}

// openShareModal renders the share dialog and wires its controls to the project
// scope endpoints. It keeps a local copy of the project, updated from each
// mutation's response so the dialog reflects server truth without a full refetch;
// closing runs reload() once to refresh the underlying page (badges and gating).
function openShareModal(proj, users, denied, reload) {
  let p = proj;
  const byId = new Map((users || []).map((u) => [u.id, u]));
  const nameOf = (id) => { const u = byId.get(id); return u ? (u.displayName || u.username) : id; };
  const me = AUTH.user && AUTH.user.id;

  const ov = document.createElement("div");
  ov.className = "modal-ov";
  ov.innerHTML = `
    <div class="modal share-modal" role="dialog" aria-modal="true" aria-label="Share project">
      <div class="modal-head">
        <h2>Share “${esc(p.name)}”</h2>
        <button type="button" class="icon-btn" data-x aria-label="Close">✕</button>
      </div>
      <div class="modal-body" id="share-body"></div>
      <div class="modal-foot">
        <span class="muted small">Sharing controls who can view and edit this project’s diagrams. Running instances are not affected.</span>
        <button type="button" class="btn" data-done>Done</button>
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

    const personRow = (id, right, extra = "") => `
      <div class="member-row"${extra}>
        <span class="avatar sm">${esc(initials(nameOf(id)))}</span>
        <div class="member-id">${esc(nameOf(id))}${id === me ? ' <span class="muted">(you)</span>' : ""}</div>
        ${right}
      </div>`;

    const ownerRow = personRow(p.ownerId, `<span class="muted">Owner</span>`);
    const memberRows = members.map((m) => personRow(
      m.ref.id,
      `${roleSelect(m.role, `data-role-for="${esc(m.ref.id)}"`)}
       <button type="button" class="icon-btn danger" data-remove="${esc(m.ref.id)}" title="Remove access">✕</button>`,
      ` data-uid="${esc(m.ref.id)}"`,
    )).join("");

    const addControl = denied
      ? `<div class="add-row">
           <input class="field" id="add-uid" placeholder="User ID (usr_…)" autocomplete="off">
           ${roleSelect("viewer", 'id="add-role"')}
           <button type="button" class="btn" id="add-btn">Add</button>
         </div>
         <p class="muted small">A searchable directory needs the admin role — add by user ID for now.</p>`
      : eligible.length
        ? `<div class="add-row">
             <select class="field" id="add-uid">
               ${eligible.map((u) => `<option value="${esc(u.id)}">${esc(u.displayName || u.username)}</option>`).join("")}
             </select>
             ${roleSelect("viewer", 'id="add-role"')}
             <button type="button" class="btn" id="add-btn">Add</button>
           </div>`
        : `<p class="muted small">Everyone already has access.</p>`;

    const privateHint = p.visibility !== "shared" && members.length
      ? `<p class="muted small">Members are kept but have no access while the project is private.</p>`
      : "";

    body.innerHTML = `
      <div class="share-sec">
        <div class="seg" role="group" aria-label="Visibility">
          <button type="button" data-vis="private" class="${p.visibility !== "shared" ? "active" : ""}">Private</button>
          <button type="button" data-vis="shared" class="${p.visibility === "shared" ? "active" : ""}">Shared</button>
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
      </div>`;
    wire();
  };

  const apply = async (fn) => { try { p = await fn(); renderBody(); } catch (e) { toast(e.message, "err"); } };
  const setVisibility = (v) => apply(() =>
    api("PATCH", `/api/v1/projects/${encodeURIComponent(p.id)}`, { visibility: v }));
  const setMember = (uid, role) => uid && apply(() =>
    api("PUT", `/api/v1/projects/${encodeURIComponent(p.id)}/members/${encodeURIComponent(uid)}`, { role }));
  const removeMember = (uid) => apply(() =>
    api("DELETE", `/api/v1/projects/${encodeURIComponent(p.id)}/members/${encodeURIComponent(uid)}`));

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

// wireConnectorManagement wires the Organization page's managed-connector section:
// a "New connector" inline form and per-row Edit / Enable-Disable / Delete. Each
// change hits the connector API, which rebuilds the runtime registry, then the page
// re-renders. Only a token *reference* is ever entered — never a secret value
// (ADR-0041).
function wireConnectorManagement(connectors) {
  const reload = () => viewConsoleOrg();
  const slot = document.getElementById("connector-form-slot");
  const newBtn = document.getElementById("new-connector");
  if (newBtn && slot) {
    newBtn.addEventListener("click", () => {
      if (slot.dataset.open === "1") { slot.innerHTML = ""; slot.dataset.open = ""; return; }
      slot.dataset.open = "1";
      slot.innerHTML = `<form class="connector-form" style="display:grid;gap:8px;grid-template-columns:1fr 1fr 1fr auto;align-items:end;margin:4px 0 14px">
        <label class="field" style="margin:0"><span>Name</span><input name="name" placeholder="risk-service" required/></label>
        <label class="field" style="margin:0"><span>Endpoint</span><input name="endpoint" placeholder="https://temis.internal" required/></label>
        <label class="field" style="margin:0"><span>Token reference (optional)</span><input name="credentialsRef" placeholder="risk_token"/></label>
        <button class="btn" type="submit">Add</button></form>`;
      slot.querySelector("form").addEventListener("submit", async (e) => {
        e.preventDefault();
        const f = new FormData(e.target);
        try {
          await api("POST", "/api/v1/connectors", {
            name: (f.get("name") || "").trim(),
            endpoint: (f.get("endpoint") || "").trim(),
            credentialsRef: (f.get("credentialsRef") || "").trim(),
          });
          toast("Connector added", "ok");
          reload();
        } catch (err) { toast("Could not add connector: " + err.message, "err"); }
      });
    });
  }
  const rows = document.getElementById("connector-rows");
  if (rows) {
    rows.addEventListener("click", async (e) => {
      const btn = e.target.closest("button[data-cact]");
      if (!btn) return;
      const id = btn.closest("tr").dataset.id;
      const c = (connectors || []).find((x) => x.id === id);
      if (!c) return;
      try {
        if (btn.dataset.cact === "toggle") {
          await api("PATCH", "/api/v1/connectors/" + encodeURIComponent(id), { enabled: !c.enabled });
        } else if (btn.dataset.cact === "edit") {
          const endpoint = window.prompt("Endpoint URL", c.endpoint);
          if (endpoint == null) return;
          const credentialsRef = window.prompt("Token reference (resolved from ATLAS_CONNECTOR_<REF>_TOKEN; blank for none)", c.credentialsRef || "");
          if (credentialsRef == null) return;
          await api("PATCH", "/api/v1/connectors/" + encodeURIComponent(id), { endpoint: endpoint.trim(), credentialsRef: credentialsRef.trim() });
        } else if (btn.dataset.cact === "delete") {
          if (!window.confirm(`Delete connector "${c.name}"?`)) return;
          await api("DELETE", "/api/v1/connectors/" + encodeURIComponent(id));
        }
        reload();
      } catch (err) { toast("Connector update failed: " + err.message, "err"); }
    });
  }
}

// wireSecretsManagement binds the encrypted-vault panel (ADR-0069): a "New secret"
// upsert form, per-row "Set value" (rotate) and "Delete". Secrets are keyed by name,
// have no enable/disable, and are write-only — the value is never read back, so a set
// is an idempotent PUT and the UI only ever sends values, never displays them. When
// the vault is denied (non-admin) or unconfigured there is nothing to wire.
function wireSecretsManagement(secrets, state) {
  if (state !== "ok") return;
  const reload = () => viewConsoleOrg();
  const put = (name, value) => api("PUT", "/api/v1/secrets/" + encodeURIComponent(name), { value });
  const slot = document.getElementById("secret-form-slot");
  const newBtn = document.getElementById("new-secret");
  if (newBtn && slot) {
    newBtn.addEventListener("click", () => {
      if (slot.dataset.open === "1") { slot.innerHTML = ""; slot.dataset.open = ""; return; }
      slot.dataset.open = "1";
      slot.innerHTML = `<form class="secret-form" style="display:grid;gap:8px;grid-template-columns:1fr 1fr auto;align-items:end;margin:4px 0 14px">
        <label class="field" style="margin:0"><span>Name</span><input name="name" placeholder="risk_token" required/></label>
        <label class="field" style="margin:0"><span>Value</span><input name="value" type="password" placeholder="••••••••" required/></label>
        <button class="btn" type="submit">Save</button></form>`;
      slot.querySelector("form").addEventListener("submit", async (e) => {
        e.preventDefault();
        const f = new FormData(e.target);
        try {
          await put((f.get("name") || "").trim(), f.get("value") || "");
          toast("Secret saved", "ok");
          reload();
        } catch (err) { toast("Could not save secret: " + err.message, "err"); }
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
          const value = window.prompt(`New value for "${name}" (stored encrypted; the old value is replaced)`);
          if (value == null || value === "") return;
          await put(name, value);
          toast("Secret updated", "ok");
        } else if (btn.dataset.sact === "delete") {
          if (!window.confirm(`Delete secret "${name}"? A connector referencing it will resolve to no token.`)) return;
          await api("DELETE", "/api/v1/secrets/" + encodeURIComponent(name));
        }
        reload();
      } catch (err) { toast("Secret update failed: " + err.message, "err"); }
    });
  }
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
    const rep = await api("POST", `/api/v1/projects/${encodeURIComponent(projectId)}/validate`);
    for (const r of rep.references) applyRefStatus(r.id, r);
    toast(rep.ok ? "All DMN references are valid" : "Some DMN references are unresolved or invalid",
      rep.ok ? "ok" : "err");
  } catch (e) { toast("could not validate project: " + e.message, "err"); }
}

// deployProject deploys the whole project: the server validates its DMN
// references (the deploy-time gate) and, only if all pass, deploys its BPMN
// diagrams as runnable definitions. A refusal (409) carries the reason and the
// per-reference results, which we surface without a reload; a success reloads so
// the new definitions show under "Deployed". Uses a raw fetch so the refusal
// body (which is not an {error} shape) is read instead of thrown away.
async function deployProject(id, reload) {
  if (!window.confirm("Deploy this project? Its DMN references are validated, then its BPMN diagrams are deployed as runnable definitions.")) return;
  let rep;
  try {
    const res = await fetch(`/api/v1/projects/${encodeURIComponent(id)}/deploy`, { method: "POST" });
    rep = await res.json();
    if (res.ok && rep.deployed) {
      const n = (rep.definitions || []).length;
      toast(n ? `Deployed ${n} definition${n === 1 ? "" : "s"}` : "Nothing to deploy in this project", "ok");
      await reload();
      return;
    }
  } catch (e) {
    toast("deploy failed: " + e.message, "err");
    return;
  }
  // Refused (or a server error): show why and reflect any DMN results in place.
  toast(rep.reason || rep.error || "Deploy refused", "err");
  for (const r of rep.references || []) applyRefStatus(r.id, r);
}

// summarizeInstances rolls the flat instance list up per process id, so the
// Instances view can show one row per process (not one per instance): how many
// are running vs. finished, and the newest activity time, keyed by processId.
function summarizeInstances(instances) {
  const byProc = new Map();
  for (const r of instances) {
    if (!r.processId) continue; // orphaned instance (its definition was deleted)
    let s = byProc.get(r.processId);
    if (!s) { s = { running: 0, finished: 0, latestCompletedAt: 0 }; byProc.set(r.processId, s); }
    if (r.state === "active") s.running++;
    else {
      s.finished++;
      if (r.completedAt > s.latestCompletedAt) s.latestCompletedAt = r.completedAt;
    }
  }
  return byProc;
}

async function viewInstances() {
  view.innerHTML = `
    <div class="between">
      <h1>Instances</h1>
      <div class="row">
        <button class="btn" id="demo">Deploy demo</button>
        <button class="btn neutral" id="refresh">Refresh</button>
      </div>
    </div>
    <p class="muted">One row per deployed process. Open a process to pick a version, then
    watch all of its instances at once (every token on the diagram) or select a single
    instance to isolate it — with its variables shown below the diagram. Each instance
    listed under the diagram has a <b>&#9654; Replay</b> link to walk it step by step. Start
    the demo to park a token on a waiting task.</p>
    <div class="card" style="padding:0">
      <table>
        <thead><tr><th>Process</th><th>Versions</th><th>Running</th><th>Finished</th><th>Last activity</th><th></th></tr></thead>
        <tbody id="rows"><tr><td colspan="6" class="empty">Loading…</td></tr></tbody>
      </table>
    </div>`;
  const tbody = document.getElementById("rows");

  const load = async () => {
    try {
      const [procs, instances] = await Promise.all([
        api("GET", "/api/v1/processes"),
        api("GET", "/api/v1/instances"),
      ]);
      const groups = groupByProcess(procs);
      if (!groups.length) {
        tbody.innerHTML = `<tr><td colspan="6" class="empty">
          No processes deployed. Click <b>Deploy demo</b> above, or create one in the
          <a href="#/modeler">Modeler</a>.</td></tr>`;
        return;
      }
      const summary = summarizeInstances(instances);
      // completedAt is unix nanoseconds; Date wants milliseconds.
      const fmtNano = (ns) => ns ? new Date(ns / 1e6).toLocaleString() : "—";
      tbody.innerHTML = groups.map((g) => {
        const s = summary.get(g.processId) || { running: 0, finished: 0, latestCompletedAt: 0 };
        const label = g.latest.name || g.processId;
        const sub = g.latest.name
          ? `<div class="muted" style="font-size:12px">${esc(g.processId)}</div>` : "";
        const versions = g.versions.length === 1
          ? `v${g.latest.version}`
          : `${g.versions.length} versions <span class="muted">· latest v${g.latest.version}</span>`;
        const running = s.running
          ? `<span class="pill ok"><span class="dot"></span>${s.running}</span>`
          : '<span class="muted">0</span>';
        const collab = g.latest.collaborationKey
          ? `<a class="replay-link" href="#/operations/c/${g.latest.collaborationKey}" title="Replay the message flow between pools">⇄ Replay</a>`
          : "";
        return `<tr>
          <td><a href="#/operations/p/${g.latest.key}"><b>${esc(label)}</b></a>${collab}${sub}</td>
          <td>${versions}</td>
          <td>${running}</td>
          <td>${s.finished || '<span class="muted">0</span>'}</td>
          <td class="muted">${esc(fmtNano(s.latestCompletedAt))}</td>
          <td style="text-align:right"><a class="btn ghost" href="#/operations/p/${g.latest.key}">Open</a></td>
        </tr>`;
      }).join("");
    } catch (e) {
      tbody.innerHTML = `<tr><td colspan="6" class="empty">${esc(e.message)}</td></tr>`;
    }
  };
  document.getElementById("refresh").addEventListener("click", load);
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
      <button class="btn neutral" id="refresh">Refresh</button>
    </div>
    <p class="muted">One row per deployed DMN decision. A decision appears once any
    deployed process references it — even before it has run — and its usage grows as
    instances evaluate it. Open a decision to inspect each evaluation — the exact
    inputs it saw, the outputs it produced, and the rule trace — or open a process to
    watch the instances that drove it.</p>
    <div class="card" style="padding:0">
      <table>
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
          <td class="muted">${esc(fmtNano(d.lastEvaluatedAt))}</td>
        </tr>`;
      }).join("");
    } catch (e) {
      tbody.innerHTML = `<tr><td colspan="5" class="empty">${esc(e.message)}</td></tr>`;
    }
  };
  document.getElementById("refresh").addEventListener("click", load);
  await load();
}

// viewDecisionDetail lists every evaluation of one decision — its "instances" —
// newest first, each showing the exact inputs it saw, the outputs it produced, and
// (expandable) the temis trace of which rules fired (ADR-0066). This is the
// drill-down for debugging a decision that isn't returning what you expect: the
// inputs show the precise evaluated value, with its JSON type and quoting, so a
// string compared against a number, a stray space, or a wrong type is visible.
async function viewDecisionDetail(id) {
  view.innerHTML = `
    <div class="between">
      <div>
        <div class="muted" style="font-size:12px"><a href="#/operations/decisions">← Decisions</a></div>
        <h1>${esc(id)}</h1>
      </div>
      <button class="btn neutral" id="refresh">Refresh</button>
    </div>
    <p class="muted">Every evaluation of this decision, newest first — one row per
    evaluation. <b>Inputs</b> is what the decision actually saw (each value with its
    type); <b>Result</b> is what it produced, tagged with the rule that fired. Hover a
    result marked <b>&#8862;</b> to see the decision table with the matched rule
    highlighted — a rule that never matches (a string compared against a number, a
    stray space, a wrong type) shows its condition in red.</p>
    <div class="card" style="padding:0">
      <table>
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
          <td class="muted">${esc(fmtNano(r.at))}</td>
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

// loadFormViewer lazily imports the vendored form-js viewer (ADR-0013 vendoring
// pattern) and injects its stylesheet once, the first time a task with a form is
// opened — so users who never open a form never pay for the 86 KB CSS or the
// bundle. The promise is cached so repeated opens reuse the one import.
let _formViewer = null;
function loadFormViewer() {
  if (!_formViewer) {
    if (!document.getElementById("form-js-css")) {
      const link = document.createElement("link");
      link.id = "form-js-css";
      link.rel = "stylesheet";
      link.href = "vendor/form-js/form-js.css";
      document.head.appendChild(link);
    }
    _formViewer = import("./vendor/form-js/form-viewer.js");
  }
  return _formViewer;
}

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
          <button class="btn ghost small" id="task-refresh">Refresh</button>
        </header>
        <ul class="tasks-list" id="task-list"><li class="tasks-empty muted">Loading&hellip;</li></ul>
      </section>
      <section class="tasks-detail" id="task-detail"></section>
    </div>`;

  const nav = document.getElementById("task-folder-nav");
  const listEl = document.getElementById("task-list");
  const detailEl = document.getElementById("task-detail");
  const titleEl = document.getElementById("task-list-title");

  const visible = () => {
    const f = TASK_FOLDERS.find((x) => x.id === state.folder) || TASK_FOLDERS[0];
    return state.tasks.filter((t) => f.match(t, state.me));
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

  function renderList() {
    const items = visible();
    const f = TASK_FOLDERS.find((x) => x.id === state.folder) || TASK_FOLDERS[0];
    titleEl.textContent = f.label;
    if (!items.length) {
      listEl.innerHTML = `<li class="tasks-empty muted">No tasks in this folder.</li>`;
      return;
    }
    listEl.innerHTML = items
      .map((t) => {
        const sel = t.key === state.selected ? " selected" : "";
        const who = t.assignee ? esc(t.assignee) : t.candidateGroups ? esc(t.candidateGroups) : "Unassigned";
        const hi = taskPriority(t) >= 70 ? `<span class="prio-dot" title="High priority (${taskPriority(t)})"></span>` : "";
        const d = dueInfo(t);
        const due = d ? `<span class="due-badge${d.overdue ? " overdue" : ""}" title="${esc(d.abs)}">${esc(d.overdue ? "Overdue" : "Due " + d.rel)}</span>` : "";
        return `<li class="tasks-item${sel}" data-key="${t.key}">
          <div class="tasks-item-top">
            <span class="tasks-item-title">${hi}${esc(taskTitle(t))}</span>
            <span class="chip">${esc(t.processId || "")}</span>
          </div>
          <div class="tasks-item-sub muted"><span>${who}</span>${due}</div>
        </li>`;
      })
      .join("");
    listEl.querySelectorAll(".tasks-item").forEach((li) => {
      li.addEventListener("click", () => {
        state.selected = Number(li.dataset.key);
        renderList();
        renderDetail();
      });
    });
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
        // Prefill from the instance's variables; a failed read just yields a
        // blank form rather than blocking the task.
        api("GET", "/api/v1/instances/" + t.processInstanceKey + "/variables").catch(() => ({})),
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

  function renderDetail() {
    destroyForm();
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
    detailEl.innerHTML = `
      <header class="tasks-detail-head">
        <h1>${esc(taskTitle(t))}</h1>
        <div class="tasks-detail-actions">
          ${assignSelect}
          <button class="btn neutral" id="task-claim"${claimDisabled}${claimHint}>${claimLabel}</button>
          <button class="btn" id="task-complete">Complete task</button>
        </div>
      </header>
      <div class="tasks-fields">
        ${row("Process", esc(t.processId || "—"))}
        ${row("Element", `<span class="chip">${esc(t.elementId || "—")}</span>`)}
        ${row("Assignee", esc(t.assignee || "—"))}
        ${row("Candidate groups", esc(t.candidateGroups || "—"))}
        ${row("Priority", `${taskPriority(t)}${taskPriority(t) >= 70 ? ' <span class="prio-dot" title="High priority"></span>' : ""}`)}
        ${row("Due", (() => { const d = dueInfo(t); return d ? `<span class="${d.overdue ? "due-text overdue" : "due-text"}" title="${esc(d.abs)}">${esc(d.label)} · ${esc(d.abs)}</span>` : "—"; })())}
        ${row("Instance", `<span class="chip">${t.processInstanceKey}</span>`)}
        ${row("Task key", `<span class="chip">${t.key}</span>`)}
      </div>
      ${formArea}`;
    document.getElementById("task-complete").addEventListener("click", async (e) => {
      const btn = e.currentTarget;
      // If a form is mounted, validate and collect its data as the task's
      // variables; an invalid form blocks completion.
      let payload;
      if (state.mountedForm) {
        const { data, errors } = state.mountedForm.submit();
        if (errors && Object.keys(errors).length > 0) {
          toast("Please fix the highlighted fields", "err");
          return;
        }
        payload = { variables: data };
      }
      btn.disabled = true;
      try {
        await api("POST", "/api/v1/tasks/" + t.key + "/complete", payload);
        toast("Task completed");
        state.selected = null;
        await load();
      } catch (err) {
        toast("Complete failed: " + err.message, "err");
        btn.disabled = false;
      }
    });
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
    if (t.formId) mountForm(t);
  }

  function renderAll() {
    renderFolders();
    renderList();
    renderDetail();
  }

  async function load() {
    try {
      state.tasks = await api("GET", "/api/v1/tasks");
      state.tasks.sort(taskOrder);
      if (!state.tasks.some((t) => t.key === state.selected)) state.selected = null;
      renderAll();
    } catch (e) {
      listEl.innerHTML = `<li class="tasks-empty err">Failed to load tasks: ${esc(e.message)}</li>`;
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
          <button class="btn ghost small" id="start-refresh">Refresh</button></header>
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
        <button class="btn" id="start-go">Start process</button>
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
        <button class="btn ghost small" id="publish-create">Publish public link</button>`;
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
        <button class="btn ghost small" id="publish-copy">Copy</button>
        <a class="btn ghost small" href="${esc(link.url)}" target="_blank" rel="noopener">Open</a>
      </div>
      <button class="btn ghost small danger" id="publish-revoke">Revoke</button>`;
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

// resolveProject looks up a project's display name so the editor can render a
// breadcrumb link back to it. Returns {id, name} or null when the id is empty
// or unknown (a new/ungrouped artifact, or a best-effort lookup that failed —
// in which case the editor just falls back to a Home-only trail).
async function resolveProject(projectId) {
  if (!projectId) return null;
  try {
    const projects = await api("GET", "/api/v1/projects");
    const p = projects.find((x) => x.id === projectId);
    return p ? { id: p.id, name: p.name } : null;
  } catch { return null; }
}

async function viewEditor(key, projectId) {
  const mod = await import("./editor.js");
  const project = await resolveProject(projectId);
  await mod.mountEditor(view, { api, toast, key, projectId, project });
}

async function viewEditorDraft(id) {
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
  await mod.mountEditor(view, { api, toast, draftId: id, projectId, project });
}

async function viewFormEditor(formId, projectId) {
  const mod = await import("./form-editor.js");
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
    view.innerHTML = `<div class="card empty"><h1>Could not load model</h1><p class="muted">${esc(e.message)}</p></div>`;
    return;
  }
  const title = g.modelName || (ref && ref.name) || "DMN model";
  const back = `<a href="#/modeler">← Modeler</a>`;
  const editBtn = ref && ref.modelRef
    ? `<button class="btn" id="dmn-edit">Bearbeiten</button>` : "";
  // Re-render from the updated model once the editor closes on a save.
  const wireEdit = () => {
    const b = document.getElementById("dmn-edit");
    if (b) b.addEventListener("click", async () => {
      await editDmnRef({ id: ref.id, modelRef: ref.modelRef, projectId: ref.projectId || "", name: ref.name }, () => viewDmnViewer(refId));
    });
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
    <div id="dmn-canvas" style="overflow:auto;border:1px solid #e5e7eb;border-radius:10px;background:#fafafa;padding:8px">${renderDrgSvg(g)}</div>
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

async function route() {
  // Any navigation closes the app switcher and tears down an editor/live view.
  document.getElementById("drawer").hidden = true;
  document.getElementById("scrim").hidden = true;
  if (window.__atlasCleanup) { try { window.__atlasCleanup(); } catch { /* ignore */ } }

  const hash = location.hash || "#/console";
  const [path, arg] = [hash.replace(/\?.*$/, ""), hash];
  let appId = "console";

  if (path.startsWith("#/modeler")) appId = "modeler";
  else if (path.startsWith("#/tasks")) appId = "tasks";
  else if (path.startsWith("#/operations")) appId = "operations";
  else if (path.startsWith("#/insights")) appId = "insights";

  // Gate the whole app behind login when enforcement is on and no session is
  // active. Auth off (the default) skips this entirely.
  if (!AUTH.loaded) await loadAuth();
  if (AUTH.enabled && !AUTH.user) {
    document.getElementById("app-name").textContent = "Atlas";
    document.getElementById("topnav").innerHTML = "";
    updateAccount();
    return viewLogin();
  }

  setChrome(appId, path);
  updateAccount();
  window.scrollTo(0, 0);

  try {
    if (path === "#/" || path === "#/console") return await viewConsoleDashboard();
    if (path === "#/console/engine") return await viewConsoleEngine();
    if (path === "#/console/logs") return await viewConsoleLogs();
    if (path === "#/console/org") return await viewConsoleOrg();
    if (path === "#/modeler") return await viewModelerHome();
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
    if (path === "#/operations/decisions") return await viewDecisions();
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
    if (appId !== "console" && appId !== "modeler" && appId !== "tasks") return viewComingSoon(appId);
    // Unknown route → dashboard.
    location.hash = "#/console";
  } catch (e) {
    view.innerHTML = `<div class="card empty"><h1>Something went wrong</h1><p class="muted">${esc(e.message)}</p></div>`;
  }
}

initShell();
window.addEventListener("hashchange", route);
loadAuth().then(route);
