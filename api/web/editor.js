// BPMN editor view. Embeds the vendored bpmn-js modeler (ADR-0013): the canvas,
// palette, and context pad come from bpmn-js; the Details panel and Deploy&run
// wiring are ours. Assets load lazily so non-editor pages stay light.

const BPMN_CSS = [
  "vendor/bpmn/assets/diagram-js.css",
  "vendor/bpmn/assets/bpmn-js.css",
  "vendor/bpmn/assets/bpmn-font/css/bpmn-embedded.css",
];

let bpmnReady; // memoized loader promise
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
    s.onload = () => resolve(window.BpmnJS);
    s.onerror = () => reject(new Error("failed to load the BPMN modeler assets"));
    document.head.appendChild(s);
  });
  return bpmnReady;
}

const BLANK = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:dc="http://www.omg.org/spec/DD/20100524/DC"
  id="Definitions_new" targetNamespace="http://atlas/bpmn">
  <bpmn:process id="Process_new" isExecutable="true">
    <bpmn:startEvent id="StartEvent_1" name="Start"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Process_new">
      <bpmndi:BPMNShape id="StartEvent_1_di" bpmnElement="StartEvent_1">
        <dc:Bounds x="180" y="160" width="36" height="36"/>
      </bpmndi:BPMNShape>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`;

let current; // active modeler, destroyed on remount

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

const shortType = (t) => (t || "").replace(/^bpmn:/, "");

export async function mountEditor(root, { api, toast, key }) {
  if (current) { try { current.destroy(); } catch { /* ignore */ } current = null; }

  root.innerHTML = `
    <div class="editor">
      <div class="editor-bar">
        <span class="crumbs">${key == null ? "New diagram" : "Deployment " + key}</span>
        <div class="etabs">
          <button data-tab="design" class="active">Design</button>
          <button data-tab="implement">Implement</button>
          <button data-tab="play">Play</button>
        </div>
        <div style="flex:1"></div>
        <button class="btn neutral" id="export">Export XML</button>
        <button class="btn" id="deploy">Deploy &amp; run</button>
      </div>
      <div class="editor-body">
        <div id="canvas"></div>
        <div id="play" class="coming" hidden>
          <div><p><b>Play</b></p><p class="muted">Interactive token play is on the roadmap.</p></div>
        </div>
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

  let BpmnJS;
  try {
    BpmnJS = await loadBpmn();
  } catch (e) {
    document.getElementById("canvas").innerHTML =
      `<div class="coming"><p>${esc(e.message)}</p></div>`;
    return;
  }

  const modeler = new BpmnJS({ container: root.querySelector("#canvas") });
  current = modeler;

  // Load content.
  try {
    if (key == null) {
      await modeler.importXML(BLANK);
    } else {
      const xml = await api("GET", `/api/v1/processes/${key}/xml`);
      await modeler.importXML(typeof xml === "string" ? xml : String(xml));
    }
    modeler.get("canvas").zoom("fit-viewport");
  } catch (e) {
    toast("could not open diagram: " + e.message, "err");
  }

  wireProperties(root, modeler);
  wireTabs(root);
  wireActions(root, modeler, api, toast);
}

function wireTabs(root) {
  const canvas = root.querySelector("#canvas");
  const play = root.querySelector("#play");
  root.querySelectorAll(".etabs button").forEach((b) => {
    b.addEventListener("click", () => {
      root.querySelectorAll(".etabs button").forEach((x) => x.classList.remove("active"));
      b.classList.add("active");
      const isPlay = b.dataset.tab === "play";
      play.hidden = !isPlay;
      canvas.style.visibility = isPlay ? "hidden" : "visible";
    });
  });
}

function wireProperties(root, modeler) {
  const icon = root.querySelector("#p-icon");
  const typename = root.querySelector("#p-typename");
  const nameEl = root.querySelector("#p-name");
  const body = root.querySelector("#p-body");
  const modeling = modeler.get("modeling");

  function show(element) {
    if (!element) {
      icon.textContent = "–"; typename.textContent = "No selection"; nameEl.textContent = "—";
      body.innerHTML = `<p class="muted">Select an element to see its properties.</p>`;
      return;
    }
    const bo = element.businessObject || {};
    const type = shortType(element.type);
    icon.textContent = type.slice(0, 2).toUpperCase();
    typename.textContent = type;
    nameEl.textContent = bo.name || bo.id || "(unnamed)";
    body.innerHTML = `
      <h3>General</h3>
      <label class="field"><span>Name</span><input type="text" id="f-name" value="${esc(bo.name || "")}"/></label>
      <label class="field"><span>ID</span><input type="text" id="f-id" value="${esc(bo.id || "")}" readonly/></label>
      <label class="field"><span>Type</span><input type="text" value="${esc(type)}" readonly/></label>`;
    const fname = body.querySelector("#f-name");
    fname.addEventListener("change", () => {
      try { modeling.updateProperties(element, { name: fname.value }); }
      catch (e) { /* selection may have changed */ }
    });
  }

  modeler.on("selection.changed", (e) => show((e.newSelection || [])[0]));
  modeler.on("element.changed", (e) => {
    const sel = modeler.get("selection").get();
    if (sel[0] && e.element && sel[0].id === e.element.id) show(sel[0]);
  });
  show(null);
}

function wireActions(root, modeler, api, toast) {
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
