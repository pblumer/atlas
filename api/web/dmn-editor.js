// The decision editor. It mounts the vendored dmn-js modeler (bpmn.io — the same
// family as the bpmn-js process modeler) as a **page of the Modeler**, so a
// decision is authored, addressed and left the way a BPMN diagram and a form are
// (ADR-draft-the-decision-editor-is-a-page).
//
// It used to be a modal overlay. That fitted what a decision was under
// ADR-0062 — a reference to a model file some process happened to use, stepped
// into from the business-rule-task picker and stepped back out of. ADR-0319 made a
// decision a durable, versioned runtime artifact published in its own right, and an
// artifact of that standing needs an address, a back button, and the same chrome as
// its siblings. The chrome here is form-editor.js's, field for field.
//
// On save the model XML is stored through the DMN upload endpoint and a reference
// is created (or overwritten in place when editing), so the business-rule-task
// picker lists the decision and adopts its inputs and output — the ADR-0062 flow,
// unchanged. Authoring the FEEL and the decision logic is still dmn-js's job;
// Atlas only stores what it produces and evaluates it through temis.

// Only the editor stylesheets we actually use are loaded, lazily, so non-editor
// pages stay light — same discipline as the bpmn-js loader.
const DMN_CSS = [
  "vendor/dmn/assets/diagram-js.css",
  "vendor/dmn/assets/dmn-js-shared.css",
  "vendor/dmn/assets/dmn-js-drd.css",
  "vendor/dmn/assets/dmn-js-decision-table.css",
  "vendor/dmn/assets/dmn-js-decision-table-controls.css",
  "vendor/dmn/assets/dmn-js-literal-expression.css",
  "vendor/dmn/assets/dmn-font/css/dmn.css",
  "vendor/dmn/assets/properties-panel.css",
];

let dmnReady; // memoized loader promise → window.AtlasDmn (Modeler + properties panel)
function loadDmn() {
  if (dmnReady) return dmnReady;
  dmnReady = new Promise((resolve, reject) => {
    for (const href of DMN_CSS) {
      if (document.querySelector(`link[href="${href}"]`)) continue;
      const l = document.createElement("link");
      l.rel = "stylesheet";
      l.href = href;
      document.head.appendChild(l);
    }
    const s = document.createElement("script");
    s.src = "vendor/dmn/dmn-modeler.js";
    // The bundle exposes the Modeler *and* the properties-panel modules together
    // (they must share one dmn-js instance) under window.AtlasDmn.
    s.onload = () => (window.AtlasDmn ? resolve(window.AtlasDmn) : reject(new Error("dmn bundle did not expose AtlasDmn")));
    s.onerror = () => reject(new Error("failed to load the DMN modeler assets"));
    document.head.appendChild(s);
  });
  return dmnReady;
}

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

const rid = () => Math.random().toString(36).slice(2, 10);

// DEFAULT_DECISION_NAME names a decision whose own name could not be read back out
// of the saved XML — a model with no <decision> yet, which dmn-js allows while the
// DRG is being drawn.
const DEFAULT_DECISION_NAME = "Decision";

// ADOPT_KEY holds what a decision authored *for* a business rule task left behind
// for the BPMN editor to pick up on the way back (see stashAdoption). sessionStorage
// rather than a module variable, because the way back is a navigation and the author
// may well reload on it.
const ADOPT_KEY = "atlas.dmn.adopt";

// takeAdoption returns the pending adoption for a process, and clears it. One-shot
// on purpose: adopting is something the return trip does once, not something that
// happens again every time that diagram is opened.
export function takeAdoption(processId) {
  let raw = null;
  try { raw = sessionStorage.getItem(ADOPT_KEY); } catch { return null; }
  if (!raw) return null;
  let rec = null;
  try { rec = JSON.parse(raw); } catch { rec = null; }
  try { sessionStorage.removeItem(ADOPT_KEY); } catch { /* best-effort */ }
  if (!rec || !rec.elementId) return null;
  // A stash left by a trip to a *different* diagram is not this diagram's to apply.
  if (processId && rec.processId && rec.processId !== processId) return null;
  return rec;
}

