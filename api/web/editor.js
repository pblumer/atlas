// BPMN editor view. Embeds the vendored bpmn-js modeler (ADR-0013): the canvas,
// palette, and context pad come from bpmn-js; the Details panel and Deploy&run
// wiring are ours. Assets load lazily so non-editor pages stay light.

import { attachFeelEditor } from "./feel.js";
import { attachJSONEditor } from "./json-editor.js";

const BPMN_CSS = [
  "vendor/bpmn/assets/diagram-js.css",
  "vendor/bpmn/assets/bpmn-js.css",
  "vendor/bpmn/assets/bpmn-font/css/bpmn-embedded.css",
];

let bpmnReady; // memoized loader promise → { BpmnJS, moddle: { zeebe, atlas } }
function loadBpmn() {
  if (bpmnReady) return bpmnReady;
  bpmnReady = new Promise((resolve, reject) => {
    for (const href of BPMN_CSS) {
      if (document.querySelector(`link[href="${href}"]`)) continue;
      const l = document.createElement("link");
      l.rel = "stylesheet"; l.href = href;
      document.head.appendChild(l);
    }
    const s = document.createElement("script");
    s.src = "vendor/bpmn/bpmn-modeler.js";
    s.onload = async () => {
      try {
        // The zeebe moddle lets bpmn-js read/write the zeebe extension elements
        // Atlas executes (zeebe:script, zeebe:taskDefinition). See ADR-0013. The
        // atlas moddle adds our own start-variable declaration (atlas:startForm),
        // which is editor metadata the engine ignores (the compiler skips unknown
        // extension elements) — it drives the typed Deploy & run form.
        const [zeebe, atlas] = await Promise.all([
          fetch("vendor/bpmn/zeebe.json").then((r) => r.json()),
          fetch("atlas-moddle.json").then((r) => r.json()),
        ]);
        resolve({ BpmnJS: window.BpmnJS, moddle: { zeebe, atlas } });
      } catch (e) {
        reject(new Error("failed to load the moddle extensions: " + e.message));
      }
    };
    s.onerror = () => reject(new Error("failed to load the BPMN modeler assets"));
    document.head.appendChild(s);
  });
  return bpmnReady;
}

// newModeler/newViewer construct a bpmn-js instance with the moddle extensions
// (zeebe + atlas) wired.
function newModeler(BpmnJS, moddle, container) {
  return new BpmnJS({ container, moddleExtensions: moddle });
}

// blankXML builds an empty diagram with a UNIQUE process id. The process id is
// the identity deployments and instances are grouped by (see the Details panel),
// so a fixed "Process_new" would make every new diagram a silent new *version* of
// the same process — the previous diagram would appear lost. A per-diagram id
// keeps distinct diagrams distinct; re-opening a deployment and redeploying still
// reuses its id, which is the intended way to cut a new version.
function blankXML() {
  const suffix = Date.now().toString(36) + Math.random().toString(36).slice(2, 6);
  const pid = `Process_${suffix}`;
  return `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:dc="http://www.omg.org/spec/DD/20100524/DC"
  id="Definitions_${suffix}" targetNamespace="http://atlas/bpmn">
  <bpmn:process id="${pid}" isExecutable="true">
    <bpmn:startEvent id="StartEvent_1" name="Start"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="${pid}">
      <bpmndi:BPMNShape id="StartEvent_1_di" bpmnElement="StartEvent_1">
        <dc:Bounds x="180" y="160" width="36" height="36"/>
      </bpmndi:BPMNShape>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`;
}

let current; // active modeler/viewer, destroyed on remount
let liveTimer; // active live-overlay poll, cleared on remount/leave

// cleanup tears down the current modeler and any live poll. app.js calls it (via
// window.__atlasCleanup) when navigating away so nothing keeps running.
export function cleanup() {
  if (liveTimer) { clearInterval(liveTimer); liveTimer = null; }
  if (current) { try { current.destroy(); } catch { /* ignore */ } current = null; }
}
window.__atlasCleanup = cleanup;

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

const shortType = (t) => (t || "").replace(/^bpmn:/, "");

export async function mountEditor(root, { api, toast, key, draftId }) {
  cleanup();

  const crumb = draftId != null ? "Draft" : key == null ? "New diagram" : "Deployment " + key;
  root.innerHTML = `
    <div class="editor">
      <div class="editor-bar">
        <span class="crumbs">${crumb}</span>
        <div class="etabs">
          <button data-tab="design" class="active">Design</button>
          <button data-tab="implement">Implement</button>
        </div>
        <div style="flex:1"></div>
        <button class="btn neutral" id="save">Save</button>
        <button class="btn neutral" id="export">Export XML</button>
        <button class="btn" id="deploy">Deploy &amp; run</button>
      </div>
      <div class="start-panel" id="deploy-panel" hidden>
        <div id="deploy-body"></div>
        <div class="row">
          <button class="btn" id="deploy-go">Deploy &amp; run</button>
          <button class="btn neutral" id="deploy-cancel">Cancel</button>
          <span class="err" id="deploy-err"></span>
        </div>
      </div>
      <div class="editor-body">
        <div id="canvas"></div>
        <div class="props-resizer" id="props-resizer" title="Drag to resize the properties panel"></div>
        <aside class="props" id="props">
          <div class="phead"><span class="ptype" id="p-icon">–</span>
            <div><div class="kv" id="p-typename">No selection</div><b id="p-name">—</b></div></div>
          <div class="psec" id="p-body">
            <p class="muted">Select an element to see its properties.</p>
          </div>
        </aside>
      </div>
      <div class="problems">
        <span class="badge" id="prob-count">0</span> Problems
        <span style="flex:1"></span>
        <span class="muted">Checked against the Atlas compiler</span>
      </div>
    </div>`;

  let lib;
  try {
    lib = await loadBpmn();
  } catch (e) {
    document.getElementById("canvas").innerHTML =
      `<div class="coming"><p>${esc(e.message)}</p></div>`;
    return;
  }

  const modeler = newModeler(lib.BpmnJS, lib.moddle, root.querySelector("#canvas"));
  current = modeler;
  window.__atlasModeler = modeler; // exposed for scripted/end-to-end testing

  // Load content: a saved draft, a deployed definition, or a fresh blank diagram.
  try {
    if (draftId != null) {
      const xml = await api("GET", `/api/v1/drafts/${encodeURIComponent(draftId)}/xml`);
      await modeler.importXML(typeof xml === "string" ? xml : String(xml));
    } else if (key == null) {
      await modeler.importXML(blankXML());
    } else {
      const xml = await api("GET", `/api/v1/processes/${key}/xml`);
      await modeler.importXML(typeof xml === "string" ? xml : String(xml));
    }
    modeler.get("canvas").zoom("fit-viewport");
    const pbo = rootProcess(modeler);
    if (pbo) root.querySelector(".crumbs").textContent = pbo.name || pbo.id || "Diagram";
  } catch (e) {
    toast("could not open diagram: " + e.message, "err");
  }

  const rerender = wireProperties(root, modeler, api);
  wireTabs(root, rerender);
  wireActions(root, modeler, api, toast);
  wireResizer(root, modeler);
}

// wireResizer makes the properties panel width draggable, so authoring long FEEL
// expressions and scripts has room. The chosen width is remembered across mounts
// (localStorage), and the bpmn-js canvas is nudged to re-fit after a drag.
function wireResizer(root, modeler) {
  const resizer = root.querySelector("#props-resizer");
  const props = root.querySelector("#props");
  if (!resizer || !props) return;
  const KEY = "atlas.propsWidth";
  const clamp = (w) => Math.max(240, Math.min(900, w));

  const saved = parseInt(localStorage.getItem(KEY) || "", 10);
  if (saved) props.style.width = clamp(saved) + "px";

  let startX = 0, startW = 0;
  const onMove = (e) => {
    // The panel is on the right, so dragging the divider left widens it.
    props.style.width = clamp(startW - (e.clientX - startX)) + "px";
  };
  const onUp = () => {
    document.removeEventListener("pointermove", onMove);
    document.removeEventListener("pointerup", onUp);
    resizer.classList.remove("dragging");
    document.body.style.userSelect = "";
    localStorage.setItem(KEY, String(parseInt(props.style.width, 10) || 300));
    // Let bpmn-js recompute its viewport for the canvas's new width.
    try { modeler && modeler.get("canvas").resized(); } catch { /* ignore */ }
    window.dispatchEvent(new Event("resize"));
  };
  resizer.addEventListener("pointerdown", (e) => {
    e.preventDefault();
    startX = e.clientX;
    startW = props.getBoundingClientRect().width;
    resizer.classList.add("dragging");
    document.body.style.userSelect = "none";
    document.addEventListener("pointermove", onMove);
    document.addEventListener("pointerup", onUp);
  });
  // Double-click the divider to reset to the default width.
  resizer.addEventListener("dblclick", () => {
    props.style.width = "300px";
    localStorage.setItem(KEY, "300");
    try { modeler && modeler.get("canvas").resized(); } catch { /* ignore */ }
    window.dispatchEvent(new Event("resize"));
  });
}

