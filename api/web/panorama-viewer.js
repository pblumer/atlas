// Read-only ArchiMate 3.2 diagram surface (ADR-0189). The canonical XML stays
// untouched on the server; this module only projects its Diagram views into the
// Atlas-owned diagram-js renderer vendored under vendor/archimate.

const esc = (value) => String(value ?? "").replace(/[&<>"']/g, (character) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[character]);

let vendorPromise;
function loadVendor() {
  if (globalThis.AtlasArchiMate) return Promise.resolve(globalThis.AtlasArchiMate);
  if (vendorPromise) return vendorPromise;
  const link = document.createElement("link");
  link.rel = "stylesheet";
  link.href = "vendor/archimate/diagram-js.css";
  document.head.appendChild(link);
  vendorPromise = new Promise((resolve, reject) => {
    const script = document.createElement("script");
    script.src = "vendor/archimate/archimate-viewer.js";
    script.onload = () => resolve(globalThis.AtlasArchiMate);
    script.onerror = () => reject(new Error("Could not load the ArchiMate renderer"));
    document.head.appendChild(script);
  });
  return vendorPromise;
}

const prettyType = (type) => String(type || "Element").replace(/([a-z])([A-Z])/g, "$1 $2");

// Atlas bindings (ADR-0189 §4) say which Atlas resource an element refers to. The
// document stores an opaque id and nothing else; the name below always comes from
// the server, so a drawing can never hold a stale copy of it.

// KEYS maps each binding key to the element types it is valid on and the label the
// panel shows. It mirrors the server's contract; a key the server would refuse must
// not be offerable here, or the panel would invite an error.
const BINDING_KEYS = [
  { key: "atlas.applicationId", label: "Process application", on: ["ApplicationComponent"] },
  { key: "atlas.processId", label: "BPMN process", on: ["BusinessProcess"] },
  { key: "atlas.connectorId", label: "Worker", on: ["ApplicationService", "ApplicationInterface"] },
  { key: "atlas.jobType", label: "Job type", on: ["ApplicationService", "ApplicationInterface"] },
  { key: "atlas.runtimeId", label: "Atlas runtime", on: ["Node"] },
  { key: "atlas.deploymentTargetId", label: "Deployment target", on: ["Node"] },
  { key: "atlas.releaseId", label: "Release", on: ["Artifact"] },
];

// STATUS_TEXT turns a resolution status into a sentence. The four are deliberately
// distinct: "you may not see it" is not "it is gone", and neither is "this server
// cannot resolve that kind yet".
const STATUS_TEXT = {
  forbidden: "outside your access",
  missing: "no longer on this server",
  unsupported: "this server cannot resolve this kind yet",
};

function bindingValueHTML(value) {
  if (value.status === "resolved") {
    return `<li class="panorama-bound"><b>${esc(value.name)}</b>
      <code class="muted">${esc(value.value)}</code></li>`;
  }
  // An unresolved binding is shown, never hidden: dropping it would make a broken
  // binding look like an absent one, and the model would then look correct.
  return `<li class="panorama-bound unresolved"><code>${esc(value.value)}</code>
    <span class="panorama-unresolved">${esc(STATUS_TEXT[value.status] || value.status)}</span></li>`;
}

function bindingsHTML(item, resolution, canEdit) {
  if (!item || item.kind === "relationship") return "";
  const applicable = BINDING_KEYS.filter((entry) => entry.on.includes(item.type));
  if (!applicable.length) {
    return `<section class="psec"><h3>Atlas bindings</h3>
      <p class="muted">No Atlas resource kind binds to a ${esc(prettyType(item.type))}.</p></section>`;
  }
  const byKey = new Map();
  for (const binding of resolution?.bindings || []) {
    if (binding.elementId === item.id) byKey.set(binding.key, binding.values);
  }
  const rows = applicable.map((entry) => {
    const values = byKey.get(entry.key) || [];
    return `<div class="panorama-binding" data-key="${esc(entry.key)}">
      <div class="panorama-binding-head">
        <span>${esc(entry.label)}</span>
        ${canEdit ? `<button class="btn ghost small" data-bind-edit="${esc(entry.key)}">${values.length ? "Change" : "Bind"}</button>` : ""}
      </div>
      ${values.length
        ? `<ul class="panorama-bound-list">${values.map(bindingValueHTML).join("")}</ul>`
        : `<p class="muted">Not bound.</p>`}
    </div>`;
  }).join("");
  return `<section class="psec"><h3>Atlas bindings</h3>${rows}</section>`;
}

function propertiesHTML(item, resolution, canEdit) {
  if (!item) return `<div class="panorama-props-empty">
    <div class="panorama-selection-icon">◇</div>
    <b>Nothing selected</b>
    <p>Select an element or relationship in the diagram to inspect its ArchiMate properties.</p>
  </div>`;
  const relationship = item.kind === "relationship";
  return `<div class="phead">
      <span class="ptype" aria-hidden="true">${relationship ? "↗" : "◇"}</span>
      <div><b>${esc(item.name || prettyType(item.type))}</b><div class="muted">${relationship ? "Relationship" : "Element"}</div></div>
    </div>
    <section class="psec"><h3>ArchiMate</h3>
      <div class="panorama-kv"><span>Type</span><b>${esc(prettyType(item.type))}</b></div>
      <div class="panorama-kv"><span>Identifier</span><code>${esc(item.id)}</code></div>
      ${relationship ? `<div class="panorama-kv"><span>Source</span><code>${esc(item.source)}</code></div>
        <div class="panorama-kv"><span>Target</span><code>${esc(item.target)}</code></div>` : ""}
    </section>
    ${item.documentation ? `<section class="psec"><h3>Documentation</h3><p>${esc(item.documentation)}</p></section>` : ""}
    ${bindingsHTML(item, resolution, canEdit)}`;
}

// pickBinding is the picker ADR-0189 §4 asks for: a user selects from resources
// they may see rather than typing an opaque id. Multi-select, because a binding is
// many-to-many — one ArchiMate component can be implemented by several Atlas
// process applications, and the picker must not quietly make that a single choice.
//
// Built on the shell's modal pattern rather than a new one. Resolves to the chosen
// ids, or null when cancelled — an empty array is a real answer here, since it is
// how a binding is cleared.
function pickBinding(list, current, key) {
  const chosen = new Set((current?.values || []).map((value) => value.value));
  return new Promise((resolve) => {
    const ov = document.createElement("div");
    ov.className = "modal-ov";
    ov.innerHTML = `
      <div class="modal" role="dialog" aria-modal="true" aria-label="Bind Atlas resource">
        <div class="modal-head"><h2>Bind ${esc(key)}</h2></div>
        <div class="modal-body">
          <p class="muted" style="margin:0 0 10px">Only resources you may see are listed.
            Selecting none clears the binding.</p>
          <ul class="panorama-pick">
            ${list.candidates.map((candidate) => `<li><label>
              <input type="checkbox" value="${esc(candidate.id)}" ${chosen.has(candidate.id) ? "checked" : ""}/>
              <span><b>${esc(candidate.name)}</b> <code class="muted">${esc(candidate.id)}</code></span>
            </label></li>`).join("")}
          </ul>
        </div>
        <div class="modal-foot">
          <button class="btn neutral" data-cancel>Cancel</button>
          <button class="btn" data-confirm>Save binding</button>
        </div>
      </div>`;
    document.body.appendChild(ov);
    const close = (result) => { ov.remove(); document.removeEventListener("keydown", onKey); resolve(result); };
    const onKey = (event) => { if (event.key === "Escape") close(null); };
    document.addEventListener("keydown", onKey);
    ov.querySelector("[data-cancel]").addEventListener("click", () => close(null));
    ov.querySelector("[data-confirm]").addEventListener("click", () => {
      close([...ov.querySelectorAll("input:checked")].map((input) => input.value));
    });
    ov.addEventListener("click", (event) => { if (event.target === ov) close(null); });
    ov.querySelector("input, [data-confirm]")?.focus();
  });
}

export async function mountPanoramaViewer(container, { api, toast, id }) {
  container.innerHTML = `<div class="card empty"><p class="muted">Loading architecture view…</p></div>`;
  const [vendor, model, xml, applications, bindings] = await Promise.all([
    loadVendor(),
    api("GET", `/api/v1/panorama/models/${encodeURIComponent(id)}`),
    api("GET", `/api/v1/panorama/models/${encodeURIComponent(id)}/xml`),
    api("GET", "/api/v1/applications").catch(() => []),
    // Bindings are additive: a model whose bindings cannot be read is still a
    // model worth opening, so this failure degrades the panel rather than the view.
    api("GET", `/api/v1/panorama/models/${encodeURIComponent(id)}/bindings`).catch(() => null),
  ]);
  const parsed = vendor.parseOpenExchange(xml);
  const application = applications.find((item) => item.id === model.applicationId);

  container.innerHTML = `<div class="editor live panorama-editor">
    <div class="editor-bar">
      <nav class="crumbs" aria-label="Breadcrumb">
        <a href="#/panorama">Models</a><span class="crumb-sep">/</span>
        <span class="crumb-current">${esc(model.name)}</span>
      </nav>
      <div class="panorama-view-tabs" role="tablist" aria-label="Architecture views">
        ${parsed.views.map((item, index) => `<button class="panorama-view-tab${index === 0 ? " active" : ""}" role="tab" aria-selected="${index === 0}" data-view="${esc(item.id)}">${esc(item.name)}</button>`).join("")}
      </div>
      <span class="spacer"></span>
      <span class="panorama-status"><span class="panorama-lock" aria-hidden="true">▣</span> Read only</span>
      <a class="btn ghost small" href="/api/v1/panorama/models/${encodeURIComponent(id)}/xml">Export XML</a>
    </div>
    <div class="panorama-tools" aria-label="Canvas controls">
      <button class="icon-btn" data-tool="zoom-in" title="Zoom in" aria-label="Zoom in">+</button>
      <button class="icon-btn" data-tool="zoom-out" title="Zoom out" aria-label="Zoom out">−</button>
      <button class="icon-btn" data-tool="fit" title="Fit diagram" aria-label="Fit diagram">⊡</button>
    </div>
    <div class="editor-body">
      <div class="panorama-canvas" role="tabpanel" aria-label="ArchiMate diagram"></div>
      <aside class="props panorama-properties" aria-label="Properties">${propertiesHTML(null)}</aside>
    </div>
    <div class="problems panorama-problems" tabindex="0">
      <span>Problems</span><span class="badge">${parsed.problems.length}</span>
      <span class="prob-summary">${parsed.problems.length ? `${parsed.problems.length} import warning${parsed.problems.length === 1 ? "" : "s"}` : "Open Exchange view loaded"}</span>
      <span class="spacer"></span><span>ArchiMate 3.2 · revision ${esc(model.revision)}${application ? ` · ${esc(application.name)}` : ""}</span>
    </div>
  </div>`;

  const canvas = container.querySelector(".panorama-canvas");
  const properties = container.querySelector(".panorama-properties");
  // The application's role travels with the listing; editing a binding is authoring
  // the model, so it needs the same rights as any other write to it.
  const canEdit = ["owner", "editor"].includes(application?.myRole);
  let resolution = bindings;
  let revision = model.revision;
  let selected = null;

  const paintProperties = () => {
    properties.innerHTML = propertiesHTML(selected, resolution, canEdit);
  };

  let viewer = null;
  const select = (diagramView) => {
    if (!diagramView) {
      canvas.innerHTML = `<div class="panorama-no-view"><div>◇</div><h2>No diagram views</h2><p>This model contains reusable ArchiMate elements, but no Diagram view yet.</p></div>`;
      return;
    }
    if (!viewer) viewer = new vendor.Viewer(canvas, (item) => { selected = item; paintProperties(); });
    viewer.render(diagramView);
  };
  select(parsed.views[0]);

  container.querySelectorAll(".panorama-view-tab").forEach((button) => button.addEventListener("click", () => {
    container.querySelectorAll(".panorama-view-tab").forEach((tab) => {
      const active = tab === button;
      tab.classList.toggle("active", active);
      tab.setAttribute("aria-selected", String(active));
    });
    selected = null;
    paintProperties();
    select(parsed.views.find((item) => item.id === button.dataset.view));
  }));
  // Binding edits are delegated from the panel, which is re-rendered on every
  // selection: a listener bound to a button would not survive the next repaint.
  properties.addEventListener("click", async (event) => {
    const button = event.target.closest("[data-bind-edit]");
    if (!button || !selected) return;
    const key = button.getAttribute("data-bind-edit");
    let list;
    try {
      list = await api("GET",
        `/api/v1/panorama/models/${encodeURIComponent(id)}/bindings/candidates?key=${encodeURIComponent(key)}`);
    } catch (e) {
      toast(e.message);
      return;
    }
    if (!list.supported) {
      toast("This Atlas version cannot resolve that kind of binding yet.");
      return;
    }
    if (!list.candidates.length) {
      toast("You have access to no resource of that kind to bind.");
      return;
    }
    const current = (resolution?.bindings || [])
      .find((b) => b.elementId === selected.id && b.key === key);
    const chosen = await pickBinding(list, current, key);
    if (chosen === null) return; // cancelled

    try {
      const updated = await api("PUT", `/api/v1/panorama/models/${encodeURIComponent(id)}/bindings`, {
        expectedRevision: revision, elementId: selected.id, key, values: chosen,
      });
      revision = updated.revision;
      // Re-read rather than patching locally: the server resolves names, and a
      // panel that invented them would be showing something the model does not say.
      resolution = await api("GET", `/api/v1/panorama/models/${encodeURIComponent(id)}/bindings`);
      paintProperties();
      toast(chosen.length ? "Binding saved." : "Binding cleared.");
    } catch (e) {
      toast(e.message);
    }
  });

  container.querySelector('[data-tool="zoom-in"]').addEventListener("click", () => viewer?.zoom(1.2));
  container.querySelector('[data-tool="zoom-out"]').addEventListener("click", () => viewer?.zoom(1 / 1.2));
  container.querySelector('[data-tool="fit"]').addEventListener("click", () => viewer?.fit());

  window.__atlasCleanup = () => {
    viewer?.destroy();
    viewer = null;
    window.__atlasCleanup = null;
  };
}
