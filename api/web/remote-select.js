// A directory field: a form-js select whose options come from a remote data source
// (ADR-0473).
//
// The select stays what form-js makes of it — it holds the submitted value, its label,
// `required`, its description and its errors — and the field the vendored bundle renders
// for it (vendor/form-js/src/atlas/viewer.jsx) mounts the search this module provides
// into its own element: a box to type into, one bounded page of matches, a selection.
// Choosing an entry sets the select's value, so validation and submission remain
// form-js's own and the value a task completes with is the id, never the label.
//
// Nothing is loaded before somebody types: the search runs on the server, through the
// Worker the stored form names, and the browser only ever holds the page it is showing
// (and what "Load more" appended to it, bounded below). The request says what the
// person typed and what they picked in the fields the source is bound to; which Worker,
// which tenant and which properties is the server's to decide.
//
// Directory fields were first saved as textfields carrying the same block. Those are still
// read: attachRemoteSelects attaches the same search to such a textfield's DOM, and the
// form editor offers to convert one into the select it is now.

import { t, plural } from "./i18n.js";

export const DATA_SOURCE_MEMBER = "atlasDataSource";

// The search waits this long after the last keystroke, so a name typed at speed is one
// request rather than one per letter.
export const DEFAULT_DEBOUNCE_MS = 300;

// The most entries "Load more" accumulates before it asks for a narrower search. A list
// longer than this is not one anybody reads; the bound keeps the browser from holding
// a directory a page at a time.
export const MAX_SHOWN = 200;

const DEFAULT_MIN_SEARCH = 2;

let instances = 0;

// isDirectoryField reports whether a component is a directory field: a select whose
// options come from a remote data source, or a textfield carrying one — the shape such
// fields were saved in first.
export function isDirectoryField(c) {
  if (!c || typeof c !== "object" || typeof c.key !== "string" || !c.key) return false;
  const src = c[DATA_SOURCE_MEMBER];
  if (!src || typeof src !== "object") return false;
  if (c.type === "textfield") return true;
  return c.type === "select" && c.values === undefined && c.valuesKey === undefined && c.valuesExpression === undefined;
}

// isLegacyDirectoryField reports a directory field saved as a textfield.
export function isLegacyDirectoryField(c) {
  return isDirectoryField(c) && c.type === "textfield";
}

// directoryFields lists a schema's directory fields: key, label, type and the data source.
// The editor and the preview use it; the renderer reads the same thing from the form's
// own field registry.
export function directoryFields(schema) {
  const out = [];
  const walk = (comps) => {
    for (const c of comps || []) {
      if (!c || typeof c !== "object") continue;
      if (isDirectoryField(c)) {
        out.push({ key: c.key, id: c.id, type: c.type, label: c.label || c.key, source: c[DATA_SOURCE_MEMBER] });
      }
      if (Array.isArray(c.components)) walk(c.components);
    }
  };
  if (schema && Array.isArray(schema.components)) walk(schema.components);
  return out;
}

// taskTransport searches through a task: the server checks that the caller may work on
// it, reads the field's data source from the task's stored form, and asks the Worker.
export function taskTransport(taskKey, fetchImpl) {
  const doFetch = fetchImpl || ((...a) => fetch(...a));
  return async (field, body, signal) => {
    const res = await doFetch(
      "/api/v1/tasks/" + encodeURIComponent(taskKey) + "/form-data/" + encodeURIComponent(field) + "/query",
      { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body), signal });
    let data = null;
    try { data = await res.json(); } catch { /* not JSON: reported by status below */ }
    if (!res.ok) {
      const err = new Error((data && data.error) || "HTTP " + res.status);
      err.status = res.status;
      err.code = (data && data.code) || "";
      err.retryAfter = (data && data.retryAfter) || 0;
      throw err;
    }
    return { items: (data && data.items) || [], nextCursor: (data && data.nextCursor) || null };
  };
}

