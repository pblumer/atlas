// BPMN editor view. Embeds the vendored bpmn-js modeler (ADR-0013): the canvas,
// palette, and context pad come from bpmn-js; the Details panel and Deploy
// wiring are ours. Assets load lazily so non-editor pages stay light.

import { attachFeelEditor } from "./feel.js";
import { attachJSONEditor } from "./json-editor.js";
import { openDmnEditor } from "./dmn-editor.js";

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

// --- Variable presentation (shared by the live and replay views) ---
//
// Operators inspect an instance's variables, which can be whole JSON structures.
// These render them clearly: scalars as labeled, type-colored fields; JSON values
// as collapsible, syntax-highlighted, pretty-printed cards.

// highlightJSON pretty-prints a canonical JSON string and wraps its tokens in
// colored spans (keys, strings, numbers, booleans, null) — a tiny, dependency-
// free syntax highlighter (ADR-0012 buildless UI). It escapes first, then spans.
function highlightJSON(text) {
  let out;
  try { out = JSON.stringify(JSON.parse(text), null, 2); }
  catch { return esc(text); } // not valid JSON after all — show it verbatim
  out = out.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
  return out.replace(
    /("(?:\\.|[^"\\])*"\s*:?|\btrue\b|\bfalse\b|\bnull\b|-?\d+(?:\.\d+)?(?:[eE][+\-]?\d+)?)/g,
    (m) => {
      let cls = "j-num";
      if (m[0] === '"') cls = /:\s*$/.test(m) ? "j-key" : "j-str";
      else if (m === "true" || m === "false") cls = "j-bool";
      else if (m === "null") cls = "j-null";
      return `<span class="${cls}">${m}</span>`;
    });
}

// jsonSummary is the compact one-liner shown on a collapsed JSON card.
function jsonSummary(text) {
  try {
    const v = JSON.parse(text);
    if (Array.isArray(v)) return `${v.length} item${v.length === 1 ? "" : "s"}`;
    if (v && typeof v === "object") {
      const n = Object.keys(v).length;
      return `${n} field${n === 1 ? "" : "s"}`;
    }
    return String(v);
  } catch { return ""; }
}

// renderVarsBody lays out a variable list: scalars as a labeled field grid, JSON
// values as collapsible, syntax-highlighted cards. collapsed is a Set of the JSON
// variable names the operator has collapsed (persists across re-renders); the
// caller wires a delegated click on `.vj-head` to toggle it and re-render.
function renderVarsBody(list, collapsed) {
  if (!list || !list.length) return '<span class="muted">No variables yet.</span>';
  const scalars = list.filter((v) => v.kind !== "json");
  const jsons = list.filter((v) => v.kind === "json");
  let html = "";
  if (scalars.length) {
    html += `<div class="vgrid">${scalars.map((v) => {
      const cls = v.kind === "boolean" ? "bool" : v.kind === "number" ? "num" : v.kind === "null" ? "null" : "str";
      return `<div class="vfield"><div class="vf-head"><span class="vk">${esc(v.name)}</span><span class="vtag">${esc(v.kind)}</span>${copyBtn(v.value, "Copy value")}</div>
        <div class="vv ${cls}">${esc(v.value)}</div></div>`;
    }).join("")}</div>`;
  }
  html += jsons.map((v) => {
    const isCollapsed = collapsed.has(v.name);
    return `<div class="vjson${isCollapsed ? " collapsed" : ""}">
      <div class="vj-head">
        <button class="vj-toggle" data-name="${esc(v.name)}" aria-expanded="${isCollapsed ? "false" : "true"}">
          <span class="chev">&#9662;</span><span class="vk">${esc(v.name)}</span>
          <span class="vtag">json</span><span class="vj-sum">${esc(jsonSummary(v.value))}</span>
        </button>
        ${copyBtn(prettyJSON(v.value), "Copy JSON")}
      </div>
      <pre class="vj-body">${highlightJSON(v.value)}</pre>
    </div>`;
  }).join("");
  return html;
}

// copyBtn renders a small clipboard button carrying the raw text to copy in a data
// attribute; a delegated handler (bindVarCopy) does the actual write. Operators
// live in these values, so every scalar, every JSON blob and the whole set are
// one click from the clipboard.
function copyBtn(text, title) {
  return `<button class="vcopy" type="button" title="${esc(title)}" aria-label="${esc(title)}" data-copy="${esc(text)}">
    <svg viewBox="0 0 16 16" width="13" height="13" aria-hidden="true"><path d="M5.5 1.5h6a1 1 0 0 1 1 1v8" fill="none" stroke="currentColor" stroke-width="1.3"/><rect x="3.5" y="4.5" width="8" height="10" rx="1" fill="none" stroke="currentColor" stroke-width="1.3"/></svg>
  </button>`;
}

// prettyJSON canonicalizes a JSON variable's stored string to pretty-printed form
// for copying (falling back to the raw text if it isn't valid JSON), so what lands
// on the clipboard matches what the operator sees in the card.
function prettyJSON(text) {
  try { return JSON.stringify(JSON.parse(text), null, 2); }
  catch { return String(text); }
}

// copyText writes text to the clipboard, falling back to a hidden textarea +
// execCommand where the async Clipboard API isn't available (older browsers, or
// insecure origins). Returns a promise that resolves once the copy is attempted.
async function copyText(text) {
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch { /* fall through to the legacy path */ }
  try {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.select();
    const ok = document.execCommand("copy");
    document.body.removeChild(ta);
    return ok;
  } catch { return false; }
}

// bindVarCopy wires the delegated click on `.vcopy` buttons inside panel: it copies
// the button's data-copy payload and flashes a brief "Copied" state on the button.
// Attach once per view; survives the panel's inner re-renders.
function bindVarCopy(panel, toast) {
  panel.addEventListener("click", async (e) => {
    const btn = e.target.closest(".vcopy");
    if (!btn || !panel.contains(btn)) return;
    e.preventDefault();
    e.stopPropagation();
    const ok = await copyText(btn.dataset.copy || "");
    if (ok) {
      btn.classList.add("copied");
      setTimeout(() => btn.classList.remove("copied"), 900);
    } else if (toast) {
      toast("Copy failed", "err");
    }
  });
}

// bindJsonCards wires the delegated expand/collapse click for JSON cards inside
// panel, toggling names in collapsed and calling rerender. Attach once per view.
function bindJsonCards(panel, collapsed, rerender) {
  panel.addEventListener("click", (e) => {
    const head = e.target.closest(".vj-toggle");
    if (!head || !panel.contains(head)) return;
    const name = head.dataset.name;
    if (collapsed.has(name)) collapsed.delete(name);
    else collapsed.add(name);
    rerender();
  });
}

export async function mountEditor(root, { api, toast, key, draftId, projectId }) {
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
        <button class="btn neutral" id="vars-toggle" title="Show the variables this diagram writes">Variables</button>
        <button class="btn neutral" id="save">Save</button>
        <button class="btn neutral" id="export">Export XML</button>
        <button class="btn" id="deploy">Deploy</button>
      </div>
      <div class="start-panel" id="deploy-panel" hidden>
        <div id="deploy-body"></div>
        <div class="row" id="deploy-actions">
          <button class="btn" id="deploy-run">Deploy &amp; run</button>
          <button class="btn neutral" id="deploy-only">Deploy only</button>
          <button class="btn neutral" id="deploy-cancel">Cancel</button>
          <span class="err" id="deploy-err"></span>
        </div>
      </div>
      <div class="editor-body">
        <div id="canvas"></div>
        <aside class="vars-panel" id="vars-panel" hidden>
          <div class="vars-head"><b>Variables</b>
            <button class="icon-btn" id="vars-close" title="Close" aria-label="Close">✕</button></div>
          <input class="vars-filter" id="vars-filter" placeholder="Filter by name or origin…"/>
          <div class="vars-list" id="vars-list"></div>
        </aside>
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

  const rerender = wireProperties(root, modeler, api, projectId, toast);
  wireTabs(root, rerender);
  wireActions(root, modeler, api, toast, projectId);
  wireEditorVars(root, modeler);
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

// wireVarsPanel makes the Live view's variables panel a first-class side panel like
// the Modeler's properties: a toolbar toggle collapses it out of the way (operators
// who want the diagram full-width), and a draggable divider widens it for reading
// long JSON. Both choices persist across mounts (localStorage), and the bpmn-js
// canvas is nudged to re-fit whenever the panel's footprint changes.
function wireVarsPanel(root, viewer) {
  const editor = root.querySelector(".editor.live");
  const panel = root.querySelector("#var-panel");
  const resizer = root.querySelector("#var-resizer");
  const toggle = root.querySelector("#vars-toggle");
  if (!editor || !panel) return;
  const WKEY = "atlas.varsWidth";
  const CKEY = "atlas.varsCollapsed";
  const clamp = (w) => Math.max(240, Math.min(900, w));

  const savedW = parseInt(localStorage.getItem(WKEY) || "", 10);
  if (savedW) panel.style.width = clamp(savedW) + "px";

  // Let bpmn-js recompute its viewport for the canvas's new width.
  const nudge = () => {
    try { viewer && viewer.get("canvas").resized(); } catch { /* ignore */ }
    window.dispatchEvent(new Event("resize"));
  };

  const applyCollapsed = (collapsed) => {
    editor.classList.toggle("vars-collapsed", collapsed);
    if (toggle) toggle.setAttribute("aria-pressed", collapsed ? "false" : "true");
    nudge();
  };
  applyCollapsed(localStorage.getItem(CKEY) === "1");

  if (toggle) {
    toggle.addEventListener("click", () => {
      const collapsed = !editor.classList.contains("vars-collapsed");
      localStorage.setItem(CKEY, collapsed ? "1" : "0");
      applyCollapsed(collapsed);
    });
  }

  if (resizer) {
    let startX = 0, startW = 0;
    const onMove = (e) => {
      // The panel is on the right, so dragging the divider left widens it.
      panel.style.width = clamp(startW - (e.clientX - startX)) + "px";
    };
    const onUp = () => {
      document.removeEventListener("pointermove", onMove);
      document.removeEventListener("pointerup", onUp);
      resizer.classList.remove("dragging");
      document.body.style.userSelect = "";
      localStorage.setItem(WKEY, String(parseInt(panel.style.width, 10) || 320));
      nudge();
    };
    resizer.addEventListener("pointerdown", (e) => {
      e.preventDefault();
      startX = e.clientX;
      startW = panel.getBoundingClientRect().width;
      resizer.classList.add("dragging");
      document.body.style.userSelect = "none";
      document.addEventListener("pointermove", onMove);
      document.addEventListener("pointerup", onUp);
    });
    // Double-click the divider to reset to the default width.
    resizer.addEventListener("dblclick", () => {
      panel.style.width = "320px";
      localStorage.setItem(WKEY, "320");
      nudge();
    });
  }
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
      // A PowerShell (job) script task writes its result into a variable too, so
      // offer it for completion just like a FEEL script result (ADR-0047).
      const js = findExt(el.businessObject, "atlas:JobScript");
      if (js && js.resultVariable) vars.add(js.resultVariable);
      // A business rule task's decision result is a variable downstream elements
      // can read, so offer it for completion just like a script result.
      const cd = findExt(el.businessObject, "zeebe:CalledDecision");
      if (cd && cd.resultVariable) vars.add(cd.resultVariable);
    });
  } catch { /* best-effort */ }
  return [...vars].sort();
}

// collectDiagramVariables statically analyses the diagram for the variables it
// writes and where — the data behind the Variables panel (like Camunda's). Each
// entry is { name, origin, originId, source }: the variable name, the element that
// writes it (name/id and the id to select it), and how (start variable, script
// result, decision result, output mapping). De-duplicated by name, sorted.
function collectDiagramVariables(modeler) {
  const out = [];
  const seen = new Set();
  const push = (name, origin, originId, source) => {
    name = (name || "").trim();
    if (!name || seen.has(name)) return;
    seen.add(name);
    out.push({ name, origin, originId, source });
  };
  try {
    const rootBo = rootProcess(modeler);
    if (rootBo) {
      for (const v of readStartVariables(rootBo)) {
        push(v.name, "Start", "", "start variable" + (v.type ? " · " + v.type : ""));
      }
    }
  } catch { /* best-effort */ }
  try {
    modeler.get("elementRegistry").forEach((el) => {
      const bo = el.businessObject;
      if (!bo) return;
      const label = bo.name || bo.id;
      const s = findExt(bo, "zeebe:Script");
      if (s && s.resultVariable) push(s.resultVariable, label, bo.id, "FEEL script");
      const js = findExt(bo, "atlas:JobScript");
      if (js && js.resultVariable) push(js.resultVariable, label, bo.id, (js.language || "job") + " script");
      const cd = findExt(bo, "zeebe:CalledDecision");
      if (cd && cd.resultVariable) push(cd.resultVariable, label, bo.id, "decision result");
      const io = findExt(bo, "zeebe:IoMapping");
      for (const p of (io && io.outputParameters) || []) push(p.target, label, bo.id, "output mapping");
    });
  } catch { /* best-effort */ }
  return out.sort((a, b) => a.name.localeCompare(b.name));
}

