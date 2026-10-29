// The vendored form-js viewer, loaded once for whoever needs it (ADR-0013's vendoring
// pattern).
//
// It lives in its own module because two unrelated surfaces render forms now: the Tasks
// app shows a user task's *work* form, and an incident shows a task's *repair* form
// (ADR-0169). Both want the same lazy import and the same one-time stylesheet injection,
// and app.js cannot be the home of it — app.js imports the incident module, so the
// incident module importing app.js back would be a cycle.

// _formViewer caches the import promise so repeated opens reuse the one module instance.
let _formViewer = null;

// FORM_LOAD_TIMEOUT_MS bounds how long anything waits for a form to arrive. Every
// surface that renders one puts up "Loading form…" first and replaces it once the
// viewer and the definition are both here — so a fetch that neither answers nor fails
// leaves that placeholder standing for good, with no error, no way to retry, and a
// disabled Send button next to it. A stall is a failure the person can act on, so it is
// reported as one. The bundle is 476 KB: this has to be long enough for a slow link to
// finish honestly, and short enough that nobody sits watching a placeholder.
export const FORM_LOAD_TIMEOUT_MS = 20000;

// withLoadDeadline rejects if `p` has not settled within the deadline, naming what did
// not arrive so the message says which half stalled. It does not cancel the underlying
// work — a module import cannot be cancelled — which is exactly why a retry is cheap:
// whatever did arrive in the meantime is in the browser's cache.
//
// `what` may instead be a function of the seconds that words the whole sentence: the
// console's editors say it in their catalogue's language (ADR-0267). This module does
// not read the catalogue itself, because the shop loads it too.
export function withLoadDeadline(p, what, ms = FORM_LOAD_TIMEOUT_MS) {
  return new Promise((resolve, reject) => {
    const seconds = Math.round(ms / 1000);
    const timer = setTimeout(
      () => reject(new Error(typeof what === "function" ? what(seconds) : `${what} did not arrive within ${seconds} seconds`)),
      ms);
    p.then(
      (v) => { clearTimeout(timer); resolve(v); },
      (e) => { clearTimeout(timer); reject(e); });
  });
}

// ensureFormStyles injects the two stylesheets a rendered form needs, once each and
// in this order: the vendored form-js stylesheet, then the Atlas bridge that maps
// the brand palette onto the tokens form-js reads (form-theme.css). Order is not
// decorative — the bridge's `:root .fjs-container` block deliberately outranks
// form-js's own `.fjs-container` declarations, and appending it second keeps it
// ahead on document order too, so neither half depends on the other's specificity
// alone.
//
// It is exported because the form editor loads the viewer through its own lazy
// path (form-editor.js) and must not end up with the renderer but not the theme —
// a preview in stock bpmn.io blue beside a console in the org's colour is exactly
// the drift this file exists to prevent.
export function ensureFormStyles() {
  ensureCss("form-js-css", "vendor/form-js/form-js.css");
  ensureCss("form-theme-css", "form-theme.css");
}

function ensureCss(id, href) {
  if (document.getElementById(id)) return;
  const link = document.createElement("link");
  link.id = id;
  link.rel = "stylesheet";
  link.href = href;
  document.head.appendChild(link);
}

// loadFormViewer lazily imports the vendored form-js viewer and injects its stylesheets
// once, the first time anything renders a form — so a user who never opens one never
// pays for the 86 KB of CSS or the bundle.
//
// Its Form asks the server for the options of a field whose options come from a
// decision (decisionOptions, below), unless the caller passes atlasDecisions itself.
export function loadFormViewer() {
  if (!_formViewer) {
    ensureFormStyles();
    // The memo is here to load the bundle once, not to make one bad fetch permanent: a
    // remembered failure would fail every later form in the tab, leaving a page reload
    // as the only way back. So a load that fails — or that runs out its deadline — is
    // forgotten, and the next open imports again.
    _formViewer = withLoadDeadline(
      import("./vendor/form-js/form-viewer.js").then((mod) => ({ ...mod, Form: askingDecisions(mod.Form) })),
      "The form viewer")
      .catch((e) => { _formViewer = null; throw e; });
  }
  return _formViewer;
}