// PREVIEW_PEOPLE is the example directory the editor's preview searches. It is made up,
// and the preview says so: a design-time preview must never reach a production Worker.
const PREVIEW_DEPARTMENTS = ["IT", "Finance", "HR", "Sales"];
const PREVIEW_NAMES = [
  "Anna Müller", "Beat Schneider", "Claudia Weber", "Daniel Keller", "Eva Meier", "Fabian Huber",
  "Gabriela Steiner", "Hans Frei", "Ines Brunner", "Jonas Baumann", "Karin Fischer", "Lukas Gerber",
  "Marina Moser", "Nicolas Zimmermann", "Olivia Wyss", "Patrick Kälin", "Ramona Graf", "Stefan Roth",
  "Tanja Bühler", "Urs Suter", "Vera Lüthi", "Walter Hofer", "Yvonne Egger", "Zoé Marti",
];

export const PREVIEW_PEOPLE = PREVIEW_NAMES.flatMap((name, i) => {
  const [first, last] = name.split(" ");
  const ascii = (s) => s.normalize("NFKD").replace(/[̀-ͯ]/g, "").toLowerCase();
  return [{
    value: "00000000-0000-4000-8000-" + String(i + 1).padStart(12, "0"),
    label: name,
    description: ascii(first) + "." + ascii(last) + "@example.com",
    details: { department: PREVIEW_DEPARTMENTS[i % PREVIEW_DEPARTMENTS.length], jobTitle: "Beispiel" },
  }];
});

// previewTransport answers from the made-up directory above, with the server's paging
// and filtering rules, so the editor shows the field as it will behave.
export function previewTransport(people = PREVIEW_PEOPLE, pageSize = 25) {
  return async (_field, body) => {
    const fold = (s) => String(s || "").toLocaleLowerCase();
    const filters = body.filters || {};
    const holds = (p) => Object.entries(filters).every(([k, v]) => fold(p.details && p.details[k]) === fold(v));
    if (body.value) {
      return { items: people.filter((p) => p.value === body.value && holds(p)), nextCursor: null };
    }
    const term = fold(body.search).trim();
    const hits = people.filter((p) => holds(p) && (!term ||
      fold(p.label).startsWith(term) || fold(p.label).split(" ").some((w) => w.startsWith(term)) || fold(p.description).startsWith(term)));
    const size = Math.min(body.limit || pageSize, pageSize);
    const from = body.cursor ? Number(body.cursor) || 0 : 0;
    const page = hits.slice(from, from + size);
    return { items: page, nextCursor: from + size < hits.length ? String(from + size) : null };
  };
}

// remoteAdapter is the search a page gives the vendored form-js bundle, as the Form's
// `atlasRemote` option (or the Playground's atlas.remote): every select whose options
// come from a remote data source mounts it into its own element. opts: { transport,
// debounceMs, preview, data, timers }.
//
// mount(host, ctx) is called by the field. ctx says what the field is and how to reach
// form-js: { field, form, inputId, describedBy, getValue, setValue, onBlur, onFocus }.
// It returns { update({ value, disabled, readonly }), destroy() }; the field calls update
// after every render, so a value form-js sets — an import, a reset — is shown too.
export function remoteAdapter(opts = {}) {
  return {
    mount(host, ctx) {
      const field = ctx.field;
      const form = ctx.form || null;
      const io = {
        inputId: ctx.inputId,
        describedBy: ctx.describedBy,
        getValue: () => toValue(ctx.getValue()),
        setValue: (v) => ctx.setValue(v || null),
        getData: () => {
          try { return { ...form._getState().data }; } catch { return { ...(opts.data || {}) }; }
        },
        labelOf: (key) => labelIn(form, key),
        onBlur: () => { if (ctx.onBlur) ctx.onBlur(); },
        onFocus: () => { if (ctx.onFocus) ctx.onFocus(); },
      };
      const c = new Controller(field, opts, io);
      host.appendChild(c.root);
      const onChanged = (state) => c.formChanged((state && state.data) || io.getData());
      if (form && typeof form.on === "function") form.on("changed", onChanged);
      c.start();
      return {
        update(state) { c.update(state || {}); },
        destroy() {
          try { if (form && typeof form.off === "function") form.off("changed", onChanged); } catch { /* the form is gone already */ }
          c.destroy();
        },
      };
    },
  };
}

