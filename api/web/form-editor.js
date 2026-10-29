// Form editor view. Three tabs over a single shared form schema (ADR-0028):
//
//   • Design   — the vendored form-js Playground: the reference modeler's split
//                surface (Form Definition editor beside a live Form Preview,
//                with Form Input and Form Output below).
//   • Validate — a testing surface. The form is rendered with the runtime
//                viewer, seeded from an editable Form Input (sample data), and
//                the live result / validation errors show in Form Output. Its
//                three areas can each be collapsed and resized independently.
//   • Source   — the raw schema as text in a JSON code editor: a form's source is
//                its form-js schema, as a diagram's is its XML. (The tab is keyed
//                "editor" in the markup and the code; only its label says Source, so
//                it reads as the BPMN and DMN editors' "XML source" does.)
//
// The three surfaces stay in sync through one canonical `schema` value: leaving
// a tab commits its edits into it, entering a tab (re)loads it. Assets load
// lazily so non-editor pages stay light, mirroring the BPMN editor (editor.js).

import { attachJSONEditor } from "./json-editor.js";
import { t as tr, plural as trPlural } from "./i18n.js";
import { installDevShortcut } from "./dev-view.js";
import { makeIdCheck } from "./idcheck.js";
import { generationWorkers, openFormGenerator } from "./formgen-dialog.js";
import { decisionOptions, ensureFormStyles } from "./formviewer.js";
import { fromTail, noteFormSaved } from "./form-return.js";
import { attachRemoteSelects, previewTransport, remoteAdapter } from "./remote-select.js";

const FORM_CSS = "vendor/form-js/form-playground.css";

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
    // The Playground embeds a live preview of the form, so the preview has to be
    // painted in the org's colours like the runtime is — the editor's own chrome
    // comes from form-playground.css and is unaffected.
    ensureFormStyles();
    playgroundReady = import("./vendor/form-js/form-playground.js");
  }
  return playgroundReady;
}

