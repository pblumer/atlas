// The form editor's half of a directory field (ADR-0473): a
// panel above form-js's own properties panel that turns the selected textfield into a
// search over a remote data source, and writes the field's atlasDataSource block.
//
// It is Atlas DOM rather than a form-js properties provider for the reason the field
// itself is: the vendored playground inlines its own Preact and exports none of it. What
// it does use of form-js is public — the editor's selection and modeling services and
// its event bus — so an edit made here is an ordinary command on the editor's stack:
// it undoes with Ctrl+Z and saves with the form.
//
// Nothing here is trusted by the server. The block is checked again when the form is
// saved, against the same provider registry this panel reads from
// GET /api/v1/form-data/sources, and refused with the reason if it does not hold.

import { DATA_SOURCE_MEMBER, directoryFields } from "./remote-select.js";

// The defaults a new directory field starts with: the source's own (the shipped
// examples'), searched where a person would look for a name.
export function defaultDataSource(provider, worker) {
  return {
    version: 1,
    workerType: provider.workerType,
    worker: worker || "",
    resource: provider.resource,
    searchFields: (provider.defaultSearch || ["displayName", "mail", "userPrincipalName"]).filter((p) => provider.searchable.includes(p)),
    filters: [],
    option: { label: provider.defaultLabel, description: provider.defaultDescription, details: [] },
    pageSize: 25,
    minSearchLength: 2,
  };
}

// openWorker names the first Worker of a source's type that lets forms search it, or ""
// when none does yet.
export function openWorker(provider, workers) {
  const w = (workers || []).find((x) => x.workerType === provider.workerType && x.formLookups);
  return w ? w.name : "";
}

// preferredProvider is the source a new directory field starts on: the first one this
// server offers that a Worker already lets forms search, else simply the first. A server
// with only a Google address book open to forms starts a field there, not on Entra.
export function preferredProvider(providers, workers) {
  return (providers || []).find((p) => openWorker(p, workers)) || (providers || [])[0] || null;
}

// keyedFields lists the fields a filter may be bound to: every keyed field of the form
// other than the directory field itself.
export function keyedFields(schema, exceptKey) {
  const out = [];
  const walk = (comps) => {
    for (const c of comps || []) {
      if (!c || typeof c !== "object") continue;
      if (typeof c.key === "string" && c.key && c.key !== exceptKey) out.push({ key: c.key, label: c.label || c.key, type: c.type });
      if (Array.isArray(c.components)) walk(c.components);
    }
  };
  walk(schema && schema.components);
  return out;
}