// askingDecisions gives every form a decisionOptions service of its own, so each form
// that is opened shows the list its decisions answer then.
function askingDecisions(Form) {
  return class extends Form {
    constructor(options = {}) {
      super({ atlasDecisions: decisionOptions(), ...options });
    }
  };
}

// decisionOptions is what a form asks for the options of a field that takes them from a
// deployed decision (ADR-draft-form-options-from-a-decision): GET
// /api/v1/form-data/decisions/{id}/options, which evaluates the newest deployment of
// the decision on the server. Every field naming the same decision shares one request.
//
// answer(id) resolves to the whole reply — {decisionId, options, skipped} — for the
// form editor's panel; options(id) to the options alone, for the field. A refusal
// rejects with the server's reason, and is not remembered, so the next ask tries again.
// With ttlMs, an answer is reused for that long and then asked afresh, which is what an
// editor wants of a list somebody may be publishing a new version of meanwhile.
export function decisionOptions({ ttlMs = 0 } = {}) {
  const asked = new Map();
  const answer = (id) => {
    let pending = asked.get(id);
    if (!pending) {
      pending = fetch(`/api/v1/form-data/decisions/${encodeURIComponent(id)}/options`, { credentials: "same-origin" })
        .then(async (res) => {
          const body = await res.json().catch(() => null);
          if (!res.ok) throw new Error((body && body.error) || `HTTP ${res.status}`);
          return body || {};
        });
      asked.set(id, pending);
      pending.catch(() => { if (asked.get(id) === pending) asked.delete(id); });
      if (ttlMs > 0) setTimeout(() => { if (asked.get(id) === pending) asked.delete(id); }, ttlMs);
    }
    return pending;
  };
  return {
    answer,
    options: (id) => answer(id).then((body) => (Array.isArray(body.options) ? body.options : [])),
  };
}

// ---------- Answering a form that refused to submit --------------------------
//
// form-js validates on submit and marks the fields it rejected, and every surface that
// renders a form used to answer that with "Please fix the highlighted fields". That
// sentence assumes the person can see the highlighting. In the Tasks app they could
// not: the form stays mounted while the Process tab is showing, so the marked fields
// were on a pane nobody was looking at. Even where they can, "highlighted" sends them
// hunting through a form for a colour instead of telling them what is missing.
//
// The two halves that every surface needs are here; the sentence itself is not. The
// console is German-first where it has been translated and English everywhere else
// (ADR-0267), so which words go around these names is the caller's to decide — the
// Modeler putting a German sentence in an English dialog is exactly the half-translated
// screen that ADR warns about.

// FORM_REFUSAL_NAMES is how many fields a refusal spells out before it starts counting.
// A blank form refuses everything it has, and a message listing fifteen field names
// names none of them — the first few plus "and 11 more" is what somebody can read at a
// glance and act on.
export const FORM_REFUSAL_NAMES = 4;

// formRefusalNames turns what a form refused into the fields to send somebody back to,
// called what the form calls them: `{shown, rest}`, where shown is at most `max` names
// and rest is how many more there were.
//
// form-js keys its errors by *field id* — the opaque `Field_1a2b3c` the form editor
// generates — so the ids are resolved through the form's own field registry, which is
// the table it keyed them with in the first place. A field with no label falls back to
// its variable key, and one the registry does not know falls back to the id: worse to
// read, still better than "some field somewhere". Two fields may share a label, so the
// names are deduplicated before they are cut.
export function formRefusalNames(form, errors, max = FORM_REFUSAL_NAMES) {
  let reg = null;
  try { reg = form.get("formFieldRegistry"); } catch { /* no registry to ask */ }
  const names = [...new Set(Object.keys(errors || {}).map((id) => {
    const f = reg && reg.get(id);
    const label = f && typeof f.label === "string" ? f.label.trim() : "";
    return label || (f && f.key) || id;
  }))];
  return { shown: names.slice(0, max), rest: Math.max(0, names.length - max) };
}

