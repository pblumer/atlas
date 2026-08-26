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

// loadFormViewer lazily imports the vendored form-js viewer and injects its stylesheet
// once, the first time anything renders a form — so a user who never opens one never
// pays for the 86 KB of CSS or the bundle.
export function loadFormViewer() {
  if (!_formViewer) {
    if (!document.getElementById("form-js-css")) {
      const link = document.createElement("link");
      link.id = "form-js-css";
      link.rel = "stylesheet";
      link.href = "vendor/form-js/form-js.css";
      document.head.appendChild(link);
    }
    _formViewer = import("./vendor/form-js/form-viewer.js");
  }
  return _formViewer;
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
      if (typeof c.key === "string") add(c.key);
      if (Array.isArray(c.components)) walk(c.components); // layout group: keys are in-scope
    }
  };
  if (schema && Array.isArray(schema.components)) walk(schema.components);
  return keys;
}