let viewerReady; // memoized loader promise → { Form }
function loadFormViewer() {
  if (!viewerReady) {
    // Same stylesheet ids the tasks app uses, so the 86 KB viewer CSS loads once.
    ensureFormStyles();
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
//
// generateFor is {processId, elementId} when the editor was reached by pressing
// "＋ New form" on a step in the Modeler. It says what the form is for, so the
// generator opens on it straight away instead of asking the author to say again what
// their click already said (ADR-0260).
//
// from is {draftId, elementId} when the editor was reached through one of the
// Modeler's form doors (ADR-0465): back goes to that
// diagram, and a save completes the trip, so the element is linked to the form it now
// is when the author returns.
export async function mountFormEditor(root, { api, toast, formId, projectId, generateFor, from }) {
  cleanup();
  const gen = generation; // this mount's token; bail if a newer navigation supersedes it
  // Claim the shared cleanup slot so navigating away tears this editor down
  // (the BPMN editor reclaims it the same way when it mounts).
  window.__atlasCleanup = cleanup;

  const isNew = formId == null;
  root.innerHTML = `
    <div class="editor form-editor">
      <div class="editor-bar">
        <a class="crumbs" id="form-back" href="#/modeler">${esc(tr("formEd.back"))}</a>
        <div class="etabs" id="form-tabs">
          <button type="button" data-ftab="design" class="active" title="${esc(tr("formEd.tab.designTitle"))}">${esc(tr("formEd.tab.design"))}</button>
          <button type="button" data-ftab="validate" title="${esc(tr("formEd.tab.validateTitle"))}">${esc(tr("formEd.tab.validate"))}</button>
          <button type="button" data-ftab="editor" title="${esc(tr("formEd.tab.sourceTitle"))}">${esc(tr("formEd.tab.source"))}</button>
        </div>
        <input id="form-name" class="form-name-input" placeholder="${esc(tr("formEd.namePlaceholder"))}" spellcheck="false" />
        <span class="chip" id="form-id-chip"></span>
        <span class="id-warn" id="form-id-warn" hidden></span>
        <div style="flex:1"></div>
        <span class="muted" id="form-status"></span>
        <button class="btn ghost" id="form-generate" hidden
          title="${esc(tr("formEd.generateTitle"))}">${esc(tr("formEd.generate"))}</button>
        <button class="btn" id="form-save" title="${esc(tr("formEd.saveTitle"))}">${esc(tr("formEd.save"))}</button>
      </div>
      <div class="editor-body fv-body">
        <div class="fv-pane active" id="pane-design">
          <div id="form-playground" class="form-playground"><p class="muted" style="padding:20px">${esc(tr("formEd.loading"))}</p></div>
        </div>
        <div class="fv-pane" id="pane-validate"></div>
        <div class="fv-pane" id="pane-editor"></div>
      </div>
    </div>`;

  const nameInput = root.querySelector("#form-name");
  const idChip = root.querySelector("#form-id-chip");
  const idWarn = root.querySelector("#form-id-warn");
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
  // A form has ONE id, and it is the schema's: that is what the Design pane's ID field
  // edits, what a user task's formId binds to, what an exported .form.json carries and
  // what re-importing that file files it under. The store used to key a form
  // independently of its schema, so editing the ID in the panel changed the document
  // and nothing else — the chip above kept showing the id the form was really stored
  // under, and the rename the author thought they had made never happened
  // (ADR-0222). The stored id is the truth on open, so stamp it
  // into the schema; from here the field IS the identity, and saving a changed one
  // renames the form.
  if (!schema || typeof schema !== "object") schema = blankSchema();
  schema.id = id;
  // savedId is what the server holds this form under — "" while it has never been
  // saved. The id in the schema is what the *next* save will file it under; when the
  // two differ, that save is a rename.
  let savedId = isNew ? "" : id;
  nameInput.value = name;

  // Point the back link at the diagram the form was opened from, when it was — that is
  // where the author is going back to (ADR-0465) — and
  // otherwise at the owning project when the form belongs to one, so a form opened
  // from inside a project returns there rather than dumping the operator on the
  // Modeler home. The edit route (#/modeler/form/e/{id}) doesn't name the project, so
  // this resolves from the loaded form's projectId (or, for a new form, the seeded one).
  // Names are resolved after the fact, so the link works at once and gets its label
  // when the answer arrives.
  (async () => {
    const backEl = root.querySelector("#form-back");
    if (!backEl) return;
    if (from && from.draftId) {
      backEl.href = "#/modeler/draft/" + encodeURIComponent(from.draftId);
      backEl.innerHTML = "&larr; Process";
      try {
        const drafts = (await api("GET", "/api/v1/drafts")) || [];
        const d = drafts.find((x) => x.processId === from.draftId);
        if (d && d.name && root.querySelector("#form-back") === backEl) backEl.innerHTML = `&larr; ${esc(d.name)}`;
      } catch { /* best-effort: the generic "Process" label still links correctly */ }
      return;
    }
    if (!project) return; // ungrouped/new: keep "← Forms" → Modeler home
    backEl.href = `#/modeler/p/${encodeURIComponent(project)}`;
    backEl.innerHTML = "&larr; Application"; // generic label until the name resolves
    try {
      const projects = await api("GET", "/api/v1/applications");
      const p = projects.find((x) => x.id === project);
      if (p && root.querySelector("#form-back") === backEl) backEl.innerHTML = `&larr; ${esc(p.name)}`;
    } catch { /* best-effort: the generic "Application" label still links correctly */ }
  })();

  // ---- Shared schema state -------------------------------------------------
  // `schema` is the single source of truth. `rev` bumps whenever it changes so
  // each pane can tell if it holds a stale copy and needs reloading on entry.
  // `schemaSource` records which tab authored the current value, so that tab
  // isn't forced to reload a schema it already reflects.
  let rev = 0;
  let schemaSource = null;

  // What this server offers a select's remote options source (ADR-0473): the sources
  // and the Workers of their types. The properties panel and the palette offer what it
  // lists, so it is asked for beside the bundle rather than after it. Not being allowed
  // to ask, or the server not answering, leaves the editor as form-js ships it.
  const sourcesReady = api("GET", "/api/v1/form-data/sources").catch(() => null);
  // The decisions a field's options can come from (ADR-draft-form-options-from-a-decision),
  // asked for beside it for the same reason. The canvas, both previews and the panel ask
  // each decision's list through one service, which asks afresh after a few seconds: a
  // list is maintained in the decision editor, maybe in the next tab.
  const decisionsReady = api("GET", "/api/v1/form-data/decisions").catch(() => null);
  const decisionService = decisionOptions({ ttlMs: 10000 });

  // The Playground is the Design pane; build it now (it is the default tab).
  let Playground;
  let dataSources = null;
  let decisionList = null;
  try {
    [{ Playground }, dataSources, decisionList] = await Promise.all([loadPlayground(), sourcesReady, decisionsReady]);
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
    // The preview searches made-up people and says so: a design-time preview must not
    // reach a production Worker.
    playground = new Playground({
      container, schema, data: {},
      atlas: {
        sources: dataSources,
        remote: remoteAdapter({ transport: previewTransport(), preview: true }),
        decisions: decisionList,
        decisionOptions: decisionService,
      },
      editorAdditionalModules: [paletteNamesModule],
    });
  } catch (e) {
    container.innerHTML = `<p class="muted err" style="padding:20px">${esc(tr("formEd.openFailed", { error: e.message }))}</p>`;
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

  // ---- Identity ------------------------------------------------------------
  // The form's id lives in the schema — the Design pane edits it under Form ▸ General
  // ▸ ID — and it is the only id the form has: a user task's formId binds to it, the
  // store files the form under it, and an exported .form.json carries it. So the chip
  // in the bar mirrors it rather than showing a second, separate id, and says when the
  // two have not been reconciled yet because the rename is still unsaved.

  // currentId reads the id as it stands right now. The active pane holds edits that
  // have not been committed into `schema` yet, so ask it first.
  function currentId() {
    try {
      if (activeTab === "design" && panes.design.pg) {
        const live = panes.design.pg.getSchema();
        if (live && typeof live.id === "string") return live.id.trim();
      } else if (activeTab === "editor" && panes.editor.editor) {
        const live = JSON.parse(panes.editor.editor.getValue());
        if (live && typeof live.id === "string") return live.id.trim();
      }
    } catch { /* mid-edit or invalid JSON — fall back to the committed schema */ }
    return typeof schema.id === "string" ? schema.id.trim() : "";
  }

  const idCheck = makeIdCheck({
    api, kind: "forms", noun: "form",
    read: currentId,
    own: () => savedId,
    onState: (state, text) => {
      idChip.classList.toggle("id-taken", state === "taken");
      idWarn.textContent = text || "";
      idWarn.hidden = !text;
    },
  });

  // syncIdentity repaints the chip from the id the schema carries and re-checks it.
  // An id that differs from the stored one is a rename waiting on Save, and the chip
  // says so — the old surface silently kept showing the stored id while the panel
  // showed the typed one, which is exactly the confusion this replaces.
  function syncIdentity() {
    const now = currentId();
    idChip.textContent = now || tr("formEd.noId");
    const pending = !!savedId && !!now && now !== savedId;
    idChip.classList.toggle("id-pending", pending);
    idChip.title = !now
      ? tr("formEd.id.none")
      : pending
        ? tr("formEd.id.pending", { from: savedId, to: now })
        : tr("formEd.id.current");
    idCheck.check();
  }
  syncIdentity();

  // ---- Unsaved edits ---------------------------------------------------------
  // Whether the form on screen differs from the one last loaded or saved — what the
  // back link asks before it leaves (see Leaving, below). It is a comparison rather than
  // a flag, so an edit undone is no edit. The baseline is taken from the Playground
  // itself once it is up, because it normalises a schema on import (schemaVersion, the
  // exporter, generated ids): compared with the stored document instead, every form
  // would read as edited the moment it opened.
  let baseline = null;
  let savedName = name;
  // liveSchema is the schema as the active pane holds it, uncommitted edits included.
  // Source that does not parse is an edit too, and reads as one.
  function liveSchema() {
    try {
      if (activeTab === "design" && panes.design.pg) return panes.design.pg.getSchema();
      if (activeTab === "editor" && panes.editor.editor) return JSON.parse(panes.editor.editor.getValue());
    } catch { return undefined; }
    return schema;
  }
  const snapshot = () => JSON.stringify(liveSchema());
  function isDirty() {
    if (nameInput.value.trim() !== savedName.trim()) return true;
    // Before the Playground has reported in there is no baseline: only what has been
    // committed into the shared schema since is known to be an edit.
    if (baseline === null) return rev > 0;
    return snapshot() !== baseline;
  }

  // The Design pane's properties panel is where the id is normally typed, so follow its
  // edits live rather than waiting for a tab change. getEditor() only exists once the
  // Playground has mounted its editor, which it does asynchronously, so this retries
  // briefly and then gives up — the tab-change and save paths still resync.
  let idWatchTimer = null;
  (function followDesignEdits(tries = 0) {
    let ed = null;
    try { ed = panes.design.pg && panes.design.pg.getEditor(); } catch { /* not ready */ }
    if (ed && typeof ed.on === "function") {
      ed.on("changed", syncIdentity);
      if (baseline === null) baseline = snapshot(); // the form as opened, as form-js holds it
      wireLegacyPreview();
      return;
    }
    if (tries < 40) idWatchTimer = setTimeout(() => followDesignEdits(tries + 1), 50);
  })();

  // ---- Directory fields saved as textfields -------------------------------------
  // A directory field is a select whose options source is a remote data source; the
  // bundle's properties panel edits it and its preview searches made-up people (above).
  // A form saved before then holds it as a textfield, which the panel offers to convert.
  // Until it is converted, the Design pane's preview attaches the same made-up search to
  // it, as the Tasks app attaches the live one.
  let designPreview = null;
  function wireLegacyPreview() {
    try {
      const pf = panes.design.pg.getForm();
      const attach = () => {
        if (designPreview) designPreview.destroy();
        // The preview form's own element, not the editor canvas beside it: both sit
        // in a .fjs-pgl-form-container, and the canvas renders fields too.
        const host = pf._container || [...container.querySelectorAll(".fjs-pgl-form-container")].pop() || container;
        designPreview = attachRemoteSelects(pf, host, { transport: previewTransport(), preview: true });
      };
      if (pf && typeof pf.on === "function") { pf.on("import.done", attach); attach(); }
    } catch { /* the playground's preview is a convenience */ }
  }

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
        toast(tr("formEd.source.invalid", { error: e.message }), "err");
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
    syncIdentity(); // the Source tab can retype the id too, not just the Design panel
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
            <button type="button" class="fv-caret" aria-label="${esc(tr("formEd.collapse"))}">&#9662;</button>
            <span class="fv-title">${esc(tr("formEd.preview"))}</span>
            <span style="flex:1"></span>
            <button type="button" class="btn neutral fv-act" id="fv-validate-btn" title="${esc(tr("formEd.validateTitle"))}">${esc(tr("formEd.validate"))}</button>
          </header>
          <div class="fv-panel-body"><div id="fv-form" class="fv-form"></div></div>
        </section>
        <div class="fv-hresizer" data-resize="input" title="${esc(tr("formEd.dragResize"))}"></div>
        <section class="fv-panel fv-input" data-panel="input">
          <header class="fv-head">
            <button type="button" class="fv-caret" aria-label="${esc(tr("formEd.collapse"))}">&#9662;</button>
            <span class="fv-title">${esc(tr("formEd.input"))}</span>
            <span class="fv-hint muted">${esc(tr("formEd.inputHint"))}</span>
          </header>
          <div class="fv-panel-body"><textarea id="fv-input" class="fv-input-ta" spellcheck="false">{}</textarea></div>
        </section>
        <div class="fv-hresizer" data-resize="output" title="${esc(tr("formEd.dragResize"))}"></div>
        <section class="fv-panel fv-output" data-panel="output">
          <header class="fv-head">
            <button type="button" class="fv-caret" aria-label="${esc(tr("formEd.collapse"))}">&#9662;</button>
            <span class="fv-title">${esc(tr("formEd.output"))}</span>
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
    catch (e) { host.innerHTML = `<p class="muted err">${esc(tr("formEd.viewerFailed", { error: e.message }))}</p>`; return; }
    try {
      if (!p.viewer) {
        p.viewer = new Form({
          container: host,
          atlasRemote: remoteAdapter({ transport: previewTransport(), preview: true }),
          atlasDecisions: decisionService,
        });
        p.viewer.on("changed", (ev) => renderOutput(ev.data, ev.errors));
      }
      if (p.lookups) { p.lookups.destroy(); p.lookups = null; }
      await p.viewer.importSchema(schema, currentInputData());
      // A directory field previews with made-up people and says so; nothing is asked
      // of a directory while a form is being designed. A select mounts that search
      // itself (above); one saved as a textfield has it attached.
      p.lookups = attachRemoteSelects(p.viewer, host, { transport: previewTransport(), preview: true, data: currentInputData() });
      renderOutput(currentInputData(), null);
    } catch (e) {
      host.innerHTML = `<p class="muted err">${esc(tr("formEd.renderFailed", { error: e.message }))}</p>`;
    }
  }

  // runValidation forces a full submit so every field's errors surface at once.
  function runValidation() {
    const p = panes.validate;
    if (!p.viewer) return;
    let res;
    try { res = p.viewer.submit(); }
    catch (e) { toast(tr("formEd.validationFailed", { error: e.message }), "err"); return; }
    renderOutput(res.data, res.errors);
    const n = res.errors ? Object.keys(res.errors).length : 0;
    toast(n === 0 ? tr("formEd.valid") : trPlural("formEd.fieldErrors", n), n === 0 ? "" : "err");
  }

  function renderOutput(data, errors) {
    const p = panes.validate;
    const out = p.el.querySelector("#fv-output");
    const status = p.el.querySelector("#fv-out-status");
    if (!out) return;
    const hasErrors = errors && Object.keys(errors).length > 0;
    let html = `<span class="fv-out-label muted">${esc(tr("formEd.out.data"))}</span>\n${esc(JSON.stringify(data ?? {}, null, 2))}`;
    if (hasErrors) {
      const items = Object.entries(errors)
        .map(([k, v]) => `  ${esc(k)}: ${esc(Array.isArray(v) ? v.join(", ") : String(v))}`)
        .join("\n");
      html += `\n\n<span class="fv-out-label err">${esc(tr("formEd.out.errors"))}</span>\n${items}`;
    }
    out.innerHTML = html;
    if (status) {
      status.textContent = hasErrors ? tr("formEd.out.errorCount", { n: Object.keys(errors).length }) : tr("formEd.out.valid");
      status.className = "fv-hint " + (hasErrors ? "err" : "ok");
    }
  }

  // ---- Source pane ---------------------------------------------------------
  function buildEditor() {
    const p = panes.editor;
    p.built = true;
    p.el.innerHTML = `
      <div class="fv-editor">
        <header class="fv-head">
          <span class="fv-title">${esc(tr("formEd.schema"))}</span>
          <span class="chip">JSON</span>
          <span class="fv-hint muted">${esc(tr("formEd.schemaHint"))}</span>
        </header>
        <div class="fv-editor-body"><textarea id="fv-schema" class="fv-schema-ta" spellcheck="false"></textarea></div>
      </div>`;
    const ta = p.el.querySelector("#fv-schema");
    ta.value = pretty(schema);
    p.editor = attachJSONEditor(ta, {});
    // The schema and the sample-input box are JSON: F2 opens either in the Developer
    // View (ADR-0145). The form designer knows no process variables, so the context
    // is the language alone.
    installDevShortcut(document, () => ({}));
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
    const nextId = (typeof schema.id === "string" ? schema.id.trim() : "") || savedId;
    if (!nextId) {
      toast(tr("formEd.needsId"), "err");
      return;
    }
    if (schema.id !== nextId) {
      // The id was cleared, so the stored one stands. Push it back through the panes so
      // the panel shows what is actually being saved rather than a blank field — the
      // document and the store agree by construction, always.
      schema.id = nextId;
      rev++;
      schemaSource = null;
      await load(activeTab);
    }
    // Renaming a form is not relabelling it: a user task binds to the id, so a task
    // still pointing at the old one finds no form. That is worth a question, not a
    // toast after the fact.
    if (savedId && nextId !== savedId && !window.confirm(tr("formEd.confirmRename", { from: savedId, to: nextId }))) {
      return;
    }
    const btn = root.querySelector("#form-save");
    btn.disabled = true;
    statusEl.textContent = tr("formEd.saving");
    try {
      const body = {
        id: nextId,
        name: nameInput.value.trim() || nextId,
        schema,
        projectId: project,
        // What this save is editing. Empty on a form that has never been saved; a
        // different id means rename this record rather than create a second one, and
        // an id another form already holds comes back 409 instead of overwriting it
        // (ADR-0222).
        from: savedId,
      };
      const res = await api("POST", "/api/v1/forms", body);
      statusEl.textContent = tr("formEd.saved");
      toast(res && res.renamedFrom
        ? tr("formEd.renamed", { from: res.renamedFrom, to: nextId })
        : tr("formEd.savedToast"));
      savedId = nextId;
      syncIdentity();
      baseline = snapshot();
      savedName = nameInput.value;
      // A form reached from a step completes its trip: the element it was opened from
      // links this id when the author goes back (ADR-0465).
      noteFormSaved(from, nextId);
      // The URL always addresses the form as it is now stored: a freshly created form
      // becomes editable in place (so a second Save updates rather than duplicating),
      // and a renamed one stops pointing at an id that no longer exists. The way back
      // stays on it.
      const back = from && from.draftId ? fromTail(from.draftId, from.elementId) : "";
      history.replaceState(null, "", "#/modeler/form/e/" + encodeURIComponent(nextId) + back);
    } catch (e) {
      statusEl.textContent = "";
      // A refused collision is about the id, so re-run the check: the chip goes red and
      // names what holds it, instead of leaving only a toast that fades.
      if (e.status === 409) idCheck.now();
      toast(tr("formEd.saveFailed", { error: e.message }), "err");
    } finally {
      btn.disabled = false;
    }
  }
  root.querySelector("#form-save").addEventListener("click", save);
  nameInput.addEventListener("input", () => { statusEl.textContent = ""; });

  // ---- Leaving -------------------------------------------------------------
  // The back link is a door, and leaving through it must not cost the form's edits —
  // the question the Modeler asks before it leaves a diagram (makeKeepEdits). It
  // matters more since a form can be reached from a step and the way back is one click
  // (ADR-0465). A modified click opens the link elsewhere
  // and leaves this editor as it is, so it is not asked about.
  root.querySelector("#form-back").addEventListener("click", (ev) => {
    if (ev.button !== 0 || ev.ctrlKey || ev.metaKey || ev.shiftKey || ev.altKey) return;
    if (!isDirty()) return;
    if (!window.confirm(tr("formEd.confirmLeave"))) ev.preventDefault();
  });

  // ---- Generate ------------------------------------------------------------
  // Writing the form from a description and from the process it belongs to
  // (ADR-0260). The button appears only where an operator has
  // configured an AI Worker: an affordance whose only possible outcome is "not
  // configured" teaches an author that the feature does not work.
  //
  // What comes back is applied to the shared schema like any other edit — unsaved,
  // under the id this editor is already holding — so the author reads it on the
  // canvas and presses Save themselves, through the same path a hand-drawn form
  // takes. That is the whole point: the model produces a draft, not a deployment.
  const genBtn = root.querySelector("#form-generate");
  async function generate() {
    commit(activeTab); // generate over what is on screen, not over the last commit
    // The form is passed in so a second generation refines it rather than replacing
    // it, the id so the result comes back under the form's own, and generateFor so a
    // form reached from a step opens on that step — this time and every time, because
    // what this form is for does not change while it is open.
    const got = await openFormGenerator({
      api, workers: available, formId: currentId() || id, schema,
      forProcess: (generateFor && generateFor.processId) || "",
      forStep: (generateFor && generateFor.elementId) || "",
    });
    if (!got || !got.schema || gen !== generation) return;
    schema = got.schema;
    schema.id = currentId() || id; // the identity is the editor's, never the model's
    rev++;
    schemaSource = null; // every pane reloads: none of them authored this
    await load(activeTab);
    syncIdentity();
    toast(got.model ? tr("formEd.generatedModel", { worker: got.worker, model: got.model }) : tr("formEd.generated", { worker: got.worker }));
  }
  let available = [];
  (async () => {
    available = await generationWorkers(api);
    if (gen !== generation || !available.length) return;
    genBtn.hidden = false;
    genBtn.addEventListener("click", generate);
    // Reached by pressing "Create a new form" on a step: that click already said what
    // the form is for, so the dialog opens on it rather than waiting behind a second
    // button the author has no reason to look for. With no AI Worker configured this
    // is never reached and the editor simply opens blank, which is what the link did
    // before there was a generator at all.
    if (generateFor) await generate();
  })();

  // Session handle: tearing down destroys every surface that was built.
  current = {
    destroy() {
      clearTimeout(reseedTimer);
      clearTimeout(idWatchTimer);
      idCheck.dispose();
      if (designPreview) { try { designPreview.destroy(); } catch { /* ignore */ } }
      if (panes.validate.lookups) { try { panes.validate.lookups.destroy(); } catch { /* ignore */ } }
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

// SECTION_TITLES are the catalogue's words for the Playground's four sections, in
// the order it draws them. form-js writes each title as the first text of the
// section's header and never touches that text again, so it is renamed in place —
// no file of form-js is changed.
const SECTION_TITLES = ["formEd.section.definition", "formEd.section.preview", "formEd.section.input", "formEd.section.output"];

function nameSections(sections) {
  sections.forEach((section, i) => {
    const header = section.querySelector(":scope > .header");
    const title = header && [...header.childNodes].find((n) => n.nodeType === 3 && n.data.trim());
    if (title && SECTION_TITLES[i]) title.data = tr(SECTION_TITLES[i]);
  });
}

// paletteNamesModule renames form-js's field types in the catalogue's words before
// the palette first draws them: the palette reads each type's config.name once, when
// it mounts, so an editor module's init is the moment. A type the catalogue does not
// name keeps form-js's own name. The palette's group headings are a constant inside
// form-js, not a service, and stay English.
function PaletteNames(formFields) {
  for (const [type, field] of Object.entries(formFields._formFields || {})) {
    const key = "formjs.field." + type;
    const name = tr(key);
    if (name !== key && field.config) field.config.name = name;
  }
}
PaletteNames.$inject = ["formFields"];
const paletteNamesModule = { __init__: ["atlasPaletteNames"], atlasPaletteNames: ["type", PaletteNames] };

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
  nameSections(sections);

  const num = (k, d) => { const v = parseFloat(localStorage.getItem(DKEY + k)); return Number.isFinite(v) ? v : d; };
  const bool = (k, d) => { const v = localStorage.getItem(DKEY + k); return v == null ? d : v === "1"; };
  const setBool = (k, v) => localStorage.setItem(DKEY + k, v ? "1" : "0");
  const state = {
    edPct: Math.min(85, Math.max(15, num("edPct", 60))),   // editor width within the top region
    topPct: Math.min(85, Math.max(15, num("topPct", 68))), // top region height within main
  };
  let previewOpen = bool("previewOpen", true);
  let bottomOpen = bool("bottomOpen", true);

  // The side columns' default widths (used by the width resizers below). The
  // properties column's natural width is captured BEFORE reflowing. The palette's
  // cannot be: form-js renders the palette into its column only after this runs, so
  // the column measures empty — and once rendered, app.css lets the palette fill
  // whatever width its column is given. Its default is form-js's own --palette-width.
  const palette = rootEl.querySelector(":scope > .fjs-pgl-palette-container");
  const props = rootEl.querySelector(":scope > .fjs-pgl-properties-container");
  const sane = (el, def) => { const w = el ? Math.round(el.getBoundingClientRect().width) : 0; return w > 50 ? w : def; };
  const paletteW0 = 270;
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
  vsplit.title = tr("formEd.resizePreview");
  topRegion.appendChild(vsplit);
  const hsplit = document.createElement("div");
  hsplit.className = "fv-grid-rresizer";
  hsplit.title = tr("formEd.resizeData");
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
    if (btn) { btn.textContent = tr(previewOpen ? "formEd.hidePreview" : "formEd.showPreview"); btn.classList.toggle("on", previewOpen); }
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
    dragWidth(r, col, side, w0, () => {
      if (col.classList.contains("fv-rail")) return; // collapsed — width is pinned
      savedW = parseInt(col.style.width, 10) || w0;
      localStorage.setItem(DKEY + wKey, String(savedW));
    });

    const t = document.createElement("button");
    t.type = "button";
    t.className = "fv-railbtn";
    t.title = tr("formEd.toggle");
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
// A double-click puts the panel back to its default width w0. Clearing the width
// instead would leave the column sized by its content, and the panels inside
// both columns now take their width from the column (app.css).
function dragWidth(divider, panel, side, w0, onEnd) {
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
  divider.addEventListener("dblclick", () => { panel.style.width = w0 + "px"; onEnd(); });
}