// stashAdoption records what was just authored so the business rule task it was
// authored for can adopt it when the author navigates back. It carries the decision
// name (which is its id, the string a calledDecision names) and the model handle, so
// the BPMN editor can find it in the catalog either way.
function stashAdoption(forTask, name, modelRef) {
  if (!forTask || !forTask.elementId) return;
  try {
    sessionStorage.setItem(ADOPT_KEY, JSON.stringify({
      processId: forTask.processId || "", elementId: forTask.elementId, name, modelRef,
    }));
  } catch { /* a browser refusing session storage just costs the auto-adopt */ }
}

// seedDmnXml is the starter model for a brand-new decision: one input data node
// feeding one decision with a decision table (one input column reading that input,
// one output column, one empty rule). Atlas derives a decision's inputs from the
// input-data nodes wired into it (ADR-0039), so the seed models a real DRG — a
// first save already adopts the "input" input and "result" output. The DMN 1.3
// namespace is one temis compiles (it appears in the engine's own fixtures).
function seedDmnXml() {
  const r = rid();
  return `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" xmlns:dmndi="https://www.omg.org/spec/DMN/20191111/DMNDI/" xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/" xmlns:di="http://www.omg.org/spec/DMN/20180521/DI/" id="Definitions_${r}" name="Decision" namespace="http://atlas/dmn">
  <inputData id="InputData_${r}" name="input" />
  <decision id="Decision_${r}" name="Decision">
    <informationRequirement id="Requirement_${r}">
      <requiredInput href="#InputData_${r}" />
    </informationRequirement>
    <decisionTable id="DecisionTable_${r}" hitPolicy="UNIQUE">
      <input id="Input_${r}" label="input">
        <inputExpression id="InputExpression_${r}" typeRef="string">
          <text>input</text>
        </inputExpression>
      </input>
      <output id="Output_${r}" name="result" typeRef="string" />
      <rule id="Rule_${r}">
        <inputEntry id="InputEntry_${r}"><text></text></inputEntry>
        <outputEntry id="OutputEntry_${r}"><text></text></outputEntry>
      </rule>
    </decisionTable>
  </decision>
  <dmndi:DMNDI>
    <dmndi:DMNDiagram id="DMNDiagram_${r}">
      <dmndi:DMNShape id="DMNShape_Decision_${r}" dmnElementRef="Decision_${r}">
        <dc:Bounds height="80" width="180" x="320" y="100" />
      </dmndi:DMNShape>
      <dmndi:DMNShape id="DMNShape_InputData_${r}" dmnElementRef="InputData_${r}">
        <dc:Bounds height="45" width="125" x="347" y="280" />
      </dmndi:DMNShape>
      <dmndi:DMNEdge id="DMNEdge_${r}" dmnElementRef="Requirement_${r}">
        <di:waypoint x="409" y="280" />
        <di:waypoint x="410" y="180" />
      </dmndi:DMNEdge>
    </dmndi:DMNDiagram>
  </dmndi:DMNDI>
</definitions>`;
}

// firstDecisionName reads the decision name straight out of the DMN XML, so the
// stored model handle and the reference name track what the author called the
// decision — no dependency on dmn-js internals.
function firstDecisionName(xml) {
  try {
    const doc = new DOMParser().parseFromString(xml, "application/xml");
    const dec = doc.querySelector("decision");
    return (dec && dec.getAttribute("name")) || "";
  } catch {
    return "";
  }
}

// viewLabel names a dmn-js view for the tab strip: the DRG overview, or a
// decision's own table/expression editor.
function viewLabel(v) {
  if (v.type === "drd") return "Overview (DRG)";
  return (v.element && v.element.name) || (v.element && v.element.id) || "Decision";
}