// attachRemoteSelects attaches a search to every directory field a mounted form holds as
// a textfield — the shape such fields were saved in first. A form whose directory fields
// are selects needs none of it: the bundle mounts remoteAdapter itself. It returns a handle
// whose destroy() detaches them; call it before the form is destroyed. opts: { transport,
// debounceMs, preview, data, timers }.
export function attachRemoteSelects(form, host, opts = {}) {
  let reg = null;
  try { reg = form.get("formFieldRegistry"); } catch { /* not a form-js Form */ }
  const fields = reg ? reg.getAll().filter(isLegacyDirectoryField) : [];
  const controllers = fields.map((f) => new TextfieldAttachment(form, host, f, opts));
  if (!controllers.length) return { destroy() {}, fields: 0 };
  // form-js re-renders its own DOM; a field it re-creates loses what was attached to it.
  // Re-attaching on every change of the form's subtree is cheap — it is a check unless
  // something actually moved — and keeps the search across a conditional or a re-render.
  const observer = new MutationObserver(() => controllers.forEach((a) => a.ensureMounted()));
  observer.observe(host, { childList: true, subtree: true });
  const onChanged = (state) => controllers.forEach((a) => a.c.formChanged((state && state.data) || {}));
  form.on("changed", onChanged);
  controllers.forEach((a) => { a.ensureMounted(); a.c.start(); });
  return {
    fields: controllers.length,
    destroy() {
      observer.disconnect();
      try { form.off("changed", onChanged); } catch { /* the form is gone already */ }
      controllers.forEach((a) => a.c.destroy());
    },
  };
}

function toValue(v) { return v == null ? "" : String(v); }

function labelIn(form, key) {
  try {
    const reg = form.get("formFieldRegistry");
    const f = reg.getAll().find((x) => x && x.key === key);
    if (f && f.label) return f.label;
  } catch { /* fall back to the key */ }
  return key;
}

// TextfieldAttachment puts the search beside a textfield form-js renders, and holds the
// value in that textfield.
class TextfieldAttachment {
  constructor(form, host, field, opts) {
    this.form = form;
    this.host = host;
    this.field = field;
    this.c = new Controller(field, opts, {
      getValue: () => { const el = this.carrier(); return el ? el.value || "" : ""; },
      setValue: (v) => this.writeCarrier(v),
      getData: () => {
        try { return { ...form._getState().data }; } catch { return { ...this.c.data }; }
      },
      labelOf: (key) => labelIn(form, key),
    });
  }

  carrier() {
    const suffix = "-" + this.field.id;
    for (const el of this.host.querySelectorAll("input.fjs-input")) {
      if (el !== this.c.input && el.id && el.id.endsWith(suffix)) return el;
    }
    return null;
  }

  ensureMounted() {
    const input = this.carrier();
    if (!input) return;
    const root = this.c.root;
    const wrapper = input.closest(".fjs-form-field");
    const group = input.closest(".fjs-input-group") || input;
    if (!wrapper) return;
    if (root.isConnected && root.parentElement === wrapper && root.previousElementSibling === group) return;
    wrapper.classList.add("atlas-rs-host");
    group.after(root);
    const label = wrapper.querySelector("label.fjs-form-field-label");
    if (label) label.htmlFor = this.c.input.id;
    const describedBy = input.getAttribute("aria-describedby");
    if (describedBy) this.c.input.setAttribute("aria-describedby", describedBy);
    this.c.setReadOnly(input.disabled || input.readOnly);
  }

  // writeCarrier puts a value into the textfield form-js owns, the way typing would:
  // the input event is what form-js listens for, and leaving the field flushes the
  // debounce it puts on a textfield, so the value is in the form's data the moment it
  // is chosen. The bundled Preact listens for focusout, not blur — a blur event reaches
  // no handler and the value would sit in the debounce until its timer ran.
  writeCarrier(value) {
    const input = this.carrier();
    if (!input) return;
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value").set;
    setter.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
    input.dispatchEvent(new FocusEvent("focusout", { bubbles: true }));
  }
}