// wireTabs toggles the Design/Implement tabs. Design is the descriptive view
// (eCH-0158 level: names/labels and control flow only); Implement surfaces the
// executable detail (FEEL conditions, script expressions, job types). Switching
// tabs re-renders the properties panel for the current selection via onChange.
function wireTabs(root, onChange) {
  root.querySelectorAll(".etabs button").forEach((b) => {
    b.addEventListener("click", () => {
      root.querySelectorAll(".etabs button").forEach((x) => x.classList.remove("active"));
      b.classList.add("active");
      if (onChange) onChange();
    });
  });
}

// activeTab reads which properties view is selected, defaulting to design.
function activeTab(root) {
  const b = root.querySelector(".etabs button.active");
  return (b && b.dataset.tab) || "design";
}

// collectFeelVariables gathers names an author is likely to reference in a FEEL
// expression, for the completion popup. Two static signals: the start variables
// the process declares up front (atlas:StartForm), which are supplied when an
// instance starts; and the result variables written by script tasks elsewhere in
// the diagram — a token that has run through one carries that variable
// downstream. Best-effort: a failure just yields no variable hints.
function collectFeelVariables(modeler) {
  const vars = new Set();
  try {
    const rootBo = rootProcess(modeler);
    if (rootBo) {
      for (const v of readStartVariables(rootBo)) {
        if (v.name) vars.add(v.name);
      }
    }
  } catch { /* best-effort */ }
  try {
    modeler.get("elementRegistry").forEach((el) => {
      const s = findExt(el.businessObject, "zeebe:Script");
      if (s && s.resultVariable) vars.add(s.resultVariable);
    });
  } catch { /* best-effort */ }
  return [...vars].sort();
}

// enhanceFeel turns the FEEL <textarea> matched by `sel` into a syntax-highlighted
// editor with completions, live validation, a hint line, and a "Test" panel that
// evaluates the expression against sample variables. No-op if the field isn't
// present for the current selection. `validate` and `evaluate` are async server
// calls (the FEEL compiler / evaluator) or null.
function enhanceFeel(body, sel, vars, validate, evaluate) {
  const ta = body.querySelector(sel);
  if (!ta) return;
  attachFeelEditor(ta, { variables: vars, validate });
  const wrap = ta.closest(".feel-editor");
  if (!wrap) return;

  const hint = document.createElement("p");
  hint.className = "feel-hint";
  hint.innerHTML = "FEEL — <kbd>Ctrl</kbd>+<kbd>Space</kbd> for completions";
  if (evaluate) hint.innerHTML += ' &middot; <button type="button" class="linklike" data-feel-test>Test</button>';
  wrap.after(hint);
  if (!evaluate) return;

  // The Test panel: sample variables (JSON) + Run, evaluating the current
  // expression server-side and showing the typed result inline.
  const panel = document.createElement("div");
  panel.className = "feel-test";
  panel.hidden = true;
  panel.innerHTML = `
    <textarea class="feel-test-vars" rows="2" spellcheck="false" placeholder='sample variables, e.g. { "amount": 100 }'></textarea>
    <div class="feel-test-row">
      <button type="button" class="btn neutral feel-test-run">Run</button>
      <span class="feel-test-out" aria-live="polite"></span>
    </div>`;
  hint.after(panel);
  const varsEl = panel.querySelector(".feel-test-vars");
  const outEl = panel.querySelector(".feel-test-out");
  const setOut = (cls, text) => { outEl.className = "feel-test-out" + (cls ? " " + cls : ""); outEl.textContent = text; };

  hint.querySelector("[data-feel-test]").addEventListener("click", () => {
    panel.hidden = !panel.hidden;
    if (!panel.hidden) varsEl.focus();
  });
  panel.querySelector(".feel-test-run").addEventListener("click", async () => {
    let variables;
    try { variables = parseStartVariables(varsEl.value).variables || {}; }
    catch (e) { setOut("err", e.message); return; }
    setOut("", "…");
    try {
      const res = await evaluate(ta.value, variables);
      if (res && res.ok) setOut("ok", `= ${res.result} (${res.kind})`);
      else setOut("err", (res && res.error) || "could not evaluate");
    } catch (e) {
      setOut("err", e.message);
    }
  });
}

// findExt returns a business object's extension element of the given moddle type.
function findExt(bo, type) {
  const ext = bo && bo.extensionElements;
  if (!ext || !ext.values) return null;
  return ext.values.find((v) => v.$type === type) || null;
}

// upsertExt ensures element has an extension element of `type` and applies props,
// through the modeling API so it participates in undo/redo.
function upsertExt(modeler, element, type, props) {
  const moddle = modeler.get("moddle");
  const modeling = modeler.get("modeling");
  const bo = element.businessObject;
  let ext = bo.extensionElements;
  if (!ext) {
    ext = moddle.create("bpmn:ExtensionElements", { values: [] });
    ext.$parent = bo;
  }
  let node = (ext.values || []).find((v) => v.$type === type);
  if (!node) {
    node = moddle.create(type);
    node.$parent = ext;
    ext.values = [...(ext.values || []), node];
  }
  Object.assign(node, props);
  modeling.updateProperties(element, { extensionElements: ext });
}

// messageFieldsHTML renders the message picker for a catch or throw event: a
// dropdown of the model's shared messages (plus "new"), and — once one is chosen —
// its name and correlation key, which are shared so every event using the message
// stays in sync (that's what lets a throw and a catch correlate). med is the
// bpmn:MessageEventDefinition.
function messageFieldsHTML(modeler, med, hint) {
  const current = med.messageRef;
  const options = listMessages(modeler).map((m) =>
    `<option value="${esc(m.id)}"${current && current.id === m.id ? " selected" : ""}>${esc(m.name || m.id)}</option>`
  ).join("");
  const fields = current ? `
    <label class="field"><span>Message name</span>
      <input type="text" id="f-msgname" value="${esc(current.name || "")}" placeholder="payment-received"/></label>
    <label class="field"><span>Correlation key (FEEL)</span>
      <textarea id="f-corrkey" rows="1" placeholder="orderId">${esc(messageCorrelationKey(current))}</textarea></label>
    <p class="muted" style="font-size:12px">Shared with every event that uses this message — a throw and a catch correlate when they use the same message and their keys evaluate equal.</p>` : "";
  return `<h3>Message</h3>
    <label class="field"><span>Message</span>
      <select id="f-msgref">
        <option value="">— none —</option>
        ${options}
        <option value="__new__">＋ New message…</option>
      </select></label>
    ${fields}
    <p class="muted" style="font-size:12px">${hint}</p>`;
}

// messagesManagerHTML lists the model's messages for central management (add,
// rename, set correlation key, delete). Shown on the diagram/collaboration root.
function messagesManagerHTML(modeler) {
  const msgs = listMessages(modeler);
  const rows = msgs.length
    ? msgs.map((m) => `
        <div class="msg-row" data-id="${esc(m.id)}">
          <input class="msg-name" value="${esc(m.name || "")}" placeholder="message name"/>
          <input class="msg-key" value="${esc(messageCorrelationKey(m))}" placeholder="correlationKey (FEEL)"/>
          <button type="button" class="btn ghost danger msg-del" title="Delete message">✕</button>
        </div>`).join("")
    : `<p class="muted" style="font-size:12px;margin:0 0 8px">No messages yet — add one, then reference it from a message throw/catch event.</p>`;
  return `<h3>Messages</h3>
    <div class="msg-list">${rows}</div>
    <button type="button" class="btn neutral" id="msg-add" style="margin-top:8px">＋ Add message</button>
    <p class="muted" style="font-size:12px">A message links a <b>throw</b> event to the <b>catch</b> events waiting for it. They correlate when they share a message and their correlation keys evaluate equal.</p>`;
}

