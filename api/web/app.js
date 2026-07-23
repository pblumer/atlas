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
  const fullBleed = route.includes("/modeler/d/") || route.includes("/modeler/draft/") || route.endsWith("/new") || route.includes("/operations/p/");
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

async function viewModelerHome() {
  view.innerHTML = `
    <div class="between">
      <h1>Modeler</h1>
      <div class="row">
        <button class="btn neutral" id="new-project">New project</button>
        <a class="btn" href="#/modeler/new">New diagram</a>
      </div>
    </div>
    <div id="projects-section"><p class="muted">Loading…</p></div>
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
  const projectsSection = document.getElementById("projects-section");

  // renderProjects shows saved-but-not-deployed diagrams (drafts) organized into
  // projects (ADR-0034). Each project is a card holding its artifacts; drafts
  // that belong to no existing project fall into an "Ungrouped" bucket. A per-row
  // "Project" dropdown moves a draft between projects.
  const renderProjects = async () => {
    let projects = [], drafts = [], refs = [];
    try {
      [projects, drafts, refs] = await Promise.all([
        api("GET", "/api/v1/projects"),
        api("GET", "/api/v1/drafts"),
        api("GET", "/api/v1/dmnrefs"),
      ]);
    } catch (e) { projectsSection.innerHTML = `<p class="empty">${esc(e.message)}</p>`; return; }

    // Bucket artifacts by project; an empty or unknown projectId reads as
    // Ungrouped. BPMN drafts and DMN references share the same buckets.
    const known = new Set(projects.map((p) => p.id));
    const bucket = (items) => {
      const byProject = new Map(), ungrouped = [];
      for (const it of items) {
        if (it.projectId && known.has(it.projectId)) {
          if (!byProject.has(it.projectId)) byProject.set(it.projectId, []);
          byProject.get(it.projectId).push(it);
        } else ungrouped.push(it);
      }
      return { byProject, ungrouped };
    };
    const draftsB = bucket(drafts);
    const refsB = bucket(refs);

    // The shared "move to…" options: Ungrouped plus every project, current selected.
    const moveOptions = (current) =>
      [`<option value=""${!current ? " selected" : ""}>Ungrouped</option>`]
        .concat(projects.map((p) =>
          `<option value="${esc(p.id)}"${p.id === current ? " selected" : ""}>${esc(p.name)}</option>`))
        .join("");
    const moveSelect = (attr, id, current) =>
      `<select ${attr}="${esc(id)}" title="Move to project"
        style="width:auto; display:inline-block; padding:5px 8px; font-size:13px">${moveOptions(current || "")}</select>`;

    const draftRows = (list) => list.map((d) => {
      const label = d.name || d.processId;
      const sub = d.name ? `<div class="muted" style="font-size:12px">${esc(d.processId)}</div>` : "";
      const href = `#/modeler/draft/${encodeURIComponent(d.processId)}`;
      return `<tr>
        <td><span class="chip">BPMN</span> <a href="${href}"><b>${esc(label)}</b></a>${sub}</td>
        <td class="muted">${esc(fmtTime(d.savedAt))}</td>
        <td style="text-align:right; white-space:nowrap">
          ${moveSelect("data-move", d.processId, d.projectId)}
          <a class="btn ghost" href="${href}">Open</a>
          <button class="btn ghost danger" data-draftdel="${esc(d.processId)}">Delete</button>
        </td>
      </tr>`;
    }).join("");

    // A DMN reference points at a temis-authored model — Atlas lists it but does
    // not edit it (ADR-0034), so there is no "Open", just the temis handle and a
    // deploy-time Validate that resolves the model and compiles it.
    const refRows = (list) => list.map((r) => `<tr>
        <td><span class="chip">DMN</span> <b>${esc(r.name)}</b>
          <div class="muted" style="font-size:12px">temis model: ${esc(r.modelRef)}</div></td>
        <td><span data-refstatus="${esc(r.id)}" class="muted" style="font-size:12px">not validated</span></td>
        <td style="text-align:right; white-space:nowrap">
          <button class="btn ghost" data-refvalidate="${esc(r.id)}">Validate</button>
          ${moveSelect("data-moveref", r.id, r.projectId)}
          <button class="btn ghost danger" data-refdel="${esc(r.id)}">Delete</button>
        </td>
      </tr>`).join("");

    const artifactTable = (dl, rl) => `<table><tbody>${draftRows(dl)}${refRows(rl)}</tbody></table>`;

    const projectCard = (p) => {
      const dl = draftsB.byProject.get(p.id) || [];
      const rl = refsB.byProject.get(p.id) || [];
      const body = (dl.length || rl.length) ? artifactTable(dl, rl)
        : `<p class="empty" style="margin:0; padding:16px">No artifacts yet — add a DMN reference, or create a diagram and move it here.</p>`;
      const n = p.artifacts;
      return `<div class="card" style="padding:0; margin-bottom:14px">
        <div class="between" style="padding:12px 14px; border-bottom:1px solid var(--border)">
          <div><b>${esc(p.name)}</b> <span class="muted" style="font-size:12px">· ${n} artifact${n === 1 ? "" : "s"}</span></div>
          <div class="row">
            <button class="btn" data-projdeploy="${esc(p.id)}">Deploy</button>
            <button class="btn ghost" data-refadd="${esc(p.id)}">Add DMN reference</button>
            ${rl.length ? `<button class="btn ghost" data-projvalidate="${esc(p.id)}">Validate DMN</button>` : ""}
            <button class="btn ghost" data-projrename="${esc(p.id)}" data-projname="${esc(p.name)}">Rename</button>
            <button class="btn ghost danger" data-projdel="${esc(p.id)}" data-projname="${esc(p.name)}">Delete</button>
          </div>
        </div>
        ${body}
      </div>`;
    };

    let html = "";
    if (projects.length) {
      const projOpen = sectionState("projects");
      html += `<h2 style="margin:6px 0 10px"><button class="section-toggle" aria-expanded="${projOpen}" data-section="projects">Projects</button></h2>
        <div class="section-body" id="sec-projects"${projOpen ? "" : " hidden"}>` + projects.map(projectCard).join("") + `</div>`;
    }
    if (draftsB.ungrouped.length || refsB.ungrouped.length) {
      const ugOpen = sectionState("ungrouped");
      html += `<h2 style="margin:${projects.length ? "18px" : "6px"} 0 10px"><button class="section-toggle" aria-expanded="${ugOpen}" data-section="ungrouped">Ungrouped <span class="muted" style="font-size:13px">· artifacts not in a project</span></button></h2>
        <div class="section-body" id="sec-ungrouped"${ugOpen ? "" : " hidden"}>
        <div class="card" style="padding:0">${artifactTable(draftsB.ungrouped, refsB.ungrouped)}</div></div>`;
    }
    if (!projects.length && !draftsB.ungrouped.length && !refsB.ungrouped.length) {
      html = `<div class="card empty">No projects or artifacts yet. Create a <b>New project</b> to
        organize your BPMN diagrams and DMN references, or start a <a href="#/modeler/new">New diagram</a> and save it.</div>`;
    }
    projectsSection.innerHTML = html;

    for (const t of projectsSection.querySelectorAll(".section-toggle"))
      t.addEventListener("click", () => toggleSection(t.dataset.section, t));

    for (const b of projectsSection.querySelectorAll("button[data-draftdel]"))
      b.addEventListener("click", () => deleteDraft(b.dataset.draftdel, renderProjects));
    for (const b of projectsSection.querySelectorAll("button[data-projrename]"))
      b.addEventListener("click", () => renameProject(b.dataset.projrename, b.dataset.projname, renderProjects));
    for (const b of projectsSection.querySelectorAll("button[data-projdel]"))
      b.addEventListener("click", () => deleteProject(b.dataset.projdel, b.dataset.projname, renderProjects));
    for (const b of projectsSection.querySelectorAll("button[data-refadd]"))
      b.addEventListener("click", () => createDmnRef(b.dataset.refadd, renderProjects));
    for (const b of projectsSection.querySelectorAll("button[data-refdel]"))
      b.addEventListener("click", () => deleteDmnRef(b.dataset.refdel, renderProjects));
    for (const b of projectsSection.querySelectorAll("button[data-refvalidate]"))
      b.addEventListener("click", () => validateDmnRef(b.dataset.refvalidate));
    for (const b of projectsSection.querySelectorAll("button[data-projvalidate]"))
      b.addEventListener("click", () => validateProject(b.dataset.projvalidate));
    for (const b of projectsSection.querySelectorAll("button[data-projdeploy]"))
      b.addEventListener("click", () => deployProject(b.dataset.projdeploy, () => Promise.all([renderProjects(), render()])));
    for (const s of projectsSection.querySelectorAll("select[data-move]"))
      s.addEventListener("change", () => moveDraft(s.dataset.move, s.value, renderProjects));
    for (const s of projectsSection.querySelectorAll("select[data-moveref]"))
      s.addEventListener("change", () => moveDmnRef(s.dataset.moveref, s.value, renderProjects));
  };
  document.getElementById("new-project").addEventListener("click", () => createProject(renderProjects));

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

