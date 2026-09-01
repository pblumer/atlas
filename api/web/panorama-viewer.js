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

// SEVERITY_TEXT is how an observation's class reads in the panel. It matches the
// landscape mesh's wording deliberately: the same finding, seen from the drawing
// and from the derived graph, must not be described in two different vocabularies.
const SEVERITY_TEXT = {
  ok: "OK",
  attention: "Attention",
  critical: "Critical",
  unknown: "Not watched",
};

// STATE_TEXT names the observation state under the class (ADR-0189 §6). The class
// is a reading aid; the state is what somebody acts on, so both are shown.
const STATE_TEXT = {
  healthy: "healthy",
  degraded: "degraded",
  "not-ready": "not ready",
  unreachable: "unreachable",
  stale: "stale",
  unbound: "nothing here observes it",
};

// liveHTML is what this element is *doing*, beside what it is (ADR-0189 §6).
//
// It is a section of the properties panel rather than an overlay on the diagram,
// and that is the ADR's own constraint reaching the UI: ArchiMate layer colours
// stay intact, so runtime state is carried by text and badges rather than by
// recolouring an element's fill out from under its semantics.
//
// A model with no observations for the selected element says so. The alternative —
// rendering nothing — reads as an element that is fine, and the difference between
// "nothing is wrong" and "nothing is watching" is the one this whole projection
// exists to keep.
function liveHTML(item, observations) {
  if (!item || !observations) return "";
  const mine = (observations.observations || []).filter((o) => o.elementId === item.id);
  if (!mine.length) {
    return `<section class="psec"><h3>Live</h3>
      <p class="muted">This element binds nothing, so there is nothing to observe.</p></section>`;
  }
  const rows = mine.map((o) => `<div class="panorama-obs panorama-sev-${esc(o.severity)}">
      <div class="panorama-obs-head">
        <b>${esc(SEVERITY_TEXT[o.severity] || o.severity)}</b>
        <span class="muted">${esc(STATE_TEXT[o.state] || o.state)} · ${esc(o.source)}</span>
      </div>
      <code>${esc(o.key)} = ${esc(o.value)}</code>
      ${o.reason ? `<p>${esc(o.reason)}</p>` : ""}
      ${detailHTML(o.detail)}
    </div>`).join("");
  // What the view cannot see, stated beside what it can. Without it a model
  // nothing observes renders as an architecture where everything is fine.
  const unwatched = (observations.unavailable || []).map((u) => esc(STATE_TEXT[u.state] || u.state));
  return `<section class="psec"><h3>Live</h3>${rows}
    ${unwatched.length ? `<p class="muted panorama-unwatched">Not watched here: ${unwatched.join(", ")}.</p>` : ""}
    <p class="muted">Read at ${esc(new Date((observations.observedAt || 0) * 1000).toLocaleTimeString())};
    nothing here is stored on the model.</p></section>`;
}

// CANVAS_MARK is how a severity is drawn *on the diagram* (ADR-0189 §6, P4d).
//
// The record's constraint is the whole design here: "ArchiMate layer colors remain
// intact. Runtime state is shown with borders, badges, icons, and an accessible
// text legend rather than recoloring semantic element fills." A layer colour is
// semantics — it says whether an element is business, application or technology —
// and painting health over it would destroy one meaning to show another. So the
// element's own fill is never touched; a mark sits *around* and *beside* it.
//
// Three classes are drawn and one is not:
//
//   - critical and attention carry a glyph, because they are what somebody is
//     scanning the diagram for;
//   - ok carries a small unobtrusive dot rather than nothing, because "observed and
//     fine" and "not observed at all" must not look the same — that conflation is
//     the one this whole projection exists to prevent;
//   - unknown carries nothing. Most elements of a young model are unbound, and a
//     badge on every one of them would make the diagram a wall of marks.
const CANVAS_MARK = {
  critical: { glyph: "!", label: "Critical — it cannot do work" },
  attention: { glyph: "•", label: "Attention — something inside it went wrong" },
  ok: { glyph: "", label: "OK — observed, nothing wrong" },
};

// SEVERITY_RANK orders the classes so an element bound to several resources shows
// the worst of them. An element that is fine in one binding and broken in another
// is not fine, and showing the first answer found would depend on document order.
const SEVERITY_RANK = { unknown: 0, ok: 1, attention: 2, critical: 3 };