// wireMessagesManager binds the Messages management section's inputs and buttons.
// rerenderRoot re-renders the root panel after add/delete so the list updates.
function wireMessagesManager(body, modeler, rerenderRoot) {
  const add = body.querySelector("#msg-add");
  if (add) add.addEventListener("click", () => { createMessage(modeler, "message"); rerenderRoot(); });
  body.querySelectorAll(".msg-row").forEach((row) => {
    const id = row.dataset.id;
    const msg = () => listMessages(modeler).find((m) => m.id === id);
    const nameIn = row.querySelector(".msg-name");
    const keyIn = row.querySelector(".msg-key");
    if (nameIn) nameIn.addEventListener("change", () => { const m = msg(); if (m) m.name = nameIn.value.trim(); });
    if (keyIn) keyIn.addEventListener("change", () => { const m = msg(); if (m) setMessageCorrelationKey(modeler, m, keyIn.value.trim()); });
    const del = row.querySelector(".msg-del");
    if (del) del.addEventListener("click", () => { deleteMessage(modeler, id); rerenderRoot(); });
  });
}

const isActivity = (bo) => /Task$/.test((bo && bo.$type) || "");

// timerDefOf returns an event's bpmn:TimerEventDefinition, or null.
function timerDefOf(bo) {
  return (bo && bo.eventDefinitions || []).find((d) => d.$type === "bpmn:TimerEventDefinition") || null;
}

// messageDefOf returns an event's bpmn:MessageEventDefinition, or null.
function messageDefOf(bo) {
  return (bo && bo.eventDefinitions || []).find((d) => d.$type === "bpmn:MessageEventDefinition") || null;
}

// definitionsOf returns the diagram's <bpmn:definitions> moddle element, where
// top-level <bpmn:message> declarations live.
function definitionsOf(modeler) {
  try { if (typeof modeler.getDefinitions === "function") return modeler.getDefinitions(); } catch { /* older bpmn-js */ }
  try { return modeler.get("canvas").getRootElement().businessObject.$parent; } catch { return null; }
}

// Messages are top-level <bpmn:message> declarations shared by reference: a throw
// event and the catch events waiting for it must point at the SAME message (same
// name and correlation key) to correlate. These helpers let the editor treat
// messages as first-class, reusable definitions rather than per-event text.

// listMessages returns every <bpmn:message> declared on the model's definitions.
function listMessages(modeler) {
  const defs = definitionsOf(modeler);
  const out = [];
  if (defs && defs.rootElements) {
    for (const el of defs.rootElements) {
      if (el.$type === "bpmn:Message") out.push(el);
    }
  }
  return out;
}

// messageCorrelationKey reads a message's zeebe correlation-key expression,
// stripped of the leading '=' the engine tolerates.
function messageCorrelationKey(msg) {
  const vals = msg && msg.extensionElements && msg.extensionElements.values;
  const sub = vals && vals.find((v) => v.$type === "zeebe:Subscription");
  return ((sub && sub.correlationKey) || "").replace(/^=\s*/, "");
}

// createMessage adds a fresh <bpmn:message> to the model and returns it.
function createMessage(modeler, name) {
  const moddle = modeler.get("moddle");
  const msg = moddle.create("bpmn:Message");
  msg.id = "Message_" + Math.random().toString(36).slice(2, 8);
  msg.name = name || "";
  const defs = definitionsOf(modeler);
  if (defs) {
    msg.$parent = defs;
    defs.rootElements = [...(defs.rootElements || []), msg];
  }
  return msg;
}

// setMessageCorrelationKey sets a message's shared zeebe correlation-key
// expression (normalizing the Zeebe '=' prefix); it applies to every event that
// references the message.
function setMessageCorrelationKey(modeler, msg, key) {
  const moddle = modeler.get("moddle");
  let ext = msg.extensionElements;
  if (!ext) { ext = moddle.create("bpmn:ExtensionElements", { values: [] }); ext.$parent = msg; msg.extensionElements = ext; }
  let sub = (ext.values || []).find((v) => v.$type === "zeebe:Subscription");
  if (!sub) { sub = moddle.create("zeebe:Subscription"); sub.$parent = ext; ext.values = [...(ext.values || []), sub]; }
  const k = (key || "").trim();
  sub.correlationKey = k === "" ? "" : (k.startsWith("=") ? k : "= " + k);
}

// linkMessage points a message event definition at a message (undo/redo tracked).
function linkMessage(modeler, element, med, msg) {
  try { modeler.get("modeling").updateModdleProperties(element, med, { messageRef: msg || undefined }); } catch { /* stale */ }
}

// deleteMessage removes a message and clears any event still referencing it, so a
// deleted message never leaves a dangling messageRef (which would fail to compile).
function deleteMessage(modeler, msgId) {
  const defs = definitionsOf(modeler);
  if (defs && defs.rootElements) defs.rootElements = defs.rootElements.filter((e) => e.id !== msgId);
  const modeling = modeler.get("modeling");
  modeler.get("elementRegistry").getAll().forEach((el) => {
    const med = messageDefOf(el.businessObject);
    if (med && med.messageRef && med.messageRef.id === msgId) {
      try { modeling.updateModdleProperties(el, med, { messageRef: undefined }); } catch { /* stale */ }
    }
  });
}

// rootProcess returns the diagram's process business object, or null if the root
// isn't a plain process (e.g. a collaboration with pools).
function rootProcess(modeler) {
  try {
    const bo = modeler.get("canvas").getRootElement().businessObject;
    return bo && /:Process$/.test(bo.$type || "") ? bo : null;
  } catch { return null; }
}

// isCollaborationRoot reports whether the diagram root is a collaboration (pools),
// rather than a single process.
function isCollaborationRoot(modeler) {
  try {
    const bo = modeler.get("canvas").getRootElement().businessObject;
    return !!bo && /:Collaboration$/.test(bo.$type || "");
  } catch { return false; }
}

// The scalar types a declared start variable can take, matching what the server
// accepts (parseStartVariables) and what the typed Deploy form coerces to.
const START_VAR_TYPES = ["string", "number", "boolean", "json"];

// readStartVariables returns the start variables a process declares via its
// atlas:StartForm extension element: [{name, type, default, required}]. Empty
// when none are declared.
function readStartVariables(bo) {
  const sf = findExt(bo, "atlas:StartForm");
  return ((sf && sf.variables) || []).map((v) => ({
    name: v.name || "",
    type: START_VAR_TYPES.includes(v.type) ? v.type : "string",
    default: v.default || "",
    required: !!v.required,
  }));
}

// writeStartVariables persists the declared start variables onto the process
// through the modeling API (undo/redo; serializes on deploy). Rows without a
// name are dropped; an empty result removes the atlas:StartForm entirely so a
// cleared declaration leaves no dangling element.
function writeStartVariables(modeler, list) {
  const moddle = modeler.get("moddle");
  const modeling = modeler.get("modeling");
  const rootEl = modeler.get("canvas").getRootElement();
  const bo = rootEl.businessObject;
  let ext = bo.extensionElements;
  if (!ext) { ext = moddle.create("bpmn:ExtensionElements", { values: [] }); ext.$parent = bo; }
  let sf = (ext.values || []).find((v) => v.$type === "atlas:StartForm");
  const clean = list.filter((v) => (v.name || "").trim() !== "");
  if (clean.length === 0) {
    if (sf) ext.values = (ext.values || []).filter((v) => v !== sf);
  } else {
    if (!sf) { sf = moddle.create("atlas:StartForm"); sf.$parent = ext; ext.values = [...(ext.values || []), sf]; }
    sf.variables = clean.map((v) => {
      const sv = moddle.create("atlas:StartVariable", {
        name: v.name.trim(),
        type: START_VAR_TYPES.includes(v.type) ? v.type : "string",
      });
      if ((v.default || "") !== "") sv.default = v.default;
      if (v.required) sv.required = true;
      sv.$parent = sf;
      return sv;
    });
  }
  modeling.updateProperties(rootEl, { extensionElements: ext });
}

