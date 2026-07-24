// Form editor view. Embeds the vendored form-js Playground (ADR-0028): the
// reference modeler's split surface — Form Definition (editor) beside Form
// Preview (live viewer), with Form Input (sample data) and Form Output (the
// live result) below. Only the toolbar and Save wiring are ours. Assets load
// lazily so non-editor pages stay light, mirroring the BPMN editor (editor.js).

const FORM_CSS = "vendor/form-js/form-playground.css";

let playgroundReady; // memoized loader promise → { Playground }
function loadPlayground() {
  if (!playgroundReady) {
    if (!document.querySelector(`link[href="${FORM_CSS}"]`)) {
      const l = document.createElement("link");
      l.rel = "stylesheet";
      l.href = FORM_CSS;
      document.head.appendChild(l);
    }
    playgroundReady = import("./vendor/form-js/form-playground.js");
  }
  return playgroundReady;
}

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// A blank form-js schema; the editor fills in schemaVersion and its own ids.
const blankSchema = () => ({ type: "default", components: [] });

// newFormId mints a stable, filename-safe id for a new form, echoing the
// "form-xxxx" ids the reference modeler generates.
function newFormId() {
  return "form-" + Date.now().toString(36) + Math.random().toString(36).slice(2, 6);
}

let current; // active Playground instance, destroyed on remount/leave

export function cleanup() {
  if (current) { try { current.destroy(); } catch { /* ignore */ } current = null; }
}

// mountFormEditor renders the form editor into root. With formId it edits an
// existing form; without it creates a new one (optionally seeded into projectId).
export async function mountFormEditor(root, { api, toast, formId, projectId }) {
  cleanup();
  // Claim the shared cleanup slot so navigating away tears this editor down
  // (the BPMN editor reclaims it the same way when it mounts).
  window.__atlasCleanup = cleanup;

  const isNew = formId == null;
  root.innerHTML = `
    <div class="editor">
      <div class="editor-bar">
        <a class="crumbs" href="#/modeler">&larr; Forms</a>
        <input id="form-name" class="form-name-input" placeholder="Form name" spellcheck="false" />
        <span class="chip" id="form-id-chip"></span>
        <div style="flex:1"></div>
        <span class="muted" id="form-status"></span>
        <button class="btn" id="form-save">Save</button>
      </div>
      <div class="editor-body">
        <div id="form-playground" class="form-playground"><p class="muted" style="padding:20px">Loading form editor&hellip;</p></div>
      </div>
    </div>`;

  const nameInput = root.querySelector("#form-name");
  const idChip = root.querySelector("#form-id-chip");
  const statusEl = root.querySelector("#form-status");
  const container = root.querySelector("#form-playground");

  // Resolve the form's identity and initial schema.
  let id = formId;
  let name = "";
  let schema = blankSchema();
  let project = projectId || "";
  if (!isNew) {
    try {
      const def = await api("GET", "/api/v1/forms/" + encodeURIComponent(formId));
      id = def.id;
      name = def.name || def.id;
      project = def.projectId || "";
      if (def.schema && typeof def.schema === "object") schema = def.schema;
    } catch (e) {
      container.innerHTML = `<p class="muted err" style="padding:20px">Failed to load form: ${esc(e.message)}</p>`;
      return;
    }
  } else {
    id = newFormId();
  }
  nameInput.value = name;
  idChip.textContent = id;

  let Playground;
  try {
    ({ Playground } = await loadPlayground());
  } catch (e) {
    container.innerHTML = `<p class="muted err" style="padding:20px">Failed to load the form editor: ${esc(e.message)}</p>`;
    return;
  }

  container.innerHTML = "";
  try {
    // The Playground renders its own split layout (definition, preview, input,
    // output) into the container. `data` seeds the Form Input panel.
    current = new Playground({ container, schema, data: {} });
  } catch (e) {
    container.innerHTML = `<p class="muted err" style="padding:20px">Could not open this form: ${esc(e.message)}</p>`;
    return;
  }

  async function save() {
    const btn = root.querySelector("#form-save");
    btn.disabled = true;
    statusEl.textContent = "Saving…";
    try {
      const body = {
        id,
        name: nameInput.value.trim() || id,
        schema: current.getSchema(),
        projectId: project,
      };
      await api("POST", "/api/v1/forms", body);
      statusEl.textContent = "Saved";
      toast("Form saved");
      // A freshly created form becomes editable in place (so a second Save
      // overwrites rather than creating a duplicate) without a reload.
      if (isNew && location.hash.startsWith("#/modeler/form/new")) {
        history.replaceState(null, "", "#/modeler/form/e/" + encodeURIComponent(id));
      }
    } catch (e) {
      statusEl.textContent = "";
      toast("Save failed: " + e.message, "err");
    } finally {
      btn.disabled = false;
    }
  }
  root.querySelector("#form-save").addEventListener("click", save);
  nameInput.addEventListener("input", () => { statusEl.textContent = ""; });
}