// Controller is one directory field's search. Where its value lives, and what the rest of
// the form holds, it asks io: { getValue, setValue, getData, labelOf, inputId?,
// describedBy?, onBlur?, onFocus? }.
class Controller {
  constructor(field, opts, io) {
    this.field = field;
    this.io = io;
    this.src = field[DATA_SOURCE_MEMBER];
    this.transport = opts.transport || (async () => { throw Object.assign(new Error("no transport"), { code: "worker_unavailable" }); });
    this.debounceMs = Number.isFinite(opts.debounceMs) ? opts.debounceMs : DEFAULT_DEBOUNCE_MS;
    // The debounce's timer, injectable so a test can say when it runs out rather than
    // wait for it.
    this.setTimer = (opts.timers && opts.timers.set) || ((fn, ms) => setTimeout(fn, ms));
    this.clearTimer = (opts.timers && opts.timers.clear) || ((id) => clearTimeout(id));
    this.preview = !!opts.preview;
    this.data = { ...(opts.data || {}) };
    this.uid = "atlas-rs-" + (++instances);
    this.seq = 0;
    this.ctrl = null;
    this.resolveCtrl = null;
    this.timer = null;
    this.items = [];
    this.cursor = null;
    this.active = -1;
    this.open = false;
    this.selected = null;
    this.readOnly = false;
    this.filterKey = JSON.stringify(this.filtersOf(this.data));
    this.build();
  }

  minLength() {
    const m = this.src.minSearchLength;
    return Number.isInteger(m) && m >= 0 ? m : DEFAULT_MIN_SEARCH;
  }

  bound() { return (this.src.filters || []).filter((f) => f && f.field); }

  // filtersOf is what the request may say about the other fields: the value of each
  // field the source is bound to, and nothing else.
  filtersOf(data) {
    const out = {};
    for (const f of this.bound()) {
      const v = data[f.field];
      if (v != null && String(v).trim() !== "") out[f.property] = String(v).trim();
    }
    return out;
  }

  missingFilter(data) {
    for (const f of this.bound()) {
      const v = data[f.field];
      if (f.required && (v == null || String(v).trim() === "")) return this.labelOf(f.field);
    }
    return "";
  }

  labelOf(key) { return this.io.labelOf(key); }

  readData() {
    try { return this.io.getData(); } catch { return { ...this.data }; }
  }

  // ---------- DOM -------------------------------------------------------------

  build() {
    const root = document.createElement("div");
    root.className = "atlas-rs";
    root.dataset.field = this.field.key;
    const label = this.field.label || this.field.key;
    root.innerHTML = `
      <div class="atlas-rs-preview" hidden></div>
      <div class="atlas-rs-selected" hidden>
        <div class="atlas-rs-selected-text">
          <span class="atlas-rs-selected-label"></span>
          <span class="atlas-rs-selected-desc"></span>
        </div>
        <button type="button" class="atlas-rs-clear"></button>
      </div>
      <input type="search" class="fjs-input atlas-rs-search" autocomplete="off" spellcheck="false"
        role="combobox" aria-autocomplete="list" aria-expanded="false" />
      <div class="atlas-rs-status" role="status" aria-live="polite"></div>
      <ul class="atlas-rs-list" role="listbox" hidden></ul>
      <button type="button" class="atlas-rs-more" hidden></button>`;
    this.root = root;
    this.input = root.querySelector(".atlas-rs-search");
    this.list = root.querySelector(".atlas-rs-list");
    this.status = root.querySelector(".atlas-rs-status");
    this.more = root.querySelector(".atlas-rs-more");
    this.chip = root.querySelector(".atlas-rs-selected");
    this.clearBtn = root.querySelector(".atlas-rs-clear");
    // The field's own label names the box when the field gives it its id — a select's
    // label is for that id. A textfield's label is pointed at it once it is in place.
    this.input.id = this.io.inputId || this.uid + "-search";
    if (this.io.describedBy) this.input.setAttribute("aria-describedby", this.io.describedBy);
    if (!this.io.inputId) this.input.setAttribute("aria-label", label);
    this.list.id = this.uid + "-list";
    this.input.setAttribute("aria-controls", this.list.id);
    this.input.placeholder = t("remote.placeholder");
    this.list.setAttribute("aria-label", t("remote.results"));
    this.more.textContent = t("remote.more");
    this.clearBtn.textContent = "✕";
    this.clearBtn.title = t("remote.clear");
    this.clearBtn.setAttribute("aria-label", t("remote.clear"));
    if (this.preview) {
      const banner = root.querySelector(".atlas-rs-preview");
      banner.hidden = false;
      banner.textContent = t("remote.preview");
    }

    this.input.addEventListener("input", () => this.schedule());
    this.input.addEventListener("keydown", (e) => this.onKey(e));
    this.input.addEventListener("blur", () => {
      this.blurTimer = setTimeout(() => this.close(), 150);
      if (this.io.onBlur) this.io.onBlur();
    });
    this.input.addEventListener("focus", () => {
      clearTimeout(this.blurTimer);
      if (this.items.length) this.show();
      if (this.io.onFocus) this.io.onFocus();
    });
    this.list.addEventListener("mousedown", (e) => e.preventDefault()); // keep focus in the box
    this.list.addEventListener("click", (e) => {
      const li = e.target.closest("li[data-index]");
      if (li) this.choose(Number(li.dataset.index));
    });
    this.more.addEventListener("click", () => this.run(true));
    this.clearBtn.addEventListener("click", () => { this.select(null); this.input.focus(); });
  }