// startVarRowHTML renders one editable declaration row. Values are escaped for
// the attributes; the type <select> marks the current option. For json-typed
// variables, the default field is a textarea (upgraded to a JSON editor after
// the row is inserted into the DOM by wireStartVars).
function startVarRowHTML(v) {
  const opts = START_VAR_TYPES
    .map((t) => `<option value="${t}"${t === v.type ? " selected" : ""}>${t}</option>`).join("");
  const defaultField = v.type === "json"
    ? `<textarea class="sv-default sv-json-default" rows="2" placeholder='{"key": "value"}' aria-label="Default value (JSON)">${esc(v.default)}</textarea>`
    : `<input type="text" class="sv-default" value="${esc(v.default)}" placeholder="default" aria-label="Default value"/>`;
  return `<div class="sv-row${v.type === "json" ? " sv-row-json" : ""}">
    <input type="text" class="sv-name" value="${esc(v.name)}" placeholder="customer" aria-label="Variable name"/>
    <select class="sv-type" aria-label="Type">${opts}</select>
    ${defaultField}
    <label class="sv-req" title="Required"><input type="checkbox" class="sv-required"${v.required ? " checked" : ""}/> req</label>
    <button type="button" class="sv-del icon-btn" title="Remove" aria-label="Remove variable">✕</button>
  </div>`;
}

// wireStartVars renders and wires the process-level start-variable declaration
// editor into #sv-list. Rows persist on change (blur), not on every keystroke,
// so typing isn't interrupted; adding a row is DOM-only until it gets a name.
// Editing the process root fires element.changed for the root, but with nothing
// selected the panel isn't re-rendered, so in-progress rows survive a save.
function wireStartVars(body, modeler) {
  const listEl = body.querySelector("#sv-list");
  const addBtn = body.querySelector("#sv-add");
  if (!listEl || !addBtn) return;

  const editors = new Map(); // row DOM node → attachJSONEditor handle

  // attachEditors upgrades any json-typed default textareas to JSON editors.
  const attachEditors = () => {
    for (const row of listEl.querySelectorAll(".sv-row-json")) {
      const ta = row.querySelector(".sv-json-default");
      if (ta && !editors.has(row)) {
        const handle = attachJSONEditor(ta, { compact: true, onChange: persist });
        if (handle) editors.set(row, handle);
      }
    }
  };

  const collect = () => [...listEl.querySelectorAll(".sv-row")].map((row) => ({
    name: row.querySelector(".sv-name").value,
    type: row.querySelector(".sv-type").value,
    default: row.querySelector(".sv-default").value,
    required: row.querySelector(".sv-required").checked,
  }));
  const persist = () => { try { writeStartVariables(modeler, collect()); } catch { /* stale */ } };

  listEl.addEventListener("change", (e) => {
    // When the type dropdown changes to or from "json", rebuild the row so the
    // default field switches between <input> and the JSON editor.
    const sel = e.target.closest(".sv-type");
    if (sel) {
      const row = sel.closest(".sv-row");
      const data = {
        name: row.querySelector(".sv-name").value,
        type: sel.value,
        default: sel.value === "json" ? "" : row.querySelector(".sv-default").value,
        required: row.querySelector(".sv-required").checked,
      };
      // Destroy any existing JSON editor for this row.
      if (editors.has(row)) { editors.get(row).destroy(); editors.delete(row); }
      row.outerHTML = startVarRowHTML(data);
      attachEditors();
    }
    persist();
  });
  listEl.addEventListener("click", (e) => {
    const del = e.target.closest(".sv-del");
    if (del) {
      const row = del.closest(".sv-row");
      if (editors.has(row)) { editors.get(row).destroy(); editors.delete(row); }
      row.remove();
      persist();
    }
  });
  addBtn.addEventListener("click", () => {
    listEl.insertAdjacentHTML("beforeend", startVarRowHTML({ name: "", type: "string", default: "", required: false }));
    listEl.querySelector(".sv-row:last-child .sv-name").focus();
  });

  // Attach editors for any pre-existing json-typed rows.
  attachEditors();
}

