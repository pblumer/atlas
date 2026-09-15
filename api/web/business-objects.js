// The class catalogue, and where one class is used
// (ADR-draft-where-a-business-object-is-used).
//
// The Data area has had two views of the vocabulary and both are *per model*: the
// canvas draws one document, the listing lists the documents. Neither answers the
// question somebody maintaining a business object actually has — "what else depends on
// this?" — and neither shows the estate's classes together, so nothing ever said that
// two applications model an Order twice.
//
// These two views read the other direction. The list is the vocabulary as a vocabulary:
// every class of every model, alphabetical, in the shared table so it filters and sorts
// like every other list in the console (ADR-0286). The detail page
// is one class and everywhere it is used — which process, which element, which member,
// which state — with the model's own uses beside it, because for an «enumeration» those
// are usually the only uses there are.
//
// Everything here is read-only. The server computes both readings on every call and
// stores neither.

import { enhanceTable } from "./table.js";

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;" }[c]));

// The three kinds of class the subset authors (ADR-0230), as a reader says them.
const KIND = {
  businessObject: "Business object",
  valueType: "Value type",
  enumeration: "Enumeration",
};
const kindOf = (s) => KIND[s] || s || "Class";

// What a process does with a class. The words are the server's; these are the
// sentences that say what each one means, once, in the place a reader meets it.
const HOW = {
  declare: { label: "declares", title: "A data object in this process is of this class" },
  read: { label: "reads", title: "An activity reads the object into a process variable before it runs" },
  write: { label: "writes", title: "An activity writes the object, a member of it, or moves its state" },
  store: { label: "store", title: "The process names a data store that keeps instances of this class" },
};

// And what the model does with it.
const MODEL_HOW = {
  attribute: { label: "typed as", title: "A class has an attribute of this type" },
  association: { label: "associated", title: "An association joins this class to another" },
  states: { label: "states of", title: "A lifecycle takes its states from this enumeration" },
  store: { label: "kept in", title: "A data store holds instances of this class" },
};

const detailHref = (modelId, name) =>
  `#/data/objects/${encodeURIComponent(modelId)}/${encodeURIComponent(name)}`;

const num = (n) => `<td class="muted" data-sort="${n}">${n ? n : "<span class=\"bo-none\">—</span>"}</td>`;

// usedCell is the list's one compound column: a class is used by processes, by the
// model, or by nothing — and the three are different situations, so they read
// differently rather than as three numbers next to each other. It sorts on the process
// count, which is the number somebody scans the column for.
function usedCell(u) {
  const procs = u.processes || 0;
  const model = u.modelUses || 0;
  const parts = [];
  if (procs) {
    parts.push(`<span class="pill">${procs} ${procs === 1 ? "process" : "processes"}</span>`);
    const how = [];
    if (u.reads) how.push(`${u.reads} read${u.reads === 1 ? "" : "s"}`);
    if (u.writes) how.push(`${u.writes} write${u.writes === 1 ? "" : "s"}`);
    if ((u.stores || []).length) how.push("a store");
    if (how.length) parts.push(`<span class="muted">${esc(how.join(" · "))}</span>`);
  }
  if (model) parts.push(`<span class="muted">${model} in the model</span>`);
  if (!parts.length) parts.push(`<span class="bo-none">used by nothing deployed</span>`);
  return `<td data-sort="${procs}" data-filter="${esc(
    [procs ? procs + " processes" : "", model ? model + " model" : "", procs || model ? "" : "unused"].filter(Boolean).join(" "),
  )}"><div class="bo-used">${parts.join(" ")}</div></td>`;
}

// Unix seconds, like every other timestamp the API hands the console.
const fmtTime = (unix) => (unix ? new Date(unix * 1000).toLocaleString() : "—");

// ---------- the list ----------

