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
];

let dmnReady; // memoized loader promise → window.DmnJS (the dmn-js Modeler)
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
    s.onload = () => (window.DmnJS ? resolve(window.DmnJS) : reject(new Error("dmn-js did not expose DmnJS")));
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
        <button class="btn ghost" data-act="cancel">Abbrechen</button>
        <button class="btn" data-act="save">Speichern &amp; übernehmen</button>
      </div>
      <div class="dmn-canvas"></div>
      <div class="dmn-hint muted">Modelliere die Entscheidungstabelle. <b>Input Data</b>-Knoten
        werden zu den Decision-Inputs; die Output-Spalte wird zur Result-Variable — beide werden
        beim Speichern automatisch in den Business-Rule-Task übernommen.</div>
    </div>`;
  document.body.appendChild(overlay);

  const canvas = overlay.querySelector(".dmn-canvas");
  const viewsBar = overlay.querySelector(".dmn-views");

  let modeler;
  let done;
  const finish = (result) => {
    if (done) return;
    done = true;
    document.removeEventListener("keydown", onKey);
    try { modeler && modeler.destroy(); } catch { /* already gone */ }
    overlay.remove();
    resolve(result);
  };
  let resolve;
  const promise = new Promise((r) => (resolve = r));
  const onKey = (e) => { if (e.key === "Escape") finish(null); };
  document.addEventListener("keydown", onKey);

  try {
    const DmnJS = await loadDmn();
    modeler = new DmnJS({ container: canvas });

    // The view switcher lets the author move between the DRG overview and each
    // decision's table without hunting for a double-click.
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