function wireProperties(root, modeler, api) {
  const icon = root.querySelector("#p-icon");
  const typename = root.querySelector("#p-typename");
  const nameEl = root.querySelector("#p-name");
  const body = root.querySelector("#p-body");
  const modeling = modeler.get("modeling");
  const selection = modeler.get("selection");

  // savePreservingPanel runs a field save whose resulting element.changed should
  // NOT rebuild the whole properties panel. Editing a FEEL field saves on blur;
  // rebuilding then would tear down the FEEL editor (losing caret/scroll) and the
  // Test panel the user just opened — clicking into the sample-variables box would
  // destroy it mid-interaction. Only this save's synchronous change is suppressed;
  // genuinely external changes still refresh the panel.
  let suppressRerender = false;
  const savePreservingPanel = (fn) => {
    suppressRerender = true;
    try { fn(); } finally { suppressRerender = false; }
  };

  function show(element) {
    if (!element) {
      // Nothing selected → show the process itself, so its id/name can be edited
      // (this is how you rename a diagram; the id is the deployed process id).
      const rootBo = rootProcess(modeler);
      if (rootBo) {
        icon.textContent = "PR"; typename.textContent = "Process";
        nameEl.textContent = rootBo.name || rootBo.id || "(process)";
        // The declaration editor is executable detail, so it lives on the
        // Implement tab (like a task's script or a branch's condition).
        let startVarsHTML = "";
        if (activeTab(root) === "implement") {
          const declared = readStartVariables(rootBo);
          startVarsHTML = `
            <h3>Start variables</h3>
            <div id="sv-list">${declared.map(startVarRowHTML).join("")}</div>
            <button type="button" class="btn neutral" id="sv-add" style="margin-top:6px">+ Add variable</button>
            <p class="muted" style="font-size:12px">Declared here, these render as a typed form on <b>Deploy &amp; run</b> — with defaults and required checks — instead of raw JSON. The engine ignores the declaration; it's authoring metadata.</p>`;
        }
        body.innerHTML = `
          <h3>Process</h3>
          <label class="field"><span>Name</span><input type="text" id="f-pname" value="${esc(rootBo.name || "")}" placeholder="Order fulfillment"/></label>
          <label class="field"><span>Process ID</span><input type="text" id="f-pid" value="${esc(rootBo.id || "")}" placeholder="order-fulfillment"/></label>
          <p class="muted" style="font-size:12px">The Process ID is the identity deployments and instances are grouped by. Renaming it and deploying creates a new process rather than a new version.</p>
          ${startVarsHTML}
          ${messagesManagerHTML(modeler)}`;
        const rootEl = modeler.get("canvas").getRootElement();
        body.querySelector("#f-pname").addEventListener("change", (e) => {
          try { modeling.updateProperties(rootEl, { name: e.target.value }); } catch { /* ignore */ }
        });
        body.querySelector("#f-pid").addEventListener("change", (e) => {
          const v = (e.target.value || "").trim();
          if (v) { try { modeling.updateProperties(rootEl, { id: v }); } catch { toast("invalid process id", "err"); } }
        });
        wireStartVars(body, modeler);
        wireMessagesManager(body, modeler, () => show(null));
        return;
      }
      // A collaboration root has no single process to rename; each pool
      // (participant) is renamed by selecting it and editing its Name.
      if (isCollaborationRoot(modeler)) {
        icon.textContent = "CO"; typename.textContent = "Collaboration"; nameEl.textContent = "(collaboration)";
        body.innerHTML = `
          <h3>Collaboration</h3>
          <p class="muted" style="font-size:12px">This diagram has several <b>pools</b> — each deploys as its own process. Select a pool to rename it, or an element inside a pool to configure it. Pools talk to each other through <b>message events</b>: a throw event in one pool and a catch event in another that reference the <b>same message</b> below.</p>
          ${messagesManagerHTML(modeler)}`;
        wireMessagesManager(body, modeler, () => show(null));
        return;
      }
      icon.textContent = "–"; typename.textContent = "No selection"; nameEl.textContent = "—";
      body.innerHTML = `<p class="muted">Select an element to see its properties.</p>`;
      return;
    }
    const bo = element.businessObject || {};
    const type = shortType(element.type);
    icon.textContent = type.slice(0, 2).toUpperCase();
    typename.textContent = type;
    nameEl.textContent = bo.name || bo.id || "(unnamed)";

    const tab = activeTab(root);
    const isSeqFlow = /:SequenceFlow$/.test(bo.$type || "");
    const src = bo.sourceRef;
    // A conditional branch is a flow out of an exclusive/inclusive gateway. Its
    // name is the descriptive label (Design); its conditionExpression is the FEEL
    // guard (Implement). The gateway's default flow carries no condition.
    const isGatewayFlow = isSeqFlow && src && /(Exclusive|Inclusive)Gateway$/.test(src.$type || "");
    const isDefaultFlow = isGatewayFlow && src.default === bo;

    // General: the descriptive view shared by both tabs. For a sequence flow the
    // name IS the diagram label (e.g. the branch outcome "Großauftrag").
    let html = `
      <h3>General</h3>
      <label class="field"><span>${isSeqFlow ? "Label" : "Name"}</span><input type="text" id="f-name" value="${esc(bo.name || "")}"${isSeqFlow ? ' placeholder="Großauftrag"' : ""}/></label>
      <label class="field"><span>ID</span><input type="text" value="${esc(bo.id || "")}" readonly/></label>`;

    if (tab === "implement") {
      if (isActivity(bo)) {
        const t = bo.$type;
        html += `
          <label class="field"><span>Task type</span>
            <select id="f-tasktype">
              <option value="bpmn:Task" ${t === "bpmn:Task" ? "selected" : ""}>Undefined task</option>
              <option value="bpmn:ScriptTask" ${t === "bpmn:ScriptTask" ? "selected" : ""}>Script task (FEEL)</option>
              <option value="bpmn:ServiceTask" ${t === "bpmn:ServiceTask" ? "selected" : ""}>Service task (job worker)</option>
              <option value="bpmn:UserTask" ${t === "bpmn:UserTask" ? "selected" : ""}>User task</option>
            </select></label>`;

        if (t === "bpmn:ScriptTask") {
          const s = findExt(bo, "zeebe:Script") || {};
          const exprText = (s.expression || "").replace(/^=\s*/, "");
          html += `<h3>Script (FEEL)</h3>
            <label class="field"><span>Expression</span>
              <textarea id="f-expr" rows="3" placeholder="amount * (1 + taxRate)">${esc(exprText)}</textarea></label>
            <label class="field"><span>Result variable</span>
              <input type="text" id="f-result" value="${esc(s.resultVariable || "")}" placeholder="gross"/></label>`;
        } else if (t === "bpmn:ServiceTask") {
          const d = findExt(bo, "zeebe:TaskDefinition") || {};
          html += `<h3>Task definition</h3>
            <label class="field"><span>Job type</span>
              <input type="text" id="f-jobtype" value="${esc(d.type || "")}" placeholder="payment"/></label>`;
        } else if (t === "bpmn:UserTask") {
          const a = findExt(bo, "zeebe:AssignmentDefinition") || {};
          html += `<h3>Assignment</h3>
            <label class="field"><span>Assignee</span>
              <input type="text" id="f-assignee" value="${esc(a.assignee || "")}" placeholder="editor"/></label>
            <label class="field"><span>Candidate groups</span>
              <input type="text" id="f-groups" value="${esc(a.candidateGroups || "")}" placeholder="reviewers"/></label>`;
        }
      } else if (isDefaultFlow) {
        html += `<h3>Condition (FEEL)</h3>
          <p class="muted" style="font-size:12px">This is the gateway's <b>default flow</b> — taken when no other branch's condition matches, so it carries no condition of its own.</p>`;
      } else if (isGatewayFlow) {
        const condText = ((bo.conditionExpression && bo.conditionExpression.body) || "").replace(/^=\s*/, "");
        html += `<h3>Condition (FEEL)</h3>
          <label class="field"><span>Expression</span>
            <textarea id="f-cond" rows="2" placeholder="amount > 100">${esc(condText)}</textarea></label>
          <p class="muted" style="font-size:12px">Evaluated when the token reaches the gateway; the first branch whose condition holds is taken.</p>`;
      } else if (bo.$type === "bpmn:IntermediateCatchEvent") {
        const timer = timerDefOf(bo);
        const msg = messageDefOf(bo);
        if (timer) {
          const dur = (timer.timeDuration && timer.timeDuration.body) || "";
          html += `<h3>Timer</h3>
            <label class="field"><span>Duration (ISO&nbsp;8601)</span>
              <input type="text" id="f-duration" value="${esc(dur)}" placeholder="PT30S"/></label>
            <p class="muted" style="font-size:12px">e.g. PT30S (30s), PT5M, PT1H, P1DT2H. The event waits this long, then continues.</p>`;
        } else if (msg) {
          html += messageFieldsHTML(modeler, msg, "The event waits until this message is published with a matching correlation key.");
        } else {
          html += `<p class="muted" style="font-size:12px">Use the wrench icon on the element to make this a <b>Timer</b> or <b>Message</b> intermediate catch event, then configure it here.</p>`;
        }
      } else if (bo.$type === "bpmn:IntermediateThrowEvent") {
        const msg = messageDefOf(bo);
        if (msg) {
          html += messageFieldsHTML(modeler, msg, "On reaching this event the message is published; any instance waiting on it with a matching correlation key continues.");
        } else {
          html += `<p class="muted" style="font-size:12px">Use the wrench icon on the element to make this a <b>Message</b> throw event, then configure it here.</p>`;
        }
      }
    } else if (isGatewayFlow && !isDefaultFlow) {
      // Design tab: point to where the executable rule lives.
      const has = bo.conditionExpression && bo.conditionExpression.body;
      html += `<p class="muted" style="font-size:12px">${has
        ? "A FEEL condition is set on this branch — edit it in the <b>Implement</b> tab."
        : "Set this branch's FEEL condition in the <b>Implement</b> tab."}</p>`;
    }
    body.innerHTML = html;

    body.querySelector("#f-name").addEventListener("change", (e) => {
      try { modeling.updateProperties(element, { name: e.target.value }); } catch { /* stale */ }
    });

    const tasktype = body.querySelector("#f-tasktype");
    if (tasktype) {
      tasktype.addEventListener("change", (e) => {
        try {
          const el = modeler.get("bpmnReplace").replaceElement(element, { type: e.target.value });
          selection.select(el);
          show(el);
        } catch (err) { /* stale */ }
      });
    }
    const fexpr = body.querySelector("#f-expr");
    const fresult = body.querySelector("#f-result");
    const saveScript = () => savePreservingPanel(() => {
      const raw = (fexpr.value || "").trim();
      upsertExt(modeler, element, "zeebe:Script", {
        expression: raw === "" ? "" : (raw.startsWith("=") ? raw : "= " + raw),
        resultVariable: (fresult.value || "").trim(),
      });
    });
    if (fexpr) fexpr.addEventListener("change", saveScript);
    if (fresult) fresult.addEventListener("change", saveScript);

    const fjob = body.querySelector("#f-jobtype");
    if (fjob) {
      fjob.addEventListener("change", () => {
        upsertExt(modeler, element, "zeebe:TaskDefinition", { type: (fjob.value || "").trim() });
      });
    }

    const fassignee = body.querySelector("#f-assignee");
    const fgroups = body.querySelector("#f-groups");
    const saveAssignment = () => {
      upsertExt(modeler, element, "zeebe:AssignmentDefinition", {
        assignee: (fassignee.value || "").trim(),
        candidateGroups: (fgroups.value || "").trim(),
      });
    };
    if (fassignee) fassignee.addEventListener("change", saveAssignment);
    if (fgroups) fgroups.addEventListener("change", saveAssignment);

    const fdur = body.querySelector("#f-duration");
    if (fdur) {
      fdur.addEventListener("change", () => {
        const timer = timerDefOf(element.businessObject);
        if (!timer) return;
        const moddle = modeler.get("moddle");
        let td = timer.timeDuration;
        if (!td) { td = moddle.create("bpmn:FormalExpression"); td.$parent = timer; }
        td.body = (fdur.value || "").trim();
        modeling.updateModdleProperties(element, timer, { timeDuration: td });
      });
    }

    const fmsgref = body.querySelector("#f-msgref");
    if (fmsgref) {
      fmsgref.addEventListener("change", () => {
        const med = messageDefOf(element.businessObject);
        if (!med) return;
        const v = fmsgref.value;
        savePreservingPanel(() => {
          if (v === "__new__") {
            linkMessage(modeler, element, med, createMessage(modeler, ""));
          } else if (v === "") {
            linkMessage(modeler, element, med, null);
          } else {
            linkMessage(modeler, element, med, listMessages(modeler).find((m) => m.id === v));
          }
        });
        show(element); // re-render so the name/key fields match the chosen message
      });
    }
    const fmsgname = body.querySelector("#f-msgname");
    if (fmsgname) {
      fmsgname.addEventListener("change", () => {
        const med = messageDefOf(element.businessObject);
        if (med && med.messageRef) med.messageRef.name = (fmsgname.value || "").trim();
      });
    }
    const fcorrkey = body.querySelector("#f-corrkey");
    if (fcorrkey) {
      fcorrkey.addEventListener("change", () => {
        const med = messageDefOf(element.businessObject);
        if (med && med.messageRef) setMessageCorrelationKey(modeler, med.messageRef, (fcorrkey.value || "").trim());
      });
    }

    const fcond = body.querySelector("#f-cond");
    if (fcond) {
      fcond.addEventListener("change", () => savePreservingPanel(() => {
        const raw = (fcond.value || "").trim();
        const beo = element.businessObject;
        const prevCond = ((beo.conditionExpression && beo.conditionExpression.body) || "").replace(/^=\s*/, "").trim();
        const curName = (beo.name || "").trim();
        // The flow's diagram label mirrors its condition — so a conditional branch
        // shows its guard on the canvas — unless the modeler gave it a distinct
        // descriptive label (then that label is left alone).
        const mirrors = curName === "" || curName === prevCond;
        const props = {};
        if (raw === "") {
          // Clearing the field removes the guard, turning the branch unconditional;
          // an auto-derived label goes with it.
          props.conditionExpression = undefined;
          if (mirrors && curName !== "") props.name = "";
        } else {
          // Store as a FEEL expression, '=' prefixed per Zeebe (Atlas strips it).
          const moddle = modeler.get("moddle");
          const expr = moddle.create("bpmn:FormalExpression", {
            body: raw.startsWith("=") ? raw : "= " + raw,
          });
          expr.$parent = beo;
          props.conditionExpression = expr;
          const plain = raw.replace(/^=\s*/, "");
          if (mirrors && curName !== plain) props.name = plain;
        }
        try { modeling.updateProperties(element, props); } catch { /* stale */ }
      }));
    }

    // Upgrade every FEEL field in this panel into a code editor (highlighting +
    // completion + live validation + a Test panel). The textareas keep their
    // identity, so the change-to-save handlers wired above are untouched.
    // Validation compiles the expression against the same engine deploy uses
    // (POST /feel/validate); Test evaluates it (POST /feel/evaluate).
    if (tab === "implement") {
      const feelVars = collectFeelVariables(modeler);
      const validate = api ? (expression) => api("POST", "/api/v1/feel/validate", { expression }) : null;
      const evaluate = api ? (expression, variables) => api("POST", "/api/v1/feel/evaluate", { expression, variables }) : null;
      enhanceFeel(body, "#f-expr", feelVars, validate, evaluate);
      enhanceFeel(body, "#f-cond", feelVars, validate, evaluate);
      enhanceFeel(body, "#f-corrkey", feelVars, validate, evaluate);
    }
  }

  modeler.on("selection.changed", (e) => show((e.newSelection || [])[0]));
  modeler.on("element.changed", (e) => {
    if (suppressRerender) return; // a FEEL-field self-save; keep the panel intact
    const sel = selection.get();
    if (sel[0] && e.element && sel[0].id === e.element.id) show(sel[0]);
  });
  show(null);

  // Returned so a Design/Implement tab switch re-renders the current selection.
  return () => show(selection.get()[0]);
}