  // setReadOnly shows the choice without the means to change it: a task opened to look
  // at, a field form-js renders disabled.
  setReadOnly(on) {
    this.readOnly = !!on;
    this.input.hidden = this.readOnly;
    this.clearBtn.hidden = this.readOnly;
    if (this.readOnly) this.close();
  }

  start() {
    const current = this.io.getValue();
    if (current) this.resolve(current);
    // A field that searches without a term lists its first page once its filters
    // allow; every other field asks nothing until somebody types.
    if (this.readOnly) return;
    if (this.minLength() === 0 && !this.missingFilter(this.readData())) this.schedule();
    else if (!current) this.hint();
  }

  // update follows what form-js renders: whether the field may be changed, and the value
  // it holds — which is the one chosen here, unless form-js set another.
  update({ value, disabled, readonly } = {}) {
    const ro = !!(disabled || readonly);
    if (ro !== this.readOnly) this.setReadOnly(ro);
    const v = toValue(value);
    const shown = this.selected ? this.selected.value : "";
    if (v === shown) return;
    if (!v) {
      this.selected = null;
      this.renderChip();
      this.renderList();
    } else {
      this.resolve(v);
    }
  }

  destroy() {
    this.clearTimer(this.timer);
    clearTimeout(this.blurTimer);
    if (this.ctrl) this.ctrl.abort();
    if (this.resolveCtrl) this.resolveCtrl.abort();
    this.seq++;
    this.root.remove();
  }

  // ---------- the value -------------------------------------------------------

  select(item, announce) {
    this.selected = item;
    this.io.setValue(item ? item.value : "");
    this.renderChip();
    if (announce) this.say(announce);
    else if (item) this.say(t("remote.selected", { label: item.label }));
  }

  renderChip() {
    const item = this.selected;
    this.chip.hidden = !item;
    if (!item) return;
    this.chip.classList.toggle("atlas-rs-unknown", !!item.unknown);
    this.chip.querySelector(".atlas-rs-selected-label").textContent = item.label;
    const parts = [item.description, ...Object.values(item.details || {})].filter(Boolean);
    this.chip.querySelector(".atlas-rs-selected-desc").textContent = parts.join(" · ");
  }

  // resolve shows what a value the form already holds stands for — a task reopened
  // after a choice was made — by asking the source for that one entry.
  async resolve(value) {
    this.selected = { value, label: value, description: t("remote.resolving") };
    this.renderChip();
    if (this.resolveCtrl) this.resolveCtrl.abort();
    const ctrl = this.resolveCtrl = new AbortController();
    try {
      const res = await this.transport(this.field.key, { value, filters: this.filtersOf(this.readData()) }, ctrl.signal);
      if (ctrl.signal.aborted || !this.selected || this.selected.value !== value) return;
      const hit = (res.items || []).find((i) => i.value === value);
      this.selected = hit || { value, label: value, description: t("remote.unknownValue"), unknown: true };
    } catch (e) {
      if (e && e.name === "AbortError") return;
      if (this.selected && this.selected.value === value) {
        this.selected = { value, label: value, description: this.errorText(e) };
      }
    }
    this.renderChip();
  }

