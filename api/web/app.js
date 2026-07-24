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
    { name: "Organization", route: "#/console/org" },
  ],
  modeler: [{ name: "Home", route: "#/modeler" }],
  operations: [{ name: "Instances", route: "#/operations" }],
  tasks: [{ name: "Inbox", route: "#/tasks" }], insights: [],
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
    desc: "Calls an external REST API from a service task off the processor loop, with server-registered endpoints and credentials. Not wired into this build yet.",
    refs: "ADR-0036", status: "planned", statusLabel: "not configured",
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
  view.innerHTML = `
    <div class="card">
      <h1>Organization</h1>
      <p class="muted">You are the only user in this organization. Multi-user access,
      roles, and clusters are not part of the single-binary build.</p>
      <div class="row"><span class="avatar" style="position:static">PB</span><span>Owner</span></div>
    </div>
    <div class="card" style="padding:0; margin-top:18px">
      <div class="between" style="padding:16px 18px 0"><h2>Connectors</h2></div>
      <p class="muted" style="padding:0 18px; margin:6px 0 12px">Sibling engines Atlas
      delegates to. Each is an org-wide integration, shared across every process.</p>
      <table><tbody>${CONNECTORS.map(connectorRow).join("")}</tbody></table>
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
      return `<tr>
        <td><div class="artifact-name"><span class="mi-icon">📁</span><a href="${href}"><b>${esc(p.name)}</b></a></div></td>
        <td class="muted">${n}</td>
        <td class="muted">${esc(fmtTime(p.updatedAt))}</td>
        <td class="row-actions">${dropdown("⋯", "icon-btn", [
          { label: "Open", icon: "→", href },
          { label: "Rename", icon: "✎", act: "rename", data: { id: p.id, name: p.name } },
          { sep: true },
          { label: "Delete", icon: "🗑", act: "del", data: { id: p.id, name: p.name }, danger: true },
        ])}</td>
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
      return `<tr data-name="${esc((d.name || d.processId).toLowerCase())}">
        ${nameCell("BPMN", d.name || d.processId, esc(d.processId), href)}
        <td class="muted">Diagram</td>
        <td class="muted">${esc(fmtTime(d.savedAt))}</td>
        <td class="row-actions">${dropdown("⋯", "icon-btn", [
          { label: "Open", icon: "→", href },
          ...moveItems(d.projectId, "movedraft", d.processId),
          { sep: true },
          { label: "Delete", icon: "🗑", act: "deldraft", data: { key: d.processId }, danger: true },
        ])}</td></tr>`;
    };
    const refRow = (r) => `<tr data-name="${esc(r.name.toLowerCase())}">
        ${nameCell("DMN", r.name, `temis model: ${esc(r.modelRef)} · <span data-refstatus="${esc(r.id)}">not validated</span>`, "")}
        <td class="muted">Decision ref</td>
        <td class="muted">${esc(fmtTime(r.createdAt))}</td>
        <td class="row-actions">${dropdown("⋯", "icon-btn", [
          { label: "Validate", icon: "✔", act: "valref", data: { id: r.id } },
          ...moveItems(r.projectId, "moveref", r.id),
          { sep: true },
          { label: "Delete", icon: "🗑", act: "delref", data: { id: r.id }, danger: true },
        ])}</td></tr>`;
    const formRow = (f) => {
      const href = `#/modeler/form/e/${encodeURIComponent(f.id)}`;
      return `<tr data-name="${esc((f.name || f.id).toLowerCase())}">
        ${nameCell("FORM", f.name || f.id, esc(f.id), href)}
        <td class="muted">Form</td>
        <td class="muted">${esc(fmtTime(f.savedAt))}</td>
        <td class="row-actions">${dropdown("⋯", "icon-btn", [
          { label: "Open", icon: "→", href },
          { sep: true },
          { label: "Delete", icon: "🗑", act: "delform", data: { id: f.id }, danger: true },
        ])}</td></tr>`;
    };

    const bodyRows = dl.map(draftRow).join("") + rl.map(refRow).join("") + fl.map(formRow).join("");
    const newDiagramHref = ungrouped ? "#/modeler/new" : `#/modeler/new/p/${encodeURIComponent(id)}`;
    const newFormHref = ungrouped ? "#/modeler/form/new" : `#/modeler/form/new/p/${encodeURIComponent(id)}`;
    const createItems = [
      { header: "Blank resources" },
      { label: "BPMN diagram", icon: "⚙", href: newDiagramHref },
      { label: "DMN reference", icon: "▦", act: "newref" },
      { label: "Form", icon: "▤", href: newFormHref },
    ];

    root.innerHTML = `
      <div class="crumb"><a href="#/modeler">Home</a> › ${esc(proj.name)}</div>
      <div class="between">
        <h1>${esc(proj.name)}</h1>
        <div class="row">
          ${ungrouped ? "" : `<button class="btn" id="pd-deploy">Deploy</button>`}
          ${dropdown("Create new", "btn neutral", createItems)}
          ${ungrouped ? "" : dropdown("⋯", "icon-btn", [
            ...(rl.length ? [{ label: "Validate DMN", icon: "✔", act: "valproj" }] : []),
            { label: "Rename project", icon: "✎", act: "renproj" },
            { sep: true },
            { label: "Delete project", icon: "🗑", act: "delproj", danger: true },
          ])}
        </div>
      </div>
      <input class="filter-input" id="pd-filter" placeholder="Filter artifacts…" autocomplete="off">
      <div class="card" style="padding:0">
        <table>
          <thead><tr><th>Name</th><th>Type</th><th>Last changed</th><th></th></tr></thead>
          <tbody id="pd-rows">${bodyRows ||
            `<tr><td colspan="4" class="empty">No artifacts yet — use <b>Create new</b> to add one.</td></tr>`}</tbody>
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
        case "renproj": renameProject(id, proj.name, render); break;
        case "delproj": deleteProject(id, proj.name, () => { location.hash = "#/modeler"; }); break;
        case "valproj": validateProject(id); break;
        case "valref": validateDmnRef(b.dataset.id); break;
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

// moveDraft reassigns a draft to a project (or to Ungrouped when projectId is "").
async function moveDraft(processId, projectId, reload) {
  try {
    await api("PATCH", `/api/v1/drafts/${encodeURIComponent(processId)}`, { projectId });
  } catch (e) { toast("could not move draft: " + e.message, "err"); }
  await reload();
}

// createDmnRef adds a DMN reference — a pointer to a temis-authored decision
// model — into a project. Atlas organizes and lists the reference; authoring
// stays in temis (ADR-0034), so we capture only a name and the temis handle.
async function createDmnRef(projectId, reload) {
  const name = window.prompt("Reference name (how it shows in Atlas)");
  if (name == null) return;
  const modelRef = window.prompt("temis model reference (the model’s name in the temis Modeler)");
  if (modelRef == null) return;
  if (!name.trim() || !modelRef.trim()) { toast("Name and temis model reference are required", "err"); return; }
  try {
    await api("POST", "/api/v1/dmnrefs", { name: name.trim(), modelRef: modelRef.trim(), projectId });
    toast(`Added DMN reference "${name.trim()}"`, "ok");
  } catch (e) { toast("could not add DMN reference: " + e.message, "err"); }
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
    instance to isolate it — with its variables shown below the diagram. Start the demo to
    park a token on a waiting task.</p>
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

// ---------- Tasks (Outlook-style inbox, ADR-0028) ----------

// A task's display title: the user task's element name, falling back to its BPMN
// id so a task authored without a name is still recognizable.
const taskTitle = (t) => t.name || t.elementId || "User task";

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

async function viewTasks() {
  const state = {
    tasks: [],
    folder: "all",
    selected: null, // job key of the selected task
    me: localStorage.getItem("atlas.tasks.me") || "",
    mountedForm: null, // the live form-js viewer instance for the selected task, if any
  };

  view.innerHTML = `
    <div class="tasks">
      <aside class="tasks-folders">
        <label class="tasks-identity">
          <span>You</span>
          <input id="task-me" type="text" placeholder="e.g. editor" value="${esc(state.me)}" spellcheck="false" />
        </label>
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
        return `<li class="tasks-item${sel}" data-key="${t.key}">
          <div class="tasks-item-top">
            <span class="tasks-item-title">${esc(taskTitle(t))}</span>
            <span class="chip">${esc(t.processId || "")}</span>
          </div>
          <div class="tasks-item-sub muted">${who}</div>
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

  // mountForm loads the vendored form-js viewer and the task's bound form schema,
  // then renders it into the detail pane. Guards against the selection changing
  // while the (async) load is in flight.
  async function mountForm(t) {
    const host = document.getElementById("task-form");
    if (!host) return;
    try {
      const [{ Form }, def] = await Promise.all([
        loadFormViewer(),
        api("GET", "/api/v1/forms/" + encodeURIComponent(t.formId)),
      ]);
      if (state.selected !== t.key) return; // selection moved on; drop this mount
      host.innerHTML = "";
      const form = new Form({ container: host });
      await form.importSchema(def.schema);
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
    const formArea = t.formId
      ? `<div class="tasks-form" id="task-form"><p class="muted">Loading form&hellip;</p></div>`
      : `<div class="tasks-form-placeholder"><p class="muted">This task has no form; completing it
         records no variables.</p></div>`;
    detailEl.innerHTML = `
      <header class="tasks-detail-head">
        <h1>${esc(taskTitle(t))}</h1>
        <div class="tasks-detail-actions">
          <button class="btn neutral" id="task-claim"${claimDisabled}${claimHint}>${claimLabel}</button>
          <button class="btn" id="task-complete">Complete task</button>
        </div>
      </header>
      <div class="tasks-fields">
        ${row("Process", esc(t.processId || "—"))}
        ${row("Element", `<span class="chip">${esc(t.elementId || "—")}</span>`)}
        ${row("Assignee", esc(t.assignee || "—"))}
        ${row("Candidate groups", esc(t.candidateGroups || "—"))}
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
          await api("POST", "/api/v1/tasks/" + t.key + "/claim", { assignee: state.me });
          toast("Task claimed");
        }
        await load(); // keeps the selection; the detail re-renders with the new assignee
      } catch (err) {
        toast("Claim failed: " + err.message, "err");
        btn.disabled = false;
      }
    });
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
      if (!state.tasks.some((t) => t.key === state.selected)) state.selected = null;
      renderAll();
    } catch (e) {
      listEl.innerHTML = `<li class="tasks-empty err">Failed to load tasks: ${esc(e.message)}</li>`;
    }
  }

  document.getElementById("task-me").addEventListener("input", (e) => {
    state.me = e.target.value.trim();
    localStorage.setItem("atlas.tasks.me", state.me);
    renderFolders();
    renderList();
  });
  document.getElementById("task-refresh").addEventListener("click", load);
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