// parseStartVariables turns a start-variables textarea value into an instance
// request body, validating client-side so an obvious typo fails before a
// round-trip. Empty input means no variables. Values can be scalars (number,
// string, boolean, null) or structured (objects, arrays) — the server stores
// objects/arrays as canonical JSON under VarJSON (ADR-0037). Throws
// Error(message) on invalid input. Shared by the Modeler's Deploy & run and the
// Live view's Start instance.
export function parseStartVariables(raw) {
  const s = (raw || "").trim();
  if (!s) return {};
  let obj;
  try { obj = JSON.parse(s); }
  catch (e) { throw new Error("not valid JSON: " + e.message); }
  if (obj === null || typeof obj !== "object" || Array.isArray(obj)) {
    throw new Error('expected a JSON object, e.g. { "amount": 100, "customer": { "name": "acme" } }');
  }
  return { variables: obj };
}

// typedDeployFieldHTML renders one typed input for a declared start variable,
// prefilled with its default. A boolean is a true/false/— select; a number an
// input[type=number]; a string a text input; a json a textarea upgraded to a
// JSON editor after insertion. Required fields carry data-required so
// readTypedDeployBody can enforce them.
function typedDeployFieldHTML(v) {
  const req = v.required ? ' <span class="req-star" title="required">*</span>' : "";
  const dr = v.required ? ' data-required="1"' : "";
  const cap = `<span>${esc(v.name)} <span class="muted">(${v.type})</span>${req}</span>`;
  let input;
  if (v.type === "boolean") {
    const d = v.default;
    input = `<select class="dv-field" data-name="${esc(v.name)}" data-type="boolean"${dr}>
      <option value=""${d === "" ? " selected" : ""}>—</option>
      <option value="true"${d === "true" ? " selected" : ""}>true</option>
      <option value="false"${d === "false" ? " selected" : ""}>false</option>
    </select>`;
  } else if (v.type === "number") {
    input = `<input type="number" step="any" class="dv-field" data-name="${esc(v.name)}" data-type="number"${dr} value="${esc(v.default)}" placeholder="0"/>`;
  } else if (v.type === "json") {
    // A textarea, upgraded to a JSON editor by wireDeployJSONEditors() after
    // the panel is inserted into the DOM.
    input = `<textarea class="dv-field dv-json" data-name="${esc(v.name)}" data-type="json"${dr} rows="3" spellcheck="false" placeholder='{ "key": "value" }'>${esc(v.default)}</textarea>`;
  } else {
    input = `<input type="text" class="dv-field" data-name="${esc(v.name)}" data-type="string"${dr} value="${esc(v.default)}"/>`;
  }
  return `<label class="field">${cap}${input}</label>`;
}

// startVarsFormHTML is the body of a start-variables panel — the editor's
// Deploy & run and the Live view's Start instance both use it: a typed form when
// the process declares start variables, otherwise a free-form JSON textarea.
// readStartFormBody reads back whichever was rendered.
function startVarsFormHTML(declared) {
  if (!declared.length) {
    return `<label class="field">
      <span>Start variables — optional. A JSON object the instance starts with. Scalar and structured values (objects, arrays) are supported. Leave empty to start with none.</span>
      <textarea class="sv-json" rows="3" spellcheck="false" placeholder='{ "amount": 100, "customer": { "name": "acme", "tags": ["a", "b"] } }'></textarea>
    </label>`;
  }
  return `<p class="muted" style="font-size:12px;margin:0 0 8px">Start variables for this run — declared on the process. Required are marked <span class="req-star">*</span>; leave an optional one blank to omit it.</p>`
    + declared.map(typedDeployFieldHTML).join("");
}

// readStartFormBody turns a rendered start-variables panel into an instance
// request body — parsing the JSON textarea, or coercing the typed fields when a
// declaration produced them. Throws Error(message) on invalid input.
function readStartFormBody(bodyEl) {
  const json = bodyEl.querySelector(".sv-json");
  return json ? parseStartVariables(json.value) : readTypedDeployBody(bodyEl);
}

// readTypedDeployBody turns the typed Deploy form into an instance request body,
// coercing each field to its declared type and enforcing required fields. Empty
// optional fields are omitted. Throws Error(message) on a bad number, invalid
// JSON, or a missing required value.
function readTypedDeployBody(bodyEl) {
  const vars = {};
  const missing = [];
  bodyEl.querySelectorAll(".dv-field").forEach((el) => {
    const { name, type } = el.dataset;
    const raw = el.value.trim();
    if (raw === "") { if (el.dataset.required === "1") missing.push(name); return; }
    if (type === "number") {
      const n = Number(raw);
      if (!Number.isFinite(n)) throw new Error(`"${name}" must be a number`);
      vars[name] = n;
    } else if (type === "boolean") {
      vars[name] = raw === "true";
    } else if (type === "json") {
      try {
        vars[name] = JSON.parse(raw);
      } catch (e) {
        throw new Error(`"${name}": invalid JSON — ${e.message}`);
      }
    } else {
      vars[name] = raw;
    }
  });
  if (missing.length) throw new Error(`required: ${missing.join(", ")}`);
  return Object.keys(vars).length ? { variables: vars } : {};
}

// wireDeployJSONEditors upgrades every json-typed textarea (.dv-json) and the
// free-form start-variables textarea (.sv-json) in the given container to a
// professional JSON editor. Called after startVarsFormHTML inserts the panel
// HTML, so the DOM elements exist.
function wireDeployJSONEditors(container) {
  for (const ta of container.querySelectorAll(".dv-json, .sv-json")) {
    attachJSONEditor(ta, { rows: 3 });
  }
}

