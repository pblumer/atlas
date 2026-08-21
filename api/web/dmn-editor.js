// Embedded DMN editor (ADR-0062). Atlas used to ship only a read-only DMN viewer,
// delegating all authoring to temis (ADR-0014). This module reverses that for the
// decision-table case: it mounts the vendored dmn-js modeler (bpmn.io — the same
// family as the bpmn-js process modeler) in a full-screen overlay so a decision is
// authored *in Atlas*. On save the model XML is stored through the DMN upload
// endpoint and a project reference is created (or overwritten in place when
// editing), so the business-rule-task picker lists the new decision and adopts its
// inputs and output — the "create a DMN model directly, inputs/outputs taken over
// automatically" flow. Authoring the FEEL/decision logic itself is still dmn-js;
// Atlas only stores and evaluates (temis) what dmn-js produces.

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

const rid = () => Math.random().toString(36).slice(2, 10);

// seedDmnXml is the starter model for a brand-new decision: one input data node
// feeding one decision with a decision table (one input column reading that input,
// one output column, one empty rule). Atlas derives a decision's inputs from the
// input-data nodes wired into it (ADR-0039), so the seed models a real DRG — a
// first save already adopts the "eingabe" input and "ergebnis" result. The DMN 1.3
// namespace is one temis compiles (it appears in the engine's own fixtures).
function seedDmnXml() {
  const r = rid();
  return `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns="https://www.omg.org/spec/DMN/20191111/MODEL/" xmlns:dmndi="https://www.omg.org/spec/DMN/20191111/DMNDI/" xmlns:dc="http://www.omg.org/spec/DMN/20180521/DC/" xmlns:di="http://www.omg.org/spec/DMN/20180521/DI/" id="Definitions_${r}" name="Entscheidung" namespace="http://atlas/dmn">
  <inputData id="InputData_${r}" name="eingabe" />
  <decision id="Decision_${r}" name="Entscheidung">
    <informationRequirement id="Requirement_${r}">
      <requiredInput href="#InputData_${r}" />
    </informationRequirement>
    <decisionTable id="DecisionTable_${r}" hitPolicy="UNIQUE">
      <input id="Input_${r}" label="eingabe">
        <inputExpression id="InputExpression_${r}" typeRef="string">
          <text>eingabe</text>
        </inputExpression>
      </input>
      <output id="Output_${r}" name="ergebnis" typeRef="string" />
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

// viewLabel names a dmn-js view for the overlay's view switcher: the DRD overview,
// or a decision's own table/expression editor.
function viewLabel(v) {
  if (v.type === "drd") return "Übersicht (DRG)";
  const name = (v.element && v.element.name) || (v.element && v.element.id) || "Entscheidung";
  return name;
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

// openDmnEditor mounts the dmn-js modeler in a modal overlay and resolves with the
// stored model once the author saves, or null if they cancel. When `modelRef` is
// given the existing model is loaded and overwritten in place on save (editing);
// otherwise a seed model is authored and a new reference is created under
// `projectId`. It needs `api` (and `toast`) from the caller so this module stays
// decoupled from app.js, matching how the bpmn editor is wired.
export async function openDmnEditor({ api, toast, projectId, modelRef }) {
  const editing = !!modelRef;
  const overlay = document.createElement("div");
  overlay.className = "dmn-overlay";
  overlay.innerHTML = `
    <div class="dmn-modal">
      <div class="dmn-modal-head">
        <strong>${editing ? "Decision bearbeiten" : "Neue Decision"}</strong>
        <div class="dmn-views"></div>
        <span style="flex:1"></span>
        <button class="btn ghost" data-act="cancel" title="Close without saving">Abbrechen</button>
        <button class="btn" data-act="save" title="Save the decision and apply it">Speichern &amp; übernehmen</button>
      </div>
      <div class="dmn-body">
        <div class="dmn-canvas"></div>
        <div class="dmn-props"></div>
      </div>
      <div class="dmn-hint muted">Modelliere die Entscheidungstabelle. <b>Input Data</b>-Knoten
        werden zu den Decision-Inputs; die Output-Spalte wird zur Result-Variable — beide werden
        beim Speichern automatisch in den Business-Rule-Task übernommen.</div>
    </div>`;
  document.body.appendChild(overlay);

  const canvas = overlay.querySelector(".dmn-canvas");
  const viewsBar = overlay.querySelector(".dmn-views");
  const propsPanel = overlay.querySelector(".dmn-props");

  let modeler;
  let done;
  // The definition-properties name/id fields are created (and recreated on view
  // switches) by dmn-js inside the canvas; patch each one as it appears so the
  // caret survives dmn-js's textContent rewrites (see keepCaretOnRewrite).
  const patchCaretFields = () =>
    canvas.querySelectorAll(".dmn-definitions-name, .dmn-definitions-id").forEach(keepCaretOnRewrite);
  const caretObserver = new MutationObserver(patchCaretFields);
  caretObserver.observe(canvas, { childList: true, subtree: true });
  const finish = (result) => {
    if (done) return;
    done = true;
    document.removeEventListener("keydown", onKey);
    caretObserver.disconnect();
    try { modeler && modeler.destroy(); } catch { /* already gone */ }
    overlay.remove();
    resolve(result);
  };
  let resolve;
  const promise = new Promise((r) => (resolve = r));
  const onKey = (e) => { if (e.key === "Escape") finish(null); };
  document.addEventListener("keydown", onKey);

  try {
    const AtlasDmn = await loadDmn();
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

    // The view switcher lets the author move between the DRG overview and each
    // decision's table without hunting for a double-click. The properties panel
    // only applies to the DRG view, so its column is shown only there.
    const renderViews = () => {
      const views = modeler.getViews();
      const active = modeler.getActiveView();
      viewsBar.innerHTML = "";
      for (const v of views) {
        const b = document.createElement("button");
        b.className = "dmn-view-tab" + (active && v.id === active.id ? " active" : "");
        b.textContent = viewLabel(v);
        b.addEventListener("click", () => modeler.open(v).catch(() => {}));
        viewsBar.appendChild(b);
      }
      propsPanel.hidden = !(active && active.type === "drd");
    };
    modeler.on("views.changed", renderViews);

    let xml;
    if (editing) {
      xml = await api("GET", "/api/v1/dmn-models/" + encodeURIComponent(modelRef) + "/xml");
      if (typeof xml !== "string") throw new Error("could not load the model XML");
    } else {
      xml = seedDmnXml();
    }
    await modeler.importXML(xml);
    renderViews();
    patchCaretFields();

    overlay.querySelector('[data-act="cancel"]').addEventListener("click", () => finish(null));
    const saveBtn = overlay.querySelector('[data-act="save"]');
    saveBtn.addEventListener("click", async () => {
      saveBtn.disabled = true;
      try {
        const out = await modeler.saveXML({ format: true });
        const savedXml = out.xml;
        const name = firstDecisionName(savedXml) || "Entscheidung";
        const q = editing
          ? "?handle=" + encodeURIComponent(modelRef)
          : "?name=" + encodeURIComponent(name);
        const up = await api("POST", "/api/v1/dmn-models" + q, savedXml, true);
        if (!editing) {
          await api("POST", "/api/v1/dmnrefs", { name, modelRef: up.modelRef, projectId: projectId || "" });
        }
        toast && toast(editing ? `Decision "${name}" gespeichert` : `Decision "${name}" erstellt`, "ok");
        finish({ modelRef: up.modelRef, modelName: up.modelName, decisions: up.decisions || [], name });
      } catch (e) {
        saveBtn.disabled = false;
        toast && toast("Speichern fehlgeschlagen: " + e.message, "err");
      }
    });
  } catch (e) {
    toast && toast("DMN-Editor konnte nicht geladen werden: " + e.message, "err");
    finish(null);
  }
  return promise;
}