async function viewEditor(key, projectId) {
  const mod = await import("./editor.js");
  await mod.mountEditor(view, { api, toast, key, projectId });
}

async function viewEditorDraft(id) {
  const mod = await import("./editor.js");
  await mod.mountEditor(view, { api, toast, draftId: id });
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
    const m = path.match(/^#\/modeler\/d\/(\d+)$/);
    if (m) return await viewEditor(Number(m[1]));
    if (path === "#/tasks") return await viewTasks();
    if (path === "#/operations") return await viewInstances();
    // A specific instance can be deep-linked (…/i/{instanceKey}) — the Modeler's
    // Deploy & run builds this so a roundtrip lands straight on the started
    // instance. The plain form defaults the picker to "All instances".
    const li = path.match(/^#\/operations\/p\/(\d+)\/i\/(\d+)$/);
    if (li) return await viewLive(Number(li[1]), Number(li[2]));
    const lm = path.match(/^#\/operations\/p\/(\d+)$/);
    if (lm) return await viewLive(Number(lm[1]));
    const cm = path.match(/^#\/operations\/c\/(\d+)$/);
    if (cm) return await viewCollaboration(Number(cm[1]));
    if (appId !== "console" && appId !== "modeler" && appId !== "tasks") return viewComingSoon(appId);
    // Unknown route → dashboard.
    location.hash = "#/console";
  } catch (e) {
    view.innerHTML = `<div class="card empty"><h1>Something went wrong</h1><p class="muted">${esc(e.message)}</p></div>`;
  }
}

initShell();
window.addEventListener("hashchange", route);
route();