function wireActions(root, modeler, api, toast) {
  // Save persists the diagram as a draft (raw XML, no compile), keyed by process
  // id, so incomplete work survives and can be reopened from the Modeler home.
  const saveBtn = root.querySelector("#save");
  saveBtn.addEventListener("click", async () => {
    saveBtn.disabled = true;
    try {
      const { xml } = await modeler.saveXML({ format: true });
      const d = await api("POST", "/api/v1/drafts", xml, true);
      root.querySelector(".crumbs").textContent = d.name || d.processId || "Draft";
      toast(`Saved draft “${d.name || d.processId}”`, "ok");
    } catch (e) {
      toast("save failed: " + e.message, "err");
    } finally {
      saveBtn.disabled = false;
    }
  });

  root.querySelector("#export").addEventListener("click", async () => {
    try {
      const { xml } = await modeler.saveXML({ format: true });
      const blob = new Blob([xml], { type: "application/xml" });
      const a = document.createElement("a");
      a.href = URL.createObjectURL(blob);
      a.download = "diagram.bpmn";
      a.click();
      URL.revokeObjectURL(a.href);
    } catch (e) { toast("export failed: " + e.message, "err"); }
  });

  // Deploy & run opens a panel to (optionally) enter start variables, then
  // deploys the model and starts an instance seeded with them — the editor's
  // equivalent of the Live view's Start instance, so a process that needs input
  // can be launched and tested without leaving the Modeler. When the process
  // declares start variables (Process panel → Start variables) the panel shows a
  // typed form built from that declaration; otherwise a free-form JSON textarea.
  const deployBtn = root.querySelector("#deploy");
  const dpanel = root.querySelector("#deploy-panel");
  const dbody = root.querySelector("#deploy-body");
  const dgo = root.querySelector("#deploy-go");
  const derr = root.querySelector("#deploy-err");
  const closeDeploy = () => { dpanel.hidden = true; derr.textContent = ""; };

  // Read the declaration fresh each open — it can change while the editor is up.
  const openDeploy = () => {
    const bo = rootProcess(modeler);
    dbody.innerHTML = startVarsFormHTML(bo ? readStartVariables(bo) : []);
    wireDeployJSONEditors(dbody);
    dpanel.hidden = false;
    derr.textContent = "";
    const first = dbody.querySelector(".sv-json, .dv-field");
    if (first) first.focus();
  };

  deployBtn.addEventListener("click", () => { dpanel.hidden ? openDeploy() : closeDeploy(); });
  root.querySelector("#deploy-cancel").addEventListener("click", closeDeploy);

  dgo.addEventListener("click", async () => {
    let body;
    try {
      body = readStartFormBody(dbody);
    } catch (e) { derr.textContent = e.message; return; }
    dgo.disabled = true;
    derr.textContent = "";
    try {
      const { xml } = await modeler.saveXML({ format: true });
      const dep = await api("POST", "/api/v1/deployments", xml, true);
      const all = dep.deployments || [{ key: dep.key, processId: dep.processId, version: dep.version }];
      if (all.length > 1) {
        // A collaboration deploys one definition per pool; which pool to start is
        // ambiguous, so just report what was deployed (start pools from Operations).
        // Start variables don't apply here — there's no single instance to seed.
        toast(`Deployed ${all.length} pools: ${all.map((d) => d.processId).join(", ")}`, "ok");
      } else {
        await api("POST", `/api/v1/processes/${dep.key}/instances`, body);
        const n = body.variables ? Object.keys(body.variables).length : 0;
        toast(`Deployed ${dep.processId} v${dep.version} and started an instance${n ? ` with ${n} variable${n === 1 ? "" : "s"}` : ""}`, "ok");
      }
      closeDeploy();
    } catch (e) {
      // The Atlas compiler rejects elements it can't execute yet — surface that
      // inline in the panel so the entered variables aren't lost.
      derr.textContent = e.message;
    } finally {
      dgo.disabled = false;
    }
  });
}