// keepCaretOnRewrite works around an upstream dmn-js bug (17.x). The DRD "definition
// properties" widget (the editable model name/id at the top-left of the DRG view)
// rewrites its contenteditable's textContent on *every* committed model change —
// including while you are typing into it. Reassigning textContent collapses the
// caret to offset 0, so each debounced keystroke lands at the start and the text
// comes out reversed ("dec" → "ced"). We patch the node's textContent setter so a
// rewrite performed while the node has focus preserves the caret's character
// offset instead of dropping it to 0. Idempotent per node (nodes are recreated on
// view switches, so this runs again for each fresh one).
function keepCaretOnRewrite(el) {
  if (el.__caretPatched) return;
  el.__caretPatched = true;
  const desc = Object.getOwnPropertyDescriptor(Node.prototype, "textContent");
  Object.defineProperty(el, "textContent", {
    configurable: true,
    get() { return desc.get.call(this); },
    set(v) {
      if (document.activeElement !== this) { desc.set.call(this, v); return; }
      const offset = caretOffset(this);
      desc.set.call(this, v);
      restoreCaret(this, offset);
    },
  });
}

// caretOffset returns the caret's position as a character offset within el (a
// plain-text contenteditable), or null when el holds no selection.
function caretOffset(el) {
  const sel = window.getSelection();
  if (!sel || sel.rangeCount === 0 || !el.contains(sel.anchorNode)) return null;
  const r = sel.getRangeAt(0).cloneRange();
  const pre = document.createRange();
  pre.selectNodeContents(el);
  pre.setEnd(r.endContainer, r.endOffset);
  return pre.toString().length;
}

// restoreCaret places the caret at character offset within el (clamped to its
// text length), so a programmatic textContent rewrite doesn't move it.
function restoreCaret(el, offset) {
  if (offset == null) return;
  const text = el.firstChild;
  const len = (el.textContent || "").length;
  const pos = Math.min(offset, len);
  const r = document.createRange();
  if (text && text.nodeType === 3) r.setStart(text, pos);
  else r.setStart(el, 0);
  r.collapse(true);
  const sel = window.getSelection();
  sel.removeAllRanges();
  sel.addRange(r);
}

let current; // active session handle, torn down on remount/leave
// generation is bumped by cleanup() on every remount/navigation. mountDmnEditor
// captures it after cleanup() and re-checks after each await, so a mount a newer
// navigation has superseded bails before it instantiates dmn-js (and its listeners)
// into a detached container and leaks them — the guard editor.js and form-editor.js
// both use.
let generation = 0;

export function cleanup() {
  generation++;
  if (current) { try { current.destroy(); } catch { /* ignore */ } current = null; }
}

