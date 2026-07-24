// Form editor view. Embeds the vendored form-js editor (ADR-0028): the palette,
// drag-and-drop canvas, preview, and properties panel come from
// @bpmn-io/form-js-editor; the toolbar and Save wiring are ours. Assets load
// lazily so non-editor pages stay light, mirroring the BPMN editor (editor.js).

const FORM_CSS = "vendor/form-js/form-editor.css";

let editorReady; // memoized loader promise → { FormEditor }
function loadFormEditor() {
  if (!editorReady) {
    if (!document.querySelector(`link[href="${FORM_CSS}"]`)) {
      const l = document.createElement("link");
      l.rel = "stylesheet";
      l.href = FORM_CSS;
      document.head.appendChild(l);
    }
    editorReady = import("./vendor/form-js/form-editor.js");
  }
  return editorReady;
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

let current; // active form-js editor instance, destroyed on remount/leave

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
        <div id="form-canvas" class="form-editor-canvas"><p class="muted" style="padding:20px">Loading form editor&hellip;</p></div>
      </div>
    </div>`;

  const nameInput = root.querySelector("#form-name");
  const idChip = root.querySelector("#form-id-chip");
  const statusEl = root.querySelector("#form-status");
  const canvas = root.querySelector("#form-canvas");

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
      canvas.innerHTML = `<p class="muted err" style="padding:20px">Failed to load form: ${esc(e.message)}</p>`;
      return;
    }
  } else {
    id = newFormId();
  }
  nameInput.value = name;
  idChip.textContent = id;

  let FormEditor;
  try {
    ({ FormEditor } = await loadFormEditor());
  } catch (e) {
    canvas.innerHTML = `<p class="muted err" style="padding:20px">Failed to load the form editor: ${esc(e.message)}</p>`;
    return;
  }

  canvas.innerHTML = "";
  const editor = new FormEditor({ container: canvas });
  current = editor;
  try {
    await editor.importSchema(schema);
  } catch (e) {
    toast("Could not open this form: " + e.message, "err");
  }

  async function save() {
    const btn = root.querySelector("#form-save");
    btn.disabled = true;
    statusEl.textContent = "Saving…";
    try {
      const body = {
        id,
        name: nameInput.value.trim() || id,
        schema: editor.saveSchema(),
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
