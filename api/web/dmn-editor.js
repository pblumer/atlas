// The decision editor. It mounts the vendored dmn-js modeler (bpmn.io — the same
// family as the bpmn-js process modeler) as a **page of the Modeler**, so a
// decision is authored, addressed and left the way a BPMN diagram and a form are
// (ADR-0320).
//
// It used to be a modal overlay. That fitted what a decision was under
// ADR-0062 — a reference to a model file some process happened to use, stepped
// into from the business-rule-task picker and stepped back out of. ADR-0319 made a
// decision a durable, versioned runtime artifact published in its own right, and an
// artifact of that standing needs an address, a back button, and the same chrome as
// its siblings. The chrome here is form-editor.js's, field for field.
//
// Save keeps a **draft** and nothing else: the model a reference resolves is
// written only by "Save to model" (ADR-0321). That is the BPMN
// editor's grammar — its Save is a draft too — and it is what makes pressing Save
// safe: a half-written decision can no longer refuse a colleague's publish of the
// same application, or offer its half-named output to the next business rule task.
//
// Writing the model creates or updates the reference, so the business-rule-task
// picker lists the decision and adopts its inputs and output — the ADR-0062 flow,
// unchanged, and still the step that completes the round trip from a task.
//
// Deploy is the third verb (ADR-0322), and the only one
// that reaches the engine: it ships what is on screen as a versioned decision
// deployment, through the same durable path an application publish uses. The bar
// therefore shows three buttons answering three different questions — what you are
// keeping, what everything resolves, and what the runtime evaluates — and the chip
// beside them says which version this decision is deployed at, as a BPMN diagram
// opened from a deployment carries its key.
//
// Authoring the FEEL and the decision logic is still dmn-js's job; Atlas only
// stores what it produces and evaluates it through temis.

import { renderTrace, fmtVal as traceValue } from "./dmn-trace.js";
import { collectDecisionDocumentation, exportDecisionDocumentation } from "./decision-doc.js";
import { attachCollab } from "./collab.js";
import { dmnSurface } from "./dmn-collab.js";
import { knowledgeModelFindings } from "./dmn-warnings.js";

// Only the editor stylesheets we actually use are loaded, lazily, so non-editor
// pages stay light — same discipline as the bpmn-js loader.
const DMN_CSS = [
  "vendor/dmn/assets/diagram-js.css",
  "vendor/dmn/assets/dmn-js-shared.css",
  "vendor/dmn/assets/dmn-js-drd.css",
  "vendor/dmn/assets/dmn-js-decision-table.css",
  "vendor/dmn/assets/dmn-js-decision-table-controls.css",
  "vendor/dmn/assets/dmn-js-literal-expression.css",
  // A business knowledge model's logic is *not* the literal-expression view above.
  // dmn-js opens a `dmn:BusinessKnowledgeModel` in its boxed-expression view — a
  // separate component, with its own container class and its own two stylesheets —
  // because a knowledge model is a FEEL *function*: it has a kind, formal
  // parameters and a body, none of which a decision's literal expression has.
  // Without these two the view still renders every one of those parts, and renders
  // them raw: no boxes, no borders, the `F` kind marker and the `()` parameter list
  // as bare text at the page edge, and the edit buttons that should stay hidden
  // until their section is hovered sitting permanently on top of the content.
  // TestEveryDmnViewIsStyled keeps this list honest when the vendored fork gains
  // another view.
  "vendor/dmn/assets/dmn-js-boxed-expression.css",
  "vendor/dmn/assets/dmn-js-boxed-expression-controls.css",
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

// sanitizeFileName turns a decision's name into something a file system will take,
// for the Export XML download. It is a download's label, not an identity: the model
// handle is preferred when there is one, and this is the fallback for a decision
// that has never been saved to one.
const sanitizeFileName = (s) =>
  String(s || "").toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "");

// wireDmnBarMenu opens and closes the bar's overflow menu, and returns a teardown
// for the document listener it installs. It is editor.js's wireBarMenu, for the
// same reason: the Console's delegated handler lives in app.js, and this editor is
// mounted without app.js by the e2e harnesses, so a bar depending on that handler
// would be a bar those harnesses cannot open. Stopping propagation on the trigger
// keeps app.js's document handler — which closes every open menu on any click it
// sees — from closing this one the moment it opens.
function wireDmnBarMenu(root) {
  const toggle = root.querySelector("#dmn-more");
  const menu = root.querySelector("#dmn-menu");
  if (!toggle || !menu) return () => {};
  const setOpen = (open) => {
    menu.hidden = !open;
    toggle.setAttribute("aria-expanded", open ? "true" : "false");
  };
  toggle.addEventListener("click", (e) => {
    e.stopPropagation();
    if (menu.hidden) {
      for (const other of document.querySelectorAll(".dropdown-menu:not([hidden])")) {
        if (other !== menu) other.hidden = true;
      }
    }
    setOpen(menu.hidden);
  });
  // Anything else — a menu item, the canvas, another part of the page — dismisses it:
  // picking an item is an action, and the menu has no business outliving it.
  const dismiss = () => {
    if (!menu.hidden || toggle.getAttribute("aria-expanded") === "true") setOpen(false);
  };
  document.addEventListener("click", dismiss);
  toggle.parentElement.addEventListener("keydown", (e) => {
    if (e.key !== "Escape" || menu.hidden) return;
    setOpen(false);
    toggle.focus();
  });
  return () => document.removeEventListener("click", dismiss);
}

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