// mountDmnEditor renders the decision editor into root.
//
//   refId     — edit the decision this DMN reference points at; absent means a new one
//   projectId — the application a new decision is filed into
//   forTask   — {processId, elementId} when the editor was reached by pressing
//               "＋ New decision" on a business rule task. It decides where the back
//               link goes and makes a successful save leave an adoption behind for
//               that task (ADR-draft-the-decision-editor-is-a-page), so the round trip
//               wires the task exactly as the overlay used to.
export async function mountDmnEditor(root, { api, toast, refId, projectId, forTask }) {
  cleanup();
  const gen = generation;
  // Claim the shared cleanup slot so navigating away tears this editor down (the
  // BPMN and form editors reclaim it the same way when they mount).
  window.__atlasCleanup = cleanup;

  const forSuffix = forTask && forTask.elementId
    ? "/for/" + encodeURIComponent(forTask.processId || "") + "/" + encodeURIComponent(forTask.elementId)
    : "";

  root.innerHTML = `
    <div class="editor dmn-editor">
      <div class="editor-bar">
        <a class="crumbs" id="dmn-back" href="#/modeler">&larr; Modeler</a>
        <div class="etabs" id="dmn-views"></div>
        <span class="chip" id="dmn-ref-chip" hidden></span>
        <div style="flex:1"></div>
        <span class="muted" id="dmn-status"></span>
        <button class="btn" id="dmn-save" title="Save this decision">Save</button>
      </div>
      <div class="editor-body dmn-body">
        <div class="dmn-canvas"></div>
        <div class="dmn-props"></div>
      </div>
      <div class="dmn-hint muted">Model the decision table. <b>Input Data</b> nodes become the
        decision's inputs and the output column becomes its result variable — both are adopted
        into a business rule task that calls this decision.</div>
    </div>`;

  const canvas = root.querySelector(".dmn-canvas");
  const viewsBar = root.querySelector("#dmn-views");
  const propsPanel = root.querySelector(".dmn-props");
  const statusEl = root.querySelector("#dmn-status");
  const chip = root.querySelector("#dmn-ref-chip");
  const saveBtn = root.querySelector("#dmn-save");
  const backEl = root.querySelector("#dmn-back");

  // ---- identity ------------------------------------------------------------
  // What this session is editing: the reference record (once it exists) and the
  // model handle behind it. A new decision has neither until its first save.
  let ref = null;            // the dmnRef record, when editing an existing decision
  let modelRef = "";         // the model handle the save overwrites in place
  let project = projectId || "";

  if (refId) {
    try {
      const refs = (await api("GET", "/api/v1/dmnrefs")) || [];
      if (gen !== generation) return; // a newer navigation landed during the fetch
      ref = refs.find((r) => r.id === refId) || null;
    } catch { /* fall through: reported as "could not load" below */ }
    if (!ref || !ref.modelRef) {
      root.innerHTML = `<div class="card empty"><h1>Decision not found</h1>` +
        `<p class="muted">This decision reference no longer exists, or its model is not editable here.</p>` +
        `<p><a class="btn" href="#/modeler">Back to the Modeler</a></p></div>`;
      return;
    }
    modelRef = ref.modelRef;
    project = ref.projectId || project;
  }

  // Where back goes: to the diagram when this decision is being authored for one of
  // its tasks, otherwise to the owning application, otherwise the Modeler home. The
  // application's name is resolved after the fact, so the link works immediately and
  // gets its label when the answer arrives — the form editor's behaviour.
  if (forTask && forTask.processId) {
    backEl.href = "#/modeler/draft/" + encodeURIComponent(forTask.processId);
    backEl.innerHTML = "&larr; Process";
  } else if (project) {
    backEl.href = `#/modeler/p/${encodeURIComponent(project)}`;
    backEl.innerHTML = "&larr; Application";
    (async () => {
      try {
        const projects = (await api("GET", "/api/v1/applications")) || [];
        const p = projects.find((x) => x.id === project);
        if (p && root.querySelector("#dmn-back") === backEl) backEl.innerHTML = `&larr; ${esc(p.name)}`;
      } catch { /* best-effort: the generic "Application" label still links correctly */ }
    })();
  }

  const showHandle = () => {
    if (!modelRef) { chip.hidden = true; return; }
    chip.hidden = false;
    chip.textContent = modelRef + ".dmn";
  };
  showHandle();

  // ---- the modeler ---------------------------------------------------------
  let modeler;
  // The definition-properties name/id fields are created (and recreated on view
  // switches) by dmn-js inside the canvas; patch each one as it appears so the
  // caret survives dmn-js's textContent rewrites (see keepCaretOnRewrite).
  const patchCaretFields = () =>
    canvas.querySelectorAll(".dmn-definitions-name, .dmn-definitions-id").forEach(keepCaretOnRewrite);
  const caretObserver = new MutationObserver(patchCaretFields);
  caretObserver.observe(canvas, { childList: true, subtree: true });

  current = {
    destroy() {
      caretObserver.disconnect();
      try { modeler && modeler.destroy(); } catch { /* already gone */ }
      modeler = null;
    },
  };

  try {
    const AtlasDmn = await loadDmn();
    if (gen !== generation) return; // superseded while the 1.3 MB bundle loaded
    // The properties panel is a DRG-view feature (it edits the decision/input-data
    // elements of the requirements graph): Name, ID, Version tag, Documentation and
    // the output Variable — the same panel Camunda's Modeler shows. It lives on the
    // `drd` editor and renders into propsPanel. The camunda moddle extension makes
    // the versionTag (and other camunda:* attributes) readable and writable; temis
    // ignores that namespace, so a saved model still compiles.
    modeler = new AtlasDmn.DmnJS({
      container: canvas,
      drd: {
        propertiesPanel: { parent: propsPanel },
        additionalModules: [
          AtlasDmn.DmnPropertiesPanelModule,
          AtlasDmn.DmnPropertiesProviderModule,
          AtlasDmn.CamundaPropertiesProviderModule,
        ],
      },
      moddleExtensions: { camunda: AtlasDmn.CamundaModdleDescriptor },
    });

    // The tab strip moves between the DRG overview and each decision's table without
    // hunting for a double-click. It is the .etabs strip the BPMN and form editors
    // use, so a tab is a tab everywhere in the Modeler. The properties panel only
    // applies to the DRG view, so its column shows only there.
    const renderViews = () => {
      const views = modeler.getViews();
      const active = modeler.getActiveView();
      viewsBar.innerHTML = "";
      for (const v of views) {
        const b = document.createElement("button");
        b.type = "button";
        b.className = active && v.id === active.id ? "active" : "";
        b.textContent = viewLabel(v);
        b.addEventListener("click", () => modeler.open(v).catch(() => {}));
        viewsBar.appendChild(b);
      }
      propsPanel.hidden = !(active && active.type === "drd");
    };
    modeler.on("views.changed", renderViews);

    let xml;
    if (modelRef) {
      xml = await api("GET", "/api/v1/dmn-models/" + encodeURIComponent(modelRef) + "/xml");
      if (gen !== generation) return;
      if (typeof xml !== "string") throw new Error("could not load the model XML");
    } else {
      xml = seedDmnXml();
    }
    await modeler.importXML(xml);
    if (gen !== generation) return;
    renderViews();
    patchCaretFields();
    // An edit is no longer "unsaved" the moment it is typed the way a diagram is, so
    // the status line starts empty and says something only after a save.
    modeler.on("views.changed", () => { statusEl.textContent = ""; });
  } catch (e) {
    if (gen !== generation) return;
    root.innerHTML = `<div class="card empty"><h1>Could not open the decision editor</h1>` +
      `<p class="muted">${esc(e.message)}</p>` +
      `<p><a class="btn" href="#/modeler">Back to the Modeler</a></p></div>`;
    return;
  }

  // ---- save ----------------------------------------------------------------
  // Save stores the model and, for a decision that does not have one yet, creates the
  // reference that files it under an application. It stays on the page and moves the
  // URL onto the edit route, so a second Save updates this decision rather than
  // creating a second one — the form editor's behaviour, for the same reason.
  async function save() {
    saveBtn.disabled = true;
    statusEl.textContent = "Saving…";
    try {
      const out = await modeler.saveXML({ format: true });
      const savedXml = out.xml;
      const name = firstDecisionName(savedXml) || DEFAULT_DECISION_NAME;
      const q = modelRef
        ? "?handle=" + encodeURIComponent(modelRef)
        : "?name=" + encodeURIComponent(name);
      const up = await api("POST", "/api/v1/dmn-models" + q, savedXml, true);
      modelRef = up.modelRef;
      if (!ref) {
        ref = await api("POST", "/api/v1/dmnrefs", { name, modelRef, projectId: project || "" });
        // The URL now addresses a stored decision, so the next save updates it and a
        // reload comes back to what was just written.
        history.replaceState(null, "", "#/modeler/dmn/e/" + encodeURIComponent(ref.id) + forSuffix);
      } else if ((ref.name || "") !== name) {
        // Editing keeps the handle, so only the display name can drift: an in-editor
        // rename is mirrored onto the reference rather than leaving the Explorer
        // showing a name the model no longer carries.
        try {
          await api("PATCH", "/api/v1/dmnrefs/" + encodeURIComponent(ref.id), { name });
          ref.name = name;
        } catch { /* the model is saved; a stale label is not worth failing the save */ }
      }
      showHandle();
      stashAdoption(forTask, name, modelRef);
      statusEl.textContent = "Saved";
      toast && toast(`Decision “${name}” saved`, "ok");
    } catch (e) {
      statusEl.textContent = "";
      toast && toast("Save failed: " + e.message, "err");
    } finally {
      saveBtn.disabled = false;
    }
  }
  saveBtn.addEventListener("click", save);
}
