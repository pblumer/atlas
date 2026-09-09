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
import { loadCanvasBundle } from "./canvas-bundle.js";

const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g,
  (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// How wide a class is drawn. It is the one piece of geometry this file still needs,
// to place a new box where there is room; the rest lives with the drawing, in
// api/web/vendor/canvas/src/uml.js.
// The step a new class is placed on, not the width one is drawn at: a class box now
// grows to hold its members (up to 380px in uml.js), so a grid stepped by the old
// fixed 200 would drop the next one on top of the last.
const BOX_STEP = 400;

// The bundle carries both canvases; this view wants the UML half of it.
function loadCanvas() {
  return loadCanvasBundle().then((bundle) => bundle.uml);
}

export async function mountClassDiagram(root, { api, toast, id }) {
  root.innerHTML = `<div class="card"><p class="muted">Loading class diagram…</p></div>`;

  let doc, subset, uml;
  try {
    [doc, subset, uml] = await Promise.all([
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
    selected: null,      // {kind: "class"|"association", id} — the one the panel edits
    // What the canvas has hold of, when that is more than one thing. A marquee
    // selects several at once; the panel still edits one of them, so the two are
    // separate fields rather than one that has to mean both.
    multi: [],
    connecting: null,    // {kind, fromId} while a relationship is being drawn
    dirty: false,
    schemaFor: "",       // class whose JSON Schema projection is open
    // What the panel's member filter is narrowed to, and what the bar's search box
    // is looking for. Both are view state: neither touches the document, and neither
    // survives leaving the class it was typed for.
    memberFilter: "",
    search: "",
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
        <span class="im-search">
          <input type="search" id="im-search" placeholder="Find a class or a member…"
            aria-label="Find a class or a member" autocomplete="off" role="combobox"
            aria-expanded="false" aria-controls="im-search-results"/>
          <div class="im-search-results" id="im-search-results" role="listbox" hidden></div>
        </span>
        <span style="flex:1"></span>
        <span class="im-dirty" id="im-dirty" hidden>unsaved</span>
        <button class="btn" id="im-save" disabled title="Save the diagram (Ctrl/⌘ + S)">Save</button>
      </div>
      <div class="im-body">
        <div class="im-canvas" id="im-canvas">
          <p class="im-empty-hint" id="im-empty" hidden>No classes yet. Add a business object — an Order, a
            Customer, a Claim — and give it a business key.</p>
          <div class="im-tools" aria-label="Canvas controls">
            <button type="button" class="icon-btn" data-tool="zoom-in" title="Zoom in" aria-label="Zoom in">+</button>
            <button type="button" class="icon-btn" data-tool="zoom-out" title="Zoom out" aria-label="Zoom out">−</button>
            <button type="button" class="icon-btn" data-tool="fit" title="Fit the whole diagram in the window" aria-label="Fit diagram">⊡</button>
            <span class="im-tool-sep" aria-hidden="true"></span>
            <button type="button" class="icon-btn" data-tool="undo" title="Undo the last move on the canvas (Ctrl/⌘ + Z)" aria-label="Undo" disabled>↺</button>
            <button type="button" class="icon-btn" data-tool="redo" title="Redo (Ctrl/⌘ + Shift + Z)" aria-label="Redo" disabled>↻</button>
          </div>
        </div>
        <div class="props-resizer im-resizer" id="im-resizer"
          title="Drag to widen the panel — double-click to reset"></div>
        <div class="im-side" id="im-side"></div>
      </div>
      <div class="im-problems" id="im-problems"></div>
    </div>`;

  const canvasEl = root.querySelector("#im-canvas");
  const emptyEl = root.querySelector("#im-empty");
  const sideEl = root.querySelector("#im-side");
  const problemsEl = root.querySelector("#im-problems");
  const saveBtn = root.querySelector("#im-save");
  const undoBtn = root.querySelector('[data-tool="undo"]');
  const redoBtn = root.querySelector('[data-tool="redo"]');
  const dirtyEl = root.querySelector("#im-dirty");

  // ---- palette -------------------------------------------------------------
  // Each kind carries the sentence that tells a modeler which one to pick. The
  // difference between a business object and a value type is the single most
  // consequential choice in this metamodel, and a palette of three bare words is
  // how it gets made by accident.
  // The canvas. Everything a modeler expects of one — zoom, pan, marquee, multi-select
  // move, undo of a move, keyboard nudging — comes from diagram-js; what Atlas owns is
  // how a class is drawn and what the served subset permits between two of them
  // (ADR-0237).
  const canvas = new uml.ClassCanvas(canvasEl, {
    subset,
    paletteEntries,
    onSelection: (bo, all) => onCanvasSelection(bo, all),
    onChange: () => { absorbMoves(); syncHistoryButtons(); },
    onTool: (tool) => showMarquee(tool === "marquee"),
  });

  // Zoom and pan have been the canvas's own since it moved onto diagram-js
  // (ADR-0237): the wheel scrolls, ctrl and the wheel zoom, and a drag on empty
  // sheet pans. What was missing is that none of that is *visible* — a person who
  // does not already know the gesture finds a diagram they cannot make fit, which
  // is what the record meant by looking like the two canvases beside it.
  //
  // So these are the Panorama canvas's three controls, with its icons, its step and
  // its placement, because zooming a diagram is the same act on both surfaces and a
  // near-miss between them is worse than either choice on its own.
  root.querySelector('[data-tool="zoom-in"]').addEventListener("click", () => canvas.zoom(1.2));
  root.querySelector('[data-tool="zoom-out"]').addEventListener("click", () => canvas.zoom(1 / 1.2));
  root.querySelector('[data-tool="fit"]').addEventListener("click", () => canvas.fit());

  // Selecting several at once. The gesture itself is diagram-js's lasso; what was
  // missing is that it had no way in. A plain drag on empty sheet pans — it has to,
  // or a diagram larger than its window could not be moved — so drawing a box has to
  // be asked for, either by this button or by holding Shift, exactly as in the
  // process modeler.
  //
  // The button arms it and nothing more. Whether the mode is still on is the canvas's
  // to say and this only follows, because the mode ends without the button being
  // touched: the box being drawn spends it, and Escape takes it back.
  // Whether the next drag draws a box is the canvas's to say and this only follows:
  // the mode ends without anything being pressed — the box being drawn spends it, and
  // Escape takes it back. diagram-js lights the palette entry itself, off the same
  // tool events, so all that is left here is the cursor over the sheet.
  function showMarquee(on) {
    canvasEl.classList.toggle("marquee", on);
  }

  // Undo and redo were the other half of the record's promise, and they were reachable
  // from nowhere: the canvas has kept a command stack since the port, and nothing on
  // screen or on the keyboard ever asked it for anything.
  //
  // What they undo is what the canvas does, which here is moving something. Everything
  // else — a renamed class, a retyped attribute, a deleted one — is the panel editing
  // the document directly, and it is not on this stack. So the button says "the last
  // move" rather than "the last change": a control that claims more than it does is
  // worse than one that claims less.
  undoBtn.addEventListener("click", () => { canvas.undo(); syncHistoryButtons(); });
  redoBtn.addEventListener("click", () => { canvas.redo(); syncHistoryButtons(); });
  function syncHistoryButtons() {
    undoBtn.disabled = !canvas.canUndo();
    redoBtn.disabled = !canvas.canRedo();
  }

  // ---- the search ----------------------------------------------------------
  //
  // A model outgrows its window in two directions at once: a canvas with thirty
  // classes on it, and a class with forty members in it. This searches both from one
  // field, because a person looking for `lieferadresse` does not know or care whether
  // it is a class or an attribute of one — and what they get back says which.
  //
  // Picking a member does two things: it selects the class, and it narrows that
  // class's panel to what was searched for. The second is the point. Selecting a
  // class with forty attributes and leaving the reader to scroll for the one they
  // just named would answer the question and then hide the answer.
  const searchEl = root.querySelector("#im-search");
  const resultsEl = root.querySelector("#im-search-results");

  // A result is where it is and what it is called: {classId, attribute?, label, kind}.
  // Classes come first because the class is the coarser answer and the one a reader
  // means more often; within each group the model's own order is kept, so the same
  // query always answers in the same order.
  function searchResults(query) {
    const q = query.trim().toLowerCase();
    if (!q) return [];
    const classes = [];
    const members = [];
    for (const c of state.model.classes || []) {
      if (c.name.toLowerCase().includes(q)) {
        classes.push({ classId: c.id, label: c.name, kind: stereotypeOf(c.stereotype).label });
      }
      for (const a of c.attributes || []) {
        if (a.name.toLowerCase().includes(q) || String(a.type).toLowerCase().includes(q)) {
          members.push({ classId: c.id, attribute: a.name, label: `${c.name} · ${a.name}`, kind: a.type });
        }
      }
      for (const lit of c.literals || []) {
        if (String(lit).toLowerCase().includes(q)) {
          members.push({ classId: c.id, attribute: String(lit), label: `${c.name} · ${lit}`, kind: "literal" });
        }
      }
    }
    for (const st of state.model.stores || []) {
      if (st.name.toLowerCase().includes(q)) {
        classes.push({ storeId: st.id, label: st.name, kind: "Data store" });
      }
    }
    return [...classes, ...members].slice(0, 12);
  }

  function renderSearch() {
    const results = searchResults(state.search);
    searchEl.setAttribute("aria-expanded", String(Boolean(state.search.trim())));
    if (!state.search.trim()) {
      resultsEl.hidden = true;
      resultsEl.innerHTML = "";
      return;
    }
    resultsEl.hidden = false;
    resultsEl.innerHTML = results.length
      ? results.map((r, i) => `<button type="button" class="im-search-hit" role="option" data-hit="${i}"
          aria-selected="${i === 0}">${esc(r.label)}<span class="muted">${esc(r.kind)}</span></button>`).join("")
      : `<p class="im-search-none muted">Nothing in this model matches “${esc(state.search.trim())}”.</p>`;
    resultsEl.__results = results;
  }

  // pickResult selects what was found and brings it into view. The canvas is scrolled
  // rather than fitted: fitting would answer "where is Order" by zooming out until
  // every class is equally unreadable.
  function pickResult(result) {
    if (!result) return;
    state.schemaFor = "";
    state.connecting = null;
    if (result.storeId) {
      selectOne({ kind: "store", id: result.storeId });
    } else {
      selectOne({ kind: "class", id: result.classId });
      // Set before the render, and claim the class the filter belongs to, so the
      // panel does not drop it as a filter typed for somebody else.
      filteredFor = result.classId;
      state.memberFilter = result.attribute || "";
    }
    closeSearch();
    render();
    focusOnCanvas(result.storeId || result.classId);
  }

  // The canvas owns the viewport, so this asks it to scroll rather than doing the
  // arithmetic here. It reaches for the diagram-js canvas and the shape map the
  // bundle exposes; a `focus(id)` of its own is the tidier home for it, and belongs
  // in the change that merges the two vendored bundles (ADR-0237).
  function focusOnCanvas(id) {
    const shape = canvas.shapes?.get(id);
    if (shape && canvas.canvas?.scrollToElement) canvas.canvas.scrollToElement(shape, { top: 80, bottom: 80, left: 80, right: 80 });
  }

  function closeSearch() {
    state.search = "";
    searchEl.value = "";
    renderSearch();
  }

  searchEl.addEventListener("input", () => {
    state.search = searchEl.value;
    renderSearch();
  });
  searchEl.addEventListener("keydown", (e) => {
    if (e.key === "Escape") { closeSearch(); searchEl.blur(); return; }
    // Enter takes the first hit, which is what a person typing a name they know
    // expects: type "Order", press Enter, be looking at Order.
    if (e.key === "Enter") {
      e.preventDefault();
      pickResult((resultsEl.__results || [])[0]);
    }
  });
  // A click on a hit has to land before the field losing focus takes the list away.
  resultsEl.addEventListener("mousedown", (e) => e.preventDefault());
  resultsEl.addEventListener("click", (e) => {
    const hit = e.target.closest("[data-hit]");
    if (hit) pickResult((resultsEl.__results || [])[Number(hit.dataset.hit)]);
  });
  searchEl.addEventListener("blur", () => setTimeout(() => { if (state.search) closeSearch(); }, 120));

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
      // Several selected go back as several. Reconciling rebuilds every relationship,
      // so the elements the canvas had are gone by now and the selection has to be
      // put back by id — and putting back only the first would quietly undo a marquee
      // on the next keystroke anywhere in the panel.
      if (state.multi.length > 1) canvas.select(state.multi.map((m) => m.id));
      else canvas.select(state.selected ? state.selected.id : null);
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
    // Which relationship is armed lives in the palette now, and the palette is
    // diagram-js's — so it is told to ask again rather than having a class toggled on it.
    canvas.refreshPalette();
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
    applyMemberFilter();
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
    if (state.multi.length > 1) return renderManySelected();
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

  // Several selected at once. The panel edits one element at a time — a name, a type,
  // a multiplicity all belong to exactly one thing — so it says what is held instead
  // of pretending to edit all of it, and each line is the way back to editing one.
  function renderManySelected() {
    const label = (sel) => {
      if (sel.kind === "store") return (storeById(sel.id) || {}).name || "unnamed";
      if (sel.kind === "class") return (classById(sel.id) || {}).name || "unnamed";
      const a = state.model.associations.find((x) => x.id === sel.id);
      return a ? (a.name || (kindOf(a.kind) || {}).label || "relationship") : "relationship";
    };
    paint(`
      ${pheadHTML("⬚", "Selection", `${state.multi.length} elements`)}
      <div class="psec">
        <h3>General</h3>
        <p class="muted">Drag any one of them to move them all. Pick a line below to edit
          that one on its own.</p>
        <div class="im-many">
          ${state.multi.map((sel, i) =>
            `<button type="button" class="im-many-row" data-many="${i}">
               <span class="kv">${esc(sel.kind)}</span> ${esc(label(sel))}</button>`).join("")}
        </div>
      </div>`);
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

  // filteredFor is the class the member filter was typed for. See renderClassPanel.
  let filteredFor = null;

  // ---- finding a member, and finding a class -------------------------------
  //
  // A class with forty attributes is a scroll, and the one being looked for is in the
  // middle of it. The filter narrows the table without touching the document: rows
  // are hidden, never removed, so every row keeps the index its edit handlers and its
  // reorder read — and the whole thing is a view state that dies with the selection.
  //
  // It is applied to the DOM rather than rendered into it, and that is the reason it
  // works: the panel re-renders on every keystroke, so a filter that re-rendered
  // would take the caret out of the field being typed in.
  function memberFilterHTML(placeholder) {
    return `<div class="im-filter">
      <input type="search" id="im-member-filter" placeholder="${esc(placeholder)}"
        value="${esc(state.memberFilter)}" aria-label="${esc(placeholder)}" autocomplete="off"/>
      <span class="im-filter-count muted" id="im-member-count"></span>
    </div>`;
  }

  // applyMemberFilter hides what does not match and says how much it hid. Reordering
  // is refused while it is narrowed, because dragging a row past rows that are not
  // on screen moves it somewhere nobody chose.
  function applyMemberFilter() {
    const rows = sideEl.querySelectorAll(".im-attrs tbody tr[data-member]");
    const countEl = sideEl.querySelector("#im-member-count");
    if (!rows.length) {
      if (countEl) countEl.textContent = "";
      return;
    }
    const q = state.memberFilter.trim().toLowerCase();
    let shown = 0;
    for (const row of rows) {
      const match = !q || row.dataset.member.includes(q);
      row.hidden = !match;
      if (match) shown++;
      const grip = row.querySelector(".im-grip");
      if (grip) {
        grip.classList.toggle("disabled", Boolean(q));
        grip.title = q
          ? "Clear the filter to reorder — dragging past rows that are hidden would move this somewhere nobody chose"
          : "Drag to reorder — the order is the order the class box reads in";
      }
    }
    if (countEl) {
      countEl.textContent = q ? `${shown} of ${rows.length}` : "";
      countEl.classList.toggle("none", q && shown === 0);
    }
    const empty = sideEl.querySelector(".im-filter-empty");
    if (empty) empty.remove();
    if (q && shown === 0) {
      const table = sideEl.querySelector(".im-attrs");
      table?.insertAdjacentHTML("afterend",
        `<p class="im-filter-empty muted">Nothing here matches “${esc(state.memberFilter.trim())}”.</p>`);
    }
  }

  function renderClassPanel(c) {
    // A filter typed for one class means nothing on the next, so it is dropped when
    // the selection moves. Comparing here rather than at each of the places that set
    // a selection is what makes that true for all of them.
    if (filteredFor !== c.id) {
      filteredFor = c.id;
      state.memberFilter = "";
    }
    const kind = stereotypeOf(c.stereotype);
    const findings = state.validation.findings.filter((f) => f.classId === c.id);
    const attrRows = (c.attributes || []).map((a, i) => `
      <tr data-attr="${i}" data-member="${esc(`${a.name} ${a.type}`.toLowerCase())}">
        <td class="im-grip" title="Drag to reorder — the order is the order the class box reads in"
            aria-label="Reorder">⠿</td>
        <td><input class="im-in" data-f="name" value="${esc(a.name)}" placeholder="name"
              title="${esc(a.name)}"/></td>
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

    // Both member tables below are `no-enhance`: they are editing grids, not lists of
    // data — their cells are inputs the shared enhancer finds no text to sort or filter
    // by, the row order is the order the class box reads in and is set by dragging, and
    // the box above each of them already filters it (ADR-0286).
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
          ${memberFilterHTML("Filter attributes by name or type…")}
          <table class="im-attrs no-enhance"><thead><tr>
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
          ${memberFilterHTML("Filter literals…")}
          <table class="im-attrs no-enhance"><tbody>
            ${(c.literals || []).map((lit, i) => `<tr data-lit="${i}" data-member="${esc(String(lit).toLowerCase())}">
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
    // The filter is not an edit: it changes what is on screen, not what is in the
    // document. Handling it here and returning is what keeps the caret in the field
    // — every path below ends in a render, and a render rebuilds this panel.
    if (target.id === "im-member-filter") {
      state.memberFilter = target.value;
      applyMemberFilter();
      return;
    }
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
        // This row is not repainted — that is what keeps the caret in the field being
        // typed in — so the two things a repaint would have refreshed are refreshed
        // here: the tooltip that makes a name readable when the column cannot show all
        // of it, and what the filter matches this row against.
        const nameInput = row.querySelector('[data-f="name"]');
        if (nameInput) nameInput.title = a.name;
        row.dataset.member = `${a.name} ${a.type}`.toLowerCase();
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
    // A line in the "several selected" list narrows the selection to that one, which
    // is also what puts it back on the canvas — the round trip is the same one a
    // click on the drawing takes.
    const many = e.target.closest("[data-many]");
    if (many && sideEl.contains(many)) {
      const sel = state.multi[Number(many.dataset.many)];
      if (sel) { selectOne(sel); render(); }
      return;
    }
    const btn = e.target.closest("[data-act]");
    if (!btn || !sideEl.contains(btn)) return;
    const act = btn.dataset.act;
    if (act === "close-schema") { state.schemaFor = ""; renderSide(); return; }

    const store = selectedStore();
    if (store && act === "del-store") {
      if (!window.confirm(`Delete the data store ${store.name}? The processes that name it will say so.`)) return;
      state.model.stores = state.model.stores.filter((s) => s.id !== store.id);
      selectOne(null);
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
        selectOne(null);
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
      selectOne(null);
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
    // Reordering past rows that are not on screen would move a member somewhere
    // nobody chose, so a narrowed list does not reorder.
    if (state.memberFilter.trim() && e.target.closest?.("tr[data-member]")) { e.preventDefault(); return; }
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
    return { x: 40 + (n % cols) * (BOX_STEP + 60), y: 40 + Math.floor(n / cols) * 220 };
  }

  // ---- the palette --------------------------------------------------------
  //
  // Down the canvas's left edge, in diagram-js's own palette — the one bpmn-js and
  // dmn-js put there, with its chrome and its stylesheet, because a person moving
  // between the process modeler and this one should not have to find a second kind of
  // toolbox. It used to be a row of text buttons in the title bar, which put the
  // things you draw with as far from the sheet you draw on as the window allows.
  //
  // The entries come from the served subset, so the palette offers exactly what the
  // write path accepts (ADR-0230) and gains a stereotype the day the server does.

  function addClass(stereotype) {
    const kind = stereotypeOf(stereotype);
    const spot = freeSpot();
    const c = {
      // A new class needs a local handle until the server mints its real id, because
      // selection and association ends both address a class by id.
      id: `new-${Math.random().toString(36).slice(2, 10)}`,
      name: `New${kind.label.replace(/\s/g, "")}`, stereotype: kind.stereotype,
      attributes: kind.hasAttributes ? [] : undefined, literals: kind.hasAttributes ? undefined : [],
      identity: [], x: spot.x, y: spot.y,
    };
    state.model.classes.push(c);
    selectOne({ kind: "class", id: c.id });
    state.connecting = null;
    markDirty(); render();
  }

  function addStore() {
    const spot = freeSpot();
    const st = {
      id: `new-${Math.random().toString(36).slice(2, 10)}`, name: "NewStore", class: "",
      worker: "", mode: (subset.storeModes[0] || {}).mode || "read",
      x: spot.x, y: spot.y + 240,
    };
    state.model.stores.push(st);
    selectOne({ kind: "store", id: st.id });
    state.connecting = null;
    markDirty(); render();
  }

  // Arming a relationship is a mode: the next two classes clicked become its ends.
  // Pressing the armed one again puts it away, which is the only way out that does
  // not require drawing something first.
  function armConnect(kind) {
    state.connecting = state.connecting && state.connecting.kind === kind
      ? null : { kind, fromId: null };
    render();
  }

  function paletteEntries() {
    // The lasso leads, in diagram-js's own `tools` group, exactly where bpmn-js puts
    // it. That group is not decoration: the palette lights the active tool by reading
    // `[data-group=tools]` out of its own markup, and with no such group it reads null
    // and throws the moment any tool is activated — which took the lasso down with it.
    // A tool belongs in the tools group; the crash was the library saying so.
    const out = [{
      id: "lasso", group: "tools",
      title: "Select several at once: draw a box around them. Shift and drag does the same " +
        "without this, and Escape puts the drag back to panning.",
      onClick: (event) => canvas.marquee(event),
    }, { id: "sep-tools", group: "tools", separator: true }];
    for (const st of subset.stereotypes) {
      out.push({
        id: st.stereotype, group: "elements", title: `${st.label} — ${st.meaning}`,
        onClick: () => addClass(st.stereotype),
      });
    }
    out.push({
      id: "store", group: "elements",
      title: "Data store — where instances of a class outlive the process that made them. " +
        "Declared once here and named by every process that reaches it.",
      onClick: () => addStore(),
    });
    out.push({ id: "sep-relations", group: "elements", separator: true });
    for (const k of subset.associationKinds) {
      out.push({
        id: k.kind, group: "relations", title: `${k.label} — ${k.rule}`,
        active: !!state.connecting && state.connecting.kind === k.kind,
        onClick: () => armConnect(k.kind),
      });
    }
    return out;
  }

  // Selecting on the canvas and selecting in the panel are the same selection, so
  // the round trip is guarded: telling the panel what the canvas selected must not
  // tell the canvas back and start again.
  let applyingSelection = false;

  function onCanvasSelection(bo, all = []) {
    if (applyingSelection) return;
    if (state.connecting) { connectStep(bo); return; }
    state.selected = bo && bo.element && bo.element !== "store-link"
      ? { kind: bo.element, id: bo.id }
      : null;
    // The store's line to the class it holds is drawn, not authored, so it is not
    // one of the things a box can take hold of — it comes and goes with its store.
    state.multi = all
      .filter((one) => one && one.element && one.element !== "store-link")
      .map((one) => ({ kind: one.element, id: one.id }));
    state.schemaFor = "";
    render();
  }

  // Every other way of selecting means exactly one thing — a click in the panel, a
  // search hit, a problem in the list — and says so by going through here. Only the
  // canvas ever reports more than one.
  function selectOne(sel) {
    state.selected = sel;
    state.multi = [];
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
    selectOne({ kind: "association", id: a.id });
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
  // nothing and no revision is spent on it. A box moved with several others reports
  // every one of them, which is what makes a marquee's drag one change and not four.
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
    if (p.dataset.class) selectOne({ kind: "class", id: p.dataset.class });
    else if (p.dataset.store) selectOne({ kind: "store", id: p.dataset.store });
    else if (p.dataset.assoc) selectOne({ kind: "association", id: p.dataset.assoc });
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
      if (state.selected && local(state.selected.id)) selectOne(null);
      state.multi = state.multi.filter((m) => !local(m.id));
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

  // ---- how wide the panel is -----------------------------------------------
  //
  // 340px is enough for a class with six attributes and not for one with a hundred,
  // where the name is the column that loses the argument — and no amount of column
  // arithmetic makes room that the panel does not have. So the panel is draggable,
  // with the divider the Modeler's panel uses and remembered the same way: a person
  // moves between the two surfaces in one session, and a divider that behaved
  // differently on each would be worse than none.
  //
  // The canvas is told after every change, because a viewport that is not told keeps
  // the width it was built with and draws into space that is no longer there.
  (function wirePanelWidth() {
    const resizer = root.querySelector("#im-resizer");
    const KEY = "atlas.imPanelWidth";
    const DEFAULT_WIDTH = 340;
    const clamp = (w) => Math.max(280, Math.min(900, w));
    const setWidth = (w) => {
      sideEl.style.width = clamp(w) + "px";
      // The same two nudges the Modeler's divider gives its canvas. The class canvas
      // exposes no resized() of its own yet — the drawing keeps working without one,
      // it simply does not re-centre — so the call is optional and the window event
      // is what the library hears today.
      canvas.resized?.();
      window.dispatchEvent(new Event("resize"));
    };

    const saved = parseInt(localStorage.getItem(KEY) || "", 10);
    if (saved) setWidth(saved);

    let startX = 0;
    let startW = 0;
    const onMove = (e) => setWidth(startW - (e.clientX - startX));
    const onUp = () => {
      document.removeEventListener("pointermove", onMove);
      document.removeEventListener("pointerup", onUp);
      resizer.classList.remove("dragging");
      document.body.style.userSelect = "";
      localStorage.setItem(KEY, String(parseInt(sideEl.style.width, 10) || DEFAULT_WIDTH));
    };
    resizer.addEventListener("pointerdown", (e) => {
      e.preventDefault();
      startX = e.clientX;
      startW = sideEl.getBoundingClientRect().width;
      resizer.classList.add("dragging");
      document.body.style.userSelect = "none";
      document.addEventListener("pointermove", onMove);
      document.addEventListener("pointerup", onUp);
    });
    resizer.addEventListener("dblclick", () => {
      setWidth(DEFAULT_WIDTH);
      localStorage.setItem(KEY, String(DEFAULT_WIDTH));
    });
  })();

  saveBtn.addEventListener("click", save);
  // Typing in a field owns its own undo — taking Ctrl+Z away from a half-typed class
  // name to move a box back would be the worst kind of surprise.
  const typing = (el) => Boolean(el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA" ||
    el.tagName === "SELECT" || el.isContentEditable));
  const onKey = (e) => {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
      e.preventDefault();
      if (state.dirty) save();
    }
    if ((e.ctrlKey || e.metaKey) && !typing(document.activeElement)) {
      const key = e.key.toLowerCase();
      if (key === "z" && !e.shiftKey) { e.preventDefault(); canvas.undo(); syncHistoryButtons(); }
      else if ((key === "z" && e.shiftKey) || key === "y") { e.preventDefault(); canvas.redo(); syncHistoryButtons(); }
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
