// The Tasks app's folder editor (ADR-draft-task-folders-are-saved-filters).
//
// A folder is a saved filter, and this is where somebody builds one. The rule it
// produces is a structured document — a match mode and a list of field/operator/
// value rows — never a string the person types: the field and operator listboxes
// are drawn from the catalogue the server publishes, and the value control is
// whichever one that operator's value shape calls for, filled from what is
// actually deployed. A process id cannot be mistyped here, because it is never
// typed.
//
// The FEEL under the conditions is the server's, fetched with the preview, not a
// second generator written here. Two generators would agree on the day they were
// written and disagree on the day one was touched, and the one shown to the
// person would be the wrong one.
//
// It lives in its own module for the reason the worker and pick dialogs do: app.js
// boots the whole console on import, so anything left inside it can only be
// exercised by hand.

import { t, plural } from "./i18n.js";

const esc = (s) => String(s).replace(/[&<>"']/g, (c) =>
  ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// The value shapes an operator can ask for. They mirror the server's constants —
// it names the shape, this decides which control draws it.
const SHAPE = {
  none: "none", text: "text", choice: "choice", choices: "choices",
  number: "number", duration: "duration", count: "count",
};

// The offers for the two shapes whose values are not a list from the server. They
// are here rather than in the catalogue because they are interface choices — how
// many steps a priority picker should have — not facts about the engine.
const PRIORITY_PRESETS = [
  { value: "70", key: "tasks.folders.priority.high" },
  { value: "50", key: "tasks.folders.priority.normal" },
  { value: "30", key: "tasks.folders.priority.low" },
];
const WITHIN_PRESETS = ["PT8H", "P1D", "P3D", "P7D"];
const UNITS = ["h", "d"];

// catalogue is fetched once per page load: the fields and operators do not change
// while somebody has the console open, and the value lists change only when a
// model is deployed — for which a refresh is the honest signal anyway.
let catalogue = null;

// loadCatalogue returns the field/operator catalogue and the value lists.
export async function loadCatalogue(api) {
  if (!catalogue) catalogue = await api("GET", "/api/v1/task-folders/fields");
  return catalogue;
}

// forgetCatalogue drops the cache, so a deploy made in this session shows up in
// the listboxes without a reload.
export function forgetCatalogue() { catalogue = null; }

// loadFolders returns the folders this identity can see, in sidebar order.
export function loadFolders(api, me) {
  return api("GET", "/api/v1/task-folders" + (me ? "?me=" + encodeURIComponent(me) : ""));
}

// loadCounts returns the badge number for every visible folder, from one scan.
export function loadCounts(api, me) {
  return api("GET", "/api/v1/task-folders/counts" + (me ? "?me=" + encodeURIComponent(me) : ""));
}

// fieldById and opById walk the catalogue. It is small (nine fields), so a scan
// is cheaper than the index that would have to be kept in step with it.
const fieldById = (cat, id) => (cat.fields || []).find((f) => f.id === id);
const opById = (field, id) => ((field && field.ops) || []).find((o) => o.id === id);

// defaultCondition builds a complete, valid row for a field — the one the editor
// drops in when somebody picks that field. Every one of them saves as it stands,
// so a new row never puts the dialog into an invalid state somebody has to
// rescue it from.
function defaultCondition(cat, fieldId) {
  const field = fieldById(cat, fieldId);
  const op = field && field.ops[0];
  const c = { field: fieldId, op: op ? op.id : "is" };
  const list = optionsFor(cat, field);
  switch (op && op.value) {
    case SHAPE.choice: c.value = list.length ? list[0].value : ""; break;
    case SHAPE.choices: c.values = list.length ? [list[0].value] : []; break;
    case SHAPE.text: c.value = ""; break;
    case SHAPE.number: c.value = "50"; break;
    case SHAPE.duration: c.value = "P3D"; break;
    case SHAPE.count: c.value = "2"; c.unit = "d"; break;
    default: break;
  }
  return c;
}

// optionsFor is the value list a field's listbox is filled from, or an empty list
// for a field whose values are typed or numeric.
function optionsFor(cat, field) {
  if (!field || !field.options) return [];
  return (cat.options && cat.options[field.options]) || [];
}

// optionLabel is what a person reads in a value listbox. A process shows its
// model name and its id, because a person recognises the name and the id is what
// the rule will carry — showing only one of them makes one of those two moments
// guesswork.
function optionLabel(o) {
  if (o.label && o.label !== o.value) return o.label + " · " + o.value;
  return o.value;
}

// openFolderEditor opens the dialog on a folder, or on nothing for a new one.
// onSaved and onDeleted are how the sidebar learns to redraw itself.
export function openFolderEditor({ api, folder, me, onSaved, onDeleted, toast }) {
  const editing = !!folder;
  const draft = {
    name: editing ? folder.name : "",
    visibility: editing ? folder.visibility : "private",
    groupId: editing ? folder.groupId || "" : "",
    rule: editing
      ? JSON.parse(JSON.stringify(folder.rule || { match: "all", conditions: [] }))
      : { match: "all", conditions: [] },
  };

  const ov = document.createElement("div");
  ov.className = "modal-ov";
  ov.innerHTML = `
    <div class="modal tf-modal" role="dialog" aria-modal="true" aria-labelledby="tf-title">
      <div class="modal-head">
        <h2 id="tf-title">${esc(t(editing ? "tasks.folders.editTitle" : "tasks.folders.newTitle"))}</h2>
        <button class="icon-btn" id="tf-x" title="${esc(t("common.close"))}" aria-label="${esc(t("common.close"))}">
          <svg viewBox="0 0 24 24" width="18" height="18"><path d="M6 6l12 12M18 6L6 18" stroke="currentColor" stroke-width="2" fill="none"/></svg>
        </button>
      </div>
      <div class="modal-body tf-body">
        <div class="tf-top">
          <label class="tf-field">
            <span class="tf-label">${esc(t("tasks.folders.nameLabel"))}</span>
            <input class="tf-in" id="tf-name" type="text" spellcheck="false"
                   placeholder="${esc(t("tasks.folders.namePlaceholder"))}" value="${esc(draft.name)}" />
          </label>
          <label class="tf-field">
            <span class="tf-label">${esc(t("tasks.folders.visibility"))}</span>
            <select class="tf-sel" id="tf-vis"></select>
          </label>
        </div>

        <div class="tf-field">
          <span class="tf-label">${esc(t("tasks.folders.conditions"))}</span>
          <div class="tf-match">
            <span>${esc(t("tasks.folders.matchPrefix"))}</span>
            <select class="tf-sel" id="tf-match">
              <option value="all">${esc(t("tasks.folders.match.all"))}</option>
              <option value="any">${esc(t("tasks.folders.match.any"))}</option>
            </select>
            <span>${esc(t("tasks.folders.matchSuffix"))}</span>
          </div>
          <div class="tf-conds" id="tf-conds"></div>
          <div><button class="btn ghost small" id="tf-add">＋ ${esc(t("tasks.folders.addCondition"))}</button></div>
        </div>

        <div class="tf-preview">
          <div class="tf-preview-head">
            <span>${esc(t("tasks.folders.preview"))}</span>
            <b id="tf-count">${esc(t("tasks.folders.countPending"))}</b>
            <span id="tf-total"></span>
            <span class="tf-preview-tag">${esc(t("tasks.folders.generatedFeel"))}</span>
          </div>
          <pre class="tf-feel" id="tf-feel"></pre>
        </div>
      </div>
      <div class="modal-foot">
        <div class="modal-actions">
          ${editing ? `<button class="btn ghost danger small" id="tf-del">${esc(t("common.delete"))}</button>` : ""}
        </div>
        <div class="modal-actions">
          <button class="btn neutral" id="tf-cancel">${esc(t("common.cancel"))}</button>
          <button class="btn" id="tf-save">${esc(t("tasks.folders.save"))}</button>
        </div>
      </div>
    </div>`;
  document.body.appendChild(ov);

  const $ = (id) => ov.querySelector("#" + id);
  const close = () => {
    document.removeEventListener("keydown", onKey);
    ov.remove();
  };
  const onKey = (e) => { if (e.key === "Escape") close(); };
  document.addEventListener("keydown", onKey);
  ov.addEventListener("mousedown", (e) => { if (e.target === ov) close(); });
  $("tf-x").addEventListener("click", close);
  $("tf-cancel").addEventListener("click", close);

  let cat = { fields: [], options: {} };

  // sel builds a <select> from [{value,label}] with one entry marked.
  const sel = (id, cls, items, current, extra) =>
    `<select class="${cls}" ${id ? `data-role="${id}"` : ""} ${extra || ""}>` +
    items.map((o) =>
      `<option value="${esc(o.value)}"${String(o.value) === String(current) ? " selected" : ""}>${esc(o.label)}</option>`
    ).join("") + `</select>`;

  // valueControl draws the third control of a row: whichever one this operator's
  // value shape calls for, filled from the server's list where there is one.
  function valueControl(cond) {
    const field = fieldById(cat, cond.field);
    const op = opById(field, cond.op);
    const list = optionsFor(cat, field).map((o) => ({ value: o.value, label: optionLabel(o) }));
    switch (op && op.value) {
      case SHAPE.none:
        return `<span class="tf-hint">${esc(t("tasks.folders.noValue"))}</span>`;
      case SHAPE.choice:
        return sel("value", "tf-sel tf-grow", list, cond.value);
      case SHAPE.choices:
        return `<select class="tf-sel tf-grow" data-role="values" multiple size="4">` +
          list.map((o) =>
            `<option value="${esc(o.value)}"${(cond.values || []).includes(o.value) ? " selected" : ""}>${esc(o.label)}</option>`
          ).join("") + `</select>`;
      case SHAPE.text:
        return `<input class="tf-in tf-grow" data-role="value" type="text" spellcheck="false" value="${esc(cond.value || "")}" />`;
      case SHAPE.number:
        return sel("value", "tf-sel tf-narrow",
          PRIORITY_PRESETS.map((p) => ({ value: p.value, label: t(p.key) })), cond.value) +
          `<input class="tf-in tf-narrow" data-role="value" type="number" min="0" max="100" value="${esc(cond.value || "50")}" />`;
      case SHAPE.duration:
        return sel("value", "tf-sel tf-grow",
          WITHIN_PRESETS.map((d) => ({ value: d, label: t("tasks.folders.within." + d) })), cond.value);
      case SHAPE.count:
        return `<input class="tf-in tf-narrow" data-role="value" type="number" min="1" value="${esc(cond.value || "1")}" />` +
          sel("unit", "tf-sel tf-narrow", UNITS.map((u) => ({ value: u, label: t("tasks.folders.unit." + u) })), cond.unit);
      default:
        return "";
    }
  }

  function renderVisibility() {
    const items = [{ value: "private", label: t("tasks.folders.visibility.private") }];
    const groups = (cat.options && cat.options.myGroups) || [];
    for (const g of groups) {
      items.push({ value: "group:" + g.value, label: t("tasks.folders.visibility.group", { name: g.label || g.value }) });
    }
    // A folder can be shared with a group the editor is no longer in — someone
    // else's, or one this account has left. Without an option for it the control
    // would fall back to the first entry and quietly un-share the folder on save.
    if (draft.visibility === "group" && draft.groupId && !groups.some((g) => g.value === draft.groupId)) {
      items.push({ value: "group:" + draft.groupId, label: t("tasks.folders.visibility.group", { name: draft.groupId }) });
    }
    items.push({ value: "org", label: t("tasks.folders.visibility.org") });
    const current = draft.visibility === "group" ? "group:" + draft.groupId : draft.visibility;
    const el = $("tf-vis");
    el.innerHTML = items.map((o) =>
      `<option value="${esc(o.value)}"${o.value === current ? " selected" : ""}>${esc(o.label)}</option>`).join("");
    el.onchange = () => {
      const v = el.value;
      if (v.startsWith("group:")) { draft.visibility = "group"; draft.groupId = v.slice(6); }
      else { draft.visibility = v; draft.groupId = ""; }
    };
  }

  function renderConditions() {
    const host = $("tf-conds");
    const joiner = t("tasks.folders.joiner." + draft.rule.match);
    host.innerHTML = draft.rule.conditions.map((c, i) => {
      const field = fieldById(cat, c.field);
      const fieldSel = sel("field", "tf-sel", (cat.fields || []).map((f) =>
        ({ value: f.id, label: t("tasks.folders.field." + f.id) })), c.field);
      const opSel = sel("op", "tf-sel", ((field && field.ops) || []).map((o) =>
        ({ value: o.id, label: t("tasks.folders.op." + o.id) })), c.op);
      return (i > 0 ? `<div class="tf-joiner">${esc(joiner)}</div>` : "") +
        `<div class="tf-cond" data-i="${i}">${fieldSel}${opSel}` +
        `<div class="tf-val">${valueControl(c)}</div>` +
        `<button class="tf-del" data-role="del" title="${esc(t("tasks.folders.removeCondition"))}" aria-label="${esc(t("tasks.folders.removeCondition"))}">×</button></div>`;
    }).join("");

    host.querySelectorAll(".tf-cond").forEach((row) => {
      const i = Number(row.dataset.i);
      const c = draft.rule.conditions[i];
      const q = (role) => row.querySelector(`[data-role="${role}"]`);
      q("field").addEventListener("change", (e) => {
        draft.rule.conditions[i] = defaultCondition(cat, e.target.value);
        redraw();
      });
      q("op").addEventListener("change", (e) => {
        const field = fieldById(cat, c.field);
        const next = defaultCondition(cat, c.field);
        next.op = e.target.value;
        // Carry the value across when the new operator wants the same shape, so
        // switching "is" to "is not" does not silently reset the choice.
        const before = opById(field, c.op), after = opById(field, e.target.value);
        if (before && after && before.value === after.value) {
          next.value = c.value; next.values = c.values; next.unit = c.unit;
        } else if (after && after.value === SHAPE.choices && c.value) {
          next.values = [c.value];
        } else if (after && after.value === SHAPE.choice && (c.values || []).length) {
          next.value = c.values[0];
        }
        draft.rule.conditions[i] = next;
        redraw();
      });
      row.querySelector('[data-role="del"]').addEventListener("click", () => {
        draft.rule.conditions.splice(i, 1);
        redraw();
      });
      const multi = q("values");
      if (multi) multi.addEventListener("change", (e) => {
        c.values = [...e.target.selectedOptions].map((o) => o.value);
        preview();
      });
      row.querySelectorAll('[data-role="value"]').forEach((el) => {
        // A text or number field must not be redrawn while it is being typed into,
        // or it loses the caret after every character. Only the preview follows.
        if (el.tagName === "INPUT") {
          el.addEventListener("input", (e) => { c.value = e.target.value; preview(); });
        } else {
          el.addEventListener("change", (e) => { c.value = e.target.value; redraw(); });
        }
      });
      const unit = q("unit");
      if (unit) unit.addEventListener("change", (e) => { c.unit = e.target.value; preview(); });
    });
  }

  // preview asks the server what the rule in the dialog would select. Debounced,
  // because it is called on every keystroke in a text condition, and sequenced by
  // a token so a slow answer cannot overwrite a newer one.
  let previewTimer = null;
  let previewSeq = 0;
  function preview() {
    clearTimeout(previewTimer);
    previewTimer = setTimeout(runPreview, 220);
  }
  async function runPreview() {
    const seq = ++previewSeq;
    const countEl = $("tf-count"), totalEl = $("tf-total"), feelEl = $("tf-feel");
    if (!countEl) return;
    countEl.textContent = t("tasks.folders.countPending");
    try {
      const out = await api("POST", "/api/v1/task-folders/preview" + (me ? "?me=" + encodeURIComponent(me) : ""),
        { rule: draft.rule });
      if (seq !== previewSeq || !$("tf-count")) return;
      feelEl.textContent = out.feel || "";
      if (!out.ok) {
        countEl.textContent = t("tasks.folders.incomplete");
        countEl.classList.add("tf-incomplete");
        totalEl.textContent = "";
        return;
      }
      countEl.classList.remove("tf-incomplete");
      countEl.textContent = plural("tasks.folders.matchCount", out.matched);
      totalEl.textContent = t(out.truncated ? "tasks.folders.ofTotalMore" : "tasks.folders.ofTotal", { n: out.total });
    } catch (err) {
      if (seq !== previewSeq || !$("tf-count")) return;
      countEl.textContent = "—";
      totalEl.textContent = "";
      feelEl.textContent = err.message;
    }
  }

  function redraw() {
    renderConditions();
    preview();
  }

  $("tf-name").addEventListener("input", (e) => { draft.name = e.target.value; });
  $("tf-match").value = draft.rule.match || "all";
  $("tf-match").addEventListener("change", (e) => { draft.rule.match = e.target.value; redraw(); });
  $("tf-add").addEventListener("click", () => {
    const first = (cat.fields && cat.fields[0] && cat.fields[0].id) || "process";
    draft.rule.conditions.push(defaultCondition(cat, first));
    redraw();
  });

  $("tf-save").addEventListener("click", async () => {
    const btn = $("tf-save");
    btn.disabled = true;
    const body = {
      name: draft.name.trim(),
      visibility: draft.visibility,
      groupId: draft.groupId,
      rule: draft.rule,
    };
    try {
      const saved = editing
        ? await api("PUT", "/api/v1/task-folders/" + encodeURIComponent(folder.id), body)
        : await api("POST", "/api/v1/task-folders", body);
      toast(t("tasks.folders.saved"), "ok");
      close();
      if (onSaved) onSaved(saved);
    } catch (err) {
      toast(t("tasks.folders.saveFailed", { error: err.message }), "err");
      btn.disabled = false;
    }
  });

  const del = $("tf-del");
  if (del) del.addEventListener("click", async () => {
    if (!confirm(t("tasks.folders.confirmDelete", { name: folder.name }))) return;
    try {
      await api("DELETE", "/api/v1/task-folders/" + encodeURIComponent(folder.id));
      toast(t("tasks.folders.deleted"), "ok");
      close();
      if (onDeleted) onDeleted(folder);
    } catch (err) {
      toast(t("tasks.folders.deleteFailed", { error: err.message }), "err");
    }
  });

  // The catalogue arrives after the dialog, so the frame is on screen while the
  // listboxes fill rather than the dialog appearing only once the fetch returns.
  loadCatalogue(api).then((loaded) => {
    if (!document.body.contains(ov)) return;
    cat = loaded || { fields: [], options: {} };
    renderVisibility();
    if (!editing && draft.rule.conditions.length === 0) {
      draft.rule.conditions.push(defaultCondition(cat, (cat.fields[0] || {}).id || "process"));
    }
    redraw();
    $("tf-name").focus();
  }).catch((err) => {
    toast(t("tasks.folders.loadFailed", { error: err.message }), "err");
  });

  return { close };
}
