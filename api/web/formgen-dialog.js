// "Write this form for me" — the dialog behind the form editor's Generate button
// (ADR-draft-ai-form-generation).
//
// The blank form is the expensive part of a form. Somebody who knows exactly what the
// process needs still has to place fourteen components, key each one, and remember which
// of them the next step actually reads. The server can ask a model for a first draft of
// that from two things the author already has: a sentence about what the form is for,
// and the process it belongs to — whose steps, documentation and variable names are
// already written down in the diagram.
//
// What this dialog is careful about is the word *draft*. Nothing it produces is saved.
// The schema comes back into the editor unsaved, under the id the editor was already
// holding, and the author reads it, fixes it and presses Save themselves — the same gate
// a hand-drawn form passes. So the affordance is deliberately not a wand that replaces
// the editor; it is a way to arrive at the editor with something on the canvas.
//
// It is its own module, and not a slab inside form-editor.js, for the reason pickmodal.js
// and workerdialog.js are: app.js boots the whole console on import, so anything left
// inside it can only ever be exercised by hand.

const esc = (s) => String(s == null ? "" : s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// generationWorkers asks whether there is anything to generate *with*, and returns the
// AI Workers this account may name — empty when none is configured, and empty when the
// question itself fails.
//
// The empty answer is what hides the button. An affordance whose only possible outcome
// is "no AI Worker is configured" teaches an author that the feature does not work,
// which is a worse outcome than never having seen it; and a server too old to know the
// route answers 404, which reads the same way and should.
export async function generationWorkers(api) {
  try {
    const cap = await api("GET", "/api/v1/forms/generate/workers");
    return cap && cap.available && Array.isArray(cap.workers) ? cap.workers : [];
  } catch {
    return [];
  }
}

// listProcesses gathers what the author can point the generator at: the drafts they are
// working on and the versions that are deployed, drafts first because a draft is what
// somebody generating a form is usually looking at. A process with both appears once —
// the server reads the draft in that case, so the entry says draft.
async function listProcesses(api) {
  const out = [];
  const seen = new Set();
  const add = (id, name, origin, key) => {
    if (!id || seen.has(id)) return;
    seen.add(id);
    out.push({ id, name: name || id, origin, key });
  };
  try {
    for (const d of await api("GET", "/api/v1/drafts")) add(d.processId, d.name, "draft");
  } catch { /* a listing that fails leaves the picker shorter, not broken */ }
  try {
    for (const p of await api("GET", "/api/v1/processes")) add(p.processId, p.name, "deployment", p.key);
  } catch { /* as above */ }
  return out;
}

// listSteps reads a process's own BPMN for the steps a form can belong to: its user
// tasks. It is read in the browser because the browser is where the choice is made and
// the XML is one request away; the server reads the same model again when it generates,
// from design-time state rather than from what was posted to it.
async function listSteps(api, process) {
  const path = process.origin === "draft"
    ? "/api/v1/drafts/" + encodeURIComponent(process.id) + "/xml"
    : "/api/v1/processes/" + encodeURIComponent(process.key) + "/xml";
  let xml;
  try {
    xml = await api("GET", path);
  } catch {
    return [];
  }
  const doc = new DOMParser().parseFromString(String(xml), "application/xml");
  if (doc.querySelector("parsererror")) return [];
  return Array.from(doc.getElementsByTagName("*"))
    .filter((el) => el.localName === "userTask" && el.getAttribute("id"))
    .map((el) => ({ id: el.getAttribute("id"), name: el.getAttribute("name") || el.getAttribute("id") }));
}

// openFormGenerator asks for the brief and returns the generated schema, or null when
// the author closed the dialog. It resolves only on a generation that succeeded: a
// failure is shown inside the dialog, where the brief that caused it is still on screen
// and can be changed.
export function openFormGenerator({ api, workers = [], formId = "", schema = null, forProcess = "", forStep = "" }) {
  return new Promise((resolve) => {
    const refinable = !!(schema && Array.isArray(schema.components) && schema.components.length > 0);
    const ov = document.createElement("div");
    ov.className = "modal-ov";
    ov.innerHTML = `
      <div class="modal fg-modal" role="dialog" aria-modal="true" aria-label="Generate this form">
        <div class="modal-head">
          <h2>Generate this form</h2>
          <button type="button" class="icon-btn" data-x aria-label="Close" title="Close">✕</button>
        </div>
        <div class="modal-body">
          <label class="field"><span>What should this form ask for?</span>
            <textarea id="fg-brief" rows="5" spellcheck="false"
              placeholder="Ein Antrag auf Sonderurlaub: Grund, Zeitraum und ob Vertretung geregelt ist."></textarea></label>
          <label class="field"><span>Process it belongs to <em>(optional, and worth it)</em></span>
            <select id="fg-process"><option value="">— none —</option></select></label>
          <label class="field" id="fg-step-field" hidden><span>Step</span>
            <select id="fg-step"><option value="">Start form — this form starts the process</option></select></label>
          ${workers.length > 1 ? `<label class="field"><span>AI Worker</span>
            <select id="fg-worker">${workers.map((w) =>
              `<option value="${esc(w.name)}">${esc(w.name)}${w.model ? " — " + esc(w.model) : ""}</option>`).join("")}</select></label>`
            : `<p class="muted small" id="fg-worker-note">Written by ${esc(workers[0] ? workers[0].name : "the configured AI Worker")}${
              workers[0] && workers[0].model ? " (" + esc(workers[0].model) + ")" : ""}.</p>`}
          ${refinable ? `<label class="field inline"><input type="checkbox" id="fg-refine" checked>
            <span>Change the form that is open, rather than starting from nothing</span></label>` : ""}
          <p class="muted small">Nothing is saved. What comes back opens in the editor for you to read,
            change and save yourself.</p>
          <p class="warn-note" id="fg-err" hidden></p>
        </div>
        <div class="modal-foot">
          <span class="muted small" id="fg-status"></span>
          <span class="modal-actions">
            <button type="button" class="btn ghost" data-cancel>Cancel</button>
            <button type="button" class="btn" data-ok>Generate</button>
          </span>
        </div>
      </div>`;
    document.body.appendChild(ov);

    const brief = ov.querySelector("#fg-brief");
    const procSel = ov.querySelector("#fg-process");
    const stepField = ov.querySelector("#fg-step-field");
    const stepSel = ov.querySelector("#fg-step");
    const workerSel = ov.querySelector("#fg-worker");
    const refine = ov.querySelector("#fg-refine");
    const errEl = ov.querySelector("#fg-err");
    const statusEl = ov.querySelector("#fg-status");
    const okBtn = ov.querySelector("[data-ok]");

    let processes = [];
    let settled = false;
    let running = false;

    const finish = (result) => {
      if (settled) return;
      settled = true;
      ov.remove();
      document.removeEventListener("keydown", onKey);
      resolve(result);
    };
    const cancel = () => { if (!running) finish(null); };
    const fail = (message) => {
      errEl.textContent = message;
      errEl.hidden = false;
    };

    // The process picker. It is optional and it is the difference between a plausible
    // form and the right one, which is why the label says so.
    (async () => {
      processes = await listProcesses(api);
      if (settled) return;
      for (const p of processes) {
        const opt = document.createElement("option");
        opt.value = p.id;
        opt.textContent = p.name === p.id ? p.id : `${p.name} (${p.id})`;
        procSel.appendChild(opt);
      }
      // Opened from a step in the Modeler — "Create a new form" on a user task, or on
      // a start event — the process and the step are already known, and pressing that
      // link was the author saying so. They arrive selected, and all that is left to
      // write is the brief.
      //
      // A process the picker does not list leaves both unset rather than pretending to
      // a selection the request could not honour: a pool of a collaboration is filed
      // under the first pool's id, and a draft can be deleted between the two screens.
      if (forProcess && processes.some((p) => p.id === forProcess)) {
        procSel.value = forProcess;
        await loadSteps();
        if (settled) return;
        if (forStep && Array.from(stepSel.options).some((o) => o.value === forStep)) {
          stepSel.value = forStep;
        }
      }
    })();

    async function loadSteps() {
      const process = processes.find((p) => p.id === procSel.value);
      stepSel.length = 1; // keep the "start form" entry
      stepField.hidden = !process;
      if (!process) return;
      for (const step of await listSteps(api, process)) {
        if (settled) return;
        const opt = document.createElement("option");
        opt.value = step.id;
        opt.textContent = step.name === step.id ? step.id : `${step.name} (${step.id})`;
        stepSel.appendChild(opt);
      }
    }
    procSel.addEventListener("change", () => { loadSteps(); });

    async function submit() {
      if (running) return;
      errEl.hidden = true;
      const description = brief.value.trim();
      if (!description && !procSel.value) {
        fail("Say what the form is for, or pick the process it belongs to.");
        brief.focus();
        return;
      }
      running = true;
      okBtn.disabled = true;
      okBtn.textContent = "Generating…";
      // A model call is seconds, sometimes a good deal more, and a dialog that says
      // nothing for that long reads as one that has stopped working.
      statusEl.textContent = "Asking the AI Worker — this can take a moment.";
      const body = { description, formId };
      if (procSel.value) body.processId = procSel.value;
      if (stepSel.value) body.elementId = stepSel.value;
      if (workerSel) body.worker = workerSel.value;
      if (refine && refine.checked && schema) body.schema = schema;
      try {
        const got = await api("POST", "/api/v1/forms/generate", body);
        finish(got);
      } catch (e) {
        running = false;
        okBtn.disabled = false;
        okBtn.textContent = "Generate";
        statusEl.textContent = "";
        fail(e && e.message ? e.message : "The generation failed.");
      }
    }

    const onKey = (e) => {
      if (e.key === "Escape") { e.preventDefault(); cancel(); return; }
      // Enter submits from anywhere but the brief, where it is a newline: the brief is
      // prose, and prose has paragraphs.
      if (e.key === "Enter" && document.activeElement !== brief && ov.contains(document.activeElement)) {
        e.preventDefault();
        submit();
      }
    };
    document.addEventListener("keydown", onKey);
    ov.addEventListener("mousedown", (e) => { if (e.target === ov) cancel(); });
    ov.querySelector("[data-x]").addEventListener("click", cancel);
    ov.querySelector("[data-cancel]").addEventListener("click", cancel);
    okBtn.addEventListener("click", submit);
    brief.focus();
  });
}
