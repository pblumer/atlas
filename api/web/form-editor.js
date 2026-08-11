// Form editor view. Three tabs over a single shared form schema (ADR-0028):
//
//   • Design   — the vendored form-js Playground: the reference modeler's split
//                surface (Form Definition editor beside a live Form Preview,
//                with Form Input and Form Output below).
//   • Validate — a testing surface. The form is rendered with the runtime
//                viewer, seeded from an editable Form Input (sample data), and
//                the live result / validation errors show in Form Output. Its
//                three areas can each be collapsed and resized independently.
//   • Editor   — the raw schema as text in a JSON code editor.
//
// The three surfaces stay in sync through one canonical `schema` value: leaving
// a tab commits its edits into it, entering a tab (re)loads it. Assets load
// lazily so non-editor pages stay light, mirroring the BPMN editor (editor.js).

import { attachJSONEditor } from "./json-editor.js";

const FORM_CSS = "vendor/form-js/form-playground.css";
const VIEWER_CSS = "vendor/form-js/form-js.css";

// ensureCss injects a stylesheet once, keyed by id so repeat calls are cheap.
function ensureCss(href, id) {
  if (document.getElementById(id)) return;
  const l = document.createElement("link");
  l.id = id;
  l.rel = "stylesheet";
  l.href = href;
  document.head.appendChild(l);
}

let playgroundReady; // memoized loader promise → { Playground }
function loadPlayground() {
  if (!playgroundReady) {
    ensureCss(FORM_CSS, "form-playground-css");
    playgroundReady = import("./vendor/form-js/form-playground.js");
  }
  return playgroundReady;
}

let viewerReady; // memoized loader promise → { Form }
function loadFormViewer() {
  if (!viewerReady) {
    // Same stylesheet id the tasks app uses, so the 86 KB viewer CSS loads once.
    ensureCss(VIEWER_CSS, "form-js-css");
    viewerReady = import("./vendor/form-js/form-viewer.js");
  }
  return viewerReady;
}

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// A blank form-js schema; the editor fills in schemaVersion and its own ids.
const blankSchema = () => ({ type: "default", components: [] });

// newFormId mints a stable, filename-safe id for a new form, echoing the
// "form-xxxx" ids the reference modeler generates.
function newFormId() {
  return "form-" + Date.now().toString(36) + Math.random().toString(36).slice(2, 6);
}

let current; // active session handle, torn down on remount/leave
// generation is bumped by cleanup() on every remount/navigation. mountFormEditor
// captures it after cleanup() and re-checks after each await, so a mount a newer
// navigation has superseded bails before it instantiates the form-js Playground
// (and its timers) into a detached container and leaks them — the same guard
// editor.js uses for the BPMN mounts.
let generation = 0;

export function cleanup() {
  generation++;
  if (current) { try { current.destroy(); } catch { /* ignore */ } current = null; }
}