// firstDecisionId reads the decision *id* out of the DMN XML — the runtime
// identity, which is what a business rule task binds to and what a deployment is
// versioned by (ADR-0319). The name is what people read; the id is what the engine
// keys on, and the two drift apart the moment somebody renames a decision.
function firstDecisionId(xml) {
  try {
    const doc = new DOMParser().parseFromString(xml, "application/xml");
    const dec = doc.querySelector("decision");
    return (dec && dec.getAttribute("id")) || "";
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

// HINT_TAIL is the part of the hint that is the same under every view: the three
// verbs on the bar. They mean what they mean regardless of how the logic on screen
// is written (ADR-0321, ADR-0322).
const HINT_TAIL = `<b>Save</b> keeps a draft only you see; <b>Save to model</b> writes the
  decision every process resolves, and is what the next Publish ships; <b>Deploy</b> ships this
  decision to the engine on its own, as a new version. <b>Test</b> runs it against sample inputs
  and shows which rules fired — nothing is saved or deployed by asking.`;

// hintFor says what the view on screen is for. dmn-js opens four, and they are not
// variations on one editor: the requirements graph, a decision's rule table, a
// decision written as one FEEL expression, and a business knowledge model — a
// reusable function with its own parameters — are four different things to author.
// The hint used to describe the decision table under all of them, which left it
// wrong on three views out of four, and most wrong on the one whose layout explains
// itself least: a knowledge model shows `F`, a parameter list and a result variable,
// and none of that is a table.
function hintFor(active) {
  const type = (active && active.type) || "decisionTable";
  if (type === "drd") {
    return `Draw the decision requirements graph. An <b>Input Data</b> node is something this
      model is given, a <b>Decision</b> holds logic, and a <b>Knowledge Model</b> is a reusable
      function a decision can invoke. Open a decision's own tab to model its logic. ` + HINT_TAIL;
  }
  if (type === "literalExpression") {
    return `This decision's logic is one FEEL expression: what it evaluates to is the decision's
      result, under the variable named below it. Its inputs are whatever the requirements graph
      gives it — both are adopted into a business rule task that calls this decision. ` + HINT_TAIL;
  }
  if (type === "boxedExpression") {
    return `A <b>knowledge model</b> is a reusable FEEL function, not a decision: nothing calls it
      from a process. <b>F</b> is the expression language, the list beside it is the parameters a
      caller passes, the body is evaluated with them, and <b>Result</b> names the variable a
      decision binds when it invokes this model. ` + HINT_TAIL;
  }
  return `Model the decision table. <b>Input Data</b> nodes become the decision's inputs and the
    output column becomes its result variable — both are adopted into a business rule task that
    calls this decision. ` + HINT_TAIL;
}

// attachDmnWarnings keeps the findings strip under the canvas, and the badges on the
// requirements graph, in step with the model. It returns a teardown.
//
// What it says is dmn-warnings.js's to decide; this is only where it is said. Two
// placements, because a finding has two moments: the strip is what an author sees
// without looking for it, including from a decision's own view where the graph is not
// on screen at all, and the badge is what marks the shape once they are looking at
// the graph. Clicking a finding goes to its element — back to the graph first when
// the author is elsewhere, since pointing at a shape in a view that does not draw it
// would point at nothing.
function attachDmnWarnings(modeler, strip, toast) {
  let findings = [];
  let badges = []; // overlay ids on the graph, ours to reap
  let bound = null; // the viewer whose changes we are listening to
  let timer = null;

  const viewerNow = () => {
    try { return modeler.getActiveViewer(); } catch { return null; }
  };

  const clearBadges = () => {
    const viewer = viewerNow();
    if (viewer && badges.length) {
      try {
        const overlays = viewer.get("overlays");
        for (const id of badges) { try { overlays.remove(id); } catch { /* went with its view */ } }
      } catch { /* a view without overlays has nothing of ours on it */ }
    }
    badges = [];
  };

  const drawBadges = () => {
    clearBadges();
    const view = modeler.getActiveView();
    if (!view || view.type !== "drd") return; // only the graph has shapes to mark
    const viewer = viewerNow();
    if (!viewer) return;
    let overlays, registry;
    try { overlays = viewer.get("overlays"); registry = viewer.get("elementRegistry"); } catch { return; }
    const marked = new Set(); // one badge per shape, however many findings name it
    for (const f of findings) {
      if (marked.has(f.element) || !registry.get(f.element)) continue;
      marked.add(f.element);
      try {
        badges.push(overlays.add(f.element, "atlas-dmn-warning", {
          position: { top: -8, right: -8 },
          html: `<span class="unsup-badge" title="${esc(f.message)}">!</span>`,
        }));
      } catch { /* a shape without graphics yet (mid-import) */ }
    }
  };

  const render = () => {
    try {
      findings = knowledgeModelFindings(modeler.getDefinitions());
    } catch {
      findings = []; // mid-import, or a model dmn-js has not settled: nothing to say yet
    }
    strip.hidden = findings.length === 0;
    if (strip.hidden) {
      strip.innerHTML = "";
      return;
    }
    strip.innerHTML = `<ul>${findings.map((f) => {
      const fix = f.fix
        ? ` <button type="button" class="dmn-warn-fix" data-fix-source="${esc(f.fix.source)}"`
          + ` data-fix-target="${esc(f.fix.target)}">${esc(f.fix.label)}</button>`
        : "";
      return `<li><button type="button" data-el="${esc(f.element)}" data-rule="${esc(f.rule)}">`
        + `${esc(f.message)}</button>${fix}</li>`;
    }).join("")}</ul>`;
  };

  // focusCanvas puts keyboard focus on the drawing. dmn-js binds its keyboard to the
  // canvas SVG, so anything done from outside the canvas — a button in the strip below
  // it — has to hand focus back, or the author's next shortcut goes to the body.
  // canvas.focus() is the supported way and older diagram-js builds lack it, so the SVG
  // is focused directly when it is not there.
  const focusCanvas = (viewer) => {
    try {
      const canvas = viewer.get("canvas");
      if (typeof canvas.focus === "function") {
        canvas.focus();
        return;
      }
      const svg = canvas.getContainer().querySelector("svg");
      svg && svg.focus && svg.focus();
    } catch { /* a view without a canvas: nothing to focus */ }
  };

  const showInGraph = (id) => {
    const viewer = viewerNow();
    if (!viewer) return;
    try {
      const el = viewer.get("elementRegistry").get(id);
      if (!el) return;
      viewer.get("selection").select(el);
      try { viewer.get("canvas").scrollToElement(el); } catch { /* older diagram-js */ }
      focusCanvas(viewer);
    } catch { /* the view changed under the click */ }
  };

  // applyFix draws the missing requirement: the author's edit, made for them. It is
  // worth offering only because it is as easy to take back as to make, and it leaves
  // both ways of doing that within reach — the new connection is *selected*, which puts
  // its context pad (a single entry, the bin) under the author's eyes, and the canvas is
  // *focused*, which is what makes Ctrl+Z work.
  //
  // The focus is the part that is not obvious. dmn-js binds its keyboard to the canvas
  // SVG, not to the document, so a shortcut reaches the model only while that SVG has
  // focus. A button in the strip below the canvas does not give it focus — the click
  // leaves it on the body — so without this the author's first Ctrl+Z would go nowhere
  // and they would reasonably conclude the edit could not be undone.
  //
  // dmn-js's own rules decide whether the connection may be made and what it is; the
  // answer for a knowledge model reaching a decision is a knowledge requirement. Asking
  // rather than constructing the element means this cannot force a connection dmn-js
  // would refuse from the palette — and when it does refuse, the author is told why
  // instead of watching a button do nothing.
  const applyFix = (sourceId, targetId) => {
    const viewer = viewerNow();
    if (!viewer) return;
    let registry, modeling, rules;
    try {
      registry = viewer.get("elementRegistry");
      modeling = viewer.get("modeling");
      rules = viewer.get("rules");
    } catch {
      toast && toast("This view cannot draw the requirement.", "err");
      return;
    }
    const source = registry.get(sourceId);
    const target = registry.get(targetId);
    if (!source || !target) {
      toast && toast("One of the two elements is not on the requirements graph, so the "
        + "requirement cannot be drawn here.", "err");
      return;
    }
    if (!rules.allowed("connection.create", { source, target })) {
      toast && toast("dmn-js will not connect these two, so this has to be drawn by hand.", "err");
      return;
    }
    let connection;
    try {
      connection = modeling.connect(source, target);
    } catch (err) {
      toast && toast("Could not draw the requirement: " + err.message, "err");
      return;
    }
    // Selecting it is half the feature: it is both where the author looks to see what
    // was drawn, and the gesture that offers to remove it again.
    try { viewer.get("selection").select(connection); } catch { /* drawn either way */ }
    focusCanvas(viewer);
    toast && toast("Knowledge requirement drawn — Ctrl+Z takes it back, or the bin in its "
      + "context pad.", "ok");
  };

  const onClick = (e) => {
    const fixBtn = e.target.closest("button[data-fix-source]");
    if (fixBtn) {
      const source = fixBtn.getAttribute("data-fix-source");
      const target = fixBtn.getAttribute("data-fix-target");
      // The repair is a drawing, so it happens on the drawing: from a decision's own
      // view the graph is opened first, which is also where the author then sees it.
      const view = modeler.getActiveView();
      if (view && view.type === "drd") {
        applyFix(source, target);
        return;
      }
      const graph = modeler.getViews().find((v) => v.type === "drd");
      if (!graph) return;
      modeler.open(graph).then(() => applyFix(source, target)).catch(() => { /* nothing to draw on */ });
      return;
    }
    const btn = e.target.closest("button[data-el]");
    if (!btn) return;
    const id = btn.getAttribute("data-el");
    const view = modeler.getActiveView();
    if (view && view.type === "drd") {
      showInGraph(id);
      return;
    }
    const drd = modeler.getViews().find((v) => v.type === "drd");
    if (!drd) return;
    modeler.open(drd).then(() => showInGraph(id)).catch(() => { /* nothing to show */ });
  };

  // A finding is recomputed from the whole model, and typing into an expression
  // fires per keystroke, so the recompute is debounced rather than run on each one.
  const refresh = () => { render(); drawBadges(); };
  const schedule = () => {
    clearTimeout(timer);
    timer = setTimeout(refresh, 200);
  };

  // Each view is its own dmn-js instance, so the listener is moved with the author — a
  // listener left on the view they came from goes quiet without saying so. dmn-collab.js
  // binds the collaboration session the same way, for the same reason.
  //
  // `commandStack.changed` rather than the graph's `element.changed`, because the views
  // are not all diagram-js: editing an expression in a decision's own view changes the
  // model without any element changing on a canvas, and the findings are about the model.
  // Subscribing to the element event as well costs one more debounced call on the graph
  // and keeps a change that arrives outside a command — a peer's, applied by the
  // collaboration session — from going unseen.
  const CHANGE_EVENTS = ["commandStack.changed", "elements.changed", "element.changed"];
  const bindActive = () => {
    unbindActive();
    const viewer = viewerNow();
    if (!viewer) return;
    for (const event of CHANGE_EVENTS) {
      try { viewer.on(event, schedule); } catch { /* a view without that event */ }
    }
    bound = viewer;
  };
  const unbindActive = () => {
    if (!bound) return;
    for (const event of CHANGE_EVENTS) {
      try { bound.off(event, schedule); } catch { /* gone with its view */ }
    }
    bound = null;
  };
  const onViews = () => { bindActive(); refresh(); };

  modeler.on("views.changed", onViews);
  strip.addEventListener("click", onClick);
  bindActive();
  refresh();

  return () => {
    clearTimeout(timer);
    strip.removeEventListener("click", onClick);
    unbindActive();
    try { modeler.off("views.changed", onViews); } catch { /* torn down with the modeler */ }
    clearBadges();
  };
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
// onDmnMenuDismiss removes the bar menu's document-level click listener; a remount
// installs a new one, so the old must go with the editor it belonged to.
let onDmnMenuDismiss;
// collab is this editor's live session, when the decision has a draft to hold one
// on (ADR-0323). Torn down with the editor.
let collab;

export function cleanup() {
  generation++;
  if (collab) { try { collab.close(); } catch { /* ignore */ } collab = null; }
  if (onDmnMenuDismiss) { onDmnMenuDismiss(); onDmnMenuDismiss = null; }
  if (current) { try { current.destroy(); } catch { /* ignore */ } current = null; }
}

// mountDmnEditor renders the decision editor into root.
//
//   refId     — edit the decision this DMN reference points at; absent means a new one
//   draftId   — open this decision draft: work that has never been written to the
//               model, so it has no reference to be addressed by
//               (ADR-0321)
//   projectId — the application a new decision is filed into
//   forTask   — {processId, elementId} when the editor was reached by pressing
//               "＋ New decision" on a business rule task. It decides where the back
//               link goes and makes a successful model save leave an adoption behind
//               for that task (ADR-0320), so the round trip wires the task exactly as
//               the overlay used to.
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
        <span class="chip deployed-chip" id="dmn-deployed-chip" hidden></span>
        <div style="flex:1"></div>
        <span class="muted" id="dmn-status"></span>
        <button class="btn neutral" id="dmn-discard" hidden title="Throw away the draft and go back to the stored model">Discard draft</button>
        <button class="btn neutral" id="dmn-save" title="Save this decision as a draft. Nothing that reads this decision changes until you save it to the model.">Save</button>
        <button class="btn neutral toggle" id="dmn-test" type="button" aria-pressed="false" title="Run this decision against sample inputs and see which rules fired — nothing is saved or deployed">Test</button>
        <span class="bar-div" aria-hidden="true"></span>
        <button class="btn neutral" id="dmn-save-model" title="Write this into the decision model every process and application resolves — this is what the next Publish ships.">Save to model</button>
        <button class="btn" id="dmn-deploy" title="Deploy this decision on its own, as a versioned runtime artifact the engine can evaluate now. To ship a whole application, use Publish on the application.">Deploy</button>
        <div class="dropdown">
          <button class="icon-btn bar-more" id="dmn-more" type="button" aria-haspopup="true" aria-expanded="false" aria-label="More actions" title="Everything else this decision can do">&#8943;</button>
          <div class="dropdown-menu" id="dmn-menu" hidden>
            <button id="dmn-autolayout" type="button" title="Re-flow the decision requirements graph into a clean layout"><span class="mi-icon">&#8649;</span>Auto-layout</button>
            <button id="dmn-export" type="button" title="Download this decision as DMN XML"><span class="mi-icon">&#8595;</span>Export XML</button>
            <button id="dmn-docexport" type="button" title="Publish this decision as a structured PDF — the requirements graph plus every decision's prose and rule table — as a numbered version you can share"><span class="mi-icon">&#128196;</span>Documentation</button>
          </div>
        </div>
      </div>
      <div class="start-panel dmn-test-panel" id="dmn-test-panel" hidden>
        <div class="row">
          <label class="field"><span>Decision</span><select id="dmn-test-decision"></select></label>
          <button class="btn" id="dmn-test-run" title="Evaluate this decision with the values below">Run</button>
          <button class="btn neutral" id="dmn-test-close" title="Close the test panel">Close</button>
          <span class="err" id="dmn-test-err"></span>
        </div>
        <div class="dmn-test-inputs" id="dmn-test-inputs"></div>
        <div class="dmn-test-result" id="dmn-test-result"></div>
      </div>
      <div class="start-panel" id="dmn-doc-panel" hidden>
        <label class="field"><span>Title</span>
          <input id="dmn-doc-title" placeholder="Leave empty to use the model name"/></label>
        <label class="field"><span>Note for this version</span>
          <input id="dmn-doc-note" placeholder="What changed, or what this version was signed off for"/></label>
        <div class="row">
          <button class="btn" id="dmn-doc-publish" title="Publish a new documentation version as a PDF">Publish version</button>
          <button class="btn neutral" id="dmn-doc-cancel" title="Close the documentation panel">Close</button>
          <span class="err" id="dmn-doc-err"></span>
        </div>
        <div class="doc-history" id="dmn-doc-history"></div>
      </div>
      <div class="editor-body dmn-body">
        <div class="dmn-canvas"></div>
        <div class="dmn-props"></div>
      </div>
      <div class="dmn-warn" id="dmn-warn" hidden></div>
      <div class="dmn-hint muted" id="dmn-hint">${hintFor(null)}</div>
    </div>`;

  const canvas = root.querySelector(".dmn-canvas");
  const viewsBar = root.querySelector("#dmn-views");
  const propsPanel = root.querySelector(".dmn-props");
  const statusEl = root.querySelector("#dmn-status");
  const chip = root.querySelector("#dmn-ref-chip");
  const draftChip = root.querySelector("#dmn-draft-chip");
  const deployedChip = root.querySelector("#dmn-deployed-chip");
  const saveBtn = root.querySelector("#dmn-save");
  const modelBtn = root.querySelector("#dmn-save-model");
  const deployBtn = root.querySelector("#dmn-deploy");
  const discardBtn = root.querySelector("#dmn-discard");
  const testBtn = root.querySelector("#dmn-test");
  const testPanel = root.querySelector("#dmn-test-panel");
  const testDecision = root.querySelector("#dmn-test-decision");
  const testInputs = root.querySelector("#dmn-test-inputs");
  const testResult = root.querySelector("#dmn-test-result");
  const testErr = root.querySelector("#dmn-test-err");
  const backEl = root.querySelector("#dmn-back");
  const hintEl = root.querySelector("#dmn-hint");
  const warnEl = root.querySelector("#dmn-warn");

  // ---- identity ------------------------------------------------------------
  // What this session is editing, across the three layers a decision has
  // (ADR-0321): the draft it is keeping, the reference it is in the
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

  let dropWarnings = () => {};
  current = {
    destroy() {
      caretObserver.disconnect();
      dropWarnings();
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
      hintEl.innerHTML = hintFor(active);
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
    dropWarnings = attachDmnWarnings(modeler, warnEl, toast);
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
  // Two acts, two buttons (ADR-0321). Save keeps a draft: the
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
    saveBtn.disabled = modelBtn.disabled = deployBtn.disabled = discardBtn.disabled = true;
    statusEl.textContent = label;
    try {
      await fn();
    } finally {
      saveBtn.disabled = modelBtn.disabled = deployBtn.disabled = discardBtn.disabled = false;
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
        const first = !collab;
        draft = saved;
        showHandle();
        // The session is keyed by the draft, so the first Save is what opens it;
        // later saves tell the session this editor's work is safely stored, which
        // is what lets a peer's deferred change sync in.
        if (first) collab = attachCollab(modeler, api, saved.id, toast, dmnSurface);
        else if (collab.markSaved) collab.markSaved();
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

  // ---- deploying -----------------------------------------------------------
  // The third verb, and the only one that reaches the engine
  // (ADR-0322). Save changes the draft, Save to model
  // changes what every reference resolves, Deploy changes what the runtime
  // evaluates — and none of the three does another's job, which is why the bar
  // shows all three.

  // lastDeployedID is the decision id the chip was last resolved for, so switching
  // views does not re-ask the server the same question.
  let lastDeployedID = null;

  // showDeployed puts the current deployment of this decision on the bar — the
  // version and the key — the way a BPMN diagram opened from a deployment carries
  // "Deployment <key>" in its crumbs. Nothing to show is the normal state of a
  // decision that has never been deployed.
  function showDeployed(id, row) {
    if (!row) { deployedChip.hidden = true; deployedChip.textContent = ""; return; }
    deployedChip.hidden = false;
    deployedChip.textContent = `Deployed v${row.version} · key ${row.key}`;
    deployedChip.title = `“${id}” is deployed as version ${row.version} under definition key ${row.key}`
      + ` — what a process deployed from now on binds to. Deploying again makes a new version;`
      + ` processes already deployed keep the version they were pinned to.`;
  }

  // refreshDeployed asks what the decision currently on screen is deployed at. It is
  // keyed by the decision *id*, not by the reference or the handle, because that is
  // what the runtime versions — renaming the decision genuinely changes which
  // deployment the answer is about, and the chip follows it.
  async function refreshDeployed(force) {
    if (!modeler) return;
    let id = "";
    try {
      const out = await modeler.saveXML({ format: false });
      id = firstDecisionId(out.xml);
    } catch { /* the chip is not worth failing anything for */ }
    if (!force && id === lastDeployedID) return;
    lastDeployedID = id;
    if (!id) { showDeployed("", null); return; }
    try {
      const rows = (await api("GET", "/api/v1/decision-deployments?decisionId=" + encodeURIComponent(id))) || [];
      if (gen !== generation || lastDeployedID !== id) return; // superseded while asking
      showDeployed(id, rows.find((r) => r.current) || null);
    } catch {
      // A caller who may not read the deployment listing still gets the editor; the
      // chip is the only thing that goes missing.
      showDeployed(id, null);
    }
  }

  // deployDecision ships what is on screen, through the same durable path an
  // application publish uses: one record, written before anything is registered,
  // carrying its own XML. It deliberately does not write the model — that is
  // "Save to model", and conflating the two is what ADR-0321 just
  // separated.
  async function deployDecision() {
    await busy("Deploying…", async () => {
      try {
        const { xml, name } = await currentXml();
        const q = [];
        if (project) q.push("projectId=" + encodeURIComponent(project));
        if (ref) q.push("artifactId=" + encodeURIComponent(ref.id));
        if (modelRef) q.push("modelRef=" + encodeURIComponent(modelRef));
        const rep = await api("POST", "/api/v1/decision-deployments" + (q.length ? "?" + q.join("&") : ""), xml, true);
        const rows = (rep && rep.decisions) || [];
        // One model can provide several decisions, each versioned on its own. The one
        // reported is the one being edited, not whichever sorted first.
        const editedID = firstDecisionId(xml);
        const primary = rows.find((r) => r.decisionId === editedID) || rows[0] || {};
        await refreshDeployed(true);
        statusEl.textContent = `Deployed v${primary.version} · key ${rep.key}`;
        toast && toast(`Decision “${name}” deployed as version ${primary.version} (key ${rep.key})`, "ok");
        // What this route still costs, said out loud where it happens. A
        // `latest`-bound task may name this decision — the deploy resolves it to the
        // record just written. A `deployment`-bound one evaluates the model bundled
        // with its own process, and there is no model to bundle
        // (ADR-0327).
        if (!modelRef) {
          toast && toast("Deployed — but this decision is not in the model yet, so a business rule task bound to “deployment” cannot use it. Press “Save to model” to make it referenceable.", "warn");
        }
      } catch (e) {
        statusEl.textContent = "";
        toast && toast("Deploy failed: " + e.message, "err");
      }
    });
  }

  // ---- trying it ----------------------------------------------------------
  // A decision table is a program, and the question its author asks first is
  // whether it does what they meant (ADR-0326).
  // The panel asks the server that about the model on screen: nothing is saved,
  // nothing is deployed, and the answer is the temis trace saying which rules fired.

  // described is the model as the server last read it: which decisions it provides
  // and what each of them wants. The form is built from that rather than from
  // anything the browser re-derives out of the XML.
  let described = [];

  // testValue turns what somebody typed into the value the decision will see. The
  // declared type decides: a number field sends a number, a boolean sends true or
  // false, and anything else is sent as JSON when it parses (so a list or a record
  // can be typed) and as plain text when it does not — which is what a string is.
  function testValue(raw, type) {
    const text = String(raw ?? "").trim();
    if (text === "") return null;
    if (type === "number") { const n = Number(text); return Number.isNaN(n) ? text : n; }
    if (type === "boolean") return text === "true";
    if (type === "string") return text;
    try { return JSON.parse(text); } catch { return text; }
  }

  // renderTestForm draws one field per input the chosen decision consumes, keeping
  // whatever was already typed into a field of the same name — retyping the amount
  // on every edit of the table is exactly the friction this panel exists to remove.
  function renderTestForm() {
    const chosen = described.find((d) => d.id === testDecision.value) || described[0];
    const kept = {};
    testInputs.querySelectorAll("input[data-in]").forEach((el) => { kept[el.dataset.in] = el.value; });
    const fields = (chosen && chosen.inputs) || [];
    testInputs.innerHTML = fields.length
      ? fields.map((f) => `<label class="field"><span>${esc(f.name)}${f.type ? ` <span class="muted">${esc(f.type)}</span>` : ""}</span>` +
          `<input type="text" data-in="${esc(f.name)}" value="${esc(kept[f.name] || "")}" placeholder="${esc(f.type === "number" ? "250" : f.type === "boolean" ? "true" : "")}"/></label>`).join("")
      : `<p class="muted">This decision reads no input data, so there is nothing to fill in.</p>`;
  }

  // describeModel asks the server what the model on screen offers. It runs when the
  // panel opens, so a decision renamed or an input added since last time is picked up.
  async function describeModel() {
    const { xml } = await currentXml();
    const res = await api("POST", "/api/v1/decisions/evaluate", { xml });
    if (!res.ok) throw new Error(res.message || "this model does not compile yet");
    described = res.decisions || [];
    if (!described.length) throw new Error("this model declares no decision to run");
    const previous = testDecision.value;
    testDecision.innerHTML = described.map((d) =>
      `<option value="${esc(d.id)}">${esc(d.name || d.id)}</option>`).join("");
    // Prefer the decision that was being tested, else the one being edited, else the
    // first — so reopening the panel lands where it was left.
    const edited = firstDecisionId(xml);
    testDecision.value = described.some((d) => d.id === previous) ? previous
      : described.some((d) => d.id === edited) ? edited : described[0].id;
    renderTestForm();
  }

  // runTest evaluates the decision with what is in the form and renders the answer:
  // the outputs, and the rule matrix that says which rules fired and why — the same
  // matrix Operations draws for a decision a running process evaluated.
  async function runTest() {
    const inputs = {};
    const chosen = described.find((d) => d.id === testDecision.value);
    const types = {};
    for (const f of (chosen && chosen.inputs) || []) types[f.name] = f.type;
    testInputs.querySelectorAll("input[data-in]").forEach((el) => {
      const v = testValue(el.value, types[el.dataset.in]);
      if (v !== null) inputs[el.dataset.in] = v;
    });
    testErr.textContent = "";
    testResult.innerHTML = `<p class="muted">Running…</p>`;
    try {
      const { xml } = await currentXml();
      const res = await api("POST", "/api/v1/decisions/evaluate", { xml, decisionId: testDecision.value, inputs });
      if (!res.ok) {
        testResult.innerHTML = "";
        testErr.textContent = res.message || "this decision did not run";
        return;
      }
      const outs = Object.entries(res.outputs || {});
      const result = outs.length
        ? `<div class="res">${outs.map(([k, v]) =>
            `<div class="res-row"><span class="res-key">${esc(k)}</span><span class="res-val">${esc(traceValue(v))}</span></div>`).join("")}</div>`
        : `<p class="muted">This decision returned nothing for those inputs — no rule matched.</p>`;
      testResult.innerHTML = result + renderTrace(res.trace);
    } catch (e) {
      testResult.innerHTML = "";
      testErr.textContent = e.message;
    }
  }

  // openTest describes the model and shows the panel; a model that does not compile
  // says so where the author is looking rather than opening an empty form.
  async function setTestOpen(open) {
    testBtn.setAttribute("aria-pressed", open ? "true" : "false");
    if (!open) { testPanel.hidden = true; return; }
    testErr.textContent = "";
    testResult.innerHTML = "";
    testPanel.hidden = false;
    try {
      await describeModel();
    } catch (e) {
      testInputs.innerHTML = "";
      testErr.textContent = e.message;
    }
  }

  // ---- the overflow menu ---------------------------------------------------
  // Export takes the decision away as the DMN file it is, and Auto-layout re-flows
  // the requirements graph — the counterparts of the BPMN editor's own two, in the
  // same place on the bar.

  async function exportXml() {
    try {
      const { xml, name } = await currentXml();
      const blob = new Blob([xml], { type: "application/xml" });
      const a = document.createElement("a");
      a.href = URL.createObjectURL(blob);
      a.download = (modelRef || sanitizeFileName(name) || "decision") + ".dmn";
      a.click();
      URL.revokeObjectURL(a.href);
    } catch (e) {
      toast && toast("Export failed: " + e.message, "err");
    }
  }

  // The result is reported by toast rather than on the status line, as the BPMN
  // editor's Auto-layout reports it: the import re-opens the views, and the
  // views.changed handler clears the status line by design, so a message left there
  // would be racing the thing that just put it there.
  async function autoLayout() {
    await busy("Laying out…", async () => {
      try {
        const { xml } = await currentXml();
        const relaid = await api("POST", "/api/v1/dmn-layout", xml, true);
        if (typeof relaid !== "string" || !relaid) throw new Error("the model came back empty");
        await modeler.importXML(relaid);
        const view = modeler.getActiveView();
        if (view && view.type === "drd") {
          try { modeler.getActiveViewer().get("canvas").zoom("fit-viewport"); } catch { /* nothing to fit */ }
        }
        lastDeployedID = null; // the import replaced the canvas; re-read the chip's subject
        await refreshDeployed(true);
        toast && toast("Decision requirements graph laid out", "ok");
      } catch (e) {
        toast && toast("Auto-layout failed: " + e.message, "err");
      }
    });
  }

  // ---- documentation ------------------------------------------------------
  // A decision table is the business rule, and the people who sign it off are the
  // ones least likely to have a Modeler open. Publishing it is ADR-0143's act for
  // a second artifact kind (ADR-0324): the browser renders
  // the picture it is already drawing, the server numbers and stores it, and a
  // revocable link puts one version in front of a reader with no account.

  const docPanel = root.querySelector("#dmn-doc-panel");
  const docErr = root.querySelector("#dmn-doc-err");
  const docHistory = root.querySelector("#dmn-doc-history");
  const docPublish = root.querySelector("#dmn-doc-publish");
  const closeDoc = () => { docPanel.hidden = true; docErr.textContent = ""; };

  // renderDocHistory draws this decision's published versions, newest first, each
  // with its download and its sharing state. Sharing is per version, so every row
  // carries its own control.
  const renderDocHistory = (versions) => {
    if (!versions.length) {
      docHistory.innerHTML = `<p class="muted doc-empty">No version published yet.</p>`;
      return;
    }
    docHistory.innerHTML = versions.map((v) => {
      const when = v.createdAt ? new Date(v.createdAt * 1000).toLocaleString() : "";
      const by = v.createdBy ? " · " + esc(v.createdBy) : "";
      const note = v.note ? `<div class="doc-note">${esc(v.note)}</div>` : "";
      const share = v.shareUrl
        ? `<a class="doc-link" href="${esc(v.shareUrl)}" target="_blank" rel="noopener">Public link</a>
           <button class="btn neutral small" data-unshare="${esc(v.id)}" title="Revoke the shared link for this version">Revoke</button>`
        : `<button class="btn neutral small" data-share="${esc(v.id)}" title="Share a link to this documentation version">Share…</button>`;
      return `<div class="doc-version">
        <div class="doc-version-head">
          <b>v${v.version}</b>
          <span class="muted">${esc(when)}${by}</span>
        </div>
        ${note}
        <div class="row">
          <a class="doc-link" href="${esc(v.pdfUrl)}" target="_blank" rel="noopener">Open PDF</a>
          ${share}
          <button class="btn neutral small" data-delete="${esc(v.id)}" data-version="${v.version}" title="Delete this version and its PDF">Delete</button>
        </div>
      </div>`;
    }).join("");
    // Every version keeps a PDF, so an old archive grows without bound. Offer a
    // one-click prune when there is enough history to be worth trimming.
    if (versions.length > 1) {
      docHistory.innerHTML += `<div class="doc-prune row">
        <span class="muted">Keep newest</span>
        <input id="dmn-doc-keep" type="number" min="1" value="5" style="width:4em"/>
        <button class="btn neutral small" id="dmn-doc-prune" title="Delete older documentation versions">Prune older versions</button>
      </div>`;
    }
  };

  // documentedDecisionId is the decision the history is filed under: the first the
  // model declares, which is what the export files it under too.
  async function documentedDecisionId() {
    const { xml } = await currentXml();
    return collectDecisionDocumentation(xml).decisionId;
  }

  const loadDocHistory = async () => {
    const id = await documentedDecisionId();
    if (!id) { docHistory.innerHTML = ""; return; }
    try {
      const versions = await api("GET", `/api/v1/decisions/${encodeURIComponent(id)}/documentation`);
      renderDocHistory(versions || []);
    } catch (e) {
      docHistory.innerHTML = `<p class="err">${esc(e.message)}</p>`;
    }
  };

  const openDoc = async () => {
    docPanel.hidden = false;
    docErr.textContent = "";
    const titleField = root.querySelector("#dmn-doc-title");
    if (!titleField.value) {
      try {
        const { name } = await currentXml();
        titleField.value = name === DEFAULT_DECISION_NAME ? "" : name;
      } catch { /* the field simply stays empty */ }
    }
    docHistory.innerHTML = `<p class="muted doc-empty">Loading…</p>`;
    await loadDocHistory();
  };

  // publishDoc renders the document from the DRG view, because that is the view
  // that draws the requirements graph: a model open on a decision table has no
  // graph to save. The author is put back on the view they were reading.
  async function publishDoc() {
    docPublish.disabled = true;
    docErr.textContent = "";
    const wasOn = modeler.getActiveView();
    try {
      const drd = (modeler.getViews() || []).find((v) => v.type === "drd");
      if (drd && (!wasOn || wasOn.id !== drd.id)) await modeler.open(drd);
      const { xml } = await currentXml();
      await exportDecisionDocumentation({
        modeler, api, xml, modelRef,
        title: root.querySelector("#dmn-doc-title").value.trim(),
        note: root.querySelector("#dmn-doc-note").value.trim(),
      });
      root.querySelector("#dmn-doc-note").value = "";
      toast && toast("Documentation version published", "ok");
      await loadDocHistory();
    } catch (e) {
      docErr.textContent = e.message;
    } finally {
      if (wasOn) { try { await modeler.open(wasOn); } catch { /* the view went away */ } }
      docPublish.disabled = false;
    }
  }

  // Only a saved draft has a stable id to key a live session on, so a decision
  // that has never been saved does not co-edit yet — the first Save opens the
  // session (ADR-0323, ADR-0140).
  if (draft) collab = attachCollab(modeler, api, draft.id, toast, dmnSurface);

  saveBtn.addEventListener("click", saveDraft);
  modelBtn.addEventListener("click", saveToModel);
  deployBtn.addEventListener("click", deployDecision);
  root.querySelector("#dmn-docexport").addEventListener("click", () => {
    docPanel.hidden ? openDoc() : closeDoc();
  });
  root.querySelector("#dmn-doc-cancel").addEventListener("click", closeDoc);
  docPublish.addEventListener("click", publishDoc);
  // Sharing, revoking, deleting a version and pruning are delegated: the history
  // is re-rendered on every change, so binding per row would leak listeners.
  docHistory.addEventListener("click", async (e) => {
    const attr = (name) => e.target.getAttribute && e.target.getAttribute(name);
    const shareId = attr("data-share");
    const unshareId = attr("data-unshare");
    const deleteId = attr("data-delete");
    const prune = e.target.id === "dmn-doc-prune";
    if (!shareId && !unshareId && !deleteId && !prune) return;

    // Deleting a version and pruning both destroy a published artifact and its
    // PDF, so both confirm first — this is not a click to make lightly.
    if (deleteId && !window.confirm(`Delete documentation v${attr("data-version")}? This removes the version and its PDF for good.`)) return;
    let keep = 0;
    let decisionId = "";
    if (prune) {
      const keepField = docHistory.querySelector("#dmn-doc-keep");
      keep = Math.max(1, parseInt(keepField && keepField.value, 10) || 1);
      decisionId = await documentedDecisionId();
      if (!decisionId) return;
      if (!window.confirm(`Keep the newest ${keep} version${keep === 1 ? "" : "s"} and delete the rest? The deleted versions and their PDFs are gone for good.`)) return;
    }

    e.target.disabled = true;
    try {
      if (shareId) await api("POST", `/api/v1/decision-docs/${encodeURIComponent(shareId)}/share`);
      else if (unshareId) await api("DELETE", `/api/v1/decision-docs/${encodeURIComponent(unshareId)}/share`);
      else if (deleteId) await api("DELETE", `/api/v1/decision-docs/${encodeURIComponent(deleteId)}`);
      else if (prune) await api("POST", `/api/v1/decisions/${encodeURIComponent(decisionId)}/documentation/prune`, { keep });
      await loadDocHistory();
    } catch (err) {
      docErr.textContent = err.message;
    } finally {
      e.target.disabled = false;
    }
  });
  discardBtn.addEventListener("click", discardDraft);
  testBtn.addEventListener("click", () => { setTestOpen(testPanel.hidden); });
  root.querySelector("#dmn-test-close").addEventListener("click", () => setTestOpen(false));
  root.querySelector("#dmn-test-run").addEventListener("click", runTest);
  testDecision.addEventListener("change", renderTestForm);
  root.querySelector("#dmn-export").addEventListener("click", exportXml);
  root.querySelector("#dmn-autolayout").addEventListener("click", autoLayout);
  onDmnMenuDismiss = wireDmnBarMenu(root);

  // The bar says what this decision is deployed at from the moment it opens, and
  // follows a decision renamed while it is open.
  refreshDeployed(true);
  modeler.on("views.changed", () => { refreshDeployed(false); });
}