// mountLive renders a deployed process read-only and overlays runtime state,
// polling for updates: elements holding a token right now are green, elements a
// token has only passed through are gray (the history heatmap), each badged with
// its count. This is the differentiator a standalone modeler can't offer — the
// diagram shows where the engine's tokens are now and the distribution of where
// they have flowed, so a finished process still tells its story.
//
// The view is organized around one process: a version picker swaps which deployed
// definition is shown, and an instance picker either aggregates every instance's
// tokens on the diagram or isolates a single one. The selected instance's
// variables are listed below the diagram.
export async function mountLive(root, { api, toast, key }) {
  cleanup();

  // Resolve the process this definition version belongs to, and all its versions,
  // so the version picker can offer them. One /processes call feeds both.
  let procName = `definition ${key}`;
  let versions = []; // [{key, version, name}], newest first
  try {
    const procs = await api("GET", "/api/v1/processes");
    const here = procs.find((x) => x.key === key);
    if (here) {
      procName = here.name || here.processId;
      versions = procs
        .filter((x) => x.processId === here.processId)
        .sort((a, b) => b.version - a.version);
    }
  } catch { /* header/version picker are best-effort */ }

  const versionOptions = versions.length
    ? versions.map((v) =>
        `<option value="${v.key}"${v.key === key ? " selected" : ""}>v${v.version}</option>`).join("")
    : `<option value="${key}" selected>current</option>`;

  root.innerHTML = `
    <div class="editor live">
      <div class="editor-bar">
        <a class="btn neutral" href="#/operations">&larr; Instances</a>
        <span class="crumbs" style="margin-left:8px">Live &middot; <b>${esc(procName)}</b></span>
        <label class="bar-select"><span>Version</span>
          <select id="version-sel">${versionOptions}</select></label>
        <label class="bar-select"><span>Instance</span>
          <select id="instance-sel"><option value="all">All instances</option></select></label>
        <div style="flex:1"></div>
        <button class="btn" id="start">Start instance</button>
        <button class="btn ghost danger" id="cancel-inst" hidden>Cancel instance</button>
        <button class="btn neutral" id="refresh">Refresh</button>
        <span class="pill ok" style="margin-left:8px"><span class="dot"></span><b id="inst-count">0</b>&nbsp;running</span>
        <span class="pill" style="margin-left:8px"><b id="token-count">0</b>&nbsp;tokens total</span>
      </div>
      <div class="start-panel" id="start-panel" hidden>
        <div id="start-body"></div>
        <div class="row">
          <button class="btn" id="start-go">Start instance</button>
          <button class="btn neutral" id="start-cancel">Cancel</button>
          <span class="err" id="start-err"></span>
        </div>
      </div>
      <div class="editor-body">
        <div id="canvas"></div>
      </div>
      <div class="var-panel" id="var-panel"></div>
      <div class="problems">
        <span class="legend-swatch live"></span> live token
        <span class="legend-swatch history" style="margin-left:12px"></span> passed through
        <span class="badge" style="margin-left:12px">N</span> token count
        <span style="flex:1"></span>
        <span class="muted">Polling every 1.5s</span>
      </div>
    </div>`;

  // Switching version loads a different deployed definition, so re-route to its
  // live view (a full remount — the diagram itself changes).
  root.querySelector("#version-sel").addEventListener("change", (e) => {
    const next = Number(e.target.value);
    if (next && next !== key) location.hash = `#/operations/p/${next}`;
  });

  let lib;
  try {
    lib = await loadBpmn();
  } catch (e) {
    root.querySelector("#canvas").innerHTML = `<div class="coming"><p>${esc(e.message)}</p></div>`;
    return;
  }

  const viewer = newModeler(lib.BpmnJS, lib.moddle, root.querySelector("#canvas"));
  current = viewer;

  try {
    const xml = await api("GET", `/api/v1/processes/${key}/xml`);
    await viewer.importXML(typeof xml === "string" ? xml : String(xml));
    viewer.get("canvas").zoom("fit-viewport");
  } catch (e) {
    root.querySelector("#canvas").innerHTML =
      `<div class="coming"><p>Could not render this model.</p>
       <p class="muted">${esc(e.message)}</p></div>`;
    return;
  }

  const canvas = viewer.get("canvas");
  const overlays = viewer.get("overlays");
  const registry = viewer.get("elementRegistry");
  const countEl = root.querySelector("#inst-count");
  const tokenEl = root.querySelector("#token-count");
  const instSel = root.querySelector("#instance-sel");
  const varPanel = root.querySelector("#var-panel");
  let marked = [];
  let selected = "all";     // "all" or an instance key (as a string)
  let instances = [];       // this version's instances, cached for the picker/variables
  let instSig = "";         // signature of the picker's current option set

  // refreshInstances pulls this version's instances and, only when the set of
  // instances (or their state) actually changed, rebuilds the picker — so the
  // operator's current selection isn't reset on every poll. Newest activity first.
  async function refreshInstances() {
    let all;
    try { all = await api("GET", "/api/v1/instances"); }
    catch { return; } // transient; the picker just keeps its current options
    instances = all
      .filter((r) => r.processDefKey === key)
      .sort((a, b) => (a.state === b.state ? b.key - a.key : a.state === "active" ? -1 : 1));
    const sig = instances.map((r) => `${r.key}:${r.state}`).join(",");
    if (sig === instSig) return;
    instSig = sig;
    // Drop a selection that no longer exists (e.g. its definition was deleted).
    if (selected !== "all" && !instances.some((r) => String(r.key) === selected)) selected = "all";
    instSel.innerHTML =
      `<option value="all"${selected === "all" ? " selected" : ""}>All instances (${instances.length})</option>` +
      instances.map((r) =>
        `<option value="${r.key}"${String(r.key) === selected ? " selected" : ""}>${r.key} · ${esc(r.state)}</option>`
      ).join("");
  }

  // JSON variable values are shown with a collapsible preview (first 60 chars)
  // instead of blowing out the chip; hover to see the full value.
  const varChips = (list) => !list || !list.length
    ? '<span class="muted">No variables.</span>'
    : list.map((v) => {
        if (v.kind === "json") {
          const preview = v.value.length > 60 ? v.value.slice(0, 57) + "..." : v.value;
          return `<span class="chip" title="${esc(v.value)}">${esc(v.name)}=<code>${esc(preview)}</code></span>`;
        }
        return `<span class="chip">${esc(v.name)}=${esc(v.value)}</span>`;
      }).join(" ");
  // completedAt is unix nanoseconds; Date wants milliseconds.
  const fmtNano = (ns) => ns ? new Date(ns / 1e6).toLocaleString() : "";

  // renderVariables shows the selected instance's variables, or — for "All
  // instances" — a compact per-instance table, beneath the diagram.
  function renderVariables() {
    if (selected === "all") {
      if (!instances.length) {
        varPanel.innerHTML = `<div class="vp-head">Variables</div>
          <p class="muted" style="margin:0">No instances yet — start one to see its variables here.</p>`;
        return;
      }
      varPanel.innerHTML = `<div class="vp-head">Variables · all instances</div>
        <table class="vp-table"><tbody>${instances.map((r) => `
          <tr><td><b>${r.key}</b></td>
            <td>${r.state === "active"
              ? '<span class="pill ok"><span class="dot"></span>active</span>'
              : `<span class="pill">${esc(r.state)}</span>`}</td>
            <td>${varChips(r.variables)}</td></tr>`).join("")}</tbody></table>`;
      return;
    }
    const inst = instances.find((r) => String(r.key) === selected);
    if (!inst) { varPanel.innerHTML = `<div class="vp-head">Variables</div>
      <p class="muted" style="margin:0">Instance no longer available.</p>`; return; }
    const when = inst.state === "active" ? "" : fmtNano(inst.completedAt);
    varPanel.innerHTML = `<div class="vp-head">Variables · instance ${inst.key}
        ${inst.state === "active"
          ? '<span class="pill ok"><span class="dot"></span>active</span>'
          : `<span class="pill">${esc(inst.state)}</span>${when ? ` <span class="muted">${esc(when)}</span>` : ""}`}
      </div>
      <div>${varChips(inst.variables)}</div>`;
  }

  async function poll() {
    await refreshInstances();
    updateCancelBtn();
    const q = selected === "all" ? "" : `?instance=${encodeURIComponent(selected)}`;
    let rt;
    try { rt = await api("GET", `/api/v1/processes/${key}/runtime${q}`); }
    catch (e) { return; } // transient; try again next tick
    if (current !== viewer) return; // navigated away mid-flight
    overlays.clear();
    for (const [id, marker] of marked) canvas.removeMarker(id, marker);
    marked = [];
    // Each element is drawn in one of two states: green if it holds a live token
    // right now, gray if tokens have only passed through it (history). Together
    // they show the flow distribution even once every instance has finished — a
    // gray trail with green where tokens are still alive.
    for (const e of rt.elements) {
      if (!registry.get(e.elementId)) continue;
      const live = e.tokens > 0;
      if (!live && !(e.visits > 0)) continue;
      const marker = live ? "atlas-active" : "atlas-visited";
      canvas.addMarker(e.elementId, marker);
      marked.push([e.elementId, marker]);
      const count = live ? e.tokens : e.visits;
      const title = live
        ? `${e.tokens} live token(s)`
        : `${e.visits} token(s) passed through`;
      overlays.add(e.elementId, "tokens", {
        position: { bottom: 4, right: 4 },
        html: `<div class="token-badge${live ? "" : " history"}" title="${title}">${count}</div>`,
      });
    }
    countEl.textContent = rt.instances;
    tokenEl.textContent = rt.tokens;
    renderVariables();
  }

  // The Cancel button targets the selected instance; it is shown only when a
  // single, still-active instance is selected (there is nothing to cancel for
  // "All instances" or an already-finished one).
  const cancelBtn = root.querySelector("#cancel-inst");
  function updateCancelBtn() {
    const inst = instances.find((r) => String(r.key) === selected);
    cancelBtn.hidden = !(inst && inst.state === "active");
  }

  // Selecting an instance isolates it on the diagram; re-poll right away so the
  // overlay and variables switch without waiting for the next tick.
  instSel.addEventListener("change", () => { selected = instSel.value; updateCancelBtn(); poll(); });

  cancelBtn.addEventListener("click", async () => {
    if (selected === "all") return;
    if (!window.confirm(`Cancel (terminate) instance ${selected}? Its token is discarded and it moves to the finished list as "terminated".`)) return;
    cancelBtn.disabled = true;
    try {
      await api("DELETE", `/api/v1/instances/${selected}`);
      toast(`Instance ${selected} terminated`, "ok");
      await refreshInstances();
      await poll();
      updateCancelBtn();
    } catch (e) {
      toast("cancel failed: " + e.message, "err");
    } finally {
      cancelBtn.disabled = false;
    }
  });

  root.querySelector("#refresh").addEventListener("click", poll);

  // Start a fresh instance of this already-deployed definition. The demo and the
  // Modeler's "Deploy & run" both couple starting to a deployment; this is the
  // path for a model that's already live — start it again straight from its view,
  // optionally seeded with start variables entered in the panel below the toolbar.
  const startBtn = root.querySelector("#start");
  const panel = root.querySelector("#start-panel");
  const startBody = root.querySelector("#start-body");
  const goBtn = root.querySelector("#start-go");
  const errEl = root.querySelector("#start-err");
  const closePanel = () => { panel.hidden = true; errEl.textContent = ""; };

  // When the deployed model declares start variables (atlas:startForm), show the
  // same typed form the Modeler's Deploy & run uses; otherwise a JSON textarea.
  // The declaration is read from the rendered definition, fresh each open.
  const openPanel = () => {
    const bo = rootProcess(viewer);
    startBody.innerHTML = startVarsFormHTML(bo ? readStartVariables(bo) : []);
    wireDeployJSONEditors(startBody);
    panel.hidden = false;
    errEl.textContent = "";
    const first = startBody.querySelector(".sv-json, .dv-field");
    if (first) first.focus();
  };

  startBtn.addEventListener("click", () => { panel.hidden ? openPanel() : closePanel(); });
  root.querySelector("#start-cancel").addEventListener("click", closePanel);

  goBtn.addEventListener("click", async () => {
    let body;
    try { body = readStartFormBody(startBody); }
    catch (e) { errEl.textContent = e.message; return; }
    goBtn.disabled = true;
    try {
      await api("POST", `/api/v1/processes/${key}/instances`, body);
      const n = body.variables ? Object.keys(body.variables).length : 0;
      toast(n ? `Started a new instance with ${n} variable${n === 1 ? "" : "s"}` : "Started a new instance", "ok");
      closePanel();
      await poll();
    } catch (e) {
      errEl.textContent = e.message;
    } finally {
      goBtn.disabled = false;
    }
  });

  await poll();
  liveTimer = setInterval(poll, 1500);
}