export async function mountObjectCatalog(root, { api }) {
  root.innerHTML = `<div class="bo-root">
    <div class="between">
      <div>
        <h1>Business objects</h1>
        <p class="muted" style="margin:0">Every class in every information model you can see — business
          objects, value types and enumerations — with where each one is used. One list across
          applications, because two applications modelling an Order twice is exactly what a per-model
          view cannot show.</p>
      </div>
      <a class="btn neutral" href="#/data">Information models →</a>
    </div>
    <div id="bo-body"><p class="muted" style="margin-top:16px">Reading the models and the processes…</p></div>
  </div>`;

  const body = root.querySelector("#bo-body");
  let rows;
  try {
    rows = await api("GET", "/api/v1/infomodel/classes");
  } catch (e) {
    body.innerHTML = `<div class="card empty"><p>${esc(e.message)}</p></div>`;
    return;
  }
  if (!root.isConnected) return; // navigated away while it was read

  if (!rows.length) {
    body.innerHTML = `<div class="card empty" style="margin-top:16px">
      <h2>Nothing is modelled yet</h2>
      <p class="muted">No information model you can see declares a class. Draw one under
        <a href="#/data">Data</a> — an Order, a Customer, a Claim — and every process that
        names it in a data object's type will show up here.</p>
    </div>`;
    return;
  }

  const row = (c) => `<tr>
    <td><div class="artifact-name">
      <a href="${detailHref(c.modelId, c.name)}"><b>${esc(c.name)}</b></a></div>
      ${c.documentation ? `<div class="bo-doc">${esc(c.documentation)}</div>` : ""}</td>
    <td><span class="chip bo-kind ${esc(c.stereotype || "")}">${esc(kindOf(c.stereotype))}</span></td>
    <td class="muted">${esc(c.applicationName || c.applicationId || "")}</td>
    <td class="muted"><a href="#/data/m/${encodeURIComponent(c.modelId)}">${esc(c.modelName)}</a></td>
    ${num(c.members || 0)}
    <td class="muted">${(c.identity || []).length
      ? `<code>${esc((c.identity || []).join(", "))}</code>`
      : `<span class="bo-none">—</span>`}</td>
    ${num(c.states || 0)}
    ${usedCell(c.usage || {})}
    <td class="muted" data-sort="${c.updatedAt || 0}">${esc(fmtTime(c.updatedAt))}</td>
  </tr>`;

  body.innerHTML = `<div class="card" style="padding:0; margin-top:16px">
      <table data-dt-key="info-classes">
        <thead><tr>
          <th>Business object</th><th>Kind</th><th>Application</th><th>Model</th>
          <th>Members</th><th>Business key</th><th>States</th><th>Used by</th><th>Last changed</th>
        </tr></thead>
        <tbody>${rows.map(row).join("")}</tbody>
      </table>
    </div>
    <p class="bo-scope">“Used by” reads the processes this installation actually runs — the deployed,
      active, latest version of each. A draft in the Modeler is not read, so a class used by nothing here
      may still be used by work in progress.</p>`;

  enhanceTable(body.querySelector("table"), {
    key: "info-classes",
    columns: [{}, {}, {}, {}, { type: "number" }, {}, { type: "number" }, { type: "number" }, {}],
  });
}

// ---------- one class ----------

