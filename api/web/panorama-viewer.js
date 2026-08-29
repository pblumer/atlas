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

function propertiesHTML(item) {
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
    ${item.documentation ? `<section class="psec"><h3>Documentation</h3><p>${esc(item.documentation)}</p></section>` : ""}`;
}

export async function mountPanoramaViewer(container, { api, id }) {
  container.innerHTML = `<div class="card empty"><p class="muted">Loading architecture view…</p></div>`;
  const [vendor, model, xml, applications] = await Promise.all([
    loadVendor(),
    api("GET", `/api/v1/panorama/models/${encodeURIComponent(id)}`),
    api("GET", `/api/v1/panorama/models/${encodeURIComponent(id)}/xml`),
    api("GET", "/api/v1/applications").catch(() => []),
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
  let viewer = null;
  const select = (diagramView) => {
    if (!diagramView) {
      canvas.innerHTML = `<div class="panorama-no-view"><div>◇</div><h2>No diagram views</h2><p>This model contains reusable ArchiMate elements, but no Diagram view yet.</p></div>`;
      return;
    }
    if (!viewer) viewer = new vendor.Viewer(canvas, (item) => { properties.innerHTML = propertiesHTML(item); });
    viewer.render(diagramView);
  };
  select(parsed.views[0]);

  container.querySelectorAll(".panorama-view-tab").forEach((button) => button.addEventListener("click", () => {
    container.querySelectorAll(".panorama-view-tab").forEach((tab) => {
      const active = tab === button;
      tab.classList.toggle("active", active);
      tab.setAttribute("aria-selected", String(active));
    });
    properties.innerHTML = propertiesHTML(null);
    select(parsed.views.find((item) => item.id === button.dataset.view));
  }));
  container.querySelector('[data-tool="zoom-in"]').addEventListener("click", () => viewer?.zoom(1.2));
  container.querySelector('[data-tool="zoom-out"]').addEventListener("click", () => viewer?.zoom(1 / 1.2));
  container.querySelector('[data-tool="fit"]').addEventListener("click", () => viewer?.fit());

  window.__atlasCleanup = () => {
    viewer?.destroy();
    viewer = null;
    window.__atlasCleanup = null;
  };
}