  // formChanged follows the form: a change of a field the source is bound to makes the
  // results stale and may make the selection one the source would no longer offer.
  formChanged(data) {
    this.data = { ...data };
    const value = data[this.field.key];
    if ((value == null || value === "") && this.selected) {
      this.selected = null;
      this.renderChip();
    }
    const key = JSON.stringify(this.filtersOf(data));
    if (key === this.filterKey) return;
    this.filterKey = key;
    this.filtersChanged(data);
  }

  filtersChanged(data) {
    this.clearTimer(this.timer);
    if (this.ctrl) this.ctrl.abort();
    this.seq++;
    this.items = [];
    this.cursor = null;
    this.renderList();
    this.close();
    if (this.selected && !this.stillAdmitted(this.selected, data)) {
      this.select(null, t("remote.clearedByFilter", { field: this.bound().map((f) => this.labelOf(f.field)).join(", ") }));
    }
    if (this.input.value || this.minLength() === 0) this.schedule();
    else this.hint();
  }

  // stillAdmitted reports whether a selection still holds under the filters as they now
  // stand. It can only say yes when the entry shows the property a filter is on; one
  // that does not is cleared rather than trusted.
  stillAdmitted(item, data) {
    for (const f of this.bound()) {
      const v = data[f.field];
      if (v == null || String(v).trim() === "") {
        if (f.required) return false;
        continue;
      }
      const has = item.details && item.details[f.property];
      if (!has || String(has).toLocaleLowerCase() !== String(v).trim().toLocaleLowerCase()) return false;
    }
    return true;
  }

  // ---------- searching -------------------------------------------------------

  hint() {
    const data = this.readData();
    const missing = this.missingFilter(data);
    const term = this.input.value;
    if (missing) this.say(t("remote.chooseFirst", { field: missing }));
    else if (lengthOf(term) < this.minLength() && this.minLength() > 0) {
      this.say(lengthOf(term) === 0 ? "" : t("remote.minChars", { n: this.minLength() }));
    } else this.say("");
  }

  schedule() {
    this.clearTimer(this.timer);
    if (this.readOnly) return;
    const data = this.readData();
    const term = this.input.value;
    if (this.missingFilter(data) || lengthOf(term) < this.minLength() || (lengthOf(term) === 0 && this.minLength() > 0)) {
      if (this.ctrl) this.ctrl.abort();
      this.seq++;
      this.items = [];
      this.cursor = null;
      this.renderList();
      this.close();
      this.hint();
      return;
    }
    this.timer = this.setTimer(() => this.run(false), this.debounceMs);
  }

  // run asks for a page. Each request withdraws the one before it, and an answer that
  // arrives for anything but the latest request is ignored — a slow first answer must
  // not overwrite the results of the term the person has typed since.
  async run(append) {
    this.clearTimer(this.timer);
    const data = this.readData();
    const body = { search: normalize(this.input.value), filters: this.filtersOf(data) };
    if (append) {
      if (!this.cursor) return;
      body.cursor = this.cursor;
    }
    if (this.ctrl) this.ctrl.abort();
    const seq = ++this.seq;
    const ctrl = this.ctrl = new AbortController();
    this.busy(true);
    try {
      const res = await this.transport(this.field.key, body, ctrl.signal);
      if (seq !== this.seq || ctrl.signal.aborted) return;
      const fresh = res.items || [];
      this.items = append ? this.items.concat(fresh).slice(0, MAX_SHOWN) : fresh;
      this.cursor = this.items.length >= MAX_SHOWN ? null : (res.nextCursor || null);
      if (!append) this.active = this.items.length ? 0 : -1;
      this.renderList();
      if (this.items.length) this.show(); else this.close();
      this.sayCount(this.items.length >= MAX_SHOWN && res.nextCursor);
    } catch (e) {
      if ((e && e.name === "AbortError") || seq !== this.seq) return;
      this.items = [];
      this.cursor = null;
      this.renderList();
      this.close();
      this.say(this.errorText(e), true);
    } finally {
      if (seq === this.seq) this.busy(false);
    }
  }

  busy(on) {
    this.root.classList.toggle("atlas-rs-busy", on);
    this.input.setAttribute("aria-busy", on ? "true" : "false");
    if (on) this.say(t("remote.loading"));
  }