// mountFormEditor renders the form editor into root. With formId it edits an
// existing form; without it creates a new one (optionally seeded into projectId).
export async function mountFormEditor(root, { api, toast, formId, projectId }) {
  cleanup();
  const gen = generation; // this mount's token; bail if a newer navigation supersedes it
  // Claim the shared cleanup slot so navigating away tears this editor down
  // (the BPMN editor reclaims it the same way when it mounts).
  window.__atlasCleanup = cleanup;

  const isNew = formId == null;
  root.innerHTML = `
    <div class="editor form-editor">
      <div class="editor-bar">
        <a class="crumbs" id="form-back" href="#/modeler">&larr; Forms</a>
        <div class="etabs" id="form-tabs">
          <button type="button" data-ftab="design" class="active">Design</button>
          <button type="button" data-ftab="validate">Validate</button>
          <button type="button" data-ftab="editor">Editor</button>
        </div>
        <input id="form-name" class="form-name-input" placeholder="Form name" spellcheck="false" />
        <span class="chip" id="form-id-chip"></span>
        <div style="flex:1"></div>
        <span class="muted" id="form-status"></span>
        <button class="btn" id="form-save">Save</button>
      </div>
      <div class="editor-body fv-body">
        <div class="fv-pane active" id="pane-design">
          <div id="form-playground" class="form-playground"><p class="muted" style="padding:20px">Loading form editor&hellip;</p></div>
        </div>
        <div class="fv-pane" id="pane-validate"></div>
        <div class="fv-pane" id="pane-editor"></div>
      </div>
    </div>`;

  const nameInput = root.querySelector("#form-name");
  const idChip = root.querySelector("#form-id-chip");
  const statusEl = root.querySelector("#form-status");
  const container = root.querySelector("#form-playground");

  // Resolve the form's identity and initial schema.
  let id = formId;
  let name = "";
  let schema = blankSchema();
  let project = projectId || "";
  if (!isNew) {
    try {
      const def = await api("GET", "/api/v1/forms/" + encodeURIComponent(formId));
      id = def.id;
      name = def.name || def.id;
      project = def.projectId || "";
      if (def.schema && typeof def.schema === "object") schema = def.schema;
    } catch (e) {
      if (gen !== generation) return;
      container.innerHTML = `<p class="muted err" style="padding:20px">Failed to load form: ${esc(e.message)}</p>`;
      return;
    }
    if (gen !== generation) return; // superseded while the form definition loaded
  } else {
    id = newFormId();
  }
  nameInput.value = name;
  idChip.textContent = id;

  // Point the back link at the owning project when the form belongs to one, so a form
  // opened from inside a project returns there rather than dumping the operator on the
  // Modeler home. The edit route (#/modeler/form/e/{id}) doesn't name the project, so
  // this resolves from the loaded form's projectId (or, for a new form, the seeded one).
  (async () => {
    const backEl = root.querySelector("#form-back");
    if (!backEl) return;
    if (!project) return; // ungrouped/new: keep "← Forms" → Modeler home
    backEl.href = `#/modeler/p/${encodeURIComponent(project)}`;
    backEl.innerHTML = "&larr; Project"; // generic label until the name resolves
    try {
      const projects = await api("GET", "/api/v1/projects");
      const p = projects.find((x) => x.id === project);
      if (p && root.querySelector("#form-back") === backEl) backEl.innerHTML = `&larr; ${esc(p.name)}`;
    } catch { /* best-effort: the generic "Project" label still links correctly */ }
  })();

  // ---- Shared schema state -------------------------------------------------
  // `schema` is the single source of truth. `rev` bumps whenever it changes so
  // each pane can tell if it holds a stale copy and needs reloading on entry.
  // `schemaSource` records which tab authored the current value, so that tab
  // isn't forced to reload a schema it already reflects.
  let rev = 0;
  let schemaSource = null;

  // The Playground is the Design pane; build it now (it is the default tab).
  let Playground;
  try {
    ({ Playground } = await loadPlayground());
  } catch (e) {
    if (gen !== generation) return;
    container.innerHTML = `<p class="muted err" style="padding:20px">Failed to load the form editor: ${esc(e.message)}</p>`;
    return;
  }
  // Superseded while the form-js bundle loaded: don't build the Playground (and its
  // timers) into a container the newer view has already replaced.
  if (gen !== generation) return;
  container.innerHTML = "";
  let playground;
  try {
    playground = new Playground({ container, schema, data: {} });
  } catch (e) {
    container.innerHTML = `<p class="muted err" style="padding:20px">Could not open this form: ${esc(e.message)}</p>`;
    return;
  }
  // Give the Playground's own areas the same collapse/resize affordances as the
  // Validate pane. Deferred a tick so its DOM has finished rendering.
  setTimeout(() => enhanceDesignLayout(container), 0);

  // Per-pane bookkeeping: `rev` is the schema revision each pane last loaded
  // (-1 = never), `built` guards one-time DOM setup.
  const panes = {
    design: { el: root.querySelector("#pane-design"), rev: 0, built: true, pg: playground },
    validate: { el: root.querySelector("#pane-validate"), rev: -1, built: false, viewer: null, input: null },
    editor: { el: root.querySelector("#pane-editor"), rev: -1, built: false, editor: null },
  };
  let activeTab = "design";

  const pretty = (s) => JSON.stringify(s, null, 2);

  // commit pulls the outgoing pane's edits back into the shared schema, bumping
  // `rev` only when the value actually changed (so an unchanged round-trip does
  // not force sibling panes to discard their view state).
  function commit(tab) {
    if (tab === "design" && panes.design.pg) {
      try {
        const next = panes.design.pg.getSchema();
        if (next && JSON.stringify(next) !== JSON.stringify(schema)) { schema = next; rev++; schemaSource = "design"; }
      } catch { /* keep last-known schema */ }
    } else if (tab === "editor" && panes.editor.editor) {
      const text = panes.editor.editor.getValue();
      let parsed;
      try { parsed = JSON.parse(text); }
      catch (e) {
        // Keep the invalid text on screen and the last valid schema intact.
        toast("Editor JSON is invalid — keeping the last valid schema (" + e.message + ")", "err");
        return;
      }
      if (JSON.stringify(parsed) !== JSON.stringify(schema)) { schema = parsed; rev++; schemaSource = "editor"; }
    }
    // Validate never edits the schema, only sample data, so it commits nothing.
  }

  // load (re)seeds the incoming pane from the shared schema if its copy is stale.
  async function load(tab) {
    const p = panes[tab];
    const stale = p.rev !== rev && schemaSource !== tab; // authored it → already shown
    if (tab === "design") {
      if (stale && p.pg) {
        try { await p.pg.setSchema(schema); } catch { /* best-effort */ }
      }
      // Re-assert the layout affordances in case setSchema rebuilt any chrome.
      enhanceDesignLayout(container);
      p.rev = rev;
    } else if (tab === "validate") {
      if (!p.built) buildValidate();
      if (stale || p.rev < 0) await reimportViewer();
      p.rev = rev;
    } else if (tab === "editor") {
      if (!p.built) buildEditor();
      if (stale) p.editor.setValue(pretty(schema));
      p.rev = rev;
    }
  }

  async function setTab(tab) {
    if (tab === activeTab) return;
    commit(activeTab);
    activeTab = tab;
    root.querySelectorAll("#form-tabs button").forEach((b) =>
      b.classList.toggle("active", b.dataset.ftab === tab));
    Object.keys(panes).forEach((k) =>
      panes[k].el.classList.toggle("active", k === tab));
    await load(tab);
  }

  root.querySelectorAll("#form-tabs button").forEach((b) =>
    b.addEventListener("click", () => setTab(b.dataset.ftab)));

  // ---- Validate pane -------------------------------------------------------
  // A vertical stack of three collapsible, resizable panels: the live Preview
  // (form-js viewer), an editable Input (sample data), and a read-only Output
  // (live data + validation errors). Preview absorbs spare height; Input and
  // Output keep resizable pixel heights.
  function buildValidate() {
    const p = panes.validate;
    p.built = true;
    p.el.innerHTML = `
      <div class="fv-validate">
        <section class="fv-panel fv-preview" data-panel="preview">
          <header class="fv-head">
            <button type="button" class="fv-caret" aria-label="Collapse">&#9662;</button>
            <span class="fv-title">Form Preview</span>
            <span style="flex:1"></span>
            <button type="button" class="btn neutral fv-act" id="fv-validate-btn" title="Run validation">Validate</button>
          </header>
          <div class="fv-panel-body"><div id="fv-form" class="fv-form"></div></div>
        </section>
        <div class="fv-hresizer" data-resize="input" title="Drag to resize"></div>
        <section class="fv-panel fv-input" data-panel="input">
          <header class="fv-head">
            <button type="button" class="fv-caret" aria-label="Collapse">&#9662;</button>
            <span class="fv-title">Form Input</span>
            <span class="fv-hint muted">sample data seeded into the form</span>
          </header>
          <div class="fv-panel-body"><textarea id="fv-input" class="fv-input-ta" spellcheck="false">{}</textarea></div>
        </section>
        <div class="fv-hresizer" data-resize="output" title="Drag to resize"></div>
        <section class="fv-panel fv-output" data-panel="output">
          <header class="fv-head">
            <button type="button" class="fv-caret" aria-label="Collapse">&#9662;</button>
            <span class="fv-title">Form Output</span>
            <span class="fv-hint muted" id="fv-out-status"></span>
          </header>
          <div class="fv-panel-body"><pre id="fv-output" class="fv-output-pre"></pre></div>
        </section>
      </div>`;

    // Collapse toggles — clicking a header (but not an action button) collapses
    // its panel to just the header bar.
    p.el.querySelectorAll(".fv-panel .fv-head").forEach((head) => {
      head.addEventListener("click", (e) => {
        if (e.target.closest(".fv-act")) return;
        head.closest(".fv-panel").classList.toggle("collapsed");
      });
    });

    // Resizers between panels adjust the height of the panel directly below.
    p.el.querySelectorAll(".fv-hresizer").forEach((r) => {
      const below = r.nextElementSibling; // the Input or Output panel
      wireVResize(r, below, "atlas.form.validate." + r.dataset.resize);
    });

    // Editable sample-data input; re-seed the preview when it changes (debounced).
    const inputTa = p.el.querySelector("#fv-input");
    p.input = attachJSONEditor(inputTa, { rows: 6, onChange: scheduleReseed });

    p.el.querySelector("#fv-validate-btn").addEventListener("click", runValidation);
  }

  let reseedTimer = null;
  function scheduleReseed() {
    clearTimeout(reseedTimer);
    reseedTimer = setTimeout(reimportViewer, 350);
  }

  function currentInputData() {
    const p = panes.validate;
    if (!p.input) return {};
    const raw = (p.input.getValue() || "").trim();
    if (!raw) return {};
    try { const v = JSON.parse(raw); return v && typeof v === "object" ? v : {}; }
    catch { return {}; }
  }

  // reimportViewer (re)renders the runtime form for the current schema, seeded
  // with the sample Input data, and refreshes the Output.
  async function reimportViewer() {
    const p = panes.validate;
    const host = p.el.querySelector("#fv-form");
    if (!host) return;
    let Form;
    try { ({ Form } = await loadFormViewer()); }
    catch (e) { host.innerHTML = `<p class="muted err">Failed to load the form viewer: ${esc(e.message)}</p>`; return; }
    try {
      if (!p.viewer) {
        p.viewer = new Form({ container: host });
        p.viewer.on("changed", (ev) => renderOutput(ev.data, ev.errors));
      }
      await p.viewer.importSchema(schema, currentInputData());
      renderOutput(currentInputData(), null);
    } catch (e) {
      host.innerHTML = `<p class="muted err">Could not render this form: ${esc(e.message)}</p>`;
    }
  }

  // runValidation forces a full submit so every field's errors surface at once.
  function runValidation() {
    const p = panes.validate;
    if (!p.viewer) return;
    let res;
    try { res = p.viewer.submit(); }
    catch (e) { toast("Validation failed: " + e.message, "err"); return; }
    renderOutput(res.data, res.errors);
    const n = res.errors ? Object.keys(res.errors).length : 0;
    toast(n === 0 ? "Form is valid" : `${n} field${n === 1 ? "" : "s"} with errors`, n === 0 ? "" : "err");
  }

  function renderOutput(data, errors) {
    const p = panes.validate;
    const out = p.el.querySelector("#fv-output");
    const status = p.el.querySelector("#fv-out-status");
    if (!out) return;
    const hasErrors = errors && Object.keys(errors).length > 0;
    let html = `<span class="fv-out-label muted">data</span>\n${esc(JSON.stringify(data ?? {}, null, 2))}`;
    if (hasErrors) {
      const items = Object.entries(errors)
        .map(([k, v]) => `  ${esc(k)}: ${esc(Array.isArray(v) ? v.join(", ") : String(v))}`)
        .join("\n");
      html += `\n\n<span class="fv-out-label err">errors</span>\n${items}`;
    }
    out.innerHTML = html;
    if (status) {
      status.textContent = hasErrors ? Object.keys(errors).length + " error(s)" : "valid";
      status.className = "fv-hint " + (hasErrors ? "err" : "ok");
    }
  }

  // ---- Editor pane ---------------------------------------------------------
  function buildEditor() {
    const p = panes.editor;
    p.built = true;
    p.el.innerHTML = `
      <div class="fv-editor">
        <header class="fv-head">
          <span class="fv-title">Form Schema</span>
          <span class="chip">JSON</span>
          <span class="fv-hint muted">edit the raw form-js schema; switch tabs to apply</span>
        </header>
        <div class="fv-editor-body"><textarea id="fv-schema" class="fv-schema-ta" spellcheck="false"></textarea></div>
      </div>`;
    const ta = p.el.querySelector("#fv-schema");
    ta.value = pretty(schema);
    p.editor = attachJSONEditor(ta, {});
  }

  // ---- Resizer helper ------------------------------------------------------
  // wireVResize makes `panel` (which sits below the divider) height-draggable,
  // remembering the chosen height across mounts. Dragging the divider upward
  // enlarges the panel beneath it, borrowing height from the flexible Preview.
  function wireVResize(divider, panel, storeKey) {
    if (!divider || !panel) return;
    const clamp = (h) => Math.max(60, Math.min(700, h));
    const saved = parseInt(localStorage.getItem(storeKey) || "", 10);
    if (saved) panel.style.height = clamp(saved) + "px";

    let startY = 0, startH = 0;
    const onMove = (e) => { panel.style.height = clamp(startH - (e.clientY - startY)) + "px"; };
    const onUp = () => {
      document.removeEventListener("pointermove", onMove);
      document.removeEventListener("pointerup", onUp);
      divider.classList.remove("dragging");
      document.body.style.userSelect = "";
      localStorage.setItem(storeKey, String(parseInt(panel.style.height, 10) || 160));
    };
    divider.addEventListener("pointerdown", (e) => {
      // A collapsed panel has no resizable body; ignore drags on its divider.
      if (panel.classList.contains("collapsed")) return;
      e.preventDefault();
      startY = e.clientY;
      startH = panel.getBoundingClientRect().height;
      divider.classList.add("dragging");
      document.body.style.userSelect = "none";
      document.addEventListener("pointermove", onMove);
      document.addEventListener("pointerup", onUp);
    });
    divider.addEventListener("dblclick", () => { panel.style.height = ""; localStorage.removeItem(storeKey); });
  }

  // ---- Save ----------------------------------------------------------------
  async function save() {
    commit(activeTab); // fold the active tab's edits into `schema` first
    const btn = root.querySelector("#form-save");
    btn.disabled = true;
    statusEl.textContent = "Saving…";
    try {
      const body = {
        id,
        name: nameInput.value.trim() || id,
        schema,
        projectId: project,
      };
      await api("POST", "/api/v1/forms", body);
      statusEl.textContent = "Saved";
      toast("Form saved");
      // A freshly created form becomes editable in place (so a second Save
      // overwrites rather than creating a duplicate) without a reload.
      if (isNew && location.hash.startsWith("#/modeler/form/new")) {
        history.replaceState(null, "", "#/modeler/form/e/" + encodeURIComponent(id));
      }
    } catch (e) {
      statusEl.textContent = "";
      toast("Save failed: " + e.message, "err");
    } finally {
      btn.disabled = false;
    }
  }
  root.querySelector("#form-save").addEventListener("click", save);
  nameInput.addEventListener("input", () => { statusEl.textContent = ""; });

  // Session handle: tearing down destroys every surface that was built.
  current = {
    destroy() {
      clearTimeout(reseedTimer);
      if (panes.design.pg) { try { panes.design.pg.destroy(); } catch { /* ignore */ } }
      if (panes.validate.viewer) { try { panes.validate.viewer.destroy(); } catch { /* ignore */ } }
      if (panes.validate.input) { try { panes.validate.input.destroy(); } catch { /* ignore */ } }
      if (panes.editor.editor) { try { panes.editor.editor.destroy(); } catch { /* ignore */ } }
    },
  };
}