// wireEditorVars drives the design-time Variables panel: a toolbar toggle opens it,
// it lists the diagram's variables (origin is click-to-select), a filter narrows
// the list, and it refreshes as the diagram changes while open — a modeling aid
// that answers "what variables exist here and who writes them" without running
// anything. (The live view has its own runtime-variables panel, wireVarsPanel.)
function wireEditorVars(root, modeler) {
  const panel = root.querySelector("#vars-panel");
  const toggle = root.querySelector("#vars-toggle");
  const closeBtn = root.querySelector("#vars-close");
  const filter = root.querySelector("#vars-filter");
  const list = root.querySelector("#vars-list");
  if (!panel || !toggle || !list) return;

  const render = () => {
    if (panel.hidden) return;
    const q = (filter.value || "").trim().toLowerCase();
    const vars = collectDiagramVariables(modeler).filter((v) =>
      !q || v.name.toLowerCase().includes(q) || (v.origin || "").toLowerCase().includes(q));
    if (!vars.length) {
      list.innerHTML = `<p class="vars-empty">${q ? "No matching variables." :
        "No variables yet. They appear as you add start variables, script or decision result variables, and output mappings."}</p>`;
      return;
    }
    list.innerHTML = vars.map((v) => `<div class="var-row">
      <div class="var-name">${esc(v.name)}</div>
      <div class="var-meta">${esc(v.source)}${v.originId
        ? ` · written by <span class="var-origin" data-el="${esc(v.originId)}">${esc(v.origin)}</span>`
        : ` · ${esc(v.origin)}`}</div></div>`).join("");
  };

  toggle.addEventListener("click", () => {
    panel.hidden = !panel.hidden;
    toggle.classList.toggle("active", !panel.hidden);
    render();
  });
  if (closeBtn) closeBtn.addEventListener("click", () => {
    panel.hidden = true;
    toggle.classList.remove("active");
  });
  filter.addEventListener("input", render);
  list.addEventListener("click", (e) => {
    const o = e.target.closest(".var-origin");
    if (!o) return;
    const el = modeler.get("elementRegistry").get(o.dataset.el);
    if (el) {
      try { modeler.get("selection").select(el); } catch { /* stale */ }
      try { modeler.get("canvas").scrollToElement(el); } catch { /* older bpmn-js */ }
    }
  });
  // Keep the open panel current as the diagram is edited.
  modeler.on("element.changed", render);
  modeler.on("elements.changed", render);
  modeler.on("import.done", render);
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

// removeExt drops an element's extension element of `type`, if present, through
// the modeling API (undo/redo aware). Used to unlink a form (drop the whole
// zeebe:FormDefinition rather than leave an empty formId behind).
function removeExt(modeler, element, type) {
  const modeling = modeler.get("modeling");
  const ext = element.businessObject.extensionElements;
  if (!ext || !ext.values) return;
  const next = ext.values.filter((v) => v.$type !== type);
  if (next.length === ext.values.length) return;
  ext.values = next;
  modeling.updateProperties(element, { extensionElements: ext });
}

// decisionInputRowHTML renders one editable business-rule-task input mapping: a
// decision input name (target) fed by a FEEL source over the instance's variables.
// The stored source is '=' prefixed (Zeebe convention); it is shown stripped.
function decisionInputRowHTML(i, source, target) {
  const src = (source || "").replace(/^=\s*/, "");
  return `<div class="dmn-input-row" data-i="${i}" style="display:flex;gap:6px;margin-bottom:6px">
    <input type="text" class="dmn-in-target" value="${esc(target || "")}" placeholder="Season" style="flex:0 0 34%" title="Decision input name"/>
    <input type="text" class="dmn-in-source" value="${esc(src)}" placeholder="order.season" style="flex:1" title="FEEL source over the instance's variables"/>
  </div>`;
}

// saveDecisionInputs rebuilds a business rule task's zeebe:ioMapping input list
// from the panel rows. A row with no target is dropped (an incomplete row); each
// kept source is stored as a '=' prefixed FEEL expression. An empty list removes
// the ioMapping element entirely so the model stays clean.
function saveDecisionInputs(modeler, element, rows) {
  const moddle = modeler.get("moddle");
  const modeling = modeler.get("modeling");
  const bo = element.businessObject;
  let ext = bo.extensionElements;
  if (!ext) {
    ext = moddle.create("bpmn:ExtensionElements", { values: [] });
    ext.$parent = bo;
  }
  let io = (ext.values || []).find((v) => v.$type === "zeebe:IoMapping");
  const params = rows
    .filter((r) => r.target !== "")
    .map((r) => moddle.create("zeebe:Input", {
      source: r.source === "" ? "" : (r.source.startsWith("=") ? r.source : "= " + r.source),
      target: r.target,
    }));
  if (params.length === 0) {
    if (io) ext.values = (ext.values || []).filter((v) => v !== io);
  } else {
    if (!io) {
      io = moddle.create("zeebe:IoMapping");
      io.$parent = ext;
      ext.values = [...(ext.values || []), io];
    }
    params.forEach((p) => (p.$parent = io));
    io.inputParameters = params;
  }
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

// The three schedule kinds a TimerEventDefinition can carry, mapped to the
// bpmn element each writes and how the panel labels it. A start event accepts
// all three; a catch or interrupting boundary fires once, so they omit the
// recurring cycle (ADR-0051/0054). Iteration order (duration, date, cycle) is
// the detection precedence in timerScheduleOf.
const TIMER_KINDS = {
  duration: { prop: "timeDuration", label: "Duration", placeholder: "PT30M" },
  date: { prop: "timeDate", label: "Date & time", placeholder: "2026-08-01T09:00:00Z" },
  cycle: { prop: "timeCycle", label: "Cycle", placeholder: "R/PT1H  ·  0 * * * *" },
};

// timerScheduleOf reports which kind a timer currently holds and its expression
// text. It keys off which sub-element exists (not its body), so a just-switched,
// still-empty kind survives a re-render. Defaults to duration for a bare timer.
function timerScheduleOf(timer) {
  for (const [kind, meta] of Object.entries(TIMER_KINDS)) {
    if (timer && timer[meta.prop]) return { kind, body: timer[meta.prop].body || "" };
  }
  return { kind: "duration", body: "" };
}

// timerFieldsHTML renders the Type picker (restricted to the kinds valid in this
// context) and the schedule-expression input, followed by a context note.
function timerFieldsHTML(timer, kinds, note) {
  const detected = timerScheduleOf(timer);
  const kind = kinds.includes(detected.kind) ? detected.kind : kinds[0];
  const meta = TIMER_KINDS[kind];
  const body = (timer && timer[meta.prop] && timer[meta.prop].body) || "";
  const opts = kinds
    .map((k) => `<option value="${k}" ${k === kind ? "selected" : ""}>${TIMER_KINDS[k].label}</option>`)
    .join("");
  return `<h3>Timer</h3>
    <label class="field"><span>Type</span>
      <select id="f-timerkind">${opts}</select></label>
    <label class="field"><span>${meta.label}</span>
      <input type="text" id="f-timerval" value="${esc(body)}" placeholder="${esc(meta.placeholder)}"/></label>
    <p class="muted" style="font-size:12px">${note}</p>`;
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
//
// By default it targets the diagram's root process. A collaboration pool has no
// single root process — each pool executes its own — so a pool passes its shape
// (targetEl, the participant) and the process it references (targetBo) to land
// the declaration on that process. Referenced-object updates go through
// updateModdleProperties; the root shape keeps updateProperties (unchanged).
// dataRefName resolves a data association's source/target reference (a data object
// reference, which bpmn-js may hand back as a one-element list) to its display name:
// the reference's name, else the underlying data object's name, else its id.
function dataRefName(ref) {
  const r = Array.isArray(ref) ? ref[0] : ref;
  if (!r) return "?";
  return r.name || (r.dataObjectRef && r.dataObjectRef.name) || r.id || "?";
}

// processOf returns the bpmn:Process business object that contains element — its
// nearest Process ancestor, falling back to the diagram's single root process (for a
// collaboration a data object lives in the pool's process, reached via the parent).
function processOf(modeler, element) {
  let bo = element && element.businessObject;
  while (bo) {
    if (/:Process$/.test(bo.$type || "")) return bo;
    bo = bo.$parent;
  }
  return rootProcess(modeler);
}

// dataObjectNames lists a process's data objects distinct by name (a nameless one
// falls back to its id), each with a representative id to point a reference at. Used
// by the "Points to" selector so several references can share one logical object.
function dataObjectNames(procBo) {
  const out = [];
  if (!procBo) return out;
  for (const o of procBo.flowElements || []) {
    if (o.$type !== "bpmn:DataObject") continue;
    const name = o.name || o.id;
    if (!out.some((e) => e.name === name)) out.push({ name, id: o.id });
  }
  return out;
}

// setAssignment writes a data association's single <assignment> from its two FEEL
// bodies — <from> (the value/transform) and <to> (an output member path or an input
// target variable). Empty bodies clear the assignment, so a stateless association
// carries no dangling element (ADR-0058/0059/0060).
function setAssignment(modeler, element, bo, fromBody, toBody) {
  const modeling = modeler.get("modeling");
  const moddle = modeler.get("moddle");
  try {
    if (!fromBody && !toBody) {
      modeling.updateModdleProperties(element, bo, { assignment: [] });
      return;
    }
    const asg = moddle.create("bpmn:Assignment", {});
    asg.$parent = bo;
    if (fromBody) {
      const f = moddle.create("bpmn:FormalExpression", { body: fromBody });
      f.$parent = asg;
      asg.from = f;
    }
    if (toBody) {
      const t = moddle.create("bpmn:FormalExpression", { body: toBody });
      t.$parent = asg;
      asg.to = t;
    }
    modeling.updateModdleProperties(element, bo, { assignment: [asg] });
  } catch { /* stale */ }
}

function writeStartVariables(modeler, list, targetEl, targetBo) {
  const moddle = modeler.get("moddle");
  const modeling = modeler.get("modeling");
  const onRoot = !targetEl;
  const rootEl = modeler.get("canvas").getRootElement();
  if (onRoot) { targetEl = rootEl; targetBo = rootEl.businessObject; }
  const bo = targetBo;
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
  if (onRoot) modeling.updateProperties(targetEl, { extensionElements: ext });
  else modeling.updateModdleProperties(targetEl, bo, { extensionElements: ext });
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
//
// targetEl/targetBo are optional: omit them for the diagram's root process, or
// pass a collaboration pool's (participant shape, referenced process) to declare
// the variables on the process that pool executes instead.
//
// wrap optionally runs the persist under a rerender guard. The root editor shows
// with nothing selected, so its self-save re-renders nothing; a pool editor is
// shown for the *selected* participant, so its save would rebuild the panel and
// tear down an in-progress JSON editor — the pool passes savePreservingPanel to
// keep the panel intact, mirroring how FEEL fields self-save.
function wireStartVars(body, modeler, targetEl, targetBo, wrap = (fn) => fn()) {
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
  const persist = () => wrap(() => { try { writeStartVariables(modeler, collect(), targetEl, targetBo); } catch { /* stale */ } });

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

// groupifyPanel turns each <h3> section of the properties panel into a collapsible
// group (Camunda-style): the heading becomes a toggle with a chevron and a filled
// dot when the group has content, and everything up to the next <h3> becomes its
// collapsible body. It works on the already-rendered panel, so every element type's
// markup is grouped by one function instead of each branch knowing about grouping.
// Nodes are moved as whole subtrees, so field listeners and rich editors survive.
function groupifyPanel(body, collapsed) {
  const heads = [...body.children].filter((n) => n.tagName === "H3");
  if (!heads.length) return;
  for (const h3 of heads) {
    const title = h3.textContent.trim();
    const group = document.createElement("div");
    group.className = "pgroup" + (collapsed.has(title) ? " collapsed" : "");
    const bodyWrap = document.createElement("div");
    bodyWrap.className = "pgroup-body";
    let n = h3.nextSibling;
    while (n && !(n.nodeType === 1 && n.tagName === "H3")) {
      const next = n.nextSibling;
      bodyWrap.appendChild(n);
      n = next;
    }
    const fields = [...bodyWrap.querySelectorAll("input, textarea, select")];
    const hasVal = fields.some((el) =>
      el.tagName === "SELECT" ? el.selectedIndex > 0
        : (el.type !== "button" && el.type !== "submit" && (el.value || "").trim() !== ""));
    const hasRows = !!bodyWrap.querySelector(".dmn-input-row, .sv-row, .msg-row, tr, li");
    const head = document.createElement("button");
    head.type = "button";
    head.className = "pgroup-head";
    head.innerHTML = `<span class="pgroup-chevron">▸</span><span class="pgroup-title"></span>`;
    head.querySelector(".pgroup-title").textContent = title;
    if (hasVal || hasRows) {
      const dot = document.createElement("span");
      dot.className = "pgroup-dot";
      dot.title = "has content";
      head.appendChild(dot);
    }
    head.addEventListener("click", () => {
      const isCol = group.classList.toggle("collapsed");
      if (isCol) collapsed.add(title); else collapsed.delete(title);
    });
    body.insertBefore(group, h3);
    group.appendChild(head);
    group.appendChild(bodyWrap);
    body.removeChild(h3);
  }
}

function wireProperties(root, modeler, api, projectId, toast) {
  const icon = root.querySelector("#p-icon");
  const typename = root.querySelector("#p-typename");
  const nameEl = root.querySelector("#p-name");
  const body = root.querySelector("#p-body");
  const modeling = modeler.get("modeling");
  const selection = modeler.get("selection");

  // Tidy the panel Camunda-style: every <h3> section becomes a collapsible group
  // with a chevron and a filled dot when it carries content. groupifyPanel runs
  // after each (re-)render via a MutationObserver, so no per-element branch has to
  // know about grouping; collapse state persists across renders in `collapsed`.
  const collapsed = new Set();
  let groupifying = false;
  const panelObserver = new MutationObserver(() => {
    if (groupifying) return;
    groupifying = true;
    try { groupifyPanel(body, collapsed); } finally { groupifying = false; }
  });
  panelObserver.observe(body, { childList: true });

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

  // addProcessToPool gives a black-box pool (a Participant with no processRef) the
  // process it needs to hold elements and deploy. Rather than hand-wire the moddle
  // (a processRef is a *reference*, so the process would also have to be added to
  // definitions.rootElements, and the collapsed shape expanded), it delegates to
  // bpmn-js's own expand: replacing the participant with an expanded one runs the
  // factory that creates the process, registers it, and grows the band — the same
  // path the palette uses for a fresh pool. The name is carried across by replace.
  function addProcessToPool(element) {
    try {
      const replaced = modeler.get("bpmnReplace")
        .replaceElement(element, { type: "bpmn:Participant", isExpanded: true });
      selection.select(replaced);
      show(replaced); // now an executable pool: elements can be dropped in
    } catch { toast("could not add a process to this pool", "err"); }
  }

  // showPool renders the Details panel for a collaboration pool (Participant).
  // The pool executes a process; this edits that process (its id — the deploy
  // identity — its name, and its start variables), or offers to create one when
  // the pool is an empty black box that can't yet hold elements.
  function showPool(element, bo) {
    icon.textContent = "PL"; typename.textContent = "Pool";
    nameEl.textContent = bo.name || bo.id || "(pool)";
    const proc = bo.processRef;
    const poolFields = `
      <h3>Pool</h3>
      <label class="field"><span>Name</span><input type="text" id="f-poolname" value="${esc(bo.name || "")}" placeholder="Teilnehmer"/></label>
      <label class="field"><span>Pool ID</span><input type="text" value="${esc(bo.id || "")}" readonly/></label>`;

    if (!proc) {
      body.innerHTML = `${poolFields}
        <p class="muted" style="font-size:12px">A pool is a <b>participant</b> — it doesn't hold the flow itself, it <i>executes a process</i>. This pool has <b>no process</b>, so it can't contain elements or run.</p>
        <h3>Process</h3>
        <p class="muted" style="font-size:12px">Add the process that sits between the pool and its elements — the participant runs it, and the elements you draw live inside it.</p>
        <button type="button" class="btn" id="f-addproc" style="margin-top:6px">+ Add a process</button>`;
      body.querySelector("#f-poolname").addEventListener("change", (e) => {
        try { modeling.updateProperties(element, { name: e.target.value }); } catch { /* stale */ }
      });
      body.querySelector("#f-addproc").addEventListener("click", () => addProcessToPool(element));
      return;
    }

    let startVarsHTML = "";
    if (activeTab(root) === "implement") {
      const declared = readStartVariables(proc);
      startVarsHTML = `
        <h3>Start variables</h3>
        <div id="sv-list">${declared.map(startVarRowHTML).join("")}</div>
        <button type="button" class="btn neutral" id="sv-add" style="margin-top:6px">+ Add variable</button>
        <p class="muted" style="font-size:12px">Declared on this pool's process, these render as a typed form on <b>Deploy &amp; run</b> — with defaults and required checks. The engine ignores the declaration; it's authoring metadata.</p>`;
    }
    body.innerHTML = `${poolFields}
      <p class="muted" style="font-size:12px">The pool is a <b>participant</b> that executes the process below. The elements live in that process — the pool only labels who runs it.</p>
      <h3>Process</h3>
      <label class="field"><span>Process name</span><input type="text" id="f-procname" value="${esc(proc.name || "")}" placeholder="Order fulfillment"/></label>
      <label class="field"><span>Process ID</span><input type="text" id="f-procid" value="${esc(proc.id || "")}" placeholder="order-fulfillment"/></label>
      <p class="muted" style="font-size:12px">Each pool deploys as its own process; the <b>Process ID</b> is that deployment's identity — instances group by it, and renaming it deploys a new process rather than a new version.</p>
      ${startVarsHTML}`;
    body.querySelector("#f-poolname").addEventListener("change", (e) => {
      try { modeling.updateProperties(element, { name: e.target.value }); } catch { /* stale */ }
    });
    body.querySelector("#f-procname").addEventListener("change", (e) => {
      try { modeling.updateModdleProperties(element, proc, { name: e.target.value }); } catch { /* stale */ }
    });
    body.querySelector("#f-procid").addEventListener("change", (e) => {
      const v = (e.target.value || "").trim();
      if (v) { try { modeling.updateModdleProperties(element, proc, { id: v }); } catch { toast("invalid process id", "err"); } }
    });
    if (activeTab(root) === "implement") wireStartVars(body, modeler, element, proc, savePreservingPanel);
  }

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
      // (participant) executes its own process, configured by selecting the pool.
      if (isCollaborationRoot(modeler)) {
        icon.textContent = "CO"; typename.textContent = "Collaboration"; nameEl.textContent = "(collaboration)";
        body.innerHTML = `
          <h3>Collaboration</h3>
          <p class="muted" style="font-size:12px">This diagram has several <b>pools</b>. A pool is a <b>participant</b> that <i>executes a process</i> — the process holds the flow, the pool just names who runs it, and each deploys as its own process. Select a pool to name it and configure the process it runs, or an element inside a pool to configure it. Pools talk to each other through <b>message events</b>: a throw event in one pool and a catch event in another that reference the <b>same message</b> below.</p>
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
    // The generic two-letter badge is the type's first two letters, but every data
    // element starts "Da" (DataObject, DataStore, DataInput/OutputAssociation) and
    // would collide on "DA" — badge them by what they are so they read at a glance:
    // data object → DO, data store → DS, output/input association → OA/IA (ADR-0053).
    const badges = {
      "bpmn:DataObjectReference": "DO",
      "bpmn:DataStoreReference": "DS",
      "bpmn:DataOutputAssociation": "OA",
      "bpmn:DataInputAssociation": "IA",
    };
    icon.textContent = badges[bo.$type] || type.slice(0, 2).toUpperCase();
    typename.textContent = type;
    nameEl.textContent = bo.name || bo.id || "(unnamed)";

    // A pool (Participant) is not itself executable — in BPMN it *executes* a
    // process: participant → processRef → the flow elements. The pool's own name
    // is only the band label; the process it references is the deploy identity
    // and the only thing that can hold elements (a Participant with no processRef
    // is a black box the engine can't run and the canvas can't drop into). So the
    // panel edits the process behind the pool, and offers to create one when the
    // pool has none — the "Prozess dazwischen" that takes the elements.
    if (/:Participant$/.test(bo.$type || "")) {
      showPool(element, bo);
      return;
    }

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

    // A data object is the data a process carries — first-class in Atlas, not just
    // decoration (ADR-0053). Its name is the engine's variable-like identity and its
    // data state is the [received]/[approved] label an activity advances. Shown on
    // both tabs since data is descriptive and executable at once.
    if (bo.$type === "bpmn:DataObjectReference") {
      const stateName = (bo.dataState && bo.dataState.name) || "";
      const collection = !!(bo.dataObjectRef && bo.dataObjectRef.isCollection);
      // The other data objects in this process, so a reference can be pointed at an
      // existing one — showing the same object in several places (one logical object,
      // no long arrows). Distinct by name: duplicates are folded at compile time.
      const names = dataObjectNames(processOf(modeler, element));
      const curName = (bo.dataObjectRef && (bo.dataObjectRef.name || bo.dataObjectRef.id)) || "";
      let pointsTo = "";
      if (names.length > 1) {
        pointsTo = `<label class="field"><span>Points to</span>
          <select id="f-pointsto">${names.map((n) => `<option value="${esc(n.id)}" ${n.name === curName ? "selected" : ""}>${esc(n.name)}</option>`).join("")}</select></label>
          <p class="muted" style="font-size:12px">Point this box at an existing data object to show <b>the same object in several places</b> — one logical object, so you can put it next to each activity without long arrows across the diagram.</p>`;
      }
      html += `<h3>Data object</h3>
        <label class="field"><span>Data state</span>
          <input type="text" id="f-datastate" value="${esc(stateName)}" placeholder="received"/></label>
        <label class="field checkbox"><input type="checkbox" id="f-collection" ${collection ? "checked" : ""}/> <span>Collection (a list of items)</span></label>
        ${pointsTo}
        <p class="muted" style="font-size:12px">A data object carries a value <i>and</i> a <b>data state</b> — <code>order [received]</code> → <code>[approved]</code>. The state set here is where the object starts each instance; a <b>data output association</b> (an arrow from an activity to this object) advances it and writes its value, a <b>data input association</b> reads it back, and the full state history is recorded per instance and survives restart. The <b>Name</b> is how the engine identifies it.</p>`;
    }

    // A data association is the arrow between an activity and a data object — it is
    // what makes data flow (ADR-0058/0059/0060). Its <assignment> carries the FEEL:
    // <from> is the value/transform, <to> is an output member path or an input target
    // variable. Shown on both tabs since it is the executable data contract.
    if (bo.$type === "bpmn:DataOutputAssociation" || bo.$type === "bpmn:DataInputAssociation") {
      const asg = (bo.assignment && bo.assignment[0]) || {};
      const fromBody = (asg.from && asg.from.body) || "";
      const toBody = (asg.to && asg.to.body) || "";
      if (bo.$type === "bpmn:DataOutputAssociation") {
        html += `<h3>Writes data object</h3>
          <label class="field"><span>FEEL value</span><input type="text" id="f-assoc-from" value="${esc(fromBody)}" placeholder="=amount * 1.19"/></label>
          <label class="field"><span>Target member <span class="muted">(optional)</span></span><input type="text" id="f-assoc-to" value="${esc(toBody)}" placeholder="name"/></label>
          <p class="muted" style="font-size:12px">When the activity completes it writes <b>${esc(dataRefName(bo.targetRef))}</b>: the <b>FEEL value</b> (over the instance's variables) becomes the object's value, and its data state advances to the one on the target reference. Leave <b>Target member</b> empty to write the whole object; set it (e.g. <code>name</code>) to update just that field of a structured object and keep the rest.</p>`;
      } else {
        html += `<h3>Reads data object</h3>
          <label class="field"><span>Target variable</span><input type="text" id="f-assoc-to" value="${esc(toBody)}" placeholder="order"/></label>
          <label class="field"><span>Transform <span class="muted">(optional)</span></span><input type="text" id="f-assoc-from" value="${esc(fromBody)}" placeholder="=order.amount"/></label>
          <p class="muted" style="font-size:12px">At activation this reads <b>${esc(dataRefName(bo.sourceRef))}</b> into the <b>Target variable</b>, so the activity's FEEL can use it. Leave <b>Transform</b> empty to copy the object's value; set it (the object is available under its name) to compute the value written into the variable.</p>`;
      }
    }

    if (tab === "implement") {
      if (isActivity(bo)) {
        const t = bo.$type;
        html += `
          <label class="field"><span>Task type</span>
            <select id="f-tasktype">
              <option value="bpmn:Task" ${t === "bpmn:Task" ? "selected" : ""}>Undefined task</option>
              <option value="bpmn:ScriptTask" ${t === "bpmn:ScriptTask" ? "selected" : ""}>Script task (FEEL)</option>
              <option value="bpmn:ServiceTask" ${t === "bpmn:ServiceTask" ? "selected" : ""}>Service task (job worker)</option>
              <option value="bpmn:BusinessRuleTask" ${t === "bpmn:BusinessRuleTask" ? "selected" : ""}>Business rule task (DMN)</option>
              <option value="bpmn:UserTask" ${t === "bpmn:UserTask" ? "selected" : ""}>User task</option>
            </select></label>`;

        if (t === "bpmn:ScriptTask") {
          // A script task is authored either in FEEL (evaluated inline by the
          // engine, <zeebe:script>) or in a general-purpose language run by a job
          // worker (<atlas:jobScript>, ADR-0047). The language selector switches
          // which the task carries; both are still bpmn:ScriptTask.
          const js = findExt(bo, "atlas:JobScript");
          const lang = js ? (js.language || "powershell") : "feel";
          html += `<h3>Script</h3>
            <label class="field"><span>Language</span>
              <select id="f-scriptlang">
                <option value="feel" ${lang === "feel" ? "selected" : ""}>FEEL (built-in, runs in the engine)</option>
                <option value="powershell" ${lang === "powershell" ? "selected" : ""}>PowerShell (job worker)</option>
              </select></label>`;
          if (lang === "feel") {
            const s = findExt(bo, "zeebe:Script") || {};
            const exprText = (s.expression || "").replace(/^=\s*/, "");
            html += `<label class="field"><span>Expression (FEEL)</span>
                <textarea id="f-expr" rows="3" placeholder="amount * (1 + taxRate)">${esc(exprText)}</textarea></label>
              <label class="field"><span>Result variable</span>
                <input type="text" id="f-result" value="${esc(s.resultVariable || "")}" placeholder="gross"/></label>`;
          } else {
            html += `<label class="field"><span>Script (PowerShell)</span>
                <textarea id="f-psbody" rows="6" spellcheck="false" placeholder='Write-Output "Hallo $Vorname"'>${esc(js && js.source || "")}</textarea></label>
              <label class="field"><span>Result variable</span>
                <input type="text" id="f-psresult" value="${esc((js && js.resultVariable) || "")}" placeholder="Greeting"/></label>
              <p class="muted" style="font-size:12px">Runs on a PowerShell job worker, off the engine's hot path. The instance's variables are available as <code>$name</code>; the script's output is written back into the result variable.</p>`;
          }
        } else if (t === "bpmn:ServiceTask") {
          const d = findExt(bo, "zeebe:TaskDefinition") || {};
          html += `<h3>Task definition</h3>
            <label class="field"><span>Job type</span>
              <input type="text" id="f-jobtype" value="${esc(d.type || "")}" placeholder="payment"/></label>`;
        } else if (t === "bpmn:BusinessRuleTask") {
          const cd = findExt(bo, "zeebe:CalledDecision") || {};
          const tc = findExt(bo, "atlas:TemisConnector");
          const mode = tc ? "connector" : "local";
          const io = findExt(bo, "zeebe:IoMapping");
          const inputs = (io && io.inputParameters) || [];
          html += `<h3>Called decision (DMN)</h3>
            <label class="field"><span>Evaluation</span>
              <select id="f-brt-mode">
                <option value="local" ${mode === "local" ? "selected" : ""}>In-engine (embedded DMN)</option>
                <option value="connector" ${mode === "connector" ? "selected" : ""}>External (temis connector)</option>
              </select></label>`;
          if (mode === "connector") {
            html += `<label class="field"><span>Connector</span>
              <input type="text" id="f-connector" value="${esc((tc && tc.connector) || "")}" placeholder="risk-service"/></label>`;
          }
          const binding = cd.bindingType === "deployment" ? "deployment" : "latest";
          const bindingField = mode === "local" ? `
            <label class="field"><span>Binding</span>
              <select id="f-brt-binding">
                <option value="latest" ${binding === "latest" ? "selected" : ""}>Latest — newest deployed version</option>
                <option value="deployment" ${binding === "deployment" ? "selected" : ""}>Deployment — pinned to this deploy</option>
              </select></label>` : "";
          html += `<label class="field"><span>Decision</span>
              <select id="f-decision-pick"><option value="">${cd.decisionId ? esc(cd.decisionId) + " (current)" : "— choose a decision —"}</option></select></label>
            <div style="display:flex; gap:8px; margin:-4px 0 6px">
              <button type="button" class="btn ghost" id="f-dmn-new">＋ Neue Decision</button>
              <button type="button" class="btn ghost" id="f-dmn-edit"${cd.decisionId ? "" : " disabled"}>Bearbeiten</button>
            </div>
            <label class="field"><span>Decision ID</span>
              <input type="text" id="f-decisionid" value="${esc(cd.decisionId || "")}" placeholder="Dish"/></label>
            <label class="field"><span>Result variable</span>
              <input type="text" id="f-resultvar" value="${esc(cd.resultVariable || "")}" placeholder="dish"/></label>${bindingField}
            <p class="muted" style="font-size:12px">Pick a decision to auto-fill its inputs and result variable. <b>Latest</b> evaluates the newest deployed version; <b>Deployment</b> pins to the version deployed with this process.</p>
            <h3>Decision inputs</h3>
            <p class="muted" style="font-size:12px">Each row feeds one decision input from a FEEL expression over the instance's variables. Leave a row's name blank to drop it.</p>
            <div id="dmn-inputs">${inputs.map((p, i) => decisionInputRowHTML(i, p.source, p.target)).join("")}${decisionInputRowHTML(inputs.length, "", "")}</div>`;
        } else if (t === "bpmn:UserTask") {
          const a = findExt(bo, "zeebe:AssignmentDefinition") || {};
          const pr = findExt(bo, "zeebe:PriorityDefinition") || {};
          const sch = findExt(bo, "zeebe:TaskSchedule") || {};
          const fd = findExt(bo, "zeebe:FormDefinition") || {};
          const curForm = fd.formId || "";
          html += `<h3>Form</h3>
            <label class="field"><span>Linked form</span>
              <select id="f-form">
                <option value="">— none —</option>
                ${curForm ? `<option value="${esc(curForm)}" selected>${esc(curForm)}</option>` : ""}
              </select></label>
            <p class="muted" style="font-size:12px">The form the Tasks app renders for this task, submitted as its variables.
              <a href="#/modeler/form/new" target="_blank" rel="noopener">Create a new form</a>, then reopen this to link it.</p>
            <h3>Assignment</h3>
            <label class="field"><span>Assignee</span>
              <input type="text" id="f-assignee" value="${esc(a.assignee || "")}" placeholder="editor"/></label>
            <label class="field"><span>Candidate groups</span>
              <input type="text" id="f-groups" value="${esc(a.candidateGroups || "")}" placeholder="reviewers"/></label>
            <h3>Schedule</h3>
            <label class="field"><span>Priority</span>
              <input type="number" id="f-priority" min="0" max="100" value="${esc(pr.priority || "50")}" placeholder="50"/></label>
            <label class="field"><span>Due in</span>
              <input type="text" id="f-due" value="${esc(sch.dueDate || "")}" placeholder="P2D, PT4H, PT30M"/></label>
            <p class="muted" style="font-size:12px">An ISO-8601 duration measured from when the task appears
              (e.g. <b>P2D</b> = 2 days, <b>PT4H</b> = 4 hours). Leave blank for no due date. Priority 0–100; higher sorts first.</p>`;
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
          html += timerFieldsHTML(timer, ["duration", "date"], `The event waits, then continues (ADR-0054).
            <b>Duration</b> waits that long (<b>PT30S</b>, <b>PT5M</b>, <b>P1DT2H</b>); <b>Date &amp; time</b> waits until that instant.
            A catch fires once, so it has no cycle. A FEEL expression is allowed in either.`);
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
      } else if (bo.$type === "bpmn:BoundaryEvent") {
        // A boundary event is attached to an activity and arms while it runs. Its
        // cancelActivity attribute (interrupting by default) and its timer/message
        // trigger are configured here; the trigger's fields reuse the same
        // timer / message wiring as the intermediate catch event.
        const timer = timerDefOf(bo);
        const msg = messageDefOf(bo);
        const interrupting = bo.cancelActivity !== false;
        html += `<h3>Behaviour</h3>
          <label class="field"><span>On trigger</span>
            <select id="f-cancelactivity">
              <option value="true" ${interrupting ? "selected" : ""}>Interrupting — cancel the activity</option>
              <option value="false" ${interrupting ? "" : "selected"}>Non-interrupting — run alongside</option>
            </select></label>
          <p class="muted" style="font-size:12px">Interrupting cancels the attached activity (and its job) and routes the token out this event; non-interrupting spawns a parallel token and lets the activity continue.</p>`;
        if (timer) {
          const kinds = interrupting ? ["duration", "date"] : ["duration", "date", "cycle"];
          const cycleNote = interrupting
            ? "An interrupting boundary fires once, so it has no cycle."
            : "A non-interrupting boundary may <b>Cycle</b> — an ISO-8601 repeating interval (<b>R/PT1H</b>) or cron (<b>0 * * * *</b>) — firing a fresh token each time.";
          html += timerFieldsHTML(timer, kinds, `The event fires relative to the activity (ADR-0054).
            <b>Duration</b> fires that long after it starts (<b>PT30S</b>, <b>PT5M</b>); <b>Date &amp; time</b> at a fixed instant.
            ${cycleNote} A FEEL expression is allowed in any type.`);
        } else if (msg) {
          html += messageFieldsHTML(modeler, msg, "The event fires when this message is published with a matching correlation key.");
        } else {
          html += `<p class="muted" style="font-size:12px">Use the wrench icon on the element to make this a <b>Timer</b> or <b>Message</b> boundary event, then configure its trigger here.</p>`;
        }
      } else if (bo.$type === "bpmn:StartEvent") {
        const timer = timerDefOf(bo);
        const msg = messageDefOf(bo);
        if (timer) {
          html += timerFieldsHTML(timer, ["duration", "date", "cycle"], `A timer start event fires on this schedule with no incoming token (ADR-0051).
            <b>Duration</b> and <b>Date &amp; time</b> fire once — that long after deploy, or at that instant;
            <b>Cycle</b> recurs, either an ISO-8601 repeating interval (<b>R/PT1H</b>, <b>R3/P1D</b>) or a cron expression (<b>0 * * * *</b>).
            A FEEL expression is allowed in any type.`);
        } else if (msg) {
          html += messageFieldsHTML(modeler, msg, "A message start event: publishing this message starts a new instance of this process, matched by message name (the correlation key is shared with the throwing event but is not yet evaluated for starts).");
        } else {
          const fd = findExt(bo, "zeebe:FormDefinition") || {};
          const curForm = fd.formId || "";
          html += `<h3>Start form</h3>
            <label class="field"><span>Linked form</span>
              <select id="f-form">
                <option value="">— none —</option>
                ${curForm ? `<option value="${esc(curForm)}" selected>${esc(curForm)}</option>` : ""}
              </select></label>
            <p class="muted" style="font-size:12px">Shown before the process starts — from the Tasks app's <b>Start</b> view — its data becomes the instance's start variables.
              <a href="#/modeler/form/new" target="_blank" rel="noopener">Create a new form</a>, then reopen this to link it.</p>
            <p class="muted" style="font-size:12px">A plain start event begins an instance directly. Use the wrench icon on the element to make this a <b>Timer</b> or <b>Message</b> start event instead.</p>`;
        }
      } else if (bo.$type === "bpmn:EndEvent") {
        const msg = messageDefOf(bo);
        if (msg) {
          html += messageFieldsHTML(modeler, msg, "On reaching this end event the message is published; any instance waiting on it with a matching correlation key continues. The instance then ends.");
        } else {
          html += `<p class="muted" style="font-size:12px">A plain end event ends the instance. Use the wrench icon on the element to make this a <b>Message</b> end event, which publishes a message as the instance ends.</p>`;
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
      try {
        modeling.updateProperties(element, { name: e.target.value });
        // A data object's engine identity is the underlying <dataObject> name, not
        // the reference's — so setting the reference's name here also names the
        // object it points at, keeping the modeled name and the runtime one in sync
        // (ADR-0053).
        if (bo.$type === "bpmn:DataObjectReference" && bo.dataObjectRef) {
          modeling.updateModdleProperties(element, bo.dataObjectRef, { name: e.target.value });
        }
      } catch { /* stale */ }
    });

    const fdatastate = body.querySelector("#f-datastate");
    if (fdatastate) {
      fdatastate.addEventListener("change", (e) => {
        const v = (e.target.value || "").trim();
        try {
          let ds;
          if (v) {
            ds = modeler.get("moddle").create("bpmn:DataState", { name: v });
            ds.$parent = bo;
          }
          // Setting dataState to undefined clears the [state] label.
          modeling.updateModdleProperties(element, bo, { dataState: ds || undefined });
        } catch { /* stale */ }
      });
    }
    const fcollection = body.querySelector("#f-collection");
    if (fcollection && bo.dataObjectRef) {
      fcollection.addEventListener("change", (e) => {
        try { modeling.updateModdleProperties(element, bo.dataObjectRef, { isCollection: !!e.target.checked }); } catch { /* stale */ }
      });
    }
    const fpointsto = body.querySelector("#f-pointsto");
    if (fpointsto) {
      fpointsto.addEventListener("change", (e) => {
        try {
          const procBo = processOf(modeler, element);
          const objs = procBo ? (procBo.flowElements || []).filter((x) => x.$type === "bpmn:DataObject") : [];
          const target = objs.find((o) => o.id === e.target.value);
          if (!target) return;
          const old = bo.dataObjectRef;
          // Point this reference at the chosen object, and relabel the box to match so
          // the canvas and the engine identity agree.
          modeling.updateModdleProperties(element, bo, { dataObjectRef: target });
          if (target.name) modeling.updateProperties(element, { name: target.name });
          // Remove the reference's former object if nothing else points at it, so a
          // freshly-dropped duplicate does not linger as a phantom object.
          if (old && old !== target && procBo) {
            const stillUsed = (procBo.flowElements || []).some((x) => x.$type === "bpmn:DataObjectReference" && x.dataObjectRef === old);
            if (!stillUsed) {
              modeling.updateModdleProperties(element, procBo, { flowElements: (procBo.flowElements || []).filter((x) => x !== old) });
            }
          }
          show(element);
        } catch { /* stale */ }
      });
    }
    const assocFrom = body.querySelector("#f-assoc-from");
    const assocTo = body.querySelector("#f-assoc-to");
    if (assocFrom || assocTo) {
      const applyAssoc = () => setAssignment(modeler, element, bo,
        assocFrom ? assocFrom.value.trim() : "",
        assocTo ? assocTo.value.trim() : "");
      if (assocFrom) assocFrom.addEventListener("change", applyAssoc);
      if (assocTo) assocTo.addEventListener("change", applyAssoc);
    }

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
    // Script language selector: switch a script task between FEEL (inline) and
    // PowerShell (job worker) by swapping which extension it carries, then
    // re-render so the right fields show. Neither language's fields are lost until
    // the author edits — switching only removes the other extension.
    const flang = body.querySelector("#f-scriptlang");
    if (flang) {
      flang.addEventListener("change", (e) => {
        try {
          if (e.target.value === "powershell") {
            removeExt(modeler, element, "zeebe:Script");
            upsertExt(modeler, element, "atlas:JobScript", { language: "powershell" });
          } else {
            removeExt(modeler, element, "atlas:JobScript");
          }
          show(element);
        } catch { /* stale */ }
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

    const fpsbody = body.querySelector("#f-psbody");
    const fpsresult = body.querySelector("#f-psresult");
    const savePowerShell = () => savePreservingPanel(() => {
      upsertExt(modeler, element, "atlas:JobScript", {
        language: "powershell",
        source: fpsbody.value || "",
        resultVariable: (fpsresult.value || "").trim(),
      });
    });
    if (fpsbody) fpsbody.addEventListener("change", savePowerShell);
    if (fpsresult) fpsresult.addEventListener("change", savePowerShell);

    const fjob = body.querySelector("#f-jobtype");
    if (fjob) {
      fjob.addEventListener("change", () => {
        upsertExt(modeler, element, "zeebe:TaskDefinition", { type: (fjob.value || "").trim() });
      });
    }

    const fdecision = body.querySelector("#f-decisionid");
    const fresultvar = body.querySelector("#f-resultvar");
    const fbinding = body.querySelector("#f-brt-binding");
    // currentBinding preserves the decision binding (ADR-0063) across every save of
    // the called decision, so editing the id/result variable never drops it.
    const currentBinding = () => (fbinding && fbinding.value === "deployment") ? "deployment" : "latest";
    const calledDecisionProps = () => ({
      decisionId: (fdecision.value || "").trim(),
      resultVariable: (fresultvar.value || "").trim(),
      bindingType: currentBinding(),
    });
    const saveDecision = () => savePreservingPanel(() => {
      upsertExt(modeler, element, "zeebe:CalledDecision", calledDecisionProps());
    });
    if (fdecision) fdecision.addEventListener("change", saveDecision);
    if (fresultvar) fresultvar.addEventListener("change", saveDecision);
    if (fbinding) fbinding.addEventListener("change", saveDecision);

    // Evaluation mode: local (embedded DMN) vs a temis connector (central). The
    // choice is the presence of the atlas:temisConnector extension the compiler
    // reads (ADR-0050); flipping it re-renders so the connector field appears.
    const fmode = body.querySelector("#f-brt-mode");
    if (fmode) {
      fmode.addEventListener("change", () => {
        savePreservingPanel(() => {
          if (fmode.value === "connector") {
            const name = (body.querySelector("#f-connector")?.value || "").trim();
            upsertExt(modeler, element, "atlas:TemisConnector", { connector: name });
          } else {
            removeExt(modeler, element, "atlas:TemisConnector");
          }
        });
        show(element);
      });
    }
    const fconnector = body.querySelector("#f-connector");
    if (fconnector) {
      fconnector.addEventListener("change", () => savePreservingPanel(() => {
        upsertExt(modeler, element, "atlas:TemisConnector", { connector: (fconnector.value || "").trim() });
      }));
    }

    // Decision picker: populate from the DMN references' decisions and, on pick,
    // set the decision id + result variable and auto-fill the input mappings from
    // the decision's declared inputs — so an author selects from a list instead of
    // typing ids and parameters by hand (ADR-0050). A previously-set source for a
    // given input target is preserved when re-picking.
    const fpick = body.querySelector("#f-decision-pick");
    if (fpick && api) {
      const scope = projectId ? "?projectId=" + encodeURIComponent(projectId) : "";
      api("GET", "/api/v1/decisions" + scope).then((decisions) => {
        if (!document.body.contains(fpick)) return;
        const cur = (fdecision?.value || "").trim();
        const opts = [`<option value="">— choose a decision —</option>`];
        for (const d of decisions || []) {
          const label = d.model ? `${d.name} · ${d.model}` : d.name;
          opts.push(`<option value="${esc(d.id)}"${d.id === cur ? " selected" : ""}>${esc(label)}</option>`);
        }
        fpick.innerHTML = opts.join("");
        fpick._catalog = decisions || [];
      }).catch(() => { /* leave the placeholder; manual entry still works */ });

      // applyPick sets the decision id + result variable and auto-fills the input
      // mappings from a catalog decision's declared inputs, preserving any custom
      // source already mapped for a target. Shared by the picker's change handler
      // and by the "author a decision" flow, so a decision created in the embedded
      // editor is adopted exactly as if it had been picked from the list.
      const applyPick = (d) => {
        const io = findExt(element.businessObject, "zeebe:IoMapping");
        const prev = {};
        for (const p of (io && io.inputParameters) || []) {
          if (p.target) prev[p.target] = (p.source || "").replace(/^=\s*/, "");
        }
        savePreservingPanel(() => {
          upsertExt(modeler, element, "zeebe:CalledDecision", {
            decisionId: d.id,
            resultVariable: (fresultvar.value || "").trim() || (d.output && d.output.name) || d.id,
            bindingType: currentBinding(),
          });
          const rows = (d.inputs || []).map((inp) => ({ target: inp.name, source: prev[inp.name] || inp.name }));
          saveDecisionInputs(modeler, element, rows);
        });
        show(element); // reflect the filled id, result variable, and input rows
      };
      fpick.addEventListener("change", () => {
        const d = (fpick._catalog || []).find((x) => x.id === fpick.value);
        if (d) applyPick(d);
      });

      // Author-a-decision: the embedded dmn-js editor (ADR-0051). "＋ Neue Decision"
      // creates a model + reference and adopts it; "Bearbeiten" opens the current
      // decision's local model in place. After authoring, the catalog is re-fetched
      // and the authored decision is adopted (id + inputs + result), so inputs and
      // output flow in automatically — no separate upload step.
      const adoptAuthored = async (result) => {
        if (!result) return;
        const scope = projectId ? "?projectId=" + encodeURIComponent(projectId) : "";
        let decisions = [];
        try { decisions = (await api("GET", "/api/v1/decisions" + scope)) || []; } catch { /* keep panel */ }
        const d = decisions.find((x) => x.id === result.name) ||
          decisions.find((x) => x.modelRef === result.modelRef);
        if (d) applyPick(d); else show(element);
      };
      const fnew = body.querySelector("#f-dmn-new");
      if (fnew) {
        fnew.addEventListener("click", async () => {
          adoptAuthored(await openDmnEditor({ api, toast, projectId }));
        });
      }
      const fedit = body.querySelector("#f-dmn-edit");
      if (fedit) {
        fedit.addEventListener("click", async () => {
          const cur = (fpick._catalog || []).find((x) => x.id === (fdecision?.value || "").trim());
          if (!cur || !cur.modelRef) {
            toast && toast("Diese Decision hat kein lokal editierbares Modell.", "err");
            return;
          }
          adoptAuthored(await openDmnEditor({ api, toast, projectId, modelRef: cur.modelRef }));
        });
      }
    }

    const inputsWrap = body.querySelector("#dmn-inputs");
    if (inputsWrap) {
      const collect = () => [...inputsWrap.querySelectorAll(".dmn-input-row")].map((row) => ({
        target: (row.querySelector(".dmn-in-target").value || "").trim(),
        source: (row.querySelector(".dmn-in-source").value || "").trim(),
      }));
      const saveInputs = () => savePreservingPanel(() => saveDecisionInputs(modeler, element, collect()));
      // wireRow saves on blur and grows the list in place — typing into the last
      // row's target appends a fresh empty row without a full re-render, so input
      // focus is never yanked mid-edit.
      const wireRow = (row) => {
        const target = row.querySelector(".dmn-in-target");
        const source = row.querySelector(".dmn-in-source");
        target.addEventListener("change", saveInputs);
        source.addEventListener("change", saveInputs);
        target.addEventListener("input", () => {
          const rows = [...inputsWrap.querySelectorAll(".dmn-input-row")];
          if (row === rows[rows.length - 1] && target.value.trim() !== "") {
            const tmp = document.createElement("div");
            tmp.innerHTML = decisionInputRowHTML(rows.length, "", "");
            const newRow = tmp.firstElementChild;
            inputsWrap.appendChild(newRow);
            wireRow(newRow);
          }
        });
      };
      [...inputsWrap.querySelectorAll(".dmn-input-row")].forEach(wireRow);
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

    // Priority and due date (ADR-0051): zeebe:priorityDefinition is written only
    // when it differs from the default 50; zeebe:taskSchedule is dropped when the
    // due-date field is cleared, keeping the XML free of empty extensions.
    const fpriority = body.querySelector("#f-priority");
    const fdue = body.querySelector("#f-due");
    if (fpriority) {
      fpriority.addEventListener("change", () => {
        const v = (fpriority.value || "").trim();
        if (v === "" || v === "50") removeExt(modeler, element, "zeebe:PriorityDefinition");
        else upsertExt(modeler, element, "zeebe:PriorityDefinition", { priority: v });
      });
    }
    if (fdue) {
      fdue.addEventListener("change", () => {
        const v = (fdue.value || "").trim();
        if (v) upsertExt(modeler, element, "zeebe:TaskSchedule", { dueDate: v });
        else removeExt(modeler, element, "zeebe:TaskSchedule");
      });
    }

    const fform = body.querySelector("#f-form");
    if (fform) {
      // Link/unlink writes (or drops) the zeebe:FormDefinition formId — the same
      // extension the compiler reads to bind a form (ADR-0028).
      fform.addEventListener("change", () => {
        const id = fform.value;
        if (id) upsertExt(modeler, element, "zeebe:FormDefinition", { formId: id });
        else removeExt(modeler, element, "zeebe:FormDefinition");
      });
      // Populate the picker with the project's forms once loaded; keep the
      // currently-linked one selected even if it is no longer in the list.
      api("GET", "/api/v1/forms").then((forms) => {
        if (!document.body.contains(fform)) return; // selection moved on
        const cur = fform.value;
        const opts = [`<option value="">— none —</option>`];
        let sawCur = false;
        for (const f of forms || []) {
          const sel = f.id === cur ? " selected" : "";
          if (f.id === cur) sawCur = true;
          opts.push(`<option value="${esc(f.id)}"${sel}>${esc(f.name || f.id)}</option>`);
        }
        if (cur && !sawCur) opts.push(`<option value="${esc(cur)}" selected>${esc(cur)} (missing)</option>`);
        fform.innerHTML = opts.join("");
      }).catch(() => { /* leave the current single-option select as-is */ });
    }

    const fcancel = body.querySelector("#f-cancelactivity");
    if (fcancel) {
      fcancel.addEventListener("change", () => {
        // Flipping cancelActivity also flips bpmn-js's solid/dashed boundary marker.
        try { modeling.updateProperties(element, { cancelActivity: fcancel.value === "true" }); } catch { /* stale */ }
      });
    }

    const ftimerkind = body.querySelector("#f-timerkind");
    const ftimerval = body.querySelector("#f-timerval");
    if (ftimerkind || ftimerval) {
      const moddle = modeler.get("moddle");
      // ensureExpr returns the FormalExpression sub-element for a kind, creating
      // an empty one parented to the timer when it does not yet exist.
      const ensureExpr = (timer, prop) => {
        let e = timer[prop];
        if (!e) { e = moddle.create("bpmn:FormalExpression"); e.$parent = timer; }
        return e;
      };
      if (ftimerkind) {
        // Switching type moves the schedule to a different sub-element: the chosen
        // kind's element is (re)created and the other two are dropped, so exactly
        // one of timeDuration/timeDate/timeCycle is ever set (the compiler rejects
        // more than one). Re-render so the value field's label and placeholder match.
        ftimerkind.addEventListener("change", () => {
          const timer = timerDefOf(element.businessObject);
          if (!timer) return;
          const props = {};
          for (const [k, meta] of Object.entries(TIMER_KINDS)) {
            props[meta.prop] = k === ftimerkind.value ? ensureExpr(timer, meta.prop) : undefined;
          }
          modeling.updateModdleProperties(element, timer, props);
          show(element);
        });
      }
      if (ftimerval) {
        ftimerval.addEventListener("change", () => {
          const timer = timerDefOf(element.businessObject);
          if (!timer) return;
          const kind = (ftimerkind && ftimerkind.value) || timerScheduleOf(timer).kind;
          const prop = TIMER_KINDS[kind].prop;
          const e = ensureExpr(timer, prop);
          e.body = (ftimerval.value || "").trim();
          modeling.updateModdleProperties(element, timer, { [prop]: e });
        });
      }
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

  // When an input association is freshly drawn (data object → activity), default its
  // target variable to the source object's name, so it compiles and reads the object
  // into a like-named variable without hand-editing (ADR-0059). Deferred so the
  // create command settles first. Output associations need no default: a bare one is
  // a valid state-only transition.
  modeler.on("commandStack.connection.create.postExecuted", (e) => {
    const conn = e.context && e.context.connection;
    const bo = conn && conn.businessObject;
    if (!bo || bo.$type !== "bpmn:DataInputAssociation") return;
    if (bo.assignment && bo.assignment.length) return; // already configured
    const objName = dataRefName(bo.sourceRef);
    if (objName && objName !== "?") setTimeout(() => setAssignment(modeler, conn, bo, "", objName), 0);
  });
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

function wireActions(root, modeler, api, toast, projectId) {
  // Save persists the diagram as a draft (raw XML, no compile), keyed by process
  // id, so incomplete work survives and can be reopened from the Modeler home. A
  // projectId (set when the editor was opened from a project's "Create new") files
  // the draft into that project (ADR-0034).
  const saveBtn = root.querySelector("#save");
  saveBtn.addEventListener("click", async () => {
    saveBtn.disabled = true;
    try {
      const { xml } = await modeler.saveXML({ format: true });
      const path = "/api/v1/drafts" + (projectId ? "?projectId=" + encodeURIComponent(projectId) : "");
      const d = await api("POST", path, xml, true);
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

  // Deploy opens a panel that offers two actions: "Deploy only" makes the model
  // runnable without touching the runtime, and "Deploy & run" additionally starts
  // an instance seeded with the entered start variables — the editor's equivalent
  // of the Live view's Start instance. A developer iterating on a model shouldn't
  // be forced to spin up an instance every time they deploy, so the two are split.
  // When the process declares start variables (Process panel → Start variables)
  // the panel shows a typed form built from that declaration; otherwise a
  // free-form JSON textarea. Start variables only apply to "Deploy & run".
  const deployBtn = root.querySelector("#deploy");
  const dpanel = root.querySelector("#deploy-panel");
  const dbody = root.querySelector("#deploy-body");
  const dactions = root.querySelector("#deploy-actions");
  const drun = root.querySelector("#deploy-run");
  const donly = root.querySelector("#deploy-only");
  const derr = root.querySelector("#deploy-err");
  const closeDeploy = () => { dpanel.hidden = true; derr.textContent = ""; };

  // Read the declaration fresh each open — it can change while the editor is up.
  // Reopening after a success also restores the form and its Deploy buttons (the
  // success view replaces them with a link back to Operations).
  const openDeploy = () => {
    const bo = rootProcess(modeler);
    dbody.innerHTML = startVarsFormHTML(bo ? readStartVariables(bo) : []);
    wireDeployJSONEditors(dbody);
    dactions.hidden = false;
    dpanel.hidden = false;
    derr.textContent = "";
    const first = dbody.querySelector(".sv-json, .dv-field");
    if (first) first.focus();
  };

  deployBtn.addEventListener("click", () => { dpanel.hidden ? openDeploy() : closeDeploy(); });
  root.querySelector("#deploy-cancel").addEventListener("click", closeDeploy);

  // resolveInstanceLink returns the Operations deep-link for the instance a
  // Deploy & run just started. A freshly deployed version has exactly one
  // instance, found by its definition key; that key completes the roundtrip so the
  // link lands directly on it. If it can't be resolved (a transient list failure),
  // fall back to the process-level Operations view.
  const resolveInstanceLink = async (defKey) => {
    try {
      const list = await api("GET", "/api/v1/instances");
      const mine = list.filter((r) => r.processDefKey === defKey);
      if (mine.length) {
        const inst = mine.reduce((a, b) => (b.key > a.key ? b : a));
        return `#/operations/p/${defKey}/i/${inst.key}`;
      }
    } catch { /* fall back to the process-level view */ }
    return `#/operations/p/${defKey}`;
  };

  // showDeploySuccess replaces the panel form with a confirmation that carries a
  // link into Operations — the deploy→run→observe roundtrip. `href` targets the
  // started instance (Deploy & run) or the process (Deploy only / collaboration).
  const showDeploySuccess = (message, href) => {
    dactions.hidden = true;
    derr.textContent = "";
    dbody.innerHTML = `<div class="deploy-success">
      <p class="deploy-success-msg">✓ ${esc(message)}</p>
      <div class="row">
        <a class="btn" href="${href}">Open in Operations →</a>
        <button type="button" class="btn neutral" id="deploy-done">Close</button>
      </div>
    </div>`;
    const done = dbody.querySelector("#deploy-done");
    if (done) done.addEventListener("click", closeDeploy);
  };

  // run === true also starts an instance; run === false deploys only. The start
  // form is read (and validated) only when running, so an unrelated JSON typo
  // never blocks a plain deploy.
  const doDeploy = async (run) => {
    let body = {};
    if (run) {
      try {
        body = readStartFormBody(dbody);
      } catch (e) { derr.textContent = e.message; return; }
    }
    drun.disabled = true;
    donly.disabled = true;
    derr.textContent = "";
    try {
      const { xml } = await modeler.saveXML({ format: true });
      const dep = await api("POST", "/api/v1/deployments", xml, true);
      const all = dep.deployments || [{ key: dep.key, processId: dep.processId, version: dep.version }];
      if (all.length > 1) {
        // A collaboration deploys one definition per pool; which pool to start is
        // ambiguous, so just report what was deployed (start pools from Operations).
        // Start variables don't apply here — there's no single instance to seed.
        const msg = `Deployed ${all.length} pools: ${all.map((d) => d.processId).join(", ")}`;
        toast(msg, "ok");
        showDeploySuccess(msg, "#/operations");
      } else if (run) {
        await api("POST", `/api/v1/processes/${dep.key}/instances`, body);
        const n = body.variables ? Object.keys(body.variables).length : 0;
        const msg = `Deployed ${dep.processId} v${dep.version} and started an instance${n ? ` with ${n} variable${n === 1 ? "" : "s"}` : ""}`;
        toast(msg, "ok");
        showDeploySuccess(msg, await resolveInstanceLink(dep.key));
      } else {
        const msg = `Deployed ${dep.processId} v${dep.version}`;
        toast(msg, "ok");
        showDeploySuccess(msg, `#/operations/p/${dep.key}`);
      }
    } catch (e) {
      // The Atlas compiler rejects elements it can't execute yet — surface that
      // inline in the panel so the entered variables aren't lost.
      derr.textContent = e.message;
    } finally {
      drun.disabled = false;
      donly.disabled = false;
    }
  };

  drun.addEventListener("click", () => doDeploy(true));
  donly.addEventListener("click", () => doDeploy(false));
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
export async function mountLive(root, { api, toast, key, instance }) {
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
        <a class="btn neutral" id="edit-modeler" href="#/modeler/d/${key}" title="Open this definition in the Modeler">✎ Edit in Modeler</a>
        <button class="btn" id="start">Start instance</button>
        <button class="btn ghost danger" id="cancel-inst" hidden>Cancel instance</button>
        <a class="btn" id="replay-inst" hidden title="Replay this instance step by step">&#9654; Replay</a>
        <a class="btn" id="collab-link" hidden>⇄ Collaboration replay</a>
        <button class="btn neutral" id="refresh">Refresh</button>
        <button class="btn neutral" id="vars-toggle" aria-pressed="true" title="Show or hide the variables panel">Variables</button>
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
        <div class="props-resizer" id="var-resizer" title="Drag to resize the variables panel"></div>
        <aside class="var-panel side" id="var-panel"></aside>
      </div>
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
    // A pool of a collaboration can jump to the replay view, which shows every
    // pool at once and plays the messages that crossed between them (ADR-0038).
    if (isCollaborationRoot(viewer)) {
      const cl = root.querySelector("#collab-link");
      cl.href = `#/operations/c/${key}`;
      cl.hidden = false;
    }
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
  const jsonCollapsed = new Set(); // JSON variable names collapsed by the operator
  let varsHTML = "";               // last rendered variables markup, to skip no-op rebuilds
  // "all" or an instance key (as a string). A deep-linked instance (Deploy & run's
  // roundtrip link, or a shared URL) preselects that one; refreshInstances falls
  // back to "all" if it no longer exists.
  let selected = instance != null ? String(instance) : "all";
  let instances = [];       // this version's instances, cached for the picker/variables
  let instSig = "";         // signature of the picker's current option set
  let liveTasks = [];       // open user-task jobs for this version, refreshed each poll

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

  // tasksFor returns the open user-task jobs waiting in the given instance, newest
  // first — the "jump to the form" targets for a running instance.
  const tasksFor = (instanceKey) =>
    liveTasks.filter((t) => String(t.processInstanceKey) === String(instanceKey))
      .sort((a, b) => b.key - a.key);

  // renderTaskLinks lists a running instance's active user tasks as one-click links
  // into the Tasks app, landing straight on the task's form (ADR-0028) — so an
  // operator goes from "the token is on this user task" to "work it" without
  // hunting through the inbox.
  function renderTaskLinks(list) {
    if (!list || !list.length) return "";
    return `<div class="vp-tasks">
      <div class="vp-tasks-head">Waiting on ${list.length === 1 ? "a user task" : list.length + " user tasks"}</div>
      ${list.map((t) => `
        <a class="task-link" href="#/tasks/t/${t.key}" title="Open this task's form in the Tasks app">
          <span class="task-link-ic" aria-hidden="true">&#128203;</span>
          <span class="task-link-name">${esc(t.name || t.elementId || ("Task " + t.key))}</span>
          ${t.formId ? '<span class="task-link-tag">form</span>' : ""}
          <span class="task-link-go">Open &#8599;</span>
        </a>`).join("")}
    </div>`;
  }

  // copyAllBtn copies every variable of an instance as one JSON object ({name:
  // value}, JSON-typed values parsed back to structures) — the whole payload in one
  // click, the shape an operator pastes into a ticket or a re-run.
  function copyAllBtn(list) {
    if (!list || !list.length) return "";
    const obj = {};
    for (const v of list) {
      if (v.kind === "json") { try { obj[v.name] = JSON.parse(v.value); continue; } catch { /* keep raw */ } }
      if (v.kind === "number") { const n = Number(v.value); obj[v.name] = Number.isNaN(n) ? v.value : n; }
      else if (v.kind === "boolean") obj[v.name] = v.value === "true";
      else if (v.kind === "null") obj[v.name] = null;
      else obj[v.name] = v.value;
    }
    return `<button class="btn ghost small vcopy-all vcopy" type="button" title="Copy all variables as JSON" data-copy="${esc(JSON.stringify(obj, null, 2))}">Copy all</button>`;
  }

  // renderVariables shows the selected instance's variables with the shared
  // polished renderer (scalars as fields, JSON as collapsible highlighted cards),
  // or — for "All instances" — a compact per-instance overview table. It rebuilds
  // only when the markup actually changes, so a 1.5s poll never resets an expanded
  // JSON card's scroll or the operator's collapse choices.
  function renderVariables() {
    let html;
    if (selected === "all") {
      html = !instances.length
        ? `<div class="vp-head"><span class="vp-title">Variables</span></div>
          <p class="muted" style="margin:0">No instances yet — start one to see its variables here.</p>`
        : `<div class="vp-head"><span class="vp-title">Variables · all instances (${instances.length})</span></div>
        <div class="vp-insts">${instances.map((r) => {
          const ts = tasksFor(r.key);
          return `<div class="vp-inst">
            <div class="vp-inst-head">
              <b title="Select this instance">${r.key}</b>
              ${r.state === "active"
                ? '<span class="pill ok"><span class="dot"></span>active</span>'
                : `<span class="pill">${esc(r.state)}</span>`}
              <span class="vp-inst-actions">${ts.length
                ? `<a class="task-link inline" href="#/tasks/t/${ts[0].key}" title="Open the waiting task's form">&#128203; Task&#8599;</a>`
                : ""}<a class="replay-link" href="#/operations/i/${r.key}" title="Replay this instance step by step">&#9654; Replay</a></span>
            </div>
            <div class="vp-inst-vars">${varChips(r.variables)}</div>
          </div>`;
        }).join("")}</div>`;
    } else {
      const inst = instances.find((r) => String(r.key) === selected);
      if (!inst) {
        html = `<div class="vp-head"><span class="vp-title">Variables</span></div>
          <p class="muted" style="margin:0">Instance no longer available.</p>`;
      } else {
        const when = inst.state === "active" ? "" : fmtNano(inst.completedAt);
        html = `<div class="vp-head">
            <span class="vp-title">Variables · instance ${inst.key}
              ${inst.state === "active"
                ? '<span class="pill ok"><span class="dot"></span>active</span>'
                : `<span class="pill">${esc(inst.state)}</span>${when ? ` <span class="muted">${esc(when)}</span>` : ""}`}</span>
            <span class="vp-actions">${copyAllBtn(inst.variables)}<a class="replay-link" href="#/operations/i/${inst.key}" title="Replay this instance step by step">&#9654; Replay</a></span>
          </div>
          ${renderTaskLinks(tasksFor(inst.key))}
          <div class="vars">${renderVarsBody(inst.variables, jsonCollapsed)}</div>`;
      }
    }
    if (html === varsHTML) return; // nothing changed — keep the DOM (and scroll) intact
    varsHTML = html;
    varPanel.innerHTML = html;
  }

  async function poll() {
    await refreshInstances();
    await refreshTasks();
    updateCancelBtn();
    const q = selected === "all" ? "" : `?instance=${encodeURIComponent(selected)}`;
    let rt;
    try { rt = await api("GET", `/api/v1/processes/${key}/runtime${q}`); }
    catch (e) { return; } // transient; try again next tick
    if (current !== viewer) return; // navigated away mid-flight
    overlays.clear();
    for (const [id, marker] of marked) canvas.removeMarker(id, marker);
    marked = [];
    // The tasks in scope: a single instance's own, or (for "All instances") every
    // waiting task of this version. Grouped by element so a user-task element in
    // the diagram can be linked straight to its form.
    const scopeTasks = selected === "all"
      ? liveTasks.slice()
      : tasksFor(selected);
    const tasksByElement = new Map();
    for (const t of scopeTasks) {
      if (!t.elementId) continue;
      const arr = tasksByElement.get(t.elementId) || [];
      arr.push(t);
      tasksByElement.set(t.elementId, arr);
    }
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
      // A user-task element with a waiting job gets a clickable "Open" badge that
      // jumps to its form. One waiting task → straight to it; several (only under
      // "All instances") → the inbox, where the operator picks.
      const ets = tasksByElement.get(e.elementId);
      if (ets && ets.length) {
        const href = ets.length === 1 ? `#/tasks/t/${ets[0].key}` : "#/tasks";
        const label = ets.length === 1 ? "Open task" : `${ets.length} tasks`;
        overlays.add(e.elementId, "open-task", {
          position: { top: 4, right: 4 },
          html: `<a class="task-open" href="${href}" title="Open the waiting user task's form">&#128203; ${label}</a>`,
        });
      }
    }
    countEl.textContent = rt.instances;
    tokenEl.textContent = rt.tokens;
    renderVariables();
  }

  // refreshTasks pulls the open user-task jobs and keeps those for this deployed
  // version. Best-effort: a transient failure leaves the previous set so the links
  // and diagram badges don't flicker.
  async function refreshTasks() {
    let all;
    try { all = await api("GET", "/api/v1/tasks"); }
    catch { return; }
    liveTasks = all.filter((t) => t.processDefKey === key);
  }

  // The Cancel button targets the selected instance; it is shown only when a
  // single, still-active instance is selected (there is nothing to cancel for
  // "All instances" or an already-finished one).
  const cancelBtn = root.querySelector("#cancel-inst");
  const replayBtn = root.querySelector("#replay-inst");
  function updateCancelBtn() {
    const inst = instances.find((r) => String(r.key) === selected);
    cancelBtn.hidden = !(inst && inst.state === "active");
    // Replay is offered for any single selected instance (active or finished): its
    // step timeline drives a play/step/scrub walk through the diagram (ADR-0046).
    if (inst) {
      replayBtn.href = `#/operations/i/${inst.key}`;
      replayBtn.hidden = false;
    } else {
      replayBtn.hidden = true;
    }
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
  bindJsonCards(varPanel, jsonCollapsed, renderVariables);
  bindVarCopy(varPanel, toast);
  wireVarsPanel(root, viewer);

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

// sleep resolves after ms milliseconds (the pause between replayed messages).
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// runDotAnimation moves an envelope dot along a message-flow edge's waypoints over
// dur ms, in a dedicated SVG layer above the diagram (diagram coordinates, so it
// tracks pan/zoom). cancelled() lets the caller abort mid-flight (navigation or a
// superseding animation). Resolves when the dot reaches the target or is cancelled.
function runDotAnimation(layer, waypoints, dur, cancelled) {
  const NS = "http://www.w3.org/2000/svg";
  const g = document.createElementNS(NS, "g");
  const halo = document.createElementNS(NS, "circle");
  halo.setAttribute("r", "11"); halo.setAttribute("class", "atlas-msg-halo");
  const dot = document.createElementNS(NS, "circle");
  dot.setAttribute("r", "6"); dot.setAttribute("class", "atlas-msg-dot");
  g.appendChild(halo); g.appendChild(dot);
  layer.appendChild(g);

  const segs = [];
  let total = 0;
  for (let i = 1; i < waypoints.length; i++) {
    const a = waypoints[i - 1], b = waypoints[i];
    const len = Math.hypot(b.x - a.x, b.y - a.y);
    segs.push({ a, b, len });
    total += len;
  }
  return new Promise((resolve) => {
    let start = null;
    function frame(ts) {
      if (start == null) start = ts;
      const t = total ? Math.min(1, (ts - start) / dur) : 1;
      let d = t * total, x = waypoints[0].x, y = waypoints[0].y;
      for (const s of segs) {
        if (d <= s.len || s === segs[segs.length - 1]) {
          const k = s.len ? Math.min(1, d / s.len) : 1;
          x = s.a.x + (s.b.x - s.a.x) * k;
          y = s.a.y + (s.b.y - s.a.y) * k;
          break;
        }
        d -= s.len;
      }
      g.setAttribute("transform", `translate(${x} ${y})`);
      if (t < 1 && !cancelled()) {
        requestAnimationFrame(frame);
      } else {
        g.remove();
        resolve();
      }
    }
    requestAnimationFrame(frame);
  });
}

// mountCollaboration renders a whole collaboration read-only and replays the
// messages that flowed between its pools (ADR-0038). The shared diagram shows
// every pool at once; a merged token overlay (green live, gray visited) is polled
// like the single-process live view, and a transport bar plays the message-flow
// timeline — each delivery animates an envelope dot along its message-flow edge in
// the order it actually happened, so a finished collaboration replays its exchange.
export async function mountCollaboration(root, { api, toast, key }) {
  cleanup();

  root.innerHTML = `
    <div class="editor live">
      <div class="editor-bar">
        <a class="btn neutral" href="#/operations">&larr; Instances</a>
        <span class="crumbs" style="margin-left:8px">Replay &middot; <b id="collab-title">Collaboration</b></span>
        <div style="flex:1"></div>
        <button class="btn neutral" id="refresh">Refresh</button>
        <span class="pill ok" style="margin-left:8px"><span class="dot"></span><b id="inst-count">0</b>&nbsp;running</span>
        <span class="pill" style="margin-left:8px"><b id="flow-count">0</b>&nbsp;messages</span>
      </div>
      <div class="replay-bar">
        <button class="btn play" id="play">&#9654; Play</button>
        <button class="btn neutral" id="step-back" title="Previous message">&#9198;</button>
        <button class="btn neutral" id="step-fwd" title="Next message">&#9197;</button>
        <input type="range" id="scrub" min="0" max="0" value="0" step="1" aria-label="Message timeline"/>
        <label class="bar-select"><span>Speed</span>
          <select id="speed" class="speed">
            <option value="1">1&times;</option>
            <option value="2">2&times;</option>
            <option value="0.5">0.5&times;</option>
          </select></label>
        <span class="clock" id="clock">no messages yet</span>
      </div>
      <div class="editor-body"><div id="canvas"></div></div>
      <div class="flow-log" id="flow-log"></div>
      <div class="problems">
        <span class="legend-swatch live"></span> live token
        <span class="legend-swatch history" style="margin-left:12px"></span> passed through
        <span style="display:inline-block;width:10px;height:10px;border-radius:50%;background:var(--accent);margin:0 4px 0 12px;vertical-align:middle"></span> message flow
        <span style="flex:1"></span>
        <span class="muted">Live tokens poll every 1.5s</span>
      </div>
    </div>`;

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
      `<div class="coming"><p>Could not render this collaboration.</p>
       <p class="muted">${esc(e.message)}</p></div>`;
    return;
  }

  const canvas = viewer.get("canvas");
  const registry = viewer.get("elementRegistry");
  const layer = canvas.getLayer("atlas-replay", 900); // message dots ride above the diagram
  const titleEl = root.querySelector("#collab-title");
  const instEl = root.querySelector("#inst-count");
  const flowCountEl = root.querySelector("#flow-count");
  const clockEl = root.querySelector("#clock");
  const scrub = root.querySelector("#scrub");
  const playBtn = root.querySelector("#play");
  const logEl = root.querySelector("#flow-log");
  const speedSel = root.querySelector("#speed");

  let flows = [];    // message-flow timeline, oldest first
  let marked = [];   // token markers to clear on the next poll
  let playing = false;
  let playhead = 0;  // number of messages delivered so far (0..flows.length)
  let animToken = 0; // bumped to supersede an in-flight animation

  const speed = () => Number(speedSel.value) || 1;
  const fmtClock = (ns) => (ns ? new Date(ns / 1e6).toLocaleTimeString() : "");

  function receiverLabel(f) {
    const el = registry.get(f.receiverElementId);
    const bo = el && el.businessObject;
    return (bo && (bo.name || bo.id)) || f.receiverElementId || "?";
  }

  function updateClock() {
    if (!flows.length) { clockEl.textContent = "no messages yet"; return; }
    const shown = Math.min(playhead, flows.length);
    clockEl.textContent = `${shown} / ${flows.length}${shown ? ` · ${fmtClock(flows[shown - 1].at)}` : ""}`;
  }

  function highlightCurrent() {
    logEl.querySelectorAll("tr[data-i]").forEach((tr) => {
      const i = Number(tr.dataset.i);
      tr.classList.toggle("done", i < playhead);
      tr.classList.toggle("pending", i >= playhead);
      tr.classList.toggle("cur", i === playhead - 1);
    });
    const cur = logEl.querySelector("tr.cur");
    if (cur) cur.scrollIntoView({ block: "nearest" });
  }

  function setPlayhead(n) {
    playhead = Math.max(0, Math.min(flows.length, n));
    scrub.value = String(playhead);
    updateClock();
    highlightCurrent();
  }

  function renderLog() {
    if (!flows.length) {
      logEl.innerHTML = `<div class="fl-head">Messages</div>
        <div class="empty">No messages have flowed between the pools yet. Start an instance from a
        pool's live view, then come back to replay the exchange.</div>`;
      return;
    }
    const rows = flows.map((f, i) => `
      <tr data-i="${i}">
        <td class="tnum">${fmtClock(f.at)}</td>
        <td><span class="msg-name">${esc(f.messageName)}</span>${f.correlationKey
          ? ` <span class="muted">· ${esc(f.correlationKey)}</span>` : ""}</td>
        <td class="muted"><span class="arrow">&rarr;</span>${esc(receiverLabel(f))}</td>
      </tr>`).join("");
    logEl.innerHTML = `<div class="fl-head">Messages <span class="muted">· ${flows.length}</span></div>
      <table><tbody>${rows}</tbody></table>`;
    logEl.querySelectorAll("tr[data-i]").forEach((tr) => tr.addEventListener("click", () => {
      const i = Number(tr.dataset.i);
      pause();
      setPlayhead(i + 1);
      animateFlow(flows[i]);
    }));
    highlightCurrent();
  }

  // messageFlowTo finds the message-flow connection whose target is the receiving
  // element, so a delivery can be animated along the edge the modeler drew.
  function messageFlowTo(receiverId) {
    let found = null;
    registry.forEach((el) => {
      if (found) return;
      const bo = el.businessObject;
      if (bo && bo.$type === "bpmn:MessageFlow" && bo.targetRef && bo.targetRef.id === receiverId) found = el;
    });
    return found;
  }

  // animateFlow plays one delivery: the receiving element pulses, and if the
  // message-flow edge exists the envelope dot travels along it. Resolves when done.
  function animateFlow(f) {
    if (!f || current !== viewer) return Promise.resolve();
    if (registry.get(f.receiverElementId)) {
      canvas.addMarker(f.receiverElementId, "atlas-flow-hit");
      setTimeout(() => { try { canvas.removeMarker(f.receiverElementId, "atlas-flow-hit"); } catch { /* gone */ } }, 700);
    }
    const conn = messageFlowTo(f.receiverElementId);
    if (!conn || !conn.waypoints || conn.waypoints.length < 2) return Promise.resolve();
    canvas.addMarker(conn.id, "atlas-flow-active");
    const wps = conn.waypoints.map((w) => ({ x: w.x, y: w.y }));
    const token = ++animToken;
    return runDotAnimation(layer, wps, 900 / speed(), () => current !== viewer || token !== animToken)
      .finally(() => { try { canvas.removeMarker(conn.id, "atlas-flow-active"); } catch { /* gone */ } });
  }

  function pause() { playing = false; playBtn.innerHTML = "&#9654; Play"; }

  async function play() {
    if (!flows.length) return;
    if (playhead >= flows.length) setPlayhead(0); // wrap to replay from the start
    playing = true;
    playBtn.innerHTML = "&#9208; Pause";
    while (playing && current === viewer && playhead < flows.length) {
      const f = flows[playhead];
      setPlayhead(playhead + 1);
      await animateFlow(f);
      if (!playing || current !== viewer) break;
      // Pause between messages, scaled to the real gap (clamped) and the speed.
      const gap = playhead < flows.length
        ? Math.min(1200, Math.max(150, (flows[playhead].at - f.at) / 1e6))
        : 0;
      await sleep(gap / speed());
    }
    pause();
  }

  async function poll() {
    let rt;
    try { rt = await api("GET", `/api/v1/collaborations/${key}/runtime`); }
    catch { return; } // transient; try again next tick
    if (current !== viewer) return;
    if (rt.pools && rt.pools.length) {
      titleEl.textContent = rt.pools.map((p) => p.name || p.processId).join(" ⇄ ");
    }
    instEl.textContent = rt.instances;
    // Merged token overlay across all pools: green where a token is now, gray where
    // one has passed through — the same two-state heatmap the live view draws.
    for (const [id, m] of marked) canvas.removeMarker(id, m);
    marked = [];
    for (const e of rt.elements || []) {
      if (!registry.get(e.elementId)) continue;
      const live = e.tokens > 0;
      if (!live && !(e.visits > 0)) continue;
      const marker = live ? "atlas-active" : "atlas-visited";
      canvas.addMarker(e.elementId, marker);
      marked.push([e.elementId, marker]);
    }
    // Rebuild the timeline only when the message set grows, so a poll never
    // disturbs the operator's current scrub position mid-replay.
    const next = rt.messageFlows || [];
    if (next.length !== flows.length) {
      const wasAtEnd = playhead >= flows.length;
      flows = next;
      flowCountEl.textContent = String(flows.length);
      scrub.max = String(flows.length);
      renderLog();
      if (!playing && wasAtEnd) setPlayhead(flows.length); // follow new messages live
      else { scrub.value = String(playhead); updateClock(); highlightCurrent(); }
    }
  }

  playBtn.addEventListener("click", () => { playing ? pause() : play(); });
  root.querySelector("#step-fwd").addEventListener("click", () => {
    pause();
    if (playhead < flows.length) { const f = flows[playhead]; setPlayhead(playhead + 1); animateFlow(f); }
  });
  root.querySelector("#step-back").addEventListener("click", () => { pause(); setPlayhead(playhead - 1); });
  scrub.addEventListener("input", () => { pause(); setPlayhead(Number(scrub.value)); });
  root.querySelector("#refresh").addEventListener("click", poll);

  await poll();
  liveTimer = setInterval(poll, 1500);
}

// mountInstanceReplay renders one process instance read-only and replays it step
// by step (ADR-0046): the instance's definition diagram with a transport bar that
// walks the token through the elements in the order they activated. Each step
// pulses the entered element and, when the two steps are joined by a sequence
// flow, animates a token dot along that edge — the single-process analogue of the
// collaboration message-flow replay. The timeline is polled so a still-running
// instance keeps gaining steps live.
export async function mountInstanceReplay(root, { api, toast, key }) {
  cleanup();

  root.innerHTML = `
    <div class="editor live">
      <div class="editor-bar">
        <a class="btn neutral" href="#/operations">&larr; Instances</a>
        <span class="crumbs" style="margin-left:8px">Replay &middot; <b id="rp-title">Instance ${esc(String(key))}</b></span>
        <div style="flex:1"></div>
        <a class="btn neutral" id="rp-live" title="Open this instance's live view">Live view</a>
        <button class="btn neutral" id="refresh">Refresh</button>
        <span class="pill" id="rp-state" style="margin-left:8px">&mdash;</span>
        <span class="pill" style="margin-left:8px"><b id="step-count">0</b>&nbsp;frames</span>
      </div>
      <div class="replay-bar">
        <button class="btn play" id="play">&#9654; Play</button>
        <button class="btn neutral" id="step-back" title="Previous step">&#9198;</button>
        <button class="btn neutral" id="step-fwd" title="Next step">&#9197;</button>
        <input type="range" id="scrub" min="0" max="0" value="0" step="1" aria-label="Replay frames"/>
        <label class="bar-select"><span>Speed</span>
          <select id="speed" class="speed">
            <option value="1">1&times;</option>
            <option value="2">2&times;</option>
            <option value="0.5">0.5&times;</option>
          </select></label>
        <span class="clock" id="clock">no steps yet</span>
      </div>
      <div class="editor-body"><div id="canvas"></div></div>
      <div class="token-legend" id="token-legend" aria-label="Active replay tokens"></div>
      <div class="var-panel" id="rp-vars"></div>
      <div class="flow-log" id="step-log"></div>
      <div class="problems">
        <span class="legend-swatch live"></span> current step
        <span class="legend-swatch history" style="margin-left:12px"></span> already walked
        <span style="display:inline-block;width:10px;height:10px;border-radius:50%;background:var(--accent);margin:0 4px 0 12px;vertical-align:middle"></span> token move
        <span style="flex:1"></span>
        <span class="muted">Timeline polls every 1.5s</span>
      </div>
    </div>`;

  let lib;
  try {
    lib = await loadBpmn();
  } catch (e) {
    root.querySelector("#canvas").innerHTML = `<div class="coming"><p>${esc(e.message)}</p></div>`;
    return;
  }

  // The timeline resolves the instance's definition, which the diagram renders.
  let tl;
  try {
    tl = await api("GET", `/api/v1/instances/${key}/timeline`);
  } catch (e) {
    root.querySelector("#canvas").innerHTML =
      `<div class="coming"><p>Could not load this instance's replay.</p>
       <p class="muted">${esc(e.message)}</p></div>`;
    return;
  }
  root.querySelector("#rp-live").href = `#/operations/p/${tl.processDefKey}/i/${key}`;

  const viewer = newModeler(lib.BpmnJS, lib.moddle, root.querySelector("#canvas"));
  current = viewer;

  try {
    const xml = await api("GET", `/api/v1/processes/${tl.processDefKey}/xml`);
    await viewer.importXML(typeof xml === "string" ? xml : String(xml));
    viewer.get("canvas").zoom("fit-viewport");
  } catch (e) {
    root.querySelector("#canvas").innerHTML =
      `<div class="coming"><p>Could not render this instance's diagram.</p>
       <p class="muted">${esc(e.message)}</p></div>`;
    return;
  }

  const canvas = viewer.get("canvas");
  const registry = viewer.get("elementRegistry");
  const layer = canvas.getLayer("atlas-replay", 900); // token dot rides above the diagram
  const titleEl = root.querySelector("#rp-title");
  const stateEl = root.querySelector("#rp-state");
  const stepCountEl = root.querySelector("#step-count");
  const clockEl = root.querySelector("#clock");
  const scrub = root.querySelector("#scrub");
  const playBtn = root.querySelector("#play");
  const logEl = root.querySelector("#step-log");
  const varPanel = root.querySelector("#rp-vars");
  const speedSel = root.querySelector("#speed");

  let steps = [];    // element-activation audit timeline, oldest first
  let frames = [];   // complete logical token states, oldest first
  let marked = [];   // element markers to clear on the next render
  let playing = false;
  let playhead = 0;  // number of frames walked so far (0..frames.length)
  let animToken = 0; // bumped to supersede an in-flight animation
  const jsonCollapsed = new Set(); // JSON variable names the operator has collapsed (persists across scrubs)

  const speed = () => Number(speedSel.value) || 1;
  const tokenColors = ["#0072b2", "#d55e00", "#009e73", "#cc79a7", "#e69f00", "#56b4e9"];
  const tokenColor = (id) => tokenColors[Number(BigInt(String(id || 0)) % BigInt(tokenColors.length))];
  const fmtClock = (ns) => (ns ? new Date(ns / 1e6).toLocaleTimeString() : "");

  function stepLabel(s) {
    const el = registry.get(s.elementId);
    const bo = el && el.businessObject;
    return (bo && (bo.name || bo.id)) || s.elementId || "?";
  }
  // A short human label for a compiled node type ("bpmn:ServiceTask" → "Service task").
  function typeLabel(t) {
    const bare = String(t || "").replace(/^bpmn:/, "").replace(/([a-z])([A-Z])/g, "$1 $2");
    return bare ? bare.charAt(0).toUpperCase() + bare.slice(1).toLowerCase() : "";
  }

  // renderVars shows the variable values as they stood when the token entered the
  // current step (the per-step snapshot the timeline carries, ADR-0048), using the
  // shared polished renderer. At playhead 0 (before any step) it shows nothing yet.
  function renderVars() {
    const pos = playhead > 0 && playhead <= frames.length ? frames[playhead - 1].position : 0;
    const cur = [...steps].reverse().find((s) => s.position <= pos) || null;
    const head = cur
      ? `Variables &middot; as of step ${playhead} (${esc(stepLabel(cur))})`
      : "Variables";
    varPanel.innerHTML = `<div class="vp-head">${head}</div>
      <div class="vars">${renderVarsBody(cur ? cur.variables : [], jsonCollapsed)}</div>`;
  }

  function updateClock() {
    if (!frames.length) { clockEl.textContent = "no frames yet"; return; }
    const shown = Math.min(playhead, frames.length);
    clockEl.textContent = `${shown} / ${frames.length}${shown ? ` · ${fmtClock(frames[shown - 1].at)}` : ""}`;
  }

  // renderOverlay marks every element walked up to the playhead: the current one
  // green (the token is "here"), the earlier ones gray (already walked).
  function renderOverlay() {
    for (const [id, m] of marked) { try { canvas.removeMarker(id, m); } catch { /* gone */ } }
    marked = [];
    const frame = playhead ? frames[playhead - 1] : null;
    const position = frame ? frame.position : 0;
    for (const s of steps.filter((item) => item.position <= position)) {
      if (!registry.get(s.elementId)) continue;
      const marker = "atlas-visited";
      canvas.addMarker(s.elementId, marker);
      marked.push([s.elementId, marker]);
    }
    for (const token of (frame ? frame.tokens : [])) {
      if (!registry.get(token.elementId)) continue;
      const marker = token.state === "waiting" ? "atlas-token-waiting" : "atlas-active";
      canvas.addMarker(token.elementId, marker);
      marked.push([token.elementId, marker]);
    }
    const legend = root.querySelector("#token-legend");
    legend.innerHTML = (frame && frame.tokens.length) ? frame.tokens.map((token) =>
      `<span class="token-chip"><i style="--token-color:${tokenColor(token.tokenId)}">#</i>` +
      `Token ${esc(String(token.tokenId))} — ${esc(stepLabel(token))}${token.state === "waiting" ? " (waiting at join)" : ""}</span>`).join("")
      : `<span class="muted">No active tokens in this frame</span>`;
  }

  function highlightCurrent() {
    logEl.querySelectorAll("tr[data-i]").forEach((tr) => {
      const i = Number(tr.dataset.i);
      tr.classList.toggle("done", i < playhead);
      tr.classList.toggle("pending", i >= playhead);
      tr.classList.toggle("cur", i === playhead - 1);
    });
    const cur = logEl.querySelector("tr.cur");
    if (cur) cur.scrollIntoView({ block: "nearest" });
  }

  function setPlayhead(n) {
    playhead = Math.max(0, Math.min(frames.length, n));
    scrub.value = String(playhead);
    updateClock();
    renderOverlay();
    highlightCurrent();
    renderVars();
  }

  function renderLog() {
    if (!steps.length) {
      logEl.innerHTML = `<div class="fl-head">Steps</div>
        <div class="empty">No steps recorded for this instance yet.</div>`;
      return;
    }
    const rows = steps.map((s, i) => `
      <tr data-i="${i}">
        <td class="tnum">${i + 1}</td>
        <td class="tnum">${fmtClock(s.at)}</td>
        <td class="tnum">${s.tokenId ? `Token ${esc(String(s.tokenId))}` : "—"}</td>
        <td><span class="msg-name">${esc(stepLabel(s))}</span></td>
        <td class="muted">${esc(typeLabel(s.type))}</td>
        <td class="muted">${esc(s.action || "activate")}${s.relation ? ` · ${esc(s.relation)}` : ""}</td>
        <td class="tnum">${s.elementInstanceKey ? esc(String(s.elementInstanceKey)) : "—"}</td>
      </tr>`).join("");
    logEl.innerHTML = `<div class="fl-head">Engine activations <span class="muted">· ${steps.length} events</span></div>
      <table><thead><tr><th>#</th><th>At</th><th>Token</th><th>Element</th><th>Type</th><th>Action</th><th>Element instance</th></tr></thead><tbody>${rows}</tbody></table>`;
    logEl.querySelectorAll("tr[data-i]").forEach((tr) => tr.addEventListener("click", () => {
      const i = Number(tr.dataset.i);
      pause();
      const frame = frames.findIndex((f) => f.position >= steps[i].position);
      setPlayhead(frame < 0 ? frames.length : frame + 1);
      animateStep(i);
    }));
    highlightCurrent();
  }

  // sequenceFlowBetween finds the connection the modeler drew from srcId to tgtId,
  // so a step can animate a token dot along the edge the token actually traversed.
  function sequenceFlowBetween(srcId, tgtId) {
    let found = null;
    registry.forEach((el) => {
      if (found) return;
      const bo = el.businessObject;
      if (bo && bo.sourceRef && bo.targetRef && bo.sourceRef.id === srcId && bo.targetRef.id === tgtId
        && el.waypoints && el.waypoints.length >= 2) found = el;
    });
    return found;
  }

  // animateStep plays the walk INTO step i: the entered element pulses, and if a
  // sequence flow joins it to the previous step the token dot travels the edge.
  function animateStep(i) {
    if (i < 0 || i >= steps.length || current !== viewer) return Promise.resolve();
    const s = steps[i];
    if (registry.get(s.elementId)) {
      canvas.addMarker(s.elementId, "atlas-flow-hit");
      setTimeout(() => { try { canvas.removeMarker(s.elementId, "atlas-flow-hit"); } catch { /* gone */ } }, 700);
    }
    if (i === 0) return Promise.resolve(); // the start event has no incoming walk
    const conn = sequenceFlowBetween(steps[i - 1].elementId, s.elementId);
    if (!conn) return Promise.resolve(); // a jump with no drawn edge (e.g. across a gateway)
    canvas.addMarker(conn.id, "atlas-flow-active");
    const wps = conn.waypoints.map((w) => ({ x: w.x, y: w.y }));
    const token = ++animToken;
    return runDotAnimation(layer, wps, 700 / speed(), () => current !== viewer || token !== animToken)
      .finally(() => { try { canvas.removeMarker(conn.id, "atlas-flow-active"); } catch { /* gone */ } });
  }

  function pause() { playing = false; playBtn.innerHTML = "&#9654; Play"; }

  async function play() {
    if (!frames.length) return;
    if (playhead >= frames.length) setPlayhead(0); // wrap to replay from the start
    playing = true;
    playBtn.innerHTML = "&#9208; Pause";
    while (playing && current === viewer && playhead < frames.length) {
      const i = playhead;
      setPlayhead(playhead + 1);
      await sleep(120 / speed());
      if (!playing || current !== viewer) break;
      // Pause between steps, scaled to the real gap (clamped) and the speed.
      const gap = playhead < frames.length
        ? Math.min(1200, Math.max(150, (frames[playhead].at - frames[i].at) / 1e6))
        : 0;
      await sleep(gap / speed());
    }
    pause();
  }

  function applyStatePill(state) {
    stateEl.textContent = state || "—";
    stateEl.className = "pill" + (state === "active" ? " ok" : "");
  }

  async function poll() {
    let next;
    try { next = await api("GET", `/api/v1/instances/${key}/timeline`); }
    catch { return; } // transient; try again next tick
    if (current !== viewer) return;
    if (next.processId) titleEl.textContent = `${next.processId} · instance ${key}`;
    applyStatePill(next.state);
    // Rebuild only when the step set grows, so a poll never disturbs the operator's
    // current scrub position mid-replay.
    const list = next.steps || [], nextFrames = next.frames || [];
    if (list.length !== steps.length || nextFrames.length !== frames.length) {
      const wasAtEnd = playhead >= frames.length;
      steps = list; frames = nextFrames;
      stepCountEl.textContent = String(frames.length);
      scrub.max = String(frames.length);
      renderLog();
      if (!playing && wasAtEnd) setPlayhead(frames.length); // follow new frames live
      else { scrub.value = String(playhead); updateClock(); renderOverlay(); highlightCurrent(); renderVars(); }
    }
  }

  playBtn.addEventListener("click", () => { playing ? pause() : play(); });
  root.querySelector("#step-fwd").addEventListener("click", () => {
    pause();
    if (playhead < frames.length) setPlayhead(playhead + 1);
  });
  root.querySelector("#step-back").addEventListener("click", () => { pause(); setPlayhead(playhead - 1); });
  scrub.addEventListener("input", () => { pause(); setPlayhead(Number(scrub.value)); });
  root.querySelector("#refresh").addEventListener("click", poll);
  bindJsonCards(varPanel, jsonCollapsed, renderVars);
  bindVarCopy(varPanel, toast);

  await poll();
  liveTimer = setInterval(poll, 1500);
}