// worstByElement reduces a document to one finding per ArchiMate element.
export function worstByElement(observations) {
  const worst = new Map();
  for (const o of (observations && observations.observations) || []) {
    const held = worst.get(o.elementId);
    if (!held || (SEVERITY_RANK[o.severity] || 0) > (SEVERITY_RANK[held.severity] || 0)) {
      worst.set(o.elementId, o);
    }
  }
  return worst;
}

// detailHTML renders the numbers behind the sentence — a version, a count — for a
// reader who wants them. Sorted, because this is something people compare between
// two servers.
function detailHTML(detail) {
  const entries = Object.entries(detail || {}).sort(([a], [b]) => a.localeCompare(b));
  if (!entries.length) return "";
  return `<div class="panorama-obs-detail">${entries
    .map(([k, v]) => `<span><span class="muted">${esc(k)}</span> ${esc(v)}</span>`).join("")}</div>`;
}

function propertiesHTML(item, resolution, canEdit, observations) {
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
    ${bindingsHTML(item, resolution, canEdit)}
    ${liveHTML(item, observations)}`;
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

// The C4 projection (ADR-0211 §8). It is shown as a structure with its loss report,
// not as a second canvas: what makes a projection trustworthy is that it says what
// it could not express, and a picture that merely omitted those would look complete
// and be wrong. ArchiMate stays the only thing anybody authors here — there is no
// write counterpart to this view and there is not meant to be one.
function c4PanelHTML(projection) {
  const byParent = new Map();
  for (const element of projection.elements) {
    const key = element.parent || "";
    if (!byParent.has(key)) byParent.set(key, []);
    byParent.get(key).push(element);
  }
  const render = (element) => {
    const children = byParent.get(element.id) || [];
    return `<li>
      <span class="c4-type">${esc(element.type)}</span>
      <b>${esc(element.name || element.id)}</b>
      <code class="muted">${esc(element.sourceType)}</code>
      ${element.description ? `<p class="muted">${esc(element.description)}</p>` : ""}
      ${children.length ? `<ul class="c4-tree">${children.map(render).join("")}</ul>` : ""}
    </li>`;
  };
  const roots = (byParent.get("") || []).map(render).join("");

  const relationships = projection.relationships.map((rel) => `<li>
    <b>${esc(rel.source)}</b> → <b>${esc(rel.target)}</b>
    ${rel.name ? `<span>${esc(rel.name)}</span>` : ""}
    <code class="muted">${esc(rel.sourceType)}</code></li>`).join("");

  // The loss report is the contractual half of this view, so it is never collapsed
  // away and never rendered as a footnote.
  const dropped = projection.dropped.map((loss) => `<li>
    <b>${esc(loss.name || loss.id)}</b>
    <code class="muted">${esc(loss.sourceType)}</code>
    <span class="c4-reason">${esc(loss.reason)}</span></li>`).join("");

  return `<div class="c4-panel">
    <div class="c4-head">
      <h2>C4 projection</h2>
      <p class="muted">A read-only projection of this ArchiMate model at revision
        ${esc(projection.sourceRevision)}, using mapping version
        ${esc(projection.mappingVersion)}. Nothing here is authored: edit the ArchiMate
        model and project again.</p>
    </div>
    <section class="psec"><h3>Structure</h3>
      ${roots ? `<ul class="c4-tree">${roots}</ul>` : `<p class="muted">Nothing in this model projects into C4.</p>`}
    </section>
    <section class="psec"><h3>Relationships</h3>
      ${relationships ? `<ul class="c4-list">${relationships}</ul>` : `<p class="muted">None.</p>`}
    </section>
    <section class="psec c4-loss"><h3>Not projected (${projection.dropped.length})</h3>
      ${dropped
        ? `<p class="muted">C4 cannot express these. They are listed rather than
             dropped quietly — a projection that hid them would look complete and be
             wrong.</p><ul class="c4-list">${dropped}</ul>`
        : `<p class="muted">Everything in this model projects into C4.</p>`}
    </section>
  </div>`;
}

// markCanvas draws each element's worst finding onto the rendered diagram.
//
// It works on the DOM the renderer produced rather than inside the renderer,
// which is a deliberate choice and not a shortcut. The ArchiMate canvas is a
// pre-built bundle Atlas ships (ADR-0012: no build step, no CDN, no Node
// toolchain at run time), so a change that needed a rebuild would put a
// toolchain between an operator and a bug fix. Decorating afterwards keeps the
// bundle byte-identical and its checksum meaningful.
//
// The mark is a border *around* the element and a badge beside it. Nothing here
// writes fill: an ArchiMate layer colour says what kind of element it is, and
// overwriting it with health would destroy one meaning to show another
// (ADR-0189 §6).
function markCanvas(canvas, view, observations) {
  canvas.querySelectorAll(".panorama-canvas-mark").forEach((mark) => mark.remove());
  canvas.querySelectorAll(".djs-element").forEach((group) => {
    group.classList.remove("panorama-marked-ok", "panorama-marked-attention", "panorama-marked-critical");
  });
  if (!observations) return 0;

  const worst = worstByElement(observations);
  let marked = 0;
  for (const shape of view.shapes || []) {
    const finding = worst.get(shape.elementRef);
    const mark = finding && CANVAS_MARK[finding.severity];
    if (!mark) continue;
    // CSS.escape: a view node id comes from somebody's exchange document, so it
    // may hold anything a selector would otherwise read as syntax.
    const group = canvas.querySelector(`.djs-element[data-element-id="${CSS.escape(shape.id)}"]`);
    if (!group) continue;

    group.classList.add(`panorama-marked-${finding.severity}`);
    // The finding joins the element's accessible name rather than replacing it: a
    // screen reader has to hear what the element *is* before what it is doing.
    const spoken = group.getAttribute("aria-label") || shape.semantic?.name || shape.elementRef;
    group.setAttribute("aria-label",
      `${spoken} — ${mark.label.split(" — ")[0]}${finding.reason ? ": " + finding.reason : ""}`);

    const svg = document.createElementNS("http://www.w3.org/2000/svg", "g");
    svg.setAttribute("class", `panorama-canvas-mark panorama-mark-${finding.severity}`);
    svg.innerHTML = `<rect class="panorama-mark-border" x="-4" y="-4"
        width="${shape.width + 8}" height="${shape.height + 8}" rx="4"/>
      <circle class="panorama-mark-dot" cx="${shape.width - 6}" cy="6" r="${mark.glyph ? 8 : 4}"/>
      ${mark.glyph ? `<text class="panorama-mark-glyph" x="${shape.width - 6}" y="6" dy="3.5"
        text-anchor="middle">${esc(mark.glyph)}</text>` : ""}`;
    group.appendChild(svg);
    marked++;
  }
  return marked;
}

// canvasLegendHTML says what the marks mean, in words.
//
// ADR-0189 §6 asks for an accessible text legend in the same breath as the badges,
// and the reason is that a mark nobody can decode is decoration. It lists only the
// classes actually on the diagram — a legend describing findings the picture does
// not contain is a legend nobody reads twice — and states what an *unmarked*
// element means, which is the half a legend usually leaves out.
function canvasLegendHTML(observations, marked) {
  if (!observations) return "";
  const present = new Set();
  for (const finding of worstByElement(observations).values()) {
    if (CANVAS_MARK[finding.severity]) present.add(finding.severity);
  }
  if (!present.size) {
    return `<div class="panorama-live-legend"><span class="muted">Nothing on this view is
      observed: no element here binds a resource this server can see.</span></div>`;
  }
  const swatches = ["critical", "attention", "ok"].filter((key) => present.has(key)).map((key) => `
    <span class="panorama-live-swatch panorama-mark-${key}">
      <svg width="16" height="16" aria-hidden="true">
        <circle cx="8" cy="8" r="${CANVAS_MARK[key].glyph ? 7 : 4}" class="panorama-mark-dot"/>
        ${CANVAS_MARK[key].glyph ? `<text x="8" y="8" dy="3.5" text-anchor="middle"
          class="panorama-mark-glyph">${esc(CANVAS_MARK[key].glyph)}</text>` : ""}
      </svg>${esc(CANVAS_MARK[key].label)}</span>`).join("");
  const unwatched = (observations.unavailable || [])
    .map((u) => esc(STATE_TEXT[u.state] || u.state));
  return `<div class="panorama-live-legend">
    ${swatches}
    <span class="muted">${marked} of ${(view0Count(observations))} bound element(s) marked;
    an unmarked element binds nothing this server observes.</span>
    ${unwatched.length ? `<span class="muted">Not watched here: ${unwatched.join(", ")}.</span>` : ""}
  </div>`;
}

// view0Count is how many distinct elements the document has an observation for. It
// is the denominator the legend needs so "3 marked" is a proportion rather than a
// number floating on its own.
function view0Count(observations) {
  return worstByElement(observations).size;
}

export async function mountPanoramaViewer(container, { api, toast, id }) {
  container.innerHTML = `<div class="card empty"><p class="muted">Loading architecture view…</p></div>`;
  const [vendor, model, xml, applications, bindings, observations] = await Promise.all([
    loadVendor(),
    api("GET", `/api/v1/panorama/models/${encodeURIComponent(id)}`),
    api("GET", `/api/v1/panorama/models/${encodeURIComponent(id)}/xml`),
    api("GET", "/api/v1/applications").catch(() => []),
    // Bindings are additive: a model whose bindings cannot be read is still a
    // model worth opening, so this failure degrades the panel rather than the view.
    api("GET", `/api/v1/panorama/models/${encodeURIComponent(id)}/bindings`).catch(() => null),
    // Observations are additive in exactly the same way, and on a server that
    // observes nothing this route refuses outright — a model is still worth
    // opening either way, so the panel loses its Live section rather than the
    // view losing the diagram.
    api("GET", `/api/v1/panorama/models/${encodeURIComponent(id)}/observations`).catch(() => null),
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
      <button class="btn ghost small" data-tool="c4" aria-pressed="false">C4 projection</button>
      <a class="btn ghost small" href="/api/v1/panorama/models/${encodeURIComponent(id)}/xml">Export XML</a>
    </div>
    <div class="panorama-tools" aria-label="Canvas controls">
      <button class="icon-btn" data-tool="zoom-in" title="Zoom in" aria-label="Zoom in">+</button>
      <button class="icon-btn" data-tool="zoom-out" title="Zoom out" aria-label="Zoom out">−</button>
      <button class="icon-btn" data-tool="fit" title="Fit diagram" aria-label="Fit diagram">⊡</button>
    </div>
    <div class="editor-body">
      <div class="panorama-stage">
        <div class="panorama-canvas" role="tabpanel" aria-label="ArchiMate diagram"></div>
        <div class="panorama-live-legend-slot" aria-label="What the runtime marks mean"></div>
      </div>
      <aside class="props panorama-properties" aria-label="Properties">${propertiesHTML(null)}</aside>
    </div>
    <div class="problems panorama-problems" tabindex="0">
      <span>Problems</span><span class="badge">${parsed.problems.length}</span>
      <span class="prob-summary">${parsed.problems.length ? `${parsed.problems.length} import warning${parsed.problems.length === 1 ? "" : "s"}` : "Open Exchange view loaded"}</span>
      <span class="spacer"></span><span>ArchiMate 3.2 · revision ${esc(model.revision)}${application ? ` · ${esc(application.name)}` : ""}</span>
    </div>
  </div>`;

  const canvas = container.querySelector(".panorama-canvas");
  const legend = container.querySelector(".panorama-live-legend-slot");
  const properties = container.querySelector(".panorama-properties");
  // The application's role travels with the listing; editing a binding is authoring
  // the model, so it needs the same rights as any other write to it.
  const canEdit = ["owner", "editor"].includes(application?.myRole);
  let resolution = bindings;
  let live = observations;
  let revision = model.revision;
  let selected = null;

  const paintProperties = () => {
    properties.innerHTML = propertiesHTML(selected, resolution, canEdit, live);
  };

  let viewer = null;
  const select = (diagramView) => {
    if (!diagramView) {
      canvas.innerHTML = `<div class="panorama-no-view"><div>◇</div><h2>No diagram views</h2><p>This model contains reusable ArchiMate elements, but no Diagram view yet.</p></div>`;
      return;
    }
    if (!viewer) viewer = new vendor.Viewer(canvas, (item) => { selected = item; paintProperties(); });
    viewer.render(diagramView);
    // The renderer adds its shapes synchronously, so the DOM is there to decorate
    // the moment render returns. Re-marking on every view switch is not an
    // optimisation to skip: each render rebuilds the canvas, and a mark left over
    // from the previous view would sit on whatever element inherited its id.
    const marked = markCanvas(canvas, diagramView, live);
    legend.innerHTML = canvasLegendHTML(live, marked);
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

  const c4Button = container.querySelector('[data-tool="c4"]');
  let c4Open = false;
  c4Button.addEventListener("click", async () => {
    if (c4Open) {
      c4Open = false;
      c4Button.setAttribute("aria-pressed", "false");
      canvas.querySelector(".c4-panel")?.remove();
      select(parsed.views.find((v) => v.id === container.querySelector(".panorama-view-tab.active")?.dataset.view)
        || parsed.views[0]);
      return;
    }
    let projection;
    try {
      projection = await api("GET", `/api/v1/panorama/models/${encodeURIComponent(id)}/c4`);
    } catch (e) {
      toast(e.message);
      return;
    }
    viewer?.destroy();
    viewer = null;
    c4Open = true;
    c4Button.setAttribute("aria-pressed", "true");
    canvas.innerHTML = c4PanelHTML(projection);
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