// ---- Design pane layout enhancement ---------------------------------------
// The Design tab hosts the form-js Playground, which ships its four areas
// (Form Definition | Form Preview / Form Input | Form Output) as a 2x2 grid
// flanked by a Components palette and a properties panel. enhanceDesignLayout
// re-lays them the way an author reads the tool: the editor is the main field
// with an optional, resizable Form Preview beside it; the Form Input/Output data
// region is decoupled below and toggles as a whole; and the Components and
// Properties columns flank both, each collapsible. Choices persist (localStorage).
//
// It reaches into the Playground's rendered DOM (vendored/pinned, ADR-0013), so
// it is written defensively: every lookup is guarded and it is idempotent, and
// it re-applies cleanly if the Playground ever rebuilds its chrome.
const DKEY = "atlas.form.design.";

function enhanceDesignLayout(container) {
  if (!container) return;
  const main = container.querySelector(".fjs-pgl-main");
  const rootEl = container.querySelector(".fjs-pgl-root");
  if (!main || !rootEl) return; // Playground not rendered yet
  // Idempotent: if this exact element is already wired, nothing to do.
  if (main.__fvWired && main.querySelector(".fv-top")) return;
  main.__fvWired = true;

  const sections = [...main.querySelectorAll(":scope > .fjs-pgl-section")];
  if (sections.length < 4) return; // unexpected layout — leave the Playground as-is
  const [editor, preview, input, output] = sections;

  const num = (k, d) => { const v = parseFloat(localStorage.getItem(DKEY + k)); return Number.isFinite(v) ? v : d; };
  const bool = (k, d) => { const v = localStorage.getItem(DKEY + k); return v == null ? d : v === "1"; };
  const setBool = (k, v) => localStorage.setItem(DKEY + k, v ? "1" : "0");
  const state = {
    edPct: Math.min(85, Math.max(15, num("edPct", 60))),   // editor width within the top region
    topPct: Math.min(85, Math.max(15, num("topPct", 68))), // top region height within main
  };
  let previewOpen = bool("previewOpen", true);
  let bottomOpen = bool("bottomOpen", true);

  // Capture the side columns' natural widths BEFORE reflowing (used by the width
  // resizers below); the palette has no explicit CSS width and would collapse the
  // instant `main` starts flexing.
  const palette = rootEl.querySelector(":scope > .fjs-pgl-palette-container");
  const props = rootEl.querySelector(":scope > .fjs-pgl-properties-container");
  const sane = (el, def) => { const w = el ? Math.round(el.getBoundingClientRect().width) : 0; return w > 50 ? w : def; };
  const paletteW0 = sane(palette, 200);
  const propsW0 = sane(props, 250);

  // Reshape main into a vertical stack of two regions: the editor with an optional
  // preview on top, and the decoupled Form Input/Output data region below (toggled
  // as a whole). form-js ships these four as a 2x2 grid with shared rows; splitting
  // it this way lets the preview and the data region come and go without holes.
  main.style.position = "relative";
  main.style.display = "flex";
  main.style.flexDirection = "column";
  main.style.flex = "1 1 auto";
  main.style.width = "auto";
  main.style.minWidth = "0";

  const topRegion = document.createElement("div");
  topRegion.className = "fv-top";
  const bottomRegion = document.createElement("div");
  bottomRegion.className = "fv-bottom";
  topRegion.append(editor, preview);
  bottomRegion.append(input, output);
  main.append(topRegion, bottomRegion);

  // Dividers: editor|preview (within the top region) and top|bottom (over main).
  const vsplit = document.createElement("div");
  vsplit.className = "fv-grid-cresizer";
  vsplit.title = "Drag to resize the preview";
  topRegion.appendChild(vsplit);
  const hsplit = document.createElement("div");
  hsplit.className = "fv-grid-rresizer";
  hsplit.title = "Drag to resize the data region";
  main.appendChild(hsplit);

  // A header's caret toggles its region; the click ignores the header's own
  // buttons (Download/Embed). The editor has no caret — it is the main field.
  function addCaret(sec, onToggle) {
    const header = sec.querySelector(":scope > .header");
    if (!header || header.querySelector(".fv-sec-caret")) return;
    const caret = document.createElement("span");
    caret.className = "fv-sec-caret";
    caret.textContent = "▾";
    header.insertBefore(caret, header.firstChild);
    header.classList.add("fv-sec-head");
    header.addEventListener("click", (e) => {
      if (e.target !== caret && e.target.closest("button, a, input, select, .fjs-pgl-button, .header-items")) return;
      onToggle();
    });
  }
  // Preview closes from its own caret and reopens from the editor-header button.
  addCaret(preview, () => { previewOpen = false; setBool("previewOpen", previewOpen); recompute(); });
  // Form Input and Form Output are one region: either caret toggles both.
  const toggleBottom = () => { bottomOpen = !bottomOpen; setBool("bottomOpen", bottomOpen); recompute(); };
  addCaret(input, toggleBottom);
  addCaret(output, toggleBottom);

  // Preview is optional; a toggle in the editor's own header brings it back once
  // its caret has closed it (a hidden section can't hold its own reopen control).
  const edHeader = editor.querySelector(":scope > .header");
  if (edHeader && !edHeader.querySelector(".fv-preview-toggle")) {
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "fv-preview-toggle";
    (edHeader.querySelector(".header-items") || edHeader).appendChild(btn);
    btn.addEventListener("click", (e) => {
      e.stopPropagation();
      previewOpen = !previewOpen;
      setBool("previewOpen", previewOpen);
      recompute();
    });
  }

  function recompute() {
    // Bottom (Form Input/Output) toggles as a whole; when off it collapses to its
    // header bar so it can be reopened, rather than vanishing.
    input.classList.toggle("fv-collapsed", !bottomOpen);
    output.classList.toggle("fv-collapsed", !bottomOpen);
    topRegion.style.flex = bottomOpen ? `1 1 ${state.topPct}%` : "1 1 auto";
    bottomRegion.style.flex = bottomOpen ? `1 1 ${100 - state.topPct}%` : "0 0 auto";
    hsplit.style.display = bottomOpen ? "" : "none";
    hsplit.style.top = state.topPct + "%";
    input.style.flex = "1 1 50%";
    output.style.flex = "1 1 50%";
    // Preview is optional; when off it is fully hidden and the editor fills the row.
    preview.style.display = previewOpen ? "" : "none";
    vsplit.style.display = previewOpen ? "" : "none";
    editor.style.flex = previewOpen ? `0 0 ${state.edPct}%` : "1 1 auto";
    preview.style.flex = previewOpen ? "1 1 auto" : "0 0 0";
    vsplit.style.left = state.edPct + "%";
    const btn = edHeader && edHeader.querySelector(".fv-preview-toggle");
    if (btn) { btn.textContent = previewOpen ? "Hide preview" : "Show preview"; btn.classList.toggle("on", previewOpen); }
  }

  dragPct(vsplit, "x", topRegion, (pct) => { state.edPct = pct; recompute(); },
    () => localStorage.setItem(DKEY + "edPct", String(Math.round(state.edPct))));
  dragPct(hsplit, "y", main, (pct) => { state.topPct = pct; recompute(); },
    () => localStorage.setItem(DKEY + "topPct", String(Math.round(state.topPct))));

  // --- Side columns (Components palette left, Properties right): each is
  // width-resizable and collapses to nothing via a chevron on its resizer. The
  // chevron lives on the resizer (not inside the column) so it stays reachable
  // when the column is collapsed away.
  setupSide(palette, "left", "paletteW", paletteW0, "paletteOpen");
  setupSide(props, "right", "propsW", propsW0, "propsOpen");

  function setupSide(col, side, wKey, w0, openKey) {
    if (!col || col.__fvWired) return;
    col.__fvWired = true;
    col.style.flex = "0 0 auto";
    let savedW = num(wKey, w0);
    col.style.width = savedW + "px";
    let open = bool(openKey, true);

    const r = document.createElement("div");
    r.className = "fv-side-resizer";
    if (side === "left") rootEl.insertBefore(r, main); else rootEl.insertBefore(r, col);
    dragWidth(r, col, side, () => {
      if (col.classList.contains("fv-rail")) return; // collapsed — width is pinned
      savedW = parseInt(col.style.width, 10) || w0;
      localStorage.setItem(DKEY + wKey, String(savedW));
    });

    const t = document.createElement("button");
    t.type = "button";
    t.className = "fv-railbtn";
    t.title = "Collapse / expand";
    r.appendChild(t);
    t.addEventListener("pointerdown", (e) => e.stopPropagation()); // click, don't drag
    t.addEventListener("click", (e) => { e.stopPropagation(); open = !open; setBool(openKey, open); apply(); });

    function apply() {
      if (open) {
        col.classList.remove("fv-rail");
        col.style.width = savedW + "px";
      } else {
        if (!col.classList.contains("fv-rail")) savedW = parseInt(col.style.width, 10) || savedW;
        col.classList.add("fv-rail");
        col.style.width = "";
      }
      // Chevron points the way the panel moves: outward to collapse, inward to reopen.
      t.textContent = ((side === "left") === open) ? "‹" : "›";
    }
    apply();
  }

  recompute();
}