// ---------- Tasks ----------
async function viewTasks() {
  view.innerHTML = `
    <div class="card" id="task-card">
      <div style="display:flex;align-items:center;justify-content:space-between;margin-bottom:16px">
        <h1 style="margin:0">Task inbox</h1>
        <button class="btn ghost" id="task-refresh">Refresh</button>
      </div>
      <div id="task-list"><p class="muted">Loading&hellip;</p></div>
    </div>`;
  async function load() {
    const list = document.getElementById("task-list");
    try {
      const res = await api("/api/v1/tasks");
      if (!res.ok) throw new Error("HTTP " + res.status);
      const tasks = await res.json();
      if (!tasks.length) {
        list.innerHTML = `<p class="muted">No open tasks.</p>`;
        return;
      }
      list.innerHTML = `<table class="tbl">
        <thead><tr><th>Process</th><th>Element</th><th>Assignee</th><th>Groups</th><th></th></tr></thead>
        <tbody>${tasks.map((t) => `<tr data-key="${t.key}">
          <td>${esc(t.processId)}</td>
          <td>${esc(t.elementId)}</td>
          <td>${esc(t.assignee || "—")}</td>
          <td>${esc(t.candidateGroups || "—")}</td>
          <td><button class="btn small task-complete" data-key="${t.key}">Complete</button></td>
        </tr>`).join("")}</tbody></table>`;
      list.querySelectorAll(".task-complete").forEach((btn) => {
        btn.addEventListener("click", async () => {
          btn.disabled = true;
          try {
            const r = await api("/api/v1/tasks/" + btn.dataset.key + "/complete", { method: "POST" });
            if (!r.ok) throw new Error("HTTP " + r.status);
            toast("Task completed");
            await load();
          } catch (e) { toast("Complete failed: " + e.message, "err"); btn.disabled = false; }
        });
      });
    } catch (e) { list.innerHTML = `<p class="muted err">Failed to load tasks: ${esc(e.message)}</p>`; }
  }
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

async function viewEditor(key) {
  const mod = await import("./editor.js");
  await mod.mountEditor(view, { api, toast, key });
}

async function viewEditorDraft(id) {
  const mod = await import("./editor.js");
  await mod.mountEditor(view, { api, toast, draftId: id });
}

async function viewLive(key) {
  const mod = await import("./editor.js");
  await mod.mountLive(view, { api, toast, key });
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
    if (path === "#/modeler/new") return await viewEditor(null);
    const dm = path.match(/^#\/modeler\/draft\/(.+)$/);
    if (dm) return await viewEditorDraft(decodeURIComponent(dm[1]));
    const m = path.match(/^#\/modeler\/d\/(\d+)$/);
    if (m) return await viewEditor(Number(m[1]));
    if (path === "#/tasks") return await viewTasks();
    if (path === "#/operations") return await viewInstances();
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