// scrollToFirstInvalidField brings the first field the form marked into view — forms
// scroll, and the thing to fix can be below the fold. `hostId` is the element the form
// was mounted into. After the current task, because the marks are drawn by the form's
// own re-render and are not in the document yet.
export function scrollToFirstInvalidField(hostId) {
  setTimeout(() => {
    const first = document.querySelector(`#${hostId} .fjs-has-errors, #${hostId} .fjs-form-field-error`);
    if (first && first.scrollIntoView) first.scrollIntoView({ block: "center" });
  }, 0);
}

// formFieldKeys extracts the variable-bearing field keys from a form-js schema — the
// keys a submit would write, and so exactly the set a repair form is allowed to touch.
//
// Every form-js input component carries a `key`: the variable it reads and writes.
// Layout-only components (text, image, spacer, separator, …) have none. A repeating
// container binds an array under its `path`, so that path is the variable rather than
// anything inside its per-row template. A plain group is layout only, so its fields
// belong to the enclosing scope and are collected by recursing. Each key appears once,
// in document order.
// FORM_FIELD_TYPES maps a form-js input component to the type of the variable it
// writes. The component type is the only type declaration a form carries, and it is a
// real one: a checkbox writes a boolean whatever it is labelled, a taglist writes an
// array. Components not listed here write a string — every text-shaped input does, and
// so does a select or a radio, whose value is one of its option keys.
// MAIL_VIEW_TYPE is the e-mail view (ADR-0489): it names a variable
// by its key to show it, and the form never submits it, so it is no variable the form
// writes.
const MAIL_VIEW_TYPE = "mailview";

const FORM_FIELD_TYPES = {
  number: "number",
  checkbox: "boolean",
  checklist: "array",
  taglist: "array",
  filepicker: "array",
};

// formFieldTypes maps each key formFieldKeys returns to that variable's type, for the
// Modeler's Variables panel: a form is the one design-time source that knows the type
// of what it writes before anything has run. Walks the same schema the same way, so
// the two agree on which keys exist; a repeating container binds an array under its
// path, whatever its per-row template contains.
export function formFieldTypes(schema) {
  const types = {};
  const walk = (comps) => {
    for (const c of comps || []) {
      if (!c || typeof c !== "object") continue;
      if (c.path) { if (!(c.path in types)) types[c.path] = "array"; continue; }
      if (c.type === MAIL_VIEW_TYPE) continue; // shows a variable, writes none
      if (typeof c.key === "string" && c.key.trim() && !(c.key.trim() in types)) {
        types[c.key.trim()] = FORM_FIELD_TYPES[c.type] || "string";
      }
      if (Array.isArray(c.components)) walk(c.components);
    }
  };
  if (schema && Array.isArray(schema.components)) walk(schema.components);
  return types;
}

export function formFieldKeys(schema) {
  const keys = [];
  const seen = new Set();
  const add = (k) => {
    k = (k || "").trim();
    if (k && !seen.has(k)) { seen.add(k); keys.push(k); }
  };
  const walk = (comps) => {
    for (const c of comps || []) {
      if (!c || typeof c !== "object") continue;
      if (c.path) { add(c.path); continue; } // repeatable scope: its path is the variable
      if (c.type === MAIL_VIEW_TYPE) continue; // shows a variable, writes none
      if (typeof c.key === "string") add(c.key);
      if (Array.isArray(c.components)) walk(c.components); // layout group: keys are in-scope
    }
  };
  if (schema && Array.isArray(schema.components)) walk(schema.components);
  return keys;
}