  sayCount(capped) {
    const n = this.items.length;
    if (!n) { this.say(t("remote.empty")); return; }
    let s = plural("remote.count", n);
    if (this.cursor) s += " " + t("remote.countMore");
    if (capped) s += " " + t("remote.refine");
    this.say(s);
  }

  say(text, isError) {
    this.status.textContent = text || "";
    this.status.classList.toggle("atlas-rs-error", !!isError);
    this.status.setAttribute("role", isError ? "alert" : "status");
  }

  errorText(e) {
    const code = (e && e.code) || "";
    const key = "remote.error." + code;
    const known = code && t(key) !== key;
    const msg = known ? t(key) : t("remote.error.generic");
    return e && e.retryAfter ? msg + " " + t("remote.retryAfter", { n: e.retryAfter }) : msg;
  }

  // ---------- the list --------------------------------------------------------

  renderList() {
    this.list.textContent = "";
    this.items.forEach((item, i) => {
      const li = document.createElement("li");
      li.id = this.uid + "-opt-" + i;
      li.setAttribute("role", "option");
      li.dataset.index = String(i);
      li.dataset.value = item.value;
      li.setAttribute("aria-selected", i === this.active ? "true" : "false");
      if (this.selected && this.selected.value === item.value) li.classList.add("atlas-rs-chosen");
      const label = document.createElement("span");
      label.className = "atlas-rs-label";
      label.textContent = item.label;
      li.appendChild(label);
      if (item.description) {
        const d = document.createElement("span");
        d.className = "atlas-rs-desc";
        d.textContent = item.description;
        li.appendChild(d);
      }
      const details = Object.values(item.details || {}).filter(Boolean);
      if (details.length) {
        const d = document.createElement("span");
        d.className = "atlas-rs-details";
        d.textContent = details.join(" · ");
        li.appendChild(d);
      }
      li.addEventListener("mouseenter", () => this.setActive(i));
      this.list.appendChild(li);
    });
    this.more.hidden = !this.cursor;
    this.updateActive();
  }

  show() {
    this.open = true;
    this.list.hidden = false;
    this.input.setAttribute("aria-expanded", "true");
  }

  close() {
    this.open = false;
    this.list.hidden = true;
    this.input.setAttribute("aria-expanded", "false");
    this.input.removeAttribute("aria-activedescendant");
  }

  setActive(i) {
    this.active = i;
    this.updateActive();
  }

  updateActive() {
    this.list.querySelectorAll("li[role=option]").forEach((li) => {
      li.setAttribute("aria-selected", Number(li.dataset.index) === this.active ? "true" : "false");
    });
    const cur = this.active >= 0 ? this.list.querySelector(`#${CSS.escape(this.uid + "-opt-" + this.active)}`) : null;
    if (cur && this.open) {
      this.input.setAttribute("aria-activedescendant", cur.id);
      if (cur.scrollIntoView) cur.scrollIntoView({ block: "nearest" });
    } else this.input.removeAttribute("aria-activedescendant");
  }

  choose(i) {
    const item = this.items[i];
    if (!item) return;
    this.select(item);
    this.close();
    this.renderList();
  }

  onKey(e) {
    const n = this.items.length;
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        if (!n) return;
        if (!this.open) this.show();
        this.setActive(Math.min(this.active + 1, n - 1));
        break;
      case "ArrowUp":
        e.preventDefault();
        if (!n) return;
        if (!this.open) this.show();
        this.setActive(Math.max(this.active - 1, 0));
        break;
      case "Home":
        if (this.open && n) { e.preventDefault(); this.setActive(0); }
        break;
      case "End":
        if (this.open && n) { e.preventDefault(); this.setActive(n - 1); }
        break;
      case "Enter":
        // Never the form's implicit submission: choosing a person is not completing
        // the task.
        e.preventDefault();
        if (this.open && this.active >= 0) this.choose(this.active);
        else if (!this.missingFilter(this.readData()) && lengthOf(this.input.value) >= this.minLength()) this.run(false);
        break;
      case "Escape":
        if (this.open) {
          e.preventDefault();
          e.stopPropagation();
          this.close();
        }
        break;
      case "Tab":
        this.close();
        break;
    }
  }
}

function normalize(s) { return String(s || "").trim().split(/\s+/).filter(Boolean).join(" "); }
function lengthOf(s) { return [...normalize(s)].length; }