export async function mountObjectDetail(root, { api, modelId, className }) {
  root.innerHTML = `<div class="bo-root"><p class="muted">Reading where ${esc(className)} is used…</p></div>`;

  let u;
  try {
    u = await api("GET", `/api/v1/infomodel/models/${encodeURIComponent(modelId)}/usage?class=${encodeURIComponent(className)}`);
  } catch (e) {
    root.innerHTML = `<div class="card empty"><h1>${esc(className)}</h1>
      <p>${esc(e.message)}</p><a class="btn ghost" href="#/data/objects">Back to the list</a></div>`;
    return;
  }
  if (!root.isConnected) return;

  const c = u.class || {};
  const s = u.summary || {};
  const isEnum = c.stereotype === "enumeration";

  root.innerHTML = `<div class="bo-root">
    <div class="between">
      <div>
        <div class="bo-crumb"><a href="#/data/objects">Business objects</a>
          <span>·</span><span>${esc(u.applicationName || u.applicationId || "")}</span>
          <span>·</span><a href="#/data/m/${encodeURIComponent(u.modelId)}">${esc(u.modelName || "")}</a></div>
        <h1 class="bo-title">${esc(c.name || className)}
          <span class="chip bo-kind ${esc(c.stereotype || "")}">${esc(kindOf(c.stereotype))}</span></h1>
        ${c.documentation ? `<p class="muted" style="margin:6px 0 0; max-width:70ch">${esc(c.documentation)}</p>` : ""}
      </div>
      <div style="display:flex; gap:8px; align-items:flex-start">
        <a class="btn neutral" href="#/data/m/${encodeURIComponent(u.modelId)}">Open the diagram →</a>
        ${isEnum ? "" : `<a class="btn neutral" href="#/data/instances?class=${encodeURIComponent(c.name || "")}">Instances →</a>`}
      </div>
    </div>

    <div class="card bo-summary">
      <div class="stats">
        <div class="stat"><b>${s.processes || 0}</b><span>${(s.processes === 1) ? "process" : "processes"}</span></div>
        <div class="stat"><b>${s.reads || 0}</b><span>${(s.reads === 1) ? "read" : "reads"}</span></div>
        <div class="stat"><b>${s.writes || 0}</b><span>${(s.writes === 1) ? "write" : "writes"}</span></div>
        <div class="stat"><b>${s.modelUses || 0}</b><span>in the model</span></div>
      </div>
      ${(s.uses || 0) ? `<div class="bo-touched">
        ${touched("Members written", s.attributes, (c.attributes || []).length && !isEnum
    ? `of ${(c.attributes || []).length}` : "")}
        ${touched("States reached", s.states, (c.lifecycle && c.lifecycle.states || []).length
    ? `of ${(c.lifecycle.states || []).length}` : "")}
        ${touched("Kept in", s.stores, "")}
      </div>` : ""}
    </div>

    ${structureHTML(c, isEnum)}

    <section class="bo-sec">
      <h2>Used in processes <span class="bo-count">${(u.processes || []).length}</span></h2>
      <p class="bo-meaning">Every deployed process that declares a data object of this class, and every
        element that reads it, writes it, moves its state, or names the store it is kept in. Only the
        deployed, active, latest version of each process is read — a Modeler draft is not.</p>
      ${processTableHTML(u.processes || [])}
    </section>

    <section class="bo-sec">
      <h2>Used in the model <span class="bo-count">${(u.model || []).length}</span></h2>
      <p class="bo-meaning">Where the vocabulary itself refers to this class. For an enumeration these
        are usually the only uses there are: nothing declares a data object of it, and everything is
        typed with it.</p>
      ${modelTableHTML(u.model || [])}
    </section>
  </div>`;

  for (const t of root.querySelectorAll("table:not(.no-enhance)")) {
    enhanceTable(t, { key: t.dataset.dtKey || undefined });
  }
}

// touched renders one "what processes actually reach" line. The counted total beside it
// is the point: the gap between what a class declares and what anything touches is the
// fact somebody is looking for.
function touched(label, values, of) {
  const list = values || [];
  return `<div class="bo-touched-row"><span class="bo-touched-label">${esc(label)}</span>
    ${list.length
    ? list.map((v) => `<span class="chip">${esc(v)}</span>`).join(" ")
    : `<span class="bo-none">none</span>`}
    ${of ? `<span class="muted">${esc(of)}</span>` : ""}</div>`;
}

