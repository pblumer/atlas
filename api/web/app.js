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

// ---------- Apps (Atlas naming; reference product names removed) ----------
const APPS = [
  { id: "console", name: "Console", route: "#/console", on: true },
  { id: "modeler", name: "Modeler", route: "#/modeler", on: true },
  { id: "tasks", name: "Tasks", route: "#/tasks", on: false },
  { id: "operations", name: "Operations", route: "#/operations", on: false },
  { id: "insights", name: "Insights", route: "#/insights", on: false },
];

// Secondary (in-app) navigation.
const TOPNAV = {
  console: [
    { name: "Dashboard", route: "#/console" },
    { name: "Engine", route: "#/console/engine" },
    { name: "Organization", route: "#/console/org" },
  ],
  modeler: [{ name: "Home", route: "#/modeler" }],
  tasks: [], operations: [], insights: [],
};

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

  api("GET", "/api/v1/info").then((i) => {
    document.querySelectorAll(".org").forEach((e) => { e.textContent = "Atlas Org"; });
    if (i && i.version) document.title = `Atlas ${i.version}`;
  }).catch(() => {});
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
  document.body.classList.toggle("editor-mode", appId === "modeler" && route.includes("/d/") || route.endsWith("/new"));
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
        <li><b>Deploy &amp; run</b> — deploy a model and start an instance straight from the editor.</li>
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
        <div class="between"><h2>Engine</h2><span class="pill ok"><span class="dot"></span>running</span></div>
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
  try {
    const [procs, stats] = await Promise.all([
      api("GET", "/api/v1/processes"),
      api("GET", "/api/v1/stats"),
    ]);
    document.getElementById("e-pi").textContent = stats.activeProcessInstances;
    document.getElementById("e-ei").textContent = stats.activeElementInstances;
    document.getElementById("e-dep").textContent = procs.length;
  } catch (e) { toast(e.message, "err"); }
}

function viewConsoleOrg() {
  view.innerHTML = `
    <div class="card">
      <h1>Organization</h1>
      <p class="muted">You are the only user in this organization. Multi-user access,
      roles, and clusters are not part of the single-binary build.</p>
      <div class="row"><span class="avatar" style="position:static">PB</span><span>Owner</span></div>
    </div>`;
}

async function viewModelerHome() {
  view.innerHTML = `
    <div class="between">
      <h1>Modeler</h1>
      <a class="btn" href="#/modeler/new">New diagram</a>
    </div>
    <div class="tabs">
      <button class="active">Diagrams</button>
      <button disabled title="not available yet">Recently deleted</button>
    </div>
    <div class="card" style="padding:0">
      <table>
        <thead><tr><th>Name</th><th>Version</th><th>Deployed</th><th></th></tr></thead>
        <tbody id="rows"><tr><td colspan="4" class="empty">Loading…</td></tr></tbody>
      </table>
    </div>`;
  try {
    const procs = await api("GET", "/api/v1/processes");
    const rows = document.getElementById("rows");
    if (!procs.length) {
      rows.innerHTML = `<tr><td colspan="4" class="empty">
        No diagrams yet. <a href="#/modeler/new">Create one</a> or deploy BPMN XML.</td></tr>`;
      return;
    }
    rows.innerHTML = procs.map((p) => `
      <tr>
        <td><a href="#/modeler/d/${p.key}"><b>${esc(p.processId)}</b></a></td>
        <td>v${p.version}</td>
        <td class="muted">${esc(fmtTime(p.deployedAt))}</td>
        <td style="text-align:right"><a class="btn ghost" href="#/modeler/d/${p.key}">Open</a></td>
      </tr>`).join("");
  } catch (e) {
    document.getElementById("rows").innerHTML =
      `<tr><td colspan="4" class="empty">${esc(e.message)}</td></tr>`;
  }
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

async function viewEditor(key) {
  const mod = await import("./editor.js");
  await mod.mountEditor(view, { api, toast, key });
}

// ---------- Router ----------
async function route() {
  // Any navigation closes the app switcher.
  document.getElementById("drawer").hidden = true;
  document.getElementById("scrim").hidden = true;

  const hash = location.hash || "#/console";
  const [path, arg] = [hash.replace(/\?.*$/, ""), hash];
  let appId = "console";

  if (path.startsWith("#/modeler")) appId = "modeler";
  else if (path.startsWith("#/tasks")) appId = "tasks";
  else if (path.startsWith("#/operations")) appId = "operations";
  else if (path.startsWith("#/insights")) appId = "insights";

  setChrome(appId, path);
  window.scrollTo(0, 0);

  try {
    if (path === "#/" || path === "#/console") return await viewConsoleDashboard();
    if (path === "#/console/engine") return await viewConsoleEngine();
    if (path === "#/console/org") return viewConsoleOrg();
    if (path === "#/modeler") return await viewModelerHome();
    if (path === "#/modeler/new") return await viewEditor(null);
    const m = path.match(/^#\/modeler\/d\/(\d+)$/);
    if (m) return await viewEditor(Number(m[1]));
    if (appId !== "console" && appId !== "modeler") return viewComingSoon(appId);
    // Unknown route → dashboard.
    location.hash = "#/console";
  } catch (e) {
    view.innerHTML = `<div class="card empty"><h1>Something went wrong</h1><p class="muted">${esc(e.message)}</p></div>`;
  }
}

initShell();
window.addEventListener("hashchange", route);
route();
