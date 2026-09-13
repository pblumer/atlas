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
// Save keeps a **draft** and nothing else: the model a reference resolves is
// written only by "Save to model" (ADR-draft-decision-drafts). That is the BPMN
// editor's grammar — its Save is a draft too — and it is what makes pressing Save
// safe: a half-written decision can no longer refuse a colleague's publish of the
// same application, or offer its half-named output to the next business rule task.
//
// Writing the model creates or updates the reference, so the business-rule-task
// picker lists the decision and adopts its inputs and output — the ADR-0062 flow,
// unchanged, and still the step that completes the round trip from a task.
// Authoring the FEEL and the decision logic is still dmn-js's job; Atlas only
// stores what it produces and evaluates it through temis.

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
//   draftId   — open this decision draft: work that has never been written to the
//               model, so it has no reference to be addressed by
//               (ADR-draft-decision-drafts)
//   projectId — the application a new decision is filed into
//   forTask   — {processId, elementId} when the editor was reached by pressing
//               "＋ New decision" on a business rule task. It decides where the back
//               link goes and makes a successful model save leave an adoption behind
//               for that task (ADR-draft-the-decision-editor-is-a-page), so the round
//               trip wires the task exactly as the overlay used to.
export async function mountDmnEditor(root, { api, toast, refId, draftId, projectId, forTask }) {
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
        <span class="chip draft-chip" id="dmn-draft-chip" hidden title="This decision has work that has not been written to the model yet — nothing else can see it">Draft</span>
        <div style="flex:1"></div>
        <span class="muted" id="dmn-status"></span>
        <button class="btn neutral" id="dmn-discard" hidden title="Throw away the draft and go back to the stored model">Discard draft</button>
        <button class="btn neutral" id="dmn-save" title="Save this decision as a draft. Nothing that reads this decision changes until you save it to the model.">Save</button>
        <button class="btn" id="dmn-save-model" title="Write this into the decision model every process and application resolves — this is what the next Publish ships.">Save to model</button>
      </div>
      <div class="editor-body dmn-body">
        <div class="dmn-canvas"></div>
        <div class="dmn-props"></div>
      </div>
      <div class="dmn-hint muted">Model the decision table. <b>Input Data</b> nodes become the
        decision's inputs and the output column becomes its result variable — both are adopted
        into a business rule task that calls this decision. <b>Save</b> keeps a draft only you
        see; <b>Save to model</b> writes the decision every process resolves, and is what the
        next Publish ships.</div>
    </div>`;

  const canvas = root.querySelector(".dmn-canvas");
  const viewsBar = root.querySelector("#dmn-views");
  const propsPanel = root.querySelector(".dmn-props");
  const statusEl = root.querySelector("#dmn-status");
  const chip = root.querySelector("#dmn-ref-chip");
  const draftChip = root.querySelector("#dmn-draft-chip");
  const saveBtn = root.querySelector("#dmn-save");
  const modelBtn = root.querySelector("#dmn-save-model");
  const discardBtn = root.querySelector("#dmn-discard");
  const backEl = root.querySelector("#dmn-back");

  // ---- identity ------------------------------------------------------------
  // What this session is editing, across the three layers a decision has
  // (ADR-draft-decision-drafts): the draft it is keeping, the reference it is in the
  // model under, and the model handle behind that. A brand-new decision has none of
  // them until it is first saved.
  let ref = null;            // the dmnRef record, when the decision is in the model
  let modelRef = "";         // the model handle "Save to model" writes
  let draft = null;          // the draft record this session writes, once it has one
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

  // A draft is keyed by the decision's reference id, so opening a decision that has
  // unsaved work opens that work rather than the model it has not been written to.
  // A draft addressed directly (#/modeler/dmn/d/…) is one for a decision that is not
  // in the model at all, so it is the only place its content exists.
  const wantDraft = draftId || (ref ? ref.id : "");
  if (wantDraft) {
    try {
      const drafts = (await api("GET", "/api/v1/dmn-drafts")) || [];
      if (gen !== generation) return;
      draft = drafts.find((d) => d.id === wantDraft) || null;
    } catch { /* no draft listing is the same as no draft: the model still opens */ }
    if (draftId && !draft) {
      root.innerHTML = `<div class="card empty"><h1>Decision draft not found</h1>` +
        `<p class="muted">This decision draft no longer exists. It may have been written to the model, or discarded.</p>` +
        `<p><a class="btn" href="#/modeler">Back to the Modeler</a></p></div>`;
      return;
    }
    if (draft) {
      modelRef = draft.modelRef || modelRef;
      project = draft.projectId || project;
    }
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
    chip.hidden = !modelRef;
    if (modelRef) chip.textContent = modelRef + ".dmn";
    // The draft chip is the one thing on the bar that says "what you are looking at
    // is not what anything else resolves yet".
    draftChip.hidden = !draft;
    discardBtn.hidden = !draft;
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

    // What opens: the author's draft if there is one — it is the newer work and the
    // only copy of it — else the stored model, else the seed for a decision that
    // does not exist yet.
    let xml;
    if (draft) {
      xml = await api("GET", "/api/v1/dmn-drafts/" + encodeURIComponent(draft.id) + "/xml");
      if (gen !== generation) return;
      if (typeof xml !== "string") throw new Error("could not load the draft XML");
    } else if (modelRef) {
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
    // The status line says what a *save* just did, so it starts empty and is cleared
    // by anything else. That a draft is open is a standing fact rather than an event,
    // so the chip in the bar says that instead.
    modeler.on("views.changed", () => { statusEl.textContent = ""; });
  } catch (e) {
    if (gen !== generation) return;
    root.innerHTML = `<div class="card empty"><h1>Could not open the decision editor</h1>` +
      `<p class="muted">${esc(e.message)}</p>` +
      `<p><a class="btn" href="#/modeler">Back to the Modeler</a></p></div>`;
    return;
  }

  // ---- saving --------------------------------------------------------------
  // Two acts, two buttons (ADR-draft-decision-drafts). Save keeps a draft: the
  // author's work, which nothing else resolves. Save to model writes the handle
  // every reference, every picker and the next Publish resolve — and clears the
  // draft, because a draft exists only while it differs from the model.

  // currentXml is what dmn-js has now, plus the decision name read back out of it.
  async function currentXml() {
    const out = await modeler.saveXML({ format: true });
    return { xml: out.xml, name: firstDecisionName(out.xml) || DEFAULT_DECISION_NAME };
  }

  // busy runs one save at a time and reports it on the status line, so a second
  // click cannot race the first.
  async function busy(label, fn) {
    saveBtn.disabled = modelBtn.disabled = discardBtn.disabled = true;
    statusEl.textContent = label;
    try {
      await fn();
    } finally {
      saveBtn.disabled = modelBtn.disabled = discardBtn.disabled = false;
    }
  }

  async function saveDraft() {
    await busy("Saving…", async () => {
      try {
        const { xml, name } = await currentXml();
        const saved = await api("POST", "/api/v1/dmn-drafts", {
          id: draft ? draft.id : (ref ? ref.id : ""),
          refId: ref ? ref.id : "",
          modelRef,
          projectId: project || "",
          xml,
        });
        draft = saved;
        showHandle();
        // A draft on a decision that is not in the model is addressed by the draft;
        // one on a decision that is keeps the decision's own address.
        if (!ref) {
          history.replaceState(null, "", "#/modeler/dmn/d/" + encodeURIComponent(saved.id) + forSuffix);
        }
        statusEl.textContent = forTask && forTask.elementId
          ? "Draft saved — save to the model to wire the task"
          : "Draft saved";
        toast && toast(`Decision “${name}” saved as a draft`, "ok");
      } catch (e) {
        statusEl.textContent = "";
        toast && toast("Save failed: " + e.message, "err");
      }
    });
  }

  // saveToModel writes the model and, for a decision that is not in it yet, creates
  // the reference that files it under an application. It stays on the page and moves
  // the URL onto the edit route, so the next save addresses this decision rather
  // than making a second one — the form editor's behaviour, for the same reason.
  async function saveToModel() {
    await busy("Saving to the model…", async () => {
      try {
        const { xml, name } = await currentXml();
        let up;
        try {
          up = await uploadModel(xml, name, false);
        } catch (e) {
          // ADR-0222: a handle another decision already holds is refused rather than
          // forked into a second copy under a name nobody chose. Replacing it is the
          // author's to decide, so it is asked here and sent as a deliberate act.
          if (e.status !== 409) throw e;
          if (!window.confirm(`${e.message}\n\nReplace that model with this decision?`)) {
            statusEl.textContent = "";
            return;
          }
          up = await uploadModel(xml, name, true);
        }
        modelRef = up.modelRef;
        if (!ref) {
          ref = await api("POST", "/api/v1/dmnrefs", { name, modelRef, projectId: project || "" });
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
        await dropDraft();
        showHandle();
        stashAdoption(forTask, name, modelRef);
        statusEl.textContent = "Saved to the model";
        toast && toast(`Decision “${name}” saved to the model`, "ok");
      } catch (e) {
        statusEl.textContent = "";
        toast && toast("Save failed: " + e.message, "err");
      }
    });
  }

  // uploadModel stores the XML under the decision's handle. ?handle= updates the
  // model this session opened; otherwise ?from= makes the upload identity-aware, so
  // a handle something else holds comes back 409 instead of silently becoming
  // "eligibility-2" (ADR-0222).
  function uploadModel(xml, name, overwrite) {
    const q = modelRef
      ? "?handle=" + encodeURIComponent(modelRef)
      : "?name=" + encodeURIComponent(name) + "&from=" + (overwrite ? "&overwrite=true" : "");
    return api("POST", "/api/v1/dmn-models" + q, xml, true);
  }

  // dropDraft clears the draft once its work is in the model. A failure here is not
  // a failed save — the model has it — so it is reported on the status line rather
  // than thrown: what is left behind is a stale draft saying "unsaved", which the
  // author can discard.
  async function dropDraft() {
    if (!draft) return;
    try {
      await api("DELETE", "/api/v1/dmn-drafts/" + encodeURIComponent(draft.id));
      draft = null;
    } catch {
      toast && toast("Saved to the model, but the draft could not be cleared — discard it when you can", "err");
    }
  }

  async function discardDraft() {
    if (!draft) return;
    const gone = !modelRef
      ? "This decision has never been saved to the model, so discarding the draft deletes it.\n\nContinue?"
      : "Throw away this draft and go back to the model as it is stored?\n\nContinue?";
    if (!window.confirm(gone)) return;
    await busy("Discarding…", async () => {
      try {
        await api("DELETE", "/api/v1/dmn-drafts/" + encodeURIComponent(draft.id));
        draft = null;
        showHandle();
        // Back to whatever the decision is without the draft: its model, or — for a
        // decision that never reached one — the Modeler, since nothing is left.
        location.hash = ref ? "#/modeler/dmn/e/" + encodeURIComponent(ref.id) + forSuffix
          : (project ? "#/modeler/p/" + encodeURIComponent(project) : "#/modeler");
      } catch (e) {
        statusEl.textContent = "";
        toast && toast("Could not discard the draft: " + e.message, "err");
      }
    });
  }

  saveBtn.addEventListener("click", saveDraft);
  modelBtn.addEventListener("click", saveToModel);
  discardBtn.addEventListener("click", discardDraft);
}