// checkDataSource says, in the panel, what the server would refuse at save. It is the
// same rules — a convenience, not a gate.
export function checkDataSource(block, provider, keys) {
  const problems = [];
  if (!block) return problems;
  if (!provider) return ["This server offers no remote data source " + block.workerType + "/" + block.resource + "."];
  if (!block.worker || !String(block.worker).trim()) problems.push("Choose the Worker whose credential answers the search.");
  if (block.activeOnly !== undefined && !provider.activeFilter) problems.push(`${provider.label} has no disabled entries to leave out; remove activeOnly.`);
  if (String(block.worker || "").trim().startsWith("=")) problems.push("The Worker must be a plain name, not a FEEL expression.");
  for (const p of block.searchFields || []) {
    if (!provider.searchable.includes(p)) problems.push(`“${p}” cannot be searched.`);
  }
  const seen = new Set();
  for (const f of block.filters || []) {
    if (!provider.filterable.includes(f.property)) problems.push(`“${f.property}” cannot be filtered on.`);
    if (seen.has(f.property)) problems.push(`“${f.property}” is filtered twice.`);
    seen.add(f.property);
    if (!f.field === !f.value) problems.push(`The filter on “${f.property}” needs a field or a fixed value.`);
    if (f.field && !keys.includes(f.field)) problems.push(`The filter on “${f.property}” is bound to “${f.field}”, which is not a field of this form.`);
  }
  const size = block.pageSize || 25;
  if (size < 1 || size > 50) problems.push("Results per page must be between 1 and 50.");
  const min = block.minSearchLength === undefined ? 2 : block.minSearchLength;
  if (min < 0 || min > 10) problems.push("The minimum search length must be between 0 and 10.");
  if ((block.option && block.option.details || []).length > 4) problems.push("An option shows at most four details.");
  return problems;
}

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// attachDataSourcePanel follows the editor's selection and, for a textfield, shows the
// panel that makes it a directory field. sources is the answer of
// GET /api/v1/form-data/sources. It returns a handle whose destroy() removes the panel.
export function attachDataSourcePanel(container, editor, sources) {
  const panel = document.createElement("section");
  panel.className = "atlas-ds-panel";
  panel.setAttribute("aria-label", "Remote data source");
  const selection = editor.get("selection");
  const modeling = editor.get("modeling");
  const registry = editor.get("formFieldRegistry");
  const eventBus = editor.get("eventBus");
  const providers = (sources && sources.providers) || [];
  const workers = (sources && sources.workers) || [];
  let field = null;

  const place = () => {
    const props = container.querySelector(".fjs-pgl-properties-container");
    if (props && panel.parentElement !== props) props.prepend(panel);
    else if (props && props.firstElementChild !== panel) props.prepend(panel);
  };

  const schemaNow = () => {
    try { return editor.getSchema(); } catch { return { components: [] }; }
  };

  const write = (block) => {
    if (!field) return;
    modeling.editFormField(field, DATA_SOURCE_MEMBER, block ? JSON.parse(JSON.stringify(block)) : undefined);
  };

  function render() {
    place();
    const sel = selection.get();
    field = sel && sel.type === "textfield" ? registry.get(sel.id) || sel : null;
    if (!field) { panel.hidden = true; panel.innerHTML = ""; return; }
    panel.hidden = false;
    const block = field[DATA_SOURCE_MEMBER] && typeof field[DATA_SOURCE_MEMBER] === "object" ? field[DATA_SOURCE_MEMBER] : null;
    const provider = block ? providers.find((p) => p.workerType === block.workerType && p.resource === block.resource) : preferredProvider(providers, workers);
    const keys = keyedFields(schemaNow(), field.key);
    if (!block) {
      panel.innerHTML = `
        <header class="atlas-ds-head">Remote data source</header>
        <p class="atlas-ds-hint">This text field takes what a person types. It can instead search a directory and submit the id of the entry the person chooses.</p>
        ${providers.length ? `<button type="button" class="btn small" data-act="enable">Search a remote data source</button>`
          : `<p class="atlas-ds-hint">This server offers no remote data source.</p>`}`;
      const on = panel.querySelector("[data-act=enable]");
      if (on) on.addEventListener("click", () => {
        write(defaultDataSource(provider, openWorker(provider, workers)));
      });
      return;
    }
    const ofType = workers.filter((w) => provider && w.workerType === provider.workerType);
    const chosen = ofType.find((w) => w.name === block.worker);
    const problems = checkDataSource(block, provider, keys.map((k) => k.key));
    const opt = block.option || {};
    const check = (name, value, list, label) =>
      `<label class="atlas-ds-check"><input type="checkbox" data-list="${name}" value="${esc(value)}" ${list.includes(value) ? "checked" : ""}/> ${esc(label || value)}</label>`;
    const selectOf = (name, values, current, empty) =>
      `<select data-field="${name}">${empty ? `<option value="">${esc(empty)}</option>` : ""}${values.map((v) =>
        `<option value="${esc(v.value)}" ${v.value === current ? "selected" : ""}>${esc(v.label)}</option>`).join("")}</select>`;
    panel.innerHTML = `
      <header class="atlas-ds-head">Remote data source <span class="chip">${esc(provider ? provider.label : block.workerType + "/" + block.resource)}</span></header>
      <label class="atlas-ds-row"><span>Source</span>${selectOf("source", providers.map((p) => ({ value: p.workerType + "/" + p.resource, label: p.label })), block.workerType + "/" + block.resource)}</label>
      <label class="atlas-ds-row"><span>Worker</span>${selectOf("worker", [
        ...ofType.map((w) => ({ value: w.name, label: w.name + (w.formLookups ? "" : " (not open to forms)") + (w.enabled ? "" : " (switched off)") })),
        ...(block.worker && !chosen ? [{ value: block.worker, label: block.worker + " (not configured)" }] : []),
      ], block.worker, "Choose a Worker…")}</label>
      ${chosen && !chosen.formLookups ? `<p class="atlas-ds-warn">The Worker ${esc(chosen.name)} does not let forms search through it yet. Its owner can allow it in the Console (Workers ▸ ${esc(chosen.name)} ▸ Forms may search). Until then the search answers “forbidden”.</p>` : ""}
      <fieldset class="atlas-ds-group"><legend>Search in</legend>
        ${(provider ? provider.searchable : []).map((p) => check("searchFields", p, block.searchFields || [])).join("")}
      </fieldset>
      <fieldset class="atlas-ds-group"><legend>Filters</legend>
        ${(block.filters || []).map((f, i) => `
          <div class="atlas-ds-filter" data-index="${i}">
            ${selectOf("f-property", (provider ? provider.filterable : []).map((p) => ({ value: p, label: p })), f.property)}
            <span>=</span>
            ${selectOf("f-mode", [{ value: "field", label: "the field" }, { value: "value", label: "the value" }], fixed(f) ? "value" : "field")}
            ${fixed(f)
              ? `<input type="text" data-field="f-value" value="${esc(f.value || "")}" aria-label="Fixed value" placeholder="value"/>`
              : selectOf("f-field", keys.map((k) => ({ value: k.key, label: k.label + " (" + k.key + ")" })), f.field, "Choose a field…")}
            <label class="atlas-ds-check" title="No search runs until this field has a value"><input type="checkbox" data-field="f-required" ${f.required ? "checked" : ""} ${fixed(f) ? "disabled" : ""}/> required</label>
            <button type="button" class="btn ghost small" data-act="f-remove" aria-label="Remove filter">✕</button>
          </div>`).join("")}
        <button type="button" class="btn ghost small" data-act="f-add">＋ Filter</button>
      </fieldset>
      <fieldset class="atlas-ds-group"><legend>Each result shows</legend>
        <label class="atlas-ds-row"><span>Label</span>${selectOf("o-label", (provider ? provider.readable : []).map((p) => ({ value: p, label: p })), opt.label || (provider && provider.defaultLabel))}</label>
        <label class="atlas-ds-row"><span>Below it</span>${selectOf("o-description", (provider ? provider.readable : []).map((p) => ({ value: p, label: p })), opt.description || (provider && provider.defaultDescription))}</label>
        <div class="atlas-ds-row"><span>Details</span><div>${(provider ? provider.readable : []).filter((p) => p !== (provider && provider.valueProperty)).map((p) => check("details", p, opt.details || [])).join("")}</div></div>
        <p class="atlas-ds-hint">The form submits the entry's ${esc(provider ? provider.valueProperty : "id")}, never its label.</p>
      </fieldset>
      <div class="atlas-ds-row"><span>Results per page</span><input type="number" min="1" max="50" data-field="pageSize" value="${esc(block.pageSize || 25)}"/></div>
      <div class="atlas-ds-row"><span>Search from</span><input type="number" min="0" max="10" data-field="minSearchLength" value="${esc(block.minSearchLength === undefined ? 2 : block.minSearchLength)}"/> characters</div>
      ${provider && provider.activeFilter ? `<label class="atlas-ds-check"><input type="checkbox" data-field="activeOnly" ${block.activeOnly === false ? "" : "checked"}/> Leave out disabled accounts</label>` : ""}
      <label class="atlas-ds-check"><input type="checkbox" data-field="verify" ${block.verify === "format" ? "" : "checked"}/> Check the choice at the source again when the task is completed</label>
      ${problems.length ? `<ul class="atlas-ds-problems">${problems.map((p) => `<li>${esc(p)}</li>`).join("")}</ul>` : ""}
      <p class="atlas-ds-hint">The Validate tab previews the field with made-up entries; no directory or address book is asked while you design.</p>
      <button type="button" class="btn ghost small" data-act="disable">Back to a plain text field</button>`;
    wire(block, provider);
  }

  function wire(block, provider) {
    const next = () => JSON.parse(JSON.stringify(block));
    const on = (sel, ev, fn) => panel.querySelectorAll(sel).forEach((el) => el.addEventListener(ev, fn));
    on("[data-act=disable]", "click", () => write(undefined));
    on("[data-field=source]", "change", (e) => {
      const [workerType, resource] = e.target.value.split("/");
      const p = providers.find((x) => x.workerType === workerType && x.resource === resource);
      if (p) write(defaultDataSource(p, openWorker(p, workers)));
    });
    on("[data-field=worker]", "change", (e) => { const b = next(); b.worker = e.target.value; write(b); });
    on("[data-list=searchFields]", "change", () => {
      const b = next();
      b.searchFields = [...panel.querySelectorAll("[data-list=searchFields]:checked")].map((x) => x.value);
      write(b);
    });
    on("[data-list=details]", "change", () => {
      const b = next();
      b.option = { ...(b.option || {}), details: [...panel.querySelectorAll("[data-list=details]:checked")].map((x) => x.value) };
      write(b);
    });
    on("[data-field=o-label]", "change", (e) => { const b = next(); b.option = { ...(b.option || {}), label: e.target.value }; write(b); });
    on("[data-field=o-description]", "change", (e) => { const b = next(); b.option = { ...(b.option || {}), description: e.target.value }; write(b); });
    on("[data-field=pageSize]", "change", (e) => { const b = next(); b.pageSize = Number(e.target.value) || 25; write(b); });
    on("[data-field=minSearchLength]", "change", (e) => { const b = next(); b.minSearchLength = Math.max(0, Number(e.target.value) || 0); write(b); });
    on("[data-field=activeOnly]", "change", (e) => { const b = next(); if (e.target.checked) delete b.activeOnly; else b.activeOnly = false; write(b); });
    on("[data-field=verify]", "change", (e) => { const b = next(); if (e.target.checked) delete b.verify; else b.verify = "format"; write(b); });
    on("[data-act=f-add]", "click", () => {
      const b = next();
      const used = new Set((b.filters || []).map((f) => f.property));
      const free = (provider ? provider.filterable : []).find((p) => !used.has(p));
      if (!free) return;
      b.filters = [...(b.filters || []), { property: free, field: "" }];
      write(b);
    });
    panel.querySelectorAll(".atlas-ds-filter").forEach((row) => {
      const i = Number(row.dataset.index);
      const edit = (fn) => { const b = next(); fn(b.filters[i]); b.filters[i] = clean(b.filters[i]); write(b); };
      row.querySelector("[data-act=f-remove]").addEventListener("click", () => { const b = next(); b.filters.splice(i, 1); write(b); });
      row.querySelector("[data-field=f-property]").addEventListener("change", (e) => edit((f) => { f.property = e.target.value; }));
      row.querySelector("[data-field=f-mode]").addEventListener("change", (e) => edit((f) => {
        if (e.target.value === "value") { delete f.field; delete f.required; f.value = f.value || ""; }
        else { delete f.value; f.field = ""; }
      }));
      const fieldSel = row.querySelector("[data-field=f-field]");
      if (fieldSel) fieldSel.addEventListener("change", (e) => edit((f) => { f.field = e.target.value; }));
      const valueIn = row.querySelector("[data-field=f-value]");
      if (valueIn) valueIn.addEventListener("change", (e) => edit((f) => { f.value = e.target.value; }));
      row.querySelector("[data-field=f-required]").addEventListener("change", (e) => edit((f) => {
        if (e.target.checked) f.required = true; else delete f.required;
      }));
    });
  }

  // fixed reports whether a filter is a fixed value rather than bound to a field.
  const fixed = (f) => Object.prototype.hasOwnProperty.call(f, "value") && !Object.prototype.hasOwnProperty.call(f, "field");

  const clean = (f) => {
    const out = { property: f.property };
    if (f.value !== undefined && f.field === undefined) out.value = f.value;
    else out.field = f.field || "";
    if (f.required && out.field !== undefined) out.required = true;
    return out;
  };

  const onSelection = () => render();
  const onChanged = () => { if (field) render(); };
  eventBus.on("selection.changed", onSelection);
  eventBus.on("changed", onChanged);
  // form-js re-renders the properties column; keep the panel at its top.
  const observer = new MutationObserver(() => place());
  observer.observe(container, { childList: true, subtree: true });
  render();
  return {
    destroy() {
      observer.disconnect();
      try { eventBus.off("selection.changed", onSelection); eventBus.off("changed", onChanged); } catch { /* editor gone */ }
      panel.remove();
    },
  };
}

// addDirectoryField appends a new directory field to the form and selects it.
export function addDirectoryField(editor, sources) {
  const workers = (sources && sources.workers) || [];
  const p = preferredProvider((sources && sources.providers) || [], workers);
  if (!p) return null;
  const registry = editor.get("formFieldRegistry");
  const root = registry.getForm();
  const taken = new Set(registry.getAll().map((f) => f.key).filter(Boolean));
  let key = "person";
  for (let i = 2; taken.has(key); i++) key = "person" + i;
  const created = editor.get("modeling").addFormField(
    { type: "textfield", key, label: "Person", validate: { required: true }, [DATA_SOURCE_MEMBER]: defaultDataSource(p, openWorker(p, workers)) },
    root, (root.components || []).length);
  try { editor.get("selection").set(created); } catch { /* selection is a convenience */ }
  return created;
}

export { directoryFields };
