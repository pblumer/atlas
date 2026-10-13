// The typed start-variable fields, shared by every surface that starts an instance.
//
// A process says what it starts with in two places: the start form linked on its start
// event (ADR-0028), and the start variables declared on the process (atlas:startForm).
// Each start surface used to honour one of them. A surface that opened the form dropped
// the declaration without a word, so a declared value the form never asked for was
// missing from every instance started that way — and a script task reading it got
// null, with nothing on any screen saying why. The surfaces now show the form *and*,
// beneath it, the declared variables the form does not bind; the declaration's typed
// fields therefore have to be reachable from the Tasks app as well as the Modeler, and
// they live here rather than in editor.js, which the Tasks app does not load.

import { attachJSONEditor } from "./json-editor.js";

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// The scalar types a declared start variable can take, matching what the server
// accepts (parseStartVariables), what the compiler reads (compiler.StartVariable) and
// what the typed fields below coerce to.
export const START_VAR_TYPES = ["string", "number", "boolean", "json"];

// typedDeployFieldHTML renders one typed input for a declared start variable,
// prefilled with its default. A boolean is a true/false/— select; a number an
// input[type=number]; a string a text input; a json a textarea upgraded to a
// JSON editor after insertion. Required fields carry data-required so
// readTypedDeployBody can enforce them.
export function typedDeployFieldHTML(v) {
  const req = v.required ? ' <span class="req-star" title="required">*</span>' : "";
  const dr = v.required ? ' data-required="1"' : "";
  const def = v.default == null ? "" : String(v.default);
  const cap = `<span>${esc(v.name)} <span class="muted">(${esc(v.type)})</span>${req}</span>`;
  let input;
  if (v.type === "boolean") {
    input = `<select class="dv-field" data-name="${esc(v.name)}" data-type="boolean"${dr}>
      <option value=""${def === "" ? " selected" : ""}>—</option>
      <option value="true"${def === "true" ? " selected" : ""}>true</option>
      <option value="false"${def === "false" ? " selected" : ""}>false</option>
    </select>`;
  } else if (v.type === "number") {
    input = `<input type="number" step="any" class="dv-field" data-name="${esc(v.name)}" data-type="number"${dr} value="${esc(def)}" placeholder="0"/>`;
  } else if (v.type === "json") {
    // A textarea, upgraded to a JSON editor by wireStartJSONEditors() after
    // the panel is inserted into the DOM.
    input = `<textarea class="dv-field dv-json" data-name="${esc(v.name)}" data-type="json"${dr} rows="3" spellcheck="false" placeholder='{ "key": "value" }'>${esc(def)}</textarea>`;
  } else {
    input = `<input type="text" class="dv-field" data-name="${esc(v.name)}" data-type="string"${dr} value="${esc(def)}"/>`;
  }
  return `<label class="field">${cap}${input}</label>`;
}

// readTypedDeployBody turns typed start-variable fields into an instance request
// body, coercing each field to its declared type and enforcing required fields. Empty
// optional fields are omitted, and the engine then seeds the declared default
// (ADR-draft-start-variable-defaults): an emptied field means "not supplied". Throws Error(message) on a bad number, invalid JSON,
// or a missing required value.
export function readTypedDeployBody(bodyEl) {
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

// wireStartJSONEditors upgrades every json-typed field (.dv-json) and the free-form
// start-variables textarea (.sv-json) in the container to the JSON editor. Called after
// the fields are inserted, so the DOM elements exist.
export function wireStartJSONEditors(container) {
  for (const ta of container.querySelectorAll(".dv-json, .sv-json")) {
    attachJSONEditor(ta, { rows: 3 });
  }
}

// formSubmittedNames is the set of top-level variable names a form-js schema submits:
// each keyed field's name up to its first dot (form-js writes `address.street` into
// `address`), and the path of a group or dynamic list, which binds everything inside
// it. A disabled field is shown but never submitted (the vendored viewer leaves it out
// of the submit data, as api/publicstartfields.go records), so it binds nothing.
export function formSubmittedNames(schema) {
  const names = new Set();
  const top = (s) => String(s).trim().split(".")[0].trim();
  const walk = (comps) => {
    for (const c of comps || []) {
      if (!c || typeof c !== "object") continue;
      if (typeof c.path === "string" && c.path.trim()) { names.add(top(c.path)); continue; }
      if (typeof c.key === "string" && c.key.trim() && !c.disabled) names.add(top(c.key));
      if (Array.isArray(c.components)) walk(c.components);
    }
  };
  if (schema && Array.isArray(schema.components)) walk(schema.components);
  return names;
}

// uncoveredStartVariables is the part of a process's declaration its start form does
// not ask for: the declared variables (`[{name, type, default, required}]`) whose name
// the form does not submit. Those are the values that would otherwise be missing from
// an instance started through the form.
export function uncoveredStartVariables(declared, schema) {
  const bound = formSubmittedNames(schema);
  return (declared || []).filter((v) => v && v.name && !bound.has(v.name));
}

// extraStartFieldsHTML renders the uncovered declared variables beneath a start form,
// "" when there are none — the common case, which must look exactly as it did.
export function extraStartFieldsHTML(extras) {
  if (!extras || !extras.length) return "";
  return `<div class="sf-extra">
    <p class="muted sf-extra-note">Also declared as start variables on the process, and not asked by the form. Required are marked <span class="req-star">*</span>; leave an optional one blank to omit it.</p>
    ${extras.map(typedDeployFieldHTML).join("")}
  </div>`;
}
