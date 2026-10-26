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
import { t } from "./i18n.js";

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// The scalar types a declared start variable can take, matching what the server
// accepts (parseStartVariables), what the compiler reads (compiler.StartVariable) and
// what the typed fields below coerce to.
export const START_VAR_TYPES = ["string", "number", "boolean", "json"];

// constantNameOf is a constant's name as it may be written: in capitals — letters A–Z,
// digits and underscores — so a reader tells a constant from a variable at a glance
// (ADR-0454, amended). The fields where a constant is named apply it while typing: umlauts
// spell out (Ä → AE), a space, dot or hyphen becomes an underscore, and anything else is
// dropped. The server applies the same rule and refuses what still breaks it (a leading
// digit, a reserved name).
export function constantNameOf(raw) {
  return String(raw || "").toUpperCase()
    .replace(/Ä/g, "AE").replace(/Ö/g, "OE").replace(/Ü/g, "UE")
    .replace(/[\s.\-]+/g, "_")
    .replace(/[^A-Z0-9_]/g, "");
}

// keepConstantNameInCapitals applies constantNameOf while a name is typed, to every field
// under container that matches selector. The caret goes after what was just typed. It is
// worked out from the value before the edit, not from the selection the browser reports
// during it: a letter from an input method (an umlaut on many keyboards) is inserted, and
// the caret placed, on the method's own schedule. A caret placed from that report once put
// the next letter at the start of the name. For the same reason the caret is placed again
// once the insertion has returned, unless the name has changed since.
export function keepConstantNameInCapitals(container, selector) {
  const before = new WeakMap(); // field → its value before the edit in progress
  const fix = (el, prev) => {
    const raw = el.value;
    const fixed = constantNameOf(raw);
    if (fixed === raw) return;
    // Where the edit ends in raw: past the longest common prefix, before the longest
    // common suffix the two values still share.
    let end = el.selectionStart ?? raw.length;
    if (prev != null) {
      const max = Math.min(prev.length, raw.length);
      let p = 0;
      while (p < max && prev[p] === raw[p]) p++;
      let s = 0;
      while (s < max - p && prev[prev.length - 1 - s] === raw[raw.length - 1 - s]) s++;
      end = raw.length - s;
    }
    const at = Math.min(fixed.length, constantNameOf(raw.slice(0, end)).length);
    const place = () => { try { el.setSelectionRange(at, at); } catch { /* not focused */ } };
    el.value = fixed;
    place();
    setTimeout(() => {
      if (el.value === fixed && document.activeElement === el &&
        el.selectionStart === el.selectionEnd && el.selectionStart !== at) place();
    }, 0);
  };
  container.addEventListener("beforeinput", (e) => {
    const el = e.target.closest?.(selector);
    if (el) before.set(el, el.value);
  });
  container.addEventListener("input", (e) => {
    const el = e.target.closest?.(selector);
    // A composition still in progress is the input method's to change; it is applied
    // when the composition ends.
    if (el && !e.isComposing) fix(el, before.get(el));
  });
  container.addEventListener("compositionend", (e) => {
    const el = e.target.closest?.(selector);
    if (el) fix(el, null);
  });
}

// NAMESPACE_INFO are the facts a namespace carries besides its constants, in lower case
// so they never share a name with one (ADR-0456). The compiler's
// namespaceInfo is the same list.
export const NAMESPACE_INFO = {
  application: ["id", "key", "name"],
  server: ["id", "name", "environment"],
};

export function isNamespaceInfo(namespace, name) {
  return (NAMESPACE_INFO[namespace] || []).includes(name);
}

// typedDeployFieldHTML renders one typed input for a declared start variable,
// prefilled with its default. A boolean is a true/false/— select; a number an
// input[type=number]; a string a text input; a json a textarea upgraded to a
// JSON editor after insertion. Required fields carry data-required so
// readTypedDeployBody can enforce them.
export function typedDeployFieldHTML(v) {
  const req = v.required ? ` <span class="req-star" title="${esc(t("startvars.required"))}">*</span>` : "";
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
// (ADR-0455): an emptied field means "not supplied". Throws Error(message) on a bad number, invalid JSON,
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
      if (!Number.isFinite(n)) throw new Error(t("startvars.notNumber", { name }));
      vars[name] = n;
    } else if (type === "boolean") {
      vars[name] = raw === "true";
    } else if (type === "json") {
      try {
        vars[name] = JSON.parse(raw);
      } catch (e) {
        throw new Error(t("startvars.badJSON", { name, error: e.message }));
      }
    } else {
      vars[name] = raw;
    }
  });
  if (missing.length) throw new Error(t("startvars.missing", { names: missing.join(", ") }));
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
    <p class="muted sf-extra-note">${esc(t("startvars.extra")).replace("{star}", '<span class="req-star">*</span>')}</p>
    ${extras.map(typedDeployFieldHTML).join("")}
  </div>`;
}
