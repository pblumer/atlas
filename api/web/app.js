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
  { id: "operations", name: "Operations", route: "#/operations", on: true },
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
  operations: [{ name: "Instances", route: "#/operations" }],
  tasks: [], insights: [],
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
  const fullBleed = route.includes("/modeler/d/") || route.endsWith("/new") || route.includes("/operations/p/");
  document.body.classList.toggle("editor-mode", fullBleed);
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

async function viewModelerHome() {
  view.innerHTML = `
    <div class="between">
      <h1>Modeler</h1>
      <a class="btn" href="#/modeler/new">New diagram</a>
    </div>
    <div class="card" style="padding:0">
      <table>
        <thead><tr><th>Process</th><th>Latest</th><th>Deployed</th><th></th></tr></thead>
        <tbody id="rows"><tr><td colspan="4" class="empty">Loading…</td></tr></tbody>
      </table>
    </div>`;
  const rows = document.getElementById("rows");

  const render = async () => {
    try {
      const groups = groupByProcess(await api("GET", "/api/v1/processes"));
      if (!groups.length) {
        rows.innerHTML = `<tr><td colspan="4" class="empty">
          No diagrams yet. <a href="#/modeler/new">Create one</a> or deploy BPMN XML.</td></tr>`;
        return;
      }
      rows.innerHTML = groups.map((g) => {
        const older = g.versions.length > 1
          ? ` <span class="muted">· ${g.versions.length} versions</span>` : "";
        return `<tr>
          <td><a href="#/modeler/d/${g.latest.key}"><b>${esc(g.processId)}</b></a></td>
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
  await render();
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

async function viewInstances() {
  view.innerHTML = `
    <div class="between">
      <h1>Instances</h1>
      <button class="btn neutral" id="refresh">Refresh</button>
    </div>
    <p class="muted">Running process instances on this server. Each holds one or more
    element instances (tokens) as it moves through the engine.</p>
    <div class="card" style="padding:0">
      <table>
        <thead><tr><th>Instance</th><th>Process</th><th>Version</th><th>Tokens</th><th>Variables</th><th>Status</th><th></th></tr></thead>
        <tbody id="rows"><tr><td colspan="7" class="empty">Loading…</td></tr></tbody>
      </table>
    </div>`;
  const load = async () => {
    try {
      const rows = await api("GET", "/api/v1/instances");
      const tbody = document.getElementById("rows");
      if (!rows.length) {
        tbody.innerHTML = `<tr><td colspan="7" class="empty">
          No running instances. Start one from the <a href="#/modeler">Modeler</a>.</td></tr>`;
        return;
      }
      const vars = (list) => !list || !list.length
        ? '<span class="muted">—</span>'
        : list.map((v) => `<span class="chip">${esc(v.name)}=${esc(v.value)}</span>`).join(" ");
      tbody.innerHTML = rows.map((r) => `
        <tr>
          <td><b>${r.key}</b></td>
          <td>${r.processId
            ? `<a href="#/operations/p/${r.processDefKey}">${esc(r.processId)}</a>`
            : '<span class="muted">def ' + r.processDefKey + "</span>"}</td>
          <td>${r.version ? "v" + r.version : "—"}</td>
          <td>${r.elementInstances}</td>
          <td>${vars(r.variables)}</td>
          <td><span class="pill ok"><span class="dot"></span>${esc(r.state)}</span></td>
          <td style="text-align:right"><a class="btn ghost" href="#/operations/p/${r.processDefKey}">Live view</a></td>
        </tr>`).join("");
    } catch (e) {
      document.getElementById("rows").innerHTML =
        `<tr><td colspan="7" class="empty">${esc(e.message)}</td></tr>`;
    }
  };
  document.getElementById("refresh").addEventListener("click", load);
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

async function viewEditor(key) {
  const mod = await import("./editor.js");
  await mod.mountEditor(view, { api, toast, key });
}

async function viewLive(key) {
  const mod = await import("./editor.js");
  await mod.mountLive(view, { api, toast, key });
}

// ---------- Router ----------
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
    if (path === "#/operations") return await viewInstances();
    const lm = path.match(/^#\/operations\/p\/(\d+)$/);
    if (lm) return await viewLive(Number(lm[1]));
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
