// The UML class canvas: the authoring surface for a process information model
// (ADR-0230, slice 2).
//
// Two decisions shape everything here.
//
// **The subset is served, not carried.** Which class kinds exist, which
// relationships may run between which of them, which multiplicities and primitive
// types there are — all of it arrives from /api/v1/infomodel/subset. This file
// keeps no copy. Two copies of a rule matrix is how you get a canvas that lets
// somebody draw an arrow the server then rejects, and the refusal message the
// server would have given is the one thing that teaches the notation.
//
// **The document is saved whole.** A canvas edits a graph — moving a box, retyping
// an attribute, redrawing a line — and a patch language for that would be a second
// way of saying everything the document already says. So the editor holds the model,
// and Save sends it back against the revision it read.

import { groupifyPanel, groupController } from "./pgroup.js";
import { attachDiagramZoom, canvasController } from "./diagram-zoom.js";

const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g,
  (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// How wide a class is drawn. It is the one piece of geometry this file still needs,
// to place a new box where there is room; the rest lives with the drawing, in
// api/web/vendor/uml/src/index.js.
const BOX_W = 200;

// The canvas bundle is loaded on demand, the way the Modeler loads bpmn-js: it is
// a hundred kilobytes that only this view needs, and the model list beside it should
// not pay for them.
let canvasReady;
function loadCanvas() {
  if (canvasReady) return canvasReady;
  canvasReady = new Promise((resolve, reject) => {
    const href = "vendor/uml/diagram-js.css";
    if (!document.querySelector(`link[href="${href}"]`)) {
      const link = document.createElement("link");
      link.rel = "stylesheet";
      link.href = href;
      document.head.appendChild(link);
    }
    if (window.AtlasUml) { resolve(window.AtlasUml); return; }
    const script = document.createElement("script");
    script.src = "vendor/uml/uml-canvas.js";
    script.onload = () => resolve(window.AtlasUml);
    script.onerror = () => reject(new Error("failed to load the class canvas assets"));
    document.head.appendChild(script);
  });
  return canvasReady;
}

export async function mountClassDiagram(root, { api, toast, id }) {
  root.innerHTML = `<div class="card"><p class="muted">Loading class diagram…</p></div>`;

  let doc, subset, AtlasUml;
  try {
    [doc, subset, AtlasUml] = await Promise.all([
      api("GET", `/api/v1/infomodel/models/${encodeURIComponent(id)}`),
      api("GET", "/api/v1/infomodel/subset"),
      loadCanvas(),
    ]);
  } catch (e) {
    root.innerHTML = `<div class="card empty"><h1>Could not open this model</h1>
      <p class="muted">${esc(e.message)}</p><a class="btn ghost" href="#/data">← Information model</a></div>`;
    return;
  }

  doc.stores = doc.stores || [];
  const state = {
    model: doc,
    validation: doc.validation || { valid: true, findings: [] },
    selected: null,      // {kind: "class"|"association", id}
    connecting: null,    // {kind, fromId} while a relationship is being drawn
    dirty: false,
    schemaFor: "",       // class whose JSON Schema projection is open
  };

  const stereotypeOf = (name) => subset.stereotypes.find((s) => s.stereotype === name) || subset.stereotypes[0];
  const storeModeOf = (m) => (subset.storeModes || []).find((x) => x.mode === m) || (subset.storeModes || [])[0] || {};
  const kindOf = (name) => subset.associationKinds.find((k) => k.kind === name);
  const classById = (cid) => state.model.classes.find((c) => c.id === cid);
  const allowed = (from, to) => subset.matrix[`${from}>${to}`] || [];

  root.innerHTML = `
    <div class="im-editor" id="im-editor">
      <div class="im-bar">
        <a class="btn neutral" href="#/data" title="Back to the information models">← Model</a>
        <b class="im-title" id="im-name">${esc(state.model.name)}</b>
        <span class="im-rev muted" id="im-rev">r${state.model.revision}</span>
        <span class="im-palette" id="im-palette"></span>
        <span style="flex:1"></span>
        <span class="im-dirty" id="im-dirty" hidden>unsaved</span>
        <button class="btn" id="im-save" disabled title="Save the diagram (Ctrl/⌘ + S)">Save</button>
      </div>
      <div class="im-body">
        <div class="im-canvas" id="im-canvas">
          <p class="im-empty-hint" id="im-empty" hidden>No classes yet. Add a business object — an Order, a
            Customer, a Claim — and give it a business key.</p>
        </div>
        <div class="im-side" id="im-side"></div>
      </div>
      <div class="im-problems" id="im-problems"></div>
    </div>`;

  const canvasEl = root.querySelector("#im-canvas");
  const emptyEl = root.querySelector("#im-empty");
  const sideEl = root.querySelector("#im-side");
  const problemsEl = root.querySelector("#im-problems");
  const saveBtn = root.querySelector("#im-save");
  const dirtyEl = root.querySelector("#im-dirty");

  // ---- palette -------------------------------------------------------------
  // Each kind carries the sentence that tells a modeler which one to pick. The
  // difference between a business object and a value type is the single most
  // consequential choice in this metamodel, and a palette of three bare words is
  // how it gets made by accident.
  root.querySelector("#im-palette").innerHTML = subset.stereotypes.map((s) =>
    `<button type="button" class="im-add" data-stereotype="${esc(s.stereotype)}"
       title="${esc(s.meaning)}">+ ${esc(s.label)}</button>`).join("") +
    `<button type="button" class="im-add store" data-add="store"
       title="Where instances of a class outlive the process that made them. Declared once here and named by every process that reaches it — which is the thing BPMN's dataStoreReference gestures at and then says nothing about.">+ Data store</button>` +
    subset.associationKinds.map((k) =>
      `<button type="button" class="im-connect" data-kind="${esc(k.kind)}"
         title="${esc(k.rule)}">${esc(k.label)}</button>`).join("");

  // The canvas. Everything a modeler expects of one — zoom, pan, marquee, multi-select
  // move, undo of a move, keyboard nudging — comes from diagram-js; what Atlas owns is
  // how a class is drawn and what the served subset permits between two of them
  // (ADR-0237).
  const canvas = new AtlasUml.ClassCanvas(canvasEl, {
    subset,
    onSelection: (bo) => onCanvasSelection(bo),
    onChange: () => absorbMoves(),
  });
  // diagram-js has zoomed on ctrl+wheel since this canvas was drawn on it (ADR-0237);
  // what it never had was anything on screen saying so. The control is the shared one,
  // so this canvas zooms like every other diagram in Atlas
  // (ADR-draft-shared-ui-primitives).
  const canvasZoom = attachDiagramZoom(canvasEl, {
    label: "Class diagram",
    controller: canvasController(() => canvas.canvas),
  });
  if (canvasZoom) canvas.diagram.get("eventBus").on("canvas.viewbox.changed", canvasZoom.sync);

  // syncCanvas brings the drawing up to date with the model. It is a reconciliation
  // rather than a redraw because the panel edits on every keystroke: a redraw would
  // take the viewport, the selection and the undo stack with it each time, so typing
  // a class name would zoom back to fit and deselect the class being renamed.
  function syncCanvas() {
    // Deaf to the canvas for the whole sync, not just the last line of it. Selecting
    // through the canvas is what tells the panel, so a selection the panel already
    // knows about is applied without being told back — and reconciling relationships
    // means removing them, which the canvas reports as "nothing is selected now". Were
    // that heard, typing in a relationship's name would deselect it on the first key.
    applyingSelection = true;
    try {
      canvas.sync(state.model, state.validation.findings || [], { unreachable: unreachableNow() });
      // An empty canvas says what to do with it. It is HTML over the drawing rather
      // than text in it: diagram-js fits the viewport to the content, so a sentence
      // drawn on the sheet would be zoomed to fill it.
      emptyEl.hidden = (state.model.classes || []).length > 0;
      canvas.select(state.selected ? state.selected.id : null);
    } finally {
      applyingSelection = false;
    }
  }

  // While a relationship is being drawn, everything it could not land on fades. The
  // rule is the served matrix, read here rather than in the drawing — the canvas is
  // told what to show, and there stays one copy of the matrix, on the server.
  function unreachableNow() {
    if (!state.connecting) return [];
    // A store is never an end of a relationship, so it is out for the whole gesture.
    const out = (state.model.stores || []).map((st) => st.id);
    if (!state.connecting.fromId) return out;
    const from = classById(state.connecting.fromId) || {};
    for (const c of state.model.classes) {
      if (!allowed(from.stereotype, c.stereotype).includes(state.connecting.kind)) out.push(c.id);
    }
    return out;
  }

  function markDirty() {
    state.dirty = true;
    dirtyEl.hidden = false;
    saveBtn.disabled = false;
  }

  // ---- rendering -----------------------------------------------------------
  function render() {
    syncCanvas();
    renderSide();
    renderProblems();
    root.querySelectorAll(".im-connect").forEach((b) =>
      b.classList.toggle("active", !!state.connecting && state.connecting.kind === b.dataset.kind));
    canvasEl.classList.toggle("connecting", !!state.connecting);
  }

  // ---- problems ------------------------------------------------------------
  // A finding says which of two things it is, and they are different answers: "this
  // build does not author that" is a limit, "that is not a thing" is a mistake.
  function renderProblems() {
    const findings = state.validation.findings || [];
    if (!findings.length) {
      problemsEl.innerHTML = `<span class="im-ok">✓ The model is consistent.</span>`;
      return;
    }
    problemsEl.innerHTML = `<div class="im-problem-head">${findings.length}
        ${findings.length === 1 ? "problem" : "problems"}</div>` +
      findings.map((f) => `<button type="button" class="im-problem ${esc(f.reason)}"
          data-class="${esc(f.classId || "")}" data-assoc="${esc(f.associationId || "")}"
          data-store="${esc(f.storeId || "")}"
          title="${f.reason === "out-of-subset"
            ? "Atlas does not author this. UML allows it; this build does not."
            : "This is not something the notation can mean."}">
          <span class="im-problem-tag">${f.reason === "out-of-subset" ? "not authored" : "invalid"}</span>
          ${esc(f.message)}</button>`).join("");
  }

  // ---- the side panel ------------------------------------------------------
  const storeById = (id) => (state.model.stores || []).find((s) => s.id === id);

  // The panel is the Modeler's: an element header naming what is selected, then
  // collapsible property groups (pgroup.js, shared with api/web/editor.js). A person
  // moves between the two surfaces in one session, so the two panels having their own
  // idea of what a group looks like was a difference with nothing behind it.
  // Every group starts open. This panel has three sections at most, and one of them
  // is the class's attributes — so collapsing is worth offering and not worth doing
  // by default, which is the opposite of the Modeler's dozen groups.
  const groupCtl = groupController(sideEl, "all");

  // paint puts a panel on screen and turns its <h3> sections into those groups. Every
  // renderer goes through it, so the grouping happens in one place rather than being
  // remembered in five.
  function paint(html) {
    sideEl.innerHTML = html;
    const body = sideEl.querySelector(".psec");
    if (body) groupifyPanel(body, groupCtl);
  }

  // The element header, the same shape the Modeler's panel uses: a type chip, the kind
  // in small type, the element's own name in bold, and whatever acts on it as a whole.
  function pheadHTML(chip, kindLabel, name, actions = "") {
    return `<div class="phead">
      <span class="ptype" title="${esc(kindLabel)}">${chip}</span>
      <div><div class="kv">${esc(kindLabel)}</div><b>${esc(name || "unnamed")}</b></div>
      <span style="flex:1"></span>${actions}</div>`;
  }

  // Two letters for the chip: the initials of a multi-word kind, the first two letters
  // of a single-word one. Derived rather than tabulated, so a stereotype the server
  // adds tomorrow gets a chip without this file being edited.
  const abbrev = (label) => {
    const words = String(label || "?").trim().split(/\s+/);
    return (words.length > 1 ? words.map((w) => w[0]).join("") : words[0].slice(0, 2)).toUpperCase();
  };

  function renderSide() {
    if (state.schemaFor) return renderSchema();
    if (!state.selected) return renderNothingSelected();
    if (state.selected.kind === "store") {
      const st = storeById(state.selected.id);
      return st ? renderStorePanel(st) : renderNothingSelected();
    }
    if (state.selected.kind === "class") {
      const c = classById(state.selected.id);
      return c ? renderClassPanel(c) : renderNothingSelected();
    }
    const a = state.model.associations.find((x) => x.id === state.selected.id);
    return a ? renderAssociationPanel(a) : renderNothingSelected();
  }

  function renderNothingSelected() {
    paint(`
      ${pheadHTML("◫", "Information model", state.model.name)}
      <div class="psec">
        <h3>General</h3>
        <p class="muted">${state.model.documentation
          ? esc(state.model.documentation)
          : "Select a class or a relationship to edit it, or add one from the toolbar."}</p>
        <label class="field"><span>Documentation</span>
          <textarea id="im-model-doc" rows="4"
            placeholder="What this model covers — which part of the business these classes describe.">${esc(state.model.documentation || "")}</textarea></label>

        <h3>This is a subset of UML</h3>
        <div class="im-note">
          <ul>${subset.limits.map((l) => `<li><b>${esc(l.area)}.</b> ${esc(l.reason)}</li>`).join("")}</ul>
        </div>
      </div>`);
  }

  function renderClassPanel(c) {
    const kind = stereotypeOf(c.stereotype);
    const findings = state.validation.findings.filter((f) => f.classId === c.id);
    const attrRows = (c.attributes || []).map((a, i) => `
      <tr data-attr="${i}">
        <td class="im-grip" title="Drag to reorder — the order is the order the class box reads in"
            aria-label="Reorder">⠿</td>
        <td><input class="im-in" data-f="name" value="${esc(a.name)}" placeholder="name"/></td>
        <td><select class="im-in" data-f="type">
          ${subset.primitives.map((p) => `<option value="${esc(p.type)}"${p.type === a.type ? " selected" : ""}>${esc(p.label)}</option>`).join("")}
          <optgroup label="Classes in this model">
            ${state.model.classes.filter((x) => x.id !== c.id).map((x) =>
              `<option value="${esc(x.name)}"${x.name === a.type ? " selected" : ""}>${esc(x.name)}</option>`).join("")}
          </optgroup>
          ${subset.primitives.some((p) => p.type === a.type) || state.model.classes.some((x) => x.name === a.type)
            ? "" : `<option value="${esc(a.type)}" selected>${esc(a.type)} (unresolved)</option>`}
        </select></td>
        <td><select class="im-in" data-f="multiplicity">
          ${subset.multiplicities.map((m) => `<option value="${esc(m.multiplicity)}"${m.multiplicity === a.multiplicity ? " selected" : ""}>${esc(m.multiplicity)}</option>`).join("")}
        </select></td>
        <td class="im-key-cell">${kind.hasIdentity
          ? `<input type="checkbox" class="im-in" data-f="key" ${(c.identity || []).includes(a.name) ? "checked" : ""}
               title="Part of the business key — what makes two of these the same one"/>`
          : ""}</td>
        <td><button type="button" class="icon-btn" data-act="del-attr" title="Remove">✕</button></td>
      </tr>`).join("");

    paint(`
      ${pheadHTML(abbrev(kind.label), kind.label, c.name,
        `<button type="button" class="icon-btn" data-act="del-class" title="Delete this class">✕</button>`)}
      <div class="psec">
        <h3>General</h3>
        <label class="field"><span>Name</span>
          <input id="im-c-name" value="${esc(c.name)}" placeholder="Order"/></label>
        <label class="field"><span>Kind</span>
          <select id="im-c-stereo">
            ${subset.stereotypes.map((s) => `<option value="${esc(s.stereotype)}"${s.stereotype === c.stereotype ? " selected" : ""}>${esc(s.label)}</option>`).join("")}
          </select></label>
        <p class="im-meaning">${esc(kind.meaning)}</p>
        <label class="field"><span>Documentation</span>
          <textarea id="im-c-doc" rows="3" placeholder="What this is, in the words the business uses.">${esc(c.documentation || "")}</textarea></label>

        ${kind.hasAttributes ? `
          <h3>Attributes</h3>
          <div class="field-actions">
            <button type="button" class="btn ghost small" data-act="add-attr">+ Attribute</button></div>
          <table class="im-attrs"><thead><tr>
            <th></th><th>Name</th><th>Type</th><th>Card.</th><th title="Business key">⚿</th><th></th>
          </tr></thead><tbody>${attrRows || `<tr><td colspan="6" class="muted">No attributes yet.</td></tr>`}</tbody></table>
          ${kind.hasIdentity ? `<p class="im-hint-text"><b>The business key</b> is what makes two of these the
            same one — <code>Order#ORD-1</code> in this process and in the next. It is the part BPMN has no
            equivalent for, and what a data store and a cross-process lookup will resolve against.</p>` : ""}
          <button type="button" class="btn ghost small" data-act="schema">View JSON Schema</button>
        ` : `
          <h3>Literals</h3>
          <div class="field-actions">
            <button type="button" class="btn ghost small" data-act="add-literal">+ Literal</button></div>
          <table class="im-attrs"><tbody>
            ${(c.literals || []).map((lit, i) => `<tr data-lit="${i}">
              <td class="im-grip" title="Drag to reorder" aria-label="Reorder">⠿</td>
              <td><input class="im-in" data-f="literal" value="${esc(lit)}" placeholder="approved"/></td>
              <td><button type="button" class="icon-btn" data-act="del-literal" title="Remove">✕</button></td>
            </tr>`).join("") || `<tr><td class="muted">No literals yet.</td></tr>`}
          </tbody></table>
        `}

        ${findings.length ? `
          <h3>Problems</h3>
          <div class="im-panel-problems">${findings.map((f) =>
            `<div class="im-problem ${esc(f.reason)}">${esc(f.message)}</div>`).join("")}</div>` : ""}
      </div>`);
  }

  // A store is two sentences: which class it keeps, and what keeps it. The panel is
  // shaped to make both of them hard to leave unsaid.
  function renderStorePanel(st) {
    const findings = state.validation.findings.filter((f) => f.storeId === st.id);
    // Only a business object with a business key can be kept: a process reads from a
    // store by naming which thing it wants, and nothing else names one.
    const storable = state.model.classes.filter(
      (c) => c.stereotype === "businessObject" && (c.identity || []).length > 0);
    const chosen = state.model.classes.find((c) => c.name === st.class);
    const keyless = chosen && !storable.includes(chosen);
    paint(`
      ${pheadHTML("⛁", "Data store", st.name,
        `<button type="button" class="icon-btn" data-act="del-store" title="Delete this store">✕</button>`)}
      <div class="psec">
        <h3>General</h3>
        <label class="field"><span>Name</span>
          <input id="im-s-name" value="${esc(st.name)}" placeholder="Orders"/></label>
        <label class="field"><span>Holds</span>
          <select id="im-s-class">
            <option value=""${st.class ? "" : " selected"}>— choose a class —</option>
            ${storable.map((c) => `<option value="${esc(c.name)}"${c.name === st.class ? " selected" : ""}>${esc(c.name)}</option>`).join("")}
            ${chosen && keyless ? `<option value="${esc(st.class)}" selected>${esc(st.class)} — cannot be kept</option>` : ""}
            ${st.class && !chosen ? `<option value="${esc(st.class)}" selected>${esc(st.class)} (unresolved)</option>` : ""}
          </select></label>
        <p class="im-meaning">Only a <b>business object with a business key</b> can be kept in a store: a process
          reads from one by naming which thing it wants, and the key is the only thing that names one.</p>
        <label class="field"><span>Documentation</span>
          <textarea id="im-s-doc" rows="3" placeholder="What is kept here, and for whom.">${esc(st.documentation || "")}</textarea></label>

        <h3>Where it is kept</h3>
        <label class="field"><span>Backed by <span class="muted">(a Worker)</span></span>
          <input id="im-s-worker" value="${esc(st.worker || "")}" placeholder="clio-main"/></label>
        <p class="im-meaning">The configured Worker that keeps it — a clio event store, a database, a SharePoint
          list. Leave it empty while the store is drawn but not yet wired; a deploy says so rather than refusing.</p>
        <label class="field"><span>Mode</span>
          <select id="im-s-mode">
            ${(subset.storeModes || []).map((m) => `<option value="${esc(m.mode)}"${m.mode === st.mode ? " selected" : ""}>${esc(m.label)}</option>`).join("")}
          </select></label>
        <p class="im-meaning">${esc(storeModeOf(st.mode).meaning || "")}</p>

        ${findings.length ? `
          <h3>Problems</h3>
          <div class="im-panel-problems">${findings.map((f) =>
            `<div class="im-problem ${esc(f.reason)}">${esc(f.message)}</div>`).join("")}</div>` : ""}
      </div>`);
  }

  function renderAssociationPanel(a) {
    const from = classById(a.from.classId) || { name: "?", stereotype: "businessObject" };
    const to = classById(a.to.classId) || { name: "?", stereotype: "businessObject" };
    const kind = kindOf(a.kind) || { label: a.kind, rule: "" };
    const allow = allowed(from.stereotype, to.stereotype);
    const findings = state.validation.findings.filter((f) => f.associationId === a.id);
    const endFields = (side, end, otherName) => `
      <fieldset class="im-end">
        <legend>${esc(side === "from" ? from.name : to.name)}</legend>
        <label class="field"><span>Role</span>
          <input class="im-end-in" data-side="${side}" data-f="role" value="${esc(end.role || "")}"
            placeholder="how ${esc(otherName)} refers to it"/></label>
        <label class="field"><span>Multiplicity</span>
          <select class="im-end-in" data-side="${side}" data-f="multiplicity">
            <option value=""${end.multiplicity ? "" : " selected"}>unsaid</option>
            ${subset.multiplicities.map((m) => `<option value="${esc(m.multiplicity)}"${m.multiplicity === end.multiplicity ? " selected" : ""}>${esc(m.multiplicity)} — ${esc(m.label)}</option>`).join("")}
          </select></label>
      </fieldset>`;

    paint(`
      ${pheadHTML("→", kind.label, a.name || `${from.name} → ${to.name}`,
        `<button type="button" class="icon-btn" data-act="del-assoc" title="Delete this relationship">✕</button>`)}
      <div class="psec">
        <h3>General</h3>
        <p class="im-reading"><b>${esc(from.name)}</b> → <b>${esc(to.name)}</b></p>
        <label class="field"><span>Kind</span>
          <select id="im-a-kind">
            ${subset.associationKinds.map((k) => `<option value="${esc(k.kind)}"${k.kind === a.kind ? " selected" : ""}
              ${allow.includes(k.kind) ? "" : " disabled"}>${esc(k.label)}${allow.includes(k.kind) ? "" : " — not between these"}</option>`).join("")}
          </select></label>
        <p class="im-meaning">${esc(kind.rule)}</p>
        <label class="field"><span>Name</span>
          <input id="im-a-name" value="${esc(a.name || "")}" placeholder="places"/></label>
        <div class="field-actions">
          <button type="button" class="btn ghost small" data-act="flip">⇄ Reverse direction</button></div>

        <h3>Ends</h3>
        ${a.kind === "generalization"
          ? `<p class="im-hint-text">A generalization has no roles or multiplicities: “is a kind of” is not a
             counted relationship. ${esc(to.name)} is the general class.</p>`
          : endFields("from", a.from, to.name) + endFields("to", a.to, from.name)}

        ${findings.length ? `
          <h3>Problems</h3>
          <div class="im-panel-problems">${findings.map((f) =>
            `<div class="im-problem ${esc(f.reason)}">${esc(f.message)}</div>`).join("")}</div>` : ""}
      </div>`);
  }

  // The panel's controls are wired once, by delegation, and read the current
  // selection out of state. Re-binding them on every render is how a single click
  // ends up adding five attributes: the panel re-renders on every keystroke, and a
  // listener attached to the panel *container* survives its contents.
  const selectedClass = () =>
    state.selected && state.selected.kind === "class" ? classById(state.selected.id) : null;
  const selectedStore = () =>
    state.selected && state.selected.kind === "store" ? storeById(state.selected.id) : null;
  const selectedAssoc = () =>
    state.selected && state.selected.kind === "association"
      ? state.model.associations.find((x) => x.id === state.selected.id) : null;

  // Every branch below asks whether anything actually changed before it redraws, and
  // that guard is load-bearing rather than an optimization. The panel is wired for
  // both input and change, so leaving a field fires a second, identical event — and
  // redrawing rebuilds the SVG. If that rebuild lands between the press and the
  // release of a click on another class, the node the press landed on is gone by the
  // time the release happens, the browser synthesizes no click at all, and the class
  // a person just clicked is silently not selected.
  function onSideEdit(e) {
    const target = e.target;
    if (target.closest("#im-model-doc")) {
      if (state.model.documentation === target.value) return;
      state.model.documentation = target.value;
      markDirty();
      return;
    }

    const st = selectedStore();
    if (st) {
      const fields = { "im-s-name": "name", "im-s-class": "class", "im-s-worker": "worker",
        "im-s-mode": "mode", "im-s-doc": "documentation" };
      const field = fields[target.id];
      if (!field || (st[field] || "") === target.value) return;
      st[field] = target.value;
      markDirty();
      // The name and the class are on the drawing; the rest is not, so only those
      // two are worth a redraw while somebody is still typing.
      if (field === "name" || field === "class") syncCanvas();
      if (field === "class") renderProblems();
      return;
    }

    const c = selectedClass();
    if (c) {
      if (target.id === "im-c-name") {
        if (c.name === target.value) return;
        // Renaming a class retypes every attribute that referred to it by the old
        // name, so a rename does not silently break the model it is part of.
        const before = c.name;
        for (const other of state.model.classes) {
          for (const a of other.attributes || []) if (a.type === before) a.type = target.value;
        }
        c.name = target.value;
        markDirty(); syncCanvas(); renderProblems();
        return;
      }
      if (target.id === "im-c-stereo") {
        if (c.stereotype === target.value) return;
        c.stereotype = target.value;
        // A kind that has no identity cannot keep one it was given.
        if (!stereotypeOf(c.stereotype).hasIdentity) c.identity = [];
        markDirty(); render();
        return;
      }
      if (target.id === "im-c-doc") {
        if ((c.documentation || "") === target.value) return;
        c.documentation = target.value;
        markDirty();
        return;
      }

      const row = target.closest("[data-attr]");
      if (row) {
        const a = c.attributes[Number(row.dataset.attr)];
        if (target.dataset.f === "key") {
          c.identity = c.identity || [];
          const at = c.identity.indexOf(a.name);
          if (target.checked === at >= 0) return;
          if (target.checked) c.identity.push(a.name);
          else c.identity.splice(at, 1);
        } else if (target.dataset.f === "name") {
          if (a.name === target.value) return;
          const idx = (c.identity || []).indexOf(a.name);
          a.name = target.value;
          if (idx >= 0) c.identity[idx] = a.name; // the key follows its attribute
        } else {
          if (a[target.dataset.f] === target.value) return;
          a[target.dataset.f] = target.value;
        }
        markDirty(); syncCanvas();
        return;
      }
      const lit = target.closest("[data-lit]");
      if (lit) {
        const i = Number(lit.dataset.lit);
        if (c.literals[i] === target.value) return;
        c.literals[i] = target.value;
        markDirty(); syncCanvas();
      }
      return;
    }

    const a = selectedAssoc();
    if (!a) return;
    if (target.id === "im-a-kind") {
      if (a.kind === target.value) return;
      a.kind = target.value;
      markDirty(); render();
      return;
    }
    if (target.id === "im-a-name") {
      if ((a.name || "") === target.value) return;
      a.name = target.value;
      markDirty(); syncCanvas();
      return;
    }
    if (target.classList.contains("im-end-in")) {
      const end = a[target.dataset.side];
      if ((end[target.dataset.f] || "") === target.value) return;
      end[target.dataset.f] = target.value;
      markDirty(); syncCanvas();
    }
  }

  function onSideClick(e) {
    const btn = e.target.closest("[data-act]");
    if (!btn || !sideEl.contains(btn)) return;
    const act = btn.dataset.act;
    if (act === "close-schema") { state.schemaFor = ""; renderSide(); return; }

    const store = selectedStore();
    if (store && act === "del-store") {
      if (!window.confirm(`Delete the data store ${store.name}? The processes that name it will say so.`)) return;
      state.model.stores = state.model.stores.filter((s) => s.id !== store.id);
      state.selected = null;
      markDirty(); render();
      return;
    }

    const c = selectedClass();
    if (c) {
      if (act === "add-attr") {
        c.attributes = c.attributes || [];
        c.attributes.push({ name: `field${c.attributes.length + 1}`, type: "string", multiplicity: "1" });
        markDirty(); render();
      } else if (act === "del-attr") {
        const i = Number(btn.closest("[data-attr]").dataset.attr);
        const removed = c.attributes[i];
        c.attributes.splice(i, 1);
        c.identity = (c.identity || []).filter((k) => k !== removed.name);
        markDirty(); render();
      } else if (act === "add-literal") {
        c.literals = c.literals || [];
        c.literals.push(`value${c.literals.length + 1}`);
        markDirty(); render();
      } else if (act === "del-literal") {
        c.literals.splice(Number(btn.closest("[data-lit]").dataset.lit), 1);
        markDirty(); render();
      } else if (act === "del-class") {
        if (!window.confirm(`Delete ${c.name}? Relationships touching it go with it.`)) return;
        state.model.classes = state.model.classes.filter((x) => x.id !== c.id);
        state.model.associations = state.model.associations.filter(
          (x) => x.from.classId !== c.id && x.to.classId !== c.id);
        state.selected = null;
        markDirty(); render();
      } else if (act === "schema") {
        state.schemaFor = c.name;
        renderSide();
      }
      return;
    }

    const a = selectedAssoc();
    if (!a) return;
    if (act === "del-assoc") {
      state.model.associations = state.model.associations.filter((x) => x.id !== a.id);
      state.selected = null;
      markDirty(); render();
    } else if (act === "flip") {
      const tmp = a.from; a.from = a.to; a.to = tmp;
      markDirty(); render();
    }
  }

  sideEl.addEventListener("input", onSideEdit);
  sideEl.addEventListener("change", onSideEdit);
  sideEl.addEventListener("click", onSideClick);

  // ---- reordering attributes and literals -----------------------------------
  // The order is not a view setting. A class box reads top to bottom, and which
  // attribute comes first is a statement about the class — a business key usually
  // belongs at the top, the way a reader expects to find it. `attributes` and
  // `literals` are already ordered arrays in the document, so moving a row is a
  // model edit like any other: it marks the model dirty and the canvas redraws.
  //
  // Only the grip starts a drag. Making the whole row draggable would take the
  // pointer away from the text inputs inside it, so selecting a word in a name
  // would drag the attribute instead.

  // listAt answers which list a row belongs to and what it is indexed by, so the
  // drag, the drop and the keyboard move all read one description of the table.
  const listAt = (row) => {
    const c = selectedClass();
    if (!c || !row) return null;
    if (row.dataset.attr !== undefined) {
      return { list: c.attributes || [], index: Number(row.dataset.attr), attr: "attr" };
    }
    if (row.dataset.lit !== undefined) {
      return { list: c.literals || [], index: Number(row.dataset.lit), attr: "lit" };
    }
    return null;
  };

  // move takes the entry at `from` and puts it at `to`, closing the gap it left.
  // A move onto its own position is not an edit, so it does not dirty the model.
  const move = (list, from, to) => {
    if (from === to || from < 0 || to < 0 || from >= list.length || to >= list.length) return false;
    list.splice(to, 0, list.splice(from, 1)[0]);
    markDirty();
    render();
    return true;
  };

  let dragging = null; // { attr, index } while a row is in flight

  sideEl.addEventListener("pointerdown", (e) => {
    const row = e.target.closest("tr[data-attr], tr[data-lit]");
    if (row) row.draggable = !!e.target.closest(".im-grip");
  });

  sideEl.addEventListener("dragstart", (e) => {
    const row = e.target.closest("tr[data-attr], tr[data-lit]");
    const at = listAt(row);
    if (!at) return;
    dragging = { attr: at.attr, index: at.index };
    row.classList.add("im-dragging");
    // Firefox starts no drag at all without data on the transfer.
    if (e.dataTransfer) {
      e.dataTransfer.effectAllowed = "move";
      try { e.dataTransfer.setData("text/plain", String(at.index)); } catch { /* not settable here */ }
    }
  });

  sideEl.addEventListener("dragover", (e) => {
    if (!dragging) return;
    const row = e.target.closest("tr[data-attr], tr[data-lit]");
    const at = listAt(row);
    if (!at || at.attr !== dragging.attr) return;
    e.preventDefault(); // without this the drop never fires
    if (e.dataTransfer) e.dataTransfer.dropEffect = "move";
    // The line marks the edge the row would land against, which is the half of the
    // hovered row the pointer is in — the same gesture every list reorder uses.
    const box = row.getBoundingClientRect();
    const after = e.clientY > box.top + box.height / 2;
    for (const r of sideEl.querySelectorAll(".im-drop-before, .im-drop-after")) {
      r.classList.remove("im-drop-before", "im-drop-after");
    }
    row.classList.add(after ? "im-drop-after" : "im-drop-before");
  });

  sideEl.addEventListener("drop", (e) => {
    if (!dragging) return;
    const row = e.target.closest("tr[data-attr], tr[data-lit]");
    const at = listAt(row);
    if (!at || at.attr !== dragging.attr) return;
    e.preventDefault();
    const box = row.getBoundingClientRect();
    const after = e.clientY > box.top + box.height / 2;
    // Dropping *after* a row that sits above the dragged one lands on that row's
    // index; below it, the gap the dragged row leaves has already shifted it up.
    let to = at.index + (after ? 1 : 0);
    if (dragging.index < to) to -= 1;
    const from = dragging.index;
    dragging = null;
    move(at.list, from, to);
  });

  sideEl.addEventListener("dragend", () => {
    dragging = null;
    for (const r of sideEl.querySelectorAll(".im-dragging, .im-drop-before, .im-drop-after")) {
      r.classList.remove("im-dragging", "im-drop-before", "im-drop-after");
    }
  });

  // Alt+Up / Alt+Down move the row a field is in, so reordering is reachable
  // without a pointer — and without leaving the field being edited.
  sideEl.addEventListener("keydown", (e) => {
    if (!e.altKey || (e.key !== "ArrowUp" && e.key !== "ArrowDown")) return;
    const at = listAt(e.target.closest("tr[data-attr], tr[data-lit]"));
    if (!at) return;
    const to = at.index + (e.key === "ArrowUp" ? -1 : 1);
    if (to < 0 || to >= at.list.length) return;
    e.preventDefault();
    const field = e.target.dataset && e.target.dataset.f;
    if (!move(at.list, at.index, to)) return;
    // render() replaced the row, so put the caret back where the author left it.
    const moved = sideEl.querySelector(`tr[data-${at.attr}="${to}"]`);
    const focus = moved && (field ? moved.querySelector(`[data-f="${field}"]`) : moved.querySelector(".im-in"));
    if (focus) focus.focus();
  });

  // renderSchema shows the derived contract beside the drawing. It is read-only and
  // says what it dropped — a JSON document is a tree and a class model is a graph,
  // so only composition survives as containment.
  async function renderSchema() {
    paint(`<div class="psec"><p class="muted">Projecting ${esc(state.schemaFor)}…</p></div>`);
    let projection;
    try {
      projection = await api("GET",
        `/api/v1/infomodel/models/${encodeURIComponent(id)}/schema?class=${encodeURIComponent(state.schemaFor)}`);
    } catch (e) {
      paint(`
        ${pheadHTML("{ }", "JSON Schema", state.schemaFor,
          `<button type="button" class="btn ghost small" data-act="close-schema">Close</button>`)}
        <div class="psec">
          <p class="muted">${esc(e.message)}</p>
          <p class="im-hint-text">A schema is derived from a saved model, so save the diagram — and fix anything the
            problems bar lists — before projecting it.</p></div>`);
      return;
    }
    paint(`
      ${pheadHTML("{ }", "JSON Schema", projection.class,
        `<button type="button" class="btn ghost small" data-act="close-schema">Close</button>`)}
      <div class="psec">
        <h3>Schema</h3>
        <p class="im-hint-text">Derived, never edited. This is what a <i>value</i> of this class is checked against;
          the diagram is what a person reads.</p>
        <pre class="im-schema">${esc(JSON.stringify(projection.schema, null, 2))}</pre>

        <h3>What the projection could not carry</h3>
        ${projection.loss.map((n) => `<div class="im-loss"><b>${esc(n.area)}.</b> ${esc(n.reason)}</div>`).join("")}
      </div>`);
  }

  // ---- interaction ---------------------------------------------------------
  // Adding a class places it where there is room rather than on top of the last
  // one: a palette that stacks boxes makes its own diagram unreadable.
  function freeSpot() {
    const cols = 4;
    const n = state.model.classes.length;
    return { x: 40 + (n % cols) * (BOX_W + 60), y: 40 + Math.floor(n / cols) * 200 };
  }

  root.querySelector("#im-palette").addEventListener("click", (e) => {
    if (e.target.closest('[data-add="store"]')) {
      const spot = freeSpot();
      const st = {
        id: `new-${Math.random().toString(36).slice(2, 10)}`, name: "NewStore", class: "",
        worker: "", mode: (subset.storeModes[0] || {}).mode || "read",
        x: spot.x, y: spot.y + 240,
      };
      state.model.stores.push(st);
      state.selected = { kind: "store", id: st.id };
      state.connecting = null;
      markDirty(); render();
      return;
    }
    const add = e.target.closest(".im-add");
    if (add) {
      const kind = stereotypeOf(add.dataset.stereotype);
      const spot = freeSpot();
      const c = {
        id: "", name: `New${kind.label.replace(/\s/g, "")}`, stereotype: kind.stereotype,
        attributes: kind.hasAttributes ? [] : undefined, literals: kind.hasAttributes ? undefined : [],
        identity: [], x: spot.x, y: spot.y,
      };
      // A new class needs a local handle until the server mints its real id, because
      // selection and association ends both address a class by id.
      c.id = `new-${Math.random().toString(36).slice(2, 10)}`;
      state.model.classes.push(c);
      state.selected = { kind: "class", id: c.id };
      state.connecting = null;
      markDirty(); render();
      return;
    }
    const connect = e.target.closest(".im-connect");
    if (connect) {
      state.connecting = state.connecting && state.connecting.kind === connect.dataset.kind
        ? null : { kind: connect.dataset.kind, fromId: null };
      render();
    }
  });

  // Selecting on the canvas and selecting in the panel are the same selection, so
  // the round trip is guarded: telling the panel what the canvas selected must not
  // tell the canvas back and start again.
  let applyingSelection = false;

  function onCanvasSelection(bo) {
    if (applyingSelection) return;
    if (state.connecting) { connectStep(bo); return; }
    state.selected = bo && bo.element && bo.element !== "store-link"
      ? { kind: bo.element, id: bo.id }
      : null;
    state.schemaFor = "";
    render();
  }

  // connectStep is the two-click draw. The first click names the end it starts from,
  // the second the end it lands on; anything else cancels, because a half-drawn
  // relationship left on screen is a mode nobody asked to stay in.
  function connectStep(bo) {
    if (!bo || bo.element !== "class") { state.connecting = null; render(); return; }
    if (!state.connecting.fromId) {
      state.connecting.fromId = bo.id;
      render();
      return;
    }
    const from = classById(state.connecting.fromId);
    const to = classById(bo.id);
    const kind = state.connecting.kind;
    if (!from || !to || from.id === to.id) { state.connecting = null; render(); return; }
    if (!allowed(from.stereotype, to.stereotype).includes(kind)) {
      // The matrix the server enforces is the matrix that refuses here, and it
      // refuses in the server's own words.
      toast(refusalFor(kind, from, to), "err");
      state.connecting = null;
      render();
      return;
    }
    const a = {
      id: `new-${Math.random().toString(36).slice(2, 10)}`, kind, name: "",
      from: { classId: from.id, role: "", multiplicity: kind === "generalization" ? "" : "1" },
      to: { classId: to.id, role: "", multiplicity: kind === "generalization" ? "" : "0..*" },
    };
    state.model.associations.push(a);
    state.selected = { kind: "association", id: a.id };
    state.connecting = null;
    markDirty(); render();
  }

  // refusalFor turns a matrix miss into the sentence the server would have sent. It
  // reads the served tables rather than restating a rule, so the two cannot drift.
  function refusalFor(kind, from, to) {
    const fk = stereotypeOf(from.stereotype);
    const tk = stereotypeOf(to.stereotype);
    if (fk.stereotype === "enumeration" || tk.stereotype === "enumeration") {
      return "An enumeration is a closed set of values, not something a relationship can point at. " +
        "Give one of these classes an attribute typed as the enumeration instead.";
    }
    if (kind === "generalization") {
      return `A ${fk.label} cannot be a kind of a ${tk.label}. A specialization has to be usable everywhere ` +
        "the thing it specializes is, so both ends must be the same kind of class.";
    }
    return `A ${fk.label} has no existence of its own, so it cannot be the whole that owns or groups parts. ` +
      "Make it a business object, or draw the relationship the other way round.";
  }

  // A shape the author dragged is written back into the document, so the arrangement
  // is saved with the model — a diagram somebody laid out is one they can read again.
  // The canvas reports what actually moved, so a box dragged away and back reports
  // nothing and no revision is spent on it.
  function absorbMoves() {
    const moves = canvas.moved();
    if (!moves.length) return;
    for (const m of moves) {
      const target = m.kind === "store" ? storeById(m.id) : classById(m.id);
      if (!target) continue;
      target.x = m.x;
      target.y = m.y;
    }
    markDirty();
  }

  problemsEl.addEventListener("click", (e) => {
    const p = e.target.closest(".im-problem");
    if (!p) return;
    if (p.dataset.class) state.selected = { kind: "class", id: p.dataset.class };
    else if (p.dataset.store) state.selected = { kind: "store", id: p.dataset.store };
    else if (p.dataset.assoc) state.selected = { kind: "association", id: p.dataset.assoc };
    state.schemaFor = "";
    render();
  });

  // ---- saving --------------------------------------------------------------
  const local = (v) => typeof v === "string" && v.startsWith("new-");

  async function save() {
    saveBtn.disabled = true;
    // Local handles are sent as they are. The server mints the real ids and rewrites
    // the association ends that pointed at a handle — which is why the canvas may
    // name a box it has just drawn without minting anything itself.
    const payload = {
      name: state.model.name,
      documentation: state.model.documentation || "",
      classes: state.model.classes,
      associations: state.model.associations,
      stores: state.model.stores,
      revision: state.model.revision,
    };
    try {
      const saved = await api("PUT", `/api/v1/infomodel/models/${encodeURIComponent(id)}`, payload);
      state.model = saved;
      state.validation = saved.validation || { valid: true, findings: [] };
      state.dirty = false;
      dirtyEl.hidden = true;
      root.querySelector("#im-rev").textContent = `r${saved.revision}`;
      // Selection is by id, and every local handle has just been replaced.
      if (state.selected && local(state.selected.id)) state.selected = null;
      toast("Saved", "ok");
      render();
    } catch (e) {
      saveBtn.disabled = false;
      // A refused save carries the findings, so the problems bar shows exactly what
      // the server objected to rather than one sentence standing in for a list.
      const findings = e.body && e.body.findings;
      if (findings && findings.length) {
        state.validation = { valid: false, findings };
        renderProblems();
        toast("Not saved — the model is not consistent yet", "err");
        return;
      }
      // A conflict is somebody else's work, not a mistake in this one: say what is
      // at stake rather than only that the save failed.
      if (e.status === 409) {
        toast("Somebody else saved this model since you opened it. Reload to see their changes.", "err");
        return;
      }
      toast(e.message, "err");
    }
  }

  saveBtn.addEventListener("click", save);
  const onKey = (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
      e.preventDefault();
      if (state.dirty) save();
    }
    if (e.key === "Escape" && state.connecting) { state.connecting = null; render(); }
  };
  document.addEventListener("keydown", onKey);
  // The editor listens on the document for its shortcut and the canvas holds a
  // diagram of its own, so both are let go of when the router navigates away.
  window.addEventListener("hashchange", () => {
    document.removeEventListener("keydown", onKey);
    canvas.destroy();
  }, { once: true });

  // One path to the first draw, not two. render() syncs, and a sync with nothing
  // drawn yet is the first draw — a fresh root, an empty undo stack and a fitted
  // viewport. Calling canvas.render() here as well would draw the model twice.
  render();
}