// dragPct wires a divider that reports its position within `area` as a clamped
// percentage (axis "x" → horizontal, "y" → vertical) while dragging.
function dragPct(divider, axis, area, onMove, onEnd) {
  const move = (e) => {
    const r = area.getBoundingClientRect();
    const pct = axis === "x"
      ? ((e.clientX - r.left) / r.width) * 100
      : ((e.clientY - r.top) / r.height) * 100;
    onMove(Math.min(85, Math.max(15, pct)));
  };
  const up = () => {
    document.removeEventListener("pointermove", move);
    document.removeEventListener("pointerup", up);
    divider.classList.remove("dragging");
    document.body.style.userSelect = "";
    onEnd();
  };
  divider.addEventListener("pointerdown", (e) => {
    e.preventDefault();
    divider.classList.add("dragging");
    document.body.style.userSelect = "none";
    document.addEventListener("pointermove", move);
    document.addEventListener("pointerup", up);
  });
}

// dragWidth wires a divider that resizes `panel`'s width. side "left" means the
// panel sits left of the divider (drag right widens); "right" is the mirror.
function dragWidth(divider, panel, side, onEnd) {
  const clamp = (w) => Math.max(120, Math.min(560, w));
  let startX = 0, startW = 0;
  const move = (e) => {
    const dx = e.clientX - startX;
    panel.style.width = clamp(side === "left" ? startW + dx : startW - dx) + "px";
  };
  const up = () => {
    document.removeEventListener("pointermove", move);
    document.removeEventListener("pointerup", up);
    divider.classList.remove("dragging");
    document.body.style.userSelect = "";
    onEnd();
  };
  divider.addEventListener("pointerdown", (e) => {
    e.preventDefault();
    startX = e.clientX;
    startW = panel.getBoundingClientRect().width;
    divider.classList.add("dragging");
    document.body.style.userSelect = "none";
    document.addEventListener("pointermove", move);
    document.addEventListener("pointerup", up);
  });
  divider.addEventListener("dblclick", () => { panel.style.width = ""; onEnd(); });
}
