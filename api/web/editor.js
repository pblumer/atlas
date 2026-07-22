// BPMN editor view. Embeds the vendored bpmn-js modeler (ADR-0013): the canvas,
// palette, and context pad come from bpmn-js; the Details panel and Deploy&run
// wiring are ours. Assets load lazily so non-editor pages stay light.

const BPMN_CSS = [
  "vendor/bpmn/assets/diagram-js.css",
  "vendor/bpmn/assets/bpmn-js.css",
  "vendor/bpmn/assets/bpmn-font/css/bpmn-embedded.css",
];

let bpmnReady; // memoized loader promise → { BpmnJS, zeebe }
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
        // Atlas executes (zeebe:script, zeebe:taskDefinition). See ADR-0013.
        const zeebe = await (await fetch("vendor/bpmn/zeebe.json")).json();
        resolve({ BpmnJS: window.BpmnJS, zeebe });
      } catch (e) {
        reject(new Error("failed to load the zeebe moddle: " + e.message));
      }
    };
    s.onerror = () => reject(new Error("failed to load the BPMN modeler assets"));
    document.head.appendChild(s);
  });
  return bpmnReady;
}

// newModeler/newViewer construct a bpmn-js instance with the zeebe moddle wired.
function newModeler(BpmnJS, zeebe, container) {
  return new BpmnJS({ container, moddleExtensions: { zeebe } });
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
      <div class="editor-body">
        <div id="canvas"></div>
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

  const modeler = newModeler(lib.BpmnJS, lib.zeebe, root.querySelector("#canvas"));
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

  const rerender = wireProperties(root, modeler);
  wireTabs(root, rerender);
  wireActions(root, modeler, api, toast);
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

// messageFieldsHTML renders the shared Message name + correlation-key inputs for
// a message catch or throw event. med is the bpmn:MessageEventDefinition.
function messageFieldsHTML(med, hint) {
  const msg = med.messageRef;
  const name = (msg && msg.name) || "";
  let key = "";
  const vals = msg && msg.extensionElements && msg.extensionElements.values;
  if (vals) {
    const sub = vals.find((v) => v.$type === "zeebe:Subscription");
    if (sub && sub.correlationKey) key = sub.correlationKey.replace(/^=\s*/, "");
  }
  return `<h3>Message</h3>
    <label class="field"><span>Message name</span>
      <input type="text" id="f-msgname" value="${esc(name)}" placeholder="payment-received"/></label>
    <label class="field"><span>Correlation key (FEEL)</span>
      <input type="text" id="f-corrkey" value="${esc(key)}" placeholder="orderId"/></label>
    <p class="muted" style="font-size:12px">${hint}</p>`;
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

// upsertMessage points a message event definition at a top-level bpmn:Message
// with the given name and zeebe correlation key, creating the message (and its
// zeebe:subscription) if needed. The messageRef change goes through the modeling
// API (undo/redo); the message element is registered on the definitions so it
// serializes on deploy. A leading '=' on the key is normalized to Zeebe form.
function upsertMessage(modeler, element, med, name, correlationKey) {
  const moddle = modeler.get("moddle");
  const modeling = modeler.get("modeling");
  let msg = med.messageRef;
  if (!msg) {
    msg = moddle.create("bpmn:Message");
    msg.id = "Message_" + Math.random().toString(36).slice(2, 8);
    const defs = definitionsOf(modeler);
    if (defs) {
      msg.$parent = defs;
      defs.rootElements = [...(defs.rootElements || []), msg];
    }
  }
  msg.name = name;
  let ext = msg.extensionElements;
  if (!ext) { ext = moddle.create("bpmn:ExtensionElements", { values: [] }); ext.$parent = msg; }
  let sub = (ext.values || []).find((v) => v.$type === "zeebe:Subscription");
  if (!sub) { sub = moddle.create("zeebe:Subscription"); sub.$parent = ext; ext.values = [...(ext.values || []), sub]; }
  const key = (correlationKey || "").trim();
  sub.correlationKey = key === "" ? "" : (key.startsWith("=") ? key : "= " + key);
  msg.extensionElements = ext;
  modeling.updateModdleProperties(element, med, { messageRef: msg });
}

// rootProcess returns the diagram's process business object, or null if the root
// isn't a plain process (e.g. a collaboration with pools).
function rootProcess(modeler) {
  try {
    const bo = modeler.get("canvas").getRootElement().businessObject;
    return bo && /:Process$/.test(bo.$type || "") ? bo : null;
  } catch { return null; }
}

function wireProperties(root, modeler) {
  const icon = root.querySelector("#p-icon");
  const typename = root.querySelector("#p-typename");
  const nameEl = root.querySelector("#p-name");
  const body = root.querySelector("#p-body");
  const modeling = modeler.get("modeling");
  const selection = modeler.get("selection");

  function show(element) {
    if (!element) {
      // Nothing selected → show the process itself, so its id/name can be edited
      // (this is how you rename a diagram; the id is the deployed process id).
      const rootBo = rootProcess(modeler);
      if (rootBo) {
        icon.textContent = "PR"; typename.textContent = "Process";
        nameEl.textContent = rootBo.name || rootBo.id || "(process)";
        body.innerHTML = `
          <h3>Process</h3>
          <label class="field"><span>Name</span><input type="text" id="f-pname" value="${esc(rootBo.name || "")}" placeholder="Order fulfillment"/></label>
          <label class="field"><span>Process ID</span><input type="text" id="f-pid" value="${esc(rootBo.id || "")}" placeholder="order-fulfillment"/></label>
          <p class="muted" style="font-size:12px">The Process ID is the identity deployments and instances are grouped by. Renaming it and deploying creates a new process rather than a new version.</p>`;
        const rootEl = modeler.get("canvas").getRootElement();
        body.querySelector("#f-pname").addEventListener("change", (e) => {
          try { modeling.updateProperties(rootEl, { name: e.target.value }); } catch { /* ignore */ }
        });
        body.querySelector("#f-pid").addEventListener("change", (e) => {
          const v = (e.target.value || "").trim();
          if (v) { try { modeling.updateProperties(rootEl, { id: v }); } catch { toast("invalid process id", "err"); } }
        });
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
          html += messageFieldsHTML(msg, "The event waits until a message with this name and a matching correlation key is published.");
        } else {
          html += `<p class="muted" style="font-size:12px">Use the wrench icon on the element to make this a <b>Timer</b> or <b>Message</b> intermediate catch event, then configure it here.</p>`;
        }
      } else if (bo.$type === "bpmn:IntermediateThrowEvent") {
        const msg = messageDefOf(bo);
        if (msg) {
          html += messageFieldsHTML(msg, "On reaching this event the message is published; any instance waiting on the same name and correlation key continues.");
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
    const saveScript = () => {
      const raw = (fexpr.value || "").trim();
      upsertExt(modeler, element, "zeebe:Script", {
        expression: raw === "" ? "" : (raw.startsWith("=") ? raw : "= " + raw),
        resultVariable: (fresult.value || "").trim(),
      });
    };
    if (fexpr) fexpr.addEventListener("change", saveScript);
    if (fresult) fresult.addEventListener("change", saveScript);

    const fjob = body.querySelector("#f-jobtype");
    if (fjob) {
      fjob.addEventListener("change", () => {
        upsertExt(modeler, element, "zeebe:TaskDefinition", { type: (fjob.value || "").trim() });
      });
    }

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

    const fmsgname = body.querySelector("#f-msgname");
    const fcorrkey = body.querySelector("#f-corrkey");
    if (fmsgname || fcorrkey) {
      const saveMsg = () => {
        const med = messageDefOf(element.businessObject);
        if (!med) return;
        upsertMessage(modeler, element, med,
          (fmsgname && fmsgname.value || "").trim(),
          (fcorrkey && fcorrkey.value || "").trim());
      };
      if (fmsgname) fmsgname.addEventListener("change", saveMsg);
      if (fcorrkey) fcorrkey.addEventListener("change", saveMsg);
    }

    const fcond = body.querySelector("#f-cond");
    if (fcond) {
      fcond.addEventListener("change", () => {
        const raw = (fcond.value || "").trim();
        if (raw === "") {
          // Clearing the field removes the guard, turning the branch unconditional.
          try { modeling.updateProperties(element, { conditionExpression: undefined }); } catch { /* stale */ }
          return;
        }
        // Store as a FEEL expression, '=' prefixed per Zeebe (Atlas strips it).
        const moddle = modeler.get("moddle");
        const expr = moddle.create("bpmn:FormalExpression", {
          body: raw.startsWith("=") ? raw : "= " + raw,
        });
        expr.$parent = element.businessObject;
        try { modeling.updateProperties(element, { conditionExpression: expr }); } catch { /* stale */ }
      });
    }
  }

  modeler.on("selection.changed", (e) => show((e.newSelection || [])[0]));
  modeler.on("element.changed", (e) => {
    const sel = selection.get();
    if (sel[0] && e.element && sel[0].id === e.element.id) show(sel[0]);
  });
  show(null);

  // Returned so a Design/Implement tab switch re-renders the current selection.
  return () => show(selection.get()[0]);
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

  const deployBtn = root.querySelector("#deploy");
  deployBtn.addEventListener("click", async () => {
    deployBtn.disabled = true;
    try {
      const { xml } = await modeler.saveXML({ format: true });
      const dep = await api("POST", "/api/v1/deployments", xml, true);
      await api("POST", `/api/v1/processes/${dep.key}/instances`, {});
      toast(`Deployed ${dep.processId} v${dep.version} and started an instance`, "ok");
    } catch (e) {
      // The Atlas compiler rejects elements it can't execute yet — surface that.
      toast("deploy failed: " + e.message, "err");
    } finally {
      deployBtn.disabled = false;
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
        <button class="btn neutral" id="refresh">Refresh</button>
        <span class="pill ok" style="margin-left:8px"><span class="dot"></span><b id="inst-count">0</b>&nbsp;running</span>
        <span class="pill" style="margin-left:8px"><b id="token-count">0</b>&nbsp;tokens total</span>
      </div>
      <div class="start-panel" id="start-panel" hidden>
        <label class="field">
          <span>Start variables — a JSON object of scalars (number, string, boolean, null). Leave empty to start with none.</span>
          <textarea id="start-vars" rows="4" spellcheck="false" placeholder='{ "amount": 100, "customer": "acme", "priority": true }'></textarea>
        </label>
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

  const viewer = newModeler(lib.BpmnJS, lib.zeebe, root.querySelector("#canvas"));
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

  const varChips = (list) => !list || !list.length
    ? '<span class="muted">No variables.</span>'
    : list.map((v) => `<span class="chip">${esc(v.name)}=${esc(v.value)}</span>`).join(" ");
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

  // Selecting an instance isolates it on the diagram; re-poll right away so the
  // overlay and variables switch without waiting for the next tick.
  instSel.addEventListener("change", () => { selected = instSel.value; poll(); });

  root.querySelector("#refresh").addEventListener("click", poll);

  // Start a fresh instance of this already-deployed definition. The demo and the
  // Modeler's "Deploy & run" both couple starting to a deployment; this is the
  // path for a model that's already live — start it again straight from its view,
  // optionally seeded with start variables entered in the panel below the toolbar.
  const startBtn = root.querySelector("#start");
  const panel = root.querySelector("#start-panel");
  const varsEl = root.querySelector("#start-vars");
  const goBtn = root.querySelector("#start-go");
  const errEl = root.querySelector("#start-err");
  const closePanel = () => { panel.hidden = true; errEl.textContent = ""; };

  startBtn.addEventListener("click", () => {
    panel.hidden = !panel.hidden;
    errEl.textContent = "";
    if (!panel.hidden) varsEl.focus();
  });
  root.querySelector("#start-cancel").addEventListener("click", closePanel);

  // Turn the textarea into a request body, validating client-side so an obvious
  // typo fails here instead of after a round-trip. Empty means no variables. The
  // server accepts only scalars (parseStartVariables), so reject objects/arrays.
  function buildBody() {
    const raw = varsEl.value.trim();
    if (!raw) return {};
    let obj;
    try { obj = JSON.parse(raw); }
    catch (e) { throw new Error("not valid JSON: " + e.message); }
    if (obj === null || typeof obj !== "object" || Array.isArray(obj)) {
      throw new Error('expected a JSON object, e.g. { "amount": 100 }');
    }
    for (const [name, v] of Object.entries(obj)) {
      const t = typeof v;
      if (v !== null && t !== "number" && t !== "string" && t !== "boolean") {
        throw new Error(`variable "${name}": only scalar values (number, string, boolean, null)`);
      }
    }
    return { variables: obj };
  }

  goBtn.addEventListener("click", async () => {
    let body;
    try { body = buildBody(); }
    catch (e) { errEl.textContent = e.message; return; }
    goBtn.disabled = true;
    try {
      await api("POST", `/api/v1/processes/${key}/instances`, body);
      const n = body.variables ? Object.keys(body.variables).length : 0;
      toast(n ? `Started a new instance with ${n} variable${n === 1 ? "" : "s"}` : "Started a new instance", "ok");
      closePanel();
      varsEl.value = "";
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