// structureHTML is what the class *is*: its members, its business key, its life. It is
// the same document the canvas draws, read as a list — a person who came here from the
// catalogue wants to check a member's name without loading a diagram.
function structureHTML(c, isEnum) {
  const members = isEnum
    ? `<table class="no-enhance bo-lit"><thead><tr><th>Literal</th></tr></thead><tbody>
        ${(c.literals || []).map((l) => `<tr><td><code>${esc(l)}</code></td></tr>`).join("")
    || `<tr><td class="empty">This enumeration has no literals yet.</td></tr>`}
      </tbody></table>`
    : `<table class="no-enhance"><thead><tr><th>Attribute</th><th>Type</th><th>Multiplicity</th><th></th></tr></thead>
        <tbody>${(c.attributes || []).map((a) => `<tr>
          <td><code>${esc(a.name)}</code>${a.documentation ? `<div class="bo-doc">${esc(a.documentation)}</div>` : ""}</td>
          <td class="muted">${esc(a.type || "")}</td>
          <td class="muted">${esc(a.multiplicity || "")}</td>
          <td>${(c.identity || []).includes(a.name)
      ? `<span class="pill" title="Part of the business key — the fact that makes this the same thing in three processes">key</span>`
      : ""}</td></tr>`).join("")
    || `<tr><td colspan="4" class="empty">No attributes yet.</td></tr>`}</tbody></table>`;

  const lc = c.lifecycle;
  const life = !lc ? "" : `<div class="bo-life">
    <h3>Lifecycle${lc.statesFrom ? ` <span class="muted">states from «${esc(lc.statesFrom)}»</span>` : ""}</h3>
    <div class="bo-states">${(lc.states || []).map((st) => `<span class="chip bo-state${st.initial ? " initial" : ""}${st.final ? " final" : ""}"
      title="${st.initial ? "Where an instance starts" : st.final ? "Nothing leaves this state" : "A stage in this object's life"}">${esc(st.name)}</span>`).join(" ")}</div>
    <ul class="bo-moves">${(lc.transitions || []).map((t) => `<li><code>${esc(t.from)}</code> →
      <code>${esc(t.to)}</code>${t.name ? ` <span class="muted">· ${esc(t.name)}</span>` : ""}</li>`).join("")}</ul>
  </div>`;

  return `<section class="bo-sec">
    <h2>What it is</h2>
    <div class="card" style="padding:0">${members}</div>
    ${life}
  </section>`;
}

function processTableHTML(uses) {
  if (!uses.length) {
    return `<div class="card empty"><p class="muted">No deployed process declares a data object of this
      class. Give a data object an <b>itemSubjectRef</b> naming it in the Modeler, deploy, and every
      read and write of it appears here.</p></div>`;
  }
  const row = (p) => {
    const how = HOW[p.kind] || { label: p.kind, title: "" };
    return `<tr>
      <td><a href="#/operations/p/${encodeURIComponent(String(p.processDefinitionKey))}"><b>${esc(p.processName || p.processId)}</b></a>
        ${p.version ? `<span class="chip">v${esc(String(p.version))}</span>` : ""}
        <div class="bo-doc"><code>${esc(p.processId)}</code></div></td>
      <td><span class="chip bo-how ${esc(p.kind)}" title="${esc(how.title)}">${esc(how.label)}</span></td>
      <td class="muted">${p.elementId ? `<code>${esc(p.elementId)}</code>` : `<span class="bo-none">—</span>`}</td>
      <td class="muted">${p.store
      ? `<span title="A data store, not a data object">🗄 ${esc(p.store)}</span>`
      : (p.object ? `<code>${esc(p.object)}</code>${p.collection ? ` <span class="chip">list</span>` : ""}` : `<span class="bo-none">—</span>`)}</td>
      <td class="muted">${memberCell(p)}</td>
      <td class="muted">${p.state ? `<span class="chip bo-state">${esc(p.state)}</span>` : `<span class="bo-none">—</span>`}</td>
      <td class="muted">${p.variable ? `<code>${esc(p.variable)}</code>` : `<span class="bo-none">—</span>`}</td>
    </tr>`;
  };
  return `<div class="card" style="padding:0">
    <table data-dt-key="info-class-uses">
      <thead><tr>
        <th>Process</th><th>How</th><th>Element</th><th>Data object</th>
        <th>Member</th><th>State</th><th>Reads into</th>
      </tr></thead>
      <tbody>${uses.map(row).join("")}</tbody>
    </table>
  </div>`;
}

// memberCell says what a write touches. Three different acts, and calling them all
// "the whole object" would report a state-only transition as having written a value.
function memberCell(p) {
  if (p.attribute) return `<code>${esc(p.attribute)}</code>`;
  if (p.kind !== "write") return `<span class="bo-none">—</span>`;
  return p.writesValue
    ? `<span class="bo-none" title="The write sets the whole value">whole object</span>`
    : `<span class="bo-none" title="The write carries no value: it only moves the data state">state only</span>`;
}

function modelTableHTML(uses) {
  if (!uses.length) {
    return `<div class="card empty"><p class="muted">Nothing else in the vocabulary refers to this class:
      no attribute is typed with it, no association reaches it, no lifecycle takes its states from it and
      no store holds it.</p></div>`;
  }
  const row = (m) => {
    const how = MODEL_HOW[m.kind] || { label: m.kind, title: "" };
    return `<tr>
      <td><span class="chip bo-how ${esc(m.kind)}" title="${esc(how.title)}">${esc(how.label)}</span></td>
      <td>${m.class
      ? `<a href="${detailHref(m.modelId, m.class)}"><b>${esc(m.class)}</b></a>`
      : `<span class="bo-none">—</span>`}</td>
      <td class="muted">${m.name ? `<code>${esc(m.name)}</code>` : `<span class="bo-none">—</span>`}</td>
      <td class="muted">${esc(m.detail || "")}</td>
      <td class="muted"><a href="#/data/m/${encodeURIComponent(m.modelId)}">${esc(m.modelName || "")}</a></td>
    </tr>`;
  };
  return `<div class="card" style="padding:0">
    <table data-dt-key="info-class-model-uses">
      <thead><tr><th>How</th><th>Class</th><th>What</th><th>Detail</th><th>Model</th></tr></thead>
      <tbody>${uses.map(row).join("")}</tbody>
    </table>
  </div>`;
}
